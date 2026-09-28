#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const html = readFileSync(join(root, 'internal/webassets/web/recovery-term.html'), 'utf8');
const script = (html.match(/<script>([\s\S]*?)<\/script>/) || [])[1];

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
console.log('=== test-recovery-robustness ===');
if (!script || script.length < 3000) { no('could not extract the page script'); process.exit(1); }

function newEl(id) {
  const el = {
    id, textContent: '', value: '', style: {}, dataset: {}, hidden: true,
    children: [], scrollTop: 0, scrollHeight: 0, clientHeight: 0, clientWidth: 0,
    className: '', type: '', placeholder: '', _listeners: {},
    classList: {
      _s: new Set(),
      toggle(c, on) { on ? this._s.add(c) : this._s.delete(c); },
      add(c) { this._s.add(c); }, remove(c) { this._s.delete(c); },
      contains(c) { return this._s.has(c); },
    },
    appendChild(c) { this.children.push(c); return c; },
    append(...cs) { this.children.push(...cs); },
    remove() {},
    setAttribute() {}, removeAttribute() {},
    addEventListener(ev, f) { (this._listeners[ev] ||= []).push(f); },
    fire(ev, e) { for (const f of (this._listeners[ev] || [])) f(e || {}); },
    click() {},
  };
  return el;
}

function buildCtx({ withXterm }) {
  const elements = {};
  const listeners = {};
  const sockets = [];
  const created = [];
  const calls = [];
  let response = null;

  class FakeWS {
    static CONNECTING = 0; static OPEN = 1; static CLOSED = 3;
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
    send(b) { if (this.readyState !== 1) throw new Error('socket closed'); this.sent.push(b); }
    close() { this.readyState = 3; if (this.onclose) this.onclose({ code: 1006 }); }
    open() { this.readyState = 1; if (this.onopen) this.onopen(); }
    kill(code = 1006) { this.readyState = 3; if (this.onclose) this.onclose({ code }); }
    receive(dados) { if (this.onmessage) this.onmessage({ data: dados }); }
  }

  const writtenXterm = [];
  const termXterm = {
    cols: 80, rows: 24, _data: null, _resize: null,
    write(x, cb) { writtenXterm.push(String(x)); if (cb) cb(); },
    focus() {}, onData(f) { this._data = f; }, onResize(f) { this._resize = f; },
    buffer: { active: {
      type: 'normal', baseY: 0, cursorY: 0, cursorX: 5, length: 2,
      getLine: (i) => ({ translateToString: () => (i === 0 ? 'line one  ' : 'line two  ') }),
    } },
    loadAddon() {}, open() {},
  };

  const doc = {
    getElementById: (id) => (elements[id] ||= newEl(id)),
    querySelector: (sel) => (elements['sel:' + sel] ||= newEl(sel)),
    createElement: (tag) => { const e = newEl('fresh:' + tag); e.tag = tag; created.push(e); return e; },
    addEventListener: (ev, f) => { (listeners[ev] ||= []).push(f); },
    cookie: 'panel_recovery_user=sam',
    hidden: false,
    body: newEl('body'),
  };

  const ctx = {
    Terminal: withXterm ? function () { return termXterm; } : undefined,
    FitAddon: withXterm ? { FitAddon: function () { return { fit() {} }; } } : undefined,
    WebSocket: FakeWS, TextEncoder, TextDecoder,
    document: doc,
    window: { addEventListener: (ev, f) => { (listeners[ev] ||= []).push(f); } },
    navigator: { clipboard: { writeText: () => Promise.resolve() } },
    location: { protocol: 'https:', host: 'panel.example', href: '' },
    requestAnimationFrame: (f) => { setTimeout(f, 0); return 1; },
    cancelAnimationFrame: () => {},
    setTimeout, clearTimeout, setInterval, clearInterval,
    Date, Math, JSON, Uint8Array, Blob: function () {}, URL: { createObjectURL: () => 'blob:x', revokeObjectURL() {} },
    confirm: () => true,
    fetch: (url, opt) => {
      calls.push({ url: String(url), opt });
      const r = response && response(String(url), opt);
      return r ? Promise.resolve(r) : Promise.resolve({ ok: true, status: 200, type: 'basic', json: () => Promise.resolve({}) });
    },
  };
  ctx.__meta = { elements, listeners, sockets, created, calls, termXterm, writtenXterm,
                 setResponse: (f) => { response = f; } };
  return ctx;
}

