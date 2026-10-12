# agistry × Codex

Registers each top-level Codex session with agistry, keeps its task, role and
presence up to date, and delivers agistry mail to it as a queued follow-up
(`codex queue`).

**Requires** Bash, Python 3.9+, `curl`, `jq`, and a Codex CLI with `queue` and
`app-server proxy` (tested with 0.160.1), on Linux or macOS. Only sessions hosted by
the **local shared Codex daemon** are supported: not `--no-daemon`, remote App Server
endpoints, IDE-private servers or cloud sessions.

## Install

```bash
bash clients/codex/install.sh --url http://HOST:7070 --token TOKEN
```

1. Leave off `--url`/`--token` if `~/.config/agistry/client.env` already has them
   (e.g. from the Claude Code client); given one, only that key changes.
2. In Codex, run **`/hooks`** and trust the new hooks. Codex skips new or changed
   hooks until you do ([hook docs](https://learn.chatgpt.com/docs/hooks)).
3. Start or resume a session. It appears in the dashboard.

The installer is safe to re-run, backs up what it changes, and keeps other hooks. It
writes:

- `~/.codex/agistry/`: the hook entry point and the bridge (honors `CODEX_HOME`)
- `~/.codex/hooks.json`: `SessionStart` and `SessionEnd` handlers
- `~/.agents/skills/agistry/`: the `agistry` skill and its CLI, adapted for Codex
- `~/.config/agistry/providers/`: the host's [resource providers](../providers/README.md),
  shared with the Claude Code client

**Uninstall:** there is no script yet. Remove the `agistry-codex.sh` handlers from
`~/.codex/hooks.json`, then delete `~/.codex/agistry/` and `~/.agents/skills/agistry/`.

## Use

The agent joins and talks to others with the skill, as in Claude Code:

```bash
~/.agents/skills/agistry/agistry.sh join implementer TASK-42
~/.agents/skills/agistry/agistry.sh who TASK-42
~/.agents/skills/agistry/agistry.sh send TASK-42:reviewer "ready for review"
```

The CLI takes the session id from `AGISTRY_SESSION_ID`, then `CODEX_THREAD_ID`. If
neither is set, prefix commands with `AGISTRY_SESSION_ID=<id>` using the id the
SessionStart hook gave the agent. Subagents don't join and must not read their
parent's mailbox.

There is no status line label as in Claude Code (Codex's status line takes built-in
items only); the [tmux sidebar](../tmux/README.md) shows each session's state instead.

## How delivery works

The SessionStart hook records the session and starts a **bridge**, one per Codex home
(a lock prevents duplicates). The bridge connects to the Codex daemon through
`codex app-server proxy` and reads which threads are loaded. Only threads that are
registered here, loaded, and not ended are heartbeated and polled. A running daemon
doesn't prove a session is alive: a thread that unloads or ends is deregistered, and
if the daemon disconnects the bridge pauses and retries. agistry's TTL covers a
crashed bridge or host.

For each live session the bridge:

1. Re-sends its identity every heartbeat (`/assign`, or `/register` before it has
   joined), so it survives a registry restart or wipe.
2. Every poll, peeks at `/inbox?peek=1` and queues each message with
   `codex queue --thread <id>`. A busy turn gets it when it finishes; an idle thread
   can start on it right away.
3. Records a receipt, then `/ack`s.

Delivery is **at least once**: receipts stop most duplicates, but a crash between
Codex accepting a message and the receipt being saved can repeat one. Each message
carries a stable `msg_id`. "Delivered" on the dashboard means Codex accepted the
message, not that the work is done. A `task:role` conflict pauses delivery for that
session and leaves the same `.conflict` notice the CLI uses.

## Settings and troubleshooting

Optional, in `~/.config/agistry/client.env`:

| Variable | Default | |
| --- | --- | --- |
| `AGISTRY_HEARTBEAT_SEC` | `120` | Heartbeat interval |
| `AGISTRY_CODEX_POLL_SEC` | `4` | Inbox poll interval |
| `AGISTRY_CODEX_BIN` | `codex` | Codex CLI to use |
| `AGISTRY_STATE_DIR` | `~/.config/agistry/state` | Local state |

The bridge's files (`bridge.log`, lock, `receipts.sqlite` holding message ids only)
live in `~/.config/agistry/state/codex/<home-hash>/`. A crashed bridge restarts on
the next SessionStart. To watch it, run it in the foreground:

```bash
bash ~/.codex/agistry/agistry-codex.sh bridge
```

The installer also sets up the host's [resource providers](../providers/README.md) as
user services for whatever `AGISTRY_PROVIDERS` lists; they don't depend on sessions.

## Tests

```bash
python3 -m unittest discover -s clients/codex -p 'test_*.py' -v
node clients/codex/smoke-queue.mjs   # optional: real CLI against local fixtures (Node 22+)
```
