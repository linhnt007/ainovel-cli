# Unified Review Step 10 — Đề xuất tái cấu trúc

> Ngày: 2026-07-30 | Status: Draft

## Vấn đề

Hiện tại review logic bị phân mảnh thành 3 step với numbering lịch sử:

```
Step 10:   HasPendingFlatReview       → editor(batch)
Step 10.5: HumanGatePending           → DỪNG (/gate)
Step 10.75: NeedsChapterReview > 0    → editor(single)
```

4 bug đã xác định (xem `docs/step-flow-analysis.md` §22):

1. **Double review** — batch (10) + single (10.75) không biết về nhau → editor gọi 2 lần
2. **Gate trước review** — step 10.5 > 10.75, user gate xong mới thấy chất lượng
3. **Arc boundary skip batch** — step 5-9 ưu tiên cao hơn, batch review bị hoãn
4. **LoadLastReviewAny vs LoadReview không đồng bộ** — 2 cơ chế kiểm tra khác nhau

## Giải pháp: Unified Review + Gate sau Review

### Ý tưởng

Gộp 3 step → 2 step. Step 10 (unified review) gọi editor 1 lần với scope-aware task. Step 10.5 (gate) đặt SAU review.

### Priority cascade mới

```
 1. Phase=Complete           → nil
 2. Phase!=Writing           → nil
 3. PendingRewrites != rỗng  → writer (rewrite)
3.5. NeedsRewriteReview > 0  → editor (re-review)
 4. Flow=Steering            → nil
 5-9. Arc post-processing    → editor/architect (giữ nguyên)
10. NeedsReviewChapter > 0   → editor (unified)
10.5. HumanGatePending       → DỪNG (/gate)
11. Còn lại                  → writer (next chapter)
```

---

## Code Changes

### 1. `internal/host/flow/state.go` — State & LoadState

**State thay đổi**:

```go
// XÓA:
//   HasPendingFlatReview bool
//   NeedsChapterReview   int

// THÊM:
//   NeedsReviewChapter int  // chương cần review (0 = không cần)
//   IsReviewBatch      bool // true = đây là mốc batch (bội ReviewInterval)
```

**LoadState thay đổi** — thay thế logic từ line 84-116:

```go
// Unified review check: thay thế HasPendingFlatReview + NeedsChapterReview
// Chỉ kiểm tra single review file (LoadReview), không còn LoadLastReviewAny.
// Nếu đúng mốc batch (ReviewInterval), đặt IsReviewBatch=true để Router
// dispatch editor với scope=both (batch + single).
if s.LastCompleted > 0 {
    isArcBoundary := s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd
    if !isArcBoundary {
        review, rerr := store.World.LoadReview(s.LastCompleted)
        if rerr != nil {
            s.LoadWarnings = append(s.LoadWarnings, "LoadReview: "+rerr.Error())
        } else if review == nil {
            s.NeedsReviewChapter = s.LastCompleted
            reviewInterval := domain.GetReviewInterval(s.QualityReviewInterval)
            if s.LastCompleted%reviewInterval == 0 {
                s.IsReviewBatch = true
            }
        }
    }
}
```

### 2. `internal/host/flow/router.go` — Route

**Thay thế line 188-216** (block 10 + 10.5 + 10.75 cũ) bằng:

