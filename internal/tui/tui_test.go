package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func loadedModel(t *testing.T) model {
	t.Helper()
	s := core.New(demo.New())
	m := newModel(context.Background(), s, Options{PollInterval: 15 * time.Second, Demo: true})
	mm, cmd := m.Update(m.directoryCmd()())
	m = mm.(model)
	if cmd == nil {
		t.Fatal("no initial history request")
	}
	mm, _ = m.Update(cmd())
	return mm.(model)
}

func key(m model, k string) (model, tea.Cmd) {
	var msg tea.KeyMsg
	switch k {
	case "esc":
		msg.Type = tea.KeyEsc
	case "enter":
		msg.Type = tea.KeyEnter
	case "ctrl+s":
		msg.Type = tea.KeyCtrlS
	case "tab":
		msg.Type = tea.KeyTab
	default:
		msg.Type = tea.KeyRunes
		msg.Runes = []rune(k)
	}
	mm, cmd := m.Update(msg)
	return mm.(model), cmd
}

func TestSidebarSectionsSkipHeadingsDuringNavigation(t *testing.T) {
	m := loadedModel(t)
	frame := ansi.Strip(m.sidebarView(22, 20))
	previous := -1
	for _, heading := range []string{"Starred", "Channels", "Direct messages", "Group messages"} {
		position := strings.Index(frame, heading)
		if position <= previous {
			t.Fatalf("missing or misplaced %q: %s", heading, frame)
		}
		previous = position
	}
	if strings.Count(frame, "#engineering") != 1 {
		t.Fatalf("starred conversation duplicated: %s", frame)
	}
	m, _ = key(m, "tab")
	for i := range m.conversations {
		if m.sideIndex != i {
			t.Fatalf("navigation stopped at a heading: index %d, want %d", m.sideIndex, i)
		}
		selected, cmd := key(m, "enter")
		if cmd == nil || selected.main.conversation.ID != m.conversations[i].ID {
			t.Fatalf("Enter opened the wrong conversation at index %d", i)
		}
		m, _ = key(m, "j")
	}
	for i := len(m.conversations) - 1; i >= 0; i-- {
		if m.sideIndex != i {
			t.Fatalf("reverse navigation stopped at a heading: index %d, want %d", m.sideIndex, i)
		}
		m, _ = key(m, "k")
	}
}

func TestSidebarScrollKeepsSelectedConversationAndSectionVisible(t *testing.T) {
	m := loadedModel(t)
	m.conversations = nil
	for i := range 40 {
		m.conversations = append(m.conversations, core.Conversation{ID: fmt.Sprintf("D%02d", i), Name: fmt.Sprintf("person%02d", i), Kind: "dm"})
	}
	m.sidebar = true
	for i, c := range m.conversations {
		m.sideIndex = i
		frame := ansi.Strip(m.sidebarView(22, 6))
		if !strings.Contains(frame, c.Label()) || !strings.Contains(frame, "Direct messages") {
			t.Fatalf("selection or heading scrolled away at %d: %s", i, frame)
		}
		if width, height := lipgloss.Size(frame); width > 23 || height > 6 {
			t.Fatalf("sidebar overflow: %dx%d", width, height)
		}
	}
	last := m.conversations[len(m.conversations)-1]
	m.switchTo(last)
	m.sideIndex = 0 // A jump prompt can change this while the sidebar is unfocused.
	if frame := ansi.Strip(m.sidebarView(22, 6)); !strings.Contains(frame, last.Label()) {
		t.Fatalf("active conversation scrolled away: %s", frame)
	}
	m, _ = key(m, "tab")
	if m.sideIndex != len(m.conversations)-1 {
		t.Fatal("sidebar focus did not start at the active conversation")
	}
}

