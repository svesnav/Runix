package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/runix/runix/internal/protocol"
)

// transferChunk is the payload size of one stream data frame. Frames are
// JSON-enveloped with base64 data, so this stays well under the 32 MiB
// frame limit while keeping per-frame overhead low.
const transferChunk = 256 << 10

func registerTransferHandlers(reg *rpcRegistry) {
	// fs.download streams a file (or an on-the-fly tar.gz of several paths)
	// from the host to the control plane.
	reg.stream(protocol.MethodFSDownload, func(ctx context.Context, raw json.RawMessage, st *AgentStream) *protocol.Error {
		params, e := decodeParams[protocol.FSDownloadParams](raw)
		if e != nil {
			return e
		}
		paths := params.Paths
		if len(paths) == 0 && params.Path != "" {
			paths = []string{params.Path}
		}
		if len(paths) == 0 {
			return &protocol.Error{Code: protocol.CodeInvalid, Message: "no path given"}
		}
		clean := make([]string, 0, len(paths))
		for _, p := range paths {
			c, e := cleanAbs(p)
			if e != nil {
				return e
			}
			clean = append(clean, c)
		}

		single := len(clean) == 1
		info, err := os.Stat(clean[0])
		if err != nil {
			return fsErr(err)
		}
		asArchive := params.Archive || !single || info.IsDir()

		if !asArchive {
			if err := st.SendCtrlJSON(protocol.FSDownloadMeta{
				Name: filepath.Base(clean[0]), Size: info.Size(),
			}); err != nil {
				return nil
			}
			f, err := os.Open(clean[0]) // #nosec G304
			if err != nil {
				return fsErr(err)
			}
			defer f.Close()
			return sendReader(ctx, st, f)
		}

		name := filepath.Base(clean[0]) + ".tar.gz"
		if !single {
			name = "download.tar.gz"
		}
		// Size is unknown up front for a streamed archive.
		if err := st.SendCtrlJSON(protocol.FSDownloadMeta{Name: name, Size: -1}); err != nil {
			return nil
		}
		pr, pw := io.Pipe()
		go func() {
			gz := gzip.NewWriter(pw)
			tw := tar.NewWriter(gz)
			var err error
			for _, src := range clean {
				if err = addToTar(tw, src); err != nil {
					break
				}
			}
			if err == nil {
				err = tw.Close()
				if err == nil {
					err = gz.Close()
				}
			}
			_ = pw.CloseWithError(err)
		}()
		defer pr.Close()
		return sendReader(ctx, st, pr)
	})

	// fs.upload receives a file from the control plane. The agent writes to
	// a sibling temp file and only publishes it on an explicit EOF control
	// frame, so an interrupted upload never replaces a good file.
	reg.stream(protocol.MethodFSUpload, func(ctx context.Context, raw json.RawMessage, st *AgentStream) *protocol.Error {
		params, e := decodeParams[protocol.FSUploadParams](raw)
		if e != nil {
			return e
		}
		path, e := cleanAbs(params.Path)
		if e != nil {
			return e
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fsErr(err)
		}
		perm := fs.FileMode(params.Mode)
		if perm == 0 {
			perm = 0o644
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), ".runix-upload-*")
		if err != nil {
			return fsErr(err)
		}
		tmpName := tmp.Name()
		committed := false
		defer func() {
			tmp.Close()
			if !committed {
				_ = os.Remove(tmpName)
			}
		}()

		for {
			select {
			case env, ok := <-st.In:
				if !ok {
					return &protocol.Error{Code: protocol.CodeInvalid, Message: "upload stream closed before EOF"}
				}
				switch env.Op {
				case protocol.StreamData:
					if _, err := tmp.Write(env.Data); err != nil {
						return fsErr(err)
					}
				case protocol.StreamCtrl:
					ctrl, err := protocol.Decode[protocol.FSUploadCtrl](env.Payload)
					if err != nil || !ctrl.EOF {
						continue
					}
					if err := tmp.Close(); err != nil {
						return fsErr(err)
					}
					if err := os.Chmod(tmpName, perm); err != nil {
						return fsErr(err)
					}
					if err := os.Rename(tmpName, path); err != nil {
						return fsErr(err)
					}
					committed = true
					return nil
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

func sendReader(ctx context.Context, st *AgentStream, r io.Reader) *protocol.Error {
	buf := make([]byte, transferChunk)
	for {
		if ctx.Err() != nil {
			return nil
		}
		n, err := r.Read(buf)
		if n > 0 {
			if sendErr := st.Send(buf[:n]); sendErr != nil {
				return nil // control plane hung up
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fsErr(err)
		}
	}
}
