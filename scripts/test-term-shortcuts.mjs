#!/usr/bin/env node
// test-term-shortcuts.mjs — Ctrl+C copies (only with a selection), Ctrl+V pastes.
//
// The RISK this test exists to pin down: Ctrl+C in a terminal is SIGINT — it is
// how a stuck command is interrupted. A "copy" shortcut that swallows the ^C
// unconditionally turns convenience into a trap, and the subtlest way for that
// to happen is the selection NOT being cleared after copying: the old selection
// stays on screen and every later Ctrl+C becomes "copy" instead of interrupt.
//
// It extracts the REAL handler from 00-shell.js and drives it with fake events.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(target, 'utf8');
let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };
console.log('=== test-term-shortcuts ===');

// Cuts only the Ctrl+C/Ctrl+V blocks out of the real handler and builds a
// function with the same signature (ev) => boolean, with term/self/state injected.
const block = src.match(/\/\/ ── Ctrl\+C \/ Ctrl\+V ─[\s\S]*?\n(?= +\/\/ Copy\/Paste in the)/);
if (!block) { no('could not extract the Ctrl+C/Ctrl+V block'); process.exit(1); }

const build = ({ selection, ctrlV }) => {
  const termState = { copied: null, cleared: false, pasted: false };
  const term = {
    getSelection: () => selection,
    clearSelection: () => { termState.cleared = true; },
  };
  const self = { hostTermCtrlV: ctrlV, _pasteIntoPane: async () => { termState.pasted = true; }, _pasteImageIfAny: () => { termState.image = true; } };
  const state = {};
  // navigator is read-only on Node 22 → inject it as a parameter
  const navigator = { clipboard: { writeText: (t) => { termState.copied = t; return Promise.resolve(); } } };
  const fn = new Function('ev', 'term', 'self', 'state', 'c', 'navigator',
    block[0] + '\n return "FELL_THROUGH";');
  const run = (ev) => fn(ev, term, self, state, ev.ctrlKey || ev.metaKey, navigator);
  return { run, estado: termState };
};
const ev = (key, extra = {}) => ({ type: 'keydown', key, ctrlKey: true, shiftKey: false, altKey: false, preventDefault(){}, ...extra });

// 1) Ctrl+C WITHOUT a selection → does NOT intercept (the ^C must be SIGINT)
{
  const { run, estado: state } = build({ selection: '', ctrlV: true });
  const r = run(ev('c'));
  (r === 'FELL_THROUGH' || r === true) && state.copied === null
    ? ok('Ctrl+C with no selection → passes through as SIGINT (does not swallow the ^C)')
    : no('Ctrl+C with no selection was INTERCEPTED — the user lost the SIGINT');
}
// 2) Ctrl+C WITH a selection → copies and CLEARS the selection
{
  const { run, estado: state } = build({ selection: 'copied text', ctrlV: true });
  const r = run(ev('c'));
  r === false && state.copied === 'copied text'
    ? ok('Ctrl+C with a selection → copies')
    : no('Ctrl+C with a selection did not copy');
  state.cleared
    ? ok('the selection is CLEARED after copying (the next Ctrl+C interrupts again)')
    : no('the selection was NOT cleared — every later Ctrl+C would stop being SIGINT');
}
// 3) Ctrl+V with the toggle on → it has to return EXACTLY false.
//
// This is the heart of the bug and the most important assertion in the file. In
// the xterm _keyDown:
//
//   if (this._customKeyEventHandler && false === this._customKeyEventHandler(e)) return false;
//   ... triggerDataEvent(i.key)   // sends ^V (0x16)
//   ... this.cancel(e, true)      // preventDefault()
//
// Ctrl+V maps to \x16. Returning TRUE makes xterm consume the key and call
// preventDefault(), and then the NATIVE 'paste' event never fires — it pastes
// neither text NOR image. Only FALSE makes _keyDown return before cancel(),
// letting the browser run the default paste. `true` here is a bug, not a detail.
{
  const { run, estado: state } = build({ selection: '', ctrlV: true });
  let blocked = false;
  const e = ev('v'); e.preventDefault = () => { blocked = true; };
  const r = run(e);
  r === false
    ? ok('Ctrl+V returns false → xterm does NOT consume the key → the native paste fires')
    : no(r === true || r === 'FELL_THROUGH'
        ? 'Ctrl+V returned true/passed → xterm sends ^V and calls preventDefault: NOTHING pastes'
        : 'Ctrl+V returned an unexpected value: ' + JSON.stringify(r));
  !blocked
    ? ok('we do not call preventDefault (xterm is the one that would cancel)')
    : no('we call preventDefault — that kills the native paste by ourselves');
  !state.pasted
    ? ok('does not paste through the Clipboard API (no permission needed)')
    : no('still pastes through the API → duplicated text and a silent NotAllowedError');
}
// 4) Ctrl+V with the toggle off → passes through (literal ^V for vim/readline)
{
  const { run, estado: state } = build({ selection: '', ctrlV: false });
  const r = run(ev('v'));
  (r === 'FELL_THROUGH' || r === true) && !state.pasted
    ? ok('Ctrl+V (toggle off) → the literal ^V reaches the app (vim visual-block)')
    : no('with the toggle off the literal ^V was not delivered');
}
// 5) Ctrl+Shift+C is not captured by this block (it goes to the legacy handler)
{
  const { run } = build({ selection: 'x', ctrlV: true });
  const r = run(ev('C', { shiftKey: true }));
  (r === 'FELL_THROUGH' || r === true)
    ? ok('Ctrl+Shift+C is not swallowed here (the legacy fallback is intact)')
    : no('Ctrl+Shift+C was captured by the new block');
}
// 6) Cmd+C (macOS) copies too
{
  const { run, estado: state } = build({ selection: 'mac', ctrlV: true });
  const r = run({ type:'keydown', key:'c', ctrlKey:false, metaKey:true, shiftKey:false, altKey:false, preventDefault(){} });
  r === false && state.copied === 'mac' ? ok('Cmd+C (macOS) copies') : no('Cmd+C did not copy');
}
console.log('─'.repeat(37));
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail === 0 ? 0 : 1);
