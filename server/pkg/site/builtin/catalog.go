package builtin

import (
	"net/url"
	"sort"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/pttime"
)

// Definition separates a concrete tracker identity from the parser engine.
// Most entries currently share the standard NexusPHP engine; exceptional
// trackers can later replace only their adapter without changing SiteService.
type Definition struct {
	Key          string
	Name         string
	Engine       string
	BaseURLs     []string
	AutoDiscover bool
}

var definitions = []Definition{
	{Key: "pttime", Name: "PTTime", Engine: "nexusphp", BaseURLs: []string{"https://www.pttime.org", "https://www.pttime.me"}, AutoDiscover: true},
	{Key: "hdsky", Name: "HDSky", Engine: "nexusphp", BaseURLs: []string{"https://hdsky.me"}, AutoDiscover: true},
	{Key: "ourbits", Name: "OurBits", Engine: "nexusphp", BaseURLs: []string{"https://ourbits.club"}, AutoDiscover: true},
	{Key: "pterclub", Name: "PTerClub", Engine: "nexusphp", BaseURLs: []string{"https://pterclub.com"}, AutoDiscover: true},
	{Key: "audiences", Name: "Audiences", Engine: "nexusphp", BaseURLs: []string{"https://audiences.me"}, AutoDiscover: true},
	{Key: "hdhome", Name: "HDHome", Engine: "nexusphp", BaseURLs: []string{"https://hdhome.org"}, AutoDiscover: true},
	{Key: "hdfans", Name: "HDFans", Engine: "nexusphp", BaseURLs: []string{"https://hdfans.org"}, AutoDiscover: true},
	{Key: "hdarea", Name: "HDArea", Engine: "nexusphp", BaseURLs: []string{"https://hdarea.club"}, AutoDiscover: true},
	{Key: "chdbits", Name: "CHDBits", Engine: "nexusphp", BaseURLs: []string{"https://chdbits.co"}, AutoDiscover: true},
	{Key: "hdchina", Name: "HDChina", Engine: "nexusphp", BaseURLs: []string{"https://hdchina.org"}, AutoDiscover: true},
	{Key: "lemonhd", Name: "LemonHD", Engine: "nexusphp", BaseURLs: []string{"https://lemonhd.org"}, AutoDiscover: true},
	{Key: "springsunday", Name: "SpringSunday", Engine: "nexusphp", BaseURLs: []string{"https://springsunday.net"}, AutoDiscover: true},
	{Key: "u2", Name: "U2", Engine: "nexusphp", BaseURLs: []string{"https://u2.dmhy.org"}, AutoDiscover: true},
	{Key: "hhanclub", Name: "HhanClub", Engine: "nexusphp", BaseURLs: []string{"https://hhanclub.net"}, AutoDiscover: true},
	{Key: "nexusphp", Name: "通用 NexusPHP", Engine: "nexusphp"},
}

func Definitions() []Definition {
	items := make([]Definition, len(definitions))
	for index, item := range definitions {
		items[index] = item
		items[index].BaseURLs = append([]string(nil), item.BaseURLs...)
	}
	return items
}

func Adapters() []site.Adapter {
	items := make([]site.Adapter, 0, len(definitions))
	for _, definition := range definitions {
		items = append(items, pttime.NewForKind(definition.Key))
	}
	return items
}

func DefinitionForKey(key string) (Definition, bool) {
	key = strings.ToLower(strings.TrimSpace(key))
	for _, definition := range definitions {
		if definition.Key == key {
			definition.BaseURLs = append([]string(nil), definition.BaseURLs...)
			return definition, true
		}
	}
	return Definition{}, false
}

func Host(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func Hosts(definition Definition) []string {
	hosts := make([]string, 0, len(definition.BaseURLs))
	for _, baseURL := range definition.BaseURLs {
		if host := Host(baseURL); host != "" {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}
