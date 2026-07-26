# Plan triển khai: Rate limit đa-giới-hạn, global, xoay model

> Handoff cho session thực thi (Sonnet). Đọc từ trên xuống, làm theo task order.
> Mọi code dưới đây viết sẵn để paste + chỉnh path. Style: comment tiếng Việt, dùng
> `errs`, `slog`, khớp repo hiện tại.

---

## 0. Bối cảnh + quyết định đã chốt

**Vấn đề:** rate limit hiện tại (`internal/bootstrap/models.go:459-577`) hard-code `minInterval=5s`
(12 RPM) cho MỌI model, không config được, chỉ RPM, in-memory, backoff trên mọi lỗi.
Vượt quota provider (nhất là RPD free tier) = cờ lạm dụng = ban nick. **Ưu tiên chất lượng
đầu ra hơn tốc độ.**

**Quyết định (user chốt):**
1. 4 giới hạn per provider+model, config được hết: **RPM, RPD, TPM, MaxConcurrent**.
2. Chạm limit → **xoay** (rotation) xuống fallback tiếp theo; **ưu tiên quay lại model gốc**
   khi nó hồi (giữ chất lượng); check availability real-time; đọc request status.
3. Đếm **persist qua restart** + **global toàn máy** (2 dự án không double-spend quota provider).
4. **Tách lỗi quota (429) riêng** khỏi lỗi vận hành (network/timeout).
5. `RPD` = **cửa sổ cuộn 24h** (an toàn nhất, không bao giờ vượt trong bất kỳ 24h nào).
6. `MaxConcurrent` = **in-process semaphore** (không cross-process — coordinator gần tuần tự,
   RPM/RPD/TPM global đã chặn vector ban chính; cross-process semaphore = over-engineer).

**Giới hạn kỹ thuật đã biết:**
- agentcore KHÔNG expose `Retry-After` header. Phát hiện 429 qua `agentcore.FailoverReason(err)=="rate_limit"`.
  Cooldown 429 = exponential cố định (30s→60s→120s, cap 300s), KHÔNG đọc header.
- `429 cooldown` giữ **in-process** (đơn giản); RPD/RPM/TPM counter mới là lớp global chống ban.
- Token thực lấy từ `resp.Message.Usage.TotalTokens` (fallback `Input+Output`). Pre-gate TPM dùng
  ước lượng thô (`estimateTokens`), Record cập nhật số thực sau.

---

## 1. Kiến trúc

```
config.jsonc (RateLimit + ModelLimits per provider)
      │  NewModelSet đọc → tính Limits hiệu lực per (provider,model)
      ▼
ratelimit.Global  (singleton registry)
  ├─ Register(provider, model, Limits)          ← gọi lúc createModelFromConfig
  ├─ For(provider, model) *Limiter              ← tra cứu runtime
  └─ store *globalStore (file + lockfile, ~/.config/ainovel-cli/ratelimit.json)
      │
      ▼
Limiter (per provider/model key)
  ├─ TryAcquire(estTokens) (*Handle, ok)        ← non-block, cho rotation
  ├─ Acquire(ctx, estTokens) (*Handle, err)     ← block-and-wait, cho single-target
  ├─ sem chan struct{}                          ← MaxConcurrent in-process
  └─ cooldownUntil time.Time                    ← 429 in-process
Handle.Record(actualTokens int, err error)      ← update event tokens + cooldown

Consumers trong models.go:
  ├─ rateLimitedModel  (single target: Default + role không fallback + mỗi fallback target)
  │     Generate: Acquire → raw.Generate → Record
  └─ failoverModel     (role có fallback): ROTATION
        Generate: duyệt [primary,...fallbacks], TryAcquire target đầu còn quota (primary first),
                  nếu hết sạch → chờ min(retryAfter) rồi Acquire target đó; raw.Generate; Record;
                  429 → Record(cooldown)+xoay tiếp; lỗi khác → path failover cũ.
```

**Ranh giới tách bạch:**
- Quota/429 → đi đường **limiter** (cooldown + rotate).
- Lỗi vận hành (network/timeout/stream_idle) → đi đường **failover cũ** (`IsFailoverEligible`).

---

## 2. Task order (làm tuần tự, mỗi task compile được)

