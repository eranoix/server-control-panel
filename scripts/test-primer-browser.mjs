#!/usr/bin/env node
// test-primer-browser.mjs
//
// THIS IS THE TEST THAT WAS MISSING FROM THE PRIMER WORK.
//
// That work was verified in layers — e2e tests against the real HostShell,
// source pins, invariants live, the route literal checked in the SERVED bundle.
// None of those layers proves the one thing the work promises: OPEN THE SESSION
// ON ANOTHER COMPUTER AND WATCH THE HISTORY APPEAR.
//
// Proving the code is there is not proving that xterm paints.
//
// So here a TEST INSTANCE of the real server comes up (real binary, its own
// VPSM_CONFIG, its own port, a temporary dataDir — production is never touched)
// and a real browser walks the path from the report:
//
//   1. sign in to the panel
//   2. open a session and produce output that SCROLLS off the screen
//   3. CLOSE the whole browser (the "leaving one PC")
//   4. open a NEW context, with no cookie and no localStorage (the "arriving at
//      the other one")
//   5. require the lines that had already scrolled off to be there
//
// Step 5 is the assertion that did not exist. If the primer breaks, it fails.

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

// The sessions this test shares between windows are dtach sessions. Without
// dtach on the PATH the server gives every connection a shell of its own,
// nothing is shared, and the last step fails with a message about the crop
// that points at the wrong place. Same rule as above: fail loudly, never skip.
if (spawnSync('sh', ['-c', 'command -v dtach'], { stdio: 'ignore' }).status !== 0) {
  console.error('FAILED: dtach is not on the PATH. The shared terminal sessions this test drives need it.');
  console.error('       Install it with your package manager, for example: apt install dtach');
  process.exit(1);
}

// The browser binary: the playwright-core in .tools/ downloads no browser, and
// the system cache holds several versions. Same search as the other harnesses.
function findBrowser() {
  const cands = [];
  if (process.env.VPSM_CHROMIUM) cands.push(process.env.VPSM_CHROMIUM);
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

// The fixture password. The hash below is of it; the instance listens on
// 127.0.0.1 on an ephemeral port and dies at the end of the test.
const PASSWORD = 'primer-test-164';
const HASH = '$2b$10$NNDNlUzV7QQA/nXilNQ8P.F7xwyH4sYn9BsMBkz9epA./5aF1nQpu';
const SESSION = 'prova-primer';

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

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'vpsm-primer-'));
const binary = path.join(tmp, 'vps-manager');
let server = null;

