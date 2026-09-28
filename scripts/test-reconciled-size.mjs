#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const shell = readFileSync(join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js'), 'utf8');
const recovery = readFileSync(join(root, 'internal/webassets/web/recovery-term.html'), 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-reconciled-size ===');

{
  const m = shell.match(/state\._assertSize = \(reason\) => \{([\s\S]*?)\n {8}\};/);
  if (!m) {
    no('could not find _assertSize in the panel client — the size is an event again');
  } else {
    const body = m[1];
    (/let cols = t\.cols, rows = t\.rows/.test(body) && /proposeDimensions\(\)/.test(body))
      ? ok('panel: reasserts by reading the CURRENT xterm size (the natural window, measured right then)')
      : no('panel: reasserts a value captured earlier — with the window hidden it is stale');
    /cols >= 2 && rows >= 1/.test(body)
      ? ok('panel: never reasserts a degenerate size')
      : no('panel: no guard against a degenerate size');
  }

  /state\.ws\.send\(JSON\.stringify\(\{type:'ping'[\s\S]{0,600}?_assertSize\('heartbeat'\)/.test(shell)
    ? ok('panel: the heartbeat reasserts the size (repaired within ≤1 cycle)')
    : no('panel: the heartbeat does not reassert — a divergence would be permanent');

  /window\.addEventListener\('focus', \(\) => this\._reconcileSizes\(\)\)/.test(shell)
    ? ok("panel: switching WINDOWS triggers reconciliation (the reported case)")
    : no('panel: nothing reconciles on coming back to the window');
  /visibilitychange[\s\S]{0,200}_reconcileSizes\(\)/.test(shell)
    ? ok('panel: coming back to the tab reconciles too')
    : no('panel: a returning tab does not reconcile');

  const r = shell.match(/_reconcileSizes\(\)\{([\s\S]*?)\n {4}\},/);
  if (!r) {
    no('could not find _reconcileSizes');
  } else {
    const i = r[1].indexOf('_safeFit'), j = r[1].indexOf('_assertSize');
    (i >= 0 && j > i)
      ? ok('panel: reconciliation fits BEFORE asserting (the order is what makes the number right)')
      : no('panel: it asserts before fitting — it would reassert the old size');
  }
}

{
  /_gridSession = \{ cols: _av\.cols, rows: _av\.rows \}/.test(shell)
    ? ok('panel: the size notice is STORED, not merely applied')
    : no('panel: the notice is not stored — the fit undoes it and nothing re-notifies');

  const sf = shell.match(/_safeFit\(fit\)\{([\s\S]*?)\n {4}\},/);
  if (!sf) {
    no('could not find _safeFit');
  } else {
    /_gridSession/.test(sf[1])
      ? ok('panel: the fit obeys the session grid')
      : no('panel: the fit ignores the session grid — it fights the notice again');
    /_assertSize/.test(sf[1])
      ? ok('panel: even while obeying, the fit asserts the natural window (that is how the session grows back)')
      : no('panel: obeying without asserting the natural window pins the session to the size of whoever left');
  }
}

{
  const sf = shell.match(/_safeFit\(fit\)\{([\s\S]*?)\n {4}\},/);
  if (!sf) {
    no('could not find _safeFit');
  } else {
    /_lastFit1col/.test(sf[1])
      ? ok('panel: a ±1 column does not undo the previous correction (damper on the oscillator)')
      : no('panel: the ±1 column oscillator is back — every round trip is a reflow and a SIGWINCH');
    /dc === -dc|\.dc === -dc/.test(sf[1])
      ? ok('panel: the damper compares the DIRECTION of the correction (it only blocks the undo)')
      : no('panel: the damper ignores the direction — it would block a legitimate correction');
  }
}

{
  /ws\/shell\?size=1&frame=1/.test(shell)
    ? ok('panel: asks for frame=1 in the socket URL')
    : no('panel: no frame=1 — the smaller window shrinks the shared session again');
}

{
  /function assertSize\(\)[\s\S]{0,400}?const cols = term\.cols, rows = term\.rows/.test(recovery)
    ? ok('recovery: reasserts by reading the CURRENT xterm size')
    : no('recovery: no reassertion of the current size');
  /ping[\s\S]{0,400}?assertSize\(\);/.test(recovery)
    ? ok('recovery: the heartbeat reasserts the size')
    : no('recovery: the heartbeat does not reassert');
  /window\.addEventListener\('focus', reconcileSize\)/.test(recovery)
    ? ok('recovery: switching WINDOWS triggers reconciliation')
    : no('recovery: nothing reconciles on coming back to the window');
  const r = recovery.match(/function reconcileSize\(\) \{([\s\S]*?)\n\}/);
  if (!r) {
    no('recovery: could not find reconcileSize');
  } else {
    const i = r[1].indexOf('safeFit'), j = r[1].indexOf('assertSize');
    (i >= 0 && j > i)
      ? ok('recovery: reconciliation fits BEFORE asserting')
      : no('recovery: it asserts before fitting');
  }
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
