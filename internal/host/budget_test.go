package host

import (
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

type budgetRecorder struct {
	cost    float64
	aborts  []string
	reports []string
}

func (r *budgetRecorder) sentinel(cfg bootstrap.BudgetConfig) *BudgetSentinel {
	return NewBudgetSentinel(cfg,
		func() float64 { return r.cost },
		func(reason string) { r.aborts = append(r.aborts, reason) },
		func(level, summary string) { r.reports = append(r.reports, level+": "+summary) },
	)
}

func subagentEndEvent() agentcore.Event {
	return agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "subagent"}
}

func commitChapterEndEvent() agentcore.Event {
	return agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "commit_chapter"}
}

func TestBudgetSentinelDisabled(t *testing.T) {
	r := &budgetRecorder{}
	if s := r.sentinel(bootstrap.BudgetConfig{}); s != nil {
		t.Fatal("disabled budget should return nil sentinel")
	}
	// an toàn với nil
	var s *BudgetSentinel
	s.OnCost(100)
	s.HandleEvent(subagentEndEvent())
	if err := s.Refuse(); err != nil {
		t.Errorf("nil sentinel Refuse should pass: %v", err)
	}
	if s.Limit() != 0 {
		t.Error("nil sentinel Limit should be 0")
	}
}

func TestBudgetSentinelWarnOnceThenBoundaryStop(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})

	// chưa đến ngưỡng: không có tác dụng phụ
	s.OnCost(5)
	if len(r.reports) != 0 {
		t.Fatalf("below warn ratio should be silent, got %v", r.reports)
	}

	// vượt ngưỡng cảnh báo: đúng một lần warn, gọi lại không phát thêm
	s.OnCost(8.5)
	s.OnCost(9)
	if len(r.reports) != 1 || !strings.HasPrefix(r.reports[0], "warn:") {
		t.Fatalf("expected exactly one warn, got %v", r.reports)
	}

	// vượt giới hạn: vào trạng thái stopPending, phát error, nhưng chưa dừng ngay (mặc định chờ đến ranh giới)
	s.OnCost(10.5)
	if len(r.reports) != 2 || !strings.HasPrefix(r.reports[1], "error:") {
		t.Fatalf("expected error report on exceeding, got %v", r.reports)
	}
	if len(r.aborts) != 0 {
		t.Fatalf("default mode should not abort before boundary, got %v", r.aborts)
	}

	// sự kiện không phải ranh giới thì không kích hoạt
	s.HandleEvent(agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "novel_context"})
	if len(r.aborts) != 0 {
		t.Fatal("non-subagent boundary should not trigger stop")
	}

	// ranh giới SubAgent: đúng một lần dừng, lặp lại ranh giới không dừng thêm
	r.cost = 10.5
	s.HandleEvent(subagentEndEvent())
	s.HandleEvent(subagentEndEvent())
	if len(r.aborts) != 1 {
		t.Fatalf("expected exactly one abort at boundary, got %v", r.aborts)
	}
}

func TestBudgetSentinelJumpStraightPastLimit(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})

	// một lần gọi vượt thẳng qua cả ngưỡng cảnh báo lẫn giới hạn: mỗi loại đúng một lần warn và error
	s.OnCost(12)
	if len(r.reports) != 2 {
		t.Fatalf("expected warn+error in single jump, got %v", r.reports)
	}
}

func TestBudgetSentinelHardStop(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8, HardStop: true})

	s.OnCost(11)
	if len(r.aborts) != 1 {
		t.Fatalf("hard_stop should abort immediately, got %v", r.aborts)
	}
	// ranh giới tiếp theo không dừng lại thêm lần nữa
	r.cost = 11
	s.HandleEvent(subagentEndEvent())
	if len(r.aborts) != 1 {
		t.Fatalf("stopped state should not abort again, got %v", r.aborts)
	}
}

func TestBudgetSentinelRefuse(t *testing.T) {
	r := &budgetRecorder{cost: 9.99}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})

	if err := s.Refuse(); err != nil {
		t.Errorf("below limit should pass: %v", err)
	}
	r.cost = 10 // đúng bằng giới hạn → từ chối
	if err := s.Refuse(); err == nil {
		t.Error("at limit should refuse")
	} else if !strings.Contains(err.Error(), "book_usd") {
		t.Errorf("refuse error should mention how to recover, got %v", err)
	}
}

