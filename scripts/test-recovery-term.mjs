#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = readFileSync(join(root, 'internal/webassets/web/recovery-term.html'), 'utf8');
const script = (html.match(/<script>([\s\S]*?)<\/script>/) || [])[1];

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-recovery-term ===');

if (!script || script.length < 3000) { no('could not extract the page script'); process.exit(1); }

const written = [];
let lineUnderCursor = 'root@vps:/opt#';
let bufferType = 'normal';
const term = {
  cols: 80, rows: 24,
  _data: null, _resize: null,
  write(x, cb) { written.push(String(x)); if (cb) cb(); },
  focus() {},
  onData(f) { this._data = f; },
  onResize(f) { this._resize = f; },
  buffer: { active: {
    get type() { return bufferType; },
    baseY: 0, cursorY: 0, cursorX: 14,
    getLine: () => ({ translateToString: () => lineUnderCursor }),
  } },
  loadAddon() {}, open() {},
};

const sockets = [];
class FakeWS {
  static CONNECTING = 0; static OPEN = 1; static CLOSED = 3;
  constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
  send(b) { if (this.readyState !== 1) throw new Error('socket closed'); this.sent.push(b); }
  close() { this.readyState = 3; if (this.onclose) this.onclose({ code: 1006 }); }
  open() { this.readyState = 1; if (this.onopen) this.onopen(); }
  kill(code = 1006) { this.readyState = 3; if (this.onclose) this.onclose({ code }); }
  receive(dados) { if (this.onmessage) this.onmessage({ data: dados }); }
}

const timers = [];
const listeners = {};
const elements = {};
let probes = 0, probeResponse = { status: 200, type: 'basic' };

const ctx = {
  Terminal: function () { return term; },
  FitAddon: { FitAddon: function () { return { fit() {} }; } },
  WebSocket: FakeWS,
  TextEncoder,
  document: {
    getElementById: (id) => (elements[id] ||= { id, textContent: '', style: {}, dataset: {}, hidden: true }),
    addEventListener: (ev, f) => { (listeners[ev] ||= []).push(f); },
    cookie: 'panel_recovery_user=sam',
    hidden: false,
  },
  window: { addEventListener: (ev, f) => { (listeners[ev] ||= []).push(f); } },
  location: { protocol: 'https:', host: 'panel.example', href: '' },
  requestAnimationFrame: (f) => { timers.push(f); return timers.length; },
  cancelAnimationFrame: () => {},
  setTimeout, clearTimeout, setInterval, clearInterval,
  Date, Math, JSON, Uint8Array,
  fetch: () => { probes++; return Promise.resolve(probeResponse); },
  confirm: () => true,
};
ctx.window.addEventListener = ctx.window.addEventListener.bind(ctx.window);

const names = Object.keys(ctx);
try {
  new Function(...names, script + '\n;this.__create = createTerminal;').call(ctx, ...names.map((n) => ctx[n]));
} catch (e) {
  no('the page script does not run: ' + e.message);
  process.exit(1);
}
if (typeof ctx.__create !== 'function') { no('createTerminal does not exist — the two terminals would drift apart again'); process.exit(1); }
sockets.length = 0;
const inst = ctx.__create({ path: '/recovery/ws/pty', element: null, pill: null, label: 'host' });
inst.open();
const st = inst.st, send = inst.send;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const output = () => written.join('');

sockets.length === 1 ? ok('opens the connection when the page loads') : no('no connection was opened at all');
sockets[0].open();

{
  written.length = 0;
  term._data('ls');
  const b = sockets[0].sent.slice(-1)[0];
  (b instanceof Uint8Array && new TextDecoder().decode(b) === 'ls')
    ? ok('the keystroke goes out raw, in a binary frame')
    : no('it did not send binary: ' + JSON.stringify(String(b)));
}

{
  sockets[0].kill(1006);
  written.length = 0;
  term._data('reboot');
  const queued = (st.outbox || []).join('');
  const painted = output().includes('reboot') && output().includes('\x1b[2m');
  (queued === 'reboot' && painted)
    ? ok('connection down: keeps the keystroke AND shows it on screen (dimmed)')
    : no(`keystroke lost or screen dead (queue=${JSON.stringify(queued)}, output=${JSON.stringify(output())})`);
}
{
  await wait(700);
  sockets.length >= 2
    ? ok('reconnects on its own after the outage (the worst hole: the session died until a reload)')
    : no('did NOT reconnect — the recovery session dies on one network blink');
}

{
  const s2 = sockets[sockets.length - 1];
  written.length = 0;
  s2.open();
  const sent = s2.sent.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('|');
  (output().includes('\b \b') && sent.includes('reboot'))
    ? ok('on return: erases the local echo and sends the queue (no duplicated text)')
    : no('reconnect did not clear/flush: output=' + JSON.stringify(output()) + ' sent=' + sent);
}

{
  const s = sockets[sockets.length - 1];
  s.kill(1006);
  lineUnderCursor = '[sudo] password for sam:';
  written.length = 0;
  term._data('mypassword');
  output() === ''
    ? ok('password prompt: no echo (the password never reaches the screen)')
    : no('ECHOED the password: ' + JSON.stringify(output()));
  lineUnderCursor = 'root@vps:/opt#';
  await wait(700);
}

{
  const s = sockets[sockets.length - 1];
  s.open();
  s.sent.length = 0;
  const block = new Uint8Array(300 * 1024);
  s.receive(block.buffer ? block : block);
  const asked = s.sent.some((x) => typeof x === 'string' && x.includes('pause'));
  asked
    ? ok('heavy output makes the client ask for a PAUSE (the server stops reading the PTY)')
    : no('it never asks for a pause — the xterm buffer blows and it DROPS bytes');
}

{
  const s = sockets[sockets.length - 1];
  probeResponse = { status: 0, type: 'opaqueredirect' };
  probes = 0;
  s.kill(1006);
  await wait(500);
  const before = sockets.length;
  const hadProbe = probes > 0;
  sockets[sockets.length - 1].kill(1006);
  await wait(800);
  (hadProbe || probes > 0)
    ? ok('after failing again, it ASKS whether the session is still valid (HEAD)')
    : no('it never asks — it would loop reconnecting forever with an expired session');
  await wait(300);
  const banner = elements['banner'] || {};
  (String(banner.textContent || '').includes('expired') && banner.hidden === false)
    ? ok('an expired session is spelled out in the page banner')
    : no('no warning in the banner: ' + JSON.stringify(banner.textContent));
  !output().includes('expired')
    ? ok('the notice is NOT written into the terminal (Claude scrollback stays clean)')
    : no('the notice dirties the terminal again');
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
