package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
)

func TestOpenSelectedMessageUsesConfiguredTargetAndContext(t *testing.T) {
	for _, target := range []localdata.MessageOpenTarget{"", localdata.Desktop, localdata.Browser} {
		for _, pane := range []string{"channel", "DM", "thread", "search"} {
			t.Run(string(target)+"/"+pane, func(t *testing.T) {
				m := loadedModel(t)
				if target != "" {
					m.messageOpenTarget = target
				}
				wantTarget := target
				if wantTarget == "" {
					wantTarget = localdata.Desktop
				}
				channel, thread := "C1", ""
				message := core.Message{ChannelID: channel, Timestamp: "1234567890.000002", Text: "selected rich message"}
				v := m.main
				v.messages = []core.Message{{ChannelID: "COTHER", Timestamp: "1234567890.000001", Text: "not selected"}, message}
				v.selected = 1
				switch pane {
				case "DM":
					channel = "D1"
					v.messages[1].ChannelID = channel
					m.main = v
				case "thread":
					thread = "1234567890.000001"
					v.thread = thread
					m.thread = &v
				case "search":
					thread = "1234567890.000001"
					v.messages[1].Permalink = "https://test.slack.com/archives/C1/p1234567890000002?thread_ts=" + thread
					m.search, m.searchActive = v, true
				default:
					m.main = v
				}
				calls := 0
				m.openMessage = func(gotTarget, gotTeam, gotChannel, gotTS, gotThread string) error {
					calls++
					if gotTarget != string(wantTarget) || gotTeam != m.identity.TeamID || gotChannel != channel || gotTS != message.Timestamp || gotThread != thread {
						t.Fatalf("wrong target/context: %s, %s, %s, %s, %s", gotTarget, gotTeam, gotChannel, gotTS, gotThread)
					}
					return nil
				}
				m, cmd := key(m, "o")
				if cmd == nil {
					t.Fatal("o did not open a message")
				}
				updated, _ := m.Update(cmd())
				if calls != 1 || updated.(model).status != "Opened in Slack ("+string(wantTarget)+")" {
					t.Fatalf("calls = %d, status = %q", calls, updated.(model).status)
				}
			})
		}
	}
}

func TestMessageOpenFailureIsShownWithoutAnotherLaunch(t *testing.T) {
	m := loadedModel(t)
	calls := 0
	m.openMessage = func(_, _, _, _, _ string) error {
		calls++
		return errors.New("Slack handler unavailable")
	}
	m, cmd := key(m, "o")
	updated, _ := m.Update(cmd())
	if calls != 1 || !strings.Contains(updated.(model).status, "Slack handler unavailable") {
		t.Fatalf("calls = %d, status = %q", calls, updated.(model).status)
	}
	m.main.messages = nil
	if _, cmd := key(m, "o"); cmd != nil {
		t.Fatal("o opened a message with an empty selection")
	}
}
