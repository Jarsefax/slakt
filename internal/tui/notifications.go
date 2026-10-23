package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	"github.com/Jarsefax/slakt/internal/render"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type notificationResult struct{ err error }

// Keep only metadata for the most recent message decision and check per
// conversation. Empty background checks must not erase a rejected DM's evidence.
type notificationDecision struct {
	channel, source, timestamp string
	startup, seen, cutoff      string
	reason, firstReason        string
	checkedAt                  time.Time
}

type notificationTrace struct {
	message, check notificationDecision
}

func (m *model) recordNotificationDecision(d notificationDecision) {
	if m.notificationDecisions == nil {
		m.notificationDecisions = map[string]notificationTrace{}
	}
	trace := m.notificationDecisions[d.channel]
	trace.check = d
	if trace.message.timestamp == "" || d.timestamp != "" && d.timestamp >= trace.message.timestamp {
		if d.timestamp != "" && d.timestamp == trace.message.timestamp {
			d.firstReason = trace.message.firstReason
			if d.firstReason == "" {
				d.firstReason = trace.message.reason
			}
		}
		trace.message = d
	}
	m.notificationDecisions[d.channel] = trace
	m.notificationLastChannel = d.channel
}

func (m model) notificationDecision(source, channel string, messages []core.Message) notificationDecision {
	return notificationDecision{
		channel: channel, source: source, timestamp: newestTimestamp(messages),
		startup: m.notificationAfter, seen: m.notificationSeen[channel],
		cutoff: max(m.notificationAfter, m.notificationSeen[channel]), checkedAt: time.Now(),
	}
}

func (m *model) notificationSkipped(source, channel string, messages []core.Message, reason string) {
	d := m.notificationDecision(source, channel, messages)
	d.reason = reason
	m.recordNotificationDecision(d)
}

func (m model) notificationTraceForView() (notificationTrace, bool) {
	channel := m.main.conversation.ID
	if m.sidebar {
		channel = m.sidebarSelectedID()
	} else if m.searchActive && m.search.selected >= 0 && m.search.selected < len(m.search.messages) {
		channel = m.search.messages[m.search.selected].ChannelID
	} else if m.thread != nil {
		channel = m.thread.conversation.ID
	}
	if channel == "" {
		channel = m.notificationLastChannel
	}
	trace, ok := m.notificationDecisions[channel]
	return trace, ok
}

func (m model) notificationConversationLabel(channel string) string {
	for _, c := range m.conversations {
		if c.ID == channel {
			return strings.Join(strings.Fields(render.Safe(c.Label())), " ")
		}
	}
	return render.Safe(channel)
}

func notificationTimestamp(ts string) string {
	if ts == "" {
		return "—"
	}
	sec, _, _ := strings.Cut(ts, ".")
	seconds, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return render.Safe(ts)
	}
	return fmt.Sprintf("%s (%s)", render.Safe(ts), time.Unix(seconds, 0).UTC().Format("2006-01-02 15:04:05 UTC"))
}

