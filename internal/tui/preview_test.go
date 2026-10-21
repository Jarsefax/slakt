package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSidebarPreviewKeepsUnreadStateUntilExplicitlyOpened(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.All, localdata.UnreadFirst, localdata.UnreadOnly} {
		t.Run(string(mode), func(t *testing.T) {
			m := loadedModel(t)
			m.sidebarMode = mode
			store := &localdata.Store{StatePath: filepath.Join(t.TempDir(), "read-state.json")}
			m.options.Store = store
			if err := store.SaveReadState(m.ctx, m.identity.TeamID, m.identity.UserID, m.readState); err != nil {
				t.Fatal(err)
			}
			before := maps.Clone(m.readState.LastRead)
			message, err := m.service.Backend.Send(m.ctx, "CGENERAL", "", "unread preview message")
			if err != nil {
				t.Fatal(err)
			}
			m.latest["CGENERAL"] = message.Timestamp
			original := m.main.conversation.ID
			m, _ = key(m, "tab")
			updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
			m = updated.(model)
			if cmd == nil || !m.sidebar || m.main.conversation.ID != original {
				t.Fatal("navigation did not start a preview while preserving the open conversation")
			}
			updated, _ = m.Update(cmd())
			m = updated.(model)
			if m.preview == nil || m.preview.conversation.ID != "CGENERAL" || m.preview.loading {
				t.Fatal("highlighted conversation did not finish loading")
			}
			if !strings.Contains(ansi.Strip(m.View()), message.Text) {
				t.Fatal("preview messages are missing from the displayed pane")
			}
			assertNoMessageNumbers(t, *m.preview)
			// Refreshes and background unread checks must not turn passive
			// sidebar browsing into reading either conversation.
			updated, _ = m.Update(pollResult{channel: original, generation: m.main.generation, main: []core.Message{{ChannelID: original, Timestamp: "2000000000.000001", Text: "arrived during preview"}}})
			m = updated.(model)
			updated, _ = m.Update(m.unreadCmd("CGENERAL")())
			m = updated.(model)
			stored, err := store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
			if err != nil || !reflect.DeepEqual(m.readState.LastRead, before) || !reflect.DeepEqual(stored.LastRead, before) {
				t.Fatalf("preview or background activity changed read positions: %+v, %+v, %v", m.readState, stored, err)
			}
			if !m.isUnread("CGENERAL") || m.sidebarSelectedID() != "CGENERAL" {
				t.Fatal("preview lost its unread marker or sidebar selection")
			}
			m, cmd = key(m, "enter")
			if m.sidebar || m.preview != nil || !m.isUnread("CGENERAL") || cmd == nil {
				t.Fatal("Enter should open the conversation without marking it read before history loads")
			}
			updated, _ = m.Update(cmd())
			m = updated.(model)
			stored, err = store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
			if err != nil || m.isUnread("CGENERAL") || stored.Position("CGENERAL") != message.Timestamp {
				t.Fatalf("explicit opening did not persist normal read tracking: %+v, %v", stored, err)
			}
			if len(m.main.messages) < 2 {
				t.Fatal("explicit opening did not restore the conversation's read history")
			}
		})
	}
}

func unreadPreviewModel(t *testing.T) (model, *unreadBackend) {
	t.Helper()
	m := loadedModel(t)
	b := &unreadBackend{Backend: m.service.Backend, page: core.MessagePage{Messages: []core.Message{
		{ChannelID: "CGENERAL", Timestamp: "2000000000.000001", Text: "already read"},
		{ChannelID: "CGENERAL", Timestamp: "2000000000.000002", Text: "at read boundary"},
		{ChannelID: "CGENERAL", Timestamp: "2000000000.000003", Text: "fresh arrival"},
	}}}
	m.service = core.New(b)
	m.readState.LastRead["CGENERAL"] = b.page.Messages[1].Timestamp
	m.latest["CGENERAL"] = b.page.Messages[2].Timestamp
	return m, b
}

