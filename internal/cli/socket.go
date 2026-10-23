package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Jarsefax/slakt/internal/auth"
	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/slackapi"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func socketSource(service *core.Service, manager *auth.Manager, config auth.SocketCredentials) *slackapi.SocketSource {
	backend, _ := service.Backend.(*slackapi.Backend)
	return &slackapi.SocketSource{Backend: backend, Token: manager.SocketToken, LockPath: manager.LockPath + ".socket-" + config.StreamID}
}

// Piped setup takes one secret per line; terminal setup hides every secret.
func socketSecret(cmd *cobra.Command, reader *bufio.Reader, prompt string) (string, error) {
	var data []byte
	var err error
	if input, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), prompt+" (hidden): ")
		data, err = term.ReadPassword(int(input.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
	} else {
		var line string
		line, err = reader.ReadString('\n')
		data = []byte(line)
		if errors.Is(err, io.EOF) && len(data) > 0 {
			err = nil
		}
	}
	if err != nil || len(data) > 8192 {
		return "", fmt.Errorf("cannot read %s; provide one value per line", prompt)
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" || strings.ContainsAny(secret, " \t\r\n") {
		return "", fmt.Errorf("invalid %s; provide only the secret value", prompt)
	}
	return secret, nil
}

func addSocketCommand(parent *cobra.Command, o *options) {
	var remove bool
	command := &cobra.Command{Use: "socket", Short: "Store Socket Mode credentials securely (hidden prompts or stdin lines)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := o.getManager()
		if err != nil {
			return err
		}
		if remove {
			if err := manager.SaveSocketToken(cmd.Context(), ""); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Socket Mode credentials removed; restart slkt to use polling.")
			return nil
		}
		reader := bufio.NewReader(io.LimitReader(cmd.InOrStdin(), 3*8193+1))
		token, err := socketSecret(cmd, reader, "App-level access token (xapp- or xoxe.xapp-)")
		if err != nil {
			return err
		}
		if !auth.ValidSocketToken(token) {
			return errors.New("expected an xapp- or xoxe.xapp- app-level token with connections:write")
		}
		config := auth.SocketCredentials{AccessToken: token}
		if auth.RotatingSocketToken(token) {
			config.RefreshToken, err = socketSecret(cmd, reader, "App-level refresh token (xoxe-)")
			if err != nil {
				return err
			}
			config.ClientSecret, err = socketSecret(cmd, reader, "Slack app client secret")
			if err != nil {
				return err
			}
		}
		if input, ok := cmd.InOrStdin().(*os.File); !ok || !term.IsTerminal(int(input.Fd())) {
			rest, err := io.ReadAll(reader)
			if err != nil || strings.TrimSpace(string(rest)) != "" {
				return errors.New("unexpected extra input; provide one secret per line")
			}
		}
		service, err := o.getService()
		if err != nil {
			return err
		}
		if _, err := service.Backend.Identity(cmd.Context()); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		if !auth.RotatingSocketToken(token) {
			if err := (&slackapi.SocketSource{Backend: service.Backend.(*slackapi.Backend), AppToken: token}).Check(ctx); err != nil {
				return err
			}
		}
		// Persist the supplied rotating pair before redeeming its single-use refresh
		// token. Every replacement pair is saved before checking the socket endpoint.
		if err := manager.SaveSocketCredentials(ctx, config); err != nil {
			return err
		}
		config, err = manager.SocketConfig(ctx)
		if err != nil {
			return err
		}
		if auth.RotatingSocketToken(token) {
			if err := socketSource(service, manager, config).Check(ctx); err != nil {
				return err
			}
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Socket Mode credentials stored in OS keyring. Restart slkt to connect. Rotating credentials refresh automatically. Configure user event subscriptions as described in docs/slack-app.md.")
		return nil
	}}
	command.Flags().BoolVar(&remove, "remove", false, "Remove the active account's Socket Mode credentials")
	parent.AddCommand(command)
}
