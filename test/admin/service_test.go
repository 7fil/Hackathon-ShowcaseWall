package admin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/admin/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
)

const (
	wid = "11111111-2222-4333-8444-555555555555"
)

var (
	admin       = identity.Identity{UserID: "a", Role: identity.RoleAdmin}
	contributor = identity.Identity{UserID: "c", Role: identity.RoleContributor}
)

type fakeRepo struct {
	work   *service.Work
	err    error
	called []string
	list   []*service.Work
	filter service.ListFilter
}

func (f *fakeRepo) List(_ context.Context, fl service.ListFilter, _ *time.Time, _ string) ([]*service.Work, error) {
	f.filter = fl
	return f.list, f.err
}
func (f *fakeRepo) Summary(context.Context) (service.DashboardSummary, error) {
	f.called = append(f.called, "summary")
	return service.DashboardSummary{TotalDraft: 1}, f.err
}
func (f *fakeRepo) Publish(context.Context, string, time.Time) (*service.Work, error) {
	f.called = append(f.called, "publish")
	return f.work, f.err
}
func (f *fakeRepo) Unpublish(context.Context, string, time.Time) (*service.Work, error) {
	f.called = append(f.called, "unpublish")
	return f.work, f.err
}
func (f *fakeRepo) SetFeatured(context.Context, string, bool, time.Time) (*service.Work, error) {
	f.called = append(f.called, "feature")
	return f.work, f.err
}

// order records the sequence commit, bump version, delete cache, publish event.
type order struct{ steps []string }

func (o *order) BumpVersion(context.Context) error { o.steps = append(o.steps, "bump"); return nil }
func (o *order) Delete(_ context.Context, keys ...string) error {
	o.steps = append(o.steps, "del:"+keys[0])
	return nil
}
func (o *order) PublishWorkEvent(_ context.Context, typ string, payload any) error {
	o.steps = append(o.steps, "event:"+typ)
	return nil
}

type fakeMedia struct{}

func (fakeMedia) CoverURL(id, _ string, published bool) string {
	if published {
		return "stable/" + id
	}
	return "signed/" + id
}
func (fakeMedia) StableCoverURL(id string) string { return "stable/" + id }

