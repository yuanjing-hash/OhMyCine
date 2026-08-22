package services

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
)

const (
	proxySigningPurpose = "proxy-signing-key:v1"
	proxyScopeMediaRead = "media-read"
	proxyDefaultTTL     = 30 * 24 * time.Hour
	proxyMaximumTTL     = 31 * 24 * time.Hour
	proxyClockSkew      = 30 * time.Second
	proxyCacheMaximum   = 4096
)

type ProxyRedirect struct {
	URL       string
	ExpiresAt time.Time
}

// VerifiedProxyArtifact is the non-sensitive identity returned after a full
// signed STRM URL and its active manifest target have been verified.
type VerifiedProxyArtifact struct {
	Opaque    string
	LibraryID uint
}

type proxyCacheEntry struct {
	redirect ProxyRedirect
	expires  time.Time
}

type proxyFlight struct {
	done     chan struct{}
	redirect ProxyRedirect
	err      error
}

// SignedProxyService signs stable STRM URLs and resolves them to short-lived
// provider URLs. Persistent storage never receives the resolved upstream URL.
type SignedProxyService struct {
	db           *gorm.DB
	credentials  *credential.Store
	connections  *ConnectionService
	publicOrigin string
	now          func() time.Time
	playback     *pan115PlaybackCoordinator

	mu      sync.Mutex
	cache   map[string]proxyCacheEntry
	flights map[string]*proxyFlight
}

func NewSignedProxyService(db *gorm.DB, credentials *credential.Store, connections *ConnectionService, publicOrigin string, log zerolog.Logger) (*SignedProxyService, error) {
	publicOrigin = strings.TrimRight(strings.TrimSpace(publicOrigin), "/")
	parsed, err := url.Parse(publicOrigin)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || wildcardPublicOrigin(parsed) {
		return nil, errors.New("signed proxy public origin is invalid")
	}
	if db == nil || credentials == nil || connections == nil {
		return nil, errors.New("signed proxy dependencies are unavailable")
	}
	now := func() time.Time { return time.Now().UTC() }
	return &SignedProxyService{db: db, credentials: credentials, connections: connections, publicOrigin: publicOrigin, now: now, playback: newPan115PlaybackCoordinator(db, connections, log, now), cache: map[string]proxyCacheEntry{}, flights: map[string]*proxyFlight{}}, nil
}

func (s *SignedProxyService) Start(parent context.Context) error {
	if s.playback == nil {
		return errors.New("115 playback coordinator is unavailable")
	}
	return s.playback.Start(parent)
}

func (s *SignedProxyService) Close() {
	if s.playback != nil {
		s.playback.Close()
	}
}

func wildcardPublicOrigin(parsed *url.URL) bool {
	ip := net.ParseIP(strings.TrimSuffix(parsed.Hostname(), "."))
	return ip != nil && ip.IsUnspecified()
}

