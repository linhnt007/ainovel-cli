package store

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voocel/ainovel-cli/internal/rules"
)

// TestLoadChapterContent_CountsWordsNotRunes bảo vệ chống bug đã sửa ở part-A: LoadChapterContent
// từng trả utf8.RuneCountInString (đúng cho tiếng Trung, sai cho tiếng Việt vì 1 từ ≈ 4-5 rune).
// Giờ phải trả rules.CountWords — dùng fixture "con " lặp lại để rune count và word count cố
// tình lệch nhau, tránh test tình cờ pass với cả hai cách đếm.
func TestLoadChapterContent_CountsWordsNotRunes(t *testing.T) {
	s := newTestStore(t)

	const wantWords = 3500
	content := strings.TrimSpace(strings.Repeat("con ", wantWords))
	if utf8.RuneCountInString(content) == wantWords {
		t.Fatalf("fixture invalid: rune count phải khác word count để test có ý nghĩa")
	}

	if err := s.Drafts.SaveDraft(1, content); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	gotText, gotCount, err := s.Drafts.LoadChapterContent(1)
	if err != nil {
		t.Fatalf("LoadChapterContent: %v", err)
	}
	if gotText != content {
		t.Errorf("text mismatch")
	}
	if gotCount != wantWords {
		t.Errorf("wordCount=%d, want %d (đếm theo từ, không phải rune)", gotCount, wantWords)
	}

	// Đúng ngữ nghĩa: chương 3500 từ nằm trong range 3000-6000 → Check không được báo
	// chapter_words vi phạm (trước khi sửa, 3500 từ ≈ 13996 rune sẽ bị báo "quá dài" oan).
	rng := &rules.WordRange{Min: 3000, Max: 6000}
	vs := rules.Check(gotText, gotCount, rules.Structured{ChapterWords: rng})
	for _, v := range vs {
		if v.Rule == "chapter_words" {
			t.Errorf("expected no chapter_words violation for %d words in [3000,6000], got %+v", gotCount, v)
		}
	}
}

// TestLoadChapterContent_EmptyDraft đảm bảo hành vi rỗng không đổi sau khi thay đổi cách đếm.
func TestLoadChapterContent_EmptyDraft(t *testing.T) {
	s := newTestStore(t)

	text, count, err := s.Drafts.LoadChapterContent(1)
	if err != nil {
		t.Fatalf("LoadChapterContent: %v", err)
	}
	if text != "" || count != 0 {
		t.Errorf("want (\"\", 0), got (%q, %d)", text, count)
	}
}
