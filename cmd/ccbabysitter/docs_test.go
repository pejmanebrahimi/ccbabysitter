package main

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
// order: the README's title, and the id and heading each has on the page.
// The words the page quotes from the app are plain text here, unlike on
// the hand-written pages, since the README does not mark them.
var explanationSections = []struct{ readme, id, heading string }{
	{"What babysitting does", "babysitting", "What babysitting does"},
	{"How it works", "apps", "Apps, the background and servers"},
	{"Hard rules", "hard-rules", "Hard rules"},
	{"Start at login", "start-at-login", "Start at login on each system"},
}

// readmeSections are README.md's sections by their titles.
func readmeSections(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return sectionsOf(string(data))
}

// sectionsOf splits Markdown into its "## " sections, by their titles.
func sectionsOf(md string) map[string]string {
	sections := map[string]string{}
	for _, part := range strings.Split("\n"+md, "\n## ")[1:] {
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
		md, ok := sections[s.readme]
		if !ok || md == "" {
			t.Fatalf("README.md has no section %q", s.readme)
		}
		text, err := markdownHTML(md)
		if err != nil {
			t.Fatalf("README.md's %q: %v", s.readme, err)
		}
		toc.WriteString(`        <li><a href="#` + s.id + `">` + s.heading + "</a></li>\n")
		if i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(`    <h2 id="` + s.id + `">` + s.heading + "</h2>\n")
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
	for _, bad := range []string{
		"[here](#install)", "[a file](SECURITY.md)", "[old](http://example.com)", "[paren](https://example.com/a_(b))",
		"# Heading", "1. numbered", "1) numbered", "* star", "+ plus", "> quote", "<picture>", "| a | b |",
		"```\ncode\n```", "text\n```\ncode\n```", "    indented code", "a footnote[^1]",
		"- item\n  - nested", "Intro:\n- a\n- b", "*italic*", "_italic_", "**bold `code` bold**",
		"an `unmatched backtick", "``double``", "a \\* escape", "[ref][1]",
		"![a picture](https://example.com/a.png)", "[**bold** label](https://example.com)", "[a * b](https://example.com)",
		"- one\n\n- two",
	} {
		if _, err := markdownHTML(bad); err == nil {
			t.Errorf("%q was turned into HTML instead of refused", bad)
		}
	}
}

// The Markdown markdownHTML reads, and what it refuses.
var (
	// markdownLink is a Markdown link out of the repository: [text](https://...).
	markdownLink = regexp.MustCompile(`\[([^\]]+)\]\((https://[^()\s]+)\)`)
	// markdownBold is Markdown's bold: **text**.
	markdownBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	// markdownBlockStart is a line that starts a block markdownHTML cannot
	// show: a heading, a numbered list, another bullet, a quote, HTML, a
	// table or a fence.
	markdownBlockStart = regexp.MustCompile("^(#|\\d+[.)] |[*+] |> |<|\\||```)")
	// markdownLeft is Markdown still in the text once bold and links are
	// turned: a link of another kind, emphasis, an escape or a footnote.
	markdownLeft = regexp.MustCompile(`\*|\]\(|\]\[|\[\^|(^|[\s(])_[^\s_]|\\[[:punct:]]|!\x00`)
)

// markdownHTML turns the README's Markdown into the docs' HTML. It knows
// only what the README sections it is used on hold: paragraphs, one level
// of "- " lists whose items may carry on over indented lines, inline code,
// bold, links out of the repository, and Vale's on and off comments, each
// a block of its own. Anything else, such as a heading, a nested or
// numbered list, a quote, a footnote, emphasis or a link within the
// README, is refused, so a change to the README that the page could not
// show fails a test instead of reaching the site half turned.
func markdownHTML(md string) (string, error) {
	var b strings.Builder
	lastList := false
	for _, block := range strings.Split(strings.Trim(md, "\n"), "\n\n") {
		block = strings.Trim(block, "\n")
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) == 1 && strings.HasPrefix(block, "<!-- vale ") && strings.HasSuffix(block, " -->") {
			b.WriteString(block + "\n")
			continue
		}
		list := strings.HasPrefix(lines[0], "- ")
		if list && lastList {
			return "", fmt.Errorf("the docs cannot show a list with blank lines in it: %q", lines[0])
		}
		lastList = list
		var items []string
		for i, line := range lines {
			bullet := strings.HasPrefix(line, "- ")
			indented := line != strings.TrimLeft(line, " \t")
			switch {
			case markdownBlockStart.MatchString(line):
				return "", fmt.Errorf("the docs cannot show this Markdown: %q", line)
			case bullet && !list, list && indented && strings.HasPrefix(strings.TrimSpace(line), "- "):
				return "", fmt.Errorf("the docs cannot show a list here: %q", line)
			case bullet:
				items = append(items, strings.TrimPrefix(line, "- "))
			case list && indented && i > 0:
				items[len(items)-1] += " " + strings.TrimSpace(line)
			case indented:
				return "", fmt.Errorf("the docs cannot show an indented line: %q", line)
			case list:
				return "", fmt.Errorf("the docs cannot show a line run on into a list: %q", line)
			default:
				items = append(items, line)
			}
		}
		if !list {
			text, err := markdownInline(strings.Join(items, " "))
			if err != nil {
				return "", err
			}
			b.WriteString("<p>" + text + "</p>\n")
			continue
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
	}
	return b.String(), nil
}

// escapeText escapes what HTML text needs escaped, and leaves quotes as
// they are, as the hand-written pages have them.
var escapeText = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// markdownInline turns one line of Markdown into HTML: code spans as they
// are, escaped, and bold and links in the text between them. A line with
// an odd number of backticks, a doubled one, or any Markdown left over is
// refused.
func markdownInline(line string) (string, error) {
	if strings.Count(line, "`")%2 == 1 || strings.Contains(line, "``") {
		return "", fmt.Errorf("the docs cannot show these backticks: %q", line)
	}
	var b strings.Builder
	for i, part := range strings.Split(line, "`") {
		if i%2 == 1 {
			b.WriteString("<code>" + escapeText(part) + "</code>")
			continue
		}
		type link struct{ text, url string }
		var links []link
		text := markdownLink.ReplaceAllStringFunc(part, func(m string) string {
			g := markdownLink.FindStringSubmatch(m)
			links = append(links, link{g[1], g[2]})
			return "\x00" + strconv.Itoa(len(links)-1) + "\x00"
		})
		text = markdownBold.ReplaceAllString(text, "\x01$1\x02")
		if markdownLeft.MatchString(text) {
			return "", fmt.Errorf("the docs cannot show this Markdown: %q", part)
		}
		text = escapeText(text)
		text = strings.NewReplacer("\x01", "<b>", "\x02", "</b>").Replace(text)
		for n, l := range links {
			if markdownLeft.MatchString(l.text) {
				return "", fmt.Errorf("the docs cannot show Markdown in a link's text: %q", l.text)
			}
			text = strings.Replace(text, "\x00"+strconv.Itoa(n)+"\x00",
				`<a href="`+html.EscapeString(l.url)+`" target="_blank" rel="noopener noreferrer">`+escapeText(l.text)+`<span class="sr"> (opens in a new tab)</span></a>`, 1)
		}
		b.WriteString(text)
	}
	return b.String(), nil
}
