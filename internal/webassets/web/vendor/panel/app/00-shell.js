(function installPerUserStoragePrefix(){
  if (typeof window === 'undefined' || !window.localStorage) return;
  const KEEP_RAW = new Set([
    'panel_token',
    'panel_user',
    'panel_active_user',
    'panel_last_user',
    'panel_build',
    'panel_recovery_token',
  ]);
  const proto = Storage.prototype;
  const orig = {
    getItem: proto.getItem,
    setItem: proto.setItem,
    removeItem: proto.removeItem,
  };
  function activeUser(){
    try { return orig.getItem.call(localStorage, 'panel_active_user') || ''; } catch(_) { return ''; }
  }
  function ns(k){
    if (typeof k !== 'string') return k;
    if (KEEP_RAW.has(k)) return k;
    if (!k.startsWith('panel_')) return k;
    const u = activeUser();
    if (!u) return k;
    return 'panel_u_' + u + '_' + k.slice(5);
  }
  try {
    if (!orig.getItem.call(localStorage, 'panel_active_user')) {
      const u = orig.getItem.call(localStorage, 'panel_user');
      if (u) orig.setItem.call(localStorage, 'panel_active_user', u);
    }
  } catch(_) {}
  Storage.prototype.getItem = function(k){ try { return orig.getItem.call(this, ns(k)); } catch(_) { return null; } };
  Storage.prototype.setItem = function(k,v){ try { return orig.setItem.call(this, ns(k), v); } catch(_) { return undefined; } };
  Storage.prototype.removeItem = function(k){ try { return orig.removeItem.call(this, ns(k)); } catch(_) { return undefined; } };
  window.__panelStorageInternal = {
    rawGet: (k) => orig.getItem.call(localStorage, k),
    rawSet: (k,v) => orig.setItem.call(localStorage, k, v),
    rawRemove: (k) => orig.removeItem.call(localStorage, k),
    activeUser,
    namespaceFor: ns,
    clearActiveUserKeys: function(){
      const u = activeUser();
      if (!u) return;
      const prefix = 'panel_u_' + u + '_';
      const toRemove = [];
      for (let i=0; i<localStorage.length; i++){
        const k = orig.getItem.call(localStorage, 'key__placeholder');
      }
      const keys = [];
      for (let i=0; i<localStorage.length; i++) keys.push(localStorage.key(i));
      keys.forEach(k => {
        if (typeof k === 'string' && k.startsWith(prefix)) {
          try { orig.removeItem.call(localStorage, k); } catch(_) {}
        }
      });
    },
  };
})();

window.panelPrefs = (function(){
  var SYNCED = ['panel_theme', 'panel_page', 'panel_tabs'];
  function _token(){ try { return localStorage.getItem('panel_token') || ''; } catch(_){ return ''; } }
  function get(key){ try { return localStorage.getItem(key); } catch(_){ return null; } }
  function setLocal(key, val){
    try { if (val == null) localStorage.removeItem(key); else localStorage.setItem(key, val); } catch(_){}
  }
  function push(key, val){
    var tok = _token();
    if (!tok) return Promise.resolve(false);
    try {
      return fetch('/api/user/prefs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + tok },
        credentials: 'include',
        body: JSON.stringify({ key: key, value: val })
      }).then(function(r){ return !!(r && r.ok); }).catch(function(){ return false; });
    } catch(_){ return Promise.resolve(false); }
  }
  function set(key, val){ setLocal(key, val); push(key, val); }
  function pull(){
    var tok = _token();
    if (!tok) return Promise.resolve({});
    try {
      return fetch('/api/user/prefs', {
        method: 'GET',
        headers: { 'Authorization': 'Bearer ' + tok },
        credentials: 'include'
      }).then(function(r){ return (r && r.ok) ? r.json() : {}; })
        .then(function(m){ return (m && typeof m === 'object') ? m : {}; })
        .catch(function(){ return {}; });
    } catch(_){ return Promise.resolve({}); }
  }
  return { SYNCED: SYNCED, get: get, set: set, setLocal: setLocal, push: push, pull: pull };
})();

window.panelTheme = (function(){
  var KEY = 'panel_theme';
  var META_DARK  = '#020617';
  var META_LIGHT = '#eef1f6';
  function _read(){ return window.panelPrefs.get(KEY); }
  function _write(v){ window.panelPrefs.set(KEY, v); }
  function _systemPref(){
    try { return window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'; }
    catch(_){ return 'dark'; }
  }
  function resolved(){
    var s = _read();
    return (s === 'light' || s === 'dark') ? s : _systemPref();
  }
  function _syncMeta(theme){
    try {
      var c = theme === 'light' ? META_LIGHT : META_DARK;
      var targets = document.querySelectorAll('meta[name="theme-color"]');
      for (var i = 0; i < targets.length; i++) targets[i].setAttribute('content', c);
    } catch(_){}
  }
  function apply(theme){
    var t = theme === 'light' ? 'light' : 'dark';
    try {
      var root = document.documentElement;
      if (t === 'light') root.setAttribute('data-theme', 'light');
      else root.removeAttribute('data-theme');
    } catch(_){}
    _syncMeta(t);
    try { window.dispatchEvent(new CustomEvent('theme-changed', { detail: { theme: t } })); } catch(_){}
    return t;
  }
  function get(){ return resolved(); }
  function set(theme){ var t = theme === 'light' ? 'light' : 'dark'; _write(t); return apply(t); }
  function toggle(){ return set(resolved() === 'light' ? 'dark' : 'light'); }
  apply(resolved());
  return { get: get, set: set, toggle: toggle, apply: apply, resolved: resolved };
})();

(function bootSanitizer(){
  const SAFE_PURGE_KEYS = [
    'panel_tabs_snapshot',
    'panel_browser_tabs',
    'panel_filters',
    'panel_ports_filter',
    'panel_conns_filter',
    'panel_jira_filter',
    'panel_jira_jql',
    'panel_jira_search',
    'panel_sort_state',
    'panel_tabs',
    'panel_term_snippets',
    'panel_term_recent',
  ];
  const KEEP_ALWAYS = new Set([
    'panel_token', 'panel_user', 'panel_last_user',
    'panel_build',
  ]);

  function purgeUnsafe() {
    SAFE_PURGE_KEYS.forEach(k => { try { localStorage.removeItem(k); } catch(_){} });
  }
  function purgeAllExceptAuth() {
    const keep = {};
    KEEP_ALWAYS.forEach(k => { const v = localStorage.getItem(k); if (v !== null) keep[k] = v; });
    try { localStorage.clear(); } catch(_) {}
    Object.entries(keep).forEach(([k,v]) => { try { localStorage.setItem(k,v); } catch(_){} });
  }

  try {
    const params = new URLSearchParams(location.search);
    if (params.has('nuke')) {
      try { localStorage.clear(); sessionStorage.clear(); } catch(_){}
      location.replace(location.pathname); return;
    }
    if (params.has('safe')) {
      purgeAllExceptAuth();
      try { sessionStorage.clear(); } catch(_){}
      location.replace(location.pathname); return;
    }
  } catch(_) {}

  try {
    const metaBuild = document.querySelector('meta[name="panel-build"]');
    const cur = metaBuild ? metaBuild.getAttribute('content') : '';
    if (cur) {
      const prev = localStorage.getItem('panel_build') || '';
      if (prev !== cur) {
        purgeUnsafe();
        try { localStorage.setItem('panel_build', cur); } catch(_){}
      }
    }
  } catch(_) {}

  function sanitizeArrayKey(storageKey, keyField, opts) {
    opts = opts || {};
    try {
      const raw = localStorage.getItem(storageKey);
      if (!raw) return;
      let arr = JSON.parse(raw);
      let wrapper = null, listPath = null;
      if (opts.wrapperListField && arr && typeof arr === 'object' && !Array.isArray(arr) && Array.isArray(arr[opts.wrapperListField])) {
        wrapper = arr; listPath = opts.wrapperListField; arr = arr[listPath];
      }
      if (!Array.isArray(arr)) { localStorage.removeItem(storageKey); return; }
      const seen = new Set();
      const clean = [];
      for (const item of arr) {
        if (!item || typeof item !== 'object') continue;
        const k = item[keyField];
        if (k === undefined || k === null || k === '') continue;
        const sk = String(k);
        if (seen.has(sk)) continue;
        seen.add(sk);
        clean.push(item);
      }
      if (wrapper) {
        wrapper[listPath] = clean;
        localStorage.setItem(storageKey, JSON.stringify(wrapper));
      } else {
        localStorage.setItem(storageKey, JSON.stringify(clean));
      }
    } catch(_) {
      try { localStorage.removeItem(storageKey); } catch(__){}
    }
  }

  sanitizeArrayKey('panel_term_workspaces', 'name');
  sanitizeArrayKey('panel_browser_tabs', 'id', { wrapperListField: 'tabs' });

  for (const key of ['panel_tabs_snapshot']) {
    try {
      const raw = localStorage.getItem(key);
      if (!raw) continue;
      const snap = JSON.parse(raw);
      if (!snap || typeof snap !== 'object') {
        localStorage.removeItem(key);
      } else if (Array.isArray(snap.panes)) {
        const seen = new Set();
        snap.panes = snap.panes.filter(p => {
          if (!p || !p.id) return false;
          if (seen.has(p.id)) return false;
          seen.add(p.id); return true;
        });
        localStorage.setItem(key, JSON.stringify(snap));
      }
    } catch(_) {
      try { localStorage.removeItem(key); } catch(__){}
    }
  }

  window.__panelSafeMode = function(){
    purgeAllExceptAuth();
    try { sessionStorage.clear(); } catch(_){}
    location.reload();
  };
})();

(function installAlpineKeyDiagnostic(){
  window.__alpineKeyBugs = [];
  const origWarn = console.warn.bind(console);
  console.warn = function(...args) {
    try {
      const msg = args.map(a => (typeof a === 'string' ? a : (a && a.message) || '')).join(' ');
      if (/x-for\s+":key"\s+is\s+(undefined|invalid)/i.test(msg)) {
        const el = args.find(a => a && a.outerHTML);
        const info = {
          ts: Date.now(),
          msg,
          html: el ? el.outerHTML.slice(0, 400) : '(no element in args)',
          parentHtml: el && el.parentElement ? el.parentElement.outerHTML.slice(0, 400) : '',
          forExpr: el ? (el.getAttribute && el.getAttribute('x-for')) : '',
          keyExpr: el ? (el.getAttribute && (el.getAttribute(':key') || el.getAttribute('x-bind:key'))) : '',
        };
        window.__alpineKeyBugs.push(info);
        console.error('[panel] Alpine x-for :key undefined — diagnostics:', info);
        console.error('[panel] To fix it: paste window.__alpineKeyBugs in DevTools and send the snippet');
      }
    } catch(_) {}
    origWarn(...args);
  };
})();

window.installFocusTrap = function (root) {
  if (!root || root.__panelTrap) return root && root.__panelTrap;
  const SELECTOR = 'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';
  const previousFocus = document.activeElement;
  function focusables() {
    return Array.from(root.querySelectorAll(SELECTOR)).filter(el => el.offsetParent !== null);
  }
  function handler(e) {
    if (e.key !== 'Tab') return;
    const els = focusables();
    if (els.length === 0) { e.preventDefault(); return; }
    const first = els[0], last = els[els.length - 1];
    if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
    else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
  }
  root.addEventListener('keydown', handler);
  setTimeout(() => { const fs = focusables(); if (fs.length) fs[0].focus(); }, 0);
  const trap = {
    release() {
      try { root.removeEventListener('keydown', handler); } catch(_) {}
      try { if (previousFocus && previousFocus.focus) previousFocus.focus(); } catch(_) {}
      root.__panelTrap = null;
    },
  };
  root.__panelTrap = trap;
  return trap;
};

(function autoWireModalFocusTraps(){
  var SEL = '[role="dialog"][aria-modal="true"]';
  var known = new Set();

  function isOutermost(el){
    return !el.parentElement || !el.parentElement.closest(SEL);
  }
  function isVisible(el){
    return el.isConnected && el.getClientRects().length > 0;
  }
  function evaluate(el){
    var vis = isVisible(el);
    if (vis && !el.__panelTrap) { try { window.installFocusTrap(el); } catch(_) {} }
    else if (!vis && el.__panelTrap) { try { el.__panelTrap.release(); } catch(_) {} }
  }
  function forget(el){
    if (!known.has(el)) return;
    if (el.__panelTrap) { try { el.__panelTrap.release(); } catch(_) {} }
    known.delete(el);
  }
  function register(el){
    if (known.has(el) || !isOutermost(el)) return;
    known.add(el);
    evaluate(el);
  }
  function scan(node){
    if (!node || node.nodeType !== 1) return;
    if (node.matches && node.matches(SEL)) register(node);
    if (node.querySelectorAll) node.querySelectorAll(SEL).forEach(register);
  }
  function unscan(node){
    if (!node || node.nodeType !== 1) return;
    forget(node);
    if (node.querySelectorAll) node.querySelectorAll(SEL).forEach(forget);
  }

  var obs = new MutationObserver(function(muts){
    for (var i = 0; i < muts.length; i++) {
      var m = muts[i];
      if (m.type === 'attributes') {
        if (known.has(m.target)) evaluate(m.target);
        continue;
      }
      m.addedNodes.forEach(scan);
      m.removedNodes.forEach(unscan);
    }
  });

  function start(){
    scan(document.body);
    obs.observe(document.body, {
      subtree: true, childList: true,
      attributes: true, attributeFilter: ['style', 'hidden'],
    });
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();

(function installErrorBoundary(){
  let active = false;
  function show(err){
    if (active) return;
    active = true;
    const msg = (err && (err.stack || err.message)) ? String(err.stack || err.message) : String(err);
    document.body.innerHTML =
      '<div style="position:fixed;inset:0;display:flex;align-items:center;justify-content:center;background:#070b12;color:#e5e7eb;font-family:system-ui,sans-serif;padding:24px">'+
      '<div style="max-width:640px;border:1px solid #ef4444;border-radius:12px;padding:24px;background:#0b1220">'+
      '<div style="font-size:11px;color:#f87171;text-transform:uppercase;margin-bottom:6px">server-control-panel — interface crashed</div>'+
      '<div style="font-size:18px;font-weight:600;margin-bottom:12px">Unexpected front-end error</div>'+
      '<div style="font-size:12px;color:#9ca3af;margin-bottom:12px">The server itself may be fine. Recover the state below. If it keeps happening, connect over SSH and run <code style="background:#111827;padding:2px 6px;border-radius:4px">panelctl rollback</code>.</div>'+
      '<pre style="font-size:10px;background:#020617;border:1px solid #1f2937;border-radius:6px;padding:10px;overflow:auto;max-height:200px;margin-bottom:16px">'+escapeHtml(msg)+'</pre>'+
      '<div style="display:flex;gap:8px;flex-wrap:wrap">'+
      '<button onclick="(window.__panelSafeMode||function(){localStorage.clear();location.reload()})()" style="background:#2563eb;color:white;border:0;padding:8px 14px;border-radius:6px;cursor:pointer;font-size:13px">Safe Mode (keeps you signed in)</button>'+
      '<button onclick="localStorage.clear();sessionStorage.clear();location.reload()" style="background:transparent;color:#ef4444;border:1px solid #7f1d1d;padding:8px 14px;border-radius:6px;cursor:pointer;font-size:13px">Clear everything (sign in again)</button>'+
      '<button onclick="location.reload()" style="background:transparent;color:#9ca3af;border:1px solid #1f2937;padding:8px 14px;border-radius:6px;cursor:pointer;font-size:13px">Just reload</button>'+
      '<a href="/api/health" target="_blank" style="background:transparent;color:#9ca3af;border:1px solid #1f2937;padding:8px 14px;border-radius:6px;text-decoration:none;font-size:13px">View /api/health</a>'+
      '</div></div></div>';
  }
  function isBenign(err){
    const msg = (err && (err.message || (typeof err === 'string' ? err : ''))) || '';
    if (/ResizeObserver loop/i.test(msg)) return true;
    if (/Cannot read properties of undefined \(reading 'after'\)/i.test(msg)) {
      window.__alpineKeyCrashCount = (window.__alpineKeyCrashCount || 0) + 1;
      if (window.__alpineKeyCrashCount <= 3 || window.__alpineKeyCrashCount % 50 === 0) {
        console.error('[panel] Alpine x-for :key crash #' + window.__alpineKeyCrashCount + ' swallowed — see window.__alpineKeyBugs');
      }
      return true;
    }
    return false;
  }
  window.addEventListener('error', (ev) => {
    const e = ev.error || ev.message;
    if (isBenign(ev.error) || /ResizeObserver loop/i.test(ev.message || '')) return;
    show(e);
  });
  window.addEventListener('unhandledrejection', (ev) => {
    if (isBenign(ev.reason)) return;
    show(ev.reason || 'promise rejection with no reason');
  });
})();

function escapeHtml(s){ return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }

function base64ToText(b64) {
  let s = String(b64 == null ? '' : b64).replace(/\s+/g, '').replace(/-/g, '+').replace(/_/g, '/');
  if (s.length % 4) s += '='.repeat(4 - (s.length % 4));
  const bin = atob(s);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder('utf-8').decode(bytes);
}

function safeJSON(key, fallback){
  try {
    const raw = localStorage.getItem(key);
    if (raw == null || raw === 'undefined') return fallback;
    const v = JSON.parse(raw);
    return v == null ? fallback : v;
  } catch(_) {
    return fallback;
  }
}

const __panelCharts = new Map();

const __panelFilterMemo = Object.create(null);
function __panelMemo(key, deps, compute) {
  const hit = __panelFilterMemo[key];
  if (hit && hit.deps.length === deps.length) {
    let same = true;
    for (let i = 0; i < deps.length; i++) { if (hit.deps[i] !== deps[i]) { same = false; break; } }
    if (same) return hit.value;
  }
  const value = compute();
  __panelFilterMemo[key] = { deps, value };
  return value;
}

function app() {
  return {
    ...(window.PanelGitModule ? window.PanelGitModule() : { git: { repos: [], repo: '' } }),
    ...(window.PanelDeployModule ? window.PanelDeployModule() : { deploy: { apps: [] } }),
    ...(window.PanelNodesModule ? window.PanelNodesModule() : { nodes: { list: [] } }),
    ...(window.PanelProxmoxModule ? window.PanelProxmoxModule() : { pvx: { health: null } }),
    ...(window.PanelAgentsModule ? window.PanelAgentsModule() : { agents: { sessions: [] } }),

    token: localStorage.getItem('panel_token') || '',
    username: localStorage.getItem('panel_user') || '',
    userEmail: localStorage.getItem('panel_email') || '',
    hostname: '',
    guestMode: false,
    guestRoomId: '',
    guestRoomName: '',
    guestEnded: false,
    vcConfirm: { open: false, title: '', desc: '', confirmLabel: 'Confirm', danger: false, onYes: null },
    loginForm: {username: localStorage.getItem('panel_last_user') || '', password:'', totp:'', remember_device:false},
    loginError: '',
    loginNeedTOTP: false,
    loginNeedSetup: false,
    loginBusy: false,
    setupToken: '',
    setupQR: {},
    setupForm: { main_code:'', recovery_code:'' },
    refreshTimer: null,
    mfaUI: {
      enrolled: false, factorId: '', factorStatus: '',
      step: 'idle', qr: '', secret: '', code: '',
      backupCodes: [], backupCodesUnused: 0, busy: false, err: ''
    },
    mobileDevicesUI: {
      items: [], busy: false, loaded: false, err: ''
    },
    mobilePairUI: {
      qrPngB64: '', serverUrl: '', expiresAt: 0, busy: false, err: ''
    },
    whatsapp: {
      enabled: false, status: 'UNPAIRED', phone: '', pushName: '',
      qrDataURL: '', wahaReachable: false, hookOK: false, lastSync: 0,
      chats: [], chatById: {}, totalUnread: 0, syncing: false,
      activeJid: null, messages: {},
      loadingMessages: false, hasMoreMessages: {},
      composer: '', attachment: null, attachmentPreview: '',
      ws: null, wsBackoff: 1000,
      notifPermission: (typeof Notification !== 'undefined' ? Notification.permission : 'default'),
      starting: false, busy: false, error: '',
      search: '',
      filter: 'all',
      replyTo: null,
      emojiOpen: false,
      msgMenu: { open:false, x:0, y:0, msg:null },
      chatMenu: false,
      voiceRec: { active:false, recorder:null, chunks:[], startedAt:0, elapsed:0, timer:null },
      chatSearch: '',
      chatSearchOpen: false,
      infoPanelOpen: false,
      dragOver: false,
      imagePreview: null,
      typingTimer: null,
      statusBySender: [],
      statusLoading: false,
      scrollPinned: true,
      newChatOpen: false,
      newChatPhone: '',
      newChatBusy: false,
      groupInfo: null,
      groupInfoLoading: false,
    },
    videocall: {
      rooms: [],
      newRoomName: '',
      newMember: '',
      selectedRoom: null,
      inCall: false,
      activeRoomId: '',
      callDisplayName: '',
      chat: [],
      chatComposer: '',
      muted: false,
      videoOff: false,
      screenSharing: false,
      recording: false,
      audioFirstMode: localStorage.getItem('panel_vc_audio_first') === '1',
      autoVideoOff: false,
      budgetKbps: parseInt(localStorage.getItem('panel_vc_budget_kbps') || '60', 10),
      codec: localStorage.getItem('panel_vc_codec') || 'auto',
      e2eeWanted: localStorage.getItem('panel_vc_e2ee_wanted') === '1',
      e2eePassphraseInput: '',
      e2eeActive: false,
      e2eePromptOpen: false,
      e2eePendingRoomId: '',
      e2eeSupported: !!(window.PanelVideoCallE2EE && window.PanelVideoCallE2EE.isSupported()),
      inviteOpen: false,
      inviteTTLMin: 60,
      pinTTLHours: 24,
      pinModalOpen: false,
      _nowTick: Math.floor(Date.now() / 1000),
      renamingRoomId: '',
      renameDraft: '',
      inviteURL: '',
      inviteExpiresAt: 0,
      inviteCopyDone: false,
      history: [],
      filesPanelOpen: false,
      transfers: [],
      wbActive: false,
      wbColor: '#60a5fa',
      annotActive: false,
      annotTool: 'pen',
      frostActive: false,
      incoming: null,
      presenceConnected: false,
      ringDevices: [],
      ringDevicesBusy: false,
      thisDeviceId: (window.PanelDevice ? window.PanelDevice.id() : ''),
      pushSupported: !!(window.PanelPush && window.PanelPush.isSupported()),
      pushSubscribed: false,
      pushPermission: (typeof Notification !== 'undefined' ? Notification.permission : 'default'),
      pushBusy: false,
      shareOpen: false,
      sinkIdSupported: (function() {
        try { return typeof HTMLMediaElement !== 'undefined' && 'setSinkId' in HTMLMediaElement.prototype; }
        catch (_) { return false; }
      })(),
      lobbyOpen: false,
      lobbyForRoomId: '',
      lobbyForPassphrase: '',
      lobbyBusy: false,
      lobbyError: '',
      lobbyPermission: 'unknown',
      lobbyMicLevel: 0,
      lobbySkipNext: localStorage.getItem('panel_vc_skip_lobby') === '1',
      settingsOpen: false,
      settingsMicLevel: 0,
      devices: { cameras: [], mics: [], speakers: [] },
      selectedDevices: {
        camera:  localStorage.getItem('panel_vc_camera_id')  || 'default',
        mic:     localStorage.getItem('panel_vc_mic_id')     || 'default',
        speaker: localStorage.getItem('panel_vc_speaker_id') || 'default',
      },
      qualityMode: localStorage.getItem('panel_vc_quality') || 'economy',
      shortcutsEnabled: localStorage.getItem('panel_vc_shortcuts') !== '0',
      shortcutsOverlay: false,
      peerStates: {},
      peerCount: 0,
      peersList: [],
      chatLastRead: 0,
      reactions: [],
      reactionsPickerOpen: false,
      dropOverlay: false,
      lobbyCaps: { audio: true, video: true },
      lobbyTone: false,
      settingsTone: false,
      pttActive: false,
      pipActive: false,
      speakingMuted: false,
      reconnectingOverlay: false,
      subtitlesActive: false,
      subtitlesSupported: !!(window.PanelSTT && typeof window.PanelSTT.availableBackends === 'function' && window.PanelSTT.availableBackends().length > 0),
      subtitlesNoEngine: false,
      subtitlesBackend: '',
      currentCaption: { from: '', text: '', expireAt: 0 },
      livePartials: {},
      callStartedAt: 0,
      peerAudioLevels: {},
      transcript: '',
      transcriptEntries: [],
      transcriptSearch: '',
      transcriptHasMore: false,
      polishWithAI: localStorage.getItem('panel_vc_polish_ai') === '1',
      polishQueue: [],
      polishBusy: false,
      showOriginalIds: {},
      subtitlesLang: localStorage.getItem('panel_vc_subtitles_lang') || 'en-US',
      subtitlesLangPickerOpen: false,
      micGain: Math.min(4, Math.max(0.25, parseFloat(localStorage.getItem('panel_vc_mic_gain') || '1.0') || 1.0)),
      micProc: (() => {
        let p = {};
        try { p = JSON.parse(localStorage.getItem('panel_vc_mic_proc') || '{}') || {}; } catch (_) {}
        return {
          noiseSuppression: p.noiseSuppression !== false,
          echoCancellation: p.echoCancellation !== false,
          autoGainControl:  p.autoGainControl  !== false,
        };
      })(),
      micProcBusy: false,
      micLevel: 0,
      micLimiting: false,
      subtitlesShow: localStorage.getItem('panel_vc_subtitles_show') === '1',
      sttBackend: localStorage.getItem('panel_vc_stt_backend') || 'whisper-local',
      whisperLocalAvailable: false,
      subtitlesBackendActive: '',
      amOwner: false,
      roomOwnerName: '',
      roomLocked: false,
      peers: [],
      captionsByPeer: {},
      localPipSize: (['sm','md','lg','hidden'].includes(localStorage.getItem('panel_vc_local_size')) ? localStorage.getItem('panel_vc_local_size') : 'md'),
      localPipPos: (['br','bl','tr','tl'].includes(localStorage.getItem('panel_vc_local_pos')) ? localStorage.getItem('panel_vc_local_pos') : 'br'),
      localMirror: localStorage.getItem('panel_vc_local_mirror') !== '0',
      remoteFit: (['cover','contain'].includes(localStorage.getItem('panel_vc_remote_fit')) ? localStorage.getItem('panel_vc_remote_fit') : 'cover'),
      spotlight: '',
      recordToCloud: localStorage.getItem('panel_vc_cloud_rec') === '1',
      recordings: [],
      summaryModal: { open: false, recId: '', busy: false, summary: '' },
      waInviteOpen: false,
      waInviteSearch: '',
      callingContact: false,
      sidePanel: '',
      settingsPopOpen: false,
      settingsGroups: (function () {
        const def = { quality: true, share: false, recording: false, tools: false, layout: false, owner: true, view: false };
        try {
          const raw = JSON.parse(localStorage.getItem('panel_vc_settings_groups') || '{}');
          if (raw && typeof raw === 'object') for (const k in def) if (typeof raw[k] === 'boolean') def[k] = raw[k];
        } catch (e) {}
        return def;
      })(),
      audioPopOpen: false,
      videoPopOpen: false,
      controlsVisible: true,
      _idleTimer: null,
      handRaised: false,
      peerHandsRaised: {},
      participants: [],
      stageFullscreen: false,
      stats: { bytesSentPerSec:0, bytesRecvPerSec:0, codec:'', resolution:'', framerate:0, rtt:0, packetsLost:0, connectionType:'direct' },
      errors: [],
      busy: false,
      loaded: false,
    },
    page: localStorage.getItem('panel_page') || 'dashboard',
    tabs: safeJSON('panel_tabs', {}),
    mobileSidebarOpen: false,
    browserHealth: null,
    browserTabs: [],
    browserActive: null,
    browserUrlInput: '',
    browserMounted: false,
    browserSnapTimer: null,
    browserTitles: {},
    browserClosedStack: [],
    browserLoading: {},
    browserUrlEditing: false,
    browserCtxMenu: { open:false, x:0, y:0, tabId:null },
    browserSearchEngine: localStorage.getItem('panel_browser_search_engine') || 'ddg',
    browserSearchOpen: false,
    browserSearchEngines: {
      ddg:       { name:'DuckDuckGo',   url:'https://duckduckgo.com/?q=%s',             color:'#de5833', letter:'D' },
      brave:     { name:'Brave Search', url:'https://search.brave.com/search?q=%s',     color:'#f1592a', letter:'B' },
      startpage: { name:'Startpage',    url:'https://www.startpage.com/do/search?q=%s', color:'#5a7df0', letter:'S' },
      google:    { name:'Google',       url:'https://www.google.com/search?q=%s',       color:'#4285f4', letter:'G' },
    },
    stats: null,
    procs: null,
    procsQ: { name:'', user:'', cmd:'', sort:'cpu', limit:200, offset:0 },
    procsLive: false,
    procsWS: null,
    procsBusy: false,
    procsZoom: parseFloat(localStorage.getItem('panel_procs_zoom') || '1') || 1,
    procsLayoutDirty: false,
    procsLayoutSavedLocal: '',
    todos: [],
    todosSummary: { overdue:0, week:0, month:0, future:0, done:0 },
    todoForm: { open:false, t:{ title:'', notes:'', category:'custom', interval_days:0, notify_wa:false }, dueDate:'', editingId:null },
    jiraConfig: { site:'', email:'', project_key:'', board_jql:'', has_token:false },
    jiraHealth: { ok:false, error:'', me:null, site:'' },
    jiraSetup:  { site:'', email:'', token:'', project_key:'' },
    jiraSetupError: '',
    jiraSettings: { open:false, site:'', email:'', token:'', project_key:'', board_jql:'', board_columns:'' },
    jiraProjects: [],
    jiraIssueTypes: [],
    jiraDetailIssueTypes: [],
    jiraIssues: [],
    jiraJQL: '',
    jiraDragKey: null,
    jiraAssignableUsers: [],
    jiraAssignableProject: '',
    jiraAssignableQuery: '',
    jiraAssignableLoading: false,
    jiraCreate: { open:false, project_key:'', issue_type:'Task', summary:'', description:'', priority:'', due_date:'', labels_str:'', assignee_id:'', assignee_name:'', parent_key:'', files:[] },
    jiraCreateEpics: [],
    jiraCreateParent: { query:'', results:[], loading:false, name:'' },
    jiraDetail: {
      open:false, key:'', issue:null, transitions:[], comments:[], newComment:'',
      editing:false, editSummary:'', editDescription:'',
      watchers:null, subtasks:[], links:[], attachments:[], worklogs:[], changelog:[],
      newLinkType:'', newLinkKey:'',
      newWorklog:{ time_spent:'', comment:'' },
    },
    jiraView: 'board',
    jiraProjectKey: '',
    jiraFilter: (() => {
      const ok = ['all','mine','todo','inprogress','review','last7','reported','custom'];
      const v = localStorage.getItem('panel_jira_filter') || 'all';
      return ok.includes(v) ? v : 'all';
    })(),
    jiraCustomJQL: localStorage.getItem('panel_jira_jql') || '',
    jiraSearch: localStorage.getItem('panel_jira_search') || '',
    jiraDetailTab: 'overview',
    jiraSelected: [],
    jiraBulkTransition: '',
    jiraLinkTypes: [],
    jiraPriorities: [],
    jiraQuickFilters: [
      { key:'all',        label:'∞ All' },
      { key:'mine',       label:'⭐ Mine' },
      { key:'todo',       label:'📋 To Do' },
      { key:'inprogress', label:'🚧 In Progress' },
      { key:'last7',      label:'📅 Last 7d' },
      { key:'reported',   label:'📣 Reported' },
      { key:'custom',     label:'⚙ JQL' },
    ],
    jiraSpaces: [],
    jiraSelectedSpace: null,
    jiraPages: [],
    jiraPagesCursor: '',
    jiraPageDrawer: { open:false, id:'', title:'', body:'', web_url:'' },
    jiraColumnMgr: { open:false, cols:[], editIdx:-1, draft:{ label:'', status_names_str:'', color:'border-gray-500/40' } },
    jiraColorPalette: ['border-gray-500/40','border-cyan-500/40','border-yellow-500/40','border-green-500/40','border-purple-500/40','border-orange-500/40','border-pink-500/40','border-red-500/40'],
    jiraHideDoneDays: parseInt(localStorage.getItem('jira_hide_done_days')||'0', 10),
    jiraColSort: (() => {
      try {
        const o = JSON.parse(localStorage.getItem('jira_col_sort') || '{}');
        return (o && typeof o === 'object' && !Array.isArray(o)) ? o : {};
      } catch (_) { return {}; }
    })(),
    jiraPrefs: (() => {
      const def = { fontScale: 1.0 };
      try {
        const v = Object.assign({}, def, JSON.parse(localStorage.getItem('panel_jira_prefs')||'{}'));
        v.fontScale = Math.min(1.4, Math.max(0.8, parseFloat(v.fontScale)||1.0));
        return v;
      } catch { return def; }
    })(),
    jiraEditComment: { id:'', body:'' },
    jiraVotes: { votes:0, has_voted:false },
    jiraVersions: [], jiraComponents: [], jiraEpics: [],
    jiraAdvFields: { story_points:'', components:[], fix_versions:[], epic_link:'' },
    jiraAIBusy: false,
    jiraWorkBusy: false,
    aiPrompts: [],
    aiPromptsLoading: false,
    aiPromptsErr: '',
    aiPromptEdits: {},
    aiPromptErrors: {},
    aiPromptSaving: {},

    jobs: [],
    jobsCounts: { running:0, queued:0, failed:0 },
    jobsPrefs: (() => {
      const def = { groupBy:'task', fontSize:'sm', filterStatus:'', filterKind:'', filterText:'' };
      try { return Object.assign(def, JSON.parse(localStorage.getItem('panel_jobs_prefs')||'{}')); } catch { return def; }
    })(),
    jobsPrefsDirty: false,
    jobLauncher: { open:false, kind:'docker_pull', args:{}, argsText:'' },
    jobLog: { open:false, minimized:false, id:'', status:'', progress:0, step:'', text:'', ws:null, kind:'', reconnect:{ attempts:0, timer:null, cancelled:false }, _replay:false },
    _jobLogRestoreId: '',
    jobsLiveTimer: null,
    jobsClockTimer: null,
    jobsNow: Math.floor(Date.now()/1000),
    jobGroupsCollapsed: (() => { try { return JSON.parse(localStorage.getItem('panel_job_groups')||'{}') || {}; } catch { return {}; } })(),
    schedJobs: [],
    schedCatalog: [],
    schedCatalogPrimary: false,
    schedCatalogLoaded: false,
    schedCatSearch: '',
    schedCatOrder: ['Operations','Backup & Data','Security & Audit','Monitoring','Network','Housekeeping','Notifications','Other'],
    schedCatIcons: { 'Operations':'⚙️','Backup & Data':'💾','Security & Audit':'🛡️','Monitoring':'📈','Network':'🌐','Housekeeping':'🧹','Notifications':'🔔','Other':'📦' },
    schedTemplates: [
      { icon:'💾', kind:'backup_now', name:'Daily vault backup', schedule:'0 3 * * *', args:{ target:'all', retention:'7' }, desc:'Every day at 03:00, keeps 7' },
      { icon:'🔒', kind:'cert_renew', name:'Renew certificates', schedule:'30 3 * * *', args:{ cert_name:'' }, desc:'Daily at 03:30' },
      { icon:'🛡️', kind:'security_audit', name:'Weekly Lynis audit', schedule:'0 2 * * 0', desc:'Sunday at 02:00' },
      { icon:'🕵️', kind:'rootkit_scan', name:'Daily rootkit hunt', schedule:'0 1 * * *', args:{ tool:'rkhunter' }, desc:'Daily at 01:00' },
      { icon:'📦', kind:'apt_upgrade', name:'Update packages (Mon)', schedule:'0 4 * * 1', desc:'Monday at 04:00' },
      { icon:'🧹', kind:'cleanup', name:'Journal vacuum', schedule:'0 5 * * 0', args:{ scope:'journal' }, desc:'Sunday at 05:00' },
      { icon:'💽', kind:'disk_check', name:'Watch disk usage (90%)', schedule:'0 * * * *', args:{ threshold:'90', path:'/' }, desc:'Every hour' },
      { icon:'📋', kind:'audit_report', name:'Audit snapshot', schedule:'0 6 1 * *', desc:'Monthly, on the 1st' },
      { icon:'🖥️', kind:'session_backup', name:'Daily session backup', schedule:'0 2 * * *', args:{ session:'all', retention:'7' }, desc:'Daily at 02:00, each live session on its own, keeps 7 per session' },
      { icon:'🖥️', kind:'session_backup', name:'Hourly session backup', schedule:'0 * * * *', args:{ session:'all', retention:'24' }, desc:'Every hour, each live session on its own, keeps 24 per session' },
    ],
    schedForm: {
      open:false,
      j:{ id:'', name:'', schedule:'0 3 * * *', kind:'', enabled:true, run_as_root:false, alert_on:'fail', then_kind:'', then_on:'success', notify_channels:[], notify_on:'success' },
      args:{},
      thenArgs:{},
      sched:{ mode:'daily', everyN:15, everyUnit:'minutes', time:'03:00', weekdays:[1], dom:1 },
      dest:{ type:'local', localPath:'', remote:'', remotePath:'' },
      preview:[], previewError:'',
    },
    schedThenCustom:{},
    schedNotifyChannels:[],
    schedRemotes:{ installed:false, list:[], loaded:false },
    schedOptions:{},
    schedCustom:{},
    fsBrowser:{ open:false, mode:'local', remote:'', path:'/', parent:'', dirs:[], loading:false, error:'', newFolder:'', _onPick:null },
    rcloneConnect:{ open:false, name:'', type:'drive', token:'', accessKey:'', secret:'', region:'', endpoint:'', provider:'', host:'', user:'', pass:'', port:'', url:'', keyFile:'', busy:false, error:'', authId:'', authUrl:'', authBusy:false },
    schedInfo:{ open:false, kind:'', label:'', icon:'', description:'', details:'', useCases:[], examples:[], output:'', nextSteps:[], requiresPrimary:false },
    schedHist:{ open:false, name:'', runs:[], loading:false, error:'' },
    containers: [],
    containersLoaded: false,
    volumesLoaded: false,
    networksLoaded: false,
    images: [],
    volumes: null,
    networks: [],
    composeProjects: [],
    composeOutput: '',
    pruneOut: '',
    pullRef: '',
    pullOut: '',
    units: [], selectedUnit:'', unitOutput:'',
    unitsLoaded: false,
    unitMode: 'status',
    journalLines: parseInt(localStorage.getItem('panel_journal_lines') || '300', 10) || 300,
    unitsAutoRefresh: parseInt(localStorage.getItem('panel_units_auto') || '0', 10) || 0,
    _unitsTimer: null,
    confirmModal: { open:false, title:'', message:'', action:null, danger:false, requireText:'', typed:'' },
    askInputModal: { open:false, title:'', label:'', value:'', placeholder:'', error:'', validate:null, submit:null },
    schedSaveBusy: false,
    ufwBusy: false,
    pruneBusy: false,
    listening: [], connections: [],
    portsFilter: Object.assign({ proto: '', text: '' },        safeJSON('panel_ports_filter', {})),
    connsFilter: Object.assign({ proto: '', state: '', text: '' }, safeJSON('panel_conns_filter', {})),
    connsLimit: 200,
    portsAutoRefresh: parseInt(localStorage.getItem('panel_ports_auto') || '0', 10) || 0,
    _portsTimer: null,
    fileLimit: 200,
    _scrollObservers: {},
    fileList: null, filePath:'/root', fileEdit:{path:'',content:''},
    fileSel: [],
    fileSearch: { open:false, root:'/root', name:'', content:'', max:200, hits:[] },
    fileProps: null,
    fileTrash: { open:false, entries:[] },
    filePreview: null,
    fileBusy: false,
    fileModal: { kind:'', path:'', dest:'', mode:'0644', uid:0, gid:0, rec:false, url:'', filename:'' },
    claudeData: null, cfg: null,
    claudeBusy: false, claudeForkModel: '',
    claudeTermSessions: [], swapAllAccountId: 'jordan',
    claudeAccounts: { accounts: [], consumers: [] }, claudeAcctBusy: false,
    claudeUsage: { accounts: [] }, claudeUsageBusy: false,
    claudeRates: { accounts: [] }, claudeRatesBusy: false,
    aiModels: { config:{}, effective:{}, allowed:[''], defaults:{} }, aiModelsBusy:false, aiModelsSavedAt:0,
    _nowTick: Date.now(), _nowTimer: null,
    aiTab: 'routing',
    privTokens: [], privStatus: null, privBusy: false, privCreated: null, privError: '',
    newPrivToken: { name:'', rpm:null, tpm:null, budget:null, expiresDays:null },
    privConn: { fmt:'anthropic', lang:'curl', tls:'trust',
      baseUrl:'https://203.0.113.10:9443', token:'', model:'claude-sonnet-4-6' },
    privCertFp: 'CF:8D:31:41:B6:6E:B8:8B:60:85:C9:41:4A:7D:B8:82:E1:67:4D:2C:4D:A9:A0:40:98:7B:88:28:A2:6C:D0:47',
    secretKeys: [], secretGroups: [], secretSearch:'', secretGroupsOpen:{},
    secretTypes:['password','token','api-key','cert','url','ssh-key','other'],
    newSecret:{key:'',value:'',group:'',type:'password',notes:''}, secretFormOpen:false, revealedSecret:{key:'',value:''},
    revealedSecretRemaining: 0, _revealTimer: null, _revealTick: null,
    audit: [],
    history: [], alertRules: [], alertFormOpen:false, clockNowSec: 0,
    metricCatalog: [], metricSnapshot: {}, metricSnapTs: 0, metricSearch: '', ruleSeries: {},
    newRule:{name:'',metric:'sys.cpu',op:'>',threshold:80,duration:60,severity:'warning'},
    alertBuilder:{ open:false, step:1, kind:'metric', metric:'sys.cpu', op:'>', threshold:80, duration:60, rearm_margin:0, renotify_sec:0, event_type:'metric.threshold', _origin:'', _kind:'', severity:'warning', enabled:true, channels:[], name:'', description:'', editingLimit:'', editingRule:'', aiBusy:false, nameTouched:false },
    alertingCfg: {enabled:false, from_user:'', chat_jid:'', min_severity:''},
    alertingUsers: [],
    alertingBusy: false,
    alertingDirty: false,
    notify: {
      channels: [], rules: [], events: [], dropped: 0,
      catalog: {event_types:[], severities:[], origins:[], kinds:[]},
      channelForm: {id:'', name:'', type:'whatsapp', enabled:true, config:{from_user:'', chat_jid:''}},
      channelFormOpen: false,
      ruleForm: {id:'', name:'', enabled:true, type_prefix:'job.', min_severity:'', source_prefix:'', _kind:'', _origin:'', channels:[]},
      ruleFormOpen: false,
      dryrun: null, busy: false,
    },
    notifyMeta: {
      'job.':            {icon:'🧩', tone:'info',    short:'Any job ending', desc:'Error, completion, cancellation or interruption.'},
      'job.failed':      {icon:'❌', tone:'danger',  short:'Job failed',              desc:'When a job ends with an error.'},
      'job.done':        {icon:'✅', tone:'success', short:'Job completed',           desc:'When a job ends successfully.'},
      'job.cancelled':   {icon:'🚫', tone:'warning', short:'Job cancelled',           desc:'When someone cancels a job.'},
      'job.interrupted': {icon:'⏸️', tone:'warning', short:'Job interrupted',        desc:'A restart or deploy stopped the job midway.'},
      'metric.threshold':{icon:'📈', tone:'warning', short:'Metric crossed threshold',   desc:'CPU, memory, disk… above the configured value.'},
      'metric.resolved': {icon:'✅', tone:'success', short:'Metric recovered',       desc:'The value came back to normal after an alert.'},
      'scheduler.enqueue_failed':{icon:'⏰', tone:'danger', short:'Scheduler failure', desc:'The scheduler could not enqueue the job.'},
    },
    pwdForm:{old:'',new:''},
    filter: safeJSON('panel_filters', {containers:'',images:'',units:'',volumes:'',networks:'',compose:''}),
    detail: {open:false, id:'', name:'', tab:'overview', logs:'', inspect:'', inspectObj:null, stats:'', top:'', term:null, ws:null, logWS:null, statsWS:null},
    _termsClaude: {
      panes: [], layout: null, activePane: null, _paneSeq: 0,
      broadcast: false,
      _dragPane: null, _dropZone: null, _dropTarget: null,
      trackpadMode: false, _swipeStart: null, _longPressTimer: null,
      _trackpadLastEmit: 0, _trackpadAnchor: null,
    },
    terms: { panes: [], layout: null, activePane: null, _paneSeq: 0, broadcast: false },
    sessionMgrOpen: false,
    sessionMgr: { tab: 'sessions', sessions: [], backups: [], busy: false, expanded: {}, preview: {}, previewOpen: {}, menuOpen: null, loadError: '' },
    createSess: { open: false, name: '', cwd: '', cmd: 'bash', account: '', busy: false, err: '' },
    mobileChangeTick: 0,
    _mobileMQ: null,
    paneContextMenu: { open: false, x: 0, y: 0, paneId: null },
    termCtxMenu: { open:false, x:0, y:0, state:null, selection:'', url:'', ip:'', path:'', clipboard:false, clipPreview:'', hasMarks:false, isPane:false, paneName:'', _markIdx:-1 },
    termWorkspaces: safeJSON('panel_term_workspaces', []),
    termWorkspacesOpen: false,
    hostTermFontSize: parseInt(localStorage.getItem('panel_term_fontsize')||'13',10),
    hostTermFontSizeMobile: parseInt(localStorage.getItem('panel_term_fontsize_mobile')||'11',10),
    hostTermSearchOpen: false,
    hostTermSnippetsOpen: false,
    hostTermSettingsOpen: false,
    hostTermTheme: localStorage.getItem('panel_term_theme') || 'dark',
    hostTermBell: localStorage.getItem('panel_term_bell') || 'none',
    hostTermCursorStyle: localStorage.getItem('panel_term_cursor_style') || 'block',
    hostTermCursorBlink: localStorage.getItem('panel_term_cursor_blink') !== '0',
    hostTermGpu: localStorage.getItem('panel_term_gpu') === '1',
    hostTermCtrlV: localStorage.getItem('panel_term_ctrl_v') !== '0',
    newVersionAvailable: false,
    hostTermAutoReload: localStorage.getItem('panel_term_auto_reload') === '1',
    hostTermLigatures: localStorage.getItem('panel_term_ligatures') === '1',
    hostTermScrollback: parseInt(localStorage.getItem('panel_term_scrollback')||'10000',10),
    hostTermPrimerMiB: Math.round((parseInt(localStorage.getItem('panel_term_primer_bytes')||'2097152',10)||0)/1048576),
    hostTermSearchCase: localStorage.getItem('panel_term_search_case') === '1',
    hostTermSearchMatchCount: '',
    hostTermAlertPattern: localStorage.getItem('panel_term_alert_pattern') || '',
    _mounted: {},
    claudeVer: { installed: '', outdated: 0, processes: [], open: false, loading: false, restarting: 0 },
    hostTermPredictiveEcho: localStorage.getItem('panel_term_predictive_echo') || 'auto',
    hostTermEchoThreshold: parseInt(localStorage.getItem('panel_term_echo_threshold') || '60', 10),
    termHelpOpen: false,
    hostNotifyEnabled: localStorage.getItem('panel_term_notify') === '1',
    hostTermStartupCmd: localStorage.getItem('panel_term_startup_cmd') || '',
    mobileSpecialChars: ['|','~','/','\\','$','*','?',':',';','.','-','_','=','"',"'",'`','&','#','@','%','!','^','<','>','(',')','[',']','{','}'],
    builtinSnippets: [
      {name:'ls -la',  cmd:'ls -la'},
      {name:'htop',    cmd:'htop'},
      {name:'df -h',   cmd:'df -h'},
      {name:'free -h', cmd:'free -h'},
      {name:'docker ps', cmd:'docker ps'},
      {name:'docker stats', cmd:'docker stats --no-stream'},
      {name:'journalctl -fu server-control-panel', cmd:'journalctl -fu server-control-panel'},
      {name:'tail -f /var/log/syslog', cmd:'sudo tail -f /var/log/syslog'},
    ],
    userSnippets: safeJSON('panel_term_snippets', []),
    recentCommands: safeJSON('panel_term_recent', []),
    recentViews: safeJSON('panel_recent_views', []),
    palettePins: safeJSON('panel_palette_pins', []),
    focusMode: false,
    chromeCollapsed: (() => { try { return localStorage.getItem('panel_chrome_collapsed') === '1'; } catch(_) { return false; } })(),
    chromePeek: false,
    tabBarH: 52,
    sttSupported: !!(window.PanelSTT && window.PanelSTT.isSupported()),
    stt: { session: null, targetElId: '', targetPath: '', baseText: '', interim: '' },
    brPersistentMounted: false,
    codeMounted: false,
    bpQuality: 'eco',
    bpActive: false,
    bpPauseTimer: null,
    bpInstances: [],
    bpInstance: localStorage.getItem('panel_bp_instance') || 'pro',
    bpHeaderHidden: false,
    bw: { in: 0, out: 0, since: 0 },
    bwPollTimer: null,

    palette: {
      open: false, query: '', selected: 0,
      pages: [
        {label:'Dashboard',           kind:'page', page:'dashboard',  hint:'g+d', kw:'home overview panel'},
        {label:'History',           kind:'page', page:'history',    hint:'g+h', kw:'history charts series'},
        {label:'Alerts',             kind:'page', page:'alerts',     hint:'g+l', kw:'alerts alarms warnings rules'},
        {label:'Metrics',            kind:'page', page:'metrics',                kw:'metrics cpu memory ram disk load'},
        {label:'Containers',          kind:'page', page:'containers', hint:'g+c', kw:'docker container'},
        {label:'Compose',             kind:'page', page:'compose',                kw:'docker-compose stack project'},
        {label:'Images',             kind:'page', page:'images',     hint:'g+i', kw:'images docker image'},
        {label:'Volumes',             kind:'page', page:'volumes',    hint:'g+v', kw:'storage docker disk'},
        {label:'Networks',               kind:'page', page:'networks',   hint:'g+n', kw:'networks network docker'},
        {label:'Prune / Pull',        kind:'page', page:'prune',                  kw:'cleanup docker prune pull clear'},
        {label:'Processes',           kind:'page', page:'processes',              kw:'processes ps top htop'},
        {label:'Ports / Connections',   kind:'page', page:'ports',                  kw:'ports connections sockets netstat network'},
        {label:'Systemd / journalctl',kind:'page', page:'systemd',    hint:'g+s', kw:'services units logs journal journalctl'},
        {label:'Files',            kind:'page', page:'files',      hint:'g+f', kw:'files explorer manager folders'},
        {label:'Terminal',            kind:'page', page:'terminal',   hint:'g+t', kw:'dev shell console bash ssh'},
        {label:'AI',                  kind:'page', page:'ai',                     kw:'ai claude artificial intelligence assistant'},
        {label:'Documentation',        kind:'page', page:'documentation',           kw:'docs help manual'},
        {label:'Graphs',              kind:'page', page:'graphs',                 kw:'graphs graph dependencies graphify'},
        {label:'VSCode (code-server)',kind:'page', page:'code',                   kw:'code editor vscode ide code-server'},
        {label:'Secrets',             kind:'page', page:'secrets',                kw:'secrets passwords vault credentials'},
        {label:'Audit',               kind:'page', page:'audit',      hint:'g+a', kw:'audit logs events trail'},
        {label:'Users',            kind:'page', page:'users',      hint:'g+u', kw:'users accounts access permissions'},
        {label:'Operations · Tasks',      kind:'page', page:'maintenance',  hint:'g+m', kw:'jira tasks maintenance kanban'},
        {label:'Operations · Jobs (queue)',  kind:'page', page:'jobs',        hint:'g+j', kw:'queue jobs'},
        {label:'Operations · Schedules', kind:'page', page:'schedules',hint:'g+e', kw:'schedule cron recurring'},
        {label:'Operations · Git',          kind:'page', page:'git',                    kw:'versioning repo repository commit branch'},
        {label:'Operations · Nodes (on the Proxmox screen)', kind:'page', page:'proxmox', kw:'nodes inventory guest lxc qemu credential revoke turnOn turnOff console'},
        {label:'Operations · Proxmox',      kind:'page', page:'proxmox',     kw:'proxmox pve hypervisor tasks upid disks smart snapshot ram load cpu memory disk network filter health'},
        {label:'Operations · Deploy',       kind:'page', page:'deploy',      hint:'g+p', kw:'deploy paas publish release rollback apps'},
        {label:'Apps · WhatsApp',          kind:'page', page:'whatsapp',    hint:'g+w', kw:'zap whats messages'},
        {label:'Apps · Video call',      kind:'page', page:'videocall',              kw:'video call meeting meet'},
        {label:'Apps · Browser',         kind:'page', page:'browser',   hint:'g+b', kw:'browser web browse'},
        {label:'Apps · Persistent browser',kind:'page',page:'persistent',             kw:'browser persistent vnc session'},
        {label:'Settings',            kind:'page', page:'config',      hint:'g+r', kw:'settings config preferences'},
      ],
      actions: [
        {label:'Focus mode',         kind:'action', do:'toggleFocusMode'},
        {label:'Hide / show the top bar (Ctrl+Shift+H)', kind:'action', do:'toggleChromeCollapsed'},
        {label:'Sign out',                       kind:'action', do:'logout'},
        {label:'Reload page',          kind:'action', do:'reloadPage'},
        {label:'Terminal: new pane',                     kind:'action', do:'paletteTermNewPane'},
        {label:'Terminal: clear screen',                     kind:'action', do:'paletteTermClear'},
        {label:'Terminal: reset (fixes a stuck terminal)',kind:'action', do:'paletteTermReset'},
        {label:'Terminal: select all',                 kind:'action', do:'paletteTermSelectAll'},
        {label:'Terminal: save scrollback…',              kind:'action', do:'paletteTermSaveScrollback'},
        {label:'Terminal: search (Ctrl+F)',                 kind:'action', do:'paletteTermSearch'},
        {label:'Terminal: send ^C (SIGINT)',              kind:'action', do:'paletteTermSigInt'},
        {label:'Terminal: send ^D (EOF)',                 kind:'action', do:'paletteTermSigEOF'},
        {label:'Terminal: send ^Z (suspend)',           kind:'action', do:'paletteTermSigSusp'},
        {label:'Terminal: close pane (session stays alive)', kind:'action', do:'paletteTermDetach'},
        {label:'Terminal: split horizontally',           kind:'action', do:'paletteTermSplitH'},
        {label:'Terminal: split vertically',             kind:'action', do:'paletteTermSplitV'},
        {label:'Terminal: toggle broadcast (types into every pane)', kind:'action', do:'paletteTermBroadcast'},
        {label:'Terminal: jump to the next prompt',        kind:'action', do:'paletteTermJumpNext'},
        {label:'Terminal: jump to the previous prompt',       kind:'action', do:'paletteTermJumpPrev'},
        {label:'Terminal: reconnect the active pane',           kind:'action', do:'paletteTermReconnect'},
        {label:'Settings · Account security (MFA / 2FA)', kind:'action', do:'paletteOpenSecurityMFA', kw:'password password mfa 2fa totp autenticador security backup codes'},
      ],
    },
    shortcutsOpen: false,
    panelHealth: null,
    notifications: [],
    bellOpen: false,
    lastSeenNotification: parseInt(localStorage.getItem('panel_last_seen_notif') || '0', 10),
    clockNow: '',
    sortState: safeJSON('panel_sort_state', {}),
    sessions: [],
    sessionsLoading: false,
    sessionsLoaded: false,
    sessionsError: '',
    adguard: {},
    adguardLoading: false,
    adguardLoaded: false,
    adguardError: '',
    _adguardTimer: null,
    tunnelDevices: [],
    tunnelLoading: false,
    tunnelLoaded: false,
    tunnelError: '',
    tunnelSummary: null,
    tunnelBusy: false,
    tunnelAddOpen: false,
    tunnelNewName: '',
    tunnelLinkOpen: false,
    tunnelLinkName: '',
    tunnelLinkText: '',
    tunnelQrSrc: '',
    tunnelLinkVariant: 'ws',
    tunnelLinkUuid: '',
    _tunnelTimer: null,
    deviceUsage: {},
    _usageTimer: null,
    dsStatus: { settings:{}, saved:{ orig:0, out:0, imgs:0, reqs_cut:0, pct:0 }, bypass:[], has_ca:false },
    dsForm: { enabled:true, quality:40, maxdim:1280, strip_trackers:true, greyscale:false, video_low:true },
    callEconomy: (localStorage.getItem('panel_vc_quality') || 'economy'),
    dsBypassText: '',
    dsLoading: false,
    dsLoaded: false,
    dsError: '',
    dsBusy: false,
    _dsTimer: null,
    editor: { open:false, path:'', language:'plaintext', dirty:false, loaded:false, saving:false, _inst:null, _model:null, _initial:'' },
    bulkSel: { containers: [], images: [], volumes: [] },
    sysAct: { running: '', output: '' },
    systemLogs: [],
    logTail: { path: '', ws: null, buffer: '', connected: false },
    ufw: { installed:false, enabled:false, output:'', newAction:'allow', newSpec:'' },
    cron: { content: '', serverContent: null, loaded: false, loading: false, error: '', saving: false },
    composeWizardOpen: false,
    composeForm: { project_name:'', dir:'', content:'' },
    auditFilter: (()=>{ try { const raw=localStorage.getItem('panel_audit_filter'); if(raw){ const obj=JSON.parse(raw); return Object.assign({user:'',action:'',q:'',from:'',to:'',limit:200}, obj); } } catch(_) {} return { user:'', action:'', q:'', from:'', to:'', limit:200 }; })(),
    auditResults: [],
    auditActions: [],
    auditLoading: false,
    newSnippet: {name:'', cmd:''},
    termThemes: {
      dark:        { label:'Dark (default)',   background:'#020617', foreground:'#e5e7eb', cursor:'#22d3ee', selectionBackground:'#155e75' },
      dracula:     { label:'Dracula',          background:'#282a36', foreground:'#f8f8f2', cursor:'#bd93f9', selectionBackground:'#44475a' },
      nord:        { label:'Nord',             background:'#2e3440', foreground:'#d8dee9', cursor:'#88c0d0', selectionBackground:'#434c5e' },
      tokyonight:  { label:'Tokyo Night',      background:'#1a1b26', foreground:'#a9b1d6', cursor:'#7aa2f7', selectionBackground:'#283457' },
      catppuccin:  { label:'Catppuccin Mocha', background:'#1e1e2e', foreground:'#cdd6f4', cursor:'#f5e0dc', selectionBackground:'#45475a' },
      onedark:     { label:'One Dark',         background:'#282c34', foreground:'#abb2bf', cursor:'#528bff', selectionBackground:'#3e4451' },
      gruvbox:     { label:'Gruvbox Dark',     background:'#282828', foreground:'#ebdbb2', cursor:'#fb4934', selectionBackground:'#504945' },
      solarized:   { label:'Solarized Dark',   background:'#002b36', foreground:'#839496', cursor:'#93a1a1', selectionBackground:'#073642' },
      solarizedl:  { label:'Solarized Light',  background:'#fdf6e3', foreground:'#586e75', cursor:'#268bd2', selectionBackground:'#eee8d5' },
      monokai:     { label:'Monokai',          background:'#272822', foreground:'#f8f8f2', cursor:'#a6e22e', selectionBackground:'#49483e' },
      github:      { label:'GitHub Light',     background:'#ffffff', foreground:'#24292e', cursor:'#0969da', selectionBackground:'#c8e1ff' },
      light:       { label:'Light (simple)',  background:'#ffffff', foreground:'#1f2937', cursor:'#2563eb', selectionBackground:'#bfdbfe' },
    },
    termFonts: {
      jetbrains: { label:'JetBrains Mono',  family:'"JetBrains Mono", ui-monospace, Menlo, Consolas, monospace' },
      fira:      { label:'Fira Code',       family:'"Fira Code", ui-monospace, Menlo, Consolas, monospace' },
      cascadia:  { label:'Cascadia Code',   family:'"Cascadia Code", ui-monospace, Menlo, Consolas, monospace' },
      ibm:       { label:'IBM Plex Mono',   family:'"IBM Plex Mono", ui-monospace, Menlo, Consolas, monospace' },
      jbmono:    { label:'JetBrains (no ligatures)', family:'"JetBrains Mono NL", ui-monospace, Menlo, Consolas, monospace' },
      system:    { label:'System (fallback)',       family:'ui-monospace, "SF Mono", Menlo, Consolas, monospace' },
    },
    hostTermFont: localStorage.getItem('panel_term_font') || 'jetbrains',
    hostMobileToolbar: (localStorage.getItem('panel_term_mobile_toolbar') ?? (window.matchMedia && window.matchMedia('(hover: none) and (pointer: coarse)').matches ? '1' : '0')) === '1',
    kbInset: 0,
    kbOpen: false,
    _wantKeyboard: false,
    _vvRaf: 0,
    termChromeHidden: false,
    paneRadial: { open: false, x: 0, y: 0, paneId: null },
    termMenuOpen: false,
    tabMgrOpen: false,
    termSel: { open: false, x: 0, y: 0 },
    termBarCatalog: [
      { key:'aa',        icon:'Aa',     label:'Appearance & font' },
      { key:'clear',     icon:'Clear', label:'Clear the screen' },
      { key:'reconnect', icon:'↻',      label:'Reconnect' },
      { key:'keyboard',  icon:'⌨',      label:'Show/hide keyboard' },
      { key:'keys',      icon:'⎋',      label:'Special keys (Esc/Tab/^C)' },
      { key:'search',    icon:'🔍',     label:'Search the terminal' },
      { key:'kill',      icon:'🗑',     label:'Kill session' },
      { key:'focus',     icon:'⤢',      label:'Focus mode' },
      { key:'sessions',   icon:'🪟',     label:'Sessions' },
      { key:'snippets',  icon:'⌘',      label:'Snippets' },
      { key:'hide',      icon:'▾',      label:'Hide the top bar' },
    ],
    termBarButtons: (function(){ const DEFAULTS = ['aa','clear','reconnect','sessions','hide'];
      try { const v = JSON.parse(localStorage.getItem('panel_term_bar')||'null');
        return Array.isArray(v) ? v.map(k => k === 'tmux' ? 'sessions' : k) : DEFAULTS.slice();
      } catch(_) { return DEFAULTS.slice(); } })(),
    pwaPrompt: null,
    pwaInstallable: false,
    pwaInstalled: window.matchMedia && window.matchMedia('(display-mode: standalone)').matches,
    usersList: [],
    usersLoaded: false,
    usersModal: { open: false, mode: 'create', form: { username: '', password: '' }, loading: false, error: '' },
    abandonedOpen: false,
    abandonedSessions: [],
    pollTimer: null,
    jobsPollTimer: null,
    deployPollTimer: null,
    statsError: null,
    statsAt: 0,
    dockerError: null,

    jobsLoading: false,        jobsLoaded: false,        jobsError: '',
    todosLoading: false,       todosLoaded: false,       todosError: '',
    schedJobsLoading: false,   schedJobsLoaded: false,   schedJobsError: '',
    schedNotifyChannelsLoaded: false, schedNotifyChannelsError: '',
    schedRemotesError: '',
    panelHealthLoading: false,  panelHealthLoaded: false,  panelHealthError: '',
    systemLogsLoading: false,  systemLogsLoaded: false,  systemLogsError: '',
    ufwLoading: false,         ufwLoaded: false,         ufwError: '',
    auditActionsLoaded: false, auditActionsError: '',
    abandonedSessionsLoading: false, abandonedSessionsLoaded: false, abandonedSessionsError: '',
    statsLoading: false,
    toasts: [],
    _toastSeq: 0,
    pendingActions: {},
    bulkProgress: { done:0, total:0, label:'', running:false, fails:[] },

    async ensureAuthCookie() {
      if (!this.token) return;
      if (/(?:^|;\s*)panel_cookie_set=1\b/.test(document.cookie)) return;
      try {
        const r = await fetch('/api/auth/refresh', { method:'POST', headers:{'Authorization':'Bearer '+this.token} });
        if (!r.ok) return;
        const data = await r.json();
        if (data && data.token) {
          this.token = data.token;
          localStorage.setItem('panel_token', this.token);
        }
      } catch (e) { /* stays on the Bearer; the next actions will revalidate */ }
    },

    async init() {
      try { this._telWire(); this._telHit('default'); } catch (_) {}
      if (this._initDone) {
        this.scheduleTokenRefresh();
        this.loadUserEmail();
        try { await this._termInitialSync(); } catch(e){ console.warn('[term-sync] re-sync failed:', e); }
        return;
      }
      if (/vc_guest=1/.test(location.search || '')) {
        const t = sessionStorage.getItem('panel_vc_guest_token');
        const rid = sessionStorage.getItem('panel_vc_guest_room_id');
        const exp = parseInt(sessionStorage.getItem('panel_vc_guest_expires_at') || '0', 10);
        if (!t || !rid) { location.href = '/join'; return; }
        if (exp > 0 && exp * 1000 < Date.now()) {
          try { sessionStorage.clear(); } catch (_) {}
          location.href = '/join?expired=1'; return;
        }
        this.guestMode = true;
        this.guestRoomId = rid;
        this.guestRoomName = sessionStorage.getItem('panel_vc_guest_room_name') || '';
        this.token = t;
        this.username = localStorage.getItem('panel_vc_guest_name') || 'Guest';
        this.videocall.lobbySkipNext = true;
        this.vcInstallShortcuts();
        this.$nextTick(() => {
          this.setPage('videocall', { silent: true });
          this.$nextTick(() => this.vcJoinCall(rid));
        });
        this._initDone = true;
        return;
      }
      if (!this.token) return;
      this._initDone = true;
      await this.ensureAuthCookie();
      this.scheduleTokenRefresh();
      this._wireOpportunisticRefresh();
      this._maybeRefreshSoon(60 * 60 * 1000);
      this.loadUserEmail();
      this.procsLayoutLoad();
      this._observeTabBar();


      this.terms = this._termsClaude;

      try {
        await this._termInitialSync();
      } catch(e){ console.warn('[term-sync] initial sync failed:', e); }

      window.addEventListener('storage', (ev) => {
        if (!ev.key) return;
        if (ev.key.endsWith('_term_workspaces') || ev.key === this._termWorkspacesKey) {
          try {
            const list = JSON.parse(ev.newValue || '[]') || [];
            this.termWorkspaces = list;
          } catch(_){}
        }
      });

      this.restoreState();

      window.addEventListener('beforeunload', (e) => {
        try { this.saveState(); } catch(_){}
        try { this._termFlushAllOnUnload(); } catch(_){}
        if (this.deploy.env.dirty) { e.preventDefault(); e.returnValue=''; }
      });
      window.addEventListener('pagehide', () => {
        try { this.saveState(); } catch(_){}
        try { this._termFlushAllOnUnload(); } catch(_){}
      });

      if (/^#videocall=join/.test(location.hash || '')) {
        setTimeout(() => this.vcConsumeInviteFromHash(), 50);
      }

      if (this.tabs.operations === 'docs' || this.tabs.operations === 'prompts') {
        this.tabs.operations = 'tasks';
        try { localStorage.setItem('panel_tabs', JSON.stringify(this.tabs)); } catch(_){}
      }

      if (this.page === 'terminals') { this.page = 'dev'; try { localStorage.setItem('panel_page', 'dev'); } catch(_){} }
      if (this.tabs && this.tabs.terminals !== undefined) {
        if (this.tabs.dev === undefined) this.tabs.dev = this.tabs.terminals;
        delete this.tabs.terminals;
        try { localStorage.setItem('panel_tabs', JSON.stringify(this.tabs)); } catch(_){}
      }

      const rawHash = location.hash || '';
      const isMagicHash = rawHash.indexOf('=') >= 0 || rawHash.indexOf('&') >= 0;
      this._bootHadHashNav = this._applyHashFromURL(rawHash) || isMagicHash;
      if (!isMagicHash) this._navSyncHistory('replace');
      this._updateTitle();

      window.addEventListener('popstate', () => { this._applyHashFromURL(location.hash); });
      window.addEventListener('hashchange', () => { this._applyHashFromURL(location.hash); });
      try { this._prefsReconcile(); } catch(_){}

      document.addEventListener('visibilitychange', () => {
        if (this.currentView !== 'persistent' || !this.brPersistentMounted) return;
        if (document.hidden) {
          if (this.bpPauseTimer) { clearTimeout(this.bpPauseTimer); this.bpPauseTimer = null; }
          this.bpPauseTimer = setTimeout(() => { this.bpActive = false; this.bpPauseTimer = null; }, 10000);
        } else {
          if (this.bpPauseTimer) { clearTimeout(this.bpPauseTimer); this.bpPauseTimer = null; }
          if (!this.bpActive) this.bpActive = true;
        }
      });

      await this.loadStats();
      this.loadConfig();
      this.loadPanelHealth();
      this.loadContainers();
      this._triggerViewLoaders(this.currentView);
      this.loadJiraConfig().then(() => { if (this.jiraConfig.has_token) this.loadJiraHealth(); });
      this.loadJobs();
      this.jobsPrefsLoad();
      this.loadClaudeVersions();
      this.jiraColSortLoad();
      this.jiraPrefsLoad();
      this.jobsPollTimer = setInterval(() => { if (document.hidden || this._skipPoll('jobs')) return; if (this.currentView !== 'jobs') this.loadJobs(); }, 15*1000);
      this.jobsLiveTimer = setInterval(() => {
        if (document.hidden || this._skipPoll('jobsLive')) return;
        if (this.currentView === 'jobs' && (this.jobsCounts.running > 0 || this.jobsCounts.queued > 0)) this.loadJobs();
      }, 5*1000);
      this.jobsClockTimer = setInterval(() => {
        if (document.hidden) return;
        if (this.currentView === 'jobs') this.jobsNow = Math.floor(Date.now()/1000);
      }, 1000);
      this.whatsappInit();
      this.loadBandwidth();
      this.bwPollTimer = setInterval(() => { if (!document.hidden && !this._skipPoll('bandwidth')) this.loadBandwidth(); }, 10000);
      this.pollTimer = setInterval(()=>{ if(document.hidden || this._skipPoll('stats')) return; this.loadStats(); if(['containers','dashboard'].includes(this.page)) this.loadContainers(); if(this.currentView==='alerts') { this.loadMetricSnapshot(); if(!this.alertFormOpen) this.loadAlertRules(); } if(this.currentView==='history') { this.loadAlertRules(); this.loadHistory().then(()=>this.drawCharts()); } }, 5000);
      this.clockAlertsTimer = setInterval(()=>{ if(document.hidden) return; if(this.currentView==='alerts') this.clockNowSec = Math.floor(Date.now()/1000); }, 1000);

      window.addEventListener('online', () => this._reconnectPanesNow());
      document.addEventListener('visibilitychange', () => {
        if (!document.hidden) { this._reconnectPanesNow(); this._reconcileSizes(); }
      });
      window.addEventListener('focus', () => this._reconcileSizes());

      try {
        this._mobileMQ = window.matchMedia('(max-width: 767.98px)');
        const onChange = () => {
          this.mobileChangeTick++;
          const fs = this._termFontSize();
          (this.terms.panes||[]).forEach(p => { if (p.term) { p.term.options.fontSize = fs; this._fitSoon(p.fit); } });
          if (this.page === 'dev') this.$nextTick(()=>this.renderPaneLayout());
        };
        if (this._mobileMQ.addEventListener) this._mobileMQ.addEventListener('change', onChange);
        else if (this._mobileMQ.addListener) this._mobileMQ.addListener(onChange);
      } catch(e){}

      this.installGlobalHotkeys();
      this.installTerminalHotkeys();
      this.installPWA();
      this.installViewportKeyboard();
      try {
        const valid = new Set((this.termBarCatalog||[]).map(b => b.key));
        const cleaned = (this.termBarButtons||[]).filter(k => valid.has(k));
        if (cleaned.length !== (this.termBarButtons||[]).length) {
          this.termBarButtons = cleaned;
          localStorage.setItem('panel_term_bar', JSON.stringify(cleaned));
        }
      } catch(_){}

      if (this.$watch) {
        const persist = (key) => (v) => {
          try { localStorage.setItem(key, JSON.stringify(v||{})); } catch(e){}
        };
        const persistStr = (key) => (v) => {
          try { localStorage.setItem(key, String(v == null ? '' : v)); } catch(e){}
        };
        this.$watch('filter',      persist('panel_filters'));
        this.$watch('portsFilter', persist('panel_ports_filter'));
        this.$watch('connsFilter', persist('panel_conns_filter'));
        this.$watch('jiraFilter',    persistStr('panel_jira_filter'));
        this.$watch('jiraCustomJQL', persistStr('panel_jira_jql'));
        this.$watch('jiraSearch',    persistStr('panel_jira_search'));
      }

      try {
        if (window.PanelSTT && window.PanelSTT.availableBackends) {
          const n = window.PanelSTT.availableBackends().length;
          this.videocall.subtitlesSupported = n > 0;
          this.videocall.subtitlesNoEngine = n === 0;
        }
        if (window.PanelSTT && window.PanelSTT.probeWhisperLocal) {
          window.PanelSTT.probeWhisperLocal().then(ok => {
            this.videocall.whisperLocalAvailable = !!ok;
            const explicit = localStorage.getItem('panel_vc_stt_backend_explicit') === '1';
            if (ok && this.videocall.sttBackend === 'web-speech' && !explicit) {
              this.videocall.sttBackend = 'whisper-local';
              try { localStorage.setItem('panel_vc_stt_backend', 'whisper-local'); } catch(_) {}
            }
          }).catch(() => { this.videocall.whisperLocalAvailable = false; });
        }
      } catch(_) {}

      this.clockNow = new Date().toLocaleTimeString();
      this.clockTimer = setInterval(()=>{ if(document.hidden) return; this.clockNow = new Date().toLocaleTimeString(); }, 1000);

      this.pollNotifications();
      this.notificationTimer = setInterval(()=>{ if(!document.hidden && !this._skipPoll('notif')) this.pollNotifications(); }, 15000);

      this.loadNotifyInbox();
      this.notifyInboxTimer = setInterval(()=>{ if(!document.hidden && !this._skipPoll('inbox')) this.loadNotifyInbox(); }, 20000);

      if (window.PanelPresence) {
        window.PanelPresence.connect({
          token: this.token,
          onIncoming: (msg) => this.vcOnIncoming(msg),
          onEvent: (msg) => this.vcOnPresenceEvent(msg),
          onState: (ev) => { this.videocall.presenceConnected = (ev.type === 'connected'); },
        });
      }
      this._installGlobalDropGuard();
      this.vcInstallShortcuts();
      this.vcInstallDropHandlers();
      this.vcInstallSpotlightClicks();
      this.vcTickTimer = setInterval(() => { this.videocall._nowTick = Math.floor(Date.now() / 1000); }, 30000);

      if (window.PanelPush && window.PanelPush.isSupported()) {
        window.PanelPush.getState(this.token).then(st => {
          this.videocall.pushSubscribed = !!st.subscribed;
          this.videocall.pushPermission = st.permission || 'default';
        }).catch(()=>{});
        window.PanelPush.onAcceptMessage((roomId) => {
          this.setPage('videocall');
          this.$nextTick(() => this.vcJoinCall(roomId));
        });
      }
      const accMatch = (location.hash || '').match(/[#&]videocall=accept[^&]*[&]room=([^&]+)/);
      if (accMatch) {
        setTimeout(() => {
          this.setPage('videocall');
          this.$nextTick(() => this.vcJoinCall(decodeURIComponent(accMatch[1])));
          try { history.replaceState(null, '', '#videocall'); } catch (_) {}
        }, 100);
      }
    },

    validPages: ['dashboard','history','alerts','containers','compose','images','volumes','networks','prune','processes','ports','systemd','files','terminal','browser','persistent','ai','users','secrets','audit','maintenance','jobs','schedules','prompts','config','whatsapp','videocall'],

    icons: (function(){
      const D = (fill, stroke) =>
        '<svg viewBox="0 0 24 24" overflow="visible">' +
          '<g class="icon-fill">' + fill + '</g>' +
          '<g class="icon-stroke">' + stroke + '</g>' +
        '</svg>';
      return {
        dashboard: D(
          '<rect x="3" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5"/>',
          '<rect x="3" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="3" width="7.5" height="7.5" rx="1.5"/><rect x="3" y="13.5" width="7.5" height="7.5" rx="1.5"/><rect x="13.5" y="13.5" width="7.5" height="7.5" rx="1.5"/>'
        ),
        history: D(
          '<path d="M3 21V3h.5v18zM3.5 17l5-5 3 3 4-5 4 4v6h-16z"/>',
          '<path d="M3 3v18h18"/><path d="M7 14l4-4 3 3 5-6"/><circle cx="11" cy="13" r="1.2"/><circle cx="14" cy="16" r="1.2"/><circle cx="19" cy="7" r="1.2"/>'
        ),
        alerts: D(
          '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9z"/>',
          '<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9z"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>'
        ),
        containers: D(
          '<path d="M12 12L4 7.5v8.5l8 4.5z"/>',
          '<path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><path d="M3.3 7L12 12l8.7-5"/><path d="M12 22V12"/>'
        ),
        compose: D(
          '<path d="M2 12l10 5 10-5-10-5z"/>',
          '<path d="M12 2l10 5-10 5L2 7z"/><path d="M2 12l10 5 10-5"/><path d="M2 17l10 5 10-5"/>'
        ),
        images: D(
          '<rect x="3" y="3" width="18" height="18" rx="2.5"/>',
          '<rect x="3" y="3" width="18" height="18" rx="2.5"/><circle cx="8.5" cy="9" r="1.7"/><path d="M21 16l-5-5-7 7"/>'
        ),
        volumes: D(
          '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v6c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
          '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>'
        ),
        networks: D(
          '<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/>',
          '<circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><path d="M8.6 13.5l6.8 4M15.4 6.5l-6.8 4"/>'
        ),
        prune: D(
          '<path d="M5 8h14l-1.5 12a2 2 0 0 1-2 1.9H8.5a2 2 0 0 1-2-1.9z"/>',
          '<path d="M3 6h18"/><path d="M19 6l-1.4 14.1a2 2 0 0 1-2 1.9H8.4a2 2 0 0 1-2-1.9L5 6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6M14 11v6"/>'
        ),
        processes: D(
          '<rect x="4" y="4" width="16" height="16" rx="2.5"/>',
          '<rect x="4" y="4" width="16" height="16" rx="2.5"/><rect x="9" y="9" width="6" height="6" rx="1"/><path d="M9 2v2M15 2v2M9 20v2M15 20v2M2 9h2M2 15h2M20 9h2M20 15h2"/>'
        ),
        ports: D(
          '<path d="M13 2L4 13.5h7.5L11 22l9-12h-7.5z"/>',
          '<path d="M13 2L4 13.5h7.5L11 22l9-12h-7.5z"/>'
        ),
        systemd: D(
          '<circle cx="12" cy="12" r="9"/>',
          '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.6h0a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>'
        ),
        files: D(
          '<path d="M3 7a2 2 0 0 1 2-2h4.5l2 2H19a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>',
          '<path d="M3 7a2 2 0 0 1 2-2h4.5l2 2H19a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>'
        ),
        terminal: D(
          '<rect x="2" y="4" width="20" height="16" rx="2.5"/>',
          '<rect x="2" y="4" width="20" height="16" rx="2.5"/><path d="M6 9l3 3-3 3"/><path d="M13 15h5"/>'
        ),
        secrets: D(
          '<rect x="4" y="11" width="16" height="11" rx="2.5"/>',
          '<rect x="4" y="11" width="16" height="11" rx="2.5"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/><circle cx="12" cy="16.5" r="1.4"/>'
        ),
        users: D(
          '<circle cx="9" cy="8" r="3.2"/><path d="M3 21v-1a6 6 0 0 1 12 0v1"/>',
          '<circle cx="9" cy="8" r="3.2"/><path d="M3 21v-1a6 6 0 0 1 12 0v1"/><circle cx="17" cy="10" r="2.5"/><path d="M14 21v-.5a4 4 0 0 1 7-2.5"/>'
        ),
        audit: D(
          '<path d="M5 3a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-5-5z"/>',
          '<path d="M5 3a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8l-5-5z"/><path d="M14 3v5h5"/><path d="M7 13h8M7 17h6"/>'
        ),
        claude: D(
          '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/>',
          '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 16l.7 1.9 1.9.7-1.9.7-.7 1.9-.7-1.9-1.9-.7 1.9-.7z"/>'
        ),
        ai: D(
          '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/>',
          '<rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><circle cx="7" cy="7.5" r=".7"/><circle cx="7" cy="16.5" r=".7"/><path d="M11 7.5h7M11 16.5h7"/>'
        ),
        operations: D(
          '<path d="M14.7 6.3a4.5 4.5 0 0 1 0 6.4l-7 7a2 2 0 0 1-2.8-2.8l7-7a4.5 4.5 0 0 1 6.4 0z"/>',
          '<path d="M14.7 6.3a4.5 4.5 0 0 1 0 6.4l-7 7a2 2 0 0 1-2.8-2.8l7-7a4.5 4.5 0 0 1 6.4 0z"/><path d="M18 2l-3 3 2 2 3-3-2-2z"/><circle cx="6" cy="18" r=".8"/>'
        ),
        agents: D(
          '<rect x="7" y="7" width="10" height="10" rx="1.5"/>',
          '<rect x="7" y="7" width="10" height="10" rx="1.5"/><path d="M10 4v3M14 4v3M10 17v3M14 17v3M4 10h3M4 14h3M17 10h3M17 14h3"/>'
        ),
        deploy: D(
          '<path d="M12 3c3 0 6 3.4 6 8.2L15 14H9L6 11.2C6 6.4 9 3 12 3z"/>',
          '<path d="M12 3c3 0 6 3.4 6 8.2L15 14H9L6 11.2C6 6.4 9 3 12 3z"/><circle cx="12" cy="9" r="1.6"/><path d="M9 16l-1.5 4M15 16l1.5 4"/>'
        ),
        config: D(
          '<circle cx="9" cy="7" r="2.4"/><circle cx="16" cy="17" r="2.4"/>',
          '<path d="M3 7h4M11 7h10"/><circle cx="9" cy="7" r="2.4"/><path d="M3 17h11M18 17h3"/><circle cx="16" cy="17" r="2.4"/>'
        ),
        browser: D(
          '<circle cx="12" cy="12" r="9"/>',
          '<circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3c2.5 2.5 3.8 5.6 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.6-3.8-9S9.5 5.5 12 3z"/>'
        ),
        persistent: D(
          '<path d="M12 2v6"/><path d="M12 22v-7"/><circle cx="12" cy="11" r="3"/><path d="M9 8h6"/>',
          '<path d="M12 2v6"/><path d="M12 22v-7"/><circle cx="12" cy="11" r="3"/><path d="M9 8h6"/>'
        ),
        videocall: D(
          '<rect x="2" y="7" width="13" height="10" rx="2"/>',
          '<rect x="2" y="7" width="13" height="10" rx="2"/><path d="M15 10l6-3v10l-6-3z"/><circle cx="6" cy="12" r="1.2"/>'
        ),
        externalLink:
          '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" overflow="visible">' +
          '<path d="M14 3h7v7"/><path d="M21 3l-9 9"/><path d="M19 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h6"/>' +
          '</svg>',
      };
    })(),

    games: {
      servers: [], loaded: false,
      selId: '',
      conn: {}, showPass: false,
      worlds: [], worldsLoaded: false,
      settings: null, settingsLoaded: false, gsDraft: {}, gsOrig: {}, settingsFilter: '',
      srvLoaded: false, srvDraft: {}, srvOrig: {}, srvRO: {}, srvBools: [],
      grpDraft: {}, grpOrig: {}, grpBools: [],
      groups: [], groupsLoaded: false, bans: [], bansText: '',
      rt: [], rtOrig: [], rtLoaded: false,
      samples: [], histHours: 6, build: {},
      rawText: '', rawPath: '', rawOpen: false,
      inv: [], invOpen: false, upName: '', upFile: null,
      sched: {}, durUnit: {}, enumCustom: {},
      trAvailable: false, trLoaded: false, trLoading: false, trSaving: false,
      trDirty: false, trCatalog: [], trApplied: [], trToggles: [], trValues: {},
      trEnabled: false, trPolicyOK: false, trPolicyReason: '',
      logs: '', logFilter: '', logTail: '200', logsAuto: false,
      backups: [], backupsLoaded: false,
      hist: { t: [], cpu: [], mem: [] },
      busy: '',
    },
    _gamesLogsTimer: null,
    _gamesPollTimer: null,

    async _gsFetch(path, opts) {
      const r = await this.api(path, opts);
      if (!r.ok) {
        let msg = 'HTTP ' + r.status;
        try { const e = await r.json(); if (e && e.error) msg = e.error; } catch (_) {}
        throw new Error(msg);
      }
      return await r.json();
    },

    gsSel() {
      return (this.games.servers || []).find(s => s.id === this.games.selId) || null;
    },

    async loadGames() {
      try {
        const d = await this._gsFetch('/api/gameservers');
        this.games.servers = d.servers || [];
        if (!this.games.selId && this.games.servers.length) this.games.selId = this.games.servers[0].id;
        this._gsPushHist();
      } catch (e) {
        this.showToast('Failed to load servers: ' + (e.message || e), 'err');
      } finally {
        this.games.loaded = true;
      }
    },

    async loadGameConn() {
      if (!this.games.selId) return;
      try { this.games.conn = await this._gsFetch('/api/gameservers/' + this.games.selId + '/connection'); }
      catch (_) { this.games.conn = {}; }
    },

    _gsPushHist() {
      const s = this.gsSel();
      if (!s || s.state !== 'running') return;
      const h = this.games.hist;
      h.t.push(Date.now()); h.cpu.push(+(s.cpuPerc || 0).toFixed(2)); h.mem.push(Math.round(s.memMb || 0));
      while (h.t.length > 60) { h.t.shift(); h.cpu.shift(); h.mem.shift(); }
    },

    gsManageTabs: [
      { tab: 'gs-worlds', label: 'Worlds', help: 'Switch the active world, duplicate, rename, download and upload.' },
      { tab: 'gs-settings', label: 'Settings', help: 'Game rules, name, slots, password, schedules and the raw file.' },
      { tab: 'gs-access', label: 'Access', help: 'Privilege groups with their own password, plus the ban list.' },
      { tab: 'gs-console', label: 'Console', help: 'Live server log, with a filter.' },
      { tab: 'gs-backups', label: 'Backups', help: 'Create, download and restore save backups.' },
    ],

    manageGame(s, tab) {
      this.selectGameById(s.id);
      this.setTab(tab);
    },

    selectGameById(id) {
      if (this.games.selId === id) return;
      this.games.selId = id;
      this.games.conn = {}; this.games.showPass = false;
      this.games.worlds = []; this.games.worldsLoaded = false;
      this.games.settings = null; this.games.settingsLoaded = false;
      this.games.srvLoaded = false; this.games.srvDraft = {}; this.games.srvOrig = {};
      this.games.grpDraft = {}; this.games.grpOrig = {}; this.games.srvRO = {};
      this.games.groups = []; this.games.groupsLoaded = false;
      this.games.bans = []; this.games.bansText = '';
      this.games.rt = []; this.games.rtLoaded = false;
      this.games.gsDraft = {}; this.games.gsOrig = {};
      this.games.logs = ''; this.games.backups = []; this.games.backupsLoaded = false;
      this.games.hist = { t: [], cpu: [], mem: [] };
    },

    gsStateLabel(s) {
      if (!s) return '—';
      if (s.state === 'running') return 'online';
      if (s.state === 'missing') return 'missing';
      if (s.state === 'exited') return 'stopped';
      return s.state || '—';
    },
    gsDotClass(s) {
      if (!s || s.state === 'missing') return 'gs-dot--off';
      if (s.state === 'running') return 'gs-dot--on';
      return 'gs-dot--warn';
    },
    gsBadgeClass(s) {
      if (!s || s.state === 'missing') return 'badge--danger';
      if (s.state === 'running') return 'badge--success';
      return 'badge--warning';
    },
    gsHeroMeta(s) {
      if (!s) return '';
      const bits = [s.game];
      if (this.games.conn.serverName) bits.push(this.games.conn.serverName);
      if (this.games.conn.slotCount) bits.push(this.games.conn.slotCount + ' slots');
      if (s.notes) bits.push(s.notes);
      return bits.filter(Boolean).join(' · ');
    },
    gsFmtMB(v) {
      v = Number(v || 0);
      return v >= 1024 ? (v / 1024).toFixed(2) + ' GB' : Math.round(v) + ' MB';
    },
    gsPct(v, max) {
      v = Number(v || 0); max = Number(max || 0);
      if (!max) return 0;
      return Math.min(100, Math.round((v / max) * 100));
    },
    gsBarClass(v, max) {
      const p = this.gsPct(v, max);
      if (p >= 90) return 'gs-bar__fill--crit';
      if (p >= 70) return 'gs-bar__fill--warn';
      return '';
    },

    gsIssues() {
      const out = [];
      const s = this.gsSel();
      if (!s) return out;
      if (s.err) out.push(s.err);
      if (s.state !== 'running') out.push('The server is not running — nobody can join.');
      if (this.gameSettingsDirtyCount() > 0) {
        out.push('There are ' + this.gameSettingsDirtyCount() + ' unsaved configuration change(s).');
      }
      if (this.gsPct(s.memMb, s.memLimitMb) >= 90) {
        out.push('Memory above 90% of the container limit — the server may be killed.');
      }
      if (this.games.backupsLoaded && this.games.backups.length === 0) {
        out.push('No backup exists for this server.');
      }
      return out;
    },
    gsLastBackup() {
      const b = this.games.backups[0];
      if (!b || !b.modified) return 'none yet';
      return 'last ' + this.timeAgo(Date.parse(b.modified) / 1000);
    },
    gsBackupTotal() {
      const mb = this.games.backups.reduce((a, b) => a + (b.sizeMb || 0), 0);
      return mb >= 1024 ? (mb / 1024).toFixed(2) + ' GB' : mb.toFixed(1) + ' MB';
    },
    gsBackupURL(b) {
      return '/api/gameservers/' + this.games.selId + '/backups/download?file=' + encodeURIComponent(b.file);
    },
    gsWorldMeta(w) {
      const bits = [];
      if (w.saveId) bits.push('id ' + w.saveId);
      bits.push((w.sizeMb || 0).toFixed(1) + ' MB');
      if (w.modified) bits.push('modified ' + this.timeAgo(Date.parse(w.modified) / 1000));
      return bits.join(' · ');
    },

    async gameAction(s, action) {
      const labels = { start: 'start', stop: 'stop', restart: 'restart' };
      if (action !== 'start') {
        const msg = 'Confirm ' + labels[action] + ' "' + s.name + '"?\n\nConnected players will be dropped.';
        if (!(await this.confirmAsync(msg))) return;
      }
      this.games.busy = s.id;
      try {
        await this._gsFetch('/api/gameservers/' + s.id + '/action', {
          method: 'POST', body: JSON.stringify({ action }),
        });
        this.showToast(s.name + ': ' + labels[action] + ' executed', 'ok');
        setTimeout(() => this.loadGames(), 1500);
      } catch (e) {
        this.showToast('Failed to ' + labels[action] + ': ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async loadGameWorlds() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/worlds');
        this.games.worlds = d.worlds || [];
      } catch (e) {
        this.showToast('Failed to load worlds: ' + (e.message || e), 'err');
      } finally { this.games.worldsLoaded = true; }
    },

    async switchGameWorld(w) {
      const msg = 'Activate the world "' + w.name + '"?\n\nThe server will be stopped, the current world archived '
        + 'and the chosen one installed. Connected players will be dropped.';
      if (!(await this.confirmAsync(msg))) return;
      this.games.busy = 'world';
      this.showToast('Switching world — the server will restart…', '');
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/worlds/switch', {
          method: 'POST', body: JSON.stringify({ world: w.name }),
        });
        this.showToast('Active world is now "' + w.name + '"', 'ok');
        await this.loadGameWorlds(); await this.loadGames();
      } catch (e) {
        this.showToast('Switch failed: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async duplicateGameWorld(w) {
      const name = await this.askInput({
        title: 'Duplicate world',
        label: 'Name of the copy of "' + w.name + '"',
        value: w.name + '-copy',
        placeholder: 'my-world',
        validate: (v) => {
          v = String(v || '').trim();
          if (!v) return 'Enter a name';
          if (/[\\/\s]/.test(v)) return 'No slashes or spaces';
          if (this.games.worlds.some(x => x.name === v)) return 'A world with that name already exists';
          return '';
        },
      });
      if (!name) return;
      this.games.busy = 'world';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/worlds/duplicate', {
          method: 'POST', body: JSON.stringify({ world: w.name, name: String(name).trim() }),
        });
        this.showToast('World duplicated as "' + String(name).trim() + '"', 'ok');
        await this.loadGameWorlds();
      } catch (e) {
        this.showToast('Failed to duplicate: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async deleteGameWorld(w) {
      const msg = 'Delete the world "' + w.name + '"?\n\nThe save files are removed from disk. '
        + 'This cannot be undone.';
      if (!(await this.confirmAsync(msg))) return;
      this.games.busy = 'world';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/worlds/delete', {
          method: 'POST', body: JSON.stringify({ world: w.name }),
        });
        this.showToast('World "' + w.name + '" deleted', 'ok');
        await this.loadGameWorlds();
      } catch (e) {
        this.showToast('Failed to delete: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async createGameBackup() {
      if (!this.games.selId) return;
      this.games.busy = 'backup';
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/backups/create', { method: 'POST' });
        this.showToast('Backup created: ' + d.file, 'ok');
        await this.loadGameBackups();
      } catch (e) {
        this.showToast('Failed to create backup: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async restoreGameBackup(b) {
      const msg = 'Restore "' + b.file + '"?\n\nThe server will be stopped and the current save overwritten. '
        + 'A zip of the current state is written first, so you can roll back.';
      if (!(await this.confirmAsync(msg))) return;
      this.games.busy = 'restore';
      this.showToast('Restoring — the server will stop and come back up…', '');
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/backups/restore', {
          method: 'POST', body: JSON.stringify({ file: b.file }),
        });
        if (d.warn) this.showToast(d.warn, 'err');
        else this.showToast('Backup restored', 'ok');
        await this.loadGameBackups(); await this.loadGames(); await this.loadGameWorlds();
      } catch (e) {
        this.showToast('Failed to restore: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async loadGameSettings() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/settings');
        this.games.settings = d;
        const gs = d.gameSettings || {};
        this.games.gsOrig = JSON.parse(JSON.stringify(gs));
        this.games.gsDraft = JSON.parse(JSON.stringify(gs));
      } catch (e) {
        this.games.settings = null;
        this.showToast('Failed to load settings: ' + (e.message || e), 'err');
      } finally { this.games.settingsLoaded = true; }
    },

    gameSettingsDiff() {
      const out = {};
      for (const k of Object.keys(this.games.gsDraft || {})) {
        if (JSON.stringify(this.games.gsDraft[k]) !== JSON.stringify(this.games.gsOrig[k])) {
          out[k] = this.games.gsDraft[k];
        }
      }
      return out;
    },
    gameSettingsDirtyCount() { return Object.keys(this.gameSettingsDiff()).length; },
    gsIsDirty(k) {
      return JSON.stringify(this.games.gsDraft[k]) !== JSON.stringify((this.games.gsOrig || {})[k]);
    },
    gsDirtyPreview() {
      return Object.keys(this.gameSettingsDiff()).slice(0, 4).map(k => this.gameSettingLabel(k)).join(', ');
    },
    resetGameSettings() { this.games.gsDraft = JSON.parse(JSON.stringify(this.games.gsOrig || {})); },

    GS_SETTING_GROUPS: [
      { title: 'Player',      match: /^player|^shroudTime|^foodBuff|^enableStarving|^fromHunger|^tombstone/ },
      { title: 'Progression',  match: /^experience|^perk/ },
      { title: 'Items & World', match: /^enableDurability|^mining|^plant|^resource|^factory|^weather|^fishing|^enableGlider|^dayTime|^nightTime|^curse/ },
      { title: 'Enemies',     match: /^enemy|^boss|^threat|^randomSpawner|^aggro|^pacify|^taming/ },
    ],
    gsSettingGroups() {
      const f = (this.games.settingsFilter || '').toLowerCase();
      const keys = Object.keys(this.games.gsDraft || {}).filter(k =>
        !f || k.toLowerCase().includes(f) || this.gameSettingLabel(k).toLowerCase().includes(f)).sort();
      const out = [];
      const used = new Set();
      for (const g of this.GS_SETTING_GROUPS) {
        const ks = keys.filter(k => g.match.test(k));
        ks.forEach(k => used.add(k));
        if (ks.length) out.push({ title: g.title, keys: ks });
      }
      const rest = keys.filter(k => !used.has(k));
      if (rest.length) out.push({ title: 'Other', keys: rest });
      return out;
    },

    async loadGameHistory() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId
          + '/history?hours=' + (this.games.histHours || 6));
        this.games.samples = d.samples || [];
        this.gsDrawCharts();
      } catch (_) { this.games.samples = []; }
    },
    async loadGameBuild() {
      if (!this.games.selId) return;
      try { this.games.build = await this._gsFetch('/api/gameservers/' + this.games.selId + '/build'); }
      catch (_) { this.games.build = {}; }
    },
    gsBuildLine() {
      const b = this.games.build || {};
      const bits = [];
      if (b.buildId) bits.push('build ' + b.buildId);
      if (b.lastUpdated) bits.push('updated ' + this.timeAgo(b.lastUpdated));
      if (b.sizeMb) bits.push((b.sizeMb / 1024).toFixed(1) + ' GB on disk');
      return bits.join(' · ') || 'no build information';
    },
    async updateGameNow() {
      if (!(await this.confirmAsync('Check for an update now?\n\n'
          + 'The container restarts — the restart is what triggers SteamCMD. Connected players will be dropped.'))) return;
      this.games.busy = 'update';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/update', { method: 'POST' });
        this.showToast('Restarted — SteamCMD checks for the update on boot', 'ok');
        setTimeout(() => { this.loadGames(); this.loadGameBuild(); }, 8000);
      } catch (e) {
        this.showToast('Failed: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    gsWorldExportURL(w) {
      return '/api/gameservers/' + this.games.selId + '/worlds/export?world=' + encodeURIComponent(w.name);
    },
    async uploadGameWorld() {
      const name = (this.games.upName || '').trim();
      if (!name || !this.games.upFile) return;
      this.games.busy = 'upload';
      try {
        const fd = new FormData();
        fd.append('name', name);
        fd.append('file', this.games.upFile);
        const r = await this.api('/api/gameservers/' + this.games.selId + '/worlds/import',
                                 { method: 'POST', body: fd });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
        this.showToast('World "' + name + '" sent', 'ok');
        this.games.upName = ''; this.games.upFile = null;
        await this.loadGameWorlds();
      } catch (e) {
        this.showToast('Failed to send: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async loadGameRaw() {
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/rawconfig');
        this.games.rawText = d.text || '';
        this.games.rawPath = d.path || '';
      } catch (e) {
        this.showToast('Failed to read the file: ' + (e.message || e), 'err');
      }
    },
    async saveGameRaw() {
      try { JSON.parse(this.games.rawText); }
      catch (e) { this.showToast('Invalid JSON: ' + e.message, 'err'); return; }
      if (!(await this.confirmAsync('Write the file and restart the server?\n\n'
          + 'A .bak of the previous file is kept.'))) return;
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/rawconfig', {
          method: 'POST', body: JSON.stringify({ text: this.games.rawText, restart: true }),
        });
        this.showToast('File written and server restarted', 'ok');
        this.games.settingsLoaded = false; this.games.srvLoaded = false;
        this.loadGames();
      } catch (e) {
        this.showToast('Not written: ' + (e.message || e), 'err');
      }
    },

    async loadGameInventory() {
      try {
        const d = await this._gsFetch('/api/gameservers/_inventory');
        this.games.inv = d.servers || [];
      } catch (e) {
        this.showToast('Failed to read inventory: ' + (e.message || e), 'err');
      }
    },
    async saveGameInventory() {
      try {
        await this._gsFetch('/api/gameservers/_inventory', {
          method: 'POST', body: JSON.stringify({ servers: this.games.inv }),
        });
        this.showToast('Inventory saved', 'ok');
        await this.loadGames();
      } catch (e) {
        this.showToast('Not saved: ' + (e.message || e), 'err');
      }
    },

    gsGroupPerms: [
      { k: 'canKickBan', label: 'Kick and ban', help: 'Allows kicking and banning other players from inside the game.' },
      { k: 'canAccessInventories', label: 'Open other players chests', help: 'Allows access to the chests and inventories of other players.' },
      { k: 'canEditWorld', label: 'Change the world', help: 'Allows digging and building outside bases.' },
      { k: 'canEditBase', label: 'Edit bases', help: 'Allows building and demolishing inside existing bases.' },
      { k: 'canExtendBase', label: 'Expand bases', help: 'Allows expanding the area of bases.' },
    ],

    gsGenPass() {
      const A = 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
      const n = new Uint32Array(14);
      (window.crypto || window.msCrypto).getRandomValues(n);
      return Array.from(n, x => A[x % A.length]).join('');
    },

    async loadGameGroups() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/groups');
        this.games.groups = d.groups || [];
      } catch (e) {
        this.showToast('Failed to load groups: ' + (e.message || e), 'err');
      } finally { this.games.groupsLoaded = true; }
    },
    addGameGroup() {
      this.games.groups.push({ name: 'New group', password: this.gsGenPass(),
        canKickBan: false, canAccessInventories: false, canEditWorld: false,
        canEditBase: false, canExtendBase: false, reservedSlots: 0 });
    },
    addPresetGroups() {
      this.games.groups = [
        { name: 'Admin', password: this.gsGenPass(), canKickBan: true, canAccessInventories: true,
          canEditWorld: true, canEditBase: true, canExtendBase: true, reservedSlots: 1 },
        { name: 'Friend', password: this.gsGenPass(), canKickBan: false, canAccessInventories: true,
          canEditWorld: true, canEditBase: true, canExtendBase: true, reservedSlots: 0 },
        { name: 'Visitor', password: this.gsGenPass(), canKickBan: false, canAccessInventories: false,
          canEditWorld: false, canEditBase: false, canExtendBase: false, reservedSlots: 0 },
      ];
      this.showToast('Template applied with generated passwords — review and save', '');
    },
    async removeGameGroup(i) {
      const g = this.games.groups[i];
      if (!(await this.confirmAsync('Remove the group "' + (g && g.name) + '"?\n\n'
          + 'Anyone using its password loses access to the server.'))) return;
      this.games.groups.splice(i, 1);
    },
    async saveGameGroups() {
      if (!(await this.confirmAsync('Save the groups and restart the server?\n\nConnected players will be dropped.'))) return;
      this.games.busy = 'groups';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/groups', {
          method: 'POST', body: JSON.stringify({ groups: this.games.groups, restart: true }),
        });
        this.showToast('Groups saved and server restarted', 'ok');
        await this.loadGameGroups(); this.loadGames();
      } catch (e) {
        this.showToast('Not saved: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    gsBansList() {
      return (this.games.bansText || '').split('\n').map(x => x.trim()).filter(Boolean);
    },
    gsBansCount() { return this.gsBansList().length; },
    async loadGameBans() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/bans');
        this.games.bans = d.bans || [];
        this.games.bansText = this.games.bans.join('\n');
      } catch (e) {
        this.showToast('Failed to load the ban list: ' + (e.message || e), 'err');
      }
    },
    async saveGameBans() {
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/bans', {
          method: 'POST', body: JSON.stringify({ bans: this.gsBansList() }),
        });
        this.showToast('Ban list saved — takes effect on the next start', 'ok');
        await this.loadGameBans();
      } catch (e) {
        this.showToast('Failed to save the ban list: ' + (e.message || e), 'err');
      }
    },

    async loadGameRuntime() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/runtime');
        this.games.rt = d.options || [];
        this.games.rtOrig = JSON.parse(JSON.stringify(this.games.rt));
        this.games.sched = {};
        this.games.rt.forEach(o => { if (o.kind === 'cron') this.games.sched[o.key] = this.gsCronParse(o.value); });
      } catch (e) {
        this.games.rt = [];
      } finally { this.games.rtLoaded = true; }
    },
    gsRtDirty() {
      let n = 0;
      (this.games.rt || []).forEach((o, i) => {
        if (!this.games.rtOrig[i] || o.value !== this.games.rtOrig[i].value) n++;
      });
      return n;
    },
    async saveGameRuntime() {
      const patch = {};
      (this.games.rt || []).forEach((o, i) => {
        if (!this.games.rtOrig[i] || o.value !== this.games.rtOrig[i].value) patch[o.key] = o.value;
      });
      if (!Object.keys(patch).length) return;
      if (!(await this.confirmAsync('Save the schedules and recreate the container?\n\n'
          + 'The server restarts; connected players will be dropped.'))) return;
      this.games.busy = 'rt';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/runtime', {
          method: 'POST', body: JSON.stringify({ patch }),
        });
        this.showToast('Schedules saved and container recreated', 'ok');
        await this.loadGameRuntime(); this.loadGames();
      } catch (e) {
        this.showToast('Failed: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    srvBoolsDef: [
      { k: 'enableVoiceChat', label: 'Voice chat', help: 'Enables voice chat on the server.' },
      { k: 'enableTextChat', label: 'Text chat', help: 'Enables text chat on the server.' },
    ],
    grpBoolsDef: [
      { k: 'canKickBan', label: 'Can kick/ban', help: 'Allows the group to kick and ban other players.' },
      { k: 'canAccessInventories', label: 'Can open chests', help: 'Allows access to the chest and inventory of other players.' },
      { k: 'canEditWorld', label: 'Can change the world', help: 'Allows digging and building outside bases.' },
      { k: 'canEditBase', label: 'Can edit bases', help: 'Allows building and demolishing inside existing bases.' },
      { k: 'canExtendBase', label: 'Can expand bases', help: 'Allows expanding the area of bases.' },
    ],

    async loadGameServerCfg() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/server');
        const srv = d.server || {}, grp = d.group || {};
        this.games.srvRO = srv._readonly || {};
        delete srv._readonly;
        this.games.srvOrig = JSON.parse(JSON.stringify(srv));
        this.games.srvDraft = JSON.parse(JSON.stringify(srv));
        this.games.grpOrig = JSON.parse(JSON.stringify(grp));
        this.games.grpDraft = JSON.parse(JSON.stringify(grp));
        this.games.srvBools = this.srvBoolsDef.filter(f => f.k in srv);
        this.games.grpBools = this.grpBoolsDef.filter(f => f.k in grp);
      } catch (e) {
        this.showToast('Failed to load server options: ' + (e.message || e), 'err');
      } finally { this.games.srvLoaded = true; }
    },

    _gsDiffOf(draft, orig) {
      const out = {};
      for (const k of Object.keys(draft || {})) {
        if (JSON.stringify(draft[k]) !== JSON.stringify((orig || {})[k])) out[k] = draft[k];
      }
      return out;
    },
    gsSrvDirtyCount() {
      return Object.keys(this._gsDiffOf(this.games.srvDraft, this.games.srvOrig)).length
           + Object.keys(this._gsDiffOf(this.games.grpDraft, this.games.grpOrig)).length;
    },
    resetServerCfg() {
      this.games.srvDraft = JSON.parse(JSON.stringify(this.games.srvOrig || {}));
      this.games.grpDraft = JSON.parse(JSON.stringify(this.games.grpOrig || {}));
    },

    async saveServerCfg(restart) {
      const srv = this._gsDiffOf(this.games.srvDraft, this.games.srvOrig);
      const grp = this._gsDiffOf(this.games.grpDraft, this.games.grpOrig);
      if (!Object.keys(srv).length && !Object.keys(grp).length) return;
      if (restart && !(await this.confirmAsync(
        'Save the server options and restart now?\n\nConnected players will be dropped.'))) return;
      this.games.busy = 'srv';
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/server', {
          method: 'POST', body: JSON.stringify({ server: srv, group: grp, restart: !!restart }),
        });
        this.games.srvOrig = JSON.parse(JSON.stringify(this.games.srvDraft));
        this.games.grpOrig = JSON.parse(JSON.stringify(this.games.grpDraft));
        if (d.restartErr) this.showToast('Saved, but the restart failed: ' + d.restartErr, 'err');
        else if (d.restarted) this.showToast('Server options saved and server restarted', 'ok');
        else this.showToast('Saved — takes effect only after a restart', '');
        this.loadGameConn(); this.loadGames();
      } catch (e) {
        this.showToast('Failed to save: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async renameGameWorld(w) {
      const name = await this.askInput({
        title: 'Rename world',
        label: 'New name for "' + w.name + '"',
        value: w.name,
        validate: (v) => {
          v = String(v || '').trim();
          if (!v) return 'Enter a name';
          if (/[\\/\s]/.test(v)) return 'No slashes or spaces here';
          if (v !== w.name && this.games.worlds.some(x => x.name === v)) return 'There is already a world with that name';
          return '';
        },
      });
      if (!name || String(name).trim() === w.name) return;
      this.games.busy = 'world';
      try {
        await this._gsFetch('/api/gameservers/' + this.games.selId + '/worlds/rename', {
          method: 'POST', body: JSON.stringify({ world: w.name, name: String(name).trim() }),
        });
        this.showToast('World renamed to "' + String(name).trim() + '"', 'ok');
        await this.loadGameWorlds(); await this.loadGames();
      } catch (e) {
        this.showToast('Failed to rename: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    GS_DOW: [
      { v: 0, label: 'Sunday' }, { v: 1, label: 'Monday' }, { v: 2, label: 'Tuesday' },
      { v: 3, label: 'Wednesday' }, { v: 4, label: 'Thursday' }, { v: 5, label: 'Friday' },
      { v: 6, label: 'Saturday' },
    ],
    GS_EVERY: [1, 2, 3, 4, 6, 8, 12],

    gsCronParse(expr) {
      const o = { mode: 'off', every: 6, hour: 6, minute: 0, dow: 1, raw: expr || '' };
      const e = (expr || '').trim();
      if (!e) return o;
      const f = e.split(/\s+/);
      if (f.length !== 5) { o.mode = 'custom'; return o; }
      const [mi, h, dom, mon, dow] = f;
      const num = (x) => /^\d+$/.test(x) ? parseInt(x, 10) : null;
      if (dom === '*' && mon === '*' && dow === '*' && num(mi) !== null) {
        const m = /^\*\/(\d+)$/.exec(h);
        if (m) { o.mode = 'interval'; o.every = parseInt(m[1], 10); o.minute = num(mi); return o; }
        if (num(h) !== null) { o.mode = 'daily'; o.hour = num(h); o.minute = num(mi); return o; }
      }
      if (dom === '*' && mon === '*' && num(dow) !== null && num(h) !== null && num(mi) !== null) {
        o.mode = 'weekly'; o.dow = num(dow); o.hour = num(h); o.minute = num(mi); return o;
      }
      o.mode = 'custom';
      return o;
    },

    gsCronBuild(o) {
      if (!o) return '';
      switch (o.mode) {
        case 'off': return '';
        case 'interval': return `${o.minute} */${o.every} * * *`;
        case 'daily': return `${o.minute} ${o.hour} * * *`;
        case 'weekly': return `${o.minute} ${o.hour} * * ${o.dow}`;
        default: return o.raw || '';
      }
    },

    gsCronLabel(o) {
      if (!o) return '';
      const hh = String(o.hour).padStart(2, '0'), mm = String(o.minute).padStart(2, '0');
      switch (o.mode) {
        case 'off': return 'Never — off';
        case 'interval': return `Every ${o.every}h, at minute ${mm}`;
        case 'daily': return `Every day at ${hh}:${mm}`;
        case 'weekly': {
          const d = this.GS_DOW.find(x => x.v === o.dow);
          return `Every ${d ? d.label : '?'} at ${hh}:${mm}`;
        }
        default: return 'Custom expression: ' + (o.raw || '');
      }
    },

    gsSched(key) {
      if (!this.games.sched[key]) this.games.sched[key] = this.gsCronParse('');
      return this.games.sched[key];
    },
    gsSyncCron(key) {
      const o = this.games.sched[key];
      const rt = (this.games.rt || []).find(x => x.key === key);
      if (rt && o) rt.value = this.gsCronBuild(o);
    },

    GS_UNITS: [
      { u: 'min', label: 'minutes', ns: 60000000000 },
      { u: 'hour', label: 'hours', ns: 3600000000000 },
    ],
    gsUnitNs(u) { const x = this.GS_UNITS.find(y => y.u === u); return x ? x.ns : 60000000000; },

    gsDurUnit(k) {
      if (!this.games.durUnit[k]) {
        const v = Number(this.games.gsDraft[k] || 0);
        this.games.durUnit[k] = v >= 3600000000000 ? 'hour' : 'min';
      }
      return this.games.durUnit[k];
    },
    gsDurGet(k) {
      const ns = Number(this.games.gsDraft[k] || 0);
      const val = ns / this.gsUnitNs(this.gsDurUnit(k));
      return Math.round(val * 100) / 100;
    },
    gsDurSet(k, v) {
      const n = parseFloat(v);
      if (isNaN(n) || n < 0) return;
      this.games.gsDraft[k] = Math.round(n * this.gsUnitNs(this.gsDurUnit(k)));
    },
    gsDurRange(k) {
      if (k === 'fromHungerToStarving') return { min: 5, max: 20, unit: 'min',
        hint: 'The game accepts 5 to 20 minutes.' };
      return null;
    },
    gsDurWarn(k) {
      const r = this.gsDurRange(k);
      if (!r) return '';
      const min = Number(this.games.gsDraft[k] || 0) / 60000000000;
      if (min < r.min || min > r.max) return 'Outside the accepted range (' + r.min + ' a ' + r.max + ' min)';
      return '';
    },

    gsFieldKind(k) {
      const m = this.gsMeta(k);
      const v = this.games.gsDraft[k];
      if (m.unit === 'ns') return 'duration';
      if (typeof v === 'boolean') return 'bool';
      if ((this.GS_ENUM_OPTS[k] || m.opts || []).length) return 'enum';
      if (typeof v === 'number') return m.def === 1 || m.def === 0.5 ? 'multiplier' : 'number';
      return 'text';
    },
    GS_MULT: [0.5, 1, 2, 5, 10],
    gsSetMult(k, v) { this.games.gsDraft[k] = v; },

    GS_ENUM_OPTS: {
      fishingDifficulty:        ['VeryEasy', 'Easy', 'Normal', 'Hard', 'VeryHard'],
      weatherFrequency:         ['Disabled', 'Rare', 'Normal', 'Often', 'Always'],
      randomSpawnerAmount:      ['Few', 'Normal', 'Many', 'Extreme'],
      aggroPoolAmount:          ['Few', 'Normal', 'Many', 'Extreme'],
      curseModifier:            ['Disabled', 'VeryEasy', 'Easy', 'Normal', 'Hard', 'VeryHard'],
      tombstoneMode:            ['Everything', 'AddBackpackMaterials', 'NoTombstone'],
      tamingStartleRepercussion:['KeepProgress', 'LoseSomeProgress', 'LoseAllProgress'],
      voiceChatMode:            ['Proximity', 'Global', 'Local'],
    },

    gsEnumOpts(k) {
      const opts = (this.GS_ENUM_OPTS[k] || this.gsMeta(k).opts || []).slice();
      const cur = this.games.gsDraft[k];
      if (cur && !opts.includes(cur)) opts.unshift(cur);
      return opts;
    },
    gsEnumCustom(k) { return !!this.games.enumCustom[k]; },
    gsToggleEnumCustom(k) { this.games.enumCustom[k] = !this.games.enumCustom[k]; },

    GS_META: {
      playerHealthFactor:      { def: 1, help: 'Multiplies the player maximum health. 2 = double health.' },
      playerManaFactor:        { def: 1, help: 'Multiplies the player maximum mana.' },
      playerStaminaFactor:     { def: 1, help: 'Multiplies maximum stamina (running, gliding, attacking).' },
      playerBodyHeatFactor:    { def: 1, help: 'Cold resistance. Higher = takes longer to freeze.' },
      playerDivingTimeFactor:  { def: 1, help: 'How long you can stay underwater before losing health.' },
      shroudTimeFactor:        { def: 1, help: 'Multiplies how long you survive inside the Shroud. It does NOT freeze the timer — it only stretches it.' },
      enableStarvingDebuff:    { def: false, help: 'If enabled, going without food applies a starvation penalty.' },
      fromHungerToStarving:    { def: 600000000000, unit: 'ns', help: 'Time between getting hungry and starting to starve. The game accepts 5 to 20 minutes.' },
      foodBuffDurationFactor:  { def: 1, help: 'Multiplies the duration of food buffs.' },
      tombstoneMode:           { def: 'AddBackpackMaterials', opts: ['Everything', 'AddBackpackMaterials', 'NoTombstone'],
                                 help: 'What is left on the tombstone when you die. Everything = everything drops; AddBackpackMaterials = backpack materials only; NoTombstone = nothing is lost.' },

      experienceCombatFactor:  { def: 1, help: 'Multiplies XP gained in combat. No known cap — 5 was accepted without clamping.' },
      experienceMiningFactor:  { def: 1, help: 'Multiplies XP gained from mining and gathering.' },
      experienceExplorationQuestsFactor: { def: 1, help: 'Multiplies XP from exploration and quests.' },
      perkCostFactor:          { def: 1, help: 'Multiplies the cost of upgrading perks. Lower = cheaper.' },
      perkUpgradeRecyclingFactor: { def: 0.5, help: 'Fraction refunded when recycling a perk. 0.5 = half comes back.' },

      enableDurability:        { def: true, help: 'If disabled, weapons and gear never break.' },
      miningDamageFactor:      { def: 1, help: 'Speed of breaking blocks and ores.' },
      plantGrowthSpeedFactor:  { def: 1, help: 'Growth speed of crops.' },
      resourceDropStackAmountFactor: { def: 1, help: 'Multiplies the amount of resources dropped by each source.' },
      factoryProductionSpeedFactor:  { def: 1, help: 'Speed of automatic production stations.' },
      enableGliderTurbulences: { def: true, help: 'Air turbulence during glider flight.' },
      weatherFrequency:        { def: 'Normal', opts: ['Disabled', 'Rare', 'Normal', 'Often', 'Always'],
                                 help: 'How often the weather changes.' },
      fishingDifficulty:       { def: 'Normal', opts: ['VeryEasy', 'Easy', 'Normal', 'Hard'],
                                 help: 'Fish strength in the fishing minigame.' },
      dayTimeDuration:         { def: 1800000000000, unit: 'ns', help: 'Day length. The default is 30 minutes.' },
      nightTimeDuration:       { def: 720000000000, unit: 'ns', help: 'Night length. The default is 12 minutes.' },
      curseModifier:           { def: 'Normal', opts: ['Disabled', 'Easy', 'Normal', 'Hard'],
                                 help: 'Intensity of the curse effect.' },

      enemyDamageFactor:       { def: 1, help: 'Multiplies the damage enemies deal.' },
      enemyHealthFactor:       { def: 1, help: 'Multiplies enemy health.' },
      enemyStaminaFactor:      { def: 1, help: 'Multiplies enemy stamina (attack frequency).' },
      enemyPerceptionRangeFactor: { def: 1, help: 'How far away enemies notice you. Lower = stealthier.' },
      bossDamageFactor:        { def: 1, help: 'Multiplies boss damage.' },
      bossHealthFactor:        { def: 1, help: 'Multiplies boss health.' },
      threatBonus:             { def: 1, help: 'Overall enemy aggressiveness.' },
      randomSpawnerAmount:     { def: 'Normal', opts: ['Few', 'Normal', 'Many', 'Extreme'],
                                 help: 'Number of enemies that spawn across the world.' },
      aggroPoolAmount:         { def: 'Normal', opts: ['Few', 'Normal', 'Many', 'Extreme'],
                                 help: 'How many enemies can attack you at the same time.' },
      pacifyAllEnemies:        { def: false, help: 'If enabled, no enemy attacks. A fully peaceful world.' },
      tamingStartleRepercussion: { def: 'LoseSomeProgress', opts: ['KeepProgress', 'LoseSomeProgress', 'LoseAllProgress'],
                                 help: 'What happens to taming progress when you scare the animal.' },
    },

    gsMeta(k) { return this.GS_META[k] || {}; },
    gsHelp(k) {
      const m = this.gsMeta(k);
      let h = m.help || 'Game configuration field.';
      if (m.def !== undefined) h += '\nGame default: ' + this.gsFmtVal(k, m.def);
      return h;
    },
    gsOpts(k) { return this.gsMeta(k).opts || []; },

    gsFmtVal(k, v) {
      const m = this.gsMeta(k);
      if (m.unit === 'ns' && typeof v === 'number' && v > 0) {
        const min = v / 60000000000;
        return v + ' (' + (Math.round(min * 10) / 10) + ' min)';
      }
      if (typeof v === 'boolean') return v ? 'on' : 'off';
      return String(v);
    },
    gsHint(k) {
      const m = this.gsMeta(k);
      const cur = this.games.gsDraft[k];
      const bits = [];
      if (m.unit === 'ns' && typeof cur === 'number' && cur > 0) {
        bits.push((Math.round((cur / 60000000000) * 10) / 10) + ' min');
      }
      if (m.def !== undefined && JSON.stringify(cur) !== JSON.stringify(m.def)) {
        bits.push('default: ' + (typeof m.def === 'boolean' ? (m.def ? 'on' : 'off') : m.def));
      }
      return bits.join(' · ');
    },
    gsIsDefault(k) {
      const m = this.gsMeta(k);
      if (m.def === undefined) return true;
      return JSON.stringify(this.games.gsDraft[k]) === JSON.stringify(m.def);
    },
    gsResetDefault(k) {
      const m = this.gsMeta(k);
      if (m.def === undefined) return;
      this.games.gsDraft[k] = m.def;
    },

    async loadGameTrainer() {
      const s = this.gsSel();
      if (!s) return;
      this.games.trLoading = true;
      try {
        const d = await this._gsFetch(`/api/gameservers/${s.id}/trainer`);
        this.games.trLoaded = true;
        if (!d) { this.games.trAvailable = false; return; }
        this.games.trAvailable = true;
        this.games.trCatalog = d.catalog || [];
        this.games.trApplied = d.applied || [];
        this.games.trPolicyOK = !!d.policyOK;
        this.games.trPolicyReason = d.policyReason || '';
        if (this.games.trDirty) return;
        const des = d.desired || {};
        this.games.trEnabled = !!des.enabled;
        this.games.trToggles = (des.toggles || []).slice();
        this.games.trValues = Object.assign({}, des.values || {});
      } finally {
        this.games.trLoading = false;
      }
    },

    gameTrainerCategories() {
      const order = ['Player', 'Damage & Defense', 'Inventory', 'Statistics'];
      const cats = [];
      for (const c of this.games.trCatalog) {
        if (c.hidden || c.patchType === 'group') continue;
        if (!cats.includes(c.category)) cats.push(c.category);
      }
      return cats.sort((a, b) => {
        const ia = order.indexOf(a), ib = order.indexOf(b);
        return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib);
      });
    },

    gameTrainerOf(cat) {
      return this.games.trCatalog.filter(
        c => c.category === cat && !c.hidden && c.patchType !== 'group');
    },

    gameTrainerIsOn(id) { return this.games.trToggles.indexOf(id) >= 0; },

    gameTrainerVal(id) {
      const v = this.games.trValues[id];
      return v === undefined ? 0 : v;
    },

    gameTrainerApplied(c) {
      if (c.valueType === 'numeric' && c.linkTo && c.linkTo !== c.id) {
        return this.games.trApplied.indexOf(c.linkTo) >= 0
            && Number(this.gameTrainerVal(c.id)) !== 0;
      }
      return this.games.trApplied.indexOf(c.id) >= 0;
    },

    gameTrainerPending(c) {
      const request = c.valueType === 'toggle'
        ? this.gameTrainerIsOn(c.id)
        : Number(this.gameTrainerVal(c.id)) !== 0;
      return request && !this.gameTrainerApplied(c);
    },

    gameTrainerToggle(id) {
      const i = this.games.trToggles.indexOf(id);
      if (i >= 0) this.games.trToggles.splice(i, 1);
      else this.games.trToggles.push(id);
      this.games.trDirty = true;
    },

    gameTrainerSetVal(id, v) {
      const n = parseFloat(v);
      this.games.trValues[id] = isNaN(n) ? 0 : n;
      this.games.trDirty = true;
    },

    async saveGameTrainer() {
      const s = this.gsSel();
      if (!s) return;
      this.games.trSaving = true;
      try {
        const d = await this._gsFetch(`/api/gameservers/${s.id}/trainer`, {
          method: 'POST',
          body: JSON.stringify({
            enabled: this.games.trEnabled,
            toggles: this.games.trToggles.slice(),
            values: Object.assign({}, this.games.trValues),
          }),
        });
        if (d) {
          this.games.trDirty = false;
          this.showToast(d.applied ? 'Trainer applied to the server'
                                   : `Saved, but not applied: ${d.reason}`);
        }
        await this.loadGameTrainer();
      } finally {
        this.games.trSaving = false;
      }
    },

    async applyGameTrainer() {
      const s = this.gsSel();
      if (!s) return;
      const d = await this._gsFetch(`/api/gameservers/${s.id}/trainer/apply`,
                                    { method: 'POST' });
      if (d) this.showToast(d.message || 'reapplied');
      await this.loadGameTrainer();
    },

    async gameTrainerAllOff() {
      if (!(await this.confirmAsync(
        'Turn the whole trainer off?\n\n'
        + 'Reverts every patch in the server memory. What you checked '
        + 'stays saved — just turn it back on later.'))) return;
      this.games.trEnabled = false;
      this.games.trDirty = true;
      await this.saveGameTrainer();
    },

    async gsResetAllDefaults() {
      if (!(await this.confirmAsync(
        'Restore the GAME RULES to their defaults?\n\n'
        + 'Name, slots, passwords and schedules are NOT affected — the game has no '
        + 'default for those fields.\n'
        + 'This only changes the form; nothing is written until you save.'))) return;
      let changed = 0, noDefault = 0;
      for (const k of Object.keys(this.games.gsDraft || {})) {
        if (this.gsMeta(k).def === undefined) { noDefault++; continue; }
        if (!this.gsIsDefault(k)) changed++;
        this.gsResetDefault(k);
      }
      const extra = noDefault ? (' · ' + noDefault + ' with no known default, kept') : '';
      this.showToast(changed + ' field(s) returned to the default' + extra + ' — review and save', '');
    },

    GAME_SETTING_LABELS: {
      enableDurability: 'Item durability',
      enableStarvingDebuff: 'Hunger debuff',
      experienceCombatFactor: 'Combat XP',
      experienceMiningFactor: 'Mining XP',
      experienceExplorationQuestsFactor: 'Exploration/quest XP',
      playerHealthFactor: 'Player health',
      playerManaFactor: 'Player mana',
      playerStaminaFactor: 'Player stamina',
      playerBodyHeatFactor: 'Cold resistance',
      playerDivingTimeFactor: 'Breath underwater',
      shroudTimeFactor: 'Time in the Shroud',
      foodBuffDurationFactor: 'Food buff duration',
      fromHungerToStarving: 'Hunger → starvation (ns)',
      tombstoneMode: 'What drops on death',
      miningDamageFactor: 'Mining damage',
      enemyDamageFactor: 'Enemy damage',
      enemyHealthFactor: 'Enemy health',
      enemyStaminaFactor: 'Enemy stamina',
      enemyPerceptionRangeFactor: 'Awareness range',
      bossDamageFactor: 'Boss damage',
      bossHealthFactor: 'Boss health',
      pacifyAllEnemies: 'Pacify enemies',
      randomSpawnerAmount: 'Spawn amount',
      aggroPoolAmount: 'Aggression pool',
      threatBonus: 'Threat bonus',
      tamingStartleRepercussion: 'On scaring a tameable',
      dayTimeDuration: 'Day length (ns)',
      nightTimeDuration: 'Night length (ns)',
      resourceDropStackAmountFactor: 'Drop amount',
      plantGrowthSpeedFactor: 'Plant growth',
      factoryProductionSpeedFactor: 'Production speed',
      perkUpgradeRecyclingFactor: 'Perk recycling',
      perkCostFactor: 'Perk cost',
      weatherFrequency: 'Weather frequency',
      fishingDifficulty: 'Fishing difficulty',
      enableGliderTurbulences: 'Glider turbulence',
      curseModifier: 'Curse modifier',
    },
    gameSettingLabel(k) {
      if (this.GAME_SETTING_LABELS[k]) return this.GAME_SETTING_LABELS[k];
      return k.replace(/([A-Z])/g, ' $1').replace(/^./, c => c.toUpperCase()).trim();
    },

    async saveGameSettings(restart) {
      const patch = this.gameSettingsDiff();
      if (!Object.keys(patch).length) return;
      if (restart && !(await this.confirmAsync(
        'Save and restart now?\n\nConnected players will be dropped.'))) return;
      this.games.busy = 'settings';
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/settings', {
          method: 'POST', body: JSON.stringify({ patch, restart: !!restart }),
        });
        this.games.gsOrig = JSON.parse(JSON.stringify(this.games.gsDraft));
        if (d.restartErr) this.showToast('Saved, but the restart failed: ' + d.restartErr, 'err');
        else if (d.restarted) this.showToast('Settings saved and server restarted', 'ok');
        else this.showToast('Saved — takes effect only after restarting the server', '');
        this.loadGames();
      } catch (e) {
        this.showToast('Failed to save: ' + (e.message || e), 'err');
      } finally { this.games.busy = ''; }
    },

    async loadGameLogs() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId
          + '/logs?tail=' + encodeURIComponent(this.games.logTail || '200'));
        this.games.logs = d.logs || '';
        if (this.games.logsAuto) {
          this.$nextTick(() => {
            const el = document.getElementById('gs-console-body');
            if (el) el.scrollTop = el.scrollHeight;
          });
        }
      } catch (e) {
        this.games.logs = 'error reading logs: ' + (e.message || e);
      }
    },
    gsLogLines() {
      const f = (this.games.logFilter || '').toLowerCase();
      const all = (this.games.logs || '').split('\n').filter(l => l.length);
      return f ? all.filter(l => l.toLowerCase().includes(f)) : all;
    },
    gsLogClass(ln) {
      const l = ln.toLowerCase();
      if (/\b(error|fatal|panic|failed)\b/.test(l)) return 'gs-console__line--err';
      if (/\b(warn|warning)\b/.test(l)) return 'gs-console__line--warn';
      return '';
    },
    toggleGameLogsAuto() {
      clearInterval(this._gamesLogsTimer);
      if (this.games.logsAuto) {
        this._gamesLogsTimer = setInterval(() => {
          if (!document.hidden && this.currentView === 'gameconsole') this.loadGameLogs();
        }, 5000);
      }
    },

    async loadGameBackups() {
      if (!this.games.selId) return;
      try {
        const d = await this._gsFetch('/api/gameservers/' + this.games.selId + '/backups');
        this.games.backups = d.backups || [];
      } catch (e) {
        this.showToast('Failed to load backups: ' + (e.message || e), 'err');
      } finally { this.games.backupsLoaded = true; }
    },

    async copyGameAddr(addr) {
      if (!addr) return;
      try {
        await navigator.clipboard.writeText(addr);
        this.showToast('Address copied: ' + addr, 'ok');
      } catch (e) {
        this.showToast('Could not copy (clipboard blocked)', 'err');
      }
    },

    async gsDrawCharts() {
      if (!(await this._ensureChart())) return;
      this.$nextTick(() => {
        const sm = this.games.samples || [];
        const tok = (n, fb) => {
          const v = getComputedStyle(document.documentElement).getPropertyValue(n).trim();
          return v || fb;
        };
        const cSuccess = tok('--success', '#3fb950');
        const cAccent = tok('--accent', '#58a6ff');
        const cWarn = tok('--warning', '#d29922');
        const cGrid = tok('--border-default', '#0f1827');
        const cTick = tok('--text-muted', '#6b7280');
        const labels = sm.map(p => new Date(p.t * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }));
        const mk = (id, data, color, unit) => {
          const ctx = document.getElementById(id);
          if (!ctx) return;
          const ex = __panelCharts.get(id);
          if (ex) {
            ex.data.labels = labels;
            ex.data.datasets[0].data = data;
            ex.update('none');
            return;
          }
          __panelCharts.set(id, new Chart(ctx, {
            type: 'line',
            data: { labels, datasets: [{ data, borderColor: color, backgroundColor: color + '22',
              fill: true, tension: 0.25, pointRadius: 0, borderWidth: 1.5, spanGaps: true }] },
            options: {
              responsive: true, maintainAspectRatio: false, animation: false,
              interaction: { intersect: false, mode: 'index' },
              plugins: { legend: { display: false },
                tooltip: { callbacks: { label: (i) => ' ' + Number(i.parsed.y).toFixed(1) + unit } } },
              scales: {
                x: { ticks: { color: cTick, maxTicksLimit: 6, maxRotation: 0, autoSkip: true }, grid: { display: false } },
                y: { ticks: { color: cTick }, grid: { color: cGrid }, beginAtZero: true },
              },
            },
          }));
        };
        mk('gs-chart-cpu', sm.map(p => p.cpu), cSuccess, '%');
        mk('gs-chart-mem', sm.map(p => p.mem), cAccent, ' MB');
        mk('gs-chart-players', sm.map(p => p.players), cWarn, ' player(s)');
      });
    },

    async refreshGamesTab() {
      if (this.page !== 'games') return;
      if (!this.games.loaded) await this.loadGames();
      const v = this.currentView;

      clearInterval(this._gamesPollTimer);
      if (v === 'gamedash' || v === 'gameservers') {
        this._gamesPollTimer = setInterval(() => {
          if (!document.hidden && this.page === 'games') {
            this.loadGames().then(() => { if (this.currentView === 'gamedash') this.gsDrawCharts(); });
          }
        }, 5000);
      }

      if (v === 'gamedash') {
        this.loadGameHistory();
        if (!this.games.build.buildId) this.loadGameBuild();
        if (!this.games.conn.address) this.loadGameConn();
        if (!this.games.worldsLoaded) this.loadGameWorlds();
        if (!this.games.backupsLoaded) this.loadGameBackups();
        if (!this.games.settingsLoaded) this.loadGameSettings();
        this.gsDrawCharts();
      }
      if (v === 'gameservers') this.loadGames();
      if (v === 'gameworlds') this.loadGameWorlds();
      if (v === 'gamesettings') {
        if (this.gameSettingsDirtyCount() === 0) this.loadGameSettings();
        if (this.gsSrvDirtyCount() === 0) this.loadGameServerCfg();
        if (this.gsRtDirty() === 0) this.loadGameRuntime();
      }
      if (v === 'gameaccess') {
        this.loadGameGroups();
        this.loadGameBans();
      }
      if (v === 'gameconsole') this.loadGameLogs();
      if (v === 'gamebackups') this.loadGameBackups();
      if (v === 'gametrainer') this.loadGameTrainer();
    },

    PAGE_REMAP: {
      history:           ['system', 'history'],
      alerts:            ['system', 'alerts'],
      processes:         ['system', 'processes'],
      ports:             ['system', 'ports'],
      systemd:           ['system', 'systemd'],
      files:             ['system', 'files'],
      metrics:           ['system', 'metrics'],
      containers:        ['docker', 'containers'],
      compose:           ['docker', 'compose'],
      images:            ['docker', 'images'],
      volumes:           ['docker', 'volumes'],
      networks:          ['docker', 'networks'],
      prune:             ['docker', 'prune'],
      terminal:          ['dev', 'host'],
      ai:                ['dev', 'ai'],
      audit:             ['security', 'audit'],
      users:             ['security', 'users'],
      secrets:           ['security', 'secrets'],
      sessions:          ['security', 'sessions'],
      network:              ['security', 'network'],
      adguard:           ['security', 'network'],
      devices:      ['security', 'network'],
      savings:          ['security', 'network'],
      maintenance:        ['operations', 'tasks'],
      jobs:              ['operations', 'jobs'],
      schedules:      ['operations', 'schedules'],
      documentation:      ['dev', 'docs'],
      graphs:            ['dev', 'graphs'],
      code:              ['dev', 'code'],
      git:               ['operations', 'git'],
      nodes:             ['operations', 'proxmox'],
      proxmox:           ['operations', 'proxmox'],
      deploy:            ['operations', 'deploy'],
      whatsapp:          ['apps', 'whatsapp'],
      videocall:         ['apps', 'videocall'],
      browser:         ['apps', 'browser'],
      persistent:        ['apps', 'persistent'],
      gamedash:          ['games', 'gs-dash'],
      gameservers:       ['games', 'gs-servers'],
      gameworlds:        ['games', 'gs-worlds'],
      gamesettings:      ['games', 'gs-settings'],
      gameaccess:        ['games', 'gs-access'],
      gameconsole:       ['games', 'gs-console'],
      gamebackups:       ['games', 'gs-backups'],
      gametrainer:       ['games', 'gs-trainer'],
    },
    GROUP_DEFAULTS: {
      system: 'history',
      docker: 'containers',
      dev: 'host',
      security: 'audit',
      apps: 'whatsapp',
      operations: 'tasks',
      games: 'gs-dash',
    },
    sectionTopOffset() {
      if (this.isMobile() && this.page === 'dev' && this.termChromeHidden) return 0;
      if (this.chromeCollapsed && !this.focusMode && !this.guestMode) return 14;
      if (this.page === 'apps' && this.tabs.apps === 'persistent' && this.bpHeaderHidden) return 0;
      const groupsWithTabs = ['system', 'docker', 'dev', 'security', 'apps', 'operations', 'games'];
      if (groupsWithTabs.includes(this.page)) return this.tabBarH || 52;
      return 0;
    },
    _observeTabBar() {
      const measure = () => {
        const bar = document.getElementById('grp-tabbar');
        if (bar && bar.offsetHeight > 0) this.tabBarH = bar.offsetHeight;
      };
      this.$nextTick(measure);
      const bar = document.getElementById('grp-tabbar');
      if (window.ResizeObserver && bar) {
        this._tabBarObs = new ResizeObserver(() => measure());
        this._tabBarObs.observe(bar);
      }
      window.addEventListener('resize', measure);
    },
    tabToView(group, tab) {
      const canonical = this.PAGE_REMAP[tab];
      if (canonical && canonical[0] === group && canonical[1] === tab) return tab;
      for (const [view, [g, t]] of Object.entries(this.PAGE_REMAP)) {
        if (g === group && t === tab) return view;
      }
      return group;
    },
    get currentView() {
      const tab = this.tabs[this.page];
      if (this.GROUP_DEFAULTS[this.page]) {
        return this.tabToView(this.page, tab || this.GROUP_DEFAULTS[this.page]);
      }
      return this.page;
    },
    _navApplying: false,
    _pendingContainerDeepLink: null,
    _navHash() {
      const tab = this.tabs[this.page];
      let h = '#' + this.page + (tab && this.GROUP_DEFAULTS[this.page] ? ':' + tab : '');
      if (this.detail && this.detail.open && this.detail.id && this.page === 'docker' && tab === 'containers') {
        h += '/' + String(this.detail.id).slice(0, 12) + ':' + (this.detail.tab || 'overview');
      }
      return h;
    },
    _navSyncHistory(mode) {
      if (this._navApplying) return;
      try {
        const h = this._navHash();
        if (location.hash === h) return;
        if (mode === 'replace') history.replaceState({ panelNav: h }, '', h);
        else                    history.pushState({ panelNav: h }, '', h);
      } catch(_){}
    },
    _applyHashFromURL(raw) {
      const h = (raw || '').replace(/^#/, '');
      if (!h) return false;
      if (h.indexOf('=') >= 0 || h.indexOf('&') >= 0) return false;
      let [grp, tabRaw, sub] = h.split(':');
      if (!grp) return false;
      if (grp === 'terminals') grp = 'dev';
      let tab = tabRaw, sel = null;
      if (tabRaw && tabRaw.indexOf('/') >= 0) {
        const i = tabRaw.indexOf('/');
        tab = tabRaw.slice(0, i);
        sel = tabRaw.slice(i + 1);
      }
      this._navApplying = true;
      try {
        if (!this.PAGE_REMAP[grp] && this.GROUP_DEFAULTS[grp]) {
          this.tabs[grp] = tab || this.GROUP_DEFAULTS[grp];
          this._prefPersist('panel_tabs', JSON.stringify(this.tabs));
        }
        this.setPage(grp, { silent: true });
      } finally {
        this._navApplying = false;
      }
      if (sel && grp === 'docker' && tab === 'containers') {
        this._pendingContainerDeepLink = { sel: sel, sub: sub || 'overview' };
        this._applyPendingContainerDeepLink(0);
      } else {
        this._pendingContainerDeepLink = null;
      }
      this._updateTitle();
      return true;
    },
    _applyPendingContainerDeepLink(attempt) {
      attempt = attempt || 0;
      const p = this._pendingContainerDeepLink;
      if (!p) return;
      const c = (this.containers || []).find(x => x.Id === p.sel || (x.Id || '').startsWith(p.sel));
      if (c) {
        this._pendingContainerDeepLink = null;
        this.openContainer(c);
        const sub = p.sub || 'overview';
        if (sub !== 'overview') {
          this.detail.tab = sub;
          if (sub === 'logs')       this.$nextTick(() => this.openDetailLogStream());
          else if (sub === 'stats') this.$nextTick(() => this.openDetailStatsStream());
          else if (sub === 'top')   this.$nextTick(() => this.loadDetailTop());
          else if (sub === 'shell') this.$nextTick(() => this.openDetailShell());
        }
        return;
      }
      if (attempt === 0) { try { this.loadContainers(); } catch(_){} }
      if (attempt < 20) {
        setTimeout(() => this._applyPendingContainerDeepLink(attempt + 1), 150);
      } else {
        this._pendingContainerDeepLink = null;
      }
    },
    VIEW_TITLES: {
      dashboard:    'Dashboard',
      config:       'Settings',
      history:      'History',
      alerts:       'Alerts',
      processes:    'Processes',
      ports:        'Ports / Connections',
      systemd:      'Systemd',
      files:        'Files',
      metrics:      'Metrics',
      containers:   'Containers',
      compose:      'Compose',
      images:       'Images',
      volumes:      'Volumes',
      networks:     'Networks',
      prune:        'Prune / Pull',
      terminal:     'Terminal',
      ai:           'IA',
      audit:        'Audit',
      users:        'Users',
      secrets:      'Secrets',
      sessions:     'Active sessions',
      maintenance:   'Tasks',
      jobs:         'Jobs',
      schedules: 'Schedules',
      documentation: 'Documentation',
      graphs:       'Graphs',
      code:         'VSCode',
      git:          'Git',
      deploy:       'Deploy',
      whatsapp:     'WhatsApp',
      videocall:    'Video call',
      browser:    'Browser',
      persistent:   'Persistent browser',
    },

    // ─── Screen-usage telemetry ───────────────────────────────────────────────
    // The canonical list of ids is embedded in the binary through go:embed at
    // internal/telemetry/screens.txt — byte-identical (sha256) across BOTH forks,
    // which a parity check enforces. What lives here is only the MAP: the legacy
    // view of this fork -> canonical id. Every id this file can emit is in the canonical
    // list; anything not mapped simply is not emitted, and the server still has the
    // allowlist as a second barrier (an unknown id becomes `unknown` and never
    // enters the JSONL raw).
    //
    // DELIBERATE absences, and why:
    //   `system.fans` — a sub-tab of the fanhub proxy, it only exists in the
    //      panel of the VM. It stays in the canonical list (the file has to be
    //      byte-identical) and comes out as 0 in the report of this fork.
    //   `nodes` (the Nodes screen) — the screen stopped existing: it was MERGED into
    //      the Proxmox tab, and `nodes` today is only a remap to `proxmox` (see
    //      PAGE_REMAP), which never becomes `currentView`. The canonical id
    //      `operations.nodes` IS ALREADY in the allowlist since the re-edit — what is
    //      missing is the screen, not the list. A key here would be dead code: it
    //      could never be read, and it would give the impression of instrumentation
    //      that does not exist. It goes in together with the node axis, when `nodes`
    //      becomes a view in its own right again.
    //   `operations.backup` and `operations.embedded` — ids already in the canonical
    //      list, but the tabs do not exist in this fork yet. Same contract as
    //      `system.fans`: they come out as 0 until the screen exists, and the
    //      key lands in the same commit that grafts the tab in (the per-node backup
    //      and restore screen, and the embedded-tools proxy). Until then there is no
    //      view to map.
    //
    // Re-edit of the canonical list — what CHANGED here:
    //   `config` and `proxmox` were deliberate absences until yesterday, because
    //   screens.txt had to be byte-identical (sha256) across BOTH forks and the two
    //   screens were born AFTER the list: navigating to them fell into the `unknown`
    //   bucket, and that was the correct behaviour for as long as the list was not
    //   re-edited in both at once. The re-edit happened — in both forks, in the same
    //   act, immediately before the home fork was frozen, which was the last window
    //   in which parity could still be restored. The keys below are the other half
    //   of that act. Removing one of them goes back to sending a live screen to the
    //   `unknown` bucket; scripts/test-proxmox-tab.mjs fails first.
    TEL_IDS: {
      dashboard:    'dashboard',
      history:      'system.history',
      processes:    'system.processes',
      ports:        'system.ports',
      systemd:      'system.systemd',
      files:        'system.files',
      metrics:      'system.metrics',
      alerts:       'system.alerts',
      gamedash:     'games.overview',
      gameservers:  'games.servers',
      gameworlds:   'games.worlds',
      gamesettings: 'games.settings',
      gameaccess:   'games.access',
      gameconsole:  'games.console',
      gamebackups:  'games.backups',
      gametrainer:  'games.trainer',
      containers:   'docker.containers',
      compose:      'docker.compose',
      images:       'docker.images',
      volumes:      'docker.volumes',
      networks:     'docker.networks',
      prune:        'docker.cleanup',
      terminal:     'dev.terminal',
      code:         'dev.code',
      documentation: 'dev.documentation',
      graphs:       'dev.graphs',
      ai:           'dev.ai',
      audit:        'security.audit',
      users:        'security.users',
      secrets:      'security.secrets',
      sessions:     'security.sessions',
      whatsapp:     'apps.whatsapp',
      videocall:    'apps.videocall',
      browser:    'apps.browser',
      persistent:   'apps.browser-persistent',
      maintenance:   'operations.tasks',
      jobs:         'operations.jobs',
      schedules: 'operations.schedules',
      deploy:       'operations.deploy',
      git:          'operations.git',
      proxmox:      'operations.proxmox',
      config:       'config',
    },
    TEL_SUB: [
      'dev.ai.agents','dev.ai.prompts','dev.ai.routing','dev.ai.tokens','dev.ai.usage',
      'operations.tasks.jira.board','operations.tasks.jira.backlog',
      'operations.tasks.jira.detail.overview','operations.tasks.jira.detail.comments',
      'operations.tasks.jira.detail.attachments','operations.tasks.jira.detail.subtasks',
      'operations.tasks.jira.detail.links','operations.tasks.jira.detail.worklog',
      'operations.tasks.jira.detail.history',
      'operations.git.branches','operations.git.blame','operations.git.reflog',
      'operations.git.hunks','operations.git.filelog',
      'operations.git.prs.list','operations.git.prs.detail',
      'docker.containers.overview','docker.containers.logs','docker.containers.shell',
      'docker.containers.stats','docker.containers.top','docker.containers.inspect',
      'docker.containers.sessions','docker.containers.backups',
    ],
    _telScreen() {
      try { return this.TEL_IDS[this.currentView] || ''; } catch (_) { return ''; }
    },
    _telHit(origin) {
      try {
        const id = this._telScreen();
        if (id && window.tel) window.tel.hit(id, origin || 'nav');
        this._telSub(this._telSubCurrent());
      } catch (_) {}
    },
    _telSubCurrent() {
      try {
        const v = this.currentView;
        if (v === 'ai')         return 'dev.ai.' + this.aiTab;
        if (v === 'maintenance') return 'operations.tasks.jira.' + this.jiraView;
        if (v === 'containers' && this.detail && this.detail.open) {
          return 'docker.containers.' + this.detail.tab;
        }
      } catch (_) {}
      return '';
    },
    _telSub(id) {
      try {
        if (!id || !window.tel) return;
        if (this.TEL_SUB.indexOf(id) < 0) return;
        window.tel.hit(id, 'nav');
      } catch (_) {}
    },
    _telWire() {
      if (this._telWired) return;
      this._telWired = true;
      const self = this;
      const w = (expr, fn) => { try { self.$watch(expr, fn); } catch (_) {} };

      w('aiTab',         (v) => { if (self.currentView === 'ai')         self._telSub('dev.ai.' + v); });
      w('jiraView',      (v) => { if (self.currentView === 'maintenance') self._telSub('operations.tasks.jira.' + v); });
      w('jiraDetailTab', (v) => { if (self.currentView === 'maintenance') self._telSub('operations.tasks.jira.detail.' + v); });
      w('git.view',      (v) => { self._telSub('operations.git.' + v); });
      w('git.inspect.kind', (v) => { self._telSub('operations.git.' + v); });
      w('git.prView',    (v) => { self._telSub('operations.git.prs.' + v); });
      w('detail.tab',    (v) => { if (self.currentView === 'containers') self._telSub('docker.containers.' + v); });
    },
    _updateTitle() {
      try {
        const label = this.VIEW_TITLES[this.currentView] || this.VIEW_TITLES[this.page] || '';
        document.title = (label ? label + ' — ' : '') + 'Server Control Panel';
      } catch(_){}
    },
    _prefPersist(key, val) {
      if (this._navApplying) window.panelPrefs.setLocal(key, val);
      else window.panelPrefs.set(key, val);
    },
    async _prefsReconcile() {
      if (!this.token) return;
      let server = {};
      try { server = await window.panelPrefs.pull(); } catch(_) { server = {}; }
      server = server || {};
      const st = server['panel_theme'];
      if (st === 'light' || st === 'dark') {
        if (st !== window.panelPrefs.get('panel_theme')) {
          window.panelPrefs.setLocal('panel_theme', st);
          try { window.panelTheme.apply(st); } catch(_){}
        }
      }
      const stabs = server['panel_tabs'];
      if (typeof stabs === 'string' && stabs) {
        window.panelPrefs.setLocal('panel_tabs', stabs);
        try {
          const obj = JSON.parse(stabs) || {};
          for (const g in obj) { if (g !== this.page && this.GROUP_DEFAULTS[g]) this.tabs[g] = obj[g]; }
        } catch(_){}
      }
      const sp = server['panel_page'];
      if (typeof sp === 'string' && sp) {
        window.panelPrefs.setLocal('panel_page', sp);
        const allowed = ['dashboard','system','docker','dev','security','apps','operations','config','git','games'];
        if (!this._bootHadHashNav && allowed.includes(sp) && sp !== this.page) {
          this._navApplying = true;
          try { this.setPage(sp, { silent: true }); } finally { this._navApplying = false; }
        }
      }
      try {
        if (!localStorage.getItem('panel_prefs_migrated')) {
          window.panelPrefs.SYNCED.forEach(function(k){
            var have = Object.prototype.hasOwnProperty.call(server, k) && server[k] != null && server[k] !== '';
            var local = window.panelPrefs.get(k);
            if (!have && local != null && local !== '') window.panelPrefs.push(k, local);
          });
          localStorage.setItem('panel_prefs_migrated', '1');
        }
      } catch(_){}
    },
    setTab(tab, opts) {
      opts = opts || {};
      if (!this.GROUP_DEFAULTS[this.page]) return;
      this.tabs[this.page] = tab;
      this._prefPersist('panel_tabs', JSON.stringify(this.tabs));
      this._navSyncHistory(opts.replace ? 'replace' : 'push');
      this._updateTitle();
      if (!opts.replace) this.pushRecentView(this.currentView);
      this._triggerViewLoaders(this.currentView);
      this._telHit('nav');
    },
    setPage(p, opts) {
      opts = opts || {};
      if (this.PAGE_REMAP[p]) {
        const [group, tab] = this.PAGE_REMAP[p];
        this.tabs[group] = tab;
        this._prefPersist('panel_tabs', JSON.stringify(this.tabs));
        p = group;
      }
      const allowed = ['dashboard','system','docker','dev','security','apps','operations','config','git','games'];
      if (!allowed.includes(p)) p = 'dashboard';
      this.page = p;
      this._prefPersist('panel_page', p);
      if (this.mobileSidebarOpen) this.mobileSidebarOpen = false;
      if (this.jiraDetail && this.jiraDetail.open) this.closeJiraDetail();
      if (this.jiraCreate && this.jiraCreate.open) this.jiraCreate.open = false;
      if (this.jiraPageDrawer && this.jiraPageDrawer.open) this.jiraPageDrawer.open = false;
      if (this.jiraColumnMgr && this.jiraColumnMgr.open) this.jiraColumnMgr.open = false;
      if (this.jobLog && this.jobLog.open) this.minimizeJobLog();
      if (this.procsLive && p !== 'operations') this.procsStop();
      if (!opts.silent) this._navSyncHistory(opts.replace ? 'replace' : 'push');
      this._updateTitle();
      if (!opts.silent) this.pushRecentView(this.currentView);
      this._triggerViewLoaders(this.currentView, opts);
      this._telHit(opts.silent ? 'default' : 'nav');
    },
    _triggerViewLoaders(p, opts) {
      opts = opts || {};
      if (p) this._mounted[p] = true;
      if (p==='dashboard')  { this.loadStats(); this.loadContainers(); this.loadPanelHealth(); }
      if (p==='containers') this.loadContainers();
      if (p==='compose')    this.loadCompose();
      if (p==='images')     this.loadImages();
      if (p==='volumes')    this.loadVolumes();
      if (p==='networks')   this.loadNetworks();
      if (p==='processes')  { this.loadStats(); this.procsStart(); }
      else if (this.procsLive)              { this.procsStop(); }
      if (p==='maintenance') this.jiraInit();
      if (p==='jobs')       { this.jobsNow = Math.floor(Date.now()/1000); this.loadJobs(); }
      if (p==='proxmox')    { this.pvxInit(); this.pvxStartPoll(); }
      else                  { this.pvxStopPoll(); }
      if (p==='deploy')     { this.loadDeployApps(); this.loadDevPorts(); }
      if (p==='schedules') { this.loadSchedCatalog(); this.loadSchedJobs(); this.loadSchedRemotes(); this.loadSchedNotifyChannels(); }
      if (p==='ports')      { this.loadPorts(); this.loadConns(); }
      if (p==='systemd')    { this.loadUnits(); this.loadSystemLogs(); this.loadUFW(); this.loadCron(); }
      if (p==='files')      this.browseFiles(this.filePath);
      if (p==='ai')         {
        this.loadClaude(); this.loadClaudeTermSessions();
        this.loadClaudeAccounts();
        this.loadClaudeAccountsUsage();
        this.loadClaudeRateLimits();
        this.loadAIModels();
        this.refreshAITab(this.aiTab);
      }
      if (p==='secrets')    this.loadSecrets();
      if (p==='gamedash' || p==='gameservers' || p==='gameworlds'
          || p==='gamesettings' || p==='gameaccess' || p==='gameconsole'
          || p==='gamebackups' || p==='gametrainer') {
        this.refreshGamesTab();
      }
      if (p==='audit')      { this.loadAuditActions(); this.runAuditSearch(); }
      if (p==='users')      this.loadUsers();
      if (p==='persistent' && !this.brPersistentMounted) {
        this.$nextTick(() => { this.brPersistentMounted = true; this.bpActive = true; });
      } else if (p==='persistent') {
        if (this.bpPauseTimer) { clearTimeout(this.bpPauseTimer); this.bpPauseTimer = null; }
        if (!this.bpActive) this.bpActive = true;
      } else if (this.brPersistentMounted) {
        if (this.bpPauseTimer) clearTimeout(this.bpPauseTimer);
        this.bpPauseTimer = setTimeout(() => { this.bpActive = false; }, 30000);
      }
      if (p==='code' && !this.codeMounted) {
        this.$nextTick(() => { this.codeMounted = true; });
      }
      if (p==='alerts')     { this.loadAlertRules(); this.loadMetricCatalog(); this.loadMetricSnapshot(); this.loadAlerting(); this.loadNotify(); }
      if (p==='history')    { this.loadAlertRules(); this.loadHistory().then(()=>this.drawCharts()); }
      if (p==='config')     { this.loadConfig(); }
      if (p==='sessions')   this.loadSessions();
      if (p==='network')       { this.loadTunnelDevices(); this.loadAdguard(); this.loadDatasaver(); this.loadUsage(); }
      if (p==='terminal') {
        try { this.saveState(); } catch(_){}
        const outgoing = this.terms;
        const target = this._termsClaude;
        if (outgoing && outgoing !== target && outgoing.panes) {
          outgoing.panes.forEach(pane => {
            try { if (pane.reconnect) pane.reconnect.cancelled = true; } catch(_){}
            try { if (pane.reconnect && pane.reconnect.timer) clearTimeout(pane.reconnect.timer); } catch(_){}
            try { if (pane.notify && pane.notify.timer) clearTimeout(pane.notify.timer); } catch(_){}
            if (pane.term) { try { pane.term.dispose(); } catch(_){} }
            if (pane.ws)   { try { pane.ws.close();   } catch(_){} }
            if (pane.resizeObserver) { try { pane.resizeObserver.disconnect(); } catch(_){} }
            pane.term = null; pane.ws = null; pane.fit = null; pane.search = null; pane.resizeObserver = null;
            pane.status = 'idle';
          });
        }
        this.terms = target;
        this.loadClaudeAccounts();
        this.loadClaudeTermSessions();
        if (!opts.skipTerminalBootstrap) {
          this.$nextTick(()=>this.openHostTerminal());
        }
      }
      if (p==='browser')  this.checkBrowserHealth();
      if (p==='videocall')  {
        if (!this.guestMode) {
          this.vcLoadRooms();
          this.vcLoadHistory();
          this.vcLoadRecordings();
          this.vcLoadDevices();
        }
      }
    },

    async checkBrowserHealth() {
      this.browserHealth = null;
      try {
        const r = await fetch('/browser/healthz', {headers:{'Authorization':'Bearer '+this.token}});
        this.browserHealth = r.ok;
        if (this.browserHealth) {
          this.browserMounted = true;
          this.ensureBrowserState();
          if (!this.browserSnapTimer) {
            this.browserSnapTimer = setInterval(()=>this.snapBrowserState(), 2000);
          }
        }
      } catch(e) { this.browserHealth = false; }
    },

    ensureBrowserState() {
      if (this.browserTabs.length) return;
      try {
        const raw = localStorage.getItem('panel_browser_tabs');
        const saved = raw && JSON.parse(raw);
        if (saved && Array.isArray(saved.tabs) && saved.tabs.length) {
          this.browserTabs = saved.tabs;
          this.browserActive = saved.active && saved.tabs.find(t=>t.id===saved.active)
            ? saved.active : saved.tabs[0].id;
          return;
        }
      } catch(e){}
      this.newBrowserTab(true);
    },
    persistBrowserTabs() {
      try {
        localStorage.setItem('panel_browser_tabs', JSON.stringify({
          tabs: this.browserTabs, active: this.browserActive
        }));
      } catch(e){}
    },
    newBrowserTab(makeActive) {
      const id = 't' + Date.now().toString(36) + Math.random().toString(36).slice(2,5);
      this.browserTabs.push({ id, panes: [{ url: '/browser/' }] });
      this.browserActive = id;
      this.persistBrowserTabs();
    },
    closeBrowserTab(id) {
      const idx = this.browserTabs.findIndex(t => t.id === id);
      if (idx < 0) return;
      const tab = this.browserTabs[idx];
      if (tab.pinned) return;
      const removed = this.browserTabs.splice(idx, 1)[0];
      this.browserClosedStack.push(JSON.parse(JSON.stringify(removed)));
      if (this.browserClosedStack.length > 10) this.browserClosedStack.shift();
      Object.keys(this.browserTitles).forEach(k => { if (k.startsWith(id+':')) delete this.browserTitles[k]; });
      Object.keys(this.browserLoading).forEach(k => { if (k.startsWith(id+':')) delete this.browserLoading[k]; });
      if (!this.browserTabs.length) this.newBrowserTab(true);
      else if (this.browserActive === id) {
        this.browserActive = this.browserTabs[Math.max(0, idx-1)].id;
      }
      this.persistBrowserTabs();
    },
    forceCloseTab(id) {
      const t = this.browserTabs.find(x=>x.id===id);
      if (t && t.pinned) t.pinned = false;
      this.closeBrowserTab(id);
    },
    reopenLastClosed() {
      const tab = this.browserClosedStack.pop();
      if (!tab) return;
      tab.id = 't' + Date.now().toString(36) + Math.random().toString(36).slice(2,5);
      this.browserTabs.push(tab);
      this.browserActive = tab.id;
      this.persistBrowserTabs();
    },
    duplicateBrowserTab(id) {
      const src = this.browserTabs.find(t=>t.id===id); if (!src) return;
      const clone = JSON.parse(JSON.stringify(src));
      clone.id = 't' + Date.now().toString(36) + Math.random().toString(36).slice(2,5);
      clone.pinned = false;
      const idx = this.browserTabs.findIndex(t=>t.id===id);
      this.browserTabs.splice(idx+1, 0, clone);
      this.browserActive = clone.id;
      this.persistBrowserTabs();
    },
    togglePinTab(id) {
      const tab = this.browserTabs.find(t=>t.id===id); if (!tab) return;
      tab.pinned = !tab.pinned;
      this.browserTabs.sort((a,b)=>(b.pinned?1:0)-(a.pinned?1:0));
      this.persistBrowserTabs();
    },
    closeOtherTabs(id) {
      const keep = new Set([id]);
      this.browserTabs.filter(t=>!t.pinned && !keep.has(t.id))
        .forEach(t => this.closeBrowserTab(t.id));
    },
    closeRightTabs(id) {
      const i = this.browserTabs.findIndex(t=>t.id===id); if (i<0) return;
      const toClose = this.browserTabs.slice(i+1).filter(t=>!t.pinned).map(t=>t.id);
      toClose.forEach(tid => this.closeBrowserTab(tid));
    },
    activateBrowserTab(id) {
      this.browserActive = id;
      this.browserUrlEditing = false;
      this.browserUrlInput = this.currentBrowserUrl();
      this.persistBrowserTabs();
    },
    getActiveBrowserTab() {
      return this.browserTabs.find(t => t.id === this.browserActive) || this.browserTabs[0];
    },
    activeIframe(paneIdx) {
      return document.querySelector(
        `iframe[data-tab="${this.browserActive}"][data-pane="${paneIdx||0}"]`
      );
    },
    navigateBrowserTab() {
      const tab = this.getActiveBrowserTab();
      if (!tab) return;
      let url = (this.browserUrlInput || '').trim();
      if (!url) return;
      if (!/^https?:\/\//i.test(url)) {
        if (url.includes('.') && !url.includes(' ')) url = 'https://' + url;
        else {
          const eng = this.browserSearchEngines[this.browserSearchEngine] || this.browserSearchEngines.ddg;
          url = eng.url.replace('%s', encodeURIComponent(url));
        }
      }
      const encoded = this.uvEncode(url);
      tab.panes[0].url = '/browser/uv/service/' + encoded;
      this.browserLoading[tab.id + ':0'] = true;
      this.persistBrowserTabs();
      this.dispatchPaneNav(tab.id, 0, encoded);
      this.browserUrlEditing = false;
      this.browserUrlInput = url;
    },
    dispatchPaneNav(tabId, paneIdx, encoded) {
      const f = document.querySelector(`iframe[data-tab="${tabId}"][data-pane="${paneIdx}"]`);
      if (!f) return;
      let curPath = '';
      try { curPath = f.contentWindow.location.pathname; } catch (_) {}
      const target = '/browser/uv/service/' + encoded;
      if (curPath === '/browser/' || curPath === '/browser/index.html' || curPath === '/browser') {
        try { f.contentWindow.postMessage({ type:'panel-browser-navigate', encoded }, location.origin); } catch (_) {}
      } else if (curPath.startsWith('/browser/uv/service/')) {
        try { f.contentWindow.location.href = target; }
        catch (_) { f.src = target; }
      } else {
        f.dataset.pendingEncoded = encoded;
        f.src = '/browser/';
      }
    },
    uvEncode(s) {
      const enc = s.toString().split('').map((c,i) =>
        i % 2 ? String.fromCharCode(c.charCodeAt(0) ^ 2) : c
      ).join('');
      return encodeURIComponent(enc);
    },
    uvDecode(s) {
      try {
        s = decodeURIComponent(s);
        return s.split('').map((c,i) =>
          i % 2 ? String.fromCharCode(c.charCodeAt(0) ^ 2) : c
        ).join('');
      } catch(e) { return ''; }
    },
    backBrowserPane(paneIdx) {
      const f = this.activeIframe(paneIdx);
      if (f && f.contentWindow) { try { f.contentWindow.history.back(); } catch(e){} }
    },
    forwardBrowserPane(paneIdx) {
      const f = this.activeIframe(paneIdx);
      if (f && f.contentWindow) { try { f.contentWindow.history.forward(); } catch(e){} }
    },
    reloadBrowserPane(paneIdx) {
      const f = this.activeIframe(paneIdx); if (!f) return;
      this.browserLoading[this.browserActive + ':' + (paneIdx||0)] = true;
      try { f.contentWindow.location.reload(); }
      catch (_) { f.src = f.src; }
    },
    toggleSplit() {
      const tab = this.getActiveBrowserTab();
      if (!tab) return;
      if (tab.panes.length === 1) tab.panes.push({ url: '/browser/' });
      else tab.panes = [tab.panes[0]];
      this.persistBrowserTabs();
    },
    closePane(paneIdx) {
      const tab = this.getActiveBrowserTab();
      if (!tab || tab.panes.length < 2) return;
      tab.panes.splice(paneIdx, 1);
      this.persistBrowserTabs();
    },
    snapBrowserState() {
      if (!this.browserMounted) return;
      if (this.currentView !== 'browser') return;
      let changed = false;
      for (const tab of this.browserTabs) {
        tab.panes.forEach((pane, idx) => {
          const f = document.querySelector(`iframe[data-tab="${tab.id}"][data-pane="${idx}"]`);
          if (!f) return;
          try {
            const cw = f.contentWindow;
            if (cw && cw.location && cw.location.pathname && cw.location.pathname.startsWith('/browser/')) {
              const cur = cw.location.pathname + (cw.location.search || '');
              if (cur && cur !== pane.url) { pane.url = cur; changed = true; }
            }
            const t = f.contentDocument && f.contentDocument.title;
            if (t) this.browserTitles[tab.id + ':' + idx] = t.slice(0, 60);
          } catch(e) { /* mid-navigation can throw */ }
        });
      }
      if (changed) this.persistBrowserTabs();
      if (!this.browserUrlEditing) {
        const cur = this.currentBrowserUrl();
        if (cur !== this.browserUrlInput) this.browserUrlInput = cur;
      }
    },
    currentBrowserUrl() {
      const tab = this.getActiveBrowserTab(); if (!tab) return '';
      const u = (tab.panes[0] && tab.panes[0].url) || '';
      if (u === '/browser/' || u === '/browser') return '';
      const m = u.match(/^\/browser\/uv\/service\/(.+)$/);
      return m ? this.uvDecode(m[1]) : '';
    },

    onAddressBarFocus(e) {
      this.browserUrlEditing = true;
      this.$nextTick(() => { try { e.target.select(); } catch(_){} });
    },
    onAddressBarBlur() {
      this.browserUrlEditing = false;
      this.browserUrlInput = this.currentBrowserUrl();
    },
    onAddressBarEscape(e) { try { e.target.blur(); } catch(_){} },

    onIframeLoad(tabId, paneIdx) {
      this.browserLoading[tabId + ':' + paneIdx] = false;
      const f = document.querySelector(`iframe[data-tab="${tabId}"][data-pane="${paneIdx}"]`);
      if (!f) return;
      const pending = f.dataset.pendingEncoded;
      if (pending) {
        delete f.dataset.pendingEncoded;
        try { f.contentWindow.postMessage({ type:'panel-browser-navigate', encoded: pending }, location.origin); } catch (_) {}
        return;
      }
      const tab = this.browserTabs.find(t=>t.id===tabId); if (!tab) return;
      const pane = tab.panes[paneIdx]; if (!pane || !pane.url) return;
      const m = pane.url.match(/^\/browser\/uv\/service\/(.+)$/);
      if (!m) return;
      try {
        const p = f.contentWindow.location.pathname;
        if (p === '/browser/' || p === '/browser/index.html' || p === '/browser') {
          f.contentWindow.postMessage({ type:'panel-browser-navigate', encoded: m[1] }, location.origin);
        }
      } catch (_) {}
    },
    onVscodeFrameLoad(frame) {
      if (!frame) return;
      this._codeRestorePing();
      if (this._wireCodePaste(frame)) return;
      let tries = 0;
      const iv = setInterval(() => { if (++tries > 20 || this._wireCodePaste(frame)) clearInterval(iv); }, 500);
    },
    _codeRestorePing(){
      const now = Date.now();
      if (this._lastCodeRestorePing && now - this._lastCodeRestorePing < 4000) return;
      this._lastCodeRestorePing = now;
      try { this.api('/api/terminal/code-restore-ping', { method:'POST' }).catch(()=>{}); } catch(_){}
    },
    _wireCodePaste(frame) {
      let win, doc;
      try { win = frame.contentWindow; doc = win.document; } catch (_) { return false; }
      if (!doc) return false;
      if (doc.__panelPasteWired) return true;
      doc.__panelPasteWired = true;

      const inTerm = (el) => el && ((el.classList && el.classList.contains('xterm-helper-textarea')) ||
                                    (el.closest && el.closest('.xterm')));
      const upload = (file) => {
        if (!file) return;
        const now = Date.now();
        if (now - (doc.__panelLastPaste || 0) < 1000) return;
        doc.__panelLastPaste = now;
        const fd = new FormData();
        fd.append('image', file, file.name || 'paste.png');
        fetch('/api/terminal/paste-image', { method: 'POST', body: fd, credentials: 'include' })
          .then((res) => { if (!res.ok) console.warn('[panel] paste-image failed:', res.status); })
          .catch((err) => console.warn('[panel] paste-image error:', err));
      };

      doc.addEventListener('paste', (e) => {
        if (!inTerm(e.target)) return;
        const cd = e.clipboardData; if (!cd) return;
        let file = null;
        for (const it of (cd.items || [])) {
          if (it.kind === 'file' && (it.type || '').startsWith('image/')) { file = it.getAsFile(); break; }
        }
        if (!file) for (const f of (cd.files || [])) { if ((f.type || '').startsWith('image/')) { file = f; break; } }
        if (!file) return;
        e.stopImmediatePropagation(); e.preventDefault();
        upload(file);
      }, true);

      doc.addEventListener('keydown', (e) => {
        if (e.key !== 'v' && e.key !== 'V') return;
        if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
        if (!inTerm(e.target)) return;
        const nav = (win.navigator && win.navigator.clipboard && win.navigator.clipboard.read) ? win.navigator : navigator;
        if (!nav.clipboard || !nav.clipboard.read) return;
        nav.clipboard.read().then((items) => {
          for (const it of items) {
            const type = (it.types || []).find((t) => t.startsWith('image/'));
            if (type) { it.getType(type).then((blob) => upload(new File([blob], 'paste.png', { type }))); return; }
          }
        }).catch(() => { /* no permission / no image — ignore, text pastes normally */ });
      }, true);

      console.debug('[panel] code-server paste interceptor armed');
      return true;
    },
    isPaneLoading(tabId, paneIdx) { return !!this.browserLoading[tabId + ':' + paneIdx]; },
    stopBrowserPane(paneIdx) {
      const f = this.activeIframe(paneIdx);
      if (!f) return;
      try { f.contentWindow.stop(); } catch(_){}
      this.browserLoading[this.browserActive + ':' + paneIdx] = false;
    },

    faviconHost(tab) {
      const u = (tab.panes[0] && tab.panes[0].url) || '';
      const m = u.match(/^\/browser\/uv\/service\/(.+)$/);
      if (!m) return '';
      try { return new URL(this.uvDecode(m[1])).hostname.replace(/^www\./,''); } catch(_) { return ''; }
    },
    faviconLetter(tab) {
      const h = this.faviconHost(tab);
      return (h && h[0] ? h[0] : '·').toUpperCase();
    },
    faviconColor(tab) {
      const h = this.faviconHost(tab) || tab.id;
      let n = 0; for (let i=0;i<h.length;i++) n = (n*31 + h.charCodeAt(i)) >>> 0;
      const hue = n % 360;
      return `hsl(${hue} 65% 45%)`;
    },

    openTabContextMenu(e, id) {
      e.preventDefault();
      this.browserCtxMenu = { open:true, x: e.clientX, y: e.clientY, tabId: id };
    },
    closeTabContextMenu() { this.browserCtxMenu.open = false; },
    ctxRun(action) {
      const id = this.browserCtxMenu.tabId;
      this.closeTabContextMenu();
      const tab = this.browserTabs.find(t=>t.id===id);
      if (!tab && action!=='reopen') return;
      switch(action) {
        case 'reload':       this.activateBrowserTab(id); this.reloadBrowserPane(0); break;
        case 'duplicate':    this.duplicateBrowserTab(id); break;
        case 'pin':          this.togglePinTab(id); break;
        case 'close':        this.forceCloseTab(id); break;
        case 'closeOthers':  this.closeOtherTabs(id); break;
        case 'closeRight':   this.closeRightTabs(id); break;
        case 'reopen':       this.reopenLastClosed(); break;
      }
    },

    bpReload() {
      if (!this.bpActive) {
        this.bpActive = true;
        return;
      }
      const f = document.getElementById('bpFrame');
      if (!f) return;
      try { f.contentWindow.location.reload(); }
      catch(_) { f.src = f.src; }
    },
    bpToggleQuality() {
      this.bpQuality = this.bpQuality === 'eco' ? 'normal' : 'eco';
      try { localStorage.setItem('panel_bp_quality', this.bpQuality); } catch(_){}
      if (this.bpActive) {
        this.bpActive = false;
        this.$nextTick(() => { this.bpActive = true; });
      }
    },
    bpToggleHeader() {
      this.bpHeaderHidden = !this.bpHeaderHidden;
      try { localStorage.setItem('panel_bp_header_hidden', this.bpHeaderHidden ? '1' : '0'); } catch(_){}
    },
    async loadUsers() {
      try {
        const r = await this.api('/api/users');
        if (!r.ok) { this.showToast && this.showToast('error listing users', 'err'); return; }
        const d = await r.json();
        this.usersList = d.users || [];
      } catch(e) { console.warn('loadUsers:', e); }
      finally { this.usersLoaded = true; }
    },
    async userCreate() {
      this.usersModal.loading = true; this.usersModal.error = '';
      try {
        const r = await this.api('/api/users/create', {
          method: 'POST',
          body: JSON.stringify({
            username: this.usersModal.form.username.trim(),
            password: this.usersModal.form.password,
          }),
        });
        const d = await r.json();
        if (!r.ok) { this.usersModal.error = d.error || 'error ' + r.status; return; }
        this.usersModal.open = false;
        this.showToast && this.showToast('user created', 'ok');
        await this.loadUsers();
      } catch(e) { this.usersModal.error = e.message; }
      finally { this.usersModal.loading = false; }
    },
    async userResetPassword() {
      this.usersModal.loading = true; this.usersModal.error = '';
      try {
        const r = await this.api('/api/users/reset-password', {
          method: 'POST',
          body: JSON.stringify({
            username: this.usersModal.form.username,
            password: this.usersModal.form.password,
          }),
        });
        const d = await r.json();
        if (!r.ok) { this.usersModal.error = d.error || 'error ' + r.status; return; }
        this.usersModal.open = false;
        this.showToast && this.showToast('password changed', 'ok');
        await this.loadUsers();
      } catch(e) { this.usersModal.error = e.message; }
      finally { this.usersModal.loading = false; }
    },
    async userDelete(username) {
      if (!(await this.confirmAsync('Remove user "'+username+'"?\n\nAll active sessions will be revoked.\nThe user will no longer be able to log in.\n\nThis action cannot be undone.'))) return;
      try {
        const r = await this.api('/api/users/delete', {
          method: 'POST', body: JSON.stringify({ username }),
        });
        const d = await r.json();
        if (!r.ok) { this.showToast('error: ' + (d.error || r.status), 'err'); return; }
        this.showToast('user removed', 'ok');
        await this.loadUsers();
      } catch(e) { this.showToast(e.message, 'err'); }
    },
    async userSetAdmin(username, makeAdmin) {
      const msg = makeAdmin
        ? 'Promote "'+username+'" to ADMIN?\n\nThey will have full privilege parity with the primary account: user management, scheduler as root, AI prompts, queue control, host shell, and so on.'
        : 'Revoke admin privileges from "'+username+'"?\n\nThey go back to being a regular user.';
      if (!(await this.confirmAsync(msg))) return;
      try {
        const r = await this.api('/api/users/set-admin', {
          method: 'POST', body: JSON.stringify({ username, admin: makeAdmin }),
        });
        const d = await r.json();
        if (!r.ok) { this.showToast('error: ' + (d.error || r.status), 'err'); return; }
        this.showToast(makeAdmin ? (username+' is now an admin') : (username+' is no longer an admin'), 'ok');
        await this.loadUsers();
      } catch(e) { this.showToast(e.message, 'err'); }
    },
    async userDisable2FA(username) {
      if (!(await this.confirmAsync('Disable 2FA for "'+username+'"?\n\nThe user will be able to log in with the password alone until 2FA is turned back on from the panel.'))) return;
      try {
        const r = await this.api('/api/users/disable-2fa', {
          method: 'POST', body: JSON.stringify({ username }),
        });
        const d = await r.json();
        if (!r.ok) { this.showToast('error: ' + (d.error || r.status), 'err'); return; }
        this.showToast('2FA disabled', 'ok');
        await this.loadUsers();
      } catch(e) { this.showToast(e.message, 'err'); }
    },
    async userRevokeSessions(username) {
      if (!(await this.confirmAsync('Revoke ALL active sessions of "'+username+'"?\n\nThe user will be logged out on every device and will have to sign in again.'))) return;
      try {
        const r = await this.api('/api/users/revoke-sessions', {
          method: 'POST', body: JSON.stringify({ username }),
        });
        const d = await r.json();
        if (!r.ok) { this.showToast('error: ' + (d.error || r.status), 'err'); return; }
        this.showToast(d.revoked + ' session(s) revoked', 'ok');
        await this.loadUsers();
      } catch(e) { this.showToast(e.message, 'err'); }
    },
    async loadBandwidth() {
      try {
        const r = await fetch('/api/session/bandwidth', {
          credentials: 'include',
          headers: { 'Authorization': 'Bearer ' + this.token }
        });
        if (!r.ok) return;
        const d = await r.json();
        this.bw = { in: d.bytes_in || 0, out: d.bytes_out || 0, since: d.since || 0 };
      } catch(_) {}
    },
    async bwReset() {
      try {
        await fetch('/api/session/bandwidth/reset', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Authorization': 'Bearer ' + this.token }
        });
        this.bw = { in: 0, out: 0, since: Math.floor(Date.now()/1000) };
      } catch(_) {}
    },
    bwFormat(n) {
      n = Number(n) || 0;
      if (n < 1024) return n + ' B';
      if (n < 1024*1024) return (n/1024).toFixed(1) + ' KB';
      if (n < 1024*1024*1024) return (n/1024/1024).toFixed(1) + ' MB';
      return (n/1024/1024/1024).toFixed(2) + ' GB';
    },
    bwSince() {
      if (!this.bw.since) return '—';
      const d = new Date(this.bw.since * 1000);
      return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    },
    async loadBrowserInstances() {
      try {
        const r = await fetch('/api/browser-instances', {
          credentials: 'include',
          headers: { 'Authorization': 'Bearer ' + this.token }
        });
        if (!r.ok) return;
        const d = await r.json();
        this.bpInstances = d.instances || [];
        if (this.bpInstances.length && !this.bpInstances.find(i => i.name === this.bpInstance)) {
          this.bpInstance = this.bpInstances[0].name;
          try { localStorage.setItem('panel_bp_instance', this.bpInstance); } catch(_){}
        }
      } catch(_) {}
    },
    bpSwitchInstance(name) {
      if (name === this.bpInstance) return;
      this.bpInstance = name;
      try { localStorage.setItem('panel_bp_instance', name); } catch(_){}
      if (this.bpActive) {
        this.bpActive = false;
        this.$nextTick(() => { this.bpActive = true; });
      }
    },
    setSearchEngine(key) {
      if (!this.browserSearchEngines[key]) return;
      this.browserSearchEngine = key;
      try { localStorage.setItem('panel_browser_search_engine', key); } catch(_){}
      this.browserSearchOpen = false;
    },

    onTabDragStart(e, id) {
      try {
        e.dataTransfer.setData('text/x-panel-tab', id);
        e.dataTransfer.effectAllowed = 'move';
      } catch(_){}
    },
    onTabDragOver(e) { e.preventDefault(); try { e.dataTransfer.dropEffect = 'move'; } catch(_){} },
    onTabDrop(e, targetId) {
      e.preventDefault();
      const draggedId = (e.dataTransfer && e.dataTransfer.getData('text/x-panel-tab')) || '';
      if (!draggedId || draggedId === targetId) return;
      const from = this.browserTabs.findIndex(t=>t.id===draggedId);
      const to   = this.browserTabs.findIndex(t=>t.id===targetId);
      if (from<0 || to<0) return;
      if (this.browserTabs[from].pinned !== this.browserTabs[to].pinned) return;
      const [moved] = this.browserTabs.splice(from, 1);
      this.browserTabs.splice(to, 0, moved);
      this.persistBrowserTabs();
    },
    browserTabTitle(tab) {
      const t = this.browserTitles[tab.id + ':0'];
      if (t) return t;
      const u = (tab.panes[0] && tab.panes[0].url) || '';
      if (u === '/browser/' || u === '/browser') return 'New tab';
      const m = u.match(/^\/browser\/uv\/service\/(.+)$/);
      if (m) {
        const dec = this.uvDecode(m[1]);
        try { return new URL(dec).hostname || dec; } catch(e) { return dec.slice(0, 30); }
      }
      return 'Loading…';
    },

    async api(path, opts={}, _retried) {
      const isForm = (typeof FormData !== 'undefined') && opts.body instanceof FormData;
      const headers = {};
      if (!isForm) headers['Content-Type'] = 'application/json';
      Object.assign(headers, opts.headers || {});
      headers['Authorization'] = 'Bearer ' + this.token;
      const _t0 = Date.now();
      const r = await fetch(path, Object.assign({}, opts, { headers }));
      this._markApiLatency(Date.now() - _t0);
      if (r.status===401) {
        const isAuthPath = /\/api\/auth\/(refresh|refresh-cookie|login|logout)$/.test(path);
        if (!_retried && !isAuthPath) {
          let ok = false;
          try { ok = await this.refreshToken(); } catch(_) {}
          if (ok) return this.api(path, opts, true);
        }
        try { this.showToast('Session expired — please log in again', 'err'); } catch(_) {}
        this.logout(true);
        throw new Error('unauthorized');
      }
      if (opts.raw) return r;
      if (r.status >= 400) throw await this._apiError(r);
      return r;
    },

    async _apiError(r) {
      const ct = (r.headers.get('content-type') || '').toLowerCase();
      let msg = '', data = null;
      try {
        if (ct.includes('json')) {
          data = await r.json();
          if (data && typeof data === 'object') {
            msg = data.error || data.message || data.detail || data.reason || '';
            if (msg && typeof msg === 'object') msg = msg.message || msg.error || '';
            if (!msg) { try { msg = JSON.stringify(data); } catch(_) { msg = ''; } }
          } else if (data != null) {
            msg = String(data);
          }
        } else {
          msg = await r.text();
        }
      } catch(_) { /* unreadable/empty body — falls through to the fallback below */ }
      msg = String(msg == null ? '' : msg).replace(/\s+/g, ' ').trim();
      if (/^<(!doctype|html)/i.test(msg)) msg = '';
      if (msg.length > 300) msg = msg.slice(0, 300) + '…';
      if (!msg) msg = 'HTTP ' + r.status + (r.statusText ? ' ' + r.statusText : '');
      const err = new Error(msg);
      err.status = r.status;
      err.data = data;
      err.url = r.url;
      return err;
    },

    async login() {
      this.loginError = '';
      this.loginBusy = true;
      try {
        const r = await fetch('/api/auth/login', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(this.loginForm)});
        const d = await r.json();
        if (r.status === 423) {
          const retry = r.headers.get('Retry-After') || '60';
          this.loginError = (d.error || 'account locked') + ' ('+retry+'s)';
          return;
        }
        if (r.ok && d.totp_required) {
          this.loginNeedTOTP = true;
          this.loginError = '';
          return;
        }
        if (!r.ok) { this.loginError = d.error || 'Failed'; return; }
        this.token = d.token; this.username = d.user;
        localStorage.setItem('panel_token', this.token); localStorage.setItem('panel_user', this.username);
        localStorage.setItem('panel_last_user', d.user);
        localStorage.setItem('panel_active_user', d.user);
        this.loginNeedTOTP = false;
        this.loginForm.password = ''; this.loginForm.totp = '';
        this.loadUserEmail();
        this.scheduleTokenRefresh();
        this.init();
      } catch(e) { this.loginError = e.message; }
      finally { this.loginBusy = false; }
    },
    loginCancelTOTP() {
      this.loginNeedTOTP = false;
      this.loginForm.totp = '';
      this.loginForm.password = '';
      this.loginError = '';
    },

    scheduleTokenRefresh() {
      if (this.refreshTimer) clearTimeout(this.refreshTimer);
      const REFRESH_BEFORE = 45 * 60 * 1000;
      const TICK = 4 * 60 * 1000;
      const tick = () => {
        this.refreshTimer = null;
        this._maybeRefreshSoon(REFRESH_BEFORE);
        this.refreshTimer = setTimeout(tick, TICK);
      };
      this.refreshTimer = setTimeout(tick, TICK);
    },
    _applyToken(tok) {
      if (!tok) return;
      this.token = tok;
      try { localStorage.setItem('panel_token', tok); } catch(_) {}
    },
    _tokenExpMs() {
      try {
        const p = JSON.parse(base64ToText((this.token || '').split('.')[1]));
        return (p && p.exp) ? p.exp * 1000 : 0;
      } catch(_) { return 0; }
    },
    _maybeRefreshSoon(bufferMs) {
      if (!this.token) return;
      const exp = this._tokenExpMs();
      if (exp && (exp - Date.now()) < bufferMs) this.refreshToken();
    },
    _wireOpportunisticRefresh() {
      if (this._refreshWired) return;
      this._refreshWired = true;
      const onWake = () => { if (!document.hidden) this._maybeRefreshSoon(60 * 60 * 1000); };
      document.addEventListener('visibilitychange', onWake);
      window.addEventListener('focus', onWake);
      window.addEventListener('online', () => this._maybeRefreshSoon(60 * 60 * 1000));
    },
    async refreshToken() {
      if (this._refreshInFlight) return this._refreshInFlight;
      this._refreshInFlight = (async () => {
        if (this.token) {
          try {
            const r = await fetch('/api/auth/refresh', {
              method: 'POST',
              headers: { 'Authorization': 'Bearer ' + this.token, 'Content-Type': 'application/json' },
              body: '{}',
            });
            if (r.ok) {
              const d = await r.json().catch(() => null);
              if (d && d.token) { this._applyToken(d.token); return true; }
            } else if (r.status !== 401) {
              return false;
            }
          } catch(_) {
            return false;
          }
        }
        try {
          const r2 = await fetch('/api/auth/refresh-cookie', {
            method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
          });
          if (r2.ok) {
            const d = await r2.json().catch(() => null);
            if (d && d.token) { this._applyToken(d.token); return true; }
          }
        } catch(_) {}
        return false;
      })();
      try { return await this._refreshInFlight; } finally { this._refreshInFlight = null; }
    },

    async logout(skipConfirm) {
      if (!skipConfirm && this.token && !(await this.confirmAsync('Log out? The token for this session will be discarded.'))) return;
      if (this.token) {
        try { fetch('/api/auth/logout', {method:'POST', headers:{'Authorization':'Bearer '+this.token}}).catch(()=>{}); } catch(e){}
      }
      localStorage.removeItem('panel_token'); localStorage.removeItem('panel_user');
      document.cookie = 'panel_cookie_set=;Max-Age=-1;Path=/';
      this.token=''; this.username='';
      this._initDone = false;
      if (this.pollTimer) { clearInterval(this.pollTimer); this.pollTimer=null; }
      if (this.bwPollTimer) { clearInterval(this.bwPollTimer); this.bwPollTimer=null; }
      if (this.jobsPollTimer) { clearInterval(this.jobsPollTimer); this.jobsPollTimer=null; }
      if (this.jobsLiveTimer) { clearInterval(this.jobsLiveTimer); this.jobsLiveTimer=null; }
      if (this.jobsClockTimer) { clearInterval(this.jobsClockTimer); this.jobsClockTimer=null; }
      if (this.deployPollTimer) { clearInterval(this.deployPollTimer); this.deployPollTimer=null; }
      try { if (this.jobLog){ this.jobLog.reconnect.cancelled = true; if (this.jobLog.reconnect.timer) clearTimeout(this.jobLog.reconnect.timer); if (this.jobLog.ws) this.jobLog.ws.close(); } } catch(_){}
      if (this.procsLive) this.procsStop();
      if (this.bpPauseTimer) { clearTimeout(this.bpPauseTimer); this.bpPauseTimer=null; }
      if (this.refreshTimer) { clearTimeout(this.refreshTimer); this.refreshTimer=null; }
      if (this.clockTimer) { clearInterval(this.clockTimer); this.clockTimer=null; }
      if (this.notificationTimer) { clearInterval(this.notificationTimer); this.notificationTimer=null; }
      if (this.vcTickTimer) { clearInterval(this.vcTickTimer); this.vcTickTimer=null; }
      if (this.browserSnapTimer) { clearInterval(this.browserSnapTimer); this.browserSnapTimer=null; }
      if (window.PanelPresence && typeof window.PanelPresence.disconnect === 'function') {
        try { window.PanelPresence.disconnect(); } catch(_) {}
      }
      try { this.vcClearIncoming(); } catch(_) {}
      try { this.whatsappDisconnect && this.whatsappDisconnect(); } catch(_) {}
      try { this.vcUninstallShortcuts && this.vcUninstallShortcuts(); } catch(_) {}
      try {
        if (typeof __panelCharts !== 'undefined') {
          for (const [id, chart] of __panelCharts) {
            try { chart.destroy(); } catch(_) {}
          }
          __panelCharts.clear();
        }
      } catch(_) {}
      try { const bpFrame = document.getElementById('bpFrame'); if (bpFrame) bpFrame.src = 'about:blank'; } catch(_) {}
      try { document.querySelectorAll('iframe[id^="browserFrame"]').forEach(f => { try { f.src='about:blank'; } catch(_) {} }); } catch(_) {}
      (this.terms.panes||[]).forEach(p => { if (p.ws) try{ p.ws.close(); }catch(e){} });
      [this.detail.ws, this.detail.logWS, this.detail.statsWS].forEach(w=>{ if(w) try{w.close();}catch(e){} });
      if (this.whatsapp && this.whatsapp.ws) {
        try { this.whatsapp.ws.close(1000, 'logout'); } catch(_) {}
        this.whatsapp.ws = null;
      }
      if (window.PANEL && window.PANEL.videocall && window.PANEL.videocall.active) {
        try { window.PANEL.videocall.active.stop(); } catch(_) {}
      }
    },

    async loadUserEmail() {
      try {
        const r = await this.api('/api/auth/me');
        const d = await r.json();
        if (d.email) {
          this.userEmail = d.email;
          localStorage.setItem('panel_email', d.email);
        } else {
          this.userEmail = '';
          localStorage.removeItem('panel_email');
        }
      } catch(e) { /* silent — the topbar keeps showing just the username */ }
    },

    async mobileDevicesLoad() {
      this.mobileDevicesUI.busy = true; this.mobileDevicesUI.err = '';
      try {
        const r = await this.api('/api/auth/mobile-sessions');
        const d = await r.json();
        if (d && d.error) { this.mobileDevicesUI.err = d.error; this.mobileDevicesUI.busy = false; return; }
        this.mobileDevicesUI.items = Array.isArray(d) ? d : [];
      } catch(e) { this.mobileDevicesUI.err = String(e); }
      this.mobileDevicesUI.loaded = true;
      this.mobileDevicesUI.busy = false;
    },
    async mobileDeviceApprove(id) {
      this.mobileDevicesUI.busy = true;
      try {
        const r = await this.api('/api/auth/mobile-sessions/approve', {method:'POST', body: JSON.stringify({id})});
        const d = await r.json();
        if (d.error) { this.showToast(d.error, 'err'); } else { this.showToast('Device approved — it can log in with a passkey now', 'ok'); }
      } catch(e) { this.showToast(e.message, 'err'); }
      await this.mobileDevicesLoad();
    },
    async mobileDeviceDeny(id) {
      if (!(await this.confirmAsync('Deny this pairing? The device will have to scan the QR code again to try once more.'))) return;
      this.mobileDevicesUI.busy = true;
      try {
        const r = await this.api('/api/auth/mobile-sessions/deny', {method:'POST', body: JSON.stringify({id})});
        const d = await r.json();
        if (d.error) { this.showToast(d.error, 'err'); } else { this.showToast('Pairing denied', 'ok'); }
      } catch(e) { this.showToast(e.message, 'err'); }
      await this.mobileDevicesLoad();
    },
    async mobileDeviceRevoke(id) {
      if (!(await this.confirmAsync('Revoke this device? It will no longer be able to log in with a passkey until it is paired again.'))) return;
      this.mobileDevicesUI.busy = true;
      try {
        const r = await this.api('/api/auth/mobile-sessions/revoke', {method:'POST', body: JSON.stringify({id})});
        const d = await r.json();
        if (d.error) { this.showToast(d.error, 'err'); } else { this.showToast('Device revoked', 'ok'); }
      } catch(e) { this.showToast(e.message, 'err'); }
      await this.mobileDevicesLoad();
    },

    async mobilePairGenerate() {
      this.mobilePairUI.busy = true; this.mobilePairUI.err = '';
      try {
        const r = await this.api('/api/auth/mobile-pair', {method:'POST', raw:true});
        const d = await r.json();
        if (!r.ok || d.error) {
          this.mobilePairUI.err = d.error || 'Could not generate the pairing QR code.';
          this.mobilePairUI.qrPngB64 = '';
        } else {
          this.mobilePairUI.qrPngB64 = d.qr_png || '';
          this.mobilePairUI.serverUrl = d.server_url || '';
          this.mobilePairUI.expiresAt = Date.now() + (d.expires_in || 0) * 1000;
          if (!this._nowTimer) { this._nowTimer = setInterval(() => { this._nowTick = Date.now(); }, 1000); }
        }
      } catch(e) { this.mobilePairUI.err = String(e.message || e); }
      this.mobilePairUI.busy = false;
    },
    mobilePairSecondsLeft() {
      if (!this.mobilePairUI.expiresAt) return 0;
      return Math.max(0, Math.round((this.mobilePairUI.expiresAt - this._nowTick) / 1000));
    },

    async mfaLoadStatus() {
      try {
        const r = await this.api('/api/auth/mfa/status');
        const d = await r.json();
        if (d.error) { this.mfaUI.err = d.error; return; }
        this.mfaUI.enrolled = !!d.enrolled;
        this.mfaUI.factorId = d.factor_id || '';
        this.mfaUI.factorStatus = d.factor_status || '';
        this.mfaUI.backupCodesUnused = d.backup_codes_unused || 0;
      } catch(e) { this.mfaUI.err = String(e); }
    },
    async mfaStartEnroll() {
      this.mfaUI.busy = true; this.mfaUI.err = ''; this.mfaUI.code = '';
      try {
        const r = await this.api('/api/auth/mfa/enroll-start', {method:'POST', body:'{}'});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); this.mfaUI.busy = false; return; }
        this.mfaUI.factorId = d.factor_id;
        this.mfaUI.qr = this.mfaNormalizeSVG(d.qr_code);
        this.mfaUI.secret = d.secret;
        this.mfaUI.step = 'confirm';
      } catch(e) { this.showToast(e.message,'err'); }
      this.mfaUI.busy = false;
    },
    mfaNormalizeSVG(svgText) {
      if (!svgText || typeof svgText !== 'string') return svgText;
      try {
        const parser = new DOMParser();
        const doc = parser.parseFromString(svgText, 'image/svg+xml');
        const svg = doc.querySelector('svg');
        if (!svg) return svgText;
        const w = svg.getAttribute('width');
        const h = svg.getAttribute('height');
        if (w && h && !svg.hasAttribute('viewBox')) {
          const wn = parseFloat(w);
          const hn = parseFloat(h);
          if (wn > 0 && hn > 0) {
            svg.setAttribute('viewBox', `0 0 ${wn} ${hn}`);
          }
        }
        svg.setAttribute('preserveAspectRatio', 'xMidYMid meet');
        svg.removeAttribute('width');
        svg.removeAttribute('height');
        return new XMLSerializer().serializeToString(svg);
      } catch(e) {
        console.warn('[mfa] SVG normalize failed, using raw:', e);
        return svgText;
      }
    },
    async mfaConfirmEnroll() {
      if (!this.mfaUI.code || this.mfaUI.code.length < 6) {
        this.showToast('Enter the 6-digit code from the app','err'); return;
      }
      this.mfaUI.busy = true;
      try {
        const r = await this.api('/api/auth/mfa/enroll-verify', {method:'POST',
          body: JSON.stringify({factor_id: this.mfaUI.factorId, code: this.mfaUI.code})});
        const d = await r.json();
        if (d.error || !d.verified) { this.showToast(d.error || 'verify failed','err'); this.mfaUI.busy = false; return; }
        this.mfaUI.backupCodes = d.backup_codes || [];
        this.mfaUI.backupCodesUnused = (d.backup_codes||[]).length;
        this.mfaUI.enrolled = true;
        this.mfaUI.step = 'backup';
        this.mfaUI.code = '';
        this.showToast('MFA enabled — save the backup codes!','ok');
      } catch(e) { this.showToast(e.message,'err'); }
      this.mfaUI.busy = false;
    },
    mfaCancelEnroll() {
      this.mfaUI.step = 'idle'; this.mfaUI.qr = ''; this.mfaUI.secret = '';
      this.mfaUI.code = ''; this.mfaUI.factorId = '';
    },
    mfaCloseBackup() {
      this.mfaUI.backupCodes = [];
      this.mfaUI.step = 'idle';
    },
    async mfaDisable() {
      if (!(await this.confirmAsync('Disable Supabase MFA? This removes the factor and erases the backup codes.'))) return;
      this.mfaUI.busy = true;
      try {
        const r = await this.api('/api/auth/mfa/disable', {method:'POST', body:'{}'});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); this.mfaUI.busy = false; return; }
        this.mfaUI.enrolled = false;
        this.mfaUI.factorId = '';
        this.mfaUI.backupCodesUnused = 0;
        this.showToast('Supabase MFA disabled','ok');
      } catch(e) { this.showToast(e.message,'err'); }
      this.mfaUI.busy = false;
    },
    async mfaCopyBackupCodes() {
      try {
        await navigator.clipboard.writeText(this.mfaUI.backupCodes.join('\n'));
        this.showToast('Backup codes copied — paste them into a password manager','ok');
      } catch(e) { this.showToast('Failed to copy: '+e.message,'err'); }
    },

    async loadStats() { try{const r=await this.api('/api/system/stats'); this.stats=await r.json(); this.hostname=this.stats?.host?.hostname||''; this.statsError=null; this.statsAt=Math.floor(Date.now()/1000);}catch(e){ console.warn('[stats] error:', e); this.statsError = e?.message||'failed to load stats'; } },

    procsQueryString() {
      const q = this.procsQ;
      const p = new URLSearchParams();
      const name = (q.name||'').trim();
      const user = (q.user||'').trim();
      const cmd  = (q.cmd||'').trim();
      if (name) p.set('name', name);
      if (user) p.set('user', user);
      if (cmd)  p.set('cmd',  cmd);
      if (q.sort) p.set('sort', q.sort);
      if (q.limit) p.set('limit', q.limit);
      if (q.offset) p.set('offset', q.offset);
      return p.toString();
    },
    async procsRefreshOnce() {
      this._procsReqId = (this._procsReqId || 0) + 1;
      const myId = this._procsReqId;
      try {
        const r = await this.api('/api/procs?' + this.procsQueryString());
        if (myId !== this._procsReqId) return;
        this.procs = await r.json();
      } catch(e){ console.warn('[procs] refresh error:', e); }
    },
    procsRefresh() {
      this.procsRefreshOnce();
      if (this.procsLive) { this.procsStop(); this.procsStart(); }
    },
    procsSetSort(col) { this.procsQ.sort = col; this.procsRefresh(); },
    _ncpu() { return (this.stats && this.stats.cpu && this.stats.cpu.cores) || navigator.hardwareConcurrency || 1; },
    procCpuPct(p) { return (p && p.cpu || 0) / this._ncpu(); },
    procCpuCls(p) { const v = this.procCpuPct(p); return v > 25 ? 'text-red-400' : v > 10 ? 'text-yellow-400' : ''; },
    procsZoomSet(v) {
      v = Math.max(0.6, Math.min(1.6, Math.round(v * 10) / 10));
      this.procsZoom = v;
      try { localStorage.setItem('panel_procs_zoom', String(v)); } catch(_){}
      this.procsLayoutDirty = true;
    },
    async procsLayoutSave() {
      try {
        const layout = { zoom: this.procsZoom, sort: this.procsQ.sort, limit: this.procsQ.limit };
        const r = await this.api('/api/user/prefs', { method: 'POST', body: JSON.stringify({ key: 'processes', value: layout }) });
        if (!r.ok) { this.showToast('Failed to save (' + r.status + ')', 'err'); return; }
        this.procsLayoutDirty = false;
        this.showToast('Layout saved to the profile', 'ok');
      } catch(e) { this.showToast(e.message || 'error', 'err'); }
    },
    async procsLayoutLoad() {
      try {
        const r = await this.api('/api/user/prefs?key=processes');
        if (!r.ok) return;
        const d = await r.json();
        const v = d && d.value;
        if (!v || typeof v !== 'object') return;
        if (typeof v.zoom === 'number') {
          this.procsZoom = v.zoom;
          try { localStorage.setItem('panel_procs_zoom', String(v.zoom)); } catch(_){}
        }
        if (v.sort) this.procsQ.sort = v.sort;
        if (v.limit) this.procsQ.limit = v.limit;
        this.procsLayoutDirty = false;
      } catch(_){}
    },
    procsStart() {
      if (this.procsLive) return;
      this.procsRefreshOnce();
      try {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const url = `${proto}//${location.host}/ws/procs?${this.procsQueryString()}`;
        this._procsWSId = (this._procsWSId || 0) + 1;
        const myId = this._procsWSId;
        const ws = new WebSocket(url);
        ws.onmessage = (ev) => {
          if (myId !== this._procsWSId) return;
          try { this.procs = JSON.parse(ev.data); } catch {}
        };
        ws.onclose = () => {
          if (myId !== this._procsWSId) return;
          this.procsLive = false;
          this.procsWS = null;
        };
        ws.onerror = () => { /* onclose will clear it */ };
        ws.onopen = () => {
          if (myId !== this._procsWSId) { try { ws.close(); } catch {} return; }
          this.procsLive = true;
        };
        this.procsWS = ws;
      } catch(e){ console.warn('[procs] WS error:', e); this.procsLive = false; }
    },
    procsStop() {
      this._procsWSId = (this._procsWSId || 0) + 1;
      if (this.procsWS) { try { this.procsWS.close(); } catch {} }
      this.procsWS = null;
      this.procsLive = false;
    },
    async loadTodos() {
      this.todosLoading = true;
      try {
        const r = await this.api('/api/todos');
        const d = await r.json();
        this.todos = d.todos || [];
        this.todosSummary = d.summary || { overdue:0, week:0, month:0, future:0, done:0 };
        this._loadOk('todos');
      } catch(e){ this._loadErr('todos', e, 'failed to load the todos'); }
      finally { this.todosLoading = false; }
    },
    todosByBucket(bucket) {
      const now = Math.floor(Date.now()/1000);
      return (this.todos||[]).filter(t => {
        if (t.status === 'done') return bucket === 'done';
        if (t.status === 'snoozed' && t.snooze_until > now) return bucket === 'future';
        if (!t.due) return bucket === 'future';
        const dt = t.due - now;
        if (dt < 0)              return bucket === 'overdue';
        if (dt < 7*86400)        return bucket === 'week';
        if (dt < 31*86400)       return bucket === 'month';
        return bucket === 'future';
      });
    },
    openTodoForm() {
      this.todoForm = { open:true, t:{ title:'', notes:'', category:'custom', interval_days:0, notify_wa:false }, dueDate:'', editingId:null };
    },
    editTodo(t) {
      this.todoForm = {
        open: true,
        t: { title:t.title, notes:t.notes||'', category:t.category||'custom', interval_days:t.interval_days||0, notify_wa:!!t.notify_wa },
        dueDate: t.due ? new Date(t.due*1000).toISOString().slice(0,10) : '',
        editingId: t.id,
      };
    },
    async saveTodo() {
      const body = { ...this.todoForm.t };
      if (this.todoForm.dueDate) {
        body.due = Math.floor(new Date(this.todoForm.dueDate + 'T00:00:00').getTime() / 1000);
      } else if (body.interval_days > 0) {
        body.due = 0;
      } else {
        body.due = 0;
      }
      if (!body.title.trim()) { this.showToast('title is required','err'); return; }
      try {
        const url = this.todoForm.editingId ? '/api/todos/'+this.todoForm.editingId : '/api/todos';
        const method = this.todoForm.editingId ? 'PATCH' : 'POST';
        const r = await this.api(url, { method, body: JSON.stringify(body) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.todoForm.open = false;
        this.showToast(this.todoForm.editingId?'TODO updated':'TODO created','ok');
        await this.loadTodos();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async markTodoDone(t) {
      try {
        const r = await this.api('/api/todos/'+t.id+'/done', { method:'POST' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast(t.interval_days>0?'done (next in '+t.interval_days+'d)':'done','ok');
        await this.loadTodos();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async snoozeTodoPrompt(t) {
      const raw = await this.askInput({ title:'Snooze TODO', label:'Snooze for how many days?', value:'7', placeholder:'7', validate:(v)=>{ const n=parseInt(v,10); return (!n||n<1)?'Enter a number of days >= 1':''; } });
      if (raw === null) return;
      const days = parseInt(raw, 10);
      if (!days || days < 1) return;
      const until = Math.floor(Date.now()/1000) + days*86400;
      try {
        const r = await this.api('/api/todos/'+t.id+'/snooze', { method:'POST', body: JSON.stringify({ until_unix: until }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast('snoozed +'+days+'d','ok');
        await this.loadTodos();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async deleteTodo(t) {
      if (!(await this.confirmAsync('Delete "'+t.title+'"?'))) return;
      try {
        const r = await this.api('/api/todos/'+t.id, { method:'DELETE' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        await this.loadTodos();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async loadJobs() {
      this.jobsLoading = true;
      try {
        const r = await this.api('/api/queue');
        const d = await r.json();
        this.jobs = d.jobs || [];
        const cutoff = Math.floor(Date.now()/1000) - 86400;
        this.jobsCounts = {
          running: this.jobs.filter(j=>j.status==='running').length,
          queued:  this.jobs.filter(j=>j.status==='queued').length,
          failed:  this.jobs.filter(j=>j.status==='failed' && (j.finished||0) >= cutoff).length,
        };
        try { this._jobLogResolveRestore(); } catch(_){}
        try { this._jobLogReconcileMinimized(); } catch(_){}
        try { this._deployPollReconcile(); } catch(_){}
        this._loadOk('jobs');
      } catch(e){ this._loadErr('jobs', e, 'failed to load the job queue'); }
      finally { this.jobsLoading = false; }
    },
    jobsPrefsTouch() {
      try { localStorage.setItem('panel_jobs_prefs', JSON.stringify(this.jobsPrefs)); } catch(_){}
      this.jobsPrefsDirty = true;
    },
    async jobsPrefsSave() {
      try {
        const r = await this.api('/api/user/prefs', { method:'POST', body: JSON.stringify({ key:'jobs', value: this.jobsPrefs }) });
        if (!r.ok) { this.showToast('Failed to save ('+r.status+')','err'); return; }
        this.jobsPrefsDirty = false;
        this.showToast('Preferences saved to the profile','ok');
      } catch(e){ this.showToast(e.message||'error','err'); }
    },
    async jobsPrefsLoad() {
      try {
        const r = await this.api('/api/user/prefs?key=jobs');
        if (!r.ok) return;
        const d = await r.json();
        const v = d && d.value;
        if (!v || typeof v !== 'object') return;
        this.jobsPrefs = Object.assign({ groupBy:'task', fontSize:'sm', filterStatus:'', filterKind:'', filterText:'' }, v);
        try { localStorage.setItem('panel_jobs_prefs', JSON.stringify(this.jobsPrefs)); } catch(_){}
        this.jobsPrefsDirty = false;
      } catch(_){}
    },
    openJobLauncher() { this.jobLauncher = { open:true, kind:'docker_pull', args:{}, argsText:'' }; },
    async enqueueJob() {
      const k = this.jobLauncher.kind;
      let args = { ...this.jobLauncher.args };
      if (k === 'shell') {
        args = { cmd: args.cmd, args: (this.jobLauncher.argsText||'').split('\n').filter(s=>s.length>0) };
      }
      try {
        const r = await this.api('/api/queue', { method:'POST', body: JSON.stringify({ kind:k, args, source:'user' }) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.jobLauncher.open = false;
        this.showToast('job queued: '+d.id, 'ok');
        await this.loadJobs();
      } catch(e){ this.showToast('error: '+e.message, 'err'); }
    },
    cancelJob(j) {
      this.askConfirm('Cancel job', 'Cancel "'+this.jobLabel(j)+'" ('+j.id+')?', async () => {
        try {
          const r = await this.api('/api/queue/'+j.id+'/cancel', { method:'POST' });
          if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
          await this.loadJobs();
        } catch(e){ this.showToast('error: '+e.message, 'err'); }
      }, { danger:true });
    },
    async reRunJob(j) {
      try {
        const r = await this.api('/api/queue/'+j.id+'/rerun', { method:'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.showToast('job '+d.id+' restarted', 'ok');
        await this.loadJobs();
      } catch(e){ this.showToast('error: '+e.message, 'err'); }
    },
    deleteJob(j) {
      this.askConfirm('Delete from history', 'Delete "'+this.jobLabel(j)+'" ('+j.id+') from history?', async () => {
        try {
          const r = await this.api('/api/queue/'+j.id, { method:'DELETE', raw:true });
          if (!r.ok) {
            const d = await r.json().catch(()=>({}));
            if (r.status === 409) throw new Error('cancel the job before deleting');
            throw new Error(d.error || ('HTTP '+r.status));
          }
          await this.loadJobs();
        } catch(e){ this.showToast('error: '+e.message, 'err'); }
      }, { danger:true });
    },
    _emptyJobLog() {
      return { open:false, minimized:false, id:'', status:'', progress:0, step:'', text:'', ws:null, kind:'', reconnect:{ attempts:0, timer:null, cancelled:false }, _replay:false };
    },
    openJobLog(j) {
      this.closeJobLog();
      this.jobLog = Object.assign(this._emptyJobLog(), {
        open:true, id:j.id, status:j.status||'', progress:j.progress||0, step:j.step||'', kind:j.kind||'',
      });
      this._jobLogPersist();
      this._jobLogConnect();
    },
    _jobLogConnect() {
      const id = this.jobLog.id;
      if (!id) return;
      if (this.jobLog.ws) { try { this.jobLog.ws.close(); } catch {} this.jobLog.ws = null; }
      this.jobLog._replay = true;
      try {
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const ws = new WebSocket(`${proto}//${location.host}/ws/queue/${id}`);
        ws.onopen = () => { this.jobLog.reconnect.attempts = 0; };
        ws.onmessage = (ev) => {
          if (this.jobLog.id !== id) return;
          try {
            const d = JSON.parse(ev.data);
            if (d.type === 'log') {
              if (this.jobLog._replay) { this.jobLog.text = d.log || ''; this.jobLog._replay = false; }
              else                     { this.jobLog.text += d.log || ''; }
              const CAP = 256*1024;
              if (this.jobLog.text.length > CAP) {
                this.jobLog.text = '…[log truncated]…\n' + this.jobLog.text.slice(-CAP);
              }
            }
            if (d.type === 'progress') this.jobLog.progress = d.progress || 0;
            if (d.type === 'step')     this.jobLog.step = d.step || '';
            if (d.type === 'status') {
              this.jobLog.status = d.status;
              if (d.progress) this.jobLog.progress = d.progress;
              if (d.step) this.jobLog.step = d.step;
              this._jobLogPersist();
              this.loadJobs();
            }
            if (this.jobLog.open) this.$nextTick(() => {
              const el = this.$refs.jobLogPre;
              if (el) el.scrollTop = el.scrollHeight;
            });
          } catch {}
        };
        ws.onclose = () => {
          if (this.jobLog.ws && this.jobLog.ws !== ws) return;
          if (this.jobLog.ws === ws) this.jobLog.ws = null;
          if (this.jobLog.id !== id || this.jobLog.reconnect.cancelled) return;
          if (['done','failed','cancelled','interrupted'].indexOf(this.jobLog.status) >= 0) return;
          this.jobLog.reconnect.attempts += 1;
          const base = Math.min(30000, 1000 * Math.pow(2, this.jobLog.reconnect.attempts-1));
          const delay = Math.floor(base * (0.8 + Math.random()*0.4));
          if (this.jobLog.reconnect.timer) clearTimeout(this.jobLog.reconnect.timer);
          this.jobLog.reconnect.timer = setTimeout(() => {
            if (this.jobLog.id === id && !this.jobLog.reconnect.cancelled) this._jobLogConnect();
          }, delay);
        };
        this.jobLog.ws = ws;
      } catch(e){ console.warn('[jobs] WS:', e); }
    },
    minimizeJobLog() {
      if (!this.jobLog.id) return;
      this.jobLog.open = false;
      this.jobLog.minimized = true;
    },
    reopenJobLog() {
      if (!this.jobLog.id) return;
      this.jobLog.open = true;
      this.jobLog.minimized = false;
      this.jobLog.reconnect.cancelled = false;
      if (!this.jobLog.ws) this._jobLogConnect();
      this.$nextTick(() => {
        const el = this.$refs.jobLogPre;
        if (el) el.scrollTop = el.scrollHeight;
      });
    },
    closeJobLog() {
      this.jobLog.reconnect.cancelled = true;
      if (this.jobLog.reconnect.timer) { try { clearTimeout(this.jobLog.reconnect.timer); } catch {} }
      if (this.jobLog.ws) { try { this.jobLog.ws.close(); } catch {} }
      this._jobLogClearPersist();
      this.jobLog = this._emptyJobLog();
    },
    _jobLogPersist() {
      try {
        if (!this.jobLog.id) { localStorage.removeItem('panel_joblog'); return; }
        localStorage.setItem('panel_joblog', JSON.stringify({ id:this.jobLog.id, status:this.jobLog.status, kind:this.jobLog.kind, savedAt:Date.now() }));
      } catch(_){}
    },
    _jobLogClearPersist() { try { localStorage.removeItem('panel_joblog'); } catch(_){} },
    _jobLogRestoreLoad() {
      try {
        const raw = JSON.parse(localStorage.getItem('panel_joblog') || 'null');
        if (raw && raw.id) this._jobLogRestoreId = raw.id;
      } catch(_){}
    },
    _jobLogResolveRestore() {
      const id = this._jobLogRestoreId;
      if (!id) return;
      this._jobLogRestoreId = '';
      const j = (this.jobs || []).find(x => x.id === id);
      if (j && (j.status === 'running' || j.status === 'queued')) {
        this.jobLog = Object.assign(this._emptyJobLog(), {
          open:false, minimized:true, id:j.id, status:j.status, progress:j.progress||0, step:j.step||'', kind:j.kind||'',
        });
        this._jobLogPersist();
      } else {
        this._jobLogClearPersist();
      }
    },
    _jobLogReconcileMinimized() {
      if (!this.jobLog.minimized || !this.jobLog.id) return;
      const j = (this.jobs || []).find(x => x.id === this.jobLog.id);
      if (!j) { this.closeJobLog(); return; }
      this.jobLog.status = j.status;
      if (typeof j.progress === 'number') this.jobLog.progress = j.progress;
      if (j.step) this.jobLog.step = j.step;
      if (['done','failed','cancelled','interrupted'].indexOf(j.status) >= 0) this.closeJobLog();
    },
    JOB_KINDS: {
      apt_upgrade:         { icon:'📦', label:'System update', desc:'updates system packages', linear:true,  summary: () => 'apt update + upgrade' },
      docker_pull:         { icon:'🐳', label:'Docker pull',            desc:'pulls an image from the registry',    linear:true,  summary: a => (a&&a.ref) || 'image' },
      docker_compose_pull: { icon:'🧩', label:'Compose pull',           desc:'pulls compose images',     linear:true,  summary: a => (a&&a.dir) || 'compose' },
      image_prune:         { icon:'🧹', label:'Image cleanup',     desc:'removes dangling images',        linear:false, summary: () => 'docker image prune -af' },
      backup_now:          { icon:'💾', label:'Backup',                 desc:'archives vault + config',      linear:false, summary: a => (a&&a.target) || 'all' },
      shell:               { icon:'⌨️', label:'Shell',                  desc:'runs a command',             linear:false, summary: a => a ? [a.cmd, ...((a.args)||[])].filter(Boolean).join(' ') : 'command' },
      jira_ai_analysis:    { icon:'🤖', label:'AI analysis (Jira)',      desc:'Claude audits the ticket',      linear:false, summary: a => (a&&a.issue_key) || 'ticket' },
    },
    kindMeta(kind) {
      return this.JOB_KINDS[kind] || { icon:'⚙️', label:kind||'job', desc:'', linear:false, summary: () => '' };
    },
    jobDesc(j) { return this.kindMeta(j.kind).desc || ''; },
    jobLabel(j)   { return this.kindMeta(j.kind).label; },
    jobSummary(j) { try { return this.kindMeta(j.kind).summary(j.args) || ''; } catch { return ''; } },
    jobStatusLabel(s) {
      return ({ running:'running', queued:'queued', done:'done', failed:'failed', cancelled:'cancelled', interrupted:'interrupted' })[s] || s;
    },
    _stableStr(o) {
      if (o == null) return '';
      if (typeof o !== 'object') return JSON.stringify(o);
      if (Array.isArray(o)) return '[' + o.map(x => this._stableStr(x)).join(',') + ']';
      return '{' + Object.keys(o).sort().map(k => JSON.stringify(k)+':'+this._stableStr(o[k])).join(',') + '}';
    },
    jobGroupKey(j) { return j.kind + '|' + this._stableStr(j.args); },
    filteredJobs() {
      let js = this.jobs || [];
      const p = this.jobsPrefs;
      if (p.filterStatus) js = js.filter(j => j.status === p.filterStatus);
      if (p.filterKind)   js = js.filter(j => j.kind === p.filterKind);
      if (p.filterText) {
        const q = p.filterText.toLowerCase();
        js = js.filter(j => (this.jobLabel(j)+' '+this.jobDesc(j)+' '+this.jobSummary(j)+' '+(j.id||'')+' '+(j.kind||'')).toLowerCase().includes(q));
      }
      return js;
    },
    jobKindsPresent() {
      const set = new Set((this.jobs||[]).map(j => j.kind));
      return [...set];
    },
    jobUnits() {
      const jobs = this.filteredJobs();
      const mode = this.jobsPrefs.groupBy || 'task';
      if (mode === 'none') return jobs.map(j => ({ type:'single', key:'s:'+j.id, mode, job:j }));
      const keyOf = mode === 'kind'   ? (j => j.kind)
                  : mode === 'status' ? (j => 'st:'+j.status)
                  : (j => this.jobGroupKey(j));
      const bucketAlways = (mode === 'kind' || mode === 'status');
      const byKey = {};
      for (const j of jobs) (byKey[keyOf(j)] ||= []).push(j);
      const seen = new Set(), units = [];
      for (const j of jobs) {
        const k = keyOf(j);
        if (seen.has(k)) continue;
        seen.add(k);
        const arr = byKey[k];
        if (arr.length >= 2 || bucketAlways) units.push({ type:'group', key:'g:'+k, mode, kind:j.kind, args:j.args, status:j.status, jobs:arr });
        else units.push({ type:'single', key:'s:'+j.id, mode, job:j });
      }
      return units;
    },
    groupIcon(u)  { return u.mode === 'status' ? '◆' : this.kindMeta(u.kind).icon; },
    groupLabel(u) { return u.mode === 'status' ? this.jobStatusLabel(u.status) : this.kindMeta(u.kind).label; },
    groupSummary(u) {
      if (u.mode === 'status') return '';
      if (u.mode === 'kind')   return this.kindMeta(u.kind).desc || '';
      return this.jobSummary({ kind:u.kind, args:u.args });
    },
    jobGroupCounts(jobs) {
      jobs = jobs || [];
      const c = { running:0, queued:0, done:0, failed:0, cancelled:0, total:jobs.length };
      for (const j of jobs) if (c[j.status] !== undefined) c[j.status]++;
      return c;
    },
    isJobGroupCollapsed(key, jobs) {
      jobs = jobs || [];

      if (Object.prototype.hasOwnProperty.call(this.jobGroupsCollapsed, key)) return this.jobGroupsCollapsed[key];
      return !jobs.some(j => j.status === 'running' || j.status === 'queued');
    },
    toggleJobGroup(key, jobs) {
      this.jobGroupsCollapsed[key] = !this.isJobGroupCollapsed(key, jobs);
      try { localStorage.setItem('panel_job_groups', JSON.stringify(this.jobGroupsCollapsed)); } catch {}
    },
    jobElapsed(j) {
      if (j.status === 'queued') return j.queued ? Math.max(0, this.jobsNow - j.queued) : null;
      if (!j.started) return null;
      const end = j.finished || this.jobsNow;
      return Math.max(0, end - j.started);
    },
    fmtDur(secs) {
      if (secs == null || secs < 0) return '—';
      secs = Math.floor(secs);
      if (secs < 60) return secs + 's';
      const m = Math.floor(secs / 60), s = secs % 60;
      if (m < 60) return s ? `${m}m ${s}s` : `${m}m`;
      const h = Math.floor(m / 60), mm = m % 60;
      return mm ? `${h}h ${mm}m` : `${h}h`;
    },
    jobETA(j) {
      if (j.status !== 'running' || !this.kindMeta(j.kind).linear) return null;
      const p = j.progress || 0;
      if (p <= 2) return null;
      const el = this.jobElapsed(j);
      if (!el) return null;
      const remain = el / p * (100 - p);
      if (!isFinite(remain) || remain < 0) return null;
      return Math.round(remain);
    },
    jobStatusChip(s) {
      return ({
        running:   'text-cyan-300 bg-cyan-500/10 border border-cyan-500/30',
        queued:    'text-gray-300 bg-gray-500/10 border border-gray-500/30',
        done:      'text-emerald-300 bg-emerald-500/10 border border-emerald-500/30',
        failed:    'text-rose-300 bg-rose-500/10 border border-rose-500/30',
        cancelled: 'text-amber-300 bg-amber-500/10 border border-amber-500/30',
        interrupted: 'text-orange-300 bg-orange-500/10 border border-orange-500/30',
      })[s] || 'text-gray-400 bg-gray-500/10';
    },
    jobBorderClass(s) {
      return ({
        running:'border-cyan-500/60', queued:'border-gray-600/50',
        done:'border-emerald-500/50', failed:'border-rose-500/60', cancelled:'border-amber-500/50',
        interrupted:'border-orange-500/50',
      })[s] || 'border-[#1f2a3d]';
    },
    jobBarClass(j) {
      if (j.status === 'running')   return 'bg-cyan-500 transition-all duration-700 ease-out';
      if (j.status === 'done')      return 'bg-emerald-500';
      if (j.status === 'failed')    return 'bg-rose-500';
      if (j.status === 'cancelled') return 'bg-amber-500';
      if (j.status === 'interrupted') return 'bg-orange-500';
      return 'bg-gray-600';
    },
    jobBarPct(j) {
      if (j.status === 'done') return 100;
      if (j.status === 'queued') return j.progress || 0;
      return j.progress || 0;
    },
    async loadAIPrompts() {
      this.aiPromptsLoading = true; this.aiPromptsErr = '';
      try {
        const r = await this.api('/api/ai/prompts', { raw:true });
        if (r.status===403) { this.aiPromptsErr = 'Only the admin (primary) user can view or edit the prompts.'; this.aiPrompts = []; return; }
        if (!r.ok) { this.aiPromptsErr = 'Failed to load ('+r.status+')'; return; }
        const d = await r.json();
        this.aiPrompts = d.prompts || [];
        const edits = {};
        for (const p of this.aiPrompts) {
          edits[p.id] = (p.id in this.aiPromptEdits) ? this.aiPromptEdits[p.id] : p.value;
        }
        this.aiPromptEdits = edits;
      } catch(e){ if(String(e).indexOf('unauthorized')<0) this.aiPromptsErr = String(e); }
      finally { this.aiPromptsLoading = false; }
    },
    aiPromptDirty(p) {
      return (this.aiPromptEdits[p.id] ?? '') !== p.value;
    },
    async saveAIPrompt(p) {
      this.aiPromptSaving[p.id] = true;
      this.aiPromptErrors[p.id] = [];
      try {
        const r = await this.api('/api/ai/prompts', { method:'PUT', raw:true, body: JSON.stringify({ id:p.id, value: this.aiPromptEdits[p.id] }) });
        if (r.status===422) {
          const d = await r.json().catch(()=>({}));
          this.aiPromptErrors[p.id] = d.reasons || [d.error || 'rejected'];
          this.showToast('Prompt rejected by the guard', 'err');
          return;
        }
        if (!r.ok) { this.showToast('Failed to save ('+r.status+')', 'err'); return; }
        const d = await r.json();
        this.aiPrompts = d.prompts || this.aiPrompts;
        for (const np of this.aiPrompts) {
          if (np.id===p.id) this.aiPromptEdits[np.id] = np.value;
        }
        this.aiPromptErrors[p.id] = [];
        this.showToast('Prompt saved', 'ok');
      } catch(e){ if(String(e).indexOf('unauthorized')<0) this.showToast(String(e),'err'); }
      finally { this.aiPromptSaving[p.id] = false; }
    },
    resetAIPrompt(p) {
      this.aiPromptEdits[p.id] = p.default;
      return this.saveAIPrompt(p);
    },

    async loadSchedJobs() {
      this.schedJobsLoading = true;
      try {
        const r = await this.api('/api/scheduler/jobs');
        const d = await r.json();
        this.schedJobs = d.jobs || [];
        this._loadOk('schedJobs');
      } catch(e){ this._loadErr('schedJobs', e, 'failed to load scheduled tasks'); }
      finally { this.schedJobsLoading = false; }
    },
    async loadSchedCatalog() {
      try {
        const r = await this.api('/api/scheduler/catalog');
        const d = await r.json();
        this.schedCatalog = d.kinds || [];
        this.schedCatalogPrimary = !!d.is_primary;
        this.schedCatalogLoaded = true;
      } catch(e){ console.warn('[sched] catalog:', e); }
    },
    schedDescriptor(kind) {
      return (this.schedCatalog||[]).find(d => d.kind === kind) || null;
    },
    schedKindLabel(kind) {
      const d = this.schedDescriptor(kind);
      return d ? d.label : (kind || '—');
    },
    applySchedTemplate(t) {
      if (!this.schedDescriptor(t.kind)) { this.showToast('that type is not available for your account','err'); return; }
      this.openSchedForm(t.kind);
      if (t.name) this.schedForm.j.name = t.name;
      if (t.schedule) { this.schedForm.j.schedule = t.schedule; this.schedForm.sched = this.schedParseToBuilder(t.schedule); }
      if (t.args) Object.assign(this.schedForm.args, t.args);
      if (t.dest) Object.assign(this.schedForm.dest, t.dest);
      this.schedReconcileCustom();
      this.schedPreview();
    },
    schedTemplatesAvailable() {
      return (this.schedTemplates||[]).filter(t => !!this.schedDescriptor(t.kind));
    },
    duplicateSchedJob(j) {
      this.editSchedJob(j);
      this.schedForm.j.id = '';
      this.schedForm.j.name = (j.name || '') + ' (copy)';
    },
    schedGroupCatalog(q) {
      q = (q||'').trim().toLowerCase();
      const items = (this.schedCatalog||[]).filter(d => !q || (d.label+' '+(d.description||'')+' '+d.kind).toLowerCase().includes(q));
      const byCat = {};
      items.forEach(d => { const c = d.category || 'Other'; (byCat[c] = byCat[c] || []).push(d); });
      const order = this.schedCatOrder;
      return Object.keys(byCat)
        .sort((a,b) => { const ia=order.indexOf(a), ib=order.indexOf(b); return (ia<0?99:ia)-(ib<0?99:ib); })
        .map(c => ({ category:c, icon:this.schedCatIcons[c]||'📦', items:byCat[c] }));
    },
    openSchedInfo(d) {
      if (typeof d === 'string') d = this.schedDescriptor(d) || { kind:d, label:d };
      this.schedInfo = {
        open:true, kind:d.kind, label:d.label || d.kind, icon:this.schedKindIcon(d.kind),
        description:d.description || '', details:d.details || '',
        useCases:d.use_cases || [], examples:d.examples || [], output:d.output || '',
        nextSteps:d.next_steps || [], requiresPrimary:!!d.requires_primary,
      };
    },
    schedKindIcon(kind) {
      return ({
        apt_upgrade:'📦', docker_pull:'🐳', docker_compose_pull:'🧩',
        image_prune:'🧹', backup_now:'💾', shell:'⌨️', jira_ai_analysis:'🤖',
        docker_restart:'🔄', docker_compose_restart:'🔁',
        systemd_restart:'🔧', docker_prune:'🧽', http_check:'🌐',
        ssl_check:'🔒', disk_check:'💽', security_audit:'🛡️', rootkit_scan:'🕵️',
        integrity_check:'🔎', trivy_scan:'🐛', fail2ban_report:'🚫',
        audit_report:'📋', cleanup:'🧯', session_backup:'🖥️',
      })[kind] || '⚙️';
    },
    schedArgsOf(kind) {
      const d = this.schedDescriptor(kind);
      return (d && d.args) || [];
    },
    async loadSchedOptions(source, force) {
      if (!source) return;
      if (!force && this.schedOptions[source] && this.schedOptions[source].loaded) return;
      try {
        const r = await this.api('/api/scheduler/options?source='+encodeURIComponent(source));
        const d = await r.json();
        this.schedOptions[source] = { groups: d.groups || [], allowCustom: d.allow_custom !== false, loaded: true };
      } catch(e){ this.schedOptions[source] = { groups: [], allowCustom: true, loaded: true }; }
      this.schedReconcileCustom();
    },
    schedArgOptions(source) {
      const o = this.schedOptions[source];
      return (o && o.groups) || [];
    },
    schedValueInSource(source, value) {
      const o = this.schedOptions[source];
      if (!o) return false;
      return (o.groups||[]).some(g => (g.options||[]).some(opt => opt.value === value));
    },
    ensureSchedOptions(kind) {
      this.schedArgsOf(kind).forEach(a => { if (a.type === 'select') this.loadSchedOptions(a.source); });
    },
    schedReconcileCustom() {
      this.schedArgsOf(this.schedForm.j.kind).forEach(a => {
        if (a.type !== 'select') return;
        const v = this.schedForm.args[a.name];
        const o = this.schedOptions[a.source];
        if (v && o && o.loaded && !this.schedValueInSource(a.source, v)) this.schedCustom[a.name] = true;
      });
    },
    schedSelectChanged(a) {
      if (this.schedForm.args[a.name] === '__custom__') {
        this.schedCustom[a.name] = true;
        this.schedForm.args[a.name] = '';
      }
    },
    schedWeekdayNames: ['sun','mon','tue','wed','thu','fri','sat'],
    _schedTimeMH(t) {
      const parts = (t || '03:00').split(':');
      const h = Math.min(23, Math.max(0, parseInt(parts[0],10) || 0));
      const m = Math.min(59, Math.max(0, parseInt(parts[1],10) || 0));
      return { h, m };
    },
    schedCompileCron(s) {
      s = s || this.schedForm.sched;
      const { h, m } = this._schedTimeMH(s.time);
      if (s.mode === 'every') {
        const n = Math.max(1, parseInt(s.everyN,10) || 1);
        return s.everyUnit === 'hours' ? `0 */${Math.min(23,n)} * * *` : `*/${Math.min(59,n)} * * * *`;
      }
      if (s.mode === 'daily')  return `${m} ${h} * * *`;
      if (s.mode === 'weekly') {
        const days = (s.weekdays||[]).slice().sort((a,b)=>a-b);
        return days.length ? `${m} ${h} * * ${days.join(',')}` : `${m} ${h} * * *`;
      }
      if (s.mode === 'monthly') {
        const d = Math.min(31, Math.max(1, parseInt(s.dom,10) || 1));
        return `${m} ${h} ${d} * *`;
      }
      return this.schedForm.j.schedule;
    },
    schedSyncCron() {
      if (this.schedForm.sched && this.schedForm.sched.mode !== 'cron') {
        this.schedForm.j.schedule = this.schedCompileCron();
      }
      this.schedPreview();
    },
    schedToggleWeekday(d) {
      const wd = this.schedForm.sched.weekdays;
      const i = wd.indexOf(d);
      if (i >= 0) wd.splice(i,1); else wd.push(d);
      this.schedSyncCron();
    },
    schedHumanize(expr) {
      const p = (expr||'').trim().split(/\s+/);
      if (p.length !== 5) return expr || '';
      const [mi,h,dom,mon,dow] = p;
      const num = x => /^\d+$/.test(x);
      const pad = n => String(n).padStart(2,'0');
      const hhmm = (H,M) => pad(H)+':'+pad(M);
      let mm;
      if ((mm = mi.match(/^\*\/(\d+)$/)) && h==='*' && dom==='*' && mon==='*' && dow==='*') return `every ${mm[1]} min`;
      if (mi==='0' && (mm = h.match(/^\*\/(\d+)$/)) && dom==='*' && mon==='*' && dow==='*') return `every ${mm[1]} h`;
      if (num(mi) && num(h) && mon==='*') {
        const t = hhmm(+h,+mi);
        if (dom==='*' && dow==='*') return `every day at ${t}`;
        if (dom==='*' && /^[0-6](,[0-6])*$/.test(dow)) {
          const ds = dow.split(',').map(d=>this.schedWeekdayNames[+d]).join(', ');
          return `${ds} at ${t}`;
        }
        if (num(dom) && dow==='*') return `day ${dom} of every month at ${t}`;
      }
      return expr;
    },
    schedParseToBuilder(expr) {
      const def = { mode:'cron', everyN:15, everyUnit:'minutes', time:'03:00', weekdays:[1], dom:1 };
      const p = (expr||'').trim().split(/\s+/);
      if (p.length !== 5) return def;
      const [mi,h,dom,mon,dow] = p;
      const num = x => /^\d+$/.test(x);
      const pad = n => String(n).padStart(2,'0');
      let mm;
      if ((mm = mi.match(/^\*\/(\d+)$/)) && h==='*' && dom==='*' && mon==='*' && dow==='*') return { ...def, mode:'every', everyN:+mm[1], everyUnit:'minutes' };
      if (mi==='0' && (mm = h.match(/^\*\/(\d+)$/)) && dom==='*' && mon==='*' && dow==='*') return { ...def, mode:'every', everyN:+mm[1], everyUnit:'hours' };
      if (num(mi) && num(h) && mon==='*') {
        const time = pad(+h)+':'+pad(+mi);
        if (dom==='*' && dow==='*') return { ...def, mode:'daily', time };
        if (dom==='*' && /^[0-6](,[0-6])*$/.test(dow)) return { ...def, mode:'weekly', time, weekdays:dow.split(',').map(Number) };
        if (num(dom) && dow==='*') return { ...def, mode:'monthly', time, dom:+dom };
      }
      return def;
    },
    async loadSchedNotifyChannels() {
      try {
        const r = await this.api('/api/notify/channels');
        const d = await r.json();
        this.schedNotifyChannels = (d.channels||[]).filter(c => c.enabled !== false);
        this._loadOk('schedNotifyChannels');
      } catch(e){ this.schedNotifyChannels = []; this._loadErr('schedNotifyChannels', e, 'failed to load notification channels'); }
    },
    schedToggleNotifyChannel(id) {
      const arr = this.schedForm.j.notify_channels || (this.schedForm.j.notify_channels = []);
      const i = arr.indexOf(id);
      if (i >= 0) arr.splice(i,1); else arr.push(id);
    },
    schedChannelIcon(type) {
      return ({ whatsapp:'🟢', telegram:'✈️', email:'✉️', webhook:'🪝', inapp:'🔔' })[type] || '🔔';
    },
    async loadSchedRemotes() {
      try {
        const r = await this.api('/api/backup/remotes');
        const d = await r.json();
        this.schedRemotes = { installed: !!d.installed, list: d.remotes || [], loaded: true };
        this.schedRemotesError = '';
      } catch(e){
        this.schedRemotes = { installed:false, list:[], loaded:false };
        this.schedRemotesError = (e && e.message === 'unauthorized') ? '' : ((e && e.message) || 'failed to load backup remotes');
      }
    },
    fsOpen(mode, startPath, onPick, remote) {
      this.fsBrowser = {
        open:true, mode, remote: remote||'', path: startPath || (mode==='remote' ? '' : '/'),
        parent:'', dirs:[], loading:false, error:'', newFolder:'', _onPick: onPick || null,
      };
      this.fsLoad();
    },
    async fsLoad() {
      this.fsBrowser.loading = true; this.fsBrowser.error = '';
      try {
        let url;
        if (this.fsBrowser.mode === 'remote') {
          url = '/api/backup/remote-browse?remote='+encodeURIComponent(this.fsBrowser.remote)+'&path='+encodeURIComponent(this.fsBrowser.path||'');
        } else {
          url = '/api/fs/browse?path='+encodeURIComponent(this.fsBrowser.path||'/');
        }
        const r = await this.api(url);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.fsBrowser.dirs = d.dirs || [];
        this.fsBrowser.path = d.path != null ? d.path : this.fsBrowser.path;
        this.fsBrowser.parent = d.parent || '';
      } catch(e){ this.fsBrowser.error = e.message; this.fsBrowser.dirs = []; }
      finally { this.fsBrowser.loading = false; }
    },
    fsEnter(dir) { this.fsBrowser.path = dir.path; this.fsLoad(); },
    fsUp() {
      if (this.fsBrowser.mode === 'remote') {
        const p = (this.fsBrowser.path||'').replace(/\/+$/,'');
        this.fsBrowser.path = p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '';
      } else {
        this.fsBrowser.path = this.fsBrowser.parent || '/';
      }
      this.fsLoad();
    },
    fsPick() {
      if (typeof this.fsBrowser._onPick === 'function') this.fsBrowser._onPick(this.fsBrowser.path);
      this.fsBrowser.open = false;
    },
    fsClose() { this.fsBrowser.open = false; },
    fsPickLocalInto(field) {
      const cur = (field === '__destLocal') ? this.schedForm.dest.localPath : (this.schedForm.args[field]||'');
      this.fsOpen('local', cur || '/', (p) => {
        if (field === '__destLocal') this.schedForm.dest.localPath = p; else this.schedForm.args[field] = p;
      });
    },
    fsPickRemoteFolder() {
      if (!this.schedForm.dest.remote) { this.showToast('choose a remote first','err'); return; }
      this.fsOpen('remote', this.schedForm.dest.remotePath || '', (p)=>{ this.schedForm.dest.remotePath = p; }, this.schedForm.dest.remote);
    },
    schedRcloneTypes:[
      { v:'sftp', label:'SFTP (another server over SSH) — no browser' },
      { v:'webdav', label:'WebDAV (Nextcloud/ownCloud…) — no browser' },
      { v:'ftp', label:'FTP — no browser' },
      { v:'s3', label:'S3 compatible (key/secret) — no browser' },
      { v:'b2', label:'Backblaze B2' },
      { v:'drive', label:'Google Drive (login)' }, { v:'onedrive', label:'OneDrive (login)' },
      { v:'dropbox', label:'Dropbox (login)' }, { v:'box', label:'Box (login)' },
      { v:'pcloud', label:'pCloud (login)' }, { v:'yandex', label:'Yandex Disk (login)' },
    ],
    rcloneMode(t) {
      if (t === 's3') return 's3';
      if (t === 'sftp') return 'sftp';
      if (t === 'ftp') return 'ftp';
      if (t === 'webdav') return 'webdav';
      return 'oauth';
    },
    openRcloneConnect() {
      this.rcloneConnect = { open:true, name:'', type:'sftp', token:'', accessKey:'', secret:'', region:'', endpoint:'', provider:'', host:'', user:'', pass:'', port:'', url:'', keyFile:'', busy:false, error:'', authId:'', authUrl:'', authBusy:false };
    },
    async startRcloneAuthorize() {
      const c = this.rcloneConnect;
      if (!c.name.trim()) { c.error = 'name the remote first'; return; }
      c.error = ''; c.authBusy = true; c.authUrl = '';
      try {
        const r = await this.api('/api/backup/remote-authorize', { method:'POST', body: JSON.stringify({ type:c.type }) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        c.authId = d.id;
        this._pollRcloneAuth();
      } catch(e){ c.error = e.message; c.authBusy = false; }
    },
    async _pollRcloneAuth() {
      const c = this.rcloneConnect;
      if (!c.open || !c.authId) return;
      try {
        const r = await this.api('/api/backup/remote-authorize/status?id='+encodeURIComponent(c.authId));
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        if (d.url) c.authUrl = d.url;
        if (d.error) { c.error = d.error; c.authBusy = false; c.authId=''; return; }
        if (d.ready) {
          const cr = await this.api('/api/backup/remote-connect', { method:'POST', body: JSON.stringify({ name:c.name.trim(), type:c.type, session_id:c.authId }) });
          const cd = await cr.json();
          if (!cr.ok) throw new Error(cd.error || ('HTTP '+cr.status));
          this.showToast('cloud connected: '+cd.name, 'ok');
          c.open = false; c.authBusy = false; c.authId = '';
          await this.loadSchedRemotes();
          this.schedForm.dest.type = 'rclone'; this.schedForm.dest.remote = cd.name;
          return;
        }
        setTimeout(() => this._pollRcloneAuth(), 2500);
      } catch(e){ c.error = e.message; c.authBusy = false; c.authId=''; }
    },
    async doRcloneConnect() {
      const c = this.rcloneConnect;
      if (!c.name.trim()) { c.error = 'name the remote'; return; }
      c.busy = true; c.error = '';
      try {
        const r = await this.api('/api/backup/remote-connect', { method:'POST', body: JSON.stringify({
          name:c.name.trim(), type:c.type, token:c.token,
          access_key:c.accessKey, secret:c.secret, region:c.region, endpoint:c.endpoint, provider:c.provider,
          host:c.host, user:c.user, pass:c.pass, port:c.port, url:c.url, key_file:c.keyFile,
        })});
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.showToast('cloud connected: '+d.name, 'ok');
        c.open = false;
        await this.loadSchedRemotes();
        this.schedForm.dest.type = 'rclone';
        this.schedForm.dest.remote = d.name;
      } catch(e){ c.error = e.message; }
      finally { c.busy = false; }
    },
    schedArgDefaults(desc) {
      const out = {};
      ((desc && desc.args) || []).forEach(a => {
        out[a.name] = (a.type === 'enum' && (a.options||[]).length) ? a.options[0] : '';
      });
      return out;
    },
    selectSchedKind(kind) {
      this.schedForm.j.kind = kind;
      this.schedForm.args = this.schedArgDefaults(this.schedDescriptor(kind));
      this.schedCustom = {};
      this.ensureSchedOptions(kind);
    },
    selectThenKind(kind) {
      this.schedForm.j.then_kind = kind;
      this.schedForm.thenArgs = kind ? this.schedArgDefaults(this.schedDescriptor(kind)) : {};
      this.schedThenCustom = {};
      if (kind) this.ensureSchedOptions(kind);
    },
    schedThenSelectChanged(a) {
      if (this.schedForm.thenArgs[a.name] === '__custom__') {
        this.schedThenCustom[a.name] = true;
        this.schedForm.thenArgs[a.name] = '';
      }
    },
    openSchedForm(kind) {
      const first = (this.schedCatalog[0] && this.schedCatalog[0].kind) || '';
      const k = kind || first;
      this.schedForm = {
        open:true,
        j:{ id:'', name:'', schedule:'0 3 * * *', kind:k, enabled:true, run_as_root:false, alert_on:'fail', then_kind:'', then_on:'success', notify_channels:[], notify_on:'success' },
        args: this.schedArgDefaults(this.schedDescriptor(k)),
        thenArgs:{},
        sched: this.schedParseToBuilder('0 3 * * *'),
        dest:{ type:'local', localPath:'', remote:'', remotePath:'' },
        preview:[], previewError:'',
      };
      this.schedCustom = {}; this.schedThenCustom = {};
      this.ensureSchedOptions(k);
      this.schedPreview();
    },
    editSchedJob(j) {
      const desc = this.schedDescriptor(j.kind);
      const args = this.schedArgDefaults(desc);
      try {
        const a = j.args ? (typeof j.args === 'string' ? JSON.parse(j.args) : j.args) : {};
        ((desc && desc.args) || []).forEach(arg => {
          if (a[arg.name] === undefined) return;
          args[arg.name] = arg.type === 'string_list' ? (a[arg.name]||[]).join('\n') : a[arg.name];
        });
      } catch {}
      let dest = { type:'local', localPath:'', remote:'', remotePath:'' };
      if (j.kind === 'backup_now') {
        try {
          const a = j.args ? (typeof j.args === 'string' ? JSON.parse(j.args) : j.args) : {};
          if (a.dest_type === 'rclone') dest = { type:'rclone', localPath:'', remote:a.remote||'', remotePath:a.remote_path||'' };
          else dest = { type:'local', localPath:a.dest||'', remote:'', remotePath:'' };
        } catch {}
      }
      let thenArgs = {};
      if (j.then_kind) {
        const td = this.schedArgDefaults(this.schedDescriptor(j.then_kind));
        try {
          const ta = j.then_args ? (typeof j.then_args === 'string' ? JSON.parse(j.then_args) : j.then_args) : {};
          ((this.schedDescriptor(j.then_kind)||{}).args || []).forEach(arg => {
            if (ta[arg.name] === undefined) return;
            td[arg.name] = arg.type === 'string_list' ? (ta[arg.name]||[]).join('\n') : ta[arg.name];
          });
        } catch {}
        thenArgs = td;
      }
      this.schedForm = {
        open:true,
        j:{ id:j.id, name:j.name, schedule:j.schedule, kind:j.kind, enabled:j.enabled, run_as_root:!!j.run_as_root, alert_on:j.alert_on||'fail', then_kind:j.then_kind||'', then_on:j.then_on||'success', notify_channels:Array.isArray(j.notify_channels)?[...j.notify_channels]:[], notify_on:j.notify_on||'success' },
        args,
        thenArgs,
        sched: this.schedParseToBuilder(j.schedule),
        dest,
        preview:[], previewError:'',
      };
      this.schedCustom = {}; this.schedThenCustom = {};
      if (j.then_kind) this.ensureSchedOptions(j.then_kind);
      this.ensureSchedOptions(j.kind);
      this.schedPreview();
    },
    async schedPreview() {
      const expr = this.schedForm.j.schedule;
      if (!expr) { this.schedForm.preview = []; return; }
      try {
        const r = await this.api('/api/scheduler/preview?expr='+encodeURIComponent(expr)+'&n=5');
        const d = await r.json();
        if (!r.ok) { this.schedForm.previewError = d.error || ('HTTP '+r.status); this.schedForm.preview = []; return; }
        this.schedForm.preview = d.fires || [];
        this.schedForm.previewError = '';
      } catch(e){ this.schedForm.previewError = e.message; }
    },
    schedBuildArgs(kind, argsObj) {
      const desc = this.schedDescriptor(kind);
      const out = {};
      ((desc && desc.args) || []).forEach(a => {
        const v = (argsObj || {})[a.name];
        if (a.type === 'string_list') {
          out[a.name] = (v||'').split('\n').map(s=>s.trim()).filter(s=>s.length>0);
        } else if (a.type === 'number') {
          out[a.name] = (v === '' || v == null) ? 0 : (parseInt(v, 10) || 0);
        } else {
          out[a.name] = v == null ? '' : v;
        }
      });
      return out;
    },
    schedFormArgs() {
      const out = this.schedBuildArgs(this.schedForm.j.kind, this.schedForm.args);
      if (this.schedForm.j.kind === 'backup_now') {
        const dst = this.schedForm.dest || {};
        out.dest_type = dst.type || 'local';
        if (out.dest_type === 'rclone') {
          out.remote = dst.remote || '';
          out.remote_path = dst.remotePath || '';
        } else {
          out.dest = dst.localPath || '';
        }
      }
      return out;
    },
    schedFormMissing() {
      const desc = this.schedDescriptor(this.schedForm.j.kind);
      const miss = [];
      if (!this.schedForm.j.name.trim()) miss.push('name');
      if (!this.schedForm.j.schedule.trim()) miss.push('cron');
      if (!this.schedForm.j.kind) miss.push('type');
      ((desc && desc.args) || []).forEach(a => {
        if (!a.required) return;
        const v = this.schedForm.args[a.name];
        const empty = a.type === 'string_list'
          ? !((v||'').split('\n').some(s=>s.trim().length>0))
          : !(v && String(v).trim().length>0);
        if (empty) miss.push(a.label || a.name);
      });
      if (this.schedForm.j.kind === 'backup_now' && this.schedForm.dest && this.schedForm.dest.type === 'rclone' && !this.schedForm.dest.remote) {
        miss.push('remote (cloud)');
      }
      return miss;
    },
    schedFormValid() { return this.schedFormMissing().length === 0; },
    async saveSchedJob() {
      if (this.schedSaveBusy) return;
      const miss = this.schedFormMissing();
      if (miss.length) { this.showToast('fill in: '+miss.join(', '), 'err'); return; }
      this.schedSaveBusy = true;
      const body = { ...this.schedForm.j, args: this.schedFormArgs() };
      if (body.then_kind) { body.then_args = this.schedBuildArgs(body.then_kind, this.schedForm.thenArgs); }
      else { body.then_args = null; body.then_on = ''; }
      try {
        const isEdit = !!body.id;
        const url = isEdit ? '/api/scheduler/jobs/'+body.id : '/api/scheduler/jobs';
        const method = isEdit ? 'PUT' : 'POST';
        const r = await this.api(url, { method, body: JSON.stringify(body) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.schedForm.open = false;
        this.showToast(isEdit?'schedule updated':'schedule created','ok');
        await this.loadSchedJobs();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
      finally { this.schedSaveBusy = false; }
    },
    async openSchedHistory(j) {
      this.schedHist = { open:true, name:j.name, runs:[], loading:true, error:'' };
      try {
        const r = await this.api('/api/scheduler/jobs/'+j.id+'/history');
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.schedHist.runs = d.runs || [];
      } catch(e){ this.schedHist.error = e.message; }
      finally { this.schedHist.loading = false; }
    },
    schedRunStatusClass(s) {
      return s==='done' ? 'is-normal' : (s==='failed' ? 'is-firing' : (s==='running'||s==='queued' ? 'is-pending' : 'is-nodata'));
    },
    schedRunSourceLabel(src) {
      if ((src||'').startsWith('scheduler-manual:')) return 'manual';
      if ((src||'').startsWith('scheduler-chain:')) return 'chained';
      return 'scheduled';
    },
    schedOpenRunLog(run) {
      this.schedHist.open = false;
      this.openJobLog({ id: run.id, status: run.status, progress: 0, step: '' });
    },
    openSchedLastLog(j) {
      if (!j.last_job_id) { this.showToast('that schedule has not fired yet','err'); return; }
      this.openJobLog({ id: j.last_job_id, status: j.last_status || '', progress: 0, step: '' });
    },
    async runSchedNow(j) {
      try {
        const r = await this.api('/api/scheduler/jobs/'+j.id+'/run-now', { method:'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.showToast('fired → job '+d.queue_job_id, 'ok');
        await this.loadSchedJobs(); await this.loadJobs();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async deleteSchedJob(j) {
      if (!(await this.confirmAsync('Delete schedule "'+j.name+'"?'))) return;
      try {
        const r = await this.api('/api/scheduler/jobs/'+j.id, { method:'DELETE' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        await this.loadSchedJobs();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async toggleSchedJob(j) {
      try {
        const r = await this.api('/api/scheduler/jobs/'+j.id, {
          method:'PUT', body: JSON.stringify({ ...j, enabled: !j.enabled }) });
        if (!r.ok) { const d=await r.json().catch(()=>({})); throw new Error(d.error || ('HTTP '+r.status)); }
        this.showToast(j.enabled?'schedule paused':'schedule resumed', 'ok');
        await this.loadSchedJobs();
      } catch(e){ this.showToast('error: '+e.message, 'err'); }
    },
    jiraDefaultJQL() {
      if (this.jiraConfig.board_jql) return this.jiraConfig.board_jql;
      if (this.jiraConfig.project_key) return 'project = '+this.jiraConfig.project_key+' AND assignee = currentUser() ORDER BY status, updated DESC';
      return 'assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC';
    },
    async loadJiraConfig() {
      try {
        const r = await this.api('/api/jira/config');
        this.jiraConfig = (await r.json()) || this.jiraConfig;
      } catch(e){ console.warn('[jira] config:', e); }
    },
    async loadJiraHealth() {
      try {
        const r = await this.api('/api/jira/health');
        this.jiraHealth = (await r.json()) || { ok:false };
      } catch(e){ this.jiraHealth = { ok:false, error:String(e) }; }
    },
    async loadJiraProjects() {
      try {
        const r = await this.api('/api/jira/projects');
        const d = await r.json();
        this.jiraProjects = d.projects || [];
      } catch(e){ console.warn('[jira] projects:', e); }
    },
    async loadJiraIssueTypes() {
      if (!this.jiraCreate.project_key) { this.jiraIssueTypes = []; return; }
      try {
        const r = await this.api('/api/jira/issuetypes?project='+encodeURIComponent(this.jiraCreate.project_key));
        const d = await r.json();
        this.jiraIssueTypes = d.issue_types || [];
        if (this.jiraIssueTypes.length && !this.jiraIssueTypes.find(t=>t.name===this.jiraCreate.issue_type)) {
          this.jiraCreate.issue_type = this.jiraIssueTypes[0].name;
        }
      } catch(e){ console.warn('[jira] issuetypes:', e); }
    },
    async loadJiraCreateEpics(projectKey) {
      projectKey = projectKey || '';
      if (!projectKey) { this.jiraCreateEpics = []; return; }
      try {
        const r = await this.api('/api/jira/project/'+encodeURIComponent(projectKey)+'/epics');
        const d = await r.json();
        this.jiraCreateEpics = (d && d.epics) || [];
      } catch(e){ console.warn('[jira] create-epics:', e); this.jiraCreateEpics = []; }
    },
    searchJiraCreateParent(q) {
      q = (q || '').trim();
      this.jiraCreateParent.query = q;
      if (this._jiraParentTimer) clearTimeout(this._jiraParentTimer);
      if (q.length < 1) { this.jiraCreateParent.results = []; return; }
      this._jiraParentTimer = setTimeout(async () => {
        const proj = this.jiraCreate.project_key || '';
        this.jiraCreateParent.loading = true;
        try {
          const url = '/api/jira/picker?query='+encodeURIComponent(q)
                    + (proj ? '&currentJQL='+encodeURIComponent('project='+proj) : '');
          const r = await this.api(url);
          const d = await r.json();
          let list = (d && d.issues) || [];
          if (proj) list = list.filter(i => (i.key||'').startsWith(proj+'-'));
          this.jiraCreateParent.results = list;
        } catch(e){ console.warn('[jira] parent-picker:', e); this.jiraCreateParent.results = []; }
        finally { this.jiraCreateParent.loading = false; }
      }, 250);
    },
    pickJiraCreateParent(iss) {
      this.jiraCreate.parent_key = iss.key;
      this.jiraCreateParent.name = iss.key + (iss.summary ? ' · ' + iss.summary : '');
      this.jiraCreateParent.query = this.jiraCreateParent.name;
      this.jiraCreateParent.results = [];
    },
    jiraCreateTypeMeta() {
      return (this.jiraIssueTypes||[]).find(t => t.name === this.jiraCreate.issue_type) || {};
    },
    async loadJiraAssignableUsers(projectKey, q) {
      projectKey = projectKey || '';
      q = (q || '').trim();
      if (!projectKey) { this.jiraAssignableUsers = []; return; }
      if (this.jiraAssignableProject === projectKey && this.jiraAssignableQuery === q && this.jiraAssignableUsers.length > 0) return;
      this.jiraAssignableLoading = true;
      try {
        const url = '/api/jira/users?project='+encodeURIComponent(projectKey)
                  + (q ? '&q='+encodeURIComponent(q) : '');
        const r = await this.api(url);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.jiraAssignableUsers = d.users || [];
        this.jiraAssignableProject = projectKey;
        this.jiraAssignableQuery = q;
      } catch(e){
        console.warn('[jira] users:', e);
        this.jiraAssignableUsers = [];
      } finally {
        this.jiraAssignableLoading = false;
      }
    },
    async loadJiraBoard() {
      if (!this.jiraConfig.has_token) return;
      try {
        const url = '/api/jira/board' + (this.jiraJQL ? '?jql='+encodeURIComponent(this.jiraJQL) : '');
        const r = await this.api(url);
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.jiraIssues = d.issues || [];
      } catch(e){
        this.showToast('Jira: '+e.message, 'err');
        this.jiraIssues = [];
      }
    },
    jiraIssuesByCat(catKey) {
      return (this.jiraIssues||[]).filter(i => i.status?.statusCategory?.key === catKey);
    },
    jiraColumns() {
      const raw = (this.jiraConfig.board_columns||'').trim();
      let cols;
      if (raw) {
        try {
          const arr = JSON.parse(raw);
          if (Array.isArray(arr) && arr.length > 0) {
            const palette = ['border-gray-500/40','border-cyan-500/40','border-yellow-500/40','border-green-500/40','border-purple-500/40','border-orange-500/40'];
            cols = arr.map((c, i) => ({
              label: c.label || 'Col '+(i+1),
              statusNames: (c.status_names || []).map(s => s.toLowerCase()),
              statusNamesRaw: c.status_names || [],
              color: c.color || palette[i % palette.length],
              kind: 'name',
            }));
          }
        } catch(e){ console.warn('[jira] board_columns parse:', e); }
      }
      if (!cols) {
        cols = [
          { label:'To Do',       statusNames:[], color:'border-gray-500/40',  kind:'cat', catKey:'new' },
          { label:'In Progress', statusNames:[], color:'border-cyan-500/40',  kind:'cat', catKey:'indeterminate' },
          { label:'Done',        statusNames:[], color:'border-green-500/40', kind:'cat', catKey:'done' },
        ];
      }
      if (cols[0].kind === 'name') {
        const known = new Set();
        cols.forEach(c => c.statusNames.forEach(s => known.add(s)));
        const orphans = (this.jiraIssues||[]).filter(i => !known.has((i.status?.name||'').toLowerCase()));
        if (orphans.length > 0) {
          cols.push({ label:'Other', statusNames:[], color:'border-red-500/40', kind:'orphan', knownSet: known });
        }
      }
      return cols;
    },
    jiraIssuesInCol(col) {
      let issues;
      if (col.kind === 'cat') {
        issues = this.jiraIssuesByCat(col.catKey);
      } else if (col.kind === 'orphan') {
        const known = col.knownSet;
        issues = (this.jiraIssues||[]).filter(i => !known.has((i.status?.name||'').toLowerCase()));
      } else {
        issues = (this.jiraIssues||[]).filter(i => col.statusNames.includes((i.status?.name||'').toLowerCase()));
      }
      return this.jiraSortIssues(issues.filter(i => this.jiraIssueVisible(i)), col.label);
    },
    jiraIssueJob(key) {
      let best = null;
      for (const j of (this.jobs || [])) {
        if (j.kind !== 'jira_ai_analysis') continue;
        if (j.owner !== this.username) continue;
        if (!j.args || j.args.issue_key !== key) continue;
        if (j.status !== 'running' && j.status !== 'queued') continue;
        if (!best) { best = j; continue; }
        const rank = s => s === 'running' ? 2 : 1;
        if (rank(j.status) > rank(best.status) ||
            (j.status === best.status && (j.started || 0) > (best.started || 0))) best = j;
      }
      return best;
    },
    boardJobMedian() { return 570; },
    boardJobElapsed(j) {
      return j && j.started ? Math.max(0, Math.floor(Date.now()/1000) - j.started) : 0;
    },
    boardJobProgress(j) {
      if (!j || j.status === 'queued') return 0;
      const t = Math.min(95, Math.round(this.boardJobElapsed(j) / this.boardJobMedian() * 100));
      return Math.max(j.progress || 0, t);
    },
    boardJobEta(j) {
      if (!j || j.status !== 'running') return null;
      const r = this.boardJobMedian() - this.boardJobElapsed(j);
      return r > 30 ? '~' + Math.ceil(r/60) + 'min' : 'finishing…';
    },
    boardJobStep(j) { return j && j.step ? j.step : ''; },
    statusToColLabel(statusName, statusCatKey) {
      if (!statusName && !statusCatKey) return '';
      const lc = (statusName||'').toLowerCase();
      for (const col of this.jiraColumns()) {
        if (col.kind === 'cat') {
          if (statusCatKey && col.catKey === statusCatKey) return col.label;
        } else if (col.kind === 'name') {
          if (col.statusNames.includes(lc)) return col.label;
        }
      }
      return statusName || '';
    },
    transitionToColLabel(tr) { return this.statusToColLabel(tr?.to_name, tr?.to_cat); },
    async jiraInit() {
      await this.loadJiraConfig();
      if (!this.jiraConfig.has_token) return;
      await this.loadJiraHealth();
      if (this.jiraHealth.ok) {
        await this.loadJiraProjects();
        if (!this.jiraProjectKey) {
          this.jiraProjectKey = this.jiraConfig.project_key || (this.jiraProjects[0]?.key || '');
        }
        this.applyJiraFilter();
        this.loadJiraLinkTypes();
      }
    },
    async saveJiraSetup() {
      this.jiraSetupError = '';
      const s = this.jiraSetup;
      if (!s.site || !s.email || !s.token) { this.jiraSetupError = 'site + email + token are required'; return; }
      try {
        const r = await this.api('/api/jira/config', { method:'POST', body: JSON.stringify(s) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        await this.loadJiraConfig();
        await this.loadJiraHealth();
        if (!this.jiraHealth.ok) {
          this.jiraSetupError = 'Credentials saved, but Jira refused: '+(this.jiraHealth.error||'?');
          return;
        }
        this.showToast('Jira connected','ok');
        this.jiraSetup = { site:'', email:'', token:'', project_key:'' };
        await this.loadJiraProjects();
        await this.loadJiraBoard();
      } catch(e){ this.jiraSetupError = e.message; }
    },
    async openJiraSettings() {
      await this.loadJiraConfig();
      this.jiraSettings = {
        open:true,
        site: this.jiraConfig.site,
        email: this.jiraConfig.email,
        token: '',
        project_key: this.jiraConfig.project_key,
        board_jql: this.jiraConfig.board_jql,
        board_columns: this.jiraConfig.board_columns,
      };
      if (this.jiraProjects.length === 0) this.loadJiraProjects();
    },
    async saveJiraSettings() {
      try {
        const r = await this.api('/api/jira/config', { method:'POST', body: JSON.stringify(this.jiraSettings) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast('config saved','ok');
        this.jiraSettings.open = false;
        await this.loadJiraConfig();
        await this.loadJiraHealth();
        await this.loadJiraBoard();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async disconnectJira() {
      if (!(await this.confirmAsync('Delete the Jira credentials of this user?'))) return;
      try {
        const r = await this.api('/api/jira/config', { method:'DELETE' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraConfig = { site:'', email:'', project_key:'', board_jql:'', has_token:false };
        this.jiraHealth = { ok:false };
        this.jiraIssues = [];
        this.jiraSettings.open = false;
        this.showToast('Jira credentials deleted','ok');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    openJiraCreate() {
      const defProject = this.jiraConfig.project_key || (this.jiraProjects[0]?.key || '');
      const base = {
        open:true,
        project_key: defProject,
        issue_type:'Task', summary:'', description:'', priority:'', due_date:'', labels_str:'',
        assignee_id:'', assignee_name:'',
        parent_key:'', files:[],
      };
      let restored = false;
      try {
        const raw = localStorage.getItem('panel_jira_draft');
        if (raw) {
          const d = JSON.parse(raw);
          if (d && typeof d === 'object') {
            const { files:_dropFiles, ...d2 } = d;
            Object.assign(base, d2, { open:true, files:[] });
            if (!base.project_key) base.project_key = defProject;
            restored = !!(d.summary || d.description || d.labels_str);
          }
        }
      } catch(_){}
      this.jiraCreate = base;
      this.jiraCreateParent = { query:'', results:[], loading:false, name:'' };
      this.loadJiraIssueTypes();
      this.loadJiraAssignableUsers(base.project_key, '');
      this.loadJiraCreateEpics(base.project_key);
      if (restored) this.showToast('draft restored','ok');
    },
    saveJiraDraft() {
      try {
        const { open, files, ...draft } = this.jiraCreate;
        localStorage.setItem('panel_jira_draft', JSON.stringify(draft));
      } catch(_){}
    },
    cancelJiraCreate() {
      this.jiraCreate.open = false;
    },
    async submitJiraCreate() {
      const c = this.jiraCreate;
      if (!c.project_key || !c.summary || !c.issue_type) { this.showToast('project + summary + type are required','err'); return; }
      const it = (this.jiraIssueTypes||[]).find(t => t.name === c.issue_type);
      if (it && it.subtask && !c.parent_key) { this.showToast('a subtask requires a parent issue','err'); return; }
      const body = {
        project_key: c.project_key,
        issue_type: c.issue_type,
        summary: c.summary,
        description: c.description,
        priority: c.priority,
        due_date: c.due_date,
        labels: c.labels_str ? c.labels_str.split(',').map(s=>s.trim()).filter(Boolean) : [],
        assignee_id: c.assignee_id || '',
        parent_key: c.parent_key || '',
      };
      try {
        const r = await this.api('/api/jira/issue', { method:'POST', body: JSON.stringify(body) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.jiraCreate.open = false;
        try { localStorage.removeItem('panel_jira_draft'); } catch(_){}
        this.showToast('created: '+d.key, 'ok');

        if (Array.isArray(c.files) && c.files.length) {
          const MAX = 32 * 1024 * 1024, N = c.files.length;
          let okN = 0, lastErr = '';
          for (const f of c.files) {
            if (f.size > MAX) { lastErr = `${f.name}: too large (${this.fmtBytes(f.size)} > ${this.fmtBytes(MAX)})`; continue; }
            try {
              const fd = new FormData();
              fd.append('file', f);
              const ra = await this.api('/api/jira/issue/'+d.key+'/attachments', { method:'POST', body: fd });
              if (!ra.ok) { const da = await ra.json().catch(()=>({})); throw new Error(da.error || ('HTTP '+ra.status)); }
              okN++;
            } catch(e){ lastErr = e.message; }
          }
          this.showToast(`${d.key}: ${okN}/${N} attachments`+(okN<N && lastErr ? ' — '+lastErr : ''), okN<N ? 'warn' : 'ok');
        }

        let det = null;
        try {
          const rd = await this.api('/api/jira/issue/'+d.key);
          if (rd.ok) det = await rd.json();
        } catch(_){ /* does not silence: the error path falls into the retry/toast below */ }
        if (det && det.key) this.jiraIssues = [det, ...(this.jiraIssues||[])];

        const has = () => (this.jiraIssues||[]).some(i => i.key === d.key);
        for (let i = 0; i < 3; i++) {
          await this.loadJiraBoard();
          if (has()) break;
          if (det && det.key) { this.jiraIssues = [det, ...this.jiraIssues]; break; }
          if (['mine','review','inprogress'].includes(this.jiraFilter)) break;
          await new Promise(res => setTimeout(res, 1500));
        }
        if (!has()) this.showToast('Issue '+d.key+' created — the board can take a few seconds to catch up', 'warn');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async openJiraIssue(iss) {
      this.jiraDetail = {
        open:true, key:iss.key, issue:null, transitions:[], comments:[], newComment:'',
        editing:false, editSummary:'', editDescription:'',
        watchers:null, subtasks:[], links:[], attachments:[], worklogs:[], changelog:[],
        newLinkType:'', newLinkKey:'', newWorklog:{ time_spent:'', comment:'' },
      };
      this.jiraDetailTab = 'overview';
      try {
        const [rIss, rTr, rWith] = await Promise.all([
          this.api('/api/jira/issue/'+iss.key),
          this.api('/api/jira/issue/'+iss.key+'/transitions'),
          this.api('/api/jira/issue/'+iss.key+'/comments'),
        ]);
        this.jiraDetail.issue = await rIss.json();
        const dTr = await rTr.json();
        this.jiraDetail.transitions = dTr.transitions || [];
        const dWith = await rWith.json();
        this.jiraDetail.comments = dWith.comments || [];
        this.loadJiraWatchers();
        this.loadJiraVotes();
        this.loadJiraProjectMeta();
        const det = this.jiraDetail.issue;
        this.jiraAdvFields = {
          story_points: det?.['customfield_10016'] || det?.story_points || '',
          components: (det?.components||[]).map(c=>c.name||c),
          fix_versions: (det?.fixVersions||[]).map(v=>v.name||v),
          epic_link: det?.parent?.key || det?.['customfield_10014'] || '',
        };
      } catch(e){ this.showToast('detail error: '+e.message,'err'); }
    },
    closeJiraDetail() {
      this.jiraDetail = { open:false, key:'', issue:null, transitions:[], comments:[], newComment:'',
        editing:false, editSummary:'', editDescription:'',
        watchers:null, subtasks:[], links:[], attachments:[], worklogs:[], changelog:[],
        newLinkType:'', newLinkKey:'', newWorklog:{ time_spent:'', comment:'' } };
    },
    async confirmCloseJiraDetail() {
      if (!this.jiraDetail.open) return;
      const d = this.jiraDetail;
      const dirty = d.editing ||
        (d.newComment && d.newComment.trim()) ||
        (d.newWorklog && d.newWorklog.time_spent) ||
        (d.newLinkKey && d.newLinkKey.trim());
      if (dirty && !(await this.confirmAsync('There are unsaved edits. Discard and close?'))) return;
      this.closeJiraDetail();
    },
    async doJiraTransition(transitionId) {
      if (!transitionId) return;
      const tr = (this.jiraDetail.transitions||[]).find(t => String(t.id) === String(transitionId));
      const label = tr ? this.transitionToColLabel(tr) : '';
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/transition', { method:'POST', body: JSON.stringify({ transition_id: transitionId }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast(this.jiraDetail.key + (label ? ' → ' + label : ' moved'), 'ok');
        await this.openJiraIssue({ key: this.jiraDetail.key });
        await this.loadJiraBoard();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async postJiraComment() {
      const body = this.jiraDetail.newComment.trim();
      if (!body) return;
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/comment', { method:'POST', body: JSON.stringify({ body }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraDetail.newComment = '';
        const c = await r.json();
        this.jiraDetail.comments.push(c);
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async jiraDropOnCol(col, ev) {
      const key = this.jiraDragKey;
      this.jiraDragKey = null;
      if (!key) return;
      const iss = this.jiraIssues.find(i => i.key === key);
      if (!iss) return;
      if (col.kind === 'cat' && iss.status?.statusCategory?.key === col.catKey) return;
      if (col.kind === 'name' && col.statusNames.includes((iss.status?.name||'').toLowerCase())) return;
      try {
        const r = await this.api('/api/jira/issue/'+key+'/transitions');
        const d = await r.json();
        const trs = d.transitions || [];
        let tr = null;
        if (col.kind === 'cat') {
          tr = trs.find(t => t.to_cat === col.catKey);
        } else {
          for (const want of col.statusNames) {
            tr = trs.find(t => (t.to_name||'').toLowerCase() === want);
            if (tr) break;
          }
        }
        if (!tr) { this.showToast('no transition to "'+col.label+'"','err'); return; }
        const r2 = await this.api('/api/jira/issue/'+key+'/transition', { method:'POST', body: JSON.stringify({ transition_id: tr.id }) });
        if (!r2.ok) { const d2=await r2.json(); throw new Error(d2.error); }
        this.showToast(key+' → '+col.label,'ok');
        await this.loadJiraBoard();
      } catch(e){ this.showToast('drop error: '+e.message,'err'); }
    },

    avatarOf(u) {
      if (!u || !u.avatarUrls) return '';
      const raw = u.avatarUrls['48x48'] || u.avatarUrls['32x32'] || u.avatarUrls['24x24'] || u.avatarUrls['16x16'] || '';
      if (!raw) return '';
      if (raw.startsWith('/')) return raw;
      return '/api/jira/avatar?u=' + encodeURIComponent(raw);
    },
    initialsOf(u) {
      if (!u || !u.displayName) return '?';
      return u.displayName.split(' ').map(s=>s[0]).join('').slice(0,2).toUpperCase();
    },
    priorityCls(name) {
      switch ((name||'').toLowerCase()) {
        case 'highest': return 'text-red-400 bg-red-500/10';
        case 'high':    return 'text-orange-400 bg-orange-500/10';
        case 'medium':  return 'text-yellow-400 bg-yellow-500/10';
        case 'low':     return 'text-blue-400 bg-blue-500/10';
        case 'lowest':  return 'text-gray-400 bg-gray-500/10';
        default:        return '';
      }
    },
    statusCls(catKey) {
      switch (catKey) {
        case 'new':           return 'text-gray-400 bg-gray-500/10';
        case 'indeterminate': return 'text-cyan-400 bg-cyan-500/10';
        case 'done':          return 'text-green-400 bg-green-500/10';
        default:              return 'text-gray-400';
      }
    },
    onJiraProjectChange() {
      this.api('/api/jira/config', { method:'POST', body: JSON.stringify({ project_key: this.jiraProjectKey }) })
        .then(() => { this.jiraConfig.project_key = this.jiraProjectKey; this.applyJiraFilter(); })
        .catch((e) => { this.showToast('could not switch project: '+e.message, 'err'); });
    },
    setJiraQuickFilter(k) { this.jiraFilter = k; this.applyJiraFilter(); },
    applyJiraFilter() {
      const proj = this.jiraProjectKey || this.jiraConfig.project_key || '';
      const where = proj ? 'project = '+proj+' AND ' : '';
      let jql = '';
      switch (this.jiraFilter) {
        case 'mine':       jql = where + 'assignee = currentUser() AND statusCategory != Done ORDER BY rank ASC'; break;
        case 'todo':       jql = where + 'statusCategory = "To Do" ORDER BY rank ASC'; break;
        case 'inprogress': jql = where + 'statusCategory = "In Progress" ORDER BY updated DESC'; break;
        case 'review':     jql = where + 'status = "IN REVIEW" ORDER BY updated DESC'; break;
        case 'last7':      jql = where + 'updated >= -7d ORDER BY updated DESC'; break;
        case 'reported':   jql = where + 'reporter = currentUser() ORDER BY updated DESC'; break;
        case 'all':        jql = where + 'ORDER BY updated DESC'; jql = jql.replace('AND ORDER','ORDER'); break;
        case 'custom':     jql = this.jiraCustomJQL || ''; break;
      }
      jql = jql.replace(/AND\s+ORDER/i, 'ORDER').replace(/^\s*AND\s+/,'');
      this.jiraJQL = jql;
      this.loadJiraBoard();
    },
    jiraFilteredIssues() {
      const q = (this.jiraSearch||'').toLowerCase();
      const src = this.jiraIssues || [];
      if (!q) return src;
      return __panelMemo('jiraIssues', [src, src.length, q], () => src.filter(i => {
        const hay = ((i.key||'')+' '+(i.summary||'')+' '+((i.labels||[]).join(' '))+' '+(i.assignee?.displayName||'')+' '+(i.status?.name||'')).toLowerCase();
        return hay.includes(q);
      }));
    },
    jiraSelectedAll() {
      const ids = this.jiraFilteredIssues().map(i=>i.key);
      return ids.length > 0 && ids.every(k => this.jiraSelected.includes(k));
    },
    jiraToggleSelectAll(on) {
      const ids = this.jiraFilteredIssues().map(i=>i.key);
      this.jiraSelected = on ? Array.from(new Set([...this.jiraSelected, ...ids])) : this.jiraSelected.filter(k => !ids.includes(k));
    },
    jiraToggleSelect(key) {
      const i = this.jiraSelected.indexOf(key);
      if (i >= 0) this.jiraSelected.splice(i,1); else this.jiraSelected.push(key);
    },
    async jiraBulkApplyTransition() {
      const target = this.jiraBulkTransition;
      if (!target || this.jiraSelected.length === 0) return;
      if (!(await this.confirmAsync('Move '+this.jiraSelected.length+' issue(s) to "'+target+'"?'))) return;
      const ok = []; const fail = [];
      for (const key of [...this.jiraSelected]) {
        try {
          const r = await this.api('/api/jira/issue/'+key+'/transitions');
          const d = await r.json();
          const tr = (d.transitions||[]).find(t => t.to_cat === target);
          if (!tr) { fail.push(key+' (no transition to '+target+')'); continue; }
          const r2 = await this.api('/api/jira/issue/'+key+'/transition', { method:'POST', body: JSON.stringify({ transition_id: tr.id }) });
          if (!r2.ok) { const d2 = await r2.json().catch(()=>({})); throw new Error(d2.error||('HTTP '+r2.status)); }
          ok.push(key);
        } catch(e){ fail.push(key+' ('+e.message+')'); }
      }
      this.showToast(`${ok.length} moved, ${fail.length} failed`, fail.length?'err':'ok');
      if (fail.length) console.warn('[bulk transition] failures:', fail);
      this.jiraSelected = [];
      this.jiraBulkTransition = '';
      await this.loadJiraBoard();
    },
    async jiraBulkAssignMe() {
      const accId = this.jiraHealth.me?.accountId;
      if (!accId) { this.showToast('no account_id','err'); return; }
      if (!(await this.confirmAsync('Assign '+this.jiraSelected.length+' issue(s) to you?'))) return;
      const ok = []; const fail = [];
      for (const key of [...this.jiraSelected]) {
        try {
          const r = await this.api('/api/jira/issue/'+key, { method:'PATCH', body: JSON.stringify({ assignee_id: accId }) });
          if (!r.ok) { const d = await r.json().catch(()=>({})); throw new Error(d.error||('HTTP '+r.status)); }
          ok.push(key);
        } catch(e){ fail.push(key+' ('+e.message+')'); }
      }
      this.showToast(`${ok.length} assigned, ${fail.length} failed`, fail.length?'err':'ok');
      if (fail.length) console.warn('[bulk assign] failures:', fail);
      this.jiraSelected = [];
      await this.loadJiraBoard();
    },
    async updateJiraIssue(patch) {
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key, { method:'PATCH', body: JSON.stringify(patch) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast('updated','ok');
        await this.refreshJiraIssue();
        await this.loadJiraBoard();
      } catch(e){
        this.showToast('error: '+this.jiraTypeFriendlyError(e.message),'err');
        try { await this.refreshJiraIssue(); } catch(_){}
      }
    },
    jiraTypeFriendlyError(msg){
      const m = (msg||'').toLowerCase();
      if (m.includes('issuetype') || m.includes('issue type') || m.includes('is not valid for this'))
        return "this Jira does not allow switching to that type from here — use 'Move' in Jira";
      return msg || 'failed';
    },
    assignToMe() {
      const accId = this.jiraHealth.me?.accountId;
      if (!accId) { this.showToast('no account_id','err'); return; }
      this.updateJiraIssue({ assignee_id: accId });
    },
    async addJiraLabel(label) {
      label = (label||'').trim();
      if (!label) return;
      const cur = this.jiraDetail.issue?.labels || [];
      if (cur.includes(label)) return;
      await this.updateJiraIssue({ labels: [...cur, label] });
    },
    async removeJiraLabel(label) {
      const cur = this.jiraDetail.issue?.labels || [];
      await this.updateJiraIssue({ labels: cur.filter(l => l !== label) });
    },
    startEditingJiraIssue() {
      this.jiraDetail.editing = true;
      this.jiraDetail.editSummary = this.jiraDetail.issue?.summary || '';
      this.jiraDetail.editDescription = this.jiraDetail.issue?.description || '';
    },
    async saveJiraEdit() {
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key, { method:'PATCH', body: JSON.stringify({ summary: this.jiraDetail.editSummary, description: this.jiraDetail.editDescription }) });
        if (!r.ok) { const d = await r.json(); throw new Error(d.error || ('HTTP '+r.status)); }
        this.showToast('saved','ok');
        this.jiraDetail.editing = false;
        await this.refreshJiraIssue();
        await this.loadJiraBoard();
      } catch(e){
        this.showToast('error saving: '+e.message,'err');
      }
    },
    async refreshJiraIssue() {
      const r = await this.api('/api/jira/issue/'+this.jiraDetail.key);
      this.jiraDetail.issue = await r.json();
    },
    async toggleWatchIssue() {
      const watching = this.jiraDetail.watchers?.watching;
      const accId = this.jiraHealth.me?.accountId;
      try {
        if (watching) {
          await this.api('/api/jira/issue/'+this.jiraDetail.key+'/watchers?account_id='+encodeURIComponent(accId), { method:'DELETE' });
        } else {
          await this.api('/api/jira/issue/'+this.jiraDetail.key+'/watchers', { method:'POST', body: JSON.stringify({ account_id: accId }) });
        }
        await this.loadJiraWatchers();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async loadJiraWatchers() {
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/watchers');
        this.jiraDetail.watchers = await r.json();
      } catch(e){}
    },
    async loadJiraLinkTypes() {
      if (this.jiraLinkTypes.length) return;
      try { const r = await this.api('/api/jira/linktypes'); const d = await r.json(); this.jiraLinkTypes = d.link_types||[]; } catch(e){}
    },
    async addJiraLink() {
      const t = this.jiraDetail.newLinkType, k = (this.jiraDetail.newLinkKey||'').trim().toUpperCase();
      if (!t || !k) return;
      try {
        const r = await this.api('/api/jira/issuelink', { method:'POST', body: JSON.stringify({ type:t, inward_key: this.jiraDetail.key, outward_key: k }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraDetail.newLinkKey = '';
        this.showToast('linked','ok');
        await this.refreshJiraIssue();
        await this.loadJiraDetailTab('links');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async removeJiraLink(linkId) {
      if (!(await this.confirmAsync('Remove link?'))) return;
      try {
        await this.api('/api/jira/issuelink/'+linkId, { method:'DELETE' });
        await this.loadJiraDetailTab('links');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async uploadJiraAttachment(file) {
      if (!file) return;
      const MAX = 32 * 1024 * 1024;
      if (file.size > MAX) {
        this.showToast(`file too large (${this.fmtBytes(file.size)} > ${this.fmtBytes(MAX)})`,'err');
        document.getElementById('jira-attach-input').value = '';
        return;
      }
      const fd = new FormData();
      fd.append('file', file);
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/attachments', { method:'POST', body: fd });
        if (!r.ok) { const d = await r.json().catch(()=>({})); throw new Error(d.error || ('HTTP '+r.status)); }
        this.showToast('attached','ok');
        await this.loadJiraDetailTab('attachments');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
      const el = document.getElementById('jira-attach-input');
      if (el) el.value = '';
    },
    async deleteJiraAttachment(id) {
      if (!(await this.confirmAsync('Delete attachment?'))) return;
      try { await this.api('/api/jira/attachment/'+id, { method:'DELETE' }); await this.loadJiraDetailTab('attachments'); } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async addJiraWorklog() {
      const w = this.jiraDetail.newWorklog;
      if (!w.time_spent) return;
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/worklog', { method:'POST', body: JSON.stringify(w) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraDetail.newWorklog = { time_spent:'', comment:'' };
        this.showToast('worklog logged','ok');
        await this.loadJiraDetailTab('worklog');
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async loadJiraDetailTab(tab) {
      const key = this.jiraDetail.key;
      if (!key) return;
      try {
        if (tab === 'watchers' || tab === 'overview') {
          await this.loadJiraWatchers();
        }
        if (tab === 'subtasks') {
          await this.refreshJiraIssue();
        }
        if (tab === 'links') {
          await this.loadJiraLinkTypes();
          await this.refreshJiraIssue();
          this.jiraDetail.links = this.jiraDetail.issue?.issuelinks || [];
        }
        if (tab === 'attachments') {
          await this.refreshJiraIssue();
          this.jiraDetail.attachments = this.jiraDetail.issue?.attachment || [];
        }
        if (tab === 'worklog') {
          const r = await this.api('/api/jira/issue/'+key+'/worklog');
          const d = await r.json();
          this.jiraDetail.worklogs = d.worklogs || [];
        }
        if (tab === 'history') {
          const r = await this.api('/api/jira/issue/'+key+'/changelog');
          const d = await r.json();
          this.jiraDetail.changelog = d.changelog || [];
        }
      } catch(e){ console.warn('[jira tab '+tab+']', e); }
    },
    async loadJiraSpaces() {
      try { const r = await this.api('/api/jira/confluence/spaces'); const d = await r.json(); this.jiraSpaces = d.spaces || []; } catch(e){ this.showToast('spaces: '+e.message,'err'); }
    },
    async selectJiraSpace(sp) {
      this.jiraSelectedSpace = sp;
      this.jiraPages = []; this.jiraPagesCursor = '';
      try {
        const r = await this.api('/api/jira/confluence/pages?space_id='+encodeURIComponent(sp.id));
        const d = await r.json();
        this.jiraPages = d.pages || [];
        this.jiraPagesCursor = d.next_cursor || '';
      } catch(e){ this.showToast('pages: '+e.message,'err'); }
    },
    async loadMoreJiraPages() {
      if (!this.jiraSelectedSpace || !this.jiraPagesCursor) return;
      try {
        const r = await this.api('/api/jira/confluence/pages?space_id='+encodeURIComponent(this.jiraSelectedSpace.id)+'&cursor='+encodeURIComponent(this.jiraPagesCursor));
        const d = await r.json();
        this.jiraPages = [...this.jiraPages, ...(d.pages||[])];
        this.jiraPagesCursor = d.next_cursor || '';
      } catch(e){}
    },
    async openConfluencePage(pg) {
      this.jiraPageDrawer = { open:true, id:pg.id, title:pg.title, body:'(loading…)', web_url:'' };
      try {
        const r = await this.api('/api/jira/confluence/page/'+pg.id);
        const d = await r.json();
        this.jiraPageDrawer.body = d.body || '(empty page)';
        this.jiraPageDrawer.web_url = d.web_url || '';
      } catch(e){ this.jiraPageDrawer.body = 'error: '+e.message; }
    },

    async openColumnManager() {
      await this.loadJiraConfig();
      let cur = [];
      try {
        const raw = (this.jiraConfig.board_columns||'').trim();
        if (raw) cur = JSON.parse(raw);
      } catch(e){}
      if (!Array.isArray(cur) || cur.length===0) {
        cur = [
          { label:'To Do',       status_names:['To Do'],       color:'border-gray-500/40' },
          { label:'In Progress', status_names:['In Progress'], color:'border-cyan-500/40' },
          { label:'Done',        status_names:['Done'],        color:'border-green-500/40' },
        ];
      }
      this.jiraColumnMgr = { open:true, cols: JSON.parse(JSON.stringify(cur)), editIdx:-1,
        draft:{ label:'', status_names_str:'', color:'border-gray-500/40' } };
    },
    addJiraColumn() {
      const d = this.jiraColumnMgr.draft;
      if (!d.label.trim() || !d.status_names_str.trim()) { this.showToast('label + status_names are required','err'); return; }
      this.jiraColumnMgr.cols.push({
        label: d.label.trim(),
        status_names: d.status_names_str.split(',').map(s=>s.trim()).filter(Boolean),
        color: d.color || 'border-gray-500/40',
      });
      this.jiraColumnMgr.draft = { label:'', status_names_str:'', color:'border-gray-500/40' };
    },
    startEditJiraColumn(i) {
      const c = this.jiraColumnMgr.cols[i];
      this.jiraColumnMgr.editIdx = i;
      this.jiraColumnMgr.draft = {
        label: c.label,
        status_names_str: (c.status_names||[]).join(', '),
        color: c.color || 'border-gray-500/40',
      };
    },
    applyEditJiraColumn() {
      const i = this.jiraColumnMgr.editIdx;
      if (i < 0) return;
      const d = this.jiraColumnMgr.draft;
      this.jiraColumnMgr.cols[i] = {
        label: d.label.trim(),
        status_names: d.status_names_str.split(',').map(s=>s.trim()).filter(Boolean),
        color: d.color || 'border-gray-500/40',
      };
      this.jiraColumnMgr.editIdx = -1;
      this.jiraColumnMgr.draft = { label:'', status_names_str:'', color:'border-gray-500/40' };
    },
    async removeJiraColumn(i) {
      if (!(await this.confirmAsync('Remove column "'+this.jiraColumnMgr.cols[i].label+'"?\n\nIssues in that column status will disappear from the board until the status is listed in another column.'))) return;
      this.jiraColumnMgr.cols.splice(i, 1);
    },
    moveJiraColumn(i, delta) {
      const j = i + delta;
      if (j < 0 || j >= this.jiraColumnMgr.cols.length) return;
      const tmp = this.jiraColumnMgr.cols[i];
      this.jiraColumnMgr.cols[i] = this.jiraColumnMgr.cols[j];
      this.jiraColumnMgr.cols[j] = tmp;
    },
    async saveColumnManager() {
      const payload = JSON.stringify(this.jiraColumnMgr.cols);
      try {
        const r = await this.api('/api/jira/config', { method:'POST', body: JSON.stringify({ board_columns: payload }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraConfig.board_columns = payload;
        this.jiraColumnMgr.open = false;
        this.showToast('columns updated','ok');
        await this.loadJiraConfig();
        await this.loadJiraBoard();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async resetColumnsToDefault() {
      if (!(await this.confirmAsync('Go back to the 3 default columns (To Do / In Progress / Done) by category?'))) return;
      try {
        const r = await this.api('/api/jira/config', { method:'POST', body: JSON.stringify({ board_columns: '[]' }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraConfig.board_columns = '';
        this.jiraColumnMgr.open = false;
        this.showToast('back to the default','ok');
        await this.loadJiraConfig();
        await this.loadJiraBoard();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },

    setJiraHideDoneDays(n) {
      this.jiraHideDoneDays = parseInt(n||0,10) || 0;
      localStorage.setItem('jira_hide_done_days', String(this.jiraHideDoneDays));
    },
    jiraIssueVisible(iss) {
      if (this.jiraHideDoneDays <= 0) return true;
      if (iss.status?.statusCategory?.key !== 'done') return true;
      const last = iss.updated || iss.created;
      if (!last) return true;
      const ageDays = (Date.now() - new Date(last).getTime()) / 86400000;
      return ageDays <= this.jiraHideDoneDays;
    },

    jiraColSortValue(label) {
      const s = this.jiraColSort[label];
      if (!s || s.key === 'none') return 'none';
      return s.key + ':' + (s.dir === 'desc' ? 'desc' : 'asc');
    },
    jiraSetColSort(label, value) {
      let next;
      if (!value || value === 'none') {
        next = { key: 'none', dir: 'asc' };
      } else {
        const [key, dir] = value.split(':');
        next = { key, dir: dir === 'desc' ? 'desc' : 'asc' };
      }
      this.jiraColSort = { ...this.jiraColSort, [label]: next };
      this._persistJiraColSort();
    },
    _persistJiraColSort() {
      try { localStorage.setItem('jira_col_sort', JSON.stringify(this.jiraColSort)); } catch (_) {}
      if (this._jiraColSortSaveTimer) clearTimeout(this._jiraColSortSaveTimer);
      this._jiraColSortSaveTimer = setTimeout(() => {
        this.api('/api/user/prefs', { method:'POST', body: JSON.stringify({ key:'jira_col_sort', value: this.jiraColSort }) })
          .catch(() => {});
      }, 400);
    },
    async jiraColSortLoad() {
      try {
        const r = await this.api('/api/user/prefs?key=jira_col_sort');
        if (!r.ok) return;
        const d = await r.json();
        const v = d && d.value;
        if (!v || typeof v !== 'object' || Array.isArray(v)) return;
        this.jiraColSort = v;
        try { localStorage.setItem('jira_col_sort', JSON.stringify(v)); } catch (_) {}
      } catch (_) {}
    },
    setJiraFontScale(delta) {
      const cur = this.jiraPrefs.fontScale || 1.0;
      let next = (delta === 0) ? 1.0 : cur + delta;
      next = Math.min(1.4, Math.max(0.8, Math.round(next*10)/10));
      this.jiraPrefs = { ...this.jiraPrefs, fontScale: next };
      this._persistJiraPrefs();
    },
    _persistJiraPrefs() {
      try { localStorage.setItem('panel_jira_prefs', JSON.stringify(this.jiraPrefs)); } catch (_) {}
      if (this._jiraPrefsSaveTimer) clearTimeout(this._jiraPrefsSaveTimer);
      this._jiraPrefsSaveTimer = setTimeout(() => {
        this.api('/api/user/prefs', { method:'POST', body: JSON.stringify({ key:'jira', value: this.jiraPrefs }) })
          .catch(() => {});
      }, 400);
    },
    async jiraPrefsLoad() {
      try {
        const r = await this.api('/api/user/prefs?key=jira');
        if (!r.ok) return;
        const d = await r.json();
        const v = d && d.value;
        if (!v || typeof v !== 'object' || Array.isArray(v)) return;
        const fs = Math.min(1.4, Math.max(0.8, parseFloat(v.fontScale)||1.0));
        this.jiraPrefs = { ...this.jiraPrefs, ...v, fontScale: fs };
        try { localStorage.setItem('panel_jira_prefs', JSON.stringify(this.jiraPrefs)); } catch (_) {}
      } catch (_) {}
    },
    jiraSortIssues(arr, label) {
      const s = this.jiraColSort[label];
      if (!s || s.key === 'none') return arr;
      const dir = s.dir === 'desc' ? -1 : 1;
      const cmp = (a, b) => {
        switch (s.key) {
          case 'name':
            return (a.summary || '').localeCompare(b.summary || '', 'en', { sensitivity: 'base' });
          case 'key': {
            const na = parseInt((a.key || '').split('-')[1] || '0', 10) || 0;
            const nb = parseInt((b.key || '').split('-')[1] || '0', 10) || 0;
            return na - nb;
          }
          case 'type':
            return (a.issuetype?.name || '').localeCompare(b.issuetype?.name || '', 'en', { sensitivity: 'base' });
          case 'updated': {
            const ta = new Date(a.updated || a.created || 0).getTime() || 0;
            const tb = new Date(b.updated || b.created || 0).getTime() || 0;
            return ta - tb;
          }
          default:
            return 0;
        }
      };
      return arr.sort((a, b) => dir * cmp(a, b));
    },

    async deleteJiraIssue() {
      const key = this.jiraDetail.key;
      const sub = (this.jiraDetail.subtasks||[]).length > 0;
      const msg = sub
        ? `DELETE ${key} and ${this.jiraDetail.subtasks.length} subtask(s)?\n\nType ${key} to confirm.`
        : `DELETE ${key} permanently?\n\nType ${key} to confirm.`;
      const a = await this.askInput({ title:'Delete issue', label:msg, placeholder:key, validate:(v)=> String(v).trim()===key ? '' : ('Type '+key+' to confirm') });
      if (a === null || a.trim() !== key) return;
      try {
        const url = '/api/jira/issue/'+key + (sub ? '?with_subtasks=1' : '');
        const r = await this.api(url, { method:'DELETE' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.showToast(key+' deleted','ok');
        this.closeJiraDetail();
        await this.loadJiraBoard();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async startJiraAI() {
      if (this.jiraAIBusy) return;
      const key = this.jiraDetail.key;
      if (!(await this.confirmAsync('Trigger AI analysis of ticket '+key+'?\n\nClaude will read the project repository, audit what the ticket describes, and post a comment with a diagnosis and a fix plan.\n\nIt takes ~2-10 min depending on the size of the project.'))) return;
      this.jiraAIBusy = true;
      try {
        const r = await this.api('/api/jira/issue/'+key+'/ai-analyze', { method:'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.showToast('AI running — job '+d.job_id+' (follow it under Operations > Jobs)', 'ok');
        await this.loadJobs();
      } catch(e){
        this.showToast('error: '+e.message, 'err');
      } finally {
        this.jiraAIBusy = false;
      }
    },
    async startJiraWork() {
      if (this.jiraWorkBusy) return;
      const key = this.jiraDetail.key;
      this.jiraWorkBusy = true;
      try {
        const r = await this.api('/api/jira/issue/'+key+'/work', { method:'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        const msg = d.reattached
          ? `Reattaching ${key} → session ${d.session}`
          : `Session created: ${d.session}${d.repo?' (cwd '+d.repo+')':''}`;
        this.showToast(msg, 'ok');
        this.closeJiraDetail();
        try { localStorage.setItem('panel_terminal_resume_session', d.session); } catch(_){}
        this.setPage('terminal');
      } catch(e){
        this.showToast('error: '+e.message, 'err');
      } finally {
        this.jiraWorkBusy = false;
      }
    },
    async cloneJiraIssue() {
      const key = this.jiraDetail.key;
      try {
        const r = await this.api('/api/jira/issue/'+key+'/clone', { method:'POST' });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error);
        this.showToast('cloned: '+d.key,'ok');
        await this.loadJiraBoard();
        this.openJiraIssue({ key: d.key });
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async loadJiraVotes() {
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/votes');
        this.jiraVotes = await r.json();
      } catch(e){}
    },
    async toggleJiraVote() {
      const method = this.jiraVotes.has_voted ? 'DELETE' : 'POST';
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/votes', { method });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        await this.loadJiraVotes();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    startEditJiraComment(c) {
      this.jiraEditComment = { id:c.id, body:c.body||'' };
    },
    async saveEditJiraComment() {
      const c = this.jiraEditComment;
      if (!c.id || !c.body.trim()) return;
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/comment/'+c.id, { method:'PUT', body: JSON.stringify({ body: c.body }) });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraEditComment = { id:'', body:'' };
        const r2 = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/comments');
        const d2 = await r2.json();
        this.jiraDetail.comments = d2.comments || [];
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async deleteJiraComment(c) {
      if (!(await this.confirmAsync('Delete comment?'))) return;
      try {
        const r = await this.api('/api/jira/issue/'+this.jiraDetail.key+'/comment/'+c.id, { method:'DELETE' });
        if (!r.ok) { const d=await r.json(); throw new Error(d.error); }
        this.jiraDetail.comments = this.jiraDetail.comments.filter(x => x.id !== c.id);
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },

    async loadJiraProjectMeta() {
      const k = this.jiraDetail.issue?.project?.key || this.jiraProjectKey;
      if (!k) return;
      try {
        const [rV, rC, rE] = await Promise.all([
          this.api('/api/jira/project/'+k+'/versions'),
          this.api('/api/jira/project/'+k+'/components'),
          this.api('/api/jira/project/'+k+'/epics'),
        ]);
        this.jiraVersions   = (await rV.json()).versions   || [];
        this.jiraComponents = (await rC.json()).components || [];
        this.jiraEpics      = (await rE.json()).epics      || [];
      } catch(e){ console.warn('[jira meta]', e); }
      this.loadJiraAssignableUsers(k, '');
      try {
        const rT = await this.api('/api/jira/issuetypes?project='+encodeURIComponent(k));
        this.jiraDetailIssueTypes = ((await rT.json()).issue_types || []).filter(t => !t.subtask);
      } catch(e){ this.jiraDetailIssueTypes = []; }
    },
    async updateAdvField(patch) {
      await this.updateJiraIssue(patch);
    },

    async loadTodoSeed() {
      try {
        const r = await this.api('/api/todos/seed');
        const d = await r.json();
        const cands = d.candidates || [];
        if (cands.length === 0) { this.showToast('host is clean — nothing to suggest','ok'); return; }
        const titles = cands.map(c=>'• '+c.title).join('\n');
        if (!(await this.confirmAsync('Insert '+cands.length+' TODO(s) based on the current state?\n\n'+titles))) return;
        const r2 = await this.api('/api/todos/seed', { method:'POST' });
        const d2 = await r2.json();
        this.showToast((d2.inserted||[]).length+' TODOs inserted','ok');
        await this.loadTodos();
      } catch(e){ this.showToast('error: '+e.message,'err'); }
    },
    async procsSignal(p, sig) {
      const danger = (sig === 'KILL' || sig === 'TERM');
      const msg = danger
        ? `Send SIG${sig} to PID ${p.pid} (${p.name})?\n\nType "${sig}" to confirm.`
        : `Send SIG${sig} to PID ${p.pid} (${p.name})?`;
      if (danger) {
        const ans = await this.askInput({ title:'Confirm signal', label:msg, placeholder:sig, validate:(v)=> String(v).trim()===sig ? '' : ('Type '+sig+' to confirm') });
        if (ans === null || ans.trim() !== sig) return;
      } else {
        if (!(await this.confirmAsync(msg))) return;
      }
      try {
        const r = await this.api('/api/procs/signal', { method:'POST', body: JSON.stringify({ pid: p.pid, signal: sig }) });
        const d = await r.json();
        if (!r.ok) throw new Error(d.error || ('HTTP '+r.status));
        this.showToast(`SIG${sig} → ${p.pid}`, 'ok');
        setTimeout(() => this.procsRefreshOnce(), 300);
      } catch(e){
        this.showToast(`failed: ${e.message||e}`, 'err');
      }
    },

    async loadContainers(){ try{const r=await this.api('/api/docker/containers'); this.containers=(await r.json())||[]; this.dockerError=null;}catch(e){ this._dockerFail(e); } finally { this.containersLoaded = true; } },
    async loadImages()    { try{const r=await this.api('/api/docker/images');     this.images=(await r.json())||[]; this.dockerError=null;   }catch(e){ this._dockerFail(e); } },
    async loadVolumes()   { try{const r=await this.api('/api/docker/volumes');    this.volumes=await r.json(); this.dockerError=null;           }catch(e){ this._dockerFail(e); } finally { this.volumesLoaded = true; } },
    async loadNetworks()  { try{const r=await this.api('/api/docker/networks');   this.networks=(await r.json())||[]; this.dockerError=null;  }catch(e){ this._dockerFail(e); } finally { this.networksLoaded = true; } },
    async loadCompose()   { try{const r=await this.api('/api/docker/compose');    this.composeProjects=(await r.json())||[]; this.dockerError=null;}catch(e){ this._dockerFail(e); } },
    _dockerFail(e){ if(e && e.message==='unauthorized') return; this.dockerError = (e && e.message) || 'failed to load from Docker'; },

    _loadOk(ns){ this[ns+'Loaded'] = true; this[ns+'Error'] = ''; },
    _loadErr(ns, e, msg){
      this[ns+'Loaded'] = false;
      if (e && e.message === 'unauthorized') { this[ns+'Error'] = ''; return; }
      this[ns+'Error'] = (e && e.message) || msg || 'failed to load';
      console.warn('['+ns+'] load:', e);
    },
    async loadClaude()    { try{const r=await this.api('/api/claude/overview');   this.claudeData=await r.json();          }catch(e){} },
    async loadClaudeTermSessions(){ try{const r=await this.api('/api/terminal/sessions?all=1'); const arr=(await r.json())||[]; const seen=new Set(); this.claudeTermSessions=arr.filter(s=>{ if(!s||!s.name||seen.has(s.name)) return false; seen.add(s.name); return true; }); this._syncPaneAccountSelects();}catch(e){} },
    async loadClaudeAccounts(){
      try{
        const r=await this.api('/api/claude/accounts');
        if(!r.ok) return;
        const d=await r.json();
        this.claudeAccounts={ accounts: d.accounts||[], consumers: d.consumers||[] };
        this._syncPaneAccountSelects();
      }catch(e){}
    },
    async swapSessionAccount(session, accountId){
      if(this.claudeAcctBusy) return;
      this.claudeAcctBusy = true;
      try{
        const r=await this.api('/api/claude/accounts/session-swap',{method:'POST', body: JSON.stringify({session, account_id: accountId})});
        const d=await r.json();
        if(!r.ok) throw new Error(d.error||('HTTP '+r.status));
        if(session==='*'){
          const swapped=(d.results||[]).filter(x=>x.swapped).length;
          const pending=(d.results||[]).filter(x=>x.needs_restart).length;
          let msg=swapped+' session(s) updated';
          if(pending>0) msg+='; '+pending+' awaiting a manual restart';
          this.showToast(msg, pending>0?'warn':'ok');
        } else if(d.needs_restart){
          this.showToast('Account saved — restart "'+session+'" manually to apply','warn');
        } else if(d.method==='respawn'){
          const msg=d.resumed
            ?'Account '+accountId+' active in "'+session+'" — look for the green banner in the terminal'
            :'Account '+accountId+' active — claude restarting in "'+session+'" (green banner in the terminal)';
          this.showToast(msg,'ok');
        } else {
          this.showToast('Account '+accountId+' active live in "'+session+'" — look for the green banner in the terminal','ok');
        }
        await this.loadClaudeTermSessions();
      }catch(e){ this.showToast('Swap failed: '+e.message,'err'); }
      finally{ this.claudeAcctBusy = false; }
    },
    async assignClaudeAccount(consumer, accountId){
      if(this.claudeAcctBusy) return;
      this.claudeAcctBusy = true;
      try{
        const r=await this.api('/api/claude/accounts/assign',{method:'POST', body: JSON.stringify({consumer, account_id: accountId})});
        const d=await r.json();
        if(!r.ok) throw new Error(d.error||('HTTP '+r.status));
        this.showToast(consumer+' → '+accountId+' · takes effect on the next job/session','ok');
        await this.loadClaudeAccounts();
      }catch(e){ this.showToast('Failed: '+e.message,'err'); await this.loadClaudeAccounts(); }
      finally{ this.claudeAcctBusy = false; }
    },
    async openClaudeAccountLogin(accountId){
      if(this.claudeAcctBusy) return;
      this.claudeAcctBusy = true;
      try{
        const r=await this.api('/api/claude/accounts/login-terminal',{method:'POST', body: JSON.stringify({account_id: accountId})});
        const d=await r.json();
        if(!r.ok) throw new Error(d.error||('HTTP '+r.status));
        this.showToast(d.hint||'Login terminal opened','ok');
        if(d.session) this.attachClaudeSession(d.session);
      }catch(e){ this.showToast('Failed: '+e.message,'err'); }
      finally{ this.claudeAcctBusy = false; }
    },
    async loadClaudeAccountsUsage(){
      if(this.claudeUsageBusy) return;
      this.claudeUsageBusy = true;
      try{
        const r=await this.api('/api/claude/accounts/usage');
        if(!r.ok) return;
        const d=await r.json();
        this.claudeUsage={ accounts: d.accounts||[] };
      }catch(e){}
      finally{ this.claudeUsageBusy = false; }
    },
    async loadAIModels(){
      if(this.aiModelsBusy) return;
      this.aiModelsBusy = true;
      try{
        const r = await this.api('/api/admin/ai-models');
        if(!r.ok) return;
        const d = await r.json();
        this.aiModels = { config:d.config||{}, effective:d.effective||{}, allowed:d.allowed||[''], defaults:d.defaults||{} };
      }catch(e){}
      finally{ this.aiModelsBusy = false; }
    },
    async saveAIModels(){
      this.aiModelsBusy = true;
      try{
        const r = await this.api('/api/admin/ai-models', { method:'POST', body: JSON.stringify(this.aiModels.config||{}) });
        if(!r.ok){ const e=await r.json().catch(()=>({})); this.showToast(e.error||'failed to save models', 'err'); return; }
        this.aiModelsSavedAt = Date.now();
        this.showToast('Model tiering saved — takes effect on the next job/session', 'ok');
        await this.loadAIModels();
      }catch(e){ this.showToast('failed to save models', 'err'); }
      finally{ this.aiModelsBusy = false; }
    },
    _modelLabel(m){ return (m==='' || m==null) ? 'Default (Opus)' : m; },
    _fmtTok(n){
      n = Number(n)||0;
      if(n>=1e9) return (n/1e9).toFixed(2)+'B';
      if(n>=1e6) return (n/1e6).toFixed(2)+'M';
      if(n>=1e3) return (n/1e3).toFixed(1)+'K';
      return String(n);
    },
    _tot(st){
      if(!st) return 0;
      return (st.input_tokens||0)+(st.output_tokens||0)+(st.cache_creation_tokens||0)+(st.cache_read_tokens||0);
    },
    _untilReset(unixSec){
      if(!unixSec) return '';
      let s = Math.max(0, Math.floor(unixSec - (this._nowTick||Date.now())/1000));
      const d=Math.floor(s/86400); s-=d*86400;
      const h=Math.floor(s/3600);  s-=h*3600;
      const m=Math.floor(s/60);    s-=m*60;
      const parts=[];
      if(d) parts.push(d+'d');
      if(d||h) parts.push(h+'h');
      if(d||h||m) parts.push(m+'m');
      parts.push(s+'s');
      return parts.join(' ');
    },
    _usageFor(accountId){
      return (this.claudeUsage.accounts||[]).find(a=>a.account_id===accountId) || null;
    },
    _tierRollup(){
      const price = { fable:[10/1e6,50/1e6,0.1], opus:[5/1e6,25/1e6,0.1], opus55:[4/1e6,20/1e6,0.05],
                      sonnet:[3/1e6,15/1e6,0.1], haiku:[1/1e6,5/1e6,0.1] };
      const tierOf = (m)=>{ m=(m||'').toLowerCase();
        if(m.includes('fable')) return 'fable';
        if(m.includes('opus'))   return 'opus';
        if(m.includes('sonnet')) return 'sonnet';
        if(m.includes('haiku'))  return 'haiku';
        return 'opus'; };
      const priceOf = (m,t)=> (t==='opus' && (m||'').toLowerCase().includes('opus-5-5'))
        ? price.opus55 : (price[t]||price.opus);
      const acc = {};
      for(const u of (this.claudeUsage.accounts||[])){
        for(const m of (u.by_model||[])){
          const t = tierOf(m.model), p = priceOf(m.model, t);
          const tok  = (m.input_tokens||0)+(m.output_tokens||0)+(m.cache_creation_tokens||0)+(m.cache_read_tokens||0);
          const ccAll = (m.cache_creation_tokens||0);
          const cc1h  = Math.max(0, Math.min(ccAll, m.cache_creation_1h_tokens||0));
          const cc5m  = ccAll - cc1h;
          const cost = (m.input_tokens||0)*p[0] + (m.output_tokens||0)*p[1]
                     + cc5m*p[0]*1.25 + cc1h*p[0]*2 + (m.cache_read_tokens||0)*p[0]*p[2];
          if(!acc[t]) acc[t]={tier:t,tokens:0,cost:0};
          acc[t].tokens += tok; acc[t].cost += cost;
        }
      }
      const rows = ['haiku','sonnet','opus','fable'].filter(t=>acc[t]).map(t=>acc[t]);
      const totalTok = rows.reduce((s,r)=>s+r.tokens,0)||1;
      rows.forEach(r=>{ r.share = r.tokens/totalTok; });
      return rows;
    },
    async loadClaudeRateLimits(){
      if(this.claudeRatesBusy) return;
      this.claudeRatesBusy = true;
      if(!this._nowTimer){ this._nowTimer = setInterval(()=>{ this._nowTick = Date.now(); }, 1000); }
      try{
        const r=await this.api('/api/claude/accounts/ratelimits');
        if(!r.ok) return;
        const d=await r.json();
        this.claudeRates={ accounts: d.accounts||[] };
      }catch(e){}
      finally{ this.claudeRatesBusy = false; }
    },
    refreshAITab(name){
      if (!['routing','usage','tokens','prompts','agents'].includes(name)) name = 'routing';
      this.aiTab = name;
      if (name==='usage')  { this.loadClaudeRateLimits(); this.loadClaudeAccountsUsage(); }
      if (name==='tokens') { this.loadPrivateApiTokens(); this.loadPrivateApiStatus(); }
      if (name==='prompts'){ this.loadAIPrompts(); }
      if (name==='agents') { this.loadAgents(); this.loadAgentAccounts(); }
    },

    async loadPrivateApiTokens(){
      this.privError = '';
      try {
        const r = await this.api('/api/private-ai/tokens', { raw:true });
        const d = await r.json().catch(()=>({}));
        if (r.status === 503) { this.privError = this._errText(d.error) || 'private-ai-api admin token is not configured.'; this.privTokens = []; return; }
        if (!r.ok) { this.privError = this._errText(d.error) || ('HTTP '+r.status); this.privTokens = []; return; }
        this.privTokens = Array.isArray(d.data) ? d.data : [];
      } catch(e){ if (e.message!=='unauthorized'){ this.privError = e.message||'network error'; } this.privTokens = []; }
    },
    async loadPrivateApiStatus(){
      try {
        const r = await this.api('/api/private-ai/status', { raw:true });
        const d = await r.json().catch(()=>({}));
        if (r.status === 503) { if (!this.privError) this.privError = this._errText(d.error) || 'private-ai-api admin token is not configured.'; this.privStatus = null; return; }
        if (!r.ok) { this.privStatus = null; return; }
        this.privStatus = d;
      } catch(e){ this.privStatus = null; }
    },
    async createPrivateApiToken(){
      if (this.privBusy) return;
      const name = (this.newPrivToken.name||'').trim();
      if (!name) { this.showToast('Enter a name for the token','err'); return; }
      this.privBusy = true;
      try {
        const body = { name };
        if (this.newPrivToken.rpm)    body.rateLimitRpm     = Number(this.newPrivToken.rpm);
        if (this.newPrivToken.tpm)    body.rateLimitTpm     = Number(this.newPrivToken.tpm);
        if (this.newPrivToken.budget) body.dailyTokenBudget = Number(this.newPrivToken.budget);
        if (this.newPrivToken.expiresDays) body.expiresInDays = Number(this.newPrivToken.expiresDays);
        const r = await this.api('/api/private-ai/tokens', {method:'POST', body: JSON.stringify(body)});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast(this._errText(d.error) || ('Failed to create (HTTP '+r.status+')'),'err'); return; }
        this.privCreated = d;
        this.newPrivToken = { name:'', rpm:null, tpm:null, budget:null, expiresDays:null };
        this.showToast('Token created','ok');
        await this.loadPrivateApiTokens();
      } catch(e){ this.showToast('Error creating token: '+(e.message||'network'),'err'); }
      finally { this.privBusy = false; }
    },
    async revokePrivateApiToken(t){
      if (!t || this.privBusy) return;
      if (!(await this.confirmAsync('Revoke the token "'+t.name+'"?\nApplications using this key stop working immediately. This cannot be undone.'))) return;
      this.privBusy = true;
      try {
        const r = await this.api('/api/private-ai/tokens/'+t.id+'/revoke', {method:'POST'});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast(this._errText(d.error) || ('Failed to revoke (HTTP '+r.status+')'),'err'); return; }
        this.showToast('Token revoked','ok');
        await this.loadPrivateApiTokens();
      } catch(e){ this.showToast('Error revoking: '+(e.message||'network'),'err'); }
      finally { this.privBusy = false; }
    },
    copyPrivToken(){
      const v = this.privCreated && this.privCreated.plaintext;
      if (!v) return;
      try { navigator.clipboard.writeText(v); this.showToast('Copied','ok'); }
      catch(e){ this.showToast('Could not copy','err'); }
    },

    _pcCopy(v, label){
      if (!v) return;
      try { navigator.clipboard.writeText(v); this.showToast(label||'Copied','ok'); }
      catch(e){ this.showToast('Could not copy','err'); }
    },
    privUseCreatedToken(){
      if (this.privCreated && this.privCreated.plaintext){
        this.privConn.token = this.privCreated.plaintext;
        this.showToast('Token applied to the sample','ok');
      } else { this.showToast('Create a token above first','err'); }
    },
    _pcBase(){ return (this.privConn.baseUrl||'').trim().replace(/\/+$/,''); },
    _pcHostPort(){ return this._pcBase().replace(/^https?:\/\//,''); },
    privCertCmd(){ return 'echo | openssl s_client -connect '+this._pcHostPort()+' 2>/dev/null | openssl x509 > private-ai-api.crt'; },
    _pcTok(){ return (this.privConn.token||'').trim() || 'sk-priv-••••••••'; },
    _pcModel(){ return (this.privConn.model||'').trim() || (this.privConn.fmt==='openai'?'gpt-4o':'claude-sonnet-4-6'); },
    privConnPath(){ return this.privConn.fmt==='openai' ? '/v1/chat/completions' : '/v1/messages'; },
    privConnFullUrl(){ return this._pcBase() + this.privConnPath(); },
    privConnAuthHeader(){
      const t = this._pcTok();
      return this.privConn.fmt==='openai' ? ('Authorization: Bearer '+t) : ('x-api-key: '+t);
    },
    privSnippet(){
      const base = this._pcBase(), tok = this._pcTok(), model = this._pcModel();
      const anthropic = this.privConn.fmt !== 'openai';
      const insecure = this.privConn.tls === 'insecure';
      const L = (arr) => arr.join('\n');
      switch (this.privConn.lang){
        case 'python-sdk': {
          const verify = insecure ? 'False' : '"private-ai-api.crt"';
          if (anthropic) return L([
            '# pip install anthropic httpx',
            'import httpx',
            'from anthropic import Anthropic',
            '',
            'client = Anthropic(',
            '    base_url="'+base+'",',
            '    api_key="'+tok+'",',
            '    http_client=httpx.Client(verify='+verify+'),  # cert self-signed',
            ')',
            'msg = client.messages.create(',
            '    model="'+model+'",',
            '    max_tokens=1024,',
            '    messages=[{"role": "user", "content": "Hello!"}],',
            ')',
            'print(msg.content[0].text)',
          ]);
          return L([
            '# pip install openai httpx',
            'import httpx',
            'from openai import OpenAI',
            '',
            'client = OpenAI(',
            '    base_url="'+base+'/v1",',
            '    api_key="'+tok+'",',
            '    http_client=httpx.Client(verify='+verify+'),  # cert self-signed',
            ')',
            'r = client.chat.completions.create(',
            '    model="'+model+'",',
            '    messages=[{"role": "user", "content": "Hello!"}],',
            ')',
            'print(r.choices[0].message.content)',
          ]);
        }
        case 'python-http': {
          const verify = insecure ? 'False' : '"private-ai-api.crt"';
          if (anthropic) return L([
            '# pip install requests',
            'import requests',
            '',
            'r = requests.post(',
            '    "'+base+'/v1/messages",',
            '    headers={"x-api-key": "'+tok+'", "anthropic-version": "2023-06-01"},',
            '    json={"model": "'+model+'", "max_tokens": 1024,',
            '          "messages": [{"role": "user", "content": "Hello!"}]},',
            '    verify='+verify+',  # cert self-signed',
            ')',
            'print(r.json())',
          ]);
          return L([
            '# pip install requests',
            'import requests',
            '',
            'r = requests.post(',
            '    "'+base+'/v1/chat/completions",',
            '    headers={"Authorization": "Bearer '+tok+'"},',
            '    json={"model": "'+model+'",',
            '          "messages": [{"role": "user", "content": "Hello!"}]},',
            '    verify='+verify+',  # cert self-signed',
            ')',
            'print(r.json())',
          ]);
        }
        case 'node-sdk': {
          const tlsNote = insecure
            ? '// self-signed: run with  NODE_TLS_REJECT_UNAUTHORIZED=0'
            : '// self-signed: run with  NODE_EXTRA_CA_CERTS=private-ai-api.crt';
          if (anthropic) return L([
            '// npm i @anthropic-ai/sdk',
            tlsNote,
            'import Anthropic from "@anthropic-ai/sdk";',
            '',
            'const client = new Anthropic({',
            '  baseURL: "'+base+'",',
            '  apiKey: "'+tok+'",',
            '});',
            'const msg = await client.messages.create({',
            '  model: "'+model+'",',
            '  max_tokens: 1024,',
            '  messages: [{ role: "user", content: "Hello!" }],',
            '});',
            'console.log(msg.content[0].text);',
          ]);
          return L([
            '// npm i openai',
            tlsNote,
            'import OpenAI from "openai";',
            '',
            'const client = new OpenAI({',
            '  baseURL: "'+base+'/v1",',
            '  apiKey: "'+tok+'",',
            '});',
            'const r = await client.chat.completions.create({',
            '  model: "'+model+'",',
            '  messages: [{ role: "user", content: "Hello!" }],',
            '});',
            'console.log(r.choices[0].message.content);',
          ]);
        }
        case 'node-fetch': {
          const tlsNote = insecure
            ? '// self-signed: run with  NODE_TLS_REJECT_UNAUTHORIZED=0'
            : '// self-signed: run with  NODE_EXTRA_CA_CERTS=private-ai-api.crt';
          if (anthropic) return L([
            tlsNote,
            'const res = await fetch("'+base+'/v1/messages", {',
            '  method: "POST",',
            '  headers: {',
            '    "x-api-key": "'+tok+'",',
            '    "anthropic-version": "2023-06-01",',
            '    "content-type": "application/json",',
            '  },',
            '  body: JSON.stringify({',
            '    model: "'+model+'", max_tokens: 1024,',
            '    messages: [{ role: "user", content: "Hello!" }],',
            '  }),',
            '});',
            'console.log(await res.json());',
          ]);
          return L([
            tlsNote,
            'const res = await fetch("'+base+'/v1/chat/completions", {',
            '  method: "POST",',
            '  headers: {',
            '    "Authorization": "Bearer '+tok+'",',
            '    "content-type": "application/json",',
            '  },',
            '  body: JSON.stringify({',
            '    model: "'+model+'",',
            '    messages: [{ role: "user", content: "Hello!" }],',
            '  }),',
            '});',
            'console.log(await res.json());',
          ]);
        }
        case 'go': {
          const tls = insecure
            ? 'tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // self-signed'
            : 'pool := x509.NewCertPool(); pem, _ := os.ReadFile("private-ai-api.crt"); pool.AppendCertsFromPEM(pem)\n\ttr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}} // self-signed';
          const url = anthropic ? base+'/v1/messages' : base+'/v1/chat/completions';
          const body = anthropic
            ? '`{"model":"'+model+'","max_tokens":1024,"messages":[{"role":"user","content":"Hello!"}]}`'
            : '`{"model":"'+model+'","messages":[{"role":"user","content":"Hello!"}]}`';
          const hdr = anthropic
            ? ['\treq.Header.Set("x-api-key", "'+tok+'")', '\treq.Header.Set("anthropic-version", "2023-06-01")']
            : ['\treq.Header.Set("Authorization", "Bearer '+tok+'")'];
          return L([
            'package main',
            '',
            'import (',
            '\t"crypto/tls"',
            insecure ? '' : '\t"crypto/x509"',
            '\t"io"; "net/http"; "os"; "strings"',
            ')',
            '',
            'func main() {',
            '\t'+tls,
            '\tclient := &http.Client{Transport: tr}',
            '\treq, _ := http.NewRequest("POST", "'+url+'", strings.NewReader('+body+'))',
            ...hdr,
            '\treq.Header.Set("content-type", "application/json")',
            '\tres, err := client.Do(req)',
            '\tif err != nil { panic(err) }',
            '\tdefer res.Body.Close()',
            '\tb, _ := io.ReadAll(res.Body); os.Stdout.Write(b)',
            '}',
          ].filter(x => x !== ''));
        }
        case 'php': {
          const tlsOpt = insecure
            ? '  CURLOPT_SSL_VERIFYPEER => false, // self-signed'
            : '  CURLOPT_CAINFO => "private-ai-api.crt", // self-signed';
          const url = anthropic ? base+'/v1/messages' : base+'/v1/chat/completions';
          const headers = anthropic
            ? '    "x-api-key: '+tok+'", "anthropic-version: 2023-06-01", "content-type: application/json",'
            : '    "Authorization: Bearer '+tok+'", "content-type: application/json",';
          const body = anthropic
            ? '\'{"model":"'+model+'","max_tokens":1024,"messages":[{"role":"user","content":"Hello!"}]}\''
            : '\'{"model":"'+model+'","messages":[{"role":"user","content":"Hello!"}]}\'';
          return L([
            '<?php',
            '$ch = curl_init("'+url+'");',
            'curl_setopt_array($ch, [',
            '  CURLOPT_POST => true,',
            '  CURLOPT_RETURNTRANSFER => true,',
            '  CURLOPT_HTTPHEADER => [',
            headers,
            '  ],',
            '  CURLOPT_POSTFIELDS => '+body+',',
            tlsOpt,
            ']);',
            'echo curl_exec($ch);',
          ]);
        }
        case 'curl':
        default: {
          const tlsFlag = insecure ? '  -k \\' : '  --cacert private-ai-api.crt \\';
          if (anthropic) return L([
            'curl '+base+'/v1/messages \\',
            '  -H "x-api-key: '+tok+'" \\',
            '  -H "anthropic-version: 2023-06-01" \\',
            '  -H "content-type: application/json" \\',
            tlsFlag,
            '  -d \'{"model":"'+model+'","max_tokens":1024,"messages":[{"role":"user","content":"Hello!"}]}\'',
          ]);
          return L([
            'curl '+base+'/v1/chat/completions \\',
            '  -H "Authorization: Bearer '+tok+'" \\',
            '  -H "content-type: application/json" \\',
            tlsFlag,
            '  -d \'{"model":"'+model+'","messages":[{"role":"user","content":"Hello!"}]}\'',
          ]);
        }
      }
    },
    privTokenStatus(t){
      if (!t) return { label:'?', cls:'' };
      if (t.revokedAt) return { label:'revoked', cls:'bg-red-500/20 text-red-300 border border-red-500/30' };
      if (t.expiresAt && t.expiresAt < Date.now()) return { label:'expired', cls:'bg-amber-500/20 text-amber-300 border border-amber-500/30' };
      if (t.enabled === false) return { label:'disabled', cls:'bg-gray-500/20 text-gray-400 border border-gray-500/30' };
      return { label:'active', cls:'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30' };
    },
    _privDate(ms){ try { return new Date(ms).toLocaleDateString(); } catch(e){ return '—'; } },
    _privDur(sec){ sec = Number(sec)||0; if (sec<60) return sec+'s'; const m=Math.floor(sec/60); if (m<60) return m+'min'; const h=Math.floor(m/60); if (h<48) return h+'h'; return Math.floor(h/24)+'d'; },

    async forkClaudeSession(uuid){
      if (this.claudeBusy) return;
      this.claudeBusy = true;
      try {
        const r = await this.api('/api/claude/session/fork', {method:'POST', body: JSON.stringify({resume_uuid: uuid||'', model: this.claudeForkModel||''})});
        const d = await r.json();
        if (!r.ok) throw new Error(d.error||('HTTP '+r.status));
        this.showToast('Session created: '+d.session,'ok');
        await this.loadClaudeTermSessions();
      } catch(e){ this.showToast('Fork failed: '+e.message,'err'); }
      finally { this.claudeBusy = false; }
    },
    async restartClaudeSession(name){
      if (!name || this.claudeBusy) return;
      if (!(await this.confirmAsync('Restart session "'+name+'"?\nClaude will be killed and reopened with --continue (history comes back from the JSONL).\nAnyone connected will have to reconnect.'))) return;
      this.claudeBusy = true;
      try {
        const r = await this.api('/api/claude/session/restart', {method:'POST', body: JSON.stringify({session_name: name, model: this.claudeForkModel||''})});
        const d = await r.json();
        if (!r.ok) throw new Error(d.error||('HTTP '+r.status));
        this.showToast('Session restarted: '+name,'ok');
        await this.loadClaudeTermSessions();
      } catch(e){ this.showToast('Restart failed: '+e.message,'err'); }
      finally { this.claudeBusy = false; }
    },
    attachClaudeSession(name){
      this.setPage('terminal');
      this.$nextTick(()=>{ try { this.reattachSession ? this.reattachSession(name) : this.openHostTerminal(name); } catch(e){} });
    },
    async loadConfig()    { try{const r=await this.api('/api/config');            this.cfg=await r.json();                  }catch(e){} },
    async _safeLoad(path, opts) {
      opts = opts || {};
      const fallback = opts.fallback === undefined ? null : opts.fallback;
      try {
        const r = await this.api(path);
        if (!r.ok) {
          if (!opts.silent) this.showToast((opts.label || path) + ': HTTP ' + r.status, 'err');
          return fallback;
        }
        const data = await r.json();
        if (opts.expect === 'array' && !Array.isArray(data)) {
          if (!opts.silent && data && data.error) this.showToast((opts.label || path) + ': ' + data.error, 'err');
          return fallback;
        }
        return data;
      } catch (e) {
        if (!opts.silent && e && e.message !== 'unauthorized') {
          this.showToast((opts.label || path) + ': ' + (e.message || 'network error'), 'err');
        }
        return fallback;
      }
    },
    async loadPorts(silent)    { const d = await this._safeLoad('/api/system/listening',  {expect:'array', fallback:[], silent, label:'Ports'}); if (d) this.listening = d; },
    async loadConns(silent)    { const d = await this._safeLoad('/api/system/connections',{expect:'array', fallback:[], silent, label:'Connections'}); if (d) this.connections = d; },
    filteredListening() {
      const f = this.portsFilter, t = (f.text||'').toLowerCase();
      return this.listening.filter(p => {
        if (f.proto && (p.Proto||'').toLowerCase() !== f.proto) return false;
        if (t && !((p.Local||'').toLowerCase().includes(t) || (p.Process||'').toLowerCase().includes(t) || String(p.PID||'').includes(t))) return false;
        return true;
      });
    },
    filteredConnections() {
      const f = this.connsFilter, t = (f.text||'').toLowerCase();
      const proto = f.proto || '', state = f.state || '', src = this.connections;
      return __panelMemo('connections', [src, src && src.length, proto, state, t], () => src.filter(c => {
        if (proto && (c.Proto||'').toLowerCase() !== proto) return false;
        if (state && (c.State||'') !== state) return false;
        if (t && !((c.Local||'').toLowerCase().includes(t) || (c.Peer||'').toLowerCase().includes(t))) return false;
        return true;
      }));
    },
    connStateClass(state) {
      switch(state) {
        case 'ESTAB': return 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30';
        case 'LISTEN': return 'bg-sky-500/20 text-sky-300 border border-sky-500/30';
        case 'TIME-WAIT': return 'bg-gray-500/20 text-gray-400 border border-gray-500/30';
        case 'CLOSE-WAIT': return 'bg-amber-500/20 text-amber-300 border border-amber-500/30';
        case 'SYN-SENT': return 'bg-violet-500/20 text-violet-300 border border-violet-500/30';
        default: return 'bg-gray-700/20 text-gray-400 border border-gray-700/30';
      }
    },
    portsAutoStart() {
      this.portsAutoStop();
      if (!this.portsAutoRefresh || this.currentView !== 'ports') return;
      this._portsTimer = setInterval(() => { if (document.hidden) return; this.loadPorts(true); this.loadConns(true); }, this.portsAutoRefresh * 1000);
    },
    portsAutoStop() {
      if (this._portsTimer) { clearInterval(this._portsTimer); this._portsTimer = null; }
    },
    async loadUnits(silent)    { const d = await this._safeLoad('/api/system/units',      {expect:'array', fallback:[], silent, label:'Systemd'}); if (d) this.units = d; this.unitsLoaded = true; },
    async loadAudit()          { const d = await this._safeLoad('/api/audit/tail?n=200',  {expect:'array', fallback:[], label:'Audit'}); if (d) this.audit = d; },
    async loadHistory(silent)  { const d = await this._safeLoad('/api/system/history',    {expect:'array', fallback:[], silent, label:'History'}); if (d) this.history = d; },
    async loadAlertRules()     { const d = await this._safeLoad('/api/metrics/rules',     {label:'Alert rules'}); this.alertRules = (d && d.rules) || []; this.loadRuleSeries(); },
    async loadMetricCatalog()  { const d = await this._safeLoad('/api/metrics/catalog',   {label:'Metrics catalog'}); this.metricCatalog = (d && d.metrics) || []; },
    async loadMetricSnapshot() { const d = await this._safeLoad('/api/metrics/snapshot',  {label:'Live metrics'}); if (d && d.values) { this.metricSnapshot = d.values; this.metricSnapTs = d.t || 0; } },
    async loadRuleSeries()     { const keys=[...new Set((this.alertRules||[]).map(r=>r.metric_key||r.metric||r.field).filter(Boolean))]; const out={}; await Promise.all(keys.map(async k=>{ const d=await this._safeLoad('/api/metrics/series?key='+encodeURIComponent(k),{silent:true,label:'Series'}); if(d&&d.points) out[k]=d.points; })); this.ruleSeries=out; },
    async loadSecrets() {
      if (!(this.usersList || []).length) { try { await this.loadUsers(); } catch(e){} }
      const d = await this._safeLoad('/api/secrets/list', {label:'Secrets'});
      this.secretKeys = (d && d.keys) || [];
      const groups = (d && d.groups) || [];
      this.secretGroups = groups;
      const open = {};
      for (const g of groups) {
        const name = g.name || '';
        open[name] = (name in this.secretGroupsOpen) ? this.secretGroupsOpen[name] : true;
      }
      this.secretGroupsOpen = open;
    },
    toggleSecretGroup(name){ this.secretGroupsOpen[name] = !this.secretGroupsOpen[name]; },
    secretGroupLabel(name){ return name === 'system' ? 'System' : (name || 'No group'); },
    get secretGroupNames(){ return this.secretGroups.map(g => g.name).filter(Boolean); },
    get secretTotalCount(){ return this.secretGroups.reduce((n,g)=> n + ((g.entries&&g.entries.length)||0), 0); },
    get filteredSecretGroups(){
      const q = (this.secretSearch || '').trim().toLowerCase();
      if (!q) return this.secretGroups;
      return this.secretGroups.map(g => ({
        name: g.name,
        entries: (g.entries || []).filter(e =>
          (e.key   || '').toLowerCase().includes(q) ||
          (e.type  || '').toLowerCase().includes(q) ||
          (e.notes || '').toLowerCase().includes(q) ||
          (g.name  || '').toLowerCase().includes(q) ||
          this.secretGroupLabel(g.name).toLowerCase().includes(q)),
      })).filter(g => g.entries.length > 0);
    },
    secretTypeBadge(type){
      switch(type){
        case 'password': return 'badge--danger';
        case 'token':
        case 'api-key':  return 'badge--info';
        case 'cert':
        case 'ssh-key':  return 'badge--warning';
        case 'url':      return 'badge--success';
        default:         return 'badge--neutral';
      }
    },
    secretFmtDate(ts){ if(!ts) return ''; try { return new Date(ts*1000).toLocaleString(); } catch(e){ return ''; } },

    _errText(x){
      if (x == null) return '';
      if (typeof x === 'string') return x;
      if (typeof x === 'object') {
        if (typeof x.message === 'string') return x.message;
        if (typeof x.error === 'string') return x.error;
        if (x.error && typeof x.error === 'object' && typeof x.error.message === 'string') return x.error.message;
        try { return JSON.stringify(x); } catch(_) { return String(x); }
      }
      return String(x);
    },
    showToast(text, kind='', undoAction=null) {
      text = this._errText(text);
      const id = ++this._toastSeq;
      const t = { id, text, kind, undoText: undoAction ? 'undo' : '', undoAction, timer: null };
      this.toasts.push(t);
      const MAX = 3;
      while (this.toasts.length > MAX) {
        const idx = this.toasts.findIndex(x => x.id !== id && !x.undoAction);
        if (idx === -1) break;
        this._clearToastTimer(this.toasts[idx]);
        this.toasts.splice(idx, 1);
      }
      const ms = undoAction ? 5000 : (kind === 'err' ? 8000 : 2800);
      t.timer = setTimeout(() => this._dismissToast(id), ms);
    },
    _clearToastTimer(t){ if (t && t.timer) { clearTimeout(t.timer); t.timer = null; } },
    _dismissToast(id){
      const i = this.toasts.findIndex(x => x.id === id);
      if (i === -1) return;
      this._clearToastTimer(this.toasts[i]);
      this.toasts.splice(i, 1);
    },
    invokeToastUndo(id){
      const t = this.toasts.find(x => x.id === id);
      if (!t) return;
      if (typeof t.undoAction === 'function') { try { t.undoAction(); } catch(e){} }
      this._dismissToast(id);
    },

    sortBy(tableId, field){
      const cur = this.sortState[tableId] || { field:'', dir:1 };
      const dir = (cur.field === field) ? -cur.dir : 1;
      this.sortState[tableId] = { field, dir };
      try { localStorage.setItem('panel_sort_state', JSON.stringify(this.sortState)); } catch(e){}
    },
    sortIcon(tableId, field){
      const s = this.sortState[tableId];
      if (!s || s.field !== field) return '';
      return s.dir === 1 ? ' ↑' : ' ↓';
    },
    applySort(tableId, rows){
      const s = this.sortState[tableId]; if (!s || !s.field) return rows;
      const f = s.field, dir = s.dir;
      return [...rows].sort((a,b)=>{
        const va = this._fieldValue(a, f), vb = this._fieldValue(b, f);
        if (va < vb) return -dir;
        if (va > vb) return dir;
        return 0;
      });
    },
    _fieldValue(obj, path){
      const parts = path.split('.');
      let v = obj;
      for (const p of parts) { if (v == null) return ''; v = v[p]; }
      return typeof v === 'string' ? v.toLowerCase() : (v||0);
    },

    async containerAction(c, action) {
      const id = c.Id;
      const base = action.split('?')[0];
      try {
        const r = await this.api('/api/docker/containers/'+id+'/'+action, {method:'POST'});
        if (!r.ok) { const e=await r.json().catch(()=>({error:'error'})); this.showToast(e.error||'error','err'); return; }
        if (base === 'remove') {
          this.showToast('action: '+base+' ok','ok');
          this.closeDetail();
          this.loadContainers();
          return;
        }
        const pendingLabel = { start:'starting…', stop:'stopping…', restart:'restarting…', kill:'killing…', pause:'pausing…', unpause:'resuming…' }[base] || (base+'…');
        const expected = { start:'running', stop:'exited', kill:'exited', pause:'paused', unpause:'running' }[base];
        const before = c.State;
        this.pendingActions[id] = pendingLabel;
        const delays = [400, 1000, 2000, 4000, 4000, 4000];
        const t0 = Date.now();
        let settled = false;
        try {
          for (const d of delays) {
            await new Promise(res => setTimeout(res, d));
            await this.loadContainers();
            const cur = this.containers.find(x => x.Id === id);
            const state = cur ? cur.State : null;
            if (state == null) { settled = true; break; }
            if (expected ? state === expected : state !== before) { settled = true; break; }
            if (Date.now() - t0 > 15000) break;
          }
        } finally {
          delete this.pendingActions[id];
        }
        if (settled) this.showToast('action: '+base+' ok','ok');
        else this.showToast('action: '+base+' — still in transition, please check','warn');
      } catch(e){ delete this.pendingActions[id]; this.showToast(e.message,'err'); }
    },
    async removeImage(id) {
      if (!(await this.confirmAsync('Remove image?'))) return;
      try { await this.api('/api/docker/images?id='+encodeURIComponent(id)+'&force=1', {method:'DELETE'}); this.showToast('removed','ok'); this.loadImages(); } catch(e){ this.showToast(e.message,'err'); }
    },

    async composeAction(p, action) {
      this.composeOutput='Running '+action+' in '+p.Name+'...';
      try {
        const r = await this.api('/api/docker/compose/action', {method:'POST', body: JSON.stringify({project:p.Name, working_dir:p.WorkingDir, action})});
        const d = await r.json();
        this.composeOutput = d.output || d.error || 'ok';
        this.loadCompose(); this.loadContainers();
      } catch(e){ this.composeOutput='error: '+e.message; }
    },

    _pruneConfirm: {
      volumes: {
        requireText: 'DELETE',
        msg: 'Delete dangling volumes?\n\nEvery Docker volume not referenced by a container will be REMOVED, along with the data inside it (databases, uploads, state of stopped apps).\n\nThere is no automatic backup and this cannot be undone.\n\nType DELETE (uppercase) to confirm:',
      },
      containers: {
        msg: 'Remove stopped containers?\n\nEvery container that is not running will be deleted, along with its logs and its writable filesystem. Named volumes are left untouched.\n\nThis cannot be undone.',
      },
      images: {
        msg: 'Remove dangling images?\n\nUntagged layers will be deleted. Images built locally and never tagged are gone for good — only a new build brings them back.\n\nThis cannot be undone.',
      },
      all: {
        requireText: 'DELETE',
        msg: 'Prune ALL?\n\nDeletes stopped containers, dangling images, dangling volumes (WITH the data inside), unused networks and the whole build cache.\n\nThis cannot be undone.\n\nType DELETE (uppercase) to confirm:',
      },
    },
    async prune(kind) {
      if (this.pruneBusy) return;
      const c = this._pruneConfirm[kind];
      if (c && !(await this.confirmAsync(c.msg, { danger: true, requireText: c.requireText || '' }))) {
        this.showToast('cancelled','warn');
        return;
      }
      this.pruneBusy = true;
      this.pruneOut = 'running...';
      try { const r = await this.api('/api/docker/prune?kind='+kind, {method:'POST'}); this.pruneOut = JSON.stringify(await r.json(), null, 2); this.loadImages(); this.loadVolumes(); }
      catch(e){ this.pruneOut=e.message; }
      finally { this.pruneBusy = false; }
    },
    async pruneAllConfirm() {
      return this.prune('all');
    },
    async pullImage() {
      if (!this.pullRef) return;
      this.pullOut = 'downloading '+this.pullRef+'...\n';
      try {
        const r = await this.api('/api/docker/pull?ref='+encodeURIComponent(this.pullRef), {method:'POST'});
        const reader = r.body.getReader(); const dec = new TextDecoder();
        for(;;){ const {value,done}=await reader.read(); if(done)break; this.pullOut += dec.decode(value); }
        this.loadImages();
      } catch(e){ this.pullOut += '\nerror: '+e.message; }
    },

    filteredUnits(){
      const q=(this.filter.units||'').toLowerCase(); const src=this.units;
      return __panelMemo('units', [src, src && src.length, q], () =>
        q?src.filter(u=>u.Name.toLowerCase().includes(q)):src);
    },
    selectUnit(name){ this.selectedUnit=name; this.unitOutput=''; this.unitMode='status'; this.loadUnitStatus(name); },
    async loadUnitStatus(name){
      this.unitMode = 'status'; this.unitOutput = '… loading status …';
      try {
        const r = await this.api('/api/system/unit-status?unit='+encodeURIComponent(name));
        if (!r.ok) { this.unitOutput = '[HTTP ' + r.status + ']'; return; }
        this.unitOutput = await r.text();
      } catch(e) { this.unitOutput = '[error] ' + (e.message||e); }
    },
    async loadJournal(name){
      this.unitMode = 'journal'; this.unitOutput = '… loading journal ('+this.journalLines+' lines) …';
      try {
        const r = await this.api('/api/system/journal?unit='+encodeURIComponent(name)+'&lines='+this.journalLines);
        if (!r.ok) { this.unitOutput = '[HTTP ' + r.status + ']'; return; }
        const arr = await r.json();
        this.unitOutput = (Array.isArray(arr) ? arr : []).join('\n');
      } catch(e) { this.unitOutput = '[error] ' + (e.message||e); }
    },
    askConfirm(title, message, action, opts) {
      opts = opts || {};
      this.confirmModal = { open: true, title, message, action, danger: !!opts.danger, requireText: opts.requireText || '', typed: '' };
    },
    confirmTypedOk() {
      const want = (this.confirmModal && this.confirmModal.requireText) || '';
      if (!want) return true;
      return String((this.confirmModal && this.confirmModal.typed) || '').trim() === want;
    },
    confirmAsync(message, opts) {
      opts = opts || {};
      const raw = String(message == null ? '' : message);
      let title = opts.title || 'Confirm', body = raw;
      if (!opts.title) {
        const nl = raw.indexOf('\n');
        if (nl > 0 && nl <= 80) { title = raw.slice(0, nl).trim(); body = raw.slice(nl + 1).replace(/^\n+/, ''); }
      }
      return new Promise((resolve) => {
        let done = false;
        const finish = (v) => { if (done) return; done = true; resolve(v); };
        const requireText = opts.requireText || '';
        const state = {
          title, message: body, danger: !!opts.danger,
          requireText, typed: '',
          action: () => {
            if (requireText && String(state.typed == null ? '' : state.typed).trim() !== requireText) return;
            finish(true);
          },
        };
        let _open = true;
        Object.defineProperty(state, 'open', {
          enumerable: true, configurable: true,
          get() { return _open; },
          set(v) { _open = v; if (!v) setTimeout(() => finish(false), 0); },
        });
        this.confirmModal = state;
      });
    },
    _askInputValidate(v) {
      const m = this.askInputModal;
      const fn = m && m.validate;
      if (typeof fn !== 'function') return '';
      try { const r = fn(String(v == null ? '' : v)); return (r && r !== true) ? String(r) : ''; }
      catch (e) { return (e && e.message) || 'invalid'; }
    },
    askInputOk() {
      const m = this.askInputModal;
      if (!m || !m.open) return false;
      return !this._askInputValidate(m.value);
    },
    askInputError() {
      const m = this.askInputModal;
      if (!m || !m.open) return '';
      return this._askInputValidate(m.value);
    },
    _sessionNameError(v) {
      const s = String(v == null ? '' : v);
      if (!s.trim()) return 'Enter a name';
      if (/[^A-Za-z0-9_-]/.test(s)) return 'Use only letters, numbers, _ and -';
      if (s.length > 40) return 'Maximum 40 characters';
      return '';
    },
    askInput(opts) {
      opts = opts || {};
      const validate = typeof opts.validate === 'function' ? opts.validate : null;
      return new Promise((resolve) => {
        let done = false;
        const finish = (v) => { if (done) return; done = true; resolve(v); };
        const state = {
          title: opts.title || 'In',
          label: opts.label || '',
          placeholder: opts.placeholder || '',
          value: opts.value == null ? '' : String(opts.value),
          error: '',
          validate,
          submit: () => {
            const v = String(state.value == null ? '' : state.value);
            if (validate) {
              let msg = '';
              try { const r = validate(v); msg = (r && r !== true) ? String(r) : ''; }
              catch (e) { msg = (e && e.message) || 'invalid'; }
              if (msg) { state.error = msg; return; }
            }
            finish(v);
          },
        };
        let _open = true;
        Object.defineProperty(state, 'open', {
          enumerable: true, configurable: true,
          get() { return _open; },
          set(v) { _open = v; if (!v) setTimeout(() => finish(null), 0); },
        });
        this.askInputModal = state;
        this.$nextTick(() => { try { const el = this.$refs.askInputField; if (el) { el.focus(); el.select(); } } catch (e) {} });
      });
    },
    unitsAutoStart() {
      this.unitsAutoStop();
      if (!this.unitsAutoRefresh || this.currentView !== 'systemd') return;
      this._unitsTimer = setInterval(() => { if (document.hidden) return; this.loadUnits(true); }, this.unitsAutoRefresh * 1000);
    },
    unitsAutoStop() {
      if (this._unitsTimer) { clearInterval(this._unitsTimer); this._unitsTimer = null; }
    },
    unitStateClass(state) {
      switch(state) {
        case 'active': return 'bg-emerald-500/20 text-emerald-300 border border-emerald-500/30';
        case 'inactive': return 'bg-gray-500/20 text-gray-400 border border-gray-500/30';
        case 'failed': return 'bg-rose-500/20 text-rose-300 border border-rose-500/30';
        case 'activating': case 'reloading': return 'bg-amber-500/20 text-amber-300 border border-amber-500/30';
        default: return 'bg-gray-700/20 text-gray-400 border border-gray-700/30';
      }
    },
    async restartUnit(name){ return this.unitAction(name, 'restart'); },
    async unitAction(name, action){
      try {
        const r = await this.api('/api/system/unit-action?unit='+encodeURIComponent(name)+'&action='+action, {method:'POST'});
        const d = await r.json();
        this.unitOutput = action+': '+(d.status||d.error)+'\n'+(d.output||'');
        this.showToast(action+': '+(d.status||d.error), d.error?'err':'ok');
      } catch(e){ this.showToast(e.message,'err'); }
    },
    async runApt(action){
      if (this.sysAct.running) return;
      this.sysAct.running = action;
      this.sysAct.output = 'running apt-get '+action+' (this can take a while)…\n';
      try {
        const r = await this.api('/api/system/apt', {method:'POST', body: JSON.stringify({action})});
        const d = await r.json();
        this.sysAct.output = (d.output||'') + (d.error?'\n[ERROR] '+d.error:'');
        this.showToast('apt '+action+': '+(d.status||'err'), d.error?'err':'ok');
      } catch(e) {
        this.sysAct.output += '\n[exception] '+e.message;
        this.showToast(e.message,'err');
      }
      this.sysAct.running = '';
    },
    async confirmReboot(){
      const msg = 'REBOOT the whole server?\n\nThe server-control-panel WILL GO DOWN — it only comes back when the machine boots again (~30s-2min). Everything running in the sessions is terminated.\n\nType REBOOT (uppercase) to confirm:';
      if (!(await this.confirmAsync(msg, { danger: true, requireText: 'REBOOT' }))) return;
      try {
        const r = await this.api('/api/system/reboot', {method:'POST', body:'{}'});
        const d = await r.json();
        this.showToast('reboot: '+(d.status||d.error), d.error?'err':'ok');
        this.sysAct.output = 'Reboot triggered at '+new Date().toLocaleTimeString()+'.\nWait for the server to come back and reload the page.';
      } catch(e){ this.showToast(e.message,'err'); }
    },

    observeMore(el, id) {
      if (!el || typeof IntersectionObserver === "undefined") return;
      this._scrollDisconnect(id);
      const root = el.closest("[data-scroll-root]") || null;
      const io = new IntersectionObserver((entries) => {
        for (const en of entries) { if (en.isIntersecting) { this._scrollBump(id); } }
      }, { root: root, rootMargin: "200px 0px" });
      io.observe(el);
      this._scrollObservers[id] = io;
    },
    _scrollBump(id) {
      const g = this.git; const i = g && g.inspect;
      switch (id) {
        case "files": {
          const tot = (this.fileList && this.fileList.entries) ? this.fileList.entries.length : 0;
          if (tot > this.fileLimit) this.fileLimit += 200; break;
        }
        case "blame": if (i && i.blame && i.blame.length > g.blameLimit) g.blameLimit += 500; break;
        case "filelog": if (i && i.filelog && i.filelog.length > g.filelogLimit) g.filelogLimit += 200; break;
        case "hunks": if (i && i.hunks && i.hunks.length > g.hunksLimit) g.hunksLimit += 200; break;
        case "reflog": if (i && i.reflog && i.reflog.length > g.reflogLimit) g.reflogLimit += 200; break;
        case "cmpFiles": {
          const t = (i && i.compare && i.compare.files) ? i.compare.files.length : 0;
          if (t > g.cmpFilesLimit) g.cmpFilesLimit += 200; break;
        }
        case "cmpCommits": {
          const t = (i && i.compare && i.compare.commits) ? i.compare.commits.length : 0;
          if (t > g.cmpCommitsLimit) g.cmpCommitsLimit += 200; break;
        }
      }
    },
    _scrollDisconnect(id) {
      const io = this._scrollObservers && this._scrollObservers[id];
      if (io) { try { io.disconnect(); } catch (e) {} delete this._scrollObservers[id]; }
    },
    _reattachFilesScroll() {
      this.$nextTick(() => { const s = document.querySelector('[data-sentinel="files"]'); if (s) this.observeMore(s, "files"); });
    },

    async browseFiles(path){ try{ const r=await this.api('/api/files/list?path='+encodeURIComponent(path)); const d=await r.json(); if(d.error){this.showToast(d.error,'err');return;} this.fileList=d; this.filePath=d.path; this.fileLimit=200; this._reattachFilesScroll();}catch(e){this.showToast(e.message,'err');} },
    async openFile(path){ try{ const r=await this.api('/api/files/read?path='+encodeURIComponent(path)); if(!r.ok){const d=await r.json().catch(()=>({})); this.showToast(d.error||r.statusText,'err');return;} const txt=await r.text(); this.fileEdit={path, content:txt};}catch(e){this.showToast(e.message,'err');} },
    async saveFile(){ try{ const r=await this.api('/api/files/write', {method:'POST', body:JSON.stringify(this.fileEdit)}); const d=await r.json(); if(d.ok){this.showToast('saved','ok');}else{this.showToast(d.error||'error','err');} }catch(e){this.showToast(e.message,'err');} },

    absPath(name){ const base=this.fileList?.path||'/'; return (base+'/'+name).replace('//','/'); },
    breadcrumb(){ const p=this.fileList?.path||'/'; if(p==='/') return [{label:'/',path:'/'}]; const parts=p.split('/').filter(Boolean); const out=[{label:'/',path:'/'}]; let acc=''; for(const s of parts){ acc+='/'+s; out.push({label:s,path:acc}); } return out; },
    iconFor(name){ const l=name.toLowerCase(); if(l.match(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/)) return '🖼'; if(l.match(/\.(mp4|webm|mkv|mov|avi)$/)) return '🎬'; if(l.match(/\.(mp3|wav|ogg|flac|m4a)$/)) return '🎵'; if(l.match(/\.(pdf)$/)) return '📕'; if(l.match(/\.(zip|tar|gz|tgz|7z|rar|bz2|xz)$/)) return '🗜'; if(l.match(/\.(json|ya?ml|toml|ini|conf|cfg)$/)) return '⚙'; if(l.match(/\.(sh|bash|zsh)$/)) return '$'; if(l.match(/\.(go|js|ts|tsx|jsx|py|rb|rs|java|c|cpp|h|html|css)$/)) return '⌨'; if(l.match(/\.(log|txt|md)$/)) return '📄'; return '📄'; },
    isArchive(name){ return /\.(zip|tar|gz|tgz|7z)$/i.test(name); },
    fileIconClass(e){ if(e.is_dir) return 'is-dir'; const l=(e.name||'').toLowerCase(); if(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/.test(l)) return 'is-img'; if(/\.(mp4|webm|mkv|mov|avi)$/.test(l)) return 'is-vid'; if(/\.(mp3|wav|ogg|flac|m4a)$/.test(l)) return 'is-aud'; if(/\.pdf$/.test(l)) return 'is-pdf'; if(/\.(zip|tar|gz|tgz|7z|rar|bz2|xz)$/.test(l)) return 'is-arch'; if(/\.(json|ya?ml|toml|ini|conf|cfg|env)$/.test(l)) return 'is-cfg'; if(/\.(go|js|ts|tsx|jsx|py|rb|rs|java|c|cpp|h|html|css|sh|bash)$/.test(l)) return 'is-code'; return 'is-other'; },
    entryClick(e){ const p=this.absPath(e.name); if(e.is_dir) this.browseFiles(p); else if(this.isImageOrMedia(e.name)) this.previewEntry(e); else this.openFile(p); },
    isImageOrMedia(name){ return /\.(png|jpe?g|gif|webp|svg|bmp|mp4|webm|mp3|wav|ogg|pdf)$/i.test(name); },
    toggleSel(name){ const i=this.fileSel.indexOf(name); if(i<0) this.fileSel.push(name); else this.fileSel.splice(i,1); },
    toggleAllSel(on){ this.fileSel = on ? (this.fileList?.entries||[]).map(e=>e.name) : []; },
    selectedAbs(){ return this.fileSel.map(n=>this.absPath(n)); },
    async refresh(){ if(this.fileList) await this.browseFiles(this.fileList.path); this.fileSel=[]; },

    async newFolder(){ const name=await this.askInput({ title:'New folder', label:'Folder name:', placeholder:'name' }); if(!name) return; const path=this.absPath(name); try{ const r=await this.api('/api/files/mkdir',{method:'POST',body:JSON.stringify({path})}); const d=await r.json(); if(d.ok){this.showToast('folder created','ok'); this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },
    async newFile(){ const name=await this.askInput({ title:'New file', label:'File name:', placeholder:'name.txt' }); if(!name) return; const path=this.absPath(name); try{ const r=await this.api('/api/files/touch',{method:'POST',body:JSON.stringify({path})}); const d=await r.json(); if(d.ok){this.showToast('file created','ok'); this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },
    async uploadFiles(files){ if(!files||!files.length) return; this.fileBusy=true; const dir=this.fileList?.path||'/'; let ok=0,fail=0; for(const f of files){ try{ const fd=new FormData(); fd.append('file',f); const r=await this.api('/api/files/upload?path='+encodeURIComponent(dir),{method:'POST',body:fd}); const d=await r.json(); if(d.ok) ok++; else fail++; }catch(e){fail++;} } this.fileBusy=false; this.showToast(`upload: ${ok} ok, ${fail} failed`,fail?'err':'ok'); this.refresh(); },
    async downloadEntry(name){ const p=this.absPath(name); try{ const r=await this.api('/api/files/download?path='+encodeURIComponent(p)); const blob=await r.blob(); const u=URL.createObjectURL(blob); const a=document.createElement('a'); a.href=u; a.download=name; a.click(); setTimeout(()=>URL.revokeObjectURL(u),5000); }catch(e){ this.showToast('download error: '+e.message,'err'); } },
    async previewEntry(e){ const p=this.absPath(e.name); const l=e.name.toLowerCase(); let kind='other'; if(l.match(/\.(png|jpe?g|gif|webp|svg|bmp|ico)$/)) kind='image'; else if(l.match(/\.(mp4|webm|mkv|mov)$/)) kind='video'; else if(l.match(/\.(mp3|wav|ogg|flac|m4a)$/)) kind='audio'; else if(l.endsWith('.pdf')) kind='pdf'; try{ const r=await this.api('/api/files/preview?path='+encodeURIComponent(p)); const blob=await r.blob(); this.filePreview={ url: URL.createObjectURL(blob), name: e.name, kind }; }catch(err){ this.showToast('preview unavailable: '+err.message,'err'); } },
    async renamePrompt(name){ const fresh=await this.askInput({ title:'Rename', label:'Rename to:', value:name }); if(!fresh||fresh===name) return; const from=this.absPath(name), to=this.absPath(fresh); try{ const r=await this.api('/api/files/rename',{method:'POST',body:JSON.stringify({from,to})}); const d=await r.json(); if(d.ok){this.showToast('renamed','ok'); this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },
    async trashEntry(name){ if(!(await this.confirmAsync('Move '+name+' to the trash?'))) return; const path=this.absPath(name); try{ const r=await this.api('/api/files/trash',{method:'POST',body:JSON.stringify({path})}); const d=await r.json(); if(d.ok){this.showToast('moved to trash','ok'); this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },
    _bulkToast(label, ok, fail, lastErr){
      const total = ok + fail;
      if (fail === 0) { this.showToast(`${label}: ${ok}/${total}`, 'ok'); return; }
      const suf = lastErr ? ' — '+lastErr : '';
      if (ok === 0) this.showToast(`${label} failed: 0/${total}`+suf, 'err');
      else this.showToast(`${label}: ${ok}/${total} — ${fail} failed`+suf, 'warn');
    },
    async bulkTrash(){ if(!(await this.confirmAsync('Move '+this.fileSel.length+' items to the trash?'))) return; const paths=this.selectedAbs(); this.fileBusy=true; let ok=0,fail=0,lastErr=''; for(const p of paths){ try{ const r=await this.api('/api/files/trash',{method:'POST',body:JSON.stringify({path:p})}); const d=await r.json().catch(()=>({ok:true})); if(d && d.ok===false){ fail++; lastErr=d.error||lastErr; } else ok++; }catch(e){ fail++; lastErr=(e&&e.message)||lastErr; } } this.fileBusy=false; this._bulkToast('Trash', ok, fail, lastErr); this.refresh(); },
    async bulkDelete(){ if(!(await this.confirmAsync('PERMANENTLY DELETE '+this.fileSel.length+' items? (no recovery)'))) return; const paths=this.selectedAbs(); try{ const r=await this.api('/api/files/bulk-delete',{method:'POST',body:JSON.stringify({paths})}); const d=await r.json(); const fail=(d.results||[]).filter(x=>!x.ok).length; this.showToast(fail?`${fail} failure(s)`:'deleted',fail?'err':'ok'); this.refresh(); }catch(e){this.showToast(e.message,'err');} },
    async bulkMovePrompt(){ const dest=await this.askInput({ title:'Move', label:'Move to which directory?', value:this.fileList?.path||'/' }); if(!dest) return; const paths=this.selectedAbs(); try{ const r=await this.api('/api/files/bulk-move',{method:'POST',body:JSON.stringify({paths,dest_dir:dest})}); const d=await r.json(); const res=Array.isArray(d.results)?d.results:null; let ok,fail,lastErr=''; if(res){ fail=res.filter(x=>!x.ok).length; ok=res.length-fail; lastErr=(res.find(x=>!x.ok)||{}).error||''; } else if(d.ok===false){ ok=0; fail=paths.length; lastErr=d.error||''; } else { ok=paths.length; fail=0; } this._bulkToast('moved', ok, fail, lastErr); this.refresh(); }catch(e){this.showToast('move failed: '+e.message,'err');} },
    async bulkCopyPrompt(){ const dest=await this.askInput({ title:'Copy', label:'Copy to which directory?', value:this.fileList?.path||'/' }); if(!dest) return; const paths=this.selectedAbs(); this.fileBusy=true; let ok=0,fail=0; for(const p of paths){ const to=(dest+'/'+p.split('/').pop()).replace('//','/'); try{ const r=await this.api('/api/files/copy',{method:'POST',body:JSON.stringify({from:p,to})}); const d=await r.json(); if(d.ok) ok++; else fail++; }catch(e){fail++;} } this.fileBusy=false; this.showToast(`copied: ${ok} ok, ${fail} failed`, fail?'err':'ok'); this.refresh(); },

    compress(fmt){ if(!this.fileSel.length){ this.showToast('select items','err'); return; } const ext={zip:'.zip',tar:'.tar.gz','7z':'.7z'}[fmt]; const dir=this.fileList?.path||'/'; const dest=(dir+'/archive-'+Date.now()+ext).replace('//','/'); this.fileModal={kind:'compress',fmt,dest,paths:this.selectedAbs(),base:dir}; },
    async doCompress(){ const {fmt,dest,paths,base}=this.fileModal; this.fileBusy=true; try{ const r=await this.api('/api/files/'+fmt,{method:'POST',body:JSON.stringify({paths,dest,base})}); const d=await r.json(); if(d.ok){this.showToast('compressed: '+d.dest,'ok'); this.fileModal={kind:''}; this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} this.fileBusy=false; },
    extractEntry(name){ this.fileModal={kind:'extract',path:this.absPath(name),dest:(this.fileList?.path||'/')+'/'+name.replace(/\.(zip|tar\.gz|tgz|tar|7z)$/i,'')}; },
    async doExtract(){ this.fileBusy=true; try{ const r=await this.api('/api/files/extract',{method:'POST',body:JSON.stringify({archive:this.fileModal.path,dest:this.fileModal.dest})}); const d=await r.json(); if(d.ok){this.showToast('extracted','ok'); this.fileModal={kind:''}; this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} this.fileBusy=false; },

    async doChmod(){ try{ const r=await this.api('/api/files/chmod',{method:'POST',body:JSON.stringify({path:this.fileModal.path,mode:this.fileModal.mode,recursive:this.fileModal.rec})}); const d=await r.json(); if(d.ok){this.showToast('chmod ok ('+d.mode+')','ok'); this.fileModal={kind:''}; this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },
    async doChown(){ try{ const r=await this.api('/api/files/chown',{method:'POST',body:JSON.stringify({path:this.fileModal.path,uid:this.fileModal.uid,gid:this.fileModal.gid,recursive:this.fileModal.rec})}); const d=await r.json(); if(d.ok){this.showToast('chown ok','ok'); this.fileModal={kind:''}; this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },

    async doFetch(){ this.fileBusy=true; try{ const r=await this.api('/api/files/fetch',{method:'POST',body:JSON.stringify({url:this.fileModal.url,dest:this.fileModal.dest,filename:this.fileModal.filename})}); const d=await r.json(); if(d.ok){this.showToast('imported: '+d.saved,'ok'); this.fileModal={kind:''}; this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} this.fileBusy=false; },

    async showProps(path){ try{ const r=await this.api('/api/files/properties?path='+encodeURIComponent(path)); const d=await r.json(); if(d.error){this.showToast(d.error,'err'); return;} this.fileProps=d; this.fileModal={kind:'props'}; }catch(e){this.showToast(e.message,'err');} },
    async doHash(path,algo){ try{ const r=await this.api('/api/files/hash?path='+encodeURIComponent(path)+'&algo='+algo); const d=await r.json(); if(d.error){this.showToast(d.error,'err'); return;} navigator.clipboard?.writeText(d.hash); this.showToast(algo+': '+d.hash.slice(0,16)+'… (copied)','ok'); }catch(e){this.showToast(e.message,'err');} },

    async doSearch(){ this.fileBusy=true; const q=new URLSearchParams({root:this.fileSearch.root,name:this.fileSearch.name,content:this.fileSearch.content,max:String(this.fileSearch.max)}); try{ const r=await this.api('/api/files/search?'+q.toString()); const d=await r.json(); this.fileSearch.hits=d.hits||[]; this.showToast((d.count||0)+' results','ok'); }catch(e){this.showToast(e.message,'err');} this.fileBusy=false; },

    async openTrash(){ this.fileTrash.open=true; try{ const r=await this.api('/api/files/trash/list'); const d=await r.json(); this.fileTrash.entries=d.entries||[]; }catch(e){this.showToast(e.message,'err');} },
    async restoreTrash(t){ try{ const r=await this.api('/api/files/trash/restore',{method:'POST',body:JSON.stringify({trash_path:t.trash_path})}); const d=await r.json(); if(d.ok){this.showToast('restored: '+d.restored,'ok'); this.openTrash(); this.refresh();} else this.showToast(d.error||'error','err'); }catch(e){this.showToast(e.message,'err');} },

    openSecretForm(group){ this.newSecret={key:'',value:'',group:group||'',type:'password',notes:''}; this.secretFormOpen=true; },
    async addSecret(){
      if(!(this.newSecret.key||'').trim()){ this.showToast('Enter the key','err'); return; }
      try{
        const r=await this.api('/api/secrets/set', {method:'POST', body:JSON.stringify(this.newSecret)});
        if(!r.ok){ const txt=(await r.text()).trim(); this.showToast(txt||('error '+r.status),'err'); return; }
        this.showToast('Secret saved','ok');
        this.secretFormOpen=false;
        this.newSecret={key:'',value:'',group:'',type:'password',notes:''};
        this.loadSecrets();
      }catch(e){ this.showToast(e.message,'err'); }
    },
    async revealSecret(key){
      try{
        const r=await this.api('/api/secrets/get?key='+encodeURIComponent(key));
        const d=await r.json();
        if(d.error){this.showToast(d.error,'err');return;}
        this.revealedSecret = d;
        this.revealedSecretRemaining = 30;
        if (this._revealTimer) { clearTimeout(this._revealTimer); }
        if (this._revealTick) { clearInterval(this._revealTick); }
        this._revealTick = setInterval(() => {
          this.revealedSecretRemaining--;
          if (this.revealedSecretRemaining <= 0) {
            clearInterval(this._revealTick); this._revealTick = null;
          }
        }, 1000);
        this._revealTimer = setTimeout(() => {
          this.revealedSecret = {};
          this.revealedSecretRemaining = 0;
          this._revealTimer = null;
          if (this._revealTick) { clearInterval(this._revealTick); this._revealTick=null; }
        }, 30000);
      }catch(e){this.showToast(e.message,'err');}
    },
    hideSecret(){
      if (this._revealTimer) { clearTimeout(this._revealTimer); this._revealTimer = null; }
      if (this._revealTick) { clearInterval(this._revealTick); this._revealTick = null; }
      this.revealedSecret = {};
      this.revealedSecretRemaining = 0;
    },
    async copySecret(){
      if (!this.revealedSecret || !this.revealedSecret.value) return;
      try{
        await navigator.clipboard.writeText(this.revealedSecret.value);
        this.showToast('Secret copied — cleared in 30s','ok');
      }catch(e){
        this.showToast('Failed to copy: '+e.message, 'err');
      }
    },
    async deleteSecret(key){ if(!(await this.confirmAsync('Remove '+key+'?')))return; try{ await this.api('/api/secrets/delete', {method:'POST', body:JSON.stringify({key})}); this.loadSecrets();}catch(e){this.showToast(e.message,'err');} },

    async addRule(){ try{ const r=await this.api('/api/metrics/rules/add', {method:'POST', body:JSON.stringify(this.newRule)}); const d=await r.json(); if(d.error){this.showToast(d.error,'err');return;} this.alertFormOpen=false; this.loadAlertRules(); }catch(e){this.showToast(e.message,'err');} },
    async removeRule(name){ try{ await this.api('/api/metrics/rules/remove', {method:'POST', body:JSON.stringify({name})}); this.loadAlertRules(); }catch(e){this.showToast(e.message,'err');} },

    _alertSev:{info:'info',warning:'warning',critical:'critical'},
    sevLabel(s){ return this._alertSev[s] || s || 'warning'; },
    metricDesc(key){ return (this.metricCatalog||[]).find(m=>m.key===key) || null; },
    metricLabel(key){ const d=this.metricDesc(key); return d ? d.label : (key||''); },
    metricUnit(key){ const d=this.metricDesc(key); return d ? d.unit : ''; },
    ruleKey(r){ return r.metric_key || r.metric || r.field || ''; },
    ruleLabel(r){ return r.label || this.metricLabel(this.ruleKey(r)) || this.ruleKey(r); },
    ruleUnit(r){ return r.unit || this.metricUnit(this.ruleKey(r)); },
    _catOrder:['System','Claude','Jobs','Notifications','Docker','WhatsApp','Authentication'],
    _catIcons:{'System':'🖥️','Claude':'✨','Jobs':'⚙️','Notifications':'🔔','Docker':'🐳','WhatsApp':'💬','Authentication':'🔐'},
    catIcon(c){ if(this._catIcons[c]) return this._catIcons[c]; if((c||'').startsWith('Claude')) return '👤'; return '📦'; },
    _catRank(cat){
      const base = this._catOrder.indexOf(cat);
      if (base >= 0) return base*10;
      if ((cat||'').startsWith('Claude')) return this._catOrder.indexOf('Claude')*10 + 1;
      return 999;
    },
    metricGroups(filtered){
      const q = filtered ? (this.metricSearch||'').toLowerCase().trim() : '';
      const g = {};
      for (const m of (this.metricCatalog||[])){
        if (q && !((m.key||'').toLowerCase().includes(q) || (m.label||'').toLowerCase().includes(q) || (m.category||'').toLowerCase().includes(q))) continue;
        (g[m.category||'Other'] = g[m.category||'Other'] || []).push(m);
      }
      return Object.entries(g).sort((a,b)=> (this._catRank(a[0])-this._catRank(b[0])) || a[0].localeCompare(b[0]));
    },
    fmtMetric(v, unit){
      v = Number(v); if (!isFinite(v)) return '—';
      switch(unit){
        case 'bytes':   return this.fmtBytes(v);
        case 'bytes/s': return this.fmtBytes(v)+'/s';
        case 'USD':     return '$'+v.toFixed(2);
        case '%':       return (v<10?v.toFixed(1):v.toFixed(0))+'%';
        case 'tokens':  return this._fmtNum(v);
        case 's':       return this._fmtDur(v);
        case 'load':    return v.toFixed(2);
        default:        return (v%1===0)? String(v) : v.toFixed(2);
      }
    },
    _fmtNum(n){ n=Number(n)||0; if(n>=1e9)return (n/1e9).toFixed(1)+'B'; if(n>=1e6)return (n/1e6).toFixed(1)+'M'; if(n>=1e3)return (n/1e3).toFixed(1)+'k'; return String(Math.round(n)); },
    _fmtDur(s){ s=Number(s)||0; if(s>=86400)return (s/86400).toFixed(1)+'d'; if(s>=3600)return (s/3600).toFixed(1)+'h'; if(s>=60)return (s/60).toFixed(0)+'m'; return s.toFixed(0)+'s'; },
    _thrColor(sev){ return sev==='critical' ? '#f87171' : sev==='info' ? '#a78bfa' : '#fbbf24'; },
    alertToneClass(state){ return ({normal:'ntf-tone-success',pending:'ntf-tone-warning',firing:'ntf-tone-danger',nodata:'ntf-tone-muted'})[state] || 'ntf-tone-muted'; },
    alertStateIco(state){ return ({normal:'✓',pending:'⏳',firing:'🔥',nodata:'∅'})[state] || '∅'; },
    _nowSec(){ return this.clockNowSec || Math.floor(Date.now()/1000); },
    alertStateLabel(r){
      const st = r.state || 'nodata';
      if (st==='normal') return 'Normal';
      if (st==='nodata') return 'No data';
      const since = r.pending_since || 0, dur = r.duration || 0, now = this._nowSec();
      if (st==='pending'){ const left = Math.max(0, dur - (now - since)); return 'Pending · in '+left+'s'; }
      if (st==='firing'){ const held = Math.max(0, (now - since) - dur); return 'Firing for '+held+'s'; }
      return st;
    },
    alertBarPct(r){
      const t = Number(r.threshold)||0, v = Number(r.current_value)||0;
      if (t<=0) return v>0?100:0;
      return Math.max(0, Math.min(100, (v/t)*100));
    },
    alertValueText(r){
      const u = this.ruleUnit(r);
      return 'current value '+this.fmtMetric(r.current_value,u)+' · limit '+r.op+' '+this.fmtMetric(r.threshold,u);
    },
    alertRuleSentence(r){
      const esc = s => String(s==null?'':s).replace(/[&<>"]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
      const key = r.metric || r.field || '';
      const lbl = this.metricLabel(key) || key || '(metric)';
      const u = this.metricUnit(key);
      const nm = r.name ? '<b>'+esc(r.name)+'</b>' : '<span class="ph">(no name)</span>';
      return nm+': <span class="kw">when</span> <b>'+esc(lbl)+'</b> <b>'+esc(r.op)+' '+esc(this.fmtMetric(r.threshold,u))+'</b> '
        +'<span class="kw">for</span> <b>'+esc(r.duration||0)+'s</b> <span class="kw">→ event</span> <b>metric.threshold</b> '
        +'<span class="kw">· severity</span> <b>'+esc(this.sevLabel(r.severity))+'</b>';
    },
    sparkPoints(r){ return (this.ruleSeries && this.ruleSeries[this.ruleKey(r)]) || []; },
    _sparkRange(r){ const pts=this.sparkPoints(r); const vals=pts.map(p=>p.v).concat([Number(r.threshold)||0]); let mn=Math.min(...vals), mx=Math.max(...vals); if(!isFinite(mn))mn=0; if(!isFinite(mx))mx=1; if(mx===mn)mx=mn+1; return {mn,mx}; },
    sparkLine(r){ const pts=this.sparkPoints(r); if(pts.length<2) return ''; const {mn,mx}=this._sparkRange(r); const W=240,H=40,n=pts.length; return pts.map((p,i)=>{ const x=(i/(n-1))*W; const y=H-((p.v-mn)/(mx-mn))*H; return x.toFixed(1)+','+Math.max(1,Math.min(H-1,y)).toFixed(1); }).join(' '); },
    sparkThreshY(r){ const pts=this.sparkPoints(r); if(pts.length<2) return 0; const {mn,mx}=this._sparkRange(r); const H=40; return Math.max(1,Math.min(H-1, H-(((Number(r.threshold)||0)-mn)/(mx-mn))*H)).toFixed(1); },

    _abFresh(){ return { open:true, step:1, kind:'metric', metric:'sys.cpu', op:'>', threshold:80, duration:60, rearm_margin:0, renotify_sec:0, event_type:'metric.threshold', _origin:'', _kind:'', severity:'warning', enabled:true, channels:[], name:'', description:'', editingLimit:'', editingRule:'', aiBusy:false, nameTouched:false }; },
    openAlertBuilder(kind, prefillMetric){
      const b=this._abFresh(); b.kind=kind||'metric'; if(prefillMetric) b.metric=prefillMetric;
      const cur=Number(this.metricSnapshot[b.metric]);
      if(isFinite(cur)&&cur>0) b.threshold=Math.max(1, Math.round(cur*1.5));
      this.alertBuilder=b; this.alertSuggestInstant();
    },
    alertTriggerLabel(){ const b=this.alertBuilder; if(b.kind==='event'){ const et=(this.notify.catalog.event_types||[]).find(e=>e.type_prefix===b.event_type); return et?et.label:b.event_type; } return this.metricLabel(b.metric)||b.metric; },
    _opPhrase(op,past){ const m=past?{'>':'goes above','>=':'reaches','<':'falls below','<=':'stays at or below'}:{'>':'above','>=':'≥','<':'below','<=':'≤'}; return m[op]||op; },
    alertSuggestInstant(){
      const b=this.alertBuilder; if(b.nameTouched) return;
      if(b.kind==='event'){
        const et=(this.notify.catalog.event_types||[]).find(e=>e.type_prefix===b.event_type);
        b.name=et?et.label:'Event alert';
        b.description='Warns when: '+(et?et.label.toLowerCase():b.event_type)+'.';
      } else {
        const lbl=this.metricLabel(b.metric)||b.metric, val=this.fmtMetric(b.threshold,this.metricUnit(b.metric));
        b.name=lbl+' '+this._opPhrase(b.op,false)+' '+val;
        const dur=b.duration>0?(' for '+b.duration+'s'):'';
        b.description='Warns when '+lbl.toLowerCase()+' '+this._opPhrase(b.op,true)+' '+val+dur+'.';
      }
    },
    async alertSuggestAI(){
      const b=this.alertBuilder; b.aiBusy=true;
      try{
        const payload={ kind:b.kind, metric:b.metric, label:this.metricLabel(b.metric), unit:this.metricUnit(b.metric), category:(this.metricDesc(b.metric)||{}).category||'', op:b.op, threshold:Number(b.threshold)||0, duration:Number(b.duration)||0, severity:b.severity, event_type:b.event_type, event_label:this.alertTriggerLabel() };
        const r=await this.api('/api/ai/suggest-alert',{method:'POST',body:JSON.stringify(payload)});
        const d=await r.json().catch(()=>({}));
        if(d.error){ this.showToast('AI: '+d.error,'err'); return; }
        if(d.name){ b.name=d.name; b.nameTouched=true; }
        if(d.description){ b.description=d.description; }
        this.showToast('AI suggestion applied ✨','ok');
      }catch(e){ this.showToast('AI unavailable right now','err'); }
      finally{ b.aiBusy=false; }
    },
    async saveAlert(){
      const b=this.alertBuilder;
      if(!b.name||!b.name.trim()){ this.showToast('Give the alert a name','err'); return; }
      if(!b.channels.length){ this.showToast('Choose at least one destination','err'); return; }
      try{
        if(b.kind==='metric'){
          const r=await this.api('/api/metrics/rules/add',{method:'POST',body:JSON.stringify({name:b.name,metric:b.metric,op:b.op,threshold:Number(b.threshold)||0,duration:Number(b.duration)||0,rearm_margin:Number(b.rearm_margin)||0,renotify_sec:Number(b.renotify_sec)||0,severity:b.severity})});
          const d=await r.json().catch(()=>({})); if(d.error){ this.showToast(d.error,'err'); return; }
          await this.api('/api/notify/rules',{method:'POST',body:JSON.stringify({id:b.editingRule||'',name:'→ '+b.name,enabled:b.enabled,type_prefix:'metric.threshold',min_severity:'',labels:{rule:b.name},channels:b.channels})});
        } else {
          const labels={}; if(b._kind)labels.kind=b._kind; if(b._origin)labels.origin=b._origin;
          await this.api('/api/notify/rules',{method:'POST',body:JSON.stringify({id:b.editingRule||'',name:b.name,enabled:b.enabled,type_prefix:b.event_type,min_severity:b.severity,labels,channels:b.channels})});
        }
        b.open=false; this.loadAlertRules(); this.loadNotify(); this.showToast('Alert saved ✅','ok');
      }catch(e){ this.showToast(e.message||'Error saving','err'); }
    },
    _linkedRule(limitName){ return (this.notify.rules||[]).find(r=>r.labels&&r.labels.rule===limitName) || null; },
    alertDestinations(limitName){ const rl=this._linkedRule(limitName); if(!rl||!rl.channels) return []; return rl.channels.map(id=>{ const c=(this.notify.channels||[]).find(x=>x.id===id); return c?c.name:id; }); },
    alertEnabled(a){ if(a.type==='event'){ return a.rule ? !!a.rule.enabled : true; } const rl=this._linkedRule(a.limit.name); return rl ? !!rl.enabled : true; },
    toggleAlertEnabled(a){ if(a.type==='event'){ if(a.rule) this.toggleNotifyRule(a.rule); return; } const rl=this._linkedRule(a.limit.name); if(rl){ this.toggleNotifyRule(rl); } else { this.showToast('No destination to enable or disable — add a channel to this alert','err'); } },
    unifiedAlerts(){
      const out=[];
      for(const lim of (this.alertRules||[])) out.push({type:'metric', limit:lim, name:lim.name});
      for(const rl of (this.notify.rules||[])) if(rl.type_prefix!=='metric.threshold') out.push({type:'event', rule:rl, name:rl.name});
      return out;
    },
    editAlert(a){
      if(a.type==='metric'){
        const lim=a.limit, rl=this._linkedRule(lim.name);
        this.alertBuilder={ open:true, step:1, kind:'metric', metric:lim.metric_key||lim.metric||'sys.cpu', op:lim.op, threshold:lim.threshold, duration:lim.duration, rearm_margin:lim.rearm_margin||0, renotify_sec:lim.renotify_sec||0, event_type:'metric.threshold', _origin:'', _kind:'', severity:lim.severity||'warning', enabled:rl?rl.enabled:true, channels:rl?(rl.channels||[]):[], name:lim.name, description:'', editingLimit:lim.name, editingRule:rl?rl.id:'', aiBusy:false, nameTouched:true };
      } else {
        const rl=a.rule, L=rl.labels||{};
        this.alertBuilder={ open:true, step:1, kind:'event', metric:'sys.cpu', op:'>', threshold:80, duration:60, rearm_margin:0, renotify_sec:0, event_type:rl.type_prefix, _origin:L.origin||'', _kind:L.kind||'', severity:rl.min_severity||'warning', enabled:rl.enabled, channels:rl.channels||[], name:rl.name, description:'', editingLimit:'', editingRule:rl.id, aiBusy:false, nameTouched:true };
      }
    },
    async removeAlert(a){
      try{
        if(a.type==='metric'){
          const rl=this._linkedRule(a.limit.name);
          await this.api('/api/metrics/rules/remove',{method:'POST',body:JSON.stringify({name:a.limit.name})});
          if(rl) await this.api('/api/notify/rules/delete',{method:'POST',body:JSON.stringify({id:rl.id})});
        } else {
          await this.api('/api/notify/rules/delete',{method:'POST',body:JSON.stringify({id:a.rule.id})});
        }
        this.loadAlertRules(); this.loadNotify();
      }catch(e){ this.showToast(e.message,'err'); }
    },
    toggleAlertChannel(id){ const ch=this.alertBuilder.channels; const i=ch.indexOf(id); if(i>=0)ch.splice(i,1); else ch.push(id); },

    async loadAlerting(){
      try {
        const r = await this.api('/api/admin/alerting', { raw:true });
        if (!r.ok) {
          if (r.status === 403) return;
          this.showToast('Error loading the alert config', 'err');
          return;
        }
        const d = await r.json();
        this.alertingCfg = Object.assign({enabled:false, from_user:'', chat_jid:'', min_severity:''}, d.config || {});
        this.alertingUsers = d.available_users || [];
        this.alertingDirty = false;
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    async saveAlerting(){
      if (this.alertingBusy) return;
      this.alertingBusy = true;
      try {
        const r = await this.api('/api/admin/alerting', {method:'POST', body: JSON.stringify(this.alertingCfg)});
        const d = await r.json();
        if (!r.ok) { this.showToast(d.error || 'Error saving', 'err'); return; }
        this.alertingDirty = false;
        this.showToast('Config saved', 'ok');
      } catch (e) { this.showToast(e.message, 'err'); }
      finally { this.alertingBusy = false; }
    },
    async testAlerting(){
      if (this.alertingBusy) return;
      this.alertingBusy = true;
      try {
        const r = await this.api('/api/admin/alerting/test', {method:'POST'});
        const d = await r.json();
        if (!r.ok) { this.showToast(d.error || 'Test error', 'err'); return; }
        if (d.upstream === 200) this.showToast('Test alert sent ✓', 'ok');
        else if (d.upstream === 204) this.showToast('Alert filtered/discarded (config off or severity)', 'err');
        else this.showToast('Upstream HTTP ' + d.upstream + ': ' + (d.body || ''), 'err');
      } catch (e) { this.showToast(e.message, 'err'); }
      finally { this.alertingBusy = false; }
    },

    async loadNotify(){
      const [ch, rl, cat] = await Promise.all([
        this._safeLoad('/api/notify/channels', {silent:true, label:'Notification channels'}),
        this._safeLoad('/api/notify/rules',    {silent:true, label:'Notification rules'}),
        this._safeLoad('/api/notify/catalog',  {silent:true, label:'Event catalog'}),
      ]);
      this.notify.channels = (ch && ch.channels) || [];
      this.notify.rules    = (rl && rl.rules) || [];
      if (cat) this.notify.catalog = cat;
      this.loadNotifyEvents();
    },
    async loadNotifyEvents(){
      const d = await this._safeLoad('/api/notify/events?limit=100', {silent:true, label:'Event history'});
      if (d) { this.notify.events = d.events || []; this.notify.dropped = d.dropped || 0; }
    },
    newNotifyChannel(){ this.notify.channelForm = {id:'', name:'', type:'inapp', enabled:true, config:{}}; this.notify.channelFormOpen = true; },
    editNotifyChannel(c){ const f = JSON.parse(JSON.stringify(c)); f.config = f.config || {from_user:'', chat_jid:''}; this.notify.channelForm = f; this.notify.channelFormOpen = true; },
    async saveNotifyChannel(){
      if (this.notify.busy) return; this.notify.busy = true;
      try {
        const r = await this.api('/api/notify/channels', {method:'POST', body: JSON.stringify(this.notify.channelForm)});
        const d = await r.json();
        if (!r.ok) { this.showToast(d.error || 'Error saving the channel', 'err'); return; }
        this.notify.channelFormOpen = false; await this.loadNotify(); this.showToast('Channel saved', 'ok');
      } catch (e) { this.showToast(e.message, 'err'); } finally { this.notify.busy = false; }
    },
    async deleteNotifyChannel(id){
      if (!(await this.confirmAsync('Remove this channel? Rules pointing at it stop delivering to it.'))) return;
      try {
        const r = await this.api('/api/notify/channels/delete', {method:'POST', body: JSON.stringify({id})});
        if (!r.ok) { const d = await r.json().catch(()=>({})); this.showToast(d.error || 'Error removing', 'err'); return; }
        await this.loadNotify();
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    async testNotifyChannel(id){
      try {
        const r = await this.api('/api/notify/channels/test', {method:'POST', body: JSON.stringify({id})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast(d.error || 'Test send failed', 'err'); return; }
        this.showToast('Test sent ✓', 'ok');
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    newNotifyRule(){ this.notify.ruleForm = {id:'', name:'', enabled:true, type_prefix:'job.', min_severity:'', source_prefix:'', _kind:'', _origin:'', channels:[]}; this.notify.dryrun = null; this.notify.ruleFormOpen = true; },
    editNotifyRule(r){
      const f = JSON.parse(JSON.stringify(r));
      f._kind = (f.labels && f.labels.kind) || '';
      f._origin = (f.labels && f.labels.origin) || '';
      f.channels = f.channels || [];
      this.notify.ruleForm = f; this.notify.dryrun = null; this.notify.ruleFormOpen = true;
    },
    _ruleFormPayload(){
      const f = this.notify.ruleForm; const labels = {};
      if (f._kind) labels.kind = f._kind;
      if (f._origin) labels.origin = f._origin;
      return {id:f.id, name:f.name, enabled:f.enabled, type_prefix:f.type_prefix||'',
              min_severity:f.min_severity||'', source_prefix:f.source_prefix||'',
              labels, channels:f.channels||[]};
    },
    toggleRuleChannel(id){
      const f = this.notify.ruleForm; if (!f) return;
      const i = f.channels.indexOf(id);
      if (i>=0) f.channels.splice(i,1); else f.channels.push(id);
    },
    async saveNotifyRule(){
      if (this.notify.busy) return; this.notify.busy = true;
      try {
        const r = await this.api('/api/notify/rules', {method:'POST', body: JSON.stringify(this._ruleFormPayload())});
        const d = await r.json();
        if (!r.ok) { this.showToast(d.error || 'Error saving the rule', 'err'); return; }
        this.notify.ruleFormOpen = false; await this.loadNotify(); this.showToast('Rule saved', 'ok');
      } catch (e) { this.showToast(e.message, 'err'); } finally { this.notify.busy = false; }
    },
    async deleteNotifyRule(id){
      if (!(await this.confirmAsync('Remove this rule?'))) return;
      try {
        const r = await this.api('/api/notify/rules/delete', {method:'POST', body: JSON.stringify({id})});
        if (!r.ok) { const d = await r.json().catch(()=>({})); this.showToast(d.error || 'Error removing', 'err'); return; }
        await this.loadNotify();
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    async dryRunNotifyRule(){
      try {
        const r = await this.api('/api/notify/dryrun', {method:'POST', body: JSON.stringify(this._ruleFormPayload())});
        const d = await r.json();
        if (!r.ok) { this.showToast(d.error || 'Dry-run error', 'err'); return; }
        this.notify.dryrun = d;
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    notifyChannelName(id){ const c = (this.notify.channels||[]).find(c=>c.id===id); return c ? c.name : id; },
    notifyEventDot(sev){ return sev==='critical' ? 'text-red-400' : (sev==='warning' ? 'text-amber-400' : 'text-sky-300'); },
    notifyTypeMeta(p){ return this.notifyMeta[p] || {icon:'🔔', tone:'info', short:(p||'Any event'), desc:''}; },
    notifyRuleSentence(f){
      const esc = s => String(s==null?'':s).replace(/[&<>"]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
      const parts = ['<span class="kw">When</span> <b>'+esc(this.notifyTypeMeta(f.type_prefix||'').short)+'</b>'];
      if (f.min_severity) parts.push('<span class="kw">with severity ≥</span> <b>'+esc(f.min_severity)+'</b>');
      if (f._origin)      parts.push('<span class="kw">source</span> <b>'+esc(f._origin)+'</b>');
      if (f._kind)        parts.push('<span class="kw">type</span> <b>'+esc(f._kind)+'</b>');
      const chs = (f.channels||[]).map(id=>'<b>'+esc(this.notifyChannelName(id))+'</b>');
      const dest = chs.length ? chs.join(', ') : '<span class="ph">choose a channel</span>';
      return parts.join(' ') + ' <span class="kw">→ send to</span> ' + dest;
    },
    async toggleNotifyRule(r){
      try {
        const resp = await this.api('/api/notify/rules', {method:'POST', body: JSON.stringify({...r, enabled: !r.enabled})});
        if (!resp.ok) { const d = await resp.json().catch(()=>({})); this.showToast(d.error || 'Error toggling', 'err'); return; }
        await this.loadNotify();
      } catch (e) { this.showToast(e.message, 'err'); }
    },
    notifyChannelTypeMeta(type){
      const ct = (this.notify.catalog.channel_types||[]).find(c=>c.type===type);
      return ct || {type, label:type||'canal', icon:'📣', help:'', fields:[]};
    },
    notifyChannelTypeFields(type){ return this.notifyChannelTypeMeta(type).fields || []; },
    notifyChannelDest(c){
      const cfg = c.config||{};
      switch(c.type){
        case 'whatsapp': return cfg.chat_jid || 'no destination';
        case 'telegram': return cfg.chat_id || 'no chat id';
        case 'email':    return cfg.to || 'no recipient';
        case 'webhook':  return cfg.url || 'no url';
        case 'inapp':    return 'in the bell 🔔 (bottom bar)';
        case 'push':     return cfg.to_user || 'all subscribers';
        default:         return cfg.chat_jid || cfg.to || cfg.url || '—';
      }
    },
    async loadNotifyInbox(){
      const d = await this._safeLoad('/api/notify/inbox?limit=30', {silent:true});
      if (!d || !Array.isArray(d.events)) return;
      const evs = d.events;
      if (this._lastInboxT === undefined) {
        this._lastInboxT = evs.reduce((m,e)=>Math.max(m, e.ts||0), 0);
        return;
      }
      const cutoff = this._lastInboxT;
      let maxT = cutoff;
      for (const e of evs.slice().reverse()) {
        const t = e.ts || 0;
        if (t > cutoff) {
          const tag = e.severity==='critical' ? '🔴 event' : (e.severity==='warning' ? '🟡 event' : '🔵 event');
          this.addNotification(tag, (e.title||e.type) + (e.body ? ' — '+e.body : ''));
          if (t > maxT) maxT = t;
        }
      }
      this._lastInboxT = maxT;
    },

    _vendorLoads: {},

    _ensureUMD(url, prop) {
      if (window[prop]) return Promise.resolve(true);
      if (this._vendorLoads[url]) return this._vendorLoads[url];
      this._vendorLoads[url] = (async () => {
        try {
          const r = await fetch(url);
          if (!r.ok) throw new Error('HTTP ' + r.status);
          new Function('define', 'exports', 'module', await r.text())();
          return !!window[prop];
        } catch (e) {
          this._vendorLoads[url] = null;
          console.warn('[panel] did not load ' + url + ':', e);
          return false;
        }
      })();
      return this._vendorLoads[url];
    },

    _ensureScript(url, prop) {
      if (window[prop]) return Promise.resolve(true);
      if (this._vendorLoads[url]) return this._vendorLoads[url];
      this._vendorLoads[url] = new Promise((resolve) => {
        const el = document.createElement('script');
        el.src = url;
        el.onload = () => resolve(!!window[prop]);
        el.onerror = () => {
          this._vendorLoads[url] = null;
          console.warn('[panel] did not load ' + url);
          resolve(false);
        };
        document.head.appendChild(el);
      });
      return this._vendorLoads[url];
    },

    _ensureChart() { return this._ensureUMD('/vendor/chart/chart.umd.min.js', 'Chart'); },

    async drawCharts() {
      if (!(await this._ensureChart())) return;
      this.$nextTick(()=>{
        const pts = this.history || [];
        const labels = pts.map(p=>new Date(p.T*1000).toLocaleTimeString([], {hour:'2-digit',minute:'2-digit'}));
        const thrSetsFor = (metricKey) => (this.alertRules||[]).filter(r=>(r.metric_key||r.metric||r.field)===metricKey).map(r=>({
          label:r.name, data:labels.map(()=>Number(r.threshold)||0),
          borderColor:this._thrColor(r.severity), borderDash:[6,4], borderWidth:1.25,
          pointRadius:0, fill:false, tension:0, spanGaps:true,
        }));
        const mk = (id, data, color, unit, sMax, field) => {
          const ctx = document.getElementById(id); if(!ctx) return;
          const thrSets = thrSetsFor(field);
          const ex = __panelCharts.get(id);
          if (ex) {
            ex.data.labels = labels;
            ex.data.datasets[0].data = data;
            if (ex.data.datasets.length - 1 === thrSets.length) {
              for (let i=0;i<thrSets.length;i++){
                ex.data.datasets[i+1].data = thrSets[i].data;
                ex.data.datasets[i+1].borderColor = thrSets[i].borderColor;
                ex.data.datasets[i+1].label = thrSets[i].label;
              }
              ex.update('none');
              return;
            }
            ex.destroy(); __panelCharts.delete(id);
          }
          __panelCharts.set(id, new Chart(ctx, {
            type:'line',
            data:{labels, datasets:[{data, borderColor:color, backgroundColor:color+'22', fill:true, tension:0.25, pointRadius:0, borderWidth:1.5, spanGaps:true}, ...thrSets]},
            options:{
              responsive:true, maintainAspectRatio:false, animation:false,
              interaction:{intersect:false, mode:'index'},
              plugins:{
                legend:{display:false},
                tooltip:{callbacks:{label:(i)=>' '+Number(i.parsed.y).toFixed(2)+unit}}
              },
              scales:{
                x:{ticks:{color:'#6b7280',maxTicksLimit:8,maxRotation:0,autoSkip:true}, grid:{color:(getComputedStyle(document.documentElement).getPropertyValue('--border-default').trim()||'#0f1827')}},
                y:{ticks:{color:'#6b7280'}, grid:{color:(getComputedStyle(document.documentElement).getPropertyValue('--border-default').trim()||'#0f1827')}, beginAtZero:true, suggestedMax:sMax}
              }
            }
          }));
        };
        mk('chart-cpu',  pts.map(p=>p.CPU),     '#3b82f6', '%', 100,       'sys.cpu');
        mk('chart-mem',  pts.map(p=>p.MemPct),  '#10b981', '%', 100,       'sys.mem_pct');
        mk('chart-load', pts.map(p=>p.Load1),   '#f59e0b', '',  undefined, 'sys.load1');
        mk('chart-disk', pts.map(p=>p.DiskPct), '#8b5cf6', '%', 100,       'sys.disk.max');
      });
    },

    async changePassword(){ try{ const r=await this.api('/api/auth/change-password', {method:'POST', body:JSON.stringify(this.pwdForm)}); const d=await r.json(); if(d.error){this.showToast(d.error,'err');return;} this.showToast('password changed','ok'); this.pwdForm={old:'',new:''}; }catch(e){this.showToast(e.message,'err');} },

    openContainer(c) {
      this.detail.open=true; this.detail.id=c.Id; this.detail.name=this.contName(c);
      this.detail.tab='overview';
      this.detail.logs=''; this.detail.inspect=''; this.detail.inspectObj=null; this.detail.stats=''; this.detail.top='';
      this.api('/api/docker/containers/'+c.Id+'/inspect').then(r=>r.json()).then(j=>{ this.detail.inspect=JSON.stringify(j,null,2); this.detail.inspectObj=j; }).catch(()=>{});
      this.$nextTick(() => { if (this.detail.open && this.detail.id === c.Id) this._navSyncHistory('push'); });
    },
    closeDetail(){
      this.detail.open=false;
      [this.detail.ws,this.detail.logWS,this.detail.statsWS].forEach(w=>{ if(w)try{w.close();}catch(e){} });
      if (this.detail.term) { try{this.detail.term.dispose();}catch(e){} this.detail.term=null; }
      try { this._navSyncHistory('push'); } catch(_){}
    },
    async loadDetailTop(){ try{ const r=await this.api('/api/docker/containers/'+this.detail.id+'/top'); this.detail.top=JSON.stringify(await r.json(),null,2);}catch(e){} },
    _wsWithRetry(url, handlers) {
      handlers = handlers || {};
      const ctl = { ws: null, cancelled: false, attempts: 0, timer: null, backoff: 1000 };
      const setStatus = (s) => { try { if (handlers.onstatus) handlers.onstatus(s); } catch(_){} };
      const scheduleRetry = () => {
        if (ctl.cancelled) return;
        ctl.attempts += 1;
        const base = Math.min(30000, ctl.backoff);
        const delay = Math.floor(base * (0.8 + Math.random() * 0.4));
        ctl.backoff = Math.min(ctl.backoff * 2, 30000);
        setStatus('reconnecting');
        if (ctl.timer) clearTimeout(ctl.timer);
        ctl.timer = setTimeout(connect, delay);
      };
      const connect = () => {
        if (ctl.cancelled) return;
        setStatus(ctl.attempts > 0 ? 'reconnecting' : 'connecting');
        let ws;
        try { ws = new WebSocket(url); } catch(e) { scheduleRetry(); return; }
        ctl.ws = ws;
        const myWs = ws;
        ws.onopen = (ev) => {
          if (ctl.cancelled || ctl.ws !== myWs) return;
          ctl.attempts = 0; ctl.backoff = 1000;
          setStatus('open');
          try { if (handlers.onopen) handlers.onopen(ev, myWs); } catch(_){}
        };
        ws.onmessage = (ev) => {
          if (ctl.cancelled || ctl.ws !== myWs) return;
          try { if (handlers.onmessage) handlers.onmessage(ev); } catch(_){}
        };
        ws.onerror = (ev) => {
          if (ctl.cancelled || ctl.ws !== myWs) return;
          try { if (handlers.onerror) handlers.onerror(ev); } catch(_){}
        };
        ws.onclose = (ev) => {
          if (ctl.ws === myWs) ctl.ws = null;
          if (ctl.cancelled) return;
          if (handlers.shouldReconnect && !handlers.shouldReconnect(ev)) { setStatus('closed'); return; }
          scheduleRetry();
        };
      };
      ctl.close = (code, reason) => {
        ctl.cancelled = true;
        if (ctl.timer) { clearTimeout(ctl.timer); ctl.timer = null; }
        const ws = ctl.ws; ctl.ws = null;
        if (ws) { try { ws.close(code, reason); } catch(_){} }
      };
      connect();
      return ctl;
    },
    openDetailLogStream(){
      if (this.detail.logWS) { try{this.detail.logWS.close(1000, 'replaced');}catch(e){} this.detail.logWS = null; }
      this.detail.logs='';
      this.detail.logsAutoScroll = true;
      const proto = location.protocol==='https:'?'wss:':'ws:';
      const url = proto+'//'+location.host+'/ws/logs/'+this.detail.id;
      const ctl = this._wsWithRetry(url, {
        onopen: () => { this.detail.logs=''; },
        onmessage: (ev) => {
          this.detail.logs += ev.data;
          const LOG_CAP = 500000;
          if (this.detail.logs.length > LOG_CAP) {
            const head = this.detail.logs.length - LOG_CAP + 200;
            this.detail.logs = '[...truncated ' + head + ' bytes]\n' + this.detail.logs.slice(head);
          }
          if (this.detail.logsAutoScroll) {
            this.$nextTick(() => {
              const el = document.getElementById('detailLogsPre');
              if (el) el.scrollTop = el.scrollHeight;
            });
          }
        },
        onerror: () => { this.detail.logs += '\n[error connecting to the stream]'; },
        shouldReconnect: () => this.detail.open && this.detail.logWS === ctl,
      });
      this.detail.logWS = ctl;
    },
    openDetailStatsStream(){
      if (this.detail.statsWS) { try{this.detail.statsWS.close(1000, 'replaced');}catch(e){} this.detail.statsWS = null; }
      this.detail.stats='';
      const proto = location.protocol==='https:'?'wss:':'ws:';
      const url = proto+'//'+location.host+'/ws/stats/'+this.detail.id;
      const ctl = this._wsWithRetry(url, {
        onmessage: (ev) => { try{ const d=JSON.parse(ev.data); this.detail.stats = JSON.stringify(d,null,2); }catch(e){ this.detail.stats += ev.data; } },
        shouldReconnect: () => this.detail.open && this.detail.statsWS === ctl,
      });
      this.detail.statsWS = ctl;
    },

    _termTheme(){ return { background:'#020617', foreground:'#e5e7eb', cursor:'#60a5fa', selectionBackground:'#1d4ed8' }; },

    buildTerminal(opts){
      const self = this;
      const el = opts.el;
      const state = opts.state;
      state.reconnect = { attempts:0, timer:null, nextDelay:0, cancelled:false };

      if (!state.term) {
        const term = new Terminal({
          fontSize: opts.fontSize || 13,
          fontFamily: (this.termFonts && this.termFonts[this.hostTermFont]?.family) || '"JetBrains Mono", ui-monospace, Menlo, Consolas, monospace',
          theme: opts.themeOverride || this._termTheme(),
          cursorStyle: this.hostTermCursorStyle || 'block',
          cursorBlink: this.hostTermCursorBlink !== false,
          scrollback: this.hostTermScrollback || 10000,
          allowProposedApi: true,
          rightClickSelectsWord: false,
          macOptionIsMeta: true,
          bellStyle: (this.hostTermBell === 'sound') ? 'sound' : 'none',
        });
        const fit = new FitAddon.FitAddon(); term.loadAddon(fit);
        fit._panelTerm = term;
        fit._panelState = state;
        const search = new SearchAddon.SearchAddon(); term.loadAddon(search);
        try {
          search.onDidChangeResults((ev) => {
            if (!ev || ev.resultCount === undefined) return;
            self.hostTermSearchMatchCount = ev.resultCount > 0
              ? (ev.resultIndex + 1) + '/' + ev.resultCount
              : 'none';
          });
        } catch(_) {}
        try { term.loadAddon(new WebLinksAddon.WebLinksAddon()); } catch(e){}
        try {
          const u11 = new Unicode11Addon.Unicode11Addon();
          term.loadAddon(u11);
          term.unicode.activeVersion = '11';
        } catch(e){}
        try { term.loadAddon(new ImageAddon.ImageAddon()); } catch(e){}
        term.open(el);
        try {
          const ta = el.querySelector('.xterm-helper-textarea');
          if (ta) {
            try { if (self.isMobile && self.isMobile()) ta.setAttribute('inputmode','none'); } catch(_){}
            ta.addEventListener('paste', (ev) => {
              const files = self._clipboardFiles(ev);
              if (!files) return;
              ev.preventDefault();
              ev.stopImmediatePropagation();
              if (self._pasteHandled(ev, files)) return;
              self._sendFilesToPane(state, files).catch(()=>{});
            }, true );
          }
        } catch(_) {}
        const loadGpuAddons = () => {
          try {
            const gl = new WebglAddon.WebglAddon();
            gl.onContextLoss(() => { try { gl.dispose(); } catch(_){} state._webgl = null; });
            term.loadAddon(gl);
            state._webgl = gl;
          } catch(e){ state._webgl = null; }
          if (self.hostTermLigatures) {
            self._ensureUMD('/vendor/xterm/ligatures.js', 'LigaturesAddon').then(ok => {
              if (ok) { try { term.loadAddon(new LigaturesAddon.LigaturesAddon()); } catch(e){} }
            });
          }
        };
        if ((self.isMobile && self.isMobile()) || !self.hostTermGpu) {
          /* no GPU: the default DomRenderer = selectable text / immune to context-loss */
        } else if (typeof requestIdleCallback === 'function') {
          requestIdleCallback(loadGpuAddons, { timeout: 250 });
        } else {
          setTimeout(loadGpuAddons, 50);
        }
        setTimeout(()=>{ self._fitSoon(fit); }, 30);

        term.attachCustomKeyEventHandler((ev) => {
          if (ev.type !== 'keydown') return true;
          const c = ev.ctrlKey || ev.metaKey;
          if (ev.key === 'Escape') {
            let hasSel = '';
            try { hasSel = term.getSelection() || ''; } catch(_){}
            if (hasSel) {
              try { term.clearSelection(); } catch(_){}
              ev.preventDefault(); return false;
            }
          }
          // ── Ctrl+C / Ctrl+V ─────────────────────────────────────────────
          // Ctrl+C in a terminal is SIGINT: it is how you interrupt a command. That
          // is why it does NOT become "copy" unconditionally — it only copies when there
          // is a SELECTION, the one moment at which copying is what the person meant
          // to say. With no selection it goes straight to the PTY as ^C. It is the behaviour
          // of Windows Terminal and of the VS Code terminal.
          //
          // After copying, the selection is CLEARED on purpose: that way the next
          // Ctrl+C goes back to interrupting. Without this, a selection forgotten on
          // screen would take the user's SIGINT away exactly when they need it most
          // (a stuck command) — the easiest way to turn a convenience shortcut
          // into a dangerous bug.
          if (c && !ev.shiftKey && !ev.altKey && (ev.key==='c' || ev.key==='C')) {
            let sel = '';
            try { sel = term.getSelection() || ''; } catch(_){}
            if (sel) {
              try { navigator.clipboard.writeText(sel); } catch(_){}
              try { term.clearSelection(); } catch(_){}
              ev.preventDefault(); return false;
            }
            return true;
          }
          if (c && !ev.shiftKey && !ev.altKey && (ev.key==='v' || ev.key==='V')) {
            if (!self.hostTermCtrlV) return true;
            return false;
          }
          // Copy/Paste in the MobaXterm/PuTTY/Xterm style: Ctrl+Insert copies,
          // Shift+Insert pastes. These shortcuts do NOT conflict with anything in the
          // browser (unlike Ctrl+Shift+C, which opens DevTools on
          // Edge/Chrome and is impossible to intercept). Selecting with the
          // mouse ALREADY copies automatically — that is the primary path.
          if (c && !ev.shiftKey && ev.key==='Insert') {
            const sel = term.getSelection();
            if (sel) { navigator.clipboard.writeText(sel).catch(()=>{}); ev.preventDefault(); return false; }
          }
          if (!c && ev.shiftKey && ev.key==='Insert') {
            ev.preventDefault();
            self._pasteIntoPane(state).catch(()=>{});
            return false;
          }
          if (c && ev.shiftKey && (ev.key==='C'||ev.key==='c')) {
            const sel = term.getSelection();
            if (sel) { navigator.clipboard.writeText(sel).catch(()=>{}); ev.preventDefault(); return false; }
          }
          if (c && ev.shiftKey && (ev.key==='V'||ev.key==='v')) {
            ev.preventDefault();
            self._pasteIntoPane(state).catch(()=>{});
            return false;
          }
          if (c && !ev.shiftKey && (ev.key==='f'||ev.key==='F')) {
            self.hostTermSearchOpen = true;
            self.$nextTick(()=>{ const i=document.getElementById('host-term-search-input'); if(i){ i.value=''; i.focus(); }});
            ev.preventDefault(); return false;
          }
          if (c && !ev.shiftKey && (ev.key==='='||ev.key==='+')) { self.hostTermFontDelta(+1); ev.preventDefault(); return false; }
          if (c && !ev.shiftKey && ev.key==='-')                  { self.hostTermFontDelta(-1); ev.preventDefault(); return false; }
          if (c && ev.shiftKey && (ev.key==='K'||ev.key==='k'))   { term.clear(); ev.preventDefault(); return false; }
          if (c && !ev.shiftKey && (ev.key==='w'||ev.key==='W')) {
            try { if (state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input',data:'\x17'})); } catch(_) {}
            ev.preventDefault(); return false;
          }
          if (c && !ev.shiftKey && ev.key===',') {
            self.hostTermSettingsOpen = !self.hostTermSettingsOpen;
            ev.preventDefault(); return false;
          }
          if (ev.ctrlKey && ev.shiftKey && (ev.key === '!' || ev.code === 'Digit1')) {
            self.termHelpOpen = !self.termHelpOpen;
            ev.preventDefault(); return false;
          }
          if (c && ev.shiftKey && (ev.key==='A'||ev.key==='a'))   { try{ term.selectAll(); }catch(_){} ev.preventDefault(); return false; }
          if (c && ev.shiftKey && (ev.key==='R'||ev.key==='r'))   { self._termResetState(state); ev.preventDefault(); return false; }
          if (c && ev.shiftKey && (ev.key==='S'||ev.key==='s'))   { self._termSaveScrollback(state); ev.preventDefault(); return false; }
          if (c && ev.shiftKey && (ev.key==='P'||ev.key==='p'))   { self.openPalette && self.openPalette(); ev.preventDefault(); return false; }
          return true;
        });
        el.addEventListener('paste', (ev) => {
          const files = self._clipboardFiles(ev);
          if (!files) return;
          ev.preventDefault();
          ev.stopPropagation();
          if (self._pasteHandled(ev, files)) return;
          self._sendFilesToPane(state, files).catch(()=>{});
        });

        el.addEventListener('mousedown', (ev) => {
          if (ev._stickySynth) return;

          if (ev.button === 2) {
            try { state._selBeforeCtx = (state.term && state.term.getSelection()) || ''; } catch(_) { state._selBeforeCtx = ''; }
            state._ctxBlockUntil = Date.now() + 500;
            ev.preventDefault();
            ev.stopImmediatePropagation();
            return;
          }
        }, true);
        el.addEventListener('contextmenu', (ev) => {
          if (self.isMobile && self.isMobile()) {
            try {
              const rect = el.getBoundingClientRect();
              const rows = term.rows || 1;
              const rowH = rect.height / rows;
              const clickRow = Math.floor((ev.clientY - rect.top) / rowH);
              const cursorRow = (term.buffer && term.buffer.active && term.buffer.active.cursorY) || 0;
              if (clickRow < cursorRow) return;
            } catch(_){}
          }
          ev.preventDefault();
          ev.stopPropagation();
          self.openTermCtxMenu(state, ev);
        });

        try {
          term.parser.registerOscHandler(52, (data) => {
            try {
              const parts = (data || '').split(';');
              if (parts.length < 2) return false;
              const b64 = parts[parts.length - 1];
              if (!b64 || b64 === '?') return true;
              const text = base64ToText(b64);
              navigator.clipboard.writeText(text).catch(() => {});
              return true;
            } catch (_) { return false; }
          });
        } catch (_) {}
        try {
          term.parser.registerOscHandler(133, (data) => {
            const kind = (data || '').charAt(0);
            if (kind === 'A' || kind === 'C') {
              try {
                const marker = term.registerMarker(0);
                if (marker) {
                  state.marks = state.marks || [];
                  state.marks.push({ kind, marker, ts: Date.now() });
                  if (state.marks.length > 500) state.marks.shift();
                }
              } catch(_) {}
            }
            return false;
          });
        } catch(_) {}

        try {
          el.addEventListener('wheel', (ev) => {
            if (!ev.shiftKey) return;
            if (!state.ws || state.ws.readyState !== 1) return;
            const step = ev.deltaY > 0 ? 4 : -4;
            state._desloc = Math.max(0, (state._desloc || 0) + step);
            try { state.ws.send(JSON.stringify({ type: 'pan', x: state._desloc })); } catch (_) {}
            ev.preventDefault();
          }, { passive: false });
        } catch (_) {}

        try {
          const ro = new ResizeObserver(()=>{ self._fitSoon(fit); });
          ro.observe(el);
          state.resizeObserver = ro;
        } catch(e) {
          window.addEventListener('resize', ()=>{ self._fitSoon(fit); });
        }

        term.onData(d => {
          if (self.terms.broadcast) {
            const panes = self.terms.panes || [];
            if (panes.length > 1) {
              panes.forEach(p => self._paneSendInput(p, d));
              if (d.indexOf('\r') >= 0 && state.notify) state.notify.cmdStart = Date.now();
              return;
            }
          }
          self._paneSendInput(state, d);
          if (d.indexOf('\r') >= 0 && state.notify) state.notify.cmdStart = Date.now();
        });
        let _resizeTimer = null, _lastResize = null;
        state._assertSize = (reason) => {
          const t = state.term;
          if (!t || !state.ws || state.ws.readyState !== 1) return false;
          let cols = t.cols, rows = t.rows;
          try {
            const d = (state.fit && typeof state.fit.proposeDimensions === 'function')
              ? state.fit.proposeDimensions() : null;
            if (d && isFinite(d.cols) && isFinite(d.rows) && d.cols >= 2 && d.rows >= 1) {
              cols = d.cols; rows = d.rows;
            }
          } catch (_) {}
          if (!(cols >= 2 && rows >= 1)) return false;
          try {
            state.ws.send(JSON.stringify({ type:'resize', cols, rows }));
            state._assertedSize = cols + 'x' + rows + (reason ? ' ' + reason : '');
            return true;
          } catch (_) { return false; }
        };
        term.onResize(({cols,rows}) => {
          if (cols < 2 || rows < 1) return;
          _lastResize = {cols, rows};
          if (_resizeTimer) return;
          _resizeTimer = setTimeout(() => {
            _resizeTimer = null;
            state._assertSize('resize');
          }, 100);
        });
        try { term.onScroll(() => { try { self._updateScrollBtn(state); } catch(_){} }); } catch(_){}

        state.term = term; state.fit = fit; state.search = search;
      } else {
        self._fitSoon(state.fit);
      }

      const setStatus = (s) => { if (opts.onStatus) opts.onStatus(s); };

      const open = () => {
        if (state.reconnect.cancelled) return;
        if (state.ws && (state.ws.readyState === 0 || state.ws.readyState === 1)) return;
        setStatus(state.reconnect.attempts>0 ? 'reconnecting' : 'connecting');
        const proto = location.protocol==='https:'?'wss:':'ws:';
        const attachOnly = state.reconnect.attempts > 0;
        const url = proto+'//'+location.host+opts.wsPath
          +(attachOnly ? (opts.wsPath.includes('?')?'&':'?')+'attach=1' : '')
          +(state._primedOk ? (opts.wsPath.includes('?')?'&':'?')+'replay=0' : '');
        const ws = new WebSocket(url);
        ws.binaryType = 'arraybuffer';
        state.ws = ws;
        const myWs = ws;

        const startHeartbeat = () => {
          if (state.heartbeatTimer) clearInterval(state.heartbeatTimer);
          state.heartbeatTimer = setInterval(() => {
            if (state.ws && state.ws.readyState === 1) {
              if (!state._pingAt) state._pingAt = Date.now();
              try { state.ws.send(JSON.stringify({type:'ping', t: Date.now()})); } catch(e){}
              if (state._assertSize) state._assertSize('heartbeat');
              if (state.lastMsgAt && Date.now() - state.lastMsgAt > 70000) {
                try { state.ws.close(4000, 'silent-death'); } catch(_) {}
              }
              if (state._wbPaused && ((state._qBytes||0) + (state._wbPending||0)) <= 32*1024) {
                state._wbPaused = false; state._wbPending = 0;
                try { state.ws.send(JSON.stringify({ type: 'resume' })); } catch(_) {}
              }
            }
          }, 20000);
        };
        const stopHeartbeat = () => {
          if (state.heartbeatTimer) { clearInterval(state.heartbeatTimer); state.heartbeatTimer = null; }
        };

        ws.onopen = () => {
          state.reconnect.attempts = 0;
          state.reconnect.nextDelay = 0;
          state.lastMsgAt = Date.now();
          setStatus('open');
          startHeartbeat();
          state._inQ = []; state._qBytes = 0; state._wbPending = 0; state._wbPaused = false; state._rafId = 0;
          self._safeFit(state.fit);
          try {
            if (state.term.cols >= 2 && state.term.rows >= 1) {
              ws.send(JSON.stringify({ type: 'resize', cols: state.term.cols, rows: state.term.rows }));
            }
          } catch (e) {}
          if (state._echoPainted > 0) {
            try { state.term.write('\b \b'.repeat(state._echoPainted)); } catch(_){}
          }
          state._echoPainted = 0;
          if (state._outbox && state._outbox.length) {
            const pending = state._outbox.join('');
            state._outbox = []; state._outboxBytes = 0;
            try { ws.send(JSON.stringify({ type:'input', data: pending })); } catch(_){}
          }
          if (state._outboxDropped) {
            state._outboxDropped = false;
            try { state.term.write('\r\n\x1b[33m[part of what was typed during the outage exceeded the queue and was not sent]\x1b[0m\r\n'); } catch(_){}
          }
          self._checkBuildStamp();
          state._restarting = false;
          if (state.reconnect) {
            if (state.reconnect.noticeTimer) { clearTimeout(state.reconnect.noticeTimer); state.reconnect.noticeTimer = null; }
            state.reconnect.noticed = false;
            state.reconnect.downSince = 0;
          }
          if (attachOnly && state.term && state.fit) {
            self._reattachRepaint(state);
          }
          if (state.startupCmd && !state.startupRan) {
            state.startupRan = true;
            const payload = state.startupCmd.endsWith('\n') ? state.startupCmd : state.startupCmd + '\n';
            setTimeout(() => {
              if (ws.readyState === 1) {
                try { ws.send(JSON.stringify({type:'input', data: payload})); } catch(e){}
              }
            }, 250);
          }
        };
        const FC_HIGH = 256 * 1024, FC_LOW = 32 * 1024;
        const fcCheck = () => {
          const inflight = (state._qBytes || 0) + (state._wbPending || 0);
          if (!state._wbPaused && inflight >= FC_HIGH) {
            state._wbPaused = true;
            try { if (state.ws && state.ws.readyState === 1) state.ws.send(JSON.stringify({ type: 'pause' })); } catch (_) {}
          } else if (state._wbPaused && inflight <= FC_LOW) {
            state._wbPaused = false;
            try { if (state.ws && state.ws.readyState === 1) state.ws.send(JSON.stringify({ type: 'resume' })); } catch (_) {}
          }
        };
        const flushTerm = () => {
          state._rafId = 0;
          if (!state.term) { state._inQ = []; state._qBytes = 0; return; }
          if (state._holdUntil) {
            const now = Date.now();
            const quiet = now - (state._lastDataAt || 0);
            if (now < state._holdUntil && quiet < 90) {
              if (!state._holdTimer) {
                const wait = Math.max(16, Math.min(90 - quiet, state._holdUntil - now));
                state._holdTimer = setTimeout(() => {
                  state._holdTimer = 0; flushTerm();
                }, wait);
              }
              return;   // keeps queueing, without painting
            }
            state._holdUntil = 0;
          }
          const q = state._inQ; state._inQ = [];
          const batchBytes = state._qBytes || 0; state._qBytes = 0;
          if (!q.length) return;
          // The rule that makes predictive echo safe — the screen goes back to
          // the server's truth BEFORE any byte of it is applied.
          try { self._erasePrediction(state); } catch(_){}
          state._wbPending = (state._wbPending || 0) + batchBytes;
          const last = q.length - 1;
          for (let i = 0; i < last; i++) state.term.write(q[i]);
          state.term.write(q[last], () => {
            state._wbPending = Math.max(0, (state._wbPending || 0) - batchBytes);
            fcCheck();
            try { self._repredictEcho(state); } catch(_){}
          });
          fcCheck();
        };
        ws.onmessage = (ev) => {
          if (!state.term) return;
          if (state.ws !== myWs) return;
          state.lastMsgAt = Date.now();
          if (typeof ev.data === 'string' && ev.data.charCodeAt(0) === 123) {
            let _av = null;
            try { _av = JSON.parse(ev.data); } catch (_) {}
            if (_av && _av.type === 'size' && _av.cols >= 2 && _av.rows >= 1) {
              state._gridSession = { cols: _av.cols, rows: _av.rows };
              try {
                if (state.term.cols !== _av.cols || state.term.rows !== _av.rows) {
                  state.term.resize(_av.cols, _av.rows);
                }
              } catch (_) {}
              return;
            }
          }
          const _n = (typeof ev.data === 'string') ? ev.data.length : ((ev.data && ev.data.byteLength) | 0);
          if (_n === 0) {
            if (state._pingAt) {
              const rtt = Date.now() - state._pingAt; state._pingAt = 0;
              state.rtt = state.rtt ? Math.round(state.rtt * 0.6 + rtt * 0.4) : rtt;
              try { self._updateQuality(state); } catch(_){}
            }
            return;
          }
          if (state._sentAt) {
            const dt = Date.now() - state._sentAt; state._sentAt = 0;
            state.eco = state.eco ? Math.round(state.eco * 0.6 + dt * 0.4) : dt;
            state._serverEchoes = true;
            if (state._echoTimer) { clearTimeout(state._echoTimer); state._echoTimer = 0; }
            try { self._updateQuality(state); } catch(_){}
          }
          state._lastDataAt = Date.now();
          const data = (typeof ev.data === 'string') ? ev.data : new Uint8Array(ev.data);
          (state._inQ || (state._inQ = [])).push(data);
          state._qBytes = (state._qBytes || 0) + (data.length || 0);
          if (!state._rafId) state._rafId = (typeof requestAnimationFrame === 'function') ? requestAnimationFrame(flushTerm) : setTimeout(flushTerm, 16);
          fcCheck();
          if (opts.onOutput) opts.onOutput(ev.data);
          if (self.hostTermAlertPattern) {
            try {
              if (!state._alertRegex || state._alertSrc !== self.hostTermAlertPattern) {
                state._alertSrc = self.hostTermAlertPattern;
                state._alertRegex = new RegExp(self.hostTermAlertPattern, 'i');
              }
              const chunkText = (typeof ev.data === 'string') ? ev.data : (state._alertDec || (state._alertDec = new TextDecoder())).decode(data, { stream: true });
              state._alertBuf = ((state._alertBuf || '') + chunkText).slice(-4096);
              if (state._alertRegex.test(state._alertBuf)) {
                const now = Date.now();
                if (!state._alertLast || now - state._alertLast > 5000) {
                  state._alertLast = now;
                  if (state.term && state.term.element) {
                    state.term.element.style.outline = '2px solid #f43f5e';
                    setTimeout(() => { if (state.term && state.term.element) state.term.element.style.outline = ''; }, 800);
                  }
                  if (self.hostNotifyEnabled && typeof Notification !== 'undefined' && Notification.permission === 'granted') {
                    try { new Notification('Pattern detected in the terminal', { body: 'Match: ' + self.hostTermAlertPattern, tag: 'panel-term-alert' }); } catch(_) {}
                  }
                  state._alertBuf = '';
                }
              }
            } catch (_) {}
          }
        };
        ws.onerror = () => { if (state.ws === myWs) setStatus('error'); };
        ws.onclose = (ev) => {
          stopHeartbeat();
          try { if (state._rafId && typeof cancelAnimationFrame === 'function') cancelAnimationFrame(state._rafId); } catch(_){}
          state._rafId = 0; state._inQ = []; state._qBytes = 0; state._wbPending = 0; state._wbPaused = false;
          try { if (state._holdTimer) clearTimeout(state._holdTimer); } catch(_){}
          state._holdTimer = 0; state._holdUntil = 0;
          if (state._pred && state._pred.txt) {
            if (state._pred.timer) { try { clearTimeout(state._pred.timer); } catch(_){} state._pred.timer = 0; }
            state._echoPainted = (state._echoPainted || 0) + state._pred.txt.length;
            state._pred.txt = '';
          }
          try {
            const isCleanup = state.ws !== myWs || state.reconnect.cancelled;
            const isNormal = ev.wasClean && (ev.code === 1000 || ev.code === 1001 || ev.code === 1005);
            const logFn = (isCleanup || isNormal) ? console.debug : console.warn;
            logFn('[panel:term] ws closed', { code: ev.code, reason: ev.reason, wasClean: ev.wasClean, name: opts.wsPath });
          } catch(_){}
          if (state.ws !== myWs) return;
          if (state.reconnect.cancelled || !state.term) { setStatus('closed'); return; }
          if (ev.code === 4404) {
            state.reconnect.cancelled = true;
            state.ended = true;
            setStatus('closed');
            try { self._renderPaneOverlay(state); } catch(_){}
            try { state.term.write('\r\n\x1b[33m[session ended — it will not be recreated]\x1b[0m\r\n'); } catch(_){}
            return;
          }
          const isRestart = (ev.code === 1012 || ev.code === 1001);
          if (isRestart) state._restarting = true;
          state.reconnect.attempts += 1;
          const FAST = state._restarting ? [250, 400, 700, 1100, 1600] : [300, 700, 1500];
          const n = state.reconnect.attempts;
          const base = n <= FAST.length
            ? FAST[n-1]
            : Math.min(5000, 3000 * Math.pow(2, n - FAST.length - 1));
          const delay = Math.floor(base * (0.8 + Math.random()*0.4));
          state.reconnect.nextDelay = delay;
          state.reconnect.reconnectAt = Date.now() + delay;
          setStatus('reconnecting');
          const codeMsg = ({
            1000: 'connection closed normally',
            1001: 'server exited (restart?)',
            1002: 'wrong WS protocol',
            1006: 'connection closed abruptly (unstable network)',
            1008: 'policy violated (token expired?)',
            1009: 'message too large',
            1011: 'internal server error',
            1012: 'server restarting',
            1013: 'temporary overload — try again in a few seconds',
            4000: 'silence detected (watchdog)',
            4401: 'invalid JWT token (logging in again may be needed)',
          })[ev.code] || ('code '+ev.code);
          const QUIET_MS = state._restarting ? 20000 : 6000;
          if (!state.reconnect.downSince) state.reconnect.downSince = Date.now();
          if (!state.reconnect.noticed && !state.reconnect.noticeTimer) {
            state.reconnect.noticeTimer = setTimeout(() => {
              state.reconnect.noticeTimer = null;
              if (state.reconnect.cancelled) return;
              if (state.ws && state.ws.readyState === 1) return;
              state.reconnect.noticed = true;
              const secs = Math.round((Date.now() - (state.reconnect.downSince || Date.now()))/1000);
              const text = state._restarting
                ? '[server update taking longer than usual — '+secs+'s; reconnecting…]'
                : '['+codeMsg+' — no connection for '+secs+'s, reconnecting… click ↻ to try now]';
              try { state.term.write('\r\n\x1b[33m'+text+'\x1b[0m\r\n'); } catch(_){}
            }, QUIET_MS);
          }
          state.reconnect.timer = setTimeout(open, delay);
        };
      };
      state._reopen = open;

      const primeAndOpen = () => {
        if (state._primerDone) { open(); return; }
        state._primerDone = true;
        const name = state.sessionName;
        const bytes = self._termPrimerBytes ? self._termPrimerBytes() : 0;
        if (!name || !bytes) { open(); return; }
        let opened = false;
        const follow = () => { if (opened) return; opened = true; open(); };
        const cap = setTimeout(() => {
          try { self._markPrimer(state, 'cap'); } catch(_){}
          follow();
        }, 6000);
        const search = (route) => fetch(route + '?name=' + encodeURIComponent(name) + '&bytes=' + bytes,
                                      { credentials: 'same-origin' })
          .then(r => r.ok ? r.arrayBuffer() : null)
          .then(b => (b && b.byteLength) ? b : null);
        search('/api/terminal/history')
          .catch(() => null)
          .then(b => b || search('/api/terminal/raw-log'))
          .then(buf => {
            if (!buf || opened || !state.term) return;
            const u8 = new Uint8Array(buf);
            if (!u8.length) return;
            const CHUNK = 256 * 1024;
            for (let i = 0; i < u8.length; i += CHUNK) {
              state.term.write(u8.subarray(i, Math.min(i + CHUNK, u8.length)));
            }
            state._primedOk = true;
            try { self._markPrimer(state, (u8.length / 1024 | 0) + ' KiB'); } catch(_){}
          })
          .catch(() => {})
          .finally(() => { clearTimeout(cap); follow(); });
      };
      primeAndOpen();
    },

    _termPrimerBytes(){
      const v = parseInt(localStorage.getItem('panel_term_primer_bytes') || '2097152', 10);
      if (!isFinite(v) || v <= 0) return 0;
      return Math.min(v, 16 * 1024 * 1024);
    },
    _markPrimer(state, text){
      state._primerInfo = text;
    },

    _termsStorageKey(){
      return 'panel_tabs_snapshot';
    },

    _termSync: {
      status: 'idle',
      lastErr: '',
      _debounce: { claude: null },
    },
    _termAuthHeaders(){
      return { 'Authorization': 'Bearer ' + (this.token || ''), 'Content-Type': 'application/json' };
    },
    _termNamespaceOf(termsRef){
      return 'claude';
    },
    async _termPullRemote(){
      try {
        const u = (window.__panelStorageInternal && window.__panelStorageInternal.rawGet('panel_active_user')) || '';
      } catch(_){}
      const ctrl = new AbortController();
      const timer = setTimeout(() => { try { ctrl.abort(); } catch(_){} }, 2500);
      try {
        this._termSync.status = 'syncing';
        const r = await fetch('/api/terminal/state', {
          headers: { 'Authorization': 'Bearer ' + this.token },
          signal: ctrl.signal,
        });
        if (!r.ok) throw new Error('HTTP '+r.status);
        const st = await r.json();
        this._termSync.status = 'idle';
        this._termSync.lastErr = '';
        return st;
      } catch (e) {
        if (e && e.name === 'AbortError') {
          this._termSync.status = 'error';
          this._termSync.lastErr = 'Pull timeout (2.5s)';
          console.warn('[term-sync] pull timeout');
        } else {
          this._termSync.status = 'error';
          this._termSync.lastErr = String(e);
          console.warn('[term-sync] pull failed:', e);
        }
        return null;
      } finally {
        clearTimeout(timer);
      }
    },
    _termSnapshotKeys: {
      claude:  'panel_tabs_snapshot',
    },
    _termWorkspacesKey:  'panel_term_workspaces',
    _termTombstonesKey:  'panel_term_tombstones',
    _termTombstoneTTLms: 30 * 24 * 60 * 60 * 1000,

    _termBackfillSavedAt(){
      const now = Date.now() - 1000;
      Object.values(this._termSnapshotKeys).forEach(key => {
        try {
          const raw = localStorage.getItem(key);
          if (!raw) return;
          const snap = JSON.parse(raw);
          if (!snap || typeof snap !== 'object') return;
          if (!Number.isFinite(snap.savedAt) || !snap.savedAt) {
            snap.savedAt = now;
            localStorage.setItem(key, JSON.stringify(snap));
          }
        } catch(_){}
      });
      try {
        const list = JSON.parse(localStorage.getItem(this._termWorkspacesKey) || '[]') || [];
        let dirty = false;
        list.forEach(ws => {
          if (ws && (!Number.isFinite(ws.savedAt) || !ws.savedAt)) {
            ws.savedAt = now; dirty = true;
          }
        });
        if (dirty) localStorage.setItem(this._termWorkspacesKey, JSON.stringify(list));
      } catch(_){}
    },

    _termLoadTombstones(){
      let raw = [];
      try { raw = JSON.parse(localStorage.getItem(this._termTombstonesKey) || '[]') || []; } catch(_){}
      const cutoff = Date.now() - this._termTombstoneTTLms;
      const m = new Map();
      raw.forEach(t => {
        if (!t || !t.name) return;
        const at = Number(t.deletedAt) || 0;
        if (at < cutoff) return;
        m.set(t.name, at);
      });
      this._termPersistTombstones(m);
      return m;
    },
    _termPersistTombstones(map){
      const list = Array.from(map.entries()).map(([name, deletedAt]) => ({name, deletedAt}));
      try { localStorage.setItem(this._termTombstonesKey, JSON.stringify(list)); } catch(_){}
    },
    _termAddTombstone(name){
      if (!name) return;
      const m = this._termLoadTombstones();
      m.set(name, Date.now());
      this._termPersistTombstones(m);
    },

    _termReconcile(remote){
      const safeRemote = (remote && typeof remote === 'object') ? remote : {};
      const remoteSnaps = (safeRemote.snapshots && typeof safeRemote.snapshots === 'object') ? safeRemote.snapshots : {};
      const remoteWs    = Array.isArray(safeRemote.workspaces) ? safeRemote.workspaces : [];
      const remoteTomb  = Array.isArray(safeRemote.deletedWorkspaces) ? safeRemote.deletedWorkspaces : [];
      const localTomb   = this._termLoadTombstones();

      const pushPlan = { snapshots: {}, workspaces: [], deletions: [] };

      Object.entries(this._termSnapshotKeys).forEach(([ns, key]) => {
        let localSnap = null;
        try { localSnap = JSON.parse(localStorage.getItem(key) || 'null'); } catch(_){}
        const remoteSnap = remoteSnaps[ns] || null;
        const lt = Number(localSnap && localSnap.savedAt) || 0;
        const rt = Number(remoteSnap && remoteSnap.savedAt) || 0;
        let winner = null;
        if (localSnap && remoteSnap) winner = (lt >= rt) ? localSnap : remoteSnap;
        else if (localSnap)          winner = localSnap;
        else if (remoteSnap)         winner = remoteSnap;
        if (winner) {
          try { localStorage.setItem(key, JSON.stringify(winner)); } catch(_){}
          if (winner === localSnap && lt > rt) pushPlan.snapshots[ns] = winner;
          else if (winner === localSnap && !remoteSnap) pushPlan.snapshots[ns] = winner;
        }
      });

      const tombByName = new Map(localTomb);
      remoteTomb.forEach(t => {
        if (!t || !t.name) return;
        const at = Number(t.deletedAt) || 0;
        const cur = tombByName.get(t.name) || 0;
        if (at > cur) tombByName.set(t.name, at);
      });
      this._termPersistTombstones(tombByName);

      const localWs = (() => {
        try { return JSON.parse(localStorage.getItem(this._termWorkspacesKey) || '[]') || []; }
        catch(_) { return []; }
      })();
      const byName = new Map();
      const consider = (ws) => {
        if (!ws || !ws.name) return;
        const tombAt = tombByName.get(ws.name) || 0;
        const wsAt   = Number(ws.savedAt) || 0;
        if (tombAt > 0 && tombAt >= wsAt) {
          return;
        }
        const cur = byName.get(ws.name);
        if (!cur || wsAt > (Number(cur.savedAt) || 0)) byName.set(ws.name, ws);
      };
      localWs.forEach(consider);
      remoteWs.forEach(consider);
      const merged = Array.from(byName.values()).sort((a,b) => (a.name||'').localeCompare(b.name||''));
      try { localStorage.setItem(this._termWorkspacesKey, JSON.stringify(merged)); } catch(_){}
      this.termWorkspaces = merged;

      const remoteByName = new Map(remoteWs.map(w => [w && w.name, w]).filter(([n]) => n));
      byName.forEach((ws, name) => {
        const r = remoteByName.get(name);
        const wsAt = Number(ws.savedAt) || 0;
        const rAt  = r ? (Number(r.savedAt) || 0) : -1;
        if (!r || wsAt > rAt) pushPlan.workspaces.push(ws);
      });
      tombByName.forEach((deletedAt, name) => {
        const remoteHas = remoteByName.has(name);
        const remoteTombHas = remoteTomb.some(t => t && t.name === name && (Number(t.deletedAt) || 0) >= deletedAt);
        if (remoteHas || !remoteTombHas) pushPlan.deletions.push(name);
      });

      return pushPlan;
    },

    async _termExecutePushPlan(plan){
      if (!plan) return;
      Object.entries(plan.snapshots || {}).forEach(([ns, snap]) => {
        this._termPushSnapshotNow(ns, snap);
      });
      for (const ws of (plan.workspaces || [])) {
        await this._termPushWorkspace(ws);
      }
      for (const name of (plan.deletions || [])) {
        await this._termDeleteWorkspace(name);
      }
    },

    _termPushSnapshotNow(namespace, snapshot){
      if (!this.token || !namespace) return;
      const timers = this._termSync._debounce;
      if (timers[namespace]) { clearTimeout(timers[namespace]); timers[namespace] = null; }
      this._termSync.status = 'syncing';
      fetch('/api/terminal/snapshot/' + encodeURIComponent(namespace), {
        method: 'PUT',
        headers: this._termAuthHeaders(),
        body: JSON.stringify(snapshot || {}),
      }).then(r => {
        if (!r.ok) throw new Error('HTTP '+r.status);
        this._termSync.status = 'idle';
        this._termSync.lastErr = '';
      }).catch(e => {
        this._termSync.status = 'error';
        this._termSync.lastErr = String(e);
        console.warn('[term-sync] migration push failed:', namespace, e);
      });
    },

    async _termInitialSync(){
      this._termBackfillSavedAt();
      const remote = await this._termPullRemote();
      const plan = this._termReconcile(remote);
      this._termExecutePushPlan(plan);
    },

    async _termManualRefresh(){
      const remote = await this._termPullRemote();
      if (!remote) {
        this.showToast('Sync failed (network or auth)', 'err');
        return;
      }
      const plan = this._termReconcile(remote);
      this._termExecutePushPlan(plan);
      this.showToast('Workspaces synced ('+(this.termWorkspaces||[]).length+')', 'ok');
    },
    async _termFetchRetry(url, opts, label){
      const delays = [600, 1800, 5000];
      let lastErr = null;
      for (let attempt = 0; attempt <= delays.length; attempt++) {
        try {
          const r = await fetch(url, opts);
          if (r.ok) return r;
          if (r.status >= 400 && r.status < 500) return r;
          lastErr = new Error('HTTP '+r.status);
        } catch (e) {
          lastErr = e;
        }
        if (attempt < delays.length) {
          console.warn('[term-sync]', label, 'retry', attempt+1, 'in', delays[attempt], 'ms');
          await new Promise(res => setTimeout(res, delays[attempt]));
        }
      }
      throw lastErr || new Error('retry exhausted');
    },

    _termPushSnapshotDebounced(namespace, snapshot){
      if (!this.token || !namespace) return;
      const timers = this._termSync._debounce;
      if (timers[namespace]) { clearTimeout(timers[namespace]); }
      timers[namespace] = setTimeout(async () => {
        timers[namespace] = null;
        this._termSync.status = 'syncing';
        try {
          const r = await this._termFetchRetry('/api/terminal/snapshot/' + encodeURIComponent(namespace), {
            method: 'PUT',
            headers: this._termAuthHeaders(),
            body: JSON.stringify(snapshot || {}),
          }, 'push snapshot '+namespace);
          if (!r.ok) throw new Error('HTTP '+r.status);
          this._termSync.status = 'idle';
          this._termSync.lastErr = '';
        } catch (e) {
          this._termSync.status = 'error';
          this._termSync.lastErr = String(e);
          console.warn('[term-sync] push snapshot failed (after retries):', namespace, e);
        }
      }, 800);
    },
    _termFlushAllOnUnload(){
      if (!this.token) return;
      const refs = {
        claude:  this._termsClaude,
      };
      Object.entries(refs).forEach(([ns, termsRef]) => {
        if (!termsRef) return;
        const snap = {
          v: 2,
          panes: (termsRef.panes||[]).map(p => ({
            id: p.id, sessionName: p.sessionName,
            startupCmd: p.startupCmd || '',
            aiProvider: p.aiProvider || 'oauth',
          })),
          layout: this._serializeLayout(termsRef.layout),
          activePane: termsRef.activePane || null,
          paneSeq: termsRef._paneSeq,
          savedAt: Date.now(),
        };
        try {
          fetch('/api/terminal/snapshot/' + ns, {
            method: 'PUT', keepalive: true,
            headers: this._termAuthHeaders(),
            body: JSON.stringify(snap),
          }).catch(()=>{});
        } catch(_){}
      });
    },
    async _termPushWorkspace(ws){
      if (!this.token || !ws || !ws.name) return false;
      try {
        this._termSync.status = 'syncing';
        const r = await this._termFetchRetry('/api/terminal/workspace/' + encodeURIComponent(ws.name), {
          method: 'PUT',
          headers: this._termAuthHeaders(),
          body: JSON.stringify(ws),
        }, 'push workspace '+ws.name);
        if (r.status === 409) {
          this._termSync.status = 'idle';
          return true;
        }
        if (!r.ok) throw new Error('HTTP '+r.status);
        this._termSync.status = 'idle';
        this._termSync.lastErr = '';
        return true;
      } catch (e) {
        this._termSync.status = 'error';
        this._termSync.lastErr = String(e);
        console.warn('[term-sync] push workspace failed (after retries):', ws.name, e);
        return false;
      }
    },
    async _termDeleteWorkspace(name){
      if (!this.token || !name) return false;
      try {
        this._termSync.status = 'syncing';
        const r = await this._termFetchRetry('/api/terminal/workspace/' + encodeURIComponent(name), {
          method: 'DELETE',
          headers: { 'Authorization': 'Bearer ' + this.token },
        }, 'delete workspace '+name);
        if (!r.ok && r.status !== 404) throw new Error('HTTP '+r.status);
        this._termSync.status = 'idle';
        this._termSync.lastErr = '';
        return true;
      } catch (e) {
        this._termSync.status = 'error';
        this._termSync.lastErr = String(e);
        console.warn('[term-sync] delete workspace failed (after retries):', name, e);
        return false;
      }
    },
    saveState(){
      try {
        if (!this.terms) return;
        const snap = {
          v: 2,
          panes: (this.terms.panes||[]).map(p => ({
            id: p.id,
            sessionName: p.sessionName,
            startupCmd: p.startupCmd || '',
            aiProvider: p.aiProvider || 'oauth',
          })),
          layout: this._serializeLayout(this.terms.layout),
          activePane: this.terms.activePane || null,
          paneSeq: this.terms._paneSeq,
          savedAt: Date.now(),
        };
        const key = this._termsStorageKey();
        localStorage.setItem(key, JSON.stringify(snap));
        try { this._termPushSnapshotDebounced(this._termNamespaceOf(this.terms), snap); } catch(_){}
      } catch(e){ console.warn('[panes] saveState error:', e); }
    },
    _serializeLayout(node){
      if (!node) return null;
      if (node.type === 'pane') return { type: 'pane', id: node.id };
      return { type: 'split', dir: node.dir, size: node.size,
               a: this._serializeLayout(node.a), b: this._serializeLayout(node.b) };
    },
    restoreState(){
      const orig = this.terms;
      try {
        this.terms = this._termsClaude;
        this._restoreStateInner('panel_tabs_snapshot');
      } catch (e) {
        console.warn('[panes] restoreState Claude failed, purging:', e);
        try { localStorage.removeItem('panel_tabs_snapshot'); } catch(_) {}
        this._termsClaude.panes = []; this._termsClaude.layout = null; this._termsClaude.activePane = null;
      }
      this.terms = orig || this._termsClaude;
      try { this._jobLogRestoreLoad(); } catch(_){}
    },
    _restoreStateInner(storageKey){
      storageKey = storageKey || 'panel_tabs_snapshot';
      let snap = null;
      try { snap = JSON.parse(localStorage.getItem(storageKey) || 'null'); } catch(e){}
      this.terms.panes = [];
      this.terms.layout = null;
      this.terms.activePane = null;
      if (!snap) return;
      if (snap.v !== undefined && snap.v !== 2 && !Array.isArray(snap.tabs) && !Array.isArray(snap.names)) {
        try { localStorage.removeItem('panel_tabs_snapshot'); } catch(_) {}
        return;
      }
      this.terms._paneSeq = Math.max(this.terms._paneSeq || 0, snap.paneSeq || 0);

      if (Array.isArray(snap.tabs) || Array.isArray(snap.names)) {
        const flatPaneDescriptors = [];
        if (Array.isArray(snap.names)) {
          const seen = new Set();
          snap.names.forEach(name => {
            if (seen.has(name)) return;
            seen.add(name);
            flatPaneDescriptors.push({ sessionName: name, startupCmd: '' });
          });
        } else {
          snap.tabs.forEach(savedTab => {
            const layoutPanes = this._allPanesFromSerialized(savedTab.layout);
            const byId = new Map((savedTab.panes||[]).map(p => [p.id, p]));
            const seen = new Set();
            layoutPanes.forEach(pid => {
              const p = byId.get(pid);
              if (p && !seen.has(pid)) {
                seen.add(pid);
                flatPaneDescriptors.push({ sessionName: p.sessionName, startupCmd: '' });
              }
            });
            (savedTab.panes||[]).forEach(p => {
              if (!seen.has(p.id)) {
                flatPaneDescriptors.push({ sessionName: p.sessionName, startupCmd: '' });
              }
            });
          });
        }
        this._hydrateFromDescriptors(flatPaneDescriptors);
        return;
      }

      if (!Array.isArray(snap.panes) || snap.panes.length === 0) {
        return;
      }
      const MAX_PANES = 24;
      const inputPanes = (snap.panes || []).slice(0, MAX_PANES);
      const seenIds = new Set();
      const idMap = {};
      inputPanes.forEach(p => {
        if (!p || !p.id || seenIds.has(p.id)) return;
        seenIds.add(p.id);
        const pane = this._makePane(p.sessionName, '', p.aiProvider);
        idMap[p.id] = pane.id;
        this.terms.panes.push(pane);
      });
      const rewriteLayout = (node) => {
        if (!node) return null;
        if (node.type === 'pane') {
          const newId = idMap[node.id];
          return newId ? { type:'pane', id: newId } : null;
        }
        if (node.type === 'split') {
          const a = rewriteLayout(node.a);
          const b = rewriteLayout(node.b);
          if (a && b) return { type:'split', dir: node.dir, size: node.size, a, b };
          return a || b || null;
        }
        return null;
      };
      let rewritten = rewriteLayout(snap.layout);
      const countPanesInLayout = (node, set) => {
        set = set || new Set();
        if (!node) return set;
        if (node.type === 'pane') { set.add(node.id); return set; }
        if (node.type === 'split') {
          countPanesInLayout(node.a, set);
          countPanesInLayout(node.b, set);
        }
        return set;
      };
      const inLayout = countPanesInLayout(rewritten);
      const missing = this.terms.panes.filter(p => !inLayout.has(p.id));
      if (missing.length > 0) {
        let layout = rewritten;
        missing.forEach(p => {
          const node = { type:'pane', id: p.id };
          if (!layout) layout = node;
          else layout = { type:'split', dir:'row', size:0.5, a: layout, b: node };
        });
        rewritten = layout;
      }
      this.terms.layout = rewritten;
      this.terms.activePane = (idMap[snap.activePane] || this.terms.panes[0]?.id || null);
      if ((snap.panes||[]).length !== this.terms.panes.length) {
        console.warn('[panes] snapshot had', (snap.panes||[]).length, 'but restored only', this.terms.panes.length, '— check for dup IDs');
      }
      this.saveState();
      this.$nextTick(()=>{
        this.renderPaneLayout();
        this.terms.panes.forEach(p => this.mountPane(p));
      });
    },
    _allPanesFromSerialized(node, out){
      out = out || [];
      if (!node) return out;
      if (node.type === 'pane') out.push(node.id);
      else { this._allPanesFromSerialized(node.a, out); this._allPanesFromSerialized(node.b, out); }
      return out;
    },
    _hydrateFromDescriptors(descriptors){
      if (!descriptors || descriptors.length === 0) return;
      const panes = descriptors.map(d => this._makePane(d.sessionName, d.startupCmd || '', d.aiProvider));
      this.terms.panes = panes;
      const buildChain = (arr) => {
        if (arr.length === 1) return { type:'pane', id: arr[0].id };
        return { type:'split', dir:'row', size: 1.0 / arr.length,
                 a: { type:'pane', id: arr[0].id },
                 b: buildChain(arr.slice(1)) };
      };
      this.terms.layout = buildChain(panes);
      this.terms.activePane = panes[0].id;
      this.$nextTick(()=>{
        this.renderPaneLayout();
        panes.forEach(p => this.mountPane(p));
      });
      this.saveState();
    },
    installGlobalHotkeys(){
      let gPrefix = false;
      const isTyping = () => {
        const el = document.activeElement;
        if (!el) return false;
        const tag = el.tagName;
        return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.closest('.xterm');
      };
      window.addEventListener('keydown', (ev) => {
        if ((ev.ctrlKey||ev.metaKey) && ev.shiftKey && (ev.key==='h'||ev.key==='H')) {
          ev.preventDefault();
          this.toggleChromeCollapsed();
          return;
        }
        if ((ev.ctrlKey||ev.metaKey) && ev.shiftKey && (ev.key==='m'||ev.key==='M')) {
          ev.preventDefault();
          if (this.stt.session) { this.sttStop(); return; }
          const active = document.activeElement;
          if (active && (active.tagName === 'TEXTAREA' || active.tagName === 'INPUT')) {
            const id = active.id || ('stt-target-' + Date.now());
            if (!active.id) active.id = id;
            this._sttStartForDOM(active);
            return;
          }
          this.showToast('Focus a text field before dictating', 'warn');
          return;
        }
        if (ev.ctrlKey && ev.shiftKey && (ev.key === '!' || ev.code === 'Digit1')) {
          ev.preventDefault();
          this.shortcutsOpen = true;
          return;
        }
        if (ev.key==='Escape') {
          if (this.videocall && this.videocall.dropOverlay) { this.vcDropOverlayHide(); return; }
          if (this.palette.open) { this.closePalette(); return; }
          if (this.shortcutsOpen) { this.shortcutsOpen=false; return; }
          if (this.focusMode) { this.focusMode = false; this.$nextTick(()=>{ const p=this.activePane(); if(p) this._fitSoon(p.fit); }); return; }
        }
        if (isTyping()) return;
        if ((ev.ctrlKey||ev.metaKey) && !ev.shiftKey && (ev.key==='p'||ev.key==='P')) {
          ev.preventDefault(); this.openPalette({ focus: 'terminal' }); return;
        }
        if ((ev.ctrlKey||ev.metaKey) && !ev.shiftKey && (ev.key==='k'||ev.key==='K')) {
          ev.preventDefault(); this.openPalette(); return;
        }
        if ((ev.ctrlKey||ev.metaKey) && (ev.key==='/' )) {
          ev.preventDefault(); this.openPalette(); return;
        }
        if (ev.key === 'g' && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
          gPrefix = true; setTimeout(()=>{ gPrefix = false; }, 800); ev.preventDefault(); return;
        }
        if (gPrefix) {
          gPrefix = false;
          const map = {
            'd':'dashboard', 't':'terminal', 'c':'containers', 'f':'files',
            'h':'history',   'a':'audit',    'l':'alerts',     's':'systemd',
            'i':'images',    'v':'volumes',  'n':'networks',   'r':'config',
            'j':'jobs',      'm':'maintenance', 'e':'schedules',
            'p':'deploy',    'u':'users',      'w':'whatsapp', 'b':'browser',
          };
          const target = map[ev.key.toLowerCase()];
          if (target) { ev.preventDefault(); this.setPage(target); }
          return;
        }
      });
    },
    openPalette(opts){
      this.palette.open = true; this.palette.selected = 0;
      this.palette.query = (opts && opts.focus === 'terminal') ? 'session' : '';
      try { this.loadAbandonedSessions(); } catch(e){}
      this.$nextTick(()=>{ const i = document.getElementById('palette-input'); if (i) { i.focus(); i.select && i.select(); }});
    },
    closePalette(){ this.palette.open = false; },
    _norm(s){ return (s||'').normalize('NFD').replace(/[\u0300-\u036f]/g,'').toLowerCase().trim(); },
    pushRecentView(view){
      if (!view || !this.VIEW_TITLES[view]) return;
      this.recentViews = [view, ...this.recentViews.filter(v => v !== view)].slice(0, 8);
      try { localStorage.setItem('panel_recent_views', JSON.stringify(this.recentViews)); } catch(_){}
    },
    getPaletteItems(){
      const q = this._norm(this.palette.query);
      const pages   = (this.palette.pages||[]).map(p => ({ it:p, m:this._norm(p.label+' '+(p.kw||'')) }));
      const actions = (this.palette.actions||[]).map(a => ({ it:a, m:this._norm(a.label+' '+(a.kw||'')) }));
      const recents = (this.recentViews||[]).map(v => {
        const label = this.VIEW_TITLES[v] || v;
        return { it:{label:'go to '+label, kind:'recent', page:v}, m:this._norm('recent '+label+' '+v) };
      });
      const conts = [];
      (this.containers||[]).forEach(c => {
        const name = this.contName(c);
        const base = this._norm('container '+name+' '+(c.Image||'')+' '+(c.State||''));
        conts.push({ it:{label:'container · '+name+' ('+(c.State||'?')+')', kind:'cont', cid:c.Id, name}, m:base });
        conts.push({ it:{label:'container · '+name+' — logs (live)', kind:'logs', cid:c.Id, name}, m:base+' logs journal' });
        if ((c.State||'') === 'running') {
          conts.push({ it:{label:'container · '+name+' — restart', kind:'cont', cid:c.Id, name, act:'restart'}, m:base+' restart reboot' });
          conts.push({ it:{label:'container · '+name+' — stop', kind:'cont', cid:c.Id, name, act:'stop'}, m:base+' stop halt' });
        } else {
          conts.push({ it:{label:'container · '+name+' — start', kind:'cont', cid:c.Id, name, act:'start'}, m:base+' start launch power on' });
        }
      });
      const deploys = [];
      ((this.deploy && this.deploy.apps) || []).forEach(a => {
        const base = this._norm('deploy '+a.name);
        deploys.push({ it:{label:'deploy · '+a.name+' — publish', kind:'deploy', app:a.name, act:'run'}, m:base+' deploy publish release ship' });
        deploys.push({ it:{label:'deploy · '+a.name+' — rollback', kind:'deploy', app:a.name, act:'rollback'}, m:base+' rollback revert undo' });
      });
      const snippets = (this.userSnippets||[]).map(s => ({ it:{label:'snippet · '+s.name+' — '+s.cmd, kind:'snippet', cmd:s.cmd}, m:this._norm('snippet '+s.name+' '+s.cmd) }));
      const panes    = (this.terms.panes||[]).map(p => ({ it:{label:'pane · '+p.sessionName, kind:'pane', paneId:p.id}, m:this._norm('pane '+p.sessionName) }));
      const sessions = (this.abandonedSessions||[]).map(s => ({ it:{label:'session · '+s.name, kind:'session', sessionName:s.name}, m:this._norm('session '+s.name) }));

      const byId = new Map();
      pages.forEach(x => byId.set('page:'+x.it.page, x));
      actions.forEach(x => { if (x.it.do) byId.set('do:'+x.it.do, x); });
      const validPins = (this.palettePins||[]).filter(id => byId.has(id));
      const pinnedSet = new Set(validPins);
      const pinned    = validPins.map(id => byId.get(id));
      const notPinned = x => !pinnedSet.has(this.paletteItemId(x.it));
      const pagesNP   = pages.filter(notPinned);
      const actionsNP = actions.filter(notPinned);

      if (q) {
        const all = [...pinned, ...recents, ...pagesNP, ...actionsNP, ...conts, ...deploys, ...snippets, ...panes, ...sessions];
        return all.filter(x => x.m.includes(q)).slice(0, 50).map(x => x.it);
      }
      const CAP = 50;
      const out = [];
      const take = (arr, n) => { for (const x of arr) { if (out.length >= CAP || n <= 0) break; out.push(x.it); n--; } };
      take(pinned, 8);
      take(recents, 6);
      take(conts, 6);
      take(deploys, 4);
      take(sessions, 4);
      take(panes, 3);
      take(snippets, 3);
      take(pagesNP, CAP);
      take(actionsNP, CAP);
      return out;
    },
    runPaletteItem(it){
      if (!it) return;
      this.closePalette();
      if (it.kind === 'page')     { this.setPage(it.page); return; }
      if (it.kind === 'action')   { const fn = this[it.do]; if (typeof fn === 'function') fn.call(this); return; }
      if (it.kind === 'recent')   { this.setPage(it.page); return; }
      if (it.kind === 'cont')     { const c = this.containers.find(x => x.Id === it.cid); if (!c) return; if (it.act) this.containerAction(c, it.act); else this.openContainer(c); return; }
      if (it.kind === 'logs')     { const c = this.containers.find(x => x.Id === it.cid); if (c) { this.openContainer(c); this.detail.tab='logs'; this.$nextTick(()=>this.openDetailLogStream()); } return; }
      if (it.kind === 'deploy')   { if (it.act === 'rollback') { const a = (this.deploy.apps||[]).find(x => x.name === it.app); if (a) this.deployRollback(a); } else { this.deployRun(it.app); } return; }
      if (it.kind === 'snippet')  { this.setPage('terminal'); this.$nextTick(()=>this.runSnippet(it.cmd)); return; }
      if (it.kind === 'pane')     { this.setPage('terminal'); this.$nextTick(()=>this.focusPane(it.paneId)); return; }
      if (it.kind === 'session')  { this.reattachSession(it.sessionName); this.setPage('terminal'); return; }
    },
    paletteItemId(it){
      if (!it) return null;
      if (it.kind === 'page'   && it.page) return 'page:'+it.page;
      if (it.kind === 'action' && it.do)   return 'do:'+it.do;
      return null;
    },
    isPinned(id){ return !!id && (this.palettePins||[]).includes(id); },
    togglePin(id){
      if (!id) return;
      const cur = this.palettePins || [];
      this.palettePins = cur.includes(id) ? cur.filter(x => x !== id) : [...cur, id];
      this._prefPersist('panel_palette_pins', JSON.stringify(this.palettePins));
      this.palette.selected = 0;
    },
    paletteOpenSecurityMFA(){
      this.setPage('config');
      const go = () => {
        const el = document.getElementById('sec-mfa');
        if (el) { el.scrollIntoView({ behavior:'smooth', block:'center' }); return true; }
        return false;
      };
      this.$nextTick(() => { if (!go()) setTimeout(go, 250); });
    },
    reloadPage(){ location.reload(); },

    _paletteTermDo(fn){
      this.setPage('terminal');
      this.$nextTick(() => {
        const p = this.activePane();
        if (!p) { this.showToast && this.showToast('no active panel', 'err'); return; }
        fn.call(this, p);
      });
    },
    _paletteSendKey(p, data){
      if (p && p.ws && p.ws.readyState===1) p.ws.send(JSON.stringify({type:'input', data}));
    },
    paletteTermNewPane()        { this.setPage('terminal'); this.$nextTick(()=>this.newPane()); },
    paletteTermClear()          { this._paletteTermDo(p => { try{ p.term.clear(); }catch(_){} }); },
    paletteTermReset()          { this._paletteTermDo(p => this._termResetState(p)); },
    paletteTermSelectAll()      { this._paletteTermDo(p => { try{ p.term.selectAll(); }catch(_){} }); },
    paletteTermSaveScrollback() { this._paletteTermDo(p => this._termSaveScrollback(p)); },
    paletteTermSearch()         { this._paletteTermDo(_ => this.hostTermSearch()); },
    paletteTermSigInt()         { this._paletteTermDo(p => this._paletteSendKey(p, '\x03')); },
    paletteTermSigEOF()         { this._paletteTermDo(p => this._paletteSendKey(p, '\x04')); },
    paletteTermSigSusp()        { this._paletteTermDo(p => this._paletteSendKey(p, '\x1a')); },
    paletteTermDetach()         { this._paletteTermDo(p => { this.closePane(p.id); this.showToast && this.showToast('panel closed — the session stays alive (dtach)', 'ok'); }); },
    paletteTermSplitH()         { this._paletteTermDo(p => this.splitPane(p.id, 'column')); },
    paletteTermSplitV()         { this._paletteTermDo(p => this.splitPane(p.id, 'row')); },
    paletteTermBroadcast()      { this.terms.broadcast = !this.terms.broadcast; this.showToast && this.showToast('broadcast '+(this.terms.broadcast?'ON':'OFF'), 'ok'); },
    paletteTermJumpNext()       { this._paletteTermDo(p => this._termJumpMark(p, +1)); },
    paletteTermJumpPrev()       { this._paletteTermDo(p => this._termJumpMark(p, -1)); },
    paletteTermReconnect()      { this._paletteTermDo(_ => this.hostTermReconnectNow()); },

    async loadPanelHealth(){
      this.panelHealthLoading = true;
      try { const r = await this.api('/api/panel/health'); this.panelHealth = await r.json(); this._loadOk('panelHealth'); }
      catch(e){ this._loadErr('panelHealth', e, 'failed to read the server-control-panel health'); }
      finally { this.panelHealthLoading = false; }
    },
    panelHealthUptime(){
      const s = this.panelHealth?.uptime_seconds || 0;
      return this.uptime(s);
    },
    tlsExpiryLabel(){
      const h = this.panelHealth;
      if (!h || !h.tls_enabled) return 'off';
      if (h.tls_mode === 'letsencrypt') return "Let's Encrypt · " + (h.tls_domain||'');
      if (h.tls_expires_in_days != null) {
        const d = h.tls_expires_in_days;
        if (d <= 0) return 'expired';
        if (d <= 14) return d + ' days';
        return d + ' days';
      }
      return h.tls_mode || 'on';
    },
    tlsExpiryClass(){
      const h = this.panelHealth;
      if (!h || !h.tls_enabled) return 'text-rose-400';
      if (h.tls_expires_in_days != null) {
        if (h.tls_expires_in_days <= 0) return 'text-rose-400';
        if (h.tls_expires_in_days <= 14) return 'text-amber-400';
      }
      return 'text-emerald-400';
    },

    addNotification(kind, text){
      this.notifications = [{kind, text, t: Math.floor(Date.now()/1000)}, ...this.notifications].slice(0, 50);
    },
    unreadNotifications(){
      return this.notifications.filter(n => n.t > this.lastSeenNotification).length;
    },
    clearNotifications(){
      this.notifications = []; this.bellOpen = false;
      this.lastSeenNotification = Math.floor(Date.now()/1000);
      localStorage.setItem('panel_last_seen_notif', String(this.lastSeenNotification));
    },
    async loadSystemLogs(){
      this.systemLogsLoading = true;
      try { const r = await this.api('/api/system/logs'); this.systemLogs = (await r.json()) || []; this._loadOk('systemLogs'); }
      catch(e){ this._loadErr('systemLogs', e, 'failed to list the system logs'); }
      finally { this.systemLogsLoading = false; }
    },
    openLogTail(path){
      this.closeLogTail();
      this.logTail = { path, ws: null, buffer: '', connected: false };
      const proto = location.protocol==='https:'?'wss:':'ws:';
      const url = proto+'//'+location.host+'/ws/system/log-tail?path='+encodeURIComponent(path);
      const ctl = this._wsWithRetry(url, {
        onopen: () => { this.logTail.connected = true; this.logTail.buffer = ''; },
        onmessage: (ev) => {
          this.logTail.buffer += ev.data;
          if (this.logTail.buffer.length > 50000) this.logTail.buffer = this.logTail.buffer.slice(-50000);
        },
        onstatus: (s) => { if (s !== 'open') this.logTail.connected = false; },
        shouldReconnect: () => this.logTail.ws === ctl,
      });
      this.logTail.ws = ctl;
    },
    closeLogTail(){
      if (this.logTail.ws) { try{ this.logTail.ws.close(); }catch(e){} }
      this.logTail = { path:'', ws:null, buffer:'', connected:false };
    },

    async loadUFW(){
      this.ufwLoading = true;
      try { const r = await this.api('/api/system/ufw'); const d = await r.json(); this.ufw = Object.assign(this.ufw, d); this._loadOk('ufw'); }
      catch(e){ this._loadErr('ufw', e, 'failed to read the ufw status'); }
      finally { this.ufwLoading = false; }
    },
    ufwParsedRules(text){
      const out = {};
      String(text == null ? '' : text).split('\n').forEach(line => {
        const m = line.match(/^\s*\[\s*(\d+)\s*\]\s*(\S.*?)\s*$/);
        if (m) out[m[1]] = m[2].replace(/\s+/g, ' ');
      });
      return out;
    },
    async ufwRule(action, spec){
      if (!action) return;
      if (this.ufwBusy) return;
      spec = String(spec == null ? '' : spec).trim();
      if (action === 'delete' && !spec) { this.showToast('enter the number (or the text) of the rule to delete','err'); return; }

      if (action === 'delete' && /^\d+$/.test(spec)) {
        const shown = this.ufwParsedRules(this.ufw.output);
        const before = shown[spec];
        if (!before) {
          this.showToast('rule '+spec+' does not exist in the current list — reload', 'err');
          await this.loadUFW();
          return;
        }
        if (!(await this.confirmAsync(
          'Delete rule '+spec+' from the firewall?\n\n['+spec+'] '+before+'\n\nIf that is not the rule you meant, cancel and reload the list. Deleted firewall rules do not come back on their own — deleting the wrong one can lock you out of the server.',
          { danger: true }))) return;

        this.ufwBusy = true;
        try {
          const rs = await this.api('/api/system/ufw');
          const ds = await rs.json();
          const now = this.ufwParsedRules(ds && ds.output);
          if (now[spec] !== before) {
            this.ufw = Object.assign(this.ufw, ds);
            this.showToast('the list changed — rule '+spec+' is now a different one. Nothing was deleted.', 'err');
            return;
          }
        } catch(e){ this.showToast(e.message,'err'); return; }
        finally { this.ufwBusy = false; }
      } else if (action === 'delete') {
        if (!(await this.confirmAsync(
          'Delete the rule "'+spec+'" from the firewall?\n\nDeleted firewall rules do not come back on their own — deleting the wrong one can lock you out of the server.',
          { danger: true }))) return;
      }

      this.ufwBusy = true;
      try {
        const r = await this.api('/api/system/ufw-rule', {method:'POST', body: JSON.stringify({action, spec})});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); return; }
        this.showToast('ufw '+action+': '+(d.status||'ok'), 'ok');
        this.ufw.newSpec = '';
        await this.loadUFW();
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.ufwBusy = false; }
    },

    async loadCron(){
      this.cron.loading = true; this.cron.error = '';
      try {
        const r = await this.api('/api/system/cron');
        const d = await r.json();
        const content = d.content || '';
        this.cron.content = content;
        this.cron.serverContent = content;
        this.cron.loaded = true;
      } catch(e){
        this.cron.loaded = false;
        this.cron.serverContent = null;
        if (e && e.message === 'unauthorized') { this.cron.error = ''; }
        else {
          this.cron.error = (e && e.message) || 'failed to read the crontab';
          console.warn('[cron] load:', e);
        }
      } finally { this.cron.loading = false; }
    },
    cronInsert(text){
      this.cron.content += (this.cron.content.endsWith('\n') || !this.cron.content ? '' : '\n') + text;
    },
    async saveCron(){
      if (!this.cron.loaded) {
        this.showToast('could not read the current crontab — reload before saving', 'err');
        return;
      }
      const fresh = String(this.cron.content || '');
      const old = String(this.cron.serverContent || '');
      if (!fresh.trim() && old.trim()) {
        const lines = old.split('\n').filter(l => l.trim() && !l.trim().startsWith('#')).length;
        const ok = await this.confirmAsync(
          'Delete the WHOLE root crontab?\n\nYou are saving empty content over '
          + lines + ' active cron line(s). Every scheduled root task will be removed.',
          { title: 'Delete the whole crontab?', danger: true }
        );
        if (!ok) return;
      }
      this.cron.saving = true;
      try {
        const r = await this.api('/api/system/cron', {method:'POST', body: JSON.stringify({content: fresh})});
        const d = await r.json();
        if (d.error) { this.showToast('crontab rejected: '+d.error, 'err'); return; }
        this.cron.serverContent = fresh;
        this.showToast('crontab saved','ok');
      } catch(e){ this.showToast('error saving the crontab: '+e.message,'err'); }
      finally { this.cron.saving = false; }
    },

    composeTemplate(kind){
      const templates = {
        nginx: `services:\n  web:\n    image: nginx:alpine\n    ports:\n      - "8080:80"\n    restart: unless-stopped\n`,
        postgres: `services:\n  db:\n    image: postgres:16-alpine\n    environment:\n      POSTGRES_PASSWORD: changeme\n      POSTGRES_USER: app\n      POSTGRES_DB: app\n    volumes:\n      - dbdata:/var/lib/postgresql/data\n    restart: unless-stopped\nvolumes:\n  dbdata:\n`,
        redis: `services:\n  cache:\n    image: redis:7-alpine\n    ports:\n      - "6379:6379"\n    restart: unless-stopped\n`,
      };
      if (templates[kind]) this.composeForm.content = templates[kind];
    },
    async composeCreate(){
      const f = this.composeForm;
      if (!f.project_name || !f.content) { this.showToast('name and compose.yml are required','err'); return; }
      try {
        const r = await this.api('/api/docker/compose/create', {method:'POST', body: JSON.stringify(f)});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); return; }
        this.showToast('compose created at '+d.path,'ok');
        this.composeWizardOpen = false;
        this.composeForm = { project_name:'', dir:'', content:'' };
        await this.loadCompose();
      } catch(e){ this.showToast(e.message,'err'); }
    },

    toggleBulkSel(kind, id){
      const arr = this.bulkSel[kind];
      const i = arr.indexOf(id);
      if (i >= 0) arr.splice(i, 1); else arr.push(id);
    },
    bulkSelectAll(kind){
      if (kind === 'containers') {
        const visible = this.filteredContainers();
        this.bulkSel.containers = (this.bulkSel.containers.length === visible.length) ? [] : visible.map(c => c.Id);
      } else if (kind === 'images') {
        const visible = this.filteredImages();
        this.bulkSel.images = (this.bulkSel.images.length === visible.length) ? [] : visible.map(i => i.Id);
      } else if (kind === 'volumes') {
        const visible = this.filteredVolumes();
        this.bulkSel.volumes = (this.bulkSel.volumes.length === visible.length) ? [] : visible.map(v => v.Name);
      }
    },
    async bulkContainerAction(action){
      const ids = [...this.bulkSel.containers];
      if (ids.length === 0) return;
      const label = { start:'Start', stop:'Stop', restart:'Restart', remove:'REMOVE' }[action] || action;
      if (!(await this.confirmAsync(label + ' ' + ids.length + ' container(s)?'))) return;
      this.bulkProgress = { done:0, total: ids.length, label, running:true, fails:[] };
      let ok = 0, lastErr = '';
      await Promise.all(ids.map(async id => {
        const c = this.containers.find(x => x.Id === id);
        const name = c ? this.contName(c) : id.slice(0,12);
        try {
          const r = await this.api('/api/docker/containers/'+id+'/'+action, {method:'POST', body:'{}'});
          if (r.ok) { ok++; }
          else {
            const e = await r.json().catch(()=>({}));
            const msg = this._errText(e.error) || ('HTTP '+r.status);
            this.bulkProgress.fails.push({ id, name, error: msg }); lastErr = msg;
          }
        } catch(e){
          const msg = (e && e.message) || 'error';
          this.bulkProgress.fails.push({ id, name, error: msg }); lastErr = msg;
        } finally { this.bulkProgress.done++; }
      }));
      const fail = ids.length - ok;
      this.bulkProgress.running = false;
      this._bulkToast(label, ok, fail, lastErr);
      this.bulkSel.containers = [];
      await this.loadContainers();
    },
    async bulkImageAction(action){
      const ids = [...this.bulkSel.images];
      if (ids.length === 0) return;
      if (action !== 'remove') return;
      if (!(await this.confirmAsync('REMOVE ' + ids.length + ' image(s)? (force=1 for images in use)'))) return;
      this.bulkProgress = { done:0, total: ids.length, label:'Remove', running:true, fails:[] };
      let ok = 0, lastErr = '';
      await Promise.all(ids.map(async id => {
        const im = this.images.find(x => x.Id === id);
        const name = (im && im.RepoTags && im.RepoTags[0] && im.RepoTags[0] !== '<none>:<none>') ? im.RepoTags[0] : (id||'').replace(/^sha256:/,'').slice(0,12);
        try {
          const r = await this.api('/api/docker/images?id='+encodeURIComponent(id)+'&force=1', {method:'DELETE'});
          if (r.ok) { ok++; }
          else {
            const e = await r.json().catch(()=>({}));
            const msg = this._errText(e.error) || ('HTTP '+r.status);
            this.bulkProgress.fails.push({ id, name, error: msg }); lastErr = msg;
          }
        } catch(e){
          const msg = (e && e.message) || 'error';
          this.bulkProgress.fails.push({ id, name, error: msg }); lastErr = msg;
        } finally { this.bulkProgress.done++; }
      }));
      const fail = ids.length - ok;
      this.bulkProgress.running = false;
      this._bulkToast('Remove', ok, fail, lastErr);
      this.bulkSel.images = [];
      await this.loadImages();
    },
    bulkVolumeCopy(){
      const names = [...this.bulkSel.volumes];
      if (names.length === 0) return;
      this._pcCopy('docker volume rm ' + names.join(' '), names.length + ' name(s) copied as a command');
    },

    _ensureMonaco(){
      return new Promise((resolve, reject) => {
        if (window.monaco) { resolve(window.monaco); return; }
        if (!window.require) { reject(new Error('monaco loader did not load')); return; }
        try {
          window.require.config({ paths: { vs: '/vendor/monaco/vs' } });
          window.require(['vs/editor/editor.main'], () => resolve(window.monaco));
        } catch(e) { reject(e); }
      });
    },
    _langForPath(path){
      const ext = (path.split('.').pop() || '').toLowerCase();
      const map = {
        'yml':'yaml','yaml':'yaml','json':'json','md':'markdown',
        'sh':'shell','bash':'shell','zsh':'shell',
        'js':'javascript','ts':'typescript','py':'python','go':'go',
        'rs':'rust','rb':'ruby','php':'php','java':'java',
        'html':'html','css':'css','scss':'scss',
        'xml':'xml','toml':'ini','ini':'ini','conf':'ini','cfg':'ini',
        'sql':'sql','dockerfile':'dockerfile',
      };
      const base = path.split('/').pop().toLowerCase();
      if (base === 'dockerfile' || base.startsWith('dockerfile.')) return 'dockerfile';
      if (base === 'makefile') return 'makefile';
      if (base.startsWith('.bashrc') || base.startsWith('.zshrc')) return 'shell';
      return map[ext] || 'plaintext';
    },
    async openEditor(path){
      this.editor.path = path;
      this.editor.language = this._langForPath(path);
      this.editor.dirty = false;
      this.editor.loaded = false;
      this.editor.saving = false;
      this.editor.open = true;
      let monaco;
      try { monaco = await this._ensureMonaco(); }
      catch(e) { this.showToast('Monaco failed: '+e.message,'err'); this.closeEditor(); return; }
      let content = '';
      try {
        const r = await this.api('/api/files/read?path=' + encodeURIComponent(path));
        if (!r.ok) {
          const t = await r.text();
          this.showToast('read failed: '+t,'err'); this.closeEditor(); return;
        }
        content = await r.text();
      } catch(e) { this.showToast(e.message,'err'); this.closeEditor(); return; }
      this.editor._initial = content;

      this.$nextTick(()=>{
        const el = document.getElementById('monaco-editor');
        if (!el) return;
        if (this.editor._inst) { try{ this.editor._inst.dispose(); }catch(e){} }
        this.editor._model = monaco.editor.createModel(content, this.editor.language);
        this.editor._inst = monaco.editor.create(el, {
          model: this.editor._model,
          theme: 'vs-dark',
          fontSize: 13,
          minimap: { enabled: true },
          automaticLayout: true,
          tabSize: 2,
          insertSpaces: true,
        });
        this.editor._model.onDidChangeContent(() => {
          this.editor.dirty = (this.editor._model.getValue() !== this.editor._initial);
        });
        this.editor._inst.addCommand(
          monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
          () => this.saveEditor()
        );
        this.editor.loaded = true;
      });
    },
    async saveEditor(){
      if (!this.editor._model || !this.editor.dirty || this.editor.saving) return;
      this.editor.saving = true;
      const content = this.editor._model.getValue();
      try {
        const r = await this.api('/api/files/write', {
          method:'POST',
          body: JSON.stringify({ path: this.editor.path, content })
        });
        if (!r.ok) {
          const t = await r.text();
          throw new Error(t);
        }
        this.editor._initial = content;
        this.editor.dirty = false;
        this.showToast('saved','ok');
      } catch(e) {
        this.showToast('error saving: '+e.message,'err');
      }
      this.editor.saving = false;
    },
    async closeEditor(){
      if (this.editor.dirty && !(await this.confirmAsync('There are unsaved changes. Discard?'))) return;
      if (this.editor._inst) { try{ this.editor._inst.dispose(); }catch(e){} }
      if (this.editor._model) { try{ this.editor._model.dispose(); }catch(e){} }
      this.editor._inst = null; this.editor._model = null;
      this.editor.open = false; this.editor.path = ''; this.editor.dirty = false; this.editor.loaded = false;
    },

    async loadSessions(){
      this.sessionsLoading = true; this.sessionsError = '';
      try {
        const r = await this.api('/api/auth/sessions');
        const d = await r.json();
        this.sessions = Array.isArray(d) ? d : (d && Array.isArray(d.sessions) ? d.sessions : []);
        this.sessionsLoaded = true;
      } catch(e){
        this.sessionsLoaded = false;
        if (e && e.message === 'unauthorized') { this.sessionsError = ''; }
        else {
          this.sessionsError = (e && e.message) || 'failed to load sessions';
          console.warn('[sessions] load:', e);
        }
      } finally { this.sessionsLoading = false; }
    },
    get sessionsTrulyEmpty(){ return this.sessionsLoaded && !this.sessionsError && this.sessions.length === 0; },
    async revokeSession(jti){
      if (!(await this.confirmAsync('End this session?'))) return;
      try {
        const r = await this.api('/api/auth/sessions/revoke', {method:'POST', body: JSON.stringify({jti})});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); return; }
        this.showToast('session ended','ok');
        await this.loadSessions();
      } catch(e) { this.showToast(e.message,'err'); }
    },
    async revokeAllSessions(){
      if (!this.sessionsLoaded) {
        this.showToast('could not read the active sessions — reload before ending one', 'err');
        return;
      }
      const others = this.sessions.filter(s => !s.current).length;
      if (others === 0) { this.showToast('no other active session', 'ok'); return; }
      if (!(await this.confirmAsync('End '+others+' other session(s)? This session stays.'))) return;
      try {
        const r = await this.api('/api/auth/sessions/revoke-all', {method:'POST', body:'{}'});
        const d = await r.json();
        if (d.error) { this.showToast(d.error,'err'); return; }
        this.showToast(d.revoked + ' session(s) ended','ok');
        await this.loadSessions();
      } catch(e) { this.showToast(e.message,'err'); }
    },
    async loadAdguard(){
      this.adguardLoading = true; this.adguardError = '';
      try {
        const r = await this.api('/api/adguard/status');
        this.adguard = await r.json();
        this.adguardLoaded = true;
        this._armAdguardPoll();
      } catch(e){
        this.adguardLoaded = false;
        if (e && e.message === 'unauthorized') { this.adguardError = ''; }
        else {
          this.adguardError = (e && e.message) || 'failed to load AdGuard';
          console.warn('[adguard] load:', e);
        }
      } finally { this.adguardLoading = false; }
    },
    _armAdguardPoll(){
      if (this._adguardTimer) return;
      this._adguardTimer = setInterval(async () => {
        if (this.currentView !== 'network') { clearInterval(this._adguardTimer); this._adguardTimer = null; return; }
        if (document.hidden) return;
        try { const r = await this.api('/api/adguard/status'); this.adguard = await r.json(); } catch(_){}
      }, 5000);
    },
    async toggleAdguard(enabled, durationMs=0){
      try {
        await this.api('/api/adguard/protection', {method:'POST', body: JSON.stringify({enabled, duration_ms: durationMs})});
        const msg = enabled ? 'filter on' : (durationMs > 0 ? 'filter paused' : 'filter off');
        this.showToast(msg, 'ok');
        await this.loadAdguard();
      } catch(e){ this.showToast(e.message, 'err'); }
    },
    fmtBytes(n){
      n = Number(n)||0;
      const u=['B','KB','MB','GB','TB']; let i=0;
      while(n>=1024 && i<u.length-1){ n/=1024; i++; }
      return (i===0? n : n.toFixed(1)) + ' ' + u[i];
    },
    async loadUsage(){
      try {
        const r = await this.api('/api/tunnel/usage');
        const j = await r.json();
        const m = {};
        (j.usage||[]).forEach(u => { m[u.name] = u; });
        this.deviceUsage = m;
        this._armUsagePoll();
      } catch(_){}
    },
    _armUsagePoll(){
      if (this._usageTimer) return;
      this._usageTimer = setInterval(async () => {
        if (this.currentView !== 'network') { clearInterval(this._usageTimer); this._usageTimer=null; return; }
        if (document.hidden) return;
        try { const r = await this.api('/api/tunnel/usage'); const j = await r.json(); const m={}; (j.usage||[]).forEach(u=>{m[u.name]=u;}); this.deviceUsage = m; } catch(_){}
      }, 2500);
    },
    devUsage(name){ return this.deviceUsage[name] || {rate_bps:0,total_bytes:0,active_conns:0}; },
    fmtRate(bps){ return this.fmtBytes(bps||0) + '/s'; },
    async loadTunnelDevices(){
      this.tunnelLoading = true; this.tunnelError = '';
      try {
        const r = await this.api('/api/tunnel/devices');
        const d = await r.json();
        this.tunnelDevices = Array.isArray(d.devices) ? d.devices : [];
        this.tunnelSummary = d.summary || null;
        this.tunnelLoaded = true;
        this._armTunnelPoll();
      } catch(e){
        this.tunnelLoaded = false;
        if (e && e.message === 'unauthorized') { this.tunnelError=''; }
        else { this.tunnelError = (e && e.message) || 'failed to load devices'; console.warn('[tunnel] load:', e); }
      } finally { this.tunnelLoading = false; }
    },
    _armTunnelPoll(){
      if (this._tunnelTimer) return;
      this._tunnelTimer = setInterval(async () => {
        if (this.currentView !== 'network') { clearInterval(this._tunnelTimer); this._tunnelTimer=null; return; }
        if (document.hidden || this.tunnelBusy) return;
        try { const r = await this.api('/api/tunnel/devices'); const d = await r.json(); this.tunnelDevices = d.devices||[]; this.tunnelSummary = d.summary||null; } catch(_){}
      }, 5000);
    },
    async addDevice(){
      const name = (this.tunnelNewName||'').trim();
      if (!name) { this.showToast('name the device','warn'); return; }
      this.tunnelBusy = true;
      try {
        const r = await this.api('/api/tunnel/devices', {method:'POST', body: JSON.stringify({name})});
        const d = await r.json();
        this.tunnelAddOpen = false;
        this.showToast('device created — restarting the tunnel','ok');
        await this.loadTunnelDevices();
        if (d.device) this._openLink(d.device.name, d.link, d.device.uuid);
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.tunnelBusy = false; }
    },
    async setDeviceExit(d, exit){
      if (exit === d.exit) return;
      try {
        await this.api('/api/tunnel/devices/'+encodeURIComponent(d.uuid)+'/exit', {method:'POST', body: JSON.stringify({exit})});
        this.showToast('exit for '+d.name+' → '+(exit==='home'?'home':'VPS')+' (reconnects in ~2s)','ok');
        await this.loadTunnelDevices();
      } catch(e){ this.showToast(e.message,'err'); await this.loadTunnelDevices(); }
    },
    async renameDevice(d){
      const name = prompt('New device name (lowercase, numbers and hyphen):', d.name);
      if (!name || name===d.name) return;
      try {
        await this.api('/api/tunnel/devices/'+encodeURIComponent(d.uuid)+'/rename', {method:'POST', body: JSON.stringify({name})});
        this.showToast('renamed','ok');
        await this.loadTunnelDevices();
      } catch(e){ this.showToast(e.message,'err'); }
    },
    async revokeDevice(d){
      if (!(await this.confirmAsync('Revoke "'+d.name+'"? It loses access to the tunnel (the other devices are not affected).'))) return;
      try {
        await this.api('/api/tunnel/devices/'+encodeURIComponent(d.uuid), {method:'DELETE'});
        this.showToast('device revoked','ok');
        await this.loadTunnelDevices();
      } catch(e){ this.showToast(e.message,'err'); }
    },
    async showDeviceLink(d){
      this.tunnelLinkUuid = d.uuid;
      this.tunnelLinkName = d.name;
      this.tunnelLinkVariant = 'ws';
      this.tunnelLinkOpen = true;
      await this.loadDeviceLink('ws');
    },
    async loadDeviceLink(variant){
      variant = (variant === 'reality') ? 'reality' : 'ws';
      this.tunnelLinkVariant = variant;
      const uuid = this.tunnelLinkUuid;
      if (!uuid) return;
      const qs = variant === 'reality' ? '?variant=reality' : '';
      try {
        const r = await this.api('/api/tunnel/devices/'+encodeURIComponent(uuid)+'/link'+qs);
        const j = await r.json();
        this.tunnelLinkText = j.link || '';
        this.tunnelLinkName = j.name || this.tunnelLinkName;
      } catch(e){ this.showToast(e.message,'err'); }
      if (this.tunnelQrSrc && this.tunnelQrSrc.startsWith('blob:')) { try{ URL.revokeObjectURL(this.tunnelQrSrc); }catch(_){} }
      this.tunnelQrSrc = '';
      try {
        const r = await this.api('/api/tunnel/devices/'+encodeURIComponent(uuid)+'/qr'+qs, {raw:true});
        if (r.ok) { this.tunnelQrSrc = URL.createObjectURL(await r.blob()); }
      } catch(_){}
    },
    async _openLink(name, link, uuid){
      this.tunnelLinkUuid = uuid; this.tunnelLinkName = name;
      this.tunnelLinkText = link || ''; this.tunnelLinkVariant = 'ws';
      if (this.tunnelQrSrc && this.tunnelQrSrc.startsWith('blob:')) { try{ URL.revokeObjectURL(this.tunnelQrSrc); }catch(_){} }
      this.tunnelQrSrc = '';
      this.tunnelLinkOpen = true;
      try {
        const r = await this.api('/api/tunnel/devices/'+encodeURIComponent(uuid)+'/qr', {raw:true});
        if (r.ok) { this.tunnelQrSrc = URL.createObjectURL(await r.blob()); }
      } catch(_){}
    },
    async setDeviceDatasaver(d, on){
      if (on && !confirm('Turn data saving on for "'+d.name+'" routes ALL HTTPS from this device through a compression proxy (MITM). If the data-saver CA is NOT installed ON THIS DEVICE, EVERY site stops loading.\n\nDo you confirm the CA is already installed on this device?')) return;
      try {
        await this.api('/api/tunnel/devices/'+encodeURIComponent(d.uuid)+'/datasaver', {method:'POST', body: JSON.stringify({on, ca_ack: on})});
        d.datasaver = on;
        this.showToast('savings of '+d.name+(on?' on':' off')+' (reconnects in ~2s)','ok');
        await this.loadTunnelDevices();
      } catch(e){ this.showToast(e.message,'err'); await this.loadTunnelDevices(); }
    },
    _dsShape(s){
      const o = (s && typeof s === 'object') ? s : {};
      const sv = (o.saved && typeof o.saved === 'object') ? o.saved : {};
      const num = (v) => (typeof v === 'number' && isFinite(v)) ? v : 0;
      return {
        settings: (o.settings && typeof o.settings === 'object') ? o.settings : {},
        bypass:   Array.isArray(o.bypass) ? o.bypass : [],
        has_ca:   !!o.has_ca,
        saved: {
          orig:     num(sv.orig),
          out:      num(sv.out),
          imgs:     num(sv.imgs),
          reqs_cut: num(sv.reqs_cut),
          pct:      num(sv.pct),
        },
      };
    },
    async loadDatasaver(){
      this.dsLoading = true; this.dsError = '';
      try {
        const r = await this.api('/api/datasaver/status');
        const s = await r.json();
        this.dsStatus = this._dsShape(s);
        const st = (s && s.settings) || {};
        this.dsForm = {
          enabled: st.enabled !== false,
          quality: st.quality || 40,
          maxdim: st.maxdim == null ? 1280 : st.maxdim,
          strip_trackers: st.strip_trackers !== false,
          greyscale: !!st.greyscale,
          video_low: st.video_low !== false,
        };
        this.dsBypassText = (s && Array.isArray(s.bypass)) ? s.bypass.join('\n') : '';
        this.dsLoaded = true;
        this._armDatasaverPoll();
      } catch(e){
        this.dsLoaded = false;
        if (e && e.message === 'unauthorized') { this.dsError=''; }
        else { this.dsError = (e && e.message) || 'failed to load the data saver'; console.warn('[datasaver] load:', e); }
      } finally { this.dsLoading = false; }
    },
    _armDatasaverPoll(){
      if (this._dsTimer) return;
      this._dsTimer = setInterval(async () => {
        if (this.currentView !== 'network') { clearInterval(this._dsTimer); this._dsTimer=null; return; }
        if (document.hidden) return;
        try { const r = await this.api('/api/datasaver/status'); const s = await r.json(); this.dsStatus = this._dsShape(s); } catch(_){}
      }, 6000);
    },
    async saveDatasaverSettings(){
      try {
        const body = {
          enabled: !!this.dsForm.enabled,
          quality: Number(this.dsForm.quality)||40,
          maxdim: Number(this.dsForm.maxdim)||0,
          strip_trackers: !!this.dsForm.strip_trackers,
          greyscale: !!this.dsForm.greyscale,
          video_low: !!this.dsForm.video_low,
        };
        await this.api('/api/datasaver/settings', {method:'POST', body: JSON.stringify(body)});
        this.showToast('preferences saved','ok');
        await this.loadDatasaver();
      } catch(e){ this.showToast(e.message,'err'); }
    },
    async saveDatasaverBypass(){
      this.dsBusy = true;
      try {
        const hosts = (this.dsBypassText||'').split('\n').map(s=>s.trim()).filter(s=>s && !s.startsWith('#'));
        await this.api('/api/datasaver/bypass', {method:'POST', body: JSON.stringify({hosts})});
        this.showToast('exceptions saved — restarting the proxies','ok');
        await this.loadDatasaver();
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.dsBusy = false; }
    },
    setCallEconomy(mode){
      this.callEconomy = mode;
      try { localStorage.setItem('panel_vc_quality', mode); } catch(_){}
      try {
        if (this.videocall) this.videocall.qualityMode = mode;
        if (this.videocall && this.videocall.active && typeof this.vcSetQuality === 'function') this.vcSetQuality(mode);
      } catch(_){}
      const lbl = {phone:'audio only',low:'minimum',economy:'economy',medium:'medium',high:'high'}[mode] || mode;
      this.showToast('video calls: '+lbl,'ok');
    },
    async downloadDatasaverCA(){
      try {
        const r = await this.api('/api/datasaver/ca', {raw:true});
        if (!r.ok) throw new Error('CA unavailable ('+r.status+')');
        const blob = await r.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url; a.download = 'panel-datasaver-ca.pem';
        document.body.appendChild(a); a.click(); a.remove();
        setTimeout(()=>URL.revokeObjectURL(url), 2000);
      } catch(e){ this.showToast(e.message,'err'); }
    },
    formatUA(ua){
      if (!ua) return '(no User-Agent)';
      const m = ua.match(/(Chrome|Firefox|Safari|Edge|Opera)\/[\d.]+/);
      const browser = m ? m[1] : 'browser';
      let os = 'OS?';
      if (/Windows/.test(ua)) os = 'Windows';
      else if (/Mac OS X|Macintosh/.test(ua)) os = 'macOS';
      else if (/Linux/.test(ua)) os = 'Linux';
      else if (/Android/.test(ua)) os = 'Android';
      else if (/iPhone|iPad/.test(ua)) os = 'iOS';
      return browser + ' · ' + os;
    },
    timeUntil(ts){
      if (!ts) return '—';
      const s = ts - Math.floor(Date.now()/1000);
      if (s < 0) return 'expired';
      if (s < 3600) return Math.floor(s/60) + 'min';
      if (s < 86400) return Math.floor(s/3600) + 'h';
      return Math.floor(s/86400) + 'd';
    },

    async loadAuditActions(){
      try { const r = await this.api('/api/audit/actions'); this.auditActions = (await r.json()) || []; this._loadOk('auditActions'); }
      catch(e){ this._loadErr('auditActions', e, 'failed to load the audit actions'); }
    },
    async runAuditSearch(){
      this.auditLoading = true;
      try {
        try { localStorage.setItem('panel_audit_filter', JSON.stringify(this.auditFilter)); } catch(_) {}
        const p = new URLSearchParams();
        if (this.auditFilter.user) p.set('user', this.auditFilter.user);
        if (this.auditFilter.action) p.set('action', this.auditFilter.action);
        if (this.auditFilter.q) p.set('q', this.auditFilter.q);
        if (this.auditFilter.from) p.set('from', String(this._toUnix(this.auditFilter.from)));
        if (this.auditFilter.to) p.set('to', String(this._toUnix(this.auditFilter.to)));
        p.set('limit', String(this.auditFilter.limit || 200));
        const r = await this.api('/api/audit/search?' + p.toString());
        this.auditResults = (await r.json()) || [];
      } catch(e){ this.showToast(e.message,'err'); }
      this.auditLoading = false;
    },
    async auditExportCSV(){
      const p = new URLSearchParams();
      if (this.auditFilter.user) p.set('user', this.auditFilter.user);
      if (this.auditFilter.action) p.set('action', this.auditFilter.action);
      if (this.auditFilter.q) p.set('q', this.auditFilter.q);
      if (this.auditFilter.from) p.set('from', String(this._toUnix(this.auditFilter.from)));
      if (this.auditFilter.to) p.set('to', String(this._toUnix(this.auditFilter.to)));
      p.set('limit', String(this.auditFilter.limit || 5000));
      p.set('format', 'csv');
      try {
        const r = await this.api('/api/audit/search?' + p.toString());
        if (!r.ok) throw new Error('export failed: ' + r.status);
        const blob = await r.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'audit-' + new Date().toISOString().slice(0,10) + '.csv';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 5000);
        this.showToast('CSV generated', 'ok');
      } catch(e) {
        this.showToast('Failed to export: ' + e.message, 'err');
      }
    },
    exportListCSV(rows, columns, filename){
      try {
        rows = rows || [];
        const keys = Object.keys(columns || {});
        const esc = (v) => {
          if (v === null || v === undefined) v = '';
          let s = String(v);
          if (/[",\n\r]/.test(s)) s = '"' + s.replace(/"/g, '""') + '"';
          return s;
        };
        const header = keys.map(k => esc(columns[k])).join(',');
        const body = rows.map(row => keys.map(k => esc(row ? row[k] : '')).join(',')).join('\r\n');
        const csv = '\uFEFF' + header + (body ? '\r\n' + body : '');
        const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = (filename || 'list') + '-' + new Date().toISOString().slice(0,10) + '.csv';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 5000);
        this.showToast('CSV generated', 'ok');
      } catch(e) {
        this.showToast('Failed to export: ' + e.message, 'err');
      }
    },
    clearAuditFilters(){
      this.auditFilter = { user:'', action:'', q:'', from:'', to:'', limit:200 };
      try { localStorage.removeItem('panel_audit_filter'); } catch(_) {}
      this.runAuditSearch();
    },
    _toUnix(dateStr){
      if (!dateStr) return 0;
      const d = new Date(dateStr);
      if (isNaN(d.getTime())) return 0;
      return Math.floor(d.getTime() / 1000);
    },

    async pollNotifications(){
      if (!this.token) return;
      try {
        const r = await this.api('/api/metrics/fires');
        const fires = await r.json() || [];
        const first = (this._lastFireT === undefined);
        const cutoff = (this._lastFireT || 0);
        let maxT = cutoff;
        for (const f of fires) {
          const t = f.Time || f.time || 0;
          if (t > maxT) maxT = t;
          if (first || t <= cutoff) continue;
          const rule = f.Rule || f.rule || 'rule';
          const sev = f.Severity || f.severity || '';
          if (f.Resolved) {
            this.addNotification('ok', rule + ' \u2014 normalized');
          } else {
            const v = (typeof f.Value === 'number') ? ' (' + f.Value.toFixed(1) + ')' : '';
            this.addNotification(sev === 'crit' ? 'err' : 'alert', rule + ' fired' + v);
          }
        }
        this._lastFireT = maxT;
      } catch(e){}
    },

    activePane(){
      if (!this.terms) return null;
      return (this.terms.panes||[]).find(p => p.id === this.terms.activePane) || this.terms.panes[0] || null;
    },
    isMobile(){
      void this.mobileChangeTick;
      return !!(window.matchMedia && window.matchMedia('(max-width: 767.98px)').matches);
    },
    _suggestPaneName(){
      const used = new Set((this.terms.panes||[]).map(p => p.sessionName));
      const main = 'main';
      if (!used.has(main)) return main;
      for (let i=2;i<200;i++){ const n='pane-'+i; if (!used.has(n)) return n; }
      return 'pane-'+Date.now();
    },
    _makePane(sessionName, startupCmd, aiProvider){
      const paneId = 'p-' + (++this.terms._paneSeq);
      return {
        type: 'pane',
        id: paneId,
        sessionName: sessionName || ('session-' + paneId),
        status: 'idle',
        term: null, fit: null, search: null, ws: null,
        reconnect: { attempts:0, timer:null, nextDelay:0, cancelled:false },
        notify: { lastOutput: 0, busy: false, alerted: false },
        startupCmd: startupCmd || '',
        startupRan: false,
        aiProvider: aiProvider || 'oauth',
      };
    },
    _findPaneNode(node, paneId, parent, side){
      if (!node) return null;
      if (node.type === 'pane' && node.id === paneId) return { node, parent, side };
      if (node.type === 'split') {
        return this._findPaneNode(node.a, paneId, node, 'a') ||
               this._findPaneNode(node.b, paneId, node, 'b');
      }
      return null;
    },
    _replaceNode(oldNode, newNode){
      if (this.terms.layout === oldNode) { this.terms.layout = newNode; return; }
      const walk = (n) => {
        if (!n || n.type !== 'split') return;
        if (n.a === oldNode) { n.a = newNode; return; }
        if (n.b === oldNode) { n.b = newNode; return; }
        walk(n.a); walk(n.b);
      };
      walk(this.terms.layout);
    },
    _allPanes(node, out){
      out = out || [];
      if (!node) return out;
      if (node.type === 'pane') out.push(node);
      else { this._allPanes(node.a, out); this._allPanes(node.b, out); }
      return out;
    },
    newPane(name, opts){
      opts = opts || {};
      let defaultCmd = this.hostTermStartupCmd || '';
      const startupCmd = opts.isReattach
        ? ''
        : (typeof opts.startupCmd === 'string' ? opts.startupCmd : defaultCmd);
      const paneName = name || this._suggestPaneName();
      const pane = this._makePane(paneName, startupCmd, opts.aiProvider);
      this.terms.panes.push(pane);
      if (!this.terms.layout) {
        this.terms.layout = { type: 'pane', id: pane.id };
      } else {
        this.terms.layout = { type: 'split', dir: 'row', size: 0.5,
                              a: this.terms.layout,
                              b: { type: 'pane', id: pane.id } };
      }
      this.terms.activePane = pane.id;
      this.saveState();
      this.$nextTick(()=> {
        this.renderPaneLayout();
        this.mountPane(pane);
      });
    },
    async newPaneWithPromptedCmd(){
      const raw = await this.askInput({ title:'New panel', label:'Command to run in the new panel (empty = none):', value:this.hostTermStartupCmd || '' });
      if (raw === null) return;
      this.newPane(undefined, { startupCmd: (raw || '').trim() });
    },
    saveStartupCmd(){
      localStorage.setItem('panel_term_startup_cmd', this.hostTermStartupCmd || '');
    },
    mountPane(pane){
      if (this.isMobile() && this.terms.activePane && this.terms.activePane !== pane.id) return;
      const el = document.getElementById('host-pane-'+pane.id);
      if (!el) return;
      if (el.clientWidth === 0 || el.clientHeight === 0) {
        if (!el.isConnected || !(this.terms.panes||[]).some(p => p.id === pane.id)) return;
        pane._mountTries = (pane._mountTries || 0) + 1;
        if (pane._mountTries > 180) return;
        requestAnimationFrame(()=> this.mountPane(pane));
        return;
      }
      pane._mountTries = 0;
      this.buildTerminal({
        el,
        wsPath: '/ws/shell?size=1&frame=1&name=' + encodeURIComponent(pane.sessionName) +
                (pane.aiProvider && pane.aiProvider !== 'oauth' ? '&ai=' + encodeURIComponent(pane.aiProvider) : ''),
        fontSize: this._termFontSize(),
        themeOverride: this.termThemes[this.hostTermTheme],
        state: pane,
        onStatus: (s) => { pane.status = s; if (s === 'open') { pane.ended = false; pane._restarting = false; } this._renderPaneOverlay(pane); },
        onOutput: (data) => this._termOutputHook(pane, data),
      });
    },
    renderPaneLayout(){
      const root = document.getElementById('host-pane-root');
      if (!root) return;
      const paneCache = {};
      root.querySelectorAll('[data-pane-id]').forEach(el => {
        const pid = el.getAttribute('data-pane-id');
        paneCache[pid] = el;
      });
      if (this.isMobile()) {
        if (!this.terms.layout) { while (root.firstChild) root.removeChild(root.firstChild); return; }
        this._attachSwipeHandlers(root);
        const panes = this.terms.panes || [];
        const activeId = this.terms.activePane || (panes[0] && panes[0].id);
        if (activeId && this.terms.activePane !== activeId) this.terms.activePane = activeId;
        Object.keys(paneCache).forEach(pid => {
          if (!panes.find(p => p.id === pid)) { try { paneCache[pid].remove(); } catch(_){} delete paneCache[pid]; }
        });
        panes.forEach(p => {
          let wrap = paneCache[p.id];
          if (wrap && !p.term) { try { wrap.remove(); } catch(_){} delete paneCache[p.id]; wrap = null; }
          if (!wrap && p.term) {
            try { p.term.dispose(); } catch(_){}
            try { p.ws && p.ws.close(); } catch(_){}
            p.term = null; p.fit = null; p.search = null; p.ws = null;
          }
          if (!wrap) { wrap = this._createPaneWrap(p); paneCache[p.id] = wrap; }
          if (wrap.parentNode !== root) root.appendChild(wrap);
          const isActive = (p.id === activeId);
          wrap.style.display = isActive ? 'flex' : 'none';
          wrap.classList.toggle('pane-active', isActive);
        });
        const active = panes.find(p => p.id === activeId);
        if (active && !active.term) this.$nextTick(()=> this.mountPane(active));
        this.$nextTick(()=> { this._refitAllPanes(); this._syncPaneAccountSelects(); });
        return;
      }

      while (root.firstChild) root.removeChild(root.firstChild);
      if (!this.terms.layout) {
        const es = document.createElement('div');
        es.className = 'term-empty-state';
        es.innerHTML = '<div class="tes-inner"><div class="tes-icon">▚</div>'
          + '<div class="tes-title">No panel open</div>'
          + '<div class="tes-sub">Open a terminal to start operating.</div>'
          + '<button class="tes-btn" data-a="new">+ New panel</button></div>';
        root.appendChild(es);
        const nb = es.querySelector('[data-a="new"]'); if (nb) nb.onclick = () => { try { this.newPane(); } catch(_){} };
        return;
      }
      const buildNode = (node) => {
        if (!node) return document.createElement('div');
        if (node.type === 'pane') {
          const pane = (this.terms.panes || []).find(p => p.id === node.id) || node;
          let wrap = paneCache[node.id];
          if (wrap && !pane.term) {
            wrap.remove();
            delete paneCache[node.id];
            wrap = null;
          }
          if (!wrap) wrap = this._createPaneWrap(pane);
          if (this.terms.activePane === node.id) wrap.classList.add('pane-active');
          else wrap.classList.remove('pane-active');
          return wrap;
        }
        const cont = document.createElement('div');
        cont.className = 'flex w-full h-full' + (node.dir === 'row' ? '' : ' flex-col');
        cont.dataset.splitDir = node.dir;
        const aWrap = document.createElement('div');
        aWrap.style.flex = node.size + ' 1 0%';
        aWrap.style.minWidth = '0';
        aWrap.style.minHeight = '0';
        aWrap.style.position = 'relative';
        aWrap.appendChild(buildNode(node.a));
        const divider = document.createElement('div');
        divider.className = 'term-divider';
        divider.dataset.splitDir = node.dir;
        if (node.dir === 'row') divider.style.cssText = 'width:6px;cursor:col-resize;background:var(--border-default);flex-shrink:0;z-index:5;';
        else divider.style.cssText = 'height:6px;cursor:row-resize;background:var(--border-default);flex-shrink:0;z-index:5;';
        divider.addEventListener('mousedown', (ev) => this._beginDividerDrag(ev, node));
        const bWrap = document.createElement('div');
        bWrap.style.flex = (1 - node.size) + ' 1 0%';
        bWrap.style.minWidth = '0';
        bWrap.style.minHeight = '0';
        bWrap.style.position = 'relative';
        bWrap.appendChild(buildNode(node.b));
        cont.appendChild(aWrap);
        cont.appendChild(divider);
        cont.appendChild(bWrap);
        return cont;
      };
      root.appendChild(buildNode(this.terms.layout));
      this.$nextTick(()=> { this._refitAllPanes(); this._syncPaneAccountSelects(); });
    },
    _attachSwipeHandlers(root){
      if (!root || root.dataset.swipeBound === '1') return;
      root.dataset.swipeBound = '1';
      let start = null, lastY = 0, axis = null, moved = false, lpTimer = null, selecting = false;
      const clearLP = () => { if (lpTimer) { clearTimeout(lpTimer); lpTimer = null; } };
      root.addEventListener('touchstart', (ev) => {
        if (this.terms.trackpadMode) return;
        if (ev.touches.length > 1) { start = null; clearLP(); return; }
        const t = ev.touches[0]; if (!t) return;
        ev.stopPropagation();
        start = { x: t.clientX, y: t.clientY, time: Date.now() };
        lastY = t.clientY; axis = null; moved = false; selecting = false;
        this._scrollAcc = 0;
        clearLP();
        lpTimer = setTimeout(() => {
          lpTimer = null;
          if (moved || !start) return;
          if (this._touchSelectStart(start.x, start.y)) {
            selecting = true;
            try { navigator.vibrate && navigator.vibrate(12); } catch(_){}
          }
        }, 500);
      }, { passive: true, capture: true });
      root.addEventListener('touchmove', (ev) => {
        if (!start || this.terms.trackpadMode) return;
        const t = ev.touches[0]; if (!t) return;
        ev.stopPropagation();
        if (selecting) { this._touchSelectExtend(t.clientX, t.clientY); if (ev.cancelable) ev.preventDefault(); return; }
        const dx = t.clientX - start.x, dy = t.clientY - start.y;
        if (!moved && (Math.abs(dx) > 8 || Math.abs(dy) > 8)) {
          moved = true; clearLP(); axis = Math.abs(dy) > Math.abs(dx) ? 'v' : 'h';
          if (this.termSel.open) this.clearTouchSelection();
        }
        if (axis === 'v') {
          const p = this.activePane && this.activePane();
          const term = p && p.term;
          const delta = t.clientY - lastY; lastY = t.clientY;
          if (term && delta) {
            try {
              const alt = term.buffer && term.buffer.active && term.buffer.active.type === 'alternate';
              if (alt) {
                const tgt = document.querySelector('#host-pane-'+p.id+' .xterm-viewport')
                         || document.querySelector('#host-pane-'+p.id+' .xterm-screen');
                if (tgt) tgt.dispatchEvent(new WheelEvent('wheel', { deltaY: -delta, deltaMode: 0, bubbles: true, cancelable: true }));
              } else {
                this._scrollAcc = (this._scrollAcc || 0) + (-delta / 18);
                const whole = Math.trunc(this._scrollAcc);
                if (whole) { term.scrollLines(whole); this._scrollAcc -= whole; }
              }
            } catch(_){}
          }
          if (ev.cancelable) ev.preventDefault();
          return;
        }
        if (axis === 'h' && ev.cancelable) ev.preventDefault();
      }, { passive: false, capture: true });
      root.addEventListener('touchend', (ev) => {
        clearLP();
        if (!start || this.terms.trackpadMode) { start = null; return; }
        ev.stopPropagation();
        const t = ev.changedTouches[0]; const s = start; start = null;
        const ax = axis; axis = null;
        if (selecting) { selecting = false; this.termSel.open = true; this._applyTouchSelection(); return; }
        if (!t) return;
        const dx = t.clientX - s.x, dy = t.clientY - s.y, dt = Date.now() - s.time;
        if (ax === 'h' && Math.abs(dx) > 60 && Math.abs(dy) < 50 && dt < 600) {
          this._focusAdjacentPane(dx < 0 ? +1 : -1);
          return;
        }
        if (ax === 'v') return;
        if (!moved && dt < 500) {
          const p = this.activePane && this.activePane();
          if (p && p.term) { try { if (p.term.hasSelection()) p.term.clearSelection(); } catch(_){} }
          this.termSel.open = false;
          if (p) { this._setActivePaneNoRender(p.id); this.summonKeyboard(); }
        }
      }, { passive: true, capture: true });
    },
    _touchCell(clientX, clientY){
      const p = this.activePane && this.activePane();
      const term = p && p.term; if (!term) return null;
      const el = document.querySelector('#host-pane-'+p.id+' .xterm-screen')
              || document.querySelector('#host-pane-'+p.id+' .xterm');
      if (!el) return null;
      const r = el.getBoundingClientRect();
      const cw = r.width / term.cols, ch = r.height / term.rows;
      let vc = Math.floor((clientX - r.left) / cw);
      let vr = Math.floor((clientY - r.top) / ch);
      vc = Math.max(0, Math.min(term.cols - 1, vc));
      vr = Math.max(0, Math.min(term.rows - 1, vr));
      const row = (term.buffer.active.viewportY || 0) + vr;
      return { term, p, col: vc, row };
    },
    _cellRect(col, row){
      const p = this.activePane && this.activePane();
      const term = p && p.term; if (!term) return null;
      const el = document.querySelector('#host-pane-'+p.id+' .xterm-screen')
              || document.querySelector('#host-pane-'+p.id+' .xterm');
      if (!el) return null;
      const r = el.getBoundingClientRect();
      const cw = r.width / term.cols, ch = r.height / term.rows;
      const vr = row - (term.buffer.active.viewportY || 0);
      return { x: r.left + col * cw, y: r.top + vr * ch, w: cw, h: ch };
    },
    _applyTouchSelection(){
      const p = this.activePane && this.activePane(); const term = p && p.term; if (!term) return;
      let sCol = this.termSel.sCol, sRow = this.termSel.sRow, eCol = this.termSel.eCol, eRow = this.termSel.eRow;
      if (eRow < sRow || (eRow === sRow && eCol < sCol)) { const c=sCol,r=sRow; sCol=eCol; sRow=eRow; eCol=c; eRow=r; }
      this.termSel.sCol = sCol; this.termSel.sRow = sRow; this.termSel.eCol = eCol; this.termSel.eRow = eRow;
      const len = (eRow - sRow) * term.cols + (eCol - sCol) + 1;
      try { term.clearSelection(); term.select(sCol, sRow, Math.max(1, len)); } catch(_){}
      const a = this._cellRect(sCol, sRow), b = this._cellRect(eCol, eRow);
      if (a) { this.termSel.sx = a.x; this.termSel.sy = a.y; }
      if (b) { this.termSel.ex = b.x + b.w; this.termSel.ey = b.y + b.h; }
      const bx = a ? a.x : (this.termSel.x || 100), by = a ? a.y : 100;
      this.termSel.x = Math.max(8, Math.min(window.innerWidth - 170, bx));
      this.termSel.y = Math.max(56, by - 52);
    },
    _touchSelectStart(clientX, clientY){
      const c = this._touchCell(clientX, clientY);
      if (!c) return false;
      const { term, col, row } = c;
      let s = col, e = col;
      try {
        const line = term.buffer.active.getLine(row);
        const text = line ? line.translateToString(true) : '';
        const isW = ch => ch && /[^\s"'`(){}\[\]<>|;,:=]/.test(ch);
        if (isW(text[col])) {
          while (s > 0 && isW(text[s - 1])) s--;
          while (e < text.length - 1 && isW(text[e + 1])) e++;
        }
      } catch(_){}
      this.termSel.sCol = s; this.termSel.sRow = row; this.termSel.eCol = e; this.termSel.eRow = row;
      this.termSel.open = true;
      this._applyTouchSelection();
      return true;
    },
    _touchSelectExtend(clientX, clientY){
      const c = this._touchCell(clientX, clientY);
      if (!c) return;
      this.termSel.eCol = c.col; this.termSel.eRow = c.row;
      this._applyTouchSelection();
    },
    moveHandle(which, ev){
      const t = ev && ev.touches && ev.touches[0]; if (!t) return;
      const c = this._touchCell(t.clientX, t.clientY); if (!c) return;
      if (which === 's') { this.termSel.sCol = c.col; this.termSel.sRow = c.row; }
      else { this.termSel.eCol = c.col; this.termSel.eRow = c.row; }
      this._applyTouchSelection();
    },
    clearTouchSelection(){
      const p = this.activePane && this.activePane();
      try { p && p.term && p.term.clearSelection(); } catch(_){}
      this.termSel.open = false;
    },
    copyTouchSelection(){
      const p = this.activePane && this.activePane();
      let txt = ''; try { txt = (p && p.term && p.term.getSelection()) || ''; } catch(_){}
      this.termSel.open = false;
      if (!txt) return;
      const done = () => { this.showToast && this.showToast('copied ('+txt.length+' chars)', 'ok'); try { p.term.clearSelection(); } catch(_){} };
      try {
        if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(txt).then(done).catch(()=>{ this._copyFallback(txt); done(); });
        else { this._copyFallback(txt); done(); }
      } catch(_) { this._copyFallback(txt); done(); }
    },
    _copyFallback(txt){
      try { const ta=document.createElement('textarea'); ta.value=txt; ta.style.position='fixed'; ta.style.opacity='0'; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); document.body.removeChild(ta); } catch(_){}
    },
    selectAllAndCopy(){
      const p = this.activePane && this.activePane();
      if (!p || !p.term) return;
      try { p.term.selectAll(); } catch(_){}
      this.copyTouchSelection();
    },
    openPaneRadial(paneId, x, y){
      if (!paneId) { const p = this.activePane && this.activePane(); paneId = p && p.id; }
      if (!paneId) return;
      this.terms.activePane = paneId;
      const R = 92, m = R + 28;
      const cx = Math.max(m, Math.min(window.innerWidth - m, x));
      const cy = Math.max(m, Math.min(window.innerHeight - m, y));
      this.paneRadial = { open: true, x: cx, y: cy, paneId };
    },
    radialItems(){
      const defs = [
        { kind:'keyboard', label:'Keyboard', icon:'⌨' },
        { kind:'clear',    label:'Clear',  icon:'🧹' },
        { kind:'reconnect',label:'Reconnect', icon:'↻' },
        { kind:'rename',   label:'Rename', icon:'✎' },
        { kind:'new',      label:'New',    icon:'＋' },
        { kind:'kill',     label:'Kill',   icon:'🗑' },
      ];
      const R = 78, n = defs.length;
      return defs.map((d, i) => {
        const a = (-Math.PI/2) + (i * 2 * Math.PI / n);
        return Object.assign({}, d, { dx: Math.round(Math.cos(a)*R), dy: Math.round(Math.sin(a)*R) });
      });
    },
    radialAction(kind){
      const paneId = this.paneRadial.paneId;
      this.paneRadial.open = false;
      const pane = (this.terms.panes||[]).find(p => p.id === paneId);
      if (!pane && kind !== 'new') return;
      switch (kind) {
        case 'keyboard':  this.summonKeyboard(); break;
        case 'clear':     this.hostTermClear(); break;
        case 'reconnect': this.hostTermReconnectNow(); break;
        case 'rename': {
          (async () => {
            const fresh = ((await this.askInput({ title:'Rename panel', label:'Rename panel (cosmetic):', value: pane.sessionName || '' })) || '').trim();
            if (fresh) { pane.sessionName = fresh; this.renderPaneLayout(); this.saveState(); }
          })();
          break;
        }
        case 'new':       this.newPane(); break;
        case 'kill':      this.killActiveSession(); break;
      }
    },
    _createPaneWrap(pane){
      const wrap = document.createElement('div');
      wrap.className = 'pane-wrap';
      wrap.dataset.paneId = pane.id;
      wrap.style.cssText = 'position:absolute;inset:0;display:flex;flex-direction:column;border:1px solid transparent;border-radius:6px;overflow:hidden;';
      const bar = document.createElement('div');
      bar.className = 'pane-title';
      bar.style.cssText = 'display:flex;align-items:center;gap:6px;padding:2px 8px;font-size:10px;background:#0b1220;color:#9ca3af;cursor:grab;user-select:none;flex-shrink:0;';
      bar.draggable = true;
      bar.dataset.paneTitleFor = pane.id;
      bar.innerHTML = '<span class="pane-dot" style="width:6px;height:6px;border-radius:50%;background:#6b7280;flex-shrink:0;"></span>' +
                      '<span class="pane-name mono truncate" style="flex:1;">' + escapeHtml(pane.sessionName || pane.id) + '</span>' +
                      '<select class="pane-acct-sel" title="Claude account for this session" ' +
                        'style="font-size:9px;background:#0b1220;color:#6b7280;border:1px solid #1e3a5f;border-radius:3px;padding:1px 3px;cursor:pointer;max-width:68px;flex-shrink:0;">' +
                      '</select>' +
                      '<button class="pane-load" title="Load a session into this panel" style="opacity:0.6;padding:0 4px;">📋</button>' +
                      '<button class="pane-split-h" title="Split horizontal (stacks panes)" style="opacity:0.6;padding:0 4px;">⬓</button>' +
                      '<button class="pane-split-v" title="Split vertical (side by side)" style="opacity:0.6;padding:0 4px;">⬔</button>' +
                      '<button class="pane-close" title="Close panel" style="opacity:0.6;padding:0 4px;color:#f43f5e;">✕</button>';
      const onBtn = (sel, fn) => {
        const b = bar.querySelector(sel);
        b.draggable = false;
        b.setAttribute('draggable', 'false');
        b.addEventListener('mousedown', (ev) => { ev.stopPropagation(); });
        b.addEventListener('click',     (ev) => { ev.stopPropagation(); ev.preventDefault(); fn(ev); });
        b.addEventListener('dragstart', (ev) => { ev.preventDefault(); ev.stopPropagation(); });
      };
      onBtn('.pane-load',    (ev) => this.openPaneSessionPicker(pane, ev.currentTarget));
      onBtn('.pane-split-h', (ev) => this.splitPane(pane.id, 'column', ev.currentTarget));
      onBtn('.pane-split-v', (ev) => this.splitPane(pane.id, 'row',    ev.currentTarget));
      onBtn('.pane-close',   () => this.closePane(pane.id));
      const acctSel = bar.querySelector('.pane-acct-sel');
      acctSel.draggable = false;
      acctSel.setAttribute('draggable', 'false');
      acctSel.addEventListener('mousedown', (ev) => ev.stopPropagation());
      acctSel.addEventListener('dragstart', (ev) => { ev.preventDefault(); ev.stopPropagation(); });
      acctSel.addEventListener('change', (ev) => {
        ev.stopPropagation();
        this.swapSessionAccount(pane.sessionName, acctSel.value);
      });
      const accts0 = (this.claudeAccounts && this.claudeAccounts.accounts) || [];
      if (accts0.length) {
        acctSel.innerHTML = accts0.map(a =>
          '<option value="' + escapeHtml(a.id) + '">' + escapeHtml(a.label) + '</option>'
        ).join('');
      }
      bar.addEventListener('dragstart', (ev) => this._beginPaneDrag(ev, pane));
      bar.addEventListener('dragend',   () => this._endPaneDrag());
      let lpStart = null;
      bar.addEventListener('touchstart', (ev) => {
        if (ev.target.closest('button')) return;
        const t = ev.touches[0]; if (!t) return;
        lpStart = { x: t.clientX, y: t.clientY };
        if (this.terms._longPressTimer) clearTimeout(this.terms._longPressTimer);
        this.terms._longPressTimer = setTimeout(() => {
          this.terms._longPressTimer = null;
          if (!lpStart) return;
          try { navigator.vibrate && navigator.vibrate(15); } catch(e){}
          this._openPaneContextMenu(pane.id, lpStart.x, lpStart.y);
        }, 500);
      }, { passive: true });
      bar.addEventListener('touchmove', (ev) => {
        if (!lpStart) return;
        const t = ev.touches[0]; if (!t) return;
        if (Math.abs(t.clientX - lpStart.x) > 10 || Math.abs(t.clientY - lpStart.y) > 10) {
          if (this.terms._longPressTimer) { clearTimeout(this.terms._longPressTimer); this.terms._longPressTimer = null; }
          lpStart = null;
        }
      }, { passive: true });
      bar.addEventListener('touchend', () => {
        if (this.terms._longPressTimer) { clearTimeout(this.terms._longPressTimer); this.terms._longPressTimer = null; }
        lpStart = null;
      }, { passive: true });
      wrap.addEventListener('mousedown', (ev) => {
        if (ev.target.closest('button')) return;
        this._setActivePaneNoRender(pane.id);
      }, true);
      wrap.addEventListener('dragover',  (ev) => this._panelDragOver(ev, pane));
      wrap.addEventListener('dragleave', (ev) => this._panelDragLeave(pane, ev));
      wrap.addEventListener('drop',      (ev) => this._panelDrop(ev, pane));
      const termEl = document.createElement('div');
      termEl.id = 'host-pane-' + pane.id;
      termEl.style.cssText = 'flex:1;min-height:0;position:relative;';
      const ov = document.createElement('div');
      ov.className = 'term-pane-ov';
      ov.dataset.paneOv = pane.id;
      ov.style.display = 'none';
      termEl.appendChild(ov);
      const sb = document.createElement('button');
      sb.className = 'term-scrollbtn';
      sb.dataset.paneScroll = pane.id;
      sb.type = 'button';
      sb.textContent = '↓ new output';
      sb.style.display = 'none';
      sb.onclick = () => { try { pane.term && pane.term.scrollToBottom(); } catch(_){} this._updateScrollBtn(pane); };
      termEl.appendChild(sb);
      wrap.appendChild(bar);
      wrap.appendChild(termEl);
      return wrap;
    },
    _reattachRepaint(state){
      const term = state.term;
      if (!term) return;
      state._holdUntil = Date.now() + 1500;
      state._lastDataAt = Date.now();
      try { term.write('\x18'); } catch(_){}
      try { term.refresh && term.refresh(0, term.rows - 1); } catch(_){}
      if (state._repaintProbe) { clearTimeout(state._repaintProbe); }
      state._repaintProbe = setTimeout(() => {
        state._repaintProbe = null;
        if (!state.term || !state.ws || state.ws.readyState !== 1) return;
        if (!this._viewportNeedsRepaint(state.term)) return;
        console.debug('[panel:term] blank viewport on reattach — escalating to the wobble');
        const rc = state.term.cols | 0, rr = state.term.rows | 0;
        if (rc <= 12 || rr <= 6) return;
        try { state.term.resize(rc - 8, rr - 4); } catch(_){}
        setTimeout(() => {
          try { this._safeFit(state.fit); } catch(_){}
          try { if (state.ws && state.ws.readyState === 1 && state.term.cols >= 2) state.ws.send(JSON.stringify({ type:'resize', cols: state.term.cols, rows: state.term.rows })); } catch(_){}
          try { state.term.refresh && state.term.refresh(0, state.term.rows - 1); } catch(_){}
        }, 160);
      }, 700);
    },
    _viewportNeedsRepaint(term){
      try {
        const buf = term.buffer && term.buffer.active;
        if (!buf || typeof buf.getLine !== 'function') return true;
        const base = buf.viewportY | 0;
        for (let i = 0; i < term.rows; i++){
          const line = buf.getLine(base + i);
          if (!line || typeof line.translateToString !== 'function') return true;
          if (line.translateToString(true).trim() !== '') return false;
        }
        return true;
      } catch(_) { return true; }
    },
    async _checkBuildStamp(){
      try {
        const now = Date.now();
        if (this._buildCheckAt && (now - this._buildCheckAt) < 3000) return;
        this._buildCheckAt = now;
        if (!this._myBuild) {
          const m = document.querySelector('meta[name="panel-build"]');
          this._myBuild = (m && m.content) || '';
        }
        if (!this._myBuild) return;
        const r = await this.api('/api/health');
        if (!r || !r.ok) return;
        const d = await r.json();
        const srv = d && d.build ? String(d.build) : '';
        if (!srv || srv === this._myBuild) return;
        if (this._buildWarned === srv) return;
        this._buildWarned = srv;
        this.newVersionAvailable = true;
        this._armSafeReload();
      } catch(_){}
    },
    _armSafeReload(){
      if (this._reloadTimer) return;
      if (!this.hostTermAutoReload) return;
      const retry = () => this._trySafeReload();
      this._reloadTimer = setInterval(retry, 5000);
      document.addEventListener('visibilitychange', retry);
    },
    _trySafeReload(){
      if (!this.newVersionAvailable || !this.hostTermAutoReload) return false;
      if (!document.hidden) { this._hiddenSince = 0; return false; }
      const now = Date.now();
      if (!this._hiddenSince) { this._hiddenSince = now; return false; }
      if (now - this._hiddenSince < 10 * 60 * 1000) return false;
      if (this._lastTyping && (now - this._lastTyping) < 10 * 60 * 1000) return false;
      const pending = (this.terms.panes || []).some(p => p._outbox && p._outbox.length);
      if (pending) return false;
      if (document.querySelector('[role="dialog"]')) return false;
      clearInterval(this._reloadTimer); this._reloadTimer = null;
      location.reload();
      return true;
    },
    _paneSendInput(pane, d){
      if (!pane || d == null || d === '') return false;
      this._lastTyping = Date.now();
      if (pane.ws && pane.ws.readyState === 1) {
        (pane._txQ || (pane._txQ = [])).push(d);
        if (!pane._txScheduled) {
          pane._txScheduled = true;
          const flush = () => { pane._txScheduled = false; this._paneTxFlush(pane); };
          if (typeof queueMicrotask === 'function') queueMicrotask(flush);
          else Promise.resolve().then(flush);
        }
        this._markSend(pane, d);
        this._predictEcho(pane, d);
        return true;
      }
      return this._paneEnqueueOffline(pane, d);
    },
    _paneTxFlush(pane){
      const q = pane._txQ; pane._txQ = [];
      if (!q || !q.length) return;
      const txt = q.join('');
      if (!txt) return;
      const ws = pane.ws;
      if (!ws || ws.readyState !== 1) { this._paneEnqueueOffline(pane, txt); return; }
      try {
        this._txEnc || (this._txEnc = new TextEncoder());
        ws.send(this._txEnc.encode(txt));
      } catch(_) { this._paneEnqueueOffline(pane, txt); }
    },
    _paneEnqueueOffline(pane, d){
      if (!pane._outbox) { pane._outbox = []; pane._outboxBytes = 0; }
      if (pane._outboxBytes + d.length > 128 * 1024) { pane._outboxDropped = true; return false; }
      pane._outbox.push(d);
      pane._outboxBytes += d.length;
      this._paneEchoOffline(pane, d);
      try { this._renderPaneOverlay(pane); } catch(_){}
      return false;
    },
    _paneEchoOffline(pane, d){
      if (!pane.term) return;
      if (pane._serverEchoes === false) return;
      if (this._looksLikePasswordLine(pane)) return;
      let out = '';
      for (const ch of d) {
        const c = ch.codePointAt(0);
        if (c === 0x7f || c === 0x08) { out += '\b \b'; continue; }
        if (c < 0x20) break;
        out += ch;
      }
      if (!out) return;
      try { pane.term.write('\x1b[2m' + out + '\x1b[22m'); } catch(_){}
      let vis = pane._echoPainted || 0;
      for (const ch of d) {
        const c = ch.codePointAt(0);
        if (c === 0x7f || c === 0x08) vis = Math.max(0, vis - 1);
        else if (c < 0x20) break;
        else vis++;
      }
      pane._echoPainted = vis;
    },
    _looksLikePasswordLine(pane){
      try {
        const buf = pane.term.buffer.active;
        const line = buf.getLine(buf.baseY + buf.cursorY);
        if (!line) return true;
        const txt = line.translateToString(true);
        if (!/[:?]$/.test(txt)) return false;
        return /(password|passwd|passphrase|\bpin\b|token|secret)/i.test(txt);
      } catch(_) { return true; }
    },
    _predictEcho(pane, d){
      if (!this._canPredict(pane, d)) return;
      const term = pane.term, buf = term.buffer.active;
      const p = pane._pred || (pane._pred = { txt:'', col:0, line:0 });
      if (!p.txt) { p.col = buf.cursorX; p.line = buf.baseY + buf.cursorY; }
      if (buf.cursorX + d.length >= term.cols - 1) return;
      p.txt += d;
      try { term.write('\x1b[2m' + d + '\x1b[22m'); } catch(_){ p.txt = ''; return; }
      const deadline = Math.min(3000, Math.max(600, (pane.eco || pane.rtt || 200) * 3));
      if (p.timer) clearTimeout(p.timer);
      p.timer = setTimeout(() => { p.timer = 0; this._erasePrediction(pane); }, deadline);
    },
    _canPredict(pane, d){
      if (!pane || !pane.term) return false;
      const mode = this.hostTermPredictiveEcho || 'auto';
      if (mode === 'never') return false;
      if (!/^[\x20-\x7e\u00a0-\uffff]+$/.test(d)) return false;
      let buf;
      try { buf = pane.term.buffer.active; } catch(_) { return false; }
      if (!buf || buf.type === 'alternate') return false;
      if (pane._serverEchoes === false) return false;
      if (this._looksLikePasswordLine(pane)) return false;
      if (mode === 'always') return true;
      const ms = pane.eco || pane.rtt || 0;
      return ms >= (this.hostTermEchoThreshold || 60);
    },
    _erasePrediction(pane){
      const p = pane && pane._pred;
      if (!p || !p.txt) return;
      const n = p.txt.length;
      p.txt = '';
      if (p.timer) { clearTimeout(p.timer); p.timer = 0; }
      try { pane.term.write('\b \b'.repeat(n)); } catch(_){}
    },
    _repredictEcho(pane){
      const p = pane && pane._pred;
      if (!p || !p.txt) return;
      const term = pane.term;
      if (!term) { p.txt = ''; return; }
      let buf;
      try { buf = term.buffer.active; } catch(_) { p.txt = ''; return; }
      if (!buf || buf.type === 'alternate') { p.txt = ''; return; }
      if ((buf.baseY + buf.cursorY) !== p.line) { p.txt = ''; return; }
      const advance = buf.cursorX - p.col;
      if (advance < 0) { p.txt = ''; return; }
      const rest = p.txt.slice(advance);
      p.col = buf.cursorX;
      p.txt = rest;
      if (!rest) return;
      if (buf.cursorX + rest.length >= term.cols - 1) { p.txt = ''; return; }
      try { term.write('\x1b[2m' + rest + '\x1b[22m'); } catch(_){ p.txt = ''; }
    },
    _markSend(pane, d){
      if (!/^[\x20-\x7e\u00a0-\uffff]+$/.test(d)) return;
      if (!pane._sentAt) pane._sentAt = Date.now();
      if (pane._echoTimer) return;
      pane._echoTimer = setTimeout(() => {
        pane._echoTimer = 0;
        if (pane._sentAt) { pane._serverEchoes = false; pane._sentAt = 0; }
      }, 1500);
    },
    _updateQuality(pane){
      const now = Date.now();
      if (pane._qualityAt && (now - pane._qualityAt) < 1000) return;
      pane._qualityAt = now;
      const ms = pane.eco || pane.rtt || 0;
      const level = !ms ? '' : (ms < 120 ? 'ok' : (ms < 350 ? 'medium' : 'bad'));
      const label = ms ? (ms < 1000 ? ms + ' ms' : (ms/1000).toFixed(1) + ' s') : '';
      if (pane.netLabel !== label) pane.netLabel = label;
      if (pane.netLevel !== level) pane.netLevel = level;
    },
    _markApiLatency(ms){
      this._apiEwma = this._apiEwma ? Math.round(this._apiEwma * 0.7 + ms * 0.3) : ms;
    },
    _slowNetwork(){
      let worst = this._apiEwma || 0;
      for (const p of (this.terms && this.terms.panes || [])) {
        const ms = (p && (p.eco || p.rtt)) || 0;
        if (ms > worst) worst = ms;
      }
      return worst >= 350;
    },
    _skipPoll(key){
      if (!this._slowNetwork()) return false;
      this._pollTicks || (this._pollTicks = {});
      const n = (this._pollTicks[key] = (this._pollTicks[key] || 0) + 1);
      return (n % 3) !== 0;
    },
    async loadClaudeVersions(){
      if (this.claudeVer.loading) return;
      this.claudeVer.loading = true;
      try {
        const r = await this.api('/api/claude/versions');
        if (!r || !r.ok) return;
        const d = await r.json();
        this.claudeVer.installed = d.installed || '';
        this.claudeVer.outdated = d.outdated || 0;
        this.claudeVer.processes = (d.processes || []).sort((a, b) =>
          (a.current === b.current) ? String(a.session||'').localeCompare(String(b.session||'')) : (a.current ? 1 : -1));
      } catch(_){} finally { this.claudeVer.loading = false; }
    },
    async restartPanelClaude(proc){
      if (!proc) return;
      if (proc.target === 'recovery') return this.restartRecoveryClaude(proc);
      return this.restartSessionClaude(proc);
    },
    async restartRecoveryClaude(proc){
      const target = proc.ref || this.claudeVer.installed || '?';
      const ok = await this.confirmAsync(
        'Restart the recovery Claude?\n'
        + 'Restarts the container and Claude comes up on the version it already downloaded. '
        + 'Unlike the sessions, there is NO --continue here: a recovery conversation in progress is lost.\n\n'
        + 'Running version: ' + proc.version + '  →  in the container: ' + target,
        { danger: true });
      if (!ok) return;
      this.claudeVer.restarting = proc.pid;
      try {
        const r = await this.api('/api/claude/recovery/restart', { method:'POST' });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
        this.showToast('recovery container restarted', 'ok');
        setTimeout(() => this.loadClaudeVersions(), 4000);
      } catch(e){
        this.showToast('error restarting the recovery: ' + e.message, 'err');
      } finally {
        this.claudeVer.restarting = 0;
      }
    },
    async restartSessionClaude(proc){
      if (!proc || !proc.session) return;
      const ok = await this.confirmAsync(
        'Restart Claude in "' + proc.session + '"?\n'
        + 'Ends Claude in this session and reopens it with --continue, resuming the conversation. '
        + 'If a task is running in it, that task is interrupted.\n\n'
        + 'Session version: ' + proc.version + '  →  installed: ' + (this.claudeVer.installed || '?'),
        { danger: true });
      if (!ok) return;
      let pane = (this.terms.panes||[]).find(p => p.sessionName === proc.session);
      if (!pane) { this.newPane(proc.session); await this.$nextTick(); pane = (this.terms.panes||[]).find(p => p.sessionName === proc.session); }
      else { this.focusPane(pane.id); }
      if (!pane) { this.showToast('could not open the session ' + proc.session, 'err'); return; }
      this.setPage('dev'); this.setTab('host');
      const steps = [['\x03', 400], ['/exit\r', 1200], ['claude --continue\r', 300]];
      for (const [txt, wait] of steps) {
        this._paneSendInput(pane, txt);
        await new Promise(r => setTimeout(r, wait));
      }
      this.showToast('restarting Claude in ' + proc.session, 'ok');
      setTimeout(() => this.loadClaudeVersions(), 8000);
    },
    _reconcileSizes(){
      const now = Date.now();
      if (this._reconcSizeAt && (now - this._reconcSizeAt) < 400) return;
      this._reconcSizeAt = now;
      (this.terms && this.terms.panes || []).forEach(p => {
        if (!p || !p.term) return;
        try { this._safeFit(p.fit); } catch(_){}
        try { if (p._assertSize) p._assertSize('window'); } catch(_){}
      });
    },
    _reconnectPanesNow(){
      const now = Date.now();
      if (this._reconnectNowAt && (now - this._reconnectNowAt) < 1000) return;
      this._reconnectNowAt = now;
      (this.terms && this.terms.panes || []).forEach(p => {
        if (!p || !p.reconnect || p.reconnect.cancelled) return;
        if (p.ws && (p.ws.readyState === 0 || p.ws.readyState === 1)) return;
        if (typeof p._reopen !== 'function') return;
        if (p.reconnect.timer) { clearTimeout(p.reconnect.timer); p.reconnect.timer = null; }
        try { p._reopen(); } catch(_){}
      });
    },
    _renderPaneOverlay(pane){
      const ov = document.querySelector('[data-pane-ov="'+pane.id+'"]');
      if (!ov) return;
      if (pane.ended){
        ov.innerHTML = '<div class="tpo-card"><div class="tpo-msg">Session ended</div>'
          + '<div class="tpo-sub">It will not be recreated automatically.</div>'
          + '<div class="tpo-actions"><button class="tpo-btn tpo-primary" data-a="new">New session here</button>'
          + '<button class="tpo-btn" data-a="close">Close panel</button></div></div>';
        ov.style.display = 'flex';
        const nb = ov.querySelector('[data-a="new"]'); if (nb) nb.onclick = () => this._newSessionInPane(pane);
        const cb = ov.querySelector('[data-a="close"]'); if (cb) cb.onclick = () => this.closePane(pane.id);
      } else if (pane._restarting && pane.status !== 'open'){
        const qr = (pane._outbox && pane._outbox.length) ? (pane._outboxBytes | 0) : 0;
        ov.innerHTML = '<div class="tpo-pill"><span class="tpo-dot"></span>Updating the server…'
          + (qr ? '<span class="tpo-sub" style="margin-left:6px;opacity:.85">' + qr + ' character' + (qr>1?'s':'') + ' queued — nothing lost</span>' : '')
          + '</div>';
        ov.style.display = 'flex';
      } else if (pane.status === 'reconnecting' || pane.status === 'connecting'){
        const q = (pane._outbox && pane._outbox.length) ? (pane._outboxBytes | 0) : 0;
        ov.innerHTML = '<div class="tpo-pill"><span class="tpo-dot"></span>'
          + (pane.status === 'reconnecting' ? 'Reconnecting…' : 'Connecting…')
          + (q ? '<span class="tpo-sub" style="margin-left:6px;opacity:.85">' + q + ' character' + (q>1?'s':'') + ' queued — nothing lost</span>' : '')
          + '<button class="tpo-link" data-a="now">reconnect now</button></div>';
        ov.style.display = 'flex';
        const b = ov.querySelector('[data-a="now"]'); if (b) b.onclick = () => this._reconnectPane(pane);
      } else {
        ov.style.display = 'none';
        ov.innerHTML = '';
      }
    },
    _newSessionInPane(pane){
      if (!pane) return;
      pane.ended = false;
      this.openSessionPicker({
        title: 'Session in this panel',
        currentName: pane.sessionName,
        onPick: (name) => this.loadSessionIntoPane(pane, name),
      });
    },
    _reconnectPane(pane){
      if (!pane) return;
      try { if (pane.reconnect){ pane.reconnect.cancelled = true; if (pane.reconnect.timer) clearTimeout(pane.reconnect.timer); } } catch(_){}
      try { if (pane.notify && pane.notify.timer) clearTimeout(pane.notify.timer); } catch(_){}
      try { if (pane.ws) pane.ws.close(); } catch(_){}
      try { if (pane.term) pane.term.dispose(); } catch(_){}
      try { if (pane.resizeObserver) pane.resizeObserver.disconnect(); } catch(_){}
      pane.term = null; pane.ws = null; pane.fit = null; pane.search = null; pane.resizeObserver = null; pane.ended = false;
      this.mountPane(pane);
    },
    _updateScrollBtn(pane){
      const sb = document.querySelector('[data-pane-scroll="'+pane.id+'"]');
      if (!sb) return;
      if (!pane.term) { sb.style.display = 'none'; return; }
      try {
        const b = pane.term.buffer.active;
        sb.style.display = ((b.viewportY || 0) < (b.baseY || 0)) ? 'block' : 'none';
      } catch(_){ sb.style.display = 'none'; }
    },
    _syncPaneAccountSelects(){
      const accounts = (this.claudeAccounts && this.claudeAccounts.accounts) || [];
      if (!accounts.length) return;
      const sessionMap = {};
      (this.claudeTermSessions || []).forEach(s => { sessionMap[s.name] = s.claude_account || ''; });
      (this.terms.panes || []).forEach(p => {
        const wrap = document.querySelector('[data-pane-id="' + p.id + '"]');
        if (!wrap) return;
        const sel = wrap.querySelector('.pane-acct-sel');
        if (!sel) return;
        if (sel.options.length !== accounts.length) {
          sel.innerHTML = accounts.map(a =>
            '<option value="' + escapeHtml(a.id) + '">' + escapeHtml(a.label) + '</option>'
          ).join('');
        }
        const acctId = sessionMap[p.sessionName] || (accounts[0] ? accounts[0].id : '');
        if (sel.value !== acctId) sel.value = acctId;
      });
    },
    _refitAllPanes(){
      (this.terms.panes||[]).forEach(p => { if (p.fit) this._fitSoon(p.fit); });
    },
    _fitSoon(fit){
      if (!fit) return;
      if (fit._deb) clearTimeout(fit._deb);
      fit._deb = setTimeout(() => {
        fit._deb = 0;
        const run = () => { this._safeFit(fit); };
        if (typeof requestAnimationFrame === 'function') requestAnimationFrame(run); else run();
      }, 140);
    },
    _safeFit(fit){
      if (!fit) return false;
      try {
        const d = (typeof fit.proposeDimensions === 'function') ? fit.proposeDimensions() : null;
        if (!d || !isFinite(d.cols) || !isFinite(d.rows) || d.cols < 2 || d.rows < 1) return false;
        const term = fit._panelTerm;
        if (!term || !(term.cols >= 2)) { fit.fit(); return true; }
        const gs = fit._panelState && fit._panelState._gridSession;
        if (gs && (gs.cols !== d.cols || gs.rows !== d.rows)) {
          if (term.cols !== gs.cols || term.rows !== gs.rows) {
            try { term.resize(gs.cols, gs.rows); } catch (_) {}
          }
          const mark = d.cols + 'x' + d.rows;
          if (fit._lastNatural !== mark) {
            fit._lastNatural = mark;
            try { fit._panelState._assertSize && fit._panelState._assertSize('natural window'); } catch (_) {}
          }
          return true;
        }
        fit._lastNatural = d.cols + 'x' + d.rows;
        const dc = d.cols - term.cols;
        if (dc === 1 || dc === -1) {
          if (fit._colPending !== d.cols) {
            fit._colPending = d.cols;
            if (fit._colTimer) clearTimeout(fit._colTimer);
            fit._colTimer = setTimeout(() => { fit._colTimer = 0; this._safeFit(fit); }, 300);
            if (d.rows !== term.rows) { try { term.resize(term.cols, d.rows); } catch (_) {} }
            return true;
          }
          fit._colPending = 0;
          const now = Date.now();
          if (fit._lastFit1col &&
              (now - fit._lastFit1col.em) < 2000 &&
              fit._lastFit1col.dc === -dc) {
            return true;
          }
          fit._lastFit1col = { dc, em: now };
        } else {
          fit._colPending = 0;
          fit._lastFit1col = null;
          if (fit._colTimer) { clearTimeout(fit._colTimer); fit._colTimer = 0; }
        }
        fit.fit();
        return true;
      } catch (e) { return false; }
    },
    focusPane(paneId){
      this.terms.activePane = paneId;
      if (this.terms.trackpadMode) { this.terms.trackpadMode = false; this._removeTrackpadOverlay && this._removeTrackpadOverlay(); }
      this.renderPaneLayout();
      const pane = (this.terms.panes||[]).find(p => p.id === paneId);
      if (pane && pane.term && (!this.isMobile() || this._wantKeyboard))
        this.$nextTick(()=>{ try{ pane.term.focus(); }catch(e){} });
    },
    _setActivePaneNoRender(paneId){
      if (this.terms.activePane === paneId) return;
      this.terms.activePane = paneId;
      const root = document.getElementById('host-pane-root');
      if (!root) return;
      root.querySelectorAll('[data-pane-id]').forEach(el => {
        if (el.getAttribute('data-pane-id') === paneId) el.classList.add('pane-active');
        else el.classList.remove('pane-active');
      });
      const pane = (this.terms.panes||[]).find(p => p.id === paneId);
      if (pane && pane.term && (!this.isMobile() || this._wantKeyboard)) try{ pane.term.focus(); }catch(e){}
    },
    splitPane(paneId, dir, anchorEl){
      if (!paneId) return;
      const found = this._findPaneNode(this.terms.layout, paneId, null, null);
      if (!found) return;
      this.openSessionPicker({
        anchorEl,
        title: 'New ' + (dir === 'row' ? 'vertical split' : 'horizontal split') + ' — choose the session',
        onPick: (sessionName) => this._doSplitPane(paneId, dir, sessionName),
      });
    },
    _doSplitPane(paneId, dir, sessionName){
      const found = this._findPaneNode(this.terms.layout, paneId, null, null);
      if (!found) return;
      const newPane = this._makePane(sessionName, '');
      newPane.startupRan = true;
      this.terms.panes.push(newPane);
      const splitNode = { type: 'split', dir: dir, size: 0.5, a: found.node, b: { type:'pane', id: newPane.id } };
      if (!found.parent) this.terms.layout = splitNode;
      else found.parent[found.side] = splitNode;
      this.terms.activePane = newPane.id;
      this.renderPaneLayout();
      this.$nextTick(()=> this.mountPane(newPane));
      this.saveState();
    },
    _nextSessionName(base){
      const used = new Set((this.terms.panes||[]).map(p => p.sessionName));
      for (let i=2; i<200; i++){
        const n = base + '-' + i;
        if (!used.has(n)) return n;
      }
      return base + '-' + Date.now();
    },
    closePane(paneId){
      if (!paneId) return;
      if ((this.terms.panes||[]).length === 1) {
        const lastPane = this.terms.panes[0];
        this.openSessionPicker({
          title: 'Load session (last panel)',
          currentName: lastPane.sessionName,
          onPick: (name) => this.loadSessionIntoPane(lastPane, name),
        });
        return;
      }
      const found = this._findPaneNode(this.terms.layout, paneId, null, null);
      if (!found) return;
      const pane = found.node;
      try { if (pane.reconnect) pane.reconnect.cancelled = true; } catch(e){}
      try { if (pane.reconnect && pane.reconnect.timer) clearTimeout(pane.reconnect.timer); } catch(e){}
      try { if (pane.notify && pane.notify.timer) clearTimeout(pane.notify.timer); } catch(e){}
      try { if (pane.ws) pane.ws.close(); } catch(e){}
      try { if (pane.term) pane.term.dispose(); } catch(e){}
      try { if (pane.resizeObserver) pane.resizeObserver.disconnect(); } catch(e){}
      pane.resizeObserver = null;
      this.terms.panes = (this.terms.panes||[]).filter(p => p.id !== paneId);
      if (found.parent) {
        const otherSide = found.side === 'a' ? 'b' : 'a';
        const survivor = found.parent[otherSide];
        if (this.terms.layout === found.parent) {
          this.terms.layout = survivor;
        } else {
          const gp = this._findSplitParent(this.terms.layout, found.parent, null, null);
          if (!gp || !gp.parent) this.terms.layout = survivor;
          else gp.parent[gp.side] = survivor;
        }
      } else {
        this.terms.layout = null;
      }
      if (this.terms.activePane === paneId) {
        const remaining = this._allPanes(this.terms.layout);
        this.terms.activePane = remaining[0]?.id || null;
      }
      this.renderPaneLayout();
      this.saveState();
    },
    async openSessionPicker(opts){
      opts = opts || {};
      document.querySelectorAll('.pane-session-menu').forEach(el => el.remove());
      await this.loadAbandonedSessions();
      const sessions = this.abandonedSessions || [];
      const menu = document.createElement('div');
      menu.className = 'pane-session-menu';
      menu.style.cssText = 'position:fixed;z-index:1000;background:#0b1220;border:1px solid #1f2a40;border-radius:6px;padding:4px;max-height:300px;overflow:auto;min-width:260px;box-shadow:0 8px 24px rgba(0,0,0,0.5);font-size:11px;color:#e5e7eb;';
      if (opts.anchorEl) {
        const rect = opts.anchorEl.getBoundingClientRect();
        menu.style.left = Math.max(8, Math.min(window.innerWidth - 270, rect.right - 260)) + 'px';
        menu.style.top  = (rect.bottom + 4) + 'px';
      } else {
        menu.style.left = '50%';
        menu.style.top  = '120px';
        menu.style.transform = 'translateX(-50%)';
      }
      const hdr = document.createElement('div');
      hdr.style.cssText = 'padding:6px 8px;color:#9ca3af;font-size:10px;text-transform:uppercase;letter-spacing:0.5px;';
      hdr.textContent = opts.title || 'Select a session';
      menu.appendChild(hdr);
      if (sessions.length === 0) {
        const empty = document.createElement('div');
        empty.style.cssText = 'padding:8px;color:#6b7280;font-style:italic;';
        empty.textContent = 'No existing sessions. Use "New session"';
        menu.appendChild(empty);
      } else {
        sessions.forEach(s => {
          const row = document.createElement('div');
          const isCurrent = opts.currentName && s.name === opts.currentName;
          row.style.cssText = 'display:flex;align-items:center;width:100%;border-radius:4px;' + (isCurrent ? 'background:#152033;' : '');
          const item = document.createElement('button');
          item.style.cssText = 'display:flex;align-items:center;gap:6px;flex:1;text-align:left;padding:6px 10px;background:transparent;color:inherit;border:0;cursor:pointer;font-family:monospace;font-size:11px;border-radius:4px;';
          const dot = document.createElement('span');
          dot.style.cssText = 'width:6px;height:6px;border-radius:50%;background:' + (s.attached ? '#fbbf24' : '#10b981') + ';flex-shrink:0;';
          const nameEl = document.createElement('span');
          nameEl.style.cssText = 'flex:1;';
          nameEl.textContent = s.name;
          const statusEl = document.createElement('span');
          statusEl.style.cssText = 'font-size:9px;color:#6b7280;';
          statusEl.textContent = s.attached ? 'in use' : 'free';
          item.appendChild(dot);
          item.appendChild(nameEl);
          item.appendChild(statusEl);
          if (isCurrent) {
            const currentEl = document.createElement('span');
            currentEl.style.cssText = 'font-size:9px;color:#3b82f6;';
            currentEl.textContent = 'current';
            item.appendChild(currentEl);
          }
          item.addEventListener('mouseenter', () => { if (!isCurrent) row.style.background = '#152033'; });
          item.addEventListener('mouseleave', () => { if (!isCurrent) row.style.background = 'transparent'; });
          item.addEventListener('click', (e) => {
            e.stopPropagation();
            menu.remove();
            if (!isCurrent && typeof opts.onPick === 'function') opts.onPick(s.name);
          });
          row.appendChild(item);
          if (!isCurrent) {
            const del = document.createElement('button');
            del.title = 'Delete session';
            del.textContent = '✕';
            del.style.cssText = 'padding:4px 8px;margin-right:4px;background:transparent;color:#6b7280;border:0;cursor:pointer;font-size:12px;border-radius:4px;flex-shrink:0;';
            del.addEventListener('mouseenter', () => { del.style.background = '#3a1212'; del.style.color = '#f87171'; });
            del.addEventListener('mouseleave', () => { del.style.background = 'transparent'; del.style.color = '#6b7280'; });
            del.addEventListener('click', async (e) => {
              e.stopPropagation();
              if (!(await this.confirmAsync('Delete session "' + s.name + '"?' + (s.attached ? '\n\n⚠ It is IN USE by another panel — this will kill the connection there.' : '')))) return;
              try { await this.api('/api/terminal/kill-session', {method:'POST', body: JSON.stringify({name: s.name})}); } catch(err){}
              menu.remove();
              this.openSessionPicker(opts);
            });
            row.appendChild(del);
          }
          menu.appendChild(row);
        });
      }
      const divider = document.createElement('div');
      divider.style.cssText = 'height:1px;background:#1f2a40;margin:4px 0;';
      menu.appendChild(divider);
      const newBtn = document.createElement('button');
      newBtn.style.cssText = 'display:block;width:100%;text-align:left;padding:6px 10px;background:transparent;color:#3b82f6;border:0;cursor:pointer;font-size:11px;border-radius:4px;';
      newBtn.textContent = '+ New session...';
      newBtn.addEventListener('mouseenter', () => newBtn.style.background = '#152033');
      newBtn.addEventListener('mouseleave', () => newBtn.style.background = 'transparent');
      newBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        menu.remove();
        const raw = await this.askInput({ title:'New session', label:'Name of the new session (letters, numbers, _, -):', placeholder:'my-session', validate:(v)=> this._sessionNameError(v) });
        if (raw === null) return;
        const name = (raw || '').replace(/[^A-Za-z0-9_-]/g,'').slice(0,40);
        if (!name) return;
        if (typeof opts.onPick === 'function') opts.onPick(name);
      });
      menu.appendChild(newBtn);
      const onDocClick = (e) => {
        if (!menu.contains(e.target)) {
          menu.remove();
          document.removeEventListener('mousedown', onDocClick, true);
        }
      };
      setTimeout(() => document.addEventListener('mousedown', onDocClick, true), 0);
      document.body.appendChild(menu);
    },
    openPaneSessionPicker(pane, anchorEl){
      this.openSessionPicker({
        anchorEl,
        title: 'Load a session into this panel',
        currentName: pane.sessionName,
        onPick: (name) => this.loadSessionIntoPane(pane, name),
      });
    },
    loadSessionIntoPane(pane, sessionName){
      if (!pane || !sessionName) return;
      try { if (pane.reconnect) pane.reconnect.cancelled = true; } catch(e){}
      try { if (pane.reconnect && pane.reconnect.timer) clearTimeout(pane.reconnect.timer); } catch(e){}
      try { if (pane.notify && pane.notify.timer) clearTimeout(pane.notify.timer); } catch(e){}
      try { if (pane.ws) pane.ws.close(); } catch(e){}
      try { if (pane.term) pane.term.dispose(); } catch(e){}
      try { if (pane.resizeObserver) pane.resizeObserver.disconnect(); } catch(e){}
      pane.term = null; pane.ws = null; pane.fit = null; pane.search = null; pane.resizeObserver = null;
      pane.notify = { lastOutput: 0, cmdStart: 0, busy: false, alerted: false };
      pane.sessionName = sessionName;
      pane.status = 'idle';
      pane.startupRan = true;
      this.renderPaneLayout();
      this.$nextTick(()=> this.mountPane(pane));
      this.saveState();
    },
    _findSplitParent(node, target, parent, side){
      if (!node) return null;
      if (node === target) return { parent, side };
      if (node.type === 'split') {
        return this._findSplitParent(node.a, target, node, 'a') ||
               this._findSplitParent(node.b, target, node, 'b');
      }
      return null;
    },
    _beginDividerDrag(ev, splitNode){
      ev.preventDefault();
      const dir = splitNode.dir;
      const root = ev.target.parentElement;
      const rect = root.getBoundingClientRect();
      const total = (dir === 'row') ? rect.width : rect.height;
      const size0 = splitNode.size;

      const aSpine = []; { let box = size0 * total, cur = splitNode.a;
        while (cur && cur.type === 'split' && cur.dir === dir) {
          aSpine.push({ node: cur, farPx: box * cur.size });
          box *= (1 - cur.size); cur = cur.b;
        } }
      const bSpine = []; { let box = (1 - size0) * total, cur = splitNode.b;
        while (cur && cur.type === 'split' && cur.dir === dir) {
          bSpine.push({ node: cur, farPx: box * (1 - cur.size) });
          box *= cur.size; cur = cur.a;
        } }

      const applyFlex = (containerEl, node) => {
        if (!containerEl || !node || node.type !== 'split') return;
        const aWrap = containerEl.children[0], bWrap = containerEl.children[2];
        if (aWrap) aWrap.style.flex = node.size + ' 1 0%';
        if (bWrap) bWrap.style.flex = (1 - node.size) + ' 1 0%';
        applyFlex(aWrap && aWrap.firstElementChild, node.a);
        applyFlex(bWrap && bWrap.firstElementChild, node.b);
      };

      const onMove = (mv) => {
        let ratio;
        if (dir === 'row') ratio = (mv.clientX - rect.left) / rect.width;
        else               ratio = (mv.clientY - rect.top)  / rect.height;
        ratio = Math.max(0.1, Math.min(0.9, ratio));
        splitNode.size = ratio;
        let box = ratio * total;
        for (const seg of aSpine) { const s = Math.max(0.05, Math.min(0.95, seg.farPx / box)); seg.node.size = s; box *= (1 - s); }
        box = (1 - ratio) * total;
        for (const seg of bSpine) { const s = Math.max(0.05, Math.min(0.95, 1 - seg.farPx / box)); seg.node.size = s; box *= s; }
        applyFlex(root, splitNode);
      };
      const onUp = () => {
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
        document.body.style.cursor = '';
        this._refitAllPanes();
        this.saveState();
      };
      document.body.style.cursor = (dir === 'row' ? 'col-resize' : 'row-resize');
      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
    },
    _beginPaneDrag(ev, pane){
      this.terms._dragPane = { paneId: pane.id };
      try { ev.dataTransfer.effectAllowed = 'move'; ev.dataTransfer.setData('text/plain', pane.id); } catch(e){}
    },
    _endPaneDrag(){
      this.terms._dragPane = null;
      document.querySelectorAll('.pane-drop-overlay').forEach(el => el.remove());
    },
    _panelDragOver(ev, targetPane){
      if (this._dragHasFiles(ev)) {
        ev.preventDefault();
        try { ev.dataTransfer.dropEffect = 'copy'; } catch(_){}
        this._showFileDropOverlay(ev.currentTarget);
        return;
      }
      if (!this.terms._dragPane) return;
      ev.preventDefault();
      ev.dataTransfer.dropEffect = 'move';
      const wrap = ev.currentTarget;
      const rect = wrap.getBoundingClientRect();
      const xRel = (ev.clientX - rect.left) / rect.width;
      const yRel = (ev.clientY - rect.top)  / rect.height;
      let zone;
      if (xRel < 0.25)      zone = 'left';
      else if (xRel > 0.75) zone = 'right';
      else if (yRel < 0.25) zone = 'top';
      else if (yRel > 0.75) zone = 'bottom';
      else                  zone = 'center';
      this._showDropOverlay(wrap, zone);
    },
    _panelDragLeave(pane, ev){
      if (ev && this._dragHasFiles(ev)) {
        const wrap = ev.currentTarget;
        try { if (wrap && ev.relatedTarget && wrap.contains(ev.relatedTarget)) return; } catch(_){}
        this._hideFileDropOverlay(wrap);
      }
    },
    _panelDrop(ev, targetPane){
      ev.preventDefault();
      if (this._dragHasFiles(ev)) {
        this._hideFileDropOverlay(ev.currentTarget);
        const files = (ev.dataTransfer && ev.dataTransfer.files) || [];
        if (files.length) this._sendFilesToPane(targetPane, files).catch(()=>{});
        return;
      }
      const src = this.terms._dragPane;
      this._endPaneDrag();
      if (!src) return;
      if (src.paneId === targetPane.id) return;
      const wrap = ev.currentTarget;
      const rect = wrap.getBoundingClientRect();
      const xRel = (ev.clientX - rect.left) / rect.width;
      const yRel = (ev.clientY - rect.top)  / rect.height;
      let zone;
      if (xRel < 0.25)      zone = 'left';
      else if (xRel > 0.75) zone = 'right';
      else if (yRel < 0.25) zone = 'top';
      else if (yRel > 0.75) zone = 'bottom';
      else                  zone = 'center';
      this._performPaneDrop(src.paneId, targetPane.id, zone);
    },
    _showFileDropOverlay(wrap){
      if (!wrap || wrap.querySelector('.pane-file-drop-overlay')) return;
      const ov = document.createElement('div');
      ov.className = 'pane-file-drop-overlay';
      ov.style.cssText = 'position:absolute;inset:6px;background:rgba(16,185,129,.22);border:2px dashed #10b981;'
        + 'pointer-events:none;z-index:11;border-radius:6px;display:flex;align-items:center;justify-content:center;'
        + 'font:600 13px ui-sans-serif,system-ui;color:#065f46;text-shadow:0 1px 0 rgba(255,255,255,.6);';
      ov.textContent = '📎 drop to attach to the terminal';
      wrap.appendChild(ov);
    },
    _hideFileDropOverlay(wrap){
      try {
        if (wrap) wrap.querySelectorAll('.pane-file-drop-overlay').forEach(el => el.remove());
        else document.querySelectorAll('.pane-file-drop-overlay').forEach(el => el.remove());
      } catch(_){}
    },
    _showDropOverlay(wrap, zone){
      const existing = wrap.querySelector('.pane-drop-overlay');
      if (existing && existing.dataset.zone === zone) return;
      if (existing) existing.remove();
      const ov = document.createElement('div');
      ov.className = 'pane-drop-overlay';
      ov.dataset.zone = zone;
      const base = 'position:absolute;background:rgba(59,130,246,0.35);border:2px solid #3b82f6;pointer-events:none;z-index:10;border-radius:4px;';
      if (zone === 'center') ov.style.cssText = base + 'inset:8px;';
      else if (zone === 'top')    ov.style.cssText = base + 'top:0;left:0;right:0;height:50%;';
      else if (zone === 'bottom') ov.style.cssText = base + 'bottom:0;left:0;right:0;height:50%;';
      else if (zone === 'left')   ov.style.cssText = base + 'top:0;bottom:0;left:0;width:50%;';
      else if (zone === 'right')  ov.style.cssText = base + 'top:0;bottom:0;right:0;width:50%;';
      wrap.appendChild(ov);
    },
    _performPaneDrop(srcPaneId, dstPaneId, zone){
      const srcFound = this._findPaneNode(this.terms.layout, srcPaneId, null, null);
      const dstFound = this._findPaneNode(this.terms.layout, dstPaneId, null, null);
      if (!srcFound || !dstFound) return;
      if (zone === 'center') {
        const tmp = srcFound.node.id;
        srcFound.node.id = dstFound.node.id;
        dstFound.node.id = tmp;
        this.renderPaneLayout();
        this.saveState();
        return;
      }
      const dir = (zone === 'left' || zone === 'right') ? 'row' : 'column';
      this._removePaneFromTree(srcPaneId);
      const newDstFound = this._findPaneNode(this.terms.layout, dstPaneId, null, null);
      if (!newDstFound) return;
      const newPaneNode = { type:'pane', id: srcPaneId };
      const srcFirst = (zone === 'top' || zone === 'left');
      const splitNode = { type:'split', dir, size: 0.5,
                          a: srcFirst ? newPaneNode : newDstFound.node,
                          b: srcFirst ? newDstFound.node : newPaneNode };
      if (!newDstFound.parent) this.terms.layout = splitNode;
      else newDstFound.parent[newDstFound.side] = splitNode;
      this.renderPaneLayout();
      this.saveState();
    },
    _removePaneFromTree(paneId){
      const found = this._findPaneNode(this.terms.layout, paneId, null, null);
      if (!found) return;
      if (!found.parent) { this.terms.layout = null; return; }
      const otherSide = found.side === 'a' ? 'b' : 'a';
      const survivor = found.parent[otherSide];
      if (this.terms.layout === found.parent) { this.terms.layout = survivor; return; }
      const gp = this._findSplitParent(this.terms.layout, found.parent, null, null);
      if (!gp || !gp.parent) this.terms.layout = survivor;
      else gp.parent[gp.side] = survivor;
    },
    async saveCurrentWorkspace(){
      const raw = await this.askInput({ title:'Save workspace', label:'Workspace name:', placeholder:'my-workspace', validate:(v)=>{ const s=String(v||'').trim(); if(!s) return 'Enter a name'; return /^[\p{L}\p{N}_\- .]{1,64}$/u.test(s) ? '' : 'Use letters, numbers, space, -, _, . (max 64)'; } });
      const name = (raw || '').trim();
      if (!name) return;
      const snap = {
        name,
        savedAt: Date.now(),
        namespace: this._termNamespaceOf(this.terms),
        panes: (this.terms.panes||[]).map(p => ({ id: p.id, sessionName: p.sessionName, startupCmd: p.startupCmd || '' })),
        layout: this._serializeLayout(this.terms.layout),
        activePane: this.terms.activePane || null,
      };
      try {
        const tomb = this._termLoadTombstones();
        if (tomb.delete(name)) this._termPersistTombstones(tomb);
      } catch(_){}
      this.termWorkspaces = (this.termWorkspaces || []).filter(w => w.name !== name);
      this.termWorkspaces.push(snap);
      localStorage.setItem(this._termWorkspacesKey, JSON.stringify(this.termWorkspaces));
      this._termPushWorkspace(snap).then(ok => {
        if (!ok) this.showToast('Workspace saved locally only (server unavailable, it retries on the next F5)', 'warn');
      });
    },
    async loadWorkspace(ws){
      if (!ws) return;
      if ((this.terms.panes||[]).length > 0 && !(await this.confirmAsync('Load workspace "'+ws.name+'"? The current panels will be closed (sessions preserved).'))) return;
      (this.terms.panes||[]).forEach(pane => {
        try { if (pane.reconnect) pane.reconnect.cancelled = true; } catch(e){}
        try { if (pane.reconnect && pane.reconnect.timer) clearTimeout(pane.reconnect.timer); } catch(e){}
        try { if (pane.notify && pane.notify.timer) clearTimeout(pane.notify.timer); } catch(e){}
        try { if (pane.ws) pane.ws.close(); } catch(e){}
        try { if (pane.term) pane.term.dispose(); } catch(e){}
      });
      this.terms.panes = [];
      this.terms.layout = null;
      this.terms.activePane = null;

      if (Array.isArray(ws.tabs)) {
        const descriptors = [];
        ws.tabs.forEach(savedTab => {
          const layoutPanes = this._allPanesFromSerialized(savedTab.layout);
          const byId = new Map((savedTab.panes||[]).map(p => [p.id, p]));
          const seen = new Set();
          layoutPanes.forEach(pid => {
            const p = byId.get(pid);
            if (p && !seen.has(pid)) {
              seen.add(pid);
              descriptors.push({ sessionName: p.sessionName, startupCmd: '' });
            }
          });
          (savedTab.panes||[]).forEach(p => {
            if (!seen.has(p.id)) descriptors.push({ sessionName: p.sessionName, startupCmd: '' });
          });
        });
        this._hydrateFromDescriptors(descriptors);
        this.termWorkspacesOpen = false;
        return;
      }

      (ws.panes || []).forEach(p => {
        const pane = this._makePane(p.sessionName, '');
        pane.id = p.id;
        this.terms.panes.push(pane);
      });
      this.terms.layout = ws.layout || (this.terms.panes[0] ? { type:'pane', id: this.terms.panes[0].id } : null);
      this.terms.activePane = ws.activePane || (this.terms.panes[0]?.id || null);
      this.$nextTick(()=>{
        this.renderPaneLayout();
        this.terms.panes.forEach(p => this.mountPane(p));
      });
      this.termWorkspacesOpen = false;
      this.saveState();
    },
    async deleteWorkspace(ws){
      if (!(await this.confirmAsync('Delete workspace "'+ws.name+'"?'))) return;
      this.termWorkspaces = (this.termWorkspaces || []).filter(w => w.name !== ws.name);
      localStorage.setItem(this._termWorkspacesKey, JSON.stringify(this.termWorkspaces));
      this._termAddTombstone(ws.name);
      this._termDeleteWorkspace(ws.name).then(ok => {
        if (!ok) this.showToast('Delete applied locally only (server unavailable, it retries on the next F5)', 'warn');
      });
    },
    installTerminalHotkeys(){
      window.addEventListener('keydown', (ev) => {
        if (this.page !== 'dev') return;
        if (!this.terms.layout) return;
        const inXterm = ev.target && ev.target.closest && ev.target.closest('.xterm');
        if (inXterm) return;
        if (ev.altKey && !ev.ctrlKey && !ev.shiftKey && (ev.key==='ArrowRight'||ev.key==='ArrowDown')) {
          ev.preventDefault(); this._focusAdjacentPane(+1); return;
        }
        if (ev.altKey && !ev.ctrlKey && !ev.shiftKey && (ev.key==='ArrowLeft'||ev.key==='ArrowUp')) {
          ev.preventDefault(); this._focusAdjacentPane(-1); return;
        }
      });
    },
    _focusAdjacentPane(dir){
      const panes = this._allPanes(this.terms.layout);
      if (panes.length < 2) return;
      const idx = panes.findIndex(p => p.id === this.terms.activePane);
      const next = panes[(idx + dir + panes.length) % panes.length];
      if (next) this.focusPane(next.id);
    },
    _openPaneContextMenu(paneId, x, y){
      this.terms.activePane = paneId;
      const menuW = 220, menuH = 280;
      const vx = Math.max(8, Math.min(window.innerWidth - menuW - 8, x));
      const vy = Math.max(8, Math.min(window.innerHeight - menuH - 8, y));
      this.paneContextMenu = { open: true, x: vx, y: vy, paneId };
    },
    paneCtxAction(kind){
      const paneId = this.paneContextMenu.paneId;
      const pane = (this.terms.panes||[]).find(p => p.id === paneId);
      this.paneContextMenu.open = false;
      if (!pane) return;
      switch (kind) {
        case 'load':
          this.openPaneSessionPicker(pane, null);
          break;
        case 'new':
          this.newPane();
          break;
        case 'rename': {
          (async () => {
            const fresh = ((await this.askInput({ title:'Rename panel', label:'Rename panel (cosmetic — the session keeps its name):', value: pane.sessionName || '' })) || '').trim();
            if (fresh) { pane.sessionName = fresh; this.renderPaneLayout(); this.saveState(); }
          })();
          break;
        }
        case 'broadcast':
          this.terms.broadcast = !this.terms.broadcast;
          this.showToast && this.showToast('broadcast '+(this.terms.broadcast?'ON':'OFF'), 'ok');
          break;
        case 'close':
          this.closePane(paneId);
          break;
      }
    },
    focusPaneByName(name){
      const pane = (this.terms.panes||[]).find(p => p.sessionName === name);
      if (!pane) return false;
      this.focusPane(pane.id);
      return true;
    },
    tabStatus(t){
      const panes = t.panes || [];
      if (panes.length === 0) return 'idle';
      const stats = panes.map(p => p.status || 'idle');
      if (stats.every(s => s === 'open')) return 'open';
      if (stats.some(s => s === 'error' || s === 'closed')) return stats.find(s => s==='error'||s==='closed');
      if (stats.some(s => s === 'connecting' || s === 'reconnecting')) return stats.find(s => s==='connecting'||s==='reconnecting');
      return 'idle';
    },
    async killActiveSession(){
      const p = this.activePane(); if (!p) return;
      if (!(await this.confirmAsync('Kill the session "'+p.sessionName+'"? Everything running inside will be terminated.'))) return;
      try { await this.api('/api/terminal/kill-session', {method:'POST', body: JSON.stringify({name: p.sessionName})}); } catch(e){}
      this.closePane(p.id);
    },
    async killSessionByName(name){
      if (!(await this.confirmAsync('Kill session "'+name+'"?'))) return;
      try { await this.api('/api/terminal/kill-session', {method:'POST', body: JSON.stringify({name: name})}); } catch(e){}
      await this.loadAbandonedSessions();
    },

    sessionMgrFmtTime(unixSec){
      if (!unixSec) return '—';
      try { return new Date(unixSec * 1000).toLocaleString('en-US'); } catch(e){ return '—'; }
    },
    sessionMgrStateLabel(s){
      const st = s && s.state;
      switch (st) {
        case 'running':       return '● working';
        case 'waiting_input': return '◐ waiting';
        case 'error':         return '✕ error';
        case 'done':          return '○ done';
        case 'idle':          return '○ idle';
        default:              return s && s.attached ? 'in use' : 'free';
      }
    },
    sessionMgrBasename(p){
      if (!p) return '';
      const parts = String(p).replace(/\/+$/,'').split('/');
      return parts[parts.length-1] || p;
    },
    get amAdmin(){
      const u = (this.usersList || []).find(x => x.username === this.username);
      return !!(u && u.is_admin);
    },
    get sessionAssignTargets(){
      return [ ...(this.usersList || []).map(u => u.username), '*' ];
    },
    sessionAssignLabel(v){ return v === '*' ? 'All' : (v || '—'); },
    async sessionMgrLoad(){
      if (!(this.usersList || []).length) { try { await this.loadUsers(); } catch(e){} }
      const url = this.amAdmin ? '/api/terminal/sessions?all=1' : '/api/terminal/sessions';
      try {
        const r = await this.api(url);
        if (!r.ok) throw new Error('HTTP '+r.status);
        this.sessionMgr.sessions = (await r.json()) || [];
        this.sessionMgr.loadError = '';
      } catch(e){
        this.sessionMgr.loadError = (e && e.message) || 'failed to load';
      }
    },
    async sessionMgrAssign(name, target){
      try {
        const r = await this.api('/api/terminal/assign-session', {method:'POST', body: JSON.stringify({name, target})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('now shows for '+(target === '*' ? 'All' : target),'ok');
      } catch(e){ this.showToast(e.message,'err'); }
      await this.sessionMgrLoad();
    },
    async sessionMgrLoadBackups(){
      try {
        const r = await this.api('/api/terminal/backups');
        this.sessionMgr.backups = (await r.json()) || [];
      } catch(e){ this.sessionMgr.backups = []; }
    },
    sessionMgrBackupGroups(){
      const groups = {};
      for (const b of (this.sessionMgr.backups || [])) {
        for (const s of (b.sessions || [])) {
          const name = (typeof s === 'string') ? s : (s.name || '');
          if (!name) continue;
          const summary = (typeof s === 'string') ? '' : (s.summary || '');
          (groups[name] = groups[name] || []).push({ id: b.id, created: b.created || 0, summary, count: (b.sessions || []).length });
        }
      }
      return Object.keys(groups).sort((a,c)=>a.localeCompare(c)).map(name => {
        const versions = groups[name].sort((a,c)=> (c.created||0) - (a.created||0));
        const summary = (versions.find(v=>v.summary) || {}).summary || '';
        return { name, summary, versions };
      });
    },
    sessionMgrExpandAll(open){
      const ex = {};
      if (open) for (const g of this.sessionMgrBackupGroups()) ex[g.name] = true;
      this.sessionMgr.expanded = ex;
    },
    async sessionMgrDeleteVersion(id, name){
      if (!(await this.confirmAsync('Delete the backup of "'+name+'" from this date?'))) return;
      try {
        const r = await this.api('/api/terminal/backup-delete', {method:'POST', body: JSON.stringify({id, name})});
        if (!r.ok) { const d = await r.json().catch(()=>({})); this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('version deleted','ok');
      } catch(e){ this.showToast(e.message,'err'); }
      await this.sessionMgrLoadBackups();
    },
    async sessionMgrTogglePreview(name){
      this.sessionMgr.previewOpen[name] = !this.sessionMgr.previewOpen[name];
      if (!this.sessionMgr.previewOpen[name]) return;
      this.sessionMgr.preview[name] = '__loading__';
      try {
        const r = await this.api('/api/terminal/preview?name=' + encodeURIComponent(name));
        if (!r.ok) { this.sessionMgr.preview[name] = { headline:'', body:'(could not read the session)' }; return; }
        const d = await r.json();
        this.sessionMgr.preview[name] = { headline: d.headline || '', body: d.body || '' };
      } catch(e){ this.sessionMgr.preview[name] = { headline:'', body:'(error reading: '+e.message+')' }; }
    },
    sessionMgrOpenSession(name){
      this.sessionMgrOpen = false;
      try { this.setPage('terminal'); } catch(e){}
      this.$nextTick(()=>{ try { this.reattachSession(name); } catch(e){} });
    },
    async sessionMgrRename(name){
      const raw = await this.askInput({ title:'Rename session', label:'New name for session "'+name+'" (letters, numbers, _ , -):', value:name, validate:(v)=> this._sessionNameError(v) });
      if (raw === null) return;
      const newName = (raw||'').replace(/[^A-Za-z0-9_-]/g,'').slice(0,40);
      if (!newName || newName === name) return;
      try {
        const r = await this.api('/api/terminal/rename-session', {method:'POST', body: JSON.stringify({name, newName})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('renamed to '+(d.name||newName),'ok');
      } catch(e){ this.showToast(e.message,'err'); }
      await this.sessionMgrLoad();
    },
    async sessionMgrKill(name){
      if (!(await this.confirmAsync('Delete (kill) the session "'+name+'"?\n\nThe processes running in it will be terminated.'))) return;
      try {
        const r = await this.api('/api/terminal/kill-session', {method:'POST', body: JSON.stringify({name})});
        if (!r.ok) { const d = await r.json().catch(()=>({})); this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('session deleted','ok');
      } catch(e){ this.showToast(e.message,'err'); }
      await this.sessionMgrLoad();
    },
    async sessionMgrBackup(name){
      this.sessionMgr.busy = true;
      try {
        const r = await this.api('/api/terminal/backup', {method:'POST', body: JSON.stringify({name})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('backup of "'+name+'" created','ok');
        await this.sessionMgrLoadBackups();
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.sessionMgr.busy = false; }
    },
    async sessionMgrBackupAll(){
      this.sessionMgr.busy = true;
      try {
        const r = await this.api('/api/terminal/backup', {method:'POST', body: JSON.stringify({})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('backup of '+(d.count||0)+' session(s) created','ok');
        await this.sessionMgrLoadBackups();
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.sessionMgr.busy = false; }
    },
    async sessionMgrRestoreAll(id){
      if (!(await this.confirmAsync('Restore ALL sessions from this backup?\n\nSessions already alive under the same name are not overwritten.'))) return;
      await this._sessionMgrRestore(id, '');
    },
    async sessionMgrRestorePick(b){
      const names = (b && b.sessions) || [];
      if (names.length === 0) { this.showToast('backup with no sessions','err'); return; }
      const raw = await this.askInput({ title:'Restore session', label:'Which session should be restored?\n\nAvailable: '+names.join(', '), value:names[0] });
      if (raw === null) return;
      const name = (raw||'').trim();
      if (!name) return;
      this._sessionMgrRestore(b.id, name);
    },
    async _sessionMgrRestore(id, name){
      this.sessionMgr.busy = true;
      try {
        const r = await this.api('/api/terminal/restore', {method:'POST', body: JSON.stringify({id, name})});
        const d = await r.json().catch(()=>({}));
        if (!r.ok) { this.showToast('error: '+(d.error||r.status),'err'); return; }
        let msg = (d.restored||0)+' session(s) restored';
        if (d.skipped) msg += ' · '+d.skipped+' skipped (already existed or the cap was reached)';
        this.showToast(msg, d.restored ? 'ok' : 'err');
        this.sessionMgr.tab = 'sessions';
        await this.sessionMgrLoad();
      } catch(e){ this.showToast(e.message,'err'); }
      finally { this.sessionMgr.busy = false; }
    },
    async sessionMgrDeleteBackup(id){
      if (!(await this.confirmAsync('Delete this backup?'))) return;
      try {
        const r = await this.api('/api/terminal/backup-delete', {method:'POST', body: JSON.stringify({id})});
        if (!r.ok) { const d = await r.json().catch(()=>({})); this.showToast('error: '+(d.error||r.status),'err'); return; }
        this.showToast('backup deleted','ok');
      } catch(e){ this.showToast(e.message,'err'); }
      await this.sessionMgrLoadBackups();
    },
    async loadAbandonedSessions(){
      this.abandonedSessionsLoading = true;
      try { const r = await this.api('/api/terminal/sessions'); this.abandonedSessions = (await r.json()) || []; this._loadOk('abandonedSessions'); }
      catch(e){ this._loadErr('abandonedSessions', e, 'failed to list terminal sessions'); }
      finally { this.abandonedSessionsLoading = false; }
    },
    async showAbandonedSessions(){ await this.loadAbandonedSessions(); this.abandonedOpen = true; },
    reattachSession(name){
      this.abandonedOpen = false;
      if (this.focusPaneByName(name)) return;
      this.newPane(name, { isReattach: true });
    },
    async openHostTerminal(){
      if ((this.terms.panes||[]).length > 0) {
        this.$nextTick(()=>{
          this.renderPaneLayout();
          this.$nextTick(()=>{
            (this.terms.panes || []).forEach(p => {
              if (!p.term) this.mountPane(p);
            });
            this._refitAllPanes();
          });
        });
        return;
      }
      try { await this.loadAbandonedSessions(); } catch(e){}
      const existing = (this.abandonedSessions || [])[0];
      const targetName = existing ? existing.name : 'main';
      this.newPane(targetName, { isReattach: !!existing });
    },
    newSessionPrompted(){
      this.createSess = { open: true, name: '', cwd: '', cmd: 'bash', account: '', busy: false, err: '' };
    },
    get createSessCwdOptions(){
      const set = new Set();
      (this.sessionMgr.sessions || []).forEach(s => { if (s.cwd) set.add(s.cwd); });
      (this.abandonedSessions || []).forEach(s => { if (s.cwd) set.add(s.cwd); });
      return Array.from(set).sort();
    },
    _createSessStartupCmd(cmd){
      switch (cmd) {
        case 'claude':          return 'claude';
        case 'claude-continue': return 'claude --continue';
        case 'claude-resume':   return 'claude --resume';
        default:                return '';
      }
    },
    async createSessSubmit(){
      const c = this.createSess;
      const name = (c.name || '').replace(/[^A-Za-z0-9_-]/g,'').slice(0,40);
      if (!name) { c.err = 'invalid name (letters, numbers, _ , -)'; return; }
      if (this.focusPaneByName(name)) { c.open = false; return; }
      c.busy = true; c.err = '';
      try {
        await this.api('/api/terminal/create', { method:'POST', body: JSON.stringify({ name, cwd: c.cwd || '', account: c.account || '' }) });
      } catch(e){
        c.busy = false; c.err = (e && e.message) ? e.message : 'failed to create a session'; return;
      }
      c.busy = false; c.open = false;
      this.abandonedOpen = false;
      const startup = this._createSessStartupCmd(c.cmd);
      this.newPane(name, startup ? { startupCmd: startup } : undefined);
      try { this.sessionMgrOpen = false; } catch(_){}
    },

    hostTermReconnectNow(){
      const p = this.activePane(); if (!p) return;
      if (p.reconnect && p.reconnect.timer) { clearTimeout(p.reconnect.timer); p.reconnect.timer=null; }
      if (p.reconnect) p.reconnect.attempts = 0;
      if (p.ws) try{ p.ws.close(1000, 'user-reconnect'); }catch(e){}
      try { if (p.term) p.term.write('\r\n\x1b[36m[reconnecting now…]\x1b[0m\r\n'); } catch(_){}
    },
    _termFontSize(){ return this.isMobile() ? (this.hostTermFontSizeMobile||11) : (this.hostTermFontSize||13); },
    hostTermFontDelta(d){
      if (this.isMobile()) {
        this.hostTermFontSizeMobile = Math.max(6, Math.min(28, (this.hostTermFontSizeMobile||11) + d));
        localStorage.setItem('panel_term_fontsize_mobile', String(this.hostTermFontSizeMobile));
      } else {
        this.hostTermFontSize = Math.max(8, Math.min(28, (this.hostTermFontSize||13) + d));
        localStorage.setItem('panel_term_fontsize', String(this.hostTermFontSize));
      }
      const fs = this._termFontSize();
      (this.terms.panes||[]).forEach(p => {
        if (p.term) { p.term.options.fontSize = fs; this._fitSoon(p.fit); }
      });
    },
    termBarDef(key){ return (this.termBarCatalog||[]).find(b => b.key === key) || { key, icon:'?', label:key }; },
    toggleTermBarButton(key){
      const i = this.termBarButtons.indexOf(key);
      if (i >= 0) this.termBarButtons.splice(i, 1); else this.termBarButtons.push(key);
      try { localStorage.setItem('panel_term_bar', JSON.stringify(this.termBarButtons)); } catch(_){}
    },
    termBarAction(key){
      switch (key) {
        case 'aa':        this.hostTermSettingsOpen = true; break;
        case 'clear':     this.hostTermClear(); break;
        case 'reconnect': this.hostTermReconnectNow(); break;
        case 'tabs':      this.tabMgrOpen = true; break;
        case 'keyboard':  this.kbOpen ? this.dismissKeyboard() : this.summonKeyboard(); break;
        case 'keys':      this.hostMobileToolbar = !this.hostMobileToolbar; try { localStorage.setItem('panel_term_mobile_toolbar', this.hostMobileToolbar?'1':'0'); } catch(_){} break;
        case 'search':    this.hostTermSearch(); break;
        case 'kill':      this.killActiveSession(); break;
        case 'focus':     this.toggleFocusMode(); break;
        case 'sessions':   this.sessionMgrOpen = true; this.sessionMgr.tab = 'sessions'; this.sessionMgrLoad(); break;
        case 'snippets':  this.hostTermSnippetsOpen = !this.hostTermSnippetsOpen; break;
        case 'hide':      this.hideTermChrome(); break;
      }
    },
    async renamePane(pane){
      if (!pane) return;
      const fresh = ((await this.askInput({ title:'Rename panel', label:'Rename panel (cosmetic — the session keeps its name):', value: pane.sessionName || '' })) || '').trim();
      if (fresh) { pane.sessionName = fresh; this.renderPaneLayout(); this.saveState(); }
    },
    hostTermClear(){ const p = this.activePane(); if (p && p.term) p.term.clear(); },
    toggleFocusMode(){
      this.focusMode = !this.focusMode;
      this.$nextTick(()=>{ this._refitAllPanes(); });
    },
    toggleChromeCollapsed(){
      this.chromeCollapsed = !this.chromeCollapsed;
      try { localStorage.setItem('panel_chrome_collapsed', this.chromeCollapsed ? '1' : '0'); } catch(_) {}
      this.$nextTick(() => {
        if (typeof this._refitAllPanes === 'function') this._refitAllPanes();
        if (this.page === 'dev' && typeof this.renderPaneLayout === 'function') this.renderPaneLayout();
      });
    },

    sttToggleForElement(elId, path) {
      if (this.stt.session) { this.sttStop(); return; }
      this.sttStart(elId, path);
    },
    sttStart(elId, path) {
      if (!window.PanelSTT || !window.PanelSTT.isSupported()) {
        this.showToast('Your browser does not support voice dictation', 'err');
        return;
      }
      const el = elId ? document.getElementById(elId) : null;
      const baseText = path ? this._sttGetPath(path) : (el ? el.value : '');
      this.stt.targetElId = elId || '';
      this.stt.targetPath = path || '';
      this.stt.baseText = baseText || '';
      this.stt.interim = '';
      const handle = window.PanelSTT.start({
        continuous: true,
        interimResults: true,
        lang: window.PanelSTT.defaultLang(),
        idleMs: 10000,
        onPartial: (p) => {
          this.stt.interim = p.text;
          this._sttRenderInline();
        },
        onFinal: (p) => {
          const sep = this.stt.baseText && !/\s$/.test(this.stt.baseText) ? ' ' : '';
          const next = (this.stt.baseText + sep + p.buffer).trim();
          if (this.stt.targetPath) this._sttSetPath(this.stt.targetPath, next);
          this.stt.baseText = next;
          this.stt.interim = '';
          this._sttRenderInline();
        },
        onError: (e) => {
          if (e.fatal) {
            this.showToast('Dictation: ' + (e.code === 'not-allowed' ? 'microphone permission denied' : e.code), 'err');
          }
        },
        onEnd: () => {
          this.stt.session = null;
          this.stt.interim = '';
          this._sttRenderInline();
        },
      });
      if (handle) this.stt.session = handle;
    },
    sttStop() {
      if (this.stt.session && this.stt.session.stop) {
        try { this.stt.session.stop(); } catch (_) {}
      }
      this.stt.session = null;
      this.stt.interim = '';
      this._sttRenderInline();
    },
    _sttGetPath(path) {
      const parts = path.split('.');
      let cur = this;
      for (const p of parts) {
        if (cur == null) return '';
        cur = cur[p];
      }
      return cur || '';
    },
    _sttSetPath(path, val) {
      const parts = path.split('.');
      const last = parts.pop();
      let cur = this;
      for (const p of parts) cur = cur[p];
      cur[last] = val;
    },
    _sttRenderInline() {
      // For UX, show the interim text in gray inline in the field (a "dictating" look).
      // The Alpine x-model does not diff sub-strings, so it only paints a placeholder.
      // In the real field, only the final text goes in. The interim shows in the floating badge.
    },
    _sttStartForDOM(targetEl) {
      if (!window.PanelSTT || !window.PanelSTT.isSupported()) {
        this.showToast('Your browser does not support voice dictation', 'err');
        return;
      }
      const baseText = targetEl.value || '';
      this.stt.targetElId = targetEl.id;
      this.stt.targetPath = '';
      this.stt.baseText = baseText;
      this.stt.interim = '';
      const dispatchInput = () => { try { targetEl.dispatchEvent(new Event('input', { bubbles: true })); } catch (_) {} };
      const handle = window.PanelSTT.start({
        continuous: true, interimResults: true,
        lang: window.PanelSTT.defaultLang(),
        idleMs: 10000,
        onPartial: (p) => { this.stt.interim = p.text; },
        onFinal: (p) => {
          const sep = this.stt.baseText && !/\s$/.test(this.stt.baseText) ? ' ' : '';
          const next = (this.stt.baseText + sep + p.buffer).trim();
          targetEl.value = next;
          this.stt.baseText = next;
          this.stt.interim = '';
          dispatchInput();
        },
        onError: (e) => {
          if (e.fatal) this.showToast('Dictation: ' + e.code, 'err');
        },
        onEnd: () => { this.stt.session = null; this.stt.interim = ''; dispatchInput(); },
      });
      if (handle) {
        this.stt.session = handle;
        this.showToast('Dictating — Ctrl+Shift+M to stop', 'ok');
      }
    },
    openTerminalAtPath(path){ this.openTerminalIn(path, 'files'); },
    openTerminalIn(path, prefix){
      const p = (path || '/').toString();
      const short = (p.split('/').filter(Boolean).slice(-2).join('-') || 'root').slice(0, 26);
      const paneName = ((prefix || 'term') + '-' + short).slice(0, 40);
      this.setPage('terminal');
      this.$nextTick(()=>{
        if (this.focusPaneByName(paneName)) return;
        this.newPane(paneName, { startupCmd: 'cd ' + this._shellQuote(p) });
      });
    },
    _shellQuote(s){
      return "'" + String(s).replace(/'/g, "'\\''") + "'";
    },
    hostTermSearch(){
      this.hostTermSearchOpen = !this.hostTermSearchOpen;
      if (this.hostTermSearchOpen) this.$nextTick(()=>{ const i=document.getElementById('host-term-search-input'); if(i){ i.value=''; i.focus(); }});
    },
    hostTermSearchClose(){
      this.hostTermSearchOpen=false;
      const p = this.activePane();
      if (p && p.search) try{ p.search.clearDecorations(); }catch(e){}
      if (p && p.term) p.term.focus();
    },
    hostTermSearchNext(reverse){
      const i = document.getElementById('host-term-search-input'); if (!i) return;
      const p = this.activePane(); if (!p || !p.search) return;
      const q = i.value; if (!q) { this.hostTermSearchMatchCount = ''; return; }
      const opts = {
        caseSensitive: !!this.hostTermSearchCase,
        decorations: { matchBackground:'#facc15', activeMatchBackground:'#f97316', matchOverviewRuler:'#facc15', activeMatchColorOverviewRuler:'#f97316' }
      };
      let found;
      if (reverse) found = p.search.findPrevious(q, opts); else found = p.search.findNext(q, opts);
      this.hostTermSearchMatchCount = found ? '✓' : 'none';
    },
    hostStatusLabel(){
      const p = this.activePane();
      if (!p) return '○ idle';
      const s = p.status;
      if (s==='open') return p.netLabel ? ('● connected · ' + p.netLabel) : '● connected';
      if (s==='connecting') return '◌ connecting…';
      if (s==='reconnecting') return '◌ reconnecting ('+(p.reconnect?.attempts||0)+')';
      if (s==='closed') return '○ disconnected';
      if (s==='error') return '⚠ error';
      return '○ idle';
    },

    openTermCtxMenu(state, ev){
      let sel = '';
      if (state && typeof state._customSelection === 'string' && state._customSelection) {
        sel = state._customSelection;
      } else if (state && typeof state._selBeforeCtx === 'string' && state._selBeforeCtx) {
        sel = state._selBeforeCtx;
      } else if (state && state.term) {
        sel = state.term.getSelection() || '';
      }
      if (!sel) {
        try {
          const ns = window.getSelection && window.getSelection();
          const nsTxt = ns ? (ns.toString() || '') : '';
          if (nsTxt.trim()) sel = nsTxt;
        } catch(_) {}
      }
      const trimmed = sel.trim();
      let url = '', ip = '', path = '';
      if (trimmed) {
        const mUrl  = trimmed.match(/^(https?:\/\/[^\s]+)$/);
        const mIp   = trimmed.match(/^(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$/);
        const mIpHost = trimmed.match(/^([a-zA-Z][a-zA-Z0-9_-]*@\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})$/);
        if (mUrl) url = mUrl[1];
        if (mIp) ip = mIp[1];
        if (!ip && mIpHost) ip = trimmed;
        if (!url && !ip && (trimmed.startsWith('/') || trimmed.startsWith('~/')) && !/\s/.test(trimmed)) path = trimmed;
      }
      const isPane = !!(state && state.id && state.type === 'pane');
      const hasMarks = isPane && !!(state.marks && state.marks.length > 0);
      const menuW = 360, menuH = 480;
      const vx = Math.max(8, Math.min(window.innerWidth  - menuW - 8, ev.clientX));
      const vy = Math.max(8, Math.min(window.innerHeight - menuH - 8, ev.clientY));
      this.termCtxMenu = {
        open: true, x: vx, y: vy, state,
        selection: sel, url, ip, path,
        clipboard: false, clipPreview: '',
        hasMarks, isPane,
        paneName: isPane ? (state.sessionName || state.id) : '',
        _markIdx: -1,
      };
      if (isPane) this.terms.activePane = state.id;
      (async () => {
        try {
          if (navigator.clipboard && navigator.clipboard.readText) {
            const t = await navigator.clipboard.readText();
            if (t && this.termCtxMenu.open) {
              this.termCtxMenu.clipboard = true;
              const single = t.replace(/\n/g, '↵');
              this.termCtxMenu.clipPreview = single.length > 60 ? (single.slice(0, 58) + '…') : single;
            }
          }
        } catch(_) { /* no permission: I still offer "Paste", just without the preview */ }
      })();
    },
    closeTermCtxMenu(){
      if (this.termCtxMenu.state) {
        try { this.termCtxMenu.state._selBeforeCtx = ''; } catch(_) {}
      }
      this.termCtxMenu.open = false;
    },
    termCtxRun(kind){
      const m = this.termCtxMenu;
      const state = m.state;
      const isPane = m.isPane;
      const pane = isPane ? (this.terms.panes||[]).find(p => p.id === state.id) : null;
      const sel = m.selection;
      const url = m.url, ip = m.ip, path = m.path;
      this.closeTermCtxMenu();
      switch (kind) {
        case 'copy': {
          let txt = (state && state._customSelection) ? state._customSelection : sel;
          if (!txt && state && state.term) { try { txt = state.term.getSelection() || ''; } catch(_) {} }
          if (!txt) {
            try {
              const ns = window.getSelection && window.getSelection();
              const nsTxt = ns ? (ns.toString() || '') : '';
              if (nsTxt.trim()) txt = nsTxt;
            } catch(_) {}
          }
          if (txt) {
            navigator.clipboard.writeText(txt)
              .then(() => this.showToast('📋 Copied (' + txt.length + ' chars)', 'ok'))
              .catch(() => this.showToast('⚠ Copy failed — clipboard blocked by the browser', 'err'));
          } else {
            this.showToast('Select text first (drag with the left button)', '');
          }
          break;
        }
        case 'copy-screen': {
          if (state && state.term && state.term.buffer && state.term.buffer.active) {
            const buf = state.term.buffer.active;
            const lines = [];
            const start = buf.viewportY;
            const end = Math.min(buf.length, start + state.term.rows);
            for (let y = start; y < end; y++) {
              const line = buf.getLine(y);
              lines.push(line ? line.translateToString(true) : '');
            }
            const txt = lines.join('\n').replace(/\n+$/, '');
            if (txt) {
              navigator.clipboard.writeText(txt)
                .then(() => this.showToast('📋 Screen copied (' + txt.length + ' chars)', 'ok'))
                .catch(() => this.showToast('⚠ Copy failed', 'err'));
            }
          }
          break;
        }
        case 'copy-all': {
          if (state && state.term && state.term.buffer && state.term.buffer.active) {
            const buf = state.term.buffer.active;
            const lines = [];
            const total = buf.length;
            for (let y = 0; y < total; y++) {
              const line = buf.getLine(y);
              lines.push(line ? line.translateToString(true) : '');
            }
            let txt = lines.join('\n').replace(/\n+$/, '');
            const MAX = 500000;
            const truncated = txt.length > MAX;
            if (truncated) txt = txt.slice(-MAX);
            if (txt) {
              const msg = truncated
                ? '📋 Copied last ' + txt.length + ' chars (buffer truncated)'
                : '📋 Buffer copied (' + txt.length + ' chars)';
              navigator.clipboard.writeText(txt)
                .then(() => this.showToast(msg, 'ok'))
                .catch(() => this.showToast('⚠ Copy failed', 'err'));
            }
          }
          break;
        }
        case 'send-selection':
          if (sel && state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: sel}));
          break;
        case 'run-selection':
          if (sel && state.ws && state.ws.readyState===1) {
            const payload = sel.endsWith('\n') ? sel : sel + '\n';
            state.ws.send(JSON.stringify({type:'input', data: payload}));
          }
          break;
        case 'search-web':
          if (sel) window.open('https://duckduckgo.com/?q=' + encodeURIComponent(sel), '_blank', 'noopener');
          break;
        case 'open-url':
          if (url) window.open(url, '_blank', 'noopener');
          break;
        case 'ssh-ip':
          if (ip && state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: 'ssh ' + ip + '\n'}));
          break;
        case 'ping-ip':
          if (ip && state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: 'ping -c 4 ' + ip + '\n'}));
          break;
        case 'open-path-files':
          if (path) { this.setPage && this.setPage('files'); this.$nextTick(()=>{ if (this.openFilesAt) this.openFilesAt(path); else if (this.filePath !== undefined) this.filePath = path; }); }
          break;
        case 'paste':
          this._pasteIntoPane(state).catch(()=>{});
          break;
        case 'attach-file':
          this._pickFilesForPane(state);
          break;
        case 'paste-run':
          (async () => {
            try {
              const t = await navigator.clipboard.readText();
              if (t && state.ws && state.ws.readyState===1) {
                const payload = t.endsWith('\n') ? t : t + '\n';
                state.ws.send(JSON.stringify({type:'input', data: payload}));
              }
            } catch(_){}
          })();
          break;
        case 'select-all':
          try { state.term.selectAll(); } catch(_){}
          break;
        case 'search':
          this.hostTermSearchOpen = true;
          this.$nextTick(()=>{ const i=document.getElementById('host-term-search-input'); if(i){ i.value=''; i.focus(); }});
          break;
        case 'clear':
          try { state.term.clear(); } catch(_){}
          break;
        case 'reset':
          this._termResetState(state);
          break;
        case 'save-scrollback':
          this._termSaveScrollback(state);
          break;
        case 'jump-prev-prompt':
          this._termJumpMark(state, -1);
          break;
        case 'jump-next-prompt':
          this._termJumpMark(state, +1);
          break;
        case 'pane-new':
          this.newPane();
          break;
        case 'pane-split-h':
          if (pane) this.splitPane(pane.id, 'column');
          break;
        case 'pane-split-v':
          if (pane) this.splitPane(pane.id, 'row');
          break;
        case 'pane-broadcast':
          this.terms.broadcast = !this.terms.broadcast;
          this.showToast && this.showToast('broadcast '+(this.terms.broadcast?'ON':'OFF'), 'ok');
          break;
        case 'pane-rename': {
          if (!pane) break;
          (async () => {
            const fresh = ((await this.askInput({ title:'Rename panel', label:'Rename panel (cosmetic — the server keeps the name):', value: pane.sessionName || '' })) || '').trim();
            if (fresh) { pane.sessionName = fresh; this.renderPaneLayout(); this.saveState(); }
          })();
          break;
        }
        case 'pane-load':
          if (pane) this.openPaneSessionPicker(pane, null);
          break;
        case 'pane-reconnect':
          this.hostTermReconnectNow();
          break;
        case 'pane-detach':
          if (pane) {
            this.closePane(pane.id);
            this.showToast && this.showToast('panel closed — the session stays alive (dtach)', 'ok');
          }
          break;
        case 'sig-int':
          if (state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: '\x03'}));
          break;
        case 'sig-eof':
          if (state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: '\x04'}));
          break;
        case 'sig-susp':
          if (state.ws && state.ws.readyState===1) state.ws.send(JSON.stringify({type:'input', data: '\x1a'}));
          break;
        case 'pane-kill':
          this.killActiveSession();
          break;
        case 'pane-close':
          if (pane) this.closePane(pane.id);
          break;
      }
    },
    _showStickyIndicator(state){
      if (!state || !state.id) return;
      const paneEl = document.getElementById('host-pane-'+state.id);
      if (!paneEl) return;
      let chip = paneEl.querySelector('.sticky-sel-chip');
      if (chip) return;
      chip = document.createElement('div');
      chip.className = 'sticky-sel-chip';
      chip.textContent = '🔒 Selection locked · right click to copy · Esc releases';
      paneEl.appendChild(chip);
    },
    _hideStickyIndicator(state){
      if (!state || !state.id) return;
      const paneEl = document.getElementById('host-pane-'+state.id);
      if (!paneEl) return;
      const chip = paneEl.querySelector('.sticky-sel-chip');
      if (chip) chip.remove();
    },
    _manualDragSelect(state, el, downEv){
      const term = state.term;
      if (!term || !term.buffer || !term.buffer.active) return;
      const screen = el.querySelector('.xterm-screen') || el;
      const sRect = screen.getBoundingClientRect();
      const cols = term.cols;
      const rows = term.rows;
      const cellW = sRect.width / cols;
      const cellH = sRect.height / rows;
      if (!cellW || !cellH) return;

      const toCell = (clientX, clientY) => {
        const col = Math.max(0, Math.min(cols, Math.floor((clientX - sRect.left) / cellW)));
        const rowInView = Math.max(0, Math.min(rows - 1, Math.floor((clientY - sRect.top) / cellH)));
        return { col, row: rowInView + term.buffer.active.viewportY };
      };

      this._clearCustomSelection(state);

      try { term.focus(); } catch(_){}

      const start = toCell(downEv.clientX, downEv.clientY);
      let end = start;
      let dragged = false;
      const DRAG_THRESHOLD = 3;
      const ox = downEv.clientX, oy = downEv.clientY;

      const overlay = document.createElement('div');
      overlay.className = 'panel-sel-overlay';
      overlay.style.cssText = 'position:fixed;left:0;top:0;pointer-events:none;z-index:1000;';
      document.body.appendChild(overlay);
      state._selOverlay = overlay;

      const normalize = () => {
        let aCol = start.col, aRow = start.row, bCol = end.col, bRow = end.row;
        if (aRow > bRow || (aRow === bRow && aCol > bCol)) {
          [aCol, bCol] = [bCol, aCol]; [aRow, bRow] = [bRow, aRow];
        }
        return { aCol, aRow, bCol, bRow };
      };

      const redrawOverlay = () => {
        overlay.innerHTML = '';
        const sR = screen.getBoundingClientRect();
        const { aCol, aRow, bCol, bRow } = normalize();
        const vY = term.buffer.active.viewportY;
        for (let r = aRow; r <= bRow; r++) {
          const rowInView = r - vY;
          if (rowInView < 0 || rowInView >= rows) continue;
          const c0 = (r === aRow) ? aCol : 0;
          const c1 = (r === bRow) ? bCol : cols;
          if (c1 <= c0) continue;
          const div = document.createElement('div');
          const left = sR.left + c0 * cellW;
          const top  = sR.top  + rowInView * cellH;
          const w    = (c1 - c0) * cellW;
          const h    = cellH;
          div.style.cssText =
            'position:fixed;' +
            'left:' + left + 'px;' +
            'top:' + top + 'px;' +
            'width:' + w + 'px;' +
            'height:' + h + 'px;' +
            'background:rgba(120,170,255,0.45);' +
            'border-radius:1px;';
          overlay.appendChild(div);
        }
      };

      const extractText = () => {
        const { aCol, aRow, bCol, bRow } = normalize();
        const buf = term.buffer.active;
        const out = [];
        for (let r = aRow; r <= bRow; r++) {
          const line = buf.getLine(r);
          if (!line) { out.push(''); continue; }
          let s;
          try {
            const c0 = (r === aRow) ? aCol : 0;
            const c1 = (r === bRow) ? bCol : cols;
            s = line.translateToString(false, c0, c1);
          } catch(_) {
            const full = line.translateToString(false);
            const c0 = (r === aRow) ? aCol : 0;
            const c1 = (r === bRow) ? bCol : full.length;
            s = full.slice(c0, c1);
          }
          if ((r === aRow ? aCol : 0) === 0 && (r === bRow ? bCol : cols) === cols) {
            s = s.replace(/\s+$/, '');
          }
          out.push(s);
        }
        return out.join('\n');
      };

      const onMove = (mv) => {
        if (!dragged) {
          if (Math.abs(mv.clientX - ox) < DRAG_THRESHOLD && Math.abs(mv.clientY - oy) < DRAG_THRESHOLD) return;
          dragged = true;
        }
        end = toCell(mv.clientX, mv.clientY);
        redrawOverlay();
      };
      const onUp = () => {
        document.removeEventListener('mousemove', onMove, true);
        document.removeEventListener('mouseup', onUp, true);
        if (dragged) {
          const txt = extractText();
          state._customSelection = txt || '';
        } else {
          this._clearCustomSelection(state);
        }
        try { term.focus(); } catch(_){}
      };
      document.addEventListener('mousemove', onMove, true);
      document.addEventListener('mouseup', onUp, true);
    },
    _clearCustomSelection(state){
      if (!state) return;
      if (state._selOverlay) {
        try { state._selOverlay.remove(); } catch(_){}
        state._selOverlay = null;
      }
      state._customSelection = '';
    },
    _stickyRedispatch(el, ev){
      const screen = el.querySelector('.xterm-screen') || el;
      const fake = new MouseEvent('mousedown', {
        bubbles: true, cancelable: true,
        view: window,
        clientX: ev.clientX, clientY: ev.clientY,
        screenX: ev.screenX, screenY: ev.screenY,
        button: 0, buttons: 1,
        detail: 1,
        ctrlKey: false, altKey: false, metaKey: false, shiftKey: false,
      });
      fake._stickySynth = true;
      screen.dispatchEvent(fake);
    },
    _termResetState(state){
      try { state.term.reset(); } catch(_){}
      if (state.ws && state.ws.readyState===1) {
        try { state.ws.send(JSON.stringify({type:'input', data: ' reset\n'})); } catch(_){}
      }
      this.showToast && this.showToast('terminal reset', 'ok');
    },
    _termSaveScrollback(state){
      try {
        const term = state.term;
        if (!term) return;
        const buf = term.buffer.active;
        const lines = [];
        const total = buf.length;
        for (let i = 0; i < total; i++) {
          const line = buf.getLine(i);
          if (!line) continue;
          lines.push(line.translateToString(true));
        }
        while (lines.length && !lines[lines.length-1].trim()) lines.pop();
        const text = lines.join('\n') + '\n';
        const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0,19);
        const name = (state.sessionName || 'terminal') + '-' + stamp + '.txt';
        const blob = new Blob([text], {type:'text/plain;charset=utf-8'});
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url; a.download = name;
        document.body.appendChild(a); a.click();
        setTimeout(()=>{ URL.revokeObjectURL(url); a.remove(); }, 200);
        this.showToast && this.showToast('scrollback saved: '+name, 'ok');
      } catch(e) {
        this.showToast && this.showToast('failed to save the scrollback: '+e.message, 'err');
      }
    },
    _termJumpMark(state, dir){
      const marks = (state.marks || []).filter(m => m.marker && !m.marker.isDisposed);
      if (marks.length === 0) {
        this.showToast && this.showToast('no prompt mark — shell without OSC 133 integration', 'err');
        return;
      }
      state._markIdx = (state._markIdx ?? marks.length);
      let idx = state._markIdx + dir;
      if (idx < 0) idx = marks.length - 1;
      if (idx >= marks.length) idx = 0;
      state._markIdx = idx;
      try {
        state.term.scrollToLine(marks[idx].marker.line);
      } catch(_){}
    },

    applyTermPrimer(){
      let mib = parseInt(this.hostTermPrimerMiB, 10);
      if (!isFinite(mib) || mib < 0) mib = 0;
      if (mib > 16) mib = 16;
      this.hostTermPrimerMiB = mib;
      localStorage.setItem('panel_term_primer_bytes', String(mib * 1048576));
      this.showToast && this.showToast(mib ? ('history on open: ' + mib + ' MiB (takes effect the next time the session opens)')
                                           : 'history on open disabled', 'ok');
    },
    applyTermTheme(){
      localStorage.setItem('panel_term_theme', this.hostTermTheme);
      const theme = this.termThemes[this.hostTermTheme] || this.termThemes.dark;
      (this.terms.panes||[]).forEach(p => {
        if (p.term) p.term.options.theme = { background:theme.background, foreground:theme.foreground, cursor:theme.cursor, selectionBackground:theme.selectionBackground };
      });
    },
    applyTermBell(){
      localStorage.setItem('panel_term_bell', this.hostTermBell);
      const mode = this.hostTermBell;
      (this.terms.panes||[]).forEach(p => {
        if (p.term) p.term.options.bellStyle = mode === 'sound' ? 'sound' : 'none';
      });
    },
    applyTermCursor(){
      localStorage.setItem('panel_term_cursor_style', this.hostTermCursorStyle);
      localStorage.setItem('panel_term_cursor_blink', this.hostTermCursorBlink ? '1' : '0');
      (this.terms.panes||[]).forEach(p => {
        if (p.term) {
          p.term.options.cursorStyle = this.hostTermCursorStyle;
          p.term.options.cursorBlink = this.hostTermCursorBlink;
        }
      });
    },
    applyTermRenderer(){
      localStorage.setItem('panel_term_gpu', this.hostTermGpu ? '1' : '0');
      localStorage.setItem('panel_term_ligatures', this.hostTermLigatures ? '1' : '0');
      if (!this.hostTermGpu) {
        let disposed = 0;
        (this.terms.panes||[]).forEach(p => {
          if (p && p._webgl) { try { p._webgl.dispose(); } catch(_){} p._webgl = null; disposed++; }
        });
        this.showToast && this.showToast('GPU off — DOM renderer active' + (disposed?' (applied already)':' (takes effect on the next terminals)'), 'ok');
      } else {
        this.showToast && this.showToast('reload the page (F5) to apply', '');
      }
    },
    termRedraw(){
      const p = (this.terms.panes||[]).find(x => x.id === this.terms.activePane) || (this.terms.panes||[])[0];
      try { if (p && p.term) { p.term.clearTextureAtlas && p.term.clearTextureAtlas(); p.term.refresh(0, p.term.rows - 1); } } catch(_){}
      try { if (this.detail && this.detail.term) { this.detail.term.refresh(0, this.detail.term.rows - 1); } } catch(_){}
      this.showToast && this.showToast('terminal redrawn', 'ok');
    },
    applyTermScrollback(){
      const v = Math.max(500, Math.min(50000, this.hostTermScrollback || 10000));
      this.hostTermScrollback = v;
      localStorage.setItem('panel_term_scrollback', String(v));
      (this.terms.panes||[]).forEach(p => {
        if (p.term) p.term.options.scrollback = v;
      });
    },
    applyTermAlertPattern(){
      localStorage.setItem('panel_term_alert_pattern', this.hostTermAlertPattern || '');
    },
    playBellPreview(){
      if (this.hostTermBell === 'sound') {
        try {
          const ctx = new (window.AudioContext||window.webkitAudioContext)();
          const o = ctx.createOscillator();
          const g = ctx.createGain();
          o.connect(g); g.connect(ctx.destination);
          o.frequency.value = 880; g.gain.value = 0.15;
          o.start(); setTimeout(() => { o.stop(); ctx.close(); }, 120);
        } catch(_) {}
      } else if (this.hostTermBell === 'visual') {
        const p = this.activePane();
        if (p && p.term && p.term.element) {
          p.term.element.style.outline = '2px solid #facc15';
          setTimeout(() => { p.term.element.style.outline = ''; }, 200);
        }
      }
    },
    _loadUserSnippets(){
      try {
        const raw = localStorage.getItem('panel_user_snippets');
        if (raw) this.userSnippets = JSON.parse(raw) || [];
      } catch(_) {}
    },
    _saveUserSnippets(){
      try { localStorage.setItem('panel_user_snippets', JSON.stringify(this.userSnippets||[])); } catch(_) {}
    },
    installPWA(){
      if ('serviceWorker' in navigator) {
        try {
          navigator.serviceWorker.register('/sw.js', { scope: '/' })
            .catch(err => console.warn('SW register fail:', err));
        } catch(_) {}
      }
      window.addEventListener('beforeinstallprompt', (ev) => {
        ev.preventDefault();
        this.pwaPrompt = ev;
        this.pwaInstallable = true;
      });
      window.addEventListener('appinstalled', () => {
        this.pwaInstalled = true;
        this.pwaInstallable = false;
        this.pwaPrompt = null;
      });
    },
    installViewportKeyboard(){
      const vv = window.visualViewport;
      if (!vv) return;
      try { if ('virtualKeyboard' in navigator) navigator.virtualKeyboard.overlaysContent = false; } catch(_){}
      const recompute = () => {
        this._vvRaf = 0;
        const raw = Math.round(window.innerHeight - (vv.height + vv.offsetTop));
        const next = raw > 60 ? raw : 0;
        if (next === this.kbInset) return;
        const wasOpen = this.kbInset > 0;
        this.kbInset = next;
        this.kbOpen = next > 0;
        this._applyKbInset();
        if (this.page === 'dev' && this.isMobile()) {
          this.$nextTick(() => this._refitAllPanes());
          setTimeout(() => { this._refitAllPanes(); this._scrollActiveToCursor(); }, 250);
        }
        if (wasOpen && next === 0) this._wantKeyboard = false;
      };
      const onVV = () => { if (this._vvRaf) return; this._vvRaf = requestAnimationFrame(recompute); };
      vv.addEventListener('resize', onVV);
      vv.addEventListener('scroll', onVV);
      this._applyKbInset();
    },
    _applyKbInset(){
      try { document.documentElement.style.setProperty('--kb-inset', (this.kbInset||0) + 'px'); } catch(_){}
      try { document.body.classList.toggle('kb-open', !!this.kbOpen); } catch(_){}
    },
    _scrollActiveToCursor(){
      const p = this.activePane && this.activePane();
      try { p && p.term && p.term.scrollToBottom(); } catch(_){}
    },
    summonKeyboard(){
      const p = this.activePane && this.activePane();
      if (!p || !p.term) return;
      this._wantKeyboard = true;
      try {
        const ta = document.querySelector('#host-pane-'+p.id+' .xterm-helper-textarea');
        if (ta) ta.setAttribute('inputmode','text');
      } catch(_){}
      try { p.term.focus(); } catch(_){}
    },
    dismissKeyboard(){
      this._wantKeyboard = false;
      try {
        const p = this.activePane && this.activePane();
        const ta = p && document.querySelector('#host-pane-'+p.id+' .xterm-helper-textarea');
        if (ta) { ta.setAttribute('inputmode','none'); ta.blur(); }
        else if (document.activeElement && document.activeElement.blur) document.activeElement.blur();
      } catch(_){}
    },
    showTermChrome(){
      if (this.termChromeHidden) { this.termChromeHidden = false; this.$nextTick(()=>this._refitAllPanes()); }
    },
    hideTermChrome(){
      if (!this.isMobile() || this.page !== 'dev') return;
      if (!this.termChromeHidden) { this.termChromeHidden = true; this.$nextTick(()=>this._refitAllPanes()); }
    },
    toggleTermChrome(){
      if (this.termChromeHidden) this.showTermChrome();
      else this.hideTermChrome();
    },
    async installPWAApp(){
      if (this.pwaPrompt) {
        try {
          this.pwaPrompt.prompt();
          const { outcome } = await this.pwaPrompt.userChoice;
          if (outcome === 'accepted') {
            this.pwaInstalled = true;
          }
          this.pwaPrompt = null;
          this.pwaInstallable = false;
        } catch(_) {}
        return;
      }
      this.askConfirm('Install on iOS',
        '1. Tap the share icon (📤) in the bottom bar of Safari\n2. Scroll and tap "Add to Home Screen"\n3. Confirm — the icon appears on the home screen as an app\n\nOn Chrome for Android the prompt shows up automatically once the criteria are met.',
        () => {});
    },
    sendKeyToActive(key){
      const p = this.activePane();
      if (!p || !p.ws || p.ws.readyState !== 1) return;
      const map = {
        'Escape': '\x1b',
        'Tab':    '\t',
        'Shift+Tab': '\x1b[Z',
        'Up':     '\x1b[A',
        'Down':   '\x1b[B',
        'Right':  '\x1b[C',
        'Left':   '\x1b[D',
        'Home':   '\x1b[H',
        'End':    '\x1b[F',
        'PageUp': '\x1b[5~',
        'PageDown':'\x1b[6~',
        'Delete': '\x1b[3~',
        'Backspace': '\x7f',
        'Enter':  '\r',
        'Ctrl+C': '\x03',
        'Ctrl+D': '\x04',
        'Ctrl+L': '\x0c',
        'Ctrl+Z': '\x1a',
        'Ctrl+R': '\x12',
        'Ctrl+U': '\x15',
        'Ctrl+W': '\x17',
        'Ctrl+A': '\x01',
        'Ctrl+E': '\x05',
        'Ctrl+K': '\x0b',
      };
      const data = map[key];
      if (data) {
        p.ws.send(JSON.stringify({type:'input', data}));
      } else if (typeof key === 'string' && key.length >= 1 && key.length <= 4) {
        p.ws.send(JSON.stringify({type:'input', data: key}));
      }
    },
    toggleTrackpad(){
      this.terms.trackpadMode = !this.terms.trackpadMode;
      if (this.terms.trackpadMode) this._installTrackpadOverlay();
      else this._removeTrackpadOverlay();
    },
    _removeTrackpadOverlay(){
      const ov = document.getElementById('trackpad-overlay');
      if (ov) ov.remove();
    },
    _installTrackpadOverlay(){
      this._removeTrackpadOverlay();
      const root = document.getElementById('host-pane-root');
      if (!root) { this.terms.trackpadMode = false; return; }
      const ov = document.createElement('div');
      ov.id = 'trackpad-overlay';
      ov.className = 'trackpad-overlay';
      const hint = document.createElement('div');
      hint.className = 'trackpad-hint';
      hint.textContent = '🖐 Drag to move the cursor · a single tap exits';
      ov.appendChild(hint);
      const arrow = document.createElement('div');
      arrow.className = 'trackpad-arrow';
      arrow.innerHTML = '<svg viewBox="0 0 24 24" fill="#60a5fa"><path d="M5 3l14 9-7 1.5-3 6.5z" stroke="white" stroke-width="1.2"/></svg>';
      arrow.style.display = 'none';
      ov.appendChild(arrow);
      root.appendChild(ov);
      let anchor = null, lastPos = null, moved = false, tapStart = 0;
      const STEP_PX = 25;
      const RATE_LIMIT_MS = 30;
      ov.addEventListener('touchstart', (ev) => {
        ev.preventDefault();
        const t = ev.touches[0]; if (!t) return;
        const rect = ov.getBoundingClientRect();
        anchor = { x: t.clientX, y: t.clientY };
        lastPos = { x: t.clientX, y: t.clientY };
        moved = false;
        tapStart = Date.now();
        arrow.style.display = 'block';
        arrow.style.left = (t.clientX - rect.left) + 'px';
        arrow.style.top  = (t.clientY - rect.top)  + 'px';
      }, { passive: false });
      ov.addEventListener('touchmove', (ev) => {
        ev.preventDefault();
        const t = ev.touches[0]; if (!t || !lastPos) return;
        const rect = ov.getBoundingClientRect();
        arrow.style.left = (t.clientX - rect.left) + 'px';
        arrow.style.top  = (t.clientY - rect.top)  + 'px';
        const now = Date.now();
        if (now - this.terms._trackpadLastEmit < RATE_LIMIT_MS) return;
        const dx = t.clientX - lastPos.x;
        const dy = t.clientY - lastPos.y;
        if (Math.abs(dx) > Math.abs(dy)) {
          if (Math.abs(dx) >= STEP_PX) {
            this.sendKeyToActive(dx > 0 ? 'Right' : 'Left');
            this.terms._trackpadLastEmit = now;
            lastPos = { x: t.clientX, y: t.clientY };
            moved = true;
          }
        } else {
          if (Math.abs(dy) >= STEP_PX) {
            this.sendKeyToActive(dy > 0 ? 'Down' : 'Up');
            this.terms._trackpadLastEmit = now;
            lastPos = { x: t.clientX, y: t.clientY };
            moved = true;
          }
        }
      }, { passive: false });
      ov.addEventListener('touchend', (ev) => {
        ev.preventDefault();
        arrow.style.display = 'none';
        const wasTap = !moved && (Date.now() - tapStart < 250);
        anchor = null; lastPos = null;
        if (wasTap) {
          this.terms.trackpadMode = false;
          this._removeTrackpadOverlay();
        }
      }, { passive: false });
    },
    applyTermFont(){
      localStorage.setItem('panel_term_font', this.hostTermFont);
      const family = (this.termFonts[this.hostTermFont] || this.termFonts.system).family;
      (this.terms.panes||[]).forEach(p => {
        if (p.term) { p.term.options.fontFamily = family; this._fitSoon(p.fit); }
      });
    },

    runSnippet(cmd){
      const p = this.activePane();
      if (!p || !p.ws || p.ws.readyState !== 1) { this.showToast('terminal is not connected','err'); return; }
      const expanded = (cmd || '').replace(/\{\{(\w+)\}\}/g, '__PANEL_LIT_$1__')
        .replace(/\{(\w+)\}/g, (m, k) => {
          const now = new Date();
          const pad = n => String(n).padStart(2, '0');
          switch (k) {
            case 'date':     return now.toISOString().slice(0, 10);
            case 'time':     return pad(now.getHours()) + ':' + pad(now.getMinutes());
            case 'datetime': return now.toISOString().slice(0, 19).replace('T', ' ');
            case 'ts':       return String(Math.floor(now.getTime() / 1000));
            case 'user':     return this.username || '';
            case 'host':     return this.hostname || '';
            case 'pane':     return (p.sessionName || p.id || '');
            default:         return m;
          }
        })
        .replace(/__PANEL_LIT_(\w+)__/g, '{$1}');
      const payload = expanded.endsWith('\n') ? expanded : expanded + '\n';
      p.ws.send(JSON.stringify({type:'input', data: payload}));
      this.pushRecentCommand(cmd);
      this.hostTermSnippetsOpen = false;
    },
    pushRecentCommand(cmd){
      const clean = (cmd||'').trim(); if (!clean) return;
      this.recentCommands = [clean, ...this.recentCommands.filter(c => c !== clean)].slice(0, 20);
      try { localStorage.setItem('panel_term_recent', JSON.stringify(this.recentCommands)); } catch(e){}
    },
    clearRecentCommands(){ this.recentCommands = []; localStorage.removeItem('panel_term_recent'); },
    addUserSnippet(){
      const n = (this.newSnippet.name||'').trim(), c = (this.newSnippet.cmd||'').trim();
      if (!n || !c) return;
      this.userSnippets.push({name:n, cmd:c});
      localStorage.setItem('panel_term_snippets', JSON.stringify(this.userSnippets));
      this.newSnippet = {name:'', cmd:''};
    },
    removeUserSnippet(i){
      this.userSnippets.splice(i,1);
      localStorage.setItem('panel_term_snippets', JSON.stringify(this.userSnippets));
    },

    notifyStatusLabel(){
      if (!('Notification' in window)) return 'not supported in this browser';
      if (Notification.permission === 'granted' && this.hostNotifyEnabled) return '● enabled';
      if (Notification.permission === 'denied') return '✕ blocked in the browser';
      return '○ disabled — click to enable';
    },
    async toggleDesktopNotifications(){
      if (!('Notification' in window)) { this.showToast('browser without support','err'); return; }
      if (this.hostNotifyEnabled) {
        this.hostNotifyEnabled = false;
        localStorage.setItem('panel_term_notify','0');
        return;
      }
      if (Notification.permission === 'default') {
        const p = await Notification.requestPermission();
        if (p !== 'granted') { this.showToast('permission denied','err'); return; }
      }
      if (Notification.permission !== 'granted') { this.showToast('blocked in the browser','err'); return; }
      this.hostNotifyEnabled = true;
      localStorage.setItem('panel_term_notify','1');
    },
    _pickFilesForPane(state){
      if (!state) return;
      let inp = document.getElementById('panel-term-file-input');
      if (!inp) {
        inp = document.createElement('input');
        inp.type = 'file';
        inp.multiple = true;
        inp.id = 'panel-term-file-input';
        inp.style.cssText = 'position:fixed;left:-9999px;width:1px;height:1px;opacity:0;';
        document.body.appendChild(inp);
      }
      inp.onchange = () => {
        const fs = inp.files;
        const copy = Array.from(fs || []);
        inp.value = '';
        if (copy.length) this._sendFilesToPane(state, copy).catch(()=>{});
      };
      inp.click();
    },
    _installGlobalDropGuard(){
      if (this._dropGuardInstalled) return;
      this._dropGuardInstalled = true;
      const block = (ev) => {
        if (ev.defaultPrevented) return;
        if (!this._dragHasFiles(ev)) return;
        ev.preventDefault();
        try { if (ev.type === 'dragover') ev.dataTransfer.dropEffect = 'none'; } catch(_){}
      };
      window.addEventListener('dragover', block, false);
      window.addEventListener('drop', (ev) => {
        const hadFile = this._dragHasFiles(ev) && !ev.defaultPrevented;
        block(ev);
        this._hideFileDropOverlay(null);
        if (hadFile) this.showToast?.('drop the file ONTO a terminal pane to attach it','info');
      }, false);
    },
    _clipboardFiles(ev){
      const cd = ev && ev.clipboardData;
      if (!cd) return null;
      const types = Array.from(cd.types || []);
      if (types.includes('text/plain')) return null;
      const out = [];
      for (const it of (cd.items || [])) {
        if (it.kind === 'file') { const f = it.getAsFile(); if (f) out.push(f); }
      }
      if (!out.length) for (const f of (cd.files || [])) out.push(f);
      return out.length ? out : null;
    },
    _pasteHandled(ev, files){
      try {
        if (ev && ev.__panelPasteHandled) return true;
        if (ev) ev.__panelPasteHandled = true;
      } catch(_) {}
      const sig = (files || [])
        .map((f) => (f.name||'') + ':' + (f.size||0) + ':' + (f.type||''))
        .join('|');
      if (!sig) return false;
      const now = Date.now();
      const ult = this._lastPasteDedup;
      if (ult && ult.sig === sig && now - ult.ts < 1000) return true;
      this._lastPasteDedup = { sig, ts: now };
      return false;
    },
    _dragHasFiles(ev){
      try {
        const t = ev.dataTransfer && ev.dataTransfer.types;
        if (!t) return false;
        return Array.from(t).includes('Files');
      } catch(_) { return false; }
    },
    async _uploadTermFile(blob, name){
      if (!blob) return null;
      const fd = new FormData();
      let n = name || blob.name || '';
      if (!n) {
        const ext = (blob.type && blob.type.split('/')[1]) || 'bin';
        n = 'paste.' + ext;
      }
      fd.append('file', blob, n);
      try {
        const r = await fetch('/api/terminal/upload', {
          method: 'POST',
          credentials: 'include',
          headers: { 'Authorization': 'Bearer ' + this.token },
          body: fd,
        });
        if (!r.ok) {
          let msg = 'HTTP ' + r.status;
          try { const e = await r.json(); if (e && e.error) msg = e.error; } catch(_){}
          this.showToast?.('upload failed: ' + msg, 'err');
          return null;
        }
        return await r.json();
      } catch(err) {
        this.showToast?.('upload failed: ' + (err && err.message || 'network'), 'err');
        return null;
      }
    },
    async _uploadPasteImage(blob){
      const d = await this._uploadTermFile(blob);
      return d ? d.path : null;
    },
    _quoteShellPath(p){
      if (!p) return '';
      if (/^[A-Za-z0-9_@%+=:,.\/-]+$/.test(p)) return p;
      return "'" + p.replace(/'/g, "'\\''") + "'";
    },
    async _sendFilesToPane(state, files){
      const list = Array.from(files || []).filter(Boolean);
      if (!list.length || !state) return;
      const total = list.length;
      const paths = [];
      for (let i = 0; i < total; i++) {
        const f = list[i];
        this.showToast?.(total > 1 ? `uploading ${i+1}/${total}: ${f.name||'file'}…` : `uploading ${f.name||'file'}…`, 'info');
        const d = await this._uploadTermFile(f, f.name);
        if (d && d.path) paths.push(this._quoteShellPath(d.path));
      }
      if (!paths.length) return;
      this._paneSendInput(state, paths.join(' ') + ' ');
      this.showToast?.(paths.length === 1 ? '📎 ' + paths[0] : `📎 ${paths.length} files attached`, 'ok');
    },
    async _pasteIntoPane(state){
      try {
        if (navigator.clipboard && navigator.clipboard.read) {
          const items = await navigator.clipboard.read();
          for (const item of items) {
            const types = item.types || [];
            const img = types.find(t => t.startsWith('image/'));
            if (img) {
              const blob = await item.getType(img);
              const path = await this._uploadPasteImage(blob);
              if (path) this._paneSendInput(state, path);
              return;
            }
          }
          for (const item of items) {
            const types = item.types || [];
            if (types.includes('text/plain')) {
              const blob = await item.getType('text/plain');
              const txt = await blob.text();
              if (txt) this._paneSendInput(state, txt);
              return;
            }
          }
          return;
        }
      } catch(_) {
        // no permission or API unavailable → try the simple path below
      }
      try {
        const t = await navigator.clipboard.readText();
        if (t) this._paneSendInput(state, t);
      } catch(_){}
    },
    _termOutputHook(pane, raw){
      if (!pane) return;
      try { this._updateScrollBtn(pane); } catch(_){}
      if (!pane.notify) pane.notify = { lastOutput: 0, cmdStart: 0, busy: false, alerted: false };
      const now = Date.now();
      pane.notify.lastOutput = now;
      const isStr = (typeof raw === 'string');
      if (this.hostTermBell === 'visual') {
        let hasBel = false;
        if (isStr) { hasBel = raw.indexOf('\x07') >= 0; }
        else { try { const u = new Uint8Array(raw); for (let i = 0; i < u.length; i++) { if (u[i] === 7) { hasBel = true; break; } } } catch (_) {} }
        if (hasBel) {
          const el = document.getElementById('host-pane-' + pane.id);
          if (el) { el.style.transition = 'background .15s'; el.style.background = '#fef3c7'; setTimeout(() => { el.style.background = ''; }, 150); }
        }
      }
      if (pane.notify.timer) clearTimeout(pane.notify.timer);
      if (!this.hostNotifyEnabled) return;
      let tail = '';
      try {
        if (isStr) tail = raw.slice(-256);
        else { const u = new Uint8Array(raw); tail = new TextDecoder().decode(u.subarray(Math.max(0, u.length - 256))); }
      } catch (_) {}
      pane.notify.timer = setTimeout(() => {
        if (!this.hostNotifyEnabled || Notification.permission !== 'granted') return;
        if (this.terms.activePane === pane.id && document.hasFocus()) return;
        if (now - (pane.notify.cmdStart || 0) < 3000) return;
        try {
          new Notification('Command finished — ' + pane.sessionName, { body: 'Last output: ' + tail.slice(-80).replace(/\s+/g, ' ').trim(), tag: 'panel-' + pane.id });
        } catch (e) {}
        pane.notify.cmdStart = 0;
      }, 10000);
    },

    openDetailShell() {
      const el = document.getElementById('cnt-term'); if (!el) return;
      if (this.detail.term) { try{ this.detail.term.dispose(); }catch(e){} }
      if (this.detail.ws) { try{ this.detail.ws.close(); }catch(e){} }
      this.detail.term = null; this.detail.ws = null;
      const state = { term:null, fit:null, search:null, ws:null, reconnect:{ attempts:0, timer:null, nextDelay:0, cancelled:false } };
      this.buildTerminal({
        el,
        wsPath: '/ws/container/'+this.detail.id,
        fontSize: this._termFontSize(),
        state,
        onStatus: ()=>{}
      });
      this.detail.term = state.term; this.detail.ws = state.ws; this.detail.fit = state.fit;
      this.detail.reconnect = state.reconnect;
    },

    async whatsappInit() {
      try {
        const r = await this.api('/api/whatsapp/status');
        if (!r.ok) return;
        const st = await r.json();
        if (st && !st.error) this._whatsappApplyState(st);
      } catch(e) { return; }
      if (!this.whatsapp.enabled) return;
      this.whatsappReloadChats();
      this.whatsappConnectWS();
    },

    _whatsappApplyState(st) {
      const wa = this.whatsapp;
      wa.enabled = !!st.enabled;
      wa.status = st.status || 'UNPAIRED';
      wa.phone = st.phone || '';
      wa.pushName = st.push_name || '';
      wa.qrDataURL = st.qr_data_url || '';
      wa.wahaReachable = !!st.waha_reachable;
      wa.hookOK = !!st.hook_ok;
      wa.lastSync = st.last_sync_ts || 0;
    },

    whatsappConnectWS() {
      if (!this.token) return;
      const wa = this.whatsapp;
      if (wa.ws && (wa.ws.readyState === 0 || wa.ws.readyState === 1)) return;
      if (wa.ws) { try { wa.ws.close(); } catch(_){} wa.ws = null; }
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const url = proto + '//' + location.host + '/ws/whatsapp';
      let ws;
      try { ws = new WebSocket(url); } catch(e) { return; }
      wa.ws = ws;
      ws.onopen = () => { wa.wsBackoff = 1000; };
      ws.onmessage = (ev) => {
        try {
          const e = JSON.parse(ev.data);
          this._whatsappOnEvent(e);
        } catch(_){}
      };
      ws.onclose = () => {
        wa.ws = null;
        if (!this.token || !wa.enabled) return;
        if (wa._reconnectTimer) clearTimeout(wa._reconnectTimer);
        wa._reconnectTimer = setTimeout(() => {
          wa._reconnectTimer = null;
          this.whatsappConnectWS();
        }, wa.wsBackoff);
        wa.wsBackoff = Math.min(wa.wsBackoff * 2, 30000);
      };
      ws.onerror = () => { /* onclose takes care of the reconnect */ };
    },

    whatsappDisconnect() {
      const wa = this.whatsapp;
      if (wa._reconnectTimer) {
        clearTimeout(wa._reconnectTimer);
        wa._reconnectTimer = null;
      }
      if (wa.ws) {
        try { wa.ws.close(); } catch(_){}
        wa.ws = null;
      }
    },

    _whatsappOnEvent(e) {
      const wa = this.whatsapp;
      if (!e || typeof e.kind !== 'string') return;
      if (e.kind === 'status' && e.state) {
        const wasNotWorking = wa.status !== 'WORKING';
        this._whatsappApplyState(e.state);
        if (wasNotWorking && wa.status === 'WORKING') this.whatsappReloadChats();
        return;
      }
      if (e.kind === 'message' && e.message) {
        const m = e.message;
        if (!m || !m.id || !m.chat) return;
        const jid = m.chat;
        if (wa.messages[jid]) {
          const existing = wa.messages[jid].find(x => x.id === m.id);
          if (!existing) {
            const pinned = m.from_me || this._whatsappIsPinned();
            wa.messages[jid].push(m);
            wa.scrollPinned = pinned;
            if (pinned) this.$nextTick(() => this._whatsappScrollBottom());
          } else if (m.media && m.media.path && (!existing.media || !existing.media.path)) {
            existing.media = m.media;
            this._whatsappRerender(jid);
          }
        }
        if (jid === 'status@broadcast') return;
        let c = wa.chatById[jid];
        if (!c) {
          c = { jid, name: jid.split('@')[0], is_group: jid.endsWith('@g.us'), unread_count: 0 };
          wa.chats.unshift(c);
          wa.chatById[jid] = c;
        }
        c.last_msg_id = m.id;
        c.last_msg_ts = m.ts;
        c.last_msg_body = this._whatsappPreview(m);
        if (!m.from_me && wa.activeJid !== jid) c.unread_count = (c.unread_count || 0) + 1;
        this._whatsappReorderChats();
        this._whatsappRecountUnread();
        if (!m.from_me && (this.currentView !== 'whatsapp' || wa.activeJid !== jid || document.hidden)) {
          this._whatsappNotify(c, m);
        }
        return;
      }
      if (e.kind === 'ack') {
        if (!e.ack_id) return;
        for (const list of Object.values(wa.messages)) {
          for (const m of list) if (m.id === e.ack_id) m.ack = e.ack_n;
        }
        return;
      }
      if (e.kind === 'revoked') {
        if (!e.ack_id) return;
        for (const list of Object.values(wa.messages)) {
          for (const m of list) if (m.id === e.ack_id) m.deleted = true;
        }
        return;
      }
      if (e.kind === 'reaction') {
        if (!e.ack_id || !e.reaction_from) return;
        for (const jid of Object.keys(wa.messages)) {
          const list = wa.messages[jid];
          let touched = false;
          for (const m of list) {
            if (m.id !== e.ack_id) continue;
            if (!Array.isArray(m.reactions)) m.reactions = [];
            const idx = m.reactions.findIndex(r => r.from === e.reaction_from);
            if (!e.reaction_emoji) {
              if (idx >= 0) m.reactions.splice(idx, 1);
            } else if (idx >= 0) {
              m.reactions[idx].emoji = e.reaction_emoji;
              m.reactions[idx].ts = e.ts || Math.floor(Date.now()/1000);
            } else {
              m.reactions.push({from: e.reaction_from, emoji: e.reaction_emoji, ts: e.ts || Math.floor(Date.now()/1000)});
            }
            touched = true;
          }
          if (touched) this._whatsappRerender(jid);
        }
        return;
      }
      if (e.kind === 'chat' && e.chat && e.chat.jid) {
        const c = e.chat;
        const existing = wa.chatById[c.jid];
        if (existing) Object.assign(existing, c);
        else { wa.chats.unshift(c); wa.chatById[c.jid] = c; }
        this._whatsappReorderChats();
      }
      if (e.kind === 'presence' && e.chat_jid) {
        const c = wa.chatById[e.chat_jid];
        if (c) {
          c.presence = e.presence || '';
          if (e.last_seen_ts) c.last_seen_ts = e.last_seen_ts;
        }
      }
    },

    _whatsappReorderChats() {
      const wa = this.whatsapp;
      wa.chats.sort((a,b) => (b.last_msg_ts||0) - (a.last_msg_ts||0));
    },
    _whatsappRecountUnread() {
      const wa = this.whatsapp;
      wa.totalUnread = wa.chats.reduce((s,c) => s + (c.unread_count||0), 0);
    },
    _whatsappPreview(m) {
      if (m.body) return m.body.length > 100 ? m.body.slice(0,100) + '…' : m.body;
      switch (m.type) {
        case 'image': return '📷 Image';
        case 'video': return '🎬 Video';
        case 'audio': return '🎵 Audio';
        case 'voice': return '🎤 Voice message';
        case 'document': return '📎 ' + (m.media?.filename || 'Document');
        case 'sticker': return '🏷️ Sticker';
        case 'location': return '📍 Location';
      }
      return '';
    },
    _whatsappScrollBottom() {
      const el = document.getElementById('wa-msgs');
      if (el) el.scrollTop = el.scrollHeight;
    },
    _whatsappIsPinned() {
      const el = document.getElementById('wa-msgs');
      if (!el) return true;
      return (el.scrollHeight - el.scrollTop - el.clientHeight) < 150;
    },

    async whatsappStart() {
      this.whatsapp.starting = true;
      this.whatsapp.error = '';
      try {
        const r = await this.api('/api/whatsapp/start', { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(()=>({}));
          this.whatsapp.error = d.error || ('HTTP '+r.status);
        }
      } catch(e) {
        this.whatsapp.error = e.message || String(e);
      } finally {
        this.whatsapp.starting = false;
      }
    },
    async whatsappStop() {
      try { await this.api('/api/whatsapp/session/stop', { method: 'POST' }); } catch(_){}
    },
    async whatsappRefreshQR() {
      try {
        const r = await this.api('/api/whatsapp/qr/refresh', { method: 'POST' });
        if (!r.ok) return;
        const d = await r.json();
        if (d && d.qr) this.whatsapp.qrDataURL = d.qr;
      } catch(_){}
    },
    async whatsappLogout() {
      if (!(await this.confirmAsync('Disconnect WhatsApp? You will have to pair again via QR.'))) return;
      try {
        const r = await this.api('/api/whatsapp/logout', { method: 'POST' });
        if (!r.ok) {
          const d = await r.json().catch(()=>({}));
          this.showToast('Failed: ' + (d.error || 'HTTP '+r.status), 'err');
          return;
        }
        this.whatsapp.activeJid = null;
        this.whatsapp.chats = [];
        this.whatsapp.chatById = {};
        this.whatsapp.messages = {};
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
    },

    _skl(arr, keyField, prefix) {
      if (!Array.isArray(arr)) return [];
      const seen = new Set();
      const out = [];
      const px = prefix || ('_skl_'+keyField+'_');
      for (let i = 0; i < arr.length; i++) {
        const it = arr[i];
        if (!it || typeof it !== 'object') continue;
        let k = it[keyField];
        if (k === undefined || k === null || k === '' || (typeof k === 'number' && Number.isNaN(k))) {
          k = px + i;
          try { it[keyField] = k; } catch(_) {}
        }
        const sk = String(k);
        if (seen.has(sk)) continue;
        seen.add(sk);
        out.push(it);
      }
      return out;
    },

    _sanitizeWaChats(arr) {
      if (!Array.isArray(arr)) return [];
      const seen = new Set();
      const out = [];
      for (const c of arr) {
        if (!c || !c.jid) continue;
        if (seen.has(c.jid)) continue;
        if (c.jid === 'status@broadcast') continue;
        seen.add(c.jid);
        out.push(c);
      }
      return out;
    },
    _sanitizeWaMessages(arr) {
      if (!Array.isArray(arr)) return [];
      const seen = new Set();
      const out = [];
      for (let i = 0; i < arr.length; i++) {
        const m = arr[i];
        if (!m) continue;
        if (!m.id) m.id = 'synth-'+(m.ts||0)+'-'+i+'-'+Math.random().toString(36).slice(2,6);
        if (seen.has(m.id)) continue;
        seen.add(m.id);
        out.push(m);
      }
      return out;
    },

    async whatsappMarkAllRead() {
      const wa = this.whatsapp;
      if ((wa.totalUnread || 0) === 0) return;
      if (!(await this.confirmAsync('Mark ALL ' + wa.totalUnread + ' unread messages as read?\n\nThis also marks them as read in WhatsApp on your phone.'))) return;
      try {
        const r = await this.api('/api/whatsapp/admin/mark-all-read', {
          method: 'POST',
          body: JSON.stringify({}),
        });
        if (!r.ok) {
          const d = await r.json().catch(()=>({}));
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        const d = await r.json();
        for (const c of wa.chats) c.unread_count = 0;
        wa.totalUnread = 0;
        if (this.showToast) this.showToast(d.marked + ' conversations marked as read', 'ok');
      } catch(e) {
        this.showToast('Failed: ' + (e.message || e), 'err');
      }
    },

    async whatsappWipeAndReimport() {
      if (!(await this.confirmAsync('Erase the LOCAL record of chats and messages and re-import from WhatsApp?\n\nThis does NOT touch WhatsApp on your phone. It only rebuilds the conversation list and the history here in the panel from the data the WAHA gateway holds.\n\nIt can take a few seconds.'))) return;
      const wa = this.whatsapp;
      wa.busy = true;
      try {
        const r = await this.api('/api/whatsapp/admin/wipe', {
          method: 'POST',
          body: JSON.stringify({ history_per_chat: 100 }),
        });
        if (!r.ok) {
          const d = await r.json().catch(()=>({}));
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        wa.chats = [];
        wa.chatById = {};
        wa.messages = {};
        wa.activeJid = null;
        wa.totalUnread = 0;
        await this.whatsappReloadChats();
        this.showToast('Re-import finished. ' + (this.whatsapp.chats.length) + ' conversation(s) loaded.', 'ok');
      } catch(e) {
        this.showToast('Failed: ' + (e.message || e), 'err');
      } finally {
        wa.busy = false;
      }
    },

    async _waRetry(fn, opts) {
      opts = opts || {};
      const max = opts.max || 3;
      const delays = opts.delays || [500, 2000, 5000];
      let lastErr;
      for (let attempt = 0; attempt < max; attempt++) {
        try {
          const r = await fn();
          if (r.status >= 400 && r.status < 500 && r.status !== 429) {
            return r;
          }
          if (r.ok) return r;
          lastErr = new Error('HTTP ' + r.status);
        } catch (e) {
          lastErr = e;
        }
        if (attempt < max - 1) {
          await new Promise(res => setTimeout(res, delays[attempt] || 5000));
        }
      }
      throw lastErr || new Error('retry exhausted');
    },

    async whatsappReloadChats() {
      try {
        const r = await this._waRetry(() => this.api('/api/whatsapp/chats/sync', { method: 'POST' }));
        if (!r.ok) return;
        const d = await r.json();
        const wa = this.whatsapp;
        wa.chats = this._sanitizeWaChats(d.chats || []);
        wa.chatById = {};
        for (const c of wa.chats) wa.chatById[c.jid] = c;
        this._whatsappRecountUnread();
      } catch(_){}
    },

    async whatsappFullSync() {
      const wa = this.whatsapp;
      if (wa.syncing) return;
      wa.syncing = true;
      try {
        const r = await this._waRetry(() => this.api('/api/whatsapp/sync', { method: 'POST' }), { max: 2, delays: [1000, 4000] });
        const d = await r.json().catch(() => ({}));
        if (!r.ok) {
          this.showToast && this.showToast('Sync failed: ' + (d.error || r.status), 'err');
          return;
        }
        await this.whatsappReloadChats();
        if (wa.activeJid) {
          delete wa.messages[wa.activeJid];
          this.whatsappOpenChat(wa.activeJid);
        }
        const added = d.messages_added || 0;
        const scanned = d.chats_scanned || 0;
        const failed = d.chats_failed || 0;
        let msg = added > 0
          ? added + ' message' + (added !== 1 ? 's' : '') + ' new' + (added !== 1 ? 's' : '') + ' in ' + scanned + ' conversation' + (scanned !== 1 ? 's' : '')
          : 'All caught up (' + scanned + ' conversation' + (scanned !== 1 ? 's' : '') + ' checked' + (scanned !== 1 ? 's' : '') + ')';
        if (failed > 0) msg += ' · ' + failed + ' failure' + (failed !== 1 ? 's' : '');
        this.showToast && this.showToast(msg, failed > 0 ? 'err' : 'ok');
      } catch (e) {
        this.showToast && this.showToast('Sync error: ' + (e && e.message || 'network'), 'err');
      } finally {
        wa.syncing = false;
      }
    },

    whatsappQuotedPreview(m) {
      if (!m || !m.quoted_id) return null;
      const list = this.whatsapp.messages[m.chat] || [];
      const q = list.find(x => x.id === m.quoted_id);
      if (!q) return null;
      let body = (q.body || '').trim();
      if (!body) {
        if (q.type === 'image') body = '📷 Image';
        else if (q.type === 'video') body = '🎬 Video';
        else if (q.type === 'voice' || q.type === 'audio') body = '🎵 Audio';
        else if (q.type === 'document') body = '📄 Document';
        else body = '(media)';
      }
      const label = q.from_me ? 'You' : (this.whatsappSenderLabel(q) || 'Contact');
      return { label, body: body.slice(0, 120) };
    },

    whatsappGroupReactions(reactions) {
      if (!Array.isArray(reactions)) return [];
      const myJid = this.whatsapp.phone ? (this.whatsapp.phone + '@c.us') : '';
      const by = new Map();
      for (const r of reactions) {
        if (!r || !r.emoji) continue;
        if (!by.has(r.emoji)) by.set(r.emoji, { emoji: r.emoji, count: 0, from: [] });
        const g = by.get(r.emoji);
        g.count++;
        const senderName = r.from === myJid ? 'You'
          : (this.whatsapp.chatById[r.from]?.name || (r.from || '').split('@')[0] || '?');
        g.from.push(senderName);
      }
      return Array.from(by.values()).sort((a, b) => b.count - a.count);
    },

    whatsappScrollToMsg(msgId) {
      if (!msgId) return;
      const el = document.querySelector('[data-msg-id="' + CSS.escape(msgId) + '"]');
      if (el) {
        el.scrollIntoView({ behavior: 'smooth', block: 'center' });
        el.classList.add('wa-msg-flash');
        setTimeout(() => el.classList.remove('wa-msg-flash'), 1500);
      }
    },

    whatsappSenderLabel(m) {
      if (!m || m.from_me) return '';
      const chat = this.whatsapp.chatById[m.chat];
      if (!chat || !chat.is_group) return '';
      const senderJid = (m.from || '').trim();
      if (!senderJid) return '';
      const known = this.whatsapp.chatById[senderJid];
      if (known && known.name) return known.name;
      const user = senderJid.split('@')[0] || '';
      if (user.length > 4) return '~' + user.slice(-8);
      return user || senderJid;
    },

    whatsappFilteredChats() {
      const wa = this.whatsapp;
      let chats = this._sanitizeWaChats(wa.chats);
      const filter = wa.filter || 'all';
      if (filter === 'archived') {
        chats = chats.filter(c => c.archived);
      } else if (filter === 'status') {
        chats = [];
      } else {
        chats = chats.filter(c => !c.archived);
        if (filter === 'unread') chats = chats.filter(c => (c.unread_count||0) > 0);
        else if (filter === 'groups') chats = chats.filter(c => c.is_group || (c.jid||'').endsWith('@g.us'));
        else if (filter === 'favorites') chats = chats.filter(c => c.pinned);
      }
      const q = (wa.search||'').toLowerCase();
      if (!q) return chats;
      return chats.filter(c => ((c.name||'') + ' ' + (c.jid||'') + ' ' + (c.last_msg_body||'')).toLowerCase().includes(q));
    },

    whatsappChatTitle(c) {
      if (!c) return '';
      if (c.name) return c.name;
      const jid = c.jid || '';
      const user = jid.split('@')[0] || '';
      if (jid.endsWith('@lid')) return 'Contact ' + user.slice(0, 4);
      if (/^\d{12,13}$/.test(user)) return this._fmtPhone(user);
      return user || jid;
    },
    _fmtPhone(d) {
      if (d.length === 13 && d.startsWith('55')) {
        return '+55 ' + d.slice(2,4) + ' ' + d.slice(4,9) + '-' + d.slice(9);
      }
      if (d.length === 12 && d.startsWith('55')) {
        return '+55 ' + d.slice(2,4) + ' ' + d.slice(4,8) + '-' + d.slice(8);
      }
      return '+' + d;
    },
    whatsappChatSubtitle(c) {
      if (!c) return '';
      const jid = c.jid || '';
      if (c.is_group || jid.endsWith('@g.us')) return 'Group';
      const user = jid.split('@')[0] || '';
      if (/^\d+$/.test(user)) return this._fmtPhone(user);
      return jid;
    },
    whatsappAvatarLabel(c) {
      if (!c) return '?';
      const title = this.whatsappChatTitle(c);
      const words = title.replace(/[+\d\s\-()]/g,'').trim().split(/\s+/).filter(Boolean);
      if (words.length === 0) return (title || '?').slice(0,2).toUpperCase();
      if (words.length === 1) return words[0].slice(0,2).toUpperCase();
      return (words[0][0] + words[1][0]).toUpperCase();
    },
    whatsappMessagesWithDividers(msgs) {
      const out = [];
      let lastDay = '';
      const sanitized = this._sanitizeWaMessages(msgs);
      for (const m of sanitized) {
        const hasBody = !!(m.body && m.body.trim());
        const isMediaType = !!m.type && m.type !== 'text';
        const hasMedia = isMediaType || !!m.media || !!m._optim_media;
        if (!hasBody && !hasMedia && !m.quoted_id && !m.deleted) continue;
        const day = this._dayKey(m.ts);
        if (day !== lastDay) {
          out.push({ type:'divider', label: this._dayLabel(m.ts), id: 'd-'+day });
          lastDay = day;
        }
        out.push({ type:'msg', m, id: m.id });
      }
      return out;
    },
    _dayKey(ts) {
      const d = new Date(ts * 1000);
      return d.getFullYear()+'-'+(d.getMonth()+1)+'-'+d.getDate();
    },
    _dayLabel(ts) {
      const d = new Date(ts * 1000);
      const now = new Date();
      const sameDay = d.toDateString() === now.toDateString();
      if (sameDay) return 'Today';
      const yesterday = new Date(now); yesterday.setDate(yesterday.getDate() - 1);
      if (d.toDateString() === yesterday.toDateString()) return 'Yesterday';
      const diffDays = Math.floor((now - d) / 86400000);
      if (diffDays < 7) {
        const names = ['Sunday','Monday','Tuesday','Wednesday','Thursday','Friday','Saturday'];
        return names[d.getDay()];
      }
      return d.toLocaleDateString('en-US');
    },
    fmtMsgTime(ts) {
      if (!ts) return '';
      const d = new Date(ts * 1000);
      return String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0');
    },

    async whatsappOpenChat(jid) {
      const wa = this.whatsapp;
      wa.activeJid = jid;
      if (!wa.messages[jid]) {
        wa.loadingMessages = true;
        try {
          const r = await this.api('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/messages?limit=50');
          if (r.ok) {
            const d = await r.json();
            wa.messages[jid] = this._sanitizeWaMessages((d.messages || []).slice().reverse());
            wa.hasMoreMessages[jid] = (d.messages || []).length >= 50;
          } else {
            wa.messages[jid] = [];
          }
        } catch(e) { wa.messages[jid] = []; }
        wa.loadingMessages = false;
      }
      const c = wa.chatById[jid];
      if (c && c.unread_count > 0) {
        c.unread_count = 0;
        this._whatsappRecountUnread();
        this.api('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/read', { method: 'POST' }).catch(()=>{});
      }
      this._whatsappAfterOpen(jid);
      wa.scrollPinned = true;
      this.$nextTick(() => this._whatsappScrollBottom());
    },

    async whatsappLoadMore() {
      const wa = this.whatsapp;
      const jid = wa.activeJid;
      if (!jid || !wa.messages[jid] || wa.messages[jid].length === 0) return;
      const oldest = wa.messages[jid][0];
      try {
        const r = await this.api('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/messages?limit=50&before=' + oldest.ts);
        if (!r.ok) return;
        const d = await r.json();
        const more = (d.messages || []).slice().reverse();
        wa.messages[jid] = this._sanitizeWaMessages(more.concat(wa.messages[jid]));
        wa.hasMoreMessages[jid] = more.length >= 50;
      } catch(_){}
    },

    whatsappFormatBody(body) {
      if (!body) return '';
      let s = String(body)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;');
      s = s.replace(/```([\s\S]+?)```/g, '<code class="wa-md-code">$1</code>');
      s = s.replace(/(^|\s)`([^`\n]+)`(?=\s|$|[.,!?;:])/g, '$1<code class="wa-md-inline-code">$2</code>');
      s = s.replace(/(^|\s)\*([^\s*][^*\n]*[^\s*]|[^\s*])\*(?=\s|$|[.,!?;:])/g, '$1<strong>$2</strong>');
      s = s.replace(/(^|\s)_([^\s_][^_\n]*[^\s_]|[^\s_])_(?=\s|$|[.,!?;:])/g, '$1<em>$2</em>');
      s = s.replace(/(^|\s)~([^\s~][^~\n]*[^\s~]|[^\s~])~(?=\s|$|[.,!?;:])/g, '$1<del>$2</del>');
      s = s.replace(/(https?:\/\/[^\s<>"]{1,500})/g, (url) => {
        return '<a href="' + url + '" target="_blank" rel="noopener noreferrer" class="wa-md-link">' + url + '</a>';
      });
      return s;
    },

    _waSendTokens: 5,
    _waSendTokensTs: 0,
    _waCheckSendToken() {
      const now = Date.now();
      if (!this._waSendTokensTs) this._waSendTokensTs = now;
      const elapsed = (now - this._waSendTokensTs) / 1000;
      const refill = Math.floor(elapsed / 2);
      if (refill > 0) {
        this._waSendTokens = Math.min(5, this._waSendTokens + refill);
        this._waSendTokensTs = now;
      }
      if (this._waSendTokens <= 0) {
        const waitSec = Math.ceil(2 - elapsed);
        this.showToast('Slow down — wait ' + Math.max(1, waitSec) + 's (anti-spam limit).', 'warn');
        return false;
      }
      this._waSendTokens--;
      return true;
    },

    async whatsappSend() {
      const wa = this.whatsapp;
      if (wa._sending) return;
      wa._sending = true;
      if (wa.busy) { wa._sending = false; return; }
      if (!this._waCheckSendToken()) { wa._sending = false; return; }
      const jid = wa.activeJid;
      if (!jid) { wa._sending = false; return; }
      const text = wa.composer.trim();
      const file = wa.attachment;
      if (!text && !file) { wa._sending = false; return; }
      wa.busy = true;
      const cid = (crypto && crypto.randomUUID) ? crypto.randomUUID()
                : ('xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
                    const r = Math.random()*16|0; return (c==='x' ? r : (r&0x3)|0x8).toString(16);
                  }));
      const replyTo = wa.replyTo;
      const optimMsg = {
        id: '_optim:' + cid,
        client_msg_id: cid,
        chat_jid: jid,
        from_me: true,
        body: text,
        ts: Math.floor(Date.now()/1000),
        ack: 0,
        type: file ? (file.type.startsWith('image/') ? 'image' : (file.type.startsWith('video/') ? 'video' : 'document')) : 'text',
        _optim: true,
      };
      if (file) optimMsg._optim_filename = file.name;
      if (replyTo) optimMsg.quoted = { id: replyTo.id, body: replyTo.body, from_me: replyTo.from_me };
      if (!wa.messages[jid]) wa.messages[jid] = [];
      wa.messages[jid] = this._sanitizeWaMessages(wa.messages[jid].concat([optimMsg]));
      wa.scrollPinned = true;
      this.$nextTick(() => this._whatsappScrollBottom());
      wa.composer = '';
      wa.attachment = null;
      wa.replyTo = null;
      try {
        if (file) {
          const fd = new FormData();
          fd.append('file', file);
          if (text) fd.append('caption', text);
          if (replyTo) fd.append('quoted_id', replyTo.id);
          fd.append('client_msg_id', cid);
          const r = await fetch('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/messages', {
            method: 'POST',
            headers: { 'Authorization': 'Bearer ' + this.token },
            body: fd,
          });
          if (!r.ok) {
            const d = await r.json().catch(()=>({error:'HTTP ' + r.status}));
            throw new Error(d.error || ('HTTP ' + r.status));
          }
          const sent = await r.json().catch(()=>null);
          if (sent && sent.id) optimMsg.id = sent.id;
          optimMsg.ack = 1;
          optimMsg._optim = false;
        } else {
          const body = { text, client_msg_id: cid };
          if (replyTo) body.quoted_id = replyTo.id;
          const r = await this.api('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/messages', {
            method: 'POST',
            body: JSON.stringify(body),
          });
          if (!r.ok) {
            const d = await r.json().catch(()=>({}));
            throw new Error(d.error || ('HTTP ' + r.status));
          }
          const sent = await r.json().catch(()=>null);
          if (sent && sent.id) optimMsg.id = sent.id;
          optimMsg.ack = 1;
          optimMsg._optim = false;
        }
        wa.attachmentPreview = '';
        wa.emojiOpen = false;
      } catch(e) {
        optimMsg.ack = -1;
        optimMsg._error = e.message || String(e);
        this.showToast('Failed to send: ' + (e.message || e), 'err');
      } finally {
        wa.busy = false;
        wa._sending = false;
        if (wa.messages[jid]) wa.messages[jid] = wa.messages[jid].slice();
      }
    },

    whatsappEmojiRows() {
      return [
        { title:'Frequent', emojis: ['😀','😂','🥰','😍','😘','😎','🤣','😊','😢','😭','😡','🥺','😴','🤔','👀','💔','❤️','🔥','✨','🎉','👏','🙏','💯','🚀'] },
        { title:'Smileys', emojis: ['😀','😃','😄','😁','😆','😅','🤣','😂','🙂','🙃','😉','😊','😇','🥰','😍','🤩','😘','😗','😚','😙','🥲','😋','😛','😜','🤪','😝','🤑','🤗','🤭','🤫','🤔','🤐','🤨','😐','😑','😶'] },
        { title:'Gestures', emojis: ['👍','👎','👌','✌️','🤞','🤟','🤘','🤙','👈','👉','👆','🖕','👇','☝️','👋','🤚','🖐️','✋','🖖','👏','🙌','👐','🤲','🤝','🙏','✍️','💪','🦾'] },
        { title:'Heart', emojis: ['❤️','🧡','💛','💚','💙','💜','🖤','🤍','🤎','💔','❣️','💕','💞','💓','💗','💖','💘','💝','💟'] },
        { title:'Objects', emojis: ['🔥','✨','🎉','🎊','🎁','🎂','🍰','☕','🍺','🍷','🍕','🍔','🌹','🌸','🌞','🌙','⭐','💯','💰','📱','💻','🚗','✈️','🏠'] },
      ];
    },
    whatsappInsertEmoji(e) {
      this.whatsapp.composer = (this.whatsapp.composer || '') + e;
      this.whatsapp.emojiOpen = false;
    },

    whatsappOpenMsgMenu(ev, msg) {
      const wa = this.whatsapp;
      const menuW = 220, menuH = 350;
      const vx = Math.max(8, Math.min(window.innerWidth  - menuW - 8, ev.clientX));
      const vy = Math.max(8, Math.min(window.innerHeight - menuH - 8, ev.clientY));
      wa.msgMenu = { open:true, x:vx, y:vy, msg };
    },
    whatsappReplyToCurrent() {
      const m = this.whatsapp.msgMenu.msg;
      if (!m) return;
      this.whatsapp.replyTo = {
        id: m.id,
        body: m.body || ('[' + (m.type || 'media') + ']'),
        type: m.type,
        fromMe: m.from_me,
        label: m.from_me ? 'You' : (this.whatsapp.chatById[this.whatsapp.activeJid]?.name || 'Reply'),
      };
      this.whatsapp.msgMenu.open = false;
    },
    whatsappCopyMsg() {
      const m = this.whatsapp.msgMenu.msg;
      if (m && m.body) navigator.clipboard.writeText(m.body).catch(()=>{});
      this.whatsapp.msgMenu.open = false;
    },
    async whatsappReact(emoji) {
      const m = this.whatsapp.msgMenu.msg;
      if (!m) return;
      this.whatsapp.msgMenu.open = false;
      try {
        await this.api('/api/whatsapp/messages/react', {
          method:'POST',
          body: JSON.stringify({ chat_jid: this.whatsapp.activeJid, message_id: m.id, emoji }),
        });
      } catch(e) { this.showToast('Failed to react: ' + (e.message||e), 'err'); }
    },
    async whatsappStarMsg() {
      const m = this.whatsapp.msgMenu.msg;
      if (!m) return;
      this.whatsapp.msgMenu.open = false;
      try {
        await this.api('/api/whatsapp/messages/star', {
          method:'POST',
          body: JSON.stringify({ chat_jid: this.whatsapp.activeJid, message_id: m.id, star: true }),
        });
      } catch(e) { this.showToast('Failed to star: ' + (e.message||e), 'err'); }
    },
    async whatsappForwardPrompt() {
      const m = this.whatsapp.msgMenu.msg;
      this.whatsapp.msgMenu.open = false;
      if (!m) return;
      const dst = await this.askInput({ title:'Forward message', label:'Forward to which chat? Paste the JID (e.g. 5522999@c.us or 1203...@g.us):', placeholder:'...@c.us' });
      if (!dst) return;
      this.api('/api/whatsapp/messages/forward', {
        method:'POST',
        body: JSON.stringify({ message_id: m.id, dest_jid: dst.trim() }),
      }).then(r => r.ok ? null : r.json().then(d=>{ throw new Error(d.error); }))
        .catch(e => this.showToast('Failed to forward: ' + (e.message||e), 'err'));
    },
    async whatsappDeleteMsg(mode) {
      const m = this.whatsapp.msgMenu.msg;
      if (!m) return;
      this.whatsapp.msgMenu.open = false;
      const confirmMsg = mode === 'everyone' ? 'Delete this message FOR EVERYONE?' : 'Delete locally?';
      if (!(await this.confirmAsync(confirmMsg))) return;
      try {
        await this.api('/api/whatsapp/messages/delete', {
          method:'POST',
          body: JSON.stringify({ chat_jid: this.whatsapp.activeJid, message_id: m.id, mode }),
        });
        m.deleted = true;
      } catch(e) { this.showToast('Failed to delete: ' + (e.message||e), 'err'); }
    },

    async whatsappTogglePin() {
      const jid = this.whatsapp.activeJid;
      const c = this.whatsapp.chatById[jid];
      if (!c) return;
      const value = !c.pinned;
      try {
        await this.api('/api/whatsapp/chat/pin', { method:'POST', body: JSON.stringify({ chat_jid: jid, value }) });
        c.pinned = value;
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
    },
    async whatsappToggleArchive() {
      const jid = this.whatsapp.activeJid;
      const c = this.whatsapp.chatById[jid];
      if (!c) return;
      const value = !c.archived;
      try {
        await this.api('/api/whatsapp/chat/archive', { method:'POST', body: JSON.stringify({ chat_jid: jid, value }) });
        c.archived = value;
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
    },
    async whatsappToggleMute() {
      const jid = this.whatsapp.activeJid;
      const c = this.whatsapp.chatById[jid];
      if (!c) return;
      const value = !c.muted;
      try {
        await this.api('/api/whatsapp/chat/mute', { method:'POST', body: JSON.stringify({ chat_jid: jid, value }) });
        c.muted = value;
        this.showToast && this.showToast(value ? 'Conversation muted' : 'Notifications re-enabled', 'ok');
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
    },
    async whatsappToggleBlock() {
      const jid = this.whatsapp.activeJid;
      const c = this.whatsapp.chatById[jid];
      if (!c) return;
      const value = !c._blocked;
      if (!(await this.confirmAsync(value ? 'Block this contact?' : 'Unblock this contact?'))) return;
      try {
        await this.api('/api/whatsapp/chat/block', { method:'POST', body: JSON.stringify({ chat_jid: jid, value }) });
        c._blocked = value;
        this.showToast && this.showToast(value ? 'Contact blocked' : 'Contact unblocked', 'ok');
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
    },
    async whatsappEditMsg() {
      const jid = this.whatsapp.activeJid;
      const m = this.whatsapp.msgMenu.msg;
      this.whatsapp.msgMenu.open = false;
      if (!m || !m.from_me || m.type !== 'text') return;
      const text = await this.askInput({ title:'Edit message', label:'Edit message:', value:m.body || '' });
      if (text === null) return;
      const newText = text.trim();
      if (!newText || newText === m.body) return;
      try {
        const r = await this.api('/api/whatsapp/messages/edit', { method:'POST', body: JSON.stringify({ chat_jid: jid, msg_id: m.id, text: newText }) });
        if (!r.ok) { const d = await r.json().catch(()=>({})); throw new Error(d.error || r.status); }
        const list = this.whatsapp.messages[jid] || [];
        const local = list.find(x => x.id === m.id);
        if (local) { local.body = newText; local.edited = true; } else { m.body = newText; m.edited = true; }
        this.showToast && this.showToast('Message edited', 'ok');
      } catch(e) { this.showToast('Failed to edit: ' + (e.message||e), 'err'); }
    },
    async whatsappNewChat() {
      const wa = this.whatsapp;
      const digits = (wa.newChatPhone || '').replace(/\D/g, '');
      if (!digits) { this.showToast && this.showToast('Enter a number with a country code', 'err'); return; }
      wa.newChatBusy = true;
      try {
        const r = await this.api('/api/whatsapp/check-number?phone=' + encodeURIComponent(digits));
        if (!r.ok) { const d = await r.json().catch(()=>({})); throw new Error(d.error || r.status); }
        const d = await r.json();
        if (d.on_wa && d.jid) {
          wa.newChatOpen = false;
          wa.newChatPhone = '';
          this.whatsappOpenChat(d.jid);
        } else {
          this.showToast && this.showToast('Number is not on WhatsApp', 'err');
        }
      } catch(e) { this.showToast('Failed: ' + (e.message||e), 'err'); }
      wa.newChatBusy = false;
    },
    async whatsappGroupInfo() {
      const jid = this.whatsapp.activeJid;
      if (!jid || !jid.endsWith('@g.us')) return;
      this.whatsapp.groupInfoLoading = true;
      this.whatsapp.groupInfo = null;
      try {
        const r = await this.api('/api/whatsapp/group-info?jid=' + encodeURIComponent(jid));
        if (!r.ok) { const d = await r.json().catch(()=>({})); throw new Error(d.error || r.status); }
        const d = await r.json();
        this.whatsapp.groupInfo = { name: d.name || '', participants: d.participants || [] };
      } catch(e) { this.showToast('Failed to load the group: ' + (e.message||e), 'err'); }
      this.whatsapp.groupInfoLoading = false;
    },

    async whatsappVoiceStart() {
      const wa = this.whatsapp;
      if (wa.voiceRec.active) return;
      if (!navigator.mediaDevices || !window.MediaRecorder) {
        this.showToast('Your browser does not support audio recording.', 'err');
        return;
      }
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio:true });
        const mime = MediaRecorder.isTypeSupported('audio/webm;codecs=opus') ? 'audio/webm;codecs=opus' : '';
        const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
        wa.voiceRec.chunks = [];
        wa.voiceRec.recorder = rec;
        wa.voiceRec.active = true;
        wa.voiceRec.startedAt = Date.now();
        wa.voiceRec.elapsed = 0;
        rec.ondataavailable = (ev) => { if (ev.data && ev.data.size) wa.voiceRec.chunks.push(ev.data); };
        rec.onstop = () => {
          stream.getTracks().forEach(t => t.stop());
        };
        rec.start();
        wa.voiceRec.timer = setInterval(() => {
          wa.voiceRec.elapsed = Math.floor((Date.now() - wa.voiceRec.startedAt) / 1000);
          if (wa.voiceRec.elapsed > 300) this.whatsappVoiceStop();
        }, 250);
      } catch(e) {
        this.showToast('Failed to access the microphone: ' + (e.message || e), 'err');
      }
    },
    async _whatsappPostMedia({ file, type, caption, replyTo, previewURL }) {
      const wa = this.whatsapp;
      if (wa._sending) return;
      wa._sending = true;
      if (wa.busy) { wa._sending = false; return; }
      if (!this._waCheckSendToken()) { wa._sending = false; return; }
      const jid = wa.activeJid;
      if (!jid) { wa._sending = false; return; }
      wa.busy = true;
      const cid = (crypto && crypto.randomUUID) ? crypto.randomUUID()
                : ('xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, c => {
                    const r = Math.random()*16|0; return (c==='x' ? r : (r&0x3)|0x8).toString(16);
                  }));
      const optim = {
        id: '_optim:' + cid,
        client_msg_id: cid,
        chat_jid: jid,
        from_me: true,
        body: caption || '',
        ts: Math.floor(Date.now()/1000),
        ack: 0,
        type,
        _optim: true,
        _optim_filename: file.name,
      };
      if (previewURL) optim._optim_media = previewURL;
      if (replyTo) optim.quoted = { id: replyTo.id, body: replyTo.body, from_me: replyTo.from_me };
      if (!wa.messages[jid]) wa.messages[jid] = [];
      wa.messages[jid] = this._sanitizeWaMessages(wa.messages[jid].concat([optim]));
      wa.scrollPinned = true;
      this.$nextTick(() => this._whatsappScrollBottom());
      try {
        const fd = new FormData();
        fd.append('file', file);
        fd.append('type', type);
        if (caption) fd.append('caption', caption);
        if (replyTo) fd.append('quoted_id', replyTo.id);
        fd.append('client_msg_id', cid);
        const r = await fetch('/api/whatsapp/chats/' + encodeURIComponent(jid) + '/messages', {
          method: 'POST',
          headers: { 'Authorization': 'Bearer ' + this.token },
          body: fd,
        });
        if (!r.ok) {
          const d = await r.json().catch(()=>({}));
          throw new Error(d.error || ('HTTP ' + r.status));
        }
        const sent = await r.json().catch(()=>null);
        if (sent && sent.id) optim.id = sent.id;
        optim.ack = 1;
        optim._optim = false;
      } catch(e) {
        optim.ack = -1;
        optim._error = e.message || String(e);
        this.showToast('Failed to send: ' + (e.message || e), 'err');
      } finally {
        wa.busy = false;
        wa._sending = false;
        if (wa.messages[jid]) wa.messages[jid] = wa.messages[jid].slice();
      }
    },
    whatsappOnComposerPaste(ev) {
      const items = ev.clipboardData && ev.clipboardData.items;
      if (!items) return;
      for (const it of items) {
        if (it.kind === 'file' && it.type.startsWith('image/')) {
          const f = it.getAsFile();
          if (f) { ev.preventDefault(); this._whatsappQueueFile(f); return; }
        }
      }
    },
    async whatsappVoiceStop() {
      const wa = this.whatsapp;
      if (!wa.voiceRec.active || !wa.voiceRec.recorder) return;
      if (wa.voiceRec.timer) clearInterval(wa.voiceRec.timer);
      wa.voiceRec.timer = null;
      const rec = wa.voiceRec.recorder;
      const chunks = wa.voiceRec.chunks;
      await new Promise(resolve => {
        rec.addEventListener('stop', resolve, { once: true });
        rec.stop();
      });
      wa.voiceRec.active = false;
      wa.voiceRec.recorder = null;
      wa.voiceRec.chunks = [];
      if (chunks.length === 0) return;
      const blob = new Blob(chunks, { type: chunks[0].type || 'audio/webm' });
      const file = new File([blob], 'voice-' + Date.now() + '.webm', { type: blob.type });
      await this._whatsappPostMedia({ file, type: 'voice' });
    },
    whatsappVoiceCancel() {
      const wa = this.whatsapp;
      if (!wa.voiceRec.active) return;
      if (wa.voiceRec.timer) clearInterval(wa.voiceRec.timer);
      wa.voiceRec.timer = null;
      if (wa.voiceRec.recorder) {
        try { wa.voiceRec.recorder.stop(); } catch(_){}
      }
      wa.voiceRec.active = false;
      wa.voiceRec.recorder = null;
      wa.voiceRec.chunks = [];
    },
    whatsappFmtElapsed(s) {
      const m = Math.floor(s / 60), sec = s % 60;
      return String(m).padStart(2,'0') + ':' + String(sec).padStart(2,'0');
    },

    whatsappPresenceLabel(c) {
      if (!c) return { text:'', color:'text-muted' };
      const p = c.presence || '';
      if (p === 'composing') return { text:'typing…', color:'text-[#10b981] font-medium' };
      if (p === 'recording') return { text:'recording audio…', color:'text-[#10b981] font-medium' };
      if (p === 'available') return { text:'online', color:'text-[#10b981]' };
      if (c.last_seen_ts && c.last_seen_ts > 0) {
        return { text:'last seen ' + this._fmtRelativeTime(c.last_seen_ts), color:'text-muted' };
      }
      return { text: this.whatsappChatSubtitle(c), color:'text-muted' };
    },
    _fmtRelativeTime(ts) {
      const d = new Date(ts * 1000);
      const now = new Date();
      const sameDay = d.toDateString() === now.toDateString();
      if (sameDay) {
        return 'today at ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0');
      }
      const y = new Date(now); y.setDate(y.getDate() - 1);
      if (d.toDateString() === y.toDateString()) {
        return 'yesterday at ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0');
      }
      return d.toLocaleDateString('en-US') + ' at ' + String(d.getHours()).padStart(2,'0') + ':' + String(d.getMinutes()).padStart(2,'0');
    },
    whatsappFmtLastSeen(ts) {
      if (!ts) return '—';
      return this._fmtRelativeTime(ts);
    },

    whatsappDropFile(ev) {
      this.whatsapp.dragOver = false;
      const f = ev.dataTransfer?.files?.[0];
      if (!f) return;
      this._whatsappQueueFile(f);
    },
    _whatsappQueueFile(f) {
      if (!f) return;
      if (f.size > 100 * 1024 * 1024) {
        this.showToast('File larger than 100MB (WhatsApp limit)', 'err');
        return;
      }
      if (f.type.startsWith('image/')) {
        const reader = new FileReader();
        reader.onload = () => {
          this.whatsapp.imagePreview = { file: f, dataURL: reader.result, caption: '' };
        };
        reader.readAsDataURL(f);
        return;
      }
      this.whatsapp.attachment = f;
    },
    async whatsappSendImagePreview() {
      const wa = this.whatsapp;
      const p = wa.imagePreview;
      if (!p || !p.file) return;
      const replyTo = wa.replyTo;
      const caption = p.caption;
      wa.imagePreview = null;
      wa.replyTo = null;
      await this._whatsappPostMedia({ file: p.file, type: 'image', caption, replyTo, previewURL: p.dataURL });
    },

    whatsappFilteredMessages(msgs) {
      const q = (this.whatsapp.chatSearch||'').toLowerCase().trim();
      if (!q) return msgs;
      return msgs.filter(m => (m.body || '').toLowerCase().includes(q));
    },

    isOnlyEmoji(text) {
      if (!text) return false;
      const t = text.replace(/\s/g,'').replace(/[‍️]/g,'');
      if (t.length === 0 || t.length > 16) return false;
      const hasEmoji = /[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}\u{1F000}-\u{1F1FF}]/u.test(t);
      const hasNonEmoji = /[a-zA-Z0-9]/.test(t);
      return hasEmoji && !hasNonEmoji;
    },

    async whatsappLoadStatus() {
      const wa = this.whatsapp;
      wa.statusLoading = true;
      try {
        const r = await this.api('/api/whatsapp/status-updates');
        if (!r.ok) return;
        const d = await r.json();
        const senders = (d.by_sender || []).filter(s => s && s.jid).map((s, idx) => ({
          jid: s.jid,
          name: s.name || '',
          items: (s.items || []).filter(it => it).map((it, j) => ({
            id: it.id || ('synth-'+idx+'-'+j),
            ts: it.ts || 0,
            type: it.type || '',
            body: it.body || '',
            media: it.media || null,
          })),
        }));
        wa.statusBySender = senders.sort((a,b) => {
          const ta = (a.items||[]).reduce((m,it)=>Math.max(m,it.ts||0),0);
          const tb = (b.items||[]).reduce((m,it)=>Math.max(m,it.ts||0),0);
          return tb - ta;
        });
      } catch(_) {
        wa.statusBySender = [];
      } finally {
        wa.statusLoading = false;
      }
    },

    whatsappOnComposerInput() {
      const wa = this.whatsapp;
      const jid = wa.activeJid;
      if (!jid) return;
      if (wa.typingTimer) clearTimeout(wa.typingTimer);
      const now = Date.now();
      if (!wa._lastTypingAt || now - wa._lastTypingAt > 4000) {
        wa._lastTypingAt = now;
        this.api('/api/whatsapp/chat/typing', {
          method:'POST', body: JSON.stringify({ chat_jid: jid, value: true })
        }).catch(()=>{});
      }
      wa.typingTimer = setTimeout(() => {
        wa._lastTypingAt = 0;
        this.api('/api/whatsapp/chat/typing', {
          method:'POST', body: JSON.stringify({ chat_jid: jid, value: false })
        }).catch(()=>{});
      }, 1500);
    },

    _whatsappAfterOpen(jid) {
      this.api('/api/whatsapp/chat/subscribe-presence', {
        method:'POST', body: JSON.stringify({ chat_jid: jid })
      }).catch(()=>{});
      const c = this.whatsapp.chatById[jid];
      if (c && !c.avatar_url && !c._avatar_checked) {
        c._avatar_checked = true;
        fetch('/api/whatsapp/avatar/' + encodeURIComponent(jid), {
          method:'GET', headers: { 'Authorization':'Bearer ' + this.token }, redirect:'manual'
        }).then(r => {
          if (!r) return;
          if (r.status === 404 || r.status === 204) return;
          if (r.status === 429) { c._avatar_checked = false; return; }
          if (this.whatsapp.chatById[jid]) this.whatsapp.chatById[jid].avatar_url = '1';
        }).catch(()=>{});
      }
    },

    whatsappPickFile(ev) {
      const f = ev.target.files && ev.target.files[0];
      if (!f) return;
      this._whatsappQueueFile(f);
      ev.target.value = '';
    },

    whatsappMediaURL(p) {
      if (!p) return '';
      if (p.startsWith('http')) return p;
      return '/api/whatsapp/media/' + p.replace(/^\/+/, '');
    },

    whatsappMediaIcon(t) {
      switch (t) {
        case 'image': return '🖼️';
        case 'video': return '🎬';
        case 'voice': return '🎙️';
        case 'audio': return '🎵';
        case 'document': return '📄';
        default: return '📎';
      }
    },
    whatsappMediaLabel(t) {
      switch (t) {
        case 'image': return 'image';
        case 'video': return 'video';
        case 'voice': return 'voice audio';
        case 'audio': return 'audio';
        case 'document': return 'file';
        default: return 'media';
      }
    },

    async whatsappDownloadMedia(m) {
      const chat = m && (m.chat || m.chat_jid);
      if (!m || !m.id || !chat) return;
      if (m._downloading) return;
      m._downloading = true;
      m._downloadErr = '';
      this._whatsappRerender(chat);
      try {
        const r = await this.api(
          '/api/whatsapp/messages/download/' + encodeURIComponent(m.id)
          + '?chat=' + encodeURIComponent(chat),
          { method: 'POST' }
        );
        const d = await r.json().catch(() => ({}));
        if (!r.ok) {
          m._downloadErr = d.error || ('HTTP ' + r.status);
          return;
        }
        if (!m.media) m.media = {};
        m.media.path = d.path || '';
        if (d.mime) m.media.mime = d.mime;
        if (d.size) m.media.size = d.size;
      } catch (e) {
        m._downloadErr = (e && e.message) || 'network error';
      } finally {
        m._downloading = false;
        this._whatsappRerender(chat);
      }
    },

    _whatsappRerender(jid) {
      const wa = this.whatsapp;
      if (jid && Array.isArray(wa.messages[jid])) {
        wa.messages[jid] = wa.messages[jid].slice();
      }
    },

    _whatsappNotify(chat, m) {
      const wa = this.whatsapp;
      if (wa.notifPermission !== 'granted') {
        if (wa.notifPermission === 'default' && typeof Notification !== 'undefined') {
          Notification.requestPermission().then(p => { wa.notifPermission = p; });
        }
        return;
      }
      try {
        const title = chat.name || chat.jid.split('@')[0];
        const body = this._whatsappPreview(m);
        const n = new Notification('WhatsApp · ' + title, { body, tag: chat.jid, renotify: false });
        n.onclick = () => {
          window.focus();
          this.setPage('whatsapp');
          this.whatsappOpenChat(chat.jid);
        };
      } catch(_){}
    },

    chatColor(jid) {
      if (!jid) return 'hsl(220,15%,30%)';
      let h = 0;
      for (let i = 0; i < jid.length; i++) h = ((h<<5) - h + jid.charCodeAt(i)) | 0;
      const hue = Math.abs(h) % 360;
      return 'hsl(' + hue + ',45%,40%)';
    },
    fmtTime(ts) {
      if (!ts) return '';
      const d = new Date(ts * 1000);
      const now = new Date();
      if (d.toDateString() === now.toDateString()) {
        return d.toTimeString().slice(0,5);
      }
      const dayMs = 86400 * 1000;
      if (now - d < 7 * dayMs) {
        return ['sun','mon','tue','wed','thu','fri','sat'][d.getDay()];
      }
      return d.toLocaleDateString();
    },
    fmtBytes(n) {
      if (!n) return '0 B';
      const u = ['B','KB','MB','GB','TB','PB']; let i = 0;
      while (n >= 1024 && i < u.length-1) { n /= 1024; i++; }
      return n.toFixed(1) + ' ' + u[i];
    },
    ackTicks(a) {
      if (a >= 2) return '✓✓';
      if (a >= 1) return '✓';
      return '🕐';
    },

    contName(c){ return (c.Names&&c.Names[0]||'').replace(/^\//,''); },
    portsStr(ps){ return (ps||[]).map(p => p.PublicPort?`${p.PublicPort}:${p.PrivatePort}/${p.Type}`:`${p.PrivatePort}/${p.Type}`).join(', '); },
    imgRepo(img){ const t=(img.RepoTags&&img.RepoTags[0])||'<none>:<none>'; return t.split(':').slice(0,-1).join(':')||'<none>'; },
    imgTag(img){ const t=(img.RepoTags&&img.RepoTags[0])||'<none>:<none>'; return t.split(':').slice(-1)[0]||'<none>'; },
    filteredContainers(){
      const q=(this.filter.containers||'').toLowerCase(); const src=this.containers;
      return __panelMemo('containers', [src, src && src.length, q], () =>
        q?src.filter(c=>(this.contName(c)+c.Image+c.Status).toLowerCase().includes(q)):src);
    },
    filteredImages(){ const q=(this.filter.images||'').toLowerCase(); return q?this.images.filter(i=>((i.RepoTags||[]).join(' ')+(i.Id||'')).toLowerCase().includes(q)):this.images; },
    filteredVolumes(){ const q=(this.filter.volumes||'').toLowerCase(); const arr=(this.volumes&&this.volumes.Volumes)||this.volumes||[]; return q?arr.filter(v=>(v.Name+(v.Driver||'')+(v.Mountpoint||'')).toLowerCase().includes(q)):arr; },
    filteredNetworks(){ const q=(this.filter.networks||'').toLowerCase(); return q?this.networks.filter(n=>(n.Name+(n.Driver||'')+(n.Id||'')).toLowerCase().includes(q)):this.networks; },
    filteredCompose(){ const q=(this.filter.compose||'').toLowerCase(); return q?this.composeProjects.filter(p=>((p.Name||'')+(p.WorkingDir||'')).toLowerCase().includes(q)):this.composeProjects; },
    bytes(n){ if(n===null||n===undefined)return '—'; if(n===0)return '0 B'; const u=['B','KB','MB','GB','TB']; let i=0; while(n>=1024&&i<u.length-1){n/=1024;i++;} return n.toFixed(1)+' '+u[i]; },
    cpuClass(p){ p = Number(p)||0; if (p >= 90) return 'meter-crit'; if (p >= 70) return 'meter-warn'; return ''; },
    memClass(p){ p = Number(p)||0; if (p >= 90) return 'meter-crit'; if (p >= 75) return 'meter-warn'; return ''; },
    diskClass(p){ p = Number(p)||0; if (p >= 95) return 'meter-crit'; if (p >= 85) return 'meter-warn'; return ''; },
    clamp01(p){ p = Number(p); if (!Number.isFinite(p) || p < 0) return 0; if (p > 100) return 100; return p; },
    uptime(s){ if(!s)return '—'; const d=Math.floor(s/86400), h=Math.floor((s%86400)/3600), m=Math.floor((s%3600)/60); if(d>0)return d+'d '+h+'h'; if(h>0)return h+'h '+m+'m'; return m+'m'; },
    timeAgo(ts){ if(!ts)return '—'; const s=Math.floor(Date.now()/1000-ts); if(s<60)return s+'s ago'; if(s<3600)return Math.floor(s/60)+'m ago'; if(s<86400)return Math.floor(s/3600)+'h ago'; return Math.floor(s/86400)+'d ago'; },
    fmtAbs(ts){ if(ts===undefined||ts===null||ts==='')return ''; let d; if(typeof ts==='number'){ d=new Date(ts*1000); } else { d=new Date(ts); } return isNaN(d.getTime())?'':d.toLocaleString(); },
    stateClass(s){ if(s==='running')return 'bg-running'; if(s==='exited'||s==='dead')return 'bg-exited'; if(s==='paused')return 'bg-paused'; if(s==='restarting')return 'bg-restart'; return 'bg-other'; },

    async vcLoadRooms() {
      if (this.videocall.busy) return;
      this.videocall.busy = true;
      try {
        const r = await fetch('/api/videocall/rooms', { headers: { Authorization: 'Bearer ' + this.token } });
        if (r.ok) this.videocall.rooms = await r.json();
        this.videocall.loaded = true;
      } catch (e) {
        this.vcError('failed to load rooms: ' + e.message);
      } finally {
        this.videocall.busy = false;
      }
    },
    async vcCreateRoom() {
      const name = (this.videocall.newRoomName || '').trim();
      if (!name) return;
      try {
        const r = await fetch('/api/videocall/rooms', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ name }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.videocall.newRoomName = '';
        await this.vcLoadRooms();
      } catch (e) { this.vcError('create room: ' + e.message); }
    },
    vcStartRenameRoom() {
      if (!this.videocall.selectedRoom) return;
      if (this.videocall.selectedRoom.owner !== this.username) return;
      this.videocall.renameDraft = this.videocall.selectedRoom.name || '';
      this.videocall.renamingRoomId = this.videocall.selectedRoom.id;
      this.$nextTick(() => {
        const el = document.getElementById('vc-rename-input');
        if (el) { el.focus(); el.select(); }
      });
    },
    vcCancelRenameRoom() {
      this.videocall.renamingRoomId = '';
      this.videocall.renameDraft = '';
    },
    async vcConfirmRenameRoom() {
      const id = this.videocall.renamingRoomId;
      const name = (this.videocall.renameDraft || '').trim();
      if (!id || !name) { this.vcCancelRenameRoom(); return; }
      if (this.videocall.selectedRoom && this.videocall.selectedRoom.name === name) {
        this.vcCancelRenameRoom(); return;
      }
      try {
        const r = await fetch('/api/videocall/rooms', {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ id, name }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
        const fresh = await r.json();
        if (this.videocall.selectedRoom && this.videocall.selectedRoom.id === id) {
          this.videocall.selectedRoom = fresh;
        }
        const idx = this.videocall.rooms.findIndex(rm => rm.id === id);
        if (idx >= 0) this.videocall.rooms[idx] = fresh;
        this.vcCancelRenameRoom();
      } catch (e) { this.vcError('rename: ' + e.message); }
    },

    vcDeleteRoom(roomId) {
      this.vcAskConfirm({
        title: 'Delete room?',
        desc: 'Every member loses access and anyone currently in the call is disconnected. History and recordings are preserved.',
        confirmLabel: '🗑 Delete room',
        danger: true,
        onYes: () => this._vcDeleteRoomNow(roomId),
      });
    },
    async _vcDeleteRoomNow(roomId) {
      try {
        const r = await fetch('/api/videocall/rooms?id=' + encodeURIComponent(roomId), {
          method: 'DELETE', headers: { Authorization: 'Bearer ' + this.token },
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        if (this.videocall.activeRoomId === roomId) this.vcHangup();
        if (this.videocall.selectedRoom && this.videocall.selectedRoom.id === roomId) this.videocall.selectedRoom = null;
        await this.vcLoadRooms();
      } catch (e) { this.vcError('delete: ' + e.message); }
    },
    async vcAddMember() {
      const room = this.videocall.selectedRoom;
      const member = (this.videocall.newMember || '').trim();
      if (!room || !member) return;
      try {
        const r = await fetch('/api/videocall/members', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: room.id, member }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.videocall.newMember = '';
        await this.vcLoadRooms();
        const fresh = this.videocall.rooms.find(rm => rm.id === room.id);
        if (fresh) this.videocall.selectedRoom = fresh;
      } catch (e) { this.vcError('add member: ' + e.message); }
    },
    async vcRemoveMember(member) {
      const room = this.videocall.selectedRoom;
      if (!room) return;
      try {
        const r = await fetch('/api/videocall/members?room_id=' + encodeURIComponent(room.id) + '&member=' + encodeURIComponent(member), {
          method: 'DELETE', headers: { Authorization: 'Bearer ' + this.token },
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.vcLoadRooms();
        const fresh = this.videocall.rooms.find(rm => rm.id === room.id);
        if (fresh) this.videocall.selectedRoom = fresh;
      } catch (e) { this.vcError('remove member: ' + e.message); }
    },
    vcSelectRoom(room) {
      this.videocall.selectedRoom = room;
    },
    async vcJoinCall(roomId) {
      if (!(await this._ensureScript('/vendor/panel/videocall.js', 'PanelVideoCall'))) {
        this.vcError('the video call client did not load');
        return;
      }
      if (this.videocall.inCall) return;
      let passphrase = '';
      if (this.videocall.e2eeWanted) {
        if (!this.videocall.e2eeSupported) {
          if (!(await this.confirmAsync('Your browser does not support end-to-end encryption. Continue WITHOUT E2EE?'))) return;
        } else {
          this.videocall.e2eePendingRoomId = roomId;
          this.videocall.e2eePassphraseInput = '';
          this.videocall.e2eePromptOpen = true;
          return;
        }
      }
      if (this.videocall.lobbySkipNext) {
        return this._vcStartCall(roomId, passphrase);
      }
      return this.vcLobbyOpen(roomId, passphrase);
    },
    async vcConfirmPassphrase() {
      const pass = (this.videocall.e2eePassphraseInput || '').trim();
      if (pass.length < 6) { this.vcError('the passphrase needs at least 6 characters'); return; }
      const roomId = this.videocall.e2eePendingRoomId;
      this.videocall.e2eePromptOpen = false;
      this.videocall.e2eePendingRoomId = '';
      this.videocall.e2eePassphraseInput = '';
      if (this.videocall.lobbySkipNext) {
        await this._vcStartCall(roomId, pass);
      } else {
        await this.vcLobbyOpen(roomId, pass);
      }
    },
    vcCancelPassphrase() {
      this.videocall.e2eePromptOpen = false;
      this.videocall.e2eePendingRoomId = '';
      this.videocall.e2eePassphraseInput = '';
    },
    async _vcStartCall(roomId, passphrase, caps) {
      const container = document.getElementById('vc-videos');
      if (!container) { this.vcError('video container not found'); return; }
      container.innerHTML = '';
      this.videocall.activeRoomId = roomId;
      this.videocall.inCall = true;
      this.videocall.callStartedAt = Date.now();
      this.videocall.amOwner = false;
      this.videocall.roomOwnerName = '';
      this.videocall.roomLocked = false;
      this.videocall.peers = [];
      this._vcDeriveOwner = () => {
        const r = this.videocall.selectedRoom;
        if (r && this.username && r.owner === this.username) {
          this.videocall.amOwner = true;
          this.videocall.roomOwnerName = r.owner;
        } else if (r) {
          this.videocall.amOwner = false;
          this.videocall.roomOwnerName = r.owner || '';
        }
      };
      this._vcDeriveOwner();
      this._vcSetupMirrorObserver();
      this._vcSetupPopoverBounds();
      if (window.PanelSTT && window.PanelSTT.probeWhisperLocal) {
        window.PanelSTT.probeWhisperLocal().then(ok => {
          this.videocall.whisperLocalAvailable = !!ok;
        }).catch(() => { this.videocall.whisperLocalAvailable = false; });
      }
      if (!this.videocall.selectedRoom || this.videocall.selectedRoom.id !== roomId) {
        const fresh = (this.videocall.rooms || []).find(r => r.id === roomId);
        if (fresh) {
          this.videocall.selectedRoom = fresh;
          this._vcDeriveOwner();
        } else {
          this.vcLoadRooms && this.vcLoadRooms().then(() => {
            const r = (this.videocall.rooms || []).find(x => x.id === roomId);
            if (r) {
              this.videocall.selectedRoom = r;
              this._vcDeriveOwner();
            }
          }).catch(() => {});
        }
      }
      setTimeout(() => { if (this._vcDeriveOwner) this._vcDeriveOwner(); }, 1000);
      setTimeout(() => { if (this._vcDeriveOwner) this._vcDeriveOwner(); }, 3000);
      this.$nextTick(() => { try { this.vcRestoreTranscriptFromDB && this.vcRestoreTranscriptFromDB(); } catch(_){} });
      const deviceIds = {
        camera:  this.videocall.selectedDevices.camera,
        mic:     this.videocall.selectedDevices.mic,
        speaker: this.videocall.selectedDevices.speaker,
      };
      this.videocall.chat = [];
      this.videocall.transcript = '';
      this.videocall.muted = false;
      this.videocall.videoOff = !!(caps && caps.audioOnly);
      this.videocall.screenSharing = false;
      this.videocall.recording = false;
      this.videocall.autoVideoOff = false;
      this.videocall.annotActive = false;
      this._annotWasAvailable = false;
      this.videocall.e2eeActive = !!passphrase;
      try {
        await window.PanelVideoCall.connect({
          roomId,
          token: this.token,
          guestMode: this.guestMode,
          displayName: this.username || 'User',
          codec: this.videocall.codec,
          quality: this.videocall.qualityMode === 'custom' ? null : this.videocall.qualityMode,
          budgetKbps: this.videocall.budgetKbps,
          e2eePassphrase: passphrase,
          audioFirstMode: this.videocall.audioFirstMode,
          deviceIds: deviceIds,
          micGain: this.videocall.micGain,
          micProcessing: this.videocall.micProc,
          audioOnly: !!(caps && caps.audioOnly),
          micOff:    !!(caps && caps.micOff),
          videosEl: container,
          onState: (ev) => {
            if (ev.type === 'devices-degraded') { this.vcInfo('Devices: ' + ev.note); return; }
            if (ev.type === 'disconnected') {
              this.videocall.inCall = false;
              this.videocall.activeRoomId = '';
              this.videocall.e2eeActive = false;
              this.videocall.peerStates = {};
              this.videocall.annotActive = false;
              this._annotWasAvailable = false;
              this.vcAnnotTeardown();
              if (this.guestMode) {
                try {
                  sessionStorage.removeItem('panel_vc_guest_token');
                  sessionStorage.removeItem('panel_vc_guest_room_id');
                  sessionStorage.removeItem('panel_vc_guest_room_name');
                  sessionStorage.removeItem('panel_vc_guest_expires_at');
                  history.replaceState(null, '', '/');
                } catch (_) {}
                this.videocall.guestEnded = true;
                return;
              }
              setTimeout(() => this.vcLoadHistory(), 800);
            } else if (ev.type === 'screen-share') {
              this.videocall.screenSharing = !!ev.on;
              this.vcAnnotReconcile();
            } else if (ev.type === 'recording') {
              this.videocall.recording = !!ev.on;
            } else if (ev.type === 'auto-video-off') {
              this.videocall.autoVideoOff = true;
              this.vcError('poor network: video paused automatically (audio-first)');
            } else if (ev.type === 'auto-video-on') {
              this.videocall.autoVideoOff = false;
            } else if (ev.type === 'weak-connection') {
              this.videocall.weakConnection = true;
              this.videocall.weakConnLoss = ev.loss || 0;
              this.videocall.weakConnRtt = ev.rtt || 0;
              this.vcInfo('Weak connection detected (loss ' + (ev.loss||0) + '%, latency ' + (ev.rtt||0) + 'ms)');
            } else if (ev.type === 'connection-recovered') {
              this.videocall.weakConnection = false;
              this.videocall.weakConnLoss = 0;
              this.videocall.weakConnRtt = 0;
            } else if (ev.type === 'turn-exhausted') {
              this.vcError('TURN server unavailable after several attempts — the connection can be lost on networks with a restrictive NAT');
            } else if (ev.type === 'codec-downgrade') {
              this.vcError('AV1 at a low framerate: falling back to ' + ev.to);
            } else if (ev.type === 'peer-count') {
              this.videocall.peerCount = ev.count || 0;
              this.videocall.peersList = ev.peers || [];
              this.videocall.peers = ev.peers || [];
              const live = new Set((ev.peers || []).map(p => p.id));
              const cleaned = {};
              for (const id in this.videocall.peerStates) {
                if (live.has(id)) cleaned[id] = this.videocall.peerStates[id];
              }
              this.videocall.peerStates = cleaned;
              this.vcAnnotReconcile();
              const keep = (obj) => {
                const o = {};
                for (const id in obj) if (id === 'me' || live.has(id)) o[id] = obj[id];
                return o;
              };
              this.videocall.captionsByPeer = keep(this.videocall.captionsByPeer);
              this.videocall.livePartials = keep(this.videocall.livePartials);
              if (this._livePartialTimers) {
                for (const k in this._livePartialTimers) {
                  if (k !== 'me' && !live.has(k)) {
                    clearTimeout(this._livePartialTimers[k]);
                    delete this._livePartialTimers[k];
                  }
                }
              }
            } else if (ev.type === 'owner-action') {
              const targetName = this.vcPeerName(ev.target) || ev.target.slice(-6);
              const byName = this.vcPeerName(ev.by) || 'Owner';
              if (ev.action === 'mute') this.vcToast('🔇 ' + byName + ' muted ' + targetName);
              else if (ev.action === 'camera-off') this.vcToast('📷 ' + byName + ' turned off the camera of ' + targetName);
              else if (ev.action === 'kick') this.vcToast('🚪 ' + byName + ' removed ' + targetName + ' from the call');
            } else if (ev.type === 'owner-request-unmute') {
              this.vcInfo('The owner asked you to unmute.');
            } else if (ev.type === 'room-locked') {
              this.videocall.roomLocked = !!ev.locked;
              const byName = this.vcPeerName(ev.by) || 'Owner';
              this.vcToast(ev.locked ? '🔒 ' + byName + ' locked the room' : '🔓 ' + byName + ' unlocked the room');
            } else if (ev.type === 'owner-transferred') {
              const newOwner = ev.newOwner || '';
              this.vcInfo('Ownership transferred to ' + newOwner);
              if (this.videocall.selectedRoom) {
                this.videocall.selectedRoom.owner = newOwner;
                if (this._vcDeriveOwner) this._vcDeriveOwner();
              }
            } else if (ev.type === 'mic-gain') {
              if (typeof ev.value === 'number') this.videocall.micGain = ev.value;
            } else if (ev.type === 'mic-muted') {
              this.videocall.muted = true;
              if (ev.forced) this.vcToast('🔇 ' + (this.vcPeerName(ev.by) || 'The room owner') + ' muted your microphone');
            } else if (ev.type === 'mic-processing') {
              if (ev.value) this.videocall.micProc = Object.assign({}, ev.value);
            } else if (ev.type === 'subtitles-backend') {
              this.videocall.subtitlesBackendActive = ev.backend || '';
              this.videocall.subtitlesBackend = ev.backend || '';
              if (this.videocall.sttBackend === 'whisper-local' && ev.backend === 'web-speech') {
                this.vcInfo('Local Whisper unavailable, using Web Speech');
              }
            } else if (ev.type === 'peer-state') {
              this.vcHandlePeerState(ev.from, ev.state || {});
              this.vcAnnotReconcile();
              const st = ev.state || {};
              if (st.quality_force && window.PanelVideoCall && window.PanelVideoCall.applyQualityProfile) {
                this.videocall.qualityMode = st.quality_force;
                window.PanelVideoCall.applyQualityProfile(st.quality_force);
                this.vcInfo('The owner set the call mode to "' + st.quality_force + '"');
              }
            } else if (ev.type === 'speaking-while-muted') {
              this.videocall.speakingMuted = !!ev.on;
            } else if (ev.type === 'reconnecting') {
              this.videocall.reconnectingOverlay = true;
            } else if (ev.type === 'reconnected') {
              this.videocall.reconnectingOverlay = false;
            } else if (ev.type === 'kicked') {
              const msg = ev.reason || 'You were removed from the call.';
              this.vcError(msg);
              setTimeout(() => {
                try { window.PanelVideoCall && window.PanelVideoCall.disconnect(); } catch(_) {}
              }, 200);
            } else if (ev.type === 'reconnect-gave-up') {
              this.videocall.reconnectingOverlay = false;
              if (this.guestMode) {
                try { sessionStorage.clear(); } catch (_) {}
                location.href = '/join?expired=1';
              } else {
                this.vcError('Connection lost. Check your network and reload the page.');
                this.vcHangup();
              }
            } else if (ev.type === 'caption') {
              const fromLabel = ev.displayName || this.vcPeerLabel(ev.from);
              this.videocall.currentCaption = {
                from: fromLabel, text: ev.text || '', peerId: ev.from,
                expireAt: Date.now() + (ev.final ? 4000 : 1500),
              };
              this.videocall.captionsByPeer = {
                ...this.videocall.captionsByPeer,
                [ev.from]: { text: ev.text || '', expireAt: Date.now() + (ev.final ? 4000 : 1500), fromLabel },
              };
              if (!ev.final && ev.text) {
                this.videocall.livePartials = {
                  ...this.videocall.livePartials,
                  [ev.from || 'me']: { text: ev.text, fromLabel, ts: Date.now() },
                };
                if (this.videocall.sidePanel === 'transcript') this.vcMaybeScrollTranscript();
                const fromKey = ev.from || 'me';
                if (this._livePartialTimers) {
                  if (this._livePartialTimers[fromKey]) clearTimeout(this._livePartialTimers[fromKey]);
                } else { this._livePartialTimers = {}; }
                this._livePartialTimers[fromKey] = setTimeout(() => {
                  const lp = { ...this.videocall.livePartials };
                  delete lp[fromKey];
                  this.videocall.livePartials = lp;
                  if (this._livePartialTimers) delete this._livePartialTimers[fromKey];
                }, 8000);
              } else if (ev.final && this.videocall.livePartials[ev.from || 'me']) {
                const lp = { ...this.videocall.livePartials };
                delete lp[ev.from || 'me'];
                this.videocall.livePartials = lp;
                if (this._livePartialTimers && this._livePartialTimers[ev.from || 'me']) {
                  clearTimeout(this._livePartialTimers[ev.from || 'me']);
                  delete this._livePartialTimers[ev.from || 'me'];
                }
              }
              if (ev.final && ev.text) {
                const stamp = new Date().toLocaleTimeString();
                this.videocall.transcript += '[' + stamp + '] ' + fromLabel + ': ' + ev.text + '\n';
                if (this.videocall.transcript.length > 50000) {
                  const lines = this.videocall.transcript.split('\n');
                  if (lines.length > 500) this.videocall.transcript = lines.slice(-500).join('\n');
                }
                const entryId = 'tr' + Date.now() + Math.random().toString(36).slice(2,5);
                const entry = {
                  id: entryId,
                  from: ev.from || 'me',
                  fromLabel, text: ev.text, ts: Date.now(), final: true,
                };
                if (Array.isArray(ev.words) && ev.words.length) entry.words = ev.words;
                if (typeof ev.confidence === 'number') entry.confidence = ev.confidence;
                if (typeof ev.startMs === 'number') entry.startMs = ev.startMs;
                if (typeof ev.endMs === 'number') entry.endMs = ev.endMs;
                this.videocall.transcriptEntries.push(entry);
                if (this.videocall.transcriptEntries.length > 500) {
                  this.videocall.transcriptEntries = this.videocall.transcriptEntries.slice(-500);
                }
                try { this._vcPersistEntry(entry); } catch (_) {}
                if (this.videocall.sidePanel === 'transcript') this.vcMaybeScrollTranscript();
                if (this.videocall.polishWithAI) this._vcEnqueuePolish(entryId);
              }
            } else if (ev.type === 'subtitles-requested') {
              const who = ev.requestedBy || this.vcPeerLabel(ev.from) || 'Another participant';
              if (ev.on) {
                this.vcInfo('🎙 ' + who + ' turned transcription on — your speech will be captured too');
                if (this.videocall.sidePanel !== 'transcript') {
                  this.videocall.sidePanel = 'transcript';
                }
              } else {
                this.vcInfo(who + ' turned transcription off (yours stays on)');
              }
            } else if (ev.type === 'subtitles-broadcast-result') {
              if (ev.on) {
                if (ev.total === 0) {
                  this.vcInfo('🎙 Transcription on — waiting for other participants');
                } else if (ev.sent === ev.total) {
                  this.vcInfo('🎙 Transcription on — request sent to ' + ev.total + ', waiting for confirmation');
                } else if (ev.sent === 0) {
                  this.vcInfo('🎙 Transcription on — asking the ' + ev.pending + ' participants');
                } else {
                  this.vcInfo('🎙 Transcription: ' + ev.sent + ' requested, ' + ev.pending + ' pending');
                }
                if (this.videocall.sidePanel !== 'transcript') {
                  this.videocall.sidePanel = 'transcript';
                }
              } else {
                this.vcInfo('Transcription off (each participant still controls their own mic)');
              }
            } else if (ev.type === 'subtitles-ack') {
              const who = this.vcPeerLabel(ev.from) || 'Participant';
              this.vcInfo('🎙 ' + who + ': starting transcription…');
            } else if (ev.type === 'subtitles-status') {
              if (!ev.ok) {
                const who = this.vcPeerLabel(ev.from) || 'Participant';
                const reason = ev.reason ? (' (' + ev.reason + ')') : '';
                this.vcInfo('⚠️ ' + who + ' could not turn transcription on' + reason);
              }
            } else if (ev.type === 'subtitles-state') {
              this.videocall.subtitlesActive = !!ev.active;
              if (ev.active && this.videocall.sidePanel !== 'transcript') {
                this.videocall.sidePanel = 'transcript';
              }
            }
          },
          onStats: (s) => {
            this.videocall.stats = s;
            if (s && s.peers) {
              const levels = {};
              for (const pid in s.peers) {
                const al = s.peers[pid] && s.peers[pid].audioLevel;
                if (typeof al === 'number') levels[pid] = al;
              }
              this.videocall.peerAudioLevels = levels;
              try {
                document.querySelectorAll('[data-vc-peer-tile]').forEach((el) => {
                  const pid = el.getAttribute('data-vc-peer-tile');
                  if (!pid) return;
                  const active = (levels[pid] || 0) > 0.04;
                  el.classList.toggle('vc-speaker-active', active);
                });
              } catch (_) {}
            }
          },
          onChat:  (m) => {
            if (typeof m.text === 'string' && m.text.startsWith('✨REACT✨')) {
              this.vcShowReaction({ emoji: m.text.slice('✨REACT✨'.length), from: m.from, ts: m.ts });
              return;
            }
            this.videocall.chat.push(m);
            if (this.videocall.chat.length > 200) this.videocall.chat = this.videocall.chat.slice(-200);
            if (this.videocall.sidePanel !== 'chat') {
              // Increment unread implicit via chatLastRead being behind
            }
          },
          onError: (msg) => this.vcError(msg),
          onFileProgress: (p) => this.vcOnFileProgress(p),
          onFileReceived: (f) => this.vcOnFileReceived(f),
          onWhiteboard:   (w) => this.vcOnWhiteboard(w),
          onRecordingReady: (r) => {
            if (this.videocall.recordToCloud) {
              this.vcUploadRecording(r.blob, r.durationS);
            } else {
              const url = URL.createObjectURL(r.blob);
              const a = document.createElement('a');
              const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
              a.href = url; a.download = 'videocall-' + stamp + '.webm';
              document.body.appendChild(a); a.click(); a.remove();
              setTimeout(() => URL.revokeObjectURL(url), 60000);
            }
          },
        });
      } catch (e) {
        this.videocall.inCall = false;
        this.videocall.activeRoomId = '';
        this.vcError('start call: ' + e.message);
      }
    },
    vcHangup() {
      if (!window.PanelVideoCall) return;
      this.vcWBTeardown();
      this._vcTeardownMirrorObserver();
      this._vcTeardownPopoverBounds();
      window.PanelVideoCall.disconnect();
      this.videocall.inCall = false;
      this.videocall.activeRoomId = '';
    },
    vcSendChat() {
      const t = (this.videocall.chatComposer || '').trim();
      if (!t || !window.PanelVideoCall) return;
      window.PanelVideoCall.sendChat(t);
      this.videocall.chat.push({ from: 'me', text: t, ts: Date.now() });
      this.videocall.chatComposer = '';
    },
    vcToggleMute() {
      if (!window.PanelVideoCall) return;
      this.videocall.muted = window.PanelVideoCall.setMuted(!this.videocall.muted);
    },
    vcToggleVideo() {
      if (!window.PanelVideoCall) return;
      this.videocall.videoOff = window.PanelVideoCall.setVideoOff(!this.videocall.videoOff);
    },
    vcSetBudget(kbps) {
      this.videocall.budgetKbps = kbps;
      localStorage.setItem('panel_vc_budget_kbps', String(kbps));
      if (window.PanelVideoCall) window.PanelVideoCall.setBudgetKbps(kbps);
    },
    vcSetCodec(codec) {
      this.videocall.codec = codec;
      localStorage.setItem('panel_vc_codec', codec);
    },
    vcError(msg) {
      this.videocall.errors.unshift({ ts: Date.now(), msg, kind: 'err' });
      if (this.videocall.errors.length > 20) this.videocall.errors = this.videocall.errors.slice(0, 20);
    },
    vcInfo(msg) {
      this.videocall.errors.unshift({ ts: Date.now(), msg, kind: 'ok' });
      if (this.videocall.errors.length > 20) this.videocall.errors = this.videocall.errors.slice(0, 20);
    },
    vcToggleCloudRec() {
      this.videocall.recordToCloud = !this.videocall.recordToCloud;
      localStorage.setItem('panel_vc_cloud_rec', this.videocall.recordToCloud ? '1' : '0');
    },
    async vcUploadRecording(blob, durationS) {
      try {
        const fd = new FormData();
        fd.append('room_id', this.videocall.activeRoomId || (this.videocall.selectedRoom && this.videocall.selectedRoom.id) || '');
        fd.append('started_at', String(Math.floor((Date.now() - durationS*1000) / 1000)));
        fd.append('duration_s', String(durationS));
        fd.append('file', blob, 'recording.webm');
        const r = await fetch('/api/videocall/recordings', {
          method: 'POST',
          headers: { Authorization: 'Bearer ' + this.token },
          body: fd,
        });
        if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
        const rec = await r.json();
        await this.vcLoadRecordings();
        return rec;
      } catch (e) { this.vcError('recording upload: ' + e.message); return null; }
    },
    async vcLoadRecordings() {
      try {
        const r = await fetch('/api/videocall/recordings', { headers: { Authorization: 'Bearer ' + this.token } });
        if (r.ok) this.videocall.recordings = await r.json();
      } catch(_) {}
    },
    vcRecordingURL(id, what) {
      return '/api/videocall/recordings/' + encodeURIComponent(id) + (what ? '/' + what : '') + '?token=' + encodeURIComponent(this.token);
    },
    vcDeleteRecording(id) {
      this.vcAskConfirm({
        title: 'Delete recording?',
        desc: 'The .webm file is removed from the server. This action is irreversible.',
        confirmLabel: '🗑 Delete',
        danger: true,
        onYes: () => this._vcDeleteRecordingNow(id),
      });
    },
    async _vcDeleteRecordingNow(id) {
      try {
        const r = await fetch('/api/videocall/recordings/' + encodeURIComponent(id), {
          method: 'DELETE', headers: { Authorization: 'Bearer ' + this.token },
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        await this.vcLoadRecordings();
      } catch (e) { this.vcError('delete: ' + e.message); }
    },
    async vcOpenSummary(id) {
      this.videocall.summaryModal = { open: true, recId: id, busy: true, summary: '' };
      try {
        const cacheResp = await fetch('/api/videocall/recordings/' + encodeURIComponent(id) + '/summary', { headers: { Authorization: 'Bearer ' + this.token } });
        if (cacheResp.ok) {
          this.videocall.summaryModal.summary = await cacheResp.text();
          this.videocall.summaryModal.busy = false;
          return;
        }
        const transcript = this.videocall.transcript || '';
        if (!transcript.trim()) {
          this.videocall.summaryModal.summary = '⚠ There is no transcript for this call. Turn on "Live transcription" during the call to generate the AI summary afterwards.';
          this.videocall.summaryModal.busy = false;
          return;
        }
        const r = await fetch('/api/videocall/recordings/' + encodeURIComponent(id) + '/summarize', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ transcript }),
        });
        if (!r.ok) {
          if (r.status === 503) {
            this.videocall.summaryModal.summary = '⚠ The AI summary is not configured on this server.\n\nTo enable it, the admin has to set ANTHROPIC_API_KEY in systemd and restart the server-control-panel.';
            return;
          }
          if (r.status === 502) {
            this.videocall.summaryModal.summary = '⚠ The Claude API did not answer (502). Try again in a few seconds.';
            return;
          }
          throw new Error('HTTP ' + r.status + ': ' + await r.text());
        }
        const data = await r.json();
        this.videocall.summaryModal.summary = data.summary || '';
      } catch (e) {
        this.videocall.summaryModal.summary = '❌ ' + e.message;
      } finally {
        this.videocall.summaryModal.busy = false;
      }
    },
    async whatsappCallContact(jid) {
      if (!jid) return;
      if (this.videocall.callingContact) return;
      const chat = this.whatsapp.chatById && this.whatsapp.chatById[jid];
      const name = (chat && (chat.name || chat.push_name || '')) || jid.replace(/@.*/, '');
      const roomName = 'Call with ' + name;
      this.videocall.callingContact = true;
      try {
        await this.vcLoadRooms();
        let room = (this.videocall.rooms || []).find(r => r.owner === this.username && r.name === roomName);
        if (!room) {
          const cr = await fetch('/api/videocall/rooms', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
            body: JSON.stringify({ name: roomName }),
          });
          if (!cr.ok) throw new Error('create room: HTTP ' + cr.status);
          await this.vcLoadRooms();
          room = (this.videocall.rooms || []).find(r => r.owner === this.username && r.name === roomName);
          if (!room) throw new Error('room created but not found');
        }
        const r = await fetch('/api/videocall/invite/whatsapp', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({
            room_id: room.id,
            ttl_min: 60,
            to_jid: jid,
            message: '📹 I am inviting you to a video call right now — click the link:',
          }),
        });
        if (!r.ok) {
          const errTxt = await r.text();
          throw new Error('WhatsApp: HTTP ' + r.status + (errTxt ? ' — ' + errTxt : ''));
        }
        this.vcInfo('Invite sent to ' + name + ' via WhatsApp');
        this.videocall.selectedRoom = room;
        const wasSkip = this.videocall.lobbySkipNext;
        this.videocall.lobbySkipNext = true;
        this.setPage('videocall', { silent: true });
        await this.$nextTick();
        await this.vcJoinCall(room.id);
        this.videocall.lobbySkipNext = wasSkip;
      } catch (e) {
        this.vcError('call contact: ' + e.message);
      } finally {
        this.videocall.callingContact = false;
      }
    },

    async vcOpenWaInvitePicker() {
      this.vcClosePopovers();
      this.videocall.waInviteOpen = true;
      this.videocall.waInviteSearch = '';
      if (!this.whatsapp.chats || this.whatsapp.chats.length === 0) {
        try { await this.whatsappReloadChats(); } catch (_) {}
      }
    },

    async vcSendInviteWhatsApp(toJid, message) {
      const room = this.videocall.selectedRoom || { id: this.videocall.activeRoomId };
      if (!room || !room.id) return;
      try {
        const r = await fetch('/api/videocall/invite/whatsapp', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: room.id, ttl_min: this.videocall.inviteTTLMin || 60, to_jid: toJid, message: message || '' }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status + ': ' + await r.text());
        return await r.json();
      } catch (e) { this.vcError('whatsapp invite: ' + e.message); return null; }
    },

    vcFmtBps(b) {
      if (!b || b < 1) return '0 B/s';
      const u = ['B/s', 'KB/s', 'MB/s', 'GB/s']; let i = 0;
      while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
      return b.toFixed(1) + ' ' + u[i];
    },
    vcFmtBytes(b) {
      if (!b || b < 1) return '0 B';
      const u = ['B', 'KB', 'MB', 'GB']; let i = 0;
      while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
      return b.toFixed(1) + ' ' + u[i];
    },

    async vcToggleScreenShare() {
      if (!window.PanelVideoCall) return;
      this.videocall.screenSharing = !!(await window.PanelVideoCall.setScreenShare(!this.videocall.screenSharing));
    },
    vcToggleRecording() {
      if (!window.PanelVideoCall) return;
      if (this.videocall.recording) window.PanelVideoCall.stopRecording();
      else window.PanelVideoCall.startRecording();
    },
    vcToggleAudioFirst() {
      this.videocall.audioFirstMode = !this.videocall.audioFirstMode;
      localStorage.setItem('panel_vc_audio_first', this.videocall.audioFirstMode ? '1' : '0');
      if (window.PanelVideoCall) window.PanelVideoCall.setAudioFirstMode(this.videocall.audioFirstMode);
    },
    vcToggleE2EEWanted() {
      this.videocall.e2eeWanted = !this.videocall.e2eeWanted;
      localStorage.setItem('panel_vc_e2ee_wanted', this.videocall.e2eeWanted ? '1' : '0');
    },

    async vcGenerateInvite(roomId, ttlMin) {
      try {
        const r = await fetch('/api/videocall/invite', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: roomId, ttl_min: ttlMin || 60 }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        this.videocall.inviteURL = data.url;
        this.videocall.inviteExpiresAt = data.expires_at;
        this.videocall.inviteOpen = true;
        this.videocall.inviteCopyDone = false;
      } catch (e) { this.vcError('generate invite: ' + e.message); }
    },
    async vcCopyInvite() {
      try {
        await navigator.clipboard.writeText(this.videocall.inviteURL);
        this.videocall.inviteCopyDone = true;
        setTimeout(() => { this.videocall.inviteCopyDone = false; }, 2500);
      } catch (e) { this.vcError('clipboard: ' + e.message); }
    },
    async vcGeneratePIN(roomId) {
      try {
        const r = await fetch('/api/videocall/pin', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: roomId, ttl_hours: this.videocall.pinTTLHours || 0 }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        const data = await r.json();
        if (this.videocall.selectedRoom && this.videocall.selectedRoom.id === roomId) {
          this.videocall.selectedRoom = { ...this.videocall.selectedRoom, pin: data.pin, pin_expires_at: data.expires_at };
        }
        await this.vcLoadRooms();
        const fresh = this.videocall.rooms.find(r => r.id === roomId);
        if (fresh) this.videocall.selectedRoom = fresh;
      } catch (e) { this.vcError('generate PIN: ' + e.message); }
    },
    vcRevokePIN(roomId) {
      this.vcAskConfirm({
        title: 'Revoke PIN?',
        desc: 'Anyone who already has this code will no longer be able to join. The links you shared become invalid immediately. Are you sure?',
        confirmLabel: '🔢 Revoke PIN',
        danger: true,
        onYes: () => this._vcRevokePINNow(roomId),
      });
    },
    async _vcRevokePINNow(roomId) {
      try {
        const r = await fetch('/api/videocall/pin', {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: roomId }),
        });
        if (!r.ok) throw new Error('HTTP ' + r.status);
        if (this.videocall.selectedRoom && this.videocall.selectedRoom.id === roomId) {
          this.videocall.selectedRoom = { ...this.videocall.selectedRoom, pin: '', pin_expires_at: 0 };
        }
        await this.vcLoadRooms();
        const fresh = this.videocall.rooms.find(r => r.id === roomId);
        if (fresh) this.videocall.selectedRoom = fresh;
      } catch (e) { this.vcError('revoke PIN: ' + e.message); }
    },
    _vcPinShareText() {
      const pin = (this.videocall.selectedRoom && this.videocall.selectedRoom.pin) || '';
      const name = (this.videocall.selectedRoom && this.videocall.selectedRoom.name) || 'the call';
      const url = location.origin + '/join?pin=' + encodeURIComponent(pin);
      return 'Join ' + name + ' using the PIN ' + pin + ' here: ' + url;
    },
    async vcSharePinLink(via) {
      const pin = (this.videocall.selectedRoom && this.videocall.selectedRoom.pin) || '';
      if (!pin) return;
      const url = location.origin + '/join?pin=' + encodeURIComponent(pin);
      const text = this._vcPinShareText();
      switch (via) {
        case 'clipboard':
          try { await navigator.clipboard.writeText(url); this.vcInfo('Link copied!'); }
          catch (e) { this.vcError('clipboard: ' + e.message); }
          break;
        case 'whatsapp':
          window.open('https://wa.me/?text=' + encodeURIComponent(text), '_blank', 'noopener');
          break;
        case 'email':
          window.location.href = 'mailto:?subject=' + encodeURIComponent('Join the call') + '&body=' + encodeURIComponent(text);
          break;
        case 'sms':
          window.location.href = 'sms:?body=' + encodeURIComponent(text);
          break;
      }
    },

    async vcConsumeInviteFromHash() {
      const m = (location.hash || '').match(/[#&]token=([^&]+)/);
      if (!m) return;
      const token = decodeURIComponent(m[1]);
      try {
        const r = await fetch('/api/videocall/invite/consume', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ token }),
        });
        if (!r.ok) {
          const t = await r.text();
          throw new Error(t || ('HTTP ' + r.status));
        }
        const data = await r.json();
        this.setPage('videocall');
        await this.vcLoadRooms();
        const fresh = this.videocall.rooms.find(rm => rm.id === (data.room && data.room.id));
        if (fresh) this.videocall.selectedRoom = fresh;
        try { history.replaceState(null, '', '#videocall'); } catch (_) {}
      } catch (e) { this.vcError('consume invite: ' + e.message); }
    },

    vcInstallShortcuts() {
      if (this._vcShortcutsInstalled) return;
      this._vcShortcutsInstalled = true;
      this._vcKeydownHandler = (e) => {
        if (!this.videocall.inCall || !this.videocall.shortcutsEnabled) return;
        const t = e.target;
        if (!t) return;
        if (e.code === 'Space' && !e.repeat) {
          const isChatInput = t.tagName === 'INPUT' && (t.placeholder === 'Message…' || t.placeholder === 'Message...');
          if (!isChatInput && this.videocall.muted) {
            this.videocall.pttActive = true;
            for (const tr of (window.PanelVideoCall._call?.localStream?.getAudioTracks() || [])) tr.enabled = true;
            e.preventDefault();
            return;
          }
        }
        if (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable) return;
        if (e.code === 'Space' && !e.repeat) {
          if (this.videocall.muted) {
            this.videocall.pttActive = true;
            for (const tr of (window.PanelVideoCall._call?.localStream?.getAudioTracks() || [])) tr.enabled = true;
          }
          e.preventDefault();
          return;
        }
        if (e.ctrlKey && e.shiftKey && (e.key === '!' || e.code === 'Digit1')) {
          this.videocall.shortcutsOverlay = !this.videocall.shortcutsOverlay;
          e.preventDefault();
          return;
        }
        const k = e.key.toLowerCase();
        switch (k) {
          case 'm': this.vcToggleMute(); e.preventDefault(); break;
          case 'v': this.vcToggleVideo(); e.preventDefault(); break;
          case 'c': this.vcToggleSidePanel('chat'); e.preventDefault(); break;
          case 'p': this.vcToggleSidePanel('participants'); e.preventDefault(); break;
          case 'f': this.vcToggleFullscreen(); e.preventDefault(); break;
          case 's': this.vcToggleScreenShare(); e.preventDefault(); break;
          case 'escape':
            if (this.videocall.shortcutsOverlay) { this.videocall.shortcutsOverlay = false; e.preventDefault(); }
            else if (this.videocall.settingsPopOpen || this.videocall.audioPopOpen || this.videocall.videoPopOpen) { this.vcClosePopovers(); e.preventDefault(); }
            else if (this.videocall.reactionsPickerOpen) { this.videocall.reactionsPickerOpen = false; e.preventDefault(); }
            break;
        }
      };
      this._vcKeyupHandler = (e) => {
        if (e.code === 'Space' && this.videocall.pttActive) {
          this.videocall.pttActive = false;
          if (this.videocall.muted) {
            for (const tr of (window.PanelVideoCall._call?.localStream?.getAudioTracks() || [])) tr.enabled = false;
          }
        }
      };
      window.addEventListener('keydown', this._vcKeydownHandler);
      window.addEventListener('keyup', this._vcKeyupHandler);
    },
    vcUninstallShortcuts() {
      if (!this._vcShortcutsInstalled) return;
      try { if (this._vcKeydownHandler) window.removeEventListener('keydown', this._vcKeydownHandler); } catch(_) {}
      try { if (this._vcKeyupHandler) window.removeEventListener('keyup', this._vcKeyupHandler); } catch(_) {}
      this._vcKeydownHandler = null;
      this._vcKeyupHandler = null;
      this._vcShortcutsInstalled = false;
    },
    vcToggleShortcuts() {
      this.videocall.shortcutsEnabled = !this.videocall.shortcutsEnabled;
      localStorage.setItem('panel_vc_shortcuts', this.videocall.shortcutsEnabled ? '1' : '0');
    },

    vcConnectionQuality() {
      const s = this.videocall.stats || {};
      const rtt = s.rtt || 0;
      const loss = s.packetsLost || 0;
      let bars;
      if (rtt > 500 || loss > 50) bars = 1;
      else if (rtt > 250 || loss > 20) bars = 2;
      else if (rtt > 100 || loss > 5) bars = 3;
      else bars = 4;
      if (s.connectionType === 'relay') bars = Math.min(bars, 3);
      return bars;
    },
    vcConnectionColor() {
      const b = this.vcConnectionQuality();
      return b >= 4 ? '#10b981' : b === 3 ? '#3b82f6' : b === 2 ? '#f59e0b' : '#ef4444';
    },
    vcConnectionLabel() {
      const b = this.vcConnectionQuality();
      return b >= 4 ? 'excellent' : b === 3 ? 'good' : b === 2 ? 'unstable' : 'bad';
    },

    vcToggleReactionsPicker() {
      this.videocall.reactionsPickerOpen = !this.videocall.reactionsPickerOpen;
      this.vcClosePopovers();
    },
    vcSendReaction(emoji) {
      this.videocall.reactionsPickerOpen = false;
      this.vcShowReaction({ emoji, from: 'me', ts: Date.now() });
      if (window.PanelVideoCall) {
        window.PanelVideoCall.sendChat('✨REACT✨' + emoji);
      }
    },
    vcShowReaction(r) {
      const id = (r.id || ('r' + Date.now() + Math.random().toString(36).slice(2,6)));
      this.videocall.reactions.push({ id, emoji: r.emoji, from: r.from, ts: r.ts || Date.now() });
      if (this.videocall.reactions.length > 50) this.videocall.reactions = this.videocall.reactions.slice(-50);
      setTimeout(() => {
        this.videocall.reactions = this.videocall.reactions.filter(x => x.id !== id);
      }, 3500);
    },

    vcHandlePeerState(from, state) {
      const cur = this.videocall.peerStates[from] || {};
      const updated = { ...cur, ...state };
      this.videocall.peerStates = { ...this.videocall.peerStates, [from]: updated };
    },

    vcChatUnread() {
      return Math.max(0, this.videocall.chat.length - this.videocall.chatLastRead);
    },
    vcMarkChatRead() {
      this.videocall.chatLastRead = this.videocall.chat.length;
    },

    vcSnapshotRemote() {
      const peers = document.querySelectorAll('#vc-videos video[data-vc-peer]');
      if (!peers.length) { this.vcError('no remote video to capture'); return; }
      const v = peers[0];
      if (!v.videoWidth) { this.vcError('the remote video has not loaded yet'); return; }
      const canvas = document.createElement('canvas');
      canvas.width = v.videoWidth; canvas.height = v.videoHeight;
      canvas.getContext('2d').drawImage(v, 0, 0, canvas.width, canvas.height);
      canvas.toBlob((blob) => {
        if (!blob) return;
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
        a.href = url; a.download = 'snapshot-' + stamp + '.png';
        document.body.appendChild(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 60000);
      }, 'image/png');
    },

    vcDropOverlayShow() {
      this.videocall.dropOverlay = true;
      clearTimeout(this._vcDropWatchdog);
      this._vcDropWatchdog = setTimeout(() => { this.videocall.dropOverlay = false; }, 700);
    },
    vcDropOverlayHide() {
      clearTimeout(this._vcDropWatchdog);
      this._vcDropWatchdog = null;
      this.videocall.dropOverlay = false;
    },
    vcInstallDropHandlers() {
      if (this._vcDropInstalled) return;
      if (window.matchMedia && window.matchMedia('(pointer: coarse)').matches) return;
      this._vcDropInstalled = true;
      const root = document.body;
      ['dragenter', 'dragover'].forEach(ev => root.addEventListener(ev, (e) => {
        if (!this.videocall.inCall) return;
        if (!e.dataTransfer || !Array.from(e.dataTransfer.types || []).includes('Files')) return;
        e.preventDefault(); e.stopPropagation();
        this.vcDropOverlayShow();
      }));
      root.addEventListener('dragleave', (e) => {
        if (!this.videocall.dropOverlay) return;
        const leftWindow = !e.relatedTarget ||
          e.clientX <= 0 || e.clientY <= 0 ||
          e.clientX >= (window.innerWidth || 0) || e.clientY >= (window.innerHeight || 0);
        if (leftWindow) this.vcDropOverlayHide();
      });
      root.addEventListener('dragend', () => this.vcDropOverlayHide());
      window.addEventListener('blur', () => this.vcDropOverlayHide());
      root.addEventListener('drop', async (e) => {
        if (!this.videocall.inCall) return;
        e.preventDefault(); e.stopPropagation();
        this.vcDropOverlayHide();
        const files = Array.from(e.dataTransfer?.files || []);
        for (const f of files) {
          if (f.size > 500*1024*1024) { this.vcError(f.name + ': larger than 500MB'); continue; }
          try { await window.PanelVideoCall.sendFile(f); } catch (err) { this.vcError('send: ' + err.message); }
        }
      });
      window.addEventListener('paste', async (e) => {
        if (!this.videocall.inCall) return;
        const items = e.clipboardData && e.clipboardData.items;
        if (!items) return;
        for (const item of items) {
          if (item.kind === 'file' && item.type.startsWith('image/')) {
            const f = item.getAsFile();
            if (f) {
              try { await window.PanelVideoCall.sendFile(f); } catch (err) { this.vcError('send paste: ' + err.message); }
            }
          }
        }
      });
    },

    vcAskConfirm(opts) {
      this.vcClosePopovers && this.vcClosePopovers();
      this.vcConfirm = {
        open: true,
        title: opts.title || 'Confirm',
        desc: opts.desc || '',
        confirmLabel: opts.confirmLabel || 'Confirm',
        danger: !!opts.danger,
        onYes: opts.onYes || (() => {}),
      };
    },
    vcConfirmYes() {
      const cb = this.vcConfirm.onYes;
      this.vcConfirm.open = false;
      if (typeof cb === 'function') cb();
    },
    vcConfirmNo() { this.vcConfirm.open = false; },

    vcHangupSafe() {
      const dur = this.videocall.activeRoomId && window.PanelVideoCall && window.PanelVideoCall._call
        ? Math.round((Date.now() - (window.PanelVideoCall._call.callStartedAt || Date.now())) / 1000)
        : 0;
      if (dur > 300) {
        this.vcAskConfirm({
          title: 'Hang up now?',
          desc: 'The call has been going for ' + Math.floor(dur/60) + ' min. To avoid leaving by accident, confirm before hanging up.',
          confirmLabel: '📞 Hang up',
          danger: true,
          onYes: () => this.vcHangup(),
        });
        return;
      }
      this.vcHangup();
    },

    async vcTogglePip() {
      try {
        if (document.pictureInPictureElement) {
          await document.exitPictureInPicture();
          this.videocall.pipActive = false;
        } else {
          const remote = document.querySelector('#vc-videos video[data-vc-peer]');
          const target = remote || document.querySelector('#vc-videos video[data-vc-local="1"]');
          if (!target) { this.vcError('no video for PiP'); return; }
          if (typeof target.requestPictureInPicture !== 'function') { this.vcError('PiP is not supported in this browser'); return; }
          await target.requestPictureInPicture();
          this.videocall.pipActive = true;
          target.addEventListener('leavepictureinpicture', () => { this.videocall.pipActive = false; }, { once: true });
        }
      } catch (e) { this.vcError('PiP: ' + e.message); }
    },

    _vcSheetMobile() {
      try { return window.matchMedia('(max-width: 767.98px)').matches; } catch (_) { return false; }
    },
    vcToggleGroup(k) {
      if (!this.videocall.settingsGroups || !(k in this.videocall.settingsGroups)) return;
      const opening = !this.videocall.settingsGroups[k];
      if (opening && this._vcSheetMobile()) {
        for (const g in this.videocall.settingsGroups) if (g !== k) this.videocall.settingsGroups[g] = false;
      }
      this.videocall.settingsGroups[k] = opening;
      try { localStorage.setItem('panel_vc_settings_groups', JSON.stringify(this.videocall.settingsGroups)); } catch (e) {}
    },

    vcSetLocalPipSize(size) {
      if (!['sm','md','lg','hidden'].includes(size)) return;
      this.videocall.localPipSize = size;
      localStorage.setItem('panel_vc_local_size', size);
    },
    vcSetLocalPipPos(pos) {
      if (!['br','bl','tr','tl'].includes(pos)) return;
      this.videocall.localPipPos = pos;
      localStorage.setItem('panel_vc_local_pos', pos);
    },
    vcToggleLocalMirror() {
      this.videocall.localMirror = !this.videocall.localMirror;
      localStorage.setItem('panel_vc_local_mirror', this.videocall.localMirror ? '1' : '0');
      try {
        const videos = document.querySelectorAll('video[data-vc-local="1"]');
        videos.forEach(v => {
          v.style.transform = this.videocall.localMirror ? 'scaleX(-1)' : '';
        });
      } catch (_) {}
    },
    vcApplyLocalMirror() {
      try {
        const videos = document.querySelectorAll('video[data-vc-local="1"]');
        videos.forEach(v => {
          v.style.transform = this.videocall.localMirror ? 'scaleX(-1)' : '';
        });
      } catch (_) {}
    },
    _vcSetupMirrorObserver() {
      try {
        const el = document.getElementById('vc-videos');
        if (!el) { setTimeout(() => this._vcSetupMirrorObserver(), 300); return; }
        if (this._vcMirrorObs) { try { this._vcMirrorObs.disconnect(); } catch (_) {} }
        this._vcMirrorObs = new MutationObserver(() => this.vcApplyLocalMirror());
        this._vcMirrorObs.observe(el, { childList: true });
        this.vcApplyLocalMirror();
        setTimeout(() => this.vcApplyLocalMirror(), 600);
      } catch (_) {}
    },
    _vcTeardownMirrorObserver() {
      if (this._vcMirrorObs) { try { this._vcMirrorObs.disconnect(); } catch (_) {} this._vcMirrorObs = null; }
    },
    _vcSetupPopoverBounds() {
      try {
        const el = document.getElementById('vc-call-root');
        if (!el) { setTimeout(() => this._vcSetupPopoverBounds(), 300); return; }
        if (this._vcRootRO) { try { this._vcRootRO.disconnect(); } catch (_) {} }
        const apply = () => {
          const h = el.clientHeight, w = el.clientWidth;
          if (h > 0) el.style.setProperty('--vc-root-h', h + 'px');
          if (w > 0) el.style.setProperty('--vc-root-w', w + 'px');
        };
        if (typeof ResizeObserver === 'function') {
          this._vcRootRO = new ResizeObserver(apply);
          this._vcRootRO.observe(el);
        }
        apply();
      } catch (_) {}
    },
    _vcTeardownPopoverBounds() {
      if (this._vcRootRO) { try { this._vcRootRO.disconnect(); } catch (_) {} this._vcRootRO = null; }
    },
    vcSetRemoteFit(fit) {
      if (!['cover','contain'].includes(fit)) return;
      this.videocall.remoteFit = fit;
      localStorage.setItem('panel_vc_remote_fit', fit);
    },
    vcSpotlightPeer(peerId) {
      const videosEl = document.getElementById('vc-videos');
      if (!videosEl) return;
      videosEl.querySelectorAll('.vc-spotlighted').forEach(el => el.classList.remove('vc-spotlighted'));
      if (this.videocall.spotlight === peerId) {
        this.videocall.spotlight = '';
        return;
      }
      this.videocall.spotlight = peerId;
      if (peerId && peerId !== 'me') {
        const tile = videosEl.querySelector('[data-vc-peer-tile="' + peerId + '"]');
        if (tile) tile.classList.add('vc-spotlighted');
      }
    },
    vcInstallSpotlightClicks() {
      if (this._vcSpotlightInstalled) return;
      this._vcSpotlightInstalled = true;
      document.addEventListener('click', (ev) => {
        if (!this.videocall.inCall) return;
        const tile = ev.target.closest && ev.target.closest('[data-vc-peer-tile]');
        if (!tile) return;
        if (ev.target !== tile && !ev.target.matches('video')) return;
        const peerId = tile.getAttribute('data-vc-peer-tile');
        if (peerId) {
          ev.stopPropagation();
          this.vcSpotlightPeer(peerId);
        }
      });
    },

    async vcSetQuality(mode) {
      this.videocall.qualityMode = mode;
      localStorage.setItem('panel_vc_quality', mode);
      if (mode !== 'custom' && window.PanelVideoCall.getQualityPresets) {
        const presets = window.PanelVideoCall.getQualityPresets();
        const p = presets[mode];
        if (p) {
          this.videocall.budgetKbps = p.videoOff ? p.audioKbps : p.videoKbps;
          localStorage.setItem('panel_vc_budget_kbps', String(this.videocall.budgetKbps));
        }
      }
      if (this.videocall.inCall && mode !== 'custom' && window.PanelVideoCall.applyQualityProfile) {
        await window.PanelVideoCall.applyQualityProfile(mode);
      }
    },
    vcKickPeer(peerId, peerLabel) {
      if (!peerId) return;
      const roomId = this.videocall.activeRoomId || (this.videocall.selectedRoom && this.videocall.selectedRoom.id);
      if (!roomId) { this.vcError('room not found'); return; }
      this.vcAskConfirm({
        title: 'Kick ' + (peerLabel || 'participant') + '?',
        desc: 'The person is disconnected immediately. They can try to rejoin if they still have access (link/PIN). To block them for good, revoke the PIN or remove them from the members.',
        confirmLabel: '🚪 Kick',
        danger: true,
        onYes: () => this._vcKickPeerNow(roomId, peerId, peerLabel),
      });
    },
    async _vcKickPeerNow(roomId, peerId, peerLabel) {
      try {
        const r = await fetch('/api/videocall/kick', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
          body: JSON.stringify({ room_id: roomId, peer_id: peerId }),
        });
        if (r.status === 403) throw new Error('only the room owner can kick');
        if (r.status === 404) throw new Error('the peer is no longer in the call');
        if (!r.ok) throw new Error('HTTP ' + r.status);
        this.vcInfo((peerLabel || 'Participant') + ' kicked');
      } catch (e) { this.vcError('kick: ' + e.message); }
    },

    async vcSetQualityForAll(mode) {
      if (!this.videocall.selectedRoom || this.videocall.selectedRoom.owner !== this.username) {
        this.vcError('only the room owner can force a mode for everyone');
        return;
      }
      await this.vcSetQuality(mode);
      let sent = false;
      if (window.PanelVideoCall && window.PanelVideoCall.sendState) {
        sent = window.PanelVideoCall.sendState({ quality_force: mode, by: this.username });
      }
      if (sent) {
        this.vcInfo('Mode "' + mode + '" applied to everyone in the call');
      } else {
        this.vcError('could not send to the other side (no connection)');
      }
    },

    vcToggleSidePanel(name) {
      this.videocall.sidePanel = (this.videocall.sidePanel === name) ? '' : name;
      this.videocall.settingsPopOpen = false;
      this.videocall.audioPopOpen = false;
      this.videocall.videoPopOpen = false;
      this.videocall.reactionsPickerOpen = false;
      if (this.videocall.sidePanel === 'chat') this.vcMarkChatRead();
    },
    vcToggleSettingsPop() {
      this.videocall.settingsPopOpen = !this.videocall.settingsPopOpen;
      this.videocall.audioPopOpen = false;
      this.videocall.videoPopOpen = false;
      if (this.videocall.settingsPopOpen && this._vcSheetMobile() && this.videocall.settingsGroups) {
        const openOnes = Object.keys(this.videocall.settingsGroups).filter((g) => this.videocall.settingsGroups[g]);
        if (openOnes.length > 1) {
          for (const g of openOnes.slice(1)) this.videocall.settingsGroups[g] = false;
          try { localStorage.setItem('panel_vc_settings_groups', JSON.stringify(this.videocall.settingsGroups)); } catch (e) {}
        }
      }
    },
    vcToggleAudioPop() {
      this.videocall.audioPopOpen = !this.videocall.audioPopOpen;
      this.videocall.videoPopOpen = false;
      this.videocall.settingsPopOpen = false;
      if (this.videocall.audioPopOpen) { this.vcRefreshDevices(); this._vcMicMeterEnsure(); }
    },
    vcToggleVideoPop() {
      this.videocall.videoPopOpen = !this.videocall.videoPopOpen;
      this.videocall.audioPopOpen = false;
      this.videocall.settingsPopOpen = false;
      if (this.videocall.videoPopOpen) this.vcRefreshDevices();
    },
    vcClosePopovers() {
      this.videocall.settingsPopOpen = false;
      this.videocall.audioPopOpen = false;
      this.videocall.videoPopOpen = false;
    },
    async vcInCallChangeCamera(id) {
      this.videocall.selectedDevices.camera = id;
      localStorage.setItem('panel_vc_camera_id', id);
      await window.PanelVideoCall.setCameraDevice(id);
      this.videocall.videoPopOpen = false;
    },
    async vcInCallChangeMic(id) {
      this.videocall.selectedDevices.mic = id;
      localStorage.setItem('panel_vc_mic_id', id);
      await window.PanelVideoCall.setMicDevice(id);
      this.videocall.audioPopOpen = false;
    },
    async vcInCallChangeSpeaker(id) {
      this.videocall.selectedDevices.speaker = id;
      localStorage.setItem('panel_vc_speaker_id', id);
      await window.PanelVideoCall.setSpeakerDevice(id);
    },
    vcRaiseHand() {
      this.videocall.handRaised = !this.videocall.handRaised;
      try {
        if (window.PanelVideoCall && this.videocall.inCall) {
          const ws = window.PanelVideoCall;
        }
      } catch (_) {}
    },
    async vcToggleSubtitles() {
      if (!window.PanelVideoCall || this._subtitlesToggling) return;
      this._subtitlesToggling = true;
      try {
        if (!this.videocall.subtitlesActive) {
          const avail = (window.PanelSTT && window.PanelSTT.availableBackends) ? window.PanelSTT.availableBackends() : [];
          const whisperUsable = avail.includes('whisper-local') && await window.PanelSTT.probeWhisperLocal();
          if (!avail.includes('web-speech') && !whisperUsable) {
            this.vcInfo('🎙 No transcription engine available right now');
            return;
          }
          try {
            if (navigator.permissions && navigator.permissions.query) {
              const st = await navigator.permissions.query({ name: 'microphone' });
              if (st && st.state === 'denied') {
                this.vcInfo('🎙 Microphone blocked — allow it in the browser permissions and try again');
                return;
              }
            }
          } catch (_) {}
        }
        this.videocall.subtitlesActive = !!window.PanelVideoCall.setSubtitles(
          !this.videocall.subtitlesActive,
          {
            lang: this.videocall.subtitlesLang,
            token: this.token,
            backend: this.videocall.sttBackend || 'web-speech',
          }
        );
        if (this.videocall.subtitlesActive && window.PanelVideoCall.setShowCaptions) {
          try { window.PanelVideoCall.setShowCaptions(this.videocall.subtitlesShow); } catch (_) {}
        }
      } finally {
        this._subtitlesToggling = false;
      }
    },
    vcSetSttBackend(backend) {
      if (backend !== 'web-speech' && backend !== 'whisper-local') return;
      if (backend === 'whisper-local' && !this.videocall.whisperLocalAvailable) {
        this.vcInfo('Local Whisper unavailable (server offline)');
        return;
      }
      this.videocall.sttBackend = backend;
      try {
        localStorage.setItem('panel_vc_stt_backend', backend);
        localStorage.setItem('panel_vc_stt_backend_explicit', '1');
      } catch (_) {}
      if (this.videocall.subtitlesActive && window.PanelVideoCall) {
        window.PanelVideoCall.setSubtitles(false, { _silentPropagate: true });
        setTimeout(() => {
          this.videocall.subtitlesActive = !!window.PanelVideoCall.setSubtitles(true, {
            lang: this.videocall.subtitlesLang,
            token: this.token,
            backend,
          });
        }, 200);
      }
    },
    vcToggleCaptions() {
      const next = !this.videocall.subtitlesShow;
      this.videocall.subtitlesShow = next;
      try { localStorage.setItem('panel_vc_subtitles_show', next ? '1' : '0'); } catch (_) {}
      if (window.PanelVideoCall && window.PanelVideoCall.setShowCaptions) {
        try { window.PanelVideoCall.setShowCaptions(next); } catch (_) {}
      }
    },
    vcSetMicGain(v) {
      const g = Math.min(4, Math.max(0.25, Number(v) || 1.0));
      this.videocall.micGain = g;
      try { localStorage.setItem('panel_vc_mic_gain', String(g)); } catch (_) {}
      if (window.PanelVideoCall && typeof window.PanelVideoCall.setMicGain === 'function') {
        try { window.PanelVideoCall.setMicGain(g); } catch (_) {}
      }
    },
    vcMicGainPct() {
      return Math.round((this.videocall.micGain || 1) * 100) + '%';
    },
    vcMicGainPos() {
      return Math.log2(this.videocall.micGain || 1);
    },
    vcSetMicGainPos(pos) {
      const p = Math.abs(pos) < 0.08 ? 0 : pos;
      this.vcSetMicGain(Math.round(Math.pow(2, p) * 100) / 100);
    },
    async vcSetMicProc(key, on) {
      if (!['noiseSuppression', 'echoCancellation', 'autoGainControl'].includes(key)) return;
      if (key === 'echoCancellation' && !on) {
        this.vcInfo('Echo cancellation off: without headphones, the other side will hear themselves back.');
      }
      await this._vcApplyMicProc(Object.assign({}, this.videocall.micProc, { [key]: !!on }));
    },
    async vcResetMicAudio() {
      this.vcSetMicGain(1.0);
      const p = this.videocall.micProc || {};
      if (p.noiseSuppression && p.echoCancellation && p.autoGainControl) return;
      await this._vcApplyMicProc({ noiseSuppression: true, echoCancellation: true, autoGainControl: true });
    },
    async _vcApplyMicProc(next) {
      if (this.videocall.micProcBusy) return;
      this.videocall.micProc = next;
      try { localStorage.setItem('panel_vc_mic_proc', JSON.stringify(next)); } catch (_) {}
      const api = window.PanelVideoCall;
      if (this.videocall.inCall && api && typeof api.setMicProcessing === 'function') {
        this.videocall.micProcBusy = true;
        try {
          const applied = await api.setMicProcessing(next);
          if (applied) {
            this.videocall.micProc = Object.assign({}, applied);
            try { localStorage.setItem('panel_vc_mic_proc', JSON.stringify(applied)); } catch (_) {}
          }
        } catch (_) {}
        finally { this.videocall.micProcBusy = false; }
      } else if (this.videocall.lobbyOpen && (this.videocall.lobbyCaps || {audio:true}).audio) {
        this.vcLobbyStartMicMonitor();
      }
    },
    _vcMicMeterEnsure() {
      if (this._vcMicMeterRaf) return;
      let last = 0;
      const tick = (t) => {
        const vc = this.videocall;
        const open = vc.inCall && (vc.audioPopOpen || vc.settingsOpen);
        const api = window.PanelVideoCall;
        const r = open && api && api.getMicLevel ? api.getMicLevel() : null;
        if (!r) {
          this._vcMicMeterRaf = 0;
          vc.micLevel = 0; vc.micLimiting = false;
          return;
        }
        if (t - last >= 50) {
          last = t;
          vc.micLevel = r.level;
          vc.micLimiting = !!r.limiting;
          if (vc.settingsOpen) vc.settingsMicLevel = r.level;
        }
        this._vcMicMeterRaf = requestAnimationFrame(tick);
      };
      this._vcMicMeterRaf = requestAnimationFrame(tick);
    },
    vcOwnerMutePeer(peerId) {
      if (!window.PanelVideoCall || !window.PanelVideoCall.ownerMutePeer) return;
      try { window.PanelVideoCall.ownerMutePeer(peerId); this.vcToast('🔇 Participant muted'); } catch (_) {}
    },
    vcOwnerCameraOffPeer(peerId) {
      if (!window.PanelVideoCall || !window.PanelVideoCall.ownerCameraOffPeer) return;
      try { window.PanelVideoCall.ownerCameraOffPeer(peerId); this.vcToast('📷 Camera turned off'); } catch (_) {}
    },
    async vcOwnerKickPeerConfirm(peerId, displayName) {
      if (!window.PanelVideoCall || !window.PanelVideoCall.ownerKickPeer) return;
      const name = displayName || 'this participant';
      if (!(await this.confirmAsync('Kick ' + name + ' from the call?\n\nThey will see a message and be disconnected.'))) return;
      try { window.PanelVideoCall.ownerKickPeer(peerId); this.vcToast('🚪 ' + name + ' was removed'); } catch (_) {}
    },
    vcOwnerToggleLock() {
      if (!window.PanelVideoCall || !window.PanelVideoCall.ownerLockRoom) return;
      const next = !this.videocall.roomLocked;
      try {
        window.PanelVideoCall.ownerLockRoom(next);
        this.videocall.roomLocked = next;
        this.vcToast(next ? '🔒 Room locked' : '🔓 Room unlocked');
      } catch (_) {}
    },
    vcOwnerMuteAll() {
      if (!window.PanelVideoCall || !window.PanelVideoCall.ownerMuteAll) return;
      try {
        const n = window.PanelVideoCall.ownerMuteAll();
        this.vcToast('🔇 ' + n + ' participant(s) muted');
      } catch (_) {}
    },
    vcToast(msg) {
      try {
        if (typeof this.vcStatusToast === 'function') return this.vcStatusToast(msg);
      } catch (_) {}
      console.log('[vc] ' + msg);
    },
    vcSetSubtitlesLang(lang) {
      this.videocall.subtitlesLang = lang;
      localStorage.setItem('panel_vc_subtitles_lang', lang);
      this.videocall.subtitlesLangPickerOpen = false;
      if (this.videocall.subtitlesActive && window.PanelVideoCall) {
        if (window.PanelVideoCall.changeSubtitlesLang) {
          window.PanelVideoCall.changeSubtitlesLang(lang);
        } else {
          window.PanelVideoCall.setSubtitles(false);
          window.PanelVideoCall.setSubtitles(true, { lang, token: this.token });
        }
      }
    },
    vcSubtitlesLangLabel() {
      const map = { 'en-US':'English (US)','pt-BR':'Portuguese (BR)','es-ES':'Spanish','fr-FR':'French','it-IT':'Italian','de-DE':'German','ja-JP':'Japanese' };
      return map[this.videocall.subtitlesLang] || this.videocall.subtitlesLang;
    },
    vcOpenTranscriptPanel() { this.vcToggleSidePanel('transcript'); },
    vcClearTranscript() {
      this.vcAskConfirm({
        title: 'Clear the transcription?',
        desc: 'Clears only on your side — the transcript of the other peers is not affected.',
        confirmLabel: 'Clear',
        danger: true,
        onYes: () => { this.videocall.transcriptEntries = []; this.videocall.transcript = ''; },
      });
    },
    vcDownloadTranscript(format) {
      format = format || 'txt';
      const entries = this.videocall.transcriptEntries;
      const room = (this.videocall.selectedRoom && this.videocall.selectedRoom.name) || this.videocall.activeRoomId || 'room';
      const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
      let blob, ext;
      const ms2srt = (ms) => {
        const s = Math.max(0, ms / 1000);
        const hh = String(Math.floor(s / 3600)).padStart(2, '0');
        const mm = String(Math.floor((s % 3600) / 60)).padStart(2, '0');
        const ss = String(Math.floor(s % 60)).padStart(2, '0');
        const mmm = String(Math.floor((s % 1) * 1000)).padStart(3, '0');
        return hh + ':' + mm + ':' + ss + ',' + mmm;
      };
      const ms2vtt = (ms) => ms2srt(ms).replace(',', '.');
      const callStart = this.videocall.callStartedAt || (entries[0] && entries[0].ts) || Date.now();
      if (format === 'srt') {
        const lines = [];
        entries.forEach((e, i) => {
          const relStart = (e.ts - callStart);
          const dur = (e.endMs && e.startMs) ? (e.endMs - e.startMs) : Math.max(2000, e.text.length * 70);
          lines.push(String(i + 1));
          lines.push(ms2srt(relStart) + ' --> ' + ms2srt(relStart + dur));
          lines.push(e.fromLabel + ': ' + e.text);
          lines.push('');
        });
        blob = new Blob([lines.join('\n')], { type: 'application/x-subrip;charset=utf-8' });
        ext = 'srt';
      } else if (format === 'vtt') {
        const lines = ['WEBVTT', ''];
        entries.forEach((e, i) => {
          const relStart = (e.ts - callStart);
          const dur = (e.endMs && e.startMs) ? (e.endMs - e.startMs) : Math.max(2000, e.text.length * 70);
          lines.push(ms2vtt(relStart) + ' --> ' + ms2vtt(relStart + dur));
          lines.push(e.fromLabel + ': ' + e.text);
          lines.push('');
        });
        blob = new Blob([lines.join('\n')], { type: 'text/vtt;charset=utf-8' });
        ext = 'vtt';
      } else if (format === 'json') {
        blob = new Blob([JSON.stringify({ room, generatedAt: new Date().toISOString(), entries }, null, 2)], { type: 'application/json' });
        ext = 'json';
      } else {
        const lines = entries.map(e =>
          '[' + new Date(e.ts).toISOString() + '] ' + e.fromLabel + ': ' + e.text
        );
        const header = '# Video call transcription\n# Room: ' + room + '\n# Generated: ' + new Date().toISOString() + '\n# Total: ' + lines.length + ' entries\n\n';
        blob = new Blob([header + lines.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' });
        ext = 'txt';
      }
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'transcript-' + room.replace(/[^a-z0-9-]/gi, '_') + '-' + ts + '.' + ext;
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 5000);
      return;
    },
    async _vcDB() {
      if (this._vcDBHandle) return this._vcDBHandle;
      return new Promise((resolve, reject) => {
        const req = indexedDB.open('panel-vc-transcript', 1);
        req.onupgradeneeded = (ev) => {
          const db = ev.target.result;
          if (!db.objectStoreNames.contains('entries')) {
            const store = db.createObjectStore('entries', { keyPath: 'id' });
            store.createIndex('roomId', 'roomId', { unique: false });
            store.createIndex('ts', 'ts', { unique: false });
          }
        };
        req.onsuccess = () => { this._vcDBHandle = req.result; resolve(req.result); };
        req.onerror = () => reject(req.error);
      });
    },
    async _vcPersistEntry(entry) {
      try {
        const db = await this._vcDB();
        const tx = db.transaction('entries', 'readwrite');
        tx.objectStore('entries').put({ ...entry, roomId: this.videocall.activeRoomId || 'unknown' });
      } catch (_) { /* IndexedDB unavailable (private mode, full disk) — skip silently */ }
    },
    async vcRestoreTranscriptFromDB() {
      try {
        const db = await this._vcDB();
        const tx = db.transaction('entries', 'readonly');
        const idx = tx.objectStore('entries').index('roomId');
        const req = idx.getAll(this.videocall.activeRoomId);
        req.onsuccess = () => {
          const rows = (req.result || []).sort((a, b) => a.ts - b.ts);
          if (rows.length) {
            this.videocall.transcriptEntries = rows;
            this.vcInfo('Recovered ' + rows.length + ' saved transcript entries');
          }
        };
      } catch (_) {}
    },
    vcIsSpeakerActive(peerId) {
      const s = this.videocall.peerAudioLevels || {};
      return (s[peerId] || 0) > 0.04;
    },
    _vcOriginalDownload() {
      const lines = this.videocall.transcriptEntries.map(e =>
        '[' + new Date(e.ts).toISOString() + '] ' + e.fromLabel + ': ' + e.text
      );
      const header = '# Video call transcription\n# Total: ' + lines.length + '\n\n';
      const blob = new Blob([header + lines.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      const stamp = new Date().toISOString().replace(/[:.]/g,'-').slice(0,19);
      a.href = url; a.download = 'transcript-' + stamp + '.txt';
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    },
    vcFilteredTranscript() {
      const q = (this.videocall.transcriptSearch || '').toLowerCase().trim();
      if (!q) return this.videocall.transcriptEntries;
      return this.videocall.transcriptEntries.filter(e =>
        (e.text||'').toLowerCase().includes(q) || (e.fromLabel||'').toLowerCase().includes(q)
      );
    },
    vcTogglePolishAI() {
      this.videocall.polishWithAI = !this.videocall.polishWithAI;
      localStorage.setItem('panel_vc_polish_ai', this.videocall.polishWithAI ? '1' : '0');
      if (this.videocall.polishWithAI) {
        for (const e of this.videocall.transcriptEntries) {
          if (!e.polishedText && !e.polishing && this._vcShouldPolish(e.text)) {
            this.videocall.polishQueue.push(e.id);
          }
        }
        this._vcDrainPolishQueue();
      }
    },
    vcToggleShowOriginal(entryId) {
      const cur = this.videocall.showOriginalIds || {};
      this.videocall.showOriginalIds = { ...cur, [entryId]: !cur[entryId] };
    },
    _vcShouldPolish(text) {
      if (!text) return false;
      const t = String(text).trim();
      if (t.length < 8) return false;
      const words = t.split(/\s+/).length;
      if (words < 3) return false;
      if (/^[A-ZÀ-Ý]/.test(t) && /[.!?]$/.test(t) && words < 10) return false;
      return true;
    },
    _vcEnqueuePolish(entryId) {
      if (!this.videocall.polishWithAI) return;
      const e = this.videocall.transcriptEntries.find(x => x.id === entryId);
      if (!e || e.polishedText || e.polishing) return;
      if (!this._vcShouldPolish(e.text)) return;
      this.videocall.polishQueue.push(entryId);
      this._vcDrainPolishQueue();
    },
    async _vcDrainPolishQueue() {
      if (this.videocall.polishBusy) return;
      this.videocall.polishBusy = true;
      try {
        while (this.videocall.polishQueue.length > 0) {
          const id = this.videocall.polishQueue.shift();
          const e = this.videocall.transcriptEntries.find(x => x.id === id);
          if (!e || e.polishedText) continue;
          e.polishing = true;
          this.videocall.transcriptEntries = this.videocall.transcriptEntries.slice();
          try {
            const idx = this.videocall.transcriptEntries.indexOf(e);
            const ctx = [];
            for (let i = Math.max(0, idx-5); i < idx && ctx.length < 3; i++) {
              const prev = this.videocall.transcriptEntries[i];
              ctx.push(prev.polishedText || prev.text);
            }
            const r = await fetch('/api/videocall/transcript/polish', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + this.token },
              body: JSON.stringify({
                text: e.text,
                lang: this.videocall.subtitlesLang || 'en-US',
                context: ctx,
                speaker: this.vcPeerLabel(e.from),
              }),
            });
            if (r.ok) {
              const data = await r.json();
              if (data.polished && data.polished !== e.text) {
                e.polishedText = data.polished;
              }
            } else if (r.status === 503) {
              this.videocall.polishWithAI = false;
              localStorage.setItem('panel_vc_polish_ai', '0');
              this.vcError('AI polish unavailable: set ANTHROPIC_API_KEY on the server');
              break;
            }
          } catch (err) { /* network blip — move on to the next */ }
          e.polishing = false;
          this.videocall.transcriptEntries = this.videocall.transcriptEntries.slice();
        }
      } finally {
        this.videocall.polishBusy = false;
      }
    },

    vcPeerLabel(peerId) {
      if (!peerId || peerId === 'me') return this.username || 'You';
      const list = this.videocall.peersList || [];
      const found = list.find(p => p.id === peerId);
      if (found && found.user) return String(found.user).replace(/^guest:/, '');
      const st = this.videocall.peerStates && this.videocall.peerStates[peerId];
      if (st && st.displayName) return String(st.displayName).replace(/^guest:/, '');
      return 'Participant';
    },
    vcPeerName(peerId) {
      const l = this.vcPeerLabel(peerId);
      return l === 'Participant' ? '' : l;
    },
    vcPeerColor(peerId) {
      if (!peerId || peerId === 'me') return '#2563eb';
      let h = 0;
      const s = String(peerId);
      for (let i = 0; i < s.length; i++) h = ((h << 5) - h + s.charCodeAt(i)) | 0;
      const hue = Math.abs(h) % 360;
      return `hsl(${hue}, 55%, 45%)`;
    },
    vcPeerInitial(peerId) {
      const name = this.vcPeerLabel(peerId);
      return (name.trim().charAt(0) || '?').toUpperCase();
    },
    vcGroupedTranscript() {
      const arr = this.vcFilteredTranscript();
      const out = [];
      let lastFrom = null, lastTs = 0;
      for (const e of arr) {
        const grouped = (e.from === lastFrom) && (e.ts - lastTs < 90000);
        out.push({ ...e, grouped, fromLabel: this.vcPeerLabel(e.from) });
        lastFrom = e.from; lastTs = e.ts;
      }
      return out;
    },
    vcMaybeScrollTranscript() {
      this.$nextTick(() => {
        const el = document.getElementById('vc-tr-scroll');
        if (!el) return;
        const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
        if (dist < 240) {
          el.scrollTop = el.scrollHeight;
          this.videocall.transcriptHasMore = false;
        } else {
          this.videocall.transcriptHasMore = true;
        }
      });
    },
    vcScrollTranscriptBottom() {
      const el = document.getElementById('vc-tr-scroll');
      if (!el) return;
      el.scrollTop = el.scrollHeight;
      this.videocall.transcriptHasMore = false;
    },
    vcOnTranscriptScroll() {
      const el = document.getElementById('vc-tr-scroll');
      if (!el) return;
      const dist = el.scrollHeight - el.scrollTop - el.clientHeight;
      this.videocall.transcriptHasMore = dist > 120;
    },
    vcShowCaption() {
      const c = this.videocall.currentCaption;
      if (!c || !c.text) return false;
      if (Date.now() > c.expireAt) return false;
      return true;
    },

    async vcToggleFullscreen() {
      const root = document.getElementById('vc-call-root');
      if (!root) return;
      try {
        if (!document.fullscreenElement) {
          await root.requestFullscreen();
          this.videocall.stageFullscreen = true;
        } else {
          await document.exitFullscreen();
          this.videocall.stageFullscreen = false;
        }
      } catch (e) { this.vcError('fullscreen: ' + e.message); }
    },
    vcCallActivity() {
      this.videocall.controlsVisible = true;
      if (this._idleTimer) clearTimeout(this._idleTimer);
      this._idleTimer = setTimeout(() => {
        if (this.videocall.settingsPopOpen || this.videocall.audioPopOpen || this.videocall.videoPopOpen) return;
        if (this.videocall.sidePanel) return;
        this.videocall.controlsVisible = false;
      }, 3500);
    },
    vcCallMouseMove() { return this.vcCallActivity(); },
    vcVideosGridClass() {
      const n = (this.videocall.peerCount || 0) + 1;
      if (n <= 1) return 'solo';
      if (n === 2) return 'peers-2';
      if (n === 3) return 'peers-3';
      return 'peers-4';
    },

    async vcLobbyOpen(roomId, passphrase) {
      this.videocall.lobbyForRoomId = roomId;
      this.videocall.lobbyForPassphrase = passphrase || '';
      this.videocall.lobbyError = '';
      this.videocall.lobbyOpen = true;
      this.videocall.lobbyBusy = true;
      try {
        const probe = await window.PanelVideoCall.probeDevicePermission();
        this.videocall.lobbyCaps = { audio: !!(probe && probe.audio), video: !!(probe && probe.video) };
        this.videocall.lobbyPermission = probe && probe.ok ? 'granted' : 'denied';
        this.videocall.lobbyError = (probe && probe.ok && probe.audio && probe.video)
          ? '' : ((probe && probe.error) || 'Allow the camera and the microphone in the browser to continue.');
        await this.vcRefreshDevices();
        const cams = this.videocall.devices.cameras.map(d => d.deviceId);
        const mics = this.videocall.devices.mics.map(d => d.deviceId);
        const spks = this.videocall.devices.speakers.map(d => d.deviceId);
        if (this.videocall.selectedDevices.camera !== 'default' && !cams.includes(this.videocall.selectedDevices.camera)) this.videocall.selectedDevices.camera = 'default';
        if (this.videocall.selectedDevices.mic    !== 'default' && !mics.includes(this.videocall.selectedDevices.mic))    this.videocall.selectedDevices.mic = 'default';
        if (this.videocall.selectedDevices.speaker !== 'default' && spks.length && !spks.includes(this.videocall.selectedDevices.speaker)) this.videocall.selectedDevices.speaker = 'default';
        if (this.videocall.lobbyCaps.video) await this.vcLobbyStartPreview();
        if (this.videocall.lobbyCaps.audio) this.vcLobbyStartMicMonitor();
        this.vcLobbyWatchDevices();
      } catch (e) {
        this.videocall.lobbyError = e.message;
      } finally {
        this.videocall.lobbyBusy = false;
      }
    },
    async vcRefreshDevices() {
      const d = await window.PanelVideoCall.listDevices();
      this.videocall.devices = d;
    },
    async vcLobbyStartPreview() {
      const v = document.getElementById('vc-lobby-preview');
      if (!v) return;
      if (v.srcObject) {
        try { v.srcObject.getTracks().forEach(t => t.stop()); } catch (_) {}
        v.srcObject = null;
      }
      const camId = this.videocall.selectedDevices.camera;
      const constraints = {
        video: camId === 'default' ? { width:{ideal:640} } : { deviceId: { exact: camId }, width:{ideal:640} },
        audio: false,
      };
      try {
        const s = await navigator.mediaDevices.getUserMedia(constraints);
        v.srcObject = s;
      } catch (e) {
        this.videocall.lobbyError = 'camera: ' + e.message;
      }
    },
    vcLobbyStartMicMonitor() {
      if (this._lobbyMicMon) { this._lobbyMicMon.stop(); this._lobbyMicMon = null; }
      const micId = this.videocall.selectedDevices.mic;
      this._lobbyMicMon = window.PanelVideoCall.createMicLevelMonitor(micId, (lvl, info) => {
        this.videocall.lobbyMicLevel = lvl;
        this.videocall.micLimiting = !!(info && info.limiting);
      }, { gain: () => this.videocall.micGain || 1, processing: this.videocall.micProc });
    },
    async vcLobbyChangeCamera(deviceId) {
      this.videocall.selectedDevices.camera = deviceId;
      localStorage.setItem('panel_vc_camera_id', deviceId);
      await this.vcLobbyStartPreview();
    },
    vcLobbyChangeMic(deviceId) {
      this.videocall.selectedDevices.mic = deviceId;
      localStorage.setItem('panel_vc_mic_id', deviceId);
      this.vcLobbyStartMicMonitor();
    },
    vcLobbyChangeSpeaker(deviceId) {
      this.videocall.selectedDevices.speaker = deviceId;
      localStorage.setItem('panel_vc_speaker_id', deviceId);
    },
    async vcLobbyTestSpeaker() {
      clearTimeout(this._lobbyToneTimer);
      this.videocall.lobbyTone = true;
      this._lobbyToneTimer = setTimeout(() => { this.videocall.lobbyTone = false; }, 900);
      await window.PanelVideoCall.playTestTone(this.videocall.selectedDevices.speaker);
    },
    vcLobbyToggleSkip() {
      this.videocall.lobbySkipNext = !this.videocall.lobbySkipNext;
      localStorage.setItem('panel_vc_skip_lobby', this.videocall.lobbySkipNext ? '1' : '0');
    },
    async vcLobbyConfirm() {
      const roomId = this.videocall.lobbyForRoomId;
      const pass = this.videocall.lobbyForPassphrase;
      const caps = this.videocall.lobbyCaps || { audio: true, video: true };
      this.vcLobbyCleanup();
      this.videocall.lobbyOpen = false;
      await this._vcStartCall(roomId, pass, { audioOnly: !caps.video, micOff: !caps.audio });
    },
    vcLobbyCancel() {
      this.vcLobbyCleanup();
      this.videocall.lobbyOpen = false;
      this.videocall.lobbyForRoomId = '';
      this.videocall.lobbyForPassphrase = '';
    },
    vcLobbyWatchDevices() {
      if (this._lobbyDevWatch || !navigator.mediaDevices) return;
      this._lobbyDevSig = null;
      const signature = async () => {
        try {
          const l = await navigator.mediaDevices.enumerateDevices();
          return l.map(d => d.kind + ':' + d.deviceId).sort().join('|');
        } catch (_) { return null; }
      };
      signature().then(sig => { this._lobbyDevSig = sig; });
      this._lobbyDevWatch = () => {
        clearTimeout(this._lobbyDevTimer);
        this._lobbyDevTimer = setTimeout(async () => {
          if (!this.videocall.lobbyOpen || this.videocall.lobbyBusy) return;
          const sig = await signature();
          if (sig === null || sig === this._lobbyDevSig) return;
          this._lobbyDevSig = sig;
          this.vcLobbyOpen(this.videocall.lobbyForRoomId, this.videocall.lobbyForPassphrase);
        }, 400);
      };
      try { navigator.mediaDevices.addEventListener('devicechange', this._lobbyDevWatch); } catch (_) { this._lobbyDevWatch = null; }
    },
    vcLobbyCleanup() {
      if (this._lobbyDevWatch) {
        clearTimeout(this._lobbyDevTimer);
        try { navigator.mediaDevices.removeEventListener('devicechange', this._lobbyDevWatch); } catch (_) {}
        this._lobbyDevWatch = null;
      }
      clearTimeout(this._lobbyToneTimer);
      this.videocall.lobbyTone = false;
      if (this._lobbyMicMon) { this._lobbyMicMon.stop(); this._lobbyMicMon = null; }
      const v = document.getElementById('vc-lobby-preview');
      if (v && v.srcObject) {
        try { v.srcObject.getTracks().forEach(t => t.stop()); } catch (_) {}
        v.srcObject = null;
      }
    },

    async vcSettingsOpen() {
      this.videocall.settingsOpen = true;
      await this.vcRefreshDevices();
      this.vcSettingsStartMicMonitor();
    },
    vcSettingsClose() {
      clearTimeout(this._settingsToneTimer);
      this.videocall.settingsTone = false;
      if (this._settingsMicMon) { this._settingsMicMon.stop(); this._settingsMicMon = null; }
      this.videocall.settingsOpen = false;
    },
    vcSettingsStartMicMonitor() {
      if (this._settingsMicMon) { this._settingsMicMon.stop(); this._settingsMicMon = null; }
      const api = window.PanelVideoCall;
      if (this.videocall.inCall && api && api.getMicLevel && api.getMicLevel()) {
        this._vcMicMeterEnsure();
        return;
      }
      const micId = this.videocall.selectedDevices.mic;
      this._settingsMicMon = window.PanelVideoCall.createMicLevelMonitor(micId, (lvl) => {
        this.videocall.settingsMicLevel = lvl;
      });
    },
    async vcSettingsChangeCamera(id) {
      this.videocall.selectedDevices.camera = id;
      localStorage.setItem('panel_vc_camera_id', id);
      await window.PanelVideoCall.setCameraDevice(id);
    },
    async vcSettingsChangeMic(id) {
      this.videocall.selectedDevices.mic = id;
      localStorage.setItem('panel_vc_mic_id', id);
      await window.PanelVideoCall.setMicDevice(id);
      this.vcSettingsStartMicMonitor();
    },
    async vcSettingsChangeSpeaker(id) {
      this.videocall.selectedDevices.speaker = id;
      localStorage.setItem('panel_vc_speaker_id', id);
      await window.PanelVideoCall.setSpeakerDevice(id);
    },
    async vcSettingsTestSpeaker() {
      clearTimeout(this._settingsToneTimer);
      this.videocall.settingsTone = true;
      this._settingsToneTimer = setTimeout(() => { this.videocall.settingsTone = false; }, 900);
      await window.PanelVideoCall.playTestTone(this.videocall.selectedDevices.speaker);
    },

    async vcPinFromCall() {
      const room = this.videocall.selectedRoom;
      if (!room) {
        if (this.videocall.activeRoomId) {
          await this.vcLoadRooms();
          this.videocall.selectedRoom = this.videocall.rooms.find(r => r.id === this.videocall.activeRoomId);
        }
        if (!this.videocall.selectedRoom) { this.vcError('room not found'); return; }
      }
      this.vcClosePopovers();
      this.videocall.pinModalOpen = true;
    },

    async vcInviteFromCall() {
      const roomId = this.videocall.activeRoomId || (this.videocall.selectedRoom && this.videocall.selectedRoom.id);
      if (!roomId) return;
      await this.vcGenerateInvite(roomId, this.videocall.inviteTTLMin || 60);
    },

    async vcToggleFrost() {
      if (!window.PanelVideoCall) return;
      this.videocall.frostActive = !!(await window.PanelVideoCall.setPrivacyFrost(!this.videocall.frostActive));
    },

    async vcTogglePush() {
      if (!window.PanelPush) return;
      if (this.videocall.pushBusy) return;
      this.videocall.pushBusy = true;
      try {
        if (this.videocall.pushSubscribed) {
          await window.PanelPush.unsubscribe(this.token);
          this.videocall.pushSubscribed = false;
        } else {
          await window.PanelPush.subscribe(this.token, (roomId) => {
            this.setPage('videocall');
            this.$nextTick(() => this.vcJoinCall(roomId));
          });
          this.videocall.pushSubscribed = true;
        }
        const st = await window.PanelPush.getState(this.token);
        this.videocall.pushPermission = st.permission || 'default';
      } catch (e) {
        this.vcError('push: ' + e.message);
      } finally {
        this.videocall.pushBusy = false;
      }
    },

    _vcShareMessage() {
      const r = this.videocall.selectedRoom;
      const name = (r && r.name) ? r.name : 'room';
      return 'Come talk to me in ' + name + ' — ' + this.videocall.inviteURL;
    },
    vcShareNative() {
      if (!navigator.share) { this.vcShareCopy(); return; }
      const msg = this._vcShareMessage();
      navigator.share({ title: 'Video call', text: msg, url: this.videocall.inviteURL })
        .catch((e) => { if (e && e.name !== 'AbortError') this.vcError('share: ' + e.message); });
    },
    vcShareWhatsApp() {
      const url = 'https://wa.me/?text=' + encodeURIComponent(this._vcShareMessage());
      window.open(url, '_blank', 'noopener,noreferrer');
    },
    vcShareTelegram() {
      const u = encodeURIComponent(this.videocall.inviteURL);
      const t = encodeURIComponent('Video call — join the room');
      window.open('https://t.me/share/url?url=' + u + '&text=' + t, '_blank', 'noopener,noreferrer');
    },
    vcShareEmail() {
      const subject = encodeURIComponent('Video call — invite');
      const body = encodeURIComponent(this._vcShareMessage() + '\n\n(Link expires in ' + (this.videocall.inviteExpiresAt ? new Date(this.videocall.inviteExpiresAt*1000).toLocaleString() : 'soon') + ')');
      window.location.href = 'mailto:?subject=' + subject + '&body=' + body;
    },
    vcShareSMS() {
      window.location.href = 'sms:?body=' + encodeURIComponent(this._vcShareMessage());
    },
    vcShareCopy() { return this.vcCopyInvite(); },

    vcOpenFilePicker() {
      const inp = document.getElementById('vc-file-input');
      if (inp) inp.click();
    },
    async vcOnFilesPicked(ev) {
      const files = Array.from(ev.target.files || []);
      ev.target.value = '';
      for (const f of files) {
        if (f.size > 500 * 1024 * 1024) { this.vcError(f.name + ': larger than 500MB; refused'); continue; }
        this.videocall.filesPanelOpen = true;
        try { await window.PanelVideoCall.sendFile(f); } catch (e) { this.vcError('send file: ' + e.message); }
      }
    },
    vcOnFileProgress(p) {
      const list = this.videocall.transfers;
      let t = list.find(x => x.id === p.id);
      if (!t) {
        t = { id: p.id, name: p.name, size: p.size, direction: p.direction, from: p.from, to: p.to, sent: 0, received: 0, done: false };
        list.unshift(t);
        if (list.length > 50) list.pop();
        this.videocall.filesPanelOpen = true;
      }
      if (p.direction === 'in') t.received = p.received || 0;
      else                       t.sent     = p.sent     || 0;
    },
    vcOnFileReceived(f) {
      const t = this.videocall.transfers.find(x => x.id === f.id);
      if (t) { t.done = true; t.blob = f.blob; }
      const url = URL.createObjectURL(f.blob);
      const a = document.createElement('a');
      a.href = url; a.download = f.name || 'file';
      document.body.appendChild(a); a.click(); a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
    },
    vcCloseFilesPanel() { this.videocall.filesPanelOpen = false; },

    vcToggleWhiteboard() {
      this.videocall.wbActive = !this.videocall.wbActive;
      this.$nextTick(() => {
        if (this.videocall.wbActive) {
          if (this.videocall.annotActive) { this.videocall.annotActive = false; this.vcAnnotTeardown(); }
          this.vcWBInit();
        }
      });
    },
    vcWBInit() {
      const canvas = document.getElementById('vc-wb-canvas');
      if (!canvas) return;
      const ctx = canvas.getContext('2d');
      this._wbCtx = ctx;
      this._wbDrawing = false;
      this._wbLast = null;
      this.vcWBResize();
      const pointerDown = (e) => {
        e.preventDefault();
        this._wbDrawing = true;
        const p = this.vcWBPoint(canvas, e);
        this._wbLast = p;
      };
      const pointerMove = (e) => {
        if (!this._wbDrawing) return;
        e.preventDefault();
        const p = this.vcWBPoint(canvas, e);
        const color = this.videocall.wbColor;
        this.vcWBDrawLine(this._wbLast, p, color, 3);
        if (window.PanelVideoCall) {
          window.PanelVideoCall.sendWhiteboardEvent({ type: 'wb-line', from: this._wbLast, to: p, color: color, width: 3 });
        }
        this._wbLast = p;
      };
      const pointerUp = () => { this._wbDrawing = false; this._wbLast = null; };
      canvas.onpointerdown = pointerDown;
      canvas.onpointermove = pointerMove;
      canvas.onpointerup = pointerUp;
      canvas.onpointercancel = pointerUp;
      canvas.onpointerleave = pointerUp;
      if (!this._wbResizeObs) {
        this._wbResizeObs = new ResizeObserver(() => this.vcWBResize());
        this._wbResizeObs.observe(canvas.parentElement);
      }
    },
    vcWBResize() {
      const canvas = document.getElementById('vc-wb-canvas');
      if (!canvas || !this._wbCtx) return;
      const sz = canvas.parentElement.getBoundingClientRect();
      const dpr = window.devicePixelRatio || 1;
      this._wbDpr = dpr;
      canvas.width = Math.round(Math.max(640, sz.width) * dpr);
      canvas.height = Math.round(Math.max(360, sz.height) * dpr);
      this.vcWBRedraw();
    },
    vcWBRedraw() {
      const c = document.getElementById('vc-wb-canvas');
      if (c && this._wbCtx) this._wbCtx.clearRect(0, 0, c.width, c.height);
      const strokes = (window.PanelVideoCall && window.PanelVideoCall.getWhiteboardStrokes()) || [];
      for (const s of strokes) {
        const col = s._from ? this.vcPeerColor(s._from) : (s.color || '#fbbf24');
        this.vcWBDrawLine(s.from, s.to, col, s.width);
      }
    },
    vcWBPoint(canvas, e) {
      const r = canvas.getBoundingClientRect();
      return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height };
    },
    _vcCanvasFor(surface) {
      if (surface === 'screen') {
        return this._annotCtx ? { ctx: this._annotCtx, dpr: this._annotDpr || 1 } : null;
      }
      return this._wbCtx ? { ctx: this._wbCtx, dpr: this._wbDpr || 1 } : null;
    },
    vcWBDrawLine(p1, p2, color, width, alpha, surface) {
      const target = this._vcCanvasFor(surface);
      if (!target || !p1 || !p2) return;
      const ctx = target.ctx, dpr = target.dpr;
      const cw = ctx.canvas.width, ch = ctx.canvas.height;
      ctx.save();
      ctx.strokeStyle = color || '#fff';
      ctx.lineWidth = (width || 2) * dpr;
      ctx.lineCap = 'round';
      ctx.lineJoin = 'round';
      ctx.globalAlpha = (alpha == null ? 1 : alpha);
      ctx.beginPath();
      ctx.moveTo(p1.x * cw, p1.y * ch);
      ctx.lineTo(p2.x * cw, p2.y * ch);
      ctx.stroke();
      ctx.restore();
    },
    vcOnWhiteboard(ev) {
      if (ev && ev.surface === 'screen') { this.vcOnAnnot(ev); return; }
      if (!this.videocall.wbActive) {
        this.videocall.wbActive = true;
        this.$nextTick(() => { this.vcWBInit(); this.vcOnWhiteboard(ev); });
        return;
      }
      if (ev.type === 'wb-snapshot') {
        this.vcWBRedraw();
      } else if (ev.type === 'wb-line') {
        const c = ev._from ? this.vcPeerColor(ev._from) : (ev.color || '#fbbf24');
        this.vcWBDrawLine(ev.from, ev.to, c, ev.width);
      } else if (ev.type === 'wb-clear') {
        const c = document.getElementById('vc-wb-canvas');
        if (c) this._wbCtx && this._wbCtx.clearRect(0, 0, c.width, c.height);
      }
    },
    vcWBClear() {
      const c = document.getElementById('vc-wb-canvas');
      if (c && this._wbCtx) this._wbCtx.clearRect(0, 0, c.width, c.height);
      if (window.PanelVideoCall) window.PanelVideoCall.sendWhiteboardEvent({ type: 'wb-clear' });
    },
    vcWBTeardown() {
      if (this._wbResizeObs) { try { this._wbResizeObs.disconnect(); } catch (_) {} this._wbResizeObs = null; }
      this._wbCtx = null;
      this._wbDrawing = false;
      this._wbLast = null;
    },

    vcAnnotAvailable() {
      if (this.videocall.screenSharing) return true;
      const ps = this.videocall.peerStates || {};
      for (const id in ps) { if (ps[id] && ps[id].screen === 'on') return true; }
      return false;
    },
    vcAnnotActive() {
      return this.videocall.annotActive && this.vcAnnotAvailable();
    },
    vcAnnotTarget() {
      const videosEl = document.getElementById('vc-videos');
      if (!videosEl) return null;
      if (this.videocall.screenSharing) {
        return videosEl.querySelector('video[data-vc-local="1"]');
      }
      const ps = this.videocall.peerStates || {};
      let chosen = null;
      for (const id in ps) {
        if (ps[id] && ps[id].screen === 'on') {
          if (!chosen) chosen = id;
          else console.info('[panel:vc] annot: multiple presenters — ignoring ' + id + ' (MVP=1)');
        }
      }
      if (!chosen) return null;
      const tile = videosEl.querySelector('[data-vc-peer-tile="' + chosen + '"]');
      return tile ? tile.querySelector('video') : null;
    },
    vcAnnotContentBox(video, hostRect) {
      const r = video.getBoundingClientRect();
      const EW = r.width, EH = r.height;
      const VW = video.videoWidth, VH = video.videoHeight;
      if (!EW || !EH || !VW || !VH) return null;
      const scale = Math.min(EW / VW, EH / VH);
      const dw = VW * scale, dh = VH * scale;
      const ox = (EW - dw) / 2, oy = (EH - dh) / 2;
      return {
        left: (r.left - hostRect.left) + ox,
        top:  (r.top  - hostRect.top)  + oy,
        w: dw,
        h: dh,
      };
    },
    vcToggleAnnot() {
      this.videocall.annotActive = !this.videocall.annotActive;
      if (this.videocall.annotActive) {
        if (this.videocall.wbActive) { this.videocall.wbActive = false; this.vcWBTeardown(); }
        this.$nextTick(() => this.vcAnnotInit());
      } else {
        this.vcAnnotTeardown();
      }
    },
    vcAnnotInit() {
      const canvas = document.getElementById('vc-annot-canvas');
      if (!canvas) return;
      this._annotCtx = canvas.getContext('2d');
      this._annotDrawing = false;
      this._annotLast = null;
      this._vcApplyAnnotFit();
      this.vcAnnotReanchor();
      const toolWidth = () => (this.videocall.annotTool === 'hl' ? 14 : 3);
      const toolAlpha = () => (this.videocall.annotTool === 'hl' ? 0.35 : 1);
      const pointerDown = (e) => {
        e.preventDefault();
        this._annotDrawing = true;
        this._annotLast = this.vcAnnotPoint(canvas, e);
      };
      const pointerMove = (e) => {
        if (!this._annotDrawing) return;
        e.preventDefault();
        const p = this.vcAnnotPoint(canvas, e);
        const color = this.videocall.wbColor;
        const width = toolWidth(), alpha = toolAlpha();
        this.vcWBDrawLine(this._annotLast, p, color, width, alpha, 'screen');
        if (window.PanelVideoCall) {
          window.PanelVideoCall.sendWhiteboardEvent({ type: 'wb-line', surface: 'screen', from: this._annotLast, to: p, color: color, width: width, alpha: alpha });
        }
        this._annotLast = p;
      };
      const pointerUp = () => { this._annotDrawing = false; this._annotLast = null; };
      canvas.onpointerdown = pointerDown;
      canvas.onpointermove = pointerMove;
      canvas.onpointerup = pointerUp;
      canvas.onpointercancel = pointerUp;
      canvas.onpointerleave = pointerUp;
      this._vcBindAnnotVideoMeta();
    },
    vcAnnotReanchor() {
      const canvas = document.getElementById('vc-annot-canvas');
      if (!canvas || !this._annotCtx) return;
      const video = this.vcAnnotTarget();
      if (!video) return;
      const host = canvas.parentElement;
      if (!host) return;
      const geom = this.vcAnnotContentBox(video, host.getBoundingClientRect());
      if (!geom) return;
      canvas.style.left = geom.left + 'px';
      canvas.style.top = geom.top + 'px';
      canvas.style.width = geom.w + 'px';
      canvas.style.height = geom.h + 'px';
      const dpr = window.devicePixelRatio || 1;
      this._annotDpr = dpr;
      canvas.width = Math.max(1, Math.round(geom.w * dpr));
      canvas.height = Math.max(1, Math.round(geom.h * dpr));
      this.vcAnnotRedraw();
    },
    vcAnnotRedraw() {
      const c = document.getElementById('vc-annot-canvas');
      if (c && this._annotCtx) this._annotCtx.clearRect(0, 0, c.width, c.height);
      const strokes = (window.PanelVideoCall && window.PanelVideoCall.getAnnotStrokes()) || [];
      for (const s of strokes) {
        const col = s._from ? this.vcPeerColor(s._from) : (s.color || '#fbbf24');
        this.vcWBDrawLine(s.from, s.to, col, s.width, s.alpha, 'screen');
      }
    },
    vcAnnotPoint(canvas, e) {
      const r = canvas.getBoundingClientRect();
      let x = (e.clientX - r.left) / r.width;
      let y = (e.clientY - r.top) / r.height;
      x = x < 0 ? 0 : (x > 1 ? 1 : x);
      y = y < 0 ? 0 : (y > 1 ? 1 : y);
      return { x: x, y: y };
    },
    vcAnnotClear() {
      const c = document.getElementById('vc-annot-canvas');
      if (c && this._annotCtx) this._annotCtx.clearRect(0, 0, c.width, c.height);
      if (window.PanelVideoCall) window.PanelVideoCall.sendWhiteboardEvent({ type: 'wb-clear', surface: 'screen' });
    },
    vcOnAnnot(ev) {
      if (!this.vcAnnotAvailable()) return;
      if (!this.videocall.annotActive) {
        this.videocall.annotActive = true;
        this.$nextTick(() => { this.vcAnnotInit(); this.vcOnAnnot(ev); });
        return;
      }
      if (!this._annotCtx) { this.$nextTick(() => this.vcOnAnnot(ev)); return; }
      if (ev.type === 'wb-snapshot') {
        this.vcAnnotRedraw();
      } else if (ev.type === 'wb-line') {
        const c = ev._from ? this.vcPeerColor(ev._from) : (ev.color || '#fbbf24');
        this.vcWBDrawLine(ev.from, ev.to, c, ev.width, ev.alpha, 'screen');
      } else if (ev.type === 'wb-clear') {
        const cv = document.getElementById('vc-annot-canvas');
        if (cv && this._annotCtx) this._annotCtx.clearRect(0, 0, cv.width, cv.height);
      }
    },
    vcAnnotReconcile() {
      const avail = this.vcAnnotAvailable();
      const was = !!this._annotWasAvailable;
      this._annotWasAvailable = avail;
      if (was && !avail) { this.vcAnnotOnShareEnded(); return; }
      if (!avail) return;
      if (!was && !this.videocall.annotActive) {
        const arr = (window.PanelVideoCall && window.PanelVideoCall.getAnnotStrokes) ? window.PanelVideoCall.getAnnotStrokes() : [];
        if (arr && arr.length) {
          this.videocall.annotActive = true;
          this.$nextTick(() => this.vcAnnotInit());
          return;
        }
      }
      if (this.videocall.annotActive) {
        this.$nextTick(() => { this._vcApplyAnnotFit(); this._vcBindAnnotVideoMeta(); this.vcAnnotReanchor(); });
      }
    },
    vcAnnotOnShareEnded() {
      this.videocall.annotActive = false;
      this.vcAnnotTeardown();
      if (window.PanelVideoCall) {
        try { window.PanelVideoCall.sendWhiteboardEvent({ type: 'wb-clear', surface: 'screen' }); } catch (_) {}
      }
    },
    _vcApplyAnnotFit() {
      this._vcClearAnnotFit();
      const video = this.vcAnnotTarget();
      if (video) { video.classList.add('vc-annot-fit'); this._annotFitEl = video; }
    },
    _vcClearAnnotFit() {
      if (this._annotFitEl) { try { this._annotFitEl.classList.remove('vc-annot-fit'); } catch (_) {} this._annotFitEl = null; }
      try { document.querySelectorAll('.vc-annot-fit').forEach(el => el.classList.remove('vc-annot-fit')); } catch (_) {}
    },
    _vcBindAnnotVideoMeta() {
      const video = this.vcAnnotTarget();
      if (!video || this._annotMetaEl === video) return;
      this._vcUnbindAnnotVideoMeta();
      const h = () => this.vcAnnotReanchor();
      video.addEventListener('loadedmetadata', h);
      video.addEventListener('resize', h);
      this._annotMetaEl = video;
      this._annotMetaH = h;
      if (!this._annotResizeObs) this._annotResizeObs = new ResizeObserver(() => this.vcAnnotReanchor());
      try { this._annotResizeObs.disconnect(); } catch (_) {}
      try { this._annotResizeObs.observe(video); } catch (_) {}
    },
    _vcUnbindAnnotVideoMeta() {
      if (this._annotMetaEl && this._annotMetaH) {
        try {
          this._annotMetaEl.removeEventListener('loadedmetadata', this._annotMetaH);
          this._annotMetaEl.removeEventListener('resize', this._annotMetaH);
        } catch (_) {}
      }
      this._annotMetaEl = null;
      this._annotMetaH = null;
    },
    vcAnnotTeardown() {
      if (this._annotResizeObs) { try { this._annotResizeObs.disconnect(); } catch (_) {} this._annotResizeObs = null; }
      this._vcUnbindAnnotVideoMeta();
      this._vcClearAnnotFit();
      const c = document.getElementById('vc-annot-canvas');
      if (c && this._annotCtx) { try { this._annotCtx.clearRect(0, 0, c.width, c.height); } catch (_) {} }
      this._annotCtx = null;
      this._annotDrawing = false;
      this._annotLast = null;
    },

    vcOnIncoming(msg) {
      if (this.videocall.inCall && this.videocall.activeRoomId === msg.room_id) return;
      const callId = msg.call_id || '';
      if (callId) {
        if (!this._vcRungCalls) this._vcRungCalls = [];
        if (this._vcRungCalls.indexOf(callId) !== -1) return;
        this._vcRungCalls.push(callId);
        if (this._vcRungCalls.length > 50) this._vcRungCalls = this._vcRungCalls.slice(-25);
      }
      this.videocall.incoming = {
        roomId: msg.room_id, roomName: msg.room_name || msg.room_id,
        from: msg.from || 'someone', ts: Date.now(), callId: callId,
      };
      if (window.PanelPresence) window.PanelPresence.playRing(20000);
      if (this._vcIncomingTimer) clearTimeout(this._vcIncomingTimer);
      this._vcIncomingTimer = setTimeout(() => {
        if (this.videocall.incoming && this.videocall.incoming.callId === callId) {
          this.vcClearIncoming();
        }
      }, 45000);
      try {
        if (window.Notification && Notification.permission === 'granted') {
          const n = new Notification('Call from ' + (msg.from||'someone'), { body: 'Room: ' + (msg.room_name||''), tag: 'panel-vc-' + msg.room_id });
          n.onclick = () => { window.focus(); n.close(); };
          this._vcIncomingNotif = n;
        }
      } catch (_) {}
    },

    vcClearIncoming() {
      this.videocall.incoming = null;
      if (this._vcIncomingTimer) { clearTimeout(this._vcIncomingTimer); this._vcIncomingTimer = 0; }
      if (window.PanelPresence) window.PanelPresence.stopRing();
      try {
        if (this._vcIncomingNotif) { this._vcIncomingNotif.close(); this._vcIncomingNotif = null; }
      } catch (_) {}
    },

    vcOnPresenceEvent(msg) {
      if (!msg || !msg.type) return;
      if (msg.type !== 'call-answered-elsewhere' && msg.type !== 'call-ended') return;
      const inc = this.videocall.incoming;
      if (!inc) return;
      if (msg.call_id && inc.callId) {
        if (msg.call_id !== inc.callId) return;
      } else if (msg.room_id && inc.roomId && msg.room_id !== inc.roomId) {
        return;
      }
      this.vcClearIncoming();
      this.showToast(msg.type === 'call-ended'
        ? 'The call has ended'
        : 'Call answered on another device', '');
    },

    vcAcceptIncoming() {
      const inc = this.videocall.incoming;
      if (!inc) return;
      this.vcClearIncoming();
      this.setPage('videocall');
      this.$nextTick(() => this.vcJoinCall(inc.roomId));
    },
    vcDismissIncoming() {
      this.vcClearIncoming();
    },

    async vcDismissAndSilenceHere() {
      this.vcClearIncoming();
      await this.vcSetDeviceRing(this.videocall.thisDeviceId, false);
    },
    async vcDismissAndMuteHere(hours) {
      this.vcClearIncoming();
      await this.vcMuteDevice(this.videocall.thisDeviceId, hours || 8);
    },

    async vcLoadDevices() {
      if (!this.token) return;
      this.videocall.ringDevicesBusy = true;
      try {
        const r = await this.api('/api/videocall/devices');
        const d = await r.json();
        this.videocall.ringDevices = (d && d.devices) || [];
      } catch (e) {
        this.showToast('Could not list the devices: ' + e.message, 'err');
      } finally {
        this.videocall.ringDevicesBusy = false;
      }
    },
    async _vcDevicePost(body) {
      const r = await this.api('/api/videocall/devices', { method: 'POST', body: JSON.stringify(body) });
      const d = await r.json();
      if (d && d.devices) this.videocall.ringDevices = d.devices;
      else await this.vcLoadDevices();
    },
    async vcSetDeviceRing(deviceId, ring) {
      if (!deviceId) return;
      try {
        await this._vcDevicePost({ device_id: deviceId, ring: !!ring, label: (window.PanelDevice && deviceId === this.videocall.thisDeviceId) ? window.PanelDevice.label() : '' });
        this.showToast(ring ? 'This device will ring again' : 'Device muted', 'ok');
      } catch (e) {
        this.showToast('Could not save: ' + e.message, 'err');
      }
    },
    async vcMuteDevice(deviceId, hours) {
      if (!deviceId) return;
      try {
        await this._vcDevicePost({ device_id: deviceId, mute_hours: hours });
        this.showToast(hours > 0 ? ('Muted for ' + hours + 'h') : 'Mute cancelled', 'ok');
      } catch (e) {
        this.showToast('Could not save: ' + e.message, 'err');
      }
    },
    async vcForgetDevice(deviceId) {
      if (!deviceId) return;
      try {
        await this._vcDevicePost({ device_id: deviceId, forget: true });
        this.showToast('Device removed from the list', 'ok');
      } catch (e) {
        this.showToast('Could not remove: ' + e.message, 'err');
      }
    },
    vcDeviceIsThis(dev) {
      return !!dev && dev.device_id === this.videocall.thisDeviceId;
    },
    vcDeviceStatus(dev) {
      if (!dev) return '';
      const now = Math.floor(Date.now() / 1000);
      if (!dev.ring) return 'does not ring';
      if (dev.mute_until && dev.mute_until > now) {
        const d = new Date(dev.mute_until * 1000);
        return 'muted until ' + d.toLocaleString('en-US', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' });
      }
      return 'rings';
    },
    vcDeviceMuted(dev) {
      if (!dev) return false;
      const now = Math.floor(Date.now() / 1000);
      return !dev.ring || (dev.mute_until && dev.mute_until > now);
    },
    vcDeviceLastSeen(dev) {
      if (!dev || !dev.last_seen) return '';
      try { return this.timeAgo ? this.timeAgo(dev.last_seen) : new Date(dev.last_seen * 1000).toLocaleString('en-US'); }
      catch (_) { return ''; }
    },

    async vcLoadHistory() {
      try {
        const r = await fetch('/api/videocall/history?limit=30', { headers: { Authorization: 'Bearer ' + this.token } });
        if (!r.ok) return;
        this.videocall.history = await r.json();
        this.$nextTick(() => this.vcRenderHistoryChart());
      } catch (_) {}
    },
    async vcRenderHistoryChart() {
      const canvas = document.getElementById('vc-history-chart');
      if (!canvas) return;
      if (!(await this._ensureChart())) return;
      const data = (this.videocall.history || []).slice().reverse();
      const labels = data.map(h => new Date(h.started_at * 1000).toLocaleString('en-US', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }));
      const totals = data.map(h => (h.bytes_sent + h.bytes_recv) / 1024 / 1024);
      const prev = __panelCharts.get('vc-history-chart');
      if (prev) { try { prev.destroy(); } catch (_) {} }
      __panelCharts.set('vc-history-chart', new Chart(canvas, {
        type: 'bar',
        data: { labels, datasets: [{ label: 'MB per call', data: totals, backgroundColor: '#60a5fa' }] },
        options: {
          responsive: true, maintainAspectRatio: false,
          plugins: { legend: { display: false } },
          scales: {
            x: { ticks: { color: '#6b7280', font: { size: 9 } }, grid: { display: false } },
            y: { ticks: { color: '#6b7280' }, grid: { color: '#1f2a40' } },
          },
        },
      }));
    }
  }
}
