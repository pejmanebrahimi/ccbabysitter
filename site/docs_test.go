package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The docs are plain pages under docs/, one folder each, so every page's
// address ends in a slash. These tests keep what plain files repeat on
// every page in step: the sidebar, the previous and next links, the edit
// link and the "On this page" list.

// docsPages lists the docs pages, docs/index.html first.
func docsPages(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, f := range siteFiles(t) {
		if strings.HasPrefix(f, "docs/") && strings.HasSuffix(f, "/index.html") {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i] == "docs/index.html" || out[j] == "docs/index.html" {
			return out[i] == "docs/index.html"
		}
		return out[i] < out[j]
	})
	if len(out) == 0 || out[0] != "docs/index.html" {
		t.Fatal("site/docs/index.html is missing")
	}
	return out
}

// docsURL is the address a docs page is served at.
func docsURL(file string) string { return "/" + strings.TrimSuffix(file, "index.html") }

var (
	sidebar     = regexp.MustCompile(`(?s)<nav class="side" id="docs-nav" aria-label="Docs">.*?</nav>`)
	sideLink    = regexp.MustCompile(`<a href="(/docs/[^"]*)"( aria-current="page")?>([^<]*)</a>`)
	sideGroup   = regexp.MustCompile(`(?s)<p class="group">([^<]*)</p>\s*<ul>(.*?)</ul>`)
	pager       = regexp.MustCompile(`(?s)<nav class="pager" aria-label="Previous and next">(.*?)</nav>`)
	pagerLink   = regexp.MustCompile(`<a class="(prev|next)" href="([^"]*)">`)
	editLink    = regexp.MustCompile(`<a class="edit" href="([^"]*)"`)
	tocBlock    = regexp.MustCompile(`(?s)<nav class="toc-list" aria-label="On this page">(.*?)</nav>`)
	tocLink     = regexp.MustCompile(`<a href="#([^"]+)">`)
	h2Tag       = regexp.MustCompile(`<h2\b([^>]*)>`)
	idAttr      = regexp.MustCompile(`\bid="([^"]+)"`)
	kindLine    = regexp.MustCompile(`<p class="kind">([^<]*)</p>\s*<h1`)
	canonicalAt = regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)
)

// TestDocsPagesShareOneSidebar checks that every docs page carries the same
// sidebar, apart from which link is marked as the current page.
func TestDocsPagesShareOneSidebar(t *testing.T) {
	var first string
	for _, f := range docsPages(t) {
		nav := sidebar.FindString(readSite(t, f))
		if nav == "" {
			t.Errorf(`%s has no <nav class="side" id="docs-nav" aria-label="Docs">`, f)
			continue
		}
		nav = strings.ReplaceAll(nav, ` aria-current="page"`, "")
		if first == "" {
			first = nav
		} else if nav != first {
			t.Errorf("%s has a different sidebar from docs/index.html:\n%s\nwant:\n%s", f, nav, first)
		}
	}
}

// TestDocsSidebarListsEveryPageOnce checks that the sidebar links to every
// docs page exactly once and to nothing else, and marks the page it is on,
// and only that one, as the current page.
func TestDocsSidebarListsEveryPageOnce(t *testing.T) {
	pages := docsPages(t)
	want := map[string]bool{}
	for _, f := range pages {
		want[docsURL(f)] = true
	}
	for _, f := range pages {
		nav := sidebar.FindString(readSite(t, f))
		seen := map[string]bool{}
		var current []string
		for _, m := range sideLink.FindAllStringSubmatch(nav, -1) {
			if seen[m[1]] {
				t.Errorf("%s: the sidebar links to %s twice", f, m[1])
			}
			seen[m[1]] = true
			if !want[m[1]] {
				t.Errorf("%s: the sidebar links to %s, which is not a docs page", f, m[1])
			}
			if m[2] != "" {
				current = append(current, m[1])
			}
		}
		for u := range want {
			if !seen[u] {
				t.Errorf("%s: the sidebar does not link to %s", f, u)
			}
		}
		if len(current) != 1 || current[0] != docsURL(f) {
			t.Errorf("%s: the sidebar marks %v as the current page, want only %s", f, current, docsURL(f))
		}
	}
}

