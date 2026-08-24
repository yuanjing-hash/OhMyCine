package jellyfin

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/mediaserver/emby"
)

type Config struct {
	Endpoint string
	APIKey   string
}

// Jellyfin deliberately remains an explicit adapter even though its current
// administration endpoints are Emby-compatible. Keeping the boundary avoids
// leaking provider decisions into refresh orchestration.
type Client struct{ inner *emby.Client }

func New(config Config) (*Client, error) {
	inner, err := emby.New(emby.Config{Endpoint: config.Endpoint, APIKey: config.APIKey})
	if err != nil {
		return nil, err
	}
	return &Client{inner: inner}, nil
}

func ParseEndpoint(value string) (*url.URL, error) { return emby.ParseEndpoint(value) }
func NormalizeAPIKey(value string) (string, error) { return emby.NormalizeAPIKey(value) }

func (c *Client) Probe(ctx context.Context) (mediaserver.ServerInfo, error) {
	info, err := c.inner.Probe(ctx)
	if err != nil {
		return mediaserver.ServerInfo{}, err
	}
	if strings.TrimSpace(info.ID) == "" {
		return mediaserver.ServerInfo{}, errors.New("jellyfin probe response is invalid")
	}
	return info, nil
}

func (c *Client) ListLibraries(ctx context.Context) ([]mediaserver.Library, error) {
	return c.inner.ListLibraries(ctx)
}

func (c *Client) RefreshLibrary(ctx context.Context, libraryID string) error {
	return c.inner.RefreshLibrary(ctx, libraryID)
}
