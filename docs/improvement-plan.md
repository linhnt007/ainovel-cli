# Kế hoạch sửa chữa & cải thiện flow cocreate + auto sáng tác

> **Cách dùng tài liệu này:** Mỗi Part bên dưới là một gói việc **tự chứa** (self-contained) — subagent chỉ cần đọc đúng Part của mình là đủ ngữ cảnh, không cần đọc phần khác. Các Part trong **cùng một Wave** không chạm chung bất kỳ file nào → chạy song song an toàn. Wave sau chỉ bắt đầu khi Wave trước đã merge và `go test ./...` xanh.

---

## Quy tắc chung (áp dụng cho MỌI Part)

1. **Không đụng file ngoài danh sách "Files sở hữu"** của Part mình. Nếu phát hiện buộc phải sửa file ngoài danh sách → DỪNG, báo cáo lại, không tự ý sửa.
2. Mọi thay đổi schema (JSON lưu trên đĩa) phải **backward-compatible**: trường mới là optional, thiếu trường = hành vi cũ. Không migrate dữ liệu im lặng.
3. Mọi gate/vòng lặp mới đều phải có **trần số vòng** (bài học từ sự cố ch204..347 ghi trong comment codebase).
4. Comment code viết tiếng Việt, theo phong cách comment sẵn có của repo (giải thích *tại sao*, không giải thích *cái gì*).
5. Sau khi xong: chạy `go test ./...` (Part Python thì chạy thử script). Báo cáo: file đã đổi + tóm tắt diff + kết quả test.
6. Con số dòng (`file:line`) trong tài liệu này là ước lượng tại thời điểm viết — luôn xác minh lại bằng nội dung thực tế trước khi sửa.

---

# WAVE 1 — Bug xác nhận, các Part hoàn toàn độc lập

## Part A — Sửa bug đơn vị đếm wordcount (rune vs từ)

**Mức độ: P0 — bug đã xác nhận, đang gây hại.**

### Bối cảnh & nguyên nhân
Repo Việt hoá từ bản gốc tiếng Trung. Bản Trung đếm "chữ" bằng rune (1 Hán tự = 1 rune) — đúng với tiếng Trung. Bản Việt định nghĩa lại `chapter_words: 3000-6000` là **từ tách theo khoảng trắng** (xem `assets/rules/default.md:20` và hàm `CountWords` tại `internal/rules/checker.go:135-139`), nhưng **nguồn số vẫn đếm rune**:

- `internal/store/drafts.go:77` — `LoadChapterContent` trả `utf8.RuneCountInString(draft)`.
- `internal/rules/checker.go:25` — fallback khi `wordCount < 0` cũng dùng `utf8.RuneCountInString`.
- Comment `internal/rules/checker.go:18` ghi "đếm theo rune" — mâu thuẫn với `CountWords` ngay trong cùng file.

Tiếng Việt: 1 từ ≈ 4-5 rune. Hậu quả: chương 3000 từ ≈ 14k rune → checker báo "quá dài" sai; `MarkChapterComplete` (`internal/store/progress.go:144`) lưu rune vào thống kê số từ → tổng tiến độ, tóm tắt trạng thái truyện, phân bổ nhịp của architect đều phồng 4-5×.

### Thay đổi
1. `internal/store/drafts.go` — `LoadChapterContent`: trả `rules.CountWords(draft)` thay cho `utf8.RuneCountInString(draft)`. Kiểm tra import cycle: nếu `store` không được import `rules` (cycle), copy logic `strings.Fields` trực tiếp (hàm nhỏ 3 dòng) kèm comment tham chiếu `rules.CountWords`.
2. `internal/rules/checker.go:25` — fallback đổi thành `wordCount = CountWords(text)`.
3. `internal/rules/checker.go:18` — sửa comment: "số từ của chương (đếm theo từ tách khoảng trắng, xem CountWords)".
4. **Không** sửa caller (`commit_chapter.go`, `check_consistency.go`, `progress.go`) — chúng tự đúng khi nguồn đổi. Không migrate `progress.json` cũ.
5. Thêm ghi chú vào cuối file này hoặc CHANGELOG (nếu repo có): sách đang viết dở sẽ thấy tổng số từ giảm 4-5× — là số đúng, không phải mất dữ liệu.

### Files sở hữu (KHÔNG đụng file khác)
- `internal/store/drafts.go`
- `internal/rules/checker.go`
- `internal/rules/checker_test.go`
- `internal/store/drafts_test.go` (tạo mới nếu chưa có)

