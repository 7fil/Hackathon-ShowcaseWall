package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
)

const workID = "11111111-2222-4333-8444-555555555555"

type memStorage struct{ objects map[string][]byte }

func (m *memStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	m.objects[key] = data
	return nil
}
func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	b, ok := m.objects[key]
	if !ok {
		return nil, "", 0, ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), "application/octet-stream", int64(len(b)), nil
}
func (m *memStorage) Delete(_ context.Context, key string) error {
	delete(m.objects, key)
	return nil
}

type fakeWorks struct {
	cover *WorkCover
	err   error
}

func (f *fakeWorks) LookupCover(context.Context, string) (*WorkCover, error) { return f.cover, f.err }

func newSvc(t *testing.T, w *fakeWorks, now time.Time) (*Service, *memStorage) {
	t.Helper()
	st := &memStorage{objects: map[string][]byte{}}
	s, err := New(Deps{
		Storage:    st,
		Works:      w,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		SignSecret: []byte("0123456789abcdef0123456789abcdef"),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func jpegBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func webpBytes() []byte {
	payload := []byte("VP8 \x04\x00\x00\x00abcd")
	size := len(payload) + 4
	hdr := []byte{'R', 'I', 'F', 'F', byte(size), byte(size >> 8), 0, 0, 'W', 'E', 'B', 'P'}
	return append(hdr, payload...)
}

func TestCheckImageAccepts(t *testing.T) {
	s, _ := newSvc(t, &fakeWorks{}, time.Now())
	cases := []struct {
		name, ct, ext string
		data          []byte
	}{
		{"png", "image/png", ".png", pngBytes(t)},
		{"jpeg", "image/jpeg", ".jpg", jpegBytes(t)},
		{"webp", "image/webp", ".webp", webpBytes()},
	}
	for _, c := range cases {
		ct, ext, problem := s.CheckImage(c.data)
		if problem != "" || ct != c.ct || ext != c.ext {
			t.Errorf("%s: got %q %q %q", c.name, ct, ext, problem)
		}
	}
}

func TestCheckImageRejects(t *testing.T) {
	s, _ := newSvc(t, &fakeWorks{}, time.Now())
	cases := map[string][]byte{
		"empty":          nil,
		"text file":      []byte("<html>not an image</html>"),
		"gif":            []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"oversize":       append(pngBytes(t), make([]byte, MaxImageBytes)...),
		"truncated png":  pngBytes(t)[:20],
		"bad webp chunk": append([]byte("RIFF\x10\x00\x00\x00WEBPXXXX"), make([]byte, 8)...),
	}
	for name, data := range cases {
		if _, _, problem := s.CheckImage(data); problem == "" {
			t.Errorf("%s: expected a problem", name)
		}
	}
}

func TestPutCoverKey(t *testing.T) {
	s, st := newSvc(t, &fakeWorks{}, time.Now())
	key, err := s.PutCover(context.Background(), workID, []byte("x"), "image/png", ".png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "works/"+workID+"/cover-") || !strings.HasSuffix(key, ".png") {
		t.Fatalf("unexpected key %q", key)
	}
	if _, ok := st.objects[key]; !ok {
		t.Fatal("object not stored")
	}
	if _, err := s.PutCover(context.Background(), "../evil", []byte("x"), "image/png", ".png"); err == nil {
		t.Error("invalid work id must be refused")
	}
	if _, err := s.PutCover(context.Background(), workID, []byte("x"), "text/html", ".html"); err == nil {
		t.Error("unexpected extension must be refused")
	}
}

func TestSignedURLVerification(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s, _ := newSvc(t, &fakeWorks{}, now)
	url := s.SignedCoverURL(workID, "works/k/cover.png")
	q := url[strings.Index(url, "?")+1:]
	var exp, sig string
	for _, kv := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k == "exp" {
			exp = v
		} else if k == "sig" {
			sig = v
		}
	}
	if !s.verify(workID, "works/k/cover.png", exp, sig) {
		t.Fatal("fresh signature must verify")
	}
	if s.verify(workID, "works/other/cover.png", exp, sig) {
		t.Error("signature for another key must fail (cover replaced)")
	}
	if s.verify(workID, "works/k/cover.png", exp, sig[:len(sig)-1]+"0") && sig[len(sig)-1] != '0' {
		t.Error("altered signature must fail")
	}
	if s.verify("99999999-2222-4333-8444-555555555555", "works/k/cover.png", exp, sig) {
		t.Error("signature for another work must fail")
	}
	s.d.Now = func() time.Time { return now.Add(SignedURLTTL + time.Second) }
	if s.verify(workID, "works/k/cover.png", exp, sig) {
		t.Error("expired signature must fail")
	}
	if s.StableCoverURL(workID) != "/api/v1/media/works/"+workID+"/cover" {
		t.Error("stable URL changed")
	}
	if got := s.CoverURL(workID, "k", true); strings.Contains(got, "sig=") {
		t.Error("published cover must use the stable URL")
	}
}

func TestStreamCover(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	works := &fakeWorks{cover: &WorkCover{CoverKey: "works/k/cover.png", ContentType: "image/png", Published: true}}
	s, st := newSvc(t, works, now)
	st.objects["works/k/cover.png"] = []byte("PNGDATA")
	ctx := context.Background()

	// Published: no signature needed.
	stream, err := s.StreamCover(ctx, workID, "", "")
	if err != nil {
		t.Fatalf("published cover: %v", err)
	}
	stream.Body.Close()
	if !stream.Public || stream.ContentType != "image/png" || stream.ETag == "" {
		t.Errorf("unexpected stream %+v", stream)
	}

	// Hidden: the same request now answers 404.
	works.cover = &WorkCover{CoverKey: "works/k/cover.png", ContentType: "image/png", Published: false}
	assertNotFound(t, s, workID, "", "")

	// Hidden with a valid signature: owner or Admin may see it.
	url := s.SignedCoverURL(workID, "works/k/cover.png")
	q := url[strings.Index(url, "?")+1:]
	parts := strings.Split(q, "&")
	exp, sig := strings.TrimPrefix(parts[0], "exp="), strings.TrimPrefix(parts[1], "sig=")
	signed, err := s.StreamCover(ctx, workID, exp, sig)
	if err != nil {
		t.Fatalf("signed draft cover: %v", err)
	}
	signed.Body.Close()
	if signed.Public {
		t.Error("draft stream must not be public")
	}

	// Tampered, expired and missing signatures are refused.
	assertNotFound(t, s, workID, exp, sig[:len(sig)-2]+"00")
	s.d.Now = func() time.Time { return now.Add(time.Hour) }
	assertNotFound(t, s, workID, exp, sig)
	s.d.Now = func() time.Time { return now }

	// Deleted work, malformed id, missing object.
	works.err = ErrWorkNotFound
	assertNotFound(t, s, workID, "", "")
	works.err = nil
	assertNotFound(t, s, "not-a-uuid", "", "")
	works.cover = &WorkCover{CoverKey: "works/gone.png", Published: true}
	assertNotFound(t, s, workID, "", "")

	// A lookup failure is an internal error, not a 404.
	works.err = errors.New("db down")
	if _, err := s.StreamCover(ctx, workID, "", ""); apperr.As(err).Kind != apperr.KindInternal {
		t.Error("lookup failure must be internal")
	}
}

func assertNotFound(t *testing.T, s *Service, id, exp, sig string) {
	t.Helper()
	_, err := s.StreamCover(context.Background(), id, exp, sig)
	if err == nil || apperr.As(err).Kind != apperr.KindNotFound {
		t.Errorf("expected 404 for id=%q exp=%q, got %v", id, exp, err)
	}
}
