package services

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/config"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const bcryptCost = 12

const (
	deviceClientKind    = "player"
	deviceTokenPrefix   = "omc_player_"
	deviceTouchInterval = 5 * time.Minute
)

type LoginAttempt struct {
	Count       int
	WindowStart time.Time
}

type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]LoginAttempt
	now      func() time.Time
}

func NewLoginLimiter() *LoginLimiter {
	return &LoginLimiter{attempts: map[string]LoginAttempt{}, now: time.Now}
}

func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	attempt := l.attempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= 15*time.Minute {
		delete(l.attempts, key)
		return true
	}
	return attempt.Count < 5
}

func (l *LoginLimiter) Failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	attempt := l.attempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) >= 15*time.Minute {
		attempt = LoginAttempt{WindowStart: now}
	}
	attempt.Count++
	l.attempts[key] = attempt
}

func (l *LoginLimiter) Success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

type AuthService struct {
	db        *gorm.DB
	config    config.Config
	authz     *AuthorizationService
	audit     *AuditService
	limiter   *LoginLimiter
	csrfKey   []byte
	dummyHash []byte
	setupMu   sync.Mutex
	now       func() time.Time
}

func NewAuthService(db *gorm.DB, cfg config.Config, authorization *AuthorizationService, audit *AuditService) (*AuthService, error) {
	if cfg.DeviceTokenIdleTTL <= 0 {
		cfg.DeviceTokenIdleTTL = 30 * 24 * time.Hour
	}
	if cfg.DeviceTokenMaxTTL <= 0 {
		cfg.DeviceTokenMaxTTL = 180 * 24 * time.Hour
	}
	csrfKey := make([]byte, 32)
	if _, err := rand.Read(csrfKey); err != nil {
		return nil, fmt.Errorf("generate csrf key: %w", err)
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("ohmycine-login-timing-placeholder"), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("generate login timing hash: %w", err)
	}
	return &AuthService{db: db, config: cfg, authz: authorization, audit: audit, limiter: NewLoginLimiter(), csrfKey: csrfKey, dummyHash: dummyHash, now: time.Now}, nil
}

func NormalizeUsername(username string) string { return strings.ToLower(strings.TrimSpace(username)) }

func validateUsername(username string) error {
	normalized := NormalizeUsername(username)
	if len(normalized) < 3 || len(normalized) > 64 {
		return appError(CodeInvalidRequest, "用户名长度应为 3 到 64 个字符", nil)
	}
	for _, char := range normalized {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return appError(CodeInvalidRequest, "用户名只能包含字母、数字、点、短横线和下划线", nil)
	}
	return nil
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 128 {
		return appError(CodeInvalidRequest, "密码长度应为 12 到 128 个字符", nil)
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

func (s *AuthService) SetupRequired() (bool, bool, error) {
	var userCount int64
	if err := s.db.Model(&models.User{}).Count(&userCount).Error; err != nil {
		return false, false, err
	}
	if userCount == 0 {
		return true, false, nil
	}
	var ownerCount int64
	if err := s.db.Model(&models.User{}).Where("is_owner = ?", true).Count(&ownerCount).Error; err != nil {
		return false, false, err
	}
	return false, ownerCount == 0, nil
}

type SetupInput struct {
	Username    string
	DisplayName string
	Password    string
}

func (s *AuthService) SetupOwner(input SetupInput, request RequestContext) (Actor, string, error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if err := validateUsername(input.Username); err != nil {
		return Actor{}, "", err
	}
	hash, err := HashPassword(input.Password)
	if err != nil {
		return Actor{}, "", err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(input.Username)
	}
	var created models.User
	var token string
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&models.User{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return appError(CodeSetupComplete, "Server 已完成首次设置", nil)
		}
		var administrator models.Role
		if err := tx.Where("code = ?", authz.RoleAdministrator).First(&administrator).Error; err != nil {
			return err
		}
		created = models.User{Username: strings.TrimSpace(input.Username), UsernameNormalized: NormalizeUsername(input.Username), DisplayName: displayName, PasswordHash: hash, Status: models.UserStatusActive, IsOwner: true, AuthzVersion: 1}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := tx.Create(&models.UserRole{UserID: created.ID, RoleID: administrator.ID, AssignedBy: &created.ID}).Error; err != nil {
			return err
		}
		var err error
		token, err = s.createSession(tx, created.ID, "", request.IPHint)
		if err != nil {
			return err
		}
		return s.audit.Record(tx, &created.ID, "setup.owner_created", "user", uintID(created.ID), "success", map[string]any{"username": created.Username}, request)
	})
	if err != nil {
		return Actor{}, "", err
	}
	actor, err := s.authz.Resolve(created.ID)
	if err != nil {
		return Actor{}, "", err
	}
	return actor, token, nil
}

