package tui

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSidebarMarkReadPreservesPreviewAndOriginalViews(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.All, localdata.UnreadFirst, localdata.UnreadOnly} {
		for _, width := range []int{100, 140} {
			t.Run(fmt.Sprintf("%s/%d", mode, width), func(t *testing.T) {
				m, b := unreadPreviewModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				m = updated.(model)
				m, load := key(m, "enter")
				updated, _ = m.Update(load())
				m = updated.(model)
				m, _ = key(m, "i")
				m, _ = key(m, "saved thread draft")
				m, _ = key(m, "esc")
				main, thread := m.main, *m.thread
				threadView := m.thread
				drafts := maps.Clone(m.drafts)
				store := &localdata.Store{StatePath: filepath.Join(t.TempDir(), "read-state.json")}
				m.options.Store = store
				m.sidebarMode = mode
				m, _ = key(m, "tab")
				m, load = key(m, "j")
				updated, _ = m.Update(load())
				m = updated.(model)
				preview, boundary := m.preview, m.previewAfter
				messages := slices.Clone(preview.messages)
				before := maps.Clone(m.readState.LastRead)
				// A background check can know about messages beyond the preview.
				latest := "2000000000.000004"
				m.observeLatest("CGENERAL", latest)
				m, cmd := key(m, "r")
				if cmd != nil || !m.sidebar || m.sidebarSelectedID() != "CGENERAL" || m.isUnread("CGENERAL") {
					t.Fatal("marking read changed focus or selection, or left the conversation unread")
				}
				if m.preview != preview || m.previewAfter != boundary || !reflect.DeepEqual(m.preview.messages, messages) {
					t.Fatal("marking read reloaded or cleared the preview")
				}
				if m.main.conversation != main.conversation || m.main.selected != main.selected || m.main.viewport.YOffset != main.viewport.YOffset || !reflect.DeepEqual(m.main.messages, main.messages) ||
					m.thread != threadView || m.thread.selected != thread.selected || m.thread.viewport.YOffset != thread.viewport.YOffset || !reflect.DeepEqual(m.thread.messages, thread.messages) || !reflect.DeepEqual(m.drafts, drafts) {
					t.Fatal("marking the preview read changed the original views or drafts")
				}
				before["CGENERAL"] = latest
				stored, err := store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
				if err != nil || !reflect.DeepEqual(stored.LastRead, before) || !reflect.DeepEqual(m.readState.LastRead, before) {
					t.Fatalf("read position was not persisted for only the highlighted conversation: %+v, %v", stored, err)
				}
				if b.channel != "CGENERAL" || !strings.Contains(ansi.Strip(m.View()), "fresh arrival") || strings.Contains(ansi.Strip(m.sidebarView(24, 20)), "● #general") {
					t.Fatal("read preview content was lost or its unread indicator remained")
				}
				assertBrowsing(t, m)
			})
		}
	}
}

func loadedReadPreview(t *testing.T) model {
	t.Helper()
	m, _ := unreadPreviewModel(t)
	m.sidebarMode = localdata.UnreadOnly
	m, _ = key(m, "tab")
	m, cmd := key(m, "j")
	updated, _ := m.Update(cmd())
	return updated.(model)
}

func TestSidebarMarkReadRemovesRowOnlyAfterMovingToNeighbor(t *testing.T) {
	for _, test := range []struct {
		key  tea.KeyMsg
		want string
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}}, "CRANDOM"},
		{tea.KeyMsg{Type: tea.KeyDown}, "CRANDOM"},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}}, "CENGINEERING"},
		{tea.KeyMsg{Type: tea.KeyUp}, "CENGINEERING"},
	} {
		t.Run(test.key.String(), func(t *testing.T) {
			m := loadedReadPreview(t)
			m.observeLatest("CRANDOM", "2000000000.000005")
			m, _ = key(m, "r")
			want := []string{"CENGINEERING", "CGENERAL", "CRANDOM"}
			if got := conversationIDs(m.sidebarConversations()); !slices.Equal(got, want) {
				t.Fatalf("read row was not retained: %v", got)
			}
			// A refresh must retain the row, preview, and selection too.
			preview := m.preview
			updated, _ := m.Update(unreadResult{channel: "CRANDOM", latest: "2000000000.000006"})
			m = updated.(model)
			if m.sidebarSelectedID() != "CGENERAL" || m.preview != preview {
				t.Fatal("background refresh removed the retained selection or preview")
			}
			updated, cmd := m.Update(test.key)
			m = updated.(model)
			if m.sidebarSelectedID() != test.want || m.sidebarReadID != "" || !m.sidebar {
				t.Fatalf("moving skipped the neighbor or left sidebar browsing: selected %s", m.sidebarSelectedID())
			}
			if slices.Contains(conversationIDs(m.sidebarConversations()), "CGENERAL") {
				t.Fatal("read row remained after selecting a neighbor")
			}
			if test.want == "CRANDOM" && (cmd == nil || m.preview == nil || m.preview.conversation.ID != test.want) {
				t.Fatal("navigation did not start the next conversation's preview")
			}
		})
	}
}

