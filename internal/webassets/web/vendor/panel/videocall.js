(function () {
  'use strict';

  if (window.PanelVideoCall) return;

  const CODEC_PREF = ['video/AV1', 'video/VP9', 'video/H264', 'video/VP8'];
  const DEFAULT_BUDGET_KBPS = 500;
  const STATS_INTERVAL_MS = 1000;
  const RECONNECT_BACKOFF_MIN = 1000;
  const RECONNECT_BACKOFF_MAX = 30000;
  const QUALITY_MODES = {
    phone:   { videoOff: true,  width: 0,    height: 0,   fps: 0,  videoKbps: 0,    audioKbps: 16, opusTweak: true,  label: 'Phone'    },
    low:     { videoOff: false, width: 160,  height: 90,  fps: 8,  videoKbps: 25,   audioKbps: 16, opusTweak: true,  label: 'Minimum'  },
    economy: { videoOff: false, width: 320,  height: 180, fps: 15, videoKbps: 60,   audioKbps: 20, opusTweak: true,  label: 'Economy'  },
    medium:  { videoOff: false, width: 640,  height: 360, fps: 24, videoKbps: 500,  audioKbps: 32, opusTweak: false, label: 'Medium'   },
    high:    { videoOff: false, width: 1280, height: 720, fps: 30, videoKbps: 2000, audioKbps: 48, opusTweak: false, label: 'High'     },
  };
  const BUDGET_FLOOR_KBPS = 8;
  const BUDGET_CEILING_KBPS = 6000;

  function tweakOpusSdp(sdp, audioKbps) {
    if (!sdp) return sdp;
    const lines = sdp.split(/\r?\n/);
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

  let active = null;

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

  function disconnect() {
    if (!active) return;
    active.stop();
    active = null;
  }

  function sendChat(text) {
    if (!active) return;
    active.sendChat(text);
  }

  function sendState(payload) {
    if (!active) return false;
    try {
      active.send({ type: 'state', payload: payload });
      return true;
    } catch (_) { return false; }
  }

  function setMuted(muted) {
    if (!active) return false;
    return active.setMuted(muted);
  }

  function setVideoOff(off) {
    if (!active) return false;
    return active.setVideoOff(off);
  }

  function setBudgetKbps(kbps) {
    if (!active) return;
    active.setBudgetKbps(kbps);
  }

  async function setScreenShare(on) {
    if (!active) return false;
    return active.setScreenShare(on);
  }

  function startRecording() {
    if (!active) return false;
    return active.startRecording();
  }

  function stopRecording() {
    if (!active) return;
    active.stopRecording();
  }

  function setAudioFirstMode(on) {
    if (!active) return;
    active.audioFirstMode = !!on;
  }

  function getQualityPresets() {
    return JSON.parse(JSON.stringify(QUALITY_MODES));
  }

  async function applyQualityProfile(profileOrName) {
    if (!active) return null;
    return active.applyQualityProfile(profileOrName);
  }

  async function listDevices() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) {
      return { cameras: [], mics: [], speakers: [] };
    }
    const list = await navigator.mediaDevices.enumerateDevices();
    const sanitize = (arr) => {
      const seen = new Set();
      const out = [];
      for (let i = 0; i < arr.length; i++) {
        const d = arr[i];
        if (!d) continue;
        const id = d.deviceId || ('synth-' + d.kind + '-' + i);
        if (seen.has(id)) continue;
        seen.add(id);
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
  async function probeDevicePermission() {
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      return { ok: false, audio: false, video: false, error: 'Browser does not support getUserMedia (HTTPS required).' };
    }
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

  function sendFile(file) {
    if (!active) return Promise.reject(new Error('no active call'));
    return active.sendFile(file);
  }

  function setPrivacyFrost(on) {
    if (!active) return false;
    return active.setPrivacyFrost(on);
  }

  function sendWhiteboardEvent(ev) {
    if (!active) return;
    active.broadcastWhiteboard(ev);
  }

  function getWhiteboardStrokes() {
    return active ? active._wbStrokes : [];
  }

  function getAnnotStrokes() {
    return active ? active._annotStrokes : [];
  }

  function setSubtitles(on, opts) {
    if (!active) return false;
    return active.setSubtitles(on, opts || {});
  }

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
      const webSpeech = !!(window.SpeechRecognition || window.webkitSpeechRecognition);
      const whisperLocal = !!(window.PanelSTT && typeof window.PanelSTT.connect === 'function');
      return webSpeech || whisperLocal;
    },
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

  const MIC_GAIN_MIN = 0.25;
  const MIC_GAIN_MAX = 4.0;
  function clampMicGain(v) {
    const n = Number(v);
    if (!isFinite(n) || n <= 0) return 1.0;
    return Math.min(MIC_GAIN_MAX, Math.max(MIC_GAIN_MIN, n));
  }
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

  function Call(opts) {
    this.opts = opts || {};
    this.roomId = opts.roomId;
    this.token = opts.token;
    this.ticketProvider = opts.ticketProvider || null;
    this.clientId = this._resolveClientId();
    this.displayName = opts.displayName || 'You';
    this.codecPref = opts.codec || 'auto';
    const presetName = (typeof opts.quality === 'string') ? opts.quality : null;
    const preset = presetName && QUALITY_MODES[presetName] ? QUALITY_MODES[presetName] : QUALITY_MODES.medium;
    this.qualityName = presetName || 'custom';
    this.videoWidth  = opts.videoWidth  || preset.width;
    this.videoHeight = opts.videoHeight || preset.height;
    this.videoFps    = opts.videoFps    || preset.fps;
    this.videoKbps   = (opts.videoKbps != null ? opts.videoKbps : preset.videoKbps);
    this.audioKbps   = opts.audioKbps   || preset.audioKbps;
    this.budgetKbps  = opts.budgetKbps  || this.videoKbps || 60;
    this.audioOnly   = !!opts.audioOnly || !!preset.videoOff;
    this.micOff      = !!opts.micOff;
    this._micGain    = clampMicGain(opts.micGain);
    this._micProc    = normMicProc(opts.micProcessing);
    this.opusTweak   = (opts.opusTweak != null ? !!opts.opusTweak : !!preset.opusTweak);
    this.videosEl = opts.videosEl;
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
      const translated = translateLegacyError(msg);
      try { userOnError(translated); } catch (_) {}
    };
    this.cbFileProgress = opts.onFileProgress || function () {};
    this.cbFileReceived = opts.onFileReceived || function () {};
    this.cbWhiteboard = opts.onWhiteboard || function () {};
    this.cbRecordingReady = opts.onRecordingReady || null;

    this._wbStrokes = [];
    this._annotStrokes = [];

    this.ws = null;
    this.wsBackoff = RECONNECT_BACKOFF_MIN;
    this.peerId = null;
    this.iceServers = [];
    this.peers = Object.create(null);
    this.localStream = null;
    this.statsTimer = null;
    this.stopped = false;
    this.networkListenerInstalled = false;

    this.e2eePassphrase = opts.e2eePassphrase || '';
    this.e2eeActive = false;

    this.cameraTrack = null;
    this.screenStream = null;
    this.screenSharing = false;

    this.recorder = null;
    this.recorderChunks = [];
    this.recordingStarted = 0;

    this.frostCanvas = null;
    this.frostVideoEl = null;
    this.frostRAF = 0;
    this.frostActive = false;

    this.audioFirstMode = !!opts.audioFirstMode;
    this.poorNetworkSince = 0;
    this.autoVideoOff = false;

    this.av1Probe = { lowFpsSince: 0, downgraded: false };

    this.callStartedAt = 0;
    this.cumBytesSent = 0;
    this.cumBytesRecv = 0;
    this.lastCodec = '';
    this.lastConnType = 'direct';

    this._origOpts = opts;
    this._userInitiatedHangup = false;

    this._peerAudioMonitors = {};
    this._activeSpeaker = null;

    this._localSpeakingMon = null;

    this._fileTxOffsets = {};
  }

  Call.prototype.start = async function () {
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
        delete video.deviceId;
        video.facingMode = { ideal: this.deviceIds.camera };
      }
    }
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
      throw new Error(lastError
        ? humanizeGumError(lastError)
        : 'No usable camera or microphone on this computer. Connect a device and try again.');
    }
    this.cameraTrack = this.localStream.getVideoTracks()[0] || null;
    if (!this.cameraTrack) this.audioOnly = true;
    if (this.degradedNote) {
      try { this.cbState({ type: 'devices-degraded', note: this.degradedNote }); } catch (_) {}
    }
    this.callStartedAt = Date.now();

    try {
      this._buildMicPipeline(this.localStream.getAudioTracks()[0]);
    } catch (e) {
      console.warn('[panel:vc] mic gain pipeline failed (no gain control): ' + e.message);
    }

    this.attachLocalPreview();

    this._startLocalSpeakingMonitor();

    await this.openSignaling();

    this.statsTimer = setInterval(() => this.collectStats(), STATS_INTERVAL_MS);

    if (!this.networkListenerInstalled) {
      this.networkListenerInstalled = true;
      this._netHandler = () => this.handleNetworkChange();
      window.addEventListener('online', this._netHandler);
      if (navigator.connection && navigator.connection.addEventListener) {
        navigator.connection.addEventListener('change', this._netHandler);
      }
    }

    this._startBgKeepalive();

    if (this._visHandler) {
      try { document.removeEventListener('visibilitychange', this._visHandler); } catch (_) {}
    }
    this._visHandler = () => {
      if (document.visibilityState === 'visible' && !this.stopped) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
          if (this._lastWsMessageAt && Date.now() - this._lastWsMessageAt > 70000) {
            console.warn('[panel:vc] WS silent >70s, forcing reconnect');
            try { this.ws.close(4000, 'silent-death'); } catch (_) {}
            this.reopenSignaling();
          }
        } else if (this.ws && this.ws.readyState >= WebSocket.CLOSING) {
          this.reopenSignaling();
        }
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
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
          try { this.ws.send(JSON.stringify({ type: 'ping' })); } catch (_) {}
        }
        if (this._pendingReconnect && !this.stopped) {
          this._pendingReconnect = false;
          this.reopenSignaling();
        }
      };
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

    const durationS = this.callStartedAt ? Math.round((Date.now() - this.callStartedAt) / 1000) : 0;
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
      const wsPath = this.opts.guestMode ? '/ws/videocall-guest' : '/ws/videocall';
      const roomParam = this.opts.guestMode ? '' : ('&room_id=' + encodeURIComponent(this.roomId));
      let authParam = '';
      if (!this.opts.guestMode && this.ticketProvider) {
        try {
          const ticket = await this.ticketProvider();
          if (ticket) authParam = '?ticket=' + encodeURIComponent(ticket);
        } catch (_) {}
      }
      if (!authParam) authParam = '?token=' + encodeURIComponent(this.token);
      const clientParam = (!this.opts.guestMode && this.clientId)
        ? ('&client_id=' + encodeURIComponent(this.clientId)) : '';
      const resumeParam = this._resuming ? '&resume=1' : '';
      const url = proto + '//' + location.host + wsPath + authParam + roomParam + clientParam + resumeParam;
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
        this._wsRetries = (this._wsRetries || 0) + 1;
        if (this._wsRetries > 8) {
          this.cbState({ type: 'reconnect-gave-up' });
          return;
        }
        this.cbState({ type: 'reconnecting' });
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

  Call.prototype._teardownPeerMonitor = function (id) {
    if (!this._peerAudioMonitors || !this._peerAudioMonitors[id]) return;
    const m = this._peerAudioMonitors[id];
    try { if (m.raf) cancelAnimationFrame(m.raf); } catch (_) {}
    try { if (m.ctx && m.ctx.state !== 'closed') m.ctx.close().catch(() => {}); } catch (_) {}
    delete this._peerAudioMonitors[id];
  };

  Call.prototype.reopenSignaling = function () {
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
      const ttlSec = (payload.turn.ttl > 0) ? payload.turn.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } else {
      this.iceServers = [{ urls: ['stun:stun.l.google.com:19302'] }];
    }
    const live = new Set((payload.peers || []).map(p => p.id));
    for (const id in this.peers) {
      if (id === this.peerId || live.has(id)) continue;
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
    for (const p of payload.peers || []) {
      this.ensurePeer(p.id, p.user, true, p.client_id);
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
      for (const id in this.peers) {
        try { this.peers[id].pc.setConfiguration({ iceServers: this.iceServers }); }
        catch (_) {}
      }
      this._turnRetries = 0;
      console.log('[panel:vc] TURN credentials refreshed (peers=' + Object.keys(this.peers).length + ')');
      const ttlSec = (tu.ttl > 0) ? tu.ttl : 3600;
      this._scheduleTURNRefresh(Math.floor(ttlSec * 0.8));
    } catch (e) {
      this._turnRetries = (this._turnRetries || 0) + 1;
      if (this._turnRetries <= 5) {
        const backoff = Math.min(60 * Math.pow(2, this._turnRetries - 1), 600);
        console.warn('[panel:vc] TURN refresh failed (' + this._turnRetries + '/5): ' + e.message + ', retrying in ' + backoff + 's');
        this._scheduleTURNRefresh(backoff);
      } else {
        console.error('[panel:vc] TURN refresh exhausted after 5 attempts');
        try { this.cbState({ type: 'turn-exhausted' }); } catch (_) {}
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
          this.ensurePeer(msg.from, info.user || 'Peer', false, info.client_id);
        }
        break;
      case 'peer-left':
        console.log('[panel:vc] peer-left ' + (msg.from ? msg.from.slice(-6) : '-'));
        if (this.peers[msg.from]) {
          this.peers[msg.from].close();
          delete this.peers[msg.from];
          this._notifyPeerCount();
        }
        this._pruneOrphanTiles();
        this._teardownPeerMonitor(msg.from);
        if (this._captionClearTimers && this._captionClearTimers[msg.from]) {
          clearTimeout(this._captionClearTimers[msg.from]);
          delete this._captionClearTimers[msg.from];
        }
        break;
      case 'reset':
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
        const data = safeParse(msg.payload) || {};
        this.cbChat({ from: msg.from, text: data.text || '', ts: Date.now() });
        break;
      }
      case 'state': {
        const data = safeParse(msg.payload) || {};
        this.cbState({ type: 'peer-state', from: msg.from, state: data });
        break;
      }
      case 'owner-mute': {
        if (msg.to === this.peerId) {
          this._applyOwnerMute(true, msg.from);
        }
        this.cbState({ type: 'owner-action', action: 'mute', target: msg.to, by: msg.from });
        break;
      }
      case 'owner-unmute': {
        if (msg.to === this.peerId) {
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
        this._userInitiatedHangup = true;
        this.cbError(msg.error || translateLegacyError(msg.error));
        if (msg.type === 'error-full') {
          this.cbState({ type: 'room-full' });
        } else if (msg.type === 'error-conflict') {
          this.cbState({ type: 'user-conflict' });
        }
        break;
      case 'kicked':
        this._userInitiatedHangup = true;
        this.cbState({ type: 'kicked', reason: msg.error || 'Removed from the call' });
        break;
    }
  };

  Call.prototype._rekeyPeer = function (oldId, newId) {
    if (oldId === newId) return;
    const peer = this.peers[oldId];
    if (!peer) return;
    delete this.peers[oldId];
    peer.remoteId = newId;
    this.peers[newId] = peer;
    if (this.videosEl) {
      const attrs = ['data-vc-peer-tile', 'data-vc-peer-avatar', 'data-vc-peer-name',
                     'data-vc-peer-sub', 'data-vc-peer', 'data-vc-peer-caption'];
      for (let i = 0; i < attrs.length; i++) {
        const el = this.videosEl.querySelector('[' + attrs[i] + '="' + oldId + '"]');
        if (el) el.setAttribute(attrs[i], newId);
      }
    }
    if (this._peerAudioMonitors && this._peerAudioMonitors[oldId]) {
      const mon = this._peerAudioMonitors[oldId];
      mon.peerId = newId;
      this._peerAudioMonitors[newId] = mon;
      delete this._peerAudioMonitors[oldId];
    }
    if (this._captionClearTimers && this._captionClearTimers[oldId]) {
      this._captionClearTimers[newId] = this._captionClearTimers[oldId];
      delete this._captionClearTimers[oldId];
    }
    if (this._activeSpeaker === oldId) this._activeSpeaker = newId;
    if (this._subtitlesRequestedById === oldId) this._subtitlesRequestedById = newId;
    if (this._fileTxOffsets) {
      for (const fid in this._fileTxOffsets) {
        const o = this._fileTxOffsets[fid];
        if (o && o.peerID === oldId) o.peerID = newId;
      }
    }
  };

  Call.prototype.ensurePeer = function (remoteId, user, initiator, clientId) {
    if (this.peers[remoteId]) return this.peers[remoteId];
    if (clientId) {
      for (const id of Object.keys(this.peers)) {
        if (id === remoteId) continue;
        const old = this.peers[id];
        if (!old || old.remoteClientId !== clientId) continue;
        const cs = old.pc && old.pc.connectionState;
        if (cs === 'connected') {
          this._rekeyPeer(id, remoteId);
          const adopted = this.peers[remoteId];
          adopted.polite = this.peerId < remoteId;
          console.log('[panel:vc] adopted ' + id.slice(-6) + '->' + remoteId.slice(-6) + ' (connected, media preserved, no renegotiation)');
          this._notifyPeerCount();
          return adopted;
        }
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
      polite: this.peerId < remoteId,
    });
    this.peers[remoteId] = peer;
    this._notifyPeerCount();
    return peer;
  };
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
    this.ensurePeer(remoteId, user, !!warn, clientId);
  };

  Call.prototype._notifyPeerCount = function () {
    try {
      const list = [];
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

  Call.prototype._preservePipTile = function (v, tile, cid) {
    try {
      v.removeAttribute('data-vc-peer');
      v.setAttribute('data-vc-pip-detached', cid);
      if (tile) {
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
    for (const id in this.peers) {
      try { this.peers[id].pc.restartIce(); } catch (_) {}
    }
  };

  Call.prototype.collectStats = async function () {
    const agg = {
      ts: Date.now(),
      bytesSentPerSec: 0, bytesRecvPerSec: 0,
      packetsLost: 0, rtt: 0,
      codec: '', resolution: '', framerate: 0,
      connectionType: 'direct',
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
    this.cumBytesSent += agg.bytesSentPerSec;
    this.cumBytesRecv += agg.bytesRecvPerSec;
    if (agg.codec) this.lastCodec = agg.codec;
    if (agg.connectionType) this.lastConnType = agg.connectionType;
    this.applyAdaptiveDegrade(agg);
    let topPeer = null, topLevel = 12;
    for (const id in this._peerAudioMonitors) {
      const lvl = this._peerAudioMonitors[id].level;
      if (lvl > topLevel) { topLevel = lvl; topPeer = id; }
    }
    if (topPeer !== this._activeSpeaker) {
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

  Call.prototype.attachLocalPreview = function () {
    if (!this.videosEl) return;
    let v = this.videosEl.querySelector('[data-vc-local="1"]');
    if (!v) {
      v = document.createElement('video');
      v.setAttribute('data-vc-local', '1');
      v.autoplay = true; v.muted = true; v.playsInline = true;
      v.style.cssText = 'background:#000;';
      this.videosEl.appendChild(v);
    }
    v.srcObject = this.localStream;
  };

  Call.prototype.setMuted = function (muted) {
    if (!this.localStream) return false;
    this._userMutedExplicit = !!muted;
    this.muted = !!muted;
    for (const t of this.localStream.getAudioTracks()) t.enabled = !muted;
    if (this._micGainRawTrack) this._micGainRawTrack.enabled = !muted;
    this.send({ type: 'state', payload: jsonRaw({ mic: muted ? 'off' : 'on' }) });
    if (!muted && this._subtitlesActive) {
      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
        if (window.PANEL_DEBUG) console.log('[panel:vc] unmute: kick STT restart');
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
    if (!off && this.autoVideoOff) {
      this.autoVideoOff = false;
    }
    this.send({ type: 'state', payload: jsonRaw({ cam: off ? 'off' : 'on' }) });
    return !!off;
  };

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
    if (this.cameraTrack && this.cameraTrack !== newTrack) {
      try { this.cameraTrack.stop(); } catch (_) {}
    }
    this.cameraTrack = newTrack;
    this.deviceIds.camera = deviceId;
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

  Call.prototype._replaceMicRaw = async function (newTrack) {
    newTrack.enabled = !this._userMutedExplicit;
    let old;
    if (this._micGainCtx && this._micGainNode) {
      old = this._micGainRawTrack;
      this._attachMicSource(newTrack);
    } else {
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
    if (this._subtitlesActive && this._restartSubtitlesLocalOnly) {
      if (this._subtitlesRestartGuard) { clearTimeout(this._subtitlesRestartGuard); this._subtitlesRestartGuard = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      this._restartSubtitlesLocalOnly(this._subtitlesBackend || 'web-speech', this._subtitlesLang);
    }
  };

  Call.prototype.getMicLevel = function () {
    if (!this._micAnalyser) return null;
    return readMicLevel(this._micAnalyser, this._micLevelBuf, this._micLimiter);
  };

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

  Call.prototype.applyQualityProfile = async function (profileOrName) {
    let p, name;
    if (typeof profileOrName === 'string') {
      name = profileOrName;
      p = QUALITY_MODES[name];
      if (!p) return null;
      p = Object.assign({}, p);
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
    p.videoKbps = p.videoOff ? 0 : Math.max(BUDGET_FLOOR_KBPS, Math.min(BUDGET_CEILING_KBPS, p.videoKbps));
    p.audioKbps = Math.max(6, Math.min(128, p.audioKbps));
    this.qualityName = name;
    this.videoWidth  = p.width  || this.videoWidth;
    this.videoHeight = p.height || this.videoHeight;
    this.videoFps    = p.fps    || this.videoFps;
    this.videoKbps   = p.videoKbps;
    this.audioKbps   = p.audioKbps;
    this.budgetKbps  = p.videoKbps;
    const wantsAudioOnly = !!p.videoOff;
    if (wantsAudioOnly !== this.audioOnly) {
      this.audioOnly = wantsAudioOnly;
      if (wantsAudioOnly) {
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
    if (!wantsAudioOnly && this.cameraTrack && this.cameraTrack.applyConstraints) {
      try {
        await this.cameraTrack.applyConstraints({
          width:     { ideal: p.width  },
          height:    { ideal: p.height },
          frameRate: { ideal: p.fps    },
        });
      } catch (e) { /* device might not support; non-fatal */ }
    }
    const newOpusTweak = (p.opusTweak != null ? !!p.opusTweak : this.opusTweak);
    const opusChanged = newOpusTweak !== this.opusTweak;
    this.opusTweak = newOpusTweak;
    for (const id in this.peers) this.peers[id].applyBitrates(p.videoKbps, p.audioKbps);
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
    if (!dcSent) {
      for (const id in this.peers) {
        this.send({ type: 'chat', to: id, payload: jsonRaw({ text: text }) });
      }
    }
  };

  Call.prototype.setScreenShare = async function (on) {
    if (on && !this.screenSharing) {
      try {
        this.screenStream = await navigator.mediaDevices.getDisplayMedia({
          video: { frameRate: { ideal: 15 } },
          audio: false,
        });
      } catch (e) {
        this.cbError('screen share: ' + e.message);
        return false;
      }
      const screenTrack = this.screenStream.getVideoTracks()[0];
      screenTrack.addEventListener('ended', () => { this.setScreenShare(false); });
      await this.swapVideoSenderTrack(screenTrack);
      this.screenSharing = true;
      this.send({ type: 'state', payload: jsonRaw({ screen: 'on' }) });
      this.cbState({ type: 'screen-share', on: true });
      return true;
    }
    if (!on && this.screenSharing) {
      let camTrack = this.cameraTrack;
      if (!camTrack || camTrack.readyState !== 'live') {
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
          try { await Promise.resolve(sender.replaceTrack(newTrack)); } catch (_) {}
        }
      }
    }
    if (this.videosEl) {
      const v = this.videosEl.querySelector('[data-vc-local="1"]');
      if (v && this.localStream) {
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
    const canvas = document.createElement('canvas');
    canvas.width = 1280; canvas.height = 720;
    const ctx = canvas.getContext('2d');
    const draw = () => {
      if (this.stopped || !this.recorder || this.recorder.state === 'inactive') return;
      ctx.fillStyle = '#000';
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      const remotes = this.videosEl.querySelectorAll('video[data-vc-peer]');
      if (remotes.length) {
        const v = remotes[0];
        if (v.videoWidth) {
          ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
        }
      }
      const local = this.videosEl.querySelector('video[data-vc-local="1"]');
      if (local && local.videoWidth) {
        const w = canvas.width * 0.25, h = (w * local.videoHeight) / local.videoWidth;
        ctx.drawImage(local, canvas.width - w - 16, canvas.height - h - 16, w, h);
      }
      requestAnimationFrame(draw);
    };
    let canvasStream;
    try { canvasStream = canvas.captureStream(20); } catch (_) { return false; }
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

  Call.prototype.sendFile = async function (file) {
    const peers = Object.values(this.peers);
    if (!peers.length) return;
    for (const peer of peers) {
      await peer.sendFile(file);
    }
  };

  Call.WB_STROKE_CAP = 4000;

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
    this._wbRecord(ev);
    for (const id in this.peers) {
      this.peers[id].sendWhiteboard(ev);
    }
  };

  Call.prototype.updatePeerCaption = function (peerId, text, isLocal) {
    if (!this.videosEl) return;
    if (this._showCaptions === false) {
      try {
        const localCap = this.videosEl.querySelector('[data-vc-local-caption]');
        if (localCap) localCap.style.display = 'none';
        const allPeerCaps = this.videosEl.querySelectorAll('[data-vc-peer-caption]');
        allPeerCaps.forEach(el => el.style.display = 'none');
      } catch (_) {}
      return;
    }
    if (isLocal || peerId === 'me') {
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

  Call.prototype.setShowCaptions = function (show) {
    this._showCaptions = !!show;
    if (!this._showCaptions) {
      try {
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
    this.cbState({ type: 'subtitles-show', show: this._showCaptions });
  };

  Call.prototype._broadcastSubtitlesRequest = function (on, lang) {
    if (!this.peers) return { sent: 0, pending: 0, total: 0 };
    const payload = JSON.stringify({
      type: 'subtitles-request',
      on: !!on,
      lang: lang || 'en-US',
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
      }
    }
    console.log('[panel:vc] subtitles-request broadcast: on=' + on +
                ' sent=' + sent + ' pending=' + pending + ' total=' + total);
    return { sent, pending, total };
  };

  Call.prototype._assignSubsHandle = function (p) {
    Promise.resolve(p).then((h) => {
      if (!this._subtitlesActive) {
        try { h && h.stop && h.stop(); } catch (_) {}
        return;
      }
      this._subtitlesHandle = h;
      if (!h && this._sendSubsStatus) this._sendSubsStatus(false, 'start-failed', this._subtitlesBackend);
    });
  };

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

      this._subtitlesRestartCount = 0;
      this._subtitlesLastSuccessAt = Date.now();
      if (this._subtitlesRestartGuard) {
        clearTimeout(this._subtitlesRestartGuard);
        this._subtitlesRestartGuard = null;
      }
      const startWithBackend = (backend) => {
        const sttStream = this.getMicStreamForSTT ? this.getMicStreamForSTT() : this.localStream;
        return window.PanelSTT.start({
          backend,
          continuous: true,
          interimResults: true,
          lang: opts.lang || 'en-US',
          token: opts.token || this.token || (window.__PANELTOKEN__ || null),
          stream: sttStream,
          prompt: opts.prompt || '',
          idleMs: 20000,
          onReady: () => {
            this._subtitlesLastSuccessAt = Date.now();
            if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
            this._sendSubsStatus(true, '', this._subtitlesBackend);
          },
          onPartial: (p) => {
            this._subtitlesLastSuccessAt = Date.now();
            this._subtitlesRestartCount = 0;
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
              this._subtitlesActive = false;
              try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
              this._subtitlesHandle = null;
              setTimeout(() => {
                this._subtitlesActive = true;
                this._subtitlesRestartCount = 0;
                this._subtitlesLastSuccessAt = Date.now();
                this._subtitlesBackend = 'web-speech';
                this._assignSubsHandle(startWithBackend('web-speech'));
                this.cbState({ type: 'subtitles-backend', backend: 'web-speech' });
              }, 250);
            } else if (e.fatal) {
              this.cbError('subtitles: ' + (e.message || e.code));
              if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
              this._sendSubsStatus(false, e.code || 'fatal', this._subtitlesBackend);
            }
          },
          onEnd: (e) => {
            if (!this._subtitlesActive) return;
            const isMuted = this.muted || (this.localStream && this.localStream.getAudioTracks().some(t => !t.enabled));
            if (isMuted) {
              if (window.PANEL_DEBUG) console.log('[panel:vc] subtitles onEnd while muted, pausing restart');
              if (this._subtitlesRestartGuard) return;
              this._subtitlesHandle = null;
              this._subtitlesRestartGuard = setTimeout(() => {
                this._subtitlesRestartGuard = null;
                if (this._subtitlesActive) this._assignSubsHandle(startWithBackend(backend));
              }, 5000);
              return;
            }
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
      this._subtitlesLang = opts.lang || 'en-US';
      this._subtitlesBackend = 'web-speech';
      this._restartSubtitlesLocalOnly = (backend, lang) => {
        opts.lang = lang;
        this._subtitlesBackend = backend;
        this._assignSubsHandle(startWithBackend(backend));
      };
      this.cbState({ type: 'subtitles-state', active: true, source: opts._silentPropagate ? 'remote' : 'local' });
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
        if (this._subtitlesRequestedById) {
          if (this._subsAckTimer) clearTimeout(this._subsAckTimer);
          this._subsAckTimer = setTimeout(() => {
            this._subsAckTimer = null;
            this._sendSubsStatus(false, 'connect-timeout', this._subtitlesBackend);
          }, 6000);
        }
      })();
      if (!opts._silentPropagate) {
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
      if (this._captionTrailingTimer) { clearTimeout(this._captionTrailingTimer); this._captionTrailingTimer = null; }
      this._captionTrailingPayload = null;
      this._subtitlesRequestedById = null;
      this._subtitlesAckSent = false;
      if (this._subsAckTimer) { clearTimeout(this._subsAckTimer); this._subsAckTimer = null; }
      try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
      this._subtitlesHandle = null;
      if (!opts._silentPropagate) {
        const r = this._broadcastSubtitlesRequest(false, this._subtitlesLang || 'en-US');
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

  Call.prototype.getMicStreamForSTT = function () {
    const raw = this._micGainRawTrack;
    if (raw && raw.readyState === 'live') {
      try { return new MediaStream([raw]); } catch (_) {}
    }
    return this.localStream;
  };

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

  Call.prototype.setMicGain = function (v) {
    const g = clampMicGain(v);
    this._micGain = g;
    if (this._micGainNode && this._micGainCtx) {
      try { this._micGainNode.gain.setTargetAtTime(g, this._micGainCtx.currentTime, 0.015); }
      catch (_) { try { this._micGainNode.gain.value = g; } catch (_) {} }
      this.cbState({ type: 'mic-gain', value: g });
    } else {
      this.cbState({ type: 'mic-gain', value: g, unavailable: true });
    }
    return g;
  };

  Call.prototype.changeSubtitlesLang = function (lang) {
    if (!this._subtitlesActive) {
      this._subtitlesLang = lang || 'en-US';
      return false;
    }
    this._subtitlesLang = lang || 'en-US';
    const backend = this._subtitlesBackend || 'web-speech';
    try { this._subtitlesHandle && this._subtitlesHandle.stop && this._subtitlesHandle.stop(); } catch (_) {}
    this._subtitlesHandle = null;
    this._subtitlesRestartCount = 0;
    this._subtitlesLastSuccessAt = Date.now();
    setTimeout(() => {
      if (this._subtitlesActive && this._restartSubtitlesLocalOnly) {
        this._restartSubtitlesLocalOnly(backend, this._subtitlesLang);
      }
    }, 100);
    return true;
  };

  Call.prototype.applyAdaptiveDegrade = function (stats) {
    const now = Date.now();
    if (this.audioFirstMode) {
      const lossPct = stats.packetsLost > 50 ? 100 : 0;
      if (lossPct > 10 || (stats.rtt > 500)) {
        if (!this.poorNetworkSince) this.poorNetworkSince = now;
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
    if (!this.av1Probe.downgraded && stats.codec && /AV1/i.test(stats.codec) && stats.framerate && stats.framerate < 20) {
      if (!this.av1Probe.lowFpsSince) this.av1Probe.lowFpsSince = now;
      if (now - this.av1Probe.lowFpsSince > 5000) {
        this.av1Probe.downgraded = true;
        for (const id in this.peers) {
          this.peers[id].forcePreferredCodec('video/VP9');
        }
        this.cbState({ type: 'codec-downgrade', from: 'AV1', to: 'VP9' });
      }
    } else if (stats.framerate >= 20) {
      this.av1Probe.lowFpsSince = 0;
    }
  };

  function PeerConn(opt) {
    this.call = opt.call;
    this.remoteId = opt.remoteId;
    this.remoteUser = opt.remoteUser;
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

    for (const t of this.call.localStream.getTracks()) {
      this.pc.addTrack(t, this.call.localStream);
    }

    this.applyCodecPreferences();
    this.applyBitrates(this.call.videoKbps, this.call.audioKbps);

    if (this.call.e2eePassphrase && window.PanelVideoCallE2EE) {
      window.PanelVideoCallE2EE.setup(this.pc, this.call.e2eePassphrase, this.call.roomId, {
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
    this._negotiationCycleEnd = 0;
    this._negotiationFiredInCycle = false;
    this.pc.onsignalingstatechange = () => {
      const st = this.pc.signalingState;
      console.log('[panel:vc] signalingState peer=' + this.remoteId.slice(-6) + ' = ' + st);
      if (st === 'stable') {
        this._negotiationCycleEnd = Date.now();
        this._negotiationFiredInCycle = false;
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
      if (this.pc.signalingState !== 'stable') {
        console.warn('[panel:vc] onnegotiationneeded ignored — state=' + this.pc.signalingState + ' peer=' + this.remoteId.slice(-6));
        return;
      }
      if (this._negotiationFiredInCycle) {
        console.log('[panel:vc] onnegotiationneeded coalesced (already-fired-this-cycle) peer=' + this.remoteId.slice(-6));
        return;
      }
      const now = Date.now();
      if (this._negotiationCycleEnd && now - this._negotiationCycleEnd < 500) {
        console.log('[panel:vc] onnegotiationneeded coalesced (cooldown post-stable) peer=' + this.remoteId.slice(-6));
        return;
      }
      if (now - (this._lastNegotiationAt || 0) < 200) {
        console.log('[panel:vc] onnegotiationneeded coalesced (throttled) peer=' + this.remoteId.slice(-6));
        return;
      }
      this._lastNegotiationAt = now;
      this._negotiationFiredInCycle = true;
      console.log('[panel:vc] onnegotiationneeded FIRE peer=' + this.remoteId.slice(-6) + ' signalingState=' + this.pc.signalingState);
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
          this._negotiationFiredInCycle = false;
          return;
        }
        await this.pc.setLocalDescription(offer);
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
      this.dc = this.pc.createDataChannel('panel-chat', { ordered: true });
      this.setupDataChannel(this.dc);
      this.dcFiles = this.pc.createDataChannel('panel-files', { ordered: true });
      this.setupFilesChannel(this.dcFiles);
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

    this._fileRx = {
      incoming: new Map(),
      expecting: null,
    };
  }

  PeerConn.prototype.setupDataChannel = function (dc) {
    try { dc.binaryType = 'arraybuffer'; } catch (_) {}
    const sendSubsReqIfActive = () => {
      if (!this.call || !this.call._subtitlesActive) return;
      try {
        dc.send(JSON.stringify({
          type: 'subtitles-request',
          on: true,
          lang: this.call._subtitlesLang || 'en-US',
          requestedBy: this.call.displayName || 'me',
          ts: Date.now(),
        }));
        console.log('[panel:vc] subtitles-request sent (DC open) → peer=' + this.remoteId.slice(-6));
      } catch (e) { console.warn('[panel:vc] subs req send fail: ' + e.message); }
    };
    if (dc.readyState === 'open') {
      sendSubsReqIfActive();
    } else {
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
      const safeText = (s) => (typeof s === 'string') ? s.slice(0, 8000) : '';
      if (payload.type === 'chat') {
        this.call.cbChat({ from: this.remoteId, text: safeText(payload.text), ts: Date.now() });
      } else if (payload.type === 'caption') {
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
        this.call.updatePeerCaption(this.remoteId, captionText);
        if (payload.final) {
          clearTimeout(this._captionClearTimer);
          this._captionClearTimer = setTimeout(() => this.call.updatePeerCaption(this.remoteId, ''), 3500);
        }
      } else if (payload.type === 'subtitles-request') {
        const requesterLabel = payload.requestedBy || ('peer ' + this.remoteId.slice(-4));
        console.log('[panel:vc] subtitles-request received: on=' + payload.on +
                    ' from=' + this.remoteId.slice(-6) + ' by=' + requesterLabel +
                    ' lang=' + (payload.lang || 'en-US'));
        if (typeof payload.on !== 'boolean') return;
        this.call.cbState({
          type: 'subtitles-requested',
          from: this.remoteId,
          requestedBy: requesterLabel,
          on: !!payload.on,
          lang: payload.lang || 'en-US',
        });
        if (payload.on) {
          this.call._subtitlesRequestedById = this.remoteId;
          this.call._subtitlesAckSent = false;
          if (this.call._subtitlesActive) {
            console.log('[panel:vc] STT already on, skipping idempotent request');
            this.call._sendSubsStatus(true, '', this.call._subtitlesBackend);
            return;
          }
          const attemptActivate = (retries) => {
            if (!window.PanelSTT) {
              if (retries > 0) {
                console.log('[panel:vc] PanelSTT not loaded, retrying in 500ms (' + retries + ' left)');
                setTimeout(() => attemptActivate(retries - 1), 500);
                return;
              }
              console.warn('[panel:vc] PanelSTT unavailable, peer ' +
                          requesterLabel + ' enabled STT but I cannot transcribe my voice');
              this.call.cbError('Transcription: ' + requesterLabel +
                                ' turned it on but your browser has no STT loaded');
              try { this.dc.send(JSON.stringify({ type: 'subtitles-status', ok: false, reason: 'no-stt-module' })); } catch (_) {}
              return;
            }
            try {
              const ok = this.call.setSubtitles(true, {
                lang: payload.lang || 'en-US',
                _silentPropagate: true,
              });
              console.log('[panel:vc] STT remote-activated → ' + (ok ? 'OK' : 'FAIL'));
            } catch (e) {
              console.error('[panel:vc] STT remote-activate error: ' + e.message);
            }
          };
          attemptActivate(5);
        }
      } else if (payload.type === 'subtitles-status' || payload.type === 'subtitles-ack') {
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

  PeerConn.prototype.setupFilesChannel = function (dc) {
    dc.binaryType = 'arraybuffer';
    dc.onmessage = (ev) => {
      const rx = this._fileRx;
      if (typeof ev.data === 'string') {
        let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; }
        if (msg.type === 'file-start') {
          if (!rx.incoming.has(msg.id)) {
            rx.incoming.set(msg.id, { meta: msg, chunks: [], received: 0 });
          }
          rx.expecting = msg.id;
          this.call.cbFileProgress({ id: msg.id, name: msg.name, size: msg.size, received: rx.incoming.get(msg.id).received, direction: 'in', from: this.remoteId });
        } else if (msg.type === 'file-resume?') {
          const x = rx.incoming.get(msg.id);
          const offset = x ? x.received : 0;
          try { dc.send(JSON.stringify({ type: 'file-resume', id: msg.id, offset })); } catch (_) {}
        } else if (msg.type === 'file-resume') {
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
    this.call._fileTxOffsets[id] = { offset: 0, file, peerID: this.remoteId, resumeOffset: 0 };
    if (resumeId) {
      try { this.dcFiles.send(JSON.stringify({ type: 'file-resume?', id })); } catch (_) {}
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

  PeerConn.prototype.setupWhiteboardChannel = function (dc) {
    const sendSnapshot = () => {
      if (!this.call) return;
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
        const surface = msg.surface === 'screen' ? 'screen' : 'board';
        const strokes = Array.isArray(msg.strokes) ? msg.strokes : [];
        for (const s of strokes) {
          if (!s._from) s._from = this.remoteId;
          this.call._wbRecord({ type: 'wb-line', surface: surface, from: s.from, to: s.to, color: s.color, width: s.width, alpha: s.alpha, _from: s._from });
        }
        this.call.cbWhiteboard(msg);
        return;
      }
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
      this.tryReorderCodecs(CODEC_PREF);
      return;
    }
    const explicit = 'video/' + this.call.codecPref;
    this.tryReorderCodecs([explicit].concat(CODEC_PREF.filter(c => c !== explicit)));
  };

  PeerConn.prototype.forcePreferredCodec = function (mime) {
    this.tryReorderCodecs([mime].concat(CODEC_PREF.filter(c => c !== mime)));
  };

  PeerConn.prototype.tryReorderCodecs = function (prefOrder) {
    if (!RTCRtpReceiver.getCapabilities) return;
    const caps = RTCRtpReceiver.getCapabilities('video');
    if (!caps || !caps.codecs) return;
    const sorted = [];
    for (const want of prefOrder) {
      for (const c of caps.codecs) if (c.mimeType === want) sorted.push(c);
    }
    for (const c of caps.codecs) if (!sorted.includes(c)) sorted.push(c);
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
      sender.setParameters(params).catch(e => {
        console.warn('[panel:vc] setParameters failed kind=' + (sender.track && sender.track.kind) + ': ' + e.message);
      });
    }
  };
  PeerConn.prototype.applyBudget = function (kbps) {
    this.applyBitrates(kbps, this.call.audioKbps || 32);
  };

  PeerConn.prototype.onTrack = function (ev) {
    const stream = ev.streams && ev.streams[0];
    if (!stream) return;
    let v = this.call.videosEl && this.call.videosEl.querySelector('[data-vc-peer="' + this.remoteId + '"]');
    if (!v) {
      v = this.call._adoptDetachedPipTile(this.remoteClientId || '', this.remoteId) || null;
    }
    if (!v) {
      const tile = document.createElement('div');
      tile.setAttribute('data-vc-peer-tile', this.remoteId);
      tile.style.cssText = 'position:relative;display:flex;flex-direction:column;align-items:center;max-width:640px;width:100%;min-height:180px;';
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
      v.style.cssText = 'width:100%;height:auto;border-radius:8px;border:1px solid #1f2937;background:#000;position:relative;z-index:1;';
      tile.appendChild(v);
      const cap = document.createElement('div');
      cap.setAttribute('data-vc-peer-caption', this.remoteId);
      cap.style.cssText = 'position:absolute;left:8px;right:8px;bottom:8px;background:rgba(0,0,0,.75);color:#fff;font-size:15px;font-weight:500;padding:6px 12px;border-radius:8px;text-align:center;backdrop-filter:blur(6px);display:none;pointer-events:none;z-index:3;line-height:1.3;';
      tile.appendChild(cap);
      this.call.videosEl && this.call.videosEl.appendChild(tile);
    }
    v.srcObject = stream;
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
        if (this.polite && this._offerInFlight) {
          try { await this._offerInFlight; } catch (_) {}
        }
        const offerCollision = this.makingOffer || this.pc.signalingState !== 'stable';
        this.ignoreOffer = !this.polite && offerCollision;
        if (this.ignoreOffer) return;
        if (offerCollision && this.pc.signalingState === 'have-local-offer') {
          await this.pc.setLocalDescription({ type: 'rollback' });
          this._negotiationFiredInCycle = false;
          this._negotiationCycleEnd = Date.now();
        }
        await this.pc.setRemoteDescription(payload);
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
        if (!this.pc.remoteDescription || !this.pc.remoteDescription.type) {
          if (!this._pendingIce) this._pendingIce = [];
          if (this._pendingIce.length < 200) {
            this._pendingIce.push(payload);
          } else if (this._pendingIce.length === 200) {
            console.warn('[panel:vc] _pendingIce cap reached peer=' + this.remoteId.slice(-6));
            this._pendingIce.push(payload);
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
    if (this._videoElCleanup) {
      try { this._videoElCleanup(); } catch (_) {}
      this._videoElCleanup = null;
    }
    if (this._opusReneTimer) { clearTimeout(this._opusReneTimer); this._opusReneTimer = null; }
    if (this._iceStallTimer) { clearTimeout(this._iceStallTimer); this._iceStallTimer = null; }
    this._pendingIce = null;
    try { if (this.dc) this.dc.close(); } catch (_) {}
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
      const v = this.call.videosEl.querySelector('[data-vc-peer="' + this.remoteId + '"]');
      const tile = this.call.videosEl.querySelector('[data-vc-peer-tile="' + this.remoteId + '"]');
      const cid = this.remoteClientId || '';
      if (v && cid && !this.call._userInitiatedHangup &&
          document.pictureInPictureElement && document.pictureInPictureElement === v) {
        this.call._preservePipTile(v, tile, cid);
        const cap = this.call.videosEl.querySelector('[data-vc-peer-caption="' + this.remoteId + '"]');
        if (cap) { try { cap.remove(); } catch (_) {} }
        return;
      }
      if (v) { try { v.srcObject = null; } catch (_) {} }
      if (tile) {
        tile.remove();
      } else if (v) {
        v.remove();
      }
      const cap = this.call.videosEl.querySelector('[data-vc-peer-caption="' + this.remoteId + '"]');
      if (cap) { try { cap.remove(); } catch (_) {} }
    }
  };

  function jsonRaw(obj) {
    return obj;
  }

  function safeParse(v) {
    if (v == null) return null;
    if (typeof v === 'object') return v;
    try { return JSON.parse(v); } catch (_) { return null; }
  }

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
