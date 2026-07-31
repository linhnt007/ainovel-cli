# Step Flow System Analysis — ainovel-cli

> Tổng hợp kết quả điều tra toàn bộ hệ thống flow, trạng thái, router, agent, tool, checkpoint, resume, entry paths.
> Ngày: 2026-07-30

---

## 1. Tổng quan kiến trúc

```
[Entry: TUI / Headless]
         │ prompt / steer
[Host vỏ mỏng]  ~860 dòng
   ├── observer        sự kiện → chiếu UI/nhật ký
   ├── flow.Dispatcher đăng ký ToolExecEnd → LoadState → Route(state) → FollowUp
   └── usage / quản lý mô hình
         │
[Coordinator (LLM, MaxTurns=100_000)]
   ├── Khi khởi động phán quyết architect_short / long
   ├── Nhận [Host hạ lệnh] → tạo subagent tool_call
   └── Nhận [Người dùng can thiệp] → phán quyết tự chủ
         │
[architect / writer / editor SubAgent] (context + model độc lập)
         │ gọi công cụ
[Tools]  novel_context · read_chapter · plan_chapter · draft_chapter · edit_chapter
         check_consistency · commit_chapter · save_review · save_arc_summary
         save_volume_summary · save_foundation
         │ bộ ba nguyên tử (artifact + progress + checkpoint)
[Store: hệ thống file (tmp + rename)]
   Progress · Checkpoints · Outline · Drafts · Summaries · Characters · World · Signals
```

**Nguyên lý cốt lõi**: Flow Router là **pure function** (không I/O, không store), trả về `*Instruction` hoặc `nil` (để LLM tự quyết). Dispatcher gọi `LoadState → Route → formatDispatchMessage → coordinator.FollowUp`.

---

## 2. Phase (Vòng đời tiểu thuyết)

Định nghĩa trong `internal/domain/runtime.go`:

| Phase | String | Ý nghĩa |
|-------|--------|---------|
| `PhaseInit` | `"init"` | Khởi tạo ban đầu |
| `PhasePremise` | `"premise"` | Xây dựng premise/world-building |
| `PhaseOutline` | `"outline"` | Outline cốt truyện |
| `PhaseWriting` | `"writing"` | Đang viết chapter |
| `PhaseComplete` | `"complete"` | Hoàn thành toàn bộ tiểu thuyết |

### Transition Rules (`internal/domain/transitions.go`)

- Map `(Phase, Event)` → `Phase` mới
- Valid tool calls per phase: mỗi phase chỉ được gọi tool tương ứng
- **StopGuard**: chặn `end_turn` nếu `Phase != Complete` (tầng agentcore)
  - Chặn liên tiếp 5 lần → terminate

---

## 3. Flow Types (Loại flow đang chạy)

Định nghĩa trong `internal/domain/runtime.go`:

| Flow | Mô tả | Entry point |
|------|-------|-------------|
| `FlowQuick` | Quick start — tự động premise → outline → writing → complete | `startup.PrepareQuick()` → `rt.StartPrepared()` |
| `FlowCocreate` | Đồng sáng tác từng bước với người dùng | `startup.PrepareCocreate()` → `rt.StartPrepared()` |
| `FlowImport` | Import novel từ file ngoài (reverse analysis) | `startup.PrepareImport()` → `rt.StartPrepared()` |
| `FlowResume` | Resume từ checkpoint cũ | `rt.Resume()` |

---

## 4. Flow States (Con của FlowWriting)

Trong phase `PhaseWriting`, có các trạng thái con:

| State | Ý nghĩa | Được set bởi |
|-------|---------|-------------|
| `FlowWriting` | Đang viết chapter mới (plan → draft → commit) | Default |
| `FlowReviewing` | Đang review chapter | `save_review` |
| `FlowRewriting` | Đang rewrite chapter (pending reviews) | `save_review` gate → `SetPendingRewrites` |
| `FlowPolishing` | Đang polish chapter | `save_review` gate |
| `FlowSteering` | Người dùng đang can thiệp | User steer |

