"use strict";
/**
 * Server Control Panel — service worker registration + Wisp transport (via Epoxy).
 * Replaces ultraviolet-static's default register-sw.js to configure the epoxy
 * transport pointing at the Wisp endpoint served by this host.
 *
 * SW_VERSION: bump it when anything in the CSP/headers served by Go changes — the
 * Service Worker inherits the CSP of the response for its own .js file at install
 * time and KEEPS that CSP until it is unregistered. Changing the URL via
 * ?v=<n> forces the browser to treat it as a new SW and reinstall it (picking up
 * the current response/CSP). 2 = the CSP gained `blob:` in connect-src (2026-06-12).
 */
const SW_VERSION = 2;
const stockSW = "/browser/uv/sw.js?v=" + SW_VERSION;
const swAllowedHostnames = ["localhost", "127.0.0.1"];

// Wisp endpoint relative to the current origin (same origin as the panel, under /browser/).
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

  // Make sure the epoxy -> wisp transport is set before navigating.
  if ((await bareMux.getTransport()) !== "/browser/epoxy/index.mjs") {
    await bareMux.setTransport("/browser/epoxy/index.mjs", [{ wisp: wispUrl }]);
  }

  // Clear old SWs in the scope before registering the current one. Without this, a SW
  // previously registered with the URL "/browser/uv/sw.js" (no ?v=) stays
  // active in the same scope with the CSP it inherited at install — and the browser
  // keeps applying that old CSP even after new responses from the server.
  // The new SW (?v=N) only "wins" after the next .register(); we call
  // unregister() first to make sure the scope is clean.
  try {
    const regs = await navigator.serviceWorker.getRegistrations();
    for (const r of regs) {
      const sw = r.active || r.waiting || r.installing;
      const url = sw && sw.scriptURL ? sw.scriptURL : "";
      // Kill any SW in the UV scope whose URL is NOT the current version.
      // Loose match (contains /browser/uv/sw.js) to cover both the URL
      // without ?v= and older versions (?v=1, etc).
      if (url.includes("/browser/uv/sw.js") && !url.endsWith(stockSW)) {
        await r.unregister();
      }
    }
  } catch (_) {}

  // Clear the old SW's caches. Without this, the response cached in there
  // still carries old headers (CSP without blob:, etc), and the browser applies
  // the cached CSP when the iframe loads through the new SW.
  try {
    if (typeof caches !== "undefined") {
      const keys = await caches.keys();
      await Promise.all(keys.map((k) => caches.delete(k)));
    }
  } catch (_) {}

  const reg = await navigator.serviceWorker.register(stockSW, { scope: __uv$config.prefix });
  // Force an update check — if the .js bytes changed, it installs. Covers the case
  // of a browser cache serving the old SW despite the reload.
  try { await reg.update(); } catch (_) {}
}

// Auto-register the SW as soon as the landing page loads (it used to depend on the
// form submit). Without this, navigating via the panel's address bar — or restoring a
// saved tab pointing at /browser/uv/service/<enc> — gives a 404 because the request
// reaches the server before the SW can intercept it.
const swReadyPromise = registerSW()
  .then(() => {
    try { window.parent.postMessage({ type: "panel-browser-ready" }, location.origin); } catch (_) {}
  })
  .catch((err) => {
    console.error("panel-browser: SW register failed", err);
    try { window.parent.postMessage({ type: "panel-browser-error", message: String(err) }, location.origin); } catch (_) {}
  });

// Listen for navigation requests from the parent panel. Wait for the SW to be ready
// before changing location.href, avoiding the race that caused a 404.
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
