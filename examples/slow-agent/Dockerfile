# Build context must be the repo root so the Go module is available:
#   docker build -f examples/slow-agent/Dockerfile -t slow-agent:latest .

ARG GO_VERSION=1.26
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

# Default (empty) source for the optional `cacert` build context. Overridden by
# `--build-context cacert=<dir>` to inject a corporate proxy CA (see builder).
FROM scratch AS cacert

####################################################################################################
# builder
####################################################################################################
FROM golang:${GO_VERSION} AS builder
WORKDIR /src

# Optional: trust a corporate proxy CA (e.g. Netskope) so module fetches over an
# intercepted TLS connection verify. No-op unless a `cacert` build context is
# supplied: docker buildx build --build-context cacert=<dir-with-a-*.pem>.
# update-ca-certificates only ingests *.crt, so normalize the extension.
COPY --from=cacert . /usr/local/share/ca-certificates/extra/
RUN for f in /usr/local/share/ca-certificates/extra/*; do \
        [ -e "$f" ] || continue; \
        cp "$f" "/usr/local/share/ca-certificates/extra-$(basename "$f").crt"; \
    done; \
    update-ca-certificates 2>/dev/null || true

# Cache go.mod/go.sum download separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
        -o /out/slow-agent \
        ./examples/slow-agent

####################################################################################################
# runtime
####################################################################################################
FROM ${RUNTIME_IMAGE}
COPY --from=builder /out/slow-agent /bin/slow-agent
ENTRYPOINT ["/bin/slow-agent"]
