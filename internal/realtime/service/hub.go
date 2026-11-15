package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

// Client represents one connected WebSocket viewer.
type Client struct {
	ip   string
	send chan []byte
}

func (c *Client) SendChan() <-chan []byte { return c.send }

type Hub struct {
	sub            Subscriber
	limiter        RateLimiter
	frontendOrigin string
	log            *slog.Logger

	mu      sync.RWMutex
	clients map[*Client]struct{}
	byIP    map[string]int
}

type Deps struct {
	Subscriber     Subscriber
	Limiter        RateLimiter
	FrontendOrigin string
	Logger         *slog.Logger
}

func NewHub(d Deps) *Hub {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Hub{
		sub:            d.Subscriber,
		limiter:        d.Limiter,
		frontendOrigin: d.FrontendOrigin,
		log:            d.Logger,
		clients:        make(map[*Client]struct{}),
		byIP:           make(map[string]int),
	}
}

// Run listens on the Redis pub/sub subscriber until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	if h.sub == nil {
		return
	}
	for {
		if ctx.Err() != nil {
			return
		}
		err := h.sub.Subscribe(ctx, func(raw []byte) {
			h.Broadcast(raw)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			h.log.Error("realtime redis subscriber error, retrying", "error", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

// CheckHandshake validates Origin and per-IP handshake rate limits before upgrading.
func (h *Hub) CheckHandshake(ctx context.Context, origin, ip string) error {
	if h.frontendOrigin != "" && origin != h.frontendOrigin {
		metrics.IncWSHandshakeRejected("bad_origin")
		h.log.Warn("audit: websocket handshake rejected", "reason", "bad_origin", "origin", origin, "ip", ip)
		return ErrOriginForbidden
	}
	if h.limiter != nil {
		ok, _, err := h.limiter.Allow(ctx, "ws:handshake:"+ip, HandshakeLimitIP, HandshakeWindowIP)
		if err != nil {
			h.log.Error("websocket handshake rate limiter failed", "error", err.Error())
		} else if !ok {
			metrics.IncWSHandshakeRejected("rate_limited")
			h.log.Warn("audit: websocket handshake rejected", "reason", "rate_limited", "ip", ip)
			return ErrHandshakeLimited
		}
	}
	h.mu.RLock()
	ipCount := h.byIP[ip]
	total := len(h.clients)
	h.mu.RUnlock()

	if ipCount >= MaxConnsPerIP {
		metrics.IncWSHandshakeRejected("ip_limit")
		h.log.Warn("audit: websocket handshake rejected", "reason", "ip_limit", "ip", ip)
		return ErrIPLimitReached
	}
	if total >= MaxTotalConns {
		metrics.IncWSHandshakeRejected("total_limit")
		h.log.Warn("audit: websocket handshake rejected", "reason", "total_limit", "ip", ip)
		return ErrHubFull
	}
	return nil
}

// Register adds a viewer connection to the hub.
func (h *Hub) Register(ip string) (*Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.byIP[ip] >= MaxConnsPerIP {
		metrics.IncWSHandshakeRejected("ip_limit")
		return nil, ErrIPLimitReached
	}
	if len(h.clients) >= MaxTotalConns {
		metrics.IncWSHandshakeRejected("total_limit")
		return nil, ErrHubFull
	}

	c := &Client{
		ip:   ip,
		send: make(chan []byte, SendBufferSize),
	}
	h.clients[c] = struct{}{}
	h.byIP[ip]++
	metrics.AddWSActiveConnections(1)
	h.log.Info("audit: websocket connect", "ip", ip, "active_connections", len(h.clients))
	return c, nil
}

// Unregister removes a client from the hub and closes its send channel.
func (h *Hub) Unregister(c *Client, reason string) {
	h.mu.Lock()
	if _, ok := h.clients[c]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.clients, c)
	h.byIP[c.ip]--
	if h.byIP[c.ip] <= 0 {
		delete(h.byIP, c.ip)
	}
	close(c.send)
	active := len(h.clients)
	h.mu.Unlock()

	metrics.AddWSActiveConnections(-1)
	if reason != "" && reason != "normal" {
		metrics.IncWSClientDropped(reason)
		h.log.Warn("audit: websocket client dropped", "ip", c.ip, "reason", reason, "active_connections", active)
	} else {
		h.log.Info("audit: websocket disconnect", "ip", c.ip, "active_connections", active)
	}
}

// Broadcast pushes a serialized event to every connected viewer. Any client whose
// send buffer is full is dropped immediately so one slow phone never blocks others.
func (h *Hub) Broadcast(msg []byte) {
	var slow []*Client
	var delivered int64

	h.mu.RLock()
	for c := range h.clients {
		select {
		case c.send <- msg:
			delivered++
		default:
			slow = append(slow, c)
		}
	}
	h.mu.RUnlock()

	metrics.AddWSEventsDelivered(delivered)
	for _, c := range slow {
		h.Unregister(c, "slow_client")
	}
}
