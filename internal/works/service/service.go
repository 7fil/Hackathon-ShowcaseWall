package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"golang.org/x/sync/singleflight"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

type Deps struct {
	Works  WorkRepository
	Media  MediaStore
	Cache  WorkCache
	Events EventPublisher
	Logger *slog.Logger
	Now    func() time.Time
}

type Service struct {
	d  Deps
	wg sync.WaitGroup
	sf singleflight.Group
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{d: d}
}

func (s *Service) Close() { s.wg.Wait() }

var (
	errWorkNotFound = apperr.New(apperr.KindNotFound, "work_not_found", "Karya tidak ditemukan atau belum dipublikasikan")
	errForbidden    = apperr.New(apperr.KindForbidden, "forbidden", "Anda tidak memiliki hak akses untuk karya ini")
)

func (s *Service) ListPublished(ctx context.Context, f PublicListFilter) (*PublicPage, error) {
	if f.Limit <= 0 || f.Limit > MaxPageLimit {
		f.Limit = DefaultPageLimit
	}
	f.Query = strings.TrimSpace(f.Query)
	f.Category = strings.TrimSpace(f.Category)
	f.TechStack = strings.TrimSpace(f.TechStack)

	cursorTime, cursorID, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, apperr.New(apperr.KindInvalid, "invalid_cursor", "Cursor paginasi tidak valid")
	}

	ver, _ := s.d.Cache.Version(ctx)
	cacheKey := fmt.Sprintf("works:v%d:list:%s", ver, hashFilter(f))
	var cached PublicPage
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && hit {
		return &cached, nil
	}

	rows, err := s.d.Works.ListPublished(ctx, PublicListFilter{
		Query: f.Query, Category: f.Category, TechStack: f.TechStack, Limit: f.Limit + 1,
	}, cursorTime, cursorID)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	var nextCursor *string
	if len(rows) > f.Limit {
		last := rows[f.Limit-1]
		rows = rows[:f.Limit]
		if last.PublishedAt != nil {
			c := encodeCursor(*last.PublishedAt, last.ID)
			nextCursor = &c
		}
	}

	items := make([]PublicWork, 0, len(rows))
	for _, w := range rows {
		items = append(items, s.toPublicWork(w))
	}
	page := &PublicPage{Items: items, NextCursor: nextCursor}
	_ = s.d.Cache.SetJSON(ctx, cacheKey, page, GalleryCacheTTL)
	return page, nil
}

