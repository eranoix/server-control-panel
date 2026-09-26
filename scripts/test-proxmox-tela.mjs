import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

// The repo root from THIS file: the pin runs both under `node scripts/...` and
// under `go test ./internal/webassets/`, whose cwd is the package.
const RAIZ = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const ler = (rel) => fs.readFileSync(path.join(RAIZ, rel), 'utf8');
const src = ler('internal/webassets/web/vendor/vpsm/app/41-proxmox.js');
const win = {};
new Function('window','document','location','setInterval','clearInterval', src)(
  win, {hidden:false,getElementById:()=>null}, {protocol:'http:',host:'x'}, ()=>1, ()=>0);
const mod = win.VPSMProxmoxModule();
const obs = (v, t=1) => ({value:v, observed_at:t});
const comp = Object.assign({
  nodes:{ list:[
    {id:'node/pve', kind:'host',  name:'hypervisor-01', status:obs('online')},
    {id:'lxc/201',  kind:'guest', vmid:201, name:'games', status:obs('running'),
     mem_used:obs(2.87e9), mem_total:obs(17.18e9), cpu_frac:obs(0.0815)},
    {id:'qemu/208', kind:'guest', vmid:208, name:'dev', status:obs('running'),
     mem_used:obs(7.16e9), mem_total:obs(8.59e9), cpu_frac:obs(0.0035)},
    {id:'lxc/204',  kind:'guest', vmid:204, name:'lab', status:obs('stopped'),
     mem_used:{value:0,observed_at:0}, mem_total:{value:0,observed_at:0}},  // NO DATA
  ], poll:{age_seconds:12}},
  api:async()=>({ok:true,status:200,json:async()=>({})}), showToast(){}, askConfirm(){},
  _apiError(){}, _errText(e){return String(e)}, $nextTick(){}, loadNodes(){},
}, mod);

let mau = 0;
const ok = (nome, cond, extra='') => { console.log((cond?'  ✓ ':'  ✗ ')+nome+(extra?'  → '+extra:'')); if(!cond) mau++; };

// ── contextual tabs ───────────────────────────────────────────────────────
const host  = comp.nodes.list[0], guest = comp.nodes.list[2];
// 🔴 This pin locked the EXACT LIST of tabs and failed on its own when the screen
// reached parity with Proxmox — with nothing broken. An exact list is fragile for
// the same reason a position was: any legitimate growth knocks it over. What
// needs a guard are the PROPERTIES.
const idsHost = comp.pvxAbasDoNo(host).map((a) => a.id);
const idsGuest = comp.pvxAbasDoNo(guest).map((a) => a.id);

ok('both lists open on the Summary', idsHost[0] === 'resumo' && idsGuest[0] === 'resumo');
ok('the host has the infrastructure tabs',
   ['discos', 'storage', 'zfs'].every((x) => idsHost.includes(x)), idsHost.join(','));
ok('the guest has console and snapshots',
   ['console', 'snaps'].every((x) => idsGuest.includes(x)), idsGuest.join(','));
ok('no infrastructure tab leaks into the guest',
   !['discos', 'storage', 'zfs', 'rede', 'sistema', 'pacotes', 'registro', 'perms']
     .some((x) => idsGuest.includes(x)), idsGuest.join(','));
// 🔴 `console` LEFT this list: the host gained a Shell once the operator granted
// Sys.Console, and the pin — correctly — failed over the change. `snaps` stays:
// a snapshot belongs to a guest, and a "hypervisor snapshot" does not exist as a
// concept in PVE.
ok('snapshots do not leak to the host (they do not exist for it)',
   !idsHost.includes('snaps'), idsHost.join(','));
ok('the host has a Shell', idsHost.includes('console'));
ok('and the guest has a console too', idsGuest.includes('console'));
ok('no repeated id in either list',
   new Set(idsHost).size === idsHost.length && new Set(idsGuest).size === idsGuest.length);
ok('no host tab shows up with a guest selected',
   !comp.pvxAbasDoNo(guest).some(a=>['discos','storage','zfs','perms'].includes(a.id)));

// a tab inherited from another type falls back to Summary, not an empty panel
comp.pvx.aberto = 'qemu/208'; comp.pvx.aba = 'zfs';
ok('a "zfs" tab inherited on a guest normalises to "resumo"', comp.pvxAbaAtiva() === 'resumo',
   comp.pvxAbaAtiva());
comp.pvx.aba = 'console';
ok('a valid guest tab is respected', comp.pvxAbaAtiva() === 'console');

// ── lab summary ───────────────────────────────────────────────────────────
comp.pvx.aberto = '';
const r = comp.pvxResumoDoLab();
ok('counts 3 guests, 2 running', r.guests===3 && r.ligados===2, `${r.guests}/${r.ligados}`);
ok('a guest with no timestamp goes into semDado, not in as a zero', r.semDado===1, String(r.semDado));
ok('summed memory IGNORES the one with no data (it does not dilute the average)',
   Math.abs(r.memTotal - (17.18e9+8.59e9)) < 1e6, (r.memTotal/1e9).toFixed(2)+' GB');
ok('the memory percentage matches the sum of the observed ones',
   Math.abs(r.memPct - ((2.87e9+7.16e9)/(17.18e9+8.59e9)*100)) < 0.01, r.memPct.toFixed(1)+'%');

