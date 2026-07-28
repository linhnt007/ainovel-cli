package ctxpack

import (
	"context"
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

func TestKeepRewriteBriefStrategy_Apply(t *testing.T) {
	strategy := NewKeepRewriteBriefStrategy()

	t.Run("contains rewrite_brief", func(t *testing.T) {
		msgs := []agentcore.AgentMessage{
			agentcore.UserMsg("Hello"),
			agentcore.Message{
				Role:    agentcore.RoleTool,
				Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"rewrite_brief": {"reason": "fix flow"}}`)},
			},
			agentcore.UserMsg("World"),
		}

		out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Applied {
			t.Fatal("expected strategy to be applied")
		}

		m, ok := out[1].(agentcore.Message)
		if !ok {
			t.Fatalf("expected agentcore.Message, got %T", out[1])
		}
		if m.Metadata["Pinned"] != true {
			t.Fatalf("expected Pinned metadata to be true, got %v", m.Metadata["Pinned"])
		}
	})

	t.Run("no rewrite_brief", func(t *testing.T) {
		msgs := []agentcore.AgentMessage{
			agentcore.UserMsg("Hello"),
			agentcore.UserMsg("World"),
		}

		out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Applied {
			t.Fatal("expected strategy to not be applied")
		}
		if len(out) != len(msgs) {
			t.Fatalf("expected same number of messages, got %d", len(out))
		}
	})
}
