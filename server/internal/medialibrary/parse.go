package medialibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var (
	sxxexxPattern         = regexp.MustCompile(`(?i)(?:^|[. _-])S\s*0*([0-9]{1,2})\s*E\s*0*([0-9]{1,5})(?:[. _-]|$)`)
	oneXEpisodePattern    = regexp.MustCompile(`(?i)(?:^|[. _-])0*([0-9]{1,2})x0*([0-9]{1,5})(?:[. _-]|$)`)
	episodeOnlyPattern    = regexp.MustCompile(`(?i)(?:^|[. _-])(?:EP?|Episode)\s*0*([0-9]{1,5})(?:[. _-]|$)`)
	chineseEpisodePattern = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百两〇零]+)\s*[集话話]`)
	seasonFolderPattern   = regexp.MustCompile(`(?i)^(?:Season|Seanson|S)\s*0*([0-9]{1,2})$`)
	chineseSeasonPattern  = regexp.MustCompile(`^第\s*([0-9一二三四五六七八九十百两〇零]+)\s*季$`)
	yearPattern           = regexp.MustCompile(`(?:^|[\s._(（\[【-])((?:18|19|20|21)[0-9]{2})(?:$|[\s._)）\]】-])`)
	bracketNoisePattern   = regexp.MustCompile(`\[[^\]]+\]|\([^)]*\)|【[^】]+】|（[^）]*）`)
	technicalTokenPattern = regexp.MustCompile(`(?i)(?:^|[. _-])(?:2160p|1080p|720p|576p|480p|UHD|BluRay|BDRip|WEB[- .]?DL|WEBRip|HDTV|DVDRip|REMUX|x264|x265|H\.?264|H\.?265|HEVC|AV1|AAC|DTS(?:-HD)?|TrueHD|Atmos|DDP?5(?:\.1)?|HDR10?|DoVi|10bit|8bit|Proper|Repack)(?:$|[. _-])`)
	spacePattern          = regexp.MustCompile(`\s+`)
)

var reservedStructureSegments = map[string]struct{}{
	"download": {}, "downloads": {}, "complete": {}, "completed": {}, "incoming": {},
	"temp": {}, "tmp": {}, "media": {}, "library": {}, "libraries": {}, "movie": {},
	"movies": {}, "film": {}, "films": {}, "tv": {}, "tvseries": {}, "series": {},
	"show": {}, "shows": {}, "video": {}, "videos": {}, "下载": {}, "下载完成": {},
	"未整理": {}, "待整理": {}, "临时": {}, "影视": {}, "影视库": {}, "媒体库": {},
	"片库": {}, "视频": {}, "电影": {}, "剧集": {}, "电视剧": {},
}

type ParsedMedia struct {
	MediaType   string
	Title       string
	SeriesTitle string
	Season      *int
	Episode     *int
}

func ParseMedia(name, providerPath string) ParsedMedia {
	segments := splitMediaPath(providerPath)
	fileName := name
	if fileName == "" && len(segments) > 0 {
		fileName = segments[len(segments)-1]
	}
	parents := segments
	if len(parents) > 0 {
		parents = parents[:len(parents)-1]
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	episodeTitle, season, episode := parseEpisodeStem(stem)
	seasonIndex, folderSeason := findSeasonFolder(parents)
	if season == nil {
		season = folderSeason
	}

	if episode != nil {
		seriesTitle := ""
		if seasonIndex >= 1 {
			seriesTitle = cleanWorkTitle(parents[seasonIndex-1])
		}
		if seriesTitle == "" {
			seriesTitle = cleanWorkTitle(episodeTitle)
		}
		if seriesTitle == "" && len(parents) > 0 {
			seriesTitle = cleanWorkTitle(parents[len(parents)-1])
		}
		if seriesTitle == "" {
			seriesTitle = episodeTitle
		}
		if seriesTitle == "" {
			seriesTitle = cleanMediaTitle(stem)
		}
		return ParsedMedia{MediaType: "tv", Title: seriesTitle, SeriesTitle: seriesTitle, Season: season, Episode: episode}
	}

	if seasonIndex >= 1 {
		seriesTitle := cleanWorkTitle(parents[seasonIndex-1])
		if seriesTitle != "" {
			return ParsedMedia{MediaType: "tv", Title: seriesTitle, SeriesTitle: seriesTitle, Season: season}
		}
	}

	title := titleFromYearFolder(parents)
	if title == "" {
		title = cleanMediaTitle(stem)
	}
	if title == "" {
		title = strings.TrimSpace(stem)
	}
	return ParsedMedia{MediaType: "movie", Title: title}
}

func WorkKey(parsed ParsedMedia, identity string) string {
	kind := "movie"
	keyMaterial := strings.TrimSpace(identity)
	if parsed.MediaType == "tv" {
		kind = "series"
		keyMaterial = normalizeTitleKey(parsed.SeriesTitle)
	}
	if keyMaterial == "" {
		kind = "file"
		keyMaterial = strings.TrimSpace(parsed.Title)
	}
	digest := sha256.Sum256([]byte(kind + "\x00" + keyMaterial))
	return kind + ":" + hex.EncodeToString(digest[:16])
}

func parseEpisodeStem(stem string) (string, *int, *int) {
	if match := sxxexxPattern.FindStringSubmatch(stem); len(match) == 3 {
		season, episode := atoiPointer(match[1]), atoiPointer(match[2])
		return cleanMediaTitle(sxxexxPattern.ReplaceAllString(stem, " ")), season, episode
	}
	if match := oneXEpisodePattern.FindStringSubmatch(stem); len(match) == 3 {
		season, episode := atoiPointer(match[1]), atoiPointer(match[2])
		return cleanMediaTitle(oneXEpisodePattern.ReplaceAllString(stem, " ")), season, episode
	}
	if match := chineseEpisodePattern.FindStringSubmatch(stem); len(match) == 2 {
		if number, ok := parseNumberText(match[1]); ok {
			return cleanMediaTitle(chineseEpisodePattern.ReplaceAllString(stem, " ")), nil, &number
		}
	}
	if match := episodeOnlyPattern.FindStringSubmatch(stem); len(match) == 2 {
		episode := atoiPointer(match[1])
		return cleanMediaTitle(episodeOnlyPattern.ReplaceAllString(stem, " ")), nil, episode
	}
	return cleanMediaTitle(stem), nil, nil
}

func findSeasonFolder(segments []string) (int, *int) {
	for index := len(segments) - 1; index >= 0; index-- {
		if match := seasonFolderPattern.FindStringSubmatch(strings.TrimSpace(segments[index])); len(match) == 2 {
			return index, atoiPointer(match[1])
		}
		if match := chineseSeasonPattern.FindStringSubmatch(strings.TrimSpace(segments[index])); len(match) == 2 {
			if number, ok := parseNumberText(match[1]); ok {
				return index, &number
			}
		}
	}
	return -1, nil
}

func titleFromYearFolder(segments []string) string {
	for index := len(segments) - 1; index >= 0; index-- {
		if yearPattern.MatchString(segments[index]) {
			if title := cleanWorkTitle(segments[index]); title != "" {
				return title
			}
		}
	}
	return ""
}

func cleanMediaTitle(value string) string {
	value = bracketNoisePattern.ReplaceAllString(value, " ")
	value = yearPattern.ReplaceAllString(value, " ")
	for technicalTokenPattern.MatchString(value) {
		value = technicalTokenPattern.ReplaceAllString(value, " ")
	}
	value = strings.NewReplacer(".", " ", "_", " ", "—", " ", "-", " ").Replace(value)
	return strings.TrimSpace(spacePattern.ReplaceAllString(value, " "))
}

func cleanWorkTitle(value string) string {
	title := cleanMediaTitle(value)
	if title == "" {
		return ""
	}
	if match := seasonFolderPattern.FindStringSubmatch(strings.TrimSpace(value)); len(match) == 2 {
		return ""
	}
	if match := chineseSeasonPattern.FindStringSubmatch(strings.TrimSpace(value)); len(match) == 2 {
		return ""
	}
	if _, reserved := reservedStructureSegments[normalizeStructureKey(title)]; reserved {
		return ""
	}
	return title
}

func normalizeTitleKey(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(cleanMediaTitle(value)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func normalizeStructureKey(value string) string {
	return normalizeTitleKey(value)
}

func splitMediaPath(value string) []string {
	parts := strings.Split(strings.ReplaceAll(value, "\\", "/"), "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			segments = append(segments, trimmed)
		}
	}
	return segments
}

func atoiPointer(value string) *int {
	number, _ := strconv.Atoi(value)
	return &number
}

func parseNumberText(value string) (int, bool) {
	if number, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return number, true
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 1 {
		if number, ok := digits[runes[0]]; ok {
			return number, true
		}
		if runes[0] == '十' {
			return 10, true
		}
	}
	for index, r := range runes {
		if r != '十' {
			continue
		}
		tens := 1
		if index > 0 {
			var ok bool
			tens, ok = digits[runes[index-1]]
			if !ok {
				return 0, false
			}
		}
		ones := 0
		if index+1 < len(runes) {
			var ok bool
			ones, ok = digits[runes[index+1]]
			if !ok {
				return 0, false
			}
		}
		return tens*10 + ones, true
	}
	return 0, false
}