| # | Task | File | Acceptance |
|---|---|---|---|
| T1 | Package `ratelimit`: types + window math + test | `internal/ratelimit/limiter.go`, `window.go`, `window_test.go` | `go test ./internal/ratelimit/` xanh, không cần IO |
| T2 | Global store: lockfile + persist + prune + test | `internal/ratelimit/store.go`, `store_test.go` | test concurrent 2 goroutine không mất update |
| T3 | Registry singleton + Limiter Acquire/TryAcquire/Record | `internal/ratelimit/registry.go` | unit rotation prefer-primary, cooldown |
| T4 | Config: `RateLimitConfig` + wiring | `internal/bootstrap/config.go` | merge test giữ field |
| T5 | models.go: bỏ code cũ, nối registry, rotation | `internal/bootstrap/models.go` | build xanh, failover test |
| T6 | `config.example.jsonc` + doc | `config.example.jsonc` | ví dụ chạy được |
| T7 | Verify end-to-end | — | build + test toàn repo + smoke run |

---

## 3. T1 — `internal/ratelimit/limiter.go` + `window.go`

### `internal/ratelimit/window.go`
```go
package ratelimit

import "time"

// event là một lần gọi model đã ghi nhận: thời điểm + token thực (hoặc ước lượng chờ Record).
type event struct {
	TS     int64 `json:"ts"`     // unix nano
	Tokens int   `json:"tokens"` // token của request này (est trước, thực sau Record)
}

// Limits là giới hạn hiệu lực cho một (provider,model). 0 = không giới hạn chiều đó.
type Limits struct {
	RPM           int `json:"rpm,omitempty"`
	RPD           int `json:"rpd,omitempty"`
	TPM           int `json:"tpm,omitempty"`
	MaxConcurrent int `json:"max_concurrent,omitempty"`
}

const (
	minuteWindow = time.Minute
	dayWindow    = 24 * time.Hour
)

// prune bỏ mọi event cũ hơn 24h (cửa sổ lớn nhất). Giữ log bounded.
func prune(evs []event, now time.Time) []event {
	cutoff := now.Add(-dayWindow).UnixNano()
	i := 0
	for i < len(evs) && evs[i].TS < cutoff {
		i++
	}
	if i == 0 {
		return evs
	}
	return append(evs[:0], evs[i:]...)
}

// check tính xem thêm 1 request (estTokens) có vượt bất kỳ giới hạn nào không.
// Trả về retryAfter=0 nếu OK; ngược lại là khoảng chờ tối thiểu tới khi có slot.
// evs PHẢI đã prune + sort tăng theo TS.
func check(evs []event, lim Limits, now time.Time, estTokens int) time.Duration {
	var worst time.Duration

	// RPD: đếm event trong 24h cuộn.
	if lim.RPD > 0 {
		dayStart := now.Add(-dayWindow).UnixNano()
		cnt := 0
		var oldest int64
		for _, e := range evs {
			if e.TS >= dayStart {
				if cnt == 0 {
					oldest = e.TS
				}
				cnt++
			}
		}
		if cnt >= lim.RPD {
			// slot mở khi event cũ nhất trong cửa sổ rời khỏi 24h.
			ra := time.Duration(oldest-dayStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	// RPM: đếm event trong 60s.
	if lim.RPM > 0 {
		minStart := now.Add(-minuteWindow).UnixNano()
		cnt := 0
		var oldest int64
		for _, e := range evs {
			if e.TS >= minStart {
				if cnt == 0 {
					oldest = e.TS
				}
				cnt++
			}
		}
		if cnt >= lim.RPM {
			ra := time.Duration(oldest-minStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	// TPM: tổng token trong 60s + estTokens.
	if lim.TPM > 0 {
		minStart := now.Add(-minuteWindow).UnixNano()
		sum := 0
		var oldest int64
		first := true
		for _, e := range evs {
			if e.TS >= minStart {
				if first {
					oldest = e.TS
					first = false
				}
				sum += e.Tokens
			}
		}
		if sum+estTokens > lim.TPM && !first {
			ra := time.Duration(oldest-minStart) + time.Nanosecond
			if ra > worst {
				worst = ra
			}
		}
	}

	return worst
}
```

