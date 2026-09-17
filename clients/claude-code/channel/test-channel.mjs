#!/usr/bin/env node
// Headless smoke test for agistry-channel. For each case it spawns the channel, does
// the MCP initialize handshake, checks the claude/channel capability is declared,
// queues a message in agistry for one session, and asserts the channel pushes a
// notifications/claude/channel carrying that message. Requires AGISTRY_URL +
// AGISTRY_TOKEN env and a reachable registry.
//
// Cases:
//   env      no pointer file: the channel polls $CLAUDE_CODE_SESSION_ID
//   pointer  by-pid/<this pid> names another session: the channel polls that one
//   stale    the pointer's start time does not match: the channel ignores it
import { execFileSync, spawn } from 'node:child_process';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const BASE = process.env.AGISTRY_URL;
const TOK = process.env.AGISTRY_TOKEN;
if (!BASE || !TOK) {
  console.error('set AGISTRY_URL and AGISTRY_TOKEN');
  process.exit(2);
}
const H = { 'Content-Type': 'application/json', 'X-Registry-Token': TOK };
const channelPath = fileURLToPath(new URL('./agistry-channel.mjs', import.meta.url));

// The channel is our child and we are not named claude, so it treats this process as
// its owner.
const myStart = execFileSync('ps', ['-o', 'lstart=', '-p', String(process.pid)], {
  encoding: 'utf8',
  env: { ...process.env, LC_ALL: 'C', TZ: 'UTC' },
}).trim().replace(/\s+/g, ' ');

function runCase(name, { pointer }) {
  const envSid = `chan-test-${name}-env-${process.pid}`;
  const ptrSid = `chan-test-${name}-ptr-${process.pid}`;
  const stateDir = mkdtempSync(join(tmpdir(), 'agistry-chan-test-'));
  let target = envSid;
  if (pointer) {
    mkdirSync(join(stateDir, 'by-pid'), { mode: 0o700 });
    const start = pointer === 'match' ? myStart : 'Thu Jan  1 00:00:00 1970';
    writeFileSync(join(stateDir, 'by-pid', String(process.pid)), `${ptrSid}\t${start}\n`);
    if (pointer === 'match') target = ptrSid;
  }

  return new Promise((resolve) => {
    const child = spawn('node', [channelPath], {
      env: {
        ...process.env,
        CLAUDE_CODE_SESSION_ID: envSid,
        AGISTRY_STATE_DIR: stateDir,
        AGISTRY_CHANNEL_ACTIVE: '1',
        AGISTRY_CHANNEL_START_DELAY_MS: '0',
        AGISTRY_POLL_MS: '600',
      },
      stdio: ['pipe', 'pipe', 'inherit'],
    });
    let buf = '';
    let gotCapability = false;
    let gotMessage = false;
    let done = false;

    const send = (obj) => child.stdin.write(JSON.stringify(obj) + '\n');

    async function finish() {
      if (done) return;
      done = true;
      clearTimeout(timer);
      for (const sid of [envSid, ptrSid]) {
        try { await fetch(`${BASE}/deregister`, { method: 'POST', headers: H, body: JSON.stringify({ session_id: sid }) }); } catch {}
      }
      child.kill();
      rmSync(stateDir, { recursive: true, force: true });
      const pass = gotCapability && gotMessage;
      console.log(`[${name}]`, pass ? 'PASS' : 'FAIL');
      resolve(pass);
    }

    async function queueMessage() {
      await fetch(`${BASE}/register`, { method: 'POST', headers: H, body: JSON.stringify({ session_id: target, cwd: '/test', host: 'test' }) });
      await fetch(`${BASE}/send`, { method: 'POST', headers: H, body: JSON.stringify({ to: target, from: 'tester', msg: `hello from channel test (${name})` }) });
      console.log(`[${name}] queued a message for`, target);
    }

    child.stdout.on('data', (d) => {
      buf += d.toString();
      let i;
      while ((i = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, i).trim();
        buf = buf.slice(i + 1);
        if (!line) continue;
        let msg;
        try { msg = JSON.parse(line); } catch { continue; }
        if (msg.id === 1 && msg.result) {
          gotCapability = !!msg.result.capabilities?.experimental?.['claude/channel'];
          console.log(`[${name}] claude/channel capability:`, gotCapability ? 'YES' : 'NO');
          send({ jsonrpc: '2.0', method: 'notifications/initialized' });
          queueMessage();
        }
        if (msg.method === 'notifications/claude/channel') {
          gotMessage = msg.params?.content?.includes(`(${name})`);
          console.log(`[${name}] channel push received:`, JSON.stringify(msg.params?.meta));
          finish();
        }
      }
    });

    send({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'test', version: '0' } } });
    const timer = setTimeout(() => { console.log(`[${name}] timeout waiting for channel push`); finish(); }, 8000);
  });
}

const results = [];
results.push(await runCase('env', {}));
results.push(await runCase('pointer', { pointer: 'match' }));
results.push(await runCase('stale', { pointer: 'stale' }));
const pass = results.every(Boolean);
console.log('RESULT:', pass ? 'PASS' : 'FAIL');
process.exit(pass ? 0 : 1);
