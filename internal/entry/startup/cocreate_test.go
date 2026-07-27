package startup

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/host"
)

// rawFourTags dựng phản hồi 4 thẻ đầy đủ như model trả về, dùng để kiểm ApplyReply lọc đúng.
func rawFourTags(reply, draft string) string {
	return "<reply>\n" + reply + "\n</reply>\n\n<draft>\n" + draft + "\n</draft>\n\n<ready>false</ready>\n\n<suggestions>\n- gợi ý A\n- gợi ý B\n</suggestions>"
}

// TestApplyReply_StripsProtocolTags: ApplyReply với Raw 4 thẻ → history không chứa <ready>/<suggestions>,
// và draft cũ không bị lặp lại (mỗi lượt chỉ giữ draft của chính nó, không phải Raw tích lũy).
func TestApplyReply_StripsProtocolTags(t *testing.T) {
	s := NewCoCreateSession("ý tưởng ban đầu")

	s.ApplyReply(host.CoCreateReply{
		Message:     "chào bạn, cho tôi hỏi thể loại?",
		Prompt:      "## Chủ đề\n- Phiêu lưu",
		Ready:       false,
		Suggestions: []string{"gợi ý A", "gợi ý B"},
		Raw:         rawFourTags("chào bạn, cho tôi hỏi thể loại?", "## Chủ đề\n- Phiêu lưu"),
	})

	hist := s.History()
	if len(hist) != 2 {
		t.Fatalf("history phải có 2 tin (user + assistant), có %d", len(hist))
	}
	asst := hist[1]
	if asst.Role != "assistant" {
		t.Fatalf("tin thứ 2 phải là assistant, là %q", asst.Role)
	}
	if strings.Contains(asst.Content, "<ready>") || strings.Contains(asst.Content, "<suggestions>") {
		t.Errorf("history assistant KHÔNG được chứa <ready>/<suggestions>, có:\n%s", asst.Content)
	}
	if !strings.Contains(asst.Content, "<draft>") {
		t.Errorf("history assistant nên chứa khối <draft> của lượt này, có:\n%s", asst.Content)
	}
	if !strings.Contains(asst.Content, "chào bạn, cho tôi hỏi thể loại?") {
		t.Errorf("history assistant phải giữ phần reply, có:\n%s", asst.Content)
	}

	// Lượt 2: draft cập nhật. Draft cũ ("Phiêu lưu") không được lặp trong tin assistant mới.
	s.AppendUser("thêm yếu tố khoa học viễn tưởng")
	s.ApplyReply(host.CoCreateReply{
		Message: "rõ rồi",
		Prompt:  "## Chủ đề\n- Khoa học viễn tưởng",
		Ready:   true,
		Raw:     rawFourTags("rõ rồi", "## Chủ đề\n- Khoa học viễn tưởng"),
	})

	hist = s.History()
	last := hist[len(hist)-1]
	if strings.Contains(last.Content, "Phiêu lưu") {
		t.Errorf("draft cũ (Phiêu lưu) không được lặp trong tin assistant mới, có:\n%s", last.Content)
	}
	// draftPrompt của session là bản mới nhất.
	if s.DraftPrompt() != "## Chủ đề\n- Khoa học viễn tưởng" {
		t.Errorf("DraftPrompt phải là bản mới nhất, có %q", s.DraftPrompt())
	}
	// suggestions bị ghi đè thành rỗng ở lượt 2.
	if len(s.Suggestions()) != 0 {
		t.Errorf("Suggestions phải rỗng sau lượt 2, có %v", s.Suggestions())
	}
}

// TestApplyReply_FallbackNoDraft: khi Prompt rỗng (phản hồi không tuân thủ giao thức, bị cắt),
// draft vòng trước phải được giữ nguyên, và tin assistant không có khối <draft>.
func TestApplyReply_FallbackNoDraft(t *testing.T) {
	s := NewCoCreateSession("ý tưởng")
	s.ApplyReply(host.CoCreateReply{Message: "reply1", Prompt: "## Draft1", Raw: rawFourTags("reply1", "## Draft1")})
	s.AppendUser("tiếp")
	// Prompt rỗng → giữ draft cũ.
	s.ApplyReply(host.CoCreateReply{Message: "reply2 nguyên văn", Prompt: "", Raw: "reply2 nguyên văn"})

	if s.DraftPrompt() != "## Draft1" {
		t.Errorf("draft cũ phải giữ nguyên khi Prompt rỗng, có %q", s.DraftPrompt())
	}
	last := s.History()[len(s.History())-1]
	if strings.Contains(last.Content, "<draft>") {
		t.Errorf("tin assistant không nên có <draft> khi Prompt rỗng, có:\n%s", last.Content)
	}
	if !strings.Contains(last.Content, "reply2 nguyên văn") {
		t.Errorf("tin assistant phải giữ reply, có:\n%s", last.Content)
	}
}

