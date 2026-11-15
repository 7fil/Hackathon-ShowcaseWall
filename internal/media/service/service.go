package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/apperr"
	"github.com/7fil/Hackathon-ShowcaseWall.git/pkg/metrics"
)

// Deps are the collaborators of the media service.
type Deps struct {
	Storage    ObjectStorage
	Works      WorkLookup // may be set later with SetWorkLookup to break the wiring cycle
	Shots      ScreenshotLookup
	Logger     *slog.Logger
	SignSecret []byte
	Now        func() time.Time
}

// Service implements the media module.
type Service struct {
	d Deps
}

// New validates the dependencies and returns the service.
func New(d Deps) (*Service, error) {
	if d.Storage == nil {
		return nil, errors.New("media: storage is required")
	}
	if len(d.SignSecret) < 16 {
		return nil, errors.New("media: sign secret must be at least 16 bytes")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Service{d: d}, nil
}

// SetWorkLookup binds the works lookup. Call it once while wiring, before the
// server accepts traffic (works needs media and media needs works).
func (s *Service) SetWorkLookup(l WorkLookup) { s.d.Works = l }

// SetScreenshotLookup binds the screenshot lookup, with the same rules as SetWorkLookup.
func (s *Service) SetScreenshotLookup(l ScreenshotLookup) { s.d.Shots = l }

var errMediaNotFound = apperr.New(apperr.KindNotFound, "media_not_found", "Gambar tidak ditemukan")

// ---------------------------------------------------------------------------
// Upload checks and storage

// CheckImage inspects the bytes (not the file name or the declared type) and
// accepts only JPEG, PNG and WebP up to MaxImageBytes. On success it returns the
// detected content type and file extension and an empty problem. On failure the
// problem is a message that can be shown next to the form field.
func (s *Service) CheckImage(data []byte) (contentType, ext, problem string) {
	if len(data) == 0 {
		return "", "", "File cover wajib diunggah"
	}
	if len(data) > MaxImageBytes {
		return "", "", "Ukuran file cover maksimal 5MB"
	}
	const badType = "Format cover harus berupa gambar JPG, PNG, atau WebP"
	const badImage = "File gambar rusak atau tidak dapat dibaca"

	var pixels int64
	switch sniff(data) {
	case "image/jpeg":
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", "", badImage
		}
		contentType, ext, pixels = "image/jpeg", ".jpg", int64(cfg.Width)*int64(cfg.Height)
	case "image/png":
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return "", "", badImage
		}
		contentType, ext, pixels = "image/png", ".png", int64(cfg.Width)*int64(cfg.Height)
	case "image/webp":
		if !validWebP(data) {
			return "", "", badImage
		}
		contentType, ext = "image/webp", ".webp"
	default:
		return "", "", badType
	}
	if pixels > MaxImagePixels {
		return "", "", "Dimensi gambar terlalu besar"
	}
	return contentType, ext, ""
}

// PutCover stores the bytes under a random key works/{workID}/cover-{uuid}{ext} and
// returns the key. Only this key is saved in the works row, never a URL.
func (s *Service) PutCover(ctx context.Context, workID string, data []byte, contentType, ext string) (string, error) {
	if !isUUID(workID) {
		return "", errors.New("media: invalid work id")
	}
	switch ext {
	case ".jpg", ".png", ".webp":
	default:
		return "", fmt.Errorf("media: unsupported extension %q", ext)
	}
	name, err := newUUID()
	if err != nil {
		return "", err
	}
	key := "works/" + workID + "/cover-" + name + ext
	if err := s.d.Storage.Put(ctx, key, data, contentType); err != nil {
		return "", err
	}
	return key, nil
}

// PutScreenshot stores an extra screenshot under works/{workID}/shot-{uuid}{ext}
// and returns the key. The checks are the same as for a cover (CheckImage).
func (s *Service) PutScreenshot(ctx context.Context, workID string, data []byte, contentType, ext string) (string, error) {
	if !isUUID(workID) {
		return "", errors.New("media: invalid work id")
	}
	switch ext {
	case ".jpg", ".png", ".webp":
	default:
		return "", fmt.Errorf("media: unsupported extension %q", ext)
	}
	name, err := newUUID()
	if err != nil {
		return "", err
	}
	key := "works/" + workID + "/shot-" + name + ext
	if err := s.d.Storage.Put(ctx, key, data, contentType); err != nil {
		return "", err
	}
	return key, nil
}

