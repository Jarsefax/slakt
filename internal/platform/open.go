package platform

import (
	"errors"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// OpenURL passes an HTTPS URL directly to the OS, never through a shell.
func OpenURL(link string) error {
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || strings.ContainsAny(link, "\r\n\x00") {
		return errors.New("refusing to open an invalid HTTPS URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
