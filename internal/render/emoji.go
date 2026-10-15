package render

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kyokomi/emoji/v2"
)

// Match code and URLs before shortcodes so their contents stay literal. A Slack
// skin-tone suffix belongs to the preceding shortcode, including unknown ones.
var emojiTokens = regexp.MustCompile("(?s)```.*?(?:```|$)|`[^`\n]*(?:`|\n|$)|https?://[^\\s<>`]+|:[A-Za-z0-9_+\\-]+:(?::skin-tone-[2-6]:)?")

var emojiShortcodes = func() map[string]string {
	codes := make(map[string]string)
	sequences := make(map[string]bool)
	for code, value := range emoji.CodeMap() {
		codes[code] = value
		sequences[value] = true
	}
	// Slack uses :emoji::skin-tone-N:. Insert the modifier after the first
	// character, ahead of any joiner, and accept only known emoji sequences.
	for code, value := range emoji.CodeMap() {
		_, size := utf8.DecodeRuneInString(value)
		for tone := byte('2'); tone <= '6'; tone++ {
			modifier := string('\U0001f3fb' + rune(tone-'2'))
			variant := value[:size] + modifier + strings.TrimPrefix(value[size:], "\ufe0f")
			if sequences[variant] {
				codes[code+":skin-tone-"+string(tone)+":"] = variant
			}
		}
	}
	return codes
}()

func emojiText(text string) string {
	return emojiTokens.ReplaceAllStringFunc(text, func(token string) string {
		if value, ok := emojiShortcodes[token]; ok {
			return value
		}
		return token
	})
}
