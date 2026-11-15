// Package handler upgrades HTTP requests on /ws/wall to read-only WebSocket
// connections using RFC 6455 framing over the standard library.
package handler

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/realtime/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

type Handler struct {
	hub *service.Hub
}

func New(hub *service.Hub) *Handler {
	return &Handler{hub: hub}
}

// Routes registers /ws/wall at both the root router and /api/v1 so both
// wss://<host>/ws/wall (PRD) and wss://<host>/api/v1/ws/wall work seamlessly.
func (h *Handler) Routes(r *gin.Engine, v1 *gin.RouterGroup) {
	r.GET("/ws/wall", h.serveWS)
	if v1 != nil {
		v1.GET("/ws/wall", h.serveWS)
	}
}

func (h *Handler) serveWS(c *gin.Context) {
	ip := c.ClientIP()
	origin := c.GetHeader("Origin")

	if err := h.hub.CheckHandshake(c.Request.Context(), origin, ip); err != nil {
		switch {
		case errors.Is(err, service.ErrOriginForbidden):
			response.Fail(c, apperr.New(apperr.KindForbidden, "forbidden_origin", "Origin WebSocket tidak diizinkan"))
		case errors.Is(err, service.ErrHandshakeLimited),
			errors.Is(err, service.ErrIPLimitReached),
			errors.Is(err, service.ErrHubFull):
			response.Fail(c, apperr.New(apperr.KindRateLimited, "rate_limited", "Terlalu banyak koneksi WebSocket"))
		default:
			response.Fail(c, apperr.Internal(err))
		}
		return
	}

	if !headerContains(c.GetHeader("Connection"), "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket") ||
		strings.TrimSpace(c.GetHeader("Sec-WebSocket-Version")) != "13" {
		response.Fail(c, apperr.New(apperr.KindInvalid, "websocket_upgrade_required", "Header upgrade WebSocket tidak valid"))
		return
	}
	key := strings.TrimSpace(c.GetHeader("Sec-WebSocket-Key"))
	if key == "" {
		response.Fail(c, apperr.New(apperr.KindInvalid, "websocket_key_missing", "Sec-WebSocket-Key wajib diisi"))
		return
	}

	client, err := h.hub.Register(ip)
	if err != nil {
		response.Fail(c, apperr.New(apperr.KindRateLimited, "rate_limited", "Terlalu banyak koneksi WebSocket"))
		return
	}

	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		h.hub.Unregister(client, "hijack_unsupported")
		response.Fail(c, apperr.Internal(errors.New("http hijacking not supported")))
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		h.hub.Unregister(client, "hijack_failed")
		return
	}

	accept := computeAcceptKey(key)
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		_ = conn.Close()
		h.hub.Unregister(client, "write_error")
		return
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		h.hub.Unregister(client, "write_error")
		return
	}

	ws := &wsConn{conn: conn, r: rw.Reader}
	done := make(chan struct{})
	go h.writePump(ws, client, done)
	h.readPump(ws, client, done)
}

func (h *Handler) readPump(ws *wsConn, client *service.Client, done chan struct{}) {
	defer func() {
		close(done)
		_ = ws.Close()
	}()
	_ = ws.conn.SetReadDeadline(time.Now().Add(service.PongWait))

	for {
		opcode, payload, err := ws.readFrame(service.MaxClientMsgBytes)
		if err != nil {
			if errors.Is(err, errFrameTooLarge) {
				_ = ws.writeFrame(opClose, nil)
				h.hub.Unregister(client, "message_too_large")
				return
			}
			h.hub.Unregister(client, "normal")
			return
		}
		_ = ws.conn.SetReadDeadline(time.Now().Add(service.PongWait))

		switch opcode {
		case opPong:
			// Client answered ping; read deadline already refreshed.
		case opPing:
			_ = ws.writeFrame(opPong, payload)
		case opClose:
			_ = ws.writeFrame(opClose, nil)
			h.hub.Unregister(client, "normal")
			return
		case opText, opBinary, opContinuation:
			// Public socket is strictly read-only: ignore any application frame.
		}
	}
}

func (h *Handler) writePump(ws *wsConn, client *service.Client, done <-chan struct{}) {
	ticker := time.NewTicker(service.PingInterval)
	defer func() {
		ticker.Stop()
		_ = ws.Close()
	}()

	for {
		select {
		case <-done:
			return
		case msg, ok := <-client.SendChan():
			if !ok {
				_ = ws.writeFrame(opClose, nil)
				return
			}
			if err := ws.writeFrame(opText, msg); err != nil {
				h.hub.Unregister(client, "write_error")
				return
			}
		case <-ticker.C:
			if err := ws.writeFrame(opPing, nil); err != nil {
				h.hub.Unregister(client, "ping_timeout")
				return
			}
		}
	}
}

// ---- RFC 6455 Framing ----

var errFrameTooLarge = errors.New("websocket frame exceeds limit")

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex
}

func (w *wsConn) Close() error { return w.conn.Close() }

func (w *wsConn) writeFrame(opcode byte, payload []byte) error {
	w.wmu.Lock()
	defer w.wmu.Unlock()

	_ = w.conn.SetWriteDeadline(time.Now().Add(service.WriteWait))
	n := len(payload)
	header := make([]byte, 0, 10)
	header = append(header, 0x80|(opcode&0x0F))

	switch {
	case n < 126:
		header = append(header, byte(n))
	case n <= 65535:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		header = append(header, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	if _, err := w.conn.Write(header); err != nil {
		return err
	}
	if n > 0 {
		if _, err := w.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (w *wsConn) readFrame(maxBytes int64) (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(w.r, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode := hdr[0] & 0x0F
	masked := (hdr[1] & 0x80) != 0
	if !masked {
		// RFC 6455 §5.1: client-to-server frames MUST be masked.
		return 0, nil, errors.New("unmasked client frame")
	}
	length := int64(hdr[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(w.r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(w.r, ext[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if length > maxBytes || length < 0 {
		return 0, nil, errFrameTooLarge
	}

	var maskKey [4]byte
	if _, err := io.ReadFull(w.r, maskKey[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return 0, nil, err
		}
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return opcode, payload, nil
}

func computeAcceptKey(key string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func headerContains(headerVal, token string) bool {
	for _, part := range strings.Split(headerVal, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
