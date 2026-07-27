package host

import (
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// Máy trạng thái ngân sách: tiến một chiều, mỗi lần chuyển trạng thái kích hoạt đúng một tác dụng phụ, không lùi.
// Tăng ngân sách = người dùng tái ủy quyền = khởi động lại sau khi đổi cấu hình / Host instance mới, không hoàn trạng thái trong instance này.
const (
	budgetNormal      int32 = iota // Chưa đến ngưỡng cảnh báo
	budgetWarned                   // Đã cảnh báo, chưa vượt ngưỡng
	budgetStopPending              // Đã vượt ngưỡng, chờ dừng tại ranh giới agent phụ
	budgetStopped                  // Đã thực thi dừng máy
)

// BudgetSentinel giám sát chi phí tích lũy, thực thi chính sách ngân sách của người dùng (khối config budget).
//
// Định vị kiến trúc (architecture.md §8.3/§10): không đánh giá hành vi mô hình — dừng khi vượt ngưỡng
// tương đương người dùng Abort thủ công tại thời điểm đó, Host chỉ thực thi một lệnh đã được ký trước.
// Nó ảnh hưởng đến luồng điều khiển nên không phải observer, được định vị ngang hàng với flow.Dispatcher
// là thành phần chính sách Host; tầng Route/công cụ không biết đến nó.
//
// Thời điểm dừng: mặc định tại ranh giới agent phụ (HandleEvent lắng nghe EventToolExecEnd(tool=subagent),
// cùng điểm kích hoạt với Dispatcher), không lãng phí chương đang xử lý; khi hardStop=true thì dừng ngay khi vượt ngưỡng.
// Ràng buộc thứ tự đăng ký: Sentinel phải đăng ký trước Dispatcher — sau khi Abort được đặt,
// FollowUp của Dispatcher tự nhiên thất bại, không cần thêm nhận thức ngân sách vào tầng định tuyến.
type BudgetSentinel struct {
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64              // Chi phí tích lũy hiện tại (gói usage.Totals; có thể inject stub để test)
	abort   func(reason string)         // Wrapper dừng Host (kèm sự kiện lý do)
	report  func(level, summary string) // Kênh xuất cảnh báo (emitEvent + notify, được inject bởi Host)

	state atomic.Int32

	// Phát hiện vùng mù tính phí: mô hình không có giá trong registry và provider không tự báo cost
	// thì mỗi lần ghi phí tăng thêm $0, ngân sách âm thầm vô hiệu. Phát hiện bằng "nhiều lần tăng
	// liên tiếp bằng 0" thay vì total==0 — cách sau không bắt được trường hợp giữa chừng dùng /model
	// chuyển sang mô hình không có giá (total dừng ở giá trị lịch sử khác 0 nhưng không tăng nữa).
	// Mô hình miễn phí cũng rơi vào đây, thông báo "ngân sách sẽ không kích hoạt" áp dụng cho chúng như nhau.
	lastTotal   atomic.Uint64 // math.Float64bits(chi phí tích lũy lần callback trước)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool

	// Phát hiện "chương bệnh" (Part J): book_usd tích lũy chỉ cảnh báo ở quy mô toàn sách, một chương bất
	// thường (rewrite loop, draft khổng lồ) có thể ngốn phần lớn ngân sách mà không có tín hiệu cục bộ nào.
	// costAtLastCommit là mốc chi phí tại lần commit_chapter gần nhất; delta so với mốc = chi phí chương vừa
	// xong. chapterCosts giữ tối đa chapterAvgWindow chi phí chương gần nhất để làm trung bình trượt so sánh.
	// Dùng mutex thay vì atomic vì thao tác (tính trung bình, trượt cửa sổ) không biểu diễn được bằng một CAS.
	// Lưu ý: giống mọi state khác của Sentinel, không phục hồi qua Host instance mới — mốc bắt đầu lại từ 0
	// (nhất quán với triết lý "tăng ngân sách = tái ủy quyền, không hoàn trạng thái" đã ghi ở đầu file).
	//
	// haveCommitted đánh dấu đã xử lý ít nhất một commit_chapter (review round 1, finding 1): delta của lần
	// commit_chapter ĐẦU TIÊN trong đời Sentinel gồm cả chi phí pha kiến trúc/nền móng chạy trước đó cùng
	// phiên (save_foundation, append_volume...), không đại diện cho "chi phí một chương" — chỉ dùng để đặt
	// mốc, không đưa vào chapterCosts, tránh làm bẩn baseline ngay từ đầu sách.
	//
	// lastChapter ghi số chương (parse từ Result JSON của commit_chapter, field "chapter") của lần xử lý gần
	// nhất, dùng để lọc trùng lặp (review round 1, finding 2): commit_chapter là idempotent/retriable — gọi
	// lại một chương đã hoàn thành và không nằm trong PendingRewrites sẽ rơi vào buildSkipResult (gần như
	// free, không có field "rewritten"), không nên tính là "chương mới". Không được lọc mù theo số chương:
	// viết lại thật (executeRewriteCommit — đúng kịch bản rewrite loop mà Part J muốn bắt) cũng commit lại
	// cùng số chương nhưng có "rewritten":true và chi phí thật, vẫn phải được tính.
	chapterMu        sync.Mutex
	costAtLastCommit float64
	chapterCosts     []float64
	haveCommitted    bool
	lastChapter      int
}

// blindZeroStreak là số lần ghi phí tăng bằng 0 liên tiếp trước khi cảnh báo. Mô hình tính phí bình thường
// mỗi lần tăng phải > 0 (cost là float tích lũy không làm tròn), lấy 5 chỉ để tránh nhiễu cực đoan,
// không phải ngưỡng có thể điều chỉnh theo chính sách.
const blindZeroStreak = 5

// perChapterWarnFactor: chương vừa hoàn thành tốn gấp hơn factor lần trung bình các chương gần đây mới
// đáng nói — chọn 3 vì biến động tự nhiên giữa các chương (cao trào dài hơn, nhiều thoại hơn...) thường
// không vượt quá 2-3x, còn rewrite loop/draft phình thường lệch rất xa (chục lần). Có thể false-positive ở
// chương cao trào dài nhưng chấp nhận được vì đây chỉ là cảnh báo, không dừng/chặn (brief Part J).
const perChapterWarnFactor = 3

// chapterAvgWindow: số chương gần nhất dùng để tính trung bình trượt — đủ để phản ánh "gần đây" mà không bị
// một chương đột biến kéo lệch trung bình quá lâu về sau.
const chapterAvgWindow = 5

// minChapterBaseline: cần tối thiểu bấy nhiêu chương đã hoàn thành làm baseline trước khi so sánh; ít hơn thì
// trung bình chưa đủ tin cậy để kết luận chương hiện tại là bất thường (brief: "≥ 3 chương làm baseline").
const minChapterBaseline = 3

// NewBudgetSentinel tạo BudgetSentinel; trả về nil khi chính sách chưa được bật (tất cả method đều an toàn với nil).
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// OnCost được UsageTracker gọi sau mỗi lần ghi phí, truyền vào chi phí tích lũy mới nhất (ngoài lock).
// Một lần callback có thể vượt qua hai mức (normal→warned→stopPending), hai tác dụng phụ đều được kích hoạt.
func (s *BudgetSentinel) OnCost(total float64) {
	if s == nil {
		return
	}
	if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
		if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
			s.report("warn", fmt.Sprintf("Vùng mù ngân sách: liên tục ghi phí nhưng chi phí tích lũy dừng ở $%.2f không tăng nữa (mô hình hiện tại không có giá trong registry và provider không tự báo cost, hoặc là mô hình miễn phí) — ngân sách sẽ không kích hoạt", total))
		}
	} else {
		s.zeroStreak.Store(0)
	}
	if total >= s.limit*s.warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("Cảnh báo ngân sách: đã chi $%.2f, đạt %.0f%% ngân sách $%.2f", total, s.warnRatio*100, s.limit))
	}
	if total >= s.limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if s.hardStop {
			s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, dừng ngay", total, s.limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, sẽ dừng sau khi agent phụ hiện tại hoàn thành", total, s.limit))
	}
}

