#!/usr/bin/env node
// Versioned test for the schedule builder (frequency → cron), the
// humanizer (cron → a spoken sentence) and the reverse parser (cron → builder model).
// Repo style: node-pure, extracts the REAL method bodies from 00-shell.js.
//
//   run: node scripts/test-sched-cron.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'vpsm', 'app', '00-shell.js');
const src = readFileSync(SRC, 'utf8');

function extract(name, sig) {
  const re = new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + '\\(' + sig + '\\) \\{([\\s\\S]*?)\\n    \\},');
  const m = src.match(re);
  if (!m) { console.error('FATAL: method not found:', name); process.exit(2); }
  return m[1];
}

const comp = {
  schedWeekdayNames: ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'],
  schedForm: { j: { schedule: '' }, sched: {} },
};
comp._schedTimeMH       = new Function('t', extract('_schedTimeMH', 't')).bind(comp);
comp.schedCompileCron   = new Function('s', extract('schedCompileCron', 's')).bind(comp);
comp.schedHumanize      = new Function('expr', extract('schedHumanize', 'expr')).bind(comp);
comp.schedParseToBuilder = new Function('expr', extract('schedParseToBuilder', 'expr')).bind(comp);

let failed = 0;
const eq = (a, b) => JSON.stringify(a) === JSON.stringify(b);
function check(name, cond, got) {
  if (cond) { console.log('  ✓', name); } else { console.error('  ✗', name, '— got:', JSON.stringify(got)); failed++; }
}

// compile
const C = s => comp.schedCompileCron(s);
check('every 15 min',  C({ mode: 'every', everyN: 15, everyUnit: 'minutes' }) === '*/15 * * * *');
check('every 6 h',     C({ mode: 'every', everyN: 6, everyUnit: 'hours' }) === '0 */6 * * *');
check('daily 03:30',   C({ mode: 'daily', time: '03:30' }) === '30 3 * * *');
check('weekly mon+thu 14:00', C({ mode: 'weekly', weekdays: [4, 1], time: '14:00' }) === '0 14 * * 1,4', C({ mode: 'weekly', weekdays: [4, 1], time: '14:00' }));
check('weekly empty → daily', C({ mode: 'weekly', weekdays: [], time: '09:00' }) === '0 9 * * *');
check('monthly day 1 04:00', C({ mode: 'monthly', dom: 1, time: '04:00' }) === '0 4 1 * *');
check('clamp minutes >59', C({ mode: 'every', everyN: 90, everyUnit: 'minutes' }) === '*/59 * * * *');

// humanize
const H = comp.schedHumanize;
check('humanize every min', H('*/15 * * * *') === 'every 15 min', H('*/15 * * * *'));
check('humanize every h',   H('0 */6 * * *') === 'every 6 h', H('0 */6 * * *'));
check('humanize daily',     H('0 3 * * *') === 'every day at 03:00', H('0 3 * * *'));
check('humanize weekly',    H('0 14 * * 1,4') === 'mon, thu at 14:00', H('0 14 * * 1,4'));
check('humanize monthly',   H('0 4 1 * *') === 'day 1 of every month at 04:00', H('0 4 1 * *'));
check('humanize unknown → raw', H('5 4 * 6 *') === '5 4 * 6 *', H('5 4 * 6 *'));
check('humanize non-cron → raw', H('@weekly') === '@weekly');

// parse
const P = comp.schedParseToBuilder;
check('parse every min', eq(P('*/15 * * * *'), { mode: 'every', everyN: 15, everyUnit: 'minutes', time: '03:00', weekdays: [1], dom: 1 }), P('*/15 * * * *'));
check('parse daily', P('0 3 * * *').mode === 'daily' && P('0 3 * * *').time === '03:00');
check('parse weekly', eq(P('0 14 * * 1,4').weekdays, [1, 4]) && P('0 14 * * 1,4').mode === 'weekly');
check('parse monthly', P('30 5 12 * *').mode === 'monthly' && P('30 5 12 * *').dom === 12 && P('30 5 12 * *').time === '05:30');
check('parse unknown → cron', P('5 4 * 6 *').mode === 'cron');

// round-trip: compile(parse(expr)) === expr for supported patterns
for (const expr of ['*/15 * * * *', '0 */6 * * *', '0 3 * * *', '0 14 * * 1,4', '0 4 1 * *']) {
  const rt = comp.schedCompileCron(P(expr));
  check('round-trip ' + expr, rt === expr, rt);
}

console.log(failed === 0 ? '\nPASS — schedule builder cron logic' : `\nFAIL — ${failed} case(s)`);
process.exit(failed === 0 ? 0 : 1);