### Acceptance
- Test mới: văn bản tiếng Việt đúng 3500 từ (generate bằng vòng lặp trong test) → `LoadChapterContent` trả 3500; `Check` với range 3000-6000 → không violation `chapter_words`.
- Test fallback: `Check(text, -1, ...)` tự đếm ra số từ, không phải số rune (fixture: câu tiếng Việt biết trước số từ ≠ số rune).
- Sửa test cũ nếu chúng đang assert theo rune (xem `checker_test.go:199-207` — test "auto wordCount" hiện expect 2500 theo ngữ nghĩa nào, cập nhật cho khớp ngữ nghĩa từ).
- `go test ./internal/rules/... ./internal/store/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Tác động lan:** rules checker, commit gate, progress stats, stage-cocreate summary, pacing architect — tất cả tự nhận số đúng, không cần sửa gì thêm.
- **Hiệu quả:** writer hết bị báo "quá dài" oan; thống kê tin được.
- **Mặt trái:** user có sách dở dang thấy số sụt (đúng); user nào đã tự chỉnh range theo rune sẽ vỡ rule — chấp nhận, ghi chú rõ.

---

## Part B — Việt hoá `rules.md.example` + dọn test data tiếng Trung

**Mức độ: P0 — template user copy đang toàn tiếng Trung.**

### Bối cảnh & nguyên nhân
Đợt Việt hoá repo sót file. `rules.md.example` (root repo) là template user copy để tự cấu hình rules, nhưng còn nguyên nội dung Trung: `forbidden_phrases` chứa `某种程度上`, `值得注意的是`; `fatigue_words` chứa `不禁/竟然/仿佛`; `forbidden_chars` cấm `——`. Trong khi bản chuẩn `assets/rules/default.md` đã Việt hoá đầy đủ (phrases: "theo một nghĩa nào đó", "đáng chú ý là"...; 16 fatigue words Việt với ngưỡng 1-3; cố ý KHÔNG cấm `——` — xem giải thích tại `default.md:26-27`). User copy example → cấm chuỗi Hán không bao giờ xuất hiện + mất hết ràng buộc tiếng Việt.

Ngoài ra `internal/diag/rules_quality_test.go:35-37` còn test data tiếng Trung (`首战取胜`, `确认搭档关系`, `揭开真相一角`) — vô hại nhưng là dấu vết chưa dọn.

### Thay đổi
1. `rules.md.example`: viết lại đồng bộ nội dung với `assets/rules/default.md` (đọc file đó làm nguồn chân lý). Giữ cấu trúc comment hướng dẫn của example (mục đích file là dạy user format). Thống nhất chuyện `——`: không cấm mặc định, thêm dòng comment "bỏ comment dòng này nếu muốn cấm gạch ngang dài" kèm ví dụ.
2. `internal/diag/rules_quality_test.go`: thay các chuỗi tiếng Trung trong test data bằng tiếng Việt tương đương (VD "thắng trận đầu", "xác nhận quan hệ đồng đội", "hé lộ một góc sự thật"). Chỉ đổi data, không đổi logic test.

### Files sở hữu
- `rules.md.example`
- `internal/diag/rules_quality_test.go`

### Acceptance
- `rules.md.example` không còn ký tự CJK (kiểm bằng regex `[\p{Han}]`).
- Nội dung rule (phrases/fatigue/ngưỡng/range) khớp `assets/rules/default.md`.
- `go test ./internal/diag/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** user customize rules không tự vô hiệu hoá hệ thống rules.
- **Mặt trái:** không có. Zero code thay đổi.

---

## Part C — `truncateJSONToTokens` sinh JSON hỏng

**Mức độ: P2 — bug im lặng, model nhận context rác không ai biết.**

### Bối cảnh & nguyên nhân
`internal/agents/ctxpack/restore.go:184-194` — `truncateJSONToTokens` cắt chuỗi JSON theo **byte thô** để lọt ngân sách token. JSON bị cắt giữa chừng = không hợp lệ. Chuỗi này được bơm vào context model sau compaction (WriterRestorePack, budget 6000 token, xem `restore.go:100-179`) → model nhận JSON cụt, âm thầm suy giảm chất lượng khôi phục ngữ cảnh chương.

### Thay đổi
1. Viết lại `truncateJSONToTokens` (hoặc thay bằng hàm mới, giữ chữ ký): unmarshal về `map[string]any` / `[]any`, **drop từng phần tử theo thứ tự ưu tiên** (mảng cắt từ cuối, key ít quan trọng drop trước — nếu không có thông tin ưu tiên thì drop key theo thứ tự ngược alphabet hoặc theo kích thước lớn nhất trước), marshal lại, lặp tới khi ước lượng token lọt budget. Kết quả **luôn là JSON hợp lệ**.
2. Nếu input không phải JSON hợp lệ ngay từ đầu (phòng thủ): fallback cắt theo rune ở ranh giới ký tự + append `…` — vẫn tốt hơn cắt byte giữa multi-byte UTF-8.
3. Giữ nguyên cách ước lượng token hiện có trong file (tìm hàm ước lượng sẵn dùng trong package, không tự chế công thức mới).

### Files sở hữu
- `internal/agents/ctxpack/restore.go`
- `internal/agents/ctxpack/restore_test.go` (tạo mới nếu chưa có; nếu đã có, chỉ thêm test)