function run(ctx) {
  const names = Object.keys(ctx).filter((n) => n !== '__meta');
  const tail = ';this.__x = { createTerminal, doAction, host, claude, showTab, '
              + 'logConsole, consoleText, downloadEvidence, renewSession, reconnectNow, clearConsole };';
  new Function(...names, script + tail).call(ctx, ...names.map((n) => ctx[n]));
  return ctx.__x;
}

{
  const ctx = buildCtx({ withXterm: false });
  let api = null;
  try { api = run(ctx); ok('no xterm: the page script still runs end to end'); }
  catch (e) { no('with no xterm the whole page dies: ' + e.message); }

  if (api) {
    const m = ctx.__meta;
    m.sockets.length >= 1
      ? ok('no xterm: it still OPENS the shell connection (the terminal stays usable)')
      : no('no xterm and no connection — the emergency screen went inert');

    api.host.term.simple === true
      ? ok('no xterm: fell back to the simple engine (and admits it, instead of pretending)')
      : no('the engine was not marked as simple');

    const banner = m.elements['banner'];
    (banner && !banner.hidden && String(banner.textContent).includes('simple mode'))
      ? ok('no xterm: the banner SAYS it degraded (the operator knows if that is the problem)')
      : no('degraded silently: ' + JSON.stringify(banner && banner.textContent));

    const s = m.sockets[0]; s.open(); s.sent.length = 0;
    const inp = m.created.find((e) => e.tag === 'input');
    if (inp) {
      inp.value = 'systemctl restart server-control-panel';
      inp.fire('keydown', { key: 'Enter', preventDefault() {} });
      const env = s.sent.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('');
      env.includes('systemctl restart server-control-panel\r')
        ? ok('no xterm: Enter in the field sends the raw line to the PTY')
        : no('the line never reached the socket: ' + JSON.stringify(env));

      s.sent.length = 0;
      inp.fire('keydown', { key: 'c', ctrlKey: true, preventDefault() {} });
      const ctrl = s.sent.map((x) => (x instanceof Uint8Array ? new TextDecoder().decode(x) : String(x))).join('');
      ctrl === '\x03'
        ? ok('no xterm: Ctrl+C becomes 0x03 (without it the simple mode would be a dead end)')
        : no('Ctrl+C did not become a control character: ' + JSON.stringify(ctrl));
    } else no('the simple mode created no input field');

    const pre = m.created.find((e) => e.className === 'simple-output');
    if (pre) {
      api.host.term.write('\x1b[32mok\x1b[0m\r\nprogress 10%\rprogress 99%\nabcX\b\b');
      const t = pre.textContent;
      (!t.includes('\x1b') && t.includes('ok') && t.includes('progress 99%') && !t.includes('progress 10%') && t.endsWith('ab'))
        ? ok('no xterm: drops ANSI, applies \\r (overwrite) and \\b (erase)')
        : no('simple-mode output is wrong: ' + JSON.stringify(t));
      const before = pre.textContent;
      api.host.term.write('first\r');
      api.host.term.write('\nsecond\r\n');
      const t2 = pre.textContent.slice(before.length);
      (t2 === 'first\nsecond\n')
        ? ok('no xterm: a CRLF split across two chunks does not swallow the line')
        : no('a split CRLF corrupted the output: ' + JSON.stringify(t2));
    } else no('the simple mode created no output <pre>');
  }
}

{
  const ctx = buildCtx({ withXterm: true });
  const api = run(ctx);
  const m = ctx.__meta;

  m.setResponse((url) => {
    if (url.includes('/recovery/action/rollback')) {
      return { ok: true, status: 200, json: () => Promise.resolve({ ok: true, message: 'rollback ok', output: 'rolled back to build 123' }) };
    }
    if (url.includes('/recovery/action/restart')) {
      return { ok: true, status: 200, json: () => Promise.resolve({ ok: false, error: 'exit status 1', output: 'Job failed' }) };
    }
    return { ok: true, status: 200, json: () => Promise.resolve({}) };
  });

  await api.doAction('rollback');
  await wait(20);
  const st = m.elements['action-status'];
  String(st.textContent).startsWith('✓')
    ? ok('a successful action shows as SUCCESS (no longer red from a ReferenceError)')
    : no('success still turns into an error: ' + JSON.stringify(st.textContent));

  const txt = api.consoleText();
  (txt.includes('rollback: ok') && txt.includes('rolled back to build 123'))
    ? ok('the action output goes to its own panel, with a timestamp and the content')
    : no('action output not recorded: ' + JSON.stringify(txt));

  !m.writtenXterm.join('').includes('rolled back to build 123')
    ? ok('the action output is NOT written inside the terminal (no repaint over a running command)')
    : no('it writes into xterm over what is running again');

  await api.doAction('restart');
  await wait(20);
  (String(m.elements['action-status'].textContent).includes('exit status 1')
    && api.consoleText().includes('FAILED'))
    ? ok('an execution failure (HTTP 200 + ok:false) shows as FAILURE, not as success')
    : no('a command that failed would read as success: ' + JSON.stringify(m.elements['action-status'].textContent));

  api.clearConsole();
  api.logConsole('$ health', 'ok');
  let overflowed = null;
  try { api.downloadEvidence(); } catch (e) { overflowed = e; }
  const downloaded = m.created.find((e) => e.tag === 'a' && String(e.download || '').startsWith('recovery-'));
  (!overflowed && downloaded)
    ? ok('saving evidence produces a file (the post-mortem does not depend on scrollback surviving)')
    : no('saving evidence failed: ' + (overflowed ? overflowed.message : 'no download was triggered'));
}

