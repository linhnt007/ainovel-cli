# Fix Human Gate Deadlock: Gate Chỉ Arm Sau Khi Review Xong — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sửa deadlock human gate — tại mốc gate mà chương chưa review, gate phải chờ editor review xong mới arm, để editor không bị `qualityControlGate` chặn (vòng lặp dispatch → chặn → circuit breaker).

**Architecture:** Dời toàn bộ phép tính `HumanGatePending` vào `LoadState` (1 nguồn sự thật duy nhất), thêm điều kiện tiên quyết "mọi việc router làm trước bước gate đã xong" (`hasPendingPreGateWork`). Hai nơi đang tính trùng — `Dispatcher.Dispatch()` và `qualityControlGate` — chuyển sang gọi `LoadState`. Thứ tự `Route()` giữ nguyên (đã đúng: step 10 review > step 10.5 gate).

**Tech Stack:** Go (module `github.com/voocel/ainovel-cli`), log/slog, store JSON trên đĩa.

## Global Constraints

- Comment trong codebase viết tiếng Việt — giữ phong cách.
- KHÔNG thêm dependency mới.
- KHÔNG thay đổi thứ tự `Route()`/`routeInner` — router đã đúng (review trước gate).
- KHÔNG thay đổi thứ tự branch trong `qualityControlGate` (PhaseComplete → frozen → pending → NeedsReviewChapter) — chỉ bỏ phần tính trùng.
- TDD: viết test đỏ trước, chạy xác nhận fail, rồi mới code.
- Verify mỗi task: `go build ./...` và `go test ./...`.
- Chạy `gofmt` trên file đã sửa.

## Context — Flow chuẩn (đích đến)

**Điều kiện mốc gate:** chương N là mốc khi `N % HumanGateEvery == 0`.

**Vòng đời mốc gate N đúng:**

1. **Commit**: writer commit chương N → `LastCompleted = N`, chưa có review, chưa ack.
   - `HumanGatePending = false` (chưa review — điều kiện mới).
   - Router step 10: `NeedsReviewChapter=N` → dispatch **editor** review (single/batch).
   - `qualityControlGate`: gate chưa pending → editor **được phép**; writer bị chặn (branch 4).
2. **Review**: editor review xong → `reviews/NN.json` tồn tại.
   - Dispatch tiếp theo: `HumanGatePending = true` (đã review, chưa ack, không còn việc pre-gate).
   - Router step 10 sạch → step 10.5: `Agent=""` → dispatcher set `gateActive=N`, ghi frozen marker, bắn `onHumanGate` (notification), `AbortSilent`.
   - `qualityControlGate`: gate pending → chặn **mọi** subagent (belt-and-suspenders). Coordinator dừng chờ.
3. **User duyệt**: `/gate ok` → `SaveHumanGateAck(N)`.
   - Dispatch tiếp: gate hết pending → router step 11 → **writer** chương N+1. Frozen/gateActive được clear (Fix 1F/2D hiện có).

**Bất biến:** Gate KHÔNG BAO GIỜ chặn editor review của chương mốc. Gate chỉ arm khi mọi việc router làm trước step 10.5 cho `LastCompleted` đã xong: rewrite queue rỗng, không còn `NeedsRewriteReview`, không còn `NeedsReviewChapter`, hậu xử lý cung truyện (review/summary/volume-summary/expansion/new-volume) hoàn tất.

**Root cause đang sửa (tóm tắt):** `Dispatcher.Dispatch()` (dispatcher.go:163) và `qualityControlGate` (build.go:398) cùng tính `HumanGatePending = LastCompleted%Every==0 && !ack` — true NGAY khi commit, trước review. Router dispatch editor (step 10 thắng 10.5), `qualityControlGate` branch 3 (pending) đứng trước branch 4 (cho phép editor) → chặn editor → vòng lặp repeat 3 lần → circuit breaker. User không được hỏi vì path thông báo (dispatcher.go:201) chỉ chạy khi Route trả `Agent=""`.

---

### Task 1: `LoadState` tính `HumanGatePending` — gate arm chỉ sau khi pre-gate work xong

**Files:**
- Modify: `internal/host/flow/state.go:15-110` (hàm `LoadState`, thêm helper)
- Modify: `internal/host/flow/state_test.go` (sửa 3 call `LoadState(store)` cũ + thêm test mới)
- Test: `internal/host/flow/state_test.go`

