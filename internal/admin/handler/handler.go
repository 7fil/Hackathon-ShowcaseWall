// Package handler binds Admin HTTP requests and maps results. It holds no
// business rules; the admin service decides.
package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

const maxJSONBytes = 16 << 10

// Handler serves the /admin routes.
type Handler struct {
	svc *service.Service
}

// New returns the admin handler.
func New(svc *service.Service) *Handler { return &Handler{svc: svc} }

// Routes registers the Admin endpoints on the /api/v1 group. requireAuth is the
// bearer-token middleware; the Admin role is required on top of it.
func (h *Handler) Routes(v1 *gin.RouterGroup, requireAuth gin.HandlerFunc) {
	adm := v1.Group("/admin", requireAuth, middleware.RequireRole(identity.RoleAdmin))
	adm.GET("/works", h.listWorks)
	adm.GET("/summary", h.getSummary)
	adm.POST("/works/:id/publish", h.publish)
	adm.POST("/works/:id/unpublish", h.unpublish)
	adm.PATCH("/works/:id/feature", middleware.BodyLimit(maxJSONBytes), h.setFeatured)
}

func (h *Handler) listWorks(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.svc.ListWorks(c.Request.Context(), caller, service.ListFilter{
		Query:  c.Query("q"),
		Status: c.Query("status"),
		Cursor: c.Query("cursor"),
		Limit:  limit,
	})
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OKWithMeta(c, http.StatusOK, page.Items, gin.H{"next_cursor": page.NextCursor})
}

func (h *Handler) getSummary(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	sum, err := h.svc.GetSummary(c.Request.Context(), caller)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, sum)
}

func (h *Handler) publish(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	w, err := h.svc.Publish(c.Request.Context(), caller, c.Param("id"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, w)
}

func (h *Handler) unpublish(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	w, err := h.svc.Unpublish(c.Request.Context(), caller, c.Param("id"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, w)
}

type featureRequest struct {
	IsFeatured *bool `json:"is_featured"`
}

func (h *Handler) setFeatured(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	var req featureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, apperr.New(apperr.KindInvalid, "invalid_json", "Body permintaan tidak valid"))
		return
	}
	if req.IsFeatured == nil {
		response.Fail(c, apperr.Validation(map[string]string{"is_featured": "is_featured wajib diisi"}))
		return
	}
	w, err := h.svc.SetFeatured(c.Request.Context(), caller, c.Param("id"), *req.IsFeatured)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, w)
}
