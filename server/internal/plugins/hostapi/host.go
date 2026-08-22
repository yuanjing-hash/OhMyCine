package hostapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"gorm.io/gorm"
)

const (
	OperationHTTP          uint32 = 1
	OperationStorageGet    uint32 = 2
	OperationStorageSet    uint32 = 3
	OperationLog           uint32 = 4
	OperationNow           uint32 = 5
	OperationEventPoll     uint32 = 6
	OperationAssetRegister uint32 = 7

	maxHostRequestBytes  = 256 * 1024
	maxAssetRequestBytes = 4 * 1024 * 1024
	maxHTTPResponseBytes = 2 * 1024 * 1024
	maxHostResponseBytes = 4 * 1024 * 1024
	maxEventPayloadBytes = 64 * 1024
	maxEventsPerPlugin   = 64
)

var (
	privateKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	operationPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	forbiddenLogKey   = regexp.MustCompile(`(?i)(authorization|cookie|password|secret|token|api[_-]?key|passkey|credential|path|url)`)
)

type Error struct {
	Code   string
	Denied bool
	Cause  error
}

func (e *Error) Error() string          { return e.Code }
func (e *Error) Unwrap() error          { return e.Cause }
func (e *Error) PermissionDenied() bool { return e.Denied }

type Resolver func(context.Context, string) ([]net.IPAddr, error)

type Host struct {
	db          *gorm.DB
	credentials *credential.Store
	log         zerolog.Logger
	client      *http.Client
	resolve     Resolver
	now         func() time.Time
	eventsMu    sync.Mutex
	eventSeq    uint64
	events      map[string][]eventRecord
	assetsMu    sync.Mutex
	assets      map[string]Asset
}

type Option func(*Host)

func WithHTTPClient(client *http.Client) Option { return func(host *Host) { host.client = client } }
func WithResolver(resolver Resolver) Option     { return func(host *Host) { host.resolve = resolver } }

func New(db *gorm.DB, credentials *credential.Store, log zerolog.Logger, options ...Option) *Host {
	host := &Host{
		db: db, credentials: credentials, log: log,
		resolve: net.DefaultResolver.LookupIPAddr,
		now:     time.Now,
		events:  make(map[string][]eventRecord),
		assets:  make(map[string]Asset),
	}
	for _, option := range options {
		option(host)
	}
	if host.client == nil {
		host.client = host.defaultHTTPClient()
	}
	return host
}

func (host *Host) defaultHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Plugin traffic never inherits an ambient proxy. Besides making the
	// destination less auditable, a proxy would bypass the dial-time IP check.
	transport.Proxy = nil
	transport.DialContext = host.dialPublicContext
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func (host *Host) dialPublicContext(ctx context.Context, network, address string) (net.Conn, error) {
	hostname, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, denied("plugin_http_dial_denied", err)
	}
	addresses, err := host.resolve(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return nil, denied("plugin_http_dns_unavailable", err)
	}
	if err := requirePublicAddresses(addresses); err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, resolved := range addresses {
		if resolved.IP == nil || (network == "tcp4" && resolved.IP.To4() == nil) || (network == "tcp6" && resolved.IP.To4() != nil) {
			continue
		}
		connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if dialErr == nil {
			return connection, nil
		}
		lastErr = dialErr
	}
	return nil, invalid("plugin_http_upstream_unavailable", lastErr)
}

func CredentialPurpose(pluginID, connectionID, scope string) string {
	return "plugin-connection:" + pluginID + ":" + connectionID + ":" + scope
}

func PrivateValuePurpose(pluginID, connectionID, key string) string {
	return "plugin-kv:" + pluginID + ":" + connectionID + ":" + key
}

