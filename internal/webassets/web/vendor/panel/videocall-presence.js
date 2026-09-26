/* panel-videocall-presence — panel-wide "someone is calling" client.
 *
 * Opens a long-lived WS to /ws/videocall-presence and fires onIncoming when
 * the server pushes an `incoming-call` event. The user's main app handles
 * the UI (modal + ringtone + browser Notification).
 *
 * Public API:
 *   window.PanelPresence.connect({ token, onIncoming, onState })
 *   window.PanelPresence.disconnect()
 *   window.PanelPresence.playRing(durationMs)        // beeps via WebAudio (no asset)
 *   window.PanelPresence.stopRing()
 *
 * The ringtone is generated on the fly with WebAudio (two-tone, low volume)
 * so we don't need to vendor an .mp3 file. Total cost: ~3KB of code.
 */
/* PanelDevice: stable identity of the DEVICE (not the tab), so the ringer
 * policy can be set per device. The id lives in localStorage (survives
 * reloads and browser restarts, unlike the per-tab client_id in
 * sessionStorage); the label is derived from the UA so the owner can
 * recognise it in the list.
 */
(function () {
  'use strict';
  if (window.PanelDevice) return;
  const KEY = 'panel_device_id';

  function uuid() {
    try {
      if (crypto && crypto.randomUUID) return crypto.randomUUID().replace(/-/g, '');
    } catch (_) {}
    let out = '';
    for (let i = 0; i < 32; i++) out += Math.floor(Math.random() * 16).toString(16);
    return out;
  }

  function id() {
    try {
      let v = localStorage.getItem(KEY);
      // Same alphabet the server accepts (sanitizeDeviceID).
      if (!v || !/^[A-Za-z0-9_-]{8,64}$/.test(v)) {
        v = uuid();
        localStorage.setItem(KEY, v);
      }
      return v;
    } catch (_) {
      // localStorage blocked (private mode): no stable identity, so the
      // device is outside the policy and always rings.
      return '';
    }
  }

  function label() {
    const ua = navigator.userAgent || '';
    let os = 'Unknown';
    if (/Windows/i.test(ua)) os = 'Windows';
    else if (/iPhone|iPad|iPod/i.test(ua)) os = 'iOS';
    else if (/Mac OS X|Macintosh/i.test(ua)) os = 'Mac';
    else if (/Android/i.test(ua)) os = 'Android';
    else if (/Linux/i.test(ua)) os = 'Linux';
    let br = 'Browser';
    // Order matters: Edge/Opera also say "Chrome" and Chrome also says
    // "Safari", so test the most specific first.
    if (/Edg\//i.test(ua)) br = 'Edge';
    else if (/OPR\/|Opera/i.test(ua)) br = 'Opera';
    else if (/Firefox\//i.test(ua)) br = 'Firefox';
    else if (/Chrome\//i.test(ua)) br = 'Chrome';
    else if (/Safari\//i.test(ua)) br = 'Safari';
    let suffix = '';
    try {
      if (window.matchMedia && window.matchMedia('(display-mode: standalone)').matches) suffix = ' (app)';
    } catch (_) {}
    return os + ' — ' + br + suffix;
  }

  window.PanelDevice = { id, label, info: () => ({ id: id(), label: label() }) };
})();

(function () {
  'use strict';
  if (window.PanelPresence) return;

  let ws = null;
  let backoff = 1000;
  let stopped = false;
  let cbIncoming = function () {};
  let cbEvent = function () {};
  let cbState = function () {};
  let curToken = '';
  let curTicketProvider = null; // async function() -> ticket; preferred over token
  let audioCtx = null;
  let ringTimer = 0;
  let ringNodes = [];

  function connect(opts) {
    stopped = false;
    curToken = opts.token || '';
    // ticketProvider fetches a one-shot 60s ticket (HttpOnly cookie auth), so
    // the JWT never ends up in a URL or log.
    curTicketProvider = opts.ticketProvider || null;
    cbIncoming = opts.onIncoming || cbIncoming;
    // onEvent gets ALL presence events, including the control ones
    // (call-answered-elsewhere, call-ended) that cancel a ring in progress.
    cbEvent = opts.onEvent || cbEvent;
    cbState = opts.onState || cbState;
    open();
  }

  function disconnect() {
    stopped = true;
    if (ws) { try { ws.close(); } catch (_) {} ws = null; }
    stopRing();
  }

  async function open() {
    if (!curToken && !curTicketProvider) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Identify the device so the server applies its ringer policy. A device
    // without an id always rings: a failed localStorage must never lose a call.
    let devParam = '';
    try {
      const d = window.PanelDevice ? window.PanelDevice.info() : null;
      if (d && d.id) {
        devParam = '&device_id=' + encodeURIComponent(d.id) +
                   '&device_label=' + encodeURIComponent(d.label || '');
      }
    } catch (_) {}
    let url;
    if (curTicketProvider) {
      let ticket = '';
      try { ticket = await curTicketProvider(); } catch (_) {}
      if (!ticket) { schedReconnect(); return; }
      url = proto + '//' + location.host + '/ws/videocall-presence?ticket=' + encodeURIComponent(ticket) + devParam;
    } else {
      url = proto + '//' + location.host + '/ws/videocall-presence?token=' + encodeURIComponent(curToken) + devParam;
    }
    try { ws = new WebSocket(url); } catch (e) { schedReconnect(); return; }
    // Reset backoff only once the WS is stable (5s without close, or a first
    // message), so a server that handshakes and closes at once (bad token)
    // does not count as a good connection.
    let stableTimer = setTimeout(() => { backoff = 1000; }, 5000);
    ws.onopen = () => { cbState({ type: 'connected' }); };
    ws.onmessage = (ev) => {
      backoff = 1000;
      if (stableTimer) { clearTimeout(stableTimer); stableTimer = 0; }
      let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; }
      // Forward everything; cbIncoming stays separate for callers that only
      // want the ring.
      try { cbEvent(msg); } catch (_) {}
      if (msg.type === 'incoming-call') cbIncoming(msg);
    };
    ws.onerror = () => {};
    ws.onclose = () => {
      if (stableTimer) { clearTimeout(stableTimer); stableTimer = 0; }
      ws = null;
      cbState({ type: 'disconnected' });
      if (!stopped) schedReconnect();
    };
  }

  function schedReconnect() {
    setTimeout(() => { if (!stopped) open(); }, backoff);
    backoff = Math.min(backoff * 2, 30000);
  }

  // ---- Ringtone (WebAudio, no .mp3 asset) ----
  // Plays a soft two-tone ring pattern: 880Hz + 660Hz beep, 500ms on / 500ms
  // off, repeating until stopRing() or the durationMs timeout. Volume capped
  // at -12dBFS so it won't blast people who forgot to lower system volume.
  function ensureAudio() {
    if (!audioCtx) {
      try { audioCtx = new (window.AudioContext || window.webkitAudioContext)(); }
      catch (_) { return null; }
    }
    return audioCtx;
  }

  function playRing(durationMs) {
    stopRing();
    const ctx = ensureAudio();
    if (!ctx) return;
    // Some browsers require resume() after a user gesture. We try here; if
    // it's still suspended the playback silently fails — acceptable.
    if (ctx.state === 'suspended') { try { ctx.resume(); } catch (_) {} }
    const start = ctx.currentTime;
    const period = 1.0; // 1s = 0.5s on, 0.5s off
    const count = Math.ceil((durationMs || 20000) / 1000);
    for (let i = 0; i < count; i++) {
      const t0 = start + i * period;
      makeBeep(ctx, t0, t0 + 0.3, 880);
      makeBeep(ctx, t0 + 0.35, t0 + 0.55, 660);
    }
    ringTimer = setTimeout(stopRing, durationMs || 20000);
  }

  function makeBeep(ctx, t0, t1, freq) {
    const osc = ctx.createOscillator();
    osc.type = 'sine';
    osc.frequency.value = freq;
    const gain = ctx.createGain();
    // Cosine ramp to avoid clicks at on/off.
    gain.gain.setValueAtTime(0, t0);
    gain.gain.linearRampToValueAtTime(0.18, t0 + 0.02);
    gain.gain.linearRampToValueAtTime(0.18, t1 - 0.05);
    gain.gain.linearRampToValueAtTime(0, t1);
    osc.connect(gain).connect(ctx.destination);
    osc.start(t0);
    osc.stop(t1);
    ringNodes.push(osc);
  }

  function stopRing() {
    if (ringTimer) { clearTimeout(ringTimer); ringTimer = 0; }
    for (const n of ringNodes) {
      try { n.stop(); } catch (_) {}
      try { n.disconnect(); } catch (_) {}
    }
    ringNodes = [];
  }

  window.PanelPresence = {
    connect: connect,
    disconnect: disconnect,
    playRing: playRing,
    stopRing: stopRing,
  };
})();
