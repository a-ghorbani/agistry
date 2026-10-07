// Real CLI/App Server queue smoke test. Local fixture inference only; no API key,
// external requests, existing daemon, or user sessions. Requires Node 22+ and codex.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {mkdtemp, rm} from 'node:fs/promises';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';

const root = await mkdtemp(path.join(os.tmpdir(), 'agistry-queue-smoke-'));
const env = {...process.env, CODEX_HOME: root};
delete env.OPENAI_API_KEY;
const codex = process.env.AGISTRY_CODEX_BIN || 'codex';
let releaseFirst;
const release = new Promise(resolve => { releaseFirst = resolve; });
let requestCount = 0;
const fixture = http.createServer(async (req, res) => {
  for await (const chunk of req) { /* Drain input; no prompt logging. */ }
  const n = ++requestCount;
  if (n === 1) await release;
  if (res.destroyed) return;
  res.writeHead(200, {'Content-Type': 'text/event-stream'});
  const message = {id: `msg_${n}`, type: 'message', role: 'assistant', status: 'completed',
    content: [{type: 'output_text', text: 'fixture completed', annotations: []}]};
  const emit = (event, data) => res.write(`event: ${event}\ndata: ${JSON.stringify({type: event, ...data})}\n\n`);
  emit('response.created', {response: {id: `resp_${n}`, status: 'in_progress', output: []}});
  emit('response.output_item.added', {output_index: 0, item: {...message, status: 'in_progress', content: []}});
  emit('response.output_text.delta', {item_id: message.id, output_index: 0, content_index: 0, delta: 'fixture completed'});
  emit('response.output_item.done', {output_index: 0, item: message});
  emit('response.completed', {response: {id: `resp_${n}`, status: 'completed', output: [message],
    usage: {input_tokens: 1, output_tokens: 1, total_tokens: 2}}});
  res.end();
});
await new Promise(resolve => fixture.listen(0, '127.0.0.1', resolve));
const portReservation = net.createServer();
await new Promise(resolve => portReservation.listen(0, '127.0.0.1', resolve));
const serverPort = portReservation.address().port;
await new Promise(resolve => portReservation.close(resolve));
const address = `ws://127.0.0.1:${serverPort}`;
const provider = `{name="agistry_fixture",base_url="http://127.0.0.1:${fixture.address().port}/v1",wire_api="responses",requires_openai_auth=false,supports_websockets=false}`;
const server = spawn(codex, ['app-server', '--listen', address,
  '-c', 'model_provider="agistry_fixture"', '-c', `model_providers.agistry_fixture=${provider}`,
  '-c', 'model="fixture"'], {cwd: root, env, stdio: ['ignore', 'ignore', 'pipe']});
let errors = '';
server.stderr.on('data', data => { errors += data; });
let ws;
const pending = new Map();
const events = [];
let nextId = 1;
async function until(check, label) {
  const deadline = Date.now() + 20000;
  while (!check()) {
    if (Date.now() > deadline) throw new Error(`Timeout: ${label}\n${errors.slice(-2000)}`);
    await delay(30);
  }
}
function rpc(method, params) {
  const id = nextId++;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { pending.delete(id); reject(new Error(`Timeout: ${method}`)); }, 15000);
    pending.set(id, {resolve: result => {clearTimeout(timer); resolve(result);},
      reject: error => {clearTimeout(timer); reject(error);}});
    ws.send(JSON.stringify({id, method, params}));
  });
}
async function queueMessage(thread, text) {
  const proc = spawn(codex, ['queue', '--remote', address, '--thread', thread, '--message', text],
    {cwd: root, env, stdio: ['ignore', 'pipe', 'pipe']});
  const timer = setTimeout(() => proc.kill('SIGKILL'), 15000);
  let output = '';
  proc.stdout.on('data', data => {output += data;});
  proc.stderr.on('data', data => {output += data;});
  const code = await new Promise(resolve => proc.on('close', resolve));
  clearTimeout(timer);
  assert.equal(code, 0, output);
}
try {
  for (let i = 0; i < 100; i++) {
    try {
      ws = await new Promise((resolve, reject) => {
        const candidate = new WebSocket(address);
        candidate.onopen = () => resolve(candidate);
        candidate.onerror = () => reject(new Error('not ready'));
      });
      break;
    } catch { await delay(50); }
  }
  assert(ws, errors);
  ws.onmessage = event => {
    const message = JSON.parse(event.data);
    const waiting = pending.get(message.id);
    if (waiting) {
      pending.delete(message.id);
      if (message.error) waiting.reject(new Error(JSON.stringify(message.error)));
      else waiting.resolve(message.result);
    } else events.push(message);
  };
  await rpc('initialize', {clientInfo: {name: 'agistry_smoke', version: '0.1'}, capabilities: {experimentalApi: true}});
  ws.send(JSON.stringify({method: 'initialized'}));
  const {thread} = await rpc('thread/start', {cwd: root, model: 'fixture'});
  await rpc('turn/start', {threadId: thread.id, input: [{type: 'text', text: 'first fixture turn'}]});
  await until(() => requestCount >= 1, 'fixture request');
  await queueMessage(thread.id, 'queued while busy');
  const queued = await rpc('thread/queue/list', {threadId: thread.id});
  assert.equal(queued.data.length, 1, JSON.stringify(queued));
  assert.equal(queued.data[0].input[0].text, 'queued while busy');
  assert.equal(events.filter(e => e.method === 'turn/started').length, 1);
  releaseFirst();
  await until(() => events.filter(e => e.method === 'turn/completed').length >= 2, 'busy follow-up completion');
  assert(events.filter(e => e.method === 'turn/completed').every(e => e.params.turn.status === 'completed'));
  await queueMessage(thread.id, 'queued while idle');
  await until(() => events.filter(e => e.method === 'turn/completed').length >= 3, 'idle follow-up completion');
  assert.equal(requestCount, 3);
  const read = await rpc('thread/read', {threadId: thread.id, includeTurns: true});
  const inputs = read.thread.turns.flatMap(t => t.items.filter(i => i.type === 'userMessage').flatMap(i => i.content));
  assert(inputs.some(i => i.text === 'queued while busy'));
  assert(inputs.some(i => i.text === 'queued while idle'));
  console.log('PASS: real codex queue waits for busy turns and dispatches idle follow-ups; all three fixture turns completed.');
} finally {
  releaseFirst();
  if (ws) ws.close();
  server.kill('SIGKILL');
  await new Promise(resolve => {if (server.exitCode !== null || server.signalCode !== null) resolve(); else server.once('close', resolve);});
  fixture.closeAllConnections();
  await new Promise(resolve => fixture.close(resolve));
  await rm(root, {recursive: true, force: true});
}