func (s *Service) GetFeatured(ctx context.Context) (*PublicWork, error) {
	ver, _ := s.d.Cache.Version(ctx)
	cacheKey := fmt.Sprintf("works:v%d:featured", ver)

	type featuredCache struct {
		Work *PublicWork `json:"work"`
	}
	var cached featuredCache
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && hit {
		return cached.Work, nil
	}

	w, err := s.d.Works.FindFeatured(ctx)
	if errors.Is(err, ErrNotFound) {
		_ = s.d.Cache.SetJSON(ctx, cacheKey, featuredCache{Work: nil}, GalleryCacheTTL)
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	pw := s.toPublicWork(w)
	_ = s.d.Cache.SetJSON(ctx, cacheKey, featuredCache{Work: &pw}, GalleryCacheTTL)
	return &pw, nil
}

func (s *Service) ListExhibition(ctx context.Context) ([]PublicWork, error) {
	ver, _ := s.d.Cache.Version(ctx)
	cacheKey := fmt.Sprintf("works:v%d:exhibition", ver)

	var cached []PublicWork
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && hit {
		return cached, nil
	}

	rows, err := s.d.Works.ListExhibition(ctx, MaxExhibition)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	items := make([]PublicWork, 0, len(rows))
	for _, w := range rows {
		items = append(items, s.toPublicWork(w))
	}
	_ = s.d.Cache.SetJSON(ctx, cacheKey, items, GalleryCacheTTL)
	return items, nil
}

func (s *Service) GetFacets(ctx context.Context) (*Facets, error) {
	ver, _ := s.d.Cache.Version(ctx)
	cacheKey := fmt.Sprintf("works:v%d:facets", ver)

	var cached Facets
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && hit {
		return &cached, nil
	}

	facets, err := s.d.Works.GetFacets(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if facets.Categories == nil {
		facets.Categories = []string{}
	}
	if facets.TechStack == nil {
		facets.TechStack = []string{}
	}
	_ = s.d.Cache.SetJSON(ctx, cacheKey, facets, GalleryCacheTTL)
	return &facets, nil
}

func (s *Service) GetPublishedDetail(ctx context.Context, id string) (*PublicWorkDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errWorkNotFound
	}
	cacheKey := "work:" + id
	var cached PublicWorkDetail
	if hit, err := s.d.Cache.GetJSON(ctx, cacheKey, &cached); err == nil && hit {
		return &cached, nil
	}

	w, err := s.d.Works.FindByID(ctx, id)
	if errors.Is(err, ErrNotFound) || (err == nil && w.Status != StatusPublished) {
		return nil, errWorkNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}

	detail := s.toPublicWorkDetail(w)
	detail.Screenshots = s.loadScreenshots(ctx, w, true)
	_ = s.d.Cache.SetJSON(ctx, cacheKey, detail, DetailCacheTTL)
	return &detail, nil
}

func (s *Service) ListMyWorks(ctx context.Context, caller identity.Identity, f ManagedListFilter) (*ManagedPage, error) {
	f.OwnerID = caller.UserID
	return s.listManaged(ctx, f)
}

func (s *Service) GetMySummary(ctx context.Context, caller identity.Identity) (*DashboardSummary, error) {
	sum, err := s.d.Works.GetSummary(ctx, caller.UserID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &sum, nil
}

func (s *Service) GetManagedDetail(ctx context.Context, caller identity.Identity, id string) (*ManagedWork, error) {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		return nil, errWorkNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := checkOwnerOrAdmin(caller, w); err != nil {
		return nil, err
	}
	mw := s.toManagedWork(w)
	mw.Screenshots = s.loadScreenshots(ctx, w, w.Status == StatusPublished)
	return &mw, nil
}

func (s *Service) CreateWork(ctx context.Context, caller identity.Identity, in CreateWorkInput) (*ManagedWork, error) {
	title, summary, desc, benefits, cat, tech, team, demo, repo, fields := validateWorkFields(
		in.Title, in.Summary, in.Description, in.Benefits, in.Category, in.DemoURL, in.RepoURL, in.TechStack, in.TeamMembers,
	)
	contentType, ext, coverMsg := s.checkCover(in.Cover)
	if coverMsg != "" {
		fields["cover"] = coverMsg
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields)
	}

	workID, err := NewUUID()
	if err != nil {
		return nil, apperr.Internal(err)
	}
	coverKey, err := s.d.Media.PutCover(ctx, workID, in.Cover.Data, contentType, ext)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	now := s.d.Now()
	w := &Work{
		ID:               workID,
		OwnerID:          caller.UserID,
		Title:            title,
		Summary:          summary,
		Description:      desc,
		Benefits:         benefits,
		Category:         cat,
		TechStack:        tech,
		TeamMembers:      team,
		DemoURL:          demo,
		RepoURL:          repo,
		Status:           StatusDraft,
		IsFeatured:       false,
		CoverKey:         coverKey,
		CoverContentType: contentType,
		CoverSizeBytes:   int64(len(in.Cover.Data)),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.d.Works.Create(ctx, w); err != nil {
		s.deleteObjectAsync(coverKey)
		return nil, apperr.Internal(err)
	}

	if loaded, err := s.d.Works.FindByID(ctx, w.ID); err == nil {
		w = loaded
	}
	s.invalidateCache(ctx, w.ID)
	metrics.IncWorksAction("created")
	s.d.Logger.Info("audit: work created", "work_id", w.ID, "user_id", caller.UserID)

	mw := s.toManagedWork(w)
	return &mw, nil
}

func (s *Service) UpdateWork(ctx context.Context, caller identity.Identity, id string, in UpdateWorkInput) (*ManagedWork, error) {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		return nil, errWorkNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := checkOwnerOrAdmin(caller, w); err != nil {
		return nil, err
	}

	title, summary, desc, benefits, cat, tech, team, demo, repo, fields := validateWorkFields(
		in.Title, in.Summary, in.Description, in.Benefits, in.Category, in.DemoURL, in.RepoURL, in.TechStack, in.TeamMembers,
	)

	var (
		newCoverKey   string
		newCoverType  string
		newCoverExt   string
		newCoverBytes int64
		oldCoverKey   string
	)
	if in.Cover != nil && len(in.Cover.Data) > 0 {
		ct, ext, coverMsg := s.checkCover(in.Cover)
		if coverMsg != "" {
			fields["cover"] = coverMsg
		} else {
			newCoverType = ct
			newCoverExt = ext
			newCoverBytes = int64(len(in.Cover.Data))
		}
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields)
	}

	// The new object is uploaded first; the old one is deleted only after the row
	// update succeeds, and the new one is deleted if the update fails.
	if newCoverType != "" {
		key, err := s.d.Media.PutCover(ctx, w.ID, in.Cover.Data, newCoverType, newCoverExt)
		if err != nil {
			return nil, apperr.Internal(err)
		}
		newCoverKey = key
		oldCoverKey = w.CoverKey
		w.CoverKey = newCoverKey
		w.CoverContentType = newCoverType
		w.CoverSizeBytes = newCoverBytes
	}

	w.Title = title
	w.Summary = summary
	w.Description = desc
	w.Benefits = benefits
	w.Category = cat
	w.TechStack = tech
	w.TeamMembers = team
	w.DemoURL = demo
	w.RepoURL = repo
	w.UpdatedAt = s.d.Now()

	if err := s.d.Works.Update(ctx, w); err != nil {
		if newCoverKey != "" {
			s.deleteObjectAsync(newCoverKey)
		}
		if errors.Is(err, ErrNotFound) {
			return nil, errWorkNotFound
		}
		return nil, apperr.Internal(err)
	}

	if oldCoverKey != "" && oldCoverKey != newCoverKey {
		s.deleteObjectAsync(oldCoverKey)
	}

	s.invalidateCache(ctx, w.ID)
	metrics.IncWorksAction("edited")
	s.d.Logger.Info("audit: work edited", "work_id", w.ID, "user_id", caller.UserID)

	mw := s.toManagedWork(w)
	mw.Screenshots = s.loadScreenshots(ctx, w, w.Status == StatusPublished)
	return &mw, nil
}

func (s *Service) DeleteWork(ctx context.Context, caller identity.Identity, id string) error {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		return errWorkNotFound
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if err := checkOwnerOrAdmin(caller, w); err != nil {
		return err
	}

	wasPublished := w.Status == StatusPublished
	shots, shotsErr := s.d.Works.ListScreenshots(ctx, w.ID)
	if shotsErr != nil {
		s.d.Logger.Warn("list screenshots before delete", "work_id", w.ID, "error", shotsErr.Error())
	}
	if err := s.d.Works.SoftDelete(ctx, w.ID, s.d.Now()); err != nil {
		if errors.Is(err, ErrNotFound) {
			return errWorkNotFound
		}
		return apperr.Internal(err)
	}

	s.invalidateCache(ctx, w.ID)
	if w.CoverKey != "" {
		s.deleteObjectAsync(w.CoverKey)
	}
	for _, sh := range shots {
		s.deleteObjectAsync(sh.ObjectKey)
	}
	if wasPublished && s.d.Events != nil {
		if err := s.d.Events.PublishWorkEvent(ctx, EventWorkDeleted, WsIDPayload{ID: w.ID}); err != nil {
			s.d.Logger.Error("publish work.deleted event", "work_id", w.ID, "error", err.Error())
		}
	}
	metrics.IncWorksAction("deleted")
	s.d.Logger.Info("audit: work deleted", "work_id", w.ID, "user_id", caller.UserID, "was_published", wasPublished)
	return nil
}

func (s *Service) listManaged(ctx context.Context, f ManagedListFilter) (*ManagedPage, error) {
	if f.Limit <= 0 || f.Limit > MaxPageLimit {
		f.Limit = DefaultPageLimit
	}
	f.Query = strings.TrimSpace(f.Query)
	f.Status = strings.TrimSpace(f.Status)
	if f.Status != "" && !ValidStatus(f.Status) {
		return nil, apperr.Validation(map[string]string{"status": "Status harus Draft, Published, atau Unpublished"})
	}

	cursorTime, cursorID, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, apperr.New(apperr.KindInvalid, "invalid_cursor", "Cursor paginasi tidak valid")
	}

	rows, err := s.d.Works.ListManaged(ctx, ManagedListFilter{
		OwnerID: f.OwnerID, Query: f.Query, Status: f.Status, Limit: f.Limit + 1,
	}, cursorTime, cursorID)
	if err != nil {
		return nil, apperr.Internal(err)
	}

	var nextCursor *string
	if len(rows) > f.Limit {
		last := rows[f.Limit-1]
		rows = rows[:f.Limit]
		c := encodeCursor(last.CreatedAt, last.ID)
		nextCursor = &c
	}

	items := make([]ManagedWork, 0, len(rows))
	for _, w := range rows {
		items = append(items, s.toManagedWork(w))
	}
	return &ManagedPage{Items: items, NextCursor: nextCursor}, nil
}

