#!/usr/bin/env node
// test-primer-navegador.mjs
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

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
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
function achaNavegador() {
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
const SENHA = 'primer-teste-164';
const HASH = '$2b$10$hks7R40N0Nvnoi7G93AeWOcVcJ7RLoJs07NLywXcS0FLBAvRWrTRu';
const SESSAO = 'prova-primer';

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const espera = (ms) => new Promise(r => setTimeout(r, ms));

async function portaLivre() {
  return new Promise((res, rej) => {
    const s = net.createServer();
    s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => res(p)); });
    s.on('error', rej);
  });
}

async function esperaSaude(url, tetoMs = 40000) {
  const ate = Date.now() + tetoMs;
  while (Date.now() < ate) {
    try {
      const r = await fetch(url + '/api/health');
      if (r.ok) return true;
    } catch { /* still coming up */ }
    await espera(300);
  }
  return false;
}

console.log('=== test-primer-navegador ===');

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'vpsm-primer-'));
const binario = path.join(tmp, 'vps-manager');
let servidor = null;

function encerra() {
  try { servidor?.kill('SIGKILL'); } catch { /* already dead */ }
  // The test sessions live in the temporary dataDir; the dtach master outlives
  // the server on purpose, so this is where it gets killed.
  spawnSync('pkill', ['-f', path.join(tmp, 'session-sox')], { stdio: 'ignore' });
  try { fs.rmSync(tmp, { recursive: true, force: true }); } catch { /* best-effort */ }
}
process.on('exit', encerra);

