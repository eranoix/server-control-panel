#!/usr/bin/env node
// test-pane-outbox.mjs — regression guard: typing during a connection outage
// (a deploy) must never be lost.
//
// THE BUG: `term.onData` did `if (ws.readyState===1) ws.send(...)` and, outside
// that, DISCARDED the keystroke silently. A deploy takes the server down for
// ~2s; everything the user typed in that window vanished — a whole command was
// typed and nothing happened.
//
// This test extracts `_paneSendInput` FROM THE REAL FILE (not a copy, which
// would drift) and exercises it against a fake WebSocket. It covers the happy
// path, the outage, the flush order and the byte ceiling.
//
// Usage: node scripts/test-pane-outbox.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
// Optional target via argv: lets the test run against a MUTATED COPY of
// 00-shell.js and prove it fails when the bug comes back (a test that only ever
// passes proves nothing).
const alvo = process.argv[2] || join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(alvo, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };

console.log('=== test-pane-outbox ===');

// ── extracts the real function ──────────────────────────────────────────────
// `new Function` here is NOT injection: the only source is the 00-shell.js of
// THIS repo, read from disk — the same code already running in the browser.
// Testing the real text is the point: a copy of the logic inside the test would
// drift from the product unnoticed (that is how an earlier bug in the agent
// coordination tool stayed invisible).
// The typing path became a FAMILY of functions (send, flush, offline queue,
// local echo, latency probe). We extract ALL of them and assemble the object —
// testing only _paneSendInput would test half a truth: the "never drops a
// keystroke" guarantee is now split between it and _paneTxFlush.
const extrai = (nome, args) => {
  const re = new RegExp('^ {4}' + nome + '\\(' + args.join(', ').replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const mm = src.match(re);
  if (!mm) { console.log('  ✗ could not extract ' + nome + ' from 00-shell.js'); process.exit(1); }
  return new Function(...args, mm[1]);
};
const app = {
  _renderPaneOverlay(){},
  _paneSendInput: extrai('_paneSendInput', ['pane', 'd']),
  _paneTxFlush: extrai('_paneTxFlush', ['pane']),
  _paneEnfileiraOffline: extrai('_paneEnfileiraOffline', ['pane', 'd']),
  _paneEcoOffline: extrai('_paneEcoOffline', ['pane', 'd']),
  _pareceLinhaDeSenha: extrai('_pareceLinhaDeSenha', ['pane']),
  _marcaEnvio: extrai('_marcaEnvio', ['pane', 'd']),
  // Sending now also fires the predictive echo. It is inert in the cases in
  // this file (a pane with no term, or a socket that is down), but it has to
  // exist — this is the real path we exercise, not a pruned version of it. The
  // boundaries of the prediction have their own suite: test-eco-preditivo.mjs.
  _preveEco: extrai('_preveEco', ['pane', 'd']),
  _podePrever: extrai('_podePrever', ['pane', 'd']),
};
const _paneSendInput = (pane, d) => app._paneSendInput(pane, d);

// The flush became a microtask (it coalesces a burst from the same tick without
// delaying anyone), so whoever asserts on what "was sent" must let it drain.
const tick = () => new Promise(r => setTimeout(r, 0));

// ── fake WebSocket ──────────────────────────────────────────────────────────
// Sending is now BINARY (no JSON envelope): the server writes every binary frame
// straight into the PTY. The fake decodes it so the test can keep reasoning in
// text — and the check that it really is binary lives in case 7.
const dec = new TextDecoder();
const novoPane = (readyState) => ({
  id: 'p1',
  ws: {
    readyState, enviados: [], cru: [],
    send(b){ this.cru.push(b); this.enviados.push(typeof b === 'string' ? b : dec.decode(b)); },
  },
});

// Fake terminal: only what the local echo uses — write and the cursor line.
const termFalso = (escrito, linha) => ({
  write(x){ escrito.push(x); },
  buffer: { active: { baseY: 0, cursorY: 0,
    getLine: () => ({ translateToString: () => linha }) } },
});

// 1) socket open → goes straight out, nothing queued
{
  const p = novoPane(1);
  const r = _paneSendInput(p, 'ls');
  await tick();
  r === true && p.ws.enviados.join('') === 'ls' && !p._outbox
    ? ok('socket open: sends straight out, nothing queued')
    : no('socket open: wrong behaviour');
}

// 2) socket down → does NOT drop: it queues instead of discarding (the bug)
{
  const p = novoPane(3);                       // 3 = CLOSED
  const r = _paneSendInput(p, 'meu comando');
  r === false && (p._outbox||[]).join('') === 'meu comando' && p.ws.enviados.length === 0
    ? ok('socket down: queues instead of discarding (the original bug)')
    : no('socket down: the keystroke was LOST');
}

// 3) order preserved — the typing is reassembled exactly
{
  const p = novoPane(0);                       // 0 = CONNECTING
  for (const c of ['g','i','t',' ','s','t','a','t','u','s','\r']) _paneSendInput(p, c);
  (p._outbox||[]).join('') === 'git status\r'
    ? ok('order preserved during the outage ("git status\\r")')
    : no('order scrambled/lost: ' + JSON.stringify((p._outbox||[]).join('')));
}

// 4) byte ceiling: does not grow unbounded and MARKS the drop (never silent)
{
  const p = novoPane(3);
  _paneSendInput(p, 'x'.repeat(128 * 1024));   // fills the ceiling exactly
  const antes = p._outboxBytes;
  _paneSendInput(p, 'y');                      // overflows it
  p._outboxDropped === true && p._outboxBytes === antes
    ? ok('128KB ceiling: stops growing and marks the drop so it can warn')
    : no('byte ceiling not honoured');
}

// 5) empty/null input does not dirty the queue
{
  const p = novoPane(3);
  _paneSendInput(p, ''); _paneSendInput(p, null); _paneSendInput(p, undefined);
  !p._outbox ? ok('empty/null input ignored') : no('empty input dirtied the queue');
}

// 6) a send that throws (socket dying between check and send) is queued
{
  const p = novoPane(1);
  p.ws.send = () => { throw new Error('socket morreu'); };
  // The old code called send() WITHOUT try/catch: the exception escaped and
  // took down the whole typing handler. We catch it here to report a readable
  // failure instead of aborting the suite halfway.
  // The send now happens in a microtask, so the return value is no longer where
  // the failure shows up — the GUARANTEE (the keystroke does not vanish) is what
  // the test has to assert, and it still holds: the catch in the flush puts the
  // text back in the queue.
  let explodiu = false;
  try { _paneSendInput(p, 'abc'); } catch(_) { explodiu = true; }
  await tick();
  (!explodiu && p._outbox && p._outbox.join('') === 'abc')
    ? ok('send that throws: lands in the queue instead of vanishing')
    : no(explodiu ? 'send that throws: the exception escaped and would take typing down' : 'send that throws: keystroke lost');
}

// ── binary frames ───────────────────────────────────────────────────────────
// 7) the keystroke goes out RAW, in a binary frame: the JSON envelope cost ~30
//    bytes per character, and on a lossy link every extra byte is one more
//    chance of stalling the whole TCP queue.
{
  const p = novoPane(1);
  _paneSendInput(p, 'a');
  await tick();
  const b = p.ws.cru[0];
  (b instanceof Uint8Array && b.length === 1 && !/type/.test(dec.decode(b)))
    ? ok('the send is raw binary (no JSON envelope per keystroke)')
    : no('the per-keystroke JSON envelope is back: ' + JSON.stringify(String(b)));
}

// 8) a burst from the same tick (auto-repeat, paste, IME) becomes ONE frame —
//    and even so nothing is reordered or delayed by a timer.
{
  const p = novoPane(1);
  for (const c of ['g','i','t',' ','p','u','l','l']) _paneSendInput(p, c);
  await tick();
  (p.ws.cru.length === 1 && p.ws.enviados.join('') === 'git pull')
    ? ok('a burst from one tick becomes 1 frame, in the right order')
    : no('coalescing failed: ' + p.ws.cru.length + ' frames, ' + JSON.stringify(p.ws.enviados.join('')));
}

// 9) with no socket, the keystroke APPEARS on screen (dimmed) instead of
//    vanishing — exactly the "sometimes it types nothing" from the report.
{
  const escrito = [];
  const p = novoPane(3);
  p.term = termFalso(escrito, '$ ');
  _paneSendInput(p, 'ls');
  const saida = escrito.join('');
  (saida.includes('ls') && saida.includes('\x1b[2m') && p._ecoPintado === 2)
    ? ok('no socket: the dimmed local echo shows up on screen')
    : no('with no socket the screen went dead: ' + JSON.stringify(saida));
}

// 10) the local echo must NEVER leak a password: not when the line is a
//     password prompt, not when the server had already stopped echoing.
{
  // The prompts that really show up day to day — the sudo one has the keyword
  // FAR from the colon, and it failed the first version of the rule.
  for (const prompt of [
    '[sudo] password for sam:',
    'Password:',
    "Enter passphrase for key '/root/.ssh/id_rsa':",
    'Senha:',
    "Password for 'https://github.com':",
  ]) {
    const escrito = [];
    const p = novoPane(3);
    p.term = termFalso(escrito, prompt);
    _paneSendInput(p, 'segredo');
    escrito.length === 0
      ? ok('does not echo at ' + JSON.stringify(prompt))
      : no('ECHOED the password at ' + JSON.stringify(prompt) + ': ' + JSON.stringify(escrito.join('')));
  }
  // And the converse: an ordinary prompt has to keep echoing, otherwise the
  // protection would have eaten the whole feature.
  {
    const escrito = [];
    const p = novoPane(3);
    p.term = termFalso(escrito, 'sam@vps:/opt/panel$ ');
    _paneSendInput(p, 'ls');
    escrito.join('').includes('ls')
      ? ok('a normal prompt keeps echoing (the protection did not eat the feature)')
      : no('a normal prompt stopped echoing — the local echo became useless');
  }

  const escrito2 = [];
  const p2 = novoPane(3);
  p2.term = termFalso(escrito2, '$ ');
  p2._servidorEcoa = false;          // server stopped echoing before the outage
  _paneSendInput(p2, 'segredo');
  escrito2.length === 0
    ? ok('server was not echoing before the outage: no echo (the mosh rule)')
    : no('ECHOED while the server was in no-echo mode: ' + JSON.stringify(escrito2.join('')));
}

// 11) control characters are not guessed: Enter/Ctrl-* have effects only the
//     shell on the other end knows. They go to the queue silently.
{
  const escrito = [];
  const p = novoPane(3);
  p.term = termFalso(escrito, '$ ');
  _paneSendInput(p, 'ok\r');
  const saida = escrito.join('');
  (saida.includes('ok') && !saida.includes('\r') && (p._outbox||[]).join('') === 'ok\r')
    ? ok('Enter is not echoed locally, but reaches the queue intact')
    : no('improper control-character echo: ' + JSON.stringify(saida));
}

// ── repaint decision on reattach ────────────────────────────────────────────
// The wobble (-8 cols/-4 rows) was the visible "refresh" on every deploy. It now
// fires only with PROOF that the screen is unusable. This block makes sure the
// proof is right in both directions: do not escalate when there is content (no
// more jolt) and do escalate when the screen is black or when it CANNOT be
// decided (otherwise the original bug returns, and that is worse than a jolt).
{
  const mp = src.match(/^ {4}_viewportPrecisaRepaint\(term\)\{\n([\s\S]*?)^ {4}\},$/m);
  if (!mp) { no('could not extract _viewportPrecisaRepaint'); }
  else {
    const precisa = new Function('term', mp[1]);
    const termCom = (linhas) => ({
      rows: linhas.length,
      buffer: { active: { viewportY: 0, getLine: (i) => linhas[i] === undefined ? null
        : { translateToString: () => linhas[i] } } },
    });
    precisa(termCom(['', '  $ ls', ''])) === false
      ? ok('screen WITH content → no escalation (no more jolt on deploy)')
      : no('it would escalate with a good screen — the jolt would be back');
    precisa(termCom(['', '   ', ''])) === true
      ? ok('blank screen → escalates to the wobble (the original bug covered)')
      : no('a black screen would NOT escalate — the original bug is back');
    precisa({ rows: 3, buffer: null }) === true
      ? ok('no buffer (the API changed) → escalate; when in doubt, keep the old screen')
      : no('no buffer would not escalate — that risks a black screen');
    precisa({ rows: 3, buffer: { active: { viewportY: 0, getLine: () => { throw new Error('x'); } } } }) === true
      ? ok('getLine that throws → escalates instead of assuming a good screen')
      : no('an exception read as a good screen — that risks a black screen');
  }
}

// ── are the fast backoff and the silence still in the code? ─────────────────
// These two checks used to look at the exact SPELLING (`const FAST = [300, 700,
// 1500]`, `QUIET_MS = 6000`) and failed any refactor that preserved the
// guarantee — which is what happened when both became conditional expressions.
// They now evaluate the VALUE: it is the guarantee that must not regress, not
// the way it happens to be written.
{
  const m = src.match(/const FAST = ([^;]+);/);
  if (!m) {
    no('could not find the backoff table');
  } else {
    const fn = new Function('state', 'return ' + m[1] + ';');
    const normal = fn({ _restarting: false });
    Array.isArray(normal) && normal[0] <= 300 && normal.length >= 3
      ? ok('fast backoff present (a ~2s deploy is not a 4–7s wait)')
      : no('fast backoff gone — the doubling from 1s is back: ' + JSON.stringify(normal));
  }
}
{
  const m = src.match(/const QUIET_MS = ([^;]+);/);
  if (!m) {
    no('could not find QUIET_MS');
  } else {
    const fn = new Function('state', 'return ' + m[1] + ';');
    const q = fn({ _restarting: false });
    q >= 3000 && /noticeTimer/.test(src)
      ? ok('the terminal notice is deferred (a short outage stays silent)')
      : no('it writes to the terminal on every outage again (QUIET_MS=' + q + ')');
  }
}
/state\._outbox\.join\(''\)/.test(src)
  ? ok('queue flush present in onopen')
  : no('the queue flush is gone from onopen — the queue would fill and never be sent');

/term\.write\('\\x18'\)/.test(src)
  ? ok('CAN (0x18) aborting a truncated sequence — the surgical fix')
  : no('CAN is gone: a truncated sequence would jam the parser again');
// The wobble may only exist INSIDE the escalation path. If it shows up loose in
// onopen again, the jolt on every deploy comes back with it.
{
  const mr = src.match(/^ {4}_reattachRepaint\(state\)\{\n([\s\S]*?)^ {4}\},$/m);
  const total = (src.match(/resize\(rc - 8, rr - 4\)/g) || []).length;
  const dentro = mr ? (mr[1].match(/resize\(rc - 8, rr - 4\)/g) || []).length : 0;
  (total === 1 && dentro === 1)
    ? ok('the wobble exists only inside the escalation (not on every reattach)')
    : no(`wobble outside the escalation (total=${total}, inside=${dentro}) — the jolt is back`);
}

// ── atomic repaint on reattach ──────────────────────────────────────────────
// The user reported, with a screenshot, that on every deploy the terminal "wipes
// everything and then loads again". It was not the server (no wobble in the log):
// on reconnect the new dtach client sends SIGWINCH and the TUI app clears the
// screen and repaints — and writing that flow in pieces leaves the MIDDLE of the
// process visible. The hold window keeps the old screen until the frame is whole.
/state\._holdUntil = Date\.now\(\) \+ \d+;/.test(src)
  ? ok('hold window armed on reattach')
  : no('the reattach hold is gone — the user sees the screen wipe and reload again');
/if \(state\._holdUntil\) \{[\s\S]{0,900}?return;   \/\/ keeps queueing, without painting/.test(src)
  ? ok('flushTerm holds the queue during the hold (nothing is discarded)')
  : no('flushTerm does not respect the hold');
// The hold has to be ADAPTIVE, not a fixed timer: the first version held a flat
// 260ms and the window expired in the MIDDLE of the repaint when the app took
// longer — the user saw the fragment. The right criterion is "the burst went quiet".
/quieto < 90/.test(src) && /_lastDataAt/.test(src)
  ? ok('the hold releases on the SILENCE of the burst, not on a fixed timer')
  : no('the hold is a fixed timer again — a long repaint shows up half-drawn');
/state\._holdUntil = Date\.now\(\) \+ 1500;/.test(src)
  ? ok('hard 1500ms ceiling (continuous output never freezes the render)')
  : no('no ceiling: continuous output could jam the terminal');
/state\._holdTimer = 0; state\._holdUntil = 0;/.test(src)
  ? ok('the hold is cleared on close (no state stuck between connections)')
  : no('the hold is not cleared on close — the render could freeze');

// ── image paste through the native event ────────────────────────────────────
// navigator.clipboard.read() needs permission and fails with NotAllowedError; the
// 'paste' event already carries the bytes. Text stays with xterm.
/addEventListener\('paste'/.test(src)
  ? ok('native paste listener present (images without needing permission)')
  : no('the native paste listener is gone — pasting an image fails again');
// The GUARANTEE here is "pasting text never becomes an upload", and what holds
// it up is the text/plain early-return in _arquivosDoClipboard. This pin
// asserted instead a literal regex of the listener that first shipped the
// feature — and twice that left it out of step with the product: the filter was
// later widened on purpose (any file type, not only images, so PDF/CSV can go
// to the AI) and that listener was removed, being a duplicate that uploaded the
// file twice. Asserting the mechanism instead of the property turned a correct
// change into a failure.
//
// The in-browser verification of this property lives in
// scripts/test-paste-unico.mjs ("a paste with text/plain stays text").
/_arquivosDoClipboard\(ev\)\{[\s\S]{0,400}?tipos\.includes\('text\/plain'\)\) return null;/.test(src)
  ? ok('pasting text never becomes an upload (text/plain early-return)')
  : no('the text/plain guard is gone — pasting from Excel/Word would upload');
!/\b_pasteImageIfAny\b/.test(src)
  ? ok('no duplicate async image path (avoids a double upload)')
  : no('_pasteImageIfAny is back — risk of a duplicate upload');

console.log('─'.repeat(37));
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail === 0 ? 0 : 1);
