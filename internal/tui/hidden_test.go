package tui

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHiddenConversationsAreFilteredBeforeUnreadRetention(t *testing.T) {
	for _, test := range []struct {
		mode localdata.SidebarMode
		want []string
	}{
		{localdata.Visible, []string{"S1", "C1", "C3", "D1", "G1"}},
		{localdata.UnreadFirst, []string{"S1", "C3", "C1", "D1", "G1"}},
		{localdata.UnreadOnly, []string{"C3"}},
		{localdata.Hidden, []string{"S2", "C2", "D2", "G2"}},
		{localdata.All, []string{"S1", "S2", "C1", "C2", "C3", "D1", "D2", "G1", "G2"}},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			m := sidebarModel(t)
			m.sidebarMode, m.sidebar = test.mode, true
			m.hidden = map[string]bool{"S2": true, "C2": true, "D2": true, "G2": true}
			m.main.conversation = m.conversations[3] // Hidden C2 must not be retained.
			m.sidebarReadID = "D2"
			if got := conversationIDs(m.sidebarConversations()); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("mode %s: got %v, want %v", test.mode, got, test.want)
			}
			if len(m.choices("bravo")) != 4 {
				t.Fatal("jump lost hidden conversations")
			}
			if test.mode == localdata.All || test.mode == localdata.Hidden {
				m.sideIndex = slices.Index(test.want, "C2")
				frame := ansi.Strip(m.sidebarView(30, 30))
				if !strings.Contains(frame, "× #bravo") {
					t.Fatalf("hidden marker missing: %s", frame)
				}
			}
		})
	}
}

func TestHidingPreservesOpenViewAndMovesToNeighbor(t *testing.T) {
	for _, kind := range []string{"channel", "private", "dm", "group-dm"} {
		t.Run(kind, func(t *testing.T) {
			m := loadedModel(t)
			var c core.Conversation
			for _, candidate := range m.conversations {
				if candidate.Kind == kind || kind == "channel" && candidate.Kind == "" {
					c = candidate
					break
				}
			}
			if c.ID == "" {
				t.Fatal("demo lacks requested conversation kind")
			}
			m.switchTo(c)
			m.main.loading = false
			m.drafts[draftKey(c.ID, "")] = "keep this draft"
			m.thread = &view{conversation: c, thread: "1.000001"}
			m.sidebar = true
			m.selectSidebar(c.ID)
			updated, _ := m.Update(pollCountdownMsg{}) // Settle the sidebar's pane rendering.
			m = updated.(model)
			mainBefore, threadBefore := m.main, m.thread
			readBefore := maps.Clone(m.readState.LastRead)
			items, index := m.sidebarConversations(), m.sideIndex
			want := items[min(index+1, len(items)-1)].ID
			if index == len(items)-1 {
				want = items[index-1].ID
			}
			m, _ = key(m, "h")
			if !m.hidden[c.ID] || slices.Contains(conversationIDs(m.sidebarConversations()), c.ID) || m.sidebarSelectedID() != want {
				t.Fatalf("hiding did not select the neighbor: hidden=%v selected=%s want=%s", m.hidden, m.sidebarSelectedID(), want)
			}
			if !reflect.DeepEqual(m.main, mainBefore) || m.thread != threadBefore || m.drafts[draftKey(c.ID, "")] != "keep this draft" || !reflect.DeepEqual(m.readState.LastRead, readBefore) {
				t.Fatal("hiding changed the open view, thread, draft, or read positions")
			}
			m.sidebarMode = localdata.Hidden
			m.selectSidebar(c.ID)
			m, _ = key(m, "h")
			if m.hidden[c.ID] || len(m.sidebarConversations()) != 0 || m.preview != nil {
				t.Fatal("unhiding the final hidden row did not empty the list and clear its preview")
			}
			if !strings.Contains(ansi.Strip(m.sidebarView(30, 8)), "No hidden conversations") {
				t.Fatal("hidden empty state missing")
			}
			m, _ = key(m, "h") // Empty sidebar is safe.
		})
	}
}

func TestHideInAllKeepsRowAndRejectsTextInput(t *testing.T) {
	m := loadedModel(t)
	m.sidebarMode, m.sidebar = localdata.All, true
	m.selectSidebar(m.main.conversation.ID)
	id := m.sidebarSelectedID()
	m, _ = key(m, "h")
	if !m.hidden[id] || m.sidebarSelectedID() != id || !strings.Contains(ansi.Strip(m.View()), "h: unhide") {
		t.Fatal("All should retain and label the hidden row")
	}
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'h'}, Paste: true},
		{Type: tea.KeyRunes, Runes: []rune{'h'}, Alt: true},
	} {
		updated, _ := m.Update(msg)
		m = updated.(model)
		if !m.hidden[id] {
			t.Fatal("paste or Alt+h toggled hiding")
		}
	}
	m, _ = key(m, "h")
	if m.hidden[id] || m.sidebarSelectedID() != id {
		t.Fatal("All should unhide without moving selection")
	}
	m, _ = key(m, "tab")
	m, _ = key(m, "i")
	m, _ = key(m, "h")
	if m.composer.Value() != "h" || len(m.hidden) != 0 {
		t.Fatal("h in the composer toggled hiding")
	}
}

