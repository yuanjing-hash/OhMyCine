package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseGitHubRepositoryURL(t *testing.T) {
	repository, err := ParseGitHubRepositoryURL("https://github.com/ohmycine/example-plugins.git")
	if err != nil {
		t.Fatal(err)
	}
	if repository.CanonicalURL() != "https://github.com/ohmycine/example-plugins" {
		t.Fatalf("CanonicalURL() = %q", repository.CanonicalURL())
	}

	for _, value := range []string{
		"http://github.com/ohmycine/plugins",
		"https://raw.githubusercontent.com/ohmycine/plugins/main/registry.json",
		"https://github.com/ohmycine/plugins/tree/main",
		"https://user:token@github.com/ohmycine/plugins",
		"https://github.com/ohmycine/plugins?ref=main",
	} {
		if _, err := ParseGitHubRepositoryURL(value); err == nil {
			t.Fatalf("ParseGitHubRepositoryURL(%q) accepted unsafe URL", value)
		}
	}
}

func TestParseRegistryAcceptsSharedFixture(t *testing.T) {
	data := readRegistryFixture(t)
	source := GitHubRepository{Owner: "ohmycine", Name: "example-plugins"}
	registry, err := ParseRegistry(data, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Plugins) != 1 || registry.Plugins[0].ID != "org.ohmycine.fixture.static-site" {
		t.Fatalf("unexpected registry: %#v", registry)
	}
}

func TestParseRegistryRejectsCrossRepositoryPackage(t *testing.T) {
	var value map[string]any
	if err := json.Unmarshal(readRegistryFixture(t), &value); err != nil {
		t.Fatal(err)
	}
	plugins := value["plugins"].([]any)
	plugins[0].(map[string]any)["packageUrl"] = "https://github.com/attacker/plugins/releases/download/v1/plugin.omcp"
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseRegistry(data, GitHubRepository{Owner: "ohmycine", Name: "example-plugins"}); err == nil {
		t.Fatal("ParseRegistry() accepted a cross-repository package")
	}
}

func TestParseRegistryRejectsUnsafeReleasePathsAndInvalidVersions(t *testing.T) {
	tests := map[string]func(map[string]any){
		"release traversal": func(plugin map[string]any) {
			plugin["packageUrl"] = "https://github.com/ohmycine/example-plugins/releases/download/../plugin.omcp"
		},
		"invalid prerelease": func(plugin map[string]any) { plugin["version"] = "1.0.0-beta..1" },
		"reversed server range": func(plugin map[string]any) {
			plugin["minServerVersion"] = "2.0.0"
			plugin["maxServerVersion"] = "1.0.0"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(readRegistryFixture(t), &value); err != nil {
				t.Fatal(err)
			}
			plugin := value["plugins"].([]any)[0].(map[string]any)
			mutate(plugin)
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseRegistry(data, GitHubRepository{Owner: "ohmycine", Name: "example-plugins"}); err == nil {
				t.Fatal("ParseRegistry() accepted unsafe registry data")
			}
		})
	}
}

func TestCompareVersionsUsesSemanticPrereleaseOrdering(t *testing.T) {
	comparison, err := CompareVersions("1.0.0-beta.10", "1.0.0-beta.2")
	if err != nil || comparison <= 0 {
		t.Fatalf("comparison=%d err=%v", comparison, err)
	}
	comparison, err = CompareVersions("999999999999999999999999999999.0.0", "2.0.0")
	if err != nil || comparison <= 0 {
		t.Fatalf("large comparison=%d err=%v", comparison, err)
	}
}

func readRegistryFixture(t *testing.T) []byte {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "plugin-sdk", "fixtures", "repository", "ohmycine-plugin-registry.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
