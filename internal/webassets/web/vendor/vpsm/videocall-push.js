/* vpsm-videocall-push — Web Push subscription helper.
 *
 * Opt-in: the user turns on the off-app calls toggle once. We:
 *   1) request Notification permission,
 *   2) ensure SW is registered (vps-manager already does that for PWA),
 *   3) PushManager.subscribe() with the server's VAPID public key,
 *   4) POST the resulting PushSubscription to /api/videocall/push/subscribe.
 *
 * To opt-out: PushManager.unsubscribe() + POST .../unsubscribe.
 *
 * Listens for SW postMessage `vpsm-vc-accept` (when the user clicks the
 * answer action on a push notification while the panel is already open).
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
    if (!isSupported()) throw new Error('Push notifications are not supported in this browser');
    // 1) Permission
    if (Notification.permission === 'default') {
      const r = await Notification.requestPermission();
      if (r !== 'granted') throw new Error('notification permission denied');
    } else if (Notification.permission === 'denied') {
      throw new Error('notifications are blocked; enable them in the browser settings');
    }
    // 2) Fetch VAPID public key
    const r = await fetch('/api/videocall/push/public-key', {
      headers: { Authorization: 'Bearer ' + token },
    });
    if (!r.ok) throw new Error('VAPID key unavailable (HTTP ' + r.status + ')');
    const { public_key } = await r.json();
    if (!public_key) throw new Error('VAPID key is empty');
    // 3) Subscribe at the browser
    const reg = await getRegistration();
    let sub = await reg.pushManager.getSubscription();
    if (!sub) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlB64ToBytes(public_key),
      });
    }
    // 4) Send subscription to our server, with the device_id: without it,
    // muting "this computer" would silence only the in-tab ring and the OS
    // push would keep firing on the device the owner just muted.
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
      throw new Error('failed to register with the server (HTTP ' + post.status + ')');
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

  // Hook SW → page messaging. When the user clicks the answer action on
  // a push notification, the SW posts a message to all open clients; we
  // forward it to the app callback so the call auto-opens.
  //
  // onAcceptMessage may be called many times (init reload, reconnect): a single
  // listener delegates to `currentAcceptCb` so the callback never fires N times.
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