func TestSidebarPreviewFiltersUnreadMessagesInEachLayout(t *testing.T) {
	for _, layout := range []struct {
		width  int
		thread bool
	}{{100, false}, {100, true}, {140, true}} {
		for _, mode := range []localdata.SidebarMode{localdata.All, localdata.UnreadFirst, localdata.UnreadOnly} {
			t.Run(fmt.Sprintf("%d/thread=%t/%s", layout.width, layout.thread, mode), func(t *testing.T) {
				m, b := unreadPreviewModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{Width: layout.width, Height: 30})
				m = updated.(model)
				if layout.thread {
					var cmd tea.Cmd
					m, cmd = key(m, "enter")
					updated, _ = m.Update(cmd())
					m = updated.(model)
				}
				main := slices.Clone(m.main.messages)
				thread := m.thread
				before := maps.Clone(m.readState.LastRead)
				m.sidebarMode = mode
				m, _ = key(m, "tab")
				m, cmd := key(m, "j")
				updated, _ = m.Update(cmd())
				m = updated.(model)
				wantAfter, wantCount := "", len(b.page.Messages)
				if mode == localdata.UnreadOnly {
					wantAfter, wantCount = before["CGENERAL"], 1
				}
				if b.channel != "CGENERAL" || b.opts.After != wantAfter || b.opts.Limit != 50 {
					t.Fatalf("unexpected preview request: %s, %+v", b.channel, b.opts)
				}
				if m.preview == nil || len(m.preview.messages) != wantCount {
					t.Fatalf("preview = %+v, want %d messages", m.preview, wantCount)
				}
				frame := ansi.Strip(m.View())
				if !strings.Contains(frame, "fresh arrival") {
					t.Fatal("unread message is missing from the preview pane")
				}
				for _, text := range []string{"already read", "at read boundary"} {
					if strings.Contains(frame, text) != (mode != localdata.UnreadOnly) {
						t.Fatalf("preview has incorrect visibility for %q: %s", text, frame)
					}
				}
				if layout.thread && layout.width >= 125 && !strings.Contains(frame, "Thread · #engineering") {
					t.Fatal("the original thread is missing beside the unread preview")
				}
				if m.thread != thread || !reflect.DeepEqual(m.main.messages, main) || !reflect.DeepEqual(m.readState.LastRead, before) {
					t.Fatal("filtering the preview changed the open conversation, thread, or read positions")
				}
			})
		}
	}
}

