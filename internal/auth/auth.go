// Package auth implements desktop OAuth and stores credentials only in the OS keyring.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"
)

const RedirectURL = "http://localhost:8765/callback"

// User scopes correspond to the methods used by the first release. No bot scopes.
var Scopes = []string{"channels:read", "channels:history", "groups:read", "groups:history", "im:read", "im:history", "im:write", "mpim:read", "mpim:history", "users:read", "chat:write", "search:read", "reactions:write"}

var ErrNotLoggedIn = errors.New("not logged in; run slkt auth login --client-id YOUR_CLIENT_ID")

type Credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	ClientID     string    `json:"client_id"`
	TeamID       string    `json:"team_id"`
	UserID       string    `json:"user_id"`
}

type Store interface {
	Load() (Credentials, error)
	Save(Credentials) error
	Delete() error
}

type KeyringStore struct{}

func (KeyringStore) Load() (Credentials, error) {
	s, err := keyring.Get("slakt", "default")
	if errors.Is(err, keyring.ErrNotFound) {
		return Credentials{}, ErrNotLoggedIn
	}
	if err != nil {
		return Credentials{}, errors.New("cannot read OS keyring; unlock your credential service and try again")
	}
	var c Credentials
	if json.Unmarshal([]byte(s), &c) != nil || c.AccessToken == "" {
		return c, errors.New("invalid slakt keyring entry; run slkt auth login again")
	}
	return c, nil
}

func (KeyringStore) Save(c Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if keyring.Set("slakt", "default", string(b)) != nil {
		return errors.New("cannot save credentials to OS keyring; unlock your credential service and run login again")
	}
	return nil
}

func (KeyringStore) Delete() error {
	err := keyring.Delete("slakt", "default")
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return errors.New("cannot remove credentials from OS keyring; unlock your credential service and try again")
	}
	return nil
}

// Manager serializes refreshes across CLI and TUI processes. No tokens go in the lock file.
type Manager struct {
	Store    Store
	OAuth    OAuth
	LockPath string
	mu       sync.Mutex
	identity string
}

func NewManager() (*Manager, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &Manager{Store: KeyringStore{}, OAuth: DefaultOAuth(), LockPath: filepath.Join(dir, "slakt", "auth.lock")}, nil
}

func (m *Manager) locked(ctx context.Context, fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.LockPath == "" { // In-memory stores in tests need no filesystem lock.
		return fn()
	}
	if err := os.MkdirAll(filepath.Dir(m.LockPath), 0700); err != nil {
		return fmt.Errorf("create authentication lock directory: %w", err)
	}
	l := flock.New(m.LockPath, flock.SetPermissions(0600))
	ok, err := l.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock credentials: %w", err)
	}
	if !ok {
		return errors.New("authentication is busy; try again")
	}
	defer l.Close()
	return fn()
}

func (m *Manager) Token(ctx context.Context) (string, error) {
	var token string
	err := m.locked(ctx, func() error {
		c, err := m.Store.Load()
		if err != nil {
			return err
		}
		identity := c.TeamID + ":" + c.UserID
		if m.identity != "" && m.identity != identity {
			return errors.New("the active Slack account changed; restart slkt before continuing")
		}
		m.identity = identity
		if !c.ExpiresAt.IsZero() && time.Until(c.ExpiresAt) < time.Minute {
			if c.RefreshToken == "" || c.ClientID == "" {
				return errors.New("authorization expired; run slkt auth login again")
			}
			next, err := m.OAuth.Exchange(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {c.ClientID}}, c)
			if err != nil {
				return fmt.Errorf("refresh authorization: %w; run slkt auth login if authorization expired", err)
			}
			// Persist rotated refresh tokens before exposing the new access token.
			if err := m.Store.Save(next); err != nil {
				return err
			}
			c = next
		}
		token = c.AccessToken
		return nil
	})
	return token, err
}

func (m *Manager) Save(ctx context.Context, c Credentials) error {
	return m.locked(ctx, func() error { return m.Store.Save(c) })
}

func (m *Manager) Logout(ctx context.Context) error {
	return m.locked(ctx, m.Store.Delete)
}

type OAuth struct {
	Client       *http.Client
	AuthorizeURL string
	TokenURL     string
}

func DefaultOAuth() OAuth {
	return OAuth{Client: &http.Client{Timeout: 30 * time.Second}, AuthorizeURL: "https://slack.com/oauth/v2/authorize", TokenURL: "https://slack.com/api/oauth.v2.access"}
}

