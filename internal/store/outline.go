package store

import (
	"fmt"
	"os"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// OutlineStore quản lý tiền đề câu chuyện, đề cương (phẳng/phân cấp) và la bàn.
type OutlineStore struct{ io *IO }

func NewOutlineStore(io *IO) *OutlineStore { return &OutlineStore{io: io} }

// SavePremise lưu tiền đề câu chuyện vào premise.md.
func (s *OutlineStore) SavePremise(content string) error {
	return s.io.WriteMarkdown("premise.md", content)
}

// LoadPremise đọc premise.md. Trả về chuỗi rỗng nếu không tồn tại.
func (s *OutlineStore) LoadPremise() (string, error) {
	data, err := s.io.ReadFile("premise.md")
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

// SaveOutline lưu đồng thời outline.json và outline.md (ghi nguyên tử).
func (s *OutlineStore) SaveOutline(entries []domain.OutlineEntry) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("outline.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("outline.md", renderOutline(entries))
	})
}

// LoadOutline đọc đề cương có cấu trúc từ outline.json.
func (s *OutlineStore) LoadOutline() ([]domain.OutlineEntry, error) {
	var entries []domain.OutlineEntry
	if err := s.io.ReadJSON("outline.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// GetChapterOutline lấy mục đề cương của chương được chỉ định.
func (s *OutlineStore) GetChapterOutline(chapter int) (*domain.OutlineEntry, error) {
	entries, err := s.LoadOutline()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Chapter == chapter {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("chapter %d not found in outline", chapter)
}

// SaveLayeredOutline lưu đề cương phân cấp (chế độ truyện dài, ghi nguyên tử).
func (s *OutlineStore) SaveLayeredOutline(volumes []domain.VolumeOutline) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("layered_outline.json", volumes); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("layered_outline.md", renderLayeredOutline(volumes))
	})
}

// LoadLayeredOutline đọc đề cương phân cấp.
func (s *OutlineStore) LoadLayeredOutline() ([]domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSON("layered_outline.json", &volumes); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return volumes, nil
}

// ClearLayeredOutline xóa các file đề cương phân cấp.
func (s *OutlineStore) ClearLayeredOutline() error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.RemoveFileUnlocked("layered_outline.json"); err != nil {
			return err
		}
		return s.io.RemoveFileUnlocked("layered_outline.md")
	})
}

// GetChapterFromLayered tìm kiếm theo số chương toàn cục trong đề cương phân cấp.
func (s *OutlineStore) GetChapterFromLayered(chapter int) (*domain.OutlineEntry, error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil {
		return nil, err
	}
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for i := range a.Chapters {
				if ch == chapter {
					e := a.Chapters[i]
					e.Chapter = ch
					return &e, nil
				}
				ch++
			}
		}
	}
	return nil, fmt.Errorf("chapter %d not found in layered outline", chapter)
}

// LocateChapter xác định vị trí tập và cung truyện dựa theo số chương toàn cục.
func (s *OutlineStore) LocateChapter(chapter int) (volume, arc int, err error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil {
		return 0, 0, err
	}
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for range a.Chapters {
				if ch == chapter {
					return v.Index, a.Index, nil
				}
				ch++
			}
		}
	}
	return 0, 0, fmt.Errorf("chapter %d not found in layered outline", chapter)
}

// ArcBoundary thông tin biên giới cung truyện.
type ArcBoundary struct {
	IsArcEnd       bool
	IsVolumeEnd    bool
	Volume         int
	Arc            int
	NextVolume     int
	NextArc        int
	NeedsExpansion bool
	NeedsNewVolume bool // cuối tập và layered_outline hiện tại không có tập tiếp theo
}

// HasNextArc kiểm tra xem còn cung truyện tiếp theo hay không.
func (b *ArcBoundary) HasNextArc() bool {
	return b.NextVolume > 0 || b.NextArc > 0
}

