package handlers

import (
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseSearchSiteScopeAcceptsOrderedDeduplicatedMultiSiteSelection(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest("GET", "/api/v1/discovery/torrent-search?site_ids=3&site_ids=1,3", nil)
	siteID, siteIDs, err := parseSearchSiteScope(context)
	if err != nil || siteID != nil || !reflect.DeepEqual(siteIDs, []uint{3, 1}) {
		t.Fatalf("site_id=%v site_ids=%v err=%v", siteID, siteIDs, err)
	}
}

func TestParseSearchSiteScopeRejectsAmbiguousOrEmptySelection(t *testing.T) {
	for _, target := range []string{
		"/api/v1/discovery/torrent-search?site_id=1&site_ids=2",
		"/api/v1/discovery/torrent-search?site_ids=",
		"/api/v1/discovery/torrent-search?site_ids=0",
	} {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest("GET", target, nil)
		if _, _, err := parseSearchSiteScope(context); err == nil {
			t.Fatalf("unsafe scope accepted: %s", target)
		}
	}
}
