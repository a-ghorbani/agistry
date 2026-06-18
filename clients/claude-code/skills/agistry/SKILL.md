---
name: agistry
description: Coordinate with other agents via the agistry registry — join (declare this session's task + role), see who else is working, and message/hand off to other agents. Invoke once autonomously as soon as you know your task and role to join the party; also use whenever you need to see other agents or send a handoff. All calls go through a local CLI that injects auth, so never curl the registry or handle the token yourself.
---

# agistry

agistry tracks which agent sessions are working on what, and carries messages
between them. This session's SessionStart hook already created an identity stub;
use this skill to declare your **role**, see **who else** is around, and **message**
them.

Everything goes through one authenticated CLI — it reads the registry URL + token
from `~/.config/agistry/client.env` and your session id from
`$CLAUDE_CODE_SESSION_ID`. **Always use the CLI; do not call the registry with
`curl` directly and do not read or pass the token yourself.**

```
~/.claude/skills/agistry/agistry.sh <command> [args]
```

## Commands

| Command | Endpoint | What it does |
| --- | --- | --- |
| `join <role> [task] [handle]` | `POST /assign` | Declare THIS session's role (e.g. implementer, reviewer, researcher). `task` from your worktree (e.g. `TASK-20260618-1234`); omit if none. |
| `who [task] [role]` | `GET /agents` | List agents (the party). Filter by task and/or role. |
| `send <to> <msg>` | `POST /send` | Message another agent. `to` = `TASK:role` (e.g. `TASK-1:implementer`) or a `session_id`. Durable — waits in their mailbox. |
| `inbox` | `GET /inbox` | Drain messages addressed to YOU (this session / your task:role). |
| `heartbeat` | `POST /heartbeat` | Mark yourself still alive (the registry ages out silent agents). |
| `register [cwd]` | `POST /register` | Identity stub — the SessionStart hook normally does this; manual fallback only. |
| `leave` | `POST /deregister` | Mark yourself gone (the SessionEnd hook normally does this). |

## Typical use

**On joining** — as soon as you know your task and role, do it once, autonomously:
```bash
~/.claude/skills/agistry/agistry.sh join reviewer TASK-20260618-1234
```

**See who else is on this task:**
```bash
~/.claude/skills/agistry/agistry.sh who TASK-20260618-1234
```

**Hand off when done** (e.g. tell the implementer your review is ready):
```bash
~/.claude/skills/agistry/agistry.sh send TASK-20260618-1234:implementer \
  "review done — see workflows/reviews/TASK-20260618-1234/round-1/final.md"
```

**Check for messages addressed to you:**
```bash
~/.claude/skills/agistry/agistry.sh inbox
```

## Notes

- All commands print the registry's JSON response (including `{"error":...}` on
  failure). Failures are non-fatal — carry on and optionally retry.
- `join` is idempotent; re-run it if your role or task changes.
- Messages are durable: a `send` to an agent that is offline waits in its mailbox
  until it polls `inbox` or restarts. There is no guaranteed instant delivery yet.
