package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SelectDraftTool chọn draft tốt nhất từ N drafts (Best-of-N).
type SelectDraftTool struct {
	store *store.Store
}

func NewSelectDraftTool(store *store.Store) *SelectDraftTool {
	return &SelectDraftTool{store: store}
}

func (t *SelectDraftTool) Name() string        { return "select_draft" }
func (t *SelectDraftTool) Description() string { return "Chọn draft tốt nhất từ các draft đã tạo. Đánh giá dựa trên độ mạch lạc, nhất quán nhân vật, chất lượng miêu tả, giọng văn." }
func (t *SelectDraftTool) Label() string       { return "Chọn draft tốt nhất" }
func (t *SelectDraftTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SelectDraftTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SelectDraftTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter":  map[string]any{"type": "integer", "description": "Số chương"},
			"selected": map[string]any{"type": "integer", "description": "Draft được chọn (1-based index)"},
			"reason":   map[string]any{"type": "string", "description": "Lý do chọn"},
		},
		"required": []string{"chapter", "selected", "reason"},
	}
}

func (t *SelectDraftTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter  int    `json:"chapter"`
		Selected int    `json:"selected"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.Selected <= 0 {
		return nil, fmt.Errorf("selected must be > 0: %w", errs.ErrToolArgs)
	}

	// Lưu selection vào meta/draft_selections/{chapter:02d}.json
	selection := map[string]any{
		"chapter":  a.Chapter,
		"selected": a.Selected,
		"reason":   a.Reason,
	}
	data, _ := json.MarshalIndent(selection, "", "  ")

	// Sử dụng Drafts store để lưu selection
	if err := t.store.Drafts.SaveSelection(a.Chapter, data); err != nil {
		return nil, fmt.Errorf("save selection: %w: %w", errs.ErrStoreWrite, err)
	}

	result := map[string]any{
		"chapter":  a.Chapter,
		"selected": a.Selected,
		"reason":   a.Reason,
	}
	return json.Marshal(result)
}