// sidebarOrder is the docs pages in the order the sidebar lists them.
func sidebarOrder(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range sideLink.FindAllStringSubmatch(sidebar.FindString(readSite(t, "docs/index.html")), -1) {
		out = append(out, m[1])
	}
	return out
}

// TestDocsPagerFollowsTheSidebar checks that each page's Previous and Next
// links go to the pages before and after it in the sidebar, and that the
// first page has no Previous and the last no Next.
func TestDocsPagerFollowsTheSidebar(t *testing.T) {
	order := sidebarOrder(t)
	index := map[string]int{}
	for i, u := range order {
		index[u] = i
	}
	for _, f := range docsPages(t) {
		m := pager.FindStringSubmatch(readSite(t, f))
		if m == nil {
			t.Errorf(`%s has no <nav class="pager" aria-label="Previous and next">`, f)
			continue
		}
		got := map[string]string{}
		for _, l := range pagerLink.FindAllStringSubmatch(m[1], -1) {
			got[l[1]] = l[2]
		}
		i := index[docsURL(f)]
		want := map[string]string{}
		if i > 0 {
			want["prev"] = order[i-1]
		}
		if i < len(order)-1 {
			want["next"] = order[i+1]
		}
		if len(got) != len(want) || got["prev"] != want["prev"] || got["next"] != want["next"] {
			t.Errorf("%s: previous and next are %v, want %v", f, got, want)
		}
	}
}

// TestDocsEditLinksPointAtThePage checks that "Edit this page on GitHub"
// opens the page's own file, and that each page names its own address.
func TestDocsEditLinksPointAtThePage(t *testing.T) {
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		want := "https://github.com/pejmanebrahimi/ccbabysitter/edit/main/site/" + f
		if m := editLink.FindStringSubmatch(s); m == nil || m[1] != want {
			t.Errorf("%s: the edit link is %v, want %s", f, m, want)
		}
		if !strings.Contains(s, `>Edit this page on GitHub<span class="sr"> (opens in a new tab)</span></a>`) {
			t.Errorf("%s: the edit link does not say Edit this page on GitHub", f)
		}
		if m := canonicalAt.FindStringSubmatch(s); m == nil || m[1] != "https://ccbabysitter.dev"+docsURL(f) {
			t.Errorf("%s: the canonical address is %v, want https://ccbabysitter.dev%s", f, m, docsURL(f))
		}
	}
}

// TestDocsOnThisPageListsTheSections checks that every section heading has
// an id of its own and that "On this page" links to each of them, in order.
func TestDocsOnThisPageListsTheSections(t *testing.T) {
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		var ids []string
		seen := map[string]bool{}
		for _, m := range h2Tag.FindAllStringSubmatch(s, -1) {
			id := idAttr.FindStringSubmatch(m[1])
			if id == nil {
				t.Errorf("%s: a section heading has no id: <h2%s>", f, m[1])
				continue
			}
			if seen[id[1]] {
				t.Errorf("%s: two sections have the id %q", f, id[1])
			}
			seen[id[1]] = true
			ids = append(ids, id[1])
		}
		var links []string
		if m := tocBlock.FindStringSubmatch(s); m != nil {
			for _, l := range tocLink.FindAllStringSubmatch(m[1], -1) {
				links = append(links, l[1])
			}
		} else if len(ids) > 0 {
			t.Errorf(`%s has sections but no <nav class="toc-list" aria-label="On this page">`, f)
		}
		if strings.Join(links, " ") != strings.Join(ids, " ") {
			t.Errorf("%s: On this page lists %v, the sections are %v", f, links, ids)
		}
	}
}

// docsGroups maps what kind of page a page is to the sidebar group that
// holds it, after the four kinds of documentation in Diataxis, with
// troubleshooting as a fifth.
var docsGroups = map[string]string{
	"Tutorial":        "Tutorials",
	"How-to guide":    "How-to guides",
	"Reference":       "Reference",
	"Explanation":     "Explanation",
	"Troubleshooting": "Troubleshooting",
}

