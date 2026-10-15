package slackapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
)

func fakeBackend(t *testing.T, handler http.HandlerFunc) *Backend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	b := New(func(context.Context) (string, error) { return "secret-test-token", nil })
	b.HTTP = server.Client()
	b.APIURL = server.URL + "/"
	return b
}

func TestConversationsPreserveGroupingMetadataAndPagination(t *testing.T) {
	var calls int
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		if r.URL.Path != "/users.conversations" || r.Header.Get("Authorization") != "Bearer secret-test-token" || r.Form.Get("limit") != "200" || r.Form.Get("exclude_archived") != "true" || r.Form.Get("types") != "public_channel,private_channel,im,mpim" {
			t.Errorf("wrong conversation request: %s %v", r.URL.Path, r.Form)
		}
		if calls == 1 {
			if r.Form.Get("cursor") != "" {
				t.Error("initial request has a cursor")
			}
			fmt.Fprint(w, `{"ok":true,"channels":[{"id":"CSTAR","name":"release","is_channel":true,"is_starred":true},{"id":"GPRIVATE","name":"private","is_private":true,"is_starred":false},{"id":"D1","is_im":true,"is_private":true,"user":"UALICE"},{"id":"GDM","name":"alice, sam","is_mpim":true,"is_private":true}],"response_metadata":{"next_cursor":"page-two"}}`)
			return
		}
		if r.Form.Get("cursor") != "page-two" {
			t.Error("pagination cursor was lost")
		}
		fmt.Fprint(w, `{"ok":true,"channels":[{"id":"DSTAR","is_im":true,"user":"USAM","is_starred":true}],"response_metadata":{"next_cursor":""}}`)
	})
	items, next, err := b.Conversations(context.Background(), "")
	if err != nil || next != "page-two" || len(items) != 4 {
		t.Fatalf("first page: %+v %q %v", items, next, err)
	}
	for i, kind := range []string{"channel", "private", "dm", "group-dm"} {
		if items[i].Kind != kind || items[i].IsStarred != (i == 0) {
			t.Errorf("conversation %d: %+v", i, items[i])
		}
	}
	if items[2].UserID != "UALICE" {
		t.Fatal("DM user was lost")
	}
	items, next, err = b.Conversations(context.Background(), next)
	if err != nil || next != "" || len(items) != 1 || !items[0].IsStarred || items[0].Kind != "dm" || calls != 2 {
		t.Fatalf("second page: %+v %q %v calls=%d", items, next, err, calls)
	}
}

func TestConversationReadErrorsRetainSafeRetryAndCancellation(t *testing.T) {
	t.Run("rate limit", func(t *testing.T) {
		var calls atomic.Int32
		b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(http.StatusTooManyRequests)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, _, err := b.Conversations(ctx, "")
		if err != context.DeadlineExceeded || calls.Load() != 1 {
			t.Fatalf("rate limit cancellation: %v calls=%d", err, calls.Load())
		}
	})
	t.Run("transient failure", func(t *testing.T) {
		var calls atomic.Int32
		b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				fmt.Fprint(w, "secret-test-token")
				return
			}
			fmt.Fprint(w, `{"ok":true,"channels":[]}`)
		})
		_, _, err := b.Conversations(context.Background(), "")
		if err != nil || calls.Load() != 2 {
			t.Fatalf("read retry: %v calls=%d", err, calls.Load())
		}
	})
	t.Run("missing scope", func(t *testing.T) {
		b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ok":false,"error":"missing_scope","needed":"secret-test-token"}`)
		})
		_, _, err := b.Conversations(context.Background(), "")
		if err == nil || !strings.Contains(err.Error(), "scopes") || strings.Contains(err.Error(), "secret-test-token") {
			t.Fatalf("scope error: %v", err)
		}
	})
}

func TestHistoryPreservesPaginationAndRequestBounds(t *testing.T) {
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.URL.Path != "/conversations.history" || r.Form.Get("channel") != "C1" || r.Form.Get("cursor") != "cursor-one" || r.Form.Get("oldest") != "1720000000.000001" {
			t.Errorf("wrong request: %s %v", r.URL.Path, r.Form)
		}
		fmt.Fprint(w, `{"ok":true,"has_more":true,"response_metadata":{"next_cursor":"cursor-two"},"messages":[{"ts":"1720000000.000002","user":"U1","text":"hello","reactions":[{"name":"wave","count":2}],"files":[{"name":"notes.txt","permalink":"https://example.com/file"}]}]}`)
	})
	p, err := b.History(context.Background(), "C1", core.PageOptions{Limit: 50, Cursor: "cursor-one", After: "1720000000.000001"})
	if err != nil || p.Cursor != "cursor-two" || !p.HasMore || len(p.Messages) != 1 || len(p.Messages[0].Reactions) != 1 || len(p.Messages[0].Attachments) != 1 {
		t.Fatalf("history: %+v %v", p, err)
	}
}

func TestRateLimitHonorsRetryAfterAndCancellation(t *testing.T) {
	var calls atomic.Int32
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "600")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := b.Identity(ctx)
	if err == nil || calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatalf("rate-limit cancellation failed: %v calls=%d", err, calls.Load())
	}
}

func TestReadRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"ok":true,"team_id":"T1","team":"test","user_id":"U1","user":"alice"}`)
	})
	start := time.Now()
	id, err := b.Identity(context.Background())
	if err != nil || id.UserID != "U1" || calls.Load() != 2 || time.Since(start) < time.Second {
		t.Fatalf("retry: %+v %v calls=%d", id, err, calls.Load())
	}
}

func TestSendIsNeverBlindlyRetried(t *testing.T) {
	for _, status := range []int{500, 429} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret-test-token and private message")
			})
			_, err := b.Send(context.Background(), "C1", "", "private message")
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "secret-test-token") || strings.Contains(err.Error(), "private message") {
				t.Fatalf("unsafe retry/error: %v calls=%d", err, calls.Load())
			}
			if status == 500 && !strings.Contains(err.Error(), "unknown") {
				t.Fatalf("ambiguous send not identified: %v", err)
			}
		})
	}
}

func TestMissingScopeIsActionableAndSafe(t *testing.T) {
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":false,"error":"missing_scope","needed":"secret-test-token"}`)
	})
	_, err := b.Search(context.Background(), "query", 30, 1)
	if err == nil || !strings.Contains(err.Error(), "scopes") || strings.Contains(err.Error(), "secret-test-token") {
		t.Fatalf("scope error: %v", err)
	}
}

func TestSlackInternalErrorLeavesWriteOutcomeUnknown(t *testing.T) {
	var calls atomic.Int32
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"ok":false,"error":"internal_error"}`)
	})
	_, err := b.Send(context.Background(), "C1", "", "hello")
	if err == nil || !strings.Contains(err.Error(), "unknown") || calls.Load() != 1 {
		t.Fatalf("write error: %v calls=%d", err, calls.Load())
	}
}

func TestSendsUseTheRequestedThread(t *testing.T) {
	b := fakeBackend(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("thread_ts") != "1720000000.000001" || r.Form.Get("text") != "hello" || r.Form.Get("channel") != "C1" {
			t.Errorf("unexpected send body: %v", r.Form)
		}
		fmt.Fprint(w, `{"ok":true,"channel":"C1","ts":"1720000000.000002"}`)
	})
	m, err := b.Send(context.Background(), "C1", "1720000000.000001", "hello")
	if err != nil || m.ThreadTS != "1720000000.000001" {
		t.Fatalf("send: %+v %v", m, err)
	}
}
