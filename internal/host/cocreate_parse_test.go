package host

import (
	"strings"
	"testing"
)

// Các test trong file này CHỈ ghim (pin) hành vi hiện có của parser giao thức XML bốn thẻ trong cocreate.go
// (splitCoCreateMarkers / extractTagContent / parseSuggestions / parseCoCreateResponse / extractReplyPreview).
// Tuyệt đối không sửa cocreate.go — nếu phát hiện hành vi lạ, test vẫn ghim đúng hành vi đang chạy và ghi chú lại trong comment.

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestParseCoCreateResponse_HappyPath: đủ bốn thẻ, đúng thứ tự, đóng mở đầy đủ.
func TestParseCoCreateResponse_HappyPath(t *testing.T) {
	raw := `<reply>
Xin chào, mình đã hiểu ý tưởng ban đầu của bạn.
</reply>

<draft>
## Chủ đề
- Tiên hiệp hiện đại
</draft>

<ready>false</ready>

<suggestions>
- Nhân vật chính là nữ
- Bối cảnh hiện đại
</suggestions>`

	got, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("không mong đợi lỗi, nhận %v", err)
	}
	if got.Message != "Xin chào, mình đã hiểu ý tưởng ban đầu của bạn." {
		t.Errorf("Message sai, nhận %q", got.Message)
	}
	if got.Prompt != "## Chủ đề\n- Tiên hiệp hiện đại" {
		t.Errorf("Prompt sai, nhận %q", got.Prompt)
	}
	if got.Ready {
		t.Errorf("Ready phải là false")
	}
	want := []string{"Nhân vật chính là nữ", "Bối cảnh hiện đại"}
	if !equalStrSlice(got.Suggestions, want) {
		t.Errorf("Suggestions sai, nhận %v muốn %v", got.Suggestions, want)
	}
	if got.Raw != strings.TrimSpace(raw) {
		t.Errorf("Raw phải giữ nguyên toàn bộ (đã trim), nhận %q", got.Raw)
	}
}

// TestParseCoCreateResponse_MissingDraftClose mô phỏng output bị cắt bởi max_tokens giữa chừng thẻ <draft>.
// Ghim hai nhánh dự phòng của extractTagContent khi có mở không đóng:
//  1. Không còn thẻ mở nào khác phía sau -> lấy đến hết chuỗi.
//  2. Có thẻ mở khác xuất hiện phía sau -> cắt đến trước thẻ đó.
func TestParseCoCreateResponse_MissingDraftClose(t *testing.T) {
	t.Run("cắt đến hết chuỗi", func(t *testing.T) {
		raw := `<reply>
Đã ghi nhận yêu cầu.
</reply>

<draft>
## Chủ đề
- Phần này bị cắt do max_tokens`

		got, err := parseCoCreateResponse(raw)
		if err != nil {
			t.Fatalf("không mong đợi lỗi, nhận %v", err)
		}
		if got.Message != "Đã ghi nhận yêu cầu." {
			t.Errorf("Message sai, nhận %q", got.Message)
		}
		wantDraft := "## Chủ đề\n- Phần này bị cắt do max_tokens"
		if got.Prompt != wantDraft {
			t.Errorf("Prompt sai, nhận %q muốn %q", got.Prompt, wantDraft)
		}
		if got.Ready {
			t.Errorf("Ready phải là false (thẻ <ready> không tồn tại trong chuỗi bị cắt)")
		}
		if got.Suggestions != nil {
			t.Errorf("Suggestions phải rỗng, nhận %v", got.Suggestions)
		}
	})

	t.Run("cắt đến trước thẻ mở kế tiếp", func(t *testing.T) {
		raw := "<reply>Đã ghi nhận.</reply><draft>## Chủ đề\n- Đang viết dở<ready>false</ready>"

		got, err := parseCoCreateResponse(raw)
		if err != nil {
			t.Fatalf("không mong đợi lỗi, nhận %v", err)
		}
		if got.Message != "Đã ghi nhận." {
			t.Errorf("Message sai, nhận %q", got.Message)
		}
		wantDraft := "## Chủ đề\n- Đang viết dở"
		if got.Prompt != wantDraft {
			t.Errorf("Prompt sai, nhận %q muốn %q", got.Prompt, wantDraft)
		}
		if got.Ready {
			t.Errorf("Ready phải là false")
		}
	})
}

