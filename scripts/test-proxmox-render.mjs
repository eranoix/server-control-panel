#!/usr/bin/env node
// test-proxmox-render.mjs — RENDERS the Proxmox tab in a real browser.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 WHY THIS PIN EXISTS, AND WHY THE OTHERS WERE NOT ENOUGH
//
// The other two screen harnesses EVALUATE EXPRESSIONS: they take every `x-text`,
// `x-show`, `:attr` and run it against a hand-built state. That catches an
// expression that blows up — and that is how the "state is born null" class was
// closed out.
//
// But they went GREEN, twice, over a screen the operator watched break. The
// defect was in no expression at all: it was in DOM SEMANTICS. Inside `<svg>`
// the parser does not create an HTMLTemplateElement — it creates an unknown SVG
// element, which has no `.content` and is NOT inert. Alpine's `x-for` blew up in
// `importNode` and the children of the fake template were rendered anyway, with
// the loop variable out of scope. No isolated expression evaluation could see
// that, because the expressions were all correct.
//
// The lesson, the same one as always on this project: only EXECUTION proves
// behaviour. Here the execution is the browser drawing.
//
// 🔴 ABSENCE OF AN ERROR IS NOT APPROVAL. A `<path d="">` does not error and does
// not paint anything — which was exactly the outcome of the crash. So each step
// may demand a MEASUREMENT of what reached the DOM: how many charts, how many
// subpaths, whether the last point has a numeric coordinate, whether the "no
// sample" notice is VISIBLE. Without that the pin would approve a blank screen.
//
// 🔴 VISIBILITY IS MEASURED WITH checkVisibility(), NEVER WITH offsetParent.
// `offsetParent` is an HTMLElement property and does NOT exist on an SVG element:
// `svg.offsetParent !== null` is ALWAYS true. That hole has lied once in here
// already, in exactly the place where the charts live.
// ────────────────────────────────────────────────────────────────────────────
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';

// .tools/ is not versioned, so its absence has to say what to do — otherwise a
// clean clone fails with "Cannot find module" and nobody knows why.
const require_ = createRequire(path.join(path.resolve(path.dirname(new URL(import.meta.url).pathname), '..'), '.tools/'));
let chromium;
try {
  ({ chromium } = require_('playwright-core'));
} catch (e) {
  console.error('FAILED: playwright-core missing from .tools/. This pin RENDERS the screen in a browser —');
  console.error('       skipping would be faking coverage, which is how this screen broke twice in production.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');

// ── browser: resolved by SEARCH, not by a matched version ──────────────────
// Tying the playwright-core version to the downloaded chromium turns an
// `npm update` into a broken pin. The search takes the first one that exists.
function achaNavegador() {
  const cands = [];
  if (process.env.VPSM_CHROMIUM) cands.push(process.env.VPSM_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) {
    for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse()) {
      cands.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
    }
  }
  cands.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const c of cands) if (fs.existsSync(c)) return c;
  return null;
}

// ── the harness page: the REAL SECTION of index.html, the REAL BUNDLES ──────
// Cutting by line number would rot on the first edit. The section is found by
// the very marker that defines it.
//
// The tab stopped being a loose <section x-show="currentView==='proxmox'">:
// today it lazy-mounts via <template x-if>, in TWO sibling blocks, both gated on
// currentView==='proxmox' — the real content (once 41-proxmox.js has spread
// pvxStaleStyle over app()) and a safety net (a "reload" card for when the module
// never arrived). Extracting only the inner part would lose exactly that gate,
// which IS production behaviour; so both <template> blocks go in whole, and it is
// Alpine itself — with 41-proxmox.js loaded in the harness — that decides at
// runtime which of the two mounts.
const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');

// Each <template> can nest others (x-for, x-if of sub-blocks); an indexOf for the
// nearest '</template>' would close too early. Count depth to find the
// </template> that actually matches the opening.
function extraiTemplateBalanceado(html, aberturaLiteral, apartirDe) {
  const iAbre = html.indexOf(aberturaLiteral, apartirDe);
  if (iAbre < 0) return null;
  const reTag = /<template\b|<\/template>/g;
  reTag.lastIndex = iAbre;
  let profundidade = 0, m;
  while ((m = reTag.exec(html))) {
    profundidade += m[0] === '</template>' ? -1 : 1;
    if (profundidade === 0) return { html: html.slice(iAbre, reTag.lastIndex), fim: reTag.lastIndex };
  }
  return null;
}

