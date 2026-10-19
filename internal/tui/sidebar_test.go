package tui

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func sidebarModel(t *testing.T) model {
	t.Helper()
	m := loadedModel(t)
	m.conversations = []core.Conversation{
		{ID: "S1", Name: "alpha", IsStarred: true}, {ID: "S2", Name: "bravo", IsStarred: true},
		{ID: "C1", Name: "alpha"}, {ID: "C2", Name: "bravo"}, {ID: "C3", Name: "charlie"},
		{ID: "D1", Name: "alpha", Kind: "dm"}, {ID: "D2", Name: "bravo", Kind: "dm"},
		{ID: "G1", Name: "alpha", Kind: "group-dm"}, {ID: "G2", Name: "bravo", Kind: "group-dm"},
	}
	m.readState = localdata.ReadState{StartedAt: "1000000000.000000", LastRead: map[string]string{}}
	m.latest = map[string]string{}
	m.main.conversation = core.Conversation{}
	for _, id := range []string{"S2", "C2", "C3", "D2", "G2"} {
		m.latest[id] = "2000000000.000001"
	}
	return m
}

func conversationIDs(items []core.Conversation) []string {
	ids := make([]string, 0, len(items))
	for _, c := range items {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestSidebarModesKeepSectionsAndNavigationInSync(t *testing.T) {
	for _, test := range []struct {
		mode localdata.SidebarMode
		want []string
	}{
		{localdata.All, []string{"S1", "S2", "C1", "C2", "C3", "D1", "D2", "G1", "G2"}},
		{localdata.UnreadFirst, []string{"S2", "S1", "C2", "C3", "C1", "D2", "D1", "G2", "G1"}},
		{localdata.UnreadOnly, []string{"S2", "C2", "C3", "D2", "G2"}},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			m := sidebarModel(t)
			source := append([]core.Conversation(nil), m.conversations...)
			m.sidebarMode, m.sidebar = test.mode, true
			if got := conversationIDs(m.sidebarConversations()); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("visible conversations = %v, want %v", got, test.want)
			}
			for i, id := range test.want {
				m.sideIndex = i
				opened, cmd := key(m, "enter")
				if cmd == nil || opened.main.conversation.ID != id {
					t.Fatalf("Enter at %d opened %s, want %s", i, opened.main.conversation.ID, id)
				}
				moved, _ := key(m, "j")
				if moved.sideIndex != min(i+1, len(test.want)-1) {
					t.Fatal("navigation used unfiltered directory indexes")
				}
				frame := ansi.Strip(m.sidebarView(22, 6))
				if !strings.Contains(frame, m.sidebarConversations()[i].Label()) || !strings.Contains(frame, test.mode.Label()) {
					t.Fatalf("mode or selection missing: %s", frame)
				}
			}
			if !reflect.DeepEqual(m.conversations, source) {
				t.Fatal("sidebar filtering changed the source directory")
			}
		})
	}
}

func TestReadConversationStaysUntilLeavingUnreadOnly(t *testing.T) {
	m := sidebarModel(t)
	m.sidebarMode = localdata.UnreadOnly
	c := m.conversations[3] // C2 is unread.
	m.switchTo(c)
	updated, _ := m.Update(messagesResult{channel: c.ID, generation: m.main.generation, page: core.MessagePage{Messages: []core.Message{{ChannelID: c.ID, Timestamp: m.latest[c.ID], Text: "new"}}}})
	m = updated.(model)
	if m.isUnread(c.ID) || !strings.Contains(strings.Join(conversationIDs(m.sidebarConversations()), ","), c.ID) {
		t.Fatal("opening should mark read while keeping the current conversation listed")
	}
	m.switchTo(m.conversations[4]) // C3 remains unread until its load succeeds.
	for _, visible := range m.sidebarConversations() {
		if visible.ID == c.ID {
			t.Fatal("read conversation stayed listed after leaving")
		}
	}
	if !m.isUnread("C3") {
		t.Fatal("conversation was marked read before loading successfully")
	}
}

