"use strict";
/**
 * VPS Manager — registro do service worker + transporte Wisp (via Epoxy).
 * Substitui o register-sw.js padrão do ultraviolet-static para configurar
 * o transporte epoxy apontando para o endpoint Wisp servido por este host.
 *
 * SW_VERSION: bump quando algo do CSP/headers servidos pelo Go mudar — o
 * Service Worker herda o CSP do response do próprio arquivo .js no momento
 * do install e fica RETIDO com esse CSP até ser unregistered. Mudar a URL
 * via ?v=<n> força o browser a tratar como SW novo e reinstalar (pegando
 * o response/CSP atual). 2 = CSP ganhou `blob:` em connect-src (12/06/2026).
 */
const SW_VERSION = 2;
const stockSW = "/browser/uv/sw.js?v=" + SW_VERSION;
const swAllowedHostnames = ["localhost", "127.0.0.1"];

// Endpoint Wisp relativo à origem atual (mesma origem do painel, sob /browser/).
const wispUrl =
  (location.protocol === "https:" ? "wss" : "ws") + "://" + location.host + "/browser/wisp/";

const bareMux = new BareMux.BareMuxConnection("/browser/baremux/worker.js");

async function registerSW() {
  if (!navigator.serviceWorker) {
    if (
      location.protocol !== "https:" &&
      !swAllowedHostnames.includes(location.hostname)
    )
      throw new Error("Service workers exigem HTTPS.");
    throw new Error("Seu navegador não suporta service workers.");
  }

  // Garante o transporte epoxy -> wisp antes de navegar.
  if ((await bareMux.getTransport()) !== "/browser/epoxy/index.mjs") {
    await bareMux.setTransport("/browser/epoxy/index.mjs", [{ wisp: wispUrl }]);
  }

  // Limpa SWs antigos no scope antes de registrar o atual. Sem isso, um SW
  // anteriormente registrado com URL "/browser/uv/sw.js" (sem ?v=) fica
  // ativo no mesmo scope com o CSP que herdou no install — e o browser
  // continua aplicando esse CSP velho mesmo após response novos do server.
  // O SW novo (?v=N) só "wins" depois do .register() seguinte; chamamos
  // unregister() primeiro pra garantir que o scope esteja limpo.
  try {
    const regs = await navigator.serviceWorker.getRegistrations();
    for (const r of regs) {
      const sw = r.active || r.waiting || r.installing;
      const url = sw && sw.scriptURL ? sw.scriptURL : "";
      // Mata qualquer SW no scope UV cuja URL NÃO seja a versão atual.
      // Match liberal (contains /browser/uv/sw.js) pra cobrir tanto a URL
      // sem ?v= quanto versões antigas (?v=1, etc).
      if (url.includes("/browser/uv/sw.js") && !url.endsWith(stockSW)) {
        await r.unregister();
      }
    }
  } catch (_) {}

  // Limpa caches do SW antigo. Sem isso, o response cacheado lá dentro
  // ainda traz headers velhos (CSP sem blob:, etc), e o browser aplica
  // o CSP do cache quando o iframe carrega via SW novo.
  try {
    if (typeof caches !== "undefined") {
      const keys = await caches.keys();
      await Promise.all(keys.map((k) => caches.delete(k)));
    }
  } catch (_) {}

  const reg = await navigator.serviceWorker.register(stockSW, { scope: __uv$config.prefix });
  // Força check de update — se o byte do .js mudou, instala. Cobre o caso
  // de cache do browser que serve o SW antigo apesar do reload.
  try { await reg.update(); } catch (_) {}
}

// Auto-registra o SW assim que o landing carrega (antes dependia do submit do
// form). Sem isso, navegar via address bar do painel — ou restaurar uma aba
// salva apontando para /browser/uv/service/<enc> — dá 404 porque o request
// chega ao servidor antes do SW interceptar.
const swReadyPromise = registerSW()
  .then(() => {
    try { window.parent.postMessage({ type: "vpsm-browser-ready" }, location.origin); } catch (_) {}
  })
  .catch((err) => {
    console.error("vpsm-browser: SW register failed", err);
    try { window.parent.postMessage({ type: "vpsm-browser-error", message: String(err) }, location.origin); } catch (_) {}
  });

// Ouve pedidos de navegação vindos do painel pai. Aguarda o SW pronto antes
// de mudar location.href, evitando a corrida que causava 404.
window.addEventListener("message", async (ev) => {
  if (ev.origin !== location.origin) return;
  const d = ev.data || {};
  if (d.type !== "vpsm-browser-navigate") return;
  try { await swReadyPromise; } catch (_) {}
  if (typeof d.encoded === "string" && d.encoded.length) {
    const prefix = (typeof __uv$config !== "undefined" && __uv$config && __uv$config.prefix) || "/browser/uv/service/";
    location.href = prefix + d.encoded;
  }
});
