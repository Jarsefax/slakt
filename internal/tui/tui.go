// Package tui implements the keyboard-first terminal interface.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/Jarsefax/slakt/internal/localdata"
	"github.com/Jarsefax/slakt/internal/platform"
	"github.com/Jarsefax/slakt/internal/render"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#67E8F9"))
var muted = lipgloss.NewStyle().Foreground(lipgloss.Color("#A7B4CB"))
var titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5F7FF"))
var messageStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8EDF7"))
var selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC")).Background(lipgloss.Color("#35365E"))
var selectionMarker = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#D5B3FF"))
var borderColor = lipgloss.Color("#627390")

type Options struct {
	Initial      *core.Conversation
	PollInterval time.Duration
	Demo         bool
	Store        *localdata.Store
}

type view struct {
	conversation core.Conversation
	thread       string
	messages     []core.Message
	selected     int
	viewport     viewport.Model
	starts       []int
	numbered     bool   // Number visibility in the cached viewport.
	day          string // Local date used by the cached Today separator.
	todayIndex   int    // First message below the Today separator, or -1.
	cursor       string
	hasMore      bool
	loading      bool
	generation   int
}

type model struct {
	ctx                                  context.Context
	service                              *core.Service
	options                              Options
	identity                             core.Identity
	conversations                        []core.Conversation
	users                                []core.User
	names                                map[string]string
	main                                 view
	preview                              *view
	previewGeneration                    int
	previewCancel                        context.CancelFunc
	previewError                         string
	thread                               *view
	search                               view
	searchActive                         bool
	searchQuery                          string
	searchPage, searchPages, searchTotal int
	width, height                        int
	sidebar                              bool
	sideIndex                            int
	mode                                 string
	input                                textinput.Model
	composer                             textarea.Model
	drafts                               map[string]string
	status                               string
	help                                 bool
	helpOffset                           int
	sending                              bool
	polling                              bool
	sidebarMode                          localdata.SidebarMode
	messageOpenTarget                    localdata.MessageOpenTarget
	openMessage                          func(target, team, channel, ts, thread string) error
	readState                            localdata.ReadState
	latest                               map[string]string
	unreadIndex                          int
	unreadPolling                        bool
	unreadError                          string
	storageError                         string
	configDirty, readDirty               bool
}

type directoryResult struct {
	identity      core.Identity
	conversations []core.Conversation
	users         []core.User
	err           error
	readState     localdata.ReadState
}
type messagesResult struct {
	channel, thread string
	generation      int
	more            bool
	page            core.MessagePage
	err             error
}
type searchResult struct {
	query  string
	page   int
	result core.SearchResult
	err    error
}
type sentResult struct {
	channel, thread, text string
	message               core.Message
	err                   error
}
type actionResult struct {
	text string
	err  error
}
type reactionResult struct {
	channel, ts, name string
	err               error
}
type dmResult struct {
	conversation core.Conversation
	err          error
}
type pollResult struct {
	channel, thread string
	generation      int
	main, replies   []core.Message
	err             error
}
type tickMsg time.Time
type editorResult struct {
	text string
	err  error
}

func Run(ctx context.Context, service *core.Service, opts Options) error {
	if opts.PollInterval < 5*time.Second {
		return fmt.Errorf("poll interval must be at least 5s")
	}
	m := newModel(ctx, service, opts)
	if !opts.Demo {
		if m.options.Store == nil {
			store, err := localdata.DefaultStore()
			if err != nil {
				return err
			}
			m.options.Store = store
		}
		config, err := m.options.Store.LoadConfig()
		if err != nil {
			return err
		}
		m.sidebarMode = config.SidebarMode
		m.messageOpenTarget = config.MessageOpenTarget
	} else {
		m.options.Store = nil
	}
	finished, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if final, ok := finished.(model); ok {
		final.clearPreview()
		if final.configDirty || final.readDirty {
			final.persistLocal()
			if err == nil && final.storageError != "" {
				err = fmt.Errorf("%s", final.storageError)
			}
		}
	}
	return err
}

func newModel(ctx context.Context, s *core.Service, opts Options) model {
	input := textinput.New()
	input.CharLimit = 500
	input.Width = 55
	composer := textarea.New()
	composer.Placeholder = "Write a message…"
	composer.CharLimit = 4000
	composer.SetHeight(3)
	composer.ShowLineNumbers = false
	composer.KeyMap.InsertNewline.SetKeys("enter", "alt+enter")
	return model{ctx: ctx, service: s, options: opts, input: input, composer: composer, drafts: map[string]string{}, names: map[string]string{}, sidebarMode: localdata.All, messageOpenTarget: localdata.Desktop, openMessage: platform.OpenSlackMessage, readState: localdata.NewReadState(time.Now()), latest: map[string]string{}, width: 100, height: 30, status: "Connecting…", main: view{viewport: viewport.New(60, 15)}, search: view{viewport: viewport.New(60, 15)}}
}

func (m model) Init() tea.Cmd { return tea.Batch(m.directoryCmd(), m.tick(), m.unreadTick()) }
func (m model) tick() tea.Cmd {
	return tea.Tick(m.options.PollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m model) directoryCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		id, err := m.service.Backend.Identity(ctx)
		if err != nil {
			return directoryResult{err: err}
		}
		conversations, users, err := m.service.Directory(ctx, true)
		state := m.readState
		if err == nil && m.options.Store != nil {
			state, err = m.options.Store.LoadReadState(ctx, id.TeamID, id.UserID)
		}
		return directoryResult{identity: id, conversations: conversations, users: users, err: err, readState: state}
	}
}

func (m *model) active() *view {
	if m.thread != nil {
		return m.thread
	}
	if m.searchActive {
		return &m.search
	}
	return &m.main
}

