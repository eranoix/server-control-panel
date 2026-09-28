#!/usr/bin/env node
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const APP = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'panel', 'app');
const src = readFileSync(join(APP, '00-shell.js'), 'utf8');

const m = src.match(/\nfunction base64ToText\(b64\) \{([\s\S]*?)\n\}\n/);
if (!m) { console.error('FATAL: base64ToText not found in 00-shell.js'); process.exit(2); }
const base64ToText = new Function('b64', m[1]);

let pass = 0, fail = 0;
const check = (name, ok, detail) => {
  console.log((ok ? 'PASS ' : 'FAIL ') + name);
  if (ok) pass++; else { fail++; if (detail) console.log('   ' + detail); }
};
const osc52 = (s) => Buffer.from(s, 'utf8').toString('base64');
const roundTrip = (s) => {
  const got = base64ToText(osc52(s));
  check(`round trip: ${JSON.stringify(s)}`, got === s, `got ${JSON.stringify(got)}`);
};

roundTrip('Grüße aus Zürich, Øresund und Ærø.');
roundTrip('ñ ß ø å æ ü ö ä ÿ œ ł ž');
roundTrip('done → ready ✓ 🚀');
roundTrip('plain ASCII stays the same');
check('empty input', base64ToText('') === '');

const raw = atob(osc52('Zürich'));
check('raw atob reproduces the defect (proves the test measures something)', raw === 'Z\u00c3\u00bcrich', `atob gave ${JSON.stringify(raw)}`);
check('base64ToText does not reproduce it', base64ToText(osc52('Zürich')) === 'Zürich');

const long = 'Grüße '.repeat(200);
check('base64 wrapped at 76 columns', base64ToText(osc52(long).replace(/(.{76})/g, '$1\n')) === long);
check('base64 without padding', base64ToText(osc52('Müller').replace(/=+$/, '')) === 'Müller');

const payload = { sub: 'zoë', name: 'Zoë Müller', exp: 1790000000, x: '>>>???' };
const b64url = Buffer.from(JSON.stringify(payload), 'utf8').toString('base64url');
check('test payload contains - or _ (otherwise base64url is not exercised)', /[-_]/.test(b64url), b64url);
let jwt;
try { jwt = JSON.parse(base64ToText(b64url)); } catch (e) { jwt = String(e); }
check('base64url JWT payload with accents', jwt && jwt.name === 'Zoë Müller' && jwt.exp === 1790000000, JSON.stringify(jwt));

const start = m.index, end = m.index + m[0].length;
let atobs = 0;
for (const name of readdirSync(APP)) {
  if (!name.endsWith('.js') || name.endsWith('.min.js')) continue;
  const text = name === '00-shell.js' ? src : readFileSync(join(APP, name), 'utf8');
  for (const hit of text.matchAll(/\batob\(/g)) {
    atobs++;
    const inside = name === '00-shell.js' && hit.index > start && hit.index < end;
    const line = text.slice(0, hit.index).split('\n').length;
    check(`${name}:${line} uses atob only inside base64ToText`, inside, text.split('\n')[line - 1].trim());
  }
}
check('the atob scan found base64ToText itself', atobs >= 1);

console.log(`\n${pass} PASS, ${fail} FAIL`);
process.exit(fail ? 1 : 0);
