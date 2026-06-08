# Build context must be the repo root so the Go module is available:
#   docker build -f examples/research-assistant/Dockerfile -t research-assistant:latest .

ARG GO_VERSION=1.26
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

####################################################################################################
# builder
####################################################################################################
FROM golang:${GO_VERSION} AS builder
WORKDIR /src

# Cache go.mod/go.sum download separately from the source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
        -o /out/research-assistant \
        ./examples/research-assistant

####################################################################################################
# runtime
####################################################################################################
FROM ${RUNTIME_IMAGE}
COPY --from=builder /out/research-assistant /bin/research-assistant
ENTRYPOINT ["/bin/research-assistant"]