func TestSidebarHidesEmptySectionsAndGroupsNewDMs(t *testing.T) {
	m := loadedModel(t)
	m.conversations = []core.Conversation{{ID: "C1", Name: "general", Kind: "channel"}}
	mm, _ := m.Update(dmResult{conversation: core.Conversation{ID: "DNEW", Name: "aaron", Kind: "dm"}})
	m = mm.(model)
	frame := ansi.Strip(m.sidebarView(22, 20))
	if strings.Contains(frame, "Starred") || strings.Contains(frame, "Group messages") || !strings.Contains(frame, "Direct messages") || !strings.Contains(frame, "@aaron") {
		t.Fatalf("incorrect sections after opening a DM: %s", frame)
	}
	if m.sideIndex != 1 || m.conversations[m.sideIndex].ID != "DNEW" {
		t.Fatal("new DM was not selected in its section")
	}
}

func TestDraftsSurviveConversationSwitchesAndThreadContext(t *testing.T) {
	m := loadedModel(t)
	original := m.main.conversation
	m, _ = key(m, "i")
	m, _ = key(m, "unsent draft")
	m, _ = key(m, "esc")
	if m.drafts[draftKey(original.ID, "")] != "unsent draft" {
		t.Fatalf("draft missing: %#v", m.drafts)
	}
	cmd := m.switchTo(core.Conversation{ID: "CGENERAL", Name: "general", Kind: "channel"})
	mm, _ := m.Update(cmd())
	m = mm.(model)
	cmd = m.switchTo(original)
	mm, _ = m.Update(cmd())
	m = mm.(model)
	m, _ = key(m, "i")
	if m.composer.Value() != "unsent draft" {
		t.Fatal("draft was not restored")
	}
	m, _ = key(m, "esc")
	var load tea.Cmd
	m, load = key(m, "enter")
	if m.thread == nil || load == nil {
		t.Fatal("thread did not open")
	}
	mm, _ = m.Update(load())
	m = mm.(model)
	m, _ = key(m, "i")
	m, _ = key(m, "thread draft")
	if m.drafts[draftKey(original.ID, m.thread.thread)] != "thread draft" || m.drafts[draftKey(original.ID, "")] != "unsent draft" {
		t.Fatal("thread draft overwrote channel draft")
	}
}

func TestIncomingMessagesKeepSelectionAndIgnoreStaleResponses(t *testing.T) {
	m := loadedModel(t)
	m.main.selected = 0
	selected := m.main.messages[0].Timestamp
	m.merge(&m.main, []core.Message{{ChannelID: m.main.conversation.ID, Timestamp: "9999999999.000001", Text: "new"}}, false, false)
	if m.main.messages[m.main.selected].Timestamp != selected {
		t.Fatal("incoming update moved the selection")
	}
	before := len(m.main.messages)
	mm, _ := m.Update(messagesResult{channel: m.main.conversation.ID, generation: m.main.generation - 1, page: core.MessagePage{Messages: []core.Message{{Timestamp: "1.000001"}}}})
	if len(mm.(model).main.messages) != before {
		t.Fatal("stale result replaced active view")
	}
}

func TestSendFailureKeepsDraftAndSuccessClearsIt(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "i")
	m, _ = key(m, "hello")
	m.sending = true
	mm, _ := m.Update(sentResult{err: fmt.Errorf("outcome unknown; check Slack")})
	m = mm.(model)
	if m.composer.Value() != "hello" || m.sending || !strings.Contains(m.status, "unknown") {
		t.Fatal("failed send lost the draft")
	}
	var send tea.Cmd
	m, send = key(m, "ctrl+s")
	if send == nil {
		t.Fatal("send command missing")
	}
	mm, _ = m.Update(send())
	m = mm.(model)
	if m.composer.Value() != "" || m.drafts[draftKey(m.main.conversation.ID, "")] != "" {
		t.Fatal("successful send did not clear the draft")
	}
}

type gapBackend struct {
	*demo.Backend
	calls int
}

func (b *gapBackend) History(_ context.Context, channel string, opts core.PageOptions) (core.MessagePage, error) {
	b.calls++
	if opts.Cursor == "" {
		return core.MessagePage{Messages: []core.Message{{Timestamp: "3.000001"}, {Timestamp: "4.000001"}}, Cursor: "older", HasMore: true}, nil
	}
	return core.MessagePage{Messages: []core.Message{{Timestamp: "1.000001"}, {Timestamp: "2.000001"}}}, nil
}

