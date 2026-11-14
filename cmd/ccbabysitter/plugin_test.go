package main

import (
	"encoding/json"
	"flag"
	"image/png"
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

// Anthropic's plugin directory reads the icon and the documentation and
// support links from plugin.json for the plugin's listing. Claude Code
// ignores them.
func TestPluginListingFields(t *testing.T) {
	var plugin struct {
		Icon             string `json:"icon"`
		DocumentationURL string `json:"documentationUrl"`
		SupportURL       string `json:"supportUrl"`
	}
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &plugin)
	if plugin.Icon != "./.claude-plugin/icon.png" {
		t.Errorf("icon is %q", plugin.Icon)
	}
	// The directory takes a square PNG of 512 to 2048 pixels, under 2 MB.
	iconPath := filepath.Join(pluginDir, filepath.FromSlash(plugin.Icon))
	if f, err := os.Open(iconPath); err != nil {
		t.Errorf("the icon: %v (site/images_test.go makes it: go test ./site -run TestImagesMatchTheArt -update)", err)
	} else {
		cfg, err := png.DecodeConfig(f)
		fi, statErr := f.Stat()
		f.Close()
		switch {
		case err != nil:
			t.Errorf("the icon is not a PNG: %v", err)
		case cfg.Width != cfg.Height || cfg.Width < 512 || cfg.Width > 2048:
			t.Errorf("the icon is %dx%d, want a square of 512 to 2048 pixels", cfg.Width, cfg.Height)
		case statErr != nil || fi.Size() >= 2<<20:
			t.Errorf("the icon must be under 2 MB")
		}
	}
	if plugin.DocumentationURL != "https://ccbabysitter.dev/docs/claude-code-plugin/" {
		t.Errorf("documentationUrl is %q", plugin.DocumentationURL)
	}
	if plugin.SupportURL != "https://github.com/pejmanebrahimi/ccbabysitter/issues" {
		t.Errorf("supportUrl is %q", plugin.SupportURL)
	}
	// The docs page the listing links to is the one the site builds.
	if _, err := os.Stat(filepath.Join(repoRoot, "site", "docs", "claude-code-plugin", "index.html")); err != nil {
		t.Errorf("documentationUrl points to a page the site does not have: %v", err)
	}
}

