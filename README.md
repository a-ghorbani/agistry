# agistry

A lightweight, fault-tolerant **registry + mailbox** for coordinating agent
processes — for example, multiple [Claude Code](https://claude.com/claude-code)
instances. It answers *who is working on which task, in which role*, and gives
them a durable mailbox to hand off to each other. Single Go binary, SQLite for
state, an embedded web dashboard.

```
┌── agent A ──┐   register / assign / heartbeat        ┌─────────────┐
│ session_id  │ ────────────────────────────────────▶  │             │
│ task + role │   send → mailbox                       │   agistry   │  ◀── browser: /
└─────────────┘ ◀────────────────────────────────────  │  (registry  │      "who's in
┌── agent B ──┐   inbox (drain)                        │  + mailbox) │       the party"
└─────────────┘                                        └─────────────┘
```

## Why

Coordinating several long-lived agents needs three things: a place to **register**
presence, a way to see **who's doing what**, and a **durable channel** to pass
work between them. agistry is a single small service that does all three, designed
to live on one host on a trusted network.

## What "fault tolerant" means here

Single-node, not HA. It survives **crashes and reboots** and **self-heals dead
entries**:

- **Durable state** — SQLite in WAL mode. Survives `kill -9` and reboot.
- **Auto-restart** — run under systemd with `Restart=always`.
- **Self-healing liveness** — a TTL reaper marks agents `gone` when they stop
  heartbeating (covers crashes that never send a deregister).

If the host dies, coordination is down. No clustering, no replication.

## Build

Needs Go 1.25+. The binary is CGO-free (pure-Go `modernc.org/sqlite`), so it
static-links and cross-compiles cleanly.

```bash
make build        # -> ./agistry
make test
```

No Go on the target box? `./build.sh` bootstraps a local Go SDK under `.gosdk/`
(no sudo), runs the tests, and builds.

## Run

```bash
REGISTRY_TOKEN=dev ./agistry
# open http://127.0.0.1:7070/  and paste the token
```

### Configuration

| Env var | Default | Meaning |
| --- | --- | --- |
| `REGISTRY_ADDR` | `:7070` | Listen address; prefer a specific LAN IP |
| `REGISTRY_DB` | `registry.db` | SQLite file path |
| `REGISTRY_TOKEN` | _(unset = auth off)_ | Shared bearer token |
| `REGISTRY_TTL_SECONDS` | `600` | Idle seconds before an agent is marked `gone` |

## Deploy (systemd)

```bash
sudo useradd -r -s /usr/sbin/nologin agistry || true
sudo mkdir -p /opt/agistry
sudo cp agistry /opt/agistry/
sudo cp deploy/agistry.env.example /opt/agistry/agistry.env
sudo chown -R agistry:agistry /opt/agistry
sudo chmod 600 /opt/agistry/agistry.env     # set a real REGISTRY_TOKEN + REGISTRY_ADDR

sudo cp deploy/agistry.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now agistry
curl -s http://<host>:7070/healthz           # -> ok
```

## Security

- **Always set `REGISTRY_TOKEN`** for any networked deployment. Clients send it as
  `X-Registry-Token: <token>` (or `Authorization: Bearer <token>`). The token is
  compared in constant time.
- **Bind to a private interface** and firewall it. agistry is built for a trusted
  LAN — do not expose it to the internet.
- The web dashboard *shell* is unauthenticated (it holds no secrets); the `/agents`
  data it fetches is token-protected.

## API

All POST bodies are JSON (≤ 1 MiB). Auth header required when `REGISTRY_TOKEN` is set.

| Method | Path | Body / query | Purpose |
| --- | --- | --- | --- |
| POST | `/register` | `{session_id, cwd, host}` | Identity stub. Idempotent; never clobbers role. |
| POST | `/assign` | `{session_id, task, role, handle, notify}` | Set role/task + delivery handle. Upsert. |
| POST | `/heartbeat` | `{session_id}` | Bump liveness; revives a `gone` entry. |
| POST | `/deregister` | `{session_id}` | Mark `gone`. |
| GET | `/agents` | `?task=&role=&state=&all=1` | Who's doing what. Hides `gone` unless `all=1`. |
| POST | `/send` | `{to\|task,role, from, msg, msg_id}` | Queue a message. `to` = `TASK:role` or `session_id`. Idempotent on `msg_id`. |
| GET | `/inbox` | `?session_id=` | Drain messages for this session or its `task:role`. |
| GET | `/` or `/ui` | — | Web dashboard. |
| GET | `/healthz` | — | Liveness probe. |

### Examples

```bash
TOK="-H X-Registry-Token:$REGISTRY_TOKEN"; BASE=http://127.0.0.1:7070

curl -s $TOK $BASE/register -d '{"session_id":"abc","cwd":"/w/TASK-42","host":"box"}'
curl -s $TOK $BASE/assign   -d '{"session_id":"abc","task":"TASK-42","role":"reviewer"}'
curl -s $TOK "$BASE/agents?task=TASK-42"
curl -s $TOK $BASE/send     -d '{"to":"TASK-42:implementer","from":"reviewer","msg":"review done: results at <path>"}'
curl -s $TOK "$BASE/inbox?session_id=def"
```

## Delivery model

- The **mailbox is the source of truth** (durable, survives target downtime).
- If a target registered with `notify=push` and a `handle` URL, `/send` fires a
  best-effort doorbell `POST {"event":"mailbox"}` to wake it. Failure is non-fatal
  — the message waits in the mailbox until the target polls `/inbox` or
  re-registers.
- `poll-only` agents (no handle) just drain `/inbox` on a timer / on register.

## License

MIT — see [LICENSE](LICENSE).
