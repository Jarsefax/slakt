# slakt project plan and implementation status

Updated: 2026-10-15. Status: first implementation, pending workspace acceptance.

## Preserved decisions

- Project: **slakt**. Only executable: **`slkt`**.
- Go CLI and TUI for channels, DMs, threads, and search; pure-Go dependencies and
  `CGO_ENABLED=0` native release binaries. Go 1.26 is the development minimum.
- Linux AMD64 and one account/workspace first. Keep platform boundaries for later
  colleague adoption on Linux, Windows, and macOS. Users do not need Go installed.
- Supported Slack APIs, a custom approved app, and the user's own identity.
  Browser OAuth with PKCE, no embedded client secret, no extracted Slack sessions.
- OS keyring for credentials. No shared backend or background daemon.
- Keep decisions and verification evidence in the repository.

The earlier planning-only restriction ended when the owner requested implementation.

## First-version implementation

| Area | Implemented | Verification |
| --- | --- | --- |
| Authentication | Browser PKCE, loopback callback, state checking, identity validation, keyring storage, refresh rotation, logout | Local HTTP and in-memory store tests; real authorization/keyring acceptance pending |
| Shared services | Cursor pagination, name/ID resolution, ambiguity errors, bounded message merge | Unit tests and offline workflow |
| CLI | Auth, channels, history, DM TUI, threads, send, reply, react, search, doctor, JSON | Offline command tests and binary smoke checks |
| TUI | Sidebar, timeline, responsive thread pane, composer, search, reactions, Slack links, external editor, session drafts | State tests and Linux pseudo-terminal smoke checks |
| Updates | Polling, Retry-After, read retries, gap pagination, deduplication, preserved selection | Simulated API and model tests; workspace rate tier pending |
| Safety | No automatic write retries, draft retention on send failure, terminal sanitization, no secret/body logs | Focused tests |
| Distribution | Linux AMD64 static build, archive/checksum recipe, dependency notices | Pure-Go build and binary smoke checks |

See README for commands and actual behavior. These are implemented features, not
claims that an unconfigured workspace integration has been validated.

## Implementation choices

- Module: `github.com/Jarsefax/slakt`, matching the configured repository remote.
- Cobra v1.10.2, Bubble Tea v1.3.10 with Bubbles v0.21.0 and Lip Gloss v1.1.0,
  slack-go v0.30.1 behind `internal/slackapi`, and go-keyring v0.2.8.
- Current slack-go requires Go 1.26. Development used the installed Go 1.26.8.
- Linux keyring access uses pure-Go D-Bus through Secret Service. The existing
  GNOME Keyring service was observed during planning; credential storage and
  locked/unlocked behavior have not been exercised on the owner's account.
- Credential operations use a per-user filesystem lock (`gofrs/flock`) across
  processes so a TUI and CLI do not consume the same rotating refresh token.
  Credentials remain in the keyring. The public client ID is saved with the
  credential so refresh does not depend on a shell environment variable.
- Polling is the initial update transport. This avoids distributing an app-level
  secret and needs no event subscriptions. The recent active channel page and
  new thread replies are refreshed; missing history pages are recovered after
  disconnection. This is not a live event feed.
- Drafts and messages stay in memory (500 messages per view). Crash recovery and
  persistent message history are deferred; editor temporary files are removed
  on normal completion. CLI history returns one page with an explicit cursor.
- JSON preserves decoded content while escaping terminal control characters.
  Human-readable rendering removes controls and simplifies Slack markup.
- `doctor` validates auth and directory permissions; it does not claim to prove
  read/search/write coverage, mutate Slack, or test every scope.

## Remaining acceptance milestone

The release target remains following a channel, answering a DM, replying in a
thread, and finding an older message entirely through `slkt`.

1. Owner creates or approves the internal app using
   [the supplied manifest](slack-app-manifest.json); enables PKCE/rotation and
   registers the callback. Follow [setup and acceptance](slack-app.md).
2. Authorize from the Linux machine and validate credential storage,
   expiry/refresh, locked-keyring errors, and local logout.
3. Confirm private-channel and DM history, threads, pagination, search, and the
   internal-app rate-limit tier in the target workspace.
4. Send/reply/react only after the owner supplies an explicit test destination
   and content. No such authorization was supplied for this implementation run.
5. Exercise terminal resizing, browser opening, external editor, disconnection
   recovery, and polling behavior against actual Slack data.

No real Slack login, keyring write/read, workspace read, or workspace send was
performed while implementing this version.

## Deferred work

- Socket Mode and validated channel/private/DM/thread event coverage. Shared app
  connections distribute events rather than broadcasting every event to every
  colleague; solve routing before multi-client event support. Never bundle a
  shared app-level token in binaries.
- Exact unread counts, synchronization of Slack read positions, deletion events,
  older/thread edit parity, and notifications while slkt is closed. The running
  Linux TUI supports desktop notifications for non-hidden conversations through
  its existing polling; unopened threads are not monitored.
- Durable drafts, persistent bounded cache, remote/headless login, multi-account
  selection, calls, canvases, workflow interaction, and file uploads.
- Additional architectures and Windows/macOS releases. Cross-compilation is only
  a portability check; native login/keyring/terminal/package tests are required.

## Primary references

- [Slack PKCE](https://docs.slack.dev/authentication/using-pkce/)
- [Token rotation](https://docs.slack.dev/authentication/using-token-rotation/)
- [Conversation history and rate limits](https://docs.slack.dev/reference/methods/conversations.history/)
- [Message search](https://docs.slack.dev/reference/methods/search.messages/)
- [Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/)
- [Cobra](https://github.com/spf13/cobra), [Bubble Tea](https://github.com/charmbracelet/bubbletea),
  [slack-go](https://github.com/slack-go/slack), [go-keyring](https://github.com/zalando/go-keyring)
