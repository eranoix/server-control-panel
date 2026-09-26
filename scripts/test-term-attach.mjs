#!/usr/bin/env node
// test-term-attach.mjs — attaching ANY file to the terminal.
//
// The risks this test exists to pin down (all of them already cost dearly):
//
//  1. REGRESSION IN THE TEXT PASTE. Copying a cell from Excel/Word puts the text
//     in the clipboard AND a kind:'file' item with the rendered PNG. If the
//     handler takes "any file", pasting text becomes a screenshot upload —
//     breaking the most common case of all to enable the rarest one.
//  2. DESTRUCTION OF THE SPA. A dragover that does not call preventDefault is
//     not a valid drop target; the drop leaks to the document and the browser
//     NAVIGATES to the file, killing every pane and the state of the tab.
//  3. HOLE IN THE OUTBOX. If the attachment writes straight to the ws instead
//     of _paneSendInput, dropping a file with the connection down loses the
//     path silently — exactly what was fixed for typing.
//  4. PATH WITH A SPACE. Injected raw, it becomes two arguments in the shell.
//
// It extracts the REAL methods from 00-shell.js and exercises them — no
// reimplementing the logic here, or the test passes with the bug standing (which
// already happened: the test asserted the wrong value and gave false confidence).
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const alvo = process.argv[2] || join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(alvo, 'utf8');
let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };
console.log('=== test-term-attach ===');

// Cuts a method out of the object-literal of the app: from "    name(" to the
// "    }," that closes it at the same indentation.
function metodo(nome) {
  const re = new RegExp('\\n    (?:async )?' + nome + '\\(([\\s\\S]*?)\\n    \\},');
  const m = src.match(re);
  if (!m) { no('could not find the method ' + nome + ' in the source'); process.exit(1); }
  return '    ' + (m[0].match(/\n    ((?:async )?[\s\S]*)\n    \},/))[1] + '\n    },';
}

// Assembles an object with the requested methods + stubs, to really call them.
function app(nomes, extra = {}) {
  const corpo = nomes.map(metodo).join('\n');
  const fabrica = new Function('extra', 'document', 'return Object.assign({' + corpo + '\n}, extra);');
  const doc = {
    createElement: () => ({ style:{}, classList:{}, dataset:{}, appendChild(){}, querySelectorAll:()=>[], set textContent(v){}, click(){} }),
    querySelectorAll: () => [],
    body: { appendChild(){} },
    getElementById: () => null,
  };
  return fabrica(extra, doc);
}

const dt = (types, files = []) => ({ types, files, dropEffect: '' });
const evento = (dataTransfer, extra = {}) => {
  let prevented = false;
  return {
    dataTransfer, clipboardData: dataTransfer,
    get defaultPrevented(){ return prevented; },
    preventDefault(){ prevented = true; },
    stopPropagation(){}, stopImmediatePropagation(){},
    currentTarget: { querySelector: () => null, querySelectorAll: () => [], appendChild(){}, contains: () => false, getBoundingClientRect: () => ({left:0,top:0,width:100,height:100}) },
    _foiPrevenido: () => prevented,
    ...extra,
  };
};
const arquivo = (name, type) => ({ name, type, kind: 'file', getAsFile(){ return this; } });

// ── 1. _arquivosDoClipboard: the rule that protects the text paste ─────────
{
  const a = app(['_arquivosDoClipboard']);
  // Excel/Word/image-in-page: text + PNG together → it has to paste TEXT.
  const excel = evento({ types: ['text/plain', 'text/html', 'Files'], items: [arquivo('image.png','image/png')], files: [] });
  a._arquivosDoClipboard(excel) === null
    ? ok('clipboard with text/plain + file → null (pastes the TEXT, does not upload the PNG)')
    : no('REGRESSION: pasting text from Excel would become an image upload');

  // A file copied in the file manager of the system: no text/plain.
  const f = arquivo('contract.pdf', 'application/pdf');
  const so = evento({ types: ['Files'], items: [f], files: [f] });
  const r = a._arquivosDoClipboard(so);
  Array.isArray(r) && r.length === 1 && r[0].name === 'contract.pdf'
    ? ok('clipboard with only a file → returns the file (PDF, not only images)')
    : no('a non-image file was not recognised in the clipboard');

  a._arquivosDoClipboard(evento({ types: ['text/plain'], items: [], files: [] })) === null
    ? ok('clipboard with only text → null')
    : no('a pure-text clipboard was treated as a file');

  a._arquivosDoClipboard({ clipboardData: null }) === null
    ? ok('event with no clipboardData → null (does not blow up)')
    : no('event with no clipboardData did not return null');
}

