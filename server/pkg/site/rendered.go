package site

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type RenderedFetchRequest struct {
	ProfileID    string
	URL          string
	AllowedHosts []string
	UserAgent    string
	Timeout      time.Duration
	MaxBytes     int64
}

type RenderedPage struct {
	HTML       []byte
	StatusCode int
	FinalURL   string
	UserAgent  string
}

type RenderedFetcher interface {
	Fetch(context.Context, RenderedFetchRequest) (RenderedPage, error)
	Health(context.Context) error
}

type FlareSolverrFetcher struct {
	endpoint *url.URL
	client   *http.Client
}

type CloakBrowserFetcher struct {
	endpoint *url.URL
	client   *http.Client
}

func NewFlareSolverrFetcher(raw string) (*FlareSolverrFetcher, error) {
	endpoint, err := renderedServiceEndpoint(raw, "/v1", false)
	if err != nil {
		return nil, err
	}
	return &FlareSolverrFetcher{endpoint: endpoint, client: renderedServiceClient(endpoint, 35*time.Second)}, nil
}

// NewCloakBrowserFetcher connects only to an explicitly installed loopback
// companion. OhMyCine never downloads or redistributes the browser binary.
func NewCloakBrowserFetcher(raw string) (*CloakBrowserFetcher, error) {
	endpoint, err := renderedServiceEndpoint(raw, "/v1/render", true)
	if err != nil {
		return nil, err
	}
	return &CloakBrowserFetcher{endpoint: endpoint, client: renderedServiceClient(endpoint, 35*time.Second)}, nil
}

func FetchRendered(ctx context.Context, config Config, request RenderedFetchRequest) (RenderedPage, error) {
	request.Timeout = boundedRenderedTimeout(request.Timeout)
	if request.UserAgent == "" {
		request.UserAgent = config.UserAgent
	}
	if err := validatePublicRenderedTarget(request); err != nil {
		return RenderedPage{}, err
	}
	if config.RenderedFetcher != nil {
		page, err := config.RenderedFetcher.Fetch(ctx, request)
		if err == nil || !errors.Is(err, ErrUnavailable) || strings.TrimSpace(config.BrowserServiceURL) == "" {
			return page, err
		}
	}
	if strings.TrimSpace(config.BrowserServiceURL) == "" {
		return RenderedPage{}, ErrUnavailable
	}
	fallback, err := NewFlareSolverrFetcher(config.BrowserServiceURL)
	if err != nil {
		return RenderedPage{}, ErrUnavailable
	}
	return fallback.Fetch(ctx, request)
}

func (f *FlareSolverrFetcher) Fetch(ctx context.Context, request RenderedFetchRequest) (RenderedPage, error) {
	if err := validateRenderedTarget(request); err != nil {
		return RenderedPage{}, err
	}
	// Rendered fetchers are intentionally credential-free. PT Cookie/passkey
	// remain inside the Server's direct tracker client and public BT profiles do
	// not have site credentials to forward to an auxiliary browser service.
	payload, _ := json.Marshal(map[string]any{"cmd": "request.get", "url": request.URL, "maxTimeout": int(boundedRenderedTimeout(request.Timeout).Milliseconds())})
	raw, err := f.post(ctx, payload, renderedEnvelopeLimit(request.MaxBytes))
	if err != nil {
		return RenderedPage{}, err
	}
	var envelope struct {
		Status   string `json:"status"`
		Solution struct {
			Response  string `json:"response"`
			Status    int    `json:"status"`
			URL       string `json:"url"`
			UserAgent string `json:"userAgent"`
		} `json:"solution"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Status != "ok" || envelope.Solution.Response == "" || int64(len(envelope.Solution.Response)) > request.MaxBytes {
		return RenderedPage{}, ErrInvalidReply
	}
	status := envelope.Solution.Status
	if status == 0 {
		status = http.StatusOK
	}
	finalURL := strings.TrimSpace(envelope.Solution.URL)
	if finalURL == "" {
		finalURL = request.URL
	}
	if err := validateRenderedFinalURL(finalURL, request.AllowedHosts); err != nil {
		return RenderedPage{}, err
	}
	if err := renderedStatusError(status); err != nil {
		return RenderedPage{}, err
	}
	return RenderedPage{HTML: []byte(envelope.Solution.Response), StatusCode: status, FinalURL: finalURL, UserAgent: safeRenderedUserAgent(envelope.Solution.UserAgent)}, nil
}

func (f *FlareSolverrFetcher) Health(ctx context.Context) error {
	payload, _ := json.Marshal(map[string]any{"cmd": "sessions.list"})
	_, err := f.post(ctx, payload, 256<<10)
	return err
}

func (f *FlareSolverrFetcher) post(ctx context.Context, payload []byte, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(request)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrInvalidReply
	}
	return raw, nil
}

func (f *CloakBrowserFetcher) Fetch(ctx context.Context, request RenderedFetchRequest) (RenderedPage, error) {
	if err := validatePublicRenderedTarget(request); err != nil {
		return RenderedPage{}, err
	}
	payload, _ := json.Marshal(map[string]any{"profile_id": request.ProfileID, "url": request.URL, "timeout_ms": int(boundedRenderedTimeout(request.Timeout).Milliseconds()), "user_agent": safeRenderedUserAgent(request.UserAgent), "max_bytes": request.MaxBytes})
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return RenderedPage{}, ErrUnavailable
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := f.client.Do(httpRequest)
	if err != nil {
		return RenderedPage{}, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return RenderedPage{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, renderedEnvelopeLimit(request.MaxBytes)+1))
	if err != nil || int64(len(raw)) > renderedEnvelopeLimit(request.MaxBytes) {
		return RenderedPage{}, ErrInvalidReply
	}
	var envelope struct {
		StatusCode int    `json:"status"`
		HTML       string `json:"html"`
		FinalURL   string `json:"final_url"`
		UserAgent  string `json:"user_agent"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.HTML == "" || int64(len(envelope.HTML)) > request.MaxBytes {
		return RenderedPage{}, ErrInvalidReply
	}
	if envelope.StatusCode == 0 {
		envelope.StatusCode = http.StatusOK
	}
	if envelope.FinalURL == "" {
		envelope.FinalURL = request.URL
	}
	if err := validateRenderedFinalURL(envelope.FinalURL, request.AllowedHosts); err != nil {
		return RenderedPage{}, err
	}
	if err := renderedStatusError(envelope.StatusCode); err != nil {
		return RenderedPage{}, err
	}
	return RenderedPage{HTML: []byte(envelope.HTML), StatusCode: envelope.StatusCode, FinalURL: envelope.FinalURL, UserAgent: safeRenderedUserAgent(envelope.UserAgent)}, nil
}

func (f *CloakBrowserFetcher) Health(ctx context.Context) error {
	endpoint := *f.endpoint
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/v1/render") + "/health"
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	response, err := f.client.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ErrUnavailable
	}
	return nil
}

