# The edge Caddy on the VPS: the official image with a rebuilt binary. Its Caddyfile, routes and
# certificates are mounted at runtime. Build from this folder: docker build deploy/caddy

# Caddy 2.11.9, the latest release, bundles modules with known vulnerabilities. Rebuild that release
# with fixed modules and a pinned Go, and refuse the build if anything reachable is still known-bad.
# Keep this stage identical to terka-web/Dockerfile. Drop a --replace once a Caddy release ships
# that version or newer.
FROM golang:1.27.4-alpine AS caddy
RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.7
RUN xcaddy build v2.11.9 --output /out/caddy \
	--replace golang.org/x/net=golang.org/x/net@v0.62.0 \
	--replace github.com/klauspost/compress=github.com/klauspost/compress@v1.20.4 \
	--replace golang.org/x/crypto=golang.org/x/crypto@v0.59.0
RUN go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /out/caddy

FROM caddy:2.11.9-alpine
COPY --from=caddy /out/caddy /usr/bin/caddy
