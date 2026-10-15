# Connect slakt to Slack

Use an internal app approved for your company workspace. Each user authorizes
their own account; slakt does not use a bot identity or the official Slack
client's cookies. No client secret or app-level token is required for this version.

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
scopes, Socket Mode configuration, app-level secret, or public event endpoint are
used. Slack's history rate limits depend on app classification: confirm the app
qualifies as an internal customer-built app before relying on 15-second polling.
Increase `--poll-interval` if needed; reads honor Slack's Retry-After response.

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

`slkt auth logout` removes the local credential entry. To revoke access on Slack's
side, remove the app authorization in Slack as well. A running operation may
already have an access token in flight when another process logs out.

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