// HandleEvent thực thi lệnh dừng đang chờ tại ranh giới agent phụ, và cập nhật tracking chi phí per-chương.
// Phải đăng ký trước Dispatcher. Không bỏ qua IsError — lỗi trả về cũng là ranh giới, không nên trì hoãn
// dừng vì agent phụ thất bại (và với commit_chapter, chi phí đã phát sinh dù tool có lỗi hay không).
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type == agentcore.EventToolExecEnd && ev.Tool == "commit_chapter" {
		s.onChapterCommit(ev.Result)
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	if s.state.Load() != budgetStopPending {
		return
	}
	s.stop(s.costNow())
}

// onChapterCommit tính chi phí chương vừa hoàn tất (delta so với mốc commit_chapter trước) và so với trung
// bình trượt các chương gần đây; vượt quá perChapterWarnFactor lần thì cảnh báo — CHỈ cảnh báo, không dừng,
// không chặn, quyết định thuộc về người dùng (brief Part J).
//
// result là Event.Result (JSON trả về của tool commit_chapter, xem commitOutput/domain.CommitResult tại
// internal/tools/commit_chapter.go) — dùng để đọc field "chapter" (nhận diện chương, finding 2) và "rewritten"
// (chỉ có ở nhánh executeRewriteCommit, phân biệt viết lại thật với skip idempotent cùng số chương).
func (s *BudgetSentinel) onChapterCommit(result json.RawMessage) {
	total := s.costNow()

	var parsed struct {
		Chapter   int  `json:"chapter"`
		Rewritten bool `json:"rewritten"`
	}
	// Best-effort: Result rỗng hoặc parse lỗi thì Chapter=0 — coi như không xác định được số chương, fail-open
	// (xử lý như một chương mới bình thường) thay vì âm thầm bỏ qua cảnh báo về sau.
	_ = json.Unmarshal(result, &parsed)

	s.chapterMu.Lock()
	defer s.chapterMu.Unlock()

	chapterCost := total - s.costAtLastCommit
	s.costAtLastCommit = total
	if chapterCost < 0 {
		// Phòng vệ: total tích lũy theo thiết kế không giảm; nếu costNow bị stub/reset (test, hoặc phục hồi
		// state lạ) thì bỏ qua thay vì báo cảnh sai với số âm.
		return
	}

	// Finding 2: lọc trùng lặp theo số chương. commit_chapter idempotent/retriable — gọi lại một chương đã
	// hoàn thành (không phải rewrite) rơi vào buildSkipResult, cùng "chapter" nhưng không làm việc thật; entry
	// chi phí thấp giả của nó sẽ làm phình count/lệch trung bình nếu tính vào baseline. Không loại trừ khi có
	// "rewritten":true — đó là rewrite loop thật, đúng thứ Part J muốn bắt.
	isDuplicate := parsed.Chapter != 0 && parsed.Chapter == s.lastChapter && !parsed.Rewritten
	if parsed.Chapter != 0 {
		s.lastChapter = parsed.Chapter
	}
	if isDuplicate {
		return
	}

	// Finding 1: lần commit_chapter đầu tiên trong đời Sentinel không đại diện cho chi phí một chương (gồm cả
	// pha kiến trúc/nền móng chạy trước writer trong cùng phiên) — chỉ set mốc phía trên, không đưa vào
	// chapterCosts. Mất 1 điểm dữ liệu chương đầu, đổi lại baseline sạch ngay từ đầu sách.
	firstCommit := !s.haveCommitted
	s.haveCommitted = true
	if firstCommit {
		return
	}

	if len(s.chapterCosts) >= minChapterBaseline {
		avg := average(s.chapterCosts)
		if avg > 0 && chapterCost > perChapterWarnFactor*avg {
			s.report("warn", fmt.Sprintf("Chương bất thường: chương vừa hoàn thành tốn $%.2f, gấp hơn %dx trung bình $%.2f/chương (tính trên %d chương gần nhất)", chapterCost, perChapterWarnFactor, avg, len(s.chapterCosts)))
		}
	}

	s.chapterCosts = append(s.chapterCosts, chapterCost)
	if len(s.chapterCosts) > chapterAvgWindow {
		s.chapterCosts = s.chapterCosts[len(s.chapterCosts)-chapterAvgWindow:]
	}
}

// average trả về trung bình cộng của xs; 0 nếu rỗng.
func average(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("Dừng do hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f; tăng budget.book_usd trong cấu hình để tiếp tục", total, s.limit))
	}
}

// Refuse kiểm tra trước khi khởi động: trả về lỗi từ chối nếu ngân sách đã cạn (được gọi ở đường phục hồi Start/Resume/Continue).
// Người dùng tăng ngân sách = tái ủy quyền, với cấu hình mới Refuse sẽ tự nhiên cho phép.
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	if cost := s.costNow(); cost >= s.limit {
		return fmt.Errorf("cuốn sách này đã chi $%.2f, đạt giới hạn ngân sách $%.2f; hãy tăng budget.book_usd trong cấu hình rồi thử lại", cost, s.limit)
	}
	return nil
}

// Limit trả về giới hạn ngân sách (dùng để hiển thị trên TUI); trả về 0 nếu chưa bật.
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	return s.limit
}
