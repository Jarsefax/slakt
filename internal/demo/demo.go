// Package demo provides a completely offline, in-memory workspace.
package demo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
)

type Backend struct {
	mu            sync.Mutex
	conversations []core.Conversation
	users         []core.User
	history       map[string][]core.Message
	threads       map[string][]core.Message
}

func New() *Backend {
	b := &Backend{
		conversations: []core.Conversation{{ID: "CGENERAL", Name: "general", Kind: "channel"}, {ID: "CENGINEERING", Name: "engineering", Kind: "channel", IsStarred: true}, {ID: "CRANDOM", Name: "random", Kind: "channel"}, {ID: "CPRIVATE", Name: "release-room", Kind: "private"}, {ID: "DALICE", Name: "alice", Kind: "dm", UserID: "UALICE"}, {ID: "GRELEASE", Name: "alice, sam", Kind: "group-dm"}},
		users:         []core.User{{ID: "UYOU", Name: "you", DisplayName: "You"}, {ID: "UALICE", Name: "alice", DisplayName: "Alice"}, {ID: "USAM", Name: "sam", DisplayName: "Sam"}},
		history:       map[string][]core.Message{}, threads: map[string][]core.Message{},
	}
	base := time.Now().Add(-2 * time.Hour).Unix()
	add := func(channel, user, text string, minute int) core.Message {
		m := core.Message{ChannelID: channel, Timestamp: fmt.Sprintf("%d.000001", base+int64(minute*60)), UserID: user, Text: text}
		b.history[channel] = append(b.history[channel], m)
		return m
	}
	add("CGENERAL", "UALICE", "Welcome to slakt. A little less browser, a little more terminal.", 0)
	add("CGENERAL", "USAM", "Try Ctrl+k to jump to a channel or person. Press i to write a message.", 3)
	root := add("CGENERAL", "UALICE", "The first version is ready to explore. Open this thread with Enter and tell us what you think.", 8)
	b.history["CGENERAL"][2].ReplyCount = 2
	b.threads[root.Timestamp] = []core.Message{root, {ChannelID: "CGENERAL", Timestamp: fmt.Sprintf("%d.000001", base+600), ThreadTS: root.Timestamp, UserID: "USAM", Text: "Channels, DMs, search, and threads—all from here."}, {ChannelID: "CGENERAL", Timestamp: fmt.Sprintf("%d.000001", base+660), ThreadTS: root.Timestamp, UserID: "UALICE", Text: "This is an offline demo. Nothing you type here reaches Slack."}}
	add("CGENERAL", "USAM", "Search with /, react with r, or press ? for the keyboard guide.", 15)
	add("CGENERAL", "UALICE", "Your draft stays here while you switch conversations. Ctrl+s sends it; Esc returns to browsing.", 18)
	add("CENGINEERING", "USAM", "Deploy notes for today:\n```\ngo test ./...\nCGO_ENABLED=0 go build ./cmd/slkt\n```", 2)
	add("CENGINEERING", "UALICE", "The deploy looks *good*. Thanks <@USAM>! _Ready for review._ ~Pending~", 5)
	add("CRANDOM", "USAM", "Small tools. Quiet terminals. Good coffee. :coffee:", 4)
	add("CPRIVATE", "UALICE", "Private conversation history appears alongside your joined channels.", 5)
	add("DALICE", "UALICE", "Hey! Want to try replying from the terminal?", 6)
	add("GRELEASE", "USAM", "Let's coordinate the release here.", 7)
	return b
}

func (b *Backend) Identity(context.Context) (core.Identity, error) {
	return core.Identity{TeamID: "TDEMO", Team: "Slakt / demo", UserID: "UYOU", User: "you"}, nil
}
func (b *Backend) Conversations(context.Context, string) ([]core.Conversation, string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]core.Conversation(nil), b.conversations...), "", nil
}
func (b *Backend) Users(context.Context, string) ([]core.User, string, error) {
	return append([]core.User(nil), b.users...), "", nil
}

