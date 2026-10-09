// Package site holds the checks for the static website in this folder.
// It has no code of its own: the site is plain files, and these tests
// keep them honest about links, text and the hosts they may reach.
package site

import (
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// binary reports whether a file is not text and is skipped by the text checks.
func binary(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".webp", ".woff2":
		return true
	}
	return false
}

// siteFiles lists every file of the site, relative to this folder, without
// the Go test files.
func siteFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, ".go") {
			return nil
		}
		out = append(out, filepath.ToSlash(path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func readSite(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryTextFileIsPlainASCII(t *testing.T) {
	for _, f := range siteFiles(t) {
		if binary(f) {
			continue
		}
		for i, c := range []byte(readSite(t, f)) {
			if c >= 0x7f || (c < 0x20 && c != '\n' && c != '\t' && c != '\r') {
				t.Errorf("%s: byte 0x%02x at %d is not plain ASCII", f, c, i)
				break
			}
		}
	}
}

var absURL = regexp.MustCompile(`(?i)\b(?:https?:)?//[a-z0-9.-]+[^\s"'<>)]*`)

var allowedURL = []string{
	"https://ccbabysitter.dev",
	"https://github.com/pejmanebrahimi/ccbabysitter",
	"https://github.com/anthropics/claude-code/issues/",
	"https://www.youtube-nocookie.com/",
	"http://www.w3.org/2000/svg",
	"https://schema.org",
	"https://opensource.org/licenses/MIT",
	"http://www.sitemaps.org/schemas/sitemap/0.9",
	// The address CC Babysitter serves its page on, as the command
	// reference quotes it.
	"http://127.0.0.1",
}

// portThenRest matches what may follow an allowed host: a port, digits
// or PORT as the docs write it in examples, then the end or a path, query
// or fragment.
var portThenRest = regexp.MustCompile(`^:(?:[0-9]+|PORT)(?:$|[/#?])`)

// allowedAddress reports whether u is one of the allowed addresses: it
// must end where the allowed one ends, or go on into a port, path, query
// or fragment, never into another host or name. An allowed address that
// ends in a slash is already a path prefix.
func allowedAddress(u string) bool {
	for _, a := range allowedURL {
		if !strings.HasPrefix(u, a) {
			continue
		}
		rest := u[len(a):]
		if rest == "" || strings.HasSuffix(a, "/") || strings.ContainsRune("/#?", rune(rest[0])) || portThenRest.MatchString(rest) {
			return true
		}
	}
	return false
}

func TestNoThirdPartyHosts(t *testing.T) {
	for _, f := range siteFiles(t) {
		if binary(f) || strings.HasSuffix(f, "OFL.txt") {
			continue
		}
		for _, u := range absURL.FindAllString(readSite(t, f), -1) {
			if !allowedAddress(u) {
				t.Errorf("%s: %s is not an allowed address", f, u)
			}
		}
	}
}

var localRef = regexp.MustCompile(`(?:href|src)="(/[^"#?]*)`)

// siteRef finds absolute references to this site, such as the canonical
// address, the sharing image or the links in llms.txt and sitemap.xml.
var siteRef = regexp.MustCompile(`https://ccbabysitter\.dev(/[^\s"'<>()\[\]` + "`" + `#?]*)`)

// cssRef finds url(...) references to site-absolute files in stylesheets.
var cssRef = regexp.MustCompile(`url\(\s*["']?(/[^"')#?\s]*)`)

// TestInternalLinksResolve checks that every site-absolute href and src,
// and every https://ccbabysitter.dev address in a page, llms.txt or the
// sitemap, points at a file in this folder. /install.sh and /install.ps1
// are added by the deploy workflow from scripts/, so they are checked there
// instead.
func TestInternalLinksResolve(t *testing.T) {
	for _, f := range siteFiles(t) {
		var refs [][]string
		s := ""
		if !binary(f) {
			s = readSite(t, f)
		}
		switch {
		case strings.HasSuffix(f, ".html"):
			refs = append(localRef.FindAllStringSubmatch(s, -1), siteRef.FindAllStringSubmatch(s, -1)...)
		case strings.HasSuffix(f, ".txt"), strings.HasSuffix(f, ".xml"):
			refs = siteRef.FindAllStringSubmatch(s, -1)
		case strings.HasSuffix(f, ".css"):
			refs = cssRef.FindAllStringSubmatch(s, -1)
		default:
			continue
		}
		for _, m := range refs {
			p := m[1]
			if p == "/install.sh" || p == "/install.ps1" {
				continue
			}
			target := strings.TrimPrefix(p, "/")
			if target == "" || strings.HasSuffix(target, "/") {
				target += "index.html"
			}
			if _, err := os.Stat(filepath.FromSlash(target)); err != nil {
				t.Errorf("%s links to %s, which is not in site/", f, p)
			}
		}
	}
}

var (
	srcAttr    = regexp.MustCompile(`\bsrc="([^"]*)"`)
	stylesheet = regexp.MustCompile(`<link\b[^>]*\brel="stylesheet"[^>]*>`)
	hrefAttr   = regexp.MustCompile(`\bhref="([^"]*)"`)
	cssURL     = regexp.MustCompile(`url\(\s*["']?([^"')\s]*)`)
)

// TestResourcesAreSiteAbsolute checks that every script, image, stylesheet
// and font the pages load is addressed from the root of this site, so none
// can come from another host or break on a page in a subfolder.
func TestResourcesAreSiteAbsolute(t *testing.T) {
	check := func(f, kind, u string) {
		if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") {
			t.Errorf("%s: %s %q must start with a single /", f, kind, u)
		}
	}
	for _, f := range siteFiles(t) {
		switch {
		case strings.HasSuffix(f, ".html"):
			s := readSite(t, f)
			for _, m := range srcAttr.FindAllStringSubmatch(s, -1) {
				check(f, "src", m[1])
			}
			for _, l := range stylesheet.FindAllString(s, -1) {
				m := hrefAttr.FindStringSubmatch(l)
				if m == nil {
					t.Errorf("%s: stylesheet link without href: %s", f, l)
					continue
				}
				check(f, "stylesheet", m[1])
			}
		case strings.HasSuffix(f, ".css"):
			for _, m := range cssURL.FindAllStringSubmatch(readSite(t, f), -1) {
				check(f, "url()", m[1])
			}
		}
	}
}

func TestInstallCommandIsInTheHTML(t *testing.T) {
	if !strings.Contains(readSite(t, "index.html"), "curl -fsSL https://ccbabysitter.dev/install.sh | sh") {
		t.Error("index.html does not contain the install command in its static HTML")
	}
}

var (
	navLinks = regexp.MustCompile(`(?s)<nav class="links"[^>]*>(.*?)</nav>`)
	anchor   = regexp.MustCompile(`(?s)<a\b([^>]*)>(.*?)</a>`)
)

// TestHomeLinksLeadToTheDocsAndReleases checks that the home page's links
// are exactly Get started, at the first docs tutorial, Docs, at the docs
// overview, and Downloads, at the latest release.
func TestHomeLinksLeadToTheDocsAndReleases(t *testing.T) {
	m := navLinks.FindStringSubmatch(readSite(t, "index.html"))
	if m == nil {
		t.Fatal(`index.html has no <nav class="links">`)
	}
	links := anchor.FindAllStringSubmatch(m[1], -1)
	want := [][2]string{
		{"/docs/first-session/", "Get started"},
		{"/docs/", "Docs"},
		{"https://github.com/pejmanebrahimi/ccbabysitter/releases/latest", "Downloads"},
	}
	if len(links) != len(want) {
		t.Fatalf("the home links are %d, want %d", len(links), len(want))
	}
	for i, w := range want {
		label := strings.HasPrefix(links[i][2], w[1]+"<") || strings.HasPrefix(links[i][2], w[1]+" <")
		if !strings.Contains(links[i][1], `href="`+w[0]+`"`) || !label {
			t.Errorf("home link %d is %q %q, want %q to %s", i+1, links[i][1], links[i][2], w[1], w[0])
		}
	}
}

// TestOutsideLinksOpenInANewTab checks that every link to another site
// opens in a new tab without handing that site this page, and says so to
// screen readers, and that links within the site stay in the same tab.
// The go-import pages are left out: they are for the go tool, unstyled and
// noindex, with nothing to come back to.
func TestOutsideLinksOpenInANewTab(t *testing.T) {
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") || strings.HasPrefix(f, "ccbabysitter/") {
			continue
		}
		for _, m := range anchor.FindAllStringSubmatch(readSite(t, f), -1) {
			attrs, body := m[1], m[2]
			switch {
			case strings.Contains(attrs, `href="https://`):
				rel := regexp.MustCompile(`\brel="([^"]*)"`).FindStringSubmatch(attrs)
				if !strings.Contains(attrs, `target="_blank"`) || rel == nil || !strings.Contains(rel[1], "noopener") {
					t.Errorf(`%s: outside link without target="_blank" and rel="noopener": <a%s>`, f, attrs)
				}
				if !strings.Contains(body, `<span class="sr"> (opens in a new tab)</span>`) && !regexp.MustCompile(`\baria-label="[^"]* \(opens in a new tab\)"`).MatchString(attrs) {
					t.Errorf("%s: outside link does not say it opens in a new tab: <a%s>", f, attrs)
				}
			case strings.Contains(attrs, `href="/`):
				if strings.Contains(attrs, "target=") {
					t.Errorf("%s: link within the site leaves the tab: <a%s>", f, attrs)
				}
			}
		}
	}
}

var placeholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// TestOnlyTheVersionPlaceholder checks that {{VERSION}} is the only
// placeholder in the site, since it is the only one the deploy workflow
// fills in.
func TestOnlyTheVersionPlaceholder(t *testing.T) {
	for _, f := range siteFiles(t) {
		if binary(f) {
			continue
		}
		for _, p := range placeholder.FindAllString(readSite(t, f), -1) {
			if p != "{{VERSION}}" {
				t.Errorf("%s has the placeholder %s, which the deploy workflow does not fill in", f, p)
			}
		}
	}
}

// TestHomeIntroAndSeeMore checks that the short intro line, which search
// results and link previews show too, the "See more" disclosure with the
// longer story and its notes are in the static HTML, where search engines and visitors without
// scripts can read them, and that the old story and the old "What it does"
// section are gone.
func TestHomeIntroAndSeeMore(t *testing.T) {
	home := readSite(t, "index.html")
	short := "Turned on Claude Code Remote Control, checked &quot;keep computer awake&quot; and went on vacation? I admire your optimism, but you need a babysitter."
	for _, want := range []string{
		`<meta name="description" content="` + short + `">`,
		`<meta property="og:description" content="` + short + `">`,
		`"description": "Turned on Claude Code Remote Control, checked \"keep computer awake\" and went on vacation? I admire your optimism, but you need a babysitter."`,
		`<p class="tagline">Turned on Claude Code Remote Control, checked "keep computer awake" and went on vacation? I admire your optimism, but you need a babysitter.</p>`,
		`<div class="why-body">
        <p>No matter how hard you hope or try, your computer and the Claude Code app will find a creative way<sup><a class="fn" href="#note-1" id="ref-1" aria-label="Note 1">1</a></sup> to kill the remote-controlled session you started before leaving the computer, hoping you could keep issuing destiny-defining orders to your coding agents while you're vacationing where the map gives up.`,
		`Well, this has happened to me, and believe me, we are not alone<sup><a class="fn" href="#note-2" id="ref-2" aria-label="Note 2">2</a></sup>. Of course, you can go ahead and write some watchdog scripts on a laptop, or tmux and whatnot on a server, but some of us prefer a babysitter.</p>`,
		`<details class="why" id="why">`, "<summary>See more <svg",
		"<p>If a babysat session's app dies, the session comes back in the background with Remote Control on, and the computer stays awake. On a server, babysat sessions survive SSH drops and reboots, no tmux needed. And one page shows every Claude Code session on the machine, in any app, with its tokens and uptime. Your agents can use it too: <code>ccbabysitter babysit self</code>.</p>",
		`<ol class="notes">`,
		`<li id="note-1">The ones I've run into: the Claude app updates itself and, for some reason, fails to start again; or it does come back after the update, but only some of the remote-controlled sessions are restored. Your computer or server can also act up and restart itself, for any reason, good or bad.</li>`,
		`<li id="note-2">Reports on Claude Code's issue tracker:`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("index.html does not contain %q in its static HTML", want)
		}
	}
	for _, n := range []string{"95364", "94049", "99585", "100114", "89599"} {
		if !strings.Contains(home, `href="https://github.com/anthropics/claude-code/issues/`+n+`"`) {
			t.Errorf("note 2 has no link to issue %s", n)
		}
	}
	for _, gone := range []string{"Why a babysitter?", "I left Claude Code working on my laptop overnight", "CC Babysitter does both.", "Left Claude Code running with Remote Control, came back to a dead session?"} {
		if strings.Contains(home, gone) {
			t.Errorf("index.html still contains %q", gone)
		}
	}
	for _, gone := range []string{"What it does", `class="about"`, "Why it exists"} {
		if strings.Contains(home, gone) {
			t.Errorf("index.html still contains %q", gone)
		}
	}
}

