package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// TestLoadNormalizesLegacyReviewingFlow: build orchestrator-era từng persist flow="reviewing"
// (đã bị gỡ khỏi domain). Load phải normalize về writing để SetFlow không kẹt trên sách cũ.
func TestLoadNormalizesLegacyReviewingFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.Progress.Init("test", 10); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Ghi thẳng progress.json với flow legacy "reviewing" (không còn hằng số nào tương ứng).
	legacy := []byte(`{"novel_name":"test","phase":"writing","flow":"reviewing","total_chapters":10}`)
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), legacy, 0o644); err != nil {
		t.Fatalf("write legacy progress: %v", err)
	}

	p, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p == nil {
		t.Fatal("expected progress, got nil")
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("expected legacy flow normalized to writing, got %q", p.Flow)
	}

	// SetFlow phải hoạt động (trước fix, "reviewing" rơi vào default:false → mọi SetFlow fail).
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow after legacy normalize: %v", err)
	}
	p, _ = store.Progress.Load()
	if p.Flow != domain.FlowRewriting {
		t.Fatalf("expected flow rewriting after SetFlow, got %q", p.Flow)
	}
}

func TestSetFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Flow != domain.FlowRewriting {
		t.Errorf("expected FlowRewriting, got %s", p.Flow)
	}
}

// TestSetHumanGateEvery_NoCreateWhenMissing: boot Host gọi SetHumanGateEvery trên
// workspace trống (vừa xóa) — KHÔNG được tạo progress.json trống, nếu không
// buildResumePrompt tưởng có sách dở dang → tự động sáng tác thay vì màn hình start.
func TestSetHumanGateEvery_NoCreateWhenMissing(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("SetHumanGateEvery: %v", err)
	}

	p, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p != nil {
		t.Fatalf("SetHumanGateEvery phải không tạo progress khi chưa tồn tại, nhưng Load trả về %+v", p)
	}
}

// TestSetHumanGateEvery_UpdatesExisting: sách đã có progress thì vẫn ghi được giá trị.
func TestSetHumanGateEvery_UpdatesExisting(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("SetHumanGateEvery: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.HumanGateEvery != 2 {
		t.Fatalf("expected human_gate_every=2, got %d", p.HumanGateEvery)
	}
}

func TestSetNovelName(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetNovelName("长夜燃灯"); err != nil {
		t.Fatalf("SetNovelName: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.NovelName != "长夜燃灯" {
		t.Fatalf("expected novel name updated, got %q", p.NovelName)
	}
}

func TestSetFlowRejectsInvalidTransition(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow rewriting: %v", err)
	}
	// rewriting -> polishing là bước nhảy không hợp lệ (rewriting chỉ được về writing/steering).
	if err := store.Progress.SetFlow(domain.FlowPolishing); err == nil {
		t.Fatal("expected invalid flow transition to be rejected")
	}
}

func TestUpdatePhaseRejectsRegression(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase outline: %v", err)
	}
	if err := store.Progress.UpdatePhase(domain.PhasePremise); err == nil {
		t.Fatal("expected phase regression to be rejected")
	}
}

func TestStartChapter(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if err := store.Progress.StartChapter(1); err != nil {
		t.Fatalf("StartChapter: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("expected phase writing, got %s", p.Phase)
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("expected flow writing, got %s", p.Flow)
	}
	if p.CurrentChapter != 1 {
		t.Fatalf("expected current chapter 1, got %d", p.CurrentChapter)
	}
	if p.InProgressChapter != 1 {
		t.Fatalf("expected in-progress chapter 1, got %d", p.InProgressChapter)
	}
}

func TestIsChapterCompleted(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	if store.Progress.IsChapterCompleted(1) {
		t.Fatal("chapter 1 should not be completed initially")
	}

	_ = store.Progress.StartChapter(1)
	_ = store.Progress.MarkChapterComplete(1, 5000, "", "")

	if !store.Progress.IsChapterCompleted(1) {
		t.Fatal("chapter 1 should be completed after MarkChapterComplete")
	}
	if store.Progress.IsChapterCompleted(2) {
		t.Fatal("chapter 2 should not be completed")
	}
}

func TestSetPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")

	chapters := []int{3, 5, 7}
	if err := store.Progress.SetPendingRewrites(chapters, "角色动机不连贯"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(p.PendingRewrites))
	}
	if p.RewriteReason != "角色动机不连贯" {
		t.Errorf("reason mismatch: %s", p.RewriteReason)
	}
}

