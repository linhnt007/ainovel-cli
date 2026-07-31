# Phân tích sâu ainovel-cli — bản xác minh (đã đối chiếu mã nguồn)

> Bản này thay bản phân tích trước. Mọi mục dưới đây đã được xác minh trực tiếp
> trên mã nguồn (file:line thực). Ba đính chính lớn so với bản cũ được đánh dấu **[ĐÍNH CHÍNH]**.

---

## 1. Một chương ra đời thế nào (luồng end-to-end)

Một vòng lặp LLM Coordinator duy nhất. `agents/build.go:104` `BuildCoordinator` dựng 1 Agent (coordinator) cầm 1 tool `subagent` phân phát 4 subagent:

```
Coordinator (system=coordinator.md, MaxTurns=100_000, ToolGate=completePhaseGate)
  └─ subagent tool → dispatch:
       architect_short / architect_long  (foundation + outline)   MaxTurns 15/20
       writer   (plan→draft→check→commit) MaxTurns 30, StopAfterTools=[commit_chapter]
       editor   (review 7 chiều + arc/vol summary) MaxTurns 20
```

- Mỗi role model riêng: `models.ForRoleWithFailover("writer"/…)`. Provider failover **thực sự chuyển model**, không chỉ log (`models.go:404-440`, chỉ kích hoạt khi role khai `fallbacks`).
- Writer tự chủ hoàn toàn một chương: `novel_context` → `read_chapter` → `plan` → `draft` → `check_consistency` → `commit_chapter`. Dừng cứng ở `commit_chapter`. Không có điểm dừng cho người xen vào giữa.
- `completePhaseGate` (build.go:362) chặn phân phát khi phase=complete — chống loop vô tận.
- Writer có ContextManager riêng dựng lại mỗi lần gọi (build.go:257): cửa sổ theo model, `KeepRecentTokens=20000`, chiến lược nén `StoreSummaryCompact`, `PostSummaryHook` = `restore.Hook()` bơm lại context store sau khi nén.

## 2. Bộ nhớ/context — engine thật sự

Hai đường bơm context cho writer:

**A. Tool `novel_context`** (writer gọi chủ động) — 3 phong bì:

- `working_memory` (kế hoạch chương, outline, user_rules, directives)
- `episodic_memory` (recent_summaries, arc/vol summary, char snapshots, foreshadow, timeline, recent_cast, **style_stats**)
- `reference_pack` (anti-ai-tone, chapter-guide theo genre)

**B. ctxpack post-compact** (`ctxpack/builder.go` + `restore.go`) — khi context đầy, nén rồi `buildWriterRestoreText` bơm lại từ store (budget ~6000 token): plan→outline→snapshots→foreshadow→reviews→timeline.

Nguồn: `loadWriterStoreSummaryState` (builder.go:87) đọc ~10 sub-store mỗi lần.

## 3. Rules & kiểm tra chất lượng

`commit_chapter` gọi `checkAndBlockRules` (commit_chapter.go:365-374):

- `rules.Lint` (luôn chạy) + `rules.Check` (user structured).
- **Chỉ `SeverityError` mới chặn commit** (trả `ErrToolPrecondition`). Warning không chặn.
- `forbidden_chars` (≥1 = Error), `forbidden_phrases` (≥1 = Error), `fatigue_words` (`n>limit` = Warning), `chapter_words` (lệch ≥20% = **Error, chặn**; ngưỡng `ChapterWordsDeviationThreshold=0.20`, types.go:145).
- Tất cả dùng `strings.Count` substring, không biết ranh giới từ.
- `stylestat.Compute` mines cụm lặp → `episodic_memory.style_stats` làm gương cho writer.
- Review **CÓ gate thật** (`save_review.go:82-140`): `evaluateScorecardGate` → `SetPendingRewrites` + `SetFlow(FlowRewriting/FlowPolishing)`. Không chỉ ghi log.

---

## 4. ⛔ BUG CHẤT LƯỢNG (đã xác minh, xếp theo tác động)

Commit "Việt hoá toàn bộ repo" dịch `assets/*.md` (prompt chính + reference sạch) nhưng **bỏ sót logic runtime trong Go**. Bốn ổ nằm đúng đường chất lượng.

### #1 — Prompt nén context của Writer CÒN NGUYÊN TIẾNG TRUNG — `ctxpack/restore.go:18-95` ✅ XÁC NHẬN (chết người nhất)

4 const, cả 4 wired vào ContextManager của writer tại `build.go:276-282`:

