package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// premiseHeadingAliases ánh xạ các biến thể heading tiếng Việt mà Kiến trúc sư (architect-short.md /
// architect-long.md) có thể xuất ra về một heading canonical duy nhất. Khóa và giá trị được so khớp
// sau khi đã chuẩn hóa qua normalizeHeadingKey (xem canonicalPremiseHeading), nên chỉ cần liệt kê ở
// đây các CÁCH DIỄN ĐẠT khác nhau — sai lệch về hoa/thường hoặc khoảng trắng thừa đã được xử lý riêng.
var premiseHeadingAliases = map[string]string{
	// Thể loại & tông điệu — short dùng "sắc thái", long dùng "tông điệu".
	"Thể loại và tông điệu": "Thể loại và tông điệu",
	"Thể loại và sắc thái":  "Thể loại và tông điệu",

	"Định vị thể loại": "Định vị thể loại",

	"Xung đột cốt lõi": "Xung đột cốt lõi",

	"Mục tiêu nhân vật chính": "Mục tiêu nhân vật chính",

	"Hướng kết cục": "Hướng kết cục",

	"Vùng cấm viết": "Vùng cấm viết",

	// Điểm bán khác biệt — short dùng "Điểm bán khác biệt", long dùng "Điểm bán hàng khác biệt".
	"Điểm bán khác biệt":      "Điểm bán khác biệt",
	"Điểm bán hàng khác biệt": "Điểm bán khác biệt",

	"Điểm móc khác biệt": "Điểm móc khác biệt",

	"Cam kết thực hiện cốt lõi": "Cam kết thực hiện cốt lõi",

	// Chỉ dùng cho truyện dài/trung.
	"Động cơ truyện":           "Động cơ truyện",
	"Tuyến quan hệ/phát triển": "Tuyến quan hệ/phát triển",
	"Lộ trình nâng cấp":        "Lộ trình nâng cấp",
	"Bước ngoặt giữa chuyện":   "Bước ngoặt giữa chuyện",
	"Bước ngoặt giữa truyện":   "Bước ngoặt giữa chuyện",
	"Mệnh đề kết cục":          "Mệnh đề kết cục",

	// Chỉ dùng cho truyện ngắn.
	"Tính phù hợp truyện ngắn": "Tính phù hợp truyện ngắn",
}

// normalizedPremiseHeadingAliases được dựng một lần từ premiseHeadingAliases, với khóa đã chuẩn hóa
// (bỏ "#", TrimSpace, hạ chữ thường, gộp khoảng trắng thừa) để việc tra cứu bền hơn trước các lệch nhỏ
// về định dạng mà LLM có thể xuất ra (thừa dấu #, thừa khoảng trắng, khác hoa/thường...).
var normalizedPremiseHeadingAliases = buildNormalizedPremiseHeadingAliases()

func buildNormalizedPremiseHeadingAliases() map[string]string {
	m := make(map[string]string, len(premiseHeadingAliases))
	for alias, canonical := range premiseHeadingAliases {
		m[normalizeHeadingKey(alias)] = canonical
	}
	return m
}

// normalizeHeadingKey chuẩn hóa một dòng heading để so khớp: bỏ toàn bộ dấu "#" ở đầu, TrimSpace,
// hạ chữ thường, rồi gộp mọi chuỗi khoảng trắng liên tiếp thành một dấu cách.
func normalizeHeadingKey(line string) string {
	title := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
	title = strings.ToLower(title)
	return strings.Join(strings.Fields(title), " ")
}

func parsePremiseSections(premise string) map[string]string {
	lines := strings.Split(premise, "\n")
	sections := make(map[string]string)
	var current string
	var body []string

	flush := func() {
		if current == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			sections[current] = text
		}
		body = body[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := canonicalPremiseHeading(trimmed); ok {
			flush()
			current = heading
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonicalPremiseHeading(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	key := normalizeHeadingKey(trimmed)
	if key == "" {
		return "", false
	}
	canonical, ok := normalizedPremiseHeadingAliases[key]
	return canonical, ok
}

func premiseStructure(premise string, tier domain.PlanningTier) map[string]any {
	sections := parsePremiseSections(premise)
	required := requiredPremiseHeadings(tier)
	found := make([]string, 0, len(required))
	var missing []string
	for _, heading := range required {
		if _, ok := sections[heading]; ok {
			found = append(found, heading)
			continue
		}
		missing = append(missing, heading)
	}

	structure := map[string]any{
		"template_ready": len(missing) == 0,
		"found":          found,
		"missing":        missing,
	}
	if len(sections) > 0 {
		structure["section_count"] = len(sections)
	}
	return structure
}

// requiredPremiseHeadings trả về tập heading tiếng Việt bắt buộc theo tier, khớp chính xác với các
// mục architect-short.md / architect-long.md ra lệnh cho Kiến trúc sư viết.
func requiredPremiseHeadings(tier domain.PlanningTier) []string {
	common := []string{
		"Thể loại và tông điệu",
		"Định vị thể loại",
		"Xung đột cốt lõi",
		"Mục tiêu nhân vật chính",
		"Hướng kết cục",
		"Vùng cấm viết",
		"Điểm bán khác biệt",
		"Điểm móc khác biệt",
		"Cam kết thực hiện cốt lõi",
	}

	switch tier {
	case domain.PlanningTierLong:
		return append(common,
			"Động cơ truyện",
			"Tuyến quan hệ/phát triển",
			"Lộ trình nâng cấp",
			"Bước ngoặt giữa chuyện",
			"Mệnh đề kết cục",
		)
	case domain.PlanningTierMid:
		return append(common,
			"Động cơ truyện",
			"Bước ngoặt giữa chuyện",
		)
	case domain.PlanningTierShort:
		return append(common,
			"Tính phù hợp truyện ngắn",
		)
	default:
		return common
	}
}
