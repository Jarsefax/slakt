# slakt

Slack, at home in your terminal. **`slkt`** is the only executable: run it without
arguments for the terminal UI, or use subcommands for quick actions and scripts.

The first version includes channels, private conversations, DMs, paginated
history, threads, search, messages, and reactions. It runs locally, uses your
Slack identity, and stores OAuth credentials in your OS keyring.

Message authors and mentions use Slack display names, falling back to full
names, usernames, then user IDs. Names remain available for deactivated users'
older messages. If the user directory is unavailable, messages still load with
the available author name or ID.

## Try it

For development, install Go **1.26 or newer**, then:

```sh
make build
./bin/slkt --demo
```

To build and install the tool on Linux, or replace an older installation:

```sh
./install.sh                         # Installs to ~/.local/bin/slkt
slkt --demo
```

The installer works from any working directory and accepts an optional install
directory, for example `./install.sh "$HOME/bin"`. It builds a pure-Go
binary for the host, checks that it runs, then atomically replaces `slkt` in the
chosen directory. A failed build leaves the installed version intact. The
default directory needs no sudo; a custom directory must be writable. The script
prints a PATH command if needed, including when another `slkt` takes precedence.
Set `GO=/path/to/go` to choose Go explicitly.

Each completed application update delivered to a user gets a new version in
`cmd/slkt/VERSION`. Before 1.0, use patch bumps for compatible fixes and minor
bumps for features or compatibility changes, including renamed CLI options.
After finishing application and build changes, prepare the update:

```sh
make prepare-update BUMP=patch       # Use BUMP=minor for features/breaking changes
make check-version
./install.sh
slkt --version                      # Or slkt -V; -v is reserved for future verbosity
```

Preparation bumps the version and records the source fingerprint in
`releases/<version>.sha256`. Commit both files with the application change.
Never overwrite an existing record to reuse a version. Installation and
`make release` refuse missing, stale, or rewritten committed records. If you
change application/build inputs after preparation, prepare a new version.
Tests and documentation alone do not require a bump. Fingerprints cover
`go.mod`, `go.sum`, `Makefile`, `install.sh`, shell scripts in `scripts/`, and
non-test files in `cmd/` and `internal/` (excluding the version file).
The record travels with source archives, so validation also works without Git.

`make build` allows intermediate development edits without preparing a delivery.
Development builds and installations include the commit, local-change marker,
and source fingerprint, such as `0.2.0-dev+0ac15ea56a5a.dirty.src.0123456789ab`.
The fingerprint distinguishes different uncommitted application edits.
Plain `go build ./cmd/slkt` uses Git metadata and a `bin.<hash>` executable
fingerprint instead. `make release` uses the numeric version for the binary and
archive. `VERSION` can customize development labels; installation and release
require its base to match the prepared version, and always validate the source.

If local Go is unavailable, the installer tries a usable **Podman**, then
**Docker**, runtime. The fallback uses `docker.io/library/golang:1.26`; set
`SLKT_GO_IMAGE` to override the builder image. A first build may download the
image and Go dependencies. Container builds require the Linux coreutils `timeout`
command. An existing local Go build failure is reported without retrying the
compilation in a container.

The container receives a temporary copy of `go.mod`, `go.sum`, `cmd`, and
`internal`, and streams the built binary back to the installer. Its workspace
and Go caches are disposable; no host directories or credentials are mounted.
The resulting binary runs directly on the host after installation.

Containers use automatic removal, with an exit/signal trap to remove this run's
container after failure or interruption. Images already present are retained,
including images cached under another tag. If the installer pulls a new image,
cleanup attempts to remove that exact image without forcing deletion or pruning
parent images. A shared/in-use image or unavailable runtime can prevent cleanup;
the script reports a warning while preserving the original exit status. Staging
files are also removed. SIGKILL and power loss cannot run shell cleanup traps.

The offline demo needs no Slack app, credentials, or network connection. Messages
and drafts in the demo live only until the process exits. Demo search supports
text, `in:channel`, and `from:person`; real search uses Slack's query syntax.

