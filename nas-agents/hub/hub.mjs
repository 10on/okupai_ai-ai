#!/usr/bin/env node
// agent-hub — LAN chat hub for agents on different machines. No dependencies, Node >= 18.
//
//   hub.mjs serve              HTTP hub (run on the NAS, systemd --user)
//   hub.mjs mcp                stdio MCP bridge (run on every machine, registered in Claude Code / Codex)
//   hub.mjs hook               Claude Code hook: injects new messages into a busy agent (non-blocking)
//   hub.mjs add-agent <name>   create a token for an agent and print it
//
// The hub only relays messages and tells agents each other's LAN address; files move directly
// between machines (scp/rsync/http — whatever the agents agree on in chat).
//
// Hub env:    HUB_PORT (7800), HUB_DATA (~/.local/share/agent-hub)
// Bridge env: HUB_URL (http://nas:7800), HUB_TOKEN, HUB_NOTE (optional free text shown to others,
//             e.g. "files: ssh denis@192.168.8.50:/srv/share")
import http from 'node:http';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import crypto from 'node:crypto';
import readline from 'node:readline';

const DATA = process.env.HUB_DATA || path.join(os.homedir(), '.local', 'share', 'agent-hub');
const AGENTS_FILE = path.join(DATA, 'agents.json'); // { name: token }
const LOG_FILE = path.join(DATA, 'messages.jsonl');
const ONLINE_MS = 90_000;
const MAX_BODY = 1 << 20;

// ---------------------------------------------------------------- logging
// One JSON object per line. Server: HUB_LOG (default <data>/hub.log, also stderr -> journald).
// Client (mcp bridge + hook): HUB_CLIENT_LOG (default <data>/client-<HUB_NAME|hostname>.log).
// Message text is logged in full (it is the point); tokens are never logged. Rotates at 10 MB (one .1 backup).
function makeLogger(file, side, also) {
  try { fs.mkdirSync(path.dirname(file), { recursive: true }); } catch { /* best effort */ }
  return (ev, fields = {}) => {
    const line = JSON.stringify({ ts: new Date().toISOString(), side, pid: process.pid, ev, ...fields });
    try {
      if (fs.existsSync(file) && fs.statSync(file).size > 10 << 20) fs.renameSync(file, file + '.1');
      fs.appendFileSync(file, line + '\n');
    } catch { /* logging must never break the agent */ }
    if (also) console.error(line);
  };
}

const [, , cmd, arg] = process.argv;
if (cmd === 'serve') serve();
else if (cmd === 'mcp') mcp();
else if (cmd === 'hook') hook();
else if (cmd === 'add-agent') addAgent(arg);
else {
  console.error('usage: hub.mjs serve | mcp | hook | add-agent <name>');
  process.exit(2);
}

// ---------------------------------------------------------------- hub

function loadAgents() {
  try { return JSON.parse(fs.readFileSync(AGENTS_FILE, 'utf8')); } catch { return {}; }
}

function addAgent(name) {
  if (!/^[a-z0-9][a-z0-9_-]{0,31}$/.test(name || '')) {
    console.error('agent name: 1-32 chars of a-z 0-9 _ -');
    process.exit(2);
  }
  fs.mkdirSync(DATA, { recursive: true });
  const agents = loadAgents();
  agents[name] = crypto.randomBytes(24).toString('hex');
  fs.writeFileSync(AGENTS_FILE, JSON.stringify(agents, null, 2), { mode: 0o600 });
  console.log(agents[name]);
}

