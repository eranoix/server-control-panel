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

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin MEASURES geometry — skipping would be faking coverage.');
  console.error('       Instale com: make tools');
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

// Cut out the REAL bottombar (with the popovers inside it, which is the bug's root).
function recorta(marca) {
  const ini = html.indexOf(marca);
  if (ini < 0) return null;
  let n = 0;
  for (const m of html.slice(ini).matchAll(/<div\b|<\/div>/g)) {
    n += m[0] === '</div>' ? -1 : 1;
    if (n === 0) return html.slice(ini, ini + m.index + 6);
  }
  return null;
}
let bar = recorta('<div class="vc-bottombar"');
if (!bar) { no('vc-bottombar not found in index.html — the pin lost its target'); process.exit(1); }
ok('vc-bottombar cut out of index.html (' + bar.length + ' bytes)');

// Without Alpine, <template x-for> does not instantiate. Materialise each list
// with REAL device labels (the ones from the report: long, and therefore the ones
// that break).
const ROTULOS = ['Default - Headset (Sonos Ace)', 'Communications - Headphones (Sonos Ace)', 'Default - Digital Output (Unknown)'];
bar = bar.replace(/<template[^>]*x-for[^>]*>([\s\S]*?)<\/template>/g, (_, inner) =>
  ROTULOS.map((r) => inner.replace(/<span x-text="[^"]*"><\/span>/g, '<span>' + r + '</span>')).join(''));
bar = bar.replace(/x-show="[^"]*"/g, '').replace(/x-text="[^"]*"/g, '').replace(/x-if="[^"]*"/g, '');

const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
// 320 (the old iPhone SE) up to 700. 767.98px is the project's mobile boundary.
for (const larg of [320, 380, 430, 700]) {
  const page = await browser.newPage({ viewport: { width: larg, height: 780 } });
  await page.setContent('<style>' + tail + '</style><style>' + estilos + '</style>'
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
      const daCasa = (el) => el.closest('.vc-popover') === pop;
      const itens = [...pop.querySelectorAll('.vc-popover-item')].filter(daCasa);
      // Wide coverage: quality pills, primary buttons and accordion headers
      // suffered from the SAME selector and also had text squeezed.
      const outros = [...pop.querySelectorAll('button')].filter(daCasa)
        .filter((b) => !b.classList.contains('vc-popover-item') && b.textContent.trim().length > 2);
      let cortados = 0, sobrepostos = 0, exemplo = '';
      for (const it of itens) {
        if (it.scrollHeight > it.clientHeight + 1) { cortados++; if (!exemplo) exemplo = it.textContent.trim().slice(0, 34); }
      }
      for (let k = 1; k < itens.length; k++) {
        const a = itens[k - 1].getBoundingClientRect(), b = itens[k].getBoundingClientRect();
        if (b.top < a.bottom - 0.5) sobrepostos++;
      }
      let outrosCortados = 0, exemploOutro = '';
      for (const b of outros) {
        if (b.scrollHeight > b.clientHeight + 1 || b.scrollWidth > b.clientWidth + 1) {
          outrosCortados++; if (!exemploOutro) exemploOutro = b.textContent.trim().replace(/\s+/g, ' ').slice(0, 30);
        }
      }
      out.push({ i, itens: itens.length, cortados, sobrepostos, exemplo, outros: outros.length, outrosCortados, exemploOutro });
    }
    // The mobile sheet has to fill the PANEL. .vc-bottombar has a transform and a
    // backdrop-filter, so it becomes the containing block for its fixed
    // descendants: `left:8;right:8` resolved against the little bar and the sheet
    // was born its width. Without this measurement, the pin does not see the defect.
    const painel = document.getElementById('vc-call-root').getBoundingClientRect();
    const barraEl = document.querySelector('.vc-bottombar');
    const barra = barraEl.getBoundingClientRect();
    const botoesBarra = [...barraEl.querySelectorAll('.vc-btn')].filter((b) => !b.closest('.vc-popover'));
    const estreitas = [], tapados = [], vazando = [];
    for (const pop of pops) {
      pops.forEach((p) => { p.style.display = 'none'; });
      pop.style.display = 'block';
      const r = pop.getBoundingClientRect();
      if (r.width < painel.width - 24) estreitas.push(Math.round(r.width));
      // The sheet must not cover the controls: with flex-wrap the bar becomes 2-3
      // rows and a `bottom` fixed in px buries the first row of buttons.
      const cob = botoesBarra.filter((b) => {
        const rr = b.getBoundingClientRect();
        return rr.top < r.bottom - 1 && rr.bottom > r.top + 1 && rr.left < r.right - 1 && rr.right > r.left + 1;
      });
      if (cob.length) tapados.push(cob.length);
      if (r.top < painel.top - 0.5) vazando.push(Math.round(painel.top - r.top));
    }
    pops.forEach((p) => { p.style.display = 'none'; });
    // The touch target of the round bar buttons has to survive the fix.
    const toolbar = [...document.querySelectorAll('.vc-bottombar .vc-btn')]
      .filter((b) => !b.closest('.vc-popover'));
    const pequenos = toolbar.filter((b) => { const r = b.getBoundingClientRect(); return r.width < 44 || r.height < 44; }).length;
    // The settings sheet has to be usable with a thumb: a sticky header with an X
    // in the touch target, and a 44px menu line.
    const wide = document.querySelector('.vc-popover-wide');
    wide.style.display = 'block';
    const head = wide.querySelector('.vc-sheet-head');
    const cs = head && getComputedStyle(head);
    const rx = head && head.querySelector('.vc-sheet-close').getBoundingClientRect();
    const sheet = {
      temCabecalho: !!head && cs.display !== 'none',
      grudado: !!cs && cs.position === 'sticky',
      fechar: rx ? Math.min(Math.round(rx.width), Math.round(rx.height)) : 0,
    };
    const linhas = [...wide.querySelectorAll('.vc-popover-item')].filter((el) => el.closest('.vc-popover') === wide);
    sheet.linhas = linhas.length;
    sheet.baixas = linhas.filter((el) => el.getBoundingClientRect().height < 44).length;
    sheet.conteudo = Math.round(wide.scrollHeight);
    wide.style.display = 'none';
    return { pops: out, toolbar: toolbar.length, pequenos, estreitas, tapados, vazando, sheet,
             painel: Math.round(painel.width), barra: Math.round(barra.width),
             alturaBarra: Math.round(barra.height), linhas: barra.height > 80 ? 2 : 1 };
  });

  const totOutros = r.pops.reduce((a, p) => a + p.outros, 0);
  const totOutrosCort = r.pops.reduce((a, p) => a + p.outrosCortados, 0);
  const totItens = r.pops.reduce((a, p) => a + p.itens, 0);
  const totCort = r.pops.reduce((a, p) => a + p.cortados, 0);
  const totSobre = r.pops.reduce((a, p) => a + p.sobrepostos, 0);
  const tag = larg + 'px';
  totItens > 20 ? ok(tag + ': ' + totItens + ' menu items measured across ' + r.pops.length + ' popovers')
                : no(tag + ': only ' + totItens + ' items — materialising the lists failed');
  totCort === 0 ? ok(tag + ': no item with text overflowing its own box')
                : no(tag + ': ' + totCort + ' item(s) with clipped text — e.g.: "' + (r.pops.find((p) => p.exemplo) || {}).exemplo + '"');
  totSobre === 0 ? ok(tag + ': no item invading the one below')
                 : no(tag + ': ' + totSobre + ' overlap(s) between menu items');
  totOutrosCort === 0 ? ok(tag + ': os ' + totOutros + ' other popover controls (pills, primaries, accordion) fit their own text')
                      : no(tag + ': ' + totOutrosCort + ' control(s) with squeezed text — e.g.: "' + (r.pops.find((p) => p.exemploOutro) || {}).exemploOutro + '"');
  r.estreitas.length === 0
    ? ok(tag + ': as ' + r.pops.length + ' sheets fill the panel (' + r.painel + 'px), not the little bar (' + r.barra + 'px)')
    : no(tag + ': ' + r.estreitas.length + ' sheet(s) pinned to the width of the little bar — ' + r.estreitas.join('/') + 'px inside a panel of ' + r.painel + 'px');
  r.tapados.length === 0
    ? ok(tag + ': no sheet covers the bar controls (bar of ' + r.alturaBarra + 'px, ' + r.linhas + '+ rows)')
    : no(tag + ': ' + r.tapados.length + ' sheet(s) covering controls — up to ' + Math.max(...r.tapados) + ' button(s) buried under the sheet');
  r.vazando.length === 0
    ? ok(tag + ': no sheet overflows the top of the panel')
    : no(tag + ': ' + r.vazando.length + ' sheet(s) running past the top of the panel (up to ' + Math.max(...r.vazando) + 'px)');
  r.sheet.temCabecalho && r.sheet.grudado
    ? ok(tag + ': the sheet has a sticky header (grab handle + title + X)')
    : no(tag + ': the sheet has no sticky header — without it you can only close it by hitting the edge');
  r.sheet.fechar >= 44
    ? ok(tag + ': the close X is in the touch target (' + r.sheet.fechar + 'px)')
    : no(tag + ': the close X at ' + r.sheet.fechar + 'px — below the 44px floor');
  r.sheet.baixas === 0
    ? ok(tag + ': as ' + r.sheet.linhas + ' sheet rows are >=44px tall')
    : no(tag + ': ' + r.sheet.baixas + ' of ' + r.sheet.linhas + ' rows below 44px');
  r.pequenos === 0 ? ok(tag + ': os ' + r.toolbar + ' round bar buttons keep a touch target >=44px')
                   : no(tag + ': ' + r.pequenos + ' bar button(s) below 44px — the fix ate the touch target');
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
