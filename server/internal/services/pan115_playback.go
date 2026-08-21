package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
)

const (
	pan115PlaybackLeaseTTL = 10 * time.Minute
	pan115CopyCleanupDelay = 5 * time.Second
	pan115CleanupBatchSize = 16
)

type pan115PlaybackCoordinator struct {
	db          *gorm.DB
	connections *ConnectionService
	log         zerolog.Logger
	now         func() time.Time

	startMu sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	rootMu  sync.Mutex
	roots   map[uint]string
}

func newPan115PlaybackCoordinator(db *gorm.DB, connections *ConnectionService, log zerolog.Logger, now func() time.Time) *pan115PlaybackCoordinator {
	return &pan115PlaybackCoordinator{db: db, connections: connections, log: log, now: now, roots: map[uint]string{}}
}

func (p *pan115PlaybackCoordinator) Start(parent context.Context) error {
	if p == nil || p.db == nil || p.connections == nil {
		return errors.New("115 playback coordinator dependencies are unavailable")
	}
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if p.cancel != nil {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel, p.done = cancel, make(chan struct{})
	go p.loop(ctx, p.done)
	return nil
}

func (p *pan115PlaybackCoordinator) Close() {
	if p == nil {
		return
	}
	p.startMu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.startMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (p *pan115PlaybackCoordinator) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	p.sweep(ctx)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sweep(ctx)
		}
	}
}

func playbackClientFingerprint(remoteAddr, userAgent string) string {
	host := strings.TrimSpace(remoteAddr)
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	if host == "" {
		host = "direct"
	}
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		userAgent = "unknown"
	}
	sum := sha256.Sum256([]byte(strings.ToLower(host) + "\x00" + userAgent))
	return hex.EncodeToString(sum[:])
}

func (p *pan115PlaybackCoordinator) Resolve(ctx context.Context, opaque string, target signedProxyTarget, userAgent, clientFingerprint string, driver cloudpkg.Driver) (cloudpkg.TemporaryURL, error) {
	if len(clientFingerprint) != sha256.Size*2 {
		return cloudpkg.TemporaryURL{}, appError(CodeProxyTargetUnavailable, "播放设备标识无效", nil)
	}
	source, err := driver.Stat(ctx, target.ProviderItemID)
	if err != nil || source.IsDir || strings.TrimSpace(source.PickCode) == "" {
		return cloudpkg.TemporaryURL{}, appError(CodeProxyTargetUnavailable, "播放目标不可用", err)
	}
	lease, err := p.acquireLease(opaque, clientFingerprint, target.ConnectionID, source.ID)
	if err != nil {
		return cloudpkg.TemporaryURL{}, err
	}
	item := source
	if lease.Role == models.Pan115PlaybackRoleSecondary {
		item, err = p.prepareCopy(ctx, driver, &lease, source)
		if err != nil {
			p.scheduleCleanup(lease.ID, true, "pan115_playback_copy_failed")
			serverlog.OperationPan115MultiDevicePlayback.Event(p.log.Warn()).Str("lease_id", lease.ID).Str("error_code", "pan115_playback_copy_failed").Msg(serverlog.OperationPan115MultiDevicePlayback.Message("临时副本准备失败"))
			return cloudpkg.TemporaryURL{}, appError(CodeProxyUpstreamUnavailable, "115 多设备播放暂时不可用", err)
		}
	}
	temporary, err := driver.DirectURL(ctx, cloudpkg.DirectURLRequest{FileID: item.ID, PickCode: item.PickCode, UserAgent: userAgent})
	if lease.Role == models.Pan115PlaybackRoleSecondary {
		p.scheduleCleanup(lease.ID, err != nil, "")
	}
	if err != nil {
		return cloudpkg.TemporaryURL{}, err
	}
	serverlog.OperationPan115MultiDevicePlayback.Event(p.log.Info()).Str("lease_id", lease.ID).Str("role", lease.Role).Msg(serverlog.OperationPan115MultiDevicePlayback.Message("已签发播放直链"))
	return temporary, nil
}

