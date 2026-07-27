# Backlog Việt hóa UX/flow — tàn dư gốc Trung ngoài pipeline sinh văn

> Khảo sát 2026-07-27. Pipeline SINH VĂN đã xử lý xong (docs/report.md). Đây là tầng còn lại:
> xuất bản, import, TUI, notify, ước lượng token. Xếp theo ưu tiên giảm dần.
> Độc lập với docs/llm-first-plan.md — làm xen kẽ được, không đụng file plan kia trừ mục 4.

## P0 — Lỗi rõ, sửa ngay được

1. **EPUB khai `zh-CN`** — `internal/host/exp/epub.go:131,178,198,246` (`xml:lang="zh-CN"`) và `:250` (`<dc:language>zh-CN</dc:language>`). Máy đọc/thư viện sẽ coi sách là tiếng Trung (hyphenation, font fallback, TTS sai). Đổi `vi` toàn bộ + test đối chiếu chuỗi trong epub_test.
2. **Import fallback GB18030** — `internal/utils/textenc.go:17-23`: file không phải UTF-8 bị giải mã như GBK (giả định "txt tiểu thuyết Trung"). Người Việt import file cũ VNI/TCVN3/Windows-1258 → ra rác. Dùng tại `host/imp/splitter.go:57`, `host/sim/scanner.go:78`. Fix: thử Windows-1258 trước (hoặc bỏ fallback GBK, báo lỗi rõ "file không phải UTF-8, hãy convert" — an toàn hơn đoán sai im lặng).
3. **Tên sách TXT bọc ngoặc Trung `《》`** — `internal/host/exp/txt.go:113-115`. Quy ước Việt: «Tên» hoặc "Tên" hoặc TÊN in hoa. Chuỗi hiển thị trực tiếp cho độc giả bản TXT.

## P1 — UX bề mặt người dùng Việt

4. **Ví dụ gợi ý trong TUI là xianxia Trung** — `internal/entry/tui/panels.go:1681`: "truyện tiên hiệp, tu luyện từ phàm nhân đến phi thăng". Người dùng Việt đầu tiên nhìn thấy chính là dòng này. Thay bằng 2-3 ví dụ đa dạng thị trường Việt (ngôn tình hiện đại, linh dị/trinh thám, lịch sử Việt...). Tiên hiệp giữ được — độc giả Việt vẫn đọc — nhưng đừng là ví dụ DUY NHẤT.
5. **Notify câm trên Windows** — `internal/notify/notify.go:104-113`: system notify chỉ có darwin (osascript) + linux (notify-send); Windows rơi default → chỉ ghi log. Máy dev chính là Windows. Fix: nhánh windows dùng PowerShell toast (BurntToast-style script hoặc `msg`/WScript đơn giản). Part 0D human gate DỰA vào notify — cần mục này trước/kèm 0D.
6. **Notify custom command ép `sh -c`** — `notify.go:89`: Windows không git-bash là chết. Fix: `runtime.GOOS=="windows"` → `cmd /C` (hoặc PowerShell), còn lại giữ sh.
7. **Genre pack mỏng** — `assets/references/genres/` chỉ fantasy/romance/suspense; thiếu pack cho thể loại writer Việt hay dùng: tiên hiệp/huyền huyễn (đọc giả Việt đọc nhiều), ngôn tình (đang gộp romance), linh dị. Mỗi pack = style-references.md + từ vựng thể loại. Việc content, không phải code — làm dần, ưu tiên thể loại bạn định viết trước.

## P2 — Calibrate ước lượng (đúng dần, không gấp)

8. **`estimateTokens` đếm BYTE chia 4** — `internal/bootstrap/models.go:632-638`: `len()` là byte; tiếng Việt có dấu 2-3 byte/ký tự → ước lượng PHÓNG ĐẠI ~1.5-2× → pre-gate TPM (ratelimit) siết sớm hơn thực tế. Không sai chức năng, chỉ lãng phí quota. Fix rẻ: đếm rune thay byte, giữ /4; hoặc calibrate hằng số riêng (tiếng Việt ~2.5-3 ký tự/token với tokenizer đa ngữ).
9. **`trimByBudget` ngân sách theo BYTE** — `internal/tools/novel_context.go:129,131` (100KB/60KB): cùng budget byte chứa ít NỘI DUNG Việt hơn Trung (mật độ nghĩa/byte thấp hơn). Cân nhắc nâng ~1.3× hoặc chuyển đếm rune. Đo trước khi chỉnh (log kích thước envelope thực tế vài chương).
10. **Comment sai mô tả** — `panels.go:1437` ("7 chữ Hán"), `panels.go:1498-1499` (mô tả width CJK cho tiếng Việt — code đúng, comment gây hiểu nhầm), comment CJK còn lại trong `txt.go`. Sửa khi tiện tay, không mở PR riêng.

## Không cần làm

- Console codepage Windows: TUI bubbletea tự xử lý UTF-8; chưa thấy lỗi thực tế — chỉ xử lý nếu có báo cáo hiển thị hỏng.
- Font EPUB: `serif` không hardcode font Trung, render Việt ổn.
- Tên lệnh slash tiếng Anh + mô tả Việt: giữ — convention CLI tốt.
- Test fixtures tiếng Trung trong `*_test.go`: vô hại, đã có kế dọn ở improvement-plan Part B cũ.
