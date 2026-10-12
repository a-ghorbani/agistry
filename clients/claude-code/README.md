# agistry × Claude Code

Make every Claude Code session auto-join the agistry registry, let an agent declare
its role autonomously, and (optionally) have handoffs from other agents wake a
running session.

| Piece | Type | What it does |
| --- | --- | --- |
| `hooks/agistry-register.sh` | SessionStart hook | Registers an identity stub (session id + cwd + host), writes a local desired-state file for the daemon to reconcile, nudges the agent to declare its role, and starts the heartbeat daemon. |
| `hooks/agistry-heartbeat.sh` | daemon | Each tick **reconciles** — replays the session's full identity from the desired-state file (self-healing if the registry was wiped), which also proves liveness; surfaces a lost-`task:role` race to the agent; exits + deregisters when Claude dies. |
| `hooks/agistry-deregister.sh` | SessionEnd hook | Marks the session `gone` and stops the heartbeat daemon when the session ends. |
| `skills/agistry/` | Skill | One skill the agent invokes to join (record task+role), see who's around, and message peers — via an auth-wrapping CLI (`agistry.sh`). |
| `channel/` | Channel (optional) | Live-wake: surfaces mailbox messages into a running session. See [channel/README.md](channel/README.md). |
| `statusline/agistry-statusline.sh` | Status line (optional) | Shows the session's registration in the Claude Code TUI status bar — `🏷 role · task` once joined, `📁 <dir> · (not joined)` before. Reads the same per-session state file. |

The hook can only know *identity* (it fires before any conversation); *role* is
semantic, so the agent declares it via the skill. The hook seeds the trigger by
injecting a nudge into the session's context.

## Install

Do this on every machine that runs Claude Code. You need a clone of this repo, a
running agistry server, and its URL + token (see the [top-level README](../../README.md#deploy-systemd)).
The scripts resolve their own location, so run them from anywhere; all of them are
safe to re-run.

### Step 1 — install into your default profile (`~/.claude`)

```bash
clients/claude-code/install.sh --url http://YOUR_HOST:7070 --token YOUR_TOKEN --with-channel
```

This copies the hooks + skill into `~/.claude`, saves the URL and token to
`~/.config/agistry/client.env`, and wires the hooks into `~/.claude/settings.json`
(a backup is saved). Optional flags:

- `--with-channel` — also install the live-wake channel, so handoffs from other
  agents arrive in a running session (needs `node` 18+ and `npm`), and register it
  with Claude as the `agistry-channel` MCP server (if the `claude` CLI is on your
  `PATH`; otherwise the installer prints the command to run). Leave it out if you
  only want registration + the skill.
- `--with-statusline` — show `role · task` in the Claude Code status bar. Opt-in
  because `settings.json` holds a **single** `.statusLine`, so this replaces any
  status line you already have (a backup is saved).

Start a fresh `claude` session: it appears in the dashboard, and the row fills in
once the agent joins with the `agistry` skill. For live-wake, finish with Step 3.

### Step 2 — add another profile (optional)

If you run other accounts via `CLAUDE_CONFIG_DIR` (e.g. `~/.claude-work1`,
`~/.claude-work2`), install into each one by pointing `CLAUDE_DIR` at it:

```bash
CLAUDE_DIR=~/.claude-work1 clients/claude-code/install.sh --with-channel
CLAUDE_DIR=~/.claude-work2 clients/claude-code/install.sh --with-channel
```

> **Note on `--url` / `--token`:** every profile shares the one
> `~/.config/agistry/client.env`, so you can **leave both flags off** when adding a
> profile — Step 1 already saved them. Passing either one updates just that key for
> **all** profiles; the other saved settings are kept.

Each profile gets its own copy of the hooks, skill, and channel; the hooks detect
which profile they run in, and the skill's paths are rewritten to point at it. (The
[tmux client](../tmux/README.md) installer wires `~/.claude` and every `~/.claude-*/`
profile on its own.)

### Step 3 — add the `claude-party` launcher (if you used `--with-channel`)

The installer already registered the channel; what's left is a way to start
sessions with it switched on. It prints the launcher for the profile it just set
up; add it to your `~/.zshrc` / `~/.bashrc`:

```bash
claude-party() {
  AGISTRY_CHANNEL_ACTIVE=1 claude "$@" \
    --dangerously-load-development-channels server:agistry-channel
}
# one per extra profile
claude-party-work1() {
  CLAUDE_CONFIG_DIR="$HOME/.claude-work1" AGISTRY_CHANNEL_ACTIVE=1 claude "$@" \
    --dangerously-load-development-channels server:agistry-channel
}
```

Run `claude-party` (or `claude-party-work1`, e.g. `claude-party-work1 --resume`)
from any project directory. Plain `claude` sessions don't use the channel:
`AGISTRY_CHANNEL_ACTIVE=1` gates its polling, so it idles there. It's a function
rather than an alias because the channels flag takes a list: anything an alias
appends after it (a prompt, `--resume`, …) would be read as another channel name.
See [channel/README.md](channel/README.md) for details.

### Uninstall

```bash
clients/claude-code/uninstall.sh                              # default profile
CLAUDE_DIR=~/.claude-work1 clients/claude-code/uninstall.sh   # another profile
```

This also unregisters the channel MCP server, if it points at that profile's copy.

`~/.config/agistry/client.env` is left in place.

### What it writes

Per profile (`~/.claude` shown; `$CLAUDE_DIR` for others):

- `~/.claude/hooks/agistry-{register,deregister,heartbeat}.sh`
- `~/.claude/skills/agistry/{SKILL.md,agistry.sh}`
- `~/.claude/providers/` (inert unless `AGISTRY_PROVIDERS` names one)
- `~/.claude/settings.json` → `hooks.SessionStart` (startup, resume, clear) + `hooks.SessionEnd`
- with `--with-channel`: `~/.claude/agistry-channel/` (+ `node_modules`), and the
  `agistry-channel` user-scope MCP server (in `~/.claude.json`, or
  `$CLAUDE_DIR/.claude.json` for other profiles)
- with `--with-statusline`: `~/.claude/statusline/agistry-statusline.sh` + `settings.json` → `.statusLine`

Shared by all profiles: `~/.config/agistry/client.env` (`0600`: `AGISTRY_URL` + `AGISTRY_TOKEN`).

## Dependencies

- `curl` (required by hooks/skill).
- `jq` (recommended — used for the idempotent `settings.json` edit and robust JSON;
  without it the installer prints a manual snippet and the scripts fall back to
  string interpolation).
- `node` 18+ and `npm` (only for the optional channel).
