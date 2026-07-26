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
