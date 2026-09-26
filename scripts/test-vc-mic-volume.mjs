#!/usr/bin/env node
// test-vc-mic-volume.mjs — o volume do microfone tem que chegar DO OUTRO LADO,
// e a transcricao tem que ler o microfone cru.
//
// O slider "Sensibilidade do mic" existia e nunca fez nada: setMicGain nao
// estava exportado em window.VPSMVideoCall, o valor salvo nao entrava no
// Call, e a troca de microfone jogava o track cru direto nos senders. Tudo
// isso passava em qualquer leitura do codigo — o slider mexia, o numero
// mudava. Por isso este pino nao le fonte: ele faz uma chamada WebRTC REAL
// entre duas abas (relay de sinalizacao no Node, mic falso do Chromium
// tocando um tom continuo) e MEDE em dB, na aba B, o audio que chegou da A.
//
// Cobre: volume inicial salvo, volume ao vivo (±6 dB), limitador, troca de
// microfone mantendo o volume, troca de processamento sem derrubar o audio,
// e a fonte da transcricao (cru, independente do volume, calada no mute).
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';

const RAIZ = path.resolve(path.dirname(new URL(import.meta.url).pathname), '..');
const WEB = path.join(RAIZ, 'internal', 'webassets', 'web');
const require_ = createRequire(path.join(RAIZ, '.tools/'));
let chromium;
try { ({ chromium } = require_('playwright-core')); }
catch {
  console.error('FALHA: playwright-core ausente em .tools/. Este pino RODA a chamada — pular seria fingir cobertura.');
  console.error('       Instale com: make tools');
  process.exit(1);
}

let pass = 0, fail = 0;
const ok = (m) => { console.log('PASS ' + m); pass++; };
const no = (m) => { console.log('FAIL ' + m); fail++; };
const perto = (v, alvo, tol) => Math.abs(v - alvo) <= tol;

const srcVC = fs.readFileSync(path.join(WEB, 'vendor', 'vpsm', 'videocall.js'), 'utf8');
const srcSTT = fs.readFileSync(path.join(WEB, 'vendor', 'vpsm', 'stt.js'), 'utf8');

// Tom de 440 Hz a -10 dBFS, continuo. Um tom estavel e o que deixa medir
// diferenca de volume em dB; a fala sintetica padrao do Chromium sao bipes.
function escreveTom(arq) {
  const sr = 48000, seg = 4, n = sr * seg, amp = Math.pow(10, -10 / 20);
  const b = Buffer.alloc(44 + n * 2);
  b.write('RIFF', 0); b.writeUInt32LE(36 + n * 2, 4); b.write('WAVE', 8);
  b.write('fmt ', 12); b.writeUInt32LE(16, 16); b.writeUInt16LE(1, 20); b.writeUInt16LE(1, 22);
  b.writeUInt32LE(sr, 24); b.writeUInt32LE(sr * 2, 28); b.writeUInt16LE(2, 32); b.writeUInt16LE(16, 34);
  b.write('data', 36); b.writeUInt32LE(n * 2, 40);
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(Math.sin(2 * Math.PI * 440 * i / sr) * amp * 32767), 44 + i * 2);
  fs.writeFileSync(arq, b);
}

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

