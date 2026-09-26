#!/usr/bin/env node
// test-recovery-robustez.mjs — what has to stay standing when EVERYTHING else
// is falling over.
//
// test-recovery-term.mjs covers the transport (reconnect, outbox, backpressure).
// This one covers the PAGE: what happens when the emulator does not load, when a
// quick action fails, when the session dies with the tab hidden. These are the
// paths that only show up on the worst day — and so the ones nobody exercises.
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const raiz = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = readFileSync(join(raiz, 'internal/webassets/web/recovery-term.html'), 'utf8');
const script = (html.match(/<script>([\s\S]*?)<\/script>/) || [])[1];

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const espera = (ms) => new Promise((r) => setTimeout(r, ms));
console.log('=== test-recovery-robustez ===');
if (!script || script.length < 3000) { no('could not extract the page script'); process.exit(1); }

// ── a minimal DOM, but an honest one ────────────────────────────────────────
// Real classList/dataset/hidden: half the defects on this page were exactly
// about hiding or showing the right thing.
function novoEl(id) {
  const el = {
    id, textContent: '', value: '', style: {}, dataset: {}, hidden: true,
    filhos: [], scrollTop: 0, scrollHeight: 0, clientHeight: 0, clientWidth: 0,
    className: '', type: '', placeholder: '', _ouvintes: {},
    classList: {
      _s: new Set(),
      toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); },
      add(c) { this._s.add(c); }, remove(c) { this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    },
    appendChild(c) { this.filhos.push(c); return c; },
    append(...cs) { this.filhos.push(...cs); },
    remove() {},
    setAttribute() {}, removeAttribute() {},
    addEventListener(ev, f) { (this._ouvintes[ev] ||= []).push(f); },
    dispara(ev, e) { for (const f of (this._ouvintes[ev] || [])) f(e || {}); },
    click() {},
  };
  return el;
}

function montaCtx({ comXterm }) {
  const elementos = {};
  const ouvintes = {};
  const sockets = [];
  const criados = [];
  const chamadas = [];
  let resposta = null;

  class FakeWS {
    static CONNECTING = 0; static OPEN = 1; static CLOSED = 3;
    constructor(url) { this.url = url; this.readyState = 0; this.enviados = []; sockets.push(this); }
    send(b) { if (this.readyState !== 1) throw new Error('socket closed'); this.enviados.push(b); }
    close() { this.readyState = 3; if (this.onclose) this.onclose({ code: 1006 }); }
    abre() { this.readyState = 1; if (this.onopen) this.onopen(); }
    derruba(code = 1006) { this.readyState = 3; if (this.onclose) this.onclose({ code }); }
    recebe(dados) { if (this.onmessage) this.onmessage({ data: dados }); }
  }

  const escritoXterm = [];
  const termXterm = {
    cols: 80, rows: 24, _dados: null, _resize: null,
    write(x, cb) { escritoXterm.push(String(x)); if (cb) cb(); },
    focus() {}, onData(f) { this._dados = f; }, onResize(f) { this._resize = f; },
    buffer: { active: {
      type: 'normal', baseY: 0, cursorY: 0, cursorX: 5, length: 2,
      getLine: (i) => ({ translateToString: () => (i === 0 ? 'line one  ' : 'line two  ') }),
    } },
    loadAddon() {}, open() {},
  };

  const doc = {
    getElementById: (id) => (elementos[id] ||= novoEl(id)),
    querySelector: (sel) => (elementos['sel:' + sel] ||= novoEl(sel)),
    createElement: (tag) => { const e = novoEl('novo:' + tag); e.tag = tag; criados.push(e); return e; },
    addEventListener: (ev, f) => { (ouvintes[ev] ||= []).push(f); },
    cookie: 'vpsm_recovery_user=sam',
    hidden: false,
    body: novoEl('body'),
  };

  const ctx = {
    Terminal: comXterm ? function () { return termXterm; } : undefined,
    FitAddon: comXterm ? { FitAddon: function () { return { fit() {} }; } } : undefined,
    WebSocket: FakeWS, TextEncoder, TextDecoder,
    document: doc,
    window: { addEventListener: (ev, f) => { (ouvintes[ev] ||= []).push(f); } },
    navigator: { clipboard: { writeText: () => Promise.resolve() } },
    location: { protocol: 'https:', host: 'vpsm.example', href: '' },
    requestAnimationFrame: (f) => { setTimeout(f, 0); return 1; },
    cancelAnimationFrame: () => {},
    setTimeout, clearTimeout, setInterval, clearInterval,
    Date, Math, JSON, Uint8Array, Blob: function () {}, URL: { createObjectURL: () => 'blob:x', revokeObjectURL() {} },
    confirm: () => true,
    fetch: (url, opt) => {
      chamadas.push({ url: String(url), opt });
      const r = resposta && resposta(String(url), opt);
      return r ? Promise.resolve(r) : Promise.resolve({ ok: true, status: 200, type: 'basic', json: () => Promise.resolve({}) });
    },
  };
  ctx.__meta = { elementos, ouvintes, sockets, criados, chamadas, termXterm, escritoXterm,
                 setResposta: (f) => { resposta = f; } };
  return ctx;
}