`make release` produces a Linux AMD64 archive and checksum in `dist/`. People
running that binary do **not** need Go. Linux AMD64 is the initial target;
Windows/macOS integration is not yet certified.

## Connect your workspace

Create or obtain approval for an internal Slack app using the supplied
[manifest](docs/slack-app-manifest.json). Enable PKCE and token rotation and
register `http://localhost:8765/callback`. Follow the full
[Slack app setup guide](docs/slack-app.md), then:

```sh
./bin/slkt auth login --client-id YOUR_PUBLIC_CLIENT_ID
./bin/slkt auth status
./bin/slkt doctor
./bin/slkt
```

Login opens your browser and runs a temporary loopback callback listener. The
client ID is public configuration; no client secret is needed or embedded. On
Linux, an unlocked Secret Service provider such as GNOME Keyring is required.
`SLKT_CLIENT_ID` and `SLKT_REDIRECT_URL` provide defaults for the login flags.
Browser and CLI must run on the same machine for this first version.

Credentials go to the OS keyring under service `slakt`, account `default`. Only a
credential lock file is stored in the user's config directory (`slakt/auth.lock`).
There is no plaintext-token fallback. Login replaces the single active account;
logout removes local credentials and does not revoke app authorization in Slack.

## CLI

```sh
slkt                                      # Interactive TUI
slkt channels
slkt read '#engineering' --limit 30
slkt read C012345 --cursor 'NEXT_CURSOR'
slkt dm @alice                             # Open a DM in the TUI
slkt thread 'https://work.slack.com/archives/C012345/p1720000000000001'
slkt send '#engineering' 'Hello'
slkt reply 'C012345:1720000000.000001' 'Looks good'
slkt react 'C012345:1720000000.000001' thumbsup
slkt search 'in:engineering deploy after:2026-10-01'
slkt search 'from:alice' --limit 30 --page 2 --json
slkt config get message_open_target        # Effective value; defaults to desktop
slkt config set message_open_target browser
slkt config set message_open_target desktop
slkt auth logout
```

All data commands accept `--json`. History/thread JSON contains `messages`,
`has_more`, and `next_cursor`; use `--cursor` to continue. History is returned
oldest first **within each page**, starting with the most recent page. Thread
pages start with the parent and oldest replies. Search JSON includes total and
page counts. Messages include `channel_id` and exact Slack `ts` so scripts can
address them without URLs. Reactions target the exact linked message; `thread`
and `reply` follow a link's parent `thread_ts` when present.
If Slack provides `has_more` without a cursor, use `--before` the oldest history
timestamp or `--after` the newest thread timestamp to continue.

Names must be unambiguous. Use Slack conversation/user IDs when names collide.
`channels` lists the authenticated user's joined conversations, not every public
channel. Referring to a DM as `@person` can open that DM through
`conversations.open`. Errors go to stderr. Exit status is 0 on success,
1 on failure, and 130 on interruption. Without a terminal, specify a subcommand.

The TUI header shows the workspace on the left and your account name on the
right. Long names are shortened to fit the terminal. The app name and version
sit at the top-right of the footer, beside the help and quit hints.

The TUI sidebar and plain-text `channels` output group conversations into
**Starred**, **Channels** (public and private), **Direct messages**, and
**Group messages**, with names sorted within each section. Empty sections are
hidden; starred conversations appear only in Starred. Sidebar headings are
skipped by keyboard navigation and stay visible when scrolling within a section.
`channels --json` remains a flat array and includes `is_starred: true` when known.

