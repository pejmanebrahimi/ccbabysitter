package site

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runNode runs a script under node with site.js loaded into a bare global
// object, so the page's helpers are tested without a browser. CI requires
// node; on a machine without it these tests are skipped.
func runNode(t *testing.T, body string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "site.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := "var window = {}; var navigator = {platform: 'MacIntel'};\n" + string(src) + "\nvar s = window.ccbSite;\n" + body
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestThemeWithoutStorage(t *testing.T) {
	out := runNode(t, `
var broken = { getItem: function () { throw new Error("blocked"); }, setItem: function () { throw new Error("blocked"); } };
console.log(JSON.stringify([s.readTheme(broken), s.nextTheme(s.readTheme(broken), false), s.nextTheme("", true), s.nextTheme("dark", false)]));
s.writeTheme(broken, "dark");
console.log("ok");`)
	if out != "[\"\",\"dark\",\"light\",\"light\"]\nok" {
		t.Fatalf("got %q", out)
	}
}

// TestThemeBackToSystemForgetsTheChoice checks that a click that lands on
// the system's own theme removes the attribute and the stored choice, so the
// page follows the system again.
func TestThemeBackToSystemForgetsTheChoice(t *testing.T) {
	got := runNode(t, `
function show(r) { return r.theme + "/" + (r.attr || "-") + "/" + (r.store || "-"); }
console.log(show(s.chooseTheme("", false)));
console.log(show(s.chooseTheme("dark", false)));
console.log(show(s.chooseTheme("", true)));
console.log(show(s.chooseTheme("light", true)));`)
	want := "dark/dark/dark\nlight/-/-\nlight/light/light\ndark/-/-"
	if got != want {
		t.Fatalf("chooseTheme:\n%s\nwant:\n%s", got, want)
	}
}

// TestCommandForEachTab checks that the i-th tab's command is the i-th
// entry of ENVS, in tab order.
func TestCommandForEachTab(t *testing.T) {
	out := runNode(t, `console.log([0,1,2,3].map(function (i) { return s.commandFor(i).cmd; }).join("|"));`)
	want := "curl -fsSL https://ccbabysitter.dev/install.sh | sh|irm https://ccbabysitter.dev/install.ps1 | iex|/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter|go install ccbabysitter.dev/ccbabysitter/cmd/ccbabysitter@latest"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

// TestTabsSelect runs the tab script on a small fake page: macOS & Linux
// stays selected at start even on Windows, a click selects a tab and sets
// its command and prompt, and the arrow keys move the selection round.
func TestTabsSelect(t *testing.T) {
	out := runNode(t, `
function el(attrs) {
  var e = { attrs: attrs || {}, on: {}, innerHTML: "", textContent: "", focused: false,
    getAttribute: function (n) { return n in this.attrs ? this.attrs[n] : null; },
    setAttribute: function (n, v) { this.attrs[n] = String(v); },
    addEventListener: function (type, f) { this.on[type] = f; },
    focus: function () { this.focused = true; } };
  return e;
}
var tabs = [0, 1, 2, 3].map(function (i) { return el({ role: "tab", "aria-selected": i === 0 ? "true" : "false" }); });
var list = el({ "data-tabs": "install" });
list.querySelectorAll = function () { return tabs; };
var prompt = el(), cmd = el();
prompt.innerHTML = "$"; cmd.textContent = "curl -fsSL https://ccbabysitter.dev/install.sh | sh";
var line = el({ id: "install" });
line.querySelector = function (q) { return q === "[data-prompt]" ? prompt : q === "[data-cmd]" ? cmd : null; };
navigator = { platform: "Win32" };
var document = { documentElement: el(), querySelector: function () { return null; },
  querySelectorAll: function (q) { return q === "[data-tabs]" ? [list] : []; },
  getElementById: function (id) { return id === "install" ? line : null; } };
`+"eval(require('fs').readFileSync('assets/site.js', 'utf8'));"+`
function state() { return tabs.map(function (t) { return t.attrs["aria-selected"] === "true" ? "1" : "0"; }).join("") + " " + prompt.innerHTML + " " + cmd.textContent; }
console.log(state());
tabs[2].on.click();
console.log(state());
tabs[2].on.keydown({ key: "ArrowRight", preventDefault: function () {} });
console.log(state() + " " + tabs[3].focused + " " + tabs[3].attrs.tabindex + tabs[2].attrs.tabindex);
tabs[3].on.keydown({ key: "ArrowRight", preventDefault: function () {} });
console.log(state());
tabs[0].on.keydown({ key: "ArrowLeft", preventDefault: function () {} });
console.log(state());`)
	want := strings.Join([]string{
		"1000 $ curl -fsSL https://ccbabysitter.dev/install.sh | sh",
		"0010 &gt; /plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter",
		"0001 $ go install ccbabysitter.dev/ccbabysitter/cmd/ccbabysitter@latest true 0-1",
		"1000 $ curl -fsSL https://ccbabysitter.dev/install.sh | sh",
		"0001 $ go install ccbabysitter.dev/ccbabysitter/cmd/ccbabysitter@latest",
	}, "\n")
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestCopyFallsBack(t *testing.T) {
	out := runNode(t, `
var selected = false;
var doc = { createRange: function () { return { selectNodeContents: function () {} }; },
            getSelection: function () { return { removeAllRanges: function () {}, addRange: function () { selected = true; } }; } };
s.copyText("x", doc, {}, {}).then(function (ok) { console.log("none:" + ok + ":" + selected); });
selected = false;
s.copyText("x", doc, { clipboard: { writeText: function () { return Promise.reject(new Error("denied")); } } }, {})
 .then(function (ok) { console.log("denied:" + ok + ":" + selected); });
s.copyText("x", doc, { clipboard: { writeText: function () { return Promise.resolve(); } } }, {})
 .then(function (ok) { console.log("works:" + ok); });`)
	for _, want := range []string{"none:false:true", "denied:false:true", "works:true"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

// runThemeScript runs theme.js against a fake page whose storage holds
// stored, or throws when stored is "blocked", and returns the data-theme
// it set, or "none".
func runThemeScript(t *testing.T, stored string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "theme.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
var theme = "none";
var document = { documentElement: { setAttribute: function (k, v) { if (k === "data-theme") theme = v; } } };
var window = {};
var stored = ` + strconv.Quote(stored) + `;
Object.defineProperty(window, "localStorage", { get: function () {
  if (stored === "blocked") throw new Error("blocked");
  return { getItem: function () { return stored; } };
} });
` + string(src) + `
console.log(theme);`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestThemeScriptAppliesTheStoredChoice checks that theme.js applies a
// stored light or dark, ignores anything else, and leaves the page to the
// system setting when storage throws.
func TestThemeScriptAppliesTheStoredChoice(t *testing.T) {
	for stored, want := range map[string]string{"dark": "dark", "light": "light", "blue": "none", "": "none", "blocked": "none"} {
		if got := runThemeScript(t, stored); got != want {
			t.Errorf("stored %q: data-theme is %q, want %q", stored, got, want)
		}
	}
}

// runArt loads art.js into a minimal fake page, mounts the animated
// kangaroo with prefers-reduced-motion set to reduced and the page shown or
// in a background tab, and returns how many timers were set.
func runArt(t *testing.T, reduced, hidden bool) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "art.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
var timers = 0; setTimeout = function () { timers++; };
function el() { return { style: {}, setAttribute: function () {}, appendChild: function () {}, getBoundingClientRect: function () {} }; }
var document = { hidden: ` + strconv.FormatBool(hidden) + `, createElementNS: el, querySelectorAll: function () { return []; } };
var window = { matchMedia: function () { return { matches: ` + strconv.FormatBool(reduced) + ` }; } };
` + string(src) + `
var host = { appendChild: function () {} };
window.ccbArt.mount(host, true);
console.log(timers);`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestReducedMotionKeepsHerStill(t *testing.T) {
	if got := runArt(t, true, false); got != "0" {
		t.Fatalf("with reduced motion no timers may be set, got %s", got)
	}
}

// TestMotionIsOtherwiseOn is the positive control for the test above: the
// same harness without reduced motion must set timers, so a harness that
// never reaches the animation cannot make the stillness test pass by itself.
func TestMotionIsOtherwiseOn(t *testing.T) {
	got := runArt(t, false, false)
	n, err := strconv.Atoi(got)
	if err != nil || n <= 0 {
		t.Fatalf("without reduced motion the loop must set timers, got %q", got)
	}
}

// TestBackgroundTabOnlyWaits checks that a cycle that starts while the page
// is hidden sets no pose changes, only the timer for the next cycle.
func TestBackgroundTabOnlyWaits(t *testing.T) {
	if got := runArt(t, false, true); got != "1" {
		t.Fatalf("a hidden page must only reschedule the loop, got %s timers", got)
	}
}

// babyArt is what mounting the 404 baby made: the fills of the rects drawn
// straight into the picture and into the head group, the viewBox, and how
// many timers were set.
type babyArt struct {
	Body    []string `json:"body"`
	Head    []string `json:"head"`
	ViewBox string   `json:"viewBox"`
	Timers  int      `json:"timers"`
}

// runBaby loads art.js into a minimal fake page holding one data-roo="baby"
// host, so the page's own start-up code mounts it, and reports what it drew.
func runBaby(t *testing.T, reduced bool) babyArt {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "art.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
var timers = 0; setTimeout = function () { timers++; };
var svg = null;
function el(ns, tag) {
  var e = { tag: tag, attrs: {}, style: {}, children: [], getBoundingClientRect: function () {} };
  e.setAttribute = function (k, v) { e.attrs[k] = String(v); };
  e.appendChild = function (c) { e.children.push(c); };
  if (tag === "svg") svg = e;
  return e;
}
var host = { getAttribute: function (k) { return k === "data-roo" ? "baby" : null; }, appendChild: function () {} };
var document = { hidden: false, createElementNS: el, querySelectorAll: function (q) { return q === "[data-roo]" ? [host] : []; } };
var window = { matchMedia: function () { return { matches: ` + strconv.FormatBool(reduced) + ` }; } };
` + string(src) + `
var out = { body: [], head: [], viewBox: svg.attrs.viewBox, timers: timers };
svg.children.forEach(function (c) {
  if (c.tag === "rect") out.body.push(c.style.fill);
  else if (c.tag === "g" && c.attrs["class"] === "head") c.children.forEach(function (r) { out.head.push(r.tag === "rect" ? r.style.fill : r.tag); });
  else out.body.push(c.tag);
});
console.log(JSON.stringify(out));`
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	var b babyArt
	if err := json.Unmarshal(out, &b); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return b
}

// TestBabyIsDrawnAlone checks that the baby mode draws the baby of the calm
// pose and nothing else: one rect in her colours for each h, s and c cell,
// framed by a viewBox of just her, with her head (the h and s cells of rows
// 9 to 11) in a group of its own.
func TestBabyIsDrawnAlone(t *testing.T) {
	grid, cols := calmArt(t)
	baby := map[string]bool{}
	for _, c := range "hsc" {
		v := cols[byte(c)]
		baby[fmt.Sprintf("#%02x%02x%02x", v.R, v.G, v.B)] = true
	}
	cells, headCells := 0, 0
	for y, row := range grid {
		for x := 0; x < len(row); x++ {
			if strings.IndexByte("hsc", row[x]) >= 0 {
				cells++
				if y <= 11 {
					headCells++
				}
			}
		}
	}
	b := runBaby(t, true)
	if b.ViewBox != "12 9 4 6" {
		t.Errorf("viewBox is %q, want %q", b.ViewBox, "12 9 4 6")
	}
	if n := len(b.Body) + len(b.Head); n != cells {
		t.Errorf("drew %d rects, want the %d baby cells of calm", n, cells)
	}
	if len(b.Head) != headCells {
		t.Errorf("the head group has %d rects, want %d", len(b.Head), headCells)
	}
	for _, f := range append(b.Body, b.Head...) {
		if !baby[f] {
			t.Errorf("drew %q, which is not one of the baby's colours", f)
		}
	}
}

func TestBabyKeepsStillWithReducedMotion(t *testing.T) {
	if got := runBaby(t, true).Timers; got != 0 {
		t.Fatalf("with reduced motion the baby may set no timers, got %d", got)
	}
}

// TestBabyLooksAroundOtherwise is the positive control for the test above.
func TestBabyLooksAroundOtherwise(t *testing.T) {
	if got := runBaby(t, false).Timers; got < 1 {
		t.Fatalf("without reduced motion the baby must schedule a look, got %d timers", got)
	}
}

// runIntro loads art.js into a fake page with a clock of its own, mounts
// the home page's animated kangaroo and runs body after it. storage is how
// the tab's sessionStorage behaves: "empty", "set" (the intro key is there),
// "blocked" (reading sessionStorage throws) or "broken" (its methods throw).
// body can call visible(), the 16 rows of the picture with # for a shown
// pixel and . for an empty one, runUntil(ms), which runs the timers due up
// to that time, and read timers, how many timers were set so far, and
// stored, the intro key in storage. It returns what body prints.
func runIntro(t *testing.T, storage string, reduced bool, body string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src, err := os.ReadFile(filepath.Join("assets", "art.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := `
var now = 0, seq = 0, queue = [], timers = 0;
setTimeout = function (fn, ms) { timers++; queue.push({ at: now + (ms || 0), seq: seq++, fn: fn }); };
function runUntil(t) {
  for (;;) {
    queue.sort(function (a, b) { return a.at - b.at || a.seq - b.seq; });
    if (!queue.length || queue[0].at > t) break;
    var e = queue.shift(); now = e.at; e.fn();
  }
  now = t;
}
var rects = [];
function el(ns, tag) {
  var e = { tag: tag, attrs: {}, style: {}, children: [], getBoundingClientRect: function () {} };
  e.setAttribute = function (k, v) { e.attrs[k] = String(v); };
  e.appendChild = function (c) { e.children.push(c); };
  if (tag === "rect") rects.push(e);
  return e;
}
function visible() {
  var rows = [];
  for (var y = 0; y < 16; y++) rows.push("................".split(""));
  rects.forEach(function (r) { if (Number(r.style.opacity) === 1) rows[+r.attrs.y][+r.attrs.x] = "#"; });
  return rows.map(function (r) { return r.join(""); });
}
var document = { hidden: false, createElementNS: el, querySelectorAll: function () { return []; } };
var window = { matchMedia: function () { return { matches: ` + strconv.FormatBool(reduced) + ` }; } };
var mode = ` + strconv.Quote(storage) + `, store = {};
if (mode === "set") store["ccb.site.intro"] = "1";
Object.defineProperty(window, "sessionStorage", { get: function () {
  if (mode === "blocked") throw new Error("blocked");
  return {
    getItem: function (k) { if (mode === "broken") throw new Error("broken"); return k in store ? store[k] : null; },
    setItem: function (k, v) { if (mode === "broken") throw new Error("broken"); store[k] = String(v); }
  };
} });
` + string(src) + `
window.ccbArt.mount({ appendChild: function () {} }, true);
var stored = store["ccb.site.intro"] || "";
` + body
	out, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// calmMask returns the calm pose as runIntro's visible() draws it, with only
// the cells whose letter is in keep shown, or every drawn cell when keep is
// empty.
func calmMask(t *testing.T, keep string) string {
	t.Helper()
	grid, _ := calmArt(t)
	var rows []string
	for _, row := range grid {
		b := []byte(row)
		for x, c := range b {
			if c != '.' && (keep == "" || strings.IndexByte(keep, c) >= 0) {
				b[x] = '#'
			} else {
				b[x] = '.'
			}
		}
		rows = append(rows, string(b))
	}
	return strings.Join(rows, "\n")
}

// TestFirstVisitStartsWithTheBaby checks the intro of a first view in a
// tab: the very first drawing is the baby alone, where she stands in the
// calm pose, the tab is marked as having seen it, and timers are set. Her
// kangaroo is there by the end of the intro, and the loop's first worried
// pose waits until she has fully arrived: at 8 seconds she is still calm,
// where a loop started at load would already be alert.
func TestFirstVisitStartsWithTheBaby(t *testing.T) {
	out := runIntro(t, "empty", false, `
console.log(visible().join("\n"));
console.log("--");
console.log(stored + " " + (timers > 0));
console.log("--");
runUntil(8000);
console.log(visible().join("\n"));`)
	parts := strings.Split(out, "\n--\n")
	if len(parts) != 3 {
		t.Fatalf("unexpected output %q", out)
	}
	if want := calmMask(t, "hsc"); parts[0] != want {
		t.Errorf("the first drawing is\n%s\nwant only the baby\n%s", parts[0], want)
	}
	if parts[1] != "1 true" {
		t.Errorf("the intro key and timers are %q, want the key set to 1 and timers set", parts[1])
	}
	if want := calmMask(t, ""); parts[2] != want {
		t.Errorf("at 8 s the picture is\n%s\nwant the calm pose\n%s", parts[2], want)
	}
}

// TestIntroOncePerTab checks that a tab that has seen the intro, or whose
// storage cannot be read or written, starts with the kangaroo and the baby
// together, as before the intro, without an exception.
func TestIntroOncePerTab(t *testing.T) {
	want := calmMask(t, "")
	for _, storage := range []string{"set", "blocked", "broken"} {
		out := runIntro(t, storage, false, `console.log(visible().join("\n")); console.log(timers > 0);`)
		if out != want+"\ntrue" {
			t.Errorf("storage %s: got\n%s\nwant the calm pose at once and the loop's timers\n%s", storage, out, want)
		}
	}
}

// TestIntroKeepsStillWithReducedMotion checks that reduced motion shows the
// calm pose at once, with no intro and no timers.
func TestIntroKeepsStillWithReducedMotion(t *testing.T) {
	out := runIntro(t, "empty", true, `console.log(visible().join("\n")); console.log(timers + " " + JSON.stringify(stored));`)
	if want := calmMask(t, "") + "\n0 \"\""; out != want {
		t.Errorf("got\n%s\nwant the calm pose, no timers and no intro key\n%s", out, want)
	}
}

// TestIntroFinishesInABackgroundTab checks that when the kangaroo is due to
// arrive while the page is hidden, she is drawn at once, setting no timers
// for her pixels, so she is there when the visitor comes back. The same
// moment on a shown page sets a timer for each arriving pixel, which makes
// the count a fair control.
func TestIntroFinishesInABackgroundTab(t *testing.T) {
	body := `
runUntil(2499);
var before = timers;
runUntil(2500);
console.log(timers - before);
console.log(visible().join("\n"));`
	shown := runIntro(t, "empty", false, body)
	hidden := runIntro(t, "empty", false, "document.hidden = true;\n"+body)
	n, err := strconv.Atoi(strings.SplitN(shown, "\n", 2)[0])
	if err != nil || n < 10 {
		t.Fatalf("on a shown page the kangaroo must arrive at 2.5 s with a timer per pixel, got %q", shown)
	}
	if want := "0\n" + calmMask(t, ""); hidden != want {
		t.Errorf("on a hidden page got\n%s\nwant no timers and the calm pose at once\n%s", hidden, want)
	}
}
