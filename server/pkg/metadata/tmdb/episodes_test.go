package tmdb

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetTVSeasonEpisodesReturnsSafeBoundedSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/tv/5/season/1" || r.URL.Query().Get("language") != "zh-CN" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = io.WriteString(w, `{"season_number":1,"episodes":[{"id":101,"name":"第一集","overview":"本集简介","air_date":"2007-01-08","episode_number":1,"season_number":1,"runtime":47,"still_path":"/episode-1.jpg","vote_average":8.2},{"id":102,"name":"错误季","episode_number":2,"season_number":2},{"id":103,"name":"非法图片","episode_number":2,"season_number":1,"still_path":"https://unsafe.example/still.jpg?token=secret"}]}`)
	}))
	defer server.Close()

	client, err := NewForTest("test-token", server.URL+"/3", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	episodes, err := client.GetTVSeasonEpisodes(context.Background(), 5, 1, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes=%+v", episodes)
	}
	if episodes[0].EpisodeNumber != 1 || episodes[0].Name != "第一集" || episodes[0].Overview != "本集简介" || episodes[0].StillPath != "/episode-1.jpg" || episodes[0].RuntimeMinutes != 47 {
		t.Fatalf("episode one=%+v", episodes[0])
	}
	if episodes[1].EpisodeNumber != 2 || episodes[1].StillPath != "" {
		t.Fatalf("episode two=%+v", episodes[1])
	}
}

func TestGetTVSeasonEpisodesRejectsInvalidIdentity(t *testing.T) {
	client := &Client{}
	if _, err := client.GetTVSeasonEpisodes(context.Background(), 0, 1, "zh-CN"); ErrorCode(err) != ErrorInvalidRequest {
		t.Fatalf("err=%v", err)
	}
	if _, err := client.GetTVSeasonEpisodes(context.Background(), 5, -1, "zh-CN"); ErrorCode(err) != ErrorInvalidRequest {
		t.Fatalf("err=%v", err)
	}
}
