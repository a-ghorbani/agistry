# Codex client

Registers independent top-level Codex sessions, reconciles their task/role and
presence, and forwards Agistry mail through `codex queue`. The Go server and the
existing resource providers need no changes.

Requires Bash, Python 3.9+, `curl`, `jq`, and a Codex CLI exposing `queue` and
`app-server proxy` (tested with 0.160.1). Supports Linux/macOS and sessions hosted
by the **local shared Codex daemon**. Sessions launched with `--no-daemon`, remote
App Server endpoints, IDE-private servers, and cloud sessions are outside this
adapter's delivery path. The bridge does not create or resume threads itself.

## Install

```bash
bash clients/codex/install.sh --url http://YOUR_HOST:7070 --token YOUR_TOKEN
```

Omit URL/token if `~/.config/agistry/client.env` already contains them. Supplied
options update only those keys, preserving providers and other settings. The
installer backs up existing configuration, preserves unrelated hook handlers,
and is safe to repeat. It installs:

- `~/.codex/agistry/`: hook entry point and Python bridge (honors `CODEX_HOME`).
- `~/.codex/hooks.json`: `SessionStart` / `SessionEnd` handlers.
- `~/.agents/skills/agistry/`: the existing skill adapted for Codex and the shared CLI.

In Codex, open **`/hooks`** and review/trust the installed hooks; Codex skips new
or changed hooks until trusted. Start/resume a session after the hooks are loaded.
See [official hook documentation](https://learn.chatgpt.com/docs/hooks).

The SessionStart hook records the session and starts one bridge per local Codex
home, using a process lock to prevent concurrent consumers. The agent declares
its role with the skill, just as in Claude Code:

```bash
~/.agents/skills/agistry/agistry.sh join implementer TASK-42
~/.agents/skills/agistry/agistry.sh who TASK-42
~/.agents/skills/agistry/agistry.sh send TASK-42:reviewer "ready for review"
```

The CLI uses `AGISTRY_SESSION_ID`, then `CODEX_THREAD_ID`, then
`CLAUDE_CODE_SESSION_ID`. When the Codex thread environment variable is absent,
use the explicit session ID supplied by the SessionStart hook. Subagents must not
join or consume a parent's mailbox.

## Delivery and recovery

The bridge performs a WebSocket handshake over `codex app-server proxy` (a raw
socket tunnel, rather than JSONL stdio) and reads the daemon's loaded
thread IDs. Only locally registered, loaded, non-ended threads are heartbeaten
or polled. **A living daemon alone does not prove a session is alive.** A thread
that unloads or ends is deregistered; when the daemon disconnects, the bridge
stops polling/heartbeating and retries with backoff. Agistry's TTL covers a
bridge/host crash. Closing a terminal does not necessarily end its daemon-hosted
thread immediately; Codex's loaded-thread lifetime is authoritative.

For each eligible session:

1. Replay desired identity from the shared local state (`/assign` or `/register`).
2. Peek at `/inbox?peek=1`, preserving pending mail on failures.
3. Invoke `codex queue --thread <UUID> --message <fenced peer message>`.
4. After a successful exit, persist a receipt, then acknowledge `/ack`.

Busy turns receive queued follow-ups after they finish. Idle threads can dispatch
them immediately. This adapter does **not** implement `turn/steer`.

Receipts prevent duplicate enqueueing after an acknowledgement failure or bridge
restart. A crash after Codex accepts a message but before the receipt commits can
still produce a duplicate; each message carries a stable `msg_id` for the agent
to recognize. Delivery is **at least once**, not exactly once. A dashboard
"delivered" mark means Codex accepted the input, not that the agent completed
the requested work. Codex owns accepted queue items; its downstream turn failures
are not Agistry acknowledgements of completed work.

Task/role conflicts create the same `.conflict` notice used by the shared CLI.
The bridge pauses delivery for that session until identity reconciliation succeeds.
After an Agistry restart/wipe, desired identity is replayed on the next heartbeat.

Runtime files live beneath `~/.config/agistry/state/codex/<home-hash>/`:
session lifecycle markers, `bridge.log`, a lock, and `receipts.sqlite` (IDs only,
no message bodies). The bridge runs detached, retries daemon outages, and starts
again on the next SessionStart if it crashes. Run it in the foreground to diagnose:

```bash
bash ~/.codex/agistry/agistry-codex.sh bridge
```

`AGISTRY_STATE_DIR`, `AGISTRY_HEARTBEAT_SEC` (120), `AGISTRY_CODEX_POLL_SEC` (4),
and `AGISTRY_CODEX_BIN` can be set in the shared client config. Leave resource
provider supervision with the existing host providers; no second device discovery
daemon is started by this adapter.

## Validate

```bash
python3 -m unittest discover -s clients/codex -p 'test_*.py' -v
# Optional real CLI smoke test (Node 22+, local fixture servers, no external model):
node clients/codex/smoke-queue.mjs
```
