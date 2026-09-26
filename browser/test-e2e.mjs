// Teste E2E headless: abre o túnel Wisp local e busca example.com pela "saída" do servidor.
import { client } from "@mercuryworkshop/wisp-js/client";

const WS = "ws://127.0.0.1:8090/wisp/";
const conn = new client.ClientConnection(WS);

const done = (ok, msg) => { console.log(ok ? "PASS" : "FAIL", msg); process.exit(ok ? 0 : 1); };
setTimeout(() => done(false, "timeout (15s)"), 15000);

conn.onopen = () => {
  console.log("INFO wisp conectado, abrindo stream -> example.com:80");
  const s = conn.create_stream("example.com", 80);
  const chunks = [];
  s.onmessage = (data) => chunks.push(Buffer.from(data));
  s.onclose = () => {
    const resp = Buffer.concat(chunks).toString("latin1");
    const head = resp.split("\r\n").slice(0, 1)[0] || "";
    console.log("INFO resposta:", head);
    const ok = /HTTP\/1\.[01] (200|30\d)/.test(resp) && /Example Domain|<html/i.test(resp);
    done(ok, ok ? `tunelou e recebeu HTML (${resp.length}B)` : `resposta inesperada (${resp.length}B)`);
  };
  s.onerror = (e) => done(false, "stream error: " + e);
  s.send(new TextEncoder().encode("GET / HTTP/1.0\r\nHost: example.com\r\nConnection: close\r\n\r\n"));
};
conn.onerror = (e) => done(false, "conn error: " + e);
