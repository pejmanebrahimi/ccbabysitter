package main

import (
	"fmt"
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
	checkGeneratedParts(t, commandsPage, commandsParts(), "TestDocsCommandReference")
}

// checkGeneratedParts fills each generated part of page with parts, and
// fails, or rewrites the page under -update-reference, when it differs. A
// part on the page that nothing makes, or a part with no place on it,
// fails too.
func checkGeneratedParts(t *testing.T, page string, parts map[string]string, test string) {
	t.Helper()
	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	want := generated.ReplaceAllStringFunc(string(b), func(m string) string {
		g := generated.FindStringSubmatch(m)
		seen[g[2]] = true
		p, ok := parts[g[2]]
		if !ok {
			t.Errorf("%s has a generated part %q that nothing makes", page, g[2])
			return m
		}
		return g[1] + p + g[4]
	})
	for name := range parts {
		if !seen[name] {
			t.Fatalf("%s has no <!-- generated:%s --> part", page, name)
		}
	}
	checkGenerated(t, page, want, test)
}

// explanationPage is the docs' page on how CC Babysitter works. Its
// sections are the README's own, so the two never drift apart.
var explanationPage = filepath.Join(repoRoot, "site", "docs", "how-it-works", "index.html")

// explanationSections are the README sections the page is made of, in
// order, with the id each has on the page.
var explanationSections = []struct{ id, title string }{
	{"how", "How it works"},
	{"babysitting", "What babysitting does"},
	{"hard-rules", "Hard rules"},
	{"start-at-login", "Start at login"},
	{"requirements", "Requirements"},
}

// readmeSections are README.md's sections by their titles.
func readmeSections(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	sections := map[string]string{}
	for _, part := range strings.Split("\n"+string(data), "\n## ")[1:] {
		title, body, _ := strings.Cut(part, "\n")
		sections[title] = strings.TrimSpace(body)
	}
	return sections
}

// go test ./cmd/ccbabysitter -run TestDocsExplanation -update-reference
func TestDocsExplanation(t *testing.T) {
	sections := readmeSections(t)
	var toc, body strings.Builder
	for i, s := range explanationSections {
		md, ok := sections[s.title]
		if !ok || md == "" {
			t.Fatalf("README.md has no section %q", s.title)
		}
		text, err := markdownHTML(md)
		if err != nil {
			t.Fatalf("README.md's %q: %v", s.title, err)
		}
		toc.WriteString(`        <li><a href="#` + s.id + `">` + s.title + "</a></li>\n")
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(`    <h2 id="` + s.id + `">` + s.title + "</h2>\n")
		for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			body.WriteString("    " + line + "\n")
		}
	}
	checkGeneratedParts(t, explanationPage, map[string]string{
		"toc":         strings.TrimRight(toc.String(), "\n"),
		"explanation": strings.TrimRight(body.String(), "\n"),
	}, "TestDocsExplanation")
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

// The README's Markdown is turned into the page's HTML: paragraphs, lists,
// inline code, bold, links and Vale's comments, with everything else
// escaped.
func TestMarkdownHTML(t *testing.T) {
	md := "First line\nsame paragraph with `a <b>` and **bold**.\n\n" +
		"- One item\n  carried on.\n- Two, [a link](https://github.com/pejmanebrahimi/ccbabysitter).\n\n" +
		"<!-- vale off -->\n\nLast & least."
	want := "<p>First line same paragraph with <code>a &lt;b&gt;</code> and <b>bold</b>.</p>\n" +
		"<ul>\n<li>One item carried on.</li>\n" +
		`<li>Two, <a href="https://github.com/pejmanebrahimi/ccbabysitter" target="_blank" rel="noopener noreferrer">a link<span class="sr"> (opens in a new tab)</span></a>.</li>` + "\n</ul>\n" +
		"<!-- vale off -->\n" +
		"<p>Last &amp; least.</p>\n"
	got, err := markdownHTML(md)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	for _, bad := range []string{"[here](#install)", "# Heading", "1. numbered"} {
		if _, err := markdownHTML(bad); err == nil {
			t.Errorf("%q was turned into HTML instead of refused", bad)
		}
	}
}

// markdownLink is a Markdown link: [text](target).
var markdownLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// markdownBold is Markdown's bold: **text**.
var markdownBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)

// markdownHTML turns the README's Markdown into the docs' HTML. It knows
// only what the README sections it is used on hold: paragraphs, one level
// of "- " lists, inline code, bold, links out of the repository's README,
// and Vale's on and off comments. Anything else, such as a heading, a
// numbered list, a footnote or a link within the README, is refused, so a
// change to the README that the page could not show fails a test instead
// of reaching the site half turned.
func markdownHTML(md string) (string, error) {
	var b strings.Builder
	for _, block := range strings.Split(strings.TrimSpace(md), "\n\n") {
		block = strings.TrimSpace(block)
		switch {
		case block == "":
		case strings.HasPrefix(block, "<!-- vale ") && strings.HasSuffix(block, " -->") && !strings.Contains(block, "\n"):
			b.WriteString(block + "\n")
		case strings.HasPrefix(block, "#"), regexp.MustCompile(`^\d+\. `).MatchString(block), strings.Contains(block, "[^"), strings.HasPrefix(block, "|"), strings.HasPrefix(block, "```"):
			return "", fmt.Errorf("the docs cannot show this Markdown: %.40q", block)
		case strings.HasPrefix(block, "- "):
			var items []string
			for _, line := range strings.Split(block, "\n") {
				if rest, ok := strings.CutPrefix(line, "- "); ok {
					items = append(items, rest)
				} else {
					items[len(items)-1] += " " + strings.TrimSpace(line)
				}
			}
			b.WriteString("<ul>\n")
			for _, item := range items {
				text, err := markdownInline(item)
				if err != nil {
					return "", err
				}
				b.WriteString("<li>" + text + "</li>\n")
			}
			b.WriteString("</ul>\n")
		default:
			text, err := markdownInline(strings.Join(strings.Fields(block), " "))
			if err != nil {
				return "", err
			}
			b.WriteString("<p>" + text + "</p>\n")
		}
	}
	return b.String(), nil
}

// markdownInline turns one line of Markdown into HTML: code spans as they
// are, escaped, and bold and links in the text between them.
func markdownInline(line string) (string, error) {
	var b strings.Builder
	for i, part := range strings.Split(line, "`") {
		if i%2 == 1 {
			b.WriteString("<code>" + html.EscapeString(part) + "</code>")
			continue
		}
		var bad error
		text := markdownLink.ReplaceAllStringFunc(part, func(m string) string {
			g := markdownLink.FindStringSubmatch(m)
			if !strings.HasPrefix(g[2], "https://") {
				bad = fmt.Errorf("a link the docs cannot follow: %s", m)
				return m
			}
			return "\x00a\x01" + g[2] + "\x02" + g[1] + "\x00/a\x01"
		})
		if bad != nil {
			return "", bad
		}
		text = html.EscapeString(text)
		text = markdownBold.ReplaceAllString(text, "<b>$1</b>")
		text = regexp.MustCompile("\x00a\x01([^\x02]*)\x02([^\x00]*)\x00/a\x01").ReplaceAllString(text,
			`<a href="$1" target="_blank" rel="noopener noreferrer">$2<span class="sr"> (opens in a new tab)</span></a>`)
		b.WriteString(text)
	}
	return b.String(), nil
}
