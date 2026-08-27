package aiprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func New(config Config) (Provider, error) {
	return newWithClient(config, nil)
}

func newWithClient(config Config, client *http.Client) (Provider, error) {
	config.ProviderType = strings.TrimSpace(config.ProviderType)
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	if config.APIKey == "" || len(config.APIKey) > 4096 || len(config.Model) > 256 || strings.ContainsAny(config.APIKey, "\r\n") || strings.ContainsAny(config.Model, "\r\n?#") {
		return nil, &Error{Code: ErrorInvalidConfig}
	}
	if client == nil {
		client = newSafeHTTPClient()
	}
	switch config.ProviderType {
	case ProviderOpenAICompatible:
		base, err := validateOpenAIBaseURL(config.BaseURL)
		if err != nil {
			return nil, err
		}
		config.BaseURL = base
		return &openAIProvider{config: config, http: client}, nil
	case ProviderGoogleAIStudio:
		if base := strings.TrimSpace(config.BaseURL); base != "" && strings.TrimSuffix(base, "/") != GoogleAIStudioBaseURL {
			return nil, &Error{Code: ErrorInvalidConfig}
		}
		config.BaseURL = GoogleAIStudioBaseURL
		return &googleProvider{config: config, http: client}, nil
	default:
		return nil, &Error{Code: ErrorInvalidConfig}
	}
}

type openAIProvider struct {
	config Config
	http   *http.Client
}

func (p *openAIProvider) Test(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	return err
}

func (p *openAIProvider) ListModels(ctx context.Context) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(p.config.BaseURL, "/v1/models"), nil)
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	p.authorize(request)
	var response struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := p.doModelListJSON(request, &response); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(response.Data))
	for _, item := range response.Data {
		id := cleanModelID(item.ID)
		if id != "" {
			name := strings.TrimSpace(item.Name)
			if name == "" || len([]rune(name)) > 256 {
				name = id
			}
			models = append(models, Model{ID: id, DisplayName: name})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (p *openAIProvider) GenerateStructured(ctx context.Context, structured StructuredRequest) ([]byte, error) {
	if err := validateStructuredRequest(structured); err != nil {
		return nil, err
	}
	result, err := p.generate(ctx, structured, true)
	if err == nil || ErrorCode(err) != ErrorSchemaUnsupported {
		return result, err
	}
	return p.generate(ctx, structured, false)
}

func (p *openAIProvider) generate(ctx context.Context, structured StructuredRequest, strictSchema bool) ([]byte, error) {
	payload, err := json.Marshal(structured.Payload)
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	responseFormat := map[string]any{"type": "json_object"}
	if strictSchema {
		responseFormat = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": structured.SchemaName, "strict": true, "schema": structured.Schema}}
	}
	body, err := json.Marshal(map[string]any{
		"model":           p.config.Model,
		"temperature":     0,
		"messages":        []map[string]string{{"role": "system", "content": structured.SystemPrompt}, {"role": "user", "content": string(payload)}},
		"response_format": responseFormat,
	})
	if err != nil || len(body) > maxStructuredRequestBytes {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint(p.config.BaseURL, "/v1/chat/completions"), bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	p.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.http.Do(request)
	if err != nil {
		return nil, &Error{Code: ErrorUnavailable, Cause: err}
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maxStructuredResponseBytes)
	if err != nil {
		return nil, err
	}
	if strictSchema && isSchemaUnsupported(response.StatusCode, responseBody) {
		return nil, &Error{Code: ErrorSchemaUnsupported}
	}
	if err := statusError(response.StatusCode); err != nil {
		return nil, err
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := decodeEnvelope(responseBody, &decoded); err != nil || len(decoded.Choices) != 1 {
		return nil, invalidResponse(err)
	}
	content := []byte(strings.TrimSpace(decoded.Choices[0].Message.Content))
	if len(content) == 0 || len(content) > maxStructuredResponseBytes {
		return nil, invalidResponse(nil)
	}
	return content, nil
}

func (p *openAIProvider) authorize(request *http.Request) {
	request.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	request.Header.Set("Accept", "application/json")
}

func (p *openAIProvider) doModelListJSON(request *http.Request, target any) error {
	response, err := p.http.Do(request)
	if err != nil {
		return &Error{Code: ErrorUnavailable, Cause: err}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxModelListResponseBytes)
	if err != nil {
		return err
	}
	if err := statusError(response.StatusCode); err != nil {
		return err
	}
	return decodeEnvelope(body, target)
}

type googleProvider struct {
	config Config
	http   *http.Client
}

func (p *googleProvider) Test(ctx context.Context) error {
	_, err := p.ListModels(ctx)
	return err
}

func (p *googleProvider) ListModels(ctx context.Context) ([]Model, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, GoogleAIStudioBaseURL+"/v1beta/models?pageSize=100", nil)
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	p.authorize(request)
	var response struct {
		Models []struct {
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := p.doModelListJSON(request, &response); err != nil {
		return nil, err
	}
	models := make([]Model, 0, len(response.Models))
	for _, item := range response.Models {
		if !contains(item.SupportedGenerationMethods, "generateContent") {
			continue
		}
		id := cleanModelID(strings.TrimPrefix(item.Name, "models/"))
		if id == "" {
			continue
		}
		name := strings.TrimSpace(item.DisplayName)
		if name == "" || len([]rune(name)) > 256 {
			name = id
		}
		models = append(models, Model{ID: id, DisplayName: name})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

func (p *googleProvider) GenerateStructured(ctx context.Context, structured StructuredRequest) ([]byte, error) {
	if err := validateStructuredRequest(structured); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(structured.Payload)
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	body, err := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": structured.SystemPrompt}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": string(payload)}}}},
		"generationConfig":  map[string]any{"temperature": 0, "responseMimeType": "application/json", "responseSchema": googleSchema(structured.Schema)},
	})
	if err != nil || len(body) > maxStructuredRequestBytes {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	model := url.PathEscape(strings.TrimPrefix(p.config.Model, "models/"))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, GoogleAIStudioBaseURL+"/v1beta/models/"+model+":generateContent", bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Code: ErrorInvalidConfig, Cause: err}
	}
	p.authorize(request)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.http.Do(request)
	if err != nil {
		return nil, &Error{Code: ErrorUnavailable, Cause: err}
	}
	defer response.Body.Close()
	responseBody, err := readBounded(response.Body, maxStructuredResponseBytes)
	if err != nil {
		return nil, err
	}
	if err := statusError(response.StatusCode); err != nil {
		return nil, err
	}
	var decoded struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := decodeEnvelope(responseBody, &decoded); err != nil || len(decoded.Candidates) != 1 || len(decoded.Candidates[0].Content.Parts) != 1 {
		return nil, invalidResponse(err)
	}
	content := []byte(strings.TrimSpace(decoded.Candidates[0].Content.Parts[0].Text))
	if len(content) == 0 || len(content) > maxStructuredResponseBytes {
		return nil, invalidResponse(nil)
	}
	return content, nil
}

