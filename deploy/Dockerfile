# The API image: one static Go binary. The web build ships in its own image from terka-web.
# Build from the repository root: docker build -f deploy/Dockerfile .

# Cross-compiled on the build machine instead of emulating the target, which is much faster.
FROM --platform=$BUILDPLATFORM golang:1.27.4 AS server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
	go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/terka ./cmd/terka

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=server /out/terka /terka
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=3 CMD ["/terka", "health"]
ENTRYPOINT ["/terka"]
CMD ["serve"]
