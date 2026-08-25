package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

var builtinProcessorCache = struct {
	sync.Mutex
	items map[string]*mediarecognition.WordProcessor
}{items: make(map[string]*mediarecognition.WordProcessor)}

const (
	mediaRecognitionStatusMatched      = "matched"
	mediaRecognitionStatusUnrecognized = "unrecognized"
	mediaRecognitionLowConfidence      = "tmdb_low_confidence"
	mediaRecognitionCandidateConflict  = "tmdb_candidate_conflict"
	mediaRecognitionCredentialMissing  = "tmdb_credential_unavailable"
)

type mediaRecognitionLookup interface {
	Search(context.Context, string, string, *int, string, string) (tmdb.Match, error)
	GetByID(context.Context, string, int64, string) (tmdb.Match, error)
}

type mediaRecognitionCandidateLookup interface {
	SearchCandidates(context.Context, string, string, *int, string, string, int) ([]tmdb.Candidate, error)
}

type mediaRecognitionCandidateEnricher interface {
	EnrichCandidates(context.Context, []tmdb.Candidate, string, int) ([]tmdb.Candidate, error)
}

const (
	mediaRecognitionMaxQueries = 10
)

// MediaRecognitionRequest contains only provider-neutral names and Profile
// snapshots. Absolute paths, provider IDs and credentials never enter the
// recognizer.
type MediaRecognitionRequest struct {
	PackageName      string
	Files            []recognitionSourceFile
	SourceKind       mediarecognition.SourceKind
	MediaTypeHint    string
	YearHint         *int
	BuiltinPackCodes []string
	BuiltinProcessor *mediarecognition.WordProcessor
	RecognitionRules []RecognitionRule
	Classification   classification.RulesV1
	Language         string
	Region           string
}

// MediaRecognitionResult is the one canonical recognition projection consumed
// by download routing and media-library reconciliation.
type MediaRecognitionResult struct {
	Status        string
	ErrorCode     string
	Title         string
	MediaType     string
	CategoryName  string
	MatchedRuleID *string
	TMDBID        *int64
	ReleaseYear   *int
	Confidence    *float64
	SeasonHint    *int
	EpisodeHint   *int
	Metadata      classification.Metadata
	Snapshot      tmdb.Snapshot
}

func parseBuiltinPackCodes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		raw = defaultProfileOrganizationConfig().BuiltinRecognitionPacksJSON
	}
	_, codes, err := canonicalBuiltinRecognitionPacks([]byte(raw))
	return codes, err
}

func builtinProcessorForCodes(codes []string) (*mediarecognition.WordProcessor, error) {
	normalized, err := mediarecognition.NormalizePackCodes(codes)
	if err != nil {
		return nil, err
	}
	key := strings.Join(normalized, "\x00")
	builtinProcessorCache.Lock()
	defer builtinProcessorCache.Unlock()
	if processor := builtinProcessorCache.items[key]; processor != nil {
		return processor, nil
	}
	processor, err := mediarecognition.NewBuiltinWordProcessor(normalized, mediarecognition.DefaultLimits())
	if err != nil {
		return nil, err
	}
	builtinProcessorCache.items[key] = processor
	return processor, nil
}

