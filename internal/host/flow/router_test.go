package flow

import (
	"fmt"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// helper: tạo một Progress đang ở giai đoạn Writing, chế độ phân lớp.
func writingProgress(completed []int, flow domain.FlowState) *domain.Progress {
	return &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              flow,
		Layered:           true,
		CompletedChapters: completed,
	}
}

func TestRoute_NilProgress(t *testing.T) {
	if got := Route(State{Progress: nil}); got != nil {
		t.Fatalf("expected nil for nil progress, got %+v", got)
	}
}

func TestRoute_PhaseComplete(t *testing.T) {
	s := State{Progress: &domain.Progress{Phase: domain.PhaseComplete}}
	if got := Route(s); got != nil {
		t.Fatalf("expected nil at PhaseComplete, got %+v", got)
	}
}

func TestRoute_NonWritingPhasesDelegateToLLM(t *testing.T) {
	for _, phase := range []domain.Phase{domain.PhaseInit, domain.PhasePremise, domain.PhaseOutline} {
		s := State{Progress: &domain.Progress{Phase: phase}, FoundationMissing: []string{"premise"}}
		if got := Route(s); got != nil {
			t.Fatalf("phase %s should return nil, got %+v", phase, got)
		}
	}
}

func TestRoute_PendingRewritesFirst(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{3, 5}
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer for rewrites, got %+v", got)
	}
	if got.Task != "Viết lại chương 3" {
		t.Errorf("expected 'Viết lại chương 3', got %q", got.Task)
	}
	if got.Chapter != 3 {
		t.Errorf("expected Chapter=3, got %d", got.Chapter)
	}
}

func TestRoute_PendingPolishingVerb(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowPolishing)
	p.PendingRewrites = []int{2}
	got := Route(State{Progress: p})
	if got == nil || got.Task != "Đánh bóng chương 2" {
		t.Fatalf("expected polish verb, got %+v", got)
	}
}

func TestRouteHumanGate(t *testing.T) {
	s := State{
		Progress: &domain.Progress{
			Phase:             domain.PhaseWriting,
			TotalChapters:     40,
			CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			CurrentChapter:    10,
		},
		LastCompleted:    10,
		HumanGatePending: true,
	}
	inst := Route(s)
	if inst == nil || inst.Agent != "" || inst.Reason != "human gate" {
		t.Fatalf("gate pending phai tra instruction dung-cho, got %+v", inst)
	}
}

func TestRoute_SteeringDelegatesToLLM(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowSteering)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("expected nil during steering, got %+v", got)
	}
}

func TestRoute_ArcEndNeedsReview(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("expected editor for arc review, got %+v", got)
	}
	if got.Reason != "Đánh giá cuối cung truyện chưa hoàn thành" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_ArcEndHasReviewNeedsSummary(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
		HasArcReview: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" || got.Reason != "Tóm tắt cung truyện chưa hoàn thành" {
		t.Fatalf("expected arc summary editor call, got %+v", got)
	}
}

func TestRoute_VolumeEndNeedsVolumeSummary(t *testing.T) {
	p := writingProgress([]int{20}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 20,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:    true,
			IsVolumeEnd: true,
			Volume:      1,
			Arc:         3,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Reason != "Tóm tắt tập chưa hoàn thành" {
		t.Fatalf("expected volume summary request, got %+v", got)
	}
}

func TestRoute_NeedsArcExpansion(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			Volume:         1,
			Arc:            2,
			NextVolume:     1,
			NextArc:        3,
			NeedsExpansion: true,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("expected architect_long for expansion, got %+v", got)
	}
	if got.Reason != "Skeleton cung truyện tiếp theo cần được mở rộng" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_NeedsNewVolume(t *testing.T) {
	p := writingProgress([]int{30}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 30,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			IsVolumeEnd:    true,
			Volume:         2,
			Arc:            4,
			NeedsNewVolume: true,
		},
		HasArcReview:     true,
		HasArcSummary:    true,
		HasVolumeSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" || got.Reason != "Cuối tập cần quyết định thêm tập mới hay kết thúc toàn bộ tác phẩm" {
		t.Fatalf("expected append_volume/complete_book dispatch, got %+v", got)
	}
}

