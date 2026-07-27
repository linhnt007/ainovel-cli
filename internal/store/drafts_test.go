package store

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/voocel/ainovel-cli/internal/domain"
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

func TestExtractStyleAnchors_AestheticSort(t *testing.T) {
	s := newTestStore(t)

	// Viết các đoạn văn mẫu cho 3 chương, mỗi đoạn thỏa mãn điều kiện làm anchor (50-300 runes, ko quá 2 ngoặc kép)
	para1 := "Đây là đoạn văn của chương một với độ dài vừa đủ để có thể trích xuất làm điểm neo phong cách của truyện."
	para2 := "Đây là đoạn văn của chương hai, chương này sẽ được cho điểm thẩm mỹ cao nhất để kiểm tra tính năng sắp xếp."
	para3 := "Đây là đoạn văn của chương ba, có điểm thẩm mỹ trung bình khá nhằm kiểm định độ chính xác của bộ lọc."

	_ = s.Drafts.SaveDraft(1, para1)
	_ = s.Drafts.SaveFinalChapter(1, para1)
	_ = s.Drafts.SaveDraft(2, para2)
	_ = s.Drafts.SaveFinalChapter(2, para2)
	_ = s.Drafts.SaveDraft(3, para3)
	_ = s.Drafts.SaveFinalChapter(3, para3)

	// Ghi các file review với điểm aesthetic khác nhau
	r1 := domain.ReviewEntry{
		Chapter: 1,
		Dimensions: []domain.DimensionScore{
			{Dimension: "aesthetic", Score: 70},
		},
	}
	r2 := domain.ReviewEntry{
		Chapter: 2,
		Dimensions: []domain.DimensionScore{
			{Dimension: "aesthetic", Score: 90},
		},
	}
	r3 := domain.ReviewEntry{
		Chapter: 3,
		Dimensions: []domain.DimensionScore{
			{Dimension: "aesthetic", Score: 80},
		},
	}

	_ = s.World.SaveReview(r1)
	_ = s.World.SaveReview(r2)
	_ = s.World.SaveReview(r3)

	// Trích xuất 1 anchor, mong muốn nhận được từ chương 2 (điểm 90)
	anchors := s.Drafts.ExtractStyleAnchors(1, 3)
	if len(anchors) != 1 {
		t.Fatalf("expected 1 anchor, got %v", anchors)
	}
	if anchors[0] != para2 {
		t.Errorf("expected anchor from chapter 2 (para2), got: %q", anchors[0])
	}
}
