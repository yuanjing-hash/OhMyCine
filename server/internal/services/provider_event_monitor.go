package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/rs/zerolog"
	serverlog "github.com/yuanjing-hash/ohmycine/server/internal/logging"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"gorm.io/gorm"
)

const (
	providerEventConnectionRefresh = 30 * time.Second
	providerEventPollInterval      = 15 * time.Second
	providerEventMaxPagesPerPoll   = 5
)

type providerEventWorker struct {
	cancel context.CancelFunc
}

// ProviderEventMonitor owns one lightweight polling loop per enabled provider
// connection. These loops are independent from the persistent Job scheduler.
type ProviderEventMonitor struct {
	db          *gorm.DB
	connections *ConnectionService
	events      *ProviderEventService
	log         zerolog.Logger

	refreshInterval time.Duration
	pollInterval    time.Duration
	maxPages        int

	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	workers map[uint]providerEventWorker
	wg      sync.WaitGroup
	started bool
	closed  bool
}

func NewProviderEventMonitor(db *gorm.DB, connections *ConnectionService, events *ProviderEventService, log zerolog.Logger) *ProviderEventMonitor {
	return &ProviderEventMonitor{db: db, connections: connections, events: events, log: log, refreshInterval: providerEventConnectionRefresh, pollInterval: providerEventPollInterval, maxPages: providerEventMaxPagesPerPoll, workers: map[uint]providerEventWorker{}}
}

func (m *ProviderEventMonitor) Start(ctx context.Context) error {
	if m == nil || m.db == nil || m.connections == nil || m.events == nil {
		return errors.New("provider event monitor is not configured")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("provider event monitor is closed")
	}
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	m.mu.Unlock()
	if err := m.reconcile(); err != nil {
		serverlog.OperationProviderLifeEvent.Event(m.log.Error()).Str("error_code", "provider_event_connection_refresh_failed").Msg(serverlog.OperationProviderLifeEvent.Message("监听器启动失败"))
		m.cancel()
		m.mu.Lock()
		m.started = false
		m.mu.Unlock()
		return err
	}
	serverlog.OperationProviderLifeEvent.Event(m.log.Info()).Msg(serverlog.OperationProviderLifeEvent.Message("监听器已启动"))
	m.wg.Add(1)
	go m.runCoordinator()
	return nil
}

func (m *ProviderEventMonitor) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
	serverlog.OperationProviderLifeEvent.Event(m.log.Info()).Msg(serverlog.OperationProviderLifeEvent.Message("监听器已停止"))
}

func (m *ProviderEventMonitor) runCoordinator() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.reconcile(); err != nil {
				serverlog.OperationProviderLifeEvent.Event(m.log.Error()).Uint("connection_id", 0).Str("error_code", "provider_event_connection_refresh_failed").Int("event_count", 0).Int64("duration_ms", 0).Msg(serverlog.OperationProviderLifeEvent.Message("连接列表刷新失败"))
			}
		}
	}
}

func (m *ProviderEventMonitor) reconcile() error {
	var records []models.Connection
	if err := m.db.WithContext(m.ctx).Select("id").Where("provider = ? AND enabled = ?", cloudpkg.ProviderPan115, true).Find(&records).Error; err != nil {
		return err
	}
	desired := make(map[uint]struct{}, len(records))
	for _, record := range records {
		desired[record.ID] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil
	}
	for id, worker := range m.workers {
		if _, ok := desired[id]; !ok {
			worker.cancel()
			delete(m.workers, id)
			serverlog.OperationProviderLifeEvent.Event(m.log.Info()).Uint("connection_id", id).Msg(serverlog.OperationProviderLifeEvent.Message("连接监听已停止"))
		}
	}
	for id := range desired {
		if _, exists := m.workers[id]; exists {
			continue
		}
		workerCtx, cancel := context.WithCancel(m.ctx)
		m.workers[id] = providerEventWorker{cancel: cancel}
		m.wg.Add(1)
		go m.runConnection(workerCtx, id)
		serverlog.OperationProviderLifeEvent.Event(m.log.Info()).Uint("connection_id", id).Msg(serverlog.OperationProviderLifeEvent.Message("连接监听已启动"))
	}
	return nil
}

func (m *ProviderEventMonitor) runConnection(ctx context.Context, connectionID uint) {
	defer m.wg.Done()
	m.poll(ctx, connectionID)
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx, connectionID)
		}
	}
}

func (m *ProviderEventMonitor) poll(ctx context.Context, connectionID uint) {
	started := time.Now()
	inserted, processed := 0, 0
	errorCode := ""
	_, driver, err := m.connections.driver(connectionID)
	if err == nil {
		source, ok := driver.(cloudpkg.ChangeSource)
		if !ok || !driver.Capabilities().ChangeCursor {
			err = errors.New("provider change cursor is unavailable")
			errorCode = "provider_event_unsupported"
		} else {
			for page := 0; page < m.maxPages; page++ {
				var count int
				var more bool
				count, more, err = m.events.IngestOnce(ctx, connectionID, source)
				inserted += count
				if err != nil || !more {
					break
				}
			}
		}
	}
	if err != nil && errorCode == "" {
		errorCode, _ = cloudpkg.ErrorInfo(err)
	}
	if count, processErr := m.events.ProcessPending(ctx, connectionID); processErr != nil {
		if err == nil {
			err = processErr
			errorCode = "provider_event_notification_failed"
		}
	} else {
		processed = count
	}
	fields := m.log.With().Uint("connection_id", connectionID).Str("error_code", errorCode).Int("received_events", inserted).Int("processed_events", processed).Int("event_count", inserted+processed).Int64("duration_ms", time.Since(started).Milliseconds()).Logger()
	if err != nil {
		serverlog.OperationProviderLifeEvent.Event(fields.Error()).Msg(serverlog.OperationProviderLifeEvent.Message("拉取或分发失败，将按周期重试"))
	} else if inserted+processed > 0 {
		serverlog.OperationProviderLifeEvent.Event(fields.Info()).Msg(serverlog.OperationProviderLifeEvent.Message("增量事件已拉取并分发"))
	} else {
		serverlog.OperationProviderLifeEvent.Event(fields.Debug()).Msg(serverlog.OperationProviderLifeEvent.Message("本轮没有新事件"))
	}
}
