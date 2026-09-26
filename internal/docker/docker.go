package docker

import (
	"context"
	"encoding/json"
	"io"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

type Client struct {
	c *client.Client
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{c: cli}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.c.Ping(ctx)
	return err
}

func (c *Client) Info(ctx context.Context) (interface{}, error) {
	return c.c.Info(ctx)
}

func (c *Client) ListContainers(ctx context.Context) ([]types.Container, error) {
	return c.c.ContainerList(ctx, container.ListOptions{All: true, Size: true})
}

func (c *Client) Inspect(ctx context.Context, id string) (interface{}, error) {
	j, err := c.c.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (c *Client) Stats(ctx context.Context, id string) (map[string]interface{}, error) {
	resp, err := c.c.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.c.ContainerStart(ctx, id, container.StartOptions{})
}

func (c *Client) Stop(ctx context.Context, id string) error {
	return c.c.ContainerStop(ctx, id, container.StopOptions{})
}

func (c *Client) Restart(ctx context.Context, id string) error {
	return c.c.ContainerRestart(ctx, id, container.StopOptions{})
}

func (c *Client) Kill(ctx context.Context, id string) error {
	return c.c.ContainerKill(ctx, id, "SIGKILL")
}

func (c *Client) Pause(ctx context.Context, id string) error {
	return c.c.ContainerPause(ctx, id)
}

func (c *Client) Unpause(ctx context.Context, id string) error {
	return c.c.ContainerUnpause(ctx, id)
}

func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	return c.c.ContainerRemove(ctx, id, container.RemoveOptions{Force: force, RemoveVolumes: false})
}

func (c *Client) Logs(ctx context.Context, id string, tail string) (string, error) {
	rc, err := c.c.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: tail, Timestamps: true,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return stripDockerStreamHeaders(b), nil
}

func (c *Client) Images(ctx context.Context) ([]image.Summary, error) {
	return c.c.ImageList(ctx, image.ListOptions{All: false})
}

func (c *Client) ImageRemove(ctx context.Context, id string, force bool) error {
	_, err := c.c.ImageRemove(ctx, id, image.RemoveOptions{Force: force})
	return err
}

func (c *Client) Volumes(ctx context.Context) (interface{}, error) {
	return c.c.VolumeList(ctx, volume.ListOptions{})
}

func (c *Client) Networks(ctx context.Context) ([]network.Summary, error) {
	return c.c.NetworkList(ctx, network.ListOptions{})
}

func (c *Client) Top(ctx context.Context, id string) (interface{}, error) {
	return c.c.ContainerTop(ctx, id, nil)
}

func (c *Client) DiskUsage(ctx context.Context) (interface{}, error) {
	return c.c.DiskUsage(ctx, types.DiskUsageOptions{})
}

// Raw returns the underlying client for advanced ops like exec.
func (c *Client) Raw() *client.Client { return c.c }

// stripDockerStreamHeaders removes 8-byte docker multiplexed stream headers.
func stripDockerStreamHeaders(b []byte) string {
	var out []byte
	for i := 0; i+8 <= len(b); {
		length := int(b[i+4])<<24 | int(b[i+5])<<16 | int(b[i+6])<<8 | int(b[i+7])
		i += 8
		end := i + length
		if end > len(b) {
			end = len(b)
		}
		out = append(out, b[i:end]...)
		i = end
	}
	if len(out) == 0 {
		return string(b)
	}
	return string(out)
}
