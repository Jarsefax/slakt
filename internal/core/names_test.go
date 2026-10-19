package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
)

func TestPreferredNameFallbacks(t *testing.T) {
	for _, test := range []struct {
		name string
		user core.User
		want string
	}{
		{"display", core.User{ID: "U1", Name: "alice", RealName: "Alice Example", DisplayName: " Alice "}, "Alice"},
		{"real", core.User{ID: "U1", Name: "alice", RealName: "Alice Example", DisplayName: " \t"}, "Alice Example"},
		{"username", core.User{ID: "U1", Name: "alice", RealName: " "}, "alice"},
		{"id", core.User{ID: "U1"}, "U1"},
		{"empty", core.User{}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.user.PreferredName(); got != test.want {
				t.Fatalf("name = %q, want %q", got, test.want)
			}
		})
	}
}

type namesBackend struct {
	*demo.Backend
	users             []core.User
	messages          []core.Message
	usersErr          error
	userCalls         int
	conversationCalls int
}

func (b *namesBackend) Users(_ context.Context, cursor string) ([]core.User, string, error) {
	b.userCalls++
	if b.usersErr != nil {
		return nil, "", b.usersErr
	}
	if cursor == "" {
		return b.users[:1], "page-two", nil
	}
	return b.users[1:], "", nil
}

func (b *namesBackend) Conversations(ctx context.Context, cursor string) ([]core.Conversation, string, error) {
	b.conversationCalls++
	return b.Backend.Conversations(ctx, cursor)
}

func (b *namesBackend) History(context.Context, string, core.PageOptions) (core.MessagePage, error) {
	return core.MessagePage{Messages: append([]core.Message(nil), b.messages...)}, nil
}

func (b *namesBackend) Replies(ctx context.Context, channel, _ string, opts core.PageOptions) (core.MessagePage, error) {
	return b.History(ctx, channel, opts)
}

func (b *namesBackend) Search(context.Context, string, int, int) (core.SearchResult, error) {
	return core.SearchResult{Messages: append([]core.Message(nil), b.messages...)}, nil
}

func TestAuthorsResolveOnColdHistoryThreadsAndSearch(t *testing.T) {
	for _, path := range []string{"history", "thread", "search"} {
		t.Run(path, func(t *testing.T) {
			b := &namesBackend{
				Backend: demo.New(),
				users: []core.User{
					{ID: "UDISPLAY", Name: "alice", RealName: "Alice Example", DisplayName: "Alice"},
					{ID: "UREAL", Name: "sam", RealName: "Sam Example"},
					{ID: "UUSERNAME", Name: "pat"},
					{ID: "UID"},
					{ID: "UFORMER", Name: "former", DisplayName: "Former Colleague", Deleted: true},
					{ID: "UBOT", DisplayName: "Bot Account", IsBot: true},
				},
				messages: []core.Message{
					{Timestamp: "1.000001", UserID: "UDISPLAY", User: "alice"},
					{Timestamp: "2.000001", UserID: "UREAL"},
					{Timestamp: "3.000001", UserID: "UUSERNAME"},
					{Timestamp: "4.000001", UserID: "UID"},
					{Timestamp: "5.000001", UserID: "UFORMER"},
					{Timestamp: "6.000001", UserID: "UUNKNOWN"},
					{Timestamp: "7.000001", UserID: "UBOT", User: "Release App"},
					{Timestamp: "8.000001", User: "Webhook App"},
				},
			}
			s := core.New(b)
			for range 2 {
				var messages []core.Message
				var err error
				if path == "search" {
					var result core.SearchResult
					result, err = s.Search(context.Background(), "hello", 30, 1)
					messages = result.Messages
				} else {
					thread := ""
					if path == "thread" {
						thread = "1.000001"
					}
					var page core.MessagePage
					page, err = s.Messages(context.Background(), "C1", thread, core.PageOptions{Limit: 30})
					messages = page.Messages
				}
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, m := range messages {
					got = append(got, m.User)
				}
				want := []string{"Alice", "Sam Example", "pat", "UID", "Former Colleague", "UUNKNOWN", "Release App", "Webhook App"}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("authors = %q, want %q", got, want)
				}
			}
			if b.userCalls != 2 || b.conversationCalls != 0 {
				t.Fatalf("directory calls: users=%d, conversations=%d", b.userCalls, b.conversationCalls)
			}
			names := s.UserNames()
			if names["UFORMER"] != "Former Colleague" || names["UREAL"] != "Sam Example" {
				t.Fatalf("mention names missing: %v", names)
			}
			names["UFORMER"] = "changed"
			if s.UserNames()["UFORMER"] != "Former Colleague" {
				t.Fatal("name snapshot changed the cache")
			}
		})
	}
}

func TestAuthorLookupFailureDoesNotHideMessages(t *testing.T) {
	b := &namesBackend{
		Backend: demo.New(), usersErr: errors.New("missing_scope"),
		messages: []core.Message{{Timestamp: "1.000001", UserID: "UUNKNOWN"}, {Timestamp: "2.000001", User: "Known App"}},
	}
	s := core.New(b)
	page, err := s.Messages(context.Background(), "C1", "", core.PageOptions{Limit: 30})
	if err != nil || len(page.Messages) != 2 || page.Messages[0].User != "UUNKNOWN" || page.Messages[1].User != "Known App" {
		t.Fatalf("history fallback: %+v, %v", page, err)
	}
	result, err := s.Search(context.Background(), "hello", 30, 1)
	if err != nil || len(result.Messages) != 2 || result.Messages[0].User != "UUNKNOWN" || result.Messages[1].User != "Known App" {
		t.Fatalf("search fallback: %+v, %v", result, err)
	}
}

func TestDeactivatedAuthorsDoNotMakeDMLookupAmbiguous(t *testing.T) {
	b := &namesBackend{
		Backend: demo.New(),
		users: []core.User{
			{ID: "UFORMER", Name: "former", DisplayName: "Alice", Deleted: true},
			{ID: "UALICE", Name: "alice", DisplayName: "Alice"},
		},
	}
	s := core.New(b)
	conversation, err := s.DM(context.Background(), "Alice")
	if err != nil || conversation.UserID != "UALICE" {
		t.Fatalf("active DM lookup: %+v, %v", conversation, err)
	}
	if _, err := s.DM(context.Background(), "former"); err == nil {
		t.Fatal("deactivated author was resolved as a new DM recipient")
	}
	if s.UserNames()["UFORMER"] != "Alice" {
		t.Fatal("excluding a DM recipient removed their author name")
	}
}
