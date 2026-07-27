package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveFoundationTool lưu thiết lập nền (premise/outline/characters), dành riêng cho Kiến trúc sư.
type SaveFoundationTool struct {
	store *store.Store
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "Lưu thiết lập nền của tiểu thuyết (premise/outline/characters/world_rules/compass v.v.). **Đây là điểm vào lưu trữ duy nhất**: nội dung không được lưu qua công cụ này sẽ không vào Store, chỉ xuất Markdown/JSON trong tin nhắn coi như mất. Tham số cố định là {type, content, scale?, volume?, arc?}. type có thể là premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book. Khi type là premise thì content phải là chuỗi Markdown; các type khác ưu tiên truyền trực tiếp mảng hoặc đối tượng JSON. expand_arc mở rộng chi tiết chương của cung truyện khung xương (cần volume + arc); append_volume thêm tập mới (content là VolumeOutline JSON đầy đủ, bao gồm cấu trúc cung truyện); update_compass cập nhật hướng kết thúc (content là StoryCompass JSON); complete_book thông báo toàn bộ cuốn sách hoàn thành (content truyền đối tượng rỗng {}, đẩy thẳng Phase=Complete; trước khi gọi phải vượt qua danh sách kiểm tra tập cuối, và không có hàng chờ làm lại). scale tùy chọn, chỉ cho phép short / mid / long."
}
func (t *SaveFoundationTool) Label() string { return "Lưu thiết lập" }

