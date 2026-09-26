// PanelSTT: speech-to-text adapter with pluggable drivers (Web Speech, whisper-local).
// Output goes through callbacks (onPartial / onFinal / onError / onEnd).
//
// Web Speech quirks this wrapper handles:
//   1. It stops by itself after silence (~5s in Chrome), so it auto-restarts.
//   2. On mobile it needs a user gesture: start it directly from the click
//      handler, not from a setTimeout.
(function () {
  'use strict';
  const isBrowser = typeof window !== 'undefined';
  if (!isBrowser) return;

  const SR_AVAILABLE = !!(window.SpeechRecognition || window.webkitSpeechRecognition);
  const LS_KEY = 'panel_stt_lang';

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

      // With opts.stream, recognize THAT track (Chrome 135+ desktop,
      // SpeechRecognition.start(track)); otherwise Web Speech listens to the
      // system default mic, which may not be the call's mic. Where
      // start(track) throws (unsupported), fall back to plain start() for good.
      const srcTrack = opts.stream && opts.stream.getAudioTracks ? (opts.stream.getAudioTracks()[0] || null) : null;
      let useTrack = !!srcTrack;
      const startRec = () => {
        if (useTrack && srcTrack.readyState === 'live') {
          try { rec.start(srcTrack); return; }
          catch (e) {
            useTrack = false;
            console.warn('[panel:stt] web-speech does not support a track (' + e.name + '), using the default mic');
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
          // No partial for idleMs: stop even in continuous mode, otherwise the
          // mic stays open draining battery on low background noise.
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
        // Recoverable: no-speech, aborted, audio-capture (mic briefly busy).
        // Only the errors in `fatal` stop the session.
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
        // The API ends by itself; restart at most 5 times so a broken driver
        // cannot loop forever.
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

      // onstart means recognition is really listening: this, not the click,
      // is what acks the initiator.
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
  // → WhisperLive. Server sends {type:'partial',text} while a segment evolves
  // and {type:'final'} when it completes.
  // opts.token (JWT) is required. opts.stream is optional (defaults to the mic).
  const whisperLocalDriver = {
    name: 'whisper-local',
    available() {
      return typeof AudioWorklet !== 'undefined'
        && typeof MediaStream !== 'undefined'
        && typeof WebSocket !== 'undefined';
    },
    async start(session, opts) {
      if (!opts.token) {
        if (opts.onError) opts.onError({ code: 'no-token', fatal: true, message: 'JWT token required' });
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
        // Ask for 16kHz so the browser's low-pass resampler does the work
        // (naive decimation aliases and hurts Whisper accuracy). If the browser
        // ignores it, the worklet resamples itself.
        ac = new (window.AudioContext || window.webkitAudioContext)({ sampleRate: 16000 });
        await ac.audioWorklet.addModule('/vendor/panel/audio-pcm-worklet.js');
        micSource = ac.createMediaStreamSource(stream);
        workletNode = new AudioWorkletNode(ac, 'panel-pcm16-worklet', { numberOfInputs: 1, numberOfOutputs: 0 });
        micSource.connect(workletNode);

        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = proto + '//' + location.host + '/ws/stt/transcribe?token=' + encodeURIComponent(opts.token);
        ws = new WebSocket(wsUrl);
        ws.binaryType = 'arraybuffer';

        const pending = [];
        let wsReady = false;

        workletNode.port.onmessage = (ev) => {
          if (stopped) return;
          // Muted (first audio track disabled): send nothing, saving upload
          // bandwidth and mobile battery.
          const audioTracks = stream && stream.getAudioTracks ? stream.getAudioTracks() : [];
          if (audioTracks.length && !audioTracks[0].enabled) return;
          const buf = ev.data; // ArrayBuffer
          if (wsReady && ws.readyState === WebSocket.OPEN) {
            try { ws.send(buf); } catch (_) {}
          } else {
            // Queue up to 100 frames (~8s) until the WS opens, so slow
            // connections do not lose the first words.
            if (pending.length < 100) pending.push(buf);
          }
        };

        ws.onopen = () => {
          wsReady = true;
          console.log('[panel:stt] whisper-local WS open, sending start...');
          ws.send(JSON.stringify({
            type: 'start',
            lang: (opts.lang || PanelSTT.defaultLang() || 'pt').slice(0, 5),
            prompt: opts.prompt || '',
          }));
          while (pending.length) { try { ws.send(pending.shift()); } catch (_) {} }
        };

        ws.onmessage = (ev) => {
          if (stopped) return;
          let msg;
          try { msg = JSON.parse(ev.data); } catch (_) { return; }
          if (msg.type === 'ready') {
            // 'ready' means the WhisperLive upstream really accepted, so it acks
            // the initiator (ws.onopen only means the Go bridge is up). A
            // missing 'ready' is covered by the caller's connect timeout.
            if (opts.onReady) opts.onReady();
          } else if (msg.type === 'partial') {
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
            // whisper-failed is transient by design (one bad batch must not kill
            // the session); it only becomes fatal after 3+ failures in 30s.
            const trulyFatal = ['upstream-unavailable', 'upstream-init-failed', 'upstream-disconnect', 'crash', 'no-token', 'bad-handshake'].includes(msg.code);
            if (msg.code === 'whisper-failed') {
              const now = Date.now();
              if (!session._whisperFailHistory) session._whisperFailHistory = [];
              session._whisperFailHistory.push(now);
              while (session._whisperFailHistory.length && now - session._whisperFailHistory[0] > 30000) {
                session._whisperFailHistory.shift();
              }
              if (session._whisperFailHistory.length >= 3) {
                if (opts.onError) opts.onError({ code: 'whisper-failed', fatal: true, message: 'unstable STT backend (3+ failures in 30s)' });
              } else {
                // No onError here, so the caller's loop guard is not triggered.
                if (window.PANEL_DEBUG) console.warn('[panel:stt] whisper-failed (transient ' + session._whisperFailHistory.length + '/3)');
              }
            } else if (trulyFatal) {
              if (opts.onError) opts.onError({ code: msg.code, fatal: true, message: msg.message || msg.code });
            } else {
              if (opts.onError) opts.onError({ code: msg.code, fatal: false, message: msg.message || msg.code });
            }
          }
        };

        ws.onerror = (ev) => {
          console.warn('[panel:stt] whisper-local WS error', ev);
          if (opts.onError) opts.onError({ code: 'ws-error', fatal: false, message: 'WebSocket error' });
        };
        ws.onclose = (ev) => {
          if (stopped) return;
          console.log('[panel:stt] whisper-local WS closed code=' + ev.code + ' reason=' + (ev.reason || '?') + ' wasClean=' + ev.wasClean);
          if (opts.onEnd) opts.onEnd({ reason: 'ws-closed', text: buffer, code: ev.code });
          cleanup();
        };

        PanelSTT.setLastLang(opts.lang || PanelSTT.defaultLang());
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

  const PanelSTT = {
    isSupported() {
      const d = drivers[currentBackend];
      return !!(d && d.available());
    },
    availableBackends() {
      return Object.keys(drivers).filter(n => drivers[n].available());
    },
    setBackend(name) {
      if (!drivers[name]) throw new Error('STT backend not registered: ' + name);
      if (!drivers[name].available()) throw new Error('STT backend unavailable: ' + name);
      currentBackend = name;
    },
    registerBackend(name, driver) {
      drivers[name] = driver;
    },
    getLastLang,
    setLastLang,
    defaultLang,
    // start({ lang, continuous, interimResults, onPartial, onFinal, onError, onEnd, idleMs, token, stream, prompt })
    // Returns a handle with .stop(). `token` is used only by whisper-local;
    // `stream` by both (web-speech via start(track), where supported).
    start(opts) {
      opts = opts || {};
      const driverName = opts.backend || currentBackend;
      const d = drivers[driverName];
      if (!d || !d.available()) {
        if (opts.onError) opts.onError({ code: 'no-backend', fatal: true, message: 'STT backend unavailable: ' + driverName });
        return null;
      }
      const session = { driver: driverName };
      return d.start(session, opts);
    },
    // Checks whether the server's whisper-local backend is alive (used at boot
    // to pick the default backend).
    async probeWhisperLocal() {
      try {
        const r = await fetch('/api/stt/health', { cache: 'no-store' });
        if (!r.ok) return false;
        const d = await r.json();
        return !!(d && d.ok && d.whisper);
      } catch (_) { return false; }
    },
  };

  window.PanelSTT = PanelSTT;
})();
