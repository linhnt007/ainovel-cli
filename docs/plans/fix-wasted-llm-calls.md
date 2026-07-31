# Plan: Loại bỏ LLM call lãng phí trong coordinator flow

## Vấn đề

Coordinator bị kẹt trong vòng lặp tốn LLM call (~3-4 RQ mỗi cycle) khi:
1. Human gate đang chờ
2. Model bị rate-limit (429)
3. Subagent fail liên tiếp

## Các pattern đã phát hiện

### Pattern A — Human gate gửi FollowUp (tốn 1 RQ vô ích)

**File:** `internal/host/flow/dispatcher.go` (dòng 216-219)
**Cơ chế:** Dispatcher gửi `coordinator.FollowUp(UserMsg(...))` khi gate trigger → LLM phải xử lý message (1 RQ) → novel_context → end_turn.
**Tác nhân:** stop_guard cũ block end_turn, fix hiện tại đã allow, nhưng vẫn tốn 1 RQ FollowUp.
**Fix:** Thay `FollowUp` bằng `coordinator.AbortSilent()` — dừng coordinator ngay, không qua LLM.
**Rủi ro:** Cần kiểm tra lifecycle transition (waitDone set lifecycleIdle → Resume được).
**RQ tiết kiệm:** 1 RQ mỗi lần gate.
**Mức ưu tiên:** CAO

### Pattern B — CooldownChecker silent skip → coordinator loop

**File:** `internal/host/flow/dispatcher.go` (dòng 224-228)
**Cơ chế:** Khi model bị rate-limit, `cooldownChecker` trả về `inCooldown=true`, dispatcher `return` — ko gửi gì cho coordinator. Coordinator ko nhận được instruction mới → end_turn → stop_guard block → novel_context → dispatcher re-route → cooldownChecker lại skip → loop đến khi hết cooldown.
**Tác nhân:** `return` im lặng, ko thông báo coordinator.
**Fix:** Inject message báo coordinator "đang cooldown, end_turn chờ".
**RQ tiết kiệm:** 3-5 RQ mỗi lần cooldown (tùy thời gian cooldown).
**Mức ưu tiên:** CAO

### Pattern C — Repeat dispatch chờ đến ngưỡng mới circuit breaker

**File:** `internal/host/flow/dispatcher.go` (dòng 263-283)
**Cơ chế:** `trackRepeat` đếm số lần dispatch lặp, chỉ fire `onSoftStop` khi đạt `MaxDispatchRepeats`. Mỗi repeat trước ngưỡng tốn 3-4 RQ (coordinator → subagent → fail → dispatch lại).
**Tác nhân:** Luôn chờ đủ N lần lặp mới kích hoạt circuit breaker.
**Fix:** Giảm `MaxDispatchRepeats` mặc định, hoặc thêm shortcut: nếu N lần lặp gần nhất đều do rate-limit → trigger sớm.
**RQ tiết kiệm:** (MaxDispatchRepeats - 1) × 3-4 RQ mỗi lần.
**Mức ưu tiên:** TRUNG BÌNH

### Pattern D — Subagent stop_guard inject message full RQ

**File:** `internal/host/reminder/subagent_guards.go` (dòng 63-72)
**Cơ chế:** Khi subagent (writer/editor) cố end_turn chưa hoàn thành, guard inject message → subagent tool-call cả vòng (~2-5 tool calls).
**Tác nhân:** Hành vi cố ý (subagent phải hoàn thành task), nhưng ko phân biệt "sắp hết context" vs "lười".
**Fix:** Thêm check context utilization: nếu context gần đầy, cho phép end_turn + escalate lên coordinator thay vì inject.
**RQ tiết kiệm:** 1-3 subagent RQ mỗi lần (ít gặp, context đầy hiếm).
**Mức ưu tiên:** THẤP

### Pattern E — Coordinator model failover trong loop

**Cơ chế:** Trong death loop, coordinator model cũng bị rate-limit → failover → tốn thêm 2 RQ.
**Tác nhân:** Symptom, ko phải cause. Fix các pattern A/B/C sẽ loại bỏ.
**RQ tiết kiệm:** 2 RQ mỗi lần (kéo theo từ A/B/C).
**Mức ưu tiên:** THẤP (tự động hết sau khi fix A/B/C)

## Kế hoạch thực hiện

### Bước 1: Pattern A — Thay FollowUp bằng AbortSilent (CAO)

**File:** `internal/host/flow/dispatcher.go`
**Thay đổi:**
```
- d.coordinator.FollowUp(agentcore.UserMsg(...))
+ d.coordinator.AbortSilent()
```
**Giải thích:** Khi gate trigger, coordinator ko cần biết lý do — user đã thấy notification qua `onHumanGate` callback. Stop coordinator sạch, ko tốn RQ.

**Lưu ý:** Set gateActive TRƯỚC khi AbortSilent để tránh race. Sửa event message trong `waitDone` nếu HumanGateFreeze > 0.

### Bước 2: Pattern B — CooldownChecker báo coordinator (CAO)

**File:** `internal/host/flow/dispatcher.go`
**Thay đổi:** Khi cooldownChecker báo inCooldown, gửi FollowUp thông báo:
```go
if inCooldown, model := d.cooldownChecker(inst.Agent); inCooldown {
    d.coordinator.FollowUp(agentcore.UserMsg(
        fmt.Sprintf("[Host] Model %s dang rate-limit, hay end_turn cho hoi phuc.", model)))
    return
}
```

**Cách 2 (dự phòng):** stop_guard check cooldown → allow end_turn (giống human gate).

### Bước 3: Pattern C — Giảm ngưỡng repeat (TRUNG BÌNH)

**File:** `internal/host/flow/dispatcher.go` hoặc config mặc định.
**Thay đổi:** Giảm `MaxDispatchRepeats` từ 10 xuống 5. Thêm logic: nếu N lần lặp đều rate-limit → trigger circuit breaker sớm.

### Bước 4: Pattern D — Context-aware subagent guard (THẤP)

**File:** `internal/host/reminder/subagent_guards.go`
**Thay đổi:** Trong `newCheckpointDeltaGuard`, thêm check context utilization >90% → allow end_turn + escalate.

## Rủi ro

1. **AbortSilent + waitDone event sai:** `waitDone` emit "Coordinator dừng" event cả khi dừng do gate. Cần sửa: nếu HumanGateFreeze > 0, emit "Dang cho duyet" thay vi "Dung".
2. **CooldownChecker FollowUp vẫn tốn 1 RQ:** Nhưng đây là RQ có chủ đích — báo coordinator chờ, ko phải RQ loop vô ích.
3. **AbortSilent khi dispatch chưa kịp set gateActive:** Set gateActive TRƯỚC khi AbortSilent.

## Kiểm tra

- Test unit: dispatcher, stop_guard, models.
- Test manual: gate trigger → ko thay "stop_guard block" log → coordinator dung sach.
- Test manual: rate-limit → coordinator nhan message cooldown → end_turn → het loop.
