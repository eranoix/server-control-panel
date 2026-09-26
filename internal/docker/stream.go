package docker

import (
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

func (c *Client) StreamLogs(ctx context.Context, id string, w io.Writer, tail string) error {
	rc, err := c.c.ContainerLogs(ctx, id, container.LogsOptions{
		Follow:     true,
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
		Timestamps: false,
	})
	if err != nil {
		return err
	}
	defer rc.Close()

	_, err = stdcopy.StdCopy(w, w, rc)
	return err
}

func (c *Client) StreamStats(ctx context.Context, id string, w io.Writer) error {
	resp, err := c.c.ContainerStats(ctx, id, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = io.Copy(w, resp.Body)
	return err
}
