# terka-caddy: the official Caddy image with a rebuilt binary, shared by the web image (terka-web)
# and the VPS edge. Published by .github/workflows/caddy.yml as ghcr.io/dikym/terka-caddy:<VERSION>.
#
# Caddy 2.11.9, the latest release, bundles modules with known vulnerabilities. Rebuild that release
# with fixed modules and a pinned Go, and refuse the build if anything reachable is still known-bad.
# When a Caddy release ships one of these versions or newer, drop its --replace. Any change here
# bumps the patch level in VERSION (2.11.9-p1 → 2.11.9-p2; a new release resets it to -p1).

# Cross-compiled on the build machine, so the arm64 image does not need emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS caddy
ARG TARGETOS
ARG TARGETARCH
RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.7
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH xcaddy build v2.11.9 --output /out/caddy \
	--replace golang.org/x/net=golang.org/x/net@v0.62.0 \
	--replace github.com/klauspost/compress=github.com/klauspost/compress@v1.20.4 \
	--replace golang.org/x/crypto=golang.org/x/crypto@v0.59.0
RUN go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary /out/caddy

FROM caddy:2.11.9-alpine
COPY --from=caddy /out/caddy /usr/bin/caddy