// WebSocket falso: tudo que o motor manda sai por __wsOut (relay no Node);
// o que o relay entrega entra por __wsIn. Mesmo contrato do servidor Go:
// `joined` com o snapshot, `peer-joined` pros outros, repasse por `to` com
// `from` carimbado.
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
  // Travamento simulado: com __travaPrimeiroPC, o 1o RTCPeerConnection da aba
  // nasce sem como gerar candidato local (relay sem TURN) — o mesmo sintoma
  // medido na corrida real: ICE parado em 'new', zero candidatos locais.
  window.__pcsCriados = 0;
  const RPC0 = window.RTCPeerConnection;
  window.RTCPeerConnection = function (cfg) {
    window.__pcsCriados++;
    if (window.__travaPrimeiroPC && window.__pcsCriados === 1) cfg = Object.assign({}, cfg, { iceServers: [], iceTransportPolicy: 'relay' });
    return new RPC0(cfg);
  };
  window.RTCPeerConnection.prototype = RPC0.prototype;
  window.__wsIn = (s) => { const w = window.__ws; if (w && w.onmessage) w.onmessage({ data: s }); };
  // Stub do motor de STT: guarda o stream que a chamada entrega pra transcricao.
  window.__sttStarts = [];
  window.VPSMSTT = {
    start(o) { window.__sttStarts.push(o.stream); return { stop() {} }; },
    setBackend() {}, probeWhisperLocal: async () => false,
  };
  // dBFS de um track num intervalo: MEDIANA do RMS por quadro (~43 ms). Tom
  // estavel da o mesmo valor em todo quadro; a mediana descarta os quadros
  // zerados por underrun do thread de audio (headless sob carga), que numa
  // media simples puxavam a leitura 2-3 dB pra baixo.
  window.__nivelDb = async (track, ms) => {
    const ctx = new AudioContext();
    const src = ctx.createMediaStreamSource(new MediaStream([track]));
    const an = ctx.createAnalyser(); an.fftSize = 2048; src.connect(an);
    const buf = new Float32Array(an.fftSize);
    const quadros = [];
    const fim = performance.now() + ms;
    await new Promise((r) => setTimeout(r, 150));
    while (performance.now() < fim) {
      an.getFloatTimeDomainData(buf);
      let s = 0; for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i];
      const rms = Math.sqrt(s / buf.length);
      quadros.push(rms > 0 ? 20 * Math.log10(rms) : -120);
      await new Promise((r) => setTimeout(r, 40));
    }
    ctx.close();
    if (!quadros.length) return -120;
    quadros.sort((a, b) => a - b);
    return quadros[Math.floor(quadros.length / 2)];
  };
  // Diferenca em dB entre dois tracks medidos JUNTOS (mesmo AudioContext,
  // mesmos quadros): mediana de (b - a) por quadro. Um underrun atinge os
  // dois ao mesmo tempo e se cancela — medir um depois do outro deixava ~1 dB
  // de ruido sob carga, o bastante pra esconder o makeup gain de 1.7 dB.
  window.__difDb = async (a, b, ms) => {
    const ctx = new AudioContext();
    const mk = (t) => { const an = ctx.createAnalyser(); an.fftSize = 2048; ctx.createMediaStreamSource(new MediaStream([t])).connect(an); return an; };
    const aa = mk(a), ab = mk(b);
    const ba = new Float32Array(2048), bb = new Float32Array(2048);
    const db = (buf) => { let s = 0; for (let i = 0; i < buf.length; i++) s += buf[i] * buf[i]; const r = Math.sqrt(s / buf.length); return r > 0 ? 20 * Math.log10(r) : -120; };
    const difs = [], nivA = [];
    await new Promise((r) => setTimeout(r, 150));
    const fim = performance.now() + ms;
    while (performance.now() < fim) {
      aa.getFloatTimeDomainData(ba); ab.getFloatTimeDomainData(bb);
      const x = db(ba), y = db(bb);
      if (x > -90 && y > -90) { difs.push(y - x); nivA.push(x); }
      await new Promise((r) => setTimeout(r, 40));
    }
    ctx.close();
    const med = (v) => { if (!v.length) return NaN; v.sort((p, q) => p - q); return v[Math.floor(v.length / 2)]; };
    return { dif: med(difs), a: med(nivA) };
  };
  // Referencia do microfone: captura propria, sem processamento (o mesmo
  // arquivo de tom que alimenta a chamada).
  window.__refMic = async () => (await navigator.mediaDevices.getUserMedia({ audio: { noiseSuppression: false, echoCancellation: false, autoGainControl: false } })).getAudioTracks()[0];
  // Audio que chegou do peer (tile remoto montado pelo motor).
  window.__remoto = () => {
    const v = document.querySelector('video[data-vc-peer]');
    const s = v && v.srcObject;
    return s && s.getAudioTracks()[0];
  };
