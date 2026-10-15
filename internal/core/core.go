// Package core is the shared application layer used by the CLI and TUI.
package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Identity struct {
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
	UserID string `json:"user_id"`
	User   string `json:"user"`
}

type Conversation struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	UserID string `json:"user_id,omitempty"`
}

func (c Conversation) Label() string {
	if c.Kind == "dm" {
		return "@" + c.Name
	}
	if c.Kind == "group-dm" {
		return c.Name
	}
	return "#" + c.Name
}

type User struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

type Reaction struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type Message struct {
	ChannelID   string     `json:"channel_id"`
	Timestamp   string     `json:"ts"`
	ThreadTS    string     `json:"thread_ts,omitempty"`
	UserID      string     `json:"user_id,omitempty"`
	User        string     `json:"user,omitempty"`
	Text        string     `json:"text"`
	ReplyCount  int        `json:"reply_count,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
	Attachments []string   `json:"attachments,omitempty"`
	Permalink   string     `json:"permalink,omitempty"`
}

type PageOptions struct {
	Limit  int
	Cursor string
	Before string
	After  string
}

type MessagePage struct {
	Messages []Message `json:"messages"`
	Cursor   string    `json:"next_cursor,omitempty"`
	HasMore  bool      `json:"has_more"`
}

type SearchResult struct {
	Messages []Message `json:"messages"`
	Total    int       `json:"total"`
	Page     int       `json:"page"`
	Pages    int       `json:"pages"`
}

type Backend interface {
	Identity(context.Context) (Identity, error)
	Conversations(context.Context, string) ([]Conversation, string, error)
	Users(context.Context, string) ([]User, string, error)
	History(context.Context, string, PageOptions) (MessagePage, error)
	Replies(context.Context, string, string, PageOptions) (MessagePage, error)
	Search(context.Context, string, int, int) (SearchResult, error)
	OpenDM(context.Context, string) (Conversation, error)
	Send(context.Context, string, string, string) (Message, error)
	React(context.Context, string, string, string) error
	Permalink(context.Context, string, string) (string, error)
}

type Service struct {
	Backend       Backend
	mu            sync.Mutex
	users         []User
	conversations []Conversation
	loaded        time.Time
}

func New(b Backend) *Service { return &Service{Backend: b} }

// collect walks cursor pagination and rejects loops instead of hanging indefinitely.
func collect[T any](ctx context.Context, fetch func(context.Context, string) ([]T, string, error)) ([]T, error) {
	items := make([]T, 0)
	cursor := ""
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, next, err := fetch(ctx, cursor)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
		if next == "" {
			return items, nil
		}
		if seen[next] || len(seen) >= 1000 {
			return nil, errors.New("Slack pagination did not terminate")
		}
		seen[next] = true
		cursor = next
	}
}

func (s *Service) Directory(ctx context.Context, refresh bool) ([]Conversation, []User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !refresh && !s.loaded.IsZero() && time.Since(s.loaded) < 5*time.Minute {
		return append([]Conversation(nil), s.conversations...), append([]User(nil), s.users...), nil
	}
	conversations, err := collect(ctx, s.Backend.Conversations)
	if err != nil {
		return nil, nil, err
	}
	users, err := collect(ctx, s.Backend.Users)
	if err != nil {
		return nil, nil, err
	}
	names := map[string]string{}
	for _, user := range users {
		names[user.ID] = user.Name
	}
	for i := range conversations {
		c := &conversations[i]
		if c.Kind == "dm" {
			c.Name = names[c.UserID]
			if c.Name == "" {
				c.Name = c.UserID
			}
		}
		if c.Name == "" {
			c.Name = c.ID
		}
	}
	sort.Slice(conversations, func(i, j int) bool {
		if conversations[i].Kind != conversations[j].Kind {
			return conversations[i].Kind < conversations[j].Kind
		}
		return conversations[i].Name < conversations[j].Name
	})
	s.conversations, s.users, s.loaded = conversations, users, time.Now()
	return append([]Conversation(nil), conversations...), append([]User(nil), users...), nil
}

var channelID = regexp.MustCompile(`^[CGD][A-Z0-9]+$`)
var userID = regexp.MustCompile(`^[UW][A-Z0-9]+$`)
var timestamp = regexp.MustCompile(`^[0-9]+\.[0-9]{6}$`)

func (s *Service) Resolve(ctx context.Context, ref string) (Conversation, error) {
	if channelID.MatchString(ref) {
		return Conversation{ID: ref, Name: ref}, nil
	}
	if strings.HasPrefix(ref, "@") {
		return s.DM(ctx, strings.TrimPrefix(ref, "@"))
	}
	conversations, _, err := s.Directory(ctx, false)
	if err != nil {
		return Conversation{}, err
	}
	name := strings.TrimPrefix(ref, "#")
	var matches []Conversation
	for _, c := range conversations {
		if c.Kind != "dm" && strings.EqualFold(c.Name, name) {
			matches = append(matches, c)
		}
	}
	if len(matches) != 1 {
		return Conversation{}, fmt.Errorf("conversation %q matched %d conversations; use a Slack conversation ID", ref, len(matches))
	}
	return matches[0], nil
}

func (s *Service) DM(ctx context.Context, ref string) (Conversation, error) {
	ref = strings.TrimPrefix(ref, "@")
	id := ref
	if !userID.MatchString(ref) {
		_, users, err := s.Directory(ctx, false)
		if err != nil {
			return Conversation{}, err
		}
		var matches []User
		for _, u := range users {
			if strings.EqualFold(u.Name, ref) || u.DisplayName != "" && strings.EqualFold(u.DisplayName, ref) {
				matches = append(matches, u)
			}
		}
		if len(matches) != 1 {
			return Conversation{}, fmt.Errorf("person %q matched %d users; use their Slack user ID", ref, len(matches))
		}
		id = matches[0].ID
	}
	c, err := s.Backend.OpenDM(ctx, id)
	if c.Name == "" {
		c.Name = ref
	}
	c.Kind, c.UserID = "dm", id
	return c, err
}

func (s *Service) Messages(ctx context.Context, channel, thread string, opts PageOptions) (MessagePage, error) {
	if opts.Limit < 1 || opts.Limit > 200 {
		return MessagePage{}, errors.New("limit must be between 1 and 200")
	}
	var page MessagePage
	var err error
	if thread == "" {
		page, err = s.Backend.History(ctx, channel, opts)
	} else {
		page, err = s.Backend.Replies(ctx, channel, thread, opts)
	}
	if err != nil {
		return MessagePage{}, err
	}
	s.hydrate(page.Messages)
	SortMessages(page.Messages)
	if page.Messages == nil {
		page.Messages = []Message{}
	}
	return page, nil
}

func (s *Service) Search(ctx context.Context, query string, limit, page int) (SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return SearchResult{}, errors.New("search query is empty")
	}
	if limit < 1 || limit > 100 || page < 1 {
		return SearchResult{}, errors.New("search limit must be 1–100 and page must be positive")
	}
	r, err := s.Backend.Search(ctx, query, limit, page)
	s.hydrate(r.Messages)
	if r.Messages == nil {
		r.Messages = []Message{}
	}
	return r, err
}

func (s *Service) hydrate(messages []Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := map[string]string{}
	for _, u := range s.users {
		names[u.ID] = u.Name
	}
	for i := range messages {
		m := &messages[i]
		if m.User == "" {
			m.User = names[m.UserID]
		}
		if m.User == "" {
			m.User = m.UserID
		}
	}
}

func (s *Service) Send(ctx context.Context, channel, thread, text string) (Message, error) {
	if strings.TrimSpace(text) == "" {
		return Message{}, errors.New("message is empty")
	}
	if len([]rune(text)) > 4000 {
		return Message{}, errors.New("message exceeds slkt's 4,000 character limit; shorten it before sending")
	}
	return s.Backend.Send(ctx, channel, thread, text)
}

var reactionName = regexp.MustCompile(`^[A-Za-z0-9_+\-]+$`)

func (s *Service) React(ctx context.Context, channel, ts, name string) error {
	name = strings.Trim(name, ":")
	if !reactionName.MatchString(name) {
		return errors.New("use a Slack emoji name, for example thumbsup")
	}
	return s.Backend.React(ctx, channel, ts, name)
}

// ParseMessageRef accepts a Slack permalink or CHANNEL:TIMESTAMP and preserves
// the exact message target (including when reacting to a reply).
func ParseMessageRef(ref string) (channel, ts string, err error) {
	if parts := strings.SplitN(ref, ":", 2); len(parts) == 2 && channelID.MatchString(parts[0]) && timestamp.MatchString(parts[1]) {
		return parts[0], parts[1], nil
	}
	u, parseErr := url.Parse(ref)
	if parseErr == nil && u.Scheme == "https" && (u.Hostname() == "slack.com" || strings.HasSuffix(u.Hostname(), ".slack.com")) && u.User == nil && u.Port() == "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) == 3 && parts[0] == "archives" && channelID.MatchString(parts[1]) && strings.HasPrefix(parts[2], "p") {
			digits := strings.TrimPrefix(parts[2], "p")
			if len(digits) > 6 {
				ts = digits[:len(digits)-6] + "." + digits[len(digits)-6:]
				if timestamp.MatchString(ts) {
					return parts[1], ts, nil
				}
			}
		}
	}
	return "", "", errors.New("expected a Slack message URL or CHANNEL_ID:TIMESTAMP (for example C012345:1720000000.000001)")
}

// ParseThreadRef uses the parent timestamp when Slack links to a thread reply.
func ParseThreadRef(ref string) (string, string, error) {
	channel, ts, err := ParseMessageRef(ref)
	if err != nil {
		return "", "", err
	}
	if u, parseErr := url.Parse(ref); parseErr == nil {
		if root := u.Query().Get("thread_ts"); root != "" {
			if !timestamp.MatchString(root) {
				return "", "", errors.New("invalid thread timestamp in Slack URL")
			}
			ts = root
		}
	}
	return channel, ts, nil
}

func SortMessages(messages []Message) {
	sort.SliceStable(messages, func(i, j int) bool { return messages[i].Timestamp < messages[j].Timestamp })
}

// Merge replaces edits, deduplicates retries and bounds the session cache. The UI
// anchors selection by timestamp, so incoming messages do not move the viewport.
func Merge(old, incoming []Message, limit int) []Message {
	byTS := make(map[string]Message, len(old)+len(incoming))
	for _, m := range old {
		byTS[m.Timestamp] = m
	}
	for _, m := range incoming {
		byTS[m.Timestamp] = m
	}
	merged := make([]Message, 0, len(byTS))
	for _, m := range byTS {
		merged = append(merged, m)
	}
	SortMessages(merged)
	if limit > 0 && len(merged) > limit {
		merged = merged[len(merged)-limit:]
	}
	return merged
}
