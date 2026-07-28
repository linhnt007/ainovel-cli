package tools

import (
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/stylestat"
)

type contextBuildState struct {
	chapter         int
	profile         domain.ContextProfile
	progress        *domain.Progress
	runMeta         *domain.RunMeta
	currentEntry    *domain.OutlineEntry
	chapterPlan     *domain.ChapterPlan
	storyThreads    []domain.RecallItem
	foreshadow      []domain.ForeshadowEntry
	relationships   []domain.RelationshipEntry
	allStateChanges []domain.StateChange
	styleRules      *domain.WritingStyleRules
	priorIssues     []domain.ConsistencyIssue
}

type chapterContextEnvelope struct {
	Working    map[string]any
	Episodic   map[string]any
	References map[string]any
	Selected   map[string]any
}

type architectContextEnvelope struct {
	Planning   map[string]any
	Foundation map[string]any
	References map[string]any
}

func newChapterContextEnvelope() chapterContextEnvelope {
	return chapterContextEnvelope{
		Working:    make(map[string]any),
		Episodic:   make(map[string]any),
		References: make(map[string]any),
		Selected:   make(map[string]any),
	}
}

func newArchitectContextEnvelope() architectContextEnvelope {
	return architectContextEnvelope{
		Planning:   make(map[string]any),
		Foundation: make(map[string]any),
		References: make(map[string]any),
	}
}

func (e chapterContextEnvelope) apply(result map[string]any) {
	// Gộp thay vì ghi đè: luồng chương của Execute sẽ apply hai phong bì liên tiếp (seed + buildChapterContext),
	// nếu gán toàn bộ thì lần apply thứ hai sẽ xóa nội dung container của seed, các canonical path
	// như working_memory.* sẽ thành con trỏ trỏ vào không khí, mô hình chỉ còn dựa vào bản sao
	// ở tầng trên để xử lý mơ hồ.
	mergeEnvelopeSection(result, "working_memory", e.Working)
	mergeEnvelopeSection(result, "episodic_memory", e.Episodic)
	mergeEnvelopeSection(result, "reference_pack", e.References)
	if len(e.Selected) > 0 {
		mergeEnvelopeSection(result, "selected_memory", e.Selected)
	}
	mergeContextSection(result, e.Working)
	mergeContextSection(result, e.Episodic)
	mergeContextSection(result, e.References)
}

// mergeEnvelopeSection gộp section vào container hiện có của result[key]; nếu container chưa tồn tại thì gán trực tiếp.
func mergeEnvelopeSection(result map[string]any, key string, section map[string]any) {
	if existing, ok := result[key].(map[string]any); ok {
		for k, v := range section {
			existing[k] = v
		}
		return
	}
	result[key] = section
}

func (e architectContextEnvelope) apply(result map[string]any) {
	result["planning_memory"] = e.Planning
	result["foundation_memory"] = e.Foundation
	result["reference_pack"] = e.References
	mergeContextSection(result, e.Planning)
	mergeContextSection(result, e.Foundation)
	mergeContextSection(result, e.References)
}

func mergeContextSection(result map[string]any, section map[string]any) {
	for key, value := range section {
		result[key] = value
	}
}

// buildProgressStatus chỉ trả về tóm tắt tiến độ khi Điều phối viên gọi (không truyền chapter),
// Người viết không cần thông tin này, tránh gây nhiễu trong quá trình viết.
func (t *ContextTool) buildProgressStatus(result map[string]any) {
	progress, err := t.store.Progress.Load()
	if err != nil || progress == nil {
		return
	}
	status := map[string]any{
		"phase":              string(progress.Phase),
		"flow":               string(progress.Flow),
		"completed_chapters": len(progress.CompletedChapters),
		"total_chapters":     progress.TotalChapters,
		"next_chapter":       progress.NextChapter(),
		"total_word_count":   progress.TotalWordCount,
	}
	if progress.InProgressChapter > 0 {
		status["in_progress_chapter"] = progress.InProgressChapter
	}
	if len(progress.PendingRewrites) > 0 {
		status["pending_rewrites"] = progress.PendingRewrites
		status["rewrite_reason"] = progress.RewriteReason
	}
	if progress.Layered {
		status["layered"] = true
		status["current_volume"] = progress.CurrentVolume
		status["current_arc"] = progress.CurrentArc
	}
	if progress.Phase == domain.PhaseComplete {
		status["finished"] = true
	}
	result["progress_status"] = status
}