const ANCORA = 'PROXMOX tab — the lab\'s SINGLE screen';
const iAncora = html.indexOf(ANCORA);
if (iAncora < 0) {
  console.error('FAILED: could not find the anchor comment for the Proxmox tab in index.html — the structure changed, update this test');
  process.exit(1);
}

const ABRE_TEMPLATE = `<template x-if="currentView==='proxmox'`;
const blocoReal = extraiTemplateBalanceado(html, ABRE_TEMPLATE, iAncora);
if (!blocoReal) { console.error('FAILED: could not find the <template x-if> holding the real content of the Proxmox tab in index.html'); process.exit(1); }
if (!/typeof pvxStaleStyle==='function'/.test(blocoReal.html)) {
  console.error('FAILED: the first <template x-if> of the Proxmox tab is no longer the one with the real content (the pvxStaleStyle gate is gone) — the structure changed, update this test');
  process.exit(1);
}

const blocoFallback = extraiTemplateBalanceado(html, ABRE_TEMPLATE, blocoReal.fim);
if (!blocoFallback) { console.error('FAILED: could not find the fallback <template x-if> (module not loaded) of the Proxmox tab in index.html'); process.exit(1); }

const secao = blocoReal.html + '\n' + blocoFallback.html;
if (secao.length < 20000) { console.error(`FAILED: the extracted section is only ${secao.length} bytes — the cut broke`); process.exit(1); }

const fixture = fs.readFileSync(path.join(RAIZ, 'scripts', 'proxmox-render-fixture.js'), 'utf8');

// Which bundle to render: `min` (what production serves) or `src` (the source).
const BUNDLE = process.env.VPSM_RENDER_BUNDLE === 'src' ? 'src' : 'min';

// 🔴 A STALE .min.js IS WORSE THAN A MISSING ONE: the server serves the old file
// without a word, so the screen in the air lags behind the repository code and
// every test that reads the source passes green over the top of it.
if (BUNDLE === 'min') {
  const dirApp = path.join(WEB, 'vendor', 'vpsm', 'app');
  const velhos = [];
  for (const nome of fs.readdirSync(dirApp).filter((x) => x.endsWith('.js') && !x.endsWith('.min.js'))) {
    const src = path.join(dirApp, nome);
    const min = path.join(dirApp, nome.slice(0, -3) + '.min.js');
    if (!fs.existsSync(min)) continue;
    if (fs.statSync(min).mtimeMs < fs.statSync(src).mtimeMs) velhos.push(nome);
  }
  if (velhos.length) {
    console.error('FAILED: a .min.js is OLDER than its source (the server serves the old one): ' + velhos.join(', '));
    console.error('       run `make minify`.');
    process.exit(1);
  }
}

// 🔴 THE PANEL CSS LIVES IN <style> INSIDE index.html, not in a file.
// Without bringing it along, the harness renders the section WITHOUT `.btn`,
// `.pvx-chip` and the rest — and then it can judge no styling at all: every
// button would look "background-less". That is exactly what the first version of
// the styling pin did.
const estilos = [...html.matchAll(/<style\b[^>]*>[\s\S]*?<\/style>/g)].map((m) => m[0]).join('\n');
if (estilos.length < 5000) {
  console.error(`FAILED: only ${estilos.length} bytes of <style> extracted from index.html — the styling pin would be measuring the void`);
  process.exit(1);
}

