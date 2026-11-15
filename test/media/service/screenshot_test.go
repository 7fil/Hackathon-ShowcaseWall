package service_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

const imageID = "99999999-8888-4777-8666-555555555555"

type fakeShots struct {
	shot *service.WorkCover
	err  error
}

func (f *fakeShots) LookupScreenshot(context.Context, string, string) (*service.WorkCover, error) {
	return f.shot, f.err
}

func newShotSvc(t *testing.T, shots *fakeShots, now time.Time) (*service.Service, *memStorage) {
	t.Helper()
	clock := now
	return newShotSvcWithClock(t, shots, &clock)
}

func newShotSvcWithClock(t *testing.T, shots *fakeShots, clock *time.Time) (*service.Service, *memStorage) {
	t.Helper()
	s, st := newSvcWithClock(t, &fakeWorks{}, clock)
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
	shots := &fakeShots{shot: &service.WorkCover{CoverKey: "works/x/shot-a.png", ContentType: "image/png", Published: true}}
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
	const key = "works/x/shot-a.png"
	clock := time.Now()
	shots := &fakeShots{shot: &service.WorkCover{CoverKey: key, ContentType: "image/png", Published: false}}
	s, st := newShotSvcWithClock(t, shots, &clock)
	st.objects[key] = []byte("img")

	opens := func(exp, sig string) bool {
		stream, err := s.StreamScreenshot(context.Background(), workID, imageID, exp, sig)
		if err != nil {
			return false
		}
		stream.Body.Close()
		return true
	}

	// No signature: refused as 404 so the work's existence is not revealed.
	if opens("", "") {
		t.Fatal("expected refusal without a signature")
	}

	// A valid signature works.
	exp, sig := signedParams(t, s.ScreenshotURL(workID, imageID, key, false))
	if !opens(exp, sig) {
		t.Fatal("valid signature refused")
	}

	// Altered, and a cover signature reused for a screenshot, are refused.
	if opens(exp, flipLastHexDigit(sig)) {
		t.Fatal("altered signature must be refused")
	}
	coverExp, coverSig := signedParams(t, s.SignedCoverURL(workID, key))
	if opens(coverExp, coverSig) {
		t.Fatal("a cover signature must not open a screenshot")
	}

	// Once the clock passes the signature's lifetime it is refused as well.
	clock = clock.Add(service.SignedURLTTL + time.Second)
	if opens(exp, sig) {
		t.Fatal("expired signature must be refused")
	}
}

func TestStreamScreenshotMissingOrMalformed(t *testing.T) {
	shots := &fakeShots{err: service.ErrWorkNotFound}
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
	if _, err := unbound.StreamScreenshot(context.Background(), workID, imageID, "", ""); err == nil || errors.Is(err, service.ErrWorkNotFound) {
		t.Fatal("an unbound lookup must be an internal error")
	}
}
