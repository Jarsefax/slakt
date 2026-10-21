package tui

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
)

func launchRestartModel(t *testing.T, service *core.Service, store *localdata.Store, mode localdata.SidebarMode, initial *core.Conversation) (model, tea.Cmd) {
	t.Helper()
	m := newModel(context.Background(), service, Options{Store: store, PollInterval: 15 * time.Second, Initial: initial})
	m.sidebarMode = mode
	updated, cmd := m.Update(m.directoryCmd()())
	m = updated.(model)
	if cmd == nil || m.storageError != "" {
		t.Fatalf("startup failed: %s %s", m.status, m.storageError)
	}
	return m, cmd
}

func TestStartupRestoresLastOpenedConversationWithoutReading(t *testing.T) {
	root := t.TempDir()
	store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
	b := demo.New()
	service := core.New(b)
	m, load := launchRestartModel(t, service, store, localdata.UnreadFirst, nil)
	updated, _ := m.Update(load())
	m = updated.(model)
	m, _ = key(m, "ctrl+k")
	m, _ = key(m, "random")
	m, load = key(m, "enter")
	updated, _ = m.Update(load())
	m = updated.(model)
	m, _ = key(m, "tab")
	for _, channel := range []string{"CENGINEERING", "CRANDOM"} {
		if _, err := b.Send(m.ctx, channel, "", "unread before quitting"); err != nil {
			t.Fatal(err)
		}
		updated, _ = m.Update(m.unreadCmd(channel)())
		m = updated.(model)
	}
	// A different sidebar preview must not replace the last committed open.
	m, load = key(m, "j")
	if load == nil || m.sidebarSelectedID() == "CRANDOM" {
		t.Fatal("test did not preview a different conversation")
	}
	updated, _ = m.Update(load())
	m = updated.(model)
	before := maps.Clone(m.readState.LastRead)
	config, err := store.LoadConfig()
	if err != nil || config.LastConversations["TDEMO/UYOU"] != "CRANDOM" {
		t.Fatalf("preview replaced the last opened conversation: %+v, %v", config, err)
	}
	_, quit := key(m, "q")
	assertQuit(t, quit)

	m, load = launchRestartModel(t, service, store, localdata.UnreadFirst, nil)
	if m.main.conversation.ID != "CRANDOM" || m.sidebarConversations()[0].ID != "CENGINEERING" || m.sidebarSelectedID() != "CRANDOM" {
		t.Fatal("restart selected the first unread row instead of the last opened conversation")
	}
	updated, _ = m.Update(load())
	m = updated.(model)
	for _, message := range []tea.Msg{
		pollCountdownMsg{},
		tea.WindowSizeMsg{Width: 140, Height: 30},
		unreadResult{channel: "CENGINEERING", err: errors.New("offline")},
	} {
		updated, _ = m.Update(message)
		m = updated.(model)
	}
	for _, input := range []string{"?", "esc", "tab", "tab", "j", "m"} {
		m, _ = key(m, input)
	}
	m, load = key(m, "R")
	updated, _ = m.Update(load())
	m = updated.(model)
	message, err := b.Send(m.ctx, "CRANDOM", "", "arrived after restarting")
	if err != nil {
		t.Fatal(err)
	}
	updated, _ = m.Update(pollResult{channel: "CRANDOM", generation: m.main.generation, main: []core.Message{message}})
	m = updated.(model)
	if !m.isUnread("CENGINEERING") || !m.isUnread("CRANDOM") || !reflect.DeepEqual(m.readState.LastRead, before) {
		t.Fatal("startup loading, navigation, or background updates cleared unread messages")
	}
	stored, err := store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
	if err != nil || !reflect.DeepEqual(stored.LastRead, before) || stored.Latest["CRANDOM"] != message.Timestamp {
		t.Fatalf("startup changed saved read positions or lost a new observation: %+v, %v", stored, err)
	}
}

