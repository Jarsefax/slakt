package platform

import (
	"context"
	"errors"
	"html"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Jarsefax/slakt/internal/render"
	"github.com/godbus/dbus/v5"
)

// Notify uses a private, bounded connection so a missing or stalled desktop
// cannot block the TUI or interfere with the keyring's shared D-Bus connection.
// Never autolaunch a desktop session on a headless host.
func Notify(ctx context.Context, summary, body string) error {
	if runtime.GOOS != "linux" {
		return errors.New("desktop notifications currently require Linux")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		return errors.New("desktop notifications require a session D-Bus address")
	}
	conn, err := dbus.Connect(address, dbus.WithContext(ctx))
	if err != nil {
		return err
	}
	defer conn.Close()
	// The summary is plain text; the body may be interpreted as markup.
	return conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(
		ctx, "org.freedesktop.Notifications.Notify", 0,
		"Slakt", uint32(0), "mail-message-new", notificationText(summary, 100),
		html.EscapeString(notificationText(body, 240)), []string{},
		map[string]dbus.Variant{"category": dbus.MakeVariant("im.received")}, int32(-1),
	).Err
}

func notificationText(text string, limit int) string {
	runes := []rune(strings.Join(strings.Fields(render.Safe(text)), " "))
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return string(runes)
}
