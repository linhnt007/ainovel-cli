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

// TestLoadState_FlatPendingReviewDerivedFromDisk kiểm tra logic suy cờ review định kỳ từ ĐĨA:
// cờ bật/tắt theo sự tồn tại của file reviews/NN-global.json, không theo cờ Flow trong Progress.
func TestLoadState_FlatPendingReviewDerivedFromDisk(t *testing.T) {
	// (i) LastCompleted=10 (bội ReviewInterval), chưa có review global nào → pending=true
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 10)
	if s := LoadState(store); !s.HasPendingFlatReview {
		t.Fatalf("(i) chưa có review batch 10 → mong HasPendingFlatReview=true, got false")
	}

	// (ii) Sau khi lưu review scope=global chapter=10 → cờ được clear (pending=false)
	if err := store.World.SaveReview(domain.ReviewEntry{Chapter: 10, Scope: "global", Verdict: "accept"}); err != nil {
		t.Fatalf("save global review ch10: %v", err)
	}
	if s := LoadState(store); s.HasPendingFlatReview {
		t.Fatalf("(ii) đã có review batch 10 → mong HasPendingFlatReview=false, got true")
	}

	// (iii) Chỉ có review cũ chapter=5, nhưng đã hoàn thành tới chương 10 → batch 10 chưa phủ → pending=true
	store2 := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store2, 10)
	if err := store2.World.SaveReview(domain.ReviewEntry{Chapter: 5, Scope: "global", Verdict: "accept"}); err != nil {
		t.Fatalf("save global review ch5: %v", err)
	}
	if s := LoadState(store2); !s.HasPendingFlatReview {
		t.Fatalf("(iii) review gần nhất là ch5 < mốc batch 10 → mong HasPendingFlatReview=true, got false")
	}
}
