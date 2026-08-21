package services

import (
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

var (
	downloadEpisodeToken  = regexp.MustCompile(`(?i)\bS\s*0*\d{1,2}\s*E\s*0*\d{1,3}\b|\b0*\d{1,2}x0*\d{1,3}\b|\b(?:EP?|Episode)\s*0*\d{1,3}\b`)
	downloadBracketNoise  = regexp.MustCompile(`\[[^\]]+\]|\([^)]*\)|【[^】]+】`)
	downloadTechToken     = regexp.MustCompile(`(?i)\b(?:4320p|2160p|1080p|720p|576p|480p|8K|4K|UHD|BluRay|BDRip|WEB[- .]?DL|WEBRip|HDTV|DVDRip|REMUX|x264|x265|H\.?264|H\.?265|HEVC|AV1|AAC|DTS(?:-HD)?|TrueHD|Atmos|DDP?5(?:\.1)?|HDR10?|DoVi|DV|10bit|8bit|Proper|Repack)\b`)
	downloadSourceToken   = regexp.MustCompile(`(?i)\b(?:AMZN|Amazon|NF|Netflix|DSNP|Disney\+?|HMAX|HBO|Hulu|ATVP|AppleTV|iTunes|BiliBili|Baha|Crunchyroll|Viu|U-?NEXT|ABEMA|TVING|PrimeVideo|Peacock|Paramount\+?)\b`)
	downloadReleaseGroup  = regexp.MustCompile(`(?i)\b(?:GrassTV|NTb|FLUX|PTerWEB|CMCT|CHD|FGT|YIFY|YTS|MeGusta|VARYG|LoliHouse|ANi|Lilith|U3|CatWEB|MTeam|MWeb|Hares|SweetSub|MagicStar|Skymoon|XiaYong|Nekomoe|DBD-Raws|GM-Team|NC-Raws)\b`)
	downloadSubtitleToken = regexp.MustCompile(`(?i)\b(?:CHS|CHT|GB|BIG5|SUBS?|MULTI[- .]?SUB)\b|简繁|繁简|简中|繁中|简体|繁体|中文字幕|中字|中英双字|中英字幕|双语字幕|内封字幕|外挂字幕|字幕组|字幕`)
	downloadTrailingGroup = regexp.MustCompile(`(?i)\s+-\s+[\da-z][\da-z-]{1,30}$`)
	downloadSuffixPart    = regexp.MustCompile(`[\p{L}\p{N}]+`)
	downloadChineseTitle  = regexp.MustCompile(`[\p{Han}][\p{Han}\s·・、，：:《》“”"'—-]*`)
	downloadLatinTitle    = regexp.MustCompile(`[A-Za-z][A-Za-z0-9\s'’:&.,!?+\-]*`)
)

type downloadRecognitionCandidate struct {
	Title     string
	MediaType string
	Year      *int
}

type recognitionSourceFile struct {
	RelativePath string
	Size         int64
}

// downloadRecognitionCandidates is the provider-neutral recognition entry.
// It considers the primary filename, meaningful parent folders, and the
// provider package name. Every Profile preprocessor is applied before the
// built-in parser and before title/year extraction.
func downloadRecognitionCandidates(manifest downloadpkg.Manifest, rules []RecognitionRule) []downloadRecognitionCandidate {
	files := make([]recognitionSourceFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, recognitionSourceFile{RelativePath: file.RelativePath, Size: file.Size})
	}
	return recognitionCandidates(manifest.Name, files, rules)
}

// recognitionCandidates is shared by downloader manifests and media-library
// recognition units. It accepts provider-relative names and sizes only.
func recognitionCandidates(packageName string, files []recognitionSourceFile, rules []RecognitionRule) []downloadRecognitionCandidate {
	return recognitionCandidatesFromSources(recognitionSources(packageName, files), rules)
}

