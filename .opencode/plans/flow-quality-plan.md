# Plan: Flow Stabilization + Output Quality (v2)

## Design Decisions

| Decision | v1 (rejected) | v2 (accepted) |
|----------|---------------|---------------|
| Circuit breaker | Hard-stop N lần | **Soft-stop**: inject instruction mạnh, không block. Hard-stop chỉ `Phase=Writing+Flow=Writing` |
| Phase!=Writing routing | Thêm premise/outline rules | **Giữ nil** cho Init/Complete. Chỉ thêm specific cho Premise/Outline khi FoundationMissing signals |
| Light gate Tier 2 interval | Mỗi 2-3 chapters | **Mỗi 5 chapters** + chỉ khi Tier 1 có warnings |
| Best-of-N scope | Mọi chapters | **Chỉ chapters quan trọng** (arc start, volume start, config list) |
| Ensemble scope | Mọi reviews | **Chỉ khi review score < threshold** |
| Notebook schema | Không rõ | **Có schema**: type/content/chapter_created/expires_at. Inject có chọn lọc. Auto-archive >10 chapters |
| Tests | Thiếu | **Mỗi new tool** cần unit + integration test. Week 5 buffer |

---

## Stream A: Flow Stabilization

### A1 — Soft-Stop Circuit Breaker (sửa từ v1)

**Files:** `internal/host/flow/dispatcher.go`, `internal/domain/runtime.go`

Không hard-stop. Thay đổi:
1. Lần N=3 → log warning
2. Lần N=5 → inject instruction: `"Đây là lần thứ N instruction này. BẮT BUỘC chuyển agent khác hoặc dừng."`
3. Lần N=10 + `Phase=Writing` + `Flow=Writing` → hard-stop (duy nhất trường hợp này)
4. Các FlowState khác (Steering/Polishing) → không bao giờ hard-stop, chỉ soft

```go
type StopLevel int
const (
    StopNone    StopLevel = 0
    StopSoft    StopLevel = 1 // inject mạnh, LLM decide
    StopHard    StopLevel = 2 // kill run (Phase=Writing + Flow=Writing only)
)
```

### A2 — LoadState Resilience

**Files:** `internal/host/flow/state.go`

- Progress load fail → fallback progress (Phase=Init, chapter=0) thay vì nil
- Thêm `LoadDegraded bool` vào State
- ArcBoundary + Review load fail → warning + safe default (đã có, refine)

### A3 — HumanGate Timeout (giữ nguyên)

**Files:** `internal/host/host.go`, `internal/entry/tui/commands.go`, `internal/host/flow/dispatcher.go`

- Config: `human_gate_timeout_minutes: 0` (0 = no timeout)
- Timer goroutine → auto-accept hoặc auto-reject tùy config
- TUI: countdown, user extend

### A4 — Phase!=Writing Rules (sửa từ v1)

**Files:** `internal/host/flow/router.go`, `internal/host/flow/state.go`

Giữ `Phase!=Writing→nil` cho mọi phase. Không add premise/outline rules vào router.

Thay vào đó, move logic vào `state.go`:
- `LoadState` check FoundationMissing + Phase → set `State.PhaseAction` hints
- Router đọc hints (không hardcode phase logic)

### A5 — Steering Timeout

**Files:** `internal/host/flow/router.go`, `internal/host/host.go`

- Đếm dispatch count khi `Flow=Steering`
- N > 3 (config) → auto-exit, fallback `FlowWriting`
- Log + inject instruction cho user

---

## Stream B: Output Quality

### B1 — Light Gate 2-Tier (sửa từ v1)

**Files (NEW):** `internal/tools/save_chapter_check.go`, `internal/store/check_store.go`

**Tier 1 (mỗi chapter):**
- Kiểm tra mechanical rules (word count, rule_violations, fatigue words, format)
- Track warnings

**Tier 2 (conditional):**
- Interval: config `light_gate_tier2_interval: 5` (default)
- Chỉ trigger khi Tier 1 có warnings trong interval
- Editor agent task "chapter_check" — 6 dimensions
- Async (không block commit)

### B2 — Writer Self-Review (1 iteration)

**Files (NEW):** `internal/tools/save_self_review.go`