const pagina = `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/tailwind.css">
${estilos}
<style>[x-cloak]{display:none!important}</style>
</head><body x-data="app()">
${secao}
<script src="/vendor/vpsm/app/00-shell.js"></script>
<script src="/vendor/vpsm/app/10-git.js"></script>
<script src="/vendor/vpsm/app/20-deploy.js"></script>
<script src="/vendor/vpsm/app/30-agents.js"></script>
<script src="/vendor/vpsm/app/40-nodes.js"></script>
<script src="/vendor/vpsm/app/41-proxmox.js"></script>
<script src="/__fixture.js"></script>
<script defer src="/vendor/alpine/alpine.min.js"></script>
</body></html>`;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/') { res.setHeader('content-type', 'text/html'); return res.end(pagina); }
  if (u === '/__fixture.js') { res.setHeader('content-type', 'application/javascript'); return res.end(fixture); }
  // The harness does not test the network layer: any /api/ answers empty, and the
  // service worker is served blank. Without this the 404 noise would hide the
  // signal that matters.
  if (u.startsWith('/api/') ) { res.setHeader('content-type', 'application/json'); return res.end('{}'); }
  if (u === '/sw.js') { res.setHeader('content-type', 'application/javascript'); return res.end(''); }
  // the browser asks for it on its own; without this its 404 becomes noise in the first step
  if (u === '/favicon.ico') { res.setHeader('content-type', 'image/x-icon'); return res.end(''); }
  // 🔴 MIRRORS THE SERVER RULE (internal/api/api.go): in production,
  // `/vendor/.../x.js` serves `x.min.js` when it exists. Testing only the source
  // would leave out exactly the artifact that goes live — and a stale `.min.js`
  // (minification fails silently and the old one keeps being served) would pass
  // green. That is why the pin runs on BOTH bundles.
  let f = path.join(WEB, u);
  if (BUNDLE === 'min' && u.endsWith('.js') && !u.endsWith('.min.js')) {
    const m = f.slice(0, -3) + '.min.js';
    if (fs.existsSync(m)) f = m;
  }
  if (!f.startsWith(WEB) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) { res.statusCode = 404; return res.end('no'); }
  const ext = path.extname(f);
  res.setHeader('content-type', ext === '.js' ? 'application/javascript' : ext === '.css' ? 'text/css' : 'text/plain');
  res.end(fs.readFileSync(f));
});

const exe = achaNavegador();
if (!exe) {
  console.error('FAILED: no Chromium found. This pin RENDERS — skipping would be faking coverage.');
  console.error('       Install it with `npx playwright install chromium` or point VPSM_CHROMIUM=<path>.');
  process.exit(1);
}

await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const porta = srv.address().port;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();

const erros = [];
page.on('console', (m) => { if (m.type() === 'error') { const l = m.location(); erros.push('console: ' + m.text() + ' @ ' + (l ? l.url + ':' + l.lineNumber : '?')); } });
page.on('pageerror', (e) => erros.push('pageerror: ' + ((e && e.message) || e) + (process.env.VPSM_RENDER_DEBUG && e && e.stack ? '\n          ' + String(e.stack).split('\n').slice(0,4).join('\n          ') : '')));
page.on('response', (r) => { if (r.status() >= 400) erros.push('HTTP ' + r.status() + ': ' + r.url()); });
// says WHICH resource was missing, instead of the console's opaque 'Failed to load resource'
page.on('requestfailed', (r) => erros.push('resource failed: ' + r.url()));
if (process.env.VPSM_RENDER_DEBUG) page.on('request', (r) => console.log('    req ' + r.url()));

await page.goto(`http://127.0.0.1:${porta}/`, { waitUntil: 'networkidle' });
await page.waitForTimeout(400);
if (process.env.VPSM_DIAG) await page.evaluate(() => { window.__diag = true; });

const total = await page.evaluate(() => (window.__roteiro || []).length);
if (total < 20) {
  console.error(`FAILED: a script of ${total} steps — vacuous, the fixture did not load`);
  await browser.close(); srv.close(); process.exit(1);
}

let reprovados = 0, medidos = 0;
for (let i = 0; i < total; i++) {
  const nome = await page.evaluate((k) => window.__roteiro[k].nome, i);
  await page.evaluate((k) => window.__roteiro[k].passo(), i);
  await page.waitForTimeout(180);
  let nota = '';
  if (await page.evaluate((k) => !!window.__roteiro[k].exige, i)) {
    medidos++;
    const r = await page.evaluate((k) => window.__roteiro[k].exige(), i);
    if (r && r.erro) erros.push('measurement: ' + r.erro);
    if (r && r.nota) nota = '  [' + r.nota + ']';
  }
  const novos = erros.splice(0);
  if (novos.length) { reprovados++; console.log('  FAIL ' + nome + '\n        ' + novos.join('\n        ')); }
  else console.log('  ok   ' + nome + nota);
}

await browser.close();
srv.close();

// Double vacuity guard: steps actually run AND steps that MEASURED the DOM.
if (medidos < 8) { console.error(`FAILED: only ${medidos} steps measured the DOM — the rest only checked for the absence of an error`); process.exit(1); }
if (reprovados) { console.error(`\nFAILED: ${reprovados} of ${total} steps failed`); process.exit(1); }
console.log(`\nPASS — bundle ${BUNDLE}: ${total} steps rendered in the browser, ${medidos} of them measuring the DOM, 0 console errors`);
