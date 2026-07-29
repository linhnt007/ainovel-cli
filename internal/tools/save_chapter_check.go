package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveChapterCheckTool lưu kết quả kiểm tra chất lượng chương (Light Gate Tier 1).
// Kiểm tra mechanical rules: word count, format, fatigue words.
type SaveChapterCheckTool struct {
	store *store.Store
}

func NewSaveChapterCheckTool(store *store.Store) *SaveChapterCheckTool {
	return &SaveChapterCheckTool{store: store}
}

func (t *SaveChapterCheckTool) Name() string        { return "save_chapter_check" }
func (t *SaveChapterCheckTool) Description() string { return "Lưu kết quả kiểm tra chất lượng chương (word count, format, fatigue words). Không block commit, chỉ ghi nhận findings." }
func (t *SaveChapterCheckTool) Label() string       { return "Kiểm tra chất lượng chương" }
func (t *SaveChapterCheckTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveChapterCheckTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveChapterCheckTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter": map[string]any{
				"type":        "integer",
				"description": "Số chương được kiểm tra",
			},
			"word_count": map[string]any{
				"type":        "integer",
				"description": "Số từ của chương",
			},
			"passed": map[string]any{
				"type":        "boolean",
				"description": "Chương có đạt kiểm tra không",
			},
			"issues": map[string]any{
				"type":        "array",
				"description": "Danh sách vấn đề phát hiện",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"rule":     map[string]any{"type": "string", "description": "Rule vi phạm (word_count_low, format_error, fatigue_word)"},
						"severity": map[string]any{"type": "string", "description": "Mức độ (error, warning, info)"},
						"message":  map[string]any{"type": "string", "description": "Mô tả vấn đề"},
						"target":   map[string]any{"type": "string", "description": "Đoạn văn bản liên quan (nếu có)"},
					},
					"required": []string{"rule", "severity", "message"},
				},
			},
		},
		"required": []string{"chapter", "word_count", "passed"},
	}
}

func (t *SaveChapterCheckTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter   int  `json:"chapter"`
		WordCount int  `json:"word_count"`
		Passed    bool `json:"passed"`
		Issues    []struct {
			Rule     string `json:"rule"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
			Target   string `json:"target,omitempty"`
		} `json:"issues,omitempty"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}

	// Chuyển đổi issues
	var issues []store.CheckIssue
	for _, iss := range a.Issues {
		issues = append(issues, store.CheckIssue{
			Rule:     iss.Rule,
			Severity: iss.Severity,
			Message:  iss.Message,
			Target:   iss.Target,
		})
	}

	check := store.ChapterCheck{
		Chapter:   a.Chapter,
		Tier:      1, // Mechanical check
		WordCount: a.WordCount,
		Passed:    a.Passed,
		Issues:    issues,
	}

	if err := t.store.Checks.Save(check); err != nil {
		return nil, fmt.Errorf("save check: %w: %w", errs.ErrStoreWrite, err)
	}

	// Tóm tắt kết quả
	issueSummary := ""
	if len(issues) > 0 {
		var warnings, errors []string
		for _, iss := range issues {
			if iss.Severity == "error" {
				errors = append(errors, iss.Rule)
			} else {
				warnings = append(warnings, iss.Rule)
			}
		}
		parts := []string{}
		if len(errors) > 0 {
			parts = append(parts, fmt.Sprintf("%d errors (%s)", len(errors), strings.Join(errors, ", ")))
		}
		if len(warnings) > 0 {
			parts = append(parts, fmt.Sprintf("%d warnings (%s)", len(warnings), strings.Join(warnings, ", ")))
		}
		issueSummary = strings.Join(parts, ", ")
	}

	result := map[string]any{
		"chapter":     a.Chapter,
		"word_count":  a.WordCount,
		"passed":      a.Passed,
		"issue_count": len(issues),
		"summary":     issueSummary,
		"tier":        1,
	}

	return json.Marshal(result)
}