package agent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeJoinRejectsEscapes(t *testing.T) {
	dest := filepath.Clean("/tmp/dest")
	escapes := []string{
		"../evil",
		"../../etc/passwd",
		"/etc/passwd",
		"sub/../../evil",
	}
	for _, name := range escapes {
		if _, err := safeJoin(dest, name); err == nil {
			t.Errorf("safeJoin(%q) allowed an escape", name)
		}
	}
	ok := []string{"file.txt", "sub/file.txt", "./sub/./file.txt"}
	for _, name := range ok {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Errorf("safeJoin(%q) rejected a legitimate entry: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got, dest) {
			t.Errorf("safeJoin(%q) = %q, outside dest", name, got)
		}
	}
}

// TestExtractTarRejectsTraversal builds a malicious tar.gz whose entry
// escapes the destination and asserts extraction fails without writing it.
func TestExtractTarRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "evil.tar.gz")
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	payload := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escaped.txt", Mode: 0o644, Size: int64(len(payload)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	if err := extractArchive(archivePath, dest); err == nil {
		t.Fatal("traversal entry was extracted without error")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Fatal("traversal entry escaped the destination directory")
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "evil.zip")
	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()

	if err := extractArchive(archivePath, dest); err == nil {
		t.Fatal("traversal entry was extracted without error")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Fatal("traversal entry escaped the destination directory")
	}
}

// TestArchiveRoundTrip covers both formats: create from a tree, extract into
// a fresh directory, and compare contents.
func TestArchiveRoundTrip(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "tree")
			if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "nested", "b.txt"), []byte("beta"), 0o600); err != nil {
				t.Fatal(err)
			}

			ext := ".tar.gz"
			if format == "zip" {
				ext = ".zip"
			}
			archivePath := filepath.Join(dir, "out"+ext)
			if err := createArchive(archivePath, []string{src}, format); err != nil {
				t.Fatalf("createArchive: %v", err)
			}

			dest := filepath.Join(dir, "restored")
			if err := extractArchive(archivePath, dest); err != nil {
				t.Fatalf("extractArchive: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(dest, "tree", "a.txt"))
			if err != nil || string(got) != "alpha" {
				t.Errorf("a.txt = %q, %v", got, err)
			}
			got, err = os.ReadFile(filepath.Join(dest, "tree", "nested", "b.txt"))
			if err != nil || string(got) != "beta" {
				t.Errorf("nested/b.txt = %q, %v", got, err)
			}
		})
	}
}

func TestCopyPathTree(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "f.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := copyPath(src, dst); err != nil {
		t.Fatalf("copyPath: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "sub", "f.txt"))
	if err != nil || string(got) != "data" {
		t.Errorf("copied file = %q, %v", got, err)
	}
}
