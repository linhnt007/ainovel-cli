package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/ratelimit"
)

// fakeChatModel là ChatModel giả cho test: đếm số lần Generate/GenerateStream được gọi
// thực sự (RAW — không tự Acquire gì cả), giống hệt những gì createModelFromConfig trả về
// sau khi sửa lỗi double-Acquire (xem models.go, ghi chú tại createModelFromConfig).
type fakeChatModel struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeChatModel) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeChatModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return &agentcore.LLMResponse{
		Message: agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("ok")},
			Usage:   &agentcore.Usage{TotalTokens: 10},
		},
	}, nil
}

func (f *fakeChatModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	out := make(chan agentcore.StreamEvent, 1)
	out <- agentcore.StreamEvent{
		Type: agentcore.StreamEventDone,
		Message: agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("ok")},
			Usage:   &agentcore.Usage{TotalTokens: 10},
		},
	}
	close(out)
	return out, nil
}

func (f *fakeChatModel) SupportsTools() bool { return true }

// testKeySeq đảm bảo mỗi lần gọi testKey ra 1 key khác nhau, kể cả 2 lần chạy `go test` liên
// tiếp trong cùng giây — ratelimit.Global không có điểm inject store tạm từ package bootstrap
// (globalStore/Registry.store là unexported, chỉ ratelimit package tự test được bằng
// t.TempDir()), nên test ở đây BẮT BUỘC chạm file global thật
// (%AppData%\ainovel-cli\ratelimit.json). Nếu dùng key cố định theo tên test, state RPM/RPD cũ
// từ lần chạy trước (cửa sổ cuộn 60s/24h) sẽ khiến lần chạy sau flaky/false-negative — key
// random hóa theo mỗi lần gọi để không bao giờ đụng dữ liệu cũ.
var testKeySeq int64

func testKey(t *testing.T, suffix string) (string, string) {
	t.Helper()
	seq := atomic.AddInt64(&testKeySeq, 1)
	uniq := fmt.Sprintf("%s-%d-%d", suffix, time.Now().UnixNano(), seq)
	return "test-provider-" + t.Name() + "-" + uniq, "test-model-" + t.Name() + "-" + uniq
}

// newFailoverModelSet dựng 1 ModelSet với role "test-role" có primary + 1 fallback, mô phỏng
// CHÍNH XÁC những gì NewModelSet tạo ra sau khi sửa lỗi double-Acquire: instance ChatModel raw
// (fake, không tự Acquire gì) được gán thẳng vào ms.models/ms.fallbacks — giống hệt những gì
// createModelFromConfig trả về (không mạng, không cần provider thật). Đi qua
// ms.ForRoleWithFailover công khai (KHÔNG tự dựng failoverModel tay) để bài test còn bắt được
// regression nếu sau này có ai bọc thêm rate limit quanh primary/fallback ngay tại
// ForRoleWithFailover — đúng lớp mã đã gây ra bug Critical.
func newFailoverModelSet(provider, model string, fake *fakeChatModel) *ModelSet {
	primary := NewSwappableModel(provider, model, fake)
	return &ModelSet{
		models: map[string]*SwappableModel{"test-role": primary},
		fallbacks: map[string][]modelTarget{
			"test-role": {{provider: provider, name: model, model: fake}},
		},
	}
}

// TestFailoverModel_Generate_NoDeadlock_SharedLimiterKey tái hiện bug Critical: trước khi sửa,
// createModelFromConfig bọc MỌI model bằng rateLimitedModel vô điều kiện, kể cả model dùng làm
// target bên trong failoverModel (kể cả primary qua SwappableModel). failoverModel.pickAvailable
// đã Acquire 1 handle cho target rồi gọi target.model.Generate(...) lại Acquire CHÍNH limiter đó
// lần 2 → với MaxConcurrent=1, request tự chờ semaphore mình đang giữ → deadlock tới ctx timeout.
//
// Sau khi sửa: createModelFromConfig trả model RAW; ForRoleWithFailover không bọc thêm gì quanh
// primary/fallback; failoverModel là lớp Acquire/Record DUY NHẤT — Generate phải trả về ngay.
func TestFailoverModel_Generate_NoDeadlock_SharedLimiterKey(t *testing.T) {
	// primary/fallback CÙNG (provider,model) => cùng 1 Limiter key, đúng kịch bản repro reviewer nêu.
	provider, model := testKey(t, "shared")
	ratelimit.Global.Register(provider, model, ratelimit.Limits{MaxConcurrent: 1})

	fake := &fakeChatModel{}
	ms := newFailoverModelSet(provider, model, fake)
	fm := ms.ForRoleWithFailover("test-role", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		resp *agentcore.LLMResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := fm.Generate(ctx, []agentcore.Message{agentcore.UserMsg("hi")}, nil)
		done <- result{resp, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Generate trả lỗi bất ngờ: %v", r.err)
		}
		if r.resp == nil {
			t.Fatal("Generate trả response nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Generate DEADLOCK — double-Acquire trên cùng Limiter với MaxConcurrent=1 (regression của bug Critical đã sửa)")
	}
}

// TestFailoverModel_GenerateStream_NoDeadlock_SharedLimiterKey: bản GenerateStream của cùng
// regression — reviewer ghi rõ bug "dính cả Generate lẫn GenerateStream/startAttempt".
func TestFailoverModel_GenerateStream_NoDeadlock_SharedLimiterKey(t *testing.T) {
	provider, model := testKey(t, "shared")
	ratelimit.Global.Register(provider, model, ratelimit.Limits{MaxConcurrent: 1})

	fake := &fakeChatModel{}
	ms := newFailoverModelSet(provider, model, fake)
	fm := ms.ForRoleWithFailover("test-role", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := fm.GenerateStream(ctx, []agentcore.Message{agentcore.UserMsg("hi")}, nil)
	if err != nil {
		t.Fatalf("GenerateStream trả lỗi bất ngờ: %v", err)
	}

	sawDone := false
	timeout := time.After(2 * time.Second)
drain:
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				break drain
			}
			if ev.Type == agentcore.StreamEventDone {
				sawDone = true
			}
		case <-timeout:
			t.Fatal("GenerateStream DEADLOCK — double-Acquire trên cùng Limiter với MaxConcurrent=1 (regression của bug Critical đã sửa)")
		}
	}
	if !sawDone {
		t.Fatal("stream đóng mà không phát StreamEventDone")
	}
}

