import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const src = read('internal/webassets/web/vendor/panel/app/41-proxmox.js');
const win = {};
new Function('window','document','location','setInterval','clearInterval', src)(
  win, {hidden:false,getElementById:()=>null}, {protocol:'http:',host:'x'}, ()=>1, ()=>0);
const mod = win.PanelProxmoxModule();
const obs = (v, t=1) => ({value:v, observed_at:t});
const comp = Object.assign({
  nodes:{ list:[
    {id:'node/pve', kind:'host',  name:'hypervisor-01', status:obs('online')},
    {id:'lxc/201',  kind:'guest', vmid:201, name:'games', status:obs('running'),
     mem_used:obs(2.87e9), mem_total:obs(17.18e9), cpu_frac:obs(0.0815)},
    {id:'qemu/208', kind:'guest', vmid:208, name:'dev', status:obs('running'),
     mem_used:obs(7.16e9), mem_total:obs(8.59e9), cpu_frac:obs(0.0035)},
    {id:'lxc/204',  kind:'guest', vmid:204, name:'lab', status:obs('stopped'),
     mem_used:{value:0,observed_at:0}, mem_total:{value:0,observed_at:0}},
  ], poll:{age_seconds:12}},
  api:async()=>({ok:true,status:200,json:async()=>({})}), showToast(){}, askConfirm(){},
  _apiError(){}, _errText(e){return String(e)}, $nextTick(){}, loadNodes(){},
}, mod);

let bad = 0;
const ok = (name, cond, extra='') => { console.log((cond?'  ✓ ':'  ✗ ')+name+(extra?'  → '+extra:'')); if(!cond) bad++; };

const host  = comp.nodes.list[0], guest = comp.nodes.list[2];
const idsHost = comp.pvxNodeTabs(host).map((a) => a.id);
const idsGuest = comp.pvxNodeTabs(guest).map((a) => a.id);

ok('both lists open on the Summary', idsHost[0] === 'summary' && idsGuest[0] === 'summary');
ok('the host has the infrastructure tabs',
   ['disks', 'storage', 'zfs'].every((x) => idsHost.includes(x)), idsHost.join(','));
ok('the guest has console and snapshots',
   ['console', 'snaps'].every((x) => idsGuest.includes(x)), idsGuest.join(','));
ok('no infrastructure tab leaks into the guest',
   !['disks', 'storage', 'zfs', 'network', 'system', 'packages', 'registry', 'perms']
     .some((x) => idsGuest.includes(x)), idsGuest.join(','));
ok('snapshots do not leak to the host (they do not exist for it)',
   !idsHost.includes('snaps'), idsHost.join(','));
ok('the host has a Shell', idsHost.includes('console'));
ok('and the guest has a console too', idsGuest.includes('console'));
ok('no repeated id in either list',
   new Set(idsHost).size === idsHost.length && new Set(idsGuest).size === idsGuest.length);
ok('no host tab shows up with a guest selected',
   !comp.pvxNodeTabs(guest).some(a=>['disks','storage','zfs','perms'].includes(a.id)));

comp.pvx.open = 'qemu/208'; comp.pvx.tab = 'zfs';
ok('a "zfs" tab inherited on a guest normalises to "summary"', comp.pvxActiveTab() === 'summary',
   comp.pvxActiveTab());
comp.pvx.tab = 'console';
ok('a valid guest tab is respected', comp.pvxActiveTab() === 'console');

comp.pvx.open = '';
const r = comp.pvxLabSummary();
ok('counts 3 guests, 2 running', r.guests===3 && r.running===2, `${r.guests}/${r.running}`);
ok('a guest with no timestamp goes into noData, not in as a zero', r.noData===1, String(r.noData));
ok('summed memory IGNORES the one with no data (it does not dilute the average)',
   Math.abs(r.memTotal - (17.18e9+8.59e9)) < 1e6, (r.memTotal/1e9).toFixed(2)+' GB');
ok('the memory percentage matches the sum of the observed ones',
   Math.abs(r.memPct - ((2.87e9+7.16e9)/(17.18e9+8.59e9)*100)) < 0.01, r.memPct.toFixed(1)+'%');