### `internal/ratelimit/window_test.go`
```go
package ratelimit

import (
	"testing"
	"time"
)

func mkEvents(now time.Time, agesSec ...int) []event {
	evs := make([]event, len(agesSec))
	for i, a := range agesSec {
		evs[i] = event{TS: now.Add(-time.Duration(a) * time.Second).UnixNano(), Tokens: 1000}
	}
	return evs
}

func TestCheck_RPM(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	lim := Limits{RPM: 3}
	// 2 event trong 60s → còn slot.
	if d := check(mkEvents(now, 10, 30), lim, now, 0); d != 0 {
		t.Fatalf("expect ok, got %v", d)
	}
	// 3 event trong 60s → chặn, chờ tới khi event cũ nhất (30s trước) rời cửa sổ ≈ 30s.
	d := check(mkEvents(now, 30, 20, 10), lim, now, 0)
	if d <= 0 || d > 31*time.Second {
		t.Fatalf("expect ~30s, got %v", d)
	}
}

func TestCheck_RPD(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	lim := Limits{RPD: 2}
	d := check(mkEvents(now, 3600, 1800), lim, now, 0) // 2 event trong ngày
	if d <= 0 {
		t.Fatalf("expect blocked, got %v", d)
	}
}

func TestCheck_TPM(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	lim := Limits{TPM: 2500}
	// 2×1000 token trong 60s, thêm est 1000 → 3000 > 2500 → chặn.
	if d := check(mkEvents(now, 10, 20), lim, now, 1000); d == 0 {
		t.Fatal("expect TPM block")
	}
	// thêm est 400 → 2400 < 2500 → ok.
	if d := check(mkEvents(now, 10, 20), lim, now, 400); d != 0 {
		t.Fatalf("expect ok, got %v", d)
	}
}

func TestPrune(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	evs := mkEvents(now, 90000, 3600, 10) // 25h, 1h, 10s
	got := prune(evs, now)
	if len(got) != 2 {
		t.Fatalf("expect 2 after prune, got %d", len(got))
	}
}
```

---

## 4. T2 — `internal/ratelimit/store.go` (global persist + lockfile)

```go
package ratelimit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// globalStore giữ event log toàn máy, chia sẻ giữa mọi tiến trình/dự án.
// File: <UserConfigDir>/ainovel-cli/ratelimit.json (Windows: %AppData%\ainovel-cli\).
// Đồng bộ cross-process bằng lockfile create-exclusive + stale detection.
type globalStore struct {
	path string
	lock string
}

type persisted struct {
	Events map[string][]event `json:"events"` // key = "provider/model"
}

func newGlobalStore() (*globalStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config dir: %w", err)
	}
	base := filepath.Join(dir, "ainovel-cli")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", base, err)
	}
	return &globalStore{
		path: filepath.Join(base, "ratelimit.json"),
		lock: filepath.Join(base, "ratelimit.lock"),
	}, nil
}

const (
	lockStale   = 10 * time.Second
	lockSpin    = 20 * time.Millisecond
	lockTimeout = 5 * time.Second
)

func (g *globalStore) acquireLock() error {
	deadline := time.Now().Add(lockTimeout)
	for {
		f, err := os.OpenFile(g.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return nil
		}
		// Stale lock: chủ cũ crash không dọn → xoá nếu quá cũ.
		if fi, statErr := os.Stat(g.lock); statErr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(g.lock)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("acquire ratelimit lock timeout")
		}
		time.Sleep(lockSpin)
	}
}

func (g *globalStore) releaseLock() { _ = os.Remove(g.lock) }

// withKey chạy fn dưới lock global, cấp cho fn slice event của 1 key (đã prune),
// và ghi lại kết quả fn trả về. now truyền vào để test xác định.
func (g *globalStore) withKey(key string, now time.Time, fn func(evs []event) []event) error {
	if err := g.acquireLock(); err != nil {
		return err
	}
	defer g.releaseLock()

	st := g.read()
	evs := prune(st.Events[key], now)
	st.Events[key] = fn(evs)
	return g.write(st)
}

// peekKey đọc-only: trả retryAfter cho key mà KHÔNG ghi. Fail-open (lock lỗi → 0 = cho qua).
func (g *globalStore) peekKey(key string, now time.Time, lim Limits, est int) time.Duration {
	if err := g.acquireLock(); err != nil {
		return 0
	}
	defer g.releaseLock()
	st := g.read()
	return check(prune(st.Events[key], now), lim, now, est)
}

func (g *globalStore) read() persisted {
	st := persisted{Events: map[string][]event{}}
	data, err := os.ReadFile(g.path)
	if err != nil {
		return st // không tồn tại → rỗng
	}
	_ = json.Unmarshal(data, &st)
	if st.Events == nil {
		st.Events = map[string][]event{}
	}
	return st
}

func (g *globalStore) write(st persisted) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, g.path) // Go os.Rename replace-existing cả trên Windows (MoveFileEx)
}
```