function shutdown() {
  try { server?.kill('SIGKILL'); } catch { /* already dead */ }
  // The test sessions live in the temporary dataDir; the dtach master outlives
  // the server on purpose, so this is where it gets killed.
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
  // schema_version 2 + an explicit primary: without it the v1->v2 migration runs
  // at boot and aborts looking for the production primary user.
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
    env: { ...process.env, VPSM_CONFIG: cfg },
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
  const navegador = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

  // Enter the panel, sign in, and open the session in a pane. Returns the context
  // for the caller to close — closing the context is the "leaving one PC".
  async function openPanel(label) {
    const ctx = await navegador.newContext({ viewport: { width: 1280, height: 800 } });
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
    // The panel only exists after the token; the app itself swaps the screen.
    await pag.waitForFunction(() => {
      const d = document.body._x_dataStack && document.body._x_dataStack[0];
      return !!(d && d.token);
    }, { timeout: 20000 });

    // Open the terminal tab and mount a pane on the test session, through the
    // app's REAL METHODS — the same path as the click, without hunting selectors.
    await pag.evaluate(async (nome) => {
      const d = document.body._x_dataStack[0];
      d.page = 'dev';
      await new Promise(r => setTimeout(r, 300));
      const pane = d._makePane(nome, '');
      d.terms.panes = [pane];
      d.terms.layout = { type: 'pane', id: pane.id };
      d.terms.activePane = pane.id;
      d.renderPaneLayout();
      await new Promise(r => setTimeout(r, 200));
      d.mountPane(pane);
    }, SESSION);

    // Wait for the socket to actually open.
    const opened = await pag.waitForFunction(() => {
      const d = document.body._x_dataStack[0];
      const p = d.terms.panes[0];
      return !!(p && p.ws && p.ws.readyState === 1 && p.term);
    }, { timeout: 30000 }).then(() => true).catch(() => false);

    return { ctx, pag, routes, erros: errors, opened, label };
  }

  // ── 1) first computer: produce history ─────────────────────────────────
  const pc1 = await openPanel('PC 1');
  if (!pc1.opened) {
    console.error('FAILED: the terminal did not connect on the first visit. Errors: ' + pc1.erros.join(' | '));
    console.error(serverLog.join('').slice(-2000));
    process.exit(1);
  }
  ok('the terminal connected and the pane mounted');

  await wait(1500);
  await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    p.ws.send(JSON.stringify({ type: 'input', data: 'for i in $(seq 1 120); do echo PROVA_$i; done\r' }));
  });
  await wait(4000);

  const sawOnPc1 = await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    const b = p.term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });
  (sawOnPc1.includes('PROVA_120'))
    ? ok('PC 1 sees the output it has just produced')
    : no('PC 1 cannot see its own output — the test never got as far as producing history');

  await pc1.ctx.close();   // ← "I leave one PC"
  await wait(2500);      // the recorder holds the session; the .hist file is written

  // ── 2) second computer: a new context, no cookie, no localStorage ──────
  const pc2 = await openPanel('PC 2');
  if (!pc2.opened) {
    console.error('FAILED: the terminal did not connect on the second visit. Errors: ' + pc2.erros.join(' | '));
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

  // THE ASSERTION THAT DID NOT EXIST. `PROVA_1` scrolled off a long time ago: it
  // can only be here if the primer brought the history back.
  const oldOnes = ['PROVA_1', 'PROVA_5', 'PROVA_20'].filter(m => {
    const re = new RegExp(m + '(?![0-9])');
    return re.test(sawOnPc2);
  });
  (oldOnes.length === 3)
    ? ok('PC 2 sees the lines that HAD ALREADY SCROLLED OFF — the history crossed the change of computer')
    : no(`PC 2 only found ${oldOnes.length}/3 of the old lines (${oldOnes.join(',') || 'none'}) — the primer did not bring the history`);

  (sawOnPc2.includes('PROVA_120'))
    ? ok('PC 2 also sees the CURRENT screen (the attach repaint still does its part)')
    : no('PC 2 cannot see the current screen — the attach repaint stopped working');

  const askedHistory = pc2.routes.some(u => u.startsWith('/api/terminal/historico'));
  askedHistory
    ? ok('the primer asked for /api/terminal/historico (and not the raw log)')
    : no('the primer did not ask for the rendered history; routes seen: ' + pc2.routes.join(', '));

  (pc2.erros.length === 0)
    ? ok('no page error along the way')
    : no('page errors: ' + pc2.erros.join(' | '));

  // ── 3) a SMALL window can no longer shrink the big one ─────────────────
  //
  // The session used to sit at the SMALLEST client: opening the terminal on a
  // phone at 53 columns put a 120-column desktop to work at 53. Now the session
  // sits at the LARGEST of the clients that accept a frame, and the smaller one
  // receives a rendered crop.
  const colsOfLarge = await pc2.pag.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);

  const smallCtx = await navegador.newContext({ viewport: { width: 520, height: 420 } });
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
  await smallPage.evaluate(async (nome) => {
    const d = document.body._x_dataStack[0];
    d.page = 'dev';
    await new Promise(r => setTimeout(r, 300));
    const pane = d._makePane(nome, '');
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

  // And the small one still sees the session: the rendered crop reaches it.
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
  await navegador.close();
} catch (e) {
  console.error('FAILED (exception): ' + (e && e.stack || e));
  fail++;
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
