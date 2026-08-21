package medialibrary

import (
	"testing"
	"time"
)

func TestGroupRecognitionUnitsKeepsRootMoviesSeparateAndTVSeasonTogether(t *testing.T) {
	now := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	files := []File{
		{RelativePath: "/Seven.Samurai.1954.mkv", Size: 10, ModifiedAt: now},
		{RelativePath: "/Ikiru.1952.mkv", Size: 11, ModifiedAt: now},
		{RelativePath: "/Shogun/Season 01/Shogun.S01E01.mkv", Size: 12, ModifiedAt: now},
		{RelativePath: "/Shogun/Season 01/Shogun.S01E02.mkv", Size: 13, ModifiedAt: now},
	}
	units := GroupRecognitionUnits(files)
	if len(units) != 3 {
		t.Fatalf("units=%d, want 3: %#v", len(units), units)
	}
	var tv *RecognitionUnit
	for index := range units {
		if units[index].MediaTypeHint == "tv" {
			tv = &units[index]
		}
	}
	if tv == nil || len(tv.Files) != 2 || tv.PackageName != "Shogun" {
		t.Fatalf("tv unit=%#v", tv)
	}
}

func TestGroupRecognitionUnitsUsesDiscOuterDirectoryAndStableProviderIDs(t *testing.T) {
	now := time.Now().UTC()
	first := GroupRecognitionUnits([]File{{RelativePath: "/Seven Samurai/BDMV/STREAM/00000.m2ts", ProviderID: "cloud-1", ProviderIDStable: true, Size: 10, ModifiedAt: now}})
	second := GroupRecognitionUnits([]File{{RelativePath: "/Renamed/BDMV/STREAM/00000.m2ts", ProviderID: "cloud-1", ProviderIDStable: true, Size: 10, ModifiedAt: now}})
	if len(first) != 1 || first[0].PackageName != "Seven Samurai" {
		t.Fatalf("first=%#v", first)
	}
	if first[0].SourceKey != second[0].SourceKey {
		t.Fatalf("provider source key changed across rename: %s != %s", first[0].SourceKey, second[0].SourceKey)
	}
}