| Biến                        | Dòng  | Đẩy vào LLM?                                    |
| --------------------------- | ----- | ----------------------------------------------- |
| `WriterSummarySystemPrompt` | 18-23 | CÓ (`你是一个小说创作上下文摘要助手`)           |
| `WriterSummaryPrompt`       | 25-59 | CÓ (`## 当前进度 / 角色即时状态 / 风格与节奏`…) |
| `WriterUpdateSummaryPrompt` | 61-80 | CÓ                                              |
| `WriterTurnPrefixPrompt`    | 82-95 | CÓ                                              |

**Cơ chế hại:** chương dài / hội thoại writer vượt `KeepRecentTokens=20000` → `FullSummary` nén phần cũ bằng 4 prompt Trung → checkpoint tóm tắt lưu trong context writer mang khung + chỉ thị + "style anchor" mô tả bằng tiếng Trung → writer đọc tiếp từ scaffold Trung → **drift ngôn ngữ/giọng, mất giọng Việt giữa chương dài, rủi ro rớt sang chữ Hán**. Model càng yếu càng vỡ. `PostSummaryHook` tái bơm restore-pack Việt từ store nên giảm nhẹ, nhưng bản summary Trung vẫn nằm trong ngữ cảnh. **Đây là rò rỉ chất lượng lớn nhất.**

> **[ĐÍNH CHÍNH]** bản cũ nói "builder.go còn tiếng Trung" — SAI. `builder.go` đã Việt hoá sạch (heading "Tiến độ hiện tại", "Kế hoạch chương", "Ảnh chụp nhân vật"…). Ổ Trung chỉ nằm ở `restore.go`.

### #2 — Kiểm tra foundation HỎNG với tiếng Việt — `tools/premise_structure.go:9-132` ✅ XÁC NHẬN (bug kép)

- `premiseHeadingAliases` (9-27) + `requiredPremiseHeadings` (98-132): **toàn heading Trung** (`题材定位/核心冲突/主角目标/终局方向/写作禁区`…).
- `canonicalPremiseHeading` (62-72): chỉ nhận dòng `#…`, `TrimLeft("#")` rồi tra map **exact** — không normalize, không substring.
- Architect prompt (`assets/prompts/architect-short.md:58-67`): ra lệnh viết heading **tiếng Việt** (`## Xung đột cốt lõi`, `## Mục tiêu nhân vật chính`, `## Hướng kết cục`…).

**Hậu quả:** heading Việt architect sinh **không bao giờ khớp** map Trung → mọi section bị bỏ → `sections` rỗng → `template_ready=false` **luôn**, `missing` = toàn bộ danh sách (bằng tiếng Trung). Cơ chế kiểm tra đầy đủ premise **vô hiệu hoàn toàn** — nền truyện thiếu mảng thì hệ thống không thấy. Bug kép: mismatch matching **+** rò tiếng Trung (`missing` Trung chảy vào `Foundation` envelope → LLM, qua `novel_context_builders.go:206, 680`).

### #3 — Toàn bộ tầng stylestat CHẾT với tiếng Việt — `internal/stylestat/stylestat.go` ✅ XÁC NHẬN (nhưng cơ chế khác bản cũ)

| Điểm                      | Dòng      | Sự thật                                                                   |
| ------------------------- | --------- | ------------------------------------------------------------------------- |
| `validGram`               | 198-208   | `if r < 0x4E00 \|\| r > 0x9FFF return false` — **chỉ nhận Hán tự thuần**  |
| `minePhrases` n-gram      | 137-193   | cửa sổ rune 3-6, nhưng lọc qua `validGram`                                |
| `patternDefs` (dò AI tic) | 80-88     | regex thuần Trung (`不是…而是`, `像一/仿佛/如同/宛如`, `沉默了/没有说话`) |
| `sentenceSplit`           | 91        | `[。！？\n]+` — chỉ tách theo dấu câu Trung                               |
| `openingTimeRe`           | 92        | `夜/清晨/黎明/晨光`… — dò mở chương bằng mốc thời gian                    |
| `titlePrefixRe`           | 93        | `^#{0,2}\s*第…章` — tiền tố tiêu đề Trung                                 |
| `shortEndingRunes`        | 96-97,288 | ngưỡng kết ngắn = 30 rune, calib CJK                                      |

**[ĐÍNH CHÍNH]** bản cũ nói "n-gram cắt giữa từ → 'ột chú' = rác bơm cho writer". SAI. `validGram` yêu cầu **mọi rune ∈ CJK** → ký tự Latin Việt đều `< 0x4E00` → gram Việt bị loại sạch → `top_phrases` **RỖNG (câm lặng), không phải rác**. Tương tự `patterns`, `opening_time_rate`, `titleFormats` đều rỗng/0 cho văn Việt.

