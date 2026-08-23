package nfo

import (
	"encoding/xml"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/pkg/metadata/tmdb"
)

var ErrSnapshotIncomplete = errors.New("metadata snapshot is incomplete")

type uniqueID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type actor struct {
	Name   string `xml:"name"`
	Role   string `xml:"role,omitempty"`
	TMDBID int64  `xml:"tmdbid,omitempty"`
	Order  int    `xml:"order"`
}

type rating struct {
	Name    string `xml:"name,attr"`
	Max     int    `xml:"max,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:"value"`
	Votes   int    `xml:"votes,omitempty"`
}

type ratings struct {
	Items []rating `xml:"rating"`
}

type namedSeason struct {
	Number int    `xml:"number,attr"`
	Name   string `xml:",chardata"`
}

type movieDocument struct {
	XMLName       xml.Name   `xml:"movie"`
	Title         string     `xml:"title"`
	SortTitle     string     `xml:"sorttitle,omitempty"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Outline       string     `xml:"outline,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Tagline       string     `xml:"tagline,omitempty"`
	Status        string     `xml:"status,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	ReleaseDate   string     `xml:"releasedate,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Rating        string     `xml:"rating,omitempty"`
	Votes         int        `xml:"votes,omitempty"`
	Ratings       *ratings   `xml:"ratings,omitempty"`
	TMDBID        int64      `xml:"tmdbid"`
	IMDbID        string     `xml:"imdbid,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
	Languages     []string   `xml:"language,omitempty"`
	Genres        []string   `xml:"genre,omitempty"`
	Countries     []string   `xml:"country,omitempty"`
	Studios       []string   `xml:"studio,omitempty"`
	Directors     []string   `xml:"director,omitempty"`
	Writers       []string   `xml:"writer,omitempty"`
	Credits       []string   `xml:"credits,omitempty"`
	Actors        []actor    `xml:"actor,omitempty"`
}

type tvShowDocument struct {
	XMLName       xml.Name      `xml:"tvshow"`
	Title         string        `xml:"title"`
	ShowTitle     string        `xml:"showtitle,omitempty"`
	SortTitle     string        `xml:"sorttitle,omitempty"`
	OriginalTitle string        `xml:"originaltitle,omitempty"`
	Outline       string        `xml:"outline,omitempty"`
	Plot          string        `xml:"plot,omitempty"`
	Tagline       string        `xml:"tagline,omitempty"`
	Status        string        `xml:"status,omitempty"`
	Year          int           `xml:"year,omitempty"`
	Premiered     string        `xml:"premiered,omitempty"`
	Aired         string        `xml:"aired,omitempty"`
	ReleaseDate   string        `xml:"releasedate,omitempty"`
	Runtime       int           `xml:"runtime,omitempty"`
	Rating        string        `xml:"rating,omitempty"`
	Votes         int           `xml:"votes,omitempty"`
	Ratings       *ratings      `xml:"ratings,omitempty"`
	TMDBID        int64         `xml:"tmdbid"`
	IMDbID        string        `xml:"imdbid,omitempty"`
	UniqueIDs     []uniqueID    `xml:"uniqueid"`
	Languages     []string      `xml:"language,omitempty"`
	Genres        []string      `xml:"genre,omitempty"`
	Countries     []string      `xml:"country,omitempty"`
	Studios       []string      `xml:"studio,omitempty"`
	Directors     []string      `xml:"director,omitempty"`
	Writers       []string      `xml:"writer,omitempty"`
	Credits       []string      `xml:"credits,omitempty"`
	Actors        []actor       `xml:"actor,omitempty"`
	NamedSeasons  []namedSeason `xml:"namedseason,omitempty"`
}

type ImageIdentity struct {
	Kind         string
	SeasonNumber *int
	TMDBPath     string
}

// ProviderSnapshot is the provider-neutral metadata accepted from one
// Host-bound plugin operation. It deliberately contains no filesystem paths,
// upstream URLs, credentials, or TMDB requirement.
type ProviderSnapshot struct {
	Kind            string
	Title           string
	OriginalTitle   string
	Overview        string
	Author          string
	PublishedDate   string
	DurationSeconds int64
	SeasonNumber    *int
	EpisodeNumber   *int
	Genres          []string
	Tags            []string
	UniqueIDs       map[string]string
}

type providerDocument struct {
	XMLName       xml.Name
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Outline       string     `xml:"outline,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Premiered     string     `xml:"premiered,omitempty"`
	ReleaseDate   string     `xml:"releasedate,omitempty"`
	Runtime       int        `xml:"runtime,omitempty"`
	Season        *int       `xml:"season,omitempty"`
	Episode       *int       `xml:"episode,omitempty"`
	UniqueIDs     []uniqueID `xml:"uniqueid"`
	Genres        []string   `xml:"genre,omitempty"`
	Tags          []string   `xml:"tag,omitempty"`
	Directors     []string   `xml:"director,omitempty"`
	Credits       []string   `xml:"credits,omitempty"`
}

