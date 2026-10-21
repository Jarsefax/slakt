package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
)

func TestUnreadIndicatorsRestoreBeforeBackgroundChecks(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.Visible, localdata.UnreadFirst, localdata.UnreadOnly} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
			launch := func() (model, tea.Cmd) {
				m := newModel(context.Background(), core.New(demo.New()), Options{Store: store, PollInterval: 15 * time.Second})
				m.sidebarMode = mode
				updated, cmd := m.Update(m.directoryCmd()())
				if cmd == nil {
					t.Fatal("startup did not request the initial conversation")
				}
				return updated.(model), cmd
			}
			m, load := launch()
			updated, _ := m.Update(load())
			m = updated.(model)
			m.sidebar = true
			for _, channel := range []string{"CGENERAL", "CRANDOM", "DALICE"} {
				updated, _ = m.Update(unreadResult{channel: channel, latest: "2000000000.000001"})
				m = updated.(model)
			}
			m.markReadThrough("CGENERAL", "2000000000.000001")
			want := conversationIDs(m.sidebarConversations())
			baseline := m.readState.StartedAt

			// Only complete the directory request on restart. No conversation
			// history or background unread request has run in the new process.
			m, _ = launch()
			if !m.isUnread("CRANDOM") || !m.isUnread("DALICE") || m.isUnread("CGENERAL") {
				t.Fatal("restart did not immediately restore the unread and read indicators")
			}
			if got := conversationIDs(m.sidebarConversations()); !slices.Equal(got, want) {
				t.Fatalf("sidebar after restart = %v, want %v", got, want)
			}
			if m.readState.StartedAt != baseline || m.readState.Position("CGENERAL") != "2000000000.000001" {
				t.Fatal("restoring unread indicators changed the saved read positions")
			}
			for _, result := range []unreadResult{
				{channel: "CRANDOM", err: errors.New("offline")},
				{channel: "CRANDOM", latest: "1999999999.000001"},
			} {
				updated, _ = m.Update(result)
				m = updated.(model)
				if !m.isUnread("CRANDOM") || m.latest["CRANDOM"] != "2000000000.000001" {
					t.Fatal("failed or stale background check erased the restored unread indicator")
				}
			}
		})
	}
}

func TestUnreadObservationSaveRetriesAfterFailure(t *testing.T) {
	m := loadedModel(t)
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(blocked, "read-state.json")}
	m.options.Store, m.sidebar = store, true
	result := unreadResult{channel: "CRANDOM", latest: "2000000000.000001"}
	updated, _ := m.Update(result)
	m = updated.(model)
	if m.storageError == "" || !m.readDirty || !m.isUnread(result.channel) {
		t.Fatal("failed save lost the unread observation, pending save, or error")
	}
	store.StatePath = filepath.Join(root, "read-state.json")
	updated, _ = m.Update(result)
	m = updated.(model)
	if m.storageError != "" || m.readDirty {
		t.Fatalf("unchanged background result did not retry the pending save: %s", m.storageError)
	}
	restarted := newModel(m.ctx, core.New(demo.New()), Options{Store: store})
	updated, _ = restarted.Update(restarted.directoryCmd()())
	if !updated.(model).isUnread(result.channel) {
		t.Fatal("retried unread observation did not survive restart")
	}
}
