package services

import (
	"context"
	"errors"
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
	mediaRecognitionCredentialMissing  = "tmdb_credential_unavailable"
)

type mediaRecognitionLookup interface {
	Search(context.Context, string, string, *int, string, string) (tmdb.Match, error)
	GetByID(context.Context, string, int64, string) (tmdb.Match, error)
}

// MediaRecognitionRequest contains only provider-neutral names and Profile
// snapshots. Absolute paths, provider IDs and credentials never enter the
// recognizer.
type MediaRecognitionRequest struct {
	PackageName      string
	Files            []recognitionSourceFile
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
	candidates := recognitionCandidatesFromSources(processedSources, request.RecognitionRules)
	if len(candidates) > 0 {
		result.Title = candidates[0].Title
		result.MediaType = candidates[0].MediaType
		result.ReleaseYear = cloneInt(candidates[0].Year)
	}
	if directHint != nil {
		result.MediaType = directHint.MediaType
		result.SeasonHint = cloneInt(directHint.Season)
		result.EpisodeHint = cloneInt(directHint.Episode)
	}
	if lookup == nil {
		result.ErrorCode = mediaRecognitionCredentialMissing
		return result
	}

	var (
		match tmdb.Match
		err   error
	)
	if directHint != nil {
		match, err = lookup.GetByID(ctx, directHint.MediaType, int64(directHint.TMDBID), request.Language)
	} else if len(candidates) == 0 {
		result.ErrorCode = tmdb.ErrorInvalidRequest
		return result
	} else {
		for _, candidate := range candidates {
			match, err = lookup.Search(ctx, candidate.MediaType, candidate.Title, candidate.Year, request.Language, request.Region)
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
	if match.Confidence < .80 {
		result.ErrorCode = mediaRecognitionLowConfidence
		return result
	}
	result.Status = mediaRecognitionStatusMatched
	return result
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
