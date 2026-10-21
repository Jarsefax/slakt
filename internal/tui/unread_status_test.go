package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func unreadStatusText(m model, now time.Time, width int) string {
	return m.unreadIndicator(now, width)
}

func TestUnreadCountdownFollowsRequestCompletion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := loadedModel(t)
		m.options.PollInterval = time.Minute
		deadline := m.nextUnreadCheck
		if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #general in 3s · checking —" {
			t.Fatalf("initial unread countdown = %q", got)
		}
		time.Sleep(time.Second)
		updated, _ := m.Update(pollCountdownMsg{})
		m = updated.(model)
		if m.nextUnreadCheck != deadline || m.unreadPolling || m.unreadIndex != 0 {
			t.Fatal("footer redraw changed the unread schedule or queue")
		}
		if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #general in 2s · checking —" {
			t.Fatalf("unread countdown after one second = %q", got)
		}
		// Creating the timer after a redraw must still use the same deadline.
		tick := m.unreadTick()().(unreadTickMsg)
		if !time.Time(tick).Equal(deadline) {
			t.Fatalf("unread check fired at %s, want %s", time.Time(tick), deadline)
		}
		updated, cmd := m.Update(tick)
		m = updated.(model)
		if !m.unreadPolling || !m.nextUnreadCheck.IsZero() || cmd == nil {
			t.Fatal("unread deadline did not start a request and suspend the countdown")
		}
		result := cmd()
		time.Sleep(20 * time.Second)
		updated, _ = m.Update(pollCountdownMsg{})
		m = updated.(model)
		if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #random · checking #general" {
			t.Fatalf("slow unread request indicator = %q", got)
		}
		if _, duplicate := m.Update(unreadTickMsg(time.Now())); duplicate != nil {
			t.Fatal("started another unread request while the first was running")
		}
		updated, cmd = m.Update(result)
		m = updated.(model)
		want := time.Now().Add(10 * time.Second) // One minute across six conversations.
		if m.unreadPolling || m.unreadCurrent.ID != "" || m.nextUnreadCheck != want {
			t.Fatal("unread countdown did not restart from request completion at the configured pace")
		}
		if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #random in 10s · checking —" {
			t.Fatalf("unread countdown after request = %q", got)
		}
		if tick := cmd().(unreadTickMsg); !time.Time(tick).Equal(want) {
			t.Fatalf("scheduled unread check disagrees with displayed deadline: %s", time.Time(tick))
		}
	})
}

func TestUnreadCountdownReschedulesFailedAndSkippedChecks(t *testing.T) {
	for _, state := range []string{"failed", "directory unavailable", "only active conversation", "empty directory"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := loadedModel(t)
				var event any = unreadTickMsg(time.Now())
				wantIndicator := "unread: next — · checking —"
				switch state {
				case "failed":
					m.unreadPolling, m.unreadCurrent = true, m.conversations[1]
					event = unreadResult{channel: m.unreadCurrent.ID, err: errors.New("offline")}
					wantIndicator = "unread: next #general in 3s · checking —"
				case "directory unavailable":
					m.identity.TeamID = ""
				case "only active conversation":
					m.conversations = []core.Conversation{m.main.conversation}
				case "empty directory":
					m.conversations = nil
				}
				updated, cmd := m.Update(event)
				m = updated.(model)
				if m.unreadPolling || m.unreadCurrent.ID != "" || m.nextUnreadCheck != time.Now().Add(m.unreadInterval()) {
					t.Fatal("failed or skipped unread check did not schedule its next deadline")
				}
				if got := unreadStatusText(m, time.Now(), 100); got != wantIndicator {
					t.Fatalf("unread indicator = %q, want %q", got, wantIndicator)
				}
				if tick := cmd().(unreadTickMsg); !time.Time(tick).Equal(m.nextUnreadCheck) {
					t.Fatal("unread timer disagrees with the saved deadline")
				}
			})
		})
	}
}

func TestDisplayedUnreadQueueFollowsConversationSwitches(t *testing.T) {
	m := sidebarModel(t)
	m.main.conversation = m.conversations[len(m.conversations)-1]
	m.unreadIndex = len(m.conversations) - 1
	m.nextUnreadCheck = time.Now().Add(2 * time.Second)
	if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #alpha in 2s · checking —" {
		t.Fatalf("queue did not wrap past the active conversation: %q", got)
	}
	updated, cmd := m.Update(unreadTickMsg(time.Now()))
	m = updated.(model)
	if got := cmd().(unreadResult); got.channel != "S1" || m.unreadCurrent.ID != got.channel {
		t.Fatalf("dispatched %s instead of the displayed conversation", got.channel)
	}
	// The running request stays named even if it becomes the open conversation.
	m.main.conversation = m.conversations[0]
	if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #bravo · checking #alpha" {
		t.Fatalf("opening the running conversation changed its status: %q", got)
	}
	// Opening the queued conversation changes which item will actually run next.
	m.main.conversation = m.conversations[1]
	if got := unreadStatusText(m, time.Now(), 100); got != "unread: next #alpha · checking #alpha" {
		t.Fatalf("displayed queue did not skip the newly active conversation: %q", got)
	}
	updated, _ = m.Update(unreadResult{channel: "S1"})
	m = updated.(model)
	updated, cmd = m.Update(unreadTickMsg(time.Now()))
	if got := cmd().(unreadResult); got.channel != "C1" {
		t.Fatalf("dispatched %s instead of the updated next conversation", got.channel)
	}
	if updated.(model).unreadCurrent.ID != "C1" {
		t.Fatal("running indicator did not follow dispatch")
	}
}