try {
  console.log('• compiling the server…');
  const build = spawnSync('go', ['build', '-o', binario, './cmd/server'], { cwd: RAIZ, encoding: 'utf8' });
  if (build.status !== 0) {
    console.error('FAILED: go build failed:\n' + (build.stderr || ''));
    process.exit(1);
  }

  const porta = await portaLivre();
  const base = `http://127.0.0.1:${porta}`;
  const cfg = path.join(tmp, 'config.json');
  // schema_version 2 + an explicit primary: without it the v1->v2 migration runs
  // at boot and aborts looking for the production primary user.
  fs.writeFileSync(cfg, JSON.stringify({
    schema_version: 2,
    primary: 'admin',
    listen: `127.0.0.1:${porta}`,
    data_dir: tmp,
    jwt_secret: 'x'.repeat(64),
    auth_backend: 'local',
    claude_home: '/root/.claude',
    users: [{ username: 'admin', password_hash: HASH, admin: true }],
  }, null, 2));

  console.log(`• bringing the test instance up on ${base} (dataDir ${tmp})`);
  servidor = spawn(binario, [], {
    cwd: RAIZ,
    env: { ...process.env, VPSM_CONFIG: cfg },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  const logServidor = [];
  servidor.stdout.on('data', d => logServidor.push(String(d)));
  servidor.stderr.on('data', d => logServidor.push(String(d)));

  if (!await esperaSaude(base)) {
    console.error('FAILED: the test instance never went healthy. Log:\n' + logServidor.join('').slice(-3000));
    process.exit(1);
  }
  ok('the test instance of the real server came up');

  const exe = achaNavegador();
  if (!exe) {
    console.error('FAILED: no chromium found. This pin RENDERS — skipping would be faking coverage.');
    process.exit(1);
  }
  const navegador = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

  // Enter the panel, sign in, and open the session in a pane. Returns the context
  // for the caller to close — closing the context is the "leaving one PC".
  async function abrePainel(rotulo) {
    const ctx = await navegador.newContext({ viewport: { width: 1280, height: 800 } });
    const pag = await ctx.newPage();
    const rotas = [];
    pag.on('request', r => { const u = r.url(); if (u.includes('/api/terminal/')) rotas.push(u.replace(base, '')); });
    const erros = [];
    pag.on('pageerror', e => erros.push(String(e)));

    await pag.goto(base + '/', { waitUntil: 'domcontentloaded' });
    await pag.waitForSelector('#login-user', { timeout: 20000 });
    await pag.fill('#login-user', 'admin');
    await pag.fill('#login-pwd', SENHA);
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
    }, SESSAO);

    // Wait for the socket to actually open.
    const abriu = await pag.waitForFunction(() => {
      const d = document.body._x_dataStack[0];
      const p = d.terms.panes[0];
      return !!(p && p.ws && p.ws.readyState === 1 && p.term);
    }, { timeout: 30000 }).then(() => true).catch(() => false);

    return { ctx, pag, rotas, erros, abriu, rotulo };
  }

  // ── 1) first computer: produce history ─────────────────────────────────
  const pc1 = await abrePainel('PC 1');
  if (!pc1.abriu) {
    console.error('FAILED: the terminal did not connect on the first visit. Errors: ' + pc1.erros.join(' | '));
    console.error(logServidor.join('').slice(-2000));
    process.exit(1);
  }
  ok('the terminal connected and the pane mounted');

  await espera(1500);
  await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    p.ws.send(JSON.stringify({ type: 'input', data: 'for i in $(seq 1 120); do echo PROVA_$i; done\r' }));
  });
  await espera(4000);

  const viuNoPc1 = await pc1.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    const b = p.term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });
  (viuNoPc1.includes('PROVA_120'))
    ? ok('PC 1 sees the output it has just produced')
    : no('PC 1 cannot see its own output — the test never got as far as producing history');

  await pc1.ctx.close();   // ← "I leave one PC"
  await espera(2500);      // the recorder holds the session; the .hist file is written

  // ── 2) second computer: a new context, no cookie, no localStorage ──────
  const pc2 = await abrePainel('PC 2');
  if (!pc2.abriu) {
    console.error('FAILED: the terminal did not connect on the second visit. Errors: ' + pc2.erros.join(' | '));
    process.exit(1);
  }
  await espera(2500);

  const viuNoPc2 = await pc2.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    const b = p.term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });

  // THE ASSERTION THAT DID NOT EXIST. `PROVA_1` scrolled off a long time ago: it
  // can only be here if the primer brought the history back.
  const antigas = ['PROVA_1', 'PROVA_5', 'PROVA_20'].filter(m => {
    const re = new RegExp(m + '(?![0-9])');
    return re.test(viuNoPc2);
  });
  (antigas.length === 3)
    ? ok('PC 2 sees the lines that HAD ALREADY SCROLLED OFF — the history crossed the change of computer')
    : no(`PC 2 only found ${antigas.length}/3 of the old lines (${antigas.join(',') || 'none'}) — the primer did not bring the history`);

  (viuNoPc2.includes('PROVA_120'))
    ? ok('PC 2 also sees the CURRENT screen (the attach repaint still does its part)')
    : no('PC 2 cannot see the current screen — the attach repaint stopped working');

  const pediuHistorico = pc2.rotas.some(u => u.startsWith('/api/terminal/historico'));
  pediuHistorico
    ? ok('the primer asked for /api/terminal/historico (and not the raw log)')
    : no('the primer did not ask for the rendered history; routes seen: ' + pc2.rotas.join(', '));

  (pc2.erros.length === 0)
    ? ok('no page error along the way')
    : no('page errors: ' + pc2.erros.join(' | '));

  // ── 3) a SMALL window can no longer shrink the big one ─────────────────
  //
  // The session used to sit at the SMALLEST client: opening the terminal on a
  // phone at 53 columns put a 120-column desktop to work at 53. Now the session
  // sits at the LARGEST of the clients that accept a frame, and the smaller one
  // receives a rendered crop.
  const colsDoGrande = await pc2.pag.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);

  const ctxPequeno = await navegador.newContext({ viewport: { width: 520, height: 420 } });
  const pagPequena = await ctxPequeno.newPage();
  await pagPequena.goto(base + '/', { waitUntil: 'domcontentloaded' });
  await pagPequena.waitForSelector('#login-user', { timeout: 20000 });
  await pagPequena.fill('#login-user', 'admin');
  await pagPequena.fill('#login-pwd', SENHA);
  await pagPequena.click('button[type="submit"]');
  await pagPequena.waitForFunction(() => {
    const d = document.body._x_dataStack && document.body._x_dataStack[0];
    return !!(d && d.token);
  }, { timeout: 20000 });
  await pagPequena.evaluate(async (nome) => {
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
  }, SESSAO);
  await pagPequena.waitForFunction(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    return !!(p && p.ws && p.ws.readyState === 1 && p.term);
  }, { timeout: 30000 }).catch(() => {});
  await espera(3000);

  const colsDaPequena = await pagPequena.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);
  const colsDoGrandeDepois = await pc2.pag.evaluate(() => document.body._x_dataStack[0].terms.panes[0].term.cols);

  (colsDaPequena < colsDoGrande)
    ? ok(`the small window really is smaller (${colsDaPequena} against ${colsDoGrande} columns)`)
    : no(`the small window did not end up smaller (${colsDaPequena} x ${colsDoGrande}) — the test exercises nothing`);

  (colsDoGrandeDepois === colsDoGrande)
    ? ok(`the big window did NOT shrink with the small one attached (${colsDoGrandeDepois} columns)`)
    : no(`the big window shrank from ${colsDoGrande} to ${colsDoGrandeDepois} — the smallest client is in charge of the session again`);

  // And the small one still sees the session: the rendered crop reaches it.
  await pc2.pag.evaluate(() => {
    const p = document.body._x_dataStack[0].terms.panes[0];
    p.ws.send(JSON.stringify({ type: 'input', data: 'echo MARCA_RECORTE\r' }));
  });
  await espera(2500);
  const viuNaPequena = await pagPequena.evaluate(() => {
    const b = document.body._x_dataStack[0].terms.panes[0].term.buffer.active;
    let t = '';
    for (let i = 0; i < b.length; i++) { const l = b.getLine(i); if (l) t += l.translateToString(true) + '\n'; }
    return t;
  });
  viuNaPequena.includes('MARCA_RECORTE')
    ? ok('the small window sees the session through the rendered crop')
    : no('the small window cannot see the session — the crop is not arriving');

  await ctxPequeno.close();
  await pc2.ctx.close();
  await navegador.close();
} catch (e) {
  console.error('FAILED (exception): ' + (e && e.stack || e));
  fail++;
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
