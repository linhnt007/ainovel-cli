# Kế hoạch tác vụ có thể giao cho model nhỏ

> **Cách dùng:** Mỗi task bên dưới **tự chứa** — model nhỏ chỉ cần đọc đúng task là đủ ngữ cảnh.
> Tasks trong cùng Wave không chạm chung file → chạy song song an toàn.
> Wave sau chỉ bắt đầu khi Wave trước đã merge + `go test ./...` xanh.

---

## Tình trạng hiện tại

Toàn bộ 13 Part trong `improvement-plan.md` (Wave 1-3) đã được thực thi:
- Part A-G (Wave 1): ✅ wordcount, rules, truncateJSON, script, cocreate tests, foreshadow, stage tail
- Part H-K (Wave 2): ✅ router flat review, rewrite cap + aesthetic, budget/cocreate
- Part L-M (Wave 3): ✅ narrative contract, voice card

Các task bên dưới là **cải thiện mới** phát sinh từ phân tích sâu flow/output quality,
độc lập với các Part đã hoàn thành.

---

## Quy tắc chung

1. **Không đụng file ngoài danh sách "Files sở hữu"** của task. Nếu phát hiện buộc phải sửa file ngoài → DỪNG, báo cáo.
2. Mọi schema mới phải **backward-compatible**: trường mới optional, thiếu = hành vi cũ.
3. Comment code viết tiếng Việt, giải thích *tại sao*.
4. Sau khi xong: chạy `go test ./...` (hoặc test cụ thể nếu ghi rõ). Báo cáo: file đã đổi + tóm tắt diff + kết quả test.
5. Con số dòng (`file:line`) là ước lượng — luôn xác minh bằng nội dung thực trước khi sửa.

---

# WAVE 4 — Context & Flow (độc lập, chạy song song)

## Task 4A — Giữ rewrite_brief qua context compression

**Mức độ: P1 — writer mất chỉ thị khi chương dài, context bị nén.**

### Vấn đề
ContextManager của writer dùng `StoreSummaryCompactStrategy` (tại `internal/agents/ctxpack/strategy.go`)
khi context đầy. Strategy này cắt messages cũ dựa trên `KeepRecentTokens`, giữ lại K tin nhắn cuối.
Nhưng `rewrite_brief` (được inject vào `working_memory` bởi `prepareChapterContext` tại
`internal/tools/novel_context_builders.go:291-307`) nằm trong tool result của `novel_context` —
tool result này có thể bị cắt nếu nằm ngoài cửa sổ giữ lại.

Khi writer đang rewrite chương và context đầy → `rewrite_brief` bị mất → writer không còn
biết lý do rewrite + các issues cần sửa → viết lại không đúng hướng.

### Thay đổi
1. **File mới:** `internal/agents/ctxpack/strategy_keep_brief.go`
   - Viết strategy mới `KeepRewriteBriefStrategy` implements `corecontext.Strategy`
   - Strategy này scan messages, tìm tool result của `novel_context` chứa JSON key `rewrite_brief`
   - Nếu tìm thấy: đánh dấu message đó là `pinned` (không bị cắt bởi các strategy khác)
   - Implementation: dùng `corecontext.Strategy` interface, Name() trả `"keep_rewrite_brief"`,
     Apply() trả về msgs không thay đổi nhưng gắn metadata `Pinned=true` cho messages chứa rewrite_brief

2. **File sửa:** `internal/agents/context_manager.go:38-43`
   - Thêm `KeepRewriteBriefStrategy` vào strategies, CHẠY TRƯỚC `FullSummary`:
   ```go
   strategies := []corecontext.Strategy{
       corecontext.NewToolResultMicrocompact(tc),
       corecontext.NewLightTrim(corecontext.LightTrimConfig{}),
       NewKeepRewriteBriefStrategy(), // MỚI
   }
   strategies = append(strategies, cfg.ExtraStrategies...)
   strategies = append(strategies, corecontext.NewFullSummary(sc))
   ```

3. **File mới:** `internal/agents/ctxpack/strategy_keep_brief_test.go`
   - Test: 10 messages, message 3 là tool result novel_context chứa `rewrite_brief`, context nhỏ →
     message 3 vẫn còn sau compression
   - Test: không có rewrite_brief → strategy là no-op, không thay đổi behavior

### Files sở hữu
- `internal/agents/ctxpack/strategy_keep_brief.go` (mới)
- `internal/agents/ctxpack/strategy_keep_brief_test.go` (mới)
- `internal/agents/context_manager.go` (chỉ thêm 1 dòng)

### Acceptance
- `go test ./internal/agents/...` xanh
- Test证明 rewrite_brief survive compression

### Ước lượng
- ~80-120 dòng code mới
- 2-3 giờ

---

## Task 4B — Inject prior review issues khi rewrite

**Mức độ: P1 — writer rewrite mà không biết vấn đề cũ.**

### Vấn đề
Khi `isRewrite=true` trong `prepareChapterContext` (`novel_context_builders.go:291-307`),
`rewrite_brief` chỉ chứa review của CHƯƠNG ĐANG WRITE. Nhưng vấn đề từ các chương TRƯỚC
(review cũ có severity >= "error") có thể ảnh hưởng đến chương hiện tại — writer không thấy.

