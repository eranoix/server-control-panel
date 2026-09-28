#!/usr/bin/env node

import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { createRequire } from 'node:module';

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const require_ = createRequire(path.join(ROOT, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin RENDERS — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

if (spawnSync('sh', ['-c', 'command -v dtach'], { stdio: 'ignore' }).status !== 0) {
  console.error('FAILED: dtach is not on the PATH. The shared terminal sessions this test drives need it.');
  console.error('       Install it with your package manager, for example: apt install dtach');
  process.exit(1);
}

function findBrowser() {
  const cands = [];
  if (process.env.PANEL_CHROMIUM) cands.push(process.env.PANEL_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) {
    for (const d of fs.readdirSync(cache).filter(x => x.startsWith('chromium-')).sort().reverse()) {
      cands.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
    }
  }
  cands.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const c of cands) if (fs.existsSync(c)) return c;
  return null;
}

const PASSWORD = 'primer-test-164';
const HASH = '$2b$10$NNDNlUzV7QQA/nXilNQ8P.F7xwyH4sYn9BsMBkz9epA./5aF1nQpu';
const SESSION = 'probe-primer';

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const wait = (ms) => new Promise(r => setTimeout(r, ms));

async function freePort() {
  return new Promise((res, rej) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); });
    s.on('error', rej);
  });
}

async function waitHealthy(url, capMs = 40000) {
  const until = Date.now() + capMs;
  while (Date.now() < until) {
    try {
      const r = await fetch(url + '/api/health');
      if (r.ok) return true;
    } catch { /* still coming up */ }
    await wait(300);
  }
  return false;
}

console.log('=== test-primer-browser ===');

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'panel-primer-'));
const binary = path.join(tmp, 'server-control-panel');
let server = null;

function shutdown() {
  try { server?.kill('SIGKILL'); } catch { /* already dead */ }
  spawnSync('pkill', ['-f', path.join(tmp, 'session-sox')], { stdio: 'ignore' });
  try { fs.rmSync(tmp, { recursive: true, force: true }); } catch { /* best-effort */ }
}
process.on('exit', shutdown);

