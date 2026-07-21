// Package docker implements the Docker runtime provider against the Engine
// HTTP API over the local socket. A hand-rolled minimal client keeps the
// agent free of the official SDK's very large dependency tree; only the
// endpoints Runix uses are implemented.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	http       *http.Client
	host       string
	socketPath string

	mu         sync.Mutex
	apiVersion string // negotiated from the engine's /version
	engine     string
}

// NewClient connects to the engine socket (default /var/run/docker.sock).
func NewClient(socketPath string) *Client {
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}
	return &Client{
		host:       "http://docker",
		socketPath: socketPath,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
				MaxIdleConns:    4,
				IdleConnTimeout: 60 * time.Second,
			},
		},
	}
}

type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("docker api %d: %s", e.Status, e.Message)
}

type versionInfo struct {
	Version    string `json:"Version"`
	APIVersion string `json:"ApiVersion"`
}

// negotiate pins the client to whatever API version the local engine
// speaks. Engines both advertise and require moving versions (Ubuntu 24.04
// ships an engine with a 1.44 minimum), so a hardcoded version breaks in
// either direction.
func (c *Client) negotiate(ctx context.Context) (versionInfo, error) {
	c.mu.Lock()
	if c.apiVersion != "" {
		v := versionInfo{Version: c.engine, APIVersion: c.apiVersion}
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+"/version", nil)
	if err != nil {
		return versionInfo{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return versionInfo{}, fmt.Errorf("docker: version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return versionInfo{}, &apiError{Status: resp.StatusCode, Message: "version query failed"}
	}
	var v versionInfo
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return versionInfo{}, fmt.Errorf("docker: parse version: %w", err)
	}
	if v.APIVersion == "" {
		v.APIVersion = "1.44"
	}
	c.mu.Lock()
	c.apiVersion, c.engine = v.APIVersion, v.Version
	c.mu.Unlock()
	return v, nil
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any) (*http.Response, error) {
	v, err := c.negotiate(ctx)
	if err != nil {
		return nil, err
	}
	u := c.host + "/v" + v.APIVersion + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("docker: marshal body: %w", err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker: %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&msg)
		return nil, &apiError{Status: resp.StatusCode, Message: msg.Message}
	}
	return resp, nil
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) post(ctx context.Context, path string, query url.Values, body any) error {
	resp, err := c.do(ctx, http.MethodPost, path, query, body)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Ping re-negotiates against the engine so availability reporting stays
// fresh across daemon restarts and upgrades.
func (c *Client) Ping(ctx context.Context) (string, error) {
	c.mu.Lock()
	c.apiVersion = ""
	c.mu.Unlock()
	v, err := c.negotiate(ctx)
	return v.Version, err
}

// Container is the list-view shape.
type Container struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
	Created int64             `json:"Created"`
}

func (c *Client) ListContainers(ctx context.Context, all bool) ([]Container, error) {
	q := url.Values{}
	if all {
		q.Set("all", "1")
	}
	var out []Container
	if err := c.getJSON(ctx, "/containers/json", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ContainerDetail is the inspect-view shape (subset).
type ContainerDetail struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status     string `json:"Status"`
		Running    bool   `json:"Running"`
		Paused     bool   `json:"Paused"`
		Restarting bool   `json:"Restarting"`
		ExitCode   int    `json:"ExitCode"`
		StartedAt  string `json:"StartedAt"`
		FinishedAt string `json:"FinishedAt"`
		Health     *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
	Config struct {
		Image     string            `json:"Image"`
		Labels    map[string]string `json:"Labels"`
		Tty       bool              `json:"Tty"`
		OpenStdin bool              `json:"OpenStdin"`
	} `json:"Config"`
	RestartCount int    `json:"RestartCount"`
	Created      string `json:"Created"`
}

func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerDetail, error) {
	var out ContainerDetail
	err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/json", nil, &out)
	return out, err
}

// InspectRaw returns the full inspect document untouched.
func (c *Client) InspectRaw(ctx context.Context, id string) (json.RawMessage, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/json", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	err := c.post(ctx, "/containers/"+url.PathEscape(id)+"/start", nil, nil)
	var ae *apiError
	// 304 = already started; the engine reports it as an error status.
	if err != nil {
		if ok := errAs(err, &ae); ok && ae.Status == http.StatusNotModified {
			return nil
		}
	}
	return err
}