// EnsureActiveKey lazily creates the first signing key after migrations have
// committed. It is safe under concurrent startup/first-artifact generation.
func (s *SignedProxyService) EnsureActiveKey() (models.ProxySigningKey, error) {
	var key models.ProxySigningKey
	if err := s.db.Where("status = ?", models.ProxySigningKeyStatusActive).First(&key).Error; err == nil {
		return key, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.ProxySigningKey{}, err
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return models.ProxySigningKey{}, err
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return models.ProxySigningKey{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(idBytes)
	encrypted, err := s.credentials.Encrypt(proxySigningPurpose+":"+id, base64.RawStdEncoding.EncodeToString(secret))
	if err != nil {
		return models.ProxySigningKey{}, err
	}
	created := models.ProxySigningKey{ID: id, SecretCiphertext: encrypted, Status: models.ProxySigningKeyStatusActive, CreatedAt: s.now()}
	if err := s.db.Create(&created).Error; err != nil {
		if retryErr := s.db.Where("status = ?", models.ProxySigningKeyStatusActive).First(&key).Error; retryErr == nil {
			return key, nil
		}
		return models.ProxySigningKey{}, err
	}
	return created, nil
}

func (s *SignedProxyService) SignArtifact(opaque string, libraryID uint, ttl time.Duration) (string, error) {
	opaque = strings.TrimSpace(opaque)
	if !validOpaqueID(opaque) || libraryID == 0 {
		return "", appError(CodeInvalidRequest, "STRM 产物身份无效", nil)
	}
	if ttl <= 0 {
		ttl = proxyDefaultTTL
	}
	if ttl > proxyMaximumTTL {
		return "", appError(CodeInvalidRequest, "STRM 签名有效期超出限制", nil)
	}
	key, err := s.EnsureActiveKey()
	if err != nil {
		return "", appError(CodeProxyUnavailable, "302 签名服务不可用", err)
	}
	secret, err := s.decryptKey(key)
	if err != nil {
		return "", appError(CodeProxyUnavailable, "302 签名服务不可用", err)
	}
	expiry := s.now().Add(ttl).Unix()
	signature := signProxy(secret, opaque, libraryID, key.ID, expiry)
	return s.publicOrigin + "/proxy/strm/" + url.PathEscape(opaque) + "?kid=" + url.QueryEscape(key.ID) + "&exp=" + strconv.FormatInt(expiry, 10) + "&sig=" + url.QueryEscape(signature), nil
}

func (s *SignedProxyService) Resolve(ctx context.Context, opaque, keyID string, expiry int64, signature, userAgent string) (ProxyRedirect, error) {
	target, err := s.verify(opaque, keyID, expiry, signature)
	if err != nil {
		return ProxyRedirect{}, err
	}
	return s.resolveTarget(ctx, strings.TrimSpace(opaque), target, userAgent, playbackClientFingerprint("internal", "compat"))
}

func (s *SignedProxyService) ResolveForClient(ctx context.Context, opaque, keyID string, expiry int64, signature, userAgent, remoteAddr string) (ProxyRedirect, error) {
	target, err := s.verify(opaque, keyID, expiry, signature)
	if err != nil {
		return ProxyRedirect{}, err
	}
	return s.resolveTarget(ctx, strings.TrimSpace(opaque), target, userAgent, playbackClientFingerprint(remoteAddr, userAgent))
}

// VerifyArtifactURL accepts only URLs issued by this service's configured
// public origin. It validates signature, expiry and current artifact state
// without resolving or exposing the provider URL.
func (s *SignedProxyService) VerifyArtifactURL(raw string) (VerifiedProxyArtifact, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Scheme == "" || parsed.Host == "" {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	if origin != s.publicOrigin {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	const prefix = "/proxy/strm/"
	if !strings.HasPrefix(parsed.EscapedPath(), prefix) || strings.Contains(strings.TrimPrefix(parsed.EscapedPath(), prefix), "/") {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	opaque, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), prefix))
	if err != nil {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	query := parsed.Query()
	if len(query) != 3 || len(query["kid"]) != 1 || len(query["exp"]) != 1 || len(query["sig"]) != 1 {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	expiry, err := strconv.ParseInt(query.Get("exp"), 10, 64)
	if err != nil {
		return VerifiedProxyArtifact{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	target, err := s.verify(opaque, query.Get("kid"), expiry, query.Get("sig"))
	if err != nil {
		return VerifiedProxyArtifact{}, err
	}
	return VerifiedProxyArtifact{Opaque: opaque, LibraryID: target.LibraryID}, nil
}

// ResolveArtifact is intentionally signature-free only at this internal
// boundary. Callers must first authorize the request independently (the Emby
// gateway does so with a bound, short-lived playback ticket).
func (s *SignedProxyService) ResolveArtifact(ctx context.Context, opaque, userAgent string) (ProxyRedirect, error) {
	opaque = strings.TrimSpace(opaque)
	if !validOpaqueID(opaque) {
		return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	target, err := s.proxyTarget(opaque)
	if err != nil {
		return ProxyRedirect{}, err
	}
	return s.resolveTarget(ctx, opaque, target, userAgent, playbackClientFingerprint("internal", "compat"))
}

func (s *SignedProxyService) ResolveArtifactForClient(ctx context.Context, opaque, userAgent, remoteAddr string) (ProxyRedirect, error) {
	opaque = strings.TrimSpace(opaque)
	if !validOpaqueID(opaque) {
		return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	target, err := s.proxyTarget(opaque)
	if err != nil {
		return ProxyRedirect{}, err
	}
	return s.resolveTarget(ctx, opaque, target, userAgent, playbackClientFingerprint(remoteAddr, userAgent))
}

func (s *SignedProxyService) verify(opaque, keyID string, expiry int64, signature string) (signedProxyTarget, error) {
	opaque, keyID, signature = strings.TrimSpace(opaque), strings.TrimSpace(keyID), strings.TrimSpace(signature)
	if !validOpaqueID(opaque) || !validKeyID(keyID) || signature == "" {
		return signedProxyTarget{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	now := s.now()
	if expiry < now.Add(-proxyClockSkew).Unix() || expiry > now.Add(proxyMaximumTTL).Unix() {
		return signedProxyTarget{}, appError(CodeProxySignatureExpired, "播放链接已过期", nil)
	}
	target, err := s.proxyTarget(opaque)
	if err != nil {
		return signedProxyTarget{}, err
	}
	var key models.ProxySigningKey
	if err := s.db.First(&key, "id = ?", keyID).Error; err != nil || (key.Status != models.ProxySigningKeyStatusActive && key.Status != models.ProxySigningKeyStatusPrevious) {
		return signedProxyTarget{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	secret, err := s.decryptKey(key)
	if err != nil {
		return signedProxyTarget{}, appError(CodeProxyUnavailable, "302 签名服务不可用", err)
	}
	want, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(want) != sha256.Size {
		return signedProxyTarget{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	got := hmacSHA256(secret, proxyCanonical(opaque, target.LibraryID, keyID, expiry))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return signedProxyTarget{}, appError(CodeProxySignatureInvalid, "播放链接无效", nil)
	}
	return target, nil
}

func (s *SignedProxyService) resolveTarget(ctx context.Context, opaque string, target signedProxyTarget, userAgent, clientFingerprint string) (ProxyRedirect, error) {
	now := s.now()
	cacheKey := proxyCacheKey(target.ConnectionID, opaque, userAgent+":"+clientFingerprint)
	if cached, ok := s.cached(cacheKey, now); ok {
		return cached, nil
	}
	return s.singleflight(ctx, cacheKey, func() (ProxyRedirect, error) {
		if cached, ok := s.cached(cacheKey, s.now()); ok {
			return cached, nil
		}
		redirect, err := s.resolveProvider(ctx, opaque, target, userAgent, clientFingerprint)
		if err != nil {
			return ProxyRedirect{}, err
		}
		s.storeCache(cacheKey, redirect)
		return redirect, nil
	})
}

type signedProxyTarget struct {
	LibraryID      uint
	ConnectionID   uint
	ProviderItemID string
	StorageType    string
	ArtifactStatus string
	ArtifactKind   string
	TargetKind     string
	Managed        bool
	ArtifactActive bool
	LibraryEnabled bool
	StorageEnabled bool
	STRMEnabled    bool
	ProxyEnabled   bool
}

func (s *SignedProxyService) proxyTarget(opaque string) (signedProxyTarget, error) {
	var target signedProxyTarget
	err := s.db.Table("media_artifacts").
		Select(`media_artifacts.library_id, COALESCE(storages.connection_id, 0) AS connection_id,
			media_artifacts.provider_item_id, storages.type AS storage_type,
			media_artifacts.status AS artifact_status, media_artifacts.kind AS artifact_kind,
			media_artifacts.target_kind, media_artifacts.managed, media_artifacts.active AS artifact_active,
			media_libraries.enabled AS library_enabled, storages.enabled AS storage_enabled,
			media_libraries.strm_enabled,
			media_libraries.signed_proxy_enabled AS proxy_enabled`).
		Joins("JOIN media_libraries ON media_libraries.id = media_artifacts.library_id").
		Joins("JOIN storages ON storages.id = media_libraries.storage_id").
		Where("media_artifacts.opaque_id = ?", opaque).Take(&target).Error
	if err != nil || !target.Managed || !target.ArtifactActive || !target.LibraryEnabled || !target.StorageEnabled || !target.STRMEnabled || !target.ProxyEnabled || target.ArtifactStatus != models.MediaArtifactStatusCompleted || target.ArtifactKind != models.MediaArtifactKindSTRM || target.TargetKind != models.MediaArtifactTargetLocalProjection || target.StorageType != models.StorageTypePan115 || target.ConnectionID == 0 || strings.TrimSpace(target.ProviderItemID) == "" {
		return signedProxyTarget{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", nil)
	}
	return target, nil
}

func (s *SignedProxyService) resolveProvider(ctx context.Context, opaque string, target signedProxyTarget, userAgent, clientFingerprint string) (ProxyRedirect, error) {
	_, driver, err := s.connections.driver(target.ConnectionID)
	if err != nil || !driver.Capabilities().TemporaryDirectURL {
		return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	var temporary cloudpkg.TemporaryURL
	if target.StorageType == models.StorageTypePan115 && s.playback != nil {
		temporary, err = s.playback.Resolve(ctx, opaque, target, userAgent, clientFingerprint, driver)
	} else {
		item, statErr := driver.Stat(ctx, target.ProviderItemID)
		if statErr != nil || item.IsDir || strings.TrimSpace(item.PickCode) == "" {
			return ProxyRedirect{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", statErr)
		}
		temporary, err = driver.DirectURL(ctx, cloudpkg.DirectURLRequest{FileID: item.ID, PickCode: item.PickCode, UserAgent: userAgent})
	}
	if err != nil {
		if ErrorCode(err) == CodeProxyDeviceLimit {
			return ProxyRedirect{}, err
		}
		return ProxyRedirect{}, appError(CodeProxyUpstreamUnavailable, "暂时无法获取播放地址", err)
	}
	parsed, err := url.Parse(strings.TrimSpace(temporary.URL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return ProxyRedirect{}, appError(CodeProxyUpstreamUnavailable, "暂时无法获取播放地址", nil)
	}
	if !proxyHeadersCompatible(temporary.Headers, userAgent) {
		return ProxyRedirect{}, appError(CodeProxyHeadersUnsupported, "该播放地址需要 302 无法安全携带的请求头", nil)
	}
	expires := temporary.ExpiresAt.UTC()
	if expires.IsZero() {
		expires = s.now().Add(2 * time.Minute)
	}
	return ProxyRedirect{URL: parsed.String(), ExpiresAt: expires}, nil
}

func proxyHeadersCompatible(headers http.Header, userAgent string) bool {
	for name, values := range headers {
		if !strings.EqualFold(name, "User-Agent") || len(values) != 1 || strings.TrimSpace(values[0]) != strings.TrimSpace(userAgent) {
			return false
		}
	}
	return true
}

func (s *SignedProxyService) decryptKey(key models.ProxySigningKey) ([]byte, error) {
	encoded, err := s.credentials.Decrypt(proxySigningPurpose+":"+key.ID, key.SecretCiphertext)
	if err != nil {
		return nil, err
	}
	secret, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(secret) != 32 {
		return nil, errors.New("proxy signing key is invalid")
	}
	return secret, nil
}

func signProxy(secret []byte, opaque string, libraryID uint, keyID string, expiry int64) string {
	return base64.RawURLEncoding.EncodeToString(hmacSHA256(secret, proxyCanonical(opaque, libraryID, keyID, expiry)))
}

func proxyCanonical(opaque string, libraryID uint, keyID string, expiry int64) []byte {
	return []byte("v1\n" + proxyScopeMediaRead + "\n" + opaque + "\n" + strconv.FormatUint(uint64(libraryID), 10) + "\n" + keyID + "\n" + strconv.FormatInt(expiry, 10))
}

func hmacSHA256(secret, message []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

func proxyCacheKey(connectionID uint, opaque, userAgent string) string {
	ua := sha256.Sum256([]byte(userAgent))
	return strconv.FormatUint(uint64(connectionID), 10) + ":" + opaque + ":" + base64.RawURLEncoding.EncodeToString(ua[:])
}

func (s *SignedProxyService) cached(key string, now time.Time) (ProxyRedirect, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.cache[key]
	if !ok || !now.Before(item.expires) {
		delete(s.cache, key)
		return ProxyRedirect{}, false
	}
	return item.redirect, true
}

func (s *SignedProxyService) storeCache(key string, redirect ProxyRedirect) {
	now := s.now()
	expires := redirect.ExpiresAt.Add(-30 * time.Second)
	maximum := now.Add(5 * time.Minute)
	if expires.After(maximum) {
		expires = maximum
	}
	if !expires.After(now) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) >= proxyCacheMaximum {
		for candidate, item := range s.cache {
			if !now.Before(item.expires) {
				delete(s.cache, candidate)
			}
		}
		if len(s.cache) >= proxyCacheMaximum {
			for candidate := range s.cache {
				delete(s.cache, candidate)
				break
			}
		}
	}
	s.cache[key] = proxyCacheEntry{redirect: redirect, expires: expires}
}

func (s *SignedProxyService) singleflight(ctx context.Context, key string, call func() (ProxyRedirect, error)) (ProxyRedirect, error) {
	s.mu.Lock()
	if flight := s.flights[key]; flight != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ProxyRedirect{}, appError(CodeProxyUpstreamUnavailable, "播放请求已取消", ctx.Err())
		case <-flight.done:
			return flight.redirect, flight.err
		}
	}
	flight := &proxyFlight{done: make(chan struct{})}
	s.flights[key] = flight
	s.mu.Unlock()
	flight.redirect, flight.err = call()
	s.mu.Lock()
	delete(s.flights, key)
	close(flight.done)
	s.mu.Unlock()
	return flight.redirect, flight.err
}

func validOpaqueID(value string) bool {
	if len(value) < 16 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func validKeyID(value string) bool { return validOpaqueID(value) && len(value) <= 32 }

func ProxyHTTPStatus(err error) int {
	switch ErrorCode(err) {
	case CodeProxySignatureInvalid, CodeProxySignatureExpired:
		return http.StatusForbidden
	case CodeProxyTargetUnavailable:
		return http.StatusNotFound
	case CodeProxyDeviceLimit:
		return http.StatusTooManyRequests
	default:
		return http.StatusBadGateway
	}
}

func NewArtifactOpaqueID() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate artifact identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