// buildUserRules tiêm Bundle đã gộp vào working_memory.user_rules (canonical path).
//
// Tiêm một điểm duy nhất: dù Người viết / Biên tập viên / Kiến trúc sư / Điều phối viên gọi novel_context
// theo luồng nào, đều lấy được preferences nhất quán tại working_memory.user_rules.
// Luồng architect ban đầu không có working_memory, hàm này sẽ tạo mới theo nhu cầu (chỉ chứa user_rules);
// luồng chapter > 0 đã có working_memory sẵn, gán trực tiếp vào đó.
//
// Dù Bundle rỗng vẫn tiêm, giữ ổn định trường dữ liệu, tránh LLM gặp user_rules=null rồi rẽ nhánh bất thường.
//
// Chiến lược tiêm: chỉ cho LLM thấy structured + preferences — đây mới là hai mục cần tuân theo khi sáng tác.
// sources / conflicts là thông tin chẩn đoán (để người dùng tự debug xung đột), không đưa vào LLM;
// CLI sẽ hiển thị chúng trên panel chẩn đoán khi cần.
func (t *ContextTool) buildUserRules(result map[string]any) {
	bundle := rules.Merge(rules.Load(t.rulesOpts))
	payload := map[string]any{
		"structured":  bundle.Structured,
		"preferences": bundle.Preferences,
	}
	working, ok := result["working_memory"].(map[string]any)
	if !ok {
		working = map[string]any{}
		result["working_memory"] = working
	}
	working["user_rules"] = payload
}

// buildUserDirectives tiêm yêu cầu sáng tác dài hạn của người dùng vào working_memory.user_directives (canonical path).
//
// Cũng là tiêm một điểm duy nhất như buildUserRules: dù Người viết / Biên tập viên / Kiến trúc sư /
// Điều phối viên đi theo luồng nào đều lấy được danh sách nhất quán. Danh sách rỗng cũng tiêm [],
// giữ ổn định trường dữ liệu (theo tiền lệ của user_rules),
// đồng thời để bài kiểm tra tính nhất quán của con trỏ prompt có thể phân tích tự nhiên.
// Hình dạng từng mục xem tại directiveFacts.
func (t *ContextTool) buildUserDirectives(result map[string]any, warn func(string, error)) {
	list, err := t.store.Directives.Load()
	if err != nil {
		warn("user_directives", err)
		return
	}
	working, ok := result["working_memory"].(map[string]any)
	if !ok {
		working = map[string]any{}
		result["working_memory"] = working
	}
	working["user_directives"] = directiveFacts(list)
}

func (t *ContextTool) buildSimulationProfile(result map[string]any, sectionKey string, warn func(string, error)) {
	profile, err := t.store.Simulation.Load()
	if err != nil {
		warn("simulation_profile", err)
		return
	}
	compact := domain.CompactSimulationProfile(profile)
	if compact == nil {
		return
	}
	section, ok := result[sectionKey].(map[string]any)
	if !ok {
		section = map[string]any{}
		result[sectionKey] = section
	}
	section["simulation_profile"] = compact
	result["simulation_profile"] = true
}

func (t *ContextTool) buildBaseContext(result map[string]any, warn func(string, error)) {
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		result["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			result["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		}
		result["premise_structure"] = premiseStructure(premise, tier)
	} else {
		warn("premise", err)
	}
	if outline, err := t.store.Outline.LoadOutline(); err == nil && outline != nil {
		result["outline"] = outline
	} else {
		warn("outline", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		result["world_rules"] = rules
	} else {
		warn("world_rules", err)
	}
}

