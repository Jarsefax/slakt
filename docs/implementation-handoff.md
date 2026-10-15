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
- `internal/render`, `internal/platform`: safe terminal text and browser boundary.

Tests use local HTTP servers and simulated data, never the real keyring or Slack.
Keep that property. Polling is intentional for this release; do not present it as
Socket Mode or exact unread/read-state synchronization. Drafts are session-only.
Ship pure-Go builds, and validate platform integrations natively before expanding
the supported release targets.

## Local environment evidence

Planning inspection found Fedora Linux 44/Sway on AMD64, Go 1.26.8, and a running
GNOME Keyring Secret Service provider. No stored credentials were accessed in
planning or implementation. Browser login, keyring storage/lock state, actual
Slack scope coverage, and the workspace's rate tier remain unverified.