function serve() {
  fs.mkdirSync(DATA, { recursive: true });
  const port = Number(process.env.HUB_PORT || 7800);
  const log = makeLogger(process.env.HUB_LOG || path.join(DATA, 'hub.log'), 'hub', true);
  const presence = new Map(); // name -> { ip, note, seen }
  const CURSORS_FILE = path.join(DATA, 'cursors.json'); // name -> last delivered message id (survives restarts)
  let cursors = {};
  try { cursors = JSON.parse(fs.readFileSync(CURSORS_FILE, 'utf8')); } catch { /* first run */ }
  const unread = n => history.filter(m => m.id > (cursors[n] || 0) && m.from !== n && (m.to === n || m.to === '*'));
  const waiters = new Map();  // name -> Set<() => void>
  let nextId = 1;
  let history = [];           // last messages, also persisted to LOG_FILE

  try {
    history = fs.readFileSync(LOG_FILE, 'utf8').split('\n').filter(Boolean).slice(-500).map(l => JSON.parse(l));
    nextId = (history.at(-1)?.id || 0) + 1;
  } catch { /* first run */ }

  const wake = name => { for (const f of waiters.get(name) || []) f(); };
  const json = (res, code, body) => {
    res.writeHead(code, { 'content-type': 'application/json' });
    res.end(JSON.stringify(body));
  };
  const readBody = req => new Promise((resolve, reject) => {
    let n = 0; const chunks = [];
    req.on('data', c => { n += c.length; if (n > MAX_BODY) { reject(new Error('body too large')); req.destroy(); } else chunks.push(c); });
    req.on('end', () => { try { resolve(chunks.length ? JSON.parse(Buffer.concat(chunks)) : {}); } catch (e) { reject(e); } });
    req.on('error', reject);
  });
  const clientIp = req => (req.socket.remoteAddress || '').replace(/^::ffff:/, '');

  http.createServer(async (req, res) => {
    const t0 = Date.now();
    let who = null, info = {};
    res.on('close', () => log('req', { agent: who, ip: clientIp(req), method: req.method, path: req.url, status: res.statusCode, ms: Date.now() - t0, aborted: !res.writableEnded || undefined, ...info }));
    try {
      const url = new URL(req.url, 'http://x');
      if (url.pathname === '/health') return json(res, 200, { ok: true });

      const token = (req.headers.authorization || '').replace(/^Bearer /, '');
      const agents = loadAgents();
      const me = Object.keys(agents).find(n => {
        const a = Buffer.from(agents[n]), b = Buffer.from(token);
        return a.length === b.length && crypto.timingSafeEqual(a, b);
      });
      if (!me) { log('auth_fail', { ip: clientIp(req), path: req.url }); return json(res, 401, { error: 'bad token' }); }
      who = me;

      const prev = presence.get(me);
      const body = req.method === 'POST' ? await readBody(req) : {};
      if (!prev || Date.now() - prev.seen >= ONLINE_MS || prev.ip !== clientIp(req)) log('presence', { agent: me, state: 'online', ip: clientIp(req), note: body.note ?? prev?.note ?? '' });
      presence.set(me, { ip: clientIp(req), note: body.note ?? prev?.note ?? '', seen: Date.now() });

      if (url.pathname === '/agents') {
        const now = Date.now();
        return json(res, 200, Object.keys(agents).map(n => {
          const p = presence.get(n);
          return { name: n, unread: unread(n).length, online: !!p && now - p.seen < ONLINE_MS, ip: p?.ip || null, note: p?.note || '', last_seen: p ? new Date(p.seen).toISOString() : null };
        }));
      }

      if (url.pathname === '/send' && req.method === 'POST') {
        const text = String(body.text || '').trim();
        if (!text) return json(res, 400, { error: 'empty text' });
        const to = body.to === '*' || !body.to ? '*' : String(body.to);
        if (to !== '*' && !agents[to]) return json(res, 404, { error: `unknown agent ${to}` });
        const msg = { id: nextId++, ts: new Date().toISOString(), from: me, to, text };
        if (body.reply_to) msg.reply_to = Number(body.reply_to);
        history.push(msg); history = history.slice(-500);
        fs.appendFileSync(LOG_FILE, JSON.stringify(msg) + '\n');
        const targets = to === '*' ? Object.keys(agents).filter(n => n !== me) : [to];
        for (const n of targets) wake(n);
        info = { msg_id: msg.id, to, reply_to: msg.reply_to };
        log('send', { ...msg, targets, woke_waiting: targets.filter(n => waiters.get(n)?.size) });
        return json(res, 200, { id: msg.id });
      }

      if (url.pathname === '/inbox') {
        // Long-poll: returns undelivered messages, waiting up to ?wait= seconds (max 55) for the first one.
        const wait = Math.min(Number(url.searchParams.get('wait') || 0), 55) * 1000;
        const drain = () => {
          const m = unread(me);
          if (m.length) {
            cursors[me] = m.at(-1).id; fs.writeFileSync(CURSORS_FILE, JSON.stringify(cursors));
            log('deliver', { agent: me, ids: m.map(x => x.id), cursor: cursors[me] });
          }
          info = { delivered: m.length, waited_ms: Date.now() - t0 };
          return m;
        };
        if (unread(me).length || !wait) return json(res, 200, drain());
        let done = false;
        const finish = () => {
          if (done) return; done = true;
          clearTimeout(t); waiters.get(me)?.delete(finish);
          if (!res.writableEnded) json(res, 200, drain());
        };
        const t = setTimeout(finish, wait);
        if (!waiters.has(me)) waiters.set(me, new Set());
        waiters.get(me).add(finish);
        req.on('close', () => { done = true; clearTimeout(t); waiters.get(me)?.delete(finish); });
        return;
      }

      if (url.pathname === '/history') {
        const n = Math.min(Number(url.searchParams.get('limit') || 30), 200);
        // Only what this agent sent, received, or what went to everyone.
        return json(res, 200, history.filter(m => m.from === me || m.to === me || m.to === '*').slice(-n));
      }

      json(res, 404, { error: 'not found' });
    } catch (e) {
      log('error', { agent: who, path: req.url, error: String(e.message || e) });
      json(res, 400, { error: String(e.message || e) });
    }
  }).listen(port, '0.0.0.0', () => log('start', { port, data: DATA, agents: Object.keys(loadAgents()), history: history.length, next_id: nextId }));
}

