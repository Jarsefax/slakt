package tui

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
)

func TestBackgroundUnreadChecksFollowHiddenMode(t *testing.T) {
	for _, test := range []struct {
		mode localdata.SidebarMode
		want []string
	}{
		{localdata.Visible, []string{"S1", "C1", "D1", "G1"}},
		{localdata.UnreadFirst, []string{"S1", "C1", "D1", "G1"}},
		{localdata.UnreadOnly, []string{"S1", "C1", "D1", "G1"}},
		{localdata.Hidden, []string{"G2", "S2", "C2", "D2"}},
		{localdata.All, []string{"G2", "S1", "S2", "C1", "C2", "D1", "D2", "G1"}},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			m := sidebarModel(t)
			m.sidebarMode = test.mode
			m.main.conversation = m.conversations[4] // C3 uses the active refresh.
			m.hidden = map[string]bool{"S2": true, "C2": true, "D2": true, "G2": true}
			m.unreadIndex = len(m.conversations) - 1 // Exercise wraparound.
			backend := &unreadBackend{Backend: m.service.Backend}
			m.service = core.New(backend)
			// Cover two full rounds so filtering cannot starve eligible entries.
			for range 2 {
				for _, want := range test.want {
					next, _ := m.nextUnreadConversation()
					if next.ID != want {
						t.Fatalf("next conversation = %s, want %s", next.ID, want)
					}
					if indicator := unreadStatusText(m, time.Now(), 100); !strings.Contains(indicator, "next "+next.Label()) {
						t.Fatalf("footer disagrees with queue: %s", indicator)
					}
					updated, cmd := m.Update(unreadTickMsg(time.Now()))
					m = updated.(model)
					if !m.unreadPolling || cmd == nil {
						t.Fatal("eligible conversation did not start a check")
					}
					result := cmd().(unreadResult)
					if backend.channel != want || result.channel != want || m.unreadCurrent.ID != want || backend.opts.Limit != 1 {
						t.Fatalf("dispatched %s, result %s, checking %s; want %s", backend.channel, result.channel, m.unreadCurrent.ID, want)
					}
					updated, _ = m.Update(result)
					m = updated.(model)
				}
			}
		})
	}
}

func TestUnreadQueueFollowsModeAndHiddenChangesDuringRequest(t *testing.T) {
	m := sidebarModel(t)
	m.main.conversation = m.conversations[4]
	m.hidden = map[string]bool{"S2": true, "C2": true, "D2": true, "G2": true}
	updated, cmd := m.Update(unreadTickMsg(time.Now()))
	m = updated.(model)
	result := cmd().(unreadResult) // S1 stays in flight throughout the changes.
	if result.channel != "S1" {
		t.Fatalf("initial request = %s, want S1", result.channel)
	}
	assertNext := func(want string) {
		t.Helper()
		next, _ := m.nextUnreadConversation()
		indicator := unreadStatusText(m, time.Now(), 100)
		if next.ID != want || !strings.Contains(indicator, "next "+next.Label()) || !strings.Contains(indicator, "checking #alpha") || m.unreadCurrent.ID != "S1" {
			t.Fatalf("next = %s, want %s; status = %s", next.ID, want, indicator)
		}
		if _, duplicate := m.Update(unreadTickMsg(time.Now())); duplicate != nil {
			t.Fatal("queue change started an overlapping check")
		}
	}
	assertNext("C1")
	m, _ = key(m, "m") // Unread first.
	assertNext("C1")
	m, _ = key(m, "m") // Unread only still checks read C1.
	assertNext("C1")
	m, _ = key(m, "m") // Hidden.
	assertNext("S2")
	m.sidebar = true
	m.selectSidebar("S2")
	m, _ = key(m, "h") // Unhide the queued conversation.
	assertNext("C2")
	m, _ = key(m, "m") // All includes the newly unhidden S2.
	assertNext("S2")
	m.selectSidebar("S2")
	m, _ = key(m, "h") // Hide queued S2 again in All.
	assertNext("S2")
	m, _ = key(m, "m") // Visible skips S2 again.
	assertNext("C1")
	m.sidebar = false
	updated, _ = m.Update(result)
	m = updated.(model)
	updated, cmd = m.Update(unreadTickMsg(time.Now()))
	if got := cmd().(unreadResult); got.channel != "C1" || updated.(model).unreadCurrent.ID != got.channel {
		t.Fatalf("next request ignored the updated filter: %+v", got)
	}
}

