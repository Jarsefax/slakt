package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
)

func notificationModel(t *testing.T) (model, *[][2]string) {
	t.Helper()
	m := loadedModel(t)
	m.options.Demo = false
	var calls [][2]string
	m.configureNotifications(localdata.Config{Notifications: localdata.NotificationsOn}, func(ctx context.Context, summary, body string) error {
		if deadline, ok := ctx.Deadline(); !ok || deadline.Sub(time.Now()) > 3*time.Second {
			t.Error("notification command has no bounded deadline")
		}
		calls = append(calls, [2]string{summary, body})
		return nil
	})
	m.notificationAfter = "1000000000.000000"
	m.readState = localdata.ReadState{StartedAt: m.notificationAfter, LastRead: map[string]string{}}
	m.latest = map[string]string{}
	return m, &calls
}

// Run each command once, without following the timers' next update. Tests that
// include poll timers run in synctest so no real time or desktop is involved.
func runNotificationCommands(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, child := range batch {
			runNotificationCommands(child)
		}
	}
}

func TestNotificationsSummarizeNewestIncomingAndDeduplicate(t *testing.T) {
	m, calls := notificationModel(t)
	channel := m.main.conversation.ID
	messages := []core.Message{
		{Timestamp: "2000000000.000003", UserID: "UYOU", Text: "my message"},
		{Timestamp: "2000000000.000002", UserID: "UALICE", Text: "Hello <@USAM> :wave:"},
		{Timestamp: "2000000000.000001", UserID: "USAM", Text: "earlier"},
	}
	cmd := m.notificationCmd("test", channel, messages)
	if cmd == nil || len(*calls) != 0 {
		t.Fatal("notification was missing or ran on the UI thread")
	}
	if result := cmd().(notificationResult); result.err != nil {
		t.Fatal(result.err)
	}
	if len(*calls) != 1 || (*calls)[0] != [2]string{"Slakt · #engineering · Alice", "Hello @Sam 👋"} {
		t.Fatalf("notification = %v", *calls)
	}
	if cmd := m.notificationCmd("test", channel, messages); cmd != nil {
		t.Fatal("same poll notified twice")
	}
	messages[1].Text = "edited text"
	messages[1].ReplyCount++
	if cmd := m.notificationCmd("test", channel, messages); cmd != nil {
		t.Fatal("an edit or reply-count update created a new message notification")
	}
}

func TestNotificationTitlesIdentifySlaktAndBaseVersion(t *testing.T) {
	for _, test := range []struct{ version, brand string }{
		{"0.21.1", "Slakt 0.21.1"},
		{"0.21.1-dev+633457241cfc.dirty.src.572431c09a23", "Slakt 0.21.1"},
		{"0.21.1+build123", "Slakt 0.21.1"},
		{"", "Slakt"},
	} {
		t.Run(test.version, func(t *testing.T) {
			m, calls := notificationModel(t)
			m.options.Version = test.version
			cmd := m.notificationCmd("test", m.main.conversation.ID, []core.Message{{Timestamp: "2000000000.000001", UserID: "UALICE", Text: "hello"}})
			if cmd == nil {
				t.Fatal("notification command missing")
			}
			if result := cmd().(notificationResult); result.err != nil {
				t.Fatal(result.err)
			}
			if len(*calls) != 1 || (*calls)[0] != [2]string{test.brand + " · #engineering · Alice", "hello"} {
				t.Fatalf("notification title or preview = %v", *calls)
			}
		})
	}
}

func TestNotificationsIgnoreStartupHistoryAndOwnMessages(t *testing.T) {
	m, calls := notificationModel(t)
	channel := m.main.conversation.ID
	for _, message := range []core.Message{
		{Timestamp: "0999999999.000001", UserID: "UALICE"},
		{Timestamp: m.notificationAfter, UserID: "UALICE"},
		{Timestamp: "2000000000.000001", UserID: "UYOU"},
	} {
		if cmd := m.notificationCmd("test", channel, []core.Message{message}); cmd != nil {
			t.Fatalf("unexpected notification for %+v", message)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("suppressed arrivals called the desktop")
	}
	// Startup cutoff is per launch, independent of a persisted unread baseline.
	m.configureNotifications(localdata.Config{Notifications: localdata.NotificationsOn}, m.notify)
	if m.notificationAfter != localdata.NewReadState(time.Now()).StartedAt {
		// The calls may straddle a microsecond, so compare the surrounding time.
		if m.notificationAfter < localdata.NewReadState(time.Now().Add(-time.Second)).StartedAt {
			t.Fatal("restart reused the account's old unread baseline")
		}
	}
	if cmd := m.notificationCmd("test", channel, []core.Message{{Timestamp: "1000000000.000001", UserID: "UALICE"}}); cmd != nil {
		t.Fatal("restart announced offline backlog")
	}
}

func TestHiddenNotificationsStaySuppressedAcrossUnhide(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, calls := notificationModel(t)
		channel := m.main.conversation.ID
		beforeHide := localdata.NewReadState(time.Now().Add(-time.Second)).StartedAt
		m.sidebar = true
		m.selectSidebar(channel)
		m.toggleSidebarHidden()
		incoming := []core.Message{{Timestamp: beforeHide, UserID: "UALICE", Text: "delayed response"}}
		if m.notificationCmd("test", channel, incoming) != nil {
			t.Fatal("hidden conversation produced a notification")
		}
		time.Sleep(time.Second)
		whileHidden := localdata.NewReadState(time.Now()).StartedAt
		time.Sleep(time.Second)
		m.sidebarMode = localdata.Hidden
		m.selectSidebar(channel)
		m.toggleSidebarHidden()
		if m.hidden[channel] {
			t.Fatal("conversation did not unhide")
		}
		incoming[0].Timestamp = whileHidden
		if m.notificationCmd("test", channel, incoming) != nil {
			t.Fatal("unhiding announced unseen hidden history")
		}
		time.Sleep(time.Second)
		incoming[0].Timestamp = localdata.NewReadState(time.Now()).StartedAt
		runNotificationCommands(m.notificationCmd("test", channel, incoming))
		if len(*calls) != 1 {
			t.Fatalf("new arrival after unhide did not notify: %v", *calls)
		}
	})
}