// RenderProvider serializes plugin-scoped metadata without registering it as
// a global scraper and without requiring a TMDB identity.
func RenderProvider(snapshot ProviderSnapshot) ([]byte, error) {
	if strings.TrimSpace(snapshot.Title) == "" || len(snapshot.UniqueIDs) == 0 {
		return nil, ErrSnapshotIncomplete
	}
	identities := make([]uniqueID, 0, len(snapshot.UniqueIDs))
	keys := make([]string, 0, len(snapshot.UniqueIDs))
	for key := range snapshot.UniqueIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		identities = append(identities, uniqueID{Type: key, Default: index == 0, Value: snapshot.UniqueIDs[key]})
	}
	published := snapshot.PublishedDate
	if len(published) > 10 {
		published = published[:10]
	}
	year := 0
	if len(published) >= 4 {
		year, _ = strconv.Atoi(published[:4])
	}
	authors := []string(nil)
	if strings.TrimSpace(snapshot.Author) != "" {
		authors = []string{snapshot.Author}
	}
	root := "movie"
	switch snapshot.Kind {
	case "series":
		root = "tvshow"
	case "episode":
		root = "episodedetails"
	case "movie", "video":
	default:
		return nil, ErrSnapshotIncomplete
	}
	document := providerDocument{
		XMLName: xml.Name{Local: root},
		Title:   snapshot.Title, OriginalTitle: snapshot.OriginalTitle, Outline: snapshot.Overview,
		Plot: snapshot.Overview, Year: year, Premiered: published, ReleaseDate: published,
		Runtime: int((snapshot.DurationSeconds + 30) / 60), Season: snapshot.SeasonNumber,
		Episode: snapshot.EpisodeNumber, UniqueIDs: identities, Genres: snapshot.Genres,
		Tags: snapshot.Tags, Directors: authors, Credits: authors,
	}
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
}

// Render serializes a deterministic Kodi/Emby-compatible movie or tvshow NFO.
// The function never constructs image URLs and cannot include source paths.
func Render(snapshot tmdb.Snapshot) ([]byte, error) {
	if snapshot.Version != 1 || snapshot.TMDBID <= 0 || strings.TrimSpace(snapshot.Title) == "" {
		return nil, ErrSnapshotIncomplete
	}
	common := buildCommon(snapshot)
	var document any
	switch snapshot.MediaType {
	case "movie":
		document = movieDocument{Title: snapshot.Title, SortTitle: snapshot.Title, OriginalTitle: snapshot.OriginalTitle, Outline: snapshot.Overview, Plot: snapshot.Overview, Tagline: snapshot.Tagline, Status: snapshot.Status, Year: common.year, Premiered: snapshot.ReleaseDate, ReleaseDate: snapshot.ReleaseDate, Runtime: snapshot.RuntimeMinutes, Rating: common.rating, Votes: snapshot.VoteCount, Ratings: common.ratings, TMDBID: snapshot.TMDBID, IMDbID: snapshot.IMDbID, UniqueIDs: common.uniqueIDs, Languages: common.languages, Genres: common.genres, Countries: common.countries, Studios: common.studios, Directors: common.directors, Writers: common.writers, Credits: common.writers, Actors: common.actors}
	case "tv":
		document = tvShowDocument{Title: snapshot.Title, ShowTitle: snapshot.Title, SortTitle: snapshot.Title, OriginalTitle: snapshot.OriginalTitle, Outline: snapshot.Overview, Plot: snapshot.Overview, Tagline: snapshot.Tagline, Status: snapshot.Status, Year: common.year, Premiered: snapshot.ReleaseDate, Aired: snapshot.ReleaseDate, ReleaseDate: snapshot.ReleaseDate, Runtime: snapshot.RuntimeMinutes, Rating: common.rating, Votes: snapshot.VoteCount, Ratings: common.ratings, TMDBID: snapshot.TMDBID, IMDbID: snapshot.IMDbID, UniqueIDs: common.uniqueIDs, Languages: common.languages, Genres: common.genres, Countries: common.countries, Studios: common.studios, Directors: common.directors, Writers: common.writers, Credits: common.writers, Actors: common.actors, NamedSeasons: common.namedSeasons}
	default:
		return nil, ErrSnapshotIncomplete
	}
	body, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
}

