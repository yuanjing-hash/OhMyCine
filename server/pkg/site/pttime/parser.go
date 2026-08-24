package pttime

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"golang.org/x/net/html"
)

func parseTorrentPage(body []byte) ([]site.Result, int, bool, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, 0, false, site.ErrInvalidReply
	}
	rows := findNodes(document, "tr")
	items, skipped := make([]site.Result, 0), 0
	for _, row := range rows {
		result, ok := parseRow(row)
		if !ok {
			continue
		}
		if result.TorrentID == "" || result.Title == "" {
			skipped++
			continue
		}
		items = append(items, result)
	}
	hasNext := false
	for _, anchor := range findNodes(document, "a") {
		text := strings.TrimSpace(nodeText(anchor))
		href := attr(anchor, "href")
		if text == "下一页" || text == "Next" || strings.Contains(strings.ToLower(attr(anchor, "rel")), "next") || strings.Contains(href, "page=") && strings.Contains(text, ">") {
			hasNext = true
			break
		}
	}
	return items, skipped, hasNext, nil
}

func parseRow(row *html.Node) (site.Result, bool) {
	var result site.Result
	anchors := findNodes(row, "a")
	for _, anchor := range anchors {
		href := attr(anchor, "href")
		parsed, err := urlParseRelative(href)
		if err != nil || !strings.HasSuffix(parsed.Path, "details.php") {
			continue
		}
		id := parsed.Query().Get("id")
		if !numericID(id) {
			continue
		}
		result.TorrentID = id
		result.Title = clean(strings.TrimSpace(attr(anchor, "title")), 512)
		if result.Title == "" {
			result.Title = clean(nodeText(anchor), 512)
		}
		break
	}
	if result.TorrentID == "" {
		return result, false
	}
	text := clean(nodeText(row), 4096)
	result.SizeBytes = parseSize(text)
	result.Quality = qualitySummary(result.Title)
	cells := findNodes(row, "td")
	if len(cells) >= 3 {
		numbers := make([]int, 0, 3)
		for i := len(cells) - 1; i >= 0 && len(numbers) < 3; i-- {
			value := strings.TrimSpace(nodeText(cells[i]))
			number, err := strconv.Atoi(value)
			if err == nil && number >= 0 {
				numbers = append(numbers, number)
			}
		}
		if len(numbers) > 0 {
			value := numbers[0]
			result.Completed = &value
		}
		if len(numbers) > 1 {
			value := numbers[1]
			result.Leechers = &value
		}
		if len(numbers) > 2 {
			value := numbers[2]
			result.Seeders = &value
		}
	}
	if strings.Contains(strings.ToLower(text), "free") || strings.Contains(text, "免费") {
		result.Promotion = "free"
	}
	if published := parsePublished(text); published != nil {
		result.Published = published
	}
	return result, true
}

func findNodes(root *html.Node, tag string) []*html.Node {
	items := []*html.Node{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == tag {
			items = append(items, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return items
}
func attr(node *html.Node, key string) string {
	for _, item := range node.Attr {
		if strings.EqualFold(item.Key, key) {
			return item.Val
		}
	}
	return ""
}
func nodeText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(strings.Fields(builder.String()), " ")
}
func clean(value string, limit int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if len([]rune(value)) > limit {
		return ""
	}
	return value
}

func parseSize(text string) int64 {
	fields := strings.Fields(text)
	for index := 0; index+1 < len(fields); index++ {
		value, err := strconv.ParseFloat(strings.Trim(fields[index], "(),"), 64)
		if err != nil || value < 0 {
			continue
		}
		unit := strings.ToUpper(strings.Trim(fields[index+1], "(),"))
		multiplier := float64(0)
		switch unit {
		case "KIB", "KB":
			multiplier = 1 << 10
		case "MIB", "MB":
			multiplier = 1 << 20
		case "GIB", "GB":
			multiplier = 1 << 30
		case "TIB", "TB":
			multiplier = 1 << 40
		}
		if multiplier > 0 && value*multiplier <= float64(^uint64(0)>>1) {
			return int64(value * multiplier)
		}
	}
	return 0
}

func qualitySummary(title string) string {
	lower := strings.ToLower(title)
	parts := []string{}
	for _, candidate := range []string{"2160p", "1080p", "720p", "remux", "bluray", "web-dl", "webrip", "hdtv", "x265", "hevc", "av1", "hdr", "dovi"} {
		if strings.Contains(lower, candidate) {
			parts = append(parts, candidate)
		}
	}
	if len(parts) > 5 {
		parts = parts[:5]
	}
	return strings.Join(parts, " · ")
}

func parsePublished(text string) *time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		for index := 0; index+len(layout) <= len(text); index++ {
			if value, err := time.ParseInLocation(layout, text[index:index+len(layout)], time.Local); err == nil {
				utc := value.UTC()
				return &utc
			}
		}
	}
	return nil
}
