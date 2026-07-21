package files

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/runix/runix/internal/modules/audit"
	"github.com/runix/runix/internal/modules/runtimes"
	"github.com/runix/runix/internal/platform/httpx"
	"github.com/runix/runix/internal/protocol"
)

// Transfers can run long; they are bounded by inactivity on the stream
// rather than a single deadline for the whole file.
const (
	transferIdleTimeout = 2 * time.Minute
	uploadChunk         = 256 << 10
	maxUploadFiles      = 64
)

// Download streams one or more paths from the agent straight to the HTTP
// response. Nothing is buffered in the control plane, so file size is
// bounded only by the client's patience.
func (h *Handler) Download(c *gin.Context) {
	if !h.authorize(c, "server.files.read") {
		return
	}
	paths := c.QueryArray("path")
	if len(paths) == 0 {
		httpx.BadRequest(c, "path is required")
		return
	}
	params := protocol.FSDownloadParams{
		Paths:   paths,
		Archive: c.Query("archive") == "true",
	}
	if len(paths) == 1 {
		params.Path = paths[0]
	}

	ctx := c.Request.Context()
	stream, err := h.hub.OpenStream(ctx, c.Param("id"), protocol.MethodFSDownload, params)
	if err != nil {
		runtimes.ProxyError(c, err)
		return
	}
	defer stream.Close()

	h.auditor.Write(ctx, audit.Record{
		Action: "files.download", TargetType: "file",
		TargetID: c.Param("id") + ":" + paths[0], New: gin.H{"paths": paths},
	})

	headerSent := false
	for {
		frameCtx, cancel := context.WithTimeout(ctx, transferIdleTimeout)
		frame, err := stream.Recv(frameCtx)
		cancel()
		if err != nil {
			if !headerSent {
				runtimes.ProxyError(c, err)
			}
			return
		}

		switch frame.Op {
		case protocol.StreamCtrl:
			meta, err := protocol.Decode[protocol.FSDownloadMeta](frame.Payload)
			if err != nil {
				continue
			}
			name := path.Base(meta.Name)
			c.Header("Content-Disposition",
				mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			c.Header("Content-Type", "application/octet-stream")
			if meta.Size >= 0 {
				c.Header("Content-Length", strconv.FormatInt(meta.Size, 10))
			}
			c.Status(http.StatusOK)
			headerSent = true
		case protocol.StreamData:
			if !headerSent {
				c.Header("Content-Type", "application/octet-stream")
				c.Status(http.StatusOK)
				headerSent = true
			}
			if _, err := c.Writer.Write(frame.Data); err != nil {
				return // client went away
			}
			c.Writer.Flush()
		case protocol.StreamClose:
			if frame.Error != nil && !headerSent {
				runtimes.ProxyError(c, frame.Error)
			}
			return
		}
	}
}

type uploadResult struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Error string `json:"error,omitempty"`
}

// Upload accepts a multipart form with one or more files and streams each to
// the agent. Files are read from the request as they arrive, so total upload
// size is not held in memory.
func (h *Handler) Upload(c *gin.Context) {
	if !h.authorize(c, "server.files.write") {
		return
	}
	dir := c.Query("path")
	if dir == "" {
		httpx.BadRequest(c, "path (target directory) is required")
		return
	}

	reader, err := c.Request.MultipartReader()
	if err != nil {
		httpx.BadRequest(c, "expected a multipart/form-data body")
		return
	}

	ctx := c.Request.Context()
	results := make([]uploadResult, 0, 4)
	for count := 0; ; count++ {
		if count >= maxUploadFiles {
			httpx.BadRequest(c, fmt.Sprintf("at most %d files per request", maxUploadFiles))
			return
		}
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			httpx.BadRequest(c, "malformed multipart body")
			return
		}
		if part.FileName() == "" {
			_ = part.Close()
			continue
		}
		name := baseName(part.FileName())
		target := path.Join(dir, name)
		size, err := h.uploadOne(ctx, c.Param("id"), target, part)
		_ = part.Close()

		result := uploadResult{Name: name, Path: target, Size: size}
		if err != nil {
			result.Error = err.Error()
		}
		results = append(results, result)
		h.auditor.Write(ctx, audit.Record{
			Action: "files.upload", TargetType: "file",
			TargetID: c.Param("id") + ":" + target, New: gin.H{"size": size}, Err: err,
		})
	}

	status := http.StatusOK
	for _, r := range results {
		if r.Error != "" {
			status = http.StatusMultiStatus
		}
	}
	c.JSON(status, gin.H{"files": results})
}

// uploadOne pumps a single file into the agent over one stream and waits for
// the agent's close frame, which carries the write result.
func (h *Handler) uploadOne(ctx context.Context, serverID, target string, src io.Reader) (int64, error) {
	stream, err := h.hub.OpenStream(ctx, serverID, protocol.MethodFSUpload,
		protocol.FSUploadParams{Path: target})
	if err != nil {
		return 0, err
	}
	defer stream.Close()

	buf := make([]byte, uploadChunk)
	var total int64
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if err := stream.SendData(ctx, buf[:n]); err != nil {
				return total, err
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return total, readErr
		}
	}
	if err := stream.SendCtrl(ctx, protocol.FSUploadCtrl{EOF: true}); err != nil {
		return total, err
	}

	// The agent closes the stream once the file is committed; its close
	// frame reports any write error.
	for {
		frameCtx, cancel := context.WithTimeout(ctx, transferIdleTimeout)
		frame, err := stream.Recv(frameCtx)
		cancel()
		if err != nil {
			return total, err
		}
		if frame.Op == protocol.StreamClose {
			if frame.Error != nil {
				return total, frame.Error
			}
			return total, nil
		}
	}
}

// baseName normalizes a browser-supplied name: some clients send a relative
// path (directory uploads), and only the final element is used.
func baseName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			return name[i+1:]
		}
	}
	return name
}
