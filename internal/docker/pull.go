package docker

import (
	"bufio"
	"context"

	"github.com/docker/docker/api/types/image"
)

func (c *Client) PullImage(ctx context.Context, ref string, progress func(line string)) error {
	rc, err := c.c.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	// Progress lines can be long; bump the buffer.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		if progress != nil {
			progress(scanner.Text())
		}
	}
	return scanner.Err()
}
