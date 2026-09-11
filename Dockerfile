# syntax=docker/dockerfile:1

# ------------------------------------------------------------------------------
# Build Stage
# ------------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build static binary without debug overhead
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o mcp-router .

# ------------------------------------------------------------------------------
# Runtime Stage
# ------------------------------------------------------------------------------
FROM alpine:3.21

# Install security certificates and timezone data
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# Copy compiled binary and example configuration
COPY --from=builder /app/mcp-router /usr/local/bin/mcp-router
COPY config.example.yaml /app/config.example.yaml

EXPOSE 8090

ENTRYPOINT ["/usr/local/bin/mcp-router"]
CMD ["-config", "/app/config.example.yaml"]
