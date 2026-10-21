package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestPollCountdownFollowsScheduledDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := loadedModel(t)
		deadline := m.nextPoll
		if got := m.pollIndicator(time.Now()); got != "poll #engineering [□□□□□] in 15s · interval 15s" {
			t.Fatalf("initial countdown = %q", got)
		}
		time.Sleep(6 * time.Second)
		if got := m.pollIndicator(time.Now()); got != "poll #engineering [■■□□□] in 9s · interval 15s" {
			t.Fatalf("countdown after six seconds = %q", got)
		}
		updated, cmd := m.Update(pollCountdownMsg{})
		m = updated.(model)
		if m.polling || m.nextPoll != deadline {
			t.Fatal("footer redraw changed the poll schedule")
		}
		if _, ok := cmd().(pollCountdownMsg); !ok {
			t.Fatal("footer redraw did not schedule another countdown tick")
		}
		// Creating the poll timer later still uses the displayed deadline.
		tick := m.tick()().(tickMsg)
		if !time.Time(tick).Equal(deadline) {
			t.Fatalf("poll fired at %s, want %s", time.Time(tick), deadline)
		}
		updated, cmd = m.Update(tick)
		m = updated.(model)
		if !m.polling || !m.nextPoll.IsZero() || cmd == nil {
			t.Fatal("deadline did not start a poll")
		}
		result := cmd()
		time.Sleep(20 * time.Second)
		updated, _ = m.Update(pollCountdownMsg{})
		m = updated.(model)
		if got := m.pollIndicator(time.Now()); got != "poll #engineering [■■■■■] polling… · interval 15s" {
			t.Fatalf("slow request indicator = %q", got)
		}
		updated, _ = m.Update(result)
		m = updated.(model)
		if m.polling || !m.nextPoll.Equal(time.Now().Add(m.options.PollInterval)) {
			t.Fatal("countdown did not restart after the request finished")
		}
	})
}

func TestPollCountdownReschedulesSkippedAndFailedPolls(t *testing.T) {
	for _, state := range []string{"loading", "sending", "no conversation", "failed", "stale result"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := loadedModel(t)
				time.Sleep(m.options.PollInterval)
				var event any = tickMsg(time.Now())
				switch state {
				case "loading":
					m.main.loading = true
				case "sending":
					m.sending = true
				case "no conversation":
					m.main.conversation.ID = ""
				case "failed", "stale result":
					m.polling = true
					result := pollResult{channel: m.main.conversation.ID, generation: m.main.generation, err: errors.New("offline")}
					if state == "stale result" {
						result.generation--
					}
					event = result
				}
				updated, cmd := m.Update(event)
				m = updated.(model)
				if m.polling || !m.nextPoll.Equal(time.Now().Add(m.options.PollInterval)) {
					t.Fatal("next poll deadline was not reset")
				}
				if tick, ok := cmd().(tickMsg); !ok || !time.Time(tick).Equal(m.nextPoll) {
					t.Fatal("scheduled poll disagrees with the countdown")
				}
			})
		})
	}
}

func TestPollFooterFitsAndRoundsUpRemainingSeconds(t *testing.T) {
	m := loadedModel(t)
	now := time.Now()
	m.options.PollInterval = 5500 * time.Millisecond
	m.nextPoll = now.Add(m.options.PollInterval)
	if got := m.pollIndicator(now); got != "poll #engineering [□□□□□] in 6s · interval 5.5s" {
		t.Fatalf("fractional interval countdown = %q", got)
	}
	if got := m.pollIndicator(m.nextPoll.Add(-time.Nanosecond)); !strings.Contains(got, "in 1s ·") {
		t.Fatalf("countdown reached zero before the deadline: %q", got)
	}
	if got := m.pollIndicator(m.nextPoll.Add(time.Second)); got != "poll #engineering [■■■■■] in 0s · interval 5.5s" {
		t.Fatalf("overdue countdown = %q", got)
	}
	for _, polling := range []bool{false, true} {
		m.polling = polling
		m.width = 40
		frame := m.View()
		lines := strings.Split(ansi.Strip(frame), "\n")
		footer := lines[len(lines)-1]
		if !strings.HasPrefix(footer, " poll #engineeri") || !strings.Contains(footer, m.identity.User) {
			t.Fatalf("poll indicator or identity missing from footer: %q", footer)
		}
		if width, _ := lipgloss.Size(frame); width > m.width {
			t.Fatalf("footer overflow: frame width %d, terminal width %d", width, m.width)
		}
	}
}

func TestPollIndicatorNamesOpenConversationInsteadOfPreview(t *testing.T) {
	m := loadedModel(t)
	m.width, m.options.PollInterval = 140, 30*time.Second
	m.nextPoll = time.Now().Add(9 * time.Second)
	m, _ = key(m, "tab")
	m, _ = key(m, "j")
	if m.preview == nil || m.preview.conversation.ID == m.main.conversation.ID {
		t.Fatal("navigation did not preview a different conversation")
	}
	if got := m.pollIndicator(time.Now()); got != "poll #engineering [■■■□□] in 9s · interval 30s" {
		t.Fatalf("poll followed the sidebar preview or ignored the configured interval: %q", got)
	}
	preview := m.preview.conversation
	m, cmd := key(m, "enter")
	updated, _ := m.Update(cmd())
	m = updated.(model)
	if got := m.pollIndicator(time.Now()); !strings.HasPrefix(got, "poll "+preview.Label()+" [") {
		t.Fatalf("poll target did not follow the explicitly opened conversation: %q", got)
	}
}