**Interfaces:**
- Produces (các task sau dựa vào):
  - `func LoadState(store *storepkg.Store, qualityReviewInterval, humanGateEvery int) State` — signature MỚI (bỏ variadic).
  - `State.HumanGateEvery` và `State.HumanGatePending` được set bên trong `LoadState`; `LoadState` đọc `store.Progress.HumanGateEvery()` trước, fallback về tham số `humanGateEvery`.
  - `func (s *State) hasPendingPreGateWork() bool` — helper internal.

- [ ] **Step 1: Viết test fail trước**

Thêm vào `internal/host/flow/state_test.go` (sau hàm `TestLoadState_UnifiedReviewDerivedFromDisk`). Lưu ý: signature mới `LoadState(store, 0, 2)` hiện COMPILE được nhờ variadic cũ (`qualityReviewInterval ...int`) nhưng chỉ dùng `[0]` → test (ii) sẽ fail đúng.

```go
// TestLoadState_HumanGateWaitsForReview: mốc gate chỉ arm SAU KHI review xong.
// Regression cho bug deadlock: chương 8 hoàn thành (8%2==0) chưa review →
// gate arm sớm → qualityControlGate chặn editor review → lặp dispatch → circuit breaker.
func TestLoadState_HumanGateWaitsForReview(t *testing.T) {
	// (i) Mốc gate (8%2==0), CHƯA review chương 8 → gate CHƯA arm.
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 8)
	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("set gate every: %v", err)
	}
	s := LoadState(store, 0, 2)
	if s.NeedsReviewChapter != 8 {
		t.Fatalf("(i) chưa review chương 8 → NeedsReviewChapter=8, got %d", s.NeedsReviewChapter)
	}
	if s.HumanGatePending {
		t.Fatal("(i) chưa review → gate CHƯA arm, HumanGatePending phải=false")
	}

	// (ii) Đã review chương 8 → gate arm (chưa ack).
	if err := store.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	s2 := LoadState(store, 0, 2)
	if s2.NeedsReviewChapter != 0 {
		t.Fatalf("(ii) đã review chương 8 → NeedsReviewChapter=0, got %d", s2.NeedsReviewChapter)
	}
	if !s2.HumanGatePending {
		t.Fatal("(ii) đã review chưa ack → gate phải arm, HumanGatePending=true")
	}

	// (iii) User ack → gate hết pending.
	if err := store.World.SaveHumanGateAck(8, "ok"); err != nil {
		t.Fatalf("save ack ch8: %v", err)
	}
	s3 := LoadState(store, 0, 2)
	if s3.HumanGatePending {
		t.Fatal("(iii) đã ack → HumanGatePending phải=false")
	}
}

// TestLoadState_HumanGateNonMilestoneAndDisabled: không arm khi chưa tới mốc hoặc gate tắt.
func TestLoadState_HumanGateNonMilestoneAndDisabled(t *testing.T) {
	// (i) Chưa tới mốc (7%2!=0) → không arm dù review thiếu.
	store := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store, 7)
	if err := store.Progress.SetHumanGateEvery(2); err != nil {
		t.Fatalf("set gate every: %v", err)
	}
	if s := LoadState(store, 0, 2); s.HumanGatePending {
		t.Fatal("(i) chương 7 không phải mốc gate → không arm")
	}

	// (ii) Gate tắt (store chưa set, param=0) → không arm dù đã review mốc chương 8.
	store2 := storepkg.NewStore(t.TempDir())
	saveFlatProgress(t, store2, 8)
	if err := store2.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	if s := LoadState(store2, 0, 0); s.HumanGatePending {
		t.Fatal("(ii) gate tắt → không arm")
	}
}

// TestState_hasPendingPreGateWork: các việc pre-gate khiến gate không được arm.
func TestState_hasPendingPreGateWork(t *testing.T) {
	cases := []struct {
		name string
		s    State
		want bool
	}{
		{"review chương mới nợ", State{NeedsReviewChapter: 8}, true},
		{"rewrite queue còn", State{Progress: &domain.Progress{PendingRewrites: []int{3}}}, true},
		{"re-review sau rewrite nợ", State{Progress: &domain.Progress{NeedsRewriteReview: 5}}, true},
		{"arc review nợ", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true}, HasArcReview: false}, true},
		{"arc summary nợ", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true}, HasArcReview: true, HasArcSummary: false}, true},
		{"volume end thiếu volume summary", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true, IsVolumeEnd: true}, HasArcReview: true, HasArcSummary: true, HasVolumeSummary: false}, true},
		{"arc end chờ expansion", State{ArcBoundary: &storepkg.ArcBoundary{IsArcEnd: true, NeedsExpansion: true, NextArc: 2}, HasArcReview: true, HasArcSummary: true}, true},
		{"không còn việc pre-gate", State{NeedsReviewChapter: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.hasPendingPreGateWork(); got != tc.want {
				t.Fatalf("hasPendingPreGateWork() = %v, want %v", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận fail**

Run: `go test ./internal/host/flow/ -run 'TestLoadState_HumanGate|TestState_hasPendingPreGateWork' -v`
Expected: FAIL — `TestLoadState_HumanGateWaitsForReview` case (ii) fail ("gate CHƯA arm → HumanGatePending phải=true" sai, got false). Các case `hasPendingPreGateWork` fail (method chưa tồn tại). Các test cũ `LoadState(store)` vẫn PASS (variadic tương thích).

- [ ] **Step 3: Cài đặt `LoadState` mới**

Sửa `internal/host/flow/state.go`. Đổi signature và thay block đầu hàm:

```go
func LoadState(store *storepkg.Store, qualityReviewInterval, humanGateEvery int) State {
	s := State{
		FoundationMissing:     store.FoundationMissing(),
		QualityReviewInterval: qualityReviewInterval,
	}
```

Bỏ block cũ:
```go
	if len(qualityReviewInterval) > 0 {
		s.QualityReviewInterval = qualityReviewInterval[0]
	}
```

Giữ nguyên phần còn lại (progress load, arc boundary, unified review check — lines 22-102). Ở cuối hàm, TRƯỚC khối `if len(s.LoadWarnings) > 0` (dòng 104-107 hiện tại), chèn:

```go
	// Human gate: chỉ arm khi mọi việc router làm TRƯỚC bước gate (steps 3-10) cho
	// LastCompleted đã xong. Nếu arm sớm khi còn editor review nợ, qualityControlGate
	// sẽ chặn chính subagent editor đang được router cử đi → deadlock (chương mốc gate
	// vừa hoàn thành chưa review → lặp dispatch editor bị chặn 3 lần → circuit breaker).
	if storedEvery := store.Progress.HumanGateEvery(); storedEvery > 0 {
		humanGateEvery = storedEvery
	}
	s.HumanGateEvery = humanGateEvery
	if s.LastCompleted > 0 && humanGateEvery > 0 &&
		s.LastCompleted%humanGateEvery == 0 &&
		!store.World.HasHumanGateAck(s.LastCompleted) &&
		!s.hasPendingPreGateWork() {
		s.HumanGatePending = true
	}
```

Sau hàm `LoadState`, thêm helper:

```go
// hasPendingPreGateWork báo còn việc router làm trước bước human gate (step 10.5):
// rewrite queue, re-review sau rewrite, editor review chương mới, hậu xử lý cuối cung truyện.
// Gate KHÔNG được arm khi còn các việc này, vì qualityControlGate chặn mọi subagent
// khi gate pending — arm sớm sẽ chặn chính agent được router cử đi (deadlock).
func (s *State) hasPendingPreGateWork() bool {
	if s.Progress != nil {
		if len(s.Progress.PendingRewrites) > 0 || s.Progress.NeedsRewriteReview > 0 {
			return true
		}
	}
	if s.NeedsReviewChapter > 0 {
		return true
	}
	if s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		if !s.HasArcReview || !s.HasArcSummary {
			return true
		}
		if s.ArcBoundary.IsVolumeEnd && !s.HasVolumeSummary {
			return true
		}
		if s.ArcBoundary.NeedsExpansion || s.ArcBoundary.NeedsNewVolume {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Sửa 3 call cũ trong `state_test.go` sang signature mới**

`internal/host/flow/state_test.go` dòng 36, 48, 59: `LoadState(store)` → `LoadState(store, 0, 0)` (3 chỗ).

- [ ] **Step 5: Chạy toàn bộ test package flow**

Run: `go test ./internal/host/flow/ -v`
Expected: ALL PASS (cũ + mới).

- [ ] **Step 6: Build + Commit**

Run: `go build ./... && gofmt -l internal/host/flow/state.go internal/host/flow/state_test.go`
Expected: build OK, gofmt không liệt kê 2 file.

```bash
git add internal/host/flow/state.go internal/host/flow/state_test.go
git commit -m "fix(flow): human gate chi arm sau khi review xong — LoadState tinh HumanGatePending"
```

---

### Task 2: `qualityControlGate` bỏ tính trùng, dùng `LoadState` — không chặn editor review tại mốc chưa review

**Files:**
- Modify: `internal/agents/build.go:389-400`
- Modify: `internal/agents/gate_test.go:98-115` (cập nhật test cũ) + thêm test regression
- Test: `internal/agents/gate_test.go`

**Interfaces:**
- Consumes: `flow.LoadState(st, cfg.Quality.ReviewInterval, cfg.Quality.HumanGateEvery)` (signature từ Task 1). `state.HumanGatePending` giờ có điều kiện "đã review" — branch 3 (pending) sẽ không chặn editor đang review chương mốc.

- [ ] **Step 1: Viết/cập nhật test fail trước**

Cập nhật `TestQualityControlGate_BlocksHumanGatePending` (gate_test.go:98-115): chương 1 phải có review trước thì gate mới arm. Thay toàn bộ thân hàm bằng:

```go
func TestQualityControlGate_BlocksHumanGatePending(t *testing.T) {
	st := newTestStore(t)
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1}
	p.HumanGateEvery = 1
	_ = st.Progress.Save(p)
	// Gate chỉ arm sau khi review xong → phải lưu review chương 1 trước.
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch1: %v", err)
	}

	cfg := bootstrap.Config{}
	cfg.Quality.HumanGateEvery = 1

	gate := qualityControlGate(st, cfg)
	decision, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"Viết chương 2"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision == nil || decision.Allowed {
		t.Fatal("expected qualityControlGate to block subagent call when HumanGatePending=true")
	}
}
```

Thêm test regression (sau `TestQualityControlGate_BlocksHumanGatePending`):

```go
// TestQualityControlGate_GateWaitsForEditorReviewAtMilestone: regression bug deadlock.
// Mốc gate chưa review → editor review ĐƯỢC phép (gate chưa arm); sau khi review → gate
// arm chặn mọi subagent; sau khi user ack → hết chặn.
func TestQualityControlGate_GateWaitsForEditorReviewAtMilestone(t *testing.T) {
	st := newTestStore(t)
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3, 4, 5, 6, 7, 8}
	p.HumanGateEvery = 2
	_ = st.Progress.Save(p)

	gate := qualityControlGate(st, bootstrap.Config{})

	// (i) Mốc gate chương 8 CHƯA review → gate chưa arm, editor review được phép.
	decEditor, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor unreviewed milestone: unexpected error: %v", err)
	}
	if decEditor != nil && !decEditor.Allowed {
		t.Fatalf("(i) mốc gate chưa review → editor review phải được phép, got block %q", decEditor.Reason)
	}

	// writer vẫn bị chặn vì còn nợ review (NeedsReviewChapter>0).
	decWriter, err := gate(context.Background(), subagentCall(`{"agent":"writer","task":"Viết chương 9"}`))
	if err != nil {
		t.Fatalf("writer unreviewed milestone: unexpected error: %v", err)
	}
	if decWriter == nil || decWriter.Allowed {
		t.Fatal("(i) chưa review → writer vẫn phải bị chặn (NeedsReviewChapter>0)")
	}

	// (ii) Đã review, chưa ack → gate arm, MỌI subagent bị chặn (kể cả editor).
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 8, Scope: "chapter", Verdict: "accept"}); err != nil {
		t.Fatalf("save review ch8: %v", err)
	}
	decEditor2, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor reviewed milestone: unexpected error: %v", err)
	}
	if decEditor2 == nil || decEditor2.Allowed {
		t.Fatal("(ii) đã review chưa ack → editor cũng phải bị chặn (gate pending)")
	}

	// (iii) User đã ack → gate hết pending, subagent lại được phép.
	if err := st.World.SaveHumanGateAck(8, "ok"); err != nil {
		t.Fatalf("save ack ch8: %v", err)
	}
	decEditor3, err := gate(context.Background(), subagentCall(`{"agent":"editor","task":"Đánh giá chương 8 (scope=chapter)"}`))
	if err != nil {
		t.Fatalf("editor acked milestone: unexpected error: %v", err)
	}
	if decEditor3 != nil && !decEditor3.Allowed {
		t.Fatalf("(iii) đã ack → editor được phép, got block %q", decEditor3.Reason)
	}
}
```

- [ ] **Step 2: Chạy test để xác nhận fail**

Run: `go test ./internal/agents/ -run 'TestQualityControlGate' -v`
Expected: FAIL — case (i) của `TestQualityControlGate_GateWaitsForEditorReviewAtMilestone` fail (build.go vẫn tính gate pending = `8%2==0 && !ack` → chặn editor, mặc dù `LoadState` đã đúng). Đây chính là bằng chứng bug còn tồn tại ở tầng gate.

- [ ] **Step 3: Cài đặt — thay block tính trùng trong `build.go`**

`internal/agents/build.go:389-400`. Thay:

```go
		qualityReviewInterval := cfg.Quality.ReviewInterval
		state := flow.LoadState(st, qualityReviewInterval)
		// Fix 3D: đọc HumanGateEvery từ store (đồng bộ với dispatcher), fallback về config nếu store chưa có.
		if storedEvery := st.Progress.HumanGateEvery(); storedEvery > 0 {
			state.HumanGateEvery = storedEvery
		} else {
			state.HumanGateEvery = cfg.Quality.HumanGateEvery
		}
		state.QualityReviewInterval = cfg.Quality.ReviewInterval
		if state.LastCompleted > 0 && state.HumanGateEvery > 0 && state.LastCompleted%state.HumanGateEvery == 0 && !st.World.HasHumanGateAck(state.LastCompleted) {
			state.HumanGatePending = true
		}
```

Bằng:

```go
		state := flow.LoadState(st, cfg.Quality.ReviewInterval, cfg.Quality.HumanGateEvery)
```

Giữ NGUYÊN các branch 1-4 phía sau (PhaseComplete / frozen / HumanGatePending / NeedsReviewChapter-writer).

- [ ] **Step 4: Chạy toàn bộ test agents**

Run: `go test ./internal/agents/ -v`
Expected: ALL PASS.

- [ ] **Step 5: Build + Commit**

Run: `go build ./... && gofmt -l internal/agents/build.go internal/agents/gate_test.go`
Expected: build OK, gofmt không liệt kê 2 file.

```bash
git add internal/agents/build.go internal/agents/gate_test.go
git commit -m "fix(agents): qualityControlGate dung LoadState — khong chan editor review tai moc gate chua review"
```

---

### Task 3: `Dispatcher.Dispatch()` bỏ tính trùng, dùng `LoadState`

**Files:**
- Modify: `internal/host/flow/dispatcher.go:152-165`

**Interfaces:**
- Consumes: `LoadState(d.store, d.QualityReviewInterval, d.HumanGateEvery)` (Task 1).

- [ ] **Step 1: Cài đặt — thay block tính trùng trong `Dispatch()`**

`internal/host/flow/dispatcher.go` trong `Dispatch()`. Thay:

```go
	state := LoadState(d.store, d.QualityReviewInterval)
	if len(state.LoadWarnings) > 0 && d.onDegraded != nil {
		d.onDegraded(state.LoadWarnings)
	}
	// Fix 3E: đọc HumanGateEvery từ store (đồng bộ với gate), fallback về field.
	if storedEvery := d.store.Progress.HumanGateEvery(); storedEvery > 0 {
		state.HumanGateEvery = storedEvery
	} else {
		state.HumanGateEvery = d.HumanGateEvery
	}
	state.QualityReviewInterval = d.QualityReviewInterval
	if state.LastCompleted > 0 && state.HumanGateEvery > 0 && state.LastCompleted%state.HumanGateEvery == 0 && !d.store.World.HasHumanGateAck(state.LastCompleted) {
		state.HumanGatePending = true
	}
```

Bằng:

```go
	state := LoadState(d.store, d.QualityReviewInterval, d.HumanGateEvery)
	if len(state.LoadWarnings) > 0 && d.onDegraded != nil {
		d.onDegraded(state.LoadWarnings)
	}
```

Các khối phía sau giữ nguyên: Fix 2D auto-clear gateActive, Fix 1F auto-clear frozen, `Route(state)`, nhánh `inst.Agent == "" && state.HumanGatePending` (thông báo gate), `trackRepeat`, dispatch.

- [ ] **Step 2: Chạy toàn bộ test package flow**

Run: `go test ./internal/host/flow/ -v`
Expected: ALL PASS (không có test nào phụ thuộc hành vi cũ).

- [ ] **Step 3: Build + Commit**

Run: `go build ./... && gofmt -l internal/host/flow/dispatcher.go`
Expected: build OK, gofmt không liệt kê file.

```bash
git add internal/host/flow/dispatcher.go
git commit -m "refactor(flow): Dispatcher dung LoadState tinh HumanGatePending — bo logic trung lap"
```

---

### Task 4: Test router tài liệu hóa bất biến review-trước-gate

**Files:**
- Modify: `internal/host/flow/router_test.go`
- Test: `internal/host/flow/router_test.go`

**Interfaces:**
- Consumes: `Route(State)` (không đổi). Chỉ tài liệu hóa hành vi hiện hữu.

- [ ] **Step 1: Viết test tài liệu (xanh sẵn)**

Thêm vào `internal/host/flow/router_test.go`:

```go
// TestRoute_ReviewBeforeGateAtMilestone: bất biến — step 10 (editor review) đứng TRƯỚC
// step 10.5 (human gate). Tại mốc gate mà chương chưa review, router ưu tiên editor review,
// không dừng chờ gate. (Tình huống "cả hai pending" không thể sinh từ LoadState mới, nhưng
// Route phải giữ thứ tự này để không tái phát deadlock gate chặn editor.)
func TestRoute_ReviewBeforeGateAtMilestone(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		TotalChapters:     40,
		CompletedChapters: []int{1, 2, 3, 4, 5, 6, 7, 8},
	}
	s := State{
		Progress:           p,
		LastCompleted:      8,
		NeedsReviewChapter: 8,
		HumanGatePending:   true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("tại mốc gate chưa review, router phải ưu tiên editor review, got %+v", got)
	}
}
```

- [ ] **Step 2: Chạy test**

Run: `go test ./internal/host/flow/ -run TestRoute_ReviewBeforeGateAtMilestone -v`
Expected: PASS (hành vi router hiện hữu).

- [ ] **Step 3: Commit**

```bash
git add internal/host/flow/router_test.go
git commit -m "test(flow): bat bien review-truoc-gate tai moc gate"
```

---

### Task 5: Verify toàn diện

- [ ] **Step 1: Chạy toàn bộ build + test + vet**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build/vet OK, toàn bộ test PASS.

- [ ] **Step 2: Kiểm tra gofmt toàn bộ file đã sửa**

Run: `gofmt -l internal/host/flow/ internal/agents/build.go internal/agents/gate_test.go`
Expected: danh sách rỗng (bỏ qua 2 file `router.go`, `router_test.go` đã lỗi gofmt từ trước — KHÔNG sửa vội, ngoài phạm vi plan).

- [ ] **Step 3: Kiểm tra không còn chỗ tính trùng `HumanGatePending`**

Run: `rg -n "HumanGatePending = true" internal/`
Expected: CHỈ 1 dòng — trong `internal/host/flow/state.go` (`s.HumanGatePending = true`).

- [ ] **Step 4: Báo cáo kết quả**

Ghi rõ: (a) lệnh test đã chạy + kết quả, (b) xác nhận không còn nguồn tính trùng, (c) đã chạy qua thực tế (nếu có) — resume session chương mốc chưa review phải dispatch editor, không phải dừng gate.

## Self-Review

- **Spec coverage:** Deadlock sửa ở 3 điểm (LoadState predicate, qualityControlGate wiring, dispatcher wiring) + test bất biến router. Đúng theo canonical flow đã định nghĩa.
- **Placeholder scan:** Mọi bước có code/test cụ thể; không có TBD/TODO.
- **Type consistency:** `LoadState(store, qualityReviewInterval, humanGateEvery int)` đồng nhất ở state.go, dispatcher.go, build.go, state_test.go. `hasPendingPreGateWork()` trả `bool`, dùng đúng. `SaveReview`/`SaveHumanGateAck`/`SetHumanGateEvery` đúng signature đã tồn tại (`store.World.SaveReview(domain.ReviewEntry{...})`, `store.World.SaveHumanGateAck(ch, note)`, `store.Progress.SetHumanGateEvery(n)`).
