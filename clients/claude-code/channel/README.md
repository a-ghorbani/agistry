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

Polling starts **only when `AGISTRY_CHANNEL_ACTIVE=1`** is set (the `claude-party`
launch below sets it). The server has to be listed in `mcpServers` for the channel
flag to find it, so Claude also spawns it in normal sessions — the env gate keeps it
idle there, so it never touches the mailbox unless it is really acting as a channel.

## Install

```bash
# from clients/claude-code/
./install.sh --with-channel
# → installs to ~/.claude/agistry-channel and runs npm install
```

Requires Node 18+ (uses global `fetch`) and `npm`. Config (registry URL + token)
comes from `~/.config/agistry/client.env`, the same file the hooks/skill use.

## Enable

The `--dangerously-load-development-channels` flag takes the **name** of a registered
MCP server (not a path), so register it once (user scope, absolute path):

```bash
claude mcp add -s user agistry-channel -- node "$HOME/.claude/agistry-channel/agistry-channel.mjs"
```

Then launch party sessions with the flag **and** the activation env var. Thanks to
the env gate, having it in `mcpServers` is safe — it idles in every non-party session:

```bash
alias claude-party='AGISTRY_CHANNEL_ACTIVE=1 claude --dangerously-load-development-channels server:agistry-channel'
```

Run `claude-party` from any project directory; the channel attaches and starts
polling your inbox.

## Caveats

- **Research preview** — `--dangerously-load-development-channels` and the channel
  protocol may change; not available on Bedrock / Vertex / Foundry.
- **Latency** is the poll interval (`AGISTRY_POLL_MS`, default 4000 ms), not instant.
- **Session id** comes from `$CLAUDE_CODE_SESSION_ID`; if a future Claude Code build
  stops exposing it to MCP subprocesses, the channel can't address its inbox.
- **At-most-once wake**: `/inbox` marks messages delivered when read, so a transport
  hiccup mid-push could drop a wake (the durable record still lives in agistry until
  read; this only affects the live notification).