func TestRoute_NormalContinue(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	p.TotalChapters = 20
	got := Route(State{Progress: p, LastCompleted: 3})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer for next chapter, got %+v", got)
	}
	if got.Task != "Viết chương 4" {
		t.Errorf("expected 'Viết chương 4', got %q", got.Task)
	}
	if got.Chapter != 4 {
		t.Errorf("expected Chapter=4, got %d", got.Chapter)
	}
}

// flatProgress tạo Progress chế độ flat (không phân tầng) đang ở giai đoạn Writing.
func flatProgress(completed []int) *domain.Progress {
	return &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: completed,
		TotalChapters:     50,
	}
}

func TestRoute_FlatPendingReviewDispatchesEditor(t *testing.T) {
	// Đã hoàn thành 5 chương (bội của ReviewInterval) nhưng chưa được review → ép editor (unified, batch+single).
	s := State{
		Progress:           flatProgress([]int{1, 2, 3, 4, 5}),
		LastCompleted:      5,
		NeedsReviewChapter: 5,
		IsReviewBatch:      true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("expected editor for pending review, got %+v", got)
	}
	if got.Task != "Đánh giá batch chương 1-5 + chapter 5 (scope=both)" {
		t.Errorf("task mismatch: %q", got.Task)
	}
	if got.Reason != "Review định kỳ batch 1-5 + chapter mới 5" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
	if got.Chapter != 0 {
		t.Errorf("nhiệm vụ editor không gắn chương cụ thể, got Chapter=%d", got.Chapter)
	}
}

func TestRoute_FlatReviewDoneContinuesWriter(t *testing.T) {
	// Đã review xong (NeedsReviewChapter=0) → tiếp tục viết chương kế.
	s := State{
		Progress:           flatProgress([]int{1, 2, 3, 4, 5}),
		LastCompleted:      5,
		NeedsReviewChapter: 0,
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer after review done, got %+v", got)
	}
	if got.Task != "Viết chương 6" {
		t.Errorf("expected 'Viết chương 6', got %q", got.Task)
	}
	if got.Chapter != 6 {
		t.Errorf("expected Chapter=6, got %d", got.Chapter)
	}
}

func TestRoute_ArcEndNonLayeredSkipsBoundary(t *testing.T) {
	// Chế độ không phải Layered: dù ArcBoundary khác nil vẫn không đi vào nhánh cuối cung truyện
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{10},
		TotalChapters:     20,
	}
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary:   &storepkg.ArcBoundary{IsArcEnd: true, Volume: 1, Arc: 2},
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("non-layered should fall through to writer, got %+v", got)
	}
}

