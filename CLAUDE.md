# ainovel-cli

## CodeGraph — kỷ luật context (chống phình)

`.codegraph/` có index. Vẫn ưu tiên CodeGraph trước grep/Read, NHƯNG chọn đúng tool theo nhu cầu — mặc định KHÔNG phải `explore`:

| Nhu cầu | Tool | Chi phí |
|---------|------|---------|
| "X ở đâu / có gì trong file", map symbol, ai phụ thuộc | `codegraph_node` (symbols-only khi đọc file) | ~800 tok |
| Tìm tên symbol mờ | `codegraph_search` | thấp |
| Ai gọi / gọi gì / blast radius khi đổi X | `codegraph_callers` / `callees` / `impact` | thấp |
| Hiểu 1 flow chạy xuyên nhiều symbol | `codegraph_explore` — **BẮT BUỘC `maxFiles: 2`** (tăng chỉ khi 1 call thật sự thiếu) | cao |

Quy tắc:
- KHÔNG dùng `explore` cho tra cứu đơn ("hàm này ký hiệu gì", "struct này field nào") — dùng `node`. `explore` dump verbatim source nhiều file → phình.
- `explore` chỉ khi câu hỏi thực sự là "luồng/kiến trúc chạy thế nào" và cần source liền mạch. Bắt đầu `maxFiles: 2`, chỉ nới khi thiếu.
- Source đã hiện trong kết quả CodeGraph = coi như đã Read; KHÔNG mở lại file đó bằng Read.
- `explore` mặc định repo này cap ~18K char / 5 file (tier <500 file). Đừng vượt bằng maxFiles cao trừ khi cần.
