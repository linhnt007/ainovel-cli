package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// Store là gốc kết hợp của quản lý trạng thái, giữ tất cả các store con.
type Store struct {
	dir string

	Progress    *ProgressStore
	Outline     *OutlineStore
	Drafts      *DraftStore
	Summaries   *SummaryStore
	RunMeta     *RunMetaStore
	Directives  *DirectivesStore
	Signals     *SignalStore
	Runtime     *RuntimeStore
	Characters  *CharacterStore
	Cast        *CastStore
	World       *WorldStore
	Checkpoints *CheckpointStore
	Sessions    *SessionStore
	Usage       *UsageStore
	Simulation  *SimulationStore
	Checks      *CheckStore     // Light Gate: kết quả kiểm tra chất lượng chương
	Notebooks   *NotebookStore  // Notebook: ghi chú của writer

	crossMu sync.Mutex // bảo vệ các thao tác nguyên tử liên miền
}

// NewStore tạo bộ quản lý trạng thái, dir là thư mục gốc đầu ra của tiểu thuyết.
func NewStore(dir string) *Store {
	io := newIO(dir)
	outline := NewOutlineStore(io)
	return &Store{
		dir:         dir,
		Progress:    NewProgressStore(newIO(dir)),
		Outline:     outline,
		Drafts:      NewDraftStore(newIO(dir)),
		Summaries:   NewSummaryStore(newIO(dir), outline),
		RunMeta:     NewRunMetaStore(newIO(dir)),
		Directives:  NewDirectivesStore(newIO(dir)),
		Signals:     NewSignalStore(newIO(dir)),
		Runtime:     NewRuntimeStore(newIO(dir)),
		Characters:  NewCharacterStore(newIO(dir), outline),
		Cast:        NewCastStore(newIO(dir)),
		World:       NewWorldStore(newIO(dir)),
		Checkpoints: NewCheckpointStore(io),
		Sessions:    NewSessionStore(newIO(dir)),
		Usage:       NewUsageStore(newIO(dir)),
		Simulation:  NewSimulationStore(newIO(dir)),
		Checks:      NewCheckStore(newIO(dir)),
		Notebooks:   NewNotebookStore(newIO(dir)),
	}
}

// Dir trả về thư mục gốc đầu ra.
func (s *Store) Dir() string { return s.dir }

// ResetAll xóa toàn bộ dữ liệu dự án và khởi tạo lại cấu trúc thư mục rỗng.
// Dùng cho lệnh /new từ TUI.
func (s *Store) ResetAll() error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	dir := s.dir
	dataDirs := []string{
		"chapters", "drafts", "summaries", "reviews", "meta",
	}
	for _, d := range dataDirs {
		if err := os.RemoveAll(filepath.Join(dir, d)); err != nil {
			return fmt.Errorf("remove %s: %w", d, err)
		}
	}

	rootFiles := []string{
		"premise.md", "outline.md", "outline.json",
		"characters.json", "characters.md",
		"world_rules.md", "world_building.md",
	}
	for _, f := range rootFiles {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", f, err)
		}
	}

	return s.Init()
}

