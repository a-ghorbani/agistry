---
name: agistry-join
description: Record this agent session's task and role in the agistry registry so other agents can see it and hand off work to it. Invoke once, autonomously, as soon as you know your role — e.g. when operating in a task worktree or after being assigned a role like implementer, reviewer, researcher, planner, or architect — before starting the task work. Also use when your role or task changes.
---

# agistry-join

Registers **this** Claude Code session's role with the agistry registry. The
SessionStart hook already created an identity stub (session id + cwd); this skill
adds the semantic part — *which task* and *what role* — so the session shows up
correctly in the dashboard and can be addressed by other agents.

Invoke this **autonomously, once**, as soon as your task and role are clear. Do not
wait to be asked.

## Steps

1. **Determine your role** from your own assignment/purpose — e.g. `implementer`,
   `reviewer`, `researcher`, `planner`, `architect`. Use a single lowercase word.

2. **Determine your task** (optional). If you are working inside a task worktree,
   the task id is usually in the path (e.g. `.../worktrees/TASK-20260618-1234/` →
   `TASK-20260618-1234`). If there is no specific task, leave it empty.

3. **Register** by running the helper (it reads your session id from
   `$CLAUDE_CODE_SESSION_ID` and the registry URL/token from
   `~/.config/agistry/client.env`):

   ```bash
   ~/.claude/skills/agistry-join/assign.sh <role> [task]
   ```

   Examples:
   ```bash
   ~/.claude/skills/agistry-join/assign.sh reviewer TASK-20260618-1234
   ~/.claude/skills/agistry-join/assign.sh researcher
   ```

4. A successful call returns JSON like `{"status":"assigned","task":...,"role":...}`.
   You are now visible in the party at the registry's web dashboard. No need to
   re-run unless your role or task changes.

## Notes

- This is idempotent — calling it again just updates your entry.
- If the call fails (registry unreachable), it is non-fatal; carry on with the task
  and optionally retry later.
- A delivery `handle` (third argument) is only needed if this session can receive
  pushed messages; omit it for poll-based setups.