func TestUnreadFooterFitsAndPreservesErrors(t *testing.T) {
	for _, width := range []int{40, 70, 79, 80, 100, 140} {
		for _, checking := range []bool{false, true} {
			for _, version := range []string{"", "0.17.0", "0.17.0-dev+abcdef012345.dirty.src.abcdef012345"} {
				t.Run(fmt.Sprintf("width=%d/checking=%t/version=%s", width, checking, version), func(t *testing.T) {
					m := loadedModel(t)
					m.conversations = []core.Conversation{
						{ID: "C1", Name: strings.Repeat("同僚👩🏽‍💻", 30)},
						{ID: "D1", Kind: "dm", Name: strings.Repeat("ålice", 30)},
						m.main.conversation,
					}
					m.options.Version, m.polling, m.mode = version, true, "compose"
					m.nextUnreadCheck = time.Now().Add(1500 * time.Millisecond)
					if checking {
						updated, _ := m.Update(unreadTickMsg(time.Now()))
						m = updated.(model)
					}
					updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 15})
					m = updated.(model)
					m.unreadError = "Unread check paused: offline"
					frame := m.View()
					if w, h := lipgloss.Size(frame); w > width || h > m.height {
						t.Fatalf("unread status overflowed the terminal: %dx%d", w, h)
					}
					lines := strings.Split(ansi.Strip(frame), "\n")
					footer := lines[len(lines)-2]
					unread := lines[len(lines)-1]
					if strings.Contains(footer, "unread:") || !strings.HasPrefix(unread, " unread: next ") || ansi.StringWidth(unread) > width {
						t.Fatalf("unread status did not use the row below polling: %q\n%q", footer, unread)
					}
					if !strings.Contains(unread, "unread: next ") || !strings.Contains(unread, " · checking ") || checking && !strings.Contains(unread, "checking #") || !checking && !strings.Contains(unread, "in 2s") {
						t.Fatalf("unread activity or countdown missing: %q", unread)
					}
					if !strings.Contains(frame, m.unreadError) || !strings.HasPrefix(footer, " "+m.pollIndicator(time.Now())) {
						t.Fatal("unread activity displaced the existing error or poll indicator")
					}
					if version != "" && (!strings.Contains(frame, "slakt 0.17.0") || ansi.StringWidth(m.footer()) != width) {
						t.Fatalf("unread activity displaced the version: %q", m.footer())
					}
				})
			}
		}
	}
}

func TestUnreadCountdownRoundsUpUntilDeadline(t *testing.T) {
	m := loadedModel(t)
	m.nextUnreadCheck = time.Now().Add(5500 * time.Millisecond)
	for _, test := range []struct {
		now  time.Time
		want string
	}{
		{m.nextUnreadCheck.Add(-5500 * time.Millisecond), "in 6s"},
		{m.nextUnreadCheck.Add(-time.Nanosecond), "in 1s"},
		{m.nextUnreadCheck.Add(time.Second), "in 0s"},
	} {
		if got := m.unreadIndicator(test.now, 100); !strings.Contains(got, test.want) {
			t.Fatalf("unread countdown = %q, want %q", got, test.want)
		}
	}
}

func TestUnreadStatusKeepsCheckingBesideNext(t *testing.T) {
	for _, width := range []int{40, 79, 80, 100, 140} {
		t.Run(fmt.Sprintf("width=%d", width), func(t *testing.T) {
			m := loadedModel(t)
			m.width, m.options.Version = width, "0.17.1"
			m.conversations = []core.Conversation{
				m.main.conversation,
				{ID: "C1", Name: "one"},
				{ID: "D1", Kind: "dm", Name: strings.Repeat("同僚👩🏽‍💻", 20)},
			}
			now := time.Now()
			for _, polling := range []bool{false, true} {
				m.polling = polling
				for _, checking := range []bool{false, true, false} {
					m.unreadPolling, m.unreadIndex = checking, 1
					m.nextUnreadCheck = now.Add(3 * time.Second)
					if checking {
						m.unreadCurrent, m.unreadIndex = m.conversations[1], 2
						m.nextUnreadCheck = time.Time{}
					}
					text := strings.Split(ansi.Strip(m.View()), "\n")
					unread := text[len(text)-1]
					next, current, ok := strings.Cut(unread, " · checking ")
					if !ok || !strings.HasPrefix(next, " unread: next ") || current == "" {
						t.Fatalf("unread labels missing: %q", unread)
					}
					if strings.HasSuffix(next, " ") || strings.TrimSpace(current) != current || ansi.StringWidth(unread) > width {
						t.Fatalf("unread status has extra padding or overflows: %q", unread)
					}
					if !checking && unread != " unread: next #one in 3s · checking —" {
						t.Fatalf("short names did not stay together: %q", unread)
					}
				}
			}
		})
	}
}