// TestBuildCoCreateMessages_WindowAndPin: 12 lượt history → msgs = system + 8 lượt cuối;
// system prompt chứa draft hiện hành đúng 1 lần dưới mục "## Bản chỉ thị hiện hành".
func TestBuildCoCreateMessages_WindowAndPin(t *testing.T) {
	var hist []host.CoCreateMessage
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		hist = append(hist, host.CoCreateMessage{Role: role, Content: "noi dung luot " + string(rune('A'+i))})
	}

	draft := "## Chủ đề\n- Nội dung chỉ thị hiện hành duy nhất"
	msgs := host.BuildCoCreateMessages("SYSTEM_BASE", draft, hist)

	// system + 8 lượt cuối = 9 message.
	if len(msgs) != 9 {
		t.Fatalf("msgs phải = system + 8 = 9, có %d", len(msgs))
	}
	if msgs[0].Role != agentcore.RoleSystem {
		t.Fatalf("msg[0] phải là system, là %q", msgs[0].Role)
	}
	sys := msgs[0].TextContent()
	if !strings.Contains(sys, "## Bản chỉ thị hiện hành") {
		t.Errorf("system phải ghim mục '## Bản chỉ thị hiện hành', có:\n%s", sys)
	}
	if n := strings.Count(sys, draft); n != 1 {
		t.Errorf("draft hiện hành phải xuất hiện đúng 1 lần trong system, có %d lần", n)
	}
	// 8 lượt cuối là history[4..11]: message đầu tiên sau system phải khớp history[4].
	if got := msgs[1].TextContent(); got != hist[4].Content {
		t.Errorf("msg[1] phải là lượt cuối thứ 8 (history[4]=%q), có %q", hist[4].Content, got)
	}
	if got := msgs[8].TextContent(); got != hist[11].Content {
		t.Errorf("msg[8] phải là lượt cuối cùng (history[11]=%q), có %q", hist[11].Content, got)
	}
}

// TestBuildCoCreateMessages_OddHistoryUserFirst: history độ dài LẺ (13) — cửa sổ chẵn 8 cắt tại chỉ số lẻ = assistant.
// Phải đảm bảo message đầu sau system là USER (invariant provider Anthropic), cửa sổ co còn 7, tổng message = 8.
func TestBuildCoCreateMessages_OddHistoryUserFirst(t *testing.T) {
	var hist []host.CoCreateMessage
	for i := 0; i < 13; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		hist = append(hist, host.CoCreateMessage{Role: role, Content: "noi dung luot " + string(rune('A'+i))})
	}

	msgs := host.BuildCoCreateMessages("SYSTEM_BASE", "## draft", hist)

	// last 8 = index 5..12; index 5 là assistant → bị bỏ; còn index 6..12 (user đầu) = 7 message.
	if len(msgs) != 8 {
		t.Fatalf("msgs phải = system + 7 (bỏ assistant mở đầu cửa sổ) = 8, có %d", len(msgs))
	}
	if msgs[0].Role != agentcore.RoleSystem {
		t.Fatalf("msg[0] phải là system, là %q", msgs[0].Role)
	}
	if msgs[1].Role != agentcore.RoleUser {
		t.Errorf("message ĐẦU sau system PHẢI là user (invariant provider), là %q", msgs[1].Role)
	}
	if got := msgs[1].TextContent(); got != hist[6].Content {
		t.Errorf("msg[1] phải là history[6] (%q), có %q", hist[6].Content, got)
	}
	if got := msgs[7].TextContent(); got != hist[12].Content {
		t.Errorf("msg[7] phải là history[12] (%q), có %q", hist[12].Content, got)
	}
}

// TestBuildCoCreateMessages_NoDraftNoPin: draft rỗng → không thêm mục ghim, system giữ nguyên.
func TestBuildCoCreateMessages_NoDraftNoPin(t *testing.T) {
	msgs := host.BuildCoCreateMessages("SYSTEM_BASE", "  ", []host.CoCreateMessage{{Role: "user", Content: "xin chào"}})
	if len(msgs) != 2 {
		t.Fatalf("msgs phải = system + 1 = 2, có %d", len(msgs))
	}
	if strings.Contains(msgs[0].TextContent(), "## Bản chỉ thị hiện hành") {
		t.Errorf("draft rỗng thì không được ghim mục chỉ thị, có:\n%s", msgs[0].TextContent())
	}
}

// TestSaveLoadRoundTrip: Save → Load giữ đủ trường (history, draftPrompt, ready, suggestions).
func TestSaveLoadRoundTrip(t *testing.T) {
	s := NewCoCreateSession("ý tưởng gốc")
	s.ApplyReply(host.CoCreateReply{
		Message:     "hỏi thêm",
		Prompt:      "## Chủ đề\n- Trinh thám",
		Ready:       true,
		Suggestions: []string{"gợi ý 1", "gợi ý 2"},
		Raw:         rawFourTags("hỏi thêm", "## Chủ đề\n- Trinh thám"),
	})

	path := filepath.Join(t.TempDir(), "meta", "cocreate_session.json")
	if err := s.Save(path); err != nil {
		t.Fatalf("Save lỗi: %v", err)
	}

	loaded, err := LoadCoCreateSession(path)
	if err != nil {
		t.Fatalf("Load lỗi: %v", err)
	}

	if loaded.DraftPrompt() != s.DraftPrompt() {
		t.Errorf("draftPrompt không khớp: %q vs %q", loaded.DraftPrompt(), s.DraftPrompt())
	}
	if loaded.Ready() != s.Ready() {
		t.Errorf("ready không khớp: %v vs %v", loaded.Ready(), s.Ready())
	}
	if strings.Join(loaded.Suggestions(), "|") != strings.Join(s.Suggestions(), "|") {
		t.Errorf("suggestions không khớp: %v vs %v", loaded.Suggestions(), s.Suggestions())
	}
	lh, sh := loaded.History(), s.History()
	if len(lh) != len(sh) {
		t.Fatalf("history độ dài không khớp: %d vs %d", len(lh), len(sh))
	}
	for i := range sh {
		if lh[i].Role != sh[i].Role || lh[i].Content != sh[i].Content {
			t.Errorf("history[%d] không khớp: %+v vs %+v", i, lh[i], sh[i])
		}
	}
}

// TestLoadMissingFile: Load file không tồn tại → trả lỗi (caller bỏ qua, mở phiên mới).
func TestLoadMissingFile(t *testing.T) {
	if _, err := LoadCoCreateSession(filepath.Join(t.TempDir(), "khong-ton-tai.json")); err == nil {
		t.Errorf("Load file không tồn tại phải trả lỗi")
	}
}
