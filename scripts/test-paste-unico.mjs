#!/usr/bin/env node
// test-paste-unico.mjs — one paste = one upload.
//
// The defect was in no function at all: it was in the TOPOLOGY of the listeners.
// There was a capture paste listener on the container and another capture one on
// the helper-textarea, which is its descendant. Both receive the SAME Event
// instance — capture walks down the tree, so the ancestor's runs first — and the
// descendant's stopImmediatePropagation arrives far too late to cancel it. Every
// screenshot pasted went up twice and injected two paths into the pane.
//
// That is why this pin runs in a BROWSER and dispatches a real ClipboardEvent:
// event propagation is DOM semantics, and evaluating the functions in isolation
// (the way the expression harnesses do) could never see the defect — which is
// exactly how it slipped through.
//
// Usage: node scripts/test-paste-unico.mjs
import { readFileSync, existsSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createRequire } from 'node:module';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const alvo = process.argv[2] || join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(alvo, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-paste-unico ===');

// ── Extraction from the real source ─────────────────────────────────────────
// Copying the logic in here would let the pin drift from the product unseen.
const extrai = (nome, args) => {
  const re = new RegExp('^ {4}' + nome + '\\(' + args.join(', ') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const m = src.match(re);
  if (!m) { no('could not extract ' + nome + ' from the source'); process.exit(1); }
  return m[1];
};
const corpoGuard = extrai('_pasteJaTratado', ['ev', 'arquivos']);
const corpoArqs  = extrai('_arquivosDoClipboard', ['ev']);

// ── 1. Structural pin: the removed listener must not come back ──────────────
// The container-capture listener that called _uploadPasteImage was the duplicate.
// If anyone reintroduces that pair (capture on the container + a direct upload),
// the guard saves it at runtime, but the intent of the code goes ambiguous again
// — so we lock it.
{
  const capturaNoContainer = /el\.addEventListener\('paste'[\s\S]{0,600}?_uploadPasteImage/.test(src);
  capturaNoContainer
    ? no('a capture paste listener on the container calling _uploadPasteImage is back')
    : ok('the duplicated capture listener on the container is still gone');
}

// ── Browser ─────────────────────────────────────────────────────────────────
const require_ = createRequire(join(raiz, '.tools', 'package.json'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch { console.error('FAILED: playwright-core missing from .tools/ — this pin needs a browser.'); process.exit(1); }

const cands = [];
const cache = '/root/.cache/ms-playwright';
if (existsSync(cache)) {
  for (const d of readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse()) {
    cands.push(join(cache, d, 'chrome-linux', 'chrome'));
  }
}
cands.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
const exe = cands.find((p) => existsSync(p));
if (!exe) { console.error('FAILED: chromium not found. `npx playwright install chromium`.'); process.exit(1); }

const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });
const page = await browser.newPage();
await page.setContent('<div id="el"><textarea class="xterm-helper-textarea"></textarea></div>');

const resultado = await page.evaluate(({ corpoGuard, corpoArqs }) => {
  // Rebuild the app around the TWO real methods from the product.
  const app = {
    _pasteJaTratado: new Function('ev', 'arquivos', corpoGuard),
    _arquivosDoClipboard: new Function('ev', corpoArqs),
  };
  const self = app;
  const el = document.getElementById('el');
  const ta = el.querySelector('.xterm-helper-textarea');

  let uploads = [];
  const sobe = (arquivos) => uploads.push(...arquivos.map((f) => f.name));

  // Wiring IDENTICAL to the product (00-shell.js): capture on the textarea (the
  // owner) and bubble on the container (the fallback), both through the guard.
  ta.addEventListener('paste', (ev) => {
    const arquivos = self._arquivosDoClipboard(ev);
    if (!arquivos) return;
    ev.preventDefault();
    ev.stopImmediatePropagation();
    if (self._pasteJaTratado(ev, arquivos)) return;
    sobe(arquivos);
  }, true);
  el.addEventListener('paste', (ev) => {
    const arquivos = self._arquivosDoClipboard(ev);
    if (!arquivos) return;
    ev.preventDefault();
    ev.stopPropagation();
    if (self._pasteJaTratado(ev, arquivos)) return;
    sobe(arquivos);
  });

  const evPaste = (arquivos, texto) => {
    const dt = new DataTransfer();
    for (const [nome, tipo] of arquivos) dt.items.add(new File(['x'], nome, { type: tipo }));
    if (texto !== undefined) dt.setData('text/plain', texto);
    return new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true });
  };

  const out = {};

  // (a) the user's case: a screenshot pasted with focus on the terminal.
  uploads = [];
  ta.dispatchEvent(evPaste([['shot.png', 'image/png']]));
  out.umPaste = uploads.slice();

  // (b) the TOPOLOGY of the defect: a third capture listener on the container,
  //     which receives the SAME Event instance before the textarea does. Without
  //     the guard that is two uploads — literally the reported bug.
  el.addEventListener('paste', (ev) => {
    const arquivos = self._arquivosDoClipboard(ev);
    if (!arquivos) return;
    if (self._pasteJaTratado(ev, arquivos)) return;
    sobe(arquivos);
  }, true);
  // A distinct name on purpose: reusing 'shot.png' here would land in the
  // signature window opened by case (a) and the pin would measure the wrong guard.
  uploads = [];
  ta.dispatchEvent(evPaste([['intruso.png', 'image/png']]));
  out.comIntruso = uploads.slice();

  // (c) text from Excel/Word: brings text/plain plus a rendered PNG. Must not upload.
  uploads = [];
  ta.dispatchEvent(evPaste([['image.png', 'image/png']], 'A1\tB1'));
  out.comTexto = uploads.slice();

  // (d) two distinct EVENTS with the same content inside the window — this is the
  //     'paste' + clipboard.read() pair from the code-server interceptor.
  uploads = [];
  ta.dispatchEvent(evPaste([['dup.png', 'image/png']]));
  ta.dispatchEvent(evPaste([['dup.png', 'image/png']]));
  out.doisEventos = uploads.slice();

  // (e) different files in sequence must not be swallowed by the guard.
  uploads = [];
  ta.dispatchEvent(evPaste([['a.png', 'image/png']]));
  ta.dispatchEvent(evPaste([['b.png', 'image/png']]));
  out.doisDiferentes = uploads.slice();

  // (f) multi-file in a single paste still uploads all of them.
  uploads = [];
  ta.dispatchEvent(evPaste([['x.pdf', 'application/pdf'], ['y.csv', 'text/csv']]));
  out.multi = uploads.slice();

  return out;
}, { corpoGuard, corpoArqs });

const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);

