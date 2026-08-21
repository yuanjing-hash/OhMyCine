package pan115offline

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

var Capabilities = downloader.Capabilities{Cancel: true, DeleteData: true, DownloadSpeed: false, UploadSpeed: false, ETA: true, NativeOffline: true, ShareReceive: true, OutputConstraint: downloader.OutputConstraintProviderStorage}

const (
	maxStorageParentDepth       = 128
	maxProviderDirectoryEntries = 5000
)

type Client struct {
	driver      cloud.NativeOfflineDriver
	rootID      string
	directoryID string
}

func New(config downloader.Config) (downloader.Client, error) {
	if config.CloudDriver == nil || strings.TrimSpace(config.ProviderStorageRootID) == "" || strings.TrimSpace(config.ProviderDirectoryID) == "" {
		return nil, errors.New("115 offline downloader requires a provider storage")
	}
	return &Client{driver: config.CloudDriver, rootID: strings.TrimSpace(config.ProviderStorageRootID), directoryID: strings.TrimSpace(config.ProviderDirectoryID)}, nil
}

func (c *Client) Test(ctx context.Context) (downloader.Health, error) {
	if _, err := c.driver.Probe(ctx); err != nil {
		return downloader.Health{}, mapError(err)
	}
	if _, err := c.validateTarget(ctx); err != nil {
		return downloader.Health{}, err
	}
	return downloader.Health{Version: "115 原生离线"}, nil
}

func (c *Client) Submit(ctx context.Context, request downloader.SubmitRequest) (downloader.Task, error) {
	if request.MetadataOnly {
		return downloader.Task{}, downloader.Error("downloader_source_unsupported", false, nil)
	}
	if request.Source.Kind == downloader.SourcePan115Share {
		return c.submitShare(ctx, request)
	}
	if request.Source.Kind == downloader.SourceProviderItem {
		return c.adoptProviderItem(ctx, request)
	}
	if request.Source.Kind != downloader.SourceURL || strings.TrimSpace(request.Source.URL) == "" {
		return downloader.Task{}, downloader.Error("downloader_source_unsupported", false, nil)
	}
	parsed, err := url.Parse(strings.TrimSpace(request.Source.URL))
	if err != nil || (parsed.Scheme != "magnet" && parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ed2k") {
		return downloader.Task{}, downloader.Error("downloader_source_invalid", false, err)
	}
	if _, err := c.validateTarget(ctx); err != nil {
		return downloader.Task{}, err
	}
	task, err := c.driver.SubmitOffline(ctx, request.Source.URL, c.directoryID)
	if err != nil {
		return downloader.Task{}, mapError(err)
	}
	return mapTask(task), nil
}

const (
	shareTaskPrefix  = "share:"
	ingestTaskPrefix = "ingest:"
)

func (c *Client) submitShare(ctx context.Context, request downloader.SubmitRequest) (downloader.Task, error) {
	share, ok := c.driver.(cloud.ShareReceiveDriver)
	if !ok || !c.driver.Capabilities().ShareReceive {
		return downloader.Task{}, downloader.Error("downloader_source_unsupported", false, nil)
	}
	mutations, ok := c.driver.(cloud.MutationDriver)
	if !ok || !mutations.Capabilities().CreateDirectory {
		return downloader.Task{}, downloader.Error("downloader_unavailable", false, nil)
	}
	directoryID := strings.TrimSpace(request.ProviderDirectoryID)
	if directoryID == "" {
		directoryID = c.directoryID
	}
	if _, err := c.validateDirectory(ctx, directoryID); err != nil {
		return downloader.Task{}, err
	}
	tag := strings.TrimSpace(request.Tag)
	if !strings.HasPrefix(tag, "omc-") || len(tag) > 64 || strings.ContainsAny(tag, "/\\\x00\r\n") {
		return downloader.Task{}, downloader.Error("downloader_source_invalid", false, errors.New("share task tag is invalid"))
	}
	taskRoot, err := c.ensureTaskDirectory(ctx, mutations, directoryID, tag)
	if err != nil {
		return downloader.Task{}, err
	}
	hasOutput, err := c.directoryHasChildren(ctx, taskRoot.ID)
	if err != nil {
		return downloader.Task{}, err
	}
	if !hasOutput {
		snapshot, inspectErr := share.InspectShare(ctx, request.Source.URL)
		if inspectErr != nil {
			return downloader.Task{}, mapError(inspectErr)
		}
		receiveErr := share.ReceiveShare(ctx, snapshot, taskRoot.ID)
		if receiveErr != nil {
			// A lost/ambiguous response may still have committed provider-side.
			// Reconcile the stable task root before deciding to retry.
			if output, checkErr := c.waitForTaskOutput(ctx, taskRoot.ID); checkErr == nil && output {
				return completedDirectoryTask(shareTaskPrefix, taskRoot), nil
			}
			return downloader.Task{}, mapError(receiveErr)
		}
		if output, waitErr := c.waitForTaskOutput(ctx, taskRoot.ID); waitErr != nil {
			return downloader.Task{}, waitErr
		} else if !output {
			return downloader.Task{}, downloader.Error("downloader_share_result_unknown", true, nil)
		}
	}
	return completedDirectoryTask(shareTaskPrefix, taskRoot), nil
}

