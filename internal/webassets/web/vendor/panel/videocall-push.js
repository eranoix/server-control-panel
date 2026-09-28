(function () {
  'use strict';
  if (window.PanelPush) return;

  function isSupported() {
    return (
      'serviceWorker' in navigator &&
      'PushManager' in window &&
      'Notification' in window
    );
  }

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
    if (Notification.permission === 'default') {
      const r = await Notification.requestPermission();
      if (r !== 'granted') throw new Error('notification permission denied');
    } else if (Notification.permission === 'denied') {
      throw new Error('notifications are blocked; enable them in the browser settings');
    }
    const r = await fetch('/api/videocall/push/public-key', {
      headers: { Authorization: 'Bearer ' + token },
    });
    if (!r.ok) throw new Error('VAPID key unavailable (HTTP ' + r.status + ')');
    const { public_key } = await r.json();
    if (!public_key) throw new Error('VAPID key is empty');
    const reg = await getRegistration();
    let sub = await reg.pushManager.getSubscription();
    if (!sub) {
      sub = await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: urlB64ToBytes(public_key),
      });
    }
    const subJSON = sub.toJSON();
    try {
      const d = window.PanelDevice ? window.PanelDevice.info() : null;
      if (d && d.id) subJSON.device_id = d.id;
    } catch (_) {}
    const post = await fetch('/api/videocall/push/subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
      body: JSON.stringify(subJSON),
    });
    if (!post.ok) {
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

  let currentAcceptCb = null;
  let listenerInstalled = false;
  function onAcceptMessage(fn) {
    currentAcceptCb = fn || null;
    if (listenerInstalled || !navigator.serviceWorker) return;
    listenerInstalled = true;
    navigator.serviceWorker.addEventListener('message', (ev) => {
      if (ev.data && ev.data.type === 'panel-vc-accept' && ev.data.room_id && currentAcceptCb) {
        try { currentAcceptCb(ev.data.room_id); } catch (_) {}
      }
    });
  }

  window.PanelPush = {
    isSupported: isSupported,
    getState: getState,
    subscribe: subscribe,
    unsubscribe: unsubscribe,
    onAcceptMessage: onAcceptMessage,
  };
})();