func (s *AuthService) Login(username, password, userAgent string, request RequestContext) (Actor, string, error) {
	user, actor, key, err := s.verifyLogin(username, password, request, "auth.login")
	if err != nil {
		return Actor{}, "", err
	}
	var token string
	err = s.db.Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		if err := tx.Model(&user).Update("last_login_at", now).Error; err != nil {
			return err
		}
		var err error
		token, err = s.createSession(tx, user.ID, userAgent, request.IPHint)
		if err != nil {
			return err
		}
		return s.audit.Record(tx, &user.ID, "auth.login", "user", uintID(user.ID), "success", map[string]any{}, request)
	})
	if err != nil {
		return Actor{}, "", err
	}
	s.limiter.Success(key)
	return actor, token, nil
}

func (s *AuthService) verifyLogin(username, password string, request RequestContext, auditAction string) (models.User, Actor, string, error) {
	key := request.IPHint + "|" + NormalizeUsername(username)
	if !s.limiter.Allow(key) {
		_ = s.audit.Record(nil, nil, auditAction, "user", NormalizeUsername(username), "rate_limited", map[string]any{}, request)
		return models.User{}, Actor{}, key, appError(CodeLoginRateLimited, "登录尝试过多，请稍后再试", nil)
	}
	var user models.User
	err := s.db.Where("username_normalized = ?", NormalizeUsername(username)).First(&user).Error
	hash := s.dummyHash
	if err == nil {
		hash = []byte(user.PasswordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
	if err != nil || !passwordMatches || user.Status != models.UserStatusActive {
		s.limiter.Failure(key)
		_ = s.audit.Record(nil, nil, auditAction, "user", NormalizeUsername(username), "failure", map[string]any{}, request)
		return models.User{}, Actor{}, key, appError(CodeInvalidCredentials, "用户名或密码错误", nil)
	}
	actor, err := s.authz.Resolve(user.ID)
	if err != nil {
		return models.User{}, Actor{}, key, err
	}
	return user, actor, key, nil
}

// DeviceLogin verifies the password once and issues a long-lived, revocable
// Player credential. The password and raw device ID are deliberately not kept.
func (s *AuthService) DeviceLogin(username, password, deviceID, deviceName string, request RequestContext) (Actor, string, models.DeviceToken, error) {
	deviceID = strings.TrimSpace(deviceID)
	deviceName = strings.TrimSpace(deviceName)
	if len(deviceID) < 8 || len(deviceID) > 256 || strings.ContainsAny(deviceID, "\r\n") {
		return Actor{}, "", models.DeviceToken{}, appError(CodeInvalidRequest, "设备标识无效", nil)
	}
	if deviceName == "" || len(deviceName) > 128 || strings.ContainsAny(deviceName, "\r\n") {
		return Actor{}, "", models.DeviceToken{}, appError(CodeInvalidRequest, "设备名称无效", nil)
	}
	user, actor, key, err := s.verifyLogin(username, password, request, "player.auth.login")
	if err != nil {
		return Actor{}, "", models.DeviceToken{}, err
	}
	var rawToken string
	var device models.DeviceToken
	err = s.db.Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		deviceHash := textHash(deviceID)
		if err := tx.Model(&models.DeviceToken{}).
			Where("user_id = ? AND device_id_hash = ? AND client_kind = ? AND revoked_at IS NULL", user.ID, deviceHash, deviceClientKind).
			Update("revoked_at", now).Error; err != nil {
			return err
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return fmt.Errorf("generate device token: %w", err)
		}
		rawToken = deviceTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
		id, err := randomID()
		if err != nil {
			return err
		}
		device = models.DeviceToken{
			ID: id, TokenHash: tokenHash(rawToken), UserID: user.ID, DeviceIDHash: deviceHash,
			DeviceName: deviceName, ClientKind: deviceClientKind, CreatedAt: now, LastSeenAt: now,
			IdleExpiresAt: now.Add(s.config.DeviceTokenIdleTTL), AbsoluteExpiresAt: now.Add(s.config.DeviceTokenMaxTTL),
		}
		if err := tx.Create(&device).Error; err != nil {
			return err
		}
		if err := tx.Model(&user).Update("last_login_at", now).Error; err != nil {
			return err
		}
		return s.audit.Record(tx, &user.ID, "player.auth.login", "device", device.ID, "success", map[string]any{"device_name": deviceName}, request)
	})
	if err != nil {
		return Actor{}, "", models.DeviceToken{}, err
	}
	s.limiter.Success(key)
	return actor, rawToken, device, nil
}

