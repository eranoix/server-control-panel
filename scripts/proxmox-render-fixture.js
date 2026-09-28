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
    c.installPWA = function () {};
    return c;
  };

  const NOTE_LAB = [
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
    '/api/nodes/lxc/204/note': { node: 'lxc/204', markdown: NOTE_LAB, origin: 'pve-notes' },
    '/api/nodes/lxc/205/note': { node: 'lxc/205', markdown: '', origin: 'empty' },
    '/api/nodes/canary/note': { node: 'canary', markdown: '', origin: 'outside-pve',
                                 reason: 'this node is not a guest of this hypervisor, so the PVE note does not apply to it' },
    '/api/nodes/node/pve/note': { node: 'node/pve', markdown: '# pve: the home server\n\n**What it is:** the physical machine that runs everything.', origin: 'pve-notes' },
    '/api/nodes/lxc/204/clone': { origin: 'lxc/204', origin_name: 'lab', type: 'lxc',
                                  next_id: 991, suggestion: 'lab-copy', on: true,
                                  needs_snapshot: true, snapshots: ['before-upgrade', 'base'] },
    '/api/nodes/lxc/202/clone': { origin: 'lxc/202', origin_name: 'pbs', type: 'lxc',
                                  next_id: 993, suggestion: 'pbs-copy', on: true,
                                  needs_snapshot: true, snapshots: [] },
    '/api/nodes/lxc/205/clone': { origin: 'lxc/205', origin_name: 'observ', type: 'lxc',
                                  next_id: 992, suggestion: 'observ-copy', on: false,
                                  needs_snapshot: false, snapshots: [] },
  };

  const C = () => document.body._x_dataStack[0];
  const visible = (el) => !!el && typeof el.checkVisibility === 'function' &&
    el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });

  const svgs = () => Array.from(document.querySelectorAll('svg[role="img"]')).filter(visible);
  const parts = (sv) => {
    const ps = Array.from(sv.querySelectorAll('path'));
    const c = sv.querySelector('circle');
    return { area: (ps[0] && ps[0].getAttribute('d')) || '',
             stroke: (ps[1] && ps[1].getAttribute('d')) || '',
             points: (ps[2] && ps[2].getAttribute('d')) || '',
             lines: sv.querySelectorAll('line').length,
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
    return { points: points, scope: opts.scope || 'hypervisor', window: 'hour' };
  };

  const stamp = (v) => ({ value: v, observed_at: 1787256260 });
  const guest = (id, name, vmid, st, cred) => ({
    id, name: name, transport: 'pve-api', address: '192.168.1.1', kind: 'guest', vmid,
    status: stamp(st), uptime: stamp(821680), cpu_frac: stamp(0.07), cpu_cores: stamp(4),
    mem_used: stamp(2e9), mem_total: stamp(8e9), mem_host: stamp(-1),
    disk_used: stamp(1e10), disk_total: stamp(5e10),
    net_in: stamp(6e9), net_out: stamp(5e9), disk_read: stamp(2e9), disk_write: stamp(6e8),
    net_in_rate: stamp(-1), net_out_rate: stamp(-1), template: false,
    credential: cred, age_seconds: 1, stale: false,
  });
  const CRED_OK = { token_id: 'panel@pve!node-x', expire: 1802645875, state: 'ok' };
  const CRED_MISSING = { token_id: '', expire: 0, state: 'absent' };
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
      template: false, credential: { token_id: 'panel@pve!audit', expire: 1802645875, state: 'ok' },
      age_seconds: 1, stale: false },
    Object.assign(guest('lxc/101', 'clone-proof', 101, 'stopped', CRED_MISSING),
                  { absent_since: 1787200000, stale: true, age_seconds: 56260 }),
    Object.assign(guest('lxc/102', 'just-deleted', 102, 'running', CRED_OK),
                  { absent_since: 1787256000, stale: false, age_seconds: 30 }),
    { id: 'canary', name: 'canary', transport: 'agent', address: '127.0.0.1:9', kind: 'external', vmid: 0,
      status: stamp(''), uptime: stamp(0), cpu_frac: stamp(0), cpu_cores: stamp(0),
      mem_used: stamp(0), mem_total: stamp(0), mem_host: stamp(0),
      disk_used: stamp(0), disk_total: stamp(0), net_in: stamp(0), net_out: stamp(0),
      disk_read: stamp(0), disk_write: stamp(0), net_in_rate: stamp(0), net_out_rate: stamp(0),
      template: false, credential: CRED_MISSING, age_seconds: -1, stale: true },
  ];

  const putSeries = (d) => {
    window.__responses['/api/proxmox/rrd'] = d;
    C().pvx.series = d;
  };

  const open = (id, tab) => {
    const n = C().pvxNodes().find((x) => x.id === id);
    if (!n) throw new Error('node not in the fixture: ' + id);
    C().pvx.open = '';
    C().pvxSelect(n);
    if (tab !== 'summary') C().pvxGoTo(tab);
  };

  const expectCharts = (howMany, check) => () => {
    const s = svgs();
    if (s.length !== howMany) return { error: 'expected ' + howMany + ' VISIBLE charts, found ' + s.length };
    const p = s.map(parts);
    if (p.some((x) => x.lines !== 3)) return { error: 'grid: ' + p.map((x) => x.lines).join(',') + ' (expected 3 in all)' };
    if (p.some((x) => x.cx === null || isNaN(Number(x.cx)) || isNaN(Number(x.cy))))
      return { error: 'end point without a numeric coordinate: ' + p.map((x) => x.cx + '/' + x.cy).join(' ') };
    const r = check ? check(p) : null;
    return r || { note: howMany + ' charts visible and intact' };
  };

  window.__script = [
    { name: 'the section opens and the hypervisor is chosen', step: () => {
        C().page = 'operations'; C().tabs.operations = 'proxmox';
        C().nodes = C().nodes || {}; C().nodes.list = NODES;
        open('node/pve', 'summary');
      }, expect: () => {
        if (!visible(document.querySelector('section'))) return { error: 'the Proxmox section did not become visible' };
        return { note: 'section visible, pve node open' };
      } },

    { name: 'Charts with NO series — the state the screen ALWAYS opens in', step: () => {
        C().pvx.tab = 'charts';
      }, expect: () => {
        if (svgs().length) return { error: 'with no series it should not draw an SVG' };
        const av = Array.from(document.querySelectorAll('div')).filter((d) => !d.children.length && d.textContent.trim() === 'no sample in this window');
        const v = av.filter(visible);
        if (v.length !== 10) return { error: 'expected 10 VISIBLE "no sample" notices, found ' + v.length + ' of ' + av.length + ' in the DOM' };
        return { note: '10 "no sample" notices: absence declared, not zero' };
      } },

    { name: 'empty series (what a network error leaves behind)', step: () => {
        putSeries({ points: [], scope: '', window: 'hour' });
      }, expect: () => (svgs().length ? { error: 'points:[] should not draw' } : { note: 'nothing drawn, as it should be' }) },

    { name: 'continuous series on the hypervisor — 10 metrics', step: () => {
        putSeries(series(60));
      }, expect: expectCharts(10, (p) => {
        if (p.some((x) => x.stroke.length < 20)) return { error: 'EMPTY stroke in some chart: no error, but nothing drawn either' };
        if (p.some((x) => subs(x.stroke) !== 1)) return { error: 'a continuous series must be ONE subpath: ' + p.map((x) => subs(x.stroke)).join(',') };
        if (p.some((x) => x.area.length < 20)) return { error: 'empty area' };
        return { note: '10 charts · continuous stroke, area, grid and end point' };
      }) },

    { name: 'series with 3 gaps — the line has to BREAK', step: () => {
        putSeries(series(60, { gapAt: [10, 11, 12, 30] }));
      }, expect: expectCharts(10, (p) => {
        const n = p.map((x) => subs(x.stroke));
        if (n.some((k) => k !== 3)) return { error: 'gap did not break the stroke: subpaths = ' + n.join(',') + ' (expected 3)' };
        return { note: 'stroke broken into 3 subpaths' };
      }) },

    { name: 'ISOLATED samples become dots', step: () => {
        putSeries(series(5, { gapAt: [1, 3] }));
      }, expect: expectCharts(10, (p) => {
        const n = p.map((x) => subs(x.points));
        if (n.some((k) => k !== 3)) return { error: 'isolated samples did not become dots: ' + n.join(',') };
        return { note: '3 isolated samples drawn' };
      }) },

    { name: 'a series of just ONE point', step: () => { putSeries(series(1)); },
      expect: expectCharts(10) },

    { name: 'points WITHOUT the metrics (all null)', step: () => {
        putSeries({ points: [{ time: 1787000000 }, { time: 1787000060 }], scope: 'x', window: 'hour' });
      }, expect: () => (svgs().length ? { error: 'a missing metric should not draw' } : { note: 'nothing drawn' }) },

    { name: 'guest opened — switches to the 7 guest metrics', step: () => {
        putSeries(series(30, { scope: 'lxc/204' }));
        open('lxc/204', 'charts');
      }, expect: expectCharts(7, (p) => {
        if (p.some((x) => !x.stroke)) return { error: 'guest with an empty stroke' };
        return { note: '7 guest metrics drawn' };
      }) },

    { name: 'STOPPED guest with the charts open', step: () => { open('lxc/205', 'charts'); },
      expect: expectCharts(7) },

    { name: 'CLEARS the selection with Charts open (yesterday’s crash)', step: () => {
        C().pvx.open = ''; C().pvx.detail = null;
      }, expect: () => (svgs().length ? { error: 'with no node open there are still ' + svgs().length + ' visible chart(s)' } : { note: 'panel closed cleanly' }) },

    { name: 'window change', step: () => { open('node/pve', 'charts'); C().pvx.window = 'day'; },
      expect: expectCharts(10) },

    { name: 'series goes back to NULL (what a badly written catch would do)', step: () => { putSeries(null); },
      expect: () => (svgs().length ? { error: 'a null series should not draw' } : { note: 'nothing drawn, no crash' }) },
  ];

  const buttons = () => Array.from(document.querySelectorAll('button')).filter(visible)
    .map((b) => (b.textContent || '').replace(/\s+/g, ' ').trim()).filter((t) => t);

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
    { name: 'BUTTONS · guest RUNNING — what is impossible stays locked', step: () => {
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const l = NO.turnOn(), d = NO.turnOff(), c = NO.cut(), co = NO.console();
        if (!l || !d || !c || !co) return { error: 'actions missing from the node panel: ' + snapshot() };
        if (!l.off) return { error: 'RUNNING guest offers "turn on", a useless order that pollutes the trail: ' + snapshot() };
        if (d.off || c.off) return { error: 'running guest CANNOT turn off: ' + snapshot() };
        if (co.off) return { error: 'running guest without a console: ' + snapshot() };
        return { note: snapshot() };
      } },
    { name: 'BUTTONS · guest STOPPED — the exact mirror', step: () => { open('lxc/205', 'summary'); },
      expect: () => {
        const l = NO.turnOn(), d = NO.turnOff(), c = NO.cut();
        if (!l) return { error: 'stopped guest without "turnOn": ' + snapshot() };
        if (l.off) return { error: 'STOPPED guest cannot turn on: ' + snapshot() };
        if ((d && !d.off) || (c && !c.off)) return { error: 'STOPPED guest offers turnOff/cut: ' + snapshot() };
        return { note: snapshot() };
      } },
    { name: 'BUTTONS · guest with NO credential — locked AND with the reason on screen', step: () => { open('lxc/202', 'summary'); },
      expect: () => {
        const l = NO.turnOn(), c = NO.cut();
        if (l && !l.off) return { error: 'no credential and "turnOn" enabled: ' + snapshot() };
        if (c && !c.off) return { error: 'no credential and "cut" enabled: ' + snapshot() };
        const t = document.body.innerText.toLowerCase();
        if (t.indexOf('vault') < 0 && t.indexOf('credential') < 0)
          return { error: 'actions locked and the screen does NOT say why: a dead button with no explanation looks like a defect' };
        return { note: snapshot() + ' · reason written on the screen' };
      } },
    { name: 'BUTTONS · hypervisor — the machine’s power exists and is enabled', step: () => { open('node/pve', 'summary'); },
      expect: () => {
        const reb = byAction("pvxHostPower('reboot')"), des = byAction("pvxHostPower('shutdown')");
        if (!reb || !des) return { error: 'hypervisor without restart/turnOff' };
        if (reb.off || des.off) return { error: 'hypervisor power locked' };
        const l = NO.turnOn();
        if (l && !l.off) return { error: 'the hypervisor offers "turnOn guest": ' + snapshot() };
        return { note: 'restart and turnOff enabled; guest power locked' };
      } },
  );

  window.__script.push({
    name: ':disabled · every expression returns a strict boolean',
    step: () => { open('lxc/204', 'summary'); },
    expect: () => {
      const comp = C();
      const targets = Array.from(document.querySelectorAll('[\\:disabled]'));
      if (targets.length < 5) return { error: 'only ' + targets.length + ' elements with :disabled: vacuous' };
      const bad = [];
      for (const el of targets) {
        const expr = el.getAttribute(':disabled');
        let v;
        try { v = Function('c', 'with (c) { return (' + expr + ') }')(comp); }
        catch (e) { bad.push(expr + ' → overflowed: ' + e.message); continue; }
        if (typeof v !== 'boolean') bad.push(expr + ' → ' + JSON.stringify(v) + ' (' + typeof v + '), not boolean');
      }
      if (bad.length) return { error: bad.length + ' non-boolean expression(s): ' + bad.join(' ;; ') };
      return { note: targets.length + ' :disabled expressions, all boolean' };
    },
  });

  const noteVisible = () => Array.from(document.querySelectorAll('.pvx-md')).filter(visible)[0] || null;

  window.__script.push(
    { name: 'SUMMARY · the node’s explanation shows up, rendered', step: () => {
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '' };
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { error: 'the node note is NOT visible in the Summary: the summary does not summarise' };
        const t = el.innerText;
        if (t.indexOf('What it does') < 0) return { error: 'the note body did not arrive: ' + t.slice(0, 80) };
        if (!el.querySelector('h3,h4,h5,h6')) return { error: 'the note title did not become a heading: Markdown was not rendered' };
        if (!el.querySelector('strong')) return { error: 'bold did not render' };
        if (!el.querySelector('code')) return { error: 'code did not render' };
        if (!el.querySelector('ul li')) return { error: 'list did not render' };
        if (!el.querySelector('hr')) return { error: 'rule did not render' };
        if (t.indexOf('**') >= 0 || t.indexOf('##') >= 0)
          return { error: 'Markdown markup leaking as text: ' + t.slice(0, 90) };
        return { note: el.querySelectorAll('h3,h4,h5,h6,strong,code,li,hr').length + ' elements rendered' };
      } },

    { name: 'SUMMARY · the explanation comes LAST', step: () => {}, expect: () => {
        const el = noteVisible();
        if (!el) return { error: 'no note' };
        const consumption = Array.from(document.querySelectorAll('div')).filter(visible)
          .find(d => !d.children.length && d.textContent.trim() === 'Usage');
        if (!consumption) return { error: 'could not find the Usage block to compare against' };
        const after = consumption.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING;
        if (!after) return { error: 'the explanation went back to BEFORE the numbers' };
        const panel = el.closest('[x-show]') && el.closest('div[class*="rounded"]');
        const actions = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '').indexOf("pvxPower(pvxOpenNode(),'start')") >= 0);
        if (actions && !(actions.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)) {
          return { error: 'the explanation comes before the ACTIONS: it has to be the last thing on the screen' };
        }
        return { note: 'numbers, actions and then the explanation' };
      } },

    { name: 'SUMMARY · a node with NO note says where one is written', step: () => {
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '' };
        open('lxc/205', 'summary');
      }, expect: () => {
        if (noteVisible()) return { error: 'empty note rendering a content block' };
        const t = document.body.innerText;
        if (t.indexOf('Notes') < 0) return { error: 'does not say WHERE the note comes from, so the operator does not know where to write it' };
        return { note: 'empty state explained' };
      } },

    { name: 'SUMMARY · a node outside the PVE explains why there is no note', step: () => {
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '' };
        open('canary', 'summary');
      }, expect: () => {
        const t = document.body.innerText;
        if (t.indexOf('not a guest of this hypervisor') < 0)
          return { error: 'external node with no explanation: "empty" and "does not apply" become the same screen' };
        return { note: 'absence of a SOURCE told apart from absence of content' };
      } },

    { name: 'SUMMARY · the HYPERVISOR has a note too', step: () => {
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '' };
        open('node/pve', 'summary');
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { error: 'the hypervisor opened with no explanation at all' };
        if (el.innerText.indexOf('physical machine') < 0) return { error: 'host note did not arrive' };
        return { note: 'the host note comes from /nodes/<node>/config' };
      } },

    { name: 'SUMMARY · hostile text in the note does not become a tag', step: () => {
        C().pvx.note = { node: 'lxc/204', markdown: '# t\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))',
                         origin: 'pve-notes', reason: '', loading: false, error: '' };
        open('lxc/204', 'summary');
        C().pvx.note.node = 'lxc/204';
      }, expect: () => {
        const el = noteVisible();
        if (!el) return { error: 'no note' };
        if (el.querySelector('img, script, svg, iframe')) return { error: '🔴 the note text became a TAG in the DOM' };
        const withEvent = Array.from(el.querySelectorAll('*')).filter(x =>
          Array.from(x.attributes).some(a => /^on/i.test(a.name)));
        if (withEvent.length) return { error: '🔴 event handler in the DOM' };
        const links = Array.from(el.querySelectorAll('a'));
        if (links.some(a => /^(javascript|data|vbscript):/i.test(a.getAttribute('href') || '')))
          return { error: '🔴 link with an executable scheme' };
        if (el.innerText.indexOf('onerror') < 0)
          return { error: 'the text vanished: a filter that ERASES hides the note from the operator' };
        return { note: 'hostile text shown as letters, without becoming DOM' };
      } },
  );

  window.__script.push(
    { name: 'RUNNING · fresh data counts normally', step: () => {
        C().pvxClearSelection();
      }, expect: () => {
        const r = C().pvxLabSummary();
        if (r.running < 2) return { error: 'fresh guests were not counted: ' + JSON.stringify(r) };
        if (r.unknown !== 2) return { error: 'expected 2 unknowns (the two missing ones): ' + JSON.stringify(r) };
        return { note: r.running + '/' + r.guests + ' running, ' + r.unknown + ' unknown' };
      } },

    { name: 'RUNNING · a guest with STALE data is not counted as running', step: () => {
        C().nodes.list = C().nodes.list.map(n => Object.assign({}, n, { stale: true, age_seconds: 2760 }));
      }, expect: () => {
        const r = C().pvxLabSummary();
        if (r.running !== 0) return { error: '🔴 counted ' + r.running + ' running from data nobody observed' };
        if (r.unknown !== r.guests) return { error: 'unknowns = ' + r.unknown + ', want ' + r.guests };
        const t = document.body.innerText;
        if (t.indexOf('no fresh observation') < 0) return { error: 'the screen does not declare that it cannot see' };
        if (t.indexOf('0 / ') >= 0) return { error: '🔴 showed "0 / N": claims they are all off, which nobody observed' };
        return { note: 'a dash instead of a number, and the reason written' };
      } },

    { name: 'RUNNING · a node that LEFT the hypervisor does not count as running either', step: () => {
        C().nodes.list = NODES.map(n => n.id === 'lxc/204'
          ? Object.assign({}, n, { absent_since: 1787200000 })
          : n);
      }, expect: () => {
        const r = C().pvxLabSummary();
        const lab = C().pvxNodes().find(x => x.id === 'lxc/204');
        if (!lab || !lab.absent_since) return { error: 'the fixture did not mark the node as absent' };
        if (r.unknown < 1) return { error: 'absent node counted as if it were observable: ' + JSON.stringify(r) };
        C().nodes.list = NODES;
        return { note: r.running + ' running, ' + r.unknown + ' unknown(s)' };
      } },
  );

  window.__script.push(
    { name: 'GONE · a node that left the hypervisor is not counted as "expired"', step: () => {
        open('node/pve', 'summary');
      }, expect: () => {
        const c = C();
        const n = c.pvxNodes().find(x => x.id === 'lxc/101');
        if (!n) return { error: 'the fixture lost the absent node' };
        const e = c.pvxNodeState(n);
        if (e !== 'gone') return { error: 'state = ' + e + ', want "gone": it has an absence stamp' };
        if (!n.stale) return { error: 'the fixture does not reproduce the real case (the absent node is also stale)' };
        const segs = {};
        c.pvxScreenSegments().forEach(x => { segs[x.key] = x.n; });
        if (segs.gone !== 2) return { error: 'the band does not count the two absent ones: ' + JSON.stringify(segs) };
        const noStamp = Object.assign({}, n, { absent_since: 0 });
        if (c.pvxNodeState(noStamp) !== 'stale') {
          return { error: 'without the stamp it should be "stale": the fixture does not reproduce the real ambiguity' };
        }
        return { note: 'with the stamp = gone; without it = stale: the order is what separates them' };
      } },

    { name: 'GONE · does not count as an attention item', step: () => {}, expect: () => {
        const c = C();
        const n = c.pvxNodes().find(x => x.id === 'lxc/101');
        const e = c.pvxNodeState(n);
        if (['stale', 'no-credential', 'critical'].indexOf(e) >= 0) {
          return { error: 'node gone from the hypervisor entered the pending list as ' + e };
        }
        return { note: 'out of the pending items, as it should be' };
      } },

    { name: 'GONE · the actions stay locked WITH the right reason', step: () => {
        open('lxc/101', 'summary');
      }, expect: () => {
        const c = C();
        for (const action of ['start', 'shutdown', 'reboot', 'clone', 'backup']) {
          const est = c.pvxActionState(c.pvxOpenNode(), action);
          if (est.can) return { error: action + ' enabled on a node the hypervisor no longer lists' };
          if (est.reason.indexOf('no longer lists this node') < 0) {
            return { error: action + ': reason too generic: ' + JSON.stringify(est.reason) };
          }
        }
        if (document.body.innerText.indexOf('hypervisor no longer lists this node') < 0) {
          return { error: 'the screen does not say the node is gone from the hypervisor' };
        }
        return { note: '5 actions locked, with the reason written on the screen' };
      } },
  );

  window.__script.push(
    { name: 'REASON · unread storage does not become "no storage accepts backups"', step: () => {
        C().pvx.storage = null;
        open('lxc/204', 'summary');
      }, expect: () => {
        const t = document.body.innerText;
        if (t.indexOf('no storage on this hypervisor accepts backups') >= 0) {
          return { error: '🔴 said the hypervisor has no backup storage without having read the list' };
        }
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (est.can) return { error: 'enabled backup without knowing where to' };
        if (est.reason.indexOf('have not read') < 0) return { error: 'reason = ' + JSON.stringify(est.reason) };
        return { note: 'honest reason: ' + est.reason };
      } },
    { name: 'REASON · with the list read and NO target, then it really is "no storage"', step: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, expect: () => {
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (est.reason.indexOf('no storage') < 0) return { error: 'reason = ' + JSON.stringify(est.reason) };
        return { note: 'now the claim is about the hypervisor, and it is true' };
      } },
    { name: 'REASON · opening Copies loads the list by itself', step: () => {
        C().pvx.storage = null;
        open('lxc/204', 'snaps');
      }, expect: () => {
        if (!C().pvx.storage) return { error: 'the Copies tab opened without the storage list: the button would lie' };
        const est = C().pvxActionState(C().pvxOpenNode(), 'backup');
        if (!est.can) return { error: 'list loaded and backup is still locked: ' + est.reason };
        return { note: 'list loaded when the tab opened, button enabled' };
      } },
  );

  const editNoteButton = () => Array.from(document.querySelectorAll('button')).filter(visible)
    .find(b => (b.getAttribute('@click') || '') === 'pvxEditNote()');
  const noteArea = () => Array.from(document.querySelectorAll('textarea')).filter(visible)
    .find(t => (t.getAttribute('aria-label') || '').indexOf('note') >= 0);

  window.__script.push(
    { name: 'NOTE · the note loads and the edit button appears', step: () => {
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '',
                         editing: false, draft: '', saving: false };
        open('lxc/204', 'summary');
      }, expect: () => {
        const b = editNoteButton();
        if (!b) return { error: 'there is no button to edit the note: the flow is still outside the panel' };
        if (b.off) return { error: 'edit button locked on a node that has a note' };
        return { note: 'note loaded and edit button available' };
      } },

    { name: 'NOTE · editing during the load does NOT open an empty draft', step: () => {
        C().pvx.note.loading = true;
        C().pvxEditNote();
      }, expect: () => {
        if (C().pvx.note.editing) return { error: '🔴 opened the draft while the note was still loading: saving would erase the description' };
        C().pvx.note.loading = false;
        return { note: 'refused to open, as it should' };
      } },

    { name: 'NOTE · editing opens the draft with the text IN FORCE', step: () => {
        C().pvxEditNote();
      }, expect: () => {
        const ta = noteArea();
        if (!ta) return { error: 'edit mode did not open a text area' };
        if (ta.value.indexOf('What it does') < 0) return { error: 'the draft did not come with the text in force: ' + ta.value.slice(0, 60) };
        if (noteVisible()) return { error: 'the rendered text stays on screen during editing' };
        return { note: 'draft open with ' + ta.value.length + ' characters, rendered body hidden' };
      } },

    { name: 'NOTE · cancelling gives the original back INTACT', step: () => {
        window.__noteBefore = C().pvx.note.markdown;
        C().pvx.note.draft = 'threw it all away';
        C().pvxCancelNote();
      }, expect: () => {
        if (C().pvx.note.markdown !== window.__noteBefore) return { error: 'cancelling changed the text in force' };
        if (C().pvx.note.editing) return { error: 'stayed in edit mode' };
        const el = noteVisible();
        if (!el || el.innerText.indexOf('What it does') < 0) return { error: 'the original text did not come back to the screen' };
        return { note: 'original intact, editing closed' };
      } },

    { name: 'NOTE · text above the ceiling locks the save, and the screen SAYS SO', step: () => {
        C().pvxEditNote();
        C().pvx.note.draft = 'a'.repeat(C().NOTE_MAX + 1);
      }, expect: () => {
        const save = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxSaveNote()');
        if (!save) return { error: 'no save button' };
        if (!save.disabled) return { error: 'over the cap and save is still enabled' };
        if (document.body.innerText.indexOf('over the cap') < 0)
          return { error: 'locked without saying why' };
        return { note: 'save locked with the reason on screen' };
      } },

    { name: 'NOTE · a node outside the PVE offers no editing', step: () => {
        C().pvxCancelNote();
        C().pvx.note = { node: '', markdown: '', origin: '', reason: '', loading: false, error: '',
                         editing: false, draft: '', saving: false };
        open('canary', 'summary');
      }, expect: () => {
        if (editNoteButton()) return { error: 'offered to edit the note of a node that has no note in the PVE' };
        return { note: 'no button, because there is nothing to edit' };
      } },
  );

  const STORAGES = { pools: [
    { id: 'local', type: 'dir', content: ['backup', 'iso', 'vztmpl'], free: 8e11, total: 9e11, used: 1e10, used_pct: 1 },
    { id: 'local-zfs', type: 'zfspool', content: ['images', 'rootdir'], free: 8e11, total: 9e11, used: 6e10, used_pct: 7 },
    { id: 'pbs', type: 'pbs', content: ['backup'], free: 8.5e11, total: 9e11, used: 4.4e10, used_pct: 5 },
  ] };

  window.__script.push(
    { name: 'RESTART · running enables, stopped locks, and the reason shows up', step: () => {
        open('lxc/204', 'summary');
        C().pvx.health = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, expect: () => {
        const reb = byAction("pvxPower(pvxOpenNode(),'reboot')");
        if (!reb) return { error: 'there is no restart button in the node panel' };
        if (reb.off) return { error: 'RUNNING guest cannot restart: ' + snapshot() };
        return { note: 'restart enabled on the running guest' };
      } },
    { name: 'RESTART · a stopped guest offers no restart', step: () => { open('lxc/205', 'summary'); },
      expect: () => {
        const reb = byAction("pvxPower(pvxOpenNode(),'reboot')");
        if (!reb) return { error: 'button vanished: this screen DISABLES with a reason, it does not hide' };
        if (!reb.off) return { error: 'STOPPED guest offers restart: the PVE would return an error and the trail would gain noise' };
        const t = document.body.innerText;
        if (t.indexOf('it is powered off') < 0) return { error: 'locked without the reason written on the screen' };
        return { note: 'restart locked, with the reason spelled out' };
      } },

    { name: 'COPIES · the tab exists and clone/backup live in it', step: () => {
        C().pvx.storage = STORAGES;
        open('lxc/204', 'snaps');
        C().pvx.snaps = [];
      }, expect: () => {
        const cl = byAction('pvxOpenClone(pvxOpenNode())');
        const bk = byAction('pvxBackupConfirm(pvxOpenNode())');
        if (!cl) return { error: 'there is no clone button in the Copies tab' };
        if (!bk) return { error: 'there is no keep-a-copy button in the Copies tab' };
        if (cl.off) return { error: 'clone locked on a normal guest' };
        if (bk.off) return { error: 'keep a copy locked even with a storage that accepts backups' };
        const tabs = Array.from(document.querySelectorAll('.pvx-tab')).filter(visible).map(b => b.textContent.trim());
        if (tabs.indexOf('Copies') < 0) return { error: 'the tab is not called Copies: ' + tabs.join(' | ') };
        return { note: 'Copies tab with clone and keep a copy enabled' };
      } },

    { name: 'COPIES · the backup target is DERIVED, not typed', step: () => {}, expect: () => {
        const sel = Array.from(document.querySelectorAll('select')).filter(visible);
        const dest = sel.find(x => (x.getAttribute('aria-label') || '') === 'destination storage');
        if (!dest) return { error: 'there is no storage selector' };
        const ops = Array.from(dest.options).map(o => o.value);
        if (ops.indexOf('local-zfs') >= 0) return { error: 'offered a storage that does NOT accept backups: ' + ops.join(',') };
        if (ops.indexOf('pbs') < 0 || ops.indexOf('local') < 0) return { error: 'valid targets missing: ' + ops.join(',') };
        return { note: 'targets = ' + ops.join(', ') + ' (local-zfs correctly left out)' };
      } },

    { name: 'COPIES · with no backup storage, the button locks WITH a reason', step: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, expect: () => {
        const bk = byAction('pvxBackupConfirm(pvxOpenNode())');
        if (!bk) return { error: 'button vanished' };
        if (!bk.off) return { error: 'no possible target and the button is still enabled' };
        if (document.body.innerText.indexOf('no storage') < 0) return { error: 'locked without saying why' };
        return { note: 'locked and explained' };
      } },

    { name: 'CLONE · the dialog reads the id from the hypervisor, nobody types it', step: () => {
        C().pvx.storage = STORAGES;
        open('lxc/204', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        if (!C().pvx.clone.open) return { error: 'the dialog did not open' };
        if (C().pvx.clone.newID !== 991) return { error: 'newID = ' + C().pvx.clone.newID + ', want 991 (from the hypervisor)' };
        if (C().pvx.clone.name !== 'lab-copy') return { error: 'suggested name = ' + C().pvx.clone.name };
        const inputs = Array.from(document.querySelectorAll('input')).filter(visible);
        const typableWithID = inputs.filter(i => String(i.value) === '991');
        if (typableWithID.length) return { error: 'the target id is in a TYPEABLE field: it is read from the hypervisor' };
        if (!document.body.innerText.includes('991')) return { error: 'the id read does not show up on screen' };
        if (document.body.innerText.indexOf('crash-consistent') < 0)
          return { error: 'running guest and no crash-consistent copy warning' };
        const sel = Array.from(document.querySelectorAll('select')).filter(visible)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (!sel) return { error: 'running CT without a source snapshot selector' };
        const ops = Array.from(sel.options).map(o => o.value);
        if (ops.join(',') !== 'before-upgrade,base') return { error: 'snapshots offered: ' + ops.join(',') };
        const btn = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxCloneConfirm()');
        if (!btn || btn.disabled) return { error: 'with a snapshot chosen, "Clone now" is still locked' };
        return { note: 'id 991 from the hypervisor, suggested name, 2 snapshots offered, button enabled' };
      } },

    { name: 'CLONE · a running CT with NO snapshot locks and says what to do', step: () => {
        C().pvxCloseClone();
        open('lxc/202', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        const btn = Array.from(document.querySelectorAll('button')).filter(visible)
          .find(b => (b.getAttribute('@click') || '') === 'pvxCloneConfirm()');
        if (!btn) return { error: 'no clone button' };
        if (!btn.disabled) return { error: 'running CT without a snapshot with "Clone now" ENABLED: the hypervisor would refuse' };
        const t = document.body.innerText;
        if (t.indexOf('create one in the section above') < 0 && t.indexOf('has no snapshot') < 0)
          return { error: 'locked without saying what to do' };
        return { note: 'locked, with the way out written on the screen' };
      } },

    { name: 'CLONE · a stopped guest does NOT get the downtime warning', step: () => {
        C().pvxCloseClone();
        open('lxc/205', 'snaps');
        C().pvxOpenClone(C().pvxOpenNode());
      }, expect: () => {
        if (C().pvx.clone.on) return { error: 'stopped guest marked as running' };
        const av = Array.from(document.querySelectorAll('div')).filter(d => !d.children.length && d.textContent.indexOf('crash-consistent') >= 0).filter(visible);
        if (av.length) return { error: 'STOPPED guest getting a warning that only applies to a running one: noise trains people to ignore it' };
        const sel = Array.from(document.querySelectorAll('select')).filter(visible)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (sel) return { error: 'stopped guest forced to pick a snapshot, a requirement the hypervisor does not make' };
        return { note: 'no warning and no snapshot requirement, because there is nothing to require' };
      } },

    { name: 'STYLE · no button renders as loose text', step: () => {
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
          return { error: invisible.length + ' button(s) with NO background or border, rendering as loose text: ' + invisible.join(' ;; ') };
        }
        return { note: 'every visible button has its own background or border' };
      } },
  );

  const TABS_HOST = ['summary', 'charts', 'console', 'tasks', 'disks', 'storage', 'zfs', 'network', 'system', 'packages', 'registry', 'perms'];
  const TABS_GUEST = ['summary', 'charts', 'console', 'tasks', 'perms'];
  for (const a of TABS_HOST) {
    window.__script.push({ name: 'host · tab ' + a + ' (pristine state)', step: ((ab) => () => {
      putSeries(null); C().pvx.storage = null; C().pvx.zfs = null; C().pvx.perms = null;
      C().pvx.health = null; C().pvx.detail = null;
      open('node/pve', ab);
    })(a) });
  }
  for (const a of TABS_GUEST) {
    window.__script.push({ name: 'guest · tab ' + a + ' (pristine state)', step: ((ab) => () => open('lxc/204', ab))(a) });
  }
})();