```go
// 10. Unified review: chương vừa hoàn thành chưa có editor review.
// Khi đúng mốc batch (ReviewInterval), editor làm cả batch + single trong 1 lần gọi.
// Khi không phải mốc batch, editor chỉ review single chapter.
if s.NeedsReviewChapter > 0 {
    if s.IsReviewBatch {
        reviewInterval := domain.GetReviewInterval(s.QualityReviewInterval)
        to := (s.LastCompleted / reviewInterval) * reviewInterval
        from := to - reviewInterval + 1
        return &Instruction{
            Agent:  "editor",
            Task:   fmt.Sprintf("Đánh giá batch chương %d-%d + chapter %d (scope=both)", from, to, s.NeedsReviewChapter),
            Reason: fmt.Sprintf("Review định kỳ batch %d-%d + chapter mới %d", from, to, s.NeedsReviewChapter),
        }
    }
    return &Instruction{
        Agent:  "editor",
        Task:   fmt.Sprintf("Đánh giá chương %d (scope=chapter)", s.NeedsReviewChapter),
        Reason: fmt.Sprintf("Chương %d đã hoàn thành nhưng chưa được editor đánh giá", s.NeedsReviewChapter),
    }
}

// 10.5. Human Gate: dừng chờ người dùng duyệt (SAU KHI editor đã review xong).
// User thấy kết quả review trước khi quyết định duyệt hay không.
if s.HumanGatePending {
    return &Instruction{
        Agent:  "",
        Task:   fmt.Sprintf("DỪNG: mốc duyệt chương %d — chờ người dùng duyệt (/gate)", s.LastCompleted),
        Reason: "human gate",
    }
}
```

### 3. `assets/prompts/editor.md` — Hướng dẫn scope=both

**Không cần sửa `internal/tools/save_review.go`** — schema đã có param `scope` (save_review.go:51). `SaveReview` trong `world.go:284-290` đã xử lý scope để chọn đường dẫn:
- `scope=chapter` → `reviews/NN.json`
- `scope=global` → `reviews/NN-global.json`

Chỉ cần cập nhật `editor.md` prompt: khi nhận task scope=both, editor gọi `save_review` 2 lần với scope khác nhau.

### 4. `internal/agents/build.go` — qualityControlGate

**File**: `internal/agents/build.go:420`

Gate này chặn writer dispatch khi còn nợ review — dùng `HasPendingFlatReview`:

```go
// Hiện tại:
if state.HasPendingFlatReview && a.Agent == "writer" {

// Sau refactor:
if s.NeedsReviewChapter > 0 && a.Agent == "writer" {
```

Nếu bỏ sót → compile error (field `HasPendingFlatReview` không còn tồn tại trong State).

### 5. `internal/agents/gate_test.go` — TestQualityControlGate_BlocksWriterOnPendingReview

**File**: `internal/agents/gate_test.go:117-144`

Test gán `HasPendingFlatReview` trực tiếp. Cần update:
- Gán `NeedsReviewChapter = 5` (thay vì `HasPendingFlatReview = true`)
- Verify gate vẫn chặn writer dispatch

### 6. `internal/host/flow/state_test.go` — LoadState test

**File**: `internal/host/flow/state_test.go:35-54`

Test `HasPendingFlatReview` trong LoadState. Cần update sang field mới.

### 7. `internal/store/world.go` — Giữ nguyên LoadLastReviewAny

`LoadLastReviewAny` có thể giữ lại (dùng cho diagnostic/report), nhưng không còn dùng trong LoadState.

### 8. `internal/host/flow/router_test.go` — Cập nhật test

Cập nhật test cases:
- `NeedsReviewChapter=5, IsReviewBatch=false` → single review
- `NeedsReviewChapter=10, IsReviewBatch=true` → batch+single
- `NeedsReviewChapter=0, HumanGatePending=true` → gate

---

## Complete Change List

| # | File | Change |
|---|------|--------|
| 1 | `internal/host/flow/state.go` | Xóa `HasPendingFlatReview`, `NeedsChapterReview`. Thêm `NeedsReviewChapter int`, `IsReviewBatch bool`. Update LoadState. |
| 2 | `internal/host/flow/router.go` | Thay block 188-216 bằng unified review + gate sau review |
| 3 | `assets/prompts/editor.md` | Hướng dẫn scope=both: editor gọi `save_review` 2 lần với scope khác nhau (schema đã có param scope, không cần sửa save_review.go) |
| 4 | `internal/agents/build.go:420` | qualityControlGate: `HasPendingFlatReview` → `NeedsReviewChapter > 0` |
| 5 | `internal/agents/gate_test.go:117-144` | Update test field từ `HasPendingFlatReview` sang `NeedsReviewChapter` |
| 6 | `internal/host/flow/state_test.go:35-54` | Update LoadState test cho field mới |
| 7 | `internal/host/flow/router_test.go` | Test cases mới: single review, batch+single, gate |

