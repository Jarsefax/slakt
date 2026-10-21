package tui

import (
	"context"
	"sort"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
)

type unreadTickMsg time.Time
type unreadResult struct {
	channel string
	latest  string
	err     error
}

func (m model) isUnread(channel string) bool {
	return m.latest[channel] > m.readState.Position(channel)
}

func (m model) readingMain() bool {
	return m.main.conversation.ID != "" && !m.main.loading && !m.sidebar && !m.searchActive && !m.help &&
		(m.thread == nil || m.width >= 125) && m.width >= 40 && m.height >= 15
}

// The source directory remains complete and alphabetically grouped, including
// for Ctrl+k. Rendering and navigation share this filtered, sorted projection.
func (m model) sidebarConversations() []core.Conversation {
	items := make([]core.Conversation, 0, len(m.conversations))
	for _, c := range m.conversations {
		switch m.sidebarMode {
		case localdata.All:
		case localdata.Hidden:
			if !m.hidden[c.ID] {
				continue
			}
		default:
			if m.hidden[c.ID] {
				continue
			}
		}
		if m.sidebarMode != localdata.UnreadOnly || m.isUnread(c.ID) || c.ID == m.main.conversation.ID || m.sidebar && c.ID == m.sidebarReadID {
			items = append(items, c)
		}
	}
	if m.sidebarMode == localdata.UnreadFirst {
		for start := 0; start < len(items); {
			end := start + 1
			for end < len(items) && items[end].Section() == items[start].Section() {
				end++
			}
			section := items[start:end]
			sort.SliceStable(section, func(i, j int) bool {
				return m.isUnread(section[i].ID) && !m.isUnread(section[j].ID)
			})
			start = end
		}
	}
	return items
}

func (m model) sidebarSelectedID() string {
	if m.sidebar && m.mode == "" {
		items := m.sidebarConversations()
		if m.sideIndex >= 0 && m.sideIndex < len(items) {
			return items[m.sideIndex].ID
		}
	}
	return m.main.conversation.ID
}

func (m *model) selectSidebar(channel string) {
	// Jump results use the same index, independently of sidebar filtering.
	if m.mode != "" {
		return
	}
	items := m.sidebarConversations()
	for i, c := range items {
		if c.ID == channel {
			m.sideIndex = i
			return
		}
	}
	m.sideIndex = min(max(0, m.sideIndex), max(0, len(items)-1))
}

func (m *model) moveSidebar(delta int) {
	items := m.sidebarConversations()
	if len(items) == 0 {
		return
	}
	next := min(max(0, m.sideIndex+delta), len(items)-1)
	if next == m.sideIndex {
		return
	}
	// Choose the neighbor before dropping the retained read row, then find
	// it again in the filtered list so removing a row never skips a neighbor.
	channel := items[next].ID
	m.sidebarReadID = ""
	m.selectSidebar(channel)
}

func (m *model) markSidebarRead() {
	items := m.sidebarConversations()
	if m.sideIndex < 0 || m.sideIndex >= len(items) {
		return
	}
	c := items[m.sideIndex]
	v := m.preview
	if c.ID == m.main.conversation.ID {
		v = &m.main
	}
	if v == nil || v.conversation.ID != c.ID || v.loading {
		m.status = "Wait for the preview to load before marking read"
		return
	}
	if m.previewError != "" {
		return
	}
	m.sidebarReadID = c.ID
	m.markReadThrough(c.ID, max(m.latest[c.ID], newestTimestamp(v.messages)))
	m.status = "Marked " + c.Label() + " read"
}

func (m *model) toggleSidebarHidden() {
	items := m.sidebarConversations()
	if m.sideIndex < 0 || m.sideIndex >= len(items) {
		return
	}
	c := items[m.sideIndex]
	hide := !m.hidden[c.ID]
	if m.hidden == nil {
		m.hidden = map[string]bool{}
	}
	if m.hiddenDirty == nil {
		m.hiddenDirty = map[string]bool{}
	}
	if hide {
		m.hidden[c.ID] = true
		m.status = "Hidden " + c.Label() + " · find it in Hidden or All"
	} else {
		delete(m.hidden, c.ID)
		m.status = "Unhid " + c.Label()
	}
	m.hiddenDirty[c.ID] = hide
	m.sidebarReadID = ""
	// Keep the row when it still belongs to this mode; otherwise select the
	// next row, or the previous row when removing the last item.
	m.selectSidebar(c.ID)
	m.persistLocal()
}

func newestTimestamp(messages []core.Message) string {
	latest := ""
	for _, msg := range messages {
		latest = max(latest, msg.Timestamp)
	}
	return latest
}

func (m *model) observeLatest(channel, latest string) {
	selected := m.sidebarSelectedID()
	m.latest[channel] = max(m.latest[channel], latest)
	m.selectSidebar(selected)
}

func (m *model) markRead(channel string, messages []core.Message) {
	m.markReadThrough(channel, newestTimestamp(messages))
}

func (m *model) markReadThrough(channel, latest string) {
	selected := m.sidebarSelectedID()
	if latest > m.readState.Position(channel) {
		m.readState.LastRead[channel] = latest
		m.readDirty = true
	}
	m.selectSidebar(selected)
	m.persistLocal()
}

func (m *model) persistLocal() {
	if m.options.Store == nil {
		m.configDirty, m.readDirty = false, false
		m.hiddenDirty = nil
		return
	}
	// Local writes finish before the next key (and before quitting), avoiding
	// out-of-order mode saves. Lock contention is bounded to keep the UI usable.
	ctx, cancel := context.WithTimeout(m.ctx, 250*time.Millisecond)
	defer cancel()
	if m.configDirty {
		if err := m.options.Store.SaveMode(ctx, m.sidebarMode); err != nil {
			m.storageError = "Cannot save configuration: " + err.Error()
			return
		}
		m.configDirty = false
	}
	if len(m.hiddenDirty) > 0 {
		if err := m.options.Store.SaveHiddenConversations(ctx, m.identity.TeamID, m.identity.UserID, m.hiddenDirty); err != nil {
			m.storageError = "Cannot save hidden conversations: " + err.Error()
			return
		}
		m.hiddenDirty = nil
	}
	if m.readDirty {
		if err := m.options.Store.SaveReadState(ctx, m.identity.TeamID, m.identity.UserID, m.readState); err != nil {
			m.storageError = "Cannot save read positions: " + err.Error()
			return
		}
		m.readDirty = false
	}
	m.storageError = ""
}

// A single round-robin request at a time starts at most 20 checks/minute.
// Larger workspaces take longer to scan, rather than creating request bursts.
func (m model) unreadTick() tea.Cmd {
	interval := 3 * time.Second
	if len(m.conversations) > 0 {
		interval = max(interval, m.options.PollInterval/time.Duration(len(m.conversations)))
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg { return unreadTickMsg(t) })
}

func (m model) unreadCmd(channel string) tea.Cmd {
	after := m.readState.Position(channel)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
		defer cancel()
		page, err := m.service.Backend.History(ctx, channel, core.PageOptions{Limit: 1, After: after})
		return unreadResult{channel: channel, latest: newestTimestamp(page.Messages), err: err}
	}
}
