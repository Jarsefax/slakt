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
		Long:  "Get or set local settings without Slack authentication.\nSupported key: message_open_target (desktop or browser; default desktop).",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	config.AddCommand(&cobra.Command{
		Use:       "get <key>",
		Short:     "Print a setting's effective value, including its default",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"message_open_target"},
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
			if o.json {
				return writeJSON(cmd.OutOrStdout(), map[string]string{args[0]: string(settings.MessageOpenTarget)})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), settings.MessageOpenTarget)
			return err
		},
	})
	config.AddCommand(&cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Save a setting (message_open_target: desktop or browser)",
		Args:      cobra.ExactArgs(2),
		ValidArgs: []string{"message_open_target"},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkConfigKey(o, args[0]); err != nil {
				return err
			}
			target := localdata.MessageOpenTarget(args[1])
			if !target.Valid() {
				return fmt.Errorf("invalid message_open_target %q (use desktop or browser)", target)
			}
			store, err := localdata.DefaultStore()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			if err := store.SaveMessageOpenTarget(ctx, target); err != nil {
				return err
			}
			if o.json {
				return writeJSON(cmd.OutOrStdout(), map[string]string{args[0]: string(target)})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s = %s\nRestart slkt to use the new setting.\n", args[0], target)
			return err
		},
	})
	root.AddCommand(config)
}

func checkConfigKey(o *options, key string) error {
	if o.demo {
		return errors.New("configuration commands are unavailable in offline demo mode; omit --demo")
	}
	if key != "message_open_target" {
		return fmt.Errorf("unknown configuration key %q (supported key: message_open_target)", key)
	}
	return nil
}
