#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const target = process.argv[2] || join(root, 'internal/webassets/web/vendor/panel/app/00-shell.js');
const src = readFileSync(target, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-predictive-echo ===');

const extract = (name, args) => {
  const re = new RegExp('^ {4}' + name + '\\(' + args.join(', ') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const m = src.match(re);
  if (!m) { no('could not extract ' + name); process.exit(1); }
  return new Function(...args, m[1]);
};

const app = {
  hostTermPredictiveEcho: 'auto',
  hostTermEchoThreshold: 60,
  _predictEcho: extract('_predictEcho', ['pane', 'd']),
  _canPredict: extract('_canPredict', ['pane', 'd']),
  _erasePrediction: extract('_erasePrediction', ['pane']),
  _repredictEcho: extract('_repredictEcho', ['pane']),
  _looksLikePasswordLine: extract('_looksLikePasswordLine', ['pane']),
};

function newPane({ line = '$ ', cursorX = 2, type: type = 'normal', eco = 300, cols = 80 } = {}) {
  const written = [];
  const pane = {
    eco, written,
    term: {
      cols,
      write(x){ written.push(x); },
      buffer: { active: {
        type: type, baseY: 0, cursorY: 0, cursorX,
        getLine: () => ({ translateToString: () => line }),
      } },
    },
  };
  return pane;
}
const output = (p) => p.written.join('');

{
  const p = newPane();
  app._predictEcho.call(app, p, 'l');
  (p._pred && p._pred.txt === 'l' && output(p) === '\x1b[2ml\x1b[22m')
    ? ok('slow network: the keystroke shows up at once, dimmed')
    : no('did not predict on a slow network: ' + JSON.stringify(output(p)));
}

{
  const cases = [
    ['alternate screen (vim/htop repaints the whole screen)', newPane({ type: 'alternate' })],
    ['password prompt on the cursor line',              newPane({ line: '[sudo] password for sam:' })],
    ['good network (below the threshold the risk is not worth it)', newPane({ eco: 20 })],
    ['edge of the line (\\b does not move up a line)',         newPane({ cursorX: 79 })],
  ];
  for (const [name, p] of cases) {
    app._predictEcho.call(app, p, 'x');
    output(p) === '' ? ok('does not predict: ' + name) : no('PREDICTED where it must not: ' + name);
  }
  const noEcho = newPane(); noEcho._serverEchoes = false;
  app._predictEcho.call(app, noEcho, 'x');
  output(noEcho) === '' ? ok('does not predict: the server stopped echoing (a password is being typed)')
                       : no('PREDICTED with the server in no-echo mode — that would leak a password');

  const control = newPane();
  app._predictEcho.call(app, control, '\r');
  output(control) === '' ? ok('does not predict: Enter/control (the effect belongs to the shell, not to us)')
                         : no('predicted a control character');

  const off = newPane();
  app._predictEcho.call({ ...app, hostTermPredictiveEcho: 'never' }, off, 'x');
  output(off) === '' ? ok('does not predict: the user turned it off') : no('ignored the user preference');

  const forced = newPane({ eco: 5 });
  app._predictEcho.call({ ...app, hostTermPredictiveEcho: 'always' }, forced, 'x');
  output(forced) !== '' ? ok('"always" mode predicts even on a good network') : no('"always" mode did not predict');
}

{
  const p = newPane();
  for (const c of ['l','s']) app._predictEcho.call(app, p, c);
  p.written.length = 0;
  app._erasePrediction.call(app, p);
  (output(p) === '\b \b'.repeat(2) && p._pred.txt === '')
    ? ok('erasing returns the screen to the server state (1 erase per cell)')
    : no('wrong erase: ' + JSON.stringify(output(p)));
}

{
  const p = newPane({ cursorX: 2 });
  for (const c of 'ls -la') app._predictEcho.call(app, p, c);
  p.written.length = 0;
  p.term.buffer.active.cursorX = 4;
  app._repredictEcho.call(app, p);
  (p._pred.txt === ' -la' && output(p) === '\x1b[2m -la\x1b[22m')
    ? ok('only the unconfirmed part is repainted (the tail does not blink every frame)')
    : no('wrong reconciliation: txt=' + JSON.stringify(p._pred.txt) + ' output=' + JSON.stringify(output(p)));
}
{
  const p = newPane({ cursorX: 2 });
  for (const c of 'ls') app._predictEcho.call(app, p, c);
  p.written.length = 0;
  p.term.buffer.active.cursorX = 4;
  app._repredictEcho.call(app, p);
  (p._pred.txt === '' && output(p) === '')
    ? ok('all confirmed → nothing repainted, no dimmed leftovers')
    : no('a guess survived a full confirmation');
}
{
  const p = newPane({ cursorX: 2 });
  for (const c of 'abc') app._predictEcho.call(app, p, c);
  p.written.length = 0;
  p.term.buffer.active.cursorY = 1;
  app._repredictEcho.call(app, p);
  (p._pred.txt === '' && output(p) === '')
    ? ok('the server changed line → the guess dies (it does not leak onto the wrong line)')
    : no('the guess survived a line change');
}
{
  const p = newPane({ cursorX: 2 });
  app._predictEcho.call(app, p, 'x');
  p.written.length = 0;
  p.term.buffer.active.type = 'alternate';
  app._repredictEcho.call(app, p);
  (p._pred.txt === '' && output(p) === '')
    ? ok('a TUI took over mid-flight → the guess is dropped, not repainted on top')
    : no('repainted on top of a TUI');
}

{
  const i = src.indexOf('self._erasePrediction(state)');
  const j = src.indexOf('self._repredictEcho(state)');
  (i > 0 && j > i && /if \(!q\.length\) return;\n {10}\/\/ The rule that makes predictive echo safe/.test(src))
    ? ok('flushTerm erases before the batch and repaints after (the order is the guarantee)')
    : no('the erase/repaint order in flushTerm does not check out');
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