**Transition chain**: Writing → Reviewing → (Rewriting | Polishing) → Writing

---

## 5. Route() Priority Cascade (11 levels)

`internal/host/flow/router.go` — pure function:

| Priority | Điều kiện | Instruction |
|----------|-----------|-------------|
| 1 | **Interrupt check** — người dùng can thiệp đang chờ | `Interrupt` → xử lý steer |
| 2 | **Milestone gate** — gate kiểm tra chất lượng tại milestones | `MilestoneGate` |
| 3 | **Commit pending** — chapter đã viết nhưng chưa commit | `CommitChapter` |
| 4 | **Review pending** — review feedback chưa xử lý | `ReviewChapter` |
| 5 | **Phase transition** — chuyển phase (init→premise→outline→writing→complete) | Theo phase target |
| 6 | **Arc completion** — kết thúc story arc | `ExpandArc` / `SaveArcSummary` |
| 7 | **Chapter planning** — lập kế hoạch chapter tiếp theo | `PlanChapter` |
| 8 | **Chapter writing** — viết chapter mới | `WriteChapter` |
| 9 | **Chapter revision** — sửa chapter (pending rewrites) | `RewriteChapter` |
| 10 | **Content check** — consistency check, word count, quality | `CheckConsistency` |
| 11 | **Default** — để LLM tự quyết định bước tiếp theo | `nil` (autonomous) |

Trả về `nil` = "tình huống phán quyết, để LLM tự chủ".

**FollowUp không silent**: Khi Route liên tục tính ra cùng một instruction (state chưa cập nhật), Dispatcher đính kèm "lần phát thứ N" để phát lại — không im lặng nuốt.

---

## 6. Sub-Agents

Định nghĩa trong `internal/agents/build.go`:

| Agent | System Prompt | MaxTurns | Stop Condition | Model Role |
|-------|--------------|----------|----------------|------------|
| **Coordinator** | `coordinator.md` | 100,000 | StopGuard (Phase=Complete) | `coordinator` |
| **Architect (short)** | `architect-short.md` | 15 | StopAfterToolResult | `architect` |
| **Architect (long)** | `architect-long.md` | 20 | StopAfterToolResult | `architect` |
| **Writer** | `writer.md` | 30 | StopAfterTools=[commit_chapter] + CheckpointDeltaGuard | `writer` |
| **Editor** | `editor.md` | 20 | CheckpointDeltaGuard | `editor` |

### Tools per Agent

| Agent | Tools |
|-------|-------|
| **Coordinator** | `subagent` (dispatch sub-agents), `novel_context` |
| **Architect** | `save_foundation`, `novel_context` |
| **Writer** | `novel_context`, `read_chapter`, `plan_chapter`, `draft_chapter`, `edit_chapter`, `check_consistency`, `commit_chapter` |
| **Editor** | `novel_context`, `read_chapter`, `save_review`, `save_arc_summary`, `save_volume_summary` |

### Four-layer Code Constraints (Không rely vào prompt)

| Layer | Location | Role |
|-------|----------|------|
| `StopAfterTools` / `StopAfterToolResult` | `agents/build.go` SubAgentConfig | Công cụ quan trọng thành công → end_turn |
| `CheckpointDeltaGuard` | `host/reminder/subagent_guards.go` | Baseline checkpoint, phải thấy checkpoint mới mới được end_turn; chặn 3 lần → terminate |
| `next_step` inline trong tool | Tool return value fields | "Tôi vừa lưu plan, bước tiếp theo là draft..." |
| Permission/precondition checks | `edit_chapter`, `commit_chapter` etc. | Chặn vật lý ở tầng dữ liệu |

---

## 7. Tools (Công cụ ghi — bộ ba nguyên tử)

Mỗi tool ghi: artifact ghi xuống đĩa → Progress cập nhật → checkpoint thêm vào. Atomic trong khóa loại trừ.

