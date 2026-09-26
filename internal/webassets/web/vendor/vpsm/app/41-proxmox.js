// 41-proxmox.js — the SINGLE lab screen (Operations → Proxmox).
// IIFE exposing window.VPSMProxmoxModule(); the object is spread inside
// app() in 00-shell.js, so `this` here is the Alpine component
// (this.api, this.showToast, this.askConfirm, this._apiError and
// this._errText already exist).
// Cast from: 40-nodes.js.
//
// ────────────────────────────────────────────────────────────────────────────
// 🔴 THE "Nodes" TAB WAS MERGED IN HERE. The node is the axis.
//
// They were two screens about the same subject: one listed the nodes without
// ever saying what they were consuming, the other showed the hypervisor as if
// it were not a node. The operator had to remember which of the two held the
// thing he wanted — and the answer changed with the question.
//
// Now it is one screen: the hypervisor is the HEADER and the guests are the
// rows. With one node and nine guests, a Datacenter→Node→Guest tree would
// spend a whole column of the screen to express a one-item hierarchy; the
// design is a dense list with a gauge embedded in every row.
//
// This ANTICIPATES the planned "node as the primary axis, tabs filtered by
// caps". The missing half is the `caps` half, which depends on the lab agent
// that does not exist yet — which is why the requirement is NOT called done.
//
// The module stays SEPARATE from 40-nodes.js, and now for another reason: that
// file is the DATA layer (fetch from the server, translate the four credential
// states), this one is the SCREEN layer. Re-copying `nodesCredLabel` here would
// create two truths about what a revoked credential is.
//
// 🔴 THE BROWSER CLOCK DOES NOT COME IN HERE.
//
// The age arrives ready-made from the server (`age_seconds`), for the same
// reason written in the header of 40-nodes.js: this project spans two machines
// and a tailnet, and the clock on the operator’s machine is not a controlled
// variable. A fast clock would make everything look expired; a slow one would
// show a mute hypervisor as if it were live.
//
// 🔴 pvxFormatAge is a DELIBERATE COPY of nodesFormatAge (40-nodes.js).
//
// It is not an import, and not `this.nodesFormatAge`. Both modules land in the
// SAME Alpine component, so calling the neighbour’s WOULD WORK — and would
// create a silent dependency between tabs: renaming a method in 40-nodes.js
// would break the Proxmox tab with no build error and no test. The copy is
// honest because scripts/test-proxmox-tab.mjs extracts BOTH functions from the
// served files, runs both and fails if they diverge.
// ────────────────────────────────────────────────────────────────────────────
(function () {
  // Gauge hysteresis memory, keyed by "id:gauge". Outside the component on
  // purpose — see the comment in pvxMedidor.
  const tiersDeHisterese = new Map();

  // ── gauge helpers, OUTSIDE the object ────────────────────────────────────
  //
  // They live here because they do not depend on the component and because
  // `pvxNoEstado` needs them: as methods, `pvxNoEstado` would have to read `this`
  // and would stop being extractable by the harness. A rule that is only testable
  // once you stand up a whole Alpine component is not tested.

  // 🔴 pvxPctDoMedidor returns null — not 0 — when there is no measurement.
  // `disk_used` from QEMU without a guest agent arrives as -1 (NaoReportado, from
  // the server), and 0% would draw a roomy disk over a number nobody measured.
  // null is absence saying its own name.
  function pvxPctDoMedidor(n, qual) {
    if (!n) return null;
    const val = (o) => (o && o.value !== undefined && o.value !== null) ? Number(o.value) : null;
    const carimbado = (o) => !!(o && o.observed_at > 0);
    if (qual === 'cpu') {
      if (!carimbado(n.cpu_frac)) return null;
      const f = val(n.cpu_frac);
      return (f === null || f < 0) ? null : f * 100;
    }
    const par = qual === 'ram' ? [n.mem_used, n.mem_total] : [n.disk_used, n.disk_total];
    if (!carimbado(par[0])) return null;
    const u = val(par[0]), t = val(par[1]);
    if (u === null || u < 0 || t === null || t <= 0) return null;
    return (u / t) * 100;
  }

  function pvxMotivoSemMedida(n, qual) {
    if (!n) return 'no data';
    const carimbo = qual === 'cpu' ? n.cpu_frac : (qual === 'ram' ? n.mem_used : n.disk_used);
    if (!carimbo || !carimbo.observed_at) return 'the panel has not observed this node yet';
    if (qual === 'disco') {
      // The concrete case in this house, measured: qemu/100 and qemu/208 return
      // `disk: 0` because the guest agent is not installed. Saying "0%" there would
      // be asserting disk headroom over a question with no answer.
      return 'not reported — QEMU without a guest agent does not report disk usage';
    }
    return 'not reported';
  }

  function pvxTextoDoMedidor(comp, n, qual, pct) {
    if (qual === 'cpu') {
      // 🔴 DO NOT repeat the percentage here. The screen already shows the number
      // next to the bar; returning "0.8% of 2 core(s)" produced TWO percentages on
      // the same line, rounded differently ("1%" and "0.8%"), and the phrase was
      // still long enough to truncate into "0.8% of 2 cor…".
      // The text complements the number, it does not duplicate it.
      const nucleos = (n.cpu_cores && n.cpu_cores.value) || 0;
      if (!nucleos) return 'cores not reported';
      return nucleos === 1 ? '1 core' : `${nucleos} cores`;
    }
    const par = qual === 'ram' ? [n.mem_used, n.mem_total] : [n.disk_used, n.disk_total];
    return comp.pvxBytes(par[0] && par[0].value) + ' / ' + comp.pvxBytes(par[1] && par[1].value);
  }

  window.VPSMProxmoxModule = function () {
    return {
      pvx: {
        saude: null,        // HypervisorView exactly as the server delivered it
        ttl: 90,
        tasks: [],
        soErros: true,      // the filter that reveals the failures nobody sees
        taskLog: null,      // { upid, lines } while the modal is open
        logAberto: false,
        disks: [],
        storage: null,      // { pools, datastore_audit:{value,observed_at}, age_seconds, stale }
        zfs: null,          // { pools, age_seconds, stale }
        perms: null,        // { permissions, storage_visivel }
        permsAberto: false,
        snaps: [],
        novoSnap: '',       // name typed for the new snapshot
        guestSel: '',       // guest id ("lxc/204")
        loading: false,
        primeiraCarga: true, // while true the table draws a skeleton, not a spinner
        forbidden: false,
        lastError: '',
        busy: '',           // operation in flight (re-entrancy guard)

        // ── the merged screen ────────────────────────────────────────────
        filtro: '',         // `field:value` filter text, ANDed
        segmento: '',       // active health-band segment; '' = none
        sel: [],            // ids selected for bulk action
        aberto: '',         // id of the node whose side panel is open
        detalhe: null,      // { node, services, deployments, jobs } of the open node
        // 🔴 `rolando` is a BOOLEAN armed by setTimeout, never an instant.
        // The temptation was to store "do not refresh until T" and compare it with
        // the clock — and this file reads no clock at all, because the rule that
        // forbids computing age in the browser admits no "just this once" exception.
        rolando: false,

        // Remote console
        con: {
          guest: '',        // id of the guest with the console open ("lxc/204")
          estado: 'closed',// fechado | abrindo | ligado | erro
          erro: '',
        },

        // Master-detail. The operator chose layout A2 and ONLY it; A5 — which was A2
        // plus a fixed exceptions band — was rejected. So there is NO exceptions band
        // on this screen.
        aba: '',            // active tab of the right-hand panel
        pausado: false,     // live cycle suspended?
        pausaMotivo: '',    // why, in plain words, so the screen can say it
        filtroFoco: false,  // does the filter have the cursor inside it?
        idadeSeg: null,     // age of the data, coming FROM THE SERVER
        // 🔴 STATE IS BORN WITH A SHAPE, NEVER NULL.
        //
        // The previous version initialised all of this with `null`, and the template
        // dereferenced it (`pvx.serie.pontos`, `pvx.sistema.network_erro`). Before the
        // first load — which is the state the screen ALWAYS opens in — that throws, and
        // a throw inside Alpine takes down the WHOLE app: the Proxmox tab dragged the
        // terminal down with it, and the operator was locked out of the only remote
        // access he has.
        //
        // Leaning on `?.` in every expression is fragile: the next expression somebody
        // writes may forget it. A stable shape in the state removes the entire class at
        // the source — `pvx.serie.pontos` becomes always safe.
        //
        // "Has it loaded yet?" moved house: it used to be the nullness of the field
        // itself, now it is `carregado`. The two questions are different, and mixing
        // them is what created the defect.
        carregado: {},

        // Maintenance. Born with a SHAPE, never null: the template reads
        // `pvx.clone.novoID` before any load, which is the state the screen ALWAYS
        // opens in.
        clone: { aberto: false, carregando: false, origem: '', origemNome: '', novoID: 0, nome: '', ligado: false,
                 precisaSnap: false, snapshots: [], snapshot: '', erro: '' },
        bkp: { storage: '', modo: 'snapshot' },
        // The note that EXPLAINS the node. Born with a shape; `carregando` and `origem`
        // separate the three states the screen has to tell apart: I have not read it
        // yet, I read it and it is empty, I read it and it is not from this hypervisor.
        nota: { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '',
                editando: false, rascunho: '', salvando: false },

        backup: { datastores: [] },     // freshness per layer — see pvxBackups()
        topologia: { pools: [] },       // pool topology
        serie: { pontos: [], escopo: '', janela: '' },
        serieCarregando: false,
        janela: 'hour',
        sistema: {},                    // network, DNS, time, certificates
        pacotes: { pacotes: [] },
        registro: { linhas: [] },       // syslog
        filtroPacote: '',
      },

      // ── HYPERVISOR power ──────────────────────────────────────────────────
      //
      // 🔴 THE ONLY PANEL ACTION WHOSE MISTAKE HAS NO REMOTE UNDO.
      //
      // This machine has no IPMI. Wake-on-LAN is no help either: the thing routing
      // the admin network is the host itself (it runs the Tailscale subnet router),
      // so when it goes down there is nowhere to wake it from. Powering it back on
      // means walking to the machine.
      //
      // The operator asked for this knowing the risk — that is on record. What the
      // screen owes him is the CONCRETE CONSEQUENCE up front, not a generic warning:
      // how many guests fall, which ones, and that the way back is physical.
      pvxGuestsLigados() {
        return this.pvxNos().filter((n) => {
          if (!this.pvxEhGuest(n)) return false;
          const st = (n.status && n.status.value) || '';
          return st === 'running' || st === 'online';
        });
      },
      pvxEnergiaHost(cmd) {
        const n = this.pvxNoAberto();
        if (!n || !this.pvxEhHost(n)) return;
        const rotulo = n.name || n.id;
        const ligados = this.pvxGuestsLigados();
        const lista = ligados.map((g) => (g.name || g.id)).join(', ') || 'none';

        // The sentence changes with the command because the RISK changes. Rebooting is
        // betting the machine comes back; shutting down guarantees it will not by itself.
        const titulo = cmd === 'reboot' ? 'Restart the hypervisor' : 'POWER OFF the hypervisor';
        const volta = cmd === 'reboot'
          ? 'If it does not come back — a new kernel that fails to boot, a pool that fails to import, a stuck fsck — the outcome is the same as a power off: somebody has to be standing in front of the machine.'
          : 'It will NOT come back on its own. There is no IPMI, and Wake-on-LAN is useless because the machine itself routes the admin network. Powering it back on means walking up to it.';

        this.askConfirm(titulo,
          `${ligados.length} running guest(s) will go down with it: ${lista}.\n\n` +
          `${volta}\n\n` +
          `Once sent, the panel loses contact with "${rotulo}" and there is no undo from here. ` +
          `Type the hypervisor name (${rotulo}) to confirm.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = n.id;
            this.pvx.lastError = '';
            try {
              const d = await (await this.api('/api/proxmox/power?command=' + encodeURIComponent(cmd),
                { method: 'POST' })).json().catch(() => ({}));
              const quantos = (d.guests_afetados || []).length;
              this.showToast(
                (cmd === 'reboot' ? 'restart' : 'power off') + ' accepted by the hypervisor' +
                (quantos ? ` — ${quantos} guest(s) going down with it` : '') +
                (d.upid ? ` (task ${d.upid})` : ''), 'ok');
              // No point going on polling something that is dying: the pause avoids a flood
              // of network errors on screen while the machine goes down.
              this.pvx.pausado = true;
              this.pvx.pausaMotivo = 'hypervisor ' + (cmd === 'reboot' ? 'restarting' : 'powering off');
              this.pvxParaTimer();
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('the hypervisor refused the command: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
            }
          },
          { danger: true, requireText: rotulo });
      },

      // ── hypervisor Summary panel ──────────────────────────────────────────
      //
      // Mirrors what the Proxmox screen shows at the top, because the operator CANNOT
      // reach that UI: CPU, IO delay, load, RAM, KSM, root disk, swap, processor
      // model, kernel, PVE version and uptime.
      pvxSaudeObs(campo) {
        const h = this.pvx.saude;
        return h ? h[campo] : null;
      },
      pvxVal(campo) {
        const o = this.pvxSaudeObs(campo);
        return o && o.observed_at ? o.value : null;
      },
      // A number with no timestamp is a number nobody observed. Returning zero there
      // would make the screen say "0% CPU" about a mute hypervisor.
      pvxNum(campo, casas) {
        const v = this.pvxVal(campo);
        return v === null || v === undefined ? '—' : Number(v).toFixed(casas === undefined ? 1 : casas);
      },
      pvxPctDe(usadoCampo, totalCampo) {
        const u = this.pvxVal(usadoCampo), t = this.pvxVal(totalCampo);
        if (u === null || t === null || !(Number(t) > 0)) return null;
        return (Number(u) / Number(t)) * 100;
      },
      // Hypervisor fractions 0..1 become percentages only at presentation time.
      // Storing percentages in the state would create two conventions for one quantity.
      pvxFracPct(campo) {
        const v = this.pvxVal(campo);
        return v === null || v === undefined ? null : Number(v) * 100;
      },
      pvxBarra(pct, teto) {
        if (pct === null) return 'width:0%;background:#64748b';
        const v = Math.max(0, Math.min(100, pct));
        const t = teto === undefined ? 85 : teto;
        const c = v >= t ? '#ef4444' : v >= t * 0.8 ? '#f59e0b' : '#22c55e';
        return `width:${v}%;background:${c}`;
      },
      pvxUptimeLongo() {
        const s2 = this.pvxVal('uptime');
        if (s2 === null) return '—';
        const d = Math.floor(s2 / 86400), h = Math.floor((s2 % 86400) / 3600), m = Math.floor((s2 % 3600) / 60);
        return d > 0 ? `${d}d ${h}h ${m}min` : `${h}h ${m}min`;
      },
      pvxLoadTrio() {
        const l = this.pvxVal('load');
        return Array.isArray(l) && l.length === 3 ? l.map((x) => Number(x).toFixed(2)) : null;
      },
      // 🔴 Load only means something DIVIDED by the cores. Load 8 on a 16-thread
      // server is half the capacity; that same 8 on a 2-thread box is a queue four
      // times deeper than the machine can take. Proxmox shows the raw number and
      // leaves the arithmetic to the reader; here the arithmetic is done.
      pvxLoadRelativo() {
        const l = this.pvxLoadTrio(), n = this.pvxVal('cpu_cores');
        if (!l || !(Number(n) > 0)) return null;
        return (Number(l[0]) / Number(n)) * 100;
      },

      // ── time series and charts ────────────────────────────────────────────
      //
      // 🔴 NO LIBRARY. The panel’s CSP is `script-src 'self'`, so no Chart.js gets in
      // — and it is no loss: an area chart with a grid and a highlighted final point
      // is ~40 lines of SVG, against 200 KB of dependency that then needs security
      // updates forever.
      //
      // The chart is the centrepiece of the Proxmox screen, and it is what separates
      // "the dev is at 83% RAM" from "the dev has been climbing for three hours". The
      // second lets you act in time; the first only states a fact.
      JANELAS: [
        { id: 'hour',  rot: '1 h' },
        { id: 'day',   rot: '1 day' },
        { id: 'week',  rot: '1 week' },
        { id: 'month', rot: '1 month' },
        { id: 'year',  rot: '1 year' },
      ],
      // Drawable metrics. `pct` says whether the axis is a fixed 0-100 (percentage)
      // or scaled to the observed maximum — scaling a percentage would make 2% CPU
      // look like a spike, and that is the classic automatic-charting mistake.
      METRICAS_NO: [
        { id: 'cpu',        rot: 'CPU',        pct: true,  cor: '#38bdf8', fmt: 'pct' },
        { id: 'iowait',     rot: 'IO delay',   pct: true,  cor: '#f59e0b', fmt: 'pct' },
        { id: 'loadavg',    rot: 'Load',       pct: false, cor: '#a78bfa', fmt: 'num' },
        { id: 'memused',    rot: 'Memory',    pct: false, cor: '#22c55e', fmt: 'bytes', teto: 'memtotal' },
        { id: 'arcsize',    rot: 'ZFS ARC', pct: false, cor: '#2dd4bf', fmt: 'bytes', teto: 'memtotal' },
        { id: 'rootused',   rot: 'Disk /',    pct: false, cor: '#94a3b8', fmt: 'bytes', teto: 'roottotal' },
        { id: 'netin',      rot: 'Network ↓',     pct: false, cor: '#38bdf8', fmt: 'taxa' },
        { id: 'netout',     rot: 'Network ↑',     pct: false, cor: '#f472b6', fmt: 'taxa' },
        { id: 'pressureiosome',     rot: 'IO pressure',     pct: true, cor: '#fb923c', fmt: 'pct' },
        { id: 'pressurememorysome', rot: 'Memory pressure', pct: true, cor: '#e879f9', fmt: 'pct' },
      ],
      METRICAS_GUEST: [
        { id: 'cpu',       rot: 'CPU',      pct: true,  cor: '#38bdf8', fmt: 'pct' },
        { id: 'mem',       rot: 'Memory',  pct: false, cor: '#22c55e', fmt: 'bytes', teto: 'maxmem' },
        { id: 'disk',      rot: 'Disk',    pct: false, cor: '#94a3b8', fmt: 'bytes', teto: 'maxdisk' },
        { id: 'netin',     rot: 'Network ↓',   pct: false, cor: '#38bdf8', fmt: 'taxa' },
        { id: 'netout',    rot: 'Network ↑',   pct: false, cor: '#f472b6', fmt: 'taxa' },
        { id: 'diskread',  rot: 'Read',  pct: false, cor: '#a78bfa', fmt: 'taxa' },
        { id: 'diskwrite', rot: 'Write',  pct: false, cor: '#fb923c', fmt: 'taxa' },
      ],
      pvxMetricas() {
        const n = this.pvxNoAberto();
        return this.pvxEhGuest(n) ? this.METRICAS_GUEST : this.METRICAS_NO;
      },
      pvxSerieAlvo() {
        const n = this.pvxNoAberto();
        return this.pvxEhGuest(n) ? n.id : '';
      },
      async pvxLoadSerie() {
        const alvo = this.pvxSerieAlvo();
        const q = '?janela=' + encodeURIComponent(this.pvx.janela) + (alvo ? '&node=' + encodeURIComponent(alvo) : '');
        this.pvx.serieCarregando = true;
        try {
          const r = await this.api('/api/proxmox/rrd' + q, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.serie = await r.json();
          this.pvx.carregado.serie = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          // Stable shape in the error path too: see pvxCarrega.
          this.pvx.serie = { pontos: [], escopo: '', janela: this.pvx.janela };
        } finally {
          this.pvx.serieCarregando = false;
        }
      },
      pvxTrocaJanela(j) { this.pvx.janela = j; this.pvxLoadSerie(); },

      // ── the drawing ───────────────────────────────────────────────────────
      //
      // A fixed viewBox + width:100% scales the chart without recomputing anything.
      // The stroke uses `vector-effect: non-scaling-stroke` because the scaling is
      // NON-uniform: without it the line fattens horizontally and thins vertically.
      GW: 600, GH: 120,
      // 🔴 A HOLE IN THE SERIES IS A HOLE. The RRD returns a missing sample when the
      // hypervisor was off or the data did not exist yet. Bridging over it would turn
      // a power cut into a straight line, and filling it with zero would turn it into
      // a period of idleness. The drawing BREAKS the stroke.
      pvxDesenho(metrica) {
        const d = this.pvx.serie;
        // 🔴 ONE SHAPE ONLY, always. The previous version returned TWO different
        // objects: the full one had `fimX`/`fimY`, the empty one did not. The template
        // reads `:cx="pvxDesenho(m).fimX"` outside any guard, so in the empty state
        // the attribute got `undefined` and the SVG refused it with
        // "attribute cx: Unexpected end of attribute". The same disease as state born
        // null, one level down — in the return value of a function.
        const vazio = {
          temDado: false, area: '', traco: '', pontos: '',
          teto: 1, max: 0, min: 0, ultimo: null, fimX: 0, fimY: 0, buracos: 0, n: 0,
        };
        if (!d || !Array.isArray(d.pontos) || !d.pontos.length) return vazio;
        const pts = d.pontos;
        const vals = pts.map((p) => {
          const v = p[metrica.id];
          return v === undefined || v === null || Number.isNaN(Number(v)) ? null : Number(v);
        });
        const presentes = vals.filter((v) => v !== null);
        if (!presentes.length) return vazio;

        const t0 = Number(pts[0].time), t1 = Number(pts[pts.length - 1].time);
        const dur = Math.max(1, t1 - t0);
        // Axis ceiling: 100 for percentages; for everything else, the larger of the
        // observed peak and the point’s own ceiling field (memtotal, maxdisk…), which
        // is what makes "5 GB of RAM" mean different things on a 2 GB guest and on a
        // 64 GB one.
        let teto;
        if (metrica.pct) {
          teto = 1; // PVE fractions arrive as 0..1
        } else {
          teto = Math.max(...presentes);
          if (metrica.teto) {
            const tetos = pts.map((p) => Number(p[metrica.teto])).filter((v) => v > 0);
            if (tetos.length) teto = Math.max(teto, Math.max(...tetos));
          }
          if (!(teto > 0)) teto = 1;
        }
        const X = (t) => ((Number(t) - t0) / dur) * this.GW;
        const Y = (v) => this.GH - Math.min(1, Math.max(0, v / teto)) * this.GH;

        const segmentos = [];
        let atual = [];
        let buracos = 0;
        for (let i = 0; i < pts.length; i++) {
          if (vals[i] === null) {
            if (atual.length) { segmentos.push(atual); atual = []; buracos++; }
            continue;
          }
          atual.push([X(pts[i].time), Y(vals[i])]);
        }
        if (atual.length) segmentos.push(atual);

        const dPath = (seg) => seg.map(([x, y], i) => (i ? 'L' : 'M') + x.toFixed(1) + ' ' + y.toFixed(1)).join(' ');
        // The area only closes over continuous segments: one single area drawn across
        // a hole would paint the gap as if there were data in it.
        const area = segmentos
          .filter((s) => s.length > 1)
          .map((s) => dPath(s) + ` L${s[s.length - 1][0].toFixed(1)} ${this.GH} L${s[0][0].toFixed(1)} ${this.GH} Z`)
          .join(' ');

        const ultimoIdx = vals.map((v, i) => (v === null ? -1 : i)).filter((i) => i >= 0).pop();
        // 🔴 ONE <path> ONLY, WITH SEVERAL SUBPATHS — not one <path> per segment.
        //
        // The original intent ("one path per segment so the stroke breaks at the
        // hole") was right; the means were wrong. It required `x-for`, and the `x-for`
        // sat INSIDE the <svg>: there `<template>` is not the HTML element, it is an
        // unknown SVG element — with no `.content` and not inert. Alpine threw inside
        // importNode and rendered the children anyway, with the loop variable out of
        // scope. The charts broke whenever there was DATA, which is exactly when
        // nobody tests.
        //
        // SVG already solves this with no loop at all: every `M` starts a NEW subpath,
        // and the stroke does not join the end of one to the start of the next. Joining
        // the segments into a single `d` gives exactly the same drawing — a stroke
        // broken at the hole — with no template, no loop and no error.
        const traco = segmentos.filter((s) => s.length > 1).map(dPath).join(' ');
        // A lone point (a single sample surrounded by holes) drawn as a zero-length
        // subpath: with `stroke-linecap="round"` the renderer paints a disc. It comes
        // free for the same reason: no loop.
        const pontos = segmentos
          .filter((s) => s.length === 1)
          .map((s) => `M${s[0][0].toFixed(1)} ${s[0][1].toFixed(1)} L${s[0][0].toFixed(1)} ${s[0][1].toFixed(1)}`)
          .join(' ');
        return {
          temDado: true,
          traco,
          pontos,
          area,
          teto,
          max: Math.max(...presentes),
          min: Math.min(...presentes),
          ultimo: vals[ultimoIdx],
          fimX: X(pts[ultimoIdx].time),
          fimY: Y(vals[ultimoIdx]),
          buracos,
          n: presentes.length,
        };
      },
      // Formatting per metric type. A raw number with no unit forces the operator to
      // guess whether 0.83 is 83% or a load of 0.83.
      pvxValorMetrica(metrica, v) {
        if (v === null || v === undefined) return '—';
        switch (metrica.fmt) {
          case 'pct':   return (Number(v) * 100).toFixed(1) + '%';
          case 'bytes': return this.pvxBytes(Number(v));
          case 'taxa':  return this.pvxBytes(Number(v)) + '/s';
          default:      return Number(v).toFixed(2);
        }
      },
      pvxRotuloJanela() {
        const j = this.JANELAS.find((x) => x.id === this.pvx.janela);
        return j ? j.rot : this.pvx.janela;
      },
      // Grid marks at 25/50/75%. Faint on purpose: the grid orients, it does not
      // compete with the data.
      GRADE: [0.25, 0.5, 0.75],

      // ── storage: physical disk, pool and storage become ONE story ─────────
      //
      // 🔴 They were three lists with no connection at all. "Disks" said a 1 TB Lexar
      // NVMe exists; "ZFS pools" said an `rpool` with 70 GB allocated exists;
      // "Storage" said a `pbs` datastore exists. Nothing said one lives inside the
      // other — and in a SINGLE-DISK lab with NO MIRROR the question that matters is
      // exactly that one:
      // "what do I lose if THIS disk dies?".
      pvxTopologiaDe(nome) {
        const ps = (this.pvx.topologia && this.pvx.topologia.pools) || [];
        return ps.find((p) => p.nome === nome) || null;
      },
      // Matches physical disk ↔ pool by the serial embedded in the by-id path.
      //
      // This lab’s NVMe is addressed as `nvme-eui.…`, which does NOT carry a readable
      // serial. In that case the serial match does not happen and the function returns
      // empty — rather than guessing by type and risking tying the pool to the wrong
      // disk, which is worse than not tying it at all.
      pvxSerialDoCaminho(caminho) {
        let base = String(caminho || '');
        const i = base.lastIndexOf('/');
        if (i >= 0) base = base.slice(i + 1);
        base = base.replace(/-part\d+$/, '').replace(/-0:0$/, '');
        if (base.startsWith('nvme-eui.')) return '';
        const j = base.lastIndexOf('_');
        return j >= 0 ? base.slice(j + 1) : '';
      },
      pvxPoolsDoDisco(d) {
        if (!d) return [];
        const serial = String(d.serial || '');
        const ps = (this.pvx.topologia && this.pvx.topologia.pools) || [];
        const achados = [];
        for (const p of ps) {
          for (const v of p.vdevs || []) {
            for (const disp of v.dispositivos || []) {
              const s = this.pvxSerialDoCaminho(disp.caminho);
              if (s && serial && s === serial && !achados.includes(p.nome)) achados.push(p.nome);
            }
          }
        }
        return achados;
      },
      // SSD lifetime. 🔴 MEASURED at the Proxmox source, not assumed:
      // Diskmanage.pm:156 does `wearout = 100 - Percentage Used`, i.e. the field is
      // REMAINING life. Showing this as "wear" would invert the meaning and make a
      // new disk look end-of-life — on a single-disk server that is the difference
      // between calm and panic.
      pvxVidaUtil(d) {
        if (!d || d.wearout_pct === null || d.wearout_pct === undefined) {
          return { medido: false, texto: 'not reported', motivo: 'this disk exposes no wear indicator (common on spinning disks and USB enclosures)' };
        }
        const pct = Number(d.wearout_pct);
        return {
          medido: true, pct,
          texto: pct.toFixed(0) + '% of life left',
          motivo: 'SMART: ' + (100 - pct).toFixed(0) + '% of the endurance already used',
        };
      },
      pvxEstiloVida(d) {
        const v = this.pvxVidaUtil(d);
        if (!v.medido) return 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66';
        const c = v.pct <= 10 ? '#ef4444' : v.pct <= 25 ? '#f59e0b' : '#22c55e';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxEstiloSaude(h) {
        const c = h === 'PASSED' || h === 'OK' ? '#22c55e' : (!h || h === 'UNKNOWN') ? '#64748b' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      // 🔴 The redundancy verdict is DERIVED from the topology, never asserted by
      // fixed text. A screen that says "no mirror" by hardcode lies the day the
      // second NVMe goes in — and the day of the change is exactly when the operator
      // most needs the screen to be right.
      pvxRedundancia(nomePool) {
        const t = this.pvxTopologiaDe(nomePool);
        if (!t) return { conhecida: false, texto: 'topology not read', estilo: 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66' };
        if (t.redundante) {
          const tipos = [...new Set((t.vdevs || []).filter((v) => v.redundante).map((v) => v.tipo))];
          return { conhecida: true, protegido: true, texto: tipos.join(' + ') + ' · survives the loss of one disk',
                   estilo: 'background:#22c55e22;color:#22c55e;border:1px solid #22c55e66' };
        }
        return { conhecida: true, protegido: false,
                 texto: t.n_dispositivos === 1 ? 'single disk · NO redundancy' : t.n_dispositivos + ' striped disks · NO redundancy',
                 estilo: 'background:#f59e0b22;color:#f59e0b;border:1px solid #f59e0b66' };
      },
      // Error counters. In a pool with no mirror, any non-zero one is lost data:
      // there is no second copy to rebuild from.
      pvxErrosDoPool(nomePool) {
        const t = this.pvxTopologiaDe(nomePool);
        if (!t) return { conhecido: false };
        return { conhecido: true, n: t.erros_contados || 0, texto: t.erros || '' };
      },
      pvxDispositivosDoPool(nomePool) {
        const t = this.pvxTopologiaDe(nomePool);
        if (!t) return [];
        return (t.vdevs || []).flatMap((v) => (v.dispositivos || []).map((d) => ({ ...d, vdev: v.nome, tipo: v.tipo })));
      },
      // 🔴 THE FIELD NEVER GOES BACK TO NULL, not even on the error path. Zeroing it
      // to `null` in a catch would recreate exactly the defect the stable shape just
      // solved — and the error path is the MOST likely one to happen with the
      // hypervisor down, which is when the screen most needs to open.
      async pvxCarrega(rota, campo, vazio) {
        try {
          const r = await this.api(rota, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx[campo] = await r.json();
          this.pvx.carregado[campo] = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx[campo] = vazio;
        }
      },
      pvxLoadSistema()  { return this.pvxCarrega('/api/proxmox/sistema', 'sistema', {}); },
      pvxLoadPacotes()  { return this.pvxCarrega('/api/proxmox/pacotes', 'pacotes', { pacotes: [] }); },
      pvxLoadRegistro() { return this.pvxCarrega('/api/proxmox/syslog?limit=300', 'registro', { linhas: [] }); },

      // ── reading the system ────────────────────────────────────────────────
      pvxInterfaces() { return (this.pvx.sistema && this.pvx.sistema.network) || []; },
      pvxDNS()        { return (this.pvx.sistema && this.pvx.sistema.dns) || null; },
      pvxHora()       { return (this.pvx.sistema && this.pvx.sistema.time) || null; },
      pvxCertificados(){ return (this.pvx.sistema && this.pvx.sistema.certificados) || []; },
      // 🔴 The difference between the hypervisor’s clock and the timezone IS the
      // information: one server in UTC and another in São Paulo produce backup
      // windows that never meet — that is how this lab’s off-site chain stayed dead
      // for 14 days, with nothing on screen able to say so.
      pvxFusoDoNo() {
        const t = this.pvxHora();
        return t ? (t.timezone || 'unknown') : '—';
      },
      // Certificate expiry: days remaining, computed between TWO server
      // timestamps (notafter and observed_at), never with the browser’s
      // clock.
      pvxDiasDoCert(c) {
        const d = this.pvx.sistema;
        if (!c || !c.notafter || !d) return null;
        return Math.floor((Number(c.notafter) - Number(d.observed_at)) / 86400);
      },
      pvxEstiloCert(c) {
        const dias = this.pvxDiasDoCert(c);
        if (dias === null) return 'background:#64748b22;color:#94a3b8;border:1px solid #64748b66';
        const cor = dias <= 7 ? '#ef4444' : dias <= 30 ? '#f59e0b' : '#22c55e';
        return `background:${cor}22;color:${cor};border:1px solid ${cor}66`;
      },
      pvxPacotesFiltrados() {
        const ps = (this.pvx.pacotes && this.pvx.pacotes.pacotes) || [];
        const t = (this.pvx.filtroPacote || '').trim().toLowerCase();
        if (!t) return ps;
        return ps.filter((p) => (p.Package || '').toLowerCase().includes(t)
                             || (p.Version || '').toLowerCase().includes(t));
      },
      pvxLinhasDoRegistro() { return (this.pvx.registro && this.pvx.registro.linhas) || []; },
      // Severity highlighting read from the TEXT of the line. The journal does not
      // return a structured level over this route, so the highlighting is heuristic —
      // and because of that it never HIDES a line, it only emphasises.
      pvxEstiloLinhaLog(t) {
        const s2 = String(t || '');
        if (/\b(error|erro|failed|failure|fatal|panic|refused|denied)\b/i.test(s2)) return 'color:#ef4444';
        if (/\b(warn|warning|aviso|degraded|timeout)\b/i.test(s2)) return 'color:#f59e0b';
        return '';
      },

      async pvxLoadTopologia() {
        try {
          const r = await this.api('/api/proxmox/zfs/topologia', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.topologia = await r.json();
          this.pvx.carregado.topologia = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.topologia = { pools: [] };
        }
      },

      // ── node type: container, VM, hypervisor or external ─────────────────────
      //
      // 🔴 The information was ALWAYS in the data — and never on the screen. The real
      // type lives in the id prefix (`lxc/203`, `qemu/208`, `node/pve`), so knowing
      // whether that thing was a container or a VM required the operator to decode a
      // string. Proxmox solves this with an icon; here the list is dense and
      // monospaced, so a short tag reads better than a drawing.
      //
      // COLOUR MEANS STATE, LETTERS MEAN TYPE. The state badge already uses
      // green/amber/red; if the type used the same palette, the two would compete for
      // the same visual channel and neither would be trustworthy. That is why the
      // type lives in a hue family outside the semantic one.
      TIPOS: {
        node:    { sigla: 'NODE',  rotulo: 'hypervisor',        cor: '#94a3b8' },
        lxc:     { sigla: 'CT',  rotulo: 'LXC container',     cor: '#38bdf8' },
        qemu:    { sigla: 'VM',  rotulo: 'virtual machine',   cor: '#a78bfa' },
        externo: { sigla: 'EXT', rotulo: 'outside the hypervisor', cor: '#2dd4bf' },
        // 🔴 Do NOT use #64748b here: it is the colour of the "parado" state. The
        // collision pin caught it — an unknown type would show up in the same colour as
        // a powered-off guest, which is exactly the confusion that separating the two
        // palettes exists to prevent.
        '?':     { sigla: '?',   rotulo: 'unknown type', cor: '#a1887f' },
      },
      pvxTipoChave(n) {
        if (!n) return '?';
        const pre = String(n.id || '').split('/')[0];
        if (pre === 'lxc' || pre === 'qemu' || pre === 'node') return pre;
        if (n.kind === 'host') return 'node';
        if (n.kind === 'externo') return 'externo';
        // 🔴 The fallback does NOT guess "VM". An id that matches nothing
        // known is a type this panel cannot classify, and saying so is the only
        // honest answer — inventing a type makes the operator act on the wrong
        // category.
        return '?';
      },
      pvxTipo(n) {
        const t = this.TIPOS[this.pvxTipoChave(n)] || this.TIPOS['?'];
        return { ...t, chave: this.pvxTipoChave(n), modelo: !!(n && n.template) };
      },
      pvxEstiloTipo(n) {
        const c = this.pvxTipo(n).cor;
        return `background:${c}1f;color:${c};border:1px solid ${c}55`;
      },
      // Badge title: the long text the short badge has no room for. A template is not
      // a startable guest, and the screen has to say so where the difference matters —
      // before somebody tries to start it.
      pvxTipoTitulo(n) {
        const t = this.pvxTipo(n);
        const base = t.rotulo + (n && n.vmid > 0 ? ` · vmid ${n.vmid}` : '');
        return t.modelo ? base + ' · TEMPLATE (not a bootable guest)' : base;
      },
      // Count per type for the list header. Answers "what do I have?" without forcing
      // anyone to count row by row.
      pvxContagemPorTipo() {
        const conta = {};
        for (const n of this.pvxNos()) {
          const k = this.pvxTipoChave(n);
          conta[k] = (conta[k] || 0) + 1;
        }
        return ['node', 'lxc', 'qemu', 'externo', '?']
          .filter((k) => conta[k])
          .map((k) => ({ chave: k, sigla: this.TIPOS[k].sigla, rotulo: this.TIPOS[k].rotulo, n: conta[k] }));
      },

      // ── contextual tabs of the right-hand panel ─────────────────────────
      //
      // What replaces the stacked blocks. There were six always-open sections, one
      // under the other: guests, tasks, disks, storage, ZFS, permissions. Now the
      // right-hand panel shows ONE of them, chosen by you, and the set of tabs
      // changes with the TYPE of the selected node — the way Proxmox itself does it.
      // Nothing appears unless you ask for it: that is what gets the error history
      // out of the way without needing a drawer.
      // The density follows the Proxmox menu, which groups System (network, DNS,
      // time, certificates) and keeps Disks apart from Storage. The operator CANNOT
      // reach that UI, so whatever is not here does not exist for him.
      ABAS_HOST: [
        { id: 'resumo',   rot: 'Summary' },
        { id: 'graficos', rot: 'Charts' },
        { id: 'console',  rot: 'Shell' },
        { id: 'tarefas',  rot: 'Tasks' },
        { id: 'discos',   rot: 'Disks' },
        { id: 'storage',  rot: 'Storage' },
        { id: 'zfs',      rot: 'ZFS' },
        { id: 'rede',     rot: 'Network' },
        { id: 'sistema',  rot: 'System' },
        { id: 'pacotes',  rot: 'Packages' },
        { id: 'registro', rot: 'Log' },
        { id: 'perms',    rot: 'Permissions' },
      ],
      ABAS_GUEST: [
        { id: 'resumo',   rot: 'Summary' },
        { id: 'graficos', rot: 'Charts' },
        { id: 'console',  rot: 'Console' },
// 🔴 "Copies", not "Snapshots": the three ways of preserving this guest —
        // snapshot, clone and stored copy — live together because they answer the SAME
        // operator question ("how do I not lose this?"). Spreading them across
        // different tabs would force him to remember which of the three was where. And
        // one more tab on a screen that already has eleven is cost, not organisation.
        { id: 'snaps',    rot: 'Copies' },
        { id: 'tarefas',  rot: 'Tasks' },
      ],
      pvxEhGuest(n) { return !!(n && n.kind === 'guest' && n.vmid > 0); },
      pvxAbasDoNo(n) {
        if (!n) return [];
        return this.pvxEhGuest(n) ? this.ABAS_GUEST : this.ABAS_HOST;
      },
      // A tab inherited from a node of ANOTHER type does not exist in the current
      // set. Without this normalisation, going from "Disks" (host) to a guest would
      // leave the right-hand panel blank, with no error at all — the same family of
      // defect that once made the whole tab open black.
      pvxAbaAtiva() {
        const n = this.pvxNoAberto();
        if (!n) return '';
        const abas = this.pvxAbasDoNo(n);
        return abas.some((a) => a.id === this.pvx.aba) ? this.pvx.aba : 'resumo';
      },
      pvxVaiPara(aba) {
        this.pvx.aba = aba;
        if (aba === 'console') {
          const n = this.pvxNoAberto();
          if (n) this.pvxAbreConsole(n.id);
        } else {
          this.pvxFechaConsole();
        }
        // Loads on demand, the first time the tab is opened. That is what makes the
        // screen cheap: before, EVERY visit fetched tasks, disks, storage, ZFS and
        // permissions, even if all you wanted was to look at one guest.
        if (aba === 'tarefas' && !this.pvx.tasks.length) this.pvxLoadTasks();
        if (aba === 'discos' && !this.pvx.disks.length) this.pvxLoadDisks();
        if (aba === 'storage' && !this.pvx.storage) this.pvxLoadStorage();
        if (aba === 'zfs' && !this.pvx.zfs) this.pvxLoadZfs();
        // The topology serves ALL THREE storage tabs: it is what ties physical disk,
        // pool and datastore into a single story.
        if ((aba === 'zfs' || aba === 'discos' || aba === 'storage') && !this.pvx.carregado.topologia) this.pvxLoadTopologia();
        if (aba === 'perms' && !this.pvx.perms && !this.pvx.permsAberto) this.pvxLoadPerms();
        if (aba === 'graficos') this.pvxLoadSerie();
        if ((aba === 'rede' || aba === 'sistema') && !this.pvx.carregado.sistema) this.pvxLoadSistema();
        // The HOST Summary shows the timezone, and the timezone comes from /sistema.
        // Without this it would be born an em-dash and would only appear after the
        // operator visited another tab — a datum that exists, hidden by navigation order.
        if (aba === 'resumo' && !this.pvx.carregado.sistema && !this.pvxEhGuest(this.pvxNoAberto())) this.pvxLoadSistema();
        // The note is the BODY of the summary, so it loads together with the tab — not
        // after a second click.
        if (aba === 'resumo') this.pvxLoadNota(this.pvx.aberto);
        // The Copies tab NEEDS the storage list to know where the copy can go. Without
        // this it only had the list if the operator had visited the Storage tab first —
        // and then the button said "no storage accepts backups", which is a lie about
        // the hypervisor.
        if (aba === 'snaps' && !this.pvx.storage) this.pvxLoadStorage();
        if (aba === 'pacotes' && !this.pvx.carregado.pacotes) this.pvxLoadPacotes();
        if (aba === 'registro' && !this.pvx.carregado.registro) this.pvxLoadRegistro();
      },
      pvxSeleciona(n) {
        if (!n) return;
        if (this.pvx.aberto === n.id) { this.pvxLimpaSelecao(); return; }
        this.pvx.aba = 'resumo';
        this.pvxAbre(n);
      },
      pvxLimpaSelecao() { this.pvx.aba = ''; this.pvxFecha(); },
      pvxTemSelecao() { return !!this.pvx.aberto; },

      // ── lab summary: the right-hand panel when NOTHING is selected ───────
      //
      // Half the screen would sit empty for free. Here it answers "do I need to act
      // now?" — which is the role the fixed band would have played, and which the
      // operator rejected along with layout A5.
      //
      // 🔴 Sums only what was ACTUALLY observed. A guest whose datum has no timestamp
      // goes into `semDado`, never in as zero: adding absence up as zero is the
      // classic way for a panel to lie that everything is roomy.
      pvxResumoDoLab() {
        const guests = this.pvxNos().filter((n) => this.pvxEhGuest(n));
        let memU = 0, memT = 0, cpuSoma = 0, cpuN = 0, semDado = 0, ligados = 0, desconhecidos = 0;
        for (const g of guests) {
          const st = (g.status && g.status.value) || '';
          // 🔴 "POWERED ON" IS A CLAIM ABOUT RIGHT NOW, AND DEMANDS OBSERVING RIGHT NOW.
          //
          // This line used to count raw `status.value` — the last known value — as if it
          // were fact about the instant. With the hypervisor unreachable, the panel said
          // "9 / 10 powered on" about a house it had not managed to see for almost an
          // hour. The operator noticed.
          //
          // And the inconsistency was INSIDE this very function: two lines below, memory
          // and CPU already checked `observed_at` and fell into "no data". Only the
          // on/off count did not check.
          //
          // 🔴 THE FIX IS NOT TO COUNT ZERO. "All powered off" would be another lie, and
          // a more dangerous one: it would assert a state nobody observed. The right
          // answer is the third one — I DO NOT KNOW —, the only true one when you cannot
          // look.
          const observado = !g.stale && !g.ausente_desde && !!(g.status && g.status.observed_at);
          if (!observado) desconhecidos++;
          else if (st === 'running' || st === 'online') ligados++;
          const mu = g.mem_used, mt = g.mem_total, cf = g.cpu_frac;
          if (!mu || !mu.observed_at || !mt || !(Number(mt.value) > 0)) semDado++;
          else { memU += Number(mu.value); memT += Number(mt.value); }
          if (cf && cf.observed_at && Number(cf.value) >= 0) { cpuSoma += Number(cf.value); cpuN++; }
        }
        const pools = this.pvxZfsPools();
        const poolRuim = pools.filter((p) => p && !p.saudavel).length;
        return {
          guests: guests.length, ligados, semDado, desconhecidos,
          memUsado: memU, memTotal: memT,
          memPct: memT > 0 ? (memU / memT) * 100 : null,
          cpuPct: cpuN ? (cpuSoma / cpuN) * 100 : null,
          poolTotal: pools.length, poolRuim,
          poolEstado: !pools.length ? 'sem-medida' : (poolRuim ? 'degradado' : 'ok'),
        };
      },
      // Backup freshness — MEASURED as unavailable over the current route, not
      // assumed. `pvesh` as root lists the datastore contents; the panel’s token has
      // PVEAuditor on / with propagate=1, PASSES the permission check and still gets
      // an empty list for a datastore with 46.9 GB used. There is no PBS credential
      // in the vault.
      //
      // Until a source exists (a PBS token of its own, or the lab agent) this tile
      // SAYS so. Never an invented number, and never disappearing in silence —
      // disappearing in silence is what let the off-site die for 14 days.
      // 🔴 THIS TILE SPENT HALF A DAY SAYING "NO SOURCE", and saying that was the
      // right thing: the token did not enumerate the datastore. Once the access was
      // widened it gained a real source.
      //
      // Each datastore appears SEPARATELY, and that already paid for itself on the
      // first measurement: `pbs` was at 3.6 h with 9 guests while `backupusb` was at
      // 340 h (14 days) with 3 guests. A single number would have let the fresh layer
      // mask the stoppage — exactly the case this screen exists to reveal.
      // "Has it loaded?" and "does it have data?" are DIFFERENT questions, and mixing
      // them is what produced the crash: the field was null until it loaded, and the
      // template dereferenced it before then. Now the shape is stable and the question
      // has a flag of its own.
      pvxBackups() {
        return { carregado: !!this.pvx.carregado.backup, itens: this.pvx.backup.datastores || [] };
      },
      // Computing the age ON THE SERVER would be the right thing, but the timestamp
      // here is an absolute unix epoch from the hypervisor, and the browser only
      // compares it with the `observed_at` of the SAME response — never with the local
      // clock. So the age stays a difference between two server clocks.
      pvxIdadeDoBackup(ds) {
        const d = this.pvx.backup;
        if (!ds || !d) return { conhecido: false };
        if (ds.erro) return { conhecido: false, erro: ds.erro };
        if (!ds.total) return { conhecido: true, vazio: true };
        const seg = Math.max(0, Number(d.observed_at) - Number(ds.ultimo_ctime));
        return { conhecido: true, vazio: false, seg, texto: this.pvxFormatAge(seg) };
      },
      // The threshold is per datastore because the layers have different cadences: PBS
      // runs every day, the external-HD rotation is "whenever I remember to swap the
      // disk". A single threshold would paint a healthy rotation red, or a PBS that
      // has been dead three days green.
      // 🔴 DISARMED IS NOT A FAILURE, and that distinction is the difference between a
      // trustworthy panel and a permanent red that trains you to ignore it.
      //
      // This lab’s `backupusb` has had no fresh copy for two weeks because the
      // operator turned the schedule off in a deliberate, recorded decision, after PBS
      // was up, verify-by-content was running and a restore had been rehearsed.
      // Painting that red is being wrong about the FACT, not about the colour.
      //
      // "fora-do-pve" is not a failure either: it means this panel does not know who
      // schedules it. The `pbs` here gets a copy every day from a systemd timer on the
      // host, invisible to /cluster/backup — and the live proof caught that before
      // deploy, when a boolean would have painted the live layer as disarmed.
      pvxEstadoBackup(ds) {
        const i = this.pvxIdadeDoBackup(ds);
        if (ds.erro) return { chave: 'erro', rotulo: 'error', cor: '#ef4444', nota: ds.erro };
        if (ds.agendamento === 'desarmado') {
          return { chave: 'desarmado', rotulo: 'disarmed', cor: '#94a3b8',
                   nota: 'schedule turned off' + (ds.schedule ? ' (era ' + ds.schedule + ')' : '') + ' — this is not a failure' };
        }
        if (i.vazio) return { chave: 'vazio', rotulo: 'no copy yet', cor: '#ef4444', nota: '' };
        const limiteH = ds.storage === 'pbs' ? 36 : 24 * 10;
        const h = i.seg / 3600;
        const cor = h <= limiteH ? '#22c55e' : h <= limiteH * 2 ? '#f59e0b' : '#ef4444';
        return {
          chave: 'active', rotulo: i.texto, cor,
          nota: ds.agendamento === 'fora-do-pve'
            ? 'scheduled outside PVE — the panel does not know by whom'
            : (ds.schedule ? 'daily at ' + ds.schedule : ''),
        };
      },
      pvxEstiloBackup(ds) {
        const c = this.pvxEstadoBackup(ds).cor;
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      async pvxLoadBackup() {
        try {
          const r = await this.api('/api/proxmox/backup', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          this.pvx.backup = await r.json();
          this.pvx.carregado.backup = true;
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          this.pvx.backup = { datastores: [] };
        }
      },
      pvxEstiloResumo(e) {
        const c = e === 'ok' ? '#22c55e' : e === 'degradado' ? '#ef4444' : '#64748b';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      // ── live refresh with automatic pause ───────────────────────────────
      //
      // The 4 "Refresh" buttons are gone: a panel that needs a click to be
      // correct is not a panel. But the operator uses this from his PHONE during
      // an incident, and a list that reorders under his finger makes him tap the
      // wrong guest. Hence the AUTOMATIC pause while the console is open or the
      // filter has focus.
      //
      // The age stamp KEEPS running during the pause. Stale data has to LOOK stale —
      // freezing the stamp along with it would turn the pause into a lie about
      // freshness.
      pvxMotivoDaPausa() {
        if (this.pvx.con.guest) return 'console open';
        if (this.pvx.filtroFoco) return 'typing in the filter';
        return '';
      },
      pvxTick() {
        if (typeof document !== 'undefined' && document.hidden) return;
        const motivo = this.pvxMotivoDaPausa();
        this.pvx.pausado = !!motivo;
        this.pvx.pausaMotivo = motivo;
        this.pvxAtualizaIdade();
        if (motivo) return;
        if (typeof this.loadNodes === 'function') this.loadNodes();
        this.pvxLoadSaude();
        const aba = this.pvxAbaAtiva();
        if (aba === 'tarefas') this.pvxLoadTasks();
        if (aba === 'discos') this.pvxLoadDisks();
        if (aba === 'storage') this.pvxLoadStorage();
        if (aba === 'zfs') this.pvxLoadZfs();
      },
      pvxAtualizaIdade() {
        const p = this.nodes && this.nodes.poll;
        this.pvx.idadeSeg = p && typeof p.age_seconds === 'number' ? p.age_seconds : null;
      },
      // The browser NEVER subtracts clocks here: the age comes from the server and the
      // browser only formats it. An inherited invariant.
      pvxIdadeTexto() {
        const s = this.pvx.idadeSeg;
        if (s == null) return 'no timestamp';
        if (s < 0) return 'never observed';
        if (s < 60) return `data from ${s}s ago`;
        const m = Math.floor(s / 60);
        if (m < 60) return `data from ${m} min ago`;
        const h = Math.floor(m / 60);
        return h < 48 ? `data from ${h}h ago` : `data from ${Math.floor(h / 24)} days ago`;
      },
      pvxIdadeVelha() { return this.pvx.idadeSeg != null && this.pvx.idadeSeg > 120; },
      pvxParaTimer() { if (this._pvxTimer) { clearInterval(this._pvxTimer); this._pvxTimer = null; } },
      pvxIniciaTimer() { this.pvxParaTimer(); this._pvxTimer = setInterval(() => this.pvxTick(), 15000); },

      pvxInit() {
        // 🔴 The screen stopped fetching EVERYTHING on entry.
        // Before: health + tasks + disks + storage + ZFS, five requests, every
        // visit, even if all you wanted was to look at one guest — and the five
        // responses were dumped stacked on the same page. Now each tab loads on
        // demand (pvxVaiPara).
        //
        // ZFS is the exception and sits here on purpose: the lab summary, which is what
        // shows with nothing selected, displays the pool health. Without it the state
        // would be born "not measured" on every entry.
        this.pvxLoadSaude();
        this.pvxLoadZfs();
        this.pvxLoadBackup();
        this.pvxIniciaTimer();
        // 🔴 THE GUEST LIST HAS TO COME ALONG. pvxGuests() reads `nodes.list`, which is
        // loaded by the Nodes tab — and anyone landing straight on Proxmox via link or
        // reload found the snapshot selector EMPTY, with no error at all. The console
        // made that worse: each guest’s credential state is what decides whether it CAN
        // have a console, and without the list every guest would show as unavailable.
        // It is a store read, and it is cheap.
        if (typeof this.loadNodes === 'function') {
// The age has to be stamped as soon as the list arrives. Before, it was only
          // read on the first tick, 15s later — and in that gap the screen said
          // "no timestamp" next to a badge already showing "11s ago".
          Promise.resolve(this.loadNodes()).finally(() => {
            this.pvx.primeiraCarga = false;
            this.pvxAtualizaIdade();
          });
        } else { this.pvx.primeiraCarga = false; }
      },

      // ---- health ----------------------------------------------------------

      // Health comes out of the server’s STORE (the poller collects it every tick).
      // This route does NOT touch the hypervisor: if it did, the age would always be
      // "0 s" and the screen would hide exactly the mute hypervisor.
      async pvxLoadSaude() {
        this.pvx.loading = true;
        try {
          const r = await this.api('/api/proxmox', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.saude = d.hypervisor || null;
          this.pvx.ttl = d.ttl_seconds || 90;
          this.pvx.forbidden = false;
          this.pvx.lastError = '';
        } catch (e) {
          this.pvx.lastError = this._errText(e);
          console.warn('[proxmox] saude', e);
        } finally { this.pvx.loading = false; }
      },

// pvxFormatAge receives age_seconds ALREADY COMPUTED BY THE SERVER.
      // -1 is the marker for "never observed" and must NOT become "now": "0 s ago"
      // reads as just-seen, which is stale data presented as live.
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

      // pvxBytes formats bytes; no clock, no state — presentation only.
      pvxBytes(n) {
        if (n === null || n === undefined) return '—';
        const u = ['B', 'KB', 'MB', 'GB', 'TB'];
        let v = Number(n), i = 0;
        while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
        return `${v.toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
      },
      pvxPct(usado, total) {
        if (!total) return 0;
        return Math.round((Number(usado) / Number(total)) * 100);
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

      // ---- tasks -----------------------------------------------------------

      // The limit is 50 and it is fixed: the server owns the ceiling (it truncates at
      // pve.MaxTasks anyway). A "how many rows" selector here would be a path from the
      // browser to the size of the hypervisor’s response.
      async pvxLoadTasks() {
        this.pvx.loading = true;
        try {
          const q = this.pvx.soErros ? '?errors=1&limit=50' : '?limit=50';
          const r = await this.api('/api/proxmox/tasks' + q, { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.tasks = d.tasks || [];
          this.pvx.lastError = '';
        } catch (e) {
          // 🔴 THE PREVIOUS DATA STAYS. Clearing the list here turned a 300 ms network
          // failure into "no tasks in this window" — which is an assertion about the
          // hypervisor, made precisely when we failed to talk to it. The screen shows the
          // error on top of the stale data; what goes away is the certainty, not the
          // information.
          this.pvx.lastError = this._errText(e);
        } finally { this.pvx.loading = false; }
      },

      pvxToggleErros() {
        this.pvx.soErros = !this.pvx.soErros;
        return this.pvxLoadTasks();
      },

      // pvxTaskOK: in PVE, "OK" is the only exitstatus that means success. Everything
      // else is the real reason for the failure, and that is what the table shows.
      pvxTaskOK(t) { return t && t.status === 'OK'; },
      pvxTaskStyle(t) {
        const c = this.pvxTaskOK(t) ? '#22c55e' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxTaskAlvo(t) { return t && t.id ? `${t.type} ${t.id}` : (t ? t.type : ''); },

      async pvxAbreLog(upid) {
        this.pvx.logAberto = true;
        this.pvx.taskLog = { upid, lines: null };
        try {
          const r = await this.api('/api/proxmox/tasks/log?upid=' + encodeURIComponent(upid));
          const d = await r.json();
          this.pvx.taskLog = { upid, lines: d.lines || [] };
        } catch (e) {
          this.pvx.taskLog = { upid, lines: ['(failed to read the log: ' + this._errText(e) + ')'] };
        }
      },
      pvxFechaLog() { this.pvx.logAberto = false; this.pvx.taskLog = null; },

      // ---- disks -----------------------------------------------------------

      async pvxLoadDisks() {
        try {
          const r = await this.api('/api/proxmox/disks', { raw: true });
          if (r.status === 403) { this.pvx.forbidden = true; return; }
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.disks = d.disks || [];
        } catch (e) {
          // Same rule as the tasks: a failure does not erase what was already known.
          this.pvx.lastError = this._errText(e);
        }
      },

      // wearout_pct arrives null when the disk does not report lifetime. Null does NOT
      // become 0: a silent disk would show up as a worn-out disk.
      pvxWearout(d) {
        if (!d || d.wearout_pct === null || d.wearout_pct === undefined) return 'not reported';
        return `${d.wearout_pct}%`;
      },
      pvxHealthStyle(d) {
        const ok = d && d.health === 'PASSED';
        const c = ok ? '#22c55e' : (d && d.health ? '#f59e0b' : '#64748b');
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      // ---- storage and zpool capacity --------------------------------------
      //
      // 🔴 Both routes come out of the server’s STORE, like the health — capacity is a
      // heartbeat, not navigation. Each brings its OWN age_seconds, and the screen’s
      // three ages (health, storage, zpool) may diverge on purpose: they come from
      // three calls that fail separately. A divergent age is not a bug, it is the
      // symptom showing up.

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

      // 🔴 pvxStorageEstado is the guard arriving on screen. `pools: []` has THREE
      // readings, and the screen has to say which one:
      //
      //   'sem-medida'  — nobody has asked yet (timestamp 0). Do not accuse anything.
      //   'sem-permissao' — asked, and the token cannot see datastores. The empty
      //                     list is the ACL filtering, not the absence of storage.
      //   'ok'          — asked, allowed to see. Empty here is genuinely empty.
      //
      // Before the ACL was widened the state was 'sem-permissao'; today it is 'ok',
      // and the band disappears on its own. It comes back the day the privilege goes.
      pvxStorageEstado() {
        const d = this.pvx.storage && this.pvx.storage.datastore_audit;
        if (!d || !d.observed_at) return 'sem-medida';
        return d.value ? 'ok' : 'sem-permissao';
      },

      pvxStoragePools() { return (this.pvx.storage && this.pvx.storage.pools) || []; },
      pvxZfsPools() { return (this.pvx.zfs && this.pvx.zfs.pools) || []; },

      // pvxUsoStyle colours the bar by usage band. The cut at 85% is not cosmetic: the
      // pool in this lab is SINGLE-DISK, with no redundancy, and filling it up is one
      // of the few ways to lose data without any hardware failing.
      pvxUsoStyle(pct) {
        const v = Math.max(0, Math.min(100, Number(pct) || 0));
        const c = v >= 85 ? '#ef4444' : (v >= 70 ? '#f59e0b' : '#22c55e');
        return `width:${v}%;background:${c}`;
      },
      pvxUsoTexto(p) {
        if (!p) return '—';
        return `${(Number(p.used_pct) || 0).toFixed(1)}% · ${this.pvxBytes(p.used)} / ${this.pvxBytes(p.total)}`;
      },

      // 🔴 pvxZfsStyle: only ONLINE is green. DEGRADED, FAULTED, SUSPENDED and UNAVAIL
      // are all red — the whole server lives in a pool with no redundancy, and
      // "amber" would invite leaving it for later.
      pvxZfsStyle(p) {
        const c = (p && p.saudavel) ? '#22c55e' : '#ef4444';
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },
      pvxZfsFrag(p) {
        if (!p || p.frag_pct === null || p.frag_pct === undefined) return '—';
        return `frag ${p.frag_pct}%`;
      },

      // ---- permissions -----------------------------------------------------

      async pvxLoadPerms() {
        this.pvx.permsAberto = !this.pvx.permsAberto;
        if (!this.pvx.permsAberto || this.pvx.perms) return;
        try {
          const r = await this.api('/api/proxmox/permissions');
          this.pvx.perms = await r.json();
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        }
      },
      pvxPermLinhas() {
        const p = this.pvx.perms && this.pvx.perms.permissions ? this.pvx.perms.permissions : {};
        return Object.keys(p).sort().map(k => ({ caminho: k, privs: Object.keys(p[k]).sort().join(', ') }));
      },

      // ---- snapshots -------------------------------------------------------

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

      // 🔴 The two mutations below wait for the WHOLE response. The server only
      // answers after the WaitTask (status stopped + exitstatus OK), so the spinner
      // covers the real operation, not the "request accepted".
      pvxCriaSnap(nodeId) {
        const nome = (this.pvx.novoSnap || '').trim();
        this.askConfirm('Create snapshot',
          `Creates the snapshot "${nome}" on "${nodeId}". The response only comes back once the hypervisor has finished the task.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(nome), { method: 'POST' });
              const d = await r.json().catch(() => ({}));
              this.showToast('snapshot created (task ' + (d.upid || '—') + ')', 'ok');
              this.pvx.novoSnap = '';
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

      pvxApagaSnap(nodeId, nome) {
        this.askConfirm('Delete snapshot',
          `Deletes "${nome}" from "${nodeId}". Irreversible: the state kept in that snapshot ceases to exist.`,
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(nome), { method: 'DELETE' });
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

      // ---- snapshot rollback -----------------------------------------------
      //
      // 🔴 The privilege ALREADY EXISTED and the panel did not surface it.
      // `VM.Snapshot` is enough for rollback (LXC/Snapshot.pm:274-276), and every
      // node token already carries it. Destructive power that is hidden stays
      // reachable by whoever holds the token — it just stops being auditable.
      //
      // The confirmation is BY TYPING (requireText = the guest’s name) and not by
      // clicking: what gets lost has no second copy, because this lab’s pool is
      // SINGLE-DISK, with no mirror.
      pvxRollbackSnap(nodeId, nome) {
        const g = this.pvxGuests().find(x => x.id === nodeId);
        const rotulo = (g && g.name) || nodeId;
        this.askConfirm('Roll back to snapshot "' + nome + '"',
          'Guest "' + rotulo + '" (' + nodeId + ') goes back to the state of snapshot "' + nome + '". ' +
          'EVERYTHING written to it after that snapshot ceases to exist — files, database, logs, ' +
          'and any work in progress. There is no second copy: the pool on this server is a ' +
          'single disk, with no mirror. Type the guest name (' + rotulo + ') to confirm.',
          async () => {
            if (this.pvx.busy) return;
            this.pvx.busy = nodeId;
            this.pvx.lastError = '';
            try {
              const r = await this.api('/api/proxmox/snapshots/rollback?node=' + encodeURIComponent(nodeId)
                + '&name=' + encodeURIComponent(nome), { method: 'POST' });
              const d = await r.json().catch(() => ({}));
              this.showToast('guest rolled back to snapshot "' + nome + '" (task ' + (d.upid || '—') + ')', 'ok');
            } catch (e) {
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('rollback failed: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              await this.pvxLoadSnaps(nodeId);
            }
          }, { danger: true, requireText: rotulo });
      },

      // 🔴 SUSPEND HAS NO BUTTON, AND THAT IS DELIBERATE. Measured on this host:
      // `vzsuspend 204` ended in `lxc-checkpoint … criu … failed: exit code 1` and the
      // CT stayed `running`. A button that always errors trains the operator to ignore
      // errors — and the next error, the real one, goes unnoticed. It comes back when
      // CRIU works here, measured.

      // ---- remote console --------------------------------------------------
      //
      // 🔴 THE TICKET AND THE PORT NEVER REACH HERE. The naive path would be for the
      // panel to return {ticket, port} and for this code to open the WebSocket
      // straight at the hypervisor. It cannot and it must not: `hypervisor.local` does
      // not resolve from outside the house, the certificate is from the cluster’s own
      // CA, and the ticket is a console credential — whoever holds it opens a shell.
      // It stays locked inside internal/pve; all that leaves here is `?node=<id>`.
      //
      // 🔴 AND THE TOKEN DOES NOT GO IN THE URL. Same reason written on the terminal’s
      // WS (00-shell.js): the query lands in the proxy’s access log = a replayable
      // shell credential. The session travels in the HttpOnly cookie, on a same-origin
      // upgrade. The server REFUSES `?token=` with a 400 — it does not merely ignore it.

      // pvxConsoleEstadoDoGuest says whether the guest CAN have a console, and why not.
      // The `pbs` CT (202) has no node token: it gets no console, and the screen has
      // to SAY that instead of offering a button that fails.
      // 🔴 THE HOST HAS A SHELL, and it is NOT the same thing as a guest console: it is
      // root ON THE HYPERVISOR, from where the nine guests can be shut down with one
      // command. Its credential is a different one too — the hypervisor’s read token,
      // because no node token has Sys.Console.
      pvxEhHost(n) { return !!(n && n.kind === 'host'); },
      pvxConsoleEstadoDoGuest(g) {
        // No `this.`: this function is PURE on purpose. The pin harness extracts it and
        // runs it isolated from the object, and a dependency on `this` here would break
        // it — which is what happened in the first version of this change. A predicate
        // about a node does not need the component.
        if (g && g.kind === 'host') {
          return { pode: true, motivo: '', host: true };
        }
        const st = (g && g.credential && g.credential.state) || '';
        if (st === 'ok') return { pode: true, motivo: '' };
        if (st === 'ausente') return { pode: false, motivo: 'no node token in the vault — this guest has no console' };
        if (st === 'revogada') return { pode: false, motivo: 'credential revoked — console unavailable' };
        if (st === 'expirada') return { pode: false, motivo: 'credential expired — console unavailable' };
        return { pode: false, motivo: 'credential state unknown' };
      },
      pvxConsolePode(g) { return this.pvxConsoleEstadoDoGuest(g).pode; },
      pvxConsoleMotivo(g) { return this.pvxConsoleEstadoDoGuest(g).motivo; },

      pvxAbreConsole(nodeId) {
        // 🔴 THE REFUSAL LIVES HERE, not in the widget. It used to be a `:disabled` on
        // an <option>: calling the function by any other path (the side panel, the
        // palette, the browser console) was enough to open the WebSocket against a guest
        // with no token, and the error would only show up coming back from the server.
        // A screen guard is a convenience; a function guard is the rule.
        const g = this.pvxNos().find(x => x.id === nodeId);
        if (g && !this.pvxConsolePode(g)) {
          this.pvx.con = { guest: '', estado: 'erro', erro: this.pvxConsoleMotivo(g) };
          this.showToast(this.pvxConsoleMotivo(g), 'err');
          return;
        }
        if (this.pvx.con.guest === nodeId && this.pvx.con.estado === 'ligado') return;
        this.pvxFechaConsole();
        this.pvx.con = { guest: nodeId, estado: 'abrindo', erro: '' };

        // The terminal is only mounted after Alpine’s next tick: before that the
        // container is still at x-show=false and the FitAddon would measure zero.
        this.$nextTick(() => {
          const el = document.getElementById('pvx-console');
          if (!el || typeof Terminal === 'undefined') {
            this.pvx.con.estado = 'erro';
            this.pvx.con.erro = 'xterm.js did not load';
            return;
          }
          el.innerHTML = '';
          const term = new Terminal({
            fontSize: 13,
            fontFamily: '"JetBrains Mono", ui-monospace, Menlo, Consolas, monospace',
            cursorBlink: true,
            scrollback: 5000,
            // convertEol STAYS FALSE: the guest’s getty already sends \r\n. Converting
            // here would inject an extra \r and the screen would draw a staircase.
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

          // 🔴 THE INPUT GOES AS TEXT, and the byte count is the SERVER’s. Measured against
          // CT 204: termproxy reads N BYTES after "0:N:", and `data.length` here counts
          // UTF-16 units — "é" would become half a character. Assembling the frame in the
          // browser would be the bug.
          term.onData(d => { if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'input', data: d })); });
          term.onResize(({ cols, rows }) => {
            if (ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols, rows }));
          });

          ws.onopen = () => {
            this.pvx.con.estado = 'ligado';
            try { fit.fit(); } catch (_) {}
            ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
            term.focus();
          };
          ws.onmessage = (ev) => {
            // BINARY is raw terminal output; TEXT is a control frame. Writing the Uint8Array
            // straight through preserves ANSI and UTF-8 split across two frames — going via
            // a string would break both.
            if (typeof ev.data === 'string') {
              let m = null;
              try { m = JSON.parse(ev.data); } catch (_) { return; }
              if (m && m.type === 'error') {
                this.pvx.con.estado = 'erro';
                this.pvx.con.erro = m.message || 'the hypervisor refused the console';
                term.write('\r\n\x1b[31m' + this.pvx.con.erro + '\x1b[0m\r\n');
              }
              return;
            }
            term.write(new Uint8Array(ev.data));
          };
          ws.onclose = () => {
            if (this.pvx.con.estado !== 'erro') this.pvx.con.estado = 'closed';
            term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n');
          };
          ws.onerror = () => {
            this.pvx.con.estado = 'erro';
            this.pvx.con.erro = this.pvx.con.erro || 'the connection to the panel dropped';
          };

          // Application-level keepalive: termproxy closes an idle session, and the server
          // sends its own as well. Two heartbeats cost nothing and an idle console does
          // not die "by itself".
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

      pvxFechaConsole() {
        if (this._pvxConPing) { clearInterval(this._pvxConPing); this._pvxConPing = null; }
        if (this._pvxConObs) { try { this._pvxConObs.disconnect(); } catch (_) {} this._pvxConObs = null; }
        if (this._pvxConWs) { try { this._pvxConWs.close(); } catch (_) {} this._pvxConWs = null; }
        if (this._pvxConTerm) { try { this._pvxConTerm.dispose(); } catch (_) {} this._pvxConTerm = null; }
        this.pvx.con = { guest: '', estado: 'closed', erro: '' };
      },

      pvxConsoleRotulo() {
        const e = this.pvx.con.estado;
        if (e === 'abrindo') return 'opening…';
        if (e === 'ligado') return 'live';
        if (e === 'erro') return 'error';
        return 'closed';
      },
      pvxConsoleStyle() {
        const e = this.pvx.con.estado;
        const c = e === 'ligado' ? '#22c55e' : (e === 'erro' ? '#ef4444' : '#f59e0b');
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      // ══════════════════════════════════════════════════════════════════
      // THE MERGED SCREEN — the node as the axis
      // ══════════════════════════════════════════════════════════════════
      //
      // Everything from here to the poll is a PURE FUNCTION on purpose: `pvxNoEstado`,
      // `pvxTier`, `pvxFiltraNos`, `pvxReescopa`, `pvxSegmentos`, `pvxDegrau` and
      // `pvxAcaoEstado` do not read `this`, do not touch the network and hold no state.
      // That is what lets scripts/test-proxmox-tab.mjs EXTRACT each one from the served
      // file and actually run it — a test that reimplemented the rule would only prove
      // the reimplementation.

      // ---- the vocabulary: SIX states, one single name for each ----------
      //
      // 🔴 `ok` gets no indicator at all on screen. With eleven nodes, nine greens
      // become silence and two reds become the whole screen; a green badge on every
      // row spends the operator’s attention exactly where there is no news. Whoever is
      // fine does not need to announce it.
      //
      // The ORDER below is precedence, and each rung short-circuits the next:
      //
      //   vencido        the data is past its TTL — I can no longer assert anything
      //   sem-credencial I can see, but I cannot act; and that holds stopped or not
      //   stopped        powered off is a STATE, not a defect; and a gauge on a
      //                  powered-off guest means nothing at all
      //   critico        some gauge at the top of the scale
      //   atencao        some gauge climbing, or a credential close to expiring
      //   ok             nothing to say
      pvxNoEstado(n) {
        if (!n) return 'ok';
        // 🔴 "SUMIU" COMES BEFORE "VENCIDO", and the two are not the same thing.
        //
        // `vencido` means the panel COULD NOT LOOK — the data aged
        // out. `sumiu` means the panel DID look and the hypervisor no longer
        // listed this node. They are different silences and they ask for
        // different things: one is an observation problem, the other is news
        // about the laboratory.
        //
        // Mixing them is what the operator saw: a destroyed clone stayed
        // counted forever as "vencido", spending the health band — which exists
        // to say whether there is something to do NOW.
        if (n.ausente_desde) return 'sumiu';
        if (n.stale) return 'vencido';
        const cred = (n.credential && n.credential.state) || '';
        if (n.transport === 'pve-api' && cred !== 'ok') return 'sem-credencial';
        const st = (n.status && n.status.value) || '';
        if (st !== 'running' && st !== 'online') return 'parado';
        let pior = 'ok';
        for (const qual of ['cpu', 'ram', 'disco']) {
          const pct = pvxPctDoMedidor(n, qual);
          if (pct === null) continue;
          if (pct >= 90) return 'critico';
          if (pct >= 70) pior = 'atencao';
        }
        return pior;
      },

      pvxRotuloEstado(e) {
        return ({
          'sumiu': 'gone from the hypervisor',
          'vencido': 'expired', 'sem-credencial': 'no credential', 'parado': 'stopped',
          'critico': 'critical', 'atencao': 'warning', 'ok': 'ok',
        })[e] || e;
      },
      pvxCorEstado(e) {
        return ({
          // Grey, like `parado`: vanishing from the hypervisor is NOT an incident.
          // Painting it red or amber is what spends the operator’s attention on something
          // he, most of the time, caused himself (he deleted a guest).
          'sumiu': '#64748b',
          'vencido': '#f59e0b', 'sem-credencial': '#ef4444', 'parado': '#64748b',
          'critico': '#ef4444', 'atencao': '#f59e0b', 'ok': '#22c55e',
        })[e] || '#64748b';
      },
      pvxEstiloEstado(e) {
        const c = this.pvxCorEstado(e);
        return `background:${c}22;color:${c};border:1px solid ${c}66`;
      },

      // ---- thresholds, with hysteresis ----------------------------------
      //
      // 🔴 70% and 90%, and the numbers have provenance: they are the
      // `warning`/`critical` from Unraid’s default.cfg. Beszel uses 65/90; the
      // difference between 65 and 70 is taste, the existence of TWO rungs is not. They
      // are written here, in one place only, so that changing the criterion is an edit
      // and not an archaeological dig through seventeen scattered `>= 85`s.
      //
      // The 3-point SLACK is what stops the colour flickering. A guest oscillating
      // between 89.7% and 90.2% would change colour on every 30 s tick; the flicker
      // informs nothing and teaches the operator to ignore the colour. Once in
      // `critico`, you only leave below 87%.
      pvxTier(pct, anterior) {
        const AVISO = 70, CRITICO = 90, FOLGA = 3;
        const v = Number(pct);
        if (!isFinite(v) || v < 0) return 'ok';
        if (anterior === 'critico') {
          if (v >= CRITICO - FOLGA) return 'critico';
          return v >= AVISO ? 'atencao' : 'ok';
        }
        if (anterior === 'atencao') {
          if (v >= CRITICO) return 'critico';
          return v >= AVISO - FOLGA ? 'atencao' : 'ok';
        }
        if (v >= CRITICO) return 'critico';
        if (v >= AVISO) return 'atencao';
        return 'ok';
      },

      // ---- the `field:value` filter, ANDed -------------------------------
      //
      // Fields: node:, status:, tipo:, estado:, cred:, id:. Terms are ANDed, and a term
      // with no `:` is a free search over id/name/address/state.
      //
      // 🔴 `tag:` DOES NOT exist, and the absence is declared: the inventory model does
      // not store tags (model.go, Node) — offering the field would always return an
      // empty list, and the operator would conclude the nodes had lost their tags.
      //
      // 🔴 An UNKNOWN field (`foo:bar`) is not ignored: it becomes a literal search for
      // the whole term. Discarding the restriction nobody understood would return MORE
      // rows than the operator asked for, silently — the worst possible error in a
      // filter, because the list looks like it obeyed.
      pvxFiltraNos(lista, texto, segmento, estadoDe) {
        const termos = String(texto || '').toLowerCase().split(/\s+/).filter(Boolean);
        return (lista || []).filter(function (n) {
          if (segmento && estadoDe(n) !== segmento) return false;
          const st = (n.status && n.status.value) || '';
          const cred = (n.credential && n.credential.state) || '';
          const tipo = String(n.id || '').split('/')[0];
          return termos.every(function (t) {
            const i = t.indexOf(':');
            const campo = i > 0 ? t.slice(0, i) : '';
            const valor = i > 0 ? t.slice(i + 1) : t;
            switch (campo) {
              case 'node': return String(n.name || '').toLowerCase().includes(valor);
              case 'status': return st.toLowerCase().includes(valor);
              // 🔴 The filter has to accept THE WORD THE SCREEN SHOWS. The badges say
              // CT, VM, NÓ and EXT; if `tipo:ct` did not filter, the screen would be
              // teaching a vocabulary it then refuses — and the operator would only find
              // out by typing and seeing an empty list.
              case 'tipo': {
                const canon = { ct: 'lxc', conteiner: 'lxc', contêiner: 'lxc', container: 'lxc',
                                vm: 'qemu', maquina: 'qemu', 'máquina': 'qemu',
                                no: 'node', 'nó': 'node', host: 'node', hipervisor: 'node' }[valor] || valor;
                return tipo.toLowerCase() === canon
                    || String(n.kind || '').toLowerCase() === canon
                    || String(n.kind || '').toLowerCase() === valor;
              }
              case 'estado': return estadoDe(n) === valor;
              case 'cred': return cred.toLowerCase().includes(valor);
              case 'id': return String(n.id || '').toLowerCase().includes(valor);
            }
            return [n.id, n.name, n.address, st, cred, tipo].join(' ').toLowerCase().includes(t);
          });
        });
      },

      // 🔴 pvxReescopa is the fix for Portainer #4430 — "select all, FILTER,
      // delete" deleted containers that had never been on the operator’s screen
      // ("tragedy all containers have been deleted"). The selection does NOT
      // survive a change of filter: whoever is not visible is not selected, and
      // there is no path in this file that changes the filter without going
      // through here.
      pvxReescopa(sel, visiveis) {
        const vistos = {};
        (visiveis || []).forEach(function (n) { vistos[n.id] = true; });
        return (sel || []).filter(function (id) { return vistos[id] === true; });
      },

      // ---- the health band, which IS the filter --------------------------
      //
      // 🔴 One thing, not two. A band that informs plus a separate filter that acts
      // force the operator to read the number in one place and reproduce it in another.
      // Here the number IS the button (cast from Portainer’s StatusSummaryBar, in
      // production): radiogroup, a count in each segment, and clicking the segment
      // ALREADY ACTIVE turns the filter off.
      //
      // The count shows even when it is ZERO, and the zeroed segment stays drawn: a
      // layout that changes width with the health of the laboratory moves the click
      // target precisely during an incident.
      pvxSegmentos(lista, estadoDe) {
        // `sumiu` goes at the END, next to `parado`: the band is read left to right by
        // urgency, and a node that left the hypervisor does not compete with a critical
        // one.
        const ordem = ['vencido', 'sem-credencial', 'critico', 'atencao', 'parado', 'sumiu', 'ok'];
        const conta = {};
        ordem.forEach(function (k) { conta[k] = 0; });
        (lista || []).forEach(function (n) {
          const e = estadoDe(n);
          if (conta[e] !== undefined) conta[e]++;
        });
        return ordem.map(function (k) { return { chave: k, n: conta[k] }; });
      },

      // ---- the confirmation ladder, in three rungs -----------------------
      //
      //   1 — no dialog.          Turn on. Restoring destroys nothing.
      //   2 — confirmation.       Graceful shutdown, create/delete snapshot.
      //   3 — TYPE the name.      Cut power, rollback, revoke credential.
      //
      // Precedent: Vercel requires typing the project name to delete AND to pause, but
      // resuming "takes effect immediately and does not ask for confirmation". The
      // destructive direction locks; the restorative one is free.
      //
      // 🔴 And a bulk action climbs A WHOLE RUNG. Starting one guest is rung 1;
      // starting seven at once is rung 2. A bulk mistake is not the individual mistake
      // repeated — it is irreversible on another scale.
      pvxDegrau(acao, quantos) {
        const BASE = {
          start: 1, shutdown: 2, stop: 3,
          snapcriar: 2, snapapagar: 2, rollback: 3, revogar: 3,
          // restarting interrupts service but does not destroy: same rung as the graceful
          // shutdown. `stop` stays at 3 because pulling the cord is another category
          // altogether.
          reboot: 2,
          // cloning does NOT alter the source — the risk is spending disk and creating a
          // guest nobody wanted, not losing data. Rung 2.
          clone: 2,
          // storing a copy is the only action on the screen that only ADDS. Rung 1: a
          // dialog on every click would train him to confirm without reading, and the price
          // of one extra backup is disk space.
          backup: 1,
        };
        const base = BASE[acao] || 2;
        const passo = Number(quantos) > 1 ? 1 : 0;
        return Math.min(3, base + passo);
      },

      // ---- the states of a CONTROL, and the reason always written out -----
      //
      // 🔴 An unavailable control IS DISABLED WITH A REASON; it does not vanish.
      // Portainer does `if (!authorized) return null` and the button evaporates — the
      // operator is left hunting for a button he remembers seeing. Coolify disables it
      // and writes "You do not have permission…". For a single operator, vanishing in
      // silence is the worse of the two: there is no colleague to ask what happened to
      // the button.
      //
      // One function for every control, and it always returns the pair {pode, motivo} —
      // never a naked boolean, because a naked boolean is exactly what produces a grey
      // button with no explanation.
      pvxAcaoEstado(n, acao) {
        if (!n) return { pode: false, motivo: 'unknown node' };
        const st = (n.status && n.status.value) || '';
        const cred = (n.credential && n.credential.state) || '';
        const ligado = st === 'running' || st === 'online';

        // A node the hypervisor no longer lists accepts no action at all — and the reason
        // says so, instead of letting the operator click and get back the Perl error PVE
        // returns for a vmid that does not exist.
        if (n.ausente_desde) {
          return { pode: false, motivo: 'the hypervisor no longer lists this node — it was deleted, or the panel lost access to it' };
        }
        if (acao === 'revogar') {
          if (cred === 'ausente') return { pode: false, motivo: 'there is no credential to revoke on this node' };
          if (cred === 'revogada') return { pode: false, motivo: 'the credential for this node has already been revoked' };
          // An EXPIRED credential can still be revoked: revoking is cleanup, and the
          // expired token goes on existing on the hypervisor until somebody deletes it.
          return { pode: true, motivo: '' };
        }

        // 🔴 CLONING AND STORING A COPY DO NOT DEPEND ON THE NODE’S CREDENTIAL.
        //
        // Both go through the PANEL’s token, because they require VM.Allocate on
        // /vms/<newid> and Datastore.AllocateSpace on /storage/<name> — paths where the
        // node token has no ACL at all. Letting them fall into the `cred !== 'ok'` guard
        // further down would lock the button on a guest the panel can copy perfectly well
        // (`pbs`, today, is exactly that case: it has no node token and is clonable all
        // the same).
        if (acao === 'clone' || acao === 'backup') {
          if (!this.pvxEhGuest(n)) {
            return { pode: false, motivo: n.kind === 'host'
              ? 'the hypervisor is not a guest — there is nothing to copy here'
              : 'external node: not a guest of this hypervisor' };
          }
          if (acao === 'backup') {
// 🔴 "I HAVE NOT READ IT YET" IS NOT "IT DOES NOT EXIST".
            //
            // The storage list only arrives when somebody asks for it. While it has not
            // arrived, saying "no storage accepts backups" is asserting something about the
            // HYPERVISOR from an absence that belongs to the PANEL. The operator read that on
            // screen with `pbs` up and running on the other side — and it is the same disease
            // that makes absent data add up as zero.
            if (!this.pvx.storage) {
              return { pode: false, motivo: 'I have not read this hypervisor storage list yet' };
            }
            if (!this.pvxStoragesDeBackup().length) {
              return { pode: false, motivo: 'no storage on this hypervisor accepts backups' };
            }
          }
          return { pode: true, motivo: '' };
        }

        const energia = acao === 'start' || acao === 'shutdown' || acao === 'stop' || acao === 'reboot';
        if (energia && (n.kind !== 'guest' || !(n.vmid > 0))) {
          // 🔴 The two cases are NOT the same, and the message has to know that. Found by
          // reading the live inventory: besides the host, there is the `canario` node (kind
          // `externo`, transport `agente`), which is not a guest of any hypervisor. Telling
          // it "the hypervisor is not powered from the panel" would be explaining with the
          // wrong reason — and a wrong reason is worse than no reason, because it sends the
          // operator looking in the wrong place.
          if (n.kind === 'externo') {
            return { pode: false, motivo: 'external node: not a guest of this hypervisor — powering on and off will depend on the lab agent (Phase 8)' };
          }
          return { pode: false, motivo: 'the hypervisor does not power on or off from the panel — that is the physical button on the machine' };
        }
        if (cred !== 'ok') {
          const porque = ({
            ausente: 'no node token in the vault',
            revogada: 'credential revoked',
            expirada: 'credential expired',
          })[cred] || 'credential state unknown';
          return { pode: false, motivo: porque + ' — the panel has no way to act on this node' };
        }
        // 🔴 These two guards were born from a MEASURED defect: the first wave found
        // `qmstart 208 — VM 208 already running` in the hypervisor’s trail, three times.
        // Somebody orders a start on something already running. A button that accepts the
        // useless order fills the task trail with noise, and that trail is where the real
        // failure gets hunted afterwards.
        // Restarting a powered-off guest is not "starting" it: PVE returns an error and
        // the trail gains noise. The right button for that already sits next to it.
        if (acao === 'reboot' && !ligado) {
          return { pode: false, motivo: 'it is powered off — to start it, use "Turn on"' };
        }
        if (acao === 'start' && ligado) {
          return { pode: false, motivo: 'it is already running — asking again returns "already running" and pollutes the task trail' };
        }
        if ((acao === 'shutdown' || acao === 'stop') && !ligado) {
          return { pode: false, motivo: 'it is already powered off' };
        }
        return { pode: true, motivo: '' };
      },

      // ══ THE NOTE: what this box DOES ════════════════════════════════════
      //
      // 🔴 THE SOURCE ALREADY EXISTED AND IT WAS NOT THE PANEL. Every guest in this
      // laboratory — and the hypervisor itself — already has a description written in
      // PVE’s `description` field (the "Notes" of the native screen), in Markdown,
      // explaining what the box does, why it exists, what happens if it falls and where
      // the things that matter live. The panel READS that. Writing a second description
      // here would create the second truth, and the two would diverge on the first day
      // somebody edited the Proxmox one.
      async pvxLoadNota(id) {
        if (!id) return;
        if (this.pvx.nota.node === id && !this.pvx.nota.erro) return; // already have it
        this.pvx.nota = { node: id, markdown: '', origem: '', motivo: '', carregando: true, erro: '',
                          editando: false, rascunho: '', salvando: false };
        try {
          const r = await this.api('/api/nodes/' + id + '/nota', { raw: true });
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          // Only applies if the selection did not change in the meantime: with fast
          // clicking between nodes, the slow response from the first would arrive LATER and
          // paint the wrong node’s note onto the right node’s screen.
          if (this.pvx.nota.node !== id) return;
          this.pvx.nota.markdown = d.markdown || '';
          this.pvx.nota.origem = d.origem || '';
          this.pvx.nota.motivo = d.motivo || '';
        } catch (e) {
          if (this.pvx.nota.node !== id) return;
          // Stable shape on the error path: the field does NOT go back to null.
          this.pvx.nota.erro = this._errText(e);
        } finally {
          if (this.pvx.nota.node === id) this.pvx.nota.carregando = false;
        }
      },

      // ── editing the note WITHOUT leaving the panel ──────────────────────
      //
      // 🔴 THE DRAFT IS SEPARATE FROM THE TEXT IN FORCE. `pvx.nota.markdown` is what
      // the hypervisor holds; `rascunho` is what is being typed. Editing the first one
      // directly would make "cancel" lose the original — and would make the screen
      // show, while you type, a note nobody saved.
      pvxEditaNota() {
        // 🔴 DOES NOT OPEN WHILE THE NOTE IS LOADING.
        //
        // The markup already hides the button at that instant, but a guard that lives
        // only in the markup is a guard the next screen forgets. And the price here is
        // high: opening the draft before the note arrives gives an EMPTY text, and saving
        // that empty text WIPES the description that was on the hypervisor.
        if (this.pvx.nota.carregando) {
          this.showToast('the note is still loading', 'err');
          return;
        }
        if (this.pvx.nota.origem === 'fora-do-pve' || this.pvx.nota.origem === 'inexistente') {
          this.showToast(this.pvx.nota.motivo || 'there is no note to edit on this node', 'err');
          return;
        }
        this.pvx.nota.rascunho = this.pvx.nota.markdown || '';
        this.pvx.nota.editando = true;
      },
      pvxCancelaNota() {
        this.pvx.nota.editando = false;
        this.pvx.nota.rascunho = '';
      },
      pvxNotaMudou() {
        return this.pvx.nota.editando && this.pvx.nota.rascunho !== (this.pvx.nota.markdown || '');
      },
      // The ceiling is the server’s own (pve.TamanhoMaximoDaNota). Duplicating a number
      // is debt, but the alternative — finding out the limit only after writing 9 KB
      // and pressing save — is worse. The server remains the one in charge.
      NOTA_MAX: 8192,
      pvxNotaCabe() { return (this.pvx.nota.rascunho || '').length <= this.NOTA_MAX; },

      async pvxSalvaNota() {
        const n = this.pvx.nota;
        if (!n.node || n.salvando) return;
        if (!this.pvxNotaCabe()) {
          this.showToast('the note went past ' + this.NOTA_MAX + ' characters — a note is meant to be READ', 'err');
          return;
        }
        n.salvando = true;
        n.erro = '';
        try {
          const r = await this.api('/api/nodes/' + n.node + '/nota', {
            method: 'PUT', body: JSON.stringify({ markdown: n.rascunho }),
          });
          const d = await r.json().catch(() => ({}));
          // Only applies if we are still on the same node: with fast clicking, the slow
          // response would paint the saved note onto another guest’s screen.
          if (this.pvx.nota.node !== n.node) return;
          this.pvx.nota.markdown = d.markdown !== undefined ? d.markdown : n.rascunho;
          this.pvx.nota.origem = d.origem || 'pve-notes';
          this.pvx.nota.editando = false;
          this.pvx.nota.rascunho = '';
          this.showToast('note saved in Proxmox', 'ok');
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.nota.erro = txt;
          this.showToast('failed to save the note: ' + txt, 'err');
        } finally {
          if (this.pvx.nota.node === n.node) this.pvx.nota.salvando = false;
        }
      },

      // pvxMd: minimal Markdown, and SAFE BY CONSTRUCTION.
      //
      // 🔴 THE ORDER IS WHAT MAKES THIS SAFE: escape EVERYTHING first, then reintroduce
      // the tags this renderer knows. No character from the source text can turn into a
      // tag, because by the time the tags go in there is no `<` left coming from the
      // text. The reverse path — convert and then try to sanitise — is the classic
      // source of XSS.
      //
      // The note comes from the hypervisor, written by the operator. It is not hostile
      // input today; but "not hostile today" is exactly the premise that ages.
      pvxMd(txt) {
        const esc = (x) => String(x).replace(/[&<>"']/g, (c) => (
          { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
        const linhas = String(txt || '').replace(/\r\n?/g, '\n').split('\n');
        const out = [];
        let emLista = false, emCodigo = false;
        const fechaLista = () => { if (emLista) { out.push('</ul>'); emLista = false; } };

        // Inline spans, applied AFTER the escape. `code` comes first so that ** inside
        // backticks does not turn into bold.
        const inline = (l) => esc(l)
          .replace(/`([^`]+)`/g, '<code class="pvx-md-code">$1</code>')
          .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
          .replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>')
          // Link: http(s) only. Any other scheme (javascript:, data:) stays as text — it
          // does not disappear, but it does not become a clickable link either.
          .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g,
            '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');

        for (const bruta of linhas) {
          const l = bruta.trimEnd();
          if (/^```/.test(l)) {
            fechaLista();
            out.push(emCodigo ? '</pre>' : '<pre class="pvx-md-pre">');
            emCodigo = !emCodigo;
            continue;
          }
          if (emCodigo) { out.push(esc(bruta)); continue; }
          if (/^\s*$/.test(l)) { fechaLista(); continue; }
          if (/^---+$/.test(l)) { fechaLista(); out.push('<hr class="pvx-md-hr">'); continue; }
          const h = l.match(/^(#{1,6})\s+(.*)$/);
          if (h) {
            fechaLista();
            const n = Math.min(6, h[1].length + 2); // a # in the note becomes an h3 on screen
            out.push('<h' + n + ' class="pvx-md-h">' + inline(h[2]) + '</h' + n + '>');
            continue;
          }
          const li = l.match(/^\s*[-*]\s+(.*)$/);
          if (li) {
            if (!emLista) { out.push('<ul class="pvx-md-ul">'); emLista = true; }
            out.push('<li>' + inline(li[1]) + '</li>');
            continue;
          }
          fechaLista();
          out.push('<p>' + inline(l) + '</p>');
        }
        fechaLista();
        if (emCodigo) out.push('</pre>');
        return out.join('\n');
      },

      // ══ MAINTENANCE: cloning and storing a copy ═════════════════════════
      //
      // 🔴 NEITHER WAITS FOR THE TASK, AND THE SCREEN SAYS SO.
      //
      // A full clone of 14 GB takes minutes. Holding the request for minutes is the
      // same as having no button — the browser gives up first. The server returns
      // `status: "aceita"` with the UPID, and here the screen OPENS THE TASK LOG right
      // away. The operator follows the real outcome instead of reading a "done" nobody
      // verified.

      // pvxStoragesDeBackup filters the storages that accept `backup`. It comes
      // out of pvx.storage, which the Storage tab already loads — there is no
      // second call, and no hand-typed list to go stale when a storage comes or
      // goes (`backupusb` left, and a fixed list would still be offering it
      // today).
      pvxStoragesDeBackup() {
        const pools = (this.pvx.storage && this.pvx.storage.pools) || [];
        return pools.filter(p => Array.isArray(p.content) && p.content.indexOf('backup') >= 0);
      },

      // pvxAbreClone ASKS the hypervisor which id is free. Nobody types a number here:
      // guessing from the list on screen is a race with anything else that allocates in
      // the meantime, and the prize for getting it wrong is an error in the middle of a
      // clone.
      async pvxAbreClone(n) {
        const estado = this.pvxAcaoEstado(n, 'clone');
        if (!estado.pode) { this.showToast(estado.motivo, 'err'); return; }
        this.pvx.clone = { aberto: true, carregando: true, origem: n.id, origemNome: n.name,
                           novoID: 0, nome: '', ligado: false,
                           precisaSnap: false, snapshots: [], snapshot: '', erro: '' };
        try {
          const r = await this.api('/api/nodes/' + n.id + '/clone', { raw: true });
          if (!r.ok) throw await this._apiError(r);
          const d = await r.json();
          this.pvx.clone.novoID = d.next_id || 0;
          this.pvx.clone.nome = d.sugestao || '';
          this.pvx.clone.ligado = !!d.ligado;
          // 🔴 A RUNNING container only clones from a snapshot. A hypervisor rule,
          // discovered by live proof — not by reading the source.
          this.pvx.clone.precisaSnap = !!d.precisa_snapshot;
          this.pvx.clone.snapshots = Array.isArray(d.snapshots) ? d.snapshots : [];
          this.pvx.clone.snapshot = this.pvx.clone.snapshots[0] || '';
        } catch (e) {
          // The shape does NOT go back to null on the error path: the dialog stays open
          // showing the reason, instead of vanishing without explaining.
          this.pvx.clone.erro = this._errText(e);
        } finally {
          this.pvx.clone.carregando = false;
        }
      },
      pvxFechaClone() { this.pvx.clone.aberto = false; },

      pvxClonaConfirma() {
        const c = this.pvx.clone;
        if (!c.novoID) { this.showToast('no target id — the hypervisor did not answer which one is free', 'err'); return; }
        if (c.precisaSnap && !c.snapshot) {
          this.showToast('a running container only clones from a snapshot — create one above, or shut the guest down', 'err');
          return;
        }
        // The warning changes with the guest’s STATE, and that difference is real:
        // copying a running guest produces a crash-consistent copy, as if the cord
        // had been pulled mid-way. That is no reason to forbid it — databases
        // survive that —, it is a reason for the operator to know.
        // The warning changes with what EXISTS, not with a generic text: cloning
        // from a snapshot, the copy is the state of that snapshot — it is not
        // crash-consistent, and saying that it is would be a lie that trains
        // people to ignore warnings.
        let avisoLigado = '';
        if (c.snapshot) {
          avisoLigado = `\n\nThe copy comes from snapshot "${c.snapshot}", not from the current state: `
            + `anything written after it is NOT included.`;
        } else if (c.ligado) {
          avisoLigado = `\n\n⚠ "${c.origemNome}" is RUNNING. The copy comes out crash-consistent: `
            + `the same state as an abrupt power cut. For a clean copy, shut it down first.`;
        }
        this.askConfirm('Clone ' + c.origemNome,
          `Creates guest ${c.novoID} ("${c.nome || 'unnamed'}") as a FULL COPY of "${c.origemNome}". `
          + `It takes up roughly as much disk as the source. `
          + `The source is NOT changed or interrupted.`
          + `\n\nThe task takes minutes and runs on the hypervisor: the panel does not wait for it, `
          + `it opens the task log so you can follow along.` + avisoLigado,
          () => this.pvxClonaAgora(), {});
      },

      async pvxClonaAgora() {
        const c = this.pvx.clone;
        if (this.pvx.busy) return;
        this.pvx.busy = c.origem;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + c.origem + '/clone', {
            method: 'POST', body: JSON.stringify({ novo_id: c.novoID, nome: c.nome }),
          });
          const d = await r.json().catch(() => ({}));
          this.pvx.clone.aberto = false;
          this.showToast(`clone ${c.novoID} requested — follow the task`, 'ok');
          if (d.upid) this.pvxAbreLog(d.upid);
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.pvx.clone.erro = txt;
          this.showToast('failed to clone: ' + txt, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxRecarrega();
        }
      },

      pvxBackupConfirma(n) {
        const estado = this.pvxAcaoEstado(n, 'backup');
        if (!estado.pode) { this.showToast(estado.motivo, 'err'); return; }
        const st = this.pvx.bkp.storage || (this.pvxStoragesDeBackup()[0] || {}).id || '';
        if (!st) { this.showToast('no storage accepts backups', 'err'); return; }
        this.pvx.bkp.storage = st;
        // Rung 1: no dialog. Storing a copy is the only action on this screen that only
        // ADDS — it does not alter, does not interrupt, does not delete. A dialog here
        // would train him to confirm without reading, and it is that automatic
        // confirmation that later lets "cut power" slip through.
        this.pvxBackupAgora(n, st);
      },

      async pvxBackupAgora(n, storage) {
        if (this.pvx.busy) return;
        this.pvx.busy = n.id;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + n.id + '/backup', {
            method: 'POST',
            body: JSON.stringify({ storage, modo: this.pvx.bkp.modo || 'snapshot' }),
          });
          const d = await r.json().catch(() => ({}));
          this.showToast(`copy of ${n.name} requested on ${storage} — follow the task`, 'ok');
          if (d.upid) this.pvxAbreLog(d.upid);
        } catch (e) {
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.showToast(`failed to store a copy of ${n.name}: ` + txt, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxRecarrega();
        }
      },

      // ---- bridge between the pure functions and the component ------------

      pvxNos() {
        // The list comes from 40-nodes.js, which is what talks to /api/nodes. This module
        // draws; it neither fetches nodes nor translates credentials.
        return (this.nodes && this.nodes.list) ? this.nodes.list : [];
      },

      pvxNosFiltrados() {
        return this.pvxFiltraNos(this.pvxNos(), this.pvx.filtro, this.pvx.segmento, this.pvxNoEstado);
      },
      pvxHost() { return this.pvxNos().filter(n => n.kind === 'host'); },

      // 🔴 EVERY filter change goes through here, and that is why the re-scoping cannot
      // be forgotten on one of the paths: there is no second path.
      pvxSetFiltro(texto) {
        this.pvx.filtro = texto;
        this.pvx.sel = this.pvxReescopa(this.pvx.sel, this.pvxNosFiltrados());
      },
      pvxSetSegmento(chave) {
// Clicking the segment ALREADY ACTIVE turns the filter off (cast from Portainer:
        // `value === key ? null : key`). Without it, the operator who filtered by
        // "erro" has to hunt for a clear button he does not know exists.
        this.pvx.segmento = (this.pvx.segmento === chave) ? '' : chave;
        this.pvx.sel = this.pvxReescopa(this.pvx.sel, this.pvxNosFiltrados());
      },
      // 🔴 pvxLimpaFiltro exists even when the filtered value has vanished from
      // the dataset — it is the fix for Portainer #12938, where the filter chip
      // disappeared while STAYING active and the list sat empty with no way at
      // all to clear it. The "Showing: X" indicator is rendered from the filter
      // state, never from the data.
      pvxLimpaFiltro() {
        this.pvx.filtro = '';
        this.pvx.segmento = '';
        this.pvx.sel = this.pvxReescopa(this.pvx.sel, this.pvxNosFiltrados());
      },
      pvxTemFiltro() {
        // Note that it looks ONLY at the filter state. Consulting the data here ("are
        // there results?") would make the announcement vanish exactly when the filter
        // emptied the list — which is when it is most needed.
        return !!(this.pvx.filtro || this.pvx.segmento);
      },
      pvxMostrando() {
        const partes = [];
        if (this.pvx.segmento) partes.push(this.pvxRotuloEstado(this.pvx.segmento));
        if (this.pvx.filtro) partes.push(this.pvx.filtro);
        return partes.join(' · ');
      },
      pvxContagem() {
        return `${this.pvxNosFiltrados().length} of ${this.pvxNos().length}`;
      },
      pvxSegmentosDaTela() {
        return this.pvxSegmentos(this.pvxNos(), this.pvxNoEstado);
      },

      // ---- gauges ---------------------------------------------------------
      //
      // 🔴 The gauge goes GREY when the node is not live. A green bar over expired
      // data, or over a powered-off guest, is a visual lie: the colour asserts "I
      // measured this and it is fine" when nobody measured. Grey is "I do not know".
      pvxMedidorVivo(n) {
        if (!n || n.stale) return false;
        const st = (n.status && n.status.value) || '';
        return st === 'running' || st === 'online';
      },

      pvxMedidor(n, qual) {
        const pct = pvxPctDoMedidor(n, qual);
        const vivo = this.pvxMedidorVivo(n);
        if (pct === null) {
          return { medido: false, vivo, pct: 0, tier: 'ok', texto: '—', motivo: pvxMotivoSemMedida(n, qual) };
        }
        // 🔴 The hysteresis memory does NOT live in Alpine’s state, and that is no
        // detail: `pvxMedidor` is called from inside rendering expressions (x-text,
        // :style, :aria-valuenow), and writing to REACTIVE state during rendering
        // is the classic route to an effect loop — the write invalidates the effect
        // that produced it. The Map lives in the IIFE’s closure, outside the
        // reactive proxy, and because of that storing the previous tier re-triggers
        // no render at all.
        const chave = (n.id || '') + ':' + qual;
        const tier = this.pvxTier(pct, tiersDeHisterese.get(chave));
        tiersDeHisterese.set(chave, tier);
        return { medido: true, vivo, pct, tier, texto: pvxTextoDoMedidor(this, n, qual, pct), motivo: '' };
      },
      pvxMedidorCor(m) {
        if (!m || !m.medido || !m.vivo) return '#64748b';
        return m.tier === 'critico' ? '#ef4444' : (m.tier === 'atencao' ? '#f59e0b' : '#22c55e');
      },
      pvxMedidorEstilo(m) {
        const v = Math.max(0, Math.min(100, Number(m && m.pct) || 0));
        return `width:${v}%;background:${this.pvxMedidorCor(m)}`;
      },
      // pvxTaxa formats bytes/s. -1 means "cannot be derived" (first observation,
      // a gap wider than a tick and a half, or a counter that reset because the
      // guest rebooted) — and that NEVER becomes "0 B/s", which reads as
      // "no traffic".
      pvxTaxa(v) {
        if (v === null || v === undefined) return '—';
        if (Number(v) < 0) return 'no baseline';
        return this.pvxBytes(v) + '/s';
      },
      pvxObs(o) { return (o && o.value !== undefined) ? o.value : null; },
      // 🔴 READING A TIMESTAMP IN ONE CALL, SAFE AT BOTH LEVELS.
      //
      // The template wrote `pvx.saude.version.value` under the shallow guard
      // `pvx.saude ? …`. The guard covered ONE level and the expression descended
      // THREE: a hypervisor that returns the health WITHOUT one of the fields — a
      // trimmed permission, an error path, a PVE version that stopped sending that —
      // throws, and a throw in Alpine takes the whole app down, terminal included.
      //
      // Nine expressions had that shape. Fixing the nine with `?.` would leave the
      // tenth to the memory of whoever writes it. A single function is the fix that
      // does not depend on remembering.
      pvxCampo(obj, nome) { return this.pvxObs(obj && obj[nome]); },

      // 🔴 "IS IT BUSY?" HAS TO RETURN A BOOLEAN. This is not fussiness — it is the
      // defect that left the operator WITH NO BUTTONS AT ALL.
      //
      // `pvx.busy` is a STRING: empty when idle, holding the node id during the
      // operation (the screen uses that id to know WHICH row is in flight). The
      // template wrote `:disabled="!pode || pvx.busy"`. With the action allowed and
      // nothing in flight that gives `false || ''`, which is `''`.
      //
      // And Alpine, on a BOOLEAN attribute, only removes the attribute when the value
      // is `null`, `undefined` or `false` — an empty string SETS it. In other words:
      // the expression said "it is not busy" and the browser understood "disabled".
      // Every node action button was born dead, forever, on every guest.
      //
      // A `!!` in the expression would fix today’s six occurrences and leave the
      // seventh to the memory of whoever writes it. A function with a name is the fix
      // that does not depend on remembering.
      pvxOcupado() { return !!this.pvx.busy; },

      // ---- node side panel -----------------------------------------------
      //
      // 🔴 A SIDE panel, not an accordion. The accordion pushes everything below it off
      // the screen: opening the detail of the first guest hides the other eight, which
      // is the opposite of what a list is for. The panel sits alongside and the list
      // stays whole, with the open row highlighted.
      async pvxAbre(n) {
        if (this.pvx.aberto === n.id) { this.pvxFecha(); return; }
        this.pvxFechaConsole();
        this.pvx.aberto = n.id;
        this.pvx.detalhe = null;
        this.pvx.snaps = [];
        this.pvx.guestSel = (n.kind === 'guest' && n.vmid > 0) ? n.id : '';
        try {
          const r = await this.api('/api/nodes/' + n.id);
          this.pvx.detalhe = await r.json().catch(() => null);
        } catch (e) {
          this.pvx.lastError = this._errText(e);
        }
        if (this.pvx.guestSel) this.pvxLoadSnaps(this.pvx.guestSel);
        // 🔴 THE NOTE LOADS HERE, ON THE CLICK PATH.
        //
        // It used to hang off `pvxVaiPara` alone, which is the TAB change — and clicking
        // a node does NOT go through there: `pvxSeleciona` sets `pvx.aba` and calls
        // `pvxAbre` directly. Result: the "What this box does" block opened with a title,
        // a button and NOTHING in between, neither text nor empty state, because the
        // state was still the initial one.
        //
        // The harness did not catch it because its `abre()` called `pvxVaiPara`
        // explicitly — it was more generous than a real click.
        this.pvxLoadNota(n.id);
      },
      pvxFecha() {
        this.pvxFechaConsole();
        this.pvx.aberto = '';
        this.pvx.detalhe = null;
        this.pvx.snaps = [];
        this.pvx.guestSel = '';
      },
      pvxNoAberto() {
        const id = this.pvx.aberto;
        return id ? (this.pvxNos().find(n => n.id === id) || null) : null;
      },

      // ---- selection and bulk action -------------------------------------

      pvxSelecionado(id) { return this.pvx.sel.indexOf(id) >= 0; },
      pvxToggleSel(id) {
        const i = this.pvx.sel.indexOf(id);
        if (i >= 0) this.pvx.sel.splice(i, 1);
        else this.pvx.sel.push(id);
      },
      // "All" means all the VISIBLE ones, never all the existing ones — it is the same
      // rule as the re-scoping, in the input direction.
      pvxSelTodos() {
        const visiveis = this.pvxNosFiltrados().map(n => n.id);
        this.pvx.sel = (this.pvx.sel.length === visiveis.length) ? [] : visiveis;
      },
      pvxEmLote() { return this.pvx.sel.length > 0; },
      pvxSelNos() {
        const ids = this.pvx.sel;
        return this.pvxNos().filter(n => ids.indexOf(n.id) >= 0);
      },

      // 🔴 pvxPowerEmMassa ENUMERATES what is going to happen, and enumerates what is
      // NOT going to happen as well. A confirmation that says "7 nodes selected" hides
      // that two of them will be silently skipped for want of a credential — and the
      // operator only finds out afterwards, counting who came up.
      pvxPowerEmMassa(acao) {
        const rotulos = { start: 'Turn on', shutdown: 'Shut down gracefully', stop: 'Cut the power' };
        const sel = this.pvxSelNos();
        const alvos = sel.filter(n => this.pvxAcaoEstado(n, acao).pode);
        const fora = sel.filter(n => !this.pvxAcaoEstado(n, acao).pode);
        if (!alvos.length) {
          this.showToast('none of the selected nodes accepts "' + acao + '" right now', 'err');
          return;
        }
        const nomes = alvos.map(n => `${n.name} (${n.id})`).join(', ');
        const pulados = fora.length
          ? `\n\nLEFT OUT (${fora.length}): ` + fora.map(n => `${n.name} — ${this.pvxAcaoEstado(n, acao).motivo}`).join('; ')
          : '';
        const degrau = this.pvxDegrau(acao, alvos.length);
        const aviso = (acao === 'stop'
          ? `Cuts the power to ${alvos.length} node(s) outright — the same as pulling the cable on each one.`
          : `Runs "${acao}" on ${alvos.length} node(s), one at a time, waiting for the hypervisor to finish each task.`)
          + `\n\nTHEY ARE: ${nomes}` + pulados
          + (degrau >= 3 ? `\n\nType IN BULK to confirm.` : '');
        const opcoes = degrau >= 3 ? { danger: true, requireText: 'IN BULK' } : {};
        this.askConfirm(rotulos[acao] + ` — ${alvos.length} node(s)`, aviso, async () => {
          for (const n of alvos) {
            await this.pvxPowerAgora(n, acao);
          }
          this.pvx.sel = [];
          await this.pvxRecarrega();
        }, opcoes);
      },

      // pvxPower is the single-node version, with the ladder applied. Rung 1 (start)
      // opens no dialog at all: restoring destroys nothing, and a dialog on every click
      // trains the operator to confirm without reading.
      pvxPower(n, acao) {
        const estado = this.pvxAcaoEstado(n, acao);
        if (!estado.pode) { this.showToast(estado.motivo, 'err'); return; }
        const degrau = this.pvxDegrau(acao, 1);
        if (degrau <= 1) { this.pvxPowerAgora(n, acao); return; }
        const rotulos = { shutdown: 'Shut down gracefully', stop: 'Cut the power', reboot: 'Restart' };
        let aviso;
        if (acao === 'stop') {
          aviso = `Cuts the power to "${n.name}" outright — the same as pulling the cable. `
            + `Whatever is in memory and has not been written is lost. `
            + `Type the node name (${n.name}) to confirm.`;
        } else if (acao === 'reboot') {
          // The warning says what REALLY happens, not "are you sure?". Whoever reads it
          // needs to know that the service goes down during the round trip and that the
          // request is made FROM THE INSIDE — a hung guest can simply ignore it.
          aviso = `Restarts "${n.name}" from the inside (the system receives the request and reboots itself). `
            + `Everything running on it is offline until it comes back. `
            + `If the system is stuck it may IGNORE the request — the task then fails `
            + `and the guest stays up, and then the way out is "Cut the power".`;
        } else {
          aviso = `Runs "${acao}" on "${n.name}". The response only comes back once the hypervisor has finished the task.`;
        }
        const opcoes = degrau >= 3 ? { danger: true, requireText: n.name } : {};
        this.askConfirm(rotulos[acao] || acao, aviso, () => this.pvxPowerAgora(n, acao), opcoes);
      },

      // pvxPowerAgora runs it and WAITS for the whole response: the server only answers
      // after the WaitTask, so the spinner covers the real operation, and not the
      // "request accepted".
      async pvxPowerAgora(n, acao) {
        if (this.pvx.busy && this.pvx.busy !== n.id) return;
        this.pvx.busy = n.id;
        this.pvx.lastError = '';
        try {
          const r = await this.api('/api/nodes/' + n.id + '/power', {
            method: 'POST', body: JSON.stringify({ action: acao }),
          });
          const d = await r.json().catch(() => ({}));
          this.showToast(`${n.name}: ${acao} finished (task ${d.upid || '—'})`, 'ok');
        } catch (e) {
          // The error body carries PVE’s exitstatus or the step that failed, and it goes to
          // the screen WHOLE. A summarised error is an error that does not help.
          const txt = this._errText(e);
          this.pvx.lastError = txt;
          this.showToast(`${n.name}: ${acao} failed: ${txt}`, 'err');
        } finally {
          this.pvx.busy = '';
          await this.pvxRecarrega();
        }
      },

      // pvxRevoga is the visible tip of the revocation decision, and it remains the
      // strongest warning on the screen: the operation is irreversible on BOTH sides
      // (the token leaves the hypervisor AND the vault). Rung 3, by definition.
      pvxRevoga(n) {
        const estado = this.pvxAcaoEstado(n, 'revogar');
        if (!estado.pode) { this.showToast(estado.motivo, 'err'); return; }
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
              this.showToast('credential revoked (' + (d.passos || []).join(' → ') + ')', 'ok');
            } catch (e) {
              // The error response NAMES the step that failed (pve.delete, pve.confirm401,
              // vault.delete, vault.recheck). Without that the operator does not know whether
              // the token died on the hypervisor or not.
              const txt = this._errText(e);
              this.pvx.lastError = txt;
              this.showToast('revocation failed: ' + txt, 'err');
            } finally {
              this.pvx.busy = '';
              // 🔴 Re-fetch from the SERVER: the credential’s new state comes from there, never
              // from a local cache that "knows" what it just did.
              await this.pvxRecarrega();
            }
          }, { danger: true, requireText: n.name });
      },

      async pvxRecarrega() {
        if (typeof this.loadNodes === 'function') await this.loadNodes();
        const aberto = this.pvx.aberto;
        this.pvx.sel = this.pvxReescopa(this.pvx.sel, this.pvxNosFiltrados());
        if (aberto && this.pvx.guestSel) await this.pvxLoadSnaps(this.pvx.guestSel);
      },

      // ---- the "needs you" band ------------------------------------------
      //
      // Only shows when there is something to say. A healthy node does not enter, and
      // the band disappears entirely when the laboratory is fine — a permanent header
      // saying "0 problems" is noise that teaches the eye to skip the region.
      pvxPrecisamDeVoce() {
        return this.pvxNos().filter(n => {
          const e = this.pvxNoEstado(n);
          // `sumiu` deliberately does NOT enter here: there is nothing to do about a node
          // the hypervisor no longer lists, and counting it as pending is exactly the noise
          // the operator complained about.
          return e === 'vencido' || e === 'sem-credencial' || e === 'critico';
        });
      },

      // ---- empty state: THREE, never one ---------------------------------
      //
      // "there is nothing" · "I am not allowed to see" · "the fetch failed" are
      // three diagnoses with three different operator actions. A single "no
      // results" turns a network failure into a conclusion about the
      // laboratory.
      pvxVazio() {
        if (this.pvx.forbidden || (this.nodes && this.nodes.forbidden)) return 'sem-permissao';
        if (this.nodes && this.nodes.lastError) return 'falhou';
        if (!this.pvxNos().length) return 'nada';
        if (!this.pvxNosFiltrados().length) return 'filtrado';
        return '';
      },

      // ---- periodic refresh ------------------------------------------------

      // 🔴 THERE IS NO "refresh every 10/30/60 s" SELECTOR, and there will not be. The
      // cadence is DERIVED from the resolution of the data: the server’s poller stamps
      // once per tick and the TTL comes in the payload, so asking faster than TTL/3
      // returns the same number with an age one second lower. A selector would offer
      // the operator a choice whose only real effect is spending network.
      //
      // Three rules go with the cadence:
      //   · a background tab does not refresh (document.hidden) — that was already so;
      //   · scrolling inside the table SUSPENDS the refresh, otherwise the rows escape
      //     from under the cursor mid-read;
      //   · the PREVIOUS dataset stays on screen during the fetch. Clearing it before
      //     the new one exists flashes the whole screen on every tick.
      pvxCadencia() {
        const ttl = Number(this.pvx.ttl) || 90;
        return Math.max(5000, Math.round((ttl * 1000) / 3));
      },
      // 🔴 `rolando` is armed by setTimeout and disarmed by setTimeout. Storing
      // "suspended until instant T" would require reading the browser’s clock, and this
      // file’s rule makes no exception even for a harmless use: the exception is what
      // makes the next person think they may compute age here.
      pvxRolou() {
        this.pvx.rolando = true;
        if (this._pvxRolTimer) clearTimeout(this._pvxRolTimer);
        this._pvxRolTimer = setTimeout(() => { this.pvx.rolando = false; }, 1500);
      },

      // Its OWN timer. It never touches another module’s timer — that is why this
      // module was born separate, and it still holds.
      pvxStartPoll() {
        if (this.pvxPollTimer) return;
        const bate = () => {
          this.pvxPollTimer = setTimeout(bate, this.pvxCadencia());
          if (document.hidden) return;
          if (this.currentView !== 'proxmox') return;
          if (this.pvx.rolando) return;
          this.pvxLoadSaude();
          this.pvxLoadTasks();
          // Capacity and zpool go into the poll because they are a heartbeat: it is the
          // refresh that makes the age GROW on screen when the hypervisor goes mute.
          // Without it the operator would see a frozen number without knowing that it had
          // stopped.
          this.pvxLoadStorage();
          this.pvxLoadZfs();
          // And the nodes, which are now the body of this screen. What used to re-fetch
          // them was 40-nodes.js’s timer, removed along with the Nodes tab.
          if (typeof this.loadNodes === 'function') this.loadNodes();
        };
        this.pvxPollTimer = setTimeout(bate, this.pvxCadencia());
      },
      pvxStopPoll() {
        if (this.pvxPollTimer) { clearTimeout(this.pvxPollTimer); this.pvxPollTimer = null; }
        if (this._pvxRolTimer) { clearTimeout(this._pvxRolTimer); this._pvxRolTimer = null; }
        this.pvx.rolando = false;
        // Leaving the tab CLOSES the console. Leaving it open would keep a live shell on
        // a guest with nobody watching — and the audit trail would record a session of
        // hours that was really a misclick.
        this.pvxFechaConsole();
      },
    };
  };
})();
