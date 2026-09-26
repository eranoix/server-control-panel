/* telemetry.js — measuring how the panel screens get used.
 *
 * WHAT IT SENDS, and only this:
 *   { v:1, s:"<sid hex>", e:[{screen:"<canonical id>", origin:"default|nav"}], dropped:N }
 * No URL, no query string, no screen content, no user name. The `sid`
 * is random per tab, lives in sessionStorage and has no tie to any identity
 * The timestamp is the SERVER's — the browser has no way to send one.
 *
 * WHY THE MAIN PATH IS fetch(keepalive) AND NOT sendBeacon (measured in
 * THIS fork):
 *   - there is no CSRF middleware on /api/* (nothing under internal/httpmw/
 *     matches 'csrf'), so both paths are allowed;
 *   - but this fork authenticates with `Authorization: Bearer` (the SPA keeps
 *     the JWT in localStorage) AND, as a fallback, with the HttpOnly cookie
 *     `vpsm_token` (internal/auth/auth.go). sendBeacon CANNOT send a header,
 *     so it depends entirely on the cookie. Depending on the cookie alone would
 *     make telemetry die IN SILENCE in any scenario where it is not there —
 *     and 14 days of an empty file would only be found out much later, too late.
 *   - `keepalive` survives pagehide/visibilitychange just like sendBeacon,
 *     with the same 64 KiB ceiling. sendBeacon stays as the fallback.
 *
 * NON-NEGOTIABLE RULE: telemetry NEVER gets in the panel's way. Everything
 * here is inside try/catch and every promise has a .catch(); a network
 * failure, a blocked localStorage or a 401 cannot produce a single error
 * that the operator can see.
 */
(function () {
  'use strict';

  var ENDPOINT = '/api/telemetry';
  var V = 1;
  var MAX_LOTE = 200;    // the server rejects the WHOLE batch above this
  var FLUSH_MS = 20000;  // 20 s: short enough not to lose a short session

  var buf = [];
  var dropped = 0;
  var timer = null;
  var ultimo = '';       // dedup of an IMMEDIATE repeat (A -> A). A -> B -> A counts 2x.
  var sid = '';

  function hex(n) {
    var b = new Uint8Array(n);
    (window.crypto || window.msCrypto).getRandomValues(b);
    var out = '';
    for (var i = 0; i < b.length; i++) out += ('0' + b[i].toString(16)).slice(-2);
    return out;
  }

  function novoSid() {
    // 16 hex = 8 bytes. Matches the ^[a-f0-9]{8,32}$ the handler demands.
    try { return hex(8); } catch (_) {
      var s = '';
      for (var i = 0; i < 16; i++) s += '0123456789abcdef'[Math.floor(Math.random() * 16)];
      return s;
    }
  }

  try {
    sid = sessionStorage.getItem('vpsm_tel_sid') || '';
    if (!/^[a-f0-9]{8,32}$/.test(sid)) {
      sid = novoSid();
      sessionStorage.setItem('vpsm_tel_sid', sid);
    }
  } catch (_) { sid = novoSid(); }

  function agenda() {
    if (timer) return;
    try { timer = setTimeout(flush, FLUSH_MS); } catch (_) {}
  }

  function flush() {
    try {
      if (timer) { clearTimeout(timer); timer = null; }
      if (!buf.length) return;

      var lote = buf.slice(0, MAX_LOTE);
      var corpo = JSON.stringify({ v: V, s: sid, e: lote, dropped: dropped });
      buf = [];
      dropped = 0;

      var tok = '';
      try { tok = localStorage.getItem('vpsm_token') || ''; } catch (_) {}

      // (1) main path: fetch keepalive with Bearer — the same authentication
      //     channel every other call in this SPA uses.
      if (tok && window.fetch) {
        try {
          window.fetch(ENDPOINT, {
            method: 'POST',
            keepalive: true,
            credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + tok },
            body: corpo
          })['catch'](function () {});
          return;
        } catch (_) { /* fall through to the fallback */ }
      }

      // (2) fallback: sendBeacon — authenticates with the HttpOnly vpsm_token cookie.
      //     Sends Content-Type: text/plain;charset=UTF-8, which the handler accepts.
      try {
        if (navigator.sendBeacon && navigator.sendBeacon(ENDPOINT, corpo)) return;
      } catch (_) {}

      // (3) last resort: fetch without Bearer (cookie), still keepalive.
      try {
        if (window.fetch) {
          window.fetch(ENDPOINT, {
            method: 'POST', keepalive: true, credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json' }, body: corpo
          })['catch'](function () {});
        }
      } catch (_) {}
    } catch (_) {}
  }

  function hit(screen, origin) {
    try {
      if (!screen || typeof screen !== 'string') return;
      origin = (origin === 'default') ? 'default' : 'nav';
      var chave = screen + '|' + origin;
      if (chave === ultimo) return;
      ultimo = chave;

      if (buf.length >= MAX_LOTE) { dropped++; flush(); return; }
      buf.push({ screen: screen, origin: origin });
      if (buf.length >= MAX_LOTE) { flush(); return; }
      agenda();
    } catch (_) {}
  }

  // Tab hidden / page going away: this is WHERE most batches leave. Without it,
  // whoever closes the tab before the 20 s records nothing — and fast navigation
  // between screens is exactly the behaviour the triage wants to see.
  try {
    document.addEventListener('visibilitychange', function () {
      if (document.visibilityState === 'hidden') flush();
    });
    window.addEventListener('pagehide', flush);
    window.addEventListener('beforeunload', flush);
  } catch (_) {}

  window.tel = { hit: hit, flush: flush, sid: function () { return sid; } };
})();
