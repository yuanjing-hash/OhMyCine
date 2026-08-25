package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDecodeDownloadRecognitionOverridePreservesOptionalEpisodeFacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	request := httptest.NewRequest("PUT", "/api/v1/downloads/task/recognition-override", strings.NewReader(`{"tmdb_id":289745,"media_type":"tv","season":1,"episode":9}`))
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	input, err := decodeDownloadRecognitionOverride(context)
	if err != nil {
		t.Fatal(err)
	}
	if input.TMDBID != 289745 || input.MediaType != "tv" || input.Season == nil || *input.Season != 1 || input.Episode == nil || *input.Episode != 9 {
		t.Fatalf("input=%+v", input)
	}
}

func TestDecodeDownloadRecognitionOverrideRejectsUnknownFields(t *testing.T) {
	request := httptest.NewRequest("PUT", "/api/v1/downloads/task/recognition-override", strings.NewReader(`{"tmdb_id":289745,"media_type":"tv","episode_number":9}`))
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	if _, err := decodeDownloadRecognitionOverride(context); err == nil {
		t.Fatal("unknown episode field was accepted")
	}
}