func validateRenderedTarget(request RenderedFetchRequest) error {
	if request.ProfileID == "" || len(request.ProfileID) > 64 || strings.ContainsAny(request.ProfileID, "\x00\r\n/\\") || request.MaxBytes < 1 || request.MaxBytes > 8<<20 {
		return ErrInvalidReply
	}
	return validateRenderedFinalURL(request.URL, request.AllowedHosts)
}

// publicRenderedProfileHosts is the trust root for automatic rendered
// fetching. Request-provided hosts can only narrow this set; they can never
// turn the fetcher into an arbitrary-URL browser proxy.
var publicRenderedProfileHosts = map[string][]string{
	"1337x": {"1337x.to"},
	"extto": {"ext.to"},
}

func validatePublicRenderedTarget(request RenderedFetchRequest) error {
	hosts, ok := publicRenderedProfileHosts[strings.ToLower(strings.TrimSpace(request.ProfileID))]
	if !ok || !sameRenderedHostSet(request.AllowedHosts, hosts) {
		return ErrUnavailable
	}
	request.AllowedHosts = hosts
	return validateRenderedTarget(request)
}

func sameRenderedHostSet(provided, registered []string) bool {
	if len(provided) == 0 || len(registered) == 0 {
		return false
	}
	for _, candidate := range provided {
		candidate = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(candidate)), ".")
		for _, allowed := range registered {
			if candidate == strings.TrimSuffix(strings.ToLower(strings.TrimSpace(allowed)), ".") {
				return true
			}
		}
	}
	return false
}

func validateRenderedFinalURL(raw string, allowedHosts []string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return ErrUnavailable
	}
	for _, host := range allowedHosts {
		if strings.EqualFold(strings.TrimSuffix(parsed.Hostname(), "."), strings.TrimSuffix(strings.TrimSpace(host), ".")) {
			return nil
		}
	}
	return ErrUnavailable
}

func renderedServiceEndpoint(raw, suffix string, loopbackOnly bool) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, ErrUnavailable
	}
	if loopbackOnly && !isLoopbackHost(endpoint.Hostname()) {
		return nil, ErrUnavailable
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + suffix
	return endpoint, nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func renderedServiceClient(endpoint *url.URL, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 2 || next.URL.Scheme != endpoint.Scheme || !strings.EqualFold(next.URL.Host, endpoint.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

func renderedStatusError(status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuthentication
	case http.StatusTooManyRequests:
		return ErrRateLimited
	default:
		return ErrUnavailable
	}
}

func boundedRenderedTimeout(value time.Duration) time.Duration {
	if value < 3*time.Second || value > 30*time.Second {
		return 12 * time.Second
	}
	return value
}

func renderedEnvelopeLimit(htmlLimit int64) int64 {
	limit := htmlLimit*8 + 64<<10
	if limit > 64<<20 {
		return 64 << 20
	}
	return limit
}

func safeRenderedUserAgent(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}
