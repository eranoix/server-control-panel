#!/usr/bin/env node
// test-eco-preditivo.mjs — the local echo guess is not allowed to lie.
//
// The idea behind predictive echo is simple and the implementation is where the
// risk lives: painting on screen something the server has not confirmed yet. The
// guarantee that makes it safe is a single one — the screen returns to the
// server's truth before any byte of it is applied — and the rest are the
// boundaries where guessing would be dishonest (full-screen app, password,
// good network, edge of the line).
//
// As in test-pane-outbox, the functions are extracted FROM THE REAL FILE: a copy
// of the logic in here would start drifting from the product with nobody seeing it.
//
// Usage: node scripts/test-eco-preditivo.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const alvo = process.argv[2] || join(raiz, 'internal/webassets/web/vendor/vpsm/app/00-shell.js');
const src = readFileSync(alvo, 'utf8');

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
console.log('=== test-eco-preditivo ===');

const extrai = (nome, args) => {
  const re = new RegExp('^ {4}' + nome + '\\(' + args.join(', ') + '\\)\\{\\n([\\s\\S]*?)^ {4}\\},$', 'm');
  const m = src.match(re);
  if (!m) { no('could not extract ' + nome); process.exit(1); }
  return new Function(...args, m[1]);
};

const app = {
  hostTermEcoPreditivo: 'auto',
  hostTermEcoLimiar: 60,
  _preveEco: extrai('_preveEco', ['pane', 'd']),
  _podePrever: extrai('_podePrever', ['pane', 'd']),
  _apagaPrevisao: extrai('_apagaPrevisao', ['pane']),
  _repreveEco: extrai('_repreveEco', ['pane']),
  _pareceLinhaDeSenha: extrai('_pareceLinhaDeSenha', ['pane']),
};

// Fake terminal with a cursor and a single line — all the prediction consults.
function novoPane({ linha = '$ ', cursorX = 2, tipo = 'normal', eco = 300, cols = 80 } = {}) {
  const escrito = [];
  const pane = {
    eco, escrito,
    term: {
      cols,
      write(x){ escrito.push(x); },
      buffer: { active: {
        type: tipo, baseY: 0, cursorY: 0, cursorX,
        getLine: () => ({ translateToString: () => linha }),
      } },
    },
  };
  return pane;
}
const saida = (p) => p.escrito.join('');

// ── 1. where the guess is honest ────────────────────────────────────────────
{
  const p = novoPane();
  app._preveEco.call(app, p, 'l');
  (p._pred && p._pred.txt === 'l' && saida(p) === '\x1b[2ml\x1b[22m')
    ? ok('slow network: the keystroke shows up at once, dimmed')
    : no('did not predict on a slow network: ' + JSON.stringify(saida(p)));
}

// ── 2. the boundaries: each one, on its own, cancels the guess ──────────────
{
  const casos = [
    ['alternate screen (vim/htop repaints the whole screen)', novoPane({ tipo: 'alternate' })],
    ['password prompt on the cursor line',              novoPane({ linha: '[sudo] password for sam:' })],
    ['good network (below the threshold the risk is not worth it)', novoPane({ eco: 20 })],
    ['edge of the line (\\b does not move up a line)',         novoPane({ cursorX: 79 })],
  ];
  for (const [nome, p] of casos) {
    app._preveEco.call(app, p, 'x');
    saida(p) === '' ? ok('does not predict: ' + nome) : no('PREDICTED where it must not: ' + nome);
  }
  const semEco = novoPane(); semEco._servidorEcoa = false;
  app._preveEco.call(app, semEco, 'x');
  saida(semEco) === '' ? ok('does not predict: the server stopped echoing (a password is being typed)')
                       : no('PREDICTED with the server in no-echo mode — that would leak a password');

  const controle = novoPane();
  app._preveEco.call(app, controle, '\r');
  saida(controle) === '' ? ok('does not predict: Enter/control (the effect belongs to the shell, not to us)')
                         : no('predicted a control character');

  const desligado = novoPane();
  app._preveEco.call({ ...app, hostTermEcoPreditivo: 'nunca' }, desligado, 'x');
  saida(desligado) === '' ? ok('does not predict: the user turned it off') : no('ignored the user preference');

  const forcado = novoPane({ eco: 5 });
  app._preveEco.call({ ...app, hostTermEcoPreditivo: 'sempre' }, forcado, 'x');
  saida(forcado) !== '' ? ok('"always" mode predicts even on a good network') : no('"always" mode did not predict');
}

