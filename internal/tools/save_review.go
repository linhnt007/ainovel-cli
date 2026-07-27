package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveReviewTool lưu kết quả rà soát của Biên tập viên.
type SaveReviewTool struct {
	store *store.Store
}

func NewSaveReviewTool(store *store.Store) *SaveReviewTool {
	return &SaveReviewTool{store: store}
}

func (t *SaveReviewTool) Name() string { return "save_review" }
func (t *SaveReviewTool) Description() string {
	return "Lưu kết quả rà soát và cập nhật trạng thái luồng. verdict là một trong accept/polish/rewrite. " +
		"Công cụ thực hiện cổng kiểm tra thẻ điểm nội bộ (có thể nâng cấp verdict), trực tiếp cập nhật flow và pending_rewrites của Progress. " +
		"Trả về dữ liệu thực tế có cấu trúc: final_verdict / affected_chapters / escalation_reason / next_flow / next_chapter"
}
func (t *SaveReviewTool) Label() string { return "Lưu rà soát" }

// Công cụ ghi (đồng thời cập nhật reviews/ và PendingRewrites/Flow của Progress), cấm chạy đồng thời.
func (t *SaveReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveReviewTool) Schema() map[string]any {
	issueSchema := schema.Object(
		schema.Property("type", schema.Enum("Chiều vấn đề", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("severity", schema.Enum("Mức độ nghiêm trọng", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("Mô tả vấn đề")).Required(),
		schema.Property("evidence", schema.String("Bằng chứng: đoạn trích nguyên văn, tình tiết cụ thể hoặc dữ liệu trạng thái")).Required(),
		schema.Property("suggestion", schema.String("Đề xuất chỉnh sửa")),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.Enum("Chiều", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("score", schema.Int("Điểm số (0-100)")).Required(),
		schema.Property("verdict", schema.Enum("Kết luận chiều (có thể bỏ qua: hệ thống tự suy luận theo score, ≥80 pass / ≥60 warning / <60 fail)", "pass", "warning", "fail")),
		schema.Property("comment", schema.String("Kết luận ngắn gọn cho chiều này; mỗi chiều bắt buộc điền, aesthetic phải trích dẫn nguyên văn hoặc số liệu thống kê cụ thể")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương được rà soát (rà soát toàn cục thì điền số chương mới nhất)")).Required(),
		schema.Property("scope", schema.Enum("Phạm vi rà soát", "chapter", "global", "arc")).Required(),
		schema.Property("dimensions", schema.Array("Điểm theo từng chiều (mỗi chiều một mục, bảy chiều)", dimensionSchema)).Required(),
		schema.Property("issues", schema.Array("Các vấn đề phát hiện được", issueSchema)).Required(),
		schema.Property("contract_status", schema.Enum("Mức độ hoàn thành hợp đồng chương", "met", "partial", "missed")),
		schema.Property("contract_misses", schema.Array("Các mục hợp đồng chưa hoàn thành hoặc vi phạm", schema.String(""))),
		schema.Property("contract_notes", schema.String("Ghi chú ngắn về tình trạng thực hiện hợp đồng")),
		schema.Property("verdict", schema.Enum("Kết luận rà soát", "accept", "polish", "rewrite")).Required(),
		schema.Property("summary", schema.String("Tóm tắt rà soát")).Required(),
		schema.Property("affected_chapters", schema.Array("Danh sách số chương cần viết lại hoặc trau chuốt (bắt buộc khi verdict là polish/rewrite)", schema.Int(""))),
	)
}

func (t *SaveReviewTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var r domain.ReviewEntry
	if err := json.Unmarshal(args, &r); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if r.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	// verdict là hàm thuần túy của score (≥80 pass / ≥60 warning / <60 fail), được suy luận xác định bởi code —
	// không để LLM cung cấp lại rồi kiểm tra tính nhất quán. Vừa loại bỏ dư thừa, vừa triệt tiêu
	// mâu thuẫn kiểu "score=85 nhưng lại cho warning".
	for i := range r.Dimensions {
		r.Dimensions[i].Verdict = expectedDimensionVerdict(r.Dimensions[i].Score)
	}
	if err := validateReviewEntry(r); err != nil {
		return nil, err
	}

	// Đọc review record trước của chương để mang theo trạng thái theo dõi vòng lặp:
	// rewrite_count (đếm dồn) và aesthetic_polished (đã polish vì thẩm mỹ chưa). File này bị
	// ghi đè mỗi lần review, nên phải đọc TRƯỚC khi SaveReview. Lỗi/thiếu file → coi như lần đầu.
	var priorRewriteCount int
	var priorAestheticPolished bool
	// Đọc prior theo ĐÚNG scope: record global ghi ở reviews/NN-global.json còn chapter/arc
	// ghi ở reviews/NN.json. Nếu luôn đọc NN.json, review scope=global sẽ thừa kế rewrite_count
	// của record chapter-scope cùng chương → dễ bị trần ép accept oan.
	if prior, err := t.store.World.LoadReviewScoped(r.Chapter, r.Scope); err == nil && prior != nil {
		priorRewriteCount = prior.RewriteCount
		priorAestheticPolished = prior.AestheticPolished
	}
	// aesthetic_polished mang theo từ lần trước; chỉ bật thêm (không bao giờ tắt) trong lần này.
	aestheticPolished := priorAestheticPolished

	// Cổng kiểm tra thẻ điểm — logic nâng cấp nội tuyến từ policy/review.go
	finalVerdict := r.Verdict
	var escalationReason string

	if r.Verdict == "accept" {
		// Kiểm tra trạng thái hợp đồng
		if r.ContractStatus == "missed" {
			finalVerdict = "rewrite"
			escalationReason = "Trạng thái thực hiện hợp đồng là missed, nâng cấp thành rewrite"
		} else if r.ContractStatus == "partial" {
			finalVerdict = "polish"
			escalationReason = "Trạng thái thực hiện hợp đồng là partial, nâng cấp thành polish"
		}
		// Cổng kiểm tra thẻ điểm
		if finalVerdict == "accept" {
			gate, triggeredAesthetic := evaluateScorecardGate(r.Dimensions, priorAestheticPolished)
			if gate != "" {
				if strings.Contains(gate, "rewrite") {
					finalVerdict = "rewrite"
				} else {
					finalVerdict = "polish"
				}
				escalationReason = gate
			}
			if triggeredAesthetic {
				aestheticPolished = true
			}
			// Ghi chú khi aesthetic vẫn dưới ngưỡng nhưng đã hết trần polish thẩm mỹ:
			// chấp nhận có chủ đích (không nâng verdict), để lại dấu vết trong escalation_reason.
			if finalVerdict == "accept" && priorAestheticPolished {
				if aes := findDimension(r.Dimensions, "aesthetic"); aes != nil && aes.Score < aestheticPolishThreshold {
					escalationReason = fmt.Sprintf(
						"aesthetic(%d) vẫn dưới ngưỡng %d nhưng chương đã polish thẩm mỹ 1 vòng → chấp nhận, ghi nợ giọng văn",
						aes.Score, aestheticPolishThreshold)
				}
			}
		}
	}

	// Trần rewrite/chương: nếu chương đã đi qua đủ maxRewritePerChapter vòng mà lần này verdict
	// vẫn là rewrite/polish → ép accept, gắn cờ quality_debt để không kẹt loop (xem hằng số trên).
	// Kiểm tra SAU khi gate đã nâng verdict (gate có thể biến accept→rewrite/polish).
	if (finalVerdict == "rewrite" || finalVerdict == "polish") && priorRewriteCount >= maxRewritePerChapter {
		r.QualityDebt = true
		r.QualityDebtReason = fmt.Sprintf(
			"đã đạt trần %d vòng rewrite/polish nhưng verdict vẫn là %s (%s) → ép accept, gánh nợ chất lượng",
			maxRewritePerChapter, finalVerdict, escalationReason)
		finalVerdict = "accept"
		escalationReason = r.QualityDebtReason
		// Chương không còn vào hàng đợi nữa → record/response phản ánh accept, không kèm chương ảnh hưởng.
		r.AffectedChapters = nil
	}

	// Ghi trạng thái theo dõi vào record trước khi lưu.
	r.AestheticPolished = aestheticPolished
	// rewrite_count chỉ tăng khi chương THỰC SỰ vào hàng đợi rewrite/polish lần này; nếu bị trần
	// ép về accept thì không tăng (chương không còn quay vòng nữa).
	r.RewriteCount = priorRewriteCount
	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		// polish (kể cả polish vì aesthetic) cũng tiêu 1 slot trong trần maxRewritePerChapter:
		// có chủ đích — mọi vòng quay lại đều đốt budget nên phải bị đếm chung, không double-jeopardy
		// (một verdict = tăng đúng 1, không phân biệt rewrite hay polish).
		r.RewriteCount = priorRewriteCount + 1
	}

	affected := r.AffectedChapters
	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		if len(affected) == 0 && r.Chapter > 0 {
			affected = []int{r.Chapter}
		}
		if err := t.store.Progress.ValidatePendingRewrites(affected); err != nil {
			return nil, fmt.Errorf("validate pending rewrites: %w", err)
		}
	}

	if err := t.store.World.SaveReview(r); err != nil {
		return nil, fmt.Errorf("save review: %w", err)
	}

	// Cập nhật Progress theo final verdict.
	// Nếu ghi thất bại phải trả về sớm — sau đó sẽ append checkpoint rà soát, nếu nuốt err ở đây
	// Điều phối viên sẽ thấy saved:true nhưng Store vẫn ở trạng thái trung gian với Flow cũ / thiếu PendingRewrites.
	progress, _ := t.store.Progress.Load()
	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		flow := domain.FlowRewriting
		if finalVerdict == "polish" {
			flow = domain.FlowPolishing
		}
		if err := t.store.Progress.SetPendingRewrites(affected, r.Summary); err != nil {
			return nil, fmt.Errorf("set pending rewrites: %w", err)
		}
		if err := t.store.Progress.SetFlow(flow); err != nil {
			return nil, fmt.Errorf("set flow %s: %w", flow, err)
		}
	} else {
		if err := t.store.Progress.SetFlow(domain.FlowWriting); err != nil {
			return nil, fmt.Errorf("set flow writing: %w", err)
		}
	}

	// Đọc snapshot Progress đã cập nhật làm dữ liệu thực tế
	latest, _ := t.store.Progress.Load()
	nextFlow := string(domain.FlowWriting)
	nextChapter := 0
	if latest != nil {
		nextFlow = string(latest.Flow)
		nextChapter = latest.NextChapter()
	}

	// Thêm điểm khôi phục
	scope := domain.ChapterScope(r.Chapter)
	if r.Scope == "arc" {
		vol, arc := 0, 0
		if progress != nil {
			vol, arc = progress.CurrentVolume, progress.CurrentArc
		}
		scope = domain.ArcScope(vol, arc)
	}
	artifact := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	if r.Scope == "global" {
		artifact = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, "review", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint review: %w", err)
	}

	return json.Marshal(map[string]any{
		"saved":              true,
		"chapter":            r.Chapter,
		"scope":              r.Scope,
		"verdict":            r.Verdict,
		"final_verdict":      finalVerdict,
		"escalation_reason":  escalationReason,
		"affected_chapters":  affected,
		"issues":             len(r.Issues),
		"next_flow":          nextFlow,
		"next_chapter":       nextChapter,
		"rewrite_count":      r.RewriteCount,
		"aesthetic_polished": r.AestheticPolished,
		"quality_debt":       r.QualityDebt,
	})
}

var expectedReviewDimensions = map[string]struct{}{
	"consistency": {},
	"character":   {},
	"pacing":      {},
	"continuity":  {},
	"foreshadow":  {},
	"hook":        {},
	"aesthetic":   {},
}

func validateReviewEntry(r domain.ReviewEntry) error {
	if strings.TrimSpace(r.Scope) == "" {
		return fmt.Errorf("scope is required")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("summary is required")
	}
	for _, issue := range r.Issues {
		if strings.TrimSpace(issue.Description) == "" {
			return fmt.Errorf("issue description is required")
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return fmt.Errorf("issue evidence is required")
		}
	}
	if err := validateDimensions(r.Dimensions); err != nil {
		return err
	}
	if (r.Verdict == "rewrite" || r.Verdict == "polish") && len(r.AffectedChapters) == 0 {
		return fmt.Errorf("affected_chapters is required when verdict=%s", r.Verdict)
	}
	return nil
}

func validateDimensions(dimensions []domain.DimensionScore) error {
	if len(dimensions) != len(expectedReviewDimensions) {
		return fmt.Errorf("dimensions must contain exactly %d entries", len(expectedReviewDimensions))
	}

	seen := make(map[string]struct{}, len(dimensions))
	for _, dim := range dimensions {
		if _, ok := expectedReviewDimensions[dim.Dimension]; !ok {
			return fmt.Errorf("unknown dimension: %s", dim.Dimension)
		}
		if _, ok := seen[dim.Dimension]; ok {
			return fmt.Errorf("duplicate dimension: %s", dim.Dimension)
		}
		seen[dim.Dimension] = struct{}{}
		if dim.Score < 0 || dim.Score > 100 {
			return fmt.Errorf("invalid score for %s: %d", dim.Dimension, dim.Score)
		}
		if strings.TrimSpace(dim.Comment) == "" {
			return fmt.Errorf("dimension comment is required: %s", dim.Dimension)
		}
	}
	return nil
}

func expectedDimensionVerdict(score int) string {
	switch {
	case score >= 80:
		return "pass"
	case score >= 60:
		return "warning"
	default:
		return "fail"
	}
}

// criticalDimensions định nghĩa các chiều quan trọng sẽ kích hoạt nâng cấp verdict.
var criticalDimensions = map[string]struct{}{
	"consistency": {},
	"character":   {},
	"continuity":  {},
}

// maxRewritePerChapter là trần số vòng rewrite/polish cho một chương.
// Lý do: bài học các chương ch204..347 — model có thể chấm thấp mãi khiến chương kẹt
// vòng lặp rewrite/polish vô hạn, đốt budget mà chất lượng không hội tụ. Sau đủ số vòng
// này, ép accept và gắn cờ quality_debt còn hơn kẹt loop (auto-accept chương dưới chuẩn
// có cờ theo dõi được, còn kẹt loop thì không ship được gì).
const maxRewritePerChapter = 3

// aestheticPolishThreshold: aesthetic < ngưỡng này kích hoạt đúng MỘT vòng polish.
// Không bao giờ nâng thành rewrite vì thẩm mỹ — rewrite dành cho lỗi critical
// (consistency/character/continuity), thẩm mỹ yếu chỉ đáng một vòng trau chuốt cục bộ.
const aestheticPolishThreshold = 70

// evaluateScorecardGate kiểm tra xem thẻ điểm có cần nâng cấp verdict không.
// Trả về chuỗi rỗng nghĩa là không nâng cấp. triggeredAesthetic=true khi việc nâng cấp
// (một phần) do aesthetic < ngưỡng — để caller đánh dấu aesthetic_polished, chặn polish
// lần thứ hai vì cùng lý do thẩm mỹ (trần 1 vòng).
// aestheticAlreadyPolished: chương này đã từng bị polish vì aesthetic hay chưa (đọc từ
// review record trước). Nếu rồi thì aesthetic thấp không còn nâng verdict nữa.
func evaluateScorecardGate(dimensions []domain.DimensionScore, aestheticAlreadyPolished bool) (string, bool) {
	var criticalFails []string
	var polishIssues []string

	for _, dim := range dimensions {
		// aesthetic có cổng riêng (ngưỡng 70 + trần 1 vòng) — xử lý tách khỏi vòng lặp chung,
		// nếu để chung thì warning aesthetic sẽ luôn nâng polish, phá vỡ trần 1 vòng.
		if dim.Dimension == "aesthetic" {
			continue
		}
		_, isCritical := criticalDimensions[dim.Dimension]
		if isCritical && (dim.Verdict == "fail" || dim.Score < 60) {
			criticalFails = append(criticalFails, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		} else if dim.Verdict == "warning" || (isCritical && dim.Score < 80) {
			polishIssues = append(polishIssues, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		}
	}

	if len(criticalFails) > 0 {
		return fmt.Sprintf("rewrite: chiều quan trọng không đạt chuẩn %v", criticalFails), false
	}

	// Cổng aesthetic: dưới ngưỡng thì nâng lên polish, nhưng chỉ đúng một lần mỗi chương.
	triggeredAesthetic := false
	if aes := findDimension(dimensions, "aesthetic"); aes != nil && aes.Score < aestheticPolishThreshold {
		if !aestheticAlreadyPolished {
			// Lần đầu aesthetic dưới ngưỡng → nâng polish và đánh dấu để lần sau không nâng lại.
			polishIssues = append(polishIssues, fmt.Sprintf("aesthetic(%d)", aes.Score))
			triggeredAesthetic = true
		}
		// Đã polish vì aesthetic một lần rồi: không thêm vào polishIssues → chấp nhận kèm ghi chú,
		// tránh đổi "ship văn xấu" lấy "kẹt loop".
	}

	if len(polishIssues) > 0 {
		return fmt.Sprintf("polish: một số chiều cần trau chuốt thêm %v", polishIssues), triggeredAesthetic
	}
	return "", false
}

// findDimension trả về con trỏ tới chiều chỉ định trong slice, nil nếu không có.
func findDimension(dimensions []domain.DimensionScore, name string) *domain.DimensionScore {
	for i := range dimensions {
		if dimensions[i].Dimension == name {
			return &dimensions[i]
		}
	}
	return nil
}
