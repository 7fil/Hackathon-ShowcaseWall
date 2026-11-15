# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed


FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata wget && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=builder /out/api /app/api
COPY --from=builder /out/seed /app/seed

RUN mkdir -p /app/secrets /data/media && chown -R appuser:appgroup /app /data

USER appuser

ENV APP_ENV=production \
    PORT=8080 \
    METRICS_PORT=9100 \
    MEDIA_LOCAL_DIR=/data/media

EXPOSE 8080 9100

HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:${PORT:-8080}/health || exit 1

ENTRYPOINT ["/app/api"]