// Công cụ ghi (cập nhật chéo domain Outline/Progress/Characters), cấm chạy đồng thời.
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("loại thiết lập", "premise", "outline", "layered_outline", "characters", "world_rules", "expand_arc", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": "Nội dung. premise truyền chuỗi Markdown; các type khác truyền trực tiếp mảng hoặc đối tượng JSON, cũng tương thích truyền chuỗi JSON. Khi expand_arc thì truyền mảng chương.",
		}).Required(),
		schema.Property("scale", schema.Enum("mức quy hoạch", "short", "mid", "long")),
		schema.Property("volume", schema.Int("số thứ tự tập mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("arc", schema.Int("số thứ tự cung truyện mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("narrative", map[string]any{
			"description": "Tùy chọn. Hợp đồng tường thuật của toàn tác phẩm, khai một lần khi tạo nền (kèm chung lệnh gọi type=premise là tiện nhất). Đối tượng {pov, pov_characters, tense, notes}: pov ví dụ 'ngôi 1' / 'ngôi 3 hạn tri' / 'ngôi 3 toàn tri' / 'đa POV'; pov_characters là mảng tên nhân vật giữ góc nhìn; tense là 'quá khứ' hoặc 'hiện tại'; notes ghi quy tắc chuyển POV (ví dụ 'đổi POV chỉ tại ranh giới chương; mỗi chương 1 POV'). Bỏ trống = không ràng buộc ngôi kể (hành vi cũ).",
		}),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type      string          `json:"type"`
		Content   json.RawMessage `json:"content"`
		Scale     string          `json:"scale"`
		Volume    int             `json:"volume"`
		Arc       int             `json:"arc"`
		Narrative json.RawMessage `json:"narrative"` // hợp đồng tường thuật optional, độc lập với type
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("invalid scale %q, expected short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// Hợp đồng tường thuật đi kèm optional, độc lập với type: khai một lần (thường kèm premise),
	// persist ngay vào meta/narrative.json để mỗi chương tiêm được narrative_contract. Bỏ trống = giữ
	// hành vi cũ (không ràng buộc POV/thì) — foundation cũ không có trường này vẫn chạy bình thường.
	if saved, err := t.persistNarrative(a.Narrative); err != nil {
		return nil, err
	} else if saved {
		result["narrative_saved"] = true
	}

	// Giai đoạn viết cấm ghi đè toàn bộ đề cương, chỉ cho phép thao tác tăng dần (expand_arc / append_volume)
	if (a.Type == "outline" || a.Type == "layered_outline") && t.isWriting() {
		return nil, fmt.Errorf(
			"giai đoạn viết cấm dùng %s để ghi đè toàn bộ đề cương. Hãy dùng expand_arc để mở rộng cung truyện khung xương, hoặc append_volume để thêm tập mới: %w", a.Type, errs.ErrToolPrecondition)
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		name := domain.ExtractNovelNameFromPremise(content)
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("save premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if name != "" {
			_ = t.store.Progress.SetNovelName(name)
			result["novel_name"] = name
		}
		_ = t.store.Progress.UpdatePhase(domain.PhasePremise)

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
		}
		_ = t.store.Progress.UpdatePhase(domain.PhaseOutline)
		_ = t.store.Progress.SetTotalChapters(len(entries))
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			_ = t.store.Progress.SetLayered(false)
			_ = t.store.Progress.UpdateVolumeArc(0, 0)
			_ = t.store.Outline.ClearLayeredOutline()
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if len(volumes) == 0 {
			return nil, fmt.Errorf("layered_outline requires at least one volume: %w", errs.ErrToolArgs)
		}
		for _, vol := range volumes {
			if len(vol.Arcs) == 0 {
				return nil, fmt.Errorf("volume %d (%s) requires at least one arc: %w", vol.Index, vol.Title, errs.ErrToolArgs)
			}
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("save layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		flat := domain.FlattenOutline(volumes)
		if err := t.store.Outline.SaveOutline(flat); err != nil {
			return nil, fmt.Errorf("save flattened outline: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.TotalChapters(volumes)
		_ = t.store.Progress.UpdatePhase(domain.PhaseOutline)
		_ = t.store.Progress.SetTotalChapters(total)
		_ = t.store.Progress.SetLayered(true)
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			_ = t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index)
		}
		result["volumes"] = len(volumes)
		result["chapters"] = total

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("save characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("save world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var chapters []domain.OutlineEntry
		if err := decode("expand_arc chapters", &chapters); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, chapters); err != nil {
			return nil, fmt.Errorf("expand arc: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["chapters"] = len(chapters)

	case "append_volume":
		if p, _ := t.store.Progress.Load(); p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("toàn bộ cuốn sách đã hoàn thành (phase=complete), không cho phép thêm tập mới: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		if len(vol.Arcs) == 0 {
			return nil, fmt.Errorf("append_volume requires at least one arc in volume %d: %w", vol.Index, errs.ErrToolArgs)
		}
		if err := t.store.AppendVolume(vol); err != nil {
			return nil, fmt.Errorf("append volume: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = vol.Index
		result["arcs"] = len(vol.Arcs)
		chCount := 0
		for _, arc := range vol.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}

	case "complete_book":
		// Điểm vào duy nhất để hoàn thành toàn bộ cuốn sách: đẩy thẳng Phase=Complete.
		// Chỉ cho phép ở giai đoạn Writing, ngăn giai đoạn quy hoạch gọi nhầm bỏ qua toàn bộ quá trình viết.
		// Từ chối khi có hàng chờ làm lại — đảm bảo PendingRewrites chạy hết mới được kết thúc.
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book chỉ có thể gọi ở giai đoạn writing (phase hiện tại=%s): %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("còn %d chương trong hàng chờ làm lại, xử lý xong rồi mới gọi complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("mark complete: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		// Tầng công cụ bắt buộc ghi đè LastUpdated bằng số chương đã hoàn thành hiện tại, không tin LLM tự điền.
		// LLM thường quên điền hoặc để 0, khiến diag.CompassDrift báo sai, Router định tuyến lệch.
		if p, _ := t.store.Progress.Load(); p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("save compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated

	default:
		return nil, fmt.Errorf("unknown type %q, expected premise/outline/layered_outline/characters/world_rules/expand_arc/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// điểm khôi phục
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}

	// Trả về các mục chưa hoàn thành còn lại, hướng dẫn Kiến trúc sư tiếp tục hoặc kết thúc;
	// khi đủ đầy, đẩy phase sang writing một lần, tránh Điều phối viên phải quay lại giao việc.
	remaining := t.store.FoundationMissing()
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	if ready {
		if p, _ := t.store.Progress.Load(); p != nil &&
			p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
			_ = t.store.Progress.UpdatePhase(domain.PhaseWriting)
			result["phase"] = string(domain.PhaseWriting)
		}
	}
	return json.Marshal(result)
}

// persistNarrative giải mã tham số narrative (nếu có) và lưu hợp đồng tường thuật.
// Tương thích cả khi LLM truyền object trực tiếp lẫn khi bọc JSON thành chuỗi (dùng normalizeFoundationContent).
// Trả về saved=false khi tham số vắng, null hoặc rỗng — khi đó giữ nguyên hành vi cũ, không ghi gì.
func (t *SaveFoundationTool) persistNarrative(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, nil
	}
	text, err := normalizeFoundationContent(raw)
	if err != nil || text == "" || text == "null" {
		// Tham số rỗng/null: coi như không khai, bỏ qua êm (không phải lỗi).
		return false, nil
	}
	var nc domain.NarrativeContract
	if err := decodeFoundationJSON("narrative", text, &nc); err != nil {
		return false, err
	}
	if nc.IsEmpty() {
		return false, nil
	}
	if err := t.store.Outline.SaveNarrative(nc); err != nil {
		return false, fmt.Errorf("save narrative: %w: %w", errs.ErrStoreWrite, err)
	}
	return true, nil
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "expand_arc", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

// decodeFoundationJSON phân tích trường content của save_foundation, khi thất bại sẽ kèm vị trí dòng/cột
// và gợi ý sửa lỗi phổ biến nhất, giúp LLM lần thử lại có thể xác định trực tiếp thay vì đoán mò.
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `Nguyên nhân phổ biến: dấu ngoặc kép trong giá trị chuỗi chưa được escape thành \", xuống dòng chưa escape thành \n, hoặc thiếu dấu phẩy giữa các trường của đối tượng. Hãy sinh lại toàn bộ đoạn một lần nữa.`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("invalid content: expected Markdown string or valid JSON value: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

func (t *SaveFoundationTool) isWriting() bool {
	p, _ := t.store.Progress.Load()
	return p != nil && p.Phase == domain.PhaseWriting
}
