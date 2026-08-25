package mediarecognition

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	videoExtensionPattern           = regexp.MustCompile(`(?i)\.(mkv|mp4|m4v|avi|mov|wmv|ts|m2ts|mts|webm|flv|iso|vob)$`)
	episodePattern                  = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])S\s*0*([0-9]{1,2})\s*E\s*0*([0-9]{1,5})(?:[^[:alnum:]]|$)|(?:^|[^[:alnum:]])0*([0-9]{1,2})x0*([0-9]{1,5})(?:[^[:alnum:]]|$)`)
	seasonPattern                   = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])(?:season|s)\s*0*([0-9]{1,2})(?:[^[:alnum:]]|$)`)
	episodeRangePattern             = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])(?:ep?|episodes?)?\s*0*([0-9]{1,5})\s*[-~～—]\s*0*([0-9]{1,5})(?:\s*(?:tv)?(?:全集|全))?(?:[^[:alnum:]]|$)`)
	bracketRangePattern             = regexp.MustCompile(`(?i)(?:\[|【)\s*(?:ep?|episodes?)?\s*0*([0-9]{1,5})\s*[-~～—]\s*0*([0-9]{1,5})[^\]】]{0,32}(?:\]|】)`)
	completeCountPattern            = regexp.MustCompile(`(?i)(?:\[|【)\s*0*([0-9]{1,5})\s*(?:全集|全)\s*(?:\]|】)`)
	bracketEpisodePattern           = regexp.MustCompile(`(?:\[|【)\s*0*([0-9]{1,5})\s*(?:\]|】)`)
	explicitTrailingEpisodePattern  = regexp.MustCompile(`(?i)-\s*ep?\s*0*([0-9]{1,5})(?:\s*(?:end|fin))?(?:\s*(?:\[|【)|$)`)
	bracketedTrailingEpisodePattern = regexp.MustCompile(`(?i)-\s*0*([0-9]{1,5})(?:\s*(?:end|fin))?\s*(\[[^\]]{1,128}\]|【[^】]{1,128}】)`)
	chineseEpisodePattern           = regexp.MustCompile(`第\s*([0-9零〇一二两兩三四五六七八九十百千]{1,12})\s*[集话話](?:$|[^\p{L}\p{N}])`)
	chineseSeasonPattern            = regexp.MustCompile(`第\s*([0-9零〇一二两兩三四五六七八九十百千]{1,12})\s*季(?:$|[^\p{L}\p{N}])`)
	numericEpisodePattern           = regexp.MustCompile(`^0*([0-9]{1,5})$`)
	yearTokenPattern                = regexp.MustCompile(`\b((?:18|19|20)[0-9]{2})\b`)
	techTokenPattern                = regexp.MustCompile(`(?i)\b(?:4320p|2160p|1080p|720p|576p|480p|8k|4k|uhd|hq|[0-9]{2,3}\s*fps|bluray|blu-ray|bdrip|bdremux|remux|web[- .]?dl|webrip|hdtv|dvdrip|x264|x265|h\.?264|h\.?265|hevc|av1|aac|dts(?:-hd)?|truehd|atmos|ddp?|hdr10\+?|hdr|dovi|dolby[ .]?vision|10bit|8bit|proper|repack|uncut|extended|director'?s[ .]?cut|amzn|netflix|nf|dsnp|hmax|atvp|itunes|bilibili|mkv|mp4|pgs|srt|ass|ssa|jpn?|eng|zh(?:[- .]?(?:cn|tw|hk))?)\b`)
	spacedTechPattern               = regexp.MustCompile(`(?i)\b(?:[hx]\s*26[45]|ddp?\s*[0-9](?:\s*[0-9])?|eac3|ac3|lpcm|flac|dvd\s*480p|hdtv\s*rip|bd\s*rip|web\s*rip|[0-9]{3,4}\s*x\s*[0-9]{3,4})\b`)
	conditionalNoisePattern         = regexp.MustCompile(`(?i)\b(?:complete|edr|iq|[0-9]+\s*audio|iso\s*pack|ultra\s+resolution|remastered|version)\b|全集`)
	languageTrackOnlyPattern        = regexp.MustCompile(`(?i)^(?:[国國粤粵英日韩韓台中普话話語语一二三四五六七八九十0-9双雙多]+(?:音轨|音軌)?|(?:mandarin|cantonese|japanese|english|chinese)(?:dub|audio)?)$`)
	subtitleTokenPattern            = regexp.MustCompile(`(?i)\b(?:chs|cht|gb|big5|subs?|multi[- .]?sub)\b|简繁|繁简|简中|繁中|简体|繁体|中文字幕|中字|中英双字|中英字幕|双语字幕|内封字幕|外挂字幕|字幕组|字幕`)
	trailingGroupPattern            = regexp.MustCompile(`^(.*?)(\s*-\s*)([[:alnum:]][[:alnum:]-]{1,31})\s*$`)
	trailingBracketGroup            = regexp.MustCompile(`^(.*?)\s*[\[【]([[:alnum:]][[:alnum:]-]{1,31})[\]】]\s*$`)
	tmdbHintPattern                 = regexp.MustCompile(`(?i)(?:\[|\{)(?:tmdbid\s*=\s*|tmdb-)([0-9]{1,12})(?:\]|\})`)
	hanTitlePattern                 = regexp.MustCompile(`[\p{Han}][\p{Han}\p{N}\s·・、，：:《》“”'’—-]*`)
	latinTitlePattern               = regexp.MustCompile(`[\p{Latin}\p{N}][\p{Latin}\p{N}\s'’:&,+!?-]*[\p{Latin}\p{N}]`)
	unsafeDrivePattern              = regexp.MustCompile(`(?i)^[a-z]:[\\/]`)
	discStackPattern                = regexp.MustCompile(`^(?:disc|disk|cd)[ ._-]*[0-9]+$`)
	structureNamePattern            = regexp.MustCompile(`^(?:season|s|disc|disk|cd)[ ._-]*[0-9]+$`)
	emptyBracketPattern             = regexp.MustCompile(`\(\s*\)|\[\s*\]|\{\s*\}`)
	leadingBracketPattern           = regexp.MustCompile(`^(?:\[([^\]\r\n]{1,64})\]|【([^】\r\n]{1,64})】)(.*)$`)
	bracketSegmentPattern           = regexp.MustCompile(`(?:\[([^\]\r\n]{1,128})\]|【([^】\r\n]{1,128})】)`)
	checksumSuffixPattern           = regexp.MustCompile(`(?i)(?:\s*[\[(]?[0-9a-f]{8}[\])]?)\s*$`)
	aliasSeparatorPattern           = regexp.MustCompile(`\s*(?:/|／|\|)\s*`)
)

