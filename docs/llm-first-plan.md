# Kế hoạch LLM-first: tối ưu ưu điểm LLM — lật nhược điểm thành ưu điểm

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Nâng chất lượng văn chương của chương đầu ra bằng cách dùng THÊM LLM đúng chỗ (chọn lọc, kiểm chứng chéo, ensemble) thay vì thêm guardrail code; code chỉ làm việc code giỏi — đo, đếm, so khớp, định tuyến.

**Architecture:** Giữ nguyên khung hiện có (architecture.md §2.1: LLM phán quyết, Host định tuyến qua flow router thuần; tool chỉ IO). Mọi cơ chế mới đều là (a) tham số hoá model theo role, (b) tool ghi sự thật mới lên đĩa, (c) nhánh route mới suy từ sự thật đĩa, (d) prompt bổ sung nhiệm vụ. KHÔNG có LLM call bên trong tool, KHÔNG state mới trong Progress trừ khi bắt buộc — mọi fact suy từ file trên đĩa (crash-safe, cùng triết lý `HasPendingFlatReview`).

**Tech Stack:** Go 1.25+, agentcore (subagent.Config, StopAfterToolResult), litellm (`llm.WithExtra`), store file-based hiện có.

## Global Constraints

- Điều kiện tiên quyết: nhánh `feat/ratelimit` đã merge (rotation gánh chi phí nhân số call; Part E tái dùng `ForRoleWithFailover`).
- Model đích là fleet local rẻ (gemma-27b / mistral-small / qwen3-30b-a3b qua llama.cpp) — mọi part chỉ chạy trên model đã qua Part 0A probe; thiết kế theo 3 nguyên tắc mục "Điều chỉnh cho fleet model rẻ".
- `go build ./...` exit 0 và `go test ./...` xanh sau MỖI task. Chạy thêm `go vet ./...` trước commit.
- Mỗi Part một nhánh `feat/<slug>`; mỗi Step "Commit" là một commit thật.
- Commit message: ASCII không dấu, Conventional Commits, kiểu hiện hành của repo (`feat(quality): ...`, `fix(flow): ...`).
- Không LLM call trong tool (`internal/tools/*`): tool chỉ IO + validate + suy verdict xác định từ số liệu.
- `flow.Route` giữ thuần túy: không IO; mọi dữ kiện mới vào qua `State`, IO tập trung ở `LoadState` hoặc do Dispatcher tiêm (config).
- Text hướng người dùng / prompt: tiếng Việt đủ dấu. KHÔNG thêm tiếng Trung.
- Field JSON mới đều `omitempty` — file cũ thiếu field đọc ra zero value = hành vi cũ (backward-compatible, theo tiền lệ `ReviewEntry` Wave 2 cũ).
- Con số ngưỡng (temperature gợi ý, ngưỡng offender, trần vote…) phải nằm ở const có comment giải thích, không magic number rải rác.
- Trước khi bắt đầu: kiểm tra trạng thái `docs/improvement-plan.md` Wave 3 (Part L POV contract, Part M voice card) — nếu đang chạy, KHÔNG chạy song song Part C/F của plan này (đụng chung `novel_context_builders.go` + prompts). Part H/I của plan cũ đã có trong code (router ép flat review; trần rewrite `maxRewritePerChapter=3` trong `save_review.go`).

## Bảng ánh xạ triết lý

| Nhược LLM | Lật thành ưu | Part |
|---|---|---|
| Writer và editor dùng chung "một chế độ não" | Mỗi role một sampling profile | A |
| Nghiện cliché, tự lặp xuyên chương | Code đo tật → phơi thành sự thật tại commit, LLM tự tránh | B |
| Anchor phong cách lấy chương SỚM nhất, không phải HAY nhất | Output tốt nhất tự thành văn mẫu | C |
| Tự khai summary không ai kiểm; lỗi ngủ 5 chương mới bị bắt | LLM kiểm LLM mỗi chương, rẻ, chặn context rot | D |
| Tật văn của model tự nó không thấy | Model KHÁC nhìn thấy ngay — rewrite bằng role riêng | E |
| Trí nhớ cửa sổ cứng, bài học review trôi mất | LLM tự chưng cất sổ tay tác giả | F |
| Output dao động (variance) | Variance = nguồn lựa chọn: N nháp, editor chọn | G |
| Một lượt chấm điểm nhiễu | K lượt chấm độc lập, code gộp median/majority | H |

## Điều chỉnh cho fleet model rẻ (bản sửa 2026-07-27)

Model thực tế sẽ dùng cho writer là model NHỎ chạy local qua llama.cpp: gemma-27b class, mistral-small, qwen3-30b-a3b. Điều này phá giả định "judge đáng tin" của bản plan đầu → ba nguyên tắc bổ sung, ưu tiên cao hơn mọi phần bên dưới khi mâu thuẫn:

1. **So sánh tương đối > chấm tuyệt đối.** Judge yếu chọn "bản nào hay hơn" đáng tin hơn hẳn chấm 0-100 (điểm dồn cục 70-85, gate 60/80 hiếm khi nổ). → Part G (chọn so sánh) ưu tiên CAO hơn Part H (điểm tuyệt đối); H hoãn đến khi có dữ liệu thật.
2. **Verify > generate.** Model nhỏ đối chiếu "sự kiện X có trong văn không" tốt hơn nhiều so với sáng tác/chấm thẩm mỹ. → Part D giữ nguyên giá trị; Part F (chưng cất notebook) hạ xuống opt-in.
3. **Đa model > đa lượt.** K lượt từ CÙNG một model rẻ = K lần cùng một bias. Fleet 3-4 model là tài sản: candidate/vote/reviser mỗi thứ một model khác nhau.

Judge cuối cùng đáng tin duy nhất của hệ thống là NGƯỜI DÙNG → Part 0D (human gate).

---

# WAVE 0 — Nền cho model rẻ (LÀM TRƯỚC TẤT CẢ)

## Part 0A — Model probe: smoke test năng lực từng model

### Bối cảnh

Mọi part sau đều giả định model gọi được tool + trả JSON đúng schema + viết tiếng Việt sạch. gemma qua llama.cpp thường không có tool template chuẩn; qwen3-a3b/mistral-small tỷ lệ hỏng JSON với schema phức tạp (save_review 7 chiều là schema nặng nhất hệ thống) chưa ai đo. Model rớt probe thì cấm vào role tương ứng — đỡ debug mù về sau.

### Files sở hữu

- Create: `scripts/modelprobe/main.go`
- Create: `docs/model-notes.md` (kết quả đo từng model — cập nhật tay sau mỗi lần chạy)

### Task 0A1: probe harness

- [x] **Step 1:** Viết `scripts/modelprobe/main.go`: load config bằng ĐÚNG hàm mà cmd chính dùng (grep `LoadConfig`/nơi `bootstrap.Config` được đọc trong `internal/entry` — dùng lại, không tự parse). Với MỖI role đã cấu hình (default/coordinator/architect/writer/editor/reviser): lấy model qua `bootstrap.NewModelSet(cfg)` + `ForRole(role)`, chạy 4 probe:

```go
// Probe 1 — tool-call round trip: 1 tool "echo" schema {text string required};
// prompt "gọi tool echo với text='ping'". Đạt: nhận đúng 1 tool call, args parse được, text=="ping".
// Probe 2 — JSON schema nặng: đưa tool giả "score" với schema COPY 7 chiều của save_review
// (dimensions array + issues + verdict enum); yêu cầu chấm một đoạn văn 200 từ cho sẵn.
// Chạy 5 lần, đếm tỷ lệ args unmarshal + validateDimensions pass. Đạt: ≥4/5.
// Probe 3 — tiếng Việt: "viết 150-200 từ tả cơn mưa, tiếng Việt". Đạt: 0 ký tự CJK
// (regex [\p{Han}]), tỷ lệ ký tự có dấu > 15% (văn Việt thật ~20-30%), không markdown rác.
// Probe 4 — context hiệu dụng: nhét câu mốc "Mã khóa là XUANVU-<n>" ở ĐẦU, đệm ~12k token
// văn bản trung tính, hỏi mã khóa ở cuối. Chạy với 12k/24k/48k đệm đến khi sai. Đạt mức nào ghi mức đó.
```

In bảng kết quả stdout + hướng dẫn chép vào `docs/model-notes.md`.

- [x] **Step 2:** `go build ./scripts/modelprobe/` xanh; chạy thật với config local llama.cpp, điền `docs/model-notes.md`: mỗi model một dòng — tool-call OK?, json-rate, việt-OK?, context hiệu dụng, kết luận "được phép vào role nào".
- [x] **Step 3: Commit** — `git commit -m "feat(scripts): modelprobe — smoke test nang luc model truoc khi gan role"`

### Acceptance

- Bảng model-notes.md có đủ 4 model dự kiến; model rớt Probe 1/2 KHÔNG được cấu hình vào writer/editor (ghi rõ trong model-notes.md); mọi part sau chỉ chạy trên model đã qua probe.

## Part 0B — Structured output: diệt vòng lặp JSON hỏng (spike)

### Bối cảnh

Tool args hỏng → validation error → model retry → đốt token, model nhỏ dính nặng nhất. llama.cpp có grammar-constrained tool calling (`--jinja` + chat template đúng); nếu bật được thì args hợp lệ ~100% ở tầng server, rẻ hơn mọi retry logic.

### Files sở hữu

- Modify: `docs/model-notes.md` (kết quả spike)
- Modify (CHỈ NẾU cần): `internal/tools/draft_chapter.go` + các tool `StrictSchema()` — thêm đường tắt config `quality.strict_schema=false` cho backend không chịu strict (quyết sau khi đo)

### Task 0B1: spike

- [ ] **Step 1:** Với từng model trên llama.cpp server: bật `--jinja` (+ `--chat-template` nếu model cần), chạy lại Probe 2 của 0A. So tỷ lệ valid trước/sau. Ghi vào model-notes.md: flags server khuyến nghị từng model.
- [ ] **Step 2:** Nếu sau grammar vẫn <5/5: thử `"response_format"` qua `extra_body` role editor (Part A đã có cơ chế) — lưu ý áp cho MỌI request của role đó, chỉ hợp với role thuần structured (editor vote). Ghi kết luận.
- [ ] **Step 3:** Chỉ khi cả hai đường fail với một model đáng giữ: implement cờ `StrictSchema` tắt được qua config (task riêng, TDD đầy đủ lúc đó). Commit doc — `git commit -m "docs(models): ket qua spike grammar/structured output llama.cpp"`

### Acceptance

- model-notes.md có mục "flags llama.cpp khuyến nghị" từng model; tỷ lệ JSON valid sau spike ≥ 4/5 cho mọi model được giữ ở role writer/editor.

## Part 0C — Context profile cho cửa sổ nhỏ

### Bối cảnh

Model local chạy 16-32k context thực dụng; envelope novel_context hiện dựng cho model cloud cửa sổ lớn. Tràn cửa sổ = compact/truncate giữa chừng = chất lượng sập không báo. Cơ chế `ResolveContextWindow` + `trimByBudget` (`novel_context.go:480`) đã có — thiếu profile RÚT GỌN chủ động theo window.

### Files sở hữu

- Modify: `internal/domain` (nơi `NewContextProfile` định nghĩa — đọc trước khi sửa), `internal/tools/novel_context_builders.go` (previous_tail, anchors), `internal/tools/novel_context.go` (writerReferences)
- Test: test profile trong package domain + `novel_context_test.go`

### Task 0C1: profile nhỏ

- [ ] **Step 1: Test fail** — bảng profile theo window (đặt tên hàm theo convention thực tế sau khi đọc `NewContextProfile`):

```go
func TestContextProfileSmallWindow(t *testing.T) {
	big := NewContextProfileForWindow(100, 200_000)
	small := NewContextProfileForWindow(100, 24_000)
	if small.SummaryWindow >= big.SummaryWindow {
		t.Fatalf("window nho phai rut summary: %d vs %d", small.SummaryWindow, big.SummaryWindow)
	}
	if small.SummaryWindow < 2 {
		t.Fatalf("san toi thieu 2 chuong: %d", small.SummaryWindow)
	}
}
```

- [ ] **Step 2-3:** Implement: `NewContextProfileForWindow(totalChapters, contextWindow int)` — dưới ngưỡng `smallWindowTokens = 32_000`: SummaryWindow giảm nửa (sàn 2), cờ `Compact bool` bật. Builders đọc cờ: `previous_tail` 800→400 rune; style anchors 3→2; `writerReferences` chỉ giữ `anti_ai_tone` (bỏ guides chương ≤3, giữ template chương 1). Call site truyền window đã resolve của writer (đường `cfg.ResolveContextWindow` — build.go:260 đã có mẫu). Hằng số có comment lý do.
- [ ] **Step 4-5:** Test pass toàn package; commit — `git commit -m "feat(context): profile rut gon cho model cua so nho"`

### Acceptance

- Window 24k: envelope đo được nhỏ hơn rõ (log token estimate trước/sau trong test hoặc chạy tay); window lớn: profile y hệt cũ, test cũ xanh nguyên.

## Part 0D — Human gate: mốc duyệt của người dùng

### Bối cảnh

Fleet toàn model rẻ → judge đáng tin duy nhất là người dùng. Cần chốt chặn định kỳ: mỗi N chương flow DỪNG, thông báo, người dùng đọc thử rồi cho chạy tiếp (kèm ghi chú — ghi chú đi vào notebook/steering). `report.md` Phase 2 đã nêu human-in-loop, chưa thành code.

### Files sở hữu

- Modify: `internal/bootstrap/config.go` (`Quality.HumanGateEvery int`, validate ≥0), `internal/store/world.go` (ack IO), `internal/host/flow/router.go` + `state.go` + `dispatcher.go`, `internal/entry/tui/` (lệnh ack — đọc command registry hiện có trước), `internal/notify` wiring (dùng kênh notify sẵn có kiểu onRepeat), `internal/bootstrap/config.example.jsonc`
- Test: `router_test.go`, `world_test.go`

### Task 0D1: fact + route + ack

- [ ] **Step 1: Test fail** — router:

```go
func TestRouteHumanGate(t *testing.T) {
	s := State{
		Progress: &domain.Progress{Phase: domain.PhaseWriting, TotalChapters: 40,
			CompletedChapters: []int{1,2,3,4,5,6,7,8,9,10}, CurrentChapter: 10},
		LastCompleted: 10, HumanGatePending: true,
	}
	inst := Route(s)
	if inst == nil || inst.Agent != "" || !strings.Contains(inst.Task, "chờ người dùng duyệt") {
		t.Fatalf("gate pending phai tra instruction dung-cho, got %+v", inst)
	}
}
```

