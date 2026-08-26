package aiprovider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type lookupIP func(context.Context, string) ([]net.IPAddr, error)

func validateOpenAIBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return "", wrapInvalidConfig("base URL too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") {
		return "", wrapInvalidConfig("base URL must be an HTTPS origin")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", wrapInvalidConfig("base URL port is not allowed")
	}
	if ip := net.ParseIP(strings.TrimSuffix(parsed.Hostname(), ".")); ip != nil && !publicIP(ip) {
		return "", wrapInvalidConfig("base URL address is not public")
	}
	cleanPath := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if cleanPath != "" && cleanPath != "/v1" {
		return "", wrapInvalidConfig("base URL path must be empty or /v1")
	}
	parsed.Path = cleanPath
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

func newSafeHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A user-configured environment proxy would make DialContext validate the
	// proxy address instead of the requested AI origin. Disable it so DNS/IP
	// validation always applies to the actual custom Base URL.
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.DialContext = safeDialContext(net.DefaultResolver.LookupIPAddr)
	return &http.Client{
		Transport:     transport,
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func safeDialContext(resolve lookupIP) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := resolve(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("resolve AI provider host: %w", err)
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		for _, address := range addresses {
			if !publicIP(address.IP) {
				return nil, fmt.Errorf("AI provider resolved to a non-public address")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
}

func publicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() &&
		!ip.IsUnspecified() && !ip.IsMulticast()
}

func endpoint(base, path string) string {
	if strings.HasSuffix(base, "/v1") && strings.HasPrefix(path, "/v1/") {
		return base + strings.TrimPrefix(path, "/v1")
	}
	return strings.TrimSuffix(base, "/") + path
}
