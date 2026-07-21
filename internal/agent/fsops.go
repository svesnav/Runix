package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/runix/runix/internal/protocol"
)

const (
	fsDefaultReadLimit = 10 << 20
	fsMaxReadLimit     = 24 << 20
	fsMaxWriteBytes    = 24 << 20
)

// cleanAbs validates and normalizes a client-supplied path. The file
// manager operates over the whole host by design (the agent runs with the
// privileges the operator gave it), but paths must be absolute and clean so
// no relative-traversal tricks reach the OS layer.
func cleanAbs(p string) (string, *protocol.Error) {
	if p == "" || !filepath.IsAbs(p) {
		return "", &protocol.Error{Code: protocol.CodeInvalid, Message: "path must be absolute"}
	}
	return filepath.Clean(p), nil
}

// fsErr converts a filesystem error into a protocol error. A nil error maps
// to nil so handlers can return fsErr(op()) directly.
func fsErr(err error) *protocol.Error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrExist):
		return &protocol.Error{Code: protocol.CodeInvalid, Message: err.Error()}
	case errors.Is(err, fs.ErrNotExist):
		return &protocol.Error{Code: protocol.CodeNotFound, Message: err.Error()}
	case errors.Is(err, fs.ErrPermission):
		return &protocol.Error{Code: protocol.CodeInvalid, Message: err.Error()}
	default:
		return &protocol.Error{Code: protocol.CodeInternal, Message: err.Error()}
	}
}

// copyPath copies a file or directory tree, preserving permission bits.
func copyPath(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case info.IsDir():
		if err := os.MkdirAll(to, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(from)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyPath(filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(from)
		if err != nil {
			return err
		}
		_ = os.Remove(to)
		return os.Symlink(target, to)
	default:
		return copyFile(from, to, info.Mode().Perm())
	}
}

func copyFile(from, to string, perm fs.FileMode) error {
	src, err := os.Open(from) // #nosec G304
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm) // #nosec G304
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return dst.Close()
}

func entryFromInfo(dir string, info fs.FileInfo) protocol.FSEntry {
	return protocol.FSEntry{
		Name:      info.Name(),
		Path:      filepath.Join(dir, info.Name()),
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   info.ModTime(),
		IsDir:     info.IsDir(),
		IsSymlink: info.Mode()&fs.ModeSymlink != 0,
	}
}

func registerFSHandlers(reg *rpcRegistry) {
	reg.call(protocol.MethodFSList, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSListParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, fsErr(err)
		}
		result := protocol.FSListResult{Path: path, Entries: []protocol.FSEntry{}}
		for _, entry := range entries {
			if !params.ShowHidden && len(entry.Name()) > 0 && entry.Name()[0] == '.' {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			result.Entries = append(result.Entries, entryFromInfo(path, info))
		}
		return result, nil
	})

	reg.call(protocol.MethodFSStat, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSStatParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fsErr(err)
		}
		return entryFromInfo(filepath.Dir(path), info), nil
	})

	reg.call(protocol.MethodFSRead, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSReadParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		limit := params.MaxBytes
		if limit <= 0 || limit > fsMaxReadLimit {
			limit = fsDefaultReadLimit
		}
		f, err := os.Open(path) // #nosec G304 -- serving arbitrary host paths is this feature
		if err != nil {
			return nil, fsErr(err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, fsErr(err)
		}
		if info.IsDir() {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "path is a directory"}
		}
		bufSize := info.Size()
		if bufSize > limit {
			bufSize = limit
		}
		buf := make([]byte, bufSize)
		n, err := f.Read(buf)
		if err != nil && !errors.Is(err, fs.ErrClosed) && n == 0 && info.Size() > 0 {
			return nil, fsErr(err)
		}
		return protocol.FSReadResult{
			Content:   buf[:n],
			Size:      info.Size(),
			Truncated: info.Size() > int64(n),
		}, nil
	})

	reg.call(protocol.MethodFSWrite, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSWriteParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		if len(params.Content) > fsMaxWriteBytes {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "content too large"}
		}
		mode := os.FileMode(params.Mode)
		if mode == 0 {
			mode = 0o644
		}
		flags := os.O_CREATE | os.O_WRONLY
		if params.Append {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		f, err := os.OpenFile(path, flags, mode) // #nosec G304
		if err != nil {
			return nil, fsErr(err)
		}
		defer f.Close()
		if _, err := f.Write(params.Content); err != nil {
			return nil, fsErr(err)
		}
		return nil, nil
	})

	reg.call(protocol.MethodFSMkdir, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSMkdirParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return nil, fsErr(err)
		}
		return nil, nil
	})

	reg.call(protocol.MethodFSRename, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSRenameParams](raw)
		if e != nil {
			return nil, e
		}
		from, e := cleanAbs(params.From)
		if e != nil {
			return nil, e
		}
		to, e := cleanAbs(params.To)
		if e != nil {
			return nil, e
		}
		if err := os.Rename(from, to); err != nil {
			return nil, fsErr(err)
		}
		return nil, nil
	})

	reg.call(protocol.MethodFSCreate, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSCreateParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		// O_EXCL so creating over an existing file is an error, not a
		// silent truncation.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) // #nosec G304
		if err != nil {
			return nil, fsErr(err)
		}
		return nil, fsErr(f.Close())
	})

	reg.call(protocol.MethodFSCopy, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSCopyParams](raw)
		if e != nil {
			return nil, e
		}
		from, e := cleanAbs(params.From)
		if e != nil {
			return nil, e
		}
		to, e := cleanAbs(params.To)
		if e != nil {
			return nil, e
		}
		if from == to {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "source and destination are identical"}
		}
		// Copying a directory into itself would recurse forever.
		if strings.HasPrefix(to+string(filepath.Separator), from+string(filepath.Separator)) {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "cannot copy a directory into itself"}
		}
		return nil, fsErr(copyPath(from, to))
	})

	reg.call(protocol.MethodFSChmod, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSChmodParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		mode, err := strconv.ParseUint(strings.TrimPrefix(params.Mode, "0o"), 8, 32)
		if err != nil || mode > 0o7777 {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "mode must be octal, e.g. 644 or 0755"}
		}
		perm := os.FileMode(mode)
		if !params.Recursive {
			return nil, fsErr(os.Chmod(path, perm))
		}
		err = filepath.WalkDir(path, func(p string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Chmod(p, perm)
		})
		return nil, fsErr(err)
	})

	reg.call(protocol.MethodFSDelete, func(_ context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.FSDeleteParams](raw)
		if e != nil {
			return nil, e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return nil, e
		}
		if path == filepath.Dir(path) {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "refusing to delete filesystem root"}
		}
		var err error
		if params.Recursive {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			return nil, fsErr(err)
		}
		return nil, nil
	})
}
