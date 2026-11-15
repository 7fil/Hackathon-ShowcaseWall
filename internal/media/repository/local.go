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
	"strings"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

// LocalDiskStorage keeps objects as plain files under a directory on the VM disk.
// It is the object store (MEDIA_LOCAL_DIR). Files are only ever
// served through the media service, which checks the work's status first.
type LocalDiskStorage struct {
	baseDir string
}

var _ service.ObjectStorage = (*LocalDiskStorage)(nil)

// NewLocalDiskStorage creates the base directory if needed and checks that it is
// writable, so a bad mount or permission problem fails at startup and not on the
// first upload.
func NewLocalDiskStorage(baseDir string) (*LocalDiskStorage, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("local storage dir is empty")
	}
	abs, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve local storage dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("create local storage dir %q: %w", abs, err)
	}
	probe, err := os.CreateTemp(abs, ".probe-*")
	if err != nil {
		return nil, fmt.Errorf("local storage dir %q is not writable: %w", abs, err)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return &LocalDiskStorage{baseDir: abs}, nil
}

// resolve maps a key to a path that cannot escape baseDir.
func (l *LocalDiskStorage) resolve(key string) (string, error) {
	if strings.ContainsRune(key, 0) {
		return "", errors.New("invalid storage key")
	}
	clean := path.Clean("/" + strings.ReplaceAll(key, `\`, "/")) // rooted clean removes any ".." that would climb out
	if clean == "/" {
		return "", errors.New("invalid storage key")
	}
	return filepath.Join(l.baseDir, filepath.FromSlash(clean)), nil
}

// Put writes the object atomically: the bytes go to a temp file in the same
// directory and are renamed into place, so a concurrent Get never sees a
// half-written image and a crash never leaves a truncated one under the real key.
func (l *LocalDiskStorage) Put(_ context.Context, key string, data []byte, _ string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		cleanup()
		return err
	}
	return nil
}

// Get opens the object. The content type is derived from the file extension; the
// media service prefers the type stored with the work anyway.
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
	if info.IsDir() { // a key that names a folder is not an object
		f.Close()
		return nil, "", 0, service.ErrObjectNotFound
	}
	return f, contentTypeForExt(full), info.Size(), nil
}

// Delete removes the object; a missing file counts as success. Parent folders
// that become empty (works/{id}/) are removed too, up to the base directory.
func (l *LocalDiskStorage) Delete(_ context.Context, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for dir := filepath.Dir(full); dir != l.baseDir && strings.HasPrefix(dir, l.baseDir+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil { // fails while the folder still has files, which is what we want
			break
		}
	}
	return nil
}

func contentTypeForExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}
