package store

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// ProgressStore quản lý trạng thái tiến độ sáng tác.
type ProgressStore struct{ io *IO }

func NewProgressStore(io *IO) *ProgressStore { return &ProgressStore{io: io} }

// Load đọc meta/progress.json. Trả về nil nếu file không tồn tại.
func (s *ProgressStore) Load() (*domain.Progress, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadUnlocked()
}

func (s *ProgressStore) loadUnlocked() (*domain.Progress, error) {
	var p domain.Progress
	if err := s.io.ReadJSONUnlocked("meta/progress.json", &p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// Chuẩn hoá giá trị flow legacy: build orchestrator-era từng persist flow="reviewing"
	// vào meta/progress.json. Sau khi FlowReviewing bị gỡ khỏi domain, "reviewing" không
	// còn nằm trong máy trạng thái nữa → CanTransitionFlow("reviewing", X) rơi vào default
	// false, khiến MỌI SetFlow trên sách cũ đang dở review đều fail (kéo theo save_review
	// không ghi được flow, cờ "còn nợ review" kẹt mãi). Normalize về writing ngay khi load:
	// reviewing vốn chỉ là trạng thái tạm giữa các vòng viết, writing là điểm nghỉ an toàn
	// tương đương và là nguồn hợp lệ để chuyển sang mọi flow khác.
	if p.Flow == "reviewing" {
		p.Flow = domain.FlowWriting
	}
	return &p, nil
}

// Save lưu tiến độ.
func (s *ProgressStore) Save(p *domain.Progress) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.saveUnlocked(p)
}

func (s *ProgressStore) saveUnlocked(p *domain.Progress) error {
	return s.io.WriteJSONUnlocked("meta/progress.json", p)
}

// Init tạo tiến độ ban đầu.
func (s *ProgressStore) Init(novelName string, totalChapters int) error {
	return s.Save(&domain.Progress{
		NovelName:     novelName,
		Phase:         domain.PhaseInit,
		TotalChapters: totalChapters,
	})
}

// SetTotalChapters đặt tổng số chương.
func (s *ProgressStore) SetTotalChapters(n int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.TotalChapters = n
		return s.saveUnlocked(p)
	})
}

// SetNovelName đặt tên tác phẩm, giá trị rỗng sẽ bị bỏ qua.
func (s *ProgressStore) SetNovelName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.NovelName = name
		return s.saveUnlocked(p)
	})
}

// UpdatePhase cập nhật giai đoạn sáng tác.
func (s *ProgressStore) UpdatePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// StartChapter đánh dấu một chương chuyển sang trạng thái đang viết. Thuần IO, không kiểm tra trạng thái.
func (s *ProgressStore) StartChapter(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0")
	}
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.Phase = domain.PhaseWriting
		if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
			p.Flow = domain.FlowWriting
		}
		if p.CurrentChapter < chapter {
			p.CurrentChapter = chapter
		}
		p.InProgressChapter = chapter
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// IsChapterCompleted kiểm tra xem chương đã được lưu và hoàn thành chưa.
func (s *ProgressStore) IsChapterCompleted(chapter int) bool {
	p, err := s.Load()
	if err != nil || p == nil {
		return false
	}
	return slices.Contains(p.CompletedChapters, chapter)
}

// MarkChapterComplete đánh dấu chương hoàn thành, cập nhật tiến độ theo cách nguyên tử.
func (s *ProgressStore) MarkChapterComplete(chapter, wordCount int, hookType, dominantStrand string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("tiến độ chưa được khởi tạo, hãy gọi Init trước")
		}
		if p.ChapterWordCounts == nil {
			p.ChapterWordCounts = make(map[int]int)
		}
		if oldWC, ok := p.ChapterWordCounts[chapter]; ok {
			p.TotalWordCount -= oldWC
		}
		p.ChapterWordCounts[chapter] = wordCount
		p.TotalWordCount += wordCount
		if !slices.Contains(p.CompletedChapters, chapter) {
			p.CompletedChapters = append(p.CompletedChapters, chapter)
		}
		if chapter+1 > p.CurrentChapter {
			p.CurrentChapter = chapter + 1
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseWriting); err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting

		if dominantStrand != "" {
			for len(p.StrandHistory) < chapter-1 {
				p.StrandHistory = append(p.StrandHistory, "")
			}
			if len(p.StrandHistory) < chapter {
				p.StrandHistory = append(p.StrandHistory, dominantStrand)
			} else {
				p.StrandHistory[chapter-1] = dominantStrand
			}
		}
		if hookType != "" {
			for len(p.HookHistory) < chapter-1 {
				p.HookHistory = append(p.HookHistory, "")
			}
			if len(p.HookHistory) < chapter {
				p.HookHistory = append(p.HookHistory, hookType)
			} else {
				p.HookHistory[chapter-1] = hookType
			}
		}

		return s.saveUnlocked(p)
	})
}

// MarkComplete đánh dấu toàn bộ tác phẩm đã hoàn thành sáng tác, đồng thời xóa cờ mở lại để chỉnh sửa (hoàn kết nghĩa là không còn ở trạng thái chỉnh sửa nữa).
func (s *ProgressStore) MarkComplete() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseComplete); err != nil {
			return err
		}
		p.Phase = domain.PhaseComplete
		p.ReopenedFromComplete = false
		return s.saveUnlocked(p)
	})
}