`;

const exe = achaNavegador();
if (!exe) { console.error('FALHA: nenhum Chromium encontrado — pular seria fingir cobertura.'); process.exit(1); }
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'vc-micvol-'));
const tom = path.join(tmp, 'tom.wav');
// localhost e contexto seguro (about:blank nao: sem navigator.mediaDevices).
const PAGINA = 'http://localhost:9/vc';
escreveTom(tom);

const browser = await chromium.launch({
  executablePath: exe,
  args: ['--no-sandbox', '--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream',
         '--use-file-for-fake-audio-capture=' + tom, '--autoplay-policy=no-user-gesture-required',
         '--disable-features=WebRtcHideLocalIpsWithMdns'],
});
// ── Relay de sinalizacao ────────────────────────────────────────────────
let ctx, pages, ordem, fila;
async function entrega(id, obj) {
  const p = pages[id];
  if (p) await p.evaluate((s) => window.__wsIn(s), JSON.stringify(obj)).catch(() => {});
}
async function abre(id, trava) {
  const page = await ctx.newPage();
  if (trava) await page.addInitScript('window.__travaPrimeiroPC = true;');
  page.on('pageerror', (e) => console.log('  [' + id + ' pageerror] ' + e.message));
  // Fila unica: o servidor real entrega em ordem (um WS por cliente). Sem
  // ela, cada __wsOut e uma chamada async solta e oferta/ICE/resposta podiam
  // chegar trocados.
  await page.exposeFunction('__wsOut', (s) => { fila = fila.then(() => relay(id, s)).catch(() => {}); });
  const relay = async (id, s) => {
    let m; try { m = JSON.parse(s); } catch { return; }
    if (m.type === '__hello') {
      const outros = ordem.filter((x) => x !== id);
      ordem.push(id);
      await entrega(id, { type: 'joined', payload: { peer_id: id, peers: outros.map((o) => ({ id: o, user: o })) } });
      for (const o of outros) await entrega(o, { type: 'peer-joined', from: id, payload: JSON.stringify({ user: id }) });
      return;
    }
    if (m.type === 'ping') { await entrega(id, { type: 'pong' }); return; }
    m.from = id;
    if (m.to) await entrega(m.to, m);
    else for (const o of ordem) if (o !== id) await entrega(o, m);
  };
  await page.addInitScript(INIT);
  await page.goto(PAGINA);
  await page.addScriptTag({ content: srcVC });
  pages[id] = page;
  return page;
}
async function conecta(page, id, extra) {
  return page.evaluate(async ({ id, extra }) => {
    const el = document.createElement('div'); document.body.appendChild(el);
    window.__eventos = [];
    await window.VPSMVideoCall.connect(Object.assign({
      roomId: 'room', token: 't', displayName: id, videosEl: el, quality: 'eco',
      onState: (ev) => window.__eventos.push(ev), onError: (e) => window.__eventos.push({ type: 'erro', e }),
    }, extra));
  }, { id, extra });
}

// Processamento desligado em A: supressao de ruido comeria o tom estavel e o
// ganho automatico andaria sozinho — a medida precisa de um sinal parado.
const SEM_PROC = { noiseSuppression: false, echoCancellation: false, autoGainControl: false };

// Monta a chamada A↔B e espera o AUDIO de A soar em B (nao so o track vivo:
// um track remoto nasce 'live' e mudo antes de qualquer RTP chegar). Sem
// remontar: a negociacao tem que se resolver sozinha — inclusive a corrida de
// ofertas simultaneas e o RTCPeerConnection que trava sem candidato ICE (o
// motor recria o par). `trava` forca esse travamento em A.
async function montaChamada(trava) {
  if (ctx) await ctx.close().catch(() => {});
  ctx = await browser.newContext();
  await ctx.route(PAGINA, (r) => r.fulfill({ contentType: 'text/html', body: '<!doctype html><html><body></body></html>' }));
  pages = {}; ordem = []; fila = Promise.resolve();
  const A = await abre('A', trava);
  const B = await abre('B');
  await conecta(A, 'A', { micGain: 2, micProcessing: SEM_PROC });
  await conecta(B, 'B', { micProcessing: SEM_PROC });
  const t0 = Date.now();
  while (Date.now() - t0 < 20000) {
    const db = await B.evaluate(() => { const t = window.__remoto(); return t ? window.__nivelDb(t, 600) : -120; });
    if (db > -40) return { A, B, seg: (Date.now() - t0) / 1000 };
  }
  return null;
}

// ── 0. Conexao travada se recupera sozinha ──────────────────────────────
// Antes: ICE parado em 'new' pra sempre (o restart so disparava em
// 'failed'), chamada muda sem volta. Agora o lado travado recria o par e
// pede ao outro (mensagem `reset`) que recrie o dele.
{
  const m = await montaChamada(true);
  const pcs = m ? await m.A.evaluate(() => window.__pcsCriados) : 0;
  m ? ok(`recuperacao: conexao travada de A religou sozinha em ${m.seg.toFixed(1)}s`) : no('recuperacao: conexao travada de A nao religou em 20s');
  pcs >= 2 ? ok(`recuperacao: A recriou o RTCPeerConnection (${pcs} criados)`) : no('recuperacao: A nao recriou o RTCPeerConnection (' + pcs + ')');
}

const montada = await montaChamada(false);
if (!montada) {
  no('chamada: o audio de A nunca chegou em B em 20s');
  await browser.close(); fs.rmSync(tmp, { recursive: true, force: true });
  console.log(`\n${pass} PASS, ${fail} FAIL`); process.exit(1);
}
const { A, B } = montada;
ok(`chamada: WebRTC real entre as abas, audio de A soando em B em ${montada.seg.toFixed(1)}s`);

const recebido = () => B.evaluate(() => window.__nivelDb(window.__remoto(), 2500));
const aLocal = (fn, arg) => A.evaluate(fn, arg);

// ── 1. Volume salvo entra na chamada ─────────────────────────────────────
// Entrou com micGain=2. (a) No track ENVIADO, medido antes de qualquer
// setMicGain: o tom e -13 dBFS RMS, entao 200% tem que sair a ~-7 — antes o
// Call ignorava o valor salvo e saia sempre a 100%. (b) Do outro lado: baixar
// pra 100% derruba o recebido em ~6 dB.
{
  const r = await aLocal(async () => {
    const ref = await window.__refMic();
    const out = await window.__difDb(ref, document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0], 1200);
    ref.stop();
    return out;
  });
  perto(r.dif, 6.02, 0.5)
    ? ok(`volume salvo: 200% aplicado desde o connect (enviado +${r.dif.toFixed(2)} dB sobre o mic)`)
    : no(`volume salvo: enviado ${r.dif.toFixed(2)} dB sobre o mic no connect — esperado +6.0 (200%)`);
}
await new Promise((r) => setTimeout(r, 3000)); // Opus/jitter buffer assentar
const em200 = await recebido();
await aLocal(() => window.VPSMVideoCall.setMicGain(1));
const em100 = await recebido();
perto(em200 - em100, 6.02, 1.5)
  ? ok(`volume ao vivo: 200% chega ${(em200 - em100).toFixed(1)} dB acima de 100% do outro lado`)
  : no(`volume ao vivo: 200% vs 100% deu ${(em200 - em100).toFixed(1)} dB no receptor (esperado ~6)`);

// ── 2. Volume ao vivo pra baixo ─────────────────────────────────────────
await aLocal(() => window.VPSMVideoCall.setMicGain(0.5));
const em50 = await recebido();
perto(em100 - em50, 6.02, 1.5)
  ? ok(`volume ao vivo: 50% chega ${(em100 - em50).toFixed(1)} dB abaixo de 100%`)
  : no(`volume ao vivo: 50% vs 100% deu ${(em100 - em50).toFixed(1)} dB (esperado ~6)`);

// ── 3. Limitador ────────────────────────────────────────────────────────
// Tom a -10 dBFS × 400% = +2 dBFS: sem limitador, clipava. Com ele, o
// medidor acusa `limiting` e o pico enviado fica abaixo de 0 dBFS.
await aLocal(() => window.VPSMVideoCall.setMicGain(4));
await new Promise((r) => setTimeout(r, 400));
const lim = await aLocal(async () => {
  let limiting = false, pico = 0;
  for (let i = 0; i < 20; i++) {
    const r = window.VPSMVideoCall.getMicLevel();
    if (r) { limiting = limiting || r.limiting; pico = Math.max(pico, r.peak); }
    await new Promise((res) => setTimeout(res, 50));
  }
  return { limiting, pico };
});
lim.limiting ? ok('limitador: 400% num sinal forte acende o aviso "limitando"') : no('limitador: getMicLevel().limiting nunca ficou true a 400%');
lim.pico < 1.0 ? ok(`limitador: pico enviado ${lim.pico.toFixed(2)} < 1.0 (sem clip)`) : no(`limitador: pico enviado ${lim.pico.toFixed(2)} — clipando`);
await aLocal(() => window.VPSMVideoCall.setMicGain(2));

// ── 4. Troca de microfone mantem o volume ───────────────────────────────
// Antes: o track novo ia CRU pros senders e o volume voltava a 100%.
const antesTroca = await recebido();
const trocou = await aLocal(() => window.VPSMVideoCall.setMicDevice('default'));
const depoisTroca = await recebido();
trocou ? ok('troca de mic: setMicDevice concluiu') : no('troca de mic: setMicDevice falhou');
perto(depoisTroca, antesTroca, 1.5)
  ? ok(`troca de mic: volume mantido do outro lado (${antesTroca.toFixed(1)} → ${depoisTroca.toFixed(1)} dBFS)`)
  : no(`troca de mic: recebido foi de ${antesTroca.toFixed(1)} pra ${depoisTroca.toFixed(1)} dBFS — volume perdido na troca`);

// ── 5. Troca de processamento nao derruba o audio ───────────────────────
const proc = await aLocal(() => window.VPSMVideoCall.setMicProcessing({ echoCancellation: true }));
proc && proc.echoCancellation === true && proc.noiseSuppression === false
  ? ok('processamento: setMicProcessing aplicou so a chave pedida')
  : no('processamento: retorno inesperado ' + JSON.stringify(proc));
const comEco = await recebido();
comEco > -40 ? ok(`processamento: audio segue chegando depois de reabrir o mic (${comEco.toFixed(1)} dBFS)`) : no(`processamento: audio sumiu depois do setMicProcessing (${comEco.toFixed(1)} dBFS)`);
await aLocal((p) => window.VPSMVideoCall.setMicProcessing(p), SEM_PROC);

// ── 5b. 100% e neutro ───────────────────────────────────────────────────
// O compressor do Web Audio soma makeup gain automatico (+1.7 dB aqui); o
// pipeline compensa. Sem isso "100%" saia mais alto que o proprio microfone.
{
  await aLocal(() => window.VPSMVideoCall.setMicGain(1));
  await aLocal(() => { window.VPSMVideoCall.setSubtitles(true, { backend: 'web-speech', lang: 'pt-BR' }); });
  await new Promise((r) => setTimeout(r, 400));
  const n = await aLocal(async () => {
    const cru = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
    const enviado = document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0];
    return window.__difDb(cru, enviado, 1200);
  });
  await aLocal(() => { window.VPSMVideoCall.setSubtitles(false); });
  await new Promise((r) => setTimeout(r, 600)); // guard de toggle rapido (500 ms)
  perto(n.dif, 0, 0.3)
    ? ok(`neutro: 100% envia o mesmo nivel do microfone (${n.dif >= 0 ? '+' : ''}${n.dif.toFixed(2)} dB)`)
    : no(`neutro: a 100% o enviado ficou ${n.dif.toFixed(2)} dB do microfone (makeup gain do limitador?)`);
  await aLocal(() => window.VPSMVideoCall.setMicGain(2));
}

// ── 6. Transcricao le o cru ─────────────────────────────────────────────
await aLocal(() => { window.VPSMVideoCall.setSubtitles(true, { backend: 'web-speech', lang: 'pt-BR' }); });
await new Promise((r) => setTimeout(r, 400));
const stt = await aLocal(async () => {
  const s = window.__sttStarts[window.__sttStarts.length - 1];
  const t = s && s.getAudioTracks()[0];
  const enviado = document.querySelector('[data-vc-local="1"]').srcObject.getAudioTracks()[0];
  const par = await window.__difDb(t, enviado, 1200);
  return { tem: !!t, mesmo: t === enviado, dbStt: par.a, dif: par.dif };
});
stt.tem ? ok('stt: a chamada entregou um track pra transcricao') : no('stt: nenhum track entregue ao STT');
!stt.mesmo ? ok('stt: o track da transcricao NAO e o enviado (nao passa pelo volume)') : no('stt: transcricao lendo o track enviado');
perto(stt.dif, 6.02, 0.5)
  ? ok(`stt: com volume em 200%, a transcricao le o cru (enviado +${stt.dif.toFixed(2)} dB sobre ele)`)
  : no(`stt: enviado ${stt.dif.toFixed(2)} dB sobre o track da transcricao — esperado +6.0`);
perto(stt.dbStt, -13, 2)
  ? ok('stt: nivel do cru bate com o tom do microfone (~-13 dBFS RMS)')
  : no(`stt: nivel do cru ${stt.dbStt.toFixed(1)} dBFS, esperado ~-13`);

// ── 7. Mutado nao transcreve ────────────────────────────────────────────
// Antes: o cru seguia enabled e o Whisper transcrevia — e mandava como
// legenda — o que a pessoa falava mutada.
const mute = await aLocal(async () => {
  window.VPSMVideoCall.setMuted(true);
  const t = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
  await new Promise((r) => setTimeout(r, 300));
  const lvl = window.VPSMVideoCall.getMicLevel();
  const r = { enabled: t.enabled, nivel: lvl ? lvl.level : -1 };
  window.VPSMVideoCall.setMuted(false);
  r.voltou = t.enabled;
  return r;
});
mute.enabled === false ? ok('mute: o track da transcricao fica desligado') : no('mute: track da transcricao seguiu ligado — transcreveria fala mutada');
mute.nivel === 0 ? ok('mute: medidor do que sai vai a zero') : no('mute: medidor em ' + mute.nivel + ' com o mic mutado');
mute.voltou === true ? ok('mute: desmutar religa o track da transcricao') : no('mute: desmutar nao religou o track da transcricao');

// ── 8. Troca de mic reinicia a transcricao no track novo ────────────────
const reinicio = await aLocal(async () => {
  const antes = window.__sttStarts.length;
  const velho = window.__sttStarts[antes - 1].getAudioTracks()[0];
  await window.VPSMVideoCall.setMicDevice('default');
  await new Promise((r) => setTimeout(r, 300));
  const novo = window.__sttStarts[window.__sttStarts.length - 1].getAudioTracks()[0];
  return { novos: window.__sttStarts.length - antes, velhoEstado: velho.readyState, novoEstado: novo.readyState, igual: velho === novo };
});
(reinicio.novos >= 1 && !reinicio.igual && reinicio.novoEstado === 'live')
  ? ok('stt: troca de mic reinicia a transcricao no track novo (vivo)')
  : no('stt: depois da troca de mic a transcricao ficou no track ' + reinicio.velhoEstado + ' (' + JSON.stringify(reinicio) + ')');
await aLocal(() => { window.VPSMVideoCall.setSubtitles(false); });

// ── 9. Web Speech reconhece o track entregue ────────────────────────────
// SpeechRecognition.start(track) (Chrome 135+). Sem ele, ouvia o mic padrao
// do sistema — que pode nem ser o da chamada. Onde start(track) lanca, cai
// no start() sem argumento.
{
  const page = await ctx.newPage();
  await page.addInitScript(`
    window.__srArgs = [];
    window.SpeechRecognition = window.webkitSpeechRecognition = class {
      start(t) {
        if (t !== undefined && window.__srRecusaTrack) throw new TypeError('sem suporte');
        window.__srArgs.push(t === undefined ? 'nada' : (t && t.kind));
        setTimeout(() => this.onstart && this.onstart(), 0);
      }
      stop() {}
    };`);
  await page.goto(PAGINA);
  await page.addScriptTag({ content: srcSTT });
  const r = await page.evaluate(async () => {
    const s = await navigator.mediaDevices.getUserMedia({ audio: true });
    const h1 = window.VPSMSTT.start({ backend: 'web-speech', continuous: true, stream: s, lang: 'pt-BR' });
    h1 && h1.stop && h1.stop();
    window.__srRecusaTrack = true;
    const h2 = window.VPSMSTT.start({ backend: 'web-speech', continuous: true, stream: s, lang: 'pt-BR' });
    h2 && h2.stop && h2.stop();
    return window.__srArgs;
  });
  r[0] === 'audio' ? ok('web-speech: start(track) recebe o track da chamada') : no('web-speech: start recebeu ' + r[0] + ' em vez do track');
  r[1] === 'nada' ? ok('web-speech: sem suporte a track, cai no start() do mic padrao') : no('web-speech: fallback recebeu ' + r[1]);
  await page.close();
}

await browser.close();
fs.rmSync(tmp, { recursive: true, force: true });
console.log(`\n${pass} PASS, ${fail} FAIL`);
process.exit(fail ? 1 : 0);