func (c *Client) adoptProviderItem(ctx context.Context, request downloader.SubmitRequest) (downloader.Task, error) {
	directoryID := strings.TrimSpace(request.ProviderDirectoryID)
	itemID := strings.TrimSpace(request.Source.ProviderItemID)
	if directoryID == "" || itemID == "" {
		return downloader.Task{}, downloader.Error("downloader_source_invalid", false, nil)
	}
	if _, err := c.validateDirectory(ctx, directoryID); err != nil {
		return downloader.Task{}, err
	}
	item, err := c.itemWithinDirectory(ctx, itemID, directoryID)
	if err != nil {
		return downloader.Task{}, err
	}
	if item.ID == directoryID {
		return downloader.Task{}, downloader.Error("downloader_source_invalid", false, errors.New("cannot adopt intake root"))
	}
	return completedDirectoryTask(ingestTaskPrefix, item), nil
}

func completedDirectoryTask(prefix string, item cloud.Item) downloader.Task {
	progress := 1.0
	return downloader.Task{ID: prefix + item.ID, Name: item.Name, Status: "completed", Progress: &progress, Completed: true, OutputItemID: item.ID}
}

func (c *Client) ensureTaskDirectory(ctx context.Context, mutations cloud.MutationDriver, parentID, name string) (cloud.Item, error) {
	found, exists, err := c.findTaskDirectory(ctx, parentID, name)
	if err != nil {
		return cloud.Item{}, err
	}
	if exists {
		return found, nil
	}
	item, err := mutations.CreateDirectory(ctx, parentID, name)
	if err != nil {
		// A create response can be lost after the directory was committed.
		recovered, recoveredExists, listErr := c.findTaskDirectory(ctx, parentID, name)
		if listErr == nil && recoveredExists {
			return recovered, nil
		}
		return cloud.Item{}, mapError(err)
	}
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != parentID || !item.IsDir || item.Name != name {
		return cloud.Item{}, downloader.Error("downloader_response_invalid", false, errors.New("provider returned an invalid share task directory"))
	}
	return item, nil
}

func (c *Client) findTaskDirectory(ctx context.Context, parentID, name string) (cloud.Item, bool, error) {
	var found *cloud.Item
	seen := 0
	for offset := int64(0); ; {
		page, err := c.driver.List(ctx, parentID, cloud.PageRequest{Offset: offset, Limit: 200})
		if err != nil {
			return cloud.Item{}, false, mapError(err)
		}
		if len(page.Items) == 0 && page.HasMore {
			return cloud.Item{}, false, downloader.Error("downloader_response_invalid", true, errors.New("share task directory pagination made no progress"))
		}
		for i := range page.Items {
			item := page.Items[i]
			seen++
			if seen > maxProviderDirectoryEntries || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != parentID || !validProviderItemName(item.Name) {
				return cloud.Item{}, false, downloader.Error("downloader_response_invalid", false, errors.New("provider returned an invalid share task directory listing"))
			}
			if item.Name != name {
				continue
			}
			if !item.IsDir || found != nil {
				return cloud.Item{}, false, downloader.Error("downloader_response_invalid", false, errors.New("share task directory is ambiguous"))
			}
			copy := item
			found = &copy
		}
		if !page.HasMore {
			break
		}
		offset += int64(len(page.Items))
	}
	if found == nil {
		return cloud.Item{}, false, nil
	}
	return *found, true, nil
}

