package platform

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/godbus/dbus/v5"
)

type desktopCall struct {
	app, icon, summary, body string
	replaces                 uint32
	actions                  []string
	hints                    map[string]dbus.Variant
	timeout                  int32
}

type desktopServer struct{ calls chan desktopCall }

func (s *desktopServer) Notify(app string, replaces uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, timeout int32) (uint32, *dbus.Error) {
	s.calls <- desktopCall{app, icon, summary, body, replaces, actions, hints, timeout}
	return 1, nil
}

func TestNotifyUsesPrivateSessionBusAndEscapesMessageText(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux desktop integration")
	}
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("isolated integration test requires dbus-daemon")
	}
	// This bus has no connection to the real desktop, keyring, or credentials.
	root, err := os.MkdirTemp("", "slkt-bus-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	configPath := filepath.Join(root, "bus.conf")
	config := `<busconfig><type>session</type><listen>unix:path=` + filepath.Join(root, "bus") + `</listen><auth>EXTERNAL</auth><policy context="default"><allow send_destination="*"/><allow receive_sender="*"/><allow own="*"/></policy></busconfig>`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	daemon := exec.Command(path, "--config-file="+configPath, "--nofork", "--nopidfile", "--print-address=1")
	var stderr bytes.Buffer
	daemon.Stderr = &stderr
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	address, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = daemon.Wait()
		t.Fatalf("private bus startup: %v: %s", err, stderr.String())
	}
	address = strings.TrimSpace(address)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	calls := make(chan desktopCall, 1)
	if err := server.Export(&desktopServer{calls}, "/org/freedesktop/Notifications", "org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	if reply, err := server.RequestName("org.freedesktop.Notifications", dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("claim service: %v, %v", reply, err)
	}
	if err := Notify(ctx, "#general\n· Alice\x1b[31m", "<b>hello</b> & <img src='file:///private'>\x00\nnext"); err != nil {
		t.Fatal(err)
	}
	call := <-calls
	if call.app != "Slakt" || call.summary != "#general · Alice" || call.body != "&lt;b&gt;hello&lt;/b&gt; &amp; &lt;img src=&#39;file:///private&#39;&gt; next" {
		t.Fatalf("unexpected desktop payload: %+v", call)
	}
	if call.replaces != 0 || call.timeout != -1 || len(call.actions) != 0 || call.hints["category"].Value() != "im.received" {
		t.Fatalf("unexpected notification semantics: %+v", call)
	}
	// Delivery errors are returned to the TUI rather than swallowed.
	if _, err := server.ReleaseName("org.freedesktop.Notifications"); err != nil {
		t.Fatal(err)
	}
	if err := Notify(ctx, "title", "body"); err == nil {
		t.Fatal("missing notification service was silently accepted")
	}
}

func TestNotifyRejectsMissingSessionAndCancellation(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if err := Notify(context.Background(), "title", "body"); err == nil {
		t.Fatal("missing session was accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Notify(ctx, "title", "body"); err == nil {
		t.Fatal("canceled notification was accepted")
	}
}

func TestNotificationTextBoundsUnicodeAndControls(t *testing.T) {
	text := notificationText(strings.Repeat("👋", 300)+"\x1b]52;c;secret\a", 240)
	if !utf8.ValidString(text) || len([]rune(text)) != 240 || !strings.HasSuffix(text, "…") || strings.Contains(text, "secret") {
		t.Fatalf("unbounded or unsafe notification: %q", text)
	}
}
