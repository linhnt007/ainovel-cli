package rules

import (
	"fmt"
	"regexp"
	"strings"
)

// Check thực hiện kiểm tra cơ học nội dung chương theo các quy tắc có cấu trúc, trả về danh sách vi phạm thực tế.
//
// Hợp đồng thiết kế:
//   - Chỉ trả về sự thật, không ra lệnh (nguyên tắc sắt)
//   - Không chặn bất kỳ luồng gọi nào
//   - severity được ánh xạ cố định theo loại quy tắc (xem bảng chú thích trong types.go)
//
// Tham số:
//   - text: nội dung chương (bản cuối hoặc bản nháp đều được)
//   - wordCount: số từ của chương (đếm theo từ tách khoảng trắng, xem CountWords). Nếu <0, checker tự tính để tránh caller quét O(n) lặp lại.
//   - s: quy tắc có cấu trúc đã hợp nhất; nếu IsEmpty thì trả về nil luôn.
func Check(text string, wordCount int, s Structured) []Violation {
	if wordCount < 0 {
		wordCount = CountWords(text)
	}

	var violations []Violation
	violations = appendForbiddenChars(violations, text, s.ForbiddenChars)
	violations = appendForbiddenPhrases(violations, text, s.ForbiddenPhrases)
	violations = appendFatigueWords(violations, text, s.FatigueWords)
	violations = appendChapterWords(violations, wordCount, s.ChapterWords)
	violations = appendParagraphStyle(violations, text)
	return violations
}

// paragraph_style: kiểm tra văn phong ngắt dòng cụt lủn.
var sentenceSplitter = regexp.MustCompile(`[.!?。！？\n]+`)

func appendParagraphStyle(vs []Violation, text string) []Violation {
	paragraphs := strings.Split(text, "\n\n")
	var validParagraphs []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			validParagraphs = append(validParagraphs, p)
		}
	}
	if len(validParagraphs) < 5 {
		return vs
	}
	singleSentenceCount := 0
	for _, p := range validParagraphs {
		sents := sentenceSplitter.Split(p, -1)
		realSents := 0
		for _, s := range sents {
			s = strings.TrimSpace(s)
			if len([]rune(s)) > 3 {
				realSents++
			}
		}
		if realSents <= 1 {
			singleSentenceCount++
		}
	}
	ratio := float64(singleSentenceCount) / float64(len(validParagraphs))
	if ratio > 0.40 {
		vs = append(vs, Violation{
			Rule:     "paragraph_style",
			Target:   "Quá nhiều đoạn văn 1 câu cụt lủn (cần ghép ít nhất 3 câu liên kết mỗi đoạn)",
			Limit:    "≤ 40% đoạn 1 câu",
			Actual:   fmt.Sprintf("%.1f%% đoạn 1 câu (%d/%d)", ratio*100, singleSentenceCount, len(validParagraphs)),
			Severity: SeverityWarning,
		})
	}
	return vs
}

// forbidden_chars: xuất hiện ≥1 lần là error.
// Mỗi quy tắc chỉ tạo một violation, actual là số lần xuất hiện.
func appendForbiddenChars(vs []Violation, text string, list []string) []Violation {
	for _, ch := range list {
		if ch == "" {
			continue
		}
		n := strings.Count(text, ch)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_chars",
			Target:   ch,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// forbidden_phrases: xuất hiện ≥1 lần là error; hành vi giống forbidden_chars, chỉ khác tên rule.
func appendForbiddenPhrases(vs []Violation, text string, list []string) []Violation {
	for _, ph := range list {
		if ph == "" {
			continue
		}
		n := strings.Count(text, ph)
		if n == 0 {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "forbidden_phrases",
			Target:   ph,
			Actual:   n,
			Severity: SeverityError,
		})
	}
	return vs
}

// fatigue_words: vi phạm khi số lần xuất hiện trong chương vượt ngưỡng, mức warning.
// Không tích lũy qua nhiều chương — vấn đề liên chương sẽ xử lý sau bằng công cụ chẩn đoán.
func appendFatigueWords(vs []Violation, text string, m map[string]int) []Violation {
	for word, limit := range m {
		if word == "" || limit <= 0 {
			continue
		}
		n := strings.Count(text, word)
		if n <= limit {
			continue
		}
		vs = append(vs, Violation{
			Rule:     "fatigue_words",
			Target:   word,
			Limit:    limit,
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	return vs
}

// chapter_words: độ lệch số từ.
func appendChapterWords(vs []Violation, wordCount int, rng *WordRange) []Violation {
	if rng == nil {
		return vs
	}
	var deviation float64
	switch {
	case wordCount < rng.Min:
		if rng.Min == 0 {
			return vs
		}
		deviation = float64(rng.Min-wordCount) / float64(rng.Min)
	case wordCount > rng.Max:
		if rng.Max == 0 {
			return vs
		}
		deviation = float64(wordCount-rng.Max) / float64(rng.Max)
	default:
		return vs // trong phạm vi cho phép
	}

	vs = append(vs, Violation{
		Rule:      "chapter_words",
		Limit:     fmt.Sprintf("%d-%d", rng.Min, rng.Max),
		Actual:    wordCount,
		Deviation: deviation,
		Severity:  SeverityWarning,
	})
	return vs
}

// CountWords đếm số TỪ THẬT của văn bản, tách theo khoảng trắng (strings.Fields tự lọc token rỗng).
// Dùng riêng cho rule chapter_words — khác với domain.WordCount (đếm rune, dùng cho hiển thị/tổng số từ
// tiến độ/thống kê phong cách), vốn đếm ký tự chứ không phải từ và không phù hợp làm ngưỡng "số từ mỗi chương"
// cho văn bản tiếng Việt (rune tiếng Việt không tương đương Hán tự về mật độ thông tin).
func CountWords(text string) int {
	return len(strings.Fields(text))
}