func (c *Client) directoryHasChildren(ctx context.Context, directoryID string) (bool, error) {
	page, err := c.driver.List(ctx, directoryID, cloud.PageRequest{Limit: 1})
	if err != nil {
		return false, mapError(err)
	}
	for _, item := range page.Items {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != directoryID || !validProviderItemName(item.Name) {
			return false, downloader.Error("downloader_response_invalid", false, errors.New("provider returned an invalid share task output"))
		}
	}
	return len(page.Items) > 0, nil
}

func (c *Client) waitForTaskOutput(ctx context.Context, directoryID string) (bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		hasOutput, err := c.directoryHasChildren(ctx, directoryID)
		if err != nil || hasOutput {
			return hasOutput, err
		}
		if attempt < 2 {
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return false, downloader.Error("downloader_unavailable", true, ctx.Err())
			case <-timer.C:
			}
		}
	}
	return false, nil
}

func (c *Client) validateTarget(ctx context.Context) (cloud.Item, error) {
	return c.validateDirectory(ctx, c.directoryID)
}

func (c *Client) validateDirectory(ctx context.Context, directoryID string) (cloud.Item, error) {
	current := strings.TrimSpace(directoryID)
	visited := make(map[string]struct{}, maxStorageParentDepth)
	for depth := 0; depth < maxStorageParentDepth; depth++ {
		item, err := c.driver.Stat(ctx, current)
		if err != nil {
			code, retryable := cloud.ErrorInfo(err)
			if code == cloud.CodeAuthExpired || code == cloud.CodeRateLimited {
				return cloud.Item{}, mapError(err)
			}
			return cloud.Item{}, downloader.Error("downloader_storage_unavailable", retryable, err)
		}
		if !item.IsDir || strings.TrimSpace(item.ID) != current {
			return cloud.Item{}, downloader.Error("downloader_storage_unavailable", false, errors.New("115 offline target is not a directory"))
		}
		if current == c.rootID {
			return item, nil
		}
		if _, exists := visited[current]; exists {
			return cloud.Item{}, downloader.Error("downloader_storage_unavailable", false, errors.New("115 offline target parent cycle"))
		}
		visited[current] = struct{}{}
		current = strings.TrimSpace(item.ParentID)
		if current == "" || (current == "0" && c.rootID != "0") {
			return cloud.Item{}, downloader.Error("downloader_storage_unavailable", false, errors.New("115 offline target is outside storage root"))
		}
	}
	return cloud.Item{}, downloader.Error("downloader_storage_unavailable", false, errors.New("115 offline target exceeds parent depth"))
}

func (c *Client) itemWithinDirectory(ctx context.Context, itemID, directoryID string) (cloud.Item, error) {
	current := strings.TrimSpace(itemID)
	visited := make(map[string]struct{}, maxStorageParentDepth)
	var initial cloud.Item
	for depth := 0; depth < maxStorageParentDepth; depth++ {
		item, err := c.driver.Stat(ctx, current)
		if err != nil {
			return cloud.Item{}, mapError(err)
		}
		if strings.TrimSpace(item.ID) != current || !validProviderItemName(item.Name) {
			return cloud.Item{}, downloader.Error("downloader_response_invalid", false, errors.New("provider returned a mismatched intake item identity"))
		}
		if depth == 0 {
			initial = item
		}
		if item.ParentID == directoryID {
			return initial, nil
		}
		if current == directoryID {
			return initial, nil
		}
		if _, exists := visited[current]; exists {
			break
		}
		visited[current] = struct{}{}
		current = strings.TrimSpace(item.ParentID)
		if current == "" || current == "0" {
			break
		}
	}
	return cloud.Item{}, downloader.Error("downloader_storage_unavailable", false, errors.New("provider item is outside intake root"))
}

