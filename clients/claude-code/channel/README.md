# agistry channel (live-wake)

A Claude Code [channel](https://code.claude.com/docs/en/channels) (research preview)
that surfaces agistry mailbox messages into a **running** session — so handoffs
from other agents arrive and wake the session automatically, instead of the agent
having to run `agistry.sh inbox` itself.

## How it works

It is an MCP server declaring `capabilities.experimental['claude/channel']`. Because
agistry runs on a different host than the agents (so an inbound localhost listener
would be unreachable), it does **not** open a port. Instead it **polls
`GET /inbox` outbound** every few seconds and pushes each message into the session
via `notifications/claude/channel` (it appears as `<channel source="agistry">…`).
The agent replies through the normal skill (`agistry.sh send`).

Polling starts **only after `mcp.connect()` succeeds**, which only happens when the
session is launched with the channel flag below. If it is ever spawned outside
channel mode, it exits before touching the mailbox.

## Install

```bash
# from clients/claude-code/
./install.sh --with-channel
# → installs to ~/.claude/agistry-channel and runs npm install
```

Requires Node 18+ (uses global `fetch`) and `npm`. Config (registry URL + token)
comes from `~/.config/agistry/client.env`, the same file the hooks/skill use.

## Enable

⚠️ **Never add this server to `mcpServers` (`~/.claude.json` / `.mcp.json`).** A
server listed there is spawned in *every* session, and this one would then drain
your mailbox even when not acting as a channel — messages would be marked delivered
but never shown. Instead, load it only via the explicit flag:

```bash
claude --dangerously-load-development-channels server:$HOME/.claude/agistry-channel/agistry-channel.mjs
```

Handy alias:

```bash
alias claude-party='claude --dangerously-load-development-channels server:$HOME/.claude/agistry-channel/agistry-channel.mjs'
```

## Caveats

- **Research preview** — `--dangerously-load-development-channels` and the channel
  protocol may change; not available on Bedrock / Vertex / Foundry.
- **Latency** is the poll interval (`AGISTRY_POLL_MS`, default 4000 ms), not instant.
- **Session id** comes from `$CLAUDE_CODE_SESSION_ID`; if a future Claude Code build
  stops exposing it to MCP subprocesses, the channel can't address its inbox.
- **At-most-once wake**: `/inbox` marks messages delivered when read, so a transport
  hiccup mid-push could drop a wake (the durable record still lives in agistry until
  read; this only affects the live notification).
