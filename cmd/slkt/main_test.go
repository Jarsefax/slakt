package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestAppVersion(t *testing.T) {
	base := strings.TrimSpace(baseVersion)
	for _, tc := range []struct {
		name     string
		override string
		info     *debug.BuildInfo
		want     string
	}{
		{name: "source archive", want: base + "-dev+bin.abcdef012345"},
		{name: "metadata without VCS", info: &debug.BuildInfo{}, want: base + "-dev+bin.abcdef012345"},
		{name: "clean checkout", info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.modified", Value: "false"},
		}}, want: base + "-dev+0123456789ab.bin.abcdef012345"},
		{name: "modified checkout", info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.revision", Value: "0123456789abcdef"},
		}}, want: base + "-dev+0123456789ab.dirty.bin.abcdef012345"},
		{name: "explicit release", override: base, want: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := version
			version = tc.override
			t.Cleanup(func() { version = previous })
			if got := appVersion(tc.info, "abcdef012345"); got != tc.want {
				t.Fatalf("version = %q, want %q", got, tc.want)
			}
		})
	}
}
