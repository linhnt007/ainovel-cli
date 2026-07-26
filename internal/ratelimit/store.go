package ratelimit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/ainovel-cli/internal/errs"
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
		return nil, fmt.Errorf("resolve config dir: %w: %w", errs.ErrConfig, err)
	}
	base := filepath.Join(dir, "ainovel-cli")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w: %w", base, errs.ErrStoreWrite, err)
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
			return fmt.Errorf("acquire ratelimit lock timeout: %w", errs.ErrStoreWrite)
		}
		time.Sleep(lockSpin)
	}
}

func (g *globalStore) releaseLock() { _ = os.Remove(g.lock) }

// withKey chạy fn dưới lock global, cấp cho fn slice event của 1 key (đã prune),
// và ghi lại kết quả fn trả về. now truyền vào để test xác định.
// Lỗi khoá/ghi được nuốt ở đây theo nguyên tắc fail-open (chỉ cảnh báo log, KHÔNG
// chặn luồng sáng tác) — caller (Limiter) không cần kiểm tra lỗi trả về, chỉ cần biết
// việc đếm quota có thể bị bỏ lỡ trong tình huống hiếm (AV/OneDrive khoá file...).
func (g *globalStore) withKey(key string, now time.Time, fn func(evs []event) []event) error {
	if err := g.acquireLock(); err != nil {
		slog.Warn("ratelimit: không lấy được khoá store, fail-open (bỏ qua đếm lần này)", "key", key, "err", err)
		return err
	}
	defer g.releaseLock()

	st := g.read()
	evs := prune(st.Events[key], now)
	st.Events[key] = fn(evs)
	if err := g.write(st); err != nil {
		slog.Warn("ratelimit: ghi store thất bại, fail-open (không chặn sáng tác)", "key", key, "err", err)
		return err
	}
	return nil
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
		return fmt.Errorf("marshal ratelimit store: %w: %w", errs.ErrStoreWrite, err)
	}
	tmp := g.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w: %w", tmp, errs.ErrStoreWrite, err)
	}
	if err := os.Rename(tmp, g.path); err != nil { // Go os.Rename replace-existing cả trên Windows (MoveFileEx)
		return fmt.Errorf("rename %s -> %s: %w: %w", tmp, g.path, errs.ErrStoreWrite, err)
	}
	return nil
}