Ví dụ: chương 5 có vấn đề "nhân vật A mất tích giữa chương" (severity error), writer viết
chương 6 nhưng không biết chương 5 có vấn đề → viết tiếp mà không sửa.

### Thay đổi
1. **File sửa:** `internal/tools/novel_context_builders.go` — trong `prepareChapterContext`,
   sau khối `if isRewrite` (quanh dòng 291-307):
   - Load reviews của chapter-1, chapter-2, chapter-3 (nếu tồn tại)
   - Lọc chỉ lấy issues có `Severity == "error"` hoặc `Severity == "critical"`
   - Thêm vào `brief["prior_issues"]` — mảng các issues từ các chương lân cận
   - Giới hạn tối đa 10 issues (ưu tiên chương gần nhất)

2. **File sửa:** `internal/tools/novel_context_builders.go` — trong `buildChapterContext`,
   thêm `prior_issues` vào working_memory nếu có

### Files sở hữu
- `internal/tools/novel_context_builders.go`
- `internal/tools/novel_context_test.go` (thêm test)

### Acceptance
- Test: rewrite chapter 5, chapter 4 có 2 issues severity error → working memory chứa `prior_issues` với 2 items
- Test: rewrite chapter 2 (không có chapter trước) → không có `prior_issues`
- Test: các issues severity warning → không được inject
- `go test ./internal/tools/...` xanh

### Ước lượng
- ~40-60 dòng code
- 1-2 giờ

---

## Task 4C — Graceful degradation khi dispatcher gặp lỗi

**Mức độ: P2 — lỗi IO khiến entire flow dừng mà không thông báo.**

### Vấn đề
`LoadState` (`internal/host/flow/state.go:11-60`) đọc nhiều store nhưng khi遇到 lỗi,
chỉ set `has*=false` và trả về state rỗng. Router sau đó trả `nil` (để Coordinator tự quyết định).
Nhưng Coordinator không có đủ thông tin → có thể gọi sai subagent hoặc loop vô hạn.

Ngoài ra, `Dispatch` (`internal/host/flow/dispatcher.go`) không có cơ chế retry khi gặp lỗi
transient (file locked, disk full tạm thời).

### Thay đổi
1. **File sửa:** `internal/host/flow/state.go` — trong `LoadState`:
   - Thu thập tất cả lỗi đọc vào `[]string` (warnings)
   - Trả về `State` kèm `LoadWarnings []string`
   - Nếu có warnings: log slog.Warn với danh sách lỗi

2. **File sửa:** `internal/host/flow/router.go` — thêm trường `LoadWarnings []string` vào `State`
   - Nếu `LoadWarnings` không rỗng và Router trả `nil`: trả instruction mặc định
     "tiếp tục viết bình thường" thay vì nil (để flow không chết)

3. **File sửa:** `internal/host/flow/dispatcher.go` — trong `Dispatch`:
   - Nếu `LoadState` trả warnings: emit event `EventFlowDegraded` với danh sách lỗi
   - Không thay đổi flow logic, chỉ thêm observability

### Files sở hữu
- `internal/host/flow/state.go`
- `internal/host/flow/router.go`
- `internal/host/flow/dispatcher.go`
- `internal/host/flow/state_test.go`
- `internal/host/flow/router_test.go`

### Acceptance
- Test: store đọc lỗi → state có warnings, router vẫn trả instruction hợp lệ
- Test: không có lỗi → warnings rỗng, behavior cũ
- `go test ./internal/host/flow/...` xanh

### Ước lượng
- ~50-80 dòng code
- 2-3 giờ

---

## Task 4D — Rate limit timeout thay vì block vô hạn

**Mức độ: P2 — rate limit làm treo toàn bộ flow.**

### Vấn đề
`failoverModel.pickAvailable` (`internal/bootstrap/models.go`) khi gặp rate limit từ provider
(mistral, openrouter) sẽ block chờ直到 rate limit window hết. Nếu rate limit dài (phút/giờ),
toàn bộ flow chết mà không có timeout.

### Thay đổi
1. **File sửa:** `internal/bootstrap/models.go` — trong logic pick model:
   - Nếu model bị rate limit: check thời gian đã chờ
   - Nếu chờ > `rateLimitTimeout` (hằng số mới, mặc định 60s): bỏ qua model này, thử model kế
   - Emit log warning khi bỏ qua vì timeout

2. **File mới:** `internal/bootstrap/models_test.go` (nếu chưa có) hoặc thêm test vào file test hiện có
   - Test: rate limit + timeout → model bị bỏ qua
   - Test: rate limit + chưa timeout → model vẫn được thử

### Files sở hữu
- `internal/bootstrap/models.go`
- `internal/bootstrap/models_test.go` (thêm test)

### Acceptance
- Test: mock rate limit > 60s → failover sang model khác
- Test: mock rate limit < 60s → vẫn thử model gốc
- `go test ./internal/bootstrap/...` xanh