func (t *ContextTool) prepareChapterContext(chapter int, envelope *chapterContextEnvelope, warn func(string, error)) contextBuildState {
	state := contextBuildState{
		chapter: chapter,
		profile: domain.NewContextProfile(0),
	}

	progress, err := t.store.Progress.Load()
	warn("progress", err)
	runMeta, err := t.store.RunMeta.Load()
	warn("run_meta", err)
	state.progress = progress
	state.runMeta = runMeta

	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Episodic["planning_tier"] = runMeta.PlanningTier
	}
	if progress != nil && progress.TotalChapters > 0 {
		state.profile = domain.NewContextProfileForWindow(progress.TotalChapters, t.WriterContextWindow)
	}
	if progress == nil || !progress.Layered {
		state.profile.Layered = false
	}

	currentEntry, currentEntryErr := t.store.Outline.GetChapterOutline(chapter)
	if currentEntryErr == nil {
		envelope.Working["current_chapter_outline"] = currentEntry
	} else {
		warn("current_chapter_outline", currentEntryErr)
	}
	state.currentEntry = currentEntry

	chapterPlan, chapterPlanErr := t.store.Drafts.LoadChapterPlan(chapter)
	if chapterPlanErr == nil && chapterPlan != nil {
		envelope.Working["chapter_plan"] = chapterPlan
		if len(chapterPlan.Contract.RequiredBeats) > 0 ||
			len(chapterPlan.Contract.ForbiddenMoves) > 0 ||
			len(chapterPlan.Contract.ContinuityChecks) > 0 ||
			len(chapterPlan.Contract.EvaluationFocus) > 0 ||
			chapterPlan.Contract.EmotionTarget != "" ||
			len(chapterPlan.Contract.PayoffPoints) > 0 ||
			chapterPlan.Contract.HookGoal != "" {
			envelope.Working["chapter_contract"] = chapterPlan.Contract
		}
	} else {
		warn("chapter_plan", chapterPlanErr)
	}
	state.chapterPlan = chapterPlan

	// Xác định có đang viết lại chương này không: quyết định novel_context có bổ sung "sự kiện dành riêng cho viết lại" hay không.
	isRewrite := progress != nil && slices.Contains(progress.PendingRewrites, chapter)

	// Phơi bày thực tế bản nháp đã tồn tại chưa: để Người viết khi được tái phân công có thể tự quyết
	// bỏ qua viết lại hay ghi đè. Chỉ phơi bày exists + word_count, không tiêm nội dung
	// (nội dung để Người viết dùng read_chapter kéo về khi cần).
	if _, draftWords, draftErr := t.store.Drafts.LoadChapterContent(chapter); draftErr == nil && draftWords > 0 {
		envelope.Working["chapter_draft"] = map[string]any{
			"exists":     true,
			"word_count": draftWords,
		}
	} else if draftErr != nil {
		warn("chapter_draft", draftErr)
	}

	// Khi viết lại, chuyển "lý do thay đổi + vị trí cần sửa" cho Người viết: lý do lấy từ hàng đợi
	// làm lại, phê bình cụ thể lấy từ đánh giá chương đó (selectReviewLessons chỉ thu hồi
	// chapter-1..chapter-3, vừa đúng bỏ sót bản thân chương hiện tại, mà Người viết cũng
	// không có công cụ đọc đánh giá). Nội dung không tiêm ở đây — giữ nguyên quy ước
	// "nội dung kéo về bằng read_chapter khi cần".
	if isRewrite {
		brief := map[string]any{"reason": progress.RewriteReason}
		if review, reviewErr := t.store.World.LoadReview(chapter); reviewErr == nil && review != nil {
			if review.Summary != "" {
				brief["review_summary"] = review.Summary
			}
			if len(review.Issues) > 0 {
				brief["issues"] = review.Issues
			}
			if len(review.ContractMisses) > 0 {
				brief["contract_misses"] = review.ContractMisses
			}
		} else if reviewErr != nil {
			warn("rewrite_review", reviewErr)
		}

		var priorIssues []domain.ConsistencyIssue
		for _, chap := range []int{chapter - 1, chapter - 2, chapter - 3} {
			if chap < 1 {
				continue
			}
			if rev, err := t.store.World.LoadReview(chap); err == nil && rev != nil {
				for _, iss := range rev.Issues {
					if iss.Severity == "error" || iss.Severity == "critical" {
						priorIssues = append(priorIssues, iss)
						if len(priorIssues) >= 10 {
							break
						}
					}
				}
			}
			if len(priorIssues) >= 10 {
				break
			}
		}
		if len(priorIssues) > 0 {
			brief["prior_issues"] = priorIssues
			state.priorIssues = priorIssues
		}

		envelope.Working["rewrite_brief"] = brief
	}

	foreshadow, foreshadowErr := t.store.World.LoadActiveForeshadow()
	warn("foreshadow_ledger", foreshadowErr)
	state.foreshadow = foreshadow

	relationships, relErr := t.store.World.LoadRelationships()
	warn("relationship_state", relErr)
	if len(relationships) > 0 {
		envelope.Episodic["relationship_state"] = relationships
	}
	state.relationships = relationships

	allStateChanges, scErr := t.store.World.LoadStateChanges()
	warn("recent_state_changes", scErr)
	state.allStateChanges = allStateChanges
	if len(allStateChanges) > 0 {
		start := max(chapter-2, 1)
		var recent []domain.StateChange
		for _, c := range allStateChanges {
			if c.Chapter >= start && c.Chapter < chapter {
				recent = append(recent, c)
			}
		}
		if len(recent) > 0 {
			envelope.Episodic["recent_state_changes"] = recent
		}
	}

	styleRules, styleErr := t.store.World.LoadStyleRules()
	warn("style_rules", styleErr)
	state.styleRules = styleRules
	// selectStoryThreads đã tự lo fallback thưa (giữ tuyến treo lâu, chỉ bỏ khi CHỈ có gợi nhớ liên quan
	// mà dưới ngưỡng), nên ở đây không lọc thêm — nếu lọc theo số lượng sẽ nuốt mất tuyến treo lâu đơn lẻ.
	state.storyThreads = t.selectStoryThreads(state)

	return state
}