{
  ok('with no answer yet, the screen does not pretend it loaded', comp.pvxBackups().loaded === false);
  ok('and the state already has a SHAPE before loading (it is not null)',
     Array.isArray(comp.pvx.backup.datastores) && comp.pvx.backup.datastores.length === 0);

  comp.pvx.backup = {
    observed_at: 1_000_000,
    datastores: [
      { storage: 'pbs', total: 65, last_ctime: 1_000_000 - 3 * 3600, guests: [100, 201, 202, 203, 204, 205, 206, 207, 208], schedule_state: 'active' },
      { storage: 'backupusb', total: 5, last_ctime: 1_000_000 - 340 * 3600, guests: [100, 201, 204], schedule_state: 'active' },
      { storage: 'local', total: 0, last_ctime: 0, guests: [], schedule_state: 'outside-pve' },
      { storage: 'broken', error: 'the hypervisor refused' },
    ],
  };
  const items = comp.pvxBackups().items;
  ok('each datastore shows up SEPARATELY (merging masks the stopped layer)', items.length === 4,
     items.map((d) => d.storage).join(' '));

  const pbs = comp.pvxBackupAge(items[0]);
  const usb = comp.pvxBackupAge(items[1]);
  ok('the age comes from the SERVER timestamp (observed_at − last_ctime)',
     pbs.seg === 3 * 3600 && usb.seg === 340 * 3600, `pbs=${pbs.seg}s usb=${usb.seg}s`);

  const empty = comp.pvxBackupAge(items[2]);
  ok('an empty datastore says "no copy yet", not an age counted from 1970',
     empty.empty === true && empty.seg === undefined);
  ok('a datastore with an error does not become silence', !!comp.pvxBackupAge(items[3]).error);

  const isGreen = (e) => /#22c55e/.test(e);
  const isRed = (e) => /#ef4444/.test(e);
  ok('PBS at 3 h comes out green', isGreen(comp.pvxBackupStyle(items[0])));
  ok('an empty datastore NEVER comes out green', !isGreen(comp.pvxBackupStyle(items[2])));

  const pbsStale = { storage: 'pbs', total: 1, last_ctime: 1_000_000 - 200 * 3600, guests: [1], schedule_state: 'active' };
  ok('PBS at 200 h fails, even though that is a normal age for the rotation',
     !isGreen(comp.pvxBackupStyle(pbsStale)));

  const disarmed = { storage: 'backupusb', total: 5, last_ctime: 1_000_000 - 340 * 3600,
                      guests: [1, 2, 3], schedule_state: 'disarmed', schedule: '03:30' };
  const eD = comp.pvxBackupState(disarmed);
  ok('a DISARMED layer does not come out red', !isRed(comp.pvxBackupStyle(disarmed)), eD.color);
  ok('and it says the label "disarmed", not an alarming age', eD.label === 'disarmed', eD.label);
  ok('and it explains WHY, with the time it used to run', /schedule turned off/.test(eD.note) && /03:30/.test(eD.note), eD.note);
  ok('and it says outright that this is not a failure', /this is not a failure/.test(eD.note));

  const outsidePve = { storage: 'pbs', total: 65, last_ctime: 1_000_000 - 3 * 3600,
                      guests: [1], schedule_state: 'outside-pve' };
  const eF = comp.pvxBackupState(outsidePve);
  ok('a fresh layer with NO job in PVE stays green', isGreen(comp.pvxBackupStyle(outsidePve)), eF.color);
  ok('and the screen admits it does not know who schedules it', /does not know by whom/.test(eF.note), eF.note);

  const staleActive = { storage: 'pbs', total: 1, last_ctime: 1_000_000 - 400 * 3600,
                       guests: [1], schedule_state: 'active', schedule: '03:30' };
  ok('an ACTIVE and old layer stays red (the real alarm did not vanish)',
     isRed(comp.pvxBackupStyle(staleActive)));
  comp.pvx.backup = { datastores: [] };
  comp.pvx.loaded.backup = false;
}

