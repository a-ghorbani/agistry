# agistry × pi

Registers each [pi](https://github.com/earendil-works/pi) session with agistry, keeps
its task, role and presence up to date, and delivers agistry mail into it.

**Requires** pi (tested with 1.1.0), Node 18+, Bash, Python 3, `curl` and `jq`.

## Install

```bash
clients/pi/install.sh --url http://HOST:7070 --token TOKEN
```

Leave off `--url`/`--token` if `~/.config/agistry/client.env` already has them (e.g.
from another client); given one, only that key changes. Then start pi, or run
`/reload` in a running one. The session appears in the dashboard.

It writes:

- `~/.pi/agent/extensions/agistry.ts`: the extension (honors `PI_CODING_AGENT_DIR`)
- the [shared pieces](../shared/install.sh): `client.env`, the `agistry` skill in
  `~/.agents/skills/agistry/` (which pi loads), `~/.config/agistry/lib/`, and the
  host's [device providers](../providers/README.md)

**Uninstall:** `clients/pi/install.sh --uninstall` removes the extension and leaves
the shared pieces, which other agents may use.

## Use

pi exports `PI_SESSION_ID` to every command the agent runs, so the skill's CLI knows
the session without any flags:

```bash
~/.agents/skills/agistry/agistry.sh join implementer TASK-42
~/.agents/skills/agistry/agistry.sh send TASK-42:reviewer "ready for review"
```

The extension tells the agent how to join with its first prompt.

## How it works

The extension maps pi's session lifecycle onto the
[shared client](../shared/agistry-session.mjs):

| pi event | agistry |
| --- | --- |
| `session_start` (startup, `/new`, `/resume`, `/fork`) | Register the session and start its heartbeat and inbox polling. `/new` keeps the previous session's task and role, as Claude Code does on `/clear`. |
| `before_agent_start` (first prompt) | Tell the agent its session id and how to join. |
| `session_shutdown` (quit, Ctrl-C, SIGTERM, SIGHUP, or switching session) | Deregister. A `/reload` keeps the session registered. |

Each message is passed to `pi.sendMessage` as a follow-up that triggers a turn. An idle
agent starts working on it right away; a busy one gets it after the current run.
Messages are acknowledged once handed to pi, at least once, each with a stable
`msg_id`. A hard kill skips deregistration; the registry's TTL then marks the session
`gone`.

Settings (in `client.env`): `AGISTRY_HEARTBEAT_SEC` (120) and `AGISTRY_POLL_SEC` (4).