### Ước lượng
- ~30-50 dòng code
- 1-2 giờ

---

## Task 4E — Checkpoint retry khi append thất bại

**Mức độ: P3 — checkpoint mất khi disk transient error.**

### Vấn đề
`AppendArtifact` (`internal/store/checkpoints.go`) gọi `io.WriteJSONUnlocked` — nếu disk error
transient (file locked bởi process khác, antivirus scan), artifact bị mất mà không retry.

### Thay đổi
1. **File sửa:** `internal/store/checkpoints.go` — trong `AppendArtifact`:
   - Thêm retry loop: thử tối đa 3 lần, mỗi lần chờ 100ms
   - Chỉ retry trên `os.IsTimeout` hoặc `os.IsPermission` (transient errors)
   - Nếu vẫn fail sau 3 lần: trả error như hiện tại (không nuốt)

2. **File sửa:** `internal/store/checkpoints_test.go` (thêm test)
   - Test: mock transient error → retry thành công
   - Test: mock permanent error → fail ngay lập tức

### Files sở hữu
- `internal/store/checkpoints.go`
- `internal/store/checkpoints_test.go`

### Acceptance
- Test: transient error retry → success
- Test: permanent error → fail immediately
- `go test ./internal/store/...` xanh

### Ước lượng
- ~20-30 dòng code
- 1 giờ

---

## Task 4F — Session compactMessage quá aggressive

**Mức độ: P3 — context nén mất thông tin quan trọng.**

### Vấn đề
`compactMessage` trong `internal/store/session.go` (quanh dòng 215-280) nén messages khi log
session. Nó dùng regex để thay thế placeholder nhưng có thể xóa quá nhiều thông tin —
đặc biệt khi message chứa JSON phức tạp (tool results từ `novel_context`).

### Thay đổi
1. **File sửa:** `internal/store/session.go` — trong `compactMessage`:
   - Chỉ nén messages có role `assistant` (reasoning/thinking)
   - KHÔNG nén tool results (role `tool`) — chúng chứa data quan trọng
   - Giới hạn độ dài nén: không nén xuống dưới 200 ký tự (để còn đọc được)

2. **File sửa:** `internal/store/session_test.go` (thêm test)
   - Test: tool result không bị nén
   - Test: assistant message dài bị nén đúng cách

### Files sở hữu
- `internal/store/session.go`
- `internal/store/session_test.go`

### Acceptance
- Test: tool result novel_context giữ nguyên khi compact
- Test: assistant thinking message bị nén hợp lý
- `go test ./internal/store/...` xanh

### Ước lượng
- ~20-40 dòng code
- 1 giờ

---

# Ma trận task × file (kiểm tra không giẫm chân)

| File | 4A | 4B | 4C | 4D | 4E | 4F |
|------|----|----|----|----|----|-----|
| `internal/agents/context_manager.go` | ✏️ | | | | | |
| `internal/agents/ctxpack/strategy_*.go` | ✏️ | | | | | |
| `internal/tools/novel_context_builders.go` | | ✏️ | | | | |
| `internal/tools/novel_context_test.go` | | ✏️ | | | | |
| `internal/host/flow/state.go` | | | ✏️ | | | |
| `internal/host/flow/router.go` | | | ✏️ | | | |
| `internal/host/flow/dispatcher.go` | | | ✏️ | | | |
| `internal/bootstrap/models.go` | | | | ✏️ | | |
| `internal/store/checkpoints.go` | | | | | ✏️ | |
| `internal/store/session.go` | | | | | | ✏️ |

Không có cặp task nào chạy song song mà chung file → **6 tasks song song an toàn**.

---

# Trình tự thực thi

```
Wave 4 (song song): 4A + 4B + 4C + 4D + 4E + 4F
  ↓
verify: go test ./...
  ↓
Wave 5 (nếu cần): task mới phát sinh từ kết quả Wave 4
```

Sau Wave 4: chạy thử 1 phiên headless ngắn (`--prompt` truyện test 3-5 chương)
để verify flow mượt mà hơn.

---

## Ước lượng tổng

| Wave | Tasks | Thời gian | Dòng code |
|------|-------|-----------|-----------|
| Wave 4 | 6 | 8-12 giờ | ~240-380 dòng |
| **Tổng** | **6** | **8-12 giờ** | **~240-380 dòng** |

So sánh: Wave 1-3 (đã hoàn thành) là ~2000+ dòng code across 13 Parts.
Wave 4 nhỏ hơn nhiều, tập trung vào edge cases và robustness.

---

## Ghi chú vận hành

- **Ưu tiên:** 4A > 4B > 4C > 4D > 4E > 4F (theo tác động đến output quality)
- **4A quan trọng nhất:** writer mất rewrite_brief = viết lại không đúng hướng = lãng phí token
- **4B giúp writer hiểu bối cảnh:** prior issues = viết có hệ thống thay vì cục bộ
- **4C-4F là robustness:** không cải thiện output trực tiếp nhưng giảm crash/infinite loop
- Sau khi Wave 4 xong, chạy thử Headless mode 5 chương để verify end-to-end
