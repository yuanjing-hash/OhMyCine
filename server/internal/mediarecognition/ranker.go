package mediarecognition

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// DefaultScoreConfig is versioned with the frozen v1 corpus. The defaults are
// intentionally centralized: future threshold changes must be accompanied by
// a benchmark report instead of scattered magic confidence values.
func DefaultScoreConfig() ScoreConfig {
	return ScoreConfig{
		TitleWeight:         .68,
		YearExact:           .12,
		YearNear:            .06,
		YearConflict:        .24,
		TypeWeight:          .10,
		TypeConflict:        .22,
		SeasonWeight:        .03,
		EpisodeWeight:       .04,
		EpisodeConflict:     .20,
		StructureWeight:     .05,
		ConsistencyWeight:   .03,
		UniquenessWeight:    .05,
		PopularityWeight:    .01,
		MatchThreshold:      .78,
		ExactTitleThreshold: .68,
		TypoTitleThreshold:  .90,
		TypoMatchThreshold:  .64,
		ConflictMargin:      .06,
		HanEquivalence:      BuiltInHanEquivalence,
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
	threshold := config.MatchThreshold
	exactIdentity := best.Score.TitleSimilarity == 1
	typoIdentity := !exactIdentity && best.Score.TitleSimilarity >= config.TypoTitleThreshold && conservativeLatinTypoMatch(parsed, best.Candidate, config.HanEquivalence)
	if exactIdentity && config.ExactTitleThreshold > 0 && config.ExactTitleThreshold < threshold {
		threshold = config.ExactTitleThreshold
	} else if typoIdentity && config.TypoMatchThreshold > 0 && config.TypoMatchThreshold < threshold {
		threshold = config.TypoMatchThreshold
	}
	if best.Score.Total < threshold {
		decision.Reason = ReasonLowConfidence
		addDiagnostic(&decision.Diagnostics, "automatic_threshold_not_met", "warning", "best candidate did not meet the corpus-calibrated automatic threshold")
		return decision
	}
	identityConflict := len(decision.Ranked) > 1 && (!exactIdentity || decision.Ranked[1].Score.TitleSimilarity == 1)
	if identityConflict && exactIdentity && strongStructuredTypeDisambiguation(parsed, best, decision.Ranked[1]) {
		identityConflict = false
	}
	if identityConflict && decision.RunnerUpGap < config.ConflictMargin && decision.Ranked[1].Score.Total >= threshold-config.ConflictMargin {
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

func strongStructuredTypeDisambiguation(parsed ParsedFacts, best, runnerUp RankedCandidate) bool {
	return parsed.SuggestedType != MediaTypeUnknown && parsed.TypeConfidence >= .80 &&
		best.Candidate.MediaType == parsed.SuggestedType && runnerUp.Candidate.MediaType != parsed.SuggestedType
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
	if candidate.EpisodeCount != nil && (*candidate.EpisodeCount <= 0 || *candidate.EpisodeCount > 1_000_000) {
		candidate.EpisodeCount = nil
	} else {
		candidate.EpisodeCount = cloneDomainInt(candidate.EpisodeCount)
	}
	candidate.SeasonYears = boundedSeasonYears(candidate.SeasonYears)
	return candidate
}

func boundedSeasonYears(values map[int]int) map[int]int {
	if len(values) == 0 {
		return nil
	}
	result := make(map[int]int, minInt(len(values), 200))
	for season, year := range values {
		if len(result) == 200 {
			break
		}
		if season >= 0 && season <= 200 && year >= 1888 && year <= 2200 {
			result[season] = year
		}
	}
	return result
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
		if recallOnlyQuery(query) {
			continue
		}
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

	if parsed.SeasonYear != nil && parsed.Season != nil && candidate.MediaType == MediaTypeTV {
		// A year next to Sxx is ambiguous in real release names: some groups
		// publish the season air year (Ai qing gong yu 2012 S03), while others
		// keep the series premiere year (Game of Thrones 2011 S03). Accept the
		// stronger matching interpretation and reject only when both known facts
		// conflict, instead of hard-coding either naming convention.
		if seasonYear, exists := candidate.SeasonYears[*parsed.Season]; exists {
			seasonDifference := absoluteInt(*parsed.SeasonYear - seasonYear)
			if candidate.ReleaseYear != nil && absoluteInt(*parsed.SeasonYear-*candidate.ReleaseYear) < seasonDifference {
				scoreYearEvidence(&ranked, *parsed.SeasonYear, *candidate.ReleaseYear, config, "series_year_with_season")
			} else {
				scoreYearEvidence(&ranked, *parsed.SeasonYear, seasonYear, config, "season_year")
			}
		} else if candidate.ReleaseYear != nil {
			scoreYearEvidence(&ranked, *parsed.SeasonYear, *candidate.ReleaseYear, config, "series_year_with_season")
		}
	} else if parsed.Year != nil && candidate.ReleaseYear != nil {
		scoreYearEvidence(&ranked, *parsed.Year, *candidate.ReleaseYear, config, "year")
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
	if parsed.SuggestedType == MediaTypeTV && parsed.TypeConfidence >= .80 && candidate.MediaType == MediaTypeTV && parsed.Episodes.EpisodeMax != nil && candidate.EpisodeCount != nil {
		strength := clamp01(parsed.TypeConfidence)
		if *parsed.Episodes.EpisodeMax <= *candidate.EpisodeCount {
			ranked.Score.Episode = config.EpisodeWeight * strength
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "episode_available", Kind: "episode", Supports: MediaTypeTV, Strength: strength, Summary: "parsed episode exists within the candidate's known total episode range"})
		} else {
			ranked.Score.ConflictPenalty += config.EpisodeConflict * strength
			ranked.Evidence = append(ranked.Evidence, Evidence{Code: "episode_outside_known_range", Kind: "episode", Supports: MediaTypeTV, Strength: strength, Conflict: true, Summary: "parsed episode exceeds the candidate's known total episode range"})
		}
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

func recallOnlyQuery(query QueryVariant) bool {
	return query.Reason == "latin_token_fallback"
}

func conservativeLatinTypoMatch(parsed ParsedFacts, candidate RemoteCandidate, han HanEquivalence) bool {
	queries := parsed.Queries
	if len(queries) == 0 && parsed.CanonicalTitle != "" {
		queries = []QueryVariant{{Title: parsed.CanonicalTitle}}
	}
	for _, query := range queries {
		if recallOnlyQuery(query) {
			continue
		}
		for _, alias := range candidateNames(candidate) {
			if boundedLatinTypoPair(query.Title, alias, han) {
				return true
			}
		}
	}
	return false
}

func boundedLatinTypoPair(left, right string, han HanEquivalence) bool {
	leftKey, rightKey := comparisonKeyWith(left, han), comparisonKeyWith(right, han)
	leftRunes, rightRunes := []rune(leftKey), []rune(rightKey)
	if leftKey == rightKey || len(leftRunes) < 10 || len(rightRunes) < 10 || absoluteInt(len(leftRunes)-len(rightRunes)) > 1 {
		return false
	}
	for _, values := range [][]rune{leftRunes, rightRunes} {
		for _, r := range values {
			if unicode.IsDigit(r) {
				continue
			}
			if !unicode.IsLetter(r) || !unicode.In(r, unicode.Latin) {
				return false
			}
		}
	}
	if levenshteinRunes(leftRunes, rightRunes) == 1 {
		return true
	}
	if len(leftRunes) != len(rightRunes) {
		return false
	}
	differences := make([]int, 0, 2)
	for index := range leftRunes {
		if leftRunes[index] != rightRunes[index] {
			differences = append(differences, index)
			if len(differences) > 2 {
				return false
			}
		}
	}
	return len(differences) == 2 && differences[1] == differences[0]+1 && leftRunes[differences[0]] == rightRunes[differences[1]] && leftRunes[differences[1]] == rightRunes[differences[0]]
}

func scoreYearEvidence(ranked *RankedCandidate, parsedYear, candidateYear int, config ScoreConfig, prefix string) {
	difference := absoluteInt(parsedYear - candidateYear)
	switch difference {
	case 0:
		ranked.Score.Year = config.YearExact
		ranked.Evidence = append(ranked.Evidence, Evidence{Code: prefix + "_exact", Kind: "year", Strength: 1, Summary: "release year exactly matches parsed facts"})
	case 1:
		ranked.Score.Year = config.YearNear
		ranked.Evidence = append(ranked.Evidence, Evidence{Code: prefix + "_near", Kind: "year", Strength: .5, Summary: "release year is within the bounded one-year tolerance"})
	default:
		ranked.Score.ConflictPenalty += config.YearConflict
		ranked.Evidence = append(ranked.Evidence, Evidence{Code: prefix + "_conflict", Kind: "year", Strength: 1, Conflict: true, Summary: "release year conflicts with parsed facts"})
	}
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
	score.Total = clamp01(score.Title + score.Year + score.MediaType + score.Season + score.Episode + score.Structure + score.Consistency + score.Uniqueness + score.Popularity - score.ConflictPenalty)
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
			result = appendUniqueBounded(result, candidateSubtitleAliases(value), 64)
		}
	}
	return result
}

// candidateSubtitleAliases exposes an explicit colon-delimited subtitle as a
// ranking alias. TMDB commonly stores franchise films as "Series: Subtitle"
// while a release site publishes only the distinctive subtitle. Requiring at
// least two words prevents a broad one-token suffix from becoming an unsafe
// automatic identity shortcut.
func candidateSubtitleAliases(value string) []string {
	for _, separator := range []string{":", "："} {
		if index := strings.LastIndex(value, separator); index >= 0 {
			suffix := boundedText(value[index+len(separator):], MaxPackageRunes)
			if len(strings.Fields(suffix)) >= 2 && meaningfulTitle(suffix) {
				return []string{suffix}
			}
		}
	}
	return nil
}

func absoluteInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