**Hậu quả thật:** với tiếng Việt, `style_stats` chỉ còn `chapters` + `ending` + `repeated_sentences` (so chuỗi thuần, còn chạy). Cả `top_phrases` **và** `patterns` luôn rỗng. Writer (`writer.md:59`) được hứa "tấm gương phản chiếu cụm quen miệng" — **mất trắng**. Editor (`editor.md:74`) được lệnh trích `style_stats.patterns` bắt bệnh lặp mẫu câu — **nguồn dữ liệu không tồn tại**. Cổng chất lượng thẩm mỹ câm lặng: hệ thống tưởng "sạch bệnh" nhưng thực ra chỉ mù.

### #4 — Đếm "từ" = đếm ký tự, ngưỡng calib cho Hán tự — `domain/chapter.go:31` + `checker.go:99-134` ✅ XÁC NHẬN

- `WordCount = utf8.RuneCountInString(content)` — đếm **rune**, không đếm từ.
- `chapter_words: 2500-6000` (default.md:15) = số **Hán tự** (1 hán tự ≈ 1 từ, calib cho tiếng Trung).
- Lệch ≥20% → **Error → chặn commit**.

**Hậu quả:** rune tiếng Việt (từ nhiều rune + dấu cách + dấu câu) không ánh xạ sang ngưỡng Hán tự. Nếu writer nhắm "số từ" thật → rune vượt xa 16000 → deviation lớn → **Error chặn commit → rủi ro vòng rewrite vô ích**. Nếu writer nhắm rune → chương Việt quá ngắn (~2500 từ). Nhãn "số từ" gây hiểu nhầm, ngưỡng vô nghĩa cho tiếng Việt. Cần soi thêm: writer.md ra lệnh mục tiêu độ dài theo đơn vị gì (từ/chữ) — nếu lệch với đơn vị đo thì đây là **hard-block tiềm tàng**.

- `scripts/check_chapter_wordcount.py`: regex `[一-鿿]` → **trả 0 cho mọi chương Việt**, tách thân bằng ký tự `章`. Hoàn toàn vô dụng. Lưu ý: **script standalone, KHÔNG nằm trong luồng Go** `commit_chapter` — CI/tool phụ, ưu tiên thấp.

### #5 — Nghi phạm nền: MODEL — **[ĐÍNH CHÍNH LỚN]**

Bản cũ giả định "writer chạy mistral/llamacpp local → văn Việt kém là kỳ vọng". **SAI theo config.**

- `config.example.jsonc:6-7`: default `provider="openrouter"`, `model="google/gemini-2.5-flash"`.
- Block `roles` (63-70) **bị comment** → writer/editor/architect/coordinator **đều = gemini-2.5-flash** trừ khi user tự khai (`models.go:110-134`, role không cấu hình → fallback `ms.Default`).
- Không có default local mistral/ollama nào được gán (`models_generated.go` chỉ là catalog giá).