{
  const ctx = buildCtx({ withXterm: true });
  const api = run(ctx);
  const m = ctx.__meta;

  m.setResponse((url) => (url.includes('/recovery/renew')
    ? { ok: false, status: 401, json: () => Promise.resolve({}) }
    : { ok: true, status: 200, json: () => Promise.resolve({}) }));
  await api.renewSession();
  await wait(20);
  String(m.elements['banner'].textContent).includes('expired')
    ? ok('a refused renewal (401) becomes a banner notice, not silence')
    : no('the 401 on renewal slipped by: ' + JSON.stringify(m.elements['banner'].textContent));

  const ctx2 = buildCtx({ withXterm: true });
  const api2 = run(ctx2);
  const m2 = ctx2.__meta;
  m2.setResponse((url) => (url.includes('/recovery/renew')
    ? { ok: true, status: 200, json: () => Promise.resolve({ ok: true, remaining_sec: 4200 }) }
    : { ok: true, status: 200, json: () => Promise.resolve({}) }));
  m2.calls.length = 0;
  ctx2.document.hidden = false;
  for (const f of (m2.listeners['visibilitychange'] || [])) f({});
  await wait(30);
  m2.calls.some((c) => c.url.includes('/recovery/renew'))
    ? ok('coming back to the tab renews the session at once (no waiting for the 5 min cycle)')
    : no('coming back to the tab did not renew — you return to a dead session');

  await wait(1100);
  /session \d+:\d\d/.test(String(m2.elements['clock'].textContent))
    ? ok('the time left in the session is ALWAYS visible')
    : no('the clock does not paint: ' + JSON.stringify(m2.elements['clock'].textContent));
}

{
  const ctx = buildCtx({ withXterm: true });
  const api = run(ctx);
  const m = ctx.__meta;
  m.sockets[0].open();
  const pill = m.elements['conn'];
  String(pill.textContent).includes('host')
    ? ok('the pill says WHICH terminal it is talking about')
    : no('pill with no label: ' + JSON.stringify(pill.textContent));

  pill.textContent = 'junk from another tab'; pill.dataset.e = 'down';
  api.showTab('host');
  (String(pill.textContent).includes('host') && pill.dataset.e === 'open')
    ? ok('switching tabs repaints the pill with the state of the front tab')
    : no('the pill kept state from another tab: ' + JSON.stringify(pill.textContent) + ' / ' + pill.dataset.e);

  m.sockets[m.sockets.length - 1].kill(1006);
  api.host.send('reboot now');
  String(pill.textContent).includes('queued')
    ? ok('what was typed during the outage is counted in the pill')
    : no('invisible queue: ' + JSON.stringify(pill.textContent));
}

{
  const ctx = buildCtx({ withXterm: true });
  ctx.Terminal = function () {
    return Object.assign({}, ctx.__meta.termXterm, {
      open() { throw new Error('canvas blocked'); },
    });
  };
  let api = null;
  try { api = run(ctx); } catch (e) { no('a throwing open() still kills the page: ' + e.message); }
  if (api) {
    const m = ctx.__meta;
    (api.host.term.simple === true && m.sockets.length >= 1)
      ? ok('an xterm that fails in open() also falls back to simple mode (and connects)')
      : no('it did not degrade when open() threw');
    String((m.elements['banner'] || {}).textContent).includes('simple mode')
      ? ok('a failure in open() is announced in the banner too')
      : no('a failure in open() degraded silently');
  }
}

console.log('─────────────────────────────────────');
console.log(`RESULT: ${pass} OK / ${fail} FAILED`);
process.exit(fail ? 1 : 0);