function clientLogger(side) {
  const who = process.env.HUB_NAME || os.hostname();
  return makeLogger(process.env.HUB_CLIENT_LOG || path.join(DATA, `client-${who}.log`), `${side}@${who}`, false);
}

// ---------------------------------------------------------------- MCP bridge

function mcp() {
  const base = (process.env.HUB_URL || '').replace(/\/$/, '');
  const token = process.env.HUB_TOKEN || '';
  const note = process.env.HUB_NOTE || '';
  if (!base || !token) {
    console.error('agent-hub mcp: set HUB_URL and HUB_TOKEN');
    process.exit(1);
  }
  const log = clientLogger('mcp');
  log('start', { hub: base, note });
  const call = async (method, p, body) => {
    const t0 = Date.now();
    try {
      const r = await fetch(base + p, {
        method,
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: body ? JSON.stringify(body) : undefined,
        signal: AbortSignal.timeout(65_000),
      });
      const data = await r.json().catch(() => ({}));
      log('hub_call', { method, path: p, status: r.status, ms: Date.now() - t0, ...(r.ok ? {} : { error: data.error }) });
      if (!r.ok) throw new Error(data.error || `hub ${r.status}`);
      return data;
    } catch (e) {
      if (!/^hub \d+$/.test(e.message) && e.name !== 'Error') log('hub_call', { method, path: p, ms: Date.now() - t0, error: `${e.name}: ${e.message}` });
      throw e;
    }
  };

  const tools = [
    {
      name: 'hub_agents',
      description: 'List agents known to the LAN hub: online status, IP address, and the note each one published (e.g. how to fetch files from it).',
      inputSchema: { type: 'object', properties: {} },
      run: () => call('POST', '/agents', { note }),
    },
    {
      name: 'hub_send',
      description: 'Send a message to another agent, or to everyone with to="*". Returns immediately; the receiver reads it whenever it next checks (asynchronous — do not wait for an answer, carry on with your work). To share files, send the path and how to reach it (host IP from hub_agents); the receiver pulls it directly (scp/rsync/http) — the hub does not carry files.',
      inputSchema: { type: 'object', properties: { to: { type: 'string', description: 'agent name or "*"' }, text: { type: 'string' }, reply_to: { type: 'number', description: 'id of the message you are answering' } }, required: ['to', 'text'] },
      run: a => call('POST', '/send', { to: a.to, text: a.text, reply_to: a.reply_to, note }),
    },
    {
      name: 'hub_inbox',
      description: 'Fetch messages addressed to you that you have not seen yet (non-blocking by default; each message is delivered once, older ones stay in hub_history). Check it at natural pauses. wait_seconds (0-55) is optional and only for when you have nothing else to do.',
      inputSchema: { type: 'object', properties: { wait_seconds: { type: 'number' } } },
      run: a => call('GET', `/inbox?wait=${Math.max(0, Math.min(Number(a.wait_seconds) || 0, 55))}`),
    },
    {
      name: 'hub_history',
      description: 'Recent chat history involving you (sent, received, broadcast).',
      inputSchema: { type: 'object', properties: { limit: { type: 'number' } } },
      run: a => call('GET', `/history?limit=${Number(a.limit) || 30}`),
    },
  ];

  const send = m => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...m }) + '\n');
  const rl = readline.createInterface({ input: process.stdin });
  rl.on('line', async line => {
    let m;
    try { m = JSON.parse(line); } catch { return send({ id: null, error: { code: -32700, message: 'parse error' } }); }
    if (!m.method || m.id === undefined) return; // notifications / responses
    log('rpc', { id: m.id, method: m.method, ...(m.method === 'tools/call' ? { tool: m.params?.name, args: m.params?.arguments } : {}) });
    const reply = (result, error) => send({ id: m.id, ...(error ? { error } : { result }) });
    switch (m.method) {
      case 'initialize':
        return reply({
          protocolVersion: m.params?.protocolVersion || '2025-06-18',
          capabilities: { tools: {} },
          serverInfo: { name: 'agent-hub', version: '0.1.0' },
          instructions: 'LAN chat with agents on other machines. Use only when the user asks you to talk to or hand work to another agent.',
        });
      case 'ping': return reply({});
      case 'tools/list':
        return reply({ tools: tools.map(({ run, ...t }) => t) });
      case 'tools/call': {
        const t = tools.find(x => x.name === m.params?.name);
        if (!t) return reply(null, { code: -32602, message: 'unknown tool' });
        try {
          const out = await t.run(m.params.arguments || {});
          log('tool_ok', { tool: t.name, result: out });
          return reply({ content: [{ type: 'text', text: JSON.stringify(out, null, 2) }], isError: false });
        } catch (e) {
          log('tool_error', { tool: t.name, error: String(e.message || e) });
          return reply({ content: [{ type: 'text', text: String(e.message || e) }], isError: true });
        }
      }
      default: return reply(null, { code: -32601, message: 'method not found' });
    }
  });
}

