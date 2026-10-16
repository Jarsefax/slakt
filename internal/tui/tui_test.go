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
	case "ctrl+c":
		msg.Type = tea.KeyCtrlC
	case "ctrl+k":
		msg.Type = tea.KeyCtrlK
	case "alt+q":
		msg.Type = tea.KeyRunes
		msg.Runes = []rune{'q'}
		msg.Alt = true
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

func TestSidebarScrollAcrossSectionBoundaries(t *testing.T) {
	m := loadedModel(t)
	m.sidebar = true
	for _, height := range []int{6, 8, 12, 20} {
		for i, conversation := range m.conversations {
			m.sideIndex = i
			frame := ansi.Strip(m.sidebarView(22, height))
			if !strings.Contains(frame, conversation.Label()) || !strings.Contains(frame, conversation.Section()) {
				t.Fatalf("selection or section missing at index %d, height %d: %s", i, height, frame)
			}
			if width, actualHeight := lipgloss.Size(frame); width > 23 || actualHeight > height {
				t.Fatalf("sidebar overflow at index %d: %dx%d", i, width, actualHeight)
			}
		}
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

func assertMessageNumbers(t *testing.T, v view, want []string) {
	t.Helper()
	v.viewport.Height = v.viewport.TotalLineCount()
	v.viewport.SetYOffset(0)
	lines := strings.Split(ansi.Strip(v.viewport.View()), "\n")
	if len(v.starts) != len(want) {
		t.Fatalf("got %d message starts, want %d", len(v.starts), len(want))
	}
	for i, start := range v.starts {
		fields := strings.Fields(lines[start])
		if len(fields) == 0 || fields[0] != want[i] {
			t.Fatalf("message %d: header %q, want number %s", i, lines[start], want[i])
		}
		if strings.Contains(lines[start], "▎") != (i == v.selected) {
			t.Fatalf("message %d has incorrect selection marker: %q", i, lines[start])
		}
		if !strings.HasPrefix(lines[start+1], "    ") {
			t.Fatalf("message %d body has a numbered gutter: %q", i, lines[start+1])
		}
	}
}

func TestHybridMessageNumbersFollowSelectionInEachPane(t *testing.T) {
	m := loadedModel(t)
	m.main.messages = []core.Message{
		{Timestamp: "2.000001", User: "Alice", Text: "Ready."},
		{Timestamp: "3.000001", User: "Sam", Text: "Checking.\nOne more line."},
		{Timestamp: "4.000001", User: "Roger", Text: "Looks good."},
	}
	m.main.selected = 1
	m.rebuild(&m.main, false)
	assertMessageNumbers(t, m.main, []string{"1", "2", "1"})
	m, _ = key(m, "j")
	assertMessageNumbers(t, m.main, []string{"2", "1", "3"})
	m, _ = key(m, "g")
	assertMessageNumbers(t, m.main, []string{"1", "1", "2"})

	m.width = 140
	m.thread = &view{thread: "2.000001", messages: m.main.messages, viewport: m.main.viewport, selected: 1}
	m.rebuild(&m.main, false)
	m.rebuild(m.thread, false)
	assertMessageNumbers(t, *m.thread, []string{"1", "2", "1"})
	assertMessageNumbers(t, m.main, []string{"1", "1", "2"})
	m, _ = key(m, "j")
	assertMessageNumbers(t, *m.thread, []string{"2", "1", "3"})
	assertMessageNumbers(t, m.main, []string{"1", "1", "2"})
}

func TestHybridNumbersAndScrollAnchorSurvivePaginationAndUpdates(t *testing.T) {
	m := loadedModel(t)
	m.height = 15
	m.main.messages = []core.Message{{Timestamp: "2.000001"}, {Timestamp: "3.000001"}, {Timestamp: "4.000001"}}
	m.main.selected = 1
	m.rebuild(&m.main, false)
	m.main.viewport.SetYOffset(m.main.starts[1] + 1)
	m.merge(&m.main, []core.Message{{Timestamp: "1.000001"}}, false, true)
	assertMessageNumbers(t, m.main, []string{"2", "1", "3", "1"})
	if m.main.messages[m.main.selected].Timestamp != "3.000001" || m.main.viewport.YOffset != m.main.starts[2]+1 {
		t.Fatal("loading older history moved the selection or scroll anchor")
	}
	m.merge(&m.main, []core.Message{{Timestamp: "5.000001"}}, false, false)
	assertMessageNumbers(t, m.main, []string{"2", "1", "3", "1", "2"})
	if m.main.viewport.YOffset != m.main.starts[2]+1 {
		t.Fatal("incoming message moved the scroll anchor")
	}
}

func TestThreadIndicatorsAndContextualActions(t *testing.T) {
	for _, test := range []struct {
		name, thread, indicator, action string
		replies                         int
	}{
		{name: "no thread", action: "start thread"},
		{name: "root without replies", thread: "1.000001", action: "start thread"},
		{name: "one reply", replies: 1, indicator: "↳ Thread · 1 reply", action: "open thread"},
		{name: "multiple replies", replies: 3, indicator: "↳ Thread · 3 replies", action: "open thread"},
		{name: "broadcast reply", thread: "0.000001", indicator: "↳ In thread", action: "open thread"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := loadedModel(t)
			m.main.messages = []core.Message{{Timestamp: "1.000001", ThreadTS: test.thread, ReplyCount: test.replies, User: "Alice", Text: "Ready."}}
			m.main.selected = 0
			m.rebuild(&m.main, false)
			m.main.viewport.GotoTop()
			frame := ansi.Strip(m.main.viewport.View())
			if test.indicator == "" {
				if strings.Contains(frame, "↳") {
					t.Fatalf("unexpected thread indicator: %s", frame)
				}
			} else if !strings.Contains(frame, "\n    ▎ "+test.indicator) {
				t.Fatalf("thread indicator missing from its own row: %s", frame)
			}
			if !strings.Contains(ansi.Strip(m.View()), "Enter: "+test.action) {
				t.Fatalf("incorrect thread action: %s", m.View())
			}
		})
	}
}

func TestConfirmedReplyUpdatesVisibleParentsWithoutMutatingSnapshots(t *testing.T) {
	for _, parentIndex := range []int{0, 2} {
		t.Run(fmt.Sprint(parentIndex), func(t *testing.T) {
			m := loadedModel(t)
			cmd := m.switchTo(core.Conversation{ID: "CGENERAL", Name: "general", Kind: "channel"})
			mm, _ := m.Update(cmd())
			m = mm.(model)
			m.main.selected = parentIndex
			m.rebuild(&m.main, false)
			before := m.main.messages[parentIndex].ReplyCount
			snapshot := m.main.messages
			m.search.messages = []core.Message{m.main.messages[parentIndex]}
			searchSnapshot := m.search.messages
			m, cmd = key(m, "enter")
			mm, _ = m.Update(cmd())
			m = mm.(model)
			m, _ = key(m, "i")
			m, _ = key(m, "reply")
			m, cmd = key(m, "ctrl+s")
			mm, _ = m.Update(cmd())
			m = mm.(model)
			if m.main.messages[parentIndex].ReplyCount != before+1 || m.search.messages[0].ReplyCount != before+1 {
				t.Fatal("confirmed reply did not update the visible parent counts")
			}
			if snapshot[parentIndex].ReplyCount != before || searchSnapshot[0].ReplyCount != before {
				t.Fatal("reply changed a previous message snapshot")
			}
			m, _ = key(m, "esc")
			m, _ = key(m, "esc")
			m.reveal(&m.main)
			frame := ansi.Strip(m.View())
			if !strings.Contains(frame, fmt.Sprintf("↳ Thread · %d", before+1)) || !strings.Contains(frame, "Enter: open thread") {
				t.Fatalf("parent still looks like it has no thread: %s", frame)
			}
		})
	}
}

func TestSendFailureKeepsDraftAndSuccessClearsIt(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "i")
	m, _ = key(m, "hello")
	m, _ = key(m, "ctrl+s")
	if !m.sending || m.composer.Focused() || m.enteringText() {
		t.Fatal("pending send still accepts text entry")
	}
	mm, _ := m.Update(sentResult{err: fmt.Errorf("outcome unknown; check Slack")})
	m = mm.(model)
	if m.composer.Value() != "hello" || m.sending || m.mode != "compose" || !m.composer.Focused() || !strings.Contains(m.status, "unknown") {
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
	if m.mode != "" || m.composer.Focused() {
		t.Fatal("successful send did not return to browsing")
	}
	_, quit := key(m, "q")
	assertQuit(t, quit)
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
	for _, size := range [][2]int{{40, 15}, {79, 24}, {80, 24}, {125, 30}, {126, 30}, {140, 40}} {
		for _, mode := range []string{"normal", "help", "compose", "jump", "search-thread", "numbered-thread"} {
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
			case "numbered-thread":
				m.main.messages = nil
				for i := range 500 {
					m.main.messages = append(m.main.messages, core.Message{Timestamp: fmt.Sprintf("%d.000001", i+1), Text: strings.Repeat("👩🏽‍💻 wrapped text ", 4), ReplyCount: 123})
				}
				m.main.selected = 499
				m.thread = &view{thread: "1.000001", messages: m.main.messages, viewport: m.main.viewport, selected: 250}
				m.rebuild(&m.main, false)
				m.rebuild(m.thread, false)
				assertMessageNumbers(t, m.main, append(numberDistances(499), "500"))
			}
			frame := m.View()
			if width, height := lipgloss.Size(frame); width > size[0] || height > size[1] {
				t.Errorf("%s %v: frame is %dx%d", mode, size, width, height)
			}
		}
	}
}

func numberDistances(from int) []string {
	var numbers []string
	for n := from; n > 0; n-- {
		numbers = append(numbers, fmt.Sprint(n))
	}
	return numbers
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
