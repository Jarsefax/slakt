## Interaction & Execution Policy
- Default to read-only exploration and planning.
- NEVER invoke write tools (file modification, patching, git commit, or state-changing bash commands) unless the user's prompt explicitly contains "PROCEED", "APPLY", or "IMPLEMENT".
- If the user asks a question, reports a bug, or suggests a change, provide a concrete plan/diff proposal only, then stop and wait for permission.