func (s *AuthService) AuthenticateDevice(token string) (Actor, models.DeviceToken, error) {
	if len(token) > 128 || !strings.HasPrefix(token, deviceTokenPrefix) {
		return Actor{}, models.DeviceToken{}, appError(CodeNotAuthenticated, "Player 凭据无效", nil)
	}
	var device models.DeviceToken
	if err := s.db.Where("token_hash = ? AND client_kind = ?", tokenHash(token), deviceClientKind).First(&device).Error; err != nil {
		return Actor{}, models.DeviceToken{}, appError(CodeNotAuthenticated, "Player 凭据无效", err)
	}
	now := s.now().UTC()
	if device.RevokedAt != nil || !now.Before(device.IdleExpiresAt) || !now.Before(device.AbsoluteExpiresAt) {
		return Actor{}, models.DeviceToken{}, appError(CodeNotAuthenticated, "Player 凭据已过期或被撤销", nil)
	}
	actor, err := s.authz.Resolve(device.UserID)
	if err != nil {
		return Actor{}, models.DeviceToken{}, err
	}
	if now.Sub(device.LastSeenAt) >= deviceTouchInterval {
		updates := map[string]any{"last_seen_at": now, "idle_expires_at": minTime(now.Add(s.config.DeviceTokenIdleTTL), device.AbsoluteExpiresAt)}
		if err := s.db.Model(&device).Updates(updates).Error; err != nil {
			return Actor{}, models.DeviceToken{}, err
		}
		device.LastSeenAt = now
		device.IdleExpiresAt = updates["idle_expires_at"].(time.Time)
	}
	return actor, device, nil
}

func (s *AuthService) ListDevices(actor Actor) ([]models.DeviceToken, error) {
	now := s.now().UTC()
	var devices []models.DeviceToken
	err := s.db.Where("user_id = ? AND client_kind = ? AND revoked_at IS NULL AND idle_expires_at > ? AND absolute_expires_at > ?", actor.User.ID, deviceClientKind, now, now).
		Order("last_seen_at DESC, created_at DESC").Find(&devices).Error
	return devices, err
}