func (m *model) numbersVisible(v *view) bool {
	return !m.sidebar && m.mode == "" && !m.help && v == m.active()
}

func (m *model) refreshViews(now time.Time) {
	for _, v := range []*view{&m.main, &m.search, m.thread, m.preview} {
		if v != nil && (v.numbered != m.numbersVisible(v) || v.day != now.Format("2006-01-02")) {
			m.rebuildAt(v, true, now)
		}
	}
}

func draftKey(channel, thread string) string { return channel + ":" + thread }
func (m *model) saveDraft() {
	v := m.active()
	if !m.searchActive || m.thread != nil {
		m.drafts[draftKey(v.conversation.ID, v.thread)] = m.composer.Value()
	}
}
func (m *model) restoreDraft() {
	v := m.active()
	m.composer.SetValue(m.drafts[draftKey(v.conversation.ID, v.thread)])
}

func (m *model) switchTo(c core.Conversation) tea.Cmd {
	m.saveDraft()
	m.mode = ""
	m.composer.Blur()
	m.thread = nil
	m.searchActive = false
	m.sidebar = false
	m.clearPreview()
	m.main = view{conversation: c, loading: true, generation: m.main.generation + 1, viewport: viewport.New(60, 15)}
	m.selectSidebar(c.ID)
	m.restoreDraft()
	m.status = "Loading " + c.Label() + "…"
	return m.loadCmd(m.main, false)
}

func (m model) loadCmd(v view, more bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		opts := core.PageOptions{Limit: 50}
		if more {
			if v.thread == "" && len(v.messages) > 0 {
				// The oldest cached message can change after eviction. A saved
				// cursor would skip evicted messages; paginate from the visible edge.
				opts.Before = v.messages[0].Timestamp
			} else {
				opts.Cursor = v.cursor
				if opts.Cursor == "" && len(v.messages) > 0 {
					opts.After = v.messages[len(v.messages)-1].Timestamp
				}
			}
		}
		page, err := m.service.Messages(ctx, v.conversation.ID, v.thread, opts)
		return messagesResult{v.conversation.ID, v.thread, v.generation, more, page, err}
	}
}

func (m model) searchCmd(query string, page int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		r, err := m.service.Search(ctx, query, 30, page)
		return searchResult{query, page, r, err}
	}
}

// Refresh the recent page to observe edits/reactions, then walk any missing pages
// until the previously newest timestamp is reached. Preserve the old view on failure.
func fetchUpdates(ctx context.Context, s *core.Service, v view) ([]core.Message, error) {
	if v.conversation.ID == "" {
		return nil, nil
	}
	latest := ""
	if len(v.messages) > 0 {
		latest = v.messages[len(v.messages)-1].Timestamp
	}
	opts := core.PageOptions{Limit: 50}
	if v.thread != "" {
		opts.After = latest
	}
	var all []core.Message
	seen := map[string]bool{}
	for pageNo := 0; pageNo < 100; pageNo++ {
		p, err := s.Messages(ctx, v.conversation.ID, v.thread, opts)
		if err != nil {
			return nil, err
		}
		all = core.Merge(all, p.Messages, 500)
		reached := v.thread == "" && (latest == "" || len(p.Messages) > 0 && p.Messages[0].Timestamp <= latest)
		if !p.HasMore || reached {
			return all, nil
		}
		next := p.Cursor
		if next == "" {
			if len(p.Messages) == 0 {
				return nil, fmt.Errorf("Slack returned an empty pagination page")
			}
			if v.thread != "" {
				opts.After = p.Messages[len(p.Messages)-1].Timestamp
			} else {
				opts.Before = p.Messages[0].Timestamp
			}
			next = opts.Before + ":" + opts.After
		} else {
			opts.Cursor = next
		}
		if seen[next] {
			return nil, fmt.Errorf("Slack repeated a history cursor")
		}
		seen[next] = true
	}
	return nil, fmt.Errorf("history gap exceeded 100 pages; reopen the conversation")
}

func (m model) pollCmd() tea.Cmd {
	main := m.main
	var thread view
	if m.thread != nil {
		thread = *m.thread
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		r := pollResult{channel: main.conversation.ID, thread: thread.thread, generation: main.generation}
		r.main, r.err = fetchUpdates(ctx, m.service, main)
		if r.err == nil && thread.thread != "" {
			r.replies, r.err = fetchUpdates(ctx, m.service, thread)
		}
		return r
	}
}

func (m *model) dimensions() (int, int, int) {
	side := 0
	if m.width >= 80 {
		side = 23
	}
	content := max(20, m.width-side-3)
	height := max(3, m.height-12)
	return side, content, height
}

func (m *model) rebuild(v *view, anchor bool) {
	m.rebuildAt(v, anchor, time.Now())
}

