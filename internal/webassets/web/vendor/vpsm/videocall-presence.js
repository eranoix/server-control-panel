/* vpsm-videocall-presence — panel-wide "someone is calling" client.
 *
 * Opens a long-lived WS to /ws/videocall-presence and fires onIncoming when
 * the server pushes an `incoming-call` event. The user's main app handles
 * the UI (modal + ringtone + browser Notification).
 *
 * Public API:
 *   window.VPSMPresence.connect({ token, onIncoming, onState })
 *   window.VPSMPresence.disconnect()
 *   window.VPSMPresence.playRing(durationMs)        // beeps via WebAudio (no asset)
 *   window.VPSMPresence.stopRing()
 *
 * The ringtone is generated on the fly with WebAudio (two-tone, low volume)
 * so we don't need to vendor an .mp3 file. Total cost: ~3KB of code.
 */
/* VPSMDevice — identidade estável do APARELHO (não da aba).
 *
 * A campainha tocava em todo aparelho logado, sem como dizer "não
 * toque neste computador". Para haver política por aparelho é preciso que o
 * aparelho tenha nome: um uuid em localStorage (sobrevive a reload, aba nova
 * e reinício do navegador — ao contrário do sessionStorage usado pelo
 * client_id, que é por-aba de propósito) + um rótulo legível derivado do UA
 * para o dono se reconhecer na lista ("Windows — Chrome").
 */
(function () {
  'use strict';
  if (window.VPSMDevice) return;
  const KEY = 'vpsm_device_id';

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
      // Mesmo alfabeto que o servidor aceita (sanitizeDeviceID): hex/underscore.
      if (!v || !/^[A-Za-z0-9_-]{8,64}$/.test(v)) {
        v = uuid();
        localStorage.setItem(KEY, v);
      }
      return v;
    } catch (_) {
      // localStorage bloqueado (modo privado): sem identidade estável, o
      // aparelho simplesmente não entra na política e toca sempre.
      return '';
    }
  }

  function label() {
    const ua = navigator.userAgent || '';
    let os = 'Desconhecido';
    if (/Windows/i.test(ua)) os = 'Windows';
    else if (/iPhone|iPad|iPod/i.test(ua)) os = 'iOS';
    else if (/Mac OS X|Macintosh/i.test(ua)) os = 'Mac';
    else if (/Android/i.test(ua)) os = 'Android';
    else if (/Linux/i.test(ua)) os = 'Linux';
    let br = 'Navegador';
    // Ordem importa: Edge/Opera também dizem "Chrome"; Chrome também diz
    // "Safari". Do mais específico para o mais genérico.
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

  window.VPSMDevice = { id, label, info: () => ({ id: id(), label: label() }) };
})();

(function () {
  'use strict';
  if (window.VPSMPresence) return;

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
    // Mobile passa ticketProvider: async () => fetch('/api/auth/ws-ticket').then(...)
    // Cookie HttpOnly autoriza o GET; ticket é one-shot 60s, NÃO loga JWT.
    curTicketProvider = opts.ticketProvider || null;
    cbIncoming = opts.onIncoming || cbIncoming;
    // onEvent recebe TODOS os eventos de presença — inclusive os de controle
    // (call-answered-elsewhere, call-ended) que cancelam um toque em curso.
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
    // Identifica o APARELHO para o servidor aplicar a política de campainha
    // dele. Aparelho sem identidade toca sempre — degradação
    // graciosa, nunca "perdi a ligação porque o localStorage falhou".
    let devParam = '';
    try {
      const d = window.VPSMDevice ? window.VPSMDevice.info() : null;
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
    // Reset backoff so a server-side reject que faz handshake e fecha
    // imediato (token bad) NAO conta como conexao boa. So reseta quando
    // ws fica "estavel" — apos 5s sem close, ou quando recebe primeira
    // mensagem. Sem isso, backoff piorava em loops curtos sem alivio.
    let stableTimer = setTimeout(() => { backoff = 1000; }, 5000);
    ws.onopen = () => { cbState({ type: 'connected' }); };
    ws.onmessage = (ev) => {
      backoff = 1000;
      if (stableTimer) { clearTimeout(stableTimer); stableTimer = 0; }
      let msg; try { msg = JSON.parse(ev.data); } catch (_) { return; }
      // Encaminha tudo: quem trata decide. Manter cbIncoming separado
      // preserva o contrato antigo de quem só quer o toque.
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

  window.VPSMPresence = {
    connect: connect,
    disconnect: disconnect,
    playRing: playRing,
    stopRing: stopRing,
  };
})();