// CheckArcBoundary kiểm tra xem một chương có phải là chương cuối của cung/tập không.
func (s *OutlineStore) CheckArcBoundary(chapter int) (*ArcBoundary, error) {
	volumes, err := s.LoadLayeredOutline()
	if err != nil || len(volumes) == 0 {
		return nil, err
	}

	type arcPos struct {
		volIdx, arcIdx int
		volume, arc    int
		chInArc        int
		arcLen         int
	}

	ch := 1
	var cur *arcPos
	for vi, v := range volumes {
		for ai, a := range v.Arcs {
			for ci := range a.Chapters {
				if ch == chapter {
					cur = &arcPos{
						volIdx:  vi,
						arcIdx:  ai,
						volume:  v.Index,
						arc:     a.Index,
						chInArc: ci,
						arcLen:  len(a.Chapters),
					}
				}
				ch++
			}
		}
	}
	if cur == nil {
		return nil, nil
	}

	b := &ArcBoundary{
		Volume: cur.volume,
		Arc:    cur.arc,
	}

	isLastChInArc := cur.chInArc == cur.arcLen-1
	isLastArcInVol := cur.arcIdx == len(volumes[cur.volIdx].Arcs)-1

	// Next*/NeedsExpansion/NeedsNewVolume chỉ có ý nghĩa ở cuối cung, nếu không Điều phối viên sẽ hiểu nhầm là cần mở rộng cung tiếp theo sớm.
	if !isLastChInArc {
		return b, nil
	}

	b.IsArcEnd = true
	if isLastArcInVol {
		b.IsVolumeEnd = true
	}

	found := false
	for vi := cur.volIdx; vi < len(volumes); vi++ {
		startArc := 0
		if vi == cur.volIdx {
			startArc = cur.arcIdx + 1
		}
		for ai := startArc; ai < len(volumes[vi].Arcs); ai++ {
			b.NextVolume = volumes[vi].Index
			b.NextArc = volumes[vi].Arcs[ai].Index
			b.NeedsExpansion = !volumes[vi].Arcs[ai].IsExpanded()
			found = true
			break
		}
		if found {
			break
		}
	}

	if b.IsVolumeEnd && !found {
		b.NeedsNewVolume = true
	}

	return b, nil
}

