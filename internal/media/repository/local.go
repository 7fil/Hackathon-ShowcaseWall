package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

// LocalDiskStorage keeps objects under a directory. It is for local development
// when MINIO_ENDPOINT is not set; production uses MinIO.
type LocalDiskStorage struct {
	baseDir string
}

var _ service.ObjectStorage = (*LocalDiskStorage)(nil)

// NewLocalDiskStorage creates the base directory if needed.
func NewLocalDiskStorage(baseDir string) (*LocalDiskStorage, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create local storage dir: %w", err)
	}
	return &LocalDiskStorage{baseDir: baseDir}, nil
}

// resolve maps a key to a path that cannot escape baseDir.
func (l *LocalDiskStorage) resolve(key string) (string, error) {
	clean := path.Clean("/" + key) // rooted clean removes any ".." that would climb out
	if clean == "/" {
		return "", errors.New("invalid storage key")
	}
	return filepath.Join(l.baseDir, filepath.FromSlash(clean)), nil
}

// Put writes the object to disk.
func (l *LocalDiskStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// Get opens the object. The content type is unknown on disk, so callers use the
// type stored with the work.
func (l *LocalDiskStorage) Get(_ context.Context, key string) (io.ReadCloser, string, int64, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, "", 0, err
	}
	f, err := os.Open(full)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", 0, service.ErrObjectNotFound
	}
	if err != nil {
		return nil, "", 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", 0, err
	}
	return f, "application/octet-stream", info.Size(), nil
}

// Delete removes the object; a missing file counts as success.
func (l *LocalDiskStorage) Delete(_ context.Context, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
