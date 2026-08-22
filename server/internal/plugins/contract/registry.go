package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const MaxRegistryBytes = 2 * 1024 * 1024

var githubSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type GitHubRepository struct {
	Owner string
	Name  string
}

func (repository GitHubRepository) CanonicalURL() string {
	return "https://github.com/" + repository.Owner + "/" + repository.Name
}

func ParseGitHubRepositoryURL(value string) (GitHubRepository, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return GitHubRepository{}, errors.New("plugin repository must be an HTTPS github.com repository URL")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 2 {
		return GitHubRepository{}, errors.New("plugin repository URL must contain only owner and repository")
	}
	owner, err := url.PathUnescape(segments[0])
	if err != nil {
		return GitHubRepository{}, errors.New("plugin repository owner is invalid")
	}
	name, err := url.PathUnescape(segments[1])
	if err != nil {
		return GitHubRepository{}, errors.New("plugin repository name is invalid")
	}
	name = strings.TrimSuffix(name, ".git")
	if !githubSegmentPattern.MatchString(owner) || !githubSegmentPattern.MatchString(name) || owner == "." || owner == ".." || name == "." || name == ".." {
		return GitHubRepository{}, errors.New("plugin repository owner or name is invalid")
	}
	return GitHubRepository{Owner: owner, Name: name}, nil
}

type RepositoryInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Homepage  string    `json:"homepage"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type RegistryEntry struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Version          string   `json:"version"`
	Channel          string   `json:"channel"`
	Categories       []string `json:"categories"`
	IconURL          string   `json:"iconUrl,omitempty"`
	ManifestURL      string   `json:"manifestUrl"`
	PackageURL       string   `json:"packageUrl"`
	PackageSHA256    string   `json:"packageSha256"`
	MinServerVersion string   `json:"minServerVersion"`
	MaxServerVersion string   `json:"maxServerVersion,omitempty"`
	ReleaseNotes     string   `json:"releaseNotes,omitempty"`
}

type Registry struct {
	SchemaVersion int             `json:"schemaVersion"`
	Repository    RepositoryInfo  `json:"repository"`
	Plugins       []RegistryEntry `json:"plugins"`
}

func ParseRegistry(data []byte, source GitHubRepository) (Registry, error) {
	if len(data) == 0 {
		return Registry{}, errors.New("plugin registry is empty")
	}
	if len(data) > MaxRegistryBytes {
		return Registry{}, fmt.Errorf("plugin registry exceeds %d bytes", MaxRegistryBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var registry Registry
	if err := decoder.Decode(&registry); err != nil {
		return Registry{}, fmt.Errorf("decode plugin registry: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return Registry{}, errors.New("plugin registry contains trailing JSON values")
	} else if !errors.Is(err, io.EOF) {
		return Registry{}, errors.New("plugin registry contains invalid trailing data")
	}
	if err := registry.Validate(source); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func (registry Registry) Validate(source GitHubRepository) error {
	if registry.SchemaVersion != 1 {
		return fmt.Errorf("unsupported plugin registry schema version %d", registry.SchemaVersion)
	}
	if !pluginIDPattern.MatchString(registry.Repository.ID) || strings.TrimSpace(registry.Repository.Name) == "" || registry.Repository.UpdatedAt.IsZero() {
		return errors.New("plugin registry repository metadata is invalid")
	}
	homepage, err := ParseGitHubRepositoryURL(registry.Repository.Homepage)
	if err != nil || !sameGitHubRepository(homepage, source) {
		return errors.New("plugin registry homepage does not match the configured GitHub repository")
	}
	if len(registry.Plugins) > 2000 {
		return errors.New("plugin registry contains too many entries")
	}
	seen := make(map[string]struct{}, len(registry.Plugins))
	for index, entry := range registry.Plugins {
		if err := entry.Validate(source); err != nil {
			return fmt.Errorf("plugin registry entry %d: %w", index, err)
		}
		key := entry.ID + "@" + entry.Version + ":" + entry.Channel
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate plugin registry entry %q", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (entry RegistryEntry) Validate(source GitHubRepository) error {
	if !pluginIDPattern.MatchString(entry.ID) || strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Description) == "" {
		return errors.New("plugin identity is invalid")
	}
	if !validPluginVersion(entry.Version) || !validPluginVersion(entry.MinServerVersion) || (entry.MaxServerVersion != "" && !validPluginVersion(entry.MaxServerVersion)) {
		return errors.New("plugin version range is invalid")
	}
	if entry.MaxServerVersion != "" {
		comparison, _ := CompareVersions(entry.MinServerVersion, entry.MaxServerVersion)
		if comparison > 0 {
			return errors.New("plugin server version range is reversed")
		}
	}
	if entry.Channel != "stable" && entry.Channel != "beta" {
		return errors.New("plugin channel is invalid")
	}
	if len(entry.Categories) > 12 {
		return errors.New("plugin category list is too large")
	}
	if err := validateUniqueStrings(entry.Categories, scopePattern, "plugin category"); err != nil {
		return err
	}
	if !githubReleaseAssetURL(entry.ManifestURL, source) || !githubReleaseAssetURL(entry.PackageURL, source) {
		return errors.New("plugin manifest and package must be GitHub Release assets from the configured repository")
	}
	if entry.IconURL != "" && !githubReleaseAssetURL(entry.IconURL, source) {
		return errors.New("plugin icon must be a GitHub Release asset from the configured repository")
	}
	if _, err := DecodeSHA256(entry.PackageSHA256); err != nil {
		return err
	}
	return nil
}

func githubReleaseAssetURL(value string, source GitHubRepository) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "github.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	return len(segments) == 6 && strings.EqualFold(segments[0], source.Owner) && strings.EqualFold(strings.TrimSuffix(segments[1], ".git"), source.Name) && segments[2] == "releases" && segments[3] == "download" && safeGitHubReleaseSegment(segments[4]) && safeGitHubReleaseSegment(segments[5])
}

// ValidateGitHubReleaseAssetURL verifies that a discovery URL is an exact,
// query-free Release asset belonging to the configured repository. Runtime
// download redirects are validated separately because GitHub uses signed CDN
// URLs after this trusted entry point.
func ValidateGitHubReleaseAssetURL(value string, source GitHubRepository) error {
	if !githubReleaseAssetURL(value, source) {
		return errors.New("plugin asset must be a GitHub Release asset from the configured repository")
	}
	return nil
}

func safeGitHubReleaseSegment(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "\\\x00\r\n")
}

func sameGitHubRepository(left, right GitHubRepository) bool {
	return strings.EqualFold(left.Owner, right.Owner) && strings.EqualFold(left.Name, right.Name)
}
