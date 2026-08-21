package pan115

import (
	"context"
	"net/http"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

type directURLSDK struct {
	*bulkSDK
	info      *pan115sdk.DownloadInfo
	pickCode  string
	userAgent string
}

func (s *directURLSDK) DownloadWithUA(pickCode, userAgent string) (*pan115sdk.DownloadInfo, error) {
	s.pickCode = pickCode
	s.userAgent = userAgent
	return s.info, nil
}

func TestDirectURLNormalizesSDKAcquisitionHeadersToUserAgentRequirement(t *testing.T) {
	const userAgent = "Emby Web/4.9.5.0"
	sdk := &directURLSDK{
		bulkSDK: &bulkSDK{},
		info: &pan115sdk.DownloadInfo{
			Url: pan115sdk.FileDownloadUrl{Url: "https://cdn.example.test/video?expires=1787300000", Valid: true},
			Header: http.Header{
				"User-Agent":   {userAgent},
				"Cookie":       {"UID=private; download_token=private"},
				"Content-Type": {"application/x-www-form-urlencoded"},
				"Referer":      {"https://115.com/"},
			},
		},
	}
	client := &Client{
		sdk: sdk, directRate: rate.NewLimiter(rate.Inf, 1), callSlots: make(chan struct{}, maxInFlightCalls),
		now: time.Now, jitter: func() time.Duration { return 0 },
	}

	temporary, err := client.DirectURL(context.Background(), cloud.DirectURLRequest{PickCode: "private-pickcode", UserAgent: "  " + userAgent + "  "})
	if err != nil {
		t.Fatal(err)
	}
	if sdk.pickCode != "private-pickcode" || sdk.userAgent != userAgent {
		t.Fatalf("download binding pickcode=%q user-agent=%q", sdk.pickCode, sdk.userAgent)
	}
	if len(temporary.Headers) != 1 || temporary.Headers.Get("User-Agent") != userAgent {
		t.Fatalf("temporary headers were not normalized: names=%v", headerNames(temporary.Headers))
	}
	if temporary.Headers.Get("Cookie") != "" || temporary.Headers.Get("Referer") != "" || temporary.Headers.Get("Content-Type") != "" {
		t.Fatalf("SDK acquisition headers escaped provider boundary: names=%v", headerNames(temporary.Headers))
	}
}

func headerNames(headers http.Header) []string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	return names
}
