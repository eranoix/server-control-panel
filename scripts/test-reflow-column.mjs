#!/usr/bin/env node
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(ROOT, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(ROOT, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILURE: playwright-core missing from .tools/. This pin RUNS an xterm — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-reflow-column ===');

const shell = fs.readFileSync(path.join(WEB, 'vendor/panel/app/00-shell.js'), 'utf8');
const recovery = fs.readFileSync(path.join(WEB, 'recovery-term.html'), 'utf8');
const index = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');

const mSafeFit = shell.match(/^ {4}_safeFit\(fit\)\{\n([\s\S]*?)^ {4}\},$/m);
if (!mSafeFit) { no('could not find _safeFit in 00-shell.js — the panel guard is gone'); process.exit(1); }
const panelSafeFitBody = mSafeFit[1];

const mRecoverySafeFit = recovery.match(/^function safeFit\(\) \{\n([\s\S]*?)^\}$/m);
if (!mRecoverySafeFit) { no('could not find safeFit in recovery-term.html — the recovery guard is gone'); process.exit(1); }
const recoverySafeFitBody = mRecoverySafeFit[1];

const pageHtml = `<!doctype html><meta charset="utf-8">
<link rel="stylesheet" href="/vendor/xterm/xterm.css">
<style>html,body{margin:0;background:#000} #t{width:900px;height:600px}</style>
<div id="t"></div>
<script src="/vendor/xterm/xterm.js"></script>
<script>
window.__ready = false;
window.addEventListener('error', e => { window.__error = String(e.message); });
const COLS = 56, ROWS = 30;
const term = new Terminal({ cols: COLS, rows: ROWS, scrollback: 1000, allowProposedApi: true });
term.open(document.getElementById('t'));

const write = (s) => new Promise(r => term.write(s, r));

// Read the whole buffer (scrollback + viewport) as text.
window.__buffer = () => {
  const b = term.buffer.active;
  const out = [];
  for (let i = 0; i < b.length; i++) {
    const l = b.getLine(i);
    out.push(l ? l.translateToString(true) : '');
  }
  return out.join('\\n');
};

// A TUI app "frame": LINES lines of exactly COLS characters. At 56 columns each
// one takes 1 physical line; at 55, it takes 2 (55 + the remainder). It is the
// cleanest way to make the physical count change under +-1 column.
const LINES = 5;
function frame(mark) {
  const rows = [];
  for (let i = 0; i < LINES; i++) {
    const tok = 'SENT' + mark + '-L' + i + '-';
    rows.push(tok.repeat(Math.ceil(COLS / tok.length)).slice(0, COLS));
  }
  return rows.join('\\r\\n');
}
// Repaint the way a TUI repaints: go up the N lines IT counted having written,
// clear from there to the end of the screen, redraw.
window.__scenario = async (withResize) => {
  term.reset();
  term.resize(COLS, ROWS);
  await write(frame('A'));
  if (withResize) term.resize(COLS - 1, ROWS);   // the spurious +-1 column
  await write('\\x1b[' + (LINES - 1) + 'A\\r\\x1b[J');
  await write(frame('A'));
  // Count COPIES of the frame: buffer lines that START with the token. Counting
  // occurrences of the token would be misleading — it repeats inside the line
  // itself to fill the 56 columns.
  return window.__buffer().split('\\n').filter(l => l.startsWith('SENTA-L0-')).length;
};

// ── the REAL guards, with the REAL Terminal ────────────────────────────────
window.__safeFit  = new Function('fit', ${JSON.stringify(panelSafeFitBody)});
window.__mkSafeFit = () => {
  // setTimeout/clearTimeout stay out of the parameters on purpose: passing them
  // detached from window makes Chrome throw "Illegal invocation", the try/catch
  // of the guard swallows it, and the pin would fail on a defect OF ITS OWN. Here
  // they resolve to the page globals, as in the real file.
  return new Function('fitAddon', 'term',
    'let colPending = 0, colTimer = 0;\\n'
    + 'function safeFit(){' + ${JSON.stringify(recoverySafeFitBody)} + '}\\n'
    + 'return safeFit;')(window.__currentFit, window.__currentTerm);
};
window.__term = term;
window.__ready = true;
</script>`;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/' ) { res.writeHead(200, {'content-type':'text/html'}); res.end(pageHtml); return; }
  const f = path.join(WEB, u.replace(/^\/+/, ''));
  if (!f.startsWith(WEB) || !fs.existsSync(f)) { res.writeHead(404); res.end(); return; }
  res.writeHead(200, {'content-type': u.endsWith('.css') ? 'text/css' : 'application/javascript'});
  res.end(fs.readFileSync(f));
});
await new Promise(r => srv.listen(0, '127.0.0.1', r));
const base = 'http://127.0.0.1:' + srv.address().port + '/';