// ── backup freshness ────────────────────────────────────────────────────────
//
// 🔴 This block pinned the OLD world: "no source, and it says why". That was right
// while the panel token could not enumerate the datastore. With full access the
// source exists, and what needs a guard changed.
//
// The property that matters most now PAID FOR ITSELF on the first live reading:
// `pbs` was at 3.6 h with 9 guests while `backupusb` was at 340 h (14 days) with
// 3 guests. A single number would have let the fresh layer MASK the stopped one.
// That is why the pin demands separation per datastore.
{
  // 🔴 THE CONTRACT CHANGED, and it changed because of a real crash: "not loaded"
  // stopped being `null` and became an empty shape plus a flag. The null field was
  // dereferenced by the template before the first load — which is the state the
  // screen ALWAYS opens in — and the throw took down the whole of Alpine, taking
  // the terminal with it.
  ok('with no answer yet, the screen does not pretend it loaded', comp.pvxBackups().carregado === false);
  ok('and the state already has a SHAPE before loading (it is not null)',
     Array.isArray(comp.pvx.backup.datastores) && comp.pvx.backup.datastores.length === 0);

  comp.pvx.backup = {
    observed_at: 1_000_000,
    datastores: [
      { storage: 'pbs', total: 65, ultimo_ctime: 1_000_000 - 3 * 3600, guests: [100, 201, 202, 203, 204, 205, 206, 207, 208], agendamento: 'active' },
      { storage: 'backupusb', total: 5, ultimo_ctime: 1_000_000 - 340 * 3600, guests: [100, 201, 204], agendamento: 'active' },
      { storage: 'local', total: 0, ultimo_ctime: 0, guests: [], agendamento: 'fora-do-pve' },
      { storage: 'quebrado', erro: 'the hypervisor refused' },
    ],
  };
  const itens = comp.pvxBackups().itens;
  ok('each datastore shows up SEPARATELY (merging masks the stopped layer)', itens.length === 4,
     itens.map((d) => d.storage).join(' '));

  const pbs = comp.pvxIdadeDoBackup(itens[0]);
  const usb = comp.pvxIdadeDoBackup(itens[1]);
  ok('the age comes from the SERVER timestamp (observed_at − ultimo_ctime)',
     pbs.seg === 3 * 3600 && usb.seg === 340 * 3600, `pbs=${pbs.seg}s usb=${usb.seg}s`);

  // 🔴 Zero is ABSENCE, not 1970. An empty datastore and one with an ancient copy
  // call for opposite actions.
  const vazio = comp.pvxIdadeDoBackup(itens[2]);
  ok('an empty datastore says "no copy yet", not an age counted from 1970',
     vazio.vazio === true && vazio.seg === undefined);
  ok('a datastore with an error does not become silence', !!comp.pvxIdadeDoBackup(itens[3]).erro);

  // The threshold is PER datastore: PBS runs every day, the external-disk rotation
  // is "whenever I remember to swap the disk". A single threshold would paint a
  // healthy rotation red, or a PBS dead for three days green.
  const verde = (e) => /#22c55e/.test(e);
  const vermelho = (e) => /#ef4444/.test(e);
  ok('PBS at 3 h comes out green', verde(comp.pvxEstiloBackup(itens[0])));
  ok('an empty datastore NEVER comes out green', !verde(comp.pvxEstiloBackup(itens[2])));

  // Negative control for the per-layer threshold.
  const pbsVelho = { storage: 'pbs', total: 1, ultimo_ctime: 1_000_000 - 200 * 3600, guests: [1], agendamento: 'active' };
  ok('PBS at 200 h fails, even though that is a normal age for the rotation',
     !verde(comp.pvxEstiloBackup(pbsVelho)));

  // ── disarmed is NOT a failure ──────────────────────────────────────────────
  //
  // 🔴 This is the fix for a defect of MINE that the operator felt first-hand: I
  // painted `backupusb` red, with no new copy for two weeks, as if a chain had
  // failed. It had not — he turned the schedule off in a dated decision, after PBS
  // was standing, verify-by-content was running and a restore had been rehearsed.
  // Permanent red trains people to ignore, which is the disease that already cost
  // this lab the credibility of its alarm channel.
  const desarmado = { storage: 'backupusb', total: 5, ultimo_ctime: 1_000_000 - 340 * 3600,
                      guests: [1, 2, 3], agendamento: 'desarmado', schedule: '03:30' };
  const eD = comp.pvxEstadoBackup(desarmado);
  ok('a DISARMED layer does not come out red', !vermelho(comp.pvxEstiloBackup(desarmado)), eD.cor);
  ok('and it says the label "desarmado", not an alarming age', eD.rotulo === 'disarmed', eD.rotulo);
  ok('and it explains WHY, with the time it used to run', /schedule turned off/.test(eD.nota) && /03:30/.test(eD.nota), eD.nota);
  ok('and it says outright that this is not a failure', /this is not a failure/.test(eD.nota));

  // ── "outside PVE" is not a failure either ──────────────────────────────────
  //
  // The live run caught this BEFORE the deploy: the `pbs` of this lab receives a
  // copy every day from a systemd timer on the host, invisible to /cluster/backup.
  // A boolean "scheduled" would paint it disarmed — wrong in the other direction.
  const foraDoPve = { storage: 'pbs', total: 65, ultimo_ctime: 1_000_000 - 3 * 3600,
                      guests: [1], agendamento: 'fora-do-pve' };
  const eF = comp.pvxEstadoBackup(foraDoPve);
  ok('a fresh layer with NO job in PVE stays green', verde(comp.pvxEstiloBackup(foraDoPve)), eF.cor);
  ok('and the screen admits it does not know who schedules it', /does not know by whom/.test(eF.nota), eF.nota);

  // A layer that is ACTIVE and old is still a failure — otherwise the fix would
  // have erased the real alarm along with the false one.
  const ativaVelha = { storage: 'pbs', total: 1, ultimo_ctime: 1_000_000 - 400 * 3600,
                       guests: [1], agendamento: 'active', schedule: '03:30' };
  ok('an ACTIVE and old layer stays red (the real alarm did not vanish)',
     vermelho(comp.pvxEstiloBackup(ativaVelha)));
  comp.pvx.backup = { datastores: [] };
  comp.pvx.carregado.backup = false;
}

