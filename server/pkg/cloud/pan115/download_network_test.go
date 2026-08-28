package pan115

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestValidatePublicDownloadURLRejectsSSRFAndHTTPSDowngrade(t *testing.T) {
	public := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	private := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	valid, _ := url.Parse("https://cdn.example.test/media?id=token")
	if err := validatePublicDownloadURL(context.Background(), valid, public); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"http://cdn.example.test/media", "https://user:pass@cdn.example.test/media", "https://cdn.example.test:8443/media", "https://cdn.example.test/media#fragment"} {
		target, _ := url.Parse(raw)
		if err := validatePublicDownloadURL(context.Background(), target, public); err == nil {
			t.Fatalf("unsafe target accepted: %s", raw)
		}
	}
	if err := validatePublicDownloadURL(context.Background(), valid, private); err == nil {
		t.Fatal("private DNS answer was accepted")
	}
	for _, raw := range []string{"100.64.0.1", "198.18.0.1", "192.0.2.1", "2001:db8::1"} {
		if isPublicDownloadIP(net.ParseIP(raw)) {
			t.Fatalf("reserved/non-public address accepted: %s", raw)
		}
	}
}

func TestDownloadRedirectRevalidatesTargetAndDropsSensitiveHeaders(t *testing.T) {
	client := newDownloadHTTPClient()
	request, _ := http.NewRequest(http.MethodGet, "https://8.8.8.8/media", nil)
	request.Header.Set("User-Agent", "115Browser")
	request.Header.Set("Range", "bytes=10-")
	request.Header.Set("Cookie", "private")
	request.Header.Set("Authorization", "private")
	previous, _ := http.NewRequest(http.MethodGet, "https://cdn.example.test/media", nil)
	if err := client.CheckRedirect(request, []*http.Request{previous}); err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Cookie") != "" || request.Header.Get("Authorization") != "" || request.Header.Get("User-Agent") != "115Browser" || request.Header.Get("Range") != "bytes=10-" {
		t.Fatalf("redirect headers=%v", request.Header)
	}
	downgrade, _ := http.NewRequest(http.MethodGet, "http://8.8.8.8/media", nil)
	if err := client.CheckRedirect(downgrade, []*http.Request{previous}); err == nil {
		t.Fatal("HTTPS downgrade redirect was accepted")
	}
}

func TestPublicDownloadDialerRejectsDNSRebinding(t *testing.T) {
	private := func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.2")}}, nil
	}
	if _, err := publicDownloadDialContext(private)(context.Background(), "tcp", "cdn.example.test:443"); err == nil {
		t.Fatal("private rebound address was dialed")
	}
}
