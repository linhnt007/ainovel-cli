---
# Quy tắc mặc định tích hợp sẵn của dự án (Phase 1 - phiên bản an toàn)
#
# Chỉ đặt ở đây các ràng buộc mặc định "có thể kiểm tra tự động + ít tranh cãi".
# Các sở thích thẩm mỹ phi tự động (như xu hướng phong cách) hiện vẫn do
# writer.md / editor.md đảm nhiệm; sẽ quyết định có chuyển vào file này hay không
# sau Phase 1.5 (sau khi kiểm thử tay F1 xác nhận working_memory có hiệu lực ràng buộc).
#
# Người dùng có thể ghi đè các trường thông thường bằng cách đặt file .md bất kỳ
# trong thư mục ./.ainovel/rules/ hoặc ~/.ainovel/rules/;
# fatigue_words được hợp nhất theo từng từ, cùng một từ thì nguồn gần hơn ghi đè ngưỡng.
# Xem chi tiết ngữ nghĩa các trường tại rules.md.example ở thư mục gốc dự án.

# Giới hạn số từ mỗi chương (đếm TỪ THẬT, tách theo khoảng trắng — không phải đếm ký tự/rune):
# luôn cảnh báo (warning) khi lệch khỏi phạm vi, khuyến nghị 2000-5000 từ để đảm bảo độ sâu miêu tả.
chapter_words: 2000-5000

# Danh sách cụm từ cấm: xuất hiện ≥1 lần là error. Bộ kiểm tra so khớp chuỗi con
# theo nghĩa đen, không hỗ trợ wildcard, nên chỉ đặt các câu sáo rỗng AI "chuỗi cố định"
# (ít tranh cãi); các mẫu có biến (như "không phải X mà là Y") không bắt được bằng
# so khớp nghĩa đen — thuộc tầng ngữ nghĩa của anti-ai-tone.md.
# Dấu gạch ngang "——" hợp lệ khi đối thoại bị ngắt, còn tranh cãi, không đưa vào
# mặc định tích hợp, để ./.ainovel/rules/ tự cấu hình.
forbidden_phrases:
  - "theo một nghĩa nào đó"
  - "đáng chú ý là"
  - "không hiểu tại sao"
  - "cảm xúc lẫn lộn"

# Giới hạn mềm cho từ sáo rỗng: commit_chapter sẽ kiểm tra số lần xuất hiện mỗi chương,
# vượt ngưỡng sẽ báo warning.
# Đây là những từ bị lạm dụng phổ biến trong tiểu thuyết mạng/truyện dài;
# anti-ai-tone.md cũng có gợi ý ngữ nghĩa cùng hướng — hai nguồn tín hiệu thống nhất.
# Sáu mục cuối (như thể/im lặng/không nói gì/X nhịp thở) là kết quả thực nghiệm từ vòng lặp dài 196 chương:
# các câu sáo rỗng truyền thống đã bị bảng trên loại bỏ, nhưng mô hình chuyển sang dùng
# các "từ nhịp truyện" này với tần suất trung bình 5-7 lần mỗi chương; ngưỡng được nới lỏng
# để cho phép sử dụng bình thường.
fatigue_words:
  không khỏi: 1
  bỗng nhiên: 1
  dường như: 2
  ngoài ra: 1
  tuy nhiên: 2
  một chút: 2
  một vệt: 2
  một sợi: 2
  tựa như: 1
  không thể không: 1
  như thể: 3
  im lặng: 2
  không nói gì: 2
  vài nhịp thở: 3
  một nhịp thở: 3
  mấy nhịp thở: 2
---
