package webassets

import (
	"fmt"
	"net/http"
)

// HandleServiceWorker serves an SW with strategic caching of the app shell
// (HTML + vendor JS + CSS) plus network-only for /api/ and /ws/. Offline it
// serves the cached shell plus a fallback. It is what makes "Add to Home
// Screen" and real offline operation possible. The cache version changes with
// every build (ProcessStartTime), forcing the SW to update.
func HandleServiceWorker(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	cacheVer := fmt.Sprintf("%d", ProcessStartTime.Unix())
	sw := `// server-control-panel Service Worker — cache app-shell, network-only /api/ + /ws/.
const CACHE = 'panel-shell-v` + cacheVer + `';
const SHELL = [
  '/',
  '/vendor/alpine/alpine.min.js',
  // A refactor extracted the giant inline <script> out into 00-shell.js.
  // WITHOUT this precache, the first offline navigation loads index.html but
  // the app() definition is empty -> Alpine boots and crashes on x-data="app()".
  '/vendor/panel/app/00-shell.js',
  // The 10/20/30 IIFEs are spread over the same app() object; without them in
  // the precache, a cold offline navigation loads the shell but breaks on a
  // missing method (the same class of crash the comment above prevents).
  '/vendor/panel/app/10-git.js',
  '/vendor/panel/app/20-deploy.js',
  '/vendor/panel/app/30-agents.js',
  // Without 40/41 in the precache, a cold/offline load (or a deploy restart)
  // loses PanelProxmoxModule and the first pvxStaleStyle(...) crashes the UI.
  '/vendor/panel/app/40-nodes.js',
  '/vendor/panel/app/41-proxmox.js',
  '/vendor/xterm/xterm.js', '/vendor/xterm/xterm.css',
  '/vendor/xterm/fit.js', '/vendor/xterm/web-links.js', '/vendor/xterm/unicode11.js',
  '/tailwind.css',
  '/manifest.webmanifest',
  '/icon-192.png', '/icon-512.png',
];

self.addEventListener('install', (e) => {
  e.waitUntil((async () => {
    const c = await caches.open(CACHE);
    await Promise.all(SHELL.map(u => c.add(u).catch(() => null)));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (e) => {
  e.waitUntil((async () => {
    const ks = await caches.keys();
    await Promise.all(ks.filter(k => k !== CACHE).map(k => caches.delete(k)));
    await self.clients.claim();
  })());
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/') ||
      url.pathname.startsWith('/browser/') || url.pathname.startsWith('/browser-persistent/') ||
      url.pathname.startsWith('/grafana/') || url.pathname.startsWith('/metrics') ||
      url.pathname.startsWith('/_docs') || url.pathname.startsWith('/_graph') || url.pathname.startsWith('/recovery')) {
    // Backend routes / proxied services (Grafana, metrics, docs, recovery):
    // the SW must NOT intercept — otherwise its own FetchEvent "resolves with a
    // network error" for navigations of those apps (see console: Grafana dashboard).
    return;
  }
  if (req.mode === 'navigate' || (req.headers.get('accept') || '').includes('text/html')) {
    e.respondWith((async () => {
      try {
        const fresh = await fetch(req);
        const c = await caches.open(CACHE);
        if (fresh && fresh.ok) c.put(req, fresh.clone());
        return fresh;
      } catch (_) {
        const cached = await caches.match(req);
        return cached || caches.match('/');
      }
    })());
    return;
  }
  const isAppAsset =
    url.pathname.startsWith('/vendor/panel/') ||
    url.pathname === '/tailwind.css';
  if (isAppAsset) {
    e.respondWith((async () => {
      try {
        const fresh = await fetch(req);
        if (fresh && fresh.ok) {
          const c = await caches.open(CACHE);
          c.put(req, fresh.clone());
        }
        return fresh;
      } catch (_) {
        // ignoreSearch: the precache stores the URL without ?v=<build>, the HTML
        // asks for it with. Without this the match fails and the precache is dead weight.
        return (await caches.match(req, { ignoreSearch: true })) || caches.match(req);
      }
    })());
    return;
  }
  e.respondWith((async () => {
    const cached = await caches.match(req);
    if (cached) return cached;
    try {
      const fresh = await fetch(req);
      if (fresh && fresh.ok && (url.pathname.startsWith('/vendor/') ||
          url.pathname.endsWith('.css') || url.pathname.endsWith('.js') ||
          url.pathname.endsWith('.png') || url.pathname.endsWith('.webmanifest'))) {
        const c = await caches.open(CACHE);
        c.put(req, fresh.clone());
      }
      return fresh;
    } catch (e) { return Response.error(); }
  })());
});

function safeStr(s, max) {
  return String(s || '').slice(0, max || 80).replace(/[\x00-\x1f]/g, "");
}

self.addEventListener('push', (event) => {
  let data = {};
  try { data = event.data ? event.data.json() : {}; } catch (_) {}
  if (data.type === 'incoming-call') {
    const title = '📞 Call from ' + safeStr(data.from, 60);
    const opts = {
      body: 'Room: ' + safeStr(data.room_name || data.room_id, 80),
      icon: '/icon-192.png',
      badge: '/icon-192.png',
      tag: 'panel-vc-' + safeStr(data.room_id, 60),
      renotify: true,
      requireInteraction: true,
      vibrate: [400, 200, 400, 200, 400],
      actions: [
        { action: 'accept', title: 'Answer' },
        { action: 'dismiss', title: 'Decline' },
      ],
      data: { type: 'incoming-call', room_id: safeStr(data.room_id, 60), room_name: safeStr(data.room_name, 80), from: safeStr(data.from, 60) },
    };
    event.waitUntil(self.registration.showNotification(title, opts));
    return;
  }
  if (data.type === 'alert-fired') {
    const title = '⚠ Alert: ' + safeStr(data.rule || 'rule', 60);
    const opts = {
      body: safeStr(data.body || (data.metric + ' ' + data.op + ' ' + data.value), 200),
      icon: '/icon-192.png',
      badge: '/icon-192.png',
      tag: 'panel-alert-' + safeStr(data.rule, 60),
      renotify: true,
      data: { type: 'alert', rule: safeStr(data.rule, 60), url: safeStr(data.url || '/#alerts', 200) },
    };
    event.waitUntil(self.registration.showNotification(title, opts));
    return;
  }
  if (data.title) {
    event.waitUntil(self.registration.showNotification(safeStr(data.title, 80), {
      body: safeStr(data.body, 200),
      icon: '/icon-192.png', badge: '/icon-192.png',
      tag: safeStr(data.tag, 60) || 'panel-generic',
      data: { type: 'generic', url: safeStr(data.url || '/', 200) },
    }));
  }
});

self.addEventListener('pushsubscriptionchange', (event) => {
  event.waitUntil((async () => {
    try {
      const reg = await self.registration;
      const oldEndpoint = (event.oldSubscription && event.oldSubscription.endpoint) || '';
      const clientsList = await self.clients.matchAll({ includeUncontrolled: true });
      clientsList.forEach(c => c.postMessage({ type: 'panel-push-resubscribe', oldEndpoint }));
    } catch(_){}
  })());
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  if (event.action === 'dismiss') return;
  const d = event.notification.data || {};
  event.waitUntil((async () => {
    const allClients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
    let url;
    if (d.type === 'incoming-call') {
      url = '/#videocall=accept&room=' + encodeURIComponent(d.room_id || '');
    } else if (d.url) {
      url = d.url;
    } else if (d.type === 'alert') {
      url = '/#alerts';
    } else {
      url = '/';
    }
    for (const c of allClients) {
      try {
        if (d.type === 'incoming-call') c.postMessage({ type: 'panel-vc-accept', room_id: d.room_id || '' });
        return c.focus();
      } catch (_) {}
    }
    return self.clients.openWindow(url);
  })());
});
`
	_, _ = w.Write([]byte(sw))
}