// DeleteObject removes one object. A missing object is not an error.
func (s *Service) DeleteObject(ctx context.Context, key string) error {
	if key == "" {
		return nil
	}
	return s.d.Storage.Delete(ctx, key)
}

// ---------------------------------------------------------------------------
// URLs

// StableCoverURL is the never-expiring URL of a Published work's cover.
func (s *Service) StableCoverURL(workID string) string {
	return "/api/v1/media/works/" + workID + "/cover"
}

// SignedCoverURL is a backend-signed URL valid for SignedURLTTL. Only hand it to
// the owner or an Admin, and never store it in a cache.
func (s *Service) SignedCoverURL(workID, coverKey string) string {
	exp := s.d.Now().Add(SignedURLTTL).Unix()
	return fmt.Sprintf("%s?exp=%d&sig=%s", s.StableCoverURL(workID), exp, s.sign(workID, coverKey, exp))
}

// CoverURL picks the stable URL for a Published work and a signed one otherwise.
func (s *Service) CoverURL(workID, coverKey string, published bool) string {
	if published {
		return s.StableCoverURL(workID)
	}
	return s.SignedCoverURL(workID, coverKey)
}

func (s *Service) sign(workID, coverKey string, exp int64) string {
	m := hmac.New(sha256.New, s.d.SignSecret)
	m.Write([]byte("media-cover"))
	m.Write([]byte{0})
	m.Write([]byte(workID))
	m.Write([]byte{0})
	m.Write([]byte(coverKey))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Service) verify(workID, coverKey, expStr, sigStr string) bool {
	if expStr == "" || sigStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || s.d.Now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(sigStr)
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(s.sign(workID, coverKey, exp))
	return hmac.Equal(got, want)
}

// ---------------------------------------------------------------------------
// Streaming

// StreamCover decides whether the caller may see the cover and opens it.
// A Published work needs nothing. Any other status needs a valid, unexpired
// signature issued for the current cover key; otherwise the answer is 404 so the
// existence of the work is not revealed. Status is checked on every request, so
// a hidden or deleted work loses its image at once.
func (s *Service) StreamCover(ctx context.Context, workID, expStr, sigStr string) (*Stream, error) {
	workID = strings.TrimSpace(workID)
	if s.d.Works == nil {
		return nil, apperr.Internal(errors.New("media: work lookup not bound"))
	}
	if !isUUID(workID) {
		return nil, errMediaNotFound
	}

	info, err := s.d.Works.LookupCover(ctx, workID)
	if errors.Is(err, ErrWorkNotFound) {
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if info.CoverKey == "" {
		return nil, errMediaNotFound
	}

	if !info.Published && !s.verify(workID, info.CoverKey, expStr, sigStr) {
		metrics.IncMediaRequest("refused")
		s.d.Logger.Warn("media refused", "work_id", workID, "reason", "bad_or_missing_signature")
		return nil, errMediaNotFound
	}

	body, storedType, size, err := s.d.Storage.Get(ctx, info.CoverKey)
	if errors.Is(err, ErrObjectNotFound) {
		s.d.Logger.Error("cover object missing", "work_id", workID)
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("read cover object: %w", err))
	}

	contentType := info.ContentType
	if contentType == "" {
		contentType = storedType
	}
	if info.SizeBytes > 0 {
		size = info.SizeBytes
	}
	sum := sha256.Sum256([]byte(info.CoverKey))
	metrics.IncMediaRequest("served")
	return &Stream{
		Body:        body,
		ContentType: contentType,
		Size:        size,
		ETag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
		Public:      info.Published,
	}, nil
}

// ---------------------------------------------------------------------------
// Screenshots

// StableScreenshotURL is the never-expiring URL of a screenshot of a Published work.
func (s *Service) StableScreenshotURL(workID, imageID string) string {
	return "/api/v1/media/works/" + workID + "/screenshots/" + imageID
}

