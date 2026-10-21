package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
)

// The dimensions include the border and padding. Keep some of the workspace
// visible even at the minimum terminal size, and use scrolling for long guides.
func (m model) helpSize() (width, rows int) {
	width = max(20, min(72, m.width-4))
	height := min(m.height-2, max(13, m.height*7/10))
	return width, max(1, height-6)
}

func (m model) commandHints() string {
	if m.help {
		return " Esc/F1 close · q quit"
	}
	if m.enteringText() {
		return " F1 help · Ctrl+c quit"
	}
	return " ? help · q quit"
}

func (m model) helpContent(width int) (string, []string) {
	var lines []string
	text := func(value string) {
		lines = append(lines, strings.Split(ansi.Wrap(value, width, ""), "\n")...)
	}
	action := func(key, description string) {
		const keyWidth = 12
		wrapped := strings.Split(ansi.Wrap(description, max(1, width-keyWidth-1), ""), "\n")
		for i, line := range wrapped {
			prefix := strings.Repeat(" ", keyWidth+1)
			if i == 0 {
				prefix = accent.Render(key+strings.Repeat(" ", max(0, keyWidth-ansi.StringWidth(key)))) + " "
			}
			lines = append(lines, prefix+line)
		}
	}
	section := func(label string) {
		lines = append(lines, "", titleStyle.Render(label))
	}

	title := "Help · Conversation"
	switch {
	case m.sending:
		title = "Help · Sending"
		text("Sending your message… You can leave help open while it finishes.")
	case m.mode == "compose":
		title = "Help · Compose"
		action("Ctrl+s", "Send draft, then browse")
		action("Enter", "Insert a newline")
		action("Esc", "Keep draft and return to browsing")
		action("Ctrl+e", "Edit draft in your external editor")
	case m.mode == "jump":
		title = "Help · Jump"
		action("↑/↓", "Choose a conversation or person")
		action("Ctrl+n/p", "Next / previous choice")
		action("Enter", "Open the selected destination")
		action("Esc", "Cancel jump")
	case m.mode == "search":
		title = "Help · Search"
		action("Enter", "Search Slack for your query")
		action("Esc", "Cancel search")
	case m.mode == "react":
		title = "Help · Reaction"
		action("Enter", "Add the named emoji reaction")
		action("Esc", "Cancel reaction")
		text("Use an emoji name such as thumbsup.")
	case m.sidebar:
		title = "Help · Conversations"
		action("j/k · ↑/↓", "Preview conversations")
		if m.sidebarSelectedID() != "" {
			action("Enter", "Open the highlighted conversation")
			action("r", "Mark the highlighted conversation read")
			label := "Hide the highlighted conversation"
			if m.hidden[m.sidebarSelectedID()] {
				label = "Unhide the highlighted conversation"
			}
			action("h", label)
		}
		action("Tab / Esc", "Return to the original view")
	default:
		v := m.active()
		if m.thread != nil {
			title = "Help · Thread"
		} else if m.searchActive {
			title = "Help · Search results"
		}
		if len(v.messages) > 0 {
			if m.thread == nil {
				label := "Start a thread on the selected message"
				if m.searchActive {
					label = "Open the selected search result's thread"
				} else if threadIndicator(v.messages[v.selected]) != "" {
					label = "Open the selected message's thread"
				}
				action("Enter", label)
			}
			action("e", "Add an emoji reaction")
			action("o", "Open the selected message in Slack")
		}
		if (!m.searchActive || m.thread != nil) && v.conversation.ID != "" && !v.loading {
			action("i", "Compose a message")
		}
		if !m.searchActive && m.main.conversation.ID != "" && !m.main.loading {
			action("r", "Mark the open conversation read")
		}
		if len(v.messages) > 0 {
			action("j/k · ↑/↓", "Select a message")
			action("g/G", "Select first / last loaded message")
			action("PgUp/PgDn", "Scroll long messages")
			action("Ctrl+u/d", "Scroll up / down half a page")
		}
		if m.searchActive && m.thread == nil {
			if !m.search.loading && m.searchPage < m.searchPages {
				action("n", "Load the next search page")
			}
		} else if v.hasMore && !v.loading {
			action("n", "Load more history")
		}
		if !v.loading && !m.searchActive {
			action("R", "Reload this conversation")
		}
		if m.thread != nil {
			action("Esc", "Close the thread")
		} else if m.searchActive {
			action("Esc", "Close search results")
		}
		if v.loading {
			text("Loading messages…")
		}
	}

	// Describe shortcuts for the underlying focus, not actions that would type
	// command characters into an input once this modal closes.
	if m.mode == "" {
		section("Navigation")
		action("Ctrl+k", "Jump to a conversation or person")
		action("Tab", "Focus / leave the conversation sidebar")
		action("/", "Search Slack")
		action("m", "Cycle sidebar mode: "+m.sidebarMode.Label())
	}
	section("Help and quit")
	action("F1", "Open / close help from any input")
	if m.mode == "" {
		action("?", "Open / close help while browsing")
	}
	action("Esc", "Close help and restore your focus")
	action("q", "Quit while help is open or browsing")
	action("Ctrl+c", "Quit anywhere")
	if m.mode != "" && !m.sending {
		text("While typing, ? and q are ordinary text. Esc then q leaves text entry and quits.")
	}

	if m.mode == "" {
		section("Reading the workspace")
		text("● unread conversation · × hidden conversation")
		text("↳ Thread means a message has replies. The selected message shows its position; other numbers show distance from it.")
		text("The poll countdown names the open conversation (+ thread when open), not the sidebar preview. Its interval starts after each refresh; unread checks run separately.")
		if m.sidebar {
			text("Previews do not mark conversations read. In Unread only, marking a row read keeps its preview until you move away.")
			text("Modes: Visible / Unread first / Unread only / Hidden / All. Visible and unread modes exclude hidden conversations; Ctrl+k and search can still find them.")
		} else {
			text("Startup restores the last conversation without marking it read. Use r or explicitly reopen it to mark it read.")
		}
	}
	section("Drafts")
	text("Drafts stay in this process, separately for each conversation and thread. Quitting discards them. Messages refresh by polling.")
	return title, lines
}