func paginate(messages []core.Message, opts core.PageOptions, reverse bool) (core.MessagePage, error) {
	filtered := make([]core.Message, 0)
	for _, m := range messages {
		if opts.After != "" && m.Timestamp <= opts.After {
			continue
		}
		if opts.Before != "" && m.Timestamp >= opts.Before {
			continue
		}
		filtered = append(filtered, m)
	}
	core.SortMessages(filtered)
	if reverse {
		for i, j := 0, len(filtered)-1; i < j; i, j = i+1, j-1 {
			filtered[i], filtered[j] = filtered[j], filtered[i]
		}
	}
	start := 0
	if opts.Cursor != "" {
		n, err := strconv.Atoi(opts.Cursor)
		if err != nil || n < 0 {
			return core.MessagePage{}, fmt.Errorf("invalid demo cursor")
		}
		start = n
	}
	start = min(start, len(filtered))
	end := min(start+opts.Limit, len(filtered))
	p := core.MessagePage{Messages: append([]core.Message{}, filtered[start:end]...), HasMore: end < len(filtered)}
	if p.HasMore {
		p.Cursor = strconv.Itoa(end)
	}
	return p, nil
}

func (b *Backend) History(_ context.Context, channel string, opts core.PageOptions) (core.MessagePage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return paginate(b.history[channel], opts, true)
}
func (b *Backend) Replies(_ context.Context, channel, ts string, opts core.PageOptions) (core.MessagePage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	messages := b.threads[ts]
	if len(messages) == 0 {
		for _, m := range b.history[channel] {
			if m.Timestamp == ts {
				messages = []core.Message{m}
				break
			}
		}
	}
	return paginate(messages, opts, false)
}

func (b *Backend) Search(_ context.Context, query string, limit, page int) (core.SearchResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var all []core.Message
	for _, messages := range b.history {
		all = append(all, messages...)
	}
	for _, messages := range b.threads {
		if len(messages) > 1 {
			all = append(all, messages[1:]...)
		}
	}
	var matches []core.Message
	for _, m := range all {
		ok := true
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if strings.HasPrefix(term, "in:") {
				name := ""
				for _, c := range b.conversations {
					if c.ID == m.ChannelID {
						name = c.Name
					}
				}
				ok = ok && strings.TrimPrefix(term, "in:") == name
			} else if strings.HasPrefix(term, "from:") {
				ok = ok && strings.EqualFold("U"+strings.TrimPrefix(term, "from:"), m.UserID)
			} else {
				ok = ok && strings.Contains(strings.ToLower(m.Text), term)
			}
		}
		if ok {
			matches = append(matches, m)
		}
	}
	core.SortMessages(matches)
	for i, j := 0, len(matches)-1; i < j; i, j = i+1, j-1 {
		matches[i], matches[j] = matches[j], matches[i]
	}
	start := min((page-1)*limit, len(matches))
	end := min(start+limit, len(matches))
	return core.SearchResult{Messages: append([]core.Message{}, matches[start:end]...), Total: len(matches), Page: page, Pages: (len(matches) + limit - 1) / limit}, nil
}

func (b *Backend) OpenDM(_ context.Context, user string) (core.Conversation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conversations {
		if c.UserID == user {
			return c, nil
		}
	}
	c := core.Conversation{ID: "D" + strings.TrimPrefix(user, "U"), Name: user, Kind: "dm", UserID: user}
	b.conversations = append(b.conversations, c)
	return c, nil
}

func (b *Backend) Send(_ context.Context, channel, thread, text string) (core.Message, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	m := core.Message{ChannelID: channel, Timestamp: fmt.Sprintf("%d.%06d", time.Now().Unix(), time.Now().Nanosecond()/1000), ThreadTS: thread, UserID: "UYOU", User: "you", Text: text}
	if thread == "" {
		b.history[channel] = append(b.history[channel], m)
	} else {
		if len(b.threads[thread]) == 0 {
			for _, root := range b.history[channel] {
				if root.Timestamp == thread {
					b.threads[thread] = []core.Message{root}
					break
				}
			}
		}
		b.threads[thread] = append(b.threads[thread], m)
		for i := range b.history[channel] {
			if b.history[channel][i].Timestamp == thread {
				b.history[channel][i].ReplyCount++
			}
		}
	}
	return m, nil
}

func (b *Backend) React(_ context.Context, channel, ts, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	update := func(messages []core.Message) {
		for i := range messages {
			if messages[i].Timestamp == ts {
				messages[i].Reactions = append(messages[i].Reactions, core.Reaction{Name: name, Count: 1})
			}
		}
	}
	update(b.history[channel])
	for _, messages := range b.threads {
		update(messages)
	}
	return nil
}
func (b *Backend) Permalink(context.Context, string, string) (string, error) {
	return "", fmt.Errorf("demo messages have no Slack permalink")
}