**Không đổi**: `internal/store/world.go` (`LoadLastReviewAny` giữ lại cho diagnostic), `Dispatcher.Dispatch()`, arc post-processing (step 5-9), rewrite flow (step 3/3.5), `save_review` gate.

---

## Impact Analysis

### Lợi ích

| Lợi ích | Mô tả |
|---------|-------|
| Không double review | Editor gọi 1 lần, xử lý cả batch + single |
| User thấy review trước gate | Gate đặt sau review, user thấy chất lượng trước khi duyệt |
| Không bị arc boundary skip | Unified review dùng `LoadReview` (single file), không phụ thuộc `HasPendingFlatReview` |
| Code đơn giản hơn | 3 step → 2 step, 2 field → 2 field rõ ràng hơn |
| Tiết kiệm LLM call | Batch + single gộp 1 lần (~1 call thay vì 2) |
| Backward compatible | File single review vẫn ghi như cũ, global file là additive |

### Rủi ro & Mitigation

| Rủi ro | Mitigation |
|--------|------------|
| Editor không biết cách xử lý scope=both | Cập nhật `editor.md` với hướng dẫn output structured |
| save_review ghi 2 file cần atomic | Dùng cùng tmp+rename pattern, lock 1 lần |
| LoadState thay đổi logic → miss edge case | Giữ LoadLastReviewAny làm fallback diagnostic, thêm log |
| Test regression | Cập nhật router_test.go đầy đủ trước khi merge |

### Không ảnh hưởng

- `Dispatcher.Dispatch()` — HumanGatePending vẫn tính như cũ
- Arc post-processing (step 5-9) — giữ nguyên
- Rewrite flow (step 3, 3.5) — giữ nguyên
- `save_review` gate (`evaluateScorecardGate`) — giữ nguyên

---

## Migration Plan

### Phase 1: Implement (1-2 ngày)

1. Sửa `state.go`: đổi State fields + LoadState logic
2. Sửa `router.go`: thay block 188-216
3. Cập nhật `router_test.go`
4. Chạy test: `go test ./internal/host/flow/...`
5. Cập nhật `editor.md` prompt (hướng dẫn scope=both)

### Phase 2: Verify (1 ngày)

1. Dry-run với Quick flow 10 chương — verify không double review
2. Test gate scenario — verify user thấy review trước gate
3. Test arc boundary — verify không bị skip

### Phase 3: Cleanup (tùy chọn)

1. Xóa `HasPendingFlatReview` references nếu còn sót
2. Deprecate `LoadLastReviewAny` (giữ lại, thêm comment)

---

## So sánh trước/sau

### Trước (hiện tại)

```
commit chương 5 (ReviewInterval=5, HumanGateEvery=5)
  → Step 10: HasPendingFlatReview? → LoadLastReviewAny → có review cũ? không → editor(batch 1-5)
  → Editor review batch → Dispatch
  → Step 10: LoadLastReviewAny thấy global review → skip
  → Step 10.5: HumanGatePending → DỪNG
  → User /gate → Dispatch
  → Step 10.75: LoadReview(5) = nil → editor(single 5)  ← DOUBLE REVIEW!
  → Editor review single → Dispatch
  → Step 11: writer(chapter 6)
```

### Sau (đề xuất)

```
commit chương 5 (ReviewInterval=5, HumanGateEvery=5)
  → Step 10: NeedsReviewChapter=5, IsReviewBatch=true → editor(unified: batch 1-5 + chapter 5)
  → Editor làm 1 lần, ghi cả 2 file → Dispatch
  → Step 10: LoadReview(5) != nil → skip
  → Step 10.5: HumanGatePending → DỪNG (user thấy review TRƯỚC)
  → User /gate → Dispatch
  → Step 11: writer(chapter 6)
```

**Kết quả**: 1 editor call thay vì 2, user thấy review trước gate.