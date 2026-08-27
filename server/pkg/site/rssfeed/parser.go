package rssfeed

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
)

var magnetPattern = regexp.MustCompile(`(?i)magnet:\?[^\s<"']+`)

type Item struct {
	Title, Subtitle string
	SizeBytes       int64
	Published       *time.Time
	Seeders         *int
	Leechers        *int
	Completed       *int
	Sources         []string
}

type document struct {
	Channel struct {
		Items []item `xml:"item"`
	} `xml:"channel"`
}

type item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	Size        string `xml:"size"`
	Seeders     string `xml:"seeders"`
	Leechers    string `xml:"leechers"`
	Downloads   string `xml:"downloads"`
	MagnetURL   string `xml:"magneturl"`
	DownloadURL string `xml:"downloadurl"`
	TorrentURL  string `xml:"torrent_url"`
	MagnetURI   string `xml:"magnet_uri"`
	Enclosure   struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
		Size string `xml:"length,attr"`
	} `xml:"enclosure"`
	Attrs []struct {
		Name  string `xml:"name,attr"`
		Value string `xml:"value,attr"`
	} `xml:"attr"`
}

func Parse(body []byte) ([]Item, error) {
	var root document
	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.Strict = true
	if err := decoder.Decode(&root); err != nil {
		return nil, site.ErrInvalidReply
	}
	items := make([]Item, 0, len(root.Channel.Items))
	for _, raw := range root.Channel.Items {
		title := clean(raw.Title, 512)
		if title == "" {
			continue
		}
		attrs := map[string]string{}
		for _, attr := range raw.Attrs {
			attrs[strings.ToLower(strings.TrimSpace(attr.Name))] = strings.TrimSpace(attr.Value)
		}
		subtitle := clean(raw.Description, 1024)
		lowerSubtitle := strings.ToLower(subtitle)
		if strings.Contains(lowerSubtitle, "magnet:") || strings.Contains(lowerSubtitle, "http://") || strings.Contains(lowerSubtitle, "https://") {
			subtitle = ""
		}
		result := Item{Title: title, Subtitle: subtitle}
		result.SizeBytes = parseSize(first(raw.Size, raw.Enclosure.Size, attrs["size"]))
		result.Seeders = parseIntPtr(first(raw.Seeders, attrs["seeders"]))
		result.Leechers = parseIntPtr(first(raw.Leechers, attrs["peers"], attrs["leechers"]))
		result.Completed = parseIntPtr(first(raw.Downloads, attrs["grabs"], attrs["downloads"]))
		result.Published = parseTime(first(raw.PubDate, attrs["date"]))
		result.Sources = uniqueSources(raw.Enclosure.URL, raw.MagnetURL, raw.DownloadURL, raw.TorrentURL, raw.MagnetURI, attrs["magneturl"], attrs["downloadurl"], attrs["torrent_url"], attrs["magnet_uri"], raw.Link, raw.GUID, raw.Description)
		items = append(items, result)
	}
	return items, nil
}

func uniqueSources(values ...string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		candidates := []string{strings.TrimSpace(value)}
		candidates = append(candidates, magnetPattern.FindAllString(value, 2)...)
		for _, candidate := range candidates {
			candidate = strings.TrimSpace(strings.ReplaceAll(candidate, "&amp;", "&"))
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}

func clean(value string, max int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if value == "" || len([]rune(value)) > max || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	return value
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseIntPtr(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}

func parseSize(value string) int64 {
	value = strings.TrimSpace(value)
	if raw, err := strconv.ParseInt(value, 10, 64); err == nil && raw >= 0 {
		return raw
	}
	fields := strings.Fields(strings.ToUpper(value))
	if len(fields) != 2 {
		return 0
	}
	number, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || number < 0 {
		return 0
	}
	var multiplier float64
	switch strings.TrimSuffix(fields[1], "B") {
	case "K", "KI":
		multiplier = 1 << 10
	case "M", "MI":
		multiplier = 1 << 20
	case "G", "GI":
		multiplier = 1 << 30
	case "T", "TI":
		multiplier = 1 << 40
	default:
		return 0
	}
	return int64(number * multiplier)
}

func parseTime(value string) *time.Time {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 02 Jan 2006 15:04:05 -0700"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}
