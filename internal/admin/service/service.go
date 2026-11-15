package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/cache"
)

// Deps are the collaborators of the admin service.
type Deps struct {
	Works  WorkRepository
	Cache  Cache
	Events EventPublisher
	Media  CoverURLs
	Logger *slog.Logger
	Now    func() time.Time
}

// Service implements the admin module.
type Service struct {
	d Deps
}

// New validates the dependencies and returns the service.
func New(d Deps) (*Service, error) {
	if d.Works == nil || d.Media == nil {
		return nil, errors.New("admin: works repository and media are required")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{d: d}, nil
}

var (
	errWorkNotFound = apperr.New(apperr.KindNotFound, "work_not_found", "Karya tidak ditemukan")
	errForbidden    = apperr.New(apperr.KindForbidden, "forbidden", "Anda tidak memiliki akses")
)

// requireAdmin is the service-level role check. The router also guards these
// routes, but the rule lives here so REST and any other entry point share it.
func (s *Service) requireAdmin(caller identity.Identity) error {
	if caller.Role == identity.RoleAdmin {
		return nil
	}
	s.d.Logger.Warn("audit: permission denied", "user_id", caller.UserID, "action", "admin")
	return errForbidden
}

// ListWorks lists every work with its owner. Query matches title or owner name.
func (s *Service) ListWorks(ctx context.Context, caller identity.Identity, f ListFilter) (*ManagedPage, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	if f.Limit <= 0 || f.Limit > MaxPageLimit {
		f.Limit = DefaultPageLimit
	}
	f.Query = strings.TrimSpace(f.Query)
	f.Status = strings.TrimSpace(f.Status)
	if f.Status != "" && !validStatus(f.Status) {
		return nil, apperr.Validation(map[string]string{"status": "Status harus Draft, Published, atau Unpublished"})
	}
	cursorTime, cursorID, err := decodeCursor(f.Cursor)
	if err != nil {
		return nil, apperr.New(apperr.KindInvalid, "invalid_cursor", "Cursor paginasi tidak valid")
	}

	// One extra row tells whether another page exists.
	rows, err := s.d.Works.List(ctx, ListFilter{Query: f.Query, Status: f.Status, Limit: f.Limit + 1}, cursorTime, cursorID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	var next *string
	if len(rows) > f.Limit {
		last := rows[f.Limit-1]
		rows = rows[:f.Limit]
		c := encodeCursor(last.CreatedAt, last.ID)
		next = &c
	}
	items := make([]ManagedWork, 0, len(rows))
	for _, w := range rows {
		items = append(items, s.toManaged(w))
	}
	return &ManagedPage{Items: items, NextCursor: next}, nil
}

// GetSummary counts all works. It ignores the table filters by design.
func (s *Service) GetSummary(ctx context.Context, caller identity.Identity) (*DashboardSummary, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	sum, err := s.d.Works.Summary(ctx)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	return &sum, nil
}

// Publish moves a Draft or Unpublished work to Published (409 if already Published).
func (s *Service) Publish(ctx context.Context, caller identity.Identity, id string) (*ManagedWork, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if !isUUID(id) {
		return nil, errWorkNotFound
	}
	w, err := s.d.Works.Publish(ctx, id, s.d.Now())
	if err != nil {
		return nil, mapRepoErr(err, apperr.New(apperr.KindConflict, "already_published", "Karya sudah berstatus Published"))
	}

	s.afterCommit(ctx, w.ID)
	if w.PublishedAt != nil {
		s.publishEvent(ctx, EventWorkPublished, WsPublishedPayload{
			ID:          w.ID,
			Title:       w.Title,
			Summary:     w.Summary,
			Category:    w.Category,
			TechStack:   nonNil(w.TechStack),
			TeamMembers: nonNil(w.TeamMembers),
			CoverURL:    s.d.Media.StableCoverURL(w.ID),
			PublishedAt: w.PublishedAt.UTC(),
		}, w.ID)
	}
	s.d.Logger.Info("audit: work published", "work_id", w.ID, "admin_id", caller.UserID)

	mw := s.toManaged(w)
	return &mw, nil
}

// Unpublish moves a Published work to Unpublished and clears its featured mark
// (409 if the work is not Published).
func (s *Service) Unpublish(ctx context.Context, caller identity.Identity, id string) (*ManagedWork, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if !isUUID(id) {
		return nil, errWorkNotFound
	}
	w, err := s.d.Works.Unpublish(ctx, id, s.d.Now())
	if err != nil {
		return nil, mapRepoErr(err, apperr.New(apperr.KindConflict, "not_published", "Hanya karya berstatus Published yang dapat di-unpublish"))
	}

	s.afterCommit(ctx, w.ID)
	s.publishEvent(ctx, EventWorkUnpublished, WsIDPayload{ID: w.ID}, w.ID)
	s.d.Logger.Info("audit: work unpublished", "work_id", w.ID, "admin_id", caller.UserID)

	mw := s.toManaged(w)
	return &mw, nil
}

// SetFeatured sets or clears the featured mark of a Published work. Featuring a
// work replaces the previous one. No realtime event is sent: the change is
// visible after a refresh, but the cache is still invalidated.
func (s *Service) SetFeatured(ctx context.Context, caller identity.Identity, id string, featured bool) (*ManagedWork, error) {
	if err := s.requireAdmin(caller); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	if !isUUID(id) {
		return nil, errWorkNotFound
	}
	w, err := s.d.Works.SetFeatured(ctx, id, featured, s.d.Now())
	if err != nil {
		return nil, mapRepoErr(err, apperr.New(apperr.KindConflict, "feature_requires_published", "Hanya karya berstatus Published yang dapat dijadikan unggulan"))
	}

	s.afterCommit(ctx, w.ID)
	s.d.Logger.Info("audit: work featured changed", "work_id", w.ID, "is_featured", featured, "admin_id", caller.UserID)

	mw := s.toManaged(w)
	return &mw, nil
}

// ---------------------------------------------------------------------------
// helpers

func mapRepoErr(err error, conflict *apperr.Error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errWorkNotFound
	case errors.Is(err, ErrStatusConflict):
		return conflict
	default:
		return apperr.Internal(err)
	}
}

// afterCommit runs the first half of the order commit, bump version, delete the
// cached work, publish the event. The TTL on the cache is only a safety net.
func (s *Service) afterCommit(ctx context.Context, workID string) {
	if s.d.Cache == nil {
		return
	}
	if err := s.d.Cache.BumpVersion(ctx); err != nil {
		s.d.Logger.Error("cache bump version", "error", err.Error())
	}
	if err := s.d.Cache.Delete(ctx, cache.WorkKeyPrefix+workID); err != nil {
		s.d.Logger.Error("cache delete work", "work_id", workID, "error", err.Error())
	}
}

func (s *Service) publishEvent(ctx context.Context, typ string, payload any, workID string) {
	if s.d.Events == nil {
		return
	}
	if err := s.d.Events.PublishWorkEvent(ctx, typ, payload); err != nil {
		s.d.Logger.Error("publish realtime event", "type", typ, "work_id", workID, "error", err.Error())
	}
}

func (s *Service) toManaged(w *Work) ManagedWork {
	var pub *time.Time
	if w.PublishedAt != nil {
		t := w.PublishedAt.UTC()
		pub = &t
	}
	return ManagedWork{
		ID:          w.ID,
		Title:       w.Title,
		Summary:     w.Summary,
		Description: w.Description,
		Benefits:    w.Benefits,
		Category:    w.Category,
		TechStack:   nonNil(w.TechStack),
		TeamMembers: nonNil(w.TeamMembers),
		CoverURL:    s.d.Media.CoverURL(w.ID, w.CoverKey, w.Status == StatusPublished),
		DemoURL:     w.DemoURL,
		RepoURL:     w.RepoURL,
		Status:      w.Status,
		IsFeatured:  w.IsFeatured,
		OwnerName:   w.OwnerName,
		PublishedAt: pub,
		CreatedAt:   w.CreatedAt.UTC(),
		UpdatedAt:   w.UpdatedAt.UTC(),
	}
}

func validStatus(s string) bool {
	return s == StatusDraft || s == StatusPublished || s == StatusUnpublished
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

type pageCursor struct {
	Time time.Time `json:"t"`
	ID   string    `json:"id"`
}

func encodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(pageCursor{Time: t.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (*time.Time, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, "", err
	}
	var c pageCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, "", err
	}
	if c.ID == "" || c.Time.IsZero() || !isUUID(c.ID) {
		return nil, "", fmt.Errorf("invalid cursor")
	}
	return &c.Time, c.ID, nil
}

// isUUID keeps a malformed id from reaching PostgreSQL's uuid cast (a 500).
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}
