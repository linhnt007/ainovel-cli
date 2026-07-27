package domain

import "testing"

func TestContextProfileSmallWindow(t *testing.T) {
	big := NewContextProfileForWindow(100, 200_000)
	small := NewContextProfileForWindow(100, 24_000)
	if small.SummaryWindow >= big.SummaryWindow {
		t.Fatalf("window nho phai rut summary: %d vs %d", small.SummaryWindow, big.SummaryWindow)
	}
	if small.SummaryWindow < 2 {
		t.Fatalf("san toi thieu 2 chuong: %d", small.SummaryWindow)
	}
	if !small.Compact {
		t.Fatalf("cờ Compact phải được bật cho cửa sổ nhỏ")
	}
	if big.Compact {
		t.Fatalf("cờ Compact không được bật cho cửa sổ lớn")
	}
}