### Acceptance
- Test: JSON lồng nhau ~10KB, budget nhỏ → output `json.Valid() == true` và ngắn hơn input.
- Test: budget đủ lớn → output nguyên vẹn (không đổi).
- Test: input không phải JSON → fallback không panic, không cắt giữa ký tự UTF-8.
- `go test ./internal/agents/ctxpack/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** loại bug không thể report (không ai thấy context sau compaction).
- **Mặt trái:** không.

---

## Part D — Đồng bộ `scripts/check_chapter_wordcount.py`

**Mức độ: P2 — công cụ dev phát thông điệp ngược triết lý.**

### Bối cảnh & nguyên nhân
Script hiện **fail cứng** khi chương < 3000 (khoảng dòng 74) và in khuyến nghị "Thêm mô tả chi tiết… Mở rộng nội tâm" (khoảng dòng 138-142) — tức là dạy **padding cho đủ số từ**, ngược hẳn `writer.md:74` ("Số từ phục vụ nhịp điệu, không phải để viết thêm cho đủ") và toàn bộ triết lý chống văn AI của hệ thống. Ngoài ra cần thống nhất đơn vị đếm là **từ tách khoảng trắng** (khớp Part A — nhưng Part này độc lập, chỉ cần script tự đếm đúng từ).

### Thay đổi
1. Đơn vị đếm: tách khoảng trắng (`len(text.split())`), bỏ đếm ký tự nếu đang có.
2. Bỏ fail cứng dưới ngưỡng → chuyển thành **báo cáo phân loại**: mỗi chương in số từ + nhãn `DƯỚI NGƯỠNG / TRONG NGƯỠNG / VƯỢT NGƯỠNG` theo range 3000-6000 (cho phép override qua flag `--min/--max`). Exit code 0 trừ khi có lỗi IO/parse thật.
3. Xoá toàn bộ khuyến nghị "viết thêm/mở rộng" — thay bằng 1 dòng trung tính: "Số từ ngoài ngưỡng: xem lại nhịp chương, không thêm chữ chỉ để đạt ngưỡng."

### Files sở hữu
- `scripts/check_chapter_wordcount.py`

### Acceptance
- Chạy `python scripts/check_chapter_wordcount.py --help` không lỗi.
- Chạy trên thư mục giả có 2 file md (1 ngắn 1 đủ) → in phân loại đúng, exit 0.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** công cụ cùng chiều triết lý; hết cổ vũ padding.
- **Mặt trái:** ai đang dùng script trong CI như gate cứng sẽ mất gate — chủ đích, ghi trong docstring script.

---

## Part E — Test parser cocreate (chỉ THÊM file test, không sửa source)

**Mức độ: P3 — trả nợ test cho parser nhiều nhánh fallback đang zero coverage.**

### Bối cảnh & nguyên nhân
Parser giao thức XML 4 thẻ của cocreate (`internal/host/cocreate.go`) có nhiều nhánh fallback thông minh nhưng **không có test nào ghim hành vi**:
- `splitCoCreateMarkers` (`cocreate.go:234`) — tách `<reply>/<draft>/<ready>/<suggestions>`.
- `extractTagContent` (`cocreate.go:249`) — 3 nhánh dự phòng: (1) có mở không đóng → cắt tới thẻ mở kế; (2) không mở có đóng (typo như `<uggestions>`) → lấy từ thẻ đóng hoàn chỉnh gần nhất; (3) reply không thẻ mở, kết bằng `</reply>`.
- `parseSuggestions` (`cocreate.go:292`) — max 3 dòng, bỏ prefix `- / * / 1.`, bỏ dòng dạng XML.
- `parseCoCreateResponse` (`cocreate.go:211`) — reply rỗng → cả đoạn thành reply; raw rỗng → error "cocreate empty response".
- `extractReplyPreview` (`cocreate.go:349`) — preview streaming.

Regression rình khi ai đó "dọn code" các nhánh này.

### Thay đổi
Tạo **file test mới** `internal/host/cocreate_parse_test.go` (nếu tên trùng file có sẵn thì chọn tên khác, tuyệt đối không sửa file test có sẵn). Table-test phủ:
1. Happy path đủ 4 thẻ.
2. Thiếu thẻ đóng `</draft>` (mô phỏng output bị cắt bởi max_tokens) → draft lấy tới thẻ kế/hết chuỗi.
3. Typo thẻ mở (`<uggestions>`) → suggestions vẫn parse được qua nhánh thẻ-đóng.
4. Reply mở đầu natural text + `</reply>` cuối.
5. Model không tuân thủ hoàn toàn (natural text trần) → cả đoạn thành reply, `Ready=false`, `Prompt=""`.
6. `<ready>yes</ready>` và `<ready>TRUE</ready>` → true; `<ready>ready</ready>` → false.
7. Suggestions: 5 dòng → giữ 3; prefix `1. `/`- `/`* ` bị bóc; dòng `<xxx>` bị bỏ; dòng < 2 ký tự bị bỏ.
8. Raw rỗng → error.

**KHÔNG sửa bất kỳ dòng nào trong `cocreate.go`** — nếu test phát hiện hành vi lạ, ghi nhận trong báo cáo, giữ test khớp hành vi hiện tại (pin behavior).

### Files sở hữu
- `internal/host/cocreate_parse_test.go` (mới)

### Acceptance
- `go test ./internal/host/ -run CoCreate` xanh, coverage các hàm parse > 85%.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** Wave 2 Part K (sửa cocreate) có lưới an toàn.
- **Mặt trái:** không.

---

## Part F — Foreshadow aging recall (chống livelock kết thúc sách)

**Mức độ: P2 — livelock thật, chính comment codebase mô tả.**

### Bối cảnh & nguyên nhân
Recall foreshadow "treo lâu" trong `internal/tools/novel_context.go` (hàm `selectStoryThreads`, quanh `novel_context.go:879,886` / `novel_context_builders.go:666-723`) có 3 giới hạn cứng gây hại:
1. Chỉ kích hoạt khi **≥6 foreshadow active** — truyện ít foreshadow không bao giờ được nhắc theo tuổi.
2. Tuổi hard-code **30 chương** (`foreshadowAgingChapters`) — vô nghĩa với sách 40 chương.
3. Cap 5 kết quả — sách dài nhiều foreshadow cũ vẫn bị quên.

Trong khi đó chế độ layered đòi `active_foreshadow == 0` mới cho kết thúc sách (`internal/tools/commit_chapter.go:647-660`) → foreshadow bị quên = writer không bao giờ thu hồi = sách không thể hoàn thành (livelock mà comment `applyCompletion` tại `commit_chapter.go:591-613` mô tả).

### Thay đổi (tất cả trong builder recall, KHÔNG đụng commit_chapter)
1. Bỏ ngưỡng ≥6: recall theo tuổi chạy với mọi số lượng foreshadow active.
2. Tuổi kích hoạt theo scale: `agingThreshold = max(10, totalChapters/8)` với `totalChapters` lấy từ Progress/Outline sẵn có trong context build state; nếu không xác định được tổng chương → giữ 30 như cũ.
3. Giai đoạn hoàn kết: nếu xác định được truyện đang ở vùng kết (đã có tín hiệu sẵn trong state — tìm compass/phase; nếu không có tín hiệu rẻ, dùng heuristic `chương hiện tại ≥ 80% tổng chương kế hoạch`): inject **toàn bộ** foreshadow active kèm tuổi từng cái, bỏ cap 5. Ngoài vùng kết giữ cap 5 như cũ.
4. Comment giải thích tại sao (livelock active_foreshadow==0, tham chiếu `applyCompletion`).

### Files sở hữu
- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`
- `internal/tools/novel_context_test.go` (chỉ thêm test)