### `internal/ratelimit/store_test.go`
```go
package ratelimit

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *globalStore {
	dir := t.TempDir()
	return &globalStore{path: filepath.Join(dir, "rl.json"), lock: filepath.Join(dir, "rl.lock")}
}

func TestStore_ConcurrentAppend(t *testing.T) {
	g := testStore(t)
	now := time.Unix(1_000_000, 0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = g.withKey("p/m", now, func(evs []event) []event {
				return append(evs, event{TS: time.Now().UnixNano(), Tokens: 1})
			})
		}()
	}
	wg.Wait()
	st := g.read()
	if len(st.Events["p/m"]) != 50 {
		t.Fatalf("lost updates: got %d want 50", len(st.Events["p/m"]))
	}
}

func TestStore_Persist(t *testing.T) {
	g := testStore(t)
	now := time.Unix(1_000_000, 0)
	_ = g.withKey("p/m", now, func(e []event) []event {
		return append(e, event{TS: now.UnixNano(), Tokens: 100})
	})
	g2 := &globalStore{path: g.path, lock: g.lock} // "restart"
	if len(g2.read().Events["p/m"]) != 1 {
		t.Fatal("state not persisted across reopen")
	}
}
```

---

## 5. T3 — `internal/ratelimit/registry.go` (Limiter + Registry)

```go
package ratelimit

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/voocel/agentcore"
)

// Registry là singleton toàn tiến trình: giữ Limits + Limiter per (provider,model),
// chia sẻ 1 globalStore. Khởi tạo lười ở lần Register/For đầu.
type Registry struct {
	mu       sync.Mutex
	store    *globalStore
	limiters map[string]*Limiter
	nowFn    func() time.Time // test override
}

var Global = &Registry{
	limiters: map[string]*Limiter{},
	nowFn:    time.Now,
}

func key(provider, model string) string { return provider + "/" + model }

// Register khai báo giới hạn cho 1 (provider,model). Gọi lúc dựng model. An toàn gọi lại.
func (r *Registry) Register(provider, model string, lim Limits) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStore()
	k := key(provider, model)
	if l, ok := r.limiters[k]; ok {
		l.limits = lim // cập nhật nếu đổi config
		l.resize(lim.MaxConcurrent)
		return
	}
	r.limiters[k] = newLimiter(k, lim, r.store, r.nowFn)
}

// For trả về Limiter của (provider,model); nếu chưa Register thì tạo Limiter không giới hạn
// (mọi field 0) — an toàn, chỉ đếm.
func (r *Registry) For(provider, model string) *Limiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ensureStore()
	k := key(provider, model)
	if l, ok := r.limiters[k]; ok {
		return l
	}
	l := newLimiter(k, Limits{}, r.store, r.nowFn)
	r.limiters[k] = l
	return l
}

func (r *Registry) ensureStore() {
	if r.store != nil {
		return
	}
	s, err := newGlobalStore()
	if err != nil {
		slog.Warn("ratelimit: không mở được store global, hạ cấp về đếm rỗng", "err", err)
		return
	}
	r.store = s
}

// Limiter quản lý 1 key. sem = concurrency in-process; cooldownUntil = 429 in-process.
type Limiter struct {
	k       string
	limits  Limits
	store   *globalStore
	nowFn   func() time.Time

	mu            sync.Mutex
	sem           chan struct{}
	cooldownUntil time.Time
	consecutive429 int
}

func newLimiter(k string, lim Limits, store *globalStore, nowFn func() time.Time) *Limiter {
	l := &Limiter{k: k, limits: lim, store: store, nowFn: nowFn}
	l.resize(lim.MaxConcurrent)
	return l
}

func (l *Limiter) resize(n int) {
	if n <= 0 {
		l.sem = nil
		return
	}
	if cap(l.sem) == n {
		return
	}
	l.sem = make(chan struct{}, n)
}

// Handle đại diện 1 slot đã chiếm. PHẢI gọi Record đúng 1 lần.
type Handle struct {
	l         *Limiter
	tsNano    int64
	tookSem   bool
}

// peek báo retryAfter hiện tại (0 = sẵn), KHÔNG chiếm slot, KHÔNG ghi file. Chỉ đọc.
// Dùng để tính thời gian chờ trong Acquire + so sánh giữa các target lúc rotation.
func (l *Limiter) peek(estTokens int) time.Duration {
	l.mu.Lock()
	now := l.nowFn()
	if now.Before(l.cooldownUntil) {
		d := l.cooldownUntil.Sub(now)
		l.mu.Unlock()
		return d
	}
	if l.sem != nil && len(l.sem) >= cap(l.sem) {
		l.mu.Unlock()
		return 50 * time.Millisecond
	}
	l.mu.Unlock()
	if l.store == nil {
		return 0
	}
	return l.store.peekKey(l.k, now, l.limits, estTokens)
}

// TryAcquire chiếm slot NGUYÊN TỬ nếu còn quota; ok=false + retryAfter nếu chặn.
// Check + append gộp trong MỘT lần lock global → không TOCTOU cross-process (không double-spend).
func (l *Limiter) TryAcquire(estTokens int) (*Handle, time.Duration, bool) {
	l.mu.Lock()
	now := l.nowFn()
	if now.Before(l.cooldownUntil) {
		d := l.cooldownUntil.Sub(now)
		l.mu.Unlock()
		return nil, d, false
	}
	l.mu.Unlock()

	// Chiếm sem in-process (non-block); đầy → chặn.
	if l.sem != nil {
		select {
		case l.sem <- struct{}{}:
		default:
			return nil, 50 * time.Millisecond, false
		}
	}

	tsNano := now.UnixNano()
	ok := l.store == nil // store nil = fail-open, luôn cho qua
	var ra time.Duration
	if l.store != nil {
		_ = l.store.withKey(l.k, now, func(evs []event) []event {
			if d := check(evs, l.limits, now, estTokens); d > 0 {
				ra = d
				return evs // không đủ quota, không ghi
			}
			ok = true
			return append(evs, event{TS: tsNano, Tokens: estTokens})
		})
	}
	if !ok {
		if l.sem != nil {
			<-l.sem // nhả sem đã chiếm hụt
		}
		return nil, ra, false
	}
	return &Handle{l: l, tsNano: tsNano, tookSem: l.sem != nil}, 0, true
}

// Acquire block-and-wait tới khi chiếm được slot hoặc ctx hủy.
func (l *Limiter) Acquire(ctx context.Context, estTokens int) (*Handle, error) {
	for {
		if h, _, ok := l.TryAcquire(estTokens); ok {
			return h, nil
		}
		ra := l.peek(estTokens)
		if ra <= 0 {
			continue // vừa có slot (race), thử lại ngay
		}
		if ra > 5*time.Second {
			ra = 5 * time.Second // ngủ ngắn rồi kiểm lại (cửa sổ có thể hồi sớm)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(ra):
		}
	}
}

// Record cập nhật token thực + xử lý 429; nhả sem. err=nil → reset cooldown streak.
func (h *Handle) Record(actualTokens int, err error) {
	if h == nil {
		return
	}
	l := h.l
	if h.tookSem {
		<-l.sem
	}

	// Cập nhật token thực cho event đã reserved (chỉ ảnh hưởng TPM).
	if l.store != nil && actualTokens > 0 {
		_ = l.store.withKey(l.k, l.nowFn(), func(evs []event) []event {
			for i := range evs {
				if evs[i].TS == h.tsNano {
					evs[i].Tokens = actualTokens
					break
				}
			}
			return evs
		})
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil && agentcore.FailoverReason(err) == "rate_limit" {
		l.consecutive429++
		backoff := time.Duration(30*(1<<uint(min(l.consecutive429-1, 3)))) * time.Second // 30,60,120,240
		if backoff > 300*time.Second {
			backoff = 300 * time.Second
		}
		l.cooldownUntil = l.nowFn().Add(backoff)
		slog.Warn("ratelimit: 429, đặt cooldown", "key", l.k, "backoff", backoff)
	} else if err == nil || !errors.Is(err, context.Canceled) {
		l.consecutive429 = 0
	}
}

func min(a, b int) int { if a < b { return a }; return b }
```