// TestParseCoCreateResponse_TypoOpenTag ghim nhánh "không mở có đóng" của extractTagContent:
// mô hình viết sai thẻ mở <suggestions> thành <uggestions>, nhưng thẻ đóng </suggestions> vẫn đúng.
// extractTagContent lấy nội dung từ sau thẻ đóng hoàn chỉnh gần nhất (</ready>) đến trước </suggestions>,
// nên dòng "<uggestions>" bị cuốn vào làm dòng đầu tiên của nội dung -- parseSuggestions sau đó tự lọc
// bỏ dòng trông như thẻ XML nên vẫn ra kết quả đúng.
func TestParseCoCreateResponse_TypoOpenTag(t *testing.T) {
	raw := `<reply>
Đã rõ.
</reply>

<draft>
## Chủ đề
</draft>

<ready>false</ready>

<uggestions>
- Thêm nhân vật
- Đổi bối cảnh
</suggestions>`

	got, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("không mong đợi lỗi, nhận %v", err)
	}
	if got.Message != "Đã rõ." {
		t.Errorf("Message sai, nhận %q", got.Message)
	}
	if got.Prompt != "## Chủ đề" {
		t.Errorf("Prompt sai, nhận %q", got.Prompt)
	}
	if got.Ready {
		t.Errorf("Ready phải là false")
	}
	want := []string{"Thêm nhân vật", "Đổi bối cảnh"}
	if !equalStrSlice(got.Suggestions, want) {
		t.Errorf("Suggestions sai, nhận %v muốn %v", got.Suggestions, want)
	}
}

// TestParseCoCreateResponse_ReplyNaturalTextThenCloseTag ghim nhánh thứ ba của extractTagContent dành riêng
// cho <reply>: mô hình mở đầu bằng văn bản tự nhiên (không có thẻ mở <reply>) rồi dán </reply> ở cuối đoạn.
func TestParseCoCreateResponse_ReplyNaturalTextThenCloseTag(t *testing.T) {
	raw := `Chào bạn, ý tưởng này thú vị đấy.</reply>

<draft>
## Chủ đề
- ABC
</draft>

<ready>false</ready>

<suggestions>
- Thêm chi tiết
</suggestions>`

	got, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("không mong đợi lỗi, nhận %v", err)
	}
	if got.Message != "Chào bạn, ý tưởng này thú vị đấy." {
		t.Errorf("Message sai, nhận %q", got.Message)
	}
	if got.Prompt != "## Chủ đề\n- ABC" {
		t.Errorf("Prompt sai, nhận %q", got.Prompt)
	}
	want := []string{"Thêm chi tiết"}
	if !equalStrSlice(got.Suggestions, want) {
		t.Errorf("Suggestions sai, nhận %v muốn %v", got.Suggestions, want)
	}
}

// TestParseCoCreateResponse_NoProtocolAtAll: mô hình không tuân thủ giao thức XML chút nào (văn bản trần).
// Cả đoạn phải trở thành reply, Prompt rỗng, Ready=false.
func TestParseCoCreateResponse_NoProtocolAtAll(t *testing.T) {
	raw := "Chào bạn, mình nghĩ ý tưởng thú vị, bạn có thể nói rõ thêm về thể loại không?"

	got, err := parseCoCreateResponse(raw)
	if err != nil {
		t.Fatalf("không mong đợi lỗi, nhận %v", err)
	}
	if got.Message != raw {
		t.Errorf("Message phải bằng toàn bộ raw, nhận %q", got.Message)
	}
	if got.Prompt != "" {
		t.Errorf("Prompt phải rỗng, nhận %q", got.Prompt)
	}
	if got.Ready {
		t.Errorf("Ready phải là false")
	}
	if got.Suggestions != nil {
		t.Errorf("Suggestions phải rỗng, nhận %v", got.Suggestions)
	}
	if got.Raw != raw {
		t.Errorf("Raw phải bằng raw gốc, nhận %q", got.Raw)
	}
}

// TestParseCoCreateResponse_ReadyValues ghim quy tắc parse <ready>: "true"/"yes" (không phân biệt hoa thường) -> true,
// bất kỳ giá trị nào khác (kể cả từ gần nghĩa như "ready") -> false.
func TestParseCoCreateResponse_ReadyValues(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"yes -> true", "<reply>ok</reply><ready>yes</ready>", true},
		{"TRUE hoa -> true", "<reply>ok</reply><ready>TRUE</ready>", true},
		{"true thường -> true", "<reply>ok</reply><ready>true</ready>", true},
		{"ready (gần nghĩa) -> false", "<reply>ok</reply><ready>ready</ready>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCoCreateResponse(tc.raw)
			if err != nil {
				t.Fatalf("không mong đợi lỗi, nhận %v", err)
			}
			if got.Ready != tc.want {
				t.Errorf("Ready = %v, muốn %v", got.Ready, tc.want)
			}
		})
	}
}