type parsedName struct {
	source          string
	raw             string
	withoutGroup    string
	withoutSpecs    string
	withoutEpisodes string
	withoutYear     string
	canonical       string
	year            *int
	specifications  []string
	releaseGroup    string
	groupStrong     bool
	season          *int
	episode         *int
	episodeMin      *int
	episodeMax      *int
	directHint      *IdentityHint
}

type namedSource struct {
	value  string
	source string
}

const maxRecognitionYear = 2200

// Parse converts provider-neutral, bounded facts into structured recognition
// facts. It performs no network or database I/O and is deterministic for a
// fixed input.
func Parse(input InputFacts) (ParsedFacts, error) {
	// Recognition output must not change merely because the wall clock crossed
	// a year boundary. TMDB and the public API already use 2200 as the bounded
	// upper year, so use the same stable horizon here.
	return parseAt(input, time.Date(2198, 1, 1, 0, 0, 0, 0, time.UTC))
}

func parseAt(input InputFacts, now time.Time) (ParsedFacts, error) {
	result := ParsedFacts{EngineVersion: EngineVersion, Titles: []TitleFact{}, Queries: []QueryVariant{}, Diagnostics: []Diagnostic{}}
	if err := validateInput(input, now); err != nil {
		return result, err
	}

	result.Structure, result.Episodes, result.TypeEvidence = analyzeStructure(input)
	result.Year = cloneDomainInt(input.YearHint)
	result.Season = cloneDomainInt(input.SeasonHint)
	if result.Season == nil && result.Episodes.SeasonMin != nil && equalDomainInt(result.Episodes.SeasonMin, result.Episodes.SeasonMax) {
		result.Season = cloneDomainInt(result.Episodes.SeasonMin)
	}

	sources := collectNamedSources(input)
	parsed := make([]parsedName, 0, len(sources))
	seasonYearFromName := false
	for _, source := range sources {
		item := analyzeName(source, now)
		if item.directHint != nil && !validIdentityHint(item.directHint) {
			return ParsedFacts{}, fmt.Errorf("%w: conflicting explicit identity hints", ErrUnsafeRecognitionInput)
		}
		if !meaningfulTitle(item.canonical) {
			continue
		}
		parsed = append(parsed, item)
		if input.YearHint == nil && item.season != nil && item.year != nil {
			seasonYearFromName = true
		}
	}
	if len(parsed) == 0 {
		addDiagnostic(&result.Diagnostics, "title_not_found", "warning", "no bounded source name produced a usable title")
		result.DirectHint = cloneIdentityHint(input.DirectHint)
		return result, nil
	}

	result.CanonicalTitle = parsed[0].canonical
	for _, item := range parsed {
		if result.Year == nil && item.year != nil {
			result.Year = cloneDomainInt(item.year)
		}
		if result.Season == nil && item.season != nil {
			result.Season = cloneDomainInt(item.season)
		}
		if item.episodeMin != nil && item.episodeMax != nil {
			updateMinMax(&result.Episodes.EpisodeMin, &result.Episodes.EpisodeMax, *item.episodeMin)
			updateMinMax(&result.Episodes.EpisodeMin, &result.Episodes.EpisodeMax, *item.episodeMax)
			if count := *item.episodeMax - *item.episodeMin + 1; count > result.Episodes.Count {
				result.Episodes.Count = count
			}
		}
		result.Specifications = appendUniqueBounded(result.Specifications, item.specifications, 32)
		if result.ReleaseGroup == "" && item.groupStrong && item.releaseGroup != "" {
			result.ReleaseGroup = item.releaseGroup
		}
		if item.directHint != nil {
			if result.DirectHint != nil && !sameIdentityHint(result.DirectHint, item.directHint) {
				return ParsedFacts{}, fmt.Errorf("%w: conflicting explicit identity hints", ErrUnsafeRecognitionInput)
			}
			result.DirectHint = cloneIdentityHint(item.directHint)
		}
		addTitleFact(&result.Titles, item.canonical, item.source, "canonical")
	}
	if hasNameEpisodeEvidence(parsed) {
		result.TypeEvidence = append(result.TypeEvidence, Evidence{Code: "release_name_episode", Kind: "structure", Supports: MediaTypeTV, Strength: .94, Summary: "release title contains bounded season or episode structure"})
	}
	if seasonYearFromName && result.Year != nil {
		result.SeasonYear = cloneDomainInt(result.Year)
		result.Year = nil
	}
	result.SuggestedType, result.TypeConfidence = decideMediaType(input.MediaTypeHint, result.Structure, result.TypeEvidence)
	if input.DirectHint != nil {
		if result.DirectHint != nil && !sameIdentityHint(result.DirectHint, input.DirectHint) {
			return ParsedFacts{}, fmt.Errorf("%w: conflicting supplied identity hint", ErrUnsafeRecognitionInput)
		}
		result.DirectHint = cloneIdentityHint(input.DirectHint)
	}
	result.Queries = buildQueryVariants(parsed, result.Year, result.SeasonYear, result.SuggestedType)
	addDiagnostic(&result.Diagnostics, "parser_complete", "info", fmt.Sprintf("parsed %d safe name sources into %d query variants", len(parsed), len(result.Queries)))
	if result.ReleaseGroup != "" {
		addDiagnostic(&result.Diagnostics, "release_group_observed", "info", "a bounded trailing release-group candidate was separated from the title")
	}
	if len(result.Specifications) > 0 {
		addDiagnostic(&result.Diagnostics, "specifications_removed", "info", fmt.Sprintf("separated %d resource specification tokens", len(result.Specifications)))
	}
	return result, nil
}

