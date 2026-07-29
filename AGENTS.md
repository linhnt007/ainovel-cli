# ainovel-cli

## CodeGraph — kỷ luật context (chống phình)

`.codegraph/` có index. Ưu tiên CodeGraph trước grep/Read.

| Nhu cầu | Tool | Chi phí |
|---------|------|---------|
| "X ở đâu", map symbol, dependencies | Bash `codegraph explore "tên_symbol"` | thấp |
| Tìm symbol mờ | Bash `codegraph explore "từ_khóa"` | thấp |
| Ai gọi / gọi gì / blast radius | Bash `codegraph explore "callers X"` | thấp |
| Flow xuyên nhiều symbol | MCP `codegraph_explore` `maxFiles: 2` | cao |

Quy tắc:
- MCP `codegraph_explore` CHỈ dùng cho flow/architecture — bắt đầu `maxFiles: 2`.
- Bash `codegraph explore` dùng cho mọi tra cứu đơn.
- Source CodeGraph hiện = đã Read; KHÔNG mở lại file đó.
- Không grep khi CodeGraph có đáp án.
