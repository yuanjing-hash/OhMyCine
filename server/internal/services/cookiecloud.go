package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" // CookieCloud protocol compatibility; not used for credential storage.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	sitepkg "github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"gorm.io/gorm"
)

const (
	cookieCloudCredentialPurpose = "site-cookiecloud-settings:v1"
	cookieCloudMaxPayload        = 16 << 20
)

type CookieCloudService struct {
	db          *gorm.DB
	audit       *AuditService
	credentials *credential.Store
	sites       *SiteService
	log         zerolog.Logger
	http        *http.Client
	now         func() time.Time
	mu          sync.Mutex
	cancel      context.CancelFunc
}

type cookieCloudCredential struct {
	UUID       string `json:"uuid"`
	Password   string `json:"password"`
	AuthHeader string `json:"auth_header,omitempty"`
}

type CookieCloudSettingsInput struct {
	Mode, BaseURL, UUID, Password, AuthHeader string
	AutoSyncMinutes                           int
	Revision                                  uint64
}

type CookieCloudSettingsSummary struct {
	Mode                 string     `json:"mode"`
	BaseURL              string     `json:"base_url"`
	AutoSyncMinutes      int        `json:"auto_sync_minutes"`
	CredentialConfigured bool       `json:"credential_configured"`
	LocalUploadPath      string     `json:"local_upload_path,omitempty"`
	LastSyncStatus       string     `json:"last_sync_status"`
	LastSyncErrorCode    string     `json:"last_sync_error_code"`
	LastSyncAt           *time.Time `json:"last_sync_at,omitempty"`
	Revision             uint64     `json:"revision"`
}

type CookieCloudSyncSummary struct {
	Status  string                 `json:"status"`
	Created int                    `json:"created"`
	Updated int                    `json:"updated"`
	Skipped int                    `json:"skipped"`
	Failed  int                    `json:"failed"`
	Issues  []CookieCloudSyncIssue `json:"issues,omitempty"`
}

type CookieCloudSyncIssue struct {
	Action    string `json:"action"`
	SiteID    uint   `json:"site_id,omitempty"`
	Kind      string `json:"kind"`
	ErrorCode string `json:"error_code"`
}

type cookieCloudEntry struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Value  string `json:"value"`
}

type cookieCloudEncryptedPayload struct {
	Encrypted  string
	CryptoType string
}

func NewCookieCloudService(db *gorm.DB, audit *AuditService, credentials *credential.Store, sites *SiteService, log zerolog.Logger) *CookieCloudService {
	return &CookieCloudService{db: db, audit: audit, credentials: credentials, sites: sites, log: log, http: cookieCloudHTTPClient(), now: func() time.Time { return time.Now().UTC() }}
}

func (s *CookieCloudService) Get(actor Actor) (CookieCloudSettingsSummary, error) {
	if !actor.IsSystemAdmin() {
		return CookieCloudSettingsSummary{}, appError(CodePermissionDenied, "仅管理员可以管理 CookieCloud", nil)
	}
	record, err := s.record()
	if err != nil {
		return CookieCloudSettingsSummary{}, err
	}
	return cookieCloudSummary(record), nil
}