func (host *Host) Call(ctx context.Context, pluginID string, operation uint32, payload []byte) ([]byte, error) {
	if len(payload) > maxHostRequestBytes && (operation != OperationAssetRegister || len(payload) > maxAssetRequestBytes) {
		return nil, invalid("plugin_host_request_too_large", nil)
	}
	permissions, err := host.permissions(pluginID)
	if err != nil {
		return nil, err
	}
	var response any
	switch operation {
	case OperationHTTP:
		response, err = host.http(ctx, pluginID, permissions, payload)
	case OperationStorageGet:
		response, err = host.storageGet(pluginID, permissions, payload)
	case OperationStorageSet:
		response, err = host.storageSet(pluginID, permissions, payload)
	case OperationLog:
		response, err = host.writeLog(pluginID, payload)
	case OperationNow:
		if len(bytes.TrimSpace(payload)) > 0 && string(bytes.TrimSpace(payload)) != "{}" {
			err = invalid("plugin_host_request_invalid", nil)
		} else {
			response = map[string]string{"now": host.now().UTC().Format(time.RFC3339Nano)}
		}
	case OperationEventPoll:
		response, err = host.pollEvents(pluginID, permissions, payload)
	case OperationAssetRegister:
		response, err = host.registerAsset(ctx, pluginID, permissions, payload)
	default:
		err = invalid("plugin_host_operation_invalid", nil)
	}
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(map[string]any{"ok": true, "data": response})
	if err != nil || len(encoded) > maxHostResponseBytes {
		return nil, invalid("plugin_host_response_invalid", err)
	}
	return encoded, nil
}

type Asset struct {
	PluginID    string
	URL         string
	Headers     http.Header
	ExpiresAt   time.Time
	Body        []byte
	ContentType string
}

// AssetStream is the only supported bridge from a registered opaque asset to
// the authenticated Player gateway. Callers must close Body. Header contains
// only media transport metadata and never upstream cookies or authorization.
type AssetStream struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

type assetRegisterRequest struct {
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers,omitempty"`
	TTLSeconds  int               `json:"ttlSeconds,omitempty"`
	BodyBase64  string            `json:"bodyBase64,omitempty"`
	ContentType string            `json:"contentType,omitempty"`
}

func (host *Host) registerAsset(ctx context.Context, pluginID string, permissions []contract.Permission, payload []byte) (map[string]any, error) {
	var input assetRegisterRequest
	if err := strictJSON(payload, &input); err != nil {
		return nil, invalid("plugin_asset_request_invalid", err)
	}
	inline := input.BodyBase64 != ""
	if inline == (input.URL != "") {
		return nil, invalid("plugin_asset_request_invalid", nil)
	}
	var target *url.URL
	var body []byte
	var err error
	if inline {
		body, err = base64.StdEncoding.DecodeString(input.BodyBase64)
		if err != nil || len(body) == 0 || len(body) > maxAssetRequestBytes || (input.ContentType != "application/json" && input.ContentType != "text/vtt; charset=utf-8") || len(input.Headers) != 0 {
			return nil, invalid("plugin_asset_body_invalid", err)
		}
		if input.ContentType == "application/json" && !json.Valid(body) {
			return nil, invalid("plugin_asset_body_invalid", nil)
		}
	} else {
		target, err = url.Parse(input.URL)
		if err != nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" || target.Port() != "" || target.Fragment != "" || !domainAllowed(target.Hostname(), permissions) {
			return nil, denied("plugin_asset_url_denied", err)
		}
		if err := host.requirePublicHost(ctx, target.Hostname()); err != nil {
			return nil, err
		}
	}
	ttl := input.TTLSeconds
	if ttl == 0 {
		ttl = 300
	}
	if ttl < 30 || ttl > 900 || len(input.Headers) > 8 {
		return nil, invalid("plugin_asset_expiry_invalid", nil)
	}
	headers := make(http.Header, len(input.Headers))
	for name, value := range input.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !allowedRequestHeader(canonical) || len(value) > 2048 {
			return nil, denied("plugin_asset_header_denied", nil)
		}
		headers.Set(canonical, value)
	}
	now := host.now().UTC()
	reference := uuid.NewString()
	targetURL := ""
	if target != nil {
		targetURL = target.String()
	}
	asset := Asset{PluginID: pluginID, URL: targetURL, Headers: headers, ExpiresAt: now.Add(time.Duration(ttl) * time.Second), Body: append([]byte(nil), body...), ContentType: input.ContentType}
	host.assetsMu.Lock()
	for key, current := range host.assets {
		if !current.ExpiresAt.After(now) {
			delete(host.assets, key)
		}
	}
	if len(host.assets) >= 4096 {
		host.assetsMu.Unlock()
		return nil, invalid("plugin_asset_capacity_exceeded", nil)
	}
	host.assets[reference] = asset
	host.assetsMu.Unlock()
	return map[string]any{"ref": reference, "expiresAt": asset.ExpiresAt.Format(time.RFC3339Nano)}, nil
}

