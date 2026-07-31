# Báo cáo đánh giá & xử lý — Rò rỉ chất lượng tiếng Việt trong ainovel-cli

> Ngày: 2026-07-26. Phạm vi: điều tra + vá các "điểm chết" khiến chất lượng sinh truyện
> và flow không đảm bảo cho output tiếng Việt. Mọi kết luận đã đối chiếu trực tiếp mã nguồn;
> mọi thay đổi đã verify build + test trên đĩa (không dựa vào báo cáo suông của subagent).

---

## 0. Tóm tắt điều hành

Commit "Việt hoá toàn bộ repo" đã dịch `assets/*.md` (prompt chính, reference) sạch chữ Hán,
nhưng **bỏ sót logic runtime trong mã Go**. Bốn ổ nằm đúng đường dẫn chất lượng, cộng vài rò rỉ nhỏ.
Đã xác minh **6 bug/điểm chết là THẬT**, sửa toàn bộ, và verify:

- `go build ./...` → **exit 0**.
- `go test ./...` → **toàn bộ xanh, 0 fail** (sau khi xử lý cả 3 tồn đọng ở §6).
- 8 file runtime mục tiêu → **0 ký tự Hán** còn lại.
- Không mất dữ liệu sau sự cố git stash đồng thời (xem §7).

**Ba đính chính lớn so với giả định ban đầu:**
1. Model mặc định là `openrouter/google/gemini-2.5-flash`, **không phải** mistral/llamacpp local → model không phải nguyên nhân gốc.
2. **Không có** hàm `checkAndBlockRules` chặn cứng commit ở tầng code; việc "chặn" nằm ở prompt `editor.md`.
3. Ngưỡng `chapter_words: 2500-6000` là **edit local chưa commit** (sai hướng); gốc dự án = `3000-6000`.

---

## 1. Bối cảnh kỹ thuật (luồng sinh 1 chương)

Một vòng lặp LLM Coordinator duy nhất (`agents/build.go:104`) cầm tool `subagent` phân phát:

```
Coordinator (coordinator.md, MaxTurns=100_000, gate=completePhaseGate)
  └─ architect_short/long  → foundation + outline
     writer  (plan→draft→check→commit)  StopAfterTools=[commit_chapter]
     editor  (review 7 chiều + arc/vol summary)
```

- Writer có ContextManager riêng: cửa sổ theo model, `KeepRecentTokens=20000`, nén `StoreSummaryCompact`,
  `PostSummaryHook = restore.Hook()` bơm lại context store sau nén.
- Context bơm cho writer qua 2 đường: tool `novel_context` (3 phong bì working/episodic/reference) và
  ctxpack post-compact (`restore.go` bơm lại từ store khi context đầy).
- Rule check tại `commit_chapter` chỉ **trả `rule_violations` như dữ liệu**, không chặn commit ở code.
  Editor LLM (`editor.md`) mới là nơi quyết định rewrite dựa trên severity.

---

## 2. Các bug đã xác minh & cách xử lý

### #1 — Prompt nén context của Writer còn nguyên tiếng Trung *(chết người nhất)*

- **File:** `internal/agents/ctxpack/restore.go:18-95`, wired tại `build.go:276-282`.
- **Triệu chứng:** 4 const (`WriterSummarySystemPrompt`, `WriterSummaryPrompt`, `WriterUpdateSummaryPrompt`,
  `WriterTurnPrefixPrompt`) 100% tiếng Trung (`你是一个小说创作上下文摘要助手`, heading `## 当前进度/角色即时状态`…).
- **Cơ chế hại:** chương dài vượt `KeepRecentTokens=20000` → `FullSummary` nén phần cũ bằng prompt Trung →
  checkpoint tóm tắt (kèm "style anchor") lưu trong context writer bằng khung tiếng Trung → writer đọc tiếp
  từ scaffold Trung → **drift ngôn ngữ/giọng, mất tiếng Việt giữa chương dài, rủi ro rớt sang chữ Hán**.
- **Đã sửa:** dịch toàn bộ sang tiếng Việt; **đồng bộ heading với `builder.go:235-265`** (dùng chung
  "Tiến độ hiện tại", "Ảnh chụp nhân vật", "Phục bút đang hoạt động", "Vấn đề chờ sửa từ biên tập") để bản
  summary sinh ra khớp bản restore; **giữ nguyên tag `<analysis>/<summary>/<previous-summary>`** vì
  `agentcore/context/summary.go` parse literal các tag này.
