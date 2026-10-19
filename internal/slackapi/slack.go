// Package slackapi keeps slack-go and its wire types behind the application boundary.
package slackapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/slack-go/slack"
)

type TokenSource func(context.Context) (string, error)

type Backend struct {
	Token  TokenSource
	HTTP   *http.Client
	APIURL string
}

func New(token TokenSource) *Backend {
	return &Backend{Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}, APIURL: "https://slack.com/api/"}
}

// request retries only reads. Writes with a lost response must be checked in
// Slack before a human retries; a network error does not mean a message failed.
func request[T any](ctx context.Context, b *Backend, method string, read bool, fn func(*slack.Client) (T, error)) (T, error) {
	return requestWithToken(ctx, b, method, read, func(token string) (T, error) {
		client := slack.New(token, slack.OptionHTTPClient(b.HTTP), slack.OptionAPIURL(b.APIURL))
		return fn(client)
	})
}

func requestWithToken[T any](ctx context.Context, b *Backend, method string, read bool, fn func(string) (T, error)) (T, error) {
	var zero T
	for attempt := 0; ; attempt++ {
		token, err := b.Token(ctx)
		if err != nil {
			return zero, err
		}
		value, err := fn(token)
		if err == nil {
			return value, nil
		}
		if ctx.Err() != nil {
			if read {
				return zero, ctx.Err()
			}
			return zero, fmt.Errorf("%s interrupted; outcome is unknown—check Slack before retrying", method)
		}
		var rate *slack.RateLimitedError
		var status slack.StatusCodeError
		var api slack.SlackErrorResponse
		wait := time.Duration(1<<min(attempt, 3)) * time.Second
		retry := false
		switch {
		case errors.As(err, &rate):
			wait = max(rate.RetryAfter, time.Second)
			retry = true
		case errors.As(err, &status):
			retry = status.Code >= 500
		case errors.As(err, &api):
			retry = api.Err == "internal_error" || api.Err == "fatal_error" || api.Err == "request_timeout" || api.Err == "service_unavailable"
		default:
			retry = true
		}
		if read && retry && attempt < 3 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return zero, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if rate != nil {
			return zero, fmt.Errorf("Slack rate limited %s; retry after %s", method, wait)
		}
		if errors.As(err, &api) {
			switch api.Err {
			case "missing_scope":
				return zero, fmt.Errorf("Slack %s requires additional user scopes; check docs/slack-app.md and authorize again", method)
			case "invalid_auth", "token_revoked", "token_expired", "not_authed", "account_inactive":
				return zero, errors.New("Slack authorization is invalid or expired; run slkt auth login again")
			case "channel_not_found", "not_in_channel":
				return zero, errors.New("conversation is unavailable; check its ID, membership, and app permissions")
			case "already_reacted":
				return zero, errors.New("you have already added that reaction")
			case "internal_error", "fatal_error", "request_timeout", "service_unavailable":
				if !read {
					return zero, fmt.Errorf("%s outcome is unknown; check Slack before retrying", method)
				}
				return zero, fmt.Errorf("Slack %s is temporarily unavailable; try again", method)
			default:
				// Slack error codes are identifiers; never print arbitrary response bodies.
				return zero, fmt.Errorf("Slack rejected %s; check permissions and arguments", method)
			}
		}
		if !read {
			return zero, fmt.Errorf("%s outcome is unknown; check Slack before retrying", method)
		}
		return zero, fmt.Errorf("Slack %s failed; check connectivity and try again", method)
	}
}

func (b *Backend) Identity(ctx context.Context) (core.Identity, error) {
	return request(ctx, b, "auth.test", true, func(api *slack.Client) (core.Identity, error) {
		r, err := api.AuthTestContext(ctx)
		if err != nil {
			return core.Identity{}, err
		}
		return core.Identity{TeamID: r.TeamID, Team: r.Team, UserID: r.UserID, User: r.User}, nil
	})
}

func conversation(c slack.Channel) core.Conversation {
	kind := "channel"
	if c.IsPrivate {
		kind = "private"
	}
	if c.IsMpIM {
		kind = "group-dm"
	}
	if c.IsIM {
		kind = "dm"
	}
	return core.Conversation{ID: c.ID, Name: c.Name, Kind: kind, UserID: c.User}
}