func validateInput(input InputFacts, now time.Time) error {
	if utf8.RuneCountInString(input.PackageName) > MaxPackageRunes || len(input.Files) > MaxInputFiles || len(input.PreparedNames) > 32 {
		return ErrRecognitionInputBounds
	}
	if input.PackageName != "" && unsafeTitleLocator(input.PackageName) {
		return ErrUnsafeRecognitionInput
	}
	if !validMediaType(input.MediaTypeHint) || input.SourceKind != "" && input.SourceKind != SourceUnknown && input.SourceKind != SourceDownload && input.SourceKind != SourceLibraryScan {
		return ErrUnsafeRecognitionInput
	}
	if !validYear(input.YearHint, now) || !validSmallNumber(input.SeasonHint) || !validEpisodeNumber(input.EpisodeHint) {
		return ErrUnsafeRecognitionInput
	}
	if input.DirectHint != nil && !validIdentityHint(input.DirectHint) {
		return ErrUnsafeRecognitionInput
	}
	for _, prepared := range input.PreparedNames {
		if utf8.RuneCountInString(prepared.Value) > MaxPackageRunes || unsafeTitleLocator(prepared.Value) {
			return ErrUnsafeRecognitionInput
		}
		if len(prepared.AppliedRules) > 64 {
			return ErrRecognitionInputBounds
		}
	}
	for _, file := range input.Files {
		if file.Size < 0 || utf8.RuneCountInString(file.RelativePath) > MaxRelativeRunes || !safeRelativePath(file.RelativePath) {
			return ErrUnsafeRecognitionInput
		}
	}
	return nil
}

func safeRelativePath(value string) bool {
	value = norm.NFC.String(strings.TrimSpace(value))
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") || unsafeDrivePattern.MatchString(value) || containsUnsafeLocator(value) {
		return false
	}
	for _, part := range strings.Split(strings.ReplaceAll(value, "\\", "/"), "/") {
		if part == "" || part == "." || part == ".." || utf8.RuneCountInString(part) > MaxPackageRunes {
			return false
		}
		for _, r := range part {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}

func containsUnsafeLocator(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "://") || strings.Contains(lower, "authorization=") || strings.Contains(lower, "signature=") ||
		strings.Contains(lower, "token=") || strings.Contains(lower, "api_key=") || strings.Contains(lower, "apikey=") ||
		strings.Contains(lower, "cookie=") || strings.Contains(lower, "x-amz-")
}

func unsafeTitleLocator(value string) bool {
	value = strings.TrimSpace(value)
	return containsUnsafeLocator(value) || unsafeDrivePattern.MatchString(value) || strings.Contains(value, "\\") || strings.HasPrefix(value, "/")
}

func validMediaType(value MediaType) bool {
	return value == MediaTypeUnknown || value == MediaTypeMovie || value == MediaTypeTV
}

func validYear(value *int, _ time.Time) bool {
	return value == nil || *value >= 1888 && *value <= maxRecognitionYear
}

func validSmallNumber(value *int) bool   { return value == nil || *value >= 0 && *value <= 200 }
func validEpisodeNumber(value *int) bool { return value == nil || *value >= 0 && *value <= 100000 }

func validIdentityHint(value *IdentityHint) bool {
	return value.Provider == "tmdb" && value.ID > 0 && validMediaType(value.MediaType)
}

func collectNamedSources(input InputFacts) []namedSource {
	items := make([]namedSource, 0, 16)
	seen := make(map[string]struct{})
	add := func(value, source string) {
		value = boundedText(value, MaxPackageRunes)
		key := comparisonKey(value)
		if key == "" {
			return
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		items = append(items, namedSource{value: value, source: source})
	}
	for _, prepared := range input.PreparedNames {
		add(prepared.Value, "profile:"+boundedCode(prepared.Source))
	}
	add(input.PackageName, "package")

	type sizedFile struct {
		path string
		size int64
	}
	files := make([]sizedFile, 0, len(input.Files))
	for _, file := range input.Files {
		if videoExtensionPattern.MatchString(file.RelativePath) {
			files = append(files, sizedFile{path: strings.ReplaceAll(file.RelativePath, "\\", "/"), size: file.Size})
		}
	}
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].size != files[j].size {
			return files[i].size > files[j].size
		}
		return files[i].path < files[j].path
	})
	if len(files) > 8 {
		files = files[:8]
	}
	for _, file := range files {
		add(path.Base(file.path), "filename")
		parent := path.Dir(file.path)
		for depth := 0; depth < 4 && parent != "." && parent != "/"; depth++ {
			base := path.Base(parent)
			if !structureDirectory(base) {
				add(base, "parent")
			}
			parent = path.Dir(parent)
		}
	}
	return items
}

func analyzeName(source namedSource, now time.Time) parsedName {
	raw := normalizeFilename(source.value)
	item := parsedName{source: source.source, raw: raw}
	withoutHint, hint := extractTMDBHint(raw)
	item.directHint = hint
	withoutHint = strings.TrimSpace(withoutHint)
	item.season, item.episode = firstSeasonEpisode(withoutHint)
	item.episodeMin, item.episodeMax = episodeRangeFromName(withoutHint)
	if item.episodeMin != nil {
		item.episode = cloneDomainInt(item.episodeMin)
	} else if item.episode != nil {
		item.episodeMin, item.episodeMax = cloneDomainInt(item.episode), cloneDomainInt(item.episode)
	}

	recognitionSurface := withoutHint
	if structuredTitle, leadingGroup, ok := structuredReleaseTitle(withoutHint); ok {
		recognitionSurface = structuredTitle
		item.releaseGroup = leadingGroup
		item.groupStrong = true
		if item.episode == nil && item.episodeMin == nil {
			if episode := bracketEpisodeFromName(withoutHint); episode != nil {
				item.episode = episode
				item.episodeMin, item.episodeMax = cloneDomainInt(episode), cloneDomainInt(episode)
			}
		}
	}

	withoutGroup, group, strong := separateTrailingGroup(recognitionSurface)
	if !strong {
		if bareTitle, bareGroup, ok := separateBareTrailingGroup(recognitionSurface); ok {
			withoutGroup, group, strong = bareTitle, bareGroup, true
		}
	}
	item.withoutGroup = cleanTitleSurface(withoutGroup)
	if item.releaseGroup == "" {
		item.releaseGroup, item.groupStrong = group, strong
	}
	if !strong {
		item.withoutGroup = cleanTitleSurface(recognitionSurface)
	}

	item.specifications = canonicalSpecifications(append(techTokenPattern.FindAllString(withoutHint, -1), spacedTechPattern.FindAllString(withoutHint, -1)...))
	withoutSpecs := techTokenPattern.ReplaceAllString(item.withoutGroup, " ")
	withoutSpecs = spacedTechPattern.ReplaceAllString(withoutSpecs, " ")
	if hasTechnicalToken(item.withoutGroup) {
		withoutSpecs = conditionalNoisePattern.ReplaceAllString(withoutSpecs, " ")
	}
	withoutSpecs = subtitleTokenPattern.ReplaceAllString(withoutSpecs, " ")
	withoutSpecs = stripTrailingReleaseNoise(withoutSpecs)
	item.withoutSpecs = cleanTitleSurface(withoutSpecs)

	item.withoutEpisodes = removeSeasonEpisodeTokens(item.withoutSpecs)
	if item.season == nil && item.episode == nil {
		item.season, item.episode = firstSeasonEpisode(item.withoutSpecs)
	}

	item.withoutYear, item.year = separatePlausibleYear(item.withoutEpisodes, now)
	item.canonical = cleanTitleSurface(item.withoutYear)
	if !meaningfulTitle(item.canonical) {
		item.canonical = cleanTitleSurface(item.withoutEpisodes)
	}
	return item
}

