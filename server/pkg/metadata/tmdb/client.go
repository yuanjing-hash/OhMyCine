package tmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAPIBaseURL       = "https://api.tmdb.org/3"
	FallbackAPIBaseURL      = "https://api.themoviedb.org/3"
	DefaultImageBaseURL     = "https://image.tmdb.org/t/p"
	maxResponseBytes        = 2 << 20
	maxImageTestBytes       = 1 << 20
	testImagePath           = "/w92/pB8BM7pdSp6B6Ih7QZ4DrQ3PmJK.jpg"
	ErrorAuthFailed         = "tmdb_auth_failed"
	ErrorNetworkUnavailable = "tmdb_network_unavailable"
	ErrorNoMatch            = "tmdb_no_match"
	ErrorInvalidResponse    = "tmdb_invalid_response"
	ErrorRequestFailed      = "tmdb_request_failed"
	ErrorInvalidRequest     = "tmdb_invalid_request"
	ErrorImageInvalid       = "tmdb_image_invalid"
)

type ClientError struct {
	Code  string
	cause error
}

func (e *ClientError) Error() string             { return e.Code }
func (e *ClientError) Unwrap() error             { return e.cause }
func clientError(code string, cause error) error { return &ClientError{Code: code, cause: cause} }
func ErrorCode(err error) string {
	var target *ClientError
	if errors.As(err, &target) {
		return target.Code
	}
	var network *networkRequestError
	if errors.As(err, &network) {
		return ErrorNetworkUnavailable
	}
	return ErrorInvalidResponse
}

// Builtin credentials are empty in source. Official builds may inject exactly
// one revocable read-only application credential with Go linker -X.
var BuiltinReadAccessToken string
var BuiltinAPIKey string

type CredentialKind string

const (
	CredentialKindReadAccessToken CredentialKind = "read_access_token"
	CredentialKindAPIKey          CredentialKind = "api_key"
)

type Credential struct {
	Kind  CredentialKind
	Value string
}

type Client struct {
	credential  Credential
	http        *http.Client
	apiBase     string
	fallbackAPI string
	imageBase   string
}
type Match struct {
	ID                  int64
	Title               string
	MediaType           string
	GenreIDs            []int
	OriginalLanguage    string
	ProductionCountries []string
	OriginCountries     []string
	ReleaseYear         *int
	Confidence          float64
	Snapshot            Snapshot
}

// Snapshot is the credential-free, deterministic TMDB projection persisted by
// the media pipeline. Image fields contain TMDB file identities only, never a
// configured image origin or a temporary/tokenized URL.
type Snapshot struct {
	Version             int              `json:"version"`
	TMDBID              int64            `json:"tmdb_id"`
	IMDbID              string           `json:"imdb_id,omitempty"`
	MediaType           string           `json:"media_type"`
	Title               string           `json:"title"`
	OriginalTitle       string           `json:"original_title,omitempty"`
	ReleaseDate         string           `json:"release_date,omitempty"`
	Overview            string           `json:"overview,omitempty"`
	Tagline             string           `json:"tagline,omitempty"`
	Status              string           `json:"status,omitempty"`
	VoteAverage         float64          `json:"vote_average,omitempty"`
	VoteCount           int              `json:"vote_count,omitempty"`
	RuntimeMinutes      int              `json:"runtime_minutes,omitempty"`
	SeasonCount         int              `json:"season_count,omitempty"`
	EpisodeCount        int              `json:"episode_count,omitempty"`
	Genres              []Genre          `json:"genres,omitempty"`
	ProductionCountries []string         `json:"production_countries,omitempty"`
	OriginCountries     []string         `json:"origin_countries,omitempty"`
	OriginalLanguage    string           `json:"original_language,omitempty"`
	SpokenLanguages     []string         `json:"spoken_languages,omitempty"`
	Studios             []Company        `json:"studios,omitempty"`
	Directors           []Person         `json:"directors,omitempty"`
	Writers             []Person         `json:"writers,omitempty"`
	Cast                []Person         `json:"cast,omitempty"`
	PosterPath          string           `json:"poster_path,omitempty"`
	BackdropPath        string           `json:"backdrop_path,omitempty"`
	Seasons             []SeasonSnapshot `json:"seasons,omitempty"`
}

