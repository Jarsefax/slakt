package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func newMessagesModel(t *testing.T) (model, *unreadBackend) {
	t.Helper()
	m := loadedModel(t)
	b := &unreadBackend{Backend: m.service.Backend}
	for i := 1; i <= 20; i++ {
		b.page.Messages = append(b.page.Messages, core.Message{
			ChannelID: "CGENERAL", Timestamp: fmt.Sprintf("2000000000.%06d", i),
			User: "Alice", Text: fmt.Sprintf("Message %d", i),
		})
	}
	m.service = core.New(b)
	m.readState.LastRead["CGENERAL"] = b.page.Messages[5].Timestamp
	m.latest["CGENERAL"] = b.page.Messages[19].Timestamp
	return m, b
}

func openGeneral(t *testing.T, m model) model {
	t.Helper()
	for _, c := range m.conversations {
		if c.ID == "CGENERAL" {
			cmd := m.switchTo(c)
			updated, _ := m.Update(cmd())
			return updated.(model)
		}
	}
	t.Fatal("missing general conversation")
	return m
}

func TestConversationOpensAtNewMessagesBeforeMarkingRead(t *testing.T) {
	for _, state := range []string{"saved position", "account baseline", "all read", "all new"} {
		t.Run(state, func(t *testing.T) {
			m, b := newMessagesModel(t)
			wantIndex := 6
			switch state {
			case "account baseline":
				m.readState.StartedAt = m.readState.LastRead["CGENERAL"]
				delete(m.readState.LastRead, "CGENERAL")
			case "all read":
				m.readState.LastRead["CGENERAL"] = b.page.Messages[19].Timestamp
				wantIndex = -1
			case "all new":
				m.readState.LastRead["CGENERAL"] = "2000000000.000000"
				wantIndex = 0
			}
			after := m.readState.Position("CGENERAL")
			m = openGeneral(t, m)
			if m.main.newAfter != after || m.main.newIndex != wantIndex {
				t.Fatalf("boundary = %q/%d, want %q/%d", m.main.newAfter, m.main.newIndex, after, wantIndex)
			}
			if m.isUnread("CGENERAL") || m.readState.Position("CGENERAL") != b.page.Messages[19].Timestamp {
				t.Fatal("opening no longer marks the loaded history read")
			}
			if wantIndex < 0 {
				if m.main.selected != 19 || strings.Contains(messageViewContent(m.main), "New messages") {
					t.Fatal("read conversation did not open at its latest message without a divider")
				}
				return
			}
			frame := ansi.Strip(m.main.viewport.View())
			if m.main.selected != wantIndex || !strings.Contains(frame, "New messages") || !strings.Contains(frame, b.page.Messages[wantIndex].Text) {
				t.Fatalf("first new message and divider were not revealed:\n%s", frame)
			}
			if wantIndex > 0 && !strings.Contains(frame, b.page.Messages[wantIndex-1].Text) {
				t.Fatalf("opening omitted the older context:\n%s", frame)
			}
			content := messageViewContent(m.main)
			if strings.Count(content, "New messages") != 1 || !strings.Contains(strings.Split(content, "\n")[m.main.starts[wantIndex]-1], "New messages") {
				t.Fatalf("divider is not immediately above the first new message:\n%s", content)
			}
		})
	}
}

func TestNewMessagesBoundarySurvivesVisitAndResetsOnReopen(t *testing.T) {
	m, b := newMessagesModel(t)
	m = openGeneral(t, m)
	after, selected := m.main.newAfter, m.main.messages[m.main.selected].Timestamp
	assertBoundary := func() {
		t.Helper()
		if m.main.newAfter != after || m.main.newIndex != 6 || m.main.messages[m.main.selected].Timestamp != selected || strings.Count(messageViewContent(m.main), "New messages") != 1 {
			t.Fatal("visit lost its original boundary or selection")
		}
	}
	updated, _ := m.Update(pollResult{channel: "CGENERAL", generation: m.main.generation, main: []core.Message{{ChannelID: "CGENERAL", Timestamp: "2000000000.000021", Text: "Live arrival"}}})
	m = updated.(model)
	assertBoundary()
	for _, keys := range [][]string{{"?", "esc"}, {"tab", "esc"}, {"enter", "esc"}} {
		for _, k := range keys {
			m, _ = key(m, k)
		}
		assertBoundary()
	}
	for _, width := range []int{40, 125, 140} {
		updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = updated.(model)
		assertBoundary()
	}
	m, cmd := key(m, "R")
	updated, _ = m.Update(cmd())
	m = updated.(model)
	assertBoundary()

	// A subsequent visit starts from the advanced read position.
	m = openGeneral(t, m)
	if m.main.newAfter != "2000000000.000021" || m.main.newIndex != -1 || strings.Contains(messageViewContent(m.main), "New messages") {
		t.Fatal("reopening reused the previous visit's new-message boundary")
	}
	// New arrivals can create a boundary in a conversation opened fully read.
	b.page.Messages = append(b.page.Messages, core.Message{ChannelID: "CGENERAL", Timestamp: "2000000000.000022", Text: "Next arrival"})
	updated, _ = m.Update(pollResult{channel: "CGENERAL", generation: m.main.generation, main: b.page.Messages[20:]})
	m = updated.(model)
	if m.main.newIndex != 20 || !strings.Contains(messageViewContent(m.main), "New messages") {
		t.Fatal("new arrival did not create a divider after the captured read position")
	}
}