- **Đính chính:** giả định "builder.go còn tiếng Trung" là SAI — `builder.go` đã sạch từ trước.

### #2 — Kiểm tra foundation hỏng với tiếng Việt *(bug kép)*

- **File:** `internal/tools/premise_structure.go:9-132`.
- **Triệu chứng:** `premiseHeadingAliases` + `requiredPremiseHeadings` toàn heading Trung, match **exact**;
  architect prompt (`architect-*.md`) ra lệnh viết heading **tiếng Việt**. Heading Việt không bao giờ khớp →
  `sections` rỗng → `template_ready=false` **luôn**, `missing` = danh sách tiếng Trung chảy vào `Foundation`
  envelope gửi LLM. Cơ chế kiểm tra đầy đủ premise **vô hiệu hoàn toàn**.
- **Đã sửa:** viết lại heading sang tiếng Việt **khớp chính xác** `architect-short.md`/`architect-long.md`;
  thêm `normalizeHeadingKey` (bỏ `#`, TrimSpace, hạ chữ thường, gộp khoảng trắng) áp cho cả key map lẫn input
  để bền với lệch hoa/thường/space; alias hoá chỗ 2 prompt diễn đạt lệch (vd "sắc thái"↔"tông điệu",
  "Điểm bán"↔"Điểm bán hàng"). Thêm test robustness.

### #3 — Toàn tầng stylestat chết câm với tiếng Việt

- **File:** `internal/stylestat/stylestat.go`.
- **Triệu chứng:** `validGram` yêu cầu **mọi rune ∈ CJK 0x4E00-0x9FFF** → gram tiếng Việt (Latin) bị loại sạch →
  `top_phrases` **RỖNG** (không phải "rác" như phán đoán ban đầu). Cộng `patternDefs` (regex Trung),
  `sentenceSplit=[。！？]`, `openingTimeRe`, `titlePrefixRe=第…章`, `shortEndingRunes` — tất cả chết.
  Hậu quả: writer mất "tấm gương tránh lặp" (`writer.md:59`), editor mất dữ liệu bắt bệnh mẫu câu
  (`editor.md:74`). **Gate thẩm mỹ mù, hệ thống tưởng "sạch bệnh" nhưng thực ra không đo được gì.**
- **Đã sửa (port sang tiếng Việt):**
  - `minePhrases`: tách `strings.Fields` → n-gram **TỪ 2-4** (bỏ rune 3-6); `validGram` lọc gram chứa
    số/dấu câu/ký hiệu thay vì lọc dải CJK; `wordEdgeStop` (~55 hư từ Việt), `stopwordWords` loại tên riêng.
  - `patternDefs`: 4 lớp regex câu sáo AI tiếng Việt (`không phải…mà là`, `(một|vài|mấy) nhịp thở`,
    `tựa như|như thể|dường như`, `im lặng|không nói gì`), hạt giống từ `anti-ai-tone.md` + `fatigue_words`.
  - `sentenceSplit`: `[.!?。！？\n]+`. `openingTimeRe`: từ thời gian Việt. `titlePrefixRe`: `^#{0,2}\s*chương\s*\d+`.
  - `shortEndingRunes` → `shortEndingWords=10` (đếm từ). Field Go `MedianRunes`→`MedianWords`, **JSON key giữ nguyên `median_runes`**.
  - **JSON key toàn struct giữ 100%** → không vỡ downstream.

### #4 — "Đếm từ" thực chất đếm ký tự + ngưỡng calib cho Hán tự

- **File:** `internal/domain/chapter.go:31`, `internal/rules/checker.go`, `assets/rules/default.md`.
- **Triệu chứng:** `WordCount = utf8.RuneCountInString` đếm **rune**. Ngưỡng `chapter_words` là số Hán tự.
  Rune tiếng Việt ≠ Hán tự → nhãn "số từ" sai + ngưỡng vô nghĩa cho tiếng Việt.
- **Điều tra blast radius:** `domain.WordCount` thực chất **dead code** (không caller ngoài test). Việc đếm rune
  thật diễn ra inline ở `store/drafts.go:77` → feed `progress.TotalWordCount`, TUI "Số từ", `diag`, JSON output.
