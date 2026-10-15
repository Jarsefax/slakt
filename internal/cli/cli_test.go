package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
