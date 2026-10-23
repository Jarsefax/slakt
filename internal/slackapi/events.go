package slackapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/gofrs/flock"
	"github.com/gorilla/websocket"
	"github.com/slack-go/slack"
)

// SocketSource owns a single app's stream. Slack distributes events between
// connections, so a local lock prevents two clients silently splitting a feed.
// Independent machines/users must use separate apps or an external relay.
type SocketSource struct {
	Backend            *Backend
	AppToken, LockPath string
	Token              TokenSource
	renewInterval      time.Duration // Tests can exercise refresh without a minute's wait.
	Dialer             *websocket.Dialer
}

func (s *SocketSource) socketURL(ctx context.Context) (string, error) {
	token := s.AppToken
	if s.Token != nil {
		var err error
		token, err = s.Token(ctx)
		if err != nil {
			return "", err
		}
	}
	if token == "" {
		return "", errors.New("Socket Mode credential removed; polling fallback")
	}
	api := slack.New("", slack.OptionAppLevelToken(token), slack.OptionHTTPClient(s.Backend.HTTP), slack.OptionAPIURL(s.Backend.APIURL))
	_, url, err := api.StartSocketModeContext(ctx)
	if err != nil {
		return "", errors.New("cannot open Socket Mode; check app token, connections:write, Socket Mode settings, and connectivity")
	}
	return url, nil
}

// Check validates the credential without retaining or logging the temporary URL.
func (s *SocketSource) Check(ctx context.Context) error {
	_, err := s.socketURL(ctx)
	return err
}

func (s *SocketSource) Events(ctx context.Context, emit func(core.LiveEvent)) {
	if s.LockPath != "" {
		lock := flock.New(s.LockPath, flock.SetPermissions(0600))
		defer lock.Close()
		for {
			ok, err := lock.TryLock()
			if err == nil && ok {
				break
			}
			emit(core.LiveEvent{State: "offline", Detail: "Socket Mode is busy or its local lock is unavailable; polling fallback"})
			if !socketWait(ctx, 30*time.Second) {
				return
			}
		}
	}
	delay := time.Second
	for ctx.Err() == nil {
		emit(core.LiveEvent{State: "connecting", Detail: "Connecting to Slack; polling fallback"})
		connected, err := s.connection(ctx, emit)
		if ctx.Err() != nil {
			return
		}
		detail := "Socket Mode disconnected; polling fallback"
		if err != nil {
			detail = err.Error()
		}
		emit(core.LiveEvent{State: "offline", Detail: detail})
		if connected {
			delay = time.Second
		}
		if !socketWait(ctx, delay) {
			return
		}
		delay = min(delay*2, time.Minute)
	}
}

func socketWait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type socketEnvelope struct {
	Type       string `json:"type"`
	EnvelopeID string `json:"envelope_id"`
	Payload    struct {
		TeamID  string          `json:"team_id"`
		EventID string          `json:"event_id"`
		Type    string          `json:"type"`
		Event   json.RawMessage `json:"event"`
	} `json:"payload"`
}

func (s *SocketSource) connection(ctx context.Context, emit func(core.LiveEvent)) (bool, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	url, err := s.socketURL(requestCtx)
	cancel()
	if err != nil {
		return false, err
	}
	dialer := s.Dialer
	if dialer == nil {
		dialer = &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 20 * time.Second}
	}
	conn, response, err := dialer.DialContext(ctx, url, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return false, errors.New("Socket Mode connection failed; polling fallback")
	}
	defer conn.Close()
	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	renewErrors := make(chan error, 1)
	if s.Token != nil {
		go func() {
			interval := s.renewInterval
			if interval <= 0 {
				interval = time.Minute
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-renewCtx.Done():
					return
				case <-ticker.C:
					refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
					token, err := s.Token(refreshCtx)
					cancel()
					if err == nil && token == "" {
						err = errors.New("Socket Mode credential removed; polling fallback")
					}
					if err != nil {
						renewErrors <- err
						conn.Close()
						return
					}
				}
			}
		}()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	conn.SetReadLimit(4 << 20)
	conn.SetReadDeadline(time.Now().Add(90 * time.Second))
	conn.SetPingHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(5*time.Second))
	})
	connected := false
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			select {
			case refreshErr := <-renewErrors:
				return connected, refreshErr
			default:
			}
			return connected, errors.New("Socket Mode disconnected; polling fallback")
		}
		conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		var envelope socketEnvelope
		if json.Unmarshal(data, &envelope) != nil {
			return connected, errors.New("invalid Socket Mode payload; reconnecting with polling fallback")
		}
		if envelope.EnvelopeID != "" {
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if conn.WriteJSON(map[string]string{"envelope_id": envelope.EnvelopeID}) != nil {
				return connected, errors.New("Socket Mode acknowledgement failed; polling fallback")
			}
		}
		switch envelope.Type {
		case "hello":
			connected = true
			emit(core.LiveEvent{State: "connected", Detail: "Socket Mode connected"})
		case "disconnect":
			return connected, nil
		case "events_api":
			if envelope.Payload.Type == "app_rate_limited" {
				return connected, errors.New("Slack event delivery rate limited; polling fallback")
			}
			event, ok := decodeLiveEvent(envelope.Payload.Event)
			if ok {
				event.TeamID, event.ID = envelope.Payload.TeamID, envelope.Payload.EventID
				emit(event)
			}
		}
	}
}

func decodeLiveEvent(data json.RawMessage) (core.LiveEvent, bool) {
	var wire struct {
		Type, Subtype, Channel string
		ChannelType            string                   `json:"channel_type"`
		DeletedTS              string                   `json:"deleted_ts"`
		Message                *slack.Message           `json:"message"`
		Item                   struct{ Channel string } `json:"item"`
	}
	if json.Unmarshal(data, &wire) != nil {
		return core.LiveEvent{}, false
	}
	event := core.LiveEvent{Channel: wire.Channel, ChannelType: wire.ChannelType}
	switch wire.Type {
	case "message":
		switch wire.Subtype {
		case "message_deleted":
			event.Deleted = wire.DeletedTS
		case "message_changed":
			if wire.Message == nil {
				return event, false
			}
			event.Message = message(wire.Channel, *wire.Message)
			event.Changed = true
			event.Broadcast = wire.Message.SubType == "thread_broadcast"
		case "message_replied":
			event.Refresh = true
		default:
			var msg slack.Message
			if json.Unmarshal(data, &msg) != nil {
				return event, false
			}
			event.Message = message(wire.Channel, msg)
			event.Broadcast = wire.Subtype == "thread_broadcast"
		}
	case "reaction_added", "reaction_removed":
		event.Channel, event.Refresh = wire.Item.Channel, true
	default:
		return event, false
	}
	return event, event.Channel != "" && (event.Message.Timestamp != "" || event.Deleted != "" || event.Refresh)
}
