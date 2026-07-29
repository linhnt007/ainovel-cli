package flow

import (
	"log/slog"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// LoadState đọc toàn bộ dữ liệu cần thiết cho Route từ Store.
// Đây là "ranh giới IO" của router: mọi thao tác đọc tập trung tại đây, Route giữ nguyên trạng thái thuần túy.
// Khi đọc thất bại, các giá trị mặc định an toàn được dùng (has*=false, boundary=nil), giúp Router ưu tiên giao lại thay vì bỏ qua.
// Khi progress load fail, tạo fallback progress (PhaseUnknown, chapter=0) thay vì trả về nil — giúp Router
// vẫn có thể dispatch writer ở safe mode thay vì crash hoặc để coordinator tự quyết định mù.
func LoadState(store *storepkg.Store, qualityReviewInterval ...int) State {
	s := State{
		FoundationMissing: store.FoundationMissing(),
	}
	if len(qualityReviewInterval) > 0 {
		s.QualityReviewInterval = qualityReviewInterval[0]
	}
	progress, err := store.Progress.Load()
	if err != nil {
		s.LoadWarnings = append(s.LoadWarnings, "progress.Load: "+err.Error())
		slog.Warn("LoadState progress.Load failed, using fallback progress", "error", err)
		// Fallback progress: PhaseWriting + CurrentChapter=1 để Router có thể dispatch writer an toàn.
		// Đây là "safe mode" — không crash, không để coordinator quyết định mù, user vẫn có thể tiếp tục.
		s.Progress = &domain.Progress{
			Phase:          domain.PhaseWriting,
			CurrentChapter: 1,
			Flow:           domain.FlowWriting,
		}
		s.LoadDegraded = true
		return s
	}
	if progress == nil {
		if err == nil {
			s.LoadWarnings = append(s.LoadWarnings, "progress.Load returned nil")
			slog.Warn("LoadState progress is nil, using fallback progress")
		}
		// Fallback progress cho trường hợp file chưa tồn tại (lần đầu khởi động).
		s.Progress = &domain.Progress{
			Phase:          domain.PhaseWriting,
			CurrentChapter: 1,
			Flow:           domain.FlowWriting,
		}
		s.LoadDegraded = true
		return s
	}
	s.Progress = progress

	// Tự động phục hồi PhaseNếu progress bị rớt về PhaseOutline nhưng đĩa đã có sẵn outline
	if s.Progress.Phase == domain.PhaseOutline {
		if vols, _ := store.Outline.LoadLayeredOutline(); len(vols) > 0 {
			s.Progress.Phase = domain.PhaseWriting
			if s.Progress.CurrentChapter <= 0 {
				s.Progress.CurrentChapter = 1
			}
			_ = store.Progress.Save(s.Progress)
		}
	}

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

	// Xác định còn nợ review định kỳ hay không, để Router cưỡng chế gọi editor
	// (áp dụng cho cả Flat Mode lẫn Layered Mode).
	if s.LastCompleted > 0 {
		// Mốc batch gần nhất: bội số ReviewInterval lớn nhất không vượt quá số chương đã hoàn thành.
		// Dùng QualityReviewInterval từ config nếu có, nếu không dùng mặc định.
		reviewInterval := domain.GetReviewInterval(s.QualityReviewInterval)
		if mark := (s.LastCompleted / reviewInterval) * reviewInterval; mark > 0 {
			// LoadLastReviewAny quét ngược tìm review (global hoặc chapter) gần nhất (chương <= LastCompleted).
			last, lerr := store.World.LoadLastReviewAny(s.LastCompleted)
			if lerr != nil {
				s.LoadWarnings = append(s.LoadWarnings, "LoadLastReviewAny: "+lerr.Error())
				slog.Warn("LoadState LoadLastReviewAny failed", "error", lerr)
				s.HasPendingFlatReview = true
			} else if last == nil || last.Chapter < mark {
				s.HasPendingFlatReview = true
			}
		}
	}

	// Set LoadDegraded nếu có bất kỳ warning nào.
	if len(s.LoadWarnings) > 0 {
		s.LoadDegraded = true
	}

	return s
}