> Ghi chú: `available` gọi `withKey` để preview có tốn 1 lần lock/read. Với volume thấp (giây/request)
> chấp nhận được. Nếu muốn nhẹ hơn, thêm cache RAM TTL 500ms — KHÔNG cần cho MVP.

### Test T3 — `registry_test.go` (rút gọn, thêm khi thực thi)
- `TestLimiter_TryAcquire_BlockThenOpen`: RPM=1, acquire 1 → TryAcquire thứ 2 ok=false, retryAfter>0.
- `TestLimiter_Cooldown429`: Record với lỗi rate_limit → available()>0 trong ~30s.
- `TestLimiter_Concurrency`: MaxConcurrent=1 → 2 commit song song, cái 2 chờ sem.
- Inject `nowFn` qua `Global.nowFn` hoặc tạo Limiter trực tiếp với clock giả.

---

## 6. T4 — `internal/bootstrap/config.go`

Thêm struct + field vào `ProviderConfig` (sau dòng 62, trước `RequiresAPIKey`):

```go
// RateLimitConfig là giới hạn tần suất cho provider (mặc định) hoặc 1 model cụ thể.
// 0 = không giới hạn chiều đó. RPD dùng cửa sổ cuộn 24h.
type RateLimitConfig struct {
	RPM           int `json:"rpm,omitempty"`            // requests/phút
	RPD           int `json:"rpd,omitempty"`            // requests/24h cuộn
	TPM           int `json:"tpm,omitempty"`            // tokens/phút
	MaxConcurrent int `json:"max_concurrent,omitempty"` // trần song song (in-process)
}
```

