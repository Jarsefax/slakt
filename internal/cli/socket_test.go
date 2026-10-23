package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/auth"
	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/slackapi"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

func TestSocketSetupReadsStdinAndStoresOnlyInKeyring(t *testing.T) {
	keyring.MockInit()
	manager := &auth.Manager{Store: auth.KeyringStore{}}
	if err := manager.Save(context.Background(), auth.Credentials{AccessToken: "user-token", ClientID: "app", TeamID: "T1", UserID: "U1"}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth.test":
			fmt.Fprint(w, `{"ok":true,"team_id":"T1","user_id":"U1"}`)
		case "/apps.connections.open":
			if r.Header.Get("Authorization") != "Bearer xapp-private" {
				t.Error("incorrect token")
			}
			fmt.Fprint(w, `{"ok":true,"url":"wss://example.com/socket"}`)
		default:
			t.Error("unexpected API request")
		}
	}))
	defer server.Close()
	backend := slackapi.New(func(context.Context) (string, error) { return "user-token", nil })
	backend.APIURL, backend.HTTP = server.URL+"/", server.Client()
	parent := &cobra.Command{Use: "auth"}
	addSocketCommand(parent, &options{manager: manager, service: core.New(backend)})
	var out bytes.Buffer
	parent.SetOut(&out)
	parent.SetErr(&out)
	parent.SetIn(strings.NewReader("xapp-private\n"))
	parent.SetArgs([]string{"socket"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "xapp-private") {
		t.Fatal("setup printed token")
	}
	if token, err := manager.SocketToken(context.Background()); err != nil || token != "xapp-private" {
		t.Fatal("app token not saved")
	}
	parent.SetArgs([]string{"socket", "--remove"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	if token, err := manager.SocketToken(context.Background()); err != nil || token != "" {
		t.Fatal("app token not removed")
	}
}

func TestRotatingSocketSetupReadsThreeSecretsAndRefreshesBeforeSocketCheck(t *testing.T) {
	keyring.MockInit()
	manager := &auth.Manager{Store: auth.KeyringStore{}}
	if err := manager.Save(context.Background(), auth.Credentials{AccessToken: "user-token", ClientID: "app", TeamID: "T1", UserID: "U1"}); err != nil {
		t.Fatal(err)
	}
	var grants, checks int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth.test":
			fmt.Fprint(w, `{"ok":true,"team_id":"T1","user_id":"U1"}`)
		case "/oauth.v2.access":
			grants++
			client, secret, ok := r.BasicAuth()
			r.ParseForm()
			if !ok || client != "app" || secret != "hidden-secret" || r.Form.Get("refresh_token") != "xoxe-hidden-refresh" {
				t.Error("missing refresh credentials")
			}
			fmt.Fprint(w, `{"ok":true,"token_type":"app_level","access_token":"xoxe.xapp-new","refresh_token":"xoxe-new-refresh","expires_in":43200}`)
		case "/apps.connections.open":
			checks++
			if grants != 1 || r.Header.Get("Authorization") != "Bearer xoxe.xapp-new" {
				t.Error("socket opened before refreshed token was available")
			}
			fmt.Fprint(w, `{"ok":true,"url":"wss://example.com/socket"}`)
		default:
			t.Error("unexpected request")
		}
	}))
	defer server.Close()
	manager.OAuth = auth.OAuth{Client: server.Client(), TokenURL: server.URL + "/oauth.v2.access"}
	backend := slackapi.New(func(context.Context) (string, error) { return "user-token", nil })
	backend.APIURL, backend.HTTP = server.URL+"/", server.Client()
	parent := &cobra.Command{Use: "auth"}
	addSocketCommand(parent, &options{manager: manager, service: core.New(backend)})
	var out bytes.Buffer
	parent.SetOut(&out)
	parent.SetErr(&out)
	parent.SetIn(strings.NewReader("xoxe.xapp-expired\nxoxe-hidden-refresh\nhidden-secret\n"))
	parent.SetArgs([]string{"socket"})
	if err := parent.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"xoxe.xapp-expired", "xoxe-hidden-refresh", "hidden-secret", "xoxe.xapp-new", "xoxe-new-refresh"} {
		if strings.Contains(out.String(), secret) {
			t.Fatal("setup printed secret")
		}
	}
	config, err := manager.SocketConfig(context.Background())
	if err != nil || config.RefreshToken != "xoxe-new-refresh" || config.ClientSecret != "hidden-secret" || grants != 1 || checks != 1 {
		t.Fatal("rotating setup did not store and check refreshed credentials")
	}
}