func (p *googleProvider) authorize(request *http.Request) {
	request.Header.Set("x-goog-api-key", p.config.APIKey)
	request.Header.Set("Accept", "application/json")
}

func (p *googleProvider) doModelListJSON(request *http.Request, target any) error {
	response, err := p.http.Do(request)
	if err != nil {
		return &Error{Code: ErrorUnavailable, Cause: err}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, maxModelListResponseBytes)
	if err != nil {
		return err
	}
	if err := statusError(response.StatusCode); err != nil {
		return err
	}
	return decodeEnvelope(body, target)
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, &Error{Code: ErrorResponseInvalid, Cause: err}
	}
	if int64(len(body)) > maximum {
		return nil, &Error{Code: ErrorResponseTooLarge}
	}
	return body, nil
}

func decodeEnvelope(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return invalidResponse(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return invalidResponse(err)
	}
	return nil
}

func statusError(status int) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Code: ErrorAuthentication}
	case status == http.StatusTooManyRequests:
		return &Error{Code: ErrorRateLimited}
	case status >= 500:
		return &Error{Code: ErrorUnavailable}
	default:
		return &Error{Code: ErrorResponseInvalid}
	}
}

func isSchemaUnsupported(status int, body []byte) bool {
	if status != http.StatusBadRequest && status != http.StatusUnprocessableEntity && status != http.StatusNotFound {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, "json_schema") || strings.Contains(text, "response_format") || strings.Contains(text, "unsupported_parameter")
}

func googleSchema(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		if types, ok := typed["type"].([]string); ok && len(types) == 2 && contains(types, "null") {
			for _, item := range types {
				if item != "null" {
					copy["type"] = strings.ToUpper(item)
					copy["nullable"] = true
				}
			}
		}
		for key, item := range typed {
			if key == "type" {
				if _, handled := copy["type"]; handled {
					continue
				}
			}
			copy[key] = googleSchema(item)
		}
		return copy
	case []string:
		if len(typed) == 2 && contains(typed, "null") {
			for _, item := range typed {
				if item != "null" {
					return strings.ToUpper(item)
				}
			}
		}
		return typed
	case string:
		if contains([]string{"object", "array", "string", "integer", "number", "boolean"}, typed) {
			return strings.ToUpper(typed)
		}
		return typed
	default:
		return typed
	}
}

func cleanModelID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n?#\\") {
		return ""
	}
	return value
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
