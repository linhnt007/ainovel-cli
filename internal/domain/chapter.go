package domain

import (
	"fmt"
)

// ReviewInterval khoảng cách kiểm duyệt toàn cục mặc định (kích hoạt mỗi N chương).
const ReviewInterval = 5

// GetReviewInterval trả về khoảng cách kiểm duyệt toàn cục dựa trên cấu hình.
// Nếu configReviewInterval > 0, dùng giá trị đó; nếu không, dùng mặc định.
func GetReviewInterval(configReviewInterval int) int {
	if configReviewInterval > 0 {
		return configReviewInterval
	}
	return ReviewInterval
}

// ShouldReview kiểm tra có cần kiểm duyệt toàn cục hay không dựa trên số chương đã hoàn thành (chế độ ngắn/trung).
func ShouldReview(completedCount int) (bool, string) {
	if completedCount > 0 && completedCount%ReviewInterval == 0 {
		return true, fmt.Sprintf("Đã hoàn thành %d chương, kích hoạt kiểm duyệt toàn cục", completedCount)
	}
	return false, ""
}

// ShouldArcReview kiểm tra có cần đánh giá cấp cung truyện/tập hay không trong chế độ dài.
func ShouldArcReview(isArcEnd, isVolumeEnd bool, volume, arc int) (bool, string) {
	if isVolumeEnd {
		return true, fmt.Sprintf("Tập %d cung truyện %d kết thúc (kết thúc tập), kích hoạt đánh giá cấp cung truyện + cấp tập", volume, arc)
	}
	if isArcEnd {
		return true, fmt.Sprintf("Tập %d cung truyện %d kết thúc, kích hoạt đánh giá cấp cung truyện", volume, arc)
	}
	return false, ""
}
