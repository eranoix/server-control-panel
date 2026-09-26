#!/usr/bin/env node
// test-vc-dispositivos.mjs — the video call must not lock the user out because
// of ONE missing device.
//
// The report: "it isn't identifying any of my devices now". The lobby screen
// showed "No camera or microphone was found" AND empty pickers — what looked like
// two symptoms was ONE: getUserMedia({audio,video}) is all-or-nothing, so missing
// JUST the camera returns NotFoundError as if nothing existed; the lobby then
// returned before enumerating, leaving the lists empty, and the Join button
// disabled. With no camera, the user could not even join by audio.
//
// This pin asserts nothing about the text of the code: it LOADS the real engine
// in a chromium and injects hardware scenarios (mic only / camera only / neither /
// both), exercising the per-kind probe and the degradation in Call.start.
import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin RUNS the video-call engine — skipping would be faking coverage.');
  console.error('       Install it with: make tools');
  process.exit(1);
}

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };

const FONTE_VC = path.join(WEB, 'vendor', 'vpsm', 'videocall.js');
const FONTE_SHELL = path.join(WEB, 'vendor', 'vpsm', 'app', '00-shell.js');
const srcVC = fs.readFileSync(FONTE_VC, 'utf8');
const srcShell = fs.readFileSync(FONTE_SHELL, 'utf8');

// Hardware stub: decides what getUserMedia does per requested kind. Reproduces the
// real browser semantics — a COMBINED request fails if EITHER side is missing.
const STUB = (temAudio, temVideo) => `
  window.__gumCalls = [];
  const fakeTrack = (kind) => ({
    kind, enabled: true, id: kind + '-fake', label: kind + ' fake',
    stop(){}, clone(){ return fakeTrack(kind); },
    getSettings(){ return {}; }, addEventListener(){}, removeEventListener(){},
  });
  // defineProperty, not assignment: these are read-only accessors on Navigator.
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: {
    getUserMedia: async (c) => {
      window.__gumCalls.push({ audio: !!c.audio, video: !!c.video });
      const querAudio = !!c.audio, querVideo = !!c.video;
      if (querAudio && !${temAudio}) { const e = new Error('no mic'); e.name = 'NotFoundError'; throw e; }
      if (querVideo && !${temVideo}) { const e = new Error('no cam'); e.name = 'NotFoundError'; throw e; }
      const tracks = [];
      if (querAudio) tracks.push(fakeTrack('audio'));
      if (querVideo) tracks.push(fakeTrack('video'));
      return { getTracks: () => tracks, getAudioTracks: () => tracks.filter(t=>t.kind==='audio'),
               getVideoTracks: () => tracks.filter(t=>t.kind==='video'),
               removeTrack(){}, addTrack(){} };
    },
    enumerateDevices: async () => [
      ...(${temAudio} ? [{ deviceId: 'mic1', kind: 'audioinput',  label: 'Fake mic',  groupId: 'g1' }] : []),
      ...(${temVideo} ? [{ deviceId: 'cam1', kind: 'videoinput',  label: 'Fake cam',  groupId: 'g2' }] : []),
      { deviceId: 'spk1', kind: 'audiooutput', label: 'Fake output', groupId: 'g3' },
    ],
    addEventListener(){}, removeEventListener(){},
  }});
  // A real headless chromium answers 'denied' for the camera when no device
  // exists. Here the scenario is absent hardware, not a denied permission.
  Object.defineProperty(navigator, 'permissions', { configurable: true, value: { query: async () => ({ state: 'prompt' }) } });
`;

async function cenario(browser, nome, temAudio, temVideo) {
  const page = await browser.newPage();
  await page.goto('about:blank');
  await page.addInitScript(STUB(temAudio, temVideo));
  await page.goto('about:blank');
  await page.addScriptTag({ content: srcVC });
  const r = await page.evaluate(async () => {
    const probe = await window.VPSMVideoCall.probeDevicePermission();
    const devs = await window.VPSMVideoCall.listDevices();
    return { probe, devs, gum: window.__gumCalls };
  });
  await page.close();
  return r;
}

