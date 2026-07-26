// Package stylestat thực hiện thống kê phong cách toàn bộ tác phẩm dựa trên phần chính văn đã viết,
// chỉ xuất ra các con số thực tế khách quan.
//
// Động lực: Cửa sổ đánh giá nội cung (~10 chương) vốn mù với các khuôn mẫu cố định ở cấp toàn tác phẩm —
// tic câu trúc có thể xuất hiện vài chục lần mỗi chương, cuối chương đồng dạng, lặp lại xuyên chương.
// Nhìn từng chương riêng lẻ thì mỗi chỗ đều "bình thường", chỉ thống kê toàn tác phẩm mới phơi bày được.
// Thống kê giao cho code (xác định, không ảo giác), phán xét giao cho LLM (editor căn cứ số liệu
// để đánh giá từng chiều, writer dựa đó tự tránh).
//
// Lưu ý ngôn ngữ: bộ dò tìm bên dưới được hiệu chỉnh cho tiếng Việt (đơn vị "từ" = token tách theo
// khoảng trắng, không phải rune như tiếng Trung). Tiếng Việt không dán liền âm tiết nên n-gram và
// ngưỡng độ dài đều tính theo số từ thay vì số ký tự.
package stylestat

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// minChapters — ít hơn số chương này thì không xuất thống kê: mẫu quá nhỏ, tần suất không có ý nghĩa.
const minChapters = 5

// phraseWindow — khai thác cụm từ động chỉ xét N chương gần nhất: writer cần tránh "cửa miệng hiện tại".
const phraseWindow = 20

// Input là dữ liệu đầu vào để thống kê. Chapters xếp tăng dần theo số chương; Stopwords là
// danh từ riêng như tên nhân vật — bỏ qua khi khai thác cụm từ động (tên xuất hiện tự nhiên
// có tần suất cao, không phải vấn đề phong cách).
type Input struct {
	Chapters  []string
	Titles    []string
	Stopwords []string
}

// Stats là kết quả thống kê phong cách toàn tác phẩm. Tất cả trường đều là số liệu thực tế,
// không chứa bất kỳ nhận định hay chỉ thị nào.
type Stats struct {
	Chapters          int            `json:"chapters"`
	Patterns          []PatternStat  `json:"patterns,omitempty"`
	TopPhrases        []PhraseStat   `json:"top_phrases,omitempty"`
	RepeatedSentences []SentenceStat `json:"repeated_sentences,omitempty"`
	Ending            EndingStat     `json:"ending"`
	OpeningTimeRate   float64        `json:"opening_time_rate"`
	TitleFormats      *TitleStat     `json:"title_formats,omitempty"`
}

// PatternStat là số đếm toàn tác phẩm cho một lớp khuôn câu cố định (tic văn phong AI phổ biến).
type PatternStat struct {
	Name       string  `json:"name"`
	Total      int     `json:"total"`
	PerChapter float64 `json:"per_chapter"`
}

// PhraseStat là cụm từ xuất hiện nhiều được khai thác trong phraseWindow chương gần nhất.
type PhraseStat struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// SentenceStat là câu dài lặp lại từng chữ xuyên nhiều chương (bằng chứng trực tiếp của lặp lại phơi bày).
type SentenceStat struct {
	Text     string `json:"text"`
	Chapters int    `json:"chapters"`
	Count    int    `json:"count"`
}

// EndingStat là phân bố hình thức dòng cuối chương. Kết thúc ngắn tự nó hợp lệ,
// chỉ khi đồng dạng toàn tác phẩm mới là vấn đề.
//
// Lưu ý: JSON key `median_runes` giữ nguyên vì downstream (buildStyleStats, writer.md, editor.md)
// tham chiếu theo tên trường này; giá trị bên trong nay là số TỪ (whitespace token) của dòng cuối
// chứ không còn là số rune — tiếng Việt không cô đọng ký tự/nghĩa như tiếng Trung nên đếm rune vô nghĩa.
type EndingStat struct {
	ShortRatio float64 `json:"short_ratio"`
	// MedianWords giữ nguyên JSON key `median_runes` (downstream tham chiếu theo tên này);
	// tên trường Go đổi sang MedianWords để phản ánh đúng đơn vị tính mới (số từ, không phải rune).
	MedianWords int `json:"median_runes"`
}

