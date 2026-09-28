#!/usr/bin/env node
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(ROOT, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(ROOT, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FAILED: playwright-core missing from .tools/. This pin RUNS the call; skipping would be faking coverage.');
  console.error('       Install with: make tools');
  process.exit(1);
}

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const near = (v, target, tol) => Math.abs(v - target) <= tol;

const srcVC = fs.readFileSync(path.join(WEB, 'vendor', 'panel', 'videocall.js'), 'utf8');
const srcSTT = fs.readFileSync(path.join(WEB, 'vendor', 'panel', 'stt.js'), 'utf8');

function writeTone(arq) {
  const sr = 48000, seg = 4, n = sr * seg, amp = Math.pow(10, -10 / 20);
  const b = Buffer.alloc(44 + n * 2);
  b.write('RIFF', 0); b.writeUInt32LE(36 + n * 2, 4); b.write('WAVE', 8);
  b.write('fmt ', 12); b.writeUInt32LE(16, 16); b.writeUInt16LE(1, 20); b.writeUInt16LE(1, 22);
  b.writeUInt32LE(sr, 24); b.writeUInt32LE(sr * 2, 28); b.writeUInt16LE(2, 32); b.writeUInt16LE(16, 34);
  b.write('data', 36); b.writeUInt32LE(n * 2, 40);
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(Math.sin(2 * Math.PI * 440 * i / sr) * amp * 32767), 44 + i * 2);
  fs.writeFileSync(arq, b);
}

function findBrowser() {
  const c = [];
  if (process.env.PANEL_CHROMIUM) c.push(process.env.PANEL_CHROMIUM);
  const cache = '/root/.cache/ms-playwright';
  if (fs.existsSync(cache)) for (const d of fs.readdirSync(cache).filter((x) => x.startsWith('chromium-')).sort().reverse())
    c.push(path.join(cache, d, 'chrome-linux64', 'chrome'));
  c.push('/usr/bin/google-chrome', '/usr/bin/chromium-browser', '/usr/bin/chromium');
  for (const x of c) if (fs.existsSync(x)) return x;
  return null;
}

