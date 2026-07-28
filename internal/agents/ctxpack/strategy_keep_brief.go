package ctxpack

import (
	"context"
	"strings"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

// KeepRewriteBriefStrategy bảo vệ message chứa rewrite_brief khỏi bị nén hoặc cắt bỏ.
type KeepRewriteBriefStrategy struct{}

// NewKeepRewriteBriefStrategy tạo instance mới của KeepRewriteBriefStrategy.
func NewKeepRewriteBriefStrategy() *KeepRewriteBriefStrategy {
	return &KeepRewriteBriefStrategy{}
}

// Name trả về tên của strategy.
func (s *KeepRewriteBriefStrategy) Name() string {
	return "keep_rewrite_brief"
}

// Apply duyệt qua các message, nếu chứa rewrite_brief thì đánh dấu Pinned = true trong metadata.
func (s *KeepRewriteBriefStrategy) Apply(ctx context.Context, _ []agentcore.AgentMessage, view []agentcore.AgentMessage, _ corecontext.Budget) ([]agentcore.AgentMessage, corecontext.StrategyResult, error) {
	applied := false
	result := make([]agentcore.AgentMessage, len(view))
	for i, msg := range view {
		result[i] = msg
		if m, ok := msg.(agentcore.Message); ok {
			if strings.Contains(m.TextContent(), "rewrite_brief") {
				// Sao chép metadata để tránh side effect và gắn Pinned=true
				meta := make(map[string]any)
				for k, v := range m.Metadata {
					meta[k] = v
				}
				meta["Pinned"] = true
				m.Metadata = meta
				result[i] = m
				applied = true
			}
		}
	}
	return result, corecontext.StrategyResult{
		Name:    s.Name(),
		Applied: applied,
	}, nil
}
