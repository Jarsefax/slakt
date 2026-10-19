package render

import (
	"html"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TextStyle describes Slack's visual formatting, independently of terminal colors.
type TextStyle struct {
	Bold, Italic, Strike, Code bool
}

// Recognize code, Slack references and URLs before emphasis markers. In
// particular, underscores in URLs and resolved display names are never markup.
var formattingTokens = regexp.MustCompile("(?s:```.*?(?:```|$))|`[^`\n]*(?:`|\n|$)|<[^<>\n]+>|https?://[^\\s<>`]+|[*_~]")

type formattingToken struct {
	text                  string
	marker                byte
	paired, opens, closes bool
	code, literal         bool
}

// FormattedText interprets Slack mrkdwn and calls format only with sanitized
// display text. The callback may add trusted terminal styles after sanitization.
// Text remains the plain CLI renderer; neither path modifies the source message.
func FormattedText(text string, names map[string]string, format func(string, TextStyle) string) string {
	var tokens []formattingToken
	appendText := func(start, end int) {
		if start < end {
			value := quoteText(text[start:end], start == 0 || text[start-1] == '\n')
			tokens = append(tokens, formattingToken{text: Text(value, names)})
		}
	}
	end := 0
	for _, match := range formattingTokens.FindAllStringIndex(text, -1) {
		start, next := match[0], match[1]
		appendText(end, start)
		value := text[start:next]
		token := formattingToken{}
		switch {
		case value[0] == '`':
			fence := "`"
			if strings.HasPrefix(value, "```") {
				fence = "```"
			}
			if len(value) > len(fence)*2 && strings.HasSuffix(value, fence) {
				token.code = true
				value = value[len(fence) : len(value)-len(fence)]
				if len(fence) == 3 {
					value = strings.TrimSuffix(strings.TrimPrefix(value, "\n"), "\n")
					value = "  " + strings.ReplaceAll(value, "\n", "\n  ")
					if start > 0 && text[start-1] != '\n' {
						value = "\n" + value
					}
					if next < len(text) && text[next] != '\n' {
						value += "\n"
					}
				}
			}
			// An unfinished code span also protects its contents from markup
			// and emoji expansion, but keeps its visible backticks.
			token.literal = true
			token.text = Safe(html.UnescapeString(value))
		case len(value) == 1 && strings.ContainsAny(value, "*_~"):
			token.text, token.marker = value, value[0]
			before, _ := utf8.DecodeLastRuneInString(text[:start])
			after, _ := utf8.DecodeRuneInString(text[next:])
			token.opens = next < len(text) && !unicode.IsSpace(after) && after != rune(token.marker) &&
				(start == 0 || !wordRune(before) && before != rune(token.marker))
			token.closes = start > 0 && !unicode.IsSpace(before) && before != rune(token.marker) &&
				(next == len(text) || !wordRune(after) && after != rune(token.marker))
		default:
			token.text = Text(value, names)
		}
		tokens = append(tokens, token)
		end = next
	}
	appendText(end, len(text))

	// Pair only valid delimiters. Unmatched markers stay visible, and each
	// style can be combined with the others without recursive parsing.
	open := map[byte]int{}
	for i := range tokens {
		token := &tokens[i]
		if token.marker == 0 {
			continue
		}
		if previous, ok := open[token.marker]; ok && token.closes {
			tokens[previous].paired, token.paired = true, true
			token.opens = false
			delete(open, token.marker)
		} else if token.opens {
			if _, ok := open[token.marker]; !ok {
				open[token.marker] = i
			}
		}
	}

	var out strings.Builder
	style := TextStyle{}
	for _, token := range tokens {
		if token.paired {
			switch token.marker {
			case '*':
				style.Bold = token.opens
			case '_':
				style.Italic = token.opens
			case '~':
				style.Strike = token.opens
			}
			continue
		}
		current := style
		if token.literal {
			current = TextStyle{Code: token.code}
		}
		out.WriteString(format(token.text, current))
	}
	return out.String()
}

func wordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r)
}

func quoteText(text string, atLineStart bool) string {
	lines := strings.SplitAfter(text, "\n")
	for i, line := range lines {
		if i == 0 && !atLineStart {
			continue
		}
		prefix := ""
		if strings.HasPrefix(line, ">") {
			prefix = ">"
		} else if strings.HasPrefix(line, "&gt;") {
			prefix = "&gt;"
		}
		if prefix != "" {
			lines[i] = "│ " + strings.TrimPrefix(strings.TrimPrefix(line, prefix), " ")
		}
	}
	return strings.Join(lines, "")
}
