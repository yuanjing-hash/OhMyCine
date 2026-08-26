package mediarecognition

import (
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FileEpisodeFact is the provider-neutral season/episode identity used by
// both manifest selection and Transfer planning. RelativePath remains inside
// the private completed-manifest boundary.
type FileEpisodeFact struct {
	RelativePath string `json:"relative_path"`
	Season       *int   `json:"season,omitempty"`
	Episode      *int   `json:"episode,omitempty"`
	Evidence     string `json:"evidence"`
}

type PackageEpisodeFacts struct {
	VideoCount    int               `json:"video_count"`
	ResolvedCount int               `json:"resolved_count"`
	Complete      bool              `json:"complete"`
	Files         []FileEpisodeFact `json:"files"`
}

// ResolvePackageEpisodes resolves one immutable package as a unit. Weak
// bracket numbers are accepted only for TV input with release evidence, and a
// multi-video package is complete only when every video has a unique episode.
func ResolvePackageEpisodes(files []FileFact, mediaType MediaType) PackageEpisodeFacts {
	result := PackageEpisodeFacts{Files: []FileEpisodeFact{}}
	if mediaType != MediaTypeTV {
		return result
	}
	type candidate struct {
		path     string
		season   *int
		episode  *int
		evidence string
	}
	candidates := make([]candidate, 0, len(files))
	for _, file := range files {
		relative := path.Clean(strings.TrimLeft(strings.ReplaceAll(file.RelativePath, "\\", "/"), "/"))
		if relative == "." || !videoExtensionPattern.MatchString(relative) {
			continue
		}
		result.VideoCount++
		base := path.Base(relative)
		season, episode := firstSeasonEpisode(base)
		evidence := "structured"
		if episode == nil {
			analyzed := analyzeName(namedSource{value: base, source: "filename"}, stableParseTime())
			season, episode = cloneDomainInt(analyzed.season), cloneDomainInt(analyzed.episode)
			if episode != nil {
				evidence = "release_name"
			}
		}
		if episode == nil && bracketReleaseEvidence(base) {
			episode = bracketEpisodeFromName(base)
			if episode != nil {
				evidence = "bracket_release"
			}
		}
		if season == nil {
			season, _ = firstSeasonEpisode(relative)
			if season == nil {
				season = seasonFromPath(relative)
			}
		}
		if episode != nil && season == nil {
			value := 1
			season = &value
		}
		candidates = append(candidates, candidate{path: relative, season: season, episode: episode, evidence: evidence})
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		if item.episode == nil {
			continue
		}
		season := 1
		if item.season != nil {
			season = *item.season
		}
		key := strconv.Itoa(season) + ":" + strconv.Itoa(*item.episode)
		if _, duplicate := seen[key]; duplicate && len(candidates) > 1 {
			continue
		}
		seen[key] = struct{}{}
		result.Files = append(result.Files, FileEpisodeFact{RelativePath: item.path, Season: cloneDomainInt(item.season), Episode: cloneDomainInt(item.episode), Evidence: item.evidence})
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].RelativePath < result.Files[j].RelativePath })
	result.ResolvedCount = len(result.Files)
	result.Complete = result.VideoCount > 0 && result.ResolvedCount == result.VideoCount && len(seen) == result.VideoCount
	return result
}

func stableParseTime() time.Time {
	return time.Date(2198, 1, 1, 0, 0, 0, 0, time.UTC)
}

func bracketReleaseEvidence(value string) bool {
	return hasTechnicalToken(value) || subtitleTokenPattern.MatchString(value) || strings.Count(value, "[")+strings.Count(value, "【") >= 3
}

func seasonFromPath(value string) *int {
	for directory := path.Dir(value); directory != "." && directory != "/"; directory = path.Dir(directory) {
		if match := seasonPattern.FindStringSubmatch(path.Base(directory)); len(match) == 2 {
			season, err := strconv.Atoi(match[1])
			if err == nil && season >= 0 && season <= 200 {
				return &season
			}
		}
	}
	return nil
}