Trong `ProviderConfig` thêm 2 field:
```go
	// RateLimit áp cho mọi model của provider này (mặc định).
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`
	// ModelLimits override theo từng model (key = tên model), phủ lên RateLimit theo từng field.
	ModelLimits map[string]RateLimitConfig `json:"model_limits,omitempty"`
```

Helper resolve (thêm cuối file config.go hoặc trong models.go):
```go
// EffectiveRateLimit ghép RateLimit provider + override model theo từng field.
func (pc ProviderConfig) EffectiveRateLimit(model string) RateLimitConfig {
	var out RateLimitConfig
	if pc.RateLimit != nil {
		out = *pc.RateLimit
	}
	if ov, ok := pc.ModelLimits[model]; ok {
		if ov.RPM > 0 {
			out.RPM = ov.RPM
		}
		if ov.RPD > 0 {
			out.RPD = ov.RPD
		}
		if ov.TPM > 0 {
			out.TPM = ov.TPM
		}
		if ov.MaxConcurrent > 0 {
			out.MaxConcurrent = ov.MaxConcurrent
		}
	}
	return out
}
```

> Kiểm tra merge config: nếu repo có `MergeConfig`/`configfile_test.go` (thấy `TestMergeConfig_ProviderExtraFields`),
> đảm bảo `RateLimit`/`ModelLimits` được giữ khi merge. Map + pointer struct thường copy ổn;
> thêm 1 case test `TestMergeConfig_RateLimit` nếu merge làm shallow field-by-field.

---

## 7. T5 — `internal/bootstrap/models.go` (thay code cũ + rotation)

### 7a. XOÁ hoàn toàn khối cũ (dòng 459-577):
`var (limitersMu, limiters)`, `type modelRateLimiter`, `getRateLimiter`, `Wait`, `RecordResult`,
`type rateLimitedModel` + 2 method Generate/GenerateStream cũ. Thay bằng bản mới dưới.

### 7b. `modelTarget` mang thêm limiter key (đã có provider+name → tra registry runtime, KHÔNG cần field mới).

### 7c. `createModelFromConfig` — Register limits + bọc rateLimitedModel mới:
```go
func createModelFromConfig(providerKey, model string, pc ProviderConfig, cache map[string]agentcore.ChatModel) (agentcore.ChatModel, error) {
	cacheKey := providerKey + "|" + model
	if m, ok := cache[cacheKey]; ok {
		return m, nil
	}
	providerType, err := pc.ProviderType(providerKey)
	if err != nil {
		return nil, fmt.Errorf("phân tích kiểu nhà cung cấp thất bại: %w", err)
	}
	m, err := llm.NewModel(providerType, model,
		llm.WithAPIKey(pc.APIKey),
		llm.WithBaseURL(pc.BaseURL),
		llm.WithStreamIdleTimeout(streamIdleTimeout),
		llm.WithProviderExtra(pc.Extra),
		llm.WithExtra(pc.ExtraBody),
	)
	if err != nil {
		return nil, fmt.Errorf("provider %s (%s): %w: %w", providerKey, providerType, errs.ErrProvider, err)
	}

	// Đăng ký giới hạn hiệu lực + bọc lớp rate limit (single-target block-and-wait).
	eff := pc.EffectiveRateLimit(model)
	ratelimit.Global.Register(providerKey, model, ratelimit.Limits{
		RPM: eff.RPM, RPD: eff.RPD, TPM: eff.TPM, MaxConcurrent: eff.MaxConcurrent,
	})
	rlModel := &rateLimitedModel{ChatModel: m, provider: providerKey, model: model}
	cache[cacheKey] = rlModel
	return rlModel, nil
}
```

### 7d. `rateLimitedModel` mới (single-target: block-and-wait qua registry):
```go
type rateLimitedModel struct {
	agentcore.ChatModel
	provider string
	model    string
}

func (m *rateLimitedModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	lim := ratelimit.Global.For(m.provider, m.model)
	h, err := lim.Acquire(ctx, estimateTokens(messages))
	if err != nil {
		return nil, err
	}
	resp, gerr := m.ChatModel.Generate(ctx, messages, tools, opts...)
	h.Record(responseTokens(resp), gerr)
	return resp, gerr
}

