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
		})
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
