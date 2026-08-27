package builtin

import (
	"errors"
	"net/url"
	"sort"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/pkg/site"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btapi"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/bthtml"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/btrss"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/pttime"
	"github.com/yuanjing-hash/ohmycine/server/pkg/site/torznab"
	"golang.org/x/net/idna"
)

var (
	ErrURLInvalid = errors.New("site_url_invalid")
	ErrBTUnknown  = errors.New("site_bt_host_unsupported")
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
	Key               string
	Name              string
	Engine            string
	BaseURLs          []string
	AutoDiscover      bool
	SiteType          string
	CredentialKind    string
	DiscoverableByURL bool
	Search            bool
	Download          bool
	ptProfile         pttime.Profile
	btProfile         btrss.Profile
	btAPIProfile      btapi.Profile
	btHTMLProfile     bthtml.Profile
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
	btRSSDefinition("nyaa", "Nyaa", btrss.NyaaProfile(), "https://nyaa.si"),
	btRSSDefinition("animetosho", "AnimeTosho", btrss.AnimeToshoProfile(), "https://feed.animetosho.org", "https://animetosho.org"),
	btRSSDefinition("tokyotoshokan", "Tokyo Toshokan", btrss.TokyoToshokanProfile(), "https://www.tokyotosho.info", "https://tokyotosho.info"),
	btRSSDefinition("mikan", "Mikan", btrss.MikanProfile(), "https://mikanani.me"),
	btRSSDefinition("anidex", "AniDex", btrss.AniDexProfile(), "https://anidex.info"),
	btRSSDefinition("dmhy", "动漫花园", btrss.DMHYProfile(), "https://share.dmhy.org"),
	btRSSDefinition("acgrip", "ACG.RIP", btrss.ACGRipProfile(), "https://acg.rip"),
	btAPIDefinition("yts", "YTS", btapi.YTSProfile(), "https://yts.mx"),
	btAPIDefinition("eztv", "EZTV", btapi.EZTVProfile(), "https://eztvx.to"),
	btHTMLDefinition("1337x", "1337x", bthtml.X1337Profile(), "https://1337x.to"),
	btHTMLDefinition("thepiratebay", "The Pirate Bay", bthtml.PirateBayProfile(), "https://thepiratebay.org"),
	btHTMLDefinition("extto", "EXT.to", bthtml.EXTToProfile(), "https://ext.to"),
	btHTMLDefinition("limetorrents", "LimeTorrents", bthtml.LimeTorrentsProfile(), "https://www.limetorrents.fun", "https://limetorrents.fun", "https://www.limetorrents.lol", "https://limetorrents.lol"),
	{Key: torznab.Kind, Name: "Torznab · Jackett/Prowlarr", Engine: "torznab", SiteType: SiteTypeBT, CredentialKind: CredentialAPIKey, Search: true, Download: true},
}

func nexusDefinition(key, name string, autoDiscover bool, baseURLs ...string) Definition {
	return Definition{Key: key, Name: name, Engine: "nexusphp", BaseURLs: baseURLs, AutoDiscover: autoDiscover, SiteType: SiteTypePT, CredentialKind: CredentialCookie, Search: true, Download: true, ptProfile: pttime.NexusPHPProfile()}
}

func btRSSDefinition(key, name string, profile btrss.Profile, baseURLs ...string) Definition {
	return Definition{Key: key, Name: name, Engine: "rss", BaseURLs: baseURLs, SiteType: SiteTypeBT, CredentialKind: CredentialNone, DiscoverableByURL: true, Search: true, Download: true, btProfile: profile}
}

func btAPIDefinition(key, name string, profile btapi.Profile, baseURLs ...string) Definition {
	return Definition{Key: key, Name: name, Engine: "api", BaseURLs: baseURLs, SiteType: SiteTypeBT, CredentialKind: CredentialNone, DiscoverableByURL: true, Search: true, Download: true, btAPIProfile: profile}
}

func btHTMLDefinition(key, name string, profile bthtml.Profile, baseURLs ...string) Definition {
	return Definition{Key: key, Name: name, Engine: "html", BaseURLs: baseURLs, SiteType: SiteTypeBT, CredentialKind: CredentialNone, DiscoverableByURL: true, Search: true, Download: true, btHTMLProfile: profile}
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
		case "api":
			items = append(items, btapi.NewForProfile(definition.Key, definition.btAPIProfile))
		case "html":
			items = append(items, bthtml.NewForProfile(definition.Key, definition.btHTMLProfile))
		case "torznab":
			items = append(items, torznab.New())
		}
	}
	return items
}

// CatalogDefinitions deliberately omits concrete public BT providers. Their
// adapters ship with Server, but a provider becomes visible only after an
// administrator enters an exact supported official origin.
func CatalogDefinitions() []Definition {
	all := Definitions()
	items := make([]Definition, 0, len(all))
	for _, definition := range all {
		if definition.SiteType == SiteTypeBT && definition.DiscoverableByURL {
			continue
		}
		items = append(items, definition)
	}
	return items
}

// ResolveBTBaseURL normalizes a public BT root and resolves it through the
// explicit built-in host registry. It never probes the network or accepts a
// fuzzy/subdomain match.
func ResolveBTBaseURL(raw string) (Definition, string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(raw, "#") || (parsed.Path != "" && parsed.Path != "/") {
		return Definition{}, "", ErrURLInvalid
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return Definition{}, "", ErrURLInvalid
	}
	host, err := idna.Lookup.ToASCII(strings.ToLower(strings.TrimSuffix(parsed.Hostname(), ".")))
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if err != nil || host == "" {
		return Definition{}, "", ErrURLInvalid
	}
	for _, definition := range definitions {
		if definition.SiteType != SiteTypeBT || !definition.DiscoverableByURL {
			continue
		}
		for _, allowed := range definition.BaseURLs {
			if strings.EqualFold(Host(allowed), host) {
				return cloneDefinition(definition), "https://" + host, nil
			}
		}
	}
	return Definition{}, "", ErrBTUnknown
}

func cloneDefinition(definition Definition) Definition {
	definition.BaseURLs = append([]string(nil), definition.BaseURLs...)
	return definition
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
