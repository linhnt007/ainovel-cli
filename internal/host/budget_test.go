package host

import (
	"encoding/json"
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

// commitChapterResultEvent dựng event commit_chapter kèm Result JSON có "chapter" (và "rewritten" khi cần) —
// dùng để test tracking per-chương nhận diện đúng chương (review round 1, finding 2), mô phỏng đúng shape
// JSON thật do commitOutput/domain.CommitResult và executeRewriteCommit trả về.
func commitChapterResultEvent(chapter int, rewritten bool) agentcore.Event {
	payload, _ := json.Marshal(struct {
		Chapter   int  `json:"chapter"`
		Committed bool `json:"committed"`
		Rewritten bool `json:"rewritten,omitempty"`
	}{Chapter: chapter, Committed: true, Rewritten: rewritten})
	return agentcore.Event{Type: agentcore.EventToolExecEnd, Tool: "commit_chapter", Result: payload}
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
	// (Chương đầu tiên trong số này bị loại khỏi chapterCosts theo finding 1 — xem
	// TestBudgetSentinelPerChapterFirstCommitExcludedFromBaseline — nhưng vì 3 chương còn lại đã đủ
	// minChapterBaseline nên bài test này không bị ảnh hưởng.)
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

// TestBudgetSentinelPerChapterFirstCommitExcludedFromBaseline kiểm tra finding 1 (review round 1): delta của
// lần commit_chapter đầu tiên gồm cả chi phí pha kiến trúc/nền móng chạy trước đó cùng phiên, không đại diện
// cho "chi phí một chương" — phải bị loại khỏi chapterCosts, chỉ dùng để đặt mốc.
//
// Chứng minh bằng phản chứng: chương 1 tốn 20.0 (giả lập cả pha setup dồn vào), chương 2-4 tốn 1.0/chương,
// chương 5 tốn 4.0 (spike thật, gấp 4x mức bình thường 1.0). Nếu chương 1 KHÔNG bị loại, trung bình 4 chương
// đầu sẽ là (20+1+1+1)/4=5.75 và spike 4.0 sẽ KHÔNG vượt quá 3x mức đó (17.25) → false negative, spike bị
// che khuất đúng như finding 1 cảnh báo. Với fix, chương 1 bị loại nên baseline chỉ còn (1,1,1), trung bình
// 1.0, spike 4.0 vượt hẳn 3x → phải cảnh báo.
func TestBudgetSentinelPerChapterFirstCommitExcludedFromBaseline(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 1000, WarnRatio: 0.8})

	r.cost = 20 // chương 1: 20.0 (bao gồm cả pha kiến trúc/nền móng trước đó) — chỉ đặt mốc, không vào baseline
	s.HandleEvent(commitChapterEndEvent())
	r.cost = 21 // chương 2: 1.0
	s.HandleEvent(commitChapterEndEvent())
	r.cost = 22 // chương 3: 1.0
	s.HandleEvent(commitChapterEndEvent())
	r.cost = 23 // chương 4: 1.0 — baseline (1,1,1) đã đủ minChapterBaseline=3, không tính chương 1 phình vào
	s.HandleEvent(commitChapterEndEvent())
	if len(r.reports) != 0 {
		t.Fatalf("chapters before the spike should not warn, got %v", r.reports)
	}

	r.cost = 27 // chương 5: 4.0 — gấp 4x trung bình 1.0 sạch (không bị chương 1 kéo lệch) -> phải cảnh báo
	s.HandleEvent(commitChapterEndEvent())
	if len(r.reports) != 1 {
		t.Fatalf("expected the real spike to be detected once baseline excludes the first (contaminated) commit, got %v", r.reports)
	}
	if !strings.Contains(r.reports[0], "$4.00") || !strings.Contains(r.reports[0], "$1.00") {
		t.Fatalf("warning should contain chapter cost vs clean average figures, got %v", r.reports[0])
	}
}

