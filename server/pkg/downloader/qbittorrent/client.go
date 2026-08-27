package qbittorrent

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

const (
	maxResponseBytes  = 1 << 20
	maxSourceURLBytes = 16 << 10
	maxManifestFiles  = 2048
)

var errMetadataStopConditionUnsupported = errors.New("qbittorrent metadata stop condition unsupported")

var Capabilities = downloader.Capabilities{Pause: true, Resume: true, Cancel: true, DeleteData: true, DownloadSpeed: true, UploadSpeed: true, ETA: true, Seeding: true, OutputConstraint: downloader.OutputConstraintLocalStaging}

type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

func New(config downloader.Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.BaseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, downloader.Error("downloader_url_invalid", false, err)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(config.BaseURL, "#") || parsed.RawPath != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, downloader.Error("downloader_url_invalid", false, nil)
	}
	return &Client{
		baseURL: strings.TrimRight(parsed.String(), "/"), username: config.Username, password: config.Password,
		http: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (c *Client) Test(ctx context.Context) (downloader.Health, error) {
	var version string
	err := c.session(ctx, func(cookie string) error {
		body, status, err := c.do(ctx, cookie, http.MethodGet, "/api/v2/app/version", "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		version = strings.TrimSpace(string(body))
		if version == "" || len(version) > 64 {
			return downloader.Error("downloader_response_invalid", false, nil)
		}
		return nil
	})
	return downloader.Health{Version: version}, err
}

func (c *Client) Submit(ctx context.Context, request downloader.SubmitRequest) (downloader.Task, error) {
	request.Tag = strings.TrimSpace(request.Tag)
	if request.Tag == "" || len(request.Tag) > 96 || strings.ContainsAny(request.Tag, ",\r\n") || strings.TrimSpace(request.SavePath) == "" {
		return downloader.Task{}, downloader.Error("downloader_request_invalid", false, nil)
	}
	if err := validateSource(request.Source); err != nil {
		return downloader.Task{}, err
	}
	// Submission can succeed upstream while the response is lost or rejected by
	// an older adapter. Adopt the stable OMC tag before creating anything.
	if existing, err := c.Get(ctx, "tag:"+request.Tag); err == nil {
		return existing, nil
	} else if code, retryable := downloader.ErrorInfo(err); code != "downloader_task_not_found" || retryable {
		return downloader.Task{}, err
	}
	var addedID string
	metadataStopCondition := request.MetadataOnly && request.Source.Kind == downloader.SourceURL && strings.HasPrefix(strings.ToLower(strings.TrimSpace(request.Source.URL)), "magnet:")
	err := c.session(ctx, func(cookie string) error {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		switch request.Source.Kind {
		case downloader.SourceURL:
			field, err := writer.CreateFormField("urls")
			if err != nil {
				return err
			}
			_, err = io.WriteString(field, request.Source.URL)
			if err != nil {
				return err
			}
		case downloader.SourceTorrent:
			filename := path.Base(strings.ReplaceAll(request.Source.Filename, "\\", "/"))
			if filename == "." || filename == "/" || filename == "" {
				filename = "upload.torrent"
			}
			field, err := writer.CreateFormFile("torrents", filename)
			if err != nil {
				return err
			}
			if _, err := field.Write(request.Source.Torrent); err != nil {
				return err
			}
		default:
			return downloader.Error("downloader_source_invalid", false, nil)
		}
		fields := map[string]string{"savepath": request.SavePath, "tags": request.Tag}
		if metadataStopCondition {
			fields["stopCondition"] = "MetadataReceived"
		}
		for key, value := range fields {
			field, err := writer.CreateFormField(key)
			if err != nil {
				return err
			}
			if _, err := io.WriteString(field, value); err != nil {
				return err
			}
		}
		if err := writer.Close(); err != nil {
			return err
		}
		response, status, err := c.do(ctx, cookie, http.MethodPost, "/api/v2/torrents/add", writer.FormDataContentType(), body.Bytes())
		if err != nil {
			return err
		}
		if status != http.StatusOK && status != http.StatusAccepted {
			return statusError(status)
		}
		trimmed := strings.TrimSpace(string(response))
		if trimmed == "Ok." {
			return nil
		}
		if trimmed == "Fails." {
			if metadataStopCondition {
				return errMetadataStopConditionUnsupported
			}
			return downloader.Error("downloader_request_failed", false, nil)
		}
		var modern struct {
			SuccessCount    int      `json:"success_count"`
			FailureCount    int      `json:"failure_count"`
			PendingCount    int      `json:"pending_count"`
			AddedTorrentIDs []string `json:"added_torrent_ids"`
		}
		if err := json.Unmarshal(response, &modern); err != nil {
			// A successful but unfamiliar response is ambiguous. Reconcile by the
			// stable tag below instead of turning a possibly-created task terminal.
			return nil
		}
		if !validModernCounts(modern.SuccessCount, modern.FailureCount, modern.PendingCount) || len(modern.AddedTorrentIDs) > 1 {
			return nil
		}
		if modern.FailureCount == 1 && modern.SuccessCount == 0 && modern.PendingCount == 0 && len(modern.AddedTorrentIDs) == 0 {
			return downloader.Error("downloader_request_failed", false, nil)
		}
		if len(modern.AddedTorrentIDs) == 1 && modern.SuccessCount == 1 && modern.FailureCount == 0 && modern.PendingCount == 0 {
			candidate := strings.TrimSpace(modern.AddedTorrentIDs[0])
			if validTorrentHash(candidate) {
				addedID = strings.ToLower(candidate)
			}
		}
		return nil
	})
	if err != nil {
		if metadataStopCondition && (errors.Is(err, errMetadataStopConditionUnsupported) || isUnsupportedStopCondition(err)) {
			request.MetadataOnly = false
			return c.Submit(ctx, request)
		}
		return downloader.Task{}, err
	}
	if addedID != "" {
		if task, getErr := c.Get(ctx, addedID); getErr == nil {
			return task, nil
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		task, getErr := c.Get(ctx, "tag:"+request.Tag)
		if getErr == nil {
			return task, nil
		}
		code, retryable := downloader.ErrorInfo(getErr)
		if code != "downloader_task_not_found" || retryable {
			return downloader.Task{}, getErr
		}
		select {
		case <-ctx.Done():
			return downloader.Task{}, downloader.Error("downloader_request_cancelled", true, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	return downloader.Task{ID: "tag:" + request.Tag, Status: "submitted"}, nil
}

func validModernCounts(success, failure, pending int) bool {
	if success < 0 || success > 1 || failure < 0 || failure > 1 || pending < 0 || pending > 1 {
		return false
	}
	return success+failure+pending <= 1
}

func validTorrentHash(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isUnsupportedStopCondition(err error) bool {
	var provider *downloader.ProviderError
	if !errors.As(err, &provider) || provider.Code != "downloader_request_failed" {
		return false
	}
	var status *httpStatusError
	return errors.As(provider.Cause, &status) && (status.status == http.StatusBadRequest || status.status == http.StatusNotFound)
}

func (c *Client) Manifest(ctx context.Context, id string) (downloader.Manifest, error) {
	hash, err := c.resolveHash(ctx, id)
	if err != nil {
		return downloader.Manifest{}, err
	}
	task, err := c.Get(ctx, hash)
	if err != nil {
		return downloader.Manifest{}, err
	}
	result := downloader.Manifest{Name: safeProviderName(task.Name), Complete: !strings.Contains(strings.ToLower(task.Status), "metadata")}
	err = c.session(ctx, func(cookie string) error {
		body, status, err := c.do(ctx, cookie, http.MethodGet, "/api/v2/torrents/files?"+url.Values{"hash": {hash}}.Encode(), "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		var files []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		}
		if err := json.Unmarshal(body, &files); err != nil || len(files) > maxManifestFiles {
			return downloader.Error("downloader_manifest_invalid", false, err)
		}
		result.Files = make([]downloader.File, 0, len(files))
		for _, file := range files {
			name, valid := safeManifestPath(file.Name)
			if !valid || file.Size < 0 {
				return downloader.Error("downloader_manifest_invalid", false, nil)
			}
			result.Files = append(result.Files, downloader.File{RelativePath: name, Size: file.Size})
		}
		return nil
	})
	return result, err
}

func safeManifestPath(value string) (string, bool) {
	if value == "" || len(value) > 2048 {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") {
		return "", false
	}
	parts := strings.Split(normalized, "/")
	for index, part := range parts {
		if part == "" || part == "." || part == ".." || strings.Contains(part, ":") {
			return "", false
		}
		if index == 0 && len(part) >= 2 && ((part[0] >= 'a' && part[0] <= 'z') || (part[0] >= 'A' && part[0] <= 'Z')) && part[1] == ':' {
			return "", false
		}
	}
	cleaned := path.Clean(normalized)
	if cleaned != normalized || cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func (c *Client) Categories(ctx context.Context) ([]downloader.Category, error) {
	var result []downloader.Category
	err := c.session(ctx, func(cookie string) error {
		body, status, err := c.do(ctx, cookie, http.MethodGet, "/api/v2/torrents/categories", "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		var values map[string]struct {
			Name     string `json:"name"`
			SavePath string `json:"savePath"`
		}
		if err := json.Unmarshal(body, &values); err != nil || len(values) > 512 {
			return downloader.Error("downloader_response_invalid", false, err)
		}
		result = make([]downloader.Category, 0, len(values))
		for key, value := range values {
			name := strings.TrimSpace(value.Name)
			if name == "" {
				name = strings.TrimSpace(key)
			}
			if name != "" && len(name) <= 128 {
				result = append(result, downloader.Category{Name: name, SavePath: value.SavePath})
			}
		}
		return nil
	})
	return result, err
}

func (c *Client) EnsureCategory(ctx context.Context, name, savePath string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n") || strings.TrimSpace(savePath) == "" {
		return downloader.Error("downloader_category_invalid", false, nil)
	}
	categories, err := c.Categories(ctx)
	if err != nil {
		return err
	}
	for _, category := range categories {
		if strings.EqualFold(category.Name, name) {
			return nil
		}
	}
	return c.action(ctx, "/api/v2/torrents/createCategory", url.Values{"category": {name}, "savePath": {savePath}})
}

func (c *Client) UpdateCategory(ctx context.Context, name, savePath string) error {
	name = strings.TrimSpace(name)
	savePath = strings.TrimSpace(savePath)
	if name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n") || savePath == "" || strings.ContainsAny(savePath, "\r\n") {
		return downloader.Error("downloader_category_invalid", false, nil)
	}
	err := c.action(ctx, "/api/v2/torrents/editCategory", url.Values{"category": {name}, "savePath": {savePath}})
	if err == nil {
		return nil
	}
	code, retryable := downloader.ErrorInfo(err)
	if code == "downloader_auth_failed" {
		return err
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && (statusErr.status == http.StatusNotFound || statusErr.status == http.StatusMethodNotAllowed) {
		return downloader.Error("downloader_category_update_unsupported", false, statusErr)
	}
	return downloader.Error("downloader_category_update_failed", retryable, err)
}

func (c *Client) SetCategory(ctx context.Context, id, category, savePath string) error {
	hash, err := c.resolveHash(ctx, id)
	if err != nil {
		return err
	}
	category = strings.TrimSpace(category)
	savePath = strings.TrimSpace(savePath)
	if category == "" || len(category) > 128 || strings.ContainsAny(category, "\r\n") || savePath == "" || strings.ContainsAny(savePath, "\r\n") {
		return downloader.Error("downloader_category_invalid", false, nil)
	}
	if err := c.action(ctx, "/api/v2/torrents/setCategory", url.Values{"hashes": {hash}, "category": {category}}); err != nil {
		return err
	}
	return c.action(ctx, "/api/v2/torrents/setLocation", url.Values{"hashes": {hash}, "location": {savePath}})
}

func safeProviderName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 256 {
		value = value[:256]
	}
	return value
}

func validateSource(source downloader.Source) error {
	switch source.Kind {
	case downloader.SourceURL:
		raw := strings.TrimSpace(source.URL)
		if raw == "" || len(raw) > maxSourceURLBytes {
			return downloader.Error("downloader_source_invalid", false, nil)
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return downloader.Error("downloader_source_invalid", false, err)
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme == "magnet" {
			if parsed.Query().Get("xt") == "" {
				return downloader.Error("downloader_source_invalid", false, nil)
			}
			return nil
		}
		if (scheme != "http" && scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return downloader.Error("downloader_source_invalid", false, nil)
		}
		return nil
	case downloader.SourceTorrent:
		filename := path.Base(strings.ReplaceAll(strings.TrimSpace(source.Filename), "\\", "/"))
		if len(source.Torrent) == 0 || len(source.Torrent) > downloader.MaxTorrentBytes || source.Torrent[0] != 'd' || !strings.EqualFold(path.Ext(filename), ".torrent") {
			return downloader.Error("downloader_source_invalid", false, nil)
		}
		return nil
	default:
		return downloader.Error("downloader_source_invalid", false, nil)
	}
}

func (c *Client) Get(ctx context.Context, id string) (downloader.Task, error) {
	var result downloader.Task
	err := c.session(ctx, func(cookie string) error {
		values := url.Values{}
		if strings.HasPrefix(id, "tag:") {
			values.Set("tag", strings.TrimPrefix(id, "tag:"))
		} else {
			values.Set("hashes", id)
		}
		body, status, err := c.do(ctx, cookie, http.MethodGet, "/api/v2/torrents/info?"+values.Encode(), "", nil)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		var torrents []torrentInfo
		if err := json.Unmarshal(body, &torrents); err != nil {
			return downloader.Error("downloader_response_invalid", false, err)
		}
		if len(torrents) == 0 {
			return downloader.Error("downloader_task_not_found", false, nil)
		}
		result = toTask(torrents[0])
		return nil
	})
	return result, err
}

func (c *Client) Pause(ctx context.Context, id string) error {
	hash, err := c.resolveHash(ctx, id)
	if err != nil {
		return err
	}
	err = c.action(ctx, "/api/v2/torrents/pause", url.Values{"hashes": {hash}})
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound {
		return c.action(ctx, "/api/v2/torrents/stop", url.Values{"hashes": {hash}})
	}
	return err
}

func (c *Client) Resume(ctx context.Context, id string) error {
	hash, err := c.resolveHash(ctx, id)
	if err != nil {
		return err
	}
	err = c.action(ctx, "/api/v2/torrents/resume", url.Values{"hashes": {hash}})
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) && statusErr.status == http.StatusNotFound {
		return c.action(ctx, "/api/v2/torrents/start", url.Values{"hashes": {hash}})
	}
	return err
}

func (c *Client) Cancel(ctx context.Context, id string, deleteData bool) error {
	hash, err := c.resolveHash(ctx, id)
	if err != nil {
		return err
	}
	return c.action(ctx, "/api/v2/torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {strconv.FormatBool(deleteData)}})
}

func (c *Client) resolveHash(ctx context.Context, id string) (string, error) {
	if !strings.HasPrefix(id, "tag:") {
		return id, nil
	}
	task, err := c.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

func (c *Client) action(ctx context.Context, endpoint string, values url.Values) error {
	return c.session(ctx, func(cookie string) error {
		body := []byte(values.Encode())
		response, status, err := c.do(ctx, cookie, http.MethodPost, endpoint, "application/x-www-form-urlencoded", body)
		if err != nil {
			return err
		}
		if status != http.StatusOK {
			return statusError(status)
		}
		// Legacy qBittorrent actions may answer "Ok." while modern versions
		// normally return an empty body. Both are successful; the legacy
		// "Fails." body is an explicit failure even though its status is 200.
		if strings.EqualFold(strings.TrimSpace(string(response)), "Fails.") {
			return downloader.Error("downloader_request_failed", false, nil)
		}
		return nil
	})
}

func (c *Client) session(ctx context.Context, fn func(string) error) error {
	values := url.Values{"username": {c.username}, "password": {c.password}}
	cookie, err := c.loginCookie(ctx, values)
	if err != nil {
		return err
	}
	return fn(cookie)
}

func (c *Client) loginCookie(ctx context.Context, values url.Values) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v2/auth/login", strings.NewReader(values.Encode()))
	if err != nil {
		return "", downloader.Error("downloader_request_invalid", false, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(request)
	if err != nil {
		return "", downloader.Error("downloader_unavailable", true, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return "", downloader.Error("downloader_response_invalid", false, err)
	}
	legacySuccess := response.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == "Ok."
	modernSuccess := response.StatusCode == http.StatusNoContent && len(body) == 0
	if !legacySuccess && !modernSuccess {
		if response.StatusCode != http.StatusOK {
			return "", statusError(response.StatusCode)
		}
		// qBittorrent historically reports rejected credentials as HTTP 200
		// with a non-Ok body (normally "Fails."). Never expose that body.
		return "", downloader.Error("downloader_auth_failed", false, nil)
	}
	for _, cookie := range response.Cookies() {
		if isSessionCookie(cookie.Name) && cookie.Value != "" {
			return cookie.Name + "=" + cookie.Value, nil
		}
	}
	return "", downloader.Error("downloader_auth_failed", false, nil)
}

func isSessionCookie(name string) bool {
	if name == "SID" {
		return true
	}
	const prefix = "QBT_SID_"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	port, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
	return err == nil && port >= 1 && port <= 65535
}

func (c *Client) do(ctx context.Context, cookie, method, endpoint, contentType string, body []byte) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, downloader.Error("downloader_request_invalid", false, err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if cookie != "" {
		request.Header.Set("Cookie", cookie)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, downloader.Error("downloader_unavailable", true, err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		return nil, response.StatusCode, downloader.Error("downloader_response_invalid", false, err)
	}
	return responseBody, response.StatusCode, nil
}

type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("downloader returned HTTP %d", e.status) }

func statusError(status int) error {
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return downloader.Error("downloader_auth_failed", false, nil)
	}
	if status == http.StatusTooManyRequests {
		return downloader.Error("downloader_rate_limited", true, &httpStatusError{status: status})
	}
	if status >= 500 {
		return downloader.Error("downloader_unavailable", true, &httpStatusError{status: status})
	}
	return downloader.Error("downloader_request_failed", false, &httpStatusError{status: status})
}

type torrentInfo struct {
	Hash       string   `json:"hash"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Progress   float64  `json:"progress"`
	Downloaded int64    `json:"downloaded"`
	TotalSize  int64    `json:"total_size"`
	DL         int64    `json:"dlspeed"`
	UL         int64    `json:"upspeed"`
	ETA        int64    `json:"eta"`
	Ratio      *float64 `json:"ratio"`
	SeedTime   *int64   `json:"seeding_time"`
	Uploaded   *int64   `json:"uploaded"`
}

func toTask(info torrentInfo) downloader.Task {
	progress := info.Progress * 100
	completed, total, downloadSpeed, uploadSpeed := info.Downloaded, info.TotalSize, info.DL, info.UL
	result := downloader.Task{ID: info.Hash, Name: info.Name, Status: info.State, Progress: &progress, BytesCompleted: &completed, BytesTotal: &total, DownloadSpeed: &downloadSpeed, UploadSpeed: &uploadSpeed}
	result.Ratio, result.SeededSeconds, result.UploadedBytes = info.Ratio, info.SeedTime, info.Uploaded
	if info.ETA >= 0 && info.ETA < 8640000 {
		eta := info.ETA
		result.ETASeconds = &eta
	}
	state := strings.ToLower(info.State)
	result.Seeding = strings.Contains(state, "upload") || strings.Contains(state, "seed") || strings.Contains(state, "stalledup") || strings.Contains(state, "pausedup") || strings.Contains(state, "stoppedup")
	result.Completed = info.Progress >= 1 && !strings.Contains(state, "checking") && !strings.Contains(state, "metadata")
	if state == "error" || state == "missingfiles" || state == "unknown" {
		result.Failed, result.ErrorCode = true, "downloader_provider_failed"
	}
	return result
}