func (s *AuthService) RevokeDevice(actor Actor, deviceID string, request RequestContext) error {
	now := s.now().UTC()
	result := s.db.Model(&models.DeviceToken{}).Where("id = ? AND user_id = ? AND client_kind = ? AND revoked_at IS NULL", deviceID, actor.User.ID, deviceClientKind).Update("revoked_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return appError(CodeNotFound, "Player 设备不存在", nil)
	}
	return s.audit.Record(nil, &actor.User.ID, "player.device.revoke", "device", deviceID, "success", map[string]any{}, request)
}

func (s *AuthService) DeviceLogout(token string, actor Actor, device models.DeviceToken, request RequestContext) error {
	now := s.now().UTC()
	if err := s.db.Model(&models.DeviceToken{}).Where("id = ? AND token_hash = ? AND user_id = ? AND revoked_at IS NULL", device.ID, tokenHash(token), actor.User.ID).Update("revoked_at", now).Error; err != nil {
		return err
	}
	return s.audit.Record(nil, &actor.User.ID, "player.auth.logout", "device", device.ID, "success", map[string]any{}, request)
}

func (s *AuthService) createSession(db *gorm.DB, userID uint, userAgent, ipHint string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now().UTC()
	sessionID, err := randomID()
	if err != nil {
		return "", err
	}
	session := models.Session{
		ID: sessionID, TokenHash: tokenHash(token), UserID: userID, CreatedAt: now, LastSeenAt: now,
		IdleExpiresAt: now.Add(s.config.SessionIdleTTL), AbsoluteExpiresAt: now.Add(s.config.SessionMaxTTL),
		UserAgentHash: textHash(userAgent), IPHint: ipHint,
	}
	if err := db.Create(&session).Error; err != nil {
		return "", err
	}
	return token, nil
}

func (s *AuthService) Authenticate(token string) (Actor, models.Session, error) {
	if token == "" {
		return Actor{}, models.Session{}, appError(CodeNotAuthenticated, "请先登录", nil)
	}
	var session models.Session
	if err := s.db.Where("token_hash = ?", tokenHash(token)).First(&session).Error; err != nil {
		return Actor{}, models.Session{}, appError(CodeNotAuthenticated, "登录会话无效", err)
	}
	now := s.now().UTC()
	if session.RevokedAt != nil || !now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		return Actor{}, models.Session{}, appError(CodeNotAuthenticated, "登录会话已过期", nil)
	}
	actor, err := s.authz.Resolve(session.UserID)
	if err != nil {
		return Actor{}, models.Session{}, err
	}
	updates := map[string]any{"last_seen_at": now, "idle_expires_at": minTime(now.Add(s.config.SessionIdleTTL), session.AbsoluteExpiresAt)}
	if err := s.db.Model(&session).Updates(updates).Error; err != nil {
		return Actor{}, models.Session{}, err
	}
	return actor, session, nil
}

func (s *AuthService) Logout(token string, actor Actor, request RequestContext) error {
	now := s.now().UTC()
	if err := s.db.Model(&models.Session{}).Where("token_hash = ? AND revoked_at IS NULL", tokenHash(token)).Update("revoked_at", now).Error; err != nil {
		return err
	}
	_ = s.audit.Record(nil, &actor.User.ID, "auth.logout", "session", "current", "success", map[string]any{}, request)
	return nil
}

func (s *AuthService) RevokeUserSessions(tx *gorm.DB, userID uint) error {
	now := s.now().UTC()
	if err := tx.Model(&models.Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error; err != nil {
		return err
	}
	return tx.Model(&models.DeviceToken{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
}

func (s *AuthService) CSRFToken(sessionToken string) string {
	mac := hmac.New(sha256.New, s.csrfKey)
	_, _ = mac.Write([]byte("ohmycine-csrf-v1\x00" + sessionToken))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *AuthService) ValidateCSRF(sessionToken, submitted string) bool {
	expected := s.CSRFToken(sessionToken)
	return hmac.Equal([]byte(expected), []byte(submitted))
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func textHash(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func randomID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
