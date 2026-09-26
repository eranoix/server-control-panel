#!/usr/bin/env node
// test-reflow-coluna.mjs — one column more or less must not destroy the
// history of the terminal.
//
// The report, from a phone: "I cannot see the first options; it is cutting off,
// repeating the text". The session was at 38x56.
//
// The cause is NOT a buffer limit — the whole chain was read and it does not
// truncate: the server relays raw bytes in 8 KB blocks and BLOCKS under pressure
// (pty.go), the client queue has no ceiling and flow control tells the server to
// pause instead of dropping. What truncates is REFLOW.
//
// Changing `cols` makes xterm re-wrap the entire scrollback. An app that repaints
// by cursor addressing — the Claude CLI: go up N lines, clear, redraw — counts
// the PHYSICAL lines it wrote. After the rewrap that N is wrong: it clears the
// wrong lines and draws over what is left. At 56 columns, ONE column is 1.8% of
// the width: it fits in a scrollbar that appears, in the virtual keyboard, or in
// the rounding of width per cell tipping over.
//
// This pin asserts nothing about the text of the code: it RUNS a real xterm,
// provokes the damage, and then exercises the real guards of BOTH clients.
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
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
console.log('=== test-reflow-coluna ===');

const shell = fs.readFileSync(path.join(WEB, 'vendor/vpsm/app/00-shell.js'), 'utf8');
const recovery = fs.readFileSync(path.join(WEB, 'recovery-term.html'), 'utf8');
const index = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');

// ── extraction of the REAL functions ────────────────────────────────────────
// Extracting (instead of reimplementing) is what stops the pin from passing
// while the product regresses: delete the guard and there is nothing to extract.
const mSafeFit = shell.match(/^ {4}_safeFit\(fit\)\{\n([\s\S]*?)^ {4}\},$/m);
if (!mSafeFit) { no('could not find _safeFit in 00-shell.js — the panel guard is gone'); process.exit(1); }
const corpoSafeFit = mSafeFit[1];

const mFitSeguro = recovery.match(/^function fitSeguro\(\) \{\n([\s\S]*?)^\}$/m);
if (!mFitSeguro) { no('could not find fitSeguro in recovery-term.html — the recovery guard is gone'); process.exit(1); }
const corpoFitSeguro = mFitSeguro[1];

// ── the page of the pin ─────────────────────────────────────────────────────
const pagina = `<!doctype html><meta charset="utf-8">
<link rel="stylesheet" href="/vendor/xterm/xterm.css">
<style>html,body{margin:0;background:#000} #t{width:900px;height:600px}</style>
<div id="t"></div>
<script src="/vendor/xterm/xterm.js"></script>
<script>
window.__pronto = false;
window.addEventListener('error', e => { window.__erro = String(e.message); });
const COLS = 56, ROWS = 30;
const term = new Terminal({ cols: COLS, rows: ROWS, scrollback: 1000, allowProposedApi: true });
term.open(document.getElementById('t'));

const escreve = (s) => new Promise(r => term.write(s, r));

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

// A TUI app "frame": LINHAS lines of exactly COLS characters. At 56 columns each
// one takes 1 physical line; at 55, it takes 2 (55 + the remainder). It is the
// cleanest way to make the physical count change under +-1 column.
const LINHAS = 5;
function quadro(marca) {
  const linhas = [];
  for (let i = 0; i < LINHAS; i++) {
    const tok = 'SENT' + marca + '-L' + i + '-';
    linhas.push(tok.repeat(Math.ceil(COLS / tok.length)).slice(0, COLS));
  }
  return linhas.join('\\r\\n');
}
// Repaint the way a TUI repaints: go up the N lines IT counted having written,
// clear from there to the end of the screen, redraw.
window.__cenario = async (comResize) => {
  term.reset();
  term.resize(COLS, ROWS);
  await escreve(quadro('A'));
  if (comResize) term.resize(COLS - 1, ROWS);   // the spurious +-1 column
  await escreve('\\x1b[' + (LINHAS - 1) + 'A\\r\\x1b[J');
  await escreve(quadro('A'));
  // Count COPIES of the frame: buffer lines that START with the token. Counting
  // occurrences of the token would be misleading — it repeats inside the line
  // itself to fill the 56 columns.
  return window.__buffer().split('\\n').filter(l => l.startsWith('SENTA-L0-')).length;
};

// ── the REAL guards, with the REAL Terminal ────────────────────────────────
window.__safeFit  = new Function('fit', ${JSON.stringify(corpoSafeFit)});
window.__mkFitSeguro = () => {
  // setTimeout/clearTimeout stay out of the parameters on purpose: passing them
  // detached from window makes Chrome throw "Illegal invocation", the try/catch
  // of the guard swallows it, and the pin would fail on a defect OF ITS OWN. Here
  // they resolve to the page globals, as in the real file.
  return new Function('fitAddon', 'term',
    'let colPend = 0, colTimer = 0;\\n'
    + 'function fitSeguro(){' + ${JSON.stringify(corpoFitSeguro)} + '}\\n'
    + 'return fitSeguro;')(window.__fitAtual, window.__termAtual);
};
window.__term = term;
window.__pronto = true;
</script>`;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/' ) { res.writeHead(200, {'content-type':'text/html'}); res.end(pagina); return; }
  const f = path.join(WEB, u.replace(/^\/+/, ''));
  if (!f.startsWith(WEB) || !fs.existsSync(f)) { res.writeHead(404); res.end(); return; }
  res.writeHead(200, {'content-type': u.endsWith('.css') ? 'text/css' : 'application/javascript'});
  res.end(fs.readFileSync(f));
});
await new Promise(r => srv.listen(0, '127.0.0.1', r));
const base = 'http://127.0.0.1:' + srv.address().port + '/';