const INIT = `
  class FakeWS {
    constructor(url) {
      this.url = url; this.readyState = 0; this.binaryType = 'blob';
      window.__ws = this;
      setTimeout(() => { this.readyState = 1; this.onopen && this.onopen(); window.__wsOut(JSON.stringify({ type: '__hello' })); }, 0);
    }
    send(d) { window.__wsOut(String(d)); }
    close() { this.readyState = 3; }
  }
  FakeWS.CONNECTING = 0; FakeWS.OPEN = 1; FakeWS.CLOSING = 2; FakeWS.CLOSED = 3;
  window.WebSocket = FakeWS;
  // Simulated stall: with __lockFirstPC the tab's first RTCPeerConnection
  // cannot gather a local candidate (relay without TURN), so ICE stays in
  // 'new' with zero local candidates, as seen in real runs.
  window.__pcsCreated = 0;
  const RPC0 = window.RTCPeerConnection;
  window.RTCPeerConnection = function (cfg) {
    window.__pcsCreated++;
    if (window.__lockFirstPC && window.__pcsCreated === 1) cfg = Object.assign({}, cfg, { iceServers: [], iceTransportPolicy: 'relay' });
    return new RPC0(cfg);
  };
  window.RTCPeerConnection.prototype = RPC0.prototype;
  window.__wsIn = (s) => { const w = window.__ws; if (w && w.onmessage) w.onmessage({ data: s }); };
  // STT engine stub: records the stream the call hands to transcription.
  window.__sttStarts = [];
  window.PanelSTT = {
    start(o) { window.__sttStarts.push(o.stream); return { stop() {} }; },
    setBackend() {}, probeWhisperLocal: async () => false,
  };
  // dBFS of a track over an interval: MEDIAN of per-frame RMS (~43 ms). The
  // median discards frames zeroed by audio-thread underruns (headless under
  // load), which would drag a plain mean 2-3 dB down.
  window.__levelDb = async (track, ms) => {
    const ctx = new AudioContext();
    const src = ctx.createMediaStreamSource(new MediaStream([track]));
    const an = ctx.createAnalyser(); an.fftSize = 2048; src.connect(an);
    const buf = new Float32Array(an.fftSize);
    const frames = [];
    const end = performance.now() + ms;
    await new Promise((r) => setTimeout(r, 150));
    while (performance.now() < end) {
      an.getFloatTimeDomainData(buf);
      let s = 0; for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i];
      const rms = Math.sqrt(s / buf.length);
      frames.push(rms > 0 ? 20 * Math.log10(rms) : -120);
      await new Promise((r) => setTimeout(r, 40));
    }
    ctx.close();
    if (!frames.length) return -120;
    frames.sort((a, b) => a - b);
    return frames[Math.floor(frames.length / 2)];
  };
  // dB difference between two tracks measured TOGETHER (same AudioContext,
  // same frames): median of (b - a) per frame. An underrun hits both at once
  // and cancels out; measuring them one after the other left ~1 dB of noise,
  // enough to hide the 1.7 dB makeup gain.
  window.__diffDb = async (a, b, ms) => {
    const ctx = new AudioContext();
    const mk = (t) => { const an = ctx.createAnalyser(); an.fftSize = 2048; ctx.createMediaStreamSource(new MediaStream([t])).connect(an); return an; };
    const aa = mk(a), ab = mk(b);
    const ba = new Float32Array(2048), bb = new Float32Array(2048);
    const db = (buf) => { let s = 0; for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i]; const r = Math.sqrt(s / buf.length); return r > 0 ? 20 * Math.log10(r) : -120; };
    const difs = [], levelsA = [];
    await new Promise((r) => setTimeout(r, 150));
    const end = performance.now() + ms;
    while (performance.now() < end) {
      aa.getFloatTimeDomainData(ba); ab.getFloatTimeDomainData(bb);
      const x = db(ba), y = db(bb);
      if (x > -90 && y > -90) { difs.push(y - x); levelsA.push(x); }
      await new Promise((r) => setTimeout(r, 40));
    }
    ctx.close();
    const med = (v) => { if (!v.length) return NaN; v.sort((p, q) => p - q); return v[Math.floor(v.length / 2)]; };
    return { diff: med(difs), a: med(levelsA) };
  };
  // Mic reference: own unprocessed capture (the same tone file that feeds the call).
  window.__refMic = async () => (await navigator.mediaDevices.getUserMedia({ audio: { noiseSuppression: false, echoCancellation: false, autoGainControl: false } })).getAudioTracks()[0];
  window.__remote = () => {
    const v = document.querySelector('video[data-vc-peer]');
    const s = v && v.srcObject;
    return s && s.getAudioTracks()[0];
  };
`;

const exe = findBrowser();
if (!exe) { console.error('FAILED: no Chromium found; skipping would be faking coverage.'); process.exit(1); }
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'vc-micvol-'));
const tom = path.join(tmp, 'tone.wav');
const PAGE = 'http://localhost:9/vc';
writeTone(tom);

