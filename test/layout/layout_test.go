// Package layout_test enforces the project rule that every automated test lives under test/.
package layout_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot walks up from the working directory to the folder that holds go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

func TestNoTestFilesOutsideTestDir(t *testing.T) {
	root := moduleRoot(t)
	var stray []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch {
			case rel == "test", d.Name() == ".git", d.Name() == "node_modules", d.Name() == "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") {
			stray = append(stray, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range stray {
		t.Errorf("%s: test files belong under test/, not next to the code (see test/README.md)", f)
	}
}
