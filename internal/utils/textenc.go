package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText giải mã byte file văn bản do người dùng cung cấp sang UTF-8:
// nếu không phải UTF-8 hợp lệ thì chuyển mã theo Windows-1258 (Vietnamese) hoặc GB18030 (Chinese).
// Heuristic: kiểm tra xem có chứa các byte đặc trưng của tiếng Việt Windows-1258 hay không.
func DecodeText(data []byte) string {
	if utf8.Valid(data) {
		return strings.TrimPrefix(string(data), "\uFEFF")
	}

	// Thử giải mã GB18030 trước (cho tiếng Trung)
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
		s := string(decoded)
		// Nếu chứa chữ Hán, khả năng cao là tiếng Trung GBK thật
		if containsHan(s) {
			return strings.TrimPrefix(s, "\uFEFF")
		}
	}

	// Nếu không có chữ Hán hoặc lỗi giải mã GB18030, giải mã dạng Windows-1258 cho tiếng Việt
	if decoded, err := charmap.Windows1258.NewDecoder().Bytes(data); err == nil {
		return strings.TrimPrefix(string(decoded), "\uFEFF")
	}

	return strings.TrimPrefix(string(data), "\uFEFF")
}

// containsHan kiểm tra xem chuỗi có chứa ký tự chữ Hán CJK hay không
func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}
