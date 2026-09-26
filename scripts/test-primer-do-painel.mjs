// test-primer-do-painel.mjs
//
// The panel had no way to recover the scrollback when the session was opened on
// another computer. It depended on the block the SERVER re-emits on attach, and
// that block is skipped in exactly the sessions that matter: measured across the
// 28 session logs on this machine, EVERY working session has a repainted stream
// and receives zero bytes of history. Hence the report — "I can only see one page".
//
// The fix is a primer on the client: fetch the RAW log and replay it into xterm
// itself before opening the socket. Three properties to protect, and none of them
// is visible on screen when it is working:
//
//  1. the ORDER (fetch → write → connect). Connecting in parallel overlaps the
//     history with the live stream;
//  2. the time CEILING — history is a comfort, the live session is the reason the
//     screen exists;
//  3. the conditional `replay=0`: only once the primer has actually written, so
//     that the server replay stays the safety net when the fetch fails.

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const shell = readFileSync(join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js'), 'utf8');
const api = readFileSync(join(raiz, 'internal/api/api.go'), 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-primer-do-painel ===');

// The route exists on the server side — without it the primer fetches nothing.
/\/api\/terminal\/log-bruto/.test(api)
  ? ok('server: the /api/terminal/log-bruto route is registered (the fallback)')
  : no('server: the raw-log route is gone — the panel is left without a fallback');
/\/api\/terminal\/historico/.test(api)
  ? ok('server: the /api/terminal/historico route is registered')
  : no('server: the rendered-history route is gone');

const m = shell.match(/const primeEAbre = \(\) => \{([\s\S]*?)\n {6}\};/);
if (!m) {
  no('panel: could not find the primer — history depends on the server alone again');
} else {
  const corpo = m[1];

  // THE ORDER OF THE SOURCES matters: the rendered history first (it is not a
  // replay, so it cannot duplicate or misalign), the raw log only as the fallback
  // for an old session that has no history file yet.
  const iHist = corpo.indexOf("/api/terminal/historico");
  const iCru = corpo.indexOf("/api/terminal/log-bruto");
  (iHist >= 0 && iCru > iHist)
    ? ok('panel: fetches the rendered history first and the raw log as the fallback')
    : no('panel: wrong source order — the raw log must not come before the history');

  // The order: open() has to happen AFTER the write, never in parallel.
  const iEscreve = corpo.indexOf('state.term.write');
  const iSeguir = corpo.indexOf('seguir()');
  const abreSoNoFim = /\.finally\(\(\) => \{ clearTimeout\(teto\); seguir\(\); \}\)/.test(corpo);
  (iEscreve >= 0 && abreSoNoFim)
    ? ok('panel: writes the history and only then connects (the order is what avoids overlap)')
    : no('panel: connects in parallel with the fetch — history and live stream overlap');
  iSeguir >= 0 || no('panel: no opening path');

  /setTimeout\(\(\) => \{[\s\S]{0,120}?seguir\(\);/.test(corpo)
    ? ok('panel: the primer has a time ceiling (a slow server does not become a terminal that never opens)')
    : no('panel: no ceiling — a slow server holds the terminal shut');

  /_primerFeito/.test(corpo)
    ? ok('panel: the primer does not run again on reconnect (it would duplicate what xterm already has)')
    : no('panel: the primer runs again on reconnect');

  /PEDACO/.test(corpo)
    ? ok('panel: writes in chunks (a few MB at once would cost the frame)')
    : no('panel: writes the history in one go');
}

// conditional `replay=0`: only once the primer has written.
/state\._primouOk \? \(opts\.wsPath\.includes\('\?'\)\?'&':'\?'\)\+'replay=0' : ''/.test(shell)
  ? ok("panel: sends replay=0 only once the primer has written (otherwise the server replay is the safety net)")
  : no('panel: unconditional replay=0 — if the fetch fails there is no history at all');

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