// CheckConsistency thực hiện một lần kiểm tra nông trên tầng dữ liệu, dùng để sinh cảnh báo khi khởi động/phục hồi.
// Hoàn toàn chỉ đọc: không sửa dữ liệu, chỉ trả về mô tả vấn đề có thể đọc được. Bên gọi quyết định cách hiển thị (log / UI).
// Để tránh chi phí IO khi quét toàn bộ thư mục, chỉ kiểm tra các điểm then chốt của Progress:
//   - Chương hoàn thành cuối cùng phải có bản thảo hoàn chỉnh trong chapters/
//   - Ở chế độ Layered, Volume/Arc hiện tại phải tìm được trong layered_outline
func (s *Store) CheckConsistency() []string {
	var warnings []string
	progress, err := s.Progress.Load()
	if err != nil || progress == nil {
		return warnings
	}
	// Kiểm tra tất cả completed chapters có tồn tại trên disk không
	for _, ch := range progress.CompletedChapters {
		if text, err := s.Drafts.LoadChapterText(ch); err == nil && text == "" {
			warnings = append(warnings, fmt.Sprintf("progress đánh dấu chương %d đã hoàn thành, nhưng chapters/%02d.md không tồn tại hoặc rỗng", ch, ch))
		}
	}
	if progress.Layered && progress.CurrentVolume > 0 && progress.CurrentArc > 0 {
		volumes, err := s.Outline.LoadLayeredOutline()
		if err == nil && len(volumes) > 0 {
			found := false
			for _, v := range volumes {
				if v.Index != progress.CurrentVolume {
					continue
				}
				for _, a := range v.Arcs {
					if a.Index == progress.CurrentArc {
						found = true
						break
					}
				}
				break
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("progress hiện tại V%d A%d không tìm thấy mục tương ứng trong đề cương phân lớp", progress.CurrentVolume, progress.CurrentArc))
			}
		}
	}
	return warnings
}

// MissingCompletedChapters trả về danh sách chương đã hoàn thành trong progress nhưng không có file trên disk.
// Dùng để phát hiện "output bị xóa nhưng meta còn" — silent corruption.
func (s *Store) MissingCompletedChapters() ([]int, error) {
	progress, err := s.Progress.Load()
	if err != nil || progress == nil {
		return nil, err
	}
	var missing []int
	for _, ch := range progress.CompletedChapters {
		if text, err := s.Drafts.LoadChapterText(ch); err == nil && text == "" {
			missing = append(missing, ch)
		}
	}
	return missing, nil
}

// FoundationMissing trả về các mục còn thiếu trong cài đặt nền tảng, theo thứ tự ổn định dùng cho Prompt/Reminder.
// Chế độ dài tập (đã có layered_outline) yêu cầu thêm compass.
func (s *Store) FoundationMissing() []string {
	var missing []string
	if p, _ := s.Outline.LoadPremise(); p == "" {
		missing = append(missing, "premise")
	}
	if o, _ := s.Outline.LoadOutline(); len(o) == 0 {
		missing = append(missing, "outline")
	}
	if c, _ := s.Characters.Load(); len(c) == 0 {
		missing = append(missing, "characters")
	}
	if r, _ := s.World.LoadWorldRules(); len(r) == 0 {
		missing = append(missing, "world_rules")
	}
	if layered, _ := s.Outline.LoadLayeredOutline(); len(layered) > 0 {
		if c, _ := s.Outline.LoadCompass(); c == nil {
			missing = append(missing, "compass")
		}
	}
	return missing
}

// Init tạo cấu trúc thư mục con cần thiết.
func (s *Store) Init() error {
	return s.Progress.io.EnsureDirs([]string{
		"chapters", "summaries", "drafts", "reviews", "meta", "meta/runtime", "meta/runtime/tasks", "meta/sessions", "meta/sessions/agents", "meta/checks",
	})
}

// ── Phương thức điều phối liên miền ──

// ExpandArc mở rộng cung truyện khung thành các chương chi tiết (Outline + Progress liên động).
func (s *Store) ExpandArc(volumeIdx, arcIdx int, chapters []domain.OutlineEntry) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.expandArcUnlocked(volumeIdx, arcIdx, chapters)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.TotalChapters(volumes)
	return s.Progress.saveUnlocked(p)
}

// AppendVolume thêm tập mới vào cuối đề cương phân lớp (Outline + Progress liên động).
func (s *Store) AppendVolume(vol domain.VolumeOutline) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.appendVolumeUnlocked(vol)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.TotalChapters(volumes)
	return s.Progress.saveUnlocked(p)
}

