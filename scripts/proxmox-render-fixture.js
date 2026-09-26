// Fixture for the Proxmox tab rendering harness.
(function () {
  const origApp = window.app;
  window.app = function () {
    const c = origApp();
    c.token = 'harness';
    c.api = async function (rota, opts) {
      const corpo = (window.__respostas && window.__respostas[rota.split('?')[0]]) || {};
      if (opts && opts.raw) return { ok: true, status: 200, json: async () => corpo };
      return corpo;
    };
    c._apiError = async () => new Error('erro-de-teste');
    c._errText = (e) => String((e && e.message) || e);
    c.showToast = () => {};
    c.askConfirm = async () => false;
    c.pvxCicloVivo = function () {};
    // The harness renders the PROXMOX SECTION, not the PWA. The service worker
    // registration runs in the app’s init and fails here for want of a real
    // scope; the error shows up in the console and would bury the very signal
    // this check exists to see. Switching it off is honest — there is nothing
    // of Proxmox in it.
    c.installPWA = function () {};
    return c;
  };

  const NOTA_LAB = [
    '## lab — o painel do Lab (a partir da Fase 9)',
    '',
    '**O que faz:** hoje, **nada**. E a caixa existe assim mesmo.',
    '',
    '**Por que existe:** para que o painel tenha para onde ir sem cirurgia.',
    '',
    '---',
    '',
    '#### Técnico',
    '',
    '- Recriável do repositório: `sh bin/guest-create nodes/lab/204.conf --apply`',
    '- IP fixo `192.168.100.42`',
  ].join('\n');

  window.__respostas = {
    '/api/proxmox/storage': { pools: [
      { id: 'local', type: 'dir', content: ['backup', 'iso', 'vztmpl'], free: 8e11, total: 9e11, used: 1e10, used_pct: 1 },
      { id: 'local-zfs', type: 'zfspool', content: ['images', 'rootdir'], free: 8e11, total: 9e11, used: 6e10, used_pct: 7 },
      { id: 'pbs', type: 'pbs', content: ['backup'], free: 8.5e11, total: 9e11, used: 4.4e10, used_pct: 5 },
    ] },
    '/api/nodes/lxc/204/nota': { node: 'lxc/204', markdown: NOTA_LAB, origem: 'pve-notes' },
    '/api/nodes/lxc/205/nota': { node: 'lxc/205', markdown: '', origem: 'vazia' },
    '/api/nodes/canario/nota': { node: 'canario', markdown: '', origem: 'fora-do-pve',
                                 motivo: 'este nó não é guest deste hipervisor — a nota do PVE não se aplica a ele' },
    '/api/nodes/node/pve/nota': { node: 'node/pve', markdown: '# pve — o servidor de casa\n\n**O que é:** a máquina física que roda tudo.', origem: 'pve-notes' },
    // RUNNING CT with a snapshot: the hypervisor only clones a running container
    // from a snapshot — a rule discovered by the live proof, not from the source.
    '/api/nodes/lxc/204/clone': { origem: 'lxc/204', origem_nome: 'lab', tipo: 'lxc',
                                  next_id: 991, sugestao: 'lab-copia', ligado: true,
                                  precisa_snapshot: true, snapshots: ['antes-do-upgrade', 'base'] },
    // RUNNING CT with NO snapshot: the case where there is nothing to offer.
    '/api/nodes/lxc/202/clone': { origem: 'lxc/202', origem_nome: 'pbs', tipo: 'lxc',
                                  next_id: 993, sugestao: 'pbs-copia', ligado: true,
                                  precisa_snapshot: true, snapshots: [] },
    // STOPPED guest: no requirement at all.
    '/api/nodes/lxc/205/clone': { origem: 'lxc/205', origem_nome: 'observ', tipo: 'lxc',
                                  next_id: 992, sugestao: 'observ-copia', ligado: false,
                                  precisa_snapshot: false, snapshots: [] },
  };

  const C = () => document.body._x_dataStack[0];
  // 🔴 Do NOT use offsetParent: it is a property of HTMLElement and does NOT
  // exist on an SVG element, so `sv.offsetParent !== null` is ALWAYS true and
  // the visibility measurement lies exactly where the charts live.
  // checkVisibility() holds for both and considers the whole ancestor chain.
  const visivel = (el) => !!el && typeof el.checkVisibility === 'function' &&
    el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });

  const svgs = () => Array.from(document.querySelectorAll('svg[role="img"]')).filter(visivel);
  const partes = (sv) => {
    const ps = Array.from(sv.querySelectorAll('path'));
    const c = sv.querySelector('circle');
    return { area: (ps[0] && ps[0].getAttribute('d')) || '',
             traco: (ps[1] && ps[1].getAttribute('d')) || '',
             pontos: (ps[2] && ps[2].getAttribute('d')) || '',
             linhas: sv.querySelectorAll('line').length,
             cx: c && c.getAttribute('cx'), cy: c && c.getAttribute('cy') };
  };
  const subs = (d) => (d.match(/M/g) || []).length;

  const serie = (n, opts) => {
    opts = opts || {};
    const pontos = [];
    for (let i = 0; i < n; i++) {
      const p = { time: 1787000000 + i * 60 };
      if (!(opts.buracoEm && opts.buracoEm.includes(i))) {
        p.cpu = 0.1 + (i % 10) / 100; p.iowait = 0.01; p.loadavg = 1.5;
        p.memused = 8e9; p.memtotal = 64e9; p.arcsize = 4e9;
        p.rootused = 2e9; p.roottotal = 900e9; p.netin = 1e6; p.netout = 5e5;
        p.pressureiosome = 0.02; p.pressurememorysome = 0.01;
        p.mem = 1e9; p.maxmem = 2e9; p.disk = 5e9; p.maxdisk = 10e9;
        p.diskread = 1e5; p.diskwrite = 2e5;
      }
      pontos.push(p);
    }
    return { pontos, escopo: opts.escopo || 'hypervisor', janela: 'hour' };
  };

  // 🔴 NODES WITH THE REAL SHAPE, COPIED FROM THE LIVE /api/nodes RESPONSE.
  //
  // The previous version of this fixture invented `{id, kind, status:{value}}`
  // and nothing else. The result was worse than useless: ALL the buttons came up
  // disabled, because `pvxAcaoEstado` requires `credential.state === 'ok'`, and I
  // nearly concluded the whole panel was dead. Live, 9 of the 11 nodes have an ok
  // credential. A poor fixture is not "a simpler test" — it is a test that lies,
  // and it lies in the most expensive direction: inventing a defect that is not
  // there.
  //
  // The three cases below are the three that really exist in this lab: a guest
  // with an ok credential, a guest with NO credential (lxc/202 is like that
  // today) and the external node `canario`, which is no hypervisor’s guest.
  const carimbo = (v) => ({ value: v, observed_at: 1787256260 });
  const guest = (id, nome, vmid, st, cred) => ({
    id, name: nome, transport: 'pve-api', address: '192.168.1.1', kind: 'guest', vmid,
    status: carimbo(st), uptime: carimbo(821680), cpu_frac: carimbo(0.07), cpu_cores: carimbo(4),
    mem_used: carimbo(2e9), mem_total: carimbo(8e9), mem_host: carimbo(-1),
    disk_used: carimbo(1e10), disk_total: carimbo(5e10),
    net_in: carimbo(6e9), net_out: carimbo(5e9), disk_read: carimbo(2e9), disk_write: carimbo(6e8),
    net_in_rate: carimbo(-1), net_out_rate: carimbo(-1), template: false,
    credential: cred, age_seconds: 1, stale: false,
  });
  const CRED_OK = { token_id: 'lab@pve!node-x', expire: 1802645875, state: 'ok' };
  const CRED_AUSENTE = { token_id: '', expire: 0, state: 'ausente' };
  const NOS = [
    guest('lxc/204', 'lab', 204, 'running', CRED_OK),
    guest('lxc/205', 'observ', 205, 'stopped', CRED_OK),
    guest('lxc/202', 'pbs', 202, 'running', CRED_AUSENTE),
    guest('qemu/208', 'dev', 208, 'running', CRED_OK),
    { id: 'node/pve', name: 'pve', transport: 'pve-api', address: '', kind: 'host', vmid: 0,
      status: carimbo('online'), uptime: carimbo(0), cpu_frac: carimbo(0), cpu_cores: carimbo(0),
      mem_used: carimbo(0), mem_total: carimbo(0), mem_host: carimbo(0),
      disk_used: carimbo(0), disk_total: carimbo(0), net_in: carimbo(0), net_out: carimbo(0),
      disk_read: carimbo(0), disk_write: carimbo(0), net_in_rate: carimbo(0), net_out_rate: carimbo(0),
      template: false, credential: { token_id: 'lab@pve!audit', expire: 1802645875, state: 'ok' },
      age_seconds: 1, stale: false },
// 🔴 The node the hypervisor no longer lists. It is NOT deleted (a guest
    // disappears for being stopped, migrated or having its ACL withdrawn), but
    // neither can it be confused with "vencido" — which means the panel failed
    // to look.
    Object.assign(guest('lxc/101', 'prova-clone', 101, 'stopped', CRED_AUSENTE),
                  { ausente_desde: 1787200000, stale: true, age_seconds: 56260 }),
    // 🔴 THE SECOND CASE, and it is the one that makes this check NON-VACUOUS:
    // absent with FRESH data. It is the real window right after the poller marks
    // the absence — the last `status` is still recent and says "running", but the
    // hypervisor already does not list the node. Without this case the `stale`
    // guard covers on its own and the `ausente_desde` guard could be removed
    // without a single check biting (measured).
    Object.assign(guest('lxc/102', 'recem-apagado', 102, 'running', CRED_OK),
                  { ausente_desde: 1787256000, stale: false, age_seconds: 30 }),
    { id: 'canario', name: 'canario', transport: 'agente', address: '127.0.0.1:9', kind: 'externo', vmid: 0,
      status: carimbo(''), uptime: carimbo(0), cpu_frac: carimbo(0), cpu_cores: carimbo(0),
      mem_used: carimbo(0), mem_total: carimbo(0), mem_host: carimbo(0),
      disk_used: carimbo(0), disk_total: carimbo(0), net_in: carimbo(0), net_out: carimbo(0),
      disk_read: carimbo(0), disk_write: carimbo(0), net_in_rate: carimbo(0), net_out_rate: carimbo(0),
      template: false, credential: CRED_AUSENTE, age_seconds: -1, stale: true },
  ];

  // 🔴 Opens through the screen’s REAL path, not by writing into pvx.aberto.
  // A shortcut is not fidelity: `pvxSeleciona` also sets `pvx.guestSel`, and the
  // whole Copies tab depends on it. Pinning only `pvx.aberto`, the harness
  // reported "there is no clone button" for a screen that has the button.
  // 🔴 THE SERIES GOES TO BOTH PLACES.
  //
  // Once `abre()` started using the real path, `pvxVaiPara('graficos')` fires
  // `pvxLoadSerie()` — which is ASYNCHRONOUS and resolves LATER, overwriting
  // anything pinned into `pvx.serie`. Teaching the stub to return the same
  // series makes the state survive the load, instead of the test racing it.
  const poeSerie = (d) => {
    window.__respostas['/api/proxmox/rrd'] = d;
    C().pvx.serie = d;
  };

  const abre = (id, aba) => {
    const n = C().pvxNos().find((x) => x.id === id);
    if (!n) throw new Error('nó inexistente no fixture: ' + id);
    C().pvx.aberto = '';
    // 🔴 ABRE EXATAMENTE COMO UM CLIQUE ABRE, e nada além disso.
    //
    // Clicar num nó chama `pvxSeleciona`, que crava a aba 'resumo' e chama
    // `pvxAbre`. Clicar numa ABA chama `pvxVaiPara`. São dois caminhos
    // diferentes, e o harness tem de usar cada um onde ele se aplica.
    //
    // A versão anterior chamava `pvxVaiPara` SEMPRE, inclusive ao abrir no
    // 'resumo' — ou seja, era mais generosa que a realidade. Isso escondeu um
    // defeito que o operador viu na tela: a nota estava pendurada só em
    // `pvxVaiPara`, então clicar num nó abria o bloco "What this box does"
    // VAZIO, sem texto e sem estado vazio. Terceiro atalho meu a mentir nesta
    // sessão; os outros dois foram `pvx.aberto` sem `pvx.guestSel` e cravar
    // `pvx.aba` sem carregar nada.
    C().pvxSeleciona(n);
    if (aba !== 'resumo') C().pvxVaiPara(aba);
  };

  const exigeGraficos = (quantos, cheque) => () => {
    const s = svgs();
    if (s.length !== quantos) return { erro: 'esperava ' + quantos + ' gráficos VISÍVEIS, achei ' + s.length };
    const p = s.map(partes);
    if (p.some((x) => x.linhas !== 3)) return { erro: 'grade: ' + p.map((x) => x.linhas).join(',') + ' (esperado 3 em todos)' };
    if (p.some((x) => x.cx === null || isNaN(Number(x.cx)) || isNaN(Number(x.cy))))
      return { erro: 'ponto final sem coordenada numérica: ' + p.map((x) => x.cx + '/' + x.cy).join(' ') };
    const r = cheque ? cheque(p) : null;
    return r || { nota: quantos + ' gráficos visíveis e íntegros' };
  };

  window.__roteiro = [
    { nome: 'the section opens and the hypervisor is chosen', passo: () => {
        C().page = 'operations'; C().tabs.operations = 'proxmox';
        C().nodes = C().nodes || {}; C().nodes.list = NOS;
        abre('node/pve', 'resumo');
      }, exige: () => {
        if (!visivel(document.querySelector('section'))) return { erro: 'a seção Proxmox não ficou visível' };
        return { nota: 'seção visível, nó pve aberto' };
      } },

    { nome: 'Charts with NO series — the state the screen ALWAYS opens in', passo: () => {
        C().pvx.aba = 'graficos';
      }, exige: () => {
        if (svgs().length) return { erro: 'sem série não deveria desenhar SVG' };
        const av = Array.from(document.querySelectorAll('div')).filter((d) => !d.children.length && d.textContent.trim() === 'no sample in this window');
        const v = av.filter(visivel);
        if (v.length !== 10) return { erro: 'esperava 10 avisos "sem amostra" VISÍVEIS, achei ' + v.length + ' of ' + av.length + ' no DOM' };
        return { nota: '10 avisos "sem amostra" — ausência declarada, não zero' };
      } },

    { nome: 'empty series (what a network error leaves behind)', passo: () => {
        poeSerie({ pontos: [], escopo: '', janela: 'hour' });
      }, exige: () => (svgs().length ? { erro: 'pontos:[] não deveria desenhar' } : { nota: 'nada desenhado, como manda' }) },

    { nome: 'continuous series on the hypervisor — 10 metrics', passo: () => {
        poeSerie(serie(60));
      }, exige: exigeGraficos(10, (p) => {
        if (p.some((x) => x.traco.length < 20)) return { erro: 'traço VAZIO em algum gráfico — não errou, mas também não desenhou' };
        if (p.some((x) => subs(x.traco) !== 1)) return { erro: 'série contínua tem de ser UM subcaminho: ' + p.map((x) => subs(x.traco)).join(',') };
        if (p.some((x) => x.area.length < 20)) return { erro: 'área vazia' };
        return { nota: '10 gráficos · traço contínuo, área, grade e ponto final' };
      }) },

    { nome: 'series with 3 gaps — the line has to BREAK', passo: () => {
        poeSerie(serie(60, { buracoEm: [10, 11, 12, 30] }));
      }, exige: exigeGraficos(10, (p) => {
        const n = p.map((x) => subs(x.traco));
        if (n.some((k) => k !== 3)) return { erro: 'buraco não quebrou o traço: subcaminhos = ' + n.join(',') + ' (esperado 3)' };
        return { nota: 'traço quebrado em 3 subcaminhos' };
      }) },

    { nome: 'ISOLATED samples become dots', passo: () => {
        poeSerie(serie(5, { buracoEm: [1, 3] }));
      }, exige: exigeGraficos(10, (p) => {
        const n = p.map((x) => subs(x.pontos));
        if (n.some((k) => k !== 3)) return { erro: 'amostras isoladas não viraram pontos: ' + n.join(',') };
        return { nota: '3 amostras isoladas desenhadas' };
      }) },

    { nome: 'a series of just ONE point', passo: () => { poeSerie(serie(1)); },
      exige: exigeGraficos(10) },

    { nome: 'points WITHOUT the metrics (all null)', passo: () => {
        poeSerie({ pontos: [{ time: 1787000000 }, { time: 1787000060 }], escopo: 'x', janela: 'hour' });
      }, exige: () => (svgs().length ? { erro: 'métrica ausente não deveria desenhar' } : { nota: 'nada desenhado' }) },

    { nome: 'guest opened — switches to the 7 guest metrics', passo: () => {
        // The stub goes in BEFORE opening: `pvxVaiPara('graficos')` fires the load,
        // and it has to find the right series already there.
        poeSerie(serie(30, { escopo: 'lxc/204' }));
        abre('lxc/204', 'graficos');
      }, exige: exigeGraficos(7, (p) => {
        if (p.some((x) => !x.traco)) return { erro: 'guest com traço vazio' };
        return { nota: '7 métricas de guest desenhadas' };
      }) },

    { nome: 'STOPPED guest with the charts open', passo: () => { abre('lxc/205', 'graficos'); },
      exige: exigeGraficos(7) },

    { nome: 'CLEARS the selection with Charts open (yesterday’s crash)', passo: () => {
        C().pvx.aberto = ''; C().pvx.detalhe = null;
      }, exige: () => (svgs().length ? { erro: 'sem nó aberto ainda há ' + svgs().length + ' gráfico(s) visível(is)' } : { nota: 'painel fechou limpo' }) },

    { nome: 'window change', passo: () => { abre('node/pve', 'graficos'); C().pvx.janela = 'day'; },
      exige: exigeGraficos(10) },

    { nome: 'series goes back to NULL (what a badly written catch would do)', passo: () => { poeSerie(null); },
      exige: () => (svgs().length ? { erro: 'serie null não deveria desenhar' } : { nota: 'nada desenhado, sem estouro' }) },
  ];

  // ── the BUTTONS: existing in the HTML is not being on screen ────────────
  const botoes = () => Array.from(document.querySelectorAll('button')).filter(visivel)
    .map((b) => (b.textContent || '').replace(/\s+/g, ' ').trim()).filter((t) => t);

  // 🔴 O BOTÃO SE IDENTIFICA PELA AÇÃO, NUNCA PELO RÓTULO.
  //
  // A primeira versão procurava por texto e me deu DOIS falsos alarmes: a tela
  // tem dois conjuntos de botões com as mesmas palavras — os de ação em MASSA
  // (desabilitados quando nada está selecionado, que é o estado normal) e os do
  // painel do nó aberto. Procurar "Turn on" achava o de massa, desabilitado, e eu
  // quase relatei que o painel inteiro estava morto.
  //
  // `@click` é a identidade: diz QUAL função aquele botão dispara. De quebra,
  // renomear o rótulo deixa de quebrar o pino.
  const porAcao = (trecho) => Array.from(document.querySelectorAll('button'))
    .filter((b) => (b.getAttribute('@click') || '').includes(trecho))
    .filter(visivel)
    .map((b) => ({ t: (b.textContent || '').replace(/\s+/g, ' ').trim(), off: b.disabled || b.getAttribute('aria-disabled') === 'true' }))[0];
  const NO = {
    ligar:   () => porAcao("pvxPower(pvxNoAberto(),'start')"),
    desligar:() => porAcao("pvxPower(pvxNoAberto(),'shutdown')"),
    cortar:  () => porAcao("pvxPower(pvxNoAberto(),'stop')"),
    console: () => porAcao('pvxAbreConsole(pvxNoAberto()'),
    revogar: () => porAcao('pvxRevoga(pvxNoAberto())'),
  };
  const retrato = () => Object.entries(NO).map(([k, f]) => {
    const b = f(); return k + '=' + (b ? (b.off ? 'travado' : 'LIBERADO') : 'ausente');
  }).join(' ');

  window.__roteiro.push(
    { nome: 'BUTTONS · guest RUNNING — what is impossible stays locked', passo: () => {
        abre('lxc/204', 'resumo');
        C().pvx.saude = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, exige: () => {
        const l = NO.ligar(), d = NO.desligar(), c = NO.cortar(), co = NO.console();
        if (!l || !d || !c || !co) return { erro: 'faltam ações no painel do nó: ' + retrato() };
        if (!l.off) return { erro: 'guest LIGADO oferece "ligar" — ordem inútil que polui a trilha: ' + retrato() };
        if (d.off || c.off) return { erro: 'guest ligado NÃO consegue desligar: ' + retrato() };
        if (co.off) return { erro: 'guest ligado sem console: ' + retrato() };
        return { nota: retrato() };
      } },
    { nome: 'BUTTONS · guest STOPPED — the exact mirror', passo: () => { abre('lxc/205', 'resumo'); },
      exige: () => {
        const l = NO.ligar(), d = NO.desligar(), c = NO.cortar();
        if (!l) return { erro: 'guest parado sem "ligar": ' + retrato() };
        if (l.off) return { erro: 'guest PARADO não consegue ligar: ' + retrato() };
        if ((d && !d.off) || (c && !c.off)) return { erro: 'guest PARADO oferece desligar/cortar: ' + retrato() };
        return { nota: retrato() };
      } },
    { nome: 'BUTTONS · guest with NO credential — locked AND with the reason on screen', passo: () => { abre('lxc/202', 'resumo'); },
      exige: () => {
        const l = NO.ligar(), c = NO.cortar();
        if (l && !l.off) return { erro: 'no credential e "ligar" liberado: ' + retrato() };
        if (c && !c.off) return { erro: 'no credential e "cortar" liberado: ' + retrato() };
        const t = document.body.innerText.toLowerCase();
        if (t.indexOf('cofre') < 0 && t.indexOf('credential') < 0)
          return { erro: 'ações travadas e a tela NÃO diz por quê — botão morto sem explicação parece defeito' };
        return { nota: retrato() + ' · motivo escrito na tela' };
      } },
    { nome: 'BUTTONS · hypervisor — the machine’s power exists and is enabled', passo: () => { abre('node/pve', 'resumo'); },
      exige: () => {
        const reb = porAcao("pvxEnergiaHost('reboot')"), des = porAcao("pvxEnergiaHost('shutdown')");
        if (!reb || !des) return { erro: 'hipervisor sem reiniciar/desligar' };
        if (reb.off || des.off) return { erro: 'energia do hipervisor travada' };
        // On the hypervisor, powering a GUEST on/off makes no sense and has to be locked.
        const l = NO.ligar();
        if (l && !l.off) return { erro: 'o hipervisor oferece "ligar guest": ' + retrato() };
        return { nota: 'reiniciar e desligar liberados; energia de guest travada' };
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
  window.__roteiro.push({
    nome: ':disabled · every expression returns a strict boolean',
    passo: () => { abre('lxc/204', 'resumo'); },
    exige: () => {
      const comp = C();
      const alvos = Array.from(document.querySelectorAll('[\\:disabled]'));
      if (alvos.length < 5) return { erro: 'só ' + alvos.length + ' elementos com :disabled — vacuidade' };
      const maus = [];
      for (const el of alvos) {
        const expr = el.getAttribute(':disabled');
        let v;
        try { v = Function('c', 'with (c) { return (' + expr + ') }')(comp); }
        catch (e) { maus.push(expr + ' → estourou: ' + e.message); continue; }
        if (typeof v !== 'boolean') maus.push(expr + ' → ' + JSON.stringify(v) + ' (' + typeof v + '), não booleano');
      }
      if (maus.length) return { erro: maus.length + ' expressão(ões) não booleana(s): ' + maus.join(' ;; ') };
      return { nota: alvos.length + ' expressões :disabled, todas booleanas' };
    },
  });

  // ══ THE NOTE: the Summary has to SUMMARISE ══════════════════════════════
  const notaVisivel = () => Array.from(document.querySelectorAll('.pvx-md')).filter(visivel)[0] || null;

  window.__roteiro.push(
    { nome: 'SUMMARY · the node’s explanation shows up, rendered', passo: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '' };
        abre('lxc/204', 'resumo');
        C().pvx.saude = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, exige: () => {
        const el = notaVisivel();
        if (!el) return { erro: 'a nota do nó NÃO está visível no Resumo — o resumo não resume' };
        const t = el.innerText;
        if (t.indexOf('O que faz') < 0) return { erro: 'o corpo da nota não chegou: ' + t.slice(0, 80) };
        // Rendered, not dumped as raw text.
        // Any level will do: the level depends on how many `#` the note uses, and
        // pinning h3 would be pinning the prose of whoever wrote the note.
        if (!el.querySelector('h3,h4,h5,h6')) return { erro: 'o título da nota não virou heading — Markdown não foi renderizado' };
        if (!el.querySelector('strong')) return { erro: 'negrito não renderizou' };
        if (!el.querySelector('code')) return { erro: 'código não renderizou' };
        if (!el.querySelector('ul li')) return { erro: 'lista não renderizou' };
        if (!el.querySelector('hr')) return { erro: 'régua não renderizou' };
        if (t.indexOf('**') >= 0 || t.indexOf('##') >= 0)
          return { erro: 'marcação de Markdown vazando como texto: ' + t.slice(0, 90) };
        return { nota: el.querySelectorAll('h3,h4,h5,h6,strong,code,li,hr').length + ' elementos renderizados' };
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
    { nome: 'SUMMARY · the explanation comes LAST', passo: () => {}, exige: () => {
        const el = notaVisivel();
        if (!el) return { erro: 'sem nota' };
        const consumo = Array.from(document.querySelectorAll('div')).filter(visivel)
          .find(d => !d.children.length && d.textContent.trim() === 'Usage');
        if (!consumo) return { erro: 'não achei o bloco Consumo para comparar' };
        const depois = consumo.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING;
        if (!depois) return { erro: 'a explicação voltou para ANTES dos números' };
        // And it is the LAST thing in the panel: nothing visible about the node comes after it.
        const painel = el.closest('[x-show]') && el.closest('div[class*="rounded"]');
        const acoes = Array.from(document.querySelectorAll('button')).filter(visivel)
          .find(b => (b.getAttribute('@click') || '').indexOf("pvxPower(pvxNoAberto(),'start')") >= 0);
        if (acoes && !(acoes.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)) {
          return { erro: 'a explicação vem antes das AÇÕES — ela tem de ser a última coisa da tela' };
        }
        return { nota: 'números, ações e depois a explicação' };
      } },

    { nome: 'SUMMARY · a node with NO note says where one is written', passo: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '' };
        abre('lxc/205', 'resumo');
      }, exige: () => {
        if (notaVisivel()) return { erro: 'nota vazia renderizando bloco de conteúdo' };
        const t = document.body.innerText;
        if (t.indexOf('Notes') < 0) return { erro: 'não diz DE ONDE a nota vem, então o operador não sabe onde escrever' };
        return { nota: 'estado vazio explicado' };
      } },

    { nome: 'SUMMARY · a node outside the PVE explains why there is no note', passo: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '' };
        abre('canario', 'resumo');
      }, exige: () => {
        const t = document.body.innerText;
        if (t.indexOf('não é guest deste hipervisor') < 0)
          return { erro: 'nó externo sem explicação — vazio e "não se aplica" viram a mesma tela' };
        return { nota: 'ausência de FONTE distinguida de ausência de conteúdo' };
      } },

    { nome: 'SUMMARY · the HYPERVISOR has a note too', passo: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '' };
        abre('node/pve', 'resumo');
      }, exige: () => {
        const el = notaVisivel();
        if (!el) return { erro: 'o hipervisor abriu sem explicação nenhuma' };
        if (el.innerText.indexOf('máquina física') < 0) return { erro: 'nota do host não chegou' };
        return { nota: 'a nota do host vem de /nodes/<node>/config' };
      } },

    { nome: 'SUMMARY · hostile text in the note does not become a tag', passo: () => {
        C().pvx.nota = { node: 'lxc/204', markdown: '# t\n\n<img src=x onerror=alert(1)>\n\n[x](javascript:alert(1))',
                         origem: 'pve-notes', motivo: '', carregando: false, erro: '' };
        abre('lxc/204', 'resumo');
        C().pvx.nota.node = 'lxc/204';
      }, exige: () => {
        const el = notaVisivel();
        if (!el) return { erro: 'sem nota' };
        if (el.querySelector('img, script, svg, iframe')) return { erro: '🔴 o texto da nota virou TAG no DOM' };
        const comEvento = Array.from(el.querySelectorAll('*')).filter(x =>
          Array.from(x.attributes).some(a => /^on/i.test(a.name)));
        if (comEvento.length) return { erro: '🔴 manipulador de evento no DOM' };
        const links = Array.from(el.querySelectorAll('a'));
        if (links.some(a => /^(javascript|data|vbscript):/i.test(a.getAttribute('href') || '')))
          return { erro: '🔴 link com esquema executável' };
        if (el.innerText.indexOf('onerror') < 0)
          return { erro: 'o texto sumiu — filtro que APAGA esconde a nota do operador' };
        return { nota: 'texto hostil exibido como letra, sem virar DOM' };
      } },
  );

  // ══ 🔴 "RUNNING" DEMANDS AN OBSERVATION FROM NOW ════════════════════════
  //
  // With the hypervisor unreachable, the panel said "9 / 10 running" about a
  // house it had not seen for nearly an hour. Counting zero would be the opposite
  // lie. The only truthful answer is "I do not know".
  window.__roteiro.push(
    { nome: 'RUNNING · fresh data counts normally', passo: () => {
        C().pvxLimpaSelecao();
      }, exige: () => {
        const r = C().pvxResumoDoLab();
        // The fixture has ONE guest absent on purpose (lxc/101). The others are
        // fresh and have to be counted — demanding "0 unknown" would be demanding
        // that the fixture not reproduce the real case.
        if (r.ligados < 2) return { erro: 'guests frescos não foram contados: ' + JSON.stringify(r) };
        // Two absentees in the fixture: one stale and one FRESH. The fresh one is
        // what proves the absence guard does work of its own.
        if (r.desconhecidos !== 2) return { erro: 'esperava 2 unknowns (os dois ausentes): ' + JSON.stringify(r) };
        return { nota: r.ligados + '/' + r.guests + ' ligados, ' + r.desconhecidos + ' unknown' };
      } },

    { nome: 'RUNNING · a guest with STALE data is not counted as running', passo: () => {
        // Exactly the state of the house today: the last known value says
        // "running", but the observation has aged.
        C().nodes.list = C().nodes.list.map(n => Object.assign({}, n, { stale: true, age_seconds: 2760 }));
      }, exige: () => {
        const r = C().pvxResumoDoLab();
        if (r.ligados !== 0) return { erro: '🔴 contou ' + r.ligados + ' ligados sobre dado que ninguém observou' };
        if (r.desconhecidos !== r.guests) return { erro: 'unknowns = ' + r.desconhecidos + ', quer ' + r.guests };
        const t = document.body.innerText;
        if (t.indexOf('no fresh observation') < 0) return { erro: 'a tela não declara que não consegue ver' };
        if (t.indexOf('0 / ') >= 0) return { erro: '🔴 mostrou "0 / N" — afirma que estão todos desligados, que ninguém observou' };
        return { nota: 'travessão em vez de número, e o motivo escrito' };
      } },

    { nome: 'RUNNING · a node that LEFT the hypervisor does not count as running either', passo: () => {
        C().nodes.list = NOS.map(n => n.id === 'lxc/204'
          ? Object.assign({}, n, { ausente_desde: 1787200000 })
          : n);
      }, exige: () => {
        const r = C().pvxResumoDoLab();
        const lab = C().pvxNos().find(x => x.id === 'lxc/204');
        if (!lab || !lab.ausente_desde) return { erro: 'o fixture não marcou o nó como ausente' };
        if (r.desconhecidos < 1) return { erro: 'nó ausente contado como se fosse observável: ' + JSON.stringify(r) };
        // GIVES BACK the original list: a step that dirties the next one is the
        // defect that already broke the console proof this morning.
        C().nodes.list = NOS;
        return { nota: r.ligados + ' ligados, ' + r.desconhecidos + ' unknown(s)' };
      } },
  );

  // ══ THE NODE THAT LEFT THE HYPERVISOR ════════════════════════════════════
  window.__roteiro.push(
    { nome: 'GONE · a node that left the hypervisor is not counted as "expired"', passo: () => {
        abre('node/pve', 'resumo');
      }, exige: () => {
        const c = C();
        const n = c.pvxNos().find(x => x.id === 'lxc/101');
        if (!n) return { erro: 'o fixture perdeu o nó ausente' };
        const e = c.pvxNoEstado(n);
        if (e !== 'sumiu') return { erro: 'estado = ' + e + ', quer "sumiu" — ele tem carimbo de ausência' };
// It is `stale` TOO, and even so it must not fall into "vencido": the
        // order of the checks is what separates "I did not look" from "I looked
        // and did not find".
        if (!n.stale) return { erro: 'o fixture não reproduz o caso real (o nó ausente também é stale)' };
        const segs = {};
        c.pvxSegmentosDaTela().forEach(x => { segs[x.chave] = x.n; });
        if (segs.sumiu !== 2) return { erro: 'a faixa não conta os dois ausentes: ' + JSON.stringify(segs) };
// 🔴 The exact property is the ORDER of the checks: the SAME node, without
        // the absence stamp, would fall into "vencido". Asserting "vencido === 0"
        // on the band would be crude — `canario` is legitimately vencido, and the
        // check would fail over a node that has nothing to do with this.
        const semCarimbo = Object.assign({}, n, { ausente_desde: 0 });
        if (c.pvxNoEstado(semCarimbo) !== 'vencido') {
          return { erro: 'sem o carimbo ele deveria ser "vencido" — o fixture não reproduz a ambiguidade real' };
        }
        return { nota: 'com carimbo = sumiu; sem carimbo = vencido — a ordem é o que separa' };
      } },

    { nome: 'GONE · does not count as an attention item', passo: () => {}, exige: () => {
        const c = C();
        const n = c.pvxNos().find(x => x.id === 'lxc/101');
        // pvxPrecisaAtencao lives in the lab summary’s count; what is asserted
        // here is that the "sumiu" state does not enter the three pending keys.
        const e = c.pvxNoEstado(n);
        if (['vencido', 'sem-credencial', 'critico'].indexOf(e) >= 0) {
          return { erro: 'nó que gone from the hypervisor entrou na lista de pendências como ' + e };
        }
        return { nota: 'fora das pendências, como deve' };
      } },

    { nome: 'GONE · the actions stay locked WITH the right reason', passo: () => {
        abre('lxc/101', 'resumo');
      }, exige: () => {
        const c = C();
        for (const acao of ['start', 'shutdown', 'reboot', 'clone', 'backup']) {
          const est = c.pvxAcaoEstado(c.pvxNoAberto(), acao);
          if (est.pode) return { erro: acao + ' liberado num nó que o hipervisor não lista mais' };
          if (est.motivo.indexOf('no longer lists this node') < 0) {
            return { erro: acao + ': motivo genérico demais — ' + JSON.stringify(est.motivo) };
          }
        }
        if (document.body.innerText.indexOf('hypervisor no longer lists this node') < 0) {
          return { erro: 'a tela não diz que o nó gone from the hypervisor' };
        }
        return { nota: '5 ações travadas, com o motivo escrito na tela' };
      } },
  );

  // 🔴 "I HAVE NOT READ IT YET" MUST NOT BECOME "IT DOES NOT EXIST" ════════
  //
  // The operator saw on screen "backup: no storage on this hypervisor accepts
  // backups" with `pbs` standing right there on the other side. The storage list
  // simply had not been loaded — and the panel asserted something about the
  // HYPERVISOR out of an absence that was its OWN.
  window.__roteiro.push(
    { nome: 'MOTIVO · storage não lido não vira "no storage accepts backups"', passo: () => {
        C().pvx.storage = null;
        abre('lxc/204', 'resumo');
      }, exige: () => {
        const t = document.body.innerText;
        if (t.indexOf('no storage on this hypervisor accepts backups') >= 0) {
          return { erro: '🔴 disse que o hipervisor não tem storage de backup sem ter lido a lista' };
        }
        const est = C().pvxAcaoEstado(C().pvxNoAberto(), 'backup');
        if (est.pode) return { erro: 'liberou backup sem saber para onde' };
        if (est.motivo.indexOf('have not read') < 0) return { erro: 'motivo = ' + JSON.stringify(est.motivo) };
        return { nota: 'motivo honesto: ' + est.motivo };
      } },
    { nome: 'REASON · with the list read and NO target, then it really is "no storage"', passo: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, exige: () => {
        const est = C().pvxAcaoEstado(C().pvxNoAberto(), 'backup');
        if (est.motivo.indexOf('no storage') < 0) return { erro: 'motivo = ' + JSON.stringify(est.motivo) };
        return { nota: 'agora a afirmação é sobre o hipervisor, e é verdadeira' };
      } },
    { nome: 'REASON · opening Copies loads the list by itself', passo: () => {
        C().pvx.storage = null;
        abre('lxc/204', 'snaps');
      }, exige: () => {
        if (!C().pvx.storage) return { erro: 'a aba Cópias abriu sem a lista de storages — o botão diria mentira' };
        const est = C().pvxAcaoEstado(C().pvxNoAberto(), 'backup');
        if (!est.pode) return { erro: 'lista carregada e o backup continua travado: ' + est.motivo };
        return { nota: 'lista carregada ao abrir a aba, botão liberado' };
      } },
  );

  // ══ EDITING THE NOTE WITHOUT LEAVING THE PANEL ═══════════════════════════
  const botaoEditarNota = () => Array.from(document.querySelectorAll('button')).filter(visivel)
    .find(b => (b.getAttribute('@click') || '') === 'pvxEditaNota()');
  const areaDaNota = () => Array.from(document.querySelectorAll('textarea')).filter(visivel)
    .find(t => (t.getAttribute('aria-label') || '').indexOf('note') >= 0);

  window.__roteiro.push(
    { nome: 'NOTE · the note loads and the edit button appears', passo: () => {
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '',
                         editando: false, rascunho: '', salvando: false };
        abre('lxc/204', 'resumo');
        // 🔴 THE MUTATION LIVES IN `passo`, NEVER IN `exige`. The harness waits for
        // Alpine to re-render AFTER the step; changing state inside the
        // measurement is measuring the DOM from before the change — which is what
        // made this check say "edit mode did not open" about a screen that opens.
      }, exige: () => {
        const b = botaoEditarNota();
        if (!b) return { erro: 'não há botão de editar a nota — o fluxo continua fora do painel' };
        if (b.off) return { erro: 'botão de editar travado num nó com nota' };
        return { nota: 'nota carregada e botão de editar disponível' };
      } },

    { nome: 'NOTE · editing during the load does NOT open an empty draft', passo: () => {
        // Saving an empty draft WOULD ERASE the hypervisor’s note. The guard lives
        // in the function, not only in the markup.
        C().pvx.nota.carregando = true;
        C().pvxEditaNota();
      }, exige: () => {
        if (C().pvx.nota.editando) return { erro: '🔴 abriu o rascunho com a nota ainda carregando — salvar apagaria a descrição' };
        C().pvx.nota.carregando = false;
        return { nota: 'recusou abrir, como deve' };
      } },

    { nome: 'NOTE · editing opens the draft with the text IN FORCE', passo: () => {
        C().pvxEditaNota();
      }, exige: () => {
        const ta = areaDaNota();
        if (!ta) return { erro: 'o modo de edição não abriu uma área de texto' };
        if (ta.value.indexOf('O que faz') < 0) return { erro: 'o rascunho não veio com o texto em vigor: ' + ta.value.slice(0, 60) };
        // 🔴 While editing, the rendered body disappears: seeing both at once would
        // make the operator confuse what is saved with what he typed.
        if (notaVisivel()) return { erro: 'o texto renderizado continua na tela durante a edição' };
        return { nota: 'rascunho aberto com ' + ta.value.length + ' caracteres, corpo renderizado escondido' };
      } },

    { nome: 'NOTE · cancelling gives the original back INTACT', passo: () => {
        window.__notaAntes = C().pvx.nota.markdown;
        C().pvx.nota.rascunho = 'joguei tudo fora';
        C().pvxCancelaNota();
      }, exige: () => {
        if (C().pvx.nota.markdown !== window.__notaAntes) return { erro: 'cancelar alterou o texto em vigor' };
        if (C().pvx.nota.editando) return { erro: 'continuou em modo de edição' };
        const el = notaVisivel();
        if (!el || el.innerText.indexOf('O que faz') < 0) return { erro: 'o texto original não voltou à tela' };
        return { nota: 'original intacto, edição fechada' };
      } },

    { nome: 'NOTE · text above the ceiling locks the save, and the screen SAYS SO', passo: () => {
        C().pvxEditaNota();
        C().pvx.nota.rascunho = 'a'.repeat(C().NOTA_MAX + 1);
      }, exige: () => {
        const salvar = Array.from(document.querySelectorAll('button')).filter(visivel)
          .find(b => (b.getAttribute('@click') || '') === 'pvxSalvaNota()');
        if (!salvar) return { erro: 'sem botão de salvar' };
        if (!salvar.disabled) return { erro: 'acima do teto e o salvar continua liberado' };
        if (document.body.innerText.indexOf('over the cap') < 0)
          return { erro: 'travado sem dizer por quê' };
        return { nota: 'salvar travado com o motivo na tela' };
      } },

    { nome: 'NOTE · a node outside the PVE offers no editing', passo: () => {
        C().pvxCancelaNota();
        C().pvx.nota = { node: '', markdown: '', origem: '', motivo: '', carregando: false, erro: '',
                         editando: false, rascunho: '', salvando: false };
        abre('canario', 'resumo');
      }, exige: () => {
        if (botaoEditarNota()) return { erro: 'ofereceu editar a nota de um nó que não tem nota no PVE' };
        return { nota: 'sem botão, porque não há o que editar' };
      } },
  );

  // ══ MAINTENANCE: restart, clone and keep a copy ══════════════════════════
  const STORAGES = { pools: [
    { id: 'local', type: 'dir', content: ['backup', 'iso', 'vztmpl'], free: 8e11, total: 9e11, used: 1e10, used_pct: 1 },
    { id: 'local-zfs', type: 'zfspool', content: ['images', 'rootdir'], free: 8e11, total: 9e11, used: 6e10, used_pct: 7 },
    { id: 'pbs', type: 'pbs', content: ['backup'], free: 8.5e11, total: 9e11, used: 4.4e10, used_pct: 5 },
  ] };

  window.__roteiro.push(
    { nome: 'RESTART · running enables, stopped locks, and the reason shows up', passo: () => {
        abre('lxc/204', 'resumo');
        C().pvx.saude = { node: 'pve', version: { value: 'pve-manager/9.2.1', observed_at: 1787256260 } };
      }, exige: () => {
        const reb = porAcao("pvxPower(pvxNoAberto(),'reboot')");
        if (!reb) return { erro: 'não existe botão de reiniciar no painel do nó' };
        if (reb.off) return { erro: 'guest LIGADO não consegue reiniciar: ' + retrato() };
        return { nota: 'reiniciar liberado no guest ligado' };
      } },
    { nome: 'RESTART · a stopped guest offers no restart', passo: () => { abre('lxc/205', 'resumo'); },
      exige: () => {
        const reb = porAcao("pvxPower(pvxNoAberto(),'reboot')");
        if (!reb) return { erro: 'botão sumiu — o desenho desta tela DESABILITA com motivo, não some' };
        if (!reb.off) return { erro: 'guest PARADO oferece reiniciar — o PVE devolveria erro e a trilha ganharia ruído' };
        const t = document.body.innerText;
        if (t.indexOf('it is powered off') < 0) return { erro: 'travado sem o motivo escrito na tela' };
        return { nota: 'reiniciar travado, com o motivo por extenso' };
      } },

    { nome: 'COPIES · the tab exists and clone/backup live in it', passo: () => {
        C().pvx.storage = STORAGES;
        abre('lxc/204', 'snaps');
        C().pvx.snaps = [];
      }, exige: () => {
        const cl = porAcao('pvxAbreClone(pvxNoAberto())');
        const bk = porAcao('pvxBackupConfirma(pvxNoAberto())');
        if (!cl) return { erro: 'não há botão de clonar na aba Cópias' };
        if (!bk) return { erro: 'não há botão de guardar cópia na aba Cópias' };
        if (cl.off) return { erro: 'clonar travado num guest normal' };
        if (bk.off) return { erro: 'guardar cópia travado mesmo com storage que aceita backup' };
        const abas = Array.from(document.querySelectorAll('.pvx-tab')).filter(visivel).map(b => b.textContent.trim());
        if (abas.indexOf('Copies') < 0) return { erro: 'a aba não se chama Cópias: ' + abas.join(' | ') };
        return { nota: 'aba Cópias com clonar e guardar cópia liberados' };
      } },

    { nome: 'COPIES · the backup target is DERIVED, not typed', passo: () => {}, exige: () => {
        const sel = Array.from(document.querySelectorAll('select')).filter(visivel);
        const dest = sel.find(x => (x.getAttribute('aria-label') || '') === 'destination storage');
        if (!dest) return { erro: 'não há seletor de storage' };
        const ops = Array.from(dest.options).map(o => o.value);
        // Only `local` and `pbs` declare `backup` content; `local-zfs` does not.
        if (ops.indexOf('local-zfs') >= 0) return { erro: 'ofereceu storage que NÃO aceita backup: ' + ops.join(',') };
        if (ops.indexOf('pbs') < 0 || ops.indexOf('local') < 0) return { erro: 'faltam destinos válidos: ' + ops.join(',') };
        return { nota: 'destinos = ' + ops.join(', ') + ' (local-zfs corretamente fora)' };
      } },

    { nome: 'COPIES · with no backup storage, the button locks WITH a reason', passo: () => {
        C().pvx.storage = { pools: [{ id: 'local-zfs', type: 'zfspool', content: ['images'], free: 1, total: 2, used: 1, used_pct: 50 }] };
      }, exige: () => {
        const bk = porAcao('pvxBackupConfirma(pvxNoAberto())');
        if (!bk) return { erro: 'botão sumiu' };
        if (!bk.off) return { erro: 'sem destino possível e o botão continua liberado' };
        if (document.body.innerText.indexOf('no storage') < 0) return { erro: 'travado sem dizer por quê' };
        return { nota: 'travado e explicado' };
      } },

    { nome: 'CLONE · the dialog reads the id from the hypervisor, nobody types it', passo: () => {
        C().pvx.storage = STORAGES;
        abre('lxc/204', 'snaps');
        C().pvxAbreClone(C().pvxNoAberto());
      }, exige: () => {
        if (!C().pvx.clone.aberto) return { erro: 'o diálogo não abriu' };
        if (C().pvx.clone.novoID !== 991) return { erro: 'novoID = ' + C().pvx.clone.novoID + ', quer 991 (veio do hipervisor)' };
        if (C().pvx.clone.nome !== 'lab-copia') return { erro: 'sugestão de nome = ' + C().pvx.clone.nome };
        // The id must NOT be a typeable field: it is a reading.
        const inputs = Array.from(document.querySelectorAll('input')).filter(visivel);
        const digitavelComID = inputs.filter(i => String(i.value) === '991');
        if (digitavelComID.length) return { erro: 'o id de destino está num campo DIGITÁVEL — ele é lido do hipervisor' };
        if (!document.body.innerText.includes('991')) return { erro: 'o id lido não aparece na tela' };
        if (document.body.innerText.indexOf('crash-consistent') < 0)
          return { erro: 'guest ligado e nenhum aviso de cópia consistente-de-queda' };
        // 🔴 A running CT requires a source snapshot, and the screen has to OFFER
        // the list instead of letting the operator take the hypervisor’s error.
        const sel = Array.from(document.querySelectorAll('select')).filter(visivel)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (!sel) return { erro: 'CT ligado sem seletor de snapshot de origem' };
        const ops = Array.from(sel.options).map(o => o.value);
        if (ops.join(',') !== 'antes-do-upgrade,base') return { erro: 'snapshots oferecidos: ' + ops.join(',') };
        const btn = Array.from(document.querySelectorAll('button')).filter(visivel)
          .find(b => (b.getAttribute('@click') || '') === 'pvxClonaConfirma()');
        if (!btn || btn.disabled) return { erro: 'com snapshot escolhido, "Clone now" continua travado' };
        return { nota: 'id 991 do hipervisor, nome sugerido, 2 snapshots oferecidos, botão liberado' };
      } },

    { nome: 'CLONE · a running CT with NO snapshot locks and says what to do', passo: () => {
        C().pvxFechaClone();
        abre('lxc/202', 'snaps');
        C().pvxAbreClone(C().pvxNoAberto());
      }, exige: () => {
        const btn = Array.from(document.querySelectorAll('button')).filter(visivel)
          .find(b => (b.getAttribute('@click') || '') === 'pvxClonaConfirma()');
        if (!btn) return { erro: 'sem botão de clonar' };
        if (!btn.disabled) return { erro: 'CT ligado sem snapshot com "Clone now" LIBERADO — o hipervisor recusaria' };
        const t = document.body.innerText;
        if (t.indexOf('create one in the section above') < 0 && t.indexOf('has no snapshot') < 0)
          return { erro: 'travado sem dizer o que fazer' };
        return { nota: 'travado, com a saída escrita na tela' };
      } },

    { nome: 'CLONE · a stopped guest does NOT get the downtime warning', passo: () => {
        C().pvxFechaClone();
        abre('lxc/205', 'snaps');
        C().pvxAbreClone(C().pvxNoAberto());
      }, exige: () => {
        if (C().pvx.clone.ligado) return { erro: 'guest parado marcado como ligado' };
        const av = Array.from(document.querySelectorAll('div')).filter(d => !d.children.length && d.textContent.indexOf('crash-consistent') >= 0).filter(visivel);
        if (av.length) return { erro: 'guest DESLIGADO recebendo aviso que só vale para ligado — ruído treina a ignorar' };
        const sel = Array.from(document.querySelectorAll('select')).filter(visivel)
          .find(x => (x.getAttribute('aria-label') || '').indexOf('snapshot') >= 0);
        if (sel) return { erro: 'guest desligado sendo obrigado a escolher snapshot — exigência que o hipervisor não faz' };
        return { nota: 'sem aviso e sem exigência de snapshot, porque não há o que exigir' };
      } },

    // 🔴 THE CLASS CHECK OF THIS BATCH: every button has to LOOK like a button.
    { nome: 'STYLE · no button renders as loose text', passo: () => {
        C().pvxFechaClone();
        abre('lxc/204', 'resumo');
      }, exige: () => {
        const invisiveis = [];
        for (const b of Array.from(document.querySelectorAll('button')).filter(visivel)) {
          const cs = getComputedStyle(b);
          const temFundo = cs.backgroundColor && cs.backgroundColor !== 'rgba(0, 0, 0, 0)' && cs.backgroundColor !== 'transparent';
          const temBorda = parseFloat(cs.borderTopWidth) > 0 &&
            cs.borderTopColor !== 'rgba(0, 0, 0, 0)' && cs.borderTopColor !== 'transparent';
          if (!temFundo && !temBorda) {
            invisiveis.push((b.textContent || '').replace(/\s+/g, ' ').trim().slice(0, 28) + ' [' + b.className + ']');
          }
        }
        if (invisiveis.length) {
          return { erro: invisiveis.length + ' botão(ões) sem fundo NEM borda — renderizam como texto solto: ' + invisiveis.join(' ;; ') };
        }
        return { nota: 'todos os botões visíveis têm fundo ou borda própria' };
      } },
  );

  // ── sweeps ALL the host and guest tabs, pristine and with data ───────────
  const ABAS_HOST = ['resumo', 'graficos', 'console', 'tarefas', 'discos', 'storage', 'zfs', 'rede', 'sistema', 'pacotes', 'registro', 'perms'];
  const ABAS_GUEST = ['resumo', 'graficos', 'console', 'tarefas', 'perms'];
  for (const a of ABAS_HOST) {
    window.__roteiro.push({ nome: 'host · tab ' + a + ' (pristine state)', passo: ((ab) => () => {
      poeSerie(null); C().pvx.storage = null; C().pvx.zfs = null; C().pvx.perms = null;
      C().pvx.saude = null; C().pvx.detalhe = null;
      abre('node/pve', ab);
    })(a) });
  }
  for (const a of ABAS_GUEST) {
    window.__roteiro.push({ nome: 'guest · tab ' + a + ' (pristine state)', passo: ((ab) => () => abre('lxc/204', ab))(a) });
  }
})();
