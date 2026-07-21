package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runix/runix/internal/platform/version"
	"github.com/runix/runix/internal/protocol"
)

const (
	updateDownloadTimeout = 10 * time.Minute
	maxAgentBinaryBytes   = 256 << 20
	// restartExitCode is returned after a successful self-update so the
	// supervisor (systemd Restart=always) starts the new binary.
	restartExitCode = 0
)

func registerUpdateHandlers(reg *rpcRegistry, log logger) {
	reg.call(protocol.MethodAgentUpdate, func(ctx context.Context, raw json.RawMessage) (any, *protocol.Error) {
		params, e := decodeParams[protocol.AgentUpdateParams](raw)
		if e != nil {
			return nil, e
		}
		if params.URL == "" {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "url is required"}
		}
		// The agent typically runs as root: an unverified binary would be a
		// remote code execution path, so the checksum is not optional.
		if len(params.SHA256) != 64 {
			return nil, &protocol.Error{Code: protocol.CodeInvalid,
				Message: "a sha256 checksum (64 hex chars) is required to verify the download"}
		}
		if !strings.HasPrefix(params.URL, "https://") && !strings.HasPrefix(params.URL, "http://") {
			return nil, &protocol.Error{Code: protocol.CodeInvalid, Message: "url must be http(s)"}
		}

		self, err := os.Executable()
		if err != nil {
			return nil, perr(fmt.Errorf("locate own binary: %w", err))
		}
		self, _ = filepath.EvalSymlinks(self)

		downloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), updateDownloadTimeout)
		defer cancel()
		tmp, sum, err := downloadTo(downloadCtx, params.URL, filepath.Dir(self))
		if err != nil {
			return nil, perr(err)
		}
		defer os.Remove(tmp)

		if !strings.EqualFold(sum, params.SHA256) {
			return nil, &protocol.Error{Code: protocol.CodeInvalid,
				Message: fmt.Sprintf("checksum mismatch: downloaded %s, expected %s", sum, params.SHA256)}
		}
		if err := os.Chmod(tmp, 0o755); err != nil {
			return nil, perr(err)
		}
		// Rename over the running binary: the kernel keeps the old inode
		// alive for this process, so the swap is safe while executing.
		if err := os.Rename(tmp, self); err != nil {
			return nil, perr(fmt.Errorf("replace binary: %w", err))
		}

		result := protocol.AgentUpdateResult{
			PreviousVersion: version.Get().Version,
			InstalledPath:   self,
			Restarting:      params.Restart,
		}
		if params.Restart {
			log.Info("agent updated, exiting so the supervisor starts the new binary",
				"path", self, "version", params.Version)
			// Give the reply time to reach the control plane before exiting.
			go func() {
				time.Sleep(2 * time.Second)
				os.Exit(restartExitCode)
			}()
		}
		return result, nil
	})
}

// logger is the slice of slog the update handler needs.
type logger interface {
	Info(msg string, args ...any)
}

// downloadTo streams the URL into a temp file next to dir and returns its
// path and sha256.
func downloadTo(ctx context.Context, url, dir string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download: unexpected status %s", resp.Status)
	}

	f, err := os.CreateTemp(dir, ".runix-agent-update-*")
	if err != nil {
		return "", "", err
	}
	defer f.Close()

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, hasher), io.LimitReader(resp.Body, maxAgentBinaryBytes))
	if err != nil {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download: %w", err)
	}
	if written == 0 {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download: empty response")
	}
	return f.Name(), hex.EncodeToString(hasher.Sum(nil)), nil
}
