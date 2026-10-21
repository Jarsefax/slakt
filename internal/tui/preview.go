package tui

import (
	"context"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// Preview results cannot be mistaken for an explicit conversation load, even
// when the user opens the same conversation before its preview request returns.
type previewResult struct {
	channel    string
	generation int
	page       core.MessagePage
	err        error
}

func (m *model) clearPreview() {
	if m.previewCancel != nil {
		m.previewCancel()
		m.previewCancel = nil
	}
	m.preview = nil
	m.previewAfter = ""
	m.previewError = ""
}

func messagesAfter(messages []core.Message, after string) []core.Message {
	if after == "" {
		return messages
	}
	var unread []core.Message
	for _, msg := range messages {
		if msg.Timestamp > after {
			unread = append(unread, msg)
		}
	}
	return unread
}

// Run after each update so filtering and background unread reordering use the
// same selected conversation as keyboard navigation. The committed views and
// drafts stay intact until Enter explicitly opens a conversation.
func (m *model) syncPreview() tea.Cmd {
	if !m.sidebar || m.mode != "" {
		m.sidebarReadID = ""
		m.clearPreview()
		return nil
	}
	if m.help {
		return nil
	}
	items := m.sidebarConversations()
	if len(items) == 0 {
		m.sidebarReadID = ""
		m.clearPreview()
		return nil
	}
	m.sideIndex = min(max(0, m.sideIndex), len(items)-1)
	c := items[m.sideIndex]
	if c.ID != m.sidebarReadID {
		m.sidebarReadID = ""
	}
	after := ""
	if m.sidebarMode == localdata.UnreadOnly {
		after = m.readState.Position(c.ID)
		// Explicitly marking read retains the preview's original unread
		// boundary until navigation leaves this row. Mode changes can still
		// replace a full-history preview with the unread projection.
		if c.ID == m.sidebarReadID && m.preview != nil && m.preview.conversation.ID == c.ID && m.previewAfter != "" {
			after = m.previewAfter
		}
	}
	if c.ID == m.main.conversation.ID {
		m.clearPreview()
		if m.sidebarMode == localdata.UnreadOnly {
			// Project the open timeline so polling stays visible without
			// changing its messages, selection, or scroll position.
			m.previewGeneration++
			m.previewAfter = after
			m.preview = &view{conversation: c, newAfter: after, messages: messagesAfter(m.main.messages, after), loading: m.main.loading, generation: m.previewGeneration, viewport: viewport.New(60, 15)}
			m.preview.selected = max(0, len(m.preview.messages)-1)
			m.rebuild(m.preview, false)
			m.positionInitial(m.preview)
		}
		return nil
	}
	if m.preview != nil && m.preview.conversation.ID == c.ID && m.previewAfter == after {
		return nil
	}
	m.clearPreview()
	m.previewGeneration++
	generation := m.previewGeneration
	m.previewAfter = after
	m.preview = &view{conversation: c, newAfter: m.readState.Position(c.ID), loading: true, generation: generation, viewport: viewport.New(60, 15)}
	m.rebuild(m.preview, false)
	ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
	m.previewCancel = cancel
	service := m.service
	return func() tea.Msg {
		defer cancel()
		page, err := service.Messages(ctx, c.ID, "", core.PageOptions{Limit: 50, After: after})
		return previewResult{channel: c.ID, generation: generation, page: page, err: err}
	}
}
