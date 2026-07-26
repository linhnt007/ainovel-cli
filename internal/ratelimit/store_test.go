package ratelimit

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *globalStore {
	dir := t.TempDir()
	return &globalStore{
		path:        filepath.Join(dir, "rl.json"),
		lock:        filepath.Join(dir, "rl.lock"),
		lockStale:   defaultLockStale,
		lockSpin:    defaultLockSpin,
		lockTimeout: defaultLockTimeout,
	}
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

// TestStore_WritePrunesStaleKeys là regression test cho fix wave cuối: withKey/write trước
// đây chỉ prune key đang truy cập, nên key không còn dùng (model đổi tên, key test random)
// sống mãi trong ratelimit.json, file phình đơn điệu theo thời gian. Sau fix, write() phải
// prune MỌI key theo "now" và xoá hẳn key có event list rỗng sau prune — không chỉ key
// đang ghi trong lần gọi withKey đó.
func TestStore_WritePrunesStaleKeys(t *testing.T) {
	g := testStore(t)
	now := time.Unix(1_000_000, 0)

	// "old/model" chỉ có event >24h so với "now" ở dưới — phải bị prune sạch + xoá key.
	stale := now.Add(-25 * time.Hour)
	_ = g.withKey("old/model", stale, func(e []event) []event {
		return append(e, event{TS: stale.UnixNano(), Tokens: 10})
	})
	if _, ok := g.read().Events["old/model"]; !ok {
		t.Fatal("setup: old/model phải tồn tại trước khi prune")
	}

	// withKey trên MỘT key khác tại "now" (>24h sau event của old/model) phải kích hoạt
	// prune toàn cục trong write(), khiến old/model biến mất khỏi file.
	_ = g.withKey("new/model", now, func(e []event) []event {
		return append(e, event{TS: now.UnixNano(), Tokens: 1})
	})

	st := g.read()
	if _, ok := st.Events["old/model"]; ok {
		t.Fatalf("old/model vẫn còn trong store sau khi write prune toàn cục: %v", st.Events["old/model"])
	}
	if len(st.Events["new/model"]) != 1 {
		t.Fatalf("new/model phải còn 1 event, got %d", len(st.Events["new/model"]))
	}
}

// TestAcquireLock_StaleRemoveFailsRespectsDeadline là regression test cho review finding
// #2 (Critical): trước fix, nhánh stale-lock gọi "continue" ngay sau os.Remove mà không
// check deadline — nếu remove thất bại (lock là 1 thư mục non-empty, mô phỏng AV/OneDrive
// giữ handle không cho xoá trên Windows) thì acquireLock busy-spin vô hạn, bỏ qua
// lockTimeout hoàn toàn. Test dùng lockTimeout/lockStale/lockSpin ngắn (field override,
// không phải hằng số sản xuất 10s/5s) để deterministic và nhanh.
//
// Test chạy acquireLock trong goroutine + select-timeout riêng (gấp đôi lockTimeout) để
// nếu code hồi quy về hành vi cũ (busy-spin vô hạn), test fail rõ ràng thay vì treo cả
// suite test mãi mãi.
func TestAcquireLock_StaleRemoveFailsRespectsDeadline(t *testing.T) {
	dir := t.TempDir()
	g := &globalStore{
		path:        filepath.Join(dir, "rl.json"),
		lock:        filepath.Join(dir, "rl.lock"),
		lockStale:   50 * time.Millisecond,
		lockSpin:    5 * time.Millisecond,
		lockTimeout: 300 * time.Millisecond,
	}

	// Lock "stale" nhưng không thể xoá: tạo thành 1 thư mục chứa file con — os.Remove
	// trên thư mục non-empty luôn lỗi (cả Windows lẫn POSIX), mô phỏng tình huống
	// AV/OneDrive giữ handle khiến remove thất bại mà không báo panic.
	if err := os.Mkdir(g.lock, 0o755); err != nil {
		t.Fatalf("setup mkdir lock dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(g.lock, "busy"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup write busy file: %v", err)
	}
	// Backdate mtime để lock bị coi là "stale" (> lockStale) ngay từ vòng lặp đầu.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(g.lock, old, old); err != nil {
		t.Fatalf("setup chtimes: %v", err)
	}

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		err := g.acquireLock()
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	select {
	case res := <-done:
		if res.err == nil {
			t.Fatal("expect timeout error khi lock không thể xoá được, got nil (acquired?!)")
		}
		if res.elapsed > g.lockTimeout+500*time.Millisecond {
			t.Fatalf("acquireLock không tôn trọng deadline: elapsed=%v want<=~%v", res.elapsed, g.lockTimeout)
		}
	case <-time.After(g.lockTimeout + 2*time.Second):
		t.Fatalf("acquireLock treo quá deadline (lockTimeout=%v) — nhánh stale-lock đang bỏ qua check deadline (regression finding #2)", g.lockTimeout)
	}
}
