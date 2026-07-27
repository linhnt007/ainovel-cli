#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Kiểm tra số từ của chương.

Đếm số TỪ (tách theo khoảng trắng, sau khi bỏ cú pháp Markdown) của file
chương và PHÂN LOẠI theo ngưỡng: DƯỚI NGƯỠNG / TRONG NGƯỠNG / VƯỢT NGƯỠNG
(mặc định 3000-6000 từ, chỉnh bằng --min/--max).

Đây là công cụ QUAN SÁT/báo cáo, KHÔNG phải cổng chặn build: exit code luôn
là 0, trừ khi có lỗi IO/parse thật sự (file/thư mục không tồn tại, không đọc
được nội dung...).

CHỦ Ý MẤT GATE CỨNG: bản trước đây fail cứng khi chương thiếu từ và in
khuyến nghị "viết thêm mô tả/nội tâm cho đủ số từ" — tức là dạy padding cho
đủ chỉ tiêu, đi ngược triết lý chống văn AI của hệ thống (số từ phục vụ nhịp
điệu chương, không phải để viết thêm cho đủ). Bản này bỏ hẳn việc fail cứng
và mọi khuyến nghị "viết thêm". Nếu bạn đang dựa vào script này như một gate
cứng trong CI, gate đó đã bị gỡ có chủ đích ở đây — hãy tự thêm logic
exit-code riêng ở nơi gọi nếu vẫn cần chặn build theo số từ.

