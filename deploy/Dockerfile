# One image holds the whole app: the Go binary with the client build embedded in it.
# Build from the repository root: docker build -f deploy/Dockerfile .

# The client build is plain files, so it is built once on the build machine for every target.
FROM --platform=$BUILDPLATFORM oven/bun:1.4 AS web
WORKDIR /src/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Cross-compiled on the build machine instead of emulating the target, which is much faster.
FROM --platform=$BUILDPLATFORM golang:1.27.3 AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/web/build/ ./internal/web/dist/
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/terka ./cmd/terka

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=server /out/terka /terka
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 CMD ["/terka", "health"]
ENTRYPOINT ["/terka"]
CMD ["serve"]
