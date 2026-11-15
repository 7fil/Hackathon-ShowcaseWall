// Package handler binds HTTP requests and maps results for the works endpoints
// (gallery, detail, /me and create/edit/delete). Admin and media have their own
// handlers. It holds no business rules.
package handler

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/middleware"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/response"
)

const maxMultipartBytes = 6 << 20

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes(v1 *gin.RouterGroup, requireAuth gin.HandlerFunc) {
	multipartLimit := middleware.BodyLimit(maxMultipartBytes)

	w := v1.Group("/works")
	w.GET("", h.listPublished)
	w.GET("/featured", h.getFeatured)
	w.GET("/exhibition", h.listExhibition)
	w.GET("/facets", h.getFacets)
	w.GET("/:id", h.getPublishedDetail)

	w.POST("", requireAuth, multipartLimit, h.createWork)
	w.PATCH("/:id", requireAuth, multipartLimit, h.updateWork)
	w.DELETE("/:id", requireAuth, h.deleteWork)
	w.POST("/:id/screenshots", requireAuth, multipartLimit, h.addScreenshot)
	w.DELETE("/:id/screenshots/:imageId", requireAuth, h.deleteScreenshot)

	me := v1.Group("/me", requireAuth)
	me.GET("/works", h.listMyWorks)
	me.GET("/works/:id", h.getManagedDetail)
	me.GET("/summary", h.getMySummary)
}

func (h *Handler) listPublished(c *gin.Context) {
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.svc.ListPublished(c.Request.Context(), service.PublicListFilter{
		Query:     c.Query("q"),
		Category:  c.Query("category"),
		TechStack: c.Query("tech_stack"),
		Cursor:    c.Query("cursor"),
		Limit:     limit,
	})
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OKWithMeta(c, http.StatusOK, page.Items, gin.H{"next_cursor": page.NextCursor})
}

func (h *Handler) getFeatured(c *gin.Context) {
	w, err := h.svc.GetFeatured(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, w)
}

func (h *Handler) listExhibition(c *gin.Context) {
	items, err := h.svc.ListExhibition(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, items)
}

func (h *Handler) getFacets(c *gin.Context) {
	facets, err := h.svc.GetFacets(c.Request.Context())
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, facets)
}

func (h *Handler) getPublishedDetail(c *gin.Context) {
	detail, err := h.svc.GetPublishedDetail(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, detail)
}

func (h *Handler) listMyWorks(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, err := h.svc.ListMyWorks(c.Request.Context(), caller, service.ManagedListFilter{
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

func (h *Handler) getMySummary(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	sum, err := h.svc.GetMySummary(c.Request.Context(), caller)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, sum)
}

func (h *Handler) getManagedDetail(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	w, err := h.svc.GetManagedDetail(c.Request.Context(), caller, c.Param("id"))
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, w)
}

func (h *Handler) createWork(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	form, cover, err := parseWorkMultipart(c, true)
	if err != nil {
		response.Fail(c, err)
		return
	}
	res, err := h.svc.CreateWork(c.Request.Context(), caller, service.CreateWorkInput{
		Title:       form.title,
		Summary:     form.summary,
		Description: form.description,
		Benefits:    form.benefits,
		Category:    form.category,
		TechStack:   form.techStack,
		TeamMembers: form.teamMembers,
		DemoURL:     form.demoURL,
		RepoURL:     form.repoURL,
		Cover:       cover,
	})
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusCreated, res)
}

func (h *Handler) updateWork(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	form, cover, err := parseWorkMultipart(c, false)
	if err != nil {
		response.Fail(c, err)
		return
	}
	res, err := h.svc.UpdateWork(c.Request.Context(), caller, c.Param("id"), service.UpdateWorkInput{
		Title:       form.title,
		Summary:     form.summary,
		Description: form.description,
		Benefits:    form.benefits,
		Category:    form.category,
		TechStack:   form.techStack,
		TeamMembers: form.teamMembers,
		DemoURL:     form.demoURL,
		RepoURL:     form.repoURL,
		Cover:       cover,
	})
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusOK, res)
}

