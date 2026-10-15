package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/Jarsefax/slakt/internal/auth"
	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
	"github.com/Jarsefax/slakt/internal/platform"
	"github.com/Jarsefax/slakt/internal/render"
	"github.com/Jarsefax/slakt/internal/slackapi"
	"github.com/Jarsefax/slakt/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type options struct {
	demo, json bool
	poll       time.Duration
	service    *core.Service
	manager    *auth.Manager
}

func (o *options) getService() (*core.Service, error) {
	if o.service != nil {
		return o.service, nil
	}
	if o.demo {
		o.service = core.New(demo.New())
		return o.service, nil
	}
	m, err := o.getManager()
	if err != nil {
		return nil, err
	}
	o.service = core.New(slackapi.New(m.Token))
	return o.service, nil
}

func (o *options) getManager() (*auth.Manager, error) {
	if o.demo {
		return nil, errors.New("authentication is unavailable in offline demo mode; omit --demo")
	}
	if o.manager != nil {
		return o.manager, nil
	}
	m, err := auth.NewManager()
	if err != nil {
		return nil, err
	}
	o.manager = m
	return m, nil
}

func New(version string) *cobra.Command {
	o := &options{}
	root := &cobra.Command{Use: "slkt", Short: "Slack, at home in your terminal", Long: "slakt is a Slack CLI and terminal UI. Run slkt without a subcommand to browse.\nTry slkt --demo for a completely offline workspace.", Version: version, SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs}
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.PersistentFlags().BoolVar(&o.demo, "demo", false, "Use an offline in-memory workspace")
	root.PersistentFlags().BoolVar(&o.json, "json", false, "Print structured JSON for data commands")
	root.PersistentFlags().DurationVar(&o.poll, "poll-interval", 15*time.Second, "TUI refresh interval (minimum 5s)")
	root.RunE = func(cmd *cobra.Command, args []string) error { return o.openTUI(cmd, nil) }
	root.CompletionOptions.DisableDefaultCmd = true
	authCmd := &cobra.Command{Use: "auth", Short: "Authorize your Slack account", Args: cobra.NoArgs}
	root.AddCommand(authCmd)
	clientID, redirect := "", ""
	login := &cobra.Command{Use: "login", Short: "Authorize in your browser using PKCE", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		m, err := o.getManager()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()
		c, err := m.OAuth.Login(ctx, clientID, redirect, platform.OpenURL, func(link string) {
			fmt.Fprintln(cmd.ErrOrStderr(), "Authorize in your browser (expires in 5 minutes):\n"+link)
		}, func(ctx context.Context, token string) (string, string, error) {
			b := slackapi.New(func(context.Context) (string, error) { return token, nil })
			id, err := b.Identity(ctx)
			return id.TeamID, id.UserID, err
		})
		if err != nil {
			return err
		}
		if err = m.Save(ctx, c); err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), map[string]any{"authenticated": true, "team_id": c.TeamID, "user_id": c.UserID})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Logged in to %s as %s. Run slkt to open your workspace.\n", render.Safe(c.TeamID), render.Safe(c.UserID))
		return nil
	}}
	login.Flags().StringVar(&clientID, "client-id", os.Getenv("SLKT_CLIENT_ID"), "Slack app public client ID (or SLKT_CLIENT_ID)")
	defaultRedirect := os.Getenv("SLKT_REDIRECT_URL")
	if defaultRedirect == "" {
		defaultRedirect = auth.RedirectURL
	}
	login.Flags().StringVar(&redirect, "redirect-url", defaultRedirect, "Registered localhost callback URL (or SLKT_REDIRECT_URL)")
	status := &cobra.Command{Use: "status", Short: "Check your workspace and identity without revealing credentials", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := o.getService()
		if err != nil {
			return err
		}
		id, err := s.Backend.Identity(cmd.Context())
		if err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), id)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Workspace: %s (%s)\nAccount:   %s (%s)\n", render.Safe(id.Team), render.Safe(id.TeamID), render.Safe(id.User), render.Safe(id.UserID))
		return nil
	}}
	logout := &cobra.Command{Use: "logout", Short: "Remove local credentials (does not revoke the Slack app)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		m, err := o.getManager()
		if err != nil {
			return err
		}
		if err = m.Logout(cmd.Context()); err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), map[string]bool{"logged_out": true})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Local credentials removed. App authorization in Slack is unchanged.")
		return nil
	}}
	authCmd.AddCommand(login, status, logout)
	root.AddCommand(&cobra.Command{Use: "channels", Short: "List your joined channels, private conversations, and DMs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := o.getService()
		if err != nil {
			return err
		}
		channels, _, err := s.Directory(cmd.Context(), false)
		if err != nil {
			return err
		}
		if channels == nil {
			channels = []core.Conversation{}
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), channels)
		}
		for _, c := range channels {
			fmt.Fprintf(cmd.OutOrStdout(), "%-16s %-11s %s\n", render.Safe(c.ID), render.Safe(c.Kind), render.Safe(c.Label()))
		}
		return nil
	}})
	addMessagesCommand(root, o, false)
	addMessagesCommand(root, o, true)
	root.AddCommand(&cobra.Command{Use: "dm @person", Short: "Open a direct message in the TUI", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := interactive(); err != nil {
			return err
		}
		s, err := o.getService()
		if err != nil {
			return err
		}
		c, err := s.DM(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return o.openTUI(cmd, &c)
	}})
	for _, reply := range []bool{false, true} {
		use, short := "send <conversation> <text>", "Send one message as your Slack user"
		if reply {
			use, short = "reply <message-url|channel:timestamp> <text>", "Reply to a message in its thread"
		}
		root.AddCommand(&cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			s, err := o.getService()
			if err != nil {
				return err
			}
			var channel, thread string
			if reply {
				channel, thread, err = core.ParseThreadRef(args[0])
			} else {
				var c core.Conversation
				c, err = s.Resolve(cmd.Context(), args[0])
				channel = c.ID
			}
			if err != nil {
				return err
			}
			message, err := s.Send(cmd.Context(), channel, thread, args[1])
			if err != nil {
				return err
			}
			if o.json {
				return writeJSON(cmd.OutOrStdout(), message)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Sent %s:%s\n", render.Safe(message.ChannelID), render.Safe(message.Timestamp))
			return nil
		}})
	}
	root.AddCommand(&cobra.Command{Use: "react <message-url|channel:timestamp> <emoji>", Short: "Add a reaction, for example thumbsup", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		channel, ts, err := core.ParseMessageRef(args[0])
		if err != nil {
			return err
		}
		s, err := o.getService()
		if err != nil {
			return err
		}
		if err = s.React(cmd.Context(), channel, ts, args[1]); err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), map[string]bool{"reacted": true})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Reaction added.")
		return nil
	}})
	var limit, page int
	search := &cobra.Command{Use: "search <query>", Short: "Search using Slack's query syntax", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := o.getService()
		if err != nil {
			return err
		}
		result, err := s.Search(cmd.Context(), args[0], limit, page)
		if err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), result)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%d matches · page %d/%d\n\n", result.Total, result.Page, max(1, result.Pages))
		printMessages(cmd.OutOrStdout(), result.Messages)
		return nil
	}}
	search.Flags().IntVar(&limit, "limit", 30, "Results per page (1–100)")
	search.Flags().IntVar(&page, "page", 1, "Search page number")
	root.AddCommand(search)
	root.AddCommand(&cobra.Command{Use: "doctor", Short: "Check credentials, connectivity, and directory permissions", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := o.getService()
		if err != nil {
			return err
		}
		id, err := s.Backend.Identity(cmd.Context())
		if err != nil {
			return err
		}
		conversations, users, err := s.Directory(cmd.Context(), true)
		if err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), map[string]any{"ok": true, "identity": id, "conversations": len(conversations), "users": len(users), "demo": o.demo, "updates": "polling"})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK  authentication: %s / %s\nOK  directory: %d conversations, %d users\nUpdates: polling (default 15s); no Socket Mode connection.\nHistory, search, sends, and reactions require their own scopes; see docs/slack-app.md.\n", render.Safe(id.Team), render.Safe(id.User), len(conversations), len(users))
		return nil
	}})
	return root
}

