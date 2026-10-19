package platform

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// OpenURL passes an HTTPS URL directly to the OS, never through a shell.
func OpenURL(link string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(link, "\r\n\x00") {
		return errors.New("refusing to open an invalid HTTPS URL")
	}
	cmd := urlCommand(link)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

var teamID = regexp.MustCompile(`^[TE][A-Z0-9]+$`)
var channelID = regexp.MustCompile(`^[CGD][A-Z0-9]+$`)
var timestamp = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

// OpenSlackMessage opens exactly one target. Desktop links go straight to the
// registered Slack handler; browser links go to the web client, without the
// permalink landing page that can launch the desktop app as well.
func OpenSlackMessage(target, team, channel, ts, thread string) error {
	link, err := slackMessageURL(target, team, channel, ts, thread)
	if err != nil {
		return err
	}
	if output, err := urlCommand(link).CombinedOutput(); err != nil {
		return fmt.Errorf("open Slack in %s: %w: %s", target, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func slackMessageURL(target, team, channel, ts, thread string) (string, error) {
	if !teamID.MatchString(team) || !channelID.MatchString(channel) || !timestamp.MatchString(ts) || (thread != "" && !timestamp.MatchString(thread)) {
		return "", errors.New("cannot open Slack message: invalid workspace, conversation, or timestamp")
	}
	query := url.Values{}
	u := url.URL{}
	switch target {
	case "desktop":
		u.Scheme, u.Host = "slack", "channel"
		query.Set("team", team)
		query.Set("id", channel)
		query.Set("message", ts)
		if thread != "" && thread != ts {
			query.Set("thread_ts", thread)
		}
	case "browser":
		u.Scheme, u.Host = "https", "app.slack.com"
		u.Path = "/client/" + team + "/" + channel
		if thread != "" && thread != ts {
			u.Path += "/thread/" + channel + "-" + thread
		}
		query.Set("message_ts", ts)
		query.Set("cid", channel)
	default:
		return "", fmt.Errorf("invalid message open target %q (use desktop or browser)", target)
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func urlCommand(link string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		return exec.Command("xdg-open", link)
	}
}
