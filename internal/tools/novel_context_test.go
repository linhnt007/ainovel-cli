package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestContextToolInjectsStyleStats(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	progress := &domain.Progress{TotalChapters: 10}
	body := "# Chương N\nAnh không phải do dự, mà là sợ hãi. Cả gian phòng im lặng, không ai nói gì.\nĐêm đã buông xuống.\nAnh bước đi."
	for ch := 1; ch <= 6; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter: %v", err)
		}
		progress.CompletedChapters = append(progress.CompletedChapters, ch)
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := NewContextTool(st, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 7})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Episodic map[string]json.RawMessage `json:"episodic_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	statsRaw, ok := payload.Episodic["style_stats"]
	if !ok {
		t.Fatalf("expected episodic_memory.style_stats, got keys %v", keysOf(payload.Episodic))
	}
	var stats struct {
		Chapters int `json:"chapters"`
		Patterns []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal(statsRaw, &stats); err != nil {
		t.Fatalf("Unmarshal stats: %v", err)
	}
	if stats.Chapters != 6 || len(stats.Patterns) == 0 {
		t.Errorf("stats content: %+v", stats)
	}
	if usage, ok := payload.Episodic["_usage"]; !ok || len(usage) == 0 {
		t.Error("expected episodic_memory._usage annotation")
	}
}

// Part L — foundation có narrative → working memory của Người viết chứa narrative_contract mỗi chương.
func TestContextToolInjectsNarrativeContractWhenDeclared(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveNarrative(domain.NarrativeContract{
		POV:           "ngôi 3 hạn tri",
		POVCharacters: []string{"Lâm Viễn"},
		Tense:         "quá khứ",
		Notes:         "đổi POV chỉ tại ranh giới chương",
	}); err != nil {
		t.Fatalf("SaveNarrative: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("missing working_memory")
	}
	nc, ok := working["narrative_contract"].(map[string]any)
	if !ok {
		t.Fatalf("expected working_memory.narrative_contract, got %T", working["narrative_contract"])
	}
	if nc["pov"] != "ngôi 3 hạn tri" || nc["tense"] != "quá khứ" {
		t.Fatalf("unexpected narrative_contract payload: %+v", nc)
	}
}

// Part L — foundation thiếu narrative → không có narrative_contract, không panic (tương thích ngược).
func TestContextToolOmitsNarrativeContractWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("missing working_memory")
	}
	if _, ok := working["narrative_contract"]; ok {
		t.Fatalf("expected no narrative_contract when foundation lacks narrative, got %+v", working["narrative_contract"])
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestContextToolReportsWarningsForCorruptedState(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write outline.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write progress.json: %v", err)
	}

	tool := NewContextTool(store, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Warnings []string `json:"_warnings"`
		Summary  string   `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Warnings) == 0 {
		t.Fatal("expected context warnings for corrupted files")
	}
	if !containsWarning(payload.Warnings, "outline") {
		t.Fatalf("expected outline warning, got %v", payload.Warnings)
	}
	if !containsWarning(payload.Warnings, "progress") {
		t.Fatalf("expected progress warning, got %v", payload.Warnings)
	}
	if !strings.Contains(payload.Summary, "cảnh-báo:") {
		t.Fatalf("expected loading summary to contain warning count, got %q", payload.Summary)
	}
}

func containsWarning(warnings []string, key string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, key) {
			return true
		}
	}
	return false
}

