package web

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// uiFiles is every file the page is made of, in the embedded filesystem.
var uiFiles = []string{"ui/index.html", "ui/app.css", "ui/app.js", "ui/theme.js", "ui/art.js", "ui/art.css", "ui/favicon.svg"}

func readUI(t *testing.T, name string) string {
	t.Helper()
	b, err := uiFS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The sentences below are shown to the user word for word. They are
// repeated here so that rewording one of them in the page is a deliberate
// change with a failing test behind it, not an accident.
func TestPageSentencesThatMustNotDrift(t *testing.T) {
	index := readUI(t, "ui/index.html")
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		"Shows every Claude Code session, and keeps the ones you choose alive, awake and reachable.",
		"No Claude Code sessions are running.",
		"Nothing from the last 14 days.",
		"Stops the background copy. The conversation is kept.",
		"A response in progress is lost.",
		"Put the program somewhere it will stay before you switch this on.",
		"Start CC Babysitter at login so this survives a restart",
		`data-act="copy-ssh"`,
		"Copy attach",
		`<span id="awake-text">Computer not kept awake</span>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain, word for word:\n%s", want)
		}
	}
	for _, want := range []string{
		`"Keeps the computer awake. "`,
		`"If "`,
		`" closes, this session continues in the background with Remote Control."`,
		`"If this session stops, it is started again in the background with Remote Control."`,
		`"If the terminal or your SSH connection closes, this session continues in the background with Remote Control."`,
		`"Remote Control is off, so your other devices can't reach it yet: "`,
		`"Computer awake: "`,
		`"Computer not kept awake"`,
		"Not running. Start CC Babysitter to reconnect.",
		`"open in "`,
		"startAtLogin: startAtLogin",
		`"Copy it by hand: "`,
		"w.canUnbabysit",
		"Waiting for apps to restore their sessions first",
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain, word for word:\n%s", want)
		}
	}
}

// A session whose app is the background has no app to close, so its
// babysit dialog makes a promise of its own rather than one about "its
// background copy" closing.
func TestTheBabysitPromiseForABackgroundSession(t *testing.T) {
	app := readUI(t, "ui/app.js")
	if strings.Contains(app, `background: "its background copy"`) {
		t.Error("a background session is not promised anything about its background copy closing")
	}
	if !strings.Contains(app, `s.host === "background"`) {
		t.Error("the babysit dialog must tell a background session apart")
	}
}

// Start at login begins unticked; the person opts in each time rather
// than opting out of something switched on for them.
func TestBabysitLoginCheckboxStartsUnticked(t *testing.T) {
	index := readUI(t, "ui/index.html")
	tag := regexp.MustCompile(`<input type="checkbox" id="babysit-login"[^>]*>`).FindString(index)
	if tag == "" {
		t.Fatal("could not find the babysit-login checkbox in index.html")
	}
	if strings.Contains(tag, "checked") {
		t.Errorf("the babysit-login checkbox must not start checked:\n%s", tag)
	}

	app := readUI(t, "ui/app.js")
	if !strings.Contains(app, `$("#babysit-login").checked = false;`) {
		t.Error("app.js must set #babysit-login unchecked when the dialog opens")
	}
}

// The command offered when the clipboard refuses is something the person
// has to select and copy, so that toast stays until it is dismissed, and
// newer toasts push out the ones that would go anyway before it.
func TestTheCopyByHandToastStaysUntilDismissed(t *testing.T) {
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`toast("Copy it by hand: " + value, true, true)`,
		`function toast(message, bad, stays)`,
		`node.classList.toggle("stays", !!stays)`,
		`if (!stays) {`,
		`list.querySelector(".toast:not(.stays)")`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
}

// The page is one header row, a line of facts, three sections that fold,
// the last folded to start with, and an activity panel that sits beside
// the sessions rather than over them.
func TestPageLayout(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<header class="bar">`, `<p class="info" id="info">`, `id="search"`,
		`<details class="sect" id="sect-babysat" open>`,
		`<details class="sect" id="sect-running" open>`,
		`<details class="sect" id="sect-past">`,
		`<aside class="activity" id="activity"`,
		`id="toggle-activity"`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	if strings.Index(index, `id="activity"`) > strings.Index(index, `class="toasts"`) {
		t.Error("the activity panel belongs inside the layout, beside the sessions")
	}
	for _, gone := range []string{"drawer-activity", "open-activity", "tpl-target"} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries %q", gone)
		}
	}
}

// jsSentences finds the double-quoted strings in a script that read as
// sentences: they start with a capital letter and hold a space.
var jsSentences = regexp.MustCompile(`"([A-Z][^"\n]* [^"\n]*)"`)

// Every line the page says is one line, with no parentheses and no
// semicolons.
func TestPageSentencesAreOneLineWithoutParenthesesOrSemicolons(t *testing.T) {
	app := readUI(t, "ui/app.js")
	for _, m := range jsSentences.FindAllStringSubmatch(app, -1) {
		if strings.ContainsAny(m[1], "();") {
			t.Errorf("app.js says %q", m[1])
		}
	}
	index := readUI(t, "ui/index.html")
	for _, m := range regexp.MustCompile(`>([^<]+)<`).FindAllStringSubmatch(index, -1) {
		if text := strings.TrimSpace(m[1]); text != "" && strings.ContainsAny(text, "();") {
			t.Errorf("index.html says %q", text)
		}
	}
}

// The program is called CC Babysitter, and that is what it calls itself
// wherever somebody reads it. "Babysit", "babysat" and "babysitting" are
// ordinary words here and stay as they are; the name is the thing that
// must not lose its first two letters.
func TestUICallsTheProgramByItsWholeName(t *testing.T) {
	index := readUI(t, "ui/index.html")
	if !strings.Contains(index, "CC Babysitter") {
		t.Error("index.html never names the program CC Babysitter")
	}
	for _, name := range uiFiles {
		body := readUI(t, name)
		if strings.Contains(body, "while Babysitter") {
			t.Errorf("%s says \"while Babysitter\" where it means \"while CC Babysitter\"", name)
		}
		for _, at := range wordmarks(body) {
			if at < 3 || body[at-3:at] != "CC " {
				t.Errorf("%s: the name at offset %d is missing its CC: %q", name, at, excerpt(body, at))
			}
		}
	}
}

// wordmarks reports every offset at which the name "Babysitter" is used as
// a name, which is to say not as the start of a longer word.
func wordmarks(body string) []int {
	var out []int
	for i := 0; i+len("Babysitter") <= len(body); i++ {
		if !strings.HasPrefix(body[i:], "Babysitter") {
			continue
		}
		rest := body[i+len("Babysitter"):]
		if rest != "" && (rest[0] >= 'a' && rest[0] <= 'z') {
			continue
		}
		out = append(out, i)
	}
	return out
}

func excerpt(body string, at int) string {
	from := at - 24
	if from < 0 {
		from = 0
	}
	to := at + 24
	if to > len(body) {
		to = len(body)
	}
	return body[from:to]
}

// The page is fed by an event stream, never by a timer that asks the API
// again, and it never builds markup out of values that came from the
// machine. Both rules are cheap to break by accident and cheap to check.
func TestAppScriptNeitherPollsNorBuildsMarkupFromData(t *testing.T) {
	app := readUI(t, "ui/app.js")
	for _, banned := range []string{"setInterval", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write"} {
		if strings.Contains(app, banned) {
			t.Errorf("app.js must not use %s", banned)
		}
	}
	for _, banned := range []string{"http://", "https://", "data:", "blob:"} {
		if strings.Contains(app, banned) {
			t.Errorf("app.js must not contain the literal %q: the page only ever talks to its own origin", banned)
		}
	}
}