func (t *ContextTool) buildChapterContext(result map[string]any, state contextBuildState, warn func(string, error)) {
	envelope := newChapterContextEnvelope()
	result["memory_policy"] = domain.NewChapterMemoryPolicy(state.progress, state.profile, state.currentEntry != nil)

	if state.profile.Layered {
		t.loadLayeredCharacters(envelope.Episodic, state.chapter, warn)
	} else {
		t.loadFilteredCharacters(envelope.Episodic, state.chapter, warn)
	}

	t.buildChapterEpisodicMemory(&envelope, state, warn)
	t.buildChapterWorkingMemory(&envelope, state, warn)
	t.buildChapterVoiceCards(&envelope, state, warn)
	t.buildChapterReferencePack(&envelope, state)
	t.buildChapterSelectedMemory(&envelope, state, warn)
	t.buildStyleStats(&envelope, state)
	envelope.apply(result)
}

// buildStyleStats thống kê phong cách toàn tập ở tất cả các chương đã hoàn thành,
// tiêm vào episodic_memory.style_stats.
// Cửa sổ đánh giá trong cung truyện tự nhiên mù với "tic câu xuất hiện vài chục lần mỗi chương,
// cấu trúc cuối chương đồng dạng, lặp từ xuyên chương" — chỉ thống kê toàn tập mới phơi bày được.
// Thống kê do code xử lý (tính xác định), phán quyết do LLM đảm nhận (Biên tập viên chấm điểm
// theo con số ở chiều aesthetic, Người viết dựa đó tự tránh). Khi chưa đủ chương, stylestat
// trả về nil, không tiêm.
func (t *ContextTool) buildStyleStats(envelope *chapterContextEnvelope, state contextBuildState) {
	if state.progress == nil || len(state.progress.CompletedChapters) == 0 {
		return
	}
	completed := slices.Clone(state.progress.CompletedChapters)
	slices.Sort(completed)
	chapters := make([]string, 0, len(completed))
	for _, ch := range completed {
		// Bỏ qua nếu đọc một chương riêng lẻ thất bại: thống kê là sự kiện best-effort, không vì thiếu một chương mà từ bỏ toàn cục
		if text, err := t.store.Drafts.LoadChapterText(ch); err == nil && text != "" {
			chapters = append(chapters, text)
		}
	}

	var titles []string
	if outline, err := t.store.Outline.LoadOutline(); err == nil {
		for _, entry := range outline {
			titles = append(titles, entry.Title)
		}
	}

	stats := stylestat.Compute(stylestat.Input{
		Chapters:  chapters,
		Titles:    titles,
		Stopwords: t.styleStopwords(),
	})
	if stats == nil {
		return
	}
	envelope.Episodic["style_stats"] = stats
}

