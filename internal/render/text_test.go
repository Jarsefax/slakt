package render

import (
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
)

func TestTerminalContentCannotExecuteControlSequences(t *testing.T) {
	payload := "hello\x1b[2J\x1b]52;c;Y2xpcGJvYXJk\a\x1bPpayload\x1b\\world\x00\r\u009b\u202e\nnext"
	got := Safe(payload)
	if got != "helloworld\nnext" {
		t.Fatalf("unsafe terminal text: %q", got)
	}
	got = Text("<@U1> &amp; &#27;[31m <https://example.com|link>", map[string]string{"U1": "alice"})
	if strings.ContainsRune(got, '\x1b') || !strings.Contains(got, "@alice &") || !strings.Contains(got, "link (https://example.com)") {
		t.Fatalf("unsafe markup render: %q", got)
	}
}

func TestAuthorResolvesIDsAndPreservesExplicitAppNames(t *testing.T) {
	names := map[string]string{"U1": "Alice\x1b[2J"}
	for _, test := range []struct {
		message core.Message
		want    string
	}{
		{core.Message{UserID: "U1"}, "Alice"},
		{core.Message{UserID: "U1", User: "U1"}, "Alice"},
		{core.Message{UserID: "U1", User: "Release App"}, "Release App"},
		{core.Message{UserID: "UUNKNOWN"}, "UUNKNOWN"},
		{core.Message{}, "app"},
	} {
		if got := Author(test.message, names); got != test.want {
			t.Errorf("author for %+v = %q, want %q", test.message, got, test.want)
		}
	}
}

func TestEmojiShortcodesInText(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
	}{
		{"standard and aliases", ":thumbsup::+1: :coffee: :smile: :tada:", "👍👍 ☕ 😄 🎉"},
		{"skin tones", ":thumbsup::skin-tone-2: :+1::skin-tone-6:", "👍🏻 👍🏿"},
		{"combined with skin tone", ":female-technologist::skin-tone-4:", "👩🏽‍💻"},
		{"combined", ":female-technologist: :family_man_woman_girl_boy:", "👩‍💻 👨‍👩‍👧‍👦"},
		{"flags", ":flag-se: :flag-england:", "🇸🇪 🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"},
		{"custom and unknown", ":company_logo: :future_emoji::skin-tone-3:", ":company_logo: :future_emoji::skin-tone-3:"},
		{"unsupported tone", ":rocket::skin-tone-3:", ":rocket::skin-tone-3:"},
		{"inline code", "`:thumbsup:` :thumbsup:", "`:thumbsup:` 👍"},
		{"fenced code", "```\n:thumbsup:\n``` :thumbsup:", "```\n:thumbsup:\n``` 👍"},
		{"unfinished code", "```\n:thumbsup:", "```\n:thumbsup:"},
		{"URLs", "https://example.com/:thumbsup: :thumbsup:", "https://example.com/:thumbsup: 👍"},
		{"Slack links", "<https://example.com/:thumbsup:|:thumbsup:>", "👍 (https://example.com/:thumbsup:)"},
		{"entities", "&#58;thumbsup&#58; &amp;", "👍 &"},
		{"ordinary colons", "time 12:30:00 and :partial", "time 12:30:00 and :partial"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Text(test.input, nil); got != test.want {
				t.Fatalf("Text(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestEmojiSequencesSurviveTerminalSanitization(t *testing.T) {
	emojis := "👩🏽‍💻 👨‍👩‍👧‍👦 ❤️ 🇸🇪 🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f"
	if got := Safe(emojis); got != emojis {
		t.Fatalf("emoji sequence changed: %q", got)
	}
	input := ":female-technologist: &#27;]52;c;YQ==&#7;\u202e\u2066\u200b"
	if got := Text(input, nil); got != "👩‍💻 " {
		t.Fatalf("unsafe controls survived emoji conversion: %q", got)
	}
}

func TestMessagesRenderEmojiAndReactionCounts(t *testing.T) {
	m := core.Message{
		Timestamp: "1.000001", Text: "Great :tada:", Attachments: []string{"Thanks :thumbsup:"},
		Reactions: []core.Reaction{{Name: "thumbsup", Count: 2}, {Name: "+1::skin-tone-4", Count: 1}, {Name: "company_logo", Count: 3}},
	}
	got := Message(m, nil)
	for _, want := range []string{"Great 🎉", "│ Thanks 👍", "👍 2  👍🏽 1  :company_logo: 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if m.Text != "Great :tada:" || m.Reactions[0].Name != "thumbsup" {
		t.Fatal("rendering changed the source message")
	}
}
