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
						if row < 0 || lines[row] != ansi.Strip(todaySeparator(m.main.viewport.Width)) {
							t.Fatalf("Today separator is not immediately above its first message:\n%s", content)
						}
						if ansi.StringWidth(lines[row]) != m.main.viewport.Width {
							t.Fatalf("separator does not fit the pane: %q", lines[row])
						}
						if test.index > 0 && m.main.starts[test.index] != m.main.starts[test.index-1]+3 {
							t.Fatal("Today added an extra row instead of replacing the ordinary separator")
						}
					}
					if strings.Count(content, ansi.Strip(todaySeparator(m.main.viewport.Width))) != want {
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
			m.preview = &view{viewport: viewport.New(40, 3)}
			panes := []*view{&m.main, &m.search, m.thread, m.preview}
			for _, v := range panes {
				v.messages = []core.Message{messageAt(before)}
				if nextDayMessage {
					v.messages = append(v.messages, messageAt(after))
				}
				v.selected = len(v.messages) - 1
				v.messages[v.selected].Text = strings.Repeat("Long message.\n", 8)
				m.rebuildAt(v, false, before)
				header := strings.Split(messageViewContent(*v), "\n")[v.starts[0]]
				if !strings.Contains(header, "Today 23:59") {
					t.Fatalf("current-day timestamp missing Today before midnight: %q", header)
				}
				v.viewport.SetYOffset(v.starts[v.selected] + 1)
			}
			m.refreshViews(after)
			for _, v := range panes {
				if v.viewport.YOffset != v.starts[v.selected]+1 {
					t.Fatal("midnight refresh moved the scroll anchor")
				}
				content := messageViewContent(*v)
				if strings.Contains(content, ansi.Strip(todaySeparator(v.viewport.Width))) != nextDayMessage {
					t.Fatalf("stale Today separator after midnight:\n%s", content)
				}
				lines := strings.Split(content, "\n")
				if header := lines[v.starts[0]]; !strings.Contains(header, "Oct 16 23:59") || strings.Contains(header, "Today") {
					t.Fatalf("previous day's timestamp did not update: %q", header)
				}
				if nextDayMessage && !strings.Contains(strings.Split(content, "\n")[v.starts[1]-1], "Today") {
					t.Fatalf("Today separator did not move to the new day:\n%s", content)
				}
				if nextDayMessage && !strings.Contains(lines[v.starts[1]], "Today 00:01") {
					t.Fatalf("new day's timestamp missing Today: %q", lines[v.starts[1]])
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
	if content := messageViewContent(m.main); strings.Count(content, ansi.Strip(todaySeparator(m.main.viewport.Width))) != 1 {
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

func TestMessageTimestampsLabelTodayInEveryPane(t *testing.T) {
	stockholm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 21, 14, 32, 0, 0, stockholm)
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, stockholm)
	m := loadedModel(t)
	m.width = 140
	m.searchActive = true
	m.thread = &view{thread: "root", viewport: viewport.New(40, 3)}
	m.preview = &view{viewport: viewport.New(40, 3)}
	for _, pane := range []struct {
		name string
		view *view
	}{
		{"conversation", &m.main},
		{"search", &m.search},
		{"thread", m.thread},
		{"preview", m.preview},
	} {
		t.Run(pane.name, func(t *testing.T) {
			v := pane.view
			v.messages = []core.Message{messageAt(start.Add(-time.Second)), messageAt(start), messageAt(now), messageAt(start.AddDate(0, 0, 1))}
			m.rebuildAt(v, false, now)
			lines := strings.Split(messageViewContent(*v), "\n")
			for i, want := range []string{"Oct 20 23:59", "Today 00:00", "Today 14:32", "Oct 22 00:00"} {
				if header := lines[v.starts[i]]; !strings.Contains(header, want) {
					t.Fatalf("message %d header = %q, want %q", i, header, want)
				}
			}
		})
	}
}
