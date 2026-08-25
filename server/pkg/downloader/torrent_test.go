package downloader

import (
	"crypto/sha1"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestTorrentMagnetHashesRawInfoAndPreservesBoundedTrackers(t *testing.T) {
	trackerA := "https://tracker.example/a"
	trackerB := "udp://tracker.example:6969/announce"
	info := "d6:lengthi123e4:name9:movie.mkve"
	raw := []byte("d8:announce" + bencodeString(trackerA) + "13:announce-listll" + bencodeString(trackerA) + "el" + bencodeString(trackerB) + "ee4:info" + info + "e")
	magnet, err := TorrentMagnet(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(magnet)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha1.Sum([]byte(info))
	wantXT := "urn:btih:" + hex.EncodeToString(digest[:])
	if parsed.Query().Get("xt") != wantXT {
		t.Fatalf("xt=%q want=%q", parsed.Query().Get("xt"), wantXT)
	}
	trackers := parsed.Query()["tr"]
	if len(trackers) != 2 || trackers[0] != "https://tracker.example/a" || trackers[1] != "udp://tracker.example:6969/announce" {
		t.Fatalf("trackers=%v", trackers)
	}
}

func TestTorrentMagnetSupportsPrivateTrackerPasskeyWithoutLoggingOrReencoding(t *testing.T) {
	tracker := "https://pt.example/announce?passkey=server-only-secret"
	info := "d7:privatei1e4:name8:show.mkve"
	raw := []byte("d8:announce" + strconv.Itoa(len(tracker)) + ":" + tracker + "4:info" + info + "e")
	magnet, err := TorrentMagnet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(magnet, "passkey%3Dserver-only-secret") {
		t.Fatal("private tracker missing from generated magnet")
	}
}

func bencodeString(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}

func TestTorrentMagnetRejectsMalformedDuplicateAndDeepPayloads(t *testing.T) {
	cases := [][]byte{
		[]byte("d4:infoi1ee"),
		[]byte("d4:infode4:infode"),
		[]byte("d4:infod4:name99:shortee"),
		[]byte("d4:info" + strings.Repeat("l", maxBencodeDepth+1) + strings.Repeat("e", maxBencodeDepth+1) + "e"),
	}
	for _, raw := range cases {
		if _, err := TorrentMagnet(raw); err == nil {
			t.Fatalf("expected malformed torrent to fail: %q", raw)
		}
	}
}
