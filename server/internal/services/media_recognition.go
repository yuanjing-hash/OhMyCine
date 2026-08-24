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
	Order     int
}

type rankedMediaRecognitionCandidate struct {
	Candidate tmdb.Candidate
}

func recognizeFromDomainCandidates(ctx context.Context, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, language, region string) (tmdb.Match, error) {
	queries := domainRecognitionSearchQueries(parsed)
	candidates := make(map[string]tmdb.Candidate)
	var firstFailure error
	for _, query := range queries {
		items, err := candidateLookup.SearchCandidates(ctx, query.MediaType, query.Title, query.Year, language, region, 10)
		if err != nil {
			if tmdb.ErrorCode(err) == tmdb.ErrorNoMatch {
				continue
			}
			if firstFailure == nil {
				firstFailure = err
			}
			continue
		}
		for _, candidate := range items {
			key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
			current, exists := candidates[key]
			if !exists || candidate.Confidence > current.Confidence {
				candidates[key] = candidate
			}
		}
	}
	if len(candidates) == 0 {
		if firstFailure != nil {
			return tmdb.Match{}, firstFailure
		}
		return tmdb.Match{}, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	remote := make([]mediarecognition.RemoteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		remote = append(remote, remoteRecognitionCandidate(candidate))
	}
	decision := mediarecognition.Rank(parsed, remote)
	if enricher, ok := candidateLookup.(mediaRecognitionCandidateEnricher); ok && len(decision.Ranked) > 0 {
		shortlist := make([]tmdb.Candidate, 0, tmdb.DefaultCandidateEnrichmentLimit)
		for _, ranked := range decision.Ranked {
			key := fmt.Sprintf("%s:%d", ranked.Candidate.MediaType, ranked.Candidate.ID)
			if candidate, exists := candidates[key]; exists {
				shortlist = append(shortlist, candidate)
			}
			if len(shortlist) == tmdb.DefaultCandidateEnrichmentLimit {
				break
			}
		}
		enriched, enrichErr := enricher.EnrichCandidates(ctx, shortlist, language, tmdb.DefaultCandidateEnrichmentLimit)
		if enrichErr != nil {
			return tmdb.Match{}, enrichErr
		}
		for _, candidate := range enriched {
			candidates[fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)] = candidate
		}
		remote = remote[:0]
		for _, candidate := range candidates {
			remote = append(remote, remoteRecognitionCandidate(candidate))
		}
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

func domainRecognitionSearchQueries(parsed mediarecognition.ParsedFacts) []mediaRecognitionQuery {
	queries := make([]mediaRecognitionQuery, 0, mediaRecognitionMaxQueries)
	seen := make(map[string]struct{})
	add := func(title string, mediaType mediarecognition.MediaType, year *int) {
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
		queries = append(queries, mediaRecognitionQuery{Title: title, MediaType: string(mediaType), Year: cloneInt(year), Order: len(queries)})
	}
	typesFor := func(preferred mediarecognition.MediaType) []mediarecognition.MediaType {
		if preferred == mediarecognition.MediaTypeTV {
			return []mediarecognition.MediaType{mediarecognition.MediaTypeTV, mediarecognition.MediaTypeMovie}
		}
		return []mediarecognition.MediaType{mediarecognition.MediaTypeMovie, mediarecognition.MediaTypeTV}
	}
	variants := prioritizedDomainQueryVariants(parsed.Queries, 3)
	for index, variant := range variants {
		if index >= 3 {
			break
		}
		for _, mediaType := range typesFor(variant.SuggestedType) {
			add(variant.Title, mediaType, variant.Year)
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
		for _, mediaType := range typesFor(variant.SuggestedType) {
			add(variant.Title, mediaType, &previous)
			add(variant.Title, mediaType, &next)
			add(variant.Title, mediaType, nil)
		}
	}
	return queries
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
		if variant.Reason != "canonical" {
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
	remote := mediarecognition.RemoteCandidate{ID: candidate.ID, MediaType: mediarecognition.MediaType(candidate.MediaType), Title: candidate.Title, OriginalTitle: candidate.OriginalTitle, AlternativeTitles: append([]string(nil), candidate.AlternativeTitles...), Translations: append([]string(nil), candidate.Translations...), ReleaseYear: cloneInt(candidate.ReleaseYear), Popularity: candidate.Popularity}
	if candidate.SeasonCount > 0 {
		remote.SeasonCount = cloneInt(&candidate.SeasonCount)
	}
	return remote
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
