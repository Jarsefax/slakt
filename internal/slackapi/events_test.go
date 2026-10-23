package slackapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/gofrs/flock"
	"github.com/gorilla/websocket"
)

func TestSocketAcknowledgesAndReconnectsWithoutExposingCredentials(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var connections atomic.Int32
	acknowledged := make(chan string, 4)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps.connections.open" {
			if r.Header.Get("Authorization") != "Bearer xapp-test-secret" {
				t.Error("missing app-level authorization")
			}
			fmt.Fprintf(w, `{"ok":true,"url":%q}`, "ws"+strings.TrimPrefix(server.URL, "http")+"/socket")
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		n := connections.Add(1)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		conn.WriteJSON(map[string]string{"type": "hello"})
		// Even unsupported events must be acknowledged.
		payload := fmt.Sprintf(`{"type":"events_api","envelope_id":"env%d","payload":{"type":"event_callback","team_id":"T1","event_id":"ev%d","event":{"type":"message","channel":"C1","user":"U1","text":"hello","ts":"2000000000.000001"}}}`, n, n)
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Error(err)
			return
		}
		var ack map[string]string
		if err := conn.ReadJSON(&ack); err != nil {
			t.Error(err)
			return
		}
		acknowledged <- ack["envelope_id"]
		if n == 1 {
			conn.WriteJSON(map[string]string{"type": "disconnect", "reason": "refresh_requested"})
			return
		}
		// Cancellation must close an otherwise idle WebSocket promptly.
		conn.ReadMessage()
	}))
	defer server.Close()
	backend := New(func(context.Context) (string, error) { return "user-secret", nil })
	backend.APIURL, backend.HTTP = server.URL+"/", server.Client()
	source := &SocketSource{Backend: backend, AppToken: "xapp-test-secret"}
	events := make(chan core.LiveEvent, 20)
	done := make(chan struct{})
	go func() { source.Events(ctx, func(e core.LiveEvent) { events <- e }); close(done) }()
	messages := 0
	deadline := time.After(6 * time.Second)
	for messages < 2 {
		select {
		case event := <-events:
			if strings.Contains(event.Detail, "secret") {
				t.Fatal("credential leaked in status")
			}
			if event.Message.Timestamp != "" {
				if event.TeamID != "T1" || event.Channel != "C1" || event.Message.Text != "hello" {
					t.Fatalf("wrong event: %+v", event)
				}
				messages++
			}
		case <-deadline:
			t.Fatal("did not reconnect and deliver messages")
		}
	}
	for _, expected := range []string{"env1", "env2"} {
		select {
		case actual := <-acknowledged:
			if actual != expected {
				t.Fatalf("ack=%q", actual)
			}
		case <-deadline:
			t.Fatal("missing acknowledgement")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("socket did not stop on cancellation")
	}
}

func TestDecodeLiveMessageVariants(t *testing.T) {
	for _, test := range []struct {
		wire  string
		check func(core.LiveEvent) bool
	}{
		{`{"type":"message","subtype":"message_changed","channel":"C1","message":{"ts":"1.000001","text":"edited"}}`, func(e core.LiveEvent) bool { return e.Changed && e.Message.Text == "edited" }},
		{`{"type":"message","subtype":"message_deleted","channel":"C1","deleted_ts":"1.000001"}`, func(e core.LiveEvent) bool { return e.Deleted == "1.000001" }},
		{`{"type":"message","subtype":"thread_broadcast","channel":"C1","ts":"2.000001","thread_ts":"1.000001"}`, func(e core.LiveEvent) bool { return e.Broadcast && e.Message.ThreadTS == "1.000001" }},
		{`{"type":"reaction_added","item":{"channel":"C1"}}`, func(e core.LiveEvent) bool { return e.Refresh }},
	} {
		e, ok := decodeLiveEvent(json.RawMessage(test.wire))
		if !ok || !test.check(e) {
			t.Fatalf("decode %s: %+v, %v", test.wire, e, ok)
		}
	}
}

func TestSocketErrorsDoNotPrintResponseBodies(t *testing.T) {
	backend := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "xapp-secret and private response", 500) })
	err := (&SocketSource{Backend: backend, AppToken: "xapp-secret"}).Check(context.Background())
	if err == nil || strings.Contains(err.Error(), "xapp-secret") || strings.Contains(err.Error(), "private response") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSocketLockFallsBackWithoutOpeningAnotherConnection(t *testing.T) {
	path := t.TempDir() + "/socket.lock"
	lock := flock.New(path)
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	var requests atomic.Int32
	backend := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); t.Error("opened a second connection") })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	(&SocketSource{Backend: backend, AppToken: "xapp-test", LockPath: path}).Events(ctx, func(event core.LiveEvent) {
		if event.State != "offline" || !strings.Contains(event.Detail, "polling fallback") {
			t.Fatalf("wrong fallback: %+v", event)
		}
		cancel()
	})
	if requests.Load() != 0 {
		t.Fatal("lock did not prevent duplicate connection")
	}
}

func TestSocketRefreshesWhileConnectedAndReconnectUsesCurrentToken(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var apiCalls, tokenCalls atomic.Int32
	refreshed := make(chan struct{})
	var refreshedOnce sync.Once
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/apps.connections.open" {
			n := apiCalls.Add(1)
			expected := "Bearer xoxe.xapp-initial"
			if n > 1 {
				expected = "Bearer xoxe.xapp-refreshed"
			}
			if r.Header.Get("Authorization") != expected {
				t.Error("reconnect used a stale access token")
			}
			fmt.Fprintf(w, `{"ok":true,"url":%q}`, "ws"+strings.TrimPrefix(server.URL, "http")+"/socket")
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.WriteJSON(map[string]string{"type": "hello"})
		if apiCalls.Load() == 1 {
			select {
			case <-refreshed:
			case <-ctx.Done():
				return
			}
			conn.WriteJSON(map[string]string{"type": "disconnect", "reason": "refresh_requested"})
		} else {
			conn.ReadMessage()
		}
	}))
	defer server.Close()
	backend := New(func(context.Context) (string, error) { return "user", nil })
	backend.APIURL, backend.HTTP = server.URL+"/", server.Client()
	source := &SocketSource{Backend: backend, renewInterval: 10 * time.Millisecond, Token: func(context.Context) (string, error) {
		if tokenCalls.Add(1) == 1 {
			return "xoxe.xapp-initial", nil
		}
		refreshedOnce.Do(func() { close(refreshed) })
		return "xoxe.xapp-refreshed", nil
	}}
	connected := 0
	source.Events(ctx, func(event core.LiveEvent) {
		if event.State == "connected" {
			connected++
			if connected == 2 {
				cancel()
			}
		}
	})
	if connected != 2 || tokenCalls.Load() < 3 || apiCalls.Load() != 2 {
		t.Fatal("did not refresh on an open connection and reconnect with the replacement")
	}
}