func recognizeMedia(ctx context.Context, lookup mediaRecognitionLookup, request MediaRecognitionRequest) MediaRecognitionResult {
	result := MediaRecognitionResult{Status: mediaRecognitionStatusUnrecognized}
	processor := request.BuiltinProcessor
	if processor == nil {
		var err error
		processor, err = builtinProcessorForCodes(request.BuiltinPackCodes)
		if err != nil {
			result.ErrorCode = recognitionProcessorErrorCode(err)
			return result
		}
	}
	sources := recognitionSources(request.PackageName, request.Files)
	processedSources := make([]string, 0, len(sources))
	var directHint *mediarecognition.DirectTMDBHint
	for _, source := range sources {
		processed, applyErr := processor.Apply(ctx, source)
		if applyErr != nil {
			result.ErrorCode = recognitionProcessorErrorCode(applyErr)
			return result
		}
		processedSources = append(processedSources, processed.Title)
		if processed.Hint != nil {
			if directHint != nil && !sameRecognitionHint(directHint, processed.Hint) {
				result.ErrorCode = string(mediarecognition.ErrorInvalidDirectHint)
				return result
			}
			directHint = processed.Hint
		}
	}
	parsed, parseErr := parseRecognitionFacts(request, processedSources, directHint)
	if parseErr != nil {
		result.ErrorCode = tmdb.ErrorInvalidRequest
		return result
	}
	result.Title = parsed.CanonicalTitle
	result.MediaType = string(parsed.SuggestedType)
	result.ReleaseYear = cloneInt(parsed.Year)
	if parsed.Season != nil {
		result.SeasonHint = cloneInt(parsed.Season)
	}
	if parsed.Episodes.EpisodeMin != nil {
		result.EpisodeHint = cloneInt(parsed.Episodes.EpisodeMin)
	}
	if parsed.DirectHint != nil && parsed.DirectHint.MediaType != mediarecognition.MediaTypeUnknown {
		result.MediaType = string(parsed.DirectHint.MediaType)
	}
	if directHint != nil {
		result.SeasonHint = cloneInt(directHint.Season)
		result.EpisodeHint = cloneInt(directHint.Episode)
	}
	if lookup == nil {
		result.ErrorCode = mediaRecognitionCredentialMissing
		return result
	}

	var (
		match                tmdb.Match
		err                  error
		legacyConfidenceGate bool
	)
	if parsed.DirectHint != nil {
		match, err = recognizeDirectDomainHint(ctx, lookup, parsed, *parsed.DirectHint, request.Language)
	} else if len(parsed.Queries) == 0 {
		result.ErrorCode = tmdb.ErrorInvalidRequest
		return result
	} else if candidateLookup, ok := lookup.(mediaRecognitionCandidateLookup); ok {
		match, err = recognizeFromDomainCandidates(ctx, lookup, candidateLookup, parsed, request.Language, request.Region)
	} else {
		legacyConfidenceGate = true
		for _, query := range domainRecognitionSearchQueries(parsed) {
			match, err = lookup.Search(ctx, query.MediaType, query.Title, query.Year, request.Language, request.Region)
			if err == nil || tmdb.ErrorCode(err) != tmdb.ErrorNoMatch {
				break
			}
		}
	}
	if err != nil {
		result.ErrorCode = tmdb.ErrorCode(err)
		return result
	}
	result.Metadata = classification.Metadata{
		MediaType:           classification.MediaType(match.MediaType),
		GenreIDs:            append([]int(nil), match.GenreIDs...),
		OriginalLanguage:    match.OriginalLanguage,
		ProductionCountries: append([]string(nil), match.ProductionCountries...),
		OriginCountries:     append([]string(nil), match.OriginCountries...),
		ReleaseYear:         cloneInt(match.ReleaseYear),
	}
	classified := classification.Classify(result.Metadata, request.Classification)
	result.Title = strings.TrimSpace(match.Title)
	result.MediaType = match.MediaType
	result.CategoryName = classified.CategoryName
	result.MatchedRuleID = classified.MatchedRuleID
	result.TMDBID = cloneInt64(&match.ID)
	result.ReleaseYear = cloneInt(match.ReleaseYear)
	result.Confidence = cloneFloat64(&match.Confidence)
	result.Snapshot = match.Snapshot
	if legacyConfidenceGate && match.Confidence < mediarecognition.DefaultScoreConfig().MatchThreshold {
		result.ErrorCode = mediaRecognitionLowConfidence
		return result
	}
	result.Status = mediaRecognitionStatusMatched
	return result
}

func recognizeDirectDomainHint(ctx context.Context, lookup mediaRecognitionLookup, parsed mediarecognition.ParsedFacts, hint mediarecognition.IdentityHint, language string) (tmdb.Match, error) {
	if hint.MediaType == mediarecognition.MediaTypeMovie || hint.MediaType == mediarecognition.MediaTypeTV {
		return lookup.GetByID(ctx, string(hint.MediaType), hint.ID, language)
	}
	types := []mediarecognition.MediaType{mediarecognition.MediaTypeMovie, mediarecognition.MediaTypeTV}
	if parsed.SuggestedType == mediarecognition.MediaTypeTV {
		types[0], types[1] = types[1], types[0]
	}
	matches := make([]tmdb.Match, 0, 2)
	var firstFailure error
	for _, mediaType := range types {
		match, err := lookup.GetByID(ctx, string(mediaType), hint.ID, language)
		if err != nil {
			if firstFailure == nil {
				firstFailure = err
			}
			continue
		}
		matches = append(matches, match)
	}
	if len(matches) == 0 {
		return tmdb.Match{}, firstFailure
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	remote := make([]mediarecognition.RemoteCandidate, 0, len(matches))
	for _, match := range matches {
		remote = append(remote, mediarecognition.RemoteCandidate{ID: match.ID, MediaType: mediarecognition.MediaType(match.MediaType), Title: match.Title, OriginalTitle: match.Snapshot.OriginalTitle, ReleaseYear: cloneInt(match.ReleaseYear)})
	}
	decision := mediarecognition.Rank(parsed, remote)
	if decision.Status != mediarecognition.DecisionMatched || decision.Match == nil {
		return tmdb.Match{}, &tmdb.ClientError{Code: domainRecognitionErrorCode(decision.Reason)}
	}
	for _, match := range matches {
		if match.ID == decision.Match.ID && match.MediaType == string(decision.Match.MediaType) {
			return match, nil
		}
	}
	return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorInvalidResponse}
}

