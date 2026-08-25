package downloader

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
)

const (
	MaxTorrentBytes       = 4 << 20
	maxBencodeDepth       = 64
	maxBencodeValues      = 200000
	maxTorrentTrackers    = 64
	maxTorrentTrackerSize = 2048
)

// TorrentMagnet converts a bounded .torrent payload to a BTIH magnet without
// re-encoding the info dictionary. The SHA-1 digest must cover the exact raw
// bencoded bytes or otherwise valid torrents can receive a different identity.
func TorrentMagnet(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > MaxTorrentBytes {
		return "", errors.New("torrent payload is empty or too large")
	}
	p := torrentBencodeParser{raw: raw}
	infoStart, infoEnd, trackers, err := p.parseTorrent()
	if err != nil {
		return "", err
	}
	digest := sha1.Sum(raw[infoStart:infoEnd])
	query := url.Values{}
	query.Set("xt", "urn:btih:"+hex.EncodeToString(digest[:]))
	for _, tracker := range trackers {
		query.Add("tr", tracker)
	}
	return "magnet:?" + query.Encode(), nil
}

type torrentBencodeParser struct {
	raw    []byte
	values int
}

func (p *torrentBencodeParser) parseTorrent() (int, int, []string, error) {
	if len(p.raw) < 2 || p.raw[0] != 'd' {
		return 0, 0, nil, errors.New("torrent root must be a dictionary")
	}
	pos := 1
	infoStart, infoEnd := -1, -1
	trackers := make([]string, 0, 4)
	seenTrackers := map[string]struct{}{}
	for {
		if pos >= len(p.raw) {
			return 0, 0, nil, errors.New("unterminated torrent dictionary")
		}
		if p.raw[pos] == 'e' {
			pos++
			break
		}
		key, next, err := p.parseString(pos)
		if err != nil {
			return 0, 0, nil, err
		}
		pos = next
		valueStart := pos
		switch string(key) {
		case "info":
			if infoStart >= 0 {
				return 0, 0, nil, errors.New("torrent contains duplicate info dictionaries")
			}
			pos, err = p.skipValue(pos, 1)
			if err == nil && p.raw[valueStart] != 'd' {
				err = errors.New("torrent info value must be a dictionary")
			}
			infoStart, infoEnd = valueStart, pos
		case "announce":
			var value []byte
			value, pos, err = p.parseString(pos)
			if err == nil {
				addTorrentTracker(&trackers, seenTrackers, string(value))
			}
		case "announce-list":
			pos, err = p.collectTrackerList(pos, 1, &trackers, seenTrackers)
		default:
			pos, err = p.skipValue(pos, 1)
		}
		if err != nil {
			return 0, 0, nil, err
		}
	}
	if pos != len(p.raw) {
		return 0, 0, nil, errors.New("torrent has trailing data")
	}
	if infoStart < 0 || infoEnd <= infoStart {
		return 0, 0, nil, errors.New("torrent info dictionary is missing")
	}
	return infoStart, infoEnd, trackers, nil
}

func (p *torrentBencodeParser) collectTrackerList(pos, depth int, trackers *[]string, seen map[string]struct{}) (int, error) {
	if depth > maxBencodeDepth || pos >= len(p.raw) || p.raw[pos] != 'l' {
		return 0, errors.New("torrent announce-list is invalid")
	}
	pos++
	for {
		if pos >= len(p.raw) {
			return 0, errors.New("unterminated torrent announce-list")
		}
		if p.raw[pos] == 'e' {
			return pos + 1, nil
		}
		if p.raw[pos] == 'l' {
			var err error
			pos, err = p.collectTrackerList(pos, depth+1, trackers, seen)
			if err != nil {
				return 0, err
			}
			continue
		}
		value, next, err := p.parseString(pos)
		if err != nil {
			return 0, errors.New("torrent announce-list contains a non-string value")
		}
		addTorrentTracker(trackers, seen, string(value))
		pos = next
	}
}

func addTorrentTracker(trackers *[]string, seen map[string]struct{}, raw string) {
	tracker := strings.TrimSpace(raw)
	if tracker == "" || len(tracker) > maxTorrentTrackerSize || len(*trackers) >= maxTorrentTrackers {
		return
	}
	parsed, err := url.Parse(tracker)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "udp", "ws", "wss":
	default:
		return
	}
	if _, exists := seen[tracker]; exists {
		return
	}
	seen[tracker] = struct{}{}
	*trackers = append(*trackers, tracker)
}

func (p *torrentBencodeParser) skipValue(pos, depth int) (int, error) {
	if depth > maxBencodeDepth || pos >= len(p.raw) {
		return 0, errors.New("torrent bencode nesting is invalid")
	}
	p.values++
	if p.values > maxBencodeValues {
		return 0, errors.New("torrent bencode contains too many values")
	}
	switch p.raw[pos] {
	case 'i':
		end := pos + 1
		for end < len(p.raw) && p.raw[end] != 'e' {
			end++
		}
		if end >= len(p.raw) || !validBencodeInteger(p.raw[pos+1:end]) {
			return 0, errors.New("torrent contains an invalid integer")
		}
		return end + 1, nil
	case 'l':
		pos++
		for {
			if pos >= len(p.raw) {
				return 0, errors.New("torrent contains an unterminated list")
			}
			if p.raw[pos] == 'e' {
				return pos + 1, nil
			}
			var err error
			pos, err = p.skipValue(pos, depth+1)
			if err != nil {
				return 0, err
			}
		}
	case 'd':
		pos++
		for {
			if pos >= len(p.raw) {
				return 0, errors.New("torrent contains an unterminated dictionary")
			}
			if p.raw[pos] == 'e' {
				return pos + 1, nil
			}
			_, next, err := p.parseString(pos)
			if err != nil {
				return 0, err
			}
			pos, err = p.skipValue(next, depth+1)
			if err != nil {
				return 0, err
			}
		}
	default:
		_, next, err := p.parseString(pos)
		return next, err
	}
}

func (p *torrentBencodeParser) parseString(pos int) ([]byte, int, error) {
	if pos >= len(p.raw) || p.raw[pos] < '0' || p.raw[pos] > '9' {
		return nil, 0, errors.New("torrent contains an invalid byte string")
	}
	colon := pos
	for colon < len(p.raw) && p.raw[colon] >= '0' && p.raw[colon] <= '9' {
		colon++
	}
	if colon >= len(p.raw) || p.raw[colon] != ':' || (colon-pos > 1 && p.raw[pos] == '0') {
		return nil, 0, errors.New("torrent contains an invalid byte-string length")
	}
	length, err := strconv.ParseUint(string(p.raw[pos:colon]), 10, 32)
	if err != nil || length > uint64(len(p.raw)) {
		return nil, 0, errors.New("torrent byte-string length is invalid")
	}
	start := colon + 1
	end64 := uint64(start) + length
	if end64 > uint64(len(p.raw)) {
		return nil, 0, errors.New("torrent byte string exceeds payload")
	}
	end := int(end64)
	return p.raw[start:end], end, nil
}

func validBencodeInteger(raw []byte) bool {
	if len(raw) == 0 || (len(raw) > 1 && raw[0] == '0') || (len(raw) > 1 && raw[0] == '-' && raw[1] == '0') {
		return false
	}
	for i, value := range raw {
		if i == 0 && value == '-' {
			if len(raw) == 1 {
				return false
			}
			continue
		}
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}
