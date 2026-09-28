import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const shell = readFileSync(join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js'), 'utf8');
const api = readFileSync(join(root, 'internal/api/api.go'), 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-panel-primer ===');

/\/api\/terminal\/raw-log/.test(api)
  ? ok('server: the /api/terminal/raw-log route is registered (the fallback)')
  : no('server: the raw-log route is gone — the panel is left without a fallback');
/\/api\/terminal\/history/.test(api)
  ? ok('server: the /api/terminal/history route is registered')
  : no('server: the rendered-history route is gone');

const m = shell.match(/const primeAndOpen = \(\) => \{([\s\S]*?)\n {6}\};/);
if (!m) {
  no('panel: could not find the primer — history depends on the server alone again');
} else {
  const body = m[1];

  const iHist = body.indexOf("/api/terminal/history");
  const iRaw = body.indexOf("/api/terminal/raw-log");
  (iHist >= 0 && iRaw > iHist)
    ? ok('panel: fetches the rendered history first and the raw log as the fallback')
    : no('panel: wrong source order — the raw log must not come before the history');

  const iWrite = body.indexOf('state.term.write');
  const iFollow = body.indexOf('follow()');
  const opensOnlyAtEnd = /\.finally\(\(\) => \{ clearTimeout\(cap\); follow\(\); \}\)/.test(body);
  (iWrite >= 0 && opensOnlyAtEnd)
    ? ok('panel: writes the history and only then connects (the order is what avoids overlap)')
    : no('panel: connects in parallel with the fetch — history and live stream overlap');
  iFollow >= 0 || no('panel: no opening path');

  /setTimeout\(\(\) => \{[\s\S]{0,120}?follow\(\);/.test(body)
    ? ok('panel: the primer has a time ceiling (a slow server does not become a terminal that never opens)')
    : no('panel: no ceiling — a slow server holds the terminal shut');

  /_primerDone/.test(body)
    ? ok('panel: the primer does not run again on reconnect (it would duplicate what xterm already has)')
    : no('panel: the primer runs again on reconnect');

  /CHUNK/.test(body)
    ? ok('panel: writes in chunks (a few MB at once would cost the frame)')
    : no('panel: writes the history in one go');
}

/state\._primedOk \? \(opts\.wsPath\.includes\('\?'\)\?'&':'\?'\)\+'replay=0' : ''/.test(shell)
  ? ok("panel: sends replay=0 only once the primer has written (otherwise the server replay is the safety net)")
  : no('panel: unconditional replay=0 — if the fetch fails there is no history at all');

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
