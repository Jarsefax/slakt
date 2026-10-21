package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func (m model) pollIndicator(now time.Time) string {
	conversation := m.main.conversation
	thread := m.thread != nil && m.thread.thread != ""
	if m.polling && m.pollCurrent.ID != "" {
		// Navigation cannot rename a request that is already running.
		conversation, thread = m.pollCurrent, m.pollThread
	}
	name := "—"
	if conversation.ID != "" {
		name = fit(conversation.Label(), m.width)
	}
	threadLabel := ""
	if thread {
		threadLabel = " + thread"
	}
	secondsUntil := func(duration time.Duration) time.Duration {
		seconds := duration / time.Second
		if duration%time.Second > 0 {
			seconds++
		}
		return seconds
	}
	interval := m.options.PollInterval.String()
	remaining := min(max(0, m.options.PollInterval), max(0, m.nextPoll.Sub(now)))
	seconds, maximum := secondsUntil(remaining), secondsUntil(max(0, m.options.PollInterval))
	minimumName := min(4, ansi.StringWidth(name))
	width := max(1, m.width-1)
	shortestStateWidth := max(len(fmt.Sprintf("%ds", maximum)), ansi.StringWidth("polling…"))
	minimumName = min(minimumName, max(1, width-len("poll ")-len(threadLabel)-1-shortestStateWidth))
	filled := 0
	if m.polling {
		filled = 5
	} else if m.options.PollInterval > 0 {
		filled = int(5 * (1 - float64(remaining)/float64(m.options.PollInterval)))
	}
	bar := " [" + strings.Repeat("■", filled) + strings.Repeat("□", 5-filled) + "] "
	// Pick a format using the longest state, so the available space stays
	// consistent between the countdown and a running request. Only the name
	// is shortened.
	for _, format := range []struct {
		separator, countdown, suffix string
	}{
		{bar, "in %ds", " · interval " + interval},
		{bar, "%ds/" + interval, ""},
		{" ", "%ds/" + interval, ""},
		{" ", "%ds", ""},
	} {
		stateWidth := max(ansi.StringWidth(fmt.Sprintf(format.countdown, maximum)), ansi.StringWidth("polling…")) + ansi.StringWidth(format.suffix)
		nameWidth := width - len("poll ") - len(threadLabel) - ansi.StringWidth(format.separator) - stateWidth
		if nameWidth < minimumName {
			continue
		}
		label := fit(name, nameWidth) + threadLabel
		state := fmt.Sprintf(format.countdown, seconds)
		if m.polling {
			state = "polling…"
		} else if conversation.ID == "" || m.nextPoll.IsZero() || m.options.PollInterval <= 0 {
			state = "waiting"
		}
		text := "poll " + label + format.separator + state + format.suffix
		return text
	}
	state := "waiting"
	if m.polling {
		state = "polling…"
	}
	return fit("poll "+name+threadLabel+" "+state, width)
}
