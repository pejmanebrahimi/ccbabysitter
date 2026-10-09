package main

import (
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// commandsPage is the docs' command reference. The parts between its
// generated markers are the CLI's own help, so the page can never describe
// a command or flag the program does not have.
var commandsPage = filepath.Join(repoRoot, "site", "docs", "commands", "index.html")

// generated matches one generated part of a page: its marker, then
// everything up to the closing marker.
var generated = regexp.MustCompile(`(?s)(<!-- generated:([a-z]+) -->\n)(.*?)(\s*<!-- /generated -->)`)

// commandsParts are the generated parts of the command reference: the list
// under On this page, and a section for the usage and for each command.
// Each help page is a block that scrolls sideways on a narrow screen, and
// takes the focus so a keyboard can scroll it too.
func commandsParts() map[string]string {
	var toc, body strings.Builder
	var usage strings.Builder
	printUsage(&usage)
	toc.WriteString(`        <li><a href="#all">All commands</a></li>` + "\n")
	body.WriteString(`    <h2 id="all">All commands</h2>` + "\n")
	body.WriteString(`    <pre class="out" tabindex="0">` + html.EscapeString(usage.String()) + "</pre>\n")
	for _, name := range referenceCommands {
		toc.WriteString(`        <li><a href="#` + name + `">` + name + "</a></li>\n")
		body.WriteString("\n    <h2 id=\"" + name + "\">" + name + "</h2>\n")
		body.WriteString(`    <pre class="out" tabindex="0">` + html.EscapeString(helpPages[name]) + "</pre>\n")
	}
	return map[string]string{"toc": strings.TrimRight(toc.String(), "\n"), "commands": strings.TrimRight(body.String(), "\n")}
}

// go test ./cmd/ccbabysitter -run TestDocsCommandReference -update-reference
func TestDocsCommandReference(t *testing.T) {
	b, err := os.ReadFile(commandsPage)
	if err != nil {
		t.Fatal(err)
	}
	parts := commandsParts()
	seen := map[string]bool{}
	want := generated.ReplaceAllStringFunc(string(b), func(m string) string {
		g := generated.FindStringSubmatch(m)
		seen[g[2]] = true
		p, ok := parts[g[2]]
		if !ok {
			t.Errorf("%s has a generated part %q that nothing makes", commandsPage, g[2])
			return m
		}
		return g[1] + p + g[4]
	})
	for name := range parts {
		if !seen[name] {
			t.Fatalf("%s has no <!-- generated:%s --> part", commandsPage, name)
		}
	}
	checkGenerated(t, commandsPage, want, "TestDocsCommandReference")
}

// Every command in the usage has a section in the reference.
func TestDocsCommandReferenceCoversTheUsage(t *testing.T) {
	var usage strings.Builder
	printUsage(&usage)
	listed := map[string]bool{}
	for _, name := range referenceCommands {
		listed[name] = true
	}
	for _, m := range regexp.MustCompile(`(?m)^  ccbabysitter ([a-z]+)`).FindAllStringSubmatch(usage.String(), -1) {
		if !listed[m[1]] {
			t.Errorf("the usage lists %s, which the reference has no section for", m[1])
		}
	}
}
