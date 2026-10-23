package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestNotificationCountersSeparateDispatchSuccessAndFailure(t *testing.T) {
	m, calls := notificationModel(t)
	success := m.notify
	m.notify = func(ctx context.Context, title, body string) error {
		_ = success(ctx, title, body)
		return errors.New("desktop unavailable")
	}
	message := core.Message{Timestamp: "2000000000.000001", UserID: "UALICE", Text: "hello"}
	cmd := m.notificationCmd("test", m.main.conversation.ID, []core.Message{message})
	if cmd == nil || m.notificationTried != 1 || m.notificationSent != 0 || m.notificationFailed != 0 || len(*calls) != 0 {
		t.Fatal("dispatch was not counted separately from completion")
	}
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if m.notificationTried != 1 || m.notificationSent != 0 || m.notificationFailed != 1 || len(*calls) != 1 {
		t.Fatal("failed delivery was counted as a successful send")
	}
	lastError := m.notificationError
	m.notify = success
	message.Timestamp = "2000000000.000002"
	cmd = m.notificationCmd("test", m.main.conversation.ID, []core.Message{message})
	updated, _ = m.Update(cmd())
	m = updated.(model)
	if m.notificationTried != 2 || m.notificationSent != 1 || m.notificationFailed != 1 || len(*calls) != 2 || m.notificationError != lastError {
		t.Fatal("successful delivery lost the failure history or miscounted attempts")
	}
	if got := m.notificationIndicator(200); !strings.HasPrefix(got, "notify: tried 2 · sent 1 · failed 1") || !strings.Contains(got, "desktop unavailable") {
		t.Fatalf("counter or latest failure missing: %s", got)
	}
	if m.notificationCmd("test", m.main.conversation.ID, []core.Message{message}) != nil || m.notificationTried != 2 {
		t.Fatal("duplicate observation was counted as an attempt")
	}
	message.Timestamp = "2000000000.000003"
	m.hidden[m.main.conversation.ID] = true
	if m.notificationCmd("test", m.main.conversation.ID, []core.Message{message}) != nil || m.notificationTried != 2 {
		t.Fatal("hidden conversation was counted as an attempt")
	}
}

func TestNotificationStatusSettingDoesNotDisableDelivery(t *testing.T) {
	m, calls := notificationModel(t)
	m.configureNotifications(localdata.Config{Notifications: localdata.NotificationsOn, NotificationStatus: localdata.NotificationsOff}, m.notify)
	m.notificationAfter = "1000000000.000000"
	if m.showNotificationStatus() || strings.Contains(m.View(), "notify:") || !m.notificationsEnabled() {
		t.Fatal("counter setting did not hide only the counter row")
	}
	cmd := m.notificationCmd("test", m.main.conversation.ID, []core.Message{{Timestamp: "2000000000.000001", UserID: "UALICE"}})
	if cmd == nil {
		t.Fatal("hiding counters disabled desktop delivery")
	}
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if len(*calls) != 1 || m.notificationTried != 1 || m.notificationSent != 1 {
		t.Fatal("delivery did not run with counters hidden")
	}
	m.configureNotifications(localdata.Config{Notifications: localdata.NotificationsOff, NotificationStatus: localdata.NotificationsOn}, m.notify)
	if !m.showNotificationStatus() || m.notificationIndicator(100) != "notify: off" {
		t.Fatal("disabled notifications were not labeled off")
	}
	restarted, _ := notificationModel(t)
	if restarted.notificationTried != 0 || restarted.notificationSent != 0 || restarted.notificationFailed != 0 || restarted.notificationError != "" {
		t.Fatal("new session retained old diagnostics")
	}
}

func TestNotificationStatusFitsTerminalAndCanBeHidden(t *testing.T) {
	for _, setting := range []localdata.Notifications{localdata.NotificationsOn, localdata.NotificationsOff} {
		for _, width := range []int{40, 80, 140} {
			for _, height := range []int{15, 30} {
				for _, mode := range []string{"", "compose", "jump", "search"} {
					m, _ := notificationModel(t)
					m.notificationStatus = setting
					m.notificationTried, m.notificationSent, m.notificationFailed = 12345, 12344, 1
					m.notificationError = "Desktop notification failed: " + strings.Repeat("desktop unavailable ", 20)
					m.mode = mode
					updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
					m = updated.(model)
					frame := m.View()
					if w, h := lipgloss.Size(frame); w > width || h != height {
						t.Fatalf("setting=%s mode=%s terminal=%dx%d frame=%dx%d", setting, mode, width, height, w, h)
					}
					lines := strings.Split(ansi.Strip(frame), "\n")
					if !strings.HasPrefix(lines[len(lines)-2], " poll ") || !strings.HasPrefix(lines[len(lines)-1], " unread:") {
						t.Fatal("notification status displaced poll or unread indicators")
					}
					if strings.Contains(ansi.Strip(frame), "notify:") != (setting == localdata.NotificationsOn) {
						t.Fatal("notification status visibility ignored configuration")
					}
					if setting == localdata.NotificationsOn {
						row := lines[len(lines)-3]
						if !strings.Contains(row, "12345") || !strings.Contains(row, "12344") || !strings.Contains(row, "f1") && !strings.Contains(row, "failed 1") {
							t.Fatalf("counter row lost a value: %q", row)
						}
					}
				}
			}
		}
	}
	m := loadedModel(t)
	if !m.showNotificationStatus() || m.notificationIndicator(100) != "notify: off (demo)" {
		t.Fatal("demo did not show the default diagnostic row without sending alerts")
	}
}