func TestExplicitMarkReadClearsDividerAndAllowsNewArrivals(t *testing.T) {
	for _, state := range []struct {
		name    string
		startup bool
		allNew  bool
	}{
		{name: "browsing"},
		{name: "startup", startup: true},
		{name: "all new", allNew: true},
		{name: "startup all new", startup: true, allNew: true},
	} {
		for _, width := range []int{70, 140} {
			t.Run(fmt.Sprintf("%s/%d", state.name, width), func(t *testing.T) {
				m, b := newMessagesModel(t)
				m.width = width
				if state.allNew {
					m.readState.LastRead["CGENERAL"] = "2000000000.000000"
				}
				open := m.switchTo
				if state.startup {
					open = m.switchToStartup
				}
				load := open(core.Conversation{ID: "CGENERAL", Name: "general", Kind: "channel"})
				updated, _ := m.Update(load())
				m = updated.(model)
				if m.main.newIndex < 0 || !strings.Contains(messageViewContent(m.main), "New messages") {
					t.Fatal("test conversation did not start with a new-message divider")
				}
				// Scroll inside a message below the divider so removing the first
				// row of entirely new history must retain the same visual anchor.
				m.main.selected = 9
				m.rebuild(&m.main, true)
				m.main.viewport.SetYOffset(m.main.starts[9] + 1)
				selected := m.main.messages[9].Timestamp
				delta := m.main.viewport.YOffset - m.main.starts[9]
				after := b.page.Messages[19].Timestamp
				m, _ = key(m, "r")
				if m.main.startup || m.main.newAfter != after || m.main.newIndex != -1 || strings.Contains(messageViewContent(m.main), "New messages") {
					t.Fatal("r did not reset the boundary and remove the divider")
				}
				if m.main.messages[m.main.selected].Timestamp != selected || m.main.viewport.YOffset-m.main.starts[m.main.selected] != delta {
					t.Fatal("clearing the divider moved the selected message or scroll anchor")
				}
				updated, _ = m.Update(tea.WindowSizeMsg{Width: width + 5, Height: m.height})
				m = updated.(model)
				if m.main.newIndex != -1 || strings.Contains(messageViewContent(m.main), "New messages") {
					t.Fatal("resizing brought back the cleared divider")
				}
				arrival := core.Message{ChannelID: "CGENERAL", Timestamp: "2000000000.000021", Text: "After explicit mark read"}
				updated, _ = m.Update(pollResult{channel: "CGENERAL", generation: m.main.generation, main: []core.Message{arrival}})
				m = updated.(model)
				if m.main.newAfter != after || m.main.newIndex != 20 || strings.Count(messageViewContent(m.main), "New messages") != 1 || m.isUnread("CGENERAL") {
					t.Fatal("later arrival did not create a fresh divider while normal read tracking resumed")
				}
				m, _ = key(m, "r")
				if m.main.newAfter != arrival.Timestamp || m.main.newIndex != -1 || strings.Contains(messageViewContent(m.main), "New messages") {
					t.Fatal("a second r did not clear the fresh divider")
				}
			})
		}
	}
}

func TestExplicitMarkReadRestoresTodayAtSharedDivider(t *testing.T) {
	m, b := newMessagesModel(t)
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	older, fresh := messageAt(start.Add(-time.Minute)), messageAt(start)
	b.page.Messages = []core.Message{older, fresh, messageAt(start.Add(time.Minute))}
	m.latest["CGENERAL"] = newestTimestamp(b.page.Messages)
	m.readState.StartedAt = older.Timestamp
	m.readState.LastRead["CGENERAL"] = older.Timestamp
	m = openGeneral(t, m)
	if m.main.newIndex != 1 || m.main.todayIndex != 1 {
		t.Fatal("test conversation did not start with a shared divider")
	}
	m, _ = key(m, "r")
	content := messageViewContent(m.main)
	if m.main.newIndex != -1 || m.main.todayIndex != 1 || strings.Contains(content, "New messages") || !strings.Contains(content, ansi.Strip(todaySeparator(m.main.viewport.Width))) {
		t.Fatal("clearing the shared new-message divider did not restore Today")
	}
}

