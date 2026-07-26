package ratelimit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

// fakeClock cấp đồng hồ giả có thể tua tới cho test, tránh sleep thật dài.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestLimiter(t *testing.T, lim Limits, clock *fakeClock) *Limiter {
	t.Helper()
	store := testStore(t)
	return newLimiter("p/m", lim, store, clock.now)
}

// TestLimiter_TryAcquire_BlockThenOpen: RPM=1, request thứ 2 trong cùng cửa sổ 60s
// phải bị chặn (ok=false, retryAfter>0); sau khi tua đồng hồ qua cửa sổ, slot mở lại.
func TestLimiter_TryAcquire_BlockThenOpen(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newTestLimiter(t, Limits{RPM: 1}, clock)

	h1, ra1, ok1 := l.TryAcquire(0)
	if !ok1 || ra1 != 0 {
		t.Fatalf("expect first acquire ok, got ok=%v ra=%v", ok1, ra1)
	}
	h1.Record(0, nil)

	if _, ra2, ok2 := l.TryAcquire(0); ok2 || ra2 <= 0 {
		t.Fatalf("expect second acquire blocked, got ok=%v ra=%v", ok2, ra2)
	}

	// Tua đồng hồ qua cửa sổ RPM (60s) → slot phải mở lại.
	clock.advance(61 * time.Second)
	if _, _, ok3 := l.TryAcquire(0); !ok3 {
		t.Fatal("expect acquire ok after window passed")
	}
}

// TestLimiter_Cooldown429: Record với lỗi mà agentcore.FailoverReason phân loại là
// "rate_limit" phải đặt cooldown ~30s (backoff bậc đầu); trong cooldown mọi TryAcquire
// bị chặn, sau khi tua đồng hồ qua cooldown thì mở lại.
func TestLimiter_Cooldown429(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newTestLimiter(t, Limits{}, clock)

	h, _, ok := l.TryAcquire(0)
	if !ok {
		t.Fatal("expect first acquire ok")
	}

	// Lỗi giả có message mà agentcore.ClassifyProvider phân loại là rate limit
	// (khớp pattern "too many requests" / "429" trong errors.go của agentcore) —
	// dùng FailoverReason thật, không cần hook riêng vì phân loại dựa trên chuỗi lỗi.
	rateLimitErr := errors.New("provider responded 429: too many requests")
	if got := agentcore.FailoverReason(rateLimitErr); got != "rate_limit" {
		t.Fatalf("precondition failed: expect fake err classified as rate_limit, got %q", got)
	}
	h.Record(0, rateLimitErr)

	if _, ra, ok := l.TryAcquire(0); ok || ra <= 0 || ra > 31*time.Second {
		t.Fatalf("expect blocked by cooldown ~30s, got ok=%v ra=%v", ok, ra)
	}

	clock.advance(31 * time.Second)
	if _, _, ok := l.TryAcquire(0); !ok {
		t.Fatal("expect acquire ok after cooldown passed")
	}
}