### Acceptance
- Test: 3 foreshadow active, 1 cái tuổi 15, tổng chương 40 → được recall (trước đây: không, vì <6).
- Test: 12 foreshadow active ở chương 85/100 → tất cả xuất hiện kèm tuổi.
- Test: không xác định tổng chương → threshold 30 (hành vi cũ).
- `go test ./internal/tools/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** đóng đường livelock chặn hoàn thành sách; foreshadow được trả nợ đúng hạn.
- **Mặt trái:** thêm vài trăm token context ở giai đoạn cuối sách — chấp nhận.

---

## Part G — Stage cocreate: thêm đuôi văn bản chương gần nhất

**Mức độ: P3 — cải thiện nghề: bàn hướng đi phải đọc trang cuối.**

### Bối cảnh & nguyên nhân
Stage cocreate (tạm dừng giữa chừng để lập kế hoạch) dựng system prompt từ `buildStoryStateSummary` (`internal/host/cocreate_stage.go:14`) — chỉ gồm dữ liệu cấp cao: progress, compass, tóm tắt tập, nhân vật, foreshadow. **Không có văn bản thật.** AI bàn hướng đi tiếp theo mà chưa đọc đoạn văn gần nhất — editor thật không làm vậy. Cơ chế "đuôi 800 rune chương liền trước" đã tồn tại cho writer (`previous_tail` trong `novel_context_builders.go:455-463`) nhưng stage summary không dùng.

### Thay đổi
1. `buildStoryStateSummary`: thêm mục cuối "## Đoạn kết chương gần nhất" — load nội dung chương hoàn thành gần nhất từ store (dùng đúng store API mà builder này đang dùng cho các mục khác; tìm hàm load chapter content trong `internal/store`), lấy **đuôi ~800 rune** (cắt tại ranh giới rune, ưu tiên cắt đầu đoạn văn — tìm `\n\n` gần nhất trong phạm vi). Best-effort như các mục khác: lỗi load → bỏ qua mục này, không fail.
2. Nếu chưa có chương nào hoàn thành → không thêm mục.

### Files sở hữu
- `internal/host/cocreate_stage.go`
- `internal/host/cocreate_stage_test.go` (chỉ thêm test)

### Acceptance
- Test: store có chương hoàn thành → summary chứa mục đuôi văn bản, độ dài ≤ ~800 rune + tiêu đề.
- Test: store rỗng → summary không có mục này, không panic.
- `go test ./internal/host/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** kế hoạch giai đoạn nối liền mạch văn thay vì chỉ metadata.
- **Mặt trái:** +800 rune input mỗi lượt stage cocreate — không đáng kể.

---

# WAVE 2 — Flow & gates (yêu cầu Wave 1 đã merge, đặc biệt Part A và E)

## Part H — Router ép review ở flat mode + dọn dead state `FlowReviewing`

**Mức độ: P0 — lỗ hổng flow nghiêm trọng nhất.**

### Bối cảnh & nguyên nhân
Flat mode (truyện không phân tập/cung): `commit_chapter` trả tín hiệu `review_required` mỗi 5 chương (`ShouldReview`, `internal/domain/chapter.go:9-14`), nhưng Flow Router (`internal/host/flow/router.go:141-151`) luôn dispatch "viết chương kế" — việc gọi editor phụ thuộc hoàn toàn Coordinator LLM đọc tín hiệu và tuân prompt. Model yếu bỏ qua → truyện chạy hàng chục chương không review. Layered mode thì router ĐÃ ép editor (`router.go:105-139`) — Part này làm nốt cho flat.

Đồng thời `domain.FlowReviewing` là **dead state**: không production code nào set nó (chỉ test), router có nhánh chờ nó (`router.go:95-97`), resume có label (`internal/host/resume.go:75`) — code chết mang mặt nạ sống.

### Thay đổi
1. **State mới trong flow State** (`internal/host/flow/` — tìm struct `State` mà `Route` nhận): thêm trường `HasPendingFlatReview bool`, tính ở nơi build State (dispatcher hoặc state builder — tìm nơi các trường như `HasArcReview` được tính): `true` khi (a) không layered, (b) tồn tại chương hoàn thành `N` với `N % ReviewInterval == 0` (interval lấy từ cùng nguồn `ShouldReview` dùng), (c) chưa tồn tại file review phủ chương đó (kiểm qua store Reviews — cùng cách layered kiểm `HasArcReview`). Chỉ xét batch gần nhất (không đòi nợ mọi batch lịch sử — nhưng batch gần nhất chưa review thì đòi).
2. **Router**: chèn nhánh mới NGAY TRƯỚC nhánh "12. Tiếp tục viết bình thường" (`router.go:141`): nếu `!p.Layered && s.HasPendingFlatReview` → `Instruction{Agent: "editor", Task: "Đánh giá batch chương <from>-<to> (scope=batch)", Reason: "Review định kỳ chưa hoàn thành"}`.
3. **Xoá `FlowReviewing`**: xoá hằng trong `internal/domain` (tìm định nghĩa), xoá nhánh `router.go:95-97`, xoá label trong `resume.go:75`, xoá/sửa test đang set nó. Lý do xoá thay vì dùng: state suy từ đĩa (có file review chưa) đáng tin hơn cờ — cờ mất khi crash, file không.
4. Cập nhật test router: thêm case flat-mode-pending-review → editor; flat-mode-đã-review → writer chương kế.

### Files sở hữu
- `internal/host/flow/router.go`
- `internal/host/flow/router_test.go`
- `internal/host/flow/dispatcher.go` (nếu State build ở đây)
- `internal/host/resume.go` (chỉ dòng label FlowReviewing)
- `internal/domain/` file chứa định nghĩa Flow constants (chỉ xoá FlowReviewing)
- Các file test khác CHỈ khi chúng reference `FlowReviewing` (grep trước, liệt kê trong báo cáo)