// Every source file in this package stays plain ASCII text, so a symbol
// such as a middle dot or an ellipsis is written as an escape or an entity
// and no editor can quietly change what the page says. Control bytes are
// caught as well as high ones: a single stray byte such as a NUL is enough
// to make every text tool treat the whole file as binary and quietly skip
// it, which is how one could hide in here unnoticed.
func TestUIFilesAreASCIIOnly(t *testing.T) {
	for _, name := range uiFiles {
		body := readUI(t, name)
		for i := 0; i < len(body); i++ {
			if !plainTextByte(body[i]) {
				t.Fatalf("%s: byte 0x%02x at offset %d is not plain ASCII text", name, body[i], i)
			}
		}
		// The long dashes, spelled as code points so that this file stays
		// ASCII too. The byte check above already covers them; naming them
		// says which mistake is being guarded against.
		for _, dash := range []rune{0x2014, 0x2013} {
			if strings.ContainsRune(body, dash) {
				t.Errorf("%s contains a dash that is not a hyphen", name)
			}
		}
	}
}

func TestStaticRoutesServeTheEmbeddedPage(t *testing.T) {
	ts, _, _ := newTS(t)
	for _, tc := range []struct{ path, contentType string }{
		{"/", "text/html"},
		{"/app.css", "text/css"},
		{"/app.js", "text/javascript"},
		{"/theme.js", "text/javascript"},
		{"/art.js", "text/javascript"},
		{"/art.css", "text/css"},
		{"/favicon.svg", "image/svg+xml"},
	} {
		r, err := keyed.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if r.StatusCode != 200 {
			t.Errorf("GET %s: status %d", tc.path, r.StatusCode)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, tc.contentType) {
			t.Errorf("GET %s: Content-Type %q, want %s", tc.path, ct, tc.contentType)
		}
		r.Body.Close()
	}
}

func TestUnknownPathIsNotFound(t *testing.T) {
	ts, _, _ := newTS(t)
	r, err := keyed.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatalf("GET /nope: status %d, want 404", r.StatusCode)
	}
}

// The four Remote Control sentences exist twice: the engine says them for
// a babysat session, and the page says them for a session that is not
// babysat and so has no watch to carry one. They must be word for word the
// same, which is what this compares.
func TestPageRepeatsTheEngineRemoteControlSentences(t *testing.T) {
	app := readUI(t, "ui/app.js")
	body := between(t, app, "function rcHintFor(host, short) {", "\n  }")

	short := "a1b2c3d4"
	cases := []struct {
		host  claude.Host
		token string
	}{
		{claude.HostTerminal, `host === "terminal"`},
		{claude.HostDesktop, `host === "desktop"`},
		{claude.HostVSCode, `host === "vscode"`},
		{claude.HostBackground, `host === "background"`},
	}
	for _, c := range cases {
		want := supervise.RCHint(c.host, short)
		line := lineContaining(t, body, c.token)
		got := jsSentence(t, line, short)
		if got != want {
			t.Errorf("host %s:\n  page:   %q\n  engine: %q", c.host, got, want)
		}
	}
}

// between returns the text of s between the first occurrence of from and
// the first occurrence of to after it.
func between(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("app.js no longer contains %q", from)
	}
	rest := s[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("app.js no longer contains %q after %q", to, from)
	}
	return rest[:j]
}

func lineContaining(t *testing.T, body, token string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, token) {
			return line
		}
	}
	t.Fatalf("no line of rcHintFor mentions %q:\n%s", token, body)
	return ""
}

// jsSentence reads the sentence out of one return statement: the pieces of
// the expression joined in the order they are written, with the one call
// the page makes replaced by what it would produce.
func jsSentence(t *testing.T, line, short string) string {
	t.Helper()
	i := strings.Index(line, "return ")
	if i < 0 {
		t.Fatalf("no return statement in %q", line)
	}
	expr := strings.TrimSpace(line[i+len("return "):])
	expr = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(expr, "}")), ";")

	var out strings.Builder
	for _, piece := range strings.Split(expr, "+") {
		piece = strings.TrimSpace(piece)
		switch {
		case piece == "attachCommand(short)":
			out.WriteString("claude attach " + short)
		case strings.HasPrefix(piece, `"`) && strings.HasSuffix(piece, `"`):
			out.WriteString(strings.Trim(piece, `"`))
		default:
			t.Fatalf("unexpected piece %q in %q", piece, line)
		}
	}
	return out.String()
}

// Moving a session between apps is gone from the program, so nothing on the
// page may still offer it or post to the routes that did it.
func TestNoMoveControlRemains(t *testing.T) {
	index := readUI(t, "ui/index.html")
	app := readUI(t, "ui/app.js")
	for _, gone := range []string{"dlg-move", "tpl-target", `data-act="move"`, `data-act="return"`, `data-act="move-bg"`, "explainer", "babysit-bg", "unbabysit-back"} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries %q", gone)
		}
	}
	for _, gone := range []string{"/move", "moveBack", "explainer", "availableTargets", "canReturn", "movedByUser", "canMoveToBackgroundWithRc"} {
		if strings.Contains(app, gone) {
			t.Errorf("app.js still uses %q", gone)
		}
	}
}

// The tunnel banner is gone from the page: the command to reach it from
// another computer is now printed to the console instead, so nothing here
// may still show it, hold it or wire up a copy button for it.
func TestNoTunnelBannerRemains(t *testing.T) {
	for _, name := range []string{"ui/index.html", "ui/app.js", "ui/app.css"} {
		body := strings.ToLower(readUI(t, name))
		if strings.Contains(body, "tunnel") {
			t.Errorf("%s still carries a tunnel reference", name)
		}
	}
}

// The keep-awake rule is not a choice any more, so no control for it may be
// left anywhere on the page: a control that cannot change anything is worse
// than no control at all.
func TestNoKeepAwakeControlRemains(t *testing.T) {
	index := readUI(t, "ui/index.html")
	app := readUI(t, "ui/app.js")
	for _, gone := range []string{
		"awake-seg", "set-awake-seg", "While babysitting",
		`data-mode="always"`, `data-mode="babysitting"`, `data-mode="off"`,
	} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries the keep-awake control: %q", gone)
		}
	}
	for _, gone := range []string{"awake-seg", "set-awake-seg", "keepAwakeMode", "While babysitting", `"always"`} {
		if strings.Contains(app, gone) {
			t.Errorf("app.js still drives the keep-awake control: %q", gone)
		}
	}
}

// artRow matches one row of a pixel grid in art.js: a quoted string on a
// line of its own. Nothing else in the script is written that way.
var artRow = regexp.MustCompile(`(?m)^\s*"([^"]*)",?\s*$`)

// Every picture is a 16 by 16 grid: four poses, the baby in the air and
// five icons. A row of the wrong length would shift every pixel after it.
func TestArtIsDrawnOnSixteenBySixteenGrids(t *testing.T) {
	art := readUI(t, "ui/art.js")
	rows := artRow.FindAllStringSubmatch(art, -1)
	if len(rows) != 10*16 {
		t.Fatalf("want 160 grid rows for ten pictures, found %d", len(rows))
	}
	for _, r := range rows {
		if len(r[1]) != 16 {
			t.Errorf("a grid row is %d pixels wide, want 16: %q", len(r[1]), r[1])
		}
	}
	for _, marker := range []string{"calm: [", "alert: [", "worried: [", "asleep: [", "var HOP = [", "desktop: [", "terminal: [", "vscode: [", "background: [", "other: ["} {
		artGrid(t, art, marker)
	}
}

// artGrid returns the 16 rows of the picture that starts at marker.
func artGrid(t *testing.T, art, marker string) []string {
	t.Helper()
	i := strings.Index(art, marker)
	if i < 0 {
		t.Fatalf("art.js has no picture at %q", marker)
	}
	var grid []string
	for _, m := range artRow.FindAllStringSubmatch(art[i:], 16) {
		grid = append(grid, m[1])
	}
	if len(grid) != 16 {
		t.Fatalf("the picture at %q has %d rows, want 16", marker, len(grid))
	}
	return grid
}

