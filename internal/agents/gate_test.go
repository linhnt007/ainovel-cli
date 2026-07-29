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

	gate := completePhaseGate(st)
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

	gate := completePhaseGate(st)
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

	gate := completePhaseGate(st)
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

	gate := completePhaseGate(st)
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
	_ = st.Progress.Save(p)

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
		t.Fatal("expected qualityControlGate to block writer when HasPendingFlatReview=true")
	}

	// Editor call cho phép
	decEditor, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Review"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decEditor != nil && !decEditor.Allowed {
		t.Fatal("expected qualityControlGate to ALLOW editor when HasPendingFlatReview=true")
	}
}
