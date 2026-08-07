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
	key := request.IPHint + "|" + NormalizeUsername(username)
	if !s.limiter.Allow(key) {
		_ = s.audit.Record(nil, nil, "auth.login", "user", NormalizeUsername(username), "rate_limited", map[string]any{}, request)
		return Actor{}, "", appError(CodeLoginRateLimited, "登录尝试过多，请稍后再试", nil)
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
		_ = s.audit.Record(nil, nil, "auth.login", "user", NormalizeUsername(username), "failure", map[string]any{}, request)
		return Actor{}, "", appError(CodeInvalidCredentials, "用户名或密码错误", nil)
	}
	s.limiter.Success(key)
	actor, err := s.authz.Resolve(user.ID)
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
	return actor, token, nil
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
	return tx.Model(&models.Session{}).Where("user_id = ? AND revoked_at IS NULL", userID).Update("revoked_at", now).Error
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
