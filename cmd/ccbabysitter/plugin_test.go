package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// updateReference rewrites the plugin's generated references, the command
// reference from the help pages and how-it-works.md from the README:
// go test ./cmd/ccbabysitter -run 'TestPluginReference|TestPluginHowItWorks' -update-reference
var updateReference = flag.Bool("update-reference", false, "rewrite plugin/skills/babysit/references")

// referencesDir is where the skill's references live, beside SKILL.md.
var referencesDir = filepath.Join(pluginDir, "skills", "babysit", "references")

// checkGenerated rewrites path with want under -update-reference, and fails
// when the file on disk is not want.
func checkGenerated(t *testing.T, path, want, test string) {
	t.Helper()
	if *updateReference {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s is out of date; run: go test ./cmd/ccbabysitter -run %s -update-reference", filepath.Base(path), test)
	}
}

// referenceCommands are the commands the reference documents, in order.
var referenceCommands = []string{"status", "list", "show", "babysit", "unbabysit", "retry", "stop",
	"activity", "settings", "open", "quit", "install", "uninstall", "reset", "version", "help"}

// The skill's reference is the CLI's own help, word for word, so it can
// never describe a command or flag the installed program does not have.
func TestPluginReference(t *testing.T) {
	var b strings.Builder
	b.WriteString("# CC Babysitter command reference\n\nGenerated from `ccbabysitter help`; do not edit by hand.\n\n```\n")
	printUsage(&b)
	b.WriteString("```\n")
	for _, name := range referenceCommands {
		page, ok := helpPages[name]
		if !ok {
			t.Fatalf("no help page for %s", name)
		}
		b.WriteString("\n## " + name + "\n\n```\n" + page + "```\n")
	}
	checkGenerated(t, filepath.Join(referencesDir, "commands.md"), b.String(), "TestPluginReference")
}

// howItWorksSections are the README sections that say how CC Babysitter
// behaves, in the order the skill's how-it-works.md has them.
var howItWorksSections = []string{"Why a babysitter?", "How it works", "What babysitting does", "Hard rules",
	"Start at login", "Uninstall", "Requirements"}

// The skill answers questions about how CC Babysitter behaves from the
// README's own words, copied whole by section, so it says what the README
// says and nothing else.
func TestPluginHowItWorks(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]string{}
	for _, part := range strings.Split("\n"+string(data), "\n## ")[1:] {
		title, body, _ := strings.Cut(part, "\n")
		sections[title] = strings.TrimSpace(body)
	}
	var b strings.Builder
	b.WriteString("# How CC Babysitter works\n\nGenerated from README.md at the top of the CC Babysitter repository; do not edit by hand.\n")
	for _, title := range howItWorksSections {
		body, ok := sections[title]
		if !ok || body == "" {
			t.Fatalf("README.md has no section %q", title)
		}
		b.WriteString("\n## " + title + "\n\n" + body + "\n")
	}
	checkGenerated(t, filepath.Join(referencesDir, "how-it-works.md"), b.String(), "TestPluginHowItWorks")
}

// The skill sends each kind of question to the reference that answers it,
// and says not to guess beyond them.
func TestSkillPointsToItsReferences(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "skills", "babysit", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"references/commands.md", "references/how-it-works.md", "Do not guess"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("SKILL.md does not mention %q", want)
		}
	}
	if _, err := os.Stat(filepath.Join(pluginDir, "skills", "babysit", "reference.md")); err == nil {
		t.Error("the old reference.md is still there")
	}
}

var commandWord = regexp.MustCompile("`ccbabysitter ([a-z]+)([^`]*)`")

// Every ccbabysitter command and flag the skill and its README name exists
// in the CLI.
func TestSkillNamesOnlyRealCommands(t *testing.T) {
	for _, name := range []string{filepath.Join("skills", "babysit", "SKILL.md"), "README.md"} {
		data, err := os.ReadFile(filepath.Join(pluginDir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range commandWord.FindAllStringSubmatch(string(data), -1) {
			cmd := m[1]
			page, ok := helpPages[cmd]
			if !ok {
				t.Errorf("%s names ccbabysitter %s, which is not a command", name, cmd)
				continue
			}
			for _, flagTok := range regexp.MustCompile(`--[a-z-]+`).FindAllString(m[2], -1) {
				if flagTok != "--json" && flagTok != "--url" && !strings.Contains(page, flagTok) {
					t.Errorf("%s names %s %s, which help %s does not have", name, cmd, flagTok, cmd)
				}
			}
		}
	}
}

// The skill never runs anything whose output carries the page's key:
// allowed-tools never lets it run status, a plain ccbabysitter or
// --foreground, and its text names them only in the rule that forbids
// them, and a plain ccbabysitter only as something the person runs in
// their terminal app.
func TestSkillNeverRunsAKeyedCommand(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "skills", "babysit", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	front, body, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
	if !ok {
		t.Fatal("SKILL.md has no frontmatter")
	}
	for _, bad := range []string{"ccbabysitter status", "ccbabysitter --foreground", "ccbabysitter)", "ccbabysitter *)"} {
		if strings.Contains(front, bad) {
			t.Errorf("allowed-tools lets the skill run %q", bad)
		}
	}
	for _, want := range []string{"name: babysit", "description:", "allowed-tools:", "argument-hint:"} {
		if !strings.Contains(front, want) {
			t.Errorf("frontmatter has no %q", want)
		}
	}
	rule := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "- Never run `ccbabysitter` with no command") {
			rule = true
			continue
		}
		if strings.Contains(line, "`ccbabysitter status`") && !strings.Contains(line, "do not run it") {
			t.Errorf("the skill names ccbabysitter status outside the rule: %q", line)
		}
		if strings.Contains(line, "--foreground") {
			t.Errorf("the skill names --foreground outside the rule: %q", line)
		}
		if strings.Contains(line, "`ccbabysitter`") && !strings.Contains(line, "terminal app") {
			t.Errorf("the skill names a plain ccbabysitter not for the terminal app: %q", line)
		}
	}
	if !rule {
		t.Error("the rule against keyed commands is gone")
	}
}

// The eval runner grants only commands by bare name, which find the fake
// first on PATH. A path into the home folder would reach a real
// ccbabysitter if the eval sandbox ever let it through.
func TestEvalsNeverGrantARealBinary(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "evals", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range regexp.MustCompile(`"Bash\(([^)]*)\)"`).FindAllStringSubmatch(string(data), -1) {
		if strings.ContainsAny(grant[1], "~/$") {
			t.Errorf("run.sh grants a command by path: %s", grant[0])
		}
	}
}

// On Windows, Claude Code's shell is usually Git Bash, and right after the
// install it still has the old PATH, so the skill also gives the install
// folder in Git Bash form, not only in PowerShell form.
func TestSkillFindsWindowsInstallFromGitBash(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "skills", "babysit", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"$LOCALAPPDATA/Programs/CCBabysitter/ccbabysitter.exe" version --json`, `& "$env:LOCALAPPDATA\Programs\CCBabysitter\ccbabysitter.exe" version --json`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("SKILL.md does not give %s", want)
		}
	}
}
