#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Kiểm tra số từ của chương
Đếm số TỪ (tách theo khoảng trắng) của file chương; dưới ngưỡng thì nhắc cần mở rộng.
Đếm theo từ thật cho tiếng Việt (khác bản cũ đếm ký tự Hán, vốn trả 0 cho văn Việt).
Vẫn nhận diện tiêu đề chương song ngữ: "# Chương N ..." (Việt) và "# 第N章 ..." (Trung).
"""

import re
import sys
from pathlib import Path

# Sửa lỗi encoding console trên Windows
if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')


def strip_markdown(text: str) -> str:
    """Bỏ các dấu Markdown để đếm nội dung thuần."""
    text = re.sub(r'#{1,6}\s*', '', text)
    text = re.sub(r'\*\*(.*?)\*\*', r'\1', text)
    text = re.sub(r'\*(.*?)\*', r'\1', text)
    text = re.sub(r'~~(.*?)~~', r'\1', text)
    text = re.sub(r'`(.*?)`', r'\1', text)
    text = re.sub(r'\[(.*?)\]\(.*?\)', r'\1', text)
    return text


def count_words(text: str) -> int:
    """Đếm số TỪ tiếng Việt: tách theo khoảng trắng sau khi bỏ Markdown."""
    text = strip_markdown(text)
    return len(text.split())


# Tiêu đề chương song ngữ: "# Chương 12: ..." (Việt) hoặc "# 第12章 ..." (Trung).
_chapter_heading_re = re.compile(r'^#+\s+(?:第.+?章|[Cc]hương\s*\d+)')


def _is_chapter_heading(line: str) -> bool:
    return bool(_chapter_heading_re.match(line))


def extract_content_from_chapter(file_path: Path) -> str:
    """Trích phần thân chương (bỏ dòng tiêu đề/metadata đầu file)."""
    content = file_path.read_text(encoding='utf-8')
    lines = content.split('\n')

    content_start = 0
    for i, line in enumerate(lines):
        if _is_chapter_heading(line):
            content_start = i + 1
            break

    return '\n'.join(lines[content_start:])


def check_chapter(file_path: str, min_words: int = 3000) -> dict:
    """Kiểm tra số từ của một chương."""
    path = Path(file_path)
    if not path.exists():
        return {
            'file': str(path),
            'exists': False,
            'word_count': 0,
            'status': 'error',
            'message': f'File không tồn tại: {file_path}',
        }

    main_content = extract_content_from_chapter(path)
    word_count = count_words(main_content)
    status = 'pass' if word_count >= min_words else 'fail'
    message = f'Số từ: {word_count}'
    if word_count >= min_words:
        message += ' (✓ đạt)'
    else:
        message += f' (✗ thiếu, cần ít nhất {min_words} từ)'

    return {
        'file': str(path),
        'exists': True,
        'word_count': word_count,
        'status': status,
        'message': message,
    }


def check_all_chapters(directory: str, pattern: str = 'Chương*.md', min_words: int = 3000) -> list:
    """Kiểm tra mọi file chương khớp mẫu trong thư mục."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Lỗi: thư mục không tồn tại - {directory}')
        return []

    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = 3000) -> None:
    """In kết quả kiểm tra."""
    if not results:
        print('Không tìm thấy file chương')
        return

    total_words = 0
    passed = 0
    failed = 0

    print('\n' + '=' * 60)
    print('Báo cáo kiểm tra số từ chương')
    print('=' * 60)

    for result in results:
        if not result['exists']:
            print(f'\n❌ {result["file"]}')
            print(f'   {result["message"]}')
            continue

        total_words += result['word_count']
        if result['status'] == 'pass':
            passed += 1
            icon = '✅'
        else:
            failed += 1
            icon = '⚠️ '

        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')

    print('\n' + '-' * 60)
    print(f'Tổng: {len(results)} chương | {passed} đạt | {failed} thiếu | tổng số từ: {total_words:,}')
    print('-' * 60)

    if failed > 0:
        print(f'\n⚠️  Có {failed} chương thiếu {min_words} từ, gợi ý mở rộng:')
        print('   - Thêm mô tả chi tiết (bối cảnh, tâm lý, hành động)')
        print('   - Bổ sung cảnh đối thoại')
        print('   - Mở rộng nội tâm nhân vật')
        print('   - Bồi đắp bối cảnh câu chuyện')
        print('\n   Tham khảo: references/content-expansion.md')


def main() -> None:
    """Hàm chính."""
    if len(sys.argv) < 2:
        print('Cách dùng:')
        print('  Kiểm tra một chương: python check_chapter_wordcount.py <đường dẫn file> [số từ tối thiểu]')
        print('  Kiểm tra tất cả:     python check_chapter_wordcount.py --all <thư mục> [số từ tối thiểu]')
        print('')
        print('Ví dụ:')
        print('  python check_chapter_wordcount.py novels/truyen/Chương01.md')
        print('  python check_chapter_wordcount.py novels/truyen/Chương01.md 3500')
        print('  python check_chapter_wordcount.py --all novels/truyen')
        print('  python check_chapter_wordcount.py --all novels/truyen 3500')
        return

    if sys.argv[1] == '--all':
        if len(sys.argv) < 3:
            print('Lỗi: dùng --all cần chỉ định đường dẫn thư mục')
            return
        directory = sys.argv[2]
        min_words = int(sys.argv[3]) if len(sys.argv) > 3 else 3000
        results = check_all_chapters(directory, min_words=min_words)
        print_results(results, min_words)
        return

    file_path = sys.argv[1]
    min_words = int(sys.argv[2]) if len(sys.argv) > 2 else 3000
    result = check_chapter(file_path, min_words)
    print_results([result], min_words)


if __name__ == '__main__':
    main()
