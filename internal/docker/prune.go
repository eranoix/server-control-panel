package docker

import (
	"context"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
)

func (c *Client) PruneContainers(ctx context.Context) (interface{}, error) {
	return c.c.ContainersPrune(ctx, filters.NewArgs())
}

func (c *Client) PruneImages(ctx context.Context) (interface{}, error) {
	return c.c.ImagesPrune(ctx, filters.NewArgs())
}

func (c *Client) PruneVolumes(ctx context.Context) (interface{}, error) {
	return c.c.VolumesPrune(ctx, filters.NewArgs())
}

func (c *Client) PruneNetworks(ctx context.Context) (interface{}, error) {
	return c.c.NetworksPrune(ctx, filters.NewArgs())
}

func (c *Client) PruneBuildCache(ctx context.Context) (interface{}, error) {
	return c.c.BuildCachePrune(ctx, types.BuildCachePruneOptions{})
}

func (c *Client) PruneAll(ctx context.Context) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	if r, err := c.PruneContainers(ctx); err == nil {
		result["containers"] = r
	} else {
		result["containers_error"] = err.Error()
	}

	if r, err := c.PruneImages(ctx); err == nil {
		result["images"] = r
	} else {
		result["images_error"] = err.Error()
	}

	if r, err := c.PruneVolumes(ctx); err == nil {
		result["volumes"] = r
	} else {
		result["volumes_error"] = err.Error()
	}

	if r, err := c.PruneNetworks(ctx); err == nil {
		result["networks"] = r
	} else {
		result["networks_error"] = err.Error()
	}

	if r, err := c.PruneBuildCache(ctx); err == nil {
		result["build_cache"] = r
	} else {
		result["build_cache_error"] = err.Error()
	}

	return result, nil
}
