package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// NotebookEntry một ghi chú trong notebook.
type NotebookEntry struct {
	ID        string   `json:"id"`
	Type      string   `json:"type"` // "character", "plot", "style", "world"
	Content   string   `json:"content"`
	Tags      []string `json:"tags,omitempty"`
	CreatedAt string   `json:"created_at"`
	Chapter   int      `json:"chapter,omitempty"` // chương tạo note
}

// NotebookStore lưu trữ ghi chú của writer.
type NotebookStore struct {
	io *IO
}

// NewNotebookStore tạo NotebookStore.
func NewNotebookStore(io *IO) *NotebookStore {
	return &NotebookStore{io: io}
}

// Save lưu notebook entry.
func (s *NotebookStore) Save(entry NotebookEntry) error {
	if entry.CreatedAt == "" {
		entry.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%s_%d", entry.Type, time.Now().UnixNano())
	}

	// Load existing entries
	entries, _ := s.LoadAll()
	entries = append(entries, entry)

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notebook: %w", err)
	}
	return s.io.WriteFile("meta/notebook.json", data)
}

// LoadAll tải tất cả notebook entries.
func (s *NotebookStore) LoadAll() ([]NotebookEntry, error) {
	data, err := s.io.ReadFile("meta/notebook.json")
	if err != nil {
		return nil, err
	}
	var entries []NotebookEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("unmarshal notebook: %w", err)
	}
	return entries, nil
}

// LoadByType tải entries theo type.
func (s *NotebookStore) LoadByType(entryType string) ([]NotebookEntry, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	var filtered []NotebookEntry
	for _, e := range all {
		if e.Type == entryType {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// LoadRecent tải N entries gần nhất.
func (s *NotebookStore) LoadRecent(n int) ([]NotebookEntry, error) {
	all, err := s.LoadAll()
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > len(all) {
		n = len(all)
	}
	return all[len(all)-n:], nil
}