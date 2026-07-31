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
func LoadState(store *storepkg.Store, qualityReviewInterval, humanGateEvery int) State {
	s := State{
		FoundationMissing:     store.FoundationMissing(),
		QualityReviewInterval: qualityReviewInterval,
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

	// Unified review check: thay thế HasPendingFlatReview + NeedsChapterReview.
	// Chỉ kiểm tra single review file (LoadReview), không còn LoadLastReviewAny.
	// Nếu đúng mốc batch (ReviewInterval), đặt IsReviewBatch=true để Router
	// dispatch editor với scope=both (batch + single trong 1 lần gọi).
	if s.LastCompleted > 0 {
		isArcBoundary := s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd
		if !isArcBoundary {
			review, rerr := store.World.LoadReview(s.LastCompleted)
			if rerr != nil {
				s.LoadWarnings = append(s.LoadWarnings, "LoadReview: "+rerr.Error())
			} else if review == nil {
				s.NeedsReviewChapter = s.LastCompleted
				reviewInterval := domain.GetReviewInterval(s.QualityReviewInterval)
				if s.LastCompleted%reviewInterval == 0 {
					s.IsReviewBatch = true
				}
			}
		}
	}

	// Human gate: chỉ arm khi mọi việc router làm TRƯỚC bước gate (steps 3-10) cho
	// LastCompleted đã xong. Nếu arm sớm khi còn editor review nợ, qualityControlGate
	// sẽ chặn chính subagent editor đang được router cử đi → deadlock (chương mốc gate
	// vừa hoàn thành chưa review → lặp dispatch editor bị chặn 3 lần → circuit breaker).
	if storedEvery := store.Progress.HumanGateEvery(); storedEvery > 0 {
		humanGateEvery = storedEvery
	}
	s.HumanGateEvery = humanGateEvery
	if s.LastCompleted > 0 && humanGateEvery > 0 &&
		s.LastCompleted%humanGateEvery == 0 &&
		!store.World.HasHumanGateAck(s.LastCompleted) &&
		!s.hasPendingPreGateWork() {
		s.HumanGatePending = true
	}

	// Set LoadDegraded nếu có bất kỳ warning nào.
	if len(s.LoadWarnings) > 0 {
		s.LoadDegraded = true
	}

	return s
}

// hasPendingPreGateWork báo còn việc router làm trước bước human gate (step 10.5):
// rewrite queue, re-review sau rewrite, editor review chương mới, hậu xử lý cuối cung truyện.
// Gate KHÔNG được arm khi còn các việc này, vì qualityControlGate chặn mọi subagent
// khi gate pending — arm sớm sẽ chặn chính agent được router cử đi (deadlock).
func (s *State) hasPendingPreGateWork() bool {
	if s.Progress != nil {
		if len(s.Progress.PendingRewrites) > 0 || s.Progress.NeedsRewriteReview > 0 {
			return true
		}
	}
	if s.NeedsReviewChapter > 0 {
		return true
	}
	if s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		if !s.HasArcReview || !s.HasArcSummary {
			return true
		}
		if s.ArcBoundary.IsVolumeEnd && !s.HasVolumeSummary {
			return true
		}
		if s.ArcBoundary.NeedsExpansion || s.ArcBoundary.NeedsNewVolume {
			return true
		}
	}
	return false
}
