package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// SocketCredentials is stored as one keyring entry, separately from user OAuth.
// StreamID remains stable across refreshes so clients still share a local lock.
type SocketCredentials struct {
	AccessToken    string    `json:"access_token"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	ClientSecret   string    `json:"client_secret,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	StreamID       string    `json:"stream_id"`
	RefreshPending bool      `json:"refresh_pending,omitempty"`
}

func socketAccount(c Credentials) string {
	return "socket/" + c.ClientID + "/" + c.TeamID + "/" + c.UserID
}

func RotatingSocketToken(token string) bool { return strings.HasPrefix(token, "xoxe.xapp-") }
func ValidSocketToken(token string) bool {
	return len(token) <= 8192 && !strings.ContainsAny(token, " \t\r\n") &&
		(strings.HasPrefix(token, "xapp-") && len(token) > 5 || RotatingSocketToken(token) && len(token) > 10)
}
func validSocketSecret(secret string) bool {
	return secret != "" && len(secret) <= 8192 && !strings.ContainsAny(secret, " \t\r\n")
}
func validateSocket(c SocketCredentials) error {
	if !ValidSocketToken(c.AccessToken) {
		return errors.New("expected an xapp- or xoxe.xapp- app-level token with connections:write")
	}
	if RotatingSocketToken(c.AccessToken) {
		if !strings.HasPrefix(c.RefreshToken, "xoxe-") || !validSocketSecret(c.RefreshToken) || !validSocketSecret(c.ClientSecret) {
			return errors.New("rotating app-level tokens require their xoxe- refresh token and the Slack app client secret")
		}
	} else if c.RefreshToken != "" || c.ClientSecret != "" {
		return errors.New("refresh credentials require a rotating xoxe.xapp- access token")
	}
	return nil
}

var errInvalidSocket = errors.New("invalid Socket Mode keyring entry; run slkt auth socket again")

func loadSocket(account string) (SocketCredentials, error) {
	raw, err := keyring.Get("slakt", account)
	if errors.Is(err, keyring.ErrNotFound) {
		return SocketCredentials{}, nil
	}
	if err != nil {
		return SocketCredentials{}, errors.New("cannot read Socket Mode credential from OS keyring")
	}
	var c SocketCredentials
	// Preserve credentials written by 0.24.0, including their original lock ID.
	if strings.HasPrefix(raw, "xapp-") {
		c.AccessToken = raw
	} else if json.Unmarshal([]byte(raw), &c) != nil {
		return c, errInvalidSocket
	}
	if validateSocket(c) != nil {
		return c, errInvalidSocket
	}
	if c.StreamID == "" {
		hash := sha256.Sum256([]byte(c.AccessToken))
		c.StreamID = hex.EncodeToString(hash[:16])
	}
	if decoded, err := hex.DecodeString(c.StreamID); err != nil || len(decoded) != 16 {
		return c, errInvalidSocket
	}
	return c, nil
}
func saveSocket(account string, c SocketCredentials) error {
	data, err := json.Marshal(c)
	if err != nil {
		return errors.New("cannot encode Socket Mode credentials")
	}
	if keyring.Set("slakt", account, string(data)) != nil {
		return errors.New("cannot update Socket Mode credentials in OS keyring")
	}
	return nil
}

// SocketConfig reports configuration without consuming a single-use refresh
// token. Used by doctor and startup before the stream has acquired its lock.
func (m *Manager) SocketConfig(ctx context.Context) (SocketCredentials, error) {
	var socket SocketCredentials
	err := m.locked(ctx, func() error {
		c, err := m.Store.Load()
		if err != nil {
			return err
		}
		socket, err = loadSocket(socketAccount(c))
		return err
	})
	return socket, err
}

