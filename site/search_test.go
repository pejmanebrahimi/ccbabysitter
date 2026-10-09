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

// notWords are the parts of a page that are not its words: the labels
// saying what kind of page or card it is, a command box's prompt and Copy,
// and text only screen readers hear.
var notWords = regexp.MustCompile(`(?s)<p class="kind">.*?</p>|<span class="kname">[^<]*</span>|<span class="p" data-prompt>[^<]*</span>|<span class="copy"[^>]*>[^<]*</span>|<span class="sr">[^<]*</span>`)

// inlineTag is a tag inside a line of text, which leaves no space where
// it was; any other tag ends a block of text.
var inlineTag = regexp.MustCompile(`</?(a|b|code|em|small|span|strong|sup)\b[^>]*>`)

// plainText is HTML as the words a reader sees, on one line.
func plainText(s string) string {
	s = notWords.ReplaceAllString(s, " ")
	s = inlineTag.ReplaceAllString(s, "")
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
	index := searchIndex(t)
	// Every section the docs have is in it: a heading the index could not
	// read would otherwise be left out without a word.
	urls := map[string]bool{}
	for _, e := range index {
		urls[e.URL] = true
	}
	for _, f := range docsPages(t) {
		for _, m := range h2Tag.FindAllStringSubmatch(readSite(t, f), -1) {
			if id := idAttr.FindStringSubmatch(m[1]); id != nil && !urls[docsURL(f)+"#"+id[1]] {
				t.Errorf("%s: the section %q is not in the search index", f, id[1])
			}
		}
	}
	b, err := json.MarshalIndent(index, "", " ")
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
// script shows it, with a label, the results it controls, and a status
// that tells screen readers what was found.
func TestDocsPagesHaveSearch(t *testing.T) {
	form := `  <form class="search" role="search" hidden>
    <label class="sr" for="docs-search">Search the docs</label>
    <input id="docs-search" type="search" placeholder="Search the docs" autocomplete="off" spellcheck="false" aria-controls="docs-results">
    <p class="sr" role="status"></p>
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

// Words match at the start of a word, a word found only in a page's title
// does not bring in every section of it, a plural finds the singular,
// letters in any language and a phone's curly apostrophe are words, and
// trailing punctuation is dropped.
func TestDocsSearchWords(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "docs.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := "var window = {};\n" + string(src) + `
var index = [
  {url: "/a/", page: "Start at login", text: "Starts when you log in."},
  {url: "/a/#move", page: "Start at login", section: "Keep the program where it is", text: "If you move it, run it again."},
  {url: "/a/#systems", page: "Start at login", section: "What it uses on each system", text: "A login item on macOS."},
  {url: "/b/#restart", page: "Restarts", section: "After a restart", text: "It's back after a restart. Caf\u00e9 open."},
  {url: "/c/", page: "Settings", text: "Change the settings."}
];
var s = window.ccbDocs.search;
function show(q) { console.log(q + " = " + JSON.stringify(s(index, q, 8).map(function (r) { return r.url; }))); }
show("art");
show("login");
show("systems");
show("caf\u00e9");
show("it\u2019s back");
show("settings.");
show("a");
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := strings.Join([]string{
		`art = []`,
		`login = ["/a/","/a/#systems"]`,
		`systems = ["/a/#systems"]`,
		"caf\u00e9 = [\"/b/#restart\"]",
		"it\u2019s back = [\"/b/#restart\"]",
		`settings. = ["/c/"]`,
		`a = ["/b/#restart","/a/","/a/#move","/a/#systems"]`,
	}, "\n")
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// The box, driven on a fake page: typing shows the results and says how
// many, Arrow Down moves into them, one Escape closes them, a press on a
// result keeps the focus so the click is followed, a click on one closes
// the list, and an index that cannot be read says so.
func TestDocsSearchBox(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := `
async function run(fails) {
  var document = { activeElement: null, on: {} };
  function el(tag) {
    return { tag: tag, children: [], hidden: tag === "ul", on: {}, className: "", textContent: "", parent: null,
      addEventListener: function (t, f) { this.on[t] = f; },
      appendChild: function (c) { c.parent = this; this.children.push(c); return c; },
      replaceChildren: function () { this.children = []; },
      focus: function () { document.activeElement = this; if (this.on.focus) this.on.focus({}); },
      contains: function (n) { for (; n; n = n.parent) if (n === this) return true; return false; },
      closest: function (q) { return q === "a" && this.tag === "a" ? this : null; } };
  }
  var form = el("form"), input = el("input"), status = el("p"), list = el("ul");
  form.hidden = true;
  [input, status, list].forEach(function (c) { form.appendChild(c); });
  function anchors() { return list.children.map(function (li) { return li.children[0]; }).filter(function (a) { return a && a.tag === "a"; }); }
  list.querySelectorAll = function () { return anchors(); };
  list.querySelector = function () { return anchors()[0] || null; };
  form.querySelector = function (q) { return q === "input" ? input : q === ".results" ? list : q === "[role=status]" ? status : null; };
  document.querySelector = function (q) { return q === ".search" ? form : null; };
  document.querySelectorAll = function () { return []; };
  document.createElement = el;
  document.addEventListener = function (t, f) { document.on[t] = f; };
  document.documentElement = { classList: { add: function () {} } };
  var index = [{url: "/docs/a/", page: "Start at login", text: "Starts when you log in."},
    {url: "/docs/a/#off", page: "Start at login", section: "Turn it off", text: "Turn start at login off."},
    {url: "javascript:alert(1)", page: "Start", text: "start"}];
  var window = { location: {}, fetch: function () {
    return fails ? Promise.resolve({ ok: false, status: 404 }) : Promise.resolve({ ok: true, json: function () { return Promise.resolve(index); } });
  } };
  eval(require("fs").readFileSync("assets/docs.js", "utf8"));
  var tick = function () { return new Promise(function (r) { setTimeout(r, 0); }); };
  var out = ["shown " + !form.hidden];
  input.focus();
  input.value = "start";
  input.on.input();
  await tick(); await tick();
  out.push("list " + !list.hidden, "status " + status.textContent);
  if (fails) return out.join(", ");
  var no = function () {};
  form.on.keydown({ key: "ArrowDown", preventDefault: no });
  out.push("down " + (document.activeElement === anchors()[0]));
  form.on.keydown({ key: "Escape", preventDefault: no });
  out.push("escape " + list.hidden, "back " + (document.activeElement === input));
  input.focus();
  var kept = false;
  list.on.mousedown({ preventDefault: function () { kept = true; } });
  out.push("press kept " + kept);
  list.on.click({ target: anchors()[0] });
  out.push("click " + list.hidden);
  return out.join(", ");
}
run(false).then(function (a) { console.log(a); return run(true); }).then(function (b) { console.log(b); });
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := "shown true, list true, status 2 results, down true, escape true, back true, press kept true, click true\n" +
		"shown true, list true, status Search is not available right now."
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