func TestUnreadChecksWaitWhenModeHasNoEligibleConversations(t *testing.T) {
	for _, state := range []string{"all hidden", "none hidden", "only active hidden"} {
		t.Run(state, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m := sidebarModel(t)
				switch state {
				case "all hidden":
					for _, c := range m.conversations {
						m.hidden[c.ID] = true
					}
				case "none hidden":
					m.sidebarMode = localdata.Hidden
				case "only active hidden":
					m.sidebarMode = localdata.Hidden
					m.main.conversation = m.conversations[0]
					m.hidden[m.main.conversation.ID] = true
				}
				backend := &unreadBackend{Backend: m.service.Backend}
				m.service = core.New(backend)
				updated, cmd := m.Update(unreadTickMsg(time.Now()))
				m = updated.(model)
				if m.unreadPolling || backend.channel != "" || unreadStatusText(m, time.Now(), 100) != "unread: next — · checking —" {
					t.Fatal("empty queue started a request or displayed an ineligible conversation")
				}
				if cmd == nil || m.nextUnreadCheck != time.Now().Add(m.unreadInterval()) {
					t.Fatal("empty queue did not retain the bounded check schedule")
				}
				if tick := cmd().(unreadTickMsg); !time.Time(tick).Equal(m.nextUnreadCheck) {
					t.Fatal("empty queue timer disagrees with its deadline")
				}
			})
		})
	}
}

func TestUnreadOnlyDiscoversReadConversationsOutsideSidebar(t *testing.T) {
	m := sidebarModel(t)
	m.sidebarMode = localdata.UnreadOnly
	m.main.conversation = m.conversations[4]
	if slices.Contains(conversationIDs(m.sidebarConversations()), "S1") {
		t.Fatal("read conversation already appears in Unread only")
	}
	m.service = core.New(&unreadBackend{Backend: m.service.Backend, page: core.MessagePage{Messages: []core.Message{{Timestamp: "2000000000.000002"}}}})
	updated, cmd := m.Update(unreadTickMsg(time.Now()))
	m = updated.(model)
	result := cmd().(unreadResult)
	if result.channel != "S1" {
		t.Fatalf("read conversation was skipped: %s", result.channel)
	}
	updated, _ = m.Update(result)
	m = updated.(model)
	if !slices.Contains(conversationIDs(m.sidebarConversations()), "S1") || !m.isUnread("S1") {
		t.Fatal("new arrival did not make the conversation appear in Unread only")
	}
}

func TestHiddenOpenConversationAndThreadKeepRefreshing(t *testing.T) {
	for _, mode := range []localdata.SidebarMode{localdata.Visible, localdata.UnreadFirst, localdata.UnreadOnly, localdata.Hidden, localdata.All} {
		t.Run(string(mode), func(t *testing.T) {
			m := loadedModel(t)
			m, cmd := key(m, "enter")
			if cmd == nil || m.thread == nil {
				t.Fatal("thread did not open")
			}
			updated, _ := m.Update(cmd())
			m = updated.(model)
			m.sidebarMode = mode
			channel := m.main.conversation.ID
			m.hidden[channel] = true
			message, err := m.service.Send(m.ctx, channel, "", "arrived while hidden")
			if err != nil {
				t.Fatal(err)
			}
			reply, err := m.service.Send(m.ctx, channel, m.thread.thread, "reply while hidden")
			if err != nil {
				t.Fatal(err)
			}
			updated, cmd = m.Update(tickMsg(time.Now()))
			m = updated.(model)
			if !m.polling || cmd == nil {
				t.Fatal("hiding the open conversation stopped its active refresh")
			}
			updated, _ = m.Update(cmd())
			m = updated.(model)
			if newestTimestamp(m.main.messages) != message.Timestamp || newestTimestamp(m.thread.messages) != reply.Timestamp {
				t.Fatal("active refresh missed a new message or reply in the hidden conversation")
			}
		})
	}
}
