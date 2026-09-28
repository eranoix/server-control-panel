#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js');
const src = readFileSync(target, 'utf8');
let pass = 0, fail = 0;
const ok = (m) => { console.log('  ✓ ' + m); pass++; };
const no = (m) => { console.log('  ✗ ' + m); fail++; };
console.log('=== test-term-attach ===');

function method(name) {
  const re = new RegExp('\\n    (?:async )?' + name + '\\(([\\s\\S]*?)\\n    \\},');
  const m = src.match(re);
  if (!m) { no('could not find the method ' + name + ' in the source'); process.exit(1); }
  return '    ' + (m[0].match(/\n    ((?:async )?[\s\S]*)\n    \},/))[1] + '\n    },';
}

function app(names, extra = {}) {
  const body = names.map(method).join('\n');
  const factory = new Function('extra', 'document', 'return Object.assign({' + body + '\n}, extra);');
  const doc = {
    createElement: () => ({ style:{}, classList:{}, dataset:{}, appendChild(){}, querySelectorAll:()=>[], set textContent(v){}, click(){} }),
    querySelectorAll: () => [],
    body: { appendChild(){} },
    getElementById: () => null,
  };
  return factory(extra, doc);
}

const dt = (types, files = []) => ({ types, files, dropEffect: '' });
const event = (dataTransfer, extra = {}) => {
  let prevented = false;
  return {
    dataTransfer, clipboardData: dataTransfer,
    get defaultPrevented(){ return prevented; },
    preventDefault(){ prevented = true; },
    stopPropagation(){}, stopImmediatePropagation(){},
    currentTarget: { querySelector: () => null, querySelectorAll: () => [], appendChild(){}, contains: () => false, getBoundingClientRect: () => ({left:0,top:0,width:100,height:100}) },
    _wasPrevented: () => prevented,
    ...extra,
  };
};
const file = (name, type) => ({ name, type, kind: 'file', getAsFile(){ return this; } });

{
  const a = app(['_clipboardFiles']);
  const excel = event({ types: ['text/plain', 'text/html', 'Files'], items: [file('image.png','image/png')], files: [] });
  a._clipboardFiles(excel) === null
    ? ok('clipboard with text/plain + file → null (pastes the TEXT, does not upload the PNG)')
    : no('REGRESSION: pasting text from Excel would become an image upload');

  const f = file('contract.pdf', 'application/pdf');
  const so = event({ types: ['Files'], items: [f], files: [f] });
  const r = a._clipboardFiles(so);
  Array.isArray(r) && r.length === 1 && r[0].name === 'contract.pdf'
    ? ok('clipboard with only a file → returns the file (PDF, not only images)')
    : no('a non-image file was not recognised in the clipboard');

  a._clipboardFiles(event({ types: ['text/plain'], items: [], files: [] })) === null
    ? ok('clipboard with only text → null')
    : no('a pure-text clipboard was treated as a file');

  a._clipboardFiles({ clipboardData: null }) === null
    ? ok('event with no clipboardData → null (does not blow up)')
    : no('event with no clipboardData did not return null');
}

{
  const a = app(['_dragHasFiles']);
  a._dragHasFiles(event(dt(['Files']))) === true
    ? ok('drag with Files → recognised as a file')
    : no('a file drag was not recognised');
  a._dragHasFiles(event(dt(['text/plain']))) === false
    ? ok('pane drag (text/plain) → NOT a file (rearranging keeps working)')
    : no('a pane drag was mistaken for a file — that would break split by drag');
}

{
  let overlay = 0;
  const a = app(['_panelDragOver', '_dragHasFiles'], {
    terms: { _dragPane: null },
    _showFileDropOverlay(){ overlay++; },
    _showDropOverlay(){ no('a FILE drag fell into the pane rearrange path'); },
  });
  const ev = event(dt(['Files']));
  a._panelDragOver(ev, { id: 'p1' });
  ev._wasPrevented()
    ? ok('dragover with a file → preventDefault (without it the drop navigates and kills the SPA)')
    : no('CRITICAL: file dragover with no preventDefault — the browser would navigate to the file');
  overlay === 1 ? ok('dragover with a file → shows the attachment overlay') : no('the attachment overlay did not appear');
  ev.dataTransfer.dropEffect === 'copy' ? ok('dropEffect = copy (the cursor says "will attach")') : no('dropEffect did not become copy');
}
{
  const a = app(['_panelDragOver', '_dragHasFiles'], {
    terms: { _dragPane: null }, _showFileDropOverlay(){}, _showDropOverlay(){},
  });
  const ev = event(dt(['text/plain']));
  a._panelDragOver(ev, { id: 'p1' });
  !ev._wasPrevented()
    ? ok('dragover with no file and no dragged pane → does not interfere')
    : no('dragover started interfering with an unrelated drag');
}