// TestBudgetSentinelPerChapterRetryDoesNotInflateBaseline kiểm tra finding 2 (review round 1): commit_chapter
// là idempotent/retriable, gọi lại cùng một chương (không phải rewrite thật) không được tính là "chương mới"
// — nếu không lọc, entry chi phí thấp giả của lần gọi lại sẽ làm baseline đạt ngưỡng minChapterBaseline sớm
// hơn thực tế và làm lệch trung bình.
//
// Chứng minh bằng phản chứng: chỉ có 2 chương thật (2, 3) trước khi chương 4 tốn 100.0 (spike rất lớn). Xen
// giữa là một lần gọi lại chương 2 (idempotent skip, "rewritten" vắng mặt) với chi phí nhỏ giả 0.01. Nếu lần
// gọi lại này bị tính là "chương mới" thì baseline sẽ có 3 entry (1.0, 0.01, 1.0) ngay trước chương 4, đủ
// minChapterBaseline=3 → sẽ cảnh báo (dùng trung bình đã bị lệch bởi entry giả ~0.67). Với fix, lần gọi lại
// bị bỏ qua hoàn toàn nên baseline mới chỉ có 2 entry thật (1.0, 1.0) — chưa đủ 3 — nên KHÔNG cảnh báo ở
// chương 4, dù chi phí spike rất lớn.
func TestBudgetSentinelPerChapterRetryDoesNotInflateBaseline(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 1000, WarnRatio: 0.8})

	r.cost = 1 // chương 1: đặt mốc (finding 1 exclusion), không vào baseline
	s.HandleEvent(commitChapterResultEvent(1, false))
	r.cost = 2 // chương 2 thật: 1.0 -> baseline entry #1
	s.HandleEvent(commitChapterResultEvent(2, false))
	r.cost = 2.01 // retry idempotent của chương 2 (buildSkipResult): chi phí giả nhỏ 0.01, không phải rewrite
	s.HandleEvent(commitChapterResultEvent(2, false))
	r.cost = 3.01 // chương 3 thật: 1.0 -> baseline entry #2 (nếu retry không bị lọc thì đây đã là entry #3)
	s.HandleEvent(commitChapterResultEvent(3, false))
	if len(r.reports) != 0 {
		t.Fatalf("no warning expected before the spike, got %v", r.reports)
	}

	r.cost = 103.01 // chương 4: spike 100.0 — baseline thật chỉ có 2 entry (chưa đủ 3) nên không được cảnh báo
	s.HandleEvent(commitChapterResultEvent(4, false))
	if len(r.reports) != 0 {
		t.Fatalf("retry must not count toward baseline — spike should stay silent until 3 real chapters exist, got %v", r.reports)
	}
}

// TestBudgetSentinelPerChapterRewriteOfSameChapterStillCounted kiểm tra vế còn lại của finding 2: dedup theo
// số chương KHÔNG được lọc nhầm rewrite thật. Viết lại một chương đã hoàn thành (executeRewriteCommit, có
// "rewritten":true) là chính kịch bản "rewrite loop" mà Part J muốn phát hiện — dù trùng số chương với lần
// commit trước, vẫn phải được tính vào baseline/cảnh báo như một chương bình thường.
func TestBudgetSentinelPerChapterRewriteOfSameChapterStillCounted(t *testing.T) {
	r := &budgetRecorder{}
	s := r.sentinel(bootstrap.BudgetConfig{BookUSD: 1000, WarnRatio: 0.8})

	r.cost = 1 // chương 1: đặt mốc (finding 1 exclusion)
	s.HandleEvent(commitChapterResultEvent(1, false))
	r.cost = 2 // chương 2: 1.0 -> baseline entry #1
	s.HandleEvent(commitChapterResultEvent(2, false))
	r.cost = 3 // chương 3: 1.0 -> baseline entry #2
	s.HandleEvent(commitChapterResultEvent(3, false))
	r.cost = 4 // chương 4: 1.0 -> baseline entry #3, đủ minChapterBaseline
	s.HandleEvent(commitChapterResultEvent(4, false))
	if len(r.reports) != 0 {
		t.Fatalf("baseline chapters should not warn, got %v", r.reports)
	}

	// Viết lại chương 4 (cùng số chương với lần commit ngay trước) với chi phí lớn: phải được tính là một
	// lần commit mới (không bị dedup bỏ qua như retry) và so với baseline sạch (1,1,1) -> vượt 3x -> cảnh báo.
	r.cost = 14 // rewrite chương 4: 10.0, gấp 10x trung bình 1.0
	s.HandleEvent(commitChapterResultEvent(4, true))
	if len(r.reports) != 1 {
		t.Fatalf("a real rewrite of the same chapter must still be tracked and warn, got %v", r.reports)
	}
	if !strings.Contains(r.reports[0], "$10.00") || !strings.Contains(r.reports[0], "$1.00") {
		t.Fatalf("warning should contain chapter cost vs average figures, got %v", r.reports[0])
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
