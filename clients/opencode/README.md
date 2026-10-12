# agistry × opencode

Registers each top-level [opencode](https://opencode.ai) session with agistry, keeps
its task, role and presence up to date, and delivers agistry mail into it.

**Requires** opencode (tested with 1.14.28), Bash, Python 3, `curl` and `jq`.

## Install

```bash
clients/opencode/install.sh --url http://HOST:7070 --token TOKEN
```

Leave off `--url`/`--token` if `~/.config/agistry/client.env` already has them (e.g.
from another client); given one, only that key changes. Then restart opencode. Each
session appears in the dashboard once it starts, or once you use a resumed one.

It writes:

- `~/.config/opencode/plugins/agistry.js`: the plugin (honors `OPENCODE_CONFIG_DIR`)
- the [shared pieces](../shared/install.sh): `client.env`, the `agistry` skill in
  `~/.agents/skills/agistry/` (which opencode loads), `~/.config/agistry/lib/`, and
  the host's [device providers](../providers/README.md)

**Uninstall:** `clients/opencode/install.sh --uninstall` removes the plugin and leaves
the shared pieces, which other agents may use.

## Use

The plugin sets `AGISTRY_SESSION_ID` for every command the agent runs, so the skill's
CLI knows the session without any flags:

```bash
~/.agents/skills/agistry/agistry.sh join implementer TASK-42
~/.agents/skills/agistry/agistry.sh send TASK-42:reviewer "ready for review"
```

The plugin also adds a line to the system prompt telling the agent how to join.

## How it works

opencode loads a plugin once per project directory and sends it the events of every
session there. The plugin tracks one [shared client](../shared/agistry-session.mjs)
session per top-level opencode session:

- **Registers** a session when it is created, or on its first activity, since a
  resumed session fires no "created" event. Subagent sessions (those with a parent)
  are skipped and get no session id, so they can't join.
- **Delivers** each message with `session.promptAsync`. An idle session starts a turn
  on it; a busy one gets it after the current turn. Messages are acknowledged once
  opencode accepts them, at least once, each with a stable `msg_id`.
- **Deregisters** a session when it is deleted, and every session when opencode shuts
  the project down. If opencode exits without that, the registry's TTL marks the
  sessions `gone`.

A session stays registered while opencode runs, even if you switch to another one in
the TUI. Settings (in `client.env`): `AGISTRY_HEARTBEAT_SEC` (120) and
`AGISTRY_POLL_SEC` (4).
