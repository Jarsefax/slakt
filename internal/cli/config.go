package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Jarsefax/slakt/internal/localdata"
	"github.com/spf13/cobra"
)

func addConfigCommand(root *cobra.Command, o *options) {
	config := &cobra.Command{
		Use:   "config",
		Short: "Get or set local settings",
		Long:  "Get or set local settings without Slack authentication.\nSupported keys: message_open_target (desktop or browser; default desktop), notifications and notification_status (on or off; default on).",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	config.AddCommand(&cobra.Command{
		Use:       "get <key>",
		Short:     "Print a setting's effective value, including its default",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"message_open_target", "notifications", "notification_status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkConfigKey(o, args[0]); err != nil {
				return err
			}
			store, err := localdata.DefaultStore()
			if err != nil {
				return err
			}
			settings, err := store.LoadConfig()
			if err != nil {
				return err
			}
			value := string(settings.MessageOpenTarget)
			if args[0] == "notifications" {
				value = string(settings.Notifications)
			} else if args[0] == "notification_status" {
				value = string(settings.NotificationStatus)
			}
			if o.json {
				return writeJSON(cmd.OutOrStdout(), map[string]string{args[0]: value})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), value)
			return err
		},
	})
	config.AddCommand(&cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Save a setting (message_open_target: desktop/browser; notifications and notification_status: on/off)",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{"message_open_target", "notifications", "notification_status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkConfigKey(o, args[0]); err != nil {
				return err
			}
			if args[0] == "notifications" || args[0] == "notification_status" {
				if !localdata.Notifications(args[1]).Valid() {
					return fmt.Errorf("invalid %s %q (use on or off)", args[0], args[1])
				}
			} else if !localdata.MessageOpenTarget(args[1]).Valid() {
				return fmt.Errorf("invalid message_open_target %q (use desktop or browser)", args[1])
			}
			store, err := localdata.DefaultStore()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			if args[0] == "notifications" {
				err = store.SaveNotifications(ctx, localdata.Notifications(args[1]))
			} else if args[0] == "notification_status" {
				err = store.SaveNotificationStatus(ctx, localdata.Notifications(args[1]))
			} else {
				err = store.SaveMessageOpenTarget(ctx, localdata.MessageOpenTarget(args[1]))
			}
			if err != nil {
				return err
			}
			if o.json {
				return writeJSON(cmd.OutOrStdout(), map[string]string{args[0]: args[1]})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\nRestart slkt to use the new setting.\n", args[0], args[1])
			return err
		},
	})
	root.AddCommand(config)
}

func checkConfigKey(o *options, key string) error {
	if o.demo {
		return errors.New("configuration commands are unavailable in offline demo mode; omit --demo")
	}
	if key != "message_open_target" && key != "notifications" && key != "notification_status" {
		return fmt.Errorf("unknown configuration key %q (supported keys: message_open_target, notifications, notification_status)", key)
	}
	return nil
}