try {
  console.log('• compiling the server…');
  const build = spawnSync('go', ['build', '-o', binary, './cmd/server'], { cwd: ROOT, encoding: 'utf8' });
  if (build.status !== 0) {
    console.error('FAILED: go build failed:\n' + (build.stderr || ''));
    process.exit(1);
  }

  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const cfg = path.join(tmp, 'config.json');
  fs.writeFileSync(cfg, JSON.stringify({
    schema_version: 2,
    primary: 'admin',
    listen: `127.0.0.1:${port}`,
    data_dir: tmp,
    jwt_secret: 'x'.repeat(64),
    auth_backend: 'local',
    claude_home: '/root/.claude',
    users: [{ username: 'admin', password_hash: HASH, admin: true }],
  }, null, 2));

  console.log(`• bringing the test instance up on ${base} (dataDir ${tmp})`);
  server = spawn(binary, [], {
    cwd: ROOT,
    env: { ...process.env, PANEL_CONFIG: cfg },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const serverLog = [];
  server.stdout.on('data', d => serverLog.push(String(d)));
  server.stderr.on('data', d => serverLog.push(String(d)));

  if (!await waitHealthy(base)) {
    console.error('FAILED: the test instance never went healthy. Log:\n' + serverLog.join('').slice(-3000));
    process.exit(1);
  }
  ok('the test instance of the real server came up');

  const exe = findBrowser();
  if (!exe) {
    console.error('FAILED: no chromium found. This pin RENDERS — skipping would be faking coverage.');
    process.exit(1);
  }
  const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

  async function openPanel(label) {
    const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
    const pag = await ctx.newPage();
    const routes = [];
    pag.on('request', r => { const u = r.url(); if (u.includes('/api/terminal/')) routes.push(u.replace(base, '')); });
    const errors = [];
    pag.on('pageerror', e => errors.push(String(e)));

    await pag.goto(base + '/', { waitUntil: 'domcontentloaded' });
    await pag.waitForSelector('#login-user', { timeout: 20000 });
    await pag.fill('#login-user', 'admin');
    await pag.fill('#login-pwd', PASSWORD);
    await pag.click('button[type="submit"]');
    await pag.waitForFunction(() => {
      const d = document.body._x_dataStack && document.body._x_dataStack[0];
      return !!(d && d.token);
    }, { timeout: 20000 });

    await pag.evaluate(async (name) => {
      const d = document.body._x_dataStack[0];
      d.page = 'dev';
      await new Promise(r => setTimeout(r, 300));
      const pane = d._makePane(name, '');
      d.terms.panes = [pane];
      d.terms.layout = { type: 'pane', id: pane.id };
      d.terms.activePane = pane.id;
      d.renderPaneLayout();
      await new Promise(r => setTimeout(r, 200));
      d.mountPane(pane);
    }, SESSION);

    const opened = await pag.waitForFunction(() => {
      const d = document.body._x_dataStack[0];
      const p = d.terms.panes[0];
      return !!(p && p.ws && p.ws.readyState === 1 && p.term);
    }, { timeout: 30000 }).then(() => true).catch(() => false);

    return { ctx, pag, routes, errors: errors, opened, label };
  }

  const pc1 = await openPanel('PC 1');
  if (!pc1.opened) {
    console.error('FAILED: the terminal did not connect on the first visit. Errors: ' + pc1.errors.join(' | '));
    console.error(serverLog.join('').slice(-2000));
    process.exit(1);
  }
  ok('the terminal connected and the pane mounted');

  await wait(1500);
  await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    p.ws.send(JSON.stringify({ type: 'input', data: 'for i in $(seq 1 120); do echo PROBE_$i; done\r' }));
  });
  await wait(4000);

  const sawOnPc1 = await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    const b = p.term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });
  (sawOnPc1.includes('PROBE_120'))
    ? ok('PC 1 sees the output it has just produced')
    : no('PC 1 cannot see its own output — the test never got as far as producing history');

  await pc1.ctx.close();
  await wait(2500);

  const pc2 = await openPanel('PC 2');
  if (!pc2.opened) {
    console.error('FAILED: the terminal did not connect on the second visit. Errors: ' + pc2.errors.join(' | '));
    process.exit(1);
  }
  await wait(2500);

  const sawOnPc2 = await pc2.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    const b = p.term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });

  const oldOnes = ['PROBE_1', 'PROBE_5', 'PROBE_20'].filter(m => {
    const re = new RegExp(m + '(?![0-9])');
    return re.test(sawOnPc2);
  });
  (oldOnes.length === 3)
    ? ok('PC 2 sees the lines that HAD ALREADY SCROLLED OFF — the history crossed the change of computer')
    : no(`PC 2 only found ${oldOnes.length}/3 of the old lines (${oldOnes.join(',') || 'none'}) — the primer did not bring the history`);

  (sawOnPc2.includes('PROBE_120'))
    ? ok('PC 2 also sees the CURRENT screen (the attach repaint still does its part)')
    : no('PC 2 cannot see the current screen — the attach repaint stopped working');

  const askedHistory = pc2.routes.some(u => u.startsWith('/api/terminal/history'));
  askedHistory
    ? ok('the primer asked for /api/terminal/history (and not the raw log)')
    : no('the primer did not ask for the rendered history; routes seen: ' + pc2.routes.join(', '));

  (pc2.errors.length === 0)
    ? ok('no page error along the way')
    : no('page errors: ' + pc2.errors.join(' | '));

  const colsOfLarge = await pc2.pag.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);

  const smallCtx = await browser.newContext({ viewport: { width: 520, height: 420 } });
  const smallPage = await smallCtx.newPage();
  await smallPage.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await smallPage.waitForSelector('#login-user', { timeout: 20000 });
  await smallPage.fill('#login-user', 'admin');
  await smallPage.fill('#login-pwd', PASSWORD);
  await smallPage.click('button[type="submit"]');
  await smallPage.waitForFunction(() => {
    const d = document.body._x_dataStack && document.body._x_dataStack[0];
    return !!(d && d.token);
  }, { timeout: 20000 });
  await smallPage.evaluate(async (name) => {
    const d = document.body._x_dataStack[0];
    d.page = 'dev';
    await new Promise(r => setTimeout(r, 300));
    const pane = d._makePane(name, '');
    d.terms.panes = [pane];
    d.terms.layout = { type: 'pane', id: pane.id };
    d.terms.activePane = pane.id;
    d.renderPaneLayout();
    await new Promise(r => setTimeout(r, 200));
    d.mountPane(pane);
  }, SESSION);
  await smallPage.waitForFunction(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    return !!(p && p.ws && p.ws.readyState === 1 && p.term);
  }, { timeout: 30000 }).catch(() => {});
  await wait(3000);

  const colsOfSmall = await smallPage.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);
  const colsOfLargeAfter = await pc2.pag.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);

  (colsOfSmall < colsOfLarge)
    ? ok(`the small window really is smaller (${colsOfSmall} against ${colsOfLarge} columns)`)
    : no(`the small window did not end up smaller (${colsOfSmall} x ${colsOfLarge}) — the test exercises nothing`);

  (colsOfLargeAfter === colsOfLarge)
    ? ok(`the big window did NOT shrink with the small one attached (${colsOfLargeAfter} columns)`)
    : no(`the big window shrank from ${colsOfLarge} to ${colsOfLargeAfter} — the smallest client is in charge of the session again`);

  await pc2.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    p.ws.send(JSON.stringify({ type: 'input', data: 'echo CLIP_MARK\r' }));
  });
  await wait(2500);
  const sawInSmall = await smallPage.evaluate(() => {
    const b = document.body._x_dataStack[0].terms.panes[0].term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });
  sawInSmall.includes('CLIP_MARK')
    ? ok('the small window sees the session through the rendered crop')
    : no('the small window cannot see the session — the crop is not arriving');

  await smallCtx.close();
  await pc2.ctx.close();
  await browser.close();
} catch (e) {
  console.error('FAILED (exception): ' + (e && e.stack || e));
  fail++;
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