let loaded = 0; comp.loadNodes = () => { loaded++; };
comp.pvx.con = {guest:'', state:'closed', error:''}; comp.pvx.focusFilter = false;
comp.pvxTick();
ok('no console and no focus: the cycle FETCHES', loaded===1, 'fetches='+loaded);
comp.pvx.con.guest = 'lxc/204'; comp.pvxTick();
ok('an open console PAUSES the fetch', loaded===1 && comp.pvx.paused, 'fetches='+loaded);
ok('the pause states its reason', comp.pvx.pauseReason==='console open', comp.pvx.pauseReason);
comp.pvx.con.guest=''; comp.pvx.focusFilter = true; comp.pvxTick();
ok('focus in the filter pauses too', loaded===1 && comp.pvx.pauseReason==='typing in the filter');
comp.nodes.poll.age_seconds = 300; comp.pvxTick();
ok('the age stamp KEEPS running during the pause',
   comp.pvx.ageSec===300 && comp.pvxAgeStale(), comp.pvxAgeText());
comp.pvx.focusFilter=false; comp.pvxTick();
ok('out of the pause, fetching resumes', loaded===2 && !comp.pvx.paused, 'fetches='+loaded);


const index = read('internal/webassets/web/index.html');
const section = (() => {
  const i = index.indexOf("currentView==='proxmox'");
  const f = index.indexOf('</section>', index.indexOf('/master-detail grid'));
  return index.slice(i, f);
})();

ok('the Proxmox section was cut out for the pins', section.length > 5000, section.length + ' chars');

const visibleLabels = (section.match(/x-text="which === 'disco' \? 'disco' : which"/g) || []).length;
ok('every gauge has a VISIBLE label (list and detail)', visibleLabels >= 2,
   visibleLabels + ' occurrence(s)');
ok('the label is not only for the screen reader',
   section.includes('aria-label="which') || section.includes(":aria-label=\"which + ' of '"));
ok('the network row gained a label instead of two loose arrows',
   /style="min-width:3\.2rem">net<\/span>/.test(section));

const cpuText = comp.pvxGauge(
  { id:'x', kind:'guest', vmid:1, status:obs('running'),
    cpu_frac:obs(0.008), cpu_cores:obs(2) }, 'cpu');
ok('the CPU text does NOT repeat the percentage', !cpuText.text.includes('%'), cpuText.text);
ok('the CPU text states the cores', /core/.test(cpuText.text), cpuText.text);
const noCores = comp.pvxGauge(
  { id:'y', kind:'guest', vmid:2, status:obs('running'), cpu_frac:obs(0.5) }, 'cpu');
ok('with no cores reported, it says so instead of inventing them',
   /cores not reported/.test(noCores.text), noCores.text);

ok('the right-hand panel shows the absolute value (Summary tab)',
   /pvxActiveTab\(\) === 'summary'[\s\S]{0,2500}pvxGauge\(pvxOpenNode\(\), which\)\.text/.test(section));