func (h *Handler) deleteWork(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	if err := h.svc.DeleteWork(c.Request.Context(), caller, c.Param("id")); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

type parsedWorkForm struct {
	title       string
	summary     string
	description string
	benefits    string
	category    string
	techStack   []string
	teamMembers []string
	demoURL     string
	repoURL     string
}

func parseWorkMultipart(c *gin.Context, requireCover bool) (parsedWorkForm, *service.CoverUpload, error) {
	if err := c.Request.ParseMultipartForm(maxMultipartBytes); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			return parsedWorkForm{}, nil, apperr.Validation(map[string]string{"cover": "Ukuran file cover maksimal 5MB"})
		}
		return parsedWorkForm{}, nil, apperr.New(apperr.KindInvalid, "invalid_multipart", "Format form-data tidak valid")
	}

	form := parsedWorkForm{
		title:       c.PostForm("title"),
		summary:     c.PostForm("summary"),
		description: c.PostForm("description"),
		benefits:    c.PostForm("benefits"),
		category:    c.PostForm("category"),
		techStack:   extractRepeatedField(c, "tech_stack"),
		teamMembers: extractRepeatedField(c, "team_members"),
		demoURL:     c.PostForm("demo_url"),
		repoURL:     c.PostForm("repo_url"),
	}

	fh, err := c.FormFile("cover")
	if err != nil {
		if requireCover {
			return form, nil, nil
		}
		return form, nil, nil
	}

	if fh.Size > service.MaxCoverBytes {
		return form, &service.CoverUpload{Data: make([]byte, service.MaxCoverBytes+1)}, nil
	}
	f, err := fh.Open()
	if err != nil {
		return form, nil, apperr.Internal(fmt.Errorf("open cover upload: %w", err))
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, service.MaxCoverBytes+1))
	if err != nil {
		return form, nil, apperr.Internal(fmt.Errorf("read cover upload: %w", err))
	}
	return form, &service.CoverUpload{
		Data:        data,
		ContentType: fh.Header.Get("Content-Type"),
	}, nil
}

func extractRepeatedField(c *gin.Context, key string) []string {
	vals := c.PostFormArray(key)
	if len(vals) == 0 {
		vals = c.PostFormArray(key + "[]")
	}
	return vals
}

func (h *Handler) addScreenshot(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	up, err := parseImageUpload(c, "image")
	if err != nil {
		response.Fail(c, err)
		return
	}
	dto, err := h.svc.AddScreenshot(c.Request.Context(), caller, c.Param("id"), up)
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, http.StatusCreated, dto)
}

func (h *Handler) deleteScreenshot(c *gin.Context) {
	caller, _ := middleware.IdentityFrom(c)
	if err := h.svc.DeleteScreenshot(c.Request.Context(), caller, c.Param("id"), c.Param("imageId")); err != nil {
		response.Fail(c, err)
		return
	}
	response.NoContent(c)
}

// parseImageUpload reads one image from a multipart form. A missing file gives a nil
// upload, which the service reports as a field error.
func parseImageUpload(c *gin.Context, field string) (*service.CoverUpload, error) {
	if err := c.Request.ParseMultipartForm(maxMultipartBytes); err != nil {
		if strings.Contains(err.Error(), "http: request body too large") {
			return nil, apperr.Validation(map[string]string{field: "Ukuran file gambar maksimal 5MB"})
		}
		return nil, apperr.New(apperr.KindInvalid, "invalid_multipart", "Format form-data tidak valid")
	}
	fh, err := c.FormFile(field)
	if err != nil {
		return nil, nil
	}
	if fh.Size > service.MaxCoverBytes {
		return &service.CoverUpload{Data: make([]byte, service.MaxCoverBytes+1)}, nil
	}
	f, err := fh.Open()
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("open image upload: %w", err))
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, service.MaxCoverBytes+1))
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("read image upload: %w", err))
	}
	return &service.CoverUpload{Data: data, ContentType: fh.Header.Get("Content-Type")}, nil
}