func addMessagesCommand(root *cobra.Command, o *options, thread bool) {
	var limit int
	var cursor, before, after string
	use, short := "read <conversation>", "Read a page of recent messages, oldest first"
	if thread {
		use, short = "thread <message-url|channel:timestamp>", "Read a page of thread replies"
	}
	cmd := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := o.getService()
		if err != nil {
			return err
		}
		var channel, ts string
		if thread {
			channel, ts, err = core.ParseThreadRef(args[0])
		} else {
			var c core.Conversation
			c, err = s.Resolve(cmd.Context(), args[0])
			channel = c.ID
		}
		if err != nil {
			return err
		}
		result, err := s.Messages(cmd.Context(), channel, ts, core.PageOptions{Limit: limit, Cursor: cursor, Before: before, After: after})
		if err != nil {
			return err
		}
		if o.json {
			return writeJSON(cmd.OutOrStdout(), result)
		}
		printMessages(cmd.OutOrStdout(), result.Messages)
		if result.Cursor != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "Next page: --cursor %s\n", render.Safe(result.Cursor))
		} else if result.HasMore {
			if thread {
				fmt.Fprintln(cmd.OutOrStdout(), "More replies are available; use --after with the newest timestamp above.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "More history is available; use --before with the oldest timestamp above.")
			}
		}
		return nil
	}}
	cmd.Flags().IntVar(&limit, "limit", 30, "Maximum messages per page (1–200)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Slack pagination cursor from the previous page")
	cmd.Flags().StringVar(&before, "before", "", "Read messages before a Slack timestamp")
	cmd.Flags().StringVar(&after, "after", "", "Read messages after a Slack timestamp (also continues threads without a cursor)")
	root.AddCommand(cmd)
}

func interactive() error {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("the TUI needs an interactive terminal; use slkt channels, read, or search (add --json for scripts)")
	}
	return nil
}

func (o *options) openTUI(cmd *cobra.Command, c *core.Conversation) error {
	if o.json {
		return errors.New("--json requires a data subcommand, for example slkt channels --json")
	}
	if err := interactive(); err != nil {
		return err
	}
	s, err := o.getService()
	if err != nil {
		return err
	}
	return tui.Run(cmd.Context(), s, tui.Options{Initial: c, PollInterval: o.poll, Demo: o.demo})
}

func printMessages(w io.Writer, messages []core.Message) {
	for _, m := range messages {
		fmt.Fprintf(w, "%s\n  %s:%s\n\n", render.Message(m, nil), render.Safe(m.ChannelID), render.Safe(m.Timestamp))
	}
}

// Escape invisible controls in JSON without changing the decoded data. Standard
// encoding/json already escapes ASCII controls, including terminal ESC bytes.
func writeJSON(w io.Writer, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	var out strings.Builder
	for _, r := range string(b) {
		if r > 127 && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			for _, unit := range utf16.Encode([]rune{r}) {
				fmt.Fprintf(&out, "\\u%04x", unit)
			}
		} else {
			out.WriteRune(r)
		}
	}
	_, err = fmt.Fprintln(w, out.String())
	return err
}