### Acceptance
- `grep -r "FlowReviewing" --include="*.go"` → 0 kết quả.
- Test router mới xanh; `go test ./...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** review từ "chạy bằng niềm tin vào LLM" thành cưỡng chế máy — đóng lỗ hổng lead lớn nhất.
- **Mặt trái:** +1 lượt editor mỗi 5 chương (chi phí đúng nghĩa); sách dở dang khi resume sẽ bị "đòi nợ" review batch gần nhất — hành vi đúng, ghi chú trong báo cáo.

---

## Part I — Trần rewrite/chương + polish gate cho aesthetic + sửa prompt khớp sự thật

**Mức độ: P1 — vá "văn AI mức warning luôn ship" + phanh vòng lặp. Gộp một Part vì cùng chạm `save_review.go` và các prompt.**

### Bối cảnh & nguyên nhân
Ba quy định cộng hưởng khiến chất lượng câu văn không có cơ chế nào bảo vệ:
1. `assets/prompts/writer.md:18` cấm writer tự polish sau khi check_consistency pass ("lãng phí lượt").
2. `assets/prompts/editor.md:166`: "Warning về thẩm mỹ không cấu thành lý do làm lại".
3. Scorecard gate (`internal/tools/save_review.go:81-105`, `evaluateScorecardGate:260-280`) chỉ nâng verdict khi các chiều critical (consistency/character/continuity — `criticalDimensions:252-256`) fail hoặc contract miss. Chiều **aesthetic không bao giờ trigger** hành động.

→ Văn AI-flavor mức warning ship 100% theo thiết kế. Đồng thời `writer.md:58` và `assets/references/anti-ai-tone.md:3` tuyên bố "bắt buộc kiểm tra khi lưu chương" — gate đó **không tồn tại** (máy chỉ enforce 4 rule substring; 5 nhóm anti-AI ngữ nghĩa hoàn toàn do LLM tự phán).

Riêng biệt: **không có trần số vòng rewrite** — `save_review` set `PendingRewrites`, router drain, lặp vô hạn nếu model chấm thấp mãi. Chỉ budget cả sách và StopGuard là phanh gián tiếp.

### Thay đổi

**I.1 — Trần rewrite/chương (làm TRƯỚC I.2, vì I.2 thêm nguồn vòng lặp mới):**
1. Tìm nơi lưu review record / progress (schema mà `save_review` ghi): thêm đếm `rewrite_count` per-chương — tăng mỗi lần chương vào `PendingRewrites` vì verdict rewrite/polish.
2. Trong `save_review`, trước khi set PendingRewrites: nếu chương đã có `rewrite_count >= 3` (hằng số, đặt tên `maxRewritePerChapter`, comment lý do) → ép verdict về `accept`, ghi cờ `quality_debt: true` + lý do vào review record, emit event cảnh báo (dùng cơ chế event sẵn có trong package nếu với tới được; không thì chỉ ghi record).
3. KHÔNG chặn ở router/commit — chặn tại nguồn (save_review) là đủ và gọn nhất.

**I.2 — Polish gate cho aesthetic:**
4. `evaluateScorecardGate`: thêm điều kiện — `aesthetic < 70` → nâng verdict tối thiểu lên `polish` (KHÔNG bao giờ rewrite vì thẩm mỹ). Nhưng: nếu review record cho thấy chương này **đã polish vì aesthetic 1 lần** (thêm cờ `aesthetic_polished` vào record khi trigger) → không nâng nữa, accept kèm ghi chú. Trần 1 vòng — mấu chốt tránh đổi "ship văn xấu" lấy "kẹt loop".
5. Đường polish dùng cơ chế sẵn có: verdict polish → `PendingRewrites` → router dispatch writer → writer dùng `edit_chapter` sửa cục bộ (writer prompt đã có hướng dẫn; editor.md đã bắt buộc trích dẫn bản gốc ở chiều aesthetic — nguyên liệu sửa có sẵn).

**I.3 — Sửa prompt khớp sự thật:**
6. `assets/prompts/writer.md` dòng ~18: giữ cấm *tự ý* polish, thêm ngoại lệ: "trừ khi nhận nhiệm vụ polish từ editor (verdict polish) — khi đó dùng edit_chapter sửa đúng các đoạn editor trích dẫn, không viết lại cả chương."
7. `assets/prompts/writer.md` dòng ~58 + `assets/references/anti-ai-tone.md` dòng ~3: bỏ/sửa chữ "bắt buộc kiểm tra khi lưu chương" → nói đúng cơ chế thật: "checker chặn forbidden_chars/phrases khi commit; fatigue/wordcount là warning; stylestat đo pattern toàn tập; editor chấm chiều aesthetic và có thể yêu cầu polish khi < 70."
8. `assets/prompts/editor.md` dòng ~166: sửa thành "Warning thẩm mỹ đơn lẻ không buộc làm lại; nhưng aesthetic < 70 sẽ kích hoạt một vòng polish (tối đa một lần mỗi chương). Khi chấm aesthetic thấp, PHẢI trích dẫn cụ thể các đoạn cần sửa — writer sẽ sửa đúng theo trích dẫn."

### Files sở hữu
- `internal/tools/save_review.go`
- `internal/tools/save_review_test.go`
- `assets/prompts/writer.md`
- `assets/prompts/editor.md`
- `assets/references/anti-ai-tone.md`
- File schema/store chứa review record (xác định khi làm; nếu là file store riêng, liệt kê trong báo cáo; nếu trùng file Part khác của Wave 2 → DỪNG, báo cáo)

### Acceptance
- Test: aesthetic=65, các chiều khác ≥80, chưa polish lần nào → verdict polish, `aesthetic_polished=true`.
- Test: aesthetic=65, `aesthetic_polished=true` → accept + ghi chú.
- Test: chương rewrite lần 4 → accept + `quality_debt=true`.
- Test cũ của scorecard gate vẫn xanh (critical fail → rewrite, contract missed → rewrite...).
- `go test ./internal/tools/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** chuỗi anti-AI hoàn chỉnh lần đầu: đo (stylestat) → chấm (editor + trích dẫn) → sửa (edit_chapter) → trần (1 vòng). Văn xuôi có người chịu trách nhiệm. Prompt hết hứa gate không tồn tại.
- **Mặt trái:** +1 vòng edit cho chương văn yếu (ước 10-20% chương) = token tăng; ngưỡng 70 là số khởi điểm cần tune (diag `ChronicLowDimension` sẵn có để theo dõi); chất lượng polish phụ thuộc editor trích dẫn cụ thể; auto-accept sau trần = ship chương dưới chuẩn có cờ — lựa chọn kia (kẹt loop) tệ hơn.