func TestPollingRecoversGapAcrossPages(t *testing.T) {
	b := &gapBackend{Backend: demo.New()}
	s := core.New(b)
	messages, err := fetchUpdates(context.Background(), s, view{conversation: core.Conversation{ID: "C1"}, messages: []core.Message{{Timestamp: "1.000001"}}})
	if err != nil || b.calls != 2 || len(messages) != 4 {
		t.Fatalf("gap reconciliation: %+v calls=%d err=%v", messages, b.calls, err)
	}
}

func TestResponsiveViews(t *testing.T) {
	m := loadedModel(t)
	for _, size := range [][2]int{{45, 18}, {80, 24}, {140, 40}} {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = mm.(model)
		if output := m.View(); !strings.Contains(output, "OFFLINE DEMO") || !strings.Contains(output, "compose") {
			t.Fatalf("bad frame at %v: %s", size, output)
		}
	}
}

func TestCacheBoundDoesNotPreventBrowsingOlderHistory(t *testing.T) {
	m := loadedModel(t)
	m.main.messages = nil
	for i := 1000; i < 1500; i++ {
		m.main.messages = append(m.main.messages, core.Message{Timestamp: fmt.Sprintf("%010d.000001", i)})
	}
	m.main.selected = 0
	m.rebuild(&m.main, false)
	older := []core.Message{{Timestamp: "0000000999.000001", Text: "older"}}
	m.merge(&m.main, older, false, true)
	if len(m.main.messages) != 500 || m.main.messages[0].Text != "older" {
		t.Fatal("old page was immediately evicted")
	}
	m.main.selected = 0
	m.merge(&m.main, []core.Message{{Timestamp: "9999999999.000001", Text: "new"}}, false, false)
	if m.main.messages[m.main.selected].Text != "older" {
		t.Fatal("new message evicted the selected older message")
	}
}

func TestConfirmedReactionAppearsWithoutMutatingSnapshots(t *testing.T) {
	m := loadedModel(t)
	message := m.main.messages[0]
	m.main.messages[0].Reactions = []core.Reaction{{Name: "thumbsup", Count: 2}}
	snapshot := m.main.messages
	mm, _ := m.Update(reactionResult{channel: message.ChannelID, ts: message.Timestamp, name: "thumbsup"})
	m = mm.(model)
	if m.main.messages[0].Reactions[0].Count != 3 || snapshot[0].Reactions[0].Count != 2 {
		t.Fatal("reaction update was missing or mutated an in-flight snapshot")
	}
}

func TestFramesFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 15}, {80, 24}, {140, 40}} {
		for _, mode := range []string{"normal", "help", "compose", "jump", "search-thread"} {
			m := loadedModel(t)
			mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m = mm.(model)
			switch mode {
			case "help":
				m.help = true
			case "compose":
				m.mode = "compose"
			case "jump":
				m.mode = "jump"
			case "search-thread":
				m.searchActive = true
				m.search.messages = m.main.messages
				m.searchQuery = "deploy"
				m.thread = &view{conversation: m.main.conversation, messages: m.main.messages, viewport: m.main.viewport}
				m.rebuild(&m.search, false)
				m.rebuild(m.thread, false)
			}
			frame := m.View()
			if width, height := lipgloss.Size(frame); width > size[0] || height > size[1] {
				t.Errorf("%s %v: frame is %dx%d", mode, size, width, height)
			}
		}
	}
}

func TestEmojiMessagesFitTerminal(t *testing.T) {
	for _, size := range [][2]int{{40, 15}, {80, 24}, {140, 40}} {
		m := loadedModel(t)
		m.main.messages = []core.Message{{
			Timestamp: "1.000001", Text: strings.Repeat(":female-technologist::skin-tone-4: :thumbsup: ", 30),
			Reactions: []core.Reaction{{Name: "thumbsup", Count: 2}},
		}}
		m.main.selected = 0
		mm, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m = mm.(model)
		frame := m.View()
		if !strings.Contains(frame, "👩🏽‍💻") {
			t.Fatalf("emoji missing from frame at %v: %s", size, frame)
		}
		if width, height := lipgloss.Size(frame); width > size[0] || height > size[1] {
			t.Errorf("emoji frame at %v is %dx%d", size, width, height)
		}
	}
}
