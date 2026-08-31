package services

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/authz"
	"github.com/yuanjing-hash/ohmycine/server/internal/credential"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

const providerDirectoryTokenPurpose = "provider-directory-token:v1"

const (
	providerTokenPurposePage = "page"
	maxProviderParentDepth   = 128
)

type ProviderDirectoryService struct {
	connections *ConnectionService
	credentials *credential.Store
	now         func() time.Time
	ttl         time.Duration
}

type providerDirectoryClaims struct {
	Version       int    `json:"v"`
	ActorID       uint   `json:"actor_id"`
	ConnectionID  uint   `json:"connection_id"`
	StorageID     uint   `json:"storage_id,omitempty"`
	StorageRootID string `json:"storage_root_id,omitempty"`
	ItemID        string `json:"item_id"`
	ParentID      string `json:"parent_id"`
	DisplayPath   string `json:"display_path"`
	Purpose       string `json:"purpose"`
	Offset        int64  `json:"offset,omitempty"`
	ExpiresAt     int64  `json:"expires_at"`
}

type ProviderDirectorySelection struct {
	ConnectionID uint
	ProviderID   string
	RelativeRoot string
	DisplayPath  string
}

func NewProviderDirectoryService(connections *ConnectionService, credentials *credential.Store) *ProviderDirectoryService {
	return &ProviderDirectoryService{connections: connections, credentials: credentials, now: time.Now, ttl: 10 * time.Minute}
}

// Browse is connection-scoped and is used only while selecting a Storage root.
func (s *ProviderDirectoryService) Browse(ctx context.Context, actor Actor, connectionID uint, token, pageToken string) (DirectoryListing, error) {
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassInteractive)
	if !actor.Can(authz.PermissionConnectionsRead) || !actor.Can(authz.PermissionStoragesBrowse) {
		return DirectoryListing{}, appError(CodePermissionDenied, "无权浏览网盘目录", nil)
	}
	claims := providerDirectoryClaims{ActorID: actor.User.ID, ConnectionID: connectionID, ItemID: "0", DisplayPath: "/"}
	resolved, err := s.resolveBrowseClaims(actor, connectionID, 0, "", token, pageToken)
	if err != nil {
		return DirectoryListing{}, err
	}
	if resolved.ItemID != "" {
		claims = resolved
	}
	_, driver, err := s.connections.Driver(actor, connectionID)
	if err != nil {
		return DirectoryListing{}, err
	}
	return s.browse(ctx, actor, driver, claims)
}

// BrowseStorage keeps all navigation rooted inside one registered Storage.
func (s *ProviderDirectoryService) BrowseStorage(ctx context.Context, actor Actor, storageID uint, token, pageToken string) (DirectoryListing, error) {
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassInteractive)
	if !actor.Can(authz.PermissionStoragesBrowse) {
		return DirectoryListing{}, appError(CodePermissionDenied, "无权浏览网盘目录", nil)
	}
	storage, err := s.providerStorage(storageID)
	if err != nil {
		return DirectoryListing{}, err
	}
	claims := providerDirectoryClaims{
		ActorID: actor.User.ID, ConnectionID: *storage.ConnectionID, StorageID: storage.ID,
		StorageRootID: storage.RootPath, ItemID: storage.RootPath, DisplayPath: "/",
	}
	resolved, err := s.resolveBrowseClaims(actor, *storage.ConnectionID, storage.ID, storage.RootPath, token, pageToken)
	if err != nil {
		return DirectoryListing{}, err
	}
	if resolved.ItemID != "" {
		claims = resolved
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return DirectoryListing{}, err
	}
	verified, accelerated, verifyErr := verifyProviderDirectoryPath(ctx, driver, storage.RootDisplayPath, claims.DisplayPath, claims.ItemID)
	if verifyErr != nil {
		return DirectoryListing{}, providerDirectoryError(verifyErr)
	}
	if !accelerated {
		if err := s.ensureWithinStorage(ctx, driver, claims.ItemID, storage.RootPath); err != nil {
			return DirectoryListing{}, err
		}
		verified, verifyErr = driver.Stat(ctx, claims.ItemID)
		if verifyErr != nil || !verified.IsDir {
			return DirectoryListing{}, providerDirectoryError(verifyErr)
		}
	}
	return s.browse(ctx, actor, driver, claims, verified)
}

