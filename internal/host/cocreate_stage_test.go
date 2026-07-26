package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newFlagTestHost tạo một Host tối giản, đủ để chạy máy trạng thái cờ cocreating và bảo vệ đồng thời.
// emitEvent dùng recover + select không chặn, chỉ cần buffer kênh events, không cần coordinator/observer.
// Nhánh trạng thái đang chạy của PauseForCoCreate sẽ gọi coordinator.Abort (tái dùng đường dừng Esc đã xác minh),
// không kiểm tra ở đây; chỉ bao phủ trạng thái không chạy và logic cờ/bảo vệ không phụ thuộc coordinator.
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("trạng thái idle nên cho phép vào đồng sáng tạo theo giai đoạn")
	}
	if !h.cocreating {
		t.Error("sau khi vào, cocreating phải là true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("vào ở trạng thái không chạy không được thay đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("sau khi hoàn thành toàn bộ truyện không nên cho phép vào đồng sáng tạo theo giai đoạn")
	}
	if h.cocreating {
		t.Error("sau khi từ chối không được đặt cờ cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("đang trong đồng sáng tạo phải từ chối vào lại")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("sau khi hủy, cocreating phải được xóa")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("hủy không được thay đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // không được panic, không được thay đổi trạng thái
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("CancelCoCreate khi không trong đồng sáng tạo phải là no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("bản nháp rỗng phải báo lỗi")
	}
	if !h.cocreating {
		t.Error("bản nháp rỗng trả về trước khi xóa cờ, cocreating phải giữ true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## 后续走向\n- 进入第二卷")
	if err == nil || !strings.Contains(err.Error(), "not in co-create") {
		t.Fatalf("khi không trong đồng sáng tạo phải báo not in co-create, nhận %v", err)
	}
}

func TestGuardExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		wantErr    string // rỗng = mong muốn cho qua
	}{
		{"running", lifecycleRunning, false, "đang chạy"},
		{"cocreating", lifecyclePaused, true, "đồng sáng tác"},
		{"idle free", lifecycleIdle, false, ""},
		{"paused free", lifecyclePaused, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			err := h.guardExclusive("导入")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("nên cho qua, nhận %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("phải chứa %q, nhận %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "导入") {
				t.Errorf("thông báo lỗi phải có action %q, nhận %v", "导入", err)
			}
		})
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries kiểm tra tính độc quyền của tất cả các điểm vào trong cửa sổ đồng sáng tạo:
// import/start/resume/continue đều phải bị từ chối trong khi cocreating, bù đắp khoảng trống chỉ kiểm tra ==running khi ở trạng thái paused.
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("vào đồng sáng tạo theo giai đoạn thất bại")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("ImportFrom trong cửa sổ đồng sáng tạo phải bị từ chối")
	}
	if err := h.StartPrepared("写个新故事"); err == nil {
		t.Error("StartPrepared trong cửa sổ đồng sáng tạo phải bị từ chối")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("Resume trong cửa sổ đồng sáng tạo phải bị từ chối")
	}
	if err := h.Continue("继续写"); err == nil {
		t.Error("Continue trong cửa sổ đồng sáng tạo phải bị từ chối")
	}

	// sau khi thoát đồng sáng tạo, khóa chiếm dụng được giải phóng (đây đi qua Cancel; đường inject Resume cần coordinator, để xác minh tích hợp)
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("sau khi thoát, cờ chiếm dụng phải được xóa")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store phải trả về chuỗi rỗng, nhận %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("影之诗", 100); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "主角登临绝巅",
		OpenThreads:     []string{"师门血仇未报"},
		EstimatedScale:  "预计 4-6 卷",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"影之诗", "Đã hoàn thành 3 chương", "chương tiếp theo là chương 4", "主角登临绝巅", "师门血仇未报", "预计 4-6 卷"} {
		if !strings.Contains(got, want) {
			t.Errorf("tóm tắt phải chứa %q, thực tế:\n%s", want, got)
		}
	}
}

// TestBuildStoryStateSummary_LatestChapterTail kiểm tra khi đã có chương hoàn thành trong store,
// summary phải chứa mục "đọc trang cuối" với đuôi văn bản không vượt quá ~800 rune.
func TestBuildStoryStateSummary_LatestChapterTail(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("影之诗", 100); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2}
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	// Chương 1 không phải chương hoàn thành gần nhất — không được dùng làm nguồn đuôi văn bản.
	if err := st.Drafts.SaveFinalChapter(1, "第一章的内容，不应该被使用。"); err != nil {
		t.Fatal(err)
	}
	// Dựng nội dung chương 2 đủ dài (vượt 800 rune) để kiểm tra việc cắt đuôi.
	para := strings.Repeat("这是结尾前的段落文字用于填充篇幅。", 40) // ~600 rune mỗi đoạn lặp lại
	lastPara := "这是最后一段，讲述主角决定连夜出发前往边境，去查明失踪的师兄下落，心中充满忧虑与决心。"
	content := para + "\n\n" + para + "\n\n" + lastPara
	if err := st.Drafts.SaveFinalChapter(2, content); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	if !strings.Contains(got, "## Đoạn kết chương gần nhất") {
		t.Fatalf("summary phải chứa tiêu đề mục đuôi văn bản, thực tế:\n%s", got)
	}
	if !strings.Contains(got, lastPara) {
		t.Fatalf("đuôi văn bản phải chứa đoạn kết thúc thật của chương gần nhất, thực tế:\n%s", got)
	}
	if strings.Contains(got, "不应该被使用") {
		t.Fatalf("không được lấy nội dung từ chương không phải chương hoàn thành gần nhất, thực tế:\n%s", got)
	}

	idx := strings.Index(got, "## Đoạn kết chương gần nhất")
	tailSection := strings.TrimSpace(got[idx+len("## Đoạn kết chương gần nhất"):])
	if n := len([]rune(tailSection)); n > 800 {
		t.Errorf("đuôi văn bản phải ≤ ~800 rune, thực tế %d rune", n)
	}
}

// TestBuildStoryStateSummary_NoCompletedChapter kiểm tra khi chưa có chương nào hoàn thành,
// summary không được thêm mục đuôi văn bản và không panic.
func TestBuildStoryStateSummary_NoCompletedChapter(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("空白之书", 100); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	if strings.Contains(got, "Đoạn kết chương gần nhất") {
		t.Errorf("chưa có chương hoàn thành thì không được thêm mục đuôi văn bản, thực tế:\n%s", got)
	}
}