func (s *CookieCloudService) Update(ctx context.Context, actor Actor, input CookieCloudSettingsInput, request RequestContext) (CookieCloudSettingsSummary, error) {
	if !actor.IsSystemAdmin() {
		return CookieCloudSettingsSummary{}, appError(CodePermissionDenied, "仅管理员可以管理 CookieCloud", nil)
	}
	record, err := s.record()
	if err != nil {
		return CookieCloudSettingsSummary{}, err
	}
	if input.Revision == 0 || input.Revision != record.Revision {
		return CookieCloudSettingsSummary{}, appError(CodeConflict, "CookieCloud 设置已变化，请刷新", nil)
	}
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode != "disabled" && mode != "remote" && mode != "local" {
		return CookieCloudSettingsSummary{}, appError(CodeCookieCloudInvalid, "CookieCloud 模式无效", nil)
	}
	baseURL := strings.TrimSpace(input.BaseURL)
	if mode == "remote" {
		baseURL, err = normalizeCookieCloudBase(baseURL)
		if err != nil {
			return CookieCloudSettingsSummary{}, err
		}
	} else {
		baseURL = ""
	}
	if input.AutoSyncMinutes != 0 && (input.AutoSyncMinutes < 15 || input.AutoSyncMinutes > 43200) {
		return CookieCloudSettingsSummary{}, appError(CodeCookieCloudInvalid, "CookieCloud 自动同步间隔无效", nil)
	}
	current, _ := s.decryptCredential(record)
	if strings.TrimSpace(input.UUID) != "" {
		current.UUID = strings.TrimSpace(input.UUID)
	}
	if input.Password != "" {
		current.Password = input.Password
	}
	if input.AuthHeader != "" {
		current.AuthHeader = input.AuthHeader
	}
	if mode != "disabled" {
		if err := validateCookieCloudCredential(current, mode); err != nil {
			return CookieCloudSettingsSummary{}, err
		}
		if mode == "remote" {
			payload, fetchErr := s.fetchRemote(ctx, baseURL, current.UUID)
			if fetchErr != nil {
				return CookieCloudSettingsSummary{}, fetchErr
			}
			if _, decryptErr := decryptCookieCloudPayload(payload, current.UUID, current.Password); decryptErr != nil {
				return CookieCloudSettingsSummary{}, decryptErr
			}
		}
	}
	ciphertext := record.CredentialCiphertext
	if ciphertext != "" || current.UUID != "" || current.Password != "" || current.AuthHeader != "" {
		encoded, marshalErr := json.Marshal(current)
		if marshalErr != nil {
			return CookieCloudSettingsSummary{}, marshalErr
		}
		ciphertext, err = s.credentials.Encrypt(cookieCloudCredentialPurpose, string(encoded))
		if err != nil {
			return CookieCloudSettingsSummary{}, err
		}
	}
	now := s.now()
	result := s.db.Model(&models.CookieCloudSettings{}).Where("id = ? AND revision = ?", 1, record.Revision).Updates(map[string]any{"mode": mode, "base_url": baseURL, "credential_ciphertext": ciphertext, "auto_sync_minutes": input.AutoSyncMinutes, "revision": gorm.Expr("revision + 1"), "updated_at": now})
	if result.Error != nil {
		return CookieCloudSettingsSummary{}, result.Error
	}
	if result.RowsAffected != 1 {
		return CookieCloudSettingsSummary{}, appError(CodeConflict, "CookieCloud 设置已变化，请刷新", nil)
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "cookiecloud.settings.update", "cookiecloud", "1", "success", map[string]any{"mode": mode, "auto_sync_minutes": input.AutoSyncMinutes}, request)
	updated, err := s.record()
	if err != nil {
		return CookieCloudSettingsSummary{}, err
	}
	return cookieCloudSummary(updated), nil
}

func (s *CookieCloudService) Sync(ctx context.Context, actor Actor, request RequestContext) (CookieCloudSyncSummary, error) {
	if !actor.IsSystemAdmin() {
		return CookieCloudSyncSummary{}, appError(CodePermissionDenied, "仅管理员可以同步 CookieCloud", nil)
	}
	result, err := s.sync(ctx)
	outcome := result.Status
	if outcome == "" {
		outcome = "success"
	}
	if err != nil {
		outcome = "failed"
	}
	_ = s.audit.Record(s.db, &actor.User.ID, "cookiecloud.sync", "cookiecloud", "1", outcome, map[string]any{"created": result.Created, "updated": result.Updated, "skipped": result.Skipped, "failed": result.Failed, "error_code": cookieCloudSyncErrorCode(result, err)}, request)
	return result, err
}

func (s *CookieCloudService) Receive(uuid, encrypted, cryptoType, authHeader string) error {
	if len(encrypted) == 0 || len(encrypted) > cookieCloudMaxPayload {
		return appError(CodeCookieCloudInvalid, "CookieCloud 上传内容无效", nil)
	}
	cryptoType, err := normalizeCookieCloudCryptoType(cryptoType)
	if err != nil {
		return err
	}
	record, err := s.record()
	if err != nil {
		return err
	}
	if record.Mode != "local" {
		return appError(CodeCookieCloudDisabled, "本地 CookieCloud 接收端未启用", nil)
	}
	secret, err := s.decryptCredential(record)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(authHeader)), []byte(secret.AuthHeader)) != 1 || strings.TrimSpace(authHeader) == "" {
		return appError(CodeCookieCloudAuthentication, "CookieCloud 上传认证失败", nil)
	}
	if subtle.ConstantTimeCompare([]byte(hashCookieCloudUUID(strings.TrimSpace(uuid))), []byte(hashCookieCloudUUID(secret.UUID))) != 1 {
		return appError(CodeCookieCloudAuthentication, "CookieCloud 上传认证失败", nil)
	}
	now := s.now()
	return s.db.Model(&models.CookieCloudPayload{}).Where("id = ?", 1).Updates(map[string]any{"uuid_hash": hashCookieCloudUUID(secret.UUID), "encrypted_payload": encrypted, "crypto_type": cryptoType, "updated_at": now}).Error
}