// SocketToken serializes refreshes across processes and persists the complete
// replacement pair before returning an access token. RefreshPending prevents
// replay after a crash or a response/storage failure with an unknown outcome.
func (m *Manager) SocketToken(ctx context.Context) (string, error) {
	var token string
	err := m.locked(ctx, func() error {
		c, err := m.Store.Load()
		if err != nil {
			return err
		}
		account := socketAccount(c)
		if m.socketIdentity != "" && m.socketIdentity != account {
			return errors.New("the active Slack app/account changed; restart slkt")
		}
		m.socketIdentity = account
		socket, err := loadSocket(account)
		if err != nil {
			return err
		}
		if socket.AccessToken == "" {
			return nil
		}
		if socket.RefreshPending {
			return errors.New("previous app-level refresh outcome is unknown; run slkt auth socket with a fresh token pair")
		}
		if RotatingSocketToken(socket.AccessToken) && (socket.ExpiresAt.IsZero() || time.Until(socket.ExpiresAt) <= 5*time.Minute) {
			socket.RefreshPending = true
			if err := saveSocket(account, socket); err != nil {
				return err
			}
			next, rejected, err := m.OAuth.refreshSocket(ctx, c.ClientID, socket)
			if err != nil {
				if rejected {
					socket.RefreshPending = false
					if saveSocket(account, socket) != nil {
						return errors.New("cannot persist failed app-level refresh; run slkt auth socket with a fresh token pair")
					}
				}
				return err
			}
			if saveSocket(account, next) != nil {
				return errors.New("app-level token refreshed but keyring save failed; run slkt auth socket with a fresh token pair")
			}
			socket = next
		}
		token = socket.AccessToken
		return nil
	})
	return token, err
}

func (m *Manager) SaveSocketToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return m.locked(ctx, func() error {
			c, err := m.Store.Load()
			if err != nil {
				return err
			}
			err = keyring.Delete("slakt", socketAccount(c))
			if errors.Is(err, keyring.ErrNotFound) {
				return nil
			}
			if err != nil {
				return errors.New("cannot remove Socket Mode credentials from OS keyring")
			}
			return nil
		})
	}
	return m.SaveSocketCredentials(ctx, SocketCredentials{AccessToken: token})
}
func (m *Manager) SaveSocketCredentials(ctx context.Context, socket SocketCredentials) error {
	if err := validateSocket(socket); err != nil {
		return err
	}
	return m.locked(ctx, func() error {
		c, err := m.Store.Load()
		if err != nil {
			return err
		}
		account := socketAccount(c)
		previous, err := loadSocket(account)
		if errors.Is(err, errInvalidSocket) {
			previous = SocketCredentials{}
		} else if err != nil {
			return err
		}
		socket.StreamID = previous.StreamID
		if socket.StreamID == "" {
			hash := sha256.Sum256([]byte(socket.AccessToken))
			socket.StreamID = hex.EncodeToString(hash[:16])
		}
		socket.RefreshPending = false
		return saveSocket(account, socket)
	})
}

// App-level rotation uses client credentials even though user login uses PKCE.
// Only an explicit rejection is safe to retry with the old refresh token.
func (o OAuth) refreshSocket(ctx context.Context, clientID string, previous SocketCredentials) (SocketCredentials, bool, error) {
	failure := errors.New("app-level token refresh failed; outcome may be unknown, run slkt auth socket with a fresh token pair")
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {previous.RefreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL, strings.NewReader(values.Encode()))
	if err != nil {
		return SocketCredentials{}, false, failure
	}
	req.SetBasicAuth(clientID, previous.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := o.Client
	if client == nil {
		client = DefaultOAuth().Client
	}
	response, err := client.Do(req)
	if err != nil {
		return SocketCredentials{}, false, failure
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return SocketCredentials{}, response.StatusCode == http.StatusTooManyRequests, errors.New("Slack app-level refresh request failed; check connectivity and app credentials")
	}
	var result struct {
		OK           bool   `json:"ok"`
		TokenType    string `json:"token_type"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result) != nil {
		return SocketCredentials{}, false, failure
	}
	if !result.OK {
		return SocketCredentials{}, true, errors.New("Slack rejected app-level token refresh; check the refresh token, client secret, and app, then run slkt auth socket")
	}
	next := previous
	next.AccessToken, next.RefreshToken = result.AccessToken, result.RefreshToken
	next.RefreshPending = false
	if result.TokenType != "app_level" || !RotatingSocketToken(next.AccessToken) || validateSocket(next) != nil || result.ExpiresIn <= 0 || result.ExpiresIn > 86400 {
		return SocketCredentials{}, false, failure
	}
	next.ExpiresAt = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return next, false, nil
}
