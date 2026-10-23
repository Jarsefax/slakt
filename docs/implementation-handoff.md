# Implementation handoff

The first version now has application code, pinned dependencies, tests, an offline
demo, and Linux build/release recipes. The owner explicitly requested this
implementation after planning. Read the [README](../README.md) for usage and
[plan](plan.md) for implemented behavior and remaining decisions.

## Start here

```sh
make test
make build
./bin/slkt --demo
```

The next milestone is actual workspace acceptance, using
[Slack app setup](slack-app.md). A public client ID, PKCE-enabled approved app,
registered localhost callback, and required user scopes are still needed.
Do not request tokens or client secrets in chat. Do not send a real test message
without an agreed destination and exact content.

## Code map

- `cmd/slkt`: executable, signal cancellation, error exit status.
- `internal/auth`: PKCE, temporary callback server, refresh and OS keyring storage.
- `internal/core`: shared services, resolution, pagination and message merge.
- `internal/slackapi`: slack-go adapter, bounded read retry and write ambiguity.
- `internal/demo`: isolated, in-memory workspace with no network or credentials.
- `internal/cli`: Cobra commands, JSON and noninteractive behavior.
- `internal/tui`: Bubble Tea UI, navigation, session drafts and polling.
- `internal/localdata`: separate XDG sidebar configuration and account-scoped
  local read positions, private atomic files, and concurrent-session merging.
- `internal/render`, `internal/platform`: safe terminal text, browser boundary,
  and Linux desktop notifications through the session D-Bus service.

Tests use local HTTP servers and simulated data, never the real keyring or Slack.
Keep that property. Polling is intentional for this release; do not present it as
Socket Mode or exact unread/read-state synchronization. Drafts are session-only.
Sidebar modes and local unread indicators persist separately; inactive
conversations use paced, one-item history checks. Thread-only replies do not
affect sidebar unread status. See the README for storage paths and read behavior.
Desktop notifications default to on for non-hidden conversations while the TUI
runs; configuration can disable them. Notification discovery adds non-hidden
conversations to Hidden mode's paced scan. Startup backlog and own messages stay
silent; at most the newest incoming message found by a check is announced.
Delivery runs outside the UI update, uses bounded private D-Bus connections, and
never changes read positions. Tests use an isolated notification service and
never notify the real desktop. Demo mode cannot send desktop notifications.
The optional notification status row defaults to on and shows per-session
dispatch, successful delivery, and failure counts. Successful delivery means
desktop-service acceptance, rather than proof of a displayed popup. The latest
failure survives later successes; `notification_status` controls only row
visibility, independently of the `notifications` delivery setting.
Notification diagnostics retain the last message decision and latest check per
conversation. The footer gives a short reason; contextual help exposes source,
message timestamp, startup/previous/effective cutoffs, and decision time in UTC.
Empty checks and older pages preserve the newer message's decision; repeat
checks retain its first outcome. Search, pagination, and fetch failures also
record their bypass reason. Only metadata is held in session memory, with no
message-body logging. The `notification_status` setting controls these details.
Ship pure-Go builds, and validate platform integrations natively before expanding
the supported release targets.

## Local environment evidence

Planning inspection found Fedora Linux 44/Sway on AMD64, Go 1.26.8, and a running
GNOME Keyring Secret Service provider. No stored credentials were accessed in
planning or implementation. Browser login, keyring storage/lock state, actual
Slack scope coverage, and the workspace's rate tier remain unverified.