// A listing in the directory also shows on claude.ai and in Cowork, so the
// description says where the plugin works; and the README, which the
// listing shows, names everything the plugin and CC Babysitter connect to
// and download. Every docs link in the README is a page the site has.
func TestPluginSaysWhereItWorksAndWhatItConnectsTo(t *testing.T) {
	var plugin struct{ Description string }
	readJSON(t, filepath.Join(pluginDir, ".claude-plugin", "plugin.json"), &plugin)
	for _, want := range []string{"Works only in Claude Code", "CC Babysitter"} {
		if !strings.Contains(plugin.Description, want) {
			t.Errorf("the description does not say %q", want)
		}
	}
	data, err := os.ReadFile(filepath.Join(pluginDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	privacy := sectionsOf(string(data))["Privacy"]
	for _, want := range []string{"`127.0.0.1`", "Anthropic", "ccbabysitter.dev", "https://github.com/pejmanebrahimi/ccbabysitter/releases"} {
		if !strings.Contains(privacy, want) {
			t.Errorf("the README's Privacy section does not name %s", want)
		}
	}
	// The install scripts are what the plugin's install command runs, so
	// every host they reach, and the releases they download, is named.
	for _, script := range []string{"install.sh", "install.ps1"} {
		body, err := os.ReadFile(filepath.Join(repoRoot, "scripts", script))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`https://([a-zA-Z0-9.-]+)`).FindAllStringSubmatch(string(body), -1) {
			if !strings.Contains(privacy, m[1]) {
				t.Errorf("%s reaches %s, which the README's Privacy section does not name", script, m[1])
			}
		}
		repo := regexp.MustCompile(`(?m)^\s*(?:REPO_URL=|\$repoUrl = )["']([^"']+)["']`).FindStringSubmatch(string(body))
		if repo == nil {
			t.Fatalf("%s sets no repository address", script)
		}
		if !strings.Contains(privacy, repo[1]+"/releases") {
			t.Errorf("%s downloads from %s/releases, which the README's Privacy section does not name", script, repo[1])
		}
	}
	links := regexp.MustCompile(`https://ccbabysitter\.dev/docs/([a-z-]+)/`).FindAllStringSubmatch(string(data), -1)
	if len(links) < 2 {
		t.Fatalf("the README links %d docs pages, want the plugin's page and troubleshooting", len(links))
	}
	for _, m := range links {
		if _, err := os.Stat(filepath.Join(repoRoot, "site", "docs", m[1], "index.html")); err != nil {
			t.Errorf("the README links %s, which the site does not have", m[0])
		}
	}
}

// Whoever installs the plugin gets only the plugin folder, so it carries
// the license itself, word for word the one at the top of the repository.
func TestPluginCarriesTheLicense(t *testing.T) {
	top, err := os.ReadFile(filepath.Join(repoRoot, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	own, err := os.ReadFile(filepath.Join(pluginDir, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	if string(own) != string(top) {
		t.Error("plugin/LICENSE differs from LICENSE; copy it: cp LICENSE plugin/LICENSE")
	}
}

// updateReference rewrites the files made from others: the plugin's and
// the docs' command references from the help pages, and the plugin's
// how-it-works.md and the docs' How it works page from the README:
// go test ./cmd/ccbabysitter -update-reference
var updateReference = flag.Bool("update-reference", false, "rewrite the plugin's references and the docs pages made from the help pages and the README")

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
		rel, _ := filepath.Rel(repoRoot, path)
		t.Fatalf("%s is out of date; run: go test ./cmd/ccbabysitter -run %s -update-reference", filepath.ToSlash(rel), test)
	}
}

// referenceCommands are the commands the reference documents, in order.
var referenceCommands = []string{"status", "list", "show", "babysit", "unbabysit", "retry", "stop", "start", "trust",
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
	// Vale's on and off comments are for the writing check, not for the
	// skill, so they are left out of its copy.
	readme := regexp.MustCompile(`(?m)^<!-- vale (on|off) -->\n\n?`).ReplaceAllString(string(data), "")
	sections := sectionsOf(readme)
	var b strings.Builder
	b.WriteString("# How CC Babysitter works\n\nGenerated from README.md at the top of the CC Babysitter repository; do not edit by hand.\n")
	for _, title := range howItWorksSections {
		body, ok := sections[title]
		if !ok || body == "" {
			t.Fatalf("README.md has no section %q", title)
		}
		b.WriteString("\n## " + title + "\n\n" + body + "\n")
	}
	if strings.Contains(b.String(), "<!--") {
		t.Fatal("how-it-works.md would carry a comment from the README")
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

// The skill pre-approves commands by bare name only. A grant by path names
// a program outside the plugin folder, which the plugin directory's review
// cannot read, so it holds the plugin for a reviewer; Claude Code asks
// before such a command instead.
func TestSkillGrantsNoCommandByPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "skills", "babysit", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^allowed-tools: (.*)$`).FindStringSubmatch(string(data))
	if m == nil {
		t.Fatal("SKILL.md has no allowed-tools")
	}
	grants := regexp.MustCompile(`[A-Za-z]+\(([^)]*)\)`).FindAllStringSubmatch(m[1], -1)
	if len(grants) == 0 {
		t.Fatal("allowed-tools grants nothing")
	}
	for _, g := range grants {
		if strings.ContainsAny(g[1], "/\\~$") {
			t.Errorf("allowed-tools grants a command by path: %s", g[0])
		}
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

// With a real card tool, Claude kept its reply short and asked on the card
// without ever showing the install command, then said it was "above". The
// skill has to say the command goes in the reply itself, before any card.
// And quitting lasts only until the next login: stopping for good is
// uninstall --keep-files, never a settings change made after quitting, and
// the skill never runs an uninstall that deletes the program and its state.
func TestSkillShowsCommandsBeforeCards(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(pluginDir, "skills", "babysit", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"in its own code block in your reply, before any card",
		"Never refer to a command you have not shown in that same reply",
		"Quitting lasts only until the next login",
		"to stop it for good, offer `ccbabysitter uninstall --keep-files`",
		"Never offer a settings change after quitting",
		"Never run `ccbabysitter reset` or `ccbabysitter uninstall` without `--keep-files`",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("SKILL.md does not say %q", want)
		}
	}
	// "A card first, then the command" read like asking before showing a
	// command; where the skill runs a command itself after asking, it says so.
	if strings.Contains(string(data), "a card first, then the command.") {
		t.Error(`SKILL.md still says "a card first, then the command."`)
	}
}