- [ ] **Step 2-3:** Implement:
  - `world.go`: `SaveHumanGateAck(chapter int, note string)` / `HasHumanGateAck(chapter int) bool` — file `reviews/%02d-humangate.json` `{chapter, note, at}`.
  - `State`: `HumanGateEvery int` (Dispatcher tiêm), `HumanGatePending bool` (LoadState: `LastCompleted>0 && HumanGateEvery>0 && LastCompleted%HumanGateEvery==0 && !HasHumanGateAck(LastCompleted)`).
  - `Route`: nhánh ưu tiên NGAY SAU PendingRewrites: gate pending → trả `Instruction{Agent: "", Task: "DỪNG: mốc duyệt chương N — chờ người dùng duyệt (/gate)", Reason: "human gate"}`. Agent rỗng = lệnh dừng-chờ, KHÔNG trả nil (nil = coordinator LLM tự do — model yếu sẽ freelance viết tiếp).
  - `Dispatcher.Dispatch`: `inst.Agent == ""` → KHÔNG FollowUp lệnh gọi subagent; thay bằng FollowUp thông điệp "[Host] Mốc duyệt người dùng: không gọi subagent, thông báo người dùng và chờ" + bắn `onHumanGate` callback (mẫu y hệt `onRepeat` — telemetry, host nối vào notify + TUI banner).
  - TUI: lệnh `/gate ok [ghi chú]` (đăng ký theo đúng registry lệnh hiện có — đọc `internal/entry/tui` trước) → `SaveHumanGateAck` → gọi `Dispatcher.Dispatch()` chạy tiếp. Ghi chú không rỗng → đồng thời ghi vào steering/directive sẵn có (`SaveDirectiveTool` path) để writer chương sau đọc được.
- [ ] **Step 4-5:** Test pass; chạy tay: `human_gate_every=2`, viết 2 chương → flow đứng + notify; `/gate ok "giọng chương 2 hơi cứng"` → chạy tiếp. Commit — `git commit -m "feat(flow): human gate — moc duyet nguoi dung moi N chuong"`

### Acceptance

- `human_gate_every=0` (mặc định): zero thay đổi. Bật: flow đứng đúng mốc, không lệnh subagent nào phát trong lúc chờ, ack xong chạy tiếp, ghi chú thành directive. Crash lúc chờ → Resume vẫn đứng (fact từ đĩa).

---

# WAVE 1 — Độc lập hoàn toàn, chạy song song được (A ∥ B ∥ C)

## Part A — Sampling theo role (`extra_body` trong RoleConfig)

### Bối cảnh & nguyên nhân

Sampling param hiện chỉ đặt được per-provider qua `ProviderConfig.ExtraBody` (`internal/bootstrap/config.go:56-59`) → writer (cần temp cao, văn sáng tạo) và editor (cần temp thấp, chấm scorecard ổn định — gate `save_review.go:327` nhạy với nhiễu điểm) buộc dùng chung temperature. `createModelFromConfig` (`models.go:278`) đã truyền `llm.WithExtra(pc.ExtraBody)` — chỉ cần lớp merge theo role.

### Files sở hữu (KHÔNG đụng file khác)

- Modify: `internal/bootstrap/config.go` (RoleConfig)
- Modify: `internal/bootstrap/models.go` (merge + cache key + call sites)
- Modify: `internal/bootstrap/config.example.jsonc`
- Test: `internal/bootstrap/models_extra_test.go` (mới), test config parse thêm vào file test config hiện có

### Interfaces

- Produces: `RoleConfig.ExtraBody map[string]any` (json `extra_body`); `mergeExtraBody(provider, role map[string]any) map[string]any`; `createModelFromConfig(providerKey, model string, pc ProviderConfig, roleExtra map[string]any, cache map[string]agentcore.ChatModel)` — chữ ký MỚI có `roleExtra`.

### Task A1: RoleConfig.ExtraBody + merge

- [ ] **Step 1: Viết test fail**

`internal/bootstrap/models_extra_test.go`:

```go
package bootstrap

import "testing"

func TestMergeExtraBody(t *testing.T) {
	provider := map[string]any{"temperature": 0.8, "min_p": 0.05}
	role := map[string]any{"temperature": 0.95}

	got := mergeExtraBody(provider, role)
	if got["temperature"] != 0.95 {
		t.Fatalf("role phai ghi de provider: got %v", got["temperature"])
	}
	if got["min_p"] != 0.05 {
		t.Fatalf("key provider khong bi role dung den phai giu nguyen: got %v", got["min_p"])
	}
	// role rong -> tra ve provider nguyen ven
	if got2 := mergeExtraBody(provider, nil); len(got2) != 2 {
		t.Fatalf("role nil phai tra provider map: %v", got2)
	}
	// khong duoc sua map goc
	if provider["temperature"] != 0.8 {
		t.Fatalf("merge lam ban map provider goc")
	}
}
```

- [ ] **Step 2: Chạy test, xác nhận fail** — `go test ./internal/bootstrap/ -run TestMergeExtraBody -v` → FAIL `undefined: mergeExtraBody`.

- [ ] **Step 3: Implement**

`config.go` — thêm field vào `RoleConfig` (sau `Thinking`, dòng ~139):

```go
	// ExtraBody ghi đè/bổ sung tham số request cho RIÊNG role này (temperature/top_p/min_p...),
	// merge đè lên ProviderConfig.ExtraBody theo từng key. Cho phép writer nhiệt cao (sáng tạo)
	// trong khi editor nhiệt thấp (chấm điểm ổn định) trên cùng một provider.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
```

`models.go` — thêm helper (cạnh `createModelFromConfig`):

