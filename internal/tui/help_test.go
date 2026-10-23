package tui

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
)

func helpText(m model) string {
	title, lines := m.helpContent(68)
	return title + "\n" + ansi.Strip(strings.Join(lines, "\n"))
}

func TestHelpPreservesInputAndCursorAndBlocksCommands(t *testing.T) {
	for _, entry := range []string{"i", "/", "ctrl+k", "e"} {
		for _, closeKey := range []string{"esc", "f1", "?"} {
			t.Run(entry+"/"+closeKey, func(t *testing.T) {
				m := loadedModel(t)
				m, _ = key(m, entry)
				m, _ = key(m, "a?b")
				updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
				m = updated.(model)
				mode, selected, offset := m.mode, m.main.selected, m.main.viewport.YOffset
				composerFocus, inputFocus := m.composer.Focused(), m.input.Focused()
				m, _ = key(m, "f1")
				if !m.help || !strings.Contains(helpText(m), "While typing, ? and q are ordinary text") {
					t.Fatal("F1 did not open input-aware help")
				}
				for _, command := range []string{"imrehR/", "enter", "tab", "ctrl+s", "ctrl+k"} {
					var cmd tea.Cmd
					m, cmd = key(m, command)
					if cmd != nil || !m.help || m.sidebarMode != "visible" || m.mode != mode || m.main.selected != selected || m.main.viewport.YOffset != offset {
						t.Fatalf("%q escaped the help modal", command)
					}
				}
				updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?qi"), Paste: true})
				m = updated.(model)
				if !m.help || cmd != nil {
					t.Fatal("pasted text closed help or ran a command")
				}
				_, cmd = key(m, "q")
				assertQuit(t, cmd)
				m, _ = key(m, closeKey)
				if m.help || m.mode != mode || m.composer.Focused() != composerFocus || m.input.Focused() != inputFocus {
					t.Fatal("closing help did not restore the input focus")
				}
				m, _ = key(m, "X?")
				value := m.input.Value()
				if entry == "i" {
					value = m.composer.Value()
				}
				if value != "a?X?b" || m.help {
					t.Fatalf("help changed the draft or cursor, or stole ?: %q", value)
				}
			})
		}
	}
}

func TestHelpCapturesSidebarCommandsAndPreservesPreview(t *testing.T) {
	m := loadedReadPreview(t)
	preview, selected, mode := m.preview, m.sidebarSelectedID(), m.sidebarMode
	read, hidden, drafts := maps.Clone(m.readState.LastRead), maps.Clone(m.hidden), maps.Clone(m.drafts)
	m, _ = key(m, "?")
	for _, command := range []string{"m", "h", "r", "enter", "tab", "ctrl+k", "/", "i", "e", "R", "jkGg"} {
		m, _ = key(m, command)
	}
	if !m.help || !m.sidebar || m.sidebarMode != mode || m.sidebarSelectedID() != selected || m.preview != preview ||
		!reflect.DeepEqual(read, m.readState.LastRead) || !reflect.DeepEqual(hidden, m.hidden) || !reflect.DeepEqual(drafts, m.drafts) {
		t.Fatal("help changed the sidebar, preview, read positions, or drafts")
	}
	m, _ = key(m, "esc")
	if m.help || !m.sidebar || m.preview != preview || m.sidebarSelectedID() != selected {
		t.Fatal("closing help lost sidebar context")
	}
}

func TestHelpShowsOnlyActionsForCurrentContext(t *testing.T) {
	m := loadedModel(t)
	if text := helpText(m); !strings.Contains(text, "Mark the open conversation read") || !strings.Contains(text, "Add an emoji reaction") {
		t.Fatal("conversation help is missing read or reaction actions")
	}
	m, _ = key(m, "tab")
	if text := helpText(m); !strings.Contains(text, "Help · Conversations") || !strings.Contains(text, "Mark the highlighted conversation read") || strings.Contains(text, "Add an emoji reaction") || strings.Contains(text, "Compose a message") {
		t.Fatal("sidebar help describes actions for the wrong focus")
	}
	m.hidden[m.sidebarSelectedID()] = true
	m.sidebarMode = localdata.All
	m.selectSidebar(m.main.conversation.ID)
	if !strings.Contains(helpText(m), "Unhide the highlighted conversation") {
		t.Fatal("sidebar help did not adapt to the selected conversation")
	}
	m, _ = key(m, "esc")
	m.searchActive = true
	m.search = m.main
	m.searchPage, m.searchPages = 1, 2
	if text := helpText(m); !strings.Contains(text, "Help · Search results") || !strings.Contains(text, "Open the selected search result's thread") || !strings.Contains(text, "Load the next search page") || strings.Contains(text, "Mark the open conversation read") || strings.Contains(text, "Compose a message") {
		t.Fatal("search help offers unavailable actions")
	}
	m.thread = &view{conversation: m.main.conversation, thread: "1.000001", messages: m.main.messages}
	if text := helpText(m); !strings.Contains(text, "Help · Thread") || !strings.Contains(text, "Close the thread") || !strings.Contains(text, "Compose a message") || strings.Contains(text, "Start a thread") {
		t.Fatal("thread help offers unavailable actions")
	}
}