// Same browser resolution as the other pins: the playwright-core in .tools/
// downloads no browser, so it points at the cache or system chromium.
function achaNavegador() {
  const c = [];
  if (process.env.VPSM_CHROMIUM) c.push(process.env.VPSM_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse())
    c.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
  c.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const x of c) if (fs.existsSync(x)) return x;
  return null;
}
const exe = achaNavegador();
if (!exe) { console.error('FAILED: no Chromium found — skipping would be faking coverage.'); process.exit(1); }
const browser = await chromium.launch({ executablePath: exe, args: ['--no-sandbox'] });

// ── 1. Microphone only (the reported case) ───────────────────────────────
{
  const { probe, devs } = await cenario(browser, 'so-mic', true, false);
  probe.ok === true   ? ok('mic only: probe.ok (joining is possible)') : no('mic only: probe.ok=' + probe.ok + ' — this would lock the user out');
  probe.audio === true  ? ok('mic only: audio detected') : no('mic only: audio=' + probe.audio);
  probe.video === false ? ok('mic only: missing video reported') : no('mic only: video=' + probe.video);
  /s.{0,3} com .udio/i.test(probe.error || '') ? ok('mic only: the message offers joining with audio alone') : no('mic only: the message does not offer audio-only: ' + probe.error);
  devs.mics.length === 1 ? ok('mic only: enumerateDevices lists the microphone') : no('mic only: mics=' + devs.mics.length);
}

// ── 2. Camera only ───────────────────────────────────────────────────────
{
  const { probe, devs } = await cenario(browser, 'so-cam', false, true);
  probe.ok === true    ? ok('camera only: probe.ok') : no('camera only: probe.ok=' + probe.ok);
  probe.video === true ? ok('camera only: video detected') : no('camera only: video=' + probe.video);
  probe.audio === false? ok('camera only: missing audio reported') : no('camera only: audio=' + probe.audio);
  devs.cameras.length === 1 ? ok('camera only: enumerateDevices lists the camera') : no('camera only: cameras=' + devs.cameras.length);
}

// ── 3. Neither one — the only case that blocks ───────────────────────────
{
  const { probe } = await cenario(browser, 'nada', false, false);
  probe.ok === false ? ok('nothing at all: probe.ok=false (it really does block)') : no('nothing at all: probe.ok=' + probe.ok);
  (probe.audio === false && probe.video === false) ? ok('nothing at all: both sides reported missing') : no('nothing at all: audio=' + probe.audio + ' video=' + probe.video);
}

// ── 4. Everything present — ONE prompt only (no regression on the happy path)
{
  const { probe, gum } = await cenario(browser, 'all', true, true);
  (probe.ok && probe.audio && probe.video) ? ok('all ok: complete probe') : no('all ok: ' + JSON.stringify(probe));
  gum.length === 1 ? ok('all ok: a single getUserMedia (no extra prompt)') : no('all ok: ' + gum.length + ' getUserMedia calls — duplicated prompt');
}

// ── 5. Engine degradation: the combined request fails, it joins audio-only
{
  const page = await browser.newPage();
  await page.addInitScript(STUB(true, false));
  await page.goto('about:blank');
  await page.addScriptTag({ content: srcVC });
  const r = await page.evaluate(async () => {
    const eventos = [];
    let erro = '';
    try {
      await window.VPSMVideoCall.connect({
        roomId: 'x', token: 't', displayName: 'teste',
        videosEl: document.createElement('div'),
        onState: (ev) => eventos.push(ev),
      });
    } catch (e) { erro = e.message; }
    return { eventos, erro, gum: window.__gumCalls };
  });
  await page.close();
  const degradou = r.eventos.some(e => e.type === 'devices-degraded');
  degradou ? ok('engine: emitted devices-degraded') : no('engine: no devices-degraded — events=' + JSON.stringify(r.eventos.map(e=>e.type)));
  const nota = (r.eventos.find(e => e.type === 'devices-degraded') || {}).note || '';
  /c.mera/i.test(nota) ? ok('engine: the note says the camera was missing ("' + nota + '")') : no('engine: unexpected note: ' + nota);
  // The last gUM attempt has to have been audio-without-video.
  const ultima = r.gum[r.gum.length - 1] || {};
  (ultima.audio === true && ultima.video === false) ? ok('engine: fell back to audio-only') : no('engine: the last attempt was ' + JSON.stringify(ultima));
  // The final error must NOT be the getUserMedia one — it has to have got past it.
  !/getUserMedia|c.mera ou microfone/i.test(r.erro) ? ok('engine: got past getUserMedia (it failed later, at the signalling)') : no('engine: stuck at getUserMedia: ' + r.erro);
}

