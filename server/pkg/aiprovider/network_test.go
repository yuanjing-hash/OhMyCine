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
		"https://api.example.com/v2",
	} {
		if _, err := validateOpenAIBaseURL(raw); err == nil {
			t.Fatalf("unsafe Base URL accepted: %q", raw)
		}
	}
	if got, err := validateOpenAIBaseURL("https://api.example.com/v1/"); err != nil || got != "https://api.example.com/v1" {
		t.Fatalf("normalized=%q err=%v", got, err)
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
