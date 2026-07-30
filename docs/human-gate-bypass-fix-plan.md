# Human Gate Bypass — Kế hoạch sửa 3 lớp

## Vấn đề

Sau human gate trigger (chapter 2), coordinator vẫn dispatch writer cho chapter 3.
Ba lớp bảo vệ đều fail:

1. **Gate (`qualityControlGate`)** không chặn — dùng `cfg.Quality.HumanGateEvery` snapshot lúc build, không đồng bộ với dispatcher
2. **Dispatcher** gửi FollowUp "không dispatch" nhưng vẫn bị EventToolExecEnd trigger → dispatch chapter 3
3. **StopGuard** chỉ block `end_turn`, không block tool call — coordinator vẫn gọi `subagent` tool

## Fix 1 — Cờ đóng băng persistent (lớp gate)

### Mục tiêu

Gate không phụ thuộc config snapshot. Dispatcher ghi frozen marker vào progress store khi trigger human gate. Gate đọc marker này.

---

### 1A. `internal/domain/progress.go` — Thêm field

```go
type Progress struct {
    // ... existing fields ...
    HumanGateFreeze int `json:"human_gate_freeze,omitempty"` // chapter number bị frozen, 0 = không frozen
}
```

### 1B. `internal/store/progress.go` — Thêm 4 methods

```go
// SetHumanGateFreeze ghi chapter vào frozen marker.
// Gọi từ Dispatcher trong WithWriteLock context (hoặc tự acquire lock).
func (s *ProgressStore) SetHumanGateFreeze(chapter int) error {
    return s.io.WithWriteLock(func() error {
        p, err := s.loadUnlocked()
        if err != nil { return err }
        if p == nil { p = &domain.Progress{} }
        p.HumanGateFreeze = chapter
        return s.saveUnlocked(p)
    })
}

// ClearHumanGateFreeze xóa frozen marker (khi user ack gate).
func (s *ProgressStore) ClearHumanGateFreeze() error {
    return s.io.WithWriteLock(func() error {
        p, err := s.loadUnlocked()
        if err != nil { return err }
        if p == nil { return nil }
        p.HumanGateFreeze = 0
        return s.saveUnlocked(p)
    })
}

// IsHumanGateFrozen kiểm tra frozen marker cho chapter cụ thể.
// chapter==0 → kiểm tra bất kỳ frozen nào.
func (s *ProgressStore) IsHumanGateFrozen(chapter int) bool {
    p, err := s.Load()
    if err != nil || p == nil { return false }
    if chapter > 0 { return p.HumanGateFreeze == chapter }
    return p.HumanGateFreeze > 0
}

// HumanGateFrozenChapter trả về chapter đang frozen, 0 = không frozen.
func (s *ProgressStore) HumanGateFrozenChapter() int {
    p, err := s.Load()
    if err != nil || p == nil { return 0 }
    return p.HumanGateFreeze
}
```

### 1C. `internal/agents/build.go` — Gate đọc frozen marker

File: `internal/agents/build.go`

Vị trí: function `qualityControlGate` (~dòng 338-403), block human gate check.

**Thay đổi:**

Hiện tại gate dùng:
```go
state.HumanGateEvery = cfg.Quality.HumanGateEvery
```

Thay bằng đọc từ store:
```go
// Đọc frozen marker từ store (luôn đồng bộ, survive restart)
if st.Progress.IsHumanGateFrozen(0) {
    frozenCh := st.Progress.HumanGateFrozenChapter()
    reason := fmt.Sprintf(
        "Mốc duyệt người dùng đang chờ duyệt cho chương %d. Hãy yêu cầu người dùng duyệt qua lệnh /gate ok trước khi tiếp tục.",
        frozenCh,
    )
    return &agentcore.GateDecision{Allowed: false, Reason: reason}, nil
}
```

Giữ nguyên check cũ (dựa `cfg.Quality.HumanGateEvery`) làm lưới thứ 2:
```go
// Check cũ dùng config — belt-and-suspenders
if cfg.Quality.HumanGateEvery > 0 && state.LastCompleted > 0 &&
    state.LastCompleted%cfg.Quality.HumanGateEvery == 0 {
    // vẫn giữ nguyên, chỉ thêm frozen marker check ở trên
}
```

### 1D. `internal/host/flow/dispatcher.go` — Ghi freeze khi trigger gate

File: `internal/host/flow/dispatcher.go`

Vị trí: function `Dispatch()`, trong block xử lý human gate (khoảng dòng 161-173).

**Thay đổi:**

Sau khi xác định human gate pending, trước khi gửi FollowUp:

```go
if inst.Agent == "" && state.HumanGatePending {
    slog.Info("human gate triggered, freezing progress",
        "chapter", state.LastCompleted)

    // NEW: ghi frozen marker
    if err := d.store.Progress.SetHumanGateFreeze(state.LastCompleted); err != nil {
        slog.Warn("failed to set human gate freeze", "err", err)
    }

    // ... existing code: gọi onHumanGate callback, gửi FollowUp ...
}
```

