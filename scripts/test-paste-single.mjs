#!/usr/bin/env node
import { readFileSync, existsSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import { createRequire } from 'node:module';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js');
const src = readFileSync(target, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-paste-single ===');

const extract = (name, args) => {
  const re = new RegExp('^ {4}' + name + '\\(' + args.join(', ') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const m = src.match(re);
  if (!m) { no('could not extract ' + name + ' from the source'); process.exit(1); }
  return m[1];
};
const guardBody = extract('_pasteHandled', ['ev', 'files']);
const filesBody  = extract('_clipboardFiles', ['ev']);

{
  const captureOnContainer = /el\.addEventListener\('paste'[\s\S]{0,600}?_uploadPasteImage/.test(src);
  captureOnContainer
    ? no('a capture paste listener on the container calling _uploadPasteImage is back')
    : ok('the duplicated capture listener on the container is still gone');
}

const require_ = createRequire(join(root, '.tools', 'package.json'));
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

const result = await page.evaluate(({ guardBody, filesBody }) => {
  const app = {
    _pasteHandled: new Function('ev', 'files', guardBody),
    _clipboardFiles: new Function('ev', filesBody),
  };
  const self = app;
  const el = document.getElementById('el');
  const ta = el.querySelector('.xterm-helper-textarea');

  let uploads = [];
  const rise = (files) => uploads.push(...files.map((f) => f.name));

  ta.addEventListener('paste', (ev) => {
    const files = self._clipboardFiles(ev);
    if (!files) return;
    ev.preventDefault();
    ev.stopImmediatePropagation();
    if (self._pasteHandled(ev, files)) return;
    rise(files);
  }, true);
  el.addEventListener('paste', (ev) => {
    const files = self._clipboardFiles(ev);
    if (!files) return;
    ev.preventDefault();
    ev.stopPropagation();
    if (self._pasteHandled(ev, files)) return;
    rise(files);
  });

  const evPaste = (files, text) => {
    const dt = new DataTransfer();
    for (const [name, type] of files) dt.items.add(new File(['x'], name, { type: type }));
    if (text !== undefined) dt.setData('text/plain', text);
    return new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true });
  };

  const out = {};

  uploads = [];
  ta.dispatchEvent(evPaste([['shot.png', 'image/png']]));
  out.umPaste = uploads.slice();

  el.addEventListener('paste', (ev) => {
    const files = self._clipboardFiles(ev);
    if (!files) return;
    if (self._pasteHandled(ev, files)) return;
    rise(files);
  }, true);
  uploads = [];
  ta.dispatchEvent(evPaste([['intruder.png', 'image/png']]));
  out.withIntruder = uploads.slice();

  uploads = [];
  ta.dispatchEvent(evPaste([['image.png', 'image/png']], 'A1\tB1'));
  out.withText = uploads.slice();

  uploads = [];
  ta.dispatchEvent(evPaste([['dup.png', 'image/png']]));
  ta.dispatchEvent(evPaste([['dup.png', 'image/png']]));
  out.twoEvents = uploads.slice();

  uploads = [];
  ta.dispatchEvent(evPaste([['a.png', 'image/png']]));
  ta.dispatchEvent(evPaste([['b.png', 'image/png']]));
  out.twoDifferent = uploads.slice();

  uploads = [];
  ta.dispatchEvent(evPaste([['x.pdf', 'application/pdf'], ['y.csv', 'text/csv']]));
  out.multi = uploads.slice();

  return out;
}, { guardBody, filesBody });

const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);

eq(result.umPaste, ['shot.png'])
  ? ok('one pasted screenshot = one upload')
  : no('one pasted screenshot should upload 1 file, it uploaded: ' + JSON.stringify(result.umPaste));

eq(result.withIntruder, ['intruder.png'])
  ? ok('the bug topology (capture on the ancestor + capture on the descendant) yields a single upload')
  : no('the bug topology yielded ' + result.withIntruder.length + ' uploads: ' + JSON.stringify(result.withIntruder));

eq(result.withText, [])
  ? ok('a paste with text/plain (Excel/Word) stays text, no upload')
  : no('a paste with text uploaded a file: ' + JSON.stringify(result.withText));

eq(result.twoEvents, ['dup.png'])
  ? ok('two distinct events with the same content inside the window = one upload')
  : no('two events with the same content yielded: ' + JSON.stringify(result.twoEvents));

eq(result.twoDifferent, ['a.png', 'b.png'])
  ? ok('different files in sequence both go up (the guard is not blind)')
  : no('different files yielded: ' + JSON.stringify(result.twoDifferent));

eq(result.multi, ['x.pdf', 'y.csv'])
  ? ok('multi-file in a single paste uploads all of them')
  : no('multi-file yielded: ' + JSON.stringify(result.multi));

await browser.close();
console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