Nhận diện tiêu đề chương song ngữ: "# Chương N ..." (Việt) và "# 第N章 ..."
(Trung) để cắt bỏ phần tiêu đề/metadata trước khi đếm.
"""

import argparse
import re
import sys
from pathlib import Path

# Sửa lỗi encoding console trên Windows
if sys.platform == 'win32':
    import io
    sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8', errors='replace')
    sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8', errors='replace')

DEFAULT_MIN_WORDS = 3000
DEFAULT_MAX_WORDS = 6000
DEFAULT_PATTERN = 'Chương*.md'

STATUS_UNDER = 'DƯỚI NGƯỠNG'
STATUS_IN_RANGE = 'TRONG NGƯỠNG'
STATUS_OVER = 'VƯỢT NGƯỠNG'
STATUS_ERROR = 'error'


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
    """Đếm số TỪ: tách theo khoảng trắng sau khi bỏ Markdown."""
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


def classify_word_count(word_count: int, min_words: int, max_words: int) -> str:
    """Phân loại số từ theo ngưỡng min/max. Không có khái niệm 'đạt/thiếu'."""
    if word_count < min_words:
        return STATUS_UNDER
    if word_count > max_words:
        return STATUS_OVER
    return STATUS_IN_RANGE


def check_chapter(file_path: str, min_words: int = DEFAULT_MIN_WORDS, max_words: int = DEFAULT_MAX_WORDS) -> dict:
    """Kiểm tra số từ của một chương và trả về phân loại (không chặn build)."""
    path = Path(file_path)
    if not path.exists():
        return {
            'file': str(path),
            'exists': False,
            'word_count': 0,
            'status': STATUS_ERROR,
            'message': f'File không tồn tại: {file_path}',
        }

    try:
        main_content = extract_content_from_chapter(path)
    except (OSError, UnicodeDecodeError) as exc:
        return {
            'file': str(path),
            'exists': True,
            'word_count': 0,
            'status': STATUS_ERROR,
            'message': f'Lỗi đọc file: {exc}',
        }

    word_count = count_words(main_content)
    label = classify_word_count(word_count, min_words, max_words)

    return {
        'file': str(path),
        'exists': True,
        'word_count': word_count,
        'status': label,
        'message': f'Số từ: {word_count} ({label})',
    }


def check_all_chapters(
    directory: str,
    pattern: str = DEFAULT_PATTERN,
    min_words: int = DEFAULT_MIN_WORDS,
    max_words: int = DEFAULT_MAX_WORDS,
) -> list:
    """Kiểm tra mọi file chương khớp mẫu trong thư mục. Trả về None nếu thư mục không tồn tại (main dựa vào None để exit 1)."""
    dir_path = Path(directory)
    if not dir_path.exists():
        print(f'Lỗi: thư mục không tồn tại - {directory}')
        return None

    chapter_files = sorted(dir_path.glob(pattern))
    return [check_chapter(str(chapter_file), min_words, max_words) for chapter_file in chapter_files]


def print_results(results: list, min_words: int = DEFAULT_MIN_WORDS, max_words: int = DEFAULT_MAX_WORDS) -> None:
    """In báo cáo phân loại. Không in khuyến nghị 'viết thêm cho đủ'."""
    if not results:
        print('Không tìm thấy file chương')
        return

    total_words = 0
    counts = {STATUS_UNDER: 0, STATUS_IN_RANGE: 0, STATUS_OVER: 0, STATUS_ERROR: 0}

    print('\n' + '=' * 60)
    print(f'Báo cáo số từ chương (ngưỡng: {min_words}-{max_words} từ)')
    print('=' * 60)

    for result in results:
        if not result['exists'] or result['status'] == STATUS_ERROR:
            counts[STATUS_ERROR] += 1
            print(f'\n[LỖI] {result["file"]}')
            print(f'   {result["message"]}')
            continue

        total_words += result['word_count']
        counts[result['status']] += 1

        icon = {
            STATUS_UNDER: '[DƯỚI]',
            STATUS_IN_RANGE: '[OK]  ',
            STATUS_OVER: '[VƯỢT]',
        }[result['status']]

        print(f'\n{icon} {Path(result["file"]).name}')
        print(f'   {result["message"]}')

    print('\n' + '-' * 60)
    print(
        f'Tổng: {len(results)} chương | '
        f'{counts[STATUS_IN_RANGE]} trong ngưỡng | '
        f'{counts[STATUS_UNDER]} dưới ngưỡng | '
        f'{counts[STATUS_OVER]} vượt ngưỡng | '
        f'{counts[STATUS_ERROR]} lỗi | '
        f'tổng số từ: {total_words:,}'
    )
    print('-' * 60)

    if counts[STATUS_UNDER] > 0 or counts[STATUS_OVER] > 0:
        print(
            f'\nSố từ ngoài ngưỡng: xem lại nhịp chương, '
            f'không thêm chữ chỉ để đạt ngưỡng.'
        )


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog='check_chapter_wordcount.py',
        description=(
            'Kiểm tra số từ của chương và phân loại DƯỚI NGƯỠNG / TRONG NGƯỠNG / '
            'VƯỢT NGƯỠNG. Công cụ báo cáo, không chặn build (exit 0 trừ lỗi IO/parse).'
        ),
    )
    parser.add_argument(
        'target',
        help='Đường dẫn file chương (mặc định), hoặc thư mục khi dùng --all',
    )
    parser.add_argument(
        '--all',
        action='store_true',
        help='Kiểm tra mọi chương khớp --pattern trong thư mục TARGET, thay vì một file',
    )
    parser.add_argument(
        '--pattern',
        default=DEFAULT_PATTERN,
        help=f'Mẫu glob khi dùng --all (mặc định: {DEFAULT_PATTERN})',
    )
    parser.add_argument(
        '--min',
        dest='min_words',
        type=int,
        default=DEFAULT_MIN_WORDS,
        help=f'Ngưỡng dưới, số từ (mặc định: {DEFAULT_MIN_WORDS})',
    )
    parser.add_argument(
        '--max',
        dest='max_words',
        type=int,
        default=DEFAULT_MAX_WORDS,
        help=f'Ngưỡng trên, số từ (mặc định: {DEFAULT_MAX_WORDS})',
    )
    return parser


def main() -> None:
    parser = build_arg_parser()
    args = parser.parse_args()

    if args.all:
        results = check_all_chapters(
            args.target, pattern=args.pattern, min_words=args.min_words, max_words=args.max_words
        )
        if results is None:
            sys.exit(1)
        print_results(results, args.min_words, args.max_words)
        if any(r['status'] == STATUS_ERROR for r in results):
            sys.exit(1)
        return

    result = check_chapter(args.target, args.min_words, args.max_words)
    print_results([result], args.min_words, args.max_words)
    if result['status'] == STATUS_ERROR:
        sys.exit(1)


if __name__ == '__main__':
    main()