// The baby stands beside the kangaroo only while she is calm, drawn in
// capitals so the hop can hide her, and the hop draws nothing but the same
// baby two rows higher. The other poses carry no baby beside her.
func TestTheBabyStandsBesideTheKangarooOnlyWhenCalm(t *testing.T) {
	art := readUI(t, "ui/art.js")
	calm := artGrid(t, art, "calm: [")
	joined := strings.Join(calm, "")
	for _, ch := range "HSC" {
		if !strings.ContainsRune(joined, ch) {
			t.Errorf("the calm pose has no %q: the baby must stand beside the kangaroo", ch)
		}
	}
	for _, pose := range []string{"alert: [", "worried: [", "asleep: ["} {
		for _, row := range artGrid(t, art, pose) {
			if strings.ToLower(row) != row {
				t.Errorf("%s has a baby beside the kangaroo: %q", strings.TrimSuffix(pose, ": ["), row)
			}
		}
	}
	for y, row := range artGrid(t, art, "var HOP = [") {
		if strings.Trim(row, ".hsc") != "" {
			t.Errorf("the hop draws something that is not the baby: %q", row)
		}
		standing := strings.Repeat(".", 16)
		if y+2 < 16 {
			standing = strings.Map(func(r rune) rune {
				if r >= 'A' && r <= 'Z' {
					return r - 'A' + 'a'
				}
				return '.'
			}, calm[y+2])
		}
		if row != standing {
			t.Errorf("hop row %d is %q, want the standing baby two rows up: %q", y, row, standing)
		}
	}
	css := readUI(t, "ui/art.css")
	for _, want := range []string{".m-o ", ".m-b ", ".m-l ", ".m-d ", ".m-p ", ".m-h ", ".m-s ", ".m-c ", ".m-z ", `.mascot.hopping[data-pose="calm"]`} {
		if !strings.Contains(css, want) {
			t.Errorf("art.css has no %s", want)
		}
	}
	for _, gone := range []string{".m-w ", ".m-j ", ".m-e ", ".m-k ", "--px-joey", "--px-wing", "--px-face", "--px-eye", "--px-beak"} {
		if strings.Contains(css, gone) {
			t.Errorf("art.css still carries %s", gone)
		}
	}
	app := readUI(t, "ui/app.js")
	if !strings.Contains(app, "window.ccbArt.hop(") {
		t.Error("app.js must play the hop when a watch starts")
	}
}

// The favicon is the alert pose, one rectangle for each run of one colour
// in a row, with the colours written out because a favicon cannot read the
// page's stylesheet.
func TestTheFaviconIsTheAlertPose(t *testing.T) {
	colours := map[byte]string{
		'o': "#3a2213", 'b': "#c8834a", 'l': "#f1d3a6", 'd': "#9a5a2c",
		'p': "#20140c", 'h': "#5a3a26", 's': "#f2c9a2",
	}
	var want []string
	for y, row := range artGrid(t, readUI(t, "ui/art.js"), "alert: [") {
		for x := 0; x < len(row); {
			ch := row[x]
			start := x
			for x < len(row) && row[x] == ch {
				x++
			}
			if ch == '.' {
				continue
			}
			fill, ok := colours[ch]
			if !ok {
				t.Fatalf("the alert pose uses %q, which has no favicon colour", ch)
			}
			want = append(want, fmt.Sprintf(`<rect x="%d" y="%d" width="%d" height="1" fill="%s"/>`, start, y, x-start, fill))
		}
	}
	icon := readUI(t, "ui/favicon.svg")
	var got []string
	for _, line := range strings.Split(icon, "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "<rect ") {
			got = append(got, line)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("favicon.svg is not the alert pose, want these %d rects:\n%s", len(want), strings.Join(want, "\n"))
	}
	if !strings.Contains(icon, `shape-rendering="crispEdges"`) {
		t.Error("the favicon must keep its pixels crisp")
	}
	css := readUI(t, "ui/art.css")
	for _, token := range []string{"--px-ink: #3a2213", "--px-body: #c8834a", "--px-light: #f1d3a6", "--px-dark: #9a5a2c", "--px-pupil: #20140c", "--px-hair: #5a3a26", "--px-skin: #f2c9a2", "--px-onesie: #9cc4e4"} {
		if !strings.Contains(css, token) {
			t.Errorf("art.css does not define %s", token)
		}
	}
}

// The kangaroo and the icons are this program's own drawings, and they
// carry no other product's name either.
func TestArtIsOurOwn(t *testing.T) {
	for _, name := range []string{"ui/art.js", "ui/art.css", "ui/favicon.svg"} {
		body := strings.ToLower(readUI(t, name))
		for _, mark := range []string{"claude", "anthropic", "clawd", "vscode-icons", "microsoft"} {
			if strings.Contains(body, mark) {
				t.Errorf("%s mentions %q", name, mark)
			}
		}
	}
}

// The art script follows the page's rules: no timers that poll, no markup
// built from strings, and nothing fetched. The one address it holds is the
// SVG namespace, which every SVG element has to be created in.
func TestArtScriptFollowsThePageRules(t *testing.T) {
	art := readUI(t, "ui/art.js")
	for _, banned := range []string{"setInterval", "innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "fetch(", "https://", "data:", "blob:"} {
		if strings.Contains(art, banned) {
			t.Errorf("art.js must not use %s", banned)
		}
	}
	if n := strings.Count(art, "http://"); n != 1 || !strings.Contains(art, `"http://www.w3.org/2000/svg"`) {
		t.Errorf("art.js may name the SVG namespace and no other address, found %d addresses", n)
	}
	css := readUI(t, "ui/art.css")
	if !strings.Contains(css, "prefers-reduced-motion") || !strings.Contains(css, "animation: none") {
		t.Error("the hop must stop for anyone who asked for less motion")
	}
	icon := readUI(t, "ui/favicon.svg")
	if !strings.Contains(icon, `viewBox="0 0 16 16"`) || strings.Contains(icon, "<script") || strings.Contains(icon, "style") {
		t.Error("the favicon is a plain 16 by 16 drawing with no script and no style")
	}
	index := readUI(t, "ui/index.html")
	for _, want := range []string{`href="/favicon.svg"`, `href="/art.css"`, `src="/art.js"`} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not load %s", want)
		}
	}
	if strings.Index(index, `src="/art.js"`) > strings.Index(index, `src="/app.js"`) {
		t.Error("art.js must load before app.js, which uses it")
	}
}

// plainTextByte reports whether a byte belongs in a plain ASCII text file:
// a printable character, or one of the three whitespace bytes that make up
// lines and indentation.
func plainTextByte(b byte) bool {
	switch b {
	case '\t', '\n', '\r':
		return true
	}
	return b >= 0x20 && b <= 0x7e
}

// Cost estimates and the price table are gone: nothing on the page may
// still show, ask for or style a price or a cost.
func TestThePageHasNoPriceOrCost(t *testing.T) {
	for _, name := range uiFiles {
		body := strings.ToLower(readUI(t, name))
		for _, gone := range []string{"price", "cost", "usd"} {
			if strings.Contains(body, gone) {
				t.Errorf("%s still carries %q", name, gone)
			}
		}
	}
}