// TestFailoverModel_Generate_ExactlyOneEventPerRealCall kiểm tra vế double-spend quota của cùng
// bug: nếu target bị bọc rate limit thêm 1 lớp (như rateLimitedModel cũ / như lỡ bọc lại tại
// ForRoleWithFailover), MỖI lần gọi thật sẽ ghi 2 event RPM thay vì 1 — âm thầm ăn gấp đôi quota
// (ngược mục tiêu chống ban), dù không MaxConcurrent nên không deadlock. Đăng ký RPM=2: sau ĐÚNG
// 1 lần gọi thật, phải còn đúng 1 slot RPM trống (TryAcquire kế tiếp phải ok=true); nếu bug tái
// diễn, 2 event đã bị ghi, slot cuối cùng bị chiếm hết ngay từ 1 lần gọi.
func TestFailoverModel_Generate_ExactlyOneEventPerRealCall(t *testing.T) {
	provider, model := testKey(t, "quota")
	ratelimit.Global.Register(provider, model, ratelimit.Limits{RPM: 2})

	fake := &fakeChatModel{}
	ms := newFailoverModelSet(provider, model, fake)
	fm := ms.ForRoleWithFailover("test-role", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := fm.Generate(ctx, []agentcore.Message{agentcore.UserMsg("hi")}, nil)
	if err != nil {
		t.Fatalf("Generate trả lỗi bất ngờ: %v", err)
	}
	if resp == nil {
		t.Fatal("Generate trả response nil")
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("model raw phải được gọi đúng 1 lần cho 1 request thật, got %d", got)
	}

	lim := ratelimit.Global.For(provider, model)
	h, ra, ok := lim.TryAcquire(0)
	if !ok {
		t.Fatalf("sau 1 lần gọi thật với RPM=2, phải còn 1 slot trống — bị chặn (retryAfter=%v) nghĩa là đã ghi 2 event (double-spend quota, regression của bug Critical đã sửa)", ra)
	}
	h.Record(0, nil)
}

// TestModelSet_ForRole_SingleTarget_ExactlyOneEventPerRealCall kiểm tra đường single-target
// (ForRole / role không fallback / Default) vẫn áp đúng 1 lớp rate limit — không thiếu (mất tác
// dụng chống ban) cũng không thừa (double-spend) — qua singleTargetModel mới thay cho
// rateLimitedModel cũ.
func TestModelSet_ForRole_SingleTarget_ExactlyOneEventPerRealCall(t *testing.T) {
	provider, model := testKey(t, "single")
	ratelimit.Global.Register(provider, model, ratelimit.Limits{RPM: 2})

	fake := &fakeChatModel{}
	ms := &ModelSet{
		Default: NewSwappableModel(provider, model, fake),
		models:  make(map[string]*SwappableModel),
	}

	m := ms.ForRole("khong-ton-tai") // rơi về Default
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := m.Generate(ctx, []agentcore.Message{agentcore.UserMsg("hi")}, nil)
	if err != nil {
		t.Fatalf("Generate trả lỗi bất ngờ: %v", err)
	}
	if resp == nil {
		t.Fatal("Generate trả response nil")
	}
	if got := fake.callCount(); got != 1 {
		t.Fatalf("model raw phải được gọi đúng 1 lần, got %d", got)
	}

	lim := ratelimit.Global.For(provider, model)
	h, ra, ok := lim.TryAcquire(0)
	if !ok {
		t.Fatalf("sau 1 lần gọi thật với RPM=2, phải còn 1 slot trống — bị chặn (retryAfter=%v)", ra)
	}
	h.Record(0, nil)
}