func TestFooterCarriesTheVersionPlaceholder(t *testing.T) {
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") || strings.HasPrefix(f, "ccbabysitter/") {
			continue
		}
		want := `{{VERSION}} &middot; Preview &middot; <a href="https://github.com/pejmanebrahimi/ccbabysitter/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">MIT<span class="sr"> (opens in a new tab)</span></a> &middot; Not affiliated with Anthropic`
		if !strings.Contains(readSite(t, f), want) {
			t.Errorf("%s has no footer line %q", f, want)
		}
	}
}

func TestEveryPageHasTheBasics(t *testing.T) {
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") || strings.HasPrefix(f, "ccbabysitter/") {
			continue
		}
		s := readSite(t, f)
		wants := []string{`<html lang="en">`, `<meta name="viewport"`, "<title>", `<header`, `<main`, `<footer`, `/assets/theme.js`, `/assets/site.css`}
		// 404.html is noindex: it has no address of its own to name and
		// nothing for a search result to describe.
		if f != "404.html" {
			wants = append(wants, `<link rel="canonical"`, `<meta name="description"`)
		}
		for _, want := range wants {
			if !strings.Contains(s, want) {
				t.Errorf("%s is missing %q", f, want)
			}
		}
		if strings.Count(s, "<h1") != 1 {
			t.Errorf("%s must have exactly one h1", f)
		}
	}
}