func TestFormatMessage(t *testing.T) {
	msg := FormatMessage(&Instruction{Agent: "writer", Task: "Viết chương 5", Reason: "tiếp tục"})
	for _, want := range []string{"[Host ra lệnh]", "writer", "Viết chương 5", "tiếp tục", "không được gọi novel_context"} {
		if !contains(msg, want) {
			t.Errorf("message missing %q: %s", want, msg)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDispatcher_TrackRepeat(t *testing.T) {
	// Không cần coordinator / store thật; trackRepeat chỉ đọc cache nội bộ.
	d := &Dispatcher{}
	inst := &Instruction{Agent: "writer", Task: "Viết chương 5", Reason: "tiếp tục"}
	if got := d.trackRepeat(inst); got != 1 {
		t.Fatalf("lần đầu hạ lệnh phải tính 1, got %d", got)
	}
	if got := d.trackRepeat(inst); got != 2 {
		t.Fatalf("cùng Agent+Task lặp lại phải tính 2, got %d", got)
	}
	// Reason khác nhau, Agent+Task giống nhau vẫn coi là cùng lệnh và tiếp tục cộng dồn
	sameTaskDiffReason := &Instruction{Agent: "writer", Task: "Viết chương 5", Reason: "弧末后继续"}
	if got := d.trackRepeat(sameTaskDiffReason); got != 3 {
		t.Fatalf("chỉ khác Reason vẫn tính là lặp, cộng dồn lên 3, got %d", got)
	}
	other := &Instruction{Agent: "writer", Task: "Viết chương 6", Reason: "tiếp tục"}
	if got := d.trackRepeat(other); got != 1 {
		t.Fatalf("Task thay đổi phải reset về 1, got %d", got)
	}
	d.ResetRepeat()
	if got := d.trackRepeat(other); got != 1 {
		t.Fatalf("sau ResetRepeat lần đầu phải tính 1, got %d", got)
	}
}

func TestFormatDispatchMessage_RepeatNotice(t *testing.T) {
	inst := &Instruction{Agent: "writer", Task: "Viết chương 5", Reason: "tiếp tục"}
	first := formatDispatchMessage(inst, 1)
	if first != FormatMessage(inst) {
		t.Fatalf("lần đầu hạ lệnh không được đính kèm ghi chú lặp: %s", first)
	}
	third := formatDispatchMessage(inst, 3)
	for _, want := range []string{"lần thứ 3", "sự thật route chưa thay đổi", "novel_context", "chuyển sang agent phụ khác"} {
		if !contains(third, want) {
			t.Errorf("ghi chú lặp thiếu %q: %s", want, third)
		}
	}
}

func TestDispatcher_OnRepeatFiresOnceAtThreshold(t *testing.T) {
	d := &Dispatcher{}
	var fired []string
	d.SetOnRepeat(func(agent, task string, n int) {
		fired = append(fired, fmt.Sprintf("%s|%s|%d", agent, task, n))
	})

	inst := &Instruction{Agent: "writer", Task: "Viết chương 5"}
	for range 6 {
		d.trackRepeat(inst) // n=1..6: chỉ callback đúng một lần khi n==3
	}
	if len(fired) != 1 || fired[0] != fmt.Sprintf("writer|Viết chương 5|%d", repeatNotifyAt) {
		t.Fatalf("phải trigger đúng một lần tại lần thứ %d, got %v", repeatNotifyAt, fired)
	}

	// Sau khi đổi key sẽ được tái vũ trang: đổi task rồi lặp tiếp 3 lần → trigger thêm một lần
	other := &Instruction{Agent: "writer", Task: "Viết chương 6"}
	for range 3 {
		d.trackRepeat(other)
	}
	if len(fired) != 2 {
		t.Fatalf("sau khi đổi key phải tái vũ trang, got %v", fired)
	}
}

func TestRoute_NeedsRewriteReview(t *testing.T) {
	// NeedsRewriteReview=2, không có PendingRewrites → editor re-review chương 2
	p := writingProgress([]int{1, 2}, domain.FlowWriting)
	p.NeedsRewriteReview = 2
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "editor" {
		t.Fatalf("expected editor for rewrite review, got %+v", got)
	}
	if got.Task != "Đánh giá lại chương 2 sau viết lại (scope=chapter)" {
		t.Errorf("task mismatch: %q", got.Task)
	}
	if got.Chapter != 0 {
		t.Errorf("editor task should have Chapter=0, got %d", got.Chapter)
	}
}

func TestRoute_StaleNeedsRewriteReviewFallsThrough(t *testing.T) {
	// Stale flag: NeedsRewriteReview=2 nhưng chương 2 KHÔNG nằm trong CompletedChapters
	// (vd sau rollback completed_chapters=null) → step 3.5 bỏ qua, fall-through xuống writer,
	// không loop editor mãi. Route là hàm thuần túy, không clear flag ở đây.
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		CurrentChapter:    3,
		CompletedChapters: nil,
	}
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("stale rewrite review phải fall-through xuống writer, got %+v", got)
	}
	if contains(got.Task, "Đánh giá lại") {
		t.Errorf("không được dispatch editor re-review, got %q", got.Task)
	}
	if got.Chapter != 1 {
		t.Errorf("expected Chapter=1 (NextChapter từ CompletedChapters rỗng), got %d", got.Chapter)
	}

	// Normal case: chương 2 đã completed + flag → vẫn editor dispatch như cũ.
	ok := writingProgress([]int{1, 2}, domain.FlowWriting)
	ok.NeedsRewriteReview = 2
	got = Route(State{Progress: ok})
	if got == nil || got.Agent != "editor" {
		t.Fatalf("flag hợp lệ (chương đã completed) phải dispatch editor, got %+v", got)
	}
	if got.Task != "Đánh giá lại chương 2 sau viết lại (scope=chapter)" {
		t.Errorf("task mismatch: %q", got.Task)
	}
}

func TestRoute_PendingRewritesBeforeNeedsReview(t *testing.T) {
	// PendingRewrites=[3] + NeedsRewriteReview=2 → step 3 thắng (writer ch3)
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{3}
	p.NeedsRewriteReview = 2
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer (step 3 wins), got %+v", got)
	}
	if got.Chapter != 3 {
		t.Errorf("expected Chapter=3, got %d", got.Chapter)
	}
}

