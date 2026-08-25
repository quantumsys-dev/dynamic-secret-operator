# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download || true

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o db-status-app main.go

# Runtime stage (distroless non-root)
FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=builder /app/db-status-app /db-status-app

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/db-status-app"]
