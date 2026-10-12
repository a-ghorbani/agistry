# agistry channel (live-wake)

A Claude Code [channel](https://code.claude.com/docs/en/channels) (research preview)
that pushes agistry mailbox messages into a **running** session, so a handoff from
another agent wakes it instead of waiting for the agent to run `agistry.sh inbox`.

## Setup

`install.sh --with-channel` installs it and registers it as the `agistry-channel` MCP
server; you then launch sessions with `claude-party`. Both steps, including other
profiles, are in the [client README](../README.md#install).

## How it works

- **Polls outward, opens no port.** The registry usually runs on another host, which
  couldn't reach a local listener. So every `AGISTRY_POLL_MS` (default 4000) it peeks
  at `GET /inbox?peek=1`, pushes each message into the session as
  `<channel source="agistry">…`, then `/ack`s only what it pushed. The agent replies
  with `agistry.sh send`.
- **Idle unless switched on.** Claude starts every MCP server listed for the profile,
  so the channel also starts in plain `claude` sessions. It polls only when
  `AGISTRY_CHANNEL_ACTIVE=1`, which `claude-party` sets, so it never takes another
  session's mail.
- **Follows the session.** `claude --resume` from the picker and `/clear` change the
  session id after the channel has started. On each poll it reads
  `~/.config/agistry/state/by-pid/<claude pid>`, which the SessionStart hook rewrites,
  and uses it if the process start time recorded there matches; otherwise it uses the
  id it started with. `/clear` keeps the declared task and role, so `TASK:role` mail
  still arrives.
- **At least once.** A push that fails isn't acked and is retried on the next poll.
  Each message carries a stable `msg_id`, so the agent can spot a repeat.

## Caveats

- **Research preview:** `--dangerously-load-development-channels` and the channel
  protocol may change. Not available on Bedrock, Vertex or Foundry.
- **Latency** is the poll interval, not instant. The first poll waits
  `AGISTRY_CHANNEL_START_DELAY_MS` (default 3000) so a resumed session settles first.
- Config (registry URL and token) comes from `~/.config/agistry/client.env`.
