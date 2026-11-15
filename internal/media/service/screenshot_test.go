package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const imageID = "99999999-8888-4777-8666-555555555555"

type fakeShots struct {
	shot *WorkCover
	err  error
}

func (f *fakeShots) LookupScreenshot(context.Context, string, string) (*WorkCover, error) {
	return f.shot, f.err
}

func newShotSvc(t *testing.T, shots *fakeShots, now time.Time) (*Service, *memStorage) {
	t.Helper()
	s, st := newSvc(t, &fakeWorks{}, now)
	s.SetScreenshotLookup(shots)
	return s, st
}

func TestPutScreenshotKey(t *testing.T) {
	s, st := newSvc(t, &fakeWorks{}, time.Now())
	key, err := s.PutScreenshot(context.Background(), workID, []byte("x"), "image/png", ".png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "works/"+workID+"/shot-") || !strings.HasSuffix(key, ".png") {
		t.Fatalf("unexpected key %q", key)
	}
	if _, ok := st.objects[key]; !ok {
		t.Fatal("object was not stored")
	}
	if _, err := s.PutScreenshot(context.Background(), workID, []byte("x"), "image/gif", ".gif"); err == nil {
		t.Fatal("an unsupported extension must be refused")
	}
	if _, err := s.PutScreenshot(context.Background(), "not-a-uuid", []byte("x"), "image/png", ".png"); err == nil {
		t.Fatal("a malformed work id must be refused")
	}
}

func TestScreenshotURLStableForPublishedSignedOtherwise(t *testing.T) {
	s, _ := newSvc(t, &fakeWorks{}, time.Now())
	pub := s.ScreenshotURL(workID, imageID, "k", true)
	if pub != "/api/v1/media/works/"+workID+"/screenshots/"+imageID {
		t.Fatalf("published URL must be stable and unsigned, got %q", pub)
	}
	priv := s.ScreenshotURL(workID, imageID, "k", false)
	if !strings.Contains(priv, "exp=") || !strings.Contains(priv, "sig=") {
		t.Fatalf("non-published URL must be signed, got %q", priv)
	}
}

func TestStreamScreenshotPublishedNeedsNoSignature(t *testing.T) {
	shots := &fakeShots{shot: &WorkCover{CoverKey: "works/x/shot-a.png", ContentType: "image/png", Published: true}}
	s, st := newShotSvc(t, shots, time.Now())
	st.objects["works/x/shot-a.png"] = []byte("img")

	stream, err := s.StreamScreenshot(context.Background(), workID, imageID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	b, _ := io.ReadAll(stream.Body)
	if string(b) != "img" || !stream.Public {
		t.Fatalf("got body %q public=%v", b, stream.Public)
	}
}

func TestStreamScreenshotNotPublishedRules(t *testing.T) {
	now := time.Now()
	shots := &fakeShots{shot: &WorkCover{CoverKey: "works/x/shot-a.png", ContentType: "image/png", Published: false}}
	s, st := newShotSvc(t, shots, now)
	st.objects["works/x/shot-a.png"] = []byte("img")

	// No signature: refused as 404 so the work's existence is not revealed.
	if _, err := s.StreamScreenshot(context.Background(), workID, imageID, "", ""); err == nil {
		t.Fatal("expected refusal without a signature")
	}

	// A valid signature works.
	exp := now.Add(time.Minute).Unix()
	sig := s.signScreenshot(workID, imageID, "works/x/shot-a.png", exp)
	expStr := formatInt(exp)
	stream, err := s.StreamScreenshot(context.Background(), workID, imageID, expStr, sig)
	if err != nil {
		t.Fatalf("valid signature refused: %v", err)
	}
	stream.Body.Close()

	// Expired, altered, and a cover signature reused for a screenshot are all refused.
	expired := now.Add(-time.Minute).Unix()
	if _, err := s.StreamScreenshot(context.Background(), workID, imageID, formatInt(expired), s.signScreenshot(workID, imageID, "works/x/shot-a.png", expired)); err == nil {
		t.Fatal("expired signature must be refused")
	}
	if _, err := s.StreamScreenshot(context.Background(), workID, imageID, expStr, sig[:len(sig)-2]+"00"); err == nil {
		t.Fatal("altered signature must be refused")
	}
	coverSig := s.sign(workID, "works/x/shot-a.png", exp)
	if _, err := s.StreamScreenshot(context.Background(), workID, imageID, expStr, coverSig); err == nil {
		t.Fatal("a cover signature must not open a screenshot")
	}
}

func TestStreamScreenshotMissingOrMalformed(t *testing.T) {
	shots := &fakeShots{err: ErrWorkNotFound}
	s, _ := newShotSvc(t, shots, time.Now())
	if _, err := s.StreamScreenshot(context.Background(), workID, imageID, "", ""); err == nil {
		t.Fatal("a missing screenshot must be 404")
	}
	if _, err := s.StreamScreenshot(context.Background(), "bad", imageID, "", ""); err == nil {
		t.Fatal("a malformed work id must be 404")
	}
	if _, err := s.StreamScreenshot(context.Background(), workID, "bad", "", ""); err == nil {
		t.Fatal("a malformed image id must be 404")
	}

	unbound, _ := newSvc(t, &fakeWorks{}, time.Now())
	if _, err := unbound.StreamScreenshot(context.Background(), workID, imageID, "", ""); err == nil || errors.Is(err, ErrWorkNotFound) {
		t.Fatal("an unbound lookup must be an internal error")
	}
}

func formatInt(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}