func TestNewMessagesNavigationAndPaginationKeepBoundaryVisible(t *testing.T) {
	m, b := newMessagesModel(t)
	m.height = 15
	// The boundaries coincide; the smallest supported pane must reveal just
	// New messages along with the author and timestamp when navigating back.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	older, fresh := messageAt(start.Add(-time.Minute)), messageAt(start)
	b.page.Messages = []core.Message{older, fresh, messageAt(start.Add(time.Minute))}
	m.readState.StartedAt = older.Timestamp
	m.readState.LastRead["CGENERAL"] = older.Timestamp
	m = openGeneral(t, m)
	if m.main.selected != 1 || m.main.todayIndex != 1 || m.main.newIndex != 1 {
		t.Fatal("Today and New messages did not share the boundary")
	}
	for _, k := range []string{"G", "k"} {
		m, _ = key(m, k)
	}
	frame := ansi.Strip(m.main.viewport.View())
	if !strings.Contains(frame, ansi.Strip(newMessagesSeparator(m.main.viewport.Width))) || !strings.Contains(frame, "Today 00:00") || !strings.Contains(frame, "Alice") {
		t.Fatalf("navigation hid the new-message boundary or timestamp:\n%s", frame)
	}
	content := messageViewContent(m.main)
	if strings.Contains(content, ansi.Strip(todaySeparator(m.main.viewport.Width))) || strings.Count(content, "New messages") != 1 {
		t.Fatalf("shared boundary rendered an extra divider:\n%s", content)
	}
	if m.main.starts[1] != m.main.starts[0]+3 {
		t.Fatal("shared boundary added an extra row")
	}
	m.main.messages[1].Text = strings.Repeat("A long new message.\n", 20)
	m.rebuild(&m.main, false)
	m.main.viewport.GotoBottom()
	m.reveal(&m.main)
	if frame := ansi.Strip(m.main.viewport.View()); !strings.Contains(frame, "New messages") || !strings.Contains(frame, "Alice") {
		t.Fatalf("long-message navigation hid the new-message boundary or author:\n%s", frame)
	}
	// Loading earlier unread messages moves the divider to the earliest loaded
	// new message while preserving the message under the cursor and its offset.
	m, _ = newMessagesModel(t)
	m = openGeneral(t, m)
	m.main.messages = m.main.messages[10:]
	m.main.selected = 0
	m.rebuild(&m.main, false)
	m.main.viewport.SetYOffset(m.main.starts[0] + 1)
	selected := m.main.messages[0].Timestamp
	m.merge(&m.main, []core.Message{{Timestamp: "2000000000.000006"}, {Timestamp: "2000000000.000007"}}, false, true)
	if m.main.messages[m.main.selected].Timestamp != selected || m.main.newIndex != 1 || m.main.viewport.YOffset != m.main.starts[m.main.selected]+1 {
		t.Fatal("pagination moved the selection, scroll anchor, or unread boundary")
	}
	for _, width := range []int{40, 125, 140} {
		m.width = width
		m.rebuild(&m.main, true)
		if w, h := lipgloss.Size(m.View()); w > m.width || h > m.height {
			t.Fatalf("divider overflowed the terminal: %dx%d", w, h)
		}
	}
}

func TestSidebarPreviewShowsNewMessagesWithoutMarkingRead(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.Visible, localdata.UnreadOnly} {
		t.Run(string(mode), func(t *testing.T) {
			m, _ := newMessagesModel(t)
			m.sidebarMode = mode
			m, _ = key(m, "tab")
			m, cmd := key(m, "j")
			updated, _ := m.Update(cmd())
			m = updated.(model)
			after := m.readState.Position("CGENERAL")
			if m.preview == nil || m.preview.newAfter != after || !strings.Contains(ansi.Strip(m.preview.viewport.View()), "New messages") {
				t.Fatal("preview did not reveal the new-message boundary")
			}
			m, _ = key(m, "r")
			if m.preview.newAfter != after || !strings.Contains(messageViewContent(*m.preview), "New messages") {
				t.Fatal("marking read erased the preview boundary")
			}
			m, cmd = key(m, "enter")
			updated, _ = m.Update(cmd())
			m = updated.(model)
			if m.main.newIndex != -1 {
				t.Fatal("opening a manually read preview reused its previous unread boundary")
			}
		})
	}
}