// ResolveAsset is a low-level in-process lookup retained for runtime tests.
// HTTP handlers must use OpenAsset so upstream URLs and headers do not escape
// the controlled transport boundary.
func (host *Host) ResolveAsset(reference string) (Asset, error) {
	if _, err := uuid.Parse(reference); err != nil {
		return Asset{}, denied("plugin_asset_reference_invalid", err)
	}
	now := host.now().UTC()
	host.assetsMu.Lock()
	defer host.assetsMu.Unlock()
	asset, ok := host.assets[reference]
	if !ok || !asset.ExpiresAt.After(now) {
		delete(host.assets, reference)
		return Asset{}, invalid("plugin_asset_expired", nil)
	}
	asset.Headers = asset.Headers.Clone()
	asset.Body = append([]byte(nil), asset.Body...)
	return asset, nil
}

// OpenAsset resolves and opens an opaque online-media asset without exposing
// the upstream URL or request headers to the handler. Remote bodies remain
// streaming and intentionally have no media-size cap; registration, caller
// authentication and Range requests are bounded at their own boundaries.
func (host *Host) OpenAsset(ctx context.Context, reference, method, rangeHeader string) (*AssetStream, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method != http.MethodGet && method != http.MethodHead {
		return nil, denied("plugin_asset_method_denied", nil)
	}
	if err := validateRangeHeader(rangeHeader); err != nil {
		return nil, invalid("plugin_asset_range_invalid", err)
	}
	asset, err := host.ResolveAsset(reference)
	if err != nil {
		return nil, err
	}
	permissions, err := host.permissions(asset.PluginID)
	if err != nil {
		return nil, err
	}
	if asset.URL == "" {
		return openInlineAsset(asset, method, rangeHeader)
	}
	target, err := url.Parse(asset.URL)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" || target.Port() != "" || target.Fragment != "" || !domainAllowed(target.Hostname(), permissions) {
		return nil, denied("plugin_asset_url_denied", err)
	}
	if err := host.requirePublicHost(ctx, target.Hostname()); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
	if err != nil {
		return nil, invalid("plugin_asset_request_invalid", err)
	}
	request.Header = asset.Headers.Clone()
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	client := host.clientForPermissions(permissions)
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		var hostError *Error
		if errors.As(err, &hostError) {
			return nil, hostError
		}
		return nil, invalid("plugin_asset_upstream_unavailable", err)
	}
	headers := safeAssetResponseHeaders(response)
	if response.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		_ = response.Body.Close()
		return &AssetStream{StatusCode: response.StatusCode, Header: headers, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		_ = response.Body.Close()
		return nil, invalid("plugin_asset_upstream_unavailable", nil)
	}
	return &AssetStream{StatusCode: response.StatusCode, Header: headers, Body: response.Body}, nil
}

func openInlineAsset(asset Asset, method, rangeHeader string) (*AssetStream, error) {
	start, end := int64(0), int64(len(asset.Body)-1)
	status := http.StatusOK
	if rangeHeader != "" && method == http.MethodGet {
		var satisfiable bool
		start, end, satisfiable = resolveByteRange(rangeHeader, int64(len(asset.Body)))
		if !satisfiable {
			headers := http.Header{"Content-Range": {fmt.Sprintf("bytes */%d", len(asset.Body))}, "Accept-Ranges": {"bytes"}, "Content-Length": {"0"}, "Content-Type": {asset.ContentType}, "Cache-Control": {"no-store"}}
			return &AssetStream{StatusCode: http.StatusRequestedRangeNotSatisfiable, Header: headers, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		status = http.StatusPartialContent
	}
	body := asset.Body
	if status == http.StatusPartialContent {
		body = body[start : end+1]
	}
	if method == http.MethodHead {
		body = nil
	}
	headers := http.Header{"Content-Type": {asset.ContentType}, "Content-Length": {strconv.FormatInt(int64(end-start+1), 10)}, "Accept-Ranges": {"bytes"}, "Cache-Control": {"no-store"}}
	if status == http.StatusPartialContent {
		headers.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(asset.Body)))
	}
	return &AssetStream{StatusCode: status, Header: headers, Body: io.NopCloser(bytes.NewReader(body))}, nil
}

