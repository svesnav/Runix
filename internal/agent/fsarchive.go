package agent

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/runix/runix/internal/protocol"
)

// Extraction guards: a malicious archive must not be able to write outside
// the destination directory (zip-slip) or exhaust the disk.
const (
	maxExtractBytes   = 8 << 30
	maxExtractEntries = 200_000
)

func registerArchiveHandlers(reg *rpcRegistry) {
	reg.call(protocol.MethodFSArchive, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSArchiveParams](raw)
		if e != nil {
			return nil, e
		}
		if len(params.Paths) == 0 {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "no source paths"}
		}
		target, e := cleanAbs(params.Target)
		if e != nil {
			return nil, e
		}
		sources := make([]string, 0, len(params.Paths))
		for _, p := range params.Paths {
			clean, e := cleanAbs(p)
			if e != nil {
				return nil, e
			}
			sources = append(sources, clean)
		}
		format := params.Format
		if format == "" {
			format = formatFromName(target)
		}
		if err := createArchive(target, sources, format); err != nil {
			_ = os.Remove(target) // don't leave a half-written archive behind
			return nil, fsErr(err)
		}
		return nil, nil
	})

	reg.call(protocol.MethodFSExtract, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSExtractParams](raw)
		if e != nil {
			return nil, e
		}
		src, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		dest := params.Dest
		if dest == "" {
			dest = filepath.Dir(src)
		}
		destClean, e := cleanAbs(dest)
		if e != nil {
			return nil, e
		}
		if err := os.MkdirAll(destClean, 0o755); err != nil {
			return nil, fsErr(err)
		}
		return nil, fsErr(extractArchive(src, destClean))
	})
}

func formatFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return protocol.ArchiveZip
	default:
		return protocol.ArchiveTarGz
	}
}

func createArchive(target string, sources []string, format string) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) // #nosec G304
	if err != nil {
		return err
	}
	defer out.Close()

	if format == protocol.ArchiveZip {
		zw := zip.NewWriter(out)
		for _, src := range sources {
			if err := addToZip(zw, src); err != nil {
				_ = zw.Close()
				return err
			}
		}
		return zw.Close()
	}

	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, src := range sources {
		if err := addToTar(tw, src); err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// walkSource visits src and yields each entry with the archive-relative name
// it should be stored under (rooted at the source's base name).
func walkSource(src string, visit func(p string, rel string, info fs.FileInfo) error) error {
	base := filepath.Base(src)
	root, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !root.IsDir() {
		return visit(src, base, root)
	}
	return filepath.Walk(src, func(p string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		name := base
		if rel != "." {
			name = path.Join(base, filepath.ToSlash(rel))
		}
		return visit(p, name, info)
	})
}

func addToTar(tw *tar.Writer, src string) error {
	return walkSource(src, func(p, rel string, info fs.FileInfo) error {
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			link = target
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = rel
		if info.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p) // #nosec G304
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
}

func addToZip(zw *zip.Writer, src string) error {
	return walkSource(src, func(p, rel string, info fs.FileInfo) error {
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil // zip has no portable symlink representation
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = rel
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(p) // #nosec G304
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
}

func extractArchive(src, dest string) error {
	lower := strings.ToLower(src)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(src, dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar"):
		return extractTar(src, dest)
	default:
		return fmt.Errorf("unsupported archive format: %s", filepath.Base(src))
	}
}

// safeJoin resolves an archive entry name inside dest, rejecting absolute
// paths and ".." escapes (zip-slip).
func safeJoin(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive entry has an empty name")
	}
	escape := fmt.Errorf("archive entry %q escapes the destination", name)
	// Archive names are always slash-separated, so a rooted entry must be
	// rejected by inspecting the raw name: host path semantics differ
	// (a leading "/" is not "absolute" on Windows).
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) ||
		filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", escape
	}
	cleaned := filepath.Clean(filepath.FromSlash(name))
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", escape
	}
	full := filepath.Join(dest, cleaned)
	if full != dest && !strings.HasPrefix(full, dest+string(filepath.Separator)) {
		return "", escape
	}
	return full, nil
}

func extractTar(src, dest string) error {
	f, err := os.Open(src) // #nosec G304
	if err != nil {
		return err
	}
	defer f.Close()

	var reader io.Reader = f
	if !strings.HasSuffix(strings.ToLower(src), ".tar") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		reader = gz
	}

	tr := tar.NewReader(reader)
	var written int64
	for entries := 0; ; entries++ {
		if entries > maxExtractEntries {
			return fmt.Errorf("archive has too many entries")
		}
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if _, err := safeJoin(dest, path.Join(path.Dir(header.Name), header.Linkname)); err != nil {
				return fmt.Errorf("symlink %q escapes the destination", header.Name)
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			n, err := writeLimited(target, tr, os.FileMode(header.Mode).Perm(), maxExtractBytes-written)
			if err != nil {
				return err
			}
			written += n
		}
	}
}

func extractZip(src, dest string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	if len(zr.File) > maxExtractEntries {
		return fmt.Errorf("archive has too many entries")
	}

	var written int64
	for _, entry := range zr.File {
		target, err := safeJoin(dest, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, entry.Mode().Perm()); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		n, err := writeLimited(target, rc, entry.Mode().Perm(), maxExtractBytes-written)
		rc.Close()
		if err != nil {
			return err
		}
		written += n
	}
	return nil
}

// writeLimited copies at most limit bytes into path, failing if the source
// is larger (decompression-bomb guard).
func writeLimited(path string, r io.Reader, perm fs.FileMode, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("archive exceeds the extraction size limit")
	}
	if perm == 0 {
		perm = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm) // #nosec G304
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, io.LimitReader(r, limit))
	if err != nil {
		return n, err
	}
	if n == limit {
		return n, fmt.Errorf("archive exceeds the extraction size limit")
	}
	return n, f.Close()
}
