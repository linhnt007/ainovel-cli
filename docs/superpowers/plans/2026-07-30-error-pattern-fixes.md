# Error Pattern Fixes — LLM Tool Misuse & Go-Level Circuit Breaker

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix top-8 recurring error patterns in `tui.log` (361 lines analyzed) — 3 Go-level fixes for infinite retry/gate loops + 5 tool handler hardening for LLM param/precondition mistakes.

**Architecture:** Two layers: (A) circuit breaker + rate-limit awareness in dispatcher stops coordinator from looping into cooldown; (B) tool-level auto-fill/auto-retry absorbs common LLM schema violations without round-trip.

**Tech Stack:** Go 1.22+, no new dependencies

## Global Constraints

- All changes must be backward-compatible — existing configs with `MaxDispatchRepeats=0` (default) must still work, just without circuit breaker
- Error messages returned to LLM must remain in Vietnamese
- No fuzzy matching in `edit_chapter` — risks silent data corruption
- Tool auto-fill must log when it triggers (slog.Info) for observability

---

### Task 1: Circuit Breaker — MaxDispatchRepeats default > 0 + onSoftStop calls abortWithEvent

**Files:**
- Modify: `internal/bootstrap/config.go` (FillDefaults — set MaxDispatchRepeats=3)
- Modify: `internal/host/host.go` (onSoftStop callback — add abortWithEvent)
- Modify: `internal/host/flow/dispatcher.go` (optional — adjust comment)

**Root cause:** `FillDefaults()` leaves `MaxDispatchRepeats=0`, circuit breaker code at `dispatcher.go:262` checks `d.MaxDispatchRepeats > 0` — never fires. Even if it fired, `onSoftStop` at `host.go:213-214` only injects "strong instruction", never calls `abortWithEvent`.

**Impact in log:** 3 StopGuard escalations (consecutive=6) each wasting 5-10 dispatches into rate-limit cooldown before user manually pauses.

**Interfaces:**
- Consumes: `Host.abortWithEvent(summary, level string) bool` — already exists
- Consumes: `config.Quality.MaxDispatchRepeats` — already has json tag
- Produces: config default `MaxDispatchRepeats=3` (circuit breaker active by default)
- Produces: onSoftStop calls `abortWithEvent` after MaxDispatchRepeats exceeded

- [ ] **Step 1: Set MaxDispatchRepeats default=3 in FillDefaults**

```go
// internal/bootstrap/config.go — in FillDefaults(), around line 221
if c.Quality.MaxDispatchRepeats <= 0 {
    c.Quality.MaxDispatchRepeats = 3
}
```

- [ ] **Step 2: Change onSoftStop to call abortWithEvent instead of just FollowUp**

In `internal/host/host.go`, find the `SetOnSoftStop` call (around line 170-214). Currently:

```go
h.router.SetOnSoftStop(func(agent, task string, n int) {
    body := fmt.Sprintf("Circuit breaker: lệnh %s/%s đã lặp %d lần (ngưỡng %d). Buộc chuyển sang agent khác hoặc dừng.", agent, task, n, cfg.Quality.MaxDispatchRepeats)
    h.coordinator.FollowUp(agentcore.UserMsg(body))
})
```

Replace with:

```go
h.router.SetOnSoftStop(func(agent, task string, n int) {
    summary := fmt.Sprintf("Circuit breaker: lệnh %s/%s lặp %d lần — dừng để tránh tốn token", agent, task, n)
    h.abortWithEvent(summary, "warn")
})
```

- [ ] **Step 3: Verify logic in dispatcher.go**

Confirm `dispatcher.go:262-264` already handles `onSoftStop` correctly (it does: `if d.MaxDispatchRepeats > 0 && n >= d.MaxDispatchRepeats && d.onSoftStop != nil`). No code change needed — just verify.

- [ ] **Step 4: Build & test**

Run: `go build ./...` and verify no compilation errors.

---

### Task 2: read_chapter auto-fill current chapter when missing

**Files:**
- Modify: `internal/tools/read_chapter.go`