func TestSidebarPreviewReloadsWhenUnreadFilterChanges(t *testing.T) {
	m, b := unreadPreviewModel(t)
	// Without a per-conversation position, the account baseline is the boundary.
	m.readState.StartedAt = m.readState.LastRead["CGENERAL"]
	delete(m.readState.LastRead, "CGENERAL")
	before := maps.Clone(m.readState.LastRead)
	m, _ = key(m, "tab")
	m, cmd := key(m, "j")
	full := cmd().(previewResult)
	preview := m.preview
	m, cmd = key(m, "m") // Visible -> Unread first uses the same history.
	if cmd != nil || m.preview != preview {
		t.Fatal("Unread first unnecessarily restarted the preview")
	}
	m, cmd = key(m, "m") // Unread first -> Unread only reloads with a boundary.
	if cmd == nil || m.preview == preview {
		t.Fatal("Unread only did not reload the preview")
	}
	unread := cmd().(previewResult)
	if b.opts.After != m.readState.StartedAt {
		t.Fatal("preview did not use the account baseline as its unread boundary")
	}
	updated, _ := m.Update(full)
	m = updated.(model)
	if !m.preview.loading || len(m.preview.messages) != 0 {
		t.Fatal("delayed full history appeared in the unread-only preview")
	}
	updated, _ = m.Update(unread)
	m = updated.(model)
	if m.preview.loading || len(m.preview.messages) != 1 || m.preview.messages[0].Text != "fresh arrival" {
		t.Fatal("unread-only preview did not exclude messages at or before the boundary")
	}
	m, _ = key(m, "m") // Unread only -> Hidden has no rows in this account.
	if m.preview != nil {
		t.Fatal("empty Hidden mode retained an unrelated preview")
	}
	m, _ = key(m, "m")   // Hidden -> All restores the complete directory.
	m, cmd = key(m, "j") // Highlight CGENERAL again to fetch full history.
	if cmd == nil {
		t.Fatal("All did not reload the full preview history")
	}
	updated, _ = m.Update(unread)
	m = updated.(model)
	if !m.preview.loading || len(m.preview.messages) != 0 {
		t.Fatal("delayed unread-only history replaced the full-history request")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if b.opts.After != "" || len(m.preview.messages) != len(b.page.Messages) || !reflect.DeepEqual(m.readState.LastRead, before) {
		t.Fatal("switching back to All did not restore full history while preserving read positions")
	}
}

func TestUnreadPreviewOfOpenConversationTracksPolling(t *testing.T) {
	m := loadedModel(t)
	m.sidebarMode = localdata.UnreadOnly
	channel := m.main.conversation.ID
	before := maps.Clone(m.readState.LastRead)
	main := slices.Clone(m.main.messages)
	selection, offset := m.main.selected, m.main.viewport.YOffset
	m, cmd := key(m, "tab")
	if cmd != nil || m.preview == nil || len(m.preview.messages) != 0 || !strings.Contains(ansi.Strip(m.View()), "No unread messages.") {
		t.Fatal("highlighting the read open conversation did not show an empty unread preview")
	}
	message := core.Message{ChannelID: channel, Timestamp: "2000000000.000001", Text: "fresh arrival"}
	updated, _ := m.Update(pollResult{channel: channel, generation: m.main.generation, main: []core.Message{message}})
	m = updated.(model)
	if m.preview == nil || len(m.preview.messages) != 1 || m.preview.messages[0].Timestamp != message.Timestamp || !strings.Contains(ansi.Strip(m.View()), message.Text) {
		t.Fatal("new arrivals did not appear in the open conversation's unread preview")
	}
	if !reflect.DeepEqual(m.readState.LastRead, before) || m.main.selected != selection || m.main.viewport.YOffset != offset || !reflect.DeepEqual(m.main.messages[:len(main)], main) {
		t.Fatal("previewing changed the open conversation's history, scroll position, or read positions")
	}
	m, _ = key(m, "esc")
	if m.preview != nil || len(m.main.messages) != len(main)+1 || m.isUnread(channel) || m.main.selected != selection || m.main.viewport.YOffset != offset {
		t.Fatal("leaving preview did not restore the open conversation and normal read tracking")
	}
}

func TestSidebarPreviewReturnsToOriginalThreadAndDrafts(t *testing.T) {
	for _, width := range []int{100, 140} {
		for _, exit := range []string{"tab", "esc"} {
			t.Run(fmt.Sprint(width)+"/"+exit, func(t *testing.T) {
				m := loadedModel(t)
				updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
				m = updated.(model)
				cmd := m.switchTo(m.conversations[1]) // #general, with a thread.
				updated, _ = m.Update(cmd())
				m = updated.(model)
				m, _ = key(m, "gjj")
				m, cmd = key(m, "enter")
				updated, _ = m.Update(cmd())
				m = updated.(model)
				m, _ = key(m, "i")
				m, _ = key(m, "thread draft")
				m, _ = key(m, "esc")
				thread := m.thread
				mainSelection, mainOffset := m.main.selected, m.main.viewport.YOffset
				selection, offset := thread.selected, thread.viewport.YOffset
				starts := slices.Clone(thread.starts)
				drafts := maps.Clone(m.drafts)
				m, _ = key(m, "tab")
				m, cmd = key(m, "j")
				updated, _ = m.Update(cmd())
				m = updated.(model)
				if m.preview == nil || m.preview.conversation.ID != "CRANDOM" || m.thread != thread {
					t.Fatal("preview replaced the original conversation or thread")
				}
				frame := ansi.Strip(m.View())
				if !strings.Contains(frame, "Good coffee") || (width >= 125 && !strings.Contains(frame, "Thread · #general")) {
					t.Fatalf("preview or retained thread is missing: %s", frame)
				}
				_, contentWidth, _ := m.dimensions()
				if width >= 125 {
					contentWidth = (contentWidth - 3) / 2
				}
				if m.preview.viewport.Width != contentWidth {
					t.Fatal("preview was not sized to the conversation pane")
				}
				m, _ = key(m, exit)
				if m.sidebar || m.preview != nil || m.thread != thread || m.thread.selected != selection || m.thread.viewport.YOffset != offset || !slices.Equal(starts, m.thread.starts) {
					t.Fatal("leaving preview changed the original thread or its scroll position")
				}
				if m.main.conversation.ID != "CGENERAL" || m.main.selected != mainSelection || m.main.viewport.YOffset != mainOffset {
					t.Fatal("leaving preview changed the original conversation or its scroll position")
				}
				if !reflect.DeepEqual(m.drafts, drafts) || m.composer.Value() != "thread draft" {
					t.Fatal("preview changed the saved drafts or composer context")
				}
			})
		}
	}
}

func TestSidebarPreviewIgnoresOutOfOrderAndDismissedResults(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "tab")
	m, first := key(m, "j") // #general.
	oldGeneral := first().(previewResult)
	oldGeneral.page.Messages = []core.Message{{Timestamp: "2000000000.000001", Text: "obsolete general result"}}
	m, second := key(m, "j") // #random.
	oldRandom := second().(previewResult)
	m, third := key(m, "k") // #general again, with a new request.
	updated, _ := m.Update(third())
	m = updated.(model)
	want := slices.Clone(m.preview.messages)
	for _, stale := range []previewResult{oldGeneral, oldRandom} {
		updated, _ = m.Update(stale)
		m = updated.(model)
		if !reflect.DeepEqual(m.preview.messages, want) {
			t.Fatal("an obsolete response replaced the current preview")
		}
	}
	m, _ = key(m, "tab")
	original := slices.Clone(m.main.messages)
	updated, _ = m.Update(oldGeneral)
	m = updated.(model)
	if m.preview != nil || !reflect.DeepEqual(m.main.messages, original) || m.isUnread("CGENERAL") {
		t.Fatal("a dismissed preview result affected the open conversation or unread state")
	}
}

