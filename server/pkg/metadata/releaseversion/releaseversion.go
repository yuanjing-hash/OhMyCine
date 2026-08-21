package releaseversion

import (
	"path"
	"regexp"
	"strings"
)

const (
	tokenStart = `(?:^|[. _\-\[\](){}])`
	tokenEnd   = `(?:$|[. _\-\[\](){}])`
)

type labeledPattern struct {
	label   string
	pattern *regexp.Regexp
}

var (
	editionPatterns = []labeledPattern{
		{label: "Director's Cut", pattern: token(`导演剪辑版`)},
		{label: "Extended Cut", pattern: token(`加长版`)},
		{label: "Final Cut", pattern: token(`终极剪辑版`)},
		{label: "Unrated", pattern: token(`未分级版`)},
		{label: "Extended Director's Cut", pattern: token(`extended[. _-]+director(?:'|’)?s[. _-]+cut`)},
		{label: "Director's Cut", pattern: token(`director(?:'|’)?s[. _-]+cut`)},
		{label: "Extended Cut", pattern: token(`extended[. _-]+cut`)},
		{label: "Extended Edition", pattern: token(`extended[. _-]+edition`)},
		{label: "Theatrical Cut", pattern: token(`theatrical[. _-]+cut`)},
		{label: "Final Cut", pattern: token(`final[. _-]+cut`)},
		{label: "Unrated", pattern: token(`unrated(?:[. _-]+(?:cut|edition))?`)},
		{label: "Open Matte", pattern: token(`open[. _-]+matte`)},
		{label: "IMAX Enhanced", pattern: token(`imax[. _-]+enhanced`)},
		{label: "IMAX", pattern: token(`imax版`)},
		{label: "IMAX", pattern: token(`imax`)},
		{label: "Criterion Collection", pattern: token(`criterion[. _-]+(?:collection|edition)`)},
		{label: "Anniversary Edition", pattern: token(`[0-9]{1,3}(?:st|nd|rd|th)[. _-]+anniversary[. _-]+edition`)},
	}
	resolutionPatterns = []labeledPattern{
		{label: "4320p", pattern: token(`4320p|8k`)},
		{label: "2160p", pattern: token(`2160p|4k`)},
		{label: "1080p", pattern: token(`1080p`)},
		{label: "1080i", pattern: token(`1080i`)},
		{label: "720p", pattern: token(`720p`)},
		{label: "576p", pattern: token(`576p`)},
		{label: "480p", pattern: token(`480p`)},
	}
	sourcePatterns = []labeledPattern{
		{label: "UHD BluRay", pattern: token(`uhd[. _-]+blu[. _-]?ray`)},
		{label: "BluRay", pattern: token(`blu[. _-]?ray`)},
		{label: "WEB-DL", pattern: token(`web[. _-]?dl`)},
		{label: "WEBRip", pattern: token(`web[. _-]?rip`)},
		{label: "BDRip", pattern: token(`bd[. _-]?rip`)},
		{label: "HDTV", pattern: token(`hdtv`)},
		{label: "DVDRip", pattern: token(`dvd[. _-]?rip`)},
	}
	remuxPattern = token(`remux`)
	hdrPatterns  = []labeledPattern{
		{label: "HDR10+", pattern: token(`hdr10(?:\+|plus)`)},
		{label: "HDR10", pattern: token(`hdr10`)},
		{label: "HDR", pattern: token(`hdr`)},
	}
	dolbyVisionPattern = token(`dovi|dolby[. _-]+vision|dv`)
)

func token(expression string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)` + tokenStart + `(?:` + expression + `)` + tokenEnd)
}

// Parse extracts a conservative, provider-neutral release version label from
// a source filename. It intentionally ignores codecs, audio formats, release
// groups and site adornments so the result is stable across downloaders and
// cloud providers.
func Parse(sourcePath string) string {
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(sourcePath), `\`, "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	if name == "" {
		return ""
	}
	labels := make([]string, 0, 6)
	labels = appendMatches(labels, name, editionPatterns, true)
	labels = appendMatches(labels, name, resolutionPatterns, true)
	labels = appendMatches(labels, name, sourcePatterns, true)
	if remuxPattern.MatchString(name) {
		labels = appendUnique(labels, "REMUX")
	}
	labels = appendMatches(labels, name, hdrPatterns, true)
	if dolbyVisionPattern.MatchString(name) {
		labels = appendUnique(labels, "Dolby Vision")
	}
	return strings.Join(labels, " ")
}

func appendMatches(labels []string, value string, patterns []labeledPattern, firstOnly bool) []string {
	for _, candidate := range patterns {
		if !candidate.pattern.MatchString(value) {
			continue
		}
		labels = appendUnique(labels, candidate.label)
		if firstOnly {
			break
		}
	}
	return labels
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