---

## Part J — Cảnh báo budget theo chương

**Mức độ: P1 — phát hiện "chương bệnh" đốt ngân sách.**

### Bối cảnh & nguyên nhân
`BudgetSentinel` (`internal/host/budget.go`) chỉ theo dõi `book_usd` tích lũy toàn sách (warn ở `warnRatio*limit`, dừng ở `limit`). Không có khái niệm chi phí per-chương → một chương bất thường (rewrite loop, draft khổng lồ) ngốn phần lớn ngân sách sách mà không có cảnh báo cục bộ nào.

### Thay đổi
1. `BudgetSentinel` thêm tracking per-chương: lắng nghe event kết thúc commit_chapter (Sentinel đã nhận events — tìm `HandleEvent`/`OnCost` tại `budget.go:75-112`; nếu event hiện có không mang tên tool, dùng event `EventToolExecEnd` với tool=`commit_chapter` như Dispatcher làm tại `internal/host/flow/dispatcher.go:57-98` — CHỈ đọc pattern từ dispatcher, không sửa file đó).
2. Ghi `costAtLastCommit`; delta giữa 2 commit = chi phí chương vừa xong. Giữ trung bình trượt (EMA hoặc trung bình 5 chương gần). Chương vừa xong tốn > 3× trung bình (hằng `perChapterWarnFactor = 3`) và đã có ≥ 3 chương làm baseline → emit warning event (dùng đúng cơ chế emit warning sẵn có của Sentinel) nêu số USD chương đó vs trung bình.
3. **Chỉ cảnh báo, không dừng, không chặn** — quyết định là của user.

### Files sở hữu
- `internal/host/budget.go`
- `internal/host/budget_test.go`

### Acceptance
- Test: 4 chương cost đều 1.0, chương 5 cost 4.0 → warning phát đúng 1 lần, nội dung chứa số liệu.
- Test: chưa đủ 3 chương baseline → không warning dù lệch.
- Test cũ của Sentinel (warn/stop toàn cục) vẫn xanh.
- `go test ./internal/host/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** chương bệnh lộ diện sớm thay vì âm thầm ăn 40% ngân sách.
- **Mặt trái:** heuristic 3× có thể false-positive ở chương cao trào dài (chấp nhận — chỉ là warning).

---

## Part K — Cocreate: persist session + cắt history + nâng maxTokens

**Mức độ: P2 — sửa 3 lỗi cùng ổ. Yêu cầu Part E (test parser) đã merge làm lưới an toàn.**

### Bối cảnh & nguyên nhân
1. **Mất công user:** toàn bộ session cocreate cold-start (hội thoại + draft trau chuốt qua nhiều lượt) chỉ nằm RAM (`internal/entry/startup/cocreate.go:11-18`); Esc/quit → mất sạch, chỉ còn log forensic `meta/sessions/cocreate.jsonl` không restore được.
2. **Phình O(n²):** `coCreateStream` (`internal/host/cocreate.go:96-107`) gửi lại **toàn bộ** history mỗi lượt; `ApplyReply` (`startup/cocreate.go:45-51`) lưu assistant = `reply.Raw` (cả 4 thẻ gồm `<draft>` đầy đủ) → draft lặp trong mọi lượt cũ.
3. **Output cụt:** `WithMaxTokens(2048)` (`cocreate.go:133`) cho cả `<reply>+<draft>+<suggestions>` — draft trưởng thành → bị cắt giữa `<draft>`, mất `<ready>/<suggestions>` đúng lúc gần xong.

### Thay đổi

**K.1 — maxTokens (nhỏ nhất, làm trước):** `cocreate.go:133` đổi `WithMaxTokens(2048)` → `WithMaxTokens(4096)`.

**K.2 — Cắt history:**
1. `ApplyReply`: lưu assistant message = `reply.Message` + (nếu `reply.Prompt != ""`) khối `<draft>` hiện hành — KHÔNG lưu `Raw` tích luỹ nữa. Mục đích gốc của việc lưu Raw là để model thấy lại draft cũ — thay bằng cơ chế ghim ở (2).
2. `coCreateStream` (hoặc nơi build msgs — giữ trách nhiệm ở host, session chỉ cung cấp data): chỉ gửi **K=8 lượt cuối** của history; **draft mới nhất ghim** vào cuối system prompt dưới mục "## Bản chỉ thị hiện hành" (session đã có `draftPrompt` — cần truyền vào stream func; xem chữ ký `CoCreateStream` tại `internal/host/host.go:940-946` và cách TUI gọi — mở rộng chữ ký hoặc thêm tham số, cập nhật cả `StageCoCreateStream`).
3. Lượt cũ bị cắt: không gửi. Model luôn có draft hiện hành (ghim) + 8 lượt gần → đủ ngữ cảnh làm việc.

**K.3 — Persist session:**
4. `startup/cocreate.go`: thêm `Save(path)`/`Load(path)` cho `CoCreateSession` (JSON: history, draftPrompt, ready, suggestions). Ghi sau mỗi `ApplyReply` (best-effort, lỗi ghi không fail flow).
5. Vị trí file: cùng chỗ meta của cocreate log (`meta/cocreate_session.json` — tìm cách `SessionStore.LogCoCreate` resolve đường dẫn để dùng cùng root; nếu tầng startup không với tới store, nhận path qua tham số từ TUI).
6. TUI (`internal/entry/tui/`): khi mở tab cocreate cold-start, nếu file session tồn tại → hiện lựa chọn khôi phục (pattern hỏi/confirm sẵn có của TUI; tối thiểu: tự khôi phục + dòng thông báo "đã khôi phục phiên trước, Esc 2 lần để xoá làm mới"). Xoá file khi `StartPrepared` thành công hoặc user chọn làm mới.

### Files sở hữu
- `internal/host/cocreate.go`
- `internal/host/host.go` (CHỈ các hàm `CoCreateStream`/`StageCoCreateStream` ~dòng 940-946)
- `internal/entry/startup/cocreate.go`
- `internal/entry/startup/cocreate_test.go` (tạo/thêm)
- `internal/entry/tui/cocreate.go`
- `internal/entry/tui/model.go`, `internal/entry/tui/events.go` (chỉ phần gọi cocreate — nếu diff lan quá phần này, DỪNG báo cáo)

### Acceptance
- Part E test parser vẫn xanh nguyên vẹn (không sửa parser).
- Test: session 12 lượt → msgs gửi model = system + 8 lượt cuối; system chứa draft hiện hành đúng 1 lần.
- Test: `ApplyReply` với Raw 4 thẻ → history không chứa `<ready>`/`<suggestions>`; draft cũ không lặp trong history.
- Test: Save → Load round-trip đủ trường.
- `go test ./internal/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** hết mất công user (nỗi đau thật nhất); chi phí mỗi lượt từ O(n·draft) về O(K); draft lớn không làm cụt output.
- **Mặt trái:** model mất chi tiết các lượt đầu (bù bằng draft ghim — draft là state tích luỹ đúng nghĩa); thêm 1 file state phải dọn đúng thời điểm; đổi chữ ký 2 hàm public của host (nội bộ repo, không phải API ngoài).