func newSvc(t *testing.T, repo *fakeRepo) (*service.Service, *order) {
	t.Helper()
	o := &order{}
	s, err := service.New(service.Deps{Works: repo, Cache: o, Events: o, Media: fakeMedia{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return s, o
}

func published() *service.Work {
	now := time.Now()
	return &service.Work{ID: wid, Title: "T", Status: service.StatusPublished, PublishedAt: &now, TechStack: []string{"Go"}}
}

func kind(err error) apperr.Kind { return apperr.As(err).Kind }

func TestContributorIsRefusedEverywhere(t *testing.T) {
	repo := &fakeRepo{work: published()}
	s, o := newSvc(t, repo)
	ctx := context.Background()
	_, e1 := s.ListWorks(ctx, contributor, service.ListFilter{})
	_, e2 := s.GetSummary(ctx, contributor)
	_, e3 := s.Publish(ctx, contributor, wid)
	_, e4 := s.Unpublish(ctx, contributor, wid)
	_, e5 := s.SetFeatured(ctx, contributor, wid, true)
	for i, err := range []error{e1, e2, e3, e4, e5} {
		if kind(err) != apperr.KindForbidden {
			t.Errorf("call %d: want 403, got %v", i, err)
		}
	}
	if len(repo.called) != 0 || len(o.steps) != 0 {
		t.Error("a refused caller must not reach the repository, cache or events")
	}
}

func TestPublishOrderAndPayload(t *testing.T) {
	repo := &fakeRepo{work: published()}
	s, o := newSvc(t, repo)
	mw, err := s.Publish(context.Background(), admin, wid)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bump", "del:work:" + wid, "event:work.published"}
	if len(o.steps) != 3 || o.steps[0] != want[0] || o.steps[1] != want[1] || o.steps[2] != want[2] {
		t.Errorf("order = %v, want %v", o.steps, want)
	}
	if mw.CoverURL != "stable/"+wid {
		t.Errorf("published work must carry the stable cover URL, got %q", mw.CoverURL)
	}
}

func TestPublishConflictAndNotFound(t *testing.T) {
	repo := &fakeRepo{err: service.ErrStatusConflict}
	s, o := newSvc(t, repo)
	if _, err := s.Publish(context.Background(), admin, wid); kind(err) != apperr.KindConflict {
		t.Errorf("want 409, got %v", err)
	}
	repo.err = service.ErrNotFound
	if _, err := s.Publish(context.Background(), admin, wid); kind(err) != apperr.KindNotFound {
		t.Errorf("want 404, got %v", err)
	}
	repo.err = errors.New("boom")
	if _, err := s.Publish(context.Background(), admin, wid); kind(err) != apperr.KindInternal {
		t.Errorf("want 500, got %v", err)
	}
	if len(o.steps) != 0 {
		t.Errorf("a failed publish must not touch cache or events: %v", o.steps)
	}
	if _, err := s.Publish(context.Background(), admin, "not-a-uuid"); kind(err) != apperr.KindNotFound {
		t.Errorf("malformed id must be 404, got %v", err)
	}
}

func TestUnpublishSendsEventAndConflicts(t *testing.T) {
	w := published()
	w.Status = service.StatusUnpublished
	repo := &fakeRepo{work: w}
	s, o := newSvc(t, repo)
	mw, err := s.Unpublish(context.Background(), admin, wid)
	if err != nil {
		t.Fatal(err)
	}
	if o.steps[len(o.steps)-1] != "event:work.unpublished" {
		t.Errorf("steps = %v", o.steps)
	}
	if mw.CoverURL != "signed/"+wid {
		t.Errorf("unpublished work needs a signed cover URL, got %q", mw.CoverURL)
	}
	repo.err = service.ErrStatusConflict
	if _, err := s.Unpublish(context.Background(), admin, wid); kind(err) != apperr.KindConflict {
		t.Errorf("want 409, got %v", err)
	}
}

func TestFeatureInvalidatesCacheButSendsNoEvent(t *testing.T) {
	w := published()
	w.IsFeatured = true
	repo := &fakeRepo{work: w}
	s, o := newSvc(t, repo)
	mw, err := s.SetFeatured(context.Background(), admin, wid, true)
	if err != nil || !mw.IsFeatured {
		t.Fatalf("got %v %v", mw, err)
	}
	for _, step := range o.steps {
		if step == "event:work.published" || step == "event:work.unpublished" {
			t.Errorf("featured change must not send an event: %v", o.steps)
		}
	}
	if len(o.steps) != 2 {
		t.Errorf("steps = %v, want bump + delete", o.steps)
	}
	repo.err = service.ErrStatusConflict
	if _, err := s.SetFeatured(context.Background(), admin, wid, true); kind(err) != apperr.KindConflict {
		t.Errorf("featuring a non-published work must be 409, got %v", err)
	}
}

func TestListValidationAndPaging(t *testing.T) {
	repo := &fakeRepo{}
	s, _ := newSvc(t, repo)
	ctx := context.Background()
	if _, err := s.ListWorks(ctx, admin, service.ListFilter{Status: "Bogus"}); kind(err) != apperr.KindValidation {
		t.Errorf("bad status must be 422, got %v", err)
	}
	if _, err := s.ListWorks(ctx, admin, service.ListFilter{Cursor: "%%%"}); kind(err) != apperr.KindInvalid {
		t.Errorf("bad cursor must be 400, got %v", err)
	}

	now := time.Now()
	for i := 0; i < 21; i++ {
		repo.list = append(repo.list, &service.Work{ID: wid, Status: service.StatusDraft, CreatedAt: now})
	}
	page, err := s.ListWorks(ctx, admin, service.ListFilter{Query: "  bagas ", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if repo.filter.Limit != service.MaxPageLimit+1 || repo.filter.Query != "bagas" {
		t.Errorf("filter not normalised: %+v", repo.filter)
	}
	if len(page.Items) != service.MaxPageLimit || page.NextCursor == nil {
		t.Errorf("want %d items and a next cursor, got %d / %v", service.MaxPageLimit, len(page.Items), page.NextCursor)
	}
	// The cursor it hands out must be accepted back.
	if _, err := s.ListWorks(ctx, admin, service.ListFilter{Cursor: *page.NextCursor}); err != nil {
		t.Errorf("own cursor rejected: %v", err)
	}
}