// ClearHandledSteer xóa PendingSteer theo cách nguyên tử và đặt lại trạng thái FlowSteering
// (RunMeta + Progress liên động).
func (s *Store) ClearHandledSteer() error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.RunMeta.io.mu.Lock()
	defer s.RunMeta.io.mu.Unlock()

	meta, err := s.RunMeta.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if meta != nil && meta.PendingSteer != "" {
		meta.PendingSteer = ""
		if err := s.RunMeta.saveUnlocked(*meta); err != nil {
			return err
		}
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p != nil && p.Flow == domain.FlowSteering {
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		if err := s.Progress.saveUnlocked(p); err != nil {
			return err
		}
	}
	return nil
}

// RollbackToChapter quay lui về chương target, xóa toàn bộ dữ liệu chương > target.
// target=0 nghĩa là chưa viết chương nào (sau architect).原子 thao tác qua crossMu.
func (s *Store) RollbackToChapter(target int) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	progress, err := s.Progress.Load()
	if err != nil {
		return fmt.Errorf("đọc tiến độ: %w", err)
	}
	if progress == nil {
		return fmt.Errorf("chưa có tiến độ để quay lui")
	}

	completed := progress.CompletedChapters
	if target > 0 && !slices.Contains(completed, target) {
		return fmt.Errorf("chương %d chưa hoàn thành, không thể quay lui về chương này", target)
	}

	// Xóa file chương > target (và cả file draft dở dang của target+1)
	toDelete := append([]int(nil), completed...)
	if progress.InProgressChapter > 0 {
		toDelete = append(toDelete, progress.InProgressChapter)
	}
	// Đảm bảo chương target+1 bị xoá draft/plan/chapter ngay cả khi chưa vào completed
	toDelete = append(toDelete, target+1)

	for _, ch := range toDelete {
		if ch <= target {
			continue
		}
		_ = s.Drafts.io.RemoveFile(fmt.Sprintf("chapters/%02d.md", ch))
		_ = s.Drafts.io.RemoveFile(fmt.Sprintf("drafts/%02d.draft.md", ch))
		_ = s.Drafts.io.RemoveFile(fmt.Sprintf("drafts/%02d.plan.json", ch))
		_ = s.Summaries.io.RemoveFile(fmt.Sprintf("summaries/%02d.json", ch))
		_ = s.World.io.RemoveFile(fmt.Sprintf("reviews/%02d.json", ch))
		_ = s.World.io.RemoveFile(fmt.Sprintf("reviews/%02d-global.json", ch))
	}

	// Xóa checkpoints và signals
	_ = s.Checkpoints.Reset()
	s.Signals.ClearStaleSignals()
	_ = s.Signals.ClearPendingCommit()
	_ = s.Runtime.Reset()

	// Cập nhật progress
	return s.Progress.io.WithWriteLock(func() error {
		p, err := s.Progress.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}

		// Lọc completed chapters
		var kept []int
		var wordCount int
		for _, ch := range p.CompletedChapters {
			if ch <= target {
				kept = append(kept, ch)
				if wc, ok := p.ChapterWordCounts[ch]; ok {
					wordCount += wc
				}
			}
		}

		p.CompletedChapters = kept
		p.TotalWordCount = wordCount

		// Xóa word counts của chương bị xóa
		if p.ChapterWordCounts != nil {
			for ch := range p.ChapterWordCounts {
				if ch > target {
					delete(p.ChapterWordCounts, ch)
				}
			}
		}

		// Đặt lại trạng thái
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		p.PendingRewrites = nil
		p.RewriteReason = ""
		p.ReopenedFromComplete = false
		p.Flow = domain.FlowWriting
		p.Phase = domain.PhaseWriting
		p.CurrentChapter = target + 1

		if target == 0 {
			p.CurrentVolume = 1
			p.CurrentArc = 1
			p.StrandHistory = nil
			p.HookHistory = nil
		} else {
			// Trim strand/hook history
			if len(p.StrandHistory) > target {
				p.StrandHistory = p.StrandHistory[:target]
			}
			if len(p.HookHistory) > target {
				p.HookHistory = p.HookHistory[:target]
			}
		}

		return s.Progress.saveUnlocked(p)
	})
}