func (s *CookieCloudService) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return errors.New("cookiecloud service already started")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	go s.loop(ctx)
	return nil
}

func (s *CookieCloudService) Close() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *CookieCloudService) loop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.syncIfDue(ctx)
		}
	}
}

func (s *CookieCloudService) syncIfDue(ctx context.Context) {
	record, err := s.record()
	if err != nil || record.Mode == "disabled" || record.AutoSyncMinutes == 0 {
		return
	}
	if record.LastSyncAt != nil && record.LastSyncAt.Add(time.Duration(record.AutoSyncMinutes)*time.Minute).After(s.now()) {
		return
	}
	_, _ = s.sync(ctx)
}

func (s *CookieCloudService) sync(ctx context.Context) (CookieCloudSyncSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.record()
	if err != nil {
		return CookieCloudSyncSummary{}, err
	}
	if record.Mode == "disabled" {
		return CookieCloudSyncSummary{}, appError(CodeCookieCloudDisabled, "CookieCloud 尚未启用", nil)
	}
	secret, err := s.decryptCredential(record)
	if err != nil {
		return CookieCloudSyncSummary{}, err
	}
	var encrypted cookieCloudEncryptedPayload
	if record.Mode == "remote" {
		encrypted, err = s.fetchRemote(ctx, record.BaseURL, secret.UUID)
	} else {
		encrypted, err = s.localPayload(secret.UUID)
	}
	if err != nil {
		s.recordSync(record, "failed", ErrorCode(err))
		return CookieCloudSyncSummary{}, err
	}
	entries, err := decryptCookieCloudPayload(encrypted, secret.UUID, secret.Password)
	if err != nil {
		s.recordSync(record, "failed", ErrorCode(err))
		return CookieCloudSyncSummary{}, err
	}
	cookies := cookiesByDomain(entries)
	var sites []models.Site
	if err := s.db.Order("priority ASC,id ASC").Find(&sites).Error; err != nil {
		return CookieCloudSyncSummary{}, err
	}
	result := CookieCloudSyncSummary{Status: "success"}
	configuredKinds := make(map[string]struct{}, len(sites))
	for _, siteRecord := range sites {
		configuredKinds[siteRecord.Kind] = struct{}{}
		host, parseErr := url.Parse(siteRecord.BaseURL)
		if parseErr != nil {
			result.Skipped++
			continue
		}
		cookie := cookieForHost(cookies, host.Hostname())
		if cookie == "" {
			result.Skipped++
			continue
		}
		oldCredential, decryptErr := s.sites.decryptCredential(siteRecord)
		if decryptErr != nil {
			result.addIssue("update", siteRecord.ID, siteRecord.Kind, CodeSiteCredentialInvalid)
			continue
		}
		adapter := s.sites.adapters[siteRecord.Kind]
		if adapter == nil {
			result.Skipped++
			continue
		}
		candidate := sitepkgConfig(siteRecord, oldCredential, cookie)
		health, probeErr := adapter.Test(ctx, candidate)
		if probeErr != nil {
			result.addIssue("update", siteRecord.ID, siteRecord.Kind, siteErrorCode(probeErr))
			continue
		}
		oldCredential.Cookie = cookie
		ciphertext, encryptErr := s.sites.encryptCredential(siteRecord.ID, siteRecord.Kind, oldCredential)
		if encryptErr != nil {
			result.addIssue("update", siteRecord.ID, siteRecord.Kind, CodeSiteCredentialInvalid)
			continue
		}
		now := s.now()
		update := s.db.Model(&models.Site{}).Where("id = ? AND revision = ?", siteRecord.ID, siteRecord.Revision).Updates(map[string]any{"credential_ciphertext": ciphertext, "last_health_status": "online", "last_health_error_code": "", "last_health_username": safeLabel(health.Username, 128), "last_health_checked_at": now, "revision": gorm.Expr("revision + 1"), "updated_at": now})
		if update.Error != nil || update.RowsAffected != 1 {
			result.addIssue("update", siteRecord.ID, siteRecord.Kind, CodeConflict)
			continue
		}
		s.sites.invalidateLimiter(siteRecord.ID)
		s.sites.deleteClaimsForSite(siteRecord.ID)
		result.Updated++
	}
	for _, candidate := range []struct {
		kind, baseURL, cookieHost string
	}{
		{kind: "pttime", baseURL: "https://www.pttime.org", cookieHost: "www.pttime.org"},
		{kind: "pttime", baseURL: "https://www.pttime.me", cookieHost: "www.pttime.me"},
	} {
		if _, exists := configuredKinds[candidate.kind]; exists {
			continue
		}
		cookie := cookieForHost(cookies, candidate.cookieHost)
		if cookie == "" {
			continue
		}
		if _, createErr := s.sites.createFromCookieCloud(ctx, candidate.kind, candidate.baseURL, cookie); createErr != nil {
			result.addIssue("create", 0, candidate.kind, ErrorCode(createErr))
			continue
		}
		configuredKinds[candidate.kind] = struct{}{}
		result.Created++
	}
	if result.Failed > 0 {
		result.Status = "partial"
	}
	errorCode := cookieCloudSyncErrorCode(result, nil)
	s.recordSync(record, result.Status, errorCode)
	event := serverlog.OperationCookieCloud.Event(s.log.Info()).Int("created", result.Created).Int("updated", result.Updated).Int("skipped", result.Skipped).Int("failed", result.Failed)
	if errorCode != "" {
		event = event.Str("error_code", errorCode)
	}
	event.Msg(serverlog.OperationCookieCloud.Message("站点凭据同步完成"))
	return result, nil
}