{
  let received = null;
  const f = file('spec.pdf', 'application/pdf');
  const a = app(['_panelDrop', '_dragHasFiles'], {
    terms: { _dragPane: null },
    _hideFileDropOverlay(){},
    _sendFilesToPane(state, files){ received = { state, files }; return Promise.resolve(); },
    _endPaneDrag(){ no('a file drop ran the pane rearrange flow'); },
  });
  const targetPane = { id: 'p9', ws: {} };
  const ev = event(dt(['Files'], [f]));
  a._panelDrop(ev, targetPane);
  ev._wasPrevented() ? ok('file drop → preventDefault') : no('the drop did not prevent the default');
  received && received.state === targetPane
    ? ok('the drop routes to the PANE where the file was dropped')
    : no('the drop did not route to the target pane (wrong state = attachment in the wrong pane)');
  received && received.files.length === 1 && received.files[0].name === 'spec.pdf'
    ? ok('the drop passes the dropped file along')
    : no('the dropped file never reached the upload');
}

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

{
  let sent = null, uploaded = [];
  const a = app(['_sendFilesToPane', '_quoteShellPath'], {
    _uploadTermFile(f){ uploaded.push(f.name); return Promise.resolve({ path: '/up/' + f.name }); },
    _paneSendInput(pane, d){ sent = { pane, d }; return true; },
    showToast(){},
  });
  const pane = { id: 'p1' };
  await a._sendFilesToPane(pane, [file('a.pdf','application/pdf'), file('b.csv','text/csv')]);
  uploaded.join(',') === 'a.pdf,b.csv' ? ok('uploads every dropped file') : no('not every file was uploaded');
  sent && sent.pane === pane
    ? ok('injects through _paneSendInput (the outbox — nothing is lost in an outage)')
    : no('CRITICAL: the attachment skipped the outbox; with the connection down the path would vanish');
  sent && sent.d === '/up/a.pdf /up/b.csv '
    ? ok('injects the paths on a single line, with a trailing space')
    : no('the injection format changed: ' + JSON.stringify(sent && sent.d));
}
{
  let sent = 0;
  const a = app(['_sendFilesToPane', '_quoteShellPath'], {
    _uploadTermFile(){ return Promise.resolve(null); },
    _paneSendInput(){ sent++; return true; },
    showToast(){},
  });
  await a._sendFilesToPane({ id:'p1' }, [file('x.bin','application/octet-stream')]);
  sent === 0 ? ok('upload failed → nothing is injected into the terminal') : no('it injected garbage after a failed upload');
}

{
  let req = null;
  const fields = [];
  globalThis.FormData = class { append(k, v, n){ fields.push([k, v, n]); } };
  globalThis.fetch = (url, opts) => { req = { url, opts }; return Promise.resolve({ ok: true, json: async () => ({ path: '/up/x' }) }); };
  const a = app(['_uploadTermFile'], { token: 'T', showToast(){} });
  await a._uploadTermFile({ name: 'final contract.pdf', type: 'application/pdf' });
  req && req.url === '/api/terminal/upload'
    ? ok('posts to /api/terminal/upload')
    : no('the upload URL changed: ' + (req && req.url));
  fields.length === 1 && fields[0][0] === 'file'
    ? ok("the multipart field is 'file'")
    : no('the multipart field is not file: ' + JSON.stringify(fields.map(c=>c[0])));
  fields[0] && fields[0][2] === 'final contract.pdf'
    ? ok('preserves the original file name (it is what orients whoever reads the path)')
    : no('original name lost in the upload');
}

{
  const src2 = readFileSync(target, 'utf8');
  /_installGlobalDropGuard\(\)\{[\s\S]*?if \(ev\.defaultPrevented\) return;/.test(src2)
    ? ok('the global guard respects whoever already called preventDefault (legitimate zones stay in charge)')
    : no('the global guard does not check defaultPrevented — it would run over existing drop zones');
  /window\.addEventListener\('drop'/.test(src2)
    ? ok('the global guard is registered on window for the drop')
    : no('the global guard does not register the drop');
}

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
