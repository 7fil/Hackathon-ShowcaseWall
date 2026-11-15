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
