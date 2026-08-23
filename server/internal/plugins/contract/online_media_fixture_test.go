package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGoContractConsumesSharedSDKOnlineMediaFixture(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "..", "..", "plugin-sdk", "fixtures", "online-media.v1.json")
	payload, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SchemaVersion int             `json:"schemaVersion"`
		Feed          json.RawMessage `json:"feed"`
		Playback      PlaybackPlan    `json:"playback"`
	}
	if err := json.Unmarshal(payload, &fixture); err != nil || fixture.SchemaVersion != 1 {
		t.Fatalf("shared fixture schema invalid: version=%d err=%v", fixture.SchemaVersion, err)
	}
	normalized, err := NormalizeFeedSections(fixture.Feed, "11111111-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	var sections []FeedSection
	if err := json.Unmarshal(normalized, &sections); err != nil || len(sections) != 1 || len(sections[0].Items) != 1 || len(sections[0].Items[0].Work.Segments) != 1 || len(sections[0].Items[0].Work.Segments[0].Versions) != 1 || len(sections[0].Items[0].Work.Segments[0].Versions[0].Variants) != 1 {
		t.Fatalf("shared feed hierarchy drifted: sections=%+v err=%v", sections, err)
	}
	if got := sections[0].Items[0].Actions; len(got) != 2 || got[0].ID != "favorite.add" || got[1].ID != "history.remove" || !got[1].RequiresConfirmation || !got[1].Destructive {
		t.Fatalf("shared action contract drifted: %+v", got)
	}
	if err := ValidatePlaybackPlan(fixture.Playback, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Playback.Assets) != 2 || fixture.Playback.Assets[0].Kind != "dash-video" || fixture.Playback.Assets[1].Kind != "dash-audio" || len(fixture.Playback.Subtitles) != 1 || len(fixture.Playback.Danmaku) != 1 || fixture.Playback.SelectionToken == "" {
		t.Fatalf("shared playback contract drifted: %+v", fixture.Playback)
	}
}
