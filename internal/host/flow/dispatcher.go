package flow

import (
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/voocel/agentcore"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Dispatcher đăng ký lắng nghe sự kiện từ Coordinator, tính toán tuyến đường khi agent phụ trả về và gửi lệnh cho Host.
//
// Vòng đời: Attach trả về một hàm detach; gọi hàm đó khi đóng Host để giải phóng đăng ký.
type Dispatcher struct {
	coordinator *agentcore.Agent
	store       *storepkg.Store

	enabled atomic.Bool // do Host kiểm soát có phát lệnh hay không (nên tắt trước khi khởi động xong)

	// Theo dõi lặp: ghi nhớ Agent+Task đã phát lần gần nhất và số lần phát liên tiếp.
	// Cùng một lệnh tính lại (agent phụ trả về nhưng trạng thái chưa tiến, Route tính lại ra cùng kết quả) không bị nuốt yên lặng,
	// mà được phát lại kèm số lần thực tế — "kết quả route liên tiếp N lần giống nhau" là sự thật chỉ Host quan sát được;
	// nếu im lặng, Coordinator sẽ rơi vào mâu thuẫn giữa "cấm tự quyết bước tiếp theo" (coordinator.md) và
	// "cấm dừng máy" (StopGuard), tự do hành động sẽ dẫn đến vòng lặp freelance kiểu #24.
	// Quyền phán quyết vẫn thuộc LLM: tin phát lại chỉ kèm sự thật và cho phép kiểm tra, không đặt ngưỡng, không ngắt mạch (kiến trúc §10.13).
	// Vì tin có kèm số lần nên mỗi lần khác nhau, không bị ép lệnh giống hệt vào followUpQ nhiều lần.
	lastMu   sync.Mutex
	lastSent *Instruction
	repeats  int

	// onRepeat là callback telemetry thuần túy (dùng cho cảnh báo chế độ không giao diện), kích hoạt một lần
	// khi cùng một lệnh được phát đến lần thứ repeatNotifyAt; không ảnh hưởng ngược lại logic phát lệnh, logic phát không hay biết về sự tồn tại của nó.
	onRepeat func(agent, task string, n int)

	// Tần suất dừng chờ người dùng duyệt (human gate).
	HumanGateEvery int
	// Callback khi dừng tại mốc duyệt người dùng (human gate).
	onHumanGate func(chapter int)

	// QualityReviewInterval: khoảng cách kiểm duyệt toàn cục từ cấu hình (mỗi N chương). Mặc định 5.
	QualityReviewInterval int

	// onDegraded là callback khi dòng chảy nạp trạng thái bị suy giảm (có cảnh báo/lỗi IO)
	onDegraded func(warnings []string)
}

// repeatNotifyAt cố định không đưa vào cấu hình: đây không phải ngưỡng luồng điều khiển (không kích hoạt hành động nào, chỉ là "gọi người"),
// điều chỉnh không mang lại lợi ích; đưa vào cấu hình lại ngầm ám chỉ có thể chỉnh ra hành vi khác.
const repeatNotifyAt = 3

// NewDispatcher tạo Dispatcher. Cần gọi Attach để đăng ký sự kiện trước khi sử dụng.
func NewDispatcher(coordinator *agentcore.Agent, store *storepkg.Store) *Dispatcher {
	d := &Dispatcher{coordinator: coordinator, store: store}
	return d
}

// SetOnHumanGate đăng ký callback cho mốc duyệt người dùng.
func (d *Dispatcher) SetOnHumanGate(cb func(chapter int)) {
	d.onHumanGate = cb
}

// SetOnDegraded đăng ký callback cảnh báo suy giảm dòng chảy.
func (d *Dispatcher) SetOnDegraded(cb func(warnings []string)) {
	d.onDegraded = cb
}

// SetOnRepeat đăng ký callback telemetry cho lệnh lặp. Phải gọi một lần trước khi Attach/bắt đầu phát lệnh.
func (d *Dispatcher) SetOnRepeat(cb func(agent, task string, n int)) {
	d.onRepeat = cb
}

// Enable bật phát lệnh theo tuyến đường; khi tắt, EventToolExecEnd đến sẽ không gửi FollowUp.
// Host bật sau khi hoàn thành prompt đầu tiên trong Start/Resume, tránh xung đột với luồng khởi động.
func (d *Dispatcher) Enable() { d.enabled.Store(true) }

// Attach đăng ký lắng nghe sự kiện Coordinator; hàm trả về dùng để hủy đăng ký khi đóng.
func (d *Dispatcher) Attach() func() {
	return d.coordinator.Subscribe(d.handle)
}

func (d *Dispatcher) handle(ev agentcore.Event) {
	if !d.enabled.Load() {
		return
	}
	// Điểm kích hoạt chính xác: agent phụ trả về thành công, hoặc reopen_book mở lại sách đã hoàn thành vào trạng thái làm lại.
	// Cả hai đều đã tiến lớp thực tế, cần ngay một lần tính Route để xác định bước tiếp theo — reopen_book không phải subagent
	// (pha complete cần vượt qua completePhaseGate), nếu không kích hoạt ở đây, hàng đợi làm lại sau khi mở lại sẽ không có dispatcher.
	// Không dùng EventModelResponse vì agentcore emit nó mỗi lần LLM call hoàn thành,
	// sẽ ép cùng một lệnh vào followUpQ nhiều lần; Steer kiểu truy vấn bị coordinator.md ràng buộc tiếp tục gọi subagent
	// trong cùng một turn, từ đó chạm điểm kích hoạt này.
	if ev.Type != agentcore.EventToolExecEnd || ev.IsError {
		return
	}
	if ev.Tool != "subagent" && ev.Tool != "reopen_book" {
		return
	}
	d.Dispatch()
}

// Dispatch tính toán tuyến đường ngay lập tức và gửi lệnh; Host có thể chủ động gọi vào thời điểm đặc biệt (ví dụ sau Resume).
func (d *Dispatcher) Dispatch() {
	state := LoadState(d.store)
	if len(state.LoadWarnings) > 0 && d.onDegraded != nil {
		d.onDegraded(state.LoadWarnings)
	}
	state.HumanGateEvery = d.HumanGateEvery
	state.QualityReviewInterval = d.QualityReviewInterval
	if state.LastCompleted > 0 && state.HumanGateEvery > 0 && state.LastCompleted%state.HumanGateEvery == 0 && !d.store.World.HasHumanGateAck(state.LastCompleted) {
		state.HumanGatePending = true
	}

	inst := Route(state)
	if inst == nil {
		return
	}

	if inst.Agent == "" && state.HumanGatePending {
		slog.Info("Mốc duyệt người dùng: không gọi subagent, thông báo người dùng và chờ", "module", "host.flow", "chapter", state.LastCompleted)
		if d.onHumanGate != nil {
			d.onHumanGate(state.LastCompleted)
		}
		d.coordinator.FollowUp(agentcore.UserMsg(fmt.Sprintf("[Host] Mốc duyệt người dùng: dừng tiến trình sáng tác tự động để chờ người dùng kiểm tra và duyệt chương %d.", state.LastCompleted)))
		return
	}

	n := d.trackRepeat(inst)
	// Tác vụ Người viết: đánh dấu chương là đang tiến hành ngay lúc phát lệnh, đề cương bên phải TUI phản ánh ngay "▸ đang tiến hành",
	// không cần chờ plan_chapter thực sự thực thi (plan_chapter sẽ gọi StartChapter lần nữa, idempotent).
	if inst.Agent == "writer" && inst.Chapter > 0 && d.store != nil {
		if err := d.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			slog.Error("flow router refuses invalid writer dispatch", "module", "host.flow", "chapter", inst.Chapter, "err", err)
			return
		}
		if err := d.store.Progress.StartChapter(inst.Chapter); err != nil {
			slog.Warn("flow router pre-mark in-progress failed", "module", "host.flow", "chapter", inst.Chapter, "err", err)
		}
	}
	msg := formatDispatchMessage(inst, n)
	slog.Debug("flow router dispatch", "module", "host.flow", "agent", inst.Agent, "reason", inst.Reason, "repeat", n)
	d.coordinator.FollowUp(agentcore.UserMsg(msg))
}

