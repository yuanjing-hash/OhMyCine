package pan115

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
	"golang.org/x/time/rate"
)

type uploadTestSDK struct {
	*bulkSDK
	pages        map[int64][]pan115sdk.File
	uploadCalls  int
	uploadParent string
	uploadName   string
	uploadSize   int64
	uploadBody   []byte
	uploadErr    error
}

func (s *uploadTestSDK) UploadFastOrByMultipart(parentID, name string, size int64, file *os.File, _ ...pan115sdk.UploadMultipartOption) error {
	s.uploadCalls++
	s.uploadParent, s.uploadName, s.uploadSize = parentID, name, size
	body, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	s.uploadBody = append([]byte(nil), body...)
	return s.uploadErr
}

func (s *uploadTestSDK) ListPage(_ string, offset, _ int64, _ ...pan115sdk.ListOption) (*[]pan115sdk.File, error) {
	s.listPageCalls++
	items := append([]pan115sdk.File(nil), s.pages[offset]...)
	return &items, nil
}

func newUploadTestClient(sdk sdkClient) *Client {
	return &Client{
		sdk: sdk, listRate: rate.NewLimiter(rate.Inf, 1), uploadRate: rate.NewLimiter(rate.Inf, 1),
		callSlots: make(chan struct{}, maxInFlightCalls), now: time.Now, jitter: func() time.Duration { return 0 },
	}
}

func managedUploadFile(t *testing.T, body []byte) *os.File {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "upload.bin"
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func TestUploadUsesManagedFileAndReconcilesAcrossPages(t *testing.T) {
	body := []byte("managed upload")
	firstPage := make([]pan115sdk.File, maxPageSize)
	for index := range firstPage {
		firstPage[index] = pan115sdk.File{FileID: "filler", ParentID: "parent", Name: "other.bin", Size: 1}
	}
	sdk := &uploadTestSDK{bulkSDK: &bulkSDK{}, pages: map[int64][]pan115sdk.File{
		0:           firstPage,
		maxPageSize: {{FileID: "uploaded", ParentID: "parent", Name: "video.mkv", Size: int64(len(body))}},
	}}
	client := newUploadTestClient(sdk)
	if !client.Capabilities().FileUpload {
		t.Fatal("115 did not advertise file upload")
	}
	item, err := client.Upload(context.Background(), cloud.UploadRequest{ParentID: "parent", Name: "video.mkv", Size: int64(len(body)), Reader: managedUploadFile(t, body)})
	if err != nil {
		t.Fatal(err)
	}
	if item.ID != "uploaded" || item.ParentID != "parent" || item.Name != "video.mkv" || item.Size != int64(len(body)) {
		t.Fatalf("item=%+v", item)
	}
	if sdk.uploadCalls != 1 || sdk.uploadParent != "parent" || sdk.uploadName != "video.mkv" || sdk.uploadSize != int64(len(body)) || !bytes.Equal(sdk.uploadBody, body) || sdk.listPageCalls != 2 {
		t.Fatalf("sdk=%+v", sdk)
	}
}

func TestUploadRejectsNonFileReaderAndChangedFileBeforeProviderCall(t *testing.T) {
	sdk := &uploadTestSDK{bulkSDK: &bulkSDK{}, pages: map[int64][]pan115sdk.File{}}
	client := newUploadTestClient(sdk)
	if _, err := client.Upload(context.Background(), cloud.UploadRequest{ParentID: "parent", Name: "video.mkv", Size: 5, Reader: bytes.NewReader([]byte("video"))}); err == nil {
		t.Fatal("non-file reader was accepted")
	} else if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeResponseInvalid || retryable {
		t.Fatalf("non-file code=%q retryable=%t err=%v", code, retryable, err)
	}
	if _, err := client.Upload(context.Background(), cloud.UploadRequest{ParentID: "parent", Name: "video.mkv", Size: 6, Reader: managedUploadFile(t, []byte("video"))}); err == nil {
		t.Fatal("changed file size was accepted")
	} else if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeResponseInvalid || retryable {
		t.Fatalf("changed-file code=%q retryable=%t err=%v", code, retryable, err)
	}
	if sdk.uploadCalls != 0 {
		t.Fatalf("invalid request reached provider: %d", sdk.uploadCalls)
	}
}

func TestUploadFailsClosedWhenProviderResultIsMissingOrAmbiguous(t *testing.T) {
	body := []byte("managed upload")
	tests := []struct {
		name  string
		files []pan115sdk.File
	}{
		{name: "missing"},
		{name: "ambiguous", files: []pan115sdk.File{
			{FileID: "one", ParentID: "parent", Name: "video.mkv", Size: int64(len(body))},
			{FileID: "two", ParentID: "parent", Name: "video.mkv", Size: int64(len(body))},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sdk := &uploadTestSDK{bulkSDK: &bulkSDK{}, pages: map[int64][]pan115sdk.File{0: test.files}}
			_, err := newUploadTestClient(sdk).Upload(context.Background(), cloud.UploadRequest{ParentID: "parent", Name: "video.mkv", Size: int64(len(body)), Reader: managedUploadFile(t, body)})
			if err == nil {
				t.Fatal("ambiguous provider result was accepted")
			}
			if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeMutationUnknown || !retryable {
				t.Fatalf("code=%q retryable=%t err=%v", code, retryable, err)
			}
			if sdk.uploadCalls != 1 {
				t.Fatalf("upload calls=%d", sdk.uploadCalls)
			}
		})
	}
}

func TestUploadMapsProviderFailureWithoutAttemptingReconciliation(t *testing.T) {
	body := []byte("managed upload")
	sdk := &uploadTestSDK{bulkSDK: &bulkSDK{}, pages: map[int64][]pan115sdk.File{}, uploadErr: errors.New("HTTP 429 rate limited")}
	_, err := newUploadTestClient(sdk).Upload(context.Background(), cloud.UploadRequest{ParentID: "parent", Name: "video.mkv", Size: int64(len(body)), Reader: managedUploadFile(t, body)})
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeRateLimited || !retryable {
		t.Fatalf("code=%q retryable=%t err=%v", code, retryable, err)
	}
	if sdk.uploadCalls != 1 || sdk.listPageCalls != 0 {
		t.Fatalf("upload=%d list=%d", sdk.uploadCalls, sdk.listPageCalls)
	}
}

var _ uploadSDK = (*uploadTestSDK)(nil)