// ── 6. Counter-check of the original bug, in the lobby source ────────────
// The `return` between the probe and vcRefreshDevices was what emptied the lists.
// If it comes back, the symptom comes back whole — and none of the runtime tests
// above would catch it, because the lobby lives in 00-shell and not in the engine.
{
  const i = srcShell.indexOf('async vcLobbyOpen(');
  const j = srcShell.indexOf('await this.vcRefreshDevices();', i);
  const trecho = i >= 0 && j > i ? srcShell.slice(i, j) : '';
  trecho ? ok('lobby: vcLobbyOpen calls vcRefreshDevices') : no('lobby: vcRefreshDevices is gone from vcLobbyOpen');
  !/\n\s+return;\n/.test(trecho) ? ok('lobby: no return before enumerating the devices') : no('lobby: the return that emptied the pickers is back');
  /lobbyCaps/.test(srcShell) ? ok('lobby: per-kind capabilities (lobbyCaps) present') : no('lobby: lobbyCaps is gone');
}

// ── 7. The lobby notice, RENDERED ────────────────────────────────────────
// A coloured band with no message at all went live once (an empty lobbyError
// plus an x-show that only tested for "truthy"). Here the real markup and the
// real CSS are mounted with the real Alpine and measured.
{
  const tail = fs.readFileSync(path.join(WEB, 'tailwind.css'), 'utf8');
  const alpine = fs.readFileSync(path.join(WEB, 'vendor', 'alpine', 'alpine.min.js'), 'utf8');
  const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
  const i = html.indexOf('<div x-show="(videocall.lobbyError||\'\').trim()"');
  const j = html.indexOf('</div>', html.indexOf('Test again')) + 6;
  const banner = i >= 0 ? html.slice(i, j) : '';
  const ci = html.indexOf('.vc-lobby-aviso {');
  const css = ci >= 0 ? html.slice(ci, html.indexOf('}', html.indexOf('.acao:hover', ci)) + 1) : '';
  const vars = ':root{--surface-1:#111827;--surface-2:#1f2937;--surface-3:#374151;--focus:#2563eb;'
             + '--text-primary:#e5e7eb;--text-muted:#9ca3af}body{background:#0b1220;margin:0;padding:16px}'
             + '[x-cloak]{display:none!important}';
  if (!banner.includes('vc-lobby-aviso') || !css) {
    no('notice: the lobby notice markup/CSS was not found in index.html');
  } else {
    const page = await browser.newPage({ viewport: { width: 760, height: 260 } });
    const casos = [
      { n: 'with a message', err: 'No camera was found. You can join anyway — with audio only.', caps: { audio: true, video: false }, ver: true,  sev: 'is-aviso' },
      { n: 'both missing', err: 'No usable camera or microphone on this computer.', caps: { audio: false, video: false }, ver: true, sev: 'is-erro' },
      { n: 'empty message',  err: '',    caps: { audio: true, video: true }, ver: false },
      { n: 'blank message', err: '   ', caps: { audio: true, video: true }, ver: false },
      { n: 'tab on an old bundle', err: 'Microphone in use by another program.', caps: undefined, ver: true, sev: 'is-aviso' },
    ];
    for (const c of casos) {
      const estado = JSON.stringify({ lobbyError: c.err, lobbyCaps: c.caps, lobbyForRoomId: 'r', lobbyForPassphrase: '' });
      await page.setContent('<style>' + tail + '</style><style>' + vars + css + '</style>'
        + "<div x-data='{ videocall: " + estado + ", vcLobbyOpen(){} }'>" + banner + '</div>');
      await page.addScriptTag({ content: alpine });
      await page.waitForTimeout(200);
      const r = await page.evaluate(() => {
        const el = document.querySelector('.vc-lobby-aviso');
        if (!el) return { existe: false };
        return { existe: true, display: getComputedStyle(el).display, classe: el.className,
                 texto: ((el.querySelector('.txt') || {}).textContent || '').trim(),
                 altura: Math.round(el.getBoundingClientRect().height),
                 botao: !!el.querySelector('.acao') };
      });
      const visivel = r.existe && r.display !== 'none';
      if (!c.ver) {
        !visivel ? ok('notice/' + c.n + ': the band does not appear') : no('notice/' + c.n + ': an empty band of ' + r.altura + 'px went live');
        continue;
      }
      if (!visivel) { no('notice/' + c.n + ': the band vanished'); continue; }
      r.texto ? ok('notice/' + c.n + ': has text') : no('notice/' + c.n + ': no text');
      r.botao ? ok('notice/' + c.n + ': has "Test again"') : no('notice/' + c.n + ': no retry button');
      r.altura <= 60 ? ok('notice/' + c.n + ': compact (' + r.altura + 'px)') : no('notice/' + c.n + ': ' + r.altura + 'px — it became a block again');
      r.classe.includes(c.sev) ? ok('notice/' + c.n + ': severity ' + c.sev) : no('notice/' + c.n + ': ' + r.classe + ' (expected ' + c.sev + ')');
    }
    await page.close();
  }
}