func (p *pan115PlaybackCoordinator) acquireLease(opaque, clientFingerprint string, connectionID uint, sourceID string) (models.Pan115PlaybackLease, error) {
	now := p.now()
	var lease models.Pan115PlaybackLease
	err := p.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Where("artifact_opaque_id = ? AND client_fingerprint = ?", opaque, clientFingerprint).First(&lease).Error
		if err == nil && lease.LeaseExpiresAt.After(now) {
			if lease.ConnectionID != connectionID || lease.SourceProviderItemID != sourceID {
				return appError(CodeProxyTargetUnavailable, "播放 lease 已失效", nil)
			}
			lease.LeaseExpiresAt = now.Add(pan115PlaybackLeaseTTL)
			if lease.Role == models.Pan115PlaybackRoleSecondary && lease.CopyDirectoryID == "" {
				lease.Status = models.Pan115PlaybackLeaseCopyPending
			}
			lease.UpdatedAt = now
			return tx.Model(&models.Pan115PlaybackLease{}).Where("id = ?", lease.ID).Updates(map[string]any{"lease_expires_at": lease.LeaseExpiresAt, "status": lease.Status, "updated_at": now}).Error
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if lease.CopyDirectoryID != "" {
				return appError(CodeProxyUpstreamUnavailable, "115 临时副本正在清理", nil)
			}
			if err := tx.Delete(&lease).Error; err != nil {
				return err
			}
		}
		var active int64
		if err := tx.Model(&models.Pan115PlaybackLease{}).Where("artifact_opaque_id = ? AND lease_expires_at > ?", opaque, now).Count(&active).Error; err != nil {
			return err
		}
		if active >= 2 {
			return appError(CodeProxyDeviceLimit, "115 同一媒体默认最多两台设备同时播放", nil)
		}
		role, status := models.Pan115PlaybackRolePrimary, models.Pan115PlaybackLeaseActive
		if active == 1 {
			role, status = models.Pan115PlaybackRoleSecondary, models.Pan115PlaybackLeaseCopyPending
		}
		lease = models.Pan115PlaybackLease{ID: uuid.NewString(), ConnectionID: connectionID, ArtifactOpaqueID: opaque, ClientFingerprint: clientFingerprint, Role: role, SourceProviderItemID: sourceID, Status: status, LeaseExpiresAt: now.Add(pan115PlaybackLeaseTTL), CreatedAt: now, UpdatedAt: now}
		return tx.Create(&lease).Error
	})
	if err != nil {
		if ErrorCode(err) == CodeProxyDeviceLimit {
			serverlog.OperationPan115MultiDevicePlayback.Event(p.log.Warn()).Str("error_code", CodeProxyDeviceLimit).Msg(serverlog.OperationPan115MultiDevicePlayback.Message("已拒绝超出双设备上限的播放请求"))
		}
		return models.Pan115PlaybackLease{}, err
	}
	return lease, nil
}

func (p *pan115PlaybackCoordinator) prepareCopy(ctx context.Context, driver cloudpkg.Driver, lease *models.Pan115PlaybackLease, source cloudpkg.Item) (cloudpkg.Item, error) {
	mutations, ok := driver.(cloudpkg.MutationDriver)
	if !ok || !mutations.Capabilities().CreateDirectory || !mutations.Capabilities().Copy || !mutations.Capabilities().Recycle {
		return cloudpkg.Item{}, errors.New("115 copy capability is unavailable")
	}
	if lease.CopyDirectoryID == "" {
		root, err := p.playbackRoot(ctx, lease.ConnectionID, driver, mutations)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		name := "lease-" + strings.ReplaceAll(lease.ID, "-", "")
		directory, err := ensurePan115NamedDirectory(ctx, driver, mutations, root, name)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		lease.CopyDirectoryID = directory.ID
		if err := p.db.Model(&models.Pan115PlaybackLease{}).Where("id = ? AND copy_directory_id = ''", lease.ID).Updates(map[string]any{"copy_directory_id": directory.ID, "status": models.Pan115PlaybackLeaseCopyPending, "updated_at": p.now()}).Error; err != nil {
			return cloudpkg.Item{}, err
		}
	}
	if lease.CopyItemID != "" {
		copyItem, err := driver.Stat(ctx, lease.CopyItemID)
		if err == nil && playbackCopyMatches(copyItem, source) {
			return copyItem, nil
		}
	}
	if candidate, count, err := findPan115PlaybackCopy(ctx, driver, lease.CopyDirectoryID, source); err != nil {
		return cloudpkg.Item{}, err
	} else if count == 1 {
		return p.persistCopyItem(lease, candidate)
	} else if count > 1 {
		return cloudpkg.Item{}, errors.New("115 playback copy is ambiguous")
	}
	copyErr := mutations.Copy(ctx, source.ID, lease.CopyDirectoryID)
	if candidate, count, reconcileErr := findPan115PlaybackCopy(ctx, driver, lease.CopyDirectoryID, source); reconcileErr == nil && count == 1 {
		return p.persistCopyItem(lease, candidate)
	} else if reconcileErr != nil {
		return cloudpkg.Item{}, reconcileErr
	} else if count > 1 {
		return cloudpkg.Item{}, errors.New("115 playback copy is ambiguous")
	}
	delays := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	for _, delay := range delays {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return cloudpkg.Item{}, ctx.Err()
		case <-timer.C:
		}
		candidate, count, err := findPan115PlaybackCopy(ctx, driver, lease.CopyDirectoryID, source)
		if err != nil {
			return cloudpkg.Item{}, err
		}
		if count == 1 {
			return p.persistCopyItem(lease, candidate)
		}
		if count > 1 {
			return cloudpkg.Item{}, errors.New("115 playback copy is ambiguous")
		}
	}
	if copyErr != nil {
		return cloudpkg.Item{}, copyErr
	}
	return cloudpkg.Item{}, errors.New("115 playback copy is not ready")
}

