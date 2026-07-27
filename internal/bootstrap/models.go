package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/ratelimit"
)

// Trong tình huống đầu ra dài + ctx dài, với nhà cung cấp hỗ trợ suy luận (mimo / deepseek-r1 v.v.)
// nếu phía server không stream reasoning delta, toàn bộ SSE sẽ im lặng trong giai đoạn suy nghĩ.
// litellm mặc định watchdog 2 phút, thường gây ngắt nhầm khi viết chương 8000 chữ.
// 5 phút bao phủ hầu hết trường hợp thực tế (xem thống kê thời gian suy nghĩ plan→draft trong tasks/todo.md),
// vẫn nhỏ hơn RequestTimeout 10 phút, đảm bảo thoát được khi mạng thực sự chết.
const streamIdleTimeout = 5 * time.Minute

// FailoverEvent biểu diễn một lần chuyển đổi nhà cung cấp tường minh.
// Reason là nhãn ngắn (rate_limit / timeout / stream_idle / network), dùng cho log có cấu trúc.
type FailoverEvent struct {
	Role         string
	Reason       string
	FromProvider string
	FromModel    string
	ToProvider   string
	ToModel      string
	Err          error
}

// FailoverReporter được gọi khi xảy ra chuyển đổi nhà cung cấp tường minh.
type FailoverReporter func(FailoverEvent)

type modelTarget struct {
	provider string
	name     string
	model    agentcore.ChatModel
}

// SwappableModel là wrapper ChatModel có thể hoán đổi nóng.
// Các yêu cầu đã bắt đầu tiếp tục dùng instance cũ; các yêu cầu tiếp theo tự động chuyển sang instance mới.
type SwappableModel struct {
	*agentcore.SwappableModel
	mu       sync.RWMutex
	provider string
	name     string
}

func NewSwappableModel(provider, name string, model agentcore.ChatModel) *SwappableModel {
	return &SwappableModel{
		SwappableModel: agentcore.NewSwappableModel(model),
		provider:       provider,
		name:           name,
	}
}

func (m *SwappableModel) ProviderName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

func (m *SwappableModel) Info() llm.ModelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if info, ok := m.SwappableModel.Current().(interface{ Info() llm.ModelInfo }); ok {
		modelInfo := info.Info()
		if modelInfo.Name == "" {
			modelInfo.Name = m.name
		}
		if modelInfo.Provider == "" {
			modelInfo.Provider = m.provider
		}
		return modelInfo
	}
	return llm.ModelInfo{
		Name:     m.name,
		Provider: m.provider,
	}
}

func (m *SwappableModel) Swap(provider, name string, model agentcore.ChatModel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SwappableModel.Swap(model)
	m.provider = provider
	m.name = name
}

func (m *SwappableModel) Current() (provider, name string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider, m.name
}

// ModelSet lưu giữ các instance mô hình phân bổ theo vai trò; vai trò chưa cấu hình sẽ fallback về mô hình mặc định.
type ModelSet struct {
	Default   *SwappableModel
	models    map[string]*SwappableModel
	fallbacks map[string][]modelTarget
	config    Config
}

// ForRole trả về mô hình cho vai trò chỉ định; trả về mô hình mặc định nếu chưa cấu hình.
// Đây là đường dùng single-target (không có failoverModel đứng giữa tự lo Acquire/Record),
// nên bọc rate limit tại đây qua singleTargetModel — instance bên trong ms.models/ms.Default
// LUÔN là ChatModel raw (xem createModelFromConfig) để tránh double-Acquire khi cùng instance
// đó cũng được ForRoleWithFailover dùng làm primary bên trong failoverModel.
func (ms *ModelSet) ForRole(role string) agentcore.ChatModel {
	if m, ok := ms.models[role]; ok {
		return &singleTargetModel{m}
	}
	return &singleTargetModel{ms.Default}
}