### 1E. `internal/host/host.go` — Xóa freeze khi ack gate

Vị trí: handler cho lệnh `/gate ok` (hoặc tương đương — nơi gọi `SaveHumanGateAck`).

**Thay đổi:**

Sau khi gọi `SaveHumanGateAck`, thêm:
```go
// Xóa frozen marker
if err := h.store.Progress.ClearHumanGateFreeze(); err != nil {
    slog.Warn("failed to clear human gate freeze", "err", err)
}
```

### 1F. `internal/host/flow/dispatcher.go` — Resume tự clear stale freeze

Trong `Dispatch()`, đầu hàm, thêm auto-cleanup:

```go
func (d *Dispatcher) Dispatch() {
    // ... load state ...

    // NEW: auto-clear frozen marker nếu gate đã được ack (survive restart)
    if frozenCh := d.store.Progress.HumanGateFrozenChapter(); frozenCh > 0 {
        if d.store.World.HasHumanGateAck(frozenCh) {
            if err := d.store.Progress.ClearHumanGateFreeze(); err != nil {
                slog.Warn("failed to clear stale freeze on resume", "err", err)
            }
        }
    }

    // ... tiếp tục ...
}
```

---

## Fix 2 — Dispatcher im lặng sau gate (lớp message)

### Mục tiêu

Khi gate active, Dispatcher bỏ qua mọi `EventToolExecEnd` từ subagent/reopen_book — không gửi FollowUp gây nhiễu cho LLM.

### 2A. `internal/host/flow/dispatcher.go` — Thêm field `gateActive`

Cấu trúc `Dispatcher`:
```go
type Dispatcher struct {
    enabled atomic.Bool
    gateActive atomic.Int32 // NEW: chapter đang gate, 0 = không active
    // ... existing fields ...
}
```

Dùng `atomic.Int32` vì `Dispatch()` có thể gọi từ goroutine khác.

### 2B. `internal/host/flow/dispatcher.go` — Handle() early return

```go
func (d *Dispatcher) handle(ev agentcore.Event) {
    if !d.enabled.Load() { return }

    // NEW: gate active → ignore subagent/reopen_book tool events
    if d.gateActive.Load() > 0 && (ev.Tool == "subagent" || ev.Tool == "reopen_book") {
        if ev.Type == agentcore.EventToolExecEnd {
            slog.Debug("dispatcher: gate active, ignoring event",
                "tool", ev.Tool, "gate_chapter", d.gateActive.Load())
            return
        }
    }

    if ev.Type != agentcore.EventToolExecEnd { return }
    if ev.Tool != "subagent" && ev.Tool != "reopen_book" { return }
    d.Dispatch()
}
```

### 2C. `internal/host/flow/dispatcher.go` — Set gateActive khi trigger gate

Trong `Dispatch()`, block human gate:

```go
if inst.Agent == "" && state.HumanGatePending {
    d.gateActive.Store(int32(state.LastCompleted)) // NEW

    if err := d.store.Progress.SetHumanGateFreeze(state.LastCompleted); err != nil {
        slog.Warn("failed to set human gate freeze", "err", err)
    }
    // ... existing code ...
}
```

### 2D. `internal/host/flow/dispatcher.go` — Auto-clear gateActive trong Dispatch()

Đầu `Dispatch()`, thêm:

```go
func (d *Dispatcher) Dispatch() {
    state := LoadState(d.store, d.QualityReviewInterval)

    // NEW: auto-clear nếu gate đã ack
    if d.gateActive.Load() > 0 {
        if state.HumanGateEvery <= 0 {
            d.gateActive.Store(0)
        } else if d.store.World.HasHumanGateAck(int(d.gateActive.Load())) {
            d.gateActive.Store(0)
        }
    }

    // ... continue ...
}
```

### 2E. `internal/host/flow/dispatcher.go` — Reset trong ResetRepeat()

```go
func (d *Dispatcher) ResetRepeat() {
    d.lastMu.Lock()
    defer d.lastMu.Unlock()
    d.lastSent = nil
    d.repeats = 0
    d.gateActive.Store(0) // NEW
}
```

---

## Fix 3 — Đồng bộ HumanGateEvery qua Store (lớp config)

### Mục tiêu

Eliminate config snapshot mismatch. `HumanGateEvery` lưu trong store. Cả gate và dispatcher đọc từ store.

### 3A. `internal/domain/progress.go` — Thêm field (nếu chưa có)

```go
type Progress struct {
    // ... existing, +HumanGateFreeze từ Fix 1 ...
    HumanGateEvery int `json:"human_gate_every,omitempty"` // NEW
}
```

### 3B. `internal/store/progress.go` — Thêm setter + getter

