package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// ChapterCheck kết quả kiểm tra chất lượng một chương.
// Lưu vào meta/checks/{chapter:02d}.json.
type ChapterCheck struct {
	Chapter    int               `json:"chapter"`
	Tier       int               `json:"tier"`        // 1 = mechanical, 2 = editor review
	CheckedAt  string            `json:"checked_at"`  // RFC3339
	WordCount  int               `json:"word_count"`
	Passed     bool              `json:"passed"`
	Issues     []CheckIssue      `json:"issues,omitempty"`
	EditorNote string            `json:"editor_note,omitempty"` // Tier 2 only
	Score      int               `json:"score,omitempty"`       // Tier 2 only (0-100)
}

// CheckIssue một vấn đề được phát hiện.
type CheckIssue struct {
	Rule     string `json:"rule"`      // ví dụ: "word_count_low", "fatigue_word", "format_error"
	Severity string `json:"severity"`  // "error", "warning", "info"
	Message  string `json:"message"`
	Target   string `json:"target,omitempty"` // đoạn văn bản liên quan
}

// CheckStore lưu trữ kết quả kiểm tra chất lượng chương.
type CheckStore struct {
	io *IO
}

// NewCheckStore tạo CheckStore.
func NewCheckStore(io *IO) *CheckStore {
	return &CheckStore{io: io}
}

// Save lưu kết quả kiểm tra chương.
func (s *CheckStore) Save(check ChapterCheck) error {
	if check.CheckedAt == "" {
		check.CheckedAt = time.Now().Format(time.RFC3339)
	}
	return s.io.WriteJSON(fmt.Sprintf("meta/checks/%02d.json", check.Chapter), check)
}

// Load tải kết quả kiểm tra chương.
func (s *CheckStore) Load(chapter int) (*ChapterCheck, error) {
	data, err := s.io.ReadFile(fmt.Sprintf("meta/checks/%02d.json", chapter))
	if err != nil {
		return nil, err
	}
	var check ChapterCheck
	if err := json.Unmarshal(data, &check); err != nil {
		return nil, fmt.Errorf("unmarshal check: %w", err)
	}
	return &check, nil
}

// HasCheck kiểm tra xem chương đã được kiểm tra chưa.
func (s *CheckStore) HasCheck(chapter int) bool {
	_, err := s.io.ReadFile(fmt.Sprintf("meta/checks/%02d.json", chapter))
	return err == nil
}

// LoadLastTier2 tải kết quả Tier 2 check gần nhất (chapter <= maxChapter).
func (s *CheckStore) LoadLastTier2(maxChapter int) (*ChapterCheck, error) {
	for ch := maxChapter; ch > 0; ch-- {
		check, err := s.Load(ch)
		if err != nil {
			continue
		}
		if check.Tier == 2 {
			return check, nil
		}
	}
	return nil, nil
}