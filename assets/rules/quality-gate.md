---
# ── Quality Gate: Số từ tối thiểu + Phong cách đoạn văn ──
# Sao chép file này vào .ainovel/rules/quality-gate.md để kích hoạt.
# Hoặc đặt vào ~/.ainovel/rules/quality-gate.md để áp dụng cho tất cả sách.
# ───────────────────────────────────────────────────────────

# Số từ tối thiểu mỗi chương: 2500 từ (đếm từ thật, tách theo khoảng trắng).
# Viết dưới 2500 từ = warning (chapter_words luôn warning, không error).
# Phạm vi 2500-6000 từ cho phép viết chương dài mà không bị chặn.
chapter_words: 2500-6000

# Từ sáo rỗng nghiêm ngặt hơn mặc định.
# Ngưỡng 1 = xuất hiện 1 lần là warning.
# Giữ cho writer tránh dùng từ cửa miệng.
fatigue_words:
  không khỏi: 1
  bỗng nhiên: 1
  dường như: 1
  ngoài ra: 1
  tuy nhiên: 1
  một chút: 1
  tựa như: 1
  không thể không: 1
  như thể: 2
  im lặng: 1
  không nói gì: 1
  vài nhịp thở: 2
  một nhịp thở: 2
  mấy nhịp thở: 1
---

# Phong cách đoạn văn

## Bắt buộc — Viết thành đoạn văn liền mạch

- **Mỗi đoạn phải có ít nhất 3 câu liên kết** — không được viết câu đơn lẻ ngắn củn vô nghĩa
- **Đoạn văn phải triển khai đầy đủ 5 yếu tố**: bối cảnh không gian → cảm giác ngũ quan → tương tác môi trường → diễn biến tâm lý → đối thoại
- **Câu ngắn (≤10 từ) không được vượt quá 30% tổng câu trong chương** — xen kẽ câu ngắn với câu dài 30+ từ
- **Không viết tóm tắt bằng chuỗi câu ngắn** — phải triển khai thành đoạn văn liền mạch
- **Mỗi đoạn có chủ đề rõ ràng** — một đoạn miêu tả, một đoạn đối thoại, một đoạn suy nghĩ; không nhồi nhét hỗn loạn

## Cấm

- Câu cụt đơn lẻ đứng riêng đoạn (vd: "Anh bước đi." rồi xuống dòng)
- Liệt kê bằng câu ngắn liên tiếp (vd: "Anh đi vào. Cửa mở. Hắn nhìn.")
- Đoạn văn dưới 2 câu
- Mở đầu chương bằng "Đêm..." hoặc "Sáng sớm..."
- Tóm tắt diễn biến bằng chuỗi câu ngắn lặp lại

## Khuyến nghị

- Ưu tiên cảm giác xúc giác, khứu giác, thính giác hơn thuần thị giác
- Đối thoại xen kẽ hành động mô tả, không để dialogue thành chuỗi dài
- Kết thúc đoạn bằng hành động hoặc ẩn ý, không kết bằng tóm tắt
- Luân phiên hình thức kết thúc chương: câu ngắn chặt đứt / dư âm đối thoại / ảnh hưởng cảnh tượng / câu hỏi hồi hộp
- Đa dạng độ dài câu: câu 5-10 từ → câu 20-30 từ → câu 40+ từ, tạo nhịp văn có hơi thở

## Ví dụ đúng

```
Cánh cửa gỗ cũ kêu kẽo kẹt khi anh đẩy vào, mùi ẩm mốc xộc lên mũi kèm theo gió lạnh từ khe hở phía trên. Phía trong tối om, chỉ có ánh trăng lọt qua ô cửa nhỏ chiếu một vệt bạc lên nền đất. Anh dừng lại, mắt quen dần với bóng tối, rồi từ từ bước tới chiếc bàn gỗewhere người đàn ông kia đang ngồi. Ngón tay anh siết chặt chuôi kiếm, tim đập chậm lại — không phải sợ hãi, mà là sự tập trung đến cực điểm trước khi đối mặt với điều gì đó không thể đoán trước.
```

```
"Cậu nghĩ mình có thể thoát được à?" Giọng nói vang lên từ phía sau, trầm thấp nhưng mang theo sự đe dọa rõ ràng. Anh quay người, đối mắt với bóng đen đang dần hiện ra dưới ánh trăng. Không vội vàng, không hoảng sợ, anh từ từ rút kiếm ra — lưỡi kiếm sáng bóng phản chiếu ánh trăng, tạo thành một vệt sáng cắt ngang bóng tối.
```

## Ví dụ sai

```
Anh đi vào. Cửa mở. Hắn nhìn anh. Không ai nói gì. Im lặng. Rồi hắn cười. Nụ cười buồn. Anh rút kiếm. Đánh nhau. Kết thúc.
```

```
Anh cảm thấy lo lắng. Anh thấy sợ. Anh biết mình phải chạy. Anh bắt đầu chạy. Anh chạy nhanh hơn. Anh không dừng lại.
```