```go
// SetHumanGateEvery lưu giá trị vào progress. Gọi 1 lần khi config loaded.
func (s *ProgressStore) SetHumanGateEvery(every int) error {
    return s.io.WithWriteLock(func() error {
        p, err := s.loadUnlocked()
        if err != nil { return err }
        if p == nil { p = &domain.Progress{} }
        p.HumanGateEvery = every
        return s.saveUnlocked(p)
    })
}

// HumanGateEvery trả về giá trị đã lưu, 0 = tắt.
func (s *ProgressStore) HumanGateEvery() int {
    p, err := s.Load()
    if err != nil || p == nil { return 0 }
    return p.HumanGateEvery
}
```

### 3C. `internal/host/host.go` — Lưu config → store khi init

Sau khi load config, thêm:

```go
if err := h.store.Progress.SetHumanGateEvery(cfg.Quality.HumanGateEvery); err != nil {
    slog.Warn("failed to save human_gate_every to store", "err", err)
}
```

Chọn vị trí: trong `NewHost()` hoặc `Run()`, tại nơi `cfg.Quality.HumanGateEvery` đã có giá trị.

### 3D. `internal/agents/build.go` — Gate đọc từ store

Trong `qualityControlGate`, thay:

```go
// Trước (dùng cfg snapshot):
// state.HumanGateEvery = cfg.Quality.HumanGateEvery

// Sau (đọc từ store):
progress, _ := st.Progress.Load()
if progress != nil {
    state.HumanGateEvery = progress.HumanGateEvery
} else {
    state.HumanGateEvery = 0
}
```

Đảm bảo `state` được gán trước khi check freeze marker.

### 3E. `internal/host/flow/dispatcher.go` — Dispatcher đọc từ store

Trong `LoadState()` (file `internal/host/flow/state.go`), thay đổi cách lấy `HumanGateEvery`:

Hiện tại dispatcher dùng `d.HumanGateEvery` (field set từ config). Thay bằng đọc từ store:

```go
// Trong LoadState hoặc Dispatch():
state.HumanGateEvery = d.store.Progress.HumanGateEvery()
```

Giữ field `d.HumanGateEvery` cho backward-compat, nhưng ghi đè bằng store value trong Dispatch().

---

## Dependency Graph

```
Fix 1 ──────────────────────────────────────────────┐
  ├── domain/progress.go: +HumanGateFreeze           │
  ├── store/progress.go: SetHumanGateFreeze,         │
  │   ClearHumanGateFreeze, IsHumanGateFrozen,       │
  │   HumanGateFrozenChapter                         │
  ├── agents/build.go: check frozen marker           │
  ├── host/flow/dispatcher.go: ghi freeze on trigger ├── song song, độc lập
  └── host/host.go: clear freeze on ack              │
                                                     │
Fix 2 ──────────────────────────────────────────────┘
  ├── host/flow/dispatcher.go: +gateActive field
  ├── host/flow/dispatcher.go: early return handle()
  ├── host/flow/dispatcher.go: set gateActive on trigger
  └── host/flow/dispatcher.go: clear on Dispatch + ResetRepeat

Fix 3 ───────────────────── (kế thừa Fix 1 files)
  ├── domain/progress.go: +HumanGateEvery
  ├── store/progress.go: SetHumanGateEvery, HumanGateEvery
  ├── host/host.go: save config → store
  ├── agents/build.go: gate đọc từ store
  └── host/flow/state.go: dispatcher đọc từ store
```

## Thứ tự implement

1. **Fix 1** (cả 6 file) — gốc rễ: gate thực sự chặn
2. **Fix 2** (1 file, 4 vị trí) — bổ sung: dispatcher không gây nhiễu
3. **Fix 3** (5 file) — nền tảng: đồng bộ config, chống tái phát

Mỗi Fix có thể test độc lập. Fix 1 + Fix 2 không overlap file (Fix 1 sửa store/gate/host, Fix 2 sửa dispatcher). Fix 3 chạm lại domain/store nhưng chỉ thêm field không conflict.

## Kiểm thử

### Unit test — gate

- `HumanGateFreeze` set → `qualityControlGate` trả về `Allowed: false`
- `HumanGateFreeze` clear → gate pass-through bình thường
- `HumanGateFreeze` ≠ chapter hiện tại → vẫn chặn (an toàn)

### Unit test — dispatcher

- `gateActive > 0` + `EventToolExecEnd` (subagent) → `handle()` return early
- `gateActive > 0` + event khác → vẫn xử lý bình thường
- `gateActive` auto-clear sau ack

### Integration test

- Full flow: gateway trigger → freeze set → coordinator attempt dispatch → gate block → user `/gate ok` → freeze clear → coordinator dispatch thành công
- Resume crash: freeze marker còn → gate block (không bypass)
- Resume sau ack: freeze marker tự clear (1F)
- `HumanGateEvery=0` → freeze không bao giờ set