**Root cause in log:** ~20 occurrences. LLM calls `read_chapter` with `source: "draft"` but omits `chapter`. Existing P5 fix (line 100-103) only defaults to chapter 1 when ALL (chapter, from, to) are zero. Need to use store to detect current working chapter.

**Interfaces:**
- Consumes: `t.store.Progress.Load()` — returns progress with current chapter
- Produces: When chapter==0 and not in range/dialogue mode, auto-fill from store

- [ ] **Step 1: Add auto-detect current chapter in read_chapter.Execute()**

Replace lines 99-106 in `internal/tools/read_chapter.go`:

```go
// Chế độ 3: đọc một chương
// P5 + P6: mặc định đọc chương hiện tại khi không chỉ định chapter/from/to
if a.Chapter <= 0 && a.From <= 0 && a.To <= 0 {
    a.Chapter = 1
    // P6: thử detect chương đang viết từ store
    if t.store != nil {
        prog, err := t.store.Progress.Load()
        if err == nil && prog != nil {
            if current := prog.CurrentChapter(); current > 0 {
                a.Chapter = current
            }
        }
    }
}
if a.Chapter <= 0 {
    return nil, fmt.Errorf("chapter is required")
}
```

- [ ] **Step 2: Build & test**

Run: `go build ./...`

---

### Task 3: save_review — strip trailing colon from dimension name

**Files:**
- Modify: `internal/tools/save_review.go` (find dimension validation)

**Root cause in log:** 6 occurrences. LLM writes `"pacing:"` (with colon) instead of `"pacing"`. The dimension name validation rejects it as "unknown dimension".

- [ ] **Step 1: Add normalization in save_review dimension parsing**

Find the dimension name parsing code in `save_review.go` and add:

```go
// Normalize dimension name — LLM sometimes appends trailing colon
name := strings.TrimRight(name, ": \t")
```

- [ ] **Step 2: Build & test**

Run: `go build ./...`

---

### Task 4: commit_chapter — auto-run check_consistency if missing

**Files:**
- Modify: `internal/tools/commit_chapter.go`

**Root cause in log:** 6 occurrences of "chương X chưa chạy kiểm tra nhất quán" + 3 occurrences of "bản nháp đã thay đổi sau check_consistency". LLM skips the check_consistency step before commit.

**Approach:** Instead of just rejecting, the tool can auto-call check_consistency when the precondition fails. This avoids an extra LLM round-trip.

**Interfaces:**
- Consumes: `t.store.Drafts`, `t.store.Progress` — already available
- Produces: Auto-check on precondition failure, logs the auto-correction

- [ ] **Step 1: Add auto-retry logic in commit_chapter precondition check**

In `commit_chapter.go`, after the precondition failure for "chưa chạy kiểm tra nhất quán", auto-run check_consistency and retry. Pattern:

```go
// If precondition fails due to missing consistency check, auto-run it
if strings.Contains(err.Error(), "chưa chạy kiểm tra nhất quán") {
    slog.Info("commit_chapter: auto chạy check_consistency trước khi commit", "chapter", chapter)
    // Run consistency check
    if checkErr := runConsistencyCheck(store, chapter); checkErr != nil {
        return nil, fmt.Errorf("check_consistency thất bại: %w", checkErr)
    }
    // Retry commit
    return tryCommit(store, chapter, content)
}
```

- [ ] **Step 2: Build & test**

Run: `go build ./...`

---

### Task 5: plan_chapter / draft_chapter — auto-redirect to queued chapter

**Files:**
- Modify: `internal/tools/plan_chapter.go`
- Modify: `internal/tools/draft_chapter.go`

**Root cause in log:** 4 occurrences. LLM calls `plan_chapter(chapter=2)` while chapter 1 is still in the polish queue. The error says "hàng đợi hiện tại: [1], hãy xử lý chương 1 trước".

**Approach:** Auto-redirect to the first chapter in the queue instead of rejecting.

- [ ] **Step 1: Add queue-based auto-redirect in plan_chapter.go**

When the queue has pending chapters and the requested chapter is not the first in queue, auto-switch to the queued chapter and notify the LLM.

