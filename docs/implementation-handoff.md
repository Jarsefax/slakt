# Implementation handoff

This is the starting context for a future implementation chat. The planning chat
was explicitly limited to documenting decisions. The owner will choose the model
and reasoning level and explicitly request implementation in a new chat.

## Read first

- [README](../README.md): project overview and proposed commands.
- [Project plan](plan.md): decisions, proposed CLI/TUI behavior, authentication,
  platform rollout, milestones, unresolved questions, and source links.
- Any applicable repository instructions present when implementation starts.

## Decisions to preserve

- The project is named **slakt**; its only executable command is **`slkt`**.
- Build a Go CLI and TUI for daily Slack use: channels, DMs, threads, and search.
- Favor pure-Go dependencies and verify builds with `CGO_ENABLED=0`.
- Users receive native binaries and do not install Go to run them.
- Start with the owner on Linux, with one account and workspace at a time.
- Eventually support colleagues on Linux, Windows, and macOS. The implementer can
  decide when to expand support. Prefer portable platform integration boundaries
  now and verified releases on other operating systems later.
- Use supported Slack APIs with the user's own authorization. Prefer browser
  OAuth with PKCE and a temporary localhost callback, subject to workspace tests.
- Store tokens in the OS credential store; do not extract official Slack client
  sessions or embed a client secret in the binary.
- Keep the first version local, with no background daemon or shared backend.

Cobra, Bubble Tea, and slack-go are proposals, not installed dependencies. Review
their current versions, API coverage, licenses, and pure-Go build compatibility
before adopting them. The Go module path has not been selected.

## What has actually been verified

- The repository contains documentation only, with no application or Go module.
- The development machine runs Fedora Linux 44 (Sway), AMD64.
- Go 1.26.8 is installed for development; the project's minimum Go version is
  still to be chosen.
- GNOME Keyring and libsecret are installed; `org.freedesktop.secrets` is running.
- No stored credentials were accessed and no keyring write/read test was run.
- Slack's official documentation was checked for OAuth, PKCE and localhost
  callbacks, user-token search, history rate limits, and Socket Mode.

No Slack app configuration, OAuth exchange, API access, message send, event
delivery, or binary build has been verified for this project. No dependency
downloads or installation changes were made during planning.

## Inputs needed for real workspace verification

- The approved internal Slack app and its public client ID.
- PKCE enabled and the chosen localhost redirect URL registered.
- The minimum user scopes matching the implemented API calls.
- An explicit test destination and message before sending anything to Slack.
- If testing Socket Mode, separately configured app-level credentials and event
  subscriptions, kept out of source control and chat.

The owner can create or obtain approval for the Slack app. It is acceptable to
build against simulated API responses while setup is pending, but report which
behaviors are only simulated. Never label an untested workspace integration as
working.

## Recommended order when implementation is requested

1. Read the plan and inspect repository state; preserve existing changes.
2. Choose a module path, minimum Go version, and a small dependency set. Set up
   builds that work with `CGO_ENABLED=0`.
3. Implement configuration, browser login, credential storage, refresh handling,
   and `auth status`/`auth logout`. Test cancellation, invalid callback state,
   expiry, and unavailable or locked credential storage.
4. Add shared services and the CLI for listing conversations, history, threads,
   search, sending, replies, and reactions, with pagination and useful errors.
5. Build the TUI on those services. Preserve drafts and navigation state and make
   the message destination and thread context visible.
6. Validate the chosen live-update approach. Handle rate limits, disconnections,
   gaps, duplicate events, and uncertain send outcomes. Shared-app Socket Mode
   does not broadcast every event to every client; resolve that before supporting
   simultaneous colleague clients.
7. Run focused tests, a pure-Go build, and Linux terminal smoke tests. Keep the
   documentation synchronized with implemented behavior and remaining limits.

The release acceptance target is being able to follow a channel, answer a DM,
reply in a thread, and find an older message entirely through `slkt`. Remote SSH
login, exact unread/read-state parity, broader OS releases, and shared event
delivery remain separate work unless explicitly included later.

## Suggested prompt for the next chat

> Implement the initial version of slakt in this repository. Read README.md,
> docs/plan.md, and docs/implementation-handoff.md first. Preserve the agreed
> decisions: the executable is slkt, use Go with pure-Go dependencies and
> CGO_ENABLED=0 builds, and target my Linux account and one Slack workspace first
> while keeping a path to colleagues on Windows, macOS, and Linux. Implement the
> planned authentication, CLI, and TUI in practical milestones. Use simulated
> Slack responses where app setup is pending, explain what is and is not verified,
> and keep the repository documentation current. Do not send Slack messages
> without an explicitly agreed test destination and content.