| Tool | Artifact | Checkpoint Step | Role |
|------|----------|-----------------|------|
| `plan_chapter` | `drafts/chXX.plan.json` | `plan` | Writer |
| `draft_chapter` | `drafts/chXX.draft.md` | `draft` | Writer |
| `edit_chapter` | `drafts/chXX.draft.md` | `edit` | Writer |
| `check_consistency` | (read-only, returns inline) | `consistency_check` | Writer |
| `commit_chapter` | `chapters/chXX.md` + Progress | `commit` | Writer |
| `save_review` | `reviews/chXX.json` | `review` | Editor |
| `save_arc_summary` | `summaries/arc-vNNaNN.json` | `arc_summary` | Editor |
| `save_volume_summary` | `summaries/vol-vNN.json` | `volume_summary` | Editor |
| `save_foundation` | `foundation/*.json` | premise/outline/layered_outline/characters/world_rules/expand_arc/append_volume/update_compass/complete_book | Architect |

### Idempotent

Trước khi thực thi, kiểm tra checkpoint trước: nếu `Step+Digest` giống → trả về artifact đã có.

### `commit_chapter` trả về 19 fields

`arc_end_reached`, `next_skeleton_arc`, `book_complete`, `writer_feedback`, `needs_expansion`, `rule_violations`...

### `save_review` Gate

`evaluateScorecardGate` → `SetPendingRewrites` + `SetFlow(FlowRewriting/FlowPolishing)`. Có gate thật, không chỉ ghi log.

---

## 8. Checkpoint & Resume

### Checkpoint (`internal/domain/checkpoint.go`)

```go
type Scope      struct { Kind ScopeKind; Chapter, Volume, Arc int }
type Checkpoint struct {
    Seq        int64       // tăng đơn điệu
    Scope      Scope       // chapter / arc / volume / global
    Step       string      // plan / draft / commit / review / arc_summary / ...
    Artifact   string
    Digest     string
    OccurredAt time.Time
}
```

Lưu trữ: `meta/checkpoints.jsonl`, chỉ append. Trùng `Scope+Step+Digest` → idempotent, không tạo dòng mới.

### Resume Flow

```
Tiến trình khởi động
  → Đọc Progress + Checkpoint gần nhất + PendingCommit + PendingSteer
  → buildResumePrompt → thông báo ngắn (không phải chỉ thị cấp bước)
  → coordinator.Prompt(resumePrompt) + Dispatcher.Enable + Dispatch
  → Coordinator tiếp tục theo chỉ thị Host
```

Resume dùng `Prompt` khởi chạy Run mới (bộ đếm turn reset, ngữ cảnh sạch), không phải `FollowUp`.

---

## 9. Writer Flow chi tiết (một chapter)

```
Writer được dispatch
  → novel_context()     — load working_memory + episodic_memory + reference_pack
  → read_chapter(N)     — đọc chapter trước đó (nếu có)
  → plan_chapter()      — lập kế hoạch (checkpoint: plan)
  → draft_chapter()     — viết bản nháp (checkpoint: draft)
  → check_consistency() — kiểm tra consistency (checkpoint: consistency_check)
  → commit_chapter()    — commit + rules check + arc detection (checkpoint: commit)
  → StopAfterTools=[commit_chapter] → end_turn
```

**ContextManager của Writer**:
- `KeepRecentTokens=20000`
- Chiến lược nén: `StoreSummaryCompact`
- `PostSummaryHook` = `restore.Hook()` bơm lại context từ store sau khi nén

---

## 10. Editor Flow

```
Editor được dispatch
  → novel_context()     — load style_stats, reviews, summaries
  → read_chapter(N)     — đọc chapter cần review
  → save_review()       — đánh giá 7 chiều (checkpoint: review)
       → evaluateScorecardGate → SetPendingRewrites + SetFlow nếu cần
  → Nếu cuối arc: save_arc_summary() (checkpoint: arc_summary)
```

---

## 11. Collaboration Modes (3 chế độ)