// Reopen mở lại tác phẩm đã hoàn kết để vào trạng thái chỉnh sửa: phase complete→writing + đưa chương mục tiêu vào hàng đợi + flow=rewriting,
// thực hiện nguyên tử trong một lần ghi lock. Đây là lối thoát miễn trừ duy nhất của ràng buộc “chỉ tiến” phaseOrder — cố ý không dùng
// ValidatePhaseTransition; tính hợp lệ của việc lùi phase hội tụ trong phương thức này và được bảo vệ bởi điều kiện tiên quyết phase=complete,
// tránh dùng sai khiến máy trạng thái mất kiểm soát. Sau khi cập nhật hàng đợi, commit_chapter sẽ tự động hoàn kết lại.
func (s *ProgressStore) Reopen(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("tiến độ chưa được khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("reopen chỉ áp dụng cho tác phẩm đã hoàn kết (phase hiện tại=%s): %w", p.Phase, errs.ErrToolPrecondition)
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting // lùi phase hợp lệ duy nhất, được bảo vệ bởi ràng buộc complete phía trên
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		p.Flow = domain.FlowRewriting
		p.ReopenedFromComplete = true // sau khi hàng đợi rỗng sẽ hoàn kết lại theo cấu trúc đầy đủ, xem khối drain trong commit_chapter
		return s.saveUnlocked(p)
	})
}

// ClearInProgress xóa trạng thái trung gian trong tiến độ.
func (s *ProgressStore) ClearInProgress() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// UpdateVolumeArc cập nhật vị trí tập/cung truyện hiện tại.
func (s *ProgressStore) UpdateVolumeArc(volume, arc int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.CurrentVolume = volume
		p.CurrentArc = arc
		return s.saveUnlocked(p)
	})
}

// SetLayered đặt cờ chế độ phân lớp.
func (s *ProgressStore) SetLayered(layered bool) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.Layered = layered
		return s.saveUnlocked(p)
	})
}

// SetFlow cập nhật trạng thái luồng hiện tại.
func (s *ProgressStore) SetFlow(flow domain.FlowState) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
			return err
		}
		p.Flow = flow
		return s.saveUnlocked(p)
	})
}

// SetPendingRewrites đặt hàng đợi chương cần viết lại và lý do.
// PendingRewrites chỉ được chứa các chương đã hoàn thành; chương chưa hoàn thành chưa có bản thảo cuối, không thể vào hàng đợi viết lại/trau chuốt.
func (s *ProgressStore) SetPendingRewrites(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		return s.saveUnlocked(p)
	})
}

// ValidatePendingRewrites kiểm tra danh sách chương có thể vào hàng đợi chỉnh sửa hay không, không thay đổi trạng thái.
func (s *ProgressStore) ValidatePendingRewrites(chapters []int) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()

	p, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		_, err := normalizePendingRewrites(chapters, nil)
		return err
	}
	_, err = normalizePendingRewrites(chapters, p.CompletedChapters)
	return err
}

// CompleteRewrite xóa chương đã hoàn thành khỏi hàng đợi viết lại.
func (s *ProgressStore) CompleteRewrite(chapter int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		var remaining []int
		for _, ch := range p.PendingRewrites {
			if ch != chapter {
				remaining = append(remaining, ch)
			}
		}
		p.PendingRewrites = remaining
		if len(remaining) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
				return err
			}
			p.Flow = domain.FlowWriting
			p.RewriteReason = ""
		}
		return s.saveUnlocked(p)
	})
}

// ClearPendingRewrites buộc xóa toàn bộ hàng đợi viết lại.
func (s *ProgressStore) ClearPendingRewrites() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.PendingRewrites = nil
		p.RewriteReason = ""
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		return s.saveUnlocked(p)
	})
}

// ValidateChapterWork kiểm tra xem chương hiện tại có được phép lập kế hoạch hoặc lưu không.
// Trong luồng trau chuốt/viết lại, chỉ được xử lý các chương có trong PendingRewrites.
func (s *ProgressStore) ValidateChapterWork(chapter int) error {
	p, err := s.Load()
	if err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
		return nil
	}
	if _, err := normalizePendingRewrites(p.PendingRewrites, p.CompletedChapters); err != nil {
		return err
	}
	if slices.Contains(p.PendingRewrites, chapter) {
		return nil
	}

	verb := "viết lại"
	if p.Flow == domain.FlowPolishing {
		verb = "trau chuốt"
	}
	return fmt.Errorf("chương %d không có trong hàng đợi %s, hàng đợi hiện tại: %v. Hãy xử lý các chương trong hàng đợi trước, rồi mới sang chương mới: %w", chapter, verb, p.PendingRewrites, errs.ErrToolConflict)
}

func normalizePendingRewrites(chapters, completed []int) ([]int, error) {
	if len(chapters) == 0 {
		return nil, nil
	}
	completedSet := make(map[int]struct{}, len(completed))
	for _, ch := range completed {
		completedSet[ch] = struct{}{}
	}

	seen := make(map[int]struct{}, len(chapters))
	normalized := make([]int, 0, len(chapters))
	var invalid []int
	for _, ch := range chapters {
		if ch <= 0 {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := completedSet[ch]; !ok {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		normalized = append(normalized, ch)
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("pending_rewrites chỉ được chứa các chương đã hoàn thành, chương không hợp lệ: %v, completed_chapters=%v: %w", invalid, completed, errs.ErrToolPrecondition)
	}
	return normalized, nil
}
