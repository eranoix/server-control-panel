// VPSMSTT — Speech-to-Text adapter para vps-manager v2.
//
// Backend default: Web Speech API (Chrome/Edge/Safari). Tunado pra PT-BR
// com restart automático, idle grace, e fallback de idioma. Saída via
// callbacks (onPartial / onFinal / onError / onEnd).
//
// O backend é trocável sem mexer nos call-sites — bastam novos drivers em
// VPSMSTT._drivers.<name>. Drivers planejados (não implementados ainda):
//   - 'deepgram'    → WebSocket pra wss://api.deepgram.com/v1/listen
//                     (precisa token; Nova-3 multilingual cobre pt-BR com WER ~5%)
//   - 'whisper-local' → POST /api/stt/transcribe (whisper.cpp via systemd local)
//
// Trocar default: window.VPSMSTT.setBackend('deepgram');
//
// Web Speech tem 3 quirks principais que esse wrapper trata:
//   1. Termina sozinho após silêncio (~5s no Chrome) — restart automático.
//   2. Erro 'no-speech' em locale ruim → fallback pt-PT → en-US.
//   3. Em mobile, exige user gesture; chamada async no click direto OK,
//      em setTimeout NOK (use start({ defer:false }).
(function () {
  'use strict';
  const isBrowser = typeof window !== 'undefined';
  if (!isBrowser) return;

  const SR_AVAILABLE = !!(window.SpeechRecognition || window.webkitSpeechRecognition);
  const LS_KEY = 'vpsm_stt_lang';

  function getLastLang() {
    try { return localStorage.getItem(LS_KEY) || ''; } catch (_) { return ''; }
  }
  function setLastLang(l) {
    try { if (l) localStorage.setItem(LS_KEY, l); } catch (_) {}
  }
  function defaultLang() {
    return getLastLang() || (navigator.language || 'pt-BR');
  }

  // ---- Web Speech driver ----------------------------------------------------
  const webSpeechDriver = {
    name: 'web-speech',
    available() { return SR_AVAILABLE; },
    start(session, opts) {
      const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
      const rec = new SR();
      rec.continuous = !!opts.continuous;
      rec.interimResults = opts.interimResults !== false;
      rec.lang = opts.lang || defaultLang();
      rec.maxAlternatives = 1;
      setLastLang(rec.lang);

      // Fonte: com opts.stream, reconhece ESSE track (Chrome 135+ desktop,
      // SpeechRecognition.start(track)) — na chamada, o mic cru escolhido.
      // Sem isso o Web Speech ouvia o microfone PADRAO do sistema, que pode
      // nem ser o da chamada. Onde start(track) lanca (plataforma sem
      // suporte), cai de vez no start() sem argumento.
      const srcTrack = opts.stream && opts.stream.getAudioTracks ? (opts.stream.getAudioTracks()[0] || null) : null;
      let useTrack = !!srcTrack;
      const startRec = () => {
        if (useTrack && srcTrack.readyState === 'live') {
          try { rec.start(srcTrack); return; }
          catch (e) {
            useTrack = false;
            console.warn('[vpsm:stt] web-speech sem suporte a track (' + e.name + '), usando o mic padrao');
          }
        }
        rec.start();
      };

      let stopRequested = false;
      let restartGuard = 0;
      let idleTimer = null;
      let finalBuffer = '';

      const armIdle = () => {
        if (idleTimer) clearTimeout(idleTimer);
        idleTimer = setTimeout(() => {
          // 7s sem partial: silencia o restart automático mesmo em continuous.
          // Caso contrário, mic fica aberto consumindo bateria em ruído baixo.
          if (!stopRequested) {
            stopRequested = true;
            try { rec.stop(); } catch (_) {}
            if (opts.onEnd) opts.onEnd({ reason: 'idle', text: finalBuffer });
          }
        }, opts.idleMs || 7000);
      };

      rec.onresult = (ev) => {
        let interim = '';
        let nowFinal = '';
        for (let i = ev.resultIndex; i < ev.results.length; i++) {
          const r = ev.results[i];
          if (r.isFinal) nowFinal += r[0].transcript;
          else interim += r[0].transcript;
        }
        if (nowFinal) {
          finalBuffer += (finalBuffer && !/\s$/.test(finalBuffer) ? ' ' : '') + nowFinal.trim();
          if (opts.onFinal) opts.onFinal({ text: nowFinal.trim(), buffer: finalBuffer });
        }
        if (interim && opts.onPartial) opts.onPartial({ text: interim, buffer: finalBuffer });
        armIdle();
      };

      rec.onerror = (ev) => {
        // Erros recuperáveis: no-speech (silêncio), aborted (vc.stop), audio-capture
        // (mic ocupado momentaneamente). Não-recuperáveis: not-allowed (perm),
        // service-not-allowed, network. Sinalize ao caller só os fatais.
        const fatal = ['not-allowed', 'service-not-allowed', 'language-not-supported', 'bad-grammar'];
        if (fatal.indexOf(ev.error) >= 0) {
          stopRequested = true;
          if (opts.onError) opts.onError({ code: ev.error, fatal: true, message: ev.error });
          return;
        }
        if (opts.onError) opts.onError({ code: ev.error, fatal: false, message: ev.error });
      };

      rec.onend = () => {
        if (idleTimer) clearTimeout(idleTimer);
        if (stopRequested) {
          if (opts.onEnd) opts.onEnd({ reason: 'manual', text: finalBuffer });
          return;
        }
        // continuous: API encerra sozinha. Restart até 5x (anti-loop infinito
        // se driver entra em estado quebrado).
        restartGuard++;
        if (restartGuard > 5) {
          if (opts.onEnd) opts.onEnd({ reason: 'restart-exhausted', text: finalBuffer });
          return;
        }
        if (opts.continuous) {
          try { startRec(); armIdle(); }
          catch (e) {
            if (opts.onEnd) opts.onEnd({ reason: 'restart-failed', text: finalBuffer, error: e.message });
          }
        } else {
          if (opts.onEnd) opts.onEnd({ reason: 'natural', text: finalBuffer });
        }
      };

      // onstart = recognition realmente começou a escutar. Âncora do
      // ack pro iniciador (sinal real, não o clique).
      rec.onstart = () => { if (opts.onReady) opts.onReady(); };

      try {
        startRec();
        armIdle();
      } catch (e) {
        if (opts.onError) opts.onError({ code: 'start-failed', fatal: true, message: e.message });
        if (opts.onEnd) opts.onEnd({ reason: 'start-failed', text: '' });
        return null;
      }

      session.recognition = rec;
      session.stop = () => {
        stopRequested = true;
        if (idleTimer) clearTimeout(idleTimer);
        try { rec.stop(); } catch (_) {}
      };
      return session;
    },
  };

  // ---- whisper-local driver (WhisperLive backend via Go bridge) -------------
  // Pipeline: AudioWorklet PCM16 16kHz mono → WS /ws/stt/transcribe → Go bridge
  // → WhisperLive Docker (Collabora) → faster-whisper + Silero VAD interno.
  // Server emite {type:partial,text} enquanto segmento evolui e {type:final}
  // quando segmento é completed. Latência típica 400-700ms, sem dados saindo
  // da VPS. Modelo trocável via env VPSM_STT_MODEL (small/medium/large-v3-turbo).
  //
  // Requer: navegador com AudioWorklet + getUserMedia + WebSocket binary.
  // Token JWT: o objeto `opts.token` é mandatório (passado pelo caller Alpine).
  // Stream local (mic) ou stream remoto (passar opts.stream com MediaStream).
  const whisperLocalDriver = {
    name: 'whisper-local',
    available() {
      return typeof AudioWorklet !== 'undefined'
        && typeof MediaStream !== 'undefined'
        && typeof WebSocket !== 'undefined';
    },
    async start(session, opts) {
      if (!opts.token) {
        if (opts.onError) opts.onError({ code: 'no-token', fatal: true, message: 'token JWT obrigatório' });
        return null;
      }
      let stream = opts.stream || null;
      let ownsStream = false;
      let ws = null;
      let ac = null;
      let workletNode = null;
      let micSource = null;
      let stopped = false;
      let buffer = '';

      const cleanup = () => {
        stopped = true;
        try { if (workletNode) workletNode.port.close(); } catch (_) {}
        try { if (workletNode) workletNode.disconnect(); } catch (_) {}
        try { if (micSource) micSource.disconnect(); } catch (_) {}
        try { if (ac && ac.state !== 'closed') ac.close(); } catch (_) {}
        try { if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'stop' })); } catch (_) {}
        try { if (ws) ws.close(1000, 'caller-stop'); } catch (_) {}
        if (ownsStream && stream) {
          try { stream.getTracks().forEach(t => t.stop()); } catch (_) {}
        }
      };

      try {
        if (!stream) {
          stream = await navigator.mediaDevices.getUserMedia({
            audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
          });
          ownsStream = true;
        }
        // 16kHz nativo — Chrome/Firefox/Safari modernos suportam. Resampling do
        // browser usa polyphase com low-pass filter (qualidade alta). Antes era
        // 48000 + nosso worklet fazia average-of-3 ingênuo (aliasing → reduz
        // accuracy do Whisper). Se browser não honrar 16000, worklet faz
        // fallback gracioso (codigo abaixo trata srcSr != targetSr).
        ac = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
        await ac.audioWorklet.addModule('/vendor/vpsm/audio-pcm-worklet.js');
        micSource = ac.createMediaStreamSource(stream);
        workletNode = new AudioWorkletNode(ac, 'vpsm-pcm16-worklet', { numberOfInputs: 1, numberOfOutputs: 0 });
        micSource.connect(workletNode);

        // WebSocket → backend Go → stt-proxy.
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = proto + '//' + location.host + '/ws/stt/transcribe?token=' + encodeURIComponent(opts.token);
        ws = new WebSocket(wsUrl);
        ws.binaryType = 'arraybuffer';

        // Queue PCM frames até WS abrir.
        const pending = [];
        let wsReady = false;

        workletNode.port.onmessage = (ev) => {
          if (stopped) return;
          // M26 BUG#7: mute-aware — se a primeira audio track está disabled
          // (user clicou mute), não envia PCM silencioso. Economiza ~30-50
          // frames/s de upload BW + bateria mobile. Server-side Silero VAD
          // já descartaria, mas eliminar na origem é sempre melhor.
          const audioTracks = stream && stream.getAudioTracks ? stream.getAudioTracks() : [];
          if (audioTracks.length && !audioTracks[0].enabled) return;
          const buf = ev.data; // ArrayBuffer
          if (wsReady && ws.readyState === WebSocket.OPEN) {
            try { ws.send(buf); } catch (_) {}
          } else {
            // Cap 100 frames (~8s) — antes era 40 (~3s) e perdia o início da
            // fala em conexões lentas onde WS ready demora. Trade-off: até 800KB
            // memória adicional pico, mas evita perder palavras iniciais.
            if (pending.length < 100) pending.push(buf);
          }
        };

        ws.onopen = () => {
          wsReady = true;
          console.log('[vpsm:stt] whisper-local WS aberto, enviando start...');
          ws.send(JSON.stringify({
            type: 'start',
            lang: (opts.lang || VPSMSTT.defaultLang() || 'pt').slice(0, 5),
            prompt: opts.prompt || '',
          }));
          // Drena queue.
          while (pending.length) { try { ws.send(pending.shift()); } catch (_) {} }
        };

        ws.onmessage = (ev) => {
          if (stopped) return;
          let msg;
          try { msg = JSON.parse(ev.data); } catch (_) { return; }
          if (msg.type === 'ready') {
            // SERVER_READY do WhisperLive = upstream aceitou de verdade.
            // Âncora FORTE do ack pro iniciador — diferente do ws.onopen, que só
            // diz que o bridge Go subiu (sem upstream confirmado, ack prematuro).
            // O caso "ready nunca chega" é coberto pelo timer connect-timeout +
            // backstop na 1ª partial/final (videocall startWithBackend).
            if (opts.onReady) opts.onReady();
          } else if (msg.type === 'partial') {
            // WhisperLive emite enquanto o segmento evolui (segmento ainda não
            // completed). Usado pra latência percebida ~400ms até começar a ver
            // o que foi dito; final vem 200-500ms depois.
            if (opts.onPartial) opts.onPartial({ text: msg.text || '', buffer });
          } else if (msg.type === 'final') {
            const txt = (msg.text || '').trim();
            if (!txt) return;
            buffer = (buffer ? buffer + ' ' : '') + txt;
            if (opts.onFinal) opts.onFinal({
              text: txt,
              buffer,
              startMs: msg.startMs || 0,
              endMs: msg.endMs || 0,
              lang: msg.lang || (opts.lang || 'pt'),
              words: msg.words || [],
              confidence: typeof msg.confidence === 'number' ? msg.confidence : 1.0,
            });
          } else if (msg.type === 'error') {
            // M23: whisper-failed e ws-error são RETRYABLE por design.
            // whisper.cpp retornar erro num batch (CPU contention, timeout,
            // VAD muito agressivo) NÃO deve matar a sessão. Só vira fatal
            // se acumular 3+ em janela de 30s — backend está doente nessa
            // hora. Cada falha individual: silencioso, continua mandando PCM.
            const trulyFatal = ['upstream-unavailable', 'upstream-init-failed', 'upstream-disconnect', 'crash', 'no-token', 'bad-handshake'].includes(msg.code);
            if (msg.code === 'whisper-failed') {
              const now = Date.now();
              if (!session._whisperFailHistory) session._whisperFailHistory = [];
              session._whisperFailHistory.push(now);
              // Mantém só falhas dos últimos 30s.
              while (session._whisperFailHistory.length && now - session._whisperFailHistory[0] > 30000) {
                session._whisperFailHistory.shift();
              }
              if (session._whisperFailHistory.length >= 3) {
                if (opts.onError) opts.onError({ code: 'whisper-failed', fatal: true, message: 'backend STT instável (3+ falhas em 30s)' });
              } else {
                // Silent: apenas log e continua. Não emite onError pra não
                // disparar loop-guard. UI mostra "stt:" badge ainda ativo.
                if (window.VPSM_DEBUG) console.warn('[vpsm:stt] whisper-failed (transient ' + session._whisperFailHistory.length + '/3)');
              }
            } else if (trulyFatal) {
              if (opts.onError) opts.onError({ code: msg.code, fatal: true, message: msg.message || msg.code });
            } else {
              if (opts.onError) opts.onError({ code: msg.code, fatal: false, message: msg.message || msg.code });
            }
          }
        };

        ws.onerror = (ev) => {
          console.warn('[vpsm:stt] whisper-local WS erro', ev);
          if (opts.onError) opts.onError({ code: 'ws-error', fatal: false, message: 'WebSocket erro' });
        };
        ws.onclose = (ev) => {
          if (stopped) return;
          console.log('[vpsm:stt] whisper-local WS fechado code=' + ev.code + ' reason=' + (ev.reason || '?') + ' wasClean=' + ev.wasClean);
          if (opts.onEnd) opts.onEnd({ reason: 'ws-closed', text: buffer, code: ev.code });
          cleanup();
        };

        VPSMSTT.setLastLang(opts.lang || VPSMSTT.defaultLang());
        session.stop = cleanup;
        session.driver = 'whisper-local';
        return session;
      } catch (e) {
        cleanup();
        if (opts.onError) opts.onError({ code: 'start-failed', fatal: true, message: e.message });
        if (opts.onEnd) opts.onEnd({ reason: 'start-failed', text: '' });
        return null;
      }
    },
  };

  // ---- Driver registry ------------------------------------------------------
  const drivers = { 'web-speech': webSpeechDriver, 'whisper-local': whisperLocalDriver };
  let currentBackend = 'web-speech';

  const VPSMSTT = {
    // True se ALGUM backend suportado funciona.
    isSupported() {
      const d = drivers[currentBackend];
      return !!(d && d.available());
    },
    // Lista backends disponíveis no browser atual.
    availableBackends() {
      return Object.keys(drivers).filter(n => drivers[n].available());
    },
    setBackend(name) {
      if (!drivers[name]) throw new Error('STT backend não registrado: ' + name);
      if (!drivers[name].available()) throw new Error('STT backend indisponível: ' + name);
      currentBackend = name;
    },
    registerBackend(name, driver) {
      drivers[name] = driver;
    },
    getLastLang,
    setLastLang,
    defaultLang,
    // start({ lang, continuous, interimResults, onPartial, onFinal, onError, onEnd, idleMs, token, stream, prompt })
    // Retorna handle com .stop(). Caller chama handle.stop() pra parar.
    // `token` é respeitado só pelo driver whisper-local; `stream`, pelos dois
    // (web-speech via start(track), onde o navegador suporta).
    start(opts) {
      opts = opts || {};
      const driverName = opts.backend || currentBackend;
      const d = drivers[driverName];
      if (!d || !d.available()) {
        if (opts.onError) opts.onError({ code: 'no-backend', fatal: true, message: 'Backend STT indisponível: ' + driverName });
        return null;
      }
      const session = { driver: driverName };
      return d.start(session, opts);
    },
    // Probe assíncrono pra verificar se whisper-local está vivo no servidor.
    // Frontend chama no boot pra decidir o default backend.
    async probeWhisperLocal() {
      try {
        const r = await fetch('/api/stt/health', { cache: 'no-store' });
        if (!r.ok) return false;
        const d = await r.json();
        return !!(d && d.ok && d.whisper);
      } catch (_) { return false; }
    },
  };

  window.VPSMSTT = VPSMSTT;
})();