// ── 3. the central guarantee: the guess is erased whole ─────────────────────
{
  const p = novoPane();
  for (const c of ['l','s']) app._preveEco.call(app, p, c);
  p.escrito.length = 0;
  app._apagaPrevisao.call(app, p);
  (saida(p) === '\b \b'.repeat(2) && p._pred.txt === '')
    ? ok('erasing returns the screen to the server state (1 erase per cell)')
    : no('wrong erase: ' + JSON.stringify(saida(p)));
}

// ── 4. confirmation by cursor (the way mosh does it) ────────────────────────
{
  // Typed "ls -la"; the server echoed only "ls" (the cursor moved 2 columns).
  const p = novoPane({ cursorX: 2 });
  for (const c of 'ls -la') app._preveEco.call(app, p, c);
  p.escrito.length = 0;
  p.term.buffer.active.cursorX = 4;          // the server confirmed 2 characters
  app._repreveEco.call(app, p);
  (p._pred.txt === ' -la' && saida(p) === '\x1b[2m -la\x1b[22m')
    ? ok('only the unconfirmed part is repainted (the tail does not blink every frame)')
    : no('wrong reconciliation: txt=' + JSON.stringify(p._pred.txt) + ' output=' + JSON.stringify(saida(p)));
}
{
  // The server confirmed everything → no guess is left over.
  const p = novoPane({ cursorX: 2 });
  for (const c of 'ls') app._preveEco.call(app, p, c);
  p.escrito.length = 0;
  p.term.buffer.active.cursorX = 4;
  app._repreveEco.call(app, p);
  (p._pred.txt === '' && saida(p) === '')
    ? ok('all confirmed → nothing repainted, no dimmed leftovers')
    : no('a guess survived a full confirmation');
}
{
  // The server changed line (Enter, scroll, repaint): the anchor is dead.
  const p = novoPane({ cursorX: 2 });
  for (const c of 'abc') app._preveEco.call(app, p, c);
  p.escrito.length = 0;
  p.term.buffer.active.cursorY = 1;
  app._repreveEco.call(app, p);
  (p._pred.txt === '' && saida(p) === '')
    ? ok('the server changed line → the guess dies (it does not leak onto the wrong line)')
    : no('the guess survived a line change');
}
{
  // A full-screen app took over between the guess and the confirmation.
  const p = novoPane({ cursorX: 2 });
  app._preveEco.call(app, p, 'x');
  p.escrito.length = 0;
  p.term.buffer.active.type = 'alternate';
  app._repreveEco.call(app, p);
  (p._pred.txt === '' && saida(p) === '')
    ? ok('a TUI took over mid-flight → the guess is dropped, not repainted on top')
    : no('repainted on top of a TUI');
}

// ── 5. the order in the render flow (what keeps nothing overlapping) ────────
// Erasing MUST happen before the batch write and repainting AFTER it — inverted,
// the guess would sit underneath the server output.
{
  const i = src.indexOf('self._apagaPrevisao(state)');
  const j = src.indexOf('self._repreveEco(state)');
  (i > 0 && j > i && /if \(!q\.length\) return;\n {10}\/\/ The rule that makes predictive echo safe/.test(src))
    ? ok('flushTerm erases before the batch and repaints after (the order is the guarantee)')
    : no('the erase/repaint order in flushTerm does not check out');
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILURES`);
process.exit(fail ? 1 : 0);