{
  const emptyComp = Object.assign(Object.create(null), comp, {
    nodesStatusStyle: () => '', nodesTransportBadge: () => '', nodesCredStyle: () => '',
    nodesCredLabel: () => '', nodesCredExpiry: () => '', nodesCredExpiryUrgent: () => false,
  });
  emptyComp.pvx.open = ''; emptyComp.pvx.detail = null; emptyComp.pvx.tab = '';

  const exprs = new Set();
  const attributes = /(?:\sx-[a-z:.-]+|\s:(?!key=)[a-zA-Z-]+)="([^"]+)"/g;
  for (const m of section.matchAll(attributes)) {
    let e = m[1];
    if (/^\s*(\(?[\w\s,)]+\)?)\s+in\s+/.test(e)) e = e.replace(/^\s*\(?[\w\s,)]+\)?\s+in\s+/, '');
    exprs.add(e);
  }
  ok('the pin harvested template expressions to evaluate', exprs.size >= 150, exprs.size + ' expressions');

  const loopNames = [...new Set(
    [...section.matchAll(/x-for="\s*\(?([\w\s,]+?)\)?\s+in\s+/g)]
      .flatMap((m) => m[1].split(',').map((x) => x.trim()))
      .filter((x) => /^[A-Za-z_$][\w$]*$/.test(x)),
  )];
  ok('the pin harvested the loop variables to neutralise', loopNames.length >= 3,
     loopNames.join(', '));
  const neutralItem = new Proxy(function () {}, {
    get: (t, k) => (k === Symbol.toPrimitive || k === 'toString' || k === 'valueOf'
      ? () => '' : neutralItem),
    apply: () => neutralItem,
    has: () => true,
  });

  const overflows = [];
  for (const e of exprs) {
    try {
      new Function(...loopNames, `with(this){ return (${e}) }`)
        .call(emptyComp, ...loopNames.map(() => neutralItem));
    } catch (err) { overflows.push(`${err.message} — ${e.slice(0, 70)}`); }
  }
  ok('no expression throws with NOTHING selected', overflows.length === 0,
     overflows.length ? overflows[0] : `${exprs.size} evaluated`);
}


{
  const BREAKPOINTS = { sm: 640, md: 768, lg: 1024, xl: 1280, '2xl': 1536 };
  const m = section.match(/(\w+):grid-cols-\[minmax\((\d+)px,(\d+)px\)_1fr\]/);
  ok('the master-detail grid declares a breakpoint', !!m, m ? m[0] : 'not found');
  if (m) {
    const [, prefix, minList] = m;
    const px = BREAKPOINTS[prefix];
    ok('the breakpoint is a known one', !!px, prefix);
    ok('two columns from 768px or below', px <= 768, `${prefix} = ${px}px`);
    ok('the list column asks for no more than 260px', Number(minList) <= 260, minList + 'px');

    const css = read('internal/webassets/web/tailwind.css');
    const value = `minmax(${m[2]}px,${m[3]}px)`;
    ok('the arbitrary class was emitted into tailwind.css', css.includes(value),
       css.includes(value) ? value : `${value} MISSING — run "make tailwind"`);
    const idx = css.indexOf(value);
    const before = css.slice(Math.max(0, idx - 4000), idx);
    const mq = [...before.matchAll(/min-width: *(\d+)px/g)].pop();
    ok('and under a media query of at most 768px',
       !!mq && Number(mq[1]) <= 768, mq ? mq[1] + 'px' : 'no media query before it');
  }

  {
    const generatedCss = read('internal/webassets/web/tailwind.css');
    const arbitrary = new Set();
    for (const m2 of section.matchAll(/class="([^"]+)"/g)) {
      for (const cls of m2[1].split(/\s+/)) {
        if (/^[a-z0-9:-]+\[[^\]]+\]$/i.test(cls) && !cls.startsWith(':')) arbitrary.add(cls);
      }
    }
    ok('the pin harvested arbitrary classes to check', arbitrary.size >= 3,
       arbitrary.size + ': ' + [...arbitrary].slice(0, 5).join(' '));
    const flatCss = generatedCss.replace(/\\2c\s/g, ',').replace(/\\(.)/g, '$1');
    const missing = [...arbitrary].filter((cls) => !flatCss.includes('.' + cls));
    ok('every arbitrary class in the section exists in the generated CSS', missing.length === 0,
       missing.length ? missing.join(', ') + ' — run "make tailwind"' : `${arbitrary.size} checked`);
  }

  const gaugeGrid = section.match(/class="grid[^"]*"\s*\n?\s*style="grid-template-columns:3rem[^"]*"/);
  const hasCap = /max-w-\[\d+px\][^"]*"\s*\n?\s*style="grid-template-columns:3rem/.test(section)
               || /grid-template-columns:[^"]*minmax\(\d+px,\s*\d+px\)/.test(section);
  ok('the gauge bar has a width cap', hasCap,
     hasCap ? 'cap present' : 'no max-w and no fixed minmax on the gauge grid');
}


{
  const line = (() => {
    const i = section.indexOf('@click="pvxSelect(n)"');
    return i < 0 ? '' : section.slice(Math.max(0, i - 900), i + 3200);
  })();
  ok('the node row was located', line.length > 1000, line.length + ' chars');

  ok('no fixed width reserved on the node row',
     !/class="w-\d+"/.test(line),
     (line.match(/class="w-\d+"/g) || ['none']).join(', '));

  const bannerTitle = (() => {
    const a = line.indexOf('<div class="flex items-center gap-2 min-w-0">');
    if (a < 0) return '';
    const b = line.indexOf('</div>', a);
    return b < 0 ? '' : line.slice(a, b);
  })();
  ok('the title strip was cut out', bannerTitle.length > 200, bannerTitle.length + ' chars');
  ok('the age stamp is INSIDE the title strip',
     bannerTitle.includes('pvxFormatAge(n.age_seconds)'));
  ok('and so is the state badge',
     bannerTitle.includes('pvxStateLabel(pvxNodeState(n))'));

  ok('the badge goes away when the node is ok', line.includes("pvxNodeState(n) !== 'ok'"));
  ok('and it does not flash before Alpine starts (x-cloak)',
     /pvxNodeState\(n\) !== 'ok'"[\s\S]{0,40}x-cloak/.test(line));
}


{
  const typeOf = (id, extra2 = {}) =>
    comp.pvxType(Object.assign({ id, kind: 'guest', vmid: 1 }, extra2));

  ok('lxc/203 is a container', typeOf('lxc/203').abbrev === 'CT', typeOf('lxc/203').label);
  ok('qemu/208 is a virtual machine', typeOf('qemu/208').abbrev === 'VM', typeOf('qemu/208').label);
  ok('node/pve is the hypervisor',
     typeOf('node/pve', { kind: 'host' }).abbrev === 'NODE', typeOf('node/pve', { kind: 'host' }).label);
  ok('canary (no prefix) is external',
     typeOf('canary', { kind: 'external' }).abbrev === 'EXT', typeOf('canary', { kind: 'external' }).label);

  const unknown = typeOf('thing/9', { kind: 'thing' });
  ok('an unknown type says it does not know, instead of guessing',
     unknown.key === '?' && unknown.abbrev === '?', unknown.label);

  const model = typeOf('lxc/900', { template: true });
  ok('a template is marked as a template', model.template === true);
  ok('and the title warns that it is not startable',
     /TEMPLATE/.test(comp.pvxTypeTitle({ id: 'lxc/900', kind: 'guest', vmid: 900, template: true })));

  const typeColors = ['node', 'lxc', 'qemu', 'external', '?'].map((k) => comp.TYPES[k].color.toLowerCase());
  const stateColors = ['ok', 'warning', 'critical', 'stale', 'no-credential', 'stopped']
    .map((e) => comp.pvxStateColor(e).toLowerCase());
  const collision = typeColors.filter((c) => stateColors.includes(c));
  ok('the TYPE palette does not collide with the STATE one', collision.length === 0,
     collision.length ? collision.join(', ') : `${typeColors.length} distinct colours`);

  const realList = [
    { id: 'node/pve', kind: 'host', name: 'pve' },
    ...[201, 202, 203, 204, 205, 206, 207].map((v) => ({ id: `lxc/${v}`, kind: 'guest', vmid: v })),
    { id: 'qemu/100', kind: 'guest', vmid: 100 }, { id: 'qemu/208', kind: 'guest', vmid: 208 },
    { id: 'canary', kind: 'external', name: 'canary' },
  ];
  const compReal = Object.assign(Object.create(null), comp, { nodes: { list: realList, poll: {} } });
  const counts = compReal.pvxCountByType();
  const map = Object.fromEntries(counts.map((t) => [t.abbrev, t.n]));
  ok('counts 7 CT, 2 VM, 1 NODE and 1 EXT',
     map.CT === 7 && map.VM === 2 && map['NODE'] === 1 && map.EXT === 1, JSON.stringify(map));
  ok('a non-existent type does not show up with a zero',
     !counts.some((t) => t.n === 0), 'zero is not information');

  const filter = (txt) => compReal.pvxFilterNodes(realList, txt, '', () => 'ok');
  ok('the filter accepts "type:ct" (the short form the screen shows)', filter('type:ct').length === 7,
     filter('type:ct').length + ' nodes');
  ok('the filter accepts "type:vm"', filter('type:vm').length === 2, filter('type:vm').length + ' nodes');
  ok('the filter accepts "type:container"', filter('type:container').length === 7);
  ok('and it still accepts "type:lxc" (the old vocabulary did not break)',
     filter('type:lxc').length === 7);
  ok('the filter accepts "type:external"', filter('type:external').length === 1);

  ok('the type badge shows up on the list row', /:style="pvxTypeStyle\(n\)"/.test(section));
  ok('and in the header of the detail panel',
     /:style="pvxTypeStyle\(pvxOpenNode\(\)\)"/.test(section));
  ok('and the count per type is in the list header',
     /pvxCountByType\(\)/.test(section));
  ok('the filter hint states the short form the screen shows', /type:ct/.test(section));
}


{
  const all = [...new Set([...comp.TABS_HOST, ...comp.TABS_GUEST].map((a) => a.id))];
  ok('the pin harvested the declared tabs', all.length >= 8, all.length + ': ' + all.join(','));
  const noPanel = all.filter((id) => !section.includes(`pvxActiveTab()==='${id}'`)
                                      && !section.includes(`pvxActiveTab() === '${id}'`));
  ok('every declared tab has a panel on the screen', noPanel.length === 0,
     noPanel.length ? 'NO PANEL:' + noPanel.join(', ') : all.length + ' checked');

  const idsOnScreen = [...new Set([...section.matchAll(/pvxActiveTab\(\)\s*===\s*'([\w-]+)'/g)].map((m) => m[1]))];
  const orphans = idsOnScreen.filter((id) => !all.includes(id));
  ok('no orphan panel (with no tab reaching it)', orphans.length === 0,
     orphans.length ? 'ORPHANS:' + orphans.join(', ') : idsOnScreen.length + ' panels');
}


{
  const host = { id: 'node/pve', kind: 'host', name: 'pve' };
  ok('the host can open a Shell', comp.pvxConsoleCan(host) === true);
  ok('and a guest with no credential is still refused, with a reason',
     comp.pvxConsoleCan({ id: 'lxc/202', kind: 'guest', vmid: 202, credential: { state: 'absent' } }) === false
     && /node token/.test(comp.pvxConsoleReason({ id: 'lxc/202', kind: 'guest', vmid: 202, credential: { state: 'absent' } })));
  ok('the screen states what the hypervisor Shell is BEFORE opening it',
     /pvxIsHost\(pvxOpenNode\(\)\)[\s\S]{0,400}root on/.test(section));
}


{
  const compE = Object.assign(Object.create(null), comp);
  compE.nodes = { list: [
    { id: 'node/pve', kind: 'host', name: 'pve' },
    { id: 'lxc/201', kind: 'guest', vmid: 201, name: 'games', status: obs('running') },
    { id: 'lxc/206', kind: 'guest', vmid: 206, name: 'data', status: obs('running') },
    { id: 'lxc/204', kind: 'guest', vmid: 204, name: 'lab', status: obs('stopped') },
  ], poll: {} };

  ok('counts only the RUNNING guests among the ones that would go down',
     compE.pvxRunningGuests().length === 2,
     compE.pvxRunningGuests().map((g) => g.name).join(','));

  let dlg = null;
  compE.askConfirm = (title, text, _fn, opts) => { dlg = { title, text, opts }; };
  compE.pvx.open = 'node/pve';

  compE.pvxHostPower('shutdown');
  ok('shutting down asks for confirmation', !!dlg);
  ok('and it demands TYPING the hypervisor name', dlg && dlg.opts && dlg.opts.requireText === 'pve',
     dlg && dlg.opts ? String(dlg.opts.requireText) : 'no requireText');
  ok('marked as dangerous', dlg && dlg.opts && dlg.opts.danger === true);
  ok('it names THE GUESTS that go down with it, not just the count',
     dlg && /games/.test(dlg.text) && /data/.test(dlg.text), dlg ? dlg.text.slice(0, 60) : '');
  ok('it does not list an already stopped guest (noise on a confirmation trains you to ignore)',
     dlg && !/lab/.test(dlg.text.split('\n')[0]));
  ok('it says it does NOT come back on its own and that restarting is on-site',
     dlg && /not come back on its own/i.test(dlg.text) && /walking up to it/i.test(dlg.text));
  ok('it warns that the panel loses contact', dlg && /loses contact/i.test(dlg.text));

  const shutdownText = dlg.text;
  dlg = null;
  compE.pvxHostPower('reboot');
  ok('restart also asks for confirmation by typing',
     dlg && dlg.opts && dlg.opts.requireText === 'pve');
  ok('and the reboot text is DIFFERENT from the shutdown text',
     dlg && dlg.text !== shutdownText);
  ok('the reboot is honest about the worst case (not coming back equals a shutdown)',
     dlg && /outcome is the same/i.test(dlg.text));

  compE.pvx.open = 'lxc/201';
  dlg = null;
  compE.pvxHostPower('shutdown');
  ok('the action does NOT fire with a guest selected', dlg === null);

  ok('the power block exists and is visually set apart',
     /Hypervisor power/.test(section) && /#ef444455/.test(section));
  ok('the screen shows how many guests would go down before the click',
     /pvxRunningGuests\(\)\.length/.test(section));
}


{
  const unprotected = [];
  for (const m of section.matchAll(/(?:\sx-[a-z:.-]+|\s:[a-zA-Z-]+)="([^"]+)"/g)) {
    const e = m[1];
    for (const d of e.matchAll(/\bpvx\.([a-zA-Z_$][\w$]*)\.(?!\s)/g)) {
      const field = d[1];
      if (field === 'loaded') continue;
      const isProtected = new RegExp(`pvx\\.${field}\\s*(?:&&|\\?)`).test(e)
        || new RegExp(`!\\s*pvx\\.${field}\\s*\\|\\|`).test(e);
      const nullish = comp.pvx[field] === null || comp.pvx[field] === undefined;
      if (!isProtected && nullish) unprotected.push(`pvx.${field} in: ${e.slice(0, 54)}`);
    }
  }
  ok('no unprotected dereference over a field that is born null',
     unprotected.length === 0,
     unprotected.length ? unprotected[0] : 'none');

  for (const c of ['series', 'system', 'backup', 'topology', 'packages', 'registry']) {
    ok(`pvx.${c} is born with a shape, not null`, comp.pvx[c] !== null && comp.pvx[c] !== undefined,
       String(comp.pvx[c] === null ? 'null' : typeof comp.pvx[c]));
  }
}

{
  const md = comp.pvxMd.bind(comp);

  ok('a heading becomes a heading', /<h3 class="pvx-md-h">apps<\/h3>/.test(md('# apps')));
  ok('bold', /<strong>does<\/strong>/.test(md('**does**')));
  ok('inline code', /<code class="pvx-md-code">ss -lnt<\/code>/.test(md('`ss -lnt`')));
  ok('list', /<ul class="pvx-md-ul">\n<li>one<\/li>/.test(md('- one')));
  ok('rule', /<hr class="pvx-md-hr">/.test(md('---')));

  const hostile = [
    '<script>alert(1)</script>',
    '<img src=x onerror=alert(1)>',
    '<a href="javascript:alert(1)">x</a>',
    '"><svg onload=alert(1)>',
    '[click](javascript:alert(1))',
    '[click](data:text/html,<script>alert(1)</script>)',
    '<iframe src="https://evil"></iframe>',
  ];
  const outputs = hostile.map(md);
  ok('🔴 no tag from the TEXT survives rendering',
     outputs.every((h) => !/<(script|img|svg|iframe|object|embed|link|style)\b/i.test(h)),
     'escaping AFTER converting is the classic source of XSS; here it escapes first');
  const emittedTags = (h) => h.match(/<[^>]*>/g) || [];
  ok('🔴 no event handler comes out in an emitted tag',
     outputs.every((h) => emittedTags(h).every((t) => !/\son\w+\s*=/i.test(t))),
     'onerror/onload in an attribute is execution without <script>');
  ok('🔴 links only with http(s) — javascript: and data: stay TEXT',
     outputs.every((h) => emittedTags(h).every((t) => !/href\s*=\s*["']?\s*(javascript|data|vbscript):/i.test(t))),
     'an href with an executable scheme is <script> under another name');
  ok('the hostile text shows up escaped, it does not disappear',
     /&lt;script&gt;/.test(md('<script>alert(1)</script>')),
     'a filter that DELETES content is a filter that hides the note from the operator');
  ok('an http link still works (the pin is not "ban everything")',
     /<a href="https:\/\/sample\.test" target="_blank" rel="noopener noreferrer">doc<\/a>/
       .test(md('[doc](https://sample.test)')));

  ok('the renderer really converts (otherwise the tests above would be vacuous)',
     /<strong>/.test(md('**x**')) && /<h3/.test(md('# y')));

  ok('an empty note returns an empty string', md('') === '' && md(null) === '' && md(undefined) === '');
}

console.log(bad ? `\nFAIL — ${bad} case(s)` : '\nPASS — contextual tabs, an honest summary and the automatic pause');
process.exit(bad?1:0);

