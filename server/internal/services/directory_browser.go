package services

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/directory"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	"gorm.io/gorm"
)

const (
	CodeDirectoryTokenInvalid = "directory_token_invalid"
	CodeDirectoryTokenExpired = "directory_token_expired"
	CodeDirectoryNotFound     = "directory_not_found"
	CodeDirectoryUnreadable   = "directory_unreadable"
	CodeDirectoryUnavailable  = "directory_unavailable"
	CodeDirectoryBusy         = "directory_busy"
	CodeDirectoryRateLimited  = "directory_rate_limited"
	maxDirectoryRateKeys      = 4096

	tokenPurposeBrowse = "browse"
	tokenPurposeSelect = "select"
)

type DirectoryBrowserService struct {
	db      *gorm.DB
	adapter directory.Adapter
	key     []byte
	now     func() time.Time
	ttl     time.Duration
	timeout time.Duration
	active  chan struct{}
	mu      sync.Mutex
	limits  map[string]*browseWindow
}

type browseWindow struct {
	started time.Time
	count   int
}

type directoryToken struct {
	Version  int    `json:"v"`
	Path     string `json:"p"`
	Platform string `json:"o"`
	Adapter  string `json:"a"`
	Purpose  string `json:"u"`
	Expires  int64  `json:"e"`
}

