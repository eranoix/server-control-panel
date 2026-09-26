#!/usr/bin/env node
// test-recovery-abas.mjs — the two tabs of /recovery must never be on screen
// at the same time.
//
// What the user saw (screenshot): the host terminal rendered on top AND the
// Claude notice underneath, with the host action bar visible in the wrong tab.
// Two screens splitting the height, neither of them usable.
//
// This is CSS semantics, not expression: only EXECUTION in a browser proves it.
// The pin renders the REAL page (the same HTML the server embeds), with the
// WebSockets neutralised, and measures visibility with checkVisibility().
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
  console.error('FAILURE: playwright-core missing from .tools/. This pin RENDERS — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-recovery-abas ===');

// The real page, with the WebSocket stubbed (the pin tests LAYOUT, not the
// transport — the transport has its own suite in test-recovery-term.mjs) and the
// container status answering whatever each scenario needs.
const pagina = fs.readFileSync(path.join(WEB, 'recovery-term.html'), 'utf8');
let containerRodando = false;

const srv = http.createServer((req, res) => {
  const u = req.url.split('?')[0];
  if (u === '/' || u === '/recovery/term') {
    res.setHeader('content-type', 'text/html');
    return res.end(pagina.replace('</head>', `<script>
      // A WebSocket that never opens: layout must not depend on a connection.
      window.WebSocket = function () { this.readyState = 0; this.close = function(){}; this.send = function(){}; };
      window.WebSocket.prototype.readyState = 0;
    </script></head>`));
  }
  // The page renews the session on its own (the recovery one lasts 30 min, and
  // expiring in the middle of a repair is the worst possible moment). The stub
  // answers so the pin can measure LAYOUT without renewal becoming 404 noise.
  if (u === '/recovery/renew') {
    res.setHeader('content-type', 'application/json');
    return res.end(JSON.stringify({ ok: true, restante_seg: 8 * 3600 }));
  }
  if (u === '/recovery/term') {
    res.statusCode = 200;
    return res.end('');
  }
  if (u === '/recovery/claude/status') {
    res.setHeader('content-type', 'application/json');
    return res.end(JSON.stringify({ ok: true, existe: true, rodando: containerRodando, autenticado: false, versao: '2.1.241 (Claude Code)' }));
  }
  const f = path.join(WEB, u);
  if (!f.startsWith(WEB) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) { res.statusCode = 404; return res.end('not found'); }
  const ext = path.extname(f);
  res.setHeader('content-type', ext === '.js' ? 'application/javascript' : ext === '.css' ? 'text/css' : 'text/plain');
  res.end(fs.readFileSync(f));
});

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

await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const porta = srv.address().port;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
const erros = [];
page.on('pageerror', (e) => erros.push('pageerror: ' + ((e && e.message) || e)));
// The browser asks for the favicon on its own and this test server does not
// serve it; counting that 404 as a page error would be noise masking signal.
page.on('console', (m) => {
  // The console "Failed to load resource" does not say WHICH resource; the URL
  // comes from the response handler below. Reporting both duplicates the failure.
  if (m.type() === 'error' && !/Failed to load resource/i.test(m.text())) erros.push('console: ' + m.text());
});
page.on('requestfailed', (r) => { if (!/favicon/i.test(r.url())) erros.push('resource failed: ' + r.url()); });
page.on('response', (r) => { if (r.status() >= 400 && !/favicon/i.test(r.url())) erros.push('HTTP ' + r.status() + ': ' + r.url()); });

const visivel = (sel) => page.evaluate((s) => {
  const el = document.querySelector(s);
  return !!el && el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
}, sel);
const clica = async (sel) => { await page.click(sel); await page.waitForTimeout(250); };

await page.goto(`http://127.0.0.1:${porta}/`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(400);

// ── 1. initial state: the host only ─────────────────────────────────────────
{
  const t = await visivel('#term'), c = await visivel('#term-claude'), off = await visivel('#claude-off');
  (t && !c && !off) ? ok('boot shows only the host terminal')
                    : no(`boot with overlapping screens (host=${t} claude=${c} notice=${off})`);
}

// ── 2. the reported bug: Claude tab with the container DOWN ─────────────────
// The screenshot showed the host terminal AND the Claude notice splitting the
// height, with the host action bar present in the wrong tab.
await clica('#aba-claude');
{
  const t = await visivel('#term'), off = await visivel('#claude-off');
  (!t && off) ? ok('Claude tab (container down): shows the notice and HIDES the host terminal')
              : no(`both screens splitting the height (host=${t} notice=${off}) — the reported bug`);
}
{
  const barra = await visivel('.action-bar');
  !barra ? ok('the host action bar disappears in the Claude tab')
         : no('host action bar visible in the Claude tab — an author `display:flex` beats the browser [hidden]');
}

// ── 3. back to the host ─────────────────────────────────────────────────────
await clica('#aba-host');
{
  const t = await visivel('#term'), off = await visivel('#claude-off'), barra = await visivel('.action-bar');
  (t && !off && barra) ? ok('going back to the host restores terminal + bar and hides the notice')
                       : no(`broken return (host=${t} notice=${off} bar=${barra})`);
}

// ── 4. with the container UP, the tab shows the Claude terminal ─────────────
containerRodando = true;
await clica('#aba-claude');
await page.waitForTimeout(400);
{
  const c = await visivel('#term-claude'), off = await visivel('#claude-off'), t = await visivel('#term');
  (c && !off && !t) ? ok('container up: the tab shows the Claude terminal, no notice and no host')
                    : no(`wrong Claude tab with the container up (claude=${c} notice=${off} host=${t})`);
}

// ── 5. the active terminal must FILL the area — not half a screen ──────────
// Without this, "visible" would pass at 20px tall, which is what the overlap
// produced in practice.
{
  const alturas = await page.evaluate(() => {
    const r = (s) => { const e = document.querySelector(s); return e ? e.getBoundingClientRect().height : 0; };
    return { claude: r('#term-claude'), janela: window.innerHeight };
  });
  (alturas.claude > alturas.janela * 0.5)
    ? ok(`active terminal fills the usable area (${Math.round(alturas.claude)}px of ${alturas.janela}px)`)
    : no(`active terminal squeezed: ${Math.round(alturas.claude)}px of ${alturas.janela}px — another screen is stealing height`);
}

erros.length === 0 ? ok('no console errors when switching tabs')
                   : no('errors:\n    ' + erros.slice(0, 6).join('\n    '));

await browser.close(); srv.close();
console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
