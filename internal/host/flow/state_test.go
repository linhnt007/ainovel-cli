package flow

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// saveFlatProgress ghi một Progress chế độ flat (không phân tầng) đang ở giai đoạn Writing.
func saveFlatProgress(t *testing.T, store *storepkg.Store, lastCompleted int) {
	t.Helper()
	completed := make([]int, 0, lastCompleted)
	for i := 1; i <= lastCompleted; i++ {
		completed = append(completed, i)
	}
	if err := store.Progress.Save(&domain.Progress{
		NovelName:         "t",
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		TotalChapters:     50,
		CompletedChapters: completed,
	}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
}

// TestLoadState_UnifiedReviewDerivedFromDisk kiểm tra logic suy review từ ĐĨA:
// NeedsReviewChapter được set khi single review file vắng mặt.
// IsReviewBatch=true khi chương là bội của ReviewInterval.
func TestLoadState_UnifiedReviewDerivedFromDisk(t *testing.T) {
	// (i) LastCompleted=10 (bội default ReviewInterval=5), chưa có review chapter nào → NeedsReviewChapter=10, IsReviewBatch=true
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 10)
	s := LoadState(store, 0, 0)
	if s.NeedsReviewChapter != 10 {
		t.Fatalf("(i) chưa có review chương 10 → mong NeedsReviewChapter=10, got %d", s.NeedsReviewChapter)
	}
	if !s.IsReviewBatch {
		t.Fatalf("(i) chương 10 là bội của 5 → mong IsReviewBatch=true, got false")
	}

	// (ii) Sau khi lưu review chapter=10 (single) → NeedsReviewChapter=0
	if err := store.World.SaveReview(domain.ReviewEntry{Chapter: 10, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch10: %v", err)
	}
	s2 := LoadState(store, 0, 0)
	if s2.NeedsReviewChapter != 0 {
		t.Fatalf("(ii) đã có review chương 10 → mong NeedsReviewChapter=0, got %d", s2.NeedsReviewChapter)
	}

	// (iii) Chỉ có review chapter=4, nhưng đã hoàn thành tới chương 5 → chương 5 chưa review → NeedsReviewChapter=5
	store2 := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store2, 5)
	if err := store2.World.SaveReview(domain.ReviewEntry{Chapter: 4, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch4: %v", err)
	}
	s3 := LoadState(store2, 0, 0)
	if s3.NeedsReviewChapter != 5 {
		t.Fatalf("(iii) chưa có review chương 5 → mong NeedsReviewChapter=5, got %d", s3.NeedsReviewChapter)
	}
	if !s3.IsReviewBatch {
		t.Fatalf("(iii) chương 5 là bội của 5 → mong IsReviewBatch=true, got false")
	}
}

// TestLoadState_HumanGateWaitsForReview: mốc gate chỉ arm SAU KHI review xong.
// Regression cho bug deadlock: chương 8 hoàn thành (8%2==0) chưa review →
// gate arm sớm → qualityControlGate chặn editor review → lặp dispatch → circuit breaker.
func TestLoadState_HumanGateWaitsForReview(t *testing.T) {
	// (i) Mốc gate (8%2==0), CHƯA review chương 8 → gate CHƯA arm.
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 8)
	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("set gate every: %v", err)
	}
	s := LoadState(store, 0, 2)
	if s.NeedsReviewChapter != 8 {
		t.Fatalf("(i) chưa review chương 8 → NeedsReviewChapter=8, got %d", s.NeedsReviewChapter)
	}
	if s.HumanGatePending {
		t.Fatal("(i) chưa review → gate CHƯA arm, HumanGatePending phải=false")
	}

	// (ii) Đã review chương 8 → gate arm (chưa ack).
	if err := store.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	s2 := LoadState(store, 0, 2)
	if s2.NeedsReviewChapter != 0 {
		t.Fatalf("(ii) đã review chương 8 → NeedsReviewChapter=0, got %d", s2.NeedsReviewChapter)
	}
	if !s2.HumanGatePending {
		t.Fatal("(ii) đã review chưa ack → gate phải arm, HumanGatePending=true")
	}

	// (iii) User ack → gate hết pending.
	if err := store.World.SaveHumanGateAck(8, "ok"); err != nil {
		t.Fatalf("save ack ch8: %v", err)
	}
	s3 := LoadState(store, 0, 2)
	if s3.HumanGatePending {
		t.Fatal("(iii) đã ack → HumanGatePending phải=false")
	}
}

// TestLoadState_HumanGateNonMilestoneAndDisabled: không arm khi chưa tới mốc hoặc gate tắt.
func TestLoadState_HumanGateNonMilestoneAndDisabled(t *testing.T) {
	// (i) Chưa tới mốc (7%2!=0) → không arm dù review thiếu.
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 7)
	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("set gate every: %v", err)
	}
	if s := LoadState(store, 0, 2); s.HumanGatePending {
		t.Fatal("(i) chương 7 không phải mốc gate → không arm")
	}

	// (ii) Gate tắt (store chưa set, param=0) → không arm dù đã review mốc chương 8.
	store2 := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store2, 8)
	if err := store2.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	if s := LoadState(store2, 0, 0); s.HumanGatePending {
		t.Fatal("(ii) gate tắt → không arm")
	}
}

// TestState_hasPendingPreGateWork: các việc pre-gate khiến gate không được arm.
func TestState_hasPendingPreGateWork(t *testing.T) {
	cases := []struct {
		name string
		s    State
		want bool
	}{
		{"review chương mới nợ", State{NeedsReviewChapter: 8}, true},
		{"rewrite queue còn", State{Progress: &domain.Progress{PendingRewrites: []int{3}}}, true},
		{"re-review sau rewrite nợ", State{Progress: &domain.Progress{NeedsRewriteReview: 5}}, true},
		{"arc review nợ", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true}, HasArcReview: false}, true},
		{"arc summary nợ", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true}, HasArcReview: true, HasArcSummary: false}, true},
		{"volume end thiếu volume summary", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true, IsVolumeEnd: true}, HasArcReview: true, HasArcSummary: true, HasVolumeSummary: false}, true},
		{"arc end chờ expansion", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true, NeedsExpansion: true, NextArc: 2}, HasArcReview: true, HasArcSummary: true}, true},
		{"không còn việc pre-gate", State{NeedsReviewChapter: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.hasPendingPreGateWork(); got != tc.want {
				t.Fatalf("hasPendingPreGateWork() = %v, want %v", got, tc.want)
			}
		})
	}
}
