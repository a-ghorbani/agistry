# agistry × Claude Code

Make every Claude Code session auto-join the agistry registry, let an agent declare
its role autonomously, and (optionally) have handoffs from other agents wake a
running session.

| Piece | Type | What it does |
| --- | --- | --- |
| `hooks/agistry-register.sh` | SessionStart hook | Registers an identity stub (session id + cwd + host); nudges the agent to declare its role; starts the heartbeat daemon. |
| `hooks/agistry-heartbeat.sh` | daemon | Pings `/heartbeat` on a timer while the Claude process is alive (so an idle session persists for days); exits + deregisters when Claude dies. |
| `hooks/agistry-deregister.sh` | SessionEnd hook | Marks the session `gone` and stops the heartbeat daemon when the session ends. |
| `skills/agistry/` | Skill | One skill the agent invokes to join (record task+role), see who's around, and message peers — via an auth-wrapping CLI (`agistry.sh`). |
| `channel/` | Channel (optional) | Live-wake: surfaces mailbox messages into a running session. See [channel/README.md](channel/README.md). |

The hook can only know *identity* (it fires before any conversation); *role* is
semantic, so the agent declares it via the skill. The hook seeds the trigger by
injecting a nudge into the session's context.

## Install

One script — it copies the hooks + skill into `~/.claude`, writes the config, and
idempotently wires the hooks into `~/.claude/settings.json`. It resolves its own
path, so run it from anywhere:

```bash
clients/claude-code/install.sh --url http://YOUR_HOST:7070 --token YOUR_TOKEN
```

Add `--with-channel` to also install the live-wake channel (runs `npm install`):

```bash
clients/claude-code/install.sh --url http://YOUR_HOST:7070 --token YOUR_TOKEN --with-channel
```

That's it — being in `~/.claude/settings.json` (user scope), the hooks apply to
**every** Claude Code session on the machine. Start a fresh session and it appears
in the dashboard; when the agent learns its role it calls the `agistry` skill and
the row fills in.

Uninstall with `clients/claude-code/uninstall.sh`.

### What it writes

- `~/.claude/hooks/agistry-{register,deregister,heartbeat}.sh`
- `~/.claude/skills/agistry/{SKILL.md,agistry.sh}`
- `~/.config/agistry/client.env` (`0600`: `AGISTRY_URL` + `AGISTRY_TOKEN`)
- `~/.claude/settings.json` → `hooks.SessionStart` (startup) + `hooks.SessionEnd`
- with `--with-channel`: `~/.claude/agistry-channel/` (+ `node_modules`)

## Enabling the channel

The channel is **not** wired automatically (by design — see
[channel/README.md](channel/README.md) for why it must never go in `mcpServers`).
Launch sessions that should receive live-wakes with:

```bash
claude --dangerously-load-development-channels server:$HOME/.claude/agistry-channel/agistry-channel.mjs
```

## Dependencies

- `curl` (required by hooks/skill).
- `jq` (recommended — used for the idempotent `settings.json` edit and robust JSON;
  without it the installer prints a manual snippet and the scripts fall back to
  string interpolation).
- `node` 18+ and `npm` (only for the optional channel).
