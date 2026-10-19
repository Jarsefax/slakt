package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSlackMessageURLsTargetSelectedMessage(t *testing.T) {
	for _, test := range []struct {
		name, target, channel, ts, thread, want string
	}{
		{"desktop channel", "desktop", "C1", "1234567890.000002", "", "slack://channel?id=C1&message=1234567890.000002&team=T1"},
		{"desktop DM reply", "desktop", "D1", "1234567890.000002", "1234567890.000001", "slack://channel?id=D1&message=1234567890.000002&team=T1&thread_ts=1234567890.000001"},
		{"browser channel", "browser", "C1", "1234567890.000002", "", "https://app.slack.com/client/T1/C1?cid=C1&message_ts=1234567890.000002"},
		{"browser group DM reply", "browser", "G1", "1234567890.000002", "1234567890.000001", "https://app.slack.com/client/T1/G1/thread/G1-1234567890.000001?cid=G1&message_ts=1234567890.000002"},
		{"browser root", "browser", "C1", "1234567890.000001", "1234567890.000001", "https://app.slack.com/client/T1/C1?cid=C1&message_ts=1234567890.000001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := slackMessageURL(test.target, "T1", test.channel, test.ts, test.thread)
			if err != nil || got != test.want {
				t.Fatalf("message link = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestSlackMessageRejectsInvalidTargetsAndIdentifiers(t *testing.T) {
	for _, args := range [][5]string{
		{"both", "T1", "C1", "1234567890.000001", ""},
		{"", "T1", "C1", "1234567890.000001", ""},
		{"desktop", "", "C1", "1234567890.000001", ""},
		{"desktop", "T1&team=T2", "C1", "1234567890.000001", ""},
		{"browser", "T1", "C1/../../other", "1234567890.000001", ""},
		{"desktop", "T1", "C1", "1234567890.000001&message=other", ""},
		{"browser", "T1", "C1", "1234567890.000001", "bad-thread"},
	} {
		if link, err := slackMessageURL(args[0], args[1], args[2], args[3], args[4]); err == nil {
			t.Fatalf("accepted invalid message link: %q", link)
		}
	}
	if err := OpenURL("slack://channel?team=T1&id=C1"); err == nil {
		t.Fatal("general URL opener accepted a non-HTTPS URL")
	}
}

func TestSlackMessageLaunchesOnlyConfiguredTargetAndReportsFailure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("exercises the Linux xdg-open boundary")
	}
	root := t.TempDir()
	launcher := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$SLKT_TEST_OPEN_LOG\"\nexit \"$SLKT_TEST_OPEN_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(root, "xdg-open"), []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	for _, target := range []string{"desktop", "browser"} {
		for _, exit := range []string{"0", "1"} {
			t.Run(target+"/exit-"+exit, func(t *testing.T) {
				logPath := filepath.Join(t.TempDir(), "opened")
				t.Setenv("SLKT_TEST_OPEN_LOG", logPath)
				t.Setenv("SLKT_TEST_OPEN_EXIT", exit)
				err := OpenSlackMessage(target, "T1", "C1", "1234567890.000001", "")
				if (err != nil) != (exit != "0") {
					t.Fatalf("launch error = %v for exit %s", err, exit)
				}
				data, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				links := strings.Split(strings.TrimSpace(string(data)), "\n")
				want, _ := slackMessageURL(target, "T1", "C1", "1234567890.000001", "")
				if len(links) != 1 || links[0] != want {
					t.Fatalf("expected exactly one launch of %q, got %q", want, links)
				}
			})
		}
	}
}
