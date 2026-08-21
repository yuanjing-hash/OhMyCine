package services

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

type monitorCloudDriver struct {
	fakeCloudDriver
	mu     sync.Mutex
	page   cloud.ChangePage
	err    error
	calls  int
	called chan struct{}
}

func (d *monitorCloudDriver) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{NetworkDrive: true, DirectoryList: true, Watch: true, ChangeCursor: true}
}

func (d *monitorCloudDriver) Changes(_ context.Context, _ cloud.ChangeCursor, _ int) (cloud.ChangePage, error) {
	d.mu.Lock()
	d.calls++
	page, err := d.page, d.err
	d.mu.Unlock()
	if d.called != nil {
		select {
		case d.called <- struct{}{}:
		default:
		}
	}
	return page, err
}

func (d *monitorCloudDriver) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

type monitorEventNotifier struct{ called chan struct{} }

func (n *monitorEventNotifier) ProviderEventsChanged(context.Context, uint, []models.ProviderEvent) error {
	select {
	case n.called <- struct{}{}:
	default:
	}
	return nil
}

func TestProviderEventMonitorRunsPerConnectionOutsideJobQueueAndStopsDisabled(t *testing.T) {
	db, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	connection, err := connections.Create(actor, ConnectionInput{Name: "event monitor", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	if err := db.Create(&models.ProviderCursor{ConnectionID: connection.ID, Stream: providerLifeStream, CursorTime: timestamp, CursorID: "1", UpdatedAt: timestamp}).Error; err != nil {
		t.Fatal(err)
	}
	driver := &monitorCloudDriver{page: cloud.ChangePage{Events: []cloud.ChangeEvent{{ID: "2", Time: timestamp.Add(time.Second), Kind: cloud.ChangeCreated, ItemID: "file", ParentID: "root", Name: "Movie.mkv"}}, NextCursor: cloud.ChangeCursor{Time: timestamp.Add(time.Second), ID: "2"}}, called: make(chan struct{}, 1)}
	connections.mu.Lock()
	connections.drivers[connection.ID] = driver
	connections.mu.Unlock()
	notifier := &monitorEventNotifier{called: make(chan struct{}, 1)}
	monitor := NewProviderEventMonitor(db, connections, NewProviderEventService(db, notifier), zerolog.Nop())
	monitor.refreshInterval = 20 * time.Millisecond
	monitor.pollInterval = 20 * time.Millisecond
	monitor.maxPages = 1
	if err := monitor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifier.called:
	case <-time.After(2 * time.Second):
		monitor.Close()
		t.Fatal("provider event was not delivered")
	}
	var event models.ProviderEvent
	deadline := time.Now().Add(2 * time.Second)
	var readErr error
	for time.Now().Before(deadline) {
		readErr = db.First(&event, "connection_id = ?", connection.ID).Error
		if readErr == nil && event.ProcessedAt != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if readErr != nil || event.ProcessedAt == nil {
		monitor.Close()
		t.Fatalf("event=%+v err=%v", event, readErr)
	}
	var jobs int64
	if err := db.Model(&models.Job{}).Count(&jobs).Error; err != nil || jobs != 0 {
		monitor.Close()
		t.Fatalf("jobs=%d err=%v", jobs, err)
	}
	if err := db.Model(&models.Connection{}).Where("id = ?", connection.ID).Update("enabled", false).Error; err != nil {
		monitor.Close()
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	calls := driver.callCount()
	time.Sleep(80 * time.Millisecond)
	if after := driver.callCount(); after != calls {
		monitor.Close()
		t.Fatalf("disabled connection kept polling: before=%d after=%d", calls, after)
	}
	monitor.Close()
}

func TestProviderEventMonitorIsolatesConnectionFailuresAndCloses(t *testing.T) {
	db, _, connections, actor := newConnectionTestService(t, &fakeCloudDriver{})
	bad, err := connections.Create(actor, ConnectionInput{Name: "bad event account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	good, err := connections.Create(actor, ConnectionInput{Name: "good event account", Provider: cloud.ProviderPan115, Cookie: testPan115Cookie, Enabled: true}, RequestContext{})
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC().Truncate(time.Second)
	for _, id := range []uint{bad.ID, good.ID} {
		if err := db.Create(&models.ProviderCursor{ConnectionID: id, Stream: providerLifeStream, CursorTime: timestamp, CursorID: "1", UpdatedAt: timestamp}).Error; err != nil {
			t.Fatal(err)
		}
	}
	badDriver := &monitorCloudDriver{err: errors.New("HTTP 429"), called: make(chan struct{}, 1)}
	goodDriver := &monitorCloudDriver{page: cloud.ChangePage{Events: []cloud.ChangeEvent{{ID: "2", Time: timestamp.Add(time.Second), Kind: cloud.ChangeDeleted, ItemID: "file"}}, NextCursor: cloud.ChangeCursor{Time: timestamp.Add(time.Second), ID: "2"}}}
	connections.mu.Lock()
	connections.drivers[bad.ID] = badDriver
	connections.drivers[good.ID] = goodDriver
	connections.mu.Unlock()
	notifier := &monitorEventNotifier{called: make(chan struct{}, 1)}
	monitor := NewProviderEventMonitor(db, connections, NewProviderEventService(db, notifier), zerolog.Nop())
	monitor.refreshInterval = time.Second
	monitor.pollInterval = time.Second
	if err := monitor.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-notifier.called:
	case <-time.After(2 * time.Second):
		monitor.Close()
		t.Fatal("healthy connection was blocked by another connection failure")
	}
	closed := make(chan struct{})
	go func() {
		monitor.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("provider event monitor did not stop")
	}
}
