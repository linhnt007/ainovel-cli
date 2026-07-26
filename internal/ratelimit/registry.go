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
		l.updateLimits(lim) // cập nhật nếu đổi config — tự khoá l.mu bên trong
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
	l.resizeLocked(lim.MaxConcurrent) // object chưa publish ra ngoài, chưa cần khoá
	return l
}

// updateLimits cập nhật limits + resize sem dưới l.mu. Gọi từ Registry.Register khi
// key đã sống (có thể đang có TryAcquire/Record chạy song song) — PHẢI khoá l.mu khi
// mutate để tránh race với TryAcquire/Record đọc l.limits/l.sem (finding review #4).
func (l *Limiter) updateLimits(lim Limits) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits = lim
	l.resizeLocked(lim.MaxConcurrent)
}

// resizeLocked thay sem theo MaxConcurrent mới. Caller phải giữ l.mu (hoặc là
// newLimiter dựng object chưa publish). Handle đang outstanding giữ tham chiếu kênh
// sem riêng (Handle.sem, chiếm dưới lúc TryAcquire) nên resize ở đây không làm lệch
// Record của các Handle cũ — chúng vẫn nhả đúng kênh đã chiếm, không phải l.sem mới.
func (l *Limiter) resizeLocked(n int) {
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
	l      *Limiter
	tsNano int64
	// sem lưu trực tiếp kênh đã chiếm lúc TryAcquire (nil nếu không giới hạn
	// concurrency) — KHÔNG đọc lại l.sem lúc Record, vì Register có thể resize
	// (đổi con trỏ channel) đồng thời giữa lúc chiếm và lúc nhả.
	sem chan struct{}
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
	limits := l.limits // snapshot dưới khoá, dùng nhất quán cho lần đọc store bên dưới
	l.mu.Unlock()
	if l.store == nil {
		return 0
	}
	return l.store.peekKey(l.k, now, limits, estTokens)
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
	// Snapshot limits+sem dưới cùng 1 khoá — Register có thể updateLimits (đổi cả 2)
	// đồng thời trên key đang sống; đọc rời rạc ngoài khoá là race (finding review #4).
	limits, sem := l.limits, l.sem
	l.mu.Unlock()

	// Chiếm sem in-process (non-block); đầy → chặn.
	if sem != nil {
		select {
		case sem <- struct{}{}:
		default:
			return nil, 50 * time.Millisecond, false
		}
	}

	tsNano := now.UnixNano()
	ok := l.store == nil // store nil = fail-open, luôn cho qua
	var ra time.Duration
	if l.store != nil {
		ran := false // callback có thực sự chạy không (phân biệt "lỗi trước khi check quota" vs "quota chặn thật")
		storeErr := l.store.withKey(l.k, now, func(evs []event) []event {
			ran = true
			if d := check(evs, limits, now, estTokens); d > 0 {
				ra = d
				return evs // không đủ quota, không ghi
			}
			ok = true
			return append(evs, event{TS: tsNano, Tokens: estTokens})
		})
		if storeErr != nil && !ran {
			// withKey lỗi TRƯỚC khi callback chạy (vd acquireLock timeout do lockfile
			// bị AV/OneDrive giữ) → không có cách nào biết quota thật. Global Constraint
			// của plan thắng code mẫu gốc ở đây: "fail-open: mọi lỗi store KHÔNG bao giờ
			// chặn sáng tác" — PHẢI cho qua, không được trả ok=false (đã verify thực
			// nghiệm: lock timeout 5s rồi chặn request là sai).
			slog.Warn("ratelimit: store lỗi trước khi kiểm tra quota, fail-open (cho qua)", "key", l.k, "err", storeErr)
			ok = true
			ra = 0
		}
	}
	if !ok {
		if sem != nil {
			<-sem // nhả sem đã chiếm hụt
		}
		return nil, ra, false
	}
	return &Handle{l: l, tsNano: tsNano, sem: sem}, 0, true
}

// Acquire block-and-wait tới khi chiếm được slot hoặc ctx hủy.
func (l *Limiter) Acquire(ctx context.Context, estTokens int) (*Handle, error) {
	for {
		// Check ctx ở ĐẦU mọi vòng lặp — kể cả nhánh fast-path "ra<=0 tiếp tục ngay"
		// bên dưới, nếu không sẽ spin vĩnh viễn không tôn trọng cancel khi store hỏng
		// dai dẳng khiến TryAcquire/peek luôn trả về "vừa hết" (finding review #3).
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if h, _, ok := l.TryAcquire(estTokens); ok {
			return h, nil
		}
		ra := l.peek(estTokens)
		if ra <= 0 {
			continue // vừa có slot (race), thử lại ngay — vòng sau vẫn check ctx ở đầu
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
	if h.sem != nil {
		<-h.sem // nhả đúng kênh đã chiếm lúc TryAcquire, không đọc lại l.sem (có thể đã bị resize)
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