// ---------------------------------------------------------------- hook

// Registered for PostToolUse / UserPromptSubmit / Stop. A busy agent never calls hub_inbox, so this
// pushes new messages into its context between tool calls. Stop: if something arrived, keep going.
// Never fails the agent: any error means "no messages".
async function hook() {
  const log = clientLogger('hook');
  const t0 = Date.now();
  let event;
  const done = (out, fields) => {
    log('hook', { event, ms: Date.now() - t0, action: out ? (out.decision || 'inject') : 'none', ...fields });
    if (out) process.stdout.write(JSON.stringify(out));
    process.exit(0);
  };
  try {
    const input = JSON.parse(fs.readFileSync(0, 'utf8') || '{}');
    event = input.hook_event_name;
    const base = (process.env.HUB_URL || '').replace(/\/$/, '');
    if (!base || !process.env.HUB_TOKEN) return done();
    const r = await fetch(`${base}/inbox`, { headers: { authorization: `Bearer ${process.env.HUB_TOKEN}` }, signal: AbortSignal.timeout(1500) });
    const msgs = r.ok ? await r.json() : [];
    if (!r.ok) return done(null, { status: r.status });
    if (!msgs.length) return done();
    const text = 'Messages from other agents via agent-hub (answer with hub_send if needed):\n' +
      msgs.map(m => `[${m.ts}] ${m.from}${m.to === '*' ? ' (to all)' : ''}: ${m.text}`).join('\n');
    const ids = msgs.map(m => m.id);
    if (event === 'Stop') {
      if (input.stop_hook_active) return done(null, { ids, skipped: 'stop_hook_active (messages stay logged on the hub)' });
      return done({ decision: 'block', reason: text }, { ids });
    }
    done({ hookSpecificOutput: { hookEventName: event, additionalContext: text } }, { ids, messages: msgs });
  } catch (e) { done(null, { error: `${e.name}: ${e.message}` }); }
}