func normalizeFilename(value string) string {
	value = norm.NFC.String(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	base := value
	if videoExtensionPattern.MatchString(base) {
		base = base[:strings.LastIndex(base, ".")]
	}
	base = stripTrailingChecksum(base)
	return cleanTitleSurface(strings.NewReplacer(".", " ", "_", " ", "+", " ", "\u00a0", " ").Replace(base))
}

func stripTrailingChecksum(value string) string {
	match := checksumSuffixPattern.FindStringIndex(value)
	if match == nil || match[0] == 0 {
		return value
	}
	prefix := strings.TrimSpace(value[:match[0]])
	if !meaningfulTitle(prefix) {
		return value
	}
	return prefix
}

// structuredReleaseTitle recognizes the common release shape
// "[group][work title][episode][technical tags]" and
// "[group]work title[SxxExxxx][technical tags]". It only promotes a title
// when the remainder also contains release structure, which protects legal
// bracketed work titles such as [REC].
func structuredReleaseTitle(value string) (string, string, bool) {
	match := leadingBracketPattern.FindStringSubmatch(value)
	if len(match) != 4 {
		return "", "", false
	}
	group := firstNonEmptyDomain(match[1], match[2])
	remainder := strings.TrimSpace(match[3])
	if technicalBracketOnly(group) {
		return structuredReleaseTitle(remainder)
	}
	// Multiple leading release/language brackets may precede an unbracketed
	// work title. Prefer that explicit title surface over treating the second
	// bracket itself as the work name.
	if unwrapped := trimLeadingBracketSegments(remainder); unwrapped != remainder {
		if _, episode := firstSeasonEpisode(unwrapped); episode != nil {
			if candidate := titlePrefixBeforeEpisode(unwrapped); meaningfulTitle(candidate) {
				return candidate, cleanTitleSurface(group), true
			}
		}
	}
	_, episode := firstSeasonEpisode(remainder)
	segments := bracketSegmentPattern.FindAllStringSubmatch(remainder, -1)
	structured := episode != nil || techTokenPattern.MatchString(remainder) || subtitleTokenPattern.MatchString(remainder) || len(segments) >= 2
	if !structured {
		return "", "", false
	}
	if !strings.HasPrefix(remainder, "[") && !strings.HasPrefix(remainder, "【") {
		// A leading bracket alone may be the legal work title ([REC]). Only
		// treat it as a release group when an explicit episode marker or a
		// second bracketed field proves that another title follows it.
		if episode == nil && len(segments) == 0 {
			return "", "", false
		}
		prefix := remainder
		if index := strings.IndexAny(prefix, "[【"); index >= 0 {
			prefix = prefix[:index]
		}
		if candidate := cleanStructuredTitleSegment(prefix); candidate != "" {
			return candidate, cleanTitleSurface(group), true
		}
	}
	for _, segment := range segments {
		candidate := firstNonEmptyDomain(segment[1], segment[2])
		if candidate = cleanStructuredTitleSegment(candidate); candidate != "" {
			return candidate, cleanTitleSurface(group), true
		}
	}
	if !technicalBracketOnly(group) && !releaseGroupStyled(group) && !strings.Contains(group, "字幕组") && !strings.Contains(group, "字幕組") {
		if candidate := cleanStructuredTitleSegment(group); candidate != "" {
			return candidate, "", true
		}
	}
	return "", "", false
}

func trimLeadingBracketSegments(value string) string {
	trimmed := strings.TrimSpace(value)
	for {
		match := leadingBracketPattern.FindStringSubmatch(trimmed)
		if len(match) != 4 {
			return trimmed
		}
		next := strings.TrimSpace(match[3])
		if next == "" || next == trimmed {
			return trimmed
		}
		trimmed = next
	}
}

func technicalBracketOnly(value string) bool {
	cleaned := techTokenPattern.ReplaceAllString(value, " ")
	cleaned = spacedTechPattern.ReplaceAllString(cleaned, " ")
	cleaned = subtitleTokenPattern.ReplaceAllString(cleaned, " ")
	return strings.TrimSpace(cleanTitleSurface(cleaned)) == ""
}

func cleanStructuredTitleSegment(value string) string {
	if minimum, _ := episodeRangeFromName(value); minimum != nil || languageTrackOnlyPattern.MatchString(strings.TrimSpace(value)) {
		return ""
	}
	if season, episode := firstSeasonEpisode(value); season != nil || episode != nil {
		if prefix := titlePrefixBeforeEpisode(value); meaningfulTitle(prefix) {
			return cleanTitleSurface(prefix)
		}
		return ""
	}
	value = techTokenPattern.ReplaceAllString(value, " ")
	value = subtitleTokenPattern.ReplaceAllString(value, " ")
	value = stripTrailingChecksum(value)
	value, _ = separatePlausibleYear(value, time.Time{})
	value = cleanTitleSurface(value)
	if !meaningfulTitle(value) {
		return ""
	}
	return value
}

func titlePrefixBeforeEpisode(value string) string {
	start := len(value)
	for _, pattern := range []*regexp.Regexp{episodePattern, seasonPattern, chineseEpisodePattern, chineseSeasonPattern, explicitTrailingEpisodePattern, bracketedTrailingEpisodePattern} {
		if index := pattern.FindStringIndex(value); index != nil && index[0] < start {
			start = index[0]
		}
	}
	if start == len(value) {
		return ""
	}
	return cleanTitleSurface(value[:start])
}

func firstNonEmptyDomain(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func cleanTitleSurface(value string) string {
	value = norm.NFC.String(value)
	value = strings.NewReplacer("—", "-", "–", "-", "－", "-", "：", ":", "，", ",").Replace(value)
	value = emptyBracketPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(strings.Trim(value, " \t-:,.·・《》\"'")), " ")
}

// stripTrailingReleaseNoise handles edition/audio fragments that become
// isolated only after the main resource tokens have been removed. The marker
// requirement protects ordinary numeric titles such as 1917 and 3 Body
// Problem: bare digits are never removed unless a trailing CC/MA marker is
// present in the same suffix.
func stripTrailingReleaseNoise(value string) string {
	fields := strings.Fields(cleanTitleSurface(value))
	if len(fields) < 2 {
		return cleanTitleSurface(value)
	}
	index := len(fields)
	marker := false
	for index > 0 {
		token := strings.ToUpper(strings.Trim(fields[index-1], "-:,."))
		singleDigit := len(token) == 1 && token[0] >= '0' && token[0] <= '9'
		if token == "CC" || token == "MA" {
			marker = true
			index--
			continue
		}
		if singleDigit {
			index--
			continue
		}
		break
	}
	if !marker || index == len(fields) {
		return cleanTitleSurface(value)
	}
	cleaned := cleanTitleSurface(strings.Join(fields[:index], " "))
	if !meaningfulTitle(cleaned) {
		return cleanTitleSurface(value)
	}
	return cleaned
}

func separateTrailingGroup(value string) (string, string, bool) {
	if match := trailingBracketGroup.FindStringSubmatch(value); len(match) == 3 && !strings.Contains(strings.ToLower(match[2]), "tmdb") {
		strong := hasTechnicalToken(match[1]) || releaseGroupStyled(match[2])
		return match[1], match[2], strong
	}
	match := trailingGroupPattern.FindStringSubmatch(value)
	if len(match) != 4 {
		return value, "", false
	}
	left, group := match[1], match[3]
	// The permissive group pattern deliberately allows hyphenated release
	// groups, but its first possible delimiter can also land inside a known
	// technical token such as DTS-HD or WEB-DL. Move only the leading group
	// segments that complete a trailing technical token back to the release
	// title; genuine group names such as GROUP-ABC stay intact.
	for parts := strings.Split(group, "-"); len(parts) > 0; parts = strings.Split(group, "-") {
		candidate := left + "-" + parts[0]
		if !endsWithTechnicalToken(candidate) {
			break
		}
		left = candidate
		group = strings.Join(parts[1:], "-")
		if group == "" {
			// The apparent delimiter belonged entirely to a resource token
			// such as WEB-DL; there is no release group to remove.
			return left, "", false
		}
	}
	strong := hasTechnicalToken(left)
	if !strong && strings.Contains(match[2], " ") && releaseGroupStyled(group) {
		// A spaced delimiter plus group-like casing is useful as a query
		// variant, but is intentionally not destructive without a nearby
		// resource token. Legal titles remain the canonical form.
		return left, group, false
	}
	return left, group, strong
}

func separateBareTrailingGroup(value string) (string, string, bool) {
	if !hasTechnicalToken(value) {
		return value, "", false
	}
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return value, "", false
	}
	group := strings.Trim(fields[len(fields)-1], "[]【】-:,. ")
	if len(group) < 2 || len(group) > 31 || hasTechnicalToken(group) || subtitleTokenPattern.MatchString(group) || !releaseGroupStyled(group) {
		return value, "", false
	}
	title := strings.TrimSpace(strings.TrimSuffix(value, fields[len(fields)-1]))
	if !meaningfulTitle(title) {
		return value, "", false
	}
	return title, group, true
}

