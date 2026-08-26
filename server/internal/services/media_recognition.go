package services

import (
	"context"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"sync"

	"github.com/yuanjing-hash/ohmycine/server/internal/classification"
	"github.com/yuanjing-hash/ohmycine/server/internal/mediarecognition"
	"github.com/yuanjing-hash/ohmycine/server/pkg/aiprovider"
	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

var builtinProcessorCache = struct {
	sync.Mutex
	items map[string]*mediarecognition.WordProcessor
}{items: make(map[string]*mediarecognition.WordProcessor)}

const (
	mediaRecognitionStatusMatched       = "matched"
	mediaRecognitionStatusUnrecognized  = "unrecognized"
	mediaIdentityStatusVerified         = "verified"
	mediaIdentityStatusProvisional      = "provisional"
	mediaIdentityStatusLocalProvisional = "local_provisional"
	mediaRecognitionLowConfidence       = "tmdb_low_confidence"
	mediaRecognitionCandidateConflict   = "tmdb_candidate_conflict"
	mediaRecognitionCredentialMissing   = "tmdb_credential_unavailable"
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

type mediaRecognitionAIAssist interface {
	GenerateCandidateArbitration(context.Context, aiprovider.CandidateArbitrationPayload) (aiprovider.CandidateArbitrationResult, error)
	GenerateTitleRewrite(context.Context, aiprovider.TitleRewritePayload) (aiprovider.TitleRewriteResult, error)
	RuntimeRelativeBasenamesEnabled() bool
}

const (
	mediaRecognitionMaxQueries = 10
)

// MediaRecognitionRequest contains only provider-neutral names and Profile
// snapshots. Absolute paths, provider IDs and credentials never enter the
// recognizer.
type MediaRecognitionRequest struct {
	PackageName string
	// AuxiliaryNames are bounded provider-neutral title facts such as a PT
	// result subtitle. They improve recall but never carry provider identity,
	// paths, URLs or credentials and never override the primary package name.
	AuxiliaryNames   []string
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
	AIAssist         mediaRecognitionAIAssist
}

// MediaRecognitionResult is the one canonical recognition projection consumed
// by download routing and media-library reconciliation.
type MediaRecognitionResult struct {
	Status         string
	ErrorCode      string
	Title          string
	MediaType      string
	CategoryName   string
	MatchedRuleID  *string
	TMDBID         *int64
	ReleaseYear    *int
	Confidence     *float64
	SeasonHint     *int
	EpisodeHint    *int
	Metadata       classification.Metadata
	Snapshot       tmdb.Snapshot
	IdentityStatus string
	IdentitySource string
	DecisionReason mediarecognition.DecisionReason
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
	processedSources := make([]string, 0, len(sources)+len(request.AuxiliaryNames))
	var directHint *mediarecognition.DirectTMDBHint
	processSource := func(source string, acceptDirectHint bool) error {
		processed, applyErr := processor.Apply(ctx, source)
		if applyErr != nil {
			return applyErr
		}
		processedSources = appendUniqueRecognitionSource(processedSources, processed.Title)
		if acceptDirectHint && processed.Hint != nil {
			if directHint != nil && !sameRecognitionHint(directHint, processed.Hint) {
				return &mediarecognition.ProcessingError{Code: mediarecognition.ErrorInvalidDirectHint, PackCode: "processor", Err: errors.New("conflicting direct TMDB hints")}
			}
			directHint = processed.Hint
		}
		return nil
	}
	for _, source := range sources {
		if applyErr := processSource(source, true); applyErr != nil {
			result.ErrorCode = recognitionProcessorErrorCode(applyErr)
			return result
		}
	}
	for _, auxiliary := range request.AuxiliaryNames {
		if auxiliary = safeRecognitionAuxiliaryName(auxiliary); auxiliary != "" {
			// Site subtitles/descriptions are untrusted weak recall facts. They
			// may improve title retrieval, but an embedded tmdb marker must never
			// become a direct identity override.
			if applyErr := processSource(auxiliary, false); applyErr != nil {
				result.ErrorCode = recognitionProcessorErrorCode(applyErr)
				return result
			}
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
		decisionReason       mediarecognition.DecisionReason
		identitySource       = mediaIdentitySourceAutomatic
	)
	if parsed.DirectHint != nil {
		identitySource = mediaIdentitySourceDirectID
		match, err = recognizeDirectDomainHint(ctx, lookup, parsed, *parsed.DirectHint, request.Language)
	} else if len(parsed.Queries) == 0 {
		result.ErrorCode = tmdb.ErrorNoMatch
		result.IdentityStatus = mediaIdentityStatusLocalProvisional
		result.IdentitySource = mediaIdentitySourceLocalProvisional
		result.DecisionReason = mediarecognition.ReasonNoMatch
		return result
	} else if candidateLookup, ok := lookup.(mediaRecognitionCandidateLookup); ok {
		match, decisionReason, identitySource, err = recognizeFromDomainCandidatesAssisted(ctx, lookup, candidateLookup, parsed, request)
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
		if result.ErrorCode == tmdb.ErrorNoMatch {
			result.IdentityStatus = mediaIdentityStatusLocalProvisional
			result.IdentitySource = mediaIdentitySourceLocalProvisional
			result.DecisionReason = decisionReason
		}
		return result
	}
	result.Metadata = classificationMetadataForMatch(match)
	classified := classification.Classify(result.Metadata, request.Classification)
	result.Title = strings.TrimSpace(match.Title)
	result.MediaType = match.MediaType
	result.CategoryName = classified.CategoryName
	result.MatchedRuleID = classified.MatchedRuleID
	result.TMDBID = cloneInt64(&match.ID)
	result.ReleaseYear = cloneInt(match.ReleaseYear)
	result.Confidence = cloneFloat64(&match.Confidence)
	result.Snapshot = match.Snapshot
	result.IdentitySource = identitySource
	if legacyConfidenceGate && match.Confidence < mediarecognition.DefaultScoreConfig().ExtremeThreshold {
		result.ErrorCode = mediaRecognitionLowConfidence
		return result
	}
	result.Status = mediaRecognitionStatusMatched
	result.IdentityStatus = mediaIdentityStatusVerified
	result.DecisionReason = decisionReason
	if decisionReason == mediarecognition.ReasonLowConfidence || decisionReason == mediarecognition.ReasonCandidateConflict || match.Confidence < mediarecognition.DefaultScoreConfig().MatchThreshold {
		result.IdentityStatus = mediaIdentityStatusProvisional
		if result.DecisionReason == "" {
			result.DecisionReason = mediarecognition.ReasonLowConfidence
		}
	}
	return result
}

func classificationMetadataForMatch(match tmdb.Match) classification.Metadata {
	return classification.Metadata{
		MediaType:           classification.MediaType(match.MediaType),
		GenreIDs:            append([]int(nil), match.GenreIDs...),
		OriginalLanguage:    match.OriginalLanguage,
		ProductionCountries: append([]string(nil), match.ProductionCountries...),
		OriginCountries:     append([]string(nil), match.OriginCountries...),
		ReleaseYear:         cloneInt(match.ReleaseYear),
	}
}

func safeRecognitionAuxiliaryName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || len([]rune(value)) > mediarecognition.MaxPackageRunes || strings.ContainsAny(value, "\x00\\") {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "://") || strings.Contains(lower, "www.") || strings.HasPrefix(value, "/") ||
		strings.Contains(lower, "authorization=") || strings.Contains(lower, "signature=") || strings.Contains(lower, "token=") ||
		strings.Contains(lower, "api_key=") || strings.Contains(lower, "apikey=") || strings.Contains(lower, "cookie=") || strings.Contains(lower, "x-amz-") ||
		strings.Contains(lower, "tmdbid") || strings.Contains(lower, "{tmdb-") || strings.Contains(lower, "[tmdb-") || strings.Contains(value, "{[") ||
		len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && value[2] == '/' {
		return ""
	}
	return value
}

func appendUniqueRecognitionSource(sources []string, value string) []string {
	key := strings.ToLower(strings.TrimSpace(value))
	for _, existing := range sources {
		if strings.ToLower(strings.TrimSpace(existing)) == key {
			return sources
		}
	}
	return append(sources, value)
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
		candidate := mediarecognition.RemoteCandidate{ID: match.ID, MediaType: mediarecognition.MediaType(match.MediaType), Title: match.Title, OriginalTitle: match.Snapshot.OriginalTitle, OriginalLanguage: match.OriginalLanguage, ReleaseYear: cloneInt(match.ReleaseYear), VoteCount: match.Snapshot.VoteCount, HasPoster: strings.TrimSpace(match.Snapshot.PosterPath) != ""}
		if match.Snapshot.SeasonCount > 0 {
			candidate.SeasonCount = cloneInt(&match.Snapshot.SeasonCount)
		}
		if match.Snapshot.EpisodeCount > 0 {
			candidate.EpisodeCount = cloneInt(&match.Snapshot.EpisodeCount)
		}
		remote = append(remote, candidate)
	}
	decision := mediarecognition.Rank(parsed, remote)
	if decision.Match == nil {
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

func recognizeFromDomainCandidates(ctx context.Context, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, language, region string) (tmdb.Match, mediarecognition.DecisionReason, error) {
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
			return tmdb.Match{}, "", err
		}
	}
	if len(candidates) == 0 {
		for _, query := range domainLatinTokenRecallQueries(parsed, queries, language, mediaRecognitionMaxQueries-searchCount) {
			queries = append(queries, query)
			if err := search(query); err != nil {
				return tmdb.Match{}, "", err
			}
		}
	}
	for _, query := range queries {
		if query.Phase != "year_fallback" {
			continue
		}
		if err := search(query); err != nil {
			return tmdb.Match{}, "", err
		}
	}
	// TMDB can index a transliterated name only through a related item in the
	// other media type. Re-query a bounded set of authoritative candidate titles
	// in the structurally preferred type. This is generic alias bridging, not a
	// title dictionary, and stays inside the same ten-request budget.
	for _, query := range domainCandidateAliasRecallQueries(parsed, candidates, candidateOrder, queries, mediaRecognitionMaxQueries-searchCount) {
		if err := search(query); err != nil {
			return tmdb.Match{}, "", err
		}
	}
	if len(candidates) == 0 {
		if firstFailure != nil {
			return tmdb.Match{}, "", firstFailure
		}
		return tmdb.Match{}, mediarecognition.ReasonNoMatch, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	remote := orderedRemoteRecognitionCandidates(candidates, candidateOrder)
	decision := mediarecognition.Rank(parsed, remote)
	if enricher, ok := candidateLookup.(mediaRecognitionCandidateEnricher); ok && len(decision.Ranked) > 0 {
		shortlist := domainEnrichmentShortlist(candidates, queryCandidateKeys, decision.Ranked, tmdb.DefaultCandidateEnrichmentLimit)
		enriched, enrichErr := enricher.EnrichCandidates(ctx, shortlist, language, tmdb.DefaultCandidateEnrichmentLimit)
		if enrichErr != nil {
			return tmdb.Match{}, "", enrichErr
		}
		for _, candidate := range enriched {
			candidates[fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)] = candidate
		}
		remote = orderedRemoteRecognitionCandidates(candidates, candidateOrder)
		decision = mediarecognition.Rank(parsed, remote)
	}
	if decision.Match == nil {
		return tmdb.Match{}, decision.Reason, &tmdb.ClientError{Code: domainRecognitionErrorCode(decision.Reason)}
	}
	best := *decision.Match
	match, err := lookup.GetByID(ctx, string(best.MediaType), best.ID, language)
	if err != nil {
		return tmdb.Match{}, decision.Reason, err
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
	// Preserve the reason on the returned match without making confidence a
	// second hard gate. The caller records provisional identity state; optional
	// AI orchestration may intercept the same decision before this fallback.
	return match, decision.Reason, nil
}

func recognizeFromDomainCandidatesAssisted(ctx context.Context, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, request MediaRecognitionRequest) (tmdb.Match, mediarecognition.DecisionReason, string, error) {
	match, reason, err := recognizeFromDomainCandidates(ctx, lookup, candidateLookup, parsed, request.Language, request.Region)
	if request.AIAssist == nil {
		return match, reason, mediaIdentitySourceAutomatic, err
	}
	if err == nil && (reason == mediarecognition.ReasonLowConfidence || reason == mediarecognition.ReasonCandidateConflict) {
		selected, action, aiErr := arbitrateRecognitionCandidates(ctx, request.AIAssist, lookup, candidateLookup, parsed, request.Language, request.Region)
		if ctx.Err() != nil {
			return tmdb.Match{}, reason, mediaIdentitySourceAutomatic, ctx.Err()
		}
		if aiErr == nil && action == "select" {
			return selected, reason, mediaIdentitySourceAI, nil
		}
		if aiErr == nil && action == "rewrite" {
			rewritten, rewrittenReason, rewriteErr := rewriteRecognitionWithAI(ctx, request.AIAssist, lookup, candidateLookup, parsed, request)
			if ctx.Err() != nil {
				return tmdb.Match{}, rewrittenReason, mediaIdentitySourceAutomatic, ctx.Err()
			}
			if rewriteErr == nil {
				return rewritten, rewrittenReason, mediaIdentitySourceAI, nil
			}
		}
		// AI is an optional adjudicator. Disabled, unavailable or invalid output
		// never replaces the deterministic provisional winner or blocks the job.
		return match, reason, mediaIdentitySourceAutomatic, nil
	}
	if err != nil && tmdb.ErrorCode(err) == tmdb.ErrorNoMatch && (reason == mediarecognition.ReasonNoMatch || reason == mediarecognition.ReasonExtremeLowConfidence) {
		rewritten, rewrittenReason, rewriteErr := rewriteRecognitionWithAI(ctx, request.AIAssist, lookup, candidateLookup, parsed, request)
		if ctx.Err() != nil {
			return tmdb.Match{}, rewrittenReason, mediaIdentitySourceAutomatic, ctx.Err()
		}
		if rewriteErr == nil {
			return rewritten, rewrittenReason, mediaIdentitySourceAI, nil
		}
	}
	return match, reason, mediaIdentitySourceAutomatic, err
}

func arbitrateRecognitionCandidates(ctx context.Context, assist mediaRecognitionAIAssist, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, language, region string) (tmdb.Match, string, error) {
	candidates, err := collectRecognitionCandidates(ctx, candidateLookup, domainRecognitionSearchQueries(parsed), language, region)
	if err != nil || len(candidates) == 0 {
		return tmdb.Match{}, "", err
	}
	remote := make([]mediarecognition.RemoteCandidate, 0, len(candidates))
	byKey := make(map[string]tmdb.Candidate, len(candidates))
	for _, candidate := range candidates {
		key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
		byKey[key] = candidate
		remote = append(remote, remoteRecognitionCandidate(candidate))
	}
	decision := mediarecognition.Rank(parsed, remote)
	if len(decision.Ranked) == 0 {
		return tmdb.Match{}, "", &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	limit := min(len(decision.Ranked), 5)
	payload := aiprovider.CandidateArbitrationPayload{Release: aiprovider.ArbitrationRelease{
		Title: strings.TrimSpace(parsed.CanonicalTitle), MediaTypeHint: string(parsed.SuggestedType), Year: cloneInt(parsed.Year), Season: cloneInt(parsed.Season), EpisodeStart: cloneInt(parsed.Episodes.EpisodeMin), EpisodeEnd: cloneInt(parsed.Episodes.EpisodeMax),
	}, Candidates: make([]aiprovider.ArbitrationCandidate, 0, limit)}
	refKeys := make(map[string]string, limit)
	scores := make(map[string]float64, limit)
	for index, ranked := range decision.Ranked[:limit] {
		ref := fmt.Sprintf("c%d", index+1)
		key := fmt.Sprintf("%s:%d", ranked.Candidate.MediaType, ranked.Candidate.ID)
		aliases := safeAICandidateAliases(ranked.Candidate)
		seasonCount, episodeCount := 0, 0
		if ranked.Candidate.SeasonCount != nil {
			seasonCount = *ranked.Candidate.SeasonCount
		}
		if ranked.Candidate.EpisodeCount != nil {
			episodeCount = *ranked.Candidate.EpisodeCount
		}
		payload.Candidates = append(payload.Candidates, aiprovider.ArbitrationCandidate{CandidateRef: ref, Title: ranked.Candidate.Title, OriginalTitle: ranked.Candidate.OriginalTitle, Aliases: aliases, MediaType: string(ranked.Candidate.MediaType), Year: cloneInt(ranked.Candidate.ReleaseYear), SeasonCount: seasonCount, EpisodeCount: episodeCount})
		refKeys[ref], scores[key] = key, ranked.Score.Total
	}
	result, err := assist.GenerateCandidateArbitration(ctx, payload)
	if err != nil {
		return tmdb.Match{}, "", err
	}
	if result.Action != "select" {
		return tmdb.Match{}, result.Action, nil
	}
	key := refKeys[result.CandidateRef]
	candidate, ok := byKey[key]
	if !ok {
		return tmdb.Match{}, "", &tmdb.ClientError{Code: tmdb.ErrorInvalidResponse}
	}
	match, err := lookup.GetByID(ctx, candidate.MediaType, candidate.ID, language)
	if err != nil {
		return tmdb.Match{}, "", err
	}
	match.Confidence = scores[key]
	return match, "select", nil
}

func rewriteRecognitionWithAI(ctx context.Context, assist mediaRecognitionAIAssist, lookup mediaRecognitionLookup, candidateLookup mediaRecognitionCandidateLookup, parsed mediarecognition.ParsedFacts, request MediaRecognitionRequest) (tmdb.Match, mediarecognition.DecisionReason, error) {
	releaseTitle := strings.TrimSpace(request.PackageName)
	if releaseTitle == "" {
		releaseTitle = strings.TrimSpace(parsed.CanonicalTitle)
	}
	payload := aiprovider.TitleRewritePayload{ReleaseTitle: releaseTitle, MediaTypeHint: string(parsed.SuggestedType), YearHint: cloneInt(parsed.Year)}
	if assist.RuntimeRelativeBasenamesEnabled() {
		payload.Files = make([]aiprovider.TitleRewriteFile, 0, min(len(request.Files), 32))
		for _, file := range request.Files {
			basename := pathpkg.Base(strings.ReplaceAll(strings.TrimSpace(file.RelativePath), "\\", "/"))
			if basename == "." || basename == "/" || basename == "" || strings.ContainsAny(basename, "\x00\r\n") {
				continue
			}
			payload.Files = append(payload.Files, aiprovider.TitleRewriteFile{Index: len(payload.Files), Basename: basename})
			if len(payload.Files) == 32 {
				break
			}
		}
	}
	rewrite, err := assist.GenerateTitleRewrite(ctx, payload)
	if err != nil {
		return tmdb.Match{}, mediarecognition.ReasonNoMatch, err
	}
	if rewrite.Action != "search" {
		return tmdb.Match{}, mediarecognition.ReasonNoMatch, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	queries := make([]mediaRecognitionQuery, 0, len(rewrite.SearchQueries))
	for index, query := range rewrite.SearchQueries {
		language := request.Language
		if query.LanguageHint != "" && query.LanguageHint != "unknown" && query.LanguageHint != "original" {
			language = query.LanguageHint
		}
		queries = append(queries, mediaRecognitionQuery{Title: query.Title, MediaType: query.MediaType, Year: cloneInt(query.Year), Language: language, Phase: "ai_rewrite", Order: index})
	}
	candidates, err := collectRecognitionCandidates(ctx, candidateLookup, queries, request.Language, request.Region)
	if err != nil || len(candidates) == 0 {
		if err == nil {
			err = &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
		}
		return tmdb.Match{}, mediarecognition.ReasonNoMatch, err
	}
	rewritten := parsed
	rewritten.CanonicalTitle = strings.TrimSpace(rewrite.PrimaryTitle)
	rewritten.Year = cloneInt(rewrite.Year)
	rewritten.Queries = make([]mediarecognition.QueryVariant, 0, len(rewrite.SearchQueries))
	for index, query := range rewrite.SearchQueries {
		rewritten.Queries = append(rewritten.Queries, mediarecognition.QueryVariant{Title: query.Title, Year: cloneInt(query.Year), SuggestedType: mediarecognition.MediaType(query.MediaType), Source: "ai_rewrite", Reason: "ai_normalized_query", Order: index})
	}
	if rewrite.MediaType == "movie" || rewrite.MediaType == "tv" {
		rewritten.SuggestedType = mediarecognition.MediaType(rewrite.MediaType)
	}
	rewritten.Season = cloneInt(rewrite.Season)
	rewritten.Episodes.EpisodeMin = cloneInt(rewrite.EpisodeStart)
	rewritten.Episodes.EpisodeMax = cloneInt(rewrite.EpisodeEnd)
	remote := make([]mediarecognition.RemoteCandidate, 0, len(candidates))
	byKey := make(map[string]tmdb.Candidate, len(candidates))
	for _, candidate := range candidates {
		key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
		byKey[key] = candidate
		remote = append(remote, remoteRecognitionCandidate(candidate))
	}
	decision := mediarecognition.Rank(rewritten, remote)
	if decision.Match == nil {
		return tmdb.Match{}, decision.Reason, &tmdb.ClientError{Code: tmdb.ErrorNoMatch}
	}
	best := *decision.Match
	match, err := lookup.GetByID(ctx, string(best.MediaType), best.ID, request.Language)
	if err != nil {
		return tmdb.Match{}, decision.Reason, err
	}
	match.Confidence = decision.Confidence
	if match.Snapshot.OriginalTitle == "" {
		match.Snapshot.OriginalTitle = best.OriginalTitle
	}
	if match.OriginalLanguage == "" {
		candidate := byKey[fmt.Sprintf("%s:%d", best.MediaType, best.ID)]
		match.OriginalLanguage = candidate.OriginalLanguage
		match.Snapshot.OriginalLanguage = candidate.OriginalLanguage
	}
	return match, decision.Reason, nil
}

func collectRecognitionCandidates(ctx context.Context, candidateLookup mediaRecognitionCandidateLookup, queries []mediaRecognitionQuery, defaultLanguage, region string) ([]tmdb.Candidate, error) {
	items := make(map[string]tmdb.Candidate)
	order := make([]string, 0, 32)
	for index, query := range queries {
		if index >= mediaRecognitionMaxQueries {
			break
		}
		language := defaultLanguage
		if query.Language != "" {
			language = query.Language
		}
		batch, err := candidateLookup.SearchCandidates(ctx, query.MediaType, query.Title, query.Year, language, region, 10)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			if tmdb.ErrorCode(err) == tmdb.ErrorNoMatch {
				continue
			}
			return nil, err
		}
		for _, candidate := range batch {
			key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
			if _, exists := items[key]; !exists {
				order = append(order, key)
			}
			items[key] = candidate
		}
	}
	result := make([]tmdb.Candidate, 0, len(order))
	for _, key := range order {
		result = append(result, items[key])
	}
	return result, nil
}

func safeAICandidateAliases(candidate mediarecognition.RemoteCandidate) []string {
	values := append([]string(nil), candidate.AlternativeTitles...)
	values = append(values, candidate.Translations...)
	result := make([]string, 0, min(len(values), 5))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len([]rune(value)) > 256 || strings.ContainsAny(value, "\x00\r\n") {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
		if len(result) == 5 {
			break
		}
	}
	return result
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
	remote := mediarecognition.RemoteCandidate{ID: candidate.ID, MediaType: mediarecognition.MediaType(candidate.MediaType), Title: candidate.Title, OriginalTitle: candidate.OriginalTitle, OriginalLanguage: candidate.OriginalLanguage, AlternativeTitles: append([]string(nil), candidate.AlternativeTitles...), Translations: append([]string(nil), candidate.Translations...), ReleaseYear: cloneInt(candidate.ReleaseYear), SeasonYears: cloneSeasonYears(candidate.SeasonYears), Popularity: candidate.Popularity, VoteCount: candidate.VoteCount, HasPoster: strings.TrimSpace(candidate.PosterPath) != ""}
	if candidate.SeasonCount > 0 {
		remote.SeasonCount = cloneInt(&candidate.SeasonCount)
	}
	if candidate.EpisodeCount > 0 {
		remote.EpisodeCount = cloneInt(&candidate.EpisodeCount)
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
	case mediarecognition.ReasonExtremeLowConfidence:
		return tmdb.ErrorNoMatch
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
