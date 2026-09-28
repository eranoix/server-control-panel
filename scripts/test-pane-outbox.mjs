#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js');
const src = readFileSync(target, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };

console.log('=== test-pane-outbox ===');

const extract = (name, args) => {
  const re = new RegExp('^ {4}' + name + '\\(' + args.join(', ').replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const mm = src.match(re);
  if (!mm) { console.log('  ✗ could not extract ' + name + ' from 00-shell.js'); process.exit(1); }
  return new Function(...args, mm[1]);
};
const app = {
  _renderPaneOverlay(){},
  _paneSendInput: extract('_paneSendInput', ['pane', 'd']),
  _paneTxFlush: extract('_paneTxFlush', ['pane']),
  _paneEnqueueOffline: extract('_paneEnqueueOffline', ['pane', 'd']),
  _paneEchoOffline: extract('_paneEchoOffline', ['pane', 'd']),
  _looksLikePasswordLine: extract('_looksLikePasswordLine', ['pane']),
  _markSend: extract('_markSend', ['pane', 'd']),
  _predictEcho: extract('_predictEcho', ['pane', 'd']),
  _canPredict: extract('_canPredict', ['pane', 'd']),
};
const _paneSendInput = (pane, d) => app._paneSendInput(pane, d);

const tick = () => new Promise(r => setTimeout(r, 0));

const dec = new TextDecoder();
const newPane = (readyState) => ({
  id: 'p1',
  ws: {
    readyState, sent: [], raw: [],
    send(b){ this.raw.push(b); this.sent.push(typeof b === 'string' ? b : dec.decode(b)); },
  },
});

const fakeTerm = (written, line) => ({
  write(x){ written.push(x); },
  buffer: { active: { baseY: 0, cursorY: 0,
    getLine: () => ({ translateToString: () => line }) } },
});

{
  const p = newPane(1);
  const r = _paneSendInput(p, 'ls');
  await tick();
  r === true && p.ws.sent.join('') === 'ls' && !p._outbox
    ? ok('socket open: sends straight out, nothing queued')
    : no('socket open: wrong behaviour');
}

{
  const p = newPane(3);
  const r = _paneSendInput(p, 'my command');
  r === false && (p._outbox||[]).join('') === 'my command' && p.ws.sent.length === 0
    ? ok('socket down: queues instead of discarding (the original bug)')
    : no('socket down: the keystroke was LOST');
}

{
  const p = newPane(0);
  for (const c of ['g','i','t',' ','s','t','a','t','u','s','\r']) _paneSendInput(p, c);
  (p._outbox||[]).join('') === 'git status\r'
    ? ok('order preserved during the outage ("git status\\r")')
    : no('order scrambled/lost: ' + JSON.stringify((p._outbox||[]).join('')));
}

{
  const p = newPane(3);
  _paneSendInput(p, 'x'.repeat(128 * 1024));
  const before = p._outboxBytes;
  _paneSendInput(p, 'y');
  p._outboxDropped === true && p._outboxBytes === before
    ? ok('128KB ceiling: stops growing and marks the drop so it can warn')
    : no('byte ceiling not honoured');
}

{
  const p = newPane(3);
  _paneSendInput(p, ''); _paneSendInput(p, null); _paneSendInput(p, undefined);
  !p._outbox ? ok('empty/null input ignored') : no('empty input dirtied the queue');
}

{
  const p = newPane(1);
  p.ws.send = () => { throw new Error('socket died'); };
  let blewUp = false;
  try { _paneSendInput(p, 'abc'); } catch(_) { blewUp = true; }
  await tick();
  (!blewUp && p._outbox && p._outbox.join('') === 'abc')
    ? ok('send that throws: lands in the queue instead of vanishing')
    : no(blewUp ? 'send that throws: the exception escaped and would take typing down' : 'send that throws: keystroke lost');
}

{
  const p = newPane(1);
  _paneSendInput(p, 'a');
  await tick();
  const b = p.ws.raw[0];
  (b instanceof Uint8Array && b.length === 1 && !/type/.test(dec.decode(b)))
    ? ok('the send is raw binary (no JSON envelope per keystroke)')
    : no('the per-keystroke JSON envelope is back: ' + JSON.stringify(String(b)));
}

{
  const p = newPane(1);
  for (const c of ['g','i','t',' ','p','u','l','l']) _paneSendInput(p, c);
  await tick();
  (p.ws.raw.length === 1 && p.ws.sent.join('') === 'git pull')
    ? ok('a burst from one tick becomes 1 frame, in the right order')
    : no('coalescing failed: ' + p.ws.raw.length + ' frames, ' + JSON.stringify(p.ws.sent.join('')));
}

{
  const written = [];
  const p = newPane(3);
  p.term = fakeTerm(written, '$ ');
  _paneSendInput(p, 'ls');
  const output = written.join('');
  (output.includes('ls') && output.includes('\x1b[2m') && p._echoPainted === 2)
    ? ok('no socket: the dimmed local echo shows up on screen')
    : no('with no socket the screen went dead: ' + JSON.stringify(output));
}

{
  for (const prompt of [
    '[sudo] password for sam:',
    'Password:',
    "Enter passphrase for key '/root/.ssh/id_rsa':",
    'Enter PIN:',
    "Password for 'https://github.com':",
  ]) {
    const written = [];
    const p = newPane(3);
    p.term = fakeTerm(written, prompt);
    _paneSendInput(p, 'secret');
    written.length === 0
      ? ok('does not echo at ' + JSON.stringify(prompt))
      : no('ECHOED the password at ' + JSON.stringify(prompt) + ': ' + JSON.stringify(written.join('')));
  }
  {
    const written = [];
    const p = newPane(3);
    p.term = fakeTerm(written, 'sam@vps:/opt/panel$ ');
    _paneSendInput(p, 'ls');
    written.join('').includes('ls')
      ? ok('a normal prompt keeps echoing (the protection did not eat the feature)')
      : no('a normal prompt stopped echoing — the local echo became useless');
  }

  const written2 = [];
  const p2 = newPane(3);
  p2.term = fakeTerm(written2, '$ ');
  p2._serverEchoes = false;
  _paneSendInput(p2, 'secret');
  written2.length === 0
    ? ok('server was not echoing before the outage: no echo (the mosh rule)')
    : no('ECHOED while the server was in no-echo mode: ' + JSON.stringify(written2.join('')));
}

{
  const written = [];
  const p = newPane(3);
  p.term = fakeTerm(written, '$ ');
  _paneSendInput(p, 'ok\r');
  const output = written.join('');
  (output.includes('ok') && !output.includes('\r') && (p._outbox||[]).join('') === 'ok\r')
    ? ok('Enter is not echoed locally, but reaches the queue intact')
    : no('improper control-character echo: ' + JSON.stringify(output));
}

{
  const mp = src.match(/^ {4}_viewportNeedsRepaint\(term\)\{\n([\s\S]*?)^ {4}\},$/m);
  if (!mp) { no('could not extract _viewportNeedsRepaint'); }
  else {
    const needs = new Function('term', mp[1]);
    const termWith = (lines) => ({
      rows: lines.length,
      buffer: { active: { viewportY: 0, getLine: (i) => lines[i] === undefined ? null
        : { translateToString: () => lines[i] } } },
    });
    needs(termWith(['', '  $ ls', ''])) === false
      ? ok('screen WITH content → no escalation (no more jolt on deploy)')
      : no('it would escalate with a good screen — the jolt would be back');
    needs(termWith(['', '   ', ''])) === true
      ? ok('blank screen → escalates to the wobble (the original bug covered)')
      : no('a black screen would NOT escalate — the original bug is back');
    needs({ rows: 3, buffer: null }) === true
      ? ok('no buffer (the API changed) → escalate; when in doubt, keep the old screen')
      : no('no buffer would not escalate — that risks a black screen');
    needs({ rows: 3, buffer: { active: { viewportY: 0, getLine: () => { throw new Error('x'); } } } }) === true
      ? ok('getLine that throws → escalates instead of assuming a good screen')
      : no('an exception read as a good screen — that risks a black screen');
  }
}

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
{
  const mr = src.match(/^ {4}_reattachRepaint\(state\)\{\n([\s\S]*?)^ {4}\},$/m);
  const total = (src.match(/resize\(rc - 8, rr - 4\)/g) || []).length;
  const inside = mr ? (mr[1].match(/resize\(rc - 8, rr - 4\)/g) || []).length : 0;
  (total === 1 && inside === 1)
    ? ok('the wobble exists only inside the escalation (not on every reattach)')
    : no(`wobble outside the escalation (total=${total}, inside=${inside}) — the jolt is back`);
}

/state\._holdUntil = Date\.now\(\) \+ \d+;/.test(src)
  ? ok('hold window armed on reattach')
  : no('the reattach hold is gone — the user sees the screen wipe and reload again');
/if \(state\._holdUntil\) \{[\s\S]{0,900}?return;   \/\/ keeps queueing, without painting/.test(src)
  ? ok('flushTerm holds the queue during the hold (nothing is discarded)')
  : no('flushTerm does not respect the hold');
/quiet < 90/.test(src) && /_lastDataAt/.test(src)
  ? ok('the hold releases on the SILENCE of the burst, not on a fixed timer')
  : no('the hold is a fixed timer again — a long repaint shows up half-drawn');
/state\._holdUntil = Date\.now\(\) \+ 1500;/.test(src)
  ? ok('hard 1500ms ceiling (continuous output never freezes the render)')
  : no('no ceiling: continuous output could jam the terminal');
/state\._holdTimer = 0; state\._holdUntil = 0;/.test(src)
  ? ok('the hold is cleared on close (no state stuck between connections)')
  : no('the hold is not cleared on close — the render could freeze');

/addEventListener\('paste'/.test(src)
  ? ok('native paste listener present (images without needing permission)')
  : no('the native paste listener is gone — pasting an image fails again');
/_clipboardFiles\(ev\)\{[\s\S]{0,400}?types\.includes\('text\/plain'\)\) return null;/.test(src)
  ? ok('pasting text never becomes an upload (text/plain early-return)')
  : no('the text/plain guard is gone — pasting from Excel/Word would upload');
!/\b_pasteImageIfAny\b/.test(src)
  ? ok('no duplicate async image path (avoids a double upload)')
  : no('_pasteImageIfAny is back — risk of a duplicate upload');

console.log('─'.repeat(37));
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail === 0 ? 0 : 1);
