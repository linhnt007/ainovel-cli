# Kế hoạch sửa lỗi TUI Log (từ tui.log)

> ✅ **Cập nhật 2026-07-29:** Tất cả 7 bước đã được implement.  
> Xem commit `1dfcc0c`, `09e9212`, `2c90c3b`.

## Tổng quan

File `output/novel/logs/tui.log` ghi nhận **12 class lỗi**, trong đó 3 lỗi chí tử gây deadlock khiến writer max turn (30) → fail ở chương 2.

---

## 12 class lỗi chi tiết

| # | Dòng | Lỗi | Tần suất | Mức độ |
|---|------|-----|----------|--------|
| 1 | 103,119 | `finish_reason=MAX_TOKENS` | 2 | Cảnh báo |
| 2 | 128,348,385,405 | `commit_chapter: bản nháp đã thay đổi sau check_consistency` | 4 | **CAO** |
| 3 | 271,295,311,329,361 | `commit_chapter: forbidden_phrases: thực tế 1` | 5 | **NGHIÊM TRỌNG** |
| 4 | 299,335,366 | `edit_chapter: could not find the exact text` | 3 | **CAO** |
| 5 | 227 | `save_review: scope is missing` | 1 | TRUNG BÌNH |
| 6 | 149,164,426 | `read_chapter: chapter is required` | 3 | THẤP |
| 7 | 62 | `save_foundation expand_arc: tool args invalid` | 1 | THẤP |
| 8 | 394,399 | `ratelimit: mọi model chạm giới hạn` | 2 | TRUNG BÌNH |
| 9 | 10,19,66 | `LLM usage data missing` | 3 | THẤP (đã biết) |
| 10 | 3,15,35,398 | `stop_guard chặn end_turn` | 4 | THÔNG TIN |
| 11 | 414 | `writer max turns (30) reached` | 1 | **NGHIÊM TRỌNG** (hậu quả) |
| 12 | 7,18,28,63 | User pause → context canceled | 4 | DỰ KIẾN |

---

## Deadlock flow (nguyên nhân gốc rễ)

```
draft_chapter
  → commit (forbidden_phrases: "thực tế" xuất hiện 1 lần)
  → edit_chapter (fail: old_string không match whitespace chính xác)
  → draft_chapter (LLM viết lại toàn bộ)
  → commit (forbidden_phrases vẫn còn)
  → edit_chapter (fail tiếp)
  → draft_chapter (LLM thử lại, hết turn)
  → max turns (30) reached → writer crash
```

Toàn bộ chương 2 không thể commit được dù đã thử 10+ lần.

---

## Kế hoạch sửa (7 bước)

### P1 — [CRITICAL] forbidden_phrases không block commit — ✅ DONE

**Hiện tại:** `forbidden_phrases` là error → commit bị chặn cứng.
**Sửa:** Giữ nguyên flag `forbidden_phrases` trong `rule_violations` (LLM vẫn thấy cảnh báo) nhưng commit không reject vì nó. Chuyển thành warning như `chapter_words`.

File: `internal/tools/commit_chapter.go:384`

**Code:**
```go
if v.Severity == rules.SeverityError && v.Rule != "forbidden_phrases" {
```

Hậu quả: LLM vẫn thấy `rule_violations` → có thể tự sửa lần sau. Nhưng không kẹt loop.

---

### P1b — [CRITICAL] Auto-strip forbidden_phrases trước commit

**Nếu P1 chưa đủ**, tự động loại bỏ phrase cấm khỏi draft content trước khi commit.

File: `internal/store/drafts.go` (hoặc `internal/tools/commit_chapter.go`)

**Cách làm:** Khi commit, nếu draft chứa forbidden_phrases, tự động remove chúng, log warning, rồi commit bản đã lọc.

---

### P2 — [HIGH] edit_chapter: normalize whitespace khi match — ✅ DONE

**Hiện tại:** edit_chapter yêu cầu old_string match tuyệt đối (whitespace + newlines).
**Sửa:** Trim space, collapse newlines, normalize Unicode trước so sánh.

File: `internal/tools/edit_chapter.go:102-105,143-154`

**Code:**
```go
func normalizeEditWhitespace(s string) string {
    s = strings.ReplaceAll(s, "\r\n", "\n")
    s = strings.ReplaceAll(s, "\t", " ")
    lines := strings.Split(s, "\n")
    for i, line := range lines {
        lines[i] = strings.Join(strings.Fields(line), " ")
    }
    return strings.Join(lines, "\n")
}
```

---

### P3 — [HIGH] commit_chapter auto re-check consistency — ✅ DONE

**Hiện tại:** commit reject nếu draft thay đổi sau `check_consistency` cuối.
**Sửa:** Tự động chạy `check_consistency` lại tại thời điểm commit, thay vì reject.

File: `internal/tools/commit_chapter.go:423-441`

**Cách làm:** Trong `commitOutput`, nếu draft checksum khác với checksum lúc check_consistency cuối → tự động append checkpoint consistency_check mới, không reject.

---

### P4 — [MEDIUM] save_review: scope mặc định — ✅ DONE

**Hiện tại:** `scope` là required param → LLM thiếu → error.
**Sửa:** Nếu scope rỗng, mặc định `"chapter"`.

File: `internal/tools/save_review.go:265-266`

**Code:**
```go
if strings.TrimSpace(r.Scope) == "" {
    r.Scope = "chapter"
}
```

---

### P5 — [LOW] read_chapter: fallback chapter — ✅ DONE

**Hiện tại:** read_chapter không chapter → "chapter is required".
**Sửa:** Nếu không chapter/from/to, mặc định đọc chương 1.

File: `internal/tools/read_chapter.go:100-103`

**Code:**
```go
if a.Chapter <= 0 && a.From <= 0 && a.To <= 0 {
    a.Chapter = 1
}
```

---

### P6 — [LOW] save_foundation expand_arc: default params — ✅ DONE

**Hiện tại:** expand_arc yêu cầu `volume` + `arc` → LLM thiếu → error.
**Sửa:** Mặc định volume=1, arc=1 nếu thiếu.

File: `internal/tools/save_foundation.go:176-182`

**Code:**
```go
if a.Volume <= 0 { a.Volume = 1 }
if a.Arc <= 0    { a.Arc = 1 }
```

---

### P7 — [MEDIUM] Tăng max_turns writer 30→50 — ✅ DONE

**Hiện tại:** Writer giới hạn 30 turns → chương phức tạp hết turn → fail.
**Sửa:** Tăng 30 → 50.

File: `internal/agents/build.go:250`

**Code:**
```go
MaxTurns: 50,
```

File: `internal/agents/build.go`

**Cách làm:**
```go
// Tìm MaxTurnsPerRun hoặc tương đương, đổi 30 → 50
```

---

## Thứ tự ưu tiên triển khai

1. **P1** ✅ (forbidden_phrases không block commit)
2. **P3** ✅ (commit auto re-check consistency)
3. **P2** ✅ (edit_chapter normalize whitespace)
4. **P7** ✅ (tăng max_turns)
5. **P4** ✅ (save_review scope default)
6. **P5** ✅ (read_chapter fallback)
7. **P6** ✅ (expand_arc default params)

> **Tất cả 7 bước đã implement. Không còn action required.**
