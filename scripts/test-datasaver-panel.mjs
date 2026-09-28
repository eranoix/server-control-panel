import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const index = read('internal/webassets/web/index.html');
const shell = read('internal/webassets/web/vendor/panel/app/00-shell.js');

let bad = 0;
const ok = (name, cond, extra = '') => {
  console.log((cond ? '  ✓ ' : '  ✗ ') + name + (extra ? '  → ' + extra : ''));
  if (!cond) bad++;
};

function block(src, mark, open = '{') {
  const i = src.indexOf(mark);
  if (i < 0) return null;
  let j = src.indexOf(open, i + mark.length - 1);
  if (j < 0) return null;
  let prof = 0;
  for (let k = j; k < src.length; k++) {
    const c = src[k];
    if (c === '{') prof++;
    else if (c === '}') { prof--; if (prof === 0) return src.slice(j, k + 1); }
  }
  return null;
}

const txtState = block(shell, 'dsStatus: {');
ok('initial dsStatus found in 00-shell.js', !!txtState);
const initialState = txtState ? new Function('return (' + txtState + ')')() : null;

const shapeBody = block(shell, '_dsShape(s){');
ok('_dsShape found in 00-shell.js', !!shapeBody);
const _dsShape = shapeBody ? new Function('s', shapeBody.slice(1, -1)) : null;

const exprs = [];
for (const m of index.matchAll(/x-(text|if|show)="([^"]*dsStatus[^"]*)"/g)) {
  exprs.push({ type: m[1], expr: m[2] });
}
ok('expressions touching dsStatus extracted from index.html', exprs.length >= 4, exprs.length + ' found');
ok('the four statistic cards are among them',
   exprs.filter((e) => e.type === 'text').length >= 4,
   exprs.filter((e) => e.type === 'text').length + ' x-text');

const scope = {
  fmtBytes: (n) => {
    if (typeof n !== 'number' || !isFinite(n)) throw new Error('fmtBytes received ' + n);
    return String(n);
  },
  dsLoaded: true, dsLoading: false, dsError: '',
};

function evaluate(expr, dsStatus) {
  const names = Object.keys(scope).concat(['dsStatus']);
  const vals = Object.values(scope).concat([dsStatus]);
  return new Function(...names, 'return (' + expr + ')')(...vals);
}

const scenarios = [
  ['initial state (before the 1st load)', initialState],
  ['complete response',      _dsShape && _dsShape({settings:{enabled:true}, bypass:[], has_ca:true,
                               saved:{orig:1000, out:220, imgs:37, reqs_cut:12, pct:78}})],
  ['empty response {}',      _dsShape && _dsShape({})],
  ['null response',          _dsShape && _dsShape(null)],
  ['saved missing',          _dsShape && _dsShape({settings:{}, bypass:[]})],
  ['saved partial (pct only)', _dsShape && _dsShape({saved:{pct: 50}})],
  ['saved full of junk',         _dsShape && _dsShape({saved:{imgs:'x', orig:null, out:undefined, pct:NaN}})],
];

for (const [name, st] of scenarios) {
  if (!st) { ok('scenario ' + name, false, 'state was not built'); continue; }
  let err = null, bad = null;
  for (const { type: type, expr } of exprs) {
    try {
      const v = evaluate(expr, st);
      if (type !== 'text') continue;
      const txt = String(v);
      if (txt.includes('undefined') || txt.includes('NaN')) { bad = expr + ' -> "' + txt + '"'; break; }
    } catch (ex) { err = expr + ' -> ' + ex.constructor.name + ': ' + ex.message; break; }
  }
  ok('survives: ' + name, !err && !bad, err || bad || '');
}

const savedFields = ['orig', 'out', 'imgs', 'reqs_cut', 'pct'];
ok('the initial state declares saved COMPLETE (not a {} that pretends to exist)',
   !!initialState && savedFields.every((c) => typeof initialState.saved?.[c] === 'number'),
   initialState ? JSON.stringify(initialState.saved) : '');

const antipadrao = /\(\s*dsStatus\.saved\s*\?\s*dsStatus\.saved\.\w+\s*:/;
ok('the index does not guard the container to dereference the field', !antipadrao.test(index));

console.log(bad ? `\nFAIL — ${bad} case(s)` : '\nPASS — the Data saver panel survives the initial state and a partial response');
process.exit(bad ? 1 : 0);