// ScreenshotURL picks the stable URL for a Published work and a signed one otherwise.
func (s *Service) ScreenshotURL(workID, imageID, objectKey string, published bool) string {
	if published {
		return s.StableScreenshotURL(workID, imageID)
	}
	exp := s.d.Now().Add(SignedURLTTL).Unix()
	return fmt.Sprintf("%s?exp=%d&sig=%s", s.StableScreenshotURL(workID, imageID), exp, s.signScreenshot(workID, imageID, objectKey, exp))
}

func (s *Service) signScreenshot(workID, imageID, objectKey string, exp int64) string {
	m := hmac.New(sha256.New, s.d.SignSecret)
	m.Write([]byte("media-screenshot"))
	m.Write([]byte{0})
	m.Write([]byte(workID))
	m.Write([]byte{0})
	m.Write([]byte(imageID))
	m.Write([]byte{0})
	m.Write([]byte(objectKey))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Service) verifyScreenshot(workID, imageID, objectKey, expStr, sigStr string) bool {
	if expStr == "" || sigStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || s.d.Now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(sigStr)
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(s.signScreenshot(workID, imageID, objectKey, exp))
	return hmac.Equal(got, want)
}

// StreamScreenshot follows the same rule as StreamCover: a screenshot of a Published
// work is public, any other needs a valid unexpired signature, and everything else is 404.
func (s *Service) StreamScreenshot(ctx context.Context, workID, imageID, expStr, sigStr string) (*Stream, error) {
	workID = strings.TrimSpace(workID)
	imageID = strings.TrimSpace(imageID)
	if s.d.Shots == nil {
		return nil, apperr.Internal(errors.New("media: screenshot lookup not bound"))
	}
	if !isUUID(workID) || !isUUID(imageID) {
		return nil, errMediaNotFound
	}

	info, err := s.d.Shots.LookupScreenshot(ctx, workID, imageID)
	if errors.Is(err, ErrWorkNotFound) {
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if info.CoverKey == "" {
		return nil, errMediaNotFound
	}

	if !info.Published && !s.verifyScreenshot(workID, imageID, info.CoverKey, expStr, sigStr) {
		metrics.IncMediaRequest("refused")
		s.d.Logger.Warn("media refused", "work_id", workID, "image_id", imageID, "reason", "bad_or_missing_signature")
		return nil, errMediaNotFound
	}

	body, storedType, size, err := s.d.Storage.Get(ctx, info.CoverKey)
	if errors.Is(err, ErrObjectNotFound) {
		s.d.Logger.Error("screenshot object missing", "work_id", workID, "image_id", imageID)
		return nil, errMediaNotFound
	}
	if err != nil {
		return nil, apperr.Internal(fmt.Errorf("read screenshot object: %w", err))
	}

	contentType := info.ContentType
	if contentType == "" {
		contentType = storedType
	}
	if info.SizeBytes > 0 {
		size = info.SizeBytes
	}
	sum := sha256.Sum256([]byte(info.CoverKey))
	metrics.IncMediaRequest("served")
	return &Stream{
		Body:        body,
		ContentType: contentType,
		Size:        size,
		ETag:        `"` + hex.EncodeToString(sum[:8]) + `"`,
		Public:      info.Published,
	}, nil
}

// ---------------------------------------------------------------------------
// helpers

// sniff detects the content type from the bytes. net/http recognises JPEG, PNG
// and WebP; anything else is rejected by the caller.
func sniff(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "image/jpeg"
	case len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	}
	return ""
}

// validWebP checks the RIFF container: declared size fits the data and the first
// chunk is one of the three WebP chunk types.
func validWebP(data []byte) bool {
	if len(data) < 20 {
		return false
	}
	riffSize := int64(data[4]) | int64(data[5])<<8 | int64(data[6])<<16 | int64(data[7])<<24
	if riffSize+8 > int64(len(data)) {
		return false
	}
	switch string(data[12:16]) {
	case "VP8 ", "VP8L", "VP8X":
		return true
	}
	return false
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// isUUID reports whether s looks like a canonical UUID. Checking first keeps a
// malformed id from reaching PostgreSQL's uuid cast, which would be a 500.
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
