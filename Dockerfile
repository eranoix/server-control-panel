# Build: fully static, no cgo. The whole front end is compiled into the binary
# with go:embed, so the runtime image needs no asset directory and no web server.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# `make build` is deliberately not used: it runs a Tailwind step that needs a
# toolchain binary which is not versioned, so it cannot work from a fresh clone.
# The committed CSS is what the binary embeds either way.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server-control-panel ./cmd/server

FROM alpine:3.20
# dtach keeps terminal sessions alive across restarts of this process; git and
# ca-certificates are used by the repository panel and by outbound TLS.
RUN apk add --no-cache ca-certificates dtach git tzdata \
 && adduser -D -u 10001 app
COPY --from=build /out/server-control-panel /usr/local/bin/server-control-panel
# The seed is baked into the image read-only and copied into the data directory
# at start. With the data directory on tmpfs, that makes every restart a clean
# slate — which is what keeps a public demo from slowly filling with whatever
# visitors leave behind.
COPY demo/data /app/seed
COPY demo/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh && mkdir -p /app/data && chown app:app /app/data
USER app
WORKDIR /app
ENV VPSM_CONFIG=/app/data/config.json \
    VPSM_DETACH_JOBS=0
EXPOSE 8765
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
  CMD wget -qO- http://127.0.0.1:8765/api/health >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