type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name,omitempty"`
}

type Company struct {
	TMDBID int64  `json:"tmdb_id,omitempty"`
	Name   string `json:"name"`
}

type Person struct {
	TMDBID    int64  `json:"tmdb_id,omitempty"`
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Job       string `json:"job,omitempty"`
}

type SeasonSnapshot struct {
	TMDBID       int64  `json:"tmdb_id,omitempty"`
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name,omitempty"`
	AirDate      string `json:"air_date,omitempty"`
	EpisodeCount int    `json:"episode_count,omitempty"`
	PosterPath   string `json:"poster_path,omitempty"`
}

type detailCredits struct {
	Cast []struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Character string `json:"character"`
	} `json:"cast"`
	Crew []struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Department string `json:"department"`
		Job        string `json:"job"`
	} `json:"crew"`
}

type detailGenre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type detailCountry struct {
	ISO string `json:"iso_3166_1"`
}

type detailLanguage struct {
	ISO string `json:"iso_639_1"`
}

type detailCompany struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type detailCreator struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Candidate is the bounded, credential-free projection used by manual media
// matching. It intentionally excludes upstream payloads and image URLs.
type Candidate struct {
	ID               int64   `json:"id"`
	Title            string  `json:"title"`
	MediaType        string  `json:"media_type"`
	OriginalLanguage string  `json:"original_language"`
	ReleaseYear      *int    `json:"release_year,omitempty"`
	Confidence       float64 `json:"confidence"`
}

func New(token string) (*Client, error) {
	return NewWithCredential(Credential{Kind: CredentialKindReadAccessToken, Value: token})
}
func NewWithRoutes(token, apiBase, imageBase string) (*Client, error) {
	return NewWithCredentialRoutes(Credential{Kind: CredentialKindReadAccessToken, Value: token}, apiBase, imageBase)
}
func NewWithCredential(credential Credential) (*Client, error) {
	return NewWithCredentialRoutes(credential, DefaultAPIBaseURL, DefaultImageBaseURL)
}
func NewWithCredentialRoutes(credential Credential, apiBase, imageBase string) (*Client, error) {
	validatedCredential, err := ValidateCredential(credential)
	if err != nil {
		return nil, err
	}
	validatedAPI, err := ValidateBaseURL(apiBase)
	if err != nil {
		return nil, err
	}
	validatedImage, err := ValidateBaseURL(imageBase)
	if err != nil {
		return nil, err
	}
	fallback := ""
	if validatedAPI == DefaultAPIBaseURL {
		fallback = FallbackAPIBaseURL
	}
	return &Client{credential: validatedCredential, apiBase: validatedAPI, fallbackAPI: fallback, imageBase: validatedImage, http: controlledHTTPClient()}, nil
}

func ValidateCredential(credential Credential) (Credential, error) {
	credential.Value = strings.TrimSpace(credential.Value)
	if credential.Kind != CredentialKindReadAccessToken && credential.Kind != CredentialKindAPIKey {
		return Credential{}, fmt.Errorf("tmdb credential kind is invalid")
	}
	if credential.Value == "" || len(credential.Value) > 4096 || strings.ContainsAny(credential.Value, "\r\n") {
		return Credential{}, fmt.Errorf("tmdb credential is invalid")
	}
	return credential, nil
}

// NewForTest permits httptest HTTP origins. Production constructors remain HTTPS-only.
func NewForTest(token, origin string, client *http.Client) (*Client, error) {
	return NewForCredentialTest(Credential{Kind: CredentialKindReadAccessToken, Value: token}, origin, client)
}
func NewForCredentialTest(credential Credential, origin string, client *http.Client) (*Client, error) {
	return newForRoutesTest(credential, origin, "", origin, client)
}
func NewForFallbackTest(token, primary, fallback string, client *http.Client) (*Client, error) {
	return newForRoutesTest(Credential{Kind: CredentialKindReadAccessToken, Value: token}, primary, fallback, primary, client)
}
func newForRoutesTest(credential Credential, primary, fallback, image string, client *http.Client) (*Client, error) {
	validatedCredential, err := ValidateCredential(credential)
	if err != nil {
		return nil, err
	}
	for _, value := range []string{primary, image} {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("tmdb test origin is invalid")
		}
	}
	if fallback != "" {
		parsed, err := url.Parse(fallback)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("tmdb test fallback is invalid")
		}
	}
	if client == nil {
		client = controlledHTTPClient()
	}
	return &Client{credential: validatedCredential, apiBase: strings.TrimRight(primary, "/"), fallbackAPI: strings.TrimRight(fallback, "/"), imageBase: strings.TrimRight(image, "/"), http: client}, nil
}