func (s *CookieCloudSyncSummary) addIssue(action string, siteID uint, kind, code string) {
	if code == "" || code == "INTERNAL_ERROR" {
		code = CodeSiteUnavailable
	}
	s.Failed++
	s.Issues = append(s.Issues, CookieCloudSyncIssue{Action: action, SiteID: siteID, Kind: kind, ErrorCode: code})
}

func cookieCloudSyncErrorCode(result CookieCloudSyncSummary, err error) string {
	if err != nil {
		return ErrorCode(err)
	}
	if len(result.Issues) > 0 {
		return result.Issues[0].ErrorCode
	}
	return ""
}

func sitepkgConfig(record models.Site, old siteCredentialEnvelope, cookie string) sitepkg.Config {
	return sitepkg.Config{BaseURL: record.BaseURL, Cookie: cookie, Passkey: old.Passkey, UserAgent: record.UserAgent, Timeout: time.Duration(record.TimeoutSeconds) * time.Second, BrowserEmulation: record.BrowserEmulation, BrowserServiceURL: record.BrowserServiceURL}
}

func (s *CookieCloudService) fetchRemote(ctx context.Context, baseURL, uuid string) (cookieCloudEncryptedPayload, error) {
	endpoint, err := url.Parse(strings.TrimRight(baseURL, "/") + "/get/" + url.PathEscape(uuid))
	if err != nil {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudInvalid, "CookieCloud 地址无效", nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudInvalid, "CookieCloud 地址无效", nil)
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.http.Do(request)
	if err != nil {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudUnavailable, "CookieCloud 服务不可用", nil)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudUnavailable, "CookieCloud 服务返回错误", nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, cookieCloudMaxPayload+1))
	if err != nil || len(body) > cookieCloudMaxPayload {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudResponseInvalid, "CookieCloud 响应无效", nil)
	}
	var payload struct {
		Encrypted  string `json:"encrypted"`
		CryptoType string `json:"crypto_type"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Encrypted == "" || len(payload.Encrypted) > cookieCloudMaxPayload {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudResponseInvalid, "CookieCloud 响应无效", nil)
	}
	cryptoType, err := normalizeCookieCloudCryptoType(payload.CryptoType)
	if err != nil {
		return cookieCloudEncryptedPayload{}, err
	}
	return cookieCloudEncryptedPayload{Encrypted: payload.Encrypted, CryptoType: cryptoType}, nil
}

func (s *CookieCloudService) localPayload(uuid string) (cookieCloudEncryptedPayload, error) {
	var payload models.CookieCloudPayload
	if err := s.db.First(&payload, 1).Error; err != nil {
		return cookieCloudEncryptedPayload{}, err
	}
	if payload.UUIDHash != hashCookieCloudUUID(uuid) || payload.EncryptedPayload == "" {
		return cookieCloudEncryptedPayload{}, appError(CodeCookieCloudUnavailable, "尚未收到浏览器扩展上传的 CookieCloud 数据", nil)
	}
	cryptoType, err := normalizeCookieCloudCryptoType(payload.CryptoType)
	if err != nil {
		return cookieCloudEncryptedPayload{}, err
	}
	return cookieCloudEncryptedPayload{Encrypted: payload.EncryptedPayload, CryptoType: cryptoType}, nil
}

func (s *CookieCloudService) record() (models.CookieCloudSettings, error) {
	var record models.CookieCloudSettings
	return record, s.db.First(&record, 1).Error
}
func (s *CookieCloudService) decryptCredential(record models.CookieCloudSettings) (cookieCloudCredential, error) {
	raw, err := s.credentials.Decrypt(cookieCloudCredentialPurpose, record.CredentialCiphertext)
	if err != nil {
		return cookieCloudCredential{}, err
	}
	var value cookieCloudCredential
	if raw != "" {
		err = json.Unmarshal([]byte(raw), &value)
	}
	return value, err
}
func (s *CookieCloudService) recordSync(record models.CookieCloudSettings, status, code string) {
	now := s.now()
	_ = s.db.Model(&models.CookieCloudSettings{}).Where("id = ?", 1).Updates(map[string]any{"last_sync_status": status, "last_sync_error_code": code, "last_sync_at": now, "updated_at": now}).Error
}

func cookieCloudSummary(record models.CookieCloudSettings) CookieCloudSettingsSummary {
	return CookieCloudSettingsSummary{Mode: record.Mode, BaseURL: record.BaseURL, AutoSyncMinutes: record.AutoSyncMinutes, CredentialConfigured: record.CredentialCiphertext != "", LocalUploadPath: func() string {
		if record.Mode == "local" {
			return "/cookiecloud"
		}
		return ""
	}(), LastSyncStatus: record.LastSyncStatus, LastSyncErrorCode: record.LastSyncErrorCode, LastSyncAt: record.LastSyncAt, Revision: record.Revision}
}

func validateCookieCloudCredential(value cookieCloudCredential, mode string) error {
	if len(value.UUID) < 5 || len(value.UUID) > 128 || strings.ContainsAny(value.UUID, "\x00\r\n/\\") || value.Password == "" || len(value.Password) > 1024 {
		return appError(CodeCookieCloudInvalid, "CookieCloud 用户 KEY 或端到端密码无效", nil)
	}
	if mode == "local" && (len(value.AuthHeader) < 12 || len(value.AuthHeader) > 512 || strings.ContainsAny(value.AuthHeader, "\x00\r\n")) {
		return appError(CodeCookieCloudInvalid, "本地 CookieCloud 共享认证至少需要 12 个字符", nil)
	}
	return nil
}

func normalizeCookieCloudBase(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", appError(CodeCookieCloudInvalid, "CookieCloud 服务地址无效", nil)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func cookieCloudHTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 || len(via) == 0 || !strings.EqualFold(request.URL.Host, via[0].URL.Host) || request.URL.Scheme != via[0].URL.Scheme {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}
func hashCookieCloudUUID(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func normalizeCookieCloudCryptoType(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "legacy"
	}
	if value != "legacy" && value != "aes-128-cbc-fixed" {
		return "", appError(CodeCookieCloudResponseInvalid, "CookieCloud 加密格式不受支持", nil)
	}
	return value, nil
}

func decryptCookieCloudPayload(payload cookieCloudEncryptedPayload, uuid, password string) ([]cookieCloudEntry, error) {
	cryptoType, err := normalizeCookieCloudCryptoType(payload.CryptoType)
	if err != nil {
		return nil, err
	}
	if cryptoType == "legacy" {
		return decryptCookieCloud(payload.Encrypted, uuid, password)
	}
	return decryptCookieCloudFixed(payload.Encrypted, uuid, password)
}

func decryptCookieCloud(encrypted, uuid, password string) ([]cookieCloudEntry, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil || len(raw) < 32 || string(raw[:8]) != "Salted__" || (len(raw)-16)%aes.BlockSize != 0 {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 密文格式无效", nil)
	}
	digest := md5.Sum([]byte(uuid + "-" + password))
	passphrase := []byte(hex.EncodeToString(digest[:])[:16])
	keyIV := evpBytesToKey(passphrase, raw[8:16], 48)
	block, err := aes.NewCipher(keyIV[:32])
	if err != nil {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 密文无效", nil)
	}
	plain := make([]byte, len(raw)-16)
	cipher.NewCBCDecrypter(block, keyIV[32:48]).CryptBlocks(plain, raw[16:])
	plain, err = unpadCookieCloud(plain)
	if err != nil {
		return nil, err
	}
	return parseCookieCloudPlain(plain)
}

func decryptCookieCloudFixed(encrypted, uuid, password string) ([]cookieCloudEntry, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
	if err != nil || len(raw) == 0 || len(raw)%aes.BlockSize != 0 {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 密文格式无效", nil)
	}
	digest := md5.Sum([]byte(uuid + "-" + password))
	key := []byte(hex.EncodeToString(digest[:])[:16])
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 密文无效", nil)
	}
	plain := make([]byte, len(raw))
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(plain, raw)
	plain, err = unpadCookieCloud(plain)
	if err != nil {
		return nil, err
	}
	return parseCookieCloudPlain(plain)
}

func unpadCookieCloud(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, appError(CodeCookieCloudAuthentication, "CookieCloud 端到端密码错误", nil)
	}
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		return nil, appError(CodeCookieCloudAuthentication, "CookieCloud 端到端密码错误", nil)
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, appError(CodeCookieCloudAuthentication, "CookieCloud 端到端密码错误", nil)
		}
	}
	return plain[:len(plain)-padding], nil
}

func parseCookieCloudPlain(plain []byte) ([]cookieCloudEntry, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(plain, &root) != nil {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 解密数据无效", nil)
	}
	contents := root
	if nested := root["cookie_data"]; len(nested) > 0 {
		if json.Unmarshal(nested, &contents) != nil {
			return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud Cookie 数据无效", nil)
		}
	}
	entries := make([]cookieCloudEntry, 0)
	for _, rawEntries := range contents {
		var group []cookieCloudEntry
		if json.Unmarshal(rawEntries, &group) != nil {
			continue
		}
		entries = append(entries, group...)
		if len(entries) > 20000 {
			return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud Cookie 数量过多", nil)
		}
	}
	if len(entries) == 0 {
		return nil, appError(CodeCookieCloudResponseInvalid, "CookieCloud 中没有可用 Cookie", nil)
	}
	return entries, nil
}

func evpBytesToKey(passphrase, salt []byte, size int) []byte {
	result, previous := make([]byte, 0, size), []byte{}
	for len(result) < size {
		input := append(append(append([]byte{}, previous...), passphrase...), salt...)
		sum := md5.Sum(input)
		previous = sum[:]
		result = append(result, previous...)
	}
	return result[:size]
}

func cookiesByDomain(entries []cookieCloudEntry) map[string]map[string]string {
	grouped := map[string]map[string]string{}
	for _, entry := range entries {
		domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(entry.Domain)), ".")
		name, value := strings.TrimSpace(entry.Name), strings.TrimSpace(entry.Value)
		if domain == "" || name == "" || value == "" || strings.ContainsAny(name+value, "\x00\r\n;") || name == "CookieAutoDeleteBrowsingDataCleanup" || name == "CookieAutoDeleteCleaningDiscarded" {
			continue
		}
		if grouped[domain] == nil {
			grouped[domain] = map[string]string{}
		}
		grouped[domain][name] = value
	}
	return grouped
}

func cookieForHost(cookies map[string]map[string]string, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	matchingDomains := make([]string, 0)
	for domain := range cookies {
		if (host == domain || strings.HasSuffix(host, "."+domain)) && strings.Contains(domain, ".") {
			matchingDomains = append(matchingDomains, domain)
		}
	}
	sort.SliceStable(matchingDomains, func(i, j int) bool {
		if len(matchingDomains[i]) == len(matchingDomains[j]) {
			return matchingDomains[i] < matchingDomains[j]
		}
		return len(matchingDomains[i]) < len(matchingDomains[j])
	})
	merged := map[string]string{}
	for _, domain := range matchingDomains {
		for name, value := range cookies[domain] {
			merged[name] = value
		}
	}
	names := make([]string, 0, len(merged))
	nonCF := false
	for name := range merged {
		if !strings.EqualFold(name, "cf_clearance") {
			nonCF = true
		}
		names = append(names, name)
	}
	if !nonCF {
		return ""
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+merged[name])
	}
	return strings.Join(parts, "; ")
}