type mediaRecognitionQuery struct {
	Title     string
	MediaType string
	Year      *int
	Language  string
	Phase     string
	Order     int
}

func recognizeFromDomainCandidates(ctx context.Context, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, language, region string) (tmdb.Match, error) {
	queries := domainRecognitionSearchQueries(parsed)
	candidates := make(map[string]tmdb.Candidate)
	candidateOrder := make([]string, 0, 32)
	queryCandidateKeys := make([][]string, 0, len(queries))
	var firstFailure error
	searchCount := 0
	search := func(query mediaRecognitionQuery) error {
		if searchCount >= mediaRecognitionMaxQueries {
			return nil
		}
		searchCount++
		queryLanguage := language
		if query.Language != "" {
			queryLanguage = query.Language
		}
		items, err := candidateLookup.SearchCandidates(ctx, query.MediaType, query.Title, query.Year, queryLanguage, region, 10)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if tmdb.ErrorCode(err) == tmdb.ErrorNoMatch {
				return nil
			}
			if firstFailure == nil {
				firstFailure = err
			}
			return nil
		}
		batch := make([]string, 0, len(items))
		for _, candidate := range items {
			key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
			batch = append(batch, key)
			current, exists := candidates[key]
			if !exists || candidate.Confidence > current.Confidence {
				candidates[key] = candidate
			}
			if !exists {
				candidateOrder = append(candidateOrder, key)
			}
		}
		queryCandidateKeys = append(queryCandidateKeys, batch)
		return nil
	}
	for _, query := range queries {
		if query.Phase == "year_fallback" {
			continue
		}
		if err := search(query); err != nil {
			return tmdb.Match{}, err
		}
	}
	if len(candidates) == 0 {
		for _, query := range domainLatinTokenRecallQueries(parsed, queries, language, mediaRecognitionMaxQueries-searchCount) {
			queries = append(queries, query)
			if err := search(query); err != nil {
				return tmdb.Match{}, err
			}
		}
	}
	for _, query := range queries {
		if query.Phase != "year_fallback" {
			continue
		}
		if err := search(query); err != nil {
			return tmdb.Match{}, err
		}
	}
	// TMDB can index a transliterated name only through a related item in the
	// other media type. Re-query a bounded set of authoritative candidate titles
	// in the structurally preferred type. This is generic alias bridging, not a
	// title dictionary, and stays inside the same ten-request budget.
	for _, query := range domainCandidateAliasRecallQueries(parsed, candidates, candidateOrder, queries, mediaRecognitionMaxQueries-searchCount) {
		if err := search(query); err != nil {
			return tmdb.Match{}, err
		}
	}
	if len(candidates) == 0 {
		if firstFailure != nil {
			return tmdb.Match{}, firstFailure
		}
		return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	remote := orderedRemoteRecognitionCandidates(candidates, candidateOrder)
	decision := mediarecognition.Rank(parsed, remote)
	if enricher, ok := candidateLookup.(mediaRecognitionCandidateEnricher); ok && len(decision.Ranked) > 0 {
		shortlist := domainEnrichmentShortlist(candidates, queryCandidateKeys, decision.Ranked, tmdb.DefaultCandidateEnrichmentLimit)
		enriched, enrichErr := enricher.EnrichCandidates(ctx, shortlist, language, tmdb.DefaultCandidateEnrichmentLimit)
		if enrichErr != nil {
			return tmdb.Match{}, enrichErr
		}
		for _, candidate := range enriched {
			candidates[fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)] = candidate
		}
		remote = orderedRemoteRecognitionCandidates(candidates, candidateOrder)
		decision = mediarecognition.Rank(parsed, remote)
	}
	if decision.Status != mediarecognition.DecisionMatched || decision.Match == nil {
		return tmdb.Match{}, &tmdb.ClientError{Code: domainRecognitionErrorCode(decision.Reason)}
	}
	best := *decision.Match
	match, err := lookup.GetByID(ctx, string(best.MediaType), best.ID, language)
	if err != nil {
		return tmdb.Match{}, err
	}
	if match.Snapshot.OriginalTitle == "" {
		match.Snapshot.OriginalTitle = strings.TrimSpace(best.OriginalTitle)
	}
	if match.OriginalLanguage == "" {
		candidate := candidates[fmt.Sprintf("%s:%d", best.MediaType, best.ID)]
		match.OriginalLanguage = candidate.OriginalLanguage
		match.Snapshot.OriginalLanguage = candidate.OriginalLanguage
	}
	if match.ReleaseYear == nil {
		match.ReleaseYear = cloneInt(best.ReleaseYear)
	}
	match.Confidence = decision.Confidence
	return match, nil
}

