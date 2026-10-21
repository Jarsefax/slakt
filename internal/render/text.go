// Package render handles untrusted Slack text at the terminal boundary.
package render

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/charmbracelet/x/ansi"
)

// Safe removes ANSI/OSC/DCS sequences, control characters and invisible format
// controls, except emoji joiners and tag characters. Run it after decoding
// entities and Slack markup, never before only.
func Safe(text string) string {
	text = ansi.Strip(text)
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if r == '\u200d' || (r >= '\U000e0020' && r <= '\U000e007f') {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, text)
}

var markup = regexp.MustCompile(`<([^<>]+)>`)

func Text(text string, names map[string]string) string {
	text = markup.ReplaceAllStringFunc(text, func(s string) string {
		parts := strings.SplitN(s[1:len(s)-1], "|", 2)
		target := parts[0]
		if strings.HasPrefix(target, "@") {
			id := strings.TrimPrefix(target, "@")
			if name := names[id]; name != "" {
				return "@" + name
			}
			return "@" + id
		}
		if strings.HasPrefix(target, "#") {
			if len(parts) == 2 {
				return "#" + parts[1]
			}
			return "#" + strings.TrimPrefix(target, "#")
		}
		if strings.HasPrefix(target, "!") {
			if len(parts) == 2 {
				return parts[1]
			}
			return "@" + strings.TrimPrefix(target, "!")
		}
		if len(parts) == 2 {
			return parts[1] + " (" + target + ")"
		}
		return target
	})
	return Safe(emojiText(html.UnescapeString(text)))
}

func Time(ts string) string {
	return TimeAt(ts, time.Now())
}

// TimeAt labels today's messages using now's calendar date and location.
func TimeAt(ts string, now time.Time) string {
	sec, _, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return Safe(ts)
	}
	messageTime := time.Unix(n, 0).In(now.Location())
	year, month, day := messageTime.Date()
	if year == now.Year() && month == now.Month() && day == now.Day() {
		return messageTime.Format("Today 15:04")
	}
	return messageTime.Format("Jan 02 15:04")
}

// Author resolves the display name at the terminal boundary.
func Author(m core.Message, names map[string]string) string {
	user := m.User
	if user == "" || user == m.UserID {
		if name := names[m.UserID]; name != "" {
			user = name
		}
	}
	if user == "" {
		user = m.UserID
	}
	if user == "" {
		user = "app"
	}
	return Safe(user)
}

func Message(m core.Message, names map[string]string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s  %s", Time(m.Timestamp), Author(m, names))
	if m.ReplyCount > 0 {
		fmt.Fprintf(&out, "  ↳ %d replies", m.ReplyCount)
	}
	out.WriteString("\n" + MessageBody(m, names))
	return out.String()
}

// MessageBody renders text, attachments, and reactions without header metadata.
func MessageBody(m core.Message, names map[string]string) string {
	return messageBody(m, func(text string) string { return Text(text, names) })
}

// FormattedMessageBody renders mrkdwn in both the message and its attachments.
func FormattedMessageBody(m core.Message, names map[string]string, format func(string, TextStyle) string) string {
	return messageBody(m, func(text string) string { return FormattedText(text, names, format) })
}

func messageBody(m core.Message, text func(string) string) string {
	var out strings.Builder
	out.WriteString(text(m.Text))
	for _, a := range m.Attachments {
		out.WriteString("\n│ " + text(a))
	}
	if len(m.Reactions) > 0 {
		out.WriteString("\n")
		for _, r := range m.Reactions {
			fmt.Fprintf(&out, "%s %d  ", Safe(emojiText(":"+r.Name+":")), r.Count)
		}
	}
	return out.String()
}
