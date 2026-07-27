# Model Notes: Nhật ký đo lường năng lực các mô hình

Tài liệu này ghi lại kết quả kiểm tra năng lực thực tế của từng mô hình trong fleet local/cloud qua `scripts/modelprobe`.

## Kết quả đo thực tế (Chạy ngày 2026-07-27)

| Model | Role | Tool-call (P1) | JSON 7 chiều (P2) | Tiếng Việt (P3) | Context 12k (P4) | Ghi chú |
|---|---|---|---|---|---|---|
| `gemma-4-31b-it` | `coordinator`, `writer`, `editor` | PASS | 0/5 - 2/5 | PASS (~18.5%) | FAIL (429 Quota) | Yếu JSON nặng không có grammar; dính hạn ngạch token Free Tier |
| `gemini-3.1-flash-lite` | `architect` | PASS | 5/5 | PASS (20.02%) | PASS | Rất tốt trong vai trò lập cấu trúc |

## Khuyến nghị và điều chỉnh
- **gemma-4-31b-it:** Cần giới hạn tần suất gọi (RPM/TPM) hoặc nâng cấp gói API trả phí để tránh HTTP 429. Khi làm `editor` hoặc `writer` xuất JSON phức tạp, cần cơ chế sửa lỗi JSON hoặc cấu hình structured output (bằng grammar/jinja) để đảm bảo đầu ra hợp lệ.
- **gemini-3.1-flash-lite:** Phù hợp tuyệt đối cho các tác vụ phân tích, cấu trúc và thiết lập khung đề cương.