func TestBudgetSentinelZeroCostBlindWarning(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})

	// ghi nhận chi phí bằng 0 liên tiếp: đến blindZeroStreak lần thì phát đúng một lần cảnh báo vùng mù, sau đó im lặng
	for range blindZeroStreak + 3 {
		s.OnCost(0)
	}
	if len(r.reports) != 1 || !strings.Contains(strings.ToLower(r.reports[0]), "vùng mù ngân sách") {
		t.Fatalf("expected exactly one blind warning, got %v", r.reports)
	}
	if len(r.aborts) != 0 {
		t.Fatal("blind warning must not abort")
	}

	// mô hình có tính phí không nên báo nhầm: tổng chi phí tăng dần theo từng lần ghi nhận
	r2 := &budgetRecorder{}
	s2 := r2.sentinel(bootstrap.BudgetConfig{BookUSD: 10, WarnRatio: 0.8})
	for i := range blindZeroStreak + 3 {
		s2.OnCost(0.1 * float64(i+1))
	}
	for _, rep := range r2.reports {
		if strings.Contains(strings.ToLower(rep), "vùng mù") {
			t.Fatalf("priced model should not trigger blind warning: %v", r2.reports)
		}
	}
}

// TestBudgetSentinelPerChapterWarnOnce kiểm tra acceptance Part J: 4 chương baseline cost 1.0, chương 5
// tốn 4.0 (gấp 4x, vượt perChapterWarnFactor=3) → cảnh báo đúng một lần kèm số liệu USD.
func TestBudgetSentinelPerChapterWarnOnce(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 1000, WarnRatio: 0.8})

	// 4 chương đầu, mỗi chương tốn đúng 1.0 (tích lũy 1,2,3,4) — đây là baseline, không nên cảnh báo.
	for i := 1; i <= 4; i++ {
		r.cost = float64(i)
		s.HandleEvent(commitChapterEndEvent())
	}
	if len(r.reports) != 0 {
		t.Fatalf("baseline chapters should not warn, got %v", r.reports)
	}

	// Chương 5 tốn 4.0 (tích lũy 4 -> 8), gấp 4x trung bình 1.0/chương -> cảnh báo đúng 1 lần kèm số liệu.
	r.cost = 8
	s.HandleEvent(commitChapterEndEvent())
	if len(r.reports) != 1 {
		t.Fatalf("expected exactly one per-chapter warning, got %v", r.reports)
	}
	if !strings.Contains(r.reports[0], "$4.00") || !strings.Contains(r.reports[0], "$1.00") {
		t.Fatalf("warning should contain chapter cost vs average figures, got %v", r.reports[0])
	}

	// Chương kế tiếp lại tốn 1.0 bình thường -> không cảnh báo thêm, tổng số warning vẫn là 1.
	r.cost = 9
	s.HandleEvent(commitChapterEndEvent())
	if len(r.reports) != 1 {
		t.Fatalf("normal chapter after the spike should not add another warning, got %v", r.reports)
	}
}

// TestBudgetSentinelPerChapterNoBaselineNoWarn kiểm tra: chưa đủ minChapterBaseline (3) chương làm baseline
// thì dù chương lệch mạnh so với các chương trước đó cũng không cảnh báo.
func TestBudgetSentinelPerChapterNoBaselineNoWarn(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 1000, WarnRatio: 0.8})

	// Chỉ 2 chương baseline (1.0, 1.0), chương 3 tốn 10.0 (lệch rất mạnh) — nhưng baseline chưa đủ 3 nên im lặng.
	r.cost = 1
	s.HandleEvent(commitChapterEndEvent())
	r.cost = 2
	s.HandleEvent(commitChapterEndEvent())
	r.cost = 12
	s.HandleEvent(commitChapterEndEvent())

	if len(r.reports) != 0 {
		t.Fatalf("less than minChapterBaseline chapters should not warn even with a big deviation, got %v", r.reports)
	}
}

func TestBudgetSentinelBlindWarningAfterModelSwitch(t *testing.T) {
	// giữa chừng chạy dài /model chuyển sang mô hình không có giá: total dừng ở giá trị lịch sử khác 0 nhưng không tăng nữa, vẫn phải cảnh báo
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 100, WarnRatio: 0.8})

	for i := range 5 {
		s.OnCost(1.0 * float64(i+1)) // giai đoạn tính phí: tổng tăng dần đến $5
	}
	for range blindZeroStreak {
		s.OnCost(5.0) // chuyển sang mô hình không có giá: tổng bị kẹt cố định
	}
	if len(r.reports) != 1 || !strings.Contains(strings.ToLower(r.reports[0]), "vùng mù") {
		t.Fatalf("expected blind warning after switch to unpriced model, got %v", r.reports)
	}
}