func orderedRemoteRecognitionCandidates(candidates map[string]tmdb.Candidate, order []string) []mediarecognition.RemoteCandidate {
	result := make([]mediarecognition.RemoteCandidate, 0, len(order))
	for _, key := range order {
		if candidate, exists := candidates[key]; exists {
			result = append(result, remoteRecognitionCandidate(candidate))
		}
	}
	return result
}

func domainCandidateAliasRecallQueries(parsed mediarecognition.ParsedFacts, candidates map[string]tmdb.Candidate, order []string, existing []mediaRecognitionQuery, maximum int) []mediaRecognitionQuery {
	if maximum <= 0 {
		return nil
	}
	keyFor := func(mediaType, title string) string {
		return mediaType + "\x00" + strings.ToLower(strings.TrimSpace(title))
	}
	seen := make(map[string]struct{}, len(existing)+maximum)
	for _, query := range existing {
		seen[keyFor(query.MediaType, query.Title)] = struct{}{}
	}
	result := make([]mediaRecognitionQuery, 0, maximum)
	for _, candidateKey := range order {
		candidate, exists := candidates[candidateKey]
		if !exists {
			continue
		}
		targetType := string(parsed.SuggestedType)
		if targetType != "movie" && targetType != "tv" {
			if candidate.MediaType == "movie" {
				targetType = "tv"
			} else {
				targetType = "movie"
			}
		}
		for _, title := range []string{candidate.Title, candidate.OriginalTitle} {
			title = strings.TrimSpace(title)
			if title == "" || len(title) > mediarecognition.MaxPackageRunes*4 || strings.ContainsAny(title, "\x00\r\n") {
				continue
			}
			key := keyFor(targetType, title)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, mediaRecognitionQuery{Title: title, MediaType: targetType, Order: len(existing) + len(result)})
			if len(result) == maximum {
				return result
			}
		}
	}
	return result
}

// domainEnrichmentShortlist preserves TMDB's per-query relevance without
// allowing one movie or TV result page to consume the entire alias budget.
// A round-robin first pass gives both cross-type recalls an enrichment chance;
// the domain rank then fills any remaining slots deterministically.
func domainEnrichmentShortlist(candidates map[string]tmdb.Candidate, queryKeys [][]string, ranked []mediarecognition.RankedCandidate, limit int) []tmdb.Candidate {
	if limit <= 0 {
		return nil
	}
	result := make([]tmdb.Candidate, 0, limit)
	seen := make(map[string]struct{}, limit)
	add := func(key string) {
		if len(result) >= limit {
			return
		}
		candidate, exists := candidates[key]
		if !exists {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, candidate)
	}
	for offset := 0; len(result) < limit; offset++ {
		advanced := false
		for _, keys := range queryKeys {
			if offset < len(keys) {
				add(keys[offset])
				advanced = true
			}
			if len(result) == limit {
				break
			}
		}
		if !advanced {
			break
		}
	}
	for _, item := range ranked {
		add(fmt.Sprintf("%s:%d", item.Candidate.MediaType, item.Candidate.ID))
	}
	return result
}

