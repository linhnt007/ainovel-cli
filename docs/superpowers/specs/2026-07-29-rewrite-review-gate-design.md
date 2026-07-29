# Post-Rewrite Quality Gate (Re-Editor)

## Problem

After writer completes a rewrite/polish and `PendingRewrites` drains, Flow Router dispatches directly to `writer(next_chapter)` (step 11), skipping mandatory editor re-review. The rewritten chapter ships without quality validation.

## Current Behavior

```
editor(verdict=rewrite) → writer rewrite → commit_chapter drain PendingRewrites
  → router step 11: writer(next_chapter)     ← BUG: no re-editor
```

## Expected Behavior

```
editor(verdict=rewrite) → writer rewrite → commit_chapter drain PendingRewrites
  → router NEW step 3.5: editor(re-review)
    → accept → router continues (gate / next chapter)
    → rewrite/polish → re-enters PendingRewrites cycle
```

## Design

### 1. New field on `Progress` (`internal/domain/runtime.go`)

```go
NeedsRewriteReview int `json:"needs_rewrite_review,omitempty"`
// chương cần đánh giá lại sau khi viết lại xong
```

### 2. `ProgressStore` changes (`internal/store/progress.go`)

| Method | Behavior |
|---|---|
| `CompleteRewrite` | When `remaining==0` (queue fully drained), set `NeedsRewriteReview = chapter` before save. Don't clear. |
| `SetPendingRewrites` | Clear `NeedsRewriteReview = 0` (entering new rewrite cycle). |
| `ClearRewriteReview()` (new) | Atomic clear `NeedsRewriteReview = 0`. |

### 3. Router new step 3.5 (`internal/host/flow/router.go`)

Insert between existing step 3 (PendingRewrites) and step 4 (Steering):

```
// 3.5. Post-rewrite quality gate: re-review before proceeding
if p.NeedsRewriteReview > 0 {
    return editor(Đánh giá lại chương N sau viết lại, scope=chapter)
}
```

Priority ensures:
- PendingRewrites non-empty → writer continues (step 3 wins)
- NeedsRewriteReview set → editor re-reviews (step 3.5)
- Neither → flow continues (steering, arc, gate, next chapter)

### 4. `save_review` accept branch (`internal/tools/save_review.go`)

When `final_verdict == "accept"`:
```go
if progress != nil && progress.NeedsRewriteReview == r.Chapter {
    t.store.Progress.ClearRewriteReview()
}
```

### 5. Test cases (`internal/host/flow/router_test.go`)

| Test | Input | Expect |
|---|---|---|
| TestRoute_NeedsRewriteReview | NeedsRewriteReview=2, no PendingRewrites | editor task |
| TestRoute_PendingRewritesBeforeNeedsReview | PendingRewrites=[3], NeedsRewriteReview=2 | writer(ch3) — step 3 wins |

### No other files need changes

## Flow Diagram

```
editor → rewrite verdict
  → SetPendingRewrites([2])
  → router step 3: writer(Viết lại chương 2)
    → commit_chapter → CompleteRewrite(2)
      → PendingRewrites=[] → ★ NeedsRewriteReview=2
  → router step 3.5: editor(Đánh giá lại chương 2)
    ↓
  ┌── accept ──→ ClearRewriteReview → router → gate/next_chapter
  │
  └── rewrite/polish ──→ SetPendingRewrites([2])
                         (clear NeedsRewriteReview)
                          → router step 3: writer rewrite lại
```

## Risk Assessment

- **Backward compatibility**: new field `omitempty`, old progress files get zero value → no re-review triggered for existing projects (safe).
- **No infinite loop**: `maxRewritePerChapter = 3` in `save_review` caps cycles.
- **No performance impact**: single int field, no new I/O in Route path.