func (m model) notificationDetails() []string {
	trace, ok := m.notificationTraceForView()
	if !ok {
		return []string{"No notification check recorded for this conversation yet."}
	}
	d := trace.message
	lines := []string{
		"Conversation: " + m.notificationConversationLabel(d.channel) + " (" + render.Safe(d.channel) + ")",
		"Decision: " + d.reason + " · source: " + d.source,
		"Message timestamp: " + notificationTimestamp(d.timestamp),
		"Startup cutoff: " + notificationTimestamp(d.startup),
		"Previously examined cutoff: " + notificationTimestamp(d.seen),
		"Effective cutoff: " + notificationTimestamp(d.cutoff),
		"Decision time: " + d.checkedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
	}
	if d.firstReason != "" && d.firstReason != d.reason {
		lines = append(lines, "First decision for this message: "+d.firstReason)
	}
	lines = append(lines, "Latest check: "+trace.check.source+" · "+trace.check.reason+" · "+trace.check.checkedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	return lines
}

func (m model) notificationsEnabled() bool {
	return !m.options.Demo && m.notify != nil
}

func (m model) showNotificationStatus() bool {
	return m.notificationStatus != localdata.NotificationsOff
}

func (m model) notificationIndicator(width int) string {
	if !m.notificationsEnabled() {
		if m.options.Demo {
			return fit("notify: off (demo)", width)
		}
		return fit("notify: off", width)
	}
	text := fmt.Sprintf("notify: tried %d · sent %d · failed %d", m.notificationTried, m.notificationSent, m.notificationFailed)
	detail := ""
	if trace, ok := m.notificationTraceForView(); ok {
		d := trace.message
		detail = d.reason + " · " + d.source + " · " + m.notificationConversationLabel(d.channel)
		if d.timestamp != "" {
			detail += " · msg " + render.Safe(d.timestamp) + " · cutoff " + render.Safe(d.cutoff)
		}
	}
	if ansi.StringWidth(text) > width || detail != "" && ansi.StringWidth(text+" · "+strings.SplitN(detail, " · ", 2)[0]) > width {
		text = fmt.Sprintf("notify: t%d s%d f%d", m.notificationTried, m.notificationSent, m.notificationFailed)
	}
	if detail != "" {
		text += " · " + detail
	}
	if m.notificationError != "" {
		text += " · " + strings.Join(strings.Fields(render.Safe(m.notificationError)), " ")
	}
	return fit(text, width)
}

// Keep notification discovery separate from read positions: reading or previewing
// must not make an arrival notify twice, and startup unread history stays silent.
// One notification per conversation/check summarizes the newest incoming message.
func (m *model) notificationCmd(source, channel string, messages []core.Message) tea.Cmd {
	d := m.notificationDecision(source, channel, messages)
	if !m.notificationsEnabled() {
		d.reason = "disabled"
		m.recordNotificationDecision(d)
		return nil
	}
	after := d.cutoff
	var newest core.Message
	var latest core.Message
	for _, msg := range messages {
		if msg.Timestamp > latest.Timestamp {
			latest = msg
		}
		m.notificationSeen[channel] = max(m.notificationSeen[channel], msg.Timestamp)
		if msg.Timestamp > after && msg.Timestamp > newest.Timestamp && msg.UserID != m.identity.UserID {
			newest = msg
		}
	}
	if newest.Timestamp == "" || m.hidden[channel] {
		switch {
		case m.hidden[channel]:
			d.reason = "hidden"
		case len(messages) == 0:
			d.reason = "no messages"
		case d.timestamp == "":
			d.reason = "missing timestamp"
		case d.timestamp <= d.startup:
			d.reason = "before startup"
		case d.timestamp <= d.seen:
			d.reason = "already examined"
		case latest.UserID == m.identity.UserID:
			d.reason = "own message"
		default:
			d.reason = "no eligible message"
		}
		m.recordNotificationDecision(d)
		return nil
	}
	d.timestamp = newest.Timestamp
	var conversation core.Conversation
	for _, c := range m.conversations {
		if c.ID == channel {
			conversation = c
			break
		}
	}
	if conversation.ID == "" {
		d.reason = "unknown conversation"
		m.recordNotificationDecision(d)
		return nil
	}
	d.reason = "queued"
	m.recordNotificationDecision(d)
	body := render.Text(newest.Text, m.names)
	if strings.TrimSpace(body) == "" {
		body = "New message"
	}
	brand := "Slakt"
	version := strings.TrimSpace(m.options.Version)
	if i := strings.IndexAny(version, "-+"); i >= 0 {
		version = version[:i]
	}
	if version != "" {
		brand += " " + version
	}
	summary := brand + " · " + conversation.Label() + " · " + render.Author(newest, m.names)
	notify := m.notify
	// Count dispatch on the UI thread. Results update sent/failed separately,
	// so an in-flight delivery is visible even before the desktop responds.
	m.notificationTried++
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
		defer cancel()
		return notificationResult{err: notify(ctx, summary, body)}
	}
}

// Runtime wiring is separate from newModel so unit tests and demo sessions never
// talk to a real desktop. A missing setting defaults to on in LoadConfig.
func (m *model) configureNotifications(config localdata.Config, notify func(context.Context, string, string) error) {
	m.notificationStatus = config.NotificationStatus
	m.notificationDecisions = map[string]notificationTrace{}
	m.notificationLastChannel = ""
	m.notify = nil
	if !m.options.Demo && config.Notifications == localdata.NotificationsOn {
		m.notify = notify
		m.notificationAfter = localdata.NewReadState(time.Now()).StartedAt
		m.notificationSeen = map[string]string{}
	}
}
