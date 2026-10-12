# agistry × Claude Code

Makes every Claude Code session join the agistry registry on its own, gives the agent
the `agistry` skill to declare its task and role and message other agents, and
(optionally) wakes a running session when a handoff arrives.

**Requires** `curl` and `jq`; `node` 18+ and `npm` for the live-wake channel. You also
need a clone of this repo and a running agistry server's URL and token (see the
[top-level README](../../README.md#run)).

## Install

Run on every machine that runs Claude Code. The scripts work from any directory and
are safe to re-run.

### 1. Your default profile (`~/.claude`)

```bash
clients/claude-code/install.sh --url http://HOST:7070 --token TOKEN --with-channel
```

| Flag | Adds |
| --- | --- |
| `--url`, `--token` | Saved to `~/.config/agistry/client.env`, which every profile (and the Codex client) shares. Given one, only that key changes. |
| `--with-channel` | [Live-wake](#live-wake-channel): handoffs from other agents arrive in a running session. Installs the channel and registers it as the `agistry-channel` MCP server. |
| `--with-statusline` | [Status bar label](#status-line): `🏷 role · task` for each session. |

Open a new `claude` session: it appears in the dashboard, and fills in its task and
role once the agent joins via the skill.

### 2. More profiles (optional)

For other accounts run through `CLAUDE_CONFIG_DIR` (e.g. `~/.claude-work1`), install
into each with `CLAUDE_DIR`. The URL and token are already saved, so leave them off:

```bash
CLAUDE_DIR=~/.claude-work1 clients/claude-code/install.sh --with-channel
CLAUDE_DIR=~/.claude-work2 clients/claude-code/install.sh --with-channel
```

Each profile gets its own hooks, skill and channel, pointing at that profile.

### 3. The `claude-party` launcher (with `--with-channel`)

The channel only runs in sessions started with it switched on. The installer prints
a launcher for the profile it set up; add it to `~/.zshrc` or `~/.bashrc`:

```bash
claude-party() {
  AGISTRY_CHANNEL_ACTIVE=1 claude "$@" \
    --dangerously-load-development-channels server:agistry-channel
}
claude-party-work1() {      # one per extra profile
  CLAUDE_CONFIG_DIR="$HOME/.claude-work1" AGISTRY_CHANNEL_ACTIVE=1 claude "$@" \
    --dangerously-load-development-channels server:agistry-channel
}
```

Then start sessions with `claude-party` (e.g. `claude-party --resume`). Use a
function, not an alias: the channels flag takes a list, so arguments an alias appends
after it would be read as channel names.

### Uninstall

```bash
clients/claude-code/uninstall.sh                              # default profile
CLAUDE_DIR=~/.claude-work1 clients/claude-code/uninstall.sh   # another profile
```

Removes that profile's hooks, skill, channel (and its MCP registration) and status
line, restoring any status line it wrapped. `client.env` and the host's
[providers](../providers/README.md) stay, since other profiles may use them.

## How it works

| Piece | Role |
| --- | --- |
| `hooks/agistry-register.sh` | SessionStart (startup, resume, clear): registers the session (id, cwd, host), records it in `~/.config/agistry/state/`, starts the heartbeat, and tells the agent to join with the skill. |
| `hooks/agistry-heartbeat.sh` | Background loop per session: re-sends the session's full identity each tick (so it survives a registry wipe), reports a lost `task:role` race to the agent, and deregisters when Claude exits. |
| `hooks/agistry-deregister.sh` | SessionEnd: marks the session `gone`, stops the heartbeat. |
| `skills/agistry/` | The skill and its CLI, `agistry.sh`: `join`, `who`, `send`, `inbox`, and [resource leases](../../README.md#resources--leases). It adds the token, so the agent never handles it. |
| `channel/` | Optional [live-wake channel](channel/README.md). |
| `statusline/` | Optional status line. |

A hook fires before any conversation, so it can only know the session's identity.
The *role* is a judgement the agent makes, so the agent declares it with the skill.

### Live-wake channel

Without it, an agent sees new messages only when it runs `agistry.sh inbox`. With it,
messages are pushed into the running `claude-party` session as they arrive. Details
and caveats: [channel/README.md](channel/README.md).

### Status line

`--with-statusline` shows `🏷 role · task` once the session has joined, and
`📁 <dir> · (not joined)` before. A profile can have only one status line, so an
existing one is kept and the label is appended to its output: `<yours> │ 🏷 role · task`.
An older copy of this script is replaced instead, so the label doesn't show twice.
Codex has no equivalent: its status line takes built-in items only.

### Files

Per profile (`~/.claude`, or `$CLAUDE_DIR`):

- `hooks/agistry-{register,deregister,heartbeat}.sh`, `skills/agistry/`
- `settings.json`: the SessionStart and SessionEnd hooks, plus `.statusLine` with
  `--with-statusline` (a backup is saved on every change)
- `agistry-channel/` and the `agistry-channel` user-scope MCP server, with `--with-channel`
- `statusline/agistry-statusline.sh` (and `.prev.json` holding the status line it
  wraps), with `--with-statusline`

Shared by the whole host: `~/.config/agistry/client.env` (mode `0600`),
`~/.config/agistry/state/`, and `~/.config/agistry/providers/`.
