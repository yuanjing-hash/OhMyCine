package pan115

import (
	"context"
	"errors"
	"reflect"
	"testing"

	pan115sdk "github.com/SheltonZhu/115driver/pkg/driver"
	"github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

type shareTestSDK struct {
	*bulkSDK
	snapshot       *pan115sdk.ShareSnapResp
	snapshotErr    error
	receivedCode   string
	receivedSecret string
	receivedIDs    []string
	receivedParent string
}

func (s *shareTestSDK) GetShareSnapWithUA(string, string, string, string, ...pan115sdk.Query) (*pan115sdk.ShareSnapResp, error) {
	return s.snapshot, s.snapshotErr
}

func (s *shareTestSDK) ReceiveShare(code, secret string, ids []string, parent string) error {
	s.receivedCode = code
	s.receivedSecret = secret
	s.receivedIDs = append([]string(nil), ids...)
	s.receivedParent = parent
	return nil
}

func shareSnapshot(count int, files ...pan115sdk.ShareFile) *pan115sdk.ShareSnapResp {
	response := &pan115sdk.ShareSnapResp{}
	response.Data.Count = count
	response.Data.List = files
	response.Data.Shareinfo.ShareTitle = "Seven Samurai"
	return response
}

func TestParseShareLinkAcceptsAllowlistedHostsAndQuerySecrets(t *testing.T) {
	for _, raw := range []string{
		"https://115.com/s/share-code?password=abcd",
		"https://share.115.com/s/share_code?pwd=1234",
		"https://115cdn.com/s/share-code?receive_code=xy-z",
	} {
		code, secret, err := parseShareLink(raw)
		if err != nil || code == "" || secret == "" {
			t.Fatalf("parseShareLink(%q) code=%q secret=%q err=%v", raw, code, secret, err)
		}
	}
	for _, raw := range []string{
		"http://115.com/s/share-code?password=abcd",
		"https://115.com.evil.invalid/s/share-code?password=abcd",
		"https://115.com/s/share-code",
		"https://115.com/other/share-code?password=abcd",
	} {
		if _, _, err := parseShareLink(raw); err == nil {
			t.Fatalf("parseShareLink accepted %q", raw)
		}
	}
}

func TestInspectAndReceiveShareKeepProviderSecretsInsideDriver(t *testing.T) {
	sdk := &shareTestSDK{bulkSDK: &bulkSDK{}, snapshot: shareSnapshot(2,
		pan115sdk.ShareFile{FileID: "movie", FileName: "Seven.Samurai.1954.mkv", IsFile: 1},
		pan115sdk.ShareFile{FileID: "subtitle", FileName: "Seven.Samurai.1954.ass", IsFile: 1},
	)}
	client := newOfflineTestClient(sdk)
	snapshot, err := client.InspectShare(context.Background(), "https://115.com/s/share-code?password=abcd")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ShareCode != "share-code" || snapshot.ReceiveCode != "abcd" || snapshot.Title != "Seven Samurai" || len(snapshot.Items) != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	if err := client.ReceiveShare(context.Background(), snapshot, "intake-task-root"); err != nil {
		t.Fatal(err)
	}
	if sdk.receivedCode != "share-code" || sdk.receivedSecret != "abcd" || sdk.receivedParent != "intake-task-root" || !reflect.DeepEqual(sdk.receivedIDs, []string{"movie", "subtitle"}) {
		t.Fatalf("receive call code=%q secret=%q parent=%q ids=%v", sdk.receivedCode, sdk.receivedSecret, sdk.receivedParent, sdk.receivedIDs)
	}
}

func TestInspectShareRejectsOversizedProviderCountBeforePartialReceive(t *testing.T) {
	sdk := &shareTestSDK{bulkSDK: &bulkSDK{}, snapshot: shareSnapshot(maxShareTopLevelItems+1,
		pan115sdk.ShareFile{FileID: "first", FileName: "first.mkv", IsFile: 1},
	)}
	_, err := newOfflineTestClient(sdk).InspectShare(context.Background(), "https://115.com/s/share-code?password=abcd")
	if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeShareTooLarge || retryable {
		t.Fatalf("error code=%q retryable=%t err=%v", code, retryable, err)
	}
}

func TestShareSnapshotRejectsDuplicateOrDelimitedProviderIdentities(t *testing.T) {
	for _, files := range [][]pan115sdk.ShareFile{
		{{FileID: "duplicate", FileName: "one.mkv", IsFile: 1}, {FileID: "duplicate", FileName: "two.mkv", IsFile: 1}},
		{{FileID: "first,second", FileName: "movie.mkv", IsFile: 1}},
	} {
		sdk := &shareTestSDK{bulkSDK: &bulkSDK{}, snapshot: shareSnapshot(len(files), files...)}
		_, err := newOfflineTestClient(sdk).InspectShare(context.Background(), "https://115.com/s/share-code?password=abcd")
		if code, retryable := cloud.ErrorInfo(err); code != cloud.CodeResponseInvalid || retryable {
			t.Fatalf("files=%+v code=%q retryable=%t err=%v", files, code, retryable, err)
		}
	}
}

func TestInspectShareMapsInvalidProviderResponseWithoutLeakingInput(t *testing.T) {
	sdk := &shareTestSDK{bulkSDK: &bulkSDK{}, snapshotErr: errors.New("provider rejected share")}
	raw := "https://115.com/s/private-share?password=private-code"
	_, err := newOfflineTestClient(sdk).InspectShare(context.Background(), raw)
	if err == nil || err.Error() == raw {
		t.Fatalf("unexpected error=%v", err)
	}
}