function findBrowser() {
  const c = [];
  if (process.env.PANEL_CHROMIUM) c.push(process.env.PANEL_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse())
    c.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
  c.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const x of c) if (fs.existsSync(x)) return x;
  return null;
}
const exe = findBrowser();
if (!exe) { console.error('FAILURE: no Chromium found — skipping would be faking coverage.'); process.exit(1); }
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();
await page.goto(base);
await page.waitForFunction('window.__ready === true', null, { timeout: 15000 });

const noResize = await page.evaluate('window.__scenario(false)');
const withResize = await page.evaluate('window.__scenario(true)');

noResize === 1
  ? ok('control: with no column change, the repaint REPLACES the frame (1 copy)')
  : no('control: the repaint already duplicates with no resize (' + noResize + ' copies) — invalid scenario');

withResize > noResize
  ? ok('reproduction: ONE column less between paint and repaint leaves ' + withResize + ' copies (was ' + noResize + ') — repeated text, exactly as reported')
  : no('reproduction: the ±1 column did not corrupt (' + withResize + ' copies) — the scenario does not exercise the reflow');

const guard = async (script) => page.evaluate(`(async () => {
  const t = window.__term;
  // DRAIN before starting. Each scenario arms 300ms timers inside the guard
  // itself; if the next one starts before they expire, the timer of the PREVIOUS
  // scenario resizes the shared terminal in the middle of this one — and the pin
  // reports a defect that does not exist. It cost an investigation: worth waiting.
  await new Promise(r => setTimeout(r, 400));
  t.reset(); t.resize(56, 30);
  let prop = { cols: 56, rows: 30 };
  const fit = {
    _panelTerm: t,
    proposeDimensions: () => prop,
    fit: () => t.resize(prop.cols, prop.rows),
  };
  const app = { _safeFit: window.__safeFit };
  const step = (c, r) => { prop = { cols: c, rows: r }; app._safeFit(fit); };
  const wait = (ms) => new Promise(r => setTimeout(r, ms));
  ${script}
})()`);

const wobble = await guard(`
  step(55, 30);                    // the chrome steals a column
  const logo = t.cols;
  await wait(120);
  step(56, 30);                    // and gives it back, before the 300ms
  await wait(450);
  return { logo, end: t.cols };
`);
wobble.logo === 56 && wobble.end === 56
  ? ok('panel: a ±1 column oscillation that undoes itself does NOT reach xterm (cols stayed 56)')
  : no('panel: the oscillation got through (immediate=' + wobble.logo + ', final=' + wobble.end + ') — reflow happens');

const sustained = await guard(`
  step(55, 30);
  const logo = t.cols;
  await wait(450);                // the second measurement agrees
  return { logo, end: t.cols };
`);
sustained.logo === 56 && sustained.end === 55
  ? ok('panel: a SUSTAINED ±1 column is applied on the second measurement (56 → 55)')
  : no('panel: a real 1 column change was not applied (immediate=' + sustained.logo + ', final=' + sustained.end + ')');

