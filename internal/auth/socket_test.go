package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestSocketCredentialIsAccountScopedAndLogoutRemovesIt(t *testing.T) {
	keyring.MockInit()
	ctx := context.Background()
	manager := &Manager{Store: KeyringStore{}}
	first := Credentials{AccessToken: "user", ClientID: "app1", TeamID: "T1", UserID: "U1"}
	if err := manager.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveSocketToken(ctx, "xapp-secret"); err != nil {
		t.Fatal(err)
	}
	first.AccessToken = "rotated-user"
	if err := manager.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if token, err := manager.SocketToken(ctx); err != nil || token != "xapp-secret" {
		t.Fatal("OAuth refresh discarded app token")
	}
	other := first
	other.UserID = "U2"
	if err := manager.Save(ctx, other); err != nil {
		t.Fatal(err)
	}
	if config, err := manager.SocketConfig(ctx); err != nil || config.AccessToken != "" {
		t.Fatal("app token leaked to other account")
	}
	if _, err := manager.SocketToken(ctx); err == nil {
		t.Fatal("running token source accepted an account switch")
	}
	if err := manager.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := manager.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	if err := manager.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if token, err := manager.SocketToken(ctx); err != nil || token != "" {
		t.Fatal("logout left active app token")
	}
	for _, token := range []string{"xoxp-user", "xoxe.xapp-rotating", "xapp-token\nsecond"} {
		if err := manager.SaveSocketToken(ctx, token); err == nil {
			t.Fatalf("accepted invalid app credential %q", token)
		}
	}
}

func rotatingSocketFixture(t *testing.T, handler http.HandlerFunc) *Manager {
	t.Helper()
	keyring.MockInit()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	m := &Manager{Store: &memoryStore{credentials: Credentials{AccessToken: "user", ClientID: "public-app", TeamID: "T1", UserID: "U1"}}, OAuth: OAuth{Client: server.Client(), TokenURL: server.URL}, LockPath: t.TempDir() + "/auth.lock"}
	if err := m.SaveSocketCredentials(context.Background(), SocketCredentials{AccessToken: "xoxe.xapp-old", RefreshToken: "xoxe-refresh-old", ClientSecret: "client-secret"}); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRotatingSocketRefreshSerializedAndPersistsCompletePair(t *testing.T) {
	var calls atomic.Int32
	manager := rotatingSocketFixture(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		client, secret, ok := r.BasicAuth()
		if !ok || client != "public-app" || secret != "client-secret" {
			t.Error("missing app client credentials")
		}
		r.ParseForm()
		expected := "xoxe-refresh-old"
		if n == 2 {
			expected = "xoxe-refresh-1"
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != expected || r.Form.Get("client_secret") != "" {
			t.Error("wrong refresh grant or replayed token")
		}
		fmt.Fprintf(w, `{"ok":true,"token_type":"app_level","access_token":"xoxe.xapp-%d","refresh_token":"xoxe-refresh-%d","expires_in":43200}`, n, n)
	})
	original, err := manager.SocketConfig(context.Background())
	if err != nil || calls.Load() != 0 {
		t.Fatal("configuration lookup refreshed token")
	}
	// Independent managers share the filesystem lock, as separate processes do.
	managers := []*Manager{manager, {Store: manager.Store, OAuth: manager.OAuth, LockPath: manager.LockPath}}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(func() {
			token, err := managers[i%2].SocketToken(context.Background())
			if err != nil || token != "xoxe.xapp-1" {
				t.Errorf("refresh returned unexpected result: %v", err)
			}
		})
	}
	group.Wait()
	config, err := manager.SocketConfig(context.Background())
	if err != nil || calls.Load() != 1 || config.RefreshToken != "xoxe-refresh-1" || config.ClientSecret != "client-secret" || config.RefreshPending || time.Until(config.ExpiresAt) < 11*time.Hour || config.StreamID != original.StreamID {
		t.Fatal("refresh did not persist complete credentials and stable lock")
	}
	// Near-expiry refresh uses the replacement refresh token, not the original.
	config.ExpiresAt = time.Now().Add(4 * time.Minute)
	if err := manager.SaveSocketCredentials(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if token, err := manager.SocketToken(context.Background()); err != nil || token != "xoxe.xapp-2" || calls.Load() != 2 {
		t.Fatal("second rotation failed")
	}
	user, err := manager.Store.Load()
	if err != nil || user.AccessToken != "user" {
		t.Fatal("app refresh overwrote user credentials")
	}
}

func TestUnknownSocketRefreshOutcomeIsNotReplayedAfterRestart(t *testing.T) {
	var calls atomic.Int32
	manager := rotatingSocketFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"ok":true,"access_token":"private-secret",`)
	})
	if token, err := manager.SocketToken(context.Background()); token != "" || err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("unsafe refresh result")
	}
	restarted := &Manager{Store: manager.Store, OAuth: manager.OAuth, LockPath: manager.LockPath}
	if token, err := restarted.SocketToken(context.Background()); token != "" || err == nil || calls.Load() != 1 {
		t.Fatal("single-use refresh was replayed")
	}
	config, err := manager.SocketConfig(context.Background())
	if err != nil || !config.RefreshPending {
		t.Fatal("uncertain outcome was not persisted")
	}
}

func TestExplicitSocketRefreshRejectionKeepsOriginalPair(t *testing.T) {
	manager := rotatingSocketFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"client-secret xoxe-refresh-old"}`)
	})
	if token, err := manager.SocketToken(context.Background()); token != "" || err == nil || strings.Contains(err.Error(), "xoxe-refresh-old") {
		t.Fatal("unsafe rejection")
	}
	config, err := manager.SocketConfig(context.Background())
	if err != nil || config.RefreshPending || config.RefreshToken != "xoxe-refresh-old" {
		t.Fatal("rejection consumed or changed stored pair")
	}
}