func TestContextToolChapterModeIncludesWorkingAndReferenceFields(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise(`## Thể loại và tông điệu
少年成长，偏紧张压迫。

## Định vị thể loại
少年升级流

## Xung đột cốt lõi
主角必须在宗门竞争中活下来。

## Mục tiêu nhân vật chính
进入内门。

## Hướng kết cục
成为真正的执棋者。

## Vùng cấm viết
不提前揭露师尊真相。

## Điểm bán khác biệt
弱者逆袭。

## Điểm móc khác biệt
每阶段都要用更高代价换成长。

## Cam kết thực hiện cốt lõi
持续兑现危机与突破。

## Động cơ truyện
试炼、资源争夺与身份升级共同推进。

## Bước ngoặt giữa chuyện
主角被迫转向另一条修行路线。
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "入门", CoreEvent: "主角进入宗门", Scenes: []string{"拜师", "立誓"}},
		{Chapter: 2, Title: "试炼", CoreEvent: "参加外门试炼", Scenes: []string{"集合", "出发"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "林砚", Role: "主角", Description: "少年修士", Arc: "成长", Traits: []string{"冷静"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "magic", Rule: "灵气可以炼化", Boundary: "凡人不可直接驾驭"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.Progress.Init("test", 2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "主角拜入宗门，确立目标。",
		Characters: []string{"林砚"},
		KeyEvents:  []string{"拜师"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "第一章正文结尾，留下试炼悬念。"); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "试炼",
		Goal:    "通过第一关",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"必须让主角通过第一关", "必须埋下内门试炼邀请"},
			ForbiddenMoves:   []string{"不能提前揭露师尊真实身份"},
			ContinuityChecks: []string{"主角左臂旧伤仍未痊愈"},
			EvaluationFocus:  []string{"重点检查试炼节奏是否拖沓"},
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"叙述保持克制"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := NewContextTool(s, References{
		Consistency:      "一致性检查",
		HookTechniques:   "钩子技巧",
		QualityChecklist: "质量清单",
	}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{
		"premise",
		"premise_sections",
		"premise_structure",
		"outline",
		"world_rules",
		"memory_policy",
		"planning_tier",
		"working_memory",
		"episodic_memory",
		"reference_pack",
		"current_chapter_outline",
		"recent_summaries",
		"chapter_plan",
		"chapter_contract",
		"previous_tail",
		"style_rules",
		"references",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in chapter context", key)
		}
	}
}

func TestContextToolArchitectModeIncludesPlanningAndFoundation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise(`## Thể loại và tông điệu
群像冒险，偏冷峻史诗。

## Định vị thể loại
群像长篇冒险

## Xung đột cốt lõi
众人必须在不断失控的旧秩序中寻找新秩序。

## Mục tiêu nhân vật chính
抵达真相核心。

## Hướng kết cục
揭开古老真相并重建秩序。

## Vùng cấm viết
不靠天降设定收尾。

## Điểm bán hàng khác biệt
群像关系推进。

## Điểm móc khác biệt
每卷都改变队伍关系结构。

## Cam kết thực hiện cốt lõi
持续提供发现、牺牲与选择。

## Động cơ truyện
旅途推进、真相调查与队伍关系共同驱动。

## Tuyến quan hệ/phát triển
队伍从互不信任走向分裂再重组。

## Lộ trình nâng cấp
从地方事件走向世界级危机。

## Bước ngoặt giữa chuyện
真相并非敌人，而是秩序本身有问题。

## Mệnh đề kết cục
秩序应由谁定义。
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "起点", CoreEvent: "旅途开始"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "沈曜", Role: "主角", Description: "流浪剑客", Arc: "寻找真相", Traits: []string{"敏锐"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "society", Rule: "城邦林立", Boundary: "皇权不可直辖边地"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1, Title: "第一卷", Theme: "踏上旅途",
			Arcs: []domain.ArcOutline{
				{Index: 1, Title: "启程", Goal: "建立队伍", Chapters: []domain.OutlineEntry{{Chapter: 1, Title: "起点"}}},
				{Index: 2, Title: "迷雾", Goal: "逼近秘密", EstimatedChapters: 5},
			},
		},
	}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "揭开古老真相",
		EstimatedScale:  "预计 3 卷",
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"保持冷峻节制"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := NewContextTool(s, References{
		OutlineTemplate:   "大纲模板",
		CharacterTemplate: "角色模板",
		LongformPlanning:  "长篇规划",
	}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{
		"memory_policy",
		"planning_tier",
		"planning_memory",
		"foundation_memory",
		"reference_pack",
		"premise_sections",
		"premise_structure",
		"characters",
		"layered_outline",
		"skeleton_arcs",
		"compass",
		"style_rules",
		"references",
		"foundation_status",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in architect context", key)
		}
	}
}