func domainRecognitionSearchQueries(parsed mediarecognition.ParsedFacts) []mediaRecognitionQuery {
	queries := make([]mediaRecognitionQuery, 0, mediaRecognitionMaxQueries)
	seen := make(map[string]struct{})
	add := func(title string, mediaType mediarecognition.MediaType, year *int, phase string) {
		if len(queries) >= mediaRecognitionMaxQueries {
			return
		}
		if mediaType != mediarecognition.MediaTypeMovie && mediaType != mediarecognition.MediaTypeTV {
			return
		}
		key := string(mediaType) + "\x00" + strings.ToLower(title) + "\x00"
		if year != nil {
			key += fmt.Sprint(*year)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		queries = append(queries, mediaRecognitionQuery{Title: title, MediaType: string(mediaType), Year: cloneInt(year), Phase: phase, Order: len(queries)})
	}
	variants := prioritizedDomainQueryVariants(parsed.Queries, 3)
	for index, variant := range variants {
		if index >= 3 {
			break
		}
		for _, mediaType := range domainRecognitionTypesFor(variant.SuggestedType) {
			add(variant.Title, mediaType, variant.Year, "primary")
		}
		if len(queries) >= mediaRecognitionMaxQueries {
			break
		}
	}
	for index, variant := range variants {
		if index >= 1 || variant.Year == nil {
			continue
		}
		previous, next := *variant.Year-1, *variant.Year+1
		for _, mediaType := range domainRecognitionTypesFor(variant.SuggestedType) {
			add(variant.Title, mediaType, &previous, "year_fallback")
			add(variant.Title, mediaType, &next, "year_fallback")
			add(variant.Title, mediaType, nil, "year_fallback")
		}
	}
	return queries
}

func domainLatinTokenRecallQueries(parsed mediarecognition.ParsedFacts, existing []mediaRecognitionQuery, language string, maximum int) []mediaRecognitionQuery {
	if maximum <= 0 {
		return nil
	}
	fallbackLanguage := strings.TrimSpace(language)
	if !strings.HasPrefix(strings.ToLower(fallbackLanguage), "en") {
		fallbackLanguage = "en-US"
	}
	seen := make(map[string]struct{}, len(existing)+maximum)
	keyFor := func(mediaType, title, queryLanguage string, year *int) string {
		key := mediaType + "\x00" + strings.ToLower(strings.TrimSpace(title)) + "\x00" + strings.ToLower(strings.TrimSpace(queryLanguage)) + "\x00"
		if year != nil {
			key += fmt.Sprint(*year)
		}
		return key
	}
	for _, query := range existing {
		queryLanguage := query.Language
		if queryLanguage == "" {
			queryLanguage = language
		}
		seen[keyFor(query.MediaType, query.Title, queryLanguage, query.Year)] = struct{}{}
	}
	result := make([]mediaRecognitionQuery, 0, maximum)
	for _, variant := range parsed.Queries {
		if variant.Reason != "latin_token_fallback" {
			continue
		}
		for _, mediaType := range domainRecognitionTypesFor(variant.SuggestedType) {
			key := keyFor(string(mediaType), variant.Title, fallbackLanguage, variant.Year)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, mediaRecognitionQuery{Title: variant.Title, MediaType: string(mediaType), Year: cloneInt(variant.Year), Language: fallbackLanguage, Order: len(existing) + len(result)})
			if len(result) == maximum {
				return result
			}
		}
	}
	return result
}

func domainRecognitionTypesFor(preferred mediarecognition.MediaType) []mediarecognition.MediaType {
	if preferred == mediarecognition.MediaTypeTV {
		return []mediarecognition.MediaType{mediarecognition.MediaTypeTV, mediarecognition.MediaTypeMovie}
	}
	return []mediarecognition.MediaType{mediarecognition.MediaTypeMovie, mediarecognition.MediaTypeTV}
}

func prioritizedDomainQueryVariants(variants []mediarecognition.QueryVariant, maximum int) []mediarecognition.QueryVariant {
	result := make([]mediarecognition.QueryVariant, 0, maximum)
	seen := make(map[string]struct{})
	add := func(variant mediarecognition.QueryVariant) {
		if len(result) >= maximum {
			return
		}
		title := strings.TrimSpace(variant.Title)
		if title == "" {
			return
		}
		key := strings.ToLower(title) + "\x00" + string(variant.SuggestedType) + "\x00"
		if variant.Year != nil {
			key += fmt.Sprint(*variant.Year)
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, variant)
	}
	for _, variant := range variants {
		if variant.Reason == "canonical" {
			add(variant)
		}
	}
	for _, variant := range variants {
		if variant.Reason != "canonical" && variant.Reason != "latin_token_fallback" {
			add(variant)
		}
	}
	return result
}