| Mode | Mô tả | Trigger |
|------|-------|---------|
| **A. Bàn giao tuần tự** | Architect → Writer → Editor (nhánh chính) | Flow tự động |
| **B. Phản hồi đánh giá (vòng kín)** | Writer phát hiện outline lệch → `writer_feedback` → Coordinator gọi Architect | `commit_chapter.writer_feedback` |
| **C. Mở rộng khung xương (kế hoạch cuộn)** | `arc_end_reached + next_skeleton_arc` → Architect mở rộng arc tiếp | `commit_chapter.arc_end_reached` |

---

## 12. TUI & Headless Entry

### TUI (`internal/entry/tui/`)

`model.go` bootstrap:
1. `ReplayQueue(0)` — replay events từ đầu
2. `Resume()` — khôi phục từ checkpoint

Flow commands từ `handleEnterKey`:
- **Quick start**: `startup.PrepareQuick()` → `rt.StartPrepared(plan.StartPrompt)`
- **Cocreate**: `startup.PrepareCocreate()` → `rt.StartPrepared(plan.StartPrompt)`
- **Import**: `startup.PrepareImport(path)` → `rt.StartPrepared(...)`
- **Resume**: `rt.Resume()`

Listeners (tea.Batch):
- `listenEvents` — `rt.Events()` channel → `eventMsg`
- `listenDone` — `rt.Done()` → `doneMsg{complete: bool}`
- `listenStream` — `rt.Stream()` → delta tokens + StreamClearSentinel
- `listenAskUser` — interactive questions
- `tickSnapshot` — `rt.Snapshot()` every 3s
- `bootstrapRuntime` — replay + resume

### Headless (`internal/entry/headless/`)

- Cùng flow engine, khác presentation layer
- Output stream ra stdout

---

## 13. Subsystems

### Imp (`internal/host/imp/`) — Import Flow

| Stage | Implementation | File |
|-------|---------------|------|
| **Split** (`StageSplitting`) | Regex split text → `[]Chapter`. Supports Chinese (第N章/回/话/卷/节/幕, 卷N, 序章/楔子/尾声/番外), English (Chapter N, Prologue, Epilogue) | `splitter.go` |
| **Foundation** (`StageFoundation`) | LLM call parse `=== TAG ===` envelopes (PREMISE, CHARACTERS, WORLD_RULES, LAYERED_OUTLINE, COMPASS) | `foundation.go`, `envelope.go` |
| **Chapter** (`StageChapter`) | LLM reverse-analyze per-chapter (summary, characters, key events, timeline) | `chapter.go` |

Sau import, continuity preserved — gọi `host.Resume()`.

### Sim (`internal/host/sim/`) — Simulation

- Mô phỏng "what-if" scenarios cho plot
- Merge simulation results vào story chính

### Observer (`internal/host/observer.go`)

- Pattern observer cho tất cả events
- Subscribe/unsubscribe mechanism
- Events: ToolExecStart, ToolExecEnd, TurnStart, TurnEnd, PhaseChanged, FlowChanged, etc.

### Reminder (`internal/host/reminder/`)

- `StopGuard` — cho Coordinator
- `CheckpointDeltaGuard` — cho architect/writer/editor
- `SubAgentGuards` — 3 sub-agent guards

### Exp (`internal/host/exp/`)

- Export TXT/EPUB 3
- Experimental features (read-only, không phụ thuộc LLM)

---

## 14. Events System

Định nghĩa trong `internal/host/events.go` và `internal/domain/runtime_events.go`:

| Event | Trigger | Payload |
|-------|---------|---------|
| `EventToolExecStart` | Tool bắt đầu chạy | Tool name, args |
| `EventToolExecEnd` | Tool chạy xong | Tool name, result, error |
| `EventTurnStart` | Bắt đầu turn mới | Turn number |
| `EventTurnEnd` | Kết thúc turn | Turn number |
| `EventPhaseChanged` | Phase thay đổi | Old phase, new phase |
| `EventFlowChanged` | Flow state thay đổi | Old flow, new flow |
| `EventChapterCommitted` | Chapter được commit | Chapter number |
| `EventBookComplete` | Sách hoàn thành | — |