func (m *model) rebuildAt(v *view, anchor bool, now time.Time) {
	_, width, height := m.dimensions()
	if m.thread != nil && m.width >= 125 {
		left := (width - 3) / 2
		if v == m.thread {
			width -= left + 3
		} else if v == &m.main || v == &m.search || v == m.preview {
			width = left
		}
	}
	oldStart := 0
	if v.selected < len(v.starts) {
		oldStart = v.starts[v.selected]
	}
	delta := v.viewport.YOffset - oldStart
	v.viewport.Width, v.viewport.Height = width, height
	var lines []string
	v.starts = nil
	v.numbered = m.numbersVisible(v)
	v.day = now.Format("2006-01-02")
	v.todayIndex = -1
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start, end := startOfDay.Unix(), startOfDay.AddDate(0, 0, 1).Unix()
	// Reserve three digits for the 500-message cache, keeping text aligned as
	// selection and pagination change the displayed numbers.
	digits := max(3, len(fmt.Sprint(len(v.messages))))
	gutterWidth := digits + 3
	textWidth := max(1, width-gutterWidth)
	for i, msg := range v.messages {
		seconds, _, _ := strings.Cut(msg.Timestamp, ".")
		ts, err := strconv.ParseInt(seconds, 10, 64)
		if v.todayIndex < 0 && err == nil && ts >= start && ts < end {
			lines = append(lines, todaySeparator(width))
			v.todayIndex = i
		} else if i > 0 {
			lines = append(lines, separator(width))
		}
		v.starts = append(v.starts, len(lines))
		number := max(i-v.selected, v.selected-i)
		marker := "  "
		numberStyle := muted
		style := messageStyle
		authorStyle, metadataStyle, threadStyle := accent.Bold(true), muted, accent.Bold(true)
		if i == v.selected {
			number = i + 1
			marker = selectionMarker.Background(selectedStyle.GetBackground()).Render("▎ ")
			numberStyle = selectionMarker.Background(selectedStyle.GetBackground())
			style = selectedStyle.Width(textWidth)
			// Each header segment needs the background too: its ANSI reset
			// otherwise interrupts the enclosing selection style.
			authorStyle = authorStyle.Background(selectedStyle.GetBackground())
			metadataStyle = metadataStyle.Background(selectedStyle.GetBackground())
			threadStyle = threadStyle.Background(selectedStyle.GetBackground())
		}
		header := authorStyle.Render(render.Author(msg, m.names)) + metadataStyle.Render("  "+render.Time(msg.Timestamp))
		if m.searchActive && v == &m.search {
			header = authorStyle.Render(render.Safe(msg.ChannelID)) + metadataStyle.Render(" · ") + header
		}
		body := header + "\n" + render.FormattedMessageBody(msg, m.names, func(text string, formatting render.TextStyle) string {
			inline := style.UnsetWidth().Bold(formatting.Bold).Italic(formatting.Italic).Strikethrough(formatting.Strike)
			if formatting.Code {
				inline = inline.Foreground(lipgloss.Color("#FCD34D"))
				if i != v.selected {
					inline = inline.Background(lipgloss.Color("#202B40"))
				} else {
					inline = inline.Underline(true)
				}
			}
			return inline.Render(text)
		})
		if v.thread == "" {
			if indicator := threadIndicator(msg); indicator != "" {
				body += "\n" + threadStyle.Render(indicator)
			}
		}
		// Lip Gloss balances ANSI styles across wrapped rows before gutters
		// are added, so emphasis cannot leak into markers or the next row.
		for row, line := range strings.Split(style.Width(textWidth).Render(body), "\n") {
			gutter := strings.Repeat(" ", digits+1)
			if row == 0 && v.numbered {
				gutter = numberStyle.Render(fmt.Sprintf("%*d ", digits, number))
			}
			lines = append(lines, gutter+marker+line)
		}
	}
	if len(lines) == 0 {
		lines = []string{muted.Render("  No messages yet.")}
	}
	v.viewport.SetContent(strings.Join(lines, "\n"))
	if anchor && v.selected < len(v.starts) {
		v.viewport.SetYOffset(max(0, v.starts[v.selected]+delta))
	}
}

func threadIndicator(msg core.Message) string {
	if msg.ReplyCount > 0 {
		replies := "replies"
		if msg.ReplyCount == 1 {
			replies = "reply"
		}
		return fmt.Sprintf("↳ Thread · %d %s", msg.ReplyCount, replies)
	}
	if msg.ThreadTS != "" && msg.ThreadTS != msg.Timestamp {
		return "↳ In thread"
	}
	return ""
}

func (m *model) reveal(v *view) {
	if len(v.starts) == 0 {
		return
	}
	start := v.starts[v.selected]
	if v.selected == v.todayIndex {
		start-- // Reveal the Today label with its first message.
	}
	if start < v.viewport.YOffset {
		v.viewport.SetYOffset(start)
	}
	end := v.viewport.TotalLineCount()
	if v.selected+1 < len(v.starts) {
		end = v.starts[v.selected+1] - 1 // Exclude the separator before the next message.
	}
	if end > v.viewport.YOffset+v.viewport.Height {
		v.viewport.SetYOffset(max(start, end-v.viewport.Height))
	}
}

func (m *model) merge(v *view, incoming []core.Message, initial, older bool) {
	ts := ""
	if v.selected < len(v.messages) {
		ts = v.messages[v.selected].Timestamp
	}
	// Keep the message under the cursor, including when an old page is prepended.
	oldOffset := v.viewport.YOffset
	oldSelectedStart := 0
	if v.selected < len(v.starts) {
		oldSelectedStart = v.starts[v.selected]
	}
	v.messages = core.Merge(v.messages, incoming, 0)
	if len(v.messages) > 500 {
		if !older && v.thread == "" {
			v.hasMore = true
		}
		start := len(v.messages) - 500
		if older {
			start = 0
		} else {
			// Evict around the viewed window, never the selected message merely
			// because newer messages arrived while browsing older history.
			for i, msg := range v.messages {
				if msg.Timestamp == ts {
					start = min(start, max(0, i-250))
					break
				}
			}
		}
		v.messages = v.messages[start : start+500]
	}
	v.selected = max(0, len(v.messages)-1)
	for i, msg := range v.messages {
		if msg.Timestamp == ts {
			v.selected = i
			break
		}
	}
	m.rebuild(v, false)
	if initial {
		v.viewport.GotoBottom()
	} else if v.selected < len(v.starts) {
		v.viewport.SetYOffset(max(0, v.starts[v.selected]+oldOffset-oldSelectedStart))
	}
}

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.update(message)
	next := updated.(model)
	cmd = tea.Batch(cmd, next.syncPreview())
	if next.readingMain() {
		next.markRead(next.main.conversation.ID, next.main.messages)
	}
	// Focus and the local date can change while panes are cached. Preserve
	// their scroll anchors when refreshing numbering or the Today separator.
	next.refreshViews(time.Now())
	return next, cmd
}

