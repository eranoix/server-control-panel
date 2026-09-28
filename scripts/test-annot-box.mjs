#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'panel', 'app', '00-shell.js');
const src = readFileSync(SRC, 'utf8');

const m = src.match(/vcAnnotContentBox\(video, hostRect\) \{([\s\S]*?)\n    \},/);
if (!m) { console.error('FATAL: vcAnnotContentBox not found in', SRC); process.exit(2); }
const realFn = new Function('video', 'hostRect', m[1]);

const mockVideo = (left, top, width, height, vw, vh) => ({
  getBoundingClientRect: () => ({ left, top, width, height, right: left + width, bottom: top + height }),
  videoWidth: vw, videoHeight: vh,
});
const approx = (a, b, eps = 0.01) => Math.abs(a - b) <= eps;
let pass = 0, fail = 0;
const check = (name, got, exp) => {
  const ok = got && approx(got.left, exp.left) && approx(got.top, exp.top) && approx(got.w, exp.w) && approx(got.h, exp.h);
  console.log((ok ? 'PASS ' : 'FAIL ') + name);
  if (ok) pass++; else { fail++; console.log('   got', JSON.stringify(got), '\n   exp', JSON.stringify(exp)); }
};
const checkNull = (name, got) => { const ok = got === null; console.log((ok ? 'PASS ' : 'FAIL ') + name); ok ? pass++ : (fail++, console.log('   got', JSON.stringify(got))); };

const host = { left: 80, top: 40 };

const v1 = mockVideo(100, 50, 400, 300, 1920, 1080);
check('16:9 in 4:3 tile → letterboxed', realFn(v1, host), { left: 20, top: 47.5, w: 400, h: 225 });

const pre = { top: 50 - 40, h: 300 };
const discr = !approx(pre.top, 47.5) || !approx(pre.h, 225);
console.log((discr ? 'PASS ' : 'FAIL ') + 'DISCRIMINANT: element-rect mapping ≠ content-box');
discr ? pass++ : fail++;

check('portrait in landscape tile → pillarboxed', realFn(mockVideo(100, 50, 400, 300, 1080, 1920), host), { left: 135.625, top: 10, w: 168.75, h: 300 });

check('matching AR → fills element rect', realFn(mockVideo(100, 50, 400, 300, 1600, 1200), host), { left: 20, top: 10, w: 400, h: 300 });

checkNull('collapsed rect → null', realFn(mockVideo(0, 0, 0, 0, 1920, 1080), host));
checkNull('no track metadata → null', realFn(mockVideo(100, 50, 400, 300, 0, 0), host));

const cb = realFn(v1, host);
const cr = { left: host.left + cb.left, top: host.top + cb.top, width: cb.w, height: cb.h };
const norm = (cx, cy) => ({ x: (cx - cr.left) / cr.width, y: (cy - cr.top) / cr.height });
const tl = norm(cr.left, cr.top), br = norm(cr.left + cr.width, cr.top + cr.height);
const ptOk = approx(tl.x, 0) && approx(tl.y, 0) && approx(br.x, 1) && approx(br.y, 1);
console.log((ptOk ? 'PASS ' : 'FAIL ') + 'pointer round-trip: corners → (0,0)/(1,1)');
ptOk ? pass++ : fail++;

console.log(`\n${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