function roda(ctx) {
  const nomes = Object.keys(ctx).filter((n) => n !== '__meta');
  const cauda = ';this.__x = { criaTerminal, doAction, host, claude, mostraAba, '
              + 'logaConsole, textoConsole, baixaEvidencia, renovaSessao, reconectaAgora, limpaConsole };';
  new Function(...nomes, script + cauda).call(ctx, ...nomes.map((n) => ctx[n]));
  return ctx.__x;
}

// ════════════════════════════════════════════════════════════════════════════
// 1. The page SURVIVES the emulator failing to load.
//    Before: `new Terminal()` ran at the top of the module, the exception rose
//    and took both tabs, the quick actions and the exit button with it — leaving
//    a black screen on a page whose purpose is to work when the assets break.
// ════════════════════════════════════════════════════════════════════════════
{
  const ctx = montaCtx({ comXterm: false });
  let api = null;
  try { api = roda(ctx); ok('no xterm: the page script still runs end to end'); }
  catch (e) { no('with no xterm the whole page dies: ' + e.message); }

  if (api) {
    const m = ctx.__meta;
    m.sockets.length >= 1
      ? ok('no xterm: it still OPENS the shell connection (the terminal stays usable)')
      : no('no xterm and no connection — the emergency screen went inert');

    api.host.term.simples === true
      ? ok('no xterm: fell back to the simple engine (and admits it, instead of pretending)')
      : no('the engine was not marked as simple');

    const faixa = m.elementos['faixa'];
    (faixa && !faixa.hidden && String(faixa.textContent).includes('simple mode'))
      ? ok('no xterm: the banner SAYS it degraded (the operator knows if that is the problem)')
      : no('degraded silently: ' + JSON.stringify(faixa && faixa.textContent));

    // Input still reaches the server.
    const s = m.sockets[0]; s.abre(); s.enviados.length = 0;
    const inp = m.criados.find((e) => e.tag === 'input');
    if (inp) {
      inp.value = 'systemctl restart vps-manager';
      inp.dispara('keydown', { key: 'Enter', preventDefault() {} });
      const env = s.enviados.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('');
      env.includes('systemctl restart vps-manager\r')
        ? ok('no xterm: Enter in the field sends the raw line to the PTY')
        : no('the line never reached the socket: ' + JSON.stringify(env));

      s.enviados.length = 0;
      inp.dispara('keydown', { key: 'c', ctrlKey: true, preventDefault() {} });
      const ctrl = s.enviados.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('');
      ctrl === '\x03'
        ? ok('no xterm: Ctrl+C becomes 0x03 (without it the simple mode would be a dead end)')
        : no('Ctrl+C did not become a control character: ' + JSON.stringify(ctrl));
    } else no('the simple mode created no input field');

    // Output: ANSI stripped, \r and \b applied — else the screen is escape junk.
    const pre = m.criados.find((e) => e.className === 'simples-saida');
    if (pre) {
      api.host.term.write('\x1b[32mok\x1b[0m\r\nprogresso 10%\rprogresso 99%\nabcX\b\b');
      const t = pre.textContent;
      (!t.includes('\x1b') && t.includes('ok') && t.includes('progresso 99%') && !t.includes('progresso 10%') && t.endsWith('ab'))
        ? ok('no xterm: drops ANSI, applies \\r (overwrite) and \\b (erase)')
        : no('simple-mode output is wrong: ' + JSON.stringify(t));
      // The PTY delivers chunks of whatever size the network feels like, and that
      // boundary lands in the middle of a CRLF with no effort at all. Handled
      // wrong, the '\r' of the CRLF erases the whole line — shell output vanishes.
      // (the scrollback of the engine accumulates on purpose — we check the suffix)
      const antes = pre.textContent;
      api.host.term.write('primeira\r');
      api.host.term.write('\nsegunda\r\n');
      const t2 = pre.textContent.slice(antes.length);
      (t2 === 'primeira\nsegunda\n')
        ? ok('no xterm: a CRLF split across two chunks does not swallow the line')
        : no('a split CRLF corrupted the output: ' + JSON.stringify(t2));
    } else no('the simple mode created no output <pre>');
  }
}

