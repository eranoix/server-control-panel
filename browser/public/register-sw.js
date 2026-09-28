"use strict";
const SW_VERSION = 2;
const stockSW = "/browser/uv/sw.js?v=" + SW_VERSION;
const swAllowedHostnames = ["localhost", "127.0.0.1"];

const wispUrl =
  (location.protocol === "https:" ? "wss" : "ws") + "://" + location.host + "/browser/wisp/";

const bareMux = new BareMux.BareMuxConnection("/browser/baremux/worker.js");

async function registerSW() {
  if (!navigator.serviceWorker) {
    if (
      location.protocol !== "https:" &&
      !swAllowedHostnames.includes(location.hostname)
    )
      throw new Error("Service workers require HTTPS.");
    throw new Error("Your browser does not support service workers.");
  }

  if ((await bareMux.getTransport()) !== "/browser/epoxy/index.mjs") {
    await bareMux.setTransport("/browser/epoxy/index.mjs", [{ wisp: wispUrl }]);
  }

  try {
    const regs = await navigator.serviceWorker.getRegistrations();
    for (const r of regs) {
      const sw = r.active || r.waiting || r.installing;
      const url = sw && sw.scriptURL ? sw.scriptURL : "";
      if (url.includes("/browser/uv/sw.js") && !url.endsWith(stockSW)) {
        await r.unregister();
      }
    }
  } catch (_) {}

  try {
    if (typeof caches !== "undefined") {
      const keys = await caches.keys();
      await Promise.all(keys.map((k) => caches.delete(k)));
    }
  } catch (_) {}

  const reg = await navigator.serviceWorker.register(stockSW, { scope: __uv$config.prefix });
  try { await reg.update(); } catch (_) {}
}

const swReadyPromise = registerSW()
  .then(() => {
    try { window.parent.postMessage({ type: "panel-browser-ready" }, location.origin); } catch (_) {}
  })
  .catch((err) => {
    console.error("panel-browser: SW register failed", err);
    try { window.parent.postMessage({ type: "panel-browser-error", message: String(err) }, location.origin); } catch (_) {}
  });

window.addEventListener("message", async (ev) => {
  if (ev.origin !== location.origin) return;
  const d = ev.data || {};
  if (d.type !== "panel-browser-navigate") return;
  try { await swReadyPromise; } catch (_) {}
  if (typeof d.encoded === "string" && d.encoded.length) {
    const prefix = (typeof __uv$config !== "undefined" && __uv$config && __uv$config.prefix) || "/browser/uv/service/";
    location.href = prefix + d.encoded;
  }
});
