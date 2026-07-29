package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/stylestat"
)

var sentenceSplit = regexp.MustCompile(`[.!?。！？\n]+`)

func styleStopwords(store *store.Store) []string {
	var words []string
	if chars, err := store.Characters.Load(); err == nil {
		for _, c := range chars {
			words = append(words, c.Name)
			words = append(words, c.Aliases...)
		}
	}
	return words
}

func checkRules(ctx context.Context, store *store.Store, rulesOpts rules.LoadOptions, text string, wordCount int) []rules.Violation {
	violations := rules.Lint(text)
	bundle := rules.Merge(rules.Load(rulesOpts))
	violations = append(violations, rules.Check(text, wordCount, bundle.Structured)...)

	// Kiểm tra độ dài tối thiểu 1500 từ
	if ctx.Value("import_mode") != true && wordCount >= 100 && wordCount < 1500 {
		violations = append(violations, rules.Violation{
			Rule:     "chapter_words_too_short",
			Target:   "Độ dài chương",
			Limit:    1500,
			Actual:   wordCount,
			Severity: rules.SeverityError,
		})
	}

	// 1. Quét lặp câu nội bộ chương (intra-chapter loop checking)
	sentenceMap := make(map[string]int)
	for _, sent := range sentenceSplit.Split(text, -1) {
		sent = strings.Trim(strings.TrimSpace(sent), `"""''「」『』`)
		if len([]rune(sent)) < 12 {
			continue
		}
		sentenceMap[sent]++
	}
	for sent, count := range sentenceMap {
		if count >= 3 {
			violations = append(violations, rules.Violation{
				Rule:     "style_repetition",
				Target:   "Lặp câu nội bộ chương: " + truncateRunes(sent, 30),
				Actual:   count,
				Severity: rules.SeverityWarning,
			})
		}
	}

	// 2. Chạy stylestat thống kê lặp
	var chapters []string
	progress, _ := store.Progress.Load()
	if progress != nil {
		for _, ch := range progress.CompletedChapters {
			if txt, err := store.Drafts.LoadChapterText(ch); err == nil && txt != "" {
				chapters = append(chapters, txt)
			}
		}
	}
	chapters = append(chapters, text)

	var titles []string
	if outline, err := store.Outline.LoadOutline(); err == nil {
		for _, entry := range outline {
			titles = append(titles, entry.Title)
		}
	}

	stats := stylestat.Compute(stylestat.Input{
		Chapters:  chapters,
		Titles:    titles,
		Stopwords: styleStopwords(store),
	})
	if stats != nil {
		// Cảnh báo nếu các khuôn câu AI xuất hiện quá dày đặc
		for _, p := range stats.Patterns {
			if p.PerChapter >= 1.5 {
				violations = append(violations, rules.Violation{
					Rule:     "style_repetition",
					Target:   fmt.Sprintf("Khuôn câu AI '%s'", p.Name),
					Limit:    1.5,
					Actual:   p.PerChapter,
					Severity: rules.SeverityWarning,
				})
			}
		}
		// Cảnh báo lặp câu dài xuyên chương
		for _, s := range stats.RepeatedSentences {
			violations = append(violations, rules.Violation{
				Rule:     "style_repetition",
				Target:   fmt.Sprintf("Lặp câu xuyên %d chương: %s", s.Chapters, s.Text),
				Actual:   s.Count,
				Severity: rules.SeverityWarning,
			})
		}
		// Cảnh báo từ cửa miệng lặp lại nhiều
		for _, ph := range stats.TopPhrases {
			threshold := max(8, len(chapters)/2)
			if ph.Count >= threshold {
				violations = append(violations, rules.Violation{
					Rule:     "style_repetition",
					Target:   fmt.Sprintf("Từ cửa miệng lặp lại: %s", ph.Text),
					Limit:    threshold,
					Actual:   ph.Count,
					Severity: rules.SeverityWarning,
				})
			}
		}
	}

	return violations
}
