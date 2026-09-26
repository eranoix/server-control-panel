#!/usr/bin/env node
// test-tamanho-reconciliado.mjs — the terminal size has to correct itself, in
// BOTH clients.
//
// The report, with a photo: switching browser windows turned the terminal into
// an unreadable screen — every segment with its first character stuck in the
// last column of the previous line. That is the signature of client and server
// disagreeing by ONE column: the program draws for one width, xterm shows another.
//
// The cause was not the size calculation; it was the size being an EVENT. The
// client only sent when xterm CHANGED, and the send was silently discarded when
// the socket was not open at that instant — precisely what happens with the
// window hidden, where the browser throttles the timer and the watchdog may
// recycle the connection. Once diverged, nothing ever reasserted: the screen
// stayed broken until someone resized the window by hand.
//
// This pin asserts what closes the class of the bug: there is periodic
// reassertion, there is reassertion on coming back to the window, and it covers
// both clients — the panel one (00-shell.js) and the recovery screen one, which
// are separate code on purpose and therefore drift apart if nobody looks.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const shell = readFileSync(join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js'), 'utf8');
const recovery = readFileSync(join(raiz, 'internal/webassets/web/recovery-term.html'), 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-tamanho-reconciliado ===');

// ── the panel client ────────────────────────────────────────────────────────
{
  // The reassertion has to send the CURRENT xterm size, not a value captured
  // when the timer was scheduled: with the window hidden the timer fires minutes
  // later, and the value at that moment is the one that counts.
  const m = shell.match(/state\._afirmaTamanho = \(motivo\) => \{([\s\S]*?)\n {8}\};/);
  if (!m) {
    no('could not find _afirmaTamanho in the panel client — the size is an event again');
  } else {
    const corpo = m[1];
    // Measured RIGHT THEN (nothing captured when the timer was scheduled) and
    // measured from the WINDOW, not from the drawn grid — see just below.
    (/let cols = t\.cols, rows = t\.rows/.test(corpo) && /proposeDimensions\(\)/.test(corpo))
      ? ok('panel: reasserts by reading the CURRENT xterm size (the natural window, measured right then)')
      : no('panel: reasserts a value captured earlier — with the window hidden it is stale');
    /cols >= 2 && rows >= 1/.test(corpo)
      ? ok('panel: never reasserts a degenerate size')
      : no('panel: no guard against a degenerate size');
  }

  // Heartbeat: it is what repairs itself within ≤1 cycle.
  /state\.ws\.send\(JSON\.stringify\(\{type:'ping'[\s\S]{0,600}?_afirmaTamanho\('heartbeat'\)/.test(shell)
    ? ok('panel: the heartbeat reasserts the size (repaired within ≤1 cycle)')
    : no('panel: the heartbeat does not reassert — a divergence would be permanent');

  // Coming back to the window: the exact instant of the report.
  /window\.addEventListener\('focus', \(\) => this\._reconciliaTamanhos\(\)\)/.test(shell)
    ? ok("panel: switching WINDOWS triggers reconciliation (the reported case)")
    : no('panel: nothing reconciles on coming back to the window');
  /visibilitychange[\s\S]{0,200}_reconciliaTamanhos\(\)/.test(shell)
    ? ok('panel: coming back to the tab reconciles too')
    : no('panel: a returning tab does not reconcile');

  // Reconciliation has to FIT before asserting: xterm decides how many columns
  // fit, and only then can the server agree on the right number.
  const r = shell.match(/_reconciliaTamanhos\(\)\{([\s\S]*?)\n {4}\},/);
  if (!r) {
    no('could not find _reconciliaTamanhos');
  } else {
    const i = r[1].indexOf('_safeFit'), j = r[1].indexOf('_afirmaTamanho');
    (i >= 0 && j > i)
      ? ok('panel: reconciliation fits BEFORE asserting (the order is what makes the number right)')
      : no('panel: it asserts before fitting — it would reassert the old size');
  }
}

// ── the grid belongs to the SESSION, and the fit must not undo the notice ───
//
// With another client attached the server puts the PTY at the SMALLEST of the
// windows and tells everyone to draw that grid. The panel obeyed for ~140ms and
// FitAddon undid it — and the server did not re-notify, because as far as IT was
// concerned nothing had changed. The pair stayed recorded in the server log
// ("120x40 → 80x24" followed by "80x24 → 120x40") and from then on the program
// wrapped lines at one width while xterm drew at another.
{
  /_gradeSessao = \{ cols: _av\.cols, rows: _av\.rows \}/.test(shell)
    ? ok('panel: the size notice is STORED, not merely applied')
    : no('panel: the notice is not stored — the fit undoes it and nothing re-notifies');

  const sf = shell.match(/_safeFit\(fit\)\{([\s\S]*?)\n {4}\},/);
  if (!sf) {
    no('could not find _safeFit');
  } else {
    /_gradeSessao/.test(sf[1])
      ? ok('panel: the fit obeys the session grid')
      : no('panel: the fit ignores the session grid — it fights the notice again');
    /_afirmaTamanho/.test(sf[1])
      ? ok('panel: even while obeying, the fit asserts the natural window (that is how the session grows back)')
      : no('panel: obeying without asserting the natural window pins the session to the size of whoever left');
  }
}

// ── a ±1 column correction must not undo the previous one ──────────────────
//
// The 300 ms quarantine filters the ISOLATED spurious event, not the pair that
// repeats: applying +1 can change the pixel box (a scrollbar appearing) and make
// the next measurement propose −1, which once applied proposes +1 again. Every
// round trip is a reflow of the entire xterm scrollback and a SIGWINCH in the
// remote program.
{
  const sf = shell.match(/_safeFit\(fit\)\{([\s\S]*?)\n {4}\},/);
  if (!sf) {
    no('could not find _safeFit');
  } else {
    /_ultimoAjuste1col/.test(sf[1])
      ? ok('panel: a ±1 column does not undo the previous correction (damper on the oscillator)')
      : no('panel: the ±1 column oscillator is back — every round trip is a reflow and a SIGWINCH');
    /dc === -dc|\.dc === -dc/.test(sf[1])
      ? ok('panel: the damper compares the DIRECTION of the correction (it only blocks the undo)')
      : no('panel: the damper ignores the direction — it would block a legitimate correction');
  }
}

// ── both clients ask for a RENDERED CROP ───────────────────────────────────
//
// Without `quadro=1`, the client goes back to being a CEILING on the size of the
// session — that is, the phone shrinks the desktop again. One letter in the URL
// and the whole defect returns, with nothing on screen saying why.
{
  /ws\/shell\?size=1&quadro=1/.test(shell)
    ? ok('panel: asks for quadro=1 in the socket URL')
    : no('panel: no quadro=1 — the smaller window shrinks the shared session again');
}

// ── the recovery screen client ──────────────────────────────────────────────
// Separate code on purpose (the screen exists outside the SPA); hence the same
// guarantee is asserted here, or the two drift apart on the next fix.
{
  /function afirmaTamanho\(\)[\s\S]{0,400}?const cols = term\.cols, rows = term\.rows/.test(recovery)
    ? ok('recovery: reasserts by reading the CURRENT xterm size')
    : no('recovery: no reassertion of the current size');
  /ping[\s\S]{0,400}?afirmaTamanho\(\);/.test(recovery)
    ? ok('recovery: the heartbeat reasserts the size')
    : no('recovery: the heartbeat does not reassert');
  /window\.addEventListener\('focus', reconciliaTamanho\)/.test(recovery)
    ? ok('recovery: switching WINDOWS triggers reconciliation')
    : no('recovery: nothing reconciles on coming back to the window');
  const r = recovery.match(/function reconciliaTamanho\(\) \{([\s\S]*?)\n\}/);
  if (!r) {
    no('recovery: could not find reconciliaTamanho');
  } else {
    const i = r[1].indexOf('fit'), j = r[1].indexOf('afirmaTamanho');
    (i >= 0 && j > i)
      ? ok('recovery: reconciliation fits BEFORE asserting')
      : no('recovery: it asserts before fitting');
  }
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
