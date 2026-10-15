# slakt

slakt is a planned terminal client for the company's Slack workspace. Its only
executable command is **`slkt`**.

Run `slkt` without arguments to open the interactive terminal UI (TUI). Use
subcommands for quick actions and scripting from the command line (CLI).

The first version focuses on daily Slack use: channels, DMs, threads, and search.
The project is currently in planning; the commands below are proposed behavior,
not implemented features.

## Agreed direction

- Build in Go and favor pure-Go dependencies.
- Distribute native binaries so users do not need Go installed.
- Keep builds working with `CGO_ENABLED=0`; evaluate dependency choices against
  this requirement.
- Provide both CLI and TUI interfaces through `slkt`.
- Use a custom Slack app, which the owner can install or obtain approval for.
- Start with the owner's account in one workspace on Linux.
- Preserve a path to colleagues on Linux, Windows, and macOS without requiring
  those platforms or shared services in the first release.

## Current status

This repository contains planning documentation only. No implementation,
dependency downloads, Slack app setup, or workspace API tests have been performed.
The owner will explicitly start implementation in a new chat after reviewing and
committing the documentation.

The [implementation handoff](docs/implementation-handoff.md) provides the context
and a suggested starting prompt for that chat.

## Proposed usage

```sh
slkt                                  # Open the TUI
slkt auth login
slkt auth status
slkt auth logout
slkt channels
slkt read '#engineering' --limit 30
slkt dm @alice                         # Open this DM in the TUI
slkt thread <message-url>
slkt send '#engineering' 'Hello'
slkt reply <message-url> 'Looks good'
slkt search 'in:engineering deploy after:2026-10-01'
slkt search 'from:alice' --json
slkt doctor
```

See [the project plan](docs/plan.md) for scope, architecture, milestones, and open
questions. Keep that document updated as decisions are made.
