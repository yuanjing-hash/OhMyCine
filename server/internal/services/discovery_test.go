package services

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/database"
	"github.com/yuanjing-hash/ohmycine/server/pkg/discovery"
)

type discoveryRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip discoveryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type fakeDiscoveryProvider struct {
	code  string
	calls int
	fail  bool
}

type concurrentDiscoveryProvider struct {
	started chan string
	release chan struct{}
}

func (*concurrentDiscoveryProvider) Code() string { return discovery.ProviderTMDB }
func (*concurrentDiscoveryProvider) Sections() []discovery.SectionDefinition {
	items := make([]discovery.SectionDefinition, 4)
	for index := range items {
		items[index] = discovery.SectionDefinition{Code: "section-" + strconv.Itoa(index), Title: "栏目"}
	}
	return items
}
func (p *concurrentDiscoveryProvider) Fetch(ctx context.Context, request discovery.Request) (discovery.Section, error) {
	select {
	case p.started <- request.Section:
	case <-ctx.Done():
		return discovery.Section{}, ctx.Err()
	}
	select {
	case <-p.release:
		return discovery.Section{Provider: discovery.ProviderTMDB, Code: request.Section, Title: request.Section, Page: request.Page, TotalPage: 1, Items: []discovery.Work{}}, nil
	case <-ctx.Done():
		return discovery.Section{}, ctx.Err()
	}
}

func (p *fakeDiscoveryProvider) Code() string { return p.code }
func (p *fakeDiscoveryProvider) Sections() []discovery.SectionDefinition {
	return []discovery.SectionDefinition{{Code: "hot", Title: "热门", MediaType: "movie"}}
}
func (p *fakeDiscoveryProvider) Fetch(_ context.Context, request discovery.Request) (discovery.Section, error) {
	p.calls++
	if p.fail {
		return discovery.Section{}, errors.New("private upstream error")
	}
	year := 2026
	return discovery.Section{Provider: p.code, Code: request.Section, Title: "热门", MediaType: "movie", Page: request.Page, TotalPage: 1, Items: []discovery.Work{{Provider: p.code, ProviderID: "1", MediaType: "movie", Title: "测试", Year: &year}}}, nil
}

func TestDiscoveryServiceCachesAndFallsBackToStaleSnapshot(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "discovery.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	provider := &fakeDiscoveryProvider{code: "tmdb"}
	service := NewDiscoveryServiceWithProviders(db, []discovery.Provider{provider}, zerolog.Nop())
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	actor := Actor{Permissions: map[string]struct{}{authz.PermissionDiscoveryRead: {}}}
	first, err := service.Section(context.Background(), actor, "tmdb", "hot", 1, "zh-CN", "CN", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Section(context.Background(), actor, "tmdb", "hot", 1, "zh-CN", "CN", false)
	if err != nil {
		t.Fatal(err)
	}
	if provider.calls != 1 || first.Stale || second.Stale {
		t.Fatalf("calls=%d first=%+v second=%+v", provider.calls, first, second)
	}
	provider.fail = true
	now = now.Add(discoveryFreshTTL + time.Minute)
	stale, err := service.Section(context.Background(), actor, "tmdb", "hot", 1, "zh-CN", "CN", false)
	if err != nil {
		t.Fatal(err)
	}
	if !stale.Stale || stale.ErrorCode != CodeDiscoveryProviderUnavailable || len(stale.Items) != 1 {
		t.Fatalf("stale=%+v", stale)
	}
}

func TestDiscoveryServiceRequiresPermissionAndValidSection(t *testing.T) {
	provider := &fakeDiscoveryProvider{code: "tmdb"}
	service := NewDiscoveryServiceWithProviders(nil, []discovery.Provider{provider}, zerolog.Nop())
	if _, err := service.Section(context.Background(), Actor{}, "tmdb", "hot", 1, "", "", false); ErrorCode(err) != CodePermissionDenied {
		t.Fatalf("permission err=%v", err)
	}
	actor := Actor{Permissions: map[string]struct{}{authz.PermissionDiscoveryRead: {}}}
	if _, err := service.Section(context.Background(), actor, "tmdb", "bad", 1, "", "", false); ErrorCode(err) != CodeDiscoverySectionInvalid {
		t.Fatalf("section err=%v", err)
	}
}

func TestDiscoveryOverviewFetchesSectionsWithBoundedConcurrency(t *testing.T) {
	db, err := database.Open(filepath.Join(t.TempDir(), "overview.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	provider := &concurrentDiscoveryProvider{started: make(chan string, 4), release: make(chan struct{})}
	service := NewDiscoveryServiceWithProviders(db, []discovery.Provider{provider}, zerolog.Nop())
	actor := Actor{Permissions: map[string]struct{}{authz.PermissionDiscoveryRead: {}}}
	done := make(chan error, 1)
	go func() {
		_, overviewErr := service.Overview(context.Background(), actor, discovery.ProviderTMDB, 1, false)
		done <- overviewErr
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-provider.started:
		case <-time.After(time.Second):
			close(provider.release)
			t.Fatal("recommendation sections were fetched serially")
		}
	}
	close(provider.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent recommendation overview did not finish")
	}
}

func TestDiscoveryDoubanImageUsesRequiredAntiHotlinkHeaders(t *testing.T) {
	service := NewDiscoveryServiceWithProviders(nil, nil, zerolog.Nop())
	service.imageHTTP = &http.Client{Transport: discoveryRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Referer") != "https://movie.douban.com/" || request.Header.Get("User-Agent") == "" {
			t.Fatalf("headers=%v", request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/jpeg"}}, Body: io.NopCloser(bytes.NewReader([]byte("jpeg")))}, nil
	})}
	body, contentType, err := service.downloadDoubanImage(context.Background(), "https://img9.doubanio.com/view/photo/test.jpg")
	if err != nil || string(body) != "jpeg" || contentType != "image/jpeg" {
		t.Fatalf("body=%q contentType=%q err=%v", body, contentType, err)
	}
}