func (host *Host) permissions(pluginID string) ([]contract.Permission, error) {
	var installation models.PluginInstallation
	if err := host.db.First(&installation, "plugin_id = ?", pluginID).Error; err != nil || installation.Status != models.PluginInstallationEnabled {
		return nil, denied("plugin_host_plugin_disabled", err)
	}
	var grants []models.PluginPermissionGrant
	if err := host.db.Where("plugin_id = ? AND plugin_package_id = ?", pluginID, installation.ActivePackageID).Order("id ASC").Find(&grants).Error; err != nil {
		return nil, invalid("plugin_host_permissions_unavailable", err)
	}
	permissions := make([]contract.Permission, 0, len(grants))
	for _, grant := range grants {
		var permission contract.Permission
		decoder := json.NewDecoder(strings.NewReader(grant.PermissionJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&permission); err != nil || permission.Validate() != nil {
			return nil, denied("plugin_host_permission_snapshot_invalid", err)
		}
		permissions = append(permissions, permission)
	}
	return permissions, nil
}

type httpRequest struct {
	ConnectionID       string              `json:"connectionId"`
	Method             string              `json:"method"`
	URL                string              `json:"url"`
	Headers            map[string]string   `json:"headers,omitempty"`
	Credential         string              `json:"credentialRef,omitempty"`
	BodyBase64         string              `json:"bodyBase64,omitempty"`
	TimeoutMS          int                 `json:"timeoutMs,omitempty"`
	CredentialBindings []credentialBinding `json:"credentialBindings,omitempty"`
}

type credentialBinding struct {
	Target string `json:"target"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Key    string `json:"key"`
}

type httpResponse struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	BodyBase64 string            `json:"bodyBase64"`
}

func (host *Host) http(ctx context.Context, pluginID string, permissions []contract.Permission, payload []byte) (httpResponse, error) {
	var input httpRequest
	if err := strictJSON(payload, &input); err != nil {
		return httpResponse{}, invalid("plugin_http_request_invalid", err)
	}
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method != http.MethodGet && method != http.MethodPost {
		return httpResponse{}, denied("plugin_http_method_denied", nil)
	}
	target, err := url.Parse(input.URL)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" || target.Fragment != "" || target.Port() != "" {
		return httpResponse{}, denied("plugin_http_url_denied", err)
	}
	if !domainAllowed(target.Hostname(), permissions) {
		return httpResponse{}, denied("plugin_http_domain_denied", nil)
	}
	if err := host.requirePublicHost(ctx, target.Hostname()); err != nil {
		return httpResponse{}, err
	}
	body, err := base64.StdEncoding.DecodeString(input.BodyBase64)
	if err != nil || len(body) > maxHostRequestBytes {
		return httpResponse{}, invalid("plugin_http_body_invalid", err)
	}
	requestContext := ctx
	if input.TimeoutMS != 0 {
		if input.TimeoutMS < 100 || input.TimeoutMS > 15000 {
			return httpResponse{}, invalid("plugin_http_timeout_invalid", nil)
		}
		var cancel context.CancelFunc
		requestContext, cancel = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(requestContext, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return httpResponse{}, invalid("plugin_http_request_invalid", err)
	}
	for name, value := range input.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !allowedRequestHeader(canonical) || len(value) > 2048 {
			return httpResponse{}, denied("plugin_http_header_denied", nil)
		}
		request.Header.Set(canonical, value)
	}
	if len(input.CredentialBindings) != 0 && input.Credential == "" {
		return httpResponse{}, denied("plugin_credential_binding_denied", nil)
	}
	if input.Credential != "" {
		if err := host.attachCredential(pluginID, input.ConnectionID, input.Credential, input.CredentialBindings, permissions, request); err != nil {
			return httpResponse{}, err
		}
	}
	client := host.clientForPermissions(permissions)
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		var hostError *Error
		if errors.As(err, &hostError) {
			return httpResponse{}, hostError
		}
		return httpResponse{}, invalid("plugin_http_upstream_unavailable", err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maxHTTPResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return httpResponse{}, invalid("plugin_http_upstream_unavailable", err)
	}
	if len(responseBody) > maxHTTPResponseBytes {
		return httpResponse{}, invalid("plugin_http_response_too_large", nil)
	}
	return httpResponse{Status: response.StatusCode, Headers: safeResponseHeaders(response.Header), BodyBase64: base64.StdEncoding.EncodeToString(responseBody)}, nil
}

func (host *Host) clientForPermissions(permissions []contract.Permission) http.Client {
	client := *host.client
	originalRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return denied("plugin_http_redirect_denied", nil)
		}
		if next.URL.Scheme != "https" || next.URL.User != nil || next.URL.Port() != "" || !domainAllowed(next.URL.Hostname(), permissions) {
			return denied("plugin_http_redirect_denied", nil)
		}
		if err := host.requirePublicHost(next.Context(), next.URL.Hostname()); err != nil {
			return err
		}
		if !strings.EqualFold(via[len(via)-1].URL.Hostname(), next.URL.Hostname()) {
			next.Header.Del("Cookie")
			next.Header.Del("Authorization")
		}
		if originalRedirect != nil {
			return originalRedirect(next, via)
		}
		return nil
	}
	return client
}

func (host *Host) attachCredential(pluginID, connectionID, scope string, bindings []credentialBinding, permissions []contract.Permission, request *http.Request) error {
	if !scopeAllowed(scope, permissions) {
		return denied("plugin_credential_scope_denied", nil)
	}
	var connection models.PluginConnection
	if err := host.db.First(&connection, "id = ? AND plugin_id = ? AND enabled = ?", connectionID, pluginID, true).Error; err != nil {
		return denied("plugin_connection_unavailable", err)
	}
	if connection.CredentialScope != scope || connection.CredentialCiphertext == "" {
		return denied("plugin_credential_unavailable", nil)
	}
	plaintext, err := host.credentials.Decrypt(CredentialPurpose(pluginID, connectionID, scope), connection.CredentialCiphertext)
	if err != nil {
		return denied("plugin_credential_unavailable", err)
	}
	switch connection.CredentialMode {
	case models.PluginCredentialModeCookie:
		request.Header.Set("Cookie", plaintext)
		if err := applyCookieBindings(request, plaintext, bindings); err != nil {
			return err
		}
	case models.PluginCredentialModeBearer:
		if len(bindings) != 0 {
			return denied("plugin_credential_binding_denied", nil)
		}
		request.Header.Set("Authorization", "Bearer "+plaintext)
	default:
		return denied("plugin_credential_mode_denied", nil)
	}
	return nil
}

func applyCookieBindings(request *http.Request, cookieValue string, bindings []credentialBinding) error {
	if len(bindings) == 0 {
		return nil
	}
	mediaType, _, mediaTypeErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if len(bindings) > 8 || request.Method != http.MethodPost || mediaTypeErr != nil || !strings.EqualFold(mediaType, "application/x-www-form-urlencoded") {
		return denied("plugin_credential_binding_denied", nil)
	}
	if request.Body == nil {
		return invalid("plugin_http_body_invalid", nil)
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxHostRequestBytes+1))
	if err != nil || len(body) > maxHostRequestBytes {
		return invalid("plugin_http_body_invalid", err)
	}
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return invalid("plugin_http_body_invalid", err)
	}
	cookieRequest := &http.Request{Header: http.Header{"Cookie": []string{cookieValue}}}
	for _, binding := range bindings {
		if binding.Target != "form" || binding.Source != "cookie" || !privateKeyPattern.MatchString(binding.Name) || !privateKeyPattern.MatchString(binding.Key) {
			return denied("plugin_credential_binding_denied", nil)
		}
		cookie, err := cookieRequest.Cookie(binding.Key)
		if err != nil || cookie.Value == "" || len(cookie.Value) > 4096 {
			return denied("plugin_credential_binding_unavailable", err)
		}
		values.Set(binding.Name, cookie.Value)
	}
	encoded := values.Encode()
	request.Body = io.NopCloser(strings.NewReader(encoded))
	request.ContentLength = int64(len(encoded))
	return nil
}

type storageRequest struct {
	ConnectionID string `json:"connectionId"`
	Key          string `json:"key"`
	ValueBase64  string `json:"valueBase64,omitempty"`
}

func (host *Host) storageGet(pluginID string, permissions []contract.Permission, payload []byte) (map[string]any, error) {
	if privateQuota(permissions) < 0 {
		return nil, denied("plugin_storage_permission_denied", nil)
	}
	var input storageRequest
	if err := strictJSON(payload, &input); err != nil || !privateKeyPattern.MatchString(input.Key) || !host.connectionExists(pluginID, input.ConnectionID) {
		return nil, invalid("plugin_storage_request_invalid", err)
	}
	var record models.PluginPrivateKV
	if err := host.db.First(&record, "plugin_id = ? AND connection_id = ? AND key = ?", pluginID, input.ConnectionID, input.Key).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return map[string]any{"found": false}, nil
	} else if err != nil {
		return nil, invalid("plugin_storage_unavailable", err)
	}
	value, err := host.credentials.Decrypt(PrivateValuePurpose(pluginID, input.ConnectionID, input.Key), record.ValueCiphertext)
	if err != nil {
		return nil, invalid("plugin_storage_unavailable", err)
	}
	return map[string]any{"found": true, "valueBase64": base64.StdEncoding.EncodeToString([]byte(value))}, nil
}

func (host *Host) storageSet(pluginID string, permissions []contract.Permission, payload []byte) (map[string]bool, error) {
	quota := privateQuota(permissions)
	if quota < 0 {
		return nil, denied("plugin_storage_permission_denied", nil)
	}
	var input storageRequest
	if err := strictJSON(payload, &input); err != nil || !privateKeyPattern.MatchString(input.Key) || !host.connectionExists(pluginID, input.ConnectionID) {
		return nil, invalid("plugin_storage_request_invalid", err)
	}
	value, err := base64.StdEncoding.DecodeString(input.ValueBase64)
	if err != nil || int64(len(value)) > quota {
		return nil, denied("plugin_storage_quota_exceeded", err)
	}
	var existing models.PluginPrivateKV
	existingErr := host.db.First(&existing, "plugin_id = ? AND connection_id = ? AND key = ?", pluginID, input.ConnectionID, input.Key).Error
	var used int64
	query := host.db.Model(&models.PluginPrivateKV{}).Where("plugin_id = ? AND connection_id = ?", pluginID, input.ConnectionID)
	if existingErr == nil {
		query = query.Where("id <> ?", existing.ID)
	} else if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return nil, invalid("plugin_storage_unavailable", existingErr)
	}
	if err := query.Select("COALESCE(SUM(plaintext_bytes), 0)").Scan(&used).Error; err != nil {
		return nil, invalid("plugin_storage_unavailable", err)
	}
	if used+int64(len(value)) > quota {
		return nil, denied("plugin_storage_quota_exceeded", nil)
	}
	ciphertext, err := host.credentials.Encrypt(PrivateValuePurpose(pluginID, input.ConnectionID, input.Key), string(value))
	if err != nil {
		return nil, invalid("plugin_storage_unavailable", err)
	}
	now := host.now().UTC()
	if existingErr == nil {
		err = host.db.Model(&models.PluginPrivateKV{}).Where("id = ?", existing.ID).Updates(map[string]any{"value_ciphertext": ciphertext, "plaintext_bytes": len(value), "updated_at": now}).Error
	} else {
		err = host.db.Create(&models.PluginPrivateKV{PluginID: pluginID, ConnectionID: input.ConnectionID, Key: input.Key, ValueCiphertext: ciphertext, PlaintextBytes: int64(len(value)), CreatedAt: now, UpdatedAt: now}).Error
	}
	if err != nil {
		return nil, invalid("plugin_storage_unavailable", err)
	}
	return map[string]bool{"stored": true}, nil
}

type logRequest struct {
	Level     string         `json:"level"`
	Operation string         `json:"operation"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

func (host *Host) writeLog(pluginID string, payload []byte) (map[string]bool, error) {
	var input logRequest
	if err := strictJSON(payload, &input); err != nil || !operationPattern.MatchString(input.Operation) || len(input.Message) > 256 || len(input.Fields) > 16 {
		return nil, invalid("plugin_log_request_invalid", err)
	}
	fields := make(map[string]any, len(input.Fields))
	for key, value := range input.Fields {
		if key == "plugin_id" || key == "operation" || !operationPattern.MatchString(key) || forbiddenLogKey.MatchString(key) {
			continue
		}
		switch value.(type) {
		case string, float64, bool:
			fields[key] = value
		}
	}
	event := host.log.With().Str("plugin_id", pluginID).Str("operation", input.Operation).Fields(fields).Logger()
	message := strings.TrimSpace(input.Message)
	switch input.Level {
	case "debug":
		event.Debug().Msg(message)
	case "warn":
		event.Warn().Msg(message)
	case "error":
		event.Error().Msg(message)
	default:
		event.Info().Msg(message)
	}
	return map[string]bool{"logged": true}, nil
}

type eventPollRequest struct {
	Topic string `json:"topic"`
	After uint64 `json:"after,omitempty"`
}

type eventRecord struct {
	ID      uint64          `json:"id"`
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
	At      time.Time       `json:"at"`
}

// Publish accepts only a pre-sanitized bounded payload from trusted Server
// code. It never reads plugin state and retains a small in-memory ring.
func (host *Host) Publish(topic string, payload json.RawMessage) error {
	if !operationPattern.MatchString(topic) || len(payload) == 0 || len(payload) > maxEventPayloadBytes || !json.Valid(payload) {
		return invalid("plugin_event_invalid", nil)
	}
	host.eventsMu.Lock()
	defer host.eventsMu.Unlock()
	host.eventSeq++
	record := eventRecord{ID: host.eventSeq, Topic: topic, Payload: append(json.RawMessage(nil), payload...), At: host.now().UTC()}
	for pluginID, records := range host.events {
		records = append(records, record)
		if len(records) > maxEventsPerPlugin {
			records = records[len(records)-maxEventsPerPlugin:]
		}
		host.events[pluginID] = records
	}
	return nil
}

func (host *Host) pollEvents(pluginID string, permissions []contract.Permission, payload []byte) ([]eventRecord, error) {
	var input eventPollRequest
	if err := strictJSON(payload, &input); err != nil || !topicAllowed(input.Topic, permissions) {
		return nil, denied("plugin_event_permission_denied", err)
	}
	host.eventsMu.Lock()
	defer host.eventsMu.Unlock()
	if _, exists := host.events[pluginID]; !exists {
		host.events[pluginID] = nil
	}
	result := make([]eventRecord, 0, 20)
	for _, record := range host.events[pluginID] {
		if record.Topic == input.Topic && record.ID > input.After {
			result = append(result, record)
			if len(result) == 20 {
				break
			}
		}
	}
	return result, nil
}

func (host *Host) connectionExists(pluginID, connectionID string) bool {
	if connectionID == "" {
		return false
	}
	var count int64
	return host.db.Model(&models.PluginConnection{}).Where("id = ? AND plugin_id = ? AND enabled = ?", connectionID, pluginID, true).Count(&count).Error == nil && count == 1
}

func (host *Host) requirePublicHost(ctx context.Context, hostname string) error {
	addresses, err := host.resolve(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return denied("plugin_http_dns_unavailable", err)
	}
	return requirePublicAddresses(addresses)
}

func requirePublicAddresses(addresses []net.IPAddr) error {
	for _, address := range addresses {
		ip := address.IP
		if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return denied("plugin_http_private_address_denied", nil)
		}
	}
	return nil
}

func domainAllowed(hostname string, permissions []contract.Permission) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	for _, permission := range permissions {
		if permission.Kind != contract.PermissionNetworkHTTP {
			continue
		}
		for _, domain := range permission.Domains {
			domain = strings.ToLower(strings.TrimSuffix(domain, "."))
			if strings.HasPrefix(domain, "*.") {
				base := strings.TrimPrefix(domain, "*.")
				if hostname != base && strings.HasSuffix(hostname, "."+base) {
					return true
				}
			} else if hostname == domain {
				return true
			}
		}
	}
	return false
}

