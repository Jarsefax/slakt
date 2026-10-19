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

The TUI sidebar and plain-text `channels` output group conversations into
**Starred**, **Channels** (public and private), **Direct messages**, and
**Group messages**, with names sorted within each section. Empty sections are
hidden; starred conversations appear only in Starred. Sidebar headings are
skipped by keyboard navigation and stay visible when scrolling within a section.
`channels --json` remains a flat array and includes `is_starred: true` when known.

Starred uses the optional `is_starred` field when Slack includes it in
[`users.conversations`](https://docs.slack.dev/reference/methods/users.conversations/).
That endpoint returns limited conversation objects and may omit the flag, so the
Starred section can be incomplete or absent. There are no extra per-conversation
API requests or new scopes. Slack's documented Web API does not expose custom
sidebar section names or assignments. The deprecated `stars.list` saved-items
API is not used. Reopen slkt to reload directory metadata.

## TUI keys

A bold **Today** separator marks the first loaded message from the current local
calendar day in conversations, threads, and search. It refreshes when the local
day changes.

| Key | Action |
| --- | --- |
| `Ctrl+k` | Jump to a conversation or person; arrows choose a result |
| `Tab` | Focus/unfocus the conversation sidebar |
| `j/k`, arrows | Select a message or conversation |
| `PgUp/PgDn`, `Ctrl+u/d` | Scroll long messages |
| `g/G` | First/last loaded message |
| `Enter` | Open a thread or search result |
| `i` | Compose; Enter inserts a newline |
| `Ctrl+s` | Send the draft; return to browsing on success |
| `Ctrl+e` | Edit draft with `$VISUAL`, `$EDITOR`, or `vi` |
| `Esc` | Save draft in this session / close thread or search |
| `/` | Search |
| `n` | Load another history/thread page or search page |
| `r` | Add a reaction by emoji name |
| `o` | Open selected message in Slack |
| `R` | Reload current conversation |
| `?` | Keybinding guide |
| `q` | Quit outside text entry, including help and pending operations |
| `Ctrl+c` | Quit in any TUI mode |

Text entry starts only when you press `i`, `/`, `Ctrl+k`, or `r`. Opening a channel
or thread, restoring a saved draft, refreshing, and resizing never activate an
input. Text-entry modes are labeled **COMPOSE**, **SEARCH**, **JUMP**, or **REACT**.
Successful sends return to browsing; failed sends keep the draft open for retry.
While an input is active, `q` is ordinary text and `Ctrl+c` still quits. To leave
text entry and quit, press `Esc`, then `q`; the terminal's combined `Alt+q`
encoding for this sequence is accepted too. Pasted text never activates commands.

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
The footer distinguishes **Enter: open thread** from **Enter: start thread**.
Confirmed replies update the visible parent indicator immediately.

The minimum size is 40×15. Destinations and thread context remain visible
while writing. Drafts are separate per conversation/thread and survive navigation
within the session. External editors receive a private temporary file; editor
commands accept an executable and whitespace-separated arguments, without shell
expansion. Unsaved drafts do not survive process exit or a crash.

## Refresh behavior and limits

- The TUI polls the active channel and open thread every **15 seconds** by
  default. Change this with `--poll-interval 30s` (minimum 5s). This is not a
  Socket Mode event feed and needs no app-level token.
- Reads honor Slack's `Retry-After`, retry transient failures up to three times,
  and support cancellation. Refresh resumes automatically after disconnection.
  Gap recovery walks pages back to the previously newest message. Selection and
  scroll position stay anchored; the cache holds at most 500 messages per view.
- Recent channel messages are refreshed for edits/reactions. Deleted cached
  messages and edits to older/thread replies may remain until reload. There are
  no exact unread counts, read-position synchronization, or background alerts.
- Failed refreshes keep the current view. Polling catches up when connectivity
  returns; recovering very large gaps is limited to 100 pages per refresh.
- Writes are never automatically retried. If a send's outcome is unknown, check
  Slack before retrying. The composer keeps the draft on failure.
- Terminal output strips untrusted control sequences. JSON escapes controls while
  preserving the decoded content. Tokens and message bodies are not logged.
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
