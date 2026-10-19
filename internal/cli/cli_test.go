package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := New("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestVersionFlags(t *testing.T) {
	for _, flag := range []string{"--version", "-V"} {
		t.Run(flag, func(t *testing.T) {
			out, err := run(t, flag)
			if err != nil || out != "slkt version test\n" {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
	}
	if out, err := run(t, "-v"); err == nil || !strings.Contains(err.Error(), "unknown shorthand flag: 'v'") {
		t.Fatalf("-v should be reserved: output=%q err=%v", out, err)
	}
}

func TestHelpFlags(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			out, err := run(t, flag)
			if err != nil || !strings.Contains(out, "-h, --help") || !strings.Contains(out, "-V, --version") || strings.Contains(out, "-v, --version") {
				t.Fatalf("output=%q err=%v", out, err)
			}
		})
	}
}

func TestOfflineCLICommands(t *testing.T) {
	for _, args := range [][]string{{"--demo", "channels", "--json"}, {"--demo", "read", "#general", "--limit", "2", "--json"}, {"--demo", "auth", "status", "--json"}, {"--demo", "search", "in:engineering deploy", "--json"}, {"--demo", "send", "#general", "hello", "--json"}, {"--demo", "doctor", "--json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := run(t, args...)
			if err != nil || !json.Valid([]byte(out)) {
				t.Fatalf("output=%s err=%v", out, err)
			}
		})
	}
}

func TestChannelsGroupTextAndKeepJSONFlat(t *testing.T) {
	out, err := run(t, "--demo", "channels")
	if err != nil {
		t.Fatal(err)
	}
	previous := -1
	for _, heading := range []string{"Starred\n", "Channels\n", "Direct messages\n", "Group messages\n"} {
		position := strings.Index(out, heading)
		if position <= previous {
			t.Fatalf("missing or misplaced %q: %s", heading, out)
		}
		previous = position
	}
	if strings.Count(out, "#engineering") != 1 {
		t.Fatalf("starred conversation was duplicated: %s", out)
	}
	out, err = run(t, "--demo", "channels", "--json")
	var conversations []core.Conversation
	if err != nil || json.Unmarshal([]byte(out), &conversations) != nil || len(conversations) == 0 || !conversations[0].IsStarred {
		t.Fatalf("flat JSON or starred metadata was lost: %s, %v", out, err)
	}
}

func TestEmojiDisplayPreservesOriginalJSON(t *testing.T) {
	out, err := run(t, "--demo", "read", "#random")
	if err != nil || !strings.Contains(out, "☕") || strings.Contains(out, ":coffee:") {
		t.Fatalf("emoji missing from plain text: %q, err=%v", out, err)
	}
	out, err = run(t, "--demo", "read", "#random", "--json")
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, ":coffee:") {
		t.Fatalf("JSON lost original emoji name: %q, err=%v", out, err)
	}
}

func TestCLIResolvesAuthorsAndMentionsWithoutPriorDirectoryLoad(t *testing.T) {
	for _, args := range [][]string{
		{"--demo", "read", "CENGINEERING"},
		{"--demo", "read", "#engineering"},
		{"--demo", "search", "deploy"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := run(t, args...)
			if err != nil || !strings.Contains(out, "  Alice\n") || !strings.Contains(out, "  Sam\n") || !strings.Contains(out, "@Sam") || strings.Contains(out, "UALICE") || strings.Contains(out, "USAM") {
				t.Fatalf("names missing: %q, err=%v", out, err)
			}
		})
	}
	out, err := run(t, "--demo", "read", "CENGINEERING", "--json")
	var page core.MessagePage
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Messages) != 2 || page.Messages[1].UserID != "UALICE" || page.Messages[1].User != "Alice" || !strings.Contains(page.Messages[1].Text, "<@USAM>") {
		t.Fatalf("JSON must retain IDs and original message text: %q, err=%v", out, err)
	}
}

func TestCLIRejectsInvalidInputWithoutOpeningTUI(t *testing.T) {
	for _, args := range [][]string{{"send", "#general"}, {"--demo", "read", "#general", "--limit", "0"}, {"--demo", "thread", "not-a-url"}, {"--demo", "auth", "login"}, {"--demo", "search", "query", "--page", "0"}, {"--json"}, {"--unknown"}} {
		if out, err := run(t, args...); err == nil {
			t.Fatalf("accepted %v: %s", args, out)
		}
	}
}

func TestJSONEscapesInvisibleControlsWithoutChangingData(t *testing.T) {
	want := "a\x1b[2J\u009b\u202e\U000e0001z"
	var out bytes.Buffer
	if err := writeJSON(&out, map[string]string{"text": want}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\u009b\u202e\U000e0001") {
		t.Fatalf("unescaped controls in %q", out.String())
	}
	var decoded map[string]string
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || decoded["text"] != want {
		t.Fatalf("JSON data changed: %q %v", decoded["text"], err)
	}
}
