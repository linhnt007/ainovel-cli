package agents

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func subagentCall(args string) agentcore.GateRequest {
	return agentcore.GateRequest{
		Call: agentcore.ToolCall{Name: "subagent", Args: json.RawMessage(args)},
	}
}

func toolCall(name string) agentcore.GateRequest {
	return agentcore.GateRequest{
		Call: agentcore.ToolCall{Name: name},
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Progress.Init("test", 10); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return st
}

func TestCompletePhaseGate_BlocksSubagentAtComplete(t *testing.T) {
	st := newTestStore(t)
	if err := st.Progress.UpdatePhase(domain.PhaseComplete); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}

	gate := qualityControlGate(st, bootstrap.Config{})
	decision, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"写第 1 章"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision == nil || decision.Allowed {
		t.Fatal("expected gate to block subagent at PhaseComplete")
	}
	if decision.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestCompletePhaseGate_AllowsSubagentWhenWriting(t *testing.T) {
	st := newTestStore(t)

	gate := qualityControlGate(st, bootstrap.Config{})
	decision, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"写第 1 章"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != nil && !decision.Allowed {
		t.Fatal("expected gate to allow subagent during Writing phase")
	}
}

func TestCompletePhaseGate_AllowsNonSubagentAtComplete(t *testing.T) {
	st := newTestStore(t)
	if err := st.Progress.UpdatePhase(domain.PhaseComplete); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}

	gate := qualityControlGate(st, bootstrap.Config{})
	for _, name := range []string{"novel_context", "ask_user"} {
		decision, err := gate(context.Background(), toolCall(name))
		if err != nil {
			t.Fatalf("tool %s: unexpected error: %v", name, err)
		}
		if decision != nil && !decision.Allowed {
			t.Fatalf("tool %s: expected allow, got block", name)
		}
	}
}

func TestCompletePhaseGate_AllowsWhenNoProgress(t *testing.T) {
	st := store.NewStore(t.TempDir())

	gate := qualityControlGate(st, bootstrap.Config{})
	decision, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"写第 1 章"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision != nil && !decision.Allowed {
		t.Fatal("expected gate to allow when progress is nil")
	}
}

func TestQualityControlGate_BlocksHumanGatePending(t *testing.T) {
	st := newTestStore(t)
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1}
	p.HumanGateEvery = 1
	_ = st.Progress.Save(p)
	// Gate chỉ arm sau khi review xong → phải lưu review chương 1 trước.
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch1: %v", err)
	}

	cfg := bootstrap.Config{}
	cfg.Quality.HumanGateEvery = 1

	gate := qualityControlGate(st, cfg)
	decision, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"Viết chương 2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision == nil || decision.Allowed {
		t.Fatal("expected qualityControlGate to block subagent call when HumanGatePending=true")
	}
}

// TestQualityControlGate_GateWaitsForEditorReviewAtMilestone: regression bug deadlock.
// Mốc gate chưa review → editor review ĐƯỢC phép (gate chưa arm); sau khi review → gate
// arm chặn mọi subagent; sau khi user ack → hết chặn.
func TestQualityControlGate_GateWaitsForEditorReviewAtMilestone(t *testing.T) {
	st := newTestStore(t)
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3, 4, 5, 6, 7, 8}
	p.HumanGateEvery = 2
	_ = st.Progress.Save(p)

	gate := qualityControlGate(st, bootstrap.Config{})

	// (i) Mốc gate chương 8 CHƯA review → gate chưa arm, editor review được phép.
	decEditor, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor unreviewed milestone: unexpected error: %v", err)
	}
	if decEditor != nil && !decEditor.Allowed {
		t.Fatalf("(i) mốc gate chưa review → editor review phải được phép, got block %q", decEditor.Reason)
	}

	// writer vẫn bị chặn vì còn nợ review (NeedsReviewChapter>0).
	decWriter, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"Viết chương 9"}`))
	if err != nil {
		t.Fatalf("writer unreviewed milestone: unexpected error: %v", err)
	}
	if decWriter == nil || decWriter.Allowed {
		t.Fatal("(i) chưa review → writer vẫn phải bị chặn (NeedsReviewChapter>0)")
	}

	// (ii) Đã review, chưa ack → gate arm, MỌI subagent bị chặn (kể cả editor).
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	decEditor2, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor reviewed milestone: unexpected error: %v", err)
	}
	if decEditor2 == nil || decEditor2.Allowed {
		t.Fatal("(ii) đã review chưa ack → editor cũng phải bị chặn (gate pending)")
	}

	// (iii) User đã ack → gate hết pending, subagent lại được phép.
	if err := st.World.SaveHumanGateAck(8, "ok"); err != nil {
		t.Fatalf("save ack ch8: %v", err)
	}
	decEditor3, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor acked milestone: unexpected error: %v", err)
	}
	if decEditor3 != nil && !decEditor3.Allowed {
		t.Fatalf("(iii) đã ack → editor được phép, got block %q", decEditor3.Reason)
	}
}

func TestQualityControlGate_BlocksWriterOnPendingReview(t *testing.T) {
	st := newTestStore(t)
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1}
	_ = st.Progress.Save(p)

	cfg := bootstrap.Config{}
	cfg.Quality.ReviewInterval = 1

	gate := qualityControlGate(st, cfg)

	// Writer call bị chặn
	decWriter, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"Viết chương 2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decWriter == nil || decWriter.Allowed {
		t.Fatal("expected qualityControlGate to block writer when NeedsReviewChapter>0")
	}

	// Editor call cho phép
	decEditor, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Review"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decEditor != nil && !decEditor.Allowed {
		t.Fatal("expected qualityControlGate to ALLOW editor when NeedsReviewChapter>0")
	}
}