// Same browser resolution as the tabs pin: the playwright cache or the system
// chrome. Skipping for lack of a browser would be faking coverage.
function achaNavegador() {
  const c = [];
  if (process.env.VPSM_CHROMIUM) c.push(process.env.VPSM_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse())
    c.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
  c.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const x of c) if (fs.existsSync(x)) return x;
  return null;
}
const exe = achaNavegador();
if (!exe) { console.error('FAILURE: no Chromium found — skipping would be faking coverage.'); process.exit(1); }
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();
await page.goto(base);
await page.waitForFunction('window.__pronto === true', null, { timeout: 15000 });

// ── 1. THE DAMAGE IS REAL ───────────────────────────────────────────────────
// Without this the rest would be a guard against an undemonstrated problem.
const semResize = await page.evaluate('window.__cenario(false)');
const comResize = await page.evaluate('window.__cenario(true)');

semResize === 1
  ? ok('control: with no column change, the repaint REPLACES the frame (1 copy)')
  : no('control: the repaint already duplicates with no resize (' + semResize + ' copies) — invalid scenario');

comResize > semResize
  ? ok('reproduction: ONE column less between paint and repaint leaves ' + comResize + ' copies (was ' + semResize + ') — repeated text, exactly as reported')
  : no('reproduction: the ±1 column did not corrupt (' + comResize + ' copies) — the scenario does not exercise the reflow');

// ── 2. THE PANEL GUARD, with the real xterm ─────────────────────────────────
// The fit is stubbed because it is the thing that MEASURES — and measuring is
// exactly what we are simulating. Terminal, reflow and column count are real.
const guarda = async (roteiro) => page.evaluate(`(async () => {
  const t = window.__term;
  // DRAIN before starting. Each scenario arms 300ms timers inside the guard
  // itself; if the next one starts before they expire, the timer of the PREVIOUS
  // scenario resizes the shared terminal in the middle of this one — and the pin
  // reports a defect that does not exist. It cost an investigation: worth waiting.
  await new Promise(r => setTimeout(r, 400));
  t.reset(); t.resize(56, 30);
  let prop = { cols: 56, rows: 30 };
  const fit = {
    _vpsmTerm: t,
    proposeDimensions: () => prop,
    fit: () => t.resize(prop.cols, prop.rows),
  };
  const app = { _safeFit: window.__safeFit };
  const passo = (c, r) => { prop = { cols: c, rows: r }; app._safeFit(fit); };
  const espera = (ms) => new Promise(r => setTimeout(r, ms));
  ${roteiro}
})()`);

const wobble = await guarda(`
  passo(55, 30);                    // the chrome steals a column
  const logo = t.cols;
  await espera(120);
  passo(56, 30);                    // and gives it back, before the 300ms
  await espera(450);
  return { logo, fim: t.cols };
`);
wobble.logo === 56 && wobble.fim === 56
  ? ok('panel: a ±1 column oscillation that undoes itself does NOT reach xterm (cols stayed 56)')
  : no('panel: the oscillation got through (immediate=' + wobble.logo + ', final=' + wobble.fim + ') — reflow happens');

const sustentado = await guarda(`
  passo(55, 30);
  const logo = t.cols;
  await espera(450);                // the second measurement agrees
  return { logo, fim: t.cols };
`);
sustentado.logo === 56 && sustentado.fim === 55
  ? ok('panel: a SUSTAINED ±1 column is applied on the second measurement (56 → 55)')
  : no('panel: a real 1 column change was not applied (immediate=' + sustentado.logo + ', final=' + sustentado.fim + ')');

const grande = await guarda(`
  passo(40, 30);                    // rotation / split: real intent
  return { logo: t.cols };
`);
grande.logo === 40
  ? ok('panel: a change of ≥2 columns goes through at once (no quarantine)')
  : no('panel: a real width change got stuck in the hysteresis (cols=' + grande.logo + ')');

