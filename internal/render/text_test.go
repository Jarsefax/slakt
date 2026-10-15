package render

import (
	"strings"
	"testing"
)

func TestTerminalContentCannotExecuteControlSequences(t *testing.T) {
	payload := "hello\x1b[2J\x1b]52;c;Y2xpcGJvYXJk\a\x1bPpayload\x1b\\world\x00\r\u009b\u202e\nnext"
	got := Safe(payload)
	if got != "helloworld\nnext" {
		t.Fatalf("unsafe terminal text: %q", got)
	}
	got = Text("<@U1> &amp; &#27;[31m <https://example.com|link>", map[string]string{"U1": "alice"})
	if strings.ContainsRune(got, '\x1b') || !strings.Contains(got, "@alice &") || !strings.Contains(got, "link (https://example.com)") {
		t.Fatalf("unsafe markup render: %q", got)
	}
}
