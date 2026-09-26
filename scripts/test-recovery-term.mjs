#!/usr/bin/env node
// test-recovery-term.mjs — the RECOVERY terminal must not be the more fragile
// of the two.
//
// It is the screen you use when everything else is broken — and it was exactly
// the most primitive client in the project: it did not reconnect (one network
// blink killed the session until someone reloaded), it silently discarded
// anything typed without a socket, it wrote to xterm once per message without
// ever asking for a pause (heavy output blows the buffer and the emulator DROPS
// bytes) and it had no heartbeat at all.
//
// The test loads the REAL script of the page, with the DOM and the WebSocket
// stubbed, and exercises the behaviour. Checking text would not be enough: what
// matters here is what happens when the connection drops.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = readFileSync(join(raiz, 'internal/webassets/web/recovery-term.html'), 'utf8');
const script = (html.match(/<script>([\s\S]*?)<\/script>/) || [])[1];

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-recovery-term ===');

if (!script || script.length < 3000) { no('could not extract the page script'); process.exit(1); }

// ── stubs ───────────────────────────────────────────────────────────────────
const escrito = [];
let linhaSobCursor = 'root@vps:/opt#';
let tipoBuffer = 'normal';
const term = {
  cols: 80, rows: 24,
  _dados: null, _resize: null,
  write(x, cb) { escrito.push(String(x)); if (cb) cb(); },
  focus() {},
  onData(f) { this._dados = f; },
  onResize(f) { this._resize = f; },
  buffer: { active: {
    get type() { return tipoBuffer; },
    baseY: 0, cursorY: 0, cursorX: 14,
    getLine: () => ({ translateToString: () => linhaSobCursor }),
  } },
  loadAddon() {}, open() {},
};

const sockets = [];
class FakeWS {
  static CONNECTING = 0; static OPEN = 1; static CLOSED = 3;
  constructor(url) { this.url = url; this.readyState = 0; this.enviados = []; sockets.push(this); }
  send(b) { if (this.readyState !== 1) throw new Error('socket closed'); this.enviados.push(b); }
  close() { this.readyState = 3; if (this.onclose) this.onclose({ code: 1006 }); }
  abre() { this.readyState = 1; if (this.onopen) this.onopen(); }
  derruba(code = 1006) { this.readyState = 3; if (this.onclose) this.onclose({ code }); }
  recebe(dados) { if (this.onmessage) this.onmessage({ data: dados }); }
}

const timers = [];
const ouvintes = {};
const elementos = {};
let sondas = 0, respostaSonda = { status: 200, type: 'basic' };

const ctx = {
  Terminal: function () { return term; },
  FitAddon: { FitAddon: function () { return { fit() {} }; } },
  WebSocket: FakeWS,
  TextEncoder,
  document: {
    // Elements are memoised: the test needs to READ BACK what the page wrote
    // (the notice banner, for instance), and a fresh object on every call
    // would lose that.
    getElementById: (id) => (elementos[id] ||= { id, textContent: '', style: {}, dataset: {}, hidden: true }),
    addEventListener: (ev, f) => { (ouvintes[ev] ||= []).push(f); },
    cookie: 'vpsm_recovery_user=sam',
    hidden: false,
  },
  window: { addEventListener: (ev, f) => { (ouvintes[ev] ||= []).push(f); } },
  location: { protocol: 'https:', host: 'vpsm.example', href: '' },
  requestAnimationFrame: (f) => { timers.push(f); return timers.length; },
  cancelAnimationFrame: () => {},
  setTimeout, clearTimeout, setInterval, clearInterval,
  Date, Math, JSON, Uint8Array,
  fetch: () => { sondas++; return Promise.resolve(respostaSonda); },
  confirm: () => true,
};
ctx.window.addEventListener = ctx.window.addEventListener.bind(ctx.window);

const nomes = Object.keys(ctx);
// The client became a FACTORY (the page has two terminals: the host shell and
// Claude, on an independent connection). The harness instantiates one and
// exercises it — testing the factory tests both, which is why it exists.
try {
  new Function(...nomes, script + '\n;this.__cria = criaTerminal;').call(ctx, ...nomes.map((n) => ctx[n]));
} catch (e) {
  no('the page script does not run: ' + e.message);
  process.exit(1);
}
if (typeof ctx.__cria !== 'function') { no('criaTerminal does not exist — the two terminals would drift apart again'); process.exit(1); }
sockets.length = 0;
const inst = ctx.__cria({ caminho: '/recovery/ws/pty', elemento: null, pilula: null, rotulo: 'host' });
inst.abre();
const st = inst.st, envia = inst.envia;
const espera = (ms) => new Promise((r) => setTimeout(r, ms));
const saida = () => escrito.join('');