func TestHelpScrollsByLineAndPageAndSurvivesResize(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "?")
	m, _ = key(m, "j")
	if m.helpOffset != 1 {
		t.Fatal("help did not scroll by one line")
	}
	_, rows := m.helpSize()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = updated.(model)
	if m.helpOffset != 1+rows {
		t.Fatal("Page Down did not scroll by one visible page")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = updated.(model)
	if m.helpOffset != 1 {
		t.Fatal("Page Up did not restore the prior page")
	}
	m, _ = key(m, "G")
	for _, size := range [][2]int{{40, 15}, {140, 40}, {30, 10}, {80, 24}} {
		updated, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = updated.(model)
		if !m.help {
			t.Fatal("resize dismissed help")
		}
		m.View() // Rendering must remain safe even below the minimum size.
	}
	m, _ = key(m, "g")
	if m.helpOffset != 0 {
		t.Fatal("g did not return help to the top")
	}
}

func TestHelpFitsAndKeepsTheWorkspaceVisible(t *testing.T) {
	for _, size := range [][2]int{{40, 15}, {70, 24}, {80, 24}, {125, 30}, {140, 40}} {
		for _, entry := range []string{"", "i", "ctrl+k", "/", "e", "tab", "enter"} {
			t.Run(fmt.Sprint(size)+"/"+entry, func(t *testing.T) {
				m := loadedModel(t)
				m.options.Version = "0.19.0"
				updated, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m = updated.(model)
				m.main.messages[0].Text = strings.Repeat("👩🏽‍💻界", 20)
				m.rebuild(&m.main, false)
				if entry != "" {
					m, _ = key(m, entry)
				}
				m, _ = key(m, "f1")
				frame := m.View()
				if w, h := lipgloss.Size(frame); w > size[0] || h > size[1] {
					t.Fatalf("help overflows: %dx%d", w, h)
				}
				if !strings.Contains(ansi.Strip(frame), "Help ·") || !strings.Contains(ansi.Strip(frame), "Esc/F1 close") {
					t.Fatalf("modal or close hint missing: %s", frame)
				}
				// Input help can cover the branding above the composer or prompt.
				if size[1] >= 24 && m.mode == "" && !strings.Contains(ansi.Strip(frame), "slakt 0.19.0") {
					t.Fatalf("background footer missing: %s", frame)
				}
				buffer := cellbuf.NewBuffer(size[0], size[1])
				cellbuf.SetContent(buffer, frame)
				if buffer.Cell(1, 0).Rune != 'O' { // Offline workspace header remains above the centered panel.
					t.Fatal("help replaced the workspace header")
				}
			})
		}
	}
}

func TestHelpAllowsSendResultsWithoutClosingOrSendingAgain(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "i")
	m, _ = key(m, "hello")
	m, send := key(m, "ctrl+s")
	m, _ = key(m, "f1")
	if !m.help || !strings.Contains(helpText(m), "Help · Sending") {
		t.Fatal("help is unavailable while sending")
	}
	updated, _ := m.Update(send())
	m = updated.(model)
	if !m.help || m.sending || m.mode != "" || m.composer.Value() != "" || strings.Contains(helpText(m), "Help · Sending") {
		t.Fatal("send result did not update the context while keeping help open")
	}
	// Poll results also update the background while help captures keyboard input.
	selected, offset := m.main.selected, m.main.viewport.YOffset
	updated, _ = m.Update(pollResult{channel: m.main.conversation.ID, generation: m.main.generation,
		main: []core.Message{{ChannelID: m.main.conversation.ID, Timestamp: fmt.Sprintf("%d.000001", time.Now().Unix()+60), Text: "arrived during help"}}})
	m = updated.(model)
	if !m.help || m.main.selected != selected || m.main.viewport.YOffset != offset || !strings.Contains(m.main.viewport.View(), "arrived during help") {
		t.Fatal("background update dismissed help, moved selection, or lost the message")
	}
	m, _ = key(m, "f1")
	assertBrowsing(t, m)
}

func TestFooterKeepsHelpDiscoverableAtMinimumWidth(t *testing.T) {
	for _, entry := range []string{"", "i", "ctrl+k", "/", "e"} {
		m := loadedModel(t)
		updated, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 15})
		m = updated.(model)
		if entry != "" {
			m, _ = key(m, entry)
		}
		want := "? help · q quit"
		if entry != "" {
			want = "F1 help · Ctrl+c quit"
		}
		if frame := ansi.Strip(m.View()); !strings.Contains(frame, want) {
			t.Fatalf("help is hidden in %q: %s", entry, frame)
		}
	}
}

func TestHelpPanelIsOpaqueAndOnlyTheWorkspaceIsDimmed(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	m := loadedModel(t)
	m, _ = key(m, "?")
	buffer := cellbuf.NewBuffer(m.width, m.height)
	cellbuf.SetContent(buffer, m.View())
	if buffer.Cell(1, 0).Style.Attrs&cellbuf.FaintAttr == 0 {
		t.Fatal("the workspace behind help is not dimmed")
	}
	width, rows := m.helpSize()
	// Locate the actual panel: the diagnostics footer can move browsing help
	// upward to keep the application version visible.
	x, y := -1, -1
	for row := 0; row < m.height && x < 0; row++ {
		for col := 0; col < m.width; col++ {
			if c := buffer.Cell(col, row); c != nil && c.Rune == '╭' {
				x, y = col, row
				break
			}
		}
	}
	if x < 0 {
		t.Fatal("help panel border missing")
	}
	background := buffer.Cell(x+1, y+1).Style.Bg
	if background == nil {
		t.Fatal("the modal has no background")
	}
	for row := y + 1; row < y+rows+5; row++ {
		for col := x + 1; col < x+width-1; col++ {
			c := buffer.Cell(col, row)
			if c.Style.Bg != background || c.Style.Attrs&cellbuf.FaintAttr != 0 {
				t.Fatalf("nested style lost the modal background or dimmed its content at (%d,%d)", col, row)
			}
		}
	}
}