func scopeAllowed(scope string, permissions []contract.Permission) bool {
	for _, permission := range permissions {
		if permission.Kind == contract.PermissionCredentialUse {
			for _, allowed := range permission.Scopes {
				if scope == allowed {
					return true
				}
			}
		}
	}
	return false
}

func topicAllowed(topic string, permissions []contract.Permission) bool {
	for _, permission := range permissions {
		if permission.Kind == contract.PermissionEventSubscribe {
			for _, allowed := range permission.Topics {
				if topic == allowed {
					return true
				}
			}
		}
	}
	return false
}

func privateQuota(permissions []contract.Permission) int64 {
	var quota int64 = -1
	for _, permission := range permissions {
		if permission.Kind == contract.PermissionPrivateStorage && permission.MaxBytes != nil && *permission.MaxBytes > quota {
			quota = *permission.MaxBytes
		}
	}
	return quota
}

func allowedRequestHeader(name string) bool {
	switch name {
	case "Accept", "Accept-Language", "Content-Type", "If-None-Match", "If-Modified-Since", "Origin", "Referer", "User-Agent":
		return true
	default:
		return false
	}
}

func safeResponseHeaders(headers http.Header) map[string]string {
	result := map[string]string{}
	for _, name := range []string{"Content-Type", "ETag", "Last-Modified", "Retry-After", "Cache-Control"} {
		if value := headers.Get(name); value != "" && len(value) <= 2048 {
			result[name] = value
		}
	}
	return result
}

