package mediarecognition

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// DefaultScoreConfig is versioned with the frozen v1 corpus. The defaults are
// intentionally centralized: future threshold changes must be accompanied by
// a benchmark report instead of scattered magic confidence values.
func DefaultScoreConfig() ScoreConfig {
	return ScoreConfig{
		TitleWeight:       .68,
		YearExact:         .12,
		YearNear:          .06,
		YearConflict:      .24,
		TypeWeight:        .10,
		TypeConflict:      .22,
		SeasonWeight:      .03,
		StructureWeight:   .05,
		ConsistencyWeight: .03,
		UniquenessWeight:  .05,
		PopularityWeight:  .01,
		MatchThreshold:    .78,
		ConflictMargin:    .06,
		HanEquivalence:    BuiltInHanEquivalence,
	}
}

func Rank(parsed ParsedFacts, candidates []RemoteCandidate) Decision {
	return RankWithConfig(parsed, candidates, DefaultScoreConfig())
}

func RankWithConfig(parsed ParsedFacts, candidates []RemoteCandidate, config ScoreConfig) Decision {
	decision := Decision{Status: DecisionUnrecognized, Reason: ReasonNoMatch, Ranked: []RankedCandidate{}, Diagnostics: []Diagnostic{}}
	if len(candidates) == 0 {
		addDiagnostic(&decision.Diagnostics, "no_remote_candidates", "warning", "remote retrieval returned no candidates")
		return decision
	}
	if len(candidates) > MaxRemoteCandidates {
		candidates = candidates[:MaxRemoteCandidates]
		addDiagnostic(&decision.Diagnostics, "remote_candidates_truncated", "warning", fmt.Sprintf("candidate set was limited to %d items", MaxRemoteCandidates))
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = boundedRemoteCandidate(candidate)
		key := fmt.Sprintf("%s:%d", candidate.MediaType, candidate.ID)
		if _, exists := seen[key]; exists || !validRemoteCandidate(candidate) {
			continue
		}
		seen[key] = struct{}{}
		decision.Ranked = append(decision.Ranked, scoreCandidate(parsed, candidate, config))
	}
	if len(decision.Ranked) == 0 {
		addDiagnostic(&decision.Diagnostics, "no_valid_candidates", "warning", "all remote candidates failed bounded identity validation")
		return decision
	}

	applyUniqueness(decision.Ranked, config)
	sort.SliceStable(decision.Ranked, func(left, right int) bool {
		if decision.Ranked[left].Score.Total != decision.Ranked[right].Score.Total {
			return decision.Ranked[left].Score.Total > decision.Ranked[right].Score.Total
		}
		if decision.Ranked[left].Candidate.MediaType != decision.Ranked[right].Candidate.MediaType {
			return decision.Ranked[left].Candidate.MediaType < decision.Ranked[right].Candidate.MediaType
		}
		return decision.Ranked[left].Candidate.ID < decision.Ranked[right].Candidate.ID
	})
	if len(decision.Ranked) > MaxRankedCandidates {
		decision.Ranked = decision.Ranked[:MaxRankedCandidates]
		addDiagnostic(&decision.Diagnostics, "ranked_diagnostics_truncated", "info", fmt.Sprintf("ranked diagnostics were limited to %d candidates", MaxRankedCandidates))
	}
	best := decision.Ranked[0]
	decision.Confidence = best.Score.Total
	if len(decision.Ranked) > 1 {
		decision.RunnerUpGap = clamp01(best.Score.Total - decision.Ranked[1].Score.Total)
	}
	if best.Score.Total < config.MatchThreshold {
		decision.Reason = ReasonLowConfidence
		addDiagnostic(&decision.Diagnostics, "automatic_threshold_not_met", "warning", "best candidate did not meet the corpus-calibrated automatic threshold")
		return decision
	}
	if len(decision.Ranked) > 1 && decision.RunnerUpGap < config.ConflictMargin && decision.Ranked[1].Score.Total >= config.MatchThreshold-config.ConflictMargin {
		decision.Reason = ReasonCandidateConflict
		addDiagnostic(&decision.Diagnostics, "candidate_margin_too_small", "warning", "top candidates remain too close after all bounded evidence")
		return decision
	}
	decision.Status = DecisionMatched
	decision.Reason = ReasonMatched
	match := best.Candidate
	decision.Match = &match
	addDiagnostic(&decision.Diagnostics, "automatic_match", "info", "one candidate passed both confidence and uniqueness gates")
	return decision
}

