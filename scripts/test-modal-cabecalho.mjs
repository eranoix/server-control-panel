#!/usr/bin/env node
// test-modal-cabecalho.mjs — a modal title has to be legible in BOTH themes,
// and the danger colour has to survive.
//
// `.fm-modal-head h3` pinned color:#fff. In the light theme that is white on
// white: the title of EVERY modal (files, confirmation, video call) simply
// vanished. And `.fm-modal-head h3` (0,1,1) beats `.text-red-400` (0,1,0), so
// the title of a destructive confirmation was never red — the danger colour had
// been dead from the start.
//
// The pin does not read CSS: it mounts each REAL header from index.html in a
// chromium and measures the computed contrast.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin MEASURES computed colour — skipping it would be faking coverage.');
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

// Every modal header in the file, with the title it actually uses.
// x-text does not change colour, so the bindings may stay inert — what matters
// is the CSS cascade over the real markup.
const cabecalhos = [];
const re = /<div class="fm-modal-head">([\s\S]*?)<\/div>/g;
let m;
while ((m = re.exec(html)) !== null) {
  const linha = html.slice(0, m.index).split('\n').length;
  cabecalhos.push({ linha, html: m[0].replace(/<h3([^>]*)>\s*<\/h3>/, '<h3$1>Title</h3>') });
}
if (cabecalhos.length < 10) { no('only ' + cabecalhos.length + ' headers found — did the regex stop matching?'); }
else ok(cabecalhos.length + ' modal headers found in index.html');

const lum = (c) => { const v = c.match(/\d+/g).map(Number).map((x) => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
                     return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2]; };
const razao = (a, b) => { const x = [lum(a), lum(b)].sort((p, q) => q - p); return (x[0] + 0.05) / (x[1] + 0.05); };

const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage({ viewport: { width: 900, height: 600 } });

for (const tema of ['dark', 'light']) {
  const corpo = cabecalhos.map((c, i) => '<div class="fm-modal" data-i="' + i + '">' + c.html + '</div>').join('');
  await page.setContent('<style>' + tail + '</style><style>' + estilos + '</style><body style="margin:0">'
    + corpo
    + '<div class="fm-modal" id="perigo"><div class="fm-modal-head"><h3 class="is-danger">Delete everything?</h3>'
    + '<button class="fm-modal-close">×</button></div></div></body>');
  await page.evaluate((t) => { if (t === 'light') document.documentElement.setAttribute('data-theme', 'light'); }, tema);
  await page.waitForTimeout(150);
  const r = await page.evaluate(() => {
    const out = [];
    document.querySelectorAll('.fm-modal[data-i]').forEach((mod) => {
      const h = mod.querySelector('h3');
      if (!h) return;
      out.push({ i: mod.dataset.i, cor: getComputedStyle(h).color, fundo: getComputedStyle(mod).backgroundColor });
    });
    const pg = document.querySelector('#perigo h3');
    const btn = document.querySelector('#perigo .fm-modal-close');
    return { titulos: out, perigo: getComputedStyle(pg).color,
             normal: getComputedStyle(document.querySelector('.fm-modal[data-i] h3')).color,
             fundoPerigo: getComputedStyle(document.querySelector('#perigo')).backgroundColor,
             btnCor: getComputedStyle(btn).color };
  });
  const ruins = r.titulos.map((t, k) => ({ k, ln: cabecalhos[t.i] ? cabecalhos[t.i].linha : '?', cr: razao(t.cor, t.fundo) }))
                         .filter((x) => x.cr < 4.5);
  ruins.length === 0
    ? ok('theme ' + tema + ': all ' + r.titulos.length + ' titles pass AA (worst = ' +
         Math.min(...r.titulos.map((t) => razao(t.cor, t.fundo))).toFixed(1) + ':1)')
    : no('theme ' + tema + ': ' + ruins.length + ' title(s) fail AA — lines ' + ruins.map((x) => x.ln).join(', '));

  const crP = razao(r.perigo, r.fundoPerigo);
  (r.perigo !== r.normal) ? ok('theme ' + tema + ': the danger title differs from the normal one (' + r.perigo + ')')
                          : no('theme ' + tema + ': the danger title is the SAME as the normal one — the danger colour is dead again');
  crP >= 4.5 ? ok('theme ' + tema + ': the danger title passes AA (' + crP.toFixed(1) + ':1)')
             : no('theme ' + tema + ': the danger title at ' + crP.toFixed(2) + ':1');
}
await page.close();

// Counter-check in the source: if anyone pins the colour back, the pin falls.
const regra = (html.match(/\.fm-modal-head h3 \{[^}]*\}/) || [''])[0];
/var\(--text-primary\)/.test(regra) ? ok('the rule uses a theme token')
                                    : no('the title rule went back to pinning a colour: ' + regra);
/#fff/i.test((html.match(/\.fm-modal-close:hover \{[^}]*\}/) || [''])[0])
  ? no('the X hover went back to pinning #fff (invisible in the light theme)')
  : ok('the X hover uses a theme token');

await browser.close();
console.log('\n' + (fail ? '✗' : '✓') + ' ' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
