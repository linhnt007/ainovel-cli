package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReviewVote kết quả vote của editor cho một chương (Ensemble Review).
type ReviewVote struct {
	Chapter    int               `json:"chapter"`
	EditorID   string            `json:"editor_id"` // "editor_1", "editor_2", "editor_3"
	VotedAt    string            `json:"voted_at"`
	Score      int               `json:"score"` // 0-100
	Dimensions map[string]int    `json:"dimensions,omitempty"`
	Verdict    string            `json:"verdict"` // "approve", "revise", "reject"
	Notes      string            `json:"notes,omitempty"`
}

// SaveReviewVoteTool lưu vote của editor (Ensemble Review).
type SaveReviewVoteTool struct {
	store *store.Store
}

func NewSaveReviewVoteTool(store *store.Store) *SaveReviewVoteTool {
	return &SaveReviewVoteTool{store: store}
}

func (t *SaveReviewVoteTool) Name() string        { return "save_review_vote" }
func (t *SaveReviewVoteTool) Description() string { return "Lưu vote đánh giá chương từ editor. Dùng cho Ensemble Review (2+1 tiebreaker)." }
func (t *SaveReviewVoteTool) Label() string       { return "Lưu vote đánh giá" }
func (t *SaveReviewVoteTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewVoteTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveReviewVoteTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"chapter":    map[string]any{"type": "integer", "description": "Số chương"},
			"editor_id":  map[string]any{"type": "string", "description": "ID editor (editor_1, editor_2, editor_3)"},
			"score":      map[string]any{"type": "integer", "description": "Điểm tổng (0-100)"},
			"dimensions": map[string]any{"type": "object", "description": "Điểm theo từng dimension", "additionalProperties": map[string]any{"type": "integer"}},
			"verdict":    map[string]any{"type": "string", "description": "Phán quyết (approve, revise, reject)"},
			"notes":      map[string]any{"type": "string", "description": "Ghi chú"},
		},
		"required": []string{"chapter", "editor_id", "score", "verdict"},
	}
}

func (t *SaveReviewVoteTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int            `json:"chapter"`
		EditorID   string         `json:"editor_id"`
		Score      int            `json:"score"`
		Dimensions map[string]int `json:"dimensions,omitempty"`
		Verdict    string         `json:"verdict"`
		Notes      string         `json:"notes,omitempty"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.EditorID == "" {
		return nil, fmt.Errorf("editor_id is required: %w", errs.ErrToolArgs)
	}
	if a.Score < 0 || a.Score > 100 {
		return nil, fmt.Errorf("score must be 0-100: %w", errs.ErrToolArgs)
	}
	if a.Verdict == "" {
		return nil, fmt.Errorf("verdict is required: %w", errs.ErrToolArgs)
	}

	vote := ReviewVote{
		Chapter:    a.Chapter,
		EditorID:   a.EditorID,
		VotedAt:    time.Now().Format(time.RFC3339),
		Score:      a.Score,
		Dimensions: a.Dimensions,
		Verdict:    a.Verdict,
		Notes:      a.Notes,
	}

	data, err := json.MarshalIndent(vote, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal vote: %w", err)
	}

	// Lưu vào meta/review_votes/{chapter:02d}_{editor_id}.json
	if err := t.store.World.SaveReviewVote(a.Chapter, a.EditorID, data); err != nil {
		return nil, fmt.Errorf("save vote: %w: %w", errs.ErrStoreWrite, err)
	}

	result := map[string]any{
		"chapter":   a.Chapter,
		"editor_id": a.EditorID,
		"score":     a.Score,
		"verdict":   a.Verdict,
	}
	return json.Marshal(result)
}