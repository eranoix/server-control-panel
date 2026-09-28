(function () {
  const hysteresisTiers = new Map();

  function pvxGaugePct(n, which) {
    if (!n) return null;
    const val = (o) => (o && o.value !== undefined && o.value !== null) ? Number(o.value) : null;
    const stamped = (o) => !!(o && o.observed_at > 0);
    if (which === 'cpu') {
      if (!stamped(n.cpu_frac)) return null;
      const f = val(n.cpu_frac);
      return (f === null || f < 0) ? null : f * 100;
    }
    const par = which === 'ram' ? [n.mem_used, n.mem_total] : [n.disk_used, n.disk_total];
    if (!stamped(par[0])) return null;
    const u = val(par[0]), t = val(par[1]);
    if (u === null || u < 0 || t === null || t <= 0) return null;
    return (u / t) * 100;
  }

  function pvxNoMeasureReason(n, which) {
    if (!n) return 'no data';
    const stamp = which === 'cpu' ? n.cpu_frac : (which === 'ram' ? n.mem_used : n.disk_used);
    if (!stamp || !stamp.observed_at) return 'the panel has not observed this node yet';
    if (which === 'disco') {
      return 'not reported — QEMU without a guest agent does not report disk usage';
    }
    return 'not reported';
  }

  function pvxGaugeText(comp, n, which, pct) {
    if (which === 'cpu') {
      const cores = (n.cpu_cores && n.cpu_cores.value) || 0;
      if (!cores) return 'cores not reported';
      return cores === 1 ? '1 core' : `${cores} cores`;
    }
    const par = which === 'ram' ? [n.mem_used, n.mem_total] : [n.disk_used, n.disk_total];
    return comp.pvxBytes(par[0] && par[0].value) + ' / ' + comp.pvxBytes(par[1] && par[1].value);
  }

  window.PanelProxmoxModule = function () {
    return {
      pvx: {
        health: null,
        ttl: 90,
        tasks: [],
        errorsOnly: true,
        taskLog: null,
        logOpen: false,
        disks: [],
        storage: null,
        zfs: null,
        perms: null,
        permsOpen: false,
        snaps: [],
        newSnap: '',
        guestSel: '',
        loading: false,
        firstLoad: true,
        forbidden: false,
        lastError: '',
        busy: '',

        filter: '',
        segment: '',
        sel: [],
        open: '',
        detail: null,
        rolando: false,

        con: {
          guest: '',
          state: 'closed',
          error: '',
        },

        tab: '',
        paused: false,
        pauseReason: '',
        focusFilter: false,
        ageSec: null,
        loaded: {},

        clone: { open: false, loading: false, origin: '', originName: '', newID: 0, name: '', on: false,
                 needsSnap: false, snapshots: [], snapshot: '', error: '' },
        bkp: { storage: '', mode: 'snapshot' },
        note: { node: '', markdown: '', origin: '', reason: '', loading: false, error: '',
                editing: false, draft: '', saving: false },

        backup: { datastores: [] },
        topology: { pools: [] },
        series: { points: [], scope: '', window: '' },
        seriesLoading: false,
        window: 'hour',
        system: {},
        packages: { packages: [] },
        registry: { lines: [] },
        packageFilter: '',
      },

      pvxRunningGuests() {
        return this.pvxNodes().filter((n) => {
          if (!this.pvxIsGuest(n)) return false;
          const st = (n.status && n.status.value) || '';
          return st === 'running' || st === 'online';
        });
      },
      pvxHostPower(cmd) {
        const n = this.pvxOpenNode();
        if (!n || !this.pvxIsHost(n)) return;
        const label = n.name || n.id;
        const running = this.pvxRunningGuests();
        const list = running.map((g) => (g.name || g.id)).join(', ') || 'none';

        const title = cmd === 'reboot' ? 'Restart the hypervisor' : 'POWER OFF the hypervisor';
        const volta = cmd === 'reboot'
          ? 'If it does not come back — a new kernel that fails to boot, a pool that fails to import, a stuck fsck — the outcome is the same as a power off: somebody has to be standing in front of the machine.'
          : 'It will NOT come back on its own. There is no IPMI, and Wake-on-LAN is useless because the machine itself routes the admin network. Powering it back on means walking up to it.';

        this.askConfirm(title,
          `${running.length} running guest(s) will go down with it: ${list}.\n\n` +
          `${volta}\n\n` +
          `Once sent, the panel loses contact with "${label}" and there is no undo from here. ` +
          `Type the hypervisor name (${label}) to confirm.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = n.id;
            this.pvx.lastError = '';
            try {
              const d = await (await this.api('/api/proxmox/power?command=' + encodeURIComponent(cmd),
                { method: 'POST' })).json().catch(() => ({}));
              const howMany = (d.guests_affected || []).length;
              this.showToast(
                (cmd === 'reboot' ? 'restart' : 'power off') + ' accepted by the hypervisor' +
                (howMany ? ` — ${howMany} guest(s) going down with it` : '') +
                (d.upid ? ` (task ${d.upid})` : ''), 'ok');
              this.pvx.paused = true;
              this.pvx.pauseReason = 'hypervisor ' + (cmd === 'reboot' ? 'restarting' : 'powering off');
              this.pvxStopTimer();
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('the hypervisor refused the command: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
            }
          },
          { danger: true, requireText: label });
      },

      pvxHealthObs(field) {
        const h = this.pvx.health;
        return h ? h[field] : null;
      },
      pvxVal(field) {
        const o = this.pvxHealthObs(field);
        return o && o.observed_at ? o.value : null;
      },
      pvxNum(field, decimals) {
        const v = this.pvxVal(field);
        return v === null || v === undefined ? '—' : Number(v).toFixed(decimals === undefined ? 1 : decimals);
      },
      pvxPctOf(usedField, totalField) {
        const u = this.pvxVal(usedField), t = this.pvxVal(totalField);
        if (u === null || t === null || !(Number(t) > 0)) return null;
        return (Number(u) / Number(t)) * 100;
      },
      pvxFracPct(field) {
        const v = this.pvxVal(field);
        return v === null || v === undefined ? null : Number(v) * 100;
      },
      pvxBar(pct, cap) {
        if (pct === null) return 'width:0%;background:#64748b';
        const v = Math.max(0, Math.min(100, pct));
        const t = cap === undefined ? 85 : cap;
        const c = v >= t ? '#ef4444' : v >= t * 0.8 ? '#f59e0b' : '#22c55e';
        return `width:${v}%;background:${c}`;
      },
      pvxLongUptime() {
        const s2 = this.pvxVal('uptime');
        if (s2 === null) return '—';
        const d = Math.floor(s2 / 86400), h = Math.floor((s2 % 86400) / 3600), m = Math.floor((s2 % 3600) / 60);
        return d > 0 ? `${d}d ${h}h ${m}min` : `${h}h ${m}min`;
      },
      pvxLoadTrio() {
        const l = this.pvxVal('load');
        return Array.isArray(l) && l.length === 3 ? l.map((x) => Number(x).toFixed(2)) : null;
      },
      pvxLoadRelative() {
        const l = this.pvxLoadTrio(), n = this.pvxVal('cpu_cores');
        if (!l || !(Number(n) > 0)) return null;
        return (Number(l[0]) / Number(n)) * 100;
      },

      WINDOWS: [
        { id: 'hour',  rot: '1 h' },
        { id: 'day',   rot: '1 day' },
        { id: 'week',  rot: '1 week' },
        { id: 'month', rot: '1 month' },
        { id: 'year',  rot: '1 year' },
      ],
      NODE_METRICS: [
        { id: 'cpu',        rot: 'CPU',        pct: true,  color: '#38bdf8', fmt: 'pct' },
        { id: 'iowait',     rot: 'IO delay',   pct: true,  color: '#f59e0b', fmt: 'pct' },
        { id: 'loadavg',    rot: 'Load',       pct: false, color: '#a78bfa', fmt: 'num' },
        { id: 'memused',    rot: 'Memory',    pct: false, color: '#22c55e', fmt: 'bytes', cap: 'memtotal' },
        { id: 'arcsize',    rot: 'ZFS ARC', pct: false, color: '#2dd4bf', fmt: 'bytes', cap: 'memtotal' },
        { id: 'rootused',   rot: 'Disk /',    pct: false, color: '#94a3b8', fmt: 'bytes', cap: 'roottotal' },
        { id: 'netin',      rot: 'Network ↓',     pct: false, color: '#38bdf8', fmt: 'rate' },
        { id: 'netout',     rot: 'Network ↑',     pct: false, color: '#f472b6', fmt: 'rate' },
        { id: 'pressureiosome',     rot: 'IO pressure',     pct: true, color: '#fb923c', fmt: 'pct' },
        { id: 'pressurememorysome', rot: 'Memory pressure', pct: true, color: '#e879f9', fmt: 'pct' },
      ],
      GUEST_METRICS: [
        { id: 'cpu',       rot: 'CPU',      pct: true,  color: '#38bdf8', fmt: 'pct' },
        { id: 'mem',       rot: 'Memory',  pct: false, color: '#22c55e', fmt: 'bytes', cap: 'maxmem' },
        { id: 'disk',      rot: 'Disk',    pct: false, color: '#94a3b8', fmt: 'bytes', cap: 'maxdisk' },
        { id: 'netin',     rot: 'Network ↓',   pct: false, color: '#38bdf8', fmt: 'rate' },
        { id: 'netout',    rot: 'Network ↑',   pct: false, color: '#f472b6', fmt: 'rate' },
        { id: 'diskread',  rot: 'Read',  pct: false, color: '#a78bfa', fmt: 'rate' },
        { id: 'diskwrite', rot: 'Write',  pct: false, color: '#fb923c', fmt: 'rate' },
      ],
      pvxMetrics() {
        const n = this.pvxOpenNode();
        return this.pvxIsGuest(n) ? this.GUEST_METRICS : this.NODE_METRICS;
      },
      pvxSeriesTarget() {
        const n = this.pvxOpenNode();
        return this.pvxIsGuest(n) ? n.id : '';
      },
      async pvxLoadSeries() {
        const target = this.pvxSeriesTarget();
        const q = '?window=' + encodeURIComponent(this.pvx.window) + (target ? '&node=' + encodeURIComponent(target) : '');
        this.pvx.seriesLoading = true;
        try {
          const r = await this.api('/api/proxmox/rrd' + q, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.series = await r.json();
          this.pvx.loaded.series = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.series = { points: [], scope: '', window: this.pvx.window };
        } finally {
          this.pvx.seriesLoading = false;
        }
      },
      pvxSwitchWindow(j) { this.pvx.window = j; this.pvxLoadSeries(); },

      GW: 600, GH: 120,
      pvxDrawing(metric) {
        const d = this.pvx.series;
        const empty = {
          hasData: false, area: '', stroke: '', points: '',
          cap: 1, max: 0, min: 0, last: null, endX: 0, endY: 0, gaps: 0, n: 0,
        };
        if (!d || !Array.isArray(d.points) || !d.points.length) return empty;
        const pts = d.points;
        const vals = pts.map((p) => {
          const v = p[metric.id];
          return v === undefined || v === null || Number.isNaN(Number(v)) ? null : Number(v);
        });
        const present = vals.filter((v) => v !== null);
        if (!present.length) return empty;

        const t0 = Number(pts[0].time), t1 = Number(pts[pts.length - 1].time);
        const dur = Math.max(1, t1 - t0);
        let cap;
        if (metric.pct) {
          cap = 1;
        } else {
          cap = Math.max(...present);
          if (metric.cap) {
            const caps = pts.map((p) => Number(p[metric.cap])).filter((v) => v > 0);
            if (caps.length) cap = Math.max(cap, Math.max(...caps));
          }
          if (!(cap > 0)) cap = 1;
        }
        const X = (t) => ((Number(t) - t0) / dur) * this.GW;
        const Y = (v) => this.GH - Math.min(1, Math.max(0, v / cap)) * this.GH;

        const segments = [];
        let current = [];
        let gaps = 0;
        for (let i = 0; i < pts.length; i++) {
          if (vals[i] === null) {
            if (current.length) { segments.push(current); current = []; gaps++; }
            continue;
          }
          current.push([X(pts[i].time), Y(vals[i])]);
        }
        if (current.length) segments.push(current);

        const dPath = (seg) => seg.map(([x, y], i) => (i ? 'L' : 'M') + x.toFixed(1) + ' ' + y.toFixed(1)).join(' ');
        const area = segments
          .filter((s) => s.length > 1)
          .map((s) => dPath(s) + ` L${s[s.length - 1][0].toFixed(1)} ${this.GH} L${s[0][0].toFixed(1)} ${this.GH} Z`)
          .join(' ');

        const lastIdx = vals.map((v, i) => (v === null ? -1 : i)).filter((i) => i >= 0).pop();
        const stroke = segments.filter((s) => s.length > 1).map(dPath).join(' ');
        const points = segments
          .filter((s) => s.length === 1)
          .map((s) => `M${s[0][0].toFixed(1)} ${s[0][1].toFixed(1)} L${s[0][0].toFixed(1)} ${s[0][1].toFixed(1)}`)
          .join(' ');
        return {
          hasData: true,
          stroke,
          points: points,
          area,
          cap,
          max: Math.max(...present),
          min: Math.min(...present),
          last: vals[lastIdx],
          endX: X(pts[lastIdx].time),
          endY: Y(vals[lastIdx]),
          gaps,
          n: present.length,
        };
      },
      pvxMetricValue(metric, v) {
        if (v === null || v === undefined) return '—';
        switch (metric.fmt) {
          case 'pct':   return (Number(v) * 100).toFixed(1) + '%';
          case 'bytes': return this.pvxBytes(Number(v));
          case 'rate':  return this.pvxBytes(Number(v)) + '/s';
          default:      return Number(v).toFixed(2);
        }
      },
      pvxWindowLabel() {
        const j = this.WINDOWS.find((x) => x.id === this.pvx.window);
        return j ? j.rot : this.pvx.window;
      },
      GRADE: [0.25, 0.5, 0.75],

      pvxTopologyOf(name) {
        const ps = (this.pvx.topology && this.pvx.topology.pools) || [];
        return ps.find((p) => p.name === name) || null;
      },
      pvxSerialOfPath(path) {
        let base = String(path || '');
        const i = base.lastIndexOf('/');
        if (i >= 0) base = base.slice(i + 1);
        base = base.replace(/-part\d+$/, '').replace(/-0:0$/, '');
        if (base.startsWith('nvme-eui.')) return '';
        const j = base.lastIndexOf('_');
        return j >= 0 ? base.slice(j + 1) : '';
      },
      pvxDiskPools(d) {
        if (!d) return [];
        const serial = String(d.serial || '');
        const ps = (this.pvx.topology && this.pvx.topology.pools) || [];
        const findings = [];
        for (const p of ps) {
          for (const v of p.vdevs || []) {
            for (const disp of v.devices || []) {
              const s = this.pvxSerialOfPath(disp.path);
              if (s && serial && s === serial && !findings.includes(p.name)) findings.push(p.name);
            }
          }
        }
        return findings;
      },
      pvxLifespan(d) {
        if (!d || d.wearout_pct === null || d.wearout_pct === undefined) {
          return { measured: false, text: 'not reported', reason: 'this disk exposes no wear indicator (common on spinning disks and USB enclosures)' };
        }
        const pct = Number(d.wearout_pct);
        return {
          measured: true, pct,
          text: pct.toFixed(0) + '% of life left',
          reason: 'SMART: ' + (100 - pct).toFixed(0) + '% of the endurance already used',
        };
      },
      pvxLifeStyle(d) {
        const v = this.pvxLifespan(d);
        if (!v.measured) return 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66';
        const c = v.pct <= 10 ? '#ef4444' : v.pct <= 25 ? '#f59e0b' : '#22c55e';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxHealthStyle(h) {
        const c = h === 'PASSED' || h === 'OK' ? '#22c55e' : (!h || h === 'UNKNOWN') ? '#64748b' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxRedundancy(namePool) {
        const t = this.pvxTopologyOf(namePool);
        if (!t) return { known: false, text: 'topology not read', style: 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66' };
        if (t.redundant) {
          const types = [...new Set((t.vdevs || []).filter((v) => v.redundant).map((v) => v.type))];
          return { known: true, isProtected: true, text: types.join(' + ') + ' · survives the loss of one disk',
                   style: 'background:#22c55e22;color:#22c55e;border:1px solid #22c55e66' };
        }
        return { known: true, isProtected: false,
                 text: t.n_devices === 1 ? 'single disk · NO redundancy' : t.n_devices + ' striped disks · NO redundancy',
                 style: 'background:#f59e0b22;color:#f59e0b;border:1px solid #f59e0b66' };
      },
      pvxPoolErrors(namePool) {
        const t = this.pvxTopologyOf(namePool);
        if (!t) return { known: false };
        return { known: true, n: t.errors_counted || 0, text: t.errors || '' };
      },
      pvxPoolDevices(namePool) {
        const t = this.pvxTopologyOf(namePool);
        if (!t) return [];
        return (t.vdevs || []).flatMap((v) => (v.devices || []).map((d) => ({ ...d, vdev: v.name, type: v.type })));
      },
      async pvxLoad(route, field, empty) {
        try {
          const r = await this.api(route, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx[field] = await r.json();
          this.pvx.loaded[field] = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx[field] = empty;
        }
      },
      pvxLoadSystem()  { return this.pvxLoad('/api/proxmox/system', 'system', {}); },
      pvxLoadPackages()  { return this.pvxLoad('/api/proxmox/packages', 'packages', { packages: [] }); },
      pvxLoadRegistry() { return this.pvxLoad('/api/proxmox/syslog?limit=300', 'registry', { lines: [] }); },

      pvxInterfaces() { return (this.pvx.system && this.pvx.system.network) || []; },
      pvxDNS()        { return (this.pvx.system && this.pvx.system.dns) || null; },
      pvxTime()       { return (this.pvx.system && this.pvx.system.time) || null; },
      pvxCertificates(){ return (this.pvx.system && this.pvx.system.certificates) || []; },
      pvxNodeTimezone() {
        const t = this.pvxTime();
        return t ? (t.timezone || 'unknown') : '—';
      },
      pvxCertDays(c) {
        const d = this.pvx.system;
        if (!c || !c.notafter || !d) return null;
        return Math.floor((Number(c.notafter) - Number(d.observed_at)) / 86400);
      },
      pvxCertStyle(c) {
        const days = this.pvxCertDays(c);
        if (days === null) return 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66';
        const color = days <= 7 ? '#ef4444' : days <= 30 ? '#f59e0b' : '#22c55e';
        return `background:${color}22;color:${color};border:1px solid ${color}66`;
      },
      pvxFilteredPackages() {
        const ps = (this.pvx.packages && this.pvx.packages.packages) || [];
        const t = (this.pvx.packageFilter || '').trim().toLowerCase();
        if (!t) return ps;
        return ps.filter((p) => (p.Package || '').toLowerCase().includes(t)
                             || (p.Version || '').toLowerCase().includes(t));
      },
      pvxRegistryRows() { return (this.pvx.registry && this.pvx.registry.lines) || []; },
      pvxLogLineStyle(t) {
        const s2 = String(t || '');
        if (/\b(error|error|failed|failure|fatal|panic|refused|denied)\b/i.test(s2)) return 'color:#ef4444';
        if (/\b(warn|warning|warning|degraded|timeout)\b/i.test(s2)) return 'color:#f59e0b';
        return '';
      },

      async pvxLoadTopology() {
        try {
          const r = await this.api('/api/proxmox/zfs/topology', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.topology = await r.json();
          this.pvx.loaded.topology = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.topology = { pools: [] };
        }
      },

      TYPES: {
        node:    { abbrev: 'NODE',  label: 'hypervisor',        color: '#94a3b8' },
        lxc:     { abbrev: 'CT',  label: 'LXC container',     color: '#38bdf8' },
        qemu:    { abbrev: 'VM',  label: 'virtual machine',   color: '#a78bfa' },
        external: { abbrev: 'EXT', label: 'outside the hypervisor', color: '#2dd4bf' },
        '?':     { abbrev: '?',   label: 'unknown type', color: '#a1887f' },
      },
      pvxTypeKey(n) {
        if (!n) return '?';
        const pre = String(n.id || '').split('/')[0];
        if (pre === 'lxc' || pre === 'qemu' || pre === 'node') return pre;
        if (n.kind === 'host') return 'node';
        if (n.kind === 'external') return 'external';
        return '?';
      },
      pvxType(n) {
        const t = this.TYPES[this.pvxTypeKey(n)] || this.TYPES['?'];
        return { ...t, key: this.pvxTypeKey(n), template: !!(n && n.template) };
      },
      pvxTypeStyle(n) {
        const c = this.pvxType(n).color;
        return `background:${c}1f;color:${c};border:1px solid ${c}55`;
      },
      pvxTypeTitle(n) {
        const t = this.pvxType(n);
        const base = t.label + (n && n.vmid > 0 ? ` · vmid ${n.vmid}` : '');
        return t.template ? base + ' · TEMPLATE (not a bootable guest)' : base;
      },
      pvxCountByType() {
        const count = {};
        for (const n of this.pvxNodes()) {
          const k = this.pvxTypeKey(n);
          count[k] = (count[k] || 0) + 1;
        }
        return ['node', 'lxc', 'qemu', 'external', '?']
          .filter((k) => count[k])
          .map((k) => ({ key: k, abbrev: this.TYPES[k].abbrev, label: this.TYPES[k].label, n: count[k] }));
      },

      TABS_HOST: [
        { id: 'summary',   rot: 'Summary' },
        { id: 'charts', rot: 'Charts' },
        { id: 'console',  rot: 'Shell' },
        { id: 'tasks',  rot: 'Tasks' },
        { id: 'disks',   rot: 'Disks' },
        { id: 'storage',  rot: 'Storage' },
        { id: 'zfs',      rot: 'ZFS' },
        { id: 'network',     rot: 'Network' },
        { id: 'system',  rot: 'System' },
        { id: 'packages',  rot: 'Packages' },
        { id: 'registry', rot: 'Log' },
        { id: 'perms',    rot: 'Permissions' },
      ],
      TABS_GUEST: [
        { id: 'summary',   rot: 'Summary' },
        { id: 'charts', rot: 'Charts' },
        { id: 'console',  rot: 'Console' },
        { id: 'snaps',    rot: 'Copies' },
        { id: 'tasks',  rot: 'Tasks' },
      ],
      pvxIsGuest(n) { return !!(n && n.kind === 'guest' && n.vmid > 0); },
      pvxNodeTabs(n) {
        if (!n) return [];
        return this.pvxIsGuest(n) ? this.TABS_GUEST : this.TABS_HOST;
      },
      pvxActiveTab() {
        const n = this.pvxOpenNode();
        if (!n) return '';
        const tabs = this.pvxNodeTabs(n);
        return tabs.some((a) => a.id === this.pvx.tab) ? this.pvx.tab : 'summary';
      },
      pvxGoTo(tab) {
        this.pvx.tab = tab;
        if (tab === 'console') {
          const n = this.pvxOpenNode();
          if (n) this.pvxOpenConsole(n.id);
        } else {
          this.pvxCloseConsole();
        }
        if (tab === 'tasks' && !this.pvx.tasks.length) this.pvxLoadTasks();
        if (tab === 'disks' && !this.pvx.disks.length) this.pvxLoadDisks();
        if (tab === 'storage' && !this.pvx.storage) this.pvxLoadStorage();
        if (tab === 'zfs' && !this.pvx.zfs) this.pvxLoadZfs();
        if ((tab === 'zfs' || tab === 'disks' || tab === 'storage') && !this.pvx.loaded.topology) this.pvxLoadTopology();
        if (tab === 'perms' && !this.pvx.perms && !this.pvx.permsOpen) this.pvxLoadPerms();
        if (tab === 'charts') this.pvxLoadSeries();
        if ((tab === 'network' || tab === 'system') && !this.pvx.loaded.system) this.pvxLoadSystem();
        if (tab === 'summary' && !this.pvx.loaded.system && !this.pvxIsGuest(this.pvxOpenNode())) this.pvxLoadSystem();
        if (tab === 'summary') this.pvxLoadNote(this.pvx.open);
        if (tab === 'snaps' && !this.pvx.storage) this.pvxLoadStorage();
        if (tab === 'packages' && !this.pvx.loaded.packages) this.pvxLoadPackages();
        if (tab === 'registry' && !this.pvx.loaded.registry) this.pvxLoadRegistry();
      },
      pvxSelect(n) {
        if (!n) return;
        if (this.pvx.open === n.id) { this.pvxClearSelection(); return; }
        this.pvx.tab = 'summary';
        this.pvxOpen(n);
      },
      pvxClearSelection() { this.pvx.tab = ''; this.pvxClose(); },
      pvxHasSelection() { return !!this.pvx.open; },

      pvxLabSummary() {
        const guests = this.pvxNodes().filter((n) => this.pvxIsGuest(n));
        let memU = 0, memT = 0, cpuSum = 0, cpuN = 0, noData = 0, running = 0, unknown = 0;
        for (const g of guests) {
          const st = (g.status && g.status.value) || '';
          const observed = !g.stale && !g.absent_since && !!(g.status && g.status.observed_at);
          if (!observed) unknown++;
          else if (st === 'running' || st === 'online') running++;
          const mu = g.mem_used, mt = g.mem_total, cf = g.cpu_frac;
          if (!mu || !mu.observed_at || !mt || !(Number(mt.value) > 0)) noData++;
          else { memU += Number(mu.value); memT += Number(mt.value); }
          if (cf && cf.observed_at && Number(cf.value) >= 0) { cpuSum += Number(cf.value); cpuN++; }
        }
        const pools = this.pvxZfsPools();
        const poolBad = pools.filter((p) => p && !p.healthy).length;
        return {
          guests: guests.length, running, noData, unknown,
          memUsed: memU, memTotal: memT,
          memPct: memT > 0 ? (memU / memT) * 100 : null,
          cpuPct: cpuN ? (cpuSum / cpuN) * 100 : null,
          poolTotal: pools.length, poolBad,
          poolState: !pools.length ? 'unmeasured' : (poolBad ? 'degraded' : 'ok'),
        };
      },
      pvxBackups() {
        return { loaded: !!this.pvx.loaded.backup, items: this.pvx.backup.datastores || [] };
      },
      pvxBackupAge(ds) {
        const d = this.pvx.backup;
        if (!ds || !d) return { known: false };
        if (ds.error) return { known: false, error: ds.error };
        if (!ds.total) return { known: true, empty: true };
        const seg = Math.max(0, Number(d.observed_at) - Number(ds.last_ctime));
        return { known: true, empty: false, seg, text: this.pvxFormatAge(seg) };
      },
      pvxBackupState(ds) {
        const i = this.pvxBackupAge(ds);
        if (ds.error) return { key: 'error', label: 'error', color: '#ef4444', note: ds.error };
        if (ds.schedule_state === 'disarmed') {
          return { key: 'disarmed', label: 'disarmed', color: '#94a3b8',
                   note: 'schedule turned off' + (ds.schedule ? ' (was ' + ds.schedule + ')' : '') + ' — this is not a failure' };
        }
        if (i.empty) return { key: 'empty', label: 'no copy yet', color: '#ef4444', note: '' };
        const limitH = ds.storage === 'pbs' ? 36 : 24 * 10;
        const h = i.seg / 3600;
        const color = h <= limitH ? '#22c55e' : h <= limitH * 2 ? '#f59e0b' : '#ef4444';
        return {
          key: 'active', label: i.text, color,
          note: ds.schedule_state === 'outside-pve'
            ? 'scheduled outside PVE — the panel does not know by whom'
            : (ds.schedule ? 'daily at ' + ds.schedule : ''),
        };
      },
      pvxBackupStyle(ds) {
        const c = this.pvxBackupState(ds).color;
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      async pvxLoadBackup() {
        try {
          const r = await this.api('/api/proxmox/backup', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.backup = await r.json();
          this.pvx.loaded.backup = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.backup = { datastores: [] };
        }
      },
      pvxSummaryStyle(e) {
        const c = e === 'ok' ? '#22c55e' : e === 'degraded' ? '#ef4444' : '#64748b';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      pvxPauseReason() {
        if (this.pvx.con.guest) return 'console open';
        if (this.pvx.focusFilter) return 'typing in the filter';
        return '';
      },
      pvxTick() {
        if (typeof document !== 'undefined' && document.hidden) return;
        const reason = this.pvxPauseReason();
        this.pvx.paused = !!reason;
        this.pvx.pauseReason = reason;
        this.pvxRefreshAge();
        if (reason) return;
        if (typeof this.loadNodes === 'function') this.loadNodes();
        this.pvxLoadHealth();
        const tab = this.pvxActiveTab();
        if (tab === 'tasks') this.pvxLoadTasks();
        if (tab === 'disks') this.pvxLoadDisks();
        if (tab === 'storage') this.pvxLoadStorage();
        if (tab === 'zfs') this.pvxLoadZfs();
      },
      pvxRefreshAge() {
        const p = this.nodes && this.nodes.poll;
        this.pvx.ageSec = p && typeof p.age_seconds === 'number' ? p.age_seconds : null;
      },
      pvxAgeText() {
        const s = this.pvx.ageSec;
        if (s == null) return 'no timestamp';
        if (s < 0) return 'never observed';
        if (s < 60) return `data from ${s}s ago`;
        const m = Math.floor(s / 60);
        if (m < 60) return `data from ${m} min ago`;
        const h = Math.floor(m / 60);
        return h < 48 ? `data from ${h}h ago` : `data from ${Math.floor(h / 24)} days ago`;
      },
      pvxAgeStale() { return this.pvx.ageSec != null && this.pvx.ageSec > 120; },
      pvxStopTimer() { if (this._pvxTimer) { clearInterval(this._pvxTimer); this._pvxTimer = null; } },
      pvxStartTimer() { this.pvxStopTimer(); this._pvxTimer = setInterval(() => this.pvxTick(), 15000); },

      pvxInit() {
        this.pvxLoadHealth();
        this.pvxLoadZfs();
        this.pvxLoadBackup();
        this.pvxStartTimer();
        if (typeof this.loadNodes === 'function') {
          Promise.resolve(this.loadNodes()).finally(() => {
            this.pvx.firstLoad = false;
            this.pvxRefreshAge();
          });
        } else { this.pvx.firstLoad = false; }
      },

      async pvxLoadHealth() {
        this.pvx.loading = true;
        try {
          const r = await this.api('/api/proxmox', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.health = d.hypervisor || null;
          this.pvx.ttl = d.ttl_seconds || 90;
          this.pvx.forbidden = false;
          this.pvx.lastError = '';
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          console.warn('[proxmox] health', e);
        } finally { this.pvx.loading = false; }
      },

      pvxFormatAge(ageSeconds) {
        if (ageSeconds === null || ageSeconds === undefined) return 'no timestamp';
        if (ageSeconds < 0) return 'never observed';
        if (ageSeconds < 60) return `data from ${ageSeconds}s ago`;
        const min = Math.floor(ageSeconds / 60);
        if (min < 60) return `data from ${min} min ago`;
        const h = Math.floor(min / 60);
        if (h < 48) return `data from ${h}h ago`;
        return `data from ${Math.floor(h / 24)} days ago`;
      },

      pvxStaleBadge(v) { return v && v.stale ? 'expired' : 'live'; },
      pvxStaleStyle(v) {
        const c = (v && v.stale) ? '#f59e0b' : '#22c55e';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      pvxBytes(n) {
        if (n === null || n === undefined) return '—';
        const u = ['B', 'KB', 'MB', 'GB', 'TB'];
        let v = Number(n), i = 0;
        while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
        return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
      },
      pvxPct(used, total) {
        if (!total) return 0;
        return Math.round((Number(used) / Number(total)) * 100);
      },
      pvxUptime(seg) {
        if (!seg) return '—';
        const d = Math.floor(seg / 86400), h = Math.floor((seg % 86400) / 3600);
        return d > 0 ? `${d}d ${h}h` : `${h}h`;
      },
      pvxLoadStr(v) {
        const l = v && v.load ? v.load.value : null;
        return (l && l.length === 3) ? l.map(x => Number(x).toFixed(2)).join('  ') : '—';
      },

      async pvxLoadTasks() {
        this.pvx.loading = true;
        try {
          const q = this.pvx.errorsOnly ? '?errors=1&limit=50' : '?limit=50';
          const r = await this.api('/api/proxmox/tasks' + q, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.tasks = d.tasks || [];
          this.pvx.lastError = '';
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        } finally { this.pvx.loading = false; }
      },

      pvxToggleErrors() {
        this.pvx.errorsOnly = !this.pvx.errorsOnly;
        return this.pvxLoadTasks();
      },

      pvxTaskOK(t) { return t && t.status === 'OK'; },
      pvxTaskStyle(t) {
        const c = this.pvxTaskOK(t) ? '#22c55e' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxTaskTarget(t) { return t && t.id ? `${t.type} ${t.id}` : (t ? t.type : ''); },

      async pvxOpenLog(upid) {
        this.pvx.logOpen = true;
        this.pvx.taskLog = { upid, lines: null };
        try {
          const r = await this.api('/api/proxmox/tasks/log?upid=' + encodeURIComponent(upid));
          const d = await r.json();
          this.pvx.taskLog = { upid, lines: d.lines || [] };
        } catch (e) {
          this.pvx.taskLog = { upid, lines: ['(failed to read the log: ' + this._errText(e) + ')'] };
        }
      },
      pvxCloseLog() { this.pvx.logOpen = false; this.pvx.taskLog = null; },

      async pvxLoadDisks() {
        try {
          const r = await this.api('/api/proxmox/disks', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.disks = d.disks || [];
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        }
      },

      pvxWearout(d) {
        if (!d || d.wearout_pct === null || d.wearout_pct === undefined) return 'not reported';
        return `${d.wearout_pct}%`;
      },
      pvxHealthStyle(d) {
        const ok = d && d.health === 'PASSED';
        const c = ok ? '#22c55e' : (d && d.health ? '#f59e0b' : '#64748b');
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      async pvxLoadStorage() {
        try {
          const r = await this.api('/api/proxmox/storage', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.storage = await r.json();
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.storage = null;
        }
      },

      async pvxLoadZfs() {
        try {
          const r = await this.api('/api/proxmox/zfs', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.zfs = await r.json();
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.zfs = null;
        }
      },

      pvxStorageState() {
        const d = this.pvx.storage && this.pvx.storage.datastore_audit;
        if (!d || !d.observed_at) return 'unmeasured';
        return d.value ? 'ok' : 'no-permission';
      },

      pvxStoragePools() { return (this.pvx.storage && this.pvx.storage.pools) || []; },
      pvxZfsPools() { return (this.pvx.zfs && this.pvx.zfs.pools) || []; },

      pvxUsageStyle(pct) {
        const v = Math.max(0, Math.min(100, Number(pct) || 0));
        const c = v >= 85 ? '#ef4444' : (v >= 70 ? '#f59e0b' : '#22c55e');
        return `width:${v}%;background:${c}`;
      },
      pvxUsageText(p) {
        if (!p) return '—';
        return `${(Number(p.used_pct) || 0).toFixed(1)}% · ${this.pvxBytes(p.used)} / ${this.pvxBytes(p.total)}`;
      },

      pvxZfsStyle(p) {
        const c = (p && p.healthy) ? '#22c55e' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxZfsFrag(p) {
        if (!p || p.frag_pct === null || p.frag_pct === undefined) return '—';
        return `frag ${p.frag_pct}%`;
      },

      async pvxLoadPerms() {
        this.pvx.permsOpen = !this.pvx.permsOpen;
        if (!this.pvx.permsOpen || this.pvx.perms) return;
        try {
          const r = await this.api('/api/proxmox/permissions');
          this.pvx.perms = await r.json();
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        }
      },
      pvxPermRows() {
        const p = this.pvx.perms && this.pvx.perms.permissions ? this.pvx.perms.permissions : {};
        return Object.keys(p).sort().map(k => ({ path: k, privs: Object.keys(p[k]).sort().join(', ') }));
      },

      pvxGuests() {
        return (this.nodes && this.nodes.list ? this.nodes.list : []).filter(n => n.kind === 'guest' && n.vmid > 0);
      },

      async pvxLoadSnaps(nodeId) {
        this.pvx.guestSel = nodeId || '';
        this.pvx.snaps = [];
        if (!this.pvx.guestSel) return;
        try {
          const r = await this.api('/api/proxmox/snapshots?node=' + encodeURIComponent(this.pvx.guestSel));
          const d = await r.json();
          this.pvx.snaps = d.snapshots || [];
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.showToast('failed to list snapshots: ' + this._errText(e), 'err');
        }
      },

      pvxCreateSnap(nodeId) {
        const name = (this.pvx.newSnap || '').trim();
        this.askConfirm('Create snapshot',
          `Creates the snapshot "${name}" on "${nodeId}". The response only comes back once the hypervisor has finished the task.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(name), { method: 'POST' });
              const d = await r.json().catch(() => ({}));
              this.showToast('snapshot created (task ' + (d.upid || '—') + ')', 'ok');
              this.pvx.newSnap = '';
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('failed to create the snapshot: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              await this.pvxLoadSnaps(nodeId);
            }
          });
      },

      pvxDeleteSnap(nodeId, name) {
        this.askConfirm('Delete snapshot',
          `Deletes "${name}" from "${nodeId}". Irreversible: the state kept in that snapshot ceases to exist.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(name), { method: 'DELETE' });
              const d = await r.json().catch(() => ({}));
              this.showToast('snapshot deleted (task ' + (d.upid || '—') + ')', 'ok');
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('failed to delete the snapshot: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              await this.pvxLoadSnaps(nodeId);
            }
          });
      },

      pvxRollbackSnap(nodeId, name) {
        const g = this.pvxGuests().find(x => x.id === nodeId);
        const label = (g && g.name) || nodeId;
        this.askConfirm('Roll back to snapshot "' + name + '"',
          'Guest "' + label + '" (' + nodeId + ') goes back to the state of snapshot "' + name + '". ' +
          'EVERYTHING written to it after that snapshot ceases to exist — files, database, logs, ' +
          'and any work in progress. There is no second copy: the pool on this server is a ' +
          'single disk, with no mirror. Type the guest name (' + label + ') to confirm.',
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots/rollback?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(name), { method: 'POST' });
              const d = await r.json().catch(() => ({}));
              this.showToast('guest rolled back to snapshot "' + name + '" (task ' + (d.upid || '—') + ')', 'ok');
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('rollback failed: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              await this.pvxLoadSnaps(nodeId);
            }
          }, { danger: true, requireText: label });
      },

      // 🔴 SUSPEND HAS NO BUTTON, AND THAT IS DELIBERATE. Measured on this host:
      // `vzsuspend 204` ended in `lxc-checkpoint … criu … failed: exit code 1` and the
      // CT stayed `running`. A button that always errors trains the operator to ignore
      // errors — and the next error, the real one, goes unnoticed. It comes back when
      // CRIU works here, measured.

      pvxIsHost(n) { return !!(n && n.kind === 'host'); },
      pvxConsoleGuestState(g) {
        if (g && g.kind === 'host') {
          return { can: true, reason: '', host: true };
        }
        const st = (g && g.credential && g.credential.state) || '';
        if (st === 'ok') return { can: true, reason: '' };
        if (st === 'absent') return { can: false, reason: 'no node token in the vault — this guest has no console' };
        if (st === 'revoked') return { can: false, reason: 'credential revoked — console unavailable' };
        if (st === 'expired') return { can: false, reason: 'credential expired — console unavailable' };
        return { can: false, reason: 'credential state unknown' };
      },
      pvxConsoleCan(g) { return this.pvxConsoleGuestState(g).can; },
      pvxConsoleReason(g) { return this.pvxConsoleGuestState(g).reason; },

      pvxOpenConsole(nodeId) {
        const g = this.pvxNodes().find(x => x.id === nodeId);
        if (g && !this.pvxConsoleCan(g)) {
          this.pvx.con = { guest: '', state: 'error', error: this.pvxConsoleReason(g) };
          this.showToast(this.pvxConsoleReason(g), 'err');
          return;
        }
        if (this.pvx.con.guest === nodeId && this.pvx.con.state === 'on') return;
        this.pvxCloseConsole();
        this.pvx.con = { guest: nodeId, state: 'opening', error: '' };

        this.$nextTick(() => {
          const el = document.getElementById('pvx-console');
          if (!el || typeof Terminal === 'undefined') {
            this.pvx.con.state = 'error';
            this.pvx.con.error = 'xterm.js did not load';
            return;
          }
          el.innerHTML = '';
          const term = new Terminal({
            fontSize: 13,
            fontFamily: '"JetBrains Mono", ui-monospace, Menlo, Consolas, monospace',
            cursorBlink: true,
            scrollback: 5000,
            convertEol: false,
          });
          const fit = new FitAddon.FitAddon();
          term.loadAddon(fit);
          term.open(el);
          try { fit.fit(); } catch (_) {}

          const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
          const ws = new WebSocket(proto + '//' + location.host
            + '/ws/proxmox/console?node=' + encodeURIComponent(nodeId));
          ws.binaryType = 'arraybuffer';

          term.onData(d => { if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'input', data: d })); });
          term.onResize(({ cols, rows }) => {
            if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols, rows }));
          });

          ws.onopen = () => {
            this.pvx.con.state = 'on';
            try { fit.fit(); } catch (_) {}
            ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
            term.focus();
          };
          ws.onmessage = (ev) => {
            if (typeof ev.data === 'string') {
              let m = null;
              try { m = JSON.parse(ev.data); } catch (_) { return; }
              if (m && m.type === 'error') {
                this.pvx.con.state = 'error';
                this.pvx.con.error = m.message || 'the hypervisor refused the console';
                term.write('\r\n\x1b[31m' + this.pvx.con.error + '\x1b[0m\r\n');
              }
              return;
            }
            term.write(new Uint8Array(ev.data));
          };
          ws.onclose = () => {
            if (this.pvx.con.state !== 'error') this.pvx.con.state = 'closed';
            term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n');
          };
          ws.onerror = () => {
            this.pvx.con.state = 'error';
            this.pvx.con.error = this.pvx.con.error || 'the connection to the panel dropped';
          };

          this._pvxConPing = setInterval(() => {
            if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'ping' }));
          }, 20000);

          const refit = () => { try { fit.fit(); } catch (_) {} };
          this._pvxConObs = new ResizeObserver(refit);
          this._pvxConObs.observe(el);

          this._pvxConTerm = term;
          this._pvxConWs = ws;
        });
      },

      pvxCloseConsole() {
        if (this._pvxConPing) { clearInterval(this._pvxConPing); this._pvxConPing = null; }
        if (this._pvxConObs) { try { this._pvxConObs.disconnect(); } catch (_) {} this._pvxConObs = null; }
        if (this._pvxConWs) { try { this._pvxConWs.close(); } catch (_) {} this._pvxConWs = null; }
        if (this._pvxConTerm) { try { this._pvxConTerm.dispose(); } catch (_) {} this._pvxConTerm = null; }
        this.pvx.con = { guest: '', state: 'closed', error: '' };
      },

      pvxConsoleLabel() {
        const e = this.pvx.con.state;
        if (e === 'opening') return 'opening…';
        if (e === 'on') return 'live';
        if (e === 'error') return 'error';
        return 'closed';
      },
      pvxConsoleStyle() {
        const e = this.pvx.con.state;
        const c = e === 'on' ? '#22c55e' : (e === 'error' ? '#ef4444' : '#f59e0b');
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      pvxNodeState(n) {
        if (!n) return 'ok';
        if (n.absent_since) return 'gone';
        if (n.stale) return 'stale';
        const cred = (n.credential && n.credential.state) || '';
        if (n.transport === 'pve-api' && cred !== 'ok') return 'no-credential';
        const st = (n.status && n.status.value) || '';
        if (st !== 'running' && st !== 'online') return 'stopped';
        let worst = 'ok';
        for (const which of ['cpu', 'ram', 'disco']) {
          const pct = pvxGaugePct(n, which);
          if (pct === null) continue;
          if (pct >= 90) return 'critical';
          if (pct >= 70) worst = 'warning';
        }
        return worst;
      },

      pvxStateLabel(e) {
        return ({
          'gone': 'gone from the hypervisor',
          'stale': 'expired', 'no-credential': 'no credential', 'stopped': 'stopped',
          'critical': 'critical', 'warning': 'warning', 'ok': 'ok',
        })[e] || e;
      },
      pvxStateColor(e) {
        return ({
          'gone': '#64748b',
          'stale': '#f59e0b', 'no-credential': '#ef4444', 'stopped': '#64748b',
          'critical': '#ef4444', 'warning': '#f59e0b', 'ok': '#22c55e',
        })[e] || '#64748b';
      },
      pvxStateStyle(e) {
        const c = this.pvxStateColor(e);
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      pvxTier(pct, anterior) {
        const WARN = 70, CRITICAL = 90, SLACK = 3;
        const v = Number(pct);
        if (!isFinite(v) || v < 0) return 'ok';
        if (anterior === 'critical') {
          if (v >= CRITICAL - SLACK) return 'critical';
          return v >= WARN ? 'warning' : 'ok';
        }
        if (anterior === 'warning') {
          if (v >= CRITICAL) return 'critical';
          return v >= WARN - SLACK ? 'warning' : 'ok';
        }
        if (v >= CRITICAL) return 'critical';
        if (v >= WARN) return 'warning';
        return 'ok';
      },

      pvxFilterNodes(list, text, segment, stateOf) {
        const terms = String(text || '').toLowerCase().split(/\s+/).filter(Boolean);
        return (list || []).filter(function (n) {
          if (segment && stateOf(n) !== segment) return false;
          const st = (n.status && n.status.value) || '';
          const cred = (n.credential && n.credential.state) || '';
          const type = String(n.id || '').split('/')[0];
          return terms.every(function (t) {
            const i = t.indexOf(':');
            const field = i > 0 ? t.slice(0, i) : '';
            const value = i > 0 ? t.slice(i + 1) : t;
            switch (field) {
              case 'node': return String(n.name || '').toLowerCase().includes(value);
              case 'status': return st.toLowerCase().includes(value);
              case 'type': {
                const canon = { ct: 'lxc', container: 'lxc',
                                vm: 'qemu', machine: 'qemu',
                                node: 'node', host: 'node', hypervisor: 'node' }[value] || value;
                return type.toLowerCase() === canon
                    || String(n.kind || '').toLowerCase() === canon
                    || String(n.kind || '').toLowerCase() === value;
              }
              case 'state': return stateOf(n) === value;
              case 'cred': return cred.toLowerCase().includes(value);
              case 'id': return String(n.id || '').toLowerCase().includes(value);
            }
            return [n.id, n.name, n.address, st, cred, type].join(' ').toLowerCase().includes(t);
          });
        });
      },

      pvxRescope(sel, visible) {
        const seen = {};
        (visible || []).forEach(function (n) { seen[n.id] = true; });
        return (sel || []).filter(function (id) { return seen[id] === true; });
      },

      pvxSegments(list, stateOf) {
        const order = ['stale', 'no-credential', 'critical', 'warning', 'stopped', 'gone', 'ok'];
        const count = {};
        order.forEach(function (k) { count[k] = 0; });
        (list || []).forEach(function (n) {
          const e = stateOf(n);
          if (count[e] !== undefined) count[e]++;
        });
        return order.map(function (k) { return { key: k, n: count[k] }; });
      },

      pvxStep(action, howMany) {
        const BASE = {
          start: 1, shutdown: 2, stop: 3,
          snapcriar: 2, snapapagar: 2, rollback: 3, revoke: 3,
          reboot: 2,
          clone: 2,
          backup: 1,
        };
        const base = BASE[action] || 2;
        const step = Number(howMany) > 1 ? 1 : 0;
        return Math.min(3, base + step);
      },

      pvxActionState(n, action) {
        if (!n) return { can: false, reason: 'unknown node' };
        const st = (n.status && n.status.value) || '';
        const cred = (n.credential && n.credential.state) || '';
        const on = st === 'running' || st === 'online';

        if (n.absent_since) {
          return { can: false, reason: 'the hypervisor no longer lists this node — it was deleted, or the panel lost access to it' };
        }
        if (action === 'revoke') {
          if (cred === 'absent') return { can: false, reason: 'there is no credential to revoke on this node' };
          if (cred === 'revoked') return { can: false, reason: 'the credential for this node has already been revoked' };
          return { can: true, reason: '' };
        }

        if (action === 'clone' || action === 'backup') {
          if (!this.pvxIsGuest(n)) {
            return { can: false, reason: n.kind === 'host'
              ? 'the hypervisor is not a guest — there is nothing to copy here'
              : 'external node: not a guest of this hypervisor' };
          }
          if (action === 'backup') {
            if (!this.pvx.storage) {
              return { can: false, reason: 'I have not read this hypervisor storage list yet' };
            }
            if (!this.pvxBackupStorages().length) {
              return { can: false, reason: 'no storage on this hypervisor accepts backups' };
            }
          }
          return { can: true, reason: '' };
        }

        const power = action === 'start' || action === 'shutdown' || action === 'stop' || action === 'reboot';
        if (power && (n.kind !== 'guest' || !(n.vmid > 0))) {
          if (n.kind === 'external') {
            return { can: false, reason: 'external node: not a guest of this hypervisor — powering on and off will depend on the lab agent (Phase 8)' };
          }
          return { can: false, reason: 'the hypervisor does not power on or off from the panel — that is the physical button on the machine' };
        }
        if (cred !== 'ok') {
          const reason = ({
            absent: 'no node token in the vault',
            revoked: 'credential revoked',
            expired: 'credential expired',
          })[cred] || 'credential state unknown';
          return { can: false, reason: reason + ' — the panel has no way to act on this node' };
        }
        if (action === 'reboot' && !on) {
          return { can: false, reason: 'it is powered off — to start it, use "Turn on"' };
        }
        if (action === 'start' && on) {
          return { can: false, reason: 'it is already running — asking again returns "already running" and pollutes the task trail' };
        }
        if ((action === 'shutdown' || action === 'stop') && !on) {
          return { can: false, reason: 'it is already powered off' };
        }
        return { can: true, reason: '' };
      },

      async pvxLoadNote(id) {
        if (!id) return;
        if (this.pvx.note.node === id && !this.pvx.note.error) return;
        this.pvx.note = { node: id, markdown: '', origin: '', reason: '', loading: true, error: '',
                          editing: false, draft: '', saving: false };
        try {
          const r = await this.api('/api/nodes/' + id + '/note', { raw: true });
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          if (this.pvx.note.node !== id) return;
          this.pvx.note.markdown = d.markdown || '';
          this.pvx.note.origin = d.origin || '';
          this.pvx.note.reason = d.reason || '';
        } catch (e) {
          if (this.pvx.note.node !== id) return;
          this.pvx.note.error = this._errText(e);
        } finally {
          if (this.pvx.note.node === id) this.pvx.note.loading = false;
        }
      },

      pvxEditNote() {
        if (this.pvx.note.loading) {
          this.showToast('the note is still loading', 'err');
          return;
        }
        if (this.pvx.note.origin === 'outside-pve' || this.pvx.note.origin === 'nonexistent') {
          this.showToast(this.pvx.note.reason || 'there is no note to edit on this node', 'err');
          return;
        }
        this.pvx.note.draft = this.pvx.note.markdown || '';
        this.pvx.note.editing = true;
      },
      pvxCancelNote() {
        this.pvx.note.editing = false;
        this.pvx.note.draft = '';
      },
      pvxNoteChanged() {
        return this.pvx.note.editing && this.pvx.note.draft !== (this.pvx.note.markdown || '');
      },
      NOTE_MAX: 8192,
      pvxNoteFits() { return (this.pvx.note.draft || '').length <= this.NOTE_MAX; },

      async pvxSaveNote() {
        const n = this.pvx.note;
        if (!n.node || n.saving) return;
        if (!this.pvxNoteFits()) {
          this.showToast('the note went past ' + this.NOTE_MAX + ' characters — a note is meant to be READ', 'err');
          return;
        }
        n.saving = true;
        n.error = '';
        try {
          const r = await this.api('/api/nodes/' + n.node + '/note', {
            method: 'PUT', body: JSON.stringify({ markdown: n.draft }),
          });
          const d = await r.json().catch(() => ({}));
          if (this.pvx.note.node !== n.node) return;
          this.pvx.note.markdown = d.markdown !== undefined ? d.markdown : n.draft;
          this.pvx.note.origin = d.origin || 'pve-notes';
          this.pvx.note.editing = false;
          this.pvx.note.draft = '';
          this.showToast('note saved in Proxmox', 'ok');
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.note.error = txt;
          this.showToast('failed to save the note: ' + txt, 'err');
        } finally {
          if (this.pvx.note.node === n.node) this.pvx.note.saving = false;
        }
      },

      pvxMd(txt) {
        const esc = (x) => String(x).replace(/[&<>"']/g, (c) => (
          { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
        const lines = String(txt || '').replace(/\r\n?/g, '\n').split('\n');
        const out = [];
        let inList = false, inCode = false;
        const closeList = () => { if (inList) { out.push('</ul>'); inList = false; } };

        const inline = (l) => esc(l)
          .replace(/`([^`]+)`/g, '<code class="pvx-md-code">$1</code>')
          .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
          .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
          .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
            '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');

        for (const raw of lines) {
          const l = raw.trimEnd();
          if (/^```/.test(l)) {
            closeList();
            out.push(inCode ? '</pre>' : '<pre class="pvx-md-pre">');
            inCode = !inCode;
            continue;
          }
          if (inCode) { out.push(esc(raw)); continue; }
          if (/^\s*$/.test(l)) { closeList(); continue; }
          if (/^---+$/.test(l)) { closeList(); out.push('<hr class="pvx-md-hr">'); continue; }
          const h = l.match(/^(#{1,6})\s+(.*)$/);
          if (h) {
            closeList();
            const n = Math.min(6, h[1].length + 2);
            out.push('<h' + n + ' class="pvx-md-h">' + inline(h[2]) + '</h' + n + '>');
            continue;
          }
          const li = l.match(/^\s*[-*]\s+(.*)$/);
          if (li) {
            if (!inList) { out.push('<ul class="pvx-md-ul">'); inList = true; }
            out.push('<li>' + inline(li[1]) + '</li>');
            continue;
          }
          closeList();
          out.push('<p>' + inline(l) + '</p>');
        }
        closeList();
        if (inCode) out.push('</pre>');
        return out.join('\n');
      },

      pvxBackupStorages() {
        const pools = (this.pvx.storage && this.pvx.storage.pools) || [];
        return pools.filter(p => Array.isArray(p.content) && p.content.indexOf('backup') >= 0);
      },

      async pvxOpenClone(n) {
        const state = this.pvxActionState(n, 'clone');
        if (!state.can) { this.showToast(state.reason, 'err'); return; }
        this.pvx.clone = { open: true, loading: true, origin: n.id, originName: n.name,
                           newID: 0, name: '', on: false,
                           needsSnap: false, snapshots: [], snapshot: '', error: '' };
        try {
          const r = await this.api('/api/nodes/' + n.id + '/clone', { raw: true });
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.clone.newID = d.next_id || 0;
          this.pvx.clone.name = d.suggestion || '';
          this.pvx.clone.on = !!d.on;
          this.pvx.clone.needsSnap = !!d.needs_snapshot;
          this.pvx.clone.snapshots = Array.isArray(d.snapshots) ? d.snapshots : [];
          this.pvx.clone.snapshot = this.pvx.clone.snapshots[0] || '';
        } catch (e) {
          this.pvx.clone.error = this._errText(e);
        } finally {
          this.pvx.clone.loading = false;
        }
      },
      pvxCloseClone() { this.pvx.clone.open = false; },

      pvxCloneConfirm() {
        const c = this.pvx.clone;
        if (!c.newID) { this.showToast('no target id — the hypervisor did not answer which one is free', 'err'); return; }
        if (c.needsSnap && !c.snapshot) {
          this.showToast('a running container only clones from a snapshot — create one above, or shut the guest down', 'err');
          return;
        }
        let warnOn = '';
        if (c.snapshot) {
          warnOn = `\n\nThe copy comes from snapshot "${c.snapshot}", not from the current state: `
            + `anything written after it is NOT included.`;
        } else if (c.on) {
          warnOn = `\n\n⚠ "${c.originName}" is RUNNING. The copy comes out crash-consistent: `
            + `the same state as an abrupt power cut. For a clean copy, shut it down first.`;
        }
        this.askConfirm('Clone ' + c.originName,
          `Creates guest ${c.newID} ("${c.name || 'unnamed'}") as a FULL COPY of "${c.originName}". `
          + `It takes up roughly as much disk as the source. `
          + `The source is NOT changed or interrupted.`
          + `\n\nThe task takes minutes and runs on the hypervisor: the panel does not wait for it, `
          + `it opens the task log so you can follow along.` + warnOn,
          () => this.pvxCloneNow(), {});
      },

      async pvxCloneNow() {
        const c = this.pvx.clone;
        if (this.pvx.busy) return;
        this.pvx.busy = c.origin;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + c.origin + '/clone', {
            method: 'POST', body: JSON.stringify({ new_id: c.newID, name: c.name }),
          });
          const d = await r.json().catch(() => ({}));
          this.pvx.clone.open = false;
          this.showToast(`clone ${c.newID} requested — follow the task`, 'ok');
          if (d.upid) this.pvxOpenLog(d.upid);
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.pvx.clone.error = txt;
          this.showToast('failed to clone: ' + txt, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxReload();
        }
      },

      pvxBackupConfirm(n) {
        const state = this.pvxActionState(n, 'backup');
        if (!state.can) { this.showToast(state.reason, 'err'); return; }
        const st = this.pvx.bkp.storage || (this.pvxBackupStorages()[0] || {}).id || '';
        if (!st) { this.showToast('no storage accepts backups', 'err'); return; }
        this.pvx.bkp.storage = st;
        this.pvxBackupNow(n, st);
      },

      async pvxBackupNow(n, storage) {
        if (this.pvx.busy) return;
        this.pvx.busy = n.id;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + n.id + '/backup', {
            method: 'POST',
            body: JSON.stringify({ storage, mode: this.pvx.bkp.mode || 'snapshot' }),
          });
          const d = await r.json().catch(() => ({}));
          this.showToast(`copy of ${n.name} requested on ${storage} — follow the task`, 'ok');
          if (d.upid) this.pvxOpenLog(d.upid);
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.showToast(`failed to store a copy of ${n.name}: ` + txt, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxReload();
        }
      },

      pvxNodes() {
        return (this.nodes && this.nodes.list) ? this.nodes.list : [];
      },

      pvxFilteredNodes() {
        return this.pvxFilterNodes(this.pvxNodes(), this.pvx.filter, this.pvx.segment, this.pvxNodeState);
      },
      pvxHost() { return this.pvxNodes().filter(n => n.kind === 'host'); },

      pvxSetFilter(text) {
        this.pvx.filter = text;
        this.pvx.sel = this.pvxRescope(this.pvx.sel, this.pvxFilteredNodes());
      },
      pvxSetSegment(key) {
        this.pvx.segment = (this.pvx.segment === key) ? '' : key;
        this.pvx.sel = this.pvxRescope(this.pvx.sel, this.pvxFilteredNodes());
      },
      pvxClearFilter() {
        this.pvx.filter = '';
        this.pvx.segment = '';
        this.pvx.sel = this.pvxRescope(this.pvx.sel, this.pvxFilteredNodes());
      },
      pvxHasFilter() {
        return !!(this.pvx.filter || this.pvx.segment);
      },
      pvxShowing() {
        const parts = [];
        if (this.pvx.segment) parts.push(this.pvxStateLabel(this.pvx.segment));
        if (this.pvx.filter) parts.push(this.pvx.filter);
        return parts.join(' · ');
      },
      pvxCount() {
        return `${this.pvxFilteredNodes().length} of ${this.pvxNodes().length}`;
      },
      pvxScreenSegments() {
        return this.pvxSegments(this.pvxNodes(), this.pvxNodeState);
      },

      pvxGaugeLive(n) {
        if (!n || n.stale) return false;
        const st = (n.status && n.status.value) || '';
        return st === 'running' || st === 'online';
      },

      pvxGauge(n, which) {
        const pct = pvxGaugePct(n, which);
        const live = this.pvxGaugeLive(n);
        if (pct === null) {
          return { measured: false, live, pct: 0, tier: 'ok', text: '—', reason: pvxNoMeasureReason(n, which) };
        }
        const key = (n.id || '') + ':' + which;
        const tier = this.pvxTier(pct, hysteresisTiers.get(key));
        hysteresisTiers.set(key, tier);
        return { measured: true, live, pct, tier, text: pvxGaugeText(this, n, which, pct), reason: '' };
      },
      pvxGaugeColor(m) {
        if (!m || !m.measured || !m.live) return '#64748b';
        return m.tier === 'critical' ? '#ef4444' : (m.tier === 'warning' ? '#f59e0b' : '#22c55e');
      },
      pvxGaugeStyle(m) {
        const v = Math.max(0, Math.min(100, Number(m && m.pct) || 0));
        return `width:${v}%;background:${this.pvxGaugeColor(m)}`;
      },
      pvxRate(v) {
        if (v === null || v === undefined) return '—';
        if (Number(v) < 0) return 'no baseline';
        return this.pvxBytes(v) + '/s';
      },
      pvxObs(o) { return (o && o.value !== undefined) ? o.value : null; },
      pvxField(obj, name) { return this.pvxObs(obj && obj[name]); },

      pvxBusy() { return !!this.pvx.busy; },

      async pvxOpen(n) {
        if (this.pvx.open === n.id) { this.pvxClose(); return; }
        this.pvxCloseConsole();
        this.pvx.open = n.id;
        this.pvx.detail = null;
        this.pvx.snaps = [];
        this.pvx.guestSel = (n.kind === 'guest' && n.vmid > 0) ? n.id : '';
        try {
          const r = await this.api('/api/nodes/' + n.id);
          this.pvx.detail = await r.json().catch(() => null);
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        }
        if (this.pvx.guestSel) this.pvxLoadSnaps(this.pvx.guestSel);
        this.pvxLoadNote(n.id);
      },
      pvxClose() {
        this.pvxCloseConsole();
        this.pvx.open = '';
        this.pvx.detail = null;
        this.pvx.snaps = [];
        this.pvx.guestSel = '';
      },
      pvxOpenNode() {
        const id = this.pvx.open;
        return id ? (this.pvxNodes().find(n => n.id === id) || null) : null;
      },

      pvxSelected(id) { return this.pvx.sel.indexOf(id) >= 0; },
      pvxToggleSel(id) {
        const i = this.pvx.sel.indexOf(id);
        if (i >= 0) this.pvx.sel.splice(i, 1);
        else this.pvx.sel.push(id);
      },
      pvxSelAll() {
        const visible = this.pvxFilteredNodes().map(n => n.id);
        this.pvx.sel = (this.pvx.sel.length === visible.length) ? [] : visible;
      },
      pvxBulk() { return this.pvx.sel.length > 0; },
      pvxSelNodes() {
        const ids = this.pvx.sel;
        return this.pvxNodes().filter(n => ids.indexOf(n.id) >= 0);
      },

      pvxPowerBulk(action) {
        const labels = { start: 'Turn on', shutdown: 'Shut down gracefully', stop: 'Cut the power' };
        const sel = this.pvxSelNodes();
        const targets = sel.filter(n => this.pvxActionState(n, action).can);
        const fora = sel.filter(n => !this.pvxActionState(n, action).can);
        if (!targets.length) {
          this.showToast('none of the selected nodes accepts "' + action + '" right now', 'err');
          return;
        }
        const names = targets.map(n => `${n.name} (${n.id})`).join(', ');
        const skipped = fora.length
          ? `\n\nLEFT OUT (${fora.length}): ` + fora.map(n => `${n.name} — ${this.pvxActionState(n, action).reason}`).join('; ')
          : '';
        const step = this.pvxStep(action, targets.length);
        const warning = (action === 'stop'
          ? `Cuts the power to ${targets.length} node(s) outright — the same as pulling the cable on each one.`
          : `Runs "${action}" on ${targets.length} node(s), one at a time, waiting for the hypervisor to finish each task.`)
          + `\n\nTHEY ARE: ${names}` + skipped
          + (step >= 3 ? `\n\nType IN BULK to confirm.` : '');
        const options = step >= 3 ? { danger: true, requireText: 'IN BULK' } : {};
        this.askConfirm(labels[action] + ` — ${targets.length} node(s)`, warning, async () => {
          for (const n of targets) {
            await this.pvxPowerNow(n, action);
          }
          this.pvx.sel = [];
          await this.pvxReload();
        }, options);
      },

      pvxPower(n, action) {
        const state = this.pvxActionState(n, action);
        if (!state.can) { this.showToast(state.reason, 'err'); return; }
        const step = this.pvxStep(action, 1);
        if (step <= 1) { this.pvxPowerNow(n, action); return; }
        const labels = { shutdown: 'Shut down gracefully', stop: 'Cut the power', reboot: 'Restart' };
        let warning;
        if (action === 'stop') {
          warning = `Cuts the power to "${n.name}" outright — the same as pulling the cable. `
            + `Whatever is in memory and has not been written is lost. `
            + `Type the node name (${n.name}) to confirm.`;
        } else if (action === 'reboot') {
          warning = `Restarts "${n.name}" from the inside (the system receives the request and reboots itself). `
            + `Everything running on it is offline until it comes back. `
            + `If the system is stuck it may IGNORE the request — the task then fails `
            + `and the guest stays up, and then the way out is "Cut the power".`;
        } else {
          warning = `Runs "${action}" on "${n.name}". The response only comes back once the hypervisor has finished the task.`;
        }
        const options = step >= 3 ? { danger: true, requireText: n.name } : {};
        this.askConfirm(labels[action] || action, warning, () => this.pvxPowerNow(n, action), options);
      },

      async pvxPowerNow(n, action) {
        if (this.pvx.busy && this.pvx.busy !== n.id) return;
        this.pvx.busy = n.id;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + n.id + '/power', {
            method: 'POST', body: JSON.stringify({ action: action }),
          });
          const d = await r.json().catch(() => ({}));
          this.showToast(`${n.name}: ${action} finished (task ${d.upid || '—'})`, 'ok');
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.showToast(`${n.name}: ${action} failed: ${txt}`, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxReload();
        }
      },

      pvxRevoke(n) {
        const state = this.pvxActionState(n, 'revoke');
        if (!state.can) { this.showToast(state.reason, 'err'); return; }
        this.askConfirm('Revoke the credential',
          `Deletes the token for "${n.name}" on the hypervisor AND in the vault, in that order. `
          + `Irreversible: the node is left with no credential until someone runs the applier again — `
          + `and with no credential the panel cannot power on, power off or open a console on this node. `
          + `Type the node name (${n.name}) to confirm.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = n.id;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/nodes/' + n.id + '/credential', { method: 'DELETE' });
              const d = await r.json().catch(() => ({}));
              this.showToast('credential revoked (' + (d.steps || []).join(' → ') + ')', 'ok');
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('revocation failed: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              await this.pvxReload();
            }
          }, { danger: true, requireText: n.name });
      },

      async pvxReload() {
        if (typeof this.loadNodes === 'function') await this.loadNodes();
        const open = this.pvx.open;
        this.pvx.sel = this.pvxRescope(this.pvx.sel, this.pvxFilteredNodes());
        if (open && this.pvx.guestSel) await this.pvxLoadSnaps(this.pvx.guestSel);
      },

      pvxNeedYou() {
        return this.pvxNodes().filter(n => {
          const e = this.pvxNodeState(n);
          return e === 'stale' || e === 'no-credential' || e === 'critical';
        });
      },

      pvxEmpty() {
        if (this.pvx.forbidden || (this.nodes && this.nodes.forbidden)) return 'no-permission';
        if (this.nodes && this.nodes.lastError) return 'failed';
        if (!this.pvxNodes().length) return 'nothing';
        if (!this.pvxFilteredNodes().length) return 'filtered';
        return '';
      },

      pvxCadence() {
        const ttl = Number(this.pvx.ttl) || 90;
        return Math.max(5000, Math.round((ttl * 1000) / 3));
      },
      pvxScrolled() {
        this.pvx.rolando = true;
        if (this._pvxRolTimer) clearTimeout(this._pvxRolTimer);
        this._pvxRolTimer = setTimeout(() => { this.pvx.rolando = false; }, 1500);
      },

      pvxStartPoll() {
        if (this.pvxPollTimer) return;
        const bate = () => {
          this.pvxPollTimer = setTimeout(bate, this.pvxCadence());
          if (document.hidden) return;
          if (this.currentView !== 'proxmox') return;
          if (this.pvx.rolando) return;
          this.pvxLoadHealth();
          this.pvxLoadTasks();
          this.pvxLoadStorage();
          this.pvxLoadZfs();
          if (typeof this.loadNodes === 'function') this.loadNodes();
        };
        this.pvxPollTimer = setTimeout(bate, this.pvxCadence());
      },
      pvxStopPoll() {
        if (this.pvxPollTimer) { clearTimeout(this.pvxPollTimer); this.pvxPollTimer = null; }
        if (this._pvxRolTimer) { clearTimeout(this._pvxRolTimer); this._pvxRolTimer = null; }
        this.pvx.rolando = false;
        this.pvxCloseConsole();
      },
    };
  };
})();
