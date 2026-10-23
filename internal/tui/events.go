package tui

import (
	"context"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

type liveResult struct {
	event  core.LiveEvent
	closed bool
}
type liveDirectoryResult struct {
	conversations []core.Conversation
	users         []core.User
	err           error
}

func (m model) startLiveCmd() tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(m.liveEvents)
			m.options.Events.Events(m.ctx, func(event core.LiveEvent) {
				select {
				case m.liveEvents <- event:
				case <-m.ctx.Done():
				}
			})
		}()
		return m.waitLiveCmd()()
	}
}
func (m model) waitLiveCmd() tea.Cmd {
	return func() tea.Msg {
		select {
		case event, ok := <-m.liveEvents:
			return liveResult{event: event, closed: !ok}
		case <-m.ctx.Done():
			return liveResult{closed: true}
		}
	}
}
func (m model) refreshInterval() time.Duration {
	if m.liveConnected {
		return max(m.options.PollInterval, 2*time.Minute)
	}
	return m.options.PollInterval
}
func (m model) knownLiveChannel(channel string) bool {
	for _, c := range m.conversations {
		if c.ID == channel {
			return true
		}
	}
	return false
}
func (m model) liveDirectoryCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		conversations, users, err := m.service.Directory(ctx, true)
		return liveDirectoryResult{conversations, users, err}
	}
}
func (m model) handleLive(result liveResult) (tea.Model, tea.Cmd) {
	e := result.event
	var next tea.Cmd
	if result.closed {
		e = core.LiveEvent{State: "offline", Detail: "Socket Mode stopped; polling fallback"}
	} else {
		next = m.waitLiveCmd()
	}
	if e.State != "" {
		wasConnected := m.liveConnected
		m.liveConnected, m.liveDetail = e.State == "connected", e.Detail
		commands := []tea.Cmd{next}
		if wasConnected != m.liveConnected {
			// Replace old deadlines; stale timer messages are ignored at dispatch.
			if !m.unreadPolling {
				commands = append(commands, m.scheduleUnreadCheck())
			}
			if !m.polling {
				if !m.main.loading && m.main.conversation.ID != "" && !m.sending {
					m.polling, m.pollCurrent = true, m.main.conversation
					m.nextPoll = time.Time{}
					m.pollThread = m.thread != nil
					commands = append(commands, m.pollCmd())
				} else {
					m.nextPoll = time.Now().Add(m.refreshInterval())
					commands = append(commands, m.tick())
				}
			}
		}
		return m, tea.Batch(commands...)
	}
	if e.TeamID != m.identity.TeamID {
		return m, next
	}
	if m.liveSeen == nil {
		m.liveSeen = map[string]bool{}
	}
	if e.ID != "" {
		if m.liveSeen[e.ID] {
			return m, next
		}
		m.liveSeen[e.ID] = true
		m.liveOrder = append(m.liveOrder, e.ID)
		if len(m.liveOrder) > 2048 {
			delete(m.liveSeen, m.liveOrder[0])
			m.liveOrder = m.liveOrder[1:]
		}
	}
	if !m.knownLiveChannel(e.Channel) {
		if len(m.livePending) < 256 {
			m.livePending = append(m.livePending, e)
		}
		if !m.liveDirectoryPending {
			m.liveDirectoryPending = true
			return m, tea.Batch(next, m.liveDirectoryCmd())
		}
		return m, next
	}
	return m, tea.Batch(next, m.applyLive(e))
}

func (m *model) applyLive(e core.LiveEvent) tea.Cmd {
	if e.Refresh {
		if m.main.conversation.ID == e.Channel && !m.polling && !m.main.loading && !m.sending {
			m.polling, m.pollCurrent = true, m.main.conversation
			m.nextPoll = time.Time{}
			m.pollThread = m.thread != nil
			return m.pollCmd()
		}
		return nil
	}
	m.liveRevision++
	m.liveChanges = append(m.liveChanges, liveChange{m.liveRevision, e})
	if len(m.liveChanges) > 2048 {
		m.liveChanges = m.liveChanges[len(m.liveChanges)-2048:]
	}
	msg := e.Message
	if name := m.names[msg.UserID]; name != "" {
		msg.User = name
	}
	views := []*view{&m.main, m.thread, m.preview}
	for _, v := range views {
		if v == nil || v.conversation.ID != e.Channel {
			continue
		}
		if e.Deleted != "" {
			selected := ""
			if v.selected >= 0 && v.selected < len(v.messages) {
				selected = v.messages[v.selected].Timestamp
			}
			remaining := make([]core.Message, 0, len(v.messages))
			for _, old := range v.messages {
				if old.Timestamp != e.Deleted {
					remaining = append(remaining, old)
				}
			}
			v.messages = remaining
			v.selected = min(v.selected, max(0, len(remaining)-1))
			for i, old := range remaining {
				if old.Timestamp == selected {
					v.selected = i
				}
			}
			m.rebuild(v, true)
		} else if msg.Timestamp != "" {
			if v.thread != "" {
				if msg.ThreadTS != v.thread && msg.Timestamp != v.thread {
					continue
				}
			} else if msg.ThreadTS != "" && msg.ThreadTS != msg.Timestamp && !e.Broadcast {
				// A reply changes the cached parent's count without adding a timeline row.
				if !e.Changed {
					for i := range v.messages {
						if v.messages[i].Timestamp == msg.ThreadTS {
							v.messages[i].ReplyCount++
							m.rebuild(v, true)
							break
						}
					}
				}
				continue
			}
			m.merge(v, []core.Message{msg}, false, false)
		}
	}
	if msg.Timestamp != "" && !e.Changed {
		if msg.ThreadTS == "" || msg.ThreadTS == msg.Timestamp || e.Broadcast {
			m.observeLatest(e.Channel, msg.Timestamp)
		}
		return m.notificationCmd("socket", e.Channel, []core.Message{msg})
	}
	return nil
}

// Protect newer pushed edits/deletions from reads that began before the event.
// Subsequent reads are authoritative again, including reaction changes.
type liveChange struct {
	revision uint64
	event    core.LiveEvent
}

func (m model) reconcileLive(channel string, messages []core.Message, revision uint64) []core.Message {
	if revision == m.liveRevision {
		return messages
	}
	changes := map[string]core.LiveEvent{}
	for _, change := range m.liveChanges {
		if change.revision <= revision || change.event.Channel != channel {
			continue
		}
		ts := change.event.Message.Timestamp
		if change.event.Deleted != "" {
			ts = change.event.Deleted
		}
		changes[ts] = change.event
	}
	result := make([]core.Message, 0, len(messages))
	for _, msg := range messages {
		if event, ok := changes[msg.Timestamp]; ok {
			if event.Deleted != "" {
				continue
			}
			msg = event.Message
		}
		result = append(result, msg)
	}
	return result
}
