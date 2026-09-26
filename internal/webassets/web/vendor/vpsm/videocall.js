/* vpsm-videocall — WebRTC P2P client integrated with vps-manager signaling.
 *
 * Exposed as: window.VPSMVideoCall = { connect(opts), disconnect() }
 *
 * Architecture (matches internal/videocall in the Go backend):
 *   - One WebSocket to /ws/videocall?token=&room_id= for signaling.
 *   - One RTCPeerConnection per remote peer. For 1-on-1 calls there is
 *     exactly one. For mesh up to 4, N-1 connections per peer.
 *   - Perfect negotiation pattern (W3C) — peers compare ids lexicographically
 *     to elect the "polite" side; eliminates glare without flips.
 *   - Codec preference AV1 > VP9 > H264 (server has no opinion; the SDP is
 *     negotiated peer-to-peer).
 *   - Hard cap maxBitrate via setParameters; do NOT loop over getStats and
 *     micromanage bitrate (Google Congestion Control already does that
 *     better than we can).
 *   - getStats(1s) feeds the bandwidth dashboard but does NOT influence
 *     transmission decisions.
 *   - ICE restart on iceConnectionState==='failed' or window.online.
 *   - Local recording uses MediaRecorder (zero server bandwidth).
 *     [Recording UI is Fase 2; the API is here for the next phase.]
 *
 * Browser compat: Chrome/Edge 88+, Firefox 91+, Safari 15+. AV1 falls back
 * automatically when both peers don't support it.
 */
