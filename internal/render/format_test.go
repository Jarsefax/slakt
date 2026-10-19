package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
)

// Describe visual spans without terminal capability detection, so these tests
// also verify formatting on CI systems without an interactive terminal.
func describeFormatting(text string, style TextStyle) string {
	if style == (TextStyle{}) {
		return text
	}
	var label string
	if style.Bold {
		label += "b"
	}
	if style.Italic {
		label += "i"
	}
	if style.Strike {
		label += "s"
	}
	if style.Code {
		label += "c"
	}
	return fmt.Sprintf("<%s>%s</%s>", label, text, label)
}

func TestSlackFormatting(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"basic", "*bold* _italic_ ~strike~", "<b>bold</b> <i>italic</i> <s>strike</s>"},
		{"combined", "*_~all~_*", "<bis>all</bis>"},
		{"reverse nesting", "_*both*_", "<bi>both</bi>"},
		{"nested", "*bold _and italic_ bold* plain", "<b>bold </b><bi>and italic</bi><b> bold</b> plain"},
		{"multiple spans", "*one* and *two*", "<b>one</b> and <b>two</b>"},
		{"punctuation", "(*bold*), _italic_!", "(<b>bold</b>), <i>italic</i>!"},
		{"unicode", "_räksmörgås_ *你好*", "<i>räksmörgås</i> <b>你好</b>"},
		{"multiline", "*first\nsecond*", "<b>first\nsecond</b>"},
		{"unmatched", "*unfinished _italic_ ~open", "*unfinished <i>italic</i> ~open"},
		{"spaces", "* spaced * _ _ ~ ~", "* spaced * _ _ ~ ~"},
		{"literal punctuation", "a*b*c snake_case_name 2*3*4 ~home", "a*b*c snake_case_name 2*3*4 ~home"},
		{"repeated markers", "**double** __double__ ~~double~~", "**double** __double__ ~~double~~"},
		{"inline code", "Use `*literal* _literal_ :tada: <@U1>` :tada:", "Use <c>*literal* _literal_ :tada: <@U1></c> 🎉"},
		{"code in emphasis", "*bold `plain` bold*", "<b>bold </b><c>plain</c><b> bold</b>"},
		{"code block", "before\n```\n*literal*\n> :tada:\n```\nafter", "before\n<c>  *literal*\n  > :tada:</c>\nafter"},
		{"inline fence", "before ```code``` after", "before <c>\n  code\n</c> after"},
		{"unfinished inline", "`*literal* :tada:\n*bold*", "`*literal* :tada:\n<b>bold</b>"},
		{"unfinished block", "```\n*literal* :tada:", "```\n*literal* :tada:"},
		{"quotes", "> *quoted*\n&gt; next\nnormal > literal", "│ <b>quoted</b>\n│ next\nnormal > literal"},
		{"code quote", "`> not quoted`", "<c>> not quoted</c>"},
		{"URL", "https://example.com/a_b_c?q=*x* _yes_", "https://example.com/a_b_c?q=*x* <i>yes</i>"},
		{"Slack link", "<https://example.com/a_b_c|link_name> _yes_", "link_name (https://example.com/a_b_c) <i>yes</i>"},
		{"mention", "<@U1> *<@U1>*", "@first_last <b>@first_last</b>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := FormattedText(test.input, map[string]string{"U1": "first_last"}, describeFormatting); got != test.want {
				t.Fatalf("FormattedText(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestFormattedTextSanitizesBeforeStyling(t *testing.T) {
	input := "*safe* <@U1> &#27;]52;c;YQ==&#7; `&#27;[31mcode`\x1b[2J\u202e"
	got := FormattedText(input, map[string]string{"U1": "Alice\x1b[2J"}, func(text string, style TextStyle) string {
		if Safe(text) != text {
			t.Fatalf("unsafe text passed to styler: %q", text)
		}
		// Only the callback's own escape sequences may reach the terminal.
		return "\x1b[1m" + text + "\x1b[m"
	})
	if plain := Safe(got); plain != "safe @Alice  code" {
		t.Fatalf("unexpected sanitized text: %q", plain)
	}
}

func TestFormattedMessageIncludesAttachmentsAndReactionsWithoutChangingSource(t *testing.T) {
	m := core.Message{Text: "*great* :tada:", Attachments: []string{"_thanks_"}, Reactions: []core.Reaction{{Name: "thumbsup", Count: 2}}}
	got := FormattedMessageBody(m, nil, describeFormatting)
	if got != "<b>great</b> 🎉\n│ <i>thanks</i>\n👍 2  " {
		t.Fatalf("unexpected message body: %q", got)
	}
	if m.Text != "*great* :tada:" || m.Attachments[0] != "_thanks_" {
		t.Fatal("formatting changed the source message")
	}
	if plain := MessageBody(m, nil); !strings.Contains(plain, "*great* 🎉\n│ _thanks_") || strings.ContainsRune(plain, '\x1b') {
		t.Fatalf("plain CLI rendering changed: %q", plain)
	}
}