**Flow Router đăng ký vào `EventToolExecEnd`** — mỗi khi tool chạy xong, Router tính toán instruction tiếp theo.

---

## 15. Host Lifecycle

```go
type Host struct {
    coordinator       *agentcore.Agent
    observer          *observer
    router            *flow.Dispatcher
    routerDetach      func()
    usage             *UsageTracker
    budget            *BudgetSentinel
    notifier          *notify.Notifier
    events, streamCh, done chan ...
    lifecycle         lifecycle  // idle / running / paused / completed
}
```

### API công khai

| Method | Ngữ nghĩa |
|--------|-----------|
| `Start(prompt)` | Khởi động run mới |
| `StartPrepared(plan)` | Khởi động từ prepared plan |
| `Resume()` | Khôi phục từ checkpoint |
| `Continue(text)` | Tiếp tục sau khi dừng |
| `Steer(text)` | Can thiệp người dùng |
| `Abort()` | Hủy run hiện tại |
| `Close()` | Đóng host |
| `Snapshot()` | Lấy snapshot hiển thị |
| `Events()` | Channel sự kiện |
| `Stream()` | Channel streaming output |
| `Done()` | Channel tín hiệu hoàn thành |

### `waitDone()` Flow

```
coordinator.WaitForIdle()
observer.finalize()

if Phase == Complete → lifecycle=completed; phát sự kiện "sáng tác hoàn thành"
else if running      → lifecycle=idle;     phát sự kiện "Điều phối viên dừng"

select { case h.done <- struct{}{}: default: }
```

**Cấm `Inject` / `FollowUp` / `Prompt` trong `waitDone`**.

---

## 16. Context System (Memory Engine)

### A. `novel_context` tool (Writer/Editor gọi chủ động)

3 envelopes:
- `working_memory` — chapter plan, outline, user_rules, directives
- `episodic_memory` — recent_summaries, arc/vol summary, char snapshots, foreshadow, timeline, recent_cast, **style_stats**
- `reference_pack` — anti-ai-tone, chapter-guide theo genre

### B. ctxpack post-compact (`ctxpack/builder.go` + `restore.go`)

Khi context đầy → nén → `buildWriterRestoreText` bơm lại từ store (budget ~6000 token):
plan → outline → snapshots → foreshadow → reviews → timeline

---

## 17. Iron Rules (Từ docs/architecture.md)

1. **Iron Rule 1**: Tools chỉ trả về dữ liệu thực tế, không trả về chỉ thị định tuyến liên lần gọi
2. **Iron Rule 2**: Định tuyến quy trình do Flow Router đảm nhận. `Route(state) → *Instruction`. Trả về `nil` = "để LLM tự chủ". FollowUp không silent.
3. **Iron Rule 3**: Coordinator không thể vật lý `end_turn` trừ khi `Phase=Complete`. StopGuard ở tầng agentcore.

---

## 18. Các điểm mở rộng (Extension Points)

| Đổi gì | Sửa đâu |
|--------|---------|
| Phong cách viết | `writer.md` prompt |
| Tiêu chí đánh giá | `editor.md` prompt |
| Thể loại mới | Thêm reference file trong `assets/references/genres/` |
| Agent phụ mới | Thêm `SubAgentConfig` trong `agents/build.go` |
| Thêm tool | `internal/tools/` + đăng ký trong build.go |
| Song song nhiều tiểu thuyết | Nhiều tiến trình |

---

## 19. Data Flow Diagram (Chi tiết)

