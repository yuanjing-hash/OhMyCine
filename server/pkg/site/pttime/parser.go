package pttime

import (
	"bytes"
	"path"
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
	type candidate struct {
		anchor *html.Node
		row    *html.Node
		score  int
	}
	candidates := make(map[string]candidate)
	order := make([]string, 0)
	for _, anchor := range findNodes(document, "a") {
		id, ok := torrentAnchorID(anchor)
		if !ok {
			continue
		}
		row, score := bestTorrentRow(anchor)
		if row == nil {
			continue
		}
		current, exists := candidates[id]
		title := torrentAnchorTitle(anchor)
		currentTitle := torrentAnchorTitle(current.anchor)
		if !exists {
			order = append(order, id)
		}
		if !exists || score > current.score || score == current.score && currentTitle == "" && title != "" {
			candidates[id] = candidate{anchor: anchor, row: row, score: score}
		}
	}
	items, skipped := make([]site.Result, 0), 0
	for _, id := range order {
		selected := candidates[id]
		result := parseTorrentResult(id, selected.anchor, selected.row)
		if result.Title == "" {
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

func parseTorrentResult(id string, anchor, row *html.Node) site.Result {
	result := site.Result{TorrentID: id, Title: torrentAnchorTitle(anchor)}
	text := clean(nodeText(row), 4096)
	result.SizeBytes = parseSize(text)
	result.Quality = qualitySummary(result.Title)
	cells := directChildren(row, "td")
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
	if hasFreePromotion(row, text) {
		result.Promotion = "free"
	}
	if published := parsePublishedFromRow(row, text); published != nil {
		result.Published = published
	}
	return result
}

func torrentAnchorID(anchor *html.Node) (string, bool) {
	parsed, err := urlParseRelative(attr(anchor, "href"))
	if err != nil || !strings.EqualFold(path.Base(parsed.Path), "details.php") {
		return "", false
	}
	id := parsed.Query().Get("id")
	return id, numericID(id)
}

func torrentAnchorTitle(anchor *html.Node) string {
	if anchor == nil {
		return ""
	}
	if title := clean(attr(anchor, "title"), 512); title != "" {
		return title
	}
	return clean(nodeText(anchor), 512)
}

// NexusPHP variants such as PandaPT put the title anchor in an inner
// table.torrentname while size and peer columns live on the outer torrent
// row. Select the ancestor row with the richest set of direct cells; ties
// deliberately keep the nearest row. Torrent ID de-duplication in the caller
// prevents the outer/nested markup from producing duplicate results.
func bestTorrentRow(anchor *html.Node) (*html.Node, int) {
	var best *html.Node
	bestScore := -1
	for current := anchor.Parent; current != nil; current = current.Parent {
		if current.Type != html.ElementNode || current.Data != "tr" {
			continue
		}
		score := len(directChildren(current, "td"))
		if score > bestScore {
			best, bestScore = current, score
		}
	}
	return best, bestScore
}

func hasFreePromotion(row *html.Node, text string) bool {
	if strings.Contains(strings.ToLower(text), "free") || strings.Contains(text, "免费") {
		return true
	}
	for _, node := range findNodes(row, "*") {
		for _, className := range strings.Fields(strings.ToLower(attr(node, "class"))) {
			if strings.HasPrefix(className, "pro_free") {
				return true
			}
		}
		label := strings.ToLower(attr(node, "title") + " " + attr(node, "alt"))
		if strings.Contains(label, "free") || strings.Contains(label, "免费") {
			return true
		}
	}
	return false
}

func parsePublishedFromRow(row *html.Node, text string) *time.Time {
	for _, span := range findNodes(row, "span") {
		if value := parsePublished(attr(span, "title")); value != nil {
			return value
		}
	}
	return parsePublished(text)
}

func directChildren(root *html.Node, tag string) []*html.Node {
	items := make([]*html.Node, 0)
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			items = append(items, child)
		}
	}
	return items
}

func findNodes(root *html.Node, tag string) []*html.Node {
	items := []*html.Node{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (tag == "*" || node.Data == tag) {
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
