# slakt project plan

Status: planning. Updated: 2026-10-15.

This document records the agreed direction and the current proposal. Proposed
libraries, commands, keybindings, and implementation details may change as the
Slack access prototype establishes what works in the target workspace.

## Agreed decisions

- Project name: **slakt**.
- Only executable name: **`slkt`**.
- Purpose: a CLI and TUI client for the company's Slack workspace.
- First-version priority: daily use of channels, DMs, threads, and search.
- Language: Go.
- Favor pure-Go dependencies and maintain builds with `CGO_ENABLED=0`.
- Users should receive native binaries and should not need Go installed.
- The owner can install a custom Slack app or obtain approval for one.
- Record the plan and subsequent decisions in this repository.
- Initial audience: the owner, using one Slack account in one workspace on Linux.
- Eventual audience: colleagues on Linux, Windows, and macOS.
- The owner delegates the timing of broader platform and multi-user support to
  the implementer; the current recommendation is Linux and personal use first,
  with portable boundaries from the start.
- This chat is documentation-only. The owner will review and commit the plan,
  then explicitly trigger implementation in a new chat with their chosen model
  and reasoning level.

## First usable release

A user can follow a channel, answer a DM, reply in a thread, and find an older
message entirely through `slkt`.

Proposed scope:

- Browse joined channels, accessible private conversations, and DMs.
- Read paginated message history and open threads.
- Send messages, reply in threads, and add reactions.
- Search using Slack's query syntax.
- Receive live updates and recover from disconnections.
- Preserve the current view while new messages arrive.
- Open a message in Slack for content that needs a richer interface.

Defer calls, canvases, workflow interactions, and file uploads. Exact unread counts
and synchronization of Slack read positions require validation before becoming
release commitments.

## Proposed CLI contract

Running `slkt` without arguments opens the TUI when attached to an interactive
terminal. For non-interactive use, require a subcommand and provide a clear error
instead of attempting to start the TUI.

| Command | Purpose |
| --- | --- |
| `slkt auth login` | Authorize in the browser using OAuth with PKCE. |
| `slkt auth status` | Show workspace, identity, and authentication status. |
| `slkt auth logout` | Remove locally stored credentials. |
| `slkt channels` | List channels. |
| `slkt read '#engineering' --limit 30` | Read recent conversation messages. |
| `slkt dm @alice` | Open a DM in the TUI. |
| `slkt thread <message-url>` | Read a thread. |
| `slkt send '#engineering' 'Hello'` | Send a message. |
| `slkt reply <message-url> 'Looks good'` | Reply in a thread. |
| `slkt search 'from:alice' --json` | Search with structured output. |
| `slkt doctor` | Diagnose configuration, permissions, and connectivity. |

Use readable output by default and JSON on request for data-producing commands.
Send errors to stderr and define predictable exit codes. Resolve names to Slack
IDs and avoid guessing when names are ambiguous. Interactive commands can offer a
selection; scripts should receive an actionable error and be able to use IDs.

## Proposed TUI

Use a conversation sidebar, a central message timeline, and an optional thread
pane. On narrow terminals, show the thread in place of the timeline.

| Key | Proposed action |
| --- | --- |
| `Ctrl+k` | Jump to a channel or person. |
| Arrow keys or `j/k` | Navigate messages outside the composer. |
| `Enter` | Open the selected message's thread. |
| `i` | Enter the composer. |
| `Esc` | Return to navigation. |
| `Ctrl+s` | Send from the composer. |
| `/` | Search. |
| `?` | Show keybinding help. |

Support multiline drafts, an external editor, readable code blocks, mentions,
links, and text representations of attachments. Make the destination and thread
context visible while composing. Keep scrolling stable during incoming updates.
Validate keybindings in supported terminals before finalizing them.

## Proposed implementation