func validRemoteCandidate(candidate RemoteCandidate) bool {
	if candidate.ID <= 0 || candidate.MediaType != MediaTypeMovie && candidate.MediaType != MediaTypeTV {
		return false
	}
	if comparisonKey(candidate.Title) == "" && comparisonKey(candidate.OriginalTitle) == "" {
		return false
	}
	return len(candidate.AlternativeTitles) <= 64 && len(candidate.Translations) <= 64
}

func boundedRemoteCandidate(candidate RemoteCandidate) RemoteCandidate {
	candidate.Title = boundedText(candidate.Title, 256)
	candidate.OriginalTitle = boundedText(candidate.OriginalTitle, 256)
	candidate.AlternativeTitles = boundedStringSlice(candidate.AlternativeTitles, 32, 256)
	candidate.Translations = boundedStringSlice(candidate.Translations, 32, 256)
	if math.IsNaN(candidate.Popularity) || math.IsInf(candidate.Popularity, 0) || candidate.Popularity < 0 {
		candidate.Popularity = 0
	}
	if candidate.Popularity > 1_000_000 {
		candidate.Popularity = 1_000_000
	}
	if candidate.ReleaseYear != nil && (*candidate.ReleaseYear < 1888 || *candidate.ReleaseYear > 2500) {
		candidate.ReleaseYear = nil
	} else {
		candidate.ReleaseYear = cloneDomainInt(candidate.ReleaseYear)
	}
	if candidate.SeasonCount != nil && (*candidate.SeasonCount < 0 || *candidate.SeasonCount > 1000) {
		candidate.SeasonCount = nil
	} else {
		candidate.SeasonCount = cloneDomainInt(candidate.SeasonCount)
	}
	return candidate
}

