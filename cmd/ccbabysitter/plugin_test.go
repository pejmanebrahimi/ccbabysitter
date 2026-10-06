package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
)

var pluginDir = filepath.Join(repoRoot, "plugin")

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// The marketplace lists the one plugin in plugin/, and the plugin carries
// CC Babysitter's own version, which the release bumps for both.
func TestPluginManifests(t *testing.T) {
	var market struct {
		Name    string                          `json:"name"`
		Owner   struct{ Name string }           `json:"owner"`
		Plugins []struct{ Name, Source string } `json:"plugins"`
	}
	readJSON(t, filepath.Join(repoRoot, ".claude-plugin", "marketplace.json"), &market)
	if market.Name != "ccbabysitter" || market.Owner.Name != "Pejman Ebrahimi" || len(market.Plugins) != 1 ||
		market.Plugins[0].Name != "ccbabysitter" || market.Plugins[0].Source != "./plugin" {
		t.Fatalf("marketplace: %+v", market)
	}
	var plugin struct {
		Name, DisplayName, Version, Description, Homepage, Repository, License string
		Author                                                                 struct{ Name string } `json:"author"`
		Keywords                                                               []string              `json:"keywords"`
	}
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &plugin)
	if plugin.Name != "ccbabysitter" || plugin.DisplayName != "CC Babysitter" || plugin.License != "MIT" ||
		plugin.Homepage != "https://ccbabysitter.dev" || plugin.Description == "" ||
		plugin.Repository != "https://github.com/pejmanebrahimi/ccbabysitter" || plugin.Author.Name != "Pejman Ebrahimi" ||
		len(plugin.Keywords) == 0 {
		t.Fatalf("plugin: %+v", plugin)
	}
	if plugin.Version != buildinfo.Version {
		t.Fatalf("plugin version %s, CC Babysitter %s: the release bumps both", plugin.Version, buildinfo.Version)
	}
}
