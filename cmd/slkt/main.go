package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"

	"github.com/Jarsefax/slakt/internal/cli"
	"github.com/Jarsefax/slakt/internal/render"
)

//go:embed VERSION
var baseVersion string

// version is set by the build or installer. Plain go builds use VCS metadata.
var version string

func appVersion(info *debug.BuildInfo) string {
	if version != "" {
		return version
	}
	label := strings.TrimSpace(baseVersion) + "-dev"
	var revision string
	var dirty bool
	if info != nil {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				dirty = setting.Value == "true"
			}
		}
	}
	if revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		label += "+" + revision
		if dirty {
			label += ".dirty"
		}
	}
	return label
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	info, _ := debug.ReadBuildInfo()
	if err := cli.New(appVersion(info)).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "slkt: "+render.Safe(err.Error()))
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
