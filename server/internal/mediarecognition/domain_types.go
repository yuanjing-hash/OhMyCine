package mediarecognition

import (
	"errors"
)

const EngineVersion = "nextgen-domain-v8"

const (
	MaxPackageRunes     = 512
	MaxRelativeRunes    = 1024
	MaxInputFiles       = 512
	MaxQueryVariants    = 32
	MaxDiagnostics      = 64
	MaxRemoteCandidates = 100
	MaxRankedCandidates = 20
)

var (
	ErrUnsafeRecognitionInput = errors.New("unsafe media recognition input")
	ErrRecognitionInputBounds = errors.New("media recognition input exceeds bounds")
)

type MediaType string

const (
	MediaTypeUnknown MediaType = ""
	MediaTypeMovie   MediaType = "movie"
	MediaTypeTV      MediaType = "tv"
)

type SourceKind string

const (
	SourceUnknown     SourceKind = "unknown"
	SourceDownload    SourceKind = "download"
	SourceLibraryScan SourceKind = "library_scan"
)

// InputFacts is deliberately provider-neutral. RelativePath must be relative
// to the provider/library boundary and must never contain provider IDs, URLs,
// credentials, or host filesystem paths.
type InputFacts struct {
	PackageName   string         `json:"package_name"`
	SourceKind    SourceKind     `json:"source_kind,omitempty"`
	Files         []FileFact     `json:"files,omitempty"`
	MediaTypeHint MediaType      `json:"media_type_hint,omitempty"`
	YearHint      *int           `json:"year_hint,omitempty"`
	SeasonHint    *int           `json:"season_hint,omitempty"`
	EpisodeHint   *int           `json:"episode_hint,omitempty"`
	PreparedNames []PreparedName `json:"prepared_names,omitempty"`
	DirectHint    *IdentityHint  `json:"direct_hint,omitempty"`
}

type FileFact struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size,omitempty"`
}

// PreparedName carries the output of a Profile/built-in word processor without
// coupling the pure parser to Profile persistence or provider adapters.
type PreparedName struct {
	Value        string   `json:"value"`
	Source       string   `json:"source,omitempty"`
	AppliedRules []string `json:"applied_rules,omitempty"`
}

// IdentityHint is lookup guidance only. An orchestration layer must retrieve
// and validate the authoritative item before accepting it.
type IdentityHint struct {
	Provider  string    `json:"provider"`
	ID        int64     `json:"id"`
	MediaType MediaType `json:"media_type,omitempty"`
}

type ParsedFacts struct {
	EngineVersion  string         `json:"engine_version"`
	CanonicalTitle string         `json:"canonical_title"`
	Titles         []TitleFact    `json:"titles"`
	Year           *int           `json:"year,omitempty"`
	Season         *int           `json:"season,omitempty"`
	SeasonYear     *int           `json:"season_year,omitempty"`
	Episodes       EpisodeFacts   `json:"episodes"`
	Specifications []string       `json:"specifications,omitempty"`
	ReleaseGroup   string         `json:"release_group,omitempty"`
	Structure      StructureFacts `json:"structure"`
	SuggestedType  MediaType      `json:"suggested_type,omitempty"`
	TypeConfidence float64        `json:"type_confidence,omitempty"`
	DirectHint     *IdentityHint  `json:"direct_hint,omitempty"`
	TypeEvidence   []Evidence     `json:"type_evidence,omitempty"`
	Queries        []QueryVariant `json:"queries"`
	Diagnostics    []Diagnostic   `json:"diagnostics"`
}

type TitleFact struct {
	Value  string `json:"value"`
	Source string `json:"source"`
	Stage  string `json:"stage"`
}

type EpisodeFacts struct {
	Count      int  `json:"count"`
	SeasonMin  *int `json:"season_min,omitempty"`
	SeasonMax  *int `json:"season_max,omitempty"`
	EpisodeMin *int `json:"episode_min,omitempty"`
	EpisodeMax *int `json:"episode_max,omitempty"`
}