Sau `commit_chapter`:
1. Router dispatch `writer` task "self_review_chapter X"
2. Writer gọi `save_self_review` — tự đánh giá + sửa
3. 1 iteration max

### B3 — Best-of-N Drafting (sửa từ v1)

**Files (NEW):** `internal/tools/select_draft.go`

- Config: `best_of_n: 2` (default OFF)
- Config list: `quality_boost_chapters: [1]` — chapters được áp dụng
- Router dispatch writer 2 lần → `select_draft` pick tốt hơn

### B4 — Ensemble Review (sửa từ v1)

**Files (NEW):** `internal/tools/save_review_vote.go`

- Config: `ensemble_review: false` (default OFF)
- Config: `ensemble_review_threshold: 50` — chỉ trigger khi score < threshold
- 2+1 tiebreaker: 2 editors → 3rd nếu disagree

### B5 — Editor Consistency

**Files:** `internal/tools/save_review.go`

- `ReviewHash string` — hash chapter content → cache nếu không đổi
- Score validation [0, 100]

### B6 — Notebook System (sửa từ v1)

**Files (NEW):** `internal/tools/save_notebook.go`, `internal/store/notebooks.go`

Schema:
```go
type NotebookEntry struct {
    Type          string // "character" | "plot" | "style" | "world"
    Content       string
    Chapter       int
    Tags          []string // character names, locations
    ExpiresAt     int     // chapter number, 0 = permanent
}
```

Injection:
- Chỉ entries liên quan đến characters/locations trong chapter hiện tại
- `novel_context_builders.go`: inject `writer_notebook` section

Retention:
- Auto-archive entries > 10 chapters old
- Expired entries exclude from context

### B7 — StyleStat Enforcement

**Files:** `internal/stylestat/stylestat.go`, `internal/tools/commit_chapter.go`

- Config: `style_repeat_threshold: 5` (default)
- Exceed → commit OK, inject warning vào context
- TUI panel highlight

---

## Implementation Order (sửa từ v1)

```
Week 1 (Flow ổn định):
  A1: Soft-stop circuit breaker
  A2: LoadState resilience
  A5: Steering timeout
  Tests: unit tests cho 3 items

Week 2 (Quality cơ bản):
  B1: Light Gate Tier 1 (mechanical check)
  B7: StyleStat enforcement
  A3: HumanGate timeout
  A4: Phase!=Writing hints
  Tests: unit tests

Week 3 (Quality nâng cao - conditional):
  B2: Writer self-review
  B1 Tier 2: Conditional (chỉ khi Tier 1 warnings)
  Tests: unit + integration

Week 4 (Quality expensive - optional):
  B3: Best-of-N (configurable, chỉ chapters quan trọng)
  B4: Ensemble review (configurable, chỉ khi score thấp)
  Tests: unit + integration

Week 5 (Notebook + Polish):
  B6: Notebook system
  B5: Editor consistency
  Integration tests
  End-to-end flow test
```

---

## Cost Analysis

| Feature | Cost/chapter | Default | Notes |
|---------|-------------|---------|-------|
| Current | 1 writer + 1 editor/5ch | ON | Baseline |
| Light Gate T1 | ~0 (mechanical) | ON | Free |
| Light Gate T2 | +1 editor/5ch | ON (conditional) | Chỉ khi T1 warnings |
| Self-review | +1 writer | ON | 1 iteration |
| Best-of-N (N=2) | +1 writer | OFF | Important chapters only |
| Ensemble (2+1) | +2-3 editors | OFF | Chỉ khi score thấp |
| Notebook | ~0 (storage) | ON | Free |

**Worst-case (all ON):** ~5x cost
**Default (recommended):** ~1.3x cost

---

## Risk Assessment

| Risk | Mitigation |
|------|------------|
| Soft-stop LLM ignore instruction | Log + user notification. Hard-stop only as last resort. |
| Light gate Tier 2 latency | Async, non-blocking. Commit proceeds immediately. |
| Notebook context overflow | Selective injection + auto-archive >10 chapters |
| Best-of-N API fail | Fallback to single draft (no quality loss, same cost) |
| Ensemble review API fail | Fallback to single editor review |
| Circuit breaker kill production | Hard-stop limited to Phase=Writing + Flow=Writing only |