// expandArcUnlocked phương thức nội bộ, được gọi trong quá trình phối hợp liên miền tại Store.ExpandArc.
func (s *OutlineStore) expandArcUnlocked(volumeIdx, arcIdx int, chapters []domain.OutlineEntry) ([]domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSONUnlocked("layered_outline.json", &volumes); err != nil {
		return nil, fmt.Errorf("load layered_outline: %w", err)
	}
	found := false
	for vi := range volumes {
		if volumes[vi].Index != volumeIdx {
			continue
		}
		for ai := range volumes[vi].Arcs {
			if volumes[vi].Arcs[ai].Index != arcIdx {
				continue
			}
			volumes[vi].Arcs[ai].Chapters = chapters
			volumes[vi].Arcs[ai].EstimatedChapters = 0
			found = true
			break
		}
		if found {
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("arc not found: volume=%d, arc=%d", volumeIdx, arcIdx)
	}
	if err := s.io.WriteJSONUnlocked("layered_outline.json", volumes); err != nil {
		return nil, err
	}
	if err := s.io.WriteMarkdownUnlocked("layered_outline.md", renderLayeredOutline(volumes)); err != nil {
		return nil, err
	}
	flat := domain.FlattenOutline(volumes)
	if err := s.io.WriteJSONUnlocked("outline.json", flat); err != nil {
		return nil, err
	}
	if err := s.io.WriteMarkdownUnlocked("outline.md", renderOutline(flat)); err != nil {
		return nil, err
	}
	return volumes, nil
}

// appendVolumeUnlocked phương thức nội bộ, được gọi trong quá trình phối hợp liên miền tại Store.AppendVolume.
func (s *OutlineStore) appendVolumeUnlocked(vol domain.VolumeOutline) ([]domain.VolumeOutline, error) {
	var volumes []domain.VolumeOutline
	if err := s.io.ReadJSONUnlocked("layered_outline.json", &volumes); err != nil {
		return nil, fmt.Errorf("load layered_outline: %w", err)
	}
	if err := validateAppendVolume(volumes, vol); err != nil {
		return nil, err
	}
	volumes = append(volumes, vol)
	if err := s.io.WriteJSONUnlocked("layered_outline.json", volumes); err != nil {
		return nil, err
	}
	if err := s.io.WriteMarkdownUnlocked("layered_outline.md", renderLayeredOutline(volumes)); err != nil {
		return nil, err
	}
	flat := domain.FlattenOutline(volumes)
	if err := s.io.WriteJSONUnlocked("outline.json", flat); err != nil {
		return nil, err
	}
	if err := s.io.WriteMarkdownUnlocked("outline.md", renderOutline(flat)); err != nil {
		return nil, err
	}
	return volumes, nil
}

func validateAppendVolume(existing []domain.VolumeOutline, vol domain.VolumeOutline) error {
	if len(existing) > 0 {
		maxIdx := existing[len(existing)-1].Index
		if vol.Index <= maxIdx {
			return fmt.Errorf("Index tập %d phải lớn hơn giá trị lớn nhất hiện có %d", vol.Index, maxIdx)
		}
	}
	if len(vol.Arcs) == 0 {
		return fmt.Errorf("tập mới phải chứa ít nhất một cung truyện")
	}
	if !vol.Arcs[0].IsExpanded() {
		return fmt.Errorf("cung truyện đầu tiên của tập mới phải chứa các chương chi tiết")
	}
	return nil
}

// SaveCompass lưu la bàn định hướng kết thúc.
func (s *OutlineStore) SaveCompass(compass domain.StoryCompass) error {
	if compass.EndingDirection == "" {
		return fmt.Errorf("ending_direction không được để trống")
	}
	return s.io.WriteJSON("meta/compass.json", compass)
}

// LoadCompass đọc la bàn định hướng kết thúc.
func (s *OutlineStore) LoadCompass() (*domain.StoryCompass, error) {
	var c domain.StoryCompass
	if err := s.io.ReadJSON("meta/compass.json", &c); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// SaveNarrative lưu hợp đồng tường thuật (POV/thì) vào meta/narrative.json.
// Persist cùng chỗ các artifact foundation khác (meta/*.json, như compass).
func (s *OutlineStore) SaveNarrative(n domain.NarrativeContract) error {
	return s.io.WriteJSON("meta/narrative.json", n)
}

// LoadNarrative đọc hợp đồng tường thuật. Trả về nil khi chưa khai (foundation cũ),
// để phía gọi bỏ qua êm bằng NarrativeContract.IsEmpty.
func (s *OutlineStore) LoadNarrative() (*domain.NarrativeContract, error) {
	var n domain.NarrativeContract
	if err := s.io.ReadJSON("meta/narrative.json", &n); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &n, nil
}

func renderLayeredOutline(volumes []domain.VolumeOutline) string {
	var b strings.Builder
	b.WriteString("# Đề cương phân cấp\n\n")
	ch := 1
	for _, v := range volumes {
		fmt.Fprintf(&b, "## Tập %d: %s\n\n", v.Index, v.Title)
		fmt.Fprintf(&b, "**Chủ đề**: %s\n\n", v.Theme)
		for _, a := range v.Arcs {
			fmt.Fprintf(&b, "### Cung %d: %s\n\n", a.Index, a.Title)
			fmt.Fprintf(&b, "**Mục tiêu**: %s\n\n", a.Goal)
			if !a.IsExpanded() {
				fmt.Fprintf(&b, "*(chưa mở rộng, ước tính %d chương)*\n\n", a.EstimatedChapters)
				continue
			}
			for _, e := range a.Chapters {
				fmt.Fprintf(&b, "#### Chương %d: %s\n\n", ch, e.Title)
				fmt.Fprintf(&b, "**Sự kiện cốt lõi**: %s\n\n", e.CoreEvent)
				if e.Hook != "" {
					fmt.Fprintf(&b, "**Điểm móc**: %s\n\n", e.Hook)
				}
				ch++
			}
		}
	}
	return b.String()
}

func renderOutline(entries []domain.OutlineEntry) string {
	var b strings.Builder
	b.WriteString("# Đề cương\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "## Chương %d: %s\n\n", e.Chapter, e.Title)
		fmt.Fprintf(&b, "**Sự kiện cốt lõi**: %s\n\n", e.CoreEvent)
		if e.Hook != "" {
			fmt.Fprintf(&b, "**Điểm móc**: %s\n\n", e.Hook)
		}
		if len(e.Scenes) > 0 {
			b.WriteString("**Cảnh**: \n")
			for i, sc := range e.Scenes {
				fmt.Fprintf(&b, "%d. %s\n", i+1, sc)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
