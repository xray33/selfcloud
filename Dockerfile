# Build Stage
FROM golang:alpine AS builder

WORKDIR /app

# Download dependencies
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
ARG IMAGE_TAG=dev
COPY . .

# Build static binary
RUN CGO_ENABLED=0 go build \
    -ldflags="-w -s -X main.buildVersion=${IMAGE_TAG} -X main.buildCommit=${IMAGE_TAG} -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/bin/selfcloud ./cmd/selfcloud

# Final Runtime Stage (Go Backend Service)
FROM alpine:3.24

RUN apk upgrade --no-cache \
    && apk add --no-cache --upgrade ca-certificates ca-certificates-bundle tzdata \
    && update-ca-certificates \
    && addgroup -g 10001 -S selfcloud \
    && adduser -u 10001 -S selfcloud -G selfcloud

WORKDIR /app
COPY --from=builder /app/bin/selfcloud /app/selfcloud

# Storage directory setup & permissions
RUN mkdir -p /var/lib/selfcloud/data \
    && chown -R selfcloud:selfcloud /app /var/lib/selfcloud/data

USER selfcloud:selfcloud

ENV SERVE_STATIC=false
ENV STORAGE_DIR=/var/lib/selfcloud/data

EXPOSE 8080

ENTRYPOINT ["/app/selfcloud"]
CMD ["serve"]
