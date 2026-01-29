# Build stage
FROM golang:1.23-alpine AS builder

ARG COMPONENT=kubebao-kms
ARG VERSION=dev

WORKDIR /workspace

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /workspace/bin/${COMPONENT} \
    ./cmd/${COMPONENT}

# Runtime stage
FROM alpine:3.20

ARG COMPONENT=kubebao-kms

# Install CA certificates for HTTPS connections
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 10123 -S kubebao && \
    adduser -u 10123 -S kubebao -G kubebao

# Copy binary from builder
COPY --from=builder /workspace/bin/${COMPONENT} /usr/local/bin/kubebao

# Set ownership
RUN chown kubebao:kubebao /usr/local/bin/kubebao

# Use non-root user
USER kubebao

# Default entrypoint
ENTRYPOINT ["/usr/local/bin/kubebao"]