func endsWithTechnicalToken(value string) bool {
	for _, pattern := range []*regexp.Regexp{techTokenPattern, spacedTechPattern} {
		indices := pattern.FindAllStringIndex(value, -1)
		if len(indices) > 0 && indices[len(indices)-1][1] == len(value) {
			return true
		}
	}
	return false
}

func hasTechnicalToken(value string) bool {
	return techTokenPattern.MatchString(value) || spacedTechPattern.MatchString(value)
}

func releaseGroupStyled(value string) bool {
	hasLower, hasUpper, upperAfterLower, hasDigit := false, false, false, false
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
			if hasLower {
				upperAfterLower = true
			}
		}
	}
	return hasDigit || upperAfterLower || hasUpper && !hasLower
}

func canonicalSpecifications(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
		if value == "" {
			continue
		}
		result = appendUniqueBounded(result, []string{value}, 32)
	}
	return result
}

func separatePlausibleYear(value string, _ time.Time) (string, *int) {
	matches := yearTokenPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return cleanTitleSurface(value), nil
	}
	for index := len(matches) - 1; index >= 0; index-- {
		match := matches[index]
		year, err := strconv.Atoi(value[match[2]:match[3]])
		if err != nil || year < 1888 || year > maxRecognitionYear {
			continue
		}
		without := cleanTitleSurface(value[:match[2]] + " " + value[match[3]:])
		if !meaningfulTitle(without) {
			continue
		}
		return without, cloneDomainInt(&year)
	}
	return cleanTitleSurface(value), nil
}

func extractTMDBHint(value string) (string, *IdentityHint) {
	matches := tmdbHintPattern.FindAllStringSubmatch(value, -1)
	var hint *IdentityHint
	for _, match := range matches {
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		candidate := &IdentityHint{Provider: "tmdb", ID: id}
		if hint != nil && !sameIdentityHint(hint, candidate) {
			return tmdbHintPattern.ReplaceAllString(value, " "), &IdentityHint{Provider: "invalid", ID: -1}
		}
		hint = candidate
	}
	return tmdbHintPattern.ReplaceAllString(value, " "), hint
}

