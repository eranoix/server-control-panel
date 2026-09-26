#!/usr/bin/env bash
# test-tunnel-datasaver.sh: INTEGRATION test of the real data-saver path.
#
# Proves, with a REAL sing-box and VLESS client (like Hiddify), that:
#   1. a proxy outbound BY NAME (datasaver-*) with a DNS that does not know
#      container names fails (NXDOMAIN, dead traffic);
#   2. an outbound BY IP works: 200 + egress IP + image recompressed to WebP;
#   3. the FULL path works: VLESS client → sing-box → auth_user → proxy → egress.
#
# Requires docker and the datasaver-vps container running. Never touches the
# production tunnel (throwaway instances on their own ports). Exit 0 = all green.
set -u
NET=n8n_default
IMG=ghcr.io/sagernet/sing-box:latest
CURL=curlimages/curl:latest
JPG="https://upload.wikimedia.org/wikipedia/commons/3/3f/JPEG_example_flower.jpg"
FAIL=0
TMP="$(mktemp -d)"
cleanup(){ docker rm -f t-sb-repro t-sb-fix t-sb-srv t-sb-cli >/dev/null 2>&1; rm -rf "$TMP"; }
trap cleanup EXIT

ok(){   echo "  ✅ $1"; }
bad(){  echo "  ❌ $1"; FAIL=1; }

# Static IP of the compression proxy (VPS egress: hermetic path, no home bridge)
DSIP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAMConfig.IPv4Address}}{{end}}' datasaver-vps 2>/dev/null)"
[ -z "$DSIP" ] && DSIP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' datasaver-vps 2>/dev/null)"
if [ -z "$DSIP" ]; then echo "datasaver-vps is not running: start /opt/datasaver first"; exit 2; fi
echo "datasaver-vps IP = $DSIP"

# --- 1) NXDOMAIN REPRO: proxy by NAME + public DNS (cannot resolve docker names)
cat > "$TMP/repro.json" <<EOF
{ "log":{"level":"error"},
  "dns":{"servers":[{"type":"udp","tag":"pub","server":"1.1.1.1"}],"final":"pub"},
  "inbounds":[{"type":"http","tag":"in","listen":"0.0.0.0","listen_port":18888}],
  "outbounds":[{"type":"http","tag":"proxy","server":"datasaver-vps-nonexistent-xyz","server_port":8080}],
  "route":{"rules":[{"inbound":["in"],"outbound":"proxy"}],"final":"proxy"} }
EOF
docker rm -f t-sb-repro >/dev/null 2>&1
docker run -d --name t-sb-repro --network "$NET" -v "$TMP/repro.json":/c.json:ro "$IMG" -D /var/lib/sing-box -c /c.json run >/dev/null 2>&1
sleep 2
code=$(docker run --rm --network "$NET" "$CURL" -s --max-time 12 -x http://t-sb-repro:18888 http://api.ipify.org -o /dev/null -w '%{http_code}' 2>/dev/null)
if [ "$code" != "200" ]; then ok "repro: an unresolvable outbound by name fails (http=$code), the bug is reproducible"; else bad "repro: should have failed, got http=$code"; fi

# --- 2) FIX: proxy by IP reaches the internet and compresses
cat > "$TMP/fix.json" <<EOF
{ "log":{"level":"error"},
  "dns":{"servers":[{"type":"udp","tag":"pub","server":"1.1.1.1"}],"final":"pub"},
  "inbounds":[{"type":"http","tag":"in","listen":"0.0.0.0","listen_port":18889}],
  "outbounds":[{"type":"http","tag":"proxy","server":"$DSIP","server_port":8080}],
  "route":{"rules":[{"action":"resolve","strategy":"ipv4_only"},{"inbound":["in"],"outbound":"proxy"}],"final":"proxy"} }
EOF
docker rm -f t-sb-fix >/dev/null 2>&1
docker run -d --name t-sb-fix --network "$NET" -v "$TMP/fix.json":/c.json:ro "$IMG" -D /var/lib/sing-box -c /c.json run >/dev/null 2>&1
sleep 2
ip=$(docker run --rm --network "$NET" "$CURL" -s --max-time 15 -x http://t-sb-fix:18889 http://api.ipify.org 2>/dev/null)
if echo "$ip" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$'; then ok "fix: proxy by IP reaches the internet (egress=$ip)"; else bad "fix: no valid egress IP (got '$ip')"; fi
read ct bytes < <(docker run --rm --network "$NET" "$CURL" -s -k --max-time 30 -x http://t-sb-fix:18889 -o /dev/null -w '%{content_type} %{size_download}' "$JPG" 2>/dev/null)
if [ "$ct" = "image/webp" ] && [ "${bytes:-0}" -gt 0 ] && [ "${bytes:-0}" -lt 36287 ]; then ok "fix: JPEG recompressed to WebP ($bytes B < 36287 B original)"; else bad "fix: no compression (type=$ct bytes=$bytes)"; fi

# --- 3) FULL PATH: VLESS client → server → auth_user → proxy(IP)
U="11111111-2222-3333-4444-555555555555"
cat > "$TMP/srv.json" <<EOF
{ "log":{"level":"error"},
  "dns":{"servers":[{"type":"udp","tag":"pub","server":"1.1.1.1"}],"final":"pub"},
  "inbounds":[{"type":"vless","tag":"vin","listen":"0.0.0.0","listen_port":18080,"users":[{"uuid":"$U","name":"testdev"}],"transport":{"type":"ws","path":"/t"}}],
  "outbounds":[{"type":"direct","tag":"direct"},{"type":"http","tag":"proxy-vps","server":"$DSIP","server_port":8080}],
  "route":{"rules":[{"action":"resolve","strategy":"ipv4_only"},{"auth_user":["testdev"],"outbound":"proxy-vps"}],"final":"direct"} }
EOF
cat > "$TMP/cli.json" <<EOF
{ "log":{"level":"error"},
  "inbounds":[{"type":"http","listen":"0.0.0.0","listen_port":11080}],
  "outbounds":[{"type":"vless","server":"t-sb-srv","server_port":18080,"uuid":"$U","transport":{"type":"ws","path":"/t"}}] }
EOF
docker rm -f t-sb-srv t-sb-cli >/dev/null 2>&1
docker run -d --name t-sb-srv --network "$NET" -v "$TMP/srv.json":/c.json:ro "$IMG" -D /var/lib/sing-box -c /c.json run >/dev/null 2>&1
sleep 1
docker run -d --name t-sb-cli --network "$NET" -v "$TMP/cli.json":/c.json:ro "$IMG" -D /var/lib/sing-box -c /c.json run >/dev/null 2>&1
sleep 2
code=$(docker run --rm --network "$NET" "$CURL" -s --max-time 20 -x http://t-sb-cli:11080 http://api.ipify.org -o /dev/null -w '%{http_code}' 2>/dev/null)
if [ "$code" = "200" ]; then ok "real path: VLESS client → sing-box → auth_user → proxy → internet (http=$code)"; else bad "real path: failed (http=$code)"; fi

echo
if [ "$FAIL" = "0" ]; then echo "RESULT: ALL TESTS GREEN ✅"; else echo "RESULT: FAILED ❌"; fi
exit $FAIL
