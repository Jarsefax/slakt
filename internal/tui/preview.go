package tui

import (
	"context"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
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
	m.previewError = ""
}

// Run after each update so filtering and background unread reordering use the
// same selected conversation as keyboard navigation. The committed views and
// drafts stay intact until Enter explicitly opens a conversation.
func (m *model) syncPreview() tea.Cmd {
	if !m.sidebar || m.mode != "" {
		m.clearPreview()
		return nil
	}
	if m.help {
		return nil
	}
	items := m.sidebarConversations()
	if len(items) == 0 {
		m.clearPreview()
		return nil
	}
	m.sideIndex = min(max(0, m.sideIndex), len(items)-1)
	c := items[m.sideIndex]
	if c.ID == m.main.conversation.ID {
		m.clearPreview()
		return nil
	}
	if m.preview != nil && m.preview.conversation.ID == c.ID {
		return nil
	}
	m.clearPreview()
	m.previewGeneration++
	generation := m.previewGeneration
	m.preview = &view{conversation: c, loading: true, generation: generation, viewport: viewport.New(60, 15)}
	m.rebuild(m.preview, false)
	ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
	m.previewCancel = cancel
	service := m.service
	return func() tea.Msg {
		defer cancel()
		page, err := service.Messages(ctx, c.ID, "", core.PageOptions{Limit: 50})
		return previewResult{channel: c.ID, generation: generation, page: page, err: err}
	}
}