// ForRoleWithFailover trả về mô hình vai trò có fallback cấp độ từng yêu cầu.
// Chỉ có hiệu lực khi vai trò đó được cấu hình tường minh fallbacks; nếu không sẽ thoái hóa về mô hình thông thường.
func (ms *ModelSet) ForRoleWithFailover(role string, report FailoverReporter) agentcore.ChatModel {
	primary, ok := ms.models[role]
	if !ok {
		return &singleTargetModel{ms.Default}
	}
	targets := ms.fallbacks[role]
	if len(targets) == 0 {
		return &singleTargetModel{primary}
	}
	// primary/fallbacks giữ ChatModel raw; failoverModel tự Acquire/Record theo target đang
	// chọn (rotation) — KHÔNG bọc thêm singleTargetModel ở đây, nếu không sẽ double-Acquire
	// trên cùng 1 Limiter (deadlock khi MaxConcurrent>0, double-spend quota khi không).
	return &failoverModel{
		role:      role,
		primary:   primary,
		fallbacks: append([]modelTarget(nil), targets...),
		report:    report,
	}
}

// Summary trả về tóm tắt phân bổ mô hình (dùng cho log).
func (ms *ModelSet) Summary() string {
	var parts []string
	for role, m := range ms.models {
		provider, name := m.Current()
		parts = append(parts, fmt.Sprintf("%s=%s/%s", role, provider, name))
	}
	if len(parts) == 0 {
		provider, name := ms.Default.Current()
		return fmt.Sprintf("default=%s/%s", provider, name)
	}
	provider, name := ms.Default.Current()
	return fmt.Sprintf("default=%s/%s %s", provider, name, strings.Join(parts, " "))
}

// CurrentSelection trả về provider/model đang có hiệu lực của vai trò.
// Khi role rỗng hoặc là "default" thì trả về mô hình mặc định.
func (ms *ModelSet) CurrentSelection(role string) (provider, model string, explicit bool) {
	if role == "" || role == "default" {
		provider, model = ms.Default.Current()
		return provider, model, true
	}
	if sw, ok := ms.models[role]; ok {
		provider, model = sw.Current()
		return provider, model, true
	}
	provider, model = ms.Default.Current()
	return provider, model, false
}

// Swap chuyển đổi mô hình mặc định hoặc mô hình của vai trò chỉ định.
// Khi role rỗng hoặc là "default" thì chuyển mô hình mặc định; các vai trò khác được ghi đè tường minh.
func (ms *ModelSet) Swap(role, provider, model string) error {
	pc, ok := ms.config.Providers[provider]
	if !ok {
		return fmt.Errorf("provider %q is not configured: %w", provider, errs.ErrConfig)
	}
	var roleExtra map[string]any
	if role != "" && role != "default" {
		if rc, ok := ms.config.Roles[role]; ok {
			roleExtra = rc.ExtraBody
		}
	}
	next, err := createModelFromConfig(provider, model, pc, roleExtra, make(map[string]agentcore.ChatModel))
	if err != nil {
		return fmt.Errorf("chuyển đổi mô hình thất bại: %w", err)
	}

	if role == "" || role == "default" {
		ms.Default.Swap(provider, model, next)
		return nil
	}

	if !knownRoles[role] {
		return fmt.Errorf("unknown role %q: %w", role, errs.ErrConfig)
	}

	if existing, ok := ms.models[role]; ok {
		existing.Swap(provider, model, next)
		return nil
	}
	ms.models[role] = NewSwappableModel(provider, model, next)
	return nil
}

// ModelName trích xuất tên mô hình hiện tại từ ChatModel; trả về chuỗi rỗng nếu thất bại.
// Hỗ trợ hoán đổi nóng của SwappableModel: luôn trả về giá trị mới nhất tại thời điểm gọi.
func ModelName(m agentcore.ChatModel) string {
	if info, ok := m.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info().Name
	}
	return ""
}