func TestSidebarMarkReadKeepsRowAtNavigationBoundary(t *testing.T) {
	m := loadedReadPreview(t)
	m, _ = key(m, "r") // #general is now the last row.
	preview := m.preview
	for _, input := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyRunes, Runes: []rune{'j'}}} {
		updated, cmd := m.Update(input)
		m = updated.(model)
		if cmd != nil || m.sidebarSelectedID() != "CGENERAL" || m.preview != preview || m.sidebarReadID != "CGENERAL" {
			t.Fatal("navigation at the list boundary dismissed the retained row")
		}
	}
	m, _ = key(m, "k")
	if m.sidebarSelectedID() != "CENGINEERING" || slices.Contains(conversationIDs(m.sidebarConversations()), "CGENERAL") {
		t.Fatal("moving away did not remove the retained row")
	}
}

func TestSidebarMarkedReadConversationCanBecomeUnreadAgain(t *testing.T) {
	m := loadedReadPreview(t)
	m, _ = key(m, "r")
	position := m.readState.Position("CGENERAL")
	preview := m.preview
	updated, _ := m.Update(unreadResult{channel: "CGENERAL", latest: "2000000000.000004"})
	m = updated.(model)
	if !m.isUnread("CGENERAL") || m.readState.Position("CGENERAL") != position || m.preview != preview {
		t.Fatal("a later arrival was marked read automatically or replaced the preview")
	}
	m, _ = key(m, "k")
	if !slices.Contains(conversationIDs(m.sidebarConversations()), "CGENERAL") {
		t.Fatal("moving away removed a conversation with new unread messages")
	}
	m, load := key(m, "j")
	result := load().(previewResult)
	result.page.Messages = append(result.page.Messages, core.Message{ChannelID: "CGENERAL", Timestamp: "2000000000.000004", Text: "arrived after marking read"})
	updated, _ = m.Update(result)
	m = updated.(model)
	if len(m.preview.messages) != 1 || m.preview.messages[0].Text != "arrived after marking read" {
		t.Fatal("returning to the conversation showed messages already marked read")
	}
}

func TestSidebarMarkReadWaitsForSuccessfulPreviewAndIgnoresPaste(t *testing.T) {
	for _, state := range []string{"loading", "failed", "pasted", "modified", "help"} {
		t.Run(state, func(t *testing.T) {
			m, _ := unreadPreviewModel(t)
			m, _ = key(m, "tab")
			m, load := key(m, "j")
			result := load().(previewResult)
			if state == "failed" {
				result.err = errors.New("offline")
			}
			if state != "loading" {
				updated, _ := m.Update(result)
				m = updated.(model)
			}
			if state == "help" {
				m, _ = key(m, "?")
			}
			before := maps.Clone(m.readState.LastRead)
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Paste: state == "pasted", Alt: state == "modified"})
			m = updated.(model)
			if !reflect.DeepEqual(m.readState.LastRead, before) || !m.isUnread("CGENERAL") || m.sidebarReadID != "" {
				t.Fatal("r marked read without a successful preview or an explicit command")
			}
		})
	}
}

func TestSidebarMarkReadRetentionEndsWhenBrowsingEnds(t *testing.T) {
	for _, exit := range []string{"tab", "esc", "enter", "ctrl+k", "/"} {
		t.Run(exit, func(t *testing.T) {
			m := loadedReadPreview(t)
			m, _ = key(m, "r")
			m, _ = key(m, exit)
			if m.sidebarReadID != "" || m.preview != nil {
				t.Fatal("leaving sidebar browsing retained the read preview")
			}
			if exit != "enter" && slices.Contains(conversationIDs(m.sidebarConversations()), "CGENERAL") {
				t.Fatal("read row remained after leaving sidebar browsing")
			}
		})
	}
}

func TestSidebarMarkReadOfOpenConversationPreservesPreview(t *testing.T) {
	m := loadedModel(t)
	m.sidebarMode = localdata.UnreadOnly
	m, _ = key(m, "tab")
	channel := m.main.conversation.ID
	message := core.Message{ChannelID: channel, Timestamp: "2000000000.000001", Text: "new in open conversation"}
	updated, _ := m.Update(pollResult{channel: channel, generation: m.main.generation, main: []core.Message{message}})
	m = updated.(model)
	m, _ = key(m, "r")
	if m.isUnread(channel) || len(m.preview.messages) != 1 || m.preview.messages[0].Timestamp != message.Timestamp {
		t.Fatal("marking the open conversation read cleared its unread preview")
	}
	message.Timestamp = "2000000000.000002"
	updated, _ = m.Update(pollResult{channel: channel, generation: m.main.generation, main: []core.Message{message}})
	m = updated.(model)
	if !m.isUnread(channel) || len(m.preview.messages) != 2 {
		t.Fatal("later arrivals in the open conversation were hidden or marked read")
	}
}