// styleStopwords thu thập tên nhân vật và bí danh để lọc khi khai thác cụm từ — tên xuất hiện tự nhiên có tần suất cao, không phải vấn đề phong cách.
func (t *ContextTool) styleStopwords() []string {
	var words []string
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			words = append(words, c.Name)
			words = append(words, c.Aliases...)
		}
	}
	if cast, err := t.store.Cast.RecentActive(50); err == nil {
		for _, e := range cast {
			words = append(words, e.Name)
			words = append(words, e.Aliases...)
		}
	}
	return words
}

func (t *ContextTool) buildChapterWorkingMemory(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	// narrative_contract: chuẩn ngôi kể/thì khai ở foundation, tiêm mỗi chương (vài chục token) làm chuẩn cứng
	// cho Người viết chống trôi POV/thì và làm mốc đối chiếu continuity cho Biên tập viên (editor chiều 7).
	// Foundation cũ thiếu trường → LoadNarrative trả nil → IsEmpty=true → bỏ qua êm, không tiêm.
	if narrative, err := t.store.Outline.LoadNarrative(); err == nil {
		if !narrative.IsEmpty() {
			envelope.Working["narrative_contract"] = narrative
		}
	} else {
		warn("narrative_contract", err)
	}

	if next, err := t.store.Outline.GetChapterOutline(state.chapter + 1); err == nil && next != nil {
		envelope.Working["next_chapter_outline"] = next
	}

	if state.profile.Layered {
		t.loadLayeredSummaries(envelope.Working, state.chapter, state.profile.SummaryWindow, warn)
	} else {
		if summaries, err := t.store.Summaries.LoadRecentSummaries(state.chapter, state.profile.SummaryWindow); err == nil && len(summaries) > 0 {
			envelope.Working["recent_summaries"] = summaries
		} else {
			warn("recent_summaries", err)
		}
	}

	if timeline, err := t.store.World.LoadRecentTimeline(state.chapter, state.profile.TimelineWindow); err == nil && len(timeline) > 0 {
		envelope.Working["timeline"] = timeline
	} else {
		warn("timeline", err)
	}

	if state.progress != nil {
		checkpoint := map[string]any{
			"in_progress_chapter": state.progress.InProgressChapter,
		}
		if len(state.progress.StrandHistory) > 0 {
			checkpoint["strand_history"] = state.progress.StrandHistory
		}
		if len(state.progress.HookHistory) > 0 {
			checkpoint["hook_history"] = state.progress.HookHistory
		}
		envelope.Working["checkpoint"] = checkpoint
	}

	if state.chapter > 1 {
		if prevText, err := t.store.Drafts.LoadChapterText(state.chapter - 1); err == nil && prevText != "" {
			runes := []rune(prevText)
			limit := 800
			if state.profile.Compact {
				limit = 400
			}
			if len(runes) > limit {
				runes = runes[len(runes)-limit:]
			}
			envelope.Working["previous_tail"] = string(runes)
		}
	}

	if len(state.priorIssues) > 0 {
		envelope.Working["prior_issues"] = state.priorIssues
	}
}