// NewModelSet tạo tập hợp đa mô hình từ cấu hình.
// Các tổ hợp provider+model giống nhau sẽ tái sử dụng cùng một instance.
func NewModelSet(cfg Config) (*ModelSet, error) {
	cache := make(map[string]agentcore.ChatModel)

	// Tạo mô hình mặc định
	defaultPC := cfg.DefaultProviderConfig()
	defaultModel, err := createModelFromConfig(cfg.Provider, cfg.ModelName, defaultPC, nil, cache)
	if err != nil {
		return nil, fmt.Errorf("default model: %w", err)
	}

	ms := &ModelSet{
		Default:   NewSwappableModel(cfg.Provider, cfg.ModelName, defaultModel),
		models:    make(map[string]*SwappableModel),
		fallbacks: make(map[string][]modelTarget),
		config:    cfg,
	}

	// Tạo mô hình ghi đè theo vai trò
	for role, rc := range cfg.Roles {
		pc, ok := cfg.Providers[rc.Provider]
		if !ok {
			return nil, fmt.Errorf("role %s references unknown provider %q: %w", role, rc.Provider, errs.ErrConfig)
		}
		m, err := createModelFromConfig(rc.Provider, rc.Model, pc, rc.ExtraBody, cache)
		if err != nil {
			return nil, fmt.Errorf("role %s model: %w", role, err)
		}
		ms.models[role] = NewSwappableModel(rc.Provider, rc.Model, m)
		slog.Info("Phân bổ mô hình theo vai trò", "module", "config", "role", role, "provider", rc.Provider, "model", rc.Model)
		if len(rc.Fallbacks) == 0 {
			continue
		}

		targets := make([]modelTarget, 0, len(rc.Fallbacks))
		for _, fallback := range rc.Fallbacks {
			fpc, ok := cfg.Providers[fallback.Provider]
			if !ok {
				return nil, fmt.Errorf("role %s fallback references unknown provider %q: %w", role, fallback.Provider, errs.ErrConfig)
			}
			fm, err := createModelFromConfig(fallback.Provider, fallback.Model, fpc, rc.ExtraBody, cache)
			if err != nil {
				return nil, fmt.Errorf("role %s fallback %s/%s: %w", role, fallback.Provider, fallback.Model, err)
			}
			targets = append(targets, modelTarget{
				provider: fallback.Provider,
				name:     fallback.Model,
				model:    fm,
			})
		}
		ms.fallbacks[role] = targets
	}

	return ms, nil
}

// createModelFromConfig tạo hoặc tái sử dụng instance ChatModel.
//
// Trả về ChatModel RAW (KHÔNG bọc rate limit) — cố ý. Instance này có thể bị dùng theo 2 kiểu
// khác nhau tùy call site: (a) single-target trực tiếp (Default, ForRole, role không fallback)
// — nơi cần đúng 1 lớp Acquire/Record, bọc bằng singleTargetModel tại call site; (b) target bên
// trong failoverModel (primary hoặc fallback) — failoverModel tự Acquire/Record theo target
// đang chọn (rotation). Nếu bọc rate limit sẵn ở đây, failoverModel sẽ double-Acquire trên cùng
// 1 Limiter (deadlock khi MaxConcurrent>0 vì tự chờ semaphore mình đang giữ; double-spend quota
// RPM/RPD/TPM khi MaxConcurrent=0) — xem task-C-report.md, mục Critical fix.
func mergeExtraBody(provider, role map[string]any) map[string]any {
	if len(provider) == 0 && len(role) == 0 {
		return nil
	}
	res := make(map[string]any)
	for k, v := range provider {
		res[k] = v
	}
	for k, v := range role {
		res[k] = v
	}
	return res
}