func TestUnreadUpdatesPreserveSelectionAndJumpResults(t *testing.T) {
	m := sidebarModel(t)
	m.sidebarMode, m.sidebar, m.sideIndex = localdata.UnreadFirst, true, 3 // C3.
	selected := m.sidebarSelectedID()
	updated, _ := m.Update(unreadResult{channel: "C1", latest: "2000000000.000002"})
	m = updated.(model)
	if m.sidebarSelectedID() != selected {
		t.Fatal("background reorder moved the keyboard selection")
	}
	m.sidebarMode = localdata.UnreadOnly
	m.prompt("jump", "Jump")
	m.sideIndex = 4
	updated, _ = m.Update(unreadResult{channel: "S1", latest: "2000000000.000002"})
	m = updated.(model)
	if m.sideIndex != 4 || m.mode != "jump" {
		t.Fatal("background update changed jump navigation or input mode")
	}
	if choices := m.choices("alpha"); len(choices) != 4 {
		t.Fatalf("jump excluded read conversations: %+v", choices)
	}
}

func TestHiddenTimelineDoesNotMarkIncomingMessagesRead(t *testing.T) {
	for _, hidden := range []string{"help", "search", "narrow thread"} {
		t.Run(hidden, func(t *testing.T) {
			m := loadedModel(t)
			switch hidden {
			case "help":
				m.help = true
			case "search":
				m.searchActive = true
			case "narrow thread":
				m.thread = &view{conversation: m.main.conversation, thread: "1.000001"}
			}
			channel := m.main.conversation.ID
			updated, _ := m.Update(pollResult{channel: channel, generation: m.main.generation, main: []core.Message{{ChannelID: channel, Timestamp: "2000000000.000001", Text: "new"}}})
			m = updated.(model)
			if !m.isUnread(channel) {
				t.Fatal("hidden incoming message was marked read")
			}
			m, _ = key(m, "esc")
			if m.isUnread(channel) {
				t.Fatal("returning to the timeline did not mark visible messages read")
			}
		})
	}
}

func TestModeCyclesOnlyWhenBrowsingSidebarAndPersistsSeparately(t *testing.T) {
	m := loadedModel(t)
	root := t.TempDir()
	store := &localdata.Store{ConfigPath: filepath.Join(root, "config", "config.json"), StatePath: filepath.Join(root, "state", "read-state.json")}
	m.options.Store = store
	m, _ = key(m, "m")
	if m.sidebarMode != localdata.All {
		t.Fatal("m changed mode without sidebar focus")
	}
	m, _ = key(m, "tab")
	for _, want := range []localdata.SidebarMode{localdata.UnreadFirst, localdata.UnreadOnly, localdata.All} {
		m, _ = key(m, "m")
		got, err := store.LoadMode()
		if err != nil || m.sidebarMode != want || got != want {
			t.Fatalf("mode = %s, saved = %s, error = %v, want %s", m.sidebarMode, got, err, want)
		}
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}, Paste: true})
	m = updated.(model)
	if m.sidebarMode != localdata.All {
		t.Fatal("pasting m changed sidebar mode")
	}
	m, _ = key(m, "tab")
	m, _ = key(m, "i")
	m, _ = key(m, "m")
	if m.sidebarMode != localdata.All || m.composer.Value() != "m" {
		t.Fatal("m in composer activated a sidebar command")
	}
}