const browser = await chromium.launch({
  executablePath: exe,
  args: ['--no-sandbox', '--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream',
         '--use-file-for-fake-audio-capture=' + tom, '--autoplay-policy=no-user-gesture-required',
         '--disable-features=WebRtcHideLocalIpsWithMdns'],
});
let ctx, pages, order, queue;
async function delivery(id, obj) {
  const p = pages[id];
  if (p) await p.evaluate((s) => window.__wsIn(s), JSON.stringify(obj)).catch(() => {});
}
async function open(id, lock) {
  const page = await ctx.newPage();
  if (lock) await page.addInitScript('window.__lockFirstPC = true;');
  page.on('pageerror', (e) => console.log('  [' + id + ' pageerror] ' + e.message));
  await page.exposeFunction('__wsOut', (s) => { queue = queue.then(() => relay(id, s)).catch(() => {}); });
  const relay = async (id, s) => {
    let m; try { m = JSON.parse(s); } catch { return; }
    if (m.type === '__hello') {
      const others = order.filter((x) => x !== id);
      order.push(id);
      await delivery(id, { type: 'joined', payload: { peer_id: id, peers: others.map((o) => ({ id: o, user: o })) } });
      for (const o of others) await delivery(o, { type: 'peer-joined', from: id, payload: JSON.stringify({ user: id }) });
      return;
    }
    if (m.type === 'ping') { await delivery(id, { type: 'pong' }); return; }
    m.from = id;
    if (m.to) await delivery(m.to, m);
    else for (const o of order) if (o !== id) await delivery(o, m);
  };
  await page.addInitScript(INIT);
  await page.goto(PAGE);
  await page.addScriptTag({ content: srcVC });
  pages[id] = page;
  return page;
}
async function connect(page, id, extra) {
  return page.evaluate(async ({ id, extra }) => {
    const el = document.createElement('div'); document.body.appendChild(el);
    window.__events = [];
    await window.PanelVideoCall.connect(Object.assign({
      roomId: 'room', token: 't', displayName: id, videosEl: el, quality: 'eco',
      onState: (ev) => window.__events.push(ev), onError: (e) => window.__events.push({ type: 'error', e }),
    }, extra));
  }, { id, extra });
}

const SEM_PROC = { noiseSuppression: false, echoCancellation: false, autoGainControl: false };

async function buildCall(lock) {
  if (ctx) await ctx.close().catch(() => {});
  ctx = await browser.newContext();
  await ctx.route(PAGE, (r) => r.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body></body></html>' }));
  pages = {}; order = []; queue = Promise.resolve();
  const A = await open('A', lock);
  const B = await open('B');
  await connect(A, 'A', { micGain: 2, micProcessing: SEM_PROC });
  await connect(B, 'B', { micProcessing: SEM_PROC });
  const t0 = Date.now();
  while (Date.now() - t0 < 20000) {
    const db = await B.evaluate(() => { const t = window.__remote(); return t ? window.__levelDb(t, 600) : -120; });
    if (db > -40) return { A, B, seg: (Date.now() - t0) / 1000 };
  }
  return null;
}

{
  const m = await buildCall(true);
  const pcs = m ? await m.A.evaluate(() => window.__pcsCreated) : 0;
  m ? ok(`recovery: A's stalled connection reconnected by itself in ${m.seg.toFixed(1)}s`) : no('recovery: A\'s stalled connection did not reconnect within 20s');
  pcs >= 2 ? ok(`recovery: A recreated the RTCPeerConnection (${pcs} created)`) : no('recovery: A did not recreate the RTCPeerConnection (' + pcs + ')');
}

const mounted = await buildCall(false);
if (!mounted) {
  no('call: A\'s audio never reached B within 20s');
  await browser.close(); fs.rmSync(tmp, { recursive: true, force: true });
  console.log(`\n${pass} PASS, ${fail} FAIL`); process.exit(1);
}
const { A, B } = mounted;
ok(`call: real WebRTC between the tabs, A's audio sounding in B after ${mounted.seg.toFixed(1)}s`);

const received = () => B.evaluate(() => window.__levelDb(window.__remote(), 2500));
const aLocal = (fn, arg) => A.evaluate(fn, arg);

{
  const r = await aLocal(async () => {
    const ref = await window.__refMic();
    const out = await window.__diffDb(ref, document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0], 1200);
    ref.stop();
    return out;
  });
  near(r.diff, 6.02, 0.5)
    ? ok(`saved volume: 200% applied from connect (sent +${r.diff.toFixed(2)} dB over the mic)`)
    : no(`saved volume: sent ${r.diff.toFixed(2)} dB over the mic at connect, expected +6.0 (200%)`);
}
await new Promise((r) => setTimeout(r, 3000));
const at200 = await received();
await aLocal(() => window.PanelVideoCall.setMicGain(1));
const at100 = await received();
near(at200 - at100, 6.02, 1.5)
  ? ok(`live volume: 200% arrives ${(at200 - at100).toFixed(1)} dB above 100% on the other side`)
  : no(`live volume: 200% vs 100% gave ${(at200 - at100).toFixed(1)} dB at the receiver (expected ~6)`);