func (m *rateLimitedModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	lim := ratelimit.Global.For(m.provider, m.model)
	h, err := lim.Acquire(ctx, estimateTokens(messages))
	if err != nil {
		return nil, err
	}
	source, serr := m.ChatModel.GenerateStream(ctx, messages, tools, opts...)
	if serr != nil {
		h.Record(0, serr)
		return nil, serr
	}
	out := make(chan agentcore.StreamEvent, 100)
	go func() {
		defer close(out)
		var streamErr error
		var toks int
		for ev := range source {
			if ev.Type == agentcore.StreamEventError {
				streamErr = ev.Err
			}
			if ev.Type == agentcore.StreamEventDone && ev.Message.Usage != nil {
				toks = usageTokens(ev.Message.Usage)
			}
			out <- ev
		}
		h.Record(toks, streamErr)
	}()
	return out, nil
}
```

### 7e. Helper token (thêm vào models.go):
```go
// estimateTokens ước lượng thô token đầu vào để pre-gate TPM (≈ 4 ký tự/token).
func estimateTokens(messages []agentcore.Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Content) // Message.Content là string; nếu có parts, cộng thêm
	}
	return chars / 4
}

func responseTokens(resp *agentcore.LLMResponse) int {
	if resp == nil || resp.Message.Usage == nil {
		return 0
	}
	return usageTokens(resp.Message.Usage)
}

func usageTokens(u *agentcore.Usage) int {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output
}
```
> Kiểm tra field `agentcore.Message.Content`: nếu KHÔNG phải string thuần (multi-part), đổi
> `estimateTokens` sang `len(fmt.Sprint(msg))/4` hoặc marshal JSON. Xác nhận lúc thực thi.

### 7f. `failoverModel` — ROTATION (thay Generate + GenerateStream):
Mấu chốt: trước khi gửi, chọn target ĐẦU TIÊN còn quota theo thứ tự `[primary, fallbacks...]`
(primary trước = tự quay lại model gốc khi hồi). Hết sạch → chờ min(retryAfter) rồi Acquire.

```go
// rotationTargets trả về danh sách target theo thứ tự ưu tiên (primary trước).
func (m *failoverModel) rotationTargets() []modelTarget {
	targets := []modelTarget{m.currentTarget()}
	for _, t := range m.fallbacks {
		if t.provider == targets[0].provider && t.name == targets[0].name {
			continue
		}
		targets = append(targets, t)
	}
	return targets
}

// pickAvailable chọn target đầu còn quota (TryAcquire ok). Nếu hết → chờ min(retryAfter)
// của cả nhóm rồi Acquire target rẻ nhất. Trả về target + handle đã chiếm slot.
func (m *failoverModel) pickAvailable(ctx context.Context, est int) (modelTarget, *ratelimit.Handle, error) {
	targets := m.rotationTargets()
	var bestRA time.Duration = 1<<62
	var bestIdx int
	for i, t := range targets {
		lim := ratelimit.Global.For(t.provider, t.name)
		if h, ra, ok := lim.TryAcquire(est); ok {
			return t, h, nil
		} else if ra < bestRA {
			bestRA, bestIdx = ra, i
		}
	}
	// Tất cả chạm limit → chờ target rẻ nhất.
	t := targets[bestIdx]
	slog.Info("ratelimit: mọi model chạm giới hạn, chờ target rẻ nhất",
		"role", m.role, "target", t.provider+"/"+t.name, "wait", bestRA)
	h, err := ratelimit.Global.For(t.provider, t.name).Acquire(ctx, est)
	return t, h, err
}

func (m *failoverModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	est := estimateTokens(messages)
	origin := m.currentTarget()

	target, h, err := m.pickAvailable(ctx, est)
	if err != nil {
		return nil, err
	}
	if target.provider != origin.provider || target.name != origin.name {
		m.reportFailover(origin, target, "rate_limit", nil)
	}
	resp, gerr := target.model.Generate(ctx, messages, tools, opts...)
	h.Record(responseTokens(resp), gerr)
	if gerr == nil {
		return resp, nil
	}

	// Lỗi vận hành (không phải quota) → path failover cũ 1 lần.
	if next, reason, ok := m.pickFallback(target, gerr); ok {
		m.reportFailover(target, next, reason, gerr)
		nh, aerr := ratelimit.Global.For(next.provider, next.name).Acquire(ctx, est)
		if aerr != nil {
			return nil, aerr
		}
		r2, e2 := next.model.Generate(ctx, messages, tools, opts...)
		nh.Record(responseTokens(r2), e2)
		return r2, e2
	}
	return nil, gerr
}
```

> `GenerateStream` của failoverModel: giữ khung goto-retry cũ (dòng 315-372) NHƯNG thay bước
> chọn target đầu bằng `pickAvailable`, và bọc `h.Record(...)` khi stream kết thúc/lỗi. Vì stream
> phức tạp, ưu tiên: (a) chọn target qua pickAvailable; (b) chiếm handle; (c) forward events;
> (d) khi gặp StreamEventError lần đầu → Record(err) + thử pickFallback 1 lần (Acquire handle mới);
> (e) khi Done → Record(tokens từ ev.Message.Usage). Viết cẩn thận, thêm test stream riêng.

### 7g. Import thêm: `"github.com/voocel/ainovel-cli/internal/ratelimit"` + `"time"` (đã có), `"log/slog"` (đã có).

---

## 8. T6 — `config.example.jsonc`

Thêm ví dụ trong block provider (giữ comment tiếng Việt, jsonc cho phép comment):
```jsonc
  "providers": {
    "openrouter": {
      "api_key": "sk-...",
      // Rate limit mặc định cho MỌI model của provider này. 0/bỏ trống = không giới hạn chiều đó.
      // RPD dùng cửa sổ cuộn 24h. Counter global toàn máy (mọi dự án chung) + sống qua restart.
      "rate_limit": { "rpm": 20, "rpd": 200, "tpm": 100000, "max_concurrent": 2 },
      // Override từng model (phủ lên rate_limit theo từng field). Đặt chặt cho free tier.
      "model_limits": {
        "google/gemini-2.5-pro":   { "rpm": 5,  "rpd": 100, "tpm": 60000 },
        "google/gemini-2.5-flash": { "rpm": 10, "rpd": 200 }
      }
    }
  }
