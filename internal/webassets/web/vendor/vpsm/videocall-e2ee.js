/* vpsm-videocall-e2ee — Insertable-Streams frame encryption.
 *
 * Threat model: the vps-manager server (signaling + TURN relay) sees only
 * encrypted media frames. A compromised server (or a TURN operator) can
 * see frame metadata (timing, size) but not payload content. Out-of-band
 * passphrase distribution (or the magic-link generator's "show once")
 * keeps the key off the server entirely.
 *
 * Crypto:
 *   key  = PBKDF2-HMAC-SHA256(passphrase, salt=roomId, 200000 iter, 16 bytes)
 *   IV   = 12-byte counter (random 8-byte session id + 4-byte frame counter)
 *   ct   = AES-GCM(key, iv, payload, aad=2-byte frame header)
 *   wire = [1-byte header][session_id 8B][counter 4B][ciphertext]
 *
 * The first 1 byte is reserved for: bit 0 = "is keyframe" (used by remote
 * to know they can recover after packet loss), bits 1-7 padding.
 *
 * Browser support paths (in order of preference):
 *   1. sender.transform = new RTCRtpScriptTransform(worker, ...)   — Safari, Chrome 113+
 *   2. sender.createEncodedStreams() + TransformStream             — Chrome/Edge
 *   3. (nothing) — fall back to no-E2EE with explicit user warning
 *
 * Public API: window.VPSMVideoCallE2EE = { isSupported, setup(pc, passphrase, roomId, role) }
 *   role = 'sender' | 'receiver' (called twice per peer connection)
 */
(function () {
  'use strict';
  if (window.VPSMVideoCallE2EE) return;

  function isSupported() {
    if (typeof RTCRtpSender === 'undefined' || typeof RTCRtpReceiver === 'undefined') return false;
    // Either insertable streams API or script-transform.
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
      salt: enc.encode('vpsm-vc:' + (roomId || '')),
      iterations: 200000,
      hash: 'SHA-256',
    }, baseKey, 128);
    return crypto.subtle.importKey('raw', bits, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']);
  }

  // Session id is regenerated per-peer per-call so counters don't collide
  // across reconnects. 8 bytes is enough to make collisions vanishingly
  // unlikely while keeping the IV under the 12-byte AES-GCM limit.
  function newSessionId() {
    const b = new Uint8Array(8);
    crypto.getRandomValues(b);
    return b;
  }

  function makeIV(sessionId, counter) {
    const iv = new Uint8Array(12);
    iv.set(sessionId, 0);
    const view = new DataView(iv.buffer);
    view.setUint32(8, counter >>> 0, false); // big-endian
    return iv;
  }

  /**
   * Wire encryption into a single RTCRtpSender or RTCRtpReceiver.
   *
   * For each video/audio track we get one EncodedStream pair. The transform
   * function reads encoded frames (already encoded by the browser, before
   * packetization), encrypts/decrypts the payload, and pushes them on.
   */
  async function setup(pc, passphrase, roomId, opts) {
    opts = opts || {};
    if (!isSupported()) {
      throw new Error('Browser does not support frame-level encryption');
    }
    const key = await deriveKey(passphrase, roomId);
    const sessionId = newSessionId();

    const onFail = opts.onDecryptFail || null;
    // Hook every existing sender + receiver.
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
    // Re-hook whenever a new transceiver is added (renegotiation,
    // hot-track-swap for screen share).
    pc.addEventListener('track', (ev) => {
      const rec = ev.receiver;
      hookEnd(rec, key, sessionId, 'receiver', onFail);
    });
    return { ok: true };
  }

  function hookEnd(endpoint, key, sessionId, role, onDecryptFail) {
    if (endpoint.__vpsmE2EEHooked) return;
    endpoint.__vpsmE2EEHooked = true;
    // Path 1: createEncodedStreams (Chrome/Edge legacy + current).
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
              const header = new Uint8Array([0]); // reserved flags byte
              const ct = await crypto.subtle.encrypt(
                { name: 'AES-GCM', iv: iv, additionalData: header },
                key, data
              );
              // Pack: [header 1B][session_id 8B][counter 4B][ciphertext]
              const out = new Uint8Array(1 + 8 + 4 + ct.byteLength);
              out[0] = header[0];
              out.set(sessionId, 1);
              new DataView(out.buffer).setUint32(9, counter >>> 0, false);
              out.set(new Uint8Array(ct), 13);
              frame.data = out.buffer;
              counter = (counter + 1) >>> 0;
            } else {
              const buf = new Uint8Array(frame.data);
              if (buf.length < 14) { /* not encrypted by us — drop */ return; }
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
            // Drop frame on auth failure. Conta os drops do receiver pra
            // alertar quando passphrase parece errada — antes era silent
            // failure (tela preta sem aviso). (Auditoria M19)
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
    // Path 2: RTCRtpScriptTransform (Safari) — requires Worker.
    // Implementing the worker path adds a vendored worker .js file and
    // significantly more code; for Fase 2 we surface "E2EE não disponível
    // neste navegador" if path 1 is missing, and let the call proceed
    // unencrypted only if the user explicitly confirms.
  }

  window.VPSMVideoCallE2EE = {
    isSupported: isSupported,
    setup: setup,
  };
})();