const large = await guard(`
  step(40, 30);                    // rotation / split: real intent
  return { logo: t.cols };
`);
large.logo === 40
  ? ok('panel: a change of ≥2 columns goes through at once (no quarantine)')
  : no('panel: a real width change got stuck in the hysteresis (cols=' + large.logo + ')');

const lines = await guard(`
  step(55, 22);                    // virtual keyboard: width +-1, height changes
  return { cols: t.cols, rows: t.rows };
`);
lines.cols === 56 && lines.rows === 22
  ? ok('panel: the ROWS go through at once even with the column quarantined (virtual keyboard)')
  : no('panel: rows stuck along with the column (cols=' + lines.cols + ', rows=' + lines.rows + ') — the prompt stays hidden');

const rec = await page.evaluate(`(async () => {
  const t = window.__term;
  await new Promise(r => setTimeout(r, 400));   // drain the timers of the previous scenario
  t.reset(); t.resize(56, 30);
  let prop = { cols: 56, rows: 30 };
  window.__currentTerm = t;
  window.__currentFit = {
    proposeDimensions: () => prop,
    fit: () => t.resize(prop.cols, prop.rows),
  };
  const safeFit = window.__mkSafeFit();
  const wait = (ms) => new Promise(r => setTimeout(r, ms));
  prop = { cols: 55, rows: 30 }; safeFit();
  const logo = t.cols;
  await wait(120);
  prop = { cols: 56, rows: 30 }; safeFit();
  await wait(450);
  const end = t.cols;
  prop = { cols: 40, rows: 30 }; safeFit();
  return { logo, end, large: t.cols };
})()`);
rec.logo === 56 && rec.end === 56
  ? ok('recovery: a ±1 column oscillation does not reach xterm')
  : no('recovery: the oscillation got through (immediate=' + rec.logo + ', final=' + rec.end + ')');
rec.large === 40
  ? ok('recovery: a change of ≥2 columns goes through at once')
  : no('recovery: a real change got stuck (cols=' + rec.large + ')');

await browser.close();
srv.close();

{
  const fora = recovery.split('\n')
    .filter(l => /fitAddon\.fit\(\)/.test(l))
    .filter(l => !/proposeDimensions !== 'function'|term\.cols >= 2|^ {4}fitAddon\.fit\(\);$/.test(l));
  fora.length === 0
    ? ok('recovery: no raw fitAddon.fit() outside safeFit')
    : no('recovery: ' + fora.length + ' raw fit() outside the guard:' + fora.map(l => '\n      ' + l.trim()).join(''));

  const rawShell = shell.split('\n')
    .filter(l => /\bfit\.fit\(\)/.test(l))
    .filter(l => !/_safeFit/.test(l));
  const inside = (panelSafeFitBody.match(/fit\.fit\(\)/g) || []).length;
  rawShell.length === inside
    ? ok('panel: all ' + inside + ' existing fit.fit() calls are inside _safeFit')
    : no('panel: there is a fit.fit() outside _safeFit (' + rawShell.length + ' in the file, ' + inside + ' in the guard)');
}

const indexCss = index.replace(/\/\*[\s\S]*?\*\//g, '');
/#content\.is-noscroll\s*\{[^}]*overflow:\s*hidden/.test(indexCss)
  ? ok('desktop: #content.is-noscroll does not scroll — no bar can steal a column from xterm')
  : no('desktop: the #content.is-noscroll{overflow:hidden} rule is gone — the bar steals a column again');

/is-noscroll/.test(indexCss) && /currentView\s*===\s*'terminal'\s*\?\s*'is-noscroll'/.test(indexCss)
  ? ok('desktop: <main> turns .is-noscroll on in the terminal screen')
  : no('desktop: <main> no longer turns .is-noscroll on — the rule is orphaned');

/html,\s*body\s*\{[^}]*scrollbar-gutter:\s*stable/.test(indexCss)
  ? no('desktop: scrollbar-gutter is back on html,body — a dead 20px strip, and they do not scroll')
  : ok('desktop: html,body reserve no gutter (they never scroll; reserving there only eats width)');

console.log('---');
console.log(pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
