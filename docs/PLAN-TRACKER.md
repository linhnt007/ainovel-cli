# Plan Tracker — Tổng hợp duy nhất

> **Dùng file này làm nguồn duy nhất.** AI chỉ cần đọc file này để biết toàn bộ trạng thái plan.
> Cập nhật: 2026-07-28

---

## Tổng quan

| Plan | File gốc | Trạng thái |
|------|-----------|------------|
| Audit codebase | `plan.md` | ✅ DONE — bug #1-#4 đã fix |
| Refactor flow | `refactor-flow-driven.md` | ✅ DONE — Host routing đã triển khai |
| Rate limit | `ratelimit-plan.md` | ✅ DONE — `internal/ratelimit/` đã có |
| Cải thiện flow | `improvement-plan.md` | ✅ DONE — 13/13 Part hoàn thành |
| LLM-first | `llm-first-plan.md` | 🔄 ĐANG CHẠY — 5/14 Part còn lại |

---

## Trạng thái chi tiết

### ✅ Đã hoàn thành

| Plan | Part | Mô tả | Bằng chứng code |
|------|------|--------|-----------------|
| audit | #1 | Prompt nén context restore.go Việt hoá | `restore.go` prompts tiếng Việt |
| audit | #2 | premise_structure.go headings Việt hoá | `premise_structure.go` headings tiếng Việt |
| audit | #3 | stylestat.go port sang tiếng Việt | `stylestat.go` regex Việt, `strings.Fields` |
| audit | #4 | Wordcount rune → từ | `checker.go:139` dùng `strings.Fields` |
| refactor | — | Host routing + flow router | `internal/host/flow/` hoàn chỉnh |
| ratelimit | T1-T7 | Rate limit đa-giới-hạn, rotation, global persist | `internal/ratelimit/` có đủ |
| improvement | A | Wordcount rune → từ | `drafts.go:81`, `checker.go:139` |
| improvement | B | rules.md.example Việt hoá | Không còn ký tự CJK |
| improvement | C | truncateJSONToTokens thông minh | `restore.go:190-224` parse JSON, drop element |
| improvement | D | wordcount.py bỏ padding advice | Chỉ classify DƯỚI/TRONG/VƯỢT |
| improvement | E | cocreate parser test | `cocreate_parse_test.go` 376 dòng, 9 test |
| improvement | F | Foreshadow aging + endgame | `agingThresholdFor(total)`, endgame 85% |
| improvement | G | Cocreate stage thêm đuôi chương | `cocreate_stage.go:85-96` |
| improvement | H | Router flat review + xoá FlowReviewing | `HasPendingFlatReview`, FlowReviewing xoá |
| improvement | I | Rewrite limit + aesthetic polish gate | `maxRewritePerChapter=3`, `aestheticPolishThreshold=70` |
| improvement | J | Budget per-chapter tracking | `chapterCosts`, `perChapterWarnFactor=3` |
| improvement | K | Cocreate persist + maxTokens 4096 | `WithMaxTokens(4096)`, session log |
| improvement | L | Narrative contract (POV/tense) | `save_foundation.go:42`, `NarrativeContract` struct |
| improvement | M | Voice card nhân vật | `CharacterVoiceCard` struct, `buildChapterVoiceCards` |
| llm-first | 0A | Model probe script | `scripts/modelprobe/main.go` |
| llm-first | 0C | Context profile cửa sổ nhỏ | `NewContextProfileForWindow`, `smallWindowTokens=32000` |
| llm-first | 0D | Human gate mỗi N chương | `HumanGateEvery`, `/gate` command, router integration |
| llm-first | A | Sampling extra_body theo role | `mergeExtraBody`, `RoleConfig.ExtraBody` |
| llm-first | C | Self-exemplar style anchor | `ExtractStyleAnchors` sort theo aesthetic score |
| llm-first | E (improvement) | Light check consistency | `CheckConsistencyTool` (read-only, per chapter) |

### 🔄 Đang chạy / Chưa hoàn thành

| Plan | Part | Mô tả | Trạng thái | File liên quan |
|------|------|--------|------------|----------------|
| llm-first | 0B | Structured output spike (grammar llama.cpp) | ⬜ CHƯA LÀM | `model-notes.md` cần spike results |
| llm-first | B | Stylestat Offenders + IntraChapterRepeats | ⬜ CHƯA LÀM | `stylestat.go`, `commit_chapter.go` |
| llm-first | D | save_chapter_check tool (light gate mới) | ⬜ CHƯA LÀM | `save_chapter_check.go` (mới) |
| llm-first | F | Chưng cất sổ tay tác giả (notebook) | ⬜ CHƯA LÀM | opt-in theo design mới |
| llm-first | G | Đa nháp + editor chọn (candidate/vote) | ⬜ CHƯA LÀM | cần fleet model |
| llm-first | H | K lượt chấm median/majority | ⬜ CHƯA LÀM | cần fleet model |

### Đã archive (không cần theo dõi)

| Plan | Lý do archive |
|------|---------------|
| `refactor-flow-driven.md` | Đã triển khai xong 2026-04-20 |
| `report.md` | Báo cáo audit cũ, đã dùng trong `plan.md` |
| `vi-ux-backlog.md` | Backlog UX, không liên quan flow chính |

---

## Thứ tự thực thi khuyến nghị

```
Ưu tiên CAO (đang chạy):
  llm-first 0B → B → D (3 part này độc lập, chạy song song được)

Ưu tiên TRUNG BÌNH (chờ fleet model):
  llm-first F → G → H (cần model probe 0A pass + nhiều model)

Ưu tiên THẤP (tối ưu sau):
  Tune ngưỡng từ kết quả thực tế
  Calib fatigue_words/anti-ai-tone từ output Việt thật
```

## Ma trận file × Part (tránh giẫm chân)

| File | 0B | B | D | F | G | H |
|------|----|----|---|---|---|---|
| `model-notes.md` | ✏️ | | | | | |
| `internal/stylestat/stylestat.go` | | ✏️ | | | | |
| `internal/tools/commit_chapter.go` | | ✏️ | | | | |
| `internal/tools/save_chapter_check.go` | | | ✏️ | | | |
| `internal/domain/review.go` | | | ✏️ | | | |
| `internal/store/world.go` | | | ✏️ | | | |
| `internal/tools/novel_context*.go` | | | | ✏️ | | |
| `internal/bootstrap/models.go` | | | | | ✏️ | ✏️ |

---

## Quick reference: 어디서 code gì

```
docs/
  plan.md                  ← audit bug #1-#4 (đã fix)
  improvement-plan.md      ← 13 Part flow/quality (đã xong)
  ratelimit-plan.md        ← rate limit (đã xong)
  llm-first-plan.md        ← 8 Part LLM optimization (đang chạy)
  PLAN-TRACKER.md          ← FILE NÀY

internal/
  ratelimit/               ← rate limit engine
  host/flow/               ← router + dispatcher + state
  tools/                   ← plan_chapter, commit_chapter, save_review, novel_context...
  stylestat/               ← style pattern detection
  store/                   ← drafts, world, progress, characters...
  agents/ctxpack/          ← context compaction + restore
  domain/                  ← ChapterPlan, NarrativeContract, CharacterVoiceCard...
```