func (o OAuth) Exchange(ctx context.Context, values url.Values, previous Credentials) (Credentials, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return Credentials{}, errors.New("invalid OAuth endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.Client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Credentials{}, ctx.Err()
		}
		return Credentials{}, errors.New("OAuth network request failed; check connectivity and authorize again")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Credentials{}, fmt.Errorf("OAuth returned HTTP %d; try login again", resp.StatusCode)
	}
	var r struct {
		OK           bool   `json:"ok"`
		Error        string `json:"error"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Team         struct {
			ID string `json:"id"`
		} `json:"team"`
		User struct {
			ID           string `json:"id"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiresIn    int64  `json:"expires_in"`
		} `json:"authed_user"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r) != nil {
		return Credentials{}, errors.New("invalid OAuth response")
	}
	if !r.OK {
		// Do not echo arbitrary response text or credentials from an error response.
		return Credentials{}, errors.New("Slack rejected authorization; check app configuration or run login again")
	}
	c := previous
	expires := r.ExpiresIn
	if r.User.AccessToken != "" {
		c.AccessToken, c.RefreshToken, expires = r.User.AccessToken, r.User.RefreshToken, r.User.ExpiresIn
	} else if r.TokenType == "user" && r.AccessToken != "" {
		c.AccessToken, c.RefreshToken = r.AccessToken, r.RefreshToken
	} else {
		return Credentials{}, errors.New("Slack did not return a user token; configure user scopes and PKCE")
	}
	if r.User.ID != "" {
		c.UserID = r.User.ID
	}
	if r.Team.ID != "" {
		c.TeamID = r.Team.ID
	}
	c.ExpiresAt = time.Time{}
	if expires > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(expires) * time.Second)
	}
	if c.RefreshToken != "" && expires <= 0 {
		return Credentials{}, errors.New("Slack returned a rotating token without its expiry")
	}
	return c, nil
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Login owns a short-lived IPv4 loopback listener. Browser and CLI must be on the same machine.
// Validate checks auth.test before credentials replace the existing account.
func (o OAuth) Login(ctx context.Context, clientID, redirect string, open func(string) error, announce func(string), validate func(context.Context, string) (string, string, error)) (Credentials, error) {
	if strings.TrimSpace(clientID) == "" {
		return Credentials{}, errors.New("set --client-id or SLKT_CLIENT_ID to your Slack app's public client ID")
	}
	u, err := url.Parse(redirect)
	if err != nil || u.Scheme != "http" || u.Hostname() != "localhost" || u.Port() == "" || u.Path == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return Credentials{}, errors.New("redirect must be http://localhost:PORT/PATH and match your Slack app registration")
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", u.Port()))
	if err != nil {
		return Credentials{}, fmt.Errorf("listen for Slack callback: %w", err)
	}
	defer listener.Close()
	verifier, err := randomSecret()
	if err != nil {
		return Credentials{}, err
	}
	state, err := randomSecret()
	if err != nil {
		return Credentials{}, err
	}
	hash := sha256.Sum256([]byte(verifier))
	query := url.Values{"client_id": {clientID}, "user_scope": {strings.Join(Scopes, ",")}, "state": {state}, "redirect_uri": {redirect}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "code_challenge_method": {"S256"}}
	link := o.AuthorizeURL + "?" + query.Encode()
	type result struct {
		code string
		err  error
	}
	resultCh := make(chan result, 1)
	var once sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != u.Path || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.Host != u.Host {
			http.Error(w, "Invalid callback host", http.StatusBadRequest)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state. Return to the login page.", http.StatusBadRequest)
			return
		}
		res := result{code: r.URL.Query().Get("code")}
		if r.URL.Query().Get("error") != "" || res.code == "" {
			res.err = errors.New("Slack authorization was denied or returned no code")
		}
		once.Do(func() { resultCh <- res })
		fmt.Fprintln(w, "Authorization received. Return to slkt to check the result.")
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second}
	defer server.Close()
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			select {
			case resultCh <- result{err: errors.New("OAuth callback server stopped")}:
			default:
			}
		}
	}()
	announce(link)
	_ = open(link) // The printed link also works when automatic browser launch is unavailable.
	select {
	case <-ctx.Done():
		return Credentials{}, ctx.Err()
	case res := <-resultCh:
		if res.err != nil {
			return Credentials{}, res.err
		}
		c, err := o.Exchange(ctx, url.Values{"client_id": {clientID}, "code": {res.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}, Credentials{ClientID: clientID})
		if err != nil {
			return Credentials{}, err
		}
		team, user, err := validate(ctx, c.AccessToken)
		if err != nil {
			return Credentials{}, err
		}
		if c.TeamID != "" && c.TeamID != team || c.UserID != "" && c.UserID != user {
			return Credentials{}, errors.New("OAuth identity did not match auth.test")
		}
		c.TeamID, c.UserID = team, user
		return c, nil
	}
}
