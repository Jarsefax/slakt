package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Smaller terminals give the unread status its own line above the footer.
const unreadInlineWidth = 80
const unreadMinimumWidth = 39

func (m model) unreadIndicator(now time.Time, width int) string {
	const prefix = "unread: next "
	const joiner = " · checking "
	// Both slots depend on the available columns, never on the request phase
	// or the lengths of the names. Reserve countdown space even while checking.
	labelWidth := width - ansi.StringWidth(prefix+joiner)
	nextWidth := max(1, (2*labelWidth+2)/3)
	currentWidth := max(1, labelWidth-nextWidth)
	interval := m.unreadInterval()
	seconds := interval / time.Second
	if interval%time.Second != 0 {
		seconds++
	}
	timerWidth := len(fmt.Sprintf(" in %ds", seconds))
	if timerWidth >= nextWidth {
		timerWidth = 0
	}
	nextName, current, countdown := "—", "—", ""
	next, _ := m.nextUnreadConversation()
	if next.ID != "" {
		nextName = next.Label()
		if !m.unreadPolling && !m.nextUnreadCheck.IsZero() && timerWidth > 0 {
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
	pad := func(text string, columns int) string {
		text = fit(text, columns)
		return text + strings.Repeat(" ", max(0, columns-ansi.StringWidth(text)))
	}
	return fit(prefix+pad(fit(nextName, nextWidth-timerWidth)+countdown, nextWidth)+joiner+pad(current, currentWidth), width)
}