// TitleStat là số đếm việc dùng lẫn lộn tiền tố "Chương N" trong tiêu đề chương
// (dùng lẫn = dấu vết cơ chế lộ ra trong sản phẩm).
type TitleStat struct {
	WithPrefix    int `json:"with_prefix"`
	WithoutPrefix int `json:"without_prefix"`
}

// patternDefs là các khuôn câu AI phổ biến của tiếng Việt. Hạt giống lấy từ
// assets/references/anti-ai-tone.md (mục II, câu đối lập định nghĩa / so sánh sáo) và
// assets/rules/default.md (fatigue_words: tựa như, như thể, dường như, im lặng, không nói gì,
// một/vài/mấy nhịp thở). Số đếm là xấp xỉ (regex không phân tích ngữ pháp), mục đích là so sánh
// theo chiều dọc với đường cơ sở của chính tác phẩm, độ chính xác tuyệt đối không quan trọng.
var patternDefs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"Câu đối lập định nghĩa『không phải…mà là…』", regexp.MustCompile(`(?i)không phải[^.!?\n]{1,40}?mà\s+là`)},
	{"Lượng từ nhịp thở『một/vài/mấy nhịp thở』", regexp.MustCompile(`(?i)(một|vài|mấy|hai|ba)\s+nhịp\s+thở`)},
	{"So sánh sáo『tựa như/như thể/dường như』", regexp.MustCompile(`(?i)tựa như|như thể|dường như`)},
	{"Nhịp im lặng『im lặng/không nói gì/không nói thành lời』", regexp.MustCompile(`(?i)im lặng|không nói gì|không nói thành lời`)},
}

var (
	// sentenceSplit tách câu theo dấu kết câu ASCII lẫn dấu toàn góc (giữ dấu cũ để không vỡ
	// văn bản còn sót ký tự Trung, ví dụ nội dung nhập/di trú từ nguồn cũ).
	sentenceSplit = regexp.MustCompile(`[.!?。！？\n]+`)
	// openingTimeRe — từ thời gian mở đầu tiếng Việt thường gặp trong câu sáo "mở chương bằng
	// đêm/sáng sớm/thức dậy" mà writer.md đã cảnh báo tránh.
	openingTimeRe = regexp.MustCompile(`(?i)đêm|sáng sớm|bình minh|rạng đông|hoàng hôn|tỉnh dậy|thức dậy|ban mai`)
	// titlePrefixRe khớp tiền tố "Chương N" theo đúng định dạng thực tế
	// (assets/references/chapter-template.md: "# Chương [X]: [Tiêu đề chương]").
	titlePrefixRe = regexp.MustCompile(`(?i)^#{0,2}\s*chương\s*\d+`)
)

// shortEndingWords — dòng cuối không vượt quá số từ này thì tính là "kết thúc ngắn".
// Tiếng Việt là ngôn ngữ đơn lập, mỗi âm tiết cách nhau bằng khoảng trắng và được đếm là
// một "từ" (token); một câu kết ngắn có chủ đích (dừng ở hành động/hình ảnh cụ thể, theo
// anti-ai-tone.md mục V) thường không quá một câu đơn giản, ví dụ "Anh đứng đó, không nói
// gì." (~6 từ) hay "Gió tắt, đêm tối bao trùm lấy tất cả." (~8 từ). Chọn 10 làm ngưỡng —
// nằm giữa khoảng 8-12 từ mà một câu kết ngắn tự nhiên thường đạt tới, đủ chặt để không
// khớp nhầm một đoạn văn dài bị ngắt dòng.
const shortEndingWords = 10