// TestLimiter_Concurrency: MaxConcurrent=1 → chiếm slot thứ 2 khi slot đầu chưa nhả
// phải bị chặn; goroutine chờ chỉ chiếm được sau khi handle đầu gọi Record để nhả sem.
func TestLimiter_Concurrency(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newTestLimiter(t, Limits{MaxConcurrent: 1}, clock)

	h1, _, ok := l.TryAcquire(0)
	if !ok {
		t.Fatal("expect first acquire ok")
	}
	if _, ra, ok := l.TryAcquire(0); ok || ra <= 0 {
		t.Fatalf("expect second acquire blocked by semaphore, got ok=%v ra=%v", ok, ra)
	}

	acquired := make(chan struct{})
	release := make(chan struct{})
	go func() {
		for {
			if h2, _, ok := l.TryAcquire(0); ok {
				close(acquired)
				<-release
				h2.Record(0, nil)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	select {
	case <-acquired:
		t.Fatal("goroutine acquired slot before first was released")
	case <-time.After(30 * time.Millisecond):
	}

	h1.Record(0, nil) // nhả slot đầu

	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never acquired slot after release")
	}
	close(release)
}

// TestLimiter_TryAcquire_StoreLockFailure_FailsOpen là regression test cho review finding
// #1 (Critical): trước fix, khi store.withKey lỗi TRƯỚC khi callback chạy (vd acquireLock
// timeout vì lockfile bị tiến trình khác/AV/OneDrive giữ), TryAcquire trả ok=false mà
// KHÔNG hề chiếm được quota thật — tức chặn request oan trong khi Global Constraint của
// plan yêu cầu "fail-open: mọi lỗi store KHÔNG bao giờ chặn sáng tác". Reviewer verify thực
// nghiệm: lock tươi có sẵn → TryAcquire mất 5.02s rồi ok=false. Test dùng lockTimeout ngắn
// (field override) để deterministic + nhanh, giả lập lock "tươi" (không stale) bị tiến
// trình khác giữ, rồi assert TryAcquire vẫn fail-open (ok=true, handle hợp lệ) trong
// khoảng ~lockTimeout, không chặn vô thời hạn.
func TestLimiter_TryAcquire_StoreLockFailure_FailsOpen(t *testing.T) {
	dir := t.TempDir()
	store := &globalStore{
		path:        filepath.Join(dir, "rl.json"),
		lock:        filepath.Join(dir, "rl.lock"),
		lockStale:   time.Hour, // không bao giờ coi là stale trong test này — ép timeout thật
		lockSpin:    5 * time.Millisecond,
		lockTimeout: 80 * time.Millisecond,
	}
	// Giả lập tiến trình khác đang giữ lock: file "tươi" (mtime vừa tạo, chưa quá lockStale)
	// → acquireLock bên trong TryAcquire phải timeout thật sau ~lockTimeout, không phải nhánh
	// stale-remove.
	f, err := os.OpenFile(store.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("setup: tạo lock giả thất bại: %v", err)
	}
	_ = f.Close()

	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newLimiter("p/m", Limits{RPM: 1}, store, clock.now)

	start := time.Now()
	h, ra, ok := l.TryAcquire(0)
	elapsed := time.Since(start)

	if !ok || h == nil {
		t.Fatalf("expect fail-open (ok=true, handle non-nil) khi store lock timeout, got ok=%v ra=%v elapsed=%v", ok, ra, elapsed)
	}
	if elapsed > store.lockTimeout+500*time.Millisecond {
		t.Fatalf("TryAcquire bị chặn quá lâu dù chính sách fail-open: elapsed=%v (lockTimeout=%v) — regression finding #1", elapsed, store.lockTimeout)
	}
	h.Record(0, nil)
}

// TestLimiter_Acquire_RespectsCanceledContext là regression test cho review finding #3
// (Critical): trước fix, Acquire chỉ check ctx.Done() bên trong nhánh select sau khi đã
// tính ra retryAfter>0 — nhánh fast-path "ra<=0 { continue }" bỏ qua ctx hoàn toàn, và nếu
// ctx đã bị huỷ TRƯỚC khi gọi Acquire, code cũ vẫn thử TryAcquire trước (có thể acquire
// thành công nếu slot còn trống) — không tôn trọng cancel ngay từ đầu. Sau fix, ctx.Err()
// được check ở ĐẦU mọi vòng lặp, nên Acquire với ctx đã huỷ phải trả lỗi ngay, không chiếm
// slot dù slot đang trống.
func TestLimiter_Acquire_RespectsCanceledContext(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_000_000, 0)}
	l := newTestLimiter(t, Limits{MaxConcurrent: 1}, clock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // huỷ TRƯỚC khi gọi Acquire — slot vẫn đang trống (chưa ai chiếm)

	h, err := l.Acquire(ctx, 0)
	if h != nil || err == nil {
		t.Fatalf("expect nil handle + non-nil err với ctx đã huỷ, got h=%v err=%v", h, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expect context.Canceled, got %v", err)
	}

	// Slot phải còn nguyên (Acquire không được lặt vặt chiếm rồi bỏ khi ctx đã huỷ) —
	// nếu fix đúng, TryAcquire trực tiếp vẫn phải ok vì Acquire chưa từng đụng vào sem.
	h2, _, ok := l.TryAcquire(0)
	if !ok {
		t.Fatal("expect slot vẫn trống sau khi Acquire trả về sớm do ctx đã huỷ — regression finding #3")
	}
	h2.Record(0, nil)
}
