package tui

import (
	"errors"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestNotificationDecisionReportsEveryEligibilityGate(t *testing.T) {
	for _, test := range []struct{ name, reason string }{
		{"eligible", "queued"},
		{"startup", "before startup"},
		{"seen", "already examined"},
		{"self", "own message"},
		{"hidden", "hidden"},
		{"unknown", "unknown conversation"},
		{"empty", "no messages"},
		{"no timestamp", "missing timestamp"},
		{"disabled", "disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, calls := notificationModel(t)
			channel := m.main.conversation.ID
			messages := []core.Message{{Timestamp: "2000000000.000001", UserID: "UALICE", Text: "private message body"}}
			switch test.name {
			case "startup":
				m.notificationAfter = messages[0].Timestamp
			case "seen":
				m.notificationSeen[channel] = messages[0].Timestamp
			case "self":
				messages[0].UserID = m.identity.UserID
			case "hidden":
				m.hidden[channel] = true
			case "unknown":
				channel = "DUNKNOWN"
			case "empty":
				messages = nil
			case "no timestamp":
				messages[0].Timestamp = ""
			case "disabled":
				m.notify = nil
			}
			before := m.notificationSeen[channel]
			cmd := m.notificationCmd("background", channel, messages)
			trace := m.notificationDecisions[channel]
			if trace.message.reason != test.reason || trace.message.source != "background" || trace.message.seen != before || trace.message.cutoff != max(m.notificationAfter, before) {
				t.Fatalf("unexpected decision: %+v", trace)
			}
			var wantTried uint64
			if test.reason == "queued" {
				wantTried = 1
			}
			if (cmd != nil) != (test.reason == "queued") || m.notificationTried != wantTried || len(*calls) != 0 {
				t.Fatal("diagnostics changed notification dispatch or ran the desktop call synchronously")
			}
			m.main.conversation.ID = channel
			for _, line := range m.notificationDetails() {
				if strings.Contains(line, "private message body") {
					t.Fatal("diagnostics retained message content")
				}
			}
		})
	}
}

func TestDisplayedDMSuppressionExplainsStartupCutoff(t *testing.T) {
	m, calls := notificationModel(t)
	dm := core.Conversation{ID: "DTRACE", Name: "alice", Kind: "dm"}
	m.conversations = append(m.conversations, dm)
	m.switchTo(dm)
	m.notificationAfter = "2000000000.000000"
	message := core.Message{ChannelID: dm.ID, Timestamp: "1999999999.000001", UserID: "UALICE", Text: "incoming DM visible in timeline"}
	updated, _ := m.Update(messagesResult{channel: dm.ID, generation: m.main.generation, page: core.MessagePage{Messages: []core.Message{message}}})
	m = updated.(model)
	if !strings.Contains(m.View(), message.Text) || m.notificationTried != 0 || len(*calls) != 0 {
		t.Fatal("startup suppression did not reproduce a visible DM with zero attempts")
	}
	trace, ok := m.notificationTraceForView()
	if !ok || trace.message.reason != "before startup" || trace.message.source != "history" || trace.message.timestamp != message.Timestamp || trace.message.cutoff != m.notificationAfter {
		t.Fatalf("visible DM suppression was not explained: %+v", trace)
	}
	_, help := m.helpContent(140)
	text := strings.Join(help, "\n")
	for _, want := range []string{"Notification decision", "before startup", "source: history", message.Timestamp, m.notificationAfter, "UTC"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help omitted %q: %s", want, text)
		}
	}
	// An arrival after the same cutoff still queues and delivers normally.
	message.Timestamp = "2000000000.000001"
	updated, cmd := m.Update(messagesResult{channel: dm.ID, generation: m.main.generation, page: core.MessagePage{Messages: []core.Message{message}}})
	m = updated.(model)
	if cmd == nil || m.notificationTried != 1 || m.notificationDecisions[dm.ID].message.reason != "queued" {
		t.Fatal("eligible DM did not retain its ordinary notification behavior")
	}
	runNotificationCommands(cmd)
	if len(*calls) != 1 {
		t.Fatal("eligible DM was not delivered")
	}
}

