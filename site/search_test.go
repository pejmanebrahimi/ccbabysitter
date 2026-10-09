package site

import (
	"encoding/json"
	"flag"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// go test ./site -run TestDocsSearchIndex -update-search
var updateSearch = flag.Bool("update-search", false, "rewrite docs/search.json from the docs pages")

// searchEntry is one place the docs search can lead to: a page, or one of
// its sections, with the words to look in.
type searchEntry struct {
	URL     string `json:"url"`
	Page    string `json:"page"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

var (
	h1Text   = regexp.MustCompile(`(?s)<h1>(.*?)</h1>`)
	mainPart = regexp.MustCompile(`(?s)<main class="doc" id="content">(.*?)<nav class="pager"`)
	h2Split  = regexp.MustCompile(`<h2 id="([^"]+)">([^<]*)</h2>`)
	anyTag   = regexp.MustCompile(`(?s)<!--.*?-->|<[^>]+>`)
)

// notWords are the parts of a page that are not its words: the label
// saying what kind of page it is, and a command box's prompt and Copy.
var notWords = regexp.MustCompile(`(?s)<p class="kind">.*?</p>|<span class="p" data-prompt>[^<]*</span>|<span class="copy"[^>]*>[^<]*</span>`)

// plainText is HTML as the words a reader sees, on one line.
func plainText(s string) string {
	s = notWords.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(html.UnescapeString(anyTag.ReplaceAllString(s, " "))), " ")
}

// searchIndex is the docs search's index: for every docs page, the page
// itself with the words before its first section, then each section with
// its own words.
func searchIndex(t *testing.T) []searchEntry {
	t.Helper()
	var out []searchEntry
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		title := h1Text.FindStringSubmatch(s)
		body := mainPart.FindStringSubmatch(s)
		if title == nil || body == nil {
			t.Fatalf("%s has no h1 or no main part", f)
		}
		page := plainText(title[1])
		url := docsURL(f)
		rest := body[1]
		heads := h2Split.FindAllStringSubmatchIndex(rest, -1)
		intro := rest
		if len(heads) > 0 {
			intro = rest[:heads[0][0]]
		}
		intro = strings.Replace(intro, title[0], "", 1)
		out = append(out, searchEntry{URL: url, Page: page, Text: plainText(intro)})
		for i, h := range heads {
			end := len(rest)
			if i+1 < len(heads) {
				end = heads[i+1][0]
			}
			out = append(out, searchEntry{URL: url + "#" + rest[h[2]:h[3]], Page: page, Section: plainText(rest[h[4]:h[5]]), Text: plainText(rest[h[1]:end])})
		}
	}
	return out
}

// The search index is made from the docs pages, so it holds every page and
// section and nothing else; -update-search rewrites it.
func TestDocsSearchIndex(t *testing.T) {
	b, err := json.MarshalIndent(searchIndex(t), "", " ")
	if err != nil {
		t.Fatal(err)
	}
	want := string(b) + "\n"
	path := filepath.Join("docs", "search.json")
	if *updateSearch {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatal("docs/search.json is out of date; run: go test ./site -run TestDocsSearchIndex -update-search")
	}
}

// The search finds the entries holding every word asked for, best first:
// a word in a section's title counts most, then in the page's title, then
// in the text. Each result carries a snippet of its text around the first
// word found.
func TestDocsSearchFinds(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("assets", "docs.js"))
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := "var window = {};\n" + string(src) + `
var index = [
  {url: "/docs/a/", page: "Start at login", text: "CC Babysitter starts when you log in."},
  {url: "/docs/a/#off", page: "Start at login", section: "Turn it off", text: "Run ccbabysitter settings autostart off to stop it starting at login."},
  {url: "/docs/b/", page: "Run it on a server", text: "Keep sessions alive on a Linux server, and start at boot."},
  {url: "/docs/c/#x", page: "Other", section: "Nothing", text: "unrelated words"}
];
var s = window.ccbDocs.search;
function show(q) { console.log(JSON.stringify(s(index, q, 8).map(function (r) { return r.url; }))); }
show("login");
show("START login");
show("autostart off");
show("server boot");
show("");
show("  ");
show("missing");
var r = s(index, "autostart", 8)[0];
console.log(JSON.stringify([r.page, r.section, r.snippet]));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := strings.Join([]string{
		`["/docs/a/","/docs/a/#off"]`,
		`["/docs/a/","/docs/a/#off"]`,
		`["/docs/a/#off"]`,
		`["/docs/b/"]`,
		`[]`,
		`[]`,
		`[]`,
		`["Start at login","Turn it off","Run ccbabysitter settings autostart off to stop it starting at login."]`,
	}, "\n")
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// Every docs page has the same search box in its header, hidden until the
// script shows it, with a label and results it controls.
func TestDocsPagesHaveSearch(t *testing.T) {
	form := `  <form class="search" role="search" hidden>
    <label class="sr" for="docs-search">Search the docs</label>
    <input id="docs-search" type="search" placeholder="Search the docs" autocomplete="off" spellcheck="false" aria-controls="docs-results" aria-expanded="false">
    <ul class="results" id="docs-results" aria-label="Search results" hidden></ul>
  </form>
`
	for _, f := range docsPages(t) {
		if !strings.Contains(readSite(t, f), `<a class="crumb" href="/docs/">Docs</a>`+"\n"+form) {
			t.Errorf("%s has no search box after Docs in its header", f)
		}
	}
	src, err := os.ReadFile(filepath.Join("assets", "docs.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `window.fetch("/docs/search.json" + version)`) {
		t.Error("docs.js does not load docs/search.json with the page's version")
	}
}
