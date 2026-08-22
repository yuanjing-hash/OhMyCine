package repository

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestGitHubClientFetchPinsRegistryToCommit(t *testing.T) {
	sha := strings.Repeat("a", 40)
	registry := validRegistry("ohmycine", "example-plugins")
	requestIndex := 0
	client := NewGitHubClient(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestIndex++
		if request.Method != http.MethodGet || request.URL.Scheme != "https" || request.URL.Host != githubAPIHost {
			t.Fatalf("unsafe request: %s %s", request.Method, request.URL.String())
		}
		var body string
		switch requestIndex {
		case 1:
			if request.URL.Path != "/repos/ohmycine/example-plugins" {
				t.Fatalf("repository path=%q", request.URL.Path)
			}
			body = `{"default_branch":"main"}`
		case 2:
			if request.URL.EscapedPath() != "/repos/ohmycine/example-plugins/commits/main" {
				t.Fatalf("commit path=%q", request.URL.EscapedPath())
			}
			body = `{"sha":"` + sha + `"}`
		case 3:
			if request.URL.Path != "/repos/ohmycine/example-plugins/contents/ohmycine-plugin-registry.v1.json" || request.URL.Query().Get("ref") != sha {
				t.Fatalf("registry request=%s", request.URL.String())
			}
			if request.Header.Get("Accept") != "application/vnd.github.raw+json" {
				t.Fatalf("registry accept=%q", request.Header.Get("Accept"))
			}
			body = registry
		default:
			t.Fatalf("unexpected request %d", requestIndex)
		}
		return response(http.StatusOK, body), nil
	}))

	snapshot, err := client.Fetch(context.Background(), contract.GitHubRepository{Owner: "ohmycine", Name: "example-plugins"})
	if err != nil {
		t.Fatal(err)
	}
	if requestIndex != 3 || snapshot.CommitSHA != sha || len(snapshot.Registry.Plugins) != 1 {
		t.Fatalf("snapshot=%+v requests=%d", snapshot, requestIndex)
	}
}

func TestGitHubClientRejectsInvalidCommitAndOversizedRegistry(t *testing.T) {
	t.Run("invalid commit", func(t *testing.T) {
		index := 0
		client := NewGitHubClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
			index++
			if index == 1 {
				return response(http.StatusOK, `{"default_branch":"main"}`), nil
			}
			return response(http.StatusOK, `{"sha":"not-a-commit"}`), nil
		}))
		_, err := client.Fetch(context.Background(), contract.GitHubRepository{Owner: "ohmycine", Name: "example-plugins"})
		if ErrorCode(err) != CodeInvalidSHA {
			t.Fatalf("error=%v code=%s", err, ErrorCode(err))
		}
	})

	t.Run("oversized registry", func(t *testing.T) {
		index := 0
		sha := strings.Repeat("b", 40)
		client := NewGitHubClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
			index++
			switch index {
			case 1:
				return response(http.StatusOK, `{"default_branch":"main"}`), nil
			case 2:
				return response(http.StatusOK, `{"sha":"`+sha+`"}`), nil
			default:
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(make([]byte, contract.MaxRegistryBytes+1)))}, nil
			}
		}))
		_, err := client.Fetch(context.Background(), contract.GitHubRepository{Owner: "ohmycine", Name: "example-plugins"})
		if ErrorCode(err) != CodeTooLarge {
			t.Fatalf("error=%v code=%s", err, ErrorCode(err))
		}
	})
}

func TestGitHubClientDoesNotFollowRedirects(t *testing.T) {
	client := NewGitHubClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		response := response(http.StatusFound, "")
		response.Header.Set("Location", "https://attacker.example.test/registry")
		return response, nil
	}))
	_, err := client.Fetch(context.Background(), contract.GitHubRepository{Owner: "ohmycine", Name: "example-plugins"})
	if ErrorCode(err) != CodeUnavailable {
		t.Fatalf("error=%v code=%s", err, ErrorCode(err))
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func validRegistry(owner, name string) string {
	return `{
  "schemaVersion": 1,
  "repository": {"id":"org.ohmycine.fixture-repository","name":"Fixture","homepage":"https://github.com/` + owner + `/` + name + `","updatedAt":"2026-08-22T00:00:00Z"},
  "plugins": [{
    "id":"org.ohmycine.fixture.static-site","name":"Static Site","description":"Fixture plugin.","version":"0.1.0","channel":"stable","categories":["online-media"],
    "manifestUrl":"https://github.com/` + owner + `/` + name + `/releases/download/v0.1.0/plugin.json",
    "packageUrl":"https://github.com/` + owner + `/` + name + `/releases/download/v0.1.0/plugin.omcp",
    "packageSha256":"0000000000000000000000000000000000000000000000000000000000000000","minServerVersion":"0.1.0"
  }]
}`
}
