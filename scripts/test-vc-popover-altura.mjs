#!/usr/bin/env node
// test-vc-popover-altura.mjs — the video-call settings menu must not be clipped
// when the window is short.
//
// The popover lives INSIDE #vc-call-root, which is a panel (below the tabs, above
// the status bar) with overflow:hidden — not the viewport. While max-height was
// calc(100dvh - 88px), in a short window the menu ended up taller than the panel
// and the container clip ate the top of it: the user saw the first section cut in
// half with no way to scroll up to it.
//
// The pin does not read CSS: it mounts the REAL popover from index.html inside a
// panel shorter than the viewport, in a chromium, and measures the computed geometry.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin MEASURES geometry — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}
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
if (!exe) { console.error('FAILED: no Chromium found — skipping would be faking coverage.'); process.exit(1); }

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };

const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
const tail = fs.readFileSync(path.join(WEB, 'tailwind.css'), 'utf8');
const estilos = [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n');

// Cut out the REAL settings popover, matching <div> tags until it closes.
function recortaPopover() {
  const ini = html.indexOf('<div x-show="videocall.settingsPopOpen" class="vc-popover vc-popover-wide"');
  if (ini < 0) return null;
  const re = /<div\b|<\/div>/g;
  re.lastIndex = ini;
  let nivel = 0, m;
  while ((m = re.exec(html)) !== null) {
    nivel += m[0] === '</div>' ? -1 : 1;
    if (nivel === 0) return html.slice(ini, m.index + 6);
  }
  return null;
}
let pop = recortaPopover();
if (!pop) { no('settings popover not found in index.html — the pin lost its target'); process.exit(1); }
ok('settings popover cut out of index.html (' + pop.length + ' bytes)');
// Without Alpine, x-show/x-if do not evaluate. Strip x-show so every accordion
// group stays OPEN — the worst case for height, which is exactly what to measure.
pop = pop.replace(/x-show="[^"]*"/g, '').replace(/x-text="[^"]*"/g, '');

// A panel SHORTER than the viewport: this is the bug scenario (tabs above, status
// bar below). If the popover measured itself against the viewport, it would
// overflow from here.
const TOPO = 120, BASE = 90;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

for (const [larg, alt] of [[1015, 800], [1280, 720], [1400, 1080]]) {
  const page = await browser.newPage({ viewport: { width: larg, height: alt } });
  await page.setContent('<style>' + tail + '</style><style>' + estilos + '</style>'
    + '<body style="margin:0;height:' + alt + 'px;position:relative">'
    + '<div id="vc-call-root" class="vc-call-root" style="top:' + TOPO + 'px;bottom:' + BASE + 'px">'
    + '  <div class="vc-stage"><div id="vc-videos" class="vc-videos"></div>'
    + '    <div class="vc-bottombar">'
    + '      <div style="position:relative;display:inline-flex;">'
    + '        <button class="vc-btn">⚙</button>' + pop
    + '      </div></div></div></div></body>');

  // The same line production runs in _vcSetupPopoverBounds().
  await page.evaluate(() => {
    const el = document.getElementById('vc-call-root');
    const h = el.clientHeight;
    if (h > 0) el.style.setProperty('--vc-root-h', h + 'px');
  });
  await page.waitForTimeout(80);

  const g = await page.evaluate(() => {
    const raiz = document.getElementById('vc-call-root');
    const pop = document.querySelector('.vc-popover-wide');
    const r = raiz.getBoundingClientRect(), p = pop.getBoundingClientRect();
    return { raizTopo: r.top, raizBase: r.bottom, popTopo: p.top, popBase: p.bottom,
             rolavel: pop.scrollHeight > pop.clientHeight + 1,
             conteudo: pop.scrollHeight, visivel: pop.clientHeight };
  });
  const tag = larg + 'x' + alt + ' (panel ' + Math.round(g.raizBase - g.raizTopo) + 'px)';

  g.popTopo >= g.raizTopo - 0.5
    ? ok(tag + ': the top of the menu is inside the panel (' + Math.round(g.popTopo - g.raizTopo) + 'px to spare)')
    : no(tag + ': the top of the menu is CLIPPED — ' + Math.round(g.raizTopo - g.popTopo) + 'px above the panel');
  g.popBase <= g.raizBase + 0.5
    ? ok(tag + ': the bottom of the menu is inside the panel')
    : no(tag + ': the bottom of the menu is CLIPPED — ' + Math.round(g.popBase - g.raizBase) + 'px below the panel');
  // Clipping is not the same as scrolling: content that did not fit has to be reachable.
  (!g.rolavel || g.visivel > 200)
    ? ok(tag + ': content reachable by scrolling (' + g.conteudo + 'px inside ' + g.visivel + 'px of window)')
    : no(tag + ': the scroll window is far too small (' + g.visivel + 'px)');

  // Counter-check: without the panel measurement the bug comes back. If THIS
  // passes, the pin has stopped testing what the fix fixes.
  const semVar = await page.evaluate(() => {
    const raiz = document.getElementById('vc-call-root');
    raiz.style.removeProperty('--vc-root-h');
    const pop = document.querySelector('.vc-popover-wide');
    return pop.getBoundingClientRect().top - raiz.getBoundingClientRect().top;
  });
  semVar < -0.5
    ? ok(tag + ': counter-check — without --vc-root-h the menu overflows by ' + Math.round(-semVar) + 'px (the fix is what holds it)')
    : no(tag + ': inert counter-check — the menu already fitted without the fix, the pin proves nothing here');

  await page.close();
}

// Counter-check in the source: if anyone reintroduces the viewport measurement, it falls.
const regra = (html.match(/\.vc-popover \{[^}]*\}/) || [''])[0];
/max-height:\s*calc\(var\(--vc-root-h/.test(regra)
  ? ok('the .vc-popover rule measures against the panel (--vc-root-h)')
  : no('the .vc-popover rule went back to measuring against the viewport: ' + regra.replace(/\s+/g, ' '));
/_vcSetupPopoverBounds\(\)/.test(fs.readFileSync(path.join(WEB, 'vendor/vpsm/app/00-shell.js'), 'utf8'))
  ? ok('00-shell.js publishes the panel height')
  : no('00-shell.js no longer calls _vcSetupPopoverBounds() — the var falls back to the viewport');

await browser.close();
console.log('\n' + (fail ? '✗' : '✓') + ' ' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
