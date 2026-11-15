// Package handler streams images. It holds no access rules; the media service
// decides who may see what.
package handler

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

// Handler serves the media routes.
type Handler struct {
	svc *service.Service
}

// New returns the media handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Routes registers GET /media/works/:id/cover on the /api/v1 group. The route is
// public; access to non-published covers is decided by the signed query string.
func (h *Handler) Routes(v1 *gin.RouterGroup) {
	v1.GET("/media/works/:id/cover", h.streamCover)
	v1.GET("/media/works/:id/screenshots/:imageId", h.streamScreenshot)
}

func (h *Handler) streamCover(c *gin.Context) {
	stream, err := h.svc.StreamCover(c.Request.Context(), c.Param("id"), c.Query("exp"), c.Query("sig"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	writeStream(c, stream)
}

func (h *Handler) streamScreenshot(c *gin.Context) {
	stream, err := h.svc.StreamScreenshot(c.Request.Context(), c.Param("id"), c.Param("imageId"), c.Query("exp"), c.Query("sig"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	writeStream(c, stream)
}

func writeStream(c *gin.Context, stream *service.Stream) {
	defer stream.Body.Close()

	hd := c.Writer.Header()
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Disposition", "inline")
	if stream.Public {
		// Revalidate on every use so a hidden or deleted work loses its image at once.
		hd.Set("Cache-Control", "no-cache")
	} else {
		hd.Set("Cache-Control", "private, no-store")
	}
	if stream.ETag != "" {
		hd.Set("ETag", stream.ETag)
		if stream.Public && c.GetHeader("If-None-Match") == stream.ETag {
			c.Status(http.StatusNotModified)
			return
		}
	}
	hd.Set("Content-Type", stream.ContentType)
	if stream.Size > 0 {
		hd.Set("Content-Length", strconv.FormatInt(stream.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, stream.Body)
}