// A card shows the model, the tokens a session has read and written and
// the ones its cache has handled, each count written short by the one
// formatter, with the cache split into reads and writes on its title.
func TestCardsShowTheModelAndTokenCounts(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<div class="stat"><dt>Tokens in</dt><dd data-f="tokensIn"></dd></div>`,
		`<div class="stat"><dt>Tokens out</dt><dd data-f="tokensOut"></dd></div>`,
		`<div class="stat"><dt>Cache</dt><dd data-f="cache"></dd></div>`,
		`<div class="stat"><dt>Model</dt><dd data-f="model"></dd></div>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	if strings.Contains(index, `data-f="tokens"`) {
		t.Error("the one Tokens stat is split into in, out and cache")
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`setText(f(node, "tokensIn"), fmtTokens(stats.inputTokens));`,
		`setText(f(node, "tokensOut"), fmtTokens(stats.outputTokens));`,
		`setText(f(node, "cache"), fmtTokens((stats.cacheReadTokens || 0) + (stats.cacheWriteTokens || 0)));`,
		`setTitle(f(node, "cache"), "read " + fmtTokens(stats.cacheReadTokens) + DOT + "write " + fmtTokens(stats.cacheWriteTokens));`,
		`setText(f(node, "model"), stats.model || "");`,
		`show(f(node, "model").parentNode, !!stats.model);`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
}

// Token counts are written short: as they are under a thousand, then in
// thousands, millions or billions with one decimal below ten of the unit
// and none above. The formatter is run as the page runs it, when node is there to
// run it.
func TestTokenCountsAreWrittenShort(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	fn := "function fmtTokens(n) {" + between(t, readUI(t, "ui/app.js"), "function fmtTokens(n) {", "\n  }") + "\n}"
	cases := []struct {
		in   string
		want string
	}{
		{"undefined", "0"}, {"0", "0"}, {"7", "7"}, {"999", "999"},
		{"1000", "1.0k"}, {"1234", "1.2k"}, {"9949", "9.9k"}, {"9950", "10k"},
		{"34000", "34k"}, {"34567", "35k"}, {"999499", "999k"}, {"999500", "1.0M"},
		{"1234567", "1.2M"}, {"12345678", "12M"}, {"987654321", "988M"},
		{"999999999", "1.0B"}, {"1e9", "1.0B"}, {"12345678901", "12B"}, {"1.5e12", "1500B"},
	}
	var script strings.Builder
	script.WriteString(fn + "\n")
	for _, c := range cases {
		script.WriteString("console.log(fmtTokens(" + c.in + "));\n")
	}
	out, err := exec.Command(node, "-e", script.String()).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) != len(cases) {
		t.Fatalf("want %d lines from node, got %q", len(cases), out)
	}
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("fmtTokens(%s) = %q, want %q", c.in, got[i], c.want)
		}
	}
}

// An In background card says what happened while the person was away and
// offers the three ways on from there. Its
// old state lines and Attach row are not shown on it.
func TestTheBackgroundCard(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<div class="rescue" data-el="rescue" hidden>`,
		`<p class="head">Kept alive while you were away</p>`,
		`<div class="journey">`,
		`<span class="arrow"></span>`,
		`data-act="back"`,
		`<a class="b quiet" data-el="remote" target="_blank" rel="noopener noreferrer" hidden>Open on claude.ai<span class="ext"></span></a>`,
		`data-act="open-terminal"`,
		`aria-label="Copy the attach command"><span class="copyglyph"></span></button>`,
		`<p class="acts-hint" data-f="actshint" hidden></p>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`" kept running and stayed reachable from your other devices."`,
		`" kept running in the background."`,
		`desktop: "The desktop app", vscode: "VS Code", terminal: "The terminal"`,
		`"The last two keep it running as it is."`,
		`"It keeps running as it is."`,
		`setText(node.querySelector('[data-act="open-terminal"]'), view.terminalLabel || "");`,
		`"Copy the command: "`,
		`"/open-terminal"`,
		`node.classList.toggle("rescued", rescued)`,
		`show(el(node, "attachbox"), !!w.attachCmd && !rescued)`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
	for _, gone := range []string{"Continuing in the background. Reach it with Remote Control.", "To use it in an app again, stop the background copy first."} {
		if strings.Contains(app, gone) {
			t.Errorf("app.js still says %q", gone)
		}
	}
	css := readUI(t, "ui/app.css")
	for _, want := range []string{".rescue {", ".journey .arrow {", ".acts.stack {", ".split .b.icon {", ".b .ext {", ".copyglyph {", ".acts-hint {"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css does not style %s", want)
		}
	}
}

// Back to the app a session came from ends the background copy after a
// dialog that says what happens, in one of three versions.
func TestTheBackDialog(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<dialog class="dlg" id="dlg-back" aria-labelledby="dlg-back-h">`,
		`data-el="back-desktop"`,
		`<p class="line">The background copy ends and <span data-f="name"></span> goes back to Desktop.</p>`,
		`<p class="line">Open it from Desktop's sidebar to carry on where you left off.</p>`,
		`<p class="line warn">Remote Control stays off until you switch it on in Desktop.</p>`,
		`data-el="back-vscode"`,
		`<p class="line">The background copy ends and <span data-f="name"></span> goes back to VS Code.</p>`,
		`<p class="line">Open it from the Claude Code panel's past conversations to carry on.</p>`,
		`<p class="line warn">Remote Control stays off until you switch it on in the VS Code panel.</p>`,
		`data-el="back-terminal"`,
		`<p class="line">The background copy ends.</p>`,
		`<p class="line">Resume it in any terminal with this command.</p>`,
		`<code class="mono" data-f="resume"></code>`,
		`data-act="copy-resume"`,
		`<p class="line warn">Remote Control stays off until you type /rc in the session.</p>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`"Back to Desktop"`, `"Back to VS Code"`, `"Back to a terminal"`, `"End background copy"`,
		`" is back with Desktop. Open it from the sidebar."`,
		`" is back with VS Code. Open it from past conversations."`,
		`"Background copy ended. Paste the command in any terminal to carry on."`,
		`onDialog($("#dlg-back"), doBack);`,
		`"/stop"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
}

// The card says when the app closed and how long the session has been in
// the background, in so many words. The two formatters are run
// as the page runs them, in UTC, when node is there to run them.
func TestTheBackgroundCardTimes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	app := readUI(t, "ui/app.js")
	script := "var MONTHS = " + between(t, app, "var MONTHS = ", ";") + ";\n" +
		"function pad2(n) {" + between(t, app, "function pad2(n) {", "}") + "}\n" +
		"function clockOf(t, withSeconds) {" + between(t, app, "function clockOf(t, withSeconds) {", "\n  }") + "\n}\n" +
		"function whenOf(iso) {" + between(t, app, "function whenOf(iso) {", "\n  }") + "\n}\n" +
		"function closedWhen(iso, now) {" + between(t, app, "function closedWhen(iso, now) {", "\n  }") + "\n}\n" +
		"function stayFor(iso, now) {" + between(t, app, "function stayFor(iso, now) {", "\n  }") + "\n}\n"
	now := `new Date("2026-09-26T09:00:00Z")`
	cases := []struct{ call, want string }{
		{`closedWhen("2026-09-26T08:46:00Z", ` + now + `)`, "at 08:46"},
		{`closedWhen("2026-09-26T00:00:00Z", ` + now + `)`, "at 00:00"},
		{`closedWhen("2026-09-25T21:30:00Z", ` + now + `)`, "yesterday at 21:30"},
		{`closedWhen("2026-09-22T07:05:00Z", ` + now + `)`, "Sep 22 at 07:05"},
		{`closedWhen("2025-12-31T23:59:00Z", ` + now + `)`, "Dec 31 at 23:59"},
		{`closedWhen("", ` + now + `)`, ""},
		{`stayFor("2026-09-26T08:46:00Z", ` + now + `)`, "for 14 min"},
		{`stayFor("2026-09-26T08:59:50Z", ` + now + `)`, "for 1 min"},
		{`stayFor("2026-09-26T08:00:00Z", ` + now + `)`, "for 1 h"},
		{`stayFor("2026-09-25T19:00:00Z", ` + now + `)`, "for 14 h"},
		{`stayFor("2026-09-24T09:00:01Z", ` + now + `)`, "for 47 h"},
		{`stayFor("2026-09-24T09:00:00Z", ` + now + `)`, "for 2 days"},
		{`stayFor("2026-09-21T08:00:00Z", ` + now + `)`, "for 5 days"},
		{`stayFor("", ` + now + `)`, ""},
	}
	for _, c := range cases {
		script += "console.log(JSON.stringify(" + c.call + "));\n"
	}
	cmd := exec.Command(node, "-e", script)
	cmd.Env = append(cmd.Environ(), "TZ=UTC")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) != len(cases) {
		t.Fatalf("want %d lines from node, got %q", len(cases), out)
	}
	for i, c := range cases {
		if got[i] != `"`+c.want+`"` {
			t.Errorf("%s = %s, want %q", c.call, got[i], c.want)
		}
	}
}

