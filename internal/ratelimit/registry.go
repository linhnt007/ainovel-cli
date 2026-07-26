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
	k      string
	limits Limits
	store  *globalStore
	nowFn  func() time.Time

	mu             sync.Mutex
	sem            chan struct{}
	cooldownUntil  time.Time
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
	l       *Limiter
	tsNano  int64
	tookSem bool
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
