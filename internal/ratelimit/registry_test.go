package ratelimit

import (
	"errors"
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
