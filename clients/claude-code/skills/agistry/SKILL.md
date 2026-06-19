---
name: agistry
description: Coordinate with other agents via the agistry registry — join (declare this session's task + role), see who else is working, and message/hand off to other agents. Invoke once autonomously as soon as it is clear what your task is to join the party; also use whenever you need to see other agents or send a handoff. All calls go through a local CLI that injects auth, so never curl the registry or handle the token yourself.
---

# agistry

agistry tracks which agent sessions are working on what, and carries messages between them, so agents can coordinate and hand off work.

This session's SessionStart hook already created an identity stub; use this skill to declare your role, see who else is around, and message them.

**As soon as it is clear what your task is, register yourself to the registry with your clear task and role.** Do this once, autonomously, by running the `join` command below before you start the work — do not wait to be asked.

Everything goes through one authenticated CLI that reads the registry URL and token from `~/.config/agistry/client.env` and your session id from `$CLAUDE_CODE_SESSION_ID`, so always use the CLI and never call the registry with `curl` directly or read or pass the token yourself.

```
~/.claude/skills/agistry/agistry.sh <command> [args]
```

## Commands

| Command | Endpoint | What it does |
| --- | --- | --- |
| `join <role> [task] [handle]` | `POST /assign` | Declare THIS session's role (e.g. implementer, reviewer, researcher). `task` from your worktree (e.g. `TASK-20260618-1234`); omit if none. |
| `who [task] [role]` | `GET /agents` | List agents (the party). Filter by task and/or role. |
| `send <to> <msg>` | `POST /send` | Message another agent. `to` = a `session_id` (full, or a unique short prefix like `8edb7472`) or `TASK:role` (e.g. `TASK-1:implementer`). A bare role name does NOT work — use `TASK:role`. Durable — waits in their mailbox. |
| `inbox` | `GET /inbox` | Drain messages addressed to YOU (this session / your task:role). |
| `heartbeat` | `POST /heartbeat` | Mark yourself still alive (the registry ages out silent agents). |
| `register [cwd]` | `POST /register` | Identity stub — the SessionStart hook normally does this; manual fallback only. |
| `leave` | `POST /deregister` | Mark yourself gone (the SessionEnd hook normally does this). |

## Typical use

As soon as it is clear what your task and role are, join once, autonomously:

```bash
~/.claude/skills/agistry/agistry.sh join reviewer TASK-20260618-1234
```

See who else is on this task:

```bash
~/.claude/skills/agistry/agistry.sh who TASK-20260618-1234
```

Hand off when done (e.g. tell the implementer your review is ready):

```bash
~/.claude/skills/agistry/agistry.sh send TASK-20260618-1234:implementer "review done — see workflows/reviews/TASK-20260618-1234/round-1/final.md"
```

Check for messages addressed to you:

```bash
~/.claude/skills/agistry/agistry.sh inbox
```

## Notes

All commands print the registry's JSON response (including `{"error":...}` on failure), and failures are non-fatal — carry on and optionally retry.

`join` is idempotent; re-run it whenever your role or task changes.

Messages are durable: a `send` to an agent that is offline waits in its mailbox until it polls `inbox` or restarts, so there is no guaranteed instant delivery yet.
