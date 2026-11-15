package repository_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/repository"
	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

type flowWorks struct{ cover *service.WorkCover }

func (f *flowWorks) LookupCover(context.Context, string) (*service.WorkCover, error) {
	if f.cover == nil {
		return nil, service.ErrWorkNotFound
	}
	c := *f.cover
	return &c, nil
}

// TestCoverLifecycleOnLocalDisk runs the real media service on the real disk store:
// check -> store -> draft needs a signature -> published is public -> unpublished
// and deleted lose the image -> deleting the object leaves nothing on disk.
func TestCoverLifecycleOnLocalDisk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	disk, err := repository.NewLocalDiskStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	works := &flowWorks{}
	svc, err := service.New(service.Deps{Storage: disk, Works: works, SignSecret: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}

	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	ct, ext, problem := svc.CheckImage(img.Bytes())
	if problem != "" {
		t.Fatalf("valid png rejected: %s", problem)
	}
	if _, _, p := svc.CheckImage([]byte("<html>not an image</html>")); p == "" {
		t.Fatal("non-image must be rejected")
	}

	workID := "11111111-1111-4111-8111-111111111111"
	key, err := svc.PutCover(ctx, workID, img.Bytes(), ct, ext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key))); err != nil {
		t.Fatalf("object not on disk: %v", err)
	}
	works.cover = &service.WorkCover{CoverKey: key, ContentType: ct, SizeBytes: int64(img.Len())}

	// Draft: no signature -> hidden; owner's signed URL -> served.
	if _, err := svc.StreamCover(ctx, workID, "", ""); err == nil {
		t.Error("draft cover without signature must be refused")
	}
	u, _ := url.Parse(svc.CoverURL(workID, key, false))
	st, err := svc.StreamCover(ctx, workID, u.Query().Get("exp"), u.Query().Get("sig"))
	if err != nil {
		t.Fatalf("signed draft cover refused: %v", err)
	}
	got, _ := io.ReadAll(st.Body)
	st.Body.Close()
	if !bytes.Equal(got, img.Bytes()) || st.ContentType != "image/png" || st.Public {
		t.Errorf("draft stream wrong: %d bytes, type %q, public %v", len(got), st.ContentType, st.Public)
	}
	if _, err := svc.StreamCover(ctx, workID, u.Query().Get("exp"), "00"+u.Query().Get("sig")[2:]); err == nil {
		t.Error("altered signature must be refused")
	}

	// Published: public, stable URL, no signature needed.
	works.cover.Published = true
	st, err = svc.StreamCover(ctx, workID, "", "")
	if err != nil {
		t.Fatalf("published cover refused: %v", err)
	}
	st.Body.Close()
	if !st.Public || st.ETag == "" {
		t.Error("published stream must be public with an ETag")
	}

	// Unpublished again: image disappears at once, object still on disk.
	works.cover.Published = false
	if _, err := svc.StreamCover(ctx, workID, "", ""); err == nil {
		t.Error("unpublished cover must be hidden")
	}

	// Deleted work: 404 whatever the signature says.
	works.cover = nil
	if _, err := svc.StreamCover(ctx, workID, u.Query().Get("exp"), u.Query().Get("sig")); err == nil {
		t.Error("deleted work must have no image")
	}

	// Replace flow: new object, old one deleted, folder only keeps the new one.
	key2, err := svc.PutCover(ctx, workID, img.Bytes(), ct, ext)
	if err != nil || key2 == key {
		t.Fatalf("replacement must get a new key: %v", err)
	}
	if err := svc.DeleteObject(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := disk.Get(ctx, key); !errors.Is(err, service.ErrObjectNotFound) {
		t.Error("old cover should be gone")
	}
	if err := svc.DeleteObject(ctx, key2); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("storage dir should be empty after deleting everything, has %d entries", len(left))
	}
}
