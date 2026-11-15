package works_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/identity"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/works/service"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
)

const (
	ownerID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	otherID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	workUID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// ---- fakes -----------------------------------------------------------------

type fakeRepo struct {
	work  *service.Work
	shots []service.Screenshot
}

func (f *fakeRepo) Create(context.Context, *service.Work) error         { return nil }
func (f *fakeRepo) Update(context.Context, *service.Work) error         { return nil }
func (f *fakeRepo) SoftDelete(context.Context, string, time.Time) error { return nil }
func (f *fakeRepo) ListPublished(context.Context, service.PublicListFilter, *time.Time, string) ([]*service.Work, error) {
	return nil, nil
}
func (f *fakeRepo) FindFeatured(context.Context) (*service.Work, error) {
	return nil, service.ErrNotFound
}
func (f *fakeRepo) ListExhibition(context.Context, int) ([]*service.Work, error) { return nil, nil }
func (f *fakeRepo) GetFacets(context.Context) (service.Facets, error)            { return service.Facets{}, nil }
func (f *fakeRepo) ListManaged(context.Context, service.ManagedListFilter, *time.Time, string) ([]*service.Work, error) {
	return nil, nil
}
func (f *fakeRepo) GetSummary(context.Context, string) (service.DashboardSummary, error) {
	return service.DashboardSummary{}, nil
}

func (f *fakeRepo) FindByID(_ context.Context, id string) (*service.Work, error) {
	if f.work == nil || f.work.ID != id {
		return nil, service.ErrNotFound
	}
	return f.work, nil
}
func (f *fakeRepo) ListScreenshots(context.Context, string) ([]service.Screenshot, error) {
	return f.shots, nil
}
func (f *fakeRepo) FindScreenshot(_ context.Context, _, imageID string) (*service.Screenshot, error) {
	for i := range f.shots {
		if f.shots[i].ID == imageID {
			return &f.shots[i], nil
		}
	}
	return nil, service.ErrScreenshotNotFound
}
func (f *fakeRepo) AddScreenshot(_ context.Context, sh *service.Screenshot, max int) error {
	if len(f.shots) >= max {
		return service.ErrScreenshotLimit
	}
	sh.Position = len(f.shots) + 1
	f.shots = append(f.shots, *sh)
	return nil
}
func (f *fakeRepo) DeleteScreenshot(_ context.Context, _, imageID string) error {
	for i := range f.shots {
		if f.shots[i].ID == imageID {
			f.shots = append(f.shots[:i], f.shots[i+1:]...)
			return nil
		}
	}
	return service.ErrScreenshotNotFound
}

type fakeMedia struct {
	deleted []string
	puts    int
}

func (m *fakeMedia) CheckImage(data []byte) (string, string, string) {
	if len(data) == 0 {
		return "", "", "File cover wajib diunggah"
	}
	if string(data) == "bad" {
		return "", "", "Format cover harus berupa gambar JPG, PNG, atau WebP"
	}
	return "image/png", ".png", ""
}
func (m *fakeMedia) PutCover(context.Context, string, []byte, string, string) (string, error) {
	return "cover-key", nil
}
func (m *fakeMedia) PutScreenshot(_ context.Context, workID string, _ []byte, _, ext string) (string, error) {
	m.puts++
	return "works/" + workID + "/shot-" + string(rune('a'+m.puts)) + ext, nil
}
func (m *fakeMedia) DeleteObject(_ context.Context, key string) error {
	m.deleted = append(m.deleted, key)
	return nil
}
func (m *fakeMedia) CoverURL(workID, _ string, published bool) string {
	if published {
		return "/stable/" + workID
	}
	return "/signed/" + workID
}
func (m *fakeMedia) ScreenshotURL(workID, imageID, _ string, published bool) string {
	if published {
		return "/stable/" + workID + "/" + imageID
	}
	return "/signed/" + workID + "/" + imageID
}

type fakeCache struct{ bumps int }

func (c *fakeCache) Version(context.Context) (int64, error)                    { return 1, nil }
func (c *fakeCache) BumpVersion(context.Context) error                         { c.bumps++; return nil }
func (c *fakeCache) GetJSON(context.Context, string, any) (bool, error)        { return false, nil }
func (c *fakeCache) SetJSON(context.Context, string, any, time.Duration) error { return nil }
func (c *fakeCache) Delete(context.Context, ...string) error                   { return nil }

func newShotTestSvc(status string) (*service.Service, *fakeRepo, *fakeMedia, *fakeCache) {
	repo := &fakeRepo{work: &service.Work{ID: workUID, OwnerID: ownerID, Status: status, CoverKey: "cover-key"}}
	media := &fakeMedia{}
	cache := &fakeCache{}
	s := service.New(service.Deps{
		Works:  repo,
		Media:  media,
		Cache:  cache,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return s, repo, media, cache
}

func asKind(t *testing.T, err error, kind apperr.Kind) {
	t.Helper()
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Kind != kind {
		t.Fatalf("want error kind %v, got %v", kind, err)
	}
}

var png = &service.CoverUpload{Data: []byte("png-bytes")}

// ---- tests -----------------------------------------------------------------

func TestOwnerAddsScreenshotAndCacheIsInvalidated(t *testing.T) {
	s, repo, _, cache := newShotTestSvc(service.StatusDraft)
	dto, err := s.AddScreenshot(context.Background(), identity.Identity{UserID: ownerID, Role: identity.RoleContributor}, workUID, png)
	if err != nil {
		t.Fatal(err)
	}
	if len(repo.shots) != 1 || dto.ID == "" {
		t.Fatalf("screenshot not stored: %+v", repo.shots)
	}
	if dto.URL == "" || dto.URL[:8] != "/signed/" {
		t.Fatalf("a Draft work must get a signed URL, got %q", dto.URL)
	}
	if cache.bumps == 0 {
		t.Fatal("the cache version must be bumped after a screenshot change")
	}
}

func TestPublishedWorkScreenshotURLIsStable(t *testing.T) {
	s, _, _, _ := newShotTestSvc(service.StatusPublished)
	dto, err := s.AddScreenshot(context.Background(), identity.Identity{UserID: ownerID, Role: identity.RoleContributor}, workUID, png)
	if err != nil {
		t.Fatal(err)
	}
	if dto.URL[:8] != "/stable/" {
		t.Fatalf("a Published work must get a stable URL, got %q", dto.URL)
	}
}

func TestAdminMayAddScreenshotToAnyWork(t *testing.T) {
	s, _, _, _ := newShotTestSvc(service.StatusDraft)
	if _, err := s.AddScreenshot(context.Background(), identity.Identity{UserID: otherID, Role: identity.RoleAdmin}, workUID, png); err != nil {
		t.Fatalf("admin refused: %v", err)
	}
}

func TestOtherContributorCannotAddOrDelete(t *testing.T) {
	other := identity.Identity{UserID: otherID, Role: identity.RoleContributor}

	// Draft: the work's existence is hidden (404).
	s, repo, media, _ := newShotTestSvc(service.StatusDraft)
	_, err := s.AddScreenshot(context.Background(), other, workUID, png)
	asKind(t, err, apperr.KindNotFound)
	if len(repo.shots) != 0 || media.puts != 0 {
		t.Fatal("nothing may be stored for a refused caller")
	}

	// Published: 403.
	s, repo, _, _ = newShotTestSvc(service.StatusPublished)
	_, err = s.AddScreenshot(context.Background(), other, workUID, png)
	asKind(t, err, apperr.KindForbidden)

	repo.shots = []service.Screenshot{{ID: "s1", WorkID: workUID, ObjectKey: "k1"}}
	err = s.DeleteScreenshot(context.Background(), other, workUID, "s1")
	asKind(t, err, apperr.KindForbidden)
	if len(repo.shots) != 1 {
		t.Fatal("the screenshot must not be deleted by another contributor")
	}
}

func TestNoTokenIdentityIsRefused(t *testing.T) {
	s, _, _, _ := newShotTestSvc(service.StatusPublished)
	if _, err := s.AddScreenshot(context.Background(), identity.Identity{}, workUID, png); err == nil {
		t.Fatal("an empty identity must be refused")
	}
}

func TestScreenshotLimitIsFive(t *testing.T) {
	s, repo, media, _ := newShotTestSvc(service.StatusDraft)
	owner := identity.Identity{UserID: ownerID, Role: identity.RoleContributor}
	for i := 0; i < service.MaxScreenshots; i++ {
		if _, err := s.AddScreenshot(context.Background(), owner, workUID, png); err != nil {
			t.Fatalf("screenshot %d refused: %v", i+1, err)
		}
	}
	_, err := s.AddScreenshot(context.Background(), owner, workUID, png)
	asKind(t, err, apperr.KindConflict)
	if len(repo.shots) != service.MaxScreenshots {
		t.Fatalf("want %d screenshots, got %d", service.MaxScreenshots, len(repo.shots))
	}
	s.Close()
	if len(media.deleted) != 1 {
		t.Fatalf("the object of the refused upload must be removed, got %v", media.deleted)
	}
}

func TestScreenshotImageValidation(t *testing.T) {
	s, _, _, _ := newShotTestSvc(service.StatusDraft)
	owner := identity.Identity{UserID: ownerID, Role: identity.RoleContributor}

	_, err := s.AddScreenshot(context.Background(), owner, workUID, nil)
	asKind(t, err, apperr.KindValidation)

	_, err = s.AddScreenshot(context.Background(), owner, workUID, &service.CoverUpload{Data: []byte("bad")})
	asKind(t, err, apperr.KindValidation)
	var ae *apperr.Error
	errors.As(err, &ae)
	if msg := ae.Fields["image"]; msg == "" || containsWord(msg, "cover") {
		t.Fatalf("the field message must name the image, not the cover: %q", msg)
	}
}

func TestDeleteScreenshotRemovesRowAndObject(t *testing.T) {
	s, repo, media, cache := newShotTestSvc(service.StatusDraft)
	repo.shots = []service.Screenshot{{ID: "s1", WorkID: workUID, ObjectKey: "works/x/shot-a.png"}}
	owner := identity.Identity{UserID: ownerID, Role: identity.RoleContributor}

	if err := s.DeleteScreenshot(context.Background(), owner, workUID, "s1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if len(repo.shots) != 0 {
		t.Fatal("row must be deleted")
	}
	if len(media.deleted) != 1 || media.deleted[0] != "works/x/shot-a.png" {
		t.Fatalf("object must be deleted, got %v", media.deleted)
	}
	if cache.bumps == 0 {
		t.Fatal("cache must be invalidated")
	}

	err := s.DeleteScreenshot(context.Background(), owner, workUID, "s1")
	asKind(t, err, apperr.KindNotFound)
}

func TestScreenshotInfoReflectsPublishedStatus(t *testing.T) {
	s, repo, _, _ := newShotTestSvc(service.StatusPublished)
	repo.shots = []service.Screenshot{{ID: "s1", WorkID: workUID, ObjectKey: "k1", ContentType: "image/png", SizeBytes: 9}}

	info, err := s.ScreenshotInfo(context.Background(), workUID, "s1")
	if err != nil || !info.Published || info.CoverKey != "k1" {
		t.Fatalf("got %+v, %v", info, err)
	}

	repo.work.Status = service.StatusUnpublished
	info, err = s.ScreenshotInfo(context.Background(), workUID, "s1")
	if err != nil || info.Published {
		t.Fatalf("a hidden work's screenshot must not be public: %+v, %v", info, err)
	}

	if _, err := s.ScreenshotInfo(context.Background(), workUID, "missing"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a missing screenshot must be ErrNotFound, got %v", err)
	}
	if _, err := s.ScreenshotInfo(context.Background(), "dddddddd-dddd-4ddd-8ddd-dddddddddddd", "s1"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("a missing work must be ErrNotFound, got %v", err)
	}
}

func TestPublicDetailListsScreenshotsWithStableURLs(t *testing.T) {
	s, repo, _, _ := newShotTestSvc(service.StatusPublished)
	repo.shots = []service.Screenshot{{ID: "s1", WorkID: workUID, ObjectKey: "k1"}, {ID: "s2", WorkID: workUID, ObjectKey: "k2"}}

	detail, err := s.GetPublishedDetail(context.Background(), workUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Screenshots) != 2 || detail.Screenshots[0].URL != "/stable/"+workUID+"/s1" {
		t.Fatalf("unexpected screenshots: %+v", detail.Screenshots)
	}
}

func TestPublicDetailOfDraftIsNotFoundEvenWithScreenshots(t *testing.T) {
	s, repo, _, _ := newShotTestSvc(service.StatusDraft)
	repo.shots = []service.Screenshot{{ID: "s1", WorkID: workUID, ObjectKey: "k1"}}
	_, err := s.GetPublishedDetail(context.Background(), workUID)
	asKind(t, err, apperr.KindNotFound)
}

func containsWord(s, w string) bool {
	for i := 0; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] == w {
			return true
		}
	}
	return false
}
