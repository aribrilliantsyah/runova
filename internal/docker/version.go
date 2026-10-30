package docker

import "context"

// Version returns the Engine's version, e.g. "28.4.0".
func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"Version"`
	}
	if err := c.call(ctx, "GET", "/version", nil, nil, &v); err != nil {
		return "", err
	}
	return v.Version, nil
}
