// Package service holds the media rules: upload checks, object keys, stable and
// signed image URLs, and the access decision for streaming an image.
package service

import (
	"context"
	"errors"
	"io"
	"time"
)

const (
	// MaxImageBytes is the largest accepted cover or screenshot (5 MB).
	MaxImageBytes = 5 << 20
	// MaxImagePixels guards against decompression bombs (width x height).
	MaxImagePixels = 40_000_000
	// SignedURLTTL is how long a signed URL for a non-public image stays valid.
	SignedURLTTL = 10 * time.Minute
)

var (
	// ErrObjectNotFound is returned by ObjectStorage when the key does not exist.
	ErrObjectNotFound = errors.New("object not found")
	// ErrWorkNotFound is returned by WorkLookup when the work does not exist or was deleted.
	ErrWorkNotFound = errors.New("work not found")
)

// ObjectStorage is the private object store (local disk). Implementations live in the
// repository layer and are the only code that talks to the store.
type ObjectStorage interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get must return ErrObjectNotFound when the key does not exist.
	Get(ctx context.Context, key string) (body io.ReadCloser, contentType string, size int64, err error)
	Delete(ctx context.Context, key string) error
}

// WorkCover is what media needs to know about a work to decide who may see its cover.
type WorkCover struct {
	CoverKey    string
	ContentType string
	SizeBytes   int64
	Published   bool
}

// WorkLookup is implemented by the works module's service. Media never reads the
// works table itself.
type WorkLookup interface {
	// LookupCover must return ErrWorkNotFound for a missing or deleted work.
	LookupCover(ctx context.Context, workID string) (*WorkCover, error)
}

// ScreenshotLookup is implemented by the works module's service. It is bound
// separately from WorkLookup so the cover rules stay untouched.
type ScreenshotLookup interface {
	// LookupScreenshot must return ErrWorkNotFound for a missing or deleted work or
	// screenshot. The returned WorkCover holds the screenshot's key and type.
	LookupScreenshot(ctx context.Context, workID, imageID string) (*WorkCover, error)
}

// Stream is an image ready to be written to the client.
type Stream struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	ETag        string
	// Public is true when the image belongs to a Published work.
	Public bool
}