func (m model) update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		_, width, _ := m.dimensions()
		m.composer.SetWidth(max(10, width-2))
		m.input.Width = max(10, width-5)
		m.rebuild(&m.main, true)
		m.rebuild(&m.search, true)
		if m.thread != nil {
			m.rebuild(m.thread, true)
		}
		if m.preview != nil {
			m.rebuild(m.preview, true)
		}
	case previewResult:
		if m.preview == nil || m.preview.conversation.ID != msg.channel || m.preview.generation != msg.generation {
			return m, nil
		}
		m.preview.loading = false
		if msg.err != nil {
			m.previewError = msg.err.Error()
			return m, nil
		}
		m.names = m.service.UserNames()
		m.merge(m.preview, msg.page.Messages, true, false)
		m.preview.cursor, m.preview.hasMore = msg.page.Cursor, msg.page.HasMore
		m.observeLatest(msg.channel, newestTimestamp(msg.page.Messages))
	case directoryResult:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.identity, m.conversations, m.users = msg.identity, msg.conversations, msg.users
		if msg.readState.StartedAt != "" {
			m.readState = msg.readState
		}
		for _, u := range m.users {
			m.names[u.ID] = u.PreferredName()
		}
		if m.options.Initial != nil {
			return m, m.switchTo(*m.options.Initial)
		}
		if len(m.conversations) > 0 {
			return m, m.switchTo(m.conversations[0])
		}
		m.status = "No conversations. Ctrl+k to open a DM."
	case messagesResult:
		v := &m.main
		if msg.thread != "" {
			v = m.thread
		}
		if v == nil || v.conversation.ID != msg.channel || v.thread != msg.thread || v.generation != msg.generation {
			return m, nil
		}
		v.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.names = m.service.UserNames()
		m.merge(v, msg.page.Messages, !msg.more, msg.more && v.thread == "")
		v.cursor, v.hasMore = msg.page.Cursor, msg.page.HasMore
		if msg.thread == "" {
			m.observeLatest(msg.channel, newestTimestamp(msg.page.Messages))
		}
		m.status = "Updated " + time.Now().Format("15:04:05")
	case unreadTickMsg:
		if m.unreadPolling {
			return m, nil
		}
		if m.identity.TeamID != "" {
			for range m.conversations {
				i := m.unreadIndex % len(m.conversations)
				m.unreadIndex = (i + 1) % len(m.conversations)
				if channel := m.conversations[i].ID; channel != m.main.conversation.ID {
					m.unreadPolling = true
					return m, m.unreadCmd(channel)
				}
			}
		}
		return m, m.unreadTick()
	case unreadResult:
		m.unreadPolling = false
		if msg.err != nil {
			m.unreadError = "Unread check paused: " + msg.err.Error()
		} else {
			m.unreadError = ""
			m.observeLatest(msg.channel, msg.latest)
		}
		return m, m.unreadTick()
	case tickMsg:
		if !m.polling && !m.main.loading && m.main.conversation.ID != "" && !m.sending {
			m.polling = true
			return m, m.pollCmd()
		}
		return m, m.tick()
	case pollResult:
		m.polling = false
		if msg.channel == m.main.conversation.ID && msg.generation == m.main.generation {
			if msg.err != nil {
				m.status = "Refresh paused: " + msg.err.Error()
			} else {
				m.names = m.service.UserNames()
				m.merge(&m.main, msg.main, false, false)
				m.observeLatest(msg.channel, newestTimestamp(msg.main))
				if m.thread != nil && m.thread.thread == msg.thread {
					m.merge(m.thread, msg.replies, false, false)
				}
				m.status = "Updated " + time.Now().Format("15:04:05")
			}
		}
		return m, m.tick()
	case searchResult:
		if msg.query != m.searchQuery {
			return m, nil
		}
		m.search.loading = false
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		m.names = m.service.UserNames()
		m.search.messages = msg.result.Messages
		m.search.selected = 0
		m.searchActive = true
		m.searchPage, m.searchPages, m.searchTotal = msg.page, msg.result.Pages, msg.result.Total
		m.rebuild(&m.search, false)
		m.search.viewport.GotoTop()
		m.status = fmt.Sprintf("%d matches · page %d/%d · n next page · Esc back", msg.result.Total, msg.page, max(1, msg.result.Pages))
	case sentResult:
		m.sending = false
		if msg.err != nil {
			m.status = msg.err.Error()
			if m.mode == "compose" {
				return m, m.composer.Focus()
			}
			return m, nil
		}
		delete(m.drafts, draftKey(msg.channel, msg.thread))
		m.mode = ""
		m.composer.Blur()
		m.composer.Reset()
		v := &m.main
		if msg.thread != "" {
			v = m.thread
		}
		if v != nil && v.conversation.ID == msg.channel && v.thread == msg.thread {
			msg.message.UserID = m.identity.UserID
			msg.message.User = m.names[m.identity.UserID]
			if msg.message.User == "" {
				msg.message.User = m.identity.User
			}
			m.merge(v, []core.Message{msg.message}, false, false)
			v.selected = max(0, len(v.messages)-1)
			m.rebuild(v, false)
			m.reveal(v)
			if msg.thread == "" {
				m.observeLatest(msg.channel, msg.message.Timestamp)
			}
		}
		if msg.thread != "" {
			for _, visible := range []*view{&m.main, &m.search, m.thread} {
				if visible == nil {
					continue
				}
				for i, parent := range visible.messages {
					if parent.ChannelID == msg.channel && parent.Timestamp == msg.thread {
						visible.messages = append([]core.Message(nil), visible.messages...)
						visible.messages[i].ReplyCount++
						m.rebuild(visible, true)
						break
					}
				}
			}
		}
		m.status = "Message sent"
	case actionResult:
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.status = msg.text
		}
	case reactionResult:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		for _, v := range []*view{&m.main, &m.search, m.thread} {
			if v == nil {
				continue
			}
			// Commands hold snapshots while requests are in flight. Keep those
			// snapshots (and backend-owned reaction slices) immutable.
			v.messages = append([]core.Message(nil), v.messages...)
			for i := range v.messages {
				message := &v.messages[i]
				if message.ChannelID != msg.channel || message.Timestamp != msg.ts {
					continue
				}
				message.Reactions = append([]core.Reaction(nil), message.Reactions...)
				found := false
				for j := range message.Reactions {
					if message.Reactions[j].Name == msg.name {
						message.Reactions[j].Count++
						found = true
						break
					}
				}
				if !found {
					message.Reactions = append(message.Reactions, core.Reaction{Name: msg.name, Count: 1})
				}
			}
			m.rebuild(v, true)
		}
		m.status = "Reaction added"
	case dmResult:
		if msg.err != nil {
			m.status = msg.err.Error()
			return m, nil
		}
		found := false
		for _, c := range m.conversations {
			if c.ID == msg.conversation.ID {
				found = true
			}
		}
		if !found {
			m.conversations = append(m.conversations, msg.conversation)
			core.SortConversations(m.conversations)
		}
		return m, m.switchTo(msg.conversation)
	case editorResult:
		if msg.err != nil {
			m.status = msg.err.Error()
		} else {
			m.composer.SetValue(msg.text)
			m.saveDraft()
			m.status = "Draft updated"
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) enteringText() bool {
	return m.mode != "" && !m.help && !m.sending && m.width >= 40 && m.height >= 15
}