// Compute tính thống kê phong cách toàn tác phẩm; trả về nil nếu số chương chưa đủ.
func Compute(in Input) *Stats {
	n := len(in.Chapters)
	if n < minChapters {
		return nil
	}
	all := strings.Join(in.Chapters, "\n")

	s := &Stats{Chapters: n}
	for _, def := range patternDefs {
		total := len(def.re.FindAllStringIndex(all, -1))
		if total == 0 {
			continue
		}
		s.Patterns = append(s.Patterns, PatternStat{
			Name:       def.name,
			Total:      total,
			PerChapter: round1(float64(total) / float64(n)),
		})
	}
	s.TopPhrases = minePhrases(recentWindow(in.Chapters), in.Stopwords)
	s.RepeatedSentences = repeatedSentences(in.Chapters)
	s.Ending = endingShape(in.Chapters)
	s.OpeningTimeRate = openingTimeRate(in.Chapters)
	s.TitleFormats = titleFormats(in.Titles)
	return s
}

func recentWindow(chapters []string) []string {
	if len(chapters) <= phraseWindow {
		return chapters
	}
	return chapters[len(chapters)-phraseWindow:]
}

// minePhrases khai thác các cụm 2–4 TỪ (tách theo khoảng trắng) xuất hiện nhiều trong cửa sổ.
// Lọc: gram chứa số/dấu câu, hư từ/đại từ ở đầu/cuối, trùng danh từ riêng;
// loại trùng: cụm nào là chuỗi con của cụm đã chọn thì bỏ.
func minePhrases(chapters []string, stopwords []string) []PhraseStat {
	text := strings.Join(chapters, "\n")
	words := strings.Fields(text)
	threshold := max(8, len(chapters)/2)

	counts := make(map[string]int)
	for size := 2; size <= 4; size++ {
		for i := 0; i+size <= len(words); i++ {
			gram := words[i : i+size]
			if !validGram(gram) {
				continue
			}
			counts[strings.Join(gram, " ")]++
		}
	}

	stopWords := stopwordWords(stopwords)
	type cand struct {
		text  string
		count int
	}
	var cands []cand
	for g, c := range counts {
		if c < threshold || hitStopword(g, stopWords) {
			continue
		}
		cands = append(cands, cand{g, c})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].count != cands[j].count {
			return cands[i].count > cands[j].count
		}
		// Cùng tần suất thì ưu tiên cụm dài hơn (thông tin nhiều hơn), sau đó sắp xếp từ điển để ổn định
		if len(cands[i].text) != len(cands[j].text) {
			return len(cands[i].text) > len(cands[j].text)
		}
		return cands[i].text < cands[j].text
	})

	var out []PhraseStat
	for _, c := range cands {
		if len(out) >= 8 {
			break
		}
		dup := false
		for _, picked := range out {
			if strings.Contains(picked.Text, c.text) || strings.Contains(c.text, picked.Text) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, PhraseStat{Text: c.text, Count: c.count})
		}
	}
	return out
}

// wordEdgeStop — gram có hư từ/đại từ/liên từ ở đầu hoặc cuối không phải cụm từ phong cách, bỏ qua.
// Danh sách gồm hệ từ, phó từ chỉ thời/mức độ, giới từ, liên từ và đại từ nhân xưng/chỉ định phổ
// biến nhất trong tiếng Việt — vai trò tương đương gramEdgeStop của bản gốc tiếng Trung.
var wordEdgeStop = map[string]bool{
	"là": true, "và": true, "với": true, "cùng": true, "nhưng": true, "mà": true,
	"thì": true, "nên": true, "để": true, "của": true, "cho": true, "ở": true,
	"trong": true, "ra": true, "lên": true, "xuống": true, "cũng": true, "đều": true,
	"còn": true, "lại": true, "chỉ": true, "mới": true, "đã": true, "đang": true,
	"sẽ": true, "rồi": true, "bị": true, "được": true, "không": true, "chưa": true,
	"phải": true, "ai": true, "gì": true, "sao": true, "nào": true, "đâu": true,
	"này": true, "đó": true, "ấy": true, "kia": true, "anh": true, "chị": true,
	"em": true, "tôi": true, "ta": true, "nó": true, "họ": true, "mình": true,
	"chúng": true, "một": true, "các": true, "những": true, "khi": true, "nếu": true,
	"vì": true, "hay": true, "hoặc": true, "như": true,
}