// ── 2. _dragTemArquivos ────────────────────────────────────────────────────
{
  const a = app(['_dragTemArquivos']);
  a._dragTemArquivos(evento(dt(['Files']))) === true
    ? ok('drag with Files → recognised as a file')
    : no('a file drag was not recognised');
  a._dragTemArquivos(evento(dt(['text/plain']))) === false
    ? ok('pane drag (text/plain) → NOT a file (rearranging keeps working)')
    : no('a pane drag was mistaken for a file — that would break split by drag');
}

// ── 3. _panelDragOver: the preventDefault that stops the browser navigating 
{
  let overlay = 0;
  const a = app(['_panelDragOver', '_dragTemArquivos'], {
    terms: { _dragPane: null },
    _showFileDropOverlay(){ overlay++; },
    _showDropOverlay(){ no('a FILE drag fell into the pane rearrange path'); },
  });
  const ev = evento(dt(['Files']));
  a._panelDragOver(ev, { id: 'p1' });
  ev._foiPrevenido()
    ? ok('dragover with a file → preventDefault (without it the drop navigates and kills the SPA)')
    : no('CRITICAL: file dragover with no preventDefault — the browser would navigate to the file');
  overlay === 1 ? ok('dragover with a file → shows the attachment overlay') : no('the attachment overlay did not appear');
  ev.dataTransfer.dropEffect === 'copy' ? ok('dropEffect = copy (the cursor says "will attach")') : no('dropEffect did not become copy');
}
{
  // No pane being dragged and no file: keeps the original early-return.
  const a = app(['_panelDragOver', '_dragTemArquivos'], {
    terms: { _dragPane: null }, _showFileDropOverlay(){}, _showDropOverlay(){},
  });
  const ev = evento(dt(['text/plain']));
  a._panelDragOver(ev, { id: 'p1' });
  !ev._foiPrevenido()
    ? ok('dragover with no file and no dragged pane → does not interfere')
    : no('dragover started interfering with an unrelated drag');
}

// ── 4. _panelDrop: uploads the files into the right pane ───────────────────
{
  let recebido = null;
  const f = arquivo('spec.pdf', 'application/pdf');
  const a = app(['_panelDrop', '_dragTemArquivos'], {
    terms: { _dragPane: null },
    _hideFileDropOverlay(){},
    _sendFilesToPane(state, files){ recebido = { state, files }; return Promise.resolve(); },
    _endPaneDrag(){ no('a file drop ran the pane rearrange flow'); },
  });
  const alvoPane = { id: 'p9', ws: {} };
  const ev = evento(dt(['Files'], [f]));
  a._panelDrop(ev, alvoPane);
  ev._foiPrevenido() ? ok('file drop → preventDefault') : no('the drop did not prevent the default');
  recebido && recebido.state === alvoPane
    ? ok('the drop routes to the PANE where the file was dropped')
    : no('the drop did not route to the target pane (wrong state = attachment in the wrong pane)');
  recebido && recebido.files.length === 1 && recebido.files[0].name === 'spec.pdf'
    ? ok('the drop passes the dropped file along')
    : no('the dropped file never reached the upload');
}

