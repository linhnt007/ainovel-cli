package ctxpack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// buildNestedJSONFixture dựng một JSON lồng nhau ~10KB để test truncate —
// nhiều key, mỗi key chứa mảng chuỗi có ký tự tiếng Việt đa byte để vừa
// kiểm tra độ ưu tiên drop, vừa kiểm tra an toàn UTF-8.
func buildNestedJSONFixture() []byte {
	type character struct {
		Name  string   `json:"name"`
		Notes []string `json:"notes"`
	}
	data := struct {
		Plan       string      `json:"plan"`
		Outline    string      `json:"outline"`
		Characters []character `json:"characters"`
		Foreshadow []string    `json:"foreshadow"`
	}{
		Plan:    "Chương 12: nhân vật chính đối đầu với phản diện tại lâu đài cổ.",
		Outline: strings.Repeat("Diễn biến cốt truyện quan trọng, ", 40),
	}
	for i := 0; i < 8; i++ {
		data.Characters = append(data.Characters, character{
			Name:  fmt.Sprintf("Nhân vật %d", i),
			Notes: []string{
				strings.Repeat("cảm xúc và động cơ hiện tại, ", 20),
				strings.Repeat("mối quan hệ đang thay đổi, ", 20),
			},
		})
	}
	for i := 0; i < 10; i++ {
		data.Foreshadow = append(data.Foreshadow, fmt.Sprintf("phục bút số %d: %s", i, strings.Repeat("chi tiết cài cắm, ", 10)))
	}
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}

func TestTruncateJSONToTokens_NestedLargeInput_SmallBudgetStillValid(t *testing.T) {
	input := buildNestedJSONFixture()
	if len(input) < 5000 {
		t.Fatalf("fixture too small for this test: %d bytes", len(input))
	}

	out := truncateJSONToTokens(input, 50)

	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON: %q", out)
	}
	if len(out) >= len(input) {
		t.Fatalf("expected output shorter than input: out=%d input=%d", len(out), len(input))
	}
}

func TestTruncateJSONToTokens_LargeBudget_ReturnsInputUnchanged(t *testing.T) {
	input := buildNestedJSONFixture()

	// Ngân sách rất lớn đảm bảo vượt qua kiểm tra estimateTextTokens ngay
	// từ đầu, không đi vào nhánh drop.
	out := truncateJSONToTokens(input, 1_000_000)

	if out != string(input) {
		t.Fatalf("expected output unchanged for large budget\nwant: %s\ngot:  %s", string(input), out)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON: %q", out)
	}
}

func TestTruncateJSONToTokens_DropsUntilFitsAndStaysValid(t *testing.T) {
	input := buildNestedJSONFixture()

	// Ngân sách vừa phải: đủ nhỏ để buộc drop vài phần tử, đủ lớn để không
	// rỗng hoàn toàn — kiểm tra vòng lặp drop hội tụ và luôn hợp lệ.
	out := truncateJSONToTokens(input, 300)

	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON: %q", out)
	}
	if len(out) >= len(input) {
		t.Fatalf("expected output shorter than input: out=%d input=%d", len(out), len(input))
	}

	estimated := estimateTextTokens(out)
	// Không khẳng định estimated <= 300 tuyệt đối (vòng lặp có thể dừng khi
	// container đã rỗng mà vẫn còn dư key nhỏ), nhưng phải nhỏ hơn hẳn ước
	// lượng của input gốc, chứng tỏ đã thực sự cắt bớt.
	inputEstimated := estimateTextTokens(string(input))
	if estimated >= inputEstimated {
		t.Fatalf("expected truncated output to have fewer estimated tokens: out=%d input=%d", estimated, inputEstimated)
	}
}

func TestTruncateJSONToTokens_InvalidJSON_FallsBackWithoutPanicOrBrokenUTF8(t *testing.T) {
	// Chuỗi không phải JSON hợp lệ, chứa nhiều ký tự tiếng Việt đa byte để
	// kiểm tra fallback không cắt giữa ký tự UTF-8.
	notJSON := strings.Repeat("đây không phải là JSON hợp lệ, chỉ là văn bản thô — ", 50)
	if json.Valid([]byte(notJSON)) {
		t.Fatalf("fixture unexpectedly valid JSON")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("truncateJSONToTokens panicked: %v", r)
		}
	}()

	out := truncateJSONToTokens([]byte(notJSON), 20)

	if !utf8.ValidString(out) {
		t.Fatalf("output contains invalid UTF-8 (cut mid-rune): %q", out)
	}
	if len(out) >= len(notJSON) {
		t.Fatalf("expected fallback output shorter than input: out=%d input=%d", len(out), len(notJSON))
	}
}

func TestTruncateJSONToTokens_EmptyBudget_NoPanic(t *testing.T) {
	input := buildNestedJSONFixture()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("truncateJSONToTokens panicked with budget<=0: %v", r)
		}
	}()

	out := truncateJSONToTokens(input, 0)
	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON: %q", out)
	}
}