type blockingPreviewBackend struct {
	core.Backend
	started chan context.Context
}

func (b *blockingPreviewBackend) History(ctx context.Context, _ string, _ core.PageOptions) (core.MessagePage, error) {
	b.started <- ctx
	<-ctx.Done()
	return core.MessagePage{}, ctx.Err()
}

func TestSidebarPreviewCancelsObsoleteRequests(t *testing.T) {
	for _, action := range []string{"j", "tab", "esc", "enter", "ctrl+k", "/"} {
		t.Run(action, func(t *testing.T) {
			m := loadedModel(t)
			b := &blockingPreviewBackend{Backend: m.service.Backend, started: make(chan context.Context, 1)}
			m.service = core.New(b)
			m, _ = key(m, "tab")
			m, cmd := key(m, "j")
			t.Cleanup(func() { m.clearPreview() })
			result := make(chan tea.Msg, 1)
			go func() { result <- cmd() }()
			var ctx context.Context
			select {
			case ctx = <-b.started:
			case <-time.After(5 * time.Second):
				t.Fatal("preview request did not start")
			}
			m, _ = key(m, action)
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("obsolete preview request was not canceled")
			}
			select {
			case msg := <-result:
				updated, _ := m.Update(msg)
				m = updated.(model)
				if m.previewError != "" || strings.Contains(m.status, "canceled") {
					t.Fatal("cancellation surfaced as an error in the new view")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled preview request did not finish")
			}
		})
	}
}

