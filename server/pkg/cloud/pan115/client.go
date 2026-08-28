package pan115

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

const (
	defaultTimeout      = 15 * time.Second
	defaultPageSize     = int64(200)
	maxPageSize         = int64(pan115sdk.MaxDirPageLimit)
	bulkTreePageSize    = int64(pan115sdk.MaxDirPageLimit)
	bulkFolderPageSize  = int64(5000)
	bulkRequestSpacing  = 500 * time.Millisecond
	maxInFlightCalls    = 2
	maxOfflineTaskPages = int64(50)
	bulkFoldersEndpoint = "https://proapi.115.com/app/2.0/chrome/downfolders"
)

var errCircuitOpen = errors.New("115 request circuit is cooling down")

type sdkClient interface {
	CookieCheck() error
	GetUser() (*pan115sdk.UserInfo, error)
	GetInfo() (pan115sdk.InfoData, error)
	ListPage(string, int64, int64, ...pan115sdk.ListOption) (*[]pan115sdk.File, error)
	GetFile(string) (*pan115sdk.File, error)
	DownloadWithUA(string, string) (*pan115sdk.DownloadInfo, error)
	AddOfflineTaskURIs([]string, string, ...pan115sdk.OfflineOption) ([]string, error)
	ListOfflineTask(int64) (pan115sdk.OfflineTaskResp, error)
	DeleteOfflineTasks([]string, bool) error
	ListTreeFiles(string, int64, int64) ([]pan115sdk.File, int64, error)
	ListTreeFolders(string, int64, int64) ([]bulkFolder, bool, error)
	ListLifeEvents(int) (lifeEventBatch, error)
}

type recycleSDK interface {
	CleanRecycleBin(string, ...string) error
	ListRecycleBin(int, int) ([]pan115sdk.RecycleBinItem, error)
}

type shareSDK interface {
	GetShareSnapWithUA(string, string, string, string, ...pan115sdk.Query) (*pan115sdk.ShareSnapResp, error)
	ReceiveShare(string, string, []string, string) error
}

type mutationSDK interface {
	Mkdir(string, string) (string, error)
	Move(string, ...string) error
	Copy(string, ...string) error
	Rename(string, string) error
	Delete(...string) error
}

type uploadSDK interface {
	UploadFastOrByMultipart(string, string, int64, *os.File, ...pan115sdk.UploadMultipartOption) error
}

type sdkAdapter struct{ *pan115sdk.Pan115Client }

const shareReceiveEndpoint = "https://webapi.115.com/share/receive"

type shareReceiveResponse struct {
	pan115sdk.BasicResp
}

func (s *sdkAdapter) ReceiveShare(shareCode, receiveCode string, fileIDs []string, directoryID string) error {
	result := shareReceiveResponse{}
	response, err := s.Client.R().SetFormData(map[string]string{
		"share_code": shareCode, "receive_code": receiveCode,
		"file_id": strings.Join(fileIDs, ","), "cid": directoryID, "is_check": "0",
	}).SetHeader("referer", pan115sdk.BuildShareReferer(shareCode, receiveCode)).
		SetHeader("User-Agent", pan115sdk.UA115Browser).
		SetResult(&result).ForceContentType("application/json;charset=UTF-8").Post(shareReceiveEndpoint)
	if err != nil {
		return err
	}
	if response.IsError() {
		return fmt.Errorf("115 share receive returned HTTP %d", response.StatusCode())
	}
	return result.Err()
}

type bulkFolder struct {
	ID       string
	ParentID string
	Name     string
}

type bulkFolderResponse struct {
	pan115sdk.BasicResp
	Data struct {
		List        []bulkFolderWire `json:"list"`
		HasNextPage flexibleBool     `json:"has_next_page"`
	} `json:"data"`
}

type bulkFolderWire struct {
	ID       pan115sdk.IntString `json:"fid"`
	ParentID pan115sdk.IntString `json:"pid"`
	Name     string              `json:"fn"`
}

type flexibleBool bool

func (value *flexibleBool) UnmarshalJSON(data []byte) error {
	var boolean bool
	if err := json.Unmarshal(data, &boolean); err == nil {
		*value = flexibleBool(boolean)
		return nil
	}
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*value = number != 0
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	*value = flexibleBool(text == "1" || strings.EqualFold(text, "true"))
	return nil
}

func (s *sdkAdapter) ListTreeFiles(directoryID string, offset, limit int64) ([]pan115sdk.File, int64, error) {
	result := pan115sdk.FileListResp{}
	response, err := s.Client.R().SetQueryParams(map[string]string{
		"aid": "1", "cid": directoryID, "offset": strconv.FormatInt(offset, 10), "limit": strconv.FormatInt(limit, 10),
		"show_dir": "0", "count_folders": "0", "cur": "0", "type": "99", "fc_mix": "0", "format": "json",
	}).SetResult(&result).ForceContentType("application/json;charset=UTF-8").Get(pan115sdk.ApiFileList)
	if err != nil {
		return nil, 0, err
	}
	if response.IsError() {
		return nil, 0, fmt.Errorf("115 bulk file request returned HTTP %d", response.StatusCode())
	}
	if !result.State {
		return nil, 0, errors.New("115 bulk file request was rejected")
	}
	files := make([]pan115sdk.File, 0, len(result.Files))
	for index := range result.Files {
		files = append(files, *(&pan115sdk.File{}).From(&result.Files[index]))
	}
	return files, int64(result.Count), nil
}

func (s *sdkAdapter) ListTreeFolders(pickCode string, page, limit int64) ([]bulkFolder, bool, error) {
	result := bulkFolderResponse{}
	response, err := s.Client.R().SetQueryParams(map[string]string{
		"pickcode": pickCode, "page": strconv.FormatInt(page, 10), "per_page": strconv.FormatInt(limit, 10),
	}).SetResult(&result).ForceContentType("application/json;charset=UTF-8").Get(bulkFoldersEndpoint)
	if err != nil {
		return nil, false, err
	}
	if response.IsError() {
		return nil, false, fmt.Errorf("115 bulk folder request returned HTTP %d", response.StatusCode())
	}
	if !result.State {
		return nil, false, errors.New("115 bulk folder request was rejected")
	}
	folders := make([]bulkFolder, 0, len(result.Data.List))
	for _, item := range result.Data.List {
		folders = append(folders, bulkFolder{ID: string(item.ID), ParentID: string(item.ParentID), Name: strings.TrimSpace(item.Name)})
	}
	return folders, bool(result.Data.HasNextPage), nil
}