// When the event stream drops, the page asks for the state, and a server
// that answers 401 is running but does not take this page's key: the pill
// says so in the server's own words rather than calling it not running.
// The function that decides is run as the page runs it, when node is
// there to run it.
func TestTheStreamSaysWhenThePageNeedsTheKey(t *testing.T) {
	app := readUI(t, "ui/app.js")
	handler := between(t, app, `stream.addEventListener("error", function () {`, "\n    });")
	if !strings.Contains(handler, "askWhyDown();") {
		t.Fatal("the stream's error handler does not ask why it dropped")
	}
	if !strings.Contains(app, KeyMessage) {
		t.Fatalf("app.js does not say the key message word for word: %q", KeyMessage)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := "function statusMessage(status) {" + between(t, app, "function statusMessage(status) {", "\n  }") + "\n}\n" +
		"function keyRefusal(status, body) {" + between(t, app, "function keyRefusal(status, body) {", "\n  }") + "\n}\n"
	cases := []struct{ call, want string }{
		{`keyRefusal(401, {message: "Server words."})`, "Server words."},
		{`keyRefusal(401, null)`, KeyMessage},
		{`keyRefusal(401, {})`, KeyMessage},
		{`keyRefusal(200, {message: "x"})`, ""},
		{`keyRefusal(503, {message: "Too many."})`, ""},
		{`keyRefusal(403, null)`, ""},
	}
	for _, c := range cases {
		script += "console.log(JSON.stringify(" + c.call + "));\n"
	}
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(got) != len(cases) {
		t.Fatalf("want %d lines from node, got %q", len(cases), out)
	}
	for i, c := range cases {
		if got[i] != `"`+c.want+`"` {
			t.Errorf("%s = %s, want %q", c.call, got[i], c.want)
		}
	}
}

// The page's keyed lists hold one row per key whatever the view says. A
// session open in two apps once arrived as two items with the same id, and
// every update added another row. The real sync is run under node against
// a stand-in for the container, when node is there to run it.
func TestKeyedListsNeverRepeatAKey(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	fn := "function sync(container, items, keyOf, make, fill) {" +
		between(t, readUI(t, "ui/app.js"), "function sync(container, items, keyOf, make, fill) {", "\n  }") + "\n}"
	script := fn + `
function Box() { this.kids = []; }
Object.defineProperty(Box.prototype, "children", { get: function () { return this.kids.slice(); } });
Object.defineProperty(Box.prototype, "firstChild", { get: function () { return this.kids[0] || null; } });
Box.prototype.insertBefore = function (n, ref) {
  if (n.parent) { n.parent.kids.splice(n.parent.kids.indexOf(n), 1); }
  var i = ref ? this.kids.indexOf(ref) : this.kids.length;
  this.kids.splice(i, 0, n);
  n.parent = this;
};
function Item(box) { this.dataset = {}; this.parent = null; }
Object.defineProperty(Item.prototype, "nextSibling", { get: function () {
  var k = this.parent.kids; return k[k.indexOf(this) + 1] || null; } });
Item.prototype.remove = function () {
  if (this.parent) { this.parent.kids.splice(this.parent.kids.indexOf(this), 1); this.parent = null; }
};
function keys(box) { return box.kids.map(function (n) { return n.dataset.key; }).join(","); }
function make() { return new Item(); }
function fill(n, it) { n.last = it.n; }
function keyOf(it) { return it.id; }

var box = new Box();
var items = [{ id: "a", n: 1 }, { id: "b", n: 2 }, { id: "a", n: 3 }, { id: "c", n: 4 }];
var firstA = null;
for (var r = 0; r < 3; r++) {
  sync(box, items, keyOf, make, fill);
  if (!firstA) { firstA = box.kids[0]; }
  console.log(keys(box) + " " + (box.kids[0] === firstA) + " " + box.kids[0].last);
}

var held = new Box();
["a", "a", "b"].forEach(function (k) { var n = new Item(); n.dataset.key = k; held.insertBefore(n, null); });
sync(held, [{ id: "a", n: 5 }], keyOf, make, fill);
console.log(keys(held));
`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	want := "a,b,c true 1\na,b,c true 1\na,b,c true 1\na"
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// A running session open in more than one app says so on its row, as a
// babysat card does, and the row says nothing when it is open in one.
func TestARunningRowSaysItIsOpenInSeveralApps(t *testing.T) {
	index := readUI(t, "ui/index.html")
	row := between(t, index, `<template id="tpl-session">`, `</template>`)
	if !strings.Contains(row, `<span class="tag multi" data-f="multi" hidden></span>`) {
		t.Errorf("the running row has no open in several apps tag:\n%s", row)
	}
	app := readUI(t, "ui/app.js")
	body := between(t, app, "function fillSession(node, s) {", "\n  }")
	for _, want := range []string{
		`var live = s.live || [];`,
		`show(multi, live.length > 1);`,
		`setText(multi, live.length > 1 ? "open in " + live.length + " apps" : "");`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fillSession does not contain %s", want)
		}
	}
}

// A name the session also goes by is in the name's tooltip, on a card and
// on a running row, and the id is there otherwise.
func TestTheOtherNameIsInTheTooltip(t *testing.T) {
	app := readUI(t, "ui/app.js")
	for fn, want := range map[string]string{
		"function fillWatch(":   `setTitle(f(node, "name"), nameTip(w.alsoCalled, w.sessionId));`,
		"function fillSession(": `setTitle(f(node, "name"), nameTip(s.alsoCalled, s.id));`,
	} {
		if !strings.Contains(between(t, app, fn, "\n  }"), want) {
			t.Errorf("%s does not contain %s", fn, want)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := "function nameTip(alsoCalled, id) {" + between(t, app, "function nameTip(alsoCalled, id) {", "\n  }") + "\n}\n" +
		`console.log(nameTip("my-app-2-94", "id-1") + "|" + nameTip("", "id-2") + "|" + nameTip(undefined, "id-3"));`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "Also called my-app-2-94|id-2|id-3" {
		t.Fatalf("%s", got)
	}
}

// A Not running row handed back to its app says so in a small badge, and
// the section header counts them while the section itself stays closed
// until the person opens it.
func TestHandedBackRowsAreBadgedAndCounted(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<details class="sect" id="sect-past">`,
		`<summary><h2>Not running</h2><span class="n" id="past-count">0</span><span class="tag handed" id="past-handed" hidden></span></summary>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	row := between(t, index, `<template id="tpl-past">`, `</template>`)
	if !strings.Contains(row, `<span class="nmcell"><span class="nm" data-f="name"></span><span class="tag handed" data-f="handed" hidden></span></span>`) {
		t.Errorf("the Not running row has no hand-back badge:\n%s", row)
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`desktop: "Handed back to Desktop", vscode: "Handed back to VS Code", terminal: "Handed back to a terminal"`,
		`" handed back"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
	if strings.Contains(app, "sect-past") {
		t.Error("the page must never open or close the Not running section itself")
	}
	css := readUI(t, "ui/app.css")
	if !strings.Contains(css, ".tag.handed {") {
		t.Error("app.css does not style .tag.handed")
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := "var HANDED_BACK = " + between(t, app, "var HANDED_BACK = ", ";") + ";\n" +
		"function handedBackCount(list) {" + between(t, app, "function handedBackCount(list) {", "\n  }") + "\n}\n" +
		`console.log(handedBackCount([{handedBackTo: "vscode"}, {handedBackTo: ""}, {}, {handedBackTo: "terminal"}, {handedBackTo: "elsewhere"}]) + "|" + handedBackCount([]));`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "2|0" {
		t.Fatalf("%s", got)
	}
}

// The empty Babysat section has no plain sentence any more: with nothing
// babysat at all it shows a centered hero with no frame and no card
// background, so it never looks like a session item. It holds the sleeping
// mascot, the headline, one promise and what to do next, which follows
// what is running. A search that matches nothing out of an otherwise
// non-empty list still just says so.
func TestTheEmptyBabysatHero(t *testing.T) {
	index := readUI(t, "ui/index.html")
	if strings.Contains(index, "Nothing babysat. Babysit a session to keep it awake and reachable.") {
		t.Error("index.html still carries the old empty Babysat sentence")
	}
	hero := between(t, index, `<div class="empty-hero" id="watches-hero" hidden>`, "\n      </div>\n")
	for _, want := range []string{
		`<span data-el="mascot"></span>`,
		`<h3>Nothing babysat yet</h3>`,
		`<p class="promise" data-f="promise"></p>`,
		`<p class="next" data-f="next"></p>`,
		`<div class="attach start" data-el="startbox" hidden>`,
		`<code class="mono" data-f="startcmd"></code>`,
		`<button class="b small" data-act="copy-start">Copy</button>`,
		`<p class="next" data-f="after" hidden></p>`,
	} {
		if !strings.Contains(hero, want) {
			t.Errorf("the hero does not contain %s", want)
		}
	}
	if !strings.Contains(index, `<p class="empty" id="watches-empty"></p>`) {
		t.Error("index.html has no plain line for a search that matches nothing")
	}
	css := readUI(t, "ui/app.css")
	for _, want := range []string{".empty-hero {", ".empty-hero .mascot {", ".empty-hero .next b {", ".empty-hero .start {"} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css does not style %s", want)
		}
	}

	app := readUI(t, "ui/app.js")
	for name, body := range map[string]string{"index.html": index, "app.js": app, "app.css": css} {
		for _, gone := range []string{`"survives"`, ".survives", "survivalState", "App crashes", "App closes", "Session stops", "SSH drops",
			"Server reboots", "Computer restarts", "Turn on Start at login", "ccbabysitter install", "serviceInstalled",
			"Nothing babysat. Babysit a session to keep it awake and reachable."} {
			if strings.Contains(body, gone) {
				t.Errorf("%s still carries %q", name, gone)
			}
		}
	}
	for _, want := range []string{
		`"A babysat session comes back on its own. If its app crashes or closes, it carries on in the background, reachable from your other devices."`,
		`"A babysat session stays on your remote list. If it stops or the server reboots, it comes back in the background with Remote Control on."`,
		`var START_CMD = "claude --bg --remote-control";`,
		`show(node.querySelector('[data-act="babysit"]'), canBabysit(s));`,
		`copyText(START_CMD);`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}

	// The next step follows what is running and, on a server, whether new
	// background sessions are babysat on their own. A session that belongs
	// to another program has no Babysit button, so it does not count.
	script := "(function () {\n" + jsFunction(t, app, "canBabysit") + jsFunction(t, app, "heroSteps") +
		`function say(v) { var s = heroSteps(v); console.log(JSON.stringify([s.next, s.command, s.after])); }` + "\n" +
		`say({env: {}, sessions: [{actionable: true}]});` + "\n" +
		`say({env: {headless: true}, sessions: [{actionable: false}, {actionable: true}], settings: {autoBabysit: true}});` + "\n" +
		`say({env: {}, sessions: [{actionable: false}]});` + "\n" +
		`say({env: {}});` + "\n" +
		`say({env: {headless: true}, sessions: [], settings: {autoBabysit: true}});` + "\n" +
		`say({env: {headless: true}, sessions: [{actionable: false}], settings: {autoBabysit: false}});` + "\n" +
		"})();\n"
	want := []string{
		`[["Press ",{"b":"Babysit"}," on a session below to start."],false,[]]`,
		`[["Press ",{"b":"Babysit"}," on a session below to start."],false,[]]`,
		`[["Start a Claude Code session, then press ",{"b":"Babysit"}," on it here."],false,[]]`,
		`[["Start a Claude Code session, then press ",{"b":"Babysit"}," on it here."],false,[]]`,
		`[["Start a background session in a project folder:"],true,["It is babysat as soon as it starts."]]`,
		`[["Start a background session in a project folder:"],true,["Then press ",{"b":"Babysit"}," on it here."]]`,
	}
	got := runNode(t, script)
	if len(got) != len(want) {
		t.Fatalf("want %d lines from node, got %q", len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("case %d: got %s, want %s", i, got[i], w)
		}
	}
}

// The babysit dialog promises what babysitting does where it runs: a
// server is not kept awake, and there what closes is the terminal or the
// SSH connection. With Remote Control off it says what that means now,
// then what to do, in the app's own words. Nothing asks for an install.
func TestTheBabysitDialogLines(t *testing.T) {
	index := readUI(t, "ui/index.html")
	dialog := between(t, index, `<dialog class="dlg" id="dlg-babysit"`, "</dialog>")
	if strings.Contains(dialog, `data-el="install"`) || strings.Contains(dialog, "ccbabysitter install") {
		t.Errorf("the babysit dialog still asks for an install:\n%s", dialog)
	}
	if !strings.Contains(dialog, `<label class="check" data-el="login" hidden><input type="checkbox" id="babysit-login">`) {
		t.Error("the start at login checkbox stays on a machine with a display")
	}
	app := readUI(t, "ui/app.js")
	for _, gone := range []string{"Turn Remote Control on first", `el(dialog, "install")`} {
		if strings.Contains(app, gone) {
			t.Errorf("app.js still carries %q", gone)
		}
	}
	script := "(function () {\n" +
		"var HOST_APP = " + between(t, app, "var HOST_APP = ", ";") + ";\n" +
		`function attachCommand(short) { return "claude attach " + short; }` + "\n" +
		jsFunction(t, app, "rcHintFor") + jsFunction(t, app, "asClause") +
		jsFunction(t, app, "babysitPromise") + jsFunction(t, app, "rcMissing") +
		`console.log(babysitPromise({host: "terminal"}, true));` + "\n" +
		`console.log(babysitPromise({host: "background"}, true));` + "\n" +
		`console.log(babysitPromise({host: "desktop"}, false));` + "\n" +
		`console.log(babysitPromise({host: "background"}, false));` + "\n" +
		`console.log(rcMissing({host: "terminal", remoteControl: false}));` + "\n" +
		`console.log(rcMissing({host: "vscode", remoteControl: false}));` + "\n" +
		`console.log(rcMissing({host: "background", shortId: "a1b2c3d4", remoteControl: false}));` + "\n" +
		`console.log("[" + rcMissing({host: "desktop", remoteControl: true}) + "]");` + "\n" +
		"})();\n"
	want := []string{
		"If the terminal or your SSH connection closes, this session continues in the background with Remote Control.",
		"If this session stops, it is started again in the background with Remote Control.",
		"Keeps the computer awake. If the desktop app closes, this session continues in the background with Remote Control.",
		"Keeps the computer awake. If this session stops, it is started again in the background with Remote Control.",
		"Remote Control is off, so your other devices can't reach it yet: type /rc in that terminal session.",
		"Remote Control is off, so your other devices can't reach it yet: switch Remote Control on in the VS Code panel for this session.",
		"Remote Control is off, so your other devices can't reach it yet: attach with `claude attach a1b2c3d4` and type /rc.",
		"[]",
	}
	got := runNode(t, script)
	if len(got) != len(want) {
		t.Fatalf("want %d lines from node, got %q", len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("case %d: got %q, want %q", i, got[i], w)
		}
	}
}

// A Not running row offers one command with a Copy button: the attach
// command of a background session CC Babysitter stopped, and the resume
// command for every other. On a server the stopped one also has its
// from-elsewhere line, with a Copy button of its own.
func TestNotRunningRowsCarryTheirCommand(t *testing.T) {
	index := readUI(t, "ui/index.html")
	row := between(t, index, `<template id="tpl-past">`, `</template>`)
	for _, want := range []string{
		`<code class="mono cmd" data-f="cmd"></code>`,
		`<button class="b small" data-act="copy-cmd">Copy</button>`,
		`<div class="attach elsewhere" data-el="sshbox" hidden>`,
		`<span class="k">From elsewhere</span>`,
		`<code class="mono" data-f="sshattach"></code>`,
		`<button class="b small" data-act="copy-ssh">Copy</button>`,
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the Not running row does not contain %s", want)
		}
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`if (act === "copy-cmd") { copyText(pastCommand(p)); }`,
		`if (act === "copy-ssh") { copyText(p.sshAttachCmd || ""); }`,
		`show(el(node, "sshbox"), !!p.sshAttachCmd);`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
	script := "(function () {\n" + jsFunction(t, app, "pastCommand") +
		`console.log(pastCommand({resumeCmd: "cd ~/ws && claude --resume x", attachCmd: "claude attach a1b2c3d4"}));` + "\n" +
		`console.log(pastCommand({resumeCmd: "cd ~/ws && claude --resume x"}));` + "\n" +
		"})();\n"
	want := []string{"claude attach a1b2c3d4", "cd ~/ws && claude --resume x"}
	got := runNode(t, script)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A refusal that names a command to run by hand is the only place that
// command is written, so its toast stays until it is dismissed.
func TestARefusalWithACommandStays(t *testing.T) {
	app := readUI(t, "ui/app.js")
	if !strings.Contains(app, "toast(message, true, hasCommand(message));") {
		t.Error("a refusal that names a command must stay")
	}
	script := "(function () {\n" + jsFunction(t, app, "hasCommand") +
		"console.log(hasCommand(\"Stop it by hand with `claude stop a1b2c3d4`.\") + \" \" + hasCommand(\"That session is not there any more.\"));\n" +
		"})();\n"
	if got := runNode(t, script); len(got) != 1 || got[0] != "true false" {
		t.Fatalf("%q", got)
	}
}

// runNode runs a script with node and returns its output lines, skipping
// the test where node is not installed.
func runNode(t *testing.T, script string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// jsFunction cuts one top-level function of app.js out of it, whole.
func jsFunction(t *testing.T, app, name string) string {
	t.Helper()
	head := "  function " + name + "("
	return head + between(t, app, head, "\n  }\n") + "\n  }\n"
}

// When the CLI says it is not logged in, the page says so in one line under
// the header, on a desktop and on a server alike; an unknown answer says
// nothing.
func TestTheNotLoggedInLine(t *testing.T) {
	index := readUI(t, "ui/index.html")
	want := `<p class="info warn" id="login-note" hidden>Claude Code is not logged in on this machine. Run <code class="mono">claude</code> once and log in.</p>`
	if !strings.Contains(index, want) {
		t.Errorf("index.html does not contain, word for word:\n%s", want)
	}
	if strings.Index(index, `id="login-note"`) < strings.Index(index, `id="info"`) {
		t.Error("the versions line stays the top line, with the login line under it")
	}
	app := readUI(t, "ui/app.js")
	if !strings.Contains(app, `show($("#login-note"), env.cliLoggedIn === "no");`) {
		t.Error("app.js must show the login line only when the CLI said it is not logged in")
	}
	if !strings.Contains(readUI(t, "ui/app.css"), ".info.warn { font-family: var(--sans); color: var(--warn); }") {
		t.Error("the login line has the warning colour")
	}
}

// The versions line names the CLI always, and Desktop and the VS Code
// extension only when they are there. Nothing is ever "not installed".
func TestTheVersionsLine(t *testing.T) {
	app := readUI(t, "ui/app.js")
	if strings.Contains(app, "not installed") {
		t.Error("app.js still says something is not installed")
	}
	script := "(function () {\n" + jsFunction(t, app, "versionsLine") +
		`console.log(versionsLine({cliFound: true, cliVersion: "2.1.283", desktopInstalled: true, desktopVersion: "1.2.3", desktopRunning: true, vscodeInstalled: true, vscodeExtVersion: "2.1.283"}).join(" | "));` + "\n" +
		`console.log(versionsLine({cliFound: true, cliVersion: "2.1.283", vscodeInstalled: true}).join(" | "));` + "\n" +
		`console.log(versionsLine({cliFound: true, desktopInstalled: true, desktopVersion: "installed"}).join(" | "));` + "\n" +
		`console.log(versionsLine({}).join(" | "));` + "\n" +
		"})();\n"
	want := []string{
		"Claude Code 2.1.283 | Desktop 1.2.3 running | VS Code extension 2.1.283",
		"Claude Code 2.1.283",
		"Claude Code found | Desktop installed",
		"Claude Code not found",
	}
	got := runNode(t, script)
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %q", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("case %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// Settings has no Save button: every setting is saved the moment it is
// changed, one at a time. A server is not offered starting at login or
// opening a browser, and only a server is offered babysitting every new
// background session.
func TestSettingsSaveOnChange(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, gone := range []string{"save-settings", "Save settings", "dr-foot", "Follow system", `data-theme-mode="system"`,
		"On a machine with no display, each background session is babysat as it starts."} {
		if strings.Contains(index, gone) {
			t.Errorf("index.html still carries %q", gone)
		}
	}
	desktop := between(t, index, `<div class="fieldset" id="set-desktop">`, "</div>")
	for _, want := range []string{`id="set-autostart"`, `id="set-autostart-note"`, `id="set-open-browser"`,
		"Start CC Babysitter when I log in", "Open this page in a browser when you run ccbabysitter"} {
		if !strings.Contains(desktop, want) {
			t.Errorf("the desktop settings do not hold %s", want)
		}
	}
	server := between(t, index, `<div class="fieldset" id="set-server" hidden>`, "</div>")
	for _, want := range []string{`id="set-auto-babysit"`, "Babysit every new background session",
		`<p class="quiet">Each background session started on this server is babysat as soon as it starts.</p>`} {
		if !strings.Contains(server, want) {
			t.Errorf("the server settings do not hold %s", want)
		}
	}
	for _, want := range []string{
		`<p class="note err" id="set-error" role="alert" hidden></p>`,
		`<button class="seg-b" data-theme-mode="dark">Dark</button>`,
		`<button class="seg-b" data-theme-mode="light">Light</button>`,
		`<button class="seg-b" data-theme-mode="auto">Auto</button>`,
		`<button class="b" data-close="drawer">Close</button>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}

	app := readUI(t, "ui/app.js")
	for _, gone := range []string{"saveSettings", "state.form", "serverAutoBabysit", `"system"`} {
		if strings.Contains(app, gone) {
			t.Errorf("app.js still uses %q", gone)
		}
	}
	for _, want := range []string{
		`show($("#set-desktop"), !headless);`,
		`show($("#set-server"), headless);`,
		`onSettingBox("#set-autostart", "autostart");`,
		`onSettingBox("#set-open-browser", "autoOpenBrowser");`,
		`onSettingBox("#set-auto-babysit", "autoBabysit");`,
		`$("#set-theme-seg").addEventListener("click", onThemeButton);`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}

	// A refused change puts its control back and says why inside
	// Settings; a saved one clears that and is kept in the view.
	script := "(function () {\n" +
		`var nodes = {"#set-error": {textContent: "", hidden: true}};` + "\n" +
		`function $(sel) { return nodes[sel]; }` + "\n" +
		`function setText(node, value) { node.textContent = value; }` + "\n" +
		`function show(node, on) { node.hidden = !on; }` + "\n" +
		`function statusMessage(status) { return "status " + status; }` + "\n" +
		`function render() {}` + "\n" +
		`var answer = null;` + "\n" +
		`function fetch(url, opts) { var a = answer; a.sent = opts.method + " " + url + " " + opts.body; return Promise.resolve({ok: a.ok, status: a.status, json: function () { return Promise.resolve(a.body); }}); }` + "\n" +
		`var state = {saving: 0, saveChain: Promise.resolve(), view: {settings: {autostart: false, theme: "dark"}}};` + "\n" +
		jsFunction(t, app, "settingsError") + jsFunction(t, app, "putSetting") + jsFunction(t, app, "saveSetting") +
		`var box = {checked: true};` + "\n" +
		`answer = {ok: false, status: 409, body: {ok: false, message: "Could not change starting at login: denied"}};` + "\n" +
		`var refused = answer;` + "\n" +
		`saveSetting("autostart", true, function () { box.checked = false; });` + "\n" +
		`state.saveChain.then(function () {` + "\n" +
		`  console.log(JSON.stringify([refused.sent, box.checked, nodes["#set-error"].textContent, nodes["#set-error"].hidden, state.saving, state.view.settings.autostart]));` + "\n" +
		`  answer = {ok: true, status: 200, body: {ok: true, message: "Settings saved."}};` + "\n" +
		`  saveSetting("theme", "light", function () { throw new Error("not undone"); });` + "\n" +
		`  return state.saveChain;` + "\n" +
		`}).then(function () {` + "\n" +
		`  console.log(JSON.stringify([answer.sent, nodes["#set-error"].hidden, state.saving, state.view.settings.theme]));` + "\n" +
		`});` + "\n" +
		"})();\n"
	got := runNode(t, script)
	want := []string{
		`["PUT /api/settings {\"autostart\":true}",false,"Could not change starting at login: denied",false,0,false]`,
		`["PUT /api/settings {\"theme\":\"light\"}",true,0,"light"]`,
	}
	if len(got) != len(want) {
		t.Fatalf("want %d lines, got %q", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("case %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

// The light palette is one block that data-theme="light" reaches, for the
// page and for the art. theme.js alone reads the computer's light or dark
// mode, turning Auto into light or dark, and a saved Follow system is Auto.
func TestThemesDarkLightAuto(t *testing.T) {
	for _, name := range []string{"ui/app.css", "ui/art.css"} {
		css := readUI(t, name)
		if strings.Count(css, ":root[data-theme=\"light\"] {\n") != 1 {
			t.Errorf("%s must hold its light palette in one block reached by data-theme=\"light\"", name)
		}
		for _, gone := range []string{`data-theme="system"`, "prefers-color-scheme"} {
			if strings.Contains(css, gone) {
				t.Errorf("%s still carries %q", name, gone)
			}
		}
	}
	if !strings.Contains(readUI(t, "ui/app.css"), `:root[data-theme="light"] { color-scheme: light; }`) {
		t.Error("the light page tells the browser it is light")
	}
	theme := readUI(t, "ui/theme.js")
	for _, want := range []string{`"(prefers-color-scheme: light)"`, `window.ccbTheme = { apply: apply };`} {
		if !strings.Contains(theme, want) {
			t.Errorf("theme.js does not contain %s", want)
		}
	}
	if !strings.Contains(readUI(t, "ui/app.js"), "window.ccbTheme.apply(theme);") {
		t.Error("app.js must leave the theme to theme.js")
	}

	// Each case loads theme.js into a stub page with a saved setting and a
	// computer in light or dark mode, runs a step, then prints what was
	// painted first, what is painted now and what is remembered. theme.js
	// reads window and document as free names, so it is wrapped where those
	// names are the stubs.
	run := func(saved, computer, then string) string {
		return "(function () {\n" +
			`var attr = null, stored = ` + saved + `, listener = null;` + "\n" +
			`var mq = {matches: ` + computer + `, addEventListener: function (e, fn) { listener = fn; }};` + "\n" +
			`var window = {matchMedia: function () { return mq; }, localStorage: {getItem: function () { return stored; }, setItem: function (k, v) { stored = v; }}};` + "\n" +
			`var document = {documentElement: {getAttribute: function () { return attr; }, setAttribute: function (k, v) { attr = v; }}};` + "\n" +
			"(function () {\n" + theme + "\n}).call(null);\n" +
			`var first = attr;` + "\n" + then + "\n" +
			`console.log(first + " " + attr + " " + stored);` + "\n" +
			"})();\n"
	}
	cases := []struct{ script, want string }{
		{run(`"dark"`, "true", ""), "dark dark dark"},
		{run(`"light"`, "false", ""), "light light light"},
		{run(`"system"`, "true", ""), "light light system"},
		{run(`null`, "true", ""), "dark dark null"},
		{run(`"auto"`, "false", "mq.matches = true; listener();"), "dark light auto"},
		{run(`"dark"`, "false", `window.ccbTheme.apply("light");`), "dark light light"},
		{run(`"dark"`, "true", `window.ccbTheme.apply("system");`), "dark light auto"},
		{run(`"light"`, "true", `window.ccbTheme.apply("sepia");`), "light dark dark"},
	}
	for i, c := range cases {
		got := runNode(t, c.script)
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("case %d: got %q, want %q", i, got, c.want)
		}
	}
}

// The page offers Quit in its settings, asks first, and says how to start
// CC Babysitter again once it has quit.
func TestThePageCanQuit(t *testing.T) {
	html, js := readUI(t, "ui/index.html"), readUI(t, "ui/app.js")
	for _, want := range []string{
		`id="open-quit"`,
		`id="dlg-quit"`,
		"Quit CC Babysitter?",
		"Babysat sessions keep running, but nothing brings them back until you run ccbabysitter again.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html has no %q", want)
		}
	}
	for _, want := range []string{
		`"/api/quit"`,
		"CC Babysitter has quit. Run ccbabysitter to start it again.",
		// The last view a quitting copy sends must not hide the sentence.
		`if (!quitHere) { show($("#reconnect"), false); }`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js has no %q", want)
		}
	}
}

// Claude Desktop scheduled task runs are listed apart from the running
// sessions, in a section of their own that starts folded and only shows
// when there is a run, and a run never gets a Babysit button nor counts
// as something to press it on.
func TestScheduledTaskRunsAreListedApart(t *testing.T) {
	index := readUI(t, "ui/index.html")
	for _, want := range []string{
		`<details class="sect" id="sect-scheduled" hidden>`,
		`<summary><h2>Scheduled task runs</h2><span class="n" id="scheduled-count">0</span></summary>`,
		`<div class="rows" id="scheduled"></div>`,
		`<span class="tag sched" data-f="sched" hidden>Scheduled task</span>`,
	} {
		if !strings.Contains(index, want) {
			t.Errorf("index.html does not contain %s", want)
		}
	}
	if strings.Index(index, `id="sect-scheduled"`) < strings.Index(index, `id="sect-running"`) {
		t.Error("the scheduled task runs come after the running sessions")
	}
	app := readUI(t, "ui/app.js")
	for _, want := range []string{
		`sync($("#scheduled"), scheduled, function (s) { return s.id; },`,
		`show(f(node, "sched"), !!s.scheduledTask);`,
		// Only the Babysit button goes: a run living as a background copy
		// keeps Stop and Copy attach.
		`show(el(node, "acts"), !!s.actionable && (canBabysit(s) || !!s.canStop || !!s.attachCmd || !!s.sshAttachCmd));`,
		`show(node.querySelector('[data-act="babysit"]'), canBabysit(s));`,
		// With only runs running, the Running section does not claim
		// nothing is running.
		`"Only scheduled task runs are running."`,
		// A run's row answers its buttons like a Running row does.
		`onAction($("#sessions"), onSessionAction);`,
		`onAction($("#scheduled"), onSessionAction);`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
	script := "(function () {\n" + jsFunction(t, app, "canBabysit") + jsFunction(t, app, "heroSteps") +
		`console.log(JSON.stringify([canBabysit({actionable: true}), canBabysit({actionable: true, scheduledTask: true})]));` + "\n" +
		`console.log(JSON.stringify(heroSteps({env: {}, sessions: [{actionable: true, scheduledTask: true}]}).next));` + "\n" +
		"})();\n"
	want := []string{
		`[true,false]`,
		`["Start a Claude Code session, then press ",{"b":"Babysit"}," on it here."]`,
	}
	got := runNode(t, script)
	if len(got) != len(want) {
		t.Fatalf("want %d lines from node, got %q", len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d: got %s, want %s", i, got[i], w)
		}
	}
}
