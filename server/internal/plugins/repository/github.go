package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

const (
	CodeUnavailable = "plugin_repository_unavailable"
	CodeRateLimited = "plugin_registry_rate_limited"
	CodeInvalidSHA  = "plugin_registry_commit_invalid"
	CodeTooLarge    = "plugin_registry_too_large"
	CodeInvalid     = "plugin_registry_invalid"

	githubAPIHost       = "api.github.com"
	maxMetadataResponse = 256 * 1024
)

var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type Error struct {
	Code  string
	Cause error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return CodeUnavailable
}

type Snapshot struct {
	CommitSHA string
	Registry  contract.Registry
	Raw       []byte
}

// GitHubClient reads only the fixed GitHub API surface used by plugin
// repositories. It never accepts a caller-provided API host or raw URL.
type GitHubClient struct {
	client *http.Client
}

func NewGitHubClient(transport http.RoundTripper) *GitHubClient {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &GitHubClient{client: &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (client *GitHubClient) Fetch(ctx context.Context, source contract.GitHubRepository) (Snapshot, error) {
	defaultBranch, err := client.defaultBranch(ctx, source)
	if err != nil {
		return Snapshot{}, err
	}
	commitSHA, err := client.commitSHA(ctx, source, defaultBranch)
	if err != nil {
		return Snapshot{}, err
	}
	raw, err := client.registry(ctx, source, commitSHA)
	if err != nil {
		return Snapshot{}, err
	}
	registry, err := contract.ParseRegistry(raw, source)
	if err != nil {
		return Snapshot{}, &Error{Code: CodeInvalid, Cause: err}
	}
	return Snapshot{CommitSHA: commitSHA, Registry: registry, Raw: raw}, nil
}

func (client *GitHubClient) defaultBranch(ctx context.Context, source contract.GitHubRepository) (string, error) {
	data, err := client.get(ctx, fmt.Sprintf("/repos/%s/%s", source.Owner, source.Name), "application/vnd.github+json", maxMetadataResponse)
	if err != nil {
		return "", err
	}
	var response struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", &Error{Code: CodeUnavailable, Cause: err}
	}
	branch := strings.TrimSpace(response.DefaultBranch)
	if branch == "" || len(branch) > 255 || strings.ContainsAny(branch, "\x00\r\n") {
		return "", &Error{Code: CodeUnavailable, Cause: errors.New("invalid default branch")}
	}
	return branch, nil
}

func (client *GitHubClient) commitSHA(ctx context.Context, source contract.GitHubRepository, branch string) (string, error) {
	path := fmt.Sprintf("/repos/%s/%s/commits/%s", source.Owner, source.Name, url.PathEscape(branch))
	data, err := client.get(ctx, path, "application/vnd.github+json", maxMetadataResponse)
	if err != nil {
		return "", err
	}
	var response struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", &Error{Code: CodeInvalidSHA, Cause: err}
	}
	sha := strings.ToLower(strings.TrimSpace(response.SHA))
	if !commitSHAPattern.MatchString(sha) {
		return "", &Error{Code: CodeInvalidSHA, Cause: errors.New("invalid commit sha")}
	}
	return sha, nil
}

func (client *GitHubClient) registry(ctx context.Context, source contract.GitHubRepository, commitSHA string) ([]byte, error) {
	path := fmt.Sprintf("/repos/%s/%s/contents/ohmycine-plugin-registry.v1.json?ref=%s", source.Owner, source.Name, url.QueryEscape(commitSHA))
	return client.get(ctx, path, "application/vnd.github.raw+json", contract.MaxRegistryBytes)
}

func (client *GitHubClient) get(ctx context.Context, path, accept string, maximum int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+githubAPIHost+path, nil)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Cause: err}
	}
	if request.URL.Scheme != "https" || request.URL.Hostname() != githubAPIHost {
		return nil, &Error{Code: CodeUnavailable, Cause: errors.New("unexpected GitHub API endpoint")}
	}
	request.Header.Set("Accept", accept)
	request.Header.Set("User-Agent", "OhMyCine-Server")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusForbidden && response.Header.Get("X-RateLimit-Remaining") == "0") {
		return nil, &Error{Code: CodeRateLimited, Cause: errors.New("GitHub rate limited")}
	}
	if response.StatusCode != http.StatusOK {
		return nil, &Error{Code: CodeUnavailable, Cause: fmt.Errorf("GitHub status %d", response.StatusCode)}
	}
	limited := io.LimitReader(response.Body, maximum+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, &Error{Code: CodeUnavailable, Cause: err}
	}
	if int64(len(data)) > maximum {
		return nil, &Error{Code: CodeTooLarge, Cause: errors.New("GitHub response too large")}
	}
	return data, nil
}