// ── 8. The redesigned lobby, MEASURED ────────────────────────────────────
// The control bar inside the frame only works if the three pills fit on one line;
// the labels may only exist for the screen reader (Tailwind's .sr-only gets purged
// and they come back as visible text); and the title has to be legible in BOTH
// themes (.fm-modal-head h3 pins #fff).
{
  const tail = fs.readFileSync(path.join(WEB, 'tailwind.css'), 'utf8');
  const alpine = fs.readFileSync(path.join(WEB, 'vendor', 'alpine', 'alpine.min.js'), 'utf8');
  const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
  const estilos = [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)].map(m => m[1]).join('\n');
  const modal = html.slice(html.indexOf('<!-- Modal: Lobby'), html.indexOf('<!-- Modal: Settings'));
  const lum = (c) => { const v = c.match(/\d+/g).map(Number).map(x => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
                       return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2]; };
  const razao = (a, b) => { const x = [lum(a), lum(b)].sort((m, n) => n - m); return (x[0] + 0.05) / (x[1] + 0.05); };
  const monta = async (page, caps, tema) => {
    const st = { lobbyOpen: true, lobbyBusy: false, lobbyError: '', lobbyCaps: caps, lobbyMicLevel: 40, lobbyTone: false,
                 lobbyForRoomId: 'r', lobbyForPassphrase: '', lobbySkipNext: false, localMirror: true,
                 selectedRoom: { name: 'Daily — platform team' }, sinkIdSupported: true,
                 selectedDevices: { camera: 'default', mic: 'default', speaker: 'default' },
                 devices: { cameras: [], mics: [], speakers: [] } };
    await page.setContent('<style>' + tail + '</style><style>' + estilos + '</style><body style="margin:0">'
      + "<div x-data='{ videocall: " + JSON.stringify(st) + ", vcLobbyCancel(){},vcLobbyConfirm(){},vcLobbyOpen(){},"
      + "vcLobbyToggleSkip(){},vcLobbyChangeCamera(){},vcLobbyChangeMic(){},vcLobbyChangeSpeaker(){},vcLobbyTestSpeaker(){} }'>"
      + modal + '</div></body>');
    await page.evaluate((t) => { if (t === 'light') document.documentElement.setAttribute('data-theme', 'light'); }, tema);
    await page.addScriptTag({ content: alpine });
    await page.waitForTimeout(250);
  };
  const page = await browser.newPage({ viewport: { width: 1200, height: 1000 } });

  await monta(page, { audio: true, video: true }, 'dark');
  const r = await page.evaluate(() => {
    const pills = [...document.querySelectorAll('.vc-pill')].filter(e => e.offsetParent !== null);
    const tops = [...new Set(pills.map(e => Math.round(e.getBoundingClientRect().top)))];
    const rotulos = [...document.querySelectorAll('.vc-sala label')].map(l => Math.round(l.getBoundingClientRect().width));
    const nivel = document.querySelector('.vc-pill .nivel');
    const mic = nivel && nivel.parentElement;
    return { n: pills.length, linhas: tops.length,
             rotuloVisivel: rotulos.filter(w => w > 2).length,
             nivelLargura: nivel ? nivel.getBoundingClientRect().width : -1,
             micLargura: mic ? mic.getBoundingClientRect().width : -1,
             entrarOff: document.querySelector('.vc-btn-join').disabled };
  });
  r.n === 3 ? ok('lobby: 3 device pills') : no('lobby: ' + r.n + ' pills');
  r.linhas === 1 ? ok('lobby: control bar on a single line') : no('lobby: pills across ' + r.linhas + ' lines — it is no longer a bar');
  r.rotuloVisivel === 0 ? ok('lobby: labels for the screen reader only') : no('lobby: ' + r.rotuloVisivel + ' label(s) rendering as text (was .sr-only purged?)');
  (r.nivelLargura > 0 && r.nivelLargura < r.micLargura) ? ok('lobby: the mic level fills the pill (' + Math.round(r.nivelLargura) + '/' + Math.round(r.micLargura) + 'px)')
                                                        : no('lobby: the meter inside the pill does not follow the level (' + r.nivelLargura + ')');
  r.entrarOff === false ? ok('lobby: Join enabled with both devices') : no('lobby: Join disabled for no reason');

  await monta(page, { audio: true, video: false }, 'dark');
  const so = await page.evaluate(() => ({
    off: document.querySelectorAll('.vc-pill.is-off').length,
    entrarOff: document.querySelector('.vc-btn-join').disabled,
    rotulo: document.querySelector('.vc-btn-join span').textContent.trim(),
  }));
  so.off === 1 ? ok('lobby: the camera pill is marked as missing') : no('lobby: ' + so.off + ' pills marked is-off');
  so.entrarOff === false ? ok('lobby: joining with audio only is possible') : no('lobby: Join blocked even with a microphone');
  /audio/i.test(so.rotulo) ? ok('lobby: the button says "' + so.rotulo + '"') : no('lobby: the button says "' + so.rotulo + '" without warning that it is audio only');

  await monta(page, { audio: false, video: false }, 'dark');
  const nada2 = await page.evaluate(() => document.querySelector('.vc-btn-join').disabled);
  nada2 === true ? ok('lobby: Join disabled with no device at all') : no('lobby: Join clickable with neither camera nor microphone');

  for (const tema of ['dark', 'light']) {
    await monta(page, { audio: true, video: true }, tema);
    const c = await page.evaluate(() => {
      const t = document.getElementById('dlg-vc-lobby-title');
      return { cor: getComputedStyle(t).color, fundo: getComputedStyle(t.closest('.fm-modal')).backgroundColor };
    });
    const cr = razao(c.cor, c.fundo);
    cr >= 4.5 ? ok('lobby: title legible in the ' + tema + ' theme (' + cr.toFixed(1) + ':1)')
              : no('lobby: title at contrast ' + cr.toFixed(2) + ':1 in theme ' + tema);
  }
  await page.close();
}