type Client struct {
	sdk             sdkClient
	downloadHTTP    *http.Client
	listRate        *rate.Limiter
	bulkRate        *rate.Limiter
	directRate      *rate.Limiter
	offlineRate     *rate.Limiter
	eventRate       *rate.Limiter
	mkdirRate       *rate.Limiter
	uploadRate      *rate.Limiter
	moveRate        *rate.Limiter
	copyRate        *rate.Limiter
	renameRate      *rate.Limiter
	recycleRate     *rate.Limiter
	purgeRate       *rate.Limiter
	offlinePages    sync.Map
	callSlots       chan struct{}
	stateMu         sync.Mutex
	now             func() time.Time
	jitter          func() time.Duration
	riskFails       int
	backoffTil      time.Time
	circuitTil      time.Time
	recyclePassword string
}

func New(config cloud.Config) (cloud.Driver, error) {
	parsed, err := ParseCookie(config.Cookie)
	if err != nil {
		return nil, cloud.Error(cloud.CodeCookieInvalid, false, err)
	}
	credential := &pan115sdk.Credential{UID: parsed.UID, CID: parsed.CID, SEID: parsed.SEID, KID: parsed.KID}
	httpClient := &http.Client{
		Timeout:       defaultTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	// The Cookie-authenticated offline endpoint returns a non-JSON "decode
	// fail" response for generic and iOS user agents. 115Browser is required
	// for offline submission and remains compatible with the read APIs.
	sdk := pan115sdk.New(pan115sdk.WithClient(httpClient), pan115sdk.UA(pan115sdk.UA115Browser)).ImportCredential(credential)
	return &Client{
		sdk: &sdkAdapter{sdk}, downloadHTTP: newDownloadHTTPClient(), listRate: rate.NewLimiter(rate.Every(2*time.Second), 1), bulkRate: rate.NewLimiter(rate.Every(bulkRequestSpacing), 1), directRate: rate.NewLimiter(rate.Every(time.Second), 1),
		offlineRate: rate.NewLimiter(rate.Every(2*time.Second), 1), eventRate: rate.NewLimiter(rate.Every(5*time.Second), 1),
		// MoviePilot's p115 integrations pace provider endpoints independently.
		// Keep that boundary here: healthy mkdir is only concurrency bounded and
		// must not wait behind move/rename/delete traffic, while each destructive
		// operation retains its conservative two-second lane.
		mkdirRate: rate.NewLimiter(rate.Inf, 1), uploadRate: rate.NewLimiter(rate.Every(2*time.Second), 1),
		moveRate: rate.NewLimiter(rate.Every(2*time.Second), 1), copyRate: rate.NewLimiter(rate.Every(2*time.Second), 1), renameRate: rate.NewLimiter(rate.Every(2*time.Second), 1),
		recycleRate: rate.NewLimiter(rate.Every(2*time.Second), 1), purgeRate: rate.NewLimiter(rate.Every(2*time.Second), 1),
		callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: defaultJitter, recyclePassword: strings.TrimSpace(config.RecyclePassword),
	}, nil
}

func (c *Client) Provider() string { return cloud.ProviderPan115 }

func (c *Client) Capabilities() cloud.Capabilities {
	return cloud.Capabilities{NetworkDrive: true, DirectoryList: true, Watch: true, NativeOfflineDownload: true, ShareReceive: true, TemporaryDirectURL: true, SignedProxy: true, FileUpload: true, ChangeCursor: true, CreateDirectory: true, Move: true, Copy: true, Rename: true, Recycle: true}
}

const maxShareTopLevelItems = 1000

var shareCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{4,128}$`)
var shareItemIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func parseShareLink(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n") {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share link is invalid"))
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share link is invalid"))
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host != "115.com" && !strings.HasSuffix(host, ".115.com") && host != "115cdn.com" && !strings.HasSuffix(host, ".115cdn.com") {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share host is invalid"))
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 2 || segments[0] != "s" {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share path is invalid"))
	}
	shareCode, unescapeErr := url.PathUnescape(segments[1])
	if unescapeErr != nil || !shareCodePattern.MatchString(shareCode) {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share code is invalid"))
	}
	query := parsed.Query()
	receiveCode := strings.TrimSpace(query.Get("password"))
	if receiveCode == "" {
		receiveCode = strings.TrimSpace(query.Get("pwd"))
	}
	if receiveCode == "" {
		receiveCode = strings.TrimSpace(query.Get("receive_code"))
	}
	if len(receiveCode) < 1 || len(receiveCode) > 32 || strings.ContainsAny(receiveCode, "\x00\r\n&=#") {
		return "", "", cloud.Error(cloud.CodeShareInvalid, false, errors.New("share receive code is invalid"))
	}
	return shareCode, receiveCode, nil
}

func validShareItemName(name string) bool {
	return name != "" && name != "." && name != ".." && len([]rune(name)) <= 255 && !strings.ContainsAny(name, "\x00\r\n/\\")
}

func (c *Client) InspectShare(ctx context.Context, raw string) (cloud.ShareSnapshot, error) {
	shareCode, receiveCode, err := parseShareLink(raw)
	if err != nil {
		return cloud.ShareSnapshot{}, err
	}
	sdk, ok := c.sdk.(shareSDK)
	if !ok {
		return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeUnavailable, false, errors.New("115 share API is unavailable"))
	}
	result := cloud.ShareSnapshot{ShareCode: shareCode, ReceiveCode: receiveCode}
	seenItemIDs := make(map[string]struct{}, maxShareTopLevelItems)
	pageLimit := int(maxPageSize)
	if pageLimit > maxShareTopLevelItems {
		pageLimit = maxShareTopLevelItems
	}
	for offset := 0; offset < maxShareTopLevelItems; {
		var page *pan115sdk.ShareSnapResp
		callErr := c.waitAndCall(ctx, c.offlineRate, func() error {
			var sdkErr error
			page, sdkErr = sdk.GetShareSnapWithUA(pan115sdk.UA115Browser, shareCode, receiveCode, "0", pan115sdk.QueryLimit(pageLimit), pan115sdk.QueryOffset(offset))
			return sdkErr
		})
		if callErr != nil {
			return cloud.ShareSnapshot{}, mapError(callErr)
		}
		if page == nil {
			return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned no share snapshot"))
		}
		if page.Data.Count > maxShareTopLevelItems {
			return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeShareTooLarge, false, errors.New("share contains too many top-level items"))
		}
		if result.Title == "" {
			result.Title = strings.TrimSpace(page.Data.Shareinfo.ShareTitle)
		}
		for _, item := range page.Data.List {
			id, name := strings.TrimSpace(item.FileID), strings.TrimSpace(item.FileName)
			if !shareItemIDPattern.MatchString(id) || !validShareItemName(name) {
				return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 returned an invalid share item"))
			}
			if _, exists := seenItemIDs[id]; exists {
				return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 returned a duplicate share item"))
			}
			seenItemIDs[id] = struct{}{}
			result.Items = append(result.Items, cloud.ShareItem{ID: id, Name: name, IsDir: item.IsFile == 0, Size: int64(item.Size)})
			if len(result.Items) > maxShareTopLevelItems {
				return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeShareTooLarge, false, errors.New("share contains too many top-level items"))
			}
		}
		pageItems := len(page.Data.List)
		if pageItems == 0 || pageItems < pageLimit || (page.Data.Count > 0 && len(result.Items) >= page.Data.Count) {
			break
		}
		offset += pageItems
	}
	if len(result.Items) == 0 {
		return cloud.ShareSnapshot{}, cloud.Error(cloud.CodeShareEmpty, false, errors.New("share is empty"))
	}
	return result, nil
}

func (c *Client) ReceiveShare(ctx context.Context, snapshot cloud.ShareSnapshot, directoryID string) error {
	directoryID = normalizeID(directoryID)
	if !shareCodePattern.MatchString(snapshot.ShareCode) || snapshot.ReceiveCode == "" || !shareItemIDPattern.MatchString(directoryID) || len(snapshot.Items) == 0 || len(snapshot.Items) > maxShareTopLevelItems {
		return cloud.Error(cloud.CodeShareInvalid, false, errors.New("share snapshot is invalid"))
	}
	ids := make([]string, 0, len(snapshot.Items))
	seen := make(map[string]struct{}, len(snapshot.Items))
	for _, item := range snapshot.Items {
		id := strings.TrimSpace(item.ID)
		if !shareItemIDPattern.MatchString(id) {
			return cloud.Error(cloud.CodeShareInvalid, false, errors.New("share item identity is invalid"))
		}
		if _, exists := seen[id]; exists {
			return cloud.Error(cloud.CodeShareInvalid, false, errors.New("share item identity is duplicated"))
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sdk, ok := c.sdk.(shareSDK)
	if !ok {
		return cloud.Error(cloud.CodeUnavailable, false, errors.New("115 share API is unavailable"))
	}
	if err := c.waitAndCall(ctx, c.offlineRate, func() error {
		return sdk.ReceiveShare(snapshot.ShareCode, snapshot.ReceiveCode, ids, directoryID)
	}); err != nil {
		return mapError(err)
	}
	return nil
}

func (c *Client) CreateDirectory(ctx context.Context, parentID, name string) (cloud.Item, error) {
	parentID = normalizeID(parentID)
	name, err := mutationName(name)
	if err != nil {
		return cloud.Item{}, err
	}
	sdk, ok := c.sdk.(mutationSDK)
	if !ok {
		return cloud.Item{}, cloud.Error(cloud.CodeUnavailable, false, errors.New("115 mutation API is unavailable"))
	}
	var itemID string
	if err := c.waitAndCall(ctx, c.mkdirRate, func() error {
		var callErr error
		itemID, callErr = sdk.Mkdir(parentID, name)
		return callErr
	}); err != nil {
		return cloud.Item{}, mapError(err)
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return cloud.Item{}, cloud.Error(cloud.CodeMutationUnknown, true, errors.New("115 returned no created directory identity"))
	}
	return cloud.Item{ID: itemID, ParentID: parentID, Name: name, IsDir: true}, nil
}

func (c *Client) Upload(ctx context.Context, request cloud.UploadRequest) (cloud.Item, error) {
	parentID := normalizeID(request.ParentID)
	name, err := mutationName(request.Name)
	if err != nil || request.Size <= 0 {
		return cloud.Item{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 upload request is invalid"))
	}
	file, ok := request.Reader.(*os.File)
	if !ok || file == nil {
		return cloud.Item{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 upload requires a managed regular file"))
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != request.Size {
		return cloud.Item{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 upload file changed"))
	}
	sdk, ok := c.sdk.(uploadSDK)
	if !ok {
		return cloud.Item{}, cloud.Error(cloud.CodeUnavailable, false, errors.New("115 upload API is unavailable"))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return cloud.Item{}, cloud.Error(cloud.CodeResponseInvalid, false, err)
	}
	if err := c.waitForRecovery(ctx); err != nil {
		return cloud.Item{}, mapError(err)
	}
	if err := c.uploadRate.Wait(ctx); err != nil {
		return cloud.Item{}, mapError(err)
	}
	select {
	case c.callSlots <- struct{}{}:
	case <-ctx.Done():
		return cloud.Item{}, mapError(ctx.Err())
	}
	uploadErr := sdk.UploadFastOrByMultipart(parentID, name, request.Size, file, pan115sdk.UploadMultipartWithThreadsNum(1))
	<-c.callSlots
	c.recordOutcome(uploadErr)
	if uploadErr != nil {
		return cloud.Item{}, mapError(uploadErr)
	}
	if err := ctx.Err(); err != nil {
		return cloud.Item{}, mapError(err)
	}
	matches := make([]cloud.Item, 0, 1)
	for offset := int64(0); offset < 10_000; offset += maxPageSize {
		page, err := c.List(ctx, parentID, cloud.PageRequest{Offset: offset, Limit: maxPageSize})
		if err != nil {
			return cloud.Item{}, err
		}
		for _, item := range page.Items {
			if item.Name == name && !item.IsDir && item.Size == request.Size {
				matches = append(matches, item)
			}
		}
		if !page.HasMore {
			break
		}
	}
	if len(matches) != 1 {
		return cloud.Item{}, cloud.Error(cloud.CodeMutationUnknown, true, errors.New("115 upload result could not be reconciled"))
	}
	return matches[0], nil
}

func (c *Client) Move(ctx context.Context, itemID, targetParentID string) error {
	return c.MoveMany(ctx, []string{itemID}, targetParentID)
}

func (c *Client) Copy(ctx context.Context, itemID, targetParentID string) error {
	return c.CopyMany(ctx, []string{itemID}, targetParentID)
}

func (c *Client) MoveMany(ctx context.Context, itemIDs []string, targetParentID string) error {
	return c.mutateMany(ctx, c.moveRate, itemIDs, targetParentID, func(sdk mutationSDK, items []string, parent string) error {
		return sdk.Move(parent, items...)
	})
}

func (c *Client) CopyMany(ctx context.Context, itemIDs []string, targetParentID string) error {
	return c.mutateMany(ctx, c.copyRate, itemIDs, targetParentID, func(sdk mutationSDK, items []string, parent string) error {
		return sdk.Copy(parent, items...)
	})
}

func (c *Client) Rename(ctx context.Context, itemID, name string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 item identity is empty"))
	}
	name, err := mutationName(name)
	if err != nil {
		return err
	}
	sdk, ok := c.sdk.(mutationSDK)
	if !ok {
		return cloud.Error(cloud.CodeUnavailable, false, errors.New("115 mutation API is unavailable"))
	}
	if err := c.waitAndCall(ctx, c.renameRate, func() error { return sdk.Rename(itemID, name) }); err != nil {
		return mapError(err)
	}
	return nil
}

func (c *Client) Recycle(ctx context.Context, itemID string) error {
	return c.RecycleMany(ctx, []string{itemID})
}

func (c *Client) RecycleMany(ctx context.Context, itemIDs []string) error {
	items, err := mutationIDs(itemIDs)
	if err != nil {
		return err
	}
	sdk, ok := c.sdk.(mutationSDK)
	if !ok {
		return cloud.Error(cloud.CodeUnavailable, false, errors.New("115 mutation API is unavailable"))
	}
	if err := c.waitAndCall(ctx, c.recycleRate, func() error { return sdk.Delete(items...) }); err != nil {
		return mapError(err)
	}
	return nil
}

func (c *Client) PurgeRecycle(ctx context.Context, itemID string) error {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" || strings.ContainsAny(itemID, "\x00\r\n/, ") {
		return cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 recycle item identity is invalid"))
	}
	sdk, ok := c.sdk.(recycleSDK)
	if !ok {
		return cloud.Error(cloud.CodeUnavailable, false, errors.New("115 recycle cleanup API is unavailable"))
	}
	if err := c.waitAndCall(ctx, c.purgeRate, func() error {
		return sdk.CleanRecycleBin(c.recyclePassword, itemID)
	}); err != nil {
		original := err
		for offset := 0; offset < 1000; offset += 100 {
			var items []pan115sdk.RecycleBinItem
			if listErr := c.waitAndCall(ctx, c.purgeRate, func() error {
				var callErr error
				items, callErr = sdk.ListRecycleBin(offset, 100)
				return callErr
			}); listErr != nil {
				return mapError(original)
			}
			found := false
			for _, item := range items {
				if strings.TrimSpace(item.FileId) == itemID {
					found = true
					break
				}
			}
			if found {
				return mapError(original)
			}
			if len(items) < 100 {
				return nil
			}
		}
		return mapError(original)
	}
	return nil
}

func (c *Client) mutateMany(ctx context.Context, limiter *rate.Limiter, itemIDs []string, targetParentID string, call func(mutationSDK, []string, string) error) error {
	items, err := mutationIDs(itemIDs)
	if err != nil {
		return err
	}
	targetParentID = normalizeID(targetParentID)
	for _, itemID := range items {
		if itemID == targetParentID {
			return cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 mutation identity is invalid"))
		}
	}
	sdk, ok := c.sdk.(mutationSDK)
	if !ok {
		return cloud.Error(cloud.CodeUnavailable, false, errors.New("115 mutation API is unavailable"))
	}
	if err := c.waitAndCall(ctx, limiter, func() error { return call(sdk, items, targetParentID) }); err != nil {
		return mapError(err)
	}
	return nil
}

func mutationIDs(itemIDs []string) ([]string, error) {
	if len(itemIDs) == 0 || len(itemIDs) > cloud.MaxBatchMutationItems {
		return nil, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 mutation identity is invalid"))
	}
	result := make([]string, 0, len(itemIDs))
	seen := make(map[string]struct{}, len(itemIDs))
	for _, raw := range itemIDs {
		itemID := strings.TrimSpace(raw)
		if itemID == "" || itemID != raw || strings.ContainsAny(itemID, "\x00\r\n/, ") {
			return nil, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 mutation identity is invalid"))
		}
		if _, duplicate := seen[itemID]; duplicate {
			return nil, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 mutation identity is duplicated"))
		}
		seen[itemID] = struct{}{}
		result = append(result, itemID)
	}
	return result, nil
}

func mutationName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "." || value == ".." || len([]rune(value)) > 255 || strings.ContainsAny(value, "/\\\x00\r\n") {
		return "", cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 item name is invalid"))
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return "", cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 item name is invalid"))
		}
	}
	return value, nil
}

func (c *Client) SubmitOffline(ctx context.Context, uri, directoryID string) (cloud.OfflineTask, error) {
	uri, directoryID = strings.TrimSpace(uri), normalizeID(directoryID)
	if uri == "" || len(uri) > 8192 || strings.ContainsAny(uri, "\x00\r\n") {
		return cloud.OfflineTask{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("offline URI is invalid"))
	}
	var hashes []string
	err := c.waitAndCall(ctx, c.offlineRate, func() error {
		var err error
		hashes, err = c.sdk.AddOfflineTaskURIs([]string{uri}, directoryID)
		return err
	})
	if err != nil {
		if recovered, ok := c.recoverSubmittedOffline(ctx, uri); ok {
			return recovered, nil
		}
		return cloud.OfflineTask{}, mapError(err)
	}
	if len(hashes) != 1 || strings.TrimSpace(hashes[0]) == "" {
		if recovered, ok := c.recoverSubmittedOffline(ctx, uri); ok {
			return recovered, nil
		}
		return cloud.OfflineTask{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned no offline task identity"))
	}
	taskID := strings.TrimSpace(hashes[0])
	c.offlinePages.Store(taskID, int64(1))
	return cloud.OfflineTask{ID: taskID, Status: "queued", ProviderStatus: 0}, nil
}

func (c *Client) recoverSubmittedOffline(ctx context.Context, uri string) (cloud.OfflineTask, bool) {
	taskID, ok := offlineInfoHash(uri)
	if !ok {
		return cloud.OfflineTask{}, false
	}
	task, err := c.GetOffline(ctx, taskID)
	return task, err == nil
}

func offlineInfoHash(uri string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(uri))
	if err != nil || !strings.EqualFold(parsed.Scheme, "magnet") {
		return "", false
	}
	var exactTopic string
	for key, values := range parsed.Query() {
		if strings.EqualFold(key, "xt") && len(values) > 0 {
			exactTopic = strings.TrimSpace(values[0])
			break
		}
	}
	const prefix = "urn:btih:"
	if len(exactTopic) <= len(prefix) || !strings.EqualFold(exactTopic[:len(prefix)], prefix) {
		return "", false
	}
	value := strings.TrimSpace(exactTopic[len(prefix):])
	switch len(value) {
	case 40:
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 20 {
			return "", false
		}
		return strings.ToLower(value), true
	case 32:
		decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(value))
		if err != nil || len(decoded) != 20 {
			return "", false
		}
		return hex.EncodeToString(decoded), true
	default:
		return "", false
	}
}

func (c *Client) GetOffline(ctx context.Context, taskID string) (cloud.OfflineTask, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return cloud.OfflineTask{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("offline task identity is empty"))
	}
	maxPage, cachedPage := int64(1), int64(0)
	if cached, ok := c.offlinePages.Load(taskID); ok {
		if page, valid := cached.(int64); valid && page > 1 && page <= maxOfflineTaskPages {
			cachedPage = page
			response, err := c.listOfflinePage(ctx, page)
			if err != nil {
				return cloud.OfflineTask{}, err
			}
			if task := findOfflineTask(response.Tasks, taskID); task != nil {
				return mapOfflineTask(task), nil
			}
			maxPage = boundedOfflinePageCount(response.PageCount)
		}
	}
	for page := int64(1); page <= maxPage; page++ {
		if page == cachedPage {
			continue
		}
		response, err := c.listOfflinePage(ctx, page)
		if err != nil {
			return cloud.OfflineTask{}, err
		}
		if task := findOfflineTask(response.Tasks, taskID); task != nil {
			c.offlinePages.Store(taskID, page)
			return mapOfflineTask(task), nil
		}
		if count := boundedOfflinePageCount(response.PageCount); count > maxPage {
			maxPage = count
		}
	}
	c.offlinePages.Delete(taskID)
	return cloud.OfflineTask{}, cloud.Error(cloud.CodeNotFound, false, errors.New("offline task was not found"))
}

func (c *Client) listOfflinePage(ctx context.Context, page int64) (pan115sdk.OfflineTaskResp, error) {
	var response pan115sdk.OfflineTaskResp
	if err := c.waitAndCall(ctx, c.offlineRate, func() error {
		var err error
		response, err = c.sdk.ListOfflineTask(page)
		return err
	}); err != nil {
		return pan115sdk.OfflineTaskResp{}, mapError(err)
	}
	return response, nil
}

func findOfflineTask(tasks []*pan115sdk.OfflineTask, taskID string) *pan115sdk.OfflineTask {
	for _, task := range tasks {
		if task != nil && strings.EqualFold(strings.TrimSpace(task.InfoHash), taskID) {
			return task
		}
	}
	return nil
}

func boundedOfflinePageCount(count int64) int64 {
	if count < 1 {
		return 1
	}
	if count > maxOfflineTaskPages {
		return maxOfflineTaskPages
	}
	return count
}

func (c *Client) CancelOffline(ctx context.Context, taskID string, deleteFiles bool) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return cloud.Error(cloud.CodeResponseInvalid, false, errors.New("offline task identity is empty"))
	}
	if err := c.waitAndCall(ctx, c.offlineRate, func() error { return c.sdk.DeleteOfflineTasks([]string{taskID}, deleteFiles) }); err != nil {
		return mapError(err)
	}
	c.offlinePages.Delete(taskID)
	return nil
}

func mapOfflineTask(task *pan115sdk.OfflineTask) cloud.OfflineTask {
	progress := task.Percent
	if progress > 1 {
		progress /= 100
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	total, eta := task.Size, task.LeftTime
	status, completed, failed := "queued", false, false
	switch task.Status {
	case 1:
		status = "downloading"
	case 2:
		status, completed = "completed", true
	case -1:
		status, failed = "failed", true
	}
	outputID := strings.TrimSpace(task.DelFileId)
	if outputID == "" {
		outputID = strings.TrimSpace(task.FileId)
	}
	return cloud.OfflineTask{ID: strings.TrimSpace(task.InfoHash), Name: strings.TrimSpace(task.Name), Status: status, Progress: &progress, BytesTotal: &total, ETASeconds: &eta, OutputItemID: outputID, Completed: completed, Failed: failed, ProviderStatus: task.Status}
}

func (c *Client) Probe(ctx context.Context) (cloud.Account, error) {
	if err := c.waitAndCall(ctx, c.listRate, func() error { return c.sdk.CookieCheck() }); err != nil {
		return cloud.Account{}, mapError(err)
	}
	var user *pan115sdk.UserInfo
	if err := c.waitAndCall(ctx, c.listRate, func() error {
		var err error
		user, err = c.sdk.GetUser()
		return err
	}); err != nil {
		return cloud.Account{}, mapError(err)
	}
	var info pan115sdk.InfoData
	if err := c.waitAndCall(ctx, c.listRate, func() error {
		var err error
		info, err = c.sdk.GetInfo()
		return err
	}); err != nil {
		return cloud.Account{}, mapError(err)
	}
	if _, err := c.List(ctx, "0", cloud.PageRequest{Limit: 1}); err != nil {
		return cloud.Account{}, err
	}
	used, total := uint64(max(info.SpaceInfo.AllUse.Size, 0)), uint64(max(info.SpaceInfo.AllTotal.Size, 0))
	return cloud.Account{ID: strconv.FormatInt(user.UserID, 10), Name: strings.TrimSpace(user.UserName), VIP: user.Vip != 0, UsedBytes: &used, TotalBytes: &total}, nil
}

func (c *Client) List(ctx context.Context, parentID string, page cloud.PageRequest) (cloud.Page, error) {
	parentID = normalizeID(parentID)
	if page.Offset < 0 {
		return cloud.Page{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("negative page offset"))
	}
	if page.Limit <= 0 {
		page.Limit = defaultPageSize
	}
	if page.Limit > maxPageSize {
		page.Limit = maxPageSize
	}
	var files *[]pan115sdk.File
	err := c.waitAndCall(ctx, c.listRate, func() error {
		var err error
		files, err = c.sdk.ListPage(parentID, page.Offset, page.Limit)
		return err
	})
	if err != nil {
		return cloud.Page{}, mapError(err)
	}
	if files == nil {
		return cloud.Page{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned an empty file page"))
	}
	items := make([]cloud.Item, 0, len(*files))
	for _, file := range *files {
		item, err := mapFile(file)
		if err != nil {
			return cloud.Page{}, err
		}
		items = append(items, item)
	}
	return cloud.Page{Items: items, Offset: page.Offset, HasMore: int64(len(items)) == page.Limit}, nil
}

// ListTree uses 115's recursive file enumeration together with its descendant
// folder stream. This is the full-scan lane; interactive List remains at the
// conservative one-request-per-two-seconds policy used by the directory UI.
func (c *Client) ListTree(ctx context.Context, rootID string, maxEntries int) (cloud.TreeResult, error) {
	rootID = normalizeID(rootID)
	if rootID == "0" {
		return cloud.TreeResult{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 root has no bulk tree identity"))
	}
	root, err := c.Stat(ctx, rootID)
	if err != nil {
		return cloud.TreeResult{}, err
	}
	if !root.IsDir || root.PickCode == "" {
		return cloud.TreeResult{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 bulk tree root is invalid"))
	}
	type folderResult struct {
		items []bulkFolder
		err   error
	}
	type fileResult struct {
		items   []pan115sdk.File
		partial bool
		err     error
	}
	foldersDone, filesDone := make(chan folderResult, 1), make(chan fileResult, 1)
	go func() {
		items, loadErr := c.loadTreeFolders(ctx, root.PickCode)
		foldersDone <- folderResult{items: items, err: loadErr}
	}()
	go func() {
		items, partial, loadErr := c.loadTreeFiles(ctx, rootID, maxEntries)
		filesDone <- fileResult{items: items, partial: partial, err: loadErr}
	}()
	folders, files := <-foldersDone, <-filesDone
	if folders.err != nil {
		return cloud.TreeResult{}, folders.err
	}
	if files.err != nil {
		return cloud.TreeResult{}, files.err
	}
	entries, err := buildTreeEntries(rootID, folders.items, files.items)
	if err != nil {
		return cloud.TreeResult{}, err
	}
	return cloud.TreeResult{Entries: entries, Partial: files.partial}, nil
}

func (c *Client) loadTreeFolders(ctx context.Context, pickCode string) ([]bulkFolder, error) {
	folders := make([]bulkFolder, 0)
	for page := int64(1); ; page++ {
		var items []bulkFolder
		var more bool
		err := c.waitAndCall(ctx, c.bulkRate, func() error {
			var err error
			items, more, err = c.sdk.ListTreeFolders(pickCode, page, bulkFolderPageSize)
			return err
		})
		if err != nil {
			return nil, mapError(err)
		}
		folders = append(folders, items...)
		if !more {
			return folders, nil
		}
	}
}

func (c *Client) loadTreeFiles(ctx context.Context, rootID string, maxEntries int) ([]pan115sdk.File, bool, error) {
	if maxEntries <= 0 {
		maxEntries = 250000
	}
	files := make([]pan115sdk.File, 0, min(maxEntries, int(bulkTreePageSize)))
	for offset := int64(0); ; {
		var page []pan115sdk.File
		var total int64
		err := c.waitAndCall(ctx, c.bulkRate, func() error {
			var err error
			page, total, err = c.sdk.ListTreeFiles(rootID, offset, bulkTreePageSize)
			return err
		})
		if err != nil {
			return nil, false, mapError(err)
		}
		if len(page) == 0 && offset < total {
			return nil, false, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 bulk file page made no progress"))
		}
		remaining := maxEntries - len(files)
		if len(page) > remaining {
			page = page[:remaining]
		}
		files = append(files, page...)
		if len(files) >= maxEntries {
			return files, offset+int64(len(page)) < total, nil
		}
		offset += int64(len(page))
		if offset >= total || len(page) == 0 {
			return files, false, nil
		}
	}
}

func buildTreeEntries(rootID string, folders []bulkFolder, files []pan115sdk.File) ([]cloud.TreeEntry, error) {
	nodes := map[string]bulkFolder{rootID: {ID: rootID}}
	for _, folder := range folders {
		if folder.ID == "" || folder.ParentID == "" || folder.Name == "" || strings.ContainsAny(folder.Name, "\x00\r\n/") {
			return nil, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned an invalid bulk folder"))
		}
		nodes[folder.ID] = folder
	}
	paths := map[string]string{rootID: ""}
	var folderPath func(string, map[string]struct{}) (string, error)
	folderPath = func(id string, visiting map[string]struct{}) (string, error) {
		if path, ok := paths[id]; ok {
			return path, nil
		}
		if _, cycle := visiting[id]; cycle {
			return "", errors.New("115 bulk folder cycle")
		}
		node, ok := nodes[id]
		if !ok {
			return "", errors.New("115 bulk folder parent is missing")
		}
		visiting[id] = struct{}{}
		parent, err := folderPath(node.ParentID, visiting)
		delete(visiting, id)
		if err != nil {
			return "", err
		}
		path := parent + "/" + node.Name
		paths[id] = path
		return path, nil
	}
	entries := make([]cloud.TreeEntry, 0, len(files))
	for _, file := range files {
		item, err := mapFile(file)
		if err != nil || item.IsDir {
			return nil, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned an invalid bulk file"))
		}
		parent, err := folderPath(item.ParentID, map[string]struct{}{})
		if err != nil {
			return nil, cloud.Error(cloud.CodeResponseInvalid, true, err)
		}
		entries = append(entries, cloud.TreeEntry{Item: item, RelativePath: parent + "/" + item.Name})
	}
	return entries, nil
}

func (c *Client) Stat(ctx context.Context, itemID string) (cloud.Item, error) {
	itemID = normalizeID(itemID)
	if itemID == "0" {
		return cloud.Item{ID: "0", ParentID: "", Name: "115 网盘", IsDir: true}, nil
	}
	var file *pan115sdk.File
	err := c.waitAndCall(ctx, c.listRate, func() error {
		var err error
		file, err = c.sdk.GetFile(itemID)
		return err
	})
	if err != nil {
		return cloud.Item{}, mapError(err)
	}
	if file == nil {
		return cloud.Item{}, cloud.Error(cloud.CodeNotFound, false, errors.New("115 item was not found"))
	}
	return mapFile(*file)
}

func (c *Client) DirectURL(ctx context.Context, request cloud.DirectURLRequest) (cloud.TemporaryURL, error) {
	request.UserAgent = strings.TrimSpace(request.UserAgent)
	pickCode := strings.TrimSpace(request.PickCode)
	if pickCode == "" {
		item, err := c.Stat(ctx, request.FileID)
		if err != nil {
			return cloud.TemporaryURL{}, err
		}
		if item.IsDir || item.PickCode == "" {
			return cloud.TemporaryURL{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 item has no download identity"))
		}
		pickCode = item.PickCode
	}
	var info *pan115sdk.DownloadInfo
	err := c.waitAndCall(ctx, c.directRate, func() error {
		var err error
		info, err = c.sdk.DownloadWithUA(pickCode, request.UserAgent)
		return err
	})
	if err != nil {
		return cloud.TemporaryURL{}, mapError(err)
	}
	if info == nil || !info.Url.Valid || strings.TrimSpace(info.Url.Url) == "" {
		return cloud.TemporaryURL{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned no direct URL"))
	}
	parsed, err := url.Parse(info.Url.Url)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return cloud.TemporaryURL{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 returned an invalid direct URL"))
	}
	// 115driver v1.3.5 returns the headers of the Cookie-authenticated API
	// request that acquired the URL (including Cookie and Content-Type). Those
	// are not CDN playback requirements and exposing them through TemporaryURL
	// makes a safe 302 impossible. 115 playback URLs are bound only to the
	// acquisition User-Agent, which the redirected client already sends again.
	// Keep that semantic requirement explicit and discard the SDK's acquisition
	// headers at the provider boundary.
	headers := make(http.Header)
	if request.UserAgent != "" {
		headers.Set("User-Agent", request.UserAgent)
	}
	return cloud.TemporaryURL{URL: parsed.String(), Headers: headers, ExpiresAt: directURLExpiry(parsed, time.Now().UTC())}, nil
}

func newDownloadHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.DialContext = publicDownloadDialContext(net.DefaultResolver.LookupIPAddr)
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(request *http.Request, previous []*http.Request) error {
			if len(previous) >= 3 {
				return errors.New("115 download redirected too many times")
			}
			if err := validatePublicDownloadURL(request.Context(), request.URL, net.DefaultResolver.LookupIPAddr); err != nil {
				return err
			}
			for key := range request.Header {
				if !strings.EqualFold(key, "User-Agent") && !strings.EqualFold(key, "Range") && !strings.EqualFold(key, "Accept-Encoding") {
					request.Header.Del(key)
				}
			}
			return nil
		},
	}
}

// OpenRead keeps the expiring CDN URL and its provider-required headers inside
// the 115 adapter. The caller receives only a stream and safe size/range facts.
func (c *Client) OpenRead(ctx context.Context, request cloud.ReadRequest) (cloud.ReadResult, error) {
	request.FileID = strings.TrimSpace(request.FileID)
	if request.FileID == "" || request.Offset < 0 {
		return cloud.ReadResult{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 read request is invalid"))
	}
	item, err := c.Stat(ctx, request.FileID)
	if err != nil {
		return cloud.ReadResult{}, err
	}
	if item.IsDir || item.Size < 0 || request.Offset > item.Size {
		return cloud.ReadResult{}, cloud.Error(cloud.CodeResponseInvalid, false, errors.New("115 read source is invalid"))
	}
	temporary, err := c.DirectURL(ctx, cloud.DirectURLRequest{FileID: item.ID, PickCode: item.PickCode, UserAgent: pan115sdk.UA115Browser})
	if err != nil {
		return cloud.ReadResult{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, temporary.URL, nil)
	if err != nil {
		return cloud.ReadResult{}, cloud.Error(cloud.CodeResponseInvalid, false, err)
	}
	if err := validatePublicDownloadURL(ctx, httpRequest.URL, net.DefaultResolver.LookupIPAddr); err != nil {
		return cloud.ReadResult{}, cloud.Error(cloud.CodeResponseInvalid, false, err)
	}
	// 115 download URLs are bound only to the acquisition User-Agent. Never
	// forward API cookies, Authorization or other acquisition headers to a CDN,
	// even if a future SDK version puts them back into TemporaryURL.Headers.
	if userAgent := strings.TrimSpace(temporary.Headers.Get("User-Agent")); userAgent != "" {
		httpRequest.Header.Set("User-Agent", userAgent)
	}
	httpRequest.Header.Set("Accept-Encoding", "identity")
	if request.Offset > 0 {
		httpRequest.Header.Set("Range", fmt.Sprintf("bytes=%d-", request.Offset))
	}
	client := c.downloadHTTP
	if client == nil {
		client = newDownloadHTTPClient()
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return cloud.ReadResult{}, cloud.Error(cloud.CodeUnavailable, true, err)
	}
	accepted := request.Offset == 0
	if request.Offset > 0 {
		accepted = response.StatusCode == http.StatusPartialContent && validContentRangeStart(response.Header.Get("Content-Range"), request.Offset)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		_ = response.Body.Close()
		return cloud.ReadResult{}, cloud.Error(cloud.CodeUnavailable, response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500, fmt.Errorf("115 download returned HTTP %d", response.StatusCode))
	}
	if response.StatusCode == http.StatusPartialContent && !validContentRangeStart(response.Header.Get("Content-Range"), request.Offset) {
		_ = response.Body.Close()
		return cloud.ReadResult{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 download returned an invalid content range"))
	}
	total := item.Size
	return cloud.ReadResult{Body: response.Body, OffsetAccepted: accepted, TotalSize: &total}, nil
}

func validContentRangeStart(value string, offset int64) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "bytes ") {
		return false
	}
	rangePart := strings.TrimPrefix(value, "bytes ")
	dash := strings.IndexByte(rangePart, '-')
	if dash <= 0 {
		return false
	}
	start, err := strconv.ParseInt(rangePart[:dash], 10, 64)
	return err == nil && start == offset
}

func (c *Client) waitAndCall(ctx context.Context, limiter *rate.Limiter, call func() error) error {
	waitStarted := time.Now()
	waitRecorded := false
	defer func() {
		if !waitRecorded {
			cloud.RecordProviderWait(ctx, time.Since(waitStarted))
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.waitForRecovery(ctx); err != nil {
		return err
	}
	if err := limiter.Wait(ctx); err != nil {
		return err
	}
	if c.callSlots == nil {
		return errors.New("115 client is not initialized")
	}
	select {
	case c.callSlots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-c.callSlots
		return err
	}
	cloud.RecordProviderWait(ctx, time.Since(waitStarted))
	waitRecorded = true
	done := make(chan error, 1)
	go func() {
		defer func() { <-c.callSlots }()
		callStarted := time.Now()
		err := call()
		cloud.RecordProviderCall(ctx, time.Since(callStarted))
		// Record the provider outcome in the calling generation even when the
		// caller's context is cancelled while the context-less SDK call is still
		// in flight. Otherwise a late 405/429 would be silently dropped and the
		// next operation could bypass the shared account recovery state.
		c.recordOutcome(err)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) waitForRecovery(ctx context.Context) error {
	c.stateMu.Lock()
	now := c.now()
	if c.circuitTil.After(now) {
		c.stateMu.Unlock()
		return errCircuitOpen
	}
	until := c.backoffTil
	c.stateMu.Unlock()
	if !until.After(now) {
		return nil
	}
	timer := time.NewTimer(until.Sub(now))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) recordOutcome(err error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if err == nil {
		// Calls already in flight when another endpoint reports risk can finish
		// successfully afterwards. Such a late success must not erase the shared
		// account backoff/circuit before its bounded recovery window has elapsed.
		now := c.now()
		if c.backoffTil.After(now) || c.circuitTil.After(now) {
			return
		}
		c.riskFails = 0
		c.backoffTil = time.Time{}
		c.circuitTil = time.Time{}
		return
	}
	if !isRiskResponse(err) {
		return
	}
	c.riskFails++
	delay := 2 * time.Second
	for attempt := 1; attempt < c.riskFails && delay < 2*time.Minute; attempt++ {
		delay *= 2
	}
	if delay > 2*time.Minute {
		delay = 2 * time.Minute
	}
	delay += c.jitter()
	c.backoffTil = c.now().Add(delay)
	if c.riskFails >= 3 {
		c.circuitTil = c.now().Add(5 * time.Minute)
	}
}

func defaultJitter() time.Duration {
	return time.Duration(time.Now().UnixNano() % int64(time.Second))
}

func isRiskResponse(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "429") || strings.Contains(message, "405") || strings.Contains(message, "rate") || strings.Contains(message, "频繁") || strings.Contains(message, "风控")
}

func mapFile(file pan115sdk.File) (cloud.Item, error) {
	id := strings.TrimSpace(file.FileID)
	if id == "" || strings.ContainsAny(id, "\x00\r\n/") || strings.ContainsAny(file.ParentID, "\x00\r\n/") || strings.ContainsAny(file.Name, "\x00\r\n/") {
		return cloud.Item{}, cloud.Error(cloud.CodeResponseInvalid, true, errors.New("115 returned an invalid item"))
	}
	return cloud.Item{ID: id, ParentID: strings.TrimSpace(file.ParentID), Name: strings.TrimSpace(file.Name), IsDir: file.IsDirectory, Size: file.Size, SHA1: strings.TrimSpace(file.Sha1), PickCode: strings.TrimSpace(file.PickCode), CreatedAt: file.CreateTime, ModifiedAt: file.UpdateTime}, nil
}

func normalizeID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "0"
	}
	return value
}

func directURLExpiry(parsed *url.URL, now time.Time) time.Time {
	for _, key := range []string{"expires", "expire", "exp", "t"} {
		value, err := strconv.ParseInt(parsed.Query().Get(key), 10, 64)
		if err == nil && value > now.Unix()+30 && value < now.Add(24*time.Hour).Unix() {
			return time.Unix(value, 0).UTC()
		}
	}
	return now.Add(4 * time.Minute)
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return cloud.Error(cloud.CodeUnavailable, true, err)
	}
	if errors.Is(err, errCircuitOpen) {
		return cloud.Error(cloud.CodeRateLimited, true, err)
	}
	if errors.Is(err, pan115sdk.ErrBadCookie) || errors.Is(err, pan115sdk.ErrNotLogin) {
		return cloud.Error(cloud.CodeAuthExpired, false, err)
	}
	if errors.Is(err, pan115sdk.ErrOfflineNoTimes) {
		return cloud.Error(cloud.CodeOfflineNoQuota, false, err)
	}
	if errors.Is(err, pan115sdk.ErrOfflineInvalidLink) {
		return cloud.Error(cloud.CodeOfflineBadLink, false, err)
	}
	if errors.Is(err, pan115sdk.ErrOfflineTaskExisted) {
		return cloud.Error(cloud.CodeOfflineTaskExists, false, err)
	}
	if errors.Is(err, pan115sdk.ErrSharedInvalid) || errors.Is(err, pan115sdk.ErrSharedNotFound) {
		return cloud.Error(cloud.CodeShareInvalid, false, err)
	}
	message := strings.ToLower(err.Error())
	if isRiskResponse(err) {
		return cloud.Error(cloud.CodeRateLimited, true, err)
	}
	if strings.Contains(message, "not found") || strings.Contains(message, "不存在") {
		return cloud.Error(cloud.CodeNotFound, false, err)
	}
	return cloud.Error(cloud.CodeUnavailable, true, err)
}
