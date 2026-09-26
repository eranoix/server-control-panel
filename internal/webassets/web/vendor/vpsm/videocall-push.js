/* vpsm-videocall-push — Web Push subscription helper.
 *
 * Opt-in: the user toggles "Receber chamadas off-app" once. We:
 *   1) request Notification permission,
 *   2) ensure SW is registered (vps-manager already does that for PWA),
 *   3) PushManager.subscribe() with the server's VAPID public key,
 *   4) POST the resulting PushSubscription to /api/videocall/push/subscribe.
 *
 * To opt-out: PushManager.unsubscribe() + POST .../unsubscribe.
 *
 * Listens for SW postMessage `vpsm-vc-accept` (when the user clicks the
 * "Atender" action on a push notification while the panel is already open).
 *
 * Public API: window.VPSMPush = {
 *   isSupported(), getState(token), subscribe(token, onAccept), unsubscribe(token), onAcceptMessage(fn)
 * }
 */
(function () {
  'use strict';
  if (window.VPSMPush) return;

  function isSupported() {
    return (
      'serviceWorker' in navigator &&
      'PushManager' in window &&
      'Notification' in window
    );
  }

  // urlBase64ToUint8Array converts the URL-safe-base64 VAPID public key the
  // server hands back into the Uint8Array PushManager.subscribe expects.
  function urlB64ToBytes(b64) {
    const padding = '='.repeat((4 - b64.length % 4) % 4);
    const base64 = (b64 + padding).replace(/-/g, '+').replace(/_/g, '/');
    const raw = atob(base64);
    const out = new Uint8Array(raw.length);
    for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }

  async function getRegistration() {
    if (!('serviceWorker' in navigator)) throw new Error('SW unsupported');
    const reg = await navigator.serviceWorker.ready;
    return reg;
  }

  async function getState(token) {
    if (!isSupported()) return { supported: false };
    let permission = Notification.permission;
    let subscribed = false;
    try {
      const reg = await getRegistration();
      const sub = await reg.pushManager.getSubscription();
      subscribed = !!sub;
    } catch (_) {}
    return { supported: true, permission, subscribed };
  }

  async function subscribe(token, onAccept) {
    if (!isSupported()) throw new Error('Push notifications não suportadas neste navegador');
    // 1) Permission
    if (Notification.permission === 'default') {
      const r = await Notification.requestPermission();
      if (r !== 'granted') throw new Error('permissão de notificação negada');
    } else if (Notification.permission === 'denied') {
      throw new Error('notificações bloqueadas — habilite nas configurações do navegador');
    }
    // 2) Fetch VAPID public key
    const r = await fetch('/api/videocall/push/public-key', {
      headers: { Authorization: 'Bearer ' + token },
    });
    if (!r.ok) throw new Error('VAPID key indisponível (HTTP ' + r.status + ')');
    const { public_key } = await r.json();
    if (!public_key) throw new Error('VAPID key vazio');
    // 3) Subscribe at the browser
    const reg = await getRegistration();
    let sub = await reg.pushManager.getSubscription();
    if (!sub) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlB64ToBytes(public_key),
      });
    }
    // 4) Send subscription to our server
    // manda também o device_id. Sem esse elo, silenciar "este
    // computador" calaria só o toque in-tab e o push do sistema operacional
    // continuaria pipocando no MESMO aparelho que o dono acabou de silenciar.
    const subJSON = sub.toJSON();
    try {
      const d = window.VPSMDevice ? window.VPSMDevice.info() : null;
      if (d && d.id) subJSON.device_id = d.id;
    } catch (_) {}
    const post = await fetch('/api/videocall/push/subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
      body: JSON.stringify(subJSON),
    });
    if (!post.ok) {
      // Rollback the browser-side sub so the user can retry cleanly.
      try { await sub.unsubscribe(); } catch (_) {}
      throw new Error('falha ao registrar no servidor (HTTP ' + post.status + ')');
    }
    onAcceptMessage(onAccept);
    return true;
  }

  async function unsubscribe(token) {
    if (!isSupported()) return false;
    const reg = await getRegistration();
    const sub = await reg.pushManager.getSubscription();
    if (!sub) return true;
    const endpoint = sub.endpoint;
    try { await sub.unsubscribe(); } catch (_) {}
    try {
      await fetch('/api/videocall/push/unsubscribe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
        body: JSON.stringify({ endpoint }),
      });
    } catch (_) {}
    return true;
  }

  // Hook SW → page messaging. When the user clicks the "Atender" action on
  // a push notification, the SW posts a message to all open clients; we
  // forward it to the app callback so the call auto-opens.
  //
  // Dedup: chamadas múltiplas a onAcceptMessage (reload do init, reconexão)
  // antes acumulavam listeners — callback disparava N vezes. Agora um único
  // listener delega pro callback atual armazenado em `currentAcceptCb`.
  // (Auditoria A2)
  let currentAcceptCb = null;
  let listenerInstalled = false;
  function onAcceptMessage(fn) {
    currentAcceptCb = fn || null;
    if (listenerInstalled || !navigator.serviceWorker) return;
    listenerInstalled = true;
    navigator.serviceWorker.addEventListener('message', (ev) => {
      if (ev.data && ev.data.type === 'vpsm-vc-accept' && ev.data.room_id && currentAcceptCb) {
        try { currentAcceptCb(ev.data.room_id); } catch (_) {}
      }
    });
  }

  window.VPSMPush = {
    isSupported: isSupported,
    getState: getState,
    subscribe: subscribe,
    unsubscribe: unsubscribe,
    onAcceptMessage: onAcceptMessage,
  };
})();
