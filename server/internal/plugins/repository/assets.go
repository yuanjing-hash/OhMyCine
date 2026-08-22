package repository

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

const (
	CodeAssetInvalid  = "plugin_asset_url_invalid"
	CodeAssetTooLarge = "plugin_asset_too_large"
	CodeAssetDownload = "plugin_asset_download_failed"

	MaxPluginPackageBytes = 64 * 1024 * 1024
	maxAssetRedirects     = 3
)

var githubAssetHosts = map[string]struct{}{
	"release-assets.githubusercontent.com": {},
	"objects.githubusercontent.com":        {},
}

// AssetClient downloads only an already validated same-repository GitHub
// Release asset. GitHub's normal CDN redirects are supported, but every hop is
// HTTPS, bounded, credential-free and restricted to explicit GitHub hosts.
type AssetClient struct {
	client *http.Client
}

func NewAssetClient(transport http.RoundTripper) *AssetClient {
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &AssetClient{}
	client.client = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxAssetRedirects {
				return errors.New("too many GitHub asset redirects")
			}
			if err := validateRedirectTarget(request.URL); err != nil {
				return err
			}
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			request.Header.Del("Proxy-Authorization")
			return nil
		},
	}
	return client
}

func (client *AssetClient) FetchManifest(ctx context.Context, source contract.GitHubRepository, assetURL string) ([]byte, error) {
	return client.fetch(ctx, source, assetURL, contract.MaxManifestBytes)
}

func (client *AssetClient) FetchPackage(ctx context.Context, source contract.GitHubRepository, assetURL string) ([]byte, error) {
	return client.fetch(ctx, source, assetURL, MaxPluginPackageBytes)
}

func (client *AssetClient) fetch(ctx context.Context, source contract.GitHubRepository, assetURL string, maximum int64) ([]byte, error) {
	if err := contract.ValidateGitHubReleaseAssetURL(assetURL, source); err != nil {
		return nil, &Error{Code: CodeAssetInvalid, Cause: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return nil, &Error{Code: CodeAssetInvalid, Cause: err}
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "OhMyCine-Server")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, &Error{Code: CodeAssetDownload, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &Error{Code: CodeAssetDownload, Cause: fmt.Errorf("GitHub asset status %d", response.StatusCode)}
	}
	if err := validateFinalAssetURL(response.Request.URL, source); err != nil {
		return nil, &Error{Code: CodeAssetInvalid, Cause: err}
	}
	if response.ContentLength > maximum {
		return nil, &Error{Code: CodeAssetTooLarge, Cause: errors.New("GitHub asset exceeds maximum size")}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, &Error{Code: CodeAssetDownload, Cause: err}
	}
	if int64(len(data)) > maximum {
		return nil, &Error{Code: CodeAssetTooLarge, Cause: errors.New("GitHub asset exceeds maximum size")}
	}
	return data, nil
}

func validateRedirectTarget(target *url.URL) error {
	if target == nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || target.Port() != "" {
		return errors.New("unsafe GitHub asset redirect")
	}
	host := strings.ToLower(strings.TrimSuffix(target.Hostname(), "."))
	if _, ok := githubAssetHosts[host]; !ok {
		return errors.New("GitHub asset redirected to an untrusted host")
	}
	return nil
}

func validateFinalAssetURL(target *url.URL, source contract.GitHubRepository) error {
	if target == nil {
		return errors.New("missing final GitHub asset URL")
	}
	if strings.EqualFold(target.Hostname(), "github.com") {
		return contract.ValidateGitHubReleaseAssetURL(target.String(), source)
	}
	return validateRedirectTarget(target)
}