// buildChapterVoiceCards nạp voice card của các nhân vật XUẤT HIỆN trong chương (khớp theo chapter_plan,
// fallback sang đề cương chương khi chưa có plan) vào working_memory.voice_cards.
//
// Chỉ nạp nhân vật của chương — KHÔNG inject toàn dàn — vì hai lý do:
//   - chống phình context (voice card chỉ tốn vài trăm token khi giới hạn ở cast của chương);
//   - chống đồng nhất hoá giọng đối thoại: Người viết cần card của đúng nhân vật đang nói trong chương này,
//     recent_cast chỉ cứu nhân vật phụ nên chính diện vẫn cần đường nạp riêng.
//
// Chỉ nhân vật core/important mới có voice (do architect sinh). Nhân vật cũ / tier thấp thiếu voice
// → IsEmpty=true → bỏ qua êm, không tạo mục rỗng. Không có nhân vật nào khớp → không tiêm key.
func (t *ContextTool) buildChapterVoiceCards(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	chars, err := t.store.Characters.Load()
	if err != nil {
		warn("voice_cards", err)
		return
	}
	if len(chars) == 0 {
		return
	}

	// Ghép văn bản mô tả chương để khớp tên/bí danh nhân vật. Ưu tiên chapter_plan (Người viết tự lập, mô tả
	// đúng nhân vật của chương); khi chưa có plan thì fallback sang đề cương chương để voice vẫn nạp được
	// ở các luồng chưa kịp lập plan. Không có văn bản nào → không suy ra được cast → không tiêm.
	matchText := chapterPlanMatchText(state.chapterPlan)
	if matchText == "" && state.currentEntry != nil {
		matchText = outlineEntryMatchText(state.currentEntry)
	}
	if matchText == "" {
		return
	}

	var cards []map[string]any
	for _, c := range chars {
		if c.Voice.IsEmpty() {
			continue // nhân vật thiếu voice (tier thấp hoặc nhân vật cũ) → bỏ qua êm
		}
		if !matchCharacter(matchText, c) {
			continue // chỉ nhân vật xuất hiện trong chương
		}
		card := map[string]any{"character": c.Name}
		if len(c.Voice.Catchphrases) > 0 {
			card["catchphrases"] = c.Voice.Catchphrases
		}
		if c.Voice.SentenceStyle != "" {
			card["sentence_style"] = c.Voice.SentenceStyle
		}
		if c.Voice.SubtextLevel != "" {
			card["subtext_level"] = c.Voice.SubtextLevel
		}
		if c.Voice.Taboo != "" {
			card["taboo"] = c.Voice.Taboo
		}
		cards = append(cards, card)
	}
	if len(cards) > 0 {
		envelope.Working["voice_cards"] = cards
	}
}

// chapterPlanMatchText ghép các trường văn bản của chapter_plan để khớp tên nhân vật của chương.
// plan nil → chuỗi rỗng (để buildChapterVoiceCards fallback sang đề cương chương).
func chapterPlanMatchText(plan *domain.ChapterPlan) string {
	if plan == nil {
		return ""
	}
	parts := []string{plan.Title, plan.Goal, plan.Conflict, plan.Hook, plan.EmotionArc, plan.Notes}
	parts = append(parts, plan.Contract.RequiredBeats...)
	parts = append(parts, plan.Contract.ForbiddenMoves...)
	parts = append(parts, plan.Contract.ContinuityChecks...)
	parts = append(parts, plan.Contract.PayoffPoints...)
	return strings.Join(parts, " ")
}

// outlineEntryMatchText ghép các trường văn bản của đề cương chương (dùng khi chưa có chapter_plan).
func outlineEntryMatchText(entry *domain.OutlineEntry) string {
	if entry == nil {
		return ""
	}
	return strings.Join(append([]string{entry.Title, entry.CoreEvent, entry.Hook}, entry.Scenes...), " ")
}

func (t *ContextTool) buildChapterSelectedMemory(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	if len(state.storyThreads) > 0 {
		envelope.Selected["story_threads"] = state.storyThreads
	}
	if lessons := t.selectReviewLessons(state.chapter, warn); len(lessons) > 0 {
		envelope.Selected["review_lessons"] = lessons
	}
}