// formatDispatchMessage tạo nội dung lệnh gửi đến Coordinator.
// Khi n>1, kèm thêm sự thật về việc lặp — thông báo "sau lần phát trước, sự thật route chưa thay đổi" và mở quyền kiểm tra,
// để LLM tự phán quyết tiếp tục thực hiện hay chuyển sang agent phụ khác; Host không áp đặt bất kỳ nhánh bắt buộc nào.
func formatDispatchMessage(inst *Instruction, n int) string {
	msg := FormatMessage(inst)
	if n > 1 {
		msg += fmt.Sprintf("\n（Lưu ý: Đây là lần thứ %d lệnh này được phát — sau lần phát trước, sự thật route chưa thay đổi. Lần này được phép gọi novel_context kiểm tra sự thật trước, rồi phán quyết tiếp tục thực hiện hoặc chuyển sang agent phụ khác.）", n)
	}
	return msg
}

// trackRepeat ghi lại số lần phát liên tiếp cùng một lệnh và trả về số lần hiện tại (1 = lệnh mới).
// Dùng đẳng thức Agent+Task (không so Reason vì Reason là văn bản phụ trợ cho người đọc).
// Khi số lần đúng bằng repeatNotifyAt, kích hoạt onRepeat một lần ngoài lock (sau khi khóa thay đổi thì đặt lại bộ đếm).
func (d *Dispatcher) trackRepeat(next *Instruction) int {
	d.lastMu.Lock()
	if d.lastSent != nil && d.lastSent.Agent == next.Agent && d.lastSent.Task == next.Task {
		d.repeats++
	} else {
		cp := *next
		d.lastSent = &cp
		d.repeats = 1
	}
	n := d.repeats
	d.lastMu.Unlock()

	if n == repeatNotifyAt && d.onRepeat != nil {
		d.onRepeat(next.Agent, next.Task, n)
	}
	return n
}

// ResetRepeat xóa theo dõi lặp. Host gọi khi Resume / Start mới,
// đảm bảo lệnh đầu tiên sau khi khôi phục hoặc tạo mới được phát với ngữ nghĩa "lần thứ 1".
func (d *Dispatcher) ResetRepeat() {
	d.lastMu.Lock()
	defer d.lastMu.Unlock()
	d.lastSent = nil
	d.repeats = 0
}
