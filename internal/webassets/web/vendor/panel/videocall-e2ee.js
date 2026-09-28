(function () {
  'use strict';
  if (window.PanelVideoCallE2EE) return;

  function isSupported() {
    if (typeof RTCRtpSender === 'undefined' || typeof RTCRtpReceiver === 'undefined') return false;
    const senderProto = RTCRtpSender.prototype;
    const hasInsertable = typeof senderProto.createEncodedStreams === 'function';
    const hasScriptTransform = typeof window.RTCRtpScriptTransform !== 'undefined';
    return hasInsertable || hasScriptTransform;
  }

  async function deriveKey(passphrase, roomId) {
    const enc = new TextEncoder();
    const baseKey = await crypto.subtle.importKey(
      'raw', enc.encode(passphrase), { name: 'PBKDF2' }, false, ['deriveBits']
    );
    const bits = await crypto.subtle.deriveBits({
      name: 'PBKDF2',
      salt: enc.encode('panel-vc:' + (roomId || '')),
      iterations: 200000,
      hash: 'SHA-256',
    }, baseKey, 128);
    return crypto.subtle.importKey('raw', bits, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
  }

  function newSessionId() {
    const b = new Uint8Array(8);
    crypto.getRandomValues(b);
    return b;
  }

  function makeIV(sessionId, counter) {
    const iv = new Uint8Array(12);
    iv.set(sessionId, 0);
    const view = new DataView(iv.buffer);
    view.setUint32(8, counter >>> 0, false);
    return iv;
  }

  async function setup(pc, passphrase, roomId, opts) {
    opts = opts || {};
    if (!isSupported()) {
      throw new Error('Browser does not support frame-level encryption');
    }
    const key = await deriveKey(passphrase, roomId);
    const sessionId = newSessionId();

    const onFail = opts.onDecryptFail || null;
    const senders = pc.getSenders();
    const receivers = pc.getReceivers();
    for (const sender of senders) {
      if (!sender.track) continue;
      hookEnd(sender, key, sessionId, 'sender', onFail);
    }
    for (const receiver of receivers) {
      if (!receiver.track) continue;
      hookEnd(receiver, key, sessionId, 'receiver', onFail);
    }
    pc.addEventListener('track', (ev) => {
      const rec = ev.receiver;
      hookEnd(rec, key, sessionId, 'receiver', onFail);
    });
    return { ok: true };
  }

  function hookEnd(endpoint, key, sessionId, role, onDecryptFail) {
    if (endpoint.__panelE2EEHooked) return;
    endpoint.__panelE2EEHooked = true;
    if (typeof endpoint.createEncodedStreams === 'function') {
      const streams = endpoint.createEncodedStreams();
      let counter = 0;
      let decryptFailures = 0;
      let lastWarn = 0;
      const transformer = new TransformStream({
        transform: async (frame, controller) => {
          try {
            if (role === 'sender') {
              const data = new Uint8Array(frame.data);
              const iv = makeIV(sessionId, counter);
              const header = new Uint8Array([0]);
              const ct = await crypto.subtle.encrypt(
                { name: 'AES-GCM', iv: iv, additionalData: header },
                key, data
              );
              const out = new Uint8Array(1 + 8 + 4 + ct.byteLength);
              out[0] = header[0];
              out.set(sessionId, 1);
              new DataView(out.buffer).setUint32(9, counter >>> 0, false);
              out.set(new Uint8Array(ct), 13);
              frame.data = out.buffer;
              counter = (counter + 1) >>> 0;
            } else {
              const buf = new Uint8Array(frame.data);
              if (buf.length < 14) {  return; }
              const header = buf.subarray(0, 1);
              const sid = buf.subarray(1, 9);
              const ctr = new DataView(buf.buffer, buf.byteOffset + 9, 4).getUint32(0, false);
              const ct = buf.subarray(13);
              const iv = makeIV(sid, ctr);
              const pt = await crypto.subtle.decrypt(
                { name: 'AES-GCM', iv: iv, additionalData: header },
                key, ct
              );
              frame.data = pt;
            }
            controller.enqueue(frame);
          } catch (e) {
            if (role === 'receiver' && onDecryptFail) {
              decryptFailures++;
              const now = Date.now();
              if (decryptFailures > 30 && now - lastWarn > 5000) {
                lastWarn = now;
                try { onDecryptFail(decryptFailures); } catch (_) {}
              }
            }
          }
        }
      });
      streams.readable.pipeThrough(transformer).pipeTo(streams.writable).catch(() => {});
      return;
    }
  }

  window.PanelVideoCallE2EE = {
    isSupported: isSupported,
    setup: setup,
  };
})();
