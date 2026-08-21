package mediarecognition

import (
	"embed"
	"fmt"
	"strings"
)

const (
	// PackCodeTV identifies the pinned MoviePilot-Help TV word snapshot.
	PackCodeTV = "tv-v1"
	// PackCodeAnime identifies the pinned MoviePilot-Help anime word snapshot.
	PackCodeAnime = "anime-v1"
)

// PackDescriptor is safe metadata for presenting a built-in word pack in the UI.
// The actual rules are read-only embedded assets.
type PackDescriptor struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	Commit    string `json:"commit"`
	RuleCount int    `json:"rule_count"`
}

var packDescriptors = []PackDescriptor{
	{
		Code:      PackCodeTV,
		Name:      "电视剧预识别词",
		Source:    "https://raw.githubusercontent.com/Putarku/MoviePilot-Help/f99c1b0bfd6721a727260e3e41e7d0bca73af8c7/Words/TV.txt",
		Commit:    "f99c1b0bfd6721a727260e3e41e7d0bca73af8c7",
		RuleCount: 28,
	},
	{
		Code:      PackCodeAnime,
		Name:      "动画预识别词",
		Source:    "https://raw.githubusercontent.com/Putarku/MoviePilot-Help/8f26b5b48ac1a863cae97dd67689d05433394349/Words/anime.txt",
		Commit:    "8f26b5b48ac1a863cae97dd67689d05433394349",
		RuleCount: 294,
	},
}

//go:embed snapshots/tv.txt snapshots/anime.txt snapshots/sources.json snapshots/LICENSE.MoviePilot-Help
var snapshotFS embed.FS

// DefaultPackCodes returns the built-in pack selection for a newly-created
// default profile. A fresh slice is returned so callers cannot mutate defaults.
func DefaultPackCodes() []string {
	return []string{PackCodeTV, PackCodeAnime}
}

// NormalizePackCodes validates a profile selection and returns it in the one
// supported execution order: TV first, then anime. Empty input is a valid way
// to disable all built-in packs; callers creating defaults must explicitly use
// DefaultPackCodes.
func NormalizePackCodes(codes []string) ([]string, error) {
	if len(codes) == 0 {
		return []string{}, nil
	}

	selected := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code != strings.TrimSpace(code) || (code != PackCodeTV && code != PackCodeAnime) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidPackCodes, code)
		}
		if selected[code] {
			return nil, fmt.Errorf("%w: duplicate %q", ErrInvalidPackCodes, code)
		}
		selected[code] = true
	}

	normalized := make([]string, 0, len(selected))
	for _, code := range DefaultPackCodes() {
		if selected[code] {
			normalized = append(normalized, code)
		}
	}
	return normalized, nil
}

// Descriptors returns read-only metadata for all available built-in packs in
// canonical execution order.
func Descriptors() []PackDescriptor {
	result := make([]PackDescriptor, len(packDescriptors))
	copy(result, packDescriptors)
	return result
}

func snapshotForPack(code string) (string, error) {
	var name string
	switch code {
	case PackCodeTV:
		name = "snapshots/tv.txt"
	case PackCodeAnime:
		name = "snapshots/anime.txt"
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidPackCodes, code)
	}
	content, err := snapshotFS.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read embedded word pack %q: %w", code, err)
	}
	return string(content), nil
}