// TestParseCoCreateSuggestions ghim toàn bộ quy tắc của parseSuggestions: giới hạn tối đa 3 dòng, bóc tiền tố
// "- " / "* " / "1. " (kể cả số nhiều chữ số), bỏ dòng trông như thẻ XML, bỏ dòng dưới 2 ký tự.
// Lưu ý: tên hàm test phải chứa "CoCreate" để lọt qua `go test -run CoCreate` theo yêu cầu chạy scoped.
func TestParseCoCreateSuggestions(t *testing.T) {
	t.Run("rỗng trả về nil", func(t *testing.T) {
		if got := parseSuggestions(""); got != nil {
			t.Errorf("muốn nil, nhận %v", got)
		}
	})

	t.Run("5 dòng chỉ giữ 3", func(t *testing.T) {
		text := "- một\n* hai\n1. ba\n- bốn\n- năm"
		got := parseSuggestions(text)
		want := []string{"một", "hai", "ba"}
		if !equalStrSlice(got, want) {
			t.Errorf("nhận %v muốn %v", got, want)
		}
	})

	t.Run("tiền tố số nhiều chữ số bị bóc", func(t *testing.T) {
		got := parseSuggestions("12. Thử nghiệm nhiều chữ số")
		want := []string{"Thử nghiệm nhiều chữ số"}
		if !equalStrSlice(got, want) {
			t.Errorf("nhận %v muốn %v", got, want)
		}
	})

	t.Run("dòng trông như thẻ XML bị bỏ", func(t *testing.T) {
		got := parseSuggestions("<foo>\n- Gợi ý thật")
		want := []string{"Gợi ý thật"}
		if !equalStrSlice(got, want) {
			t.Errorf("nhận %v muốn %v", got, want)
		}
	})

	t.Run("dòng dưới 2 ký tự bị bỏ", func(t *testing.T) {
		got := parseSuggestions("- a\n- Gợi ý đủ dài")
		want := []string{"Gợi ý đủ dài"}
		if !equalStrSlice(got, want) {
			t.Errorf("nhận %v muốn %v", got, want)
		}
	})

	t.Run("dòng trống bị bỏ qua không tính vào giới hạn", func(t *testing.T) {
		got := parseSuggestions("\n\n- một\n\n- hai\n")
		want := []string{"một", "hai"}
		if !equalStrSlice(got, want) {
			t.Errorf("nhận %v muốn %v", got, want)
		}
	})
}

// TestParseCoCreateResponse_EmptyRaw: raw rỗng (hoặc chỉ toàn khoảng trắng) phải trả về lỗi "cocreate empty response".
func TestParseCoCreateResponse_EmptyRaw(t *testing.T) {
	for _, raw := range []string{"", "   \n\t  "} {
		_, err := parseCoCreateResponse(raw)
		if err == nil {
			t.Fatalf("muốn lỗi cho raw %q, nhận nil", raw)
		}
		if !strings.Contains(err.Error(), "cocreate empty response") {
			t.Errorf("thông điệp lỗi sai, nhận %q", err.Error())
		}
	}
}

// TestSplitCoCreateMarkers_TagOrderIndependentOfPosition xác nhận splitCoCreateMarkers gọi đúng bốn hàm
// extractTagContent tương ứng và trả về đúng kiểu dữ liệu (bool cho ready, []string cho suggestions).
func TestSplitCoCreateMarkers(t *testing.T) {
	raw := `<reply>Trả lời</reply><draft>Bản thảo</draft><ready>true</ready><suggestions>- Gợi ý</suggestions>`
	reply, draft, ready, suggestions := splitCoCreateMarkers(raw)
	if reply != "Trả lời" {
		t.Errorf("reply sai: %q", reply)
	}
	if draft != "Bản thảo" {
		t.Errorf("draft sai: %q", draft)
	}
	if !ready {
		t.Errorf("ready phải là true")
	}
	want := []string{"Gợi ý"}
	if !equalStrSlice(suggestions, want) {
		t.Errorf("suggestions sai: %v", suggestions)
	}
}

// TestStripOrderedCoCreatePrefix ghim nhánh bảo vệ của stripOrderedPrefix mà parseSuggestions không bao giờ
// chạm tới (vì isOrderedSuggestion đã lọc trước): không có chữ số ở đầu, hoặc chỉ có số mà không có ". " theo sau.
func TestStripOrderedCoCreatePrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"không có chữ số ở đầu -> giữ nguyên", "abc", "abc"},
		{"chỉ toàn chữ số, không có phần đuôi -> giữ nguyên", "12", "12"},
		{"đúng định dạng số thứ tự -> bóc tiền tố", "3. ok", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripOrderedPrefix(tc.in); got != tc.want {
				t.Errorf("stripOrderedPrefix(%q) = %q, muốn %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestExtractCoCreateReplyPreview ghim hành vi xem trước streaming của extractReplyPreview.
func TestExtractCoCreateReplyPreview(t *testing.T) {
	t.Run("đang streaming, chưa có thẻ đóng", func(t *testing.T) {
		got := extractReplyPreview("<reply>Đang gõ")
		if got != "Đang gõ" {
			t.Errorf("nhận %q", got)
		}
	})

	t.Run("đã đóng thẻ reply", func(t *testing.T) {
		got := extractReplyPreview("<reply>Xong rồi</reply><draft>tiếp theo")
		if got != "Xong rồi" {
			t.Errorf("nhận %q", got)
		}
	})

	t.Run("chưa có thẻ mở reply nào", func(t *testing.T) {
		got := extractReplyPreview("Đang suy nghĩ")
		if got != "Đang suy nghĩ" {
			t.Errorf("nhận %q", got)
		}
	})

	t.Run("nửa vời: không thẻ mở reply, cắt trước thẻ mở draft", func(t *testing.T) {
		got := extractReplyPreview("Đây là câu trả lời<draft>")
		if got != "Đây là câu trả lời" {
			t.Errorf("nhận %q", got)
		}
	})
}