const linhas = await guarda(`
  passo(55, 22);                    // virtual keyboard: width +-1, height changes
  return { cols: t.cols, rows: t.rows };
`);
linhas.cols === 56 && linhas.rows === 22
  ? ok('panel: the ROWS go through at once even with the column quarantined (virtual keyboard)')
  : no('panel: rows stuck along with the column (cols=' + linhas.cols + ', rows=' + linhas.rows + ') — the prompt stays hidden');

// ── 3. THE RECOVERY GUARD — separate code on purpose ────────────────────────
const rec = await page.evaluate(`(async () => {
  const t = window.__term;
  await new Promise(r => setTimeout(r, 400));   // drain the timers of the previous scenario
  t.reset(); t.resize(56, 30);
  let prop = { cols: 56, rows: 30 };
  window.__termAtual = t;
  window.__fitAtual = {
    proposeDimensions: () => prop,
    fit: () => t.resize(prop.cols, prop.rows),
  };
  const fitSeguro = window.__mkFitSeguro();
  const espera = (ms) => new Promise(r => setTimeout(r, ms));
  prop = { cols: 55, rows: 30 }; fitSeguro();
  const logo = t.cols;
  await espera(120);
  prop = { cols: 56, rows: 30 }; fitSeguro();
  await espera(450);
  const fim = t.cols;
  prop = { cols: 40, rows: 30 }; fitSeguro();
  return { logo, fim, grande: t.cols };
})()`);
rec.logo === 56 && rec.fim === 56
  ? ok('recovery: a ±1 column oscillation does not reach xterm')
  : no('recovery: the oscillation got through (immediate=' + rec.logo + ', final=' + rec.fim + ')');
rec.grande === 40
  ? ok('recovery: a change of ≥2 columns goes through at once')
  : no('recovery: a real change got stuck (cols=' + rec.grande + ')');

await browser.close();
srv.close();

// ── 4. No path escapes the guard ────────────────────────────────────────────
// The bug class only closes if EVERY fit goes through the guard. A new raw fit,
// added months later, reopens the hole with nothing flagging it.
{
  const fora = recovery.split('\n')
    .filter(l => /fitAddon\.fit\(\)/.test(l))
    .filter(l => !/proposeDimensions !== 'function'|term\.cols >= 2|^ {4}fitAddon\.fit\(\);$/.test(l));
  fora.length === 0
    ? ok('recovery: no raw fitAddon.fit() outside fitSeguro')
    : no('recovery: ' + fora.length + ' raw fit() outside the guard:' + fora.map(l => '\n      ' + l.trim()).join(''));

  const cruShell = shell.split('\n')
    .filter(l => /\bfit\.fit\(\)/.test(l))
    .filter(l => !/_safeFit/.test(l));
  // The only legitimate fit.fit() calls are the ones INSIDE _safeFit.
  const dentro = (corpoSafeFit.match(/fit\.fit\(\)/g) || []).length;
  cruShell.length === dentro
    ? ok('panel: all ' + dentro + ' existing fit.fit() calls are inside _safeFit')
    : no('panel: there is a fit.fit() outside _safeFit (' + cruShell.length + ' in the file, ' + dentro + ' in the guard)');
}

// ── 5. The usable width of the terminal (the desktop side of the same bug) ──
// Comments stripped: these assertions talk about CSS, and a comment that QUOTES
// the rule (e.g. the history of where it lived) must not pass for implementation
// nor trigger the counter-test. That is exactly what happened when it was moved.
const indexCss = index.replace(/\/\*[\s\S]*?\*\//g, '');
// The protection is no longer a reserved gutter, it is the impossibility of a
// scrollbar: on the terminal screen #conteudo does not scroll, so no bar can
// appear and steal a column. Same bug covered, at no width cost on any page.
/#conteudo\.is-noscroll\s*\{[^}]*overflow:\s*hidden/.test(indexCss)
  ? ok('desktop: #conteudo.is-noscroll does not scroll — no bar can steal a column from xterm')
  : no('desktop: the #conteudo.is-noscroll{overflow:hidden} rule is gone — the bar steals a column again');

// The rule only counts if someone turns it on in the terminal screen. Without
// this pair, the CSS above is orphaned and the guarantee vanishes silently.
/is-noscroll/.test(indexCss) && /currentView\s*===\s*'terminal'\s*\?\s*'is-noscroll'/.test(indexCss)
  ? ok('desktop: <main> turns .is-noscroll on in the terminal screen')
  : no('desktop: <main> no longer turns .is-noscroll on — the rule is orphaned');

// Counter-test: on html,body the reservation protects NO column at all (both have
// overflow:hidden, they never scroll) and still eats 10px+10px of the width of
// EVERY page — measured: window 1303 → body.clientWidth 1283. Back there = regression.
/html,\s*body\s*\{[^}]*scrollbar-gutter:\s*stable/.test(indexCss)
  ? no('desktop: scrollbar-gutter is back on html,body — a dead 20px strip, and they do not scroll')
  : ok('desktop: html,body reserve no gutter (they never scroll; reserving there only eats width)');

console.log('---');
console.log(pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