// ── automatic pause ───────────────────────────────────────────────────────
let carregou = 0; comp.loadNodes = () => { carregou++; };
comp.pvx.con = {guest:'', estado:'closed', erro:''}; comp.pvx.filtroFoco = false;
comp.pvxTick();
ok('no console and no focus: the cycle FETCHES', carregou===1, 'buscas='+carregou);
comp.pvx.con.guest = 'lxc/204'; comp.pvxTick();
ok('an open console PAUSES the fetch', carregou===1 && comp.pvx.pausado, 'buscas='+carregou);
ok('the pause states its reason', comp.pvx.pausaMotivo==='console open', comp.pvx.pausaMotivo);
comp.pvx.con.guest=''; comp.pvx.filtroFoco = true; comp.pvxTick();
ok('focus in the filter pauses too', carregou===1 && comp.pvx.pausaMotivo==='typing in the filter');
// the timestamp keeps running during the pause
comp.nodes.poll.age_seconds = 300; comp.pvxTick();
ok('the age stamp KEEPS running during the pause',
   comp.pvx.idadeSeg===300 && comp.pvxIdadeVelha(), comp.pvxIdadeTexto());
comp.pvx.filtroFoco=false; comp.pvxTick();
ok('out of the pause, fetching resumes', carregou===2 && !comp.pvx.pausado, 'buscas='+carregou);


// ── a visible label on the gauges (the operator could not tell them apart) ────
//
// The complaint was literal: "what is each thing? no idea. nothing is labelled."
// The screen had three stacked bars and the label existed ONLY in the aria-label —
// anyone on a screen reader knew, anyone looking did not. These pins demand the
// VISIBLE label, and that is why they cannot settle for the aria-label.
const index = ler('internal/webassets/web/index.html');
const secao = (() => {
  const i = index.indexOf("currentView==='proxmox'");
  const f = index.indexOf('</section>', index.indexOf('/master-detail grid'));
  return index.slice(i, f);
})();

ok('the Proxmox section was cut out for the pins', secao.length > 5000, secao.length + ' chars');

// the x-text that renders the quantity name — this is the label the eye reads.
const rotulosVisiveis = (secao.match(/x-text="qual === 'disco' \? 'disco' : qual"/g) || []).length;
ok('every gauge has a VISIBLE label (list and detail)', rotulosVisiveis >= 2,
   rotulosVisiveis + ' occurrence(s)');
ok('the label is not only for the screen reader',
   secao.includes('aria-label="qual') || secao.includes(":aria-label=\"qual + ' of '"));
// Deliberately tight: it demands the label IN THE CONSUMPTION BLOCK, not just any
// ">rede<" somewhere in the section. The first version of this pin was loose and
// went green over the mutation that deleted the label — it was the mutation that
// showed that, not the reading.
ok('the network row gained a label instead of two loose arrows',
   /style="min-width:3\.2rem">rede<\/span>/.test(secao));

// The CPU text cannot repeat the percentage: it read "0.8% of 2 core…", two
// percentages on the same line, rounded differently, and truncated.
const cpuTexto = comp.pvxMedidor(
  { id:'x', kind:'guest', vmid:1, status:obs('running'),
    cpu_frac:obs(0.008), cpu_cores:obs(2) }, 'cpu');
ok('the CPU text does NOT repeat the percentage', !cpuTexto.texto.includes('%'), cpuTexto.texto);
ok('the CPU text states the cores', /core/.test(cpuTexto.texto), cpuTexto.texto);
const semNucleos = comp.pvxMedidor(
  { id:'y', kind:'guest', vmid:2, status:obs('running'), cpu_frac:obs(0.5) }, 'cpu');
ok('with no cores reported, it says so instead of inventing them',
   /cores not reported/.test(semNucleos.texto), semNucleos.texto);

// The absolute value belongs to the wide panel, not to the narrow column.
ok('the right-hand panel shows the absolute value (Summary tab)',
   /pvxAbaAtiva\(\) === 'resumo'[\s\S]{0,2500}pvxMedidor\(pvxNoAberto\(\), qual\)\.texto/.test(secao));