func firstSeasonEpisode(value string) (*int, *int) {
	match := episodePattern.FindStringSubmatch(value)
	if len(match) != 0 {
		seasonText, episodeText := match[1], match[2]
		if seasonText == "" {
			seasonText, episodeText = match[3], match[4]
		}
		season, _ := strconv.Atoi(seasonText)
		episode, _ := strconv.Atoi(episodeText)
		return cloneDomainInt(&season), cloneDomainInt(&episode)
	}
	var season *int
	if match := seasonPattern.FindStringSubmatch(value); len(match) == 2 {
		parsed, _ := strconv.Atoi(match[1])
		season = cloneDomainInt(&parsed)
	} else if match := chineseSeasonPattern.FindStringSubmatch(value); len(match) == 2 {
		season = parseBoundedOrdinal(match[1], 200)
	}
	var episode *int
	if match := chineseEpisodePattern.FindStringSubmatch(value); len(match) == 2 {
		episode = parseBoundedOrdinal(match[1], 100000)
	} else if match := explicitTrailingEpisodePattern.FindStringSubmatch(value); len(match) == 2 {
		parsed, _ := strconv.Atoi(match[1])
		if parsed < 1888 || parsed > maxRecognitionYear {
			episode = cloneDomainInt(&parsed)
		}
	} else if match := bracketedTrailingEpisodePattern.FindStringSubmatch(value); len(match) == 3 && trailingEpisodeBracketHasEvidence(match[2]) {
		parsed, _ := strconv.Atoi(match[1])
		if parsed < 1888 || parsed > maxRecognitionYear {
			episode = cloneDomainInt(&parsed)
			if season == nil {
				defaultSeason := 1
				season = &defaultSeason
			}
		}
	}
	return season, episode
}

func trailingEpisodeBracketHasEvidence(value string) bool {
	return techTokenPattern.MatchString(value) || spacedTechPattern.MatchString(value) || subtitleTokenPattern.MatchString(value) ||
		strings.ContainsAny(strings.ToLower(value), "语語粤粵国國英日韩韓音轨軌")
}

func episodeRangeFromName(value string) (*int, *int) {
	match := bracketRangePattern.FindStringSubmatch(value)
	if len(match) == 0 {
		match = episodeRangePattern.FindStringSubmatch(value)
	}
	if len(match) == 0 {
		if complete := completeCountPattern.FindStringSubmatch(value); len(complete) == 2 {
			maximum, err := strconv.Atoi(complete[1])
			if err == nil && maximum > 0 && maximum <= 100000 && (maximum < 1888 || maximum > maxRecognitionYear) {
				minimum := 1
				return cloneDomainInt(&minimum), cloneDomainInt(&maximum)
			}
		}
	}
	if len(match) != 3 {
		return nil, nil
	}
	minimum, minimumErr := strconv.Atoi(match[1])
	maximum, maximumErr := strconv.Atoi(match[2])
	if minimumErr != nil || maximumErr != nil || minimum < 0 || maximum < minimum || maximum > 100000 || minimum >= 1888 && maximum <= maxRecognitionYear {
		return nil, nil
	}
	return cloneDomainInt(&minimum), cloneDomainInt(&maximum)
}

func bracketEpisodeFromName(value string) *int {
	for _, match := range bracketEpisodePattern.FindAllStringSubmatch(value, -1) {
		parsed, err := strconv.Atoi(match[1])
		if err == nil && parsed >= 0 && parsed <= 100000 && (parsed < 1888 || parsed > maxRecognitionYear) {
			return cloneDomainInt(&parsed)
		}
	}
	return nil
}

func hasNameEpisodeEvidence(items []parsedName) bool {
	for _, item := range items {
		if item.season != nil || item.episode != nil || item.episodeMin != nil {
			return true
		}
	}
	return false
}

func parseBoundedOrdinal(value string, maximum int) *int {
	value = strings.NewReplacer("两", "二", "兩", "二").Replace(strings.TrimSpace(value))
	number, _, err := parseEpisodeNumber(value)
	if err != nil || number < 0 || number > maximum {
		return nil
	}
	return cloneDomainInt(&number)
}

func removeSeasonEpisodeTokens(value string) string {
	without := episodePattern.ReplaceAllString(value, " ")
	without = seasonPattern.ReplaceAllString(without, " ")
	without = bracketRangePattern.ReplaceAllString(without, " ")
	without = completeCountPattern.ReplaceAllString(without, " ")
	without = episodeRangePattern.ReplaceAllString(without, " ")
	without = explicitTrailingEpisodePattern.ReplaceAllString(without, " ")
	without = bracketedTrailingEpisodePattern.ReplaceAllString(without, " ")
	without = chineseEpisodePattern.ReplaceAllString(without, " ")
	without = chineseSeasonPattern.ReplaceAllString(without, " ")
	cleaned := cleanTitleSurface(without)
	// An ordinal expression can itself be a legitimate work title (for
	// example, a film named "第八集"). Only treat the expression as release
	// structure when a meaningful work-title surface remains.
	if meaningfulTitle(cleaned) {
		return cleaned
	}
	return cleanTitleSurface(value)
}

