package startup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/host"
)

// CoCreateSession lưu trữ trạng thái phi UI cho chế độ đồng sáng tác.
type CoCreateSession struct {
	history        []host.CoCreateMessage
	draftPrompt    string
	ready          bool
	streamReply    string
	streamThinking string
	suggestions    []string
}

func NewCoCreateSession(initial string) *CoCreateSession {
	return &CoCreateSession{
		history: []host.CoCreateMessage{
			{Role: "user", Content: strings.TrimSpace(initial)},
		},
	}
}

func (s *CoCreateSession) History() []host.CoCreateMessage {
	if s == nil {
		return nil
	}
	return append([]host.CoCreateMessage(nil), s.history...)
}

func (s *CoCreateSession) ApplyReply(reply host.CoCreateReply) {
	if s == nil {
		return
	}
	s.streamReply = ""
	s.streamThinking = ""
	// Phía assistant chỉ lưu <reply> + (nếu có) khối <draft> của LƯỢT NÀY, KHÔNG lưu Raw tích lũy nữa.
	// Lý do: Raw còn kèm <ready>/<suggestions> (nhiễu, vô nghĩa cho vòng sau) và draft trong Raw là bản đầy đủ
	// tích lũy → lặp trong mọi lượt cũ, phình history O(n·draft). Mục đích gốc "để model thấy lại draft" nay do
	// cơ chế ghim draft hiện hành vào system prompt ở host (buildCoCreateMessages) đảm nhiệm; khối <draft> giữ ở
	// đây chỉ để model thấy diễn tiến trong cửa sổ K lượt gần nhất. TUI vẫn cắt đúng phần trước <draft> để hiển thị.
	message := strings.TrimSpace(reply.Message)
	if message == "" {
		// Đường dự phòng parse (không tuân thủ giao thức): Message có thể rỗng, Raw giữ nguyên cả đoạn.
		message = strings.TrimSpace(reply.Raw)
	}
	// Chỉ ghi đè draft khi Prompt không rỗng: đường dự phòng parse sẽ trả về Prompt="",
	// lúc đó phải giữ nguyên draft vòng trước, nếu không "chỉ thị sáng tác hiện tại" mà
	// người dùng đã tích lũy sẽ bị xóa bởi phản hồi bị cắt đứt.
	prompt := strings.TrimSpace(reply.Prompt)
	content := message
	if prompt != "" {
		if content != "" {
			content += "\n\n"
		}
		content += "<draft>\n" + prompt + "\n</draft>"
	}
	if content != "" {
		s.history = append(s.history, host.CoCreateMessage{Role: "assistant", Content: content})
	}
	if prompt != "" {
		s.draftPrompt = prompt
	}
	s.ready = reply.Ready
	// suggestions ghi đè trực tiếp (kể cả ghi đè thành rỗng): gợi ý mỗi vòng chỉ có nghĩa cho thời điểm hiện tại.
	s.suggestions = append(s.suggestions[:0], reply.Suggestions...)
}

func (s *CoCreateSession) AppendUser(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// Người dùng đã quyết định câu tiếp theo muốn nói, suggestions lập tức vô hiệu,
	// tránh gợi ý cũ vẫn còn treo trên ô nhập khi AI chưa kịp phản hồi gây nhầm lẫn.
	s.suggestions = nil
	s.history = append(s.history, host.CoCreateMessage{Role: "user", Content: text})
}

// ApplyDelta nhận tích lũy luồng streaming; kind="thinking" ghi vào luồng suy luận, "reply" ghi vào xem trước phản hồi.
// Hai luồng tích lũy riêng biệt, TUI có thể tô màu theo từng khối, cho người dùng thấy LLM đang hoạt động ngay cả trong giai đoạn thinking.
func (s *CoCreateSession) ApplyDelta(kind, text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	switch kind {
	case host.CoCreateProgressThinking:
		s.streamThinking = text
	case host.CoCreateProgressReply:
		s.streamReply = text
	}
}

func (s *CoCreateSession) StreamReply() string {
	if s == nil {
		return ""
	}
	return s.streamReply
}

func (s *CoCreateSession) StreamThinking() string {
	if s == nil {
		return ""
	}
	return s.streamThinking
}

func (s *CoCreateSession) DraftPrompt() string {
	if s == nil {
		return ""
	}
	return s.draftPrompt
}

func (s *CoCreateSession) Suggestions() []string {
	if s == nil {
		return nil
	}
	return s.suggestions
}

func (s *CoCreateSession) Ready() bool {
	if s == nil {
		return false
	}
	return s.ready
}

func (s *CoCreateSession) CanStart() bool {
	return strings.TrimSpace(s.DraftPrompt()) != ""
}

func (s *CoCreateSession) InitialInput() string {
	if s == nil || len(s.history) == 0 {
		return ""
	}
	return strings.TrimSpace(s.history[0].Content)
}

// cocreateSessionData là ảnh chụp JSON của phiên đồng sáng tác để persist qua Esc/quit.
// Chỉ gồm trạng thái tích lũy có ý nghĩa khôi phục (hội thoại + draft + ready + suggestions);
// hai luồng streaming (streamReply/streamThinking) là tạm thời trong lúc chờ LLM nên không lưu.
type cocreateSessionData struct {
	History     []host.CoCreateMessage `json:"history"`
	DraftPrompt string                 `json:"draft_prompt"`
	Ready       bool                   `json:"ready"`
	Suggestions []string               `json:"suggestions"`
}

// Save ghi phiên ra file JSON (best-effort: caller nuốt lỗi để không làm hỏng flow chính).
// Tạo thư mục cha nếu chưa có để không phụ thuộc thứ tự khởi tạo store.
func (s *CoCreateSession) Save(path string) error {
	if s == nil {
		return nil
	}
	data := cocreateSessionData{
		History:     s.history,
		DraftPrompt: s.draftPrompt,
		Ready:       s.ready,
		Suggestions: s.suggestions,
	}
	buf, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cocreate session: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir cocreate session: %w", err)
	}
	return os.WriteFile(path, buf, 0o644)
}

// LoadCoCreateSession đọc phiên đã persist. Trả về lỗi khi file không tồn tại / JSON hỏng để caller
// quyết định (thường: bỏ qua, mở phiên mới). Các trường streaming khôi phục về rỗng (đúng ngữ nghĩa).
func LoadCoCreateSession(path string) (*CoCreateSession, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data cocreateSessionData
	if err := json.Unmarshal(buf, &data); err != nil {
		return nil, fmt.Errorf("unmarshal cocreate session: %w", err)
	}
	return &CoCreateSession{
		history:     data.History,
		draftPrompt: data.DraftPrompt,
		ready:       data.Ready,
		suggestions: data.Suggestions,
	}, nil
}

func (s *CoCreateSession) BuildPlan() (Plan, error) {
	if s == nil || !s.CanStart() {
		return Plan{}, fmt.Errorf("cocreate draft prompt is required")
	}
	return Plan{
		Mode:        ModeCoCreate,
		DisplayName: "Kế hoạch đồng sáng tác",
		StartPrompt: host.BuildStartPrompt(s.DraftPrompt()),
	}, nil
}
