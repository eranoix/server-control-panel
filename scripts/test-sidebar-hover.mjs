#!/usr/bin/env node
// test-sidebar-hover.mjs — the two side rails: they expand on hover and
// COLLAPSE when the mouse leaves, even after a click on them.
//   • left (navigation)
//   • right (terminal actions)
//
// THE BUG: the expansion is pure CSS, triggered by `:hover` OR `:focus-within`.
// Clicking a nav item focuses the element (they are `div[role=button]
// [tabindex="0"]` and `<button>`), and the focus does NOT leave when the pointer
// does — so `:focus-within` stays true and the rail stays open until the user
// clicks somewhere else. That was exactly the reported symptom.
//
// WHY THE TEST RUNS IN A REAL BROWSER: this is CSS behaviour (cascade,
// specificity, the browser's own :focus-visible heuristic). No textual assertion
// over the file would prove that the rail collapses — and a textual assertion is
// precisely what let bugs through before. Here Chrome loads the REAL CSS
// extracted from index.html, the mouse clicks and leaves, and the computed width
// is measured.
//
// Usage: scripts/test-sidebar-hover.mjs [path-to-index.html]
import { readFileSync, mkdtempSync, writeFileSync, rmSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/index.html');
const html = readFileSync(target, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };
console.log('=== test-sidebar-hover ===');

const chrome = ['/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/snap/bin/chromium']
  .find(p => { try { readFileSync(p); return true; } catch { return false; } });
if (!chrome) { console.log('  ⚠ no Chrome — test skipped (not a failure)'); process.exit(0); }

// The app's REAL CSS: every <style> in index.html. Copying the rules by hand
// would make the test diverge from the product exactly when it mattered.
// Production loads tailwind.css BEFORE the inline <style> blocks (line 16 of
// index.html), and that is where the box-sizing reset comes from. Without it the
// harness measures a cascade that exists nowhere — that is how the rail showed up
// 14px outside the viewport in a test meant to reflect the real screen.
let reset = '';
for (const cand of [join(dirname(target), 'tailwind.css'),
                    join(root, 'internal/webassets/web/tailwind.css')]) {
  try { reset = readFileSync(cand, 'utf8'); break; } catch {}
}
reset
  ? ok('tailwind.css loaded in the harness (same cascade order as production)')
  : no('could not find tailwind.css next to index.html — the test cascade does not match production');
let css = reset + '\n' + [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map(m => m[1]).join('\n');

// Headless Chrome reports `hover: none` and will NOT let you emulate the
// opposite (Emulation.setEmulatedMedia does not cover that media feature).
// Untreated, the `@media (hover: hover)` wrapping the rail rules would not even
// match and the test would measure an inert page — a false green at the exact
// point it exists to watch. We neutralise ONLY the environment predicate; the
// rail rules stay byte for byte the ones in the product.
const GATE = /\(hover: hover\) and |and \(hover: hover\)/g;
const swaps = (css.match(GATE) || []).length;
const rawCss = css;                 // with the gate intact = an environment without hover
css = css.replace(GATE, '');
if (swaps === 2) {
  ok(`@media (hover) gate neutralised in the harness (${swaps} occurrences: left rail + right rail)`);
} else {
  no(`expected 2 '(hover: hover)' gates in index.html (left rail and right rail), found ${swaps} — the CSS was restructured and this test may be measuring an inert page`);
}

// Minimal DOM with the SAME structure as the product: focusable items inside the rail.
const page = `<!doctype html><html><head><meta charset="utf-8"><style>
  html,body{margin:0;padding:0;height:100%}
  ${css}
</style></head><body>
  <aside class="sidebar glass is-collapsed" id="sb">
    <div class="sidebar-head"><div><svg width="16" height="16"></svg></div></div>
    <button class="sidebar-search" id="search"><span class="sidebar-search__label">Search</span></button>
    <div class="nav-item" id="nav1" role="button" tabindex="0"><span class="nav-icon">■</span><span>Dashboard</span></div>
    <div class="nav-item" id="nav2" role="button" tabindex="0"><span class="nav-icon">■</span><span>Terminal</span></div>
  </aside>
  <main style="height:100%"><div id="fora" style="width:400px;height:400px"></div></main>
</body></html>`;

const profile = mkdtempSync(join(tmpdir(), 'panel-chrome-'));
const file = join(profile, 'sidebar.html');
writeFileSync(file, page);

const port = 9222 + (process.pid % 900);
const proc = spawn(chrome, [
  '--headless=new', `--remote-debugging-port=${port}`, `--user-data-dir=${profile}`,
  '--no-sandbox', '--disable-gpu', '--window-size=1400,900', '--no-first-run',
  '--disable-extensions', '--disable-dev-shm-usage', 'about:blank',
], { stdio: ['ignore', 'ignore', 'pipe'] });

const clear = () => { try { proc.kill('SIGKILL'); } catch {} try { rmSync(profile, { recursive: true, force: true }); } catch {} };
process.on('exit', clear);

const sleep = (ms) => new Promise(r => setTimeout(r, ms));

async function targetWs() {
  for (let i = 0; i < 60; i++) {
    try {
      const r = await fetch(`http://127.0.0.1:${port}/json/list`);
      const targets = await r.json();
      const p = targets.find(t => t.type === 'page');
      if (p?.webSocketDebuggerUrl) return p.webSocketDebuggerUrl;
    } catch {}
    await sleep(150);
  }
  throw new Error('Chrome never brought the debugging port up');
}

class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map();
    ws.onmessage = (e) => { const m = JSON.parse(e.data);
      if (m.id && this.pending.has(m.id)) { const { res, rej } = this.pending.get(m.id); this.pending.delete(m.id);
        m.error ? rej(new Error(m.error.message)) : res(m.result); } }; }
  send(method, params = {}) { const id = ++this.id;
    return new Promise((res, rej) => { this.pending.set(id, { res, rej }); this.ws.send(JSON.stringify({ id, method, params })); }); }
  async eval(expr) { const r = await this.send('Runtime.evaluate', { expression: expr, returnByValue: true }); return r.result?.value; }
  async mouse(type, x, y, button = 'none', clickCount = 0) {
    await this.send('Input.dispatchMouseEvent', { type, x, y, button, clickCount, buttons: 0 });
  }
}