func analyzeStructure(input InputFacts) (StructureFacts, EpisodeFacts, []Evidence) {
	structure := StructureFacts{FileCount: len(input.Files)}
	episodes := make(map[string][2]int)
	seasons := make(map[int]struct{})
	for _, file := range input.Files {
		normalized := strings.ReplaceAll(file.RelativePath, "\\", "/")
		lower := strings.ToLower(normalized)
		segments := strings.Split(lower, "/")
		for _, segment := range segments[:len(segments)-1] {
			switch {
			case segment == "bdmv":
				structure.HasBDMV = true
			case segment == "video_ts":
				structure.HasVideoTS = true
			case discStackPattern.MatchString(segment):
				structure.HasDiscStack = true
			case segment == "extras" || segment == "extra" || segment == "trailers" || segment == "interviews" || segment == "samples":
				structure.HasExtras = true
			}
			if match := seasonPattern.FindStringSubmatch(segment); len(match) == 2 {
				season, _ := strconv.Atoi(match[1])
				seasons[season] = struct{}{}
				structure.HasSeasonFolder = true
			} else if match := chineseSeasonPattern.FindStringSubmatch(segment); len(match) == 2 {
				if season := parseBoundedOrdinal(match[1], 200); season != nil {
					seasons[*season] = struct{}{}
					structure.HasSeasonFolder = true
				}
			}
		}
		if !videoExtensionPattern.MatchString(normalized) {
			continue
		}
		structure.VideoCount++
		stem := videoExtensionPattern.ReplaceAllString(path.Base(normalized), "")
		season, episode := firstSeasonEpisode(stem)
		if episode == nil && !strings.Contains(lower, "/bdmv/") && !strings.Contains(lower, "/video_ts/") {
			if match := numericEpisodePattern.FindStringSubmatch(stem); len(match) == 2 {
				parsed, _ := strconv.Atoi(match[1])
				episode = &parsed
				if season == nil && len(seasons) == 1 {
					for value := range seasons {
						season = cloneDomainInt(&value)
					}
				}
			}
		}
		if episode != nil {
			seasonValue := 0
			if season != nil {
				seasonValue = *season
				seasons[seasonValue] = struct{}{}
			}
			episodes[fmt.Sprintf("%d:%d", seasonValue, *episode)] = [2]int{seasonValue, *episode}
		}
	}
	facts := episodeFactsFromSets(episodes, seasons)
	evidence := make([]Evidence, 0, 8)
	if input.MediaTypeHint != MediaTypeUnknown {
		evidence = append(evidence, Evidence{Code: "explicit_media_type", Kind: "type", Supports: input.MediaTypeHint, Strength: 1, Summary: "caller supplied a bounded media type context"})
	}
	if structure.HasSeasonFolder {
		evidence = append(evidence, Evidence{Code: "season_directory", Kind: "structure", Supports: MediaTypeTV, Strength: .92, Summary: "season directory structure supports television"})
	}
	if facts.Count >= 2 {
		strength := .82
		if facts.Count >= 8 {
			strength = .97
		}
		evidence = append(evidence, Evidence{Code: "episode_sequence", Kind: "structure", Supports: MediaTypeTV, Strength: strength, Summary: fmt.Sprintf("%d distinct episode markers support television", facts.Count)})
	} else if facts.Count == 1 {
		evidence = append(evidence, Evidence{Code: "explicit_episode_marker", Kind: "structure", Supports: MediaTypeTV, Strength: .90, Summary: "one explicit season or episode marker strongly supports television"})
	} else if structure.VideoCount >= 3 {
		evidence = append(evidence, Evidence{Code: "multi_video_collection", Kind: "structure", Supports: MediaTypeTV, Strength: .35, Summary: "multiple video files weakly support television"})
	}
	if structure.HasBDMV || structure.HasVideoTS {
		evidence = append(evidence, Evidence{Code: "disc_video_structure", Kind: "structure", Supports: MediaTypeMovie, Strength: .88, Summary: "BDMV or VIDEO_TS structure strongly supports a movie disc"})
	} else if structure.VideoCount == 1 && facts.Count == 0 {
		evidence = append(evidence, Evidence{Code: "single_video", Kind: "structure", Supports: MediaTypeMovie, Strength: .30, Summary: "one video file weakly supports a movie"})
	}
	return structure, facts, evidence
}

func episodeFactsFromSets(episodes map[string][2]int, seasons map[int]struct{}) EpisodeFacts {
	result := EpisodeFacts{Count: len(episodes)}
	for season := range seasons {
		updateMinMax(&result.SeasonMin, &result.SeasonMax, season)
	}
	for _, pair := range episodes {
		if pair[0] > 0 || len(seasons) > 0 {
			updateMinMax(&result.SeasonMin, &result.SeasonMax, pair[0])
		}
		updateMinMax(&result.EpisodeMin, &result.EpisodeMax, pair[1])
	}
	return result
}

func updateMinMax(minimum, maximum **int, value int) {
	if *minimum == nil || value < **minimum {
		*minimum = cloneDomainInt(&value)
	}
	if *maximum == nil || value > **maximum {
		*maximum = cloneDomainInt(&value)
	}
}

func decideMediaType(hint MediaType, structure StructureFacts, evidence []Evidence) (MediaType, float64) {
	if hint != MediaTypeUnknown {
		return hint, 1
	}
	tv, movie := 0.0, 0.0
	for _, item := range evidence {
		if item.Supports == MediaTypeTV {
			tv = maxFloat(tv, item.Strength)
		} else if item.Supports == MediaTypeMovie {
			movie = maxFloat(movie, item.Strength)
		}
	}
	if tv == movie {
		return MediaTypeUnknown, tv
	}
	if tv > movie {
		return MediaTypeTV, tv
	}
	return MediaTypeMovie, movie
}

func buildQueryVariants(names []parsedName, year, seasonYear *int, mediaType MediaType) []QueryVariant {
	result := make([]QueryVariant, 0, MaxQueryVariants)
	seen := make(map[string]struct{})
	add := func(title string, item parsedName, reason string, candidateYear *int) {
		title = cleanTitleSurface(title)
		if !meaningfulTitle(title) || len(result) >= MaxQueryVariants {
			return
		}
		key := comparisonKey(title) + "\x00" + string(mediaType)
		if candidateYear != nil {
			key += "\x00" + strconv.Itoa(*candidateYear)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, QueryVariant{Title: title, Year: cloneDomainInt(candidateYear), SeasonYear: cloneDomainInt(seasonYear), SuggestedType: mediaType, Source: item.source, Reason: reason, Order: len(result)})
	}
	// Give every bounded name source one canonical opportunity before adding
	// noisier fallback stages from the first source. The service has a strict
	// external-request budget, so source-fair ordering prevents filename
	// variants from crowding out a clean parent or package title.
	for _, item := range names {
		add(item.canonical, item, "canonical", year)
	}
	for _, item := range names {
		for _, split := range splitAliasTitles(item.canonical) {
			add(split.title, item, split.reason, year)
		}
	}
	for _, item := range names {
		if item.groupStrong && comparisonKey(item.withoutGroup) != comparisonKey(item.raw) {
			add(item.withoutGroup, item, "without_release_group", year)
		} else if item.releaseGroup != "" && !item.groupStrong {
			add(strings.TrimSuffix(item.raw, "-"+item.releaseGroup), item, "possible_release_group", year)
		}
	}
	for _, item := range names {
		add(item.withoutSpecs, item, "without_specifications", year)
	}
	for _, item := range names {
		add(item.withoutEpisodes, item, "without_season_episode", year)
	}
	for _, item := range names {
		if item.year != nil {
			candidateYear := item.year
			if seasonYear != nil {
				candidateYear = nil
			}
			add(item.withoutYear, item, "without_year", candidateYear)
		}
	}
	for _, item := range names {
		for _, split := range splitLanguageTitles(item.canonical) {
			add(split.title, item, split.reason, year)
		}
	}
	for _, item := range names {
		add(item.raw, item, "raw", year)
	}
	for _, item := range names {
		for _, token := range distinctiveLatinRecallTokens(item.canonical) {
			// A single distinctive token is retrieval-only evidence. Rank must
			// still compare the complete title so a broad token such as "Tiga"
			// cannot become an automatic identity by itself.
			add(token, item, "latin_token_fallback", year)
		}
	}
	return result
}

