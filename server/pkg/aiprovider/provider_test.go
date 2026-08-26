package aiprovider

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestOpenAICompatibleModelsAndStructuredSchemaFallback(t *testing.T) {
	var mu sync.Mutex
	var requests []*http.Request
	call := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Clone(request.Context()))
		mu.Unlock()
		if request.Header.Get("Authorization") != "Bearer secret-key" {
			t.Fatalf("authorization header=%q", request.Header.Get("Authorization"))
		}
		if request.Method == http.MethodGet {
			return jsonResponse(200, `{"data":[{"id":"gpt-z"},{"id":"gpt-a"}]}`), nil
		}
		call++
		body, _ := io.ReadAll(request.Body)
		if call == 1 {
			if !strings.Contains(string(body), `"type":"json_schema"`) || !strings.Contains(string(body), `"strict":true`) {
				t.Fatalf("strict request=%s", body)
			}
			return jsonResponse(400, `{"error":{"code":"unsupported_parameter","message":"response_format json_schema unsupported"}}`), nil
		}
		if !strings.Contains(string(body), `"type":"json_object"`) {
			t.Fatalf("fallback request=%s", body)
		}
		return jsonResponse(200, `{"choices":[{"message":{"content":"{\"action\":\"unknown\"}"}}]}`), nil
	})}
	provider, err := newWithClient(Config{ProviderType: ProviderOpenAICompatible, BaseURL: "https://api.example.com/v1", APIKey: "secret-key", Model: "gpt-test"}, client)
	if err != nil {
		t.Fatal(err)
	}
	models, err := provider.ListModels(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "gpt-a" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	result, err := provider.GenerateStructured(context.Background(), StructuredRequest{SystemPrompt: "return JSON", Payload: map[string]string{"title": "safe"}, SchemaName: "fixture", Schema: map[string]any{"type": "object"}})
	if err != nil || string(result) != `{"action":"unknown"}` || call != 2 {
		t.Fatalf("result=%s calls=%d err=%v", result, call, err)
	}
	if requests[0].URL.Path != "/v1/models" || requests[1].URL.Path != "/v1/chat/completions" {
		t.Fatalf("paths=%q %q", requests[0].URL.Path, requests[1].URL.Path)
	}
}

func TestGoogleAIStudioUsesNativeProtocolAndFiltersModels(t *testing.T) {
	var generated map[string]any
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("x-goog-api-key") != "google-key" || request.Header.Get("Authorization") != "" {
			t.Fatalf("unsafe Google auth headers: %+v", request.Header)
		}
		if request.Method == http.MethodGet {
			if request.URL.Host != "generativelanguage.googleapis.com" || request.URL.Path != "/v1beta/models" {
				t.Fatalf("models URL=%s", request.URL)
			}
			return jsonResponse(200, `{"models":[{"name":"models/gemini-flash","displayName":"Gemini Flash","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`), nil
		}
		if request.URL.Path != "/v1beta/models/gemini-flash:generateContent" {
			t.Fatalf("generate URL=%s", request.URL)
		}
		body, _ := io.ReadAll(request.Body)
		if err := json.Unmarshal(body, &generated); err != nil {
			t.Fatal(err)
		}
		return jsonResponse(200, `{"candidates":[{"content":{"parts":[{"text":"{\"action\":\"unknown\"}"}]}}]}`), nil
	})}
	provider, err := newWithClient(Config{ProviderType: ProviderGoogleAIStudio, APIKey: "google-key", Model: "gemini-flash"}, client)
	if err != nil {
		t.Fatal(err)
	}
	models, err := provider.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "gemini-flash" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	if _, err := provider.GenerateStructured(context.Background(), StructuredRequest{SystemPrompt: "return JSON", Payload: map[string]string{"title": "safe"}, SchemaName: "fixture", Schema: map[string]any{"type": "object", "properties": map[string]any{"action": map[string]any{"type": "string"}}}}); err != nil {
		t.Fatal(err)
	}
	generationConfig, _ := generated["generationConfig"].(map[string]any)
	if generationConfig["responseMimeType"] != "application/json" {
		t.Fatalf("generation config=%+v", generationConfig)
	}
	responseSchema, _ := generationConfig["responseSchema"].(map[string]any)
	if responseSchema["type"] != "OBJECT" {
		t.Fatalf("Google schema=%+v", responseSchema)
	}
}

func TestStrictDomainResultsRejectInventedCandidatesExtraFieldsAndRanges(t *testing.T) {
	payload := CandidateArbitrationPayload{Release: ArbitrationRelease{Title: "Fixture"}, Candidates: []ArbitrationCandidate{{CandidateRef: "c1", Title: "Fixture", MediaType: "tv"}}}
	valid := `{"action":"select","candidate_ref":"c1","normalized_title":"Fixture","media_type":"tv","year":2026,"season":1,"episode_start":1,"episode_end":2,"confidence":0.9,"reason_code":"title_alias_match"}`
	if result, err := DecodeCandidateArbitration([]byte(valid), payload); err != nil || result.CandidateRef != "c1" {
		t.Fatalf("valid result=%+v err=%v", result, err)
	}
	for _, raw := range []string{
		strings.Replace(valid, `"c1"`, `"invented"`, 1),
		strings.TrimSuffix(valid, "}") + `,"explanation":"leak"}`,
		strings.Replace(valid, `"episode_end":2`, `"episode_end":0`, 1),
		valid + `{}`,
	} {
		if _, err := DecodeCandidateArbitration([]byte(raw), payload); ErrorCode(err) != ErrorResponseInvalid {
			t.Fatalf("unsafe response accepted: %s err=%v", raw, err)
		}
	}
	rewrite := `{"action":"search","primary_title":"标准标题","original_title":null,"aliases":["Alias"],"media_type":"movie","year":2026,"season":null,"episode_start":null,"episode_end":null,"search_queries":[{"title":"标准标题","media_type":"movie","year":2026,"language_hint":"zh-CN"}],"confidence":0.8,"reason_code":"release_tags_removed"}`
	if _, err := DecodeTitleRewrite([]byte(rewrite)); err != nil {
		t.Fatal(err)
	}
}

func TestOpenAIBaseURLAndDialerRejectSSRFAddresses(t *testing.T) {
	for _, raw := range []string{"http://api.example.com", "https://127.0.0.1", "https://10.0.0.1", "https://api.example.com:8443", "https://user@api.example.com", "https://api.example.com/path", "https://api.example.com?key=x"} {
		if _, err := validateOpenAIBaseURL(raw); ErrorCode(err) != ErrorInvalidConfig {
			t.Fatalf("unsafe base accepted: %s err=%v", raw, err)
		}
	}
	dial := safeDialContext(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	})
	if _, err := dial(context.Background(), "tcp", "api.example.com:443"); err == nil {
		t.Fatal("private DNS answer was accepted")
	}
}
