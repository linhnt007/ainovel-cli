package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSaveReviewPersistsContractAssessment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":           3,
		"scope":             "chapter",
		"dimensions":        []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"}, {"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"}, {"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"}, {"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"}, {"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"}, {"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"}, {"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"}},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"contract_misses":   []string{"未明确埋下内门试炼邀请"},
		"contract_notes":    "主线推进达成，但 contract 中的第二个推进项没有落地。",
		"verdict":           "polish",
		"summary":           "本章基本完成目标，但 contract 仍有漏项。",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review == nil {
		t.Fatal("expected review saved, got nil")
	}
	if review.ContractStatus != "partial" {
		t.Fatalf("unexpected contract status: %q", review.ContractStatus)
	}
	if len(review.ContractMisses) != 1 || review.ContractMisses[0] != "未明确埋下内门试炼邀请" {
		t.Fatalf("unexpected contract misses: %+v", review.ContractMisses)
	}
	if review.Dimension("aesthetic") == nil {
		t.Fatalf("expected aesthetic dimension persisted, got %+v", review.Dimensions)
	}
}

func TestSaveReviewRejectsMissingDimensions(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"}},
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimensions must contain exactly") {
		t.Fatalf("expected dimensions validation error, got %v", err)
	}
}

func TestSaveReviewRejectsDimensionWithoutComment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "基本一致"},
			{"dimension": "character", "score": 82, "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78},
			{"dimension": "continuity", "score": 84, "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "comment": "正常"},
			{"dimension": "hook", "score": 76, "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "comment": "语言基本成立"},
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimension comment is required: pacing") {
		t.Fatalf("expected dimension comment validation error, got %v", err)
	}
}

func TestSaveReviewRejectsUnfinishedAffectedChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 80); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for ch := 1; ch <= 58; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 58,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "基本一致"},
			{"dimension": "character", "score": 82, "comment": "人设稳定"},
			{"dimension": "pacing", "score": 58, "comment": "节奏需要重写"},
			{"dimension": "continuity", "score": 84, "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "comment": "正常"},
			{"dimension": "hook", "score": 76, "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "comment": "语言基本成立"},
		},
		"issues":            []map[string]any{},
		"contract_status":   "partial",
		"verdict":           "polish",
		"summary":           "需要打磨第 58 章，不能把未完成章节入队。",
		"affected_chapters": []int{65},
		"contract_misses":   []string{"节奏超出本章职责"},
		"contract_notes":    "应只处理已完成章节。",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "pending_rewrites chỉ được chứa các chương đã hoàn thành") {
		t.Fatalf("expected unfinished affected chapter rejection, got %v", err)
	}
	review, err := s.World.LoadReview(58)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review != nil {
		t.Fatalf("review should not be saved when pending rewrite validation fails: %+v", review)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowWriting && p.Flow != "" {
		t.Fatalf("flow should not enter rewrite/polish, got %s", p.Flow)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

// TestSaveReviewDerivesVerdictFromScore kiểm tra: verdict được suy ra tất định từ score, khi mô hình
// cung cấp verdict không nhất quán (ví dụ score=85 nhưng điền warning) sẽ không báo lỗi mà bị ghi đè
// thành giá trị đúng (pass). Phòng hồi quy: mâu thuẫn score/verdict từng khiến save_review thất bại liên tục.
func TestSaveReviewDerivesVerdictFromScore(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "一致"},
			{"dimension": "character", "score": 82, "comment": "稳定"}, // bỏ qua verdict
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 85, "verdict": "warning", "comment": "语言成立"}, // không nhất quán: score=85 nhưng điền warning
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute should succeed (verdict auto-derived), got %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	// 85 → pass (ghi đè warning mà mô hình cung cấp); 82 bỏ qua verdict → pass.
	if d := review.Dimension("aesthetic"); d == nil || d.Verdict != "pass" {
		t.Fatalf("aesthetic verdict should be derived to pass, got %+v", d)
	}
	if d := review.Dimension("character"); d == nil || d.Verdict != "pass" {
		t.Fatalf("character verdict should be derived to pass, got %+v", d)
	}
}

func TestSaveReviewRejectsMissingAffectedChaptersForRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"},
		},
		"issues":  []map[string]any{},
		"verdict": "rewrite",
		"summary": "需要重写",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "affected_chapters is required") {
		t.Fatalf("expected affected_chapters validation error, got %v", err)
	}
}

// aestheticDims trả về bộ 7 chiều với aesthetic theo tham số, các chiều còn lại ≥80 (pass).
func aestheticDims(aestheticScore int) []map[string]any {
	return []map[string]any{
		{"dimension": "consistency", "score": 85, "comment": "nhất quán"},
		{"dimension": "character", "score": 85, "comment": "ổn định"},
		{"dimension": "pacing", "score": 82, "comment": "nhịp ổn"},
		{"dimension": "continuity", "score": 85, "comment": "liền mạch"},
		{"dimension": "foreshadow", "score": 82, "comment": "bình thường"},
		{"dimension": "hook", "score": 82, "comment": "móc câu ổn"},
		{"dimension": "aesthetic", "score": aestheticScore, "comment": "văn có chỗ AI: \"không phải X mà là Y\""},
	}
}

func execSaveReview(t *testing.T, tool *SaveReviewTool, args map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	return res
}