func TestSetPendingRewritesRejectsUnfinishedChapters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")

	if err := store.Progress.SetPendingRewrites([]int{3, 5}, "测试"); err == nil {
		t.Fatal("expected unfinished chapter to be rejected")
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

func TestValidateChapterWorkRejectsCorruptPendingRewriteQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 80)
	for ch := 1; ch <= 58; ch++ {
		_ = store.Progress.MarkChapterComplete(ch, 3000, "", "")
	}

	p, _ := store.Progress.Load()
	p.Flow = domain.FlowPolishing
	p.PendingRewrites = []int{65}
	if err := store.Progress.Save(p); err != nil {
		t.Fatalf("Save corrupt progress: %v", err)
	}

	if err := store.Progress.ValidateChapterWork(65); err == nil {
		t.Fatal("expected corrupt pending_rewrites to be rejected")
	}
}

func TestCompleteRewrite(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5, 7}, "测试重写")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// Hoàn thành chương 5
	if err := store.Progress.CompleteRewrite(5); err != nil {
		t.Fatalf("CompleteRewrite(5): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Fatalf("expected 2 pending after removing 5, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowRewriting {
		t.Errorf("flow should still be rewriting, got %s", p.Flow)
	}

	// Hoàn thành chương 3
	_ = store.Progress.CompleteRewrite(3)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(p.PendingRewrites))
	}

	// Hoàn thành chương cuối → tự động reset Flow
	_ = store.Progress.CompleteRewrite(7)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("expected 0 pending, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow should reset to writing, got %s", p.Flow)
	}
	if p.RewriteReason != "" {
		t.Errorf("reason should be cleared, got %s", p.RewriteReason)
	}
}

func TestCompleteRewrite_NotInQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5}, "测试")

	// Hoàn thành chương không có trong hàng đợi không nên báo lỗi
	if err := store.Progress.CompleteRewrite(99); err != nil {
		t.Fatalf("CompleteRewrite(99): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Errorf("queue should be unchanged, got %d", len(p.PendingRewrites))
	}
}

func TestCompleteRewrite_SetsNeedsRewriteReview(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{2}, "测试")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// Hoàn thành chương cuối cùng trong hàng đợi → đặt NeedsRewriteReview
	if err := store.Progress.CompleteRewrite(2); err != nil {
		t.Fatalf("CompleteRewrite(2): %v", err)
	}
	p, _ := store.Progress.Load()
	if p.NeedsRewriteReview != 2 {
		t.Fatalf("expected NeedsRewriteReview=2, got %d", p.NeedsRewriteReview)
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow should reset to writing, got %s", p.Flow)
	}
}

func TestCompleteRewrite_PartialDoesNotSetFlag(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{2, 3}, "测试")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// Hoàn thành 1 trong 2 chương → chưa đặt NeedsRewriteReview
	_ = store.Progress.CompleteRewrite(2)
	p, _ := store.Progress.Load()
	if p.NeedsRewriteReview != 0 {
		t.Fatalf("expected NeedsRewriteReview=0 (queue not empty), got %d", p.NeedsRewriteReview)
	}
}

func TestSetPendingRewrites_ClearsNeedsRewriteReview(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")

	// Đặt cờ NeedsRewriteReview trước
	p, _ := store.Progress.Load()
	p.NeedsRewriteReview = 2
	_ = store.Progress.Save(p)

	// SetPendingRewrites phải xóa cờ
	if err := store.Progress.SetPendingRewrites([]int{3}, "新重写"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	p, _ = store.Progress.Load()
	if p.NeedsRewriteReview != 0 {
		t.Fatalf("expected NeedsRewriteReview=0 after SetPendingRewrites, got %d", p.NeedsRewriteReview)
	}
}

func TestClearRewriteReview(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)

	// Đặt cờ
	p, _ := store.Progress.Load()
	p.NeedsRewriteReview = 5
	_ = store.Progress.Save(p)

	// ClearRewriteReview xóa cờ
	if err := store.Progress.ClearRewriteReview(); err != nil {
		t.Fatalf("ClearRewriteReview: %v", err)
	}
	p, _ = store.Progress.Load()
	if p.NeedsRewriteReview != 0 {
		t.Fatalf("expected NeedsRewriteReview=0 after clear, got %d", p.NeedsRewriteReview)
	}
}