---

# WAVE 3 — Chất lượng văn chương (yêu cầu Wave 2 merge; Part L làm TRƯỚC M vì chạm chung novel_context_builders + prompts)

> **Lưu ý điều phối:** L và M **chạm chung file** (`novel_context_builders.go`, `architect-long.md`, `architect-short.md`, `writer.md`, `editor.md`) → **KHÔNG chạy song song**. Chạy L xong, merge, rồi chạy M. (Đây là ngoại lệ duy nhất của quy tắc song song trong tài liệu này.)

## Part L — Khai báo POV/tense (narrative contract)

**Mức độ: P1 — lỗ thủng chuẩn nghề sơ đẳng.**

### Bối cảnh & nguyên nhân
Không prompt nào trong hệ đặt chuẩn ngôi kể (ngôi 1 / ngôi 3 hạn tri / toàn tri), thì (quá khứ/hiện tại), nhân vật POV. `editor.md:70` chiều 7 chỉ kiểm "góc nhìn thống nhất hoặc chuyển đổi có chủ ý" — nhất quán với cái model tự chọn ngẫu nhiên ở chương 1, không có chuẩn để so. Trôi POV giữa sách là lỗi biên tập viên năm nhất phải bắt; hệ này không bắt được.

### Thay đổi
1. **Schema foundation** (`internal/tools/save_foundation.go` — tìm struct args/payload): thêm trường optional `narrative`:
   ```json
   {
     "pov": "ngôi 3 hạn tri",
     "pov_characters": ["Tên A"],
     "tense": "quá khứ",
     "notes": "đổi POV chỉ tại ranh giới chương; mỗi chương 1 POV"
   }
   ```
   Optional — thiếu = hành vi cũ (không ràng buộc). Cho phép `pov: "đa POV"` kèm notes quy tắc chuyển.
2. **Lưu & nạp:** persist cùng chỗ foundation data hiện có; `novel_context` builder (`internal/tools/novel_context_builders.go`, hàm build working memory cho writer quanh `:429`) inject `narrative` vào `chapter_contract` hoặc mục riêng `narrative_contract` trong working memory mỗi chương — vài chục token, luôn có mặt.
3. **Prompts:**
   - `assets/prompts/architect-long.md` + `architect-short.md`: mục premise/foundation thêm yêu cầu khai `narrative` (bắt buộc khai khi tạo mới; 1-2 dòng hướng dẫn chọn theo thể loại).
   - `assets/prompts/writer.md`: mục tiêu chuẩn thêm "tuân thủ `narrative_contract` trong working memory; không đổi ngôi/thì giữa chừng trừ khi contract cho phép".
   - `assets/prompts/editor.md` chiều 7: đổi tiêu chí thành "so với `narrative` đã khai trong foundation; lệch = lỗi continuity, trích dẫn câu lệch".
4. Foundation cũ thiếu trường → mọi tầng bỏ qua êm (kiểm nil).

### Files sở hữu
- `internal/tools/save_foundation.go` (+ test)
- `internal/tools/novel_context_builders.go`
- `internal/tools/novel_context_test.go` (thêm test)
- `assets/prompts/architect-long.md`, `assets/prompts/architect-short.md`, `assets/prompts/writer.md`, `assets/prompts/editor.md`