func (m model) handleHelpKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Paste {
		return m, nil
	}
	width, rows := m.helpSize()
	_, lines := m.helpContent(width - 4)
	limit := max(0, len(lines)-rows)
	m.helpOffset = min(m.helpOffset, limit)
	switch key.String() {
	case "esc", "f1", "?":
		m.help = false
	case "j", "down":
		m.helpOffset = min(limit, m.helpOffset+1)
	case "k", "up":
		m.helpOffset = max(0, m.helpOffset-1)
	case "pgdown", "ctrl+d":
		m.helpOffset = min(limit, m.helpOffset+rows)
	case "pgup", "ctrl+u":
		m.helpOffset = max(0, m.helpOffset-rows)
	case "g", "home":
		m.helpOffset = 0
	case "G", "end":
		m.helpOffset = limit
	}
	return m, nil
}

func (m model) helpOverlay(frame string) string {
	width, rows := m.helpSize()
	contentWidth := width - 4
	title, lines := m.helpContent(contentWidth)
	start := min(m.helpOffset, max(0, len(lines)-rows))
	visible := append([]string(nil), lines[start:min(len(lines), start+rows)]...)
	for len(visible) < rows {
		visible = append(visible, "")
	}
	controls := "↑/↓ scroll · Esc/F1 close"
	if contentWidth >= 50 {
		controls += fmt.Sprintf(" · %d–%d/%d", start+1, min(len(lines), start+rows), len(lines))
	}
	content := titleStyle.Render(fit(title, contentWidth)) + "\n\n" + strings.Join(visible, "\n") + "\n\n" + muted.Render(controls)
	panel := lipgloss.NewStyle().Width(contentWidth+2).Padding(0, 1).
		Border(lipgloss.RoundedBorder()).BorderForeground(borderColor).
		Foreground(lipgloss.Color("#E8EDF7")).Background(lipgloss.Color("#151C2D")).Render(content)
	panelWidth, panelHeight := lipgloss.Size(panel)
	x, y := (m.width-panelWidth)/2, (m.height-panelHeight)/2
	canvas := cellbuf.NewBuffer(m.width, m.height)
	cellbuf.SetContent(canvas, frame)
	for row := range m.height {
		for col := range m.width {
			if c := canvas.Cell(col, row); c != nil {
				c.Style.Attrs = c.Style.Attrs&^cellbuf.BoldAttr | cellbuf.FaintAttr
			}
		}
	}
	// Drawing in terminal cells keeps emoji and wide characters intact where
	// the opaque panel intersects a message or sidebar label.
	cellbuf.SetContentRect(canvas, panel, cellbuf.Rect(x, y, panelWidth, panelHeight))
	// Nested text styles can reset the panel's background. Use its padded
	// corner as the color-profile-aware background for the whole interior.
	background := canvas.Cell(x+1, y+1).Style.Bg
	for row := y + 1; row < y+panelHeight-1; row++ {
		for col := x + 1; col < x+panelWidth-1; col++ {
			c := canvas.Cell(col, row)
			c.Style.Bg = background
			canvas.SetCell(col, row, c)
		}
	}
	return strings.ReplaceAll(cellbuf.Render(canvas), "\r\n", "\n")
}