```go
// mergeExtraBody gộp extra_body cấp provider với cấp role; role thắng theo từng key.
// Không sửa map đầu vào. role rỗng → trả thẳng provider (giữ nguyên hành vi cũ).
func mergeExtraBody(provider, role map[string]any) map[string]any {
	if len(role) == 0 {
		return provider
	}
	out := make(map[string]any, len(provider)+len(role))
	for k, v := range provider {
		out[k] = v
	}
	for k, v := range role {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 4: Test pass** — `go test ./internal/bootstrap/ -run TestMergeExtraBody -v` → PASS.
- [ ] **Step 5: Commit** — `git commit -m "feat(models): mergeExtraBody — extra_body cap role de len cap provider"`

### Task A2: createModelFromConfig nhận roleExtra + cache key

- [ ] **Step 1: Viết test fail**

Thêm vào `models_extra_test.go`:

```go
func TestCreateModelRoleExtraCacheKey(t *testing.T) {
	pc := ProviderConfig{APIKey: "test-key", BaseURL: "http://127.0.0.1:0", Type: "openai"}
	cache := make(map[string]agentcore.ChatModel)

	m1, err := createModelFromConfig("p", "m", pc, nil, cache)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := createModelFromConfig("p", "m", pc, map[string]any{"temperature": 0.3}, cache)
	if err != nil {
		t.Fatal(err)
	}
	m3, err := createModelFromConfig("p", "m", pc, map[string]any{"temperature": 0.3}, cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(cache) != 2 {
		t.Fatalf("extra khac nhau phai ra 2 entry cache, got %d", len(cache))
	}
	if m1 == m2 {
		t.Fatal("extra khac nhau khong duoc dung chung instance")
	}
	if m2 != m3 {
		t.Fatal("extra giong het phai tai su dung instance")
	}
}
```

(import `"github.com/voocel/agentcore"`; nếu `llm.NewModel` từ chối BaseURL trên thì bỏ BaseURL — client chỉ construct, không gọi mạng.)

- [ ] **Step 2: Fail** — compile error vì chữ ký cũ 4 tham số.

- [ ] **Step 3: Implement** — `models.go`:

```go
func createModelFromConfig(providerKey, model string, pc ProviderConfig, roleExtra map[string]any, cache map[string]agentcore.ChatModel) (agentcore.ChatModel, error) {
	// json.Marshal sort key map → chuỗi ổn định làm phần đuôi cache key;
	// role có extra khác nhau trên cùng provider/model phải là instance khác.
	extraKey, _ := json.Marshal(roleExtra)
	cacheKey := providerKey + "|" + model + "|" + string(extraKey)
	...
	m, err := llm.NewModel(providerType, model,
		llm.WithAPIKey(pc.APIKey),
		llm.WithBaseURL(pc.BaseURL),
		llm.WithStreamIdleTimeout(streamIdleTimeout),
		llm.WithProviderExtra(pc.Extra),
		llm.WithExtra(mergeExtraBody(pc.ExtraBody, roleExtra)),
	)
	...
}
```

(thêm import `encoding/json`). Sửa TẤT CẢ call sites — compiler chỉ chỗ:

1. `NewModelSet` default (`models.go:219`): `createModelFromConfig(cfg.Provider, cfg.ModelName, defaultPC, nil, cache)`
2. `NewModelSet` role (`:237`): `createModelFromConfig(rc.Provider, rc.Model, pc, rc.ExtraBody, cache)`
3. `NewModelSet` fallback (`:253`): `createModelFromConfig(fallback.Provider, fallback.Model, fpc, rc.ExtraBody, cache)` — fallback phục vụ role nào thì mang sampling của role đó.
4. `Swap` (`:181`): tra extra theo role TRƯỚC khi create:

```go
	var roleExtra map[string]any
	if role != "" && role != "default" {
		if rc, ok := ms.config.Roles[role]; ok {
			roleExtra = rc.ExtraBody
		}
	}
	next, err := createModelFromConfig(provider, model, pc, roleExtra, make(map[string]agentcore.ChatModel))
```

- [ ] **Step 4: Pass** — `go test ./internal/bootstrap/ -v` (toàn package, bắt regression configfile_test).
- [ ] **Step 5: Commit** — `git commit -m "feat(models): extra_body theo role — merge de len provider, cache key theo extra"`

### Task A3: config.example.jsonc + doc

- [ ] **Step 1:** Trong block `// "roles": {` (dòng ~74) thêm ví dụ:

```jsonc
  // "roles": {
  //   // Sampling theo role: writer nhiệt cao cho văn sáng tạo, editor nhiệt thấp cho chấm điểm ổn định.
  //   "writer": { "provider": "openrouter", "model": "...", "extra_body": { "temperature": 0.95, "top_p": 0.98 } },
  //   "editor": { "provider": "openrouter", "model": "...", "extra_body": { "temperature": 0.3 } }
  // },
  //
  // Preset khuyến nghị theo model local (llama.cpp) — model nhỏ RẤT nhạy sampling:
  //   qwen3-30b-a3b : writer { "temperature": 0.7, "top_p": 0.8, "min_p": 0.0, "presence_penalty": 1.0 }
  //                   (khuyến nghị chính thức của Qwen3 non-thinking; presence_penalty chống loop)
  //   gemma-27b     : writer { "temperature": 1.0, "top_p": 0.95, "top_k": 64 }
  //   mistral-small : writer { "temperature": 0.7, "top_p": 0.95 }
  //   editor MỌI model: { "temperature": 0.2, "top_p": 0.9 } — chấm điểm cần lặp lại được.
  //   Chạy Part 0A probe lại sau khi đổi preset; số trên là điểm xuất phát, không phải chân lý.
```

- [ ] **Step 2:** `go build ./...` xanh. Commit — `git commit -m "docs(config): vi du extra_body theo role"`

### Acceptance

- Config có `roles.writer.extra_body.temperature=0.95` → request của writer mang temperature 0.95, editor không bị ảnh hưởng (verify bằng test cache key + đọc log request nếu provider hỗ trợ echo).
- Không config `extra_body` role → hành vi y hệt trước (merge trả thẳng provider map, cache key đổi dạng nhưng nhất quán).

### Tác động / mặt trái

- Ưu: rẻ nhất toàn plan; nền cho G (candidate nhiệt cao) và H (vote nhiệt thấp).
- Trái: người dùng đặt temperature vô lý tự chịu (đúng quy ước ExtraBody hiện có — "người dùng tự chịu trách nhiệm").

---

## Part B — Đóng vòng stylestat: offender → sự thật tại commit

### Bối cảnh & nguyên nhân

`stylestat` đo cụm lặp (`top_phrases`), câu lặp verbatim xuyên chương (`repeated_sentences`) nhưng chỉ inject làm fact cho LLM "tự tránh" (`novel_context_builders.go:363-400`) — vòng đo→siết đang hở: writer lơ là thì không gì nhắc tại thời điểm commit. Fix đúng triết lý: code ĐO (đếm offender trong chương mới), phơi thành `rule_violations` warning trong output `commit_chapter` — writer thấy ngay lúc còn sửa được, editor thấy khi review. KHÔNG chặn commit (chỉ `SeverityWarning`), phán quyết vẫn thuộc LLM.

### Files sở hữu

- Modify: `internal/stylestat/stylestat.go` (+`Offenders`)
- Modify: `internal/tools/commit_chapter.go` (+`checkStyleRepetition`, nối vào `checkRules`)
- Test: `internal/stylestat/stylestat_test.go`, `internal/tools/commit_chapter_test.go`

### Interfaces

- Produces: `stylestat.Offender{Kind, Text string; Count, Chapters int}`; `(s *Stats) Offenders() []Offender`; Violation mới `rule="style_repetition"`, severity warning.

### Task B1: Stats.Offenders

- [ ] **Step 1: Test fail** — thêm vào `stylestat_test.go`:

```go
func TestOffenders(t *testing.T) {
	s := &Stats{
		TopPhrases: []PhraseStat{
			{Text: "anh nhin co", Count: 12}, // >= offenderPhraseCount -> offender
			{Text: "cum hiem", Count: 3},     // duoi nguong -> bo
		},
		RepeatedSentences: []SentenceStat{
			{Text: "Gio lanh thoi qua vai ao.", Chapters: 4, Count: 5}, // luon la offender
		},
	}
	got := s.Offenders()
	if len(got) != 2 {
		t.Fatalf("muon 2 offender, got %d: %+v", len(got), got)
	}
	if got[0].Kind != "phrase" || got[0].Text != "anh nhin co" {
		t.Fatalf("offender[0] sai: %+v", got[0])
	}
	if got[1].Kind != "sentence" || got[1].Chapters != 4 {
		t.Fatalf("offender[1] sai: %+v", got[1])
	}
}
```

- [ ] **Step 2: Fail** — `go test ./internal/stylestat/ -run TestOffenders` → undefined.

- [ ] **Step 3: Implement** — `stylestat.go`:

```go
// offenderPhraseCount — cụm từ trong top_phrases đạt số lần này trong cửa sổ phraseWindow
// thì coi là "cửa miệng" đáng cảnh báo tại commit. 8 lần / 20 chương ≈ gần nửa số chương
// dính cụm này — đủ để người đọc nhận ra khuôn.
const offenderPhraseCount = 8

// Offender là một mục lặp đủ nặng để phơi thành rule violation warning tại commit_chapter.
// Kind: "phrase" (cụm 2-4 từ vượt offenderPhraseCount) | "sentence" (câu lặp verbatim ≥3 chương —
// mọi mục trong RepeatedSentences đều đạt chuẩn theo định nghĩa của repeatedSentences).
type Offender struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Count    int    `json:"count"`
	Chapters int    `json:"chapters,omitempty"`
}

// Offenders lọc từ Stats các mục vượt ngưỡng — thuần lọc số liệu, không phán xét.
func (s *Stats) Offenders() []Offender {
	if s == nil {
		return nil
	}
	var out []Offender
	for _, p := range s.TopPhrases {
		if p.Count >= offenderPhraseCount {
			out = append(out, Offender{Kind: "phrase", Text: p.Text, Count: p.Count})
		}
	}
	for _, r := range s.RepeatedSentences {
		out = append(out, Offender{Kind: "sentence", Text: r.Text, Count: r.Count, Chapters: r.Chapters})
	}
	return out
}
```

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(stylestat): Offenders — loc cum/cau lap vuot nguong"`

### Task B2: commit_chapter nối offender vào violations

- [ ] **Step 1: Test fail** — `commit_chapter_test.go` (dùng harness store test sẵn có của file này; pattern: tạo store tạm, lưu ≥5 chương final chứa cụm lặp, plan + draft chương mới chứa cụm đó ≥2 lần, chạy check qua export nội bộ):

```go
func TestCheckStyleRepetition(t *testing.T) {
	st := newTestStore(t) // dung helper san co trong package test nay
	// 6 chuong cu, moi chuong nhet cum "anh nhin co that lau" 2 lan -> corpus count 12 >= 8
	filler := strings.Repeat("Câu đệm bình thường không lặp. ", 20)
	for ch := 1; ch <= 6; ch++ {
		content := filler + "anh nhìn cô thật lâu. " + filler + "anh nhìn cô thật lâu."
		if err := st.Drafts.SaveFinalChapter(ch, content); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.StartChapter(ch); err != nil {
			t.Fatal(err)
		}
		if err := st.Progress.MarkChapterComplete(ch, 500, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	tool := NewCommitChapterTool(st)
	draft := filler + "anh nhìn cô thật lâu. anh nhìn cô thật lâu."
	vs := tool.checkStyleRepetition(7, draft)
	found := false
	for _, v := range vs {
		if v.Rule == "style_repetition" && v.Severity == rules.SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("phai co violation style_repetition warning, got %+v", vs)
	}
	// chuong khong chua offender -> khong violation
	if vs2 := tool.checkStyleRepetition(7, filler); len(vs2) != 0 {
		t.Fatalf("khong chua offender ma van bao: %+v", vs2)
	}
}
```

(Nếu package chưa có `newTestStore`, dùng đúng helper khởi tạo store mà các test khác trong `commit_chapter_test.go` đang dùng — đọc file test trước khi viết.)

- [ ] **Step 2: Fail** — undefined `checkStyleRepetition`.

- [ ] **Step 3: Implement** — `commit_chapter.go`:

```go
// styleRepetitionMinInChapter — cụm "cửa miệng" phải xuất hiện từ ngần này lần trong CHƯƠNG MỚI
// mới bị nêu tên (1 lần là dùng từ bình thường; câu lặp verbatim thì 1 lần đã đáng nói).
const styleRepetitionMinInChapter = 2

// checkStyleRepetition đo offender toàn tập (cụm cửa miệng / câu lặp verbatim từ stylestat)
// xuất hiện trong chương sắp commit. Chỉ trả sự thật warning — không bao giờ chặn commit;
// LLM (writer lúc nhận kết quả, editor lúc review) tự phán quyết. Best-effort: lỗi đọc → nil.
func (t *CommitChapterTool) checkStyleRepetition(chapter int, content string) []rules.Violation {
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil {
		return nil
	}
	completed := slices.Clone(progress.CompletedChapters)
	slices.Sort(completed)
	var chapters []string
	for _, ch := range completed {
		if ch == chapter {
			continue
		}
		if text, lerr := t.store.Drafts.LoadChapterText(ch); lerr == nil && text != "" {
			chapters = append(chapters, text)
		}
	}
	stats := stylestat.Compute(stylestat.Input{Chapters: chapters})
	if stats == nil {
		return nil
	}
	var out []rules.Violation
	for _, o := range stats.Offenders() {
		n := strings.Count(content, o.Text)
		min := styleRepetitionMinInChapter
		if o.Kind == "sentence" {
			min = 1
		}
		if n < min {
			continue
		}
		out = append(out, rules.Violation{
			Rule:     "style_repetition",
			Target:   o.Text,
			Limit:    o.Count, // số lần đã tích trong corpus — cho LLM thấy độ nặng
			Actual:   n,
			Severity: rules.SeverityWarning,
		})
	}
	return out
}
```

Nối vào `checkRules` (`commit_chapter.go:382`):

```go
func (t *CommitChapterTool) checkRules(chapter int, text string, wordCount int) []rules.Violation {
	violations := rules.Lint(text)
	bundle := rules.Merge(rules.Load(t.rulesOpts))
	violations = append(violations, rules.Check(text, wordCount, bundle.Structured)...)
	return append(violations, t.checkStyleRepetition(chapter, text)...)
}
```

(chữ ký thêm `chapter` — sửa caller `checkAndBlockRules` và caller của nó, compiler chỉ chỗ; `checkAndBlockRules(a.Chapter, content, wordCount)`.) Import `slices`, `strings`, `stylestat`.

Lưu ý stopwords: `Compute` không có stopwords ở đây → tên nhân vật có thể lọt vào top_phrases. Chấp nhận ở warning-level (LLM nhận ra tên riêng); KHÔNG kéo dependency Characters vào commit tool trong Part này.

- [ ] **Step 4: Pass** — `go test ./internal/tools/ -run TestCheckStyleRepetition -v` rồi `go test ./internal/tools/`.
- [ ] **Step 5: Commit** — `git commit -m "feat(quality): style_repetition warning tai commit — dong vong stylestat"`

### Task B3: detector lặp TRONG chương (tật loop của model local)

Model nhỏ qua llama.cpp hay loop cụm NGAY TRONG một chương — stylestat hiện chỉ soi xuyên chương, mù với tật này.

- [ ] **Step 1: Test fail** — `stylestat_test.go`:

```go
func TestIntraChapterRepeats(t *testing.T) {
	loopy := strings.Repeat("hắn siết chặt nắm đấm trong im lặng. Cơn gió thổi qua. ", 6)
	got := IntraChapterRepeats(loopy)
	if len(got) == 0 {
		t.Fatal("cum lap 6 lan trong 1 chuong phai bi bat")
	}
	if got[0].Count < 4 {
		t.Fatalf("count sai: %+v", got[0])
	}
	clean := "Mỗi câu một kiểu. Không gì lặp ở đây cả. Trời hôm nay khác hôm qua."
	if got := IntraChapterRepeats(clean); len(got) != 0 {
		t.Fatalf("van sach ma van bao: %+v", got)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement** — `stylestat.go`:

```go
// intraRepeatMinCount — cụm 3-5 từ xuất hiện từ ngần này lần TRONG MỘT chương là dấu loop
// của model local (văn người hiếm khi chạm 4 trừ điệp ngữ chủ ý — LLM tự phán khi thấy fact).
const intraRepeatMinCount = 4

// IntraChapterRepeats khai thác cụm 3-5 từ lặp ≥ intraRepeatMinCount lần trong một văn bản
// đơn lẻ. Tách khỏi Compute (toàn tập) vì chạy trên draft CHƯA commit — dùng bởi
// commit_chapter để phơi tật loop ngay khi còn sửa được.
func IntraChapterRepeats(text string) []PhraseStat
```

(thuật toán tái dùng khung n-gram của `minePhrases` — tách hàm đếm gram dùng chung nếu tiện, giữ filter hư từ; KHÔNG cần stopwords tên nhân vật vì ngưỡng 4 lần/chương đủ cao.)

`commit_chapter.go` — `checkStyleRepetition` thêm ở cuối:

```go
	for _, r := range stylestat.IntraChapterRepeats(content) {
		out = append(out, rules.Violation{
			Rule: "style_repetition", Target: r.Text,
			Limit: "lặp trong chương", Actual: r.Count,
			Severity: rules.SeverityWarning,
		})
	}
```

(nhánh này chạy cả khi `Compute` trả nil — sách <5 chương vẫn bắt được loop; đảo thứ tự guard cho phù hợp.)

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(stylestat): bat cum lap trong mot chuong — tat loop cua model local"`

### Acceptance

- Chương mới chứa cụm cửa miệng toàn tập ≥2 lần → output commit có violation `style_repetition` warning, commit KHÔNG bị chặn.
- Chương chứa cụm 3-5 từ lặp ≥4 lần nội bộ → warning kể cả khi sách <5 chương.
- <5 chương hoàn thành (stylestat nil) → không thêm gì, không lỗi.
- `go test ./internal/tools/ ./internal/stylestat/` xanh.

### Tác động / mặt trái

- Ưu: vòng đo→nhắc khép kín đúng thời điểm sửa rẻ nhất (trước khi editor vào).
- Trái: +IO đọc ≤ toàn bộ chương final mỗi commit (file local, chấp nhận được; nếu truyện >300 chương thấy chậm thì tối ưu sau bằng cache — YAGNI bây giờ).

---

## Part C — Self-exemplar: style anchor từ chương điểm aesthetic cao nhất

### Bối cảnh & nguyên nhân

`ExtractStyleAnchors` (`store/drafts.go:171`) quét từ chương 1 TĂNG DẦN lấy đoạn đầu tiên đạt filter → văn mẫu luôn là các chương sớm nhất — thường là văn non nhất của cả cuốn. Trong khi điểm `aesthetic` từng chương đã nằm sẵn trong `reviews/NN.json` (`world.go:296`). Lật: chương được chấm hay nhất trở thành văn mẫu — output tốt tự thành chuẩn, càng viết càng tự nâng.

### Files sở hữu

- Modify: `internal/store/world.go` (+`BestAestheticChapters`)
- Modify: `internal/store/drafts.go` (+`ExtractStyleAnchorsFrom`, refactor chung helper)
- Modify: `internal/tools/novel_context_builders.go` (`buildChapterReferencePack`)
- Test: `internal/store/world_test.go`, test drafts (file test store hiện có), `internal/tools/novel_context_test.go`

### Interfaces

- Produces: `(s *WorldStore) BestAestheticChapters(maxChapter, n, minScore int) []int` (desc theo điểm); `(s *DraftStore) ExtractStyleAnchorsFrom(chapters []int, maxAnchors int) []string`.

### Task C1: WorldStore.BestAestheticChapters

- [ ] **Step 1: Test fail** — `world_test.go`:

```go
func TestBestAestheticChapters(t *testing.T) {
	s := newTestWorldStore(t) // helper san co cua world_test.go
	save := func(ch, score int) {
		err := s.SaveReview(domain.ReviewEntry{
			Chapter: ch, Scope: "chapter", Verdict: "accept", Summary: "x",
			Dimensions: []domain.DimensionScore{{Dimension: "aesthetic", Score: score, Comment: "c"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	save(2, 88)
	save(5, 91)
	save(7, 60) // duoi minScore -> loai
	got := s.BestAestheticChapters(10, 3, 75)
	want := []int{5, 2}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := s.BestAestheticChapters(10, 3, 95); len(got) != 0 {
		t.Fatalf("khong chuong nao dat minScore ma van tra: %v", got)
	}
}
```

- [ ] **Step 2: Fail.**

- [ ] **Step 3: Implement** — `world.go` (sau `LoadReview`):

```go
// BestAestheticChapters trả về tối đa n chương có điểm aesthetic cao nhất (giảm dần) trong
// [1..maxChapter], chỉ nhận điểm ≥ minScore — anchor lấy từ văn ĐƯỢC CHẤM là hay, không phải
// "đỡ tệ nhất trong đám tệ". Chương chưa từng được review (không có reviews/NN.json) bị bỏ qua;
// review thưa là bình thường (mỗi ReviewInterval chương / arc-end / light-check flag).
func (s *WorldStore) BestAestheticChapters(maxChapter, n, minScore int) []int {
	type scored struct{ ch, score int }
	var list []scored
	for ch := 1; ch <= maxChapter; ch++ {
		r, err := s.LoadReview(ch)
		if err != nil || r == nil {
			continue
		}
		for _, d := range r.Dimensions {
			if d.Dimension == "aesthetic" && d.Score >= minScore {
				list = append(list, scored{ch, d.Score})
				break
			}
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].score != list[j].score {
			return list[i].score > list[j].score
		}
		return list[i].ch > list[j].ch // điểm bằng nhau: ưu tiên chương mới hơn (giọng hiện tại)
	})
	out := make([]int, 0, n)
	for i := 0; i < len(list) && i < n; i++ {
		out = append(out, list[i].ch)
	}
	return out
}
```

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(store): BestAestheticChapters — xep chuong theo diem aesthetic tu review"`

### Task C2: ExtractStyleAnchorsFrom + builder dùng nó

- [ ] **Step 1: Test fail** — test drafts:

```go
func TestExtractStyleAnchorsFrom(t *testing.T) {
	s := newTestDraftStore(t)
	para := strings.Repeat("Văn mẫu đủ dài để lọt filter năm mươi đến ba trăm rune. ", 3)
	if err := s.SaveFinalChapter(5, para+"\n\n"+para); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFinalChapter(2, "ngắn"); err != nil { // < 50 rune -> loai
		t.Fatal(err)
	}
	got := s.ExtractStyleAnchorsFrom([]int{5, 2}, 3)
	if len(got) == 0 {
		t.Fatal("phai trich duoc anchor tu chuong 5")
	}
}
```

- [ ] **Step 2: Fail.**

- [ ] **Step 3: Implement** — `drafts.go`: tách filter đoạn của `ExtractStyleAnchors` thành helper rồi cả hai dùng chung:

```go
// anchorParagraphs lọc các đoạn 50-300 rune, ít hội thoại, từ một chương — tiêu chí giữ
// NGUYÊN từ ExtractStyleAnchors cũ.
func anchorParagraphs(text string, remain int) []string {
	var anchors []string
	for _, para := range strings.Split(text, "\n\n") {
		if len(anchors) >= remain {
			break
		}
		para = strings.TrimSpace(para)
		rc := utf8.RuneCountInString(para)
		if rc < 50 || rc > 300 {
			continue
		}
		if strings.Count(para, "“") > 2 {
			continue
		}
		anchors = append(anchors, para)
	}
	return anchors
}

// ExtractStyleAnchorsFrom trích anchor theo DANH SÁCH chương chỉ định (thứ tự ưu tiên đã được
// caller xếp — ví dụ theo điểm aesthetic giảm dần), khác ExtractStyleAnchors quét 1..max tăng dần.
func (s *DraftStore) ExtractStyleAnchorsFrom(chapters []int, maxAnchors int) []string {
	if maxAnchors <= 0 {
		maxAnchors = 5
	}
	var anchors []string
	for _, ch := range chapters {
		if len(anchors) >= maxAnchors {
			break
		}
		text, err := s.LoadChapterText(ch)
		if err != nil || text == "" {
			continue
		}
		anchors = append(anchors, anchorParagraphs(text, maxAnchors-len(anchors))...)
	}
	return anchors
}
```

`ExtractStyleAnchors` cũ viết lại thân dùng `anchorParagraphs` (hành vi giữ nguyên — test cũ phải vẫn xanh).

`novel_context_builders.go` — `buildChapterReferencePack` (dòng ~552-558) thay khối anchors:

```go
		// Self-exemplar: ưu tiên văn mẫu từ chương được chấm aesthetic cao nhất (≥75 — trên
		// vùng warning của scorecard); chưa có review nào đạt → fallback quét cũ.
		best := t.store.World.BestAestheticChapters(maxCompleted, 3, 75)
		anchors := t.store.Drafts.ExtractStyleAnchorsFrom(best, 3)
		if len(anchors) == 0 {
			anchors = t.store.Drafts.ExtractStyleAnchors(3, maxCompleted)
		}
		if len(anchors) > 0 {
			envelope.References["style_anchors"] = anchors
		}
```

- [ ] **Step 4:** thêm test builder vào `novel_context_test.go`: store có review aesthetic 90 cho chương 3 + chương 1..5 có văn → `style_anchors` chứa đoạn của chương 3 (assert đoạn văn chương 3 xuất hiện trong anchors). Chạy `go test ./internal/store/ ./internal/tools/`.
- [ ] **Step 5: Commit** — `git commit -m "feat(context): style anchor lay tu chuong diem aesthetic cao nhat"`

### Acceptance

- Có review điểm cao → anchors từ đúng các chương đó; không có → hành vi cũ nguyên vẹn.
- Test cũ của ExtractStyleAnchors không đổi kết quả.

### Tác động / mặt trái

- Ưu: vòng tự cường: chương hay → thành mẫu → chương sau hay hơn.
- Trái: phụ thuộc mật độ review (5 chương/lần); Part D tăng mật độ chấm → C mạnh dần theo D.

---

# WAVE 2 — Flow mới, làm TUẦN TỰ D → E → F (đụng chung router/dispatcher/build/editor.md)

## Part D — Light check mỗi chương (kiểm nhanh + kiểm chứng summary)

### Bối cảnh & nguyên nhân

`ReviewInterval = 5` → 4/5 chương vào sách không qua mắt editor; summary do writer TỰ khai tại `commit_chapter` (`commit_chapter.go:215-221`) không ai đối chiếu — summary sai thì mọi chương sau nhận context hỏng (lỗi tích lũy im lặng). Lật nhược "tự khai không đáng tin" bằng LLM kiểm LLM: sau mỗi commit, editor chạy MỘT lượt kiểm nhanh rẻ (đối chiếu summary với nguyên văn + soi consistency/continuity), ghi sự thật `reviews/NN-check.json`; flag → router phái review sâu đúng chương đó. Deep review 5 chương/lần giữ nguyên.

### Files sở hữu

- Create: `internal/tools/save_chapter_check.go`
- Modify: `internal/domain/review.go` (+ChapterCheck), `internal/store/world.go` (IO check), `internal/host/flow/state.go`, `internal/host/flow/router.go`, `internal/host/flow/dispatcher.go`, `internal/bootstrap/config.go` (+QualityConfig), `internal/agents/build.go` (editor tools + StopAfterToolResult), `internal/tools/read_chapter.go` (`with_summary`), `assets/prompts/editor.md`, `internal/host/host.go` (wiring NewDispatcher), `internal/bootstrap/config.example.jsonc`
- Test: `internal/tools/save_chapter_check_test.go`, `internal/host/flow/router_test.go`, `internal/store/world_test.go`

### Interfaces

- Produces: `domain.ChapterCheck{Chapter int; SummaryFidelity string; SummaryIssues []string; Continuity string; Issues []ConsistencyIssue; Verdict string; Notes string}`; `WorldStore.SaveChapterCheck/LoadChapterCheck`; `bootstrap.QualityConfig{LightCheck bool}` tại `Config.Quality`; `flow.State{LightCheckEnabled, LightCheckPending, LightCheckFlagged}`; `flow.DispatchOptions{Quality bootstrap.QualityConfig}` — `NewDispatcher(coordinator, store, opts DispatchOptions)`.

### Task D1: domain + store IO

- [ ] **Step 1: Test fail** — `world_test.go`:

```go
func TestChapterCheckRoundtrip(t *testing.T) {
	s := newTestWorldStore(t)
	if c, err := s.LoadChapterCheck(3); err != nil || c != nil {
		t.Fatalf("chua luu phai tra nil,nil: %v %v", c, err)
	}
	in := domain.ChapterCheck{Chapter: 3, SummaryFidelity: "ok", Continuity: "flag", Verdict: "flag"}
	if err := s.SaveChapterCheck(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadChapterCheck(3)
	if err != nil || got == nil || got.Verdict != "flag" {
		t.Fatalf("roundtrip hong: %+v %v", got, err)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement** — `domain/review.go`:

```go
// ChapterCheck là kết quả kiểm nhanh (light gate) NGAY sau khi một chương commit: kiểm chứng
// summary tự khai của writer so với nguyên văn + soi nhanh consistency/continuity. Không thay
// thế review sâu định kỳ (7 chiều); chỉ là chốt chặn rẻ chống context rot và lỗi phát hiện trễ.
type ChapterCheck struct {
	Chapter         int                `json:"chapter"`
	SummaryFidelity string             `json:"summary_fidelity"` // ok / mismatch
	SummaryIssues   []string           `json:"summary_issues,omitempty"`
	Continuity      string             `json:"continuity"` // ok / flag
	Issues          []ConsistencyIssue `json:"issues,omitempty"`
	Verdict         string             `json:"verdict"` // pass / flag — code suy từ 2 trường trên
	Notes           string             `json:"notes,omitempty"`
}
```

`store/world.go` (cạnh SaveReview):

```go
// SaveChapterCheck lưu kết quả kiểm nhanh mỗi chương (light gate).
func (s *WorldStore) SaveChapterCheck(c domain.ChapterCheck) error {
	return s.io.WriteJSON(fmt.Sprintf("reviews/%02d-check.json", c.Chapter), &c)
}

// LoadChapterCheck đọc kết quả kiểm nhanh; chưa có file → (nil, nil).
func (s *WorldStore) LoadChapterCheck(chapter int) (*domain.ChapterCheck, error) {
	var c domain.ChapterCheck
	if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d-check.json", chapter), &c); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}
```

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(domain): ChapterCheck + IO kiem nhanh moi chuong"`

### Task D2: tool save_chapter_check

- [ ] **Step 1: Test fail** — `save_chapter_check_test.go`:

```go
func TestSaveChapterCheckVerdictCoercion(t *testing.T) {
	st := newTestStore(t)
	tool := NewSaveChapterCheckTool(st)
	// LLM khai pass nhung summary mismatch -> code ep verdict=flag
	args, _ := json.Marshal(map[string]any{
		"chapter": 4, "summary_fidelity": "mismatch",
		"summary_issues": []string{"summary nói A chết, chương không có"},
		"continuity":     "ok", "verdict": "pass",
	})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Verdict string `json:"verdict"`
	}
	if json.Unmarshal(out, &r); r.Verdict != "flag" {
		t.Fatalf("mismatch phai ep flag, got %s", r.Verdict)
	}
	saved, _ := st.World.LoadChapterCheck(4)
	if saved == nil || saved.Verdict != "flag" {
		t.Fatalf("dia phai ghi flag: %+v", saved)
	}
}

func TestSaveChapterCheckValidate(t *testing.T) {
	st := newTestStore(t)
	tool := NewSaveChapterCheckTool(st)
	args, _ := json.Marshal(map[string]any{"chapter": 0, "summary_fidelity": "ok", "continuity": "ok", "verdict": "pass"})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("chapter<=0 phai loi")
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement** — `internal/tools/save_chapter_check.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveChapterCheckTool lưu kết quả kiểm nhanh mỗi chương của Biên tập viên (light gate).
// Verdict là hàm xác định của hai trường thực tế (summary_fidelity/continuity) — code suy,
// không tin LLM tự khai (cùng nguyên tắc verdict-from-score của save_review).
type SaveChapterCheckTool struct {
	store *store.Store
}

func NewSaveChapterCheckTool(store *store.Store) *SaveChapterCheckTool {
	return &SaveChapterCheckTool{store: store}
}

func (t *SaveChapterCheckTool) Name() string { return "save_chapter_check" }
func (t *SaveChapterCheckTool) Description() string {
	return "Lưu kết quả kiểm nhanh chương (light check): đối chiếu tóm tắt với nguyên văn + soi nhanh consistency/continuity. " +
		"verdict do hệ thống suy: mismatch hoặc continuity=flag → flag (host sẽ điều phối review sâu); ngược lại pass."
}
func (t *SaveChapterCheckTool) Label() string                             { return "Kiểm nhanh chương" }
func (t *SaveChapterCheckTool) ReadOnly(_ json.RawMessage) bool           { return false }
func (t *SaveChapterCheckTool) ConcurrencySafe(_ json.RawMessage) bool    { return false }

func (t *SaveChapterCheckTool) Schema() map[string]any {
	issueSchema := schema.Object(
		schema.Property("type", schema.Enum("Chiều vấn đề", "consistency", "character", "continuity")).Required(),
		schema.Property("severity", schema.Enum("Mức độ", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("Mô tả vấn đề")).Required(),
		schema.Property("evidence", schema.String("Trích nguyên văn làm bằng chứng")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương vừa kiểm")).Required(),
		schema.Property("summary_fidelity", schema.Enum("Tóm tắt khớp nguyên văn không", "ok", "mismatch")).Required(),
		schema.Property("summary_issues", schema.Array("Điểm sai/thiếu của tóm tắt (bắt buộc khi mismatch)", schema.String(""))),
		schema.Property("continuity", schema.Enum("Nối tiếp chương trước + trạng thái nhân vật ổn không", "ok", "flag")).Required(),
		schema.Property("issues", schema.Array("Vấn đề phát hiện (khi continuity=flag)", issueSchema)),
		schema.Property("verdict", schema.Enum("Kết luận (hệ thống sẽ tự suy lại)", "pass", "flag")),
		schema.Property("notes", schema.String("Ghi chú ngắn")),
	)
}

func (t *SaveChapterCheckTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var c domain.ChapterCheck
	if err := json.Unmarshal(args, &c); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if c.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	if c.SummaryFidelity == "mismatch" && len(c.SummaryIssues) == 0 {
		return nil, fmt.Errorf("summary_issues bắt buộc khi summary_fidelity=mismatch")
	}
	// Verdict xác định từ sự thật — không tin tự khai.
	c.Verdict = "pass"
	if c.SummaryFidelity == "mismatch" || c.Continuity == "flag" {
		c.Verdict = "flag"
	}
	if err := t.store.World.SaveChapterCheck(c); err != nil {
		return nil, fmt.Errorf("save chapter check: %w", err)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(c.Chapter), "light_check",
		fmt.Sprintf("reviews/%02d-check.json", c.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint light check: %w", err)
	}
	next := "pass — host sẽ điều phối viết chương kế"
	if c.Verdict == "flag" {
		next = "flag — host sẽ điều phối review sâu chương này"
	}
	return json.Marshal(map[string]any{
		"saved": true, "chapter": c.Chapter, "verdict": c.Verdict, "next": next,
	})
}
```

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(tools): save_chapter_check — light gate moi chuong"`

### Task D3: read_chapter with_summary

- [ ] **Step 1:** Đọc `internal/tools/read_chapter.go` (chưa đọc trong plan này). Thêm property optional `with_summary` (bool) vào Schema; trong Execute, khi true: load `store.Summaries` summary chương đó + `state_changes` của chương (lọc từ `World.LoadStateChanges()` theo `Chapter==N`) và trả kèm trong JSON output dưới key `stored_summary`, `stored_state_changes`. Test: lưu summary rồi gọi read_chapter with_summary → output chứa summary.
- [ ] **Step 2:** Test pass, commit — `git commit -m "feat(tools): read_chapter with_summary — doc kem tom tat da luu de doi chieu"`

### Task D4: config Quality + State + Route + Dispatcher

- [ ] **Step 1: Test fail** — `router_test.go` (Route thuần, không IO):

```go
func TestRouteLightCheck(t *testing.T) {
	base := State{
		Progress: &domain.Progress{
			Phase: domain.PhaseWriting, TotalChapters: 20,
			CompletedChapters: []int{1, 2, 3}, CurrentChapter: 3,
		},
		LastCompleted: 3, LightCheckEnabled: true,
	}

	s := base
	s.LightCheckPending = true
	inst := Route(s)
	if inst == nil || inst.Agent != "editor" || !strings.Contains(inst.Task, "Kiểm nhanh chương 3") {
		t.Fatalf("pending phai phat kiem nhanh, got %+v", inst)
	}

	s = base
	s.LightCheckFlagged = true
	inst = Route(s)
	if inst == nil || inst.Agent != "editor" || !strings.Contains(inst.Task, "scope=chapter") {
		t.Fatalf("flagged phai phat review sau, got %+v", inst)
	}

	s = base // enabled nhung khong pending/flagged -> viet tiep nhu cu
	if inst = Route(s); inst == nil || inst.Agent != "writer" {
		t.Fatalf("khong no light check phai viet tiep, got %+v", inst)
	}

	s = base
	s.LightCheckEnabled = false
	s.LightCheckPending = true // fact co nhung feature tat -> bo qua
	if inst = Route(s); inst == nil || inst.Agent != "writer" {
		t.Fatalf("feature tat phai bo qua light check, got %+v", inst)
	}
}
```

- [ ] **Step 2: Fail** — State thiếu field.

- [ ] **Step 3: Implement**

`bootstrap/config.go` — thêm vào `Config` (cạnh `Style`):

```go
	// Quality gom công tắc vòng chất lượng LLM-first (light check / ensemble / best-of-N).
	Quality QualityConfig `json:"quality,omitempty"`
```

```go
// QualityConfig là các công tắc vòng chất lượng. Mặc định tắt hết = hành vi cũ nguyên vẹn;
// bật dần khi ngân sách call cho phép (cần rate limit rotation gánh).
type QualityConfig struct {
	// LightCheck bật kiểm nhanh mỗi chương bằng editor ngay sau commit (kiểm chứng summary + continuity).
	LightCheck bool `json:"light_check,omitempty"`
}
```

`flow/router.go` — `State` thêm:

```go
	// LightCheckEnabled do Dispatcher tiêm từ config (LoadState chỉ đọc store, không biết config).
	LightCheckEnabled bool
	// LightCheckPending: chương hoàn thành gần nhất chưa có reviews/NN-check.json và chưa có
	// review chương phủ lên. LightCheckFlagged: check gần nhất verdict=flag mà chưa có review chương.
	LightCheckPending bool
	LightCheckFlagged bool
```

`Route` — chèn giữa bước 4 (Steering) và bước 5 (hậu xử lý arc), cập nhật doc-comment ưu tiên:

```go
	// 4b. Light gate mỗi chương: review sâu cho chương bị flag đi trước, kiểm nhanh đi sau.
	if s.LightCheckEnabled && s.LightCheckFlagged {
		return &Instruction{
			Agent:   "editor",
			Task:    fmt.Sprintf("Review sâu chương %d (scope=chapter, save_review) — kiểm nhanh đã gắn cờ", s.LastCompleted),
			Reason:  "Light check verdict=flag, cần phán quyết 7 chiều",
			Chapter: 0,
		}
	}
	if s.LightCheckEnabled && s.LightCheckPending {
		return &Instruction{
			Agent:  "editor",
			Task:   fmt.Sprintf("Kiểm nhanh chương %d: read_chapter(chapter=%d, with_summary=true), đối chiếu tóm tắt với nguyên văn, soi consistency/continuity với chương trước, gọi save_chapter_check", s.LastCompleted, s.LastCompleted),
			Reason: "Chương vừa hoàn thành chưa qua kiểm nhanh",
		}
	}
```

`flow/state.go` — cuối `LoadState` (sau khối flat review):

```go
	// Light check: sự thật từ đĩa cho chương hoàn thành gần nhất. Chỉ xét LastCompleted —
	// chương cũ trước khi bật tính năng không bị truy thu. Lỗi đọc → coi như nợ check
	// (fail-toward-review, đồng bộ triết lý HasPendingFlatReview).
	if s.LastCompleted > 0 {
		check, cerr := store.World.LoadChapterCheck(s.LastCompleted)
		review, _ := store.World.LoadReview(s.LastCompleted)
		switch {
		case cerr != nil || check == nil:
			if review == nil {
				s.LightCheckPending = true
			}
		case check.Verdict == "flag" && review == nil:
			s.LightCheckFlagged = true
		}
	}
```

`flow/dispatcher.go`:

```go
// DispatchOptions là các dữ kiện cấu hình Route cần nhưng không nằm trong store.
type DispatchOptions struct {
	Quality bootstrap.QualityConfig
}
```

`Dispatcher` thêm field `opts DispatchOptions`; `NewDispatcher(coordinator *agentcore.Agent, store *storepkg.Store, opts DispatchOptions)`; trong `Dispatch()`:

```go
	state := LoadState(d.store)
	state.LightCheckEnabled = d.opts.Quality.LightCheck
	inst := Route(state)
```

(import `bootstrap` vào package flow — nếu tạo import cycle bootstrap→flow thì thay bằng struct cục bộ `QualityOptions{LightCheck bool}` trong package flow, Host map từ config sang; kiểm tra bằng `go build ./...`.)

`internal/host/host.go` — tìm call site `NewDispatcher(` (grep), truyền `flow.DispatchOptions{Quality: cfg.Quality}` (Host có cfg trong tay tại điểm lắp ráp; nếu không, thêm tham số từ nơi gọi BuildCoordinator — compiler dẫn đường).

`agents/build.go` — `editorTools` thêm `tools.NewSaveChapterCheckTool(store)`; editor `StopAfterToolResult`:

```go
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_arc_summary" || toolName == "save_volume_summary" ||
				toolName == "save_chapter_check"
		},
```

`config.example.jsonc` thêm:

```jsonc
  // Vòng chất lượng LLM-first. Bật dần khi có rate limit rotation gánh chi phí call.
  // "quality": { "light_check": true },
```

- [ ] **Step 4: Pass** — `go test ./internal/host/flow/ ./internal/bootstrap/ -v` + `go build ./...`.
- [ ] **Step 5: Commit** — `git commit -m "feat(flow): light gate moi chuong — route kiem nhanh + review sau khi flag"`

### Task D5: editor.md — chế độ kiểm nhanh

- [ ] **Step 1:** `assets/prompts/editor.md` — thêm section mới trước "## Chế độ biên tập cấp cung truyện" (dòng ~169):

```markdown
## Chế độ kiểm nhanh mỗi chương (light check)

Khi nhiệm vụ là "Kiểm nhanh chương N": đây KHÔNG phải review 7 chiều. Làm đúng 3 việc, nhanh:

1. `read_chapter(chapter=N, with_summary=true)` — đọc nguyên văn + tóm tắt đã lưu.
2. Đối chiếu tóm tắt/key_events/state_changes với nguyên văn: sự kiện nào summary khai mà văn
   không có (bịa), sự kiện quan trọng nào văn có mà summary thiếu (sót)? Tóm tắt là trí nhớ duy
   nhất của các chương sau — sai ở đây là hỏng dây chuyền.
3. Soi nhanh consistency/continuity: trạng thái nhân vật (sống/chết/vị trí/vật phẩm), nối tiếp
   đuôi chương trước, mâu thuẫn thiết định lộ liễu.

Xong gọi `save_chapter_check`. Cấm: sửa văn, gọi save_review, chấm 7 chiều trong chế độ này.
Nếu hệ thống trả verdict=flag, host sẽ giao nhiệm vụ review sâu riêng — đừng tự làm luôn.
```

- [ ] **Step 2:** `go build ./...` (assets embed). Chạy tay 1 vòng nếu có sách test: commit 1 chương → thấy dispatch "Kiểm nhanh chương N" → file `reviews/NN-check.json` xuất hiện.
- [ ] **Step 3: Commit** — `git commit -m "feat(prompts): che do kiem nhanh moi chuong cho editor"`

### Acceptance

- `quality.light_check=true`: sau mỗi commit chương, editor được phái kiểm nhanh; `flag` → review sâu scope=chapter đúng chương; `pass` → viết tiếp.
- Tắt flag → không một dispatch nào khác trước đây thay đổi (router_test cũ xanh nguyên).
- Crash giữa chừng: mọi quyết định suy lại được từ đĩa (`NN-check.json` / `NN.json`).

### Tác động / mặt trái

- Ưu: chặn context rot tại nguồn; lỗi lộ sau 1 chương thay vì 5; tạo mật độ review nuôi Part C.
- Trái: +1 editor call/chương (ngắn — không chấm 7 chiều). Editor có thể "kiểm nhanh" thành review dài — prompt đã cấm tường minh; theo dõi qua log token.

---

## Part E — Role `reviser`: rewrite/polish bằng model khác (LÀM SAU D)

### Bối cảnh & nguyên nhân

Rewrite hiện do CHÍNH writer model thực hiện (`router.go:84-96` phái "writer") — tật văn của model nó không tự thấy, rewrite dễ tái phạm đúng tật cũ. Hạ tầng xoay model của feat/ratelimit (`ForRoleWithFailover`, `models.go:124`) cho phép lắp role mới chỉ bằng config. Lật: nhược "mỗi model một tật" thành ưu "model B sửa tật model A".

### Files sở hữu

- Modify: `internal/bootstrap/config.go` (knownRoles), `internal/agents/build.go` (subagent reviser + ApplyThinking), `internal/host/flow/router.go` (+State.ReviserAvailable, nhánh rewrite), `internal/host/flow/dispatcher.go` (DispatchOptions), `internal/entry/tui/command_model.go` (modelRoleOptions), `assets/prompts/coordinator.md` (khai báo agent), `internal/bootstrap/config.example.jsonc`
- Test: `internal/host/flow/router_test.go`, test config

### Interfaces

- Consumes: `DispatchOptions` (Part D). Produces: role `reviser` hợp lệ trong `Roles`; `State.ReviserAvailable bool`; subagent tên `reviser` (chỉ đăng ký khi config có role).

### Task E1: knownRoles + router

- [ ] **Step 1: Test fail** — `router_test.go`:

```go
func TestRouteRewriteDispatchesReviser(t *testing.T) {
	s := State{
		Progress: &domain.Progress{
			Phase: domain.PhaseWriting, Flow: domain.FlowRewriting,
			PendingRewrites: []int{4}, CompletedChapters: []int{1, 2, 3, 4},
		},
		LastCompleted: 4,
	}
	if inst := Route(s); inst == nil || inst.Agent != "writer" {
		t.Fatalf("chua co reviser phai dung writer, got %+v", inst)
	}
	s.ReviserAvailable = true
	if inst := Route(s); inst == nil || inst.Agent != "reviser" {
		t.Fatalf("co reviser phai dung reviser, got %+v", inst)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement** — `config.go`: `knownRoles` thêm `"reviser": true` (comment: `// reviser: model tu sửa khác model viết — tật văn của writer không tự lặp lại`). `router.go`: State thêm `ReviserAvailable bool` (comment: do Dispatcher tiêm từ config); nhánh PendingRewrites:

```go
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Viết lại"
		if p.Flow == domain.FlowPolishing {
			verb = "Đánh bóng"
		}
		agent := "writer"
		if s.ReviserAvailable {
			agent = "reviser"
		}
		return &Instruction{
			Agent:   agent,
			Task:    fmt.Sprintf("%s chương %d", verb, ch),
			Reason:  fmt.Sprintf("Hàng đợi PendingRewrites còn %d chương", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}
```

`dispatcher.go`: `DispatchOptions` thêm `ReviserAvailable bool`; `Dispatch()` set `state.ReviserAvailable = d.opts.ReviserAvailable`.

- [ ] **Step 4: Pass** `go test ./internal/host/flow/`. **Step 5: Commit** — `git commit -m "feat(flow): role reviser — rewrite bang model khac khi duoc cau hinh"`

### Task E2: build.go đăng ký subagent reviser

- [ ] **Step 1: Implement** (integration — verify bằng build + chạy tay; router đã test):

`build.go`, sau khối `editor` (dòng ~309):

```go
	// Reviser: bản sao cấu hình writer nhưng model theo role "reviser" — chỉ đăng ký khi
	// người dùng cấu hình role này. Cross-model rewrite: tật văn của writer model không tự
	// lặp lại dưới mắt model khác; toàn bộ tool/stop-guard/context factory dùng chung writer.
	subagentConfigs := []subagent.Config{architectShort, architectLong, writer, editor}
	if _, ok := cfg.Roles["reviser"]; ok {
		reviser := writer
		reviser.Name = "reviser"
		reviser.Description = "Người tu sửa: viết lại/đánh bóng chương theo kết quả review, dùng model khác người viết"
		reviser.Model = models.ForRoleWithFailover("reviser", reportFailover)
		reviser.ThinkingLevel = roleThinking(cfg, "reviser")
		subagentConfigs = append(subagentConfigs, reviser)
	}
	subagentTool := subagent.New(subagentConfigs...)
```

(xóa dòng `subagentTool := subagent.New(architectShort, architectLong, writer, editor)` cũ). Kiểm tra phần ApplyThinking closure cuối `build.go` (đọc đoạn 330+ khi làm): thêm case `"reviser"` trỏ tới subagent reviser nếu closure switch theo role. Host wiring: nơi gọi `NewDispatcher` truyền thêm `ReviserAvailable: func() bool { _, ok := cfg.Roles["reviser"]; return ok }()`.

`command_model.go`: `modelRoleOptions` thêm `{Key: "reviser", Label: "Reviser"}`.

`coordinator.md`: mục liệt kê agent phụ thêm dòng: `- reviser: người tu sửa (chỉ tồn tại khi được cấu hình) — nhận nhiệm vụ "Viết lại/Đánh bóng chương N" thay writer khi Host ra lệnh.`

`config.example.jsonc` roles block thêm:

```jsonc
  //   // Cross-model rewrite: model khác sửa tật văn của writer.
  //   "reviser": { "provider": "openrouter", "model": "mot-model-KHAC-writer", "extra_body": { "temperature": 0.7 } }
```

- [ ] **Step 2:** `go build ./...` + `go test ./...`. Chạy tay: config có reviser, tạo review verdict=rewrite → log dispatch hiện `subagent(reviser, "Viết lại chương N")`, chương được rewrite bởi model reviser (check session log `_meta.model`).
- [ ] **Step 3: Commit** — `git commit -m "feat(agents): dang ky subagent reviser tu config role"`

### Acceptance

- Không config reviser → zero thay đổi hành vi (router test nhánh writer).
- Config reviser → mọi rewrite/polish dispatch sang reviser với model cấu hình; `/model` TUI chỉnh được role reviser.

### Tác động / mặt trái

- Ưu: đa dạng giọng có chủ đích, phá vòng "tự sửa tự lặp"; zero cost khi tắt.
- Trái: `agentToRole` passthrough "reviser" → session log usage tính theo role reviser (đúng mong muốn); model khác = giọng có thể lệch — reviser nhận cùng style prompt + style_anchors nên lệch trong biên kiểm soát, editor vẫn gác sau rewrite.

---

## Part F — Sổ tay tác giả (author notebook) — OPT-IN, LÀM SAU E

> **Trạng thái (bản sửa fleet rẻ): OPT-IN, rủi ro có chủ đích.** Model rẻ chưng cất kém —
> sổ tay nhiễm một kết luận sai thì rác được inject VĨNH VIỄN vào mọi chương sau, độc hơn
> không có sổ tay. Điều kiện bật: (1) editor là model TỐT NHẤT trong fleet (qua Part 0A probe),
> (2) người dùng duyệt sổ tay tại mỗi human gate (Part 0D) — file `world/notebook.json` là
> JSON thường, sửa tay trực tiếp được; hướng dẫn xem/sửa in ra ở thông báo gate. Gate config:
> chỉ inject `author_notebook` khi `quality.notebook=true`.

### Bối cảnh & nguyên nhân

Bài học review chỉ sống trong `review_lessons` cửa sổ N-1..N-3 (`novel_context_builders.go:469-471`) — chương 50 không còn nhớ bài học chương 10. Cửa sổ cứng là giới hạn code; LLM chưng cất tốt hơn code cắt. Lật: editor định kỳ tự chưng cất "điều đã học về truyện NÀY" thành sổ tay sống — thay trí nhớ cửa sổ bằng trí nhớ được biên tập.

### Files sở hữu

- Create: `internal/tools/save_notebook.go`
- Modify: `internal/domain/review.go` (+AuthorNotebook), `internal/store/world.go` (IO), `internal/tools/novel_context_builders.go` (inject), `internal/agents/build.go` (editor tools), `assets/prompts/editor.md`, `assets/prompts/writer.md`
- Test: `internal/tools/save_notebook_test.go`, `internal/store/world_test.go`, `internal/tools/novel_context_test.go`

### Interfaces

- Produces: `domain.AuthorNotebook{UpdatedChapter int; Content string}`; `WorldStore.SaveNotebook/LoadNotebook`; context key `selected_memory.author_notebook` (string).

### Task F1: domain + store + tool

- [ ] **Step 1: Test fail:**

`world_test.go`:

```go
func TestNotebookRoundtrip(t *testing.T) {
	s := newTestWorldStore(t)
	if nb, err := s.LoadNotebook(); err != nil || nb != nil {
		t.Fatalf("chua co notebook phai nil,nil: %v %v", nb, err)
	}
	if err := s.SaveNotebook(domain.AuthorNotebook{UpdatedChapter: 10, Content: "giọng lạnh, tránh ẩn dụ thời tiết"}); err != nil {
		t.Fatal(err)
	}
	nb, err := s.LoadNotebook()
	if err != nil || nb == nil || nb.UpdatedChapter != 10 {
		t.Fatalf("roundtrip hong: %+v %v", nb, err)
	}
}
```

`save_notebook_test.go`:

```go
func TestSaveNotebookValidate(t *testing.T) {
	st := newTestStore(t)
	tool := NewSaveNotebookTool(st)
	// rong -> loi
	args, _ := json.Marshal(map[string]any{"chapter": 5, "content": ""})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("content rong phai loi")
	}
	// qua tran -> loi (buoc chung cat, khong cho notebook phinh)
	long := strings.Repeat("x", notebookMaxRunes+1)
	args, _ = json.Marshal(map[string]any{"chapter": 5, "content": long})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("vuot tran rune phai loi")
	}
	args, _ = json.Marshal(map[string]any{"chapter": 5, "content": "bài học"})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	nb, _ := st.World.LoadNotebook()
	if nb == nil || nb.UpdatedChapter != 5 {
		t.Fatalf("notebook chua luu: %+v", nb)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement**

`domain/review.go`:

```go
// AuthorNotebook là sổ tay tác giả — trí nhớ dài hạn do editor CHƯNG CẤT (không phải append):
// bài học giọng văn, tật cần tránh, quy tắc thế giới nổi lên qua các vòng review. Ghi đè toàn
// bộ mỗi lần cập nhật; giữ ngắn là trách nhiệm của editor, trần cứng do tool ép.
type AuthorNotebook struct {
	UpdatedChapter int    `json:"updated_chapter"`
	Content        string `json:"content"`
}
```

`world.go`:

```go
// SaveNotebook ghi đè sổ tay tác giả (world/notebook.json).
func (s *WorldStore) SaveNotebook(nb domain.AuthorNotebook) error {
	return s.io.WriteJSON("world/notebook.json", &nb)
}

// LoadNotebook đọc sổ tay; chưa có → (nil, nil).
func (s *WorldStore) LoadNotebook() (*domain.AuthorNotebook, error) {
	var nb domain.AuthorNotebook
	if err := s.io.ReadJSON("world/notebook.json", &nb); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &nb, nil
}
```

`save_notebook.go` (khung tool y hệt save_chapter_check; điểm riêng):

```go
// notebookMaxRunes — trần độ dài sổ tay (~1000-1200 từ tiếng Việt). Vượt trần là lỗi tool:
// buộc editor chưng cất thay vì dồn đống; trí nhớ được biên tập mới có giá trị hơn cửa sổ cứng.
const notebookMaxRunes = 8000
```

Schema: `chapter` (Int, required — chương mốc cập nhật), `content` (String, required — "toàn văn sổ tay MỚI sau chưng cất, ghi đè bản cũ"). Execute: validate non-empty + `utf8.RuneCountInString(content) <= notebookMaxRunes`; `SaveNotebook`; checkpoint `AppendArtifact(domain.ChapterScope(chapter), "notebook", "world/notebook.json")`; trả `{saved, updated_chapter, runes}`.

`novel_context_builders.go` — `buildChapterSelectedMemory`:

```go
	if nb, err := t.store.World.LoadNotebook(); err == nil && nb != nil && nb.Content != "" {
		envelope.Selected["author_notebook"] = nb.Content
	}
```

`build.go`: `editorTools` thêm `tools.NewSaveNotebookTool(store)`.

- [ ] **Step 4: Pass** — 3 package test. **Step 5: Commit** — `git commit -m "feat(quality): so tay tac gia — editor chung cat, inject vao writer context"`

### Task F2: prompts

- [ ] **Step 1:** `editor.md` — cuối section "## Chế độ đánh giá định kỳ theo batch" (dòng ~176) và cuối "## Chế độ biên tập cấp cung truyện" thêm cùng đoạn:

```markdown
Sau khi save_review xong: cập nhật sổ tay tác giả. Đọc `selected_memory.author_notebook`
(bản cũ, nếu có) + các vấn đề vừa chấm, viết BẢN MỚI đã chưng cất qua `save_notebook`:
giữ ≤ ~1000 từ; gộp bài học mới, xóa mục đã hết hiệu lực hoặc writer đã sửa được bền vững;
ưu tiên: tật văn tái phạm > quy tắc thế giới nổi lên > ghi chú giọng nhân vật. Sổ tay là
trí nhớ dài hạn duy nhất sống qua mọi cửa sổ context — viết cho writer đọc, mệnh lệnh ngắn,
có ví dụ trích từ chính truyện khi đáng giá.
```

`writer.md` — mục "## Tiêu chuẩn viết" (dòng 47) thêm điều:

```markdown
- Đọc `selected_memory.author_notebook` (nếu có) TRƯỚC khi viết: đây là bài học tích lũy từ
  mọi vòng review của chính truyện này — vi phạm mục đã ghi trong sổ tay nặng hơn vi phạm
  quy tắc chung, vì đã được nhắc đích danh.
```

- [ ] **Step 2:** `go build ./...`; chạy tay 1 vòng review batch → `world/notebook.json` xuất hiện, chương kế writer context có `author_notebook`.
- [ ] **Step 3: Commit** — `git commit -m "feat(prompts): quy trinh chung cat va tuan thu so tay tac gia"`

### Acceptance

- Sau review batch/arc đầu tiên có notebook trên đĩa; các chương sau inject `selected_memory.author_notebook`; vượt 8000 rune bị tool từ chối.
- Chưa có notebook → không inject key, không lỗi.

### Tác động / mặt trái

- Ưu: trí nhớ dài hạn được biên tập; bù đúng chỗ `review_lessons` cửa sổ 3 chương.
- Trái: editor có thể quên bước notebook (prompt-driven, không route cưỡng chế — chấp nhận: notebook là tăng cường, `review_lessons` vẫn còn). Nếu sau 2-3 sách thấy hay quên → thêm nhánh route cưỡng chế sau (không làm bây giờ).

---

# WAVE 3 — Đắt nhất, làm TUẦN TỰ G → H (đụng chung router/dispatcher/build/prompts)

## Part G — Best-of-N: N nháp ứng viên, editor chọn

### Bối cảnh & nguyên nhân

`draft_chapter` sinh đúng 1 bản (`draft_chapter.go`), không có candidate selection. Variance của LLM hiện là rủi ro (hên xui bản đầu); lật thành nguồn lựa chọn: K writer run độc lập (nhiệt cao — Part A), editor chấm chọn, writer hoàn thiện bản thắng. Đắt K× ở khâu draft nhưng rẻ hơn chuỗi rewrite (draft+review+rewrite+re-review) khi bản đầu xấu.

### Files sở hữu

- Create: `internal/tools/select_draft.go`
- Modify: `internal/store/drafts.go` (candidate IO), `internal/tools/draft_chapter.go` (+candidate), `internal/tools/read_chapter.go` (source=candidate), `internal/host/flow/router.go` + `state.go` + `dispatcher.go`, `internal/bootstrap/config.go` (Quality.BestOfN + validate), `internal/agents/build.go` (writer StopAfterToolResult + editor tools), `assets/prompts/writer.md`, `assets/prompts/editor.md`, `internal/bootstrap/config.example.jsonc`
- Test: drafts store test, `draft_chapter_test.go`, `select_draft_test.go` (mới), `router_test.go`, `read_draft_test.go`

### Interfaces

- Produces: `DraftStore.SaveCandidate(chapter, k int, content string)` / `LoadCandidate(chapter, k int)` / `CountCandidates(chapter int) int` / `PromoteCandidate(chapter, k int) error` / `HasSelection(chapter int) (int, bool)`; file layout `drafts/NN.cand-K.md`, `drafts/NN.selection.json`; `QualityConfig.BestOfN int`; `State.{BestOfN, NextCandidates int, NextHasSelection bool}`; tool `select_draft{chapter, choice, reason}`; `draft_chapter` arg mới `candidate int` (kết quả JSON có `"candidate": k`).

### Task G1: candidate IO trong DraftStore

- [ ] **Step 1: Test fail** — drafts store test:

```go
func TestCandidateLifecycle(t *testing.T) {
	s := newTestDraftStore(t)
	if n := s.CountCandidates(3); n != 0 {
		t.Fatalf("chua co candidate: %d", n)
	}
	if err := s.SaveCandidate(3, 1, "bản một"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCandidate(3, 2, "bản hai"); err != nil {
		t.Fatal(err)
	}
	if n := s.CountCandidates(3); n != 2 {
		t.Fatalf("muon 2, got %d", n)
	}
	if txt, err := s.LoadCandidate(3, 2); err != nil || txt != "bản hai" {
		t.Fatalf("load candidate: %q %v", txt, err)
	}
	if err := s.PromoteCandidate(3, 2); err != nil {
		t.Fatal(err)
	}
	if draft, err := s.LoadDraft(3); err != nil || draft != "bản hai" {
		t.Fatalf("promote phai ghi vao draft chinh: %q %v", draft, err)
	}
	if n := s.CountCandidates(3); n != 0 {
		t.Fatalf("promote phai xoa candidates: %d", n)
	}
	if k, ok := s.HasSelection(3); !ok || k != 2 {
		t.Fatalf("selection record: %d %v", k, ok)
	}
	if _, err := s.LoadCandidate(3, 9); err == nil {
		t.Fatal("candidate khong ton tai phai loi")
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement** — `drafts.go` (dùng đúng cơ chế đọc/ghi file mà SaveDraft/LoadDraft đang dùng — soi 2 hàm đó rồi nhân bản với path candidate):

```go
// candidatePath: drafts/NN.cand-K.md — bản nháp ứng viên trong chế độ best-of-N.
// selectionPath: drafts/NN.selection.json — dấu vết đã chọn (fact cho router + audit).

func (s *DraftStore) SaveCandidate(chapter, k int, content string) error
func (s *DraftStore) LoadCandidate(chapter, k int) (string, error)

// CountCandidates đếm liên tiếp từ 1 — file cand-1..cand-n tồn tại thì trả n (lỗ hổng giữa
// chừng coi như dừng tại đó; router chỉ cần "ứng viên kế tiếp là số mấy").
func (s *DraftStore) CountCandidates(chapter int) int

// PromoteCandidate: copy cand-K → draft chính (SaveDraft), ghi selection.json {"chosen":K},
// xóa toàn bộ cand-*. Thứ tự: ghi draft + selection TRƯỚC, xóa sau — crash giữa chừng thì
// candidates còn thừa trên đĩa nhưng selection đã có → router vẫn đi tiếp đúng nhánh.
func (s *DraftStore) PromoteCandidate(chapter, k int) error

type draftSelection struct {
	Chosen int `json:"chosen"`
}

func (s *DraftStore) HasSelection(chapter int) (int, bool)
```

(Thân hàm theo io helper thực tế của DraftStore — đọc `drafts.go:1-60` khi implement; giữ nguyên convention `%02d`.)

- [ ] **Step 4: Pass.** **Step 5: Commit** — `git commit -m "feat(store): candidate lifecycle cho best-of-N"`

### Task G2: draft_chapter + read_chapter + select_draft

- [ ] **Step 1: Test fail:**

`draft_chapter_test.go`:

```go
func TestDraftChapterCandidate(t *testing.T) {
	st := newTestStore(t)
	// plan truoc (precondition cua draft_chapter)
	mustPlanChapter(t, st, 2) // dung helper/pattern san co trong file test nay
	tool := NewDraftChapterTool(st)
	args, _ := json.Marshal(map[string]any{"chapter": 2, "content": "văn ứng viên", "mode": "write", "candidate": 1})
	out, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Candidate int `json:"candidate"`
	}
	json.Unmarshal(out, &r)
	if r.Candidate != 1 {
		t.Fatalf("output phai co candidate=1: %s", out)
	}
	if txt, _ := st.Drafts.LoadCandidate(2, 1); txt != "văn ứng viên" {
		t.Fatalf("phai luu vao cand file: %q", txt)
	}
	if d, err := st.Drafts.LoadDraft(2); err == nil && d != "" {
		t.Fatalf("khong duoc dung vao draft chinh: %q", d)
	}
	// append + candidate -> loi
	args, _ = json.Marshal(map[string]any{"chapter": 2, "content": "x", "mode": "append", "candidate": 2})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("append voi candidate phai loi")
	}
}
```

`select_draft_test.go`:

```go
func TestSelectDraft(t *testing.T) {
	st := newTestStore(t)
	st.Drafts.SaveCandidate(2, 1, "một")
	st.Drafts.SaveCandidate(2, 2, "hai")
	tool := NewSelectDraftTool(st)
	args, _ := json.Marshal(map[string]any{"chapter": 2, "choice": 2, "reason": "mở cảnh mạnh hơn"})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.Drafts.LoadDraft(2); d != "hai" {
		t.Fatalf("draft phai la ban 2: %q", d)
	}
	// choice khong ton tai -> loi
	args, _ = json.Marshal(map[string]any{"chapter": 2, "choice": 5, "reason": "x"})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("choice khong ton tai phai loi")
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement**

`draft_chapter.go`: Schema thêm `schema.Property("candidate", schema.Int("Số thứ tự bản ứng viên trong chế độ best-of-N (0/bỏ trống = viết nháp chính thức)"))` (KHÔNG required — giữ strict schema? OpenAI strict yêu cầu mọi property trong required; nếu StrictSchema()=true thì required kèm mô tả "điền 0 khi không ở chế độ ứng viên" — làm theo đúng cách `mode` đã xử lý, tức thêm vào required, model điền 0). Execute:

```go
	var a struct {
		Chapter   int    `json:"chapter"`
		Content   string `json:"content"`
		Mode      string `json:"mode"`
		Candidate int    `json:"candidate"`
	}
	...
	if a.Candidate > 0 {
		if a.Mode == "append" {
			return nil, fmt.Errorf("ứng viên chỉ hỗ trợ mode=write (mỗi ứng viên là một bản trọn vẹn): %w", errs.ErrToolArgs)
		}
		// vẫn qua ValidateChapterWork + yêu cầu plan như nhánh thường (giữ nguyên các check phía trên)
		if err := t.store.Drafts.SaveCandidate(a.Chapter, a.Candidate, a.Content); err != nil {
			return nil, fmt.Errorf("save candidate: %w", err)
		}
		return json.Marshal(map[string]any{
			"written": true, "chapter": a.Chapter, "candidate": a.Candidate,
			"word_count": utf8.RuneCountInString(a.Content),
			"next_step":  "Ứng viên đã lưu — DỪNG tại đây, host sẽ điều phối bước tiếp theo",
		})
	}
```

(đặt nhánh này sau các precondition, trước switch mode; nhánh candidate KHÔNG StartChapter lại — StartChapter đã idempotent, giữ gọi như cũ cũng được, chọn giữ để TUI hiện "đang tiến hành".)

`read_chapter.go`: source enum thêm `"candidate"`, property `candidate` Int; source=candidate → `LoadCandidate(chapter, candidate)`.

`select_draft.go` — tool mới (editor):

```go
// SelectDraftTool chốt bản nháp thắng cuộc trong chế độ best-of-N: promote ứng viên được chọn
// thành draft chính, ghi dấu vết chọn (drafts/NN.selection.json). Phán quyết CHỌN là của editor
// (LLM); tool chỉ thi hành file ops.
```

Schema: chapter Int required; choice Int required; reason String required ("một câu: vì sao bản này thắng — lưu làm audit"). Execute: validate `LoadCandidate(chapter, choice)` tồn tại; `PromoteCandidate`; checkpoint `AppendArtifact(ChapterScope, "draft_select", fmt.Sprintf("drafts/%02d.selection.json", chapter))`; trả `{selected: true, chapter, choice, next: "host sẽ giao hoàn thiện + commit"}`.

`build.go`: `editorTools` thêm `tools.NewSelectDraftTool(store)`; editor StopAfterToolResult thêm `|| toolName == "select_draft"`; writer thêm:

```go
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			// Chế độ best-of-N: lưu ứng viên xong là hết nhiệm vụ của run này — dừng để host
			// điều phối ứng viên kế / bước chọn. Nháp chính thức (candidate=0) không dừng.
			if toolName != "draft_chapter" {
				return false
			}
			var r struct {
				Candidate int `json:"candidate"`
			}
			_ = json.Unmarshal(result, &r)
			return r.Candidate > 0
		},
```

(giữ nguyên `StopAfterTools: []string{"commit_chapter"}` — xác nhận agentcore cho phép cả hai cùng lúc; nếu không, gộp commit_chapter vào StopAfterToolResult: `toolName=="commit_chapter" → true`.)

- [ ] **Step 4: Pass** — `go test ./internal/tools/ ./internal/store/`.
- [ ] **Step 5: Commit** — `git commit -m "feat(tools): draft candidate + select_draft cho best-of-N"`

### Task G3: config + router

- [ ] **Step 1: Test fail** — `router_test.go`:

```go
func TestRouteBestOfN(t *testing.T) {
	base := State{
		Progress: &domain.Progress{
			Phase: domain.PhaseWriting, TotalChapters: 20,
			CompletedChapters: []int{1}, CurrentChapter: 1,
		},
		LastCompleted: 1, BestOfN: 3,
	}
	// 0 ung vien -> writer viet ung vien 1/3
	s := base
	inst := Route(s)
	if inst == nil || inst.Agent != "writer" || !strings.Contains(inst.Task, "ứng viên 1/3") || inst.Chapter != 2 {
		t.Fatalf("got %+v", inst)
	}
	// du 3 ung vien -> editor chon
	s = base
	s.NextCandidates = 3
	if inst = Route(s); inst == nil || inst.Agent != "editor" || !strings.Contains(inst.Task, "select_draft") {
		t.Fatalf("got %+v", inst)
	}
	// da chon -> writer hoan thien
	s = base
	s.NextHasSelection = true
	if inst = Route(s); inst == nil || inst.Agent != "writer" || !strings.Contains(inst.Task, "Hoàn thiện chương 2") {
		t.Fatalf("got %+v", inst)
	}
	// tat best-of-N -> nhu cu
	s = base
	s.BestOfN = 0
	if inst = Route(s); inst == nil || inst.Task != "Viết chương 2" {
		t.Fatalf("got %+v", inst)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement**

`config.go` — `QualityConfig` thêm:

```go
	// BestOfN: số bản nháp ứng viên mỗi chương mới (0/1 = 1 bản, tắt). Trần 3: quá 3 bản
	// chi phí tăng tuyến tính nhưng chất lượng bản thắng tăng theo log — không đáng.
	BestOfN int `json:"best_of_n,omitempty"`
```

Trong hàm Validate của Config (tìm nơi validate providers/roles hiện có) thêm:

```go
	if c.Quality.BestOfN < 0 || c.Quality.BestOfN > 3 {
		return fmt.Errorf("quality.best_of_n phải trong [0..3], got %d: %w", c.Quality.BestOfN, errs.ErrConfig)
	}
```

`router.go` — State thêm:

```go
	// Best-of-N (Dispatcher tiêm BestOfN từ config; hai fact sau do LoadState đọc đĩa cho NextChapter):
	BestOfN          int
	NextCandidates   int  // số ứng viên đã có của chương kế
	NextHasSelection bool // đã chọn xong (drafts/NN.selection.json) nhưng chương chưa commit
```

Bước 11 của `Route` thay bằng:

```go
	// 11. Viết chương kế — có nhánh best-of-N: thu ứng viên → chọn → hoàn thiện.
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if s.BestOfN > 1 && !s.NextHasSelection {
		if s.NextCandidates < s.BestOfN {
			k := s.NextCandidates + 1
			return &Instruction{
				Agent: "writer",
				Task: fmt.Sprintf(
					"Viết bản nháp ứng viên %d/%d cho chương %d: nếu chương chưa có kế hoạch thì plan_chapter trước; sau đó draft_chapter(chapter=%d, mode=write, candidate=%d) rồi DỪNG — không đọc ứng viên khác, không check_consistency, không commit_chapter",
					k, s.BestOfN, next, next, k),
				Reason:  "Best-of-N: thu thập ứng viên độc lập",
				Chapter: next,
			}
		}
		return &Instruction{
			Agent: "editor",
			Task: fmt.Sprintf(
				"Chọn bản nháp tốt nhất cho chương %d: đọc lần lượt %d ứng viên qua read_chapter(chapter=%d, source=candidate, candidate=k), so theo 7 chiều + hợp đồng chương, rồi gọi select_draft",
				next, s.BestOfN, next),
			Reason: "Best-of-N: đủ ứng viên, cần phán quyết chọn",
		}
	}
	task := fmt.Sprintf("Viết chương %d", next)
	reason := "Tiếp tục viết chương tiếp theo"
	if s.BestOfN > 1 && s.NextHasSelection {
		task = fmt.Sprintf("Hoàn thiện chương %d từ bản nháp đã chọn: read_chapter(chapter=%d, source=draft), chỉnh sửa nhỏ nếu cần, check_consistency, commit_chapter", next, next)
		reason = "Best-of-N: bản thắng đã chọn, cần hoàn thiện và lưu"
	}
	return &Instruction{Agent: "writer", Task: task, Reason: reason, Chapter: next}
```

`state.go` — cuối LoadState:

```go
	// Best-of-N: fact ứng viên/chọn của chương kế (đĩa). BestOfN do Dispatcher tiêm.
	if next := progress.NextChapter(); next > 0 {
		s.NextCandidates = store.Drafts.CountCandidates(next)
		_, s.NextHasSelection = store.Drafts.HasSelection(next)
	}
```

`dispatcher.go`: `Dispatch()` thêm `state.BestOfN = d.opts.Quality.BestOfN`. Lưu ý pre-mark StartChapter hiện có (`dispatcher.go:86-94`) áp cho cả nhiệm vụ ứng viên (Chapter>0) — giữ nguyên, đúng ngữ nghĩa "đang tiến hành".

`writer.md` — "## Giao thức thực thi" thêm:

```markdown
- Nhiệm vụ "bản nháp ứng viên k/K": chỉ plan_chapter (nếu thiếu) + draft_chapter(candidate=k)
  rồi DỪNG. Cấm đọc ứng viên khác (mỗi bản phải độc lập), cấm check_consistency/commit.
- Nhiệm vụ "Hoàn thiện chương N từ bản nháp đã chọn": KHÔNG viết lại từ đầu — đọc draft,
  sửa tối thiểu (mạch nối, lỗi nhỏ), rồi check_consistency + commit_chapter như thường.
```

`editor.md` — thêm section ngắn sau light check:

```markdown
## Chế độ chọn bản nháp (best-of-N)

Nhiệm vụ "Chọn bản nháp tốt nhất chương N": đọc TỪNG ứng viên trọn vẹn, so trên cùng thước
(7 chiều + hợp đồng chương + user_rules), chọn bản có TRẦN cao nhất (điểm mạnh nổi bật) thay vì
bản tròn trịa không hồn — lỗi nhỏ sửa được ở vòng hoàn thiện, sự sống của văn thì không.
Gọi select_draft(chapter, choice, reason). Cấm save_review/sửa văn trong chế độ này.
```

`config.example.jsonc`: `"quality": { "light_check": true, "best_of_n": 2 },` (dạng comment).

- [ ] **Step 4: Pass** — `go test ./internal/host/flow/ ./internal/bootstrap/ -v`; `go build ./...`.
- [ ] **Step 5: Commit** — `git commit -m "feat(flow): best-of-N — thu ung vien, editor chon, writer hoan thien"`

### Task G4: candidate đa model (điểm mấu chốt với fleet rẻ)

Đa dạng THẬT đến từ model khác nhau, không phải temp khác nhau — 3 candidate cùng một model rẻ vẫn cùng một khuôn tật. Bạn có 3-4 model local: mỗi ứng viên một model.

- [ ] **Step 1:** Config: `QualityConfig` thêm:

```go
	// CandidateModels: model cho từng bản ứng viên best-of-N (phần tử k-1 cho ứng viên k).
	// Thiếu/ngắn hơn BestOfN → ứng viên còn lại dùng model writer hiện tại. Chỉ có nghĩa khi BestOfN>1.
	CandidateModels []ModelRef `json:"candidate_models,omitempty"`
```

Validate: từng ref phải trỏ provider đã khai trong `Providers` (tái dùng đúng kiểu check của fallbacks).

- [ ] **Step 2:** Cơ chế swap: `ModelSet.Swap(role, provider, model)` đã hot-swap an toàn (request đang chạy giữ instance cũ — `models.go:46-53`). `Dispatcher` giữ `models *bootstrap.ModelSet` + `candidateModels []ModelRef` (qua `DispatchOptions`); trong `Dispatch()`, khi instruction là nhiệm vụ ứng viên k (nhận biết: `inst.Agent=="writer" && strings.Contains(inst.Task, "ứng viên")` — XẤU; thay bằng field mới `Instruction.CandidateIndex int` do Route điền, 0 = không phải candidate):

```go
	// Xoay model theo ứng viên: variance thật đến từ model khác nhau. Swap hot-safe;
	// lỗi swap chỉ warn — thà candidate chạy model hiện tại còn hơn đứng flow.
	if inst.CandidateIndex > 0 && len(d.candidateModels) >= inst.CandidateIndex {
		ref := d.candidateModels[inst.CandidateIndex-1]
		if err := d.models.Swap("writer", ref.Provider, ref.Model); err != nil {
			slog.Warn("swap candidate model that bai", "module", "host.flow", "k", inst.CandidateIndex, "err", err)
		}
	}
```

Nhiệm vụ "Hoàn thiện chương N" (sau selection): swap writer VỀ model gốc — Dispatcher lưu `(provider, model)` của writer đọc một lần lúc khởi tạo (`models.CurrentSelection("writer")`), swap về trước khi phát lệnh hoàn thiện. Route điền `CandidateIndex` ở nhánh ứng viên; test router assert field này.

- [ ] **Step 3:** Test: router điền CandidateIndex đúng k; config validate ref lạ → lỗi. Chạy tay 1 chương best_of_n=3 + 3 model: session log `_meta.model` của 3 lượt ứng viên là 3 model khác nhau, lượt hoàn thiện về model gốc.
- [ ] **Step 4: Commit** — `git commit -m "feat(flow): candidate da model — moi ung vien mot model local khac nhau"`

### Acceptance

- `best_of_n=0/1` hoặc thiếu: mọi test route cũ xanh nguyên, không file cand nào được tạo.
- `best_of_n=3`: trình tự dispatch quan sát được: writer(ứng viên 1)→writer(2)→writer(3)→editor(chọn)→writer(hoàn thiện+commit); crash tại bất kỳ điểm nào, Resume tính lại đúng bước từ file đĩa.
- Rewrite queue (PendingRewrites) KHÔNG đi qua nhánh candidate (ưu tiên 3 đứng trước 11).

### Tác động / mặt trái

- Ưu: đòn bẩy chất lượng lớn nhất plan; nhiệt cao per-role (Part A) làm variance thành tài sản.
- Trái: chi phí draft ×K (chỉ bật khi rotation gánh nổi); editor chọn sai thì mất bản hay — giảm rủi ro bằng prompt "chọn trần cao nhất" + reason bắt buộc để audit.

---

## Part H — Ensemble review: K lượt chấm độc lập, code gộp (HOÃN — quyết sau khi G chạy thật)

> **Trạng thái (bản sửa fleet rẻ): HOÃN.** K lượt từ cùng một model rẻ = K lần cùng một bias
> (điểm dồn 70-85, gate 60/80 không nổ) — median của bias vẫn là bias. CHỈ làm Part H khi:
> (1) đã chạy G + D trọn ≥1 sách và dữ liệu cho thấy verdict batch review vẫn nhiễu đáng kể,
> VÀ (2) mỗi vote một MODEL khác nhau (thêm `quality.vote_models []ModelRef`, cơ chế swap y hệt
> Task G4 nhưng cho role editor). Thiết kế dưới đây giữ nguyên làm tài liệu — cộng thêm
> vote_models khi triển khai.

### Bối cảnh & nguyên nhân

Verdict rewrite/polish/accept treo trên MỘT lượt chấm của editor; scorecard gate (`save_review.go:327`) nhạy với nhiễu (chênh vài điểm quanh 60/80 lật cả verdict). Lật: K lượt chấm độc lập (temp thấp — Part A), code gộp bằng phép ĐẾM (median điểm, majority verdict, worst contract) — code làm đúng việc code giỏi, mỗi phán quyết đơn lẻ vẫn của LLM. Chỉ áp cho review global/arc (điểm quyết định đắt nhất); light check và review chương lẻ giữ 1 lượt.

### Files sở hữu

- Create: `internal/tools/save_review_vote.go`
- Modify: `internal/tools/save_review.go` (tách core dùng chung), `internal/store/world.go` (vote IO), `internal/host/flow/router.go` + `state.go` + `dispatcher.go`, `internal/bootstrap/config.go` (Quality.ReviewVotes + validate), `internal/agents/build.go` (editor tools + stop), `assets/prompts/editor.md`, `internal/bootstrap/config.example.jsonc`
- Test: `save_review_vote_test.go` (mới — gồm aggregate), `world_test.go`, `router_test.go`

### Interfaces

- Consumes: `applyReviewAndRoute` tách từ `save_review.go` (toàn bộ thân Execute từ "đọc prior review" đến response map). Produces: `WorldStore.SaveReviewVote(r domain.ReviewEntry, idx int)` / `LoadReviewVotes(chapter int) []domain.ReviewEntry` / `CountReviewVotes(chapter int) int` / `ClearReviewVotes(chapter int) error` (file `reviews/NN-vote-i.json`); `aggregateVotes([]domain.ReviewEntry) domain.ReviewEntry`; `QualityConfig.ReviewVotes int`; `State.{ReviewVotes, PendingReviewVotes int}`.

### Task H1: refactor save_review — tách core

- [ ] **Step 1:** Tách thân `SaveReviewTool.Execute` (từ dòng "Đọc review record trước" `save_review.go:84` đến hết build response `:230`) thành:

```go
// applyReviewAndRoute là lõi dùng chung của save_review và save_review_vote (lượt chốt):
// gate thẻ điểm + trần rewrite + ghi review + cập nhật Flow/PendingRewrites + checkpoint.
// r đã qua validateReviewEntry và coercion verdict-from-score.
func applyReviewAndRoute(st *store.Store, r domain.ReviewEntry) (map[string]any, error)
```

`Execute` cũ = unmarshal + coercion + validate + `applyReviewAndRoute` + `json.Marshal`. KHÔNG đổi hành vi — toàn bộ test `save_review_test.go` hiện có phải xanh nguyên, đó là lưới an toàn của refactor.

- [ ] **Step 2:** `go test ./internal/tools/ -run TestSaveReview -v` xanh. Commit — `git commit -m "refactor(tools): tach applyReviewAndRoute khoi save_review"`

### Task H2: vote IO + aggregate

- [ ] **Step 1: Test fail:**

`world_test.go`:

```go
func TestReviewVoteIO(t *testing.T) {
	s := newTestWorldStore(t)
	r := domain.ReviewEntry{Chapter: 10, Scope: "global", Verdict: "accept", Summary: "v1"}
	if err := s.SaveReviewVote(r, 1); err != nil {
		t.Fatal(err)
	}
	r.Summary = "v2"
	if err := s.SaveReviewVote(r, 2); err != nil {
		t.Fatal(err)
	}
	if n := s.CountReviewVotes(10); n != 2 {
		t.Fatalf("muon 2 vote, got %d", n)
	}
	votes, err := s.LoadReviewVotes(10)
	if err != nil || len(votes) != 2 || votes[1].Summary != "v2" {
		t.Fatalf("load votes: %+v %v", votes, err)
	}
	if err := s.ClearReviewVotes(10); err != nil {
		t.Fatal(err)
	}
	if n := s.CountReviewVotes(10); n != 0 {
		t.Fatalf("clear roi van con %d", n)
	}
}
```

`save_review_vote_test.go` — aggregate thuần:

```go
func TestAggregateVotes(t *testing.T) {
	mk := func(verdict string, aesthetic, consistency int, contract string) domain.ReviewEntry {
		dims := []domain.DimensionScore{}
		for _, d := range []string{"consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic"} {
			score := 85
			if d == "aesthetic" {
				score = aesthetic
			}
			if d == "consistency" {
				score = consistency
			}
			dims = append(dims, domain.DimensionScore{Dimension: d, Score: score, Comment: "c"})
		}
		return domain.ReviewEntry{
			Chapter: 10, Scope: "global", Verdict: verdict, Summary: "s",
			Dimensions: dims, ContractStatus: contract,
			AffectedChapters: []int{9},
		}
	}
	votes := []domain.ReviewEntry{
		mk("accept", 90, 85, "met"),
		mk("polish", 62, 80, "partial"),
		mk("accept", 70, 88, "met"),
	}
	agg := aggregateVotes(votes)
	// median aesthetic của {90,62,70} = 70; consistency {85,80,88} = 85
	if s := findDimension(agg.Dimensions, "aesthetic").Score; s != 70 {
		t.Fatalf("median aesthetic muon 70, got %d", s)
	}
	if agg.Verdict != "accept" { // majority 2/3
		t.Fatalf("majority verdict muon accept, got %s", agg.Verdict)
	}
	if agg.ContractStatus != "partial" { // worst thang
		t.Fatalf("contract worst muon partial, got %s", agg.ContractStatus)
	}
	// tie 1-1 (2 votes) -> nghieng ve verdict nang hon
	agg2 := aggregateVotes([]domain.ReviewEntry{mk("accept", 80, 80, "met"), mk("rewrite", 80, 50, "met")})
	if agg2.Verdict != "rewrite" {
		t.Fatalf("tie phai nghieng nang, got %s", agg2.Verdict)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement**

`world.go` (pattern y hệt review IO; `LoadReviewVotes` quét i=1.. tăng dần đến khi IsNotExist; `ClearReviewVotes` xóa từng file — io helper cần hàm Remove: soi `store/io` khi implement, nếu chưa có Remove thì thêm vào io helper với test riêng).

`save_review_vote.go`:

```go
// aggregateVotes gộp K lượt chấm độc lập thành một ReviewEntry bằng phép ĐẾM thuần:
//   - điểm mỗi chiều: median (bền với 1 lượt chấm lệch — chính là nhiễu ta muốn triệt);
//   - verdict: đa số; hòa → nghiêng verdict NẶNG hơn (rewrite > polish > accept — thà xem lại
//     nhầm còn hơn ship nhầm);
//   - contract_status: xấu nhất thắng (missed > partial > met);
//   - issues: hợp tất cả (mỗi lượt thấy vấn đề khác nhau là ĐẶC TÍNH của ensemble, không dedup
//     ngữ nghĩa — việc của editor vòng sau);
//   - affected_chapters: hợp + khử trùng lặp; summary: ghép có đánh số lượt.
// Phán quyết từng lượt vẫn 100% của LLM; hàm này không chứa suy luận ngữ nghĩa nào.
func aggregateVotes(votes []domain.ReviewEntry) domain.ReviewEntry
```

(median: sort điểm từng dimension, lấy giữa — chẵn thì phần tử thấp hơn trong hai giữa, thiên nghiêm; verdict rank map `{"accept":0,"polish":1,"rewrite":2}`.)

Tool `save_review_vote`: Schema = COPY schema của save_review (nguyên bảy chiều — engineer đọc task này không đọc task khác: copy từ `save_review.go:35-61`, đổi mô tả đầu: "Lưu MỘT lượt chấm độc lập trong chế độ ensemble..."). Execute:

```go
	// coercion verdict-from-score + validate y het save_review
	...
	k := t.votes // số lượt yêu cầu, tiêm khi construct: NewSaveReviewVoteTool(store, votes int)
	idx := t.store.World.CountReviewVotes(r.Chapter) + 1
	if err := t.store.World.SaveReviewVote(r, idx); err != nil {
		return nil, fmt.Errorf("save vote: %w", err)
	}
	if idx < k {
		return json.Marshal(map[string]any{
			"saved_vote": idx, "of": k, "final": false,
			"next": "còn lượt chấm — host sẽ điều phối lượt kế",
		})
	}
	votes, err := t.store.World.LoadReviewVotes(r.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load votes: %w", err)
	}
	agg := aggregateVotes(votes)
	resp, err := applyReviewAndRoute(t.store, agg)
	if err != nil {
		return nil, err
	}
	if cerr := t.store.World.ClearReviewVotes(r.Chapter); cerr != nil {
		// vote thừa không phá logic (review đã chốt trên đĩa) — chỉ warn
		slog.Warn("don vote that bai", "module", "review", "chapter", r.Chapter, "err", cerr)
	}
	resp["aggregated_from"] = k
	return json.Marshal(resp)
```

- [ ] **Step 4: Pass** — `go test ./internal/tools/ ./internal/store/ -v`.
- [ ] **Step 5: Commit** — `git commit -m "feat(quality): ensemble review — vote doc lap, code gop median/majority"`

### Task H3: config + router + prompts

- [ ] **Step 1: Test fail** — `router_test.go`:

```go
func TestRouteFlatReviewWithVotes(t *testing.T) {
	s := State{
		Progress: &domain.Progress{
			Phase: domain.PhaseWriting, TotalChapters: 20,
			CompletedChapters: []int{1, 2, 3, 4, 5}, CurrentChapter: 5,
		},
		LastCompleted: 5, HasPendingFlatReview: true,
		ReviewVotes: 3, PendingReviewVotes: 1,
	}
	inst := Route(s)
	if inst == nil || inst.Agent != "editor" || !strings.Contains(inst.Task, "lượt chấm độc lập 2/3") ||
		!strings.Contains(inst.Task, "save_review_vote") {
		t.Fatalf("got %+v", inst)
	}
	s.ReviewVotes = 0 // tat -> task cu
	if inst = Route(s); inst == nil || strings.Contains(inst.Task, "save_review_vote") {
		t.Fatalf("tat votes van ra vote task: %+v", inst)
	}
}
```

- [ ] **Step 2: Fail.**
- [ ] **Step 3: Implement**

`config.go` — QualityConfig thêm:

```go
	// ReviewVotes: số lượt chấm độc lập cho review global/arc (0/1 = 1 lượt, tắt). Trần 5.
	// Lẻ tốt hơn chẵn (majority không hòa); 3 là điểm cân bằng chi phí/độ ổn định.
	ReviewVotes int `json:"review_votes,omitempty"`
```

Validate: `[0..5]`, lỗi ngoài biên như BestOfN.

`router.go` — State thêm `ReviewVotes int` (tiêm từ config) + `PendingReviewVotes int` (đĩa). Nhánh flat review (bước 10) sửa:

```go
	if !p.Layered && s.HasPendingFlatReview {
		to := (s.LastCompleted / domain.ReviewInterval) * domain.ReviewInterval
		from := to - domain.ReviewInterval + 1
		if s.ReviewVotes > 1 {
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Lượt chấm độc lập %d/%d cho batch chương %d-%d (scope=global): chấm đủ 7 chiều rồi gọi save_review_vote; CẤM đọc reviews/ của các lượt trước",
					s.PendingReviewVotes+1, s.ReviewVotes, from, to),
				Reason: "Ensemble review: thu lượt chấm",
			}
		}
		return &Instruction{ /* nhánh cũ giữ nguyên */ }
	}
```

Nhánh arc review (bước 5, `!s.HasArcReview`) sửa tương tự (task vote khi ReviewVotes>1, scope=arc).

`state.go` — trong khối flat review, khi set `HasPendingFlatReview=true` thêm `s.PendingReviewVotes = store.World.CountReviewVotes(mark)`; khối arc: khi `!s.HasArcReview` → `s.PendingReviewVotes = store.World.CountReviewVotes(s.LastCompleted)`.

`dispatcher.go`: `state.ReviewVotes = d.opts.Quality.ReviewVotes`.

`build.go`: `editorTools` thêm `tools.NewSaveReviewVoteTool(store, cfg.Quality.ReviewVotes)` (chỉ thêm khi `cfg.Quality.ReviewVotes > 1` — không có trong tool list thì editor không gọi nhầm); editor StopAfterToolResult thêm `save_review_vote` NHƯNG chỉ khi kết quả `final:false`... — đơn giản hơn: luôn dừng sau save_review_vote (lượt chốt cũng dừng — host route tiếp, y hệt save_review hiện không hard-stop nhưng vote là nhiệm-vụ-một-việc):

```go
			return toolName == "save_arc_summary" || toolName == "save_volume_summary" ||
				toolName == "save_chapter_check" || toolName == "select_draft" ||
				toolName == "save_review_vote"
```

Lưu ý: lượt chốt dừng sau khi vote đã aggregate + applyReviewAndRoute xong → dispatcher nhận EventToolExecEnd của subagent → Route thấy PendingRewrites/Flow mới → đi tiếp đúng. Riêng nhiệm vụ batch review 1-lượt (votes tắt) giữ hành vi cũ (save_review không hard-stop, EditorStopGuard lo).

`editor.md` — thêm dưới "## Chế độ đánh giá định kỳ theo batch":

```markdown
### Chế độ ensemble (lượt chấm độc lập i/K)

Nhiệm vụ ghi "lượt chấm độc lập i/K": chấm như một lượt review batch đầy đủ nhưng kết thúc
bằng save_review_vote (KHÔNG save_review). Độc lập nghĩa là: không đọc reviews/ của lượt
trước, không đoán các lượt khác chấm gì — giá trị của ensemble nằm ở chỗ mỗi lượt nhìn bằng
mắt riêng. Hệ thống tự gộp K lượt (median điểm, đa số verdict) ở lượt cuối.
```

`config.example.jsonc`: `"quality": { "light_check": true, "best_of_n": 2, "review_votes": 3 }` (comment).

- [ ] **Step 4: Pass** — `go test ./internal/host/flow/ ./internal/bootstrap/ ./internal/tools/`; `go build ./...`.
- [ ] **Step 5: Commit** — `git commit -m "feat(flow): route ensemble votes cho review global/arc"`

### Acceptance

- `review_votes=3`: batch review chạy 3 dispatch editor liên tiếp (task đánh số 1/3→2/3→3/3), file `NN-vote-1..3.json` xuất hiện rồi bị dọn, review chốt `NN-global.json` mang điểm median + verdict majority, gate/trần rewrite hoạt động y cũ trên bản gộp.
- `review_votes` tắt: không tool vote trong editor tools, task và hành vi cũ nguyên vẹn.
- Crash giữa lượt 2: Resume đếm lại vote từ đĩa, phát đúng "lượt 3/3" (hoặc 2/3 nếu file chưa ghi).

### Tác động / mặt trái

- Ưu: verdict đắt nhất hệ thống (rewrite cả batch) không còn treo trên một lần lắc xúc xắc.
- Trái: chi phí review ×K tại mốc 5 chương (K=3 → +2 call/5 chương — rẻ hơn nhiều so với một rewrite oan); editor gọi nhầm save_review thay vote → review chốt sớm 1 lượt (degrade về hành vi cũ, không hỏng — prompt đã cấm, chấp nhận).

---

# Ma trận file × Part (kiểm tra không giẫm chân)

| File | A | B | C | D | E | F | G | H |
|---|---|---|---|---|---|---|---|---|
| `bootstrap/config.go` | ✏ | | | ✏ | ✏ | | ✏ | ✏ |
| `bootstrap/models.go` | ✏ | | | | | | | |
| `bootstrap/config.example.jsonc` | ✏ | | | ✏ | ✏ | | ✏ | ✏ |
| `stylestat/stylestat.go` | | ✏ | | | | | | |
| `tools/commit_chapter.go` | | ✏ | | | | | | |
| `store/world.go` | | | ✏ | ✏ | | ✏ | | ✏ |
| `store/drafts.go` | | | ✏ | | | | ✏ | |
| `tools/novel_context_builders.go` | | | ✏ | | | ✏ | | |
| `tools/save_chapter_check.go` (mới) | | | | ✚ | | | | |
| `tools/save_notebook.go` (mới) | | | | | | ✚ | | |
| `tools/select_draft.go` (mới) | | | | | | | ✚ | |
| `tools/save_review_vote.go` (mới) | | | | | | | | ✚ |
| `tools/save_review.go` | | | | | | | | ✏ |
| `tools/draft_chapter.go` | | | | | | | ✏ | |
| `tools/read_chapter.go` | | | | ✏ | | | ✏ | |
| `domain/review.go` | | | | ✏ | | ✏ | | |
| `host/flow/router.go` | | | | ✏ | ✏ | | ✏ | ✏ |
| `host/flow/state.go` | | | | ✏ | | | ✏ | ✏ |
| `host/flow/dispatcher.go` | | | | ✏ | ✏ | | ✏ | ✏ |
| `host/host.go` | | | | ✏ | ✏ | | | |
| `agents/build.go` | | | | ✏ | ✏ | ✏ | ✏ | ✏ |
| `entry/tui/command_model.go` | | | | | ✏ | | | |
| `assets/prompts/writer.md` | | | | | | ✏ | ✏ | |
| `assets/prompts/editor.md` | | | | ✏ | | ✏ | ✏ | ✏ |
| `assets/prompts/coordinator.md` | | | | | ✏ | | | |

Wave 1 (A∥B∥C): không file chung → song song an toàn. Wave 2 (D→E→F) và Wave 3 (G→H): TUẦN TỰ vì chung router/dispatcher/build/editor.md.

# Trình tự & phụ thuộc (bản sửa fleet rẻ — ĐÈ lên số wave của các header phía trên)

```
[merge feat/ratelimit]
        │
  0A probe → 0B grammar → (0C ∥ 0D)     WAVE 0 — nền model rẻ, làm trước tất cả
        │
   ┌────┴────┐
   A         B (+B3)                     đợt 1 — song song
   └────┬────┘
        D                                đợt 2 — light check (nền QualityConfig+DispatchOptions)
        │
        G (+G4 đa model)                 đợt 3 — best-of-N, đòn bẩy chính với judge yếu
        │
   E → C                                 đợt 4 — reviser; C sau D vì cần mật độ review
        │
   F / H                                 đợt 5 — F opt-in (điều kiện ở header F),
                                         H hoãn (điều kiện ở header H) — quyết bằng
                                         dữ liệu ≥1 sách chạy thật
```

Điểm dừng tự nhiên: sau MỖI đợt hệ thống chạy trọn vẹn và mọi tính năng mới đều tắt được bằng config (mặc định tắt: `Quality` zero value + không config role reviser = hành vi hôm nay). Sau mỗi đợt: chạy một sách test ngắn 6-8 chương end-to-end + dừng ở human gate đọc thử trước khi vào đợt kế.

# Đo thành công — CHỈ số cơ học + phán quyết người dùng (không tin điểm judge rẻ)

- `style_repetition` warnings/chương giảm dần theo số chương (vòng B khép hoạt động — đếm từ commit output).
- stylestat: `per_chapter` các pattern + số `repeated_sentences` KHÔNG tăng theo độ dài sách.
- Tỷ lệ chương bị người dùng chê tại human gate (ghi chú `/gate`) giảm giữa các đợt — đây là thước đo chính.
- Token/chương ổn định (không phình vì retry JSON hỏng — chứng cứ 0B hoạt động).
- Tham khảo có dè chừng (số do judge rẻ sinh): `quality_debt` thưa đi, `rewrite_count` trung bình giảm.
