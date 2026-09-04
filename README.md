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

![agistry dashboard — force-directed graph of agents grouped by host, with a selected agent's task, session, and message history in the side panel](docs/dashboard.png)

The dashboard groups live agents by host, draws message handoffs as links, and
opens a side panel with each agent's task, session, cwd, and recent messages.
Toggle the **Graph / Table** views, filter `gone`/`idle`, and auto-refresh.

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
| `REGISTRY_PENDING_TTL_SECONDS` | `604800` (7d) | Seconds an unclaimed message waits before it is dead-lettered |
| `REGISTRY_RESOURCE_TTL_SECONDS` | `900` | Provider silence before a resource is presumed `gone` |
| `REGISTRY_LEASE_MAX_HOLD_SECONDS` | `3600` | Fallback cap on a single hold, when a resource sets none |
| `REGISTRY_RESOURCES_FILE` | _(unset)_ | Optional JSON file seeding resources that have no provider to announce them |
| `AGISTRY_WEB_DIR` | _(unset = embedded)_ | Dev: serve `web/index.html` from this dir instead of the embedded copy (edit + refresh, no rebuild) |

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
| POST | `/assign` | `{session_id, task, role, cwd, host, force}` | Set role/task. `task` must be a short tag (≤40 chars, no spaces). Single owner per `task:role` (409 if held). Refuses to change an existing identity without `force:true`. |
| POST | `/heartbeat` | `{session_id, cwd, host}` | Bump liveness; revives a `gone` entry and re-creates a stub if the session is unknown (e.g. after a registry wipe). |
| POST | `/deregister` | `{session_id}` | Mark `gone`. |
| GET | `/agents` | `?task=&role=&state=&all=1` | Who's doing what. Hides `gone` unless `all=1`. |
| POST | `/send` | `{to\|task,role, from, msg, msg_id}` | Queue a message. `to` = `TASK:role` (may be not-yet-joined — late binding) or a live `session_id`. Idempotent on `msg_id`. |
| GET | `/inbox` | `?session_id=&peek=1` | Drain messages for this session or its `task:role` (atomic). `peek=1` returns without consuming. |
| POST | `/ack` | `{session_id, msg_ids:[...]}` | Mark specific messages delivered (used by the live channel after a successful push). |
| GET | `/messages` | `?limit=N` | Read-only recent message feed (does **not** consume). |
| POST | `/resources/register` | `{id, kind, name, host, meta, max_hold_seconds}` | Announce/refresh a resource. Idempotent; a refresh revives one that aged out. |
| POST | `/resources/deregister` | `{id}` | Retire a resource; releases any standing lease. |
| GET | `/resources` | `?kind=&host=&id=&free=1&held=1&all=1` | The board: what exists, who holds it, until when, and what the last holder left. |
| POST | `/resources/claim` | `{resource_id, session_id, ttl_seconds, note}` | Take it exclusively. 409 if a **live** holder has it (response carries their `task:role` so you can message them). Reclaims a stale lease (flagged `reclaimed`). Always reports `previous` — what the last holder left on it. |
| POST | `/resources/renew` | `{resource_id, session_id, ttl_seconds}` | Extend your hold, never past the resource's cap. |
| POST | `/resources/release` | `{resource_id, session_id, note}` | Hand it back. The `note` becomes the record of what you left on it. |
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

## Resources & leases

A **resource** is a thing agents contend for: an Android phone on a USB hub, a GPU, a
staging environment, an API quota. Lanes claim one so they stop interrupting each other
— reinstalling over a running e2e suite, or taking screenshots of someone else's build.

**agistry does not enforce anything.** It cannot stop you running `adb install`. A lease
is *advisory*: it prevents collisions, it does not prove the resource is in the state
you expect. Keep whatever verification you already do — the lease and the check answer
different questions, and a lease that quietly replaces a check makes things worse, since
the collision still happens and now nothing detects it.

- **A lease, not a lock.** The dominant failure is not contention, it is an agent that
  dies mid-hold and deadlocks the fleet. A lease ends when its deadline passes **or**
  when the holder's session goes `gone` — and the second is why this lives in the
  registry rather than a lock file: only agistry knows the holder died. *Alive holder →
  held; dead holder → free.* The Claude Code heartbeat daemon ticks independently of
  agent activity, so a lane blocked in a three-hour test still holds its phone.
- **A cap, so a live-but-idle holder cannot sit on a device forever.** Per resource via
  `max_hold_seconds` (set it to ~6h for phones running full e2e suites), falling back to
  `REGISTRY_LEASE_MAX_HOLD_SECONDS`. Renewal can never outrun it; hitting the cap is the
  signal to re-claim deliberately.
- **No `steal`.** Reclaiming a dead holder needs no ceremony and happens automatically
  inside `claim`, which reports what it displaced. Taking a device from a *live* holder
  is a conversation, not an API call — hence the 409 carrying their `task:role`, which
  you hand straight to `/send`.
- **The note is worth more than the lock.** Every lease carries an opaque note agistry
  stores and never interprets — an installed bundle hash, a job id, a reason. It
  survives release, so a lane arriving at a *free* device still learns what is on it,
  and a reviewer reading back later has a durable record of what was there at capture
  time.
- **Discovery belongs to the provider, not the server.** A phone is attached to a
  *host*, not to the registry, so only something running on that host can see it.
  `clients/providers/agistry-adb-provider.sh` is the reference: it runs `adb devices`
  and posts what it finds, on a loop, so devices appear and age out with reality.
  Providers are **opt-in per host** — nothing starts unless `AGISTRY_PROVIDERS` names
  it, since most hosts have no devices attached. Static things with nobody to announce
  them (a staging URL, a quota) go in `REGISTRY_RESOURCES_FILE` instead.

```bash
agistry.sh resources free                      # what is available
agistry.sh claim adb:R5CT21 21600 "app-debug sha256:deadbeef"
agistry.sh renew adb:R5CT21                    # extend (capped)
agistry.sh release adb:R5CT21 "left sha256:deadbeef resident"

# occupied? the 409 names the holder — ask them, do not force
agistry.sh send POC-94:e2e "need adb:R5CT21 for a benchmark — done with it?"
```

## Delivery model

- The **mailbox is the source of truth** — durable, survives the target being offline.
- **Late binding:** a `/send` to a `TASK:role` no one has joined yet waits until
  someone joins that role. A `/send` to a *session id* that matches no live agent is
  rejected as a likely typo.
- **Atomic drain:** `/inbox` selects and marks-delivered in a single statement.
- Two ways a message reaches an agent:
  - **Live channel** (peek + `/ack`) — the bundled Claude Code channel polls
    `/inbox?peek=1`, pushes each message into the running session, and `/ack`s only
    what it delivered: **at-least-once into context** (a dropped push is retried).
  - **Manual poll** (`/inbox`) — the agent drains its mailbox on demand; consume-on-read,
    best-effort.
- **Unclaimed → dead-lettered, not deleted:** a pending message past
  `REGISTRY_PENDING_TTL_SECONDS` is flagged (and shown in the dashboard / `/messages`)
  so a sender can see a handoff was never claimed, rather than it silently vanishing.
- **Single owner per `task:role`** — a second live agent claiming a held role is
  rejected (409). The same mechanism (a partial unique index) gives one holder per
  resource.

## Schema & upgrades

The schema is greenfield — `CREATE TABLE IF NOT EXISTS`, **no in-repo migrations**.
State is disposable: agents reconcile their identity on every heartbeat, liveness
self-heals via TTL, delivered messages GC. To ship a schema change: **stop the
service → delete the `registry.db*` files → restart**; clients re-register within one
heartbeat. Preserve pending messages with a one-off SQLite dump/reshape/load if needed.

## License

MIT — see [LICENSE](LICENSE).