func (b *Backend) Conversations(ctx context.Context, cursor string) ([]core.Conversation, string, error) {
	type page struct {
		items  []core.Conversation
		cursor string
	}
	r, err := requestWithToken(ctx, b, "users.conversations", true, func(token string) (page, error) {
		// slack-go's Channel drops is_starred. Decode this read here so the
		// optional flag survives, without a separate info request per channel.
		values := url.Values{"cursor": {cursor}, "limit": {"200"}, "exclude_archived": {"true"}, "types": {"public_channel,private_channel,im,mpim"}}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.APIURL+"users.conversations", strings.NewReader(values.Encode()))
		if err != nil {
			return page{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response, err := b.HTTP.Do(req)
		if err != nil {
			return page{}, err
		}
		defer response.Body.Close()
		if response.StatusCode == http.StatusTooManyRequests {
			retry, _ := strconv.Atoi(response.Header.Get("Retry-After"))
			return page{}, &slack.RateLimitedError{RetryAfter: time.Duration(retry) * time.Second}
		}
		if response.StatusCode != http.StatusOK {
			return page{}, slack.StatusCodeError{Code: response.StatusCode, Status: response.Status}
		}
		var result struct {
			slack.SlackResponse
			Channels []struct {
				slack.Channel
				IsStarred bool `json:"is_starred"`
			} `json:"channels"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			return page{}, err
		}
		if err := result.Err(); err != nil {
			return page{}, err
		}
		items := make([]core.Conversation, 0, len(result.Channels))
		for _, c := range result.Channels {
			item := conversation(c.Channel)
			item.IsStarred = c.IsStarred
			items = append(items, item)
		}
		return page{items, result.ResponseMetadata.Cursor}, nil
	})
	return r.items, r.cursor, err
}

func (b *Backend) Users(ctx context.Context, cursor string) ([]core.User, string, error) {
	type page struct {
		items  []core.User
		cursor string
	}
	r, err := request(ctx, b, "users.list", true, func(api *slack.Client) (page, error) {
		p := api.GetUsersPaginated(slack.GetUsersOptionLimit(200))
		p.Cursor = cursor
		p, err := p.Next(ctx)
		items := make([]core.User, 0, len(p.Users))
		for _, u := range p.Users {
			realName := u.Profile.RealName
			if strings.TrimSpace(realName) == "" {
				realName = u.RealName
			}
			items = append(items, core.User{ID: u.ID, Name: u.Name, DisplayName: u.Profile.DisplayName, RealName: realName, Deleted: u.Deleted, IsBot: u.IsBot})
		}
		return page{items, p.Cursor}, err
	})
	return r.items, r.cursor, err
}

func message(channel string, m slack.Message) core.Message {
	r := core.Message{ChannelID: channel, Timestamp: m.Timestamp, ThreadTS: m.ThreadTimestamp, UserID: m.User, User: m.Username, Text: m.Text, ReplyCount: m.ReplyCount}
	if r.User == "" && m.BotProfile != nil {
		r.User = m.BotProfile.Name
	}
	for _, reaction := range m.Reactions {
		r.Reactions = append(r.Reactions, core.Reaction{Name: reaction.Name, Count: reaction.Count})
	}
	for _, a := range m.Attachments {
		text := a.Title
		if a.Text != "" {
			text += "\n" + a.Text
		} else if a.Fallback != "" {
			text += "\n" + a.Fallback
		}
		r.Attachments = append(r.Attachments, text)
	}
	for _, f := range m.Files {
		r.Attachments = append(r.Attachments, "[file] "+f.Name+" "+f.Permalink)
	}
	if r.Text == "" && len(r.Attachments) == 0 {
		r.Text = "[Rich message: press o to open in Slack]"
	}
	return r
}

func (b *Backend) History(ctx context.Context, channel string, opts core.PageOptions) (core.MessagePage, error) {
	return request(ctx, b, "conversations.history", true, func(api *slack.Client) (core.MessagePage, error) {
		r, err := api.GetConversationHistoryContext(ctx, &slack.GetConversationHistoryParameters{ChannelID: channel, Limit: opts.Limit, Cursor: opts.Cursor, Latest: opts.Before, Oldest: opts.After})
		if err != nil {
			return core.MessagePage{}, err
		}
		p := core.MessagePage{Cursor: r.ResponseMetaData.NextCursor, HasMore: r.HasMore, Messages: make([]core.Message, 0, len(r.Messages))}
		for _, m := range r.Messages {
			p.Messages = append(p.Messages, message(channel, m))
		}
		return p, nil
	})
}

func (b *Backend) Replies(ctx context.Context, channel, ts string, opts core.PageOptions) (core.MessagePage, error) {
	return request(ctx, b, "conversations.replies", true, func(api *slack.Client) (core.MessagePage, error) {
		messages, more, next, err := api.GetConversationRepliesContext(ctx, &slack.GetConversationRepliesParameters{ChannelID: channel, Timestamp: ts, Limit: opts.Limit, Cursor: opts.Cursor, Latest: opts.Before, Oldest: opts.After})
		p := core.MessagePage{Cursor: next, HasMore: more, Messages: make([]core.Message, 0, len(messages))}
		for _, m := range messages {
			p.Messages = append(p.Messages, message(channel, m))
		}
		return p, err
	})
}

func (b *Backend) Search(ctx context.Context, query string, limit, page int) (core.SearchResult, error) {
	return request(ctx, b, "search.messages", true, func(api *slack.Client) (core.SearchResult, error) {
		params := slack.NewSearchParameters()
		params.Count, params.Page, params.Sort, params.SortDirection = limit, page, "timestamp", "desc"
		r, err := api.SearchMessagesContext(ctx, query, params)
		if err != nil {
			return core.SearchResult{}, err
		}
		result := core.SearchResult{Total: r.Total, Page: page, Pages: r.Paging.Pages, Messages: make([]core.Message, 0, len(r.Matches))}
		for _, m := range r.Matches {
			result.Messages = append(result.Messages, core.Message{ChannelID: m.Channel.ID, Timestamp: m.Timestamp, UserID: m.User, User: m.Username, Text: m.Text, Permalink: m.Permalink})
		}
		return result, nil
	})
}

func (b *Backend) OpenDM(ctx context.Context, user string) (core.Conversation, error) {
	return request(ctx, b, "conversations.open", false, func(api *slack.Client) (core.Conversation, error) {
		c, _, _, err := api.OpenConversationContext(ctx, &slack.OpenConversationParameters{Users: []string{user}, ReturnIM: true})
		if err != nil {
			return core.Conversation{}, err
		}
		return conversation(*c), nil
	})
}

func (b *Backend) Send(ctx context.Context, channel, thread, text string) (core.Message, error) {
	return request(ctx, b, "chat.postMessage", false, func(api *slack.Client) (core.Message, error) {
		opts := []slack.MsgOption{slack.MsgOptionText(text, false), slack.MsgOptionDisableLinkUnfurl(), slack.MsgOptionDisableMediaUnfurl()}
		if thread != "" {
			opts = append(opts, slack.MsgOptionTS(thread))
		}
		_, ts, err := api.PostMessageContext(ctx, channel, opts...)
		return core.Message{ChannelID: channel, Timestamp: ts, ThreadTS: thread, Text: text}, err
	})
}

func (b *Backend) React(ctx context.Context, channel, ts, name string) error {
	_, err := request(ctx, b, "reactions.add", false, func(api *slack.Client) (struct{}, error) {
		return struct{}{}, api.AddReactionContext(ctx, name, slack.ItemRef{Channel: channel, Timestamp: ts})
	})
	return err
}

func (b *Backend) Permalink(ctx context.Context, channel, ts string) (string, error) {
	return request(ctx, b, "chat.getPermalink", true, func(api *slack.Client) (string, error) {
		return api.GetPermalinkContext(ctx, &slack.PermalinkParameters{Channel: channel, Ts: ts})
	})
}