func createModelFromConfig(providerKey, model string, pc ProviderConfig, roleExtra map[string]any, cache map[string]agentcore.ChatModel) (agentcore.ChatModel, error) {
	merged := mergeExtraBody(pc.ExtraBody, roleExtra)
	var extraPart string
	if len(merged) > 0 {
		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s:%v", k, merged[k]))
		}
		extraPart = "|" + strings.Join(parts, ";")
	}
	cacheKey := providerKey + "|" + model + extraPart

	if m, ok := cache[cacheKey]; ok {
		return m, nil
	}

	providerType, err := pc.ProviderType(providerKey)
	if err != nil {
		return nil, fmt.Errorf("phân tích kiểu nhà cung cấp thất bại: %w", err)
	}

	m, err := llm.NewModel(providerType, model,
		llm.WithAPIKey(pc.APIKey),
		llm.WithBaseURL(pc.BaseURL),
		llm.WithStreamIdleTimeout(streamIdleTimeout),
		llm.WithProviderExtra(pc.Extra),
		llm.WithExtra(merged),
	)
	if err != nil {
		return nil, fmt.Errorf("provider %s (%s): %w: %w", providerKey, providerType, errs.ErrProvider, err)
	}

	// Đăng ký giới hạn hiệu lực (RPM/RPD/TPM/MaxConcurrent) vào registry global. Rate limit áp
	// dụng bằng cách bọc runtime tại call site (singleTargetModel) hoặc trong failoverModel —
	// KHÔNG bọc ở đây.
	eff := pc.EffectiveRateLimit(model)
	ratelimit.Global.Register(providerKey, model, ratelimit.Limits{
		RPM: eff.RPM, RPD: eff.RPD, TPM: eff.TPM, MaxConcurrent: eff.MaxConcurrent,
	})
	cache[cacheKey] = m
	return m, nil
}

type failoverModel struct {
	role      string
	primary   *SwappableModel
	fallbacks []modelTarget
	report    FailoverReporter
}

// rotationTargets trả về danh sách target theo thứ tự ưu tiên (primary trước).
// Primary luôn đứng đầu để tự động quay lại model gốc khi nó hồi quota, giữ chất lượng.
func (m *failoverModel) rotationTargets() []modelTarget {
	targets := []modelTarget{m.currentTarget()}
	for _, t := range m.fallbacks {
		if t.provider == targets[0].provider && t.name == targets[0].name {
			continue
		}
		targets = append(targets, t)
	}
	return targets
}

// pickAvailable chọn target đầu còn quota (TryAcquire ok). Nếu mọi target đều chạm giới hạn,
// chờ target có retryAfter ngắn nhất rồi Acquire (block-and-wait). Trả về target + handle đã chiếm slot.
func (m *failoverModel) pickAvailable(ctx context.Context, est int) (modelTarget, *ratelimit.Handle, error) {
	targets := m.rotationTargets()
	var bestRA time.Duration = 1 << 62
	var bestIdx int
	for i, t := range targets {
		lim := ratelimit.Global.For(t.provider, t.name)
		if h, ra, ok := lim.TryAcquire(est); ok {
			return t, h, nil
		} else if ra < bestRA {
			bestRA, bestIdx = ra, i
		}
	}
	// Tất cả chạm limit → chờ target rẻ nhất.
	t := targets[bestIdx]
	slog.Info("ratelimit: mọi model chạm giới hạn, chờ target rẻ nhất",
		"role", m.role, "target", t.provider+"/"+t.name, "wait", bestRA)
	h, err := ratelimit.Global.For(t.provider, t.name).Acquire(ctx, est)
	return t, h, err
}

func (m *failoverModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	est := estimateTokens(messages)
	origin := m.currentTarget()

	target, h, err := m.pickAvailable(ctx, est)
	if err != nil {
		return nil, err
	}
	if target.provider != origin.provider || target.name != origin.name {
		m.reportFailover(origin, target, "rate_limit", nil)
	}
	resp, gerr := target.model.Generate(ctx, messages, tools, opts...)
	h.Record(responseTokens(resp), gerr)
	if gerr == nil {
		return resp, nil
	}

	// Lỗi vận hành (không phải quota) → path failover cũ 1 lần.
	if next, reason, ok := m.pickFallback(target, gerr); ok {
		m.reportFailover(target, next, reason, gerr)
		nh, aerr := ratelimit.Global.For(next.provider, next.name).Acquire(ctx, est)
		if aerr != nil {
			return nil, aerr
		}
		r2, e2 := next.model.Generate(ctx, messages, tools, opts...)
		nh.Record(responseTokens(r2), e2)
		return r2, e2
	}
	return nil, gerr
}