const width = (cdp) => cdp.eval("getComputedStyle(document.getElementById('sb')).width");
const center  = (cdp, id) => cdp.eval(`(()=>{const r=document.getElementById(${JSON.stringify(id)}).getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2};})()`);

try {
  const url = await targetWs();
  const ws = new WebSocket(url);
  await new Promise((r, rej) => { ws.onopen = r; ws.onerror = () => rej(new Error('the Chrome WS failed')); });
  const cdp = new CDP(ws);
  await cdp.send('Page.enable');
  await cdp.send('Runtime.enable');
  await cdp.send('Page.navigate', { url: 'file://' + file });
  await sleep(700);

  const COLLAPSED = 50.6, EXPANDED = 240;
  const px = (v) => parseFloat(String(v));

  // Initial state: collapsed.
  let w = px(await width(cdp));
  Math.abs(w - COLLAPSED) < 2
    ? ok(`starts collapsed (${w}px)`)
    : no(`did not start collapsed: ${w}px`);

  // Hover expands — the very function the rail exists to have.
  const targetNav = await center(cdp, 'nav1');
  await cdp.mouse('mouseMoved', targetNav.x, targetNav.y);
  await sleep(400);
  w = px(await width(cdp));
  Math.abs(w - EXPANDED) < 4
    ? ok(`hover expands (${w}px)`)
    : no(`hover did not expand: ${w}px`);

  // Mouse leaves with NO click: it has to collapse (the path that already worked).
  await cdp.mouse('mouseMoved', 900, 600);
  await sleep(400);
  w = px(await width(cdp));
  Math.abs(w - COLLAPSED) < 2
    ? ok(`mouse leaves with no click → collapses (${w}px)`)
    : no(`stayed open after leaving without a click: ${w}px`);

  // ── THE BUG: click an item and take the mouse away ────────────────────
  await cdp.mouse('mouseMoved', targetNav.x, targetNav.y);
  await sleep(150);
  await cdp.mouse('mousePressed', targetNav.x, targetNav.y, 'left', 1);
  await cdp.mouse('mouseReleased', targetNav.x, targetNav.y, 'left', 1);
  await sleep(150);
  const focused = await cdp.eval("document.activeElement && (document.activeElement.id || document.activeElement.tagName)");
  await cdp.mouse('mouseMoved', 900, 600);
  await sleep(450);
  w = px(await width(cdp));
  Math.abs(w - COLLAPSED) < 2
    ? ok(`CLICKS the item and takes the mouse away → collapses (${w}px)`)
    : no(`CRITICAL: stayed open after click+leave (${w}px) — focus on '${focused}' is holding the rail`);

  // Same thing through the search button (it is a <button>, it focuses even more easily).
  const targetSearch = await center(cdp, 'search');
  await cdp.mouse('mouseMoved', targetSearch.x, targetSearch.y);
  await sleep(150);
  await cdp.mouse('mousePressed', targetSearch.x, targetSearch.y, 'left', 1);
  await cdp.mouse('mouseReleased', targetSearch.x, targetSearch.y, 'left', 1);
  await cdp.mouse('mouseMoved', 900, 600);
  await sleep(450);
  w = px(await width(cdp));
  Math.abs(w - COLLAPSED) < 2
    ? ok(`clicks the search button and takes the mouse away → collapses (${w}px)`)
    : no(`stayed open after clicking the search button (${w}px)`);

  // ── Accessibility: the keyboard must NOT lose the expansion ───────────
  // Tabbing through has to open the rail, otherwise keyboard users see icons
  // only. That is why the fix cannot be "remove :focus-within".
  await cdp.eval("document.getElementById('sb').focus?.()");
  await cdp.eval("document.body.focus()");
  await sleep(100);
  await cdp.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', windowsVirtualKeyCode: 9, key: 'Tab', code: 'Tab' });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', windowsVirtualKeyCode: 9, key: 'Tab', code: 'Tab' });
  await sleep(350);
  const keyboardFocus = await cdp.eval("document.activeElement && document.activeElement.id");
  w = px(await width(cdp));
  if (keyboardFocus && keyboardFocus !== 'null') {
    Math.abs(w - EXPANDED) < 4
      ? ok(`Tab focuses '${keyboardFocus}' and the rail OPENS (${w}px) — keyboard preserved`)
      : no(`Tab focused '${keyboardFocus}' but the rail stayed at ${w}px — keyboard users see icons only`);
  } else {
    no('Tab did not move focus into the rail — the keyboard could not be verified');
  }

  // ══ RIGHT RAIL (terminal actions) ═════════════════════════════════════
  // The rail is a flex sibling of the terminal. If the expansion changed the
  // IN-FLOW width, every hover would reflow the terminal — and a reflow there
  // fires an xterm refit and a SIGWINCH on the PTY, i.e. the screen REPAINTS.
  // Moving the mouse would cause the same damage a deploy used to cause. So the
  // central assertion here is not "it expanded": it is "the terminal did NOT
  // change width while expanding".
  const termPage = `<!doctype html><html><head><meta charset="utf-8"><style>
    html,body{margin:0;padding:0;height:100%}
    ${css}
  </style></head><body>
    <div class="term-body" id="tb" style="height:100vh">
      <div class="term-pane-card" id="pane" style="flex:1 1 auto">terminal</div>
      <div class="term-side term-hide-mobile" id="ts" role="toolbar">
        <span class="term-status-dot"></span>
        <div class="ts-sep"></div>
        <button class="ts-btn" id="tsb1"><span class="ts-glyph">A</span><span class="ts-lbl">Font</span></button>
        <button class="ts-btn" id="tsb2"><span class="ts-glyph">B</span><span class="ts-lbl">Snippets</span></button>
      </div>
    </div>
  </body></html>`;
  const arqTerm = join(profile, 'termside.html');
  writeFileSync(arqTerm, termPage);
  await cdp.send('Page.navigate', { url: 'file://' + arqTerm });
  await sleep(700);

  const widthOf = (id) => cdp.eval(`getComputedStyle(document.getElementById(${JSON.stringify(id)})).width`);
  const RAIL = 14, RAIL_OPEN = 48;

  let wr = px(await widthOf('ts'));
  Math.abs(wr - RAIL) < 2
    ? ok(`the right rail starts thin (${wr}px) — only the grip hinting something is there`)
    : no(`the right rail did not start thin: ${wr}px`);

  // Content invisible while collapsed (otherwise clipped icons leak into the rail).
  const buttonOpacity = await cdp.eval("getComputedStyle(document.getElementById('tsb1')).opacity");
  parseFloat(buttonOpacity) === 0
    ? ok('buttons invisible with the rail collapsed')
    : no(`buttons showing through the thin rail (opacity ${buttonOpacity})`);

  const terminalWidthBefore = px(await widthOf('pane'));

  const targetRail = await center(cdp, 'ts');
  await cdp.mouse('mouseMoved', targetRail.x, targetRail.y);
  await sleep(400);
  wr = px(await widthOf('ts'));
  Math.abs(wr - RAIL_OPEN) < 3
    ? ok(`hover expands the right rail (${wr}px)`)
    : no(`hover did not expand the right rail: ${wr}px`);

  const terminalWidthDuring = px(await widthOf('pane'));
  Math.abs(terminalWidthDuring - terminalWidthBefore) < 0.6
    ? ok(`the terminal does NOT reflow during the expansion (${terminalWidthBefore}px → ${terminalWidthDuring}px)`)
    : no(`CRITICAL: the terminal went from ${terminalWidthBefore}px to ${terminalWidthDuring}px on hover — that fires an xterm refit and a SIGWINCH on the PTY, repainting the screen`);

  parseFloat(await cdp.eval("getComputedStyle(document.getElementById('tsb1')).opacity")) === 1
    ? ok('buttons visible with the rail expanded')
    : no('buttons stayed invisible after expanding');

  await cdp.mouse('mouseMoved', 40, 300);
  await sleep(400);
  wr = px(await widthOf('ts'));
  Math.abs(wr - RAIL) < 2
    ? ok(`mouse leaves → the right rail retracts (${wr}px)`)
    : no(`the right rail stayed open after the mouse left: ${wr}px`);

  // The left rail lesson applied here: clicking a <button> in the rail must not
  // pin it open.
  await cdp.mouse('mouseMoved', targetRail.x, targetRail.y);
  await sleep(250);
  const targetBtn = await center(cdp, 'tsb1');
  await cdp.mouse('mousePressed', targetBtn.x, targetBtn.y, 'left', 1);
  await cdp.mouse('mouseReleased', targetBtn.x, targetBtn.y, 'left', 1);
  await cdp.mouse('mouseMoved', 40, 300);
  await sleep(450);
  wr = px(await widthOf('ts'));
  Math.abs(wr - RAIL) < 2
    ? ok(`clicks a rail button and takes the mouse away → retracts (${wr}px)`)
    : no(`CRITICAL: the right rail was pinned open after a click (${wr}px) — :focus-within is back`);

  // Keyboard: whoever navigates by Tab needs to see the rail open.
  await cdp.eval("document.body.focus()");
  await cdp.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', windowsVirtualKeyCode: 9, key: 'Tab', code: 'Tab' });
  await cdp.send('Input.dispatchKeyEvent', { type: 'keyUp', windowsVirtualKeyCode: 9, key: 'Tab', code: 'Tab' });
  await sleep(350);
  const railFocus = await cdp.eval("document.activeElement && document.activeElement.id");
  wr = px(await widthOf('ts'));
  if (railFocus && String(railFocus).startsWith('tsb')) {
    Math.abs(wr - RAIL_OPEN) < 3
      ? ok(`Tab focuses '${railFocus}' and the right rail OPENS (${wr}px) — keyboard preserved`)
      : no(`Tab focused '${railFocus}' but the rail stayed at ${wr}px — keyboard users cannot reach the actions`);
  } else {
    no(`Tab did not focus a rail button (it went to '${railFocus}')`);
  }

  // ── Touch fallback: with no hover, the rail has to stay VISIBLE ────────
  // Expand-on-hover is unreachable on a tablet. Headless is, by nature, a device
  // without hover — so loading the CSS with the gate INTACT (`rawCss`) is
  // enough to exercise exactly what a tablet ≥768px would see.
  {
    const touchPage = `<!doctype html><html><head><meta charset="utf-8"><style>
      html,body{margin:0;padding:0;height:100%}
      ${rawCss}
    </style></head><body>
      <div class="term-body" id="tb" style="height:100vh">
        <div class="term-pane-card" id="pane" style="flex:1 1 auto">terminal</div>
        <div class="term-side" id="ts" role="toolbar">
          <button class="ts-btn" id="tsb1"><span class="ts-lbl">Font</span></button>
        </div>
      </div>
    </body></html>`;
    const arqToque = join(profile, 'toque.html');
    writeFileSync(arqToque, touchPage);
    await cdp.send('Page.navigate', { url: 'file://' + arqToque });
    await sleep(600);
    const wt = px(await cdp.eval("getComputedStyle(document.getElementById('ts')).width"));
    const opac = parseFloat(await cdp.eval("getComputedStyle(document.getElementById('tsb1')).opacity"));
    (Math.abs(wt - 48) < 2 && opac === 1)
      ? ok(`with no hover (tablet) the rail stays wide and visible (${wt}px, opacity ${opac})`)
      : no(`with no hover the rail came out ${wt}px / opacity ${opac} — on a tablet the actions would be unreachable`);
  }

  // ── The CSS trap the fix has to keep avoiding ─────────────────────────
  // A selector list is all-or-nothing: ONE invalid selector invalidates the WHOLE
  // RULE. A bare `:has()` next to `:hover` means that, in a browser without
  // `:has()` support, the rail loses EVEN the hover and never expands. Inside
  // `:is()`/`:where()` the list is forgiving and the degradation stays contained.
  {
    const rules = [...css.matchAll(/([^{}]+)\{[^{}]*\}/g)].map(m => m[1]);
    const dangerous = rules.filter(sel =>
      sel.includes(':has(') && sel.includes(':hover') &&
      !/:(is|where)\(\s*:has\(/.test(sel));
    dangerous.length === 0
      ? ok(':has() never appears bare next to :hover (a browser without :has() loses only the keyboard, not the hover)')
      : no(`CRITICAL: ${dangerous.length} rule(s) mix :hover with a bare :has() — without :has() support the rail would not expand at all`);
  }

  ws.close();
} catch (e) {
  no('error while running the browser: ' + e.message);
}

console.log(`\n${pass} passed, ${fail} failed`);
clear();
process.exit(fail ? 1 : 0);
