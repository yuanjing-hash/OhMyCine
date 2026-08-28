package services

import (
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

func TestNormalizeMediaTypeDirectoryTemplate(t *testing.T) {
	tests := []struct {
		name, mediaType, input, want string
	}{
		{"movie prefix", "movie", "{category}/{title}", "电影/{category}/{title}"},
		{"tv prefix", "tv", "{category}/{title}/Season {season:02}", "电视剧/{category}/{title}/Season {season:02}"},
		{"movie idempotent", "movie", "电影/{category}/{title}", "电影/{category}/{title}"},
		{"tv idempotent", "tv", "电视剧/{category}/{title}", "电视剧/{category}/{title}"},
		{"wrong movie root", "movie", "电视剧/{category}/{title}", "电影/{category}/{title}"},
		{"wrong tv root", "tv", "电影/{category}/{title}", "电视剧/{category}/{title}"},
		{"deduplicate fixed roots", "movie", "电影/电视剧/电影/{category}/{title}", "电影/{category}/{title}"},
		{"insert missing category", "tv", "Series/{title}/Season {season:02}", "电视剧/{category}/Series/{title}/Season {season:02}"},
		{"move category to second segment", "movie", "Archive/{category}/{title}", "电影/{category}/Archive/{title}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeMediaTypeDirectoryTemplate(test.input, test.mediaType); got != test.want {
				t.Fatalf("normalize=%q want=%q", got, test.want)
			}
		})
	}
}

func TestTypeFirstSnapshotsSeparateMovieAndTVWithSameCategory(t *testing.T) {
	year, season, episode := 2026, 1, 2
	manifest := downloadpkg.Manifest{Complete: true, Files: []downloadpkg.File{{RelativePath: "Example.S01E02.mkv", Size: minimumAutomaticTransferVideoBytes}}}
	tests := []struct {
		name string
		task models.DownloadTask
		want string
	}{
		{
			name: "movie",
			task: models.DownloadTask{ScrapeMediaType: "movie", ScrapeTitle: "Example", ScrapeCategory: "动画", ScrapeYear: &year, MovieDirectoryTemplate: defaultMovieDirectoryTemplate, MovieFilenameTemplate: defaultMovieFilenameTemplate},
			want: "电影/动画/Example (2026)/Example (2026).mkv",
		},
		{
			name: "tv",
			task: models.DownloadTask{ScrapeMediaType: "tv", ScrapeTitle: "Example", ScrapeCategory: "动画", ScrapeYear: &year, ScrapeSeason: &season, ScrapeEpisode: &episode, TVDirectoryTemplate: defaultTVDirectoryTemplate, TVFilenameTemplate: defaultTVFilenameTemplate},
			want: "电视剧/动画/Example (2026)/Season 01/Example - S01E02.mkv",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			targets, err := buildTransferTargets(test.task, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if len(targets) != 1 || targets[0].Relative != test.want {
				t.Fatalf("targets=%+v want=%q", targets, test.want)
			}
		})
	}
}
