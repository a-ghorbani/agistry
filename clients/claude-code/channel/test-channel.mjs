#!/usr/bin/env node
// Headless smoke test for agistry-channel: spawns the channel, does the MCP
// initialize handshake, checks the claude/channel capability is declared, queues
// a message in agistry for the test session, and asserts the channel pushes a
// notifications/claude/channel carrying that message. Requires AGISTRY_URL +
// AGISTRY_TOKEN env and a reachable registry.
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const BASE = process.env.AGISTRY_URL;
const TOK = process.env.AGISTRY_TOKEN;
if (!BASE || !TOK) {
  console.error('set AGISTRY_URL and AGISTRY_TOKEN');
  process.exit(2);
}
const SID = 'chan-test-' + process.pid;
const H = { 'Content-Type': 'application/json', 'X-Registry-Token': TOK };

const channelPath = fileURLToPath(new URL('./agistry-channel.mjs', import.meta.url));
const child = spawn('node', [channelPath], {
  env: { ...process.env, CLAUDE_CODE_SESSION_ID: SID, AGISTRY_POLL_MS: '600' },
  stdio: ['pipe', 'pipe', 'inherit'],
});

let buf = '';
let gotCapability = false;
let gotMessage = false;
let done = false;

child.stdout.on('data', (d) => {
  buf += d.toString();
  let i;
  while ((i = buf.indexOf('\n')) >= 0) {
    const line = buf.slice(0, i).trim();
    buf = buf.slice(i + 1);
    if (!line) continue;
    let msg;
    try { msg = JSON.parse(line); } catch { continue; }
    onMessage(msg);
  }
});

function send(obj) { child.stdin.write(JSON.stringify(obj) + '\n'); }

function onMessage(msg) {
  if (msg.id === 1 && msg.result) {
    gotCapability = !!msg.result.capabilities?.experimental?.['claude/channel'];
    console.log('initialize → claude/channel capability:', gotCapability ? 'YES' : 'NO');
    send({ jsonrpc: '2.0', method: 'notifications/initialized' });
    queueMessage();
  }
  if (msg.method === 'notifications/claude/channel') {
    console.log('channel push received:', JSON.stringify(msg.params));
    gotMessage = true;
    finish();
  }
}

async function queueMessage() {
  await fetch(`${BASE}/register`, { method: 'POST', headers: H, body: JSON.stringify({ session_id: SID, cwd: '/test', host: 'test' }) });
  await fetch(`${BASE}/send`, { method: 'POST', headers: H, body: JSON.stringify({ to: SID, from: 'tester', msg: 'hello from channel test' }) });
  console.log('queued a message for', SID);
}

async function finish() {
  if (done) return;
  done = true;
  try { await fetch(`${BASE}/deregister`, { method: 'POST', headers: H, body: JSON.stringify({ session_id: SID }) }); } catch {}
  child.kill();
  const pass = gotCapability && gotMessage;
  console.log('RESULT:', pass ? 'PASS' : 'FAIL');
  process.exit(pass ? 0 : 1);
}

// kick off the MCP handshake
send({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'test', version: '0' } } });
setTimeout(() => { console.log('timeout waiting for channel push'); finish(); }, 8000);
