// Server Control Panel — tunnelled browser (Ultraviolet + Wisp)
// Isolated Node server: it listens on 127.0.0.1 only and is exposed by the panel
// (Go) under /browser/ behind authentication. It must NOT be exposed directly to the internet.
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

// --- Wisp hardening: stops the proxy from being used to reach the VPS's internal network
// (panel, Supabase pooler, docker, metadata, etc.). The browser only goes out to the public internet.
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
  // stream_limit_per_host stays disabled (-1): wisp-js@0.4.1 has a bug that
  // runs `for...of` over connection.streams (an object, not iterable) and kills the
  // process. The total limit below uses Object.keys and works normally.
  stream_limit_per_host: -1,
  stream_limit_total: 512,
  wisp_motd: "panel-browser",
});

const app = express();
app.disable("x-powered-by");

// Healthcheck so the panel can monitor the service.
app.get("/healthz", (_req, res) => res.json({ ok: true, service: "panel-browser" }));

// Bandwidth counters (KB) per tab+pane, exposed via /api/bandwidth. Updated
// in the wisp upgrade handler by wrapping the socket to add up the bytes carried.
const bwCounters = { totalSent: 0, totalRecv: 0, perOrigin: {} };
app.get("/api/bandwidth", (_req, res) => res.json(bwCounters));

// ---- Layer 1: challenge solver (Byparr) ----
// When UV gets a 403 from an anti-bot (Akamai/Cloudflare/DataDome), the client
// calls this endpoint with the URL. Byparr (a real Camoufox Firefox, running
// on 127.0.0.1:8191) navigates, solves the challenge and returns cookies+HTML.
// The client injects the cookies into the UV session and retries the navigation.
// All the overhead (challenge bandwidth ~10-30MB) stays on the VPS — the browser
// on the user's PC only receives the final HTML (~50-500KB, same as plain UV).
const BYPARR_URL = process.env.BYPARR_URL || "http://127.0.0.1:8191/v1";
// Byparr waits for 'networkidle' by default; some sites with RUM/trackers never
// reach networkidle, so we allow 90s before giving up. Camoufox usually has the
// challenge cookies solved well before that (~5-15s) — the extra time covers
// the worst case. The client sees a spinner with a progress message.
const SOLVER_TIMEOUT_MS = 90000;

app.post("/api/solve", express.json({ limit: "32kb" }), async (req, res) => {
  const url = (req.body && req.body.url || "").toString().trim();
  if (!/^https?:\/\//i.test(url)) {
    return res.status(400).json({ error: "invalid url" });
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
      return res.status(502).json({ error: "solver error " + r.status });
    }
    const data = await r.json();
    if (data.status !== "ok") {
      return res.status(502).json({ error: data.message || "solver failed", raw: data });
    }
    // Lean response for the client: only what it needs to inject cookies + UA.
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
      // HTML is not sent by default (it can be heavy); the client asks with ?withHtml=1.
      html: req.query.withHtml === "1" ? (sol.response || "") : undefined,
      htmlSize: (sol.response || "").length,
      tookMs: data.endTimestamp && data.startTimestamp ? (data.endTimestamp - data.startTimestamp) : null,
    });
  } catch (e) {
    clearTimeout(to);
    res.status(502).json({ error: "solver unreachable: " + (e.message || e) });
  }
});

// The overlay (our index/branding) takes precedence over UV's default static files.
// no-cache on register-sw.js + index.html: these files coordinate the Service
// Worker version (?v=N). If the browser served them from cache, the user would be
// stuck on an old SW with stale CSP/headers even after a server deploy. The rest
// of the static files (uv.bundle.js, etc — paths carrying the lib version) may be cached.
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
  console.log(`[panel-browser] Ultraviolet+Wisp on http://${HOST}:${PORT} (wisp on /wisp/)`);
});

for (const sig of ["SIGINT", "SIGTERM"]) {
  process.on(sig, () => server.close(() => process.exit(0)));
}
