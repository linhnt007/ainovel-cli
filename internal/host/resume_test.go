package host

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// TestBuildResumePrompt_EmptyWorkspace: workspace trống (progress.json không tồn tại)
// → label rỗng, không tự động Resume → TUI giữ màn hình start.
func TestBuildResumePrompt_EmptyWorkspace(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)

	prompt, label, err := buildResumePrompt(st)
	if err != nil {
		t.Fatalf("buildResumePrompt: %v", err)
	}
	if label != "" {
		t.Fatalf("workspace trống phải trả label rỗng, got %q (prompt=%q)", label, prompt)
	}
}

// TestBuildResumePrompt_PhantomEmptyProgress: file progress.json rỗng (phase="", do
// helper vô tình tạo khi workspace mới) phải được coi là chưa bắt đầu → không Resume.
func TestBuildResumePrompt_PhantomEmptyProgress(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	// Tái hiện file trống mà SetHumanGateEvery cũ tạo ra khi progress chưa tồn tại.
	if err := st.Progress.Save(&domain.Progress{HumanGateEvery: 2}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	prompt, label, err := buildResumePrompt(st)
	if err != nil {
		t.Fatalf("buildResumePrompt: %v", err)
	}
	if label != "" {
		t.Fatalf("progress phase rỗng phải trả label rỗng, got %q (prompt=%q)", label, prompt)
	}
}

// TestBuildResumePrompt_PhaseInit: PhaseInit (khởi tạo xong nhưng chưa bắt đầu sáng tác)
// → không có gì để khôi phục → không Resume.
func TestBuildResumePrompt_PhaseInit(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Progress.Init("test", 10); err != nil {
		t.Fatalf("Init: %v", err)
	}

	_, label, err := buildResumePrompt(st)
	if err != nil {
		t.Fatalf("buildResumePrompt: %v", err)
	}
	if label != "" {
		t.Fatalf("PhaseInit phải trả label rỗng, got %q", label)
	}
}

// TestBuildResumePrompt_WritingResumes: sách đang viết vẫn Resume bình thường.
func TestBuildResumePrompt_WritingResumes(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Progress.Save(&domain.Progress{
		NovelName:      "test",
		Phase:          domain.PhaseWriting,
		CurrentChapter: 1,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	prompt, label, err := buildResumePrompt(st)
	if err != nil {
		t.Fatalf("buildResumePrompt: %v", err)
	}
	if label == "" {
		t.Fatal("PhaseWriting phải trả label khôi phục")
	}
	if prompt == "" {
		t.Fatal("PhaseWriting phải trả prompt khôi phục")
	}
}