- **Đã sửa (phương án an toàn — thêm hàm riêng, không đổi ngữ nghĩa toàn cục):**
  - Thêm `rules.CountWords(text)` = `len(strings.Fields(text))`, **chỉ dùng cho rule `chapter_words`** tại
    `commit_chapter.go:checkRules`. Không đụng rune-count dùng cho hiển thị/tiến độ/thống kê.
  - `chapter_words` severity **Error → Warning luôn** (bỏ nhánh escalate ≥20%) → xoá rủi ro chặn cứng/vòng rewrite.
    `forbidden_chars`/`forbidden_phrases` vẫn Error.
  - `default.md`: `chapter_words: 3000-6000` (từ thật), khớp `rules.md.example` + `loader.go` gốc dự án.
  - `editor.md`: bỏ nhánh "severity=error → rewrite" cho chapter_words.
- **Đính chính:** không có hard-block ở code; `2500-6000` là edit local sai hướng, đã đưa về 3000-6000.

### #5 — Rò rỉ chữ Hán nhỏ (nhiễu tool schema / file xuất)

- `save_review.go` — `审阅/審阅` trong `Description()`/`Label()`/schema tool editor → **gửi LLM**. Đã dịch "rà soát".
- `host/exp/txt.go:122-131` — header xuất `第 %d 卷/章` **ghi chữ Hán vào file .txt novel xuất ra**. Đổi "Tập %d"/"Chương %d".
- `store/session.go:215-280` — placeholder nén `第N章正文/%d字/见 chapters/`. Đổi Việt. Xác nhận `diag/redact.go:116`
  chỉ so tiền tố `[session_compact:`, không parse nội dung → an toàn.
- `editor.md:127` — ví dụ ngoặc góc Trung `「」` → `"…"`.

### #6 — Test fixture còn tiếng Trung (làm suite đỏ sau khi port #3)

- 5 test nạp ngữ liệu Trung, fail sau khi stylestat port sang Việt: `TestComputePatterns`,
  `TestComputeTopPhrasesWithStopwords`, `TestComputeEndingAndOpening`, `TestComputeTitleFormats`,
  `TestContextToolInjectsStyleStats`.
- **Đã sửa:** thay fixture sang tiếng Việt khớp detector mới, tính lại expected. Không đổi hằng số trong
  `stylestat.go` để ép test. Xác nhận detector không sai — 5 test xanh chỉ nhờ sửa fixture.

---

## 3. Danh sách file thay đổi (đã verify trên đĩa)

**Mã nguồn runtime:**

| File | Bug | Ghi chú |
|---|---|---|
| `internal/agents/ctxpack/restore.go` | #1 | 4 prompt nén → Việt |
| `internal/tools/premise_structure.go` | #2 | heading Việt + normalize match |
| `internal/stylestat/stylestat.go` | #3 | port toàn detector |
| `internal/rules/checker.go` | #4 | +`CountWords`, Error→Warning |
| `internal/rules/types.go` | #4 | bảng severity + comment threshold |
| `internal/tools/commit_chapter.go` | #4 | `checkRules` dùng `CountWords` |
| `internal/rules/loader.go` | #4 | doc string embedded |
| `internal/tools/save_review.go` | #5 | xoá `审阅` |
| `internal/host/exp/txt.go` | #5 | header xuất Việt |
| `internal/store/session.go` | #5 | placeholder nén Việt |
| `assets/prompts/editor.md` | #4,#5 | bỏ nhánh error + ngoặc Việt |
| `assets/rules/default.md` | #4 | `chapter_words: 3000-6000` |
| `rules.md.example` | #4 | đồng bộ severity |

**Test (fixture Việt hoá):** `premise_structure_test.go`, `novel_context_test.go`, `stylestat_test.go`,
`checker_test.go`, `txt_test.go`, `exporter_test.go`.

> Lưu ý: một số file trên đã ở trạng thái `M` từ trước phiên (WIP Việt-hoá). Diffstat tổng hợp cả thay đổi cũ
> lẫn mới; phần thuộc 6 task là các thay đổi hành vi runtime mô tả ở §2.

---

## 4. Bằng chứng verify

```
$ go build ./...            → exit 0
$ go test ./...             → toàn bộ ok, trừ internal/notify (pre-existing, §6)
$ grep -oP '[\x{4e00}-\x{9fff}]' <8 file mục tiêu> | wc -l   → 0 mỗi file
$ git stash list           → (rỗng)
$ grep chapter_words assets/rules/default.md  → chapter_words: 3000-6000
```