func TestRoute_NeedsRewriteReviewZeroFallsThrough(t *testing.T) {
	// NeedsRewriteReview=0 → không trigger step 3.5, tiếp tục flow bình thường
	p := writingProgress([]int{1, 2}, domain.FlowWriting)
	p.TotalChapters = 10
	got := Route(State{Progress: p, LastCompleted: 2})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer for next chapter, got %+v", got)
	}
	if got.Task != "Viết chương 3" {
		t.Errorf("expected 'Viết chương 3', got %q", got.Task)
	}
}

func TestRoute_LoadWarningsFallback(t *testing.T) {
	t.Run("nil progress but has warnings", func(t *testing.T) {
		s := State{
			Progress:     nil,
			LoadWarnings: []string{"failed to load progress"},
		}
		got := Route(s)
		if got == nil {
			t.Fatal("expected fallback instruction, got nil")
		}
		if got.Agent != "writer" || got.Chapter != 1 {
			t.Fatalf("expected writer for chapter 1, got %+v", got)
		}
	})

	t.Run("writing phase but returns nil (e.g. steering) with warnings", func(t *testing.T) {
		s := State{
			Progress: &domain.Progress{
				Phase:             domain.PhaseWriting,
				Flow:              domain.FlowSteering,
				CompletedChapters: []int{1},
			},
			LoadWarnings: []string{"some warning"},
		}
		got := Route(s)
		if got == nil {
			t.Fatal("expected fallback instruction, got nil")
		}
		if got.Agent != "writer" || got.Chapter != 2 {
			t.Fatalf("expected writer for next chapter (2), got %+v", got)
		}
	})
}

// TestRoute_ReviewBeforeGateAtMilestone: bất biến — step 10 (editor review) đứng TRƯỚC
// step 10.5 (human gate). Tại mốc gate mà chương chưa review, router ưu tiên editor review,
// không dừng chờ gate. (Tình huống "cả hai pending" không thể sinh từ LoadState mới, nhưng
// Route phải giữ thứ tự này để không tái phát deadlock gate chặn editor.)
func TestRoute_ReviewBeforeGateAtMilestone(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		TotalChapters:     40,
		CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8},
	}
	s := State{
		Progress:           p,
		LastCompleted:      8,
		NeedsReviewChapter: 8,
		HumanGatePending:   true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("tại mốc gate chưa review, router phải ưu tiên editor review, got %+v", got)
	}
}