```
User Input
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ Entry (TUI/Headless)                                │
│   model.go: handleEnterKey → startup.Prepare*()     │
│   → Host.StartPrepared() hoặc Host.Resume()         │
└─────────────────────────────────────────────────────┘
    │
    ▼
┌─────────────────────────────────────────────────────┐
│ Host                                                │
│   events, streamCh, done channels                   │
│   observer.finalize() → lifecycle transitions       │
│   flow.Dispatcher: EventToolExecEnd → Route → FollowUp│
└─────────────────────────────────────────────────────┘
    │ Prompt
    ▼
┌─────────────────────────────────────────────────────┐
│ Coordinator Agent (MaxTurns=100,000)                 │
│   System: coordinator.md                             │
│   Tools: subagent, novel_context                     │
│   StopGuard: chặn end_turn khi Phase≠Complete        │
│   ToolGate: completePhaseGate (phase=complete → chặn)│
│                                                      │
│   Receive [Host hạ lệnh] → subagent(writer)          │
│   Receive [Người dùng can thiệp] → phán quyết tự chủ │
└─────────────────────────────────────────────────────┘
    │ subagent tool_call
    ▼
┌─────────────────────────────────────────────────────┐
│ Sub-Agents (context + model độc lập)                 │
│                                                      │
│ Architect (15/20 turns)                              │
│   └─ save_foundation → StopAfterToolResult           │
│                                                      │
│ Writer (30 turns)                                    │
│   └─ plan → draft → check → commit → StopAfterTools  │
│   └─ CheckpointDeltaGuard                            │
│   └─ ContextManager: KeepRecentTokens=20000          │
│                                                      │
│ Editor (20 turns)                                    │
│   └─ review → arc_summary → CheckpointDeltaGuard     │
└─────────────────────────────────────────────────────┘
    │ tool calls
    ▼
┌─────────────────────────────────────────────────────┐
│ Tools (atomic: artifact + progress + checkpoint)     │
│                                                      │
│ Đọc: novel_context, read_chapter                     │
│ Ghi: plan_chapter, draft_chapter, edit_chapter,      │
│      check_consistency, commit_chapter,              │
│      save_review, save_arc_summary,                  │
│      save_volume_summary, save_foundation            │
│                                                      │
│ Idempotent: check checkpoint trước khi ghi           │
│ ConcurrencySafe=false: ngăn race condition           │
└─────────────────────────────────────────────────────┘
    │ read/write
    ▼
┌─────────────────────────────────────────────────────┐
│ Store (file system: tmp + rename)                    │
│   Progress, Checkpoints (jsonl), Outline, Drafts,    │
│   Summaries, Characters, World, Signals, Runtime     │
└─────────────────────────────────────────────────────┘
```

---

## 20. Step Numbering History & Analysis

Gốc từ comment trong `internal/host/flow/router.go:72-86`:

```
 1. Phase=Complete        → nil (LLM tự do xuất tóm tắt)
 2. Phase!=Writing        → nil (LLM quyết định architect)
 3. PendingRewrites != rỗng → writer (viết lại/đánh bóng)
3.5. NeedsRewriteReview>0  → editor (re-review sau rewrite)
 4. Flow=Steering         → nil (đang can thiệp)
 5. Thiếu arc review      → editor(arc review)
 6. Thiếu arc summary     → editor(arc summary)
 7. Thiếu volume summary  → editor(volume summary)
 8. Skeleton arc tiếp     → architect_long(expand_arc)
 9. Cuối tập cần quyết định → architect_long(append_volume/complete_book)
10. Flat mode nợ review   → editor(batch review)
10.5. Human Gate Pending  → DỪNG chờ người dùng (/gate)
10.75. Chương chưa review → editor(chapter review)
11. Còn lại               → writer(viết chương tiếp)
```

**Vì sao số lẻ (3.5, 10.5, 10.75)?** — Lịch sử phát triển: các bước được thêm dần để vá lỗ hổng logic, không refactor numbering. Cả 3 bước 10.x đều liên quan đến **editor review** — dấu hiệu review từng là điểm yếu được vá nhiều lần.

### 3 bước 10.x — khác scope, dễ lẫn