// ── 5. _quoteShellPath ─────────────────────────────────────────────────────
{
  const a = app(['_quoteShellPath']);
  a._quoteShellPath('/data/users/sam/uploads/1-spec.pdf') === '/data/users/sam/uploads/1-spec.pdf'
    ? ok('plain path → no quotes (it does not pollute the line)')
    : no('a plain path was quoted for no reason');
  a._quoteShellPath('/tmp/my file.pdf') === "'/tmp/my file.pdf'"
    ? ok('path with a space → quoted (otherwise it becomes two shell arguments)')
    : no('a path with a space was not quoted');
  a._quoteShellPath("/tmp/o'brien.txt") === "'/tmp/o'\\''brien.txt'"
    ? ok("path with a single quote → escaped correctly")
    : no('single-quote escaping is wrong — it would break the command line');
  a._quoteShellPath('') === '' ? ok('empty path → empty string') : no('empty path not handled');
}

// ── 6. _sendFilesToPane: uses the OUTBOX, not the ws directly ──────────────
{
  let enviado = null, subiu = [];
  const a = app(['_sendFilesToPane', '_quoteShellPath'], {
    _uploadTermFile(f){ subiu.push(f.name); return Promise.resolve({ path: '/up/' + f.name }); },
    _paneSendInput(pane, d){ enviado = { pane, d }; return true; },
    showToast(){},
  });
  const pane = { id: 'p1' };
  await a._sendFilesToPane(pane, [arquivo('a.pdf','application/pdf'), arquivo('b.csv','text/csv')]);
  subiu.join(',') === 'a.pdf,b.csv' ? ok('uploads every dropped file') : no('not every file was uploaded');
  enviado && enviado.pane === pane
    ? ok('injects through _paneSendInput (the outbox — nothing is lost in an outage)')
    : no('CRITICAL: the attachment skipped the outbox; with the connection down the path would vanish');
  enviado && enviado.d === '/up/a.pdf /up/b.csv '
    ? ok('injects the paths on a single line, with a trailing space')
    : no('the injection format changed: ' + JSON.stringify(enviado && enviado.d));
}
{
  // A failed upload must not inject a broken path nor an empty string.
  let enviado = 0;
  const a = app(['_sendFilesToPane', '_quoteShellPath'], {
    _uploadTermFile(){ return Promise.resolve(null); },
    _paneSendInput(){ enviado++; return true; },
    showToast(){},
  });
  await a._sendFilesToPane({ id:'p1' }, [arquivo('x.bin','application/octet-stream')]);
  enviado === 0 ? ok('upload failed → nothing is injected into the terminal') : no('it injected garbage after a failed upload');
}

// ── 7. _uploadTermFile: the contract with the server ───────────────────────
{
  let req = null;
  const campos = [];
  globalThis.FormData = class { append(k, v, n){ campos.push([k, v, n]); } };
  globalThis.fetch = (url, opts) => { req = { url, opts }; return Promise.resolve({ ok: true, json: async () => ({ path: '/up/x' }) }); };
  const a = app(['_uploadTermFile'], { token: 'T', showToast(){} });
  await a._uploadTermFile({ name: 'final contract.pdf', type: 'application/pdf' });
  req && req.url === '/api/terminal/upload'
    ? ok('posts to /api/terminal/upload')
    : no('the upload URL changed: ' + (req && req.url));
  campos.length === 1 && campos[0][0] === 'file'
    ? ok("the multipart field is 'file'")
    : no('the multipart field is not file: ' + JSON.stringify(campos.map(c=>c[0])));
  campos[0] && campos[0][2] === 'final contract.pdf'
    ? ok('preserves the original file name (it is what orients whoever reads the path)')
    : no('original name lost in the upload');
}

// ── 8. global guard: it acts only on what nobody handled ───────────────────
{
  const src2 = readFileSync(alvo, 'utf8');
  /_installGlobalDropGuard\(\)\{[\s\S]*?if \(ev\.defaultPrevented\) return;/.test(src2)
    ? ok('the global guard respects whoever already called preventDefault (legitimate zones stay in charge)')
    : no('the global guard does not check defaultPrevented — it would run over existing drop zones');
  /window\.addEventListener\('drop'/.test(src2)
    ? ok('the global guard is registered on window for the drop')
    : no('the global guard does not register the drop');
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
