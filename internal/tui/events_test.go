package tui

import (
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
)

func TestSocketUpdatesInactiveUnreadAndDeduplicates(t *testing.T) {
	m, _ := notificationModel(t)
	channel := ""
	for _, c := range m.conversations {
		if c.ID != m.main.conversation.ID {
			channel = c.ID
			break
		}
	}
	event := core.LiveEvent{TeamID: m.identity.TeamID, ID: "ev1", Channel: channel, Message: core.Message{ChannelID: channel, Timestamp: "2000000000.000001", UserID: "UALICE", Text: "pushed"}}
	updated, _ := m.Update(liveResult{event: event})
	m = updated.(model)
	if !m.isUnread(channel) || m.notificationTried != 1 {
		t.Fatalf("missing immediate unread or notification: %+v", m.latest)
	}
	updated, _ = m.Update(liveResult{event: event})
	m = updated.(model)
	if m.notificationTried != 1 || m.liveRevision != 1 {
		t.Fatal("duplicate event applied twice")
	}
	event.ID, event.TeamID, event.Message.Timestamp = "ev2", "OTHER", "2000000000.000002"
	updated, _ = m.Update(liveResult{event: event})
	m = updated.(model)
	if m.latest[channel] != "2000000000.000001" {
		t.Fatal("accepted another workspace's event")
	}
}

func TestSocketEditsAndDeletionsWinOverInflightHistory(t *testing.T) {
	m := loadedModel(t)
	channel := m.main.conversation.ID
	old := m.main.messages[0]
	edited := old
	edited.Text = "pushed edit"
	m.applyLive(core.LiveEvent{Channel: channel, Message: edited, Changed: true})
	stale := pollResult{channel: channel, generation: m.main.generation, main: []core.Message{old}}
	updated, _ := m.Update(stale)
	m = updated.(model)
	if m.main.messages[0].Text != "pushed edit" {
		t.Fatal("stale read overwrote edit")
	}
	selected := m.main.messages[len(m.main.messages)-1].Timestamp
	m.main.selected = len(m.main.messages) - 1
	m.applyLive(core.LiveEvent{Channel: channel, Deleted: old.Timestamp})
	updated, _ = m.Update(stale)
	m = updated.(model)
	for _, msg := range m.main.messages {
		if msg.Timestamp == old.Timestamp {
			t.Fatal("stale read resurrected deletion")
		}
	}
	if m.main.messages[m.main.selected].Timestamp != selected {
		t.Fatal("deletion moved surviving selected message")
	}
}

func TestSocketRepliesAndReconciliationIntervals(t *testing.T) {
	m := loadedModel(t)
	parent := m.main.messages[0]
	before := len(m.main.messages)
	m.applyLive(core.LiveEvent{Channel: m.main.conversation.ID, Message: core.Message{Timestamp: "2000000000.000001", ThreadTS: parent.Timestamp, Text: "thread only"}})
	if len(m.main.messages) != before || m.main.messages[0].ReplyCount != parent.ReplyCount+1 {
		t.Fatal("reply entered main timeline or parent was not updated")
	}
	m.liveConnected = true
	if m.refreshInterval() < 2*time.Minute || m.unreadInterval() < 30*time.Second {
		t.Fatal("healthy socket still polls aggressively")
	}
	m.liveConnected = false
	if m.refreshInterval() != m.options.PollInterval || m.unreadInterval() != 3*time.Second {
		t.Fatal("disconnection did not restore polling fallback")
	}
}

func TestSocketDisconnectReplacesSlowReconciliationDeadlines(t *testing.T) {
	m := loadedModel(t)
	m.liveStarted, m.liveConnected = true, true
	m.main.loading = true
	m.nextPoll = time.Now().Add(2 * time.Minute)
	m.nextUnreadCheck = time.Now().Add(30 * time.Second)
	updated, _ := m.Update(liveResult{event: core.LiveEvent{State: "offline", Detail: "disconnected"}})
	m = updated.(model)
	if m.liveConnected || time.Until(m.nextPoll) > 16*time.Second || time.Until(m.nextUnreadCheck) > 4*time.Second {
		t.Fatal("disconnect left slow timers active")
	}
	deadline := m.nextPoll
	updated, command := m.update(tickMsg(time.Now().Add(-time.Second)))
	m = updated.(model)
	if command != nil || m.nextPoll != deadline {
		t.Fatal("stale timer changed replacement schedule")
	}
}