type splitTitle struct{ title, reason string }

func splitLanguageTitles(value string) []splitTitle {
	result := make([]splitTitle, 0, 2)
	if han := cleanTitleSurface(hanTitlePattern.FindString(value)); meaningfulTitle(han) && comparisonKey(han) != comparisonKey(value) {
		result = append(result, splitTitle{title: han, reason: "han_title"})
	}
	if latin := cleanTitleSurface(latinTitlePattern.FindString(value)); meaningfulTitle(latin) && comparisonKey(latin) != comparisonKey(value) {
		result = append(result, splitTitle{title: latin, reason: "latin_title"})
	}
	return result
}

func splitAliasTitles(value string) []splitTitle {
	parts := aliasSeparatorPattern.Split(value, -1)
	if len(parts) < 2 {
		return nil
	}
	result := make([]splitTitle, 0, len(parts))
	for _, part := range parts {
		part = cleanTitleSurface(part)
		if meaningfulTitle(part) && comparisonKey(part) != comparisonKey(value) {
			result = append(result, splitTitle{title: part, reason: "multilingual_alias"})
		}
	}
	return result
}

func distinctiveLatinRecallTokens(value string) []string {
	tokens := comparisonTokens(value, nil)
	fullKey := []rune(comparisonKeyWith(value, nil))
	if len(tokens) < 2 || len(fullKey) < 10 {
		return nil
	}
	for _, r := range fullKey {
		if unicode.IsDigit(r) {
			continue
		}
		if !unicode.IsLetter(r) || !unicode.In(r, unicode.Latin) {
			return nil
		}
	}
	type tokenCandidate struct {
		value  string
		length int
		order  int
	}
	candidates := make([]tokenCandidate, 0, len(tokens))
	for order, token := range tokens {
		runes := []rune(token)
		if len(runes) < 4 {
			continue
		}
		hasLetter, valid := false, true
		for _, r := range runes {
			switch {
			case unicode.IsDigit(r):
			case unicode.IsLetter(r) && unicode.In(r, unicode.Latin):
				hasLetter = true
			default:
				valid = false
			}
		}
		if valid && hasLetter {
			candidates = append(candidates, tokenCandidate{value: token, length: len(runes), order: order})
		}
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].length != candidates[right].length {
			return candidates[left].length > candidates[right].length
		}
		return candidates[left].order < candidates[right].order
	})
	result := make([]string, 0, 2)
	for _, candidate := range candidates {
		result = append(result, candidate.value)
		if len(result) == 2 {
			break
		}
	}
	return result
}

func meaningfulTitle(value string) bool {
	value = cleanTitleSurface(value)
	if utf8.RuneCountInString(value) < 2 || utf8.RuneCountInString(value) > MaxPackageRunes {
		return false
	}
	hasLetter := false
	for _, r := range value {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if hasLetter {
		return true
	}
	if len(value) == 4 {
		number, err := strconv.Atoi(value)
		return err == nil && number >= 1000 && value[0] != '0'
	}
	return false
}

func structureDirectory(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "bdmv" || lower == "stream" || lower == "certificate" || lower == "video_ts" || lower == "audio_ts" || lower == "extras" || lower == "trailers" {
		return true
	}
	return structureNamePattern.MatchString(lower)
}

func addTitleFact(items *[]TitleFact, value, source, stage string) {
	if len(*items) >= MaxQueryVariants {
		return
	}
	key := comparisonKey(value)
	for _, item := range *items {
		if comparisonKey(item.Value) == key && item.Stage == stage {
			return
		}
	}
	*items = append(*items, TitleFact{Value: boundedText(value, MaxPackageRunes), Source: boundedCode(source), Stage: boundedCode(stage)})
}

func addDiagnostic(items *[]Diagnostic, code, severity, summary string) {
	if len(*items) >= MaxDiagnostics {
		return
	}
	*items = append(*items, Diagnostic{Code: boundedCode(code), Severity: boundedCode(severity), Summary: boundedText(summary, 256)})
}

func appendUniqueBounded(target, values []string, maximum int) []string {
	for _, value := range values {
		if len(target) >= maximum {
			break
		}
		found := false
		for _, existing := range target {
			if strings.EqualFold(existing, value) {
				found = true
				break
			}
		}
		if !found {
			target = append(target, boundedText(value, 64))
		}
	}
	return target
}

func boundedText(value string, maximum int) string {
	value = norm.NFC.String(strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > maximum {
		value = string(runes[:maximum])
	}
	return value
}

func boundedCode(value string) string { return boundedText(value, 64) }

func cloneDomainInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneIdentityHint(value *IdentityHint) *IdentityHint {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func sameIdentityHint(left, right *IdentityHint) bool {
	return left != nil && right != nil && left.Provider == right.Provider && left.ID == right.ID && (left.MediaType == right.MediaType || left.MediaType == MediaTypeUnknown || right.MediaType == MediaTypeUnknown)
}

func equalDomainInt(left, right *int) bool {
	return left != nil && right != nil && *left == *right
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
