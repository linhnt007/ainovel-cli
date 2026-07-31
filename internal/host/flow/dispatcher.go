package flow

import (
	"context"
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

	enabled    atomic.Bool  // do Host kiểm soát có phát lệnh hay không (nên tắt trước khi khởi động xong)
	gateActive atomic.Int32 // Fix 2A: chapter đang gate, 0 = không active. Dùng atomic vì Dispatch() có thể gọi từ goroutine khác.

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

	// onSoftStop là callback khi circuit breaker kích hoạt (lặp lại >= MaxDispatchRepeats).
	// Nhận vào agent, task, repeat count. Host dùng callback này để inject strong instruction hoặc abort.
	onSoftStop func(agent, task string, n int)

	// Tần suất dừng chờ người dùng duyệt (human gate).
	HumanGateEvery int
	// Callback khi dừng tại mốc duyệt người dùng (human gate).
	onHumanGate func(chapter int)

	// QualityReviewInterval: khoảng cách kiểm duyệt toàn cục từ cấu hình (mỗi N chương). Mặc định 5.
	QualityReviewInterval int

	// MaxDispatchRepeats: ngưỡng mềm cho circuit breaker. 0 = disable (chỉ notify).
	MaxDispatchRepeats int

	// SteeringTimeout: số lần dispatch tối đa khi Flow=Steering trước khi auto-exit. 0 = disable.
	SteeringTimeout int

	// onSteeringTimeout là callback khi steering vượt quá số lần cho phép.
	onSteeringTimeout func(steerCount int)

	// onDegraded là callback khi dòng chảy nạp trạng thái bị suy giảm (có cảnh báo/lỗi IO)
	onDegraded func(warnings []string)

	// cooldownChecker kiểm tra xem model của agent có đang cooldown (rate-limit 429) không.
	// Trả về (inCooldown, model). nil = không kiểm tra (bỏ qua).
	cooldownChecker func(agent string) (inCooldown bool, model string)

	// steeringCount đếm số lần dispatch liên tiếp khi Flow=Steering.
	steeringCount int

	// lastGateChapter ghi nhớ chương cuối cùng đã gửi thông báo human gate,
	// đảm bảo chỉ gửi FollowUp 1 lần duy nhất cho mỗi mốc gate, tránh vòng lặp
	// coordinator → LLM tốn token → gate chặn → dispatch lại → ∞.
	lastGateChapter int
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

// SetOnSoftStop đăng ký callback khi circuit breaker kích hoạt (lặp >= MaxDispatchRepeats).
func (d *Dispatcher) SetOnSoftStop(cb func(agent, task string, n int)) {
	d.onSoftStop = cb
}

// SetOnSteeringTimeout đăng ký callback khi steering vượt quá số lần cho phép.
func (d *Dispatcher) SetOnSteeringTimeout(cb func(steerCount int)) {
	d.onSteeringTimeout = cb
}

// SetCooldownChecker đăng ký callback kiểm tra cooldown rate-limit của model theo agent.
func (d *Dispatcher) SetCooldownChecker(cb func(agent string) (inCooldown bool, model string)) {
	d.cooldownChecker = cb
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
	// Fix 2B: gate active → ignore subagent/reopen_book tool events
	if d.gateActive.Load() > 0 && (ev.Tool == "subagent" || ev.Tool == "reopen_book") {
		if ev.Type == agentcore.EventToolExecEnd {
			slog.Debug("dispatcher: gate active, ignoring event",
				"tool", ev.Tool, "gate_chapter", d.gateActive.Load())
			return
		}
	}
	// Cả tool thành công lẫn tool bị gate chặn (IsError=true) đều cần route lại:
	// - Thành công → tiến trình đã thay đổi, cần quyết định bước tiếp.
	// - Gate chặn (vd: chưa review mà đòi gọi writer) → coordinator đang lạc hướng,
	//   Host phải gửi FollowUp đúng để đưa về luồng, không được bỏ mặc coordinator tự xoay.
	if ev.Type != agentcore.EventToolExecEnd {
		return
	}
	if ev.Tool != "subagent" && ev.Tool != "reopen_book" {
		return
	}
	d.Dispatch()
}

// Dispatch tính toán tuyến đường ngay lập tức và gửi lệnh; Host có thể chủ động gọi vào thời điểm đặc biệt (ví dụ sau Resume).
func (d *Dispatcher) Dispatch() {
	state := LoadState(d.store, d.QualityReviewInterval, d.HumanGateEvery)
	if len(state.LoadWarnings) > 0 && d.onDegraded != nil {
		d.onDegraded(state.LoadWarnings)
	}

	// Fix 2D: auto-clear gateActive nếu gate đã ack.
	if d.gateActive.Load() > 0 {
		if state.HumanGateEvery <= 0 || d.store.World.HasHumanGateAck(int(d.gateActive.Load())) {
			d.gateActive.Store(0)
		}
	}

	// Fix 1F: auto-clear frozen marker nếu gate đã được ack (survive restart).
	if frozenCh := d.store.Progress.HumanGateFrozenChapter(); frozenCh > 0 {
		if d.store.World.HasHumanGateAck(frozenCh) {
			if err := d.store.Progress.ClearHumanGateFreeze(); err != nil {
				slog.Warn("failed to clear stale freeze on resume", "err", err)
			}
		}
	}

	inst := Route(state)
	if inst == nil {
		return
	}

	// Steering timeout: đếm số lần dispatch liên tiếp khi Flow=Steering.
	// Khi vượt ngưỡng, gọi onSteeringTimeout để Host auto-exit steering.
	if state.Progress != nil && state.Progress.Flow == "steering" {
		d.steeringCount++
		if d.SteeringTimeout > 0 && d.steeringCount >= d.SteeringTimeout && d.onSteeringTimeout != nil {
			slog.Warn("Steering timeout: auto-exit steering flow", "module", "host.flow", "steer_count", d.steeringCount, "threshold", d.SteeringTimeout)
			d.onSteeringTimeout(d.steeringCount)
			d.steeringCount = 0 // reset sau khi trigger
		}
	} else {
		d.steeringCount = 0 // reset khi không phải steering
	}

	if inst.Agent == "" && state.HumanGatePending {
		slog.Info("Mốc duyệt người dùng: không gọi subagent, thông báo người dùng và chờ", "module", "host.flow", "chapter", state.LastCompleted)

		// Fix 2C: set gateActive khi trigger gate.
		d.gateActive.Store(int32(state.LastCompleted))

		// Fix 1D: ghi frozen marker.
		if err := d.store.Progress.SetHumanGateFreeze(state.LastCompleted); err != nil {
			slog.Warn("failed to set human gate freeze", "err", err)
		}

		if d.onHumanGate != nil {
			d.onHumanGate(state.LastCompleted)
		}
		// AbortSilent: dừng coordinator ngay không qua LLM.
		// User đã thấy notification qua onHumanGate callback, không cần tốn RQ.
		// lastGateChapter guard vẫn tồn tại để tránh gọi lặp AbortSilent.
		if d.lastGateChapter != state.LastCompleted {
			d.lastGateChapter = state.LastCompleted
			d.coordinator.AbortSilent()
		}
		return
	}

	// Task 6: Kiểm tra cooldown trước khi dispatch
	if d.cooldownChecker != nil && inst.Agent != "" {
		if inCooldown, model := d.cooldownChecker(inst.Agent); inCooldown {
			slog.Warn("flow router: model đang cooldown, báo coordinator chờ", "module", "host.flow", "agent", inst.Agent, "model", model)
			// Báo coordinator end_turn để tránh loop: nếu im lặng return, coordinator
			// end_turn → stop_guard chặn → novel_context → dispatch lại → cooldown lại → loop.
			d.coordinator.FollowUp(agentcore.UserMsg(
				fmt.Sprintf("[Host] Model %s đang bị rate-limit, hãy end_turn để chờ hồi phục.", model)))
			return
		}
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
	// Khởi động lại Coordinator nếu đang idle (vd: sau human gate ack).
	// Continue no-op nếu Coordinator đang chạy (ErrAlreadyRunning) → an toàn.
	_ = d.coordinator.Continue(context.Background())
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
// Khi số lần >= MaxDispatchRepeats (và MaxDispatchRepeats > 0), kích hoạt onSoftStop — circuit breaker mềm.
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
	// Circuit breaker mềm: khi lặp >= ngưỡng, inject strong instruction buộc Coordinator chuyển hướng.
	if d.MaxDispatchRepeats > 0 && n >= d.MaxDispatchRepeats && d.onSoftStop != nil {
		d.onSoftStop(next.Agent, next.Task, n)
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
	d.gateActive.Store(0) // Fix 2E: reset gateActive
}
