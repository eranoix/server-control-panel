#!/usr/bin/env node
// test-vc-bottombar-mobile.mjs — on the mobile layout, a menu item with long
// text has to GROW, not overlap the one below it.
//
// `.vc-bottombar button { width:48px!important; height:48px!important }` is a
// DESCENDANT selector: the popovers (mic, camera, settings) are children of the
// bottombar itself, so the touch-target rule for the round buttons also landed on
// the ~60 menu lines, which are wide and carry text that wraps. Squeezed into
// 48x48 with no clip, they invaded one another — the text turned into illegible
// soup.
//
// The pin does not read CSS: it mounts the REAL bottombar from index.html in a
// chromium at mobile width and measures box by box.
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
  console.error('       Install with: make tools');
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

// Cut out the REAL bottombar (with the popovers inside it, which is the bug's root).
function clip(mark) {
  const ini = html.indexOf(mark);
  if (ini < 0) return null;
  let n = 0;
  for (const m of html.slice(ini).matchAll(/<div\b|<\/div>/g)) {
    n += m[0] === '</div>' ? -1 : 1;
    if (n === 0) return html.slice(ini, ini + m.index + 6);
  }
  return null;
}
let bar = clip('<div class="vc-bottombar"');
if (!bar) { no('vc-bottombar not found in index.html — the pin lost its target'); process.exit(1); }
ok('vc-bottombar cut out of index.html (' + bar.length + ' bytes)');