func TestNotificationDecisionPersistsAcrossEmptyChecksAndOtherConversations(t *testing.T) {
	m, _ := notificationModel(t)
	channel := m.main.conversation.ID
	message := core.Message{Timestamp: "2000000000.000001", UserID: "UALICE"}
	m.notificationCmd("history", "DUNKNOWN", []core.Message{message})
	m.notificationCmd("background", "DUNKNOWN", []core.Message{message})
	trace := m.notificationDecisions["DUNKNOWN"]
	if trace.message.reason != "already examined" || trace.message.firstReason != "unknown conversation" || trace.message.seen != message.Timestamp {
		t.Fatalf("repeated checks lost the first rejection or previous cutoff: %+v", trace)
	}
	m.notificationCmd("background", "DUNKNOWN", nil)
	m.notificationSkipped("poll", "DUNKNOWN", nil, "fetch failed")
	m.notificationCmd("background", channel, []core.Message{message})
	m.main.conversation.ID = "DUNKNOWN"
	trace, ok := m.notificationTraceForView()
	if !ok || trace.message.timestamp != message.Timestamp || trace.check.reason != "fetch failed" || trace.message.firstReason != "unknown conversation" {
		t.Fatalf("empty, failed, or unrelated checks erased the evidence: %+v", trace)
	}
	m.notificationSkipped("history", "DUNKNOWN", []core.Message{{Timestamp: "1000000000.000001"}}, "history pagination")
	trace, _ = m.notificationTraceForView()
	if trace.message.timestamp != message.Timestamp || trace.check.reason != "history pagination" {
		t.Fatal("loading older history erased the newer message decision")
	}
	m.sidebar, m.sidebarMode = true, localdata.All
	m.selectSidebar(channel)
	trace, _ = m.notificationTraceForView()
	if trace.message.channel != channel {
		t.Fatal("sidebar diagnostics did not follow the selected conversation")
	}
}

func TestNotificationDecisionSourcesAndBypassedDisplayPaths(t *testing.T) {
	for _, test := range []struct{ name, source, reason string }{
		{"preview", "preview", "queued"}, {"history", "history", "queued"}, {"background", "background", "queued"}, {"poll", "poll", "queued"},
		{"older history", "history", "history pagination"}, {"search", "search", "search results"},
		{"failed preview", "preview", "fetch failed"}, {"failed history", "history", "fetch failed"},
		{"failed background", "background", "fetch failed"}, {"failed poll", "poll", "fetch failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, calls := notificationModel(t)
				channel := m.main.conversation.ID
				messages := []core.Message{{ChannelID: channel, Timestamp: "2000000000.000001", UserID: "UALICE", Text: "incoming"}}
				var event tea.Msg
				switch test.name {
				case "preview":
					preview := m.main
					m.preview, m.sidebar = &preview, true
					event = previewResult{channel: channel, generation: preview.generation, page: core.MessagePage{Messages: messages}}
				case "history", "older history":
					event = messagesResult{channel: channel, generation: m.main.generation, more: test.name == "older history", page: core.MessagePage{Messages: messages}}
				case "background":
					event = unreadResult{channel: channel, latest: messages[0].Timestamp, messages: messages}
				case "poll":
					event = pollResult{channel: channel, generation: m.main.generation, main: messages}
				case "search":
					event = searchResult{query: "", page: 1, result: core.SearchResult{Messages: messages}}
				case "failed background":
					event = unreadResult{channel: channel, err: errors.New("offline")}
				case "failed history":
					event = messagesResult{channel: channel, generation: m.main.generation, err: errors.New("offline")}
				case "failed poll":
					event = pollResult{channel: channel, generation: m.main.generation, err: errors.New("offline")}
				case "failed preview":
					preview := m.main
					m.preview, m.sidebar = &preview, true
					event = previewResult{channel: channel, generation: preview.generation, err: errors.New("offline")}
				}
				updated, cmd := m.Update(event)
				m = updated.(model)
				if trace := m.notificationDecisions[channel]; trace.check.reason != test.reason || trace.check.source != test.source {
					t.Fatalf("source %s: %+v", test.name, trace)
				}
				runNotificationCommands(cmd)
				if (len(*calls) != 0) != (test.reason == "queued") {
					t.Fatal("recording bypassed display paths changed delivery behavior")
				}
			})
		})
	}
}

func TestNotificationDecisionLayoutAndStatusToggle(t *testing.T) {
	for _, width := range []int{40, 80, 140} {
		for _, setting := range []localdata.Notifications{localdata.NotificationsOn, localdata.NotificationsOff} {
			m, _ := notificationModel(t)
			m.notificationStatus = setting
			m.notificationAfter = "2000000000.000000"
			m.notificationCmd("history", m.main.conversation.ID, []core.Message{{Timestamp: "1999999999.000001", UserID: "UALICE"}})
			updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 15})
			m = updated.(model)
			frame := m.View()
			if w, h := lipgloss.Size(frame); w > width || h != m.height {
				t.Fatalf("diagnostic footer overflowed: %dx%d at width %d", w, h, width)
			}
			if strings.Contains(ansi.Strip(frame), "before startup") != (setting == localdata.NotificationsOn) {
				t.Fatal("decision reason was clipped or ignored notification_status")
			}
			_, help := m.helpContent(width - 4)
			if strings.Contains(strings.Join(help, "\n"), "Notification decision") != (setting == localdata.NotificationsOn) {
				t.Fatal("notification_status did not control diagnostic help")
			}
		}
	}
}
