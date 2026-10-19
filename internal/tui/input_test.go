package tui

import (
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
)

func assertQuit(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("quit command missing")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("got %T, want QuitMsg", msg)
	}
}

func assertBrowsing(t *testing.T, m model) {
	t.Helper()
	if m.mode != "" || m.composer.Focused() || m.input.Focused() {
		t.Fatalf("unexpected text entry: mode=%q composer=%v prompt=%v", m.mode, m.composer.Focused(), m.input.Focused())
	}
}

func TestQuitWorksOutsideTextEntry(t *testing.T) {
	for _, state := range []string{"channel", "thread", "sidebar", "help", "loading", "sending", "search results", "undersized input"} {
		for _, quitKey := range []string{"q", "qqq", "ctrl+c"} {
			t.Run(state+"/"+quitKey, func(t *testing.T) {
				m := loadedModel(t)
				switch state {
				case "thread":
					m, _ = key(m, "enter")
				case "sidebar":
					m, _ = key(m, "tab")
				case "help":
					m, _ = key(m, "?")
				case "loading":
					m.main.loading = true
				case "sending":
					m, _ = key(m, "i")
					m, _ = key(m, "a message")
					m, _ = key(m, "ctrl+s")
				case "search results":
					m.searchActive = true
				case "undersized input":
					m, _ = key(m, "i")
					m.width = 20
				}
				_, cmd := key(m, quitKey)
				assertQuit(t, cmd)
			})
		}
	}
}

func TestModeKeyFollowsPlainQuitAvailabilityWithoutChangingFocus(t *testing.T) {
	for _, state := range []string{"channel", "thread", "sidebar", "help", "loading", "sending", "search results", "undersized input"} {
		t.Run(state, func(t *testing.T) {
			m := loadedModel(t)
			switch state {
			case "thread":
				m, _ = key(m, "enter")
			case "sidebar":
				m, _ = key(m, "tab")
			case "help":
				m, _ = key(m, "?")
			case "loading":
				m.main.loading = true
			case "sending":
				m, _ = key(m, "i")
				m, _ = key(m, "a message")
				m, _ = key(m, "ctrl+s")
			case "search results":
				m.searchActive = true
			case "undersized input":
				m, _ = key(m, "i")
				m.width = 20
			}
			_, quit := key(m, "q")
			assertQuit(t, quit)
			changed, cmd := key(m, "m")
			if changed.sidebarMode != localdata.UnreadFirst || cmd != nil {
				t.Fatal("m did not cycle the mode where plain q is available")
			}
			if changed.sidebar != m.sidebar || changed.help != m.help || changed.mode != m.mode ||
				changed.sending != m.sending || changed.searchActive != m.searchActive || changed.thread != m.thread ||
				changed.composer.Focused() != m.composer.Focused() || changed.input.Focused() != m.input.Focused() ||
				changed.main.conversation.ID != m.main.conversation.ID || changed.main.selected != m.main.selected ||
				changed.composer.Value() != m.composer.Value() {
				t.Fatal("m changed focus, selection, input, or pending operation")
			}
			changed, _ = key(changed, "mm")
			if changed.sidebarMode != localdata.All {
				t.Fatal("batched m keys did not cycle in order")
			}
			changed, cmd = key(changed, "mqm")
			assertQuit(t, cmd)
			if changed.sidebarMode != localdata.UnreadFirst {
				t.Fatal("batched m after quit should not execute")
			}
		})
	}
}

func TestModeKeyStaysLiteralInEveryTextEntryMode(t *testing.T) {
	for _, entry := range []string{"i", "/", "ctrl+k", "r"} {
		t.Run(entry, func(t *testing.T) {
			m := loadedModel(t)
			m, _ = key(m, entry)
			m, _ = key(m, "mmm")
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Paste: true})
			m = updated.(model)
			value := m.input.Value()
			if entry == "i" {
				value = m.composer.Value()
			}
			if value != "mmmm" || m.sidebarMode != localdata.All {
				t.Fatalf("m activated a command in text entry: input=%q mode=%q", value, m.sidebarMode)
			}
		})
	}
	for _, input := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'m'}, Paste: true},
		{Type: tea.KeyRunes, Runes: []rune("mm"), Paste: true},
		{Type: tea.KeyRunes, Runes: []rune{'m'}, Alt: true},
	} {
		m, cmd := loadedModel(t).Update(input)
		if m.(model).sidebarMode != localdata.All || cmd != nil {
			t.Fatal("pasted or modified m activated a command")
		}
	}
}