func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{}
	if timeout > 0 {
		q.Set("t", strconv.Itoa(int(timeout.Seconds())))
	}
	err := c.post(ctx, "/containers/"+url.PathEscape(id)+"/stop", q, nil)
	var ae *apiError
	if err != nil {
		if ok := errAs(err, &ae); ok && ae.Status == http.StatusNotModified {
			return nil
		}
	}
	return err
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{}
	if timeout > 0 {
		q.Set("t", strconv.Itoa(int(timeout.Seconds())))
	}
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/restart", q, nil)
}

func (c *Client) KillContainer(ctx context.Context, id, signal string) error {
	q := url.Values{}
	if signal != "" {
		q.Set("signal", signal)
	}
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/kill", q, nil)
}

func (c *Client) PauseContainer(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/pause", nil, nil)
}

func (c *Client) UnpauseContainer(ctx context.Context, id string) error {
	return c.post(ctx, "/containers/"+url.PathEscape(id)+"/unpause", nil, nil)
}

func (c *Client) RemoveContainer(ctx context.Context, id string, force, volumes bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if volumes {
		q.Set("v", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/containers/"+url.PathEscape(id), q, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// CreateConfig is the provider-facing container creation document.
type CreateConfig struct {
	Image         string            `json:"image"`
	Cmd           []string          `json:"cmd,omitempty"`
	Env           []string          `json:"env,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Binds         []string          `json:"binds,omitempty"`
	Ports         []string          `json:"ports,omitempty"` // "8080:80/tcp"
	RestartPolicy string            `json:"restartPolicy,omitempty"`
	// Tty allocates a pseudo-terminal. Console-driven servers usually want
	// this off so output stays line-oriented.
	Tty bool `json:"tty,omitempty"`
}

func (c *Client) CreateContainer(ctx context.Context, name string, cfg CreateConfig) (string, error) {
	portBindings := map[string][]map[string]string{}
	exposed := map[string]struct{}{}
	for _, p := range cfg.Ports {
		parts := strings.SplitN(p, ":", 2)
		if len(parts) != 2 {
			return "", fmt.Errorf("docker: bad port mapping %q (want host:container[/proto])", p)
		}
		containerPort := parts[1]
		if !strings.Contains(containerPort, "/") {
			containerPort += "/tcp"
		}
		exposed[containerPort] = struct{}{}
		portBindings[containerPort] = append(portBindings[containerPort],
			map[string]string{"HostPort": parts[0]})
	}
	body := map[string]any{
		"Image":  cfg.Image,
		"Cmd":    cfg.Cmd,
		"Env":    cfg.Env,
		"Labels": cfg.Labels,
		// Keep stdin open so the container's console can be attached later.
		// Without this the process gets no input stream and the console is
		// permanently unavailable — a decision that cannot be undone
		// without recreating the container.
		"OpenStdin":    true,
		"StdinOnce":    false,
		"Tty":          cfg.Tty,
		"ExposedPorts": exposed,
		"HostConfig": map[string]any{
			"Binds":        cfg.Binds,
			"PortBindings": portBindings,
			"RestartPolicy": map[string]any{
				"Name": cfg.RestartPolicy,
			},
		},
	}
	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	resp, err := c.do(ctx, http.MethodPost, "/containers/create", q, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Logs returns the raw log body; multiplexed unless the container has a TTY.
func (c *Client) Logs(ctx context.Context, id string, follow bool, tail int, timestamps bool) (io.ReadCloser, error) {
	q := url.Values{}
	q.Set("stdout", "1")
	q.Set("stderr", "1")
	if follow {
		q.Set("follow", "1")
	}
	if tail > 0 {
		q.Set("tail", strconv.Itoa(tail))
	}
	if timestamps {
		q.Set("timestamps", "1")
	}
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", q, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Stats returns one non-streaming stats sample.
type StatsSample struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs  uint64 `json:"online_cpus"`
		Throttling  struct {
			ThrottledPeriods uint64 `json:"throttled_periods"`
		} `json:"throttling_data"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
	BlkioStats struct {
		IOServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
}

func (c *Client) Stats(ctx context.Context, id string) (StatsSample, error) {
	q := url.Values{}
	q.Set("stream", "0")
	var out StatsSample
	err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/stats", q, &out)
	return out, err
}