func (c *Client) Get(ctx context.Context, id string) (downloader.Task, error) {
	if prefix, itemID, ok := providerDirectoryTask(id); ok {
		item, err := c.driver.Stat(ctx, itemID)
		if err != nil {
			return downloader.Task{}, mapError(err)
		}
		if strings.TrimSpace(item.ID) != itemID || !validProviderItemName(item.Name) {
			return downloader.Task{}, downloader.Error("downloader_response_invalid", false, errors.New("provider returned a mismatched task identity"))
		}
		return completedDirectoryTask(prefix, item), nil
	}
	task, err := c.driver.GetOffline(ctx, id)
	if err != nil {
		return downloader.Task{}, mapError(err)
	}
	return mapTask(task), nil
}

func (c *Client) Manifest(ctx context.Context, id string) (downloader.Manifest, error) {
	if _, itemID, ok := providerDirectoryTask(id); ok {
		return c.manifestFromItem(ctx, itemID)
	}
	task, err := c.driver.GetOffline(ctx, id)
	if err != nil {
		return downloader.Manifest{}, mapError(err)
	}
	if !task.Completed || strings.TrimSpace(task.OutputItemID) == "" {
		return downloader.Manifest{Complete: false}, nil
	}
	return c.manifestFromItem(ctx, task.OutputItemID)
}

func (c *Client) manifestFromItem(ctx context.Context, itemID string) (downloader.Manifest, error) {
	root, err := c.driver.Stat(ctx, itemID)
	if err != nil {
		return downloader.Manifest{}, mapError(err)
	}
	if strings.TrimSpace(root.ID) != strings.TrimSpace(itemID) || !validProviderItemName(root.Name) {
		return downloader.Manifest{}, downloader.Error("downloader_response_invalid", false, errors.New("provider returned a mismatched manifest root"))
	}
	manifest := downloader.Manifest{Name: root.Name, Complete: true}
	if !root.IsDir {
		manifest.Files = []downloader.File{{RelativePath: root.Name, Size: root.Size, ProviderItemID: root.ID, ProviderParentID: root.ParentID, SHA1: root.SHA1}}
		return manifest, nil
	}
	type pendingDirectory struct{ id, prefix string }
	pending := []pendingDirectory{{id: root.ID, prefix: ""}}
	visitedDirectories := map[string]struct{}{}
	seenItems := map[string]struct{}{root.ID: {}}
	entryCount := 0
	for len(pending) > 0 {
		directory := pending[0]
		pending = pending[1:]
		if _, exists := visitedDirectories[directory.id]; exists {
			return downloader.Manifest{}, downloader.Error("downloader_response_invalid", false, errors.New("provider manifest contains a directory cycle"))
		}
		visitedDirectories[directory.id] = struct{}{}
		for offset := int64(0); ; {
			page, listErr := c.driver.List(ctx, directory.id, cloud.PageRequest{Offset: offset, Limit: 200})
			if listErr != nil {
				return downloader.Manifest{}, mapError(listErr)
			}
			if len(page.Items) == 0 && page.HasMore {
				return downloader.Manifest{}, downloader.Error("downloader_response_invalid", true, errors.New("provider manifest pagination made no progress"))
			}
			for _, item := range page.Items {
				entryCount++
				if entryCount > maxProviderDirectoryEntries {
					return downloader.Manifest{}, downloader.Error("downloader_manifest_too_large", false, errors.New("provider manifest exceeded its bounded size"))
				}
				if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.ParentID) != directory.id || !validProviderItemName(item.Name) {
					return downloader.Manifest{}, downloader.Error("downloader_response_invalid", false, errors.New("provider manifest violated its trusted shape"))
				}
				if _, exists := seenItems[item.ID]; exists {
					return downloader.Manifest{}, downloader.Error("downloader_response_invalid", false, errors.New("provider manifest contains a duplicate item identity"))
				}
				seenItems[item.ID] = struct{}{}
				relative := item.Name
				if directory.prefix != "" {
					relative = directory.prefix + "/" + item.Name
				}
				if item.IsDir {
					pending = append(pending, pendingDirectory{id: item.ID, prefix: relative})
				} else {
					manifest.Files = append(manifest.Files, downloader.File{RelativePath: relative, Size: item.Size, ProviderItemID: item.ID, ProviderParentID: item.ParentID, SHA1: item.SHA1})
					if len(manifest.Files) > 5000 {
						return downloader.Manifest{}, downloader.Error("downloader_manifest_too_large", false, nil)
					}
				}
			}
			if !page.HasMore {
				break
			}
			offset += int64(len(page.Items))
		}
	}
	return manifest, nil
}