- Cobra for CLI commands.
- Bubble Tea for the TUI.
- The community-maintained slack-go client behind a small adapter.
- Shared conversation, search, and messaging services for both interfaces.
- One process with no background daemon for the first version.
- OS credential storage for tokens, with preferences stored separately.
- Bounded message caching; persistent message history is not an initial promise.

Review dependencies before adoption, including their transitive dependencies and
platform support. Build release targets with `CGO_ENABLED=0` and smoke-test the
resulting binaries. Credential storage must also fit this build strategy; required
OS credential services should be documented. A pure-Go build does not remove the
need to validate platform integration.

### Platform and audience rollout

Recommend supporting Linux for the owner first. The development machine is Linux
AMD64; that is the first build and smoke-test target. Additional architectures
and Windows/macOS releases follow when their integrations can be verified.

Keep browser launching, config paths, credential storage, and terminal handling
behind small platform-aware boundaries. Use each user's own OAuth authorization
and local credentials. Do not hard-code the owner's Slack identity or workspace
throughout the application. The first release can still select only one account
and workspace at a time.

This allows later colleague adoption without introducing a shared backend or
multi-tenant service now. Broader release support requires native smoke tests for
login, credential storage, terminal input, resizing, and packaging on each OS;
cross-compiling alone is not proof of support.

Socket Mode delivery is a separate growth decision: multiple connections to one
Slack app receive distributed events, not a broadcast copy to each client.
Resolve event routing before offering shared-app live events to colleagues. Do
not embed a shared app-level token in distributed binaries. For the personal
prototype, evaluate one locally configured connection. A polling-based fallback
can be considered if event access or setup blocks progress; document the actual
refresh behavior rather than presenting it as a validated live-event feed.

## Slack integration

Use supported Slack APIs and a user token so operations follow the authenticated
user's identity and authorized access. Slack's `search.messages` method requires
a user token. Determine the minimum scopes from the methods actually used.

Evaluate Socket Mode for live events without a public HTTP endpoint. It uses a
separate app-level token and event subscriptions. Verify actual channel, private
conversation, DM, and thread event coverage in the target workspace.

### Proposed authentication flow

Prefer browser-based OAuth with PKCE and a temporary localhost callback. Slack
documents this for public desktop clients without an embedded client secret or a
backend server. Request user scopes only in this flow. Store credentials in the
OS credential store and handle token refresh. PKCE refresh tokens expire after
30 days; handle expiry by requesting authorization again. Enabling PKCE requires
Slack support to reverse. Validate this configuration in the access milestone.

The proposed user experience is:

1. Run `slkt auth login`.
2. Generate a random verifier and state value in memory, derive the verifier's
   SHA-256 challenge, and start a short-lived loopback-only callback listener.
3. Open the Slack authorization page in the user's browser with the challenge,
   requested user scopes, state, public client ID, and registered callback URL.
4. Let Slack handle sign-in and the user's approval. Validate callback state and
   exchange the resulting authorization code with the original verifier.
5. Validate the resulting workspace and identity, save the credentials in the OS
   credential store, and stop the callback listener.

The verifier binds the authorization code to the initiating login attempt. An
intercepted code alone is insufficient to complete that exchange. No permanent
client secret is shipped inside the executable.

An existing browser Slack session can simplify authorization, but the user must
still grant the app access. A login in the official desktop client does not
guarantee a browser login. Do not depend on extracting the official client's
cookies or local session credentials; Slack's documented app flow issues separate
tokens. The user should not have to supply their Slack password to `slkt`.

`slkt auth status` should show identity and workspace without revealing secrets.
`slkt auth logout` removes local credentials; distinguish this from revoking the
app's authorization in Slack. The exact callback registration, refresh lifecycle,
and credential-storage implementation remain to be validated. Socket Mode setup
needs its own app-level credential and is separate from this user login flow.

### Installation requirements and observed environment

