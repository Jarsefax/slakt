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
		{name: "source archive", want: base + "-dev"},
		{name: "metadata without VCS", info: &debug.BuildInfo{}, want: base + "-dev"},
		{name: "clean checkout", info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.modified", Value: "false"},
		}}, want: base + "-dev+0123456789ab"},
		{name: "modified checkout", info: &debug.BuildInfo{Settings: []debug.BuildSetting{
			{Key: "vcs.modified", Value: "true"},
			{Key: "vcs.revision", Value: "0123456789abcdef"},
		}}, want: base + "-dev+0123456789ab.dirty"},
		{name: "explicit release", override: base, want: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := version
			version = tc.override
			t.Cleanup(func() { version = previous })
			if got := appVersion(tc.info); got != tc.want {
				t.Fatalf("version = %q, want %q", got, tc.want)
			}
		})
	}
}