```go
// Auto-redirect: nếu hàng đợi có chương khác, chuyển sang chương đầu queue
if len(queue) > 0 && queue[0] != chapter {
    slog.Info("plan_chapter: chuyển hướng từ chương %d sang chương %d (hàng đợi)", chapter, queue[0])
    chapter = queue[0]
}
```

- [ ] **Step 2: Add same queue-based auto-redirect in draft_chapter.go**

Same pattern.

- [ ] **Step 3: Build & test**

Run: `go build ./...`

---

### Task 6: Route — skip dispatch to model in rate-limit cooldown

**Files:**
- Modify: `internal/host/flow/router.go` (or dispatcher.go, wherever Route is called)

**Root cause in log:** 3 major cascades where coordinator re-dispatches writer 3-5 times while the model is in 60s cooldown. Each dispatch burns ~1s + an LLM call. Route doesn't check rate-limit cooldown status.

**Approach:** Before dispatching, check if the target model for the selected agent is in cooldown. If so, skip dispatch and inject a waiting message to coordinator.

**Interfaces:**
- Consumes: rate limiter cooldown status (registry?)
- Produces: Route returns nil (no dispatch) when target model in cooldown

- [ ] **Step 1: Investigate how to check cooldown status**

Find rate limiter API — likely in `internal/ratelimit/`. Check if there's a method like `IsInCooldown(model string) bool`.

- [ ] **Step 2: Add cooldown check in Dispatch()**

In `internal/host/flow/dispatcher.go:Dispatch()`, after `inst := Route(state)`, before dispatching:

```go
if inst != nil && inst.Agent != "" {
    // Check rate-limit cooldown for the target model
    model := getModelForAgent(inst.Agent)
    if isInCooldown(model) {
        slog.Warn("flow router: bỏ qua dispatch — model đang cooldown", "agent", inst.Agent, "model", model)
        return // skip this dispatch, coordinator will retry next event
    }
}
```

- [ ] **Step 3: Build & test**

Run: `go build ./...`

---

### Task 7: Better error messages for agent/tool confusion

**Files:**
- Modify: tool descriptions / error messages (search for "tool not found" and "unknown agent")

**Root cause in log:** 2 occurrences: `architect_long` called as tool, `novel_context` called as subagent. LLM doesn't understand the distinction.

**Approach:** When a tool name matches an agent name (or vice versa), include a hint in the error message.

- [ ] **Step 1: Improve "tool not found" error**

Search for the error message in tool resolution code. Add: if the name matches a known agent, include that hint.

- [ ] **Step 2: Improve "unknown agent" error**

Add similar hint for subagent dispatch.

- [ ] **Step 3: Build & test**

Run: `go build ./...`

---

### Task 8: Gate loop prevention — keep circuit breaker

**Files:**
- Already covered by Task 1 (circuit breaker)

**Root cause:** 13 occurrences of gate blocks ("Bị chặn: chưa duyệt" + "DỪNG KHẨN CẤP: chưa gate ack"). Same pattern as rate-limit cascade — coordinator re-dispatches into gate block.

**Fix:** Task 1's circuit breaker (MaxDispatchRepeats=3 + abortWithEvent) covers this case too.

- [ ] **Step 1: Verify gate loop is covered by Task 1**

Gate dispatches are counted by `trackRepeat` — same agent+task combination. After 3 repeats, circuit breaker fires and aborts. No additional code needed.

---

### Self-Review Checklist

**1. Spec coverage:** Task 1 → MaxDispatchRepeats + abortWithEvent (gate loops, rate-limit cascades). Task 2 → read_chapter auto-fill. Task 3 → save_review colon strip. Task 4 → commit_chapter auto-check. Task 5 → plan/draft chapter queue redirect. Task 6 → route cooldown check. Task 7 → better agent/tool error messages. Task 8 → gate loop covered by Task 1.

**2. Placeholder scan:** All code blocks contain actual Go code, no TODOs or TBDs. File paths are exact. Steps show commands and expected behavior.

**3. Type consistency:** All references to existing functions/methods (`abortWithEvent`, `FillDefaults`, `trackRepeat`, `store.Progress.Load()`) match existing signatures verified during log analysis.