func TestFailedPreviewKeepsUnreadStateAndReportsError(t *testing.T) {
	m := loadedModel(t)
	m.latest["CGENERAL"] = "2000000000.000001"
	before := maps.Clone(m.readState.LastRead)
	m.service = core.New(&unreadBackend{Backend: m.service.Backend, err: errors.New("offline")})
	m, _ = key(m, "tab")
	m, cmd := key(m, "j")
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if !m.isUnread("CGENERAL") || !reflect.DeepEqual(m.readState.LastRead, before) || !strings.Contains(ansi.Strip(m.View()), "Preview failed: offline") {
		t.Fatal("failed preview lost unread state or hid its error")
	}
	m, cmd = key(m, "enter")
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if !m.isUnread("CGENERAL") || !reflect.DeepEqual(m.readState.LastRead, before) || m.previewError != "" {
		t.Fatal("failed explicit load changed read state or retained the preview error")
	}
}

func TestSidebarPreviewFollowsModeChangesAndBackgroundReordering(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "tab")
	m, cmd := key(m, "j") // #general is already read.
	updated, _ := m.Update(cmd())
	m = updated.(model)
	preview := m.preview
	before := maps.Clone(m.readState.LastRead)
	updated, _ = m.Update(unreadResult{channel: "CRANDOM", latest: "2000000000.000001"})
	m = updated.(model)
	m, cmd = key(m, "m") // Unread first moves #random ahead of #general.
	if m.sidebarSelectedID() != "CGENERAL" || m.preview != preview || cmd != nil {
		t.Fatal("unread reordering changed the previewed conversation or reloaded its history")
	}
	m, cmd = key(m, "m") // Unread only removes the read #general row.
	if m.sidebarSelectedID() != "CRANDOM" || m.preview == nil || m.preview.conversation.ID != "CRANDOM" || cmd == nil {
		t.Fatal("preview did not follow the selection after filtering removed its row")
	}
	updated, _ = m.Update(cmd())
	m = updated.(model)
	updated, cmd = m.Update(unreadResult{channel: "CGENERAL", latest: "2000000000.000002"})
	m = updated.(model)
	if m.sidebarSelectedID() != "CRANDOM" || m.preview.conversation.ID != "CRANDOM" || !reflect.DeepEqual(m.readState.LastRead, before) {
		t.Fatal("background insertion moved the preview selection or changed read positions")
	}
	// The existing conversation stays pinned in Unread only while previewing.
	if !slices.Contains(conversationIDs(m.sidebarConversations()), m.main.conversation.ID) {
		t.Fatal("previewing evicted the open conversation from the sidebar")
	}
}

func TestSidebarPreviewResizesWithItsRetainedThread(t *testing.T) {
	m := loadedModel(t)
	m, cmd := key(m, "enter")
	updated, _ := m.Update(cmd())
	m = updated.(model)
	m, _ = key(m, "tab")
	m, cmd = key(m, "j")
	updated, _ = m.Update(cmd())
	m = updated.(model)
	for _, width := range []int{140, 160, 100} {
		updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
		m = updated.(model)
		_, contentWidth, height := m.dimensions()
		previewWidth, threadWidth := contentWidth, contentWidth
		if width >= 125 {
			previewWidth = (contentWidth - 3) / 2
			threadWidth = contentWidth - previewWidth - 3
		}
		if m.preview.viewport.Width != previewWidth || m.thread.viewport.Width != threadWidth || m.preview.viewport.Height != height {
			t.Fatalf("preview or thread has the wrong dimensions after resizing to %d", width)
		}
		for _, line := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("preview frame overflows after resizing to %d: %q", width, ansi.Strip(line))
			}
		}
	}
}