| Step | Trigger | Scope | Agent |
|------|---------|-------|-------|
| **10** | `HasPendingFlatReview` — batch N chương đạt mốc `ReviewInterval` nhưng thiếu file review global | **global** (batch) | editor |
| **10.5** | `HumanGatePending` — chapter là bội của `HumanGateEvery` | **gate** (dừng, không agent) | *(dừng)* |
| **10.75** | `NeedsChapterReview > 0` — chapter vừa commit chưa có review file | **chapter** (single) | editor |

---

## 21. Role Exchange Patterns (Writer ↔ Editor heartbeat)

### Chuỗi trao đổi chuẩn (1 chapter)

```
WRITER ──commit──→ EDITOR (10.75: chapter review)
                       │
                       ├── pass ──→ WRITER (11: next chapter)
                       │
                       └── fail ──→ WRITER (3: rewrite)
                                       │
                                       └──commit──→ EDITOR (3.5: re-review)
                                                       │
                                                       └── pass ──→ WRITER (11: next)
```

**Mỗi chapter đều qua writer → editor → writer. Không có đường tắt.**

### Chi tiết 1 chu kỳ:

```
1. Writer viết chương N
   → plan_chapter → draft_chapter → check_consistency → commit_chapter
   → StopAfterTools=[commit_chapter] → end_turn
   → EventToolExecEnd → Dispatcher.Dispatch()

2. Route(State{LastCompleted=N, NeedsChapterReview=N})
   → Step 10.75 matches → Instruction{Agent:"editor", Task:"Đánh giá chương N"}
   → FollowUp("[Host ra lệnh] gọi subagent(editor, ...)")
   → Coordinator gọi subagent(editor)

3. Editor review chương N
   → novel_context → read_chapter(N) → save_review
   → evaluateScorecardGate():
       - PASS: Flow=Writing, PendingRewrites rỗng
       - FAIL: Flow=Rewriting, PendingRewrites=[N]
   → end_turn → Dispatch → Route

4a. PASS case:
   → State{LastCompleted=N, NeedsChapterReview=0}
   → Step 11 matches → Instruction{Agent:"writer", Task:"Viết chương N+1"}

4b. FAIL case (cần rewrite):
   → State{PendingRewrites=[N], Flow=FlowRewriting}
   → Step 3 matches → Instruction{Agent:"writer", Task:"Viết lại chương N"}
   → Writer rewrite → commit → Dispatch → Route
   → State{PendingRewrites rỗng, NeedsRewriteReview=N}
   → Step 3.5 matches → Instruction{Agent:"editor", Task:"Đánh giá lại chương N"}
   → Editor re-review → pass → Step 11 → writer chương N+1
```

### Các pattern trao đổi khác:

| Pattern | Trigger | Flow |
|---------|---------|------|
| **Writer feedback** | `commit_chapter.writer_feedback` (outline lệch) | Writer → Coordinator → Architect → Coordinator → Writer |
| **Arc completion** | `commit_chapter.arc_end_reached` | Writer → Editor(arc review) → Editor(arc summary) → Architect(expand_arc) → Writer |
| **Volume end** | `arc_end + volume_end` | Writer → Editor(arc review) → Editor(arc summary) → Editor(vol summary) → Architect(append_volume/complete_book) |
| **Human gate** | `chapter % HumanGateEvery == 0` | Writer → Gate(DỪNG) → User /gate → Editor(review) → Writer |
| **Steering** | User `Steer()` | Writer/Editor/Architect bị ngắt → Coordinator xử lý steer → Route trả về nil → Coordinator tự quyết |

---

## 22. Vấn đề "lẫn lộn" giữa các step

### Vấn đề 1: Double review (Step 10 + Step 10.75)

```
commit chương 10 (ReviewInterval=5)
  → Step 10: HasPendingFlatReview=true → editor(batch 6-10)
  → Editor review batch → Dispatch → Route
  → Step 10.75: NeedsChapterReview=10 (vì LoadReview(10) vẫn nil — batch review ghi global file, không phải single file)
  → Editor lại được gọi review single chương 10
```

