# Build the manager binary
FROM golang:1.23 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# Cache dependencies before building and copying source
RUN go mod download

# Copy Go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/
COPY pkg/ pkg/

# Build statically linked binary with stripped debug information
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -a -ldflags="-w -s" -o manager cmd/main.go

# Use Chainguard Static Distroless image as minimal, zero-CVE base image
FROM cgr.dev/chainguard/static:latest
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
