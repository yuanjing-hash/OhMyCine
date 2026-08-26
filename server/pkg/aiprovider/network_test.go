package aiprovider

import (
	"net/http"
	"testing"
)

func TestValidateOpenAIBaseURLRejectsEmptyQueryFragmentAndPrivateOrigins(t *testing.T) {
	for _, raw := range []string{
		"https://api.example.com?",
		"https://api.example.com#",
		"https://127.0.0.1/v1",
		"https://api.example.com/api//v1",
		"https://api.example.com/api/../v1",
		"https://api.example.com/api/%76%31",
		"https://api.example.com/api\\v1",
	} {
		if _, err := validateOpenAIBaseURL(raw); err == nil {
			t.Fatalf("unsafe Base URL accepted: %q", raw)
		}
	}
	if got, err := validateOpenAIBaseURL("https://api.example.com/v1/"); err != nil || got != "https://api.example.com/v1" {
		t.Fatalf("normalized=%q err=%v", got, err)
	}
	if got, err := validateOpenAIBaseURL("https://openrouter.ai/api/v1/"); err != nil || got != "https://openrouter.ai/api/v1" {
		t.Fatalf("OpenRouter normalized=%q err=%v", got, err)
	}
}

func TestOpenAIEndpointAppendsVersionExactlyOnce(t *testing.T) {
	tests := map[string]string{
		"https://api.example.com":      "https://api.example.com/v1/models",
		"https://api.example.com/v1":   "https://api.example.com/v1/models",
		"https://openrouter.ai/api/v1": "https://openrouter.ai/api/v1/models",
	}
	for base, expected := range tests {
		if actual := endpoint(base, "/v1/models"); actual != expected {
			t.Fatalf("endpoint(%q)=%q, want %q", base, actual, expected)
		}
	}
}

func TestSafeAIHTTPClientDoesNotUseEnvironmentProxy(t *testing.T) {
	client := newSafeHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("safe AI transport must not use an environment proxy")
	}
}
