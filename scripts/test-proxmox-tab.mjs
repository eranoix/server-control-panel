#!/usr/bin/env node
// test-proxmox-tab.mjs — the pin for the Proxmox tab.
//
// House style (see test-sched-catalog.mjs): plain node, zero dependencies, and
// the code under test is EXTRACTED FROM THE SERVED FILE instead of re-copied
// here: a test that loads a copy proves the copy, not what goes to the browser.
//
// What it defends, and every item fails on its own:
//
//  1. pvxFormatAge is a DELIBERATE copy of nodesFormatAge. Both are extracted
//     from BOTH files, executed, and have to produce IDENTICAL output. The
//     copy is honest only while somebody proves it has not drifted.
//  2. The new module does not read the browser clock and does not touch the
//     neighbouring tab's timer (both checks ignore comments — a lesson this
//     repository learned the hard way: `grep -c` counts prose).
//  3. The SEVEN registration grafts exist, matched on a line of CODE.
//  4. TEL_IDS still has NO `proxmox` key, AND the deliberate-absence comment
//     mentions `proxmox`. The two halves together: the first alone would pass
//     for forgetfulness; the second makes the absence a declared one.
//
//   run: node scripts/test-proxmox-tab.mjs
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const APP = join(here, '..', 'internal', 'webassets', 'web', 'vendor', 'vpsm', 'app');
const IDX = join(here, '..', 'internal', 'webassets', 'web', 'index.html');

const shell = readFileSync(join(APP, '00-shell.js'), 'utf8');
const nodesJs = readFileSync(join(APP, '40-nodes.js'), 'utf8');
const pvxJs = readFileSync(join(APP, '41-proxmox.js'), 'utf8');
const index = readFileSync(IDX, 'utf8');

let failed = 0;
function check(nome, cond, extra) {
  if (cond) { console.log('  ✓', nome); }
  else { console.error('  ✗', nome, extra === undefined ? '' : extra); failed++; }
}

// noComments strips line comments so that an ABSENCE check cannot be
// satisfied (or violated) by prose. Only whole lines and trailing comments with
// a space before the slashes — no literal in this project contains " //".
function noComments(src) {
  return src.split('\n')
    .filter(l => !l.trimStart().startsWith('//'))
    .map(l => l.replace(/\s\/\/.*$/, ''))
    .join('\n');
}

// extracts the body of an object method indented by six spaces, by name.
function extract(src, arquivo, nome, sig) {
  const re = new RegExp(nome + '\\(' + sig + '\\) \\{([\\s\\S]*?)\\n      \\},');
  const m = src.match(re);
  if (!m) { console.error('FATAL: method not found in', arquivo + ':', nome); process.exit(2); }
  return m[1];
}

// ── 1. the copy has not drifted ────────────────────────────────────────────
const nodesFormatAge = new Function('ageSeconds', extract(nodesJs, '40-nodes.js', 'nodesFormatAge', 'ageSeconds'));
const pvxFormatAge = new Function('ageSeconds', extract(pvxJs, '41-proxmox.js', 'pvxFormatAge', 'ageSeconds'));

const samples = [-1, 0, 1, 59, 60, 3599, 3600, 172800, null, undefined];
let diverged = '';
for (const a of samples) {
  const x = nodesFormatAge(a), y = pvxFormatAge(a);
  if (x !== y) { diverged = `age=${a}: nodes=${JSON.stringify(x)} pvx=${JSON.stringify(y)}`; break; }
}
check('pvxFormatAge has not drifted from nodesFormatAge', diverged === '', diverged);
check('-1 is still "never observed" (never "0s")', pvxFormatAge(-1) === 'never observed', pvxFormatAge(-1));
check('0 is "data from 0s ago", not "never"', pvxFormatAge(0) === 'data from 0s ago', pvxFormatAge(0));

// ── 2. the module reads neither the browser clock nor the neighbour timer ──
const pvxCode = noComments(pvxJs);
check('41-proxmox.js does not contain Date.now(', !pvxCode.includes('Date.now('));
check('41-proxmox.js does not contain new Date(', !pvxCode.includes('new Date('));
check('41-proxmox.js does not mention nodesPollTimer', !pvxCode.includes('nodesPollTimer'));
check('41-proxmox.js has a timer of its own (pvxPollTimer)', pvxCode.includes('pvxPollTimer'));
check('41-proxmox.js exposes window.VPSMProxmoxModule', /window\.VPSMProxmoxModule\s*=\s*function/.test(pvxCode));

