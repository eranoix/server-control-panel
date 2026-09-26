#!/usr/bin/env node
// test-pane-deploy.mjs — what the panes do during a deploy.
//
// The risks this test locks down:
//
//  1. RELOADING IN THE USER'S FACE. Auto-reload exists because an open tab is
//     stuck on the old JS after every deploy. But reloading while somebody is
//     working is worse than the problem it solves — selection, scroll and focus
//     all go. Each guard here is one way that could happen, and each one has its
//     own assert.
//  2. LOSING THE SEND QUEUE. Reloading with a pending outbox throws away what was
//     typed during the outage — it undoes the queue work from the other side.
//  3. TREATING A DEPLOY AS A NETWORK FAILURE. If the client stops recognising
//     close 1012, the deploy goes back to announcing itself as "connection
//     abruptly closed" and to taking 6s longer to come back.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(target, 'utf8');
let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };
console.log('=== test-pane-deploy ===');

function method(name) {
  const re = new RegExp('\\n    (?:async )?' + name + '\\(([\\s\\S]*?)\\n    \\},');
  const m = src.match(re);
  if (!m) { no('could not find the method ' + name); process.exit(1); }
  return m[0].replace(/^\n/, '') + '';
}

// ── 1. _trySafeReload: the guard matrix ───────────────────────────────
function scenario({ hidden, hiddenSince, lastTyping, outbox = [], dialog = false, ligado: on = true, nova = true }) {
  const now = 1_000_000_000;
  const doc = {
    get hidden(){ return hidden; },
    querySelector: (sel) => (dialog && sel.includes('dialog')) ? {} : null,
  };
  let reloaded = false;
  const body = method('_trySafeReload');
  const factory = new Function('document', 'location', 'Date', 'clearInterval', 'setInterval',
    'return {' + body + '\n};');
  const obj = factory(doc, { reload(){ reloaded = true; } },
    { now: () => now }, () => {}, () => 1);
  Object.assign(obj, {
    newVersionAvailable: nova,
    hostTermAutoReload: on,
    _hiddenSince: hiddenSince === undefined ? 0 : now - hiddenSince,
    _lastTyping: lastTyping === undefined ? 0 : now - lastTyping,
    _reloadTimer: 1,
    terms: { panes: outbox.map(n => ({ _outbox: n ? new Array(n).fill('x') : [] })) },
  });
  obj._trySafeReload();
  return reloaded;
}

// The windows went from 20s/90s up to 10min/10min. At ~40-90 deploys a day, the
// old ones turned any quick look at another tab into a reload — and a reload, on
// a bad link, is what makes the interface unusable.
const ALL_CLEAR = { hidden: true, hiddenSince: 15 * 60_000, lastTyping: 15 * 60_000, outbox: [0, 0] };

scenario(ALL_CLEAR) === true
  ? ok('tab idle for 15min, queue empty → reloads (the mechanism is still alive)')
  : no('did not reload even in the completely free scenario — the mechanism is dead');

// The regression that forced the wider windows: a one-minute alt-tab can no
// longer hand back a reloaded page.
scenario({ ...ALL_CLEAR, hiddenSince: 60_000 }) === false
  ? ok('hidden for 1min → does NOT reload (this was the constant-reload regression)')
  : no('went back to reloading after a short alt-tab');

scenario({ ...ALL_CLEAR, lastTyping: 5 * 60_000 }) === false
  ? ok('typed 5min ago → does NOT reload (recent work still wins)')
  : no('would reload over work from 5 minutes ago');

scenario({ ...ALL_CLEAR, hidden: false }) === false
  ? ok('tab VISIBLE → never reloads (the guard that matters)')
  : no('CRITICAL: would reload with the tab in front of the user');

scenario({ ...ALL_CLEAR, hiddenSince: 5_000 }) === false
  ? ok('hidden for only 5s (quick alt-tab) → does not reload')
  : no('would reload during a short alt-tab — the user comes back to a jolt');