var descriptionTag = regexp.MustCompile(`<meta (?:name="description"|property="og:description") content="([^"]*)"`)

// TestDescriptionsAreShort keeps every page's description short enough
// that search results and link previews show it whole.
func TestDescriptionsAreShort(t *testing.T) {
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") {
			continue
		}
		for _, m := range descriptionTag.FindAllStringSubmatch(readSite(t, f), -1) {
			if n := len(m[1]); n > 160 {
				t.Errorf("%s: description is %d characters, more than 160: %q", f, n, m[1])
			}
		}
	}
}

// luminance is the WCAG relative luminance of a #rrggbb colour.
func luminance(t *testing.T, hex string) float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("%q is not a #rrggbb colour", hex)
	}
	var l float64
	for i, w := range []float64{0.2126, 0.7152, 0.0722} {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("%q: %v", hex, err)
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		l += w * c
	}
	return l
}

func contrast(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

var colorToken = regexp.MustCompile(`--(paper|sky|ground|ink|grey|prompt):\s*(#[0-9a-fA-F]{6})`)

// TestTextColoursAreReadable checks WCAG AA (4.5:1) for the ink, grey and
// prompt colours on the paper, and on the ground the home page's text sits
// on below the horizon, in the light theme, in the dark theme chosen by
// hand, and in the dark theme chosen by the system.
func TestTextColoursAreReadable(t *testing.T) {
	css := readSite(t, "assets/site.css")
	blocks := map[string]*regexp.Regexp{
		"light":         regexp.MustCompile(`(?m)^:root\s*\{([^}]*)\}`),
		"dark (manual)": regexp.MustCompile(`:root\[data-theme="dark"\]\s*\{([^}]*)\}`),
		"dark (system)": regexp.MustCompile(`:root:not\(\[data-theme="light"\]\)\s*\{([^}]*)\}`),
	}
	for name, re := range blocks {
		m := re.FindStringSubmatch(css)
		if m == nil {
			t.Errorf("site.css has no %s colour block", name)
			continue
		}
		tok := map[string]string{}
		for _, c := range colorToken.FindAllStringSubmatch(m[1], -1) {
			tok[c[1]] = c[2]
		}
		for _, k := range []string{"paper", "sky", "ground", "ink", "grey", "prompt"} {
			if tok[k] == "" {
				t.Errorf("%s: no --%s colour", name, k)
			}
		}
		for _, bg := range []string{"paper", "ground"} {
			if tok[bg] == "" {
				continue
			}
			for _, k := range []string{"ink", "grey", "prompt"} {
				if tok[k] == "" {
					continue
				}
				if r := contrast(t, tok[k], tok[bg]); r < 4.5 {
					t.Errorf("%s: --%s %s on --%s %s has contrast %.2f, below 4.5", name, k, tok[k], bg, tok[bg], r)
				}
			}
		}
	}
}

// TestHomeHasSkyAndGround checks that the home page paints the sky above
// the horizon and the ground below it, and that only a page with a horizon
// does: the 404 page keeps the paper.
func TestHomeHasSkyAndGround(t *testing.T) {
	css := readSite(t, "assets/site.css")
	if !regexp.MustCompile(`(?m)^html:has\(\.top\), body:has\(\.top\) \{ background: var\(--ground\); \}`).MatchString(css) {
		t.Error("site.css does not paint a page with a .top in --ground")
	}
	m := regexp.MustCompile(`(?m)^\.top\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if m == nil || !strings.Contains(m[1], "background: var(--sky);") {
		t.Error("site.css does not paint .top in --sky")
	}
	if !strings.Contains(readSite(t, "index.html"), `<header class="top" data-sky>`) || strings.Contains(readSite(t, "404.html"), `class="top"`) {
		t.Error("the home page must have a .top and the 404 page none")
	}
}

// TestNotFoundShowsTheLoneBaby checks that the 404 page shows the baby on
// her own, drawn by art.js at 6 px to a pixel, with no kangaroo and no
// picture for a visit without scripts, and links home.
func TestNotFoundShowsTheLoneBaby(t *testing.T) {
	s := readSite(t, "404.html")
	for _, want := range []string{
		`<div class="roo baby" data-roo="baby"></div>`,
		`<h1 class="name">Nothing here, baby.</h1>`,
		`<a href="/">Back to CC Babysitter</a>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("404.html is missing %q", want)
		}
	}
	for _, gone := range []string{"<noscript", `data-roo="still"`, `data-roo="animate"`} {
		if strings.Contains(s, gone) {
			t.Errorf("404.html still contains %q", gone)
		}
	}
	if !strings.Contains(readSite(t, "assets/site.css"), ".lost .roo.baby { width: 24px; height: 36px;") {
		t.Error("site.css does not size the 404 baby at 24 by 36 px")
	}
}

