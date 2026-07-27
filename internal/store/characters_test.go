package store

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// Part M — voice card phải roundtrip qua characters.json: Save rồi Load lại giữ nguyên trường voice
// của nhân vật core/important, và nhân vật cũ thiếu voice thì Load về nil (bỏ qua êm, không panic).
func TestCharacterStoreRoundtripsVoiceCard(t *testing.T) {
	s := newTestStore(t)

	chars := []domain.Character{
		{Name: "Lâm Viễn", Role: "chính", Tier: "core", Voice: &domain.CharacterVoiceCard{
			Catchphrases:  []string{"'ừ thì'", "'biết rồi'"},
			SentenceStyle: "câu ngắn, cộc",
			SubtextLevel:  "cao — hiếm khi nói thẳng cảm xúc",
			Taboo:         "không bao giờ văn hoa",
		}},
		// Nhân vật cũ không có voice.
		{Name: "Tô Ly", Role: "phụ", Tier: "secondary"},
	}
	if err := s.Characters.Save(chars); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := s.Characters.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 characters, got %d", len(loaded))
	}

	lam := loaded[0]
	if lam.Voice.IsEmpty() {
		t.Fatalf("expected Lâm Viễn to keep voice card, got %+v", lam.Voice)
	}
	if lam.Voice.SentenceStyle != "câu ngắn, cộc" || lam.Voice.Taboo != "không bao giờ văn hoa" {
		t.Fatalf("voice card fields lost in roundtrip: %+v", lam.Voice)
	}
	if len(lam.Voice.Catchphrases) != 2 {
		t.Fatalf("expected 2 catchphrases, got %+v", lam.Voice.Catchphrases)
	}

	// Nhân vật cũ: voice về nil, IsEmpty=true, không panic.
	if !loaded[1].Voice.IsEmpty() {
		t.Fatalf("expected Tô Ly voice to be empty/nil, got %+v", loaded[1].Voice)
	}
}

// renderCharacters phải xuất hồ sơ giọng nói khi có, và bỏ qua êm khi nhân vật thiếu voice.
func TestRenderCharactersIncludesVoiceCard(t *testing.T) {
	md := renderCharacters([]domain.Character{
		{Name: "Lâm Viễn", Role: "chính", Description: "thiếu niên", Voice: &domain.CharacterVoiceCard{
			Catchphrases:  []string{"'ừ thì'"},
			SentenceStyle: "câu ngắn, cộc",
		}},
		{Name: "Tô Ly", Role: "phụ", Description: "bạn đồng hành"},
	})

	if !strings.Contains(md, "Hồ sơ giọng nói") {
		t.Fatalf("expected voice section rendered, got:\n%s", md)
	}
	if !strings.Contains(md, "câu ngắn, cộc") {
		t.Fatalf("expected sentence_style rendered, got:\n%s", md)
	}
	// Nhân vật thiếu voice không tạo section rỗng: chỉ có đúng một lần "Hồ sơ giọng nói".
	if n := strings.Count(md, "Hồ sơ giọng nói"); n != 1 {
		t.Fatalf("expected exactly one voice section (Tô Ly has none), got %d:\n%s", n, md)
	}
}
