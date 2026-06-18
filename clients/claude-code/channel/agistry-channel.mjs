#!/usr/bin/env node
// agistry-channel: a Claude Code "channel" (research preview) that surfaces
// agistry mailbox messages into the running session.
//
// It is an MCP server (stdio) declaring the `claude/channel` capability. Rather
// than expose an inbound HTTP port (which the registry, on another host, could not
// reach), it polls agistry `/inbox` outbound on a timer and pushes each message
// into the session via `notifications/claude/channel`. The agent replies through
// the normal agistry skill (`agistry.sh send`).
//
// Config: ~/.config/agistry/client.env (AGISTRY_URL, AGISTRY_TOKEN) — same file the
// hooks/skill use. Session id comes from $CLAUDE_CODE_SESSION_ID. Poll interval
// from $AGISTRY_POLL_MS (default 4000).

import { Server } from '@modelcontextprotocol/sdk/server/index.js';
import { StdioServerTransport } from '@modelcontextprotocol/sdk/server/stdio.js';
import { readFileSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';

function loadConfig() {
  const cfg = {
    url: process.env.AGISTRY_URL || 'http://127.0.0.1:7070',
    token: process.env.AGISTRY_TOKEN || '',
  };
  try {
    const txt = readFileSync(join(homedir(), '.config', 'agistry', 'client.env'), 'utf8');
    for (const line of txt.split('\n')) {
      const m = line.match(/^\s*([A-Z_]+)=(.*)$/);
      if (!m) continue;
      if (m[1] === 'AGISTRY_URL') cfg.url = m[2].trim();
      else if (m[1] === 'AGISTRY_TOKEN') cfg.token = m[2].trim();
    }
  } catch {
    // no config file — rely on env / defaults
  }
  return cfg;
}

const { url, token } = loadConfig();
const sid = process.env.CLAUDE_CODE_SESSION_ID || '';
const pollMs = Number(process.env.AGISTRY_POLL_MS || 4000);

const server = new Server(
  { name: 'agistry', version: '0.1.0' },
  {
    capabilities: { experimental: { 'claude/channel': {} } },
    instructions:
      'Messages from other agents arrive here as <channel source="agistry"> events, each addressed to you via the agistry registry (a handoff or note). Act on the message, and if you need to reply or hand off, use the agistry skill (agistry.sh send <to> <msg>).',
  },
);

await server.connect(new StdioServerTransport());

const authHeaders = token ? { 'X-Registry-Token': token } : {};

async function poll() {
  if (!sid) return; // without a session id we cannot address an inbox
  let data;
  try {
    const res = await fetch(`${url}/inbox?session_id=${encodeURIComponent(sid)}`, { headers: authHeaders });
    if (!res.ok) return;
    data = await res.json();
  } catch {
    return; // registry unreachable — try again next tick
  }
  for (const m of data.messages || []) {
    try {
      await server.notification({
        method: 'notifications/claude/channel',
        params: {
          content: m.body || '',
          meta: {
            from: String(m.from || ''),
            msg_id: String(m.msg_id || ''),
            note: 'agistry handoff — reply with agistry.sh send',
          },
        },
      });
    } catch {
      // transport hiccup — the message was already marked delivered by /inbox,
      // so we do not re-fetch it; acceptable at-most-once for a wake signal.
    }
  }
}

setInterval(poll, pollMs);
poll();