### Acceptance
- Test: foundation có narrative → working memory writer chứa `narrative_contract`.
- Test: foundation thiếu narrative → không có mục, không panic.
- `go test ./internal/tools/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** chặn trôi POV — chi phí ~vài chục token/chương.
- **Mặt trái:** +1 trường schema maintain; kiến trúc sư có thể khai sáo (hướng dẫn chọn theo thể loại giảm rủi ro).

---

## Part M — Voice card nhân vật chính (LÀM SAU Part L)

**Mức độ: P1 — chống đồng nhất hoá giọng đối thoại.**

### Bối cảnh & nguyên nhân
Nhân vật trong `characters.json` chỉ có description/traits. Dialogue sheet per-nhân-vật chỉ được sinh ở arc mode truyện dài (qua `save_arc_summary`, xem `editor.md:187-191`). Nhân vật chính không có hồ sơ giọng nói → sau 20 chương mọi nhân vật nói giọng của model. `recent_cast` (writer.md:76-82) cứu nhân vật phụ nhưng bỏ chính diện. Stylestat không đo được (nó đo pattern toàn văn, không per-nhân-vật).

### Thay đổi
1. **Schema character** (tìm struct trong `internal/store/` — characters store + snapshots): thêm trường optional `voice` cho tier core/important:
   ```json
   {
     "catchphrases": ["câu cửa miệng, từ đệm đặc trưng"],
     "sentence_style": "câu ngắn, cộc; hầu như không dùng từ Hán Việt trang trọng",
     "subtext_level": "cao — hiếm khi nói thẳng cảm xúc",
     "taboo": "không bao giờ văn hoa, không giải thích dài"
   }
   ```
2. **Sinh:** `assets/prompts/architect-long.md` + `architect-short.md`: khi tạo nhân vật core/important, bắt buộc kèm `voice`; thêm 1 ví dụ TỐT và 1 ví dụ SÁO (VD sáo: "nói năng điềm đạm, sâu sắc" — cấm kiểu này, đòi đặc điểm *kiểm chứng được trong câu chữ*).
3. **Nạp:** `novel_context_builders.go` — nơi lọc nhân vật theo chương cho writer (quanh `loadLayeredCharacters` / cast lọc theo tier): kèm `voice` của các nhân vật xuất hiện trong `chapter_plan`. Không inject toàn bộ dàn nhân vật — chỉ nhân vật của chương.
4. **Kiểm:** `assets/prompts/editor.md` chiều 2 (character): thêm tiêu chí "đối thoại khớp voice card; lệch giọng = trích dẫn câu lệch + nêu nhân vật". Editor được phép đề xuất cập nhật voice qua cơ chế cập nhật nhân vật sẵn có nếu nhân vật phát triển giọng có chủ ý.
5. `assets/prompts/writer.md`: mục nhân vật thêm "viết đối thoại theo voice card trong working memory".
6. Characters cũ thiếu `voice` → bỏ qua êm.

### Files sở hữu
- File store characters trong `internal/store/` (+ test) — xác định chính xác khi làm, liệt kê trong báo cáo
- `internal/tools/novel_context_builders.go`
- `internal/tools/novel_context_test.go` (thêm test)
- `assets/prompts/architect-long.md`, `assets/prompts/architect-short.md`, `assets/prompts/writer.md`, `assets/prompts/editor.md`

### Acceptance
- Test: nhân vật có voice + có mặt trong chapter_plan → working memory chứa voice card của đúng nhân vật đó, không chứa nhân vật vắng mặt.
- Test: nhân vật thiếu voice → không panic, không mục rỗng.
- `go test ./internal/tools/... ./internal/store/...` xanh.

### Tác động / hiệu quả / mặt trái
- **Hiệu quả:** đánh đúng điểm yếu văn AI khó nhất (giọng đồng nhất); cho editor chuẩn cụ thể để trích dẫn khi chấm.
- **Mặt trái:** +200-400 token context/chương; voice do LLM sinh có thể sáo (ví dụ TỐT/SÁO trong prompt giảm rủi ro); hiệu quả chỉ thấy khi đọc, khó đo máy.

---

# Ma trận file × Part (kiểm tra không giẫm chân)

| File / vùng | A | B | C | D | E | F | G | H | I | J | K | L | M |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `internal/store/drafts.go` | ✏️ | | | | | | | | | | | | |
| `internal/rules/checker*.go` | ✏️ | | | | | | | | | | | | |
| `rules.md.example` + `diag/rules_quality_test.go` | | ✏️ | | | | | | | | | | | |
| `internal/agents/ctxpack/restore*.go` | | | ✏️ | | | | | | | | | | |
| `scripts/check_chapter_wordcount.py` | | | | ✏️ | | | | | | | | | |
| `internal/host/cocreate_parse_test.go` (mới) | | | | | ✏️ | | | | | | | | |
| `internal/tools/novel_context*.go` | | | | | | ✏️ | | | | | | ✏️ | ✏️ |
| `internal/host/cocreate_stage*.go` | | | | | | | ✏️ | | | | | | |
| `internal/host/flow/*` + `resume.go` + `domain` Flow | | | | | | | | ✏️ | | | | | |
| `internal/tools/save_review*.go` | | | | | | | | | ✏️ | | | | |
| `assets/prompts/*.md` + `anti-ai-tone.md` | | | | | | | | | ✏️ | | | ✏️ | ✏️ |
| `internal/host/budget*.go` | | | | | | | | | | ✏️ | | | |
| `internal/host/cocreate.go` + `host.go` (2 hàm) + `startup/cocreate.go` + TUI | | | | | | | | | | | ✏️ | | |
| `internal/tools/save_foundation.go` | | | | | | | | | | | | ✏️ | |
| Store characters | | | | | | | | | | | | | ✏️ |

Xung đột duy nhất theo thiết kế: **F–L–M** chung `novel_context*` và **I–L–M** chung prompts → F nằm Wave 1 (xong trước), L → M tuần tự trong Wave 3, I nằm Wave 2 (xong trước L/M). Không có cặp nào chạy song song mà chung file.

# Trình tự & song song

- **Wave 1:** A, B, C, D, E, F, G — **7 subagent song song**, không chung file.
- **Wave 2:** H, I, J, K — **4 subagent song song**, không chung file. (K cần E đã merge — E thuộc Wave 1 nên tự thoả.)
- **Wave 3:** L trước, M sau — **tuần tự**.
- Sau mỗi wave: `go test ./...` + chạy thử 1 phiên headless ngắn (`--prompt` truyện test vài chương) trước khi sang wave kế.