// ════════════════════════════════════════════════════════════════════════════
// 2. The quick actions tell the TRUTH.
//    Before: on success the page called `term.write(...)` in a scope where `term`
//    does not exist. The ReferenceError fell into the catch and repainted success
//    as a red error — inviting a second click on `rollback`.
// ════════════════════════════════════════════════════════════════════════════
{
  const ctx = montaCtx({ comXterm: true });
  const api = roda(ctx);
  const m = ctx.__meta;

  m.setResposta((url) => {
    if (url.includes('/recovery/action/rollback')) {
      return { ok: true, status: 200, json: () => Promise.resolve({ ok: true, message: 'rollback ok', output: 'rolled back to build 123' }) };
    }
    if (url.includes('/recovery/action/restart')) {
      // The command ran and exited != 0: the server returns HTTP 200 with ok:false.
      return { ok: true, status: 200, json: () => Promise.resolve({ ok: false, error: 'exit status 1', output: 'Job failed' }) };
    }
    return { ok: true, status: 200, json: () => Promise.resolve({}) };
  });

  await api.doAction('rollback');
  await espera(20);
  const st = m.elementos['action-status'];
  String(st.textContent).startsWith('✓')
    ? ok('a successful action shows as SUCCESS (no longer red from a ReferenceError)')
    : no('success still turns into an error: ' + JSON.stringify(st.textContent));

  const txt = api.textoConsole();
  (txt.includes('rollback: ok') && txt.includes('rolled back to build 123'))
    ? ok('the action output goes to its own panel, with a timestamp and the content')
    : no('action output not recorded: ' + JSON.stringify(txt));

  !m.escritoXterm.join('').includes('rolled back to build 123')
    ? ok('the action output is NOT written inside the terminal (no repaint over a running command)')
    : no('it writes into xterm over what is running again');

  await api.doAction('restart');
  await espera(20);
  (String(m.elementos['action-status'].textContent).includes('exit status 1')
    && api.textoConsole().includes('FAILED'))
    ? ok('an execution failure (HTTP 200 + ok:false) shows as FAILURE, not as success')
    : no('a command that failed would read as success: ' + JSON.stringify(m.elementos['action-status'].textContent));

  // Evidence: it has to join the console + both screens, without blowing up.
  api.limpaConsole();
  api.logaConsole('$ health', 'ok');
  let estourou = null;
  try { api.baixaEvidencia(); } catch (e) { estourou = e; }
  const baixado = m.criados.find((e) => e.tag === 'a' && String(e.download || '').startsWith('recovery-'));
  (!estourou && baixado)
    ? ok('saving evidence produces a file (the post-mortem does not depend on scrollback surviving)')
    : no('saving evidence failed: ' + (estourou ? estourou.message : 'no download was triggered'));
}