// TestDocsPagesSayWhatKindTheyAre checks that every docs page but the
// overview says above its title what kind of page it is, and sits in the
// sidebar group for that kind.
func TestDocsPagesSayWhatKindTheyAre(t *testing.T) {
	group := map[string]string{}
	for _, g := range sideGroup.FindAllStringSubmatch(sidebar.FindString(readSite(t, "docs/index.html")), -1) {
		for _, l := range sideLink.FindAllStringSubmatch(g[2], -1) {
			group[l[1]] = g[1]
		}
	}
	for _, f := range docsPages(t) {
		m := kindLine.FindStringSubmatch(readSite(t, f))
		if f == "docs/index.html" {
			if m != nil {
				t.Errorf("the overview says it is a %s", m[1])
			}
			continue
		}
		if m == nil {
			t.Errorf(`%s does not say what kind of page it is in <p class="kind"> before its h1`, f)
			continue
		}
		want, ok := docsGroups[m[1]]
		if !ok {
			t.Errorf("%s says it is a %q, which is not one of the kinds", f, m[1])
		}
		if group[docsURL(f)] != want {
			t.Errorf("%s is a %s but sits in the sidebar group %q, want %q", f, m[1], group[docsURL(f)], want)
		}
	}
}

// TestDocsPagesLoadTheirStylesAndScripts checks that every docs page loads
// the docs stylesheet after the site's, and the docs script after the
// site's, at the end of the page.
func TestDocsPagesLoadTheirStylesAndScripts(t *testing.T) {
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		site, docs := strings.Index(s, `<link rel="stylesheet" href="/assets/site.css">`), strings.Index(s, `<link rel="stylesheet" href="/assets/docs.css">`)
		if site < 0 || docs < site {
			t.Errorf("%s must load /assets/docs.css after /assets/site.css", f)
		}
		js, djs := strings.Index(s, `<script src="/assets/site.js"></script>`), strings.Index(s, `<script src="/assets/docs.js"></script>`)
		if js < strings.Index(s, "</main>") || djs < js {
			t.Errorf("%s must load /assets/site.js and then /assets/docs.js after </main>", f)
		}
		if n := strings.Count(s, "data-theme-switch"); n != 1 {
			t.Errorf("%s has %d theme buttons, want 1", f, n)
		}
	}
}

// TestDocsCommandsCanBeCopied checks that every command shown on a docs
// page is a copy button like the one on the home page, with a prompt.
func TestDocsCommandsCanBeCopied(t *testing.T) {
	cmd := regexp.MustCompile(`(?s)<button class="cmd" type="button" data-copy>(.*?)</button>`)
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		for _, m := range cmd.FindAllStringSubmatch(s, -1) {
			for _, want := range []string{`<span class="p" data-prompt>`, `<span data-cmd>`, `<span class="copy" data-copy-label aria-live="polite">Copy</span>`} {
				if !strings.Contains(m[1], want) {
					t.Errorf("%s: a command has no %s: %s", f, want, m[1])
				}
			}
		}
		if strings.Contains(s, "<pre") && !strings.Contains(s, `<pre class="out"`) {
			t.Errorf("%s: a <pre> that is not output; show a command as a copy button", f)
		}
	}
}

// TestDocsMarksTheSectionBeingRead checks which section "On this page"
// marks for the headings' places in the window: none above the first, then
// the last heading that has reached the line.
func TestDocsMarksTheSectionBeingRead(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "docs.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := "var window = {};\n" + string(src) + `
var at = window.ccbDocs.sectionAt;
console.log([at([], 90), at([120, 600], 90), at([90, 600], 90), at([-400, 40, 300], 90), at([-900, -500, -100], 90)].join(" "));`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "-1 -1 0 1 2" {
		t.Fatalf("sectionAt gave %q, want %q", got, "-1 -1 0 1 2")
	}
}