func recognitionCandidatesFromSources(sources []string, rules []RecognitionRule) []downloadRecognitionCandidate {
	candidates := make([]downloadRecognitionCandidate, 0, len(sources)*2)
	seen := map[string]struct{}{}
	for _, source := range sources {
		preferred := make([]downloadRecognitionCandidate, 0, 2)
		fallback := make([]downloadRecognitionCandidate, 0, 2)
		for _, assumedType := range []string{"movie", "tv"} {
			processed := applyRecognitionRules(source, assumedType, rules)
			ruleApplied := processed != source
			mediaType, _, _, _ := medialibrary.ParseFilename(filepath.Base(processed), "/"+strings.TrimLeft(strings.ReplaceAll(processed, "\\", "/"), "/"))
			if mediaType != assumedType {
				continue
			}
			var year *int
			if values := downloadYearPattern.FindStringSubmatch(processed); len(values) == 2 {
				if parsed, err := strconv.Atoi(values[1]); err == nil {
					year = &parsed
					processed = downloadYearPattern.ReplaceAllString(processed, " ")
				}
			}
			for _, title := range downloadSearchTitles(processed) {
				candidate := downloadRecognitionCandidate{Title: title, MediaType: assumedType, Year: cloneInt(year)}
				if ruleApplied {
					preferred = append(preferred, candidate)
				} else {
					fallback = append(fallback, candidate)
				}
			}
		}
		for _, candidate := range append(preferred, fallback...) {
			title := candidate.Title
			year := candidate.Year
			assumedType := candidate.MediaType
			key := assumedType + "\x00" + strings.ToLower(title) + "\x00"
			if year != nil {
				key += strconv.Itoa(*year)
			}
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func downloadRecognitionSources(manifest downloadpkg.Manifest) []string {
	files := make([]recognitionSourceFile, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, recognitionSourceFile{RelativePath: file.RelativePath, Size: file.Size})
	}
	return recognitionSources(manifest.Name, files)
}

func recognitionSources(packageName string, files []recognitionSourceFile) []string {
	primaryPath := ""
	primarySize := int64(-1)
	for _, file := range files {
		if isVideoFile(file.RelativePath) && file.Size > primarySize {
			primaryPath, primarySize = normalizedManifestPath(file.RelativePath), file.Size
		}
	}
	sources := make([]string, 0, 6)
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		stem := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(value, "\\", "/")), filepath.Ext(value))
		if value == "" || !containsRecognitionLetter(stem) || isRecognitionStructureName(value) {
			return
		}
		key := strings.ToLower(value)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		sources = append(sources, value)
	}
	if primaryPath != "" {
		add(pathpkg.Base(primaryPath))
		parent := pathpkg.Dir(primaryPath)
		for parent != "." && parent != "/" {
			add(pathpkg.Base(parent))
			next := pathpkg.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
	}
	add(packageName)
	return sources
}

func containsRecognitionLetter(value string) bool {
	for _, character := range value {
		if unicode.IsLetter(character) {
			return true
		}
	}
	return false
}

func isRecognitionStructureName(value string) bool {
	stem := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(value, "\\", "/")), filepath.Ext(value))
	switch strings.ToLower(strings.TrimSpace(stem)) {
	case "bdmv", "stream", "certificate", "video_ts", "audio_ts", "disc", "disk", "cd1", "cd2":
		return true
	default:
		return false
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// downloadSearchTitles is a conservative Server-side port of the Player raw
// scraper's title cleanup. It removes release metadata while preserving
// Chinese/Latin work titles and returns at most three bounded candidates.
func downloadSearchTitles(value string) []string {
	cleaned := strings.TrimSuffix(filepath.Base(strings.ReplaceAll(value, "\\", "/")), filepath.Ext(value))
	cleaned = downloadEpisodeToken.ReplaceAllString(cleaned, " ")
	cleaned = downloadBracketNoise.ReplaceAllString(cleaned, " ")
	cleaned = downloadTechToken.ReplaceAllString(cleaned, " ")
	cleaned = downloadSourceToken.ReplaceAllString(cleaned, " ")
	cleaned = downloadReleaseGroup.ReplaceAllString(cleaned, " ")
	cleaned = downloadSubtitleToken.ReplaceAllString(cleaned, " ")
	cleaned = strings.NewReplacer(".", " ", "_", " ", "—", " ").Replace(cleaned)
	cleaned = downloadTrailingGroup.ReplaceAllString(cleaned, " ")
	cleaned = strings.Join(strings.Fields(strings.Trim(cleaned, " -:：,，.·・《》\"'")), " ")
	baseCleaned := cleaned
	cleaned = stripDownloadReleaseSuffix(cleaned)

	candidates := make([]string, 0, 3)
	add := func(candidate string) {
		candidate = strings.Join(strings.Fields(strings.Trim(candidate, " -:：,，.·・《》\"'")), " ")
		if len([]rune(candidate)) < 2 || len([]rune(candidate)) > 256 {
			return
		}
		key := strings.ToLower(candidate)
		for _, existing := range candidates {
			if strings.ToLower(existing) == key {
				return
			}
		}
		if len(candidates) < 3 {
			candidates = append(candidates, candidate)
		}
	}
	if match := downloadChineseTitle.FindString(cleaned); match != "" {
		add(match)
	}
	if match := downloadLatinTitle.FindString(cleaned); match != "" {
		add(match)
	}
	add(cleaned)
	add(baseCleaned)
	return candidates
}

func stripDownloadReleaseSuffix(value string) string {
	parts := downloadSuffixPart.FindAllStringIndex(value, -1)
	if len(parts) < 2 {
		return value
	}
	index := len(parts)
	noiseCount := 0
	strongNoise := false
	for index > 0 {
		part := parts[index-1]
		token := strings.ToUpper(value[part[0]:part[1]])
		isDigit := len(token) == 1 && token[0] >= '0' && token[0] <= '9'
		if token != "CC" && token != "MA" && token != "SONYHD" && !isDigit {
			break
		}
		if token == "CC" || token == "SONYHD" {
			strongNoise = true
		}
		noiseCount++
		index--
	}
	if index == 0 || index == len(parts) || noiseCount < 2 || !strongNoise {
		return value
	}
	return strings.TrimRight(value[:parts[index][0]], " \t-:：,，.·・《》\"'")
}