```

---

## 9. T7 — Verify

```bash
go build ./...
go test ./internal/ratelimit/... ./internal/bootstrap/...
go vet ./internal/ratelimit/...
```
Smoke thủ công:
1. Đặt `rate_limit.rpm=2` cho 1 provider, chạy vài chương → log phải thấy chờ giãn cách + (nếu có fallback) `reportFailover reason=rate_limit`.
2. Kiểm file `%AppData%\ainovel-cli\ratelimit.json` có event, prune sau 24h.
3. Restart giữa chừng → counter không reset (RPD giữ nguyên).
4. Chạy 2 tiến trình cùng lúc → không double-spend (tổng request/phút ≤ rpm).

---

## 10. Gotchas / rủi ro (đọc trước khi code)

1. **`agentcore.Message.Content` kiểu gì?** Nếu multi-part (không phải string), `estimateTokens`
   phải đổi cách đo. XÁC NHẬN đầu tiên (`go doc github.com/voocel/agentcore.Message`).
2. **Retry-After không có** → cooldown 429 cố định exponential. Nếu sau này agentcore expose header,
   nối vào `Handle.Record`.
3. **`TryAcquire`/`peek` tốn 1 lock/lần** → rotation duyệt N target = tối đa N lock. Volume thấp OK.
   Nếu nóng, thêm cache RAM TTL 500ms trong Limiter. Lưu ý pickAvailable duyệt loser bằng TryAcquire
   (chiếm hụt rồi nhả ngay) — chấp nhận được ở volume này; nếu muốn thuần đọc, đổi loser sang `peek`.
4. **Cooldown 429 + concurrency in-process** (không global) — cố ý. Chỉ RPM/RPD/TPM global.
5. **os.Rename replace trên Windows**: Go xử lý được (MoveFileEx). Nhưng nếu `ratelimit.json` bị
   AV/OneDrive lock → write fail; withKey nuốt lỗi (fail-open, chỉ warn) để KHÔNG chặn sáng tác.
6. **Lockfile stale 10s**: nếu 1 request thực > 10s giữ lock? KHÔNG — lock chỉ giữ lúc read/update
   (mili giây), nhả ngay, KHÔNG giữ suốt LLM call. Đảm bảo `withKey` không bọc network call.
7. **fail-open toàn cục**: store lỗi/nil → Limiter chỉ đếm rỗng, không chặn. Chống ban là phụ trợ,
   tuyệt đối không để rate limit làm kẹt luồng viết.
8. **min() Go 1.21+**: repo có thể đã có builtin `min`; nếu trùng khai báo, bỏ helper `min` cục bộ.

---

## 11. Định nghĩa hoàn thành

- [ ] `go build ./...` + `go test ./...` xanh.
- [ ] 4 giới hạn RPM/RPD/TPM/MaxConcurrent áp đúng từ config, override model hoạt động.
- [ ] Rotation chọn primary trước, xoay fallback khi chạm limit, quay lại primary khi hồi.
- [ ] Counter global (`%AppData%\ainovel-cli\ratelimit.json`) sống qua restart + chia sẻ 2 tiến trình.
- [ ] 429 tách khỏi lỗi vận hành (cooldown vs failover path).
- [ ] fail-open: mọi lỗi store KHÔNG chặn sáng tác.