// ── 9. The in-call devices modal ─────────────────────────────────────────
// The same language as the lobby, but over the modal surface — the dark glass of
// the pill only works over video, and the test button used translucent white,
// which disappears in the light theme.
{
  const tail = fs.readFileSync(path.join(WEB, 'tailwind.css'), 'utf8');
  const alpine = fs.readFileSync(path.join(WEB, 'vendor', 'alpine', 'alpine.min.js'), 'utf8');
  const html = fs.readFileSync(path.join(WEB, 'index.html'), 'utf8');
  const estilos = [...html.matchAll(/<style>([\s\S]*?)<\/style>/g)].map(m => m[1]).join('\n');
  const modal = html.slice(html.indexOf('<!-- Modal: Settings'), html.indexOf('<!-- Modal: E2EE'));
  const lum = (c) => { const v = c.match(/\d+/g).map(Number).map(x => { x /= 255; return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4); });
                       return 0.2126 * v[0] + 0.7152 * v[1] + 0.0722 * v[2]; };
  const razao = (a, b) => { const x = [lum(a), lum(b)].sort((m, n) => n - m); return (x[0] + 0.05) / (x[1] + 0.05); };
  const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
  for (const tema of ['dark', 'light']) {
    const st = { settingsOpen: true, settingsMicLevel: 44, settingsTone: false, sinkIdSupported: true,
                 selectedDevices: { camera: 'default', mic: 'default', speaker: 'default' },
                 devices: { cameras: [], mics: [], speakers: [] } };
    await page.setContent('<style>' + tail + '</style><style>' + estilos + '</style><body style="margin:0">'
      + "<div x-data='{ videocall: " + JSON.stringify(st) + ", vcSettingsClose(){},vcSettingsChangeCamera(){},"
      + "vcSettingsChangeMic(){},vcSettingsChangeSpeaker(){},vcSettingsTestSpeaker(){} }'>" + modal + '</div></body>');
    await page.evaluate((t) => { if (t === 'light') document.documentElement.setAttribute('data-theme', 'light'); }, tema);
    await page.addScriptTag({ content: alpine });
    await page.waitForTimeout(250);
    const r = await page.evaluate(() => {
      const linhas = [...document.querySelectorAll('.vc-pill.is-linha')].filter(e => e.offsetParent !== null);
      const nivel = document.querySelector('.vc-pill.is-linha .nivel');
      const teste = document.querySelector('.vc-pill.is-linha .teste');
      const rot = document.querySelector('.vc-pill label.rotulo');
      const tit = document.getElementById('dlg-vc-settings-title');
      const cs = linhas[0] ? getComputedStyle(linhas[0]) : null;
      const ct = teste ? getComputedStyle(teste) : null;
      return { n: linhas.length,
               nivelPct: nivel && linhas[1] ? nivel.getBoundingClientRect().width / linhas[1].getBoundingClientRect().width : -1,
               temTeste: !!teste, testeBorda: ct ? ct.borderTopColor : '', testeFundo: ct ? ct.backgroundColor : '',
               rotuloVisivel: rot ? rot.getBoundingClientRect().width > 2 : false,
               rotuloMaiusc: rot ? getComputedStyle(rot).textTransform : '',
               fundoLinha: cs ? cs.backgroundColor : '', corLinha: cs ? cs.color : '',
               corTit: tit ? getComputedStyle(tit).color : '', fundoModal: getComputedStyle(document.querySelector('.fm-modal')).backgroundColor };
    });
    if (tema === 'dark') {
      r.n === 3 ? ok('in-call: 3 device lines') : no('in-call: ' + r.n + ' lines');
      (r.nivelPct > 0.3 && r.nivelPct < 0.6) ? ok('in-call: the mic level fills the line (' + Math.round(r.nivelPct * 100) + '%)')
                                             : no('in-call: meter at ' + Math.round(r.nivelPct * 100) + '% for a level of 44');
      r.temTeste ? ok('in-call: the output test button on the line itself') : no('in-call: the output test is gone');
      r.rotuloVisivel ? ok('in-call: label visible (mic and output carry the same device name)') : no('in-call: label invisible — mic and output cannot be told apart');
      r.rotuloMaiusc === 'none' ? ok('in-call: label without the uppercase from .fm-modal-body label') : no('in-call: label with text-transform:' + r.rotuloMaiusc);
    }
    const crT = razao(r.corTit, r.fundoModal);
    crT >= 4.5 ? ok('in-call: title legible in the ' + tema + ' theme (' + crT.toFixed(1) + ':1)') : no('in-call: title ' + crT.toFixed(2) + ':1 in theme ' + tema);
    const crL = razao(r.corLinha, r.fundoLinha);
    crL >= 4.5 ? ok('in-call: line text legible in the ' + tema + ' theme (' + crL.toFixed(1) + ':1)') : no('in-call: line ' + crL.toFixed(2) + ':1 in theme ' + tema);
    const crB = razao(r.testeBorda, r.testeFundo);
    // The border has to stand out from the button's own background, or it vanishes.
    (r.testeBorda !== r.testeFundo) ? ok('in-call: the test button is visible in theme ' + tema) : no('in-call: the test button is invisible in the ' + tema + ' theme (border == background)');
  }
  await page.close();
}

await browser.close();
console.log('\n' + (fail ? '✗' : '✓') + ' ' + pass + ' passed, ' + fail + ' failed');
process.exit(fail ? 1 : 0);
