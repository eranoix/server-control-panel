#!/usr/bin/env node
// Keep-alive test for the code-server iframe (the VSCode tab).
//
// Node-pure, zero-dep (the repo convention — no playwright). It extracts the
// REAL latch from 00-shell.js (shipped bytes, via regex) and runs it against a
// controlled `this`, proving the mount-once semantics. It also asserts the
// wiring in index.html (the template keys off codeMounted; the section still
// hides via x-show, i.e. CSS-only). The framework behaviour (a latched Alpine
// x-if keeps the SAME node; an iframe under display:none does NOT unload) is
// the brPersistentMounted pattern already running in production — this test
// covers the only NEW logic (the latch plus the wiring).
//
//   run: node scripts/test-code-keepalive.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SHELL = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'vpsm', 'app', '00-shell.js');
const HTML = join(here, '..', 'internal', 'webassets', 'web', 'index.html');
const shell = readFileSync(SHELL, 'utf8');
const html = readFileSync(HTML, 'utf8');

let pass = 0, fail = 0;
const ok = (name, cond) => { console.log((cond ? 'PASS ' : 'FAIL ') + name); cond ? pass++ : fail++; };

// --- 1) extract the REAL latch and run it with a controlled this -------------
const m = shell.match(/if \(p==='code' && !this\.codeMounted\) \{[\s\S]*?\n      \}/);
ok('the codeMounted latch exists in 00-shell.js', !!m);
if (m) {
  const runLatch = new Function('p', m[0]); // usa this.codeMounted + this.$nextTick
  const queue = [];
  const ctx = { codeMounted: false, $nextTick: (fn) => queue.push(fn) };
  const flush = () => { while (queue.length) queue.shift()(); };

  runLatch.call(ctx, 'other');
  ok('view != code: nothing scheduled (the iframe does not lazy-mount)', queue.length === 0 && ctx.codeMounted === false);

  runLatch.call(ctx, 'code');
  ok('1st visit to code: schedules 1 latch (lazy mount on the first visit)', queue.length === 1);
  flush();
  ok('after $nextTick: codeMounted = true', ctx.codeMounted === true);

  runLatch.call(ctx, 'other');
  flush();
  ok('leaving the tab does NOT reset codeMounted (keep-alive)', ctx.codeMounted === true);

  runLatch.call(ctx, 'code');
  ok('revisiting code: the !codeMounted guard blocks a re-schedule (no remount)', queue.length === 0);
}

// --- 2) reactive declaration of the latch ------------------------------------
ok('codeMounted declared in x-data', /\n\s*codeMounted:\s*false\s*,/.test(shell));

// --- 3) wiring in index.html -------------------------------------------------
// the editor iframe now mounts off codeMounted (keep-alive), not currentView
const frameBlock = html.match(/<template x-if="[^"]+">\s*<iframe id="vscode-frame"/);
ok('the vscode-frame template uses x-if="codeMounted"', /<template x-if="codeMounted">\s*<iframe id="vscode-frame"/.test(html));
ok('NO longer uses x-if="currentView===\'code\'" on the iframe (that forced a reload)',
   !!frameBlock && !/currentView==='code'">\s*<iframe id="vscode-frame"/.test(html));
// the section still controls VISIBILITY via x-show (CSS display:none, no unmount)
ok('the VSCode section hides via x-show (CSS-only, keeps the iframe alive)',
   /<section x-show="currentView==='code'"/.test(html));

// --- summary -----------------------------------------------------------------
console.log(`\n== code-server keep-alive: ${pass}/${pass + fail} ==`);
process.exit(fail === 0 ? 0 : 1);
