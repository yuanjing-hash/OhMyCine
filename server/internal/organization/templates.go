package organization

import "strings"

const (
	MovieTypeRoot = "电影"
	TVTypeRoot    = "电视剧"

	DefaultMovieDirectoryTemplate = MovieTypeRoot + "/{category}/{title} ({year})"
	DefaultTVDirectoryTemplate    = TVTypeRoot + "/{category}/{title} ({year})/Season {season:02}"
)

// NormalizeDirectoryTemplate enforces the Server-owned media-type root while
// preserving the Profile-owned structure below it. Existing or wrong/repeated
// fixed roots are removed first so normalization is idempotent.
func NormalizeDirectoryTemplate(value, mediaType string) string {
	root, fallback := MovieTypeRoot, DefaultMovieDirectoryTemplate
	if mediaType == "tv" {
		root, fallback = TVTypeRoot, DefaultTVDirectoryTemplate
	}
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return fallback
	}
	parts := strings.Split(value, "/")
	for len(parts) > 0 && (parts[0] == MovieTypeRoot || parts[0] == TVTypeRoot) {
		parts = parts[1:]
	}
	categoryIndex := -1
	for index, part := range parts {
		if part == "{category}" {
			categoryIndex = index
			break
		}
	}
	if categoryIndex >= 0 {
		parts = append(parts[:categoryIndex], parts[categoryIndex+1:]...)
	}
	parts = append([]string{"{category}"}, parts...)
	return root + "/" + strings.Join(parts, "/")
}
