# Kế hoạch sửa lỗi Flow

## Mục lục

- [1. Xóa output rebuild vẫn tiếp tục cũ](#1-xóa-output-rebuild-vẫn-tiếp-tục-cũ)
- [2. /reset không start lại từ chapter 1](#2-reset-không-start-lại-từ-chapter-1)
- [3. Editor + reviewer rules bị bypass](#3-editor--reviewer-rules-bị-bypass)
- [4. qualityControlGate chưa được wire](#4-qualitycontrolgate-chưa-được-wire)

---

## 1. Xóa output rebuild vẫn tiếp tục cũ

### Root cause

`StartPrepared` (host.go:263) gọi `Progress.Init("", 0)` để khởi tạo progress mới. Nhưng user xóa thủ công thư mục output (chapters/, drafts/) trong khi `meta/progress.json` còn nguyên → khi dùng `/resume`, `buildResumePrompt` (resume.go:22) thấy progress với `CompletedChapters` cũ → `NextChapter() = LatestCompleted() + 1` → tiếp tục từ chương cũ.

Không có cơ chế phát hiện "output bị xóa nhưng meta còn" → silent corruption.

### Affected code

| File | Line | Vai trò |
|------|------|---------|
| `internal/host/host.go` | 241-285 | `StartPrepared`: chỉ Init progress, k check output dir |
| `internal/host/resume.go` | 22-65 | `buildResumePrompt`: dùng progress hiện tại, k verify consistency với disk |
| `internal/domain/runtime.go` | 82-84 | `NextChapter()`: `return LatestCompleted() + 1` — pure math, k IO |
| `internal/store/store.go` | 233-338 | `RollbackToChapter`: xóa chapter/draft/review nhưng k xóa progress nếu file k tồn tại |

### Fix plan

```go
// A. Thêm verify function: kiểm tra completed chapters có tồn tại trên disk không
func (s *Store) VerifyProgressConsistency() ([]int, error) {
    // Với mỗi chapter trong CompletedChapters, kiểm tra chapters/%02d.md tồn tại
    // Nếu >= 1 chapter completed k tồn tại → trả về list missing chapters
}

// B. Trong buildResumePrompt, gọi VerifyProgressConsistency
//    Nếu phát hiện inconsistency → auto-rollback hoặc cảnh báo

// C. Thêm command /rebuild: xóa toàn bộ + start fresh
```

---

## 2. /reset không start lại từ chapter 1

### Root cause

`ResetToChapter` (host.go:1200) gọi `h.Abort()` → `coordinator.Abort()`. `agentcore.Agent.Abort()` (agent.go:210-217) **không clear `a.messages`** (lịch sử hội thoại). Nó chỉ cancel context + emit abort marker.

Sau đó `/resume` gọi `coordinator.Prompt(ctx, prompt)` (host.go:321) → `startPromptRunLocked` (agent.go:314) **copy toàn bộ messages cũ** vào AgentContext mới → LLM thấy cả history cũ (chương 2-5 đã viết).

Hậu quả: LLM thấy mâu thuẫn giữa "đã hoàn thành chương 2" (history cũ) và "bắt đầu từ chương 1" (prompt mới) → thường chọn tin history → dispatch writer cho chapter 2.

### Proof

```go
// agentcore/agent.go:210
func (a *Agent) Abort() {
    a.wantAbortMarker.Store(true)
    if a.cancel != nil { a.cancel() }
    // KHÔNG clear a.messages
    // KHÔNG clear a.steeringQ, a.followUpQ
}

// agentcore/agent.go:314
func (a *Agent) startPromptRunLocked(ctx context.Context, msgs []AgentMessage) {
    agentCtx := AgentContext{
        Messages: copyMessages(a.messages),  // ← COPY HISTORY CŨ
    }
    // ... prompt msgs được APPEND vào
}

// agentcore/agent.go:597
func (a *Agent) Reset() {
    a.messages = nil  // ← CÓ clear nhưng KHÔNG BAO GIỜ được gọi
    a.steeringQ = nil
    a.followUpQ = nil
}
```

### Affected code

| File | Line | Vai trò |
|------|------|---------|
| `agentcore/agent.go` | 210-217 | `Abort()`: k clear messages |
| `agentcore/agent.go` | 304-309 | `ClearMessages()`: tồn tại nhưng k được gọi |
| `agentcore/agent.go` | 597-623 | `Reset()`: tồn tại nhưng k được gọi |
| `internal/host/host.go` | 400-421 | `Abort()`: wrapper, k clear messages |
| `internal/host/host.go` | 1198-1216 | `ResetToChapter`: gọi Abort + Rollback nhưng k clear coordinator history |
| `internal/host/host.go` | 288-332 | `Resume()`: gọi Prompt trên coordinator với history cũ |

### Fix plan

```go
// A. Trong ResetToChapter (host.go:1200), sau Abort gọi ClearMessages
func (h *Host) ResetToChapter(target int) error {
    h.Abort()
    h.coordinator.ClearMessages()  // ← THÊM: clear lịch sử hội thoại
    
    if err := h.store.RollbackToChapter(target); err != nil {
        return fmt.Errorf("quay lui dữ liệu: %w", err)
    }
    // ...
}

// B. HOẶC dùng Reset() thay vì Abort() khi lifecycle là running
// Reset() mạnh hơn: clear messages + queues + sync context engine
```

---

## 3. Editor + reviewer rules bị bypass

### Root cause

Router priority chain (router.go:82-215) **không enforce per-chapter review**:

```
 3. PendingRewrites          → writer
 3.5 NeedsRewriteReview      → editor (chỉ sau rewrite)
...
10. HasPendingFlatReview     → editor (batch N chương 1 lần)
10.5 HumanGatePending        → stop (chờ user)
11. Default                  → writer ← KHÔNG CHECK "chapter đã review chưa"
```

Giữa step 10 và 11: nếu `HasPendingFlatReview=false` (vì chưa đến mốc review interval), writer dispatch ngay mà editor chưa review chương vừa viết. `qualityControlGate` lẽ ra phải chặn writer khi `HasPendingFlatReview=true` nhưng **chưa được implement** (xem [issue 4](#4-qualitycontrolgate-chưa-được-wire)).

### Proof

```go
// internal/host/flow/router.go:204-214 — không có check per-chapter review
// 11. Tiếp tục viết bình thường
next := p.NextChapter()
if next <= 0 { return nil }
return &Instruction{
    Agent:   "writer",
    Task:    fmt.Sprintf("Viết chương %d", next),
    Reason:  "Tiếp tục viết chương tiếp theo",
    Chapter: next,
}
```

### Affected code

| File | Line | Vai trò |
|------|------|---------|
| `internal/host/flow/router.go` | 68-81 | Comment priority list: thiếu per-chapter review step |
| `internal/host/flow/router.go` | 204-214 | Step 11: writer dispatch k check editor state |
| `internal/host/flow/state.go` | 84-101 | `HasPendingFlatReview`: chỉ tính periodic, k phải per-chapter |
| `internal/store/store.go` | `World.HasChapterReview(chapter)` | Có method để check nhưng k được router dùng |

### Fix plan

```go
// A. Thêm step 10.75 trong router.go: check per-chapter review
// 10.75. Nếu chương vừa hoàn thành chưa được editor review → dispatch editor
if s.NeedsChapterReview > 0 {
    return &Instruction{
        Agent: "editor",
        Task:  fmt.Sprintf("Đánh giá chương %d", s.NeedsChapterReview),
    }
}

// B. LoadState tính NeedsChapterReview:
//    Nếu LastCompleted > 0 và chưa có review cho chapter đó
//    VÀ không phải arc boundary (đã xử lý ở step 5-9)
//    → NeedsChapterReview = LastCompleted

// C. Cấu hình: "per_chapter_review: bool" trong quality config
//    Mặc định true cho các dự án mới
```

---

## 4. qualityControlGate chưa được wire

### Root cause

Trong `internal/agents/gate_test.go:98-145` có tham chiếu đến `qualityControlGate` function:
```go
// gate_test.go:107
gate := qualityControlGate(st, cfg)
```

Nhưng function này **không tồn tại** trong codebase. `rg` toàn bộ project chỉ tìm thấy 5 reference, tất cả trong `gate_test.go`. Không có `internal/agents/gate.go` hay bất kỳ file nào implement nó.

Mục đích của `qualityControlGate` theo test:
- Khi `HasPendingFlatReview=true`:
  - Chặn `subagent("writer", ...)` → không allowed
  - Cho phép `subagent("editor", ...)` → allowed
- Ngược lại: cho qua tất cả

Đây là cơ chế **ToolGate** (agentcore level), chặn writer ngay tại lớp tool execution — mạnh hơn router dispatch vì router chỉ gửi instruction (coordinator có thể ignore).

### Proof: Missing function

```
$ rg "qualityControlGate" internal/
internal/agents/gate_test.go:107: gate := qualityControlGate(st, cfg)
internal/agents/gate_test.go:113: t.Fatal("expected qualityControlGate to block writer dispatch when HasPendingFlatReview")
internal/agents/gate_test.go:126: gate := qualityControlGate(st, cfg)
internal/agents/gate_test.go:134: t.Fatal("expected qualityControlGate to block writer when PendingFlatReview")
internal/agents/gate_test.go:143: t.Fatal("expected qualityControlGate to ALLOW editor when PendingFlatReview")

$ ls internal/agents/*.go
build.go  context_manager.go  gate_test.go  thinking_test.go
// Không có gate.go — qualityControlGate KHÔNG TỒN TẠI
```

### Affected code

| File | Line | Vai trò |
|------|------|---------|
| `internal/agents/gate_test.go` | 98-145 | Test tham chiếu hàm không tồn tại |
| `internal/agents/build.go` | 337 | Chỉ wire `completePhaseGate`, k có `qualityControlGate` |
| `internal/host/flow/state.go` | `HasPendingFlatReview` | Được tính nhưng k được dùng làm gate |

### Fix plan

```go
// A. Tạo internal/agents/gate.go với implementation qualityControlGate
func qualityControlGate(st *store.Store, cfg qualityConfig) agentcore.ToolGate {
    return func(ctx context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
        if req.Call.Name != "subagent" {
            return nil, nil
        }
        var args struct {
            Name string `json:"name"`
        }
        if err := json.Unmarshal(req.Call.Args, &args); err != nil {
            return nil, nil // fail-open
        }
        progress, err := st.Progress.Load()
        if err != nil || progress == nil {
            return nil, nil // fail-open
        }

        // Tính HasPendingFlatReview
        reviewInterval := domain.GetReviewInterval(cfg.ReviewInterval)
        lastCompleted := progress.LatestCompleted()
        needsReview := false
        if lastCompleted > 0 {
            if mark := (lastCompleted / reviewInterval) * reviewInterval; mark > 0 {
                last, _ := st.World.LoadLastReviewAny(lastCompleted)
                needsReview = last == nil || last.Chapter < mark
            }
        }

        if !needsReview {
            return nil, nil // không nợ review → cho qua
        }

        // Đang nợ review: chặn writer, cho editor qua
        if args.Name == "writer" {
            return &agentcore.GateDecision{
                Allowed: false,
                Reason:  "Còn nợ đánh giá định kỳ, hãy gọi editor trước",
            }, nil
        }
        return nil, nil // editor và architect được phép
    }
}

// B. Wire vào build.go cùng với completePhaseGate
agent := agentcore.NewAgent(
    // ... existing options
    agentcore.WithToolGate(combinedGate(st, cfg)),
)

// C. combinedGate chạy cả completePhaseGate lẫn qualityControlGate
func combinedGate(st *store.Store, cfg qualityConfig) agentcore.ToolGate {
    complete := completePhaseGate(st)
    quality := qualityControlGate(st, cfg)
    return func(ctx context.Context, req agentcore.GateRequest) (*agentcore.GateDecision, error) {
        // complete gate kiểm tra trước
        if d, err := complete(ctx, req); d != nil || err != nil {
            return d, err
        }
        // quality gate kiểm tra sau
        return quality(ctx, req)
    }
}
```

---

## Tổng quan thứ tự ưu tiên sửa

| Ưu tiên | Issue | Effort | Risk | Phụ thuộc |
|---------|-------|--------|------|-----------|
| P0 | #2: /reset nhảy chapter | 1 file, 1 dòng | Thấp | None |
| P0 | #4: qualityControlGate chưa wire | 2 files (~50 LOC) | Trung bình | Cần test kỹ |
| P1 | #3: Per-chapter review | 2 files (~30 LOC) | Trung bình | #4 trước (nếu dùng ToolGate) |
| P2 | #1: Xóa output rebuild | 2 files (~60 LOC) | Thấp | None |

## Risk assessment

### P0 #2: ClearMessages sau reset
- **Risk:** Thấp. `ClearMessages()` là method có sẵn trong agentcore. Chỉ gọi đúng 1 lần sau abort.
- **Side effect:** Mất lịch sử hội thoại cũ — đây chính là mục đích.
- **Test:** ResetToChapter(0) → Resume → kiểm tra coordinator chỉ thấy 1 turn.

### P0 #4: qualityControlGate implementation
- **Risk:** Trung bình. ToolGate chặn writer ở mức thấp nhất — nếu logic `HasPendingFlatReview` sai, writer có thể bị chặn oan hoặc k được chặn.
- **Mitigation:** Gate fail-open (lỗi → cho qua). Test kỹ edge cases.
- **Test:** gate_test.go đã có sẵn test cases (đang fail vì missing function). Chỉ cần implement + chạy.

### P1 #3: Per-chapter review step
- **Risk:** Trung bình. Thay đổi flow routing — có thể ảnh hưởng throughput.
- **Note:** Nên configurable (`per_chapter_review: bool`).
- **Alternative:** Dùng `qualityControlGate` mở rộng check cả per-chapter review thay vì chỉ periodic.

### P2 #1: VerifyProgressConsistency
- **Risk:** Thấp. Read-only check, chỉ thêm warning/auto-fix.
- **Note:** Nên chạy ở startup (Resume) để phát hiện sớm.