Starred uses the optional `is_starred` field when Slack includes it in
[`users.conversations`](https://docs.slack.dev/reference/methods/users.conversations/).
That endpoint returns limited conversation objects and may omit the flag, so the
Starred section can be incomplete or absent. Starred metadata adds no
per-conversation API requests or new scopes. Slack's documented Web API does not expose custom
sidebar section names or assignments. The deprecated `stars.list` saved-items
API is not used. Reopen slkt to reload directory metadata.

Press `m` outside text entry to cycle the conversation sidebar mode, from any pane:

- **Visible** (the default when no mode is saved) keeps every non-hidden
  conversation in its usual alphabetical order.
- **Unread first** keeps non-hidden conversations, placing unread conversations
  first within each section. Each unread/read group remains alphabetical.
- **Unread only** shows non-hidden unread conversations and the currently open
  conversation, unless it is hidden.
  Explicitly opening a conversation marks it read locally after history loads
  successfully; its row stays visible until you switch away. Empty sections are hidden.
- **Hidden** shows only hidden conversations, so they are easy to unhide.
- **All** keeps every conversation, including hidden ones, in its usual
  alphabetical order. Hidden conversations have a **×** marker.

Press `h` with the sidebar focused to hide or unhide the highlighted channel,
DM, or group DM. Hiding removes it from **Visible** and both unread modes;
**Hidden** and **All** keep it accessible. A disappearing row selects its next
neighbor, or the previous row at the end of the list. The open conversation,
thread, and drafts stay intact. Hiding preserves read positions and does not
change Slack settings or notifications. `Ctrl+k` and Slack search still find
hidden conversations; opening one does not unhide it. Existing saved modes stay
selected after upgrading.

The current mode appears below **CONVERSATIONS**. A **●** marks conversations
with unread messages. `Ctrl+k` always searches the full conversation directory.
With the sidebar focused, `j/k` or arrows preview the highlighted conversation
in the message pane without marking it read. An open thread stays alongside the
preview on wide terminals; on narrow terminals the preview temporarily replaces
the thread. In **Unread only**, previews show only messages newer than the
conversation's local read position, including when highlighting the currently
open conversation. `Enter` opens the highlighted conversation with its full
history. `Tab` or `Esc` discards the preview and returns to the original
conversation, thread, or search, preserving drafts and scroll positions.

Press `r` in the sidebar to mark all messages in the highlighted conversation
read locally once its preview has loaded. Focus stays in the sidebar and the
preview remains visible. In **Unread only**, the read row stays selected until
you move up or down to another conversation; then it disappears unless new
unread messages have arrived. Navigation at a list boundary keeps the row.
Leaving sidebar browsing also releases the retained row.

Press `r` while browsing an open channel or DM, including its thread, to mark
that conversation read locally without changing focus, selection, scroll position,
or drafts. This also clears its **New messages** divider; later arrivals can
create a fresh divider. Search results must be closed first. Use `e` to add an
emoji reaction to the selected message.

Messages in an open conversation are marked read as they arrive while browsing
its timeline. Read tracking pauses while the conversation sidebar has focus,
including during background refreshes. Messages received while its timeline is
hidden by help, search, or a narrow thread view remain unread until you return
to the timeline. Explicitly opening a conversation treats its loaded history as
read, without requiring you to scroll through each message.

Startup restores the last opened conversation for the current workspace and
account, using its ID so renames and sidebar sorting do not change the selection.
An explicitly requested startup conversation takes priority; if no saved
conversation is available, slkt opens the first eligible conversation.
Any conversation opened by startup keeps its unread state, including through
polling, reloading, resizing, and pane changes. To resume normal read marking,
deliberately reopen it with sidebar `Enter` or `Ctrl+k`, or mark it read with
`r` in the sidebar or open conversation. Previewing other conversations does not
change the saved selection.

A bright **New messages** divider separates older history from messages newer
than the local read position captured when you opened the conversation or its
sidebar preview. The view opens at the first new message in the loaded history,
with a little older context above it when space permits. The divider stays at
that boundary for the visit, including after automatic read marking, refreshes,
and resizing. New arrivals appear below the same boundary. Pressing `r` in the
open conversation clears its divider and resets the boundary to the new read
position. Sidebar `r` preserves its preview's divider. Opening the conversation
again captures a fresh read position; a fully read conversation opens at its
latest message. Use `n` to load earlier history when needed.
Thread-only replies and search results have no local unread boundary.

Unread tracking belongs to slkt and does not read or change Slack's read markers.
Existing history starts read when tracking is first enabled for an account;
messages arriving after that baseline, including while slkt is closed, can be
flagged unread on the next background check. Known unread indicators are saved
and restored on startup without waiting for those checks. When upgrading from
a version that only saved read positions, the first scan builds this saved state.
Tracking covers conversation history, including thread replies broadcast to the
conversation. Replies only inside a thread, edits, and reactions do not create
sidebar unread indicators.

## Local configuration and state

UI configuration and unread state are stored separately:

| Data | Default Linux path | Environment override |
| --- | --- | --- |
| UI settings | `~/.config/slakt/config.json` | `$XDG_CONFIG_HOME/slakt/config.json` |
| Read positions and latest known message timestamps | `~/.local/state/slakt/read-state.json` | `$XDG_STATE_HOME/slakt/read-state.json` |

The configuration uses `"sidebar_mode": "visible"`, `"unread-first"`,
`"unread-only"`, `"hidden"`, or `"all"`; changing the mode saves it immediately
without changing focus. Missing or empty modes default to `"visible"`.
The `"message_open_target"` setting controls where `o` opens the selected message:

```json
{
  "sidebar_mode": "visible",
  "message_open_target": "desktop"
}
```

- `"desktop"` (the default when omitted) opens the message directly in the
  installed Slack app using its registered `slack://` handler.
- `"browser"` opens the message directly in Slack's web client in your default
  browser, bypassing the permalink landing page that can also launch the app.

Use `slkt config set message_open_target desktop` or
`slkt config set message_open_target browser`, then restart slkt to change the
message target. `slkt config get message_open_target` prints the effective value,
including the default when no setting has been saved. Both commands accept
`--json`, run locally without Slack authentication, and honor `XDG_CONFIG_HOME`.
The setter preserves other configuration fields. Configuration commands reject
`--demo`, which keeps its settings in memory.

You can also edit the configuration file directly. Each key
press opens only the chosen target; a launch failure is reported in the TUI.
Invalid target values are reported on startup. Cycling the sidebar mode preserves
the message target.

Hidden conversation IDs are saved immediately in the UI configuration under
`"hidden_conversations"`, scoped by `"TEAM_ID/USER_ID"`, with each hidden ID set
to `true`. Choices survive restarts and conversation renames. Concurrent sessions
merge changes to different conversation IDs; the latest save wins for the same
ID. Hiding and unhiding preserve other settings and read positions.

The last opened conversation ID is saved in the UI configuration under
`"last_conversations"`, keyed by `"TEAM_ID/USER_ID"`. Only opening a conversation
changes that preference; background checks and previews preserve it. Concurrent
sessions preserve other accounts' selections; the latest open saved for the same
account wins.

Read positions are scoped by workspace and account and saved when messages are read.
Files contain IDs, timestamps, and settings, with no message text or credentials.
Updates use private files and atomic replacement; concurrent sessions merge read
positions without moving them backwards. Deleting the state file resets tracking
on the next launch without changing the configured mode. Offline demo mode keeps
settings, hidden choices, and read positions in memory and does not touch these files.

## TUI keys

A muted **Today** separator marks the first loaded message from the current local
calendar day in conversations, threads, and search. When it coincides with the
**New messages** boundary, only the bright **New messages** divider appears.
Each current-day message also shows **Today HH:MM** in its timestamp, including
in conversation previews and CLI output. Older messages show **Mon DD HH:MM**.
The TUI labels refresh when the local day changes.

| Key | Action |
| --- | --- |
| `Ctrl+k` | Jump to a conversation or person; arrows choose a result |
| `Tab` | Focus/unfocus the conversation sidebar |
| `m` | Cycle Visible / Unread first / Unread only / Hidden / All outside text entry and help |
| `h` | Hide/unhide the highlighted sidebar conversation |
| `j/k`, arrows | Select a message; preview conversations while the sidebar has focus |
| `PgUp/PgDn`, `Ctrl+u/d` | Scroll long messages |
| `g/G` | First/last loaded message |
| `Enter` | Open a thread, search result, or highlighted sidebar conversation |
| `i` | Compose; Enter inserts a newline |
| `Ctrl+s` | Send the draft; return to browsing on success |
| `Ctrl+e` | Edit draft with `$VISUAL`, `$EDITOR`, or `vi` |
| `Esc` | Leave sidebar preview / save draft in this session / close thread or search |
| `/` | Search |
| `n` | Load another history/thread page or search page |
| `r` | Mark the highlighted sidebar conversation or open channel/DM read |
| `e` | Add a reaction to the selected message by emoji name |
| `o` | Open selected message in desktop Slack (default) or the configured browser target |
| `R` | Reload current conversation |
| `?` | Open contextual help while browsing; close help |
| `F1` | Open/close contextual help, including while typing or sending |
| `q` | Quit outside text entry, including help and pending operations |
| `Ctrl+c` | Quit in any TUI mode |

Text entry starts only when you press `i`, `/`, or `Ctrl+k`, or use `e` outside
the sidebar. Opening a channel or thread, restoring a saved draft, refreshing,
and resizing never activate an input. Text-entry modes are labeled **COMPOSE**,
**SEARCH**, **JUMP**, or **REACT**.
Successful sends return to browsing; failed sends keep the draft open for retry.
While an input is active, `q` is ordinary text and `Ctrl+c` still quits. To leave
text entry and quit, press `Esc`, then `q`; the terminal's combined `Alt+q`
encoding for this sequence is accepted too. Pasted text never activates commands.

The command hints show **? help · q quit** while browsing and **F1 help · Ctrl+c
quit** while typing. Composer instructions stay beside the draft. Help opens in
a centered modal over the dimmed workspace, with actions for the current focus
first, followed by common navigation and reference notes. `Esc`, `F1`, or `?`
closes it and preserves your input cursor, draft, selection, and focus. Use
arrows or `j/k` to scroll, `PgUp/PgDn` or `Ctrl+u/d` for pages, and `g/G` or
`Home/End` for the top/bottom. Other commands are captured by the modal; `q`
and `Ctrl+c` still quit. Background polling and pending sends can finish while
help is open. In text entry, `?` remains ordinary text; use `F1` to open help.

Threads appear alongside the timeline on wide terminals and replace it on narrow
ones. Messages use Vim-style hybrid numbering: the selected message shows its
1-based position in the loaded list, and other messages show their distance from
the selection. Only the active message list shows numbers, including the parent
in a thread. Focusing the sidebar or entering text hides numbers in every message
pane. Inactive selections keep their highlight and marker for context, and the
blank number gutter keeps wrapping and alignment stable across focus changes.
Wrapped lines have no extra numbers. Positions can change when older history is
loaded or cached messages are evicted. Numeric keyboard commands are deferred.

Messages with replies show a separate **↳ Thread · N replies** row; replies
shown in the channel or search show **↳ In thread** when no count is available.
Contextual help distinguishes **Enter: open thread** from **Enter: start thread**.
Confirmed replies update the visible parent indicator immediately.

The minimum size is 40×15. Destinations and thread context remain visible
while writing. Drafts are separate per conversation/thread and survive navigation
within the session. External editors receive a private temporary file; editor
commands accept an executable and whitespace-separated arguments, without shell
expansion. Unsaved drafts do not survive process exit or a crash.

## Refresh behavior and limits

- The TUI polls the open conversation and open thread with a **15-second**
  interval by default. Change this with `--poll-interval 30s` (minimum 5s). This is not a
  Socket Mode event feed and needs no app-level token.
- The footer names the open conversation being polled, adds `+ thread` when a
  thread is open, and shows a progress bar, countdown, and configured interval:
  `poll #general [■■□□□] in 9s · interval 15s`. It displays `polling…` during
  refreshes, then waits the configured interval after the request finishes.
  Narrower footers shorten names and use `9s/15s` (remaining/configured interval),
  dropping the bar when needed. Highlighting a sidebar preview does not change
  the poll target; Enter opens that conversation. A running refresh keeps its
  original target label until it finishes.
- Below the poll countdown, the unread status groups `next` and `checking`
  together with a ` · ` separator, shortening long names to fit. Between checks,
  `next` counts down to the next request and `checking` shows `—`. The countdown
  starts when the previous request finishes.
- Inactive conversations are checked one at a time for local unread indicators,
  requesting at most one history item per check. Checks are at least three seconds
  apart (slower if configured polling permits), starting at most 20 checks
  per minute, with the usual read retry/backoff behavior. A full scan takes longer
  in large workspaces; saved unread indicators appear immediately, and checks
  discover messages received while closed. Failed checks retain known unread
  state and retry later.
- Background unread checks follow the sidebar mode's hidden filter: **Visible**,
  **Unread first**, and **Unread only** skip hidden conversations; **Hidden** checks
  only hidden conversations; **All** checks every conversation. Read conversations
  still get checked in **Unread only** so new messages can make them appear.
  Scrolling the sidebar does not affect eligibility. Mode and hide/unhide changes
  apply to the next check; a request already running may finish. The open
  conversation and thread keep their separate refresh, even when hidden.
- Reads honor Slack's `Retry-After`, retry transient failures up to three times,
  and support cancellation. Refresh resumes automatically after disconnection.
  Gap recovery walks pages back to the previously newest message. Selection and
  scroll position stay anchored; the cache holds at most 500 messages per view.
- Recent channel messages are refreshed for edits/reactions. Deleted cached
  messages and edits to older/thread replies may remain until reload. There are
  no exact unread counts, Slack read-position synchronization, or background alerts.
- Failed refreshes keep the current view. Polling catches up when connectivity
  returns; recovering very large gaps is limited to 100 pages per refresh.
- Writes are never automatically retried. If a send's outcome is unknown, check
  Slack before retrying. The composer keeps the draft on failure.
- Terminal output strips untrusted control sequences. JSON escapes controls while
  preserving the decoded content. Tokens and message bodies are not logged.
- The TUI displays Slack's `*bold*`, `_italic_`, and `~strikethrough~` as terminal
  styles, highlights inline code, indents code blocks, and marks quotes with a
  side bar. Formatting stays literal inside code; unmatched markers remain
  visible. Style appearance depends on the terminal and font. Plain CLI output
  and JSON retain the original formatting markers.
- Standard emoji names in messages and reactions display as Unicode emoji,
  including skin tones and combined emoji. Code snippets and URLs stay literal;
  custom or unknown emoji stay as `:name:`. Appearance and color depend on the
  terminal and its fonts. JSON preserves the original Slack names.
- Files, rich blocks, calls, canvases, and workflows can be opened in Slack;
  file upload and rich interactions are deferred.

## Development and verification

```sh
make test          # Unit and simulated-API tests
make race          # Race detector (requires a C toolchain for testing)
make vet
make build         # CGO_ENABLED=0 native binary
make release       # Linux AMD64 archive + SHA-256
python3 scripts/test_install.py  # Isolated installer lifecycle tests; no real containers
python3 scripts/smoke_tui.py  # Linux PTY workflow against the offline demo
```

Tests use an in-memory workspace and local HTTP servers, covering OAuth
state/PKCE, refresh rotation/storage failure, pagination, rate limits, uncertain
sends, gap recovery, drafts, and terminal sanitization. Normal tests never access
the real keyring or Slack workspace. Dependency versions are pinned in
`go.mod`/`go.sum`; [third-party notices](docs/THIRD_PARTY_NOTICES.md) accompany the
release. The race detector's C requirement does not apply to shipped builds.
Regenerate release license texts after dependency changes with
`python3 scripts/third_party_notices.py`.

Real workspace OAuth, scope coverage, keyring lock/unlock, rate-limit tier, and
terminal/browser integrations still need owner acceptance using
[the setup checklist](docs/slack-app.md). No test message has been sent to Slack.
See [the plan](docs/plan.md) and [handoff](docs/implementation-handoff.md) for
remaining work.
