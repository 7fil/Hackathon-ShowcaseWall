// Package service manages the WebSocket hub, connection limits per IP and total,
// handshake rate limits, and Redis event fan-out. It holds no business rules.
package service

import (
	"context"
	"errors"
	"time"
)

const (
	MaxConnsPerIP     = 10
	MaxTotalConns     = 500
	HandshakeLimitIP  = 20
	HandshakeWindowIP = time.Minute
	SendBufferSize    = 64
	PingInterval      = 30 * time.Second
	PongWait          = 60 * time.Second
	WriteWait         = 10 * time.Second
	MaxClientMsgBytes = 512
)

var (
	ErrOriginForbidden  = errors.New("realtime: origin not allowed")
	ErrHandshakeLimited = errors.New("realtime: handshake rate limited")
	ErrIPLimitReached   = errors.New("realtime: per-ip connection limit reached")
	ErrHubFull          = errors.New("realtime: total connection limit reached")
)

// Subscriber subscribes to the Redis pub/sub channel and yields serialized JSON messages.
type Subscriber interface {
	Subscribe(ctx context.Context, onMessage func(raw []byte)) error
}

// RateLimiter checks handshake rate limits per IP.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error)
}