// TestThemeButtonIsAboveThePage checks that the theme switch has a z-index:
// without one, the positioned top of the home page paints over it and
// takes its clicks.
func TestThemeButtonIsAboveThePage(t *testing.T) {
	m := regexp.MustCompile(`(?m)^\.theme\s*\{([^}]*)\}`).FindStringSubmatch(readSite(t, "assets/site.css"))
	if m == nil {
		t.Fatal("site.css has no .theme rule")
	}
	if !regexp.MustCompile(`\bz-index:\s*[1-9]`).MatchString(m[1]) {
		t.Errorf(".theme declares no positive z-index: %s", m[1])
	}
}

func TestRobotsAllowsEveryoneAndNamesTheSitemap(t *testing.T) {
	s := readSite(t, "robots.txt")
	for _, want := range []string{"User-agent: *\nAllow: /", "User-agent: GPTBot", "User-agent: ClaudeBot", "User-agent: PerplexityBot", "User-agent: Google-Extended", "Sitemap: https://ccbabysitter.dev/sitemap.xml"} {
		if !strings.Contains(s, want) {
			t.Errorf("robots.txt is missing %q", want)
		}
	}
	if strings.Contains(s, "Disallow: /") {
		t.Error("robots.txt must not disallow anything")
	}
}

// TestSitemapListsThePages checks that the sitemap lists the home page and
// every docs page, and nothing else.
func TestSitemapListsThePages(t *testing.T) {
	s := readSite(t, "sitemap.xml")
	want := []string{"https://ccbabysitter.dev/"}
	for _, f := range docsPages(t) {
		want = append(want, "https://ccbabysitter.dev"+docsURL(f))
	}
	for _, u := range want {
		if !strings.Contains(s, "<loc>"+u+"</loc>") {
			t.Errorf("sitemap.xml does not list %s", u)
		}
	}
	if n := strings.Count(s, "<loc>"); n != len(want) {
		t.Errorf("sitemap.xml lists %d pages, want %d", n, len(want))
	}
}