func parseRecognitionFacts(request MediaRecognitionRequest, processedSources []string, directHint *mediarecognition.DirectTMDBHint) (mediarecognition.ParsedFacts, error) {
	prepared := make([]mediarecognition.PreparedName, 0, len(processedSources)*3)
	seen := make(map[string]struct{})
	addPrepared := func(value, source string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len(prepared) >= 32 {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		prepared = append(prepared, mediarecognition.PreparedName{Value: value, Source: source})
	}
	for _, source := range processedSources {
		addPrepared(source, "builtin")
		for _, mediaType := range []string{"movie", "tv"} {
			if processed := applyRecognitionRules(source, mediaType, request.RecognitionRules); processed != source {
				addPrepared(processed, "profile_"+mediaType)
			}
		}
	}
	files := make([]mediarecognition.FileFact, 0, len(request.Files))
	for _, file := range request.Files {
		relative := strings.ReplaceAll(strings.TrimSpace(file.RelativePath), "\\", "/")
		// MediaLibrary entries use one leading slash as their canonical logical
		// provider-root marker after the storage boundary has already validated
		// them. Strip exactly that marker only for the trusted scan adapter; raw
		// downloader/unknown facts keep it and are rejected as absolute input by
		// the pure recognizer.
		if request.SourceKind == mediarecognition.SourceLibraryScan && strings.HasPrefix(relative, "/") && !strings.HasPrefix(relative, "//") {
			relative = strings.TrimPrefix(relative, "/")
		}
		files = append(files, mediarecognition.FileFact{RelativePath: relative, Size: file.Size})
	}
	input := mediarecognition.InputFacts{PackageName: strings.TrimSpace(request.PackageName), SourceKind: request.SourceKind, Files: files, MediaTypeHint: mediarecognition.MediaType(request.MediaTypeHint), YearHint: cloneInt(request.YearHint), PreparedNames: prepared}
	if directHint != nil {
		input.DirectHint = &mediarecognition.IdentityHint{Provider: "tmdb", ID: int64(directHint.TMDBID), MediaType: mediarecognition.MediaType(directHint.MediaType)}
	}
	return mediarecognition.Parse(input)
}

func remoteRecognitionCandidate(candidate tmdb.Candidate) mediarecognition.RemoteCandidate {
	remote := mediarecognition.RemoteCandidate{ID: candidate.ID, MediaType: mediarecognition.MediaType(candidate.MediaType), Title: candidate.Title, OriginalTitle: candidate.OriginalTitle, AlternativeTitles: append([]string(nil), candidate.AlternativeTitles...), Translations: append([]string(nil), candidate.Translations...), ReleaseYear: cloneInt(candidate.ReleaseYear), SeasonYears: cloneSeasonYears(candidate.SeasonYears), Popularity: candidate.Popularity}
	if candidate.SeasonCount > 0 {
		remote.SeasonCount = cloneInt(&candidate.SeasonCount)
	}
	return remote
}

func cloneSeasonYears(values map[int]int) map[int]int {
	if len(values) == 0 {
		return nil
	}
	result := make(map[int]int, len(values))
	for season, year := range values {
		result[season] = year
	}
	return result
}

func domainRecognitionErrorCode(reason mediarecognition.DecisionReason) string {
	switch reason {
	case mediarecognition.ReasonLowConfidence:
		return mediaRecognitionLowConfidence
	case mediarecognition.ReasonCandidateConflict:
		return mediaRecognitionCandidateConflict
	default:
		return tmdb.ErrorNoMatch
	}
}

func recognitionProcessorErrorCode(err error) string {
	var processing *mediarecognition.ProcessingError
	if errors.As(err, &processing) {
		return string(processing.Code)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return string(mediarecognition.ErrorContextCanceled)
	}
	if errors.Is(err, mediarecognition.ErrInvalidPackCodes) {
		return "invalid_pack_codes"
	}
	return string(mediarecognition.ErrorInvalidRule)
}

func sameRecognitionHint(left, right *mediarecognition.DirectTMDBHint) bool {
	if left == nil || right == nil || left.TMDBID != right.TMDBID || left.MediaType != right.MediaType {
		return left == nil && right == nil
	}
	return equalRecognitionInt(left.Season, right.Season) && equalRecognitionInt(left.Episode, right.Episode)
}

func equalRecognitionInt(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
