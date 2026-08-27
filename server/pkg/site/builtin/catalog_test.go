package builtin

import (
	"errors"
	"testing"
)

func TestCatalogKeysAndAdaptersStayAligned(t *testing.T) {
	definitions := Definitions()
	adapters := Adapters()
	if len(definitions) < 10 || len(adapters) != len(definitions) {
		t.Fatalf("definitions=%d adapters=%d", len(definitions), len(adapters))
	}
	seen := map[string]struct{}{}
	for index, definition := range definitions {
		if definition.Key == "" || definition.Name == "" || definition.Engine == "" || adapters[index].Kind() != definition.Key {
			t.Fatalf("definition=%+v adapter=%q", definition, adapters[index].Kind())
		}
		if definition.SiteType != SiteTypePT && definition.SiteType != SiteTypeBT {
			t.Fatalf("invalid site type: %+v", definition)
		}
		if definition.CredentialKind != CredentialCookie && definition.CredentialKind != CredentialAPIKey && definition.CredentialKind != CredentialNone {
			t.Fatalf("invalid credential kind: %+v", definition)
		}
		if _, exists := seen[definition.Key]; exists {
			t.Fatalf("duplicate key %q", definition.Key)
		}
		seen[definition.Key] = struct{}{}
		if definition.AutoDiscover && len(Hosts(definition)) == 0 {
			t.Fatalf("auto-discovery definition has no host: %+v", definition)
		}
	}
	for _, key := range []string{"nyaa", "animetosho", "tokyotoshokan", "mikan", "anidex", "dmhy", "acgrip", "yts", "eztv", "1337x", "thepiratebay", "extto", "limetorrents", "torznab"} {
		definition, ok := DefinitionForKey(key)
		if !ok || definition.SiteType != SiteTypeBT || definition.AutoDiscover {
			t.Fatalf("BT definition %q=%+v", key, definition)
		}
	}
	if _, ok := seen["nexusphp"]; !ok {
		t.Fatal("generic NexusPHP adapter missing")
	}
	for key, expectedOrigin := range map[string]string{"sewerpt": "https://sewerpt.com", "panda": "https://pandapt.net"} {
		definition, ok := DefinitionForKey(key)
		if !ok || !definition.AutoDiscover || len(definition.BaseURLs) != 1 || definition.BaseURLs[0] != expectedOrigin {
			t.Fatalf("definition %q=%+v", key, definition)
		}
	}
}

func TestResolveBTBaseURLUsesExactOfficialHosts(t *testing.T) {
	for raw, expected := range map[string]string{
		"https://nyaa.si":          "nyaa",
		"https://share.dmhy.org/":  "dmhy",
		"https://acg.rip":          "acgrip",
		"https://yts.mx":           "yts",
		"https://eztvx.to":         "eztv",
		"https://1337x.to":         "1337x",
		"https://thepiratebay.org": "thepiratebay",
		"https://ext.to":           "extto",
		"https://limetorrents.fun": "limetorrents",
		"https://limetorrents.lol": "limetorrents",
	} {
		definition, canonical, err := ResolveBTBaseURL(raw)
		if err != nil || definition.Key != expected || canonical == "" || !definition.DiscoverableByURL || !definition.Search || !definition.Download {
			t.Fatalf("resolve %q: definition=%+v canonical=%q err=%v", raw, definition, canonical, err)
		}
	}
	for _, raw := range []string{"http://nyaa.si", "https://user@nyaa.si", "https://nyaa.si/search", "https://nyaa.si?q=x", "https://nyaa.si?", "https://nyaa.si#", "https://nyaa.si.evil.test", "https://mirror.example.test", "https://nyaa.si:8443"} {
		if _, _, err := ResolveBTBaseURL(raw); err == nil {
			t.Fatalf("unsafe or unknown BT origin accepted: %q", raw)
		}
	}
	if _, _, err := ResolveBTBaseURL("https://mirror.example.test"); !errors.Is(err, ErrBTUnknown) {
		t.Fatalf("unknown host error=%v", err)
	}
}

func TestCatalogDefinitionsHideConcretePublicBTProviders(t *testing.T) {
	for _, definition := range CatalogDefinitions() {
		if definition.SiteType == SiteTypeBT && definition.DiscoverableByURL {
			t.Fatalf("public BT shortcut leaked into catalog: %+v", definition)
		}
	}
	if definition, ok := DefinitionForKey("nyaa"); !ok || !definition.DiscoverableByURL {
		t.Fatal("internal public BT registry was removed together with catalog entry")
	}
}
