package tui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) unreadIndicator(now time.Time, width int) string {
	const prefix = "unread: next "
	const joiner = " · checking "
	nextName, current, countdown := "—", "—", ""
	next, _ := m.nextUnreadConversation()
	if next.ID != "" {
		nextName = next.Label()
		if !m.unreadPolling && !m.nextUnreadCheck.IsZero() {
			remaining := max(0, m.nextUnreadCheck.Sub(now))
			seconds := remaining / time.Second
			if remaining%time.Second != 0 {
				seconds++
			}
			countdown = fmt.Sprintf(" in %ds", seconds)
		}
	}
	if m.unreadPolling {
		current = m.unreadCurrent.Label()
	}
	if width-ansi.StringWidth(prefix+countdown+joiner) < 2 {
		countdown = ""
	}
	// Share the available columns without padding between the two statuses.
	// A short name leaves more room for the other conversation's name.
	labelWidth := width - ansi.StringWidth(prefix+countdown+joiner)
	currentWidth := min(ansi.StringWidth(current), max(1, labelWidth/3))
	nextWidth := max(1, labelWidth-currentWidth)
	currentWidth = max(1, labelWidth-min(ansi.StringWidth(nextName), nextWidth))
	return fit(prefix+fit(nextName, nextWidth)+countdown+joiner+fit(current, currentWidth), width)
}
