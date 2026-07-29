package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveNotebookTool lưu ghi chú của writer (character notes, plot notes, style decisions, world consistency).
type SaveNotebookTool struct {
	store *store.Store
}

func NewSaveNotebookTool(store *store.Store) *SaveNotebookTool {
	return &SaveNotebookTool{store: store}
}

func (t *SaveNotebookTool) Name() string        { return "save_notebook" }
func (t *SaveNotebookTool) Description() string { return "Lưu ghi chú (character notes, plot notes, style decisions, world consistency). Context inject cho writer ở mỗi chapter." }
func (t *SaveNotebookTool) Label() string       { return "Lưu ghi chú" }
func (t *SaveNotebookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveNotebookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveNotebookTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":    map[string]any{"type": "string", "description": "Loại note (character, plot, style, world)"},
			"content": map[string]any{"type": "string", "description": "Nội dung ghi chú"},
			"tags":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Tags liên quan"},
			"chapter": map[string]any{"type": "integer", "description": "Chương tạo note (nếu có)"},
		},
		"required": []string{"type", "content"},
	}
}

func (t *SaveNotebookTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type    string   `json:"type"`
		Content string   `json:"content"`
		Tags    []string `json:"tags,omitempty"`
		Chapter int      `json:"chapter,omitempty"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Type == "" {
		return nil, fmt.Errorf("type is required: %w", errs.ErrToolArgs)
	}
	if a.Content == "" {
		return nil, fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	entry := store.NotebookEntry{
		Type:    a.Type,
		Content: a.Content,
		Tags:    a.Tags,
		Chapter: a.Chapter,
	}

	if err := t.store.Notebooks.Save(entry); err != nil {
		return nil, fmt.Errorf("save notebook: %w: %w", errs.ErrStoreWrite, err)
	}

	result := map[string]any{
		"type":    a.Type,
		"content": a.Content,
		"saved":   true,
	}
	return json.Marshal(result)
}