func validProviderItemName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && name != "." && name != ".." && len([]rune(name)) <= 255 && !strings.ContainsAny(name, "\x00\r\n/\\")
}

func (c *Client) Pause(context.Context, string) error {
	return downloader.Error("downloader_action_unsupported", false, nil)
}

func (c *Client) Resume(context.Context, string) error {
	return downloader.Error("downloader_action_unsupported", false, nil)
}

func (c *Client) Cancel(ctx context.Context, id string, deleteData bool) error {
	if _, itemID, ok := providerDirectoryTask(id); ok {
		if !deleteData {
			return nil
		}
		mutations, supported := c.driver.(cloud.MutationDriver)
		if !supported || !mutations.Capabilities().Recycle {
			return downloader.Error("downloader_action_unsupported", false, nil)
		}
		return mapError(mutations.Recycle(ctx, itemID))
	}
	return mapError(c.driver.CancelOffline(ctx, id, deleteData))
}

func providerDirectoryTask(id string) (string, string, bool) {
	for _, prefix := range []string{shareTaskPrefix, ingestTaskPrefix} {
		if strings.HasPrefix(id, prefix) && strings.TrimSpace(strings.TrimPrefix(id, prefix)) != "" {
			return prefix, strings.TrimSpace(strings.TrimPrefix(id, prefix)), true
		}
	}
	return "", "", false
}

func mapTask(task cloud.OfflineTask) downloader.Task {
	var completed *int64
	if task.BytesTotal != nil && task.Progress != nil {
		value := int64(float64(*task.BytesTotal) * *task.Progress)
		completed = &value
	}
	return downloader.Task{ID: task.ID, Name: task.Name, Status: task.Status, Progress: task.Progress, BytesCompleted: completed, BytesTotal: task.BytesTotal, ETASeconds: task.ETASeconds, Completed: task.Completed, Failed: task.Failed, OutputItemID: task.OutputItemID}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	code, retryable := cloud.ErrorInfo(err)
	switch code {
	case cloud.CodeAuthExpired:
		return downloader.Error("downloader_auth_failed", false, err)
	case cloud.CodeNotFound:
		return downloader.Error("downloader_task_not_found", false, err)
	case cloud.CodeOfflineNoQuota:
		return downloader.Error("downloader_quota_exhausted", false, err)
	case cloud.CodeOfflineBadLink:
		return downloader.Error("downloader_source_invalid", false, err)
	case cloud.CodeOfflineTaskExists:
		return downloader.Error("downloader_task_exists", false, err)
	case cloud.CodeShareInvalid, cloud.CodeShareEmpty, cloud.CodeShareTooLarge:
		return downloader.Error("downloader_share_invalid", false, err)
	case cloud.CodeShareUnknown, cloud.CodeMutationUnknown:
		return downloader.Error("downloader_share_result_unknown", true, err)
	case cloud.CodeResponseInvalid:
		return downloader.Error("downloader_response_invalid", retryable, err)
	case cloud.CodeRateLimited:
		return downloader.Error("downloader_rate_limited", true, err)
	default:
		return downloader.Error("downloader_unavailable", retryable, err)
	}
}
