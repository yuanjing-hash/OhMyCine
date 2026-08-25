package builtin

import (
	"net/url"
	"sort"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btrss"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/pttime"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/torznab"
)

const (
	SiteTypePT       = "pt"
	SiteTypeBT       = "bt"
	CredentialCookie = "cookie"
	CredentialAPIKey = "api_key"
	CredentialNone   = "none"
)

// Definition separates a concrete tracker identity from the parser engine.
// Most entries currently share the standard NexusPHP engine; exceptional
// trackers can later replace only their adapter without changing SiteService.
type Definition struct {
	Key            string
	Name           string
	Engine         string
	BaseURLs       []string
	AutoDiscover   bool
	SiteType       string
	CredentialKind string
	ptProfile      pttime.Profile
	btProfile      btrss.Profile
}

var definitions = []Definition{
	nexusDefinition("pttime", "PTTime", true, "https://www.pttime.org", "https://www.pttime.me"),
	nexusDefinition("sewerpt", "下水道 · SewerPT", true, "https://sewerpt.com"),
	nexusDefinition("panda", "熊猫高清 · PandaPT", true, "https://pandapt.net"),
	nexusDefinition("hdsky", "HDSky", true, "https://hdsky.me"),
	nexusDefinition("ourbits", "OurBits", true, "https://ourbits.club"),
	nexusDefinition("pterclub", "PTerClub", true, "https://pterclub.com"),
	nexusDefinition("audiences", "Audiences", true, "https://audiences.me"),
	nexusDefinition("hdhome", "HDHome", true, "https://hdhome.org"),
	nexusDefinition("hdfans", "HDFans", true, "https://hdfans.org"),
	nexusDefinition("hdarea", "HDArea", true, "https://hdarea.club"),
	nexusDefinition("chdbits", "CHDBits", true, "https://chdbits.co"),
	nexusDefinition("hdchina", "HDChina", true, "https://hdchina.org"),
	nexusDefinition("lemonhd", "LemonHD", true, "https://lemonhd.org"),
	nexusDefinition("springsunday", "SpringSunday", true, "https://springsunday.net"),
	nexusDefinition("u2", "U2", true, "https://u2.dmhy.org"),
	nexusDefinition("hhanclub", "HhanClub", true, "https://hhanclub.net"),
	nexusDefinition("nexusphp", "通用 NexusPHP", false),
	btDefinition("nyaa", "Nyaa", "https://nyaa.si", btrss.NyaaProfile()),
	btDefinition("animetosho", "AnimeTosho", "https://feed.animetosho.org", btrss.AnimeToshoProfile()),
	btDefinition("tokyotoshokan", "Tokyo Toshokan", "https://www.tokyotosho.info", btrss.TokyoToshokanProfile()),
	btDefinition("mikan", "Mikan", "https://mikanani.me", btrss.MikanProfile()),
	btDefinition("anidex", "AniDex", "https://anidex.info", btrss.AniDexProfile()),
	{Key: torznab.Kind, Name: "Torznab · Jackett/Prowlarr", Engine: "torznab", SiteType: SiteTypeBT, CredentialKind: CredentialAPIKey},
}

func nexusDefinition(key, name string, autoDiscover bool, baseURLs ...string) Definition {
	return Definition{Key: key, Name: name, Engine: "nexusphp", BaseURLs: baseURLs, AutoDiscover: autoDiscover, SiteType: SiteTypePT, CredentialKind: CredentialCookie, ptProfile: pttime.NexusPHPProfile()}
}

func btDefinition(key, name, baseURL string, profile btrss.Profile) Definition {
	return Definition{Key: key, Name: name, Engine: "rss", BaseURLs: []string{baseURL}, SiteType: SiteTypeBT, CredentialKind: CredentialNone, btProfile: profile}
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
		switch definition.Engine {
		case "nexusphp":
			items = append(items, pttime.NewForProfile(definition.Key, definition.ptProfile))
		case "rss":
			items = append(items, btrss.NewForProfile(definition.Key, definition.btProfile))
		case "torznab":
			items = append(items, torznab.New())
		}
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
