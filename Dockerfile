# Build stage
FROM golang:1.23-alpine AS builder

ARG COMPONENT=kubebao-kms
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

RUN apk add --no-cache git make

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w -X main.Version=${VERSION}" \
    -trimpath \
    -o /workspace/bin/${COMPONENT} \
    ./cmd/${COMPONENT}

# Runtime stage — distroless-like minimal image
FROM alpine:3.20

ARG COMPONENT=kubebao-kms

RUN apk add --no-cache ca-certificates tzdata && \
    apk upgrade --no-cache && \
    rm -rf /var/cache/apk/*

RUN addgroup -g 10123 -S kubebao && \
    adduser -u 10123 -S kubebao -G kubebao -h /nonexistent -s /sbin/nologin

COPY --from=builder --chown=kubebao:kubebao /workspace/bin/${COMPONENT} /usr/local/bin/kubebao

USER 10123:10123

ENTRYPOINT ["/usr/local/bin/kubebao"]