func (p *pan115PlaybackCoordinator) persistCopyItem(lease *models.Pan115PlaybackLease, item cloudpkg.Item) (cloudpkg.Item, error) {
	if item.IsDir || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.PickCode) == "" {
		return cloudpkg.Item{}, errors.New("115 playback copy has no download identity")
	}
	lease.CopyItemID = item.ID
	if err := p.db.Model(&models.Pan115PlaybackLease{}).Where("id = ? AND copy_directory_id = ?", lease.ID, lease.CopyDirectoryID).Updates(map[string]any{"copy_item_id": item.ID, "updated_at": p.now()}).Error; err != nil {
		return cloudpkg.Item{}, err
	}
	return item, nil
}

func playbackCopyMatches(candidate, source cloudpkg.Item) bool {
	if candidate.IsDir || candidate.Size != source.Size || candidate.Name != source.Name {
		return false
	}
	return source.SHA1 == "" || strings.EqualFold(candidate.SHA1, source.SHA1)
}

func findPan115PlaybackCopy(ctx context.Context, driver cloudpkg.Driver, directoryID string, source cloudpkg.Item) (cloudpkg.Item, int, error) {
	items, err := listCloudDirectory(ctx, driver, directoryID)
	if err != nil {
		return cloudpkg.Item{}, 0, err
	}
	var candidate cloudpkg.Item
	count := 0
	for _, item := range items {
		if playbackCopyMatches(item, source) {
			candidate, count = item, count+1
		}
	}
	return candidate, count, nil
}

func (p *pan115PlaybackCoordinator) playbackRoot(ctx context.Context, connectionID uint, driver cloudpkg.Driver, mutations cloudpkg.MutationDriver) (string, error) {
	p.rootMu.Lock()
	cached := p.roots[connectionID]
	p.rootMu.Unlock()
	if cached != "" {
		if item, err := driver.Stat(ctx, cached); err == nil && item.IsDir {
			return cached, nil
		}
	}
	root, err := ensurePan115NamedDirectory(ctx, driver, mutations, "0", "OhMyCine")
	if err != nil {
		return "", err
	}
	root, err = ensurePan115NamedDirectory(ctx, driver, mutations, root.ID, ".playback-copies")
	if err != nil {
		return "", err
	}
	p.rootMu.Lock()
	p.roots[connectionID] = root.ID
	p.rootMu.Unlock()
	return root.ID, nil
}

func ensurePan115NamedDirectory(ctx context.Context, driver cloudpkg.Driver, mutations cloudpkg.MutationDriver, parentID, name string) (cloudpkg.Item, error) {
	items, err := listCloudDirectory(ctx, driver, parentID)
	if err != nil {
		return cloudpkg.Item{}, err
	}
	matches := namedCloudItems(items, name)
	if len(matches) == 1 && matches[0].IsDir {
		return matches[0], nil
	}
	if len(matches) != 0 {
		return cloudpkg.Item{}, errors.New("115 playback directory is ambiguous")
	}
	created, createErr := mutations.CreateDirectory(ctx, parentID, name)
	if createErr == nil && created.IsDir && created.ID != "" {
		return created, nil
	}
	items, err = listCloudDirectory(ctx, driver, parentID)
	if err == nil {
		matches = namedCloudItems(items, name)
		if len(matches) == 1 && matches[0].IsDir {
			return matches[0], nil
		}
	}
	if createErr != nil {
		return cloudpkg.Item{}, createErr
	}
	return cloudpkg.Item{}, errors.New("115 playback directory creation is ambiguous")
}

func (p *pan115PlaybackCoordinator) scheduleCleanup(leaseID string, immediate bool, errorCode string) {
	now := p.now()
	cleanupAt := now.Add(pan115CopyCleanupDelay)
	if immediate {
		cleanupAt = now
	}
	updates := map[string]any{"status": models.Pan115PlaybackLeaseCleanupPending, "cleanup_after": cleanupAt, "next_retry_at": nil, "updated_at": now}
	if errorCode != "" {
		updates["last_error_code"] = errorCode
	}
	_ = p.db.Model(&models.Pan115PlaybackLease{}).Where("id = ? AND copy_directory_id <> ''", leaseID).Updates(updates).Error
}

