// Package flow triển khai Flow Router theo ngành dọc: Host quyết định dựa trên thực tế
// xem SubAgent nào sẽ được gọi tiếp theo và làm gì.
//
// Nguyên tắc thiết kế:
//   - Route là hàm thuần túy: đầu vào là State, đầu ra là *Instruction. Không có IO, không gọi Store, có thể unit test độc lập.
//   - State được LoadState (không thuần túy) xây dựng từ Store, đọc toàn bộ dữ liệu cần thiết cho routing một lần.
//   - Trả về nil là hợp lệ: nghĩa là "để Coordinator LLM tự quyết định".
//
// Router bao gồm các quyết định kiểu "tra bảng" (bước tiếp theo mỗi chương, hậu xử lý cuối cung truyện, điều phối theo hàng đợi),
// không bao gồm các quyết định kiểu "hiểu ngữ nghĩa" (chọn kiến trúc sư, xử lý Steer của người dùng, xuất tóm tắt).
package flow

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Instruction chỉ thị cho Host bước tiếp theo yêu cầu Coordinator gọi SubAgent nào và nhiệm vụ gì.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // mô tả nhiệm vụ giao cho SubAgent
	Reason  string // lý do dành cho Coordinator xem (tùy chọn, tiện debug và ghi log)
	Chapter int    // số chương liên quan đến nhiệm vụ writer (tiếp tục/viết lại/đánh bóng); 0 = không liên quan (nhiệm vụ editor/architect)
}

// State là đầu vào của Route: tất cả dữ liệu thực tế phải được khai báo rõ ràng ở đây, Route không được đọc Store nội bộ.
type State struct {
	Progress *domain.Progress

	// Chương đã hoàn thành cuối cùng (phần tử cuối của Progress.CompletedChapters); 0 nghĩa là chưa bắt đầu viết.
	LastCompleted int

	// Thông tin ranh giới cung truyện của chương trước; khi IsArcEnd=false các trường còn lại không có ý nghĩa.
	// Nên là nil khi LastCompleted=0 hoặc không ở chế độ Layered.
	ArcBoundary *storepkg.ArcBoundary

	// Ba dữ liệu hậu xử lý cuối cung truyện: đánh giá / tóm tắt cung / tóm tắt tập đã hoàn thành chưa.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// HasPendingFlatReview: chế độ flat (không phân tầng) còn nợ review định kỳ hay không —
	// tính từ đĩa (batch chương gần nhất là bội của ReviewInterval nhưng chưa có file review global phủ lên).
	// Chỉ có ý nghĩa khi !Progress.Layered.
	HasPendingFlatReview bool

	// Các mục thiếu trong cài đặt nền tảng (tín hiệu bổ sung trong giai đoạn lập kế hoạch).
	FoundationMissing []string
}

// Route trả về chỉ thị bước tiếp theo dựa trên dữ liệu thực tế; trả về nil nghĩa là để Coordinator LLM tự quyết định.
//
// Mức độ ưu tiên quyết định (loại trừ lẫn nhau, khớp từ trên xuống):
//  1. Phase=Complete        → nil (LLM xuất tóm tắt)
//  2. Phase!=Writing        → nil (LLM quyết định chọn kiến trúc sư / bổ sung kế hoạch)
//  3. PendingRewrites không rỗng  → writer viết lại/đánh bóng theo hàng đợi
//  4. Flow=Steering         → nil (đang xử lý can thiệp của người dùng)
//  5. Thiếu đánh giá cuối cung truyện           → editor(arc review)
//  6. Có đánh giá nhưng thiếu tóm tắt cung  → editor(arc summary)
//  7. Cuối tập có tóm tắt cung nhưng thiếu tóm tắt tập → editor(volume summary)
//  8. Cung truyện tiếp theo là skeleton           → architect_long(expand_arc)
//  9. Cuối tập cần quyết định tập tiếp theo       → architect_long(append_volume / complete_book)
// 10. Flat mode còn nợ review định kỳ           → editor(batch review)
// 11. Các trường hợp còn lại                  → writer(viết next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. Trạng thái kết thúc: để LLM xuất tóm tắt
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. Giai đoạn lập kế hoạch do Coordinator quyết định (chọn architect_long/short + vòng lặp bổ sung)
	if p.Phase != domain.PhaseWriting {
		return nil
	}

	// 3. Hàng đợi viết lại/đánh bóng được ưu tiên (dữ liệu đã được tầng công cụ ghi đĩa, Router chỉ điều phối theo danh sách)
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Viết lại"
		if p.Flow == domain.FlowPolishing {
			verb = "Đánh bóng"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s chương %d", verb, ch),
			Reason:  fmt.Sprintf("Hàng đợi PendingRewrites còn %d chương", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. Đang xử lý can thiệp của người dùng: Coordinator đang quyết định, Host không chiếm quyền
	if p.Flow == domain.FlowSteering {
		return nil
	}

	// 5-9. Hậu xử lý cuối cung truyện trong chế độ phân lớp
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Thực hiện đánh giá cấp cung truyện cho tập %d cung %d (scope=arc)", b.Volume, b.Arc),
				Reason: "Đánh giá cuối cung truyện chưa hoàn thành",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt cung %d tập %d (save_arc_summary)", b.Arc, b.Volume),
				Reason: "Tóm tắt cung truyện chưa hoàn thành",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt tập %d (save_volume_summary)", b.Volume),
				Reason: "Tóm tắt tập chưa hoàn thành",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("Mở rộng cung %d tập %d (save_foundation type=expand_arc)", b.NextArc, b.NextVolume),
				Reason: "Skeleton cung truyện tiếp theo cần được mở rộng",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "Đánh giá rồi gọi save_foundation type=append_volume (tiếp tục viết) hoặc type=complete_book (kết thúc toàn bộ tác phẩm)",
				Reason: "Cuối tập cần quyết định thêm tập mới hay kết thúc toàn bộ tác phẩm",
			}
		}
	}

	// 10. Chế độ flat: cưỡng chế review định kỳ mỗi ReviewInterval chương. Trước đây Router luôn
	// dispatch "viết chương kế", việc gọi editor phụ thuộc hoàn toàn Coordinator LLM đọc tín hiệu
	// review_required và tuân prompt — model yếu bỏ qua → truyện chạy hàng chục chương không review.
	// Nay quyết định dựa trên sự thật đĩa (HasPendingFlatReview suy từ file review), sống sót qua crash.
	if !p.Layered && s.HasPendingFlatReview {
		to := (s.LastCompleted / domain.ReviewInterval) * domain.ReviewInterval
		from := to - domain.ReviewInterval + 1
		return &Instruction{
			Agent:  "editor",
			Task:   fmt.Sprintf("Đánh giá batch chương %d-%d (scope=global)", from, to),
			Reason: "Review định kỳ chưa hoàn thành",
		}
	}

	// 11. Tiếp tục viết bình thường
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("Viết chương %d", next),
		Reason:  "Tiếp tục viết chương tiếp theo",
		Chapter: next,
	}
}

// FormatMessage định dạng Instruction thành tin nhắn người dùng gửi cho Coordinator.
// Định dạng cố định, giúp Coordinator prompt nhận dạng và LLM phản hồi trực tiếp.
func FormatMessage(i *Instruction) string {
	return fmt.Sprintf(
		"[Host ra lệnh] Bước tiếp theo: gọi subagent(%s, %q)\nLý do: %s\nĐây là lệnh từ tầng luồng, hãy thực thi ngay, không được gọi novel_context trước, không được xuất suy luận trước.",
		i.Agent, i.Task, i.Reason,
	)
}