func (t *ContextTool) buildChapterEpisodicMemory(envelope *chapterContextEnvelope, state contextBuildState, warn func(string, error)) {
	if len(state.foreshadow) > 0 && len(state.storyThreads) == 0 {
		envelope.Episodic["foreshadow_ledger"] = state.foreshadow
	}

	// Danh sách nhân vật phụ: thu hồi các nhân vật phụ hoạt động gần đây, giúp Người viết
	// giữ nhất quán giọng điệu/vai trò khi đưa nhân vật cũ trở lại.
	// Không thu hồi toàn bộ (truyện dài sẽ phình to), chỉ lấy N nhân vật hoạt động gần nhất,
	// sắp xếp giảm dần theo LastSeenChapter.
	if recentCast, err := t.store.Cast.RecentActive(15); err == nil && len(recentCast) > 0 {
		simplified := make([]map[string]any, 0, len(recentCast))
		for _, e := range recentCast {
			item := map[string]any{
				"name":             e.Name,
				"first_seen":       e.FirstSeenChapter,
				"last_seen":        e.LastSeenChapter,
				"appearance_count": e.AppearanceCount,
			}
			if e.BriefRole != "" {
				item["brief_role"] = e.BriefRole
			}
			if len(e.Aliases) > 0 {
				item["aliases"] = e.Aliases
			}
			simplified = append(simplified, item)
		}
		envelope.Episodic["recent_cast"] = simplified
	} else if err != nil {
		warn("recent_cast", err)
	}

	if state.progress != nil && state.progress.TotalChapters > 30 && state.currentEntry != nil {
		if related := t.buildRelatedChapters(
			state.chapter,
			state.currentEntry,
			state.foreshadow,
			state.relationships,
			state.allStateChanges,
		); len(related) > 0 {
			envelope.Episodic["related_chapters"] = related
		}
	}

	if state.profile.Layered && state.progress != nil {
		pos := map[string]any{
			"volume": state.progress.CurrentVolume,
			"arc":    state.progress.CurrentArc,
		}
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			globalCh := 1
			for _, v := range volumes {
				if v.Index == state.progress.CurrentVolume {
					pos["volume_title"] = v.Title
					pos["volume_theme"] = v.Theme
				}
				for _, arc := range v.Arcs {
					if v.Index == state.progress.CurrentVolume && arc.Index == state.progress.CurrentArc {
						pos["arc_title"] = arc.Title
						pos["arc_goal"] = arc.Goal
						if n := len(arc.Chapters); n > 0 {
							pos["arc_total_chapters"] = n
							pos["arc_chapter_index"] = state.chapter - globalCh + 1
						}
					}
					globalCh += len(arc.Chapters)
				}
			}
		} else {
			warn("layered_outline", err)
		}
		envelope.Episodic["position"] = pos
	}
}

func (t *ContextTool) buildChapterReferencePack(envelope *chapterContextEnvelope, state contextBuildState) {
	if state.styleRules != nil {
		envelope.References["style_rules"] = state.styleRules
	} else {
		var maxCompleted int
		if state.progress != nil {
			maxCompleted = maxCompletedChapter(state.progress.CompletedChapters)
		}
		anchorLimit := 3
		if state.profile.Compact {
			anchorLimit = 2
		}
		if anchors := t.store.Drafts.ExtractStyleAnchors(anchorLimit, maxCompleted); len(anchors) > 0 {
			envelope.References["style_anchors"] = anchors
		}

		if state.currentEntry != nil {
			var voiceSamples []map[string]any
			chars, _ := t.store.Characters.Load()
			for _, c := range chars {
				if c.Tier == "secondary" || c.Tier == "decorative" {
					continue
				}
				samples := t.store.Drafts.ExtractDialogue(c.Name, c.Aliases, 3, maxCompleted)
				if len(samples) > 0 {
					voiceSamples = append(voiceSamples, map[string]any{
						"character": c.Name,
						"samples":   samples,
					})
				}
				if len(voiceSamples) >= 5 {
					break
				}
			}
			if len(voiceSamples) > 0 {
				envelope.References["voice_samples"] = voiceSamples
			}
		}
	}

	envelope.References["references"] = t.writerReferences(state.chapter, state.profile.Compact)
}

func (t *ContextTool) buildArchitectContext(result map[string]any, warn func(string, error)) {
	envelope := newArchitectContextEnvelope()
	result["memory_policy"] = domain.NewArchitectMemoryPolicy()
	t.buildArchitectPlanning(&envelope, warn)
	t.buildArchitectFoundation(&envelope, warn)
	t.buildArchitectReferences(&envelope, warn)
	envelope.apply(result)
}