scenario({ ...ALL_CLEAR, lastTyping: 10_000 }) === false
  ? ok('typed 10s ago → does not reload (work in progress wins)')
  : no('would reload with the user mid-command');

scenario({ ...ALL_CLEAR, outbox: [0, 3] }) === false
  ? ok('outbox with pending input → does not reload (it does not drop what was typed during the outage)')
  : no('CRITICAL: would reload and throw the send queue away');

scenario({ ...ALL_CLEAR, dialog: true }) === false
  ? ok('dialog open → does not reload')
  : no('would reload with a dialog open');

scenario({ ...ALL_CLEAR, ligado: false }) === false
  ? ok('toggle off → does not reload')
  : no('ignored the user toggle');

scenario({ ...ALL_CLEAR, nova: false }) === false
  ? ok('no new version → does not reload for nothing')
  : no('reloaded without a new version');

// A first pass with the tab freshly hidden only STAMPS the time, it does not reload.
scenario({ ...ALL_CLEAR, hiddenSince: undefined }) === false
  ? ok('the first check with the tab hidden only stamps the instant')
  : no('reloaded on the first check, without waiting out the interval');

// ── 2. Recognising the announced restart ───────────────────────────────────
{
  const m = src.match(/const isRestart = \(([^)]*)\);/);
  m && m[1].includes('1012')
    ? ok('close 1012 (Service Restart) is recognised as a deploy')
    : no('the client no longer recognises 1012 — a deploy would look like a network failure again');
  m && m[1].includes('1001')
    ? ok('close 1001 (going away) counts as a restart too')
    : no('1001 is not handled — a proxy in front restarting would fall into the error path');
}
{
  const m = src.match(/const FAST = ([^;]+);/);
  if (!m) no('could not find the backoff table');
  else {
    const fn = new Function('state', 'return ' + m[1].replace(/^state\._restarting \? /, 'state._restarting ? ') + ';');
    const withRestart = fn({ _restarting: true });
    const noRestart = fn({ _restarting: false });
    withRestart[0] < noRestart[0]
      ? ok(`an announced restart probes sooner (${withRestart[0]}ms vs ${noRestart[0]}ms)`)
      : no('backoff did not get more aggressive on an announced restart');
    withRestart.length > noRestart.length
      ? ok('an announced restart insists more times before going exponential')
      : no('restart did not gain the extra fast attempts');
  }
}
{
  const m = src.match(/const QUIET_MS = ([^;]+);/);
  if (!m) no('could not find QUIET_MS');
  else {
    const fn = new Function('state', 'return ' + m[1] + ';');
    fn({ _restarting: true }) > fn({ _restarting: false })
      ? ok('an announced restart does not write "no connection" to the terminal early (wider window)')
      : no('a deploy would go back to polluting the scrollback with a failure notice');
    fn({ _restarting: true }) < 60000
      ? ok('but a restart that hangs still warns (the window is not infinite)')
      : no('a hung restart would stay silent forever');
  }
}

// ── 3. Hygiene: one build check per deploy, not one per pane ───────────────
/_buildCheckAt/.test(src) && /_buildCheckAt\) < 3000/.test(src)
  ? ok('the version check is single-flight (N panes reconnecting ≠ N /api/health)')
  : no('each pane would fire its own /api/health the instant the server comes up');

// ── 4. The server really does have to warn ─────────────────────────────────
{
  const main = readFileSync(join(root, 'cmd/server/main.go'), 'utf8');
  // Look for the CALL, not the mention: the comment just above names srv.Shutdown
  // and a naive indexOf matches that instead, inverting the order and failing
  // correct code.
  const idxWarn = main.indexOf('ptysvc.NotifyRestart(');
  const idxShutdown = main.search(/^\s*_ = srv\.Shutdown\(/m);
  idxWarn > 0 && idxWarn < idxShutdown
    ? ok('main.go warns the terminals BEFORE taking the HTTP server down')
    : no('the restart notice does not happen before the shutdown — it would arrive too late');
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
