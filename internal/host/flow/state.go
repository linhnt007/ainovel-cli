package flow

import (
	"log/slog"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// LoadState đọc toàn bộ dữ liệu cần thiết cho Route từ Store.
// Đây là "ranh giới IO" của router: mọi thao tác đọc tập trung tại đây, Route giữ nguyên trạng thái thuần túy.
// Khi đọc thất bại, các giá trị mặc định an toàn được dùng (has*=false, boundary=nil), giúp Router ưu tiên giao lại thay vì bỏ qua.
func LoadState(store *storepkg.Store) State {
	s := State{
		FoundationMissing: store.FoundationMissing(),
	}
	progress, err := store.Progress.Load()
	if err != nil {
		s.LoadWarnings = append(s.LoadWarnings, "progress.Load: "+err.Error())
		slog.Warn("LoadState progress.Load failed", "error", err)
	}
	if progress == nil {
		if err == nil {
			s.LoadWarnings = append(s.LoadWarnings, "progress.Load returned nil")
			slog.Warn("LoadState progress is nil")
		}
		return s
	}
	s.Progress = progress

	if n := len(progress.CompletedChapters); n > 0 {
		s.LastCompleted = progress.CompletedChapters[n-1]
	}

	// Ranh giới cung truyện chỉ được tính trong chế độ phân tầng và khi có chương đã hoàn thành
	if progress.Layered && s.LastCompleted > 0 {
		if boundary, berr := store.Outline.CheckArcBoundary(s.LastCompleted); berr != nil {
			s.LoadWarnings = append(s.LoadWarnings, "CheckArcBoundary: "+berr.Error())
			slog.Warn("LoadState CheckArcBoundary failed", "error", berr)
		} else if boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview = store.World.HasArcReview(s.LastCompleted)
				s.HasArcSummary = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary = store.Summaries.HasVolumeSummary(boundary.Volume)
				}
			}
		}
	}

	// Chế độ flat (không phân tầng): xác định còn nợ review định kỳ hay không, để Router
	// cưỡng chế gọi editor thay vì phó mặc Coordinator LLM tuân tín hiệu review_required.
	// Nguyên tắc: suy trạng thái từ ĐĨA (batch gần nhất đã có file review global chưa) đáng tin
	// hơn một cờ Flow="reviewing" — cờ mất khi tiến trình crash, còn file review trên đĩa thì không.
	if !progress.Layered && s.LastCompleted > 0 {
		// Mốc batch gần nhất: bội số ReviewInterval lớn nhất không vượt quá số chương đã hoàn thành.
		// Dùng QualityReviewInterval từ config nếu có, nếu không dùng mặc định.
		reviewInterval := domain.GetReviewInterval(s.QualityReviewInterval)
		if mark := (s.LastCompleted / reviewInterval) * reviewInterval; mark > 0 {
			// LoadLastReview quét ngược tìm review global gần nhất (chương <= LastCompleted).
			// Chưa có review nào, hoặc review gần nhất còn cũ hơn mốc batch → batch gần nhất chưa được phủ.
			// Đọc lỗi cũng coi như còn nợ: thiên về giao lại editor thay vì âm thầm bỏ qua review
			// (đồng bộ triết lý fail-toward-review của HasArcReview ở nhánh phân tầng).
			last, lerr := store.World.LoadLastReview(s.LastCompleted)
			if lerr != nil {
				s.LoadWarnings = append(s.LoadWarnings, "LoadLastReview: "+lerr.Error())
				slog.Warn("LoadState LoadLastReview failed", "error", lerr)
				s.HasPendingFlatReview = true
			} else if last == nil || last.Chapter < mark {
				s.HasPendingFlatReview = true
			}
		}
	}

	return s
}