// GenerateStream giữ khung goto-retry cũ, nhưng: (a) chọn target đầu qua pickAvailable
// (rotation, ưu tiên quay lại primary), (b) chiếm handle limiter tương ứng, (c) forward
// event xuống caller, (d) StreamEventError lần đầu → Record lỗi + thử pickFallback (path
// failover vận hành cũ) đúng 1 lần, chiếm handle mới cho target kế; (e) StreamEventDone →
// Record token thực từ Usage.
func (m *failoverModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	out := make(chan agentcore.StreamEvent, 100)
	est := estimateTokens(messages)

	go func() {
		defer close(out)

		origin := m.currentTarget()
		current, handle, perr := m.pickAvailable(ctx, est)
		if perr != nil {
			out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: perr}
			return
		}
		if current.provider != origin.provider || current.name != origin.name {
			m.reportFailover(origin, current, "rate_limit", nil)
		}
		fallbackUsed := false

	retry:
		source, resp, serr := m.startAttempt(ctx, current, messages, tools, opts...)
		if serr != nil {
			recorded := false
			if !fallbackUsed {
				if next, reason, ok := m.pickFallback(current, serr); ok {
					handle.Record(0, serr)
					recorded = true
					fallbackUsed = true
					m.reportFailover(current, next, reason, serr)
					current = next
					nh, aerr := ratelimit.Global.For(next.provider, next.name).Acquire(ctx, est)
					if aerr != nil {
						out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: aerr}
						return
					}
					handle = nh
					goto retry
				}
			}
			if !recorded {
				handle.Record(0, serr)
			}
			out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: serr}
			return
		}
		if resp != nil {
			handle.Record(responseTokens(resp), nil)
			out <- agentcore.StreamEvent{
				Type:       agentcore.StreamEventDone,
				Message:    resp.Message,
				StopReason: resp.Message.StopReason,
			}
			return
		}

		forwarded := false
		for ev := range source {
			switch ev.Type {
			case agentcore.StreamEventError:
				recorded := false
				if ev.Err != nil && !forwarded && !fallbackUsed {
					if next, reason, ok := m.pickFallback(current, ev.Err); ok {
						handle.Record(0, ev.Err)
						recorded = true
						fallbackUsed = true
						m.reportFailover(current, next, reason, ev.Err)
						current = next
						nh, aerr := ratelimit.Global.For(next.provider, next.name).Acquire(ctx, est)
						if aerr != nil {
							out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: aerr}
							return
						}
						handle = nh
						goto retry
					}
				}
				if !recorded {
					handle.Record(0, ev.Err)
				}
				out <- ev
				return
			case agentcore.StreamEventDone:
				toks := 0
				if ev.Message.Usage != nil {
					toks = usageTokens(ev.Message.Usage)
				}
				handle.Record(toks, nil)
				out <- ev
				return
			default:
				forwarded = true
				out <- ev
			}
		}
		// source đóng mà không phát StreamEventError/Done tường minh (bất thường) — vẫn
		// phải nhả slot đã chiếm để không rò rỉ handle.
		handle.Record(0, nil)
	}()

	return out, nil
}

func (m *failoverModel) SupportsTools() bool {
	return m.primary != nil && m.primary.SupportsTools()
}

func (m *failoverModel) ProviderName() string {
	if m.primary == nil {
		return ""
	}
	return m.primary.ProviderName()
}

func (m *failoverModel) Info() llm.ModelInfo {
	if m.primary == nil {
		return llm.ModelInfo{}
	}
	return m.primary.Info()
}

func (m *failoverModel) currentTarget() modelTarget {
	if m.primary == nil {
		return modelTarget{}
	}
	provider, name := m.primary.Current()
	return modelTarget{
		provider: provider,
		name:     name,
		model:    m.primary,
	}
}

func (m *failoverModel) pickFallback(current modelTarget, err error) (modelTarget, string, bool) {
	if err == nil || current.model == nil {
		return modelTarget{}, "", false
	}
	if errors.Is(err, context.Canceled) {
		return modelTarget{}, "", false
	}

	if !agentcore.IsFailoverEligible(err) {
		return modelTarget{}, agentcore.FailoverReason(err), false
	}
	reason := agentcore.FailoverReason(err)
	for _, target := range m.fallbacks {
		if target.provider == current.provider && target.name == current.name {
			continue
		}
		if target.model == nil {
			continue
		}
		return target, reason, true
	}
	return modelTarget{}, reason, false
}

