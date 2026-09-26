/* panel-videocall — WebRTC P2P client integrated with server-control-panel signaling.
 *
 * Exposed as: window.PanelVideoCall = { connect(opts), disconnect() }
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
 *     [Recording UI is phase 2; the API is here for the next phase.]
 *
 * Browser compat: Chrome/Edge 88+, Firefox 91+, Safari 15+. AV1 falls back
 * automatically when both peers don't support it.
 */
(function () {
  'use strict';

  if (window.PanelVideoCall) return; // idempotent load

  // ---- constants ------------------------------------------------------

  const CODEC_PREF = ['video/AV1', 'video/VP9', 'video/H264', 'video/VP8'];
  const DEFAULT_BUDGET_KBPS = 500; // medium-mode default; user can override
  const STATS_INTERVAL_MS = 1000;
  const RECONNECT_BACKOFF_MIN = 1000;
  const RECONNECT_BACKOFF_MAX = 30000;
  // Quality presets — knob for the user's "Economy / Medium / High" buttons.
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
  // Phone/Minimum/Economy enable the Opus tweak (usedtx + maxaveragebitrate +
  // useinbandfec=0); Medium/High keep FEC on for resilience on unstable networks.
  // Phone sets videoOff: Call.start skips video in getUserMedia, and
  // applyQualityProfile drops the video sender when switching to it mid-call.
  const QUALITY_MODES = {
    phone:   { videoOff: true,  width: 0,    height: 0,   fps: 0,  videoKbps: 0,    audioKbps: 16, opusTweak: true,  label: 'Phone'    },
    low:     { videoOff: false, width: 160,  height: 90,  fps: 8,  videoKbps: 25,   audioKbps: 16, opusTweak: true,  label: 'Minimum'  },
    economy: { videoOff: false, width: 320,  height: 180, fps: 15, videoKbps: 60,   audioKbps: 20, opusTweak: true,  label: 'Economy'  },
    medium:  { videoOff: false, width: 640,  height: 360, fps: 24, videoKbps: 500,  audioKbps: 32, opusTweak: false, label: 'Medium'   },
    high:    { videoOff: false, width: 1280, height: 720, fps: 30, videoKbps: 2000, audioKbps: 48, opusTweak: false, label: 'High'     },
  };
  // Absolute floor for the budget slider — below this, even audio Opus DTX
  // doesn't reconstruct cleanly. Above it, the network stack can do its
  // adaptive thing. The 6 Mbps ceiling is enough for 1080p AV1.
  const BUDGET_FLOOR_KBPS = 8;
  const BUDGET_CEILING_KBPS = 6000;

  // tweakOpusSdp munges the SDP to force Opus into an ultra-economical mode:
  //   - usedtx=1            : discontinuous transmission (nothing sent during silence).
  //   - useinbandfec=0      : disables FEC; less resilience, ~20% less bandwidth.
  //   - maxaveragebitrate=N : hard cap; without it Opus sometimes ignores the
  //                           RTCRtpSender maxBitrate.
  //   - cbr=0; stereo=0     : VBR mono (voice); stereo would double the bandwidth.
  // Applied before setLocalDescription. Only touches the Opus fmtp, never video.
  function tweakOpusSdp(sdp, audioKbps) {
    if (!sdp) return sdp;
    const lines = sdp.split(/\r?\n/);
    // The Opus payload type varies between browsers and sessions.
    let opusPt = null;
    for (const l of lines) {
      const m = l.match(/^a=rtpmap:(\d+)\s+opus\/48000/i);
      if (m) { opusPt = m[1]; break; }
    }
    if (!opusPt) return sdp;
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
    // No fmtp line: inject one right after the Opus rtpmap.
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

  /** Broadcast a `state` signaling message to all peers in the room (via WS),
      e.g. the owner forcing a quality mode on everyone. */
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
    // Before permission is granted deviceId is "", and duplicate empty keys
    // crash the whole Alpine x-for. Synthesize a stable id and dedup by it.
    const sanitize = (arr) => {
      const seen = new Set();
      const out = [];
      for (let i = 0; i < arr.length; i++) {
        const d = arr[i];
        if (!d) continue;
        const id = d.deviceId || ('synth-' + d.kind + '-' + i);
        if (seen.has(id)) continue;
        seen.add(id);
        // Plain-object wrapper: MediaDeviceInfo is read-only.
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
  // Turns a getUserMedia DOMException into an actionable sentence.
  function humanizeGumError(e, kindLabel) {
    const name = e && e.name;
    const target = kindLabel || 'camera/microphone';
    if (name === 'NotAllowedError' || name === 'PermissionDeniedError') {
      return 'Access to ' + target + ' is blocked. Click the 🔒 icon next to the URL, allow it, then reload the page.';
    }
    if (name === 'NotFoundError' || name === 'DevicesNotFoundError') {
      return 'No ' + target + ' was found on this computer.';
    }
    if (name === 'NotReadableError' || name === 'TrackStartError') {
      return target.charAt(0).toUpperCase() + target.slice(1) + ' is already in use by another program (Zoom/Meet/OBS/a system app). Close the others and try again.';
    }
    if (name === 'OverconstrainedError') {
      return 'The saved ' + target + ' device no longer exists (was it disconnected?). Pick another one from the list.';
    }
    if (name === 'SecurityError') {
      return 'Blocked by security policy: the site must be served over HTTPS.';
    }
    if (name === 'AbortError') {
      return 'The ' + target + ' permission request was cancelled or interrupted.';
    }
    return (e && e.message) || ('Unknown error while accessing the ' + target + '.');
  }
  // Probes ONE kind in isolation: getUserMedia is all-or-nothing, so asking
  // for {audio,video} with only a mic present fails as if nothing existed.
  async function probeKind(kind) {
    try {
      const s = await navigator.mediaDevices.getUserMedia(
        kind === 'audio' ? { audio: true } : { video: true });
      s.getTracks().forEach(t => t.stop());
      return { ok: true };
    } catch (e) {
      return { ok: false, name: e && e.name, error: humanizeGumError(e, kind === 'audio' ? 'microphone' : 'camera') };
    }
  }
  // Returns { ok, audio, video, error, detail }. `ok` means "can join with
  // SOMETHING", not "everything works"; block joining only when both are missing.
  async function probeDevicePermission() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return { ok: false, audio: false, video: false, error: 'Browser does not support getUserMedia (HTTPS required).' };
    }
    // Check permission state BEFORE prompting: if both are already denied,
    // retrying is pointless and the fix is the padlock settings.
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
            error: 'Camera AND microphone access were DENIED for this site. Click the padlock 🔒 (or ⓘ) icon in the address bar, find Camera/Microphone and switch them to "Allow". Then reload the page.',
            permState: { camera: 'denied', microphone: 'denied' },
          };
        }
      }
    } catch (_) {}
    try {
      const s = await navigator.mediaDevices.getUserMedia({ audio: true, video: true });
      s.getTracks().forEach(t => t.stop());
      return { ok: true, audio: true, video: true };
    } catch (_) {
      // Combined request failed; one missing device is enough for that, so
      // probe each kind to find out which side works.
    }
    // Always probe for real: Chromium reports 'denied' for a camera that
    // simply does not exist, so permissions.query cannot tell the reason.
    const [a, v] = await Promise.all([probeKind('audio'), probeKind('video')]);
    if (a.ok && v.ok) return { ok: true, audio: true, video: true };
    let error;
    if (!a.ok && !v.ok) {
      error = a.error === v.error ? a.error : (a.error + ' ' + v.error);
    } else if (a.ok) {
      error = v.error + ' You can still join, audio only.';
    } else {
      error = a.error + ' You can still join, but nobody will hear you.';
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
  // Outside a call there is nothing to apply: the shell stores the preference
  // and it takes effect on the next connect().
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
  // Test tone routed through a given sinkId — used by the lobby "Test"
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
  // Used by the lobby to show a live VU meter. opts.gain (number or function)
  // and opts.processing reproduce the call pipeline (gain + limiter + processing)
  // so the meter shows what the other side will hear, not the raw mic.
  // onLevel(level 0..100, {limiting}).
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

  /** Changes the STT language in place without broadcasting off/on. */
  function changeSubtitlesLang(lang) {
    if (!active) return false;
    return active.changeSubtitlesLang(lang);
  }

  window.PanelVideoCall = {
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
      // Web Speech (native Chrome/Edge) OR local whisper (PanelSTT bridge to
      // WhisperLive), so Firefox/Safari also get subtitles when the backend exists.
      const webSpeech = !!(window.SpeechRecognition || window.webkitSpeechRecognition);
      const whisperLocal = !!(window.PanelSTT && typeof window.PanelSTT.connect === 'function');
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
      return !!(window.PanelVideoCallE2EE && window.PanelVideoCallE2EE.isSupported());
    },
    sendState: sendState,
  };

  // Send gain range. 4x (+12 dB) is safe only because the pipeline limiter
  // holds the peaks; without it, voice clipped above ~2x.
  const MIC_GAIN_MIN = 0.25;
  const MIC_GAIN_MAX = 4.0;
  function clampMicGain(v) {
    const n = Number(v);
    if (!isFinite(n) || n <= 0) return 1.0;
    return Math.min(MIC_GAIN_MAX, Math.max(MIC_GAIN_MIN, n));
  }
  // Browser capture processing, all on by default. Disabling echo cancellation
  // without headphones makes the other side hear itself (the UI warns).
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
  // Meter level: RMS in dBFS mapped to 0..100 (-60 dB .. 0 dB), a scale that
  // follows the ear. `limiting` = the limiter is holding back more than 4 dB,
  // i.e. the gain is too high for this microphone.
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
  // Limiter: DynamicsCompressor at -3 dBFS, ratio 20. The Web Audio node applies
  // an AUTOMATIC makeup gain of (1/full_scale_gain)^0.6 (+1.71 dB here) to every
  // signal, so we compensate it. `comp` is exposed so the meter can read `reduction`.
  const MIC_LIM_THRESHOLD = -3;
  const MIC_LIM_RATIO = 20;
  function makeMicLimiter(ctx) {
    const comp = ctx.createDynamicsCompressor();
    comp.threshold.value = MIC_LIM_THRESHOLD;
    comp.knee.value = 0;
    comp.ratio.value = MIC_LIM_RATIO;
    comp.attack.value = 0.002;
    comp.release.value = 0.12;
    const fullScaleDb = MIC_LIM_THRESHOLD - MIC_LIM_THRESHOLD / MIC_LIM_RATIO;
    const makeupDb = -0.6 * fullScaleDb;
    const compensate = ctx.createGain();
    compensate.gain.value = Math.pow(10, -makeupDb / 20);
    comp.connect(compensate);
    return { input: comp, output: compensate, comp: comp };
  }

  // ---- Call -----------------------------------------------------------

  function Call(opts) {
    this.opts = opts || {};
    this.roomId = opts.roomId;
    this.token = opts.token;
    // ticketProvider: async () => string. When set, the signaling WS uses
    // ?ticket=<X> instead of ?token=<JWT>, keeping the JWT out of access logs.
    this.ticketProvider = opts.ticketProvider || null;
    // Stable client identity so a reconnect evicts our own ghost. Authenticated:
    // uuid in sessionStorage (per tab, per room); guests: empty (the server
    // derives it from the token jti).
    this.clientId = this._resolveClientId();
    this.displayName = opts.displayName || 'You';
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
    // audioOnly: from opts (legacy) OR the preset's videoOff; skips the camera.
    this.audioOnly   = !!opts.audioOnly || !!preset.videoOff;
    // micOff: join without a microphone (video only), counterpart of audioOnly.
    this.micOff      = !!opts.micOff;
    // Mic send gain (1.0 = neutral) and browser processing, from the shell's
    // saved preference, so the slider value is actually applied.
    this._micGain    = clampMicGain(opts.micGain);
    this._micProc    = normMicProc(opts.micProcessing);
    // opusTweak: DTX/FEC-off/maxaveragebitrate via SDP munging in PeerConn.
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
      console.error('[panel:vc] ERROR:', msg);
      // Maps raw legacy server errors to friendly messages.
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
    // Wake Lock keeps the screen on during the call (critical on mobile);
    // re-acquired on visibilitychange when back in the foreground.
    (async () => {
      try {
        if (navigator.wakeLock && !this._wakeLock) {
          this._wakeLock = await navigator.wakeLock.request('screen');
          this._wakeLock.addEventListener('release', () => { this._wakeLock = null; });
        }
      } catch (e) { console.warn('[panel:vc] wakeLock failed: ' + e.message); }
    })();
    this._wakeLockVisListener = async () => {
      if (document.visibilityState === 'visible' && !this._wakeLock && !this.stopped) {
        try { this._wakeLock = await navigator.wakeLock.request('screen'); } catch (_) {}
      }
    };
    document.addEventListener('visibilitychange', this._wakeLockVisListener);
    // 1. Local media (mic + maybe camera). Honor the lobby's device picks
    //    when they're not "default".
    // micOff: the lobby already found no usable mic; requesting audio anyway
    // would fail the whole getUserMedia and take video down with it.
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
    // getUserMedia is all-or-nothing: one missing kind fails the WHOLE request.
    // Try a degrading plan and join with whatever exists. This lives here because
    // every entry path (lobby, skip-lobby, recovery, guest) goes through start().
    const plan = [];
    const noId = (c) => {
      if (!c || typeof c !== 'object') return c;
      const cp = Object.assign({}, c);
      delete cp.deviceId; delete cp.facingMode;
      return cp;
    };
    const step = (a, v, note) => { if (a || v) plan.push({ audio: a, video: v, note: note }); };
    step(audio, video, '');
    const hasFixedId = (audio && audio.deviceId) || (video && (video.deviceId || video.facingMode));
    if (hasFixedId) step(noId(audio), noId(video), 'the saved device no longer exists, joined with the system default');
    if (audio && video) {
      step(noId(audio), false, 'no camera available, joined audio only');
      step(false, noId(video), 'no microphone available, you joined but nobody will hear you');
    }
    let lastError = null;
    this.degradedNote = '';
    for (const p of plan) {
      try {
        this.localStream = await navigator.mediaDevices.getUserMedia({ audio: p.audio, video: p.video });
        this.degradedNote = p.note || '';
        break;
      } catch (e) { lastError = e; }
    }
    if (!this.localStream) {
      // Empty plan = audioOnly AND micOff (nothing to request).
      throw new Error(lastError
        ? humanizeGumError(lastError)
        : 'No usable camera or microphone on this computer. Connect a device and try again.');
    }
    this.cameraTrack = this.localStream.getVideoTracks()[0] || null;
    // audioOnly must reflect what ACTUALLY came back, or the rest of the engine
    // operates on a null camera track.
    if (!this.cameraTrack) this.audioOnly = true;
    if (this.degradedNote) {
      try { this.cbState({ type: 'devices-degraded', note: this.degradedNote }); } catch (_) {}
    }
    this.callStartedAt = Date.now();

    // raw mic -> GainNode -> limiter -> destination -> SENT track. The sent track
    // stays stable for the whole call: switching mic or processing only rewires
    // the source (_attachMicSource), with no replaceTrack on peers. The raw track
    // (_micGainRawTrack) is what transcription reads (getMicStreamForSTT).
    try {
      this._buildMicPipeline(this.localStream.getAudioTracks()[0]);
    } catch (e) {
      console.warn('[panel:vc] mic gain pipeline failed (no gain control): ' + e.message);
    }

    this.attachLocalPreview();

    this._startLocalSpeakingMonitor();

    // 2. Signaling WS. The promise resolves when we receive `joined`.
    await this.openSignaling();

    // 3. Stats loop.
    this.statsTimer = setInterval(() => this.collectStats(), STATS_INTERVAL_MS);

    // 4. Network listeners for ICE restart. Keep the handler ref so Call.stop
    // can remove it; otherwise reconnects pile up listeners and cascade ICE restarts.
    if (!this.networkListenerInstalled) {
      this.networkListenerInstalled = true;
      this._netHandler = () => this.handleNetworkChange();
      window.addEventListener('online', this._netHandler);
      if (navigator.connection && navigator.connection.addEventListener) {
        navigator.connection.addEventListener('change', this._netHandler);
      }
    }

    // 5. Background keepalive via Web Worker. Browsers throttle timers in hidden
    // tabs, freezing the app ping and reconnect so proxies close the WS as idle.
    // Workers are NOT throttled.
    this._startBgKeepalive();

    // 6. On returning to the tab, health-check immediately and reconnect if the
    // connection died in the background. Guarded against double installation.
    if (this._visHandler) {
      try { document.removeEventListener('visibilitychange', this._visHandler); } catch (_) {}
    }
    this._visHandler = () => {
      if (document.visibilityState === 'visible' && !this.stopped) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
          // Silent-death detection: >70s without any server message (pong takes
          // ~25s) means a middlebox probably dropped us. Reopen BEFORE the
          // browser notices via onclose (which can take 100s+).
          if (this._lastWsMessageAt && Date.now() - this._lastWsMessageAt > 70000) {
            console.warn('[panel:vc] WS silent >70s, forcing reconnect');
            try { this.ws.close(4000, 'silent-death'); } catch (_) {}
            this.reopenSignaling();
          }
        } else if (this.ws && this.ws.readyState >= WebSocket.CLOSING) {
          this.reopenSignaling();
        }
        // Health-check peers, debounced to one restartIce per peer per 15s;
        // quick tab switching otherwise cascades restarts and jams signaling.
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

  // Inline worker that ticks outside background-tab throttling.
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
        // App ping: the server's 'pong' resets its pong deadline and any
        // middlebox (Cloudflare/Traefik/NAT) idle counter.
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
        }
        // Run a reconnect that got stuck in a throttled setTimeout.
        if (this._pendingReconnect && !this.stopped) {
          this._pendingReconnect = false;
          this.reopenSignaling();
        }
      };
      // 20s: well inside common idle timeouts (Cloudflare 100s, Traefik 60s).
      this._bgWorker.postMessage({ cmd: 'start', interval: 20000 });
    } catch (_) { /* no Worker: degrade gracefully */ }
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
    if (this._wakeLockVisListener) {
      try { document.removeEventListener('visibilitychange', this._wakeLockVisListener); } catch (_) {}
      this._wakeLockVisListener = null;
    }
    if (this._wakeLock) {
      try { this._wakeLock.release(); } catch (_) {}
      this._wakeLock = null;
    }
    // Null the handlers BEFORE stop() so ondataavailable cannot keep pushing
    // chunks into an orphaned closure.
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
    // Must stop the muted-speaking detector: its track clone + AudioContext keep
    // the mic "in use" and the next call fails with "device busy".
    this._stopLocalSpeakingMonitor();
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
    if (this.frostActive) {
      this.frostActive = false;
      if (this.frostRAF) { try { cancelAnimationFrame(this.frostRAF); } catch (_) {} this.frostRAF = 0; }
      this.frostCanvas = null;
      if (this.frostVideoEl) { try { this.frostVideoEl.srcObject = null; } catch (_) {} this.frostVideoEl = null; }
    }
    // Stop subtitles, or the local-whisper WS + AudioContext leak and the next
    // call gets NotReadableError (mic still "in use").
    if (this._subtitlesActive) {
      this._subtitlesActive = false;
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      if (this._captionTrailingTimer) { clearTimeout(this._captionTrailingTimer); this._captionTrailingTimer = null; }
      this._captionTrailingPayload = null;
      if (this._localCapClearTimer) { clearTimeout(this._localCapClearTimer); this._localCapClearTimer = null; }
      try { this.updatePeerCaption('me', '', true); } catch (_) {}
    }
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
    if (this._peerAudioMonitors) {
      for (const id in this._peerAudioMonitors) {
        const m = this._peerAudioMonitors[id];
        if (m && m.raf) { try { cancelAnimationFrame(m.raf); } catch (_) {} }
        if (m && m.ctx && m.ctx.state !== "closed") { try { m.ctx.close().catch(()=>{}); } catch (_) {} }
      }
      this._peerAudioMonitors = {};
    }
    // Close any preserved PiP window BEFORE clearing the grid; innerHTML=''
    // would otherwise orphan the OS window and the pending timer.
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

    // POST final session totals so the dashboard history knows about it.
    // Fire-and-forget; no auth header here because the existing JWT cookie
    // covers the request (same-origin POST).
    const durationS = this.callStartedAt ? Math.round((Date.now() - this.callStartedAt) / 1000) : 0;
    // Guests cannot access /api/videocall/sessions (logged-in users only).
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

  // Stable client identity for authenticated users, in sessionStorage keyed by
  // room: survives WS reconnects but NOT shared across tabs (each tab is a
  // distinct participant, preserving the server's two-tab guard). Guests get ''
  // (the server derives it from the token jti). Ephemeral if storage is unavailable.
  Call.prototype._resolveClientId = function () {
    if (this.opts.guestMode) return '';
    const key = 'panel:vc:cid:' + this.roomId;
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
      // Guest mode (PIN entry): the guest token embeds the room, so no room_id
      // in the query (the server would reject a mismatch).
      const wsPath = this.opts.guestMode ? '/ws/videocall-guest' : '/ws/videocall';
      const roomParam = this.opts.guestMode ? '' : ('&room_id=' + encodeURIComponent(this.roomId));
      // ticketProvider preferred (mobile cookie-auth flow); fallback token.
      // Guest mode ALWAYS uses the guest JWT (guests have no ws-ticket).
      let authParam = '';
      if (!this.opts.guestMode && this.ticketProvider) {
        try {
          const ticket = await this.ticketProvider();
          if (ticket) authParam = '?ticket=' + encodeURIComponent(ticket);
        } catch (_) {}
      }
      if (!authParam) authParam = '?token=' + encodeURIComponent(this.token);
      // client_id for authenticated users only; guests omit it (server uses jti).
      const clientParam = (!this.opts.guestMode && this.clientId)
        ? ('&client_id=' + encodeURIComponent(this.clientId)) : '';
      // `resume=1` tells the server this WS REOPENS an ongoing call, so the
      // rejoin does not ring the other devices. The flag can only SILENCE:
      // the server uses it to suppress ringing, never to trigger it.
      const resumeParam = this._resuming ? '&resume=1' : '';
      const url = proto + '//' + location.host + wsPath + authParam + roomParam + clientParam + resumeParam;
      // Log client_id on every (re)connect: a changing value (or 'none')
      // explains a ghost the server-side eviction could not match.
      console.log('[panel:vc] WS connecting guest=' + (!!this.opts.guestMode) +
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
        // Last-message timestamp for the silent-death watchdog.
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
        // Reconnect with backoff, capped at 8 attempts, then 'reconnect-gave-up';
        // otherwise guests with an expired token loop forever.
        this._wsRetries = (this._wsRetries || 0) + 1;
        if (this._wsRetries > 8) {
          this.cbState({ type: 'reconnect-gave-up' });
          return;
        }
        this.cbState({ type: 'reconnecting' });
        // setTimeout is throttled in background tabs; mark pending so the
        // (unthrottled) worker tick can also trigger the reopen.
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

  // Tears down ONE peer audio monitor (AudioContext + analyser + RAF).
  Call.prototype._teardownPeerMonitor = function (id) {
    if (!this._peerAudioMonitors || !this._peerAudioMonitors[id]) return;
    const m = this._peerAudioMonitors[id];
    try { if (m.raf) cancelAnimationFrame(m.raf); } catch (_) {}
    try { if (m.ctx && m.ctx.state !== 'closed') m.ctx.close().catch(() => {}); } catch (_) {}
    delete this._peerAudioMonitors[id];
  };

  Call.prototype.reopenSignaling = function () {
    // Mid-call reconnect that SURVIVES a server restart: media is P2P/TURN and
    // does not go through signaling, so `connected` PCs stay up (ICE consent is
    // peer-to-peer, RFC 7675). We PRESERVE them and ADOPT them under the new
    // peer id once the WS reopens (re-key via stable ClientID in ensurePeer ->
    // _rekeyPeer). Only non-`connected` PCs are closed and recreated from the
    // snapshot; a monitor is torn down only together with the PC it observes.
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
    console.log('[panel:vc] reopen: preserved=' + preserved + ' dropped=' + dropped);
    // While this cycle lasts, the WS carries resume=1 (see openSignaling).
    this._resuming = true;
    this.openSignaling()
      .then(() => this.cbState({ type: 'reconnected' }))
      .catch(err => this.cbError('reconnect: ' + err.message))
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
      // Refresh TURN credentials at 0.8 * TTL so calls longer than the TTL
      // keep TURN (critical behind CGNAT).
      const ttlSec = (payload.turn.ttl > 0) ? payload.turn.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } else {
      this.iceServers = [{ urls: ['stun:stun.l.google.com:19302'] }];
    }
    // payload.peers is the authoritative room roster. A `peer-left` missed while
    // the socket was down would leave a ghost tile, so drop local peers that are
    // NOT in the snapshot (except ourselves).
    const live = new Set((payload.peers || []).map(p => p.id));
    for (const id in this.peers) {
      if (id === this.peerId || live.has(id)) continue;
      // Do NOT close a `connected` PC here: it is a restart survivor under its
      // OLD id, awaiting adoption in the ensurePeer loop below (the snapshot lists
      // it under the NEW id). connected = adopt; any other state = real ghost.
      const peer = this.peers[id];
      const cs = peer && peer.pc && peer.pc.connectionState;
      if (cs === 'connected') {
        console.log('[panel:vc] reconcile: preserving ' + id.slice(-6) + ' (connected, awaiting adoption)');
        continue;
      }
      console.log('[panel:vc] reconcile: removing ghost peer ' + id.slice(-6) + ' (missing from snapshot)');
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
      // setConfiguration does not force an ICE restart; new credentials apply
      // to future candidates.
      for (const id in this.peers) {
        try { this.peers[id].pc.setConfiguration({ iceServers: this.iceServers }); }
        catch (_) {}
      }
      this._turnRetries = 0;
      console.log('[panel:vc] TURN credentials refreshed (peers=' + Object.keys(this.peers).length + ')');
      const ttlSec = (tu.ttl > 0) ? tu.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } catch (e) {
      // Exponential backoff, capped at 10 min and 5 attempts.
      this._turnRetries = (this._turnRetries || 0) + 1;
      if (this._turnRetries <= 5) {
        const backoff = Math.min(60 * Math.pow(2, this._turnRetries - 1), 600);
        console.warn('[panel:vc] TURN refresh failed (' + this._turnRetries + '/5): ' + e.message + ', retrying in ' + backoff + 's');
        this._scheduleTURNRefresh(backoff);
      } else {
        console.error('[panel:vc] TURN refresh exhausted after 5 attempts');
        try { this.cbState({ type: 'turn-exhausted' }); } catch (_) {}
        // One more try in 10 min, in case the network recovers.
        this._turnRetries = 0;
        this._scheduleTURNRefresh(600);
      }
    }
  };

  Call.prototype.handleSignal = function (msg) {
    if (msg.type !== 'ice' && msg.type !== 'pong') {
      console.log('[panel:vc] WS recv type=' + msg.type + ' from=' + (msg.from ? msg.from.slice(-6) : '-'));
    }
    switch (msg.type) {
      case 'peer-joined':
        if (msg.from && msg.from !== this.peerId) {
          console.log('[panel:vc] peer-joined ' + msg.from.slice(-6) + ' — preparing PeerConn (as answerer)');
          const info = safeParse(msg.payload) || {};
          this.ensurePeer(msg.from, info.user || 'Peer', /*initiator=*/false, info.client_id);
        }
        break;
      case 'peer-left':
        console.log('[panel:vc] peer-left ' + (msg.from ? msg.from.slice(-6) : '-'));
        if (this.peers[msg.from]) {
          this.peers[msg.from].close();
          delete this.peers[msg.from];
          this._notifyPeerCount();
        }
        // Safety net: sweep orphan tiles (e.g. a "no camera" placeholder).
        this._pruneOrphanTiles();
        // Without this, each departing peer leaks an AudioContext + RAF.
        this._teardownPeerMonitor(msg.from);
        if (this._captionClearTimers && this._captionClearTimers[msg.from]) {
          clearTimeout(this._captionClearTimers[msg.from]);
          delete this._captionClearTimers[msg.from];
        }
        break;
      case 'reset':
        // The other side detected a stuck connection and is rebuilding its end.
        if (msg.from && this.peers[msg.from]) this._rebuildPeer(msg.from, 'requested by the other side', false);
        break;
      case 'offer':
      case 'answer':
      case 'ice': {
        const peer = this.peers[msg.from];
        if (!peer) {
          console.warn('[panel:vc] signal ' + msg.type + ' from unknown peer ' + (msg.from ? msg.from.slice(-6) : '-'));
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
      // Owner actions (the server validated the sender is the owner); applied
      // locally when we are the target.
      case 'owner-mute': {
        if (msg.to === this.peerId) {
          this._applyOwnerMute(true, msg.from);
        }
        this.cbState({ type: 'owner-action', action: 'mute', target: msg.to, by: msg.from });
        break;
      }
      case 'owner-unmute': {
        if (msg.to === this.peerId) {
          // Privacy: the owner can only ASK to unmute, never force the mic open.
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
          this._userInitiatedHangup = true;
          this.cbError('You were removed from the call by the owner.');
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
        // The server sends specific types with ready-to-show messages in
        // msg.error; the type lets the UI tell "room full" from "account in use".
        this._userInitiatedHangup = true; // no reopenSignaling after fatal join errors
        this.cbError(msg.error || translateLegacyError(msg.error));
        if (msg.type === 'error-full') {
          this.cbState({ type: 'room-full' });
        } else if (msg.type === 'error-conflict') {
          this.cbState({ type: 'user-conflict' });
        }
        break;
      case 'kicked':
        // Tell the UI BEFORE the WS closes, so the user sees the reason
        // instead of reconnect-gave-up.
        this._userInitiatedHangup = true; // no reopenSignaling
        this.cbState({ type: 'kicked', reason: msg.error || 'Removed from the call' });
        break;
    }
  };

  // -- Peers -----------------------------------------------------------

  // Re-keys (adopts) a LIVE PeerConn from its OLD id to the NEW id assigned
  // after reconnect. Migrates EVERYTHING keyed by id in the SAME synchronous
  // tick so no consumer sees intermediate state.
  Call.prototype._rekeyPeer = function (oldId, newId) {
    if (oldId === newId) return;
    const peer = this.peers[oldId];
    if (!peer) return;
    // (0) Critical: all PC handlers read this.remoteId at fire time, so
    //     rerouting here is atomic.
    delete this.peers[oldId];
    peer.remoteId = newId;
    this.peers[newId] = peer;
    // (a) The tile's id-keyed DOM attributes.
    if (this.videosEl) {
      const attrs = ['data-vc-peer-tile', 'data-vc-peer-avatar', 'data-vc-peer-name',
                     'data-vc-peer-sub', 'data-vc-peer', 'data-vc-peer-caption'];
      for (let i = 0; i < attrs.length; i++) {
        const el = this.videosEl.querySelector('[' + attrs[i] + '="' + oldId + '"]');
        if (el) el.setAttribute(attrs[i], newId);
      }
    }
    // (b) Audio monitor: its RAF re-reads _peerAudioMonitors[remoteId] lazily,
    //     so moving it is enough.
    if (this._peerAudioMonitors && this._peerAudioMonitors[oldId]) {
      const mon = this._peerAudioMonitors[oldId];
      mon.peerId = newId;
      this._peerAudioMonitors[newId] = mon;
      delete this._peerAudioMonitors[oldId];
    }
    // (c) _captionClearTimers (currently never written; migrated for safety).
    if (this._captionClearTimers && this._captionClearTimers[oldId]) {
      this._captionClearTimers[newId] = this._captionClearTimers[oldId];
      delete this._captionClearTimers[oldId];
    }
    // (d) Load-bearing: _activeSpeaker drives the CSS ring via data-vc-peer, and
    //     _sendSubsStatus looks up this.peers[_subtitlesRequestedById].
    if (this._activeSpeaker === oldId) this._activeSpeaker = newId;
    if (this._subtitlesRequestedById === oldId) this._subtitlesRequestedById = newId;
    // (e) _fileTxOffsets[*].peerID: cosmetic (never read), kept consistent.
    if (this._fileTxOffsets) {
      for (const fid in this._fileTxOffsets) {
        const o = this._fileTxOffsets[fid];
        if (o && o.peerID === oldId) o.peerID = newId;
      }
    }
  };

  Call.prototype.ensurePeer = function (remoteId, user, initiator, clientId) {
    // Idempotent: also absorbs a second call for an already adopted peer
    // (snapshot and peer-joined can arrive in either order).
    if (this.peers[remoteId]) return this.peers[remoteId];
    // Same stable clientId under ANOTHER id: either a restart survivor (adopt)
    // or the ghost of the same client's previous connection (close).
    if (clientId) {
      for (const id of Object.keys(this.peers)) {
        if (id === remoteId) continue;
        const old = this.peers[id];
        if (!old || old.remoteClientId !== clientId) continue;
        const cs = old.pc && old.pc.connectionState;
        // Still `connected`: media never dropped, only signaling. Re-key instead
        // of close+renegotiate; no new PeerConn means no addTrack and no
        // onnegotiationneeded, so ZERO renegotiation.
        if (cs === 'connected') {
          this._rekeyPeer(id, remoteId);
          const adopted = this.peers[remoteId];
          // Recompute politeness with the NEW ids (deterministic and symmetric),
          // covering glare during the brief asymmetric adoption window.
          adopted.polite = this.peerId < remoteId;
          console.log('[panel:vc] adopted ' + id.slice(-6) + '->' + remoteId.slice(-6) + ' (connected, media preserved, no renegotiation)');
          this._notifyPeerCount();
          return adopted;
        }
        // Not `connected`: the ghost of this client's dead connection. Close it
        // before creating the new one.
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
  // Rebuilds a peer connection from scratch. `warn`: we are the stuck side, so
  // send `reset` for the other side to rebuild and come back as initiator. The
  // reset goes over the same WS before the new offer, so it arrives first.
  // At most 3 per peer.
  Call.prototype._rebuildPeer = function (remoteId, reason, warn) {
    const old = this.peers[remoteId];
    if (!old || this.stopped) return;
    this._peerRebuilds = this._peerRebuilds || {};
    const n = (this._peerRebuilds[remoteId] || 0) + 1;
    if (n > 3) {
      console.warn('[panel:vc] connection with ' + remoteId.slice(-6) + ' did not come up after 3 rebuilds, giving up');
      this.cbError('Could not connect audio/video with ' + (old.remoteUser || 'the participant') + '. Leave and join again.');
      return;
    }
    this._peerRebuilds[remoteId] = n;
    console.warn('[panel:vc] rebuilding connection with ' + remoteId.slice(-6) + ' (' + reason + ', ' + n + '/3)');
    const user = old.remoteUser, clientId = old.remoteClientId;
    if (warn) this.send({ type: 'reset', to: remoteId, payload: jsonRaw({ reason: reason }) });
    try { old.close(); } catch (_) {}
    delete this.peers[remoteId];
    this._teardownPeerMonitor(remoteId);
    this.ensurePeer(remoteId, user, /*initiator=*/!!warn, clientId);
  };

  // Alpine does not see mutations of `this.peers` (not reactive), so notify the UI.
  Call.prototype._notifyPeerCount = function () {
    try {
      const list = [];
      // Dedup by clientId (guests can share a name) so a residual ghost never
      // inflates the participant count.
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

  // Safety net: removes any peer tile without a live PeerConn behind it.
  // Idempotent.
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

  // PiP survives reconnects: _preservePipTile unbinds the tile from the peer id
  // (which changes on rejoin) without removing the <video>, so the native PiP
  // window stays open. Indexed by stable clientId; _adoptDetachedPipTile
  // reattaches it, _dropDetachedPip cleans up if the peer never returns.
  Call.prototype._preservePipTile = function (v, tile, cid) {
    try {
      v.removeAttribute('data-vc-peer');
      v.setAttribute('data-vc-pip-detached', cid);
      if (tile) {
        // Out of _pruneOrphanTiles' reach and hidden from the grid; the OS
        // window keeps showing the video even with display:none.
        tile.removeAttribute('data-vc-peer-tile');
        tile.setAttribute('data-vc-pip-detached-tile', cid);
        tile.style.display = 'none';
      }
      this._pipDetached = this._pipDetached || {};
      this._pipDetached[cid] = { v: v, tile: tile };
      console.log('[panel:vc] PiP: tile preserved (client ' + cid.slice(-6) + '), OS window kept during reconnect');
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
        // Re-key the tile children, or onTrack's avatar/caption logic cannot find them.
        ['data-vc-peer-avatar', 'data-vc-peer-name', 'data-vc-peer-sub', 'data-vc-peer-caption'].forEach(function (a) {
          const el = tile.querySelector('[' + a + ']');
          if (el) el.setAttribute(a, newId);
        });
      }
    } catch (_) {}
    delete this._pipDetached[cid];
    console.log('[panel:vc] PiP: tile readopted (client ' + cid.slice(-6) + ' -> ' + newId.slice(-6) + '), media restored in the OS window');
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
      // Do NOT set width inline: it overrides the data-local-size CSS rules.
      v.style.cssText = 'background:#000;';
      this.videosEl.appendChild(v);
    }
    v.srcObject = this.localStream;
  };

  Call.prototype.setMuted = function (muted) {
    if (!this.localStream) return false;
    this._userMutedExplicit = !!muted;
    this.muted = !!muted; // read by the STT loop guard to skip the mute period
    for (const t of this.localStream.getAudioTracks()) t.enabled = !muted;
    // The raw track too: transcription reads it, so otherwise muted speech would
    // be sent as captions. (The muted-speaking alert reads its own clone.)
    if (this._micGainRawTrack) this._micGainRawTrack.enabled = !muted;
    this.send({ type: 'state', payload: jsonRaw({ mic: muted ? 'off' : 'on' }) });
    // On unmute, give STT a grace window so the loop guard does not trip on
    // counts left over from the mute period.
    if (!muted && this._subtitlesActive) {
      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
        if (window.PANEL_DEBUG) console.log('[panel:vc] unmute: kick STT restart');
        // Short delay so track.enabled propagates before restarting.
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
    // Manual "video on" must reset autoVideoOff, or the auto-degrade state
    // machine never emits auto-video-on again.
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
    catch (e) { this.cbError('camera: ' + e.message); return false; }
    const newTrack = s.getVideoTracks()[0];
    if (!newTrack) return false;
    // Stop the old camera track (release the device).
    if (this.cameraTrack && this.cameraTrack !== newTrack) {
      try { this.cameraTrack.stop(); } catch (_) {}
    }
    this.cameraTrack = newTrack;
    this.deviceIds.camera = deviceId;
    // During screen share do NOT replace the sender (it would silently drop the
    // shared screen); the new camera takes effect when sharing stops.
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
    catch (e) { this.cbError('microphone: ' + e.message); return false; }
    const newTrack = s.getAudioTracks()[0];
    if (!newTrack) return false;
    this.deviceIds.mic = deviceId;
    await this._replaceMicRaw(newTrack);
    return true;
  };

  // Toggles noise suppression, echo cancellation and auto gain by reopening the
  // mic: many browsers silently ignore applyConstraints for these three keys.
  // Goes through the device-swap path, so peers do not notice.
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
      this.cbError('microphone: ' + e.message);
      return prev;
    }
    const newTrack = s.getAudioTracks()[0];
    if (!newTrack) { this._micProc = prev; return prev; }
    await this._replaceMicRaw(newTrack);
    this.cbState({ type: 'mic-processing', value: next });
    return next;
  };

  // Builds the gain pipeline on the raw getUserMedia track. localStream (peers,
  // recording) gets the pipeline output; the raw track is kept for transcription
  // and source swaps.
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
    // A context created outside a user gesture may start suspended, which
    // sends pure silence to peers.
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
    console.log('[panel:vc] mic gain ready (' + Math.round(this._micGain * 100) + '%)');
    return true;
  };

  Call.prototype._attachMicSource = function (rawTrack) {
    if (this._micGainSrc) { try { this._micGainSrc.disconnect(); } catch (_) {} }
    this._micGainSrc = this._micGainCtx.createMediaStreamSource(new MediaStream([rawTrack]));
    this._micGainSrc.connect(this._micGainNode);
    this._micGainRawTrack = rawTrack;
  };

  // Swaps the raw track (another mic, or new processing). With the pipeline only
  // the source is rewired: the sent track is unchanged and no replaceTrack is needed.
  Call.prototype._replaceMicRaw = async function (newTrack) {
    newTrack.enabled = !this._userMutedExplicit;
    let old;
    if (this._micGainCtx && this._micGainNode) {
      old = this._micGainRawTrack;
      this._attachMicSource(newTrack);
    } else {
      // No pipeline (AudioContext refused): replace directly on the senders.
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
    // Transcription reads the raw track: restart it locally, no broadcast to peers.
    if (this._subtitlesActive && this._restartSubtitlesLocalOnly) {
      // A scheduled restart (mute/backoff pause) would start a second handle.
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      this._restartSubtitlesLocalOnly(this._subtitlesBackend || 'web-speech', this._subtitlesLang);
    }
  };

  // Level of what is SENT to peers (after gain and limiter); null = no pipeline.
  Call.prototype.getMicLevel = function () {
    if (!this._micAnalyser) return null;
    return readMicLevel(this._micAnalyser, this._micLevelBuf, this._micLimiter);
  };

  // Muted-speaking alert. Reads a CLONE of the raw track with its own enabled
  // flag, so muting does not silence the analyser. Restarted on every source swap.
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
      const mon = { ctx, stream: cloneStream, raf: 0, warned: false };
      this._localSpeakingMon = mon;
      const tick = () => {
        if (this.stopped || this._localSpeakingMon !== mon) return;
        // Resume if the browser suspended the context in a background tab.
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
        } catch (e) { this.cbError('audio output: ' + e.message); }
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
    // (1) Toggle video on/off (the phone profile goes audio-only mid-call).
    const wantsAudioOnly = !!p.videoOff;
    if (wantsAudioOnly !== this.audioOnly) {
      this.audioOnly = wantsAudioOnly;
      if (wantsAudioOnly) {
        // Hot-disable: drop the video sender and stop the camera. The peer keeps
        // receiving audio only; no renegotiation required.
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
        // Hot-enable: reacquire the camera and put it on the existing
        // transceiver, or add a new track if there is none.
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
        } catch (e) { this.cbError('re-enable video: ' + e.message); }
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
    // (3) Update Opus SDP tweak state. The new fmtp only reaches the peer on
    // the next renegotiation.
    const newOpusTweak = (p.opusTweak != null ? !!p.opusTweak : this.opusTweak);
    const opusChanged = newOpusTweak !== this.opusTweak;
    this.opusTweak = newOpusTweak;
    // (4) Apply bitrates on every active sender.
    for (const id in this.peers) this.peers[id].applyBitrates(p.videoKbps, p.audioKbps);
    // (5) Never dispatch 'negotiationneeded' by hand: without the native flag
    // dirty it causes an endless offer/answer loop. The Opus tweak is applied
    // by tweakOpusSdp on the next spontaneous renegotiation, or the next call.
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
      // Swap back to the camera BEFORE stopping the screen track, otherwise the
      // sender keeps sending a black frame until replaceTrack completes.
      let camTrack = this.cameraTrack;
      if (!camTrack || camTrack.readyState !== 'live') {
        // The camera track can die during the share (e.g. background tab).
        try {
          const stream = await navigator.mediaDevices.getUserMedia({ video: true });
          camTrack = stream.getVideoTracks()[0];
          this.cameraTrack = camTrack;
          if (this.localStream) this.localStream.addTrack(camTrack);
        } catch (e) {
          console.warn('[panel:vc] camera re-acquire failed after screen share', e);
        }
      }
      if (camTrack && camTrack.readyState === 'live') {
        await this.swapVideoSenderTrack(camTrack);
      }
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
      // Wait up to 2s for 'connected' before replaceTrack: on slow networks the
      // remote otherwise sees black frames. If it never connects, skip it (the
      // normal onnegotiationneeded path updates it).
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
          // Promise.resolve wrapper for Safari < 15.2, where replaceTrack may return synchronously.
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
    // If the caller registered onRecordingReady it decides (cloud upload,
    // local save, or both); otherwise fall back to a local download.
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
        // Discreet "Privacy on" label overlay
        ctx.fillStyle = 'rgba(0,0,0,0.6)';
        ctx.fillRect(0, canvas.height - 36, canvas.width, 36);
        ctx.fillStyle = '#fff';
        ctx.font = '14px sans-serif';
        ctx.fillText('🌫  Privacy on', 12, canvas.height - 14);
        this.frostRAF = requestAnimationFrame(draw);
      };
      this.frostActive = true;
      requestAnimationFrame(draw);
      const stream = canvas.captureStream(20);
      const blurTrack = stream.getVideoTracks()[0];
      try {
        await this.swapVideoSenderTrack(blurTrack);
      } catch (e) {
        // Tear down the canvas and video on failure so the blur does not outlive the toggle.
        this.frostActive = false;
        if (this.frostRAF) { cancelAnimationFrame(this.frostRAF); this.frostRAF = 0; }
        this.frostCanvas = null;
        if (this.frostVideoEl) { try { this.frostVideoEl.srcObject = null; } catch (_) {} this.frostVideoEl = null; }
        try { stream.getTracks().forEach(t => t.stop()); } catch (_) {}
        this.cbError('Privacy: ' + e.message);
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
  // `panel-files` channel:
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

  // Update the caption under a peer's tile (remote captions arrive over the
  // DataChannel, local ones from our own STT). Empty text hides it.
  Call.prototype.updatePeerCaption = function (peerId, text, isLocal) {
    if (!this.videosEl) return;
    // _showCaptions only controls the video overlay; text still flows through
    // cbState to the transcript panel and summary. Defaults to on.
    if (this._showCaptions === false) {
      try {
        const localCap = this.videosEl.querySelector('[data-vc-local-caption]');
        if (localCap) localCap.style.display = 'none';
        const allPeerCaps = this.videosEl.querySelectorAll('[data-vc-peer-caption]');
        allPeerCaps.forEach(el => el.style.display = 'none');
      } catch (_) {}
      return;
    }
    // The local caption is a separate overlay (data-vc-local-caption) because
    // the local video has no tile.
    if (isLocal || peerId === 'me') {
      // Remove duplicates first: orphan captions pile up in a different
      // parentElement when videosEl changes between renegotiations.
      try {
        const scopes = [this.videosEl, this.videosEl.parentElement].filter(Boolean);
        const allCaps = [];
        for (const scope of scopes) {
          scope.querySelectorAll('[data-vc-local-caption]').forEach(el => allCaps.push(el));
        }
        if (allCaps.length > 1) {
          for (let i = 0; i < allCaps.length - 1; i++) {
            try { allCaps[i].remove(); } catch (_) {}
          }
        }
      } catch (_) {}
      let cap = this.videosEl.querySelector('[data-vc-local-caption]')
             || (this.videosEl.parentElement && this.videosEl.parentElement.querySelector('[data-vc-local-caption]'));
      if (!cap && this.videosEl.parentElement) {
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

  // setShowCaptions only controls the visual overlay. When false, STT keeps
  // running and the transcript panel and summary keep filling.
  Call.prototype.setShowCaptions = function (show) {
    this._showCaptions = !!show;
    if (!this._showCaptions) {
      try {
        // The local caption may live in videosEl OR its parent, so clear both
        // scopes (this also removes orphan duplicates).
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
    this.cbState({ type: 'legendas-show', show: this._showCaptions });
  };

  // -- Live subtitles (PanelSTT adapter — whisper-local OR web-speech) --------
  // Ask EVERY active peer to turn its local STT on/off. Each peer transcribes
  // its own audio and sends the text over the DC, so one user enabling it
  // shows everyone's speech to everyone. Idempotent on the receiving side.
  // Returns {sent, pending, total} so the caller can give feedback.
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
        catch (e) { console.warn('[panel:vc] subs broadcast fail peer=' + id.slice(-6) + ': ' + e.message); }
      } else {
        pending++;
        // DC not open yet: dc.onopen checks _subtitlesActive and sends then.
        // No local queue needed; the Call flag is the source of truth.
      }
    }
    console.log('[panel:vc] subtitles-request broadcast: on=' + on +
                ' sent=' + sent + ' pending=' + pending + ' total=' + total);
    return { sent, pending, total };
  };

  // _assignSubsHandle: startWithBackend returns a sync session (web-speech),
  // a Promise (whisper-local) or null (no backend). Normalize it so
  // _subtitlesHandle is never a raw Promise (.stop/.setGain would be no-ops).
  // If subtitles were turned off while the Promise resolved, stop the new
  // handle instead of leaving it orphaned.
  Call.prototype._assignSubsHandle = function (p) {
    Promise.resolve(p).then((h) => {
      if (!this._subtitlesActive) {
        try { h && h.stop && h.stop(); } catch (_) {}
        return;
      }
      this._subtitlesHandle = h;
      // start returned null (no backend): report the failure to the initiator.
      if (!h && this._sendSubsStatus) this._sendSubsStatus(false, 'start-failed', this._subtitlesBackend);
    });
  };

  // _sendSubsStatus: when THIS peer was remotely enabled by an initiator, tell
  // the initiator over its DC whether STT actually started. The ack is one-shot
  // (_subtitlesAckSent); failure statuses may repeat. Only the enabled peer
  // confirms (1-to-1 for now).
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
    // Ignore a flip within 500ms of the last toggle: rapid stutter confuses remote peers.
    const now = Date.now();
    if (this._lastSubtitlesToggleAt && now - this._lastSubtitlesToggleAt < 500) {
      const prev = this._lastSubtitlesToggleValue;
      if (prev !== on) {
        console.warn('[panel:vc] setSubtitles toggle ignored (rapid stutter ' +
                    (now - this._lastSubtitlesToggleAt) + 'ms)');
        return this._subtitlesActive;
      }
    }
    this._lastSubtitlesToggleAt = now;
    this._lastSubtitlesToggleValue = on;
    if (on && !this._subtitlesActive) {
      if (!window.PanelSTT) { this.cbError('subtitles: STT module not loaded'); return false; }

      // Throttle partials to ~4/s (web-speech emits 10-20/s); finals always go
      // through. A trailing-edge send guarantees the latest text is shown.
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
        // Auto-clear on every update (not only on final, which may never come):
        // hide 6s after the last partial or 2.5s after a final.
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
        // Include displayName so the receiver does not depend on peer-count
        // arriving before the first caption.
        if (this.displayName && this.displayName !== 'You') {
          payload.displayName = this.displayName;
        }
        if (isFinal) {
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

      // Anti-loop guard counts consecutive restarts with no result. Fully
      // reset on manual enable so it can recover after a previous trip.
      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
      }
      const startWithBackend = (backend) => {
        // Raw microphone track (see getMicStreamForSTT).
        const sttStream = this.getMicStreamForSTT ? this.getMicStreamForSTT() : this.localStream;
        return window.PanelSTT.start({
          backend,
          continuous: true,
          interimResults: true,
          lang: opts.lang || 'pt-BR',
          // Token priority: opts.token > this.token (the call WS token, which
          // also works for invited guests) > global __PANELTOKEN__ (logged-in only).
          token: opts.token || this.token || (window.__PANELTOKEN__ || null),
          stream: sttStream,
          prompt: opts.prompt || '',
          idleMs: 20000,
          // onReady = STT really started (web-speech onstart / whisper
          // SERVER_READY): ack the initiator and cancel the connect timeout.
          onReady: () => {
            this._subtitlesLastSuccessAt = Date.now();
            if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
            this._sendSubsStatus(true, '', this._subtitlesBackend);
          },
          onPartial: (p) => {
            this._subtitlesLastSuccessAt = Date.now();
            this._subtitlesRestartCount = 0;
            // Backstop for onReady: text arrived, so STT is alive. Idempotent.
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
              console.warn('[panel:vc] whisper-local failed (' + e.code + '), falling back to web-speech');
              this._subtitlesActive = false; // blocks the onEnd restart below
              // Stop the old handle first, or whisper-local's WebSocket,
              // AudioContext and worklet are orphaned and can lock the mic.
              try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
              this._subtitlesHandle = null;
              setTimeout(() => {
                this._subtitlesActive = true;
                this._subtitlesRestartCount = 0; // reset when switching driver
                this._subtitlesLastSuccessAt = Date.now(); // grace window
                this._subtitlesBackend = 'web-speech';
                this._assignSubsHandle(startWithBackend('web-speech'));
                this.cbState({ type: 'subtitles-backend', backend: 'web-speech' });
              }, 250);
            } else if (e.fatal) {
              // web-speech fatal too: report it but do NOT turn off here; the
              // onEnd loop-guard decides. This is a real dead end, so tell the
              // initiator and cancel the connect timeout.
              this.cbError('subtitles: ' + (e.message || e.code));
              if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
              this._sendSubsStatus(false, e.code || 'fatal', this._subtitlesBackend);
            }
          },
          onEnd: (e) => {
            if (!this._subtitlesActive) return;
            // While muted, STT ends on silence: that is NOT a failure, or the
            // loop-guard would trip for anyone muted for a while.
            const isMuted = this.muted || (this.localStream && this.localStream.getAudioTracks().some(t => !t.enabled));
            if (isMuted) {
              // Slower retry (5s) without bumping the counter; unmute resumes fast.
              if (window.PANEL_DEBUG) console.log('[panel:vc] subtitles onEnd while muted, pausing restart');
              if (this._subtitlesRestartGuard) return;
              // The session already ended: null the handle so the unmute kick in
              // setMuted (guarded by `!_subtitlesHandle`) can restart it.
              this._subtitlesHandle = null;
              this._subtitlesRestartGuard = setTimeout(() => {
                this._subtitlesRestartGuard = null;
                if (this._subtitlesActive) this._assignSubsHandle(startWithBackend(backend));
              }, 5000);
              return;
            }
            // Loop-guard: 5 restarts with no result for 15s. Browsers restart
            // web-speech on idle, so a tighter guard trips on silence.
            const now = Date.now();
            const sinceLastSuccess = now - (this._subtitlesLastSuccessAt || 0);
            if (sinceLastSuccess > 15000) {
              this._subtitlesRestartCount++;
            }
            if (this._subtitlesRestartCount >= 5) {
              console.warn('[panel:vc] subtitles loop-guard tripped, turning off (backend=' + backend + ')');
              this._subtitlesActive = false;
              this.cbState({ type: 'subtitles-state', active: false, source: 'loop-guard' });
              this.cbError('Subtitles turned off: ' + backend + ' is unstable. Tap the 🎙 button to try again.');
              return;
            }
            if (this._subtitlesRestartGuard) return;
            // Exponential backoff: 1s, 2s, 4s, 8s, 16s (cap)
            const delay = Math.min(16000, 1000 * Math.pow(2, this._subtitlesRestartCount));
            console.log('[panel:vc] subtitles restart in ' + delay + 'ms (attempt ' + (this._subtitlesRestartCount + 1) + ', backend=' + backend + ', reason=' + (e.reason || 'unknown') + ', code=' + (e.code || '?') + ')');
            this._subtitlesRestartGuard = setTimeout(() => {
              this._subtitlesRestartGuard = null;
              if (this._subtitlesActive) this._assignSubsHandle(startWithBackend(backend));
            }, delay);
          },
        });
      };

      this._subtitlesActive = true;
      this._subtitlesLang = opts.lang || 'pt-BR';
      this._subtitlesBackend = 'web-speech';
      // Lets changeSubtitlesLang restart in place without an off/on broadcast.
      this._restartSubtitlesLocalOnly = (backend, lang) => {
        opts.lang = lang;
        this._subtitlesBackend = backend;
        this._assignSubsHandle(startWithBackend(backend));
      };
      // Every on/off transition emits state so the UI button stays in sync,
      // including remote activation (_silentPropagate).
      this.cbState({ type: 'subtitles-state', active: true, source: opts._silentPropagate ? 'remote' : 'local' });
      // whisper-local (WhisperLive) is the default; web-speech is the fallback
      // when it is offline or when the user picks it in the call settings.
      const requestedBackend = opts.backend ||
        (typeof localStorage !== 'undefined' && localStorage.getItem('panel_vc_stt_backend')) ||
        'whisper-local';
      (async () => {
        let backend = requestedBackend;
        if (backend === 'whisper-local') {
          const ok = await window.PanelSTT.probeWhisperLocal();
          if (!this._subtitlesActive) return;
          if (!ok) {
            console.warn('[panel:vc] whisper-local unavailable, falling back to web-speech');
            backend = 'web-speech';
          }
        }
        if (!this._subtitlesActive) return;
        this._subtitlesBackend = backend;
        try { window.PanelSTT.setBackend(backend); } catch (_) {}
        this._assignSubsHandle(startWithBackend(backend));
        this.cbState({ type: 'subtitles-backend', backend });
        // If remotely enabled (an initiator is waiting), report 'connect-timeout'
        // when no ack happens within 6s. Cleared on onReady/partial/final,
        // terminal onError and OFF.
        if (this._subtitlesRequestedById) {
          if (this._subsAckTimer) clearTimeout(this._subsAckTimer);
          this._subsAckTimer = setTimeout(() => {
            this._subsAckTimer = null;
            this._sendSubsStatus(false, 'connect-timeout', this._subtitlesBackend);
          }, 6000);
        }
      })();
      // Ask every peer to start its local STT too, unless this start came from
      // a remote request (avoids an amplification loop).
      if (!opts._silentPropagate) {
        // We are the initiator: clear any stale remote requester so onReady
        // does not ack an old peer.
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
        // Rescue retries at 1.5/4/8s for peers whose DC opened but whose send
        // failed silently (dc.onopen already covers DCs that open later).
        if (r.pending > 0 || r.sent < r.total) {
          const retries = [1500, 4000, 8000];
          retries.forEach((delay, i) => {
            setTimeout(() => {
              if (!this._subtitlesActive) return;
              const r2 = this._broadcastSubtitlesRequest(true, this._subtitlesLang);
              if (window.PANEL_DEBUG) console.log('[panel:vc] subs rescue retry ' + (i+1) + '/3: sent=' + r2.sent + '/' + r2.total);
            }, delay);
          });
        }
      }
      return true;
    }
    if (!on && this._subtitlesActive) {
      this._subtitlesActive = false;
      this.cbState({ type: 'subtitles-state', active: false, source: opts._silentPropagate ? 'remote' : 'local' });
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      // Cancel the pending trailing caption so nothing is sent after off.
      if (this._captionTrailingTimer) { clearTimeout(this._captionTrailingTimer); this._captionTrailingTimer = null; }
      this._captionTrailingPayload = null;
      // Reset the round-trip so no connect-timeout fires a false status after off.
      this._subtitlesRequestedById = null;
      this._subtitlesAckSent = false;
      if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      // Off does NOT stop other peers' STT (each controls its own mic); the
      // request is informational only.
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

  // getMicStreamForSTT: transcription ALWAYS reads the raw mic track, before
  // send gain, limiter, Opus and network. Send gain is for listeners; the STT
  // engines normalize level themselves and pre-amplifying only risks clipping.
  Call.prototype.getMicStreamForSTT = function () {
    const raw = this._micGainRawTrack;
    if (raw && raw.readyState === 'live') {
      try { return new MediaStream([raw]); } catch (_) {}
    }
    return this.localStream;
  };

  // ---- Owner action helpers ------------------------------------------------
  // The server already validated that these came from the room owner.
  Call.prototype._applyOwnerMute = function (mute, byPeerId) {
    if (mute && !this.muted) {
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

  // Owner API for the UI. The server validates ownership before relaying and
  // silently ignores requests from non-owners.
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

  // Live mic send gain: affects what peers hear and the recording, not STT
  // (which reads the raw track). A short ramp keeps slider drags from clicking.
  Call.prototype.setMicGain = function (v) {
    const g = clampMicGain(v);
    this._micGain = g;
    if (this._micGainNode && this._micGainCtx) {
      try { this._micGainNode.gain.setTargetAtTime(g, this._micGainCtx.currentTime, 0.015); }
      catch (_) { try { this._micGainNode.gain.value = g; } catch (_) {} }
      this.cbState({ type: 'mic-gain', value: g });
    } else {
      // No gain pipeline: keep the value but there is nothing to apply it to.
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
    // Restart locally only (no re-broadcast), reusing the error/restart lifecycle.
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
        // Warn about the weak connection BEFORE auto-video-off so the user knows why.
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
        // No restartIce here: it only belongs to ICE 'failed' and would keep the
        // negotiation flag dirty (renegotiation loop). The renegotiation after
        // setCodecPreferences already reselects the codec.
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
    // Stable remote identity (PeerInfo.client_id), empty for old clients.
    // Used by the visual dedup in ensurePeer.
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
    if (this.call.e2eePassphrase && window.PanelVideoCallE2EE) {
      window.PanelVideoCallE2EE.setup(this.pc, this.call.e2eePassphrase, this.call.roomId, {
        // Fires when 30+ frames fail to decrypt, almost always a wrong passphrase.
        onDecryptFail: (count) => {
          this.call.cbError('E2EE: ' + count + ' frames failed to decrypt. The passphrase may be wrong; check with the other side.');
        },
      })
        .then(() => { this.call.e2eeActive = true; })
        .catch(err => this.call.cbError('E2EE: ' + err.message));
    }

    this.pc.ontrack = (ev) => {
      console.log('[panel:vc] ontrack peer=' + this.remoteId.slice(-6) + ' kind=' + (ev.track && ev.track.kind) + ' muted=' + (ev.track && ev.track.muted) + ' enabled=' + (ev.track && ev.track.enabled) + ' state=' + (ev.track && ev.track.readyState) + ' streams=' + (ev.streams ? ev.streams.length : 0));
      this.onTrack(ev);
    };
    this._localCandCount = 0;
    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) {
        this._localCandCount++;
        this.call.send({ type: 'ice', to: this.remoteId, payload: jsonRaw(ev.candidate.toJSON()) });
      } else {
        console.log('[panel:vc] ICE gathering complete peer=' + this.remoteId.slice(-6));
      }
    };
    // Shared by the ICE and connection state handlers; the _restartPending flag
    // prevents a double restart when both turn 'failed' at the same time.
    const tryRestartIce = (reason) => {
      if (this._restartPending) return;
      const now = Date.now();
      if (now - (this._lastIceRestartAt || 0) < 15000) return;
      this._restartPending = true;
      this._lastIceRestartAt = now;
      console.warn('[panel:vc] ' + reason + ' — restarting ICE peer=' + this.remoteId.slice(-6));
      try { this.pc.restartIce(); }
      catch (e) { console.error('[panel:vc] restartIce threw: ' + e.message); }
      setTimeout(() => { this._restartPending = false; }, 15000);
    };
    this.pc.oniceconnectionstatechange = () => {
      const st = this.pc.iceConnectionState;
      console.log('[panel:vc] ICE state peer=' + this.remoteId.slice(-6) + ' = ' + st);
      if (st === 'failed') tryRestartIce('ICE failed');
    };
    this.pc.onconnectionstatechange = () => {
      const cs = this.pc.connectionState;
      console.log('[panel:vc] connectionState peer=' + this.remoteId.slice(-6) + ' = ' + cs);
      if (cs === 'failed') tryRestartIce('connectionState failed');
    };
    // Negotiation is throttled per CYCLE (until back to 'stable'), not by time:
    //   _negotiationCycleEnd: when we last returned to 'stable'
    //   _negotiationFiredInCycle: an offer was already sent this cycle
    //   _lastNegotiationAt: time of the last offer
    this._negotiationCycleEnd = 0;
    this._negotiationFiredInCycle = false;
    this.pc.onsignalingstatechange = () => {
      const st = this.pc.signalingState;
      console.log('[panel:vc] signalingState peer=' + this.remoteId.slice(-6) + ' = ' + st);
      if (st === 'stable') {
        this._negotiationCycleEnd = Date.now();
        this._negotiationFiredInCycle = false; // reset for the next cycle
        // Safety net: signaling finished but ICE never left 'new', so the
        // 'failed' restart never fires. Sometimes an RTCPeerConnection gathers
        // NO local candidates even after restartIce, while a new one works:
        //   - zero local candidates: this instance is stuck, rebuild the peer;
        //   - candidates present: a path problem, restartIce.
        if (this._iceStallTimer) clearTimeout(this._iceStallTimer);
        this._iceStallTimer = setTimeout(() => {
          this._iceStallTimer = null;
          if (!this.pc || this.pc.signalingState === 'closed') return;
          if (this.pc.iceConnectionState !== 'new' || !this.pc.remoteDescription) return;
          if (!this._localCandCount) this.call._rebuildPeer(this.remoteId, 'no local ICE candidate', true);
          else tryRestartIce('ICE stuck in new after negotiation');
        }, 5000);
      }
    };
    this.pc.onnegotiationneeded = async () => {
      // Anti-loop protection.
      // (1) only negotiate in stable
      if (this.pc.signalingState !== 'stable') {
        console.warn('[panel:vc] onnegotiationneeded ignored — state=' + this.pc.signalingState + ' peer=' + this.remoteId.slice(-6));
        return;
      }
      // (2) only ONE offer per cycle (until back to stable)
      if (this._negotiationFiredInCycle) {
        console.log('[panel:vc] onnegotiationneeded coalesced (already-fired-this-cycle) peer=' + this.remoteId.slice(-6));
        return;
      }
      const now = Date.now();
      // (3) 500ms cooldown after stable: absorbs the rebound event from
      // setCodecPreferences/replaceTrack already covered by the previous round.
      if (this._negotiationCycleEnd && now - this._negotiationCycleEnd < 500) {
        console.log('[panel:vc] onnegotiationneeded coalesced (cooldown post-stable) peer=' + this.remoteId.slice(-6));
        return;
      }
      // (4) 200ms throttle: coalesces the burst of addTrack events from the
      // constructor into one offer.
      if (now - (this._lastNegotiationAt || 0) < 200) {
        console.log('[panel:vc] onnegotiationneeded coalesced (throttled) peer=' + this.remoteId.slice(-6));
        return;
      }
      this._lastNegotiationAt = now;
      this._negotiationFiredInCycle = true;
      console.log('[panel:vc] onnegotiationneeded FIRE peer=' + this.remoteId.slice(-6) + ' signalingState=' + this.pc.signalingState);
      // Offer in flight: a polite side receiving an offer now waits for this
      // to settle and rolls back explicitly (see handleSignal).
      let offerSettled;
      this._offerInFlight = new Promise((r) => { offerSettled = r; });
      try {
        this.makingOffer = true;
        const offer = await this.pc.createOffer();
        if (this.call.opusTweak) {
          offer.sdp = tweakOpusSdp(offer.sdp, this.call.audioKbps);
        }
        if (this.pc.signalingState !== 'stable') {
          console.warn('[panel:vc] onnegotiationneeded aborted post-createOffer — state=' + this.pc.signalingState);
          this._negotiationFiredInCycle = false; // let the next attempt through
          return;
        }
        await this.pc.setLocalDescription(offer);
        // If a remote offer was applied on top (rollback), this offer is dead;
        // sending it would make the other side answer a session that is gone.
        if (this.pc.signalingState !== 'have-local-offer') {
          console.warn('[panel:vc] offer superseded before sending, state=' + this.pc.signalingState + ' peer=' + this.remoteId.slice(-6));
          return;
        }
        this.call.send({ type: 'offer', to: this.remoteId, payload: jsonRaw(this.pc.localDescription) });
      } catch (e) {
        this._negotiationFiredInCycle = false;
        this.call.cbError('negotiation: ' + e.message);
      } finally {
        this.makingOffer = false;
        this._offerInFlight = null;
        offerSettled();
      }
    };

    if (this.initiator) {
      // Initiator opens all DCs up-front; the polite side listens via
      // ondatachannel and routes by label.
      this.dc = this.pc.createDataChannel('panel-chat', { ordered: true });
      this.setupDataChannel(this.dc);
      this.dcFiles = this.pc.createDataChannel('panel-files', { ordered: true });
      this.setupFilesChannel(this.dcFiles);
      // Whiteboard: reliable + ordered. A dropped stroke leaves a permanent gap
      // in a shared drawing (no way to backfill a single missing segment), and
      // the on-open snapshot below relies on guaranteed delivery to bootstrap a
      // (re)joining peer. Latency cost is negligible for sparse stroke events.
      this.dcWB = this.pc.createDataChannel('panel-wb', { ordered: true });
      this.setupWhiteboardChannel(this.dcWB);
    } else {
      this.pc.ondatachannel = (ev) => {
        const dc = ev.channel;
        if (dc.label === 'panel-chat')        { this.dc = dc; this.setupDataChannel(dc); }
        else if (dc.label === 'panel-files')  { this.dcFiles = dc; this.setupFilesChannel(dc); }
        else if (dc.label === 'panel-wb')     { this.dcWB = dc; this.setupWhiteboardChannel(dc); }
      };
    }

    // File transfer receiver state — declared once per PeerConn.
    this._fileRx = {
      incoming: new Map(), // id → {meta, chunks, received}
      expecting: null,     // id we're currently receiving binary for
    };
  }

  PeerConn.prototype.setupDataChannel = function (dc) {
    // Consistent binaryType across browsers (Safari historically defaulted to 'blob').
    try { dc.binaryType = 'arraybuffer'; } catch (_) {}
    // If local transcription is on, send the request to THIS peer so late
    // joiners also start STT. Idempotent on the receiver.
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
        console.log('[panel:vc] subtitles-request sent (DC open) → peer=' + this.remoteId.slice(-6));
      } catch (e) { console.warn('[panel:vc] subs req send fail: ' + e.message); }
    };
    // The polite peer gets the DC via ondatachannel and it may already be open.
    if (dc.readyState === 'open') {
      sendSubsReqIfActive();
    } else {
      // Chain any existing onopen.
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
      // Cap string length: a hostile peer could send megabytes of text and freeze the UI.
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
          displayName: typeof payload.displayName === 'string' ? safeText(payload.displayName).slice(0, 80) : undefined,
          text: captionText,
          final: !!payload.final,
          words: Array.isArray(payload.words) ? payload.words.slice(0, 200) : undefined,
          confidence: typeof payload.confidence === 'number' ? payload.confidence : undefined,
          lang: typeof payload.lang === 'string' ? payload.lang.slice(0, 8) : undefined,
          startMs: typeof payload.startMs === 'number' ? payload.startMs : undefined,
          endMs: typeof payload.endMs === 'number' ? payload.endMs : undefined,
        });
        // Shown under the peer's tile; cleared 3.5s after a final.
        this.call.updatePeerCaption(this.remoteId, captionText);
        if (payload.final) {
          clearTimeout(this._captionClearTimer);
          this._captionClearTimer = setTimeout(() => this.call.updatePeerCaption(this.remoteId, ''), 3500);
        }
      } else if (payload.type === 'subtitles-request') {
        // A remote peer asked everyone to turn local STT on/off. Each peer
        // transcribes only its own voice, so all must run STT to see all speech.
        // payload: { type: 'subtitles-request', on: bool, lang?: string, requestedBy?: string, ts?: number }
        const requesterLabel = payload.requestedBy || ('peer ' + this.remoteId.slice(-4));
        console.log('[panel:vc] subtitles-request received: on=' + payload.on +
                    ' from=' + this.remoteId.slice(-6) + ' by=' + requesterLabel +
                    ' lang=' + (payload.lang || 'pt-BR'));
        if (typeof payload.on !== 'boolean') return;
        // Tell the UI WHO asked, even if local STT is already on.
        this.call.cbState({
          type: 'subtitles-requested',
          from: this.remoteId,
          requestedBy: requesterLabel,
          on: !!payload.on,
          lang: payload.lang || 'pt-BR',
        });
        if (payload.on) {
          // Remember the initiator to send ack/status when STT starts or fails.
          // Single requester only; needs a per-peer map for 3+ peers.
          this.call._subtitlesRequestedById = this.remoteId;
          this.call._subtitlesAckSent = false;
          if (this.call._subtitlesActive) {
            console.log('[panel:vc] STT already on, skipping idempotent request');
            // Already transcribing: ack now, or a re-request would never be acked.
            this.call._sendSubsStatus(true, '', this.call._subtitlesBackend);
            return;
          }
          // Enable locally WITHOUT re-propagating (the requester already
          // broadcast to everyone; avoids an amplification loop).
          const attemptActivate = (retries) => {
            if (!window.PanelSTT) {
              if (retries > 0) {
                // PanelSTT may still be lazy-loading.
                console.log('[panel:vc] PanelSTT not loaded, retrying in 500ms (' + retries + ' left)');
                setTimeout(() => attemptActivate(retries - 1), 500);
                return;
              }
              console.warn('[panel:vc] PanelSTT unavailable, peer ' +
                          requesterLabel + ' enabled STT but I cannot transcribe my voice');
              this.call.cbError('Transcription: ' + requesterLabel +
                                ' turned it on but your browser has no STT loaded');
              // Tell the initiator explicitly that we could not enable it.
              try { this.dc.send(JSON.stringify({ type: 'subtitles-status', ok: false, reason: 'no-stt-module' })); } catch (_) {}
              return;
            }
            try {
              const ok = this.call.setSubtitles(true, {
                lang: payload.lang || 'pt-BR',
                _silentPropagate: true,
              });
              console.log('[panel:vc] STT remote-activated → ' + (ok ? 'OK' : 'FAIL'));
            } catch (e) {
              console.error('[panel:vc] STT remote-activate error: ' + e.message);
            }
          };
          // Up to 5 retries of 500ms for PanelSTT to load
          attemptActivate(5);
        }
        // An off request does NOT stop our STT (each peer controls its own mic);
        // the cbState above is informational.
      } else if (payload.type === 'subtitles-status' || payload.type === 'subtitles-ack') {
        // ack/status from a peer we enabled remotely: the real confirmation
        // that its STT started (or failed). Forwarded to the UI.
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

  // ---- File transfer (per-peer DataChannel `panel-files`) ----
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
      this.call.cbError('file channel is not open for ' + this.remoteUser);
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

  // ---- Whiteboard (per-peer DataChannel `panel-wb`, reliable + ordered) ----
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
      } catch (e) { console.warn('[panel:vc] wb snapshot send fail: ' + e.message); }
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
    // Do NOT dispatch 'negotiationneeded' by hand: setCodecPreferences already
    // sets the native flag, and a manual dispatch causes a renegotiation loop.
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
    // Detect video transceivers by sender OR receiver track: in audio-only mode
    // sender.track is null, and losing codec prefs there causes a renegotiation loop.
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
      // Log setParameters failures: Firefox (no transactionId) and Chrome
      // (degradationPreference changed mid-call) reject with OperationError.
      sender.setParameters(params).catch(e => {
        console.warn('[panel:vc] setParameters failed kind=' + (sender.track && sender.track.kind) + ': ' + e.message);
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
    // If a tile of the same client (stable clientId) was preserved in PiP,
    // re-adopt THAT <video> so the OS window kept open during the reconnect
    // gets the restored media.
    if (!v) {
      v = this.call._adoptDetachedPipTile(this.remoteClientId || '', this.remoteId) || null;
    }
    if (!v) {
      // Wrapper tile: video plus a caption overlay under this peer's video.
      const tile = document.createElement('div');
      tile.setAttribute('data-vc-peer-tile', this.remoteId);
      tile.style.cssText = 'position:relative;display:flex;flex-direction:column;align-items:center;max-width:640px;width:100%;min-height:180px;';
      // Avatar fallback, shown when there is no video. Color derived from the peer id.
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
      nameLbl.textContent = (this.remoteUser || 'Participant').replace(/^guest:/, '');
      const sub = document.createElement('div');
      sub.setAttribute('data-vc-peer-sub', this.remoteId);
      sub.style.cssText = 'font-size:11px;opacity:.7;';
      sub.textContent = '📷 no camera';
      avatar.appendChild(ini); avatar.appendChild(nameLbl); avatar.appendChild(sub);
      tile.appendChild(avatar);

      v = document.createElement('video');
      v.setAttribute('data-vc-peer', this.remoteId);
      v.autoplay = true; v.playsInline = true;
      // The video is ALWAYS visible; the avatar sits above it (z-index) until
      // frames render. Never display:none the video: remote tracks arrive muted
      // and Chrome does not reliably fire 'unmute'.
      v.style.cssText = 'width:100%;height:auto;border-radius:8px;border:1px solid #1f2937;background:#000;position:relative;z-index:1;';
      tile.appendChild(v);
      const cap = document.createElement('div');
      cap.setAttribute('data-vc-peer-caption', this.remoteId);
      cap.style.cssText = 'position:absolute;left:8px;right:8px;bottom:8px;background:rgba(0,0,0,.75);color:#fff;font-size:15px;font-weight:500;padding:6px 12px;border-radius:8px;text-align:center;backdrop-filter:blur(6px);display:none;pointer-events:none;z-index:3;line-height:1.3;';
      tile.appendChild(cap);
      this.call.videosEl && this.call.videosEl.appendChild(tile);
    }
    v.srcObject = stream;
    // Hide the avatar once the first frame decodes (videoWidth > 0), using
    // events reliable in every browser (loadedmetadata, resize, playing); show
    // it again when the track ends.
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
    v.addEventListener('loadedmetadata', hideAvatar);
    v.addEventListener('resize', hideAvatar);
    v.addEventListener('playing', hideAvatar);
    const trackEndedHandlers = [];
    stream.getVideoTracks().forEach(t => {
      t.addEventListener('ended', showAvatarIfNoVideo);
      trackEndedHandlers.push({ track: t, handler: showAvatarIfNoVideo });
    });
    stream.addEventListener('removetrack', showAvatarIfNoVideo);
    // Kept so PeerConn.close can remove the listeners; their closures would
    // otherwise leak this.call across calls.
    this._videoElCleanup = () => {
      try { v.removeEventListener('loadedmetadata', hideAvatar); } catch (_) {}
      try { v.removeEventListener('resize', hideAvatar); } catch (_) {}
      try { v.removeEventListener('playing', hideAvatar); } catch (_) {}
      try { stream.removeEventListener('removetrack', showAvatarIfNoVideo); } catch (_) {}
      trackEndedHandlers.forEach(({ track, handler }) => {
        try { track.removeEventListener('ended', handler); } catch (_) {}
      });
    };
    if (v.videoWidth > 0) hideAvatar();
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

  // A peer's signaling messages are processed IN ORDER, one at a time (W3C
  // perfect negotiation); interleaving them left the call silent during glare.
  PeerConn.prototype.handleSignal = function (msg) {
    this._sigChain = (this._sigChain || Promise.resolve())
      .then(() => this._handleSignal(msg))
      .catch((e) => console.warn('[panel:vc] signal ' + msg.type + ' failed: ' + (e && e.message)));
    return this._sigChain;
  };

  PeerConn.prototype._handleSignal = async function (msg) {
    const payload = safeParse(msg.payload);
    if (!payload) return;
    try {
      if (msg.type === 'offer') {
        // Polite side with its own offer between createOffer and
        // setLocalDescription: still 'stable', so the browser's IMPLICIT
        // rollback would race setLocalDescription (Chrome then gathers no ICE
        // candidates). Wait for the offer to settle and use the explicit rollback.
        if (this.polite && this._offerInFlight) {
          try { await this._offerInFlight; } catch (_) {}
        }
        const offerCollision = this.makingOffer || this.pc.signalingState !== 'stable';
        this.ignoreOffer = !this.polite && offerCollision;
        if (this.ignoreOffer) return;
        // Glare: the rollback MUST complete before setRemoteDescription (in
        // parallel Chrome throws InvalidStateError), and is only valid in
        // have-local-offer.
        if (offerCollision && this.pc.signalingState === 'have-local-offer') {
          await this.pc.setLocalDescription({ type: 'rollback' });
          // Reset now: onsignalingstatechange may arrive late and the next
          // offer would be silently coalesced.
          this._negotiationFiredInCycle = false;
          this._negotiationCycleEnd = Date.now();
        }
        await this.pc.setRemoteDescription(payload);
        // Drain ICE candidates that arrived before the remote description.
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
        // Queue candidates that arrive before the remote description.
        if (!this.pc.remoteDescription || !this.pc.remoteDescription.type) {
          if (!this._pendingIce) this._pendingIce = [];
          // Cap at 200 against a hostile peer spamming ICE (normal is ~30).
          if (this._pendingIce.length < 200) {
            this._pendingIce.push(payload);
          } else if (this._pendingIce.length === 200) {
            console.warn('[panel:vc] _pendingIce cap reached peer=' + this.remoteId.slice(-6));
            this._pendingIce.push(payload); // keep the one that triggers the warning
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
    // Remove video element listeners first, or their closures leak this.call.
    if (this._videoElCleanup) {
      try { this._videoElCleanup(); } catch (_) {}
      this._videoElCleanup = null;
    }
    if (this._opusReneTimer) { clearTimeout(this._opusReneTimer); this._opusReneTimer = null; }
    if (this._iceStallTimer) { clearTimeout(this._iceStallTimer); this._iceStallTimer = null; }
    this._pendingIce = null;
    try { if (this.dc) this.dc.close(); } catch (_) {}
    // Null the handlers so closed PCs are not kept alive through closures.
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
      // Remove the whole tile (avatar, name, caption), not just the <video>,
      // or an orphan ghost placeholder stays in the DOM.
      const v = this.call.videosEl.querySelector('[data-vc-peer="' + this.remoteId + '"]');
      const tile = this.call.videosEl.querySelector('[data-vc-peer-tile="' + this.remoteId + '"]');
      // If THIS peer is in native Picture-in-Picture, do NOT destroy the
      // <video>: the OS window is bound to the element. Preserve it (frozen on
      // the last frame) and reattach media when the same clientId returns.
      // Only on a reconnect drop; our own hangup tears everything down.
      const cid = this.remoteClientId || '';
      if (v && cid && !this.call._userInitiatedHangup &&
          document.pictureInPictureElement && document.pictureInPictureElement === v) {
        this.call._preservePipTile(v, tile, cid);
        const cap = this.call.videosEl.querySelector('[data-vc-peer-caption="' + this.remoteId + '"]');
        if (cap) { try { cap.remove(); } catch (_) {} }
        return; // tile preserved, do not remove
      }
      if (v) { try { v.srcObject = null; } catch (_) {} }
      if (tile) {
        tile.remove();
      } else if (v) {
        v.remove(); // defensive fallback (no tile, legacy layout)
      }
      // The peer caption may have been created outside the tile; remove any orphan.
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

  // Stable client id (32 hex chars), persisted per tab in sessionStorage so a
  // WS reconnect evicts the previous ghost peer instead of duplicating its tile.
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

  // Map raw English errors from older servers to user-facing messages
  // (newer servers already send them via mapJoinErrorPT). Case-insensitive.
  function translateLegacyError(msg) {
    if (!msg || typeof msg !== 'string') return msg;
    const m = msg.toLowerCase();
    if (m.includes('room is full'))            return 'Room is full: the 4-person limit was reached.';
    if (m.includes('user already in room'))     return 'Your account is already in this room in another tab. Close it to join here.';
    if (m.includes('rate limit'))               return 'Too many requests. Wait a few seconds.';
    if (m.includes('forbidden'))                return 'You are not a member of this room.';
    if (m.includes('peer not in any room'))     return 'Session lost. Reload the page.';
    if (m.includes('target peer not connected'))return 'The other side disconnected.';
    if (m.includes('target peer is not in the same room')) return 'The recipient is not in this room.';
    if (m.includes('peer missing'))             return 'Invalid identification. Reload the page.';
    if (m.startsWith('signal '))                return 'Signaling error: the connection may be unstable.';
    return msg;
  }
})();