func safeAssetResponseHeaders(response *http.Response) http.Header {
	result := make(http.Header)
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control"} {
		if value := response.Header.Get(name); value != "" && len(value) <= 2048 && !strings.ContainsAny(value, "\r\n") {
			result.Set(name, value)
		}
	}
	if result.Get("Content-Length") == "" && response.ContentLength >= 0 {
		result.Set("Content-Length", strconv.FormatInt(response.ContentLength, 10))
	}
	result.Set("Cache-Control", "no-store")
	return result
}

func validateRangeHeader(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 128 || strings.Contains(value, ",") || !strings.HasPrefix(value, "bytes=") {
		return errors.New("only one byte range is supported")
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return errors.New("byte range is invalid")
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		if _, err := strconv.ParseUint(part, 10, 63); err != nil {
			return errors.New("byte range is invalid")
		}
	}
	if parts[0] != "" && parts[1] != "" {
		start, _ := strconv.ParseUint(parts[0], 10, 63)
		end, _ := strconv.ParseUint(parts[1], 10, 63)
		if start > end {
			return errors.New("byte range is reversed")
		}
	}
	return nil
}

func resolveByteRange(value string, size int64) (int64, int64, bool) {
	if size <= 0 || validateRangeHeader(value) != nil {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if parts[0] == "" {
		suffix, _ := strconv.ParseInt(parts[1], 10, 64)
		if suffix <= 0 {
			return 0, 0, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true
	}
	start, _ := strconv.ParseInt(parts[0], 10, 64)
	if start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if parts[1] != "" {
		end, _ = strconv.ParseInt(parts[1], 10, 64)
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}

func strictJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request must contain exactly one JSON value")
	}
	return nil
}

func invalid(code string, cause error) error { return &Error{Code: code, Cause: cause} }
func denied(code string, cause error) error  { return &Error{Code: code, Denied: true, Cause: cause} }

func SortedPermissionKinds(permissions []contract.Permission) []string {
	result := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		result = append(result, string(permission.Kind))
	}
	sort.Strings(result)
	return result
}

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	return "plugin_host_internal"
}
