package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
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

func appVersion(info *debug.BuildInfo, fingerprint string) string {
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
	if fingerprint != "" {
		separator := "+"
		if revision != "" {
			separator = "."
		}
		label += separator + "bin." + fingerprint
	}
	return label
}

func executableFingerprint() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12], nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	info, _ := debug.ReadBuildInfo()
	var fingerprint string
	if version == "" {
		var err error
		fingerprint, err = executableFingerprint()
		if err != nil {
			fmt.Fprintln(os.Stderr, "slkt: cannot fingerprint this build:", err)
			os.Exit(1)
		}
	}
	if err := cli.New(appVersion(info, fingerprint)).ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "slkt: "+render.Safe(err.Error()))
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			os.Exit(130)
		}
		os.Exit(1)
	}
}