// ── no template expression throws with NOTHING selected ──────────────────────
//
// 🔴 THIS REPRODUCES A REAL CRASH, not a hypothesis. Clicking "← Lab summary"
// calls pvxLimpaSelecao(), which clears pvx.aberto — and pvxNoAberto() starts
// returning null. Alpine unmounts the subtree of the <template x-if>, but the
// effects that were inside it (chiefly the ones created by x-for) still evaluate
// ONCE before removal. At that point `pvxNoAberto().name` threw and the whole
// interface came down with:
//     TypeError: Cannot read properties of null (reading 'name')
//
// The old dereferences were safe by LUCK: nothing re-evaluated late. The x-for in
// the Consumption block broke that luck. So the pin does not check "there is a ?.
// in the file" — it EVALUATES every template expression in the exact state of the
// crash.
{
  const compVazio = Object.assign(Object.create(null), comp, {
    // functions that come from the 40-nodes.js module (spread into the same app());
    // absent here only because the harness loads one module at a time.
    nodesStatusStyle: () => '', nodesTransportBadge: () => '', nodesCredStyle: () => '',
    nodesCredLabel: () => '', nodesCredExpiry: () => '', nodesCredExpiryUrgente: () => false,
  });
  compVazio.pvx.aberto = ''; compVazio.pvx.detalhe = null; compVazio.pvx.aba = '';

  const exprs = new Set();
  // 🔴 THE ATTRIBUTE LIST WAS AN ALLOWLIST, AND IT AGED IN SILENCE. When the screen
  // gained the SVG charts, the new bindings (:d, :cx, :fill, :viewBox, x-model) fell
  // OUTSIDE the pin — which stayed green by evaluating 66 expressions and ignoring
  // the ones that had just been born. An allowlist of attributes has to be kept up
  // with every new piece of markup, and nobody keeps it up.
  //
  // Now it matches ANY Alpine binding: `x-something="..."` and `:something="..."`.
  const atributos = /(?:\sx-[a-z:.-]+|\s:(?!key=)[a-zA-Z-]+)="([^"]+)"/g;
  for (const m of secao.matchAll(atributos)) {
    let e = m[1];
    // `x-for="item in collection"` is not an evaluable expression: it is a loop
    // clause. What is worth evaluating is the COLLECTION — that is what throws when
    // the selected node disappears. `:key` stays out: it only exists inside the
    // loop, with the item variable in scope.
    if (/^\s*(\(?[\w\s,)]+\)?)\s+in\s+/.test(e)) e = e.replace(/^\s*\(?[\w\s,)]+\)?\s+in\s+/, '');
    // 🔴 NO STATE ALLOWLIST. The previous version only evaluated expressions that
    // mentioned pvxNoAberto, pvx.detalhe, pvx.saude, pvx.taskLog or pvx.con — and
    // all the state born afterwards (pvx.serie, pvx.sistema, pvx.backup,
    // pvx.topologia, pvx.pacotes, pvx.registro) fell OUTSIDE. The pin stayed green
    // while ignoring exactly the expressions that froze the screen and locked the
    // operator out of the terminal.
    //
    // It was the SECOND allowlist in this same pin to age in silence: the first was
    // of attributes, and I fixed it without looking for another one in the same
    // file. Now there is none — EVERYTHING is evaluated.
    exprs.add(e);
  }
  // Vacuity guard: if the regex stops matching, this pin would go green without
  // evaluating a single expression — the same blindness it exists to prevent.
  ok('the pin harvested template expressions to evaluate', exprs.size >= 150, exprs.size + ' expressions');

  // Names declared in `x-for="X in ..."` and `x-for="(X, Y) in ..."`.
  const nomesDeLaco = [...new Set(
    [...secao.matchAll(/x-for="\s*\(?([\w\s,]+?)\)?\s+in\s+/g)]
      .flatMap((m) => m[1].split(',').map((x) => x.trim()))
      .filter((x) => /^[A-Za-z_$][\w$]*$/.test(x)),
  )];
  ok('the pin harvested the loop variables to neutralise', nomesDeLaco.length >= 3,
     nomesDeLaco.join(', '));
  const itemNeutro = new Proxy(function () {}, {
    get: (t, k) => (k === Symbol.toPrimitive || k === 'toString' || k === 'valueOf'
      ? () => '' : itemNeutro),
    apply: () => itemNeutro,
    has: () => true,
  });

  const estouros = [];
  for (const e of exprs) {
    try {
      // The loop variables are NEUTRALISED, not null: what this pin measures is
      // "some expression throws because the STATE is null", and a `seg.chave`
      // blowing up for want of the x-for item would be noise hiding the signal.
      // The proxy returns itself for any property, so a chained access
      // (`g.credential.state`) survives too.
      new Function(...nomesDeLaco, `with(this){ return (${e}) }`)
        .call(compVazio, ...nomesDeLaco.map(() => itemNeutro));
    } catch (err) { estouros.push(`${err.message} — ${e.slice(0, 70)}`); }
  }
  ok('no expression throws with NOTHING selected', estouros.length === 0,
     estouros.length ? estouros[0] : `${exprs.size} evaluated`);
}


// ── the screen goes two-column early, and the class REALLY exists in the CSS ─
//
// 🔴 Two distinct defects, one pin for each.
//
// (a) The breakpoint was `lg` (1024px CSS). A laptop with Windows scaling at
//     150% reports ~947px with the window at half screen — below 1024. The
//     operator saw the page stacked "as if it were a phone", list taking up
//     everything, bars stretched. The limit was habit, not a measured width.
//
// (b) The class is an ARBITRARY value with a responsive prefix. If Tailwind does
//     not emit it into the generated CSS, the HTML stays correct and the layout
//     stays in a single column — silently, with no error at all. This is the
//     kind of failure that only shows up in the eye of whoever uses it, which
//     is how it got here.
{
  const BREAKPOINTS = { sm: 640, md: 768, lg: 1024, xl: 1280, '2xl': 1536 };
  const m = secao.match(/(\w+):grid-cols-\[minmax\((\d+)px,(\d+)px\)_1fr\]/);
  ok('the master-detail grid declares a breakpoint', !!m, m ? m[0] : 'not found');
  if (m) {
    const [, prefixo, minLista] = m;
    const px = BREAKPOINTS[prefixo];
    ok('the breakpoint is a known one', !!px, prefixo);
    ok('two columns from 768px or below', px <= 768, `${prefixo} = ${px}px`);
    ok('the list column asks for no more than 260px', Number(minLista) <= 260, minLista + 'px');

    // The missing link: the class has to be in the GENERATED CSS, not only in the HTML.
    const css = ler('internal/webassets/web/tailwind.css');
    const valor = `minmax(${m[2]}px,${m[3]}px)`;
    ok('the arbitrary class was emitted into tailwind.css', css.includes(valor),
       css.includes(valor) ? valor : `${valor} MISSING — run "make tailwind"`);
    const idx = css.indexOf(valor);
    const antes = css.slice(Math.max(0, idx - 4000), idx);
    const mq = [...antes.matchAll(/min-width: *(\d+)px/g)].pop();
    ok('and under a media query of at most 768px',
       !!mq && Number(mq[1]) <= 768, mq ? mq[1] + 'px' : 'no media query before it');
  }

  // ── EVERY arbitrary class in the section exists in the generated CSS ─────
  //
  // Generalises the case above. An arbitrary Tailwind class (`max-w-[420px]`,
  // `min-[900px]:flex`, …) only becomes CSS if the scan finds it; if it is not
  // emitted, the HTML is right and the style simply does not exist — silently.
  //
  // 🔴 A MEASUREMENT TRAP, and I fell into it: in the CSS the brackets come
  // ESCAPED (`.max-w-\[420px\]{max-width:420px}`). Searching for a raw
  // `max-w-[420px]` gives zero and makes a class that is there look absent. The
  // pin builds the escaped selector, the way Tailwind writes it.
  {
    const cssGerado = ler('internal/webassets/web/tailwind.css');
    const arbitrarias = new Set();
    for (const m2 of secao.matchAll(/class="([^"]+)"/g)) {
      for (const cls of m2[1].split(/\s+/)) {
        if (/^[a-z0-9:-]+\[[^\]]+\]$/i.test(cls) && !cls.startsWith(':')) arbitrarias.add(cls);
      }
    }
    ok('the pin harvested arbitrary classes to check', arbitrarias.size >= 3,
       arbitrarias.size + ': ' + [...arbitrarias].slice(0, 5).join(' '));
    // Instead of reproducing every Tailwind escaping rule — `\\[`, `\\(`, and the
    // comma that becomes the unicode escape `\\2c ` — we normalise the CSS back to
    // the original text of the class. Chasing the rules one by one is how the pin
    // would end up wrong the day Tailwind changes its escaping.
    const cssPlano = cssGerado.replace(/\\2c\s/g, ',').replace(/\\(.)/g, '$1');
    const ausentes = [...arbitrarias].filter((cls) => !cssPlano.includes('.' + cls));
    ok('every arbitrary class in the section exists in the generated CSS', ausentes.length === 0,
       ausentes.length ? ausentes.join(', ') + ' — run "make tailwind"' : `${arbitrarias.size} checked`);
  }

  // The bar cannot stretch without a cap: in stacked mode it became a 400px dash,
  // which adds no precision and only pushes the value away from the label.
  // The cap can live on the grid (max-w) or on the bar column (a fixed minmax).
  // What it cannot do is not exist: with no cap, in stacked mode the bar becomes a
  // 400px dash, which adds no precision and only pushes value away from label.
  const gradeMedidor = secao.match(/class="grid[^"]*"\s*\n?\s*style="grid-template-columns:3rem[^"]*"/);
  const temTeto = /max-w-\[\d+px\][^"]*"\s*\n?\s*style="grid-template-columns:3rem/.test(secao)
               || /grid-template-columns:[^"]*minmax\(\d+px,\s*\d+px\)/.test(secao);
  ok('the gauge bar has a width cap', temTeto,
     temTeto ? 'cap present' : 'no max-w and no fixed minmax on the gauge grid');
}


// ── the node row does not reserve width it never uses ───────────────────────
//
// 🔴 The complaint was: "why reserve such a big field for a simple ok? it could
// go on the same line as the title". The badge lived inside a
// <span class="w-24">, and that box took up 6rem EVEN with the badge hidden —
// a blank gap the size of a word, per node, per row. Together with `w-40` on the
// name, flex-wrap broke the row into three in a 240px column, and the age stamp
// ended up ON TOP of the disk bar.
{
  const linha = (() => {
    const i = secao.indexOf('@click="pvxSeleciona(n)"');
    return i < 0 ? '' : secao.slice(Math.max(0, i - 900), i + 3200);
  })();
  ok('the node row was located', linha.length > 1000, linha.length + ' chars');

  ok('no fixed width reserved on the node row',
     !/class="w-\d+"/.test(linha),
     (linha.match(/class="w-\d+"/g) || ['none']).join(', '));

  // (The POSITION pin that used to be here was removed: it compared indices inside
  // a fixed-size window of text and started failing on its own as soon as the row
  // grew, with nothing broken. The MEMBERSHIP pin just below — "it is inside the
  // title strip" — measures the same property without depending on how much markup
  // happens to sit around it.)

  // The age stamp also moves up into the title strip: below, it collided with the
  // disk bar.
  //
  // 🔴 The first version of this pin compared POSITIONS ("age before the gauges")
  // and went green over the mutation that pulled the stamp out of the title strip —
  // because "outside the strip, but before the x-for" still satisfied the
  // comparison. Relative position is weak; the real property is BELONGING to the
  // strip. The pin now cuts out the title strip and demands the stamp inside it.
  const faixaTitulo = (() => {
    const a = linha.indexOf('<div class="flex items-center gap-2 min-w-0">');
    if (a < 0) return '';
    const b = linha.indexOf('</div>', a);
    return b < 0 ? '' : linha.slice(a, b);
  })();
  ok('the title strip was cut out', faixaTitulo.length > 200, faixaTitulo.length + ' chars');
  ok('the age stamp is INSIDE the title strip',
     faixaTitulo.includes('pvxFormatAge(n.age_seconds)'));
  ok('and so is the state badge',
     faixaTitulo.includes('pvxRotuloEstado(pvxNoEstado(n))'));

  ok('the badge goes away when the node is ok', linha.includes("pvxNoEstado(n) !== 'ok'"));
  ok('and it does not flash before Alpine starts (x-cloak)',
     /pvxNoEstado\(n\) !== 'ok'"[\s\S]{0,40}x-cloak/.test(linha));
}


// ── node type: container, VM, hypervisor or external ────────────────────────
//
// 🔴 The complaint: "in proxmox you can tell whether it is a container, a vm, etc.
// here we cannot". The data was always there — in the id PREFIX — and the screen
// never showed it, so knowing whether `lxc/203` was a container meant decoding it.
{
  const tipoDe = (id, extra2 = {}) =>
    comp.pvxTipo(Object.assign({ id, kind: 'guest', vmid: 1 }, extra2));

  ok('lxc/203 is a container', tipoDe('lxc/203').sigla === 'CT', tipoDe('lxc/203').rotulo);
  ok('qemu/208 is a virtual machine', tipoDe('qemu/208').sigla === 'VM', tipoDe('qemu/208').rotulo);
  ok('node/pve is the hypervisor',
     tipoDe('node/pve', { kind: 'host' }).sigla === 'NODE', tipoDe('node/pve', { kind: 'host' }).rotulo);
  ok('canario (no prefix) is external',
     tipoDe('canario', { kind: 'externo' }).sigla === 'EXT', tipoDe('canario', { kind: 'externo' }).rotulo);

  // 🔴 The fallback cannot guess. Inventing "VM" would make the operator act on the
  // wrong category — starting, stopping or snapshotting something that is not what
  // the screen said it was.
  const desconhecido = tipoDe('coisa/9', { kind: 'coisa' });
  ok('an unknown type says it does not know, instead of guessing',
     desconhecido.chave === '?' && desconhecido.sigla === '?', desconhecido.rotulo);

  // A template is not a startable guest, and the difference has to show up BEFORE
  // somebody tries to start it.
  const modelo = tipoDe('lxc/900', { template: true });
  ok('a template is marked as a template', modelo.modelo === true);
  ok('and the title warns that it is not startable',
     /TEMPLATE/.test(comp.pvxTipoTitulo({ id: 'lxc/900', kind: 'guest', vmid: 900, template: true })));

  // Colour = state; letter = type. If the two palettes collided, neither would be
  // trustworthy — the green "ok" badge and a green type would fight over the same
  // visual channel.
  const coresTipo = ['node', 'lxc', 'qemu', 'externo', '?'].map((k) => comp.TIPOS[k].cor.toLowerCase());
  const coresEstado = ['ok', 'atencao', 'critico', 'vencido', 'sem-credencial', 'parado']
    .map((e) => comp.pvxCorEstado(e).toLowerCase());
  const colisao = coresTipo.filter((c) => coresEstado.includes(c));
  ok('the TYPE palette does not collide with the STATE one', colisao.length === 0,
     colisao.length ? colisao.join(', ') : `${coresTipo.length} distinct colours`);

  // Count per type in the header, with the 11 real nodes of the lab.
  const listaReal = [
    { id: 'node/pve', kind: 'host', name: 'pve' },
    ...[201, 202, 203, 204, 205, 206, 207].map((v) => ({ id: `lxc/${v}`, kind: 'guest', vmid: v })),
    { id: 'qemu/100', kind: 'guest', vmid: 100 }, { id: 'qemu/208', kind: 'guest', vmid: 208 },
    { id: 'canario', kind: 'externo', name: 'canario' },
  ];
  const compReal = Object.assign(Object.create(null), comp, { nodes: { list: listaReal, poll: {} } });
  const contagem = compReal.pvxContagemPorTipo();
  const mapa = Object.fromEntries(contagem.map((t) => [t.sigla, t.n]));
  ok('counts 7 CT, 2 VM, 1 NODE and 1 EXT',
     mapa.CT === 7 && mapa.VM === 2 && mapa['NODE'] === 1 && mapa.EXT === 1, JSON.stringify(mapa));
  ok('a non-existent type does not show up with a zero',
     !contagem.some((t) => t.n === 0), 'zero is not information');

  // The filter has to accept the word the badge shows.
  const filtrar = (txt) => compReal.pvxFiltraNos(listaReal, txt, '', () => 'ok');
  ok('the filter accepts "tipo:ct" (the short form the screen shows)', filtrar('tipo:ct').length === 7,
     filtrar('tipo:ct').length + ' nodes');
  ok('the filter accepts "tipo:vm"', filtrar('tipo:vm').length === 2, filtrar('tipo:vm').length + ' nodes');
  ok('the filter accepts "tipo:conteiner"', filtrar('tipo:conteiner').length === 7);
  ok('and it still accepts "tipo:lxc" (the old vocabulary did not break)',
     filtrar('tipo:lxc').length === 7);
  ok('the filter accepts "tipo:externo"', filtrar('tipo:externo').length === 1);

  // The screen shows the badge in the three places that matter.
  ok('the type badge shows up on the list row', /:style="pvxEstiloTipo\(n\)"/.test(secao));
  ok('and in the header of the detail panel',
     /:style="pvxEstiloTipo\(pvxNoAberto\(\)\)"/.test(secao));
  ok('and the count per type is in the list header',
     /pvxContagemPorTipo\(\)/.test(secao));
  ok('the filter hint states the short form the screen shows', /tipo:ct/.test(secao));
}


// ── every declared tab HAS a panel that shows it ────────────────────────────
//
// 🔴 It is the tab-level version of the defect that made the whole screen open
// BLACK: a clickable tab whose panel does not exist leaves the prime area blank,
// with no error at all. Here the tab list grew from 6 to 11 at once — exactly the
// kind of change where one of them ends up with no markup and nobody notices.
{
  const todas = [...new Set([...comp.ABAS_HOST, ...comp.ABAS_GUEST].map((a) => a.id))];
  ok('the pin harvested the declared tabs', todas.length >= 8, todas.length + ': ' + todas.join(','));
  const semPainel = todas.filter((id) => !secao.includes(`pvxAbaAtiva()==='${id}'`)
                                      && !secao.includes(`pvxAbaAtiva() === '${id}'`));
  ok('every declared tab has a panel on the screen', semPainel.length === 0,
     semPainel.length ? 'NO PANEL:' + semPainel.join(', ') : todas.length + ' checked');

  // And the reverse: an orphan panel, reached by no tab, is dead code that nobody
  // will delete because it looks like it is in use.
  const idsNaTela = [...new Set([...secao.matchAll(/pvxAbaAtiva\(\)\s*===\s*'([\w-]+)'/g)].map((m) => m[1]))];
  const orfaos = idsNaTela.filter((id) => !todas.includes(id));
  ok('no orphan panel (with no tab reaching it)', orfaos.length === 0,
     orfaos.length ? 'ORPHANS:' + orfaos.join(', ') : idsNaTela.length + ' panels');
}


// ── the host Shell is not a guest console ───────────────────────────────────
//
// 🔴 One is root INSIDE a container; the other is root ON THE HYPERVISOR, from
// where the nine guests go down with one command. They do not share a credential
// either: no node token has Sys.Console, so reusing one of them on the host would
// give 403. The screen has to SAY the difference before opening, not after.
{
  const host = { id: 'node/pve', kind: 'host', name: 'pve' };
  ok('the host can open a Shell', comp.pvxConsolePode(host) === true);
  ok('and a guest with no credential is still refused, with a reason',
     comp.pvxConsolePode({ id: 'lxc/202', kind: 'guest', vmid: 202, credential: { state: 'ausente' } }) === false
     && /node token/.test(comp.pvxConsoleMotivo({ id: 'lxc/202', kind: 'guest', vmid: 202, credential: { state: 'ausente' } })));
  ok('the screen states what the hypervisor Shell is BEFORE opening it',
     /pvxEhHost\(pvxNoAberto\(\)\)[\s\S]{0,400}root on/.test(secao));
}


// ── hypervisor power: the action with no remote undo ────────────────────────
//
// 🔴 The operator asked for Restart and Shut down knowing the machine has no IPMI
// and that Wake-on-LAN is no use (the machine routes the admin network itself).
// What the screen owes is the CONCRETE CONSEQUENCE before the click, not a
// generic warning — a generic warning is what trains people to ignore.
{
  const compE = Object.assign(Object.create(null), comp);
  compE.nodes = { list: [
    { id: 'node/pve', kind: 'host', name: 'pve' },
    { id: 'lxc/201', kind: 'guest', vmid: 201, name: 'games', status: obs('running') },
    { id: 'lxc/206', kind: 'guest', vmid: 206, name: 'data', status: obs('running') },
    { id: 'lxc/204', kind: 'guest', vmid: 204, name: 'lab', status: obs('stopped') },
  ], poll: {} };

  ok('counts only the RUNNING guests among the ones that would go down',
     compE.pvxGuestsLigados().length === 2,
     compE.pvxGuestsLigados().map((g) => g.name).join(','));

  // Captures what the confirmation would say, without executing anything.
  let dlg = null;
  compE.askConfirm = (titulo, texto, _fn, opts) => { dlg = { titulo, texto, opts }; };
  compE.pvx.aberto = 'node/pve';

  compE.pvxEnergiaHost('shutdown');
  ok('shutting down asks for confirmation', !!dlg);
  ok('and it demands TYPING the hypervisor name', dlg && dlg.opts && dlg.opts.requireText === 'pve',
     dlg && dlg.opts ? String(dlg.opts.requireText) : 'no requireText');
  ok('marked as dangerous', dlg && dlg.opts && dlg.opts.danger === true);
  ok('it names THE GUESTS that go down with it, not just the count',
     dlg && /games/.test(dlg.texto) && /data/.test(dlg.texto), dlg ? dlg.texto.slice(0, 60) : '');
  ok('it does not list an already stopped guest (noise on a confirmation trains you to ignore)',
     dlg && !/lab/.test(dlg.texto.split('\n')[0]));
  ok('it says it does NOT come back on its own and that restarting is on-site',
     dlg && /not come back on its own/i.test(dlg.texto) && /walking up to it/i.test(dlg.texto));
  ok('it warns that the panel loses contact', dlg && /loses contact/i.test(dlg.texto));

  // 🔴 The two sentences have to be DIFFERENT: restarting is betting the machine
  // comes back; shutting down is guaranteeing it does not come back on its own. One
  // text for both would make the graver one look like routine.
  const textoShutdown = dlg.texto;
  dlg = null;
  compE.pvxEnergiaHost('reboot');
  ok('restart also asks for confirmation by typing',
     dlg && dlg.opts && dlg.opts.requireText === 'pve');
  ok('and the reboot text is DIFFERENT from the shutdown text',
     dlg && dlg.texto !== textoShutdown);
  ok('the reboot is honest about the worst case (not coming back equals a shutdown)',
     dlg && /outcome is the same/i.test(dlg.texto));

  // On a guest, the buttons do not exist.
  compE.pvx.aberto = 'lxc/201';
  dlg = null;
  compE.pvxEnergiaHost('shutdown');
  ok('the action does NOT fire with a guest selected', dlg === null);

  // And on the screen: set apart, red-bordered, out of the middle of the numbers.
  ok('the power block exists and is visually set apart',
     /Hypervisor power/.test(secao) && /#ef444455/.test(secao));
  ok('the screen shows how many guests would go down before the click',
     /pvxGuestsLigados\(\)\.length/.test(secao));
}


// ── no state dereferenced by the template is born NULL ──────────────────────
//
// 🔴 The defect that locked the operator out of the terminal. `pvx.serie`,
// `pvx.sistema` and the others were born `null`, and the template dereferenced
// them — `pvx.serie.pontos` — before the first load, which is the state the screen
// ALWAYS opens in. A throw in Alpine takes down the WHOLE app, and the terminal
// lives in the same app.
//
// Relying on `?.` in every expression is fragile: the next one somebody writes may
// forget it. This pin demands the fix AT THE SOURCE — a stable shape in the state.
{
  // The rule is NOT "no field may be null": in `taskLog`, null MEANS "still
  // loading", and erasing that meaning would trade one defect for another. The rule
  // is more precise, and it is the one that describes the real defect:
  //
  //   a dereference with a PLAIN dot (`pvx.X.Y`) over a field that can be null is
  //   a crash waiting for the first load.
  //
  // `pvx.X?.Y` and `pvx.X && pvx.X.Y` are protected and pass.
  const desprotegidas = [];
  for (const m of secao.matchAll(/(?:\sx-[a-z:.-]+|\s:[a-zA-Z-]+)="([^"]+)"/g)) {
    const e = m[1];
    for (const d of e.matchAll(/\bpvx\.([a-zA-Z_$][\w$]*)\.(?!\s)/g)) {
      const campo = d[1];
      if (campo === 'carregado') continue;
      // Three forms of protection count: `pvx.X && …`, `pvx.X?.…` / `pvx.X ? …`,
      // and the short circuit `!pvx.X || …` — this last one protects because, when
      // null, the `!` is true and the rest never evaluates.
      const protegido = new RegExp(`pvx\\.${campo}\\s*(?:&&|\\?)`).test(e)
        || new RegExp(`!\\s*pvx\\.${campo}\\s*\\|\\|`).test(e);
      const nulo = comp.pvx[campo] === null || comp.pvx[campo] === undefined;
      if (!protegido && nulo) desprotegidas.push(`pvx.${campo} em: ${e.slice(0, 54)}`);
    }
  }
  ok('no unprotected dereference over a field that is born null',
     desprotegidas.length === 0,
     desprotegidas.length ? desprotegidas[0] : 'none');

  // And the fields created in this pass have a stable shape at the source — that is
  // what stops the next expression having to remember the `?.`.
  for (const c of ['serie', 'sistema', 'backup', 'topologia', 'pacotes', 'registro']) {
    ok(`pvx.${c} is born with a shape, not null`, comp.pvx[c] !== null && comp.pvx[c] !== undefined,
       String(comp.pvx[c] === null ? 'null' : typeof comp.pvx[c]));
  }
}

// ── the NOTE: minimal Markdown, SAFE BY CONSTRUCTION ────────────────────────
//
// 🔴 The order is what makes this safe: escape EVERYTHING first, then
// reintroduce the tags the renderer knows. No character of the source text can
// become a tag, because by the time the tags go in there is no `<` left coming
// from the text. The reverse path — convert and then clean — is the classic
// source of XSS, and a pin that only tested "bold becomes <strong>" would not
// tell the two apart.
{
  const md = comp.pvxMd.bind(comp);

  ok('a heading becomes a heading', /<h3 class="pvx-md-h">apps<\/h3>/.test(md('# apps')));
  ok('bold', /<strong>faz<\/strong>/.test(md('**faz**')));
  ok('inline code', /<code class="pvx-md-code">ss -lnt<\/code>/.test(md('`ss -lnt`')));
  ok('list', /<ul class="pvx-md-ul">\n<li>um<\/li>/.test(md('- um')));
  ok('rule', /<hr class="pvx-md-hr">/.test(md('---')));

  // ── what must NOT happen ──────────────────────────────────────────────────
  const hostil = [
    '<script>alert(1)</script>',
    '<img src=x onerror=alert(1)>',
    '<a href="javascript:alert(1)">x</a>',
    '"><svg onload=alert(1)>',
    '[clique](javascript:alert(1))',
    '[clique](data:text/html,<script>alert(1)</script>)',
    '<iframe src="https://evil"></iframe>',
  ];
  const saidas = hostil.map(md);
  ok('🔴 no tag from the TEXT survives rendering',
     saidas.every((h) => !/<(script|img|svg|iframe|object|embed|link|style)\b/i.test(h)),
     'escaping AFTER converting is the classic source of XSS; here it escapes first');
  // 🔴 THE INSPECTION LOOKS ONLY AT THE TAGS THE RENDERER EMITTED.
  //
  // Every `<` coming from the text has already become `&lt;`, so any `<...>` in the
  // output is ours. Searching for " onerror=" in the WHOLE output would flag the
  // escaped text `&lt;img src=x onerror=alert(1)&gt;` — which is exactly the
  // CORRECT result, displayed as letters. That is what happened in the first
  // version of this pin: it failed the right behaviour.
  const tagsEmitidas = (h) => h.match(/<[^>]*>/g) || [];
  ok('🔴 no event handler comes out in an emitted tag',
     saidas.every((h) => tagsEmitidas(h).every((t) => !/\son\w+\s*=/i.test(t))),
     'onerror/onload in an attribute is execution without <script>');
  ok('🔴 links only with http(s) — javascript: and data: stay TEXT',
     saidas.every((h) => tagsEmitidas(h).every((t) => !/href\s*=\s*["']?\s*(javascript|data|vbscript):/i.test(t))),
     'an href with an executable scheme is <script> under another name');
  // And the positive control: the hostile text STAYS VISIBLE, escaped. Making it
  // vanish would be the screen hiding what the note says.
  ok('the hostile text shows up escaped, it does not disappear',
     /&lt;script&gt;/.test(md('<script>alert(1)</script>')),
     'a filter that DELETES content is a filter that hides the note from the operator');
  ok('an http link still works (the pin is not "ban everything")',
     /<a href="https:\/\/exemplo\.test" target="_blank" rel="noopener noreferrer">doc<\/a>/
       .test(md('[doc](https://exemplo.test)')));

  // 🔴 NEGATIVE CONTROL ON THE PIN ITSELF: if the renderer started returning the
  // raw text, everything above would still be "no dangerous tag" by accident — the
  // output would be escaped text. This case guarantees that it CONVERTS.
  ok('the renderer really converts (otherwise the tests above would be vacuous)',
     /<strong>/.test(md('**x**')) && /<h3/.test(md('# y')));

  // An empty note is neither an error nor loose HTML.
  ok('an empty note returns an empty string', md('') === '' && md(null) === '' && md(undefined) === '');
}

console.log(mau ? `\nFAIL — ${mau} case(s)` : '\nPASS — contextual tabs, an honest summary and the automatic pause');
process.exit(mau?1:0);