// validGram báo cụm từ có hợp lệ để khai thác không: không chứa số/dấu câu/ký hiệu,
// và không có hư từ/đại từ ở hai đầu.
func validGram(gram []string) bool {
	for _, w := range gram {
		for _, r := range w {
			if unicode.IsDigit(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
				return false
			}
		}
	}
	first := strings.ToLower(gram[0])
	last := strings.ToLower(gram[len(gram)-1])
	if wordEdgeStop[first] || wordEdgeStop[last] {
		return false
	}
	return true
}

// stopwordWords tách danh từ riêng (tên nhân vật) thành các từ đơn: tên người tiếng Việt
// là cụm nhiều âm tiết cách nhau bằng khoảng trắng ("Cửu Uyên" → "cửu", "uyên"), mỗi âm tiết
// đã tự nhiên là một token nên không cần cắt bigram nhân tạo như bản gốc tiếng Trung.
// Thà lọc nghiêm hơn — bớt một cụm từ thực tế không sao, còn tên người lọt vào danh sách
// cửa miệng mới là nhiễu.
func stopwordWords(stopwords []string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range stopwords {
		for _, part := range strings.Fields(w) {
			set[strings.ToLower(part)] = true
		}
	}
	return set
}

func hitStopword(gram string, stopWords map[string]bool) bool {
	if len(stopWords) == 0 {
		return false
	}
	for _, w := range strings.Fields(gram) {
		if stopWords[strings.ToLower(w)] {
			return true
		}
	}
	return false
}

// repeatedSentences tìm các câu ≥12 ký tự lặp lại từng chữ xuyên ≥3 chương, lấy top 5 theo số lần.
func repeatedSentences(chapters []string) []SentenceStat {
	type rec struct {
		count    int
		chapters map[int]struct{}
	}
	seen := make(map[string]*rec)
	for ci, text := range chapters {
		for _, sent := range sentenceSplit.Split(text, -1) {
			// Bỏ dấu ngoặc kép bao quanh rồi gộp: cùng một câu thoại có/không có ngoặc mở không nên tính là hai câu khác
			sent = strings.Trim(strings.TrimSpace(sent), `"""''「」『』`)
			if len([]rune(sent)) < 12 {
				continue
			}
			r := seen[sent]
			if r == nil {
				r = &rec{chapters: make(map[int]struct{})}
				seen[sent] = r
			}
			r.count++
			r.chapters[ci] = struct{}{}
		}
	}

	var out []SentenceStat
	for sent, r := range seen {
		if len(r.chapters) < 3 {
			continue
		}
		out = append(out, SentenceStat{Text: truncateRunes(sent, 40), Chapters: len(r.chapters), Count: r.count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func endingShape(chapters []string) EndingStat {
	var lengths []int
	short := 0
	for _, text := range chapters {
		line := lastNonEmptyLine(text)
		if line == "" {
			continue
		}
		n := len(strings.Fields(line))
		lengths = append(lengths, n)
		if n <= shortEndingWords {
			short++
		}
	}
	if len(lengths) == 0 {
		return EndingStat{}
	}
	sort.Ints(lengths)
	return EndingStat{
		ShortRatio:  round2(float64(short) / float64(len(lengths))),
		MedianWords: lengths[len(lengths)/2],
	}
}

func openingTimeRate(chapters []string) float64 {
	hit := 0
	for _, text := range chapters {
		if openingTimeRe.MatchString(firstParagraph(text)) {
			hit++
		}
	}
	return round2(float64(hit) / float64(len(chapters)))
}

func titleFormats(titles []string) *TitleStat {
	if len(titles) == 0 {
		return nil
	}
	t := &TitleStat{}
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			continue
		}
		if titlePrefixRe.MatchString(title) {
			t.WithPrefix++
		} else {
			t.WithoutPrefix++
		}
	}
	// Chỉ báo cáo khi có dùng lẫn lộn; định dạng đồng nhất không phải vấn đề thực tế
	if t.WithPrefix == 0 || t.WithoutPrefix == 0 {
		return nil
	}
	return t
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// firstParagraph lấy dòng đầu tiên không rỗng và không phải tiêu đề Markdown
// (dòng đầu file chương thường là tiêu đề # ).
func firstParagraph(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