// Images returns only stable TMDB file identities. Callers choose bounded
// sizes and resolve bytes through the configured TMDB image client at runtime.
func Images(snapshot tmdb.Snapshot) []ImageIdentity {
	images := make([]ImageIdentity, 0, 2+len(snapshot.Seasons))
	if snapshot.PosterPath != "" {
		images = append(images, ImageIdentity{Kind: "poster", TMDBPath: snapshot.PosterPath})
	}
	if snapshot.BackdropPath != "" {
		images = append(images, ImageIdentity{Kind: "fanart", TMDBPath: snapshot.BackdropPath})
	}
	if snapshot.MediaType == "tv" {
		for _, season := range snapshot.Seasons {
			if season.PosterPath == "" || season.SeasonNumber < 0 {
				continue
			}
			number := season.SeasonNumber
			images = append(images, ImageIdentity{Kind: "season_poster", SeasonNumber: &number, TMDBPath: season.PosterPath})
		}
	}
	return images
}

type commonFields struct {
	year         int
	rating       string
	ratings      *ratings
	uniqueIDs    []uniqueID
	languages    []string
	genres       []string
	countries    []string
	studios      []string
	directors    []string
	writers      []string
	actors       []actor
	namedSeasons []namedSeason
}

func buildCommon(snapshot tmdb.Snapshot) commonFields {
	fields := commonFields{uniqueIDs: []uniqueID{{Type: "tmdb", Default: true, Value: strconv.FormatInt(snapshot.TMDBID, 10)}}}
	if snapshot.IMDbID != "" {
		fields.uniqueIDs = append(fields.uniqueIDs, uniqueID{Type: "imdb", Value: snapshot.IMDbID})
	}
	if len(snapshot.ReleaseDate) >= 4 {
		fields.year, _ = strconv.Atoi(snapshot.ReleaseDate[:4])
	}
	if snapshot.VoteAverage > 0 && snapshot.VoteAverage <= 10 {
		fields.rating = strconv.FormatFloat(snapshot.VoteAverage, 'f', 1, 64)
		fields.ratings = &ratings{Items: []rating{{Name: "themoviedb", Max: 10, Default: true, Value: fields.rating, Votes: snapshot.VoteCount}}}
	}
	if len(snapshot.SpokenLanguages) > 0 {
		fields.languages = append(fields.languages, snapshot.SpokenLanguages...)
	} else if snapshot.OriginalLanguage != "" {
		fields.languages = append(fields.languages, snapshot.OriginalLanguage)
	}
	for _, genre := range snapshot.Genres {
		if genre.Name != "" {
			fields.genres = append(fields.genres, genre.Name)
		}
	}
	fields.countries = append(fields.countries, snapshot.ProductionCountries...)
	if len(fields.countries) == 0 {
		fields.countries = append(fields.countries, snapshot.OriginCountries...)
	}
	for _, company := range snapshot.Studios {
		if company.Name != "" {
			fields.studios = append(fields.studios, company.Name)
		}
	}
	for _, person := range snapshot.Directors {
		if person.Name != "" {
			fields.directors = append(fields.directors, person.Name)
		}
	}
	for _, person := range snapshot.Writers {
		if person.Name != "" {
			fields.writers = append(fields.writers, person.Name)
		}
	}
	for index, person := range snapshot.Cast {
		if person.Name != "" {
			fields.actors = append(fields.actors, actor{Name: person.Name, Role: person.Character, TMDBID: person.TMDBID, Order: index})
		}
	}
	for _, season := range snapshot.Seasons {
		if season.SeasonNumber >= 0 && season.Name != "" {
			fields.namedSeasons = append(fields.namedSeasons, namedSeason{Number: season.SeasonNumber, Name: season.Name})
		}
	}
	return fields
}
