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
