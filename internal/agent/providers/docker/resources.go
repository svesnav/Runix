package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Image, Volume and Network management. These are Docker-specific object
// types rather than runtimes, so they live outside the Runtime abstraction
// and are surfaced through their own API.

type Image struct {
	ID         string   `json:"id"`
	RepoTags   []string `json:"repoTags"`
	Size       int64    `json:"size"`
	Created    int64    `json:"created"`
	Containers int64    `json:"containers"`
	Dangling   bool     `json:"dangling"`
}

func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var raw []struct {
		ID         string   `json:"Id"`
		RepoTags   []string `json:"RepoTags"`
		Size       int64    `json:"Size"`
		Created    int64    `json:"Created"`
		Containers int64    `json:"Containers"`
	}
	q := url.Values{}
	q.Set("all", "0")
	if err := c.getJSON(ctx, "/images/json", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(raw))
	for _, img := range raw {
		tags := img.RepoTags
		dangling := len(tags) == 0 || (len(tags) == 1 && tags[0] == "<none>:<none>")
		if tags == nil {
			tags = []string{}
		}
		out = append(out, Image{
			ID: img.ID, RepoTags: tags, Size: img.Size, Created: img.Created,
			Containers: img.Containers, Dangling: dangling,
		})
	}
	return out, nil
}

// PullImage streams the pull and blocks until it completes, surfacing the
// daemon's error if one appears in the progress stream.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	name, tag := splitImageRef(ref)
	q := url.Values{}
	q.Set("fromImage", name)
	if tag != "" {
		q.Set("tag", tag)
	}
	resp, err := c.do(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("docker: pull %s: %w", ref, err)
		}
		if msg.Error != "" {
			return fmt.Errorf("docker: pull %s: %s", ref, msg.Error)
		}
	}
}

func splitImageRef(ref string) (name, tag string) {
	if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
		return ref[:idx], ref[idx+1:]
	}
	return ref, "latest"
}

func (c *Client) RemoveImage(ctx context.Context, id string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/images/"+url.PathEscape(id), q, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

type PruneResult struct {
	Reclaimed int64 `json:"reclaimedBytes"`
	Deleted   int   `json:"deleted"`
}

func (c *Client) PruneImages(ctx context.Context) (PruneResult, error) {
	var out struct {
		ImagesDeleted  []struct{} `json:"ImagesDeleted"`
		SpaceReclaimed int64      `json:"SpaceReclaimed"`
	}
	resp, err := c.do(ctx, http.MethodPost, "/images/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Reclaimed: out.SpaceReclaimed, Deleted: len(out.ImagesDeleted)}, nil
}

type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	CreatedAt  string            `json:"createdAt"`
	Labels     map[string]string `json:"labels"`
}

func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	var out struct {
		Volumes []struct {
			Name       string            `json:"Name"`
			Driver     string            `json:"Driver"`
			Mountpoint string            `json:"Mountpoint"`
			CreatedAt  string            `json:"CreatedAt"`
			Labels     map[string]string `json:"Labels"`
		} `json:"Volumes"`
	}
	if err := c.getJSON(ctx, "/volumes", nil, &out); err != nil {
		return nil, err
	}
	volumes := make([]Volume, 0, len(out.Volumes))
	for _, v := range out.Volumes {
		volumes = append(volumes, Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint,
			CreatedAt: v.CreatedAt, Labels: v.Labels,
		})
	}
	return volumes, nil
}

func (c *Client) CreateVolume(ctx context.Context, name, driver string, labels map[string]string) error {
	if driver == "" {
		driver = "local"
	}
	return c.post(ctx, "/volumes/create", nil, map[string]any{
		"Name": name, "Driver": driver, "Labels": labels,
	})
}

func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/volumes/"+url.PathEscape(name), q, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) PruneVolumes(ctx context.Context) (PruneResult, error) {
	var out struct {
		VolumesDeleted []string `json:"VolumesDeleted"`
		SpaceReclaimed int64    `json:"SpaceReclaimed"`
	}
	resp, err := c.do(ctx, http.MethodPost, "/volumes/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Reclaimed: out.SpaceReclaimed, Deleted: len(out.VolumesDeleted)}, nil
}

type Network struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Containers int               `json:"containers"`
	Labels     map[string]string `json:"labels"`
}

func (c *Client) ListNetworks(ctx context.Context) ([]Network, error) {
	var raw []struct {
		ID         string            `json:"Id"`
		Name       string            `json:"Name"`
		Driver     string            `json:"Driver"`
		Scope      string            `json:"Scope"`
		Internal   bool              `json:"Internal"`
		Containers map[string]any    `json:"Containers"`
		Labels     map[string]string `json:"Labels"`
	}
	if err := c.getJSON(ctx, "/networks", nil, &raw); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(raw))
	for _, n := range raw {
		out = append(out, Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope,
			Internal: n.Internal, Containers: len(n.Containers), Labels: n.Labels,
		})
	}
	return out, nil
}

func (c *Client) CreateNetwork(ctx context.Context, name, driver string, internal bool) error {
	if driver == "" {
		driver = "bridge"
	}
	return c.post(ctx, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": driver, "Internal": internal,
	})
}

func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/networks/"+url.PathEscape(id), nil, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (c *Client) PruneNetworks(ctx context.Context) (PruneResult, error) {
	var out struct {
		NetworksDeleted []string `json:"NetworksDeleted"`
	}
	resp, err := c.do(ctx, http.MethodPost, "/networks/prune", nil, nil)
	if err != nil {
		return PruneResult{}, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Deleted: len(out.NetworksDeleted)}, nil
}

// DiskUsage summarizes what Docker is consuming on the host.
type DiskUsage struct {
	ImagesSize     int64 `json:"imagesSize"`
	ContainersSize int64 `json:"containersSize"`
	VolumesSize    int64 `json:"volumesSize"`
}

func (c *Client) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var out struct {
		LayersSize int64 `json:"LayersSize"`
		Containers []struct {
			SizeRw int64 `json:"SizeRw"`
		} `json:"Containers"`
		Volumes []struct {
			UsageData struct {
				Size int64 `json:"Size"`
			} `json:"UsageData"`
		} `json:"Volumes"`
	}
	if err := c.getJSON(ctx, "/system/df", nil, &out); err != nil {
		return DiskUsage{}, err
	}
	usage := DiskUsage{ImagesSize: out.LayersSize}
	for _, ctr := range out.Containers {
		usage.ContainersSize += ctr.SizeRw
	}
	for _, v := range out.Volumes {
		usage.VolumesSize += v.UsageData.Size
	}
	return usage, nil
}

// Resources exposes the object-management surface to the agent RPC layer
// without it needing the concrete provider type.
func (p *Provider) Client() *Client { return p.client }