func controlledHTTPClient() *http.Client {
	return &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
func ValidateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("tmdb route must be an absolute HTTPS prefix")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") || parsed.RawPath != "" {
		return "", fmt.Errorf("tmdb route contains unsupported URL fields")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("tmdb route path is invalid")
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (c *Client) Test(ctx context.Context) error { return c.TestAPI(ctx) }
func (c *Client) TestAPI(ctx context.Context) error {
	var response struct {
		ID int64 `json:"id"`
	}
	if err := c.get(ctx, "/movie/550", nil, &response); err != nil {
		return err
	}
	if response.ID != 550 {
		return fmt.Errorf("tmdb response identity is invalid")
	}
	return nil
}
func (c *Client) TestImage(ctx context.Context) error { return testImage(ctx, c.http, c.imageBase) }

// DownloadJPEG resolves a snapshot image identity through the configured TMDB
// image origin. It accepts no absolute URL and returns only bounded JPEG bytes.
func (c *Client) DownloadJPEG(ctx context.Context, identity, size string, maxBytes int64) ([]byte, error) {
	identity = cleanImagePath(identity)
	if identity == "" || !allowedImageSize(size) || maxBytes <= 0 || maxBytes > 20<<20 {
		return nil, clientError(ErrorInvalidRequest, nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.imageBase+"/"+size+identity, nil)
	if err != nil {
		return nil, clientError(ErrorInvalidRequest, err)
	}
	request.Header.Set("Accept", "image/jpeg")
	httpClient := *c.http
	httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, clientError(ErrorNetworkUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, clientError(ErrorRequestFailed, nil)
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "image/jpeg" {
		return nil, clientError(ErrorImageInvalid, nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(body) < 3 || int64(len(body)) > maxBytes || body[0] != 0xff || body[1] != 0xd8 || body[2] != 0xff {
		return nil, clientError(ErrorImageInvalid, err)
	}
	return body, nil
}

func allowedImageSize(value string) bool {
	switch value {
	case "w300", "w500", "w780", "w1280", "original":
		return true
	default:
		return false
	}
}

func TestImageBase(ctx context.Context, base string) error {
	validated, err := ValidateBaseURL(base)
	if err != nil {
		return err
	}
	return testImage(ctx, controlledHTTPClient(), validated)
}
func testImageBaseWithClient(ctx context.Context, base string, client *http.Client) error {
	if client == nil {
		client = controlledHTTPClient()
	}
	return testImage(ctx, client, strings.TrimRight(base, "/"))
}
func testImage(ctx context.Context, client *http.Client, base string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+testImagePath, nil)
	if err != nil {
		return fmt.Errorf("tmdb image request invalid")
	}
	request.Header.Set("Accept", "image/*")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("tmdb image unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb image request failed")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		return fmt.Errorf("tmdb image content type is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxImageTestBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxImageTestBytes {
		return fmt.Errorf("tmdb image response is invalid")
	}
	return nil
}

func (c *Client) Search(ctx context.Context, mediaType, title string, year *int, language, region string) (Match, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "movie" && mediaType != "tv" {
		return Match{}, clientError(ErrorInvalidRequest, nil)
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len(title) > 256 {
		return Match{}, clientError(ErrorInvalidRequest, nil)
	}
	values := url.Values{"query": {title}, "include_adult": {"false"}}
	if language != "" {
		values.Set("language", language)
	}
	if region != "" && mediaType == "movie" {
		values.Set("region", region)
	}
	if year != nil {
		if mediaType == "movie" {
			values.Set("year", strconv.Itoa(*year))
		} else {
			values.Set("first_air_date_year", strconv.Itoa(*year))
		}
	}
	var response struct {
		Results []struct {
			ID               int64    `json:"id"`
			Title            string   `json:"title"`
			Name             string   `json:"name"`
			OriginalTitle    string   `json:"original_title"`
			OriginalName     string   `json:"original_name"`
			OriginalLanguage string   `json:"original_language"`
			GenreIDs         []int    `json:"genre_ids"`
			ReleaseDate      string   `json:"release_date"`
			FirstAirDate     string   `json:"first_air_date"`
			OriginCountries  []string `json:"origin_country"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/search/"+mediaType, values, &response); err != nil {
		return Match{}, err
	}
	if len(response.Results) == 0 {
		return Match{}, clientError(ErrorNoMatch, nil)
	}
	first := response.Results[0]
	resultTitle, date := first.Title, first.ReleaseDate
	if mediaType == "tv" {
		resultTitle, date = first.Name, first.FirstAirDate
	}
	originalTitle := first.OriginalTitle
	if mediaType == "tv" {
		originalTitle = first.OriginalName
	}
	confidence := max(titleConfidence(title, resultTitle), titleConfidence(title, originalTitle))
	if year != nil && parseYear(date) != nil && *year == *parseYear(date) && confidence < .88 && len(response.Results) == 1 {
		confidence = .88
	}
	if len(response.Results) > 1 && titleConfidence(title, titleOf(mediaType, response.Results[1].Title, response.Results[1].Name)) >= confidence-.05 {
		confidence -= .2
	}
	result, err := c.getDetailedMatch(ctx, mediaType, first.ID, language)
	if err != nil {
		return Match{}, err
	}
	if result.Title == "" {
		result.Title = resultTitle
		result.Snapshot.Title = resultTitle
	}
	if result.Snapshot.OriginalTitle == "" {
		result.Snapshot.OriginalTitle = cleanText(originalTitle, 512)
	}
	if result.OriginalLanguage == "" {
		result.OriginalLanguage = first.OriginalLanguage
		result.Snapshot.OriginalLanguage = first.OriginalLanguage
	}
	if result.ReleaseYear == nil {
		result.ReleaseYear = parseYear(date)
	}
	if result.Snapshot.ReleaseDate == "" {
		result.Snapshot.ReleaseDate = cleanDate(date)
	}
	if len(result.GenreIDs) == 0 {
		result.GenreIDs = append([]int(nil), first.GenreIDs...)
		for _, id := range first.GenreIDs {
			if id > 0 {
				result.Snapshot.Genres = append(result.Snapshot.Genres, Genre{ID: id})
			}
		}
	}
	if len(result.OriginCountries) == 0 {
		result.OriginCountries = append([]string(nil), first.OriginCountries...)
		result.Snapshot.OriginCountries = append([]string(nil), first.OriginCountries...)
	}
	result.Confidence = confidence
	return result, nil
}

// GetByID fetches and validates a direct identity hint. Callers must never
// trust title/category metadata embedded in recognition words or submitted by
// a browser; only this server-side projection is used for classification.
func (c *Client) GetByID(ctx context.Context, mediaType string, id int64, language string) (Match, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if (mediaType != "movie" && mediaType != "tv") || id <= 0 {
		return Match{}, clientError(ErrorInvalidRequest, nil)
	}
	match, err := c.getDetailedMatch(ctx, mediaType, id, language)
	if err != nil {
		return Match{}, err
	}
	if match.ID != id || strings.TrimSpace(match.Title) == "" {
		return Match{}, clientError(ErrorInvalidResponse, nil)
	}
	match.Confidence = 1
	return match, nil
}

func (c *Client) getDetailedMatch(ctx context.Context, mediaType string, id int64, language string) (Match, error) {
	values := url.Values{"append_to_response": {"credits,external_ids"}}
	if language != "" {
		values.Set("language", language)
	}
	if mediaType == "movie" {
		var detail struct {
			ID                  int64            `json:"id"`
			IMDbID              string           `json:"imdb_id"`
			Title               string           `json:"title"`
			OriginalTitle       string           `json:"original_title"`
			OriginalLanguage    string           `json:"original_language"`
			ReleaseDate         string           `json:"release_date"`
			Overview            string           `json:"overview"`
			Tagline             string           `json:"tagline"`
			Status              string           `json:"status"`
			VoteAverage         float64          `json:"vote_average"`
			VoteCount           int              `json:"vote_count"`
			Runtime             int              `json:"runtime"`
			PosterPath          string           `json:"poster_path"`
			BackdropPath        string           `json:"backdrop_path"`
			Genres              []detailGenre    `json:"genres"`
			ProductionCountries []detailCountry  `json:"production_countries"`
			SpokenLanguages     []detailLanguage `json:"spoken_languages"`
			ProductionCompanies []detailCompany  `json:"production_companies"`
			Credits             detailCredits    `json:"credits"`
			ExternalIDs         struct {
				IMDbID string `json:"imdb_id"`
			} `json:"external_ids"`
		}
		if err := c.get(ctx, "/movie/"+strconv.FormatInt(id, 10), values, &detail); err != nil {
			return Match{}, err
		}
		if detail.ID != 0 && detail.ID != id {
			return Match{}, clientError(ErrorInvalidResponse, nil)
		}
		imdbID := detail.IMDbID
		if imdbID == "" {
			imdbID = detail.ExternalIDs.IMDbID
		}
		snapshot := Snapshot{Version: 1, TMDBID: id, IMDbID: cleanIMDbID(imdbID), MediaType: mediaType, Title: cleanText(detail.Title, 512), OriginalTitle: cleanText(detail.OriginalTitle, 512), ReleaseDate: cleanDate(detail.ReleaseDate), Overview: cleanText(detail.Overview, 32768), Tagline: cleanText(detail.Tagline, 2048), Status: cleanText(detail.Status, 128), VoteAverage: boundedRating(detail.VoteAverage), VoteCount: boundedCount(detail.VoteCount), RuntimeMinutes: boundedRuntime(detail.Runtime), OriginalLanguage: cleanCode(detail.OriginalLanguage), PosterPath: cleanImagePath(detail.PosterPath), BackdropPath: cleanImagePath(detail.BackdropPath)}
		populateCommonSnapshot(&snapshot, detail.Genres, detail.ProductionCountries, nil, detail.Credits, nil)
		populateDetailSnapshot(&snapshot, detail.SpokenLanguages, detail.ProductionCompanies)
		return matchFromSnapshot(snapshot), nil
	}
	var detail struct {
		ID                  int64            `json:"id"`
		Name                string           `json:"name"`
		OriginalName        string           `json:"original_name"`
		OriginalLanguage    string           `json:"original_language"`
		FirstAirDate        string           `json:"first_air_date"`
		Overview            string           `json:"overview"`
		Tagline             string           `json:"tagline"`
		Status              string           `json:"status"`
		VoteAverage         float64          `json:"vote_average"`
		VoteCount           int              `json:"vote_count"`
		EpisodeRuntime      []int            `json:"episode_run_time"`
		NumberOfSeasons     int              `json:"number_of_seasons"`
		NumberOfEpisodes    int              `json:"number_of_episodes"`
		OriginCountries     []string         `json:"origin_country"`
		ProductionCountries []detailCountry  `json:"production_countries"`
		PosterPath          string           `json:"poster_path"`
		BackdropPath        string           `json:"backdrop_path"`
		Genres              []detailGenre    `json:"genres"`
		SpokenLanguages     []detailLanguage `json:"spoken_languages"`
		ProductionCompanies []detailCompany  `json:"production_companies"`
		Credits             detailCredits    `json:"credits"`
		CreatedBy           []detailCreator  `json:"created_by"`
		Seasons             []struct {
			ID           int64  `json:"id"`
			SeasonNumber int    `json:"season_number"`
			Name         string `json:"name"`
			AirDate      string `json:"air_date"`
			EpisodeCount int    `json:"episode_count"`
			PosterPath   string `json:"poster_path"`
		} `json:"seasons"`
		ExternalIDs struct {
			IMDbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(id, 10), values, &detail); err != nil {
		return Match{}, err
	}
	if detail.ID != 0 && detail.ID != id {
		return Match{}, clientError(ErrorInvalidResponse, nil)
	}
	runtimeMinutes := 0
	if len(detail.EpisodeRuntime) > 0 {
		runtimeMinutes = boundedRuntime(detail.EpisodeRuntime[0])
	}
	snapshot := Snapshot{Version: 1, TMDBID: id, IMDbID: cleanIMDbID(detail.ExternalIDs.IMDbID), MediaType: mediaType, Title: cleanText(detail.Name, 512), OriginalTitle: cleanText(detail.OriginalName, 512), ReleaseDate: cleanDate(detail.FirstAirDate), Overview: cleanText(detail.Overview, 32768), Tagline: cleanText(detail.Tagline, 2048), Status: cleanText(detail.Status, 128), VoteAverage: boundedRating(detail.VoteAverage), VoteCount: boundedCount(detail.VoteCount), RuntimeMinutes: runtimeMinutes, SeasonCount: boundedCount(detail.NumberOfSeasons), EpisodeCount: boundedCount(detail.NumberOfEpisodes), OriginalLanguage: cleanCode(detail.OriginalLanguage), PosterPath: cleanImagePath(detail.PosterPath), BackdropPath: cleanImagePath(detail.BackdropPath)}
	populateCommonSnapshot(&snapshot, detail.Genres, detail.ProductionCountries, detail.OriginCountries, detail.Credits, detail.CreatedBy)
	populateDetailSnapshot(&snapshot, detail.SpokenLanguages, detail.ProductionCompanies)
	for _, season := range detail.Seasons {
		if season.SeasonNumber < 0 || season.SeasonNumber > 10000 {
			continue
		}
		snapshot.Seasons = append(snapshot.Seasons, SeasonSnapshot{TMDBID: max(int64(0), season.ID), SeasonNumber: season.SeasonNumber, Name: cleanText(season.Name, 512), AirDate: cleanDate(season.AirDate), EpisodeCount: max(0, season.EpisodeCount), PosterPath: cleanImagePath(season.PosterPath)})
		if len(snapshot.Seasons) == 1000 {
			break
		}
	}
	return matchFromSnapshot(snapshot), nil
}

func populateDetailSnapshot(snapshot *Snapshot, languages []detailLanguage, companies []detailCompany) {
	for _, language := range languages {
		if code := cleanCode(language.ISO); code != "" && !containsString(snapshot.SpokenLanguages, code) {
			snapshot.SpokenLanguages = append(snapshot.SpokenLanguages, code)
			if len(snapshot.SpokenLanguages) == 32 {
				break
			}
		}
	}
	for _, company := range companies {
		name := cleanText(company.Name, 256)
		if name == "" || containsCompany(snapshot.Studios, company.ID, name) {
			continue
		}
		snapshot.Studios = append(snapshot.Studios, Company{TMDBID: max(int64(0), company.ID), Name: name})
		if len(snapshot.Studios) == 100 {
			break
		}
	}
}

func containsCompany(values []Company, id int64, name string) bool {
	for _, value := range values {
		if (id > 0 && value.TMDBID == id) || (id <= 0 && value.Name == name) {
			return true
		}
	}
	return false
}

func populateCommonSnapshot(snapshot *Snapshot, genres []detailGenre, production []detailCountry, origin []string, credits detailCredits, creators []detailCreator) {
	for _, genre := range genres {
		if genre.ID <= 0 {
			continue
		}
		snapshot.Genres = append(snapshot.Genres, Genre{ID: genre.ID, Name: cleanText(genre.Name, 128)})
		if len(snapshot.Genres) == 100 {
			break
		}
	}
	for _, country := range production {
		if code := cleanCode(country.ISO); code != "" && !containsString(snapshot.ProductionCountries, code) {
			snapshot.ProductionCountries = append(snapshot.ProductionCountries, code)
		}
	}
	for _, raw := range origin {
		if code := cleanCode(raw); code != "" && !containsString(snapshot.OriginCountries, code) {
			snapshot.OriginCountries = append(snapshot.OriginCountries, code)
		}
	}
	for _, creator := range creators {
		appendUniquePerson(&snapshot.Writers, Person{TMDBID: max(int64(0), creator.ID), Name: cleanText(creator.Name, 256), Job: "Creator"}, 50)
	}
	for _, crew := range credits.Crew {
		person := Person{TMDBID: max(int64(0), crew.ID), Name: cleanText(crew.Name, 256), Job: cleanText(crew.Job, 128)}
		if person.Name == "" {
			continue
		}
		if strings.EqualFold(crew.Job, "Director") {
			appendUniquePerson(&snapshot.Directors, person, 50)
		}
		if strings.EqualFold(crew.Department, "Writing") || isWriterJob(crew.Job) {
			appendUniquePerson(&snapshot.Writers, person, 100)
		}
	}
	for _, cast := range credits.Cast {
		person := Person{TMDBID: max(int64(0), cast.ID), Name: cleanText(cast.Name, 256), Character: cleanText(cast.Character, 256)}
		if person.Name != "" {
			appendUniquePerson(&snapshot.Cast, person, 100)
		}
	}
}

func matchFromSnapshot(snapshot Snapshot) Match {
	match := Match{ID: snapshot.TMDBID, Title: snapshot.Title, MediaType: snapshot.MediaType, OriginalLanguage: snapshot.OriginalLanguage, ProductionCountries: append([]string(nil), snapshot.ProductionCountries...), OriginCountries: append([]string(nil), snapshot.OriginCountries...), ReleaseYear: parseYear(snapshot.ReleaseDate), Snapshot: snapshot}
	for _, genre := range snapshot.Genres {
		match.GenreIDs = append(match.GenreIDs, genre.ID)
	}
	return match
}

func appendUniquePerson(target *[]Person, candidate Person, limit int) {
	if candidate.Name == "" || len(*target) >= limit {
		return
	}
	for _, existing := range *target {
		if candidate.TMDBID > 0 && existing.TMDBID == candidate.TMDBID && existing.Job == candidate.Job {
			return
		}
		if candidate.TMDBID == 0 && existing.TMDBID == 0 && existing.Name == candidate.Name && existing.Job == candidate.Job {
			return
		}
	}
	*target = append(*target, candidate)
}

func isWriterJob(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "writer", "screenplay", "story", "teleplay", "novel", "characters":
		return true
	default:
		return false
	}
}

func cleanText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func cleanCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" || len(value) > 16 || strings.ContainsAny(value, "\r\n\t /\\?#") {
		return ""
	}
	return value
}

func cleanDate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func cleanIMDbID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 3 || len(value) > 32 || !strings.HasPrefix(value, "tt") {
		return ""
	}
	for _, character := range value[2:] {
		if character < '0' || character > '9' {
			return ""
		}
	}
	return value
}

func cleanImagePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "?#\\\r\n") || strings.Contains(value, "..") {
		return ""
	}
	return value
}

func boundedRuntime(value int) int {
	if value <= 0 || value > 24*60 {
		return 0
	}
	return value
}

func boundedRating(value float64) float64 {
	if value <= 0 || value > 10 {
		return 0
	}
	return value
}

func boundedCount(value int) int {
	if value <= 0 || value > 1_000_000_000 {
		return 0
	}
	return value
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// SearchCandidates returns at most ten safe summaries for administrator
// correction. Full details are fetched again with GetByID when an override is
// saved.
func (c *Client) SearchCandidates(ctx context.Context, mediaType, title string, year *int, language, region string, limit int) ([]Candidate, error) {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "movie" && mediaType != "tv" {
		return nil, clientError(ErrorInvalidRequest, nil)
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len([]rune(title)) > 256 {
		return nil, clientError(ErrorInvalidRequest, nil)
	}
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	values := url.Values{"query": {title}, "include_adult": {"false"}}
	if language != "" {
		values.Set("language", language)
	}
	if region != "" && mediaType == "movie" {
		values.Set("region", region)
	}
	if year != nil {
		if mediaType == "movie" {
			values.Set("year", strconv.Itoa(*year))
		} else {
			values.Set("first_air_date_year", strconv.Itoa(*year))
		}
	}
	var response struct {
		Results []struct {
			ID               int64  `json:"id"`
			Title            string `json:"title"`
			Name             string `json:"name"`
			OriginalLanguage string `json:"original_language"`
			ReleaseDate      string `json:"release_date"`
			FirstAirDate     string `json:"first_air_date"`
		} `json:"results"`
	}
	if err := c.get(ctx, "/search/"+mediaType, values, &response); err != nil {
		return nil, err
	}
	if len(response.Results) == 0 {
		return nil, clientError(ErrorNoMatch, nil)
	}
	items := make([]Candidate, 0, min(limit, len(response.Results)))
	for _, result := range response.Results {
		candidateTitle, date := result.Title, result.ReleaseDate
		if mediaType == "tv" {
			candidateTitle, date = result.Name, result.FirstAirDate
		}
		if result.ID <= 0 || strings.TrimSpace(candidateTitle) == "" {
			continue
		}
		items = append(items, Candidate{ID: result.ID, Title: candidateTitle, MediaType: mediaType, OriginalLanguage: result.OriginalLanguage, ReleaseYear: parseYear(date), Confidence: titleConfidence(title, candidateTitle)})
		if len(items) == limit {
			break
		}
	}
	if len(items) == 0 {
		return nil, clientError(ErrorNoMatch, nil)
	}
	return items, nil
}

func titleOf(mediaType, movie, tv string) string {
	if mediaType == "tv" {
		return tv
	}
	return movie
}
func titleConfidence(query, candidate string) float64 {
	normalize := func(value string) string {
		return strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(value)), " "))
	}
	query, candidate = normalize(query), normalize(candidate)
	if query == candidate {
		return .98
	}
	if strings.Contains(query, candidate) || strings.Contains(candidate, query) {
		return .82
	}
	return .62
}
func parseYear(value string) *int {
	if len(value) < 4 {
		return nil
	}
	year, err := strconv.Atoi(value[:4])
	if err != nil || year < 1888 || year > 2200 {
		return nil
	}
	return &year
}

func (c *Client) get(ctx context.Context, endpoint string, values url.Values, target any) error {
	body, err := c.getAt(ctx, c.apiBase, endpoint, values)
	if err != nil && c.fallbackAPI != "" && isNetworkFailure(err) {
		body, err = c.getAt(ctx, c.fallbackAPI, endpoint, values)
	}
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return clientError(ErrorInvalidResponse, err)
	}
	return nil
}
func (c *Client) getAt(ctx context.Context, base, endpoint string, values url.Values) ([]byte, error) {
	values = cloneValues(values)
	if c.credential.Kind == CredentialKindAPIKey {
		values.Set("api_key", c.credential.Value)
	}
	requestURL := base + endpoint
	if len(values) > 0 {
		requestURL += "?" + values.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, clientError(ErrorInvalidRequest, err)
	}
	if c.credential.Kind == CredentialKindReadAccessToken {
		request.Header.Set("Authorization", "Bearer "+c.credential.Value)
	}
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &networkRequestError{cause: err}
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil || len(body) > maxResponseBytes {
		return nil, clientError(ErrorInvalidResponse, readErr)
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, clientError(ErrorAuthFailed, nil)
	}
	if response.StatusCode != http.StatusOK {
		return nil, clientError(ErrorRequestFailed, nil)
	}
	return body, nil
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values)+1)
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

type networkRequestError struct{ cause error }

func (e *networkRequestError) Error() string { return ErrorNetworkUnavailable }
func (e *networkRequestError) Unwrap() error { return e.cause }
func isNetworkFailure(err error) bool {
	var requestErr *networkRequestError
	if !errors.As(err, &requestErr) {
		return false
	}
	cause := requestErr.cause
	var urlError *url.Error
	if errors.As(cause, &urlError) {
		cause = urlError.Err
	}
	if errors.Is(cause, context.Canceled) {
		return false
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return true
	}
	var dnsError *net.DNSError
	if errors.As(cause, &dnsError) {
		return true
	}
	var operationError *net.OpError
	if errors.As(cause, &operationError) {
		return operationError.Op == "dial" || operationError.Op == "connect"
	}
	var networkError net.Error
	return errors.As(cause, &networkError) && networkError.Timeout()
}