func TestClearRewriteReview_NilProgress(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	// Không Init → progress nil → ClearRewriteReview không lỗi
	if err := store.Progress.ClearRewriteReview(); err != nil {
		t.Fatalf("ClearRewriteReview on nil progress: %v", err)
	}
}

func TestClearPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(1, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{1, 2, 3}, "测试")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	if err := store.Progress.ClearPendingRewrites(); err != nil {
		t.Fatalf("ClearPendingRewrites: %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Errorf("expected empty, got %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow should be writing, got %s", p.Flow)
	}
}

func TestRollbackToChapter_ClearsNeedsRewriteReview(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init("test", 10)
	_ = store.Progress.MarkChapterComplete(1, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{1}, "test")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// Hoàn thành chương cuối cùng trong hàng đợi → đặt NeedsRewriteReview
	if err := store.Progress.CompleteRewrite(1); err != nil {
		t.Fatalf("CompleteRewrite(1): %v", err)
	}
	p, _ := store.Progress.Load()
	if p.NeedsRewriteReview != 1 {
		t.Fatalf("expected NeedsRewriteReview=1 (bug state), got %d", p.NeedsRewriteReview)
	}

	// Rollback phải xóa cờ re-review để không kẹt editor re-review sau reset
	if err := store.RollbackToChapter(0); err != nil {
		t.Fatalf("RollbackToChapter(0): %v", err)
	}
	p, _ = store.Progress.Load()
	if p.NeedsRewriteReview != 0 {
		t.Errorf("expected NeedsRewriteReview=0 after rollback, got %d", p.NeedsRewriteReview)
	}
	if len(p.PendingRewrites) != 0 {
		t.Errorf("expected empty PendingRewrites, got %d", len(p.PendingRewrites))
	}
	if len(p.CompletedChapters) != 0 {
		t.Errorf("expected empty CompletedChapters, got %d", len(p.CompletedChapters))
	}
	if p.CurrentChapter != 1 {
		t.Errorf("expected CurrentChapter=1, got %d", p.CurrentChapter)
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("expected flow writing, got %s", p.Flow)
	}
}

// TestRollbackToChapter0_DerivesVolumeArcFromOutline: reset về 0 phải lấy volume/arc
// đầu tiên từ layered outline thật, không hardcode 1/1 — dữ liệu cũ lưu index lệch
// (vd LLM trả volume index=0) sẽ kẹt "Vn Am không tìm thấy" + expand_arc sau mỗi reset.
func TestRollbackToChapter0_DerivesVolumeArcFromOutline(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Outline lưu volume index lệch 0 (dữ liệu lỗi cũ), arc index 1
	vols := []domain.VolumeOutline{{
		Index: 0, Title: "Tập lệch", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "Cung 1", Goal: "g"}},
	}}
	if err := s.Outline.SaveLayeredOutline(vols); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}

	// Đưa progress về trạng thái đang viết với V/A sai lệch
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	p, _ := s.Progress.Load()
	p.Layered = true
	p.CurrentVolume = 7
	p.CurrentArc = 9
	if err := s.Progress.Save(p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.RollbackToChapter(0); err != nil {
		t.Fatalf("RollbackToChapter(0): %v", err)
	}
	p, _ = s.Progress.Load()
	if p.CurrentVolume != 0 || p.CurrentArc != 1 {
		t.Errorf("expected V0 A1 derived from outline, got V%d A%d", p.CurrentVolume, p.CurrentArc)
	}
	if p.Layered != true {
		t.Error("expected Layered=true preserved")
	}
}

// TestRollbackToChapter0_FallsBackTo1WhenNoOutline: chưa có layered outline (hoặc chưa
// phân lớp) thì rollback vẫn về 1/1 như cũ — không đổi hành vi truyện ngắn.
func TestRollbackToChapter0_FallsBackTo1WhenNoOutline(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Progress.Init("test", 10); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	if err := s.RollbackToChapter(0); err != nil {
		t.Fatalf("RollbackToChapter(0): %v", err)
	}
	p, _ := s.Progress.Load()
	if p.CurrentVolume != 1 || p.CurrentArc != 1 {
		t.Errorf("expected fallback V1 A1, got V%d A%d", p.CurrentVolume, p.CurrentArc)
	}
}