eq(resultado.umPaste, ['shot.png'])
  ? ok('one pasted screenshot = one upload')
  : no('one pasted screenshot should upload 1 file, it uploaded: ' + JSON.stringify(resultado.umPaste));

eq(resultado.comIntruso, ['intruso.png'])
  ? ok('the bug topology (capture on the ancestor + capture on the descendant) yields a single upload')
  : no('the bug topology yielded ' + resultado.comIntruso.length + ' uploads: ' + JSON.stringify(resultado.comIntruso));

eq(resultado.comTexto, [])
  ? ok('a paste with text/plain (Excel/Word) stays text, no upload')
  : no('a paste with text uploaded a file: ' + JSON.stringify(resultado.comTexto));

eq(resultado.doisEventos, ['dup.png'])
  ? ok('two distinct events with the same content inside the window = one upload')
  : no('two events with the same content yielded: ' + JSON.stringify(resultado.doisEventos));

eq(resultado.doisDiferentes, ['a.png', 'b.png'])
  ? ok('different files in sequence both go up (the guard is not blind)')
  : no('different files yielded: ' + JSON.stringify(resultado.doisDiferentes));

eq(resultado.multi, ['x.pdf', 'y.csv'])
  ? ok('multi-file in a single paste uploads all of them')
  : no('multi-file yielded: ' + JSON.stringify(resultado.multi));

await browser.close();
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