For the proposed local login flow, a user needs the `slkt` binary, a browser,
network access to Slack, an authorized company Slack account, and an available
OS credential service for persistent token storage. No Go installation, separate
web server, or official Slack client is required. The callback listener is part
of `slkt` and exists only during login. Browser and CLI are assumed to run on the
same machine; remote SSH and headless login are deferred.

The Slack app must be configured once with PKCE, a registered callback URL, and
the required user scopes. App installation/approval and each user's authorization
are distinct steps. The app's public client ID is configuration, not a secret.
The exact app identity, workspace ID, callback URL/port, and scope set have not
been supplied or validated yet. Do not request tokens or secrets in chat.

On 2026-10-15, read-only inspection confirmed Fedora Linux 44 (Sway) on the owner's
machine, GNOME Keyring and libsecret installed, and GNOME Keyring running as the
session's `org.freedesktop.secrets` provider. No stored credentials were read or
written. The service's existence is verified; an application-level store/read
test and lock/unlock behavior remain untested. This is local environment evidence,
not a requirement to use GNOME on every Linux installation.

Respect pagination and rate limits, including `Retry-After`. Slack currently
documents Tier 3 history limits for internal customer-built apps; verify that the
installed app qualifies. Reconcile history after reconnecting and deduplicate
retried events. Avoid blind retries of message sends when the result is uncertain.

Keep tokens and message bodies out of routine logs. Render message content safely
for a terminal, including stripping untrusted terminal control sequences.

## Milestones

1. **Prove workspace access.** Authenticate; list conversations; read a private
   channel and a DM; retrieve a thread; search; send an explicitly requested test
   message; verify live events. Record the working scopes and app configuration.
2. **Build the CLI foundation.** Implement pagination, name resolution, JSON
   output, useful errors, cancellation, and rate-limit handling.
3. **Build the daily-use TUI.** Implement navigation, timeline, threads, composer,
   search, and reactions using the same services as the CLI.
4. **Make it reliable and distributable.** Verify reconnection, history gap
   recovery, event deduplication, draft recovery, resizing, and release packaging.

Use focused tests for API error handling, pagination, event reconciliation, and
message-send ambiguity. Validate terminal behavior interactively and validate
authentication and event coverage against the actual workspace.

## Open decisions

- Additional release architectures and timing of Windows/macOS support.
- Validate PKCE login, callback registration, refresh behavior, and minimum scopes.
- Credential storage behavior on systems without an available credential service.
- Library versions after checking pure-Go builds and required API coverage.
- Read-state synchronization and the accuracy of unread indicators.
- Draft persistence and local cache retention.
- Event-routing design for colleagues using a shared Slack app, including whether
  Socket Mode remains appropriate beyond personal use.

## Implementation status and handoff

No application code, Go module, dependency lockfile, build configuration, or
executable has been created. No dependencies have been downloaded for this
project. No Slack app has been created or configured through this chat, and no
workspace authentication, message reads, or message sends have been performed.
All feature descriptions above are plans rather than verified product behavior.

Review and commit these documents before implementation. See the
[implementation handoff](implementation-handoff.md) for a self-contained starting
point for the next chat. Preserve these decisions and update this document when
actual workspace evidence changes a proposal.

## References

- [Slack token types](https://docs.slack.dev/authentication/tokens/)
- [OAuth authorization](https://docs.slack.dev/authentication/installing-with-oauth/)
- [Native-client OAuth with PKCE](https://docs.slack.dev/authentication/using-pkce/)
- [Message search](https://docs.slack.dev/reference/methods/search.messages/)
- [Conversation history and rate limits](https://docs.slack.dev/reference/methods/conversations.history/)
- [Socket Mode](https://docs.slack.dev/apis/events-api/using-socket-mode/)
- [Conversation read positions](https://docs.slack.dev/reference/methods/conversations.mark/)
- [Cobra](https://github.com/spf13/cobra)
- [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- [slack-go](https://github.com/slack-go/slack)