func TestUndersizedTerminalCannotStartHiddenTextEntry(t *testing.T) {
	m := loadedModel(t)
	m.width = 20
	for _, entry := range []string{"i", "/", "ctrl+k", "r"} {
		m, _ = key(m, entry)
		assertBrowsing(t, m)
	}
}

func TestQuitCharactersRemainTextInExplicitInputModes(t *testing.T) {
	for _, entry := range []string{"i", "/", "ctrl+k", "r"} {
		t.Run(entry, func(t *testing.T) {
			m := loadedModel(t)
			assertBrowsing(t, m)
			m, _ = key(m, entry)
			m, _ = key(m, "qqq")
			value := m.input.Value()
			if entry == "i" {
				value = m.composer.Value()
			}
			if value != "qqq" {
				t.Fatalf("q did not remain text: %q", value)
			}
			_, cmd := key(m, "ctrl+c")
			assertQuit(t, cmd)
			// Terminals also encode a rapid Escape, q as Alt+q.
			_, cmd = key(m, "alt+q")
			assertQuit(t, cmd)
			m, _ = key(m, "esc")
			assertBrowsing(t, m)
			_, cmd = key(m, "q")
			assertQuit(t, cmd)
		})
	}
}

func TestBatchedKeysRespectExplicitEntryAndStopAtQuit(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "giquiet")
	if m.main.selected != 0 || m.mode != "compose" || m.composer.Value() != "quiet" {
		t.Fatalf("batched commands did not enter text explicitly: mode=%q value=%q", m.mode, m.composer.Value())
	}
	m, _ = key(m, "esc")
	m, cmd := key(m, "gqi")
	assertQuit(t, cmd)
	assertBrowsing(t, m) // The i after q must not enter the composer.
}

func TestPastedCommandsNeverEnterTextOrQuit(t *testing.T) {
	m := loadedModel(t)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("iqqq"), Paste: true}
	mm, cmd := m.Update(msg)
	m = mm.(model)
	assertBrowsing(t, m)
	if cmd != nil {
		t.Fatal("paste triggered a command")
	}
	m, _ = key(m, "i")
	mm, _ = m.Update(msg)
	if got := mm.(model).composer.Value(); got != "iqqq" {
		t.Fatalf("paste did not remain literal text: %q", got)
	}
}

func TestNavigationAndBackgroundUpdatesDoNotEnterText(t *testing.T) {
	for _, event := range []string{"resize", "messages", "poll", "search", "reaction", "directory", "dm", "editor"} {
		t.Run(event, func(t *testing.T) {
			m := loadedModel(t)
			var msg tea.Msg
			switch event {
			case "resize":
				msg = tea.WindowSizeMsg{Width: 70, Height: 24}
			case "messages":
				msg = messagesResult{channel: m.main.conversation.ID, generation: m.main.generation, page: core.MessagePage{Messages: m.main.messages}}
			case "poll":
				msg = pollResult{channel: m.main.conversation.ID, generation: m.main.generation, main: m.main.messages}
			case "search":
				msg = searchResult{query: m.searchQuery, result: core.SearchResult{Messages: m.main.messages}}
			case "reaction":
				msg = reactionResult{channel: m.main.conversation.ID, ts: m.main.messages[0].Timestamp, name: "thumbsup"}
			case "directory":
				msg = m.directoryCmd()()
			case "dm":
				msg = dmResult{conversation: core.Conversation{ID: "DNEW", Name: "someone", Kind: "dm"}}
			case "editor":
				msg = editorResult{text: "restored draft"}
			}
			mm, _ := m.Update(msg)
			assertBrowsing(t, mm.(model))
		})
	}
	m := loadedModel(t)
	m.drafts[draftKey(m.main.conversation.ID, "")] = "saved draft"
	for _, navigation := range []string{"tab", "enter", "esc", "enter", "esc", "j", "k", "R"} {
		var cmd tea.Cmd
		m, cmd = key(m, navigation)
		assertBrowsing(t, m)
		if cmd != nil {
			mm, _ := m.Update(cmd())
			m = mm.(model)
			assertBrowsing(t, m)
		}
	}
}
