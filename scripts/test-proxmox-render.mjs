#!/usr/bin/env node
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { createRequire } from 'node:module';

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

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(ROOT, 'internal', 'webassets', 'web');

function findBrowser() {
  const cands = [];
  if (process.env.PANEL_CHROMIUM) cands.push(process.env.PANEL_CHROMIUM);
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

const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');

function extractBalancedTemplate(html, literalOpening, startingFrom) {
  const iOpen = html.indexOf(literalOpening, startingFrom);
  if (iOpen < 0) return null;
  const reTag = /<template\b|<\/template>/g;
  reTag.lastIndex = iOpen;
  let depth = 0, m;
  while ((m = reTag.exec(html))) {
    depth += m[0] === '</template>' ? -1 : 1;
    if (depth === 0) return { html: html.slice(iOpen, reTag.lastIndex), end: reTag.lastIndex };
  }
  return null;
}

const ANCHOR = 'PROXMOX tab — the lab\'s SINGLE screen';
const iAnchor = html.indexOf(ANCHOR);
if (iAnchor < 0) {
  console.error('FAILED: could not find the anchor comment for the Proxmox tab in index.html — the structure changed, update this test');
  process.exit(1);
}

const OPEN_TEMPLATE = `<template x-if="currentView==='proxmox'`;
const realBlock = extractBalancedTemplate(html, OPEN_TEMPLATE, iAnchor);
if (!realBlock) { console.error('FAILED: could not find the <template x-if> holding the real content of the Proxmox tab in index.html'); process.exit(1); }
if (!/typeof pvxStaleStyle==='function'/.test(realBlock.html)) {
  console.error('FAILED: the first <template x-if> of the Proxmox tab is no longer the one with the real content (the pvxStaleStyle gate is gone) — the structure changed, update this test');
  process.exit(1);
}

const fallbackBlock = extractBalancedTemplate(html, OPEN_TEMPLATE, realBlock.end);
if (!fallbackBlock) { console.error('FAILED: could not find the fallback <template x-if> (module not loaded) of the Proxmox tab in index.html'); process.exit(1); }

const section = realBlock.html + '\n' + fallbackBlock.html;
if (section.length < 20000) { console.error(`FAILED: the extracted section is only ${section.length} bytes — the cut broke`); process.exit(1); }

const fixture = fs.readFileSync(path.join(ROOT, 'scripts', 'proxmox-render-fixture.js'), 'utf8');

const BUNDLE = process.env.PANEL_RENDER_BUNDLE === 'src' ? 'src' : 'min';

if (BUNDLE === 'min') {
  const dirApp = path.join(WEB, 'vendor', 'panel', 'app');
  const stale = [];
  for (const name of fs.readdirSync(dirApp).filter((x) => x.endsWith('.js') && !x.endsWith('.min.js'))) {
    const src = path.join(dirApp, name);
    const min = path.join(dirApp, name.slice(0, -3) + '.min.js');
    if (!fs.existsSync(min)) continue;
    if (fs.statSync(min).mtimeMs < fs.statSync(src).mtimeMs) stale.push(name);
  }
  if (stale.length) {
    console.error('FAILED: a .min.js is OLDER than its source (the server serves the old one): ' + stale.join(', '));
    console.error('       run `make minify`.');
    process.exit(1);
  }
}

const styles = [...html.matchAll(/<style\b[^>]*>[\s\S]*?<\/style>/g)].map((m) => m[0]).join('\n');
if (styles.length < 5000) {
  console.error(`FAILED: only ${styles.length} bytes of <style> extracted from index.html — the styling pin would be measuring the void`);
  process.exit(1);
}

const pageHtml = `<!doctype html><html><head><meta charset="utf-8">
<link rel="stylesheet" href="/tailwind.css">
${styles}
<style>[x-cloak]{display:none!important}</style>
</head><body x-data="app()">
${section}
<script src="/vendor/panel/app/00-shell.js"></script>
<script src="/vendor/panel/app/10-git.js"></script>
<script src="/vendor/panel/app/20-deploy.js"></script>
<script src="/vendor/panel/app/30-agents.js"></script>
<script src="/vendor/panel/app/40-nodes.js"></script>
<script src="/vendor/panel/app/41-proxmox.js"></script>
<script src="/__fixture.js"></script>
<script defer src="/vendor/alpine/alpine.min.js"></script>
</body></html>`;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/') { res.setHeader('content-type', 'text/html'); return res.end(pageHtml); }
  if (u === '/__fixture.js') { res.setHeader('content-type', 'application/javascript'); return res.end(fixture); }
  if (u.startsWith('/api/') ) { res.setHeader('content-type', 'application/json'); return res.end('{}'); }
  if (u === '/sw.js') { res.setHeader('content-type', 'application/javascript'); return res.end(''); }
  if (u === '/favicon.ico') { res.setHeader('content-type', 'image/x-icon'); return res.end(''); }
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

const exe = findBrowser();
if (!exe) {
  console.error('FAILED: no Chromium found. This pin RENDERS — skipping would be faking coverage.');
  console.error('       Install it with `npx playwright install chromium` or point PANEL_CHROMIUM=<path>.');
  process.exit(1);
}

await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const port = srv.address().port;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();

const errors = [];
page.on('console', (m) => { if (m.type() === 'error') { const l = m.location(); errors.push('console: ' + m.text() + ' @ ' + (l ? l.url + ':' + l.lineNumber : '?')); } });
page.on('pageerror', (e) => errors.push('pageerror: ' + ((e && e.message) || e) + (process.env.PANEL_RENDER_DEBUG && e && e.stack ? '\n          ' + String(e.stack).split('\n').slice(0,4).join('\n          ') : '')));
page.on('response', (r) => { if (r.status() >= 400) errors.push('HTTP ' + r.status() + ': ' + r.url()); });
page.on('requestfailed', (r) => errors.push('resource failed: ' + r.url()));
if (process.env.PANEL_RENDER_DEBUG) page.on('request', (r) => console.log('    req ' + r.url()));

await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: 'networkidle' });
await page.waitForTimeout(400);
if (process.env.PANEL_DIAG) await page.evaluate(() => { window.__diag = true; });

const total = await page.evaluate(() => (window.__script || []).length);
if (total < 20) {
  console.error(`FAILED: a script of ${total} steps — vacuous, the fixture did not load`);
  await browser.close(); srv.close(); process.exit(1);
}

let rejected = 0, measured = 0;
for (let i = 0; i < total; i++) {
  const name = await page.evaluate((k) => window.__script[k].name, i);
  await page.evaluate((k) => window.__script[k].step(), i);
  await page.waitForTimeout(180);
  let note = '';
  if (await page.evaluate((k) => !!window.__script[k].expect, i)) {
    measured++;
    const r = await page.evaluate((k) => window.__script[k].expect(), i);
    if (r && r.error) errors.push('measurement: ' + r.error);
    if (r && r.note) note = '  [' + r.note + ']';
  }
  const newOnes = errors.splice(0);
  if (newOnes.length) { rejected++; console.log('  FAIL ' + name + '\n        ' + newOnes.join('\n        ')); }
  else console.log('  ok   ' + name + note);
}

await browser.close();
srv.close();

if (measured < 8) { console.error(`FAILED: only ${measured} steps measured the DOM — the rest only checked for the absence of an error`); process.exit(1); }
if (rejected) { console.error(`\nFAILED: ${rejected} of ${total} steps failed`); process.exit(1); }
console.log(`\nPASS — bundle ${BUNDLE}: ${total} steps rendered in the browser, ${measured} of them measuring the DOM, 0 console errors`);
