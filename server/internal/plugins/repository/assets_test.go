package repository

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

func TestAssetClientAllowsOnlyBoundedGitHubCDNRedirect(t *testing.T) {
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	requests := 0
	client := NewAssetClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if request.URL.Hostname() != "github.com" || request.Method != http.MethodGet {
				t.Fatalf("initial request=%s %s", request.Method, request.URL)
			}
			response := response(http.StatusFound, "")
			response.Request = request
			response.Header.Set("Location", "https://release-assets.githubusercontent.com/github-production-release-asset/file?sp=token")
			return response, nil
		case 2:
			if request.URL.Hostname() != "release-assets.githubusercontent.com" || request.Header.Get("Authorization") != "" || request.Header.Get("Cookie") != "" {
				t.Fatalf("unsafe redirected request=%s headers=%v", request.URL, request.Header)
			}
			response := response(http.StatusOK, "manifest")
			response.Request = request
			return response, nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	}))
	data, err := client.FetchManifest(context.Background(), source, "https://github.com/ohmycine/plugins/releases/download/v0.1.0/plugin.json")
	if err != nil || string(data) != "manifest" || requests != 2 {
		t.Fatalf("data=%q requests=%d err=%v", data, requests, err)
	}
}

func TestAssetClientRejectsUntrustedRedirectAndOversizedBody(t *testing.T) {
	source := contract.GitHubRepository{Owner: "ohmycine", Name: "plugins"}
	t.Run("untrusted redirect", func(t *testing.T) {
		client := NewAssetClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
			response := response(http.StatusFound, "")
			response.Header.Set("Location", "https://example.com/plugin.omcp")
			return response, nil
		}))
		_, err := client.FetchPackage(context.Background(), source, "https://github.com/ohmycine/plugins/releases/download/v0.1.0/plugin.omcp")
		if ErrorCode(err) != CodeAssetDownload {
			t.Fatalf("error=%v code=%s", err, ErrorCode(err))
		}
	})

	t.Run("oversized manifest", func(t *testing.T) {
		client := NewAssetClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", contract.MaxManifestBytes+1))), Request: request}, nil
		}))
		_, err := client.FetchManifest(context.Background(), source, "https://github.com/ohmycine/plugins/releases/download/v0.1.0/plugin.json")
		if ErrorCode(err) != CodeAssetTooLarge {
			t.Fatalf("error=%v code=%s", err, ErrorCode(err))
		}
	})
}
