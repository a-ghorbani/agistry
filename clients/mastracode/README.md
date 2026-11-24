# agistry × Mastra Code

Registers each [Mastra Code](https://code.mastra.ai) session with agistry, keeps its
task, role and presence up to date, and delivers agistry mail into it.

**Requires** Mastra Code (tested with 0.50.2) on Node 22.18+ (it loads the plugin's
TypeScript directly), Bash, Python 3, `curl` and `jq`.

## Install

```bash
clients/mastracode/install.sh --url http://HOST:7070 --token TOKEN
```

Leave off `--url`/`--token` if `~/.config/agistry/client.env` already has them (e.g.
from another client); given one, only that key changes. Then start `mastracode`. The
session appears in the dashboard.

It writes:

- `~/.mastracode/plugins/agistry/`: the plugin, enabled in
  `~/.mastracode/plugins/plugins.json` (it shows up under `/plugins`)
- the [shared pieces](../shared/install.sh): `client.env`, the `agistry` skill in
  `~/.agents/skills/agistry/` (which Mastra Code loads), `~/.config/agistry/lib/`, and
  the host's [device providers](../providers/README.md)

**Uninstall:** `clients/mastracode/install.sh --uninstall` removes the plugin and its
registry entry and leaves the shared pieces, which other agents may use.

## Use

The plugin sets `AGISTRY_SESSION_ID` and `AGISTRY_AGENT_KIND` in Mastra Code's
environment, which every command the agent runs inherits, so the skill's CLI needs no
flags:

```bash
~/.agents/skills/agistry/agistry.sh join implementer TASK-42
~/.agents/skills/agistry/agistry.sh send TASK-42:reviewer "ready for review"
```

The plugin's instructions tell the agent how to join.

## How it works

Mastra Code gives plugins no session-lifecycle events, but it runs a plugin's **signal
providers** for as long as it runs: it connects them to the coding agent, starts them,
and stops them when the plugin is disabled, updated or removed. The plugin is one such
provider around the [shared client](../shared/agistry-session.mjs):

- **Registers** once the TUI's session exists, under an id of its own
  (`mastracode-<uuid>`). Mastra Code's own session id is per project, so two TUIs in
  one directory would share it, and its thread id changes on `/new` and resume. One id
  per process keeps the task and role across `/new` and thread switches, as Claude
  Code does on `/clear`.
- **Delivers** each message as a high-priority notification signal on the current
  thread. An idle thread wakes and the agent acts on it; a busy one gets it after the
  current step. Messages are acknowledged once Mastra Code has stored them, at least
  once, each with a stable `msg_id`. A session with no thread yet (a fresh TUI, or
  right after `/new`) holds its mail until the first prompt creates one.
- **Deregisters** on exit, and when the plugin is stopped. A hard kill skips that; the
  registry's TTL then marks the session `gone`.

Only the interactive TUI is covered: headless (`--prompt`) and `--acp` runs don't
register. Settings (in `client.env`): `AGISTRY_HEARTBEAT_SEC` (120) and
`AGISTRY_POLL_SEC` (4).

The plugin reaches into Mastra Code's runtime through `getActiveSession()`, which
Mastra Code marks experimental, so a future release may need a small update here.