// TestSaveReviewAestheticPolishGate: aesthetic=65, các chiều khác ≥80, chưa polish lần nào
// → verdict nâng lên polish và cờ aesthetic_polished=true, rewrite_count=1.
func TestSaveReviewAestheticPolishGate(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	res := execSaveReview(t, tool, map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": aestheticDims(65),
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "văn ổn nhưng thẩm mỹ dưới ngưỡng",
	})

	if res["final_verdict"] != "polish" {
		t.Fatalf("expected final_verdict=polish, got %v", res["final_verdict"])
	}
	if res["aesthetic_polished"] != true {
		t.Fatalf("expected aesthetic_polished=true, got %v", res["aesthetic_polished"])
	}
	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if !review.AestheticPolished {
		t.Fatalf("expected persisted AestheticPolished=true, got %+v", review)
	}
	if review.RewriteCount != 1 {
		t.Fatalf("expected RewriteCount=1, got %d", review.RewriteCount)
	}
}

// TestSaveReviewAestheticPolishCeiling: aesthetic=65 nhưng chương đã polish thẩm mỹ 1 lần
// → không nâng nữa, accept kèm ghi chú (trần 1 vòng).
func TestSaveReviewAestheticPolishCeiling(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	// Gieo review record trước: chương đã polish thẩm mỹ 1 lần.
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 3, Scope: "chapter", Verdict: "polish", Summary: "vòng trước",
		AestheticPolished: true, RewriteCount: 1,
	}); err != nil {
		t.Fatalf("seed SaveReview: %v", err)
	}

	tool := NewSaveReviewTool(s)
	res := execSaveReview(t, tool, map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": aestheticDims(65),
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "thẩm mỹ vẫn dưới ngưỡng nhưng đã hết trần polish",
	})

	if res["final_verdict"] != "accept" {
		t.Fatalf("expected final_verdict=accept (đã hết trần polish thẩm mỹ), got %v", res["final_verdict"])
	}
	note, _ := res["escalation_reason"].(string)
	if !strings.Contains(note, "aesthetic") {
		t.Fatalf("expected ghi chú về aesthetic trong escalation_reason, got %q", note)
	}
	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if !review.AestheticPolished {
		t.Fatalf("expected AestheticPolished vẫn true sau khi carry-forward, got %+v", review)
	}
}

// TestSaveReviewRewriteCeiling: chương đã qua đủ maxRewritePerChapter vòng, lần thứ 4 vẫn rewrite
// → ép accept + quality_debt=true.
func TestSaveReviewRewriteCeiling(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	// Gieo review record trước: chương đã rewrite/polish đủ trần (3 vòng).
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 3, Scope: "chapter", Verdict: "rewrite", Summary: "vòng 3",
		RewriteCount: maxRewritePerChapter,
	}); err != nil {
		t.Fatalf("seed SaveReview: %v", err)
	}

	tool := NewSaveReviewTool(s)
	res := execSaveReview(t, tool, map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 55, "comment": "vẫn lỗi nhất quán"},
			{"dimension": "character", "score": 82, "comment": "ổn định"},
			{"dimension": "pacing", "score": 78, "comment": "hơi chậm"},
			{"dimension": "continuity", "score": 84, "comment": "liền mạch"},
			{"dimension": "foreshadow", "score": 80, "comment": "bình thường"},
			{"dimension": "hook", "score": 76, "comment": "móc câu tạm"},
			{"dimension": "aesthetic", "score": 81, "comment": "văn tạm ổn"},
		},
		"issues": []map[string]any{
			{"type": "consistency", "severity": "critical", "description": "mâu thuẫn thiết lập", "evidence": "đoạn A vs đoạn B"},
		},
		"verdict":           "rewrite",
		"summary":           "vẫn còn lỗi critical sau nhiều vòng",
		"affected_chapters": []int{3},
	})

	if res["final_verdict"] != "accept" {
		t.Fatalf("expected final_verdict=accept (chạm trần rewrite), got %v", res["final_verdict"])
	}
	if res["quality_debt"] != true {
		t.Fatalf("expected quality_debt=true, got %v", res["quality_debt"])
	}
	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if !review.QualityDebt || review.QualityDebtReason == "" {
		t.Fatalf("expected QualityDebt=true với lý do, got %+v", review)
	}
	// Không tăng count nữa khi bị trần ép accept.
	if review.RewriteCount != maxRewritePerChapter {
		t.Fatalf("expected RewriteCount giữ nguyên %d khi bị trần, got %d", maxRewritePerChapter, review.RewriteCount)
	}
	// Chương không vào hàng đợi rewrite.
	p, _ := s.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("expected không có pending rewrites khi bị trần, got %v", p.PendingRewrites)
	}
}

func TestSaveReviewRejectsIssueWithoutEvidence(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "基本一致"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "人设稳定"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "略慢"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "连贯"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "正常"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "钩子一般"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "语言基本成立"},
		},
		"issues": []map[string]any{
			{"type": "hook", "severity": "warning", "description": "章末钩子偏弱"},
		},
		"verdict":           "polish",
		"summary":           "需要补强钩子。",
		"affected_chapters": []int{3},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "issue evidence is required") {
		t.Fatalf("expected issue evidence validation error, got %v", err)
	}
}
