package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/demo"
)

type pagedBackend struct {
	*demo.Backend
	cursors []string
	repeat  bool
}

func (b *pagedBackend) Conversations(_ context.Context, cursor string) ([]core.Conversation, string, error) {
	b.cursors = append(b.cursors, cursor)
	if cursor == "" || b.repeat {
		return []core.Conversation{{ID: "C1", Name: "same", Kind: "channel"}}, "next", nil
	}
	return []core.Conversation{{ID: "C2", Name: "same", Kind: "private"}}, "", nil
}

func TestDirectoryPaginationAndAmbiguity(t *testing.T) {
	b := &pagedBackend{Backend: demo.New()}
	s := core.New(b)
	channels, _, err := s.Directory(context.Background(), false)
	if err != nil || len(channels) != 2 || strings.Join(b.cursors, ",") != ",next" {
		t.Fatalf("pagination: %v %v %v", channels, b.cursors, err)
	}
	if _, err = s.Resolve(context.Background(), "#same"); err == nil || !strings.Contains(err.Error(), "matched 2") {
		t.Fatalf("expected ambiguity: %v", err)
	}
	if c, err := s.Resolve(context.Background(), "C2"); err != nil || c.ID != "C2" {
		t.Fatal("ID resolution failed")
	}
	b.repeat = true
	if _, _, err = s.Directory(context.Background(), true); err == nil {
		t.Fatal("repeated cursor was accepted")
	}
}

func TestConversationSectionsAndOrdering(t *testing.T) {
	conversations := []core.Conversation{
		{ID: "GDM", Name: "Alice, Sam", Kind: "group-dm"},
		{ID: "C2", Name: "Beta", Kind: "channel"},
		{ID: "D1", Name: "alice", Kind: "dm"},
		{ID: "G1", Name: "alpha", Kind: "private"},
		{ID: "DSTAR", Name: "zoe", Kind: "dm", IsStarred: true},
		{ID: "CSTAR", Name: "Release", Kind: "channel", IsStarred: true},
		{ID: "C1", Name: "alpha", Kind: "channel"},
	}
	core.SortConversations(conversations)
	wantIDs := []string{"CSTAR", "DSTAR", "C1", "G1", "C2", "D1", "GDM"}
	wantSections := []string{"Starred", "Starred", "Channels", "Channels", "Channels", "Direct messages", "Group messages"}
	for i, c := range conversations {
		if c.ID != wantIDs[i] || c.Section() != wantSections[i] {
			t.Errorf("position %d: %+v in %q; want %s in %q", i, c, c.Section(), wantIDs[i], wantSections[i])
		}
	}
	if conversations[1].Kind != "dm" || conversations[1].Label() != "@zoe" || conversations[3].Kind != "private" {
		t.Fatal("grouping changed conversation types or labels")
	}
}

func TestDirectoryRetainsStarredMetadataInCache(t *testing.T) {
	s := core.New(demo.New())
	for _, refresh := range []bool{true, false} {
		conversations, _, err := s.Directory(context.Background(), refresh)
		if err != nil || len(conversations) == 0 || conversations[0].ID != "CENGINEERING" || !conversations[0].IsStarred {
			t.Fatalf("directory refresh=%v: %+v, %v", refresh, conversations, err)
		}
		conversations[0].IsStarred = false
	}
}

func TestMessageReferencesDistinguishReplyFromThread(t *testing.T) {
	link := "https://work.slack.com/archives/C0123/p1720000000000002?thread_ts=1720000000.000001&cid=C0123"
	channel, ts, err := core.ParseMessageRef(link)
	if err != nil || channel != "C0123" || ts != "1720000000.000002" {
		t.Fatalf("exact reference: %s %s %v", channel, ts, err)
	}
	_, ts, err = core.ParseThreadRef(link)
	if err != nil || ts != "1720000000.000001" {
		t.Fatalf("thread reference: %s %v", ts, err)
	}
	for _, ref := range []string{"https://slack.com.evil.org/archives/C1/p1720000000000001", "https://evilslack.com/archives/C1/p1720000000000001", "https://u:p@work.slack.com/archives/C1/p1720000000000001", "https://work.slack.com/archives/C1/pbad000001", "C1:nope", "C1:1.1"} {
		if _, _, err := core.ParseMessageRef(ref); err == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
	if _, _, err := core.ParseMessageRef("C123:1720000000.000001"); err != nil {
		t.Fatal(err)
	}
}

func TestMergeReplacesEditsDeduplicatesAndBoundsCache(t *testing.T) {
	old := []core.Message{{Timestamp: "1.000001", Text: "one"}, {Timestamp: "2.000001", Text: "two"}}
	updates := []core.Message{{Timestamp: "2.000001", Text: "edited"}, {Timestamp: "3.000001", Text: "three"}}
	merged := core.Merge(old, updates, 2)
	if len(merged) != 2 || merged[0].Text != "edited" || merged[1].Text != "three" || old[1].Text != "two" {
		t.Fatalf("bad reconciliation: %+v", merged)
	}
}

func TestOfflineDailyWorkflow(t *testing.T) {
	ctx := context.Background()
	s := core.New(demo.New())
	c, err := s.Resolve(ctx, "#general")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Messages(ctx, c.ID, "", core.PageOptions{Limit: 2})
	if err != nil || len(p.Messages) != 2 || p.Cursor == "" {
		t.Fatalf("history: %+v %v", p, err)
	}
	older, err := s.Messages(ctx, c.ID, "", core.PageOptions{Limit: 2, Cursor: p.Cursor})
	if err != nil || older.Messages[1].Timestamp >= p.Messages[0].Timestamp {
		t.Fatalf("older history: %+v %v", older, err)
	}
	dm, err := s.DM(ctx, "@alice")
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.Send(ctx, dm.ID, "", "hello from tests")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := s.Send(ctx, dm.ID, message.Timestamp, "a threaded reply")
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.Messages(ctx, dm.ID, message.Timestamp, core.PageOptions{Limit: 30})
	if err != nil || len(thread.Messages) != 2 || thread.Messages[1].Timestamp != reply.Timestamp {
		t.Fatalf("thread: %+v %v", thread, err)
	}
	result, err := s.Search(ctx, "threaded", 30, 1)
	if err != nil || result.Total != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	if err := s.React(ctx, dm.ID, reply.Timestamp, ":thumbsup:"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Send(ctx, dm.ID, "", " \n"); err == nil {
		t.Fatal("accepted blank message")
	}
	if _, err = s.Search(ctx, "", 30, 1); err == nil {
		t.Fatal("accepted empty query")
	}
}
