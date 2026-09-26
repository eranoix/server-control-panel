// Fixture for the Proxmox tab rendering harness.
(function () {
  const origApp = window.app;
  window.app = function () {
    const c = origApp();
    c.token = 'harness';
    c.api = async function (route, opts) {
      const body = (window.__responses && window.__responses[route.split('?')[0]]) || {};
      if (opts && opts.raw) return { ok: true, status: 200, json: async () => body };
      return body;
    };
    c._apiError = async () => new Error('test-error');
    c._errText = (e) => String((e && e.message) || e);
    c.showToast = () => {};
    c.askConfirm = async () => false;
    c.pvxLiveCycle = function () {};
    // The harness renders the PROXMOX SECTION, not the PWA. The service worker
    // registration runs in the app’s init and fails here for want of a real
    // scope; the error shows up in the console and would bury the very signal
    // this check exists to see. Switching it off is honest — there is nothing
    // of Proxmox in it.
    c.installPWA = function () {};
    return c;
  };

  const NOTA_LAB = [
    '## lab: the Lab panel (from Phase 9 on)',
    '',
    '**What it does:** today, **nothing**. And the box exists anyway.',
    '',
    '**Why it exists:** so the panel has somewhere to go without surgery.',
    '',
    '---',
    '',
    '#### Technical',
    '',
    '- Recreatable from the repository: `sh bin/guest-create nodes/lab/204.conf --apply`',
    '- Fixed IP `192.168.100.42`',
  ].join('\n');

  window.__responses = {
    '/api/proxmox/storage': { pools: [
      { id: 'local', type: 'dir', content: ['backup', 'iso', 'vztmpl'], free: 8e11, total: 9e11, used: 1e10, used_pct: 1 },
      { id: 'local-zfs', type: 'zfspool', content: ['images', 'rootdir'], free: 8e11, total: 9e11, used: 6e10, used_pct: 7 },
      { id: 'pbs', type: 'pbs', content: ['backup'], free: 8.5e11, total: 9e11, used: 4.4e10, used_pct: 5 },
    ] },
    '/api/nodes/lxc/204/nota': { node: 'lxc/204', markdown: NOTA_LAB, origem: 'pve-notes' },
    '/api/nodes/lxc/205/nota': { node: 'lxc/205', markdown: '', origem: 'vazia' },
    '/api/nodes/canario/nota': { node: 'canario', markdown: '', origem: 'fora-do-pve',
                                 motivo: 'this node is not a guest of this hypervisor, so the PVE note does not apply to it' },
    '/api/nodes/node/pve/nota': { node: 'node/pve', markdown: '# pve: the home server\n\n**What it is:** the physical machine that runs everything.', origem: 'pve-notes' },
    // RUNNING CT with a snapshot: the hypervisor only clones a running container
    // from a snapshot — a rule discovered by the live proof, not from the source.
    '/api/nodes/lxc/204/clone': { origem: 'lxc/204', origem_nome: 'lab', tipo: 'lxc',
                                  next_id: 991, sugestao: 'lab-copy', ligado: true,
                                  precisa_snapshot: true, snapshots: ['before-upgrade', 'base'] },
    // RUNNING CT with NO snapshot: the case where there is nothing to offer.
    '/api/nodes/lxc/202/clone': { origem: 'lxc/202', origem_nome: 'pbs', tipo: 'lxc',
                                  next_id: 993, sugestao: 'pbs-copy', ligado: true,
                                  precisa_snapshot: true, snapshots: [] },
    // STOPPED guest: no requirement at all.
    '/api/nodes/lxc/205/clone': { origem: 'lxc/205', origem_nome: 'observ', tipo: 'lxc',
                                  next_id: 992, sugestao: 'observ-copy', ligado: false,
                                  precisa_snapshot: false, snapshots: [] },
  };

  const C = () => document.body._x_dataStack[0];
  // 🔴 Do NOT use offsetParent: it is a property of HTMLElement and does NOT
  // exist on an SVG element, so `sv.offsetParent !== null` is ALWAYS true and
  // the visibility measurement lies exactly where the charts live.
  // checkVisibility() holds for both and considers the whole ancestor chain.
  const visible = (el) => !!el && typeof el.checkVisibility === 'function' &&
    el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });

  const svgs = () => Array.from(document.querySelectorAll('svg[role="img"]')).filter(visible);
  const parts = (sv) => {
    const ps = Array.from(sv.querySelectorAll('path'));
    const c = sv.querySelector('circle');
    return { area: (ps[0] && ps[0].getAttribute('d')) || '',
             stroke: (ps[1] && ps[1].getAttribute('d')) || '',
             pontos: (ps[2] && ps[2].getAttribute('d')) || '',
             linhas: sv.querySelectorAll('line').length,
             cx: c && c.getAttribute('cx'), cy: c && c.getAttribute('cy') };
  };
  const subs = (d) => (d.match(/M/g) || []).length;

  const series = (n, opts) => {
    opts = opts || {};
    const points = [];
    for (let i = 0; i < n; i++) {
      const p = { time: 1787000000 + i * 60 };
      if (!(opts.gapAt && opts.gapAt.includes(i))) {
        p.cpu = 0.1 + (i % 10) / 100; p.iowait = 0.01; p.loadavg = 1.5;
        p.memused = 8e9; p.memtotal = 64e9; p.arcsize = 4e9;
        p.rootused = 2e9; p.roottotal = 900e9; p.netin = 1e6; p.netout = 5e5;
        p.pressureiosome = 0.02; p.pressurememorysome = 0.01;
        p.mem = 1e9; p.maxmem = 2e9; p.disk = 5e9; p.maxdisk = 10e9;
        p.diskread = 1e5; p.diskwrite = 2e5;
      }
      points.push(p);
    }
    return { pontos: points, escopo: opts.escopo || 'hypervisor', janela: 'hour' };
  };

  // 🔴 NODES WITH THE REAL SHAPE, COPIED FROM THE LIVE /api/nodes RESPONSE.
  //
  // The previous version of this fixture invented `{id, kind, status:{value}}`
  // and nothing else. The result was worse than useless: ALL the buttons came up
  // disabled, because `pvxActionState` requires `credential.state === 'ok'`, and I
  // nearly concluded the whole panel was dead. Live, 9 of the 11 nodes have an ok
  // credential. A poor fixture is not "a simpler test" — it is a test that lies,
  // and it lies in the most expensive direction: inventing a defect that is not
  // there.
  //
  // The three cases below are the three that really exist in this lab: a guest
  // with an ok credential, a guest with NO credential (lxc/202 is like that
  // today) and the external node `canario`, which is no hypervisor’s guest.
  const stamp = (v) => ({ value: v, observed_at: 1787256260 });
  const guest = (id, nome, vmid, st, cred) => ({
    id, name: nome, transport: 'pve-api', address: '192.168.1.1', kind: 'guest', vmid,
    status: stamp(st), uptime: stamp(821680), cpu_frac: stamp(0.07), cpu_cores: stamp(4),
    mem_used: stamp(2e9), mem_total: stamp(8e9), mem_host: stamp(-1),
    disk_used: stamp(1e10), disk_total: stamp(5e10),
    net_in: stamp(6e9), net_out: stamp(5e9), disk_read: stamp(2e9), disk_write: stamp(6e8),
    net_in_rate: stamp(-1), net_out_rate: stamp(-1), template: false,
    credential: cred, age_seconds: 1, stale: false,
  });
  const CRED_OK = { token_id: 'lab@pve!node-x', expire: 1802645875, state: 'ok' };
  const CRED_MISSING = { token_id: '', expire: 0, state: 'ausente' };
  const NODES = [
    guest('lxc/204', 'lab', 204, 'running', CRED_OK),
    guest('lxc/205', 'observ', 205, 'stopped', CRED_OK),
    guest('lxc/202', 'pbs', 202, 'running', CRED_MISSING),
    guest('qemu/208', 'dev', 208, 'running', CRED_OK),
    { id: 'node/pve', name: 'pve', transport: 'pve-api', address: '', kind: 'host', vmid: 0,
      status: stamp('online'), uptime: stamp(0), cpu_frac: stamp(0), cpu_cores: stamp(0),
      mem_used: stamp(0), mem_total: stamp(0), mem_host: stamp(0),
      disk_used: stamp(0), disk_total: stamp(0), net_in: stamp(0), net_out: stamp(0),
      disk_read: stamp(0), disk_write: stamp(0), net_in_rate: stamp(0), net_out_rate: stamp(0),
      template: false, credential: { token_id: 'lab@pve!audit', expire: 1802645875, state: 'ok' },
      age_seconds: 1, stale: false },
    // 🔴 The node the hypervisor no longer lists. It is NOT deleted (a guest
    // disappears for being stopped, migrated or having its ACL withdrawn), but
    // neither can it be confused with "vencido" (expired), which means the panel
    // failed to look.
    Object.assign(guest('lxc/101', 'clone-proof', 101, 'stopped', CRED_MISSING),
                  { ausente_desde: 1787200000, stale: true, age_seconds: 56260 }),
    // 🔴 THE SECOND CASE, and it is the one that makes this check NON-VACUOUS:
    // absent with FRESH data. It is the real window right after the poller marks
    // the absence — the last `status` is still recent and says "running", but the
    // hypervisor already does not list the node. Without this case the `stale`
    // guard covers on its own and the `ausente_desde` guard could be removed
    // without a single check biting (measured).
    Object.assign(guest('lxc/102', 'just-deleted', 102, 'running', CRED_OK),
                  { ausente_desde: 1787256000, stale: false, age_seconds: 30 }),
    { id: 'canario', name: 'canario', transport: 'agente', address: '127.0.0.1:9', kind: 'externo', vmid: 0,
      status: stamp(''), uptime: stamp(0), cpu_frac: stamp(0), cpu_cores: stamp(0),
      mem_used: stamp(0), mem_total: stamp(0), mem_host: stamp(0),
      disk_used: stamp(0), disk_total: stamp(0), net_in: stamp(0), net_out: stamp(0),
      disk_read: stamp(0), disk_write: stamp(0), net_in_rate: stamp(0), net_out_rate: stamp(0),
      template: false, credential: CRED_MISSING, age_seconds: -1, stale: true },
  ];

  // 🔴 Opens through the screen’s REAL path, not by writing into pvx.open.
  // A shortcut is not fidelity: `pvxSelect` also sets `pvx.guestSel`, and the
  // whole Copies tab depends on it. Pinning only `pvx.open`, the harness
  // reported "there is no clone button" for a screen that has the button.
  // 🔴 THE SERIES GOES TO BOTH PLACES.
  //
  // Once `open()` started using the real path, `pvxGoTo('charts')` fires
  // `pvxLoadSeries()` — which is ASYNCHRONOUS and resolves LATER, overwriting
  // anything pinned into `pvx.series`. Teaching the stub to return the same
  // series makes the state survive the load, instead of the test racing it.
  const putSeries = (d) => {
    window.__responses['/api/proxmox/rrd'] = d;
    C().pvx.series = d;
  };

  const open = (id, tab) => {
    const n = C().pvxNodes().find((x) => x.id === id);
    if (!n) throw new Error('node not in the fixture: ' + id);
    C().pvx.open = '';
    // 🔴 Open EXACTLY the way a click does, and nothing more: clicking a node
    // calls `pvxSelect` (which pins the summary tab and calls `pvxOpen`), clicking
    // a TAB calls `pvxGoTo`. Always calling `pvxGoTo` is kinder than reality and
    // hid a real defect: the note was loaded only from `pvxGoTo`, so clicking a
    // node showed an EMPTY "What this box does" block.
    C().pvxSelect(n);
    if (tab !== 'summary') C().pvxGoTo(tab);
  };

  const expectCharts = (howMany, check) => () => {
    const s = svgs();
    if (s.length !== howMany) return { erro: 'expected ' + howMany + ' VISIBLE charts, found ' + s.length };
    const p = s.map(parts);
    if (p.some((x) => x.linhas !== 3)) return { erro: 'grid: ' + p.map((x) => x.linhas).join(',') + ' (expected 3 in all)' };
    if (p.some((x) => x.cx === null || isNaN(Number(x.cx)) || isNaN(Number(x.cy))))
      return { erro: 'end point without a numeric coordinate: ' + p.map((x) => x.cx + '/' + x.cy).join(' ') };
    const r = check ? check(p) : null;
    return r || { nota: howMany + ' charts visible and intact' };
  };

  window.__script = [
    { nome: 'the section opens and the hypervisor is chosen', step: () => {
        C().page = 'operations'; C().tabs.operations = 'proxmox';
        C().nodes = C().nodes || {}; C().nodes.list = NODES;
        open('node/pve', 'summary');
      }, expect: () => {
        if (!visible(document.querySelector('section'))) return { erro: 'the Proxmox section did not become visible' };
        return { nota: 'section visible, pve node open' };
      } },

    { nome: 'Charts with NO series — the state the screen ALWAYS opens in', step: () => {
        C().pvx.tab = 'charts';
      }, expect: () => {
        if (svgs().length) return { erro: 'with no series it should not draw an SVG' };
        const av = Array.from(document.querySelectorAll('div')).filter((d) => !d.children.length && d.textContent.trim() === 'no sample in this window');
        const v = av.filter(visible);
        if (v.length !== 10) return { erro: 'expected 10 VISIBLE "no sample" notices, found ' + v.length + ' of ' + av.length + ' in the DOM' };
        return { nota: '10 "no sample" notices: absence declared, not zero' };
      } },

    { nome: 'empty series (what a network error leaves behind)', step: () => {
        putSeries({ pontos: [], escopo: '', janela: 'hour' });
      }, expect: () => (svgs().length ? { erro: 'pontos:[] should not draw' } : { nota: 'nothing drawn, as it should be' }) },

    { nome: 'continuous series on the hypervisor — 10 metrics', step: () => {
        putSeries(series(60));
      }, expect: expectCharts(10, (p) => {
        if (p.some((x) => x.stroke.length < 20)) return { erro: 'EMPTY stroke in some chart: no error, but nothing drawn either' };
        if (p.some((x) => subs(x.stroke) !== 1)) return { erro: 'a continuous series must be ONE subpath: ' + p.map((x) => subs(x.stroke)).join(',') };
        if (p.some((x) => x.area.length < 20)) return { erro: 'empty area' };
        return { nota: '10 charts · continuous stroke, area, grid and end point' };
      }) },

    { nome: 'series with 3 gaps — the line has to BREAK', step: () => {
        putSeries(series(60, { gapAt: [10, 11, 12, 30] }));
      }, expect: expectCharts(10, (p) => {
        const n = p.map((x) => subs(x.stroke));
        if (n.some((k) => k !== 3)) return { erro: 'gap did not break the stroke: subpaths = ' + n.join(',') + ' (expected 3)' };
        return { nota: 'stroke broken into 3 subpaths' };
      }) },

    { nome: 'ISOLATED samples become dots', step: () => {
        putSeries(series(5, { gapAt: [1, 3] }));
      }, expect: expectCharts(10, (p) => {
        const n = p.map((x) => subs(x.pontos));
        if (n.some((k) => k !== 3)) return { erro: 'isolated samples did not become dots: ' + n.join(',') };
        return { nota: '3 isolated samples drawn' };
      }) },

    { nome: 'a series of just ONE point', step: () => { putSeries(series(1)); },
      expect: expectCharts(10) },

    { nome: 'points WITHOUT the metrics (all null)', step: () => {
        putSeries({ pontos: [{ time: 1787000000 }, { time: 1787000060 }], escopo: 'x', janela: 'hour' });
      }, expect: () => (svgs().length ? { erro: 'a missing metric should not draw' } : { nota: 'nothing drawn' }) },

    { nome: 'guest opened — switches to the 7 guest metrics', step: () => {
        // The stub goes in BEFORE opening: `pvxGoTo('charts')` fires the load,
        // and it has to find the right series already there.
        putSeries(series(30, { escopo: 'lxc/204' }));
        open('lxc/204', 'charts');
      }, expect: expectCharts(7, (p) => {
        if (p.some((x) => !x.stroke)) return { erro: 'guest with an empty stroke' };
        return { nota: '7 guest metrics drawn' };
      }) },

    { nome: 'STOPPED guest with the charts open', step: () => { open('lxc/205', 'charts'); },
      expect: expectCharts(7) },

    { nome: 'CLEARS the selection with Charts open (yesterday’s crash)', step: () => {
        C().pvx.open = ''; C().pvx.detail = null;
      }, expect: () => (svgs().length ? { erro: 'with no node open there are still ' + svgs().length + ' visible chart(s)' } : { nota: 'panel closed cleanly' }) },

    { nome: 'window change', step: () => { open('node/pve', 'charts'); C().pvx.janela = 'day'; },
      expect: expectCharts(10) },

    { nome: 'series goes back to NULL (what a badly written catch would do)', step: () => { putSeries(null); },
      expect: () => (svgs().length ? { erro: 'a null series should not draw' } : { nota: 'nothing drawn, no crash' }) },
  ];

  // ── the BUTTONS: existing in the HTML is not being on screen ────────────
  const buttons = () => Array.from(document.querySelectorAll('button')).filter(visible)
    .map((b) => (b.textContent || '').replace(/\s+/g, ' ').trim()).filter((t) => t);

  // 🔴 A button is identified by its ACTION (`@click`), never by its label: the
  // screen has two button sets with the same words (the BULK ones, disabled when
  // nothing is selected, and the open node's), so a text search finds the wrong
  // one. It also keeps a label rename from breaking the pin.
  const byAction = (excerpt) => Array.from(document.querySelectorAll('button'))
    .filter((b) => (b.getAttribute('@click') || '').includes(excerpt))
    .filter(visible)
    .map((b) => ({ t: (b.textContent || '').replace(/\s+/g, ' ').trim(), off: b.disabled || b.getAttribute('aria-disabled') === 'true' }))[0];
  const NO = {
    turnOn:   () => byAction("pvxPower(pvxOpenNode(),'start')"),
    turnOff:() => byAction("pvxPower(pvxOpenNode(),'shutdown')"),
    cut:  () => byAction("pvxPower(pvxOpenNode(),'stop')"),
    console: () => byAction('pvxOpenConsole(pvxOpenNode()'),
    revoke: () => byAction('pvxRevoke(pvxOpenNode())'),
  };
  const snapshot = () => Object.entries(NO).map(([k, f]) => {
    const b = f(); return k + '=' + (b ? (b.off ? 'locked' : 'ENABLED') : 'missing');
  }).join(' ');

  window.__script.push(
    { nome: 'BUTTONS · guest RUNNING — what is impossible stays locked', step: () => {
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const l = NO.turnOn(), d = NO.turnOff(), c = NO.cut(), co = NO.console();
        if (!l || !d || !c || !co) return { erro: 'actions missing from the node panel: ' + snapshot() };
        if (!l.off) return { erro: 'RUNNING guest offers "turn on", a useless order that pollutes the trail: ' + snapshot() };
        if (d.off || c.off) return { erro: 'running guest CANNOT turn off: ' + snapshot() };
        if (co.off) return { erro: 'running guest without a console: ' + snapshot() };
        return { nota: snapshot() };
      } },
    { nome: 'BUTTONS · guest STOPPED — the exact mirror', step: () => { open('lxc/205', 'summary'); },
      expect: () => {
        const l = NO.turnOn(), d = NO.turnOff(), c = NO.cut();
        if (!l) return { erro: 'stopped guest without "turnOn": ' + snapshot() };
        if (l.off) return { erro: 'STOPPED guest cannot turn on: ' + snapshot() };
        if ((d && !d.off) || (c && !c.off)) return { erro: 'STOPPED guest offers turnOff/cut: ' + snapshot() };
        return { nota: snapshot() };
      } },
    { nome: 'BUTTONS · guest with NO credential — locked AND with the reason on screen', step: () => { open('lxc/202', 'summary'); },
      expect: () => {
        const l = NO.turnOn(), c = NO.cut();
        if (l && !l.off) return { erro: 'no credential and "turnOn" enabled: ' + snapshot() };
        if (c && !c.off) return { erro: 'no credential and "cut" enabled: ' + snapshot() };
        const t = document.body.innerText.toLowerCase();
        if (t.indexOf('cofre') < 0 && t.indexOf('credential') < 0)
          return { erro: 'actions locked and the screen does NOT say why: a dead button with no explanation looks like a defect' };
        return { nota: snapshot() + ' · reason written on the screen' };
      } },
    { nome: 'BUTTONS · hypervisor — the machine’s power exists and is enabled', step: () => { open('node/pve', 'summary'); },
      expect: () => {
        const reb = byAction("pvxHostPower('reboot')"), des = byAction("pvxHostPower('shutdown')");
        if (!reb || !des) return { erro: 'hypervisor without restart/turnOff' };
        if (reb.off || des.off) return { erro: 'hypervisor power locked' };
        // On the hypervisor, powering a GUEST on/off makes no sense and has to be locked.
        const l = NO.turnOn();
        if (l && !l.off) return { erro: 'the hypervisor offers "turnOn guest": ' + snapshot() };
        return { nota: 'restart and turnOff enabled; guest power locked' };
      } },
  );

  // 🔴 EVERY :disabled EXPRESSION HAS TO RETURN A STRICT BOOLEAN.
  //
  // This closes the CLASS of defect that left the operator with no buttons.
  // Alpine, on a boolean attribute, only REMOVES it when the value is null,
  // undefined or false — anything else SETS it, the empty string included. So an
  // expression that returns `''` to mean "not busy" disables the button forever,
  // silently, and the operator sees a screen of dead buttons with no error
  // message to investigate.
  //
  // The check evaluates EVERY `:disabled` in the section against the current
  // state and demands `true` or `false`. It is not text analysis: it is the
  // value Alpine is going to use.
  window.__script.push({
    nome: ':disabled · every expression returns a strict boolean',
    step: () => { open('lxc/204', 'summary'); },
    expect: () => {
      const comp = C();
      const targets = Array.from(document.querySelectorAll('[\\:disabled]'));
      if (targets.length < 5) return { erro: 'only ' + targets.length + ' elements with :disabled: vacuous' };
      const bad = [];
      for (const el of targets) {
        const expr = el.getAttribute(':disabled');
        let v;
        try { v = Function('c', 'with (c) { return (' + expr + ') }')(comp); }
        catch (e) { bad.push(expr + ' → overflowed: ' + e.message); continue; }
        if (typeof v !== 'boolean') bad.push(expr + ' → ' + JSON.stringify(v) + ' (' + typeof v + '), not boolean');
      }
      if (bad.length) return { erro: bad.length + ' non-boolean expression(s): ' + bad.join(' ;; ') };
      return { nota: targets.length + ' :disabled expressions, all boolean' };
    },
  });

  // ══ THE NOTE: the Summary has to SUMMARISE ══════════════════════════════
  const noteVisible = () => Array.from(document.querySelectorAll('.pvx-md')).filter(visible)[0] || null;

  window.__script.push(
    { nome: 'SUMMARY · the node’s explanation shows up, rendered', step: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '' };
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { erro: 'the node note is NOT visible in the Summary: the summary does not summarise' };
        const t = el.innerText;
        if (t.indexOf('What it does') < 0) return { erro: 'the note body did not arrive: ' + t.slice(0, 80) };
        // Rendered, not dumped as raw text.
        // Any level will do: the level depends on how many `#` the note uses, and
        // pinning h3 would be pinning the prose of whoever wrote the note.
        if (!el.querySelector('h3,h4,h5,h6')) return { erro: 'the note title did not become a heading: Markdown was not rendered' };
        if (!el.querySelector('strong')) return { erro: 'bold did not render' };
        if (!el.querySelector('code')) return { erro: 'code did not render' };
        if (!el.querySelector('ul li')) return { erro: 'list did not render' };
        if (!el.querySelector('hr')) return { erro: 'rule did not render' };
        if (t.indexOf('**') >= 0 || t.indexOf('##') >= 0)
          return { erro: 'Markdown markup leaking as text: ' + t.slice(0, 90) };
        return { nota: el.querySelectorAll('h3,h4,h5,h6,strong,code,li,hr').length + ' elements rendered' };
      } },

    // 🔴 THIS CHECK WAS INVERTED, and the history stays here.
    //
    // It used to assert that the explanation came BEFORE the numbers — my design,
    // argued with "whoever clicks a node asks what this is before how it is".
    // The operator, who uses the screen every day and already knows what each box
    // is, decided the opposite: the top is the place for numbers, and the
    // explanation is reference.
    //
    // The check was not DELETED: the position is still pinned, only at the other
    // end. Deleting it would leave the order free to drift back on its own in the
    // next edit — and the operator’s decision would become an accident.
    { nome: 'SUMMARY · the explanation comes LAST', step: () => {}, expect: () => {
        const el = noteVisible();
        if (!el) return { erro: 'no note' };
        const consumption = Array.from(document.querySelectorAll('div')).filter(visible)
          .find(d => !d.children.length && d.textContent.trim() === 'Usage');
        if (!consumption) return { erro: 'could not find the Usage block to compare against' };
        const after = consumption.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING;
        if (!after) return { erro: 'the explanation went back to BEFORE the numbers' };
        // And it is the LAST thing in the panel: nothing visible about the node comes after it.
        const panel = el.closest('[x-show]') && el.closest('div[class*="rounded"]');
        const actions = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '').indexOf("pvxPower(pvxOpenNode(),'start')") >= 0);
        if (actions && !(actions.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)) {
          return { erro: 'the explanation comes before the ACTIONS: it has to be the last thing on the screen' };
        }
        return { nota: 'numbers, actions and then the explanation' };
      } },

    { nome: 'SUMMARY · a node with NO note says where one is written', step: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '' };
        open('lxc/205', 'summary');
      }, expect: () => {
        if (noteVisible()) return { erro: 'empty note rendering a content block' };
        const t = document.body.innerText;
        if (t.indexOf('Notes') < 0) return { erro: 'does not say WHERE the note comes from, so the operator does not know where to write it' };
        return { nota: 'empty state explained' };
      } },

    { nome: 'SUMMARY · a node outside the PVE explains why there is no note', step: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '' };
        open('canario', 'summary');
      }, expect: () => {
        const t = document.body.innerText;
        if (t.indexOf('not a guest of this hypervisor') < 0)
          return { erro: 'external node with no explanation: "empty" and "does not apply" become the same screen' };
        return { nota: 'absence of a SOURCE told apart from absence of content' };
      } },

    { nome: 'SUMMARY · the HYPERVISOR has a note too', step: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '' };
        open('node/pve', 'summary');
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { erro: 'the hypervisor opened with no explanation at all' };
        if (el.innerText.indexOf('physical machine') < 0) return { erro: 'host note did not arrive' };
        return { nota: 'the host note comes from /nodes/<node>/config' };
      } },

    { nome: 'SUMMARY · hostile text in the note does not become a tag', step: () => {
        C().pvx.nota = { node: 'lxc/204', markdown: '# t\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))',
                         origem: 'pve-notes', motivo: '', loading: false, erro: '' };
        open('lxc/204', 'summary');
        C().pvx.nota.node = 'lxc/204';
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { erro: 'no note' };
        if (el.querySelector('img, script, svg, iframe')) return { erro: '🔴 the note text became a TAG in the DOM' };
        const withEvent = Array.from(el.querySelectorAll('*')).filter(x =>
          Array.from(x.attributes).some(a => /^on/i.test(a.name)));
        if (withEvent.length) return { erro: '🔴 event handler in the DOM' };
        const links = Array.from(el.querySelectorAll('a'));
        if (links.some(a => /^(javascript|data|vbscript):/i.test(a.getAttribute('href') || '')))
          return { erro: '🔴 link with an executable scheme' };
        if (el.innerText.indexOf('onerror') < 0)
          return { erro: 'the text vanished: a filter that ERASES hides the note from the operator' };
        return { nota: 'hostile text shown as letters, without becoming DOM' };
      } },
  );

  // ══ 🔴 "RUNNING" DEMANDS AN OBSERVATION FROM NOW ════════════════════════
  //
  // With the hypervisor unreachable, the panel said "9 / 10 running" about a
  // house it had not seen for nearly an hour. Counting zero would be the opposite
  // lie. The only truthful answer is "I do not know".
  window.__script.push(
    { nome: 'RUNNING · fresh data counts normally', step: () => {
        C().pvxClearSelection();
      }, expect: () => {
        const r = C().pvxLabSummary();
        // The fixture has ONE guest absent on purpose (lxc/101). The others are
        // fresh and have to be counted — demanding "0 unknown" would be demanding
        // that the fixture not reproduce the real case.
        if (r.running < 2) return { erro: 'fresh guests were not counted: ' + JSON.stringify(r) };
        // Two absentees in the fixture: one stale and one FRESH. The fresh one is
        // what proves the absence guard does work of its own.
        if (r.unknown !== 2) return { erro: 'expected 2 unknowns (the two missing ones): ' + JSON.stringify(r) };
        return { nota: r.running + '/' + r.guests + ' running, ' + r.unknown + ' unknown' };
      } },

    { nome: 'RUNNING · a guest with STALE data is not counted as running', step: () => {
        // Exactly the state of the house today: the last known value says
        // "running", but the observation has aged.
        C().nodes.list = C().nodes.list.map(n => Object.assign({}, n, { stale: true, age_seconds: 2760 }));
      }, expect: () => {
        const r = C().pvxLabSummary();
        if (r.running !== 0) return { erro: '🔴 counted ' + r.running + ' running from data nobody observed' };
        if (r.unknown !== r.guests) return { erro: 'unknowns = ' + r.unknown + ', want ' + r.guests };
        const t = document.body.innerText;
        if (t.indexOf('no fresh observation') < 0) return { erro: 'the screen does not declare that it cannot see' };
        if (t.indexOf('0 / ') >= 0) return { erro: '🔴 showed "0 / N": claims they are all off, which nobody observed' };
        return { nota: 'a dash instead of a number, and the reason written' };
      } },

    { nome: 'RUNNING · a node that LEFT the hypervisor does not count as running either', step: () => {
        C().nodes.list = NODES.map(n => n.id === 'lxc/204'
          ? Object.assign({}, n, { ausente_desde: 1787200000 })
          : n);
      }, expect: () => {
        const r = C().pvxLabSummary();
        const lab = C().pvxNodes().find(x => x.id === 'lxc/204');
        if (!lab || !lab.ausente_desde) return { erro: 'the fixture did not mark the node as absent' };
        if (r.unknown < 1) return { erro: 'absent node counted as if it were observable: ' + JSON.stringify(r) };
        // GIVES BACK the original list: a step that dirties the next one is the
        // defect that already broke the console proof this morning.
        C().nodes.list = NODES;
        return { nota: r.running + ' running, ' + r.unknown + ' unknown(s)' };
      } },
  );

  // ══ THE NODE THAT LEFT THE HYPERVISOR ════════════════════════════════════
  window.__script.push(
    { nome: 'GONE · a node that left the hypervisor is not counted as "expired"', step: () => {
        open('node/pve', 'summary');
      }, expect: () => {
        const c = C();
        const n = c.pvxNodes().find(x => x.id === 'lxc/101');
        if (!n) return { erro: 'the fixture lost the absent node' };
        const e = c.pvxNodeState(n);
        if (e !== 'gone') return { erro: 'state = ' + e + ', want "gone": it has an absence stamp' };
        // It is `stale` TOO, and even so it must not fall into "vencido": the
        // order of the checks is what separates "I did not look" from "I looked
        // and did not find".
        if (!n.stale) return { erro: 'the fixture does not reproduce the real case (the absent node is also stale)' };
        const segs = {};
        c.pvxScreenSegments().forEach(x => { segs[x.key] = x.n; });
        if (segs.gone !== 2) return { erro: 'the band does not count the two absent ones: ' + JSON.stringify(segs) };
        // 🔴 The exact property is the ORDER of the checks: the SAME node, without
        // the absence stamp, would fall into "vencido". Asserting "vencido === 0"
        // on the band would be crude — `canario` is legitimately vencido, and the
        // check would fail over a node that has nothing to do with this.
        const noStamp = Object.assign({}, n, { ausente_desde: 0 });
        if (c.pvxNodeState(noStamp) !== 'vencido') {
          return { erro: 'without the stamp it should be "vencido": the fixture does not reproduce the real ambiguity' };
        }
        return { nota: 'with the stamp = gone; without it = vencido: the order is what separates them' };
      } },

    { nome: 'GONE · does not count as an attention item', step: () => {}, expect: () => {
        const c = C();
        const n = c.pvxNodes().find(x => x.id === 'lxc/101');
        // The lab summary's attention count only takes the three pending states;
        // what is asserted here is that "gone" is not one of them.
        const e = c.pvxNodeState(n);
        if (['vencido', 'sem-credencial', 'critico'].indexOf(e) >= 0) {
          return { erro: 'node gone from the hypervisor entered the pending list as ' + e };
        }
        return { nota: 'out of the pending items, as it should be' };
      } },

    { nome: 'GONE · the actions stay locked WITH the right reason', step: () => {
        open('lxc/101', 'summary');
      }, expect: () => {
        const c = C();
        for (const action of ['start', 'shutdown', 'reboot', 'clone', 'backup']) {
          const est = c.pvxActionState(c.pvxOpenNode(), action);
          if (est.can) return { erro: action + ' enabled on a node the hypervisor no longer lists' };
          if (est.motivo.indexOf('no longer lists this node') < 0) {
            return { erro: action + ': reason too generic: ' + JSON.stringify(est.motivo) };
          }
        }
        if (document.body.innerText.indexOf('hypervisor no longer lists this node') < 0) {
          return { erro: 'the screen does not say the node is gone from the hypervisor' };
        }
        return { nota: '5 actions locked, with the reason written on the screen' };
      } },
  );

  // 🔴 "I HAVE NOT READ IT YET" MUST NOT BECOME "IT DOES NOT EXIST" ════════
  //
  // The operator saw on screen "backup: no storage on this hypervisor accepts
  // backups" with `pbs` standing right there on the other side. The storage list
  // simply had not been loaded — and the panel asserted something about the
  // HYPERVISOR out of an absence that was its OWN.
  window.__script.push(
    { nome: 'REASON · unread storage does not become "no storage accepts backups"', step: () => {
        C().pvx.storage = null;
        open('lxc/204', 'summary');
      }, expect: () => {
        const t = document.body.innerText;
        if (t.indexOf('no storage on this hypervisor accepts backups') >= 0) {
          return { erro: '🔴 said the hypervisor has no backup storage without having read the list' };
        }
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (est.can) return { erro: 'enabled backup without knowing where to' };
        if (est.motivo.indexOf('have not read') < 0) return { erro: 'reason = ' + JSON.stringify(est.motivo) };
        return { nota: 'honest reason: ' + est.motivo };
      } },
    { nome: 'REASON · with the list read and NO target, then it really is "no storage"', step: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, expect: () => {
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (est.motivo.indexOf('no storage') < 0) return { erro: 'reason = ' + JSON.stringify(est.motivo) };
        return { nota: 'now the claim is about the hypervisor, and it is true' };
      } },
    { nome: 'REASON · opening Copies loads the list by itself', step: () => {
        C().pvx.storage = null;
        open('lxc/204', 'snaps');
      }, expect: () => {
        if (!C().pvx.storage) return { erro: 'the Copies tab opened without the storage list: the button would lie' };
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (!est.can) return { erro: 'list loaded and backup is still locked: ' + est.motivo };
        return { nota: 'list loaded when the tab opened, button enabled' };
      } },
  );

  // ══ EDITING THE NOTE WITHOUT LEAVING THE PANEL ═══════════════════════════
  const editNoteButton = () => Array.from(document.querySelectorAll('button')).filter(visible)
    .find(b => (b.getAttribute('@click') || '') === 'pvxEditNote()');
  const noteArea = () => Array.from(document.querySelectorAll('textarea')).filter(visible)
    .find(t => (t.getAttribute('aria-label') || '').indexOf('note') >= 0);

  window.__script.push(
    { nome: 'NOTE · the note loads and the edit button appears', step: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '',
                         editing: false, rascunho: '', saving: false };
        open('lxc/204', 'summary');
        // 🔴 THE MUTATION LIVES IN `step`, NEVER IN `expect`. The harness waits for
        // Alpine to re-render AFTER the step; changing state inside the
        // measurement is measuring the DOM from before the change — which is what
        // made this check say "edit mode did not open" about a screen that opens.
      }, expect: () => {
        const b = editNoteButton();
        if (!b) return { erro: 'there is no button to edit the note: the flow is still outside the panel' };
        if (b.off) return { erro: 'edit button locked on a node that has a note' };
        return { nota: 'note loaded and edit button available' };
      } },

    { nome: 'NOTE · editing during the load does NOT open an empty draft', step: () => {
        // Saving an empty draft WOULD ERASE the hypervisor’s note. The guard lives
        // in the function, not only in the markup.
        C().pvx.nota.loading = true;
        C().pvxEditNote();
      }, expect: () => {
        if (C().pvx.nota.editing) return { erro: '🔴 opened the draft while the note was still loading: saving would erase the description' };
        C().pvx.nota.loading = false;
        return { nota: 'refused to open, as it should' };
      } },

    { nome: 'NOTE · editing opens the draft with the text IN FORCE', step: () => {
        C().pvxEditNote();
      }, expect: () => {
        const ta = noteArea();
        if (!ta) return { erro: 'edit mode did not open a text area' };
        if (ta.value.indexOf('What it does') < 0) return { erro: 'the draft did not come with the text in force: ' + ta.value.slice(0, 60) };
        // 🔴 While editing, the rendered body disappears: seeing both at once would
        // make the operator confuse what is saved with what he typed.
        if (noteVisible()) return { erro: 'the rendered text stays on screen during editing' };
        return { nota: 'draft open with ' + ta.value.length + ' characters, rendered body hidden' };
      } },

    { nome: 'NOTE · cancelling gives the original back INTACT', step: () => {
        window.__noteBefore = C().pvx.nota.markdown;
        C().pvx.nota.rascunho = 'threw it all away';
        C().pvxCancelNote();
      }, expect: () => {
        if (C().pvx.nota.markdown !== window.__noteBefore) return { erro: 'cancelling changed the text in force' };
        if (C().pvx.nota.editing) return { erro: 'stayed in edit mode' };
        const el = noteVisible();
        if (!el || el.innerText.indexOf('What it does') < 0) return { erro: 'the original text did not come back to the screen' };
        return { nota: 'original intact, editing closed' };
      } },

    { nome: 'NOTE · text above the ceiling locks the save, and the screen SAYS SO', step: () => {
        C().pvxEditNote();
        C().pvx.nota.rascunho = 'a'.repeat(C().NOTA_MAX + 1);
      }, expect: () => {
        const save = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxSaveNote()');
        if (!save) return { erro: 'no save button' };
        if (!save.disabled) return { erro: 'over the cap and save is still enabled' };
        if (document.body.innerText.indexOf('over the cap') < 0)
          return { erro: 'locked without saying why' };
        return { nota: 'save locked with the reason on screen' };
      } },

    { nome: 'NOTE · a node outside the PVE offers no editing', step: () => {
        C().pvxCancelNote();
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', loading: false, erro: '',
                         editing: false, rascunho: '', saving: false };
        open('canario', 'summary');
      }, expect: () => {
        if (editNoteButton()) return { erro: 'offered to edit the note of a node that has no note in the PVE' };
        return { nota: 'no button, because there is nothing to edit' };
      } },
  );

  // ══ MAINTENANCE: restart, clone and keep a copy ══════════════════════════
  const STORAGES = { pools: [
    { id: 'local', type: 'dir', content: ['backup', 'iso', 'vztmpl'], free: 8e11, total: 9e11, used: 1e10, used_pct: 1 },
    { id: 'local-zfs', type: 'zfspool', content: ['images', 'rootdir'], free: 8e11, total: 9e11, used: 6e10, used_pct: 7 },
    { id: 'pbs', type: 'pbs', content: ['backup'], free: 8.5e11, total: 9e11, used: 4.4e10, used_pct: 5 },
  ] };

  window.__script.push(
    { nome: 'RESTART · running enables, stopped locks, and the reason shows up', step: () => {
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const reb = byAction("pvxPower(pvxOpenNode(),'reboot')");
        if (!reb) return { erro: 'there is no restart button in the node panel' };
        if (reb.off) return { erro: 'RUNNING guest cannot restart: ' + snapshot() };
        return { nota: 'restart enabled on the running guest' };
      } },
    { nome: 'RESTART · a stopped guest offers no restart', step: () => { open('lxc/205', 'summary'); },
      expect: () => {
        const reb = byAction("pvxPower(pvxOpenNode(),'reboot')");
        if (!reb) return { erro: 'button vanished: this screen DISABLES with a reason, it does not hide' };
        if (!reb.off) return { erro: 'STOPPED guest offers restart: the PVE would return an error and the trail would gain noise' };
        const t = document.body.innerText;
        if (t.indexOf('it is powered off') < 0) return { erro: 'locked without the reason written on the screen' };
        return { nota: 'restart locked, with the reason spelled out' };
      } },

    { nome: 'COPIES · the tab exists and clone/backup live in it', step: () => {
        C().pvx.storage = STORAGES;
        open('lxc/204', 'snaps');
        C().pvx.snaps = [];
      }, expect: () => {
        const cl = byAction('pvxOpenClone(pvxOpenNode())');
        const bk = byAction('pvxBackupConfirm(pvxOpenNode())');
        if (!cl) return { erro: 'there is no clone button in the Copies tab' };
        if (!bk) return { erro: 'there is no keep-a-copy button in the Copies tab' };
        if (cl.off) return { erro: 'clone locked on a normal guest' };
        if (bk.off) return { erro: 'keep a copy locked even with a storage that accepts backups' };
        const tabs = Array.from(document.querySelectorAll('.pvx-tab')).filter(visible).map(b => b.textContent.trim());
        if (tabs.indexOf('Copies') < 0) return { erro: 'the tab is not called Copies: ' + tabs.join(' | ') };
        return { nota: 'Copies tab with clone and keep a copy enabled' };
      } },

    { nome: 'COPIES · the backup target is DERIVED, not typed', step: () => {}, expect: () => {
        const sel = Array.from(document.querySelectorAll('select')).filter(visible);
        const dest = sel.find(x => (x.getAttribute('aria-label') || '') === 'destination storage');
        if (!dest) return { erro: 'there is no storage selector' };
        const ops = Array.from(dest.options).map(o => o.value);
        // Only `local` and `pbs` declare `backup` content; `local-zfs` does not.
        if (ops.indexOf('local-zfs') >= 0) return { erro: 'offered a storage that does NOT accept backups: ' + ops.join(',') };
        if (ops.indexOf('pbs') < 0 || ops.indexOf('local') < 0) return { erro: 'valid targets missing: ' + ops.join(',') };
        return { nota: 'targets = ' + ops.join(', ') + ' (local-zfs correctly left out)' };
      } },

    { nome: 'COPIES · with no backup storage, the button locks WITH a reason', step: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, expect: () => {
        const bk = byAction('pvxBackupConfirm(pvxOpenNode())');
        if (!bk) return { erro: 'button vanished' };
        if (!bk.off) return { erro: 'no possible target and the button is still enabled' };
        if (document.body.innerText.indexOf('no storage') < 0) return { erro: 'locked without saying why' };
        return { nota: 'locked and explained' };
      } },

    { nome: 'CLONE · the dialog reads the id from the hypervisor, nobody types it', step: () => {
        C().pvx.storage = STORAGES;
        open('lxc/204', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        if (!C().pvx.clone.open) return { erro: 'the dialog did not open' };
        if (C().pvx.clone.newID !== 991) return { erro: 'newID = ' + C().pvx.clone.newID + ', want 991 (from the hypervisor)' };
        if (C().pvx.clone.nome !== 'lab-copy') return { erro: 'suggested name = ' + C().pvx.clone.nome };
        // The id must NOT be a typeable field: it is a reading.
        const inputs = Array.from(document.querySelectorAll('input')).filter(visible);
        const typableWithID = inputs.filter(i => String(i.value) === '991');
        if (typableWithID.length) return { erro: 'the target id is in a TYPEABLE field: it is read from the hypervisor' };
        if (!document.body.innerText.includes('991')) return { erro: 'the id read does not show up on screen' };
        if (document.body.innerText.indexOf('crash-consistent') < 0)
          return { erro: 'running guest and no crash-consistent copy warning' };
        // 🔴 A running CT requires a source snapshot, and the screen has to OFFER
        // the list instead of letting the operator take the hypervisor’s error.
        const sel = Array.from(document.querySelectorAll('select')).filter(visible)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (!sel) return { erro: 'running CT without a source snapshot selector' };
        const ops = Array.from(sel.options).map(o => o.value);
        if (ops.join(',') !== 'before-upgrade,base') return { erro: 'snapshots offered: ' + ops.join(',') };
        const btn = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxCloneConfirm()');
        if (!btn || btn.disabled) return { erro: 'with a snapshot chosen, "Clone now" is still locked' };
        return { nota: 'id 991 from the hypervisor, suggested name, 2 snapshots offered, button enabled' };
      } },

    { nome: 'CLONE · a running CT with NO snapshot locks and says what to do', step: () => {
        C().pvxCloseClone();
        open('lxc/202', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        const btn = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxCloneConfirm()');
        if (!btn) return { erro: 'no clone button' };
        if (!btn.disabled) return { erro: 'running CT without a snapshot with "Clone now" ENABLED: the hypervisor would refuse' };
        const t = document.body.innerText;
        if (t.indexOf('create one in the section above') < 0 && t.indexOf('has no snapshot') < 0)
          return { erro: 'locked without saying what to do' };
        return { nota: 'locked, with the way out written on the screen' };
      } },

    { nome: 'CLONE · a stopped guest does NOT get the downtime warning', step: () => {
        C().pvxCloseClone();
        open('lxc/205', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        if (C().pvx.clone.ligado) return { erro: 'stopped guest marked as running' };
        const av = Array.from(document.querySelectorAll('div')).filter(d => !d.children.length && d.textContent.indexOf('crash-consistent') >= 0).filter(visible);
        if (av.length) return { erro: 'STOPPED guest getting a warning that only applies to a running one: noise trains people to ignore it' };
        const sel = Array.from(document.querySelectorAll('select')).filter(visible)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (sel) return { erro: 'stopped guest forced to pick a snapshot, a requirement the hypervisor does not make' };
        return { nota: 'no warning and no snapshot requirement, because there is nothing to require' };
      } },

    // 🔴 THE CLASS CHECK OF THIS BATCH: every button has to LOOK like a button.
    { nome: 'STYLE · no button renders as loose text', step: () => {
        C().pvxCloseClone();
        open('lxc/204', 'summary');
      }, expect: () => {
        const invisible = [];
        for (const b of Array.from(document.querySelectorAll('button')).filter(visible)) {
          const cs = getComputedStyle(b);
          const hasBackground = cs.backgroundColor && cs.backgroundColor !== 'rgba(0, 0, 0, 0)' && cs.backgroundColor !== 'transparent';
          const hasBorder = parseFloat(cs.borderTopWidth) > 0 &&
            cs.borderTopColor !== 'rgba(0, 0, 0, 0)' && cs.borderTopColor !== 'transparent';
          if (!hasBackground && !hasBorder) {
            invisible.push((b.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 28) + ' [' + b.className + ']');
          }
        }
        if (invisible.length) {
          return { erro: invisible.length + ' button(s) with NO background or border, rendering as loose text: ' + invisible.join(' ;; ') };
        }
        return { nota: 'every visible button has its own background or border' };
      } },
  );

  // ── sweeps ALL the host and guest tabs, pristine and with data ───────────
  const ABAS_HOST = ['summary', 'charts', 'console', 'tarefas', 'discos', 'storage', 'zfs', 'rede', 'sistema', 'pacotes', 'registry', 'perms'];
  const ABAS_GUEST = ['summary', 'charts', 'console', 'tarefas', 'perms'];
  for (const a of ABAS_HOST) {
    window.__script.push({ nome: 'host · tab ' + a + ' (pristine state)', step: ((ab) => () => {
      putSeries(null); C().pvx.storage = null; C().pvx.zfs = null; C().pvx.perms = null;
      C().pvx.health = null; C().pvx.detail = null;
      open('node/pve', ab);
    })(a) });
  }
  for (const a of ABAS_GUEST) {
    window.__script.push({ nome: 'guest · tab ' + a + ' (pristine state)', step: ((ab) => () => open('lxc/204', ab))(a) });
  }
})();