func TestOpenConversationMarkReadPreservesViewAndDraft(t *testing.T) {
	for _, channel := range []string{"CGENERAL", "CPRIVATE", "DALICE", "GRELEASE", "CEMPTY"} {
		t.Run(channel, func(t *testing.T) {
			m := loadedModel(t)
			c := core.Conversation{ID: channel, Name: "empty", Kind: "channel"}
			for _, candidate := range m.conversations {
				if candidate.ID == channel {
					c = candidate
				}
			}
			m.readState.LastRead[channel] = "1000000000.000001"
			load := m.switchToStartup(c)
			updated, _ := m.Update(load())
			m = updated.(model)
			m, _ = key(m, "i")
			m, _ = key(m, "remember this draft")
			m, _ = key(m, "esc")
			m, _ = key(m, "g")
			m, _ = key(m, "j")
			store := &localdata.Store{StatePath: filepath.Join(t.TempDir(), "read-state.json")}
			m.options.Store = store
			// Mark all known messages, even if the latest is beyond loaded history.
			m.observeLatest(channel, "2000000000.000004")
			m.observeLatest("COTHER", "2000000000.000005")
			before, drafts := m.main, maps.Clone(m.drafts)
			messages := slices.Clone(m.main.messages)
			read := maps.Clone(m.readState.LastRead)
			m, cmd := key(m, "r")
			if cmd != nil || m.sidebar || m.main.startup || m.isUnread(channel) || m.status != "Marked "+c.Label()+" read" {
				t.Fatal("r did not mark the open conversation read and stay in browsing")
			}
			assertBrowsing(t, m)
			if m.main.selected != before.selected || m.main.viewport.YOffset != before.viewport.YOffset ||
				m.main.newAfter != before.newAfter || !reflect.DeepEqual(m.main.messages, messages) ||
				!reflect.DeepEqual(m.drafts, drafts) || m.composer.Value() != "remember this draft" {
				t.Fatal("marking read changed the timeline, unread boundary, or draft")
			}
			read[channel] = "2000000000.000004"
			stored, err := store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
			if err != nil || !reflect.DeepEqual(stored.LastRead, read) || !reflect.DeepEqual(m.readState.LastRead, read) || !m.isUnread("COTHER") {
				t.Fatalf("marking read did not persist only the open conversation: %+v, %v", stored, err)
			}
		})
	}
}

func TestThreadMarkReadUsesConversationHistoryAndPreservesThread(t *testing.T) {
	for _, width := range []int{70, 140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := loadedModel(t)
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
			m = updated.(model)
			m, load := key(m, "enter")
			updated, _ = m.Update(load())
			m = updated.(model)
			m.main.startup = true
			channel := m.main.conversation.ID
			m.observeLatest(channel, "2000000000.000004")
			// Thread-only replies must not advance the conversation's read position.
			m.thread.messages = slices.Clone(m.thread.messages)
			m.thread.messages[len(m.thread.messages)-1].Timestamp = "3000000000.000005"
			m.rebuild(m.thread, true)
			thread := m.thread
			before := *thread
			m, cmd := key(m, "r")
			if cmd != nil || m.main.startup || m.isUnread(channel) || m.readState.Position(channel) != "2000000000.000004" {
				t.Fatal("r in a thread did not mark its containing conversation read")
			}
			if m.thread != thread || !reflect.DeepEqual(m.thread.messages, before.messages) || m.thread.selected != before.selected || m.thread.viewport.YOffset != before.viewport.YOffset {
				t.Fatal("marking read closed or changed the thread")
			}
			assertBrowsing(t, m)
		})
	}
}

func TestOpenConversationMarkReadIgnoresUnavailableContextsAndPaste(t *testing.T) {
	for _, state := range []string{"loading", "no conversation", "help", "search", "search thread", "sending", "pasted", "modified"} {
		t.Run(state, func(t *testing.T) {
			m := loadedModel(t)
			m.main.startup = true
			m.observeLatest(m.main.conversation.ID, "2000000000.000004")
			switch state {
			case "loading":
				m.main.loading = true
			case "no conversation":
				m.main.conversation = core.Conversation{}
			case "help":
				m, _ = key(m, "?")
			case "search", "search thread":
				m, _ = key(m, "/")
				m, _ = key(m, "deploy")
				var load tea.Cmd
				m, load = key(m, "enter")
				updated, _ := m.Update(load())
				m = updated.(model)
				if state == "search thread" {
					m, load = key(m, "enter")
					updated, _ = m.Update(load())
					m = updated.(model)
				}
			case "sending":
				m.sending = true
			}
			before := maps.Clone(m.readState.LastRead)
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Paste: state == "pasted", Alt: state == "modified"})
			m = updated.(model)
			if !reflect.DeepEqual(m.readState.LastRead, before) || !m.main.startup {
				t.Fatal("r marked messages read outside an available conversation or without an explicit command")
			}
		})
	}
}
