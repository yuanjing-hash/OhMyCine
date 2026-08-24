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
		if _, exists := seen[definition.Key]; exists {
			t.Fatalf("duplicate key %q", definition.Key)
		}
		seen[definition.Key] = struct{}{}
		if definition.AutoDiscover && len(Hosts(definition)) == 0 {
			t.Fatalf("auto-discovery definition has no host: %+v", definition)
		}
	}
	if _, ok := seen["nexusphp"]; !ok {
		t.Fatal("generic NexusPHP adapter missing")
	}
}