func (m *failoverModel) reportFailover(from, to modelTarget, reason string, err error) {
	if m.report != nil {
		m.report(FailoverEvent{
			Role:         m.role,
			Reason:       reason,
			FromProvider: from.provider,
			FromModel:    from.name,
			ToProvider:   to.provider,
			ToModel:      to.name,
			Err:          err,
		})
	}
}

func (m *failoverModel) startAttempt(ctx context.Context, target modelTarget, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, *agentcore.LLMResponse, error) {
	if target.model == nil {
		return nil, nil, fmt.Errorf("no model configured")
	}

	streamCh, err := target.model.GenerateStream(ctx, messages, tools, opts...)
	if err == nil {
		return streamCh, nil, nil
	}

	resp, genErr := target.model.Generate(ctx, messages, tools, opts...)
	if genErr != nil {
		return nil, nil, genErr
	}
	return nil, resp, nil
}

// singleTargetModel bọc 1 *SwappableModel (Default hoặc role không có fallback) để áp rate
// limit tại nơi dùng single-target — nơi KHÔNG có failoverModel đứng giữa tự lo Acquire/Record.
// Tra provider/model ĐỘNG qua Current() ở mỗi lần gọi (không cache tĩnh) để luôn khớp instance
// đang thực sự được dùng, kể cả sau khi Swap() nóng lúc runtime đổi sang provider/model khác.
//
// Instance ChatModel bên trong (do createModelFromConfig trả về) LUÔN là raw, không tự bọc rate
// limit — tránh double-Acquire khi cùng instance đó cũng được failoverModel dùng làm primary
// (failoverModel tự Acquire/Record theo target đang chọn, xem rotationTargets/pickAvailable).
type singleTargetModel struct {
	*SwappableModel
}

func (m *singleTargetModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	provider, model := m.SwappableModel.Current()
	lim := ratelimit.Global.For(provider, model)
	h, err := lim.Acquire(ctx, estimateTokens(messages))
	if err != nil {
		return nil, err
	}
	resp, gerr := m.SwappableModel.Generate(ctx, messages, tools, opts...)
	h.Record(responseTokens(resp), gerr)
	return resp, gerr
}

func (m *singleTargetModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	provider, model := m.SwappableModel.Current()
	lim := ratelimit.Global.For(provider, model)
	h, err := lim.Acquire(ctx, estimateTokens(messages))
	if err != nil {
		return nil, err
	}
	source, serr := m.SwappableModel.GenerateStream(ctx, messages, tools, opts...)
	if serr != nil {
		h.Record(0, serr)
		return nil, serr
	}

	out := make(chan agentcore.StreamEvent, 100)
	go func() {
		defer close(out)
		var streamErr error
		var toks int
		for ev := range source {
			if ev.Type == agentcore.StreamEventError {
				streamErr = ev.Err
			}
			if ev.Type == agentcore.StreamEventDone && ev.Message.Usage != nil {
				toks = usageTokens(ev.Message.Usage)
			}
			out <- ev
		}
		h.Record(toks, streamErr)
	}()

	return out, nil
}

// estimateTokens ước lượng thô token đầu vào để pre-gate TPM (≈ 4 ký tự/token).
func estimateTokens(messages []agentcore.Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.TextContent())
	}
	return chars / 4
}

// responseTokens trích số token thực từ phản hồi (không stream); 0 nếu thiếu Usage.
func responseTokens(resp *agentcore.LLMResponse) int {
	if resp == nil || resp.Message.Usage == nil {
		return 0
	}
	return usageTokens(resp.Message.Usage)
}

// usageTokens ưu tiên TotalTokens do provider báo cáo; fallback Input+Output.
func usageTokens(u *agentcore.Usage) int {
	if u == nil {
		return 0
	}
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output
}
