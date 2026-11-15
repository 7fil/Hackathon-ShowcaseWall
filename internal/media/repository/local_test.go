package repository

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/7fil/Hackathon-ShowcaseWall.git/internal/media/service"
)

func TestLocalStorage(t *testing.T) {
	dir := t.TempDir()
	st, err := NewLocalDiskStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Put(ctx, "works/a/cover.png", []byte("data"), "image/png"); err != nil {
		t.Fatal(err)
	}
	body, _, size, err := st.Get(ctx, "works/a/cover.png")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "data" || size != 4 {
		t.Errorf("got %q size %d", b, size)
	}
	if _, _, _, err := st.Get(ctx, "works/missing.png"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("missing object must be ErrObjectNotFound, got %v", err)
	}
	if err := st.Delete(ctx, "works/a/cover.png"); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, "works/a/cover.png"); err != nil {
		t.Errorf("deleting twice must succeed: %v", err)
	}
}

func TestLocalStorageCannotEscapeBaseDir(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "media")
	st, err := NewLocalDiskStorage(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(context.Background(), "../../escape.txt", []byte("x"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); err == nil {
		t.Error("a key with .. must not write outside the base directory")
	}
	if _, err := os.Stat(filepath.Join(base, "escape.txt")); err != nil {
		t.Errorf("the key should be confined inside the base dir: %v", err)
	}
	if err := st.Put(context.Background(), "", []byte("x"), ""); err == nil {
		t.Error("an empty key must be refused")
	}
}

func TestLocalStorageOverwriteIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	st, err := NewLocalDiskStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := "works/a/cover.png"
	if err := st.Put(ctx, key, []byte("old"), "image/png"); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(ctx, key, []byte("newer"), "image/png"); err != nil {
		t.Fatal(err)
	}
	body, _, size, err := st.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(body)
	body.Close()
	if string(b) != "newer" || size != 5 {
		t.Errorf("got %q size %d", b, size)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "works", "a"))
	if len(entries) != 1 {
		t.Errorf("expected only the object in its folder, got %d entries", len(entries))
	}
}

func TestLocalStorageContentTypeFromExtension(t *testing.T) {
	st, _ := NewLocalDiskStorage(t.TempDir())
	ctx := context.Background()
	for key, want := range map[string]string{
		"w/a.jpg": "image/jpeg", "w/a.png": "image/png", "w/a.webp": "image/webp", "w/a.bin": "application/octet-stream",
	} {
		if err := st.Put(ctx, key, []byte("x"), ""); err != nil {
			t.Fatal(err)
		}
		body, ct, _, err := st.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		body.Close()
		if ct != want {
			t.Errorf("%s: got %q want %q", key, ct, want)
		}
	}
}

func TestLocalStorageDirectoryKeyIsNotAnObject(t *testing.T) {
	st, _ := NewLocalDiskStorage(t.TempDir())
	ctx := context.Background()
	_ = st.Put(ctx, "works/a/cover.png", []byte("x"), "")
	if _, _, _, err := st.Get(ctx, "works/a"); !errors.Is(err, service.ErrObjectNotFound) {
		t.Errorf("a folder must read as not found, got %v", err)
	}
}

func TestLocalStorageDeleteRemovesEmptyFoldersButKeepsBase(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewLocalDiskStorage(dir)
	ctx := context.Background()
	_ = st.Put(ctx, "works/a/cover-1.png", []byte("x"), "")
	_ = st.Put(ctx, "works/a/cover-2.png", []byte("y"), "")
	_ = st.Delete(ctx, "works/a/cover-1.png")
	if _, err := os.Stat(filepath.Join(dir, "works", "a")); err != nil {
		t.Errorf("folder with a remaining object must stay: %v", err)
	}
	_ = st.Delete(ctx, "works/a/cover-2.png")
	if _, err := os.Stat(filepath.Join(dir, "works", "a")); err == nil {
		t.Error("empty work folder should be removed")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("base dir must never be removed: %v", err)
	}
}

func TestNewLocalDiskStorageFailsEarlyOnBadDir(t *testing.T) {
	if _, err := NewLocalDiskStorage(""); err == nil {
		t.Error("empty dir must be refused")
	}
	file := filepath.Join(t.TempDir(), "afile")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if _, err := NewLocalDiskStorage(filepath.Join(file, "sub")); err == nil {
		t.Error("a dir under a regular file must fail at startup")
	}
}