// Without Alpine, <template x-for> does not instantiate. Materialise each list
// with REAL device labels (the ones from the report: long, and therefore the ones
// that break).
const LABELS = ['Default - Headset (Sonos Ace)', 'Communications - Headphones (Sonos Ace)', 'Default - Digital Output (Unknown)'];
bar = bar.replace(/<template[^>]*x-for[^>]*>([\s\S]*?)<\/template>/g, (_, inner) =>
  LABELS.map((r) => inner.replace(/<span x-text="[^"]*"><\/span>/g, '<span>' + r + '</span>')).join(''));
bar = bar.replace(/x-show="[^"]*"/g, '').replace(/x-text="[^"]*"/g, '').replace(/x-if="[^"]*"/g, '');

const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
// 320 (the old iPhone SE) up to 700. 767.98px is the project's mobile boundary.
for (const larg of [320, 380, 430, 700]) {
  const page = await browser.newPage({ viewport: { width: larg, height: 780 } });
  await page.setContent('<style>' + tail + '</style><style>' + styles + '</style>'
    + '<body style="margin:0"><div id="vc-call-root" class="vc-call-root">'
    + '<div class="vc-stage"><div id="vc-videos" class="vc-videos"></div>' + bar + '</div></div></body>');
  // The same lines production runs in _vcSetupPopoverBounds().
  await page.evaluate(() => {
    const el = document.getElementById('vc-call-root');
    if (el.clientHeight > 0) el.style.setProperty('--vc-root-h', el.clientHeight + 'px');
    if (el.clientWidth > 0) el.style.setProperty('--vc-root-w', el.clientWidth + 'px');
  });
  await page.waitForTimeout(120);

  const r = await page.evaluate(() => {
    // First-level sheets only: a NESTED popover (the language picker, inside the
    // settings one) only has a box while its parent is visible — measuring it in
    // isolation would return 0px and report a defect that does not exist.
    const pops = [...document.querySelectorAll('.vc-bottombar .vc-popover')]
      .filter((p) => !p.parentElement.closest('.vc-popover'));
    const out = [];
    for (const [i, pop] of pops.entries()) {
      // One popover at a time: on mobile they all become position:fixed and stack.
      pops.forEach((p) => { p.style.display = 'none'; });
      pop.style.display = 'block';
      // A nested popover (the language picker) is position:fixed on mobile: its
      // items would appear at the top of the screen and skew the overlap check.
      const ownHost = (el) => el.closest('.vc-popover') === pop;
      const items = [...pop.querySelectorAll('.vc-popover-item')].filter(ownHost);
      // Wide coverage: quality pills, primary buttons and accordion headers
      // suffered from the SAME selector and also had text squeezed.
      const others = [...pop.querySelectorAll('button')].filter(ownHost)
        .filter((b) => !b.classList.contains('vc-popover-item') && b.textContent.trim().length > 2);
      let clipped = 0, overlapping = 0, sample = '';
      for (const it of items) {
        if (it.scrollHeight > it.clientHeight + 1) { clipped++; if (!sample) sample = it.textContent.trim().slice(0, 34); }
      }
      for (let k = 1; k < items.length; k++) {
        const a = items[k - 1].getBoundingClientRect(), b = items[k].getBoundingClientRect();
        if (b.top < a.bottom - 0.5) overlapping++;
      }
      let othersClipped = 0, otherSample = '';
      for (const b of others) {
        if (b.scrollHeight > b.clientHeight + 1 || b.scrollWidth > b.clientWidth + 1) {
          othersClipped++; if (!otherSample) otherSample = b.textContent.trim().replace(/\s+/g, ' ').slice(0, 30);
        }
      }
      out.push({ i, items: items.length, clipped, overlapping, sample, others: others.length, othersClipped, otherSample });
    }
    // The mobile sheet has to fill the PANEL. .vc-bottombar has a transform and a
    // backdrop-filter, so it becomes the containing block for its fixed
    // descendants: `left:8;right:8` resolved against the little bar and the sheet
    // was born its width. Without this measurement, the pin does not see the defect.
    const panel = document.getElementById('vc-call-root').getBoundingClientRect();
    const barEl = document.querySelector('.vc-bottombar');
    const bar = barEl.getBoundingClientRect();
    const barButtons = [...barEl.querySelectorAll('.vc-btn')].filter((b) => !b.closest('.vc-popover'));
    const narrow = [], covered = [], leaking = [];
    for (const pop of pops) {
      pops.forEach((p) => { p.style.display = 'none'; });
      pop.style.display = 'block';
      const r = pop.getBoundingClientRect();
      if (r.width < panel.width - 24) narrow.push(Math.round(r.width));
      // The sheet must not cover the controls: with flex-wrap the bar becomes 2-3
      // rows and a `bottom` fixed in px buries the first row of buttons.
      const cob = barButtons.filter((b) => {
        const rr = b.getBoundingClientRect();
        return rr.top < r.bottom - 1 && rr.bottom > r.top + 1 && rr.left < r.right - 1 && rr.right > r.left + 1;
      });
      if (cob.length) covered.push(cob.length);
      if (r.top < panel.top - 0.5) leaking.push(Math.round(panel.top - r.top));
    }
    pops.forEach((p) => { p.style.display = 'none'; });
    // The touch target of the round bar buttons has to survive the fix.
    const toolbar = [...document.querySelectorAll('.vc-bottombar .vc-btn')]
      .filter((b) => !b.closest('.vc-popover'));
    const small = toolbar.filter((b) => { const r = b.getBoundingClientRect(); return r.width < 44 || r.height < 44; }).length;
    // The settings sheet has to be usable with a thumb: a sticky header with an X
    // in the touch target, and a 44px menu line.
    const wide = document.querySelector('.vc-popover-wide');
    wide.style.display = 'block';
    const head = wide.querySelector('.vc-sheet-head');
    const cs = head && getComputedStyle(head);
    const rx = head && head.querySelector('.vc-sheet-close').getBoundingClientRect();
    const sheet = {
      hasHeader: !!head && cs.display !== 'none',
      stuck: !!cs && cs.position === 'sticky',
      close: rx ? Math.min(Math.round(rx.width), Math.round(rx.height)) : 0,
    };
    const lines = [...wide.querySelectorAll('.vc-popover-item')].filter((el) => el.closest('.vc-popover') === wide);
    sheet.lines = lines.length;
    sheet.low = lines.filter((el) => el.getBoundingClientRect().height < 44).length;
    sheet.content = Math.round(wide.scrollHeight);
    wide.style.display = 'none';
    return { pops: out, toolbar: toolbar.length, small, narrow, covered, leaking, sheet,
             panel: Math.round(panel.width), bar: Math.round(bar.width),
             barHeight: Math.round(bar.height), lines: bar.height > 80 ? 2 : 1 };
  });

  const totOthers = r.pops.reduce((a, p) => a + p.others, 0);
  const totOthersClipped = r.pops.reduce((a, p) => a + p.othersClipped, 0);
  const totItems = r.pops.reduce((a, p) => a + p.items, 0);
  const totalClipped = r.pops.reduce((a, p) => a + p.clipped, 0);
  const totOver = r.pops.reduce((a, p) => a + p.overlapping, 0);
  const tag = larg + 'px';
  totItems > 20 ? ok(tag + ': ' + totItems + ' menu items measured across ' + r.pops.length + ' popovers')
                : no(tag + ': only ' + totItems + ' items — materialising the lists failed');
  totalClipped === 0 ? ok(tag + ': no item with text overflowing its own box')
                : no(tag + ': ' + totalClipped + ' item(s) with clipped text — e.g.: "' + (r.pops.find((p) => p.sample) || {}).sample + '"');
  totOver === 0 ? ok(tag + ': no item invading the one below')
                 : no(tag + ': ' + totOver + ' overlap(s) between menu items');
  totOthersClipped === 0 ? ok(tag + ': os ' + totOthers + ' other popover controls (pills, primaries, accordion) fit their own text')
                      : no(tag + ': ' + totOthersClipped + ' control(s) with squeezed text — e.g.: "' + (r.pops.find((p) => p.otherSample) || {}).otherSample + '"');
  r.narrow.length === 0
    ? ok(tag + ': as ' + r.pops.length + ' sheets fill the panel (' + r.panel + 'px), not the little bar (' + r.bar + 'px)')
    : no(tag + ': ' + r.narrow.length + ' sheet(s) pinned to the width of the little bar — ' + r.narrow.join('/') + 'px inside a panel of ' + r.panel + 'px');
  r.covered.length === 0
    ? ok(tag + ': no sheet covers the bar controls (bar of ' + r.barHeight + 'px, ' + r.lines + '+ rows)')
    : no(tag + ': ' + r.covered.length + ' sheet(s) covering controls — up to ' + Math.max(...r.covered) + ' button(s) buried under the sheet');
  r.leaking.length === 0
    ? ok(tag + ': no sheet overflows the top of the panel')
    : no(tag + ': ' + r.leaking.length + ' sheet(s) running past the top of the panel (up to ' + Math.max(...r.leaking) + 'px)');
  r.sheet.hasHeader && r.sheet.stuck
    ? ok(tag + ': the sheet has a sticky header (grab handle + title + X)')
    : no(tag + ': the sheet has no sticky header — without it you can only close it by hitting the edge');
  r.sheet.close >= 44
    ? ok(tag + ': the close X is in the touch target (' + r.sheet.close + 'px)')
    : no(tag + ': the close X at ' + r.sheet.close + 'px — below the 44px floor');
  r.sheet.low === 0
    ? ok(tag + ': as ' + r.sheet.lines + ' sheet rows are >=44px tall')
    : no(tag + ': ' + r.sheet.low + ' of ' + r.sheet.lines + ' rows below 44px');
  r.small === 0 ? ok(tag + ': os ' + r.toolbar + ' round bar buttons keep a touch target >=44px')
                   : no(tag + ': ' + r.small + ' bar button(s) below 44px — the fix until the touch target');
  await page.close();
}

// Counter-check in the source: the selector must not go back to being a descendant
// one. It searches the WHOLE file — bounding the @media block with a non-greedy
// regex stopped at the first `}` and the counter-check passed even with the old code.
/\.vc-bottombar\s+button\s*[.{]/.test(html)
  ? no('the sizing rule went back to using `.vc-bottombar button` (it catches the ~60 popover buttons)')
  : ok('no rule sizes `.vc-bottombar button` by descendancy');

await browser.close();
console.log('\n' + (fail ? '✗' : '✓') + ' ' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