func TestHiddenConversationsLoadOnStartupWithoutOpeningHiddenByDefault(t *testing.T) {
	service := core.New(demo.New())
	ctx := context.Background()
	identity, _ := service.Backend.Identity(ctx)
	conversations, _, _ := service.Directory(ctx, true)
	first := conversations[0]
	newStore := func() *localdata.Store {
		root := t.TempDir()
		store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
		if err := store.SaveHiddenConversations(ctx, identity.TeamID, identity.UserID, map[string]bool{first.ID: true}); err != nil {
			t.Fatal(err)
		}
		return store
	}
	for _, mode := range []localdata.SidebarMode{localdata.Visible, localdata.UnreadFirst, localdata.UnreadOnly, localdata.Hidden, localdata.All} {
		m := newModel(ctx, service, Options{Store: newStore(), PollInterval: 15 * time.Second})
		m.sidebarMode = mode
		updated, cmd := m.Update(m.directoryCmd()())
		m = updated.(model)
		want := conversations[1].ID
		if mode == localdata.Hidden || mode == localdata.All {
			want = first.ID
		}
		if cmd == nil || !m.hidden[first.ID] || m.main.conversation.ID != want {
			t.Fatalf("startup in %s opened %s, want %s; hidden=%v", mode, m.main.conversation.ID, want, m.hidden)
		}
	}
	m := newModel(ctx, service, Options{Store: newStore(), Initial: &first})
	updated, cmd := m.Update(m.directoryCmd()())
	m = updated.(model)
	if cmd == nil || m.main.conversation.ID != first.ID || !m.hidden[first.ID] {
		t.Fatal("explicit initial conversation should open while staying hidden")
	}
}

func TestHiddenSaveRetriesAfterFailureAndSurvivesRestart(t *testing.T) {
	m := loadedModel(t)
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &localdata.Store{ConfigPath: filepath.Join(blocked, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
	m.options.Store, m.sidebar = store, true
	id := m.sidebarSelectedID()
	m, _ = key(m, "h")
	if m.storageError == "" || !m.hiddenDirty[id] {
		t.Fatal("failed save lost pending hidden choice or error")
	}
	store.ConfigPath = filepath.Join(root, "config.json")
	m.persistLocal()
	if m.storageError != "" || len(m.hiddenDirty) != 0 {
		t.Fatalf("save retry failed: %s", m.storageError)
	}
	restarted := newModel(m.ctx, m.service, Options{Store: store})
	updated, _ := restarted.Update(restarted.directoryCmd()())
	restarted = updated.(model)
	if !restarted.hidden[id] {
		t.Fatal("hidden choice did not survive restart")
	}
}

func TestHidingDiscardsDelayedPreviewAndCanOpenWhileStayingHidden(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "tab")
	m, cmd := key(m, "j")
	delayed := cmd().(previewResult)
	id := m.sidebarSelectedID()
	m, _ = key(m, "h")
	neighbor := m.preview
	updated, _ := m.Update(delayed)
	m = updated.(model)
	if !m.hidden[id] || m.sidebarSelectedID() == id || m.preview != neighbor {
		t.Fatal("late hidden preview displaced the new selection")
	}
	m.sidebarMode = localdata.Hidden
	m.selectSidebar(id)
	m, cmd = key(m, "enter")
	if cmd == nil || m.main.conversation.ID != id || !m.hidden[id] {
		t.Fatal("opening a hidden conversation should retain its hidden choice")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if m.main.loading || !m.hidden[id] || len(m.main.messages) == 0 {
		t.Fatal("hidden conversation did not load normally")
	}
	m.sidebarMode = localdata.Visible
	if slices.Contains(conversationIDs(m.sidebarConversations()), id) {
		t.Fatal("opening the hidden conversation made it visible")
	}
}

func TestStartupWithEveryConversationHiddenKeepsDirectoryAccessible(t *testing.T) {
	m := loadedModel(t)
	hidden := map[string]bool{}
	for _, c := range m.conversations {
		hidden[c.ID] = true
	}
	fresh := newModel(m.ctx, m.service, m.options)
	updated, cmd := fresh.Update(directoryResult{identity: m.identity, conversations: m.conversations, hidden: hidden})
	fresh = updated.(model)
	if cmd != nil || fresh.main.conversation.ID != "" || len(fresh.sidebarConversations()) != 0 || len(fresh.choices("general")) == 0 {
		t.Fatal("startup must leave hidden conversations closed but accessible through jump")
	}
	fresh, _ = key(fresh, "tab")
	fresh, _ = key(fresh, "mmm") // Visible -> Hidden.
	fresh, cmd = key(fresh, "enter")
	if cmd == nil || fresh.main.conversation.ID == "" {
		t.Fatal("all-hidden startup cannot recover through Hidden mode")
	}
}