func checkOwnerOrAdmin(caller identity.Identity, w *Work) error {
	if caller.Role == identity.RoleAdmin || (caller.UserID != "" && caller.UserID == w.OwnerID) {
		return nil
	}
	if w.Status == StatusPublished {
		return errForbidden
	}
	return errWorkNotFound
}

func (s *Service) toPublicWork(w *Work) PublicWork {
	var pubAt time.Time
	if w.PublishedAt != nil {
		pubAt = w.PublishedAt.UTC()
	} else {
		pubAt = w.CreatedAt.UTC()
	}
	return PublicWork{
		ID:          w.ID,
		Title:       w.Title,
		Summary:     w.Summary,
		Category:    w.Category,
		TechStack:   nonNilSlice(w.TechStack),
		TeamMembers: nonNilSlice(w.TeamMembers),
		CoverURL:    s.d.Media.CoverURL(w.ID, w.CoverKey, true),
		PublishedAt: pubAt,
		IsFeatured:  w.IsFeatured,
	}
}

func (s *Service) toPublicWorkDetail(w *Work) PublicWorkDetail {
	return PublicWorkDetail{
		PublicWork:  s.toPublicWork(w),
		Description: w.Description,
		Benefits:    w.Benefits,
		DemoURL:     w.DemoURL,
		RepoURL:     w.RepoURL,
		Screenshots: []ScreenshotDTO{},
	}
}