type DirectoryItem struct {
	Name              string `json:"name"`
	Location          string `json:"location"`
	Token             string `json:"token,omitempty"`
	SelectionToken    string `json:"selection_token,omitempty"`
	Selectable        bool   `json:"selectable"`
	Enterable         bool   `json:"enterable"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
	Kind              string `json:"kind,omitempty"`
}

type DirectoryCrumb struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type DirectoryListing struct {
	Platform              string           `json:"platform"`
	Location              string           `json:"location"`
	CurrentToken          string           `json:"current_token"`
	CurrentSelectionToken string           `json:"current_selection_token"`
	ParentToken           string           `json:"parent_token,omitempty"`
	Breadcrumbs           []DirectoryCrumb `json:"breadcrumbs"`
	Items                 []DirectoryItem  `json:"items"`
	Truncated             bool             `json:"truncated"`
}

func NewDirectoryBrowserService(db *gorm.DB, adapter directory.Adapter) (*DirectoryBrowserService, error) {
	key := make([]byte, 64)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if adapter == nil {
		adapter = directory.NativeAdapter{}
	}
	return &DirectoryBrowserService{db: db, adapter: adapter, key: key, now: time.Now, ttl: 10 * time.Minute, timeout: 5 * time.Second, active: make(chan struct{}, 8), limits: map[string]*browseWindow{}}, nil
}

func (s *DirectoryBrowserService) Roots(ctx context.Context, actor Actor, request RequestContext) (DirectoryListing, error) {
	if err := s.authorize(actor); err != nil {
		return DirectoryListing{}, err
	}
	release, err := s.acquire(actor.User.ID, request.IPHint)
	if err != nil {
		return DirectoryListing{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type rootResult struct {
		items []directory.Root
		err   error
	}
	results := make(chan rootResult, 1)
	go func() {
		items, adapterErr := s.adapter.Roots(ctx)
		release()
		results <- rootResult{items: items, err: adapterErr}
	}()
	var roots []directory.Root
	select {
	case result := <-results:
		roots, err = result.items, result.err
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		return DirectoryListing{}, s.browserError(err)
	}
	items := make([]DirectoryItem, 0, len(roots))
	truncated := len(roots) > directory.DefaultResultLimit
	if truncated {
		roots = roots[:directory.DefaultResultLimit]
	}
	for _, root := range roots {
		item := DirectoryItem{Name: root.Name, Location: root.Path, Selectable: root.Selectable, Enterable: root.Enterable, UnavailableReason: root.Reason, Kind: root.Kind}
		if root.Enterable {
			item.Token, _ = s.sign(root.Path, tokenPurposeBrowse)
		}
		if root.Selectable {
			item.SelectionToken, _ = s.sign(root.Path, tokenPurposeSelect)
		}
		items = append(items, item)
	}
	return DirectoryListing{Platform: s.adapter.Platform(), Location: "", Breadcrumbs: []DirectoryCrumb{}, Items: items, Truncated: truncated}, nil
}

func (s *DirectoryBrowserService) List(ctx context.Context, actor Actor, token string, request RequestContext) (DirectoryListing, error) {
	if err := s.authorize(actor); err != nil {
		return DirectoryListing{}, err
	}
	path, err := s.resolve(token, tokenPurposeBrowse)
	if err != nil {
		return DirectoryListing{}, err
	}
	release, err := s.acquire(actor.User.ID, request.IPHint)
	if err != nil {
		return DirectoryListing{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	type listResult struct {
		items     []directory.Entry
		truncated bool
		err       error
	}
	results := make(chan listResult, 1)
	go func() {
		items, truncated, adapterErr := s.adapter.Directories(ctx, path, directory.DefaultResultLimit)
		release()
		results <- listResult{items: items, truncated: truncated, err: adapterErr}
	}()
	var entries []directory.Entry
	var truncated bool
	select {
	case result := <-results:
		entries, truncated, err = result.items, result.truncated, result.err
	case <-ctx.Done():
		err = ctx.Err()
	}
	if err != nil {
		return DirectoryListing{}, s.browserError(err)
	}
	items := make([]DirectoryItem, 0, len(entries))
	for _, entry := range entries {
		item := DirectoryItem{Name: entry.Name, Location: entry.Path, Selectable: entry.Selectable, Enterable: entry.Enterable, UnavailableReason: entry.Reason, Kind: "directory"}
		if entry.Enterable {
			item.Token, _ = s.sign(entry.Path, tokenPurposeBrowse)
		}
		if entry.Selectable {
			item.SelectionToken, _ = s.sign(entry.Path, tokenPurposeSelect)
		}
		items = append(items, item)
	}
	listing := DirectoryListing{Platform: s.adapter.Platform(), Location: path, Breadcrumbs: s.breadcrumbs(path), Items: items, Truncated: truncated}
	listing.CurrentToken, _ = s.sign(path, tokenPurposeBrowse)
	listing.CurrentSelectionToken, _ = s.sign(path, tokenPurposeSelect)
	parent := filepath.Dir(path)
	if parent != path {
		listing.ParentToken, _ = s.sign(parent, tokenPurposeBrowse)
	}
	return listing, nil
}

func (s *DirectoryBrowserService) StorageToken(ctx context.Context, actor Actor, storageID uint, request RequestContext) (DirectoryListing, error) {
	if err := s.authorize(actor); err != nil {
		return DirectoryListing{}, err
	}
	var record models.Storage
	if err := s.db.First(&record, storageID).Error; err != nil {
		return DirectoryListing{}, storageNotFound(err)
	}
	if _, err := (storagefs.LocalDriver{}).CanonicalizeRoot(record.RootPath); err != nil {
		return DirectoryListing{}, storagePathError(err)
	}
	if err := s.adapter.Validate(ctx, record.RootPath); err != nil {
		return DirectoryListing{}, s.browserError(err)
	}
	token, err := s.sign(record.RootPath, tokenPurposeBrowse)
	if err != nil {
		return DirectoryListing{}, err
	}
	return s.List(ctx, actor, token, request)
}

func (s *DirectoryBrowserService) ResolveSelection(ctx context.Context, actor Actor, token string) (string, error) {
	if !actor.Can(authz.PermissionStoragesBrowse) {
		return "", appError(CodePermissionDenied, "无权使用目录选择结果", nil)
	}
	path, err := s.resolve(token, tokenPurposeSelect)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.adapter.Validate(ctx, path); err != nil {
		return "", s.browserError(err)
	}
	canonical, err := (storagefs.LocalDriver{}).CanonicalizeRoot(path)
	if err != nil {
		return "", storagePathError(err)
	}
	return canonical, nil
}

func (s *DirectoryBrowserService) authorize(actor Actor) error {
	if !actor.Can(authz.PermissionStoragesBrowse) {
		return appError(CodePermissionDenied, "无权浏览 Server 目录", nil)
	}
	return nil
}

func (s *DirectoryBrowserService) acquire(userID uint, ipHint string) (func(), error) {
	now := s.now()
	key := uintID(userID) + "\x00" + ipHint
	s.mu.Lock()
	for candidate, existing := range s.limits {
		if now.Sub(existing.started) >= time.Minute {
			delete(s.limits, candidate)
		}
	}
	window := s.limits[key]
	if window == nil || now.Sub(window.started) >= time.Minute {
		if window == nil && len(s.limits) >= maxDirectoryRateKeys {
			s.mu.Unlock()
			return nil, appError(CodeDirectoryRateLimited, "目录浏览请求过于频繁", nil)
		}
		window = &browseWindow{started: now}
		s.limits[key] = window
	}
	if window.count >= 120 {
		s.mu.Unlock()
		return nil, appError(CodeDirectoryRateLimited, "目录浏览请求过于频繁", nil)
	}
	window.count++
	s.mu.Unlock()
	select {
	case s.active <- struct{}{}:
		return func() { <-s.active }, nil
	default:
		return nil, appError(CodeDirectoryBusy, "目录浏览服务繁忙", nil)
	}
}

func (s *DirectoryBrowserService) sign(path, purpose string) (string, error) {
	payload, err := json.Marshal(directoryToken{Version: 1, Path: filepath.Clean(path), Platform: s.adapter.Platform(), Adapter: s.adapter.Version(), Purpose: purpose, Expires: s.now().Add(s.ttl).Unix()})
	if err != nil {
		return "", err
	}
	signature := hmac.New(sha256.New, s.key[:32])
	_, _ = signature.Write(payload)
	plaintext := append(payload, signature.Sum(nil)...)
	block, err := aes.NewCipher(s.key[32:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *DirectoryBrowserService) resolve(token, purpose string) (string, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	block, err := aes.NewCipher(s.key[32:])
	if err != nil {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	plaintext, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil || len(plaintext) <= sha256.Size {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	payload := plaintext[:len(plaintext)-sha256.Size]
	provided := plaintext[len(plaintext)-sha256.Size:]
	signature := hmac.New(sha256.New, s.key[:32])
	_, _ = signature.Write(payload)
	if !hmac.Equal(signature.Sum(nil), provided) {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	var claims directoryToken
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Version != 1 || claims.Purpose != purpose || claims.Platform != s.adapter.Platform() || claims.Adapter != s.adapter.Version() || !filepath.IsAbs(claims.Path) {
		return "", appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	if s.now().Unix() >= claims.Expires {
		return "", appError(CodeDirectoryTokenExpired, "目录令牌已过期，请重新选择", nil)
	}
	return filepath.Clean(claims.Path), nil
}

func (s *DirectoryBrowserService) breadcrumbs(path string) []DirectoryCrumb {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	current := volume + string(filepath.Separator)
	if volume == "" {
		current = string(filepath.Separator)
	}
	crumbs := []DirectoryCrumb{}
	rootToken, _ := s.sign(current, tokenPurposeBrowse)
	crumbs = append(crumbs, DirectoryCrumb{Name: current, Token: rootToken})
	for _, part := range parts {
		current = filepath.Join(current, part)
		token, _ := s.sign(current, tokenPurposeBrowse)
		crumbs = append(crumbs, DirectoryCrumb{Name: part, Token: token})
	}
	return crumbs
}

func (s *DirectoryBrowserService) browserError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return appError(CodeDirectoryUnavailable, "目录读取已取消或超时", nil)
	}
	var adapterErr *directory.AdapterError
	if errors.As(err, &adapterErr) {
		switch adapterErr.Kind {
		case directory.ErrorNotFound:
			return appError(CodeDirectoryNotFound, "目录不存在或已失效", nil)
		case directory.ErrorUnreadable:
			return appError(CodeDirectoryUnreadable, "无权读取该目录", nil)
		}
	}
	return appError(CodeDirectoryUnavailable, "目录暂时不可用", nil)
}
