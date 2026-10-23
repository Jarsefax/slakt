# Connect slakt to Slack

Use an internal app approved for your company workspace. Each user authorizes
their own account; slakt does not use a bot identity or the official Slack
client's cookies. User login uses PKCE without a client secret. Optional Socket
Mode stores its app-level credentials in the OS keyring; rotating app-level
tokens also require the app client secret for their refresh grant.

## Configure the app

1. In [Slack app management](https://api.slack.com/apps), create an app from
   [slack-app-manifest.json](slack-app-manifest.json) for your workspace, or apply
   its configuration to an approved app. Workspace policy may require approval.
2. Under OAuth & Permissions, verify **PKCE** and **token rotation** are enabled.
   Slack requires contacting support to reverse PKCE enablement. The manifest
   is configuration only; this repository has not installed an app.
3. Register exactly `http://localhost:8765/callback` as the redirect URL. If you
   choose another port, pass the matching URL with `--redirect-url` on login.
   The listener binds only to `127.0.0.1`, runs for at most five minutes, and
   checks state and callback host/path. Browser and CLI must share a machine.
4. Find the **public Client ID** in Basic Information. Keep the client secret
   out of slakt, shell command arguments, the repository, and chat.
5. Install/approve the app as required by workspace policy. Per-user OAuth
   consent is a separate step. Only request the user scopes listed below.

```sh
./bin/slkt auth login --client-id YOUR_PUBLIC_CLIENT_ID
./bin/slkt auth status
./bin/slkt doctor
```

If automatic browser opening fails, use the URL printed in your local terminal.
Do not share the authorization URL. Login validates the workspace/account before
saving credentials, replacing the previous single active account. Cancel with
Ctrl+c. A port conflict is an error; choose and register another localhost port.

## User scopes and API calls

| User scopes | Used for |
| --- | --- |
| `channels:read`, `groups:read`, `im:read`, `mpim:read` | `users.conversations`: joined public/private conversations and DMs |
| `channels:history`, `groups:history`, `im:history`, `mpim:history` | `conversations.history`, `conversations.replies` |
| `users:read` | `users.list`: names, mentions, and unambiguous people lookup |
| `im:write` | `conversations.open`: open a DM |
| `chat:write` | `chat.postMessage`: send/reply as the authorizing user |
| `search:read` | `search.messages`: Slack search |
| `reactions:write` | `reactions.add` |

`auth.test` checks identity; `chat.getPermalink` opens a selected message in Slack.
Actual method/scope coverage must be verified against the installed app. Missing
scopes produce an actionable error; update the app and authorize again. No bot
scopes or public event endpoint are required. Slack's history rate limits depend
on app classification: confirm the app qualifies as an internal customer-built
app before relying on polling.
Increase `--poll-interval` if needed; reads honor Slack's Retry-After response.

The conversation directory groups public/private channels, DMs, and group DMs
using conversation type flags. When `users.conversations` includes `is_starred`,
the conversation appears in Starred instead of its type section. This limited
response may omit starred metadata; slakt does not fetch every conversation's
details to fill it in. No additional scope is needed. Custom Slack sidebar
sections are not exposed by the documented Web API, and the deprecated
`stars.list` saved-items API is not used.

## Receive live updates with Socket Mode

1. Apply the updated manifest, or enable **Socket Mode** and **Event Subscriptions**
   in app management. Subscribe **on behalf of users** (workspace/user events) to
   `message.channels`, `message.groups`, `message.im`, and `message.mpim`.
   The existing history user scopes cover these message subscriptions. Reauthorize
   if Slack requests it. Bot subscriptions alone will not cover your personal DMs.
2. Under **Basic Information → App-Level Tokens**, generate an app-level token
   with **connections:write** for the same Slack app as your user login. Both
   `xapp-` and rotating `xoxe.xapp-` access tokens are supported. For a rotating
   token, copy its accompanying `xoxe-` refresh token when Slack displays it;
   the values are only shown once. Also have the **Client Secret** from Basic
   Information → App Credentials ready. Keep all three values private.
3. Run `slkt auth socket` and paste the access token at its hidden prompt. For
   a rotating token, it then asks for the app-level refresh token and app client
   secret, also hidden. This stores the supplied pair in the keyring, immediately
   refreshes it to establish a known expiry, saves the replacement pair, and checks
   `apps.connections.open`. It also supports an already expired access token when
   the accompanying refresh token is still valid. For a non-rotating token, the
   socket check happens before storage. Setup does not verify event subscriptions;
   verify those in app management. Never pass secrets as shell arguments.
4. Restart `slkt`. The footer shows connection state or polling fallback. Verify
   a new message in an inactive public channel, private channel, DM, and group DM
   updates unread state and notifications without waiting for a scan. Verify a
   thread reply, edit, deletion, and network disconnect/reconnect as well.

Use `slkt --poll-only` for a polling session, or `slkt auth socket --remove` to
remove this account's stored app-level credential. Restart a running TUI after
changing credentials. `slkt doctor` reports whether a credential is configured;
it does not refresh app tokens, open a socket, or verify subscription coverage.

For automated input, provide one secret per line on stdin: access token alone for
`xapp-`, or access token, refresh token, and client secret for `xoxe.xapp-`, in that
order. Use a secure credential source; do not place literal secrets in shell history.

Rotating credentials are stored together as JSON in the account-scoped keyring
entry. While connected, slkt checks them once a minute and refreshes within five
minutes of expiry. Each refresh uses the latest single-use refresh token and saves
both replacements before exposing the access token. Cross-process authentication
locking serializes refresh grants. The separate stream lock stays stable when
tokens rotate, and reconnects reload the current access token from the keyring.
Existing non-rotating credentials remain usable.

If a refresh response is lost, invalid, or cannot be saved, slkt retains a marker
in the keyring and does not replay the possibly consumed refresh token, even
after restart. Generate a fresh app-level access/refresh pair and rerun
`slkt auth socket` after unlocking the keyring or restoring connectivity. It falls
back to polling until valid credentials are configured and the TUI is restarted.
A successful refresh followed by a failed socket check leaves the new credentials
stored; fix the Socket Mode settings or connectivity and restart slkt.

Events follow the authorized users' scopes and conversation membership; Socket
Mode does not grant access to every conversation in a workspace. The client
filters workspace and conversation IDs, acknowledges envelopes, deduplicates
recent event IDs, reconnects automatically, and never logs event bodies or tokens.
New conversations trigger a directory refresh before applying their events.
Ordinary replies update the thread and parent's reply count without adding a
main timeline row. They can trigger notifications but do not advance the main
conversation's unread position. Broadcast replies also update the main timeline.
Edits/deletions do not generate new-message alerts. Reaction events, if separately
subscribed with their required scopes, refresh the open conversation.

While connected, the open view reconciles at least every two minutes (or the
configured interval when longer), and background history checks run at least
30 seconds apart. On connection failure, the ordinary polling schedule resumes;
a failed connection cannot erase cached messages or unread positions. Opening
and reconnecting also reconcile the open view. The background scan catches
messages received while offline; large workspaces still take time to finish a
scan. Push delivery does not provide Slack read-position synchronization.

**One independent client per Slack app:** Slack distributes events among an
app's connections; it does not broadcast each event to every slkt instance.
A local lock prevents two instances using the same app token from splitting
updates on this machine; the second instance polls until the lock is released.
Different tokens for the same app still share its stream. Use a dedicated Slack
app for each independent user/client across machines, or deploy a shared relay
that routes events to clients. A shared relay is not included in slkt.

## Credential service

On Linux, run an unlocked Secret Service provider in the desktop session (GNOME
Keyring is one example) and make the session D-Bus available. slakt stores one JSON
credential record under service `slakt`, account `default`; it includes access and
refresh tokens, expiry, public client ID, workspace ID, and user ID. Preferences
are flags/environment variables; tokens never go in a config file.

Refreshes occur shortly before expiry and persist the rotated refresh token
before returning the access token. A per-user lock prevents concurrent slkt
processes from racing refresh. Slack PKCE refresh tokens expire after 30 days;
authorization must be repeated when Slack rejects refresh. Storage failures do
not silently fall back to a token file. If a refresh succeeds but saving fails,
unlock the credential service and authorize again.

`slkt auth logout` removes the active account's local user and Socket Mode
credential entries. Socket credentials for other previously authorized accounts
remain scoped to those accounts; remove them before switching accounts if needed.
To revoke access on Slack's side, remove the app authorization in Slack as well.
Close running TUI sessions when logging out: an already established Socket Mode
connection checks stored credentials once a minute and closes when its credential
is removed. Close it explicitly to stop immediately. A running
operation may also have a user access token in flight.

## Owner acceptance checklist

No real workspace checks have been performed for the initial implementation.
These checks require the owner's app and account. Credentials stay on the machine.

- Authorize, confirm `auth status` identifies the expected workspace/user, and
  confirm login/logout work with the real keyring. Try the locked-keyring case.
- List conversations; read a joined private channel and a DM. Test a second
  history/thread page using the returned cursor. No content should be logged.
- Search for a known message and open a result/thread in the TUI. Press `o` to
  verify browser integration. Confirm the app's scopes cover each method.
- Agree on a test destination and exact message first. Then send a message,
  reply, and react using the CLI/TUI, and verify the author's identity in Slack.
- Leave a session open through a token refresh, and reauthorize after expiry or
  revocation. Do not edit token values by hand to simulate rotation.
- Resize the TUI, switch conversations with unsent drafts, use an external editor,
  and disconnect/reconnect the network. Verify refresh recovers missing pages,
  preserves selected messages, and shows failures without dropping drafts.

## References

- [Slack desktop PKCE and refresh](https://docs.slack.dev/authentication/using-pkce/)
- [Conversation history and app rate tiers](https://docs.slack.dev/reference/methods/conversations.history/)
- [User conversations](https://docs.slack.dev/reference/methods/users.conversations/)
- [Message search](https://docs.slack.dev/reference/methods/search.messages/)

- [Socket Mode setup and connection behavior](https://docs.slack.dev/apis/events-api/using-socket-mode/)
- [User and bot event subscriptions](https://docs.slack.dev/apis/events-api/)

- [Rotating app-level refresh grants](https://docs.slack.dev/authentication/using-token-rotation/)