// ── 3. the seven grafts ────────────────────────────────────────────────────
const grafts = [
  ['index.html: the tab button', index, /^\s*<button class="tab-btn"[^>]*@click="setTab\('proxmox'\)"/m],
  // The screen stopped being a <section x-show> and became a lazy mount under
  // <template x-if>: with x-show Alpine evaluated the whole tree at boot, so a
  // missing 41-proxmox.js module took down the WHOLE APP instead of just this
  // tab. The graft checks the new contract — late mounting AND the fallback
  // branch — not the old tag; asserting the old shape only kept the pin red.
  ['index.html: the screen mounts lazily', index,
    /^\s*<template x-if="currentView==='proxmox' && typeof pvxStaleStyle==='function'">$/m],
  ['index.html: fallback when the module is missing', index,
    /^\s*<template x-if="currentView==='proxmox' && typeof pvxStaleStyle!=='function'">$/m],
  ['index.html: the module script', index, /^<script src="\/vendor\/vpsm\/app\/41-proxmox\.js\?v=__VPSM_BUILD__"><\/script>$/m],
  ['00-shell.js: spread into app()', shell, /^\s*\.\.\.\(window\.VPSMProxmoxModule \? window\.VPSMProxmoxModule\(\) : \{ pvx: \{ health: null \} \}\),$/m],
  ['00-shell.js: command palette', shell, /^\s*\{label:'Operations · Proxmox',\s*kind:'page',\s*page:'proxmox',/m],
  ['00-shell.js: PAGE_REMAP', shell, /^\s*proxmox:\s*\['operations',\s*'proxmox'\],$/m],
  ['00-shell.js: _triggerViewLoaders', shell, /^\s*if \(p==='proxmox'\)\s*\{ this\.pvxInit\(\); this\.pvxStartPoll\(\); \}$/m],
];
for (const [nome, src, re] of grafts) check(nome, re.test(src));

// the module's script has to come AFTER 40-nodes.js: app() spreads in load
// order, and a module that arrives before the shell does not exist for Alpine.
//
// 🔴 The comparison is between the <script> TAGS, not the first appearance of
// the name in the file. An earlier version compared `indexOf('41-proxmox.js')`
// with `indexOf('40-nodes.js')` and passed by accident: both names appear in a
// COMMENT before they appear in a tag, and folding the two screens together
// changed the comment order without changing load order. A pin that fails when
// the code is right is as bad as one that passes when it is wrong.
const tag = (arq) => index.indexOf('<script src="/vendor/vpsm/app/' + arq + '?v=__VPSM_BUILD__">');
check('41-proxmox.js loads after 40-nodes.js (comparing the TAGS)',
  tag('40-nodes.js') > 0 && tag('41-proxmox.js') > tag('40-nodes.js'),
  `40-nodes=${tag('40-nodes.js')} 41-proxmox=${tag('41-proxmox.js')}`);

// ── 4. TEL_IDS against the canonical list, and the declared absences ──────
//
// 🔴 This block was INVERTED at one point, and the inversion records a fact,
// not a loosening. Until the list was re-cut, `proxmox` and `config` HAD to sit
// outside TEL_IDS: the ids did not exist in the canonical list, and screens.txt
// has to be byte-identical in both forks (sha256, by a parity check), so
// inventing the key here would emit an id outside the allowlist and break that
// parity. The list was re-cut IN BOTH FORKS in the same act, immediately before
// the home fork was frozen — the window in which that was still possible. Now
// the requirement is the symmetric one: the key HAS to exist, or a live screen
// falls back into the `unknown` bucket.
//
// What was NOT loosened: the rule "no id outside the allowlist" still holds,
// and it is now verified more strongly — against the FILE, and not against a
// list of names re-copied here. Any invented id in TEL_IDS or in TEL_SUB fails,
// including the ones nobody thought to list.
const SCREENS = join(here, '..', 'internal', 'telemetry', 'screens.txt');
const allowlist = new Set(readFileSync(SCREENS, 'utf8').split('\n').map(l => l.trim()).filter(Boolean));
check('screens.txt has the re-cut canonical list (76 ids)', allowlist.size === 76, allowlist.size);

const telBlock = shell.match(/TEL_IDS: \{([\s\S]*?)\n    \},/);
if (!telBlock) { console.error('FATAL: TEL_IDS block not found'); process.exit(2); }
const telSubBlock = shell.match(/TEL_SUB: \[([\s\S]*?)\n    \],/);
if (!telSubBlock) { console.error('FATAL: TEL_SUB block not found'); process.exit(2); }

const paresTel = [...telBlock[1].matchAll(/^\s*([A-Za-z0-9_$]+):\s*'([^']+)'/gm)].map(m => [m[1], m[2]]);
check('TEL_IDS was parsed (the block did not come out empty)', paresTel.length >= 40, paresTel.length);

const notInList = paresTel.filter(([, id]) => !allowlist.has(id)).map(([k, id]) => `${k}->${id}`);
check('🔴 every id in TEL_IDS is in the screens.txt allowlist', notInList.length === 0,
  notInList.join(' '));

const subs = [...telSubBlock[1].matchAll(/'([^']+)'/g)].map(m => m[1]);
const subNotInList = subs.filter(id => !allowlist.has(id));
check('🔴 every sub-action in TEL_SUB is in the screens.txt allowlist', subNotInList.length === 0,
  subNotInList.join(' '));

const idDe = (key) => (paresTel.find(([k]) => k === key) || [])[1];
check('TEL_IDS HAS the proxmox key, and it points at operacoes.proxmox',
  idDe('proxmox') === 'operacoes.proxmox', idDe('proxmox'),
);
check('TEL_IDS HAS the config key, and it points at config',
  idDe('config') === 'config', idDe('config'),
);

// The absences that REMAIN are still declared absences: `nodes` (the screen was
// folded into Proxmox and never becomes a currentView) and the two screens
// whose ids are already in the canonical list but whose tab does not exist yet.
// An undeclared absence is an absence somebody "fixes" later.
const comment = shell.slice(shell.indexOf('DELIBERATE absences'), shell.indexOf('TEL_IDS: {'));
for (const key of ['nodes', 'operacoes.backup', 'operacoes.embutidas']) {
  check(`the absence of ${key} is DECLARED in the comment`, comment.includes(key),
    'an undeclared absence is an absence somebody "fixes" later');
}
for (const key of ['nodes', 'backup', 'embutidas']) {
  check(`TEL_IDS really does NOT have the key ${key} (the declaration does not lie)`,
    !paresTel.some(([k]) => k === key));
}
check('the declaration states the reason (screens-parity)', /byte-identical/.test(comment));

// ── 5. storage capacity and zpool ──────────────────────────────────────────
//
// 🔴 The check that matters most here is the one on the GUARD. An earlier pass
// put a banner on screen saying "this token cannot see /storage"; a later ACL
// change made it vanish. The wrong way to make it vanish is to DELETE the
// banner — then it never comes back, and the regression on the day the
// privilege is withdrawn goes unnoticed. The right way is for the banner to
// stay in the HTML, tied to the MEASURED verdict. These checks fail deleters.

check('41-proxmox.js has pvxLoadStorage', /pvxLoadStorage\(\)/.test(pvxCode));
check('41-proxmox.js has pvxLoadZfs', /pvxLoadZfs\(\)/.test(pvxCode));
check('pvxInit loads capacity AND zpool',
  /pvxInit\(\) \{[\s\S]*?pvxLoadStorage\(\)[\s\S]*?pvxLoadZfs\(\)[\s\S]*?\},/.test(pvxCode));

// Capacity is a HEARTBEAT: without being in the poll, the number freezes on the
// screen while the age does not grow — stale data presented as live, again.
const poll = extract(pvxJs, '41-proxmox.js', 'pvxStartPoll', '');
check('capacity and zpool are in the 30 s poll (they are a heartbeat)',
  poll.includes('pvxLoadStorage()') && poll.includes('pvxLoadZfs()'),
  'without the poll the age never grows and the block lies in green');

// The guard, in its three readings. `sem-medida` is the third, and it is the
// one that stops the screen accusing a lack of permission nobody measured.
const estado = extract(pvxJs, '41-proxmox.js', 'pvxStorageState', '');
for (const st of ['sem-medida', 'sem-permissao', 'ok']) {
  check(`pvxStorageState distinguishes '${st}'`, estado.includes(`'${st}'`));
}
check('pvxStorageState demands the TIMESTAMP before accusing (observed_at)',
  estado.includes('observed_at'),
  'without checking the timestamp, "I never asked" would turn into "no permission"');

// The function is extracted from the file that is SERVED and really executed;
// only the read of component state is swapped for the argument. Rewriting the
// logic here would prove the copy, not what goes to the browser.
const guardFn = new Function('storageDoPainel', estado.replace(/this\.pvx\.storage/g, 'storageDoPainel'));
check('guard: no timestamp → sem-medida', guardFn({ datastore_audit: { value: false, observed_at: 0 } }) === 'sem-medida');
check('🔴 guard: measured and DENIED → sem-permissao (the banner COMES BACK)',
  guardFn({ datastore_audit: { value: false, observed_at: 1787000000 } }) === 'sem-permissao');
check('🔴 guard: measured and granted → ok (the banner GOES on its own)',
  guardFn({ datastore_audit: { value: true, observed_at: 1787000000 } }) === 'ok');

// And the banner has to keep EXISTING in the HTML, tied to the verdict.
check('index.html: the no-permission banner is still in the HTML',
  /x-show="pvxStorageState\(\) === 'sem-permissao'"/.test(index),
  'deleting the banner is removing the detector because the alarm stopped ringing');
// 🔴 THESE TWO PINS USED TO LOCK THE TITLE, and the title is the cheapest part
// to change and the one that matters least. The block was renamed from "Storage
// capacity" to "Datastores" — more precise, because it went on to show type,
// content, usage AND the pool behind each one — and the pin failed over a
// better screen. A label pin protects a word; what needs a guard is that the
// block EXISTS and iterates the datastores.
check('index.html: the datastore block exists and iterates the pools',
  /pvxActiveTab\(\)==='storage'/.test(index) && /x-for="p in pvxStoragePools\(\)"/.test(index));
check('index.html: the ZFS pool block exists and iterates the pools',
  /pvxActiveTab\(\)==='zfs'/.test(index) && /x-for="p in pvxZfsPools\(\)"/.test(index));
check('index.html: usage bar per storage', /pvxUsoStyle\(p\.used_pct\)/.test(index));
check('index.html: capacity has an age OF ITS OWN on screen',
  /pvxFormatAge\(pvx\.storage \? pvx\.storage\.age_seconds : null\)/.test(index),
  'without its own age the block would inherit the health badge and lie in green');
check('index.html: zpool has an age OF ITS OWN on screen',
  /pvxFormatAge\(pvx\.zfs \? pvx\.zfs\.age_seconds : null\)/.test(index));

// Only ONLINE is green. A DEGRADED pool on a single-disk server cannot show up
// in amber, because amber invites you to leave it for later.
const zfsStyle = extract(pvxJs, '41-proxmox.js', 'pvxZfsStyle', 'p');
const zfsStyleFn = new Function('p', zfsStyle);
check('🔴 DEGRADED is not green', !zfsStyleFn({ health: 'DEGRADED', saudavel: false }).includes('#22c55e'));
check('ONLINE is green', zfsStyleFn({ health: 'ONLINE', saudavel: true }).includes('#22c55e'));

// The usage bar saturates, and the red band starts at 85% — filling a pool with
// no redundancy is one of the few ways to lose data with no hardware failing.
const usoStyle = extract(pvxJs, '41-proxmox.js', 'pvxUsoStyle', 'pct');
const usoFn = new Function('pct', usoStyle);
check('bar: 90% is red', usoFn(90).includes('#ef4444'));
check('bar: 75% is amber', usoFn(75).includes('#f59e0b'));
check('bar: 7% is green', usoFn(6.87).includes('#22c55e'));
check('bar: an absurd value saturates at 100%', usoFn(9999).includes('width:100%'));
check('bar: null does not become NaN', usoFn(null).includes('width:0%'));

// ── remote console and rollback ────────────────────────────────────────────
//
// 🔴 The first three items in this section are invariants of SECRET and of
// AUDIT TRAIL, not of appearance. They fail the most tempting "improvement"
// anyone could make to this file: handing the ticket back to the browser so it
// can open the WebSocket straight at the hypervisor. That would work, and it
// would put a shell credential in DevTools.

check('🔴 41-proxmox.js does NOT know about a console ticket (vncticket)',
  !pvxCode.includes('vncticket') && !pvxCode.includes('PVEVNC'),
  'the ticket is a shell credential — it stays locked inside internal/pve');
check('🔴 41-proxmox.js does NOT build a hypervisor URL (vncwebsocket)',
  !pvxCode.includes('vncwebsocket'),
  'the browser cannot reach hypervisor.local and has no TLS pin');
// The check cuts out the WHOLE EXPRESSION of `new WebSocket(...)` — and not the
// string literal of the route. An earlier version of this pin looked only at
// the literal, and the mutation `... + '&token=' + localStorage.vpsm_token`
// walked straight past it: the `token=` came from OUTSIDE the quotes. A pin
// that fails like that is worse than none, because it looks like it is guarding.
const wsExpr = (() => {
  const i = pvxCode.indexOf('new WebSocket(');
  if (i < 0) return '';
  return pvxCode.slice(i, pvxCode.indexOf(';', i));
})();
check('the console opens /ws/proxmox/console?node=', /\/ws\/proxmox\/console\?node=/.test(wsExpr), wsExpr);
check('🔴 the console WebSocket does NOT carry a token in the URL',
  wsExpr !== '' && !/token/i.test(wsExpr),
  'a query lands in the access log = a replayable shell credential (see /ws/shell)');
check('🔴 41-proxmox.js does not even touch the panel JWT',
  !pvxCode.includes('vpsm_token') && !pvxCode.includes('localStorage'),
  'the session travels in the HttpOnly cookie; the module has no reason to read the token');
check('🔴 41-proxmox.js does NOT build the "0:N:" frame (the count is in BYTES, on the server)',
  !/['"`]0:['"`]\s*\+/.test(pvxCode),
  'data.length counts UTF-16: "é" would arrive cut in half — measured on CT 204');
check('input goes as {type:"input"} and the translation belongs to the server',
  /type:\s*'input'/.test(pvxCode) && /type:\s*'resize'/.test(pvxCode));
check('terminal output is written as BINARY (Uint8Array)',
  /term\.write\(new Uint8Array/.test(pvxCode),
  'going through a string would break ANSI and UTF-8 split across two frames');

// The console HAS to close when you leave the tab: a live shell on a guest with
// nobody watching is a session the trail records as hours, from one misclick.
const stopPoll = extract(pvxJs, '41-proxmox.js', 'pvxStopPoll', '');
check('🔴 leaving the tab CLOSES the console', /pvxCloseConsole\(\)/.test(stopPoll),
  'without this the shell stays alive with the tab closed');

// A guest with no node token (CT 202, `pbs`) gets no console — and the screen
// SAYS why instead of offering a button that fails.
const conState = extract(pvxJs, '41-proxmox.js', 'pvxConsoleGuestState', 'g');
const conStateFn = new Function('g', conState);
check('🔴 missing credential → console unavailable WITH A REASON',
  conStateFn({ credential: { state: 'ausente' } }).can === false &&
  /node token/.test(conStateFn({ credential: { state: 'ausente' } }).motivo));
check('credential ok → console available', conStateFn({ credential: { state: 'ok' } }).can === true);
check('credential expired → unavailable (a 401 is indistinguishable from revocation)',
  conStateFn({ credential: { state: 'expirada' } }).can === false);
check('a node with no credential field does not become "can"', conStateFn({}).can === false);

check('index.html: the console block, now in the side panel of the node',
  /<span class="font-semibold text-sm">Console<\/span>/.test(index));
check('index.html: the terminal is IN-PAGE (div#pvx-console), with no iframe',
  /id="pvx-console"/.test(index) && !/<iframe[^>]*console/i.test(index));

// 🔴 The console guard was RAISED A LEVEL. It used to be a `:disabled` on an
// <option> of the selector: calling `pvxOpenConsole` by any other path (side
// panel, palette, browser console) opened the WebSocket against a guest with no
// token. A widget guard is a convenience; the rule now lives in the FUNCTION,
// and the button stays disabled WITH A REASON on top of it.
const openCon = extract(pvxJs, '41-proxmox.js', 'pvxOpenConsole', 'nodeId');
check('🔴 pvxOpenConsole REFUSES a guest with no credential in the function itself',
  /pvxConsoleCan\(g\)/.test(openCon) && /return;/.test(openCon),
  'a guard only on the widget is a guard that gets bypassed by another path');
check('index.html: the console button disables with a reason (and does not vanish)',
  /:disabled="!pvxConsoleCan\(pvxOpenNode\(\)\)"/.test(index) &&
  /pvxConsoleReason\(pvxOpenNode\(\)\)/.test(index));

// Rollback: exposed, confirmed by TYPING, and suspend left OUT.
check('🔴 rollback uses requireText (confirmation by typing)',
  /requireText:\s*label/.test(pvxCode),
  'what is lost has no second copy: the pool is single-disk');
check('🔴 the rollback warning says what is lost AND that there is no mirror',
  /ceases to exist/.test(pvxJs) && /single disk, with no mirror/.test(pvxJs),
  'a confirmation with no written consequence is a confirmation clicked on autopilot');
check('index.html: rollback button on the snapshot row',
  /pvxRollbackSnap\(pvx\.guestSel, sn\.name\)/.test(index));
check('🔴 there is NO suspend button (vzsuspend fails on the CRIU of this host)',
  !/pvxSuspende|vzsuspend|Suspender/.test(pvxCode) && !/pvxSuspende/.test(index),
  'a button that always errors trains the operator to ignore errors');
check('the reason suspend is left out is WRITTEN in the code',
  /CRIU|criu/.test(pvxJs) && /lxc-checkpoint/.test(pvxJs),
  'an absence with no written reason becomes a ticket; with a reason, it becomes a decision');

// The guest list has to arrive together with the tab — anyone landing straight
// on Proxmox found the selector empty, with no error at all.
const init = extract(pvxJs, '41-proxmox.js', 'pvxInit', '');
check('🔴 pvxInit loads the node list (otherwise the selector is born empty)',
  /loadNodes\(\)/.test(init));

// ══════════════════════════════════════════════════════════════════════════
// the folded screen, with the node as its axis
// ══════════════════════════════════════════════════════════════════════════
//
// These rules are PURE functions on purpose, and that is why this pin can
// EXECUTE them instead of hunting for substrings. Every `check` below fails on
// its own for one specific regression, and each of them was verified by
// mutation before this file was committed.

// 🔴 The ABSENCE checks need a slice: the whole `index` holds thirty-odd
// screens, and "there is no <select> with auto-submit" would be failed by a
// <select> from another tab this work never touched. The slice is the section.
const pvxSection = index.slice(index.indexOf("currentView==='proxmox'"), index.indexOf('Deploy / Heroku-style PaaS'));

// ── the Nodes tab left the menu WITHOUT breaking the three ways in ─────────
check('🔴 the Nodes tab left the menu', !/setTab\('nodes'\)/.test(index),
  'the screen was folded in; the button cannot keep leading nowhere');
check('🔴 the currentView===\'nodes\' section no longer exists', !/currentView==='nodes'/.test(index));
// 🔴 The check above is an ABSENCE check, and absence does not prove reach. It
// stayed green while the tab opened BLACK: the old section had indeed gone, and
// no other one became reachable, because `tabToView` resolved the tab to the
// alias 'nodes'. Every absence pin needs the positive counterpart below.
check('🔴 tabToView resolves by the CANONICAL key, not by key order',
  /const canonical = this\.PAGE_REMAP\[tab\];/.test(shell),
  'without this an aliased tab (nodes/proxmox) resolves to the alias and the screen opens black');
check('🔴 the Proxmox tab resolves to a section that exists',
  (() => {
    const remap = {};
    const remapBlock = shell.slice(shell.indexOf('PAGE_REMAP: {'));
    for (const m of remapBlock.slice(0, remapBlock.indexOf('\n    },')).matchAll(
        /^\s*([A-Za-z0-9_]+):\s*\['(\w+)',\s*'([\w-]+)'\]/gm)) remap[m[1]] = [m[2], m[3]];
    const canonical = remap['proxmox'];
    if (!canonical || canonical[0] !== 'operations' || canonical[1] !== 'proxmox') return false;
    // same rule as the shell: canonical first
    return new RegExp("currentView==='proxmox'").test(index);
  })(),
  'the section has to EXIST and the resolution has to reach it — both halves');
check('🔴 PAGE_REMAP keeps `nodes` REDIRECTING to proxmox',
  /^\s*nodes:\s*\['operations',\s*'proxmox'\],$/m.test(shell),
  'deleting the key would send old links, bookmarks and the palette nowhere');
check('🔴 the command palette still finds "nos"/"inventario"',
  /kind:'page',\s*page:'proxmox',\s*kw:'nos nodes inventario/.test(shell),
  'whoever types "nos" wants the inventory — it changed address, not subject');
// 🔴 The check is about CODE, not about prose: the comment that explains the
// removal names the removed function, and a raw grep would fail precisely
// because of the explanation. It is the same trap, and it has caught us before.
const shellCode = noComments(shell);
check('🔴 _triggerViewLoaders no longer calls the removed timer',
  !/nodesStartPoll|nodesStopPoll/.test(shellCode),
  'calling a function that no longer exists breaks the whole navigation, not just the tab');
check('40-nodes.js REMOVED the timer that would never fire again',
  !/nodesStartPoll\(\)\s*\{/.test(nodesJs),
  'a timer guarded by currentView===\'nodes\' never runs again: code that looks like care and is not');
check('40-nodes.js is still the data layer (nodesCredLabel lives there)',
  /nodesCredLabel\(n\)\s*\{/.test(nodesJs) && !/nodesCredLabel\(n\)\s*\{/.test(pvxJs),
  're-copying the four credential states would create two truths about "revoked"');
check('TEL_IDS still has NO nodes key', !/^\s*nodes:\s*'/m.test(telBlock[1]));

// ── the vocabulary of states, executed ────────────────────────────────────
const nodeState = new Function('n', extract(pvxJs, '41-proxmox.js', 'pvxNodeState', 'n')
  // pvxGaugePct lives outside the object (precisely so this function can be
  // pure); here it enters as a MINIMAL reimplementation, only to exercise the
  // precedence. The measurement rules have checks of their own further down.
  .replace('pvxGaugePct(n, which)', '(n.__pct ? n.__pct[which] : null)'));

const live = (extra) => Object.assign({
  stale: false, transport: 'pve-api', kind: 'guest', vmid: 207,
  status: { value: 'running', observed_at: 1 }, credential: { state: 'ok' }, __pct: {},
}, extra || {});

check('state: a healthy node is `ok`', nodeState(live()) === 'ok');
// 🔴 And the screen has to OBEY: the function returning 'ok' does not stop the
// template drawing a green badge on every row. Measured by mutation: deleting
// the x-show sailed straight past the check above.
check('🔴 an `ok` node draws NO state badge on the row',
  /x-show="pvxNodeState\(n\) !== 'ok'"/.test(pvxSection),
  'with eleven nodes, nine green badges spend attention where there is no news');
check('🔴 nor in the side panel',
  /x-show="pvxNodeState\(pvxOpenNode\(\)\) !== 'ok'"/.test(pvxSection));
check('🔴 state: STALE data beats everything — nothing else can be asserted',
  nodeState(live({ stale: true, credential: { state: 'ausente' }, __pct: { ram: 99 } })) === 'vencido');
check('state: no credential beats "parado" (it is what explains why it will not start)',
  nodeState(live({ status: { value: 'stopped' }, credential: { state: 'ausente' } })) === 'sem-credencial');
check('🔴 state: a STOPPED guest is not judged by a gauge',
  nodeState(live({ status: { value: 'stopped' }, __pct: { ram: 99 } })) === 'parado',
  'a gauge on a switched-off guest means nothing at all');
check('state: 90% on any gauge is critical', nodeState(live({ __pct: { ram: 90 } })) === 'critico');
check('state: 70% is attention', nodeState(live({ __pct: { ram: 70 } })) === 'atencao');
check('state: 69.9% is still ok', nodeState(live({ __pct: { ram: 69.9 } })) === 'ok');
check('state: the WORST gauge wins (cpu ok + disk critical = critical)',
  nodeState(live({ __pct: { cpu: 1, disco: 95 } })) === 'critico');

// ── thresholds with hysteresis ────────────────────────────────────────────
const tier = new Function('pct', 'anterior', extract(pvxJs, '41-proxmox.js', 'pvxTier', 'pct, anterior'));
check('threshold: 91% with no history is critical', tier(91, undefined) === 'critico');
check('threshold: 75% with no history is attention', tier(75, undefined) === 'atencao');
check('🔴 hysteresis: what was critical at 88% STAYS critical (3 points of slack)',
  tier(88, 'critico') === 'critico',
  'without slack, a guest oscillating around 90% changes colour every tick and colour stops informing');
check('hysteresis: critical only lets go below 87%', tier(86.9, 'critico') === 'atencao');
check('hysteresis: attention at 68% stays attention', tier(68, 'atencao') === 'atencao');
check('hysteresis: attention lets go below 67%', tier(66.9, 'atencao') === 'ok');
check('hysteresis: attention does NOT stop a climb to critical', tier(95, 'atencao') === 'critico');
check('threshold: a missing value (-1) does not become an alarm', tier(-1, undefined) === 'ok');

// ── a missing measurement NEVER becomes zero ──────────────────────────────
const pctSource = pvxJs.match(/function pvxGaugePct\(n, which\) \{([\s\S]*?)\n  \}/);
if (!pctSource) { console.error('FATAL: pvxGaugePct not found'); process.exit(2); }
const pct = new Function('n', 'which', pctSource[1]);
const stamp = (v) => ({ value: v, observed_at: 1787000000 });
check('🔴 an UNREPORTED disk (-1, QEMU with no guest agent) returns null, never 0',
  pct({ disk_used: stamp(-1), disk_total: stamp(34359738368) }, 'disco') === null,
  '0% would draw a roomy disk over a number nobody measured');
check('a reported disk returns the right fraction',
  Math.round(pct({ disk_used: stamp(12362973184), disk_total: stamp(51539607552) }, 'disco')) === 24);
check('🔴 a field with NO TIMESTAMP returns null (never observed ≠ zero)',
  pct({ mem_used: { value: 0, observed_at: 0 }, mem_total: stamp(100) }, 'ram') === null);
check('ram: 7.19/8.59 GB on guest `dev` gives 83.7% (the number the screen was not showing)',
  Math.round(pct({ mem_used: stamp(7185268736), mem_total: stamp(8589934592) }, 'ram') * 10) / 10 === 83.6 ||
  Math.round(pct({ mem_used: stamp(7185268736), mem_total: stamp(8589934592) }, 'ram')) === 84);
check('cpu arrives as a FRACTION and becomes a percentage without multiplying by cores',
  Math.round(pct({ cpu_frac: stamp(0.0914188626454006) }, 'cpu') * 100) / 100 === 9.14,
  'multiplying by maxcpu would give 73% and match no other screen in the world');
check('a zeroed total does not become a division by zero',
  pct({ disk_used: stamp(10), disk_total: stamp(0) }, 'disco') === null);

// ── the gauge goes GREY when the node is not live ─────────────────────────
const cor = new Function('m', extract(pvxJs, '41-proxmox.js', 'pvxGaugeColor', 'm'));
const GRAY = '#64748b';
check('🔴 the gauge of a STALE/stopped node goes GREY, even with a low value',
  cor({ measured: true, live: false, tier: 'ok' }) === GRAY,
  'a coloured bar over dead data asserts a measurement nobody made');
check('🔴 the gauge of a stale node goes grey even while CRITICAL',
  cor({ measured: true, live: false, tier: 'critico' }) === GRAY);
check('a gauge with no measurement goes grey', cor({ measured: false, live: true, tier: 'ok' }) === GRAY);
check('a live and critical gauge is red', cor({ measured: true, live: true, tier: 'critico' }) === '#ef4444');
check('a live and ok gauge is green', cor({ measured: true, live: true, tier: 'ok' }) === '#22c55e');

// 🔴 No RENDERING expression may write to reactive state.
//
// `pvxGauge` is called from inside x-text/:style/:aria-valuenow, and it needs
// to remember the previous tier for the hysteresis to work. Keeping that memory
// in `this.pvx.*` would make the write invalidate the effect that produced it —
// a render loop. The memory lives in a Map in the closure of the IIFE.
const gaugeBody = extract(pvxJs, '41-proxmox.js', 'pvxGauge', 'n, which');
check('🔴 pvxGauge does NOT write to reactive Alpine state',
  !/this\.pvx\.[^=;\n]*=[^=]/.test(gaugeBody),
  'a reactive write inside a render expression is an effect loop');
check('the hysteresis memory lives outside the component (a Map in the closure)',
  /const tiersDeHisterese = new Map\(\);/.test(pvxJs) &&
  /tiersDeHisterese\.set/.test(gaugeBody));

// ── the `field:value` filter ──────────────────────────────────────────────
const filter = new Function('list', 'text', 'segment', 'stateOf',
  extract(pvxJs, '41-proxmox.js', 'pvxFilterNodes', 'list, text, segment, stateOf'));
// The list is the real lab, cut down: `games` healthy, `pbs` (CT 202) with no
// node token — which is its REAL state in the vault —, `dev` stopped and the
// hypervisor itself. `transport` matters: `pvxNodeState` only demands a
// credential from whatever speaks over the PVE API.
const LIST = [
  { id: 'lxc/201', name: 'games', kind: 'guest', transport: 'pve-api', status: { value: 'running' }, credential: { state: 'ok' } },
  { id: 'lxc/202', name: 'pbs', kind: 'guest', transport: 'pve-api', status: { value: 'running' }, credential: { state: 'ausente' } },
  { id: 'qemu/208', name: 'dev', kind: 'guest', transport: 'pve-api', status: { value: 'stopped' }, credential: { state: 'ok' } },
  { id: 'node/pve', name: 'pve', kind: 'host', transport: 'pve-api', status: { value: 'online' }, credential: { state: 'ok' } },
];
const fakeState = (n) => (n.credential.state !== 'ok' ? 'sem-credencial'
  : (n.status.value === 'stopped' ? 'parado' : 'ok'));
const ids = (r) => r.map(n => n.id).join(',');

check('an empty filter returns everything', filter(LIST, '', '', fakeState).length === 4);
check('a free-text filter matches name and id', ids(filter(LIST, 'games', '', fakeState)) === 'lxc/201');
check('filter tipo:lxc', ids(filter(LIST, 'tipo:lxc', '', fakeState)) === 'lxc/201,lxc/202');
check('filter status:stopped', ids(filter(LIST, 'status:stopped', '', fakeState)) === 'qemu/208');
check('filter cred:ausente', ids(filter(LIST, 'cred:ausente', '', fakeState)) === 'lxc/202');
check('🔴 two terms are ANDed, not ORed',
  ids(filter(LIST, 'tipo:lxc cred:ausente', '', fakeState)) === 'lxc/202',
  'OR would return MORE rows than the operator asked for — silently');
check('🔴 an UNKNOWN field becomes a literal search, it is not ignored',
  filter(LIST, 'tag:producao', '', fakeState).length === 0,
  'dropping the constraint nobody understood returns more rows than were asked for');
check('the segment of the health band filters together with the text',
  ids(filter(LIST, 'tipo:lxc', 'sem-credencial', fakeState)) === 'lxc/202');
check('a segment with no match returns empty (and empty is not an error)',
  filter(LIST, '', 'critico', fakeState).length === 0);

// ── 🔴 re-scoping the selection — Portainer #4430 ─────────────────────────
const reescopa = new Function('sel', 'visible', extract(pvxJs, '41-proxmox.js', 'pvxReescopa', 'sel, visible'));
check('🔴 the selection is RE-SCOPED when the filter changes',
  reescopa(['lxc/201', 'lxc/202', 'qemu/208'], filter(LIST, 'tipo:lxc', '', fakeState)).join(',') === 'lxc/201,lxc/202',
  'Portainer #4430: select all → filter → delete deleted what was never on the screen');
check('re-scoping empties the selection when nothing visible is left',
  reescopa(['lxc/201'], filter(LIST, 'status:stopped', '', fakeState)).length === 0);
check('re-scoping preserves the order and invents no id',
  reescopa(['qemu/208'], LIST).join(',') === 'qemu/208');
// 🔴 This is the check that EXECUTES the three paths, and it exists because the
// previous version was BLIND — measured by mutation in that same session.
//
// The previous version looked for the string `pvxReescopa(this.pvx.sel` inside
// each of the three methods. The mutation "pvxSetSegment returns early, before
// re-scoping" WALKED PAST it: the new `return` made the line unreachable, and
// an unreachable line is still text in the file. It is the exact sibling of the
// mistake already made with `'&token=' + localStorage`.
//
// A pin that checks for the presence of text cannot see reachability. This one
// builds a fake component, calls the three methods FOR REAL and looks at what
// happened to the selection.
const fakeComponent = () => {
  const comp = {
    pvx: { filter: '', segment: '', sel: [], tiers: {} },
    nodes: { list: LIST },
  };
  const turnOn = (nome, params) => {
    const corpo = extract(pvxJs, '41-proxmox.js', nome, params.join(',\\s*'))
      .replace('pvxGaugePct(n, which)', '(n.__pct ? n.__pct[which] : null)');
    const f = new Function(...params, corpo);
    comp[nome] = function (...a) { return f.apply(comp, a); };
  };
  turnOn('pvxNodeState', ['n']);
  turnOn('pvxFilterNodes', ['list', 'text', 'segment', 'stateOf']);
  turnOn('pvxReescopa', ['sel', 'visible']);
  turnOn('pvxNos', []);
  turnOn('pvxFilteredNodes', []);
  turnOn('pvxSetFilter', ['text']);
  turnOn('pvxSetSegment', ['key']);
  turnOn('pvxClearFilter', []);
  turnOn('pvxSelAll', []);
  return comp;
};

// path 1 — change the filter TEXT
let c = fakeComponent();
c.pvx.sel = ['lxc/201', 'lxc/202', 'qemu/208'];
c.pvxSetFilter('tipo:lxc');
check('🔴 EXECUTED: pvxSetFilter re-scopes the selection',
  c.pvx.sel.join(',') === 'lxc/201,lxc/202', c.pvx.sel.join(','));

// path 2 — change the SEGMENT of the health band
c = fakeComponent();
c.pvx.sel = ['lxc/201', 'lxc/202', 'qemu/208'];
c.pvxSetSegment('sem-credencial');
check('🔴 EXECUTED: pvxSetSegment re-scopes the selection',
  c.pvx.sel.join(',') === 'lxc/202', c.pvx.sel.join(','));
check('EXECUTED: clicking the ALREADY ACTIVE segment turns the filter off',
  (() => { c.pvxSetSegment('sem-credencial'); return c.pvx.segment === ''; })());

// path 3 — clear
c = fakeComponent();
c.pvxSetSegment('parado');
c.pvx.sel = ['qemu/208'];
c.pvxClearFilter();
check('EXECUTED: pvxClearFilter clears text AND segment',
  c.pvx.filter === '' && c.pvx.segment === '');

// "select all" — the VISIBLE ones, never the existing ones
c = fakeComponent();
c.pvxSetFilter('tipo:lxc');
c.pvxSelAll();
check('🔴 EXECUTED: "all" means the VISIBLE ones, never the existing ones',
  c.pvx.sel.join(',') === 'lxc/201,lxc/202', c.pvx.sel.join(','));
check('EXECUTED: "all" again clears everything (two exits)',
  (() => { c.pvxSelAll(); return c.pvx.sel.length === 0; })());

// ── 🔴 filter still dismissible with no dataset value — Portainer #12938 ───
check('🔴 "Showing: X ×" comes from the FILTER STATE, not from the data',
  /x-show="pvxHasFilter\(\)"/.test(index) && /aria-label="Clear filter"/.test(index),
  'Portainer #12938: the chip vanished while STILL active and the list had no way to clear it');
const hasFilter = new Function('pvx', extract(pvxJs, '41-proxmox.js', 'pvxHasFilter', '')
  .replace(/this\.pvx/g, 'pvx'));
check('🔴 a filter on a state MISSING from the dataset is still announced',
  hasFilter({ filter: '', segment: 'critico' }) === true,
  'this is exactly the case where the last critical node recovered and the chip has to survive');
check('with no filter at all, nothing is announced', hasFilter({ filter: '', segment: '' }) === false);
check('the health band IS the filter (a radiogroup, not decorative text)',
  /role="radiogroup"/.test(index) && /role="radio"/.test(index) && /pvxSetSegment\(seg\.key\)/.test(index));
check('the result count is ALWAYS in the DOM (role=status, aria-atomic)',
  /role="status" aria-atomic="true"[^>]*pvxCount\(\)/.test(index),
  'a region that is only born after the change is not announced by a screen reader');
check('the filter field has a stable id and lives outside what the refresh redraws',
  /id="pvx-filter"/.test(index) && !/x-show="pvx.loading"[\s\S]{0,400}id="pvx-filter"/.test(index));
// 🔴 THIS PIN USED TO BE "no <select> on this screen", AND IT WAS TOO GENERAL.
//
// It was born of two REAL and different defects, and it banned the whole
// category to catch both:
//   (1) a `<select>` that fires an action on @change — a screen reader walks the
//       options with the arrow keys, so going through the list EXECUTES every
//       option on the way;
//   (2) two dropdowns to choose WHICH guest, when the guest is already selected
//       in the list beside them — typing where a list already existed.
//
// The blanket ban cost dearly when it came to choosing a PARAMETER: where to
// send the copy and in what mode. There is no alternative list there, and the
// project rule is exactly the opposite — anything with a group becomes a
// dropdown, typing is the last resort. A `<select>` that only binds an x-model
// and waits for a button has neither of the two defects.
//
// The pin now asserts the TWO properties that describe the defects, instead of
// the category:
const selects = [...pvxSection.matchAll(/<select\b[\s\S]*?<\/select>/g)].map((m) => m[0]);
check('🔴 no <select> ACTS on @change — walking the options with the arrows would run every one',
  selects.every((sel) => !/@change|x-on:change/.test(sel)),
  'a screen reader walks the options one by one; an action on @change fires them all on the way');
check('🔴 no <select> picks THE NODE — the list is there for that, and it already is the selector',
  selects.every((sel) => !/(pvx\.open|pvx\.guestSel|pvxSelect|pvxOpen\()/.test(sel)),
  'two dropdowns to pick the guest already selected beside them was typing where a list existed');
check('every <select> has an accessible name (aria-label or <label>)',
  selects.every((sel) => /aria-label="/.test(sel)),
  'a select with no name is a "combo box" and nothing more to anyone on a screen reader');

// ── segments: the count stays visible, zero included ──────────────────────
const segs = new Function('list', 'stateOf', extract(pvxJs, '41-proxmox.js', 'pvxSegments', 'list, stateOf'));
const R = segs(LIST, fakeState);
// 🔴 THIS PIN USED TO LOCK THE NUMBER SIX and failed the day the band gained
// a new state (`sumiu`, for the node the hypervisor stopped listing) —
// complaining about a legitimate addition instead of checking the thing it
// exists to protect.
//
// The property is written into the pin below: the band DOES NOT SHRINK with the
// data. It draws EVERY state the component declares, always, the zeroed ones
// included — because a band that shrinks moves the click target during the
// incident. That is what is asserted now, and it holds for six, seven or twenty.
{
  const declared = [...new Set([...pvxJs.matchAll(/return '([a-z-]+)';/g)]
    .map((m) => m[1]))].filter((e) => /^(vencido|sem-credencial|critico|atencao|parado|gone|ok)$/.test(e));
  check('the band draws EVERY declared state, always',
    declared.length >= 6 && declared.every((e) => R.some((x) => x.key === e)),
    `declared: ${declared.join(', ')} — in the band: ${R.map((x) => x.key).join(', ')}`);
  check('and it draws no state nobody declares (the band invents no segment)',
    R.every((x) => declared.includes(x.key)),
    R.map((x) => x.key).join(', '));
}
check('🔴 a zeroed segment is still drawn, with its zero',
  R.some(x => x.key === 'critico' && x.n === 0),
  'a band that shrinks with the health moves the click target during the incident');
check('the segment counts match the list',
  R.find(x => x.key === 'sem-credencial').n === 1 && R.find(x => x.key === 'parado').n === 1);

// ── the confirmation ladder ───────────────────────────────────────────────
const step = new Function('action', 'howMany', extract(pvxJs, '41-proxmox.js', 'pvxStep', 'action, howMany'));
check('step 1: STARTING a node asks nothing', step('start', 1) === 1);
check('step 2: a graceful shutdown confirms', step('shutdown', 1) === 2);
check('step 3: cutting the power demands typing', step('stop', 1) === 3);
check('step 3: revoking a credential demands typing', step('revoke', 1) === 3);
check('step 3: rollback demands typing', step('rollback', 1) === 3);
check('🔴 a BULK action climbs a whole step (starting 7 already asks)',
  step('start', 7) === 2,
  'a bulk mistake is not the individual mistake repeated — it is irreversible on another scale');
check('bulk: shutting down 7 climbs to typing', step('shutdown', 7) === 3);
check('bulk: cutting the power saturates at 3 (there is no step 4)', step('stop', 7) === 3);
check('🔴 destructive does NOT share a row with restorative',
  /irreversible — you must type the node name/.test(index),
  'before this pass, "Revoke the credential" sat on the same row as "Turn on"');
check('the bulk confirmation ENUMERATES who will be affected AND who is left out',
  /THEY ARE:/.test(pvxJs) && /LEFT OUT/.test(pvxJs),
  '"7 selected" hides that two of them will be skipped in silence');

// ── a control without permission: DISABLED WITH A REASON, never gone ──────
const action = new Function('n', 'action', extract(pvxJs, '41-proxmox.js', 'pvxActionState', 'n, action'));
const semCred = { kind: 'guest', vmid: 202, status: { value: 'running' }, credential: { state: 'ausente' } };
check('🔴 no credential: the control closes WITH A REASON',
  action(semCred, 'start').can === false && /no node token in the vault/.test(action(semCred, 'start').motivo),
  'Portainer removes the button (`if (!authorized) return null`); Coolify disables it and explains');
// 🔴 The list of actions being walked has to be FULL. Measured by mutation:
// swapping the `x-for` for an empty list kept the reason expression in the file
// and the previous check passed — over a screen that showed no reason at all.
// 🔴 THIS PIN USED TO LOCK THE LITERAL LIST ['start','shutdown','stop','revogar']
// and failed the day the screen gained restart, clone and keep-a-copy —
// complaining about the NEW actions instead of checking whether they were
// covered.
//
// The right property is stronger, not looser: EVERY action the screen offers as
// a button has to appear in the list of VISIBLE reasons. That way, forgetting
// to cover a new action fails; adding a covered action does not.
{
  const offered = new Set(
    [...pvxSection.matchAll(/pvxActionState\(pvxOpenNode\(\),\s*'([a-z]+)'\)/g)].map((m) => m[1]));
  const mList = pvxSection.match(/x-for="a in \[([^\]]*)\]"[^>]*:key="'mot-'/);
  const covered = new Set(
    mList ? [...mList[1].matchAll(/'([a-z]+)'/g)].map((m) => m[1]) : []);
  const missing = [...offered].filter((a) => !covered.has(a));
  check('🔴 the reason shows up as VISIBLE TEXT, not only in title=',
    offered.size >= 5 && covered.size > 0 && missing.length === 0 &&
    /x-text="a \+ ': ' \+ pvxActionState\(pvxOpenNode\(\), a\)\.motivo"/.test(pvxSection),
    missing.length
      ? `actions with no visible reason: ${missing.join(', ')} — a disabled button takes no focus, and a hint only in title is unreachable by keyboard`
      : 'a disabled button takes no focus: a hint only on hover is unreachable by keyboard');
}
check('🔴 and the console also explains why it is closed',
  /x-text="'console: ' \+ pvxConsoleReason\(pvxOpenNode\(\)\)"/.test(pvxSection));
check('🔴 the control still exists in the HTML (there is no v-if erasing it)',
  /:disabled="!pvxActionState\(pvxOpenNode\(\),'start'\)\.can/.test(index) &&
  !/x-show="pvxActionState[^"]*can"/.test(index));
check('the host neither starts nor stops from the panel, and the screen SAYS why',
  action({ kind: 'host', vmid: 0, status: { value: 'online' }, credential: { state: 'ok' } }, 'start').can === false &&
  /physical button/.test(action({ kind: 'host', vmid: 0, status: { value: 'online' }, credential: { state: 'ok' } }, 'start').motivo));
// 🔴 The `canario` node (kind `externo`, transport `agente`) exists in the live
// inventory, and its reason is a DIFFERENT one. A wrong reason is worse than a
// missing reason: it sends the operator looking in the wrong place.
check('🔴 an EXTERNAL node gets its own reason, not the hypervisor one',
  /lab agent/.test(action({ kind: 'externo', vmid: 0, transport: 'agente', status: { value: '' }, credential: { state: 'ok' } }, 'start').motivo));
check('🔴 something already on refuses "start" — the `VM 208 already running` defect, measured',
  action({ kind: 'guest', vmid: 208, status: { value: 'running' }, credential: { state: 'ok' } }, 'start').can === false,
  'a useless order fills the trail with noise where the real failure is being looked for');
check('something already off refuses "cut the power"',
  action({ kind: 'guest', vmid: 208, status: { value: 'stopped' }, credential: { state: 'ok' } }, 'stop').can === false);
check('a stopped guest WITH a credential accepts start',
  action({ kind: 'guest', vmid: 208, status: { value: 'stopped' }, credential: { state: 'ok' } }, 'start').can === true);
check('an EXPIRED credential can still be revoked (revoking is cleanup)',
  action({ kind: 'guest', vmid: 208, status: { value: 'running' }, credential: { state: 'expirada' } }, 'revoke').can === true);
check('a MISSING credential has nothing to revoke, and the screen says so',
  action(semCred, 'revoke').can === false && /there is no credential to revoke/.test(action(semCred, 'revoke').motivo));

// ── the TWO clocks on the screen ──────────────────────────────────────────
check('🔴 the screen shows the POLLER clock (when it last tried)',
  /nodes\.poll[\s\S]{0,200}poller: checked/.test(index),
  'without it, "the node went quiet" and "the poller stopped" are the same screen');
check('🔴 and the DATA clock, separately (the age of what it managed to get)',
  /hypervisor: ' \+ pvxFormatAge/.test(index));
check('the reason the last attempt failed shows up in full',
  /nodes\.poll && nodes\.poll\.error/.test(index));
check('40-nodes.js keeps the second clock as it comes from the server',
  /this\.nodes\.poll = d\.poll \|\| null;/.test(nodesJs),
  'the age of the attempt also arrives READY — the browser subtracts no clocks');

// ── adaptive refresh, with no interval selector ───────────────────────────
const cad = new Function('pvx', extract(pvxJs, '41-proxmox.js', 'pvxCadence', '').replace(/this\.pvx/g, 'pvx'));
check('🔴 the cadence is DERIVED from the server TTL, not chosen on the screen',
  cad({ ttl: 90 }) === 30000 && cad({ ttl: 300 }) === 100000,
  'Dash0 removed the 10/30/60 s selector: the choice only spends network');
check('the cadence has a floor (an absurd TTL does not become a request storm)',
  cad({ ttl: 1 }) === 5000);
check('🔴 there is NO refresh-interval selector on the screen',
  !/refresh every|refreshInterval|refresh interval/i.test(index.slice(
    index.indexOf("currentView==='proxmox'"), index.indexOf('Deploy / Heroku-style PaaS'))));
const startPoll = extract(pvxJs, '41-proxmox.js', 'pvxStartPoll', '');
check('🔴 scrolling the table SUSPENDS the refresh',
  /this\.pvx\.rolando/.test(startPoll) && /@scroll="pvxScrolled\(\)"/.test(index),
  'without this the rows escape from under the cursor mid-read');
check('a background tab does not refresh', /document\.hidden/.test(startPoll));
check('the poll re-fetches the nodes (the 40-nodes.js timer was removed)',
  /loadNodes\(\)/.test(startPoll));
check('🔴 the scroll suspension does NOT read the browser clock',
  !/Date\.now|performance\.now/.test(pvxCode),
  'the "just this once" exception is what makes the next person compute an age here');

// ── a failure does not erase the data already on the screen ───────────────
const loadTasks = extract(pvxJs, '41-proxmox.js', 'pvxLoadTasks', '');
check('🔴 a failure fetching tasks does NOT clear the list',
  !/this\.pvx\.tasks = \[\];/.test(loadTasks),
  'clearing turned 300 ms of bad network into "no tasks in this window"');
check('a failure fetching disks does not clear either',
  !/this\.pvx\.disks = \[\];/.test(extract(pvxJs, '41-proxmox.js', 'pvxLoadDisks', '')));

// ── the skeleton, and the THREE empties ──────────────────────────────────
const vazio = new Function('pvx', 'nodes', 'list', 'filtrados',
  extract(pvxJs, '41-proxmox.js', 'pvxEmpty', '')
    .replace(/this\.pvxFilteredNodes\(\)/g, 'filtrados')
    .replace(/this\.pvxNos\(\)/g, 'list')
    .replace(/this\.pvx\./g, 'pvx.')
    .replace(/this\.nodes/g, 'nodes'));
check('🔴 empty: no permission is a diagnosis of its own',
  vazio({ forbidden: false }, { forbidden: true }, [], []) === 'sem-permissao');
check('🔴 empty: a fetch failure is another one (the lab may be perfectly fine)',
  vazio({ forbidden: false }, { forbidden: false, lastError: 'connection refused' }, [], []) === 'falhou');
check('empty: "there is nothing" is the third', vazio({ forbidden: false }, { forbidden: false }, [], []) === 'nada');
check('empty: a filter with no match is distinct from "there is nothing"',
  vazio({ forbidden: false }, { forbidden: false }, [1], []) === 'filtrado');
check('with data, there is no empty state at all',
  vazio({ forbidden: false }, { forbidden: false }, [1], [1]) === '');
check('🔴 the empty state replaces the WHOLE TABLE, header included',
  /<template x-if="!pvxEmpty\(\)">/.test(index),
  'a standing header makes a screen reader announce six columns of a table with no rows');
check('a skeleton, not a spinner, on the first load',
  /vpsm-skel/.test(index.slice(index.indexOf("currentView==='proxmox'"))) &&
  /pvx\.firstLoad/.test(index));

// ── rate: a hole rendered as a hole ──────────────────────────────────────
const rate = new Function('v', extract(pvxJs, '41-proxmox.js', 'pvxRate', 'v')
  .replace('this.pvxBytes(v)', 'String(v)'));
check('🔴 an INCOMPUTABLE rate (-1) never becomes "0 B/s"',
  rate(-1) === 'no baseline',
  '0 B/s reads as "no traffic" — which is a claim about minutes nobody watched');
check('a missing rate is an em dash, not zero', rate(null) === '—');
check('a real rate is formatted per second', rate(100000) === '100000/s');

// ── no action hidden behind hover ────────────────────────────────────────
check('🔴 no action appears only on hover (this screen opens on a phone mid-incident)',
  !/group-hover|hover:opacity-100|opacity-0 hover/.test(pvxSection));

console.log(failed === 0 ? '\nPASS — the folded screen: grafts, deliberate absence, the formatAge copy, the datastore guard in its three states, the console invariants, and the node-axis rules (states, hysteresis, ANDed filter, selection re-scoping, grey over dead data, the 3-step ladder, two clocks, three empties)'
  : `\nFAIL — ${failed} case(s)`);
process.exit(failed === 0 ? 0 : 1);