func (t *ContextTool) buildArchitectPlanning(envelope *architectContextEnvelope, warn func(string, error)) {
	runMeta, err := t.store.RunMeta.Load()
	warn("run_meta", err)
	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Planning["planning_tier"] = runMeta.PlanningTier
	}

	var layered []domain.VolumeOutline
	if l, err := t.store.Outline.LoadLayeredOutline(); err == nil && len(l) > 0 {
		layered = l
		envelope.Planning["layered_outline"] = layered
		var skeletonArcs []map[string]any
		for _, v := range layered {
			for _, a := range v.Arcs {
				if !a.IsExpanded() {
					skeletonArcs = append(skeletonArcs, map[string]any{
						"volume":             v.Index,
						"arc":                a.Index,
						"title":              a.Title,
						"goal":               a.Goal,
						"estimated_chapters": a.EstimatedChapters,
					})
				}
			}
		}
		if len(skeletonArcs) > 0 {
			envelope.Planning["skeleton_arcs"] = skeletonArcs
		}
	} else {
		warn("layered_outline", err)
	}

	var compass *domain.StoryCompass
	if c, err := t.store.Outline.LoadCompass(); err == nil && c != nil {
		compass = c
		envelope.Planning["compass"] = compass
	} else {
		warn("compass", err)
	}
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		envelope.Planning["volume_summaries"] = volSummaries
	} else {
		warn("volume_summaries", err)
	}

	// completion_signals tập trung các sự kiện then chốt về "toàn tập đã nên kết thúc chưa",
	// giúp Kiến trúc sư nhìn thấy bức tranh đối chiếu ngay khi phán quyết complete_book / append_volume.
	// Nếu để rải rác trong progress / compass / foreshadow / layered_outline, LLM dễ bỏ sót khi tự tổng hợp.
	envelope.Planning["completion_signals"] = t.completionSignals(layered, compass)
}

func (t *ContextTool) completionSignals(layered []domain.VolumeOutline, compass *domain.StoryCompass) map[string]any {
	signals := map[string]any{}
	if progress, _ := t.store.Progress.Load(); progress != nil {
		signals["completed_chapters"] = len(progress.CompletedChapters)
		signals["total_word_count"] = progress.TotalWordCount
		signals["phase"] = string(progress.Phase)
	}
	if len(layered) > 0 {
		signals["planned_chapters"] = len(domain.FlattenOutline(layered))
		signals["volumes_total"] = len(layered)
	}
	if compass != nil {
		if compass.EstimatedScale != "" {
			signals["compass_estimated_scale"] = compass.EstimatedScale
		}
		signals["open_threads_count"] = len(compass.OpenThreads)
	}
	if active, err := t.store.World.LoadActiveForeshadow(); err == nil {
		signals["active_foreshadow_count"] = len(active)
	}
	return signals
}

func (t *ContextTool) buildArchitectFoundation(envelope *architectContextEnvelope, warn func(string, error)) {
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			envelope.Foundation["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		}
		envelope.Foundation["premise_structure"] = premiseStructure(premise, tier)
	} else {
		warn("premise", err)
	}

	if chars, err := t.store.Characters.Load(); err == nil && chars != nil {
		envelope.Foundation["characters"] = chars
	} else {
		warn("characters", err)
	}

	if snapshots, err := t.store.Characters.LoadLatestSnapshots(); err == nil && len(snapshots) > 0 {
		envelope.Foundation["character_snapshots"] = snapshots
	} else {
		warn("character_snapshots", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		envelope.Foundation["world_rules"] = rules
	} else {
		warn("world_rules", err)
	}
	// narrative_contract: cho Kiến trúc sư thấy ngôi kể/thì đã khai (nếu có) để không khai lại/ghi đè khi
	// sửa gia tăng. Chưa khai → bỏ qua êm.
	if narrative, err := t.store.Outline.LoadNarrative(); err == nil {
		if !narrative.IsEmpty() {
			envelope.Foundation["narrative_contract"] = narrative
		}
	} else {
		warn("narrative_contract", err)
	}
	if foreshadow, err := t.store.World.LoadActiveForeshadow(); err == nil && len(foreshadow) > 0 {
		envelope.Foundation["foreshadow_ledger"] = foreshadow
	} else {
		warn("foreshadow_ledger", err)
	}
	envelope.Foundation["foundation_status"] = t.foundationStatus()
}

func (t *ContextTool) buildArchitectReferences(envelope *architectContextEnvelope, warn func(string, error)) {
	if styleRules, err := t.store.World.LoadStyleRules(); err == nil && styleRules != nil {
		envelope.References["style_rules"] = styleRules
	} else {
		warn("style_rules", err)
	}

	envelope.References["references"] = t.architectReferences()
}