type StructureFacts struct {
	FileCount       int  `json:"file_count"`
	VideoCount      int  `json:"video_count"`
	HasSeasonFolder bool `json:"has_season_folder,omitempty"`
	HasBDMV         bool `json:"has_bdmv,omitempty"`
	HasVideoTS      bool `json:"has_video_ts,omitempty"`
	HasDiscStack    bool `json:"has_disc_stack,omitempty"`
	HasExtras       bool `json:"has_extras,omitempty"`
}

type QueryVariant struct {
	Title         string    `json:"title"`
	Year          *int      `json:"year,omitempty"`
	SeasonYear    *int      `json:"season_year,omitempty"`
	SuggestedType MediaType `json:"suggested_type,omitempty"`
	Source        string    `json:"source"`
	Reason        string    `json:"reason"`
	Order         int       `json:"order"`
}

type Evidence struct {
	Code     string    `json:"code"`
	Kind     string    `json:"kind"`
	Supports MediaType `json:"supports,omitempty"`
	Strength float64   `json:"strength"`
	Conflict bool      `json:"conflict,omitempty"`
	Summary  string    `json:"summary,omitempty"`
}

type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

type RemoteCandidate struct {
	ID                int64       `json:"id"`
	MediaType         MediaType   `json:"media_type"`
	Title             string      `json:"title"`
	OriginalTitle     string      `json:"original_title,omitempty"`
	AlternativeTitles []string    `json:"alternative_titles,omitempty"`
	Translations      []string    `json:"translations,omitempty"`
	ReleaseYear       *int        `json:"release_year,omitempty"`
	SeasonCount       *int        `json:"season_count,omitempty"`
	SeasonYears       map[int]int `json:"season_years,omitempty"`
	Popularity        float64     `json:"popularity,omitempty"`
}

type ScoreBreakdown struct {
	TitleSimilarity float64 `json:"title_similarity"`
	Title           float64 `json:"title"`
	Year            float64 `json:"year"`
	MediaType       float64 `json:"media_type"`
	Season          float64 `json:"season"`
	Structure       float64 `json:"structure"`
	Consistency     float64 `json:"consistency"`
	Uniqueness      float64 `json:"uniqueness"`
	Popularity      float64 `json:"popularity"`
	ConflictPenalty float64 `json:"conflict_penalty"`
	Total           float64 `json:"total"`
}

type RankedCandidate struct {
	Candidate RemoteCandidate `json:"candidate"`
	Score     ScoreBreakdown  `json:"score"`
	Evidence  []Evidence      `json:"evidence"`
}

type DecisionStatus string
type DecisionReason string

const (
	DecisionMatched      DecisionStatus = "matched"
	DecisionUnrecognized DecisionStatus = "unrecognized"

	ReasonMatched           DecisionReason = "matched"
	ReasonNoMatch           DecisionReason = "no_match"
	ReasonLowConfidence     DecisionReason = "low_confidence"
	ReasonCandidateConflict DecisionReason = "candidate_conflict"
)

type Decision struct {
	Status      DecisionStatus    `json:"status"`
	Reason      DecisionReason    `json:"reason"`
	Match       *RemoteCandidate  `json:"match,omitempty"`
	Confidence  float64           `json:"confidence,omitempty"`
	RunnerUpGap float64           `json:"runner_up_gap,omitempty"`
	Ranked      []RankedCandidate `json:"ranked,omitempty"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
}

type ScoreConfig struct {
	TitleWeight         float64
	YearExact           float64
	YearNear            float64
	YearConflict        float64
	TypeWeight          float64
	TypeConflict        float64
	SeasonWeight        float64
	StructureWeight     float64
	ConsistencyWeight   float64
	UniquenessWeight    float64
	PopularityWeight    float64
	MatchThreshold      float64
	ExactTitleThreshold float64
	TypoTitleThreshold  float64
	TypoMatchThreshold  float64
	ConflictMargin      float64
	HanEquivalence      HanEquivalence
}
