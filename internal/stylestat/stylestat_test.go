package stylestat

import (
	"strings"
	"testing"
)

func chapterWith(body string) string {
	return "# 标题\n" + body
}

func TestComputeBelowMinChapters(t *testing.T) {
	in := Input{Chapters: []string{"a", "b", "c", "d"}}
	if Compute(in) != nil {
		t.Fatal("below minChapters should return nil")
	}
}

func TestComputePatterns(t *testing.T) {
	body := "Anh không phải do dự, mà là sợ hãi thật sự. Cả người khẽ run lên một nhịp thở. Mọi thứ tựa như đang chậm lại. Cả căn phòng im lặng.\nChính văn.\n"
	chapters := make([]string, 6)
	for i := range chapters {
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	want := map[string]int{
		"Câu đối lập định nghĩa『không phải…mà là…』":              6,
		"Lượng từ nhịp thở『một/vài/mấy nhịp thở』":                6,
		"So sánh sáo『tựa như/như thể/dường như』":                 6,
		"Nhịp im lặng『im lặng/không nói gì/không nói thành lời』": 6,
	}
	for _, p := range s.Patterns {
		if w, ok := want[p.Name]; ok && p.Total != w {
			t.Errorf("%s total: got %d want %d", p.Name, p.Total, w)
		}
		if p.PerChapter != 1.0 {
			t.Errorf("%s per_chapter: got %v want 1.0", p.Name, p.PerChapter)
		}
	}
	if len(s.Patterns) != 4 {
		t.Errorf("want 4 pattern classes, got %d: %+v", len(s.Patterns), s.Patterns)
	}
}

func TestComputeTopPhrasesWithStopwords(t *testing.T) {
	// "khẽ cau mày" xuất hiện với tần suất cao; "Cửu Uyên" là tên nhân vật nên bị lọc bỏ
	line := "Cửu Uyên khẽ cau mày nhìn xa xăm.\n"
	chapters := make([]string, 10)
	for i := range chapters {
		chapters[i] = chapterWith(strings.Repeat(line, 3))
	}
	s := Compute(Input{Chapters: chapters, Stopwords: []string{"Cửu Uyên"}})
	if s == nil {
		t.Fatal("expected stats")
	}
	var hasPhrase, hasName bool
	for _, p := range s.TopPhrases {
		if strings.Contains(p.Text, "khẽ cau mày") {
			hasPhrase = true
		}
		if strings.Contains(strings.ToLower(p.Text), "uyên") || strings.Contains(strings.ToLower(p.Text), "cửu") {
			hasName = true
		}
	}
	if !hasPhrase {
		t.Errorf("expected 'khẽ cau mày' phrase mined, got %+v", s.TopPhrases)
	}
	if hasName {
		t.Errorf("character name should be filtered, got %+v", s.TopPhrases)
	}
}

func TestComputeRepeatedSentences(t *testing.T) {
	motto := "此生未能远行，望你替我看看远方的山海。"
	chapters := make([]string, 6)
	for i := range chapters {
		body := "平常正文，没有什么重复。\n"
		if i%2 == 0 {
			body += motto + "\n"
		}
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if len(s.RepeatedSentences) == 0 {
		t.Fatalf("expected repeated sentence, got none")
	}
	got := s.RepeatedSentences[0]
	if got.Chapters != 3 || got.Count != 3 {
		t.Errorf("repeated sentence: %+v", got)
	}
	if !strings.HasPrefix(got.Text, "此生未能远行") {
		t.Errorf("text: %q", got.Text)
	}
}

func TestComputeEndingAndOpening(t *testing.T) {
	short := chapterWith("Đêm khuya tĩnh lặng.\nAnh bước đi rất chậm trong bóng tối.\nAnh dừng lại, không nói gì.")
	long := chapterWith("Ban ngày trời nắng.\nAnh bước đi rất chậm trong bóng tối rồi dừng lại nhìn quanh.\nĐây là một câu kết rất dài, kể lể từng chi tiết nhỏ nhặt để vượt quá ngưỡng mười từ quy định cho câu kết ngắn.")
	chapters := []string{short, short, short, long, long}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if s.Ending.ShortRatio != 0.6 {
		t.Errorf("short_ratio: got %v want 0.6", s.Ending.ShortRatio)
	}
	if s.OpeningTimeRate != 0.6 {
		t.Errorf("opening_time_rate: got %v want 0.6", s.OpeningTimeRate)
	}
}

func TestComputeTitleFormats(t *testing.T) {
	chapters := make([]string, 5)
	for i := range chapters {
		chapters[i] = chapterWith("正文。")
	}
	// Dùng lẫn lộn → báo cáo
	s := Compute(Input{Chapters: chapters, Titles: []string{"Chương 1 Gió nổi", "Mây cuộn", "Chương 3 Sấm động"}})
	if s.TitleFormats == nil || s.TitleFormats.WithPrefix != 2 || s.TitleFormats.WithoutPrefix != 1 {
		t.Errorf("title formats: %+v", s.TitleFormats)
	}
	// Đồng nhất → không báo cáo
	s = Compute(Input{Chapters: chapters, Titles: []string{"Gió nổi", "Mây cuộn"}})
	if s.TitleFormats != nil {
		t.Errorf("uniform titles should not report: %+v", s.TitleFormats)
	}
}
