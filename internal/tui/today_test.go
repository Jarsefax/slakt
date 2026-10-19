package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func messageAt(t time.Time) core.Message {
	return core.Message{Timestamp: fmt.Sprintf("%d.000001", t.Unix()), User: "Alice", Text: "A message."}
}

func messageViewContent(v view) string {
	v.viewport.Height = v.viewport.TotalLineCount()
	v.viewport.SetYOffset(0)
	return ansi.Strip(v.viewport.View())
}

func TestTodaySeparatorUsesLocalCalendarDay(t *testing.T) {
	stockholm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{
		time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 29, 12, 0, 0, 0, stockholm),  // 23-hour day.
		time.Date(2026, 10, 25, 12, 0, 0, 0, stockholm), // 25-hour day.
	} {
		t.Run(now.Format("2006-01-02 MST"), func(t *testing.T) {
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			end := start.AddDate(0, 0, 1)
			for _, test := range []struct {
				name     string
				messages []core.Message
				index    int
			}{
				{name: "empty", index: -1},
				{name: "older only", messages: []core.Message{messageAt(start.AddDate(0, 0, -2)), messageAt(start.Add(-time.Second))}, index: -1},
				{name: "today only", messages: []core.Message{messageAt(start), messageAt(end.Add(-time.Second))}, index: 0},
				{name: "day boundaries", messages: []core.Message{messageAt(start.Add(-time.Second)), messageAt(start), messageAt(end.Add(-time.Second)), messageAt(end)}, index: 1},
				{name: "future only", messages: []core.Message{messageAt(end)}, index: -1},
				{name: "invalid timestamps", messages: []core.Message{{Timestamp: "invalid"}, {Timestamp: "999999999999999999999999.000001"}, messageAt(start)}, index: 2},
			} {
				t.Run(test.name, func(t *testing.T) {
					m := loadedModel(t)
					m.main.messages, m.main.selected = test.messages, 0
					m.rebuildAt(&m.main, false, now)
					content := messageViewContent(m.main)
					want := 0
					if test.index >= 0 {
						want = 1
						lines := strings.Split(content, "\n")
						row := m.main.starts[test.index] - 1
						if row < 0 || !strings.Contains(lines[row], " Today ") {
							t.Fatalf("Today separator is not immediately above its first message:\n%s", content)
						}
						if ansi.StringWidth(lines[row]) != m.main.viewport.Width {
							t.Fatalf("separator does not fit the pane: %q", lines[row])
						}
						if test.index > 0 && m.main.starts[test.index] != m.main.starts[test.index-1]+3 {
							t.Fatal("Today added an extra row instead of replacing the ordinary separator")
						}
					}
					if strings.Count(content, "Today") != want {
						t.Fatalf("want %d Today separators:\n%s", want, content)
					}
				})
			}
		})
	}
}

func TestTodaySeparatorRefreshesCachedPanesAfterMidnight(t *testing.T) {
	before := time.Date(2026, 10, 16, 23, 59, 0, 0, time.Local)
	after := before.Add(2 * time.Minute)
	for _, nextDayMessage := range []bool{false, true} {
		t.Run(fmt.Sprintf("next day message=%t", nextDayMessage), func(t *testing.T) {
			m := loadedModel(t)
			m.height = 15
			m.searchActive = true
			m.thread = &view{thread: "root", viewport: viewport.New(40, 3)}
			panes := []*view{&m.main, &m.search, m.thread}
			for _, v := range panes {
				v.messages = []core.Message{messageAt(before)}
				if nextDayMessage {
					v.messages = append(v.messages, messageAt(after))
				}
				v.selected = len(v.messages) - 1
				v.messages[v.selected].Text = strings.Repeat("Long message.\n", 8)
				m.rebuildAt(v, false, before)
				v.viewport.SetYOffset(v.starts[v.selected] + 1)
			}
			m.refreshViews(after)
			for _, v := range panes {
				if v.viewport.YOffset != v.starts[v.selected]+1 {
					t.Fatal("midnight refresh moved the scroll anchor")
				}
				content := messageViewContent(*v)
				if strings.Contains(content, "Today") != nextDayMessage {
					t.Fatalf("stale Today separator after midnight:\n%s", content)
				}
				if nextDayMessage && !strings.Contains(strings.Split(content, "\n")[v.starts[1]-1], "Today") {
					t.Fatalf("Today separator did not move to the new day:\n%s", content)
				}
			}
		})
	}
}

func TestTodaySeparatorPreservesNavigationAndHistoryAnchors(t *testing.T) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	m := loadedModel(t)
	m.height = 15
	m.main.messages = []core.Message{messageAt(start), messageAt(start.Add(time.Minute))}
	m.main.selected = 0
	m.rebuild(&m.main, false)
	m.main.viewport.GotoBottom()
	m.reveal(&m.main)
	if m.main.viewport.YOffset != 0 || !strings.Contains(m.main.viewport.View(), "Today") {
		t.Fatal("navigating to today's first message hid the separator")
	}

	m.main.viewport.SetYOffset(m.main.starts[0] + 1)
	selected := m.main.messages[0].Timestamp
	m.merge(&m.main, []core.Message{messageAt(start.Add(-time.Minute))}, false, true)
	if m.main.messages[m.main.selected].Timestamp != selected || m.main.viewport.YOffset != m.main.starts[1]+1 {
		t.Fatal("loading older history moved the selection or scroll anchor")
	}
	assertMessageNumbers(t, m.main, []string{"1", "2", "1"})
	if content := messageViewContent(m.main); strings.Count(content, "Today") != 1 {
		t.Fatalf("loading older history duplicated or removed Today:\n%s", content)
	}

	m, _ = key(m, "g")
	m, _ = key(m, "j")
	if !strings.Contains(m.main.viewport.View(), "Today") {
		t.Fatal("navigating across the day boundary hid the separator")
	}
	for _, size := range [][2]int{{40, 15}, {125, 30}, {140, 40}} {
		m.width, m.height = size[0], size[1]
		m.rebuild(&m.main, true)
		if width, height := lipgloss.Size(m.View()); width > size[0] || height > size[1] {
			t.Fatalf("Today separator overflows terminal %v: %dx%d", size, width, height)
		}
	}
}