await aLocal(() => window.PanelVideoCall.setMicGain(0.5));
const at50 = await received();
near(at100 - at50, 6.02, 1.5)
  ? ok(`live volume: 50% arrives ${(at100 - at50).toFixed(1)} dB below 100%`)
  : no(`live volume: 50% vs 100% gave ${(at100 - at50).toFixed(1)} dB (expected ~6)`);

await aLocal(() => window.PanelVideoCall.setMicGain(4));
await new Promise((r) => setTimeout(r, 400));
const lim = await aLocal(async () => {
  let limiting = false, peak = 0;
  for (let i = 0; i < 20; i++) {
    const r = window.PanelVideoCall.getMicLevel();
    if (r) { limiting = limiting || r.limiting; peak = Math.max(peak, r.peak); }
    await new Promise((res) => setTimeout(res, 50));
  }
  return { limiting, peak };
});
lim.limiting ? ok('limiter: 400% on a strong signal lights the "limiting" warning') : no('limiter: getMicLevel().limiting never became true at 400%');
lim.peak < 1.0 ? ok(`limiter: peak sent ${lim.peak.toFixed(2)} < 1.0 (no clipping)`) : no(`limiter: peak sent ${lim.peak.toFixed(2)}, clipping`);
await aLocal(() => window.PanelVideoCall.setMicGain(2));

const beforeSwap = await received();
const swapped = await aLocal(() => window.PanelVideoCall.setMicDevice('default'));
const afterSwap = await received();
swapped ? ok('mic switch: setMicDevice completed') : no('mic switch: setMicDevice failed');
near(afterSwap, beforeSwap, 1.5)
  ? ok(`mic switch: volume kept on the other side (${beforeSwap.toFixed(1)} → ${afterSwap.toFixed(1)} dBFS)`)
  : no(`mic switch: received level went from ${beforeSwap.toFixed(1)} to ${afterSwap.toFixed(1)} dBFS, volume lost in the switch`);

const proc = await aLocal(() => window.PanelVideoCall.setMicProcessing({ echoCancellation: true }));
proc && proc.echoCancellation === true && proc.noiseSuppression === false
  ? ok('processing: setMicProcessing applied only the requested key')
  : no('processing: unexpected return ' + JSON.stringify(proc));
const withEcho = await received();
withEcho > -40 ? ok(`processing: audio keeps arriving after reopening the mic (${withEcho.toFixed(1)} dBFS)`) : no(`processing: audio gone after setMicProcessing (${withEcho.toFixed(1)} dBFS)`);
await aLocal((p) => window.PanelVideoCall.setMicProcessing(p), SEM_PROC);

{
  await aLocal(() => window.PanelVideoCall.setMicGain(1));
  await aLocal(() => { window.PanelVideoCall.setSubtitles(true, { backend: 'web-speech', lang: 'en-US' }); });
  await new Promise((r) => setTimeout(r, 400));
  const n = await aLocal(async () => {
    const raw = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
    const sent = document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0];
    return window.__diffDb(raw, sent, 1200);
  });
  await aLocal(() => { window.PanelVideoCall.setSubtitles(false); });
  await new Promise((r) => setTimeout(r, 600));
  near(n.diff, 0, 0.3)
    ? ok(`neutral: 100% sends the same level as the mic (${n.diff >= 0 ? '+' : ''}${n.diff.toFixed(2)} dB)`)
    : no(`neutral: at 100% the sent level was ${n.diff.toFixed(2)} dB off the mic (limiter makeup gain?)`);
  await aLocal(() => window.PanelVideoCall.setMicGain(2));
}

