package tools

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestParsePremiseSections(t *testing.T) {
	premise := `# Premise

## Thể loại và tông điệu
Huyền huyễn phương Đông, trưởng thành lạnh khốc.

## Định vị thể loại
Huyền huyễn tu luyện dòng nâng cấp, hướng đến độc giả thích cảm giác sảng khoái và tiến triển quan hệ.

## Xung đột cốt lõi
Nhân vật chính phải lựa chọn giữa quy tắc tông môn và lương tâm cá nhân.

## Bước ngoặt giữa truyện
Lộ trình tu luyện cũ thất bại, buộc phải chuyển sang hệ thống cấm thuật.
`

	sections := parsePremiseSections(premise)
	if sections["Thể loại và tông điệu"] == "" {
		t.Fatalf("expected 'Thể loại và tông điệu' section, got %+v", sections)
	}
	if sections["Định vị thể loại"] == "" {
		t.Fatalf("expected 'Định vị thể loại' section, got %+v", sections)
	}
	if sections["Xung đột cốt lõi"] == "" {
		t.Fatalf("expected 'Xung đột cốt lõi' section, got %+v", sections)
	}
	if sections["Bước ngoặt giữa chuyện"] == "" {
		t.Fatalf("expected alias 'Bước ngoặt giữa truyện' normalized to 'Bước ngoặt giữa chuyện', got %+v", sections)
	}
}

// TestCanonicalPremiseHeadingIsRobustToFormatting kiểm tra heading lệch nhỏ về hoa/thường,
// khoảng trắng thừa và số lượng dấu "#" vẫn khớp về cùng một canonical heading.
func TestCanonicalPremiseHeadingIsRobustToFormatting(t *testing.T) {
	cases := []string{
		"## Xung đột cốt lõi",
		"##Xung đột cốt lõi",
		"##  Xung   đột   cốt   lõi  ",
		"## XUNG ĐỘT CỐT LÕI",
		"### xung đột cốt lõi",
	}
	for _, line := range cases {
		got, ok := canonicalPremiseHeading(line)
		if !ok {
			t.Fatalf("expected %q to match a canonical heading", line)
		}
		if got != "Xung đột cốt lõi" {
			t.Fatalf("expected %q to normalize to 'Xung đột cốt lõi', got %q", line, got)
		}
	}
}

func TestPremiseStructure(t *testing.T) {
	premise := `## Thể loại và tông điệu
Dòng nâng cấp, thiên hướng lạnh khốc.

## Định vị thể loại
Dòng nâng cấp

## Xung đột cốt lõi
Xung đột

## Mục tiêu nhân vật chính
Mục tiêu

## Hướng kết cục
Kết cục

## Vùng cấm viết
Vùng cấm

## Điểm bán khác biệt
Điểm bán

## Điểm móc khác biệt
Điểm móc

## Cam kết thực hiện cốt lõi
Cam kết

## Động cơ truyện
Động cơ

## Bước ngoặt giữa chuyện
Bước ngoặt
`

	structure := premiseStructure(premise, domain.PlanningTierMid)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected template_ready, got %+v", structure)
	}
	missing, _ := structure["missing"].([]string)
	if len(missing) != 0 {
		t.Fatalf("expected no missing headings, got %+v", missing)
	}
}

func TestPremiseStructureShortAcceptsLegacyHeadingAlias(t *testing.T) {
	premise := `## Thể loại và sắc thái
Đơn tập áp lực cao, giải cứu khẩn cấp.

## Định vị thể loại
Truyện ngắn phiêu lưu mật độ cao.

## Xung đột cốt lõi
Nhân vật chính phải giải cứu con tin trong một đêm.

## Mục tiêu nhân vật chính
Giải cứu con tin và sống sót rời đi.

## Hướng kết cục
Hoàn thành nhiệm vụ nhưng phải trả giá.

## Vùng cấm viết
Không mở rộng thành truyện dài kỳ.

## Điểm bán hàng khác biệt
Áp lực thời gian và các cú lật liên tục.

## Điểm móc khác biệt
Mỗi lựa chọn đều rút ngắn thời gian giải cứu.

## Cam kết thực hiện cốt lõi
Cảm giác cấp bách, lựa chọn và lật ngược tình thế.

## Tính phù hợp truyện ngắn
Mâu thuẫn chính và cung nhân vật đều có thể hoàn thành trong một nhiệm vụ.
`

	structure := premiseStructure(premise, domain.PlanningTierShort)
	if ready, _ := structure["template_ready"].(bool); !ready {
		t.Fatalf("expected short template_ready, got %+v", structure)
	}
}