func TestSocketLegacyTokenRemainsReadableAndKeepsLockIdentity(t *testing.T) {
	keyring.MockInit()
	user := Credentials{ClientID: "app", TeamID: "T1", UserID: "U1", AccessToken: "user"}
	manager := &Manager{Store: &memoryStore{credentials: user}}
	if err := keyring.Set("slakt", socketAccount(user), "xapp-legacy"); err != nil {
		t.Fatal(err)
	}
	before, err := manager.SocketConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token, err := manager.SocketToken(context.Background()); err != nil || token != "xapp-legacy" {
		t.Fatal("legacy credential was lost")
	}
	if err := manager.SaveSocketCredentials(context.Background(), SocketCredentials{AccessToken: "xoxe.xapp-replacement", RefreshToken: "xoxe-refresh", ClientSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	after, err := manager.SocketConfig(context.Background())
	if err != nil || before.StreamID != after.StreamID {
		t.Fatal("credential replacement changed local stream lock")
	}
}

type socketRoundTripper func(*http.Request) (*http.Response, error)

func (fn socketRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestSocketRefreshDoesNotExposeTokenWhenReplacementCannotBeSaved(t *testing.T) {
	keyring.MockInit()
	manager := &Manager{Store: &memoryStore{credentials: Credentials{AccessToken: "user", ClientID: "app", TeamID: "T1", UserID: "U1"}}}
	manager.OAuth = OAuth{TokenURL: "https://slack.invalid/oauth.v2.access", Client: &http.Client{Transport: socketRoundTripper(func(r *http.Request) (*http.Response, error) {
		// This runs synchronously after the preflight keyring save, so no credential
		// operation races with swapping the test provider.
		keyring.MockInitWithError(errors.New("keyring locked"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"token_type":"app_level","access_token":"xoxe.xapp-private-replacement","refresh_token":"xoxe-private-replacement","expires_in":43200}`))}, nil
	})}}
	if err := manager.SaveSocketCredentials(context.Background(), SocketCredentials{AccessToken: "xoxe.xapp-old", RefreshToken: "xoxe-old", ClientSecret: "secret"}); err != nil {
		t.Fatal(err)
	}
	token, err := manager.SocketToken(context.Background())
	if token != "" || err == nil || !strings.Contains(err.Error(), "keyring save failed") || strings.Contains(err.Error(), "private-replacement") {
		t.Fatal("replacement exposed before persistence")
	}
	keyring.MockInit()
}
