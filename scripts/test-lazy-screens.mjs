#!/usr/bin/env node
// test-lazy-screens.mjs — the heavy screens must not be built at boot,
// and must not be destroyed afterwards.
//
// The gain: four sections (maintenance, schedules, AI, games) add up to ~195 KB
// of markup and thousands of nodes that the browser built and Alpine walked on
// EVERY load, even for someone who never opened those screens. Wrapped in a
// <template x-if="_mounted.X">, they are only born on the first visit.
//
// The risk, and the reason this pin exists: an x-if tied to VISIBILITY would
// destroy the screen on a tab switch, losing scroll and state — that is how the
// code-server iframe broke. The _mounted latch never goes back to false, and
// that is what has to stay true.
//
// Only EXECUTION proves it: the test renders the REAL markup in a real browser
// and measures the DOM before and after navigating.
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
  console.error('FAILED: playwright-core missing from .tools/. This pin RENDERS — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

const SCREENS = ['maintenance', 'schedules', 'ai', 'gamesettings'];
const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');

// Cut each <template x-if="_mounted.X"> … </template> out of the real markup,
// counting nesting (there are hundreds of <template> tags inside them).
function clip(name) {
  const open = `<template x-if="_mounted.${name}">`;
  const ini = html.indexOf(open);
  if (ini < 0) return null;
  let prof = 0;
  const re = /<template\b|<\/template>/g;
  re.lastIndex = ini;
  for (let m; (m = re.exec(html)); ) {
    prof += m[0][1] === '/' ? -1 : 1;
    if (prof === 0) return html.slice(ini, m.index + '</template>'.length);
  }
  return null;
}
const blocks = SCREENS.map((t) => {
  const b = clip(t);
  if (!b) { console.error(`FAILED: the screen "${t}" is no longer inside <template x-if="_mounted.${t}"> — the lazy mount has been undone`); process.exit(1); }
  if (b.length < 15000) { console.error(`FAILED: the block for "${t}" is only ${b.length} bytes — the cut broke`); process.exit(1); }
  return b;
});

const styles = [...html.matchAll(/<style\b[^>]*>[\s\S]*?<\/style>/g)].map((m) => m[0]).join('\n');

// The fixture neutralises only the NETWORK and init(): everything else is the
// real app(), with the real initial state. If one of these screens depended on
// state that only exists after a fetch, the pin has to say so — today they render
// at boot with the default state, so continuing to render is exactly the contract.
const fixture = `
  window.fetch = () => Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve({}), text: () => Promise.resolve('') });
  const appReal = window.app;
  window.app = function(){
    const o = appReal();
    o.init = function(){};      // no timers, no loading: the pin measures the DOM
    o.token = 'pin';
    return o;
  };
`;

const pageHtml = `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/tailwind.css">
${styles}
<style>[x-cloak]{display:none!important}</style>
</head><body x-data="app()">
${blocks.join('\n')}
<script src="/vendor/panel/app/00-shell.js"></script>
<script src="/vendor/panel/app/10-git.js"></script>
<script src="/vendor/panel/app/20-deploy.js"></script>
<script src="/vendor/panel/app/30-agents.js"></script>
<script src="/vendor/panel/app/40-nodes.js"></script>
<script src="/vendor/panel/app/41-proxmox.js"></script>
<script>${fixture}</script>
<script defer src="/vendor/alpine/alpine.min.js"></script>
</body></html>`;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/') { res.setHeader('content-type', 'text/html'); return res.end(pageHtml); }
  if (u.startsWith('/api/')) { res.setHeader('content-type', 'application/json'); return res.end('{}'); }
  if (u === '/favicon.ico') { res.setHeader('content-type', 'image/x-icon'); return res.end(''); }
  const f = path.join(WEB, u);
  if (!f.startsWith(WEB) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) { res.statusCode = 404; return res.end('no'); }
  const ext = path.extname(f);
  res.setHeader('content-type', ext === '.js' ? 'application/javascript' : ext === '.css' ? 'text/css' : 'text/plain');
  res.end(fs.readFileSync(f));
});

function findBrowser() {
  const cands = [];
  if (process.env.PANEL_CHROMIUM) cands.push(process.env.PANEL_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse())
    cands.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
  cands.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const c of cands) if (fs.existsSync(c)) return c;
  return null;
}
const exe = findBrowser();
if (!exe) { console.error('FAILED: no Chromium found — skipping would be faking coverage.'); process.exit(1); }

await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const port = srv.address().port;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push('pageerror: ' + ((e && e.message) || e)));
page.on('console', (m) => { if (m.type() === 'error') errors.push('console: ' + m.text()); });

await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'networkidle' });
await page.waitForTimeout(300);

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };

const count = (screen) => page.evaluate((t) => document.querySelectorAll(`section[x-show*="${t}"] *`).length, screen);

// ── 1. at boot, none of them exist ──────────────────────────────────────────
{
  let born = [];
  for (const t of SCREENS) if ((await count(t)) > 0) born.push(t);
  born.length === 0
    ? ok('boot builds none of the heavy screens (that is the whole gain)')
    : no('screens built at boot anyway: ' + born.join(', '));
}

// ── 2. the first visit mounts — and really mounts, with content ─────────────
const sizes = {};
for (const t of SCREENS) {
  await page.evaluate((screen) => {
    const root = document.querySelector('[x-data]');
    const app = Alpine.$data(root);
    app.currentView = screen;
    app._triggerViewLoaders(screen);
  }, t);
  await page.waitForTimeout(150);
  const n = await count(t);
  const exists = await page.evaluate((screen) => !!document.querySelector(`section[x-show*="${screen}"]`), t);
  sizes[t] = n;
  // The floor is deliberately low and measures what can be measured without
  // inventing: part of the content of these screens is only born after a fetch
  // (games.settings, for one, keeps the whole body hidden under the default
  // state). What the pin asserts is the 0 → mounted transition, with the real
  // section in the DOM and no error — a broken mount gives 0 nodes or blows up in
  // the console, and both are caught.
  (exists && n > 10)
    ? ok(`visiting "${t}" mounts the screen (${n} nodes)`)
    : no(`"${t}" did not mount (section present=${exists}, ${n} nodes)`);
}

// ── 3. the latch: leaving the screen must NOT destroy it ────────────────────
{
  await page.evaluate(() => {
    const app = Alpine.$data(document.querySelector('[x-data]'));
    app.currentView = 'terminal';
    app._triggerViewLoaders('terminal');
  });
  await page.waitForTimeout(150);
  const lost = [];
  for (const t of SCREENS) if ((await count(t)) < sizes[t]) lost.push(t);
  lost.length === 0
    ? ok('leaving the screen does not destroy the DOM (scroll and state survive — the code-server lesson)')
    : no('screens destroyed on a tab switch: ' + lost.join(', '));
}

// ── 4. rendering must not cost an error ─────────────────────────────────────
errors.length === 0
  ? ok('no console/page error while mounting the four screens')
  : no('errors while mounting:\n    ' + errors.slice(0, 8).join('\n    '));

await browser.close(); srv.close();
console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