await aLocal(() => { window.PanelVideoCall.setSubtitles(true, { backend: 'web-speech', lang: 'en-US' }); });
await new Promise((r) => setTimeout(r, 400));
const stt = await aLocal(async () => {
  const s = window.__sttStarts[window.__sttStarts.length - 1];
  const t = s && s.getAudioTracks()[0];
  const sent = document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0];
  const par = await window.__diffDb(t, sent, 1200);
  return { has: !!t, same: t === sent, dbStt: par.a, diff: par.diff };
});
stt.has ? ok('stt: the call handed a track to transcription') : no('stt: no track handed to STT');
!stt.same ? ok('stt: the transcription track is NOT the sent one (volume does not apply)') : no('stt: transcription reading the sent track');
near(stt.diff, 6.02, 0.5)
  ? ok(`stt: with volume at 200%, transcription reads the raw mic (sent +${stt.diff.toFixed(2)} dB over it)`)
  : no(`stt: sent ${stt.diff.toFixed(2)} dB over the transcription track, expected +6.0`);
near(stt.dbStt, -13, 2)
  ? ok('stt: raw level matches the mic tone (~-13 dBFS RMS)')
  : no(`stt: raw level ${stt.dbStt.toFixed(1)} dBFS, expected ~-13`);

const mute = await aLocal(async () => {
  window.PanelVideoCall.setMuted(true);
  const t = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
  await new Promise((r) => setTimeout(r, 300));
  const lvl = window.PanelVideoCall.getMicLevel();
  const r = { enabled: t.enabled, level: lvl ? lvl.level : -1 };
  window.PanelVideoCall.setMuted(false);
  r.recovered = t.enabled;
  return r;
});
mute.enabled === false ? ok('mute: the transcription track goes off') : no('mute: transcription track stayed on, it would transcribe muted speech');
mute.level === 0 ? ok('mute: the outgoing level meter drops to zero') : no('mute: meter at ' + mute.level + ' with the mic muted');
mute.recovered === true ? ok('mute: unmuting turns the transcription track back on') : no('mute: unmuting did not turn the transcription track back on');

const restart = await aLocal(async () => {
  const before = window.__sttStarts.length;
  const old = window.__sttStarts[before - 1].getAudioTracks()[0];
  await window.PanelVideoCall.setMicDevice('default');
  await new Promise((r) => setTimeout(r, 300));
  const fresh = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
  return { newOnes: window.__sttStarts.length - before, oldState: old.readyState, newState: fresh.readyState, equal: old === fresh };
});
(restart.newOnes >= 1 && !restart.equal && restart.newState === 'live')
  ? ok('stt: a mic switch restarts transcription on the new (live) track')
  : no('stt: after the mic switch transcription stayed on the ' + restart.oldState + ' track (' + JSON.stringify(restart) + ')');
await aLocal(() => { window.PanelVideoCall.setSubtitles(false); });

{
  const page = await ctx.newPage();
  await page.addInitScript(`
    window.__srArgs = [];
    window.SpeechRecognition = window.webkitSpeechRecognition = class {
      start(t) {
        if (t !== undefined && window.__srRejectTrack) throw new TypeError('unsupported');
        window.__srArgs.push(t === undefined ? 'nothing' : (t && t.kind));
        setTimeout(() => this.onstart && this.onstart(), 0);
      }
      stop() {}
    };`);
  await page.goto(PAGE);
  await page.addScriptTag({ content: srcSTT });
  const r = await page.evaluate(async () => {
    const s = await navigator.mediaDevices.getUserMedia({ audio: true });
    const h1 = window.PanelSTT.start({ backend: 'web-speech', continuous: true, stream: s, lang: 'en-US' });
    h1 && h1.stop && h1.stop();
    window.__srRejectTrack = true;
    const h2 = window.PanelSTT.start({ backend: 'web-speech', continuous: true, stream: s, lang: 'en-US' });
    h2 && h2.stop && h2.stop();
    return window.__srArgs;
  });
  r[0] === 'audio' ? ok('web-speech: start(track) receives the call track') : no('web-speech: start received ' + r[0] + ' instead of the track');
  r[1] === 'nothing' ? ok('web-speech: without track support, falls back to start() on the default mic') : no('web-speech: fallback received ' + r[1]);
  await page.close();
}

await browser.close();
fs.rmSync(tmp, { recursive: true, force: true });
console.log(`\n${pass} PASS, ${fail} FAIL`);
process.exit(fail ? 1 : 0);