func (s *Service) toManagedWork(w *Work) ManagedWork {
	coverURL := s.d.Media.CoverURL(w.ID, w.CoverKey, w.Status == StatusPublished)
	var pubAt *time.Time
	if w.PublishedAt != nil {
		t := w.PublishedAt.UTC()
		pubAt = &t
	}
	return ManagedWork{
		ID:          w.ID,
		Title:       w.Title,
		Summary:     w.Summary,
		Description: w.Description,
		Benefits:    w.Benefits,
		Category:    w.Category,
		TechStack:   nonNilSlice(w.TechStack),
		TeamMembers: nonNilSlice(w.TeamMembers),
		CoverURL:    coverURL,
		DemoURL:     w.DemoURL,
		RepoURL:     w.RepoURL,
		Status:      w.Status,
		IsFeatured:  w.IsFeatured,
		OwnerName:   w.OwnerName,
		PublishedAt: pubAt,
		CreatedAt:   w.CreatedAt.UTC(),
		UpdatedAt:   w.UpdatedAt.UTC(),
		Screenshots: []ScreenshotDTO{},
	}
}

func (s *Service) invalidateCache(ctx context.Context, workID string) {
	if s.d.Cache == nil {
		return
	}
	_ = s.d.Cache.BumpVersion(ctx)
	if workID != "" {
		_ = s.d.Cache.Delete(ctx, "work:"+workID)
	}
}

func (s *Service) deleteObjectAsync(key string) {
	if s.d.Media == nil || key == "" {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := s.d.Media.DeleteObject(ctx, key); err != nil {
			s.d.Logger.Warn("delete storage object failed", "key", key, "error", err.Error())
		}
	}()
}

func nonNilSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func hashFilter(f PublicListFilter) string {
	raw := fmt.Sprintf("q=%s|c=%s|t=%s|cur=%s|l=%d",
		strings.ToLower(f.Query),
		strings.ToLower(f.Category),
		strings.ToLower(f.TechStack),
		f.Cursor,
		f.Limit,
	)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:12])
}

// checkCover validates the upload through the media module. A nil or empty
// upload is reported as a missing cover.
func (s *Service) checkCover(c *CoverUpload) (contentType, ext, problem string) {
	if c == nil {
		return s.d.Media.CheckImage(nil)
	}
	return s.d.Media.CheckImage(c.Data)
}

// CoverInfo lets the media module decide who may see a cover without reading the
// works table itself. A deleted or missing work is ErrNotFound.
func (s *Service) CoverInfo(ctx context.Context, id string) (*CoverInfo, error) {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(id))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &CoverInfo{
		CoverKey:    w.CoverKey,
		ContentType: w.CoverContentType,
		SizeBytes:   w.CoverSizeBytes,
		Published:   w.Status == StatusPublished,
	}, nil
}

// ---------------------------------------------------------------------------
// Screenshots (Should Have). Not realtime: like any edit, they show after a refresh.

var (
	errScreenshotNotFound = apperr.New(apperr.KindNotFound, "screenshot_not_found", "Screenshot tidak ditemukan")
	errScreenshotLimit    = apperr.New(apperr.KindConflict, "screenshot_limit", "Maksimal 5 screenshot per karya")
)