func TestSharingAndStructuredData(t *testing.T) {
	home := readSite(t, "index.html")
	for _, want := range []string{
		`<meta property="og:title"`, `<meta property="og:description"`, `<meta property="og:image" content="https://ccbabysitter.dev/og.png">`,
		`<meta property="og:url" content="https://ccbabysitter.dev/">`, `<meta name="twitter:card" content="summary_large_image">`,
		`<script type="application/ld+json">`, `"@type": "SoftwareApplication"`, `"softwareVersion": "{{VERSION}}"`,
		`"license": "https://opensource.org/licenses/MIT"`, `"operatingSystem": "macOS, Windows, Linux"`,
	} {
		if !strings.Contains(home, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
	alt := "A pixel kangaroo with a baby beside her on a horizon line, and the words CC Babysitter."
	for _, want := range []string{`<meta property="og:image:alt" content="` + alt + `">`, `<meta name="twitter:image:alt" content="` + alt + `">`} {
		if !strings.Contains(home, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
}

func TestGoImportPages(t *testing.T) {
	wantImport := `<meta name="go-import" content="ccbabysitter.dev/ccbabysitter git https://github.com/pejmanebrahimi/ccbabysitter">`
	wantSource := `<meta name="go-source" content="ccbabysitter.dev/ccbabysitter https://github.com/pejmanebrahimi/ccbabysitter https://github.com/pejmanebrahimi/ccbabysitter/tree/main{/dir} https://github.com/pejmanebrahimi/ccbabysitter/blob/main{/dir}/{file}#L{line}">`
	pages := []string{"ccbabysitter/index.html", "ccbabysitter/cmd/ccbabysitter/index.html"}
	for _, f := range pages {
		s := readSite(t, f)
		if !strings.Contains(s, wantImport) {
			t.Errorf("%s lacks the exact go-import tag", f)
		}
		if !strings.Contains(s, wantSource) {
			t.Errorf("%s lacks the exact go-source tag", f)
		}
	}
	if readSite(t, pages[0]) != readSite(t, pages[1]) {
		t.Error("the two go-import pages must be byte-identical")
	}
}

func TestCNAMEAndLLMs(t *testing.T) {
	if strings.TrimSpace(readSite(t, "CNAME")) != "ccbabysitter.dev" {
		t.Error("CNAME must be ccbabysitter.dev")
	}
	l := readSite(t, "llms.txt")
	for _, want := range []string{"# CC Babysitter", "curl -fsSL https://ccbabysitter.dev/install.sh | sh", "https://ccbabysitter.dev/docs/", "https://ccbabysitter.dev/docs/first-session/", "https://github.com/pejmanebrahimi/ccbabysitter", "ccbabysitter babysit self", "ccbabysitter --help",
		"/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter", "`ccbabysitter open`: open the page in the browser without printing its key"} {
		if !strings.Contains(l, want) {
			t.Errorf("llms.txt is missing %q", want)
		}
	}
}

// TestTheSkyIsTheHomeHeader checks the home page's markup for the sky: one
// element marked data-sky, the .top, which is the page's header; no corner
// theme button, since the sun and moon are the switch; the label on the
// kangaroo picture rather than on the whole sky; and the scripts in order.
// The 404 page keeps its corner button.
func TestTheSkyIsTheHomeHeader(t *testing.T) {
	home := readSite(t, "index.html")
	if n := strings.Count(home, "data-sky"); n != 1 {
		t.Errorf("index.html has %d data-sky, want 1", n)
	}
	if !strings.Contains(home, `<header class="top" data-sky>`) {
		t.Error(`the .top must be the <header class="top" data-sky>`)
	}
	if strings.Contains(home, "data-theme-switch") {
		t.Error("index.html still has a corner theme button")
	}
	label := "A pixel kangaroo babysits a baby. The baby breaks apart and comes back, safe in her pouch."
	if !strings.Contains(home, `<div class="roo" data-roo="animate" role="img" aria-label="`+label+`">`) {
		t.Error(`the .roo must carry role="img" and the kangaroo's label`)
	}
	if top := regexp.MustCompile(`<[a-z]+ class="top"[^>]*>`).FindString(home); top == "" || strings.Contains(top, "role=") {
		t.Errorf("the .top must have no role: %q", top)
	}
	if n := strings.Count(readSite(t, "404.html"), "data-theme-switch"); n != 1 {
		t.Errorf("404.html has %d theme buttons, want 1", n)
	}
	last, order := -1, []string{"/assets/art.js", "/assets/site.js", "/assets/zones.js", "/assets/sky.js"}
	for _, src := range order {
		i := strings.Index(home, `<script src="`+src+`"></script>`)
		if i < 0 || i < last || i < strings.Index(home, "</main>") {
			t.Errorf("index.html must load %s after </main> and after the script before it, in the order %v", src, order)
		}
		last = i
	}
}

// TestTheSkyHasItsStylesAndData checks the stylesheet's sky rules and the
// two names the zone table must hold.
func TestTheSkyHasItsStylesAndData(t *testing.T) {
	css := readSite(t, "assets/site.css")
	for _, want := range []string{"scrollbar-gutter: stable both-edges", ".orb", ".cap"} {
		if !strings.Contains(css, want) {
			t.Errorf("site.css does not contain %q", want)
		}
	}
	zones := readSite(t, "assets/zones.js")
	for _, want := range []string{`"Europe/Helsinki":[60.17,24.97]`, `"Asia/Calcutta":"Asia/Kolkata"`} {
		if !strings.Contains(zones, want) {
			t.Errorf("zones.js does not contain %q", want)
		}
	}
}

// TestInstallTabs checks the install commands are tabs, in this order, with
// macOS & Linux selected in the static HTML for every visitor, that the tab
// labels are the names in site.js, and that a long command wraps in its box
// rather than scrolling.
func TestInstallTabs(t *testing.T) {
	home := readSite(t, "index.html")
	if !strings.Contains(home, `<div class="tabs" role="tablist" aria-label="Install for" data-tabs="install">`) {
		t.Error("index.html has no install tab list")
	}
	if strings.Contains(home, "data-env-switch") {
		t.Error("index.html still has the rotating switch")
	}
	tab := regexp.MustCompile(`<button class="tab" type="button" role="tab" aria-selected="(true|false)" aria-controls="install"( tabindex="-1")?>([^<]*)</button>`)
	var labels, selected []string
	for _, m := range tab.FindAllStringSubmatch(home, -1) {
		labels = append(labels, m[3])
		selected = append(selected, m[1])
		if (m[1] == "true") == (m[2] != "") {
			t.Errorf("tab %q: only the selected tab is in the Tab order", m[3])
		}
	}
	if got := strings.Join(labels, "|"); got != "macOS &amp; Linux|Windows|Claude Code|Go" {
		t.Errorf("tabs are %q", got)
	}
	if got := strings.Join(selected, "|"); got != "true|false|false|false" {
		t.Errorf("selected tabs are %q", got)
	}
	js := readSite(t, "assets/site.js")
	var names []string
	for _, m := range regexp.MustCompile(`\{ name: "([^"]*)"`).FindAllStringSubmatch(js, -1) {
		names = append(names, m[1])
	}
	if got := strings.Join(names, "|"); got != strings.Join(labels, "|") {
		t.Errorf("site.js names %q, tabs %q", got, strings.Join(labels, "|"))
	}
	if !strings.Contains(js, `{ name: "Claude Code", prompt: "&gt;", cmd: "/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter" }`) {
		t.Error("site.js has no Claude Code entry")
	}
	if strings.Contains(js, "navigator.platform") {
		t.Error("site.js still picks a tab by platform")
	}
	css := readSite(t, "assets/site.css")
	if !strings.Contains(css, `.tab[aria-selected="true"]`) {
		t.Error("site.css does not style the selected tab")
	}
	text := regexp.MustCompile(`\.cmd \.text \{[^}]*\}`).FindString(css)
	if !strings.Contains(text, "white-space: normal") || !strings.Contains(text, "overflow-wrap: anywhere") || strings.Contains(text, "overflow-x") {
		t.Errorf("a long command does not wrap: %q", text)
	}
}

// An allowed address may go on into a port, a path, a query or a
// fragment, and never into another host, as with a longer name or a
// user before an @.
func TestAllowedAddress(t *testing.T) {
	for _, c := range []struct {
		u  string
		ok bool
	}{
		{"https://ccbabysitter.dev", true},
		{"https://ccbabysitter.dev/docs/", true},
		{"https://ccbabysitter.dev#top", true},
		{"http://127.0.0.1:47391/?token=KEY", true},
		{"http://127.0.0.1:PORT", true},
		{"http://127.0.0.1:PORT/?token=KEY", true},
		{"https://github.com/pejmanebrahimi/ccbabysitter/issues", true},
		{"https://ccbabysitter.dev.evil.com", false},
		{"http://127.0.0.1.evil.com", false},
		{"https://ccbabysitter.dev:443@evil.com/x", false},
		{"http://127.0.0.1:8080@evil.com", false},
		{"https://schema.org:@evil.com", false},
		{"http://127.0.0.1:80x", false},
		{"https://github.com/pejmanebrahimi/ccbabysitter-evil", false},
	} {
		if got := allowedAddress(c.u); got != c.ok {
			t.Errorf("allowedAddress(%q) = %v, want %v", c.u, got, c.ok)
		}
	}
}
