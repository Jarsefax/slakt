package render

import (
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
)

func TestTimeAtUsesLocalCalendarDay(t *testing.T) {
	stockholm, err := time.LoadLocation("Europe/Stockholm")
	if err != nil {
		t.Fatal(err)
	}
	for _, now := range []time.Time{
		time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 21, 12, 0, 0, 0, stockholm),
		time.Date(2026, 3, 29, 12, 0, 0, 0, stockholm),  // 23-hour day.
		time.Date(2026, 10, 25, 12, 0, 0, 0, stockholm), // 25-hour day.
		time.Date(2026, 1, 1, 12, 0, 0, 0, time.FixedZone("UTC-7", -7*60*60)),
	} {
		t.Run(now.Format("2006-01-02 MST"), func(t *testing.T) {
			start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			end := start.AddDate(0, 0, 1)
			for _, test := range []struct {
				name string
				at   time.Time
				want string
			}{
				{"previous day", start.Add(-time.Second), start.Add(-time.Second).Format("Jan 02 15:04")},
				{"midnight", start, "Today 00:00"},
				{"midday", now, "Today 12:00"},
				{"last second", end.Add(-time.Second), "Today 23:59"},
				{"next day", end, end.Format("Jan 02 15:04")},
				{"previous year", now.AddDate(-1, 0, 0), now.AddDate(-1, 0, 0).Format("Jan 02 15:04")},
			} {
				t.Run(test.name, func(t *testing.T) {
					ts := fmt.Sprintf("%d.000001", test.at.Unix())
					if got := TimeAt(ts, now); got != test.want {
						t.Fatalf("TimeAt(%q, %s) = %q, want %q", ts, now, got, test.want)
					}
				})
			}
		})
	}
}

func TestTimeAtKeepsInvalidTimestampsSafe(t *testing.T) {
	for _, ts := range []string{"", "invalid", "999999999999999999999999.000001", "bad\x1b[2J\u202e"} {
		if got := TimeAt(ts, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)); got != Safe(ts) {
			t.Fatalf("TimeAt(%q) = %q, want %q", ts, got, Safe(ts))
		}
	}
}

func TestTimeAndMessageLabelCurrentDate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		ts := fmt.Sprintf("%d.000001", now.Unix())
		want := "Today " + now.Format("15:04")
		if got := Time(ts); got != want {
			t.Fatalf("Time(%q) = %q, want %q", ts, got, want)
		}
		got := Message(core.Message{Timestamp: ts, User: "Alice", Text: "Hello"}, nil)
		if !strings.HasPrefix(got, want+"  Alice\n") {
			t.Fatalf("message timestamp missing Today: %q", got)
		}
	})
}