**Kết luận mới:** model mặc định là **gemini-2.5-flash** — tier "flash", tiếng Việt khá nhưng không phải thảm hoạ như mô tả cũ. Vẫn là đòn bẩy chất lượng (nâng lên gemini-2.5-pro / model mạnh tiếng Việt cho writer/editor), nhưng **KHÔNG phải nguyên nhân gốc** như bản cũ nghĩ. Nguyên nhân gốc = 4 bug Việt hoá sót (#1-#4) đầu độc chính ngữ cảnh mà model tốt cũng phải đọc.

### Rò rỉ tiếng Trung nhỏ (mới phát hiện — nhiễu, không chết người)

| File:line                                   | Ghi chú                                                                                        | Mức        |
| ------------------------------------------- | ---------------------------------------------------------------------------------------------- | ---------- |
| `tools/save_review.go:14,25,29,50,51,57,58` | `审阅/審阅` lẫn trong `Description()`/`Label()`/schema tool editor → gửi LLM                   | THẤP       |
| `store/session.go:215-280`                  | placeholder nén `第N章正文`, `%d字`, `见 chapters/` → có thể lọt vào ngữ cảnh đã nén           | THẤP       |
| `host/exp/txt.go:122,129,131`               | header xuất `第 %d 卷/章` → **ghi chữ Hán vào file .txt novel xuất ra** (lỗi output thấy được) | THẤP-TRUNG |
| `assets/prompts/editor.md:127`              | ví dụ dùng ngoặc góc Trung `「……」` thay vì `"…"`/`«…»`                                        | nit        |

Chữ Trung runtime còn lại (`commit_chapter.go`, `edit_chapter.go` comment; `splitter.go` regex import truyện Trung — **cố ý, hợp lệ**) không ảnh hưởng output. `_test.go`, `testdata/`, `docs/` bỏ qua.

---

## 5. Lưu trữ & SQLite

Hiện: 15 sub-store JSON qua `store/io.go` (atomic temp-write). Không có interface repository chung — mỗi store gọi `io.ReadFile/WriteFile` + `json.Marshal` trực tiếp.

`novel_context` + `ctxpack` mỗi chương load lặp: `LoadRecentSummaries`, `LoadLatestSnapshots`, `LoadActiveForeshadow`, `LoadRecentTimeline`, `LoadArcSummaries`, `LoadAllVolumeSummaries`, `LocateChapter`… — đúng loại truy vấn quan hệ sqlite tăng tốc + làm giàu ("phục bút planted chưa resolve", "sự kiện timeline trong arc Y"). Ứng viên sqlite: Summaries, Characters, Cast, World (foreshadow/timeline/relationship), Progress. Draft chương giữ `.md`.

Đây là thay backend từng store, **không phải rewrite**.

---

## 6. VERDICT

**Refactor + tái định hướng. KHÔNG rewrite.** Engine chín, tài sản quý domain-agnostic (ctxpack/resume/failover/TUI/store domain). Nút thắt chất lượng = **4 ổ bug Việt hoá sót** (#1-#4) — chúng đầu độc ngữ cảnh/gate mà cả model tốt cũng phải ăn. Model default **không** yếu như tưởng (gemini-2.5-flash), nên sửa bug là đòn bẩy lớn hơn đổi model. Sửa tại chỗ rẻ hơn rewrite nhiều, và rewrite không tự sửa mấy thứ này.

### Lộ trình cập nhật (ROI giảm dần)

**Phase 1 — Vá rò rỉ Việt hoá (cao nhất, ~1-2 ngày, ít code):**

1. **[#1]** Dịch 4 prompt `restore.go:18-95` sang Việt + format heading Việt. **Ưu tiên tuyệt đối** — đánh trực tiếp độ ổn định ngôn ngữ chương dài.
2. **[#2]** Đổi `premise_structure.go` aliases/headings sang Việt, đồng bộ **đúng chữ** với `architect-short.md`/`architect-long.md`. Cân nhắc match normalize (lower + trim) thay vì exact để bền hơn.
3. **[#3]** Port `stylestat.go` sang tiếng Việt: token hoá theo khoảng trắng (n-gram **từ** 2-4, bỏ `validGram` CJK-only); viết lại `patternDefs` cho cú pháp Việt; `sentenceSplit` thêm `.!?`; `openingTimeRe`/`titlePrefixRe` sang mốc thời gian Việt + "Chương N"; đổi `shortEndingRunes` sang đếm từ. Đây là làm sống lại cả tầng gate thẩm mỹ.
4. **[#4]** Đổi ngữ nghĩa `WordCount` sang **đếm từ thật** (split khoảng trắng) HOẶC hạ `chapter_words` từ Error→Warning (bỏ ép nhồi/rủi ro loop). Kiểm tra đơn vị mục tiêu độ dài trong `writer.md` cho khớp. Đổi nhãn.
5. Rò rỉ nhỏ: `save_review.go` schema, `exp/txt.go` header xuất file, `session.go` placeholder, `editor.md:127` ngoặc.
6. Re-derive `fatigue_words`/`anti-ai-tone` từ output Việt thật (default.md hiện đã Việt, nhưng ngưỡng nên calib lại từ vòng chạy Việt).

**Phase 0 (song song) — nâng model role writer/editor:** default = gemini-2.5-flash. Test gemini-2.5-pro / model mạnh tiếng Việt vs flash cho writer+editor. Bật lại block `roles`. Đòn bẩy phụ (sau khi #1-#4 sạch — nếu không, model tốt vẫn ăn ngữ cảnh độc).

**Phase 2 — Human-in-loop:** `cocreate_stage` đã có khung; thêm review-gate mỗi chương (tận dụng `evaluateScorecardGate` sẵn có); autonomous thành opt-in.

**Phase 3 — SQLite cross-chapter:** Summaries/Characters/Cast/World trước; draft giữ `.md`.

### Thứ tự thực thi khuyến nghị

`#1` (restore.go) → `#4` (wordcount, rủi ro hard-block) → `#2` (premise) → `#3` (stylestat) → rò rỉ nhỏ → Phase 0 model. Lý do: #1 hại rộng nhất mỗi chương dài; #4 có thể đang chặn cứng commit; #2/#3 làm sống lại gate nhưng không chặn luồng.
