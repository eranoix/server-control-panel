(function () {
  'use strict';

  var ENDPOINT = '/api/telemetry';
  var V = 1;
  var MAX_BATCH = 200;
  var FLUSH_MS = 20000;

  var buf = [];
  var dropped = 0;
  var timer = null;
  var last = '';
  var sid = '';

  function hex(n) {
    var b = new Uint8Array(n);
    (window.crypto || window.msCrypto).getRandomValues(b);
    var out = '';
    for (var i = 0; i < b.length; i++) out += ('0' + b[i].toString(16)).slice(-2);
    return out;
  }

  function newSid() {
    try { return hex(8); } catch (_) {
      var s = '';
      for (var i = 0; i < 16; i++) s += '0123456789abcdef'[Math.floor(Math.random() * 16)];
      return s;
    }
  }

  try {
    sid = sessionStorage.getItem('panel_tel_sid') || '';
    if (!/^[a-f0-9]{8,32}$/.test(sid)) {
      sid = newSid();
      sessionStorage.setItem('panel_tel_sid', sid);
    }
  } catch (_) { sid = newSid(); }

  function agenda() {
    if (timer) return;
    try { timer = setTimeout(flush, FLUSH_MS); } catch (_) {}
  }

  function flush() {
    try {
      if (timer) { clearTimeout(timer); timer = null; }
      if (!buf.length) return;

      var batch = buf.slice(0, MAX_BATCH);
      var body = JSON.stringify({ v: V, s: sid, e: batch, dropped: dropped });
      buf = [];
      dropped = 0;

      var tok = '';
      try { tok = localStorage.getItem('panel_token') || ''; } catch (_) {}

      if (tok && window.fetch) {
        try {
          window.fetch(ENDPOINT, {
            method: 'POST',
            keepalive: true,
            credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + tok },
            body: body
          })['catch'](function () {});
          return;
        } catch (_) { /* fall through to the fallback */ }
      }

      try {
        if (navigator.sendBeacon && navigator.sendBeacon(ENDPOINT, body)) return;
      } catch (_) {}

      try {
        if (window.fetch) {
          window.fetch(ENDPOINT, {
            method: 'POST', keepalive: true, credentials: 'same-origin',
            headers: { 'Content-Type': 'application/json' }, body: body
          })['catch'](function () {});
        }
      } catch (_) {}
    } catch (_) {}
  }

  function hit(screen, origin) {
    try {
      if (!screen || typeof screen !== 'string') return;
      origin = (origin === 'default') ? 'default' : 'nav';
      var key = screen + '|' + origin;
      if (key === last) return;
      last = key;

      if (buf.length >= MAX_BATCH) { dropped++; flush(); return; }
      buf.push({ screen: screen, origin: origin });
      if (buf.length >= MAX_BATCH) { flush(); return; }
      agenda();
    } catch (_) {}
  }

  try {
    document.addEventListener('visibilitychange', function () {
      if (document.visibilityState === 'hidden') flush();
    });
    window.addEventListener('pagehide', flush);
    window.addEventListener('beforeunload', flush);
  } catch (_) {}

  window.tel = { hit: hit, flush: flush, sid: function () { return sid; } };
})();