func TestReadTrackingRestartsAndDetectsOfflineMessages(t *testing.T) {
	root := t.TempDir()
	store := &localdata.Store{ConfigPath: filepath.Join(root, "config", "config.json"), StatePath: filepath.Join(root, "state", "read-state.json")}
	b := demo.New()
	service := core.New(b)
	launch := func() model {
		m := newModel(context.Background(), service, Options{PollInterval: 15 * time.Second, Store: store})
		updated, cmd := m.Update(m.directoryCmd()())
		if cmd == nil {
			t.Fatal("startup did not open an initial conversation")
		}
		updated, _ = updated.(model).Update(cmd())
		return updated.(model)
	}
	m := launch()
	startedAt := m.readState.StartedAt
	updated, _ := m.Update(m.unreadCmd("CGENERAL")())
	if updated.(model).isUnread("CGENERAL") {
		t.Fatal("initial history was flagged unread")
	}
	// The fake backend receives a message between launches, without a running
	// TUI observing it. No Slack request or real credential is involved.
	message, err := b.Send(context.Background(), "CGENERAL", "", "arrived while closed")
	if err != nil {
		t.Fatal(err)
	}
	m = launch()
	if m.readState.StartedAt != startedAt {
		t.Fatal("restarting reset the initial read baseline")
	}
	updated, _ = m.Update(m.unreadCmd("CGENERAL")())
	m = updated.(model)
	if !m.isUnread("CGENERAL") {
		t.Fatal("message received between launches was missed")
	}
	stored, err := store.LoadReadState(context.Background(), "TDEMO", "UYOU")
	if err != nil || stored.Position("CGENERAL") != startedAt {
		t.Fatalf("background check marked unread messages read: %+v, %v", stored, err)
	}
	cmd := m.switchTo(core.Conversation{ID: "CGENERAL", Name: "general"})
	updated, _ = m.Update(cmd())
	m = updated.(model)
	stored, err = store.LoadReadState(context.Background(), "TDEMO", "UYOU")
	if err != nil || m.isUnread("CGENERAL") || stored.Position("CGENERAL") != message.Timestamp {
		t.Fatalf("opening did not persist the read position: %+v, %v", stored, err)
	}
	m = launch()
	updated, _ = m.Update(m.unreadCmd("CGENERAL")())
	if updated.(model).isUnread("CGENERAL") {
		t.Fatal("already read messages reappeared as unread after restart")
	}
}

type unreadBackend struct {
	core.Backend
	channel string
	opts    core.PageOptions
	page    core.MessagePage
	err     error
}

func (b *unreadBackend) History(_ context.Context, channel string, opts core.PageOptions) (core.MessagePage, error) {
	b.channel, b.opts = channel, opts
	return b.page, b.err
}

func TestBackgroundUnreadChecksAreBoundedAndKeepStateOnFailure(t *testing.T) {
	m := sidebarModel(t)
	m.main.conversation = m.conversations[0]
	m.main.loading = true
	b := &unreadBackend{Backend: m.service.Backend, page: core.MessagePage{Messages: []core.Message{{Timestamp: "2000000000.000002"}}}}
	m.service = core.New(b)
	updated, cmd := m.Update(unreadTickMsg(time.Now()))
	m = updated.(model)
	if cmd == nil || !m.unreadPolling {
		t.Fatal("background check did not start")
	}
	result := cmd().(unreadResult)
	if b.channel == m.main.conversation.ID || b.opts.Limit != 1 || b.opts.After != m.readState.Position(b.channel) {
		t.Fatalf("background request not bounded to an inactive conversation: %s, %+v", b.channel, b.opts)
	}
	if _, duplicate := m.Update(unreadTickMsg(time.Now())); duplicate != nil {
		t.Fatal("started an overlapping background request")
	}
	updated, next := m.Update(result)
	m = updated.(model)
	if !m.isUnread(result.channel) || m.unreadPolling || next == nil {
		t.Fatal("background result did not flag unread and schedule another check")
	}
	updated, _ = m.Update(unreadResult{channel: result.channel, err: errors.New("offline")})
	m = updated.(model)
	if !m.isUnread(result.channel) || m.unreadError == "" {
		t.Fatal("failed background request erased known unread state or hid its error")
	}
	// A delayed request must not undo a read position recorded while it ran.
	m.markRead(result.channel, []core.Message{{Timestamp: "2000000000.000003"}})
	updated, _ = m.Update(result)
	if updated.(model).isUnread(result.channel) {
		t.Fatal("late background response made already read messages unread")
	}
}