// ════════════════════════════════════════════════════════════════════════════
// 3. The session: expiring silently is worse than expiring.
// ════════════════════════════════════════════════════════════════════════════
{
  const ctx = montaCtx({ comXterm: true });
  const api = roda(ctx);
  const m = ctx.__meta;

  // 401 = the 30 min token expired (tab hidden too long). This used to slip by
  // and the page kept looking alive until the operator typed and nothing moved.
  m.setResposta((url) => (url.includes('/recovery/renew')
    ? { ok: false, status: 401, json: () => Promise.resolve({}) }
    : { ok: true, status: 200, json: () => Promise.resolve({}) }));
  await api.renovaSessao();
  await espera(20);
  String(m.elementos['faixa'].textContent).includes('expired')
    ? ok('a refused renewal (401) becomes a banner notice, not silence')
    : no('the 401 on renewal slipped by: ' + JSON.stringify(m.elementos['faixa'].textContent));

  // Coming back to the tab is when the session is closest to dying — the 5 min
  // interval does not run while the tab is hidden.
  const ctx2 = montaCtx({ comXterm: true });
  const api2 = roda(ctx2);
  const m2 = ctx2.__meta;
  m2.setResposta((url) => (url.includes('/recovery/renew')
    ? { ok: true, status: 200, json: () => Promise.resolve({ ok: true, restante_seg: 4200 }) }
    : { ok: true, status: 200, json: () => Promise.resolve({}) }));
  m2.chamadas.length = 0;
  ctx2.document.hidden = false;
  for (const f of (m2.ouvintes['visibilitychange'] || [])) f({});
  await espera(30);
  m2.chamadas.some((c) => c.url.includes('/recovery/renew'))
    ? ok('coming back to the tab renews the session at once (no waiting for the 5 min cycle)')
    : no('coming back to the tab did not renew — you return to a dead session');

  // Clock: knowing 8 min are left changes the order of what you do in a crisis.
  await espera(1100);
  /session \d+:\d\d/.test(String(m2.elementos['relogio'].textContent))
    ? ok('the time left in the session is ALWAYS visible')
    : no('the clock does not paint: ' + JSON.stringify(m2.elementos['relogio'].textContent));
}

// ════════════════════════════════════════════════════════════════════════════
// 4. The connection pill is shared by both tabs.
//    Without a repaint on switch, it froze in the state of the OTHER tab —
//    reading "connected" from a terminal that dropped is worse than nothing.
// ════════════════════════════════════════════════════════════════════════════
{
  const ctx = montaCtx({ comXterm: true });
  const api = roda(ctx);
  const m = ctx.__meta;
  m.sockets[0].abre();
  const pilula = m.elementos['conn'];
  String(pilula.textContent).includes('host')
    ? ok('the pill says WHICH terminal it is talking about')
    : no('pill with no label: ' + JSON.stringify(pilula.textContent));

  // Leave for the Claude tab (which never connected) and come back: the pill has
  // to speak about the host again, without waiting for the next network event.
  pilula.textContent = 'junk from another tab'; pilula.dataset.e = 'down';
  api.mostraAba('host');
  (String(pilula.textContent).includes('host') && pilula.dataset.e === 'open')
    ? ok('switching tabs repaints the pill with the state of the front tab')
    : no('the pill kept state from another tab: ' + JSON.stringify(pilula.textContent) + ' / ' + pilula.dataset.e);

  // Typing held during the outage has to be VISIBLE, otherwise it looks lost.
  m.sockets[m.sockets.length - 1].derruba(1006);
  api.host.envia('reboot now');
  String(pilula.textContent).includes('queued')
    ? ok('what was typed during the outage is counted in the pill')
    : no('invisible queue: ' + JSON.stringify(pilula.textContent));
}

// ════════════════════════════════════════════════════════════════════════════
// 5. xterm can exist and STILL fail to open — open() is where it touches canvas,
//    fonts and WebGL, which is where a locked-down browser takes it down. Having
//    the try/catch only around the constructor left the protection useless in
//    exactly the case most likely to need it.
// ════════════════════════════════════════════════════════════════════════════
{
  const ctx = montaCtx({ comXterm: true });
  ctx.Terminal = function () {
    return Object.assign({}, ctx.__meta.termXterm, {
      open() { throw new Error('canvas blocked'); },
    });
  };
  let api = null;
  try { api = roda(ctx); } catch (e) { no('a throwing open() still kills the page: ' + e.message); }
  if (api) {
    const m = ctx.__meta;
    (api.host.term.simples === true && m.sockets.length >= 1)
      ? ok('an xterm that fails in open() also falls back to simple mode (and connects)')
      : no('it did not degrade when open() threw');
    String((m.elementos['faixa'] || {}).textContent).includes('simple mode')
      ? ok('a failure in open() is announced in the banner too')
      : no('a failure in open() degraded silently');
  }
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