func TestTrimByBudgetRemovesMirroredMemoryKeys(t *testing.T) {
	result := map[string]any{
		"references": map[string]string{
			"a": strings.Repeat("x", 200),
			"b": strings.Repeat("y", 200),
		},
		"reference_pack": map[string]any{
			"references": map[string]string{
				"a": strings.Repeat("x", 200),
				"b": strings.Repeat("y", 200),
			},
			"style_rules": []string{"克制"},
		},
	}

	trimByBudget(result, 80)

	if _, ok := result["references"]; ok {
		t.Fatal("expected top-level references to be trimmed")
	}
	pack, ok := result["reference_pack"].(map[string]any)
	if !ok {
		t.Fatal("expected reference_pack to remain available")
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("expected mirrored references to be trimmed from reference_pack")
	}
}

func TestContextToolSelectedMemoryRecallsStoryThreadsAndReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "邀约", CoreEvent: "长老暗中给出内门试炼邀请", Scenes: []string{"密谈", "留下试炼令"}},
		{Chapter: 2, Title: "试炼前夜", CoreEvent: "林砚准备回应内门试炼邀请", Hook: "谁在背后推动这场试炼", Scenes: []string{"整理线索", "决定赴约"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "内门试炼邀请的真实目的", PlantedAt: 1, Status: "planted"},
		{ID: "trial_mastermind", Description: "谁在背后推动这场试炼", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "试炼规则碑文残卷", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "外门弟子的旧债纠纷", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "长老手中令牌的来历", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "山门背后的隐藏通道", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "试炼盘口的幕后操盘人", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "试炼前夜",
		Goal:    "决定是否回应邀请",
		Contract: domain.ChapterContract{
			PayoffPoints: []string{"回应内门试炼邀请"},
			HookGoal:     "抛出谁在背后推动试炼",
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter:        1,
		Scope:          "chapter",
		Verdict:        "polish",
		Summary:        "主线启动完成，但伏笔不够明确。",
		ContractStatus: "partial",
		ContractMisses: []string{"未明确埋下内门试炼邀请"},
		Issues: []domain.ConsistencyIssue{
			{Type: "hook", Severity: "warning", Description: "章末钩子不够具体"},
		},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads  []domain.RecallItem `json:"story_threads"`
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
		Summary string `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.StoryThreads) == 0 {
		t.Fatal("expected story thread recall items")
	}
	if len(payload.Selected.ReviewLessons) == 0 {
		t.Fatal("expected review lesson recall items")
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "内门试炼邀请") {
		t.Fatalf("expected story thread recall to mention invite, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "推动这场试炼") {
		t.Fatalf("expected story thread recall to mention trial mastermind, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "试炼规则碑文残卷") {
		t.Fatalf("expected weak-overlap foreshadow to stay out, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "建议回看第") {
		t.Fatalf("expected related_chapters not to be duplicated into story_threads, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "contract") {
		t.Fatalf("expected review lesson recall to mention contract miss, got %+v", payload.Selected.ReviewLessons)
	}
	if !strings.Contains(payload.Summary, "gợi-nhớ-tuyến-truyện:") || !strings.Contains(payload.Summary, "gợi-nhớ-đánh-giá:") {
		t.Fatalf("expected loading summary to report selected memory, got %q", payload.Summary)
	}
}

// Phục bút treo quá lâu chưa thu hồi, dù không liên quan đến từ khóa chương hiện tại,
// vẫn phải được bù vào story_threads theo tuổi tài khoản —
// đây chính là điểm mù của cơ chế gọi lại theo độ liên quan
// (những sợi treo riêng quá lâu nhưng không va chạm từ khóa ở chương này).
// Phục bút mới đặt gần đây (tuổi tài khoản < ngưỡng) không được gắn nhầm nhãn "chưa thu hồi".
func TestContextToolSelectedMemorySurfacesAgingForeshadow(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Chủ đề chương hiện tại không liên quan đến bất kỳ phục bút nào, đảm bảo gọi lại theo liên quan trả về rỗng, chỉ còn bù theo tuổi tài khoản có hiệu lực.
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 50, Title: "瘟疫", CoreEvent: "林砚在城南医馆救治瘟疫病患", Scenes: []string{"熬药", "封锁街巷"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 60); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// 6 mục đạt ngưỡng gọi lại; hai mục đầu có tuổi tài khoản ≥30 (treo lâu), bốn mục sau có tuổi tài khoản <30 (gần đây).
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "ancient_seal", Description: "上古封印的裂隙", PlantedAt: 3, Status: "planted"},
		{ID: "lost_bloodline", Description: "主角失落的血脉来历", PlantedAt: 5, Status: "advanced"},
		{ID: "market_feud", Description: "昨夜集市的口角", PlantedAt: 47, Status: "planted"},
		{ID: "rumor_a", Description: "近日传闻甲", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_b", Description: "近日传闻乙", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_c", Description: "近日传闻丙", PlantedAt: 49, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 50})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Hai phục bút treo lâu phải được bù vào, kèm chú thích tuổi tài khoản "未回收".
	if !containsRecallSummary(payload.Selected.StoryThreads, "上古封印的裂隙") {
		t.Fatalf("expected aging foreshadow to surface despite no relevance, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "失落的血脉") {
		t.Fatalf("expected second aging foreshadow to surface, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "chưa thu hồi") {
		t.Fatalf("expected aging item to carry overdue annotation, got %+v", payload.Selected.StoryThreads)
	}
	// Phục bút gần đây (tuổi tài khoản <30 và không liên quan) không được bù vào.
	if containsRecallSummary(payload.Selected.StoryThreads, "昨夜集市的口角") {
		t.Fatalf("recent foreshadow must not be labeled overdue, got %+v", payload.Selected.StoryThreads)
	}
}

func TestContextToolSelectedMemoryIncludesGlobalReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "开端", CoreEvent: "故事开始"},
		{Chapter: 2, Title: "推进", CoreEvent: "主线继续推进"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 6); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 1,
		Scope:   "global",
		Verdict: "polish",
		Summary: "全局推进合格，但角色目标表达还不够稳定。",
		Issues: []domain.ConsistencyIssue{
			{Type: "character", Severity: "warning", Description: "主角目标表达不够稳定"},
		},
	}); err != nil {
		t.Fatalf("SaveReview(global): %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "主角目标表达不够稳定") {
		t.Fatalf("expected global review lesson to be recalled, got %+v", payload.Selected.ReviewLessons)
	}
}

func TestContextToolKeepsFullForeshadowWhenRecallNotTriggered(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "起势", CoreEvent: "故事起势"},
		{Chapter: 2, Title: "推进", CoreEvent: "继续推进"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 4); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "small_1", Description: "第一条小伏笔", PlantedAt: 1, Status: "planted"},
		{ID: "small_2", Description: "第二条小伏笔", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger to remain when selected recall is not triggered")
	}
	if _, ok := payload["selected_memory"]; ok {
		t.Fatalf("expected no selected_memory for small foreshadow sets, got %+v", payload["selected_memory"])
	}
}

func TestContextToolFallsBackToFullForeshadowWhenSelectionIsTooSparse(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "邀约", CoreEvent: "长老暗中给出内门试炼邀请"},
		{Chapter: 2, Title: "试炼前夜", CoreEvent: "林砚准备回应内门试炼邀请", Scenes: []string{"整理线索", "决定赴约"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "内门试炼邀请的真实目的", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "试炼规则碑文残卷", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "外门弟子的旧债纠纷", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "长老手中令牌的来历", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "山门背后的隐藏通道", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "试炼盘口的幕后操盘人", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger when selection is too sparse")
	}
	if selected, ok := payload["selected_memory"].(map[string]any); ok {
		if _, exists := selected["story_threads"]; exists {
			t.Fatalf("expected sparse story_threads to fall back to full ledger, got %+v", selected["story_threads"])
		}
	}
}

func containsRecallSummary(items []domain.RecallItem, want string) bool {
	for _, item := range items {
		if strings.Contains(item.Summary, want) {
			return true
		}
	}
	return false
}

func TestContextToolInjectsRewriteBriefForPendingRewriteChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "节奏拖沓，需要压缩前半段"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "rewrite",
		Summary: "前半段铺垫过长，冲突迟迟不出现。",
		Issues: []domain.ConsistencyIssue{
			{Type: "pacing", Severity: "error", Description: "前 2000 字无推进"},
		},
		ContractMisses: []string{"未兑现试炼开场"},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	brief, ok := payload["rewrite_brief"].(map[string]any)
	if !ok {
		t.Fatalf("expected rewrite_brief in chapter context, got %T", payload["rewrite_brief"])
	}
	if got := brief["reason"]; got != "节奏拖沓，需要压缩前半段" {
		t.Fatalf("expected rewrite reason, got %v", got)
	}
	if got, _ := brief["review_summary"].(string); !strings.Contains(got, "铺垫过长") {
		t.Fatalf("expected review summary from chapter review, got %v", brief["review_summary"])
	}
	if issues, _ := brief["issues"].([]any); len(issues) == 0 {
		t.Fatalf("expected review issues in rewrite_brief, got %v", brief["issues"])
	}
	if misses, _ := brief["contract_misses"].([]any); len(misses) == 0 {
		t.Fatalf("expected contract misses in rewrite_brief, got %v", brief["contract_misses"])
	}
}

func TestContextToolOmitsRewriteBriefForNormalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, ok := payload["rewrite_brief"]; ok {
		t.Fatal("expected no rewrite_brief for chapter outside PendingRewrites")
	}
}

func TestContextToolInjectsUserDirectivesOnBothPaths(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if _, err := s.Directives.Add(domain.UserDirective{Text: "对话占比提高", Chapter: 2, TotalChapters: 3}); err != nil {
		t.Fatalf("AddDirective: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	for name, chapter := range map[string]int{"writer": 1, "architect": 0} {
		args, _ := json.Marshal(map[string]any{"chapter": chapter})
		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("[%s] Execute: %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatalf("[%s] Unmarshal: %v", name, err)
		}
		working, ok := payload["working_memory"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] missing working_memory", name)
		}
		directives, ok := working["user_directives"].([]any)
		if !ok || len(directives) != 1 {
			t.Fatalf("[%s] expected 1 directive, got %v", name, working["user_directives"])
		}
		entry, _ := directives[0].(map[string]any)
		if entry["text"] != "对话占比提高" || entry["at_chapter"] != float64(2) || entry["at_total_chapters"] != float64(3) {
			t.Errorf("[%s] unexpected directive entry: %v", name, entry)
		}
		if _, hasCreatedAt := entry["created_at"]; hasCreatedAt {
			t.Errorf("[%s] created_at là thông tin kiểm toán, không được truyền vào LLM", name)
		}
	}
}

func TestContextToolInjectsEmptyUserDirectives(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 0})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("missing working_memory")
	}
	// Danh sách rỗng cũng inject [] (trường ổn định, theo tiền lệ user_rules), không được là null/thiếu
	directives, ok := working["user_directives"].([]any)
	if !ok {
		t.Fatalf("expected stable empty array, got %T", working["user_directives"])
	}
	if len(directives) != 0 {
		t.Errorf("expected empty list, got %v", directives)
	}
}

// Part F — chống livelock kết thúc sách: recall theo tuổi phải chạy với MỌI số lượng foreshadow active
// (bỏ ngưỡng cứng ≥6 cũ), và ngưỡng tuổi scale theo tổng chương (max(10, total/8)) thay vì cố định 30.
// Sách 40 chương: một phục bút treo 15 chương (>= max(10,40/8=5)=10) phải nổi lên dù chỉ có 3 foreshadow —
// trước đây bị chặn vì <6 foreshadow.
func TestContextToolAgingRecallSurfacesSingleForeshadowInShortBook(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Chủ đề chương hiện tại không dính từ khóa của bất kỳ phục bút nào → gợi nhớ theo liên quan trả rỗng,
	// chỉ còn bù theo tuổi có hiệu lực.
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 20, Title: "北岭观星", CoreEvent: "林砚独自登上北岭架设浑天仪观测星象", Scenes: []string{"记录星轨", "校准刻度"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 40); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// Chỉ 3 phục bút (dưới ngưỡng ≥6 cũ). Một cái treo 15 chương, hai cái mới đặt.
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "bronze_mirror", Description: "尘封地窖里的青铜古镜", PlantedAt: 5, Status: "planted"},
		{ID: "spice_ledger", Description: "码头新到的香料账目", PlantedAt: 18, Status: "planted"},
		{ID: "child_song", Description: "巷口孩童传唱的童谣", PlantedAt: 19, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 20})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "青铜古镜") {
		t.Fatalf("expected aging foreshadow to surface despite only 3 active, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "chưa thu hồi") {
		t.Fatalf("expected aging item to carry overdue annotation, got %+v", payload.Selected.StoryThreads)
	}
	// Phục bút mới đặt (tuổi < ngưỡng 10) không được gắn nhãn treo lâu.
	if containsRecallSummary(payload.Selected.StoryThreads, "香料账目") ||
		containsRecallSummary(payload.Selected.StoryThreads, "童谣") {
		t.Fatalf("recent foreshadow must not surface as overdue, got %+v", payload.Selected.StoryThreads)
	}
}

// Part F — vùng kết: ở chương 85/100 (>= endgameChapterPercent) bỏ cap 5 và inject TOÀN BỘ foreshadow
// active kèm tuổi từng cái, kể cả cái vừa đặt — để writer thu hồi hết trước khi complete_book
// (chế độ layered đòi active_foreshadow==0, xem applyCompletion trong commit_chapter.go).
func TestContextToolEndgameInjectsAllActiveForeshadowWithAge(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 85, Title: "临渊", CoreEvent: "林砚独自面对深渊边缘的抉择", Scenes: []string{"独白", "凝望"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init("test", 100); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// 12 phục bút active, PlantedAt trải rộng: có cái treo rất lâu, có cái vừa đặt (tuổi 1) ở chương 84.
	ledger := make([]domain.ForeshadowEntry, 0, 12)
	plantedAts := []int{3, 8, 14, 21, 29, 36, 44, 51, 60, 70, 79, 84}
	for i, pa := range plantedAts {
		ledger = append(ledger, domain.ForeshadowEntry{
			ID:          fmt.Sprintf("thread_%02d", i),
			Description: fmt.Sprintf("独立悬置线索编号%02d", i),
			PlantedAt:   pa,
			Status:      "planted",
		})
	}
	if err := s.World.SaveForeshadowLedger(ledger); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 85})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Bỏ cap 5: toàn bộ 12 phục bút active đều xuất hiện.
	if got := len(payload.Selected.StoryThreads); got != 12 {
		t.Fatalf("expected all 12 active foreshadow in endgame (cap 5 removed), got %d: %+v", got, payload.Selected.StoryThreads)
	}
	// Mỗi mục kèm tuổi.
	for _, item := range payload.Selected.StoryThreads {
		if !strings.Contains(item.Summary, "chưa thu hồi") {
			t.Fatalf("expected every endgame item to carry age annotation, got %+v", item)
		}
	}
	// Cái vừa đặt (tuổi 1, chương 84) cũng phải xuất hiện — chứng minh không còn ngưỡng tuổi ở vùng kết.
	if !containsRecallSummary(payload.Selected.StoryThreads, "编号11") ||
		!containsRecallSummary(payload.Selected.StoryThreads, "đã 1 chương chưa thu hồi") {
		t.Fatalf("expected freshly-planted foreshadow (age 1) to surface in endgame, got %+v", payload.Selected.StoryThreads)
	}
}

// Part F — không xác định được tổng chương → ngưỡng tuổi rơi về mặc định 30 (hành vi cũ):
// phục bút treo 25 chương KHÔNG nổi (25 < 30), treo 35 chương thì nổi. Nếu dùng nhầm sàn scale 10 thì
// cái treo 25 sẽ lọt — test này chốt đúng fallback 30.
func TestContextToolAgingThresholdFallsBackTo30WhenTotalUnknown(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 40, Title: "夜行", CoreEvent: "林砚趁夜穿过荒原前往废驿", Scenes: []string{"避开巡逻", "点燃火把"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	// Tổng chương = 0 → không xác định được; resolveTotalChapters trả 0 → agingThresholdFor dùng mặc định 30.
	if err := s.Progress.Init("test", 0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "old_debt", Description: "十年前的一笔旧账", PlantedAt: 5, Status: "planted"},     // tuổi 35 >= 30 → nổi
		{ID: "mid_thread", Description: "半途搁下的一桩公案", PlantedAt: 15, Status: "planted"}, // tuổi 25 < 30 → không nổi
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 40})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "旧账") {
		t.Fatalf("expected 35-chapter-old foreshadow to surface under default threshold 30, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "公案") {
		t.Fatalf("25-chapter-old foreshadow must stay out under default threshold 30 (not scale floor 10), got %+v", payload.Selected.StoryThreads)
	}
}

// Part M — voice card: nhân vật có voice VÀ xuất hiện trong chapter_plan → working_memory.voice_cards
// chứa đúng card của nhân vật đó; nhân vật có voice nhưng VẮNG MẶT trong plan không được nạp (chỉ nhân vật của chương).
func TestContextToolInjectsVoiceCardsForChapterCharacters(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 5); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 2, Title: "Đối đầu", CoreEvent: "cuộc chạm mặt"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lâm Viễn", Role: "chính", Tier: "core", Voice: &domain.CharacterVoiceCard{
			Catchphrases:  []string{"'ừ thì'"},
			SentenceStyle: "câu ngắn, cộc",
			Taboo:         "không bao giờ văn hoa",
		}},
		// Tô Ly có voice nhưng KHÔNG xuất hiện trong chapter_plan → không được nạp.
		{Name: "Tô Ly", Role: "phụ", Tier: "important", Voice: &domain.CharacterVoiceCard{
			SentenceStyle: "nhiều từ Hán Việt trang trọng",
		}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter:  2,
		Title:    "Đối đầu",
		Goal:     "Lâm Viễn quyết định ra tay",
		Conflict: "Lâm Viễn đối mặt kẻ thù cũ",
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("missing working_memory")
	}
	cardsRaw, ok := working["voice_cards"].([]any)
	if !ok {
		t.Fatalf("expected working_memory.voice_cards array, got %T", working["voice_cards"])
	}
	if len(cardsRaw) != 1 {
		t.Fatalf("expected exactly 1 voice card (only chapter cast), got %d: %+v", len(cardsRaw), cardsRaw)
	}
	card, _ := cardsRaw[0].(map[string]any)
	if card["character"] != "Lâm Viễn" {
		t.Fatalf("expected voice card for Lâm Viễn, got %+v", card)
	}
	if _, ok := card["catchphrases"].([]any); !ok {
		t.Fatalf("expected catchphrases in voice card, got %+v", card)
	}
	if card["taboo"] != "không bao giờ văn hoa" {
		t.Fatalf("expected taboo field, got %+v", card)
	}
	// Nhân vật vắng mặt không được lọt vào.
	for _, c := range cardsRaw {
		if m, _ := c.(map[string]any); m["character"] == "Tô Ly" {
			t.Fatalf("absent character Tô Ly must not appear in voice_cards, got %+v", cardsRaw)
		}
	}
}

// Part M — nhân vật thiếu voice (nhân vật cũ / tier thấp) → không panic, không tạo mục voice_cards rỗng.
func TestContextToolOmitsVoiceCardsWhenCharactersLackVoice(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 5); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 2, Title: "Đối đầu", CoreEvent: "cuộc chạm mặt"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	// Nhân vật cũ: không có trường voice; và một nhân vật có con trỏ voice nhưng rỗng (IsEmpty) — đều phải bỏ qua êm.
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lâm Viễn", Role: "chính", Tier: "core"},
		{Name: "Tô Ly", Role: "phụ", Tier: "important", Voice: &domain.CharacterVoiceCard{}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "Đối đầu",
		Goal:    "Lâm Viễn và Tô Ly chạm mặt",
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}

	tool := NewContextTool(s, References{}, "default", rules.LoadOptions{})
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working, ok := payload["working_memory"].(map[string]any)
	if !ok {
		t.Fatal("missing working_memory")
	}
	if _, ok := working["voice_cards"]; ok {
		t.Fatalf("expected no voice_cards when no character has a voice, got %+v", working["voice_cards"])
	}
}
