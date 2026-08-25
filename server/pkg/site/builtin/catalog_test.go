package builtin

import "testing"

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
	for _, key := range []string{"nyaa", "animetosho", "tokyotoshokan", "mikan", "anidex", "torznab"} {
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