func TestRunningPollKeepsOriginalConversationAndThreadLabel(t *testing.T) {
	m := loadedModel(t)
	m.width = 140
	m.thread = &view{conversation: m.main.conversation, thread: m.main.messages[0].Timestamp}
	updated, cmd := m.Update(tickMsg(time.Now()))
	m = updated.(model)
	if got := m.pollIndicator(time.Now()); got != "poll #engineering + thread [■■■■■] polling… · interval 15s" {
		t.Fatalf("running poll scope = %q", got)
	}
	result := cmd()
	m.switchTo(core.Conversation{ID: "DALICE", Name: "alice", Kind: "dm"})
	if got := m.pollIndicator(time.Now()); !strings.Contains(got, "#engineering + thread") || strings.Contains(got, "@alice") {
		t.Fatalf("navigation renamed the in-flight request: %q", got)
	}
	updated, _ = m.Update(result)
	m = updated.(model)
	if got := m.pollIndicator(time.Now()); !strings.HasPrefix(got, "poll @alice [") || strings.Contains(got, "+ thread") || m.pollCurrent.ID != "" || m.pollThread {
		t.Fatalf("completed poll did not restore the next request's scope: %q", got)
	}
}

func TestPollFooterFitsLongNamesAndKeepsThreadAndInterval(t *testing.T) {
	for _, width := range []int{40, 70, 79, 80, 100, 140} {
		for _, thread := range []bool{false, true} {
			for _, polling := range []bool{false, true} {
				t.Run(fmt.Sprintf("width=%d/thread=%t/polling=%t", width, thread, polling), func(t *testing.T) {
					m := loadedModel(t)
					m.width, m.polling, m.options.Version = width, polling, "0.20.1"
					m.main.conversation = core.Conversation{ID: "LONG", Name: strings.Repeat("同僚👩🏽‍💻", 20), Kind: "group-dm"}
					m.nextPoll = time.Now().Add(9 * time.Second)
					if thread {
						m.thread = &view{conversation: m.main.conversation, thread: "1.000001"}
					}
					footer := m.footer(time.Now())
					if ansi.StringWidth(footer) != width || !strings.HasSuffix(footer, "slkt 0.20.1") {
						t.Fatalf("footer overflowed or displaced the version: %q", footer)
					}
					if indicator := m.pollIndicator(time.Now()); strings.Contains(indicator, "+ thread") != thread || polling && !strings.Contains(indicator, "polling…") || !polling && !strings.Contains(indicator, "15s") {
						t.Fatalf("shortened footer lost poll scope, state, or interval: %q", indicator)
					}
				})
			}
		}
	}
}

func TestVersionFooter(t *testing.T) {
	for _, version := range []string{"0.16.0", "0.16.0-dev+abcdef012345.dirty.src.abcdef012345"} {
		for _, width := range []int{40, 70, 140} {
			for _, polling := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/width=%d/polling=%t", version, width, polling), func(t *testing.T) {
					m := loadedModel(t)
					m.options.Version = version
					m.width, m.polling = width, polling
					m.nextPoll = time.Now().Add(time.Hour)
					frame := m.View()
					lines := strings.Split(ansi.Strip(frame), "\n")
					footer := lines[len(lines)-1]
					if !strings.HasPrefix(footer, " "+m.pollIndicator(time.Now())) || !strings.Contains(footer, "slkt 0.16.0") {
						t.Fatalf("poll indicator or version missing: %q", footer)
					}
					if ansi.StringWidth(footer) != width || strings.HasSuffix(footer, " ") {
						t.Fatalf("version is not aligned to the right edge at width %d: %q", width, footer)
					}
					if !strings.Contains(version, "-dev") || width == 140 {
						if !strings.HasSuffix(footer, "slkt "+version) || !strings.Contains(footer, m.identity.User) {
							t.Fatalf("full version or identity missing: %q", footer)
						}
					}
					if frameWidth, _ := lipgloss.Size(frame); frameWidth > width {
						t.Fatalf("frame width %d exceeds terminal width %d", frameWidth, width)
					}
				})
			}
		}
	}
}

func TestVersionFooterFitsLongUnicodeIdentity(t *testing.T) {
	m := loadedModel(t)
	m.options.Version = "0.16.0"
	m.width, m.polling = 40, true
	m.identity.User = strings.Repeat("同僚", 20)
	footer := m.footer(time.Now())
	if ansi.StringWidth(footer) != m.width || !strings.HasSuffix(footer, "slkt 0.16.0") || !strings.HasPrefix(footer, " "+m.pollIndicator(time.Now())) {
		t.Fatalf("long identity displaced the poll indicator or version: %q", footer)
	}
}
