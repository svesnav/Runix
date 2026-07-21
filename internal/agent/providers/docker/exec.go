package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The Engine API's exec/attach endpoints do not speak plain HTTP: after the
// upgrade response the connection becomes a raw bidirectional byte stream.
// net/http cannot hand that back, so these requests are written directly
// onto a dialed socket.

type hijackedConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", c.socketPath)
}

// hijack issues a POST that upgrades to a raw stream and returns the
// connection positioned at the first payload byte.
func (c *Client) hijack(ctx context.Context, path string, body any) (*hijackedConn, error) {
	v, err := c.negotiate(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("docker: dial: %w", err)
	}

	payload := []byte("{}")
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			conn.Close()
			return nil, err
		}
	}
	req := fmt.Sprintf(
		"POST /v%s%s HTTP/1.1\r\nHost: docker\r\nContent-Type: application/json\r\n"+
			"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: %d\r\n\r\n%s",
		v.APIVersion, path, len(payload), payload)
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("docker: write hijack request: %w", err)
	}

	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("docker: read hijack response: %w", err)
	}
	// 101 Switching Protocols (upgrade honored) or 200 (stream follows).
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		conn.Close()
		return nil, &apiError{Status: resp.StatusCode, Message: string(msg)}
	}
	// Clear the handshake deadline: the stream is long-lived.
	_ = conn.SetDeadline(time.Time{})
	return &hijackedConn{conn: conn, r: r}, nil
}

func (h *hijackedConn) Read(p []byte) (int, error)  { return h.r.Read(p) }
func (h *hijackedConn) Write(p []byte) (int, error) { return h.conn.Write(p) }
func (h *hijackedConn) Close() error                { return h.conn.Close() }

// closeWrite signals EOF on stdin without tearing down the read side.
func (h *hijackedConn) closeWrite() error {
	if cw, ok := h.conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// attach opens the container's *main process* streams. Unlike exec, this is
// the process's own stdin/stdout, which is what a console-driven program
// (game server, REPL daemon) listens on.
func (c *Client) attach(ctx context.Context, container string) (*hijackedConn, error) {
	return c.hijack(ctx,
		"/containers/"+url.PathEscape(container)+"/attach?stream=1&stdin=1&stdout=1&stderr=1", nil)
}

type execConfig struct {
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
	Cmd          []string `json:"Cmd"`
	Env          []string `json:"Env,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	User         string   `json:"User,omitempty"`
}

func (c *Client) createExec(ctx context.Context, container string, cfg execConfig) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+url.PathEscape(container)+"/exec", nil, cfg)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("docker: decode exec id: %w", err)
	}
	return out.ID, nil
}

func (c *Client) startExec(ctx context.Context, execID string, tty bool) (*hijackedConn, error) {
	return c.hijack(ctx, "/exec/"+url.PathEscape(execID)+"/start", map[string]bool{
		"Detach": false,
		"Tty":    tty,
	})
}

func (c *Client) resizeExec(ctx context.Context, execID string, cols, rows uint16) error {
	q := url.Values{}
	q.Set("w", strconv.Itoa(int(cols)))
	q.Set("h", strconv.Itoa(int(rows)))
	return c.post(ctx, "/exec/"+url.PathEscape(execID)+"/resize", q, nil)
}

func (c *Client) execExitCode(ctx context.Context, execID string) (int, error) {
	var out struct {
		ExitCode int  `json:"ExitCode"`
		Running  bool `json:"Running"`
	}
	if err := c.getJSON(ctx, "/exec/"+url.PathEscape(execID)+"/json", nil, &out); err != nil {
		return 0, err
	}
	return out.ExitCode, nil
}

// demux splits Docker's stdout/stderr multiplexed stream (8-byte header per
// frame). Used when the exec has no TTY.
func demux(r io.Reader, stdout, stderr io.Writer) error {
	var header [8]byte
	for {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		dst := stdout
		if header[0] == 2 {
			dst = stderr
		}
		if dst == nil {
			dst = io.Discard
		}
		if _, err := io.CopyN(dst, r, size); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// execTerminal adapts a hijacked exec stream to the runtime Terminal
// contract.
type execTerminal struct {
	client *Client
	execID string
	conn   *hijackedConn
	once   sync.Once
}

func (t *execTerminal) Read(p []byte) (int, error)  { return t.conn.Read(p) }
func (t *execTerminal) Write(p []byte) (int, error) { return t.conn.Write(p) }

func (t *execTerminal) Resize(cols, rows uint16) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return t.client.resizeExec(ctx, t.execID, cols, rows)
}

func (t *execTerminal) Close() error {
	var err error
	t.once.Do(func() { err = t.conn.Close() })
	return err
}
