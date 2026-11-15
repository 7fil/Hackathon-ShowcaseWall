package realtime_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/service"
)

type fakeLimiter struct {
	allow bool
}

func (f *fakeLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return f.allow, time.Minute, nil
}

func newTestHub(limiter service.RateLimiter) *service.Hub {
	return service.NewHub(service.Deps{
		Limiter:        limiter,
		FrontendOrigin: "https://group4.bccdev.id",
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestHandshakeOriginAndRateLimit(t *testing.T) {
	h := newTestHub(&fakeLimiter{allow: true})
	ctx := context.Background()

	if err := h.CheckHandshake(ctx, "https://evil.example.com", "1.2.3.4"); !errors.Is(err, service.ErrOriginForbidden) {
		t.Fatalf("expected ErrOriginForbidden, got %v", err)
	}
	if err := h.CheckHandshake(ctx, "https://group4.bccdev.id", "1.2.3.4"); err != nil {
		t.Fatalf("expected valid handshake, got %v", err)
	}

	hBlocked := newTestHub(&fakeLimiter{allow: false})
	if err := hBlocked.CheckHandshake(ctx, "https://group4.bccdev.id", "1.2.3.4"); !errors.Is(err, service.ErrHandshakeLimited) {
		t.Fatalf("expected ErrHandshakeLimited, got %v", err)
	}
}

func TestPerIPLimitAndSlowClientDrop(t *testing.T) {
	h := newTestHub(&fakeLimiter{allow: true})
	var clients []*service.Client
	for i := 0; i < service.MaxConnsPerIP; i++ {
		c, err := h.Register("10.0.0.1")
		if err != nil {
			t.Fatalf("register %d failed: %v", i, err)
		}
		clients = append(clients, c)
	}
	if _, err := h.Register("10.0.0.1"); !errors.Is(err, service.ErrIPLimitReached) {
		t.Fatalf("expected ErrIPLimitReached on connection %d, got %v", service.MaxConnsPerIP+1, err)
	}

	// client[0] never reads, so its buffer fills up; client[1] is drained before every
	// broadcast so it always has room and must survive.
	for i := 0; i <= service.SendBufferSize; i++ {
		select {
		case <-clients[1].SendChan():
		default:
		}
		h.Broadcast([]byte(`{"type":"work.deleted","seq":1,"payload":{"id":"x"}}`))
	}

	// client[0] should have been dropped and its channel closed.
	for range clients[0].SendChan() {
	}

	for _, c := range clients {
		h.Unregister(c, "normal")
	}
}
