package downloader

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

var FakeCapabilities = Capabilities{Pause: true, Resume: true, Cancel: true, DeleteData: true, DownloadSpeed: true, UploadSpeed: true, ETA: true, OutputConstraint: OutputConstraintNone}

type FakeClient struct {
	mu    sync.Mutex
	tasks map[string]*fakeTask
	now   func() time.Time
}

type fakeTask struct {
	id        string
	name      string
	startedAt time.Time
	elapsed   time.Duration
	pausedAt  *time.Time
	cancelled bool
}

func NewFakeClient() *FakeClient {
	return &FakeClient{tasks: map[string]*fakeTask{}, now: time.Now}
}

func (c *FakeClient) Test(context.Context) (Health, error) { return Health{Version: "fake-v1"}, nil }

func (c *FakeClient) Submit(_ context.Context, request SubmitRequest) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := uuid.NewString()
	name := request.Source.Filename
	if name == "" {
		name = "Fake download"
	}
	c.tasks[id] = &fakeTask{id: id, name: name, startedAt: c.now()}
	return c.task(c.tasks[id]), nil
}

func (c *FakeClient) Get(_ context.Context, id string) (Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok {
		return Task{}, Error("downloader_task_not_found", false, nil)
	}
	return c.task(task), nil
}

func (c *FakeClient) Pause(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok {
		return Error("downloader_task_not_found", false, nil)
	}
	if task.pausedAt == nil {
		now := c.now()
		task.elapsed += now.Sub(task.startedAt)
		task.pausedAt = &now
	}
	return nil
}

func (c *FakeClient) Resume(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok {
		return Error("downloader_task_not_found", false, nil)
	}
	if task.pausedAt != nil {
		task.startedAt = c.now()
		task.pausedAt = nil
	}
	return nil
}

func (c *FakeClient) Cancel(_ context.Context, id string, _ bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	task, ok := c.tasks[id]
	if !ok {
		return Error("downloader_task_not_found", false, nil)
	}
	task.cancelled = true
	return nil
}

func (c *FakeClient) task(task *fakeTask) Task {
	if task.cancelled {
		return Task{ID: task.id, Name: task.name, Status: "cancelled", Failed: true, ErrorCode: "download_cancelled"}
	}
	elapsed := task.elapsed
	if task.pausedAt == nil {
		elapsed += c.now().Sub(task.startedAt)
	}
	progressValue := float64(elapsed) / float64(8*time.Second) * 100
	if progressValue > 100 {
		progressValue = 100
	}
	total := int64(128 * 1024 * 1024)
	completed := int64(float64(total) * progressValue / 100)
	downloadSpeed := int64(16 * 1024 * 1024)
	uploadSpeed := int64(256 * 1024)
	eta := int64((100 - progressValue) / 12.5)
	status := "downloading"
	if task.pausedAt != nil {
		status, downloadSpeed, uploadSpeed = "paused", 0, 0
	}
	completedFlag := progressValue >= 100
	if completedFlag {
		status, downloadSpeed, eta = "completed", 0, 0
	}
	return Task{ID: task.id, Name: task.name, Status: status, Progress: &progressValue, BytesCompleted: &completed, BytesTotal: &total, DownloadSpeed: &downloadSpeed, UploadSpeed: &uploadSpeed, ETASeconds: &eta, Completed: completedFlag}
}