func TestStartupReadExemptionEndsOnExplicitOpenOrMarkRead(t *testing.T) {
	for _, action := range []string{"sidebar open", "jump", "sidebar mark read", "timeline mark read"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
			b := demo.New()
			first, err := b.Send(context.Background(), "CENGINEERING", "", "unread on startup")
			if err != nil {
				t.Fatal(err)
			}
			state := localdata.NewReadState(time.Unix(1000000000, 0))
			state.Latest["CENGINEERING"] = first.Timestamp
			if err := store.SaveReadState(context.Background(), "TDEMO", "UYOU", state); err != nil {
				t.Fatal(err)
			}
			m, load := launchRestartModel(t, core.New(b), store, localdata.UnreadFirst, nil)
			updated, _ := m.Update(load())
			m = updated.(model)
			if !m.isUnread("CENGINEERING") || m.readState.Position("CENGINEERING") != state.StartedAt {
				t.Fatal("startup marked the automatically opened conversation read")
			}
			// Another session changes the preference after startup. Reopening
			// this conversation should save a new choice; marking read should not.
			if err := store.SaveLastConversation(m.ctx, "TDEMO", "UYOU", "DALICE"); err != nil {
				t.Fatal(err)
			}
			switch action {
			case "sidebar open":
				m, _ = key(m, "tab")
				m, load = key(m, "enter")
			case "jump":
				m, _ = key(m, "ctrl+k")
				m, _ = key(m, "engineering")
				m, load = key(m, "enter")
			case "sidebar mark read":
				m, _ = key(m, "tab")
				m, _ = key(m, "r")
				m, _ = key(m, "tab")
				load = nil
			case "timeline mark read":
				m, _ = key(m, "r")
				load = nil
			}
			if load != nil {
				if !m.isUnread("CENGINEERING") {
					t.Fatal("explicit opening marked read before history loaded")
				}
				updated, _ = m.Update(load())
				m = updated.(model)
			}
			if m.isUnread("CENGINEERING") || m.readState.Position("CENGINEERING") != first.Timestamp {
				t.Fatal("explicit action did not mark the conversation read")
			}
			second, err := b.Send(m.ctx, "CENGINEERING", "", "arrived while reading")
			if err != nil {
				t.Fatal(err)
			}
			updated, _ = m.Update(pollResult{channel: "CENGINEERING", generation: m.main.generation, main: []core.Message{second}})
			m = updated.(model)
			stored, err := store.LoadReadState(m.ctx, m.identity.TeamID, m.identity.UserID)
			if err != nil || m.isUnread("CENGINEERING") || stored.Position("CENGINEERING") != second.Timestamp {
				t.Fatalf("normal automatic read marking did not resume: %+v, %v", stored, err)
			}
			want := "CENGINEERING"
			if action == "sidebar mark read" || action == "timeline mark read" {
				want = "DALICE"
			}
			config, err := store.LoadConfig()
			if err != nil || config.LastConversations["TDEMO/UYOU"] != want {
				t.Fatalf("explicit open or read marking saved the wrong preference: %+v, %v", config, err)
			}
		})
	}
}

func TestStartupConversationChoicePreservesReadPositions(t *testing.T) {
	for _, test := range []struct {
		name, saved, explicit, want string
		hidden                      bool
	}{
		{name: "legacy without selection", want: "CENGINEERING"},
		{name: "missing saved conversation", saved: "CDELETED", want: "CENGINEERING"},
		{name: "saved conversation", saved: "CRANDOM", want: "CRANDOM"},
		{name: "saved hidden conversation", saved: "CRANDOM", hidden: true, want: "CRANDOM"},
		{name: "explicit initial overrides saved", saved: "CRANDOM", explicit: "DALICE", want: "DALICE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
			ctx := context.Background()
			if test.saved != "" {
				if err := store.SaveLastConversation(ctx, "TDEMO", "UYOU", test.saved); err != nil {
					t.Fatal(err)
				}
			}
			if test.hidden {
				if err := store.SaveHiddenConversations(ctx, "TDEMO", "UYOU", map[string]bool{test.saved: true}); err != nil {
					t.Fatal(err)
				}
			}
			state := localdata.NewReadState(time.Unix(1000000000, 0))
			if err := store.SaveReadState(ctx, "TDEMO", "UYOU", state); err != nil {
				t.Fatal(err)
			}
			var initial *core.Conversation
			if test.explicit != "" {
				initial = &core.Conversation{ID: test.explicit, Name: "alice", Kind: "dm"}
			}
			m, load := launchRestartModel(t, core.New(demo.New()), store, localdata.UnreadFirst, initial)
			updated, _ := m.Update(load())
			m = updated.(model)
			if m.main.conversation.ID != test.want || !m.isUnread(test.want) || len(m.readState.LastRead) != 0 || m.readState.StartedAt != state.StartedAt {
				t.Fatalf("startup opened the wrong conversation or changed read positions: %s %+v", m.main.conversation.ID, m.readState)
			}
		})
	}
}

func TestLastConversationSaveRetriesWhileStartupReadMarkingIsPaused(t *testing.T) {
	root := t.TempDir()
	store := &localdata.Store{ConfigPath: filepath.Join(root, "config.json"), StatePath: filepath.Join(root, "read-state.json")}
	m := newModel(context.Background(), core.New(demo.New()), Options{Store: store})
	directory := m.directoryCmd()()
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	store.ConfigPath = filepath.Join(blocked, "config.json")
	updated, load := m.Update(directory)
	m = updated.(model)
	if m.storageError == "" || !m.conversationDirty {
		t.Fatal("failed save lost the pending last conversation or error")
	}
	store.ConfigPath = filepath.Join(root, "config.json")
	updated, _ = m.Update(pollCountdownMsg{})
	m = updated.(model)
	config, err := store.LoadConfig()
	if err != nil || m.storageError != "" || m.conversationDirty || config.LastConversations["TDEMO/UYOU"] != m.main.conversation.ID {
		t.Fatalf("startup did not retry the pending conversation save: %+v, %v, %s", config, err, m.storageError)
	}
	updated, _ = m.Update(load())
	if len(updated.(model).readState.LastRead) != 0 {
		t.Fatal("retrying the save marked startup history read")
	}
}

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