func (m model) isCommandKey(key tea.KeyMsg, command string) bool {
	return !key.Paste && key.String() == command && !m.enteringText()
}

func (m model) isQuitKey(key tea.KeyMsg) bool {
	if key.Paste {
		return false
	}
	switch key.String() {
	case "ctrl+c", "alt+q":
		// A terminal may encode a rapid Escape, q as Alt+q.
		return true
	case "q":
		return m.isCommandKey(key, "q")
	}
	return false
}

func (m model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// The terminal can deliver several typed characters in one event. Dispatch
	// commands in order, then pass the remaining text intact to an explicitly
	// opened input. Pasted text must never activate commands.
	if key.Type == tea.KeyRunes && !key.Alt && !key.Paste && len(key.Runes) > 1 && !m.enteringText() {
		var cmds []tea.Cmd
		for i, r := range key.Runes {
			if m.enteringText() {
				key.Runes = key.Runes[i:]
				updated, cmd := m.handleKey(key)
				return updated, tea.Batch(append(cmds, cmd)...)
			}
			single := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
			if m.isQuitKey(single) {
				return m, tea.Quit
			}
			updated, cmd := m.handleKey(single)
			m = updated.(model)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}
	if m.isQuitKey(key) {
		return m, tea.Quit
	}
	if m.isCommandKey(key, "m") {
		selected := m.sidebarSelectedID()
		m.sidebarMode = m.sidebarMode.Next()
		m.selectSidebar(selected)
		m.configDirty = true
		m.persistLocal()
		return m, nil
	}
	if m.width < 40 || m.height < 15 {
		return m, nil
	}
	k := key.String()
	if m.sending {
		return m, nil
	}
	if m.help {
		if k == "?" || k == "esc" {
			m.help = false
		}
		_, width, height := m.dimensions()
		limit := max(0, len(helpLines(width))-(height+2))
		switch k {
		case "j", "down", "pgdown":
			m.helpOffset = min(limit, m.helpOffset+1)
		case "k", "up", "pgup":
			m.helpOffset = max(0, m.helpOffset-1)
		}
		return m, nil
	}
	if m.mode == "compose" {
		switch k {
		case "esc":
			m.saveDraft()
			m.mode = ""
			m.composer.Blur()
			return m, nil
		case "ctrl+e":
			return m, m.editCmd()
		case "ctrl+s":
			v := m.active()
			channel, thread, text := v.conversation.ID, v.thread, m.composer.Value()
			if strings.TrimSpace(text) == "" {
				return m, nil
			}
			m.sending = true
			m.composer.Blur()
			m.saveDraft()
			m.status = "Sending…"
			return m, func() tea.Msg {
				ctx, cancel := context.WithTimeout(m.ctx, 45*time.Second)
				defer cancel()
				message, err := m.service.Send(ctx, channel, thread, text)
				return sentResult{channel, thread, text, message, err}
			}
		}
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(key)
		m.saveDraft()
		return m, cmd
	}
	if m.mode != "" {
		switch k {
		case "esc":
			m.mode = ""
			m.input.Blur()
			return m, nil
		case "down", "ctrl+n":
			m.sideIndex++
			return m, nil
		case "up", "ctrl+p":
			m.sideIndex = max(0, m.sideIndex-1)
			return m, nil
		case "enter":
			text := strings.TrimSpace(m.input.Value())
			mode := m.mode
			m.mode = ""
			m.input.Blur()
			if mode == "jump" {
				choices := m.choices(text)
				if len(choices) == 0 {
					m.status = "No matching conversation or person"
					return m, nil
				}
				choice := choices[min(m.sideIndex, len(choices)-1)]
				if choice.user != "" {
					return m, func() tea.Msg { c, err := m.service.DM(m.ctx, choice.user); return dmResult{c, err} }
				}
				return m, m.switchTo(choice.conversation)
			}
			if mode == "search" {
				if text == "" {
					return m, nil
				}
				m.saveDraft()
				m.thread = nil
				m.searchQuery = text
				m.searchActive = true
				m.search.loading = true
				m.status = "Searching…"
				return m, m.searchCmd(text, 1)
			}
			if mode == "react" {
				v := m.active()
				if len(v.messages) == 0 {
					return m, nil
				}
				message := v.messages[v.selected]
				return m, func() tea.Msg {
					err := m.service.React(m.ctx, message.ChannelID, message.Timestamp, text)
					return reactionResult{message.ChannelID, message.Timestamp, strings.Trim(text, ":"), err}
				}
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		m.sideIndex = 0
		return m, cmd
	}
	switch k {
	case "?":
		m.help = true
		m.helpOffset = 0
		return m, nil
	case "ctrl+k":
		return m, m.prompt("jump", "Jump to a channel or person…")
	case "/":
		return m, m.prompt("search", "Search Slack…")
	case "tab":
		m.sidebar = !m.sidebar
		if m.sidebar {
			m.selectSidebar(m.main.conversation.ID)
		}
		return m, nil
	case "esc":
		if m.sidebar {
			m.sidebar = false
			return m, nil
		}
		m.saveDraft()
		if m.thread != nil {
			m.thread = nil
			m.rebuild(&m.main, true)
			m.rebuild(&m.search, true)
		} else if m.searchActive {
			m.searchActive = false
		}
		m.sidebar = false
		m.restoreDraft()
		return m, nil
	}
	if m.sidebar {
		items := m.sidebarConversations()
		switch k {
		case "j", "down":
			m.sideIndex = min(m.sideIndex+1, max(0, len(items)-1))
		case "k", "up":
			m.sideIndex = max(0, m.sideIndex-1)
		case "enter":
			if len(items) > 0 {
				return m, m.switchTo(items[min(m.sideIndex, len(items)-1)])
			}
		}
		return m, nil
	}
	v := m.active()
	switch k {
	case "j", "down", "k", "up", "g", "G", "home", "end":
		if len(v.messages) == 0 {
			return m, nil
		}
		switch k {
		case "j", "down":
			v.selected = min(v.selected+1, len(v.messages)-1)
		case "k", "up":
			v.selected = max(0, v.selected-1)
		case "g", "home":
			v.selected = 0
		case "G", "end":
			v.selected = len(v.messages) - 1
		}
		m.rebuild(v, false)
		m.reveal(v)
	case "pgup", "ctrl+u":
		v.viewport.HalfPageUp()
	case "pgdown", "ctrl+d":
		v.viewport.HalfPageDown()
	case "i":
		if m.searchActive && m.thread == nil {
			m.status = "Open a search result with Enter before composing"
			return m, nil
		}
		if v.conversation.ID == "" || v.loading {
			return m, nil
		}
		m.mode = "compose"
		m.restoreDraft()
		return m, m.composer.Focus()
	case "enter":
		if len(v.messages) == 0 || m.thread != nil {
			return m, nil
		}
		m.saveDraft()
		message := v.messages[v.selected]
		ts := message.ThreadTS
		if ts == "" {
			ts = message.Timestamp
		}
		if message.Permalink != "" {
			if _, root, err := core.ParseThreadRef(message.Permalink); err == nil {
				ts = root
			}
		}
		c := v.conversation
		if m.searchActive {
			c = core.Conversation{ID: message.ChannelID, Name: message.ChannelID}
			for _, candidate := range m.conversations {
				if candidate.ID == c.ID {
					c = candidate
					break
				}
			}
		}
		m.thread = &view{conversation: c, thread: ts, loading: true, generation: m.main.generation, viewport: viewport.New(40, 15)}
		m.restoreDraft()
		m.rebuild(&m.main, true)
		m.rebuild(&m.search, true)
		return m, m.loadCmd(*m.thread, false)
	case "n":
		if m.searchActive && m.thread == nil {
			if !m.search.loading && m.searchPage < m.searchPages {
				m.search.loading = true
				return m, m.searchCmd(m.searchQuery, m.searchPage+1)
			}
			return m, nil
		}
		if v.hasMore && !v.loading {
			v.loading = true
			return m, m.loadCmd(*v, true)
		}
	case "R":
		if !v.loading && !m.searchActive {
			v.loading = true
			v.cursor = ""
			v.messages = nil
			return m, m.loadCmd(*v, false)
		}
	case "r":
		if len(v.messages) > 0 {
			return m, m.prompt("react", "Emoji name, e.g. thumbsup")
		}
	case "o":
		if len(v.messages) > 0 {
			message := v.messages[v.selected]
			if message.ThreadTS == "" {
				message.ThreadTS = v.thread
			}
			if message.ThreadTS == "" && message.Permalink != "" {
				if _, root, err := core.ParseThreadRef(message.Permalink); err == nil {
					message.ThreadTS = root
				}
			}
			return m, func() tea.Msg {
				err := m.openMessage(string(m.messageOpenTarget), m.identity.TeamID, message.ChannelID, message.Timestamp, message.ThreadTS)
				return actionResult{"Opened in Slack (" + string(m.messageOpenTarget) + ")", err}
			}
		}
	}
	return m, nil
}

func (m *model) prompt(mode, placeholder string) tea.Cmd {
	m.sidebar = false
	m.clearPreview()
	m.mode = mode
	m.input.SetValue("")
	m.input.Placeholder = placeholder
	m.sideIndex = 0
	return m.input.Focus()
}

type choice struct {
	label, user  string
	conversation core.Conversation
}

func (m model) choices(query string) []choice {
	query = strings.ToLower(strings.TrimLeft(query, "#@"))
	var choices []choice
	for _, c := range m.conversations {
		if strings.Contains(strings.ToLower(c.Label()), query) {
			choices = append(choices, choice{label: c.Label(), conversation: c})
		}
	}
	for _, u := range m.users {
		if u.Deleted {
			continue
		}
		if strings.Contains(strings.ToLower(u.Name+" "+u.DisplayName), query) {
			choices = append(choices, choice{label: "@" + u.Name + " · open DM", user: u.ID})
		}
	}
	return choices
}

func (m model) editCmd() tea.Cmd {
	f, err := os.CreateTemp("", "slkt-draft-*.txt")
	if err != nil {
		return func() tea.Msg { return editorResult{err: err} }
	}
	path := f.Name()
	_, err = f.WriteString(m.composer.Value())
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return func() tea.Msg { return editorResult{err: err} }
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	args := strings.Fields(editor)
	if len(args) == 0 {
		args = []string{"vi"}
	}
	cmd := exec.Command(args[0], append(args[1:], path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorResult{err: fmt.Errorf("editor exited unsuccessfully; draft kept in composer")}
		}
		b, err := os.ReadFile(path)
		if len([]rune(string(b))) > 4000 {
			return editorResult{err: fmt.Errorf("edited draft exceeds 4,000 characters; original kept")}
		}
		return editorResult{text: strings.TrimSuffix(string(b), "\n"), err: err}
	})
}

func fit(text string, width int) string {
	return ansi.Truncate(strings.ReplaceAll(render.Safe(text), "\n", " "), max(1, width), "…")
}

func separator(width int) string {
	return lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("─", max(0, width)))
}

func todaySeparator(width int) string {
	label := fit(" Today ", width)
	padding := max(0, width-ansi.StringWidth(label))
	return accent.Bold(true).Render(strings.Repeat("─", padding/2) + label + strings.Repeat("─", padding-padding/2))
}

func sidebarHeading(section string, width int) string {
	color := lipgloss.Color("#67E8F9")
	switch section {
	case "Starred":
		color = lipgloss.Color("#FCD34D")
	case "Direct messages":
		color = lipgloss.Color("#6EE7B7")
	case "Group messages":
		color = lipgloss.Color("#C4B5FD")
	}
	label := fit(" "+section+" ", width)
	label += strings.Repeat("─", max(0, width-ansi.StringWidth(label)))
	return lipgloss.NewStyle().Bold(true).Foreground(color).Background(lipgloss.Color("#202B40")).Width(width).Render(label)
}

func (m model) sidebarView(width, height int) string {
	lines := []string{titleStyle.Render("  CONVERSATIONS"), muted.Render(fit("  "+m.sidebarMode.Label()+" · m", width)), separator(width)}
	type row struct {
		text, section string
		heading       bool
		spacer        bool
	}
	var rows []row
	section, selectedRow := "", 0
	for i, c := range m.sidebarConversations() {
		if c.Section() != section {
			section = c.Section()
			if len(rows) > 0 {
				rows = append(rows, row{section: section, spacer: true})
			}
			rows = append(rows, row{text: sidebarHeading(section, width), section: section, heading: true})
		}
		if m.sidebar && i == m.sideIndex || !m.sidebar && c.ID == m.main.conversation.ID {
			selectedRow = len(rows)
		}
		prefix := "  "
		if c.ID == m.main.conversation.ID {
			prefix = "› "
		}
		if m.isUnread(c.ID) {
			prefix += "● "
		}
		label := fit(prefix+c.Label(), width-2)
		if m.sidebar && i == m.sideIndex {
			label = selectedStyle.Width(width).Render(label)
		} else if c.ID == m.main.conversation.ID {
			label = accent.Bold(true).Render(label)
		} else if m.isUnread(c.ID) {
			label = titleStyle.Render(label)
		} else {
			label = messageStyle.Render(label)
		}
		rows = append(rows, row{text: label, section: section})
	}
	if len(rows) == 0 && len(lines) < height {
		label := "No conversations"
		if m.sidebarMode == localdata.UnreadOnly {
			label = "No unread conversations"
		}
		lines = append(lines, muted.Render(fit(label, width)))
	}
	// Leave room for a repeated section heading when scrolling into a group.
	start := max(0, selectedRow-(height-len(lines))+2)
	if start < len(rows) && rows[start].spacer {
		start++
	}
	if start < len(rows) && start > 0 && !rows[start].heading {
		lines = append(lines, sidebarHeading(rows[start].section, width))
	}
	for i := start; i < len(rows) && len(lines) < height; i++ {
		if (rows[i].heading || rows[i].spacer) && len(lines) == height-1 {
			break
		}
		lines = append(lines, rows[i].text)
	}
	border := borderColor
	if m.sidebar {
		border = lipgloss.Color("#67E8F9")
	}
	return lipgloss.NewStyle().Width(width).Height(height).BorderStyle(lipgloss.NormalBorder()).BorderRight(true).BorderForeground(border).Render(strings.Join(lines, "\n"))
}

func pane(v view, width, height int, label string) string {
	if v.loading {
		label += " · loading…"
	}
	caption := titleStyle.Render(fit(label, width))
	return lipgloss.NewStyle().Width(width).Height(height + 3).Render(caption + "\n" + separator(width) + "\n" + v.viewport.View() + "\n" + muted.Render("  "+func() string {
		if v.hasMore {
			return "n · load more"
		}
		return fmt.Sprintf("%d messages", len(v.messages))
	}()))
}

func helpLines(width int) []string {
	text := "Keyboard guide\n\nCtrl+k    Jump to a channel/person\nTab       Conversation sidebar\nm         Cycle sidebar mode outside text entry\nj/k ↑/↓   Select a message\nPgUp/Dn   Scroll long messages\nEnter     Open/start thread\ni         Compose a message\nEnter     Newline while composing\nCtrl+s    Send draft, then browse\nEsc       Keep draft / go back\nCtrl+e    External editor in composer\n/         Search Slack\nr         Add a reaction\no         Open message (configured target)\nn         Next history/search page\nR         Reload conversation\nq         Quit outside text entry\nCtrl+c    Quit anywhere\nEsc q     Leave text entry and quit\n\nText entry starts only with i, /, Ctrl+k, or r.\nSelected message: position starting at 1.\nOther numbers: distance from selection.\n↳ Thread: this message has replies.\n● Conversation: unread in slkt.\nSidebar modes: All / Unread first / Unread only.\nSidebar arrows/j/k preview without marking read.\nEnter opens; Tab/Esc returns to the original view.\nThe open conversation stays listed until you leave.\n\nUpdates use polling. Drafts live only in this process."
	return strings.Split(ansi.Wrap(text, max(20, width-2), ""), "\n")
}

func (m model) View() string {
	if m.width < 40 || m.height < 15 {
		return "slakt needs at least a 40×15 terminal. Resize, or press q to quit."
	}
	side, width, height := m.dimensions()
	badge := m.identity.Team
	if badge == "" {
		badge = "your terminal workspace"
	}
	if m.options.Demo {
		badge = "OFFLINE DEMO · nothing reaches Slack"
	}
	header := accent.Bold(true).Render(" slakt ") + muted.Render(" / "+fit(badge, max(10, m.width-15)))
	var body string
	if m.help {
		lines := helpLines(width)
		start := min(m.helpOffset, max(0, len(lines)-(height+2)))
		body = strings.Join(lines[start:min(len(lines), start+height+2)], "\n")
		body = lipgloss.NewStyle().Height(height + 2).Render(body)
	} else {
		v := m.main
		label := m.main.conversation.Label()
		if m.searchActive && !m.sidebar {
			v = m.search
			label = fmt.Sprintf("Search: %s · %d matches · %d/%d", m.searchQuery, m.searchTotal, m.searchPage, max(1, m.searchPages))
		}
		if m.preview != nil {
			v = *m.preview
			label = "Preview · " + v.conversation.Label()
		}
		if m.thread != nil && m.width >= 125 {
			left := (width - 3) / 2
			right := width - left - 3
			body = lipgloss.JoinHorizontal(lipgloss.Top, pane(v, left, height, label), accent.Render(" │ "), pane(*m.thread, right, height, "Thread · "+m.thread.conversation.Label()))
		} else if m.thread != nil && !m.sidebar {
			body = pane(*m.thread, width, height, "Thread · "+m.thread.conversation.Label()+" · Esc back")
		} else {
			body = pane(v, width, height, label)
		}
		if side > 0 {
			body = lipgloss.JoinHorizontal(lipgloss.Top, m.sidebarView(side-1, height+3), " ", body)
		}
	}
	var bottom string
	switch m.mode {
	case "compose":
		v := m.main
		if m.thread != nil {
			v = *m.thread
		}
		context := v.conversation.Label()
		if v.thread != "" {
			context += " / thread"
		}
		label := " COMPOSE · To " + context + " · Ctrl+s send · Esc browse · Ctrl+c quit · Ctrl+e editor"
		if m.sending {
			label = " SENDING · To " + context + " · m mode · q / Ctrl+c quit"
		}
		bottom = accent.Render(fit(label, m.width)) + "\n" + m.composer.View()
	case "jump", "search", "react":
		bottom = ansi.Truncate(accent.Render(" "+strings.ToUpper(m.mode)+" › ")+m.input.View(), m.width, "…")
		if m.mode == "jump" {
			choices := m.choices(m.input.Value())
			index := min(m.sideIndex, max(0, len(choices)-1))
			start := max(0, index-2)
			for i := start; i < min(len(choices), start+3); i++ {
				label := "  " + fit(choices[i].label, width-4)
				if i == index {
					label = selectedStyle.Render(label)
				}
				bottom += "\n" + label
			}
		} else {
			bottom += "\n" + muted.Render(" Enter to confirm · Esc cancel")
		}
	default:
		action := ""
		v := m.active()
		switch {
		case m.sidebar:
			action = "↑/↓ preview  Enter: open  Tab/Esc: back  "
		case m.thread != nil:
			action = "Esc: back  "
		case len(v.messages) > 0:
			action = "Enter: start thread  "
			if threadIndicator(v.messages[v.selected]) != "" {
				action = "Enter: open thread  "
			}
		}
		bottom = muted.Render(fit(" q quit  m mode  i compose  "+action+"Ctrl+k jump  / search  ? help", m.width))
		if draft := m.drafts[draftKey(m.main.conversation.ID, "")]; draft != "" {
			bottom += "\n" + accent.Render(fit(" Draft saved for this conversation · i to continue", m.width))
		}
	}
	if m.help {
		bottom = muted.Render(fit(" q quit · m mode · ↑/↓ scroll · ?/Esc back", m.width))
	}
	statusText := m.status
	if m.previewError != "" {
		statusText = "Preview failed: " + m.previewError
	}
	if m.unreadError != "" {
		statusText = m.unreadError
	}
	if m.storageError != "" {
		statusText = m.storageError
	}
	status := muted.Render(" "+fit(statusText, m.width-3)) + "\n" + muted.Render(fit(fmt.Sprintf(" polling every %s · %s", m.options.PollInterval, m.identity.User), m.width))
	return header + "\n" + separator(m.width) + "\n" + body + "\n" + separator(m.width) + "\n" + bottom + "\n" + status
}
