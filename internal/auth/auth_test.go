package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	credentials      Credentials
	loadErr, saveErr error
	saves            int
}

func (s *memoryStore) Load() (Credentials, error) { return s.credentials, s.loadErr }
func (s *memoryStore) Save(c Credentials) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.credentials = c
	s.saves++
	return nil
}
func (s *memoryStore) Delete() error {
	s.credentials = Credentials{}
	s.loadErr = ErrNotLoggedIn
	return nil
}

func TestRefreshIsSerializedAndPersistsRotatedCredentials(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "" || r.Form.Get("code_verifier") != "" || r.Form.Get("refresh_token") != "old-refresh" {
			t.Error("invalid public-client refresh request")
		}
		fmt.Fprint(w, `{"ok":true,"token_type":"user","access_token":"next-access","refresh_token":"next-refresh","expires_in":43200}`)
	}))
	defer server.Close()
	store := &memoryStore{credentials: Credentials{ClientID: "public", AccessToken: "old", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Hour), TeamID: "T1", UserID: "U1"}}
	m := &Manager{Store: store, OAuth: OAuth{Client: server.Client(), TokenURL: server.URL}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			token, err := m.Token(context.Background())
			if err != nil || token != "next-access" {
				t.Errorf("token=%q err=%v", token, err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 1 || store.saves != 1 || store.credentials.RefreshToken != "next-refresh" || store.credentials.TeamID != "T1" {
		t.Fatalf("refresh was not serialized or persisted: calls=%d store=%+v", requests.Load(), store)
	}
}

func TestRefreshDoesNotExposeTokenWhenStorageFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"token_type":"user","access_token":"new","refresh_token":"new-refresh","expires_in":43200}`)
	}))
	defer server.Close()
	m := &Manager{Store: &memoryStore{credentials: Credentials{ClientID: "id", AccessToken: "old", RefreshToken: "r", ExpiresAt: time.Now().Add(-time.Minute)}, saveErr: errors.New("locked")}, OAuth: OAuth{Client: server.Client(), TokenURL: server.URL}}
	token, err := m.Token(context.Background())
	if err == nil || token != "" {
		t.Fatalf("expected storage failure, got token %q err %v", token, err)
	}
}

func TestMissingExpiredAndUnavailableCredentials(t *testing.T) {
	for _, store := range []*memoryStore{{loadErr: ErrNotLoggedIn}, {loadErr: errors.New("keyring unavailable")}, {credentials: Credentials{AccessToken: "expired", ExpiresAt: time.Now().Add(-time.Hour)}}} {
		m := &Manager{Store: store}
		if token, err := m.Token(context.Background()); err == nil || token != "" {
			t.Fatal("expected authentication error")
		}
	}
}

func TestAccountChangeRequiresRestart(t *testing.T) {
	store := &memoryStore{credentials: Credentials{AccessToken: "one", TeamID: "T1", UserID: "U1"}}
	m := &Manager{Store: store}
	if _, err := m.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	store.credentials = Credentials{AccessToken: "two", TeamID: "T2", UserID: "U2"}
	if token, err := m.Token(context.Background()); err == nil || token != "" {
		t.Fatal("a running client silently switched accounts")
	}
}

func callbackURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return fmt.Sprintf("http://localhost:%d/callback", port)
}

func TestPKCERejectsBadStateAndValidatesIdentity(t *testing.T) {
	var challenge string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		verifier := r.Form.Get("code_verifier")
		sum := sha256.Sum256([]byte(verifier))
		if len(verifier) < 43 || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.Form.Get("client_secret") != "" {
			t.Error("PKCE verification failed")
		}
		fmt.Fprint(w, `{"ok":true,"team":{"id":"T1"},"authed_user":{"id":"U1","access_token":"user-token"}}`)
	}))
	defer server.Close()
	o := OAuth{Client: server.Client(), AuthorizeURL: server.URL + "/authorize", TokenURL: server.URL + "/token"}
	redirect := callbackURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := o.Login(ctx, "public-id", redirect, func(link string) error {
		u, _ := url.Parse(link)
		challenge = u.Query().Get("code_challenge")
		if u.Query().Get("scope") != "" || u.Query().Get("code_challenge_method") != "S256" {
			t.Error("wrong OAuth scope or challenge method")
		}
		res, err := http.Get(redirect + "?state=wrong&code=intercepted")
		if err != nil {
			t.Error(err)
			return err
		}
		_ = res.Body.Close()
		if res.StatusCode != 400 {
			t.Error("invalid state was accepted")
		}
		res, err = http.Get(redirect + "?state=" + url.QueryEscape(u.Query().Get("state")) + "&code=valid")
		if err != nil {
			return err
		}
		_ = res.Body.Close()
		return nil
	}, func(string) {}, func(_ context.Context, token string) (string, string, error) {
		if token != "user-token" {
			t.Error("wrong user token")
		}
		return "T1", "U1", nil
	})
	if err != nil || c.TeamID != "T1" || c.UserID != "U1" || c.ClientID != "public-id" {
		t.Fatalf("login: %+v %v", c, err)
	}
	if _, err = http.Get(redirect); err == nil {
		t.Fatal("callback listener remained open after login")
	}
}

func TestLoginCancellationAndRedirectValidation(t *testing.T) {
	o := DefaultOAuth()
	for _, redirect := range []string{"https://localhost:8765/callback", "http://0.0.0.0:8765/callback", "http://localhost/callback", "http://localhost:8765/callback?x=1", "http://user@localhost:8765/callback"} {
		_, err := o.Login(context.Background(), "id", redirect, nil, nil, nil)
		if err == nil {
			t.Fatalf("accepted %s", redirect)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := o.Login(ctx, "id", callbackURL(t), func(string) error { cancel(); return nil }, func(string) {}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestExchangeRejectsBotAndDoesNotLeakResponse(t *testing.T) {
	for _, body := range []string{`{"ok":true,"token_type":"bot","access_token":"secret-bot"}`, `{"ok":false,"error":"secret-token"}`, `not-json secret-token`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		o := OAuth{Client: server.Client(), TokenURL: server.URL}
		_, err := o.Exchange(context.Background(), url.Values{}, Credentials{})
		server.Close()
		if err == nil || strings.Contains(err.Error(), "secret-") {
			t.Fatalf("unsafe error: %v", err)
		}
	}
}