**Root cause**: `HasPendingFlatReview` dùng `LoadLastReviewAny` (tìm cả global + chapter review), nhưng `NeedsChapterReview` dùng `LoadReview` (chỉ tìm single chapter file). Editor làm batch review ghi file global — `NeedsChapterReview` không biết, lại trigger single review. **Double review.**

### Vấn đề 2: Gate nuốt review (Step 10.5 > Step 10.75)

```
commit chương 5 (HumanGateEvery=5)
  → Step 10.5: HumanGatePending=true → DỪNG
  → User bấm /gate → Dispatch → Route
  → Step 10.75: NeedsChapterReview=5 → editor(chapter 5)
```

**User phải chờ**: bấm gate xong mới đến review, review xong mới viết tiếp. Không có cơ chế "review trước khi gate" để user thấy chất lượng trước khi duyệt.

### Vấn đề 3: Arc boundary skip batch review (Step 5-9 > Step 10)

```
commit chương 15 (cuối arc, ReviewInterval=5)
  → Step 5: IsArcEnd=true, !HasArcReview → editor(arc review)
  → Step 10: BỊ BỎ QUA (Step 5 match trước, cascade loại trừ)
  → Editor làm arc review → Dispatch → Route
  → Step 6: !HasArcSummary → editor(arc summary)
  → ... đến khi arc post-processing xong
  → Step 10: HasPendingFlatReview vẫn true, nhưng LastCompleted có thể đã tăng
  → Batch review bị hoãn vô thời hạn
```

### Vấn đề 4: Review lỏng giữa batch và single

`HasPendingFlatReview` (Step 10) tính từ `LoadLastReviewAny` — quét ngược tìm review **gần nhất** (có thể là global hoặc chapter). `NeedsChapterReview` (Step 10.75) dùng `LoadReview(chapter)` — chỉ tìm **single chapter file**.

Hai cơ chế không đồng bộ:
- LoadLastReviewAny thấy global review → "đã có review" → Step 10 không trigger
- LoadReview(chapter) thấy không có single file → "chưa có review" → Step 10.75 trigger

---

## 23. So sánh với flow lý tưởng

### Hiện tại (thực tế)

```
Writer → (10.75: single review) → Editor → Writer
                                        ↘ fail → Writer(rewrite) → Editor(3.5: re-review)
```

Với batch review (Step 10) và human gate (Step 10.5) chen ngang, gây double review, gate block không có pre-review.

### Lý tưởng (đề xuất)

```
Writer → Editor(review) → Gate(nếu cần) → Writer
            │
            ├── single review (chapter lẻ)
            ├── batch review (mốc batch) — gộp luôn single, không gọi 2 lần
            └── arc review (cuối arc) — gộp luôn batch
```

Hợp nhất 3 loại review thành 1 lần gọi editor thông minh (scope-aware). Gate đặt SAU review để user thấy chất lượng trước khi duyệt.

---

## 24. Known Issues & Improvement Plans

Từ `docs/plan.md` — 4 bug Việt hoá sót:

| # | Bug | Impact | Severity |
|---|-----|--------|----------|
| 1 | Prompt nén context Writer còn tiếng Trung (`ctxpack/restore.go`) | Drift ngôn ngữ chương dài, rủi ro rớt sang chữ Hán | **Critical** |
| 2 | Kiểm tra foundation hỏng với tiếng Việt (`tools/premise_structure.go`) | Heading Việt không match map Trung → template_ready=false luôn | **High** |
| 3 | Stylestat chết với tiếng Việt (`internal/stylestat/`) | `validGram` CJK-only → `top_phrases` và `patterns` luôn rỗng | **High** |
| 4 | WordCount = rune count, calib cho Hán tự | Ngưỡng vô nghĩa cho tiếng Việt, risk hard-block commit | **Medium** |

### Lộ trình sửa

`#1` (restore.go) → `#4` (wordcount) → `#2` (premise) → `#3` (stylestat) → rò rỉ nhỏ → Phase 0 model upgrade.
