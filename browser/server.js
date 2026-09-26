// VPS Manager — Navegador tunelado (Ultraviolet + Wisp)
// Servidor Node isolado, escuta apenas em 127.0.0.1 e é exposto pelo painel
// (Go) sob /browser/ com autenticação. NÃO deve ser exposto direto à internet.
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import express from "express";
import { server as wisp } from "@mercuryworkshop/wisp-js/server";
import { uvPath } from "@titaniumnetwork-dev/ultraviolet";
import { publicPath } from "ultraviolet-static";
import { baremuxPath } from "@mercuryworkshop/bare-mux/node";

const __dirname = dirname(fileURLToPath(import.meta.url));
const overlayPath = join(__dirname, "public");
const epoxyPath = join(__dirname, "node_modules", "@mercuryworkshop", "epoxy-transport", "dist");

const HOST = process.env.HOST || "127.0.0.1";
const PORT = parseInt(process.env.PORT || "8090", 10);

// --- Hardening do Wisp: impede usar o proxy para alcançar a rede interna da VPS
// (painel, pooler do Supabase, docker, metadata, etc.). O navegador só sai pra internet pública.
Object.assign(wisp.options, {
  allow_private_ips: false,
  allow_loopback_ips: false,
  allow_udp_streams: true,
  allow_tcp_streams: true,
  hostname_blacklist: [
    /^localhost$/i,
    /\.internal$/i,
    /(^|\.)panel\.northwind\.example$/i,
  ],
  // stream_limit_per_host fica desativado (-1): há um bug em wisp-js@0.4.1 que
  // faz `for...of` sobre connection.streams (objeto, não iterável) e derruba o
  // processo. O limite total abaixo usa Object.keys e funciona normalmente.
  stream_limit_per_host: -1,
  stream_limit_total: 512,
  wisp_motd: "vpsm-browser",
});

const app = express();
app.disable("x-powered-by");

// Healthcheck para o painel monitorar o serviço.
app.get("/healthz", (_req, res) => res.json({ ok: true, service: "vpsm-browser" }));

// Counters de banda (KB) por aba+pane, expostos via /api/bandwidth. Atualizado
// no upgrade handler do wisp envolvendo o socket pra somar bytes trafegados.
const bwCounters = { totalSent: 0, totalRecv: 0, perOrigin: {} };
app.get("/api/bandwidth", (_req, res) => res.json(bwCounters));

// ---- Camada 1: Challenge solver (Byparr) ----
// Quando UV pega 403 por anti-bot (Akamai/Cloudflare/DataDome), o cliente
// chama este endpoint passando a URL. Byparr (Camoufox Firefox real, rodando
// em 127.0.0.1:8191) navega, resolve o challenge, e retorna cookies+HTML.
// Cliente injeta os cookies na sessão do UV e re-tenta a navegação.
// Tudo o overhead (banda do challenge ~10-30MB) fica na VPS — pro browser
// no PC do usuário só chega o HTML final (~50-500KB, igual UV cru).
const BYPARR_URL = process.env.BYPARR_URL || "http://127.0.0.1:8191/v1";
// Byparr aguarda 'networkidle' por padrão; alguns sites com RUM/trackers nunca
// atingem networkidle, então damos 90s antes de desistir. O Camoufox geralmente
// já tem cookies de challenge resolvidos bem antes (~5-15s) — o tempo extra
// cobre o pior caso. Cliente vê spinner com mensagem de progresso.
const SOLVER_TIMEOUT_MS = 90000;

app.post("/api/solve", express.json({ limit: "32kb" }), async (req, res) => {
  const url = (req.body && req.body.url || "").toString().trim();
  if (!/^https?:\/\//i.test(url)) {
    return res.status(400).json({ error: "url inválida" });
  }
  const ctl = new AbortController();
  const to = setTimeout(() => ctl.abort(), SOLVER_TIMEOUT_MS + 5000);
  try {
    const r = await fetch(BYPARR_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ cmd: "request.get", url, maxTimeout: SOLVER_TIMEOUT_MS }),
      signal: ctl.signal,
    });
    clearTimeout(to);
    if (!r.ok) {
      return res.status(502).json({ error: "solver erro " + r.status });
    }
    const data = await r.json();
    if (data.status !== "ok") {
      return res.status(502).json({ error: data.message || "solver falhou", raw: data });
    }
    // Resposta enxuta pro cliente: só o necessário pra injetar cookies + UA.
    const sol = data.solution || {};
    res.json({
      url: sol.url || url,
      status: sol.status,
      userAgent: sol.userAgent || "",
      cookies: (sol.cookies || []).map(c => ({
        name: c.name, value: c.value, domain: c.domain,
        path: c.path || "/", secure: !!c.secure, httpOnly: !!c.httpOnly,
        sameSite: c.sameSite || null, expires: c.expires || null,
      })),
      // HTML não é enviado por padrão (pode ser pesado); cliente pede com ?withHtml=1.
      html: req.query.withHtml === "1" ? (sol.response || "") : undefined,
      htmlSize: (sol.response || "").length,
      tookMs: data.endTimestamp && data.startTimestamp ? (data.endTimestamp - data.startTimestamp) : null,
    });
  } catch (e) {
    clearTimeout(to);
    res.status(502).json({ error: "solver inalcançável: " + (e.message || e) });
  }
});

// Overlay (nosso index/branding) tem precedência sobre o static padrão do UV.
// no-cache em register-sw.js + index.html: esses arquivos coordenam a versão
// do Service Worker (?v=N). Se o browser servir do cache, o user fica preso
// num SW velho com CSP/headers obsoletos mesmo após deploy do server. O resto
// do static (uv.bundle.js, etc — paths com hash de versão da lib) pode cachear.
app.use((req, res, next) => {
  if (req.path === "/" || req.path === "/index.html" || req.path === "/register-sw.js") {
    res.set("Cache-Control", "no-cache, no-store, must-revalidate");
  }
  next();
});
app.use(express.static(overlayPath, { fallthrough: true }));
app.use(express.static(publicPath, { fallthrough: true }));
app.use("/uv/", express.static(uvPath, { fallthrough: true }));
app.use("/epoxy/", express.static(epoxyPath, { fallthrough: true }));
app.use("/baremux/", express.static(baremuxPath, { fallthrough: true }));

const server = createServer();
server.on("request", app);
server.on("upgrade", (req, socket, head) => {
  if (req.url.endsWith("/wisp/")) {
    wisp.routeRequest(req, socket, head);
  } else {
    socket.end();
  }
});

server.listen(PORT, HOST, () => {
  console.log(`[vpsm-browser] Ultraviolet+Wisp em http://${HOST}:${PORT} (wisp em /wisp/)`);
});

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => server.close(() => process.exit(0)));
}