Trích `checker.go:99-133` (đã đọc trực tiếp) xác nhận severity luôn `SeverityWarning`, `CountWords` tồn tại.

---

## 5. Đánh giá chất lượng chung

- **Kiến trúc engine:** chín, tài sản domain-agnostic quý (ctxpack/resume/failover/TUI/store domain).
  Kết luận giữ nguyên: **refactor + tái định hướng, KHÔNG rewrite.**
- **Nguyên nhân gốc chất lượng tiếng Việt:** không phải model yếu (default = gemini-2.5-flash), mà là
  **4 ổ bug Việt-hoá sót đầu độc chính ngữ cảnh/gate** mà cả model tốt cũng phải đọc. Sau §2, các ổ này đã bịt.
- **Gate chất lượng:** review CÓ gate thật (`save_review.go:82` `evaluateScorecardGate` → `SetPendingRewrites`
  → `FlowRewriting/Polishing`), không chỉ ghi log. Sau khi stylestat sống lại (#3), tầng thẩm mỹ mới thực sự
  có dữ liệu để editor bắt bệnh.

---

## 6. Vấn đề tồn đọng & khuyến nghị

**Đã xử lý (đợt "triển khai toàn bộ"):**

1. ✅ **`internal/host/exp/txt.go:62` `chapterHeaderRe`** — đổi sang song ngữ
   `^#+\s+(?:第.+?章|[Cc]hương\s*\d+)`. Nay bóc đúng cả header Việt `# Chương N` lẫn Trung `# 第N章`, không còn
   rủi ro header trùng lọt vào file .txt xuất. Verify: `go test ./internal/host/exp/...` xanh.

2. ✅ **`internal/notify` — `TestCommandChannelEnvAndStdin`** — không phải bug sản phẩm. Kênh command chạy
   `sh -c` (`notify.go:89`); test nhúng đường dẫn tạm kiểu Windows `C:\...` vào chuỗi lệnh sh → dấu `\` bị sh
   diễn giải thành escape → redirect hỏng. `LookPath("sh")` không chặn được vì git-bash cấp `sh` trên Windows.
   Đã thêm skip `runtime.GOOS == "windows"` (đúng bản chất "test UNIX shell" mà chính test tự khai). Verify:
   `go test ./internal/notify/` xanh.

3. ✅ **`scripts/check_chapter_wordcount.py`** — viết lại: đếm TỪ thật (tách khoảng trắng sau khi bỏ Markdown),
   nhận diện tiêu đề chương song ngữ (`# Chương N` / `# 第N章`), Việt hoá toàn bộ thông báo. Smoke test: chương
   Việt mẫu đếm đúng số từ, bóc đúng dòng tiêu đề.

> Sau đợt này: `go build ./...` exit 0, `go test ./...` **xanh toàn bộ, 0 fail**.

**Bài học quy trình:**

- Sự cố **git stash đồng thời** (2 subagent T2+T5 lỡ `git stash` cùng lúc, tưởng nhầm edit của agent khác là
  "phiên khác") suýt gây mất edit. Đã khôi phục, verify sạch. *Khuyến nghị:* khi fan-out nhiều agent sửa file
  trong cùng một repo, hoặc chạy **tuần tự**, hoặc dùng **worktree riêng cho mỗi agent**, và **cấm subagent chạy
  `git stash`/thao tác git phá trạng thái**.

---

## 7. Lộ trình tiếp theo (ROI giảm dần)

- **Phase 0 (song song):** nâng model role writer/editor lên gemini-2.5-pro / model mạnh tiếng Việt; bật lại
  block `roles` trong config (hiện comment → mọi role = flash). Đòn bẩy phụ, sau khi #1-#4 đã sạch.
- **Phase 2 — Human-in-loop:** tận dụng `cocreate_stage` + `evaluateScorecardGate` sẵn có; thêm review-gate mỗi
  chương; autonomous thành opt-in.
- **Phase 3 — SQLite cross-chapter:** Summaries/Characters/Cast/World trước; draft giữ `.md`. Thay backend từng
  store, không rewrite.

---

*Chi tiết phân tích các điểm mấu chốt: xem `docs/plan.md`.*