func (p *pan115PlaybackCoordinator) sweep(ctx context.Context) {
	now := p.now()
	var leases []models.Pan115PlaybackLease
	if err := p.db.Where("copy_directory_id <> '' AND status IN ? AND (cleanup_after IS NULL OR cleanup_after <= ?) AND (next_retry_at IS NULL OR next_retry_at <= ?)", []string{models.Pan115PlaybackLeaseCopyPending, models.Pan115PlaybackLeaseCleanupPending, models.Pan115PlaybackLeaseCleanupFailed}, now, now).Order("updated_at,id").Limit(pan115CleanupBatchSize).Find(&leases).Error; err != nil {
		serverlog.OperationPan115PlaybackCleanup.Event(p.log.Error()).Str("error_code", "pan115_playback_cleanup_query_failed").Msg(serverlog.OperationPan115PlaybackCleanup.Message("待清理副本查询失败"))
		return
	}
	for _, lease := range leases {
		if ctx.Err() != nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := p.cleanupLease(cleanupCtx, lease)
		cancel()
		if err != nil {
			p.recordCleanupFailure(lease, err)
		}
	}
	if now.Unix()%60 < 5 {
		_ = p.db.Where("lease_expires_at <= ? AND copy_directory_id = ''", now.Add(-time.Hour)).Delete(&models.Pan115PlaybackLease{}).Error
	}
}

func (p *pan115PlaybackCoordinator) cleanupLease(ctx context.Context, lease models.Pan115PlaybackLease) error {
	_, driver, err := p.connections.driver(lease.ConnectionID)
	if err != nil {
		return err
	}
	mutations, mutationOK := driver.(cloudpkg.MutationDriver)
	purger, purgeOK := driver.(cloudpkg.ExactRecyclePurger)
	if !mutationOK || !purgeOK || !mutations.Capabilities().Recycle {
		return errors.New("115 exact recycle cleanup capability is unavailable")
	}
	if item, statErr := driver.Stat(ctx, lease.CopyDirectoryID); statErr == nil {
		if !item.IsDir {
			return errors.New("115 playback cleanup target changed")
		}
		if err := mutations.Recycle(ctx, lease.CopyDirectoryID); err != nil {
			if _, retryErr := driver.Stat(ctx, lease.CopyDirectoryID); retryErr == nil {
				return err
			} else if code, _ := cloudpkg.ErrorInfo(retryErr); code != cloudpkg.CodeNotFound {
				return err
			}
		}
	} else if code, _ := cloudpkg.ErrorInfo(statErr); code != cloudpkg.CodeNotFound {
		return statErr
	}
	if err := purger.PurgeRecycle(ctx, lease.CopyDirectoryID); err != nil {
		return err
	}
	now := p.now()
	result := p.db.Model(&models.Pan115PlaybackLease{}).Where("id = ? AND copy_directory_id = ?", lease.ID, lease.CopyDirectoryID).Updates(map[string]any{"copy_directory_id": "", "copy_item_id": "", "status": models.Pan115PlaybackLeaseCompleted, "cleanup_after": nil, "next_retry_at": nil, "last_error_code": "", "cleaned_at": now, "updated_at": now})
	if result.Error != nil || result.RowsAffected != 1 {
		return errors.New("115 playback cleanup state changed")
	}
	serverlog.OperationPan115PlaybackCleanup.Event(p.log.Info()).Str("lease_id", lease.ID).Msg(serverlog.OperationPan115PlaybackCleanup.Message("临时副本已精确清理"))
	return nil
}

func (p *pan115PlaybackCoordinator) recordCleanupFailure(lease models.Pan115PlaybackLease, err error) {
	retry := lease.RetryCount + 1
	delay := time.Minute * time.Duration(1<<min(retry-1, 6))
	next := p.now().Add(delay)
	code := "pan115_playback_cleanup_failed"
	if providerCode, _ := cloudpkg.ErrorInfo(err); providerCode != cloudpkg.CodeUnavailable {
		code = providerCode
	}
	_ = p.db.Model(&models.Pan115PlaybackLease{}).Where("id = ? AND copy_directory_id = ?", lease.ID, lease.CopyDirectoryID).Updates(map[string]any{"status": models.Pan115PlaybackLeaseCleanupFailed, "retry_count": retry, "next_retry_at": next, "last_error_code": safeLabel(code, 96), "updated_at": p.now()}).Error
	serverlog.OperationPan115PlaybackCleanup.Event(p.log.Warn()).Str("lease_id", lease.ID).Str("error_code", safeLabel(code, 96)).Time("retry_at", next).Msg(serverlog.OperationPan115PlaybackCleanup.Message("临时副本清理失败，已安排重试"))
}
