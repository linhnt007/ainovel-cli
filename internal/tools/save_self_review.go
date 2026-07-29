package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SelfReview kết quả tự đánh giá của writer cho một chương.
type SelfReview struct {
	Chapter      int      `json:"chapter"`
	ReviewedAt   string   `json:"reviewed_at"`
	IssuesFound  []string `json:"issues_found,omitempty"`
	Fixed        []string `json:"fixed,omitempty"`
	Remaining    []string `json:"remaining,omitempty"`
	Confidence   int      `json:"confidence"` // 0-100
}

// SaveSelfReviewTool lưu kết quả tự đánh giá của writer.
type SaveSelfReviewTool struct {
	store *store.Store
}

func NewSaveSelfReviewTool(store *store.Store) *SaveSelfReviewTool {
	return &SaveSelfReviewTool{store: store}
}

func (t *SaveSelfReviewTool) Name() string        { return "save_self_review" }
func (t *SaveSelfReviewTool) Description() string { return "Lưu kết quả tự đánh giá chương vừa viết. Ghi nhận issues đã tìm, đã sửa, và còn tồn đọng." }
func (t *SaveSelfReviewTool) Label() string       { return "Tự đánh giá chương" }
func (t *SaveSelfReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveSelfReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveSelfReviewTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter":       map[string]any{"type": "integer", "description": "Số chương"},
			"issues_found":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Danh sách vấn đề phát hiện"},
			"fixed":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Danh sách vấn đề đã sửa"},
			"remaining":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Danh sách vấn đề còn tồn đọng"},
			"confidence":    map[string]any{"type": "integer", "description": "Độ tự tin (0-100)"},
		},
		"required": []string{"chapter", "confidence"},
	}
}

func (t *SaveSelfReviewTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter     int      `json:"chapter"`
		IssuesFound []string `json:"issues_found,omitempty"`
		Fixed       []string `json:"fixed,omitempty"`
		Remaining   []string `json:"remaining,omitempty"`
		Confidence  int      `json:"confidence"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.Confidence < 0 || a.Confidence > 100 {
		return nil, fmt.Errorf("confidence must be 0-100: %w", errs.ErrToolArgs)
	}

	review := SelfReview{
		Chapter:     a.Chapter,
		ReviewedAt:  time.Now().Format(time.RFC3339),
		IssuesFound: a.IssuesFound,
		Fixed:       a.Fixed,
		Remaining:   a.Remaining,
		Confidence:  a.Confidence,
	}

	data, err := json.MarshalIndent(review, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal self_review: %w", err)
	}

	// Lưu vào meta/self_reviews/{chapter:02d}.json
	dir := filepath.Join(t.store.Dir(), "meta", "self_reviews")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create self_reviews dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%02d.json", a.Chapter))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("save self_review: %w: %w", errs.ErrStoreWrite, err)
	}

	result := map[string]any{
		"chapter":      a.Chapter,
		"issues_found": len(a.IssuesFound),
		"fixed":        len(a.Fixed),
		"remaining":    len(a.Remaining),
		"confidence":   a.Confidence,
	}
	return json.Marshal(result)
}