func TestNotificationResultsFromPollingAndLoads(t *testing.T) {
	for _, source := range []string{"inactive", "active", "thread", "initial load", "preview", "late poll after navigation"} {
		t.Run(source, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, calls := notificationModel(t)
				channel := m.main.conversation.ID
				messages := []core.Message{{ChannelID: channel, Timestamp: "2000000000.000001", UserID: "UALICE", Text: "new"}}
				var event tea.Msg
				switch source {
				case "inactive":
					event = unreadResult{channel: channel, latest: messages[0].Timestamp, messages: messages}
				case "active":
					event = pollResult{channel: channel, generation: m.main.generation, main: messages}
				case "thread":
					m.thread = &view{conversation: m.main.conversation, thread: "1000000000.000001"}
					event = pollResult{channel: channel, generation: m.main.generation, thread: m.thread.thread, replies: messages}
				case "initial load":
					event = messagesResult{channel: channel, generation: m.main.generation, page: core.MessagePage{Messages: messages}}
				case "preview":
					m.sidebar = true
					preview := m.main
					m.preview = &preview
					event = previewResult{channel: channel, generation: preview.generation, page: core.MessagePage{Messages: messages}}
				case "late poll after navigation":
					event = pollResult{channel: channel, generation: m.main.generation - 1, main: messages}
				}
				updated, cmd := m.Update(event)
				m = updated.(model)
				runNotificationCommands(cmd)
				if len(*calls) != 1 {
					t.Fatalf("%s notification count = %d", source, len(*calls))
				}
				// Another observation from any path must not repeat the alert.
				if m.notificationCmd("test", channel, messages) != nil {
					t.Fatal("observing the same message through another path notified twice")
				}
			})
		})
	}
}

func TestNotificationFailuresPreservePollingAndReadState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := notificationModel(t)
		channel := m.main.conversation.ID
		m.sidebar = true
		beforeRead := m.readState.Position(channel)
		m.notify = func(context.Context, string, string) error { return errors.New("desktop unavailable") }
		messages := []core.Message{{Timestamp: "2000000000.000001", UserID: "UALICE"}}
		updated, cmd := m.Update(unreadResult{channel: channel, latest: messages[0].Timestamp, messages: messages})
		m = updated.(model)
		runNotificationCommands(cmd)
		result := notificationResult{err: errors.New("desktop unavailable")}
		updated, _ = m.Update(result)
		m = updated.(model)
		if m.unreadPolling || m.nextUnreadCheck.IsZero() || !m.isUnread(channel) || m.readState.Position(channel) != beforeRead {
			t.Fatalf("notification failure interrupted polling or changed read positions: polling=%v next=%v unread=%v read=%s before=%s latest=%s", m.unreadPolling, m.nextUnreadCheck, m.isUnread(channel), m.readState.Position(channel), beforeRead, m.latest[channel])
		}
		if !strings.Contains(m.View(), "Desktop notification failed") {
			t.Fatal("notification error was not visible")
		}
		if m.notificationCmd("test", channel, messages) != nil {
			t.Fatal("failed notification retried on every poll")
		}
		updated, _ = m.Update(notificationResult{})
		if updated.(model).notificationError != m.notificationError {
			t.Fatal("successful delivery erased the latest failure reason")
		}
	})
}

func TestNotificationConfigurationAndHiddenModeDiscovery(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.Visible, localdata.UnreadFirst, localdata.UnreadOnly, localdata.Hidden, localdata.All} {
		m, _ := notificationModel(t)
		m.sidebarMode = mode
		m.main.conversation = core.Conversation{}
		m.hidden = map[string]bool{}
		for i, c := range m.conversations {
			m.unreadIndex = i
			if next, _ := m.nextUnreadConversation(); next.ID != c.ID {
				t.Fatalf("%s mode skipped notification discovery for %s", mode, c.ID)
			}
		}
	}
	for _, test := range []struct {
		setting localdata.Notifications
		demo    bool
	}{{localdata.NotificationsOff, false}, {localdata.NotificationsOn, true}} {
		m, calls := notificationModel(t)
		m.options.Demo = test.demo
		m.configureNotifications(localdata.Config{Notifications: test.setting}, m.notify)
		m.sidebarMode, m.main.conversation = localdata.Hidden, core.Conversation{}
		if m.notificationsEnabled() || m.notificationCmd("test", "CGENERAL", []core.Message{{Timestamp: "2000000000.000001"}}) != nil || len(*calls) != 0 {
			t.Fatal("disabled or demo notifications called desktop")
		}
		if next, _ := m.nextUnreadConversation(); next.ID != "" {
			t.Fatal("disabled notifications changed the sidebar's discovery filter")
		}
	}
}