// loadScreenshots builds the DTOs for a work. A read failure is logged and gives an
// empty list, so a broken screenshot table never hides the work itself.
func (s *Service) loadScreenshots(ctx context.Context, w *Work, published bool) []ScreenshotDTO {
	rows, err := s.d.Works.ListScreenshots(ctx, w.ID)
	if err != nil {
		s.d.Logger.Error("list screenshots", "work_id", w.ID, "error", err.Error())
		return []ScreenshotDTO{}
	}
	out := make([]ScreenshotDTO, 0, len(rows))
	for _, sh := range rows {
		out = append(out, ScreenshotDTO{ID: sh.ID, URL: s.d.Media.ScreenshotURL(w.ID, sh.ID, sh.ObjectKey, published)})
	}
	return out
}

// AddScreenshot lets the owner or an Admin add one extra image (up to MaxScreenshots).
func (s *Service) AddScreenshot(ctx context.Context, caller identity.Identity, workID string, up *CoverUpload) (*ScreenshotDTO, error) {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(workID))
	if errors.Is(err, ErrNotFound) {
		return nil, errWorkNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if err := checkOwnerOrAdmin(caller, w); err != nil {
		return nil, err
	}

	var data []byte
	if up != nil {
		data = up.Data
	}
	contentType, ext, problem := s.d.Media.CheckImage(data)
	if problem != "" {
		problem = strings.ReplaceAll(problem, "cover", "gambar")
		return nil, apperr.Validation(map[string]string{"image": problem})
	}

	key, err := s.d.Media.PutScreenshot(ctx, w.ID, data, contentType, ext)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	id, err := NewUUID()
	if err != nil {
		s.deleteObjectAsync(key)
		return nil, apperr.Internal(err)
	}
	sh := &Screenshot{ID: id, WorkID: w.ID, ObjectKey: key, ContentType: contentType, SizeBytes: int64(len(data))}
	if err := s.d.Works.AddScreenshot(ctx, sh, MaxScreenshots); err != nil {
		s.deleteObjectAsync(key)
		switch {
		case errors.Is(err, ErrScreenshotLimit):
			return nil, errScreenshotLimit
		case errors.Is(err, ErrNotFound):
			return nil, errWorkNotFound
		}
		return nil, apperr.Internal(err)
	}

	s.invalidateCache(ctx, w.ID)
	metrics.IncWorksAction("screenshot_added")
	s.d.Logger.Info("audit: screenshot added", "work_id", w.ID, "image_id", id, "user_id", caller.UserID)
	return &ScreenshotDTO{ID: id, URL: s.d.Media.ScreenshotURL(w.ID, id, key, w.Status == StatusPublished)}, nil
}

// DeleteScreenshot removes one extra image; the object is deleted after the row.
func (s *Service) DeleteScreenshot(ctx context.Context, caller identity.Identity, workID, imageID string) error {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(workID))
	if errors.Is(err, ErrNotFound) {
		return errWorkNotFound
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if err := checkOwnerOrAdmin(caller, w); err != nil {
		return err
	}
	imageID = strings.TrimSpace(imageID)
	sh, err := s.d.Works.FindScreenshot(ctx, w.ID, imageID)
	if errors.Is(err, ErrScreenshotNotFound) {
		return errScreenshotNotFound
	}
	if err != nil {
		return apperr.Internal(err)
	}
	if err := s.d.Works.DeleteScreenshot(ctx, w.ID, sh.ID); err != nil {
		if errors.Is(err, ErrScreenshotNotFound) {
			return errScreenshotNotFound
		}
		return apperr.Internal(err)
	}
	s.deleteObjectAsync(sh.ObjectKey)
	s.invalidateCache(ctx, w.ID)
	metrics.IncWorksAction("screenshot_removed")
	s.d.Logger.Info("audit: screenshot removed", "work_id", w.ID, "image_id", sh.ID, "user_id", caller.UserID)
	return nil
}

// ScreenshotInfo lets the media module decide who may see a screenshot. A missing or
// deleted work or screenshot is ErrNotFound. CoverInfo is reused: CoverKey holds the
// screenshot's object key.
func (s *Service) ScreenshotInfo(ctx context.Context, workID, imageID string) (*CoverInfo, error) {
	w, err := s.d.Works.FindByID(ctx, strings.TrimSpace(workID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sh, err := s.d.Works.FindScreenshot(ctx, w.ID, strings.TrimSpace(imageID))
	if errors.Is(err, ErrScreenshotNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &CoverInfo{
		CoverKey:    sh.ObjectKey,
		ContentType: sh.ContentType,
		SizeBytes:   sh.SizeBytes,
		Published:   w.Status == StatusPublished,
	}, nil
}