func boundedStringSlice(values []string, maximum, runeLimit int) []string {
	if len(values) > maximum {
		values = values[:maximum]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = boundedText(value, runeLimit)
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func scoreCandidate(parsed ParsedFacts, candidate RemoteCandidate, config ScoreConfig) RankedCandidate {
	ranked := RankedCandidate{Candidate: candidate, Evidence: []Evidence{}}
	aliases := candidateNames(candidate)
	bestSimilarity := 0.0
	strongVariantMatches := 0
	for _, query := range parsed.Queries {
		queryBest := 0.0
		for _, alias := range aliases {
			queryBest = maxFloat(queryBest, TitleSimilarity(query.Title, alias, config.HanEquivalence))
		}
		bestSimilarity = maxFloat(bestSimilarity, queryBest)
		if queryBest >= .90 {
			strongVariantMatches++
		}
	}
	if len(parsed.Queries) == 0 && parsed.CanonicalTitle != "" {
		for _, alias := range aliases {
			bestSimilarity = maxFloat(bestSimilarity, TitleSimilarity(parsed.CanonicalTitle, alias, config.HanEquivalence))
		}
	}
	ranked.Score.TitleSimilarity = bestSimilarity
	ranked.Score.Title = config.TitleWeight * bestSimilarity
	ranked.Evidence = append(ranked.Evidence, Evidence{Code: "best_title_similarity", Kind: "title", Strength: bestSimilarity, Summary: "best normalized localized/original/alternative/translation title similarity"})

	if parsed.Year != nil && candidate.ReleaseYear != nil {
		difference := absoluteInt(*parsed.Year - *candidate.ReleaseYear)
		switch difference {
		case 0:
			ranked.Score.Year = config.YearExact
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "year_exact", Kind: "year", Strength: 1, Summary: "release year exactly matches parsed facts"})
		case 1:
			ranked.Score.Year = config.YearNear
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "year_near", Kind: "year", Strength: .5, Summary: "release year is within the bounded one-year tolerance"})
		default:
			ranked.Score.ConflictPenalty += config.YearConflict
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "year_conflict", Kind: "year", Strength: 1, Conflict: true, Summary: "release year conflicts with parsed facts"})
		}
	}
	if parsed.SuggestedType != MediaTypeUnknown {
		strength := clamp01(parsed.TypeConfidence)
		if candidate.MediaType == parsed.SuggestedType {
			ranked.Score.MediaType = config.TypeWeight * strength
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "type_supported", Kind: "type", Supports: candidate.MediaType, Strength: strength, Summary: "candidate type agrees with structural evidence"})
			if strength >= .80 {
				ranked.Score.Structure = config.StructureWeight * strength
			}
		} else if strength >= .80 {
			ranked.Score.ConflictPenalty += config.TypeConflict * strength
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "type_conflict", Kind: "type", Supports: parsed.SuggestedType, Strength: strength, Conflict: true, Summary: "candidate type conflicts with strong structural evidence"})
		}
	}
	if parsed.Season != nil && candidate.MediaType == MediaTypeTV && candidate.SeasonCount != nil && *parsed.Season <= *candidate.SeasonCount {
		ranked.Score.Season = config.SeasonWeight
		ranked.Evidence = append(ranked.Evidence, Evidence{Code: "season_available", Kind: "season", Supports: MediaTypeTV, Strength: 1, Summary: "parsed season exists in the candidate season range"})
	}
	if strongVariantMatches >= 2 {
		strength := clamp01(float64(strongVariantMatches-1) / 3)
		ranked.Score.Consistency = config.ConsistencyWeight * strength
		ranked.Evidence = append(ranked.Evidence, Evidence{Code: "multi_source_consistency", Kind: "title", Strength: strength, Summary: "multiple generated title variants support the same candidate"})
	}
	if candidate.Popularity > 0 {
		strength := clamp01(math.Log1p(candidate.Popularity) / math.Log(1001))
		ranked.Score.Popularity = config.PopularityWeight * strength
	}
	updateTotal(&ranked.Score)
	return ranked
}

func applyUniqueness(ranked []RankedCandidate, config ScoreConfig) {
	for index := range ranked {
		second := 0.0
		for other := range ranked {
			if index == other {
				continue
			}
			second = maxFloat(second, ranked[other].Score.TitleSimilarity)
		}
		gap := clamp01(ranked[index].Score.TitleSimilarity - second)
		ranked[index].Score.Uniqueness = config.UniquenessWeight * gap
		if gap > 0 {
			ranked[index].Evidence = append(ranked[index].Evidence, Evidence{Code: "title_uniqueness", Kind: "uniqueness", Strength: gap, Summary: "title evidence is more specific than competing candidates"})
		}
		updateTotal(&ranked[index].Score)
	}
}

func updateTotal(score *ScoreBreakdown) {
	score.Total = clamp01(score.Title + score.Year + score.MediaType + score.Season + score.Structure + score.Consistency + score.Uniqueness + score.Popularity - score.ConflictPenalty)
}

func candidateNames(candidate RemoteCandidate) []string {
	result := make([]string, 0, 2+len(candidate.AlternativeTitles)+len(candidate.Translations))
	values := make([]string, 0, cap(result))
	values = append(values, candidate.Title, candidate.OriginalTitle)
	values = append(values, candidate.AlternativeTitles...)
	values = append(values, candidate.Translations...)
	for _, value := range values {
		value = boundedText(value, MaxPackageRunes)
		if strings.TrimSpace(value) != "" {
			result = appendUniqueBounded(result, []string{value}, 64)
		}
	}
	return result
}

func absoluteInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
