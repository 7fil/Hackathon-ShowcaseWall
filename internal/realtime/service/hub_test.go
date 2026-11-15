package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeLimiter struct {
	allow bool
}

func (f *fakeLimiter) Allow(context.Context, string, int, time.Duration) (bool, time.Duration, error) {
	return f.allow, time.Minute, nil
}

func newTestHub(limiter RateLimiter) *Hub {
	return NewHub(Deps{
		Limiter:        limiter,
		FrontendOrigin: "https://group4.bccdev.id",
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestHandshakeOriginAndRateLimit(t *testing.T) {
	h := newTestHub(&fakeLimiter{allow: true})
	ctx := context.Background()

	if err := h.CheckHandshake(ctx, "https://evil.example.com", "1.2.3.4"); !errors.Is(err, ErrOriginForbidden) {
		t.Fatalf("expected ErrOriginForbidden, got %v", err)
	}
	if err := h.CheckHandshake(ctx, "https://group4.bccdev.id", "1.2.3.4"); err != nil {
		t.Fatalf("expected valid handshake, got %v", err)
	}

	hBlocked := newTestHub(&fakeLimiter{allow: false})
	if err := hBlocked.CheckHandshake(ctx, "https://group4.bccdev.id", "1.2.3.4"); !errors.Is(err, ErrHandshakeLimited) {
		t.Fatalf("expected ErrHandshakeLimited, got %v", err)
	}
}

func TestPerIPLimitAndSlowClientDrop(t *testing.T) {
	h := newTestHub(&fakeLimiter{allow: true})
	var clients []*Client
	for i := 0; i < MaxConnsPerIP; i++ {
		c, err := h.Register("10.0.0.1")
		if err != nil {
			t.Fatalf("register %d failed: %v", i, err)
		}
		clients = append(clients, c)
	}
	if _, err := h.Register("10.0.0.1"); !errors.Is(err, ErrIPLimitReached) {
		t.Fatalf("expected ErrIPLimitReached on connection %d, got %v", MaxConnsPerIP+1, err)
	}

	// Drain client[1] once so it has buffer room while client[0] fills up.
	<-clients[1].SendChan()
	for i := 0; i <= SendBufferSize; i++ {
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
