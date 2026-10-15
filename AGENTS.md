## Interaction & Execution Policy
- Default to read-only exploration and planning.
- NEVER invoke write tools (file modification, patching, git commit, or state-changing bash commands) unless the user's prompt explicitly contains "PROCEED", "APPLY", or "IMPLEMENT".
- If the user asks a question, reports a bug, or suggests a change, provide a concrete plan/diff proposal only, then stop and wait for permission.

## Versioning and delivery
- Every completed application update delivered to the user needs a new version. Intermediate edits, test builds, documentation-only changes, and test-only changes do not.
- Before 1.0, use a patch bump for compatible fixes and a minor bump for new features or compatibility changes (including renamed or removed CLI options).
- Finish application/build changes, then run `make prepare-update BUMP=patch` (or `BUMP=minor`). This updates `cmd/slkt/VERSION` and adds an immutable source record under `releases/`.
- Never edit an existing release record to reuse a delivered version. If source changes after preparation, prepare another version before installation or release.
- Include the version file and its source record in the same commit as the application update. Preserve changes from other chats unless the user authorizes incorporating them.
- Install with `./install.sh` and package with `make release`; both must pass the version check. `make build` is for development and does not mark an update as delivered.
- After installing, verify both `slkt --version` and `slkt -V` and report the installed version. Do not claim an update is delivered based only on source changes or a build.