(function () {
  'use strict';

  if (window.VPSMVideoCall) return; // idempotent load

  // ---- constants ------------------------------------------------------

  const CODEC_PREF = ['video/AV1', 'video/VP9', 'video/H264', 'video/VP8'];
  const DEFAULT_BUDGET_KBPS = 500; // medium-mode default; user can override
  const STATS_INTERVAL_MS = 1000;
  const RECONNECT_BACKOFF_MIN = 1000;
  const RECONNECT_BACKOFF_MAX = 30000;
  // Quality presets — knob for the user's "Economia / Médio / Alto" buttons.
  // Numbers picked to match what real codecs actually deliver at each level:
  //  - economy: 320x180@15 at 60kbps video + 16kbps audio Opus = ~80kbps total,
  //    holds on mobile 3G or weak Wi-Fi. Video is grainy but recognizable.
  //  - medium:  640x360@24, ~500kbps video + 32kbps audio = ~540kbps. Smooth,
  //    looks fine on a phone or in a small window on desktop.
  //  - high:    1280x720@30, ~2000kbps video + 48kbps audio = ~2050kbps. The
  //    "HD" experience on home Wi-Fi.
  //
  // Codec stays on the user's previous choice ('auto' picks AV1→VP9→H264).
  // Changing codec live needs renegotiation; we apply it on the next call.
  // Telefone/Mínima/Economia ativam tweak Opus (usedtx + maxaveragebitrate
  // + useinbandfec=0). Médio/Alto deixam o codec respirar com FEC ligado
  // pra resiliência em rede instável.
  //
  // Telefone marca videoOff: true — Call.start nem chama getUserMedia
  // com video, e applyQualityProfile derruba o video sender quando o
  // user troca pra Telefone mid-call. Economia ~80 kbps continua sendo
  // o nosso "Economy" de baseline; abaixo dele tem Mínima (~40) e
  // Telefone (~15).
  const QUALITY_MODES = {
    phone:   { videoOff: true,  width: 0,    height: 0,   fps: 0,  videoKbps: 0,    audioKbps: 16, opusTweak: true,  label: 'Telefone' },
    low:     { videoOff: false, width: 160,  height: 90,  fps: 8,  videoKbps: 25,   audioKbps: 16, opusTweak: true,  label: 'Mínima'   },
    economy: { videoOff: false, width: 320,  height: 180, fps: 15, videoKbps: 60,   audioKbps: 20, opusTweak: true,  label: 'Economia' },
    medium:  { videoOff: false, width: 640,  height: 360, fps: 24, videoKbps: 500,  audioKbps: 32, opusTweak: false, label: 'Médio'    },
    high:    { videoOff: false, width: 1280, height: 720, fps: 30, videoKbps: 2000, audioKbps: 48, opusTweak: false, label: 'Alto'     },
  };
  // Absolute floor for the budget slider — below this, even audio Opus DTX
  // doesn't reconstruct cleanly. Above it, the network stack can do its
  // adaptive thing.
  // Floor de 8 kbps habilita Opus extremo (audio-only quase telefônico).
  // Cap em 6 Mbps é mais que o suficiente pra 1080p AV1 — não temos UI
  // pra ir além.
  const BUDGET_FLOOR_KBPS = 8;
  const BUDGET_CEILING_KBPS = 6000;

  // tweakOpusSdp munge o SDP pra forçar Opus em modo super-econômico:
  //   - usedtx=1            : discontinuous transmission (não manda nada
  //                           durante silêncio). Reduz consumo médio em
  //                           40-60% numa chamada normal.
  //   - useinbandfec=0      : desliga Forward Error Correction. Perde
  //                           resiliência mas economiza ~20% extras.
  //   - maxaveragebitrate=N : cap rígido. Sem isso, o Opus às vezes ignora
  //                           o maxBitrate do RTCRtpSender.
  //   - cbr=0; stereo=0     : VBR mono (voz é o caso). Stereo dobraria a banda.
  //
  // Aplicado em createOffer/createAnswer antes do setLocalDescription. Não
  // mexe em SDP de vídeo — só na seção m=audio.
  function tweakOpusSdp(sdp, audioKbps) {
    if (!sdp) return sdp;
    const lines = sdp.split(/\r?\n/);
    // Encontra o payload type do Opus (varia entre browsers / sessões).
    let opusPt = null;
    for (const l of lines) {
      const m = l.match(/^a=rtpmap:(\d+)\s+opus\/48000/i);
      if (m) { opusPt = m[1]; break; }
    }
    if (!opusPt) return sdp; // Sem opus na oferta? Não munge.
    const fmtpRegex = new RegExp('^a=fmtp:' + opusPt + '\\s+(.*)$');
    let hasFmtp = false;
    const out = [];
    const want = {
      usedtx: '1',
      useinbandfec: '0',
      cbr: '0',
      stereo: '0',
      maxaveragebitrate: String(Math.max(6000, Math.min(64000, (audioKbps || 16) * 1000))),
    };
    for (const l of lines) {
      const m = l.match(fmtpRegex);
      if (m) {
        hasFmtp = true;
        const params = {};
        m[1].split(';').forEach(kv => {
          const eq = kv.indexOf('=');
          if (eq > 0) params[kv.slice(0, eq).trim()] = kv.slice(eq + 1).trim();
        });
        Object.assign(params, want);
        const rebuilt = Object.keys(params).map(k => k + '=' + params[k]).join(';');
        out.push('a=fmtp:' + opusPt + ' ' + rebuilt);
      } else {
        out.push(l);
      }
    }
    // Se não havia fmtp, injeta logo após o rtpmap do opus.
    if (!hasFmtp) {
      const final = [];
      const inject = 'a=fmtp:' + opusPt + ' ' + Object.keys(want).map(k => k + '=' + want[k]).join(';');
      for (const l of out) {
        final.push(l);
        if (/^a=rtpmap:\d+\s+opus\/48000/i.test(l)) final.push(inject);
      }
      return final.join('\r\n');
    }
    return out.join('\r\n');
  }

  // ---- state ----------------------------------------------------------

  // Singleton call state. We disallow more than one active call per tab —
  // the panel UI enforces this (no "join another room while in a call").
  let active = null;

  // ---- public API -----------------------------------------------------

  /**
   * Open a videocall.
   * @param {Object} opts
   * @param {string} opts.roomId
   * @param {string} opts.token         JWT for /ws/videocall?token=
   * @param {string} [opts.displayName]
   * @param {string} [opts.codec]       AV1 | VP9 | H264 | "auto" (default)
   * @param {number} [opts.budgetKbps]  hard cap on outgoing video bitrate
   * @param {boolean} [opts.audioOnly]
   * @param {string} [opts.e2eePassphrase]  enables AES-GCM frame encryption
   * @param {boolean} [opts.audioFirstMode] auto-pause video on bad network
   * @param {function} [opts.onState]   ({type, ...}) updates the UI
   * @param {function} [opts.onStats]   (statsObject)
   * @param {function} [opts.onChat]    ({from, text, ts})
   * @param {function} [opts.onError]   (errMsg)
   * @param {HTMLElement} opts.videosEl Container DIV — call inserts <video> elements
   * @returns {Promise<void>}
   */
  async function connect(opts) {
    if (active) {
      throw new Error('a call is already active in this tab');
    }
    const call = new Call(opts);
    active = call;
    try {
      await call.start();
    } catch (e) {
      active = null;
      throw e;
    }
  }

  /** Hangup and tear down everything. Safe to call when idle. */
  function disconnect() {
    if (!active) return;
    active.stop();
    active = null;
  }

  /** Send a chat message to all peers via DataChannel. */
  function sendChat(text) {
    if (!active) return;
    active.sendChat(text);
  }

  /** Broadcast a `state` signaling message to all peers in the room (via WS).
      Usado por features como "owner forçando quality mode pra todos". */
  function sendState(payload) {
    if (!active) return false;
    try {
      active.send({ type: 'state', payload: payload });
      return true;
    } catch (_) { return false; }
  }

  /** Toggle local audio mute. Returns new muted state. */
  function setMuted(muted) {
    if (!active) return false;
    return active.setMuted(muted);
  }

  /** Toggle local video off. Returns new camera-off state. */
  function setVideoOff(off) {
    if (!active) return false;
    return active.setVideoOff(off);
  }

  /** Adjust the outgoing bandwidth cap in kbps. */
  function setBudgetKbps(kbps) {
    if (!active) return;
    active.setBudgetKbps(kbps);
  }

  /** Start/stop screen sharing. Returns the new screen-share state. */
  async function setScreenShare(on) {
    if (!active) return false;
    return active.setScreenShare(on);
  }

  /** Start local-only recording. Returns true if started. */
  function startRecording() {
    if (!active) return false;
    return active.startRecording();
  }

  /** Stop and download the local recording. */
  function stopRecording() {
    if (!active) return;
    active.stopRecording();
  }

  /** Toggle audio-first mode (auto-pause video on bad network). */
  function setAudioFirstMode(on) {
    if (!active) return;
    active.audioFirstMode = !!on;
  }

  /** Get the static preset definitions (for UI to read out values). */
  function getQualityPresets() {
    return JSON.parse(JSON.stringify(QUALITY_MODES));
  }

  /**
   * Apply a quality profile live to the active call. Accepts either a
   * preset name ('economy'|'medium'|'high') OR a custom object
   * { width, height, fps, videoKbps, audioKbps }. Returns the effective
   * profile applied (post-clamping).
   *
   * - width/height/fps go through MediaStreamTrack.applyConstraints — the
   *   browser may downgrade to the nearest supported value.
   * - videoKbps + audioKbps go through RTCRtpSender.setParameters which
   *   doesn't need SDP renegotiation; effect is immediate.
   */
  async function applyQualityProfile(profileOrName) {
    if (!active) return null;
    return active.applyQualityProfile(profileOrName);
  }

  // -- Device APIs (work in-call AND out-of-call) -----------------------
  // enumerateDevices() returns empty labels until the user has granted
  // permission at least once. probeDevicePermission triggers a one-shot
  // getUserMedia just so labels populate; the stream is dropped immediately.
  async function listDevices() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) {
      return { cameras: [], mics: [], speakers: [] };
    }
    const list = await navigator.mediaDevices.enumerateDevices();
    // Sanitiza: enumerateDevices() retorna deviceId="" antes do user dar
    // permissão, e múltiplas entradas com deviceId vazio causam :key undefined
    // no Alpine x-for → crash da UI inteira. Synth-gera ID estável quando
    // vazio, e dedup por ID resolvido.
    const sanitize = (arr) => {
      const seen = new Set();
      const out = [];
      for (let i = 0; i < arr.length; i++) {
        const d = arr[i];
        if (!d) continue;
        const id = d.deviceId || ('synth-' + d.kind + '-' + i);
        if (seen.has(id)) continue;
        seen.add(id);
        // Wrapper plain-object — MediaDeviceInfo é read-only.
        out.push({
          deviceId: id,
          kind: d.kind,
          label: d.label || '',
          groupId: d.groupId || '',
        });
      }
      return out;
    };
    return {
      cameras:  sanitize(list.filter(d => d.kind === 'videoinput')),
      mics:     sanitize(list.filter(d => d.kind === 'audioinput')),
      speakers: sanitize(list.filter(d => d.kind === 'audiooutput')),
    };
  }
  // Traduz o DOMException do getUserMedia numa frase acionavel. Extraido do
  // probe porque agora tres call-sites precisam da mesma traducao (probe
  // combinado, probe por tipo, e a degradacao do Call.start).
  function humanizeGumError(e, kindLabel) {
    const name = e && e.name;
    const alvo = kindLabel || 'câmera/microfone';
    if (name === 'NotAllowedError' || name === 'PermissionDeniedError') {
      return 'Permissão de ' + alvo + ' bloqueada. Clique no ícone 🔒 ao lado da URL → permitir → recarregar a página.';
    }
    if (name === 'NotFoundError' || name === 'DevicesNotFoundError') {
      return 'Nenhum(a) ' + alvo + ' foi encontrado(a) neste computador.';
    }
    if (name === 'NotReadableError' || name === 'TrackStartError') {
      return alvo.charAt(0).toUpperCase() + alvo.slice(1) + ' já está em uso por outro programa (Zoom/Meet/OBS/app do sistema). Feche os outros e tente de novo.';
    }
    if (name === 'OverconstrainedError') {
      return 'O dispositivo salvo para ' + alvo + ' não existe mais (foi desconectado?). Escolha outro na lista.';
    }
    if (name === 'SecurityError') {
      return 'Bloqueado por política de segurança — o site precisa estar em HTTPS.';
    }
    if (name === 'AbortError') {
      return 'O pedido de permissão de ' + alvo + ' foi cancelado/interrompido.';
    }
    return (e && e.message) || ('Erro desconhecido ao acessar ' + alvo + '.');
  }
  // Testa UM tipo isolado. Existe porque getUserMedia e all-or-nothing:
  // pedir {audio,video} junto e ter so o microfone devolve NotFoundError
  // como se NADA existisse — foi o que trancava o usuario fora da chamada.
  async function probeKind(kind) {
    try {
      const s = await navigator.mediaDevices.getUserMedia(
        kind === 'audio' ? { audio: true } : { video: true });
      s.getTracks().forEach(t => t.stop());
      return { ok: true };
    } catch (e) {
      return { ok: false, name: e && e.name, error: humanizeGumError(e, kind === 'audio' ? 'microfone' : 'câmera') };
    }
  }
  // Resultado: { ok, audio, video, error, detail } — `ok` significa "da pra
  // entrar na chamada com ALGUMA coisa", nao "esta tudo perfeito". Quem chama
  // decide o que fazer com um lado faltando; bloquear a entrada so quando os
  // dois faltam.
  async function probeDevicePermission() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return { ok: false, audio: false, video: false, error: 'Browser não suporta getUserMedia (HTTPS exigido).' };
    }
    // Estado da permissao ANTES de pedir: se ja foi negado, a instrucao e
    // outra (mexer no cadeado) e re-tentar so gastaria um prompt. So e
    // bloqueio duro quando os DOIS estao negados.
    let camDenied = false, micDenied = false;
    try {
      if (navigator.permissions && navigator.permissions.query) {
        const [camP, micP] = await Promise.all([
          navigator.permissions.query({ name: 'camera' }).catch(() => null),
          navigator.permissions.query({ name: 'microphone' }).catch(() => null),
        ]);
        camDenied = !!(camP && camP.state === 'denied');
        micDenied = !!(micP && micP.state === 'denied');
        if (camDenied && micDenied) {
          return {
            ok: false, audio: false, video: false,
            error: 'Permissão de câmera E microfone foi NEGADA neste site. Clique no ícone de cadeado 🔒 (ou ⓘ) na barra de endereço, encontre Câmera/Microfone e troque pra "Permitir". Depois recarregue a página.',
            permState: { camera: 'denied', microphone: 'denied' },
          };
        }
      }
    } catch (_) {}
    // Caminho feliz: um unico prompt para os dois.
    try {
      const s = await navigator.mediaDevices.getUserMedia({ audio: true, video: true });
      s.getTracks().forEach(t => t.stop());
      return { ok: true, audio: true, video: true };
    } catch (_) {
      // Combinado falhou. NAO concluir "sem dispositivo": basta UM dos dois
      // faltar pra derrubar o pedido inteiro. Descobre qual lado funciona.
    }
    // Sempre testa os dois de verdade. O `permissions.query` acima serve so
    // pro atalho de "os dois negados"; usa-lo pra DECIDIR o motivo por tipo
    // mentiria — o chromium responde 'denied' pra camera que simplesmente
    // nao existe, e o usuario iria cacar um cadeado que nao resolve nada.
    const [a, v] = await Promise.all([probeKind('audio'), probeKind('video')]);
    if (a.ok && v.ok) return { ok: true, audio: true, video: true };
    let error;
    if (!a.ok && !v.ok) {
      error = a.error === v.error ? a.error : (a.error + ' ' + v.error);
    } else if (a.ok) {
      error = v.error + ' Você pode entrar assim mesmo — só com áudio.';
    } else {
      error = a.error + ' Você pode entrar assim mesmo, mas ninguém vai te ouvir.';
    }
    return {
      ok: a.ok || v.ok,
      audio: a.ok,
      video: v.ok,
      error: error,
      detail: { audio: a, video: v },
    };
  }
  async function setCameraDevice(id) {
    if (!active) return false;
    return active.setCameraDevice(id);
  }
  async function setMicDevice(id) {
    if (!active) return false;
    return active.setMicDevice(id);
  }
  // Volume de envio + processamento. Fora de chamada nao ha o que aplicar —
  // o shell guarda a preferencia e ela entra no proximo connect().
  function setMicGain(v) {
    if (!active) return clampMicGain(v);
    return active.setMicGain(v);
  }
  async function setMicProcessing(proc) {
    if (!active) return normMicProc(proc);
    return active.setMicProcessing(proc);
  }
  function getMicLevel() {
    if (!active) return null;
    return active.getMicLevel();
  }
  async function setSpeakerDevice(id) {
    if (!active) return false;
    return active.setSpeakerDevice(id);
  }
  // Test tone routed through a given sinkId — used by the lobby "Testar"
  // button so the user can confirm audio is coming out of the right device
  // before joining. 440Hz, 0.5s, gentle envelope to avoid clicks.
  async function playTestTone(sinkId) {
    try {
      const ctx = new (window.AudioContext || window.webkitAudioContext)();
      const dst = ctx.createMediaStreamDestination();
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.frequency.value = 440;
      gain.gain.setValueAtTime(0, ctx.currentTime);
      gain.gain.linearRampToValueAtTime(0.18, ctx.currentTime + 0.05);
      gain.gain.linearRampToValueAtTime(0.18, ctx.currentTime + 0.4);
      gain.gain.linearRampToValueAtTime(0, ctx.currentTime + 0.5);
      osc.connect(gain).connect(dst);
      const a = document.createElement('audio');
      a.srcObject = dst.stream;
      if (sinkId && sinkId !== 'default' && typeof a.setSinkId === 'function') {
        try { await a.setSinkId(sinkId); } catch (_) {}
      }
      await a.play();
      osc.start();
      osc.stop(ctx.currentTime + 0.5);
      setTimeout(() => { try { a.pause(); if (ctx && ctx.state !== "closed") ctx.close().catch(()=>{}); } catch (_) {} }, 700);
      return true;
    } catch (e) { return false; }
  }
  // Mic level monitor — returns a controller you call .stop() on.
  // Used by the lobby to show a live VU meter. opts.gain (numero ou funcao)
  // e opts.processing reproduzem o pipeline da chamada — volume + limitador
  // + o mesmo processamento — pra o medidor mostrar o que o outro lado vai
  // ouvir, nao o mic cru. onLevel(level 0..100, {limiting}).
  function createMicLevelMonitor(deviceId, onLevel, opts) {
    opts = opts || {};
    let ctx, stream, raf, running = true;
    const gainOf = () => clampMicGain(typeof opts.gain === 'function' ? opts.gain() : (opts.gain != null ? opts.gain : 1));
    (async () => {
      try {
        stream = await navigator.mediaDevices.getUserMedia({
          audio: opts.processing ? micConstraints(deviceId, opts.processing)
            : (deviceId && deviceId !== 'default' ? { deviceId: { exact: deviceId } } : true),
        });
        if (!running) { stream.getTracks().forEach(t => t.stop()); return; }
        ctx = new (window.AudioContext || window.webkitAudioContext)();
        const src = ctx.createMediaStreamSource(stream);
        const gain = ctx.createGain();
        const limiter = makeMicLimiter(ctx);
        const analyser = ctx.createAnalyser();
        analyser.fftSize = 1024;
        src.connect(gain);
        gain.connect(limiter.input);
        limiter.output.connect(analyser);
        const buf = new Float32Array(analyser.fftSize);
        const tick = () => {
          if (!running) return;
          gain.gain.value = gainOf();
          const r = readMicLevel(analyser, buf, limiter);
          onLevel(r.level, r);
          raf = requestAnimationFrame(tick);
        };
        tick();
      } catch (e) { onLevel(0, { level: 0, peak: 0, limiting: false }); }
    })();
    return {
      stop() {
        running = false;
        if (raf) cancelAnimationFrame(raf);
        if (stream) stream.getTracks().forEach(t => t.stop());
        if (ctx && ctx.state !== "closed") { try { ctx.close().catch(()=>{}); } catch (_) {} }
      }
    };
  }

  /** Send a file to all peers via DataChannel. Returns a promise. */
  function sendFile(file) {
    if (!active) return Promise.reject(new Error('no active call'));
    return active.sendFile(file);
  }

  /** Toggle privacy frost (full-frame blur). honest label — NOT background-only. */
  function setPrivacyFrost(on) {
    if (!active) return false;
    return active.setPrivacyFrost(on);
  }

  /** Send a whiteboard stroke / clear to all peers. */
  function sendWhiteboardEvent(ev) {
    if (!active) return;
    active.broadcastWhiteboard(ev);
  }

  /** Full buffered whiteboard board (normalized strokes) for redraw on resize. */
  function getWhiteboardStrokes() {
    return active ? active._wbStrokes : [];
  }

  /** Full buffered screen-annotation strokes (normalized) for redraw. */
  function getAnnotStrokes() {
    return active ? active._annotStrokes : [];
  }

  /** Start/stop live subtitles via Web Speech API. opts.lang = BCP-47 locale. */
  function setSubtitles(on, opts) {
    if (!active) return false;
    return active.setSubtitles(on, opts || {});
  }

  /** M26: troca lang do STT in-place sem disparar broadcast off→on. */
  function changeSubtitlesLang(lang) {
    if (!active) return false;
    return active.changeSubtitlesLang(lang);
  }

  window.VPSMVideoCall = {
    connect: connect,
    disconnect: disconnect,
    sendChat: sendChat,
    setMuted: setMuted,
    setVideoOff: setVideoOff,
    setBudgetKbps: setBudgetKbps,
    setScreenShare: setScreenShare,
    startRecording: startRecording,
    stopRecording: stopRecording,
    setAudioFirstMode: setAudioFirstMode,
    getQualityPresets: getQualityPresets,
    applyQualityProfile: applyQualityProfile,
    sendFile: sendFile,
    setPrivacyFrost: setPrivacyFrost,
    sendWhiteboardEvent: sendWhiteboardEvent,
    getWhiteboardStrokes: getWhiteboardStrokes,
    getAnnotStrokes: getAnnotStrokes,
    setSubtitles: setSubtitles,
    changeSubtitlesLang: changeSubtitlesLang,
    isSubtitlesSupported: function () {
      // Web Speech (Chrome/Edge nativos) OU whisper-local (VPSMSTT bridge
      // pro WhisperLive). Sem o segundo check, Firefox/Safari nunca
      // mostravam o toggle de subtitles mesmo com backend disponivel.
      const webSpeech = !!(window.SpeechRecognition || window.webkitSpeechRecognition);
      const whisperLocal = !!(window.VPSMSTT && typeof window.VPSMSTT.connect === 'function');
      return webSpeech || whisperLocal;
    },
    // Device control (camera / mic / speaker) — works in-call and out-of-call
    listDevices: listDevices,
    probeDevicePermission: probeDevicePermission,
    setCameraDevice: setCameraDevice,
    setMicDevice: setMicDevice,
    setMicGain: setMicGain,
    setMicProcessing: setMicProcessing,
    getMicLevel: getMicLevel,
    setSpeakerDevice: setSpeakerDevice,
    playTestTone: playTestTone,
    createMicLevelMonitor: createMicLevelMonitor,
    onDeviceChange: function (cb) {
      if (navigator.mediaDevices && navigator.mediaDevices.addEventListener) {
        navigator.mediaDevices.addEventListener('devicechange', cb);
      }
    },
    isE2EESupported: function () {
      return !!(window.VPSMVideoCallE2EE && window.VPSMVideoCallE2EE.isSupported());
    },
    sendState: sendState,
  };

  // ---- Microfone: volume de envio, processamento e medidor --------------
  // Faixa do volume de envio. 4x (+12 dB) cabe porque o limitador do
  // pipeline segura os picos; sem ele, acima de ~2x a voz clipava.
  const MIC_GAIN_MIN = 0.25;
  const MIC_GAIN_MAX = 4.0;
  function clampMicGain(v) {
    const n = Number(v);
    if (!isFinite(n) || n <= 0) return 1.0;
    return Math.min(MIC_GAIN_MAX, Math.max(MIC_GAIN_MIN, n));
  }
  // Processamento do navegador na captura. Default = tudo ligado (o que a
  // chamada sempre usou). Desligar o cancelamento de eco sem fone faz o outro
  // lado se ouvir de volta — a UI avisa.
  function normMicProc(p) {
    p = p || {};
    return {
      noiseSuppression: p.noiseSuppression !== false,
      echoCancellation: p.echoCancellation !== false,
      autoGainControl:  p.autoGainControl  !== false,
    };
  }
  function micConstraints(deviceId, proc) {
    const a = normMicProc(proc);
    if (deviceId && deviceId !== 'default') a.deviceId = { exact: deviceId };
    return a;
  }
  // Nivel pro medidor: RMS em dBFS mapeado em 0..100 (-60 dB .. 0 dB) — a
  // escala em dB acompanha o ouvido; a media linear de FFT antiga deixava
  // fala normal em 20-30% e grito em 100%. `limiting` = o limitador esta
  // segurando mais de 4 dB: volume alto demais pra esse microfone.
  function readMicLevel(analyser, buf, limiter) {
    analyser.getFloatTimeDomainData(buf);
    let sum = 0, peak = 0;
    for (let i = 0; i < buf.length; i++) {
      const v = buf[i];
      sum += v * v;
      const a = v < 0 ? -v : v;
      if (a > peak) peak = a;
    }
    const rms = Math.sqrt(sum / buf.length);
    const db = rms > 0 ? 20 * Math.log10(rms) : -100;
    const level = Math.max(0, Math.min(100, Math.round((db + 60) / 60 * 100)));
    const comp = limiter && limiter.comp;
    const reduction = comp && typeof comp.reduction === 'number' ? comp.reduction : 0;
    return { level: level, peak: peak, limiting: reduction < -4 };
  }
  // Limitador: DynamicsCompressor em -3 dBFS, ratio 20. O no do Web Audio
  // aplica um makeup gain AUTOMATICO de (1/ganho_na_escala_cheia)^0.6 — aqui,
  // +1.71 dB em todo sinal, com ou sem pico. Sem compensar, "100%" saia mais
  // alto que o microfone. `input` e `output` delimitam a cadeia; o `comp` fica
  // exposto pro medidor ler `reduction`.
  const MIC_LIM_THRESHOLD = -3;
  const MIC_LIM_RATIO = 20;
  function makeMicLimiter(ctx) {
    const comp = ctx.createDynamicsCompressor();
    comp.threshold.value = MIC_LIM_THRESHOLD;
    comp.knee.value = 0;
    comp.ratio.value = MIC_LIM_RATIO;
    comp.attack.value = 0.002;
    comp.release.value = 0.12;
    const escalaCheiaDb = MIC_LIM_THRESHOLD - MIC_LIM_THRESHOLD / MIC_LIM_RATIO;
    const makeupDb = -0.6 * escalaCheiaDb;
    const compensa = ctx.createGain();
    compensa.gain.value = Math.pow(10, -makeupDb / 20);
    comp.connect(compensa);
    return { input: comp, output: compensa, comp: comp };
  }

  // ---- Call -----------------------------------------------------------

  function Call(opts) {
    this.opts = opts || {};
    this.roomId = opts.roomId;
    this.token = opts.token;
    // ticketProvider: async () => string. Quando setado, signaling WS usa
    // ?ticket=<X> em vez de ?token=<JWT>. Não loga JWT em access logs.
    this.ticketProvider = opts.ticketProvider || null;
    // FIX: identidade estável de cliente p/ eviction de fantasma no
    // reconnect. Auth → uuid persistido em sessionStorage (por-aba, por-sala);
    // guests não geram nada (server deriva do jti do token), então fica vazio.
    this.clientId = this._resolveClientId();
    this.displayName = opts.displayName || 'Você';
    this.codecPref = opts.codec || 'auto';
    // Quality profile resolves into 4 pieces: width/height/fps for the
    // local camera, videoKbps for the outbound video cap, audioKbps for
    // Opus. If a name is given, fold the preset into the explicit fields;
    // explicit fields in opts win over preset (so a "custom" caller can
    // override one knob).
    const presetName = (typeof opts.quality === 'string') ? opts.quality : null;
    const preset = presetName && QUALITY_MODES[presetName] ? QUALITY_MODES[presetName] : QUALITY_MODES.medium;
    this.qualityName = presetName || 'custom';
    this.videoWidth  = opts.videoWidth  || preset.width;
    this.videoHeight = opts.videoHeight || preset.height;
    this.videoFps    = opts.videoFps    || preset.fps;
    this.videoKbps   = (opts.videoKbps != null ? opts.videoKbps : preset.videoKbps);
    this.audioKbps   = opts.audioKbps   || preset.audioKbps;
    // Keep this for back-compat with old budget slider (mirrors videoKbps).
    this.budgetKbps  = opts.budgetKbps  || this.videoKbps || 60;
    // audioOnly: vem do opts (legacy) OU do preset videoOff. Quando true,
    // getUserMedia não pede câmera.
    this.audioOnly   = !!opts.audioOnly || !!preset.videoOff;
    // micOff: entrar sem microfone (so video). Par do audioOnly.
    this.micOff      = !!opts.micOff;
    // Volume de envio do mic (1.0 = neutro) e o processamento do navegador
    // (supressao de ruido / cancelamento de eco / ganho automatico). Vem da
    // preferencia salva pelo shell. Sem isto a chamada nascia sempre em 1.0x
    // e o slider mostrava um valor que nao estava aplicado.
    this._micGain    = clampMicGain(opts.micGain);
    this._micProc    = normMicProc(opts.micProcessing);
    // opusTweak: liga DTX/FEC-off/maxavgbitrate via SDP munging. Aplica
    // no createOffer/createAnswer no PeerConn.
    this.opusTweak   = (opts.opusTweak != null ? !!opts.opusTweak : !!preset.opusTweak);
    this.videosEl = opts.videosEl;
    // Device IDs picked by the user in the lobby. Empty / 'default' = let
    // browser pick. Stored per-Call so a hot reload of preferences doesn't
    // disturb an active call.
    this.deviceIds = {
      camera:  (opts.deviceIds && opts.deviceIds.camera)  || '',
      mic:     (opts.deviceIds && opts.deviceIds.mic)     || '',
      speaker: (opts.deviceIds && opts.deviceIds.speaker) || '',
    };
    this.cbState = opts.onState || function () {};
    this.cbStats = opts.onStats || function () {};
    this.cbChat = opts.onChat || function () {};
    const userOnError = opts.onError || function () {};
    this.cbError = function (msg) {
      console.error('[vpsm:vc] ERROR:', msg);
      // Traduz erros legacy crus em inglês que ainda possam vir do server.
      const translated = translateLegacyError(msg);
      try { userOnError(translated); } catch (_) {}
    };
    this.cbFileProgress = opts.onFileProgress || function () {};
    this.cbFileReceived = opts.onFileReceived || function () {};
    this.cbWhiteboard = opts.onWhiteboard || function () {};
    this.cbRecordingReady = opts.onRecordingReady || null;

    // Whiteboard shared state: every stroke this peer has seen (drawn locally
    // OR received live), in normalized [0..1] coords. Source of truth for the
    // snapshot sent to a (re)joining peer on DC open, and for the resize redraw.
    // Each entry: {from,to,color,width,alpha?,_from?} — _from set for remote-origin
    // strokes (used for per-author coloring); absent => locally drawn.
    //
    // the SAME transport carries two surfaces, kept in separate buffers
    // so a clear/snapshot on one never bleeds into the other. `_wbStrokes` is the
    // opaque collaborative board (surface 'board', the default); `_annotStrokes`
    // is the transparent annotation layer anchored over the shared screen
    // (surface 'screen'). Routing is by `ev.surface` in _wbRecord.
    this._wbStrokes = [];
    this._annotStrokes = [];

    this.ws = null;
    this.wsBackoff = RECONNECT_BACKOFF_MIN;
    this.peerId = null;
    this.iceServers = []; // populated from server `joined` message
    this.peers = Object.create(null); // peerId -> PeerConn
    this.localStream = null;
    this.statsTimer = null;
    this.stopped = false;
    this.networkListenerInstalled = false;

    // E2EE: stored so PeerConns created later (e.g. peer-joined mid-call)
    // can hook frame encryption from the start.
    this.e2eePassphrase = opts.e2eePassphrase || '';
    this.e2eeActive = false;

    // Screen share: separate track replaced into the existing sender.
    this.cameraTrack = null;   // original camera track (saved for revert)
    this.screenStream = null;
    this.screenSharing = false;

    // Recording: client-side only. Composes local + remote video onto a
    // hidden canvas, captures via canvas.captureStream() into MediaRecorder.
    this.recorder = null;
    this.recorderChunks = [];
    this.recordingStarted = 0;

    // Privacy frost (full-frame blur). Honest label — does NOT do
    // background-only segmentation. Useful when you need to step away
    // from the camera without disabling video entirely.
    this.frostCanvas = null;
    this.frostVideoEl = null;
    this.frostRAF = 0;
    this.frostActive = false;

    // Audio-first auto-degrade
    this.audioFirstMode = !!opts.audioFirstMode;
    this.poorNetworkSince = 0;
    this.autoVideoOff = false;

    // AV1 runtime fallback
    this.av1Probe = { lowFpsSince: 0, downgraded: false };

    // Session totals (POSTed to /api/videocall/sessions on hangup so the
    // server can show "bandwidth history" in the dashboard).
    this.callStartedAt = 0;
    this.cumBytesSent = 0;
    this.cumBytesRecv = 0;
    this.lastCodec = '';
    this.lastConnType = 'direct';

    // Auto-reconnect state preservation. When the signaling WS closes
    // without a clean leave, we hang on to the original opts and retry —
    // device picks, passphrase, quality preset all survive untouched.
    this._origOpts = opts;
    this._userInitiatedHangup = false;

    // Active speaker detection: one AnalyserNode per peer's incoming
    // audio stream. Cheap (~256 FFT) and gives us a 0-100 level value.
    this._peerAudioMonitors = {}; // peerId → { ctx, analyser, data, level, raf }
    this._activeSpeaker = null;

    // "You are muted but speaking" detector — runs only when localStream
    // is muted, samples local mic level, fires onState({type:'speaking-while-muted'})
    // when level stays above threshold for >800ms.
    this._localSpeakingMon = null;

    // File transfer offset tracking per (peerID, fileID) for resume.
    this._fileTxOffsets = {}; // fid → { offset, file, peerID }
  }

  Call.prototype.start = async function () {
    // M22 QW11: Wake Lock evita tela apagar durante chamada (mobile crítico).
    // Auto-re-acquire em visibilitychange ao voltar pra foreground.
    (async () => {
      try {
        if (navigator.wakeLock && !this._wakeLock) {
          this._wakeLock = await navigator.wakeLock.request('screen');
          this._wakeLock.addEventListener('release', () => { this._wakeLock = null; });
        }
      } catch (e) { console.warn('[vpsm:vc] wakeLock falhou: ' + e.message); }
    })();
    this._wakeLockVisListener = async () => {
      if (document.visibilityState === 'visible' && !this._wakeLock && !this.stopped) {
        try { this._wakeLock = await navigator.wakeLock.request('screen'); } catch (_) {}
      }
    };
    document.addEventListener('visibilitychange', this._wakeLockVisListener);
    // 1. Local media (mic + maybe camera). Honor the lobby's device picks
    //    when they're not "default".
    // micOff: o lobby ja provou que nao ha microfone utilizavel. Pedir audio
    // assim mesmo faria o gUM inteiro falhar e levaria o video junto.
    let audio = false;
    if (!this.micOff) {
      audio = micConstraints(this.deviceIds.mic, this._micProc);
    }
    let video = false;
    if (!this.audioOnly) {
      video = {
        width:     { ideal: this.videoWidth  },
        height:    { ideal: this.videoHeight },
        frameRate: { ideal: this.videoFps    },
      };
      if (this.deviceIds.camera && this.deviceIds.camera !== 'default') {
        video.deviceId = { exact: this.deviceIds.camera };
      } else if (this.deviceIds.camera === 'environment' || this.deviceIds.camera === 'user') {
        // Mobile facingMode shortcut (set by mobile flip-camera button).
        delete video.deviceId;
        video.facingMode = { ideal: this.deviceIds.camera };
      }
    }
    // getUserMedia e all-or-nothing: falta UM tipo (camera desconectada, mic
    // tomado por outro app, deviceId salvo que sumiu) e o pedido INTEIRO
    // falha. Antes isso virava excecao e trancava a entrada na chamada.
    // Agora tenta um plano em degradacao e entra com o que existir — o
    // motor e o unico ponto por onde TODAS as entradas passam (lobby,
    // pular-lobby, recovery, convidado), entao a rede de seguranca fica aqui.
    const plano = [];
    const semId = (c) => {
      if (!c || typeof c !== 'object') return c;
      const cp = Object.assign({}, c);
      delete cp.deviceId; delete cp.facingMode;
      return cp;
    };
    const passo = (a, v, nota) => { if (a || v) plano.push({ audio: a, video: v, nota: nota }); };
    passo(audio, video, '');
    const temIdFixo = (audio && audio.deviceId) || (video && (video.deviceId || video.facingMode));
    if (temIdFixo) passo(semId(audio), semId(video), 'o dispositivo salvo não existe mais — entrei com o padrão do sistema');
    if (audio && video) {
      passo(semId(audio), false, 'sem câmera disponível — entrei só com áudio');
      passo(false, semId(video), 'sem microfone disponível — você entrou, mas ninguém vai te ouvir');
    }
    let ultimoErro = null;
    this.degradedNote = '';
    for (const p of plano) {
      try {
        this.localStream = await navigator.mediaDevices.getUserMedia({ audio: p.audio, video: p.video });
        this.degradedNote = p.nota || '';
        break;
      } catch (e) { ultimoErro = e; }
    }
    if (!this.localStream) {
      // Plano vazio = chamaram com audioOnly E micOff (nada a pedir). Caso
      // contrario, ultimoErro tem o motivo real da ultima tentativa.
      throw new Error(ultimoErro
        ? humanizeGumError(ultimoErro)
        : 'Nenhuma câmera ou microfone utilizável neste computador — conecte um aparelho e tente de novo.');
    }
    this.cameraTrack = this.localStream.getVideoTracks()[0] || null;
    // audioOnly reflete o que REALMENTE veio. Sem isso o resto do motor
    // (toggleVideo, applyQualityProfile, o sender de video) acha que existe
    // camera e opera sobre um track nulo.
    if (!this.cameraTrack) this.audioOnly = true;
    if (this.degradedNote) {
      try { this.cbState({ type: 'devices-degraded', note: this.degradedNote }); } catch (_) {}
    }
    this.callStartedAt = Date.now();

    // ---- Volume de envio do mic ------------------------------------------
    // mic cru → GainNode (volume) → limitador → destino → track ENVIADO.
    // O track enviado fica estavel a chamada inteira: trocar de microfone ou
    // de processamento so religa a fonte (_attachMicSource), sem replaceTrack
    // nos peers. O cru fica em _micGainRawTrack e e o que a transcricao le
    // (getMicStreamForSTT): a voz como sai do microfone, antes do volume, do
    // codec e da rede.
    try {
      this._buildMicPipeline(this.localStream.getAudioTracks()[0]);
    } catch (e) {
      console.warn('[vpsm:vc] pipeline de volume do mic falhou (sem ajuste de volume): ' + e.message);
    }

    this.attachLocalPreview();

    // Alerta "falando mutado" (ver _startLocalSpeakingMonitor).
    this._startLocalSpeakingMonitor();

    // 2. Signaling WS. The promise resolves when we receive `joined`.
    await this.openSignaling();

    // 3. Stats loop.
    this.statsTimer = setInterval(() => this.collectStats(), STATS_INTERVAL_MS);

    // 4. Network listeners for ICE restart. Salva handler ref pra remover
    // em Call.stop (sem isso, reconexões acumulavam listeners e ICE
    // disparava em cascata). (Auditoria A4)
    if (!this.networkListenerInstalled) {
      this.networkListenerInstalled = true;
      this._netHandler = () => this.handleNetworkChange();
      window.addEventListener('online', this._netHandler);
      if (navigator.connection && navigator.connection.addEventListener) {
        navigator.connection.addEventListener('change', this._netHandler);
      }
    }

    // 5. Background keepalive via Web Worker. Browsers throttle setTimeout/
    // setInterval em abas ocultas (Chrome: ~1Hz após 5min, mais agressivo
    // depois). Quando o user fica em PiP em outra aba, o ping aplicacional
    // e o reconnect setTimeout ficam congelados, e middleboxes/proxies
    // fecham a WS por idle. Worker NÃO é throttled.
    this._startBgKeepalive();

    // 6. Visibility-change: ao voltar pra aba, fazer health-check imediato.
    // Se desconectou em background, dispara reconnect já.
    // Guard contra dupla-instalação (defensive — start() é singleton, mas
    // se algum dia rodar 2x acumularia listeners).
    if (this._visHandler) {
      try { document.removeEventListener('visibilitychange', this._visHandler); } catch (_) {}
    }
    this._visHandler = () => {
      if (document.visibilityState === 'visible' && !this.stopped) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
          // FIX Onda3 A2: silent-death detection. Se passaram >70s sem
          // qualquer mensagem do server (ping → pong demora ~25s),
          // o middleware provavelmente dropou. Força reopen ANTES do
          // browser detectar via onclose (que pode demorar 100s+).
          if (this._lastWsMessageAt && Date.now() - this._lastWsMessageAt > 70000) {
            console.warn('[vpsm:vc] WS silent >70s — forçando reconexão');
            try { this.ws.close(4000, 'silent-death'); } catch (_) {}
            this.reopenSignaling();
          }
        } else if (this.ws && this.ws.readyState >= WebSocket.CLOSING) {
          // WS morreu enquanto background; reconecta agora.
          this.reopenSignaling();
        }
        // Health-check peers. Debounce: só dispara restartIce em um peer
        // se passou >15s desde o último — sem isso, alternar abas rapidamente
        // com ICE oscilando entre disconnected↔failed disparava restarts em
        // cascata e travava o signaling.
        const now = Date.now();
        for (const id in this.peers) {
          const peer = this.peers[id];
          const pc = peer && peer.pc;
          if (!pc) continue;
          const st = pc.iceConnectionState;
          if (st === 'disconnected' || st === 'failed') {
            const last = peer._lastIceRestartAt || 0;
            if (now - last > 15000) {
              peer._lastIceRestartAt = now;
              try { pc.restartIce(); } catch (_) {}
            }
          }
        }
      }
    };
    document.addEventListener('visibilitychange', this._visHandler);

    this.cbState({ type: 'connected' });
  };

  // Worker inline que dispara ticks fora do throttle de aba background.
  Call.prototype._startBgKeepalive = function () {
    if (this._bgWorker) return;
    try {
      const src = '(' + function () {
        let id = 0;
        self.onmessage = (e) => {
          const m = e.data || {};
          if (m.cmd === 'start') {
            if (id) clearInterval(id);
            id = setInterval(() => self.postMessage({ tick: 1 }), m.interval || 20000);
          } else if (m.cmd === 'stop') {
            if (id) clearInterval(id); id = 0;
          }
        };
      }.toString() + ')()';
      const url = URL.createObjectURL(new Blob([src], { type: 'application/javascript' }));
      this._bgWorker = new Worker(url);
      this._bgWorkerURL = url;
      this._bgWorker.onmessage = () => {
        if (this.stopped) return;
        // Ping aplicacional. Server responde 'pong' (ws.go:306-307), o que
        // reseta o pong-deadline server-side e também faz qualquer middlebox
        // (Cloudflare/Traefik/NAT) reiniciar o idle counter.
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
        }
        // Se houve agendamento de reconnect que ficou travado no setTimeout
        // throttled, executa agora.
        if (this._pendingReconnect && !this.stopped) {
          this._pendingReconnect = false;
          this.reopenSignaling();
        }
      };
      // 20s: largo o suficiente pra não floodar; curto o suficiente pra
      // bater dentro de qualquer idle timeout razoável (Cloudflare 100s,
      // Traefik default 60s).
      this._bgWorker.postMessage({ cmd: 'start', interval: 20000 });
    } catch (_) { /* Worker indisponível: degrade graciosamente */ }
  };

  Call.prototype._stopBgKeepalive = function () {
    if (this._bgWorker) {
      try { this._bgWorker.postMessage({ cmd: 'stop' }); } catch (_) {}
      try { this._bgWorker.terminate(); } catch (_) {}
      this._bgWorker = null;
    }
    if (this._bgWorkerURL) {
      try { URL.revokeObjectURL(this._bgWorkerURL); } catch (_) {}
      this._bgWorkerURL = null;
    }
  };

  Call.prototype.stop = function () {
    if (this.stopped) return;
    this.stopped = true;
    this._userInitiatedHangup = true;
    if (this.statsTimer) clearInterval(this.statsTimer);
    if (this._turnRefreshTimer) { clearTimeout(this._turnRefreshTimer); this._turnRefreshTimer = null; }
    // M22 QW11: release wakeLock e remove visibilitychange listener.
    if (this._wakeLockVisListener) {
      try { document.removeEventListener('visibilitychange', this._wakeLockVisListener); } catch (_) {}
      this._wakeLockVisListener = null;
    }
    if (this._wakeLock) {
      try { this._wakeLock.release(); } catch (_) {}
      this._wakeLock = null;
    }
    // M22 QW7: MediaRecorder cleanup completo — null handlers ANTES de stop()
    // pra evitar ondataavailable continuar pushando chunks em closure orphan
    // após stop. recorderChunks resetado pra liberar memória.
    if (this.recorder && this.recorder.state !== 'inactive') {
      try { this.recorder.ondataavailable = null; } catch (_) {}
      try { this.recorder.onstop = null; } catch (_) {}
      try { this.recorder.stop(); } catch (_) {}
    }
    this.recorder = null;
    this.recorderChunks = [];
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      try { this.ws.send(JSON.stringify({ type: 'leave' })); } catch (_) {}
    }
    if (this.ws) {
      try { this.ws.close(); } catch (_) {}
      this.ws = null;
    }
    for (const id in this.peers) this.peers[id].close();
    this.peers = Object.create(null);
    if (this.localStream) {
      this.localStream.getTracks().forEach(t => t.stop());
      this.localStream = null;
    }
    if (this.screenStream) {
      this.screenStream.getTracks().forEach(t => t.stop());
      this.screenStream = null;
    }
    // Cleanup do "you are muted speaking" detector. O clone da audio track +
    // AudioContext ficavam vivos após hangup, mantendo o microfone "in use"
    // até GC — segunda chamada quebrava com "device busy". (Auditoria C3)
    this._stopLocalSpeakingMonitor();
    // Cleanup do pipeline de volume (AudioContext + track cru preservado).
    if (this._micGainCtx) {
      try {
        if (this._micGainSrc) this._micGainSrc.disconnect();
        if (this._micGainNode) this._micGainNode.disconnect();
        if (this._micGainCtx.state !== "closed") this._micGainCtx.close().catch(() => {});
      } catch (_) {}
      this._micGainCtx = null;
      this._micGainNode = null;
      this._micGainSrc = null;
      this._micLimiter = null;
      this._micAnalyser = null;
    }
    if (this._micGainRawTrack) {
      try { this._micGainRawTrack.stop(); } catch (_) {}
      this._micGainRawTrack = null;
    }
    // Cleanup do background frost (canvas + RAF). Se ficou ativo, libera.
    if (this.frostActive) {
      this.frostActive = false;
      if (this.frostRAF) { try { cancelAnimationFrame(this.frostRAF); } catch (_) {} this.frostRAF = 0; }
      this.frostCanvas = null;
      if (this.frostVideoEl) { try { this.frostVideoEl.srcObject = null; } catch (_) {} this.frostVideoEl = null; }
    }
    // Subtitle recognition se estiver rodando.
    // M26 BUG#1: campo era `_subtitles` (não existe) — handle real é `_subtitlesHandle`.
    // Sem este fix, ao desligar a chamada, o WebSocket do whisper-local +
    // AudioContext + AudioWorkletNode vazavam. Mic ficava "in use" pra próxima
    // chamada (NotReadableError). Leak crônico em sessões frequentes.
    if (this._subtitlesActive) {
      this._subtitlesActive = false;
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      if (this._captionTrailingTimer) { clearTimeout(this._captionTrailingTimer); this._captionTrailingTimer = null; }
      this._captionTrailingPayload = null;
      // Clear local caption timer + force-hide qualquer overlay ainda visível.
      if (this._localCapClearTimer) { clearTimeout(this._localCapClearTimer); this._localCapClearTimer = null; }
      try { this.updatePeerCaption('me', '', true); } catch (_) {}
    }
    // Network listeners cleanup (A4)
    if (this.networkListenerInstalled && this._netHandler) {
      try { window.removeEventListener('online', this._netHandler); } catch (_) {}
      if (navigator.connection && navigator.connection.removeEventListener) {
        try { navigator.connection.removeEventListener('change', this._netHandler); } catch (_) {}
      }
      this.networkListenerInstalled = false;
      this._netHandler = null;
    }
    // visibilitychange handler + bg keepalive worker.
    if (this._visHandler) {
      try { document.removeEventListener('visibilitychange', this._visHandler); } catch (_) {}
      this._visHandler = null;
    }
    this._stopBgKeepalive();
    // Active-speaker analysers por peer — libera AudioContexts pendentes.
    if (this._peerAudioMonitors) {
      for (const id in this._peerAudioMonitors) {
        const m = this._peerAudioMonitors[id];
        if (m && m.raf) { try { cancelAnimationFrame(m.raf); } catch (_) {} }
        if (m && m.ctx && m.ctx.state !== "closed") { try { m.ctx.close().catch(()=>{}); } catch (_) {} }
      }
      this._peerAudioMonitors = {};
    }
    // encerra qualquer janela de PiP preservada e zera o estado de
    // reattach antes de limpar a grade (innerHTML='' removeria o elemento e
    // deixaria a janela do SO órfã / o timer pendente).
    if (this._pipReattachTimer) { try { clearTimeout(this._pipReattachTimer); } catch (_) {} this._pipReattachTimer = null; }
    if (this._pipDetached) {
      for (const cid in this._pipDetached) {
        const d = this._pipDetached[cid];
        try {
          if (d && d.v && document.pictureInPictureElement === d.v && document.exitPictureInPicture) {
            document.exitPictureInPicture().catch(() => {});
          }
        } catch (_) {}
      }
      this._pipDetached = {};
    }
    if (this.videosEl) this.videosEl.innerHTML = '';

    // POST final session totals so the dashboard "histórico" knows about it.
    // Fire-and-forget; no auth header here because the existing JWT cookie
    // covers the request (same-origin POST).
    const durationS = this.callStartedAt ? Math.round((Date.now() - this.callStartedAt) / 1000) : 0;
    // Guest tokens não acessam /api/videocall/sessions (endpoint protected
     // por user logado). Skip pra não gerar 401/400 no console.
    if (durationS > 0 && (this.cumBytesSent + this.cumBytesRecv) > 0 && !this.opts.guestMode) {
      try {
        const headers = { 'Content-Type': 'application/json' };
        if (this.token) headers['Authorization'] = 'Bearer ' + this.token;
        fetch('/api/videocall/sessions', {
          method: 'POST',
          headers: headers,
          credentials: 'include',
          body: JSON.stringify({
            room_id: this.roomId,
            started_at: Math.round(this.callStartedAt / 1000),
            duration_s: durationS,
            bytes_sent: Math.max(0, Math.round(this.cumBytesSent || 0)),
            bytes_recv: Math.max(0, Math.round(this.cumBytesRecv || 0)),
            codec: this.lastCodec || '',
            connection_type: this.lastConnType || 'direct',
          }),
        }).catch(() => {});
      } catch (_) {}
    }
    this.cbState({ type: 'disconnected' });
  };

  // -- Signaling -------------------------------------------------------

  // FIX: resolve a identidade estável de cliente p/ usuários autenticados.
  // Persiste em sessionStorage chaveado por sala — sobrevive ao reconnect de WS
  // (mesma aba) mas NÃO vaza entre abas (cada aba = participante legítimo
  // distinto, preservando o guard de 2-abas do server). Guests retornam ''
  // (o server deriva ClientID do jti do token). Fallback efêmero se
  // sessionStorage estiver indisponível (modo privado restrito).
  Call.prototype._resolveClientId = function () {
    if (this.opts.guestMode) return '';
    const key = 'vpsm:vc:cid:' + this.roomId;
    try {
      let id = sessionStorage.getItem(key);
      if (!id || !/^[A-Za-z0-9]{1,64}$/.test(id)) {
        id = _genClientId();
        sessionStorage.setItem(key, id);
      }
      return id;
    } catch (_) {
      return _genClientId();
    }
  };

  Call.prototype.openSignaling = function () {
    return new Promise(async (resolve, reject) => {
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      // Guest mode (entrada por PIN): usa /ws/videocall-guest, token carrega
      // a room embedded — não precisa passar room_id na query (e o server
      // rejeitaria divergência por segurança).
      const wsPath = this.opts.guestMode ? '/ws/videocall-guest' : '/ws/videocall';
      const roomParam = this.opts.guestMode ? '' : ('&room_id=' + encodeURIComponent(this.roomId));
      // ticketProvider preferred (mobile cookie-auth flow); fallback token.
      // Guest mode SEMPRE usa o guest token JWT (não tem ws-ticket pra guests).
      let authParam = '';
      if (!this.opts.guestMode && this.ticketProvider) {
        try {
          const ticket = await this.ticketProvider();
          if (ticket) authParam = '?ticket=' + encodeURIComponent(ticket);
        } catch (_) {}
      }
      if (!authParam) authParam = '?token=' + encodeURIComponent(this.token);
      // FIX: client_id como param próprio (começa com &, depois de
      // authParam/roomParam). Só pra auth — guest não manda (server usa jti).
      const clientParam = (!this.opts.guestMode && this.clientId)
        ? ('&client_id=' + encodeURIComponent(this.clientId)) : '';
      // `resume=1` avisa o servidor que este WS é a REABERTURA de
      // uma chamada em curso, não uma ligação nova — assim o rejoin não toca
      // a campainha nos outros aparelhos. Antes disso, todo deploy (que
      // derruba o WS e dispara o reopen) fazia o primeiro cliente
      // a reconectar parecer "o primeiro da sala", e o telefone tocava de
      // novo no meio da conversa. A flag só sabe SILENCIAR: o servidor a usa
      // apenas para suprimir o toque, nunca para provocar um.
      const resumeParam = this._resuming ? '&resume=1' : '';
      const url = proto + '//' + location.host + wsPath + authParam + roomParam + clientParam + resumeParam;
      // FIX (observabilidade): loga o client_id em CADA (re)conexão.
      // Se o mesmo valor aparecer no reconnect, a eviction server-side casa;
      // valores diferentes (ou 'none') explicam fantasma que não some.
      console.log('[vpsm:vc] WS connecting guest=' + (!!this.opts.guestMode) +
        ' client_id=' + (this.clientId ? this.clientId.slice(-8) : 'none'));
      const ws = new WebSocket(url);
      this.ws = ws;
      let resolved = false;
      ws.onopen = () => {
        this.wsBackoff = RECONNECT_BACKOFF_MIN;
        this._wsRetries = 0;
        this._lastWsMessageAt = Date.now();
      };
      ws.onmessage = (ev) => {
        // FIX Onda3 A2: track last-message timestamp pra pong watchdog detectar
        // silêncio prolongado (rede cortou middleware sem fechar TCP).
        this._lastWsMessageAt = Date.now();
        let msg;
        try { msg = JSON.parse(ev.data); } catch (_) { return; }
        if (msg.type === 'joined' && !resolved) {
          resolved = true;
          this.handleJoined(msg.payload);
          resolve();
        } else {
          this.handleSignal(msg);
        }
      };
      ws.onerror = () => {
        if (!resolved) reject(new Error('WS error before join'));
      };
      ws.onclose = (ev) => {
        if (this.stopped) return;
        if (!resolved) { reject(new Error('WS closed: ' + ev.code)); return; }
        // Reconnect with backoff. Limit pra 8 tentativas — depois disso
        // emite 'reconnect-gave-up' pra UI mostrar "sessão expirou/sem rede".
        // Sem isso, guests com token expirado ficavam em loop infinito.
        this._wsRetries = (this._wsRetries || 0) + 1;
        if (this._wsRetries > 8) {
          this.cbState({ type: 'reconnect-gave-up' });
          return;
        }
        this.cbState({ type: 'reconnecting' });
        // Em aba background, setTimeout é throttled (≥1s, e progressivamente
        // pior). Marca pending — o worker tick (20s, não throttled) também
        // dispara o reopen. O setTimeout aqui ainda corre quando visível.
        this._pendingReconnect = true;
        setTimeout(() => {
          if (!this.stopped && this._pendingReconnect) {
            this._pendingReconnect = false;
            this.reopenSignaling();
          }
        }, this.wsBackoff);
        this.wsBackoff = Math.min(this.wsBackoff * 2, RECONNECT_BACKOFF_MAX);
      };
    });
  };

  // cleanup de UM monitor de áudio (AudioContext + analyser + RAF).
  // Extraído pra ser reusável entre reopenSignaling, o reconcile do
  // handleJoined, peer-left e a adoção — antes era copy-paste em 4 lugares.
  Call.prototype._teardownPeerMonitor = function (id) {
    if (!this._peerAudioMonitors || !this._peerAudioMonitors[id]) return;
    const m = this._peerAudioMonitors[id];
    try { if (m.raf) cancelAnimationFrame(m.raf); } catch (_) {}
    try { if (m.ctx && m.ctx.state !== 'closed') m.ctx.close().catch(() => {}); } catch (_) {}
    delete this._peerAudioMonitors[id];
  };

  Call.prototype.reopenSignaling = function () {
    // Mid-call reconnect que SOBREVIVE ao deploy.
    //   A mídia é P2P/relay-coturn e NÃO passa pelo servidor de signaling.
    //   Um deploy (`systemctl restart vps-manager`, ~2s) só derruba o WS —
    //   as PeerConnections continuam `connected` (ICE consent-freshness é
    //   peer-a-peer, RFC 7675, não atravessa o servidor reiniciando). Então
    //   PRESERVAMOS as PCs `connected` e as ADOTAMOS quando o WS reabre e o
    //   server nos atribui um peer id novo (re-key old->new via ClientID
    //   estável em ensurePeer -> _rekeyPeer). Device picks, passphrase e
    //   quality preset já sobrevivem (vivem em `this`, intocados pelo WS close).
    //
    //   Só destruímos PCs que NÃO estão `connected` (connecting/failed/
    //   disconnected/closed) — essas não dá pra adotar; são recriadas a partir
    //   do snapshot. Degradação graciosa: se TUDO caiu, vira o close+recreate
    //   de sempre, sem regressão. O leak de AudioContext (auditoria V4) segue
    //   coberto: monitor só é destruído junto do PC que ele observa.
    if (this._userInitiatedHangup) return;
    this.cbState({ type: 'reconnecting' });
    let preserved = 0, dropped = 0;
    for (const id of Object.keys(this.peers)) {
      const peer = this.peers[id];
      const cs = peer && peer.pc && peer.pc.connectionState;
      if (cs === 'connected') { preserved++; continue; }
      try { if (peer) peer.close(); } catch (_) {}
      delete this.peers[id];
      this._teardownPeerMonitor(id);
      if (this._captionClearTimers && this._captionClearTimers[id]) {
        clearTimeout(this._captionClearTimers[id]);
        delete this._captionClearTimers[id];
      }
      dropped++;
    }
    // Telemetria (premissa do fix): preserved>0 num deploy prova que a mídia
    // sobreviveu; dropped distingue queda real de janela de signaling.
    console.log('[vpsm:vc] reopen: preserved=' + preserved + ' dropped=' + dropped);
    // Enquanto este ciclo durar, o WS carrega resume=1 (ver openSignaling).
    this._resuming = true;
    this.openSignaling()
      .then(() => this.cbState({ type: 'reconnected' }))
      .catch(err => this.cbError('reconectar: ' + err.message))
      .finally(() => { this._resuming = false; });
  };

  Call.prototype.send = function (msg) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    try { this.ws.send(JSON.stringify(msg)); } catch (_) {}
  };

  Call.prototype.handleJoined = function (payload) {
    this.peerId = payload.peer_id;
    if (payload.turn && payload.turn.urls && payload.turn.urls.length) {
      const ice = { urls: payload.turn.urls };
      if (payload.turn.username) ice.username = payload.turn.username;
      if (payload.turn.credential) ice.credential = payload.turn.credential;
      this.iceServers = [ice];
      // FIX V5: TURN credentials refresh mid-call. coturn TTL típico 1h —
      // calls >1h ficavam sem TURN, conexão caía sem reconnect viável em CGNAT.
      // Agenda refetch em 0.8 * TTL.
      const ttlSec = (payload.turn.ttl > 0) ? payload.turn.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } else {
      this.iceServers = [{ urls: ['stun:stun.l.google.com:19302'] }];
    }
    // FIX (robustez): o snapshot payload.peers é a verdade autoritativa
    // de quem está na sala AGORA (server-side). Numa reconexão de WS, se
    // perdemos um `peer-left` enquanto o socket estava caído, um tile fantasma
    // sobreviveria. Reconcilia: remove qualquer peer local que NÃO esteja no
    // snapshot (exceto nós mesmos). É o que fecha o caso "rede caiu e voltou".
    const live = new Set((payload.peers || []).map(p => p.id));
    for (const id in this.peers) {
      if (id === this.peerId || live.has(id)) continue;
      // NÃO derrubar um PC `connected` aqui — ele é um SOBREVIVENTE
      // de deploy sob o id ANTIGO, esperando adoção no laço de ensurePeer logo
      // abaixo (re-key old->new via clientId estável). O reconcile original
      // assumia "ausente do snapshot ⇒ fantasma", mas pós-deploy o snapshot
      // lista o MESMO peer sob id NOVO — o id velho some de `live` por
      // construção. A guarda por connectionState distingue os dois casos:
      // connected = adotar; qualquer outro estado = fantasma real (fecha).
      const peer = this.peers[id];
      const cs = peer && peer.pc && peer.pc.connectionState;
      if (cs === 'connected') {
        console.log('[vpsm:vc] reconcile: preservando ' + id.slice(-6) + ' (connected, aguarda adoção)');
        continue;
      }
      console.log('[vpsm:vc] reconcile: removendo peer fantasma ' + id.slice(-6) + ' (ausente do snapshot)');
      try { if (peer) peer.close(); } catch (_) {}
      delete this.peers[id];
      this._teardownPeerMonitor(id);
      if (this._captionClearTimers && this._captionClearTimers[id]) {
        clearTimeout(this._captionClearTimers[id]);
        delete this._captionClearTimers[id];
      }
    }
    // Snapshot of existing peers — we initiate offers to each of them (we're
    // the "newcomer", they're "existing").
    for (const p of payload.peers || []) {
      this.ensurePeer(p.id, p.user, /*initiator=*/true, p.client_id);
    }
    this._notifyPeerCount();
    this._pruneOrphanTiles();
  };

  Call.prototype._scheduleTURNRefresh = function (seconds) {
    if (this._turnRefreshTimer) clearTimeout(this._turnRefreshTimer);
    this._turnRefreshTimer = setTimeout(() => this._refreshTURN(), seconds * 1000);
  };

  Call.prototype._refreshTURN = async function () {
    if (this._userInitiatedHangup) return;
    try {
      const r = await fetch('/api/videocall/turn', {
        headers: { 'Authorization': 'Bearer ' + this.token },
      });
      if (!r.ok) throw new Error('status ' + r.status);
      const tu = await r.json();
      if (!tu || !tu.urls || !tu.urls.length) return;
      const ice = { urls: tu.urls };
      if (tu.username) ice.username = tu.username;
      if (tu.credential) ice.credential = tu.credential;
      this.iceServers = [ice];
      // Reaplica nos PCs existentes via setConfiguration (não força ICE restart;
      // só usa as novas credenciais nos próximos candidates).
      for (const id in this.peers) {
        try { this.peers[id].pc.setConfiguration({ iceServers: this.iceServers }); }
        catch (_) {}
      }
      this._turnRetries = 0;
      console.log('[vpsm:vc] TURN credentials refreshed (peers=' + Object.keys(this.peers).length + ')');
      // Reagenda
      const ttlSec = (tu.ttl > 0) ? tu.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } catch (e) {
      // FIX Onda3 A6: exponential backoff com cap de 10min e max 5 tentativas.
      // Antes era uma única retry em 60s — se falhasse de novo, perdia TURN
      // pelo resto da call (CGNAT >2h morria silencioso).
      this._turnRetries = (this._turnRetries || 0) + 1;
      if (this._turnRetries <= 5) {
        const backoff = Math.min(60 * Math.pow(2, this._turnRetries - 1), 600);
        console.warn('[vpsm:vc] TURN refresh falhou (' + this._turnRetries + '/5): ' + e.message + ' — retry em ' + backoff + 's');
        this._scheduleTURNRefresh(backoff);
      } else {
        console.error('[vpsm:vc] TURN refresh exhausted após 5 tentativas');
        try { this.cbState({ type: 'turn-exhausted' }); } catch (_) {}
        // Última tentativa em 10min pra recuperar quando rede melhorar
        this._turnRetries = 0;
        this._scheduleTURNRefresh(600);
      }
    }
  };

  Call.prototype.handleSignal = function (msg) {
    if (msg.type !== 'ice' && msg.type !== 'pong') {
      console.log('[vpsm:vc] WS recv type=' + msg.type + ' from=' + (msg.from ? msg.from.slice(-6) : '-'));
    }
    switch (msg.type) {
      case 'peer-joined':
        if (msg.from && msg.from !== this.peerId) {
          console.log('[vpsm:vc] peer-joined ' + msg.from.slice(-6) + ' — preparing PeerConn (as answerer)');
          const info = safeParse(msg.payload) || {};
          this.ensurePeer(msg.from, info.user || 'Peer', /*initiator=*/false, info.client_id);
        }
        break;
      case 'peer-left':
        console.log('[vpsm:vc] peer-left ' + (msg.from ? msg.from.slice(-6) : '-'));
        if (this.peers[msg.from]) {
          this.peers[msg.from].close();
          delete this.peers[msg.from];
          this._notifyPeerCount();
        }
        // Rede de segurança: varre tiles órfãos (sem PeerConn vivo). Cobre o
        // caso do placeholder "sem câmera" sobreviver à saída do peer.
        this._pruneOrphanTiles();
        // M22 QW3: cleanup AudioContext + RAF do monitor de áudio do peer
        // saído. Sem isso, sala 3-4 com churn acumulava 1 AudioContext + RAF
        // por peer disconnect — leak crônico em chamadas longas. (via
        // helper compartilhado _teardownPeerMonitor.)
        this._teardownPeerMonitor(msg.from);
        // M22: limpa caption clear timer pendente do peer
        if (this._captionClearTimers && this._captionClearTimers[msg.from]) {
          clearTimeout(this._captionClearTimers[msg.from]);
          delete this._captionClearTimers[msg.from];
        }
        break;
      case 'reset':
        // O outro lado detectou que a conexao travou e esta recriando a dele.
        if (msg.from && this.peers[msg.from]) this._rebuildPeer(msg.from, 'pedido do outro lado', false);
        break;
      case 'offer':
      case 'answer':
      case 'ice': {
        const peer = this.peers[msg.from];
        if (!peer) {
          console.warn('[vpsm:vc] signal ' + msg.type + ' from unknown peer ' + (msg.from ? msg.from.slice(-6) : '-'));
        }
        if (peer) peer.handleSignal(msg);
        break;
      }
      case 'chat': {
        // Chat may also flow through DataChannel — server-side relay is the
        // fallback when the DC isn't open yet.
        const data = safeParse(msg.payload) || {};
        this.cbChat({ from: msg.from, text: data.text || '', ts: Date.now() });
        break;
      }
      case 'state': {
        const data = safeParse(msg.payload) || {};
        this.cbState({ type: 'peer-state', from: msg.from, state: data });
        break;
      }
      // Owner actions — server validou que sender é o dono. Aplica localmente
      // se sou o alvo (msg.to === this.peerId).
      case 'owner-mute': {
        if (msg.to === this.peerId) {
          this._applyOwnerMute(true, msg.from);
        }
        this.cbState({ type: 'owner-action', action: 'mute', target: msg.to, by: msg.from });
        break;
      }
      case 'owner-unmute': {
        if (msg.to === this.peerId) {
          // NÃO faz unmute automático — só notifica. Privacy: dono não pode
          // forçar abrir mic; só pede.
          this.cbState({ type: 'owner-request-unmute', by: msg.from });
        }
        break;
      }
      case 'owner-camera': {
        if (msg.to === this.peerId) {
          this._applyOwnerCamera(false, msg.from);
        }
        this.cbState({ type: 'owner-action', action: 'camera-off', target: msg.to, by: msg.from });
        break;
      }
      case 'owner-kick': {
        if (msg.to === this.peerId) {
          // Sou o alvo. Mostra mensagem e encerra.
          this._userInitiatedHangup = true;
          this.cbError('Você foi removido(a) da chamada pelo dono.');
          this.cbState({ type: 'owner-kicked', by: msg.from });
          try { this.stop(); } catch (_) {}
        } else {
          this.cbState({ type: 'owner-action', action: 'kick', target: msg.to, by: msg.from });
        }
        break;
      }
      case 'owner-lock': {
        const data = safeParse(msg.payload) || {};
        this._roomLocked = !!data.locked;
        this.cbState({ type: 'room-locked', locked: this._roomLocked, by: msg.from });
        break;
      }
      case 'owner-transfer': {
        const data = safeParse(msg.payload) || {};
        if (data.newOwner) {
          this._roomOwner = data.newOwner;
          this.cbState({ type: 'owner-transferred', newOwner: data.newOwner, by: msg.from });
        }
        break;
      }
      case 'error':
      case 'error-full':
      case 'error-conflict':
        // FIX VC #10: server agora envia tipos específicos com mensagens
        // PT-BR já formatadas. Os tipos permitem UI agir distintamente
        // (sala cheia vs conta já em uso). Backend manda mensagem PT-BR
        // direto em msg.error, então usamos como está. Fallback inglês
        // pra eventuais errors antigos sem tradução.
        this._userInitiatedHangup = true; // impede reopenSignaling em erros fatais de join
        this.cbError(msg.error || translateLegacyError(msg.error));
        // Sinaliza state distinto pra UI poder distinguir ação possível.
        if (msg.type === 'error-full') {
          this.cbState({ type: 'room-full' });
        } else if (msg.type === 'error-conflict') {
          this.cbState({ type: 'user-conflict' });
        }
        break;
      case 'kicked':
        // Server expulsou este peer (owner kick). Sinaliza pra UI ANTES do
        // WS fechar — assim o user vê motivo antes do reconnect-gave-up.
        this._userInitiatedHangup = true; // impede reopenSignaling
        this.cbState({ type: 'kicked', reason: msg.error || 'Removido da chamada' });
        break;
    }
  };

  // -- Peers -----------------------------------------------------------

  // re-key (adoção) de um PeerConn VIVO do id ANTIGO para o id NOVO
  // que o server atribuiu pós-reconexão. Migra TUDO que é chaveado por id, no
  // MESMO tick síncrono, pra que nenhum consumidor veja estado intermediário:
  //   (0) this.peers + peer.remoteId  [crítico — handlers leem remoteId lazy]
  //   (a) os 6 atributos DOM do tile  [tile/avatar/name/sub/video/caption]
  //   (b) o monitor de áudio          [RAF relê _peerAudioMonitors[remoteId] lazy]
  //   (c) _captionClearTimers         [código morto hoje; migrado por robustez]
  //   (d) _activeSpeaker + _subtitlesRequestedById  [load-bearing — call-level]
  //   (e) _fileTxOffsets[*].peerID    [cosmético — .peerID nunca é lido]
  Call.prototype._rekeyPeer = function (oldId, newId) {
    if (oldId === newId) return;
    const peer = this.peers[oldId];
    if (!peer) return;
    // (0) crítico: o mapa e o campo remoteId. Todos os event handlers do PC
    //     (ontrack/onicecandidate/onconnectionstatechange/offer) são closures
    //     que leem this.remoteId no fire-time → re-rotear aqui é atômico.
    delete this.peers[oldId];
    peer.remoteId = newId;
    this.peers[newId] = peer;
    // (a) os 6 atributos DOM chaveados por id no tile do peer.
    if (this.videosEl) {
      const attrs = ['data-vc-peer-tile', 'data-vc-peer-avatar', 'data-vc-peer-name',
                     'data-vc-peer-sub', 'data-vc-peer', 'data-vc-peer-caption'];
      for (let i = 0; i < attrs.length; i++) {
        const el = this.videosEl.querySelector('[' + attrs[i] + '="' + oldId + '"]');
        if (el) el.setAttribute(attrs[i], newId);
      }
    }
    // (b) move o monitor de áudio. O RAF relê this.call._peerAudioMonitors
    //     [this.remoteId] (agora = newId) LAZY → sobrevive sem recriar.
    if (this._peerAudioMonitors && this._peerAudioMonitors[oldId]) {
      const mon = this._peerAudioMonitors[oldId];
      mon.peerId = newId;
      this._peerAudioMonitors[newId] = mon;
      delete this._peerAudioMonitors[oldId];
    }
    // (c) _captionClearTimers (plural, call-level): hoje código morto (lido/
    //     deletado, nunca escrito) — migrado por robustez caso volte a ser usado.
    if (this._captionClearTimers && this._captionClearTimers[oldId]) {
      this._captionClearTimers[newId] = this._captionClearTimers[oldId];
      delete this._captionClearTimers[oldId];
    }
    // (d) estado call-level que CONGELA o id [load-bearing]:
    //     _activeSpeaker dispara o CSS ring via [data-vc-peer="<id>"];
    //     _subtitlesRequestedById é dereferenciado por _sendSubsStatus como
    //     this.peers[id] → sem migrar, o ack/status de legenda some pós-adoção.
    if (this._activeSpeaker === oldId) this._activeSpeaker = newId;
    if (this._subtitlesRequestedById === oldId) this._subtitlesRequestedById = newId;
    // (e) _fileTxOffsets[*].peerID: cosmético (.peerID nunca é lido — roteamento
    //     de arquivo vai pela this.dc do PC adotado), migrado por consistência.
    if (this._fileTxOffsets) {
      for (const fid in this._fileTxOffsets) {
        const o = this._fileTxOffsets[fid];
        if (o && o.peerID === oldId) o.peerID = newId;
      }
    }
  };

  Call.prototype.ensurePeer = function (remoteId, user, initiator, clientId) {
    // Early-return idempotente: também absorve uma 2ª chamada para um peer já
    // adotado (snapshot + peer-joined chegando nas duas ordens).
    if (this.peers[remoteId]) return this.peers[remoteId];
    // Mesmo clientId estável sob OUTRO id ⇒ ou é um sobrevivente de deploy
    // (adotar) ou o fantasma da conexão anterior do mesmo cliente (fechar).
    if (clientId) {
      for (const id of Object.keys(this.peers)) {
        if (id === remoteId) continue;
        const old = this.peers[id];
        if (!old || old.remoteClientId !== clientId) continue;
        const cs = old.pc && old.pc.connectionState;
        // ADOÇÃO pós-deploy. clientId casa + PC ainda `connected` ⇒ a
        // mídia nunca caiu (só o WS de signaling). Adotamos re-keyando old->new
        // em vez de fechar+renegociar: onicecandidate/offer leem this.remoteId
        // LAZY, então re-keyar re-roteia o signaling no MESMO tick. Sem
        // `new PeerConn` ⇒ sem addTrack ⇒ sem onnegotiationneeded — a mídia
        // segue intacta e ZERO renegociação é disparada.
        if (cs === 'connected') {
          this._rekeyPeer(id, remoteId);
          const adopted = this.peers[remoteId];
          // Recomputa politeness com os ids NOVOS dos dois lados (perfect
          // negotiation). Determinístico e simétrico: o outro lado computa o
          // inverso. Cobre o glare da janela sub-segundo de adoção assimétrica.
          adopted.polite = this.peerId < remoteId;
          console.log('[vpsm:vc] adopted ' + id.slice(-6) + '->' + remoteId.slice(-6) + ' (connected — mídia preservada, sem renegociação)');
          this._notifyPeerCount();
          return adopted;
        }
        // FIX (idempotência visual): clientId casa mas o PC NÃO está
        // `connected` ⇒ é o fantasma da conexão morta do mesmo cliente que o
        // server ainda não evictou no nosso lado. Fecha+remove antes do novo.
        try { old.close(); } catch (_) {}
        delete this.peers[id];
        this._teardownPeerMonitor(id);
      }
    }
    const peer = new PeerConn({
      call: this,
      remoteId: remoteId,
      remoteUser: user,
      remoteClientId: clientId || '',
      initiator: initiator,
      // Perfect negotiation: the LEXICOGRAPHICALLY LARGER id is impolite.
      // Both peers compute this from the same source of truth (their own
      // assigned ids), so the answer is deterministic and identical on both
      // sides.
      polite: this.peerId < remoteId,
    });
    this.peers[remoteId] = peer;
    this._notifyPeerCount();
    return peer;
  };
  // Recria a conexao com um peer do zero (PeerConn novo). `avisar`: somos o
  // lado que travou — manda `reset` pro outro recriar o dele e voltamos como
  // iniciador (abrimos os DataChannels e oferecemos). O `reset` sai pelo mesmo
  // WS e antes da oferta nova, entao chega primeiro. Ate 3 por peer.
  Call.prototype._rebuildPeer = function (remoteId, motivo, avisar) {
    const old = this.peers[remoteId];
    if (!old || this.stopped) return;
    this._peerRebuilds = this._peerRebuilds || {};
    const n = (this._peerRebuilds[remoteId] || 0) + 1;
    if (n > 3) {
      console.warn('[vpsm:vc] conexao com ' + remoteId.slice(-6) + ' nao sobe apos 3 recriacoes — desisto');
      this.cbError('Não consegui ligar o áudio/vídeo com ' + (old.remoteUser || 'o participante') + '. Saia e entre de novo.');
      return;
    }
    this._peerRebuilds[remoteId] = n;
    console.warn('[vpsm:vc] recriando conexao com ' + remoteId.slice(-6) + ' (' + motivo + ', ' + n + '/3)');
    const user = old.remoteUser, clientId = old.remoteClientId;
    if (avisar) this.send({ type: 'reset', to: remoteId, payload: jsonRaw({ reason: motivo }) });
    try { old.close(); } catch (_) {}
    delete this.peers[remoteId];
    this._teardownPeerMonitor(remoteId);
    this.ensurePeer(remoteId, user, /*initiator=*/!!avisar, clientId);
  };

  // Alpine não enxerga mutações em `this.peers` (objeto fora da reatividade).
  // Notifica via callback pra UI atualizar contadores. (Auditoria A3)
  Call.prototype._notifyPeerCount = function () {
    try {
      const list = [];
      // FIX: dedup por clientId (não por user — guests podem repetir
      // nome). Redundante após a eviction (server + ensurePeer), mas garante
      // que um fantasma residual nunca infle a contagem de participantes.
      const seen = new Set();
      for (const id in this.peers) {
        const p = this.peers[id];
        const cid = p.remoteClientId || '';
        if (cid && seen.has(cid)) continue;
        if (cid) seen.add(cid);
        list.push({ id: p.remoteId, user: p.remoteUser || '' });
      }
      this.cbState({ type: 'peer-count', count: list.length, peers: list });
    } catch (_) {}
  };

  // FIX (rede de segurança): remove do DOM qualquer tile de peer
  // (data-vc-peer-tile) que não tenha um PeerConn vivo por trás. Pega resíduos
  // órfãos de reconexões — independente de como surgiram (ex.: peer-left
  // perdido, bug histórico do close que só removia o <video>). Idempotente.
  Call.prototype._pruneOrphanTiles = function () {
    if (!this.videosEl) return;
    try {
      const tiles = this.videosEl.querySelectorAll('[data-vc-peer-tile]');
      for (let i = 0; i < tiles.length; i++) {
        const id = tiles[i].getAttribute('data-vc-peer-tile');
        if (id && id !== this.peerId && !this.peers[id]) {
          try { tiles[i].remove(); } catch (_) {}
        }
      }
    } catch (_) {}
  };

  // PiP sobrevive à reconexão. _preservePipTile desvincula o tile do
  // esquema de peer-id (que muda no rejoin) sem remover o <video> do DOM — a
  // janela de PiP nativa segue aberta, congelada no último frame. Indexamos por
  // clientId (estável entre reconexões, âncora da adoção). _adoptDetachedPipTile
  // readota esse <video> quando o mesmo cliente volta. _dropDetachedPip é a rede
  // de segurança caso o peer não retorne (não deixa janela fantasma pra sempre).
  Call.prototype._preservePipTile = function (v, tile, cid) {
    try {
      v.removeAttribute('data-vc-peer');
      v.setAttribute('data-vc-pip-detached', cid);
      if (tile) {
        // tira do alcance do _pruneOrphanTiles e do reflow de layout, e esconde
        // o tile congelado da grade (a janela do SO continua mostrando o vídeo
        // mesmo com o elemento display:none na página).
        tile.removeAttribute('data-vc-peer-tile');
        tile.setAttribute('data-vc-pip-detached-tile', cid);
        tile.style.display = 'none';
      }
      this._pipDetached = this._pipDetached || {};
      this._pipDetached[cid] = { v: v, tile: tile };
      console.log('[vpsm:vc] PiP: tile preservado (cliente ' + cid.slice(-6) + ') — janela do SO mantida durante reconexão');
      if (this._pipReattachTimer) clearTimeout(this._pipReattachTimer);
      this._pipReattachTimer = setTimeout(() => this._dropDetachedPip(cid), 30000);
    } catch (_) {}
  };

  Call.prototype._adoptDetachedPipTile = function (cid, newId) {
    if (!cid || !this._pipDetached || !this._pipDetached[cid]) return null;
    const d = this._pipDetached[cid];
    const v = d.v, tile = d.tile;
    if (this._pipReattachTimer) { clearTimeout(this._pipReattachTimer); this._pipReattachTimer = null; }
    try {
      v.setAttribute('data-vc-peer', newId);
      v.removeAttribute('data-vc-pip-detached');
      if (tile) {
        tile.setAttribute('data-vc-peer-tile', newId);
        tile.removeAttribute('data-vc-pip-detached-tile');
        tile.style.display = '';
        // re-chaveia os filhos do tile (avatar/nome/sub/caption) pro id novo,
        // senão a lógica de avatar/caption do onTrack não os encontra.
        ['data-vc-peer-avatar', 'data-vc-peer-name', 'data-vc-peer-sub', 'data-vc-peer-caption'].forEach(function (a) {
          const el = tile.querySelector('[' + a + ']');
          if (el) el.setAttribute(a, newId);
        });
      }
    } catch (_) {}
    delete this._pipDetached[cid];
    console.log('[vpsm:vc] PiP: tile readotado (cliente ' + cid.slice(-6) + ' -> ' + newId.slice(-6) + ') — mídia restaurada na janela do SO');
    return v;
  };

  Call.prototype._dropDetachedPip = function (cid) {
    const d = this._pipDetached && this._pipDetached[cid];
    if (!d) return;
    try {
      if (document.pictureInPictureElement && document.pictureInPictureElement === d.v && document.exitPictureInPicture) {
        document.exitPictureInPicture().catch(() => {});
      }
    } catch (_) {}
    try { if (d.tile) d.tile.remove(); else if (d.v) d.v.remove(); } catch (_) {}
    delete this._pipDetached[cid];
  };

  Call.prototype.handleNetworkChange = function () {
    // network came back / changed. Ask every PC to restart ICE.
    for (const id in this.peers) {
      try { this.peers[id].pc.restartIce(); } catch (_) {}
    }
  };

  // -- Stats -----------------------------------------------------------

  Call.prototype.collectStats = async function () {
    const agg = {
      ts: Date.now(),
      bytesSentPerSec: 0, bytesRecvPerSec: 0,
      packetsLost: 0, rtt: 0,
      codec: '', resolution: '', framerate: 0,
      connectionType: 'direct', // direct | relay | unknown
    };
    const samples = [];
    for (const id in this.peers) {
      const s = await this.peers[id].collectStats();
      if (s) samples.push(s);
    }
    for (const s of samples) {
      agg.bytesSentPerSec += s.bytesSentPerSec || 0;
      agg.bytesRecvPerSec += s.bytesRecvPerSec || 0;
      agg.packetsLost += s.packetsLost || 0;
      if (s.rtt > agg.rtt) agg.rtt = s.rtt;
      if (s.codec) agg.codec = s.codec;
      if (s.resolution) agg.resolution = s.resolution;
      if (s.framerate > agg.framerate) agg.framerate = s.framerate;
      if (s.connectionType === 'relay') agg.connectionType = 'relay';
    }
    // Accumulate session totals for /api/videocall/sessions POST on hangup.
    this.cumBytesSent += agg.bytesSentPerSec;
    this.cumBytesRecv += agg.bytesRecvPerSec;
    if (agg.codec) this.lastCodec = agg.codec;
    if (agg.connectionType) this.lastConnType = agg.connectionType;
    // Adaptive degrade hooks (audio-first + AV1 fallback).
    this.applyAdaptiveDegrade(agg);
    // Update active-speaker indicator. Threshold of ~12 (out of 0-255 avg)
    // distinguishes voice from line noise.
    let topPeer = null, topLevel = 12;
    for (const id in this._peerAudioMonitors) {
      const lvl = this._peerAudioMonitors[id].level;
      if (lvl > topLevel) { topLevel = lvl; topPeer = id; }
    }
    if (topPeer !== this._activeSpeaker) {
      // Toggle .speaking class on video elements so the CSS ring fires.
      if (this.videosEl) {
        if (this._activeSpeaker) {
          const old = this.videosEl.querySelector('[data-vc-peer="' + this._activeSpeaker + '"]');
          if (old) old.classList.remove('speaking');
        }
        if (topPeer) {
          const cur = this.videosEl.querySelector('[data-vc-peer="' + topPeer + '"]');
          if (cur) cur.classList.add('speaking');
        }
      }
      this._activeSpeaker = topPeer;
      this.cbState({ type: 'active-speaker', peerId: topPeer });
    }
    this.cbStats(agg);
  };

  // -- Local controls --------------------------------------------------

  Call.prototype.attachLocalPreview = function () {
    if (!this.videosEl) return;
    let v = this.videosEl.querySelector('[data-vc-local="1"]');
    if (!v) {
      v = document.createElement('video');
      v.setAttribute('data-vc-local', '1');
      v.autoplay = true; v.muted = true; v.playsInline = true;
      // NÃO setar width inline — clobava as regras data-local-size
      // (sem !important) e travava Peq/Grd. Geometria fica 100% no CSS
      // (.vc-videos video[data-vc-local="1"] + variantes data-local-size).
      v.style.cssText = 'background:#000;';
      this.videosEl.appendChild(v);
    }
    v.srcObject = this.localStream;
  };

  Call.prototype.setMuted = function (muted) {
    if (!this.localStream) return false;
    this._userMutedExplicit = !!muted;
    this.muted = !!muted; // M26: expõe pro loop-guard skip mute-period
    for (const t of this.localStream.getAudioTracks()) t.enabled = !muted;
    // O cru tambem: e dele que a transcricao le. Antes so o enviado era
    // desligado e o Whisper seguia transcrevendo — e mandando como legenda —
    // o que a pessoa falava MUTADA. (O alerta "falando mutado" le um clone
    // com enabled proprio, entao continua ouvindo.)
    if (this._micGainRawTrack) this._micGainRawTrack.enabled = !muted;
    this.send({ type: 'state', payload: jsonRaw({ mic: muted ? 'off' : 'on' }) });
    // M26: ao desmutar, dá janela de graça pro STT — reset counter + lastSuccess
    // pra evitar loop-guard tropeçar com contagem residual do período mute.
    if (!muted && this._subtitlesActive) {
      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      // Se o STT tinha pausado por mute, força restart imediato.
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
        if (window.VPSM_DEBUG) console.log('[vpsm:vc] desmute → kick STT restart');
        // schedule curto pra esperar track.enabled propagar antes de re-iniciar
        setTimeout(() => {
          if (this._subtitlesActive && !this._subtitlesHandle && this._restartSubtitlesLocalOnly) {
            this._restartSubtitlesLocalOnly(this._subtitlesBackend || 'web-speech', this._subtitlesLang);
          }
        }, 300);
      }
    }
    return !!muted;
  };

  Call.prototype.setVideoOff = function (off) {
    if (!this.localStream) return false;
    for (const t of this.localStream.getVideoTracks()) t.enabled = !off;
    // M22 QW5: reset autoVideoOff sync — quando user clica "video on"
    // manualmente, autoVideoOff state machine ficava desalinhada e
    // não emitia auto-video-on em recovery futuro.
    if (!off && this.autoVideoOff) {
      this.autoVideoOff = false;
    }
    this.send({ type: 'state', payload: jsonRaw({ cam: off ? 'off' : 'on' }) });
    return !!off;
  };

  // -- Live device hot-swap (works mid-call without renegotiation) -----
  // RTCRtpSender.replaceTrack lets us swap the underlying camera/mic
  // without touching SDP. setSinkId on remote <video> changes which
  // speaker/headset the remote audio plays through.

  Call.prototype.setCameraDevice = async function (deviceId) {
    if (this.frostActive) await this.setPrivacyFrost(false);
    const constraints = { video: { width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 30 } } };
    if (deviceId === 'environment' || deviceId === 'user') {
      constraints.video.facingMode = { ideal: deviceId };
    } else if (deviceId && deviceId !== 'default') {
      constraints.video.deviceId = { exact: deviceId };
    }
    let s;
    try { s = await navigator.mediaDevices.getUserMedia(constraints); }
    catch (e) { this.cbError('câmera: ' + e.message); return false; }
    const newTrack = s.getVideoTracks()[0];
    if (!newTrack) return false;
    // Stop the old camera track (release the device).
    if (this.cameraTrack && this.cameraTrack !== newTrack) {
      try { this.cameraTrack.stop(); } catch (_) {}
    }
    this.cameraTrack = newTrack;
    this.deviceIds.camera = deviceId;
    // Se estiver em screen-share, NÃO substitui o sender — a nova câmera
    // só vira efetiva quando user parar de compartilhar tela. Antes, swap
    // arrancava a tela compartilhada silenciosamente. (Auditoria A6)
    if (!this.screenSharing) {
      await this.swapVideoSenderTrack(newTrack);
    } else {
      this.cbState({ type: 'camera-swapped-during-screen-share' });
    }
    return true;
  };

  Call.prototype.setMicDevice = async function (deviceId) {
    let s;
    try { s = await navigator.mediaDevices.getUserMedia({ audio: micConstraints(deviceId, this._micProc) }); }
    catch (e) { this.cbError('microfone: ' + e.message); return false; }
    const newTrack = s.getAudioTracks()[0];
    if (!newTrack) return false;
    this.deviceIds.mic = deviceId;
    await this._replaceMicRaw(newTrack);
    return true;
  };

  // Liga/desliga supressao de ruido, cancelamento de eco e ganho automatico.
  // Reabre o microfone com as novas restricoes: applyConstraints nessas tres
  // chaves e ignorado em silencio por boa parte dos navegadores (o track
  // devolve getSettings() inalterado). A troca passa pelo mesmo caminho da
  // troca de dispositivo, entao os peers nao percebem.
  Call.prototype.setMicProcessing = async function (proc) {
    const next = normMicProc(Object.assign({}, this._micProc, proc || {}));
    const prev = this._micProc;
    this._micProc = next;
    const current = this._micGainRawTrack || (this.localStream && this.localStream.getAudioTracks()[0]);
    if (!current) return next;
    let s;
    try { s = await navigator.mediaDevices.getUserMedia({ audio: micConstraints(this.deviceIds.mic, next) }); }
    catch (e) {
      this._micProc = prev;
      this.cbError('microfone: ' + e.message);
      return prev;
    }
    const newTrack = s.getAudioTracks()[0];
    if (!newTrack) { this._micProc = prev; return prev; }
    await this._replaceMicRaw(newTrack);
    this.cbState({ type: 'mic-processing', value: next });
    return next;
  };

  // Monta o pipeline de volume em cima do track cru que o getUserMedia deu.
  // O track que vai no localStream (e dali pros peers e pra gravacao) passa
  // a ser a saida do pipeline; o cru fica guardado pra transcricao e pras
  // trocas de fonte.
  Call.prototype._buildMicPipeline = function (rawTrack) {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!rawTrack || !AC) return false;
    const ctx = new AC();
    const gain = ctx.createGain();
    gain.gain.value = this._micGain;
    const limiter = makeMicLimiter(ctx);
    const analyser = ctx.createAnalyser();
    analyser.fftSize = 1024;
    const dest = ctx.createMediaStreamDestination();
    gain.connect(limiter.input);
    limiter.output.connect(dest);
    limiter.output.connect(analyser);
    const sentTrack = dest.stream.getAudioTracks()[0];
    if (!sentTrack) {
      try { ctx.close().catch(() => {}); } catch (_) {}
      return false;
    }
    // Contexto criado fora de um gesto pode nascer suspenso — e suspenso a
    // saida e silencio puro pros peers.
    if (ctx.state === 'suspended') { try { ctx.resume(); } catch (_) {} }
    this._micGainCtx = ctx;
    this._micGainNode = gain;
    this._micLimiter = limiter;
    this._micAnalyser = analyser;
    this._micLevelBuf = new Float32Array(analyser.fftSize);
    this._attachMicSource(rawTrack);
    sentTrack.enabled = rawTrack.enabled;
    this.localStream.removeTrack(rawTrack);
    this.localStream.addTrack(sentTrack);
    console.log('[vpsm:vc] volume do mic pronto (' + Math.round(this._micGain * 100) + '%)');
    return true;
  };

  Call.prototype._attachMicSource = function (rawTrack) {
    if (this._micGainSrc) { try { this._micGainSrc.disconnect(); } catch (_) {} }
    this._micGainSrc = this._micGainCtx.createMediaStreamSource(new MediaStream([rawTrack]));
    this._micGainSrc.connect(this._micGainNode);
    this._micGainRawTrack = rawTrack;
  };

  // Troca o track cru (outro microfone, ou o mesmo com outro processamento).
  // Com pipeline: so religa a fonte — o track enviado nao muda, nenhum
  // replaceTrack. Antes a troca de mic mandava o track novo CRU direto pros
  // senders: o volume escolhido sumia, e a transcricao e o alerta "falando
  // mutado" ficavam presos no track velho, ja parado.
  Call.prototype._replaceMicRaw = async function (newTrack) {
    newTrack.enabled = !this._userMutedExplicit;
    let old;
    if (this._micGainCtx && this._micGainNode) {
      old = this._micGainRawTrack;
      this._attachMicSource(newTrack);
    } else {
      // Sem pipeline (AudioContext recusado): caminho antigo, direto nos senders.
      old = this.localStream && this.localStream.getAudioTracks()[0];
      for (const id in this.peers) {
        const pc = this.peers[id].pc;
        for (const sender of pc.getSenders()) {
          if (sender.track && sender.track.kind === 'audio') {
            try { await sender.replaceTrack(newTrack); } catch (_) {}
          }
        }
      }
      if (this.localStream) {
        if (old) { try { this.localStream.removeTrack(old); } catch (_) {} }
        this.localStream.addTrack(newTrack);
      }
    }
    if (old && old !== newTrack) { try { old.stop(); } catch (_) {} }
    this._startLocalSpeakingMonitor();
    // A transcricao le o cru: reinicia em cima do novo, sem broadcast pros peers.
    if (this._subtitlesActive && this._restartSubtitlesLocalOnly) {
      // Um restart agendado (pausa por mute/backoff) religaria um segundo handle.
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      this._restartSubtitlesLocalOnly(this._subtitlesBackend || 'web-speech', this._subtitlesLang);
    }
  };

  // Nivel do que SAI pros peers (depois do volume e do limitador). null =
  // sem pipeline. Mutado le zero, que e o que o outro lado ouve.
  Call.prototype.getMicLevel = function () {
    if (!this._micAnalyser) return null;
    return readMicLevel(this._micAnalyser, this._micLevelBuf, this._micLimiter);
  };

  // Alerta "falando mutado". Le um CLONE do cru com enabled proprio, entao
  // mutar (que desliga o cru e o enviado) nao cala o analisador. Religado a
  // cada troca de fonte.
  Call.prototype._startLocalSpeakingMonitor = function () {
    this._stopLocalSpeakingMonitor();
    try {
      const audioTrack = this._micGainRawTrack || (this.localStream && this.localStream.getAudioTracks()[0]);
      if (!audioTrack || audioTrack.readyState !== 'live') return;
      const clone = audioTrack.clone();
      clone.enabled = true;
      const cloneStream = new MediaStream([clone]);
      const ctx = new (window.AudioContext || window.webkitAudioContext)();
      const src = ctx.createMediaStreamSource(cloneStream);
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 256;
      src.connect(analyser);
      const data = new Uint8Array(analyser.frequencyBinCount);
      let aboveSince = 0;
      // M22 QW1: RAF handle salvo pra cancel em stop.
      const mon = { ctx, stream: cloneStream, raf: 0, warned: false };
      this._localSpeakingMon = mon;
      const tick = () => {
        if (this.stopped || this._localSpeakingMon !== mon) return;
        // M22: resume AudioContext se browser suspendeu em background tab
        if (ctx.state === 'suspended') { try { ctx.resume(); } catch (_) {} }
        analyser.getByteFrequencyData(data);
        let sum = 0;
        for (let i = 0; i < data.length; i++) sum += data[i];
        const lvl = sum / data.length;
        if (this._userMutedExplicit && lvl > 18) {
          if (!aboveSince) aboveSince = Date.now();
          else if (!mon.warned && Date.now() - aboveSince > 800) {
            mon.warned = true;
            this.cbState({ type: 'speaking-while-muted', on: true });
          }
        } else {
          aboveSince = 0;
          if (mon.warned) { mon.warned = false; this.cbState({ type: 'speaking-while-muted', on: false }); }
        }
        mon.raf = requestAnimationFrame(tick);
      };
      mon.raf = requestAnimationFrame(tick);
    } catch (_) { /* permission edge case; ignore */ }
  };

  Call.prototype._stopLocalSpeakingMonitor = function () {
    const mon = this._localSpeakingMon;
    if (!mon) return;
    this._localSpeakingMon = null;
    try {
      if (mon.raf) cancelAnimationFrame(mon.raf);
      if (mon.stream) mon.stream.getTracks().forEach(t => t.stop());
      if (mon.ctx && mon.ctx.state !== "closed") mon.ctx.close().catch(() => {});
    } catch (_) {}
    if (mon.warned) this.cbState({ type: 'speaking-while-muted', on: false });
  };

  Call.prototype.setSpeakerDevice = async function (deviceId) {
    this.deviceIds.speaker = deviceId;
    if (!this.videosEl) return false;
    const targets = this.videosEl.querySelectorAll('video[data-vc-peer]');
    let ok = false;
    for (const el of targets) {
      if (typeof el.setSinkId === 'function') {
        try {
          await el.setSinkId(deviceId === 'default' ? '' : deviceId);
          ok = true;
        } catch (e) { this.cbError('saída de áudio: ' + e.message); }
      }
    }
    return ok;
  };

  Call.prototype.setBudgetKbps = function (kbps) {
    this.budgetKbps = Math.max(BUDGET_FLOOR_KBPS, Math.min(BUDGET_CEILING_KBPS, kbps | 0));
    this.videoKbps  = this.budgetKbps;
    this.qualityName = 'custom';
    for (const id in this.peers) this.peers[id].applyBitrates(this.videoKbps, this.audioKbps);
  };

  /**
   * Apply a quality profile live. Honored mid-call:
   *  - track.applyConstraints  → camera renegotiates resolution / fps
   *  - sender.setParameters    → video maxBitrate + audio maxBitrate
   *
   * If profileOrName is a preset name, the preset object is looked up;
   * otherwise an explicit { width, height, fps, videoKbps, audioKbps }
   * is taken as-is. Returns the effective profile actually applied.
   */
  Call.prototype.applyQualityProfile = async function (profileOrName) {
    let p, name;
    if (typeof profileOrName === 'string') {
      name = profileOrName;
      p = QUALITY_MODES[name];
      if (!p) return null;
      p = Object.assign({}, p); // shallow copy so we can mutate locally
    } else if (profileOrName && typeof profileOrName === 'object') {
      name = 'custom';
      p = {
        videoOff:  profileOrName.videoOff != null ? !!profileOrName.videoOff : this.audioOnly,
        width:     profileOrName.width     || this.videoWidth,
        height:    profileOrName.height    || this.videoHeight,
        fps:       profileOrName.fps       || this.videoFps,
        videoKbps: profileOrName.videoKbps != null ? profileOrName.videoKbps : this.videoKbps,
        audioKbps: profileOrName.audioKbps || this.audioKbps,
        opusTweak: profileOrName.opusTweak != null ? !!profileOrName.opusTweak : this.opusTweak,
      };
    } else {
      return null;
    }
    // Clamp the bitrates within sane absolute bounds.
    p.videoKbps = p.videoOff ? 0 : Math.max(BUDGET_FLOOR_KBPS, Math.min(BUDGET_CEILING_KBPS, p.videoKbps));
    p.audioKbps = Math.max(6, Math.min(128, p.audioKbps));
    this.qualityName = name;
    this.videoWidth  = p.width  || this.videoWidth;
    this.videoHeight = p.height || this.videoHeight;
    this.videoFps    = p.fps    || this.videoFps;
    this.videoKbps   = p.videoKbps;
    this.audioKbps   = p.audioKbps;
    this.budgetKbps  = p.videoKbps;
    // (1) Toggle video on/off (Telefone vira audio-only mid-call).
    const wantsAudioOnly = !!p.videoOff;
    if (wantsAudioOnly !== this.audioOnly) {
      this.audioOnly = wantsAudioOnly;
      if (wantsAudioOnly) {
        // Hot-disable: derruba o video sender + para a câmera. O peer
        // continua recebendo só áudio. Sem renegociação obrigatória.
        for (const id in this.peers) {
          const pc = this.peers[id].pc;
          for (const sender of pc.getSenders()) {
            if (sender.track && sender.track.kind === 'video') {
              try { await sender.replaceTrack(null); } catch (_) {}
            }
          }
        }
        if (this.cameraTrack) { try { this.cameraTrack.stop(); } catch (_) {} this.cameraTrack = null; }
        if (this.localStream) {
          for (const t of this.localStream.getVideoTracks()) {
            try { this.localStream.removeTrack(t); } catch (_) {}
          }
        }
      } else {
        // Hot-enable: pega câmera de volta e injeta no transceiver
        // existente (se houver) ou cria novo.
        try {
          const constraints = {
            video: {
              width: { ideal: this.videoWidth }, height: { ideal: this.videoHeight }, frameRate: { ideal: this.videoFps },
            },
          };
          if (this.deviceIds.camera && this.deviceIds.camera !== 'default') {
            constraints.video.deviceId = { exact: this.deviceIds.camera };
          }
          const s = await navigator.mediaDevices.getUserMedia(constraints);
          const newTrack = s.getVideoTracks()[0];
          this.cameraTrack = newTrack;
          if (this.localStream) this.localStream.addTrack(newTrack);
          for (const id in this.peers) {
            const pc = this.peers[id].pc;
            const tx = pc.getTransceivers().find(t => t.sender && (!t.sender.track || (t.sender.track && t.sender.track.kind === 'video')));
            if (tx) { try { await tx.sender.replaceTrack(newTrack); } catch (_) {} }
            else    { try { pc.addTrack(newTrack, this.localStream); } catch (_) {} }
          }
        } catch (e) { this.cbError('reabilitar vídeo: ' + e.message); }
      }
    }
    // (2) Apply camera constraints (no renegotiation needed).
    if (!wantsAudioOnly && this.cameraTrack && this.cameraTrack.applyConstraints) {
      try {
        await this.cameraTrack.applyConstraints({
          width:     { ideal: p.width  },
          height:    { ideal: p.height },
          frameRate: { ideal: p.fps    },
        });
      } catch (e) { /* device might not support; non-fatal */ }
    }
    // (3) Update Opus SDP tweak state. Mudar ele exige renegociação pra
    // o novo fmtp ir pro peer — disparo via dispatchEvent abaixo se mudou.
    const newOpusTweak = (p.opusTweak != null ? !!p.opusTweak : this.opusTweak);
    const opusChanged = newOpusTweak !== this.opusTweak;
    this.opusTweak = newOpusTweak;
    // (4) Apply bitrates on every active sender.
    for (const id in this.peers) this.peers[id].applyBitrates(p.videoKbps, p.audioKbps);
    // (5) Trigger renegotiation if opus tweak flipped.
    // FIX Onda3 A4: bypassava throttle ao chamar dispatchEvent direto.
    // M21: REMOVIDO dispatchEvent('negotiationneeded') manual.
    // O dispatch artificial dispara handler SEM que o needs-negotiation flag
    // nativo esteja dirty → createOffer retorna SDP idêntica → answer chega →
    // stable → outro fire legítimo (porque flag continua dirty por outra
    // razão real) = loop infinito (centenas de FIRE→answer→stable→FIRE
    // observados em produção).
    //
    // Opus tweak entra via tweakOpusSdp no createOffer da próxima renegotiation
    // ESPONTÂNEA — qualquer addTrack/setCodecPref/replaceTrack subsequente
    // já aplicará. Se nada disparar, a config nova só vale na próxima call —
    // aceitável vs loop bug.
    void opusChanged;
    this.cbState({ type: 'quality', profile: { name: name, ...p } });
    return { name, ...p };
  };

  Call.prototype.sendChat = function (text) {
    text = String(text || '').slice(0, 4000);
    if (!text) return;
    let dcSent = false;
    for (const id in this.peers) {
      if (this.peers[id].sendChat(text)) dcSent = true;
    }
    // Fallback: relay through signaling for any peer whose DC isn't open.
    if (!dcSent) {
      // Broadcast via server — server only forwards to same room (ACL OK).
      // We use Forward by sending one msg per peer.
      for (const id in this.peers) {
        this.send({ type: 'chat', to: id, payload: jsonRaw({ text: text }) });
      }
    }
  };

  // -- Screen share / Recording / Audio-first -------------------------

  Call.prototype.setScreenShare = async function (on) {
    if (on && !this.screenSharing) {
      try {
        this.screenStream = await navigator.mediaDevices.getDisplayMedia({
          video: { frameRate: { ideal: 15 } }, // 15fps is plenty for slides
          audio: false,
        });
      } catch (e) {
        this.cbError('screen share: ' + e.message);
        return false;
      }
      const screenTrack = this.screenStream.getVideoTracks()[0];
      // Auto-stop when user clicks "Stop sharing" in the browser UI.
      screenTrack.addEventListener('ended', () => { this.setScreenShare(false); });
      await this.swapVideoSenderTrack(screenTrack);
      this.screenSharing = true;
      this.send({ type: 'state', payload: jsonRaw({ screen: 'on' }) });
      this.cbState({ type: 'screen-share', on: true });
      return true;
    }
    if (!on && this.screenSharing) {
      // M22 QW2: ORDEM corrigida — swap camera ANTES de stop screen track.
      // Antes parava primeiro → sender continuava enviando último frame
      // (preto) durante 100-2000ms até replaceTrack completar. Agora:
      // 1) Validar cameraTrack ainda vivo (era #4 do audit — track pode ter
      //    morrido em background durante screen share).
      // 2) replaceTrack ANTES de stop.
      // 3) stop screen track DEPOIS — peer já recebe frames da câmera.
      let camTrack = this.cameraTrack;
      if (!camTrack || camTrack.readyState !== 'live') {
        // Re-pega câmera (track morreu mid-screen-share, e.g. tab background)
        try {
          const stream = await navigator.mediaDevices.getUserMedia({ video: true });
          camTrack = stream.getVideoTracks()[0];
          this.cameraTrack = camTrack;
          if (this.localStream) this.localStream.addTrack(camTrack);
        } catch (e) {
          console.warn('[vpsm:vc] camera re-get falhou pós-screen-share', e);
        }
      }
      if (camTrack && camTrack.readyState === 'live') {
        await this.swapVideoSenderTrack(camTrack);
      }
      // Stop SÓ depois do swap
      if (this.screenStream) {
        try { this.screenStream.getTracks().forEach(t => t.stop()); } catch (_) {}
        this.screenStream = null;
      }
      this.screenSharing = false;
      this.send({ type: 'state', payload: jsonRaw({ screen: 'off' }) });
      this.cbState({ type: 'screen-share', on: false });
      return false;
    }
    return this.screenSharing;
  };

  Call.prototype.swapVideoSenderTrack = async function (newTrack) {
    for (const id in this.peers) {
      const pc = this.peers[id].pc;
      // FIX Onda3 A3: aguardar pc.connectionState==='connected' antes de
      // replaceTrack. Em redes lentas, replace resolvia mas track ainda
      // negociando — remote via black frame por 2-3s. Wait até 2s pra peer
      // estabilizar; se não conectar, skipa (peer será atualizado via
      // onnegotiationneeded normal).
      if (pc.connectionState !== 'connected') {
        let retries = 0;
        while (pc.connectionState !== 'connected' && retries < 20) {
          await new Promise(r => setTimeout(r, 100));
          retries++;
        }
        if (pc.connectionState !== 'connected') continue;
      }
      for (const sender of pc.getSenders()) {
        if (sender.track && sender.track.kind === 'video') {
          // Promise.resolve wrapper pra Safari < 15.2 — antes retornava
          // sync e o await podia rejeitar silenciosamente. (Auditoria P4)
          try { await Promise.resolve(sender.replaceTrack(newTrack)); } catch (_) {}
        }
      }
    }
    // Update local preview to show what we're broadcasting.
    if (this.videosEl) {
      const v = this.videosEl.querySelector('[data-vc-local="1"]');
      if (v && this.localStream) {
        // Replace the video track in localStream.
        const old = this.localStream.getVideoTracks()[0];
        if (old) this.localStream.removeTrack(old);
        this.localStream.addTrack(newTrack);
        v.srcObject = this.localStream;
      }
    }
  };

  Call.prototype.startRecording = function () {
    if (this.recorder && this.recorder.state !== 'inactive') return false;
    if (!this.videosEl) return false;
    // Compose local + remote videos onto a hidden canvas + capture stream
    // from it. This gives us a single-file recording even with multiple
    // peers. For 1-on-1 it's just local PiP over remote.
    const canvas = document.createElement('canvas');
    canvas.width = 1280; canvas.height = 720;
    const ctx = canvas.getContext('2d');
    const draw = () => {
      if (this.stopped || !this.recorder || this.recorder.state === 'inactive') return;
      ctx.fillStyle = '#000';
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      // Remote first (full-frame)
      const remotes = this.videosEl.querySelectorAll('video[data-vc-peer]');
      if (remotes.length) {
        const v = remotes[0];
        if (v.videoWidth) {
          ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
        }
      }
      // Local PiP (bottom-right, 25% width)
      const local = this.videosEl.querySelector('video[data-vc-local="1"]');
      if (local && local.videoWidth) {
        const w = canvas.width * 0.25, h = (w * local.videoHeight) / local.videoWidth;
        ctx.drawImage(local, canvas.width - w - 16, canvas.height - h - 16, w, h);
      }
      requestAnimationFrame(draw);
    };
    let canvasStream;
    try { canvasStream = canvas.captureStream(20); } catch (_) { return false; }
    // Mix audio tracks (local mic + each peer's incoming audio).
    const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
    const dst = audioCtx.createMediaStreamDestination();
    try {
      if (this.localStream) {
        const src = audioCtx.createMediaStreamSource(this.localStream);
        src.connect(dst);
      }
    } catch (_) {}
    for (const id in this.peers) {
      const v = this.videosEl.querySelector('video[data-vc-peer="' + id + '"]');
      if (v && v.srcObject) {
        try { audioCtx.createMediaStreamSource(v.srcObject).connect(dst); } catch (_) {}
      }
    }
    dst.stream.getAudioTracks().forEach(t => canvasStream.addTrack(t));

    // Pick the best supported codec for the recording container.
    let mime = 'video/webm;codecs=vp9,opus';
    if (!MediaRecorder.isTypeSupported(mime)) mime = 'video/webm;codecs=vp8,opus';
    if (!MediaRecorder.isTypeSupported(mime)) mime = 'video/webm';
    try {
      this.recorder = new MediaRecorder(canvasStream, { mimeType: mime, videoBitsPerSecond: 2_500_000 });
    } catch (e) {
      this.cbError('MediaRecorder: ' + e.message);
      return false;
    }
    this.recorderChunks = [];
    this.recorder.ondataavailable = (ev) => { if (ev.data && ev.data.size) this.recorderChunks.push(ev.data); };
    this.recorder.onstop = () => this.downloadRecording();
    this.recorder.start(1000);
    this.recordingStarted = Date.now();
    requestAnimationFrame(draw);
    this.send({ type: 'state', payload: jsonRaw({ recording: 'on' }) });
    this.cbState({ type: 'recording', on: true });
    return true;
  };

  Call.prototype.stopRecording = function () {
    if (!this.recorder || this.recorder.state === 'inactive') return;
    try { this.recorder.stop(); } catch (_) {}
    this.send({ type: 'state', payload: jsonRaw({ recording: 'off' }) });
    this.cbState({ type: 'recording', on: false });
  };

  Call.prototype.downloadRecording = function () {
    const chunks = this.recorderChunks;
    this.recorderChunks = [];
    this.recorder = null;
    if (!chunks.length) return;
    const blob = new Blob(chunks, { type: chunks[0].type || 'video/webm' });
    const durationS = this.recordingStarted ? Math.round((Date.now() - this.recordingStarted) / 1000) : 0;
    // Se o caller registrou onRecordingReady, deixa ele decidir (upload pra
    // nuvem, salvar local, ou ambos). Caso contrário cai no download local.
    if (typeof this.cbRecordingReady === 'function') {
      try { this.cbRecordingReady({ blob, durationS, mimeType: blob.type }); return; }
      catch (e) { /* fallback */ }
    }
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
    a.href = url; a.download = 'videocall-' + stamp + '.webm';
    document.body.appendChild(a); a.click(); a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  };

  // -- Privacy frost (full-frame blur) -------------------------------
  // We route the camera through a hidden <video> → blurred canvas →
  // captureStream → replaceTrack. Not background-only segmentation — just
  // a privacy curtain. Toggle off restores the camera track unchanged.
  Call.prototype.setPrivacyFrost = async function (on) {
    if (on && !this.frostActive) {
      if (!this.cameraTrack) return false;
      const src = document.createElement('video');
      src.autoplay = true; src.muted = true; src.playsInline = true;
      src.srcObject = new MediaStream([this.cameraTrack]);
      try { await src.play(); } catch (_) {}
      this.frostVideoEl = src;
      const canvas = document.createElement('canvas');
      canvas.width = 640; canvas.height = 360;
      this.frostCanvas = canvas;
      const ctx = canvas.getContext('2d');
      const draw = () => {
        if (!this.frostActive) return;
        ctx.filter = 'blur(18px) saturate(0.6)';
        try { ctx.drawImage(src, 0, 0, canvas.width, canvas.height); } catch (_) {}
        ctx.filter = 'none';
        // Discreet "Privacidade ativa" label overlay
        ctx.fillStyle = 'rgba(0,0,0,0.6)';
        ctx.fillRect(0, canvas.height - 36, canvas.width, 36);
        ctx.fillStyle = '#fff';
        ctx.font = '14px sans-serif';
        ctx.fillText('🌫  Privacidade ativa', 12, canvas.height - 14);
        this.frostRAF = requestAnimationFrame(draw);
      };
      this.frostActive = true;
      requestAnimationFrame(draw);
      const stream = canvas.captureStream(20);
      const blurTrack = stream.getVideoTracks()[0];
      try {
        await this.swapVideoSenderTrack(blurTrack);
      } catch (e) {
        // Cleanup ao falhar — antes, canvas + video ficavam vivos
        // pra sempre, deixando o vídeo borrado mesmo após "desligar".
        // (Auditoria A7)
        this.frostActive = false;
        if (this.frostRAF) { cancelAnimationFrame(this.frostRAF); this.frostRAF = 0; }
        this.frostCanvas = null;
        if (this.frostVideoEl) { try { this.frostVideoEl.srcObject = null; } catch (_) {} this.frostVideoEl = null; }
        try { stream.getTracks().forEach(t => t.stop()); } catch (_) {}
        this.cbError('Privacidade: ' + e.message);
        return false;
      }
      this.send({ type: 'state', payload: jsonRaw({ frost: 'on' }) });
      this.cbState({ type: 'frost', on: true });
      return true;
    }
    if (!on && this.frostActive) {
      this.frostActive = false;
      if (this.frostRAF) cancelAnimationFrame(this.frostRAF);
      if (this.cameraTrack) await this.swapVideoSenderTrack(this.cameraTrack);
      this.frostCanvas = null; this.frostVideoEl = null; this.frostRAF = 0;
      this.send({ type: 'state', payload: jsonRaw({ frost: 'off' }) });
      this.cbState({ type: 'frost', on: false });
      return false;
    }
    return this.frostActive;
  };

  // -- File transfer (DataChannel chunked) ---------------------------
  // Each file gets its own JSON-framed protocol over the per-peer
  // `vpsm-files` channel:
  //
  //   [{type:'file-start', id, name, size, mime, chunks}]
  //   binary chunks (ArrayBuffer) — 16KB each, in order, by id
  //   [{type:'file-end', id}]
  //
  // We don't multiplex transfers — if two start at once, the second
  // waits. Keeps the receiver state minimal.
  Call.prototype.sendFile = async function (file) {
    const peers = Object.values(this.peers);
    if (!peers.length) return;
    for (const peer of peers) {
      await peer.sendFile(file);
    }
  };

  // -- Whiteboard broadcast ------------------------------------------
  // Soft cap on buffered strokes. A stroke is ~100 bytes of JSON; 4000 keeps a
  // full snapshot well under the ~256KB DataChannel message ceiling. Beyond the
  // cap we drop the oldest segments (chunked snapshots are a future option).
  Call.WB_STROKE_CAP = 4000;

  // Record a whiteboard event into the shared buffer so it can be replayed to a
  // (re)joining peer and redrawn after a resize. `wb-line` accumulates; any
  // `wb-clear` (from any side) resets the board. `_from` is undefined for
  // locally-drawn strokes and the origin peer id for received ones.
  //
  // route by `ev.surface`. 'screen' => the annotation overlay buffer;
  // anything else (including absent, for backward-compat with pre-surface peers)
  // => the collaborative board. A `wb-clear` only zeroes its own surface so the
  // two layers never clobber each other. `alpha` rides along for the highlighter.
  Call.prototype._wbRecord = function (ev) {
    if (!ev) return;
    const buf = ev.surface === 'screen' ? this._annotStrokes : this._wbStrokes;
    if (ev.type === 'wb-clear') { buf.length = 0; return; }
    if (ev.type !== 'wb-line' || !ev.from || !ev.to) return;
    buf.push({ from: ev.from, to: ev.to, color: ev.color, width: ev.width, alpha: ev.alpha, _from: ev._from });
    if (buf.length > Call.WB_STROKE_CAP) {
      buf.splice(0, buf.length - Call.WB_STROKE_CAP);
    }
  };

  Call.prototype.broadcastWhiteboard = function (ev) {
    // Buffer the local stroke (no _from => authored here) before sending so the
    // snapshot to a future joiner includes strokes WE drew, not just received.
    this._wbRecord(ev);
    for (const id in this.peers) {
      this.peers[id].sendWhiteboard(ev);
    }
  };

  // Atualiza o caption visual abaixo do tile de um peer. Chamado pelo
  // próprio Call quando recebe caption events (do Web Speech remoto via
  // DataChannel ou do próprio Speech local). Texto vazio esconde.
  Call.prototype.updatePeerCaption = function (peerId, text, isLocal) {
    if (!this.videosEl) return;
    // _showLegendas controla SÓ a sobreposição visual em vídeo. Texto continua
    // emitindo via cbState (transcript panel + resumo permanecem populando).
    // Default true (legendas on); UI altera via setShowLegendas(false).
    if (this._showLegendas === false) {
      // Remove qualquer caption já renderizada pra zerar imediato.
      try {
        const localCap = this.videosEl.querySelector('[data-vc-local-caption]');
        if (localCap) localCap.style.display = 'none';
        const allPeerCaps = this.videosEl.querySelectorAll('[data-vc-peer-caption]');
        allPeerCaps.forEach(el => el.style.display = 'none');
      } catch (_) {}
      return;
    }
    // Local caption fica em um overlay separado (data-vc-local-caption)
    // já que o local video tem o pattern data-vc-local="1" — não tem tile.
    if (isLocal || peerId === 'me') {
      // Limpa DUPLICATAS antes de pegar/criar — bug: captions órfãs ficavam
      // em parentElement diferente quando videosEl mudava entre renegotiations.
      // Resultado: várias caption divs empilhadas, cada uma com partial diferente.
      try {
        const scopes = [this.videosEl, this.videosEl.parentElement].filter(Boolean);
        const allCaps = [];
        for (const scope of scopes) {
          scope.querySelectorAll('[data-vc-local-caption]').forEach(el => allCaps.push(el));
        }
        // Mantém só o ÚLTIMO criado; remove resto. Se nenhum exists, próximo
        // bloco cria um novo.
        if (allCaps.length > 1) {
          for (let i = 0; i < allCaps.length - 1; i++) {
            try { allCaps[i].remove(); } catch (_) {}
          }
        }
      } catch (_) {}
      let cap = this.videosEl.querySelector('[data-vc-local-caption]')
             || (this.videosEl.parentElement && this.videosEl.parentElement.querySelector('[data-vc-local-caption]'));
      if (!cap && this.videosEl.parentElement) {
        // Cria caption local anchorada no stage (centralizada inferior — só
        // pra dar feedback do que o user está dizendo).
        cap = document.createElement('div');
        cap.setAttribute('data-vc-local-caption', '1');
        cap.style.cssText = 'position:absolute;left:50%;transform:translateX(-50%);bottom:108px;background:rgba(70,120,249,.85);color:#fff;font-size:14px;padding:5px 12px;border-radius:8px;display:none;pointer-events:none;z-index:5;max-width:60%;text-align:center;';
        this.videosEl.parentElement.appendChild(cap);
      }
      if (cap) {
        if (text) { cap.textContent = '🎤 ' + text; cap.style.display = 'block'; }
        else { cap.style.display = 'none'; cap.textContent = ''; }
      }
      return;
    }
    const cap = this.videosEl.querySelector('[data-vc-peer-caption="' + peerId + '"]');
    if (!cap) return;
    if (text) { cap.textContent = text; cap.style.display = 'block'; }
    else { cap.style.display = 'none'; }
  };

  // setShowLegendas controla SÓ a sobreposição visual. Se false, transcript
  // panel + resumo continuam populando com o texto (STT segue rodando).
  // Útil quando você quer gravar tudo pro resumo mas não quer letra sobre
  // a imagem do peer durante a chamada.
  Call.prototype.setShowLegendas = function (show) {
    this._showLegendas = !!show;
    if (!this._showLegendas) {
      // Remove ATUAL + DUPLICATAS órfãs em parentElement do videosEl (bug:
      // captions empilhavam quando videosEl mudava entre renegotiations).
      try {
        // Local caption pode estar em videosEl OU parentElement (criada via
        // appendChild(parent) na primeira chamada de updatePeerCaption local).
        // Catamos AMBOS escopos pra limpar dupes.
        const scopes = [this.videosEl, this.videosEl && this.videosEl.parentElement].filter(Boolean);
        for (const scope of scopes) {
          scope.querySelectorAll('[data-vc-local-caption]').forEach(el => {
            el.style.display = 'none';
            el.textContent = '';
          });
          scope.querySelectorAll('[data-vc-peer-caption]').forEach(el => {
            el.style.display = 'none';
            el.textContent = '';
          });
        }
      } catch (_) {}
    }
    this.cbState({ type: 'legendas-show', show: this._showLegendas });
  };

  // -- Live subtitles (VPSMSTT adapter — whisper-local OR web-speech) --------
  // Roda local: transcreve sua voz e envia texto via data channel pros peers.
  // Backend default: whisper.cpp + Silero VAD via stt-proxy (WER 5-6% PT-BR,
  // timestamps por palavra, confidence per-word, sem hallucinations em
  // silêncio). Fallback transparente pra Web Speech API se backend offline.
  // Broadcast pra TODOS os peers ativos: ativem/desativem STT local.
  // Cada peer transcreve seu próprio áudio e envia o resultado via DC.
  // Resultado UX: 1 usuário ativa transcription → falas de TODOS aparecem
  // pra todos. Idempotente: peer que já tem STT ativo ignora request.
  // Retorna {sent: N, pending: N, total: N} pro caller dar feedback ao user.
  Call.prototype._broadcastSubtitlesRequest = function (on, lang) {
    if (!this.peers) return { sent: 0, pending: 0, total: 0 };
    const payload = JSON.stringify({
      type: 'subtitles-request',
      on: !!on,
      lang: lang || 'pt-BR',
      requestedBy: this.displayName || 'me',
      ts: Date.now(),
    });
    let sent = 0, pending = 0, total = 0;
    for (const id in this.peers) {
      total++;
      const p = this.peers[id];
      if (p && p.dc && p.dc.readyState === 'open') {
        try { p.dc.send(payload); sent++; }
        catch (e) { console.warn('[vpsm:vc] subs broadcast fail peer=' + id.slice(-6) + ': ' + e.message); }
      } else {
        pending++;
        // DC ainda não está open: o setupDataChannel.dc.onopen handler vai
        // checar _subtitlesActive e enviar quando abrir. Não precisa queue
        // local — o flag global no Call é a fonte de verdade.
      }
    }
    console.log('[vpsm:vc] subtitles-request broadcast: on=' + on +
                ' sent=' + sent + ' pending=' + pending + ' total=' + total);
    return { sent, pending, total };
  };

  // _assignSubsHandle: normaliza o retorno de startWithBackend, que é
  // uma SESSION síncrona (web-speech), uma Promise (whisper-local async start)
  // ou null (backend indisponível). Sem isso, _subtitlesHandle virava a Promise
  // crua no whisper-local e todos os reads (.stop/.setGain) viravam no-op —
  // OFF não derrubava o WS, fallback vazava worklet, etc. O guard de
  // _subtitlesActive cobre a race: se o user desligou enquanto a Promise
  // resolvia, paramos o handle recém-chegado em vez de o deixar órfão.
  Call.prototype._assignSubsHandle = function (p) {
    Promise.resolve(p).then((h) => {
      if (!this._subtitlesActive) {
        try { h && h.stop && h.stop(); } catch (_) {}
        return;
      }
      this._subtitlesHandle = h;
      // start retornou null (no-backend) → reporta falha honesta ao iniciador.
      if (!h && this._sendSubsStatus) this._sendSubsStatus(false, 'start-failed', this._subtitlesBackend);
    });
  };

  // _sendSubsStatus: round-trip de confirmação. Quando ESTE peer foi
  // remote-ativado por outro (o iniciador), devolve um ack/status pro DC dele
  // dizendo se o STT realmente subiu. Hoje o iniciador só sabe que o dc.send()
  // ocorreu — não que o motor do outro lado funcionou. ack é one-shot (idempotente
  // via _subtitlesAckSent); status de falha pode repetir. Unidirecional por design
  // (só o peer ativado confirma; Fase 1 é 1-a-1 — ver TODO(group)).
  Call.prototype._sendSubsStatus = function (ok, reason, backend) {
    const id = this._subtitlesRequestedById;
    if (!id) return;
    if (ok && this._subtitlesAckSent) return;
    const p = this.peers[id];
    if (p && p.dc && p.dc.readyState === 'open') {
      if (ok) this._subtitlesAckSent = true;
      try {
        p.dc.send(JSON.stringify({
          type: ok ? 'subtitles-ack' : 'subtitles-status',
          ok: !!ok, reason: reason || '', backend: backend || '',
        }));
      } catch (_) {}
    }
  };

  Call.prototype.setSubtitles = function (on, opts) {
    opts = opts || {};
    // M26: idempotency guard — bloqueia rapid toggle off→on em <500ms
    // que confunde peers remotos. Permite legítimo (cliques distantes) mas
    // recusa o "stutter" típico de UI mal sincronizada.
    const now = Date.now();
    if (this._lastSubtitlesToggleAt && now - this._lastSubtitlesToggleAt < 500) {
      const prev = this._lastSubtitlesToggleValue;
      if (prev !== on) {
        console.warn('[vpsm:vc] setSubtitles toggle ignorado (rapid stutter ' +
                    (now - this._lastSubtitlesToggleAt) + 'ms)');
        return this._subtitlesActive;
      }
    }
    this._lastSubtitlesToggleAt = now;
    this._lastSubtitlesToggleValue = on;
    if (on && !this._subtitlesActive) {
      if (!window.VPSMSTT) { this.cbError('subtítulos: módulo STT não carregado'); return false; }

      // M23: throttle de partials pra max ~4/s. Final SEMPRE passa.
      // Web-speech emite partial 10-20×/s em fala contínua e jogava todos
      // pro DC, dobrando bw da chamada e travando UI em mobile lento.
      // Trailing-edge: último partial drop dispara após cooldown pra garantir
      // que o texto mais recente sempre chega na tela.
      this._lastCaptionPartialAt = 0;
      this._captionTrailingTimer = null;
      this._captionTrailingPayload = null;
      const sendCaptionNow = (payload) => {
        for (const id in this.peers) {
          if (this.peers[id].dc && this.peers[id].dc.readyState === 'open') {
            try { this.peers[id].dc.send(JSON.stringify(payload)); } catch (_) {}
          }
        }
        this.cbState({ type: 'caption', from: 'me', text: payload.text, final: !!payload.final, words: payload.words, confidence: payload.confidence });
        this.updatePeerCaption('me', payload.text, true);
        // Auto-clear timer — antes só rodava em `final` e a legenda local
        // ficava visível pra sempre quando WhisperLive demorava a marcar
        // segmento completed (medium model ~1-4s no EPYC; o 8-16s antigo era do
        // whisper.cpp, removido — ver [[project_vpsm_v2_whisperlive]]) ou final nunca
        // chegava. Reset a cada update — enquanto user fala, fica visível;
        // 6s após último partial / 2.5s após final, some.
        if (this._localCapClearTimer) clearTimeout(this._localCapClearTimer);
        const delay = payload.final ? 2500 : 6000;
        this._localCapClearTimer = setTimeout(() => {
          this.updatePeerCaption('me', '', true);
          this._localCapClearTimer = null;
        }, delay);
      };
      const broadcastCaption = (text, isFinal, extra) => {
        if (!text) return;
        const payload = { type: 'caption', text, final: !!isFinal, ts: Date.now() };
        if (extra) {
          if (Array.isArray(extra.words) && extra.words.length) payload.words = extra.words;
          if (typeof extra.confidence === 'number') payload.confidence = extra.confidence;
          if (extra.lang) payload.lang = extra.lang;
          if (typeof extra.startMs === 'number') payload.startMs = extra.startMs;
          if (typeof extra.endMs === 'number') payload.endMs = extra.endMs;
        }
        // M26 BUG#5: inclui displayName no payload pra eliminar race com
        // peersList. Receiver usa payload.displayName direto sem depender
        // de peer-count chegar antes da primeira caption.
        if (this.displayName && this.displayName !== 'Você') {
          payload.displayName = this.displayName;
        }
        if (isFinal) {
          // Final cancela trailing pendente e dispara já.
          if (this._captionTrailingTimer) {
            clearTimeout(this._captionTrailingTimer);
            this._captionTrailingTimer = null;
            this._captionTrailingPayload = null;
          }
          sendCaptionNow(payload);
          this._lastCaptionPartialAt = Date.now();
          return;
        }
        const now = Date.now();
        const elapsed = now - this._lastCaptionPartialAt;
        if (elapsed >= 250) {
          sendCaptionNow(payload);
          this._lastCaptionPartialAt = now;
          if (this._captionTrailingTimer) {
            clearTimeout(this._captionTrailingTimer);
            this._captionTrailingTimer = null;
            this._captionTrailingPayload = null;
          }
        } else {
          // Guarda último, dispara trailing.
          this._captionTrailingPayload = payload;
          if (!this._captionTrailingTimer) {
            this._captionTrailingTimer = setTimeout(() => {
              this._captionTrailingTimer = null;
              if (this._captionTrailingPayload && this._subtitlesActive) {
                sendCaptionNow(this._captionTrailingPayload);
                this._lastCaptionPartialAt = Date.now();
              }
              this._captionTrailingPayload = null;
            }, 250 - elapsed);
          }
        }
      };

      // Anti-loop guard: conta restarts consecutivos sem nenhum resultado.
      // Threshold 5 (era 3) + janela 15s (era 10) — web-speech reinicia
      // legitimamente quando há silêncio, contar isso como falha era ruim.
      // Reset COMPLETO ao ativar manualmente — permite recovery após
      // loop-guard ter desligado anteriormente.
      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
      }
      const startWithBackend = (backend) => {
        // Track cru do microfone (ver getMicStreamForSTT).
        const sttStream = this.getMicStreamForSTT ? this.getMicStreamForSTT() : this.localStream;
        return window.VPSMSTT.start({
          backend,
          continuous: true,
          interimResults: true,
          lang: opts.lang || 'pt-BR',
          // Token priority: opts.token > this.token (Call WS token — funciona
          // pra guests via invite/PIN) > __VPSMTOKEN__ global (só user logado).
          // Sem this.token, convidados nunca tinham token válido pro STT e
          // falhavam com 'no-token' → fallback web-speech → loop-guard tripou.
          token: opts.token || this.token || (window.__VPSMTOKEN__ || null),
          stream: sttStream,
          prompt: opts.prompt || '',
          idleMs: 20000,
          // onReady = STT realmente subiu (web-speech onstart / whisper
          // SERVER_READY). Confirma o ack ao iniciador e mata o connect-timeout.
          onReady: () => {
            this._subtitlesLastSuccessAt = Date.now();
            if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
            this._sendSubsStatus(true, '', this._subtitlesBackend);
          },
          onPartial: (p) => {
            this._subtitlesLastSuccessAt = Date.now();
            this._subtitlesRestartCount = 0;
            // Backstop do onReady: chegou texto → STT está vivo. Idempotente.
            if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
            this._sendSubsStatus(true, '', this._subtitlesBackend);
            broadcastCaption(p.text, false, null);
          },
          onFinal: (p) => {
            this._subtitlesLastSuccessAt = Date.now();
            this._subtitlesRestartCount = 0;
            if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
            this._sendSubsStatus(true, '', this._subtitlesBackend);
            broadcastCaption(p.text, true, {
              words: p.words, confidence: p.confidence, lang: p.lang,
              startMs: p.startMs, endMs: p.endMs,
            });
          },
          onError: (e) => {
            if (e.fatal && backend === 'whisper-local') {
              console.warn('[vpsm:vc] whisper-local falhou (' + e.code + '), fallback pra web-speech');
              this._subtitlesActive = false; // bloqueia onEnd-restart abaixo
              // M26 BUG#2: cleanup handle anterior antes de criar novo. Sem
              // isso o WebSocket+AudioContext+workletNode do whisper-local
              // ficavam órfãos. Em fallbacks repetidos, mic bloqueado.
              try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
              this._subtitlesHandle = null;
              setTimeout(() => {
                this._subtitlesActive = true;
                this._subtitlesRestartCount = 0; // reset ao trocar de driver
                this._subtitlesLastSuccessAt = Date.now(); // janela de graça
                this._subtitlesBackend = 'web-speech';
                this._assignSubsHandle(startWithBackend('web-speech'));
                this.cbState({ type: 'subtitles-backend', backend: 'web-speech' });
              }, 250);
            } else if (e.fatal) {
              // web-speech também fatal — propaga erro mas NÃO desliga
              // imediatamente. Loop-guard no onEnd decide.
              this.cbError('subtítulos: ' + (e.message || e.code));
              // dead-end real (whisper já caiu + web-speech fatal, ou
              // web-speech direto). Reporta falha honesta ao iniciador + mata o
              // connect-timeout. (upstream-disconnect no whisper cai no branch
              // de fallback acima, não aqui — recuperação, não falha.)
              if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
              this._sendSubsStatus(false, e.code || 'fatal', this._subtitlesBackend);
            }
          },
          onEnd: (e) => {
            if (!this._subtitlesActive) return;
            // M26: se o user está MUTE, STT termina por silêncio. NÃO conta
            // como falha — espera ele desmutar pra retomar. Sem isso, o
            // loop-guard tropeçava em 30-60s pra qualquer um que ficasse
            // mute por mais que alguns segundos.
            const isMuted = this.muted || (this.localStream && this.localStream.getAudioTracks().some(t => !t.enabled));
            if (isMuted) {
              // Schedule retry mais lento (5s) — quando desmutar, retoma rápido.
              // NÃO incrementa counter.
              if (window.VPSM_DEBUG) console.log('[vpsm:vc] subtitles onEnd com mute — pausando restart');
              if (this._subtitlesRestartGuard) return;
              // Step 3b: a sessão JÁ encerrou (onEnd disparou) — nula o
              // handle pra que o kick de unmute (setMuted, guard `!_subtitlesHandle`)
              // consiga re-disparar o restart. Sem isto, desmutar em <5s limpava o
              // guard mas via o handle truthy → nada reiniciava → STT ficava morto
              // até toggle manual. (correção #1 do plano)
              this._subtitlesHandle = null;
              this._subtitlesRestartGuard = setTimeout(() => {
                this._subtitlesRestartGuard = null;
                if (this._subtitlesActive) this._assignSubsHandle(startWithBackend(backend));
              }, 5000);
              return;
            }
            // Loop-guard: 5 restarts (era 3) e janela de 15s (era 10) — em web-speech
            // o browser pode reiniciar sem palavra falada por idle, contar
            // como falha era agressivo demais. `no-token` e erros de config
            // não contam (já viraram fatal acima, fallback pra outro driver).
            const now = Date.now();
            const sinceLastSuccess = now - (this._subtitlesLastSuccessAt || 0);
            if (sinceLastSuccess > 15000) {
              this._subtitlesRestartCount++;
            }
            if (this._subtitlesRestartCount >= 5) {
              console.warn('[vpsm:vc] subtitles loop-guard tripou — desligando (backend=' + backend + ')');
              this._subtitlesActive = false;
              this.cbState({ type: 'subtitles-state', active: false, source: 'loop-guard' });
              this.cbError('Legendas desligadas — ' + backend + ' instável. Toque o botão 🎙 pra tentar novamente.');
              return;
            }
            if (this._subtitlesRestartGuard) return;
            // Backoff exponencial: 1s, 2s, 4s, 8s, 16s (cap)
            const delay = Math.min(16000, 1000 * Math.pow(2, this._subtitlesRestartCount));
            console.log('[vpsm:vc] subtitles restart em ' + delay + 'ms (tentativa ' + (this._subtitlesRestartCount + 1) + ', backend=' + backend + ', reason=' + (e.reason || 'unknown') + ', code=' + (e.code || '?') + ')');
            this._subtitlesRestartGuard = setTimeout(() => {
              this._subtitlesRestartGuard = null;
              if (this._subtitlesActive) this._assignSubsHandle(startWithBackend(backend));
            }, delay);
          },
        });
      };

      // Escolhe backend: whisper-local se servidor disponível, senão web-speech.
      this._subtitlesActive = true;
      this._subtitlesLang = opts.lang || 'pt-BR';
      this._subtitlesBackend = 'web-speech';
      // M26: expõe startWithBackend pra changeSubtitlesLang reinicia in-place
      // sem disparar toggle off-on broadcast.
      this._restartSubtitlesLocalOnly = (backend, lang) => {
        opts.lang = lang;
        this._subtitlesBackend = backend;
        this._assignSubsHandle(startWithBackend(backend));
      };
      // M25: emite estado sincronizado pra UI. Sem isso, quando remote-activate
      // chama setSubtitles(true,{_silentPropagate}), o botão UI ficava OFF
      // visualmente mesmo com STT rodando. Toda transição on/off emite.
      this.cbState({ type: 'subtitles-state', active: true, source: opts._silentPropagate ? 'remote' : 'local' });
      // ARQUITETURA (Jun 2026 — WhisperLive): whisper-local é o DEFAULT.
      //   Stack nova: WhisperLive (Collabora) em Docker com faster-whisper.
      //   Latência típica ~400ms (partial) / ~700ms (final) — viável real-time.
      //   web-speech permanece como fallback se WhisperLive offline ou se user
      //   explicitamente prefere (botão Web Speech na settings da call).
      //   Pré-Jun 2026: whisper.cpp tinha lag de 10s, daí web-speech era default.
      const requestedBackend = opts.backend ||
        (typeof localStorage !== 'undefined' && localStorage.getItem('vpsm_vc_stt_backend')) ||
        'whisper-local';
      (async () => {
        let backend = requestedBackend;
        // Verifica disponibilidade. web-speech: SR_AVAILABLE. whisper-local: probe.
        if (backend === 'whisper-local') {
          const ok = await window.VPSMSTT.probeWhisperLocal();
          if (!this._subtitlesActive) return;
          if (!ok) {
            console.warn('[vpsm:vc] whisper-local indisponível, caindo pra web-speech');
            backend = 'web-speech';
          }
        }
        if (!this._subtitlesActive) return;
        this._subtitlesBackend = backend;
        try { window.VPSMSTT.setBackend(backend); } catch (_) {}
        this._assignSubsHandle(startWithBackend(backend));
        this.cbState({ type: 'subtitles-backend', backend });
        // se fomos remote-ativados (há um iniciador esperando),
        // arma o connect-timeout. Sem ack em 6s → reporta 'connect-timeout'
        // ("ainda conectando" virou "falhou"). Limpo em onReady/partial/final,
        // onError terminal e OFF. Sem requester, _sendSubsStatus é no-op.
        if (this._subtitlesRequestedById) {
          if (this._subsAckTimer) clearTimeout(this._subsAckTimer);
          this._subsAckTimer = setTimeout(() => {
            this._subsAckTimer = null;
            this._sendSubsStatus(false, 'connect-timeout', this._subtitlesBackend);
          }, 6000);
        }
      })();
      // Propaga pra TODOS os peers ativarem STT local também — a menos que
      // este start veio de uma request remota (evita amplification loop).
      // Resultado: 1 user ativa transcription → todos transcrevem em paralelo
      // → todos veem todas as falas via DC broadcast de captions.
      if (!opts._silentPropagate) {
        // sou o INICIADOR desta sessão (não fui remote-ativado) — limpa
        // qualquer requester remoto stale pra meu onReady não mandar ack a um
        // peer antigo. (O caminho remoto seta _subtitlesRequestedById antes de
        // chamar setSubtitles com _silentPropagate=true, então não cai aqui.)
        this._subtitlesRequestedById = null;
        this._subtitlesAckSent = false;
        const r = this._broadcastSubtitlesRequest(true, this._subtitlesLang);
        this.cbState({
          type: 'subtitles-broadcast-result',
          on: true,
          sent: r.sent,
          pending: r.pending,
          total: r.total,
          initiator: 'me',
        });
        // M25: rescue retry pra peers que estavam pending. Em redes ruins
        // o DC pode abrir 500ms-2s depois da chamada estabilizar. O
        // setupDataChannel.dc.onopen JÁ cobre isso pra peers conectados
        // antes; este retry cobre o caso de peer cujo DC abriu MAS o
        // send falhou silencioso (try/catch). 3 tentativas em 1.5/4/8s.
        if (r.pending > 0 || r.sent < r.total) {
          const retries = [1500, 4000, 8000];
          retries.forEach((delay, i) => {
            setTimeout(() => {
              if (!this._subtitlesActive) return;
              const r2 = this._broadcastSubtitlesRequest(true, this._subtitlesLang);
              if (window.VPSM_DEBUG) console.log('[vpsm:vc] subs rescue retry ' + (i+1) + '/3: sent=' + r2.sent + '/' + r2.total);
            }, delay);
          });
        }
      }
      return true;
    }
    if (!on && this._subtitlesActive) {
      this._subtitlesActive = false;
      // M25: emite estado sincronizado.
      this.cbState({ type: 'subtitles-state', active: false, source: opts._silentPropagate ? 'remote' : 'local' });
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      // M23: cancela trailing caption pendente pra não enviar após off.
      if (this._captionTrailingTimer) { clearTimeout(this._captionTrailingTimer); this._captionTrailingTimer = null; }
      this._captionTrailingPayload = null;
      // zera o round-trip — sem requester pendente, sem timer de
      // connect-timeout pra disparar status falso após o desligamento.
      this._subtitlesRequestedById = null;
      this._subtitlesAckSent = false;
      if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      // Off NÃO desliga STT dos outros — cada um controla seu microfone.
      // Só emite request informativo (UI atualiza badge "X ativou/desativou").
      if (!opts._silentPropagate) {
        const r = this._broadcastSubtitlesRequest(false, this._subtitlesLang || 'pt-BR');
        this.cbState({
          type: 'subtitles-broadcast-result',
          on: false,
          sent: r.sent,
          pending: r.pending,
          total: r.total,
          initiator: 'me',
        });
      }
      return false;
    }
    return this._subtitlesActive;
  };

  // M25: troca idioma do STT local SEM toggle off-on broadcast.
  // Antes vcSetSubtitlesLang chamava setSubtitles(false)+setSubtitles(true)
  // que disparava broadcasts off→on em sequência, confundindo o estado dos
  // peers remotos. Este método é cirúrgico: para o handle local, troca lang,
  // reinicia mesmo backend. _subtitlesActive permanece true durante todo o
  // processo — peers remotos não recebem nenhum subtitles-request.
  // getMicStreamForSTT — a transcricao le SEMPRE o track cru: a voz como sai
  // do microfone escolhido, antes do volume de envio, do limitador, do Opus
  // e da rede. Cada lado transcreve o proprio microfone e manda o texto pelo
  // DataChannel, entao nenhum lado depende do audio que chegou pela rede.
  // O volume de envio e pra quem OUVE; Whisper e Web Speech normalizam
  // nivel sozinhos, e amplificar antes so aproximava a voz do clip.
  Call.prototype.getMicStreamForSTT = function () {
    const raw = this._micGainRawTrack;
    if (raw && raw.readyState === 'live') {
      try { return new MediaStream([raw]); } catch (_) {}
    }
    return this.localStream;
  };

  // ---- Owner action helpers ------------------------------------------------
  // Aplica mute remoto. Toggle do microfone local. Server já validou que veio
  // do owner — não precisa re-checar aqui.
  Call.prototype._applyOwnerMute = function (mute, byPeerId) {
    if (mute && !this.muted) {
      // Era this.toggleMic(false) — metodo que nunca existiu; sempre caia no
      // fallback, que desligava os tracks sem marcar _userMutedExplicit nem
      // avisar os peers do estado do mic.
      this.setMuted(true);
      this.cbState({ type: 'mic-muted', by: byPeerId, forced: true });
    }
  };
  Call.prototype._applyOwnerCamera = function (cameraOn, byPeerId) {
    if (!cameraOn && this.localStream) {
      try {
        (this.localStream.getVideoTracks() || []).forEach(t => { t.enabled = false; });
        this.cameraOff = true;
        this.cbState({ type: 'camera-off', by: byPeerId, forced: true });
      } catch (_) {}
    }
  };

  // Owner-API exposta pra UI. Cada uma manda signaling pelo WS; server valida
  // ownership antes de relayar. Sem ownership = silenciosamente ignorado.
  Call.prototype.ownerMutePeer = function (peerId) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    try {
      this.ws.send(JSON.stringify({ type: 'owner-mute', to: peerId }));
      return true;
    } catch (_) { return false; }
  };
  Call.prototype.ownerRequestUnmute = function (peerId) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    try {
      this.ws.send(JSON.stringify({ type: 'owner-unmute', to: peerId }));
      return true;
    } catch (_) { return false; }
  };
  Call.prototype.ownerCameraOffPeer = function (peerId) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    try {
      this.ws.send(JSON.stringify({ type: 'owner-camera', to: peerId }));
      return true;
    } catch (_) { return false; }
  };
  Call.prototype.ownerKickPeer = function (peerId) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    try {
      this.ws.send(JSON.stringify({ type: 'owner-kick', to: peerId }));
      return true;
    } catch (_) { return false; }
  };
  Call.prototype.ownerLockRoom = function (locked) {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    const payload = JSON.stringify({ locked: !!locked });
    try {
      this.ws.send(JSON.stringify({ type: 'owner-lock', payload }));
      this._roomLocked = !!locked;
      return true;
    } catch (_) { return false; }
  };
  Call.prototype.ownerMuteAll = function () {
    let n = 0;
    for (const id in this.peers) {
      if (this.ownerMutePeer(id)) n++;
    }
    return n;
  };

  // Volume de envio do mic, ao vivo. Afeta o que os peers ouvem (e a
  // gravacao); a transcricao nao, ela le o cru. Rampa de 50 ms pra arrastar
  // o slider nao estalar.
  Call.prototype.setMicGain = function (v) {
    const g = clampMicGain(v);
    this._micGain = g;
    if (this._micGainNode && this._micGainCtx) {
      try { this._micGainNode.gain.setTargetAtTime(g, this._micGainCtx.currentTime, 0.015); }
      catch (_) { try { this._micGainNode.gain.value = g; } catch (_) {} }
      this.cbState({ type: 'mic-gain', value: g });
    } else {
      // Sem pipeline: o valor fica guardado, mas nao ha onde aplicar.
      this.cbState({ type: 'mic-gain', value: g, unavailable: true });
    }
    return g;
  };

  Call.prototype.changeSubtitlesLang = function (lang) {
    if (!this._subtitlesActive) {
      this._subtitlesLang = lang || 'pt-BR';
      return false;
    }
    this._subtitlesLang = lang || 'pt-BR';
    const backend = this._subtitlesBackend || 'web-speech';
    try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
    this._subtitlesHandle = null;
    this._subtitlesRestartCount = 0;
    this._subtitlesLastSuccessAt = Date.now();
    // Reativa local-only via mesmo path mas marca silentPropagate pra evitar
    // re-broadcast. Reusa todo o lifecycle de erro/restart.
    setTimeout(() => {
      if (this._subtitlesActive && this._restartSubtitlesLocalOnly) {
        this._restartSubtitlesLocalOnly(backend, this._subtitlesLang);
      }
    }, 100);
    return true;
  };

  // applyAdaptiveDegrade is called from collectStats once per second. Two
  // safety nets:
  //   1. Audio-first mode: if packet loss is sustained for 5s, kill outgoing
  //      video so audio stays clean. Restored when loss subsides.
  //   2. AV1 fallback: if AV1 is the active codec but framerate stays below
  //      20 fps for 5s, force a renegotiation with VP9 preferred.
  Call.prototype.applyAdaptiveDegrade = function (stats) {
    const now = Date.now();
    // (1) audio-first
    if (this.audioFirstMode) {
      const lossPct = stats.packetsLost > 50 ? 100 : 0; // simple threshold
      if (lossPct > 10 || (stats.rtt > 500)) {
        if (!this.poorNetworkSince) this.poorNetworkSince = now;
        // FIX Onda3 A8: warning visual ANTES do auto-video-off. User vê
        // "conexão fraca" e entende por que video pode ser desligado.
        if (now - this.poorNetworkSince > 2500 && !this.weakConnectionNotified) {
          this.weakConnectionNotified = true;
          this.cbState({ type: 'weak-connection', loss: lossPct, rtt: Math.round(stats.rtt || 0) });
        }
        if (now - this.poorNetworkSince > 5000 && !this.autoVideoOff) {
          for (const t of (this.localStream ? this.localStream.getVideoTracks() : [])) t.enabled = false;
          this.autoVideoOff = true;
          this.cbState({ type: 'auto-video-off' });
        }
      } else {
        this.poorNetworkSince = 0;
        if (this.weakConnectionNotified) {
          this.weakConnectionNotified = false;
          this.cbState({ type: 'connection-recovered' });
        }
        if (this.autoVideoOff) {
          for (const t of (this.localStream ? this.localStream.getVideoTracks() : [])) t.enabled = true;
          this.autoVideoOff = false;
          this.cbState({ type: 'auto-video-on' });
        }
      }
    }
    // (2) AV1 fallback
    if (!this.av1Probe.downgraded && stats.codec && /AV1/i.test(stats.codec) && stats.framerate && stats.framerate < 20) {
      if (!this.av1Probe.lowFpsSince) this.av1Probe.lowFpsSince = now;
      if (now - this.av1Probe.lowFpsSince > 5000) {
        this.av1Probe.downgraded = true;
        // M21: REMOVIDO restartIce automático. ICE restart só é apropriado
        // quando ICE state==='failed' (já tratado em oniceconnectionstatechange).
        // Forçar restart em ICE connected é desperdício de bandwidth e gera
        // round extra de SDP-diff que mantém needs-negotiation flag dirty,
        // contribuindo pro loop infinito.
        // A renegotiation natural pós-setCodecPreferences já reseleciona m-line.
        for (const id in this.peers) {
          this.peers[id].forcePreferredCodec('video/VP9');
        }
        this.cbState({ type: 'codec-downgrade', from: 'AV1', to: 'VP9' });
      }
    } else if (stats.framerate >= 20) {
      this.av1Probe.lowFpsSince = 0;
    }
  };

  // ---- PeerConn -------------------------------------------------------

  function PeerConn(opt) {
    this.call = opt.call;
    this.remoteId = opt.remoteId;
    this.remoteUser = opt.remoteUser;
    // FIX: identidade estável do peer remoto (vem do PeerInfo.client_id).
    // Vazio p/ clientes antigos. Usado pela dedup visual em ensurePeer.
    this.remoteClientId = opt.remoteClientId || '';
    this.initiator = !!opt.initiator;
    this.polite = !!opt.polite;
    this.makingOffer = false;
    this.ignoreOffer = false;
    this.dc = null;
    this.lastBytesSent = 0;
    this.lastBytesRecv = 0;
    this.lastStatsTs = 0;

    const cfg = { iceServers: this.call.iceServers, bundlePolicy: 'max-bundle' };
    this.pc = new RTCPeerConnection(cfg);

    // Attach local tracks. addTrack auto-creates transceivers — easier than
    // managing them ourselves.
    for (const t of this.call.localStream.getTracks()) {
      this.pc.addTrack(t, this.call.localStream);
    }

    this.applyCodecPreferences();
    this.applyBitrates(this.call.videoKbps, this.call.audioKbps);

    // E2EE: wire frame encryption before the first SDP exchange. If
    // unsupported on this browser, surface a clear error and continue
    // unencrypted only if the user already accepted (Call sets the
    // passphrase only when they did).
    if (this.call.e2eePassphrase && window.VPSMVideoCallE2EE) {
      window.VPSMVideoCallE2EE.setup(this.pc, this.call.e2eePassphrase, this.call.roomId, {
        // Disparado quando 30+ frames falham descriptografar — quase
        // sempre passphrase incorreta. Antes era tela preta silenciosa.
        // (Auditoria M19)
        onDecryptFail: (count) => {
          this.call.cbError('E2EE: ' + count + ' frames falharam. Passphrase pode estar incorreta — verifique com o outro lado.');
        },
      })
        .then(() => { this.call.e2eeActive = true; })
        .catch(err => this.call.cbError('E2EE: ' + err.message));
    }

    this.pc.ontrack = (ev) => {
      console.log('[vpsm:vc] ontrack peer=' + this.remoteId.slice(-6) + ' kind=' + (ev.track && ev.track.kind) + ' muted=' + (ev.track && ev.track.muted) + ' enabled=' + (ev.track && ev.track.enabled) + ' state=' + (ev.track && ev.track.readyState) + ' streams=' + (ev.streams ? ev.streams.length : 0));
      this.onTrack(ev);
    };
    this._localCandCount = 0;
    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) {
        this._localCandCount++;
        this.call.send({ type: 'ice', to: this.remoteId, payload: jsonRaw(ev.candidate.toJSON()) });
      } else {
        console.log('[vpsm:vc] ICE gathering complete peer=' + this.remoteId.slice(-6));
      }
    };
    // FIX Onda3 A7: extrair restartIce em função compartilhada com flag
    // _restartPending (não timestamp). Antes, ICE state + connection state
    // ambos com throttle 15s podiam disparar 2x restart se ambos virassem
    // failed em <15ms (raro mas existe). Agora flag mutex.
    const tryRestartIce = (reason) => {
      if (this._restartPending) return;
      const now = Date.now();
      if (now - (this._lastIceRestartAt || 0) < 15000) return;
      this._restartPending = true;
      this._lastIceRestartAt = now;
      console.warn('[vpsm:vc] ' + reason + ' — restarting ICE peer=' + this.remoteId.slice(-6));
      try { this.pc.restartIce(); }
      catch (e) { console.error('[vpsm:vc] restartIce threw: ' + e.message); }
      setTimeout(() => { this._restartPending = false; }, 15000);
    };
    this.pc.oniceconnectionstatechange = () => {
      const st = this.pc.iceConnectionState;
      console.log('[vpsm:vc] ICE state peer=' + this.remoteId.slice(-6) + ' = ' + st);
      if (st === 'failed') tryRestartIce('ICE failed');
    };
    this.pc.onconnectionstatechange = () => {
      const cs = this.pc.connectionState;
      console.log('[vpsm:vc] connectionState peer=' + this.remoteId.slice(-6) + ' = ' + cs);
      if (cs === 'failed') tryRestartIce('connectionState failed');
    };
    // M21: throttle robusto baseado em CICLO de negotiation, não em "tempo
    // desde último FIRE". Tracked state:
    //   _negotiationCycleEnd: timestamp da última volta a 'stable' (fim de ciclo)
    //   _negotiationFiredInCycle: true se já emitimos UMA offer neste ciclo
    //   _lastNegotiationAt: timestamp do último FIRE (kept pra compat)
    // Reset em 'stable' permite próximo ciclo começar limpo.
    this._negotiationCycleEnd = 0;
    this._negotiationFiredInCycle = false;
    this.pc.onsignalingstatechange = () => {
      const st = this.pc.signalingState;
      console.log('[vpsm:vc] signalingState peer=' + this.remoteId.slice(-6) + ' = ' + st);
      if (st === 'stable') {
        this._negotiationCycleEnd = Date.now();
        this._negotiationFiredInCycle = false; // reset pro próximo ciclo
        // Rede de seguranca: sinalizacao fechou mas o ICE nunca saiu de 'new'
        // (nenhum par sendo testado) — e o restart so disparava em 'failed',
        // que nunca chega. Chamada muda, sem recuperacao. Medido: em ~1 de 10
        // entradas o RTCPeerConnection de um lado nao gera NENHUM candidato
        // local, nem depois de restartIce com credenciais novas; um
        // RTCPeerConnection novo na mesma aba coleta normalmente. Por isso:
        //   - zero candidatos locais ⇒ esta instancia travou ⇒ recria o par;
        //   - com candidatos ⇒ problema de caminho ⇒ restartIce.
        if (this._iceStallTimer) clearTimeout(this._iceStallTimer);
        this._iceStallTimer = setTimeout(() => {
          this._iceStallTimer = null;
          if (!this.pc || this.pc.signalingState === 'closed') return;
          if (this.pc.iceConnectionState !== 'new' || !this.pc.remoteDescription) return;
          if (!this._localCandCount) this.call._rebuildPeer(this.remoteId, 'nenhum candidato ICE local', true);
          else tryRestartIce('ICE parado em new apos a negociacao');
        }, 5000);
      }
    };
    this.pc.onnegotiationneeded = async () => {
      // M21: PROTEÇÃO ANTI-LOOP em 3 camadas.
      // (1) só renega em stable
      if (this.pc.signalingState !== 'stable') {
        console.warn('[vpsm:vc] onnegotiationneeded ignored — state=' + this.pc.signalingState + ' peer=' + this.remoteId.slice(-6));
        return;
      }
      // (2) só UMA offer por ciclo lógico (até voltar a stable de novo)
      if (this._negotiationFiredInCycle) {
        console.log('[vpsm:vc] onnegotiationneeded coalesced (already-fired-this-cycle) peer=' + this.remoteId.slice(-6));
        return;
      }
      const now = Date.now();
      // (3) cooldown 500ms pós-stable — bloqueia ricochete do flag dirty
      // quando setCodecPreferences/replaceTrack disparam evento legítimo
      // que já foi coberto pelo round anterior. Sem isso, loops de FIRE→
      // answer→stable→FIRE 200-300ms apart ocorriam (observado em produção).
      if (this._negotiationCycleEnd && now - this._negotiationCycleEnd < 500) {
        console.log('[vpsm:vc] onnegotiationneeded coalesced (cooldown post-stable) peer=' + this.remoteId.slice(-6));
        return;
      }
      // (4) throttle clássico — em fluxo inicial (constructor), múltiplos
      // addTrack disparam evento em sequência rápida ANTES do primeiro
      // ciclo. Throttle 200ms coalesce essas em 1 offer combinada.
      if (now - (this._lastNegotiationAt || 0) < 200) {
        console.log('[vpsm:vc] onnegotiationneeded coalesced (throttled) peer=' + this.remoteId.slice(-6));
        return;
      }
      this._lastNegotiationAt = now;
      this._negotiationFiredInCycle = true;
      console.log('[vpsm:vc] onnegotiationneeded FIRE peer=' + this.remoteId.slice(-6) + ' signalingState=' + this.pc.signalingState);
      // Oferta em voo: o lado educado que recebe uma oferta AGORA espera esta
      // assentar e faz rollback explicito (ver handleSignal).
      let ofertaAssentou;
      this._offerInFlight = new Promise((r) => { ofertaAssentou = r; });
      try {
        this.makingOffer = true;
        const offer = await this.pc.createOffer();
        if (this.call.opusTweak) {
          offer.sdp = tweakOpusSdp(offer.sdp, this.call.audioKbps);
        }
        if (this.pc.signalingState !== 'stable') {
          console.warn('[vpsm:vc] onnegotiationneeded aborted post-createOffer — state=' + this.pc.signalingState);
          this._negotiationFiredInCycle = false; // libera próximo tentar
          return;
        }
        await this.pc.setLocalDescription(offer);
        // Se uma oferta remota ja foi aplicada por cima (rollback), esta
        // oferta morreu — manda-la faria o outro lado responder a uma sessao
        // que nao existe mais.
        if (this.pc.signalingState !== 'have-local-offer') {
          console.warn('[vpsm:vc] oferta superada antes do envio — state=' + this.pc.signalingState + ' peer=' + this.remoteId.slice(-6));
          return;
        }
        this.call.send({ type: 'offer', to: this.remoteId, payload: jsonRaw(this.pc.localDescription) });
      } catch (e) {
        this._negotiationFiredInCycle = false;
        this.call.cbError('negotiation: ' + e.message);
      } finally {
        this.makingOffer = false;
        this._offerInFlight = null;
        ofertaAssentou();
      }
    };

    if (this.initiator) {
      // Initiator opens all DCs up-front; the polite side listens via
      // ondatachannel and routes by label.
      this.dc = this.pc.createDataChannel('vpsm-chat', { ordered: true });
      this.setupDataChannel(this.dc);
      this.dcFiles = this.pc.createDataChannel('vpsm-files', { ordered: true });
      this.setupFilesChannel(this.dcFiles);
      // Whiteboard: reliable + ordered. A dropped stroke leaves a permanent gap
      // in a shared drawing (no way to backfill a single missing segment), and
      // the on-open snapshot below relies on guaranteed delivery to bootstrap a
      // (re)joining peer. Latency cost is negligible for sparse stroke events.
      this.dcWB = this.pc.createDataChannel('vpsm-wb', { ordered: true });
      this.setupWhiteboardChannel(this.dcWB);
    } else {
      this.pc.ondatachannel = (ev) => {
        const dc = ev.channel;
        if (dc.label === 'vpsm-chat')        { this.dc = dc; this.setupDataChannel(dc); }
        else if (dc.label === 'vpsm-files')  { this.dcFiles = dc; this.setupFilesChannel(dc); }
        else if (dc.label === 'vpsm-wb')     { this.dcWB = dc; this.setupWhiteboardChannel(dc); }
      };
    }

    // File transfer receiver state — declared once per PeerConn.
    this._fileRx = {
      incoming: new Map(), // id → {meta, chunks, received}
      expecting: null,     // id we're currently receiving binary for
    };
  }

  PeerConn.prototype.setupDataChannel = function (dc) {
    // M22 QW9: binaryType consistente cross-browser (Chrome default 'arraybuffer',
    // Safari historicamente 'blob').
    try { dc.binaryType = 'arraybuffer'; } catch (_) {}
    // Helper: se transcription ativa local, propaga request pra ESSE peer.
    // Idempotente — peer já com STT ativo ignora. Crítico pra late joiners:
    // quem entra DEPOIS que A ativou recebe a request via dc.open.
    const sendSubsReqIfActive = () => {
      if (!this.call || !this.call._subtitlesActive) return;
      try {
        dc.send(JSON.stringify({
          type: 'subtitles-request',
          on: true,
          lang: this.call._subtitlesLang || 'pt-BR',
          requestedBy: this.call.displayName || 'me',
          ts: Date.now(),
        }));
        console.log('[vpsm:vc] subtitles-request enviada (DC open) → peer=' + this.remoteId.slice(-6));
      } catch (e) { console.warn('[vpsm:vc] subs req send fail: ' + e.message); }
    };
    // Polite peer recebe DC via ondatachannel — pode JÁ estar 'open' nesse
    // ponto. Se sim, dispara imediato; senão, registra handler.
    if (dc.readyState === 'open') {
      sendSubsReqIfActive();
    } else {
      // Preserva qualquer onopen pré-existente; chain.
      const prevOnOpen = dc.onopen;
      dc.onopen = (ev) => {
        if (typeof prevOnOpen === 'function') { try { prevOnOpen(ev); } catch (_) {} }
        sendSubsReqIfActive();
      };
    }
    dc.onmessage = (ev) => {
      let payload;
      try { payload = JSON.parse(ev.data); } catch (_) { return; }
      if (!payload || !payload.type) return;
      // M22 QW4: cap em string length pra defender contra peer hostil
      // mandando 10MB de texto. textContent escapa HTML mas tamanho mata UI.
      const safeText = (s) => (typeof s === 'string') ? s.slice(0, 8000) : '';
      if (payload.type === 'chat') {
        this.call.cbChat({ from: this.remoteId, text: safeText(payload.text), ts: Date.now() });
      } else if (payload.type === 'caption') {
        // Live subtitle from this peer.
        // Enriched payload: words[], confidence, lang, startMs/endMs (whisper-local).
        const captionText = safeText(payload.text);
        this.call.cbState({
          type: 'caption',
          from: this.remoteId,
          // M26 BUG#5: propaga displayName se peer enviou — eliminação de race.
          displayName: typeof payload.displayName === 'string' ? safeText(payload.displayName).slice(0, 80) : undefined,
          text: captionText,
          final: !!payload.final,
          words: Array.isArray(payload.words) ? payload.words.slice(0, 200) : undefined,
          confidence: typeof payload.confidence === 'number' ? payload.confidence : undefined,
          lang: typeof payload.lang === 'string' ? payload.lang.slice(0, 8) : undefined,
          startMs: typeof payload.startMs === 'number' ? payload.startMs : undefined,
          endMs: typeof payload.endMs === 'number' ? payload.endMs : undefined,
        });
        // Anchor visual abaixo do tile do peer. Auto-some 3.5s após final.
        this.call.updatePeerCaption(this.remoteId, captionText);
        if (payload.final) {
          // Limpa após 3.5s pra próxima fala
          clearTimeout(this._captionClearTimer);
          this._captionClearTimer = setTimeout(() => this.call.updatePeerCaption(this.remoteId, ''), 3500);
        }
      } else if (payload.type === 'subtitles-request') {
        // Peer remoto pediu pra TODOS ativarem (ou desativarem) STT local.
        // STT é local-only por design: cada peer transcreve sua voz e
        // broadcasta caption via DC. A propagação garante que TODOS rodem
        // STT → TODOS vêem TODAS as falas. Sem este flow, A ativa e só
        // a voz do A aparece (porque só ele roda STT).
        //
        // payload: { type: 'subtitles-request', on: bool, lang?: string, requestedBy?: string, ts?: number }
        const requesterLabel = payload.requestedBy || ('peer ' + this.remoteId.slice(-4));
        console.log('[vpsm:vc] subtitles-request recebida: on=' + payload.on +
                    ' from=' + this.remoteId.slice(-6) + ' by=' + requesterLabel +
                    ' lang=' + (payload.lang || 'pt-BR'));
        // Validação payload básica
        if (typeof payload.on !== 'boolean') return;
        // Emite cbState pra UI saber QUEM solicitou — mesmo se STT local
        // já está ativo (idempotente do POV da UI).
        this.call.cbState({
          type: 'subtitles-requested',
          from: this.remoteId,
          requestedBy: requesterLabel,
          on: !!payload.on,
          lang: payload.lang || 'pt-BR',
        });
        if (payload.on) {
          // registra QUEM pediu (this.remoteId = o iniciador), pra
          // devolver ack/status quando o STT subir (onReady) ou falhar.
          // TODO(group): _subtitlesRequestedById vira Map<peerId,…> antes de 3+ peers.
          this.call._subtitlesRequestedById = this.remoteId;
          this.call._subtitlesAckSent = false;
          if (this.call._subtitlesActive) {
            console.log('[vpsm:vc] STT já ativo, request idempotente skip');
            // Já estou transcrevendo — confirma na hora pro iniciador não ficar
            // no escuro (senão um re-request a peer já-ativo nunca acka).
            this.call._sendSubsStatus(true, '', this.call._subtitlesBackend);
            return;
          }
          // Ativa localmente. NÃO re-propaga (evita amplification loop —
          // o requester já enviou pra TODOS via call._broadcastSubtitlesRequest).
          const attemptActivate = (retries) => {
            if (!window.VPSMSTT) {
              if (retries > 0) {
                // VPSMSTT pode estar lazy-loading. Tenta de novo em 500ms.
                console.log('[vpsm:vc] VPSMSTT não carregado, retry em 500ms (' + retries + ' restantes)');
                setTimeout(() => attemptActivate(retries - 1), 500);
                return;
              }
              console.warn('[vpsm:vc] VPSMSTT não disponível — peer ' +
                          requesterLabel + ' ativou STT mas não posso transcrever minha voz');
              this.call.cbError('Transcrição: ' + requesterLabel +
                                ' ativou mas seu navegador não tem STT carregado');
              // avisa o iniciador que NÃO conseguimos ativar (falha
              // explícita > silêncio). Síncrono via DC (não passa por onReady).
              try { this.dc.send(JSON.stringify({ type: 'subtitles-status', ok: false, reason: 'no-stt-module' })); } catch (_) {}
              return;
            }
            try {
              const ok = this.call.setSubtitles(true, {
                lang: payload.lang || 'pt-BR',
                _silentPropagate: true,
              });
              console.log('[vpsm:vc] STT remote-activated → ' + (ok ? 'OK' : 'FAIL'));
            } catch (e) {
              console.error('[vpsm:vc] STT remote-activate erro: ' + e.message);
            }
          };
          // Até 5 retries de 500ms = 2.5s pra VPSMSTT carregar
          attemptActivate(5);
        }
        // Off request NÃO desativa STT dos outros (cada um controla seu mic).
        // Só emitiu cbState acima — UI mostra toast informativo.
      } else if (payload.type === 'subtitles-status' || payload.type === 'subtitles-ack') {
        // ack/status do peer que ativamos remotamente. Encaminha pra UI
        // (00-shell) — é a confirmação REAL de que o STT do outro lado subiu
        // (ou falhou). Antes o iniciador só sabia que o dc.send() ocorreu, nunca
        // que o motor do peer funcionou → falha muda.
        this.call.cbState({
          type: payload.type,
          from: this.remoteId,
          ok: !!payload.ok,
          reason: payload.reason || '',
          backend: payload.backend || '',
        });
      }
    };
  };

  PeerConn.prototype.sendChat = function (text) {
    if (!this.dc || this.dc.readyState !== 'open') return false;
    try { this.dc.send(JSON.stringify({ type: 'chat', text: text })); return true; }
    catch (_) { return false; }
  };

  // ---- File transfer (per-peer DataChannel `vpsm-files`) ----
  PeerConn.prototype.setupFilesChannel = function (dc) {
    dc.binaryType = 'arraybuffer';
    dc.onmessage = (ev) => {
      const rx = this._fileRx;
      if (typeof ev.data === 'string') {
        let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; }
        if (msg.type === 'file-start') {
          // Already had a partial? Keep it for resume; otherwise fresh.
          if (!rx.incoming.has(msg.id)) {
            rx.incoming.set(msg.id, { meta: msg, chunks: [], received: 0 });
          }
          rx.expecting = msg.id;
          this.call.cbFileProgress({ id: msg.id, name: msg.name, size: msg.size, received: rx.incoming.get(msg.id).received, direction: 'in', from: this.remoteId });
        } else if (msg.type === 'file-resume?') {
          // Sender asks where we are in the transfer.
          const x = rx.incoming.get(msg.id);
          const offset = x ? x.received : 0;
          try { dc.send(JSON.stringify({ type: 'file-resume', id: msg.id, offset })); } catch (_) {}
        } else if (msg.type === 'file-resume') {
          // We are the sender; remote tells us where to resume from.
          const tx = this.call._fileTxOffsets[msg.id];
          if (tx) { tx.resumeOffset = msg.offset || 0; tx.resumeSignal && tx.resumeSignal(); }
        } else if (msg.type === 'file-end') {
          const x = rx.incoming.get(msg.id);
          if (x) {
            const blob = new Blob(x.chunks, { type: x.meta.mime || 'application/octet-stream' });
            this.call.cbFileReceived({ id: msg.id, name: x.meta.name, size: x.meta.size, mime: x.meta.mime, blob, from: this.remoteId });
            rx.incoming.delete(msg.id);
          }
          rx.expecting = null;
        }
      } else if (ev.data instanceof ArrayBuffer) {
        if (!rx.expecting) return;
        const x = rx.incoming.get(rx.expecting);
        if (!x) return;
        x.chunks.push(ev.data);
        x.received += ev.data.byteLength;
        this.call.cbFileProgress({ id: rx.expecting, name: x.meta.name, size: x.meta.size, received: x.received, direction: 'in', from: this.remoteId });
      }
    };
  };

  PeerConn.prototype.sendFile = async function (file, resumeId) {
    if (!this.dcFiles || this.dcFiles.readyState !== 'open') {
      this.call.cbError('canal de arquivos não está aberto pra ' + this.remoteUser);
      return;
    }
    const id = resumeId || ('f-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7));
    const CHUNK = 16 * 1024;
    const chunks = Math.ceil(file.size / CHUNK);
    let startOffset = 0;
    // Track tx state for the receiver's resume protocol.
    this.call._fileTxOffsets[id] = { offset: 0, file, peerID: this.remoteId, resumeOffset: 0 };
    if (resumeId) {
      // Ask the receiver where they left off.
      try { this.dcFiles.send(JSON.stringify({ type: 'file-resume?', id })); } catch (_) {}
      // Wait up to 2s for the resume response.
      const tx = this.call._fileTxOffsets[id];
      await new Promise(res => {
        tx.resumeSignal = res;
        setTimeout(res, 2000);
      });
      startOffset = (tx.resumeOffset || 0);
    }
    this.dcFiles.send(JSON.stringify({
      type: 'file-start', id, name: file.name, size: file.size, mime: file.type || '', chunks,
      offset: startOffset,
    }));
    const MAX_BUFFERED = 4 * 1024 * 1024;
    let offset = startOffset;
    while (offset < file.size) {
      const slice = file.slice(offset, offset + CHUNK);
      const buf = await slice.arrayBuffer();
      while (this.dcFiles.bufferedAmount > MAX_BUFFERED && this.dcFiles.readyState === 'open') {
        await new Promise(r => setTimeout(r, 50));
      }
      if (this.dcFiles.readyState !== 'open') {
        // DC dropped mid-send. Remember offset for resume.
        this.call._fileTxOffsets[id].offset = offset;
        return;
      }
      this.dcFiles.send(buf);
      offset += buf.byteLength;
      this.call._fileTxOffsets[id].offset = offset;
      this.call.cbFileProgress({ id, name: file.name, size: file.size, sent: offset, direction: 'out', to: this.remoteId });
    }
    this.dcFiles.send(JSON.stringify({ type: 'file-end', id }));
    delete this.call._fileTxOffsets[id];
  };

  // ---- Whiteboard (per-peer DataChannel `vpsm-wb`, reliable + ordered) ----
  PeerConn.prototype.setupWhiteboardChannel = function (dc) {
    // On DC open, hand THIS peer the full board so a (re)joiner sees everything
    // drawn before they connected. Mirrors the subtitles-request bootstrap on
    // the chat channel. Reliable transport guarantees the snapshot lands.
    const sendSnapshot = () => {
      if (!this.call) return;
      // one envelope per non-empty surface, each carrying its own
      // `surface` tag so the receiver routes board strokes to the board buffer
      // and screen-annotation strokes to the annotation buffer. An older
      // peers simply never set `surface` (treated as 'board' on receive).
      try {
        if (this.call._wbStrokes.length) {
          dc.send(JSON.stringify({ type: 'wb-snapshot', surface: 'board', strokes: this.call._wbStrokes }));
        }
        if (this.call._annotStrokes.length) {
          dc.send(JSON.stringify({ type: 'wb-snapshot', surface: 'screen', strokes: this.call._annotStrokes }));
        }
      } catch (e) { console.warn('[vpsm:vc] wb snapshot send fail: ' + e.message); }
    };
    if (dc.readyState === 'open') {
      sendSnapshot();
    } else {
      const prevOnOpen = dc.onopen;
      dc.onopen = (ev) => {
        if (typeof prevOnOpen === 'function') { try { prevOnOpen(ev); } catch (_) {} }
        sendSnapshot();
      };
    }
    dc.onmessage = (ev) => {
      let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; }
      if (!msg || !msg.type) return;
      if (msg.type === 'wb-snapshot') {
        // Strokes carry their own _from (author). Fill missing ones with the
        // sender id (their own locally-drawn strokes) so attribution survives,
        // then buffer them locally too — keeps OUR board complete for any peer
        // we later snapshot. Drawing a duplicate stroke is visually idempotent
        // (identical normalized coords), so cross-relay overlap is harmless.
        //
        // the envelope's `surface` tag routes the whole batch to the
        // right buffer; re-inject it on every stroke so _wbRecord lands them in
        // the matching layer (board strokes never leak into the annotation
        // buffer and vice-versa). Absent => 'board' (an older sender).
        const surface = msg.surface === 'screen' ? 'screen' : 'board';
        const strokes = Array.isArray(msg.strokes) ? msg.strokes : [];
        for (const s of strokes) {
          if (!s._from) s._from = this.remoteId;
          this.call._wbRecord({ type: 'wb-line', surface: surface, from: s.from, to: s.to, color: s.color, width: s.width, alpha: s.alpha, _from: s._from });
        }
        this.call.cbWhiteboard(msg);
        return;
      }
      // Stamp with peer id so the UI can color strokes per author, and buffer it
      // so we can replay/redraw it.
      msg._from = this.remoteId;
      this.call._wbRecord(msg);
      this.call.cbWhiteboard(msg);
    };
  };

  PeerConn.prototype.sendWhiteboard = function (ev) {
    if (!this.dcWB || this.dcWB.readyState !== 'open') return;
    try { this.dcWB.send(JSON.stringify(ev)); } catch (_) {}
  };

  PeerConn.prototype.applyCodecPreferences = function () {
    if (this.call.codecPref === 'auto') {
      // Use the global CODEC_PREF order.
      this.tryReorderCodecs(CODEC_PREF);
      return;
    }
    const explicit = 'video/' + this.call.codecPref;
    this.tryReorderCodecs([explicit].concat(CODEC_PREF.filter(c => c !== explicit)));
  };

  // forcePreferredCodec moves `mime` (e.g. "video/VP9") to the front and
  // triggers renegotiation. Used by Call.applyAdaptiveDegrade for the AV1
  // runtime fallback.
  PeerConn.prototype.forcePreferredCodec = function (mime) {
    this.tryReorderCodecs([mime].concat(CODEC_PREF.filter(c => c !== mime)));
    // M21: REMOVIDO dispatchEvent('negotiationneeded') artificial.
    // setCodecPreferences JÁ marca needs-negotiation flag nativamente
    // (Chrome 100+). O dispatch manual era duplicação + causa principal do
    // loop infinito de FIRE→answer→stable→FIRE observado em produção.
    // O browser vai disparar onnegotiationneeded sozinho no próximo
    // microtask drain.
  };

  PeerConn.prototype.tryReorderCodecs = function (prefOrder) {
    if (!RTCRtpReceiver.getCapabilities) return;
    const caps = RTCRtpReceiver.getCapabilities('video');
    if (!caps || !caps.codecs) return;
    const sorted = [];
    for (const want of prefOrder) {
      for (const c of caps.codecs) if (c.mimeType === want) sorted.push(c);
    }
    // Append the rest so we never strip codecs the browser supports — that
    // would break negotiation when the remote side only has those.
    for (const c of caps.codecs) if (!sorted.includes(c)) sorted.push(c);
    // M21: detectar video transceiver tanto por sender quanto receiver track
    // (audio-only mode tem sender.track=null mas receiver.track ainda kind=video).
    // Sem isso, transceiver perdia codec prefs após replaceTrack(null) e ganhava
    // codec aleatório quando track voltava → outra renegotiation → loop.
    for (const tx of this.pc.getTransceivers()) {
      const senderKind = tx.sender && tx.sender.track && tx.sender.track.kind;
      const receiverKind = tx.receiver && tx.receiver.track && tx.receiver.track.kind;
      const dirHasVideo = tx.direction && /sendrecv|sendonly|recvonly/.test(tx.direction);
      if (senderKind === 'video' || receiverKind === 'video' ||
          (dirHasVideo && (senderKind === 'video' || receiverKind === 'video'))) {
        try { tx.setCodecPreferences(sorted); } catch (_) {}
      }
    }
  };

  /**
   * Apply video + audio bitrate caps. Both go through setParameters which
   * is non-renegotiating (no SDP roundtrip), so it's safe to call multiple
   * times per second. degradationPreference is 'maintain-framerate' on
   * low budgets (under 200kbps prefer keeping motion over sharpness) and
   * 'maintain-resolution' otherwise.
   */
  PeerConn.prototype.applyBitrates = function (videoKbps, audioKbps) {
    for (const sender of this.pc.getSenders()) {
      if (!sender.track) continue;
      const params = sender.getParameters();
      if (!params.encodings || !params.encodings.length) params.encodings = [{}];
      if (sender.track.kind === 'video') {
        params.encodings[0].maxBitrate = videoKbps * 1000;
        params.degradationPreference = (videoKbps <= 200) ? 'maintain-framerate' : 'maintain-resolution';
      } else if (sender.track.kind === 'audio') {
        params.encodings[0].maxBitrate = (audioKbps || 32) * 1000;
      }
      // M22 QW10: log silent setParameters failures pra debug em prod.
      // Firefox sem transactionId, Chrome com degradationPreference mudada
      // mid-call retornam OperationError silenciosamente.
      sender.setParameters(params).catch(e => {
        console.warn('[vpsm:vc] setParameters falhou kind=' + (sender.track && sender.track.kind) + ': ' + e.message);
      });
    }
  };
  // back-compat alias — older code paths still call applyBudget(kbps).
  PeerConn.prototype.applyBudget = function (kbps) {
    this.applyBitrates(kbps, this.call.audioKbps || 32);
  };

  PeerConn.prototype.onTrack = function (ev) {
    const stream = ev.streams && ev.streams[0];
    if (!stream) return;
    let v = this.call.videosEl && this.call.videosEl.querySelector('[data-vc-peer="' + this.remoteId + '"]');
    // se há um tile PRESERVADO em PiP do mesmo cliente (clientId
    // estável), readota ESSE <video> em vez de criar um novo — assim a janela
    // do SO, que ficou aberta durante a reconexão, recebe a mídia restaurada.
    if (!v) {
      v = this.call._adoptDetachedPipTile(this.remoteClientId || '', this.remoteId) || null;
    }
    if (!v) {
      // Wrapper tile: video + caption overlay abaixo do video.
      // Caption fica embaixo do PEER que está falando (não centralizado no stage).
      const tile = document.createElement('div');
      tile.setAttribute('data-vc-peer-tile', this.remoteId);
      tile.style.cssText = 'position:relative;display:flex;flex-direction:column;align-items:center;max-width:640px;width:100%;min-height:180px;';
      // Avatar fallback (visível quando NÃO há vídeo: stream só áudio, peer
      // muted câmera, autorizou só mic em incógnito etc). Cor derivada do
      // peer id pra consistência visual.
      const avatar = document.createElement('div');
      avatar.setAttribute('data-vc-peer-avatar', this.remoteId);
      const hue = (function (s) { let h = 0; for (let i = 0; i < s.length; i++) h = ((h<<5)-h + s.charCodeAt(i)) | 0; return Math.abs(h) % 360; })(this.remoteId);
      const initial = (this.remoteUser || '?').replace(/^guest:/, '').charAt(0).toUpperCase() || '?';
      avatar.style.cssText = 'position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;background:hsl('+hue+',55%,28%);border-radius:8px;color:#fff;z-index:2;gap:8px;pointer-events:none;';
      const ini = document.createElement('div');
      ini.style.cssText = 'width:96px;height:96px;border-radius:50%;background:hsl('+hue+',55%,45%);display:flex;align-items:center;justify-content:center;font-size:48px;font-weight:700;line-height:1;';
      ini.textContent = initial;
      const nameLbl = document.createElement('div');
      nameLbl.setAttribute('data-vc-peer-name', this.remoteId);
      nameLbl.style.cssText = 'font-size:14px;font-weight:500;opacity:.95;';
      nameLbl.textContent = (this.remoteUser || 'Participante').replace(/^guest:/, '');
      const sub = document.createElement('div');
      sub.setAttribute('data-vc-peer-sub', this.remoteId);
      sub.style.cssText = 'font-size:11px;opacity:.7;';
      sub.textContent = '📷 sem câmera';
      avatar.appendChild(ini); avatar.appendChild(nameLbl); avatar.appendChild(sub);
      tile.appendChild(avatar);

      v = document.createElement('video');
      v.setAttribute('data-vc-peer', this.remoteId);
      v.autoplay = true; v.playsInline = true;
      // Video SEMPRE visible com fundo preto. Avatar fica EM CIMA via z-index
      // até o video começar a renderizar frames (videoWidth > 0). Não usamos
      // display:none no video — remote tracks chegam com t.muted=true e o
      // evento 'unmute' não dispara confiável no Chrome, então a velha lógica
      // ficava com video permanentemente escondido mesmo recebendo frames.
      v.style.cssText = 'width:100%;height:auto;border-radius:8px;border:1px solid #1f2937;background:#000;position:relative;z-index:1;';
      tile.appendChild(v);
      // Caption overlay — absolute, anchored ao bottom do tile, visível
      // só quando o peer está falando.
      const cap = document.createElement('div');
      cap.setAttribute('data-vc-peer-caption', this.remoteId);
      cap.style.cssText = 'position:absolute;left:8px;right:8px;bottom:8px;background:rgba(0,0,0,.75);color:#fff;font-size:15px;font-weight:500;padding:6px 12px;border-radius:8px;text-align:center;backdrop-filter:blur(6px);display:none;pointer-events:none;z-index:3;line-height:1.3;';
      tile.appendChild(cap);
      this.call.videosEl && this.call.videosEl.appendChild(tile);
    }
    v.srcObject = stream;
    // Avatar visibility: nunca mexemos no display do <video>. O avatar
    // (z-index:2) começa visible em cima do video preto. Quando o video
    // element decodifica o primeiro frame (videoWidth > 0), escondemos o
    // avatar. Se a track sumir/voltar, o avatar reaparece/oculta.
    //
    // Eventos confiáveis em qualquer browser:
    //   - 'loadedmetadata' / 'resize' — dimensões intrínsecas chegaram
    //   - 'playing' — vídeo está renderizando
    //   - track 'ended' — track foi removida do lado do peer
    const tileEl = this.call.videosEl && this.call.videosEl.querySelector('[data-vc-peer-tile="' + this.remoteId + '"]');
    const avEl = tileEl && tileEl.querySelector('[data-vc-peer-avatar="' + this.remoteId + '"]');
    const hideAvatar = () => {
      if (avEl && v.videoWidth > 0 && v.videoHeight > 0) avEl.style.display = 'none';
    };
    const showAvatarIfNoVideo = () => {
      if (!avEl) return;
      const has = stream.getVideoTracks().some(t => t.readyState === 'live');
      if (!has) avEl.style.display = 'flex';
    };
    // Hooks no video element (não no track).
    v.addEventListener('loadedmetadata', hideAvatar);
    v.addEventListener('resize', hideAvatar);
    v.addEventListener('playing', hideAvatar);
    // Hook em ended das tracks pra reverter (peer desligou câmera).
    const trackEndedHandlers = [];
    stream.getVideoTracks().forEach(t => {
      t.addEventListener('ended', showAvatarIfNoVideo);
      trackEndedHandlers.push({ track: t, handler: showAvatarIfNoVideo });
    });
    stream.addEventListener('removetrack', showAvatarIfNoVideo);
    // M22 QW14: guarda handlers pra remover em PeerConn.close.
    // Sem isso, hideAvatar/showAvatarIfNoVideo seguram closure sobre this.call
    // e vazam memória entre chamadas sucessivas.
    this._videoElCleanup = () => {
      try { v.removeEventListener('loadedmetadata', hideAvatar); } catch (_) {}
      try { v.removeEventListener('resize', hideAvatar); } catch (_) {}
      try { v.removeEventListener('playing', hideAvatar); } catch (_) {}
      try { stream.removeEventListener('removetrack', showAvatarIfNoVideo); } catch (_) {}
      trackEndedHandlers.forEach(({ track, handler }) => {
        try { track.removeEventListener('ended', handler); } catch (_) {}
      });
    };
    // Estado inicial: se já temos width imediatamente (renegotiation), esconde.
    if (v.videoWidth > 0) hideAvatar();
    // Se não tem nenhuma video track (peer só com áudio), avatar permanece.
    if (stream.getVideoTracks().length === 0 && avEl) avEl.style.display = 'flex';
    const spk = this.call.deviceIds && this.call.deviceIds.speaker;
    if (spk && spk !== 'default' && typeof v.setSinkId === 'function') {
      v.setSinkId(spk).catch(() => {});
    }
    // Attach an audio analyser to this remote stream for active-speaker
    // detection. Done only once per peer; if onTrack fires again (e.g.
    // after renegotiation) we reuse the existing monitor.
    if (!this.call._peerAudioMonitors[this.remoteId]) {
      try {
        const audioTracks = stream.getAudioTracks();
        if (audioTracks.length > 0) {
          const audioStream = new MediaStream([audioTracks[0]]);
          const ctx = new (window.AudioContext || window.webkitAudioContext)();
          const src = ctx.createMediaStreamSource(audioStream);
          const analyser = ctx.createAnalyser();
          analyser.fftSize = 256;
          analyser.smoothingTimeConstant = 0.7;
          src.connect(analyser);
          const data = new Uint8Array(analyser.frequencyBinCount);
          const mon = { ctx, analyser, data, level: 0, peerId: this.remoteId, raf: 0 };
          this.call._peerAudioMonitors[this.remoteId] = mon;
          const tick = () => {
            if (this.call.stopped || !this.call._peerAudioMonitors[this.remoteId]) return;
            analyser.getByteFrequencyData(data);
            let sum = 0;
            for (let i = 0; i < data.length; i++) sum += data[i];
            mon.level = sum / data.length;
            mon.raf = requestAnimationFrame(tick);
          };
          tick();
        }
      } catch (e) { /* AudioContext may be suspended; non-fatal */ }
    }
  };

  // Mensagens de sinalizacao de um peer sao processadas EM ORDEM, uma por vez
  // (perfect negotiation do W3C). Antes cada uma rodava solta: uma oferta
  // remota era aplicada no meio da oferta local, um ICE entrava no meio do
  // rollback. Na corrida de ofertas simultaneas isso deixava a chamada muda.
  PeerConn.prototype.handleSignal = function (msg) {
    this._sigChain = (this._sigChain || Promise.resolve())
      .then(() => this._handleSignal(msg))
      .catch((e) => console.warn('[vpsm:vc] signal ' + msg.type + ' falhou: ' + (e && e.message)));
    return this._sigChain;
  };

  PeerConn.prototype._handleSignal = async function (msg) {
    const payload = safeParse(msg.payload);
    if (!payload) return;
    try {
      if (msg.type === 'offer') {
        // Lado educado com oferta propria entre createOffer e
        // setLocalDescription: ainda 'stable', entao o rollback explicito
        // abaixo nao rodava e o setRemoteDescription caia no rollback
        // IMPLICITO do navegador, concorrente com o setLocalDescription — e
        // nesse caminho o Chrome nao gerava candidato ICE local nenhum.
        // Espera a oferta assentar (vira have-local-offer) e segue pelo
        // rollback explicito, o caminho que funciona.
        if (this.polite && this._offerInFlight) {
          try { await this._offerInFlight; } catch (_) {}
        }
        const offerCollision = this.makingOffer || this.pc.signalingState !== 'stable';
        this.ignoreOffer = !this.polite && offerCollision;
        if (this.ignoreOffer) return;
        // FIX glare (Chrome 121+): rollback DEVE ser sequencial antes do
        // setRemoteDescription. Promise.all paralelo gera InvalidStateError
        // "Cannot rollback in this state".
        // M22 QW6: rollback SÓ é válido em have-local-offer. Em outros
        // estados (have-remote-offer, stable) joga "Cannot rollback".
        if (offerCollision && this.pc.signalingState === 'have-local-offer') {
          await this.pc.setLocalDescription({ type: 'rollback' });
          // M26: reset atomicamente pós-rollback. onsignalingstatechange JÁ
          // reseta quando hit stable, mas o handler async pode chegar atrasado
          // e a próxima createOffer ser coalesced silenciosamente. Reset
          // imediato aqui garante que o renegot pós-rollback dispare.
          this._negotiationFiredInCycle = false;
          this._negotiationCycleEnd = Date.now();
        }
        await this.pc.setRemoteDescription(payload);
        // Drena ICE candidates que chegaram antes do remoteDescription
        // estar pronto.
        if (this._pendingIce && this._pendingIce.length) {
          const queue = this._pendingIce;
          this._pendingIce = [];
          for (const cand of queue) {
            try { await this.pc.addIceCandidate(cand); } catch (_) {}
          }
        }
        const answer = await this.pc.createAnswer();
        if (this.call.opusTweak) {
          answer.sdp = tweakOpusSdp(answer.sdp, this.call.audioKbps);
        }
        await this.pc.setLocalDescription(answer);
        this.call.send({ type: 'answer', to: this.remoteId, payload: jsonRaw(this.pc.localDescription) });
      } else if (msg.type === 'answer') {
        if (this.pc.signalingState === 'have-local-offer') {
          await this.pc.setRemoteDescription(payload);
          if (this._pendingIce && this._pendingIce.length) {
            const queue = this._pendingIce;
            this._pendingIce = [];
            for (const cand of queue) {
              try { await this.pc.addIceCandidate(cand); } catch (_) {}
            }
          }
        }
      } else if (msg.type === 'ice') {
        // Pending-ICE queue: ICE candidate antes do remoteDescription pronto
        // enfileira pra drenar depois. Antes morria silente em redes restritas.
        if (!this.pc.remoteDescription || !this.pc.remoteDescription.type) {
          if (!this._pendingIce) this._pendingIce = [];
          // M22 QW13: cap em 200 candidates pra defender contra peer hostil
          // spammando ICE. Em condições normais raramente passa de ~30 candidates.
          if (this._pendingIce.length < 200) {
            this._pendingIce.push(payload);
          } else if (this._pendingIce.length === 200) {
            console.warn('[vpsm:vc] _pendingIce cap atingido peer=' + this.remoteId.slice(-6));
            this._pendingIce.push(payload); // permite o que dispara o warn
          }
          return;
        }
        try { await this.pc.addIceCandidate(payload); }
        catch (e) { if (!this.ignoreOffer) throw e; }
      }
    } catch (e) {
      this.call.cbError('signal ' + msg.type + ': ' + e.message);
    }
  };

  PeerConn.prototype.collectStats = async function () {
    if (!this.pc) return null;
    let stats;
    try { stats = await this.pc.getStats(); } catch (_) { return null; }
    const now = Date.now();
    let bytesSent = 0, bytesRecv = 0, packetsLost = 0, rtt = 0;
    let codec = '', resW = 0, resH = 0, fps = 0, connType = 'direct';
    const codecMap = new Map();
    stats.forEach(r => { if (r.type === 'codec') codecMap.set(r.id, r); });
    stats.forEach(r => {
      if (r.type === 'outbound-rtp' && r.kind === 'video') {
        bytesSent += r.bytesSent || 0;
        if (r.framesPerSecond) fps = r.framesPerSecond;
        if (r.frameWidth && r.frameHeight) { resW = r.frameWidth; resH = r.frameHeight; }
        const c = codecMap.get(r.codecId);
        if (c && c.mimeType) codec = c.mimeType.replace(/^video\//, '');
      } else if (r.type === 'outbound-rtp' && r.kind === 'audio') {
        bytesSent += r.bytesSent || 0;
      } else if (r.type === 'inbound-rtp') {
        bytesRecv += r.bytesReceived || 0;
        packetsLost += r.packetsLost || 0;
      } else if (r.type === 'remote-inbound-rtp') {
        if (r.roundTripTime) rtt = Math.max(rtt, r.roundTripTime * 1000);
      } else if (r.type === 'candidate-pair' && r.nominated && r.state === 'succeeded') {
        if (r.currentRoundTripTime) rtt = Math.max(rtt, r.currentRoundTripTime * 1000);
        // Look up the local candidate to detect relay.
        stats.forEach(c => {
          if (c.id === r.localCandidateId && c.candidateType === 'relay') connType = 'relay';
        });
      }
    });
    let bytesSentPerSec = 0, bytesRecvPerSec = 0;
    if (this.lastStatsTs > 0) {
      const dt = (now - this.lastStatsTs) / 1000;
      if (dt > 0) {
        bytesSentPerSec = Math.max(0, (bytesSent - this.lastBytesSent) / dt);
        bytesRecvPerSec = Math.max(0, (bytesRecv - this.lastBytesRecv) / dt);
      }
    }
    this.lastBytesSent = bytesSent;
    this.lastBytesRecv = bytesRecv;
    this.lastStatsTs = now;
    return {
      bytesSentPerSec, bytesRecvPerSec, packetsLost, rtt,
      codec, resolution: resW && resH ? (resW + 'x' + resH) : '',
      framerate: fps, connectionType: connType,
    };
  };

  PeerConn.prototype.close = function () {
    // M22 QW14: limpa listeners de video element antes de remover o elemento.
    // Sem isso, closures sobre this.call vazam por toda a vida da página.
    if (this._videoElCleanup) {
      try { this._videoElCleanup(); } catch (_) {}
      this._videoElCleanup = null;
    }
    if (this._opusReneTimer) { clearTimeout(this._opusReneTimer); this._opusReneTimer = null; }
    if (this._iceStallTimer) { clearTimeout(this._iceStallTimer); this._iceStallTimer = null; }
    this._pendingIce = null;
    try { if (this.dc) this.dc.close(); } catch (_) {}
    // M22 QW14: zera handlers ICE/negotiation pra liberar GC referências
    // sobre call/this. Sem isso, PCs fechados ficavam vivos via closures.
    try {
      this.pc.onnegotiationneeded = null;
      this.pc.onicecandidate = null;
      this.pc.oniceconnectionstatechange = null;
      this.pc.onsignalingstatechange = null;
      this.pc.ontrack = null;
      this.pc.ondatachannel = null;
    } catch (_) {}
    try { this.pc.close(); } catch (_) {}
    if (this.call.videosEl) {
      // FIX (causa do tile fantasma VISÍVEL): o <video> é filho de um
      // wrapper <div data-vc-peer-tile> que carrega o avatar "📷 sem câmera",
      // o nome e a caption. Remover só o <video> deixava o wrapper órfão no
      // DOM — exatamente o placeholder fantasma que sobrevivia ao peer-left de
      // uma reconexão (agora que a eviction dispara peer-left de forma
      // confiável). Removemos o TILE inteiro; o <video> some junto.
      const v = this.call.videosEl.querySelector('[data-vc-peer="' + this.remoteId + '"]');
      const tile = this.call.videosEl.querySelector('[data-vc-peer-tile="' + this.remoteId + '"]');
      // se ESTE peer está na janela de Picture-in-Picture nativa, NÃO
      // destruir o <video> — removê-lo do DOM fecharia a janela do SO. A janela
      // de PiP fica presa ao ELEMENTO; enquanto ele existir, ela sobrevive. Então
      // preservamos o elemento (a janela congela no último frame) e reanexamos a
      // mídia quando o MESMO cliente (clientId estável) voltar — a reconexão "usa
      // esta tela" pra trazer o usuário de volta. Só na queda de reconexão; num
      // hangup nosso o teardown da chamada limpa tudo normalmente.
      const cid = this.remoteClientId || '';
      if (v && cid && !this.call._userInitiatedHangup &&
          document.pictureInPictureElement && document.pictureInPictureElement === v) {
        this.call._preservePipTile(v, tile, cid);
        const cap = this.call.videosEl.querySelector('[data-vc-peer-caption="' + this.remoteId + '"]');
        if (cap) { try { cap.remove(); } catch (_) {} }
        return; // tile preservado — não remover
      }
      if (v) { try { v.srcObject = null; } catch (_) {} }
      if (tile) {
        tile.remove();
      } else if (v) {
        v.remove(); // fallback defensivo (tile não encontrado, layout legado)
      }
      // Caption do peer pode ter sido criada fora do tile em renegotiations
      // antigas — limpa órfã remanescente por id.
      const cap = this.call.videosEl.querySelector('[data-vc-peer-caption="' + this.remoteId + '"]');
      if (cap) { try { cap.remove(); } catch (_) {} }
    }
  };

  // ---- utilities ------------------------------------------------------

  function jsonRaw(obj) {
    // SignalingMsg.Payload is json.RawMessage server-side, so we send it
    // as a JSON-encoded JSON string. Here we just pass the object — the
    // outer JSON.stringify on the WS send wraps it.
    return obj;
  }

  function safeParse(v) {
    if (v == null) return null;
    if (typeof v === 'object') return v;
    try { return JSON.parse(v); } catch (_) { return null; }
  }

  // FIX: gera um client id estável (32 hex chars). Usado como identidade
  // de cliente persistida em sessionStorage por-aba, de modo que o reconnect de
  // WS evicte o peer fantasma da conexão anterior em vez de virar tile duplicado.
  function _genClientId() {
    try {
      if (window.crypto && crypto.getRandomValues) {
        const a = new Uint8Array(16);
        crypto.getRandomValues(a);
        return Array.from(a, b => b.toString(16).padStart(2, '0')).join('');
      }
    } catch (_) {}
    return 'c' + Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
  }

  // FIX VC #10: traduz strings de erro inglesas legacy que ainda possam vir
  // de versões antigas do server pra PT-BR. Server novo já manda PT-BR via
  // mapJoinErrorPT, mas durante deploy parcial alguns errors podem chegar
  // crus. Match prefix-insensitive.
  function translateLegacyError(msg) {
    if (!msg || typeof msg !== 'string') return msg;
    const m = msg.toLowerCase();
    if (m.includes('room is full'))            return 'Sala cheia — limite de 4 pessoas atingido.';
    if (m.includes('user already in room'))     return 'Sua conta já está nesta sala em outra aba. Feche a outra para entrar aqui.';
    if (m.includes('rate limit'))               return 'Muitas requisições — aguarde alguns segundos.';
    if (m.includes('forbidden'))                return 'Você não é membro desta sala.';
    if (m.includes('peer not in any room'))     return 'Sessão perdida — recarregue a página.';
    if (m.includes('target peer not connected'))return 'O outro lado desconectou.';
    if (m.includes('target peer is not in the same room')) return 'O destinatário não está nesta sala.';
    if (m.includes('peer missing'))             return 'Identificação inválida — recarregue a página.';
    if (m.startsWith('signal '))                return 'Erro de sinalização — a conexão pode estar instável.';
    return msg;
  }
})();
