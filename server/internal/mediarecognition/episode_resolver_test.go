package mediarecognition

import (
	"fmt"
	"testing"
)

func TestResolvePackageEpisodesSupportsBracketedAnimeCollection(t *testing.T) {
	files := make([]FileFact, 0, 10)
	for episode := 1; episode <= 10; episode++ {
		files = append(files, FileFact{RelativePath: fmt.Sprintf("[Lilith-Raws] Megami-ryou no Ryoubo-kun. [%02dv2][Baha][WEB-DL][1080p][AVC AAC][BIG5][MP4].mp4", episode), Size: 400 << 20})
	}
	resolved := ResolvePackageEpisodes(files, MediaTypeTV)
	if !resolved.Complete || resolved.VideoCount != 10 || resolved.ResolvedCount != 10 {
		t.Fatalf("resolved=%+v", resolved)
	}
	for index, fact := range resolved.Files {
		if fact.Season == nil || *fact.Season != 1 || fact.Episode == nil || *fact.Episode != index+1 {
			t.Fatalf("fact[%d]=%+v", index, fact)
		}
	}
}

func TestResolvePackageEpisodesRejectsTechnicalNumbersAndDuplicates(t *testing.T) {
	for _, files := range [][]FileFact{
		{{RelativePath: "Example [1080p][10bit][1920X1080][2024].mkv", Size: 400 << 20}},
		{{RelativePath: "Show [01][1080p].mkv", Size: 400 << 20}, {RelativePath: "Show copy [01][1080p].mkv", Size: 400 << 20}},
	} {
		if resolved := ResolvePackageEpisodes(files, MediaTypeTV); resolved.Complete {
			t.Fatalf("unsafe package resolved=%+v", resolved)
		}
	}
}