// ── 1. connects on its own when the page loads ──────────────────────────────
sockets.length === 1 ? ok('opens the connection when the page loads') : no('no connection was opened at all');
sockets[0].abre();

// ── 2. binary input, no per-keystroke envelope ──────────────────────────────
{
  escrito.length = 0;
  term._dados('ls');
  const b = sockets[0].enviados.slice(-1)[0];
  (b instanceof Uint8Array && new TextDecoder().decode(b) === 'ls')
    ? ok('the keystroke goes out raw, in a binary frame')
    : no('it did not send binary: ' + JSON.stringify(String(b)));
}

// ── 3. the outage: reconnects AND does not swallow what was typed ───────────
{
  sockets[0].derruba(1006);
  escrito.length = 0;
  term._dados('reboot');
  const naFila = (st.outbox || []).join('');
  const pintou = saida().includes('reboot') && saida().includes('\x1b[2m');
  (naFila === 'reboot' && pintou)
    ? ok('connection down: keeps the keystroke AND shows it on screen (dimmed)')
    : no(`keystroke lost or screen dead (queue=${JSON.stringify(naFila)}, output=${JSON.stringify(saida())})`);
}
{
  await espera(700);
  sockets.length >= 2
    ? ok('reconnects on its own after the outage (the worst hole: the session died until a reload)')
    : no('did NOT reconnect — the recovery session dies on one network blink');
}

// ── 4. on return, erase the guess and flush the queue in order ──────────────
{
  const s2 = sockets[sockets.length - 1];
  escrito.length = 0;
  s2.abre();
  const enviado = s2.enviados.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('|');
  (saida().includes('\b \b') && enviado.includes('reboot'))
    ? ok('on return: erases the local echo and sends the queue (no duplicated text)')
    : no('reconnect did not clear/flush: output=' + JSON.stringify(saida()) + ' sent=' + enviado);
}

// ── 5. a password is never echoed locally ───────────────────────────────────
{
  const s = sockets[sockets.length - 1];
  s.derruba(1006);
  linhaSobCursor = '[sudo] password for sam:';
  escrito.length = 0;
  term._dados('minhasenha');
  saida() === ''
    ? ok('password prompt: no echo (the password never reaches the screen)')
    : no('ECHOED the password: ' + JSON.stringify(saida()));
  linhaSobCursor = 'root@vps:/opt#';
  await espera(700);
}

// ── 6. backpressure: without it, heavy output corrupts the screen ───────────
{
  const s = sockets[sockets.length - 1];
  s.abre();
  s.enviados.length = 0;
  const bloco = new Uint8Array(300 * 1024);
  s.recebe(bloco.buffer ? bloco : bloco);
  const pediu = s.enviados.some((x) => typeof x === 'string' && x.includes('pause'));
  pediu
    ? ok('heavy output makes the client ask for a PAUSE (the server stops reading the PTY)')
    : no('it never asks for a pause — the xterm buffer blows and it DROPS bytes');
}

// ── 7. an expired session is not a dropped network ──────────────────────────
{
  const s = sockets[sockets.length - 1];
  respostaSonda = { status: 0, type: 'opaqueredirect' };
  sondas = 0;
  s.derruba(1006);
  await espera(500);
  const antes = sockets.length;
  const houveSonda = sondas > 0;
  sockets[sockets.length - 1].derruba(1006);   // the 2nd attempt fires the probe
  await espera(800);
  (houveSonda || sondas > 0)
    ? ok('after failing again, it ASKS whether the session is still valid (HEAD)')
    : no('it never asks — it would loop reconnecting forever with an expired session');
  await espera(300);
  // The notice goes to the page BANNER, NOT inside the terminal. Writing to
  // xterm dirtied the scrollback of the conversation with Claude — and a notice
  // about the page does not belong to the content of the session, which is still
  // alive on the server. The test asserts the new place, and a clean terminal.
  const faixa = elementos['faixa'] || {};
  (String(faixa.textContent || '').includes('expired') && faixa.hidden === false)
    ? ok('an expired session is spelled out in the page banner')
    : no('no warning in the banner: ' + JSON.stringify(faixa.textContent));
  !saida().includes('expired')
    ? ok('the notice is NOT written into the terminal (Claude scrollback stays clean)')
    : no('the notice dirties the terminal again');
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