func (s *ProviderDirectoryService) ResolveSelection(ctx context.Context, actor Actor, connectionID uint, token string) (ProviderDirectorySelection, error) {
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassInteractive)
	claims, err := s.resolve(actor, token, tokenPurposeSelect)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	if claims.ConnectionID != connectionID || claims.StorageID != 0 || claims.StorageRootID != "" {
		return ProviderDirectorySelection{}, appError(CodeDirectoryTokenInvalid, "目录令牌与数据源不匹配", nil)
	}
	_, driver, err := s.connections.Driver(actor, connectionID)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	item, accelerated, err := verifyProviderDirectoryPath(ctx, driver, "/", claims.DisplayPath, claims.ItemID)
	if err != nil {
		return ProviderDirectorySelection{}, providerDirectoryError(err)
	}
	if !accelerated {
		item, err = driver.Stat(ctx, claims.ItemID)
		if err != nil || !item.IsDir {
			return ProviderDirectorySelection{}, providerDirectoryError(err)
		}
	}
	return ProviderDirectorySelection{ConnectionID: connectionID, ProviderID: item.ID, RelativeRoot: claims.DisplayPath, DisplayPath: claims.DisplayPath}, nil
}

func (s *ProviderDirectoryService) ResolveStorageSelection(ctx context.Context, actor Actor, storageID uint, token string) (ProviderDirectorySelection, error) {
	ctx = cloudpkg.WithReadClass(ctx, cloudpkg.ReadClassInteractive)
	if !actor.Can(authz.PermissionStoragesBrowse) {
		return ProviderDirectorySelection{}, appError(CodePermissionDenied, "无权使用目录选择结果", nil)
	}
	storage, err := s.providerStorage(storageID)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	claims, err := s.resolve(actor, token, tokenPurposeSelect)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	if claims.ConnectionID != *storage.ConnectionID || claims.StorageID != storage.ID || claims.StorageRootID != storage.RootPath {
		return ProviderDirectorySelection{}, appError(CodeDirectoryTokenInvalid, "目录令牌与 Storage 不匹配", nil)
	}
	_, driver, err := s.connections.driver(*storage.ConnectionID)
	if err != nil {
		return ProviderDirectorySelection{}, err
	}
	item, accelerated, err := verifyProviderDirectoryPath(ctx, driver, storage.RootDisplayPath, claims.DisplayPath, claims.ItemID)
	if err != nil {
		return ProviderDirectorySelection{}, providerDirectoryError(err)
	}
	if !accelerated {
		if err := s.ensureWithinStorage(ctx, driver, claims.ItemID, storage.RootPath); err != nil {
			return ProviderDirectorySelection{}, err
		}
		item, err = driver.Stat(ctx, claims.ItemID)
		if err != nil || !item.IsDir {
			return ProviderDirectorySelection{}, providerDirectoryError(err)
		}
	}
	relative, err := normalizeProviderRelativePath(claims.DisplayPath)
	if err != nil {
		return ProviderDirectorySelection{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	return ProviderDirectorySelection{ConnectionID: *storage.ConnectionID, ProviderID: item.ID, RelativeRoot: relative, DisplayPath: relative}, nil
}

func (s *ProviderDirectoryService) browse(ctx context.Context, actor Actor, driver cloudpkg.Driver, claims providerDirectoryClaims, verified ...cloudpkg.Item) (DirectoryListing, error) {
	if claims.ItemID != "0" {
		var item cloudpkg.Item
		if len(verified) > 0 && verified[0].ID == claims.ItemID {
			item = verified[0]
		} else {
			resolved, accelerated, resolveErr := verifyProviderDirectoryPath(ctx, driver, "/", claims.DisplayPath, claims.ItemID)
			if resolveErr != nil {
				return DirectoryListing{}, providerDirectoryError(resolveErr)
			}
			if accelerated {
				item = resolved
			} else {
				item, resolveErr = driver.Stat(ctx, claims.ItemID)
				if resolveErr != nil || !item.IsDir {
					return DirectoryListing{}, providerDirectoryError(resolveErr)
				}
			}
		}
		if item.ParentID != "" {
			claims.ParentID = item.ParentID
		}
	}
	pageResult, err := driver.List(ctx, claims.ItemID, cloudpkg.PageRequest{Offset: claims.Offset, Limit: 200})
	if err != nil {
		return DirectoryListing{}, providerDirectoryError(err)
	}
	items := make([]DirectoryItem, 0, len(pageResult.Items))
	for _, item := range pageResult.Items {
		if !item.IsDir {
			continue
		}
		if item.ID == "" || item.ParentID != claims.ItemID || !validProviderName(item.Name) {
			return DirectoryListing{}, providerDirectoryError(errors.New("provider returned an invalid directory item"))
		}
		display := joinProviderDisplayPath(claims.DisplayPath, item.Name)
		browseToken, err := s.sign(claims.scoped(actor.User.ID, item.ID, claims.ItemID, display, tokenPurposeBrowse, 0))
		if err != nil {
			return DirectoryListing{}, err
		}
		selectionToken, err := s.sign(claims.scoped(actor.User.ID, item.ID, claims.ItemID, display, tokenPurposeSelect, 0))
		if err != nil {
			return DirectoryListing{}, err
		}
		items = append(items, DirectoryItem{Name: item.Name, Location: display, Token: browseToken, SelectionToken: selectionToken, Selectable: true, Enterable: true, Kind: "cloud-directory"})
	}
	currentBrowse, err := s.sign(claims.scoped(actor.User.ID, claims.ItemID, claims.ParentID, claims.DisplayPath, tokenPurposeBrowse, 0))
	if err != nil {
		return DirectoryListing{}, err
	}
	currentSelect, err := s.sign(claims.scoped(actor.User.ID, claims.ItemID, claims.ParentID, claims.DisplayPath, tokenPurposeSelect, 0))
	if err != nil {
		return DirectoryListing{}, err
	}
	listing := DirectoryListing{
		Platform:              cloudpkg.ProviderPan115,
		Location:              claims.DisplayPath,
		CurrentToken:          currentBrowse,
		CurrentSelectionToken: currentSelect,
		Breadcrumbs:           make([]DirectoryCrumb, 0),
		Items:                 items,
		Truncated:             false,
	}
	if pageResult.HasMore && len(pageResult.Items) > 0 {
		listing.NextPageToken, err = s.sign(claims.scoped(actor.User.ID, claims.ItemID, claims.ParentID, claims.DisplayPath, providerTokenPurposePage, claims.Offset+int64(len(pageResult.Items))))
		if err != nil {
			return DirectoryListing{}, err
		}
	}
	if claims.ItemID != "0" && (claims.StorageID == 0 || claims.ItemID != claims.StorageRootID) {
		parentDisplay := path.Dir(claims.DisplayPath)
		listing.ParentToken, _ = s.sign(claims.scoped(actor.User.ID, claims.ParentID, "", parentDisplay, tokenPurposeBrowse, 0))
	}
	return listing, nil
}

func verifyProviderDirectoryPath(ctx context.Context, driver cloudpkg.Driver, rootDisplayPath, relativePath, expectedID string) (cloudpkg.Item, bool, error) {
	resolver, ok := driver.(cloudpkg.DirectoryPathResolver)
	if !ok {
		return cloudpkg.Item{}, false, nil
	}
	providerPath, err := joinProviderPath(rootDisplayPath, relativePath)
	if err != nil {
		// Legacy Storage records without a display path retain the ancestry
		// fallback instead of weakening their boundary proof.
		return cloudpkg.Item{}, false, nil
	}
	item, err := resolver.ResolveDirectory(ctx, providerPath)
	if err != nil {
		return cloudpkg.Item{}, true, err
	}
	if !item.IsDir || strings.TrimSpace(item.ID) != strings.TrimSpace(expectedID) {
		return cloudpkg.Item{}, true, cloudpkg.Error(cloudpkg.CodeNotFound, false, errors.New("provider directory identity changed"))
	}
	return item, true, nil
}

func joinProviderPath(rootDisplayPath, relativePath string) (string, error) {
	root, err := normalizeProviderRelativePath(strings.TrimSpace(rootDisplayPath))
	if err != nil {
		return "", err
	}
	relative, err := normalizeProviderRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	if relative == "/" {
		return root, nil
	}
	if root == "/" {
		return relative, nil
	}
	return path.Clean(strings.TrimSuffix(root, "/") + relative), nil
}

func (s *ProviderDirectoryService) resolveBrowseClaims(actor Actor, connectionID, storageID uint, storageRootID, token, pageToken string) (providerDirectoryClaims, error) {
	if strings.TrimSpace(pageToken) != "" {
		claims, err := s.resolve(actor, pageToken, providerTokenPurposePage)
		if err != nil {
			return providerDirectoryClaims{}, err
		}
		if claims.ConnectionID != connectionID || claims.StorageID != storageID || claims.StorageRootID != storageRootID || claims.Offset <= 0 {
			return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "分页令牌与目录范围不匹配", nil)
		}
		return claims, nil
	}
	if strings.TrimSpace(token) == "" {
		return providerDirectoryClaims{}, nil
	}
	claims, err := s.resolve(actor, token, tokenPurposeBrowse)
	if err != nil {
		return providerDirectoryClaims{}, err
	}
	if claims.ConnectionID != connectionID || claims.StorageID != storageID || claims.StorageRootID != storageRootID {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌与目录范围不匹配", nil)
	}
	return claims, nil
}

func (s *ProviderDirectoryService) providerStorage(storageID uint) (models.Storage, error) {
	var storage models.Storage
	if err := s.connections.db.First(&storage, storageID).Error; err != nil {
		return storage, storageNotFound(err)
	}
	if storage.Type != models.StorageTypePan115 || storage.ConnectionID == nil || strings.TrimSpace(storage.RootPath) == "" {
		return storage, appError(CodeStorageTypeUnsupported, "该 Storage 不支持网盘目录浏览", nil)
	}
	return storage, nil
}

func (s *ProviderDirectoryService) ensureWithinStorage(ctx context.Context, driver cloudpkg.Driver, itemID, rootID string) error {
	itemID, rootID = strings.TrimSpace(itemID), strings.TrimSpace(rootID)
	if itemID == "" || rootID == "" {
		return appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	visited := make(map[string]struct{}, maxProviderParentDepth)
	current := itemID
	for depth := 0; depth < maxProviderParentDepth; depth++ {
		if current == rootID {
			return nil
		}
		if current == "" || current == "0" {
			return appError(CodeMediaLibraryPathInvalid, "所选网盘目录不在 Storage 范围内", nil)
		}
		if _, exists := visited[current]; exists {
			return appError(CodeDirectoryUnavailable, "115 目录暂时不可用", nil)
		}
		visited[current] = struct{}{}
		item, err := driver.Stat(ctx, current)
		if err != nil || !item.IsDir {
			return providerDirectoryError(err)
		}
		current = strings.TrimSpace(item.ParentID)
	}
	return appError(CodeMediaLibraryPathInvalid, "所选网盘目录不在 Storage 范围内", nil)
}

func (s *ProviderDirectoryService) sign(claims providerDirectoryClaims) (string, error) {
	claims.Version = 2
	claims.ExpiresAt = s.now().Add(s.ttl).Unix()
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return s.credentials.Encrypt(providerDirectoryTokenPurpose, string(payload))
}

func (claims providerDirectoryClaims) scoped(actorID uint, itemID, parentID, displayPath, purpose string, offset int64) providerDirectoryClaims {
	return providerDirectoryClaims{
		ActorID: actorID, ConnectionID: claims.ConnectionID, StorageID: claims.StorageID, StorageRootID: claims.StorageRootID,
		ItemID: itemID, ParentID: parentID, DisplayPath: displayPath, Purpose: purpose, Offset: offset,
	}
}

func (s *ProviderDirectoryService) resolve(actor Actor, token, purpose string) (providerDirectoryClaims, error) {
	if len(token) == 0 || len(token) > 8192 {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	payload, err := s.credentials.Decrypt(providerDirectoryTokenPurpose, token)
	if err != nil {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	var claims providerDirectoryClaims
	if json.Unmarshal([]byte(payload), &claims) != nil || claims.Version != 2 || claims.ActorID != actor.User.ID || claims.ConnectionID == 0 || claims.ItemID == "" || claims.Purpose != purpose || !strings.HasPrefix(claims.DisplayPath, "/") {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	if (claims.StorageID == 0) != (claims.StorageRootID == "") {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	if _, err := normalizeProviderRelativePath(claims.DisplayPath); err != nil {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenInvalid, "目录令牌无效", nil)
	}
	if s.now().Unix() >= claims.ExpiresAt {
		return providerDirectoryClaims{}, appError(CodeDirectoryTokenExpired, "目录令牌已过期，请重新选择", nil)
	}
	return claims, nil
}

func normalizeProviderRelativePath(value string) (string, error) {
	if strings.ContainsAny(value, "\x00\r\n\\") || !strings.HasPrefix(value, "/") {
		return "", errors.New("invalid provider-relative path")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "/../") {
		return "", errors.New("invalid provider-relative path")
	}
	return clean, nil
}

func validProviderName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "\x00\r\n/\\")
}

func joinProviderDisplayPath(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return strings.TrimSuffix(parent, "/") + "/" + name
}

func providerDirectoryError(err error) error {
	if err == nil {
		return appError(CodeDirectoryNotFound, "网盘目录不存在或已失效", nil)
	}
	code, _ := cloudpkg.ErrorInfo(err)
	if code == cloudpkg.CodeNotFound {
		return appError(CodeDirectoryNotFound, "网盘目录不存在或已失效", nil)
	}
	if code == cloudpkg.CodeRateLimited {
		return appError(CodeDirectoryRateLimited, "115 请求受到限速，请稍后重试", nil)
	}
	return appError(CodeDirectoryUnavailable, "115 目录暂时不可用", err)
}
