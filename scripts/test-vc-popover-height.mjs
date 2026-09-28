#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(ROOT, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(ROOT, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin MEASURES geometry — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}
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
if (!exe) { console.error('FAILED: no Chromium found — skipping would be faking coverage.'); process.exit(1); }

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };

const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
const tail = fs.readFileSync(path.join(WEB, 'tailwind.css'), 'utf8');
const styles = [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n');

function clipPopover() {
  const ini = html.indexOf('<div x-show="videocall.settingsPopOpen" class="vc-popover vc-popover-wide"');
  if (ini < 0) return null;
  const re = /<div\b|<\/div>/g;
  re.lastIndex = ini;
  let level = 0, m;
  while ((m = re.exec(html)) !== null) {
    level += m[0] === '</div>' ? -1 : 1;
    if (level === 0) return html.slice(ini, m.index + 6);
  }
  return null;
}
let pop = clipPopover();
if (!pop) { no('settings popover not found in index.html — the pin lost its target'); process.exit(1); }
ok('settings popover cut out of index.html (' + pop.length + ' bytes)');
pop = pop.replace(/x-show="[^"]*"/g, '').replace(/x-text="[^"]*"/g, '');

const TOP = 120, BASE = 90;
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

for (const [larg, alt] of [[1015, 800], [1280, 720], [1400, 1080]]) {
  const page = await browser.newPage({ viewport: { width: larg, height: alt } });
  await page.setContent('<style>' + tail + '</style><style>' + styles + '</style>'
    + '<body style="margin:0;height:' + alt + 'px;position:relative">'
    + '<div id="vc-call-root" class="vc-call-root" style="top:' + TOP + 'px;bottom:' + BASE + 'px">'
    + '  <div class="vc-stage"><div id="vc-videos" class="vc-videos"></div>'
    + '    <div class="vc-bottombar">'
    + '      <div style="position:relative;display:inline-flex;">'
    + '        <button class="vc-btn">⚙</button>' + pop
    + '      </div></div></div></div></body>');

  await page.evaluate(() => {
    const el = document.getElementById('vc-call-root');
    const h = el.clientHeight;
    if (h > 0) el.style.setProperty('--vc-root-h', h + 'px');
  });
  await page.waitForTimeout(80);

  const g = await page.evaluate(() => {
    const root = document.getElementById('vc-call-root');
    const pop = document.querySelector('.vc-popover-wide');
    const r = root.getBoundingClientRect(), p = pop.getBoundingClientRect();
    return { topRoot: r.top, baseRoot: r.bottom, popTop: p.top, popBase: p.bottom,
             rolavel: pop.scrollHeight > pop.clientHeight + 1,
             content: pop.scrollHeight, visible: pop.clientHeight };
  });
  const tag = larg + 'x' + alt + ' (panel ' + Math.round(g.baseRoot - g.topRoot) + 'px)';

  g.popTop >= g.topRoot - 0.5
    ? ok(tag + ': the top of the menu is inside the panel (' + Math.round(g.popTop - g.topRoot) + 'px to spare)')
    : no(tag + ': the top of the menu is CLIPPED — ' + Math.round(g.topRoot - g.popTop) + 'px above the panel');
  g.popBase <= g.baseRoot + 0.5
    ? ok(tag + ': the bottom of the menu is inside the panel')
    : no(tag + ': the bottom of the menu is CLIPPED — ' + Math.round(g.popBase - g.baseRoot) + 'px below the panel');
  (!g.rolavel || g.visible > 200)
    ? ok(tag + ': content reachable by scrolling (' + g.content + 'px inside ' + g.visible + 'px of window)')
    : no(tag + ': the scroll window is far too small (' + g.visible + 'px)');

  const noVar = await page.evaluate(() => {
    const root = document.getElementById('vc-call-root');
    root.style.removeProperty('--vc-root-h');
    const pop = document.querySelector('.vc-popover-wide');
    return pop.getBoundingClientRect().top - root.getBoundingClientRect().top;
  });
  noVar < -0.5
    ? ok(tag + ': counter-check — without --vc-root-h the menu overflows by ' + Math.round(-noVar) + 'px (the fix is what holds it)')
    : no(tag + ': inert counter-check — the menu already fitted without the fix, the pin proves nothing here');

  await page.close();
}

const rule = (html.match(/\.vc-popover \{[^}]*\}/) || [''])[0];
/max-height:\s*calc\(var\(--vc-root-h/.test(rule)
  ? ok('the .vc-popover rule measures against the panel (--vc-root-h)')
  : no('the .vc-popover rule went back to measuring against the viewport: ' + rule.replace(/\s+/g, ' '));
/_vcSetupPopoverBounds\(\)/.test(fs.readFileSync(path.join(WEB, 'vendor/panel/app/00-shell.js'), 'utf8'))
  ? ok('00-shell.js publishes the panel height')
  : no('00-shell.js no longer calls _vcSetupPopoverBounds() — the var falls back to the viewport');

await browser.close();
console.log('\n' + (fail ? '✗' : '✓') + ' ' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
