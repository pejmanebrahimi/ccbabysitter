package site

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The docs screenshots in docs/shots are taken from the demo page by
// scripts/docs-shots.mjs, in light and dark, and manifest.json records
// each shot's size and a hash of the page code and the demo they show.
// These tests ask for new shots when that code changes, and keep the pages
// that show them in step with the manifest.

const reshoot = "run `node scripts/docs-shots.mjs`, which needs Node 22 or later and Chrome, Chromium or Edge, and commit the new shots"

type shotManifest struct {
	Page  string `json:"page"`
	Shots map[string]struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"shots"`
}

func readManifest(t *testing.T) shotManifest {
	t.Helper()
	var m shotManifest
	if err := json.Unmarshal([]byte(readSite(t, "docs/shots/manifest.json")), &m); err != nil {
		t.Fatalf("docs/shots/manifest.json: %v", err)
	}
	return m
}

// pageHash is the hash scripts/docs-shots.mjs records: SHA-256 over the
// path and contents of every file of the page and of the demo, line
// endings made LF, in path order.
func pageHash(t *testing.T) string {
	t.Helper()
	ui := filepath.Join("..", "internal", "web", "ui")
	entries, err := os.ReadDir(ui)
	if err != nil {
		t.Fatal(err)
	}
	files := []string{"internal/web/demo.go"}
	for _, e := range entries {
		files = append(files, "internal/web/ui/"+e.Name())
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(f + "\n"))
		h.Write([]byte(strings.ReplaceAll(string(b), "\r\n", "\n")))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestDocsShotsAreCurrent fails when the page or the demo changed after the
// docs screenshots were taken.
func TestDocsShotsAreCurrent(t *testing.T) {
	if got := readManifest(t).Page; got != pageHash(t) {
		t.Fatalf("internal/web/ui or internal/web/demo.go changed since the docs screenshots were taken: %s", reshoot)
	}
}

// TestDocsShotsHashTheSameWay checks that the script and the test above
// hash the page the same way.
func TestDocsShotsHashTheSameWay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := `import("../scripts/docs-shots.mjs").then((m) => console.log(m.pageHash("..")))`
	out, err := exec.Command(node, "--input-type=module", "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), pageHash(t); got != want {
		t.Fatalf("the script hashes the page as %s, the test as %s", got, want)
	}
}

var (
	shotFigure = regexp.MustCompile(`(?s)<figure class="shot">(.*?)</figure>`)
	shotImg    = regexp.MustCompile(`<img class="(light|dark)-only" src="/docs/shots/([a-z-]+)-(light|dark)\.webp" width="(\d+)" height="(\d+)" alt="([^"]*)" loading="lazy">`)
)

// TestDocsShotsAreShownWhole checks that every shot in the manifest has its
// light and its dark picture and is shown on a docs page, that no picture
// is left over, and that each figure shows one shot as a light and a dark
// picture of the manifest's size, with the same alt text.
func TestDocsShotsAreShownWhole(t *testing.T) {
	m := readManifest(t)
	files := map[string]bool{}
	for _, f := range siteFiles(t) {
		if strings.HasPrefix(f, "docs/shots/") && strings.HasSuffix(f, ".webp") {
			files[strings.TrimPrefix(f, "docs/shots/")] = true
		}
	}
	for name := range m.Shots {
		for _, theme := range []string{"light", "dark"} {
			f := name + "-" + theme + ".webp"
			if !files[f] {
				t.Errorf("the shot %s has no %s picture: %s", name, theme, reshoot)
			}
			delete(files, f)
		}
	}
	for f := range files {
		t.Errorf("docs/shots/%s is not in the manifest", f)
	}

	shown := map[string]bool{}
	for _, f := range docsPages(t) {
		s := readSite(t, f)
		figures := shotFigure.FindAllStringSubmatch(s, -1)
		if n := strings.Count(s, "/docs/shots/"); n != 2*len(figures) {
			t.Errorf("%s shows %d pictures from docs/shots outside the expected pairs", f, n-2*len(figures))
		}
		for _, fig := range figures {
			imgs := shotImg.FindAllStringSubmatch(fig[1], -1)
			if len(imgs) != 2 || imgs[0][1] != "light" || imgs[1][1] != "dark" {
				t.Errorf("%s: a figure must hold a light-only and then a dark-only picture: %s", f, fig[1])
				continue
			}
			name := imgs[0][2]
			size, ok := m.Shots[name]
			if !ok {
				t.Errorf("%s shows %s, which is not in the manifest", f, name)
				continue
			}
			shown[name] = true
			for _, img := range imgs {
				if img[2] != name || img[3] != img[1] {
					t.Errorf("%s: a figure mixes pictures: %s", f, fig[1])
				}
				if img[4] != strconv.Itoa(size.Width) || img[5] != strconv.Itoa(size.Height) {
					t.Errorf("%s shows %s at %sx%s, the manifest says %dx%d", f, name, img[4], img[5], size.Width, size.Height)
				}
				if strings.TrimSpace(img[6]) == "" {
					t.Errorf("%s shows %s without alt text", f, name)
				}
			}
			if imgs[0][6] != imgs[1][6] {
				t.Errorf("%s: the light and dark pictures of %s have different alt text", f, name)
			}
		}
	}
	for name := range m.Shots {
		if !shown[name] {
			t.Errorf("the shot %s is not shown on any docs page", name)
		}
	}
}

// webpSize reads a WebP picture's width and height from its header, for the
// three kinds of WebP file: lossy, lossless and extended.
func webpSize(b []byte) (int, int, bool) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return le(b[26:28]) & 0x3fff, le(b[28:30]) & 0x3fff, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		bits := le(b[21:25])
		return bits&0x3fff + 1, bits>>14&0x3fff + 1, true
	case "VP8X":
		return le(b[24:27]) + 1, le(b[27:30]) + 1, true
	}
	return 0, 0, false
}

// le reads a little-endian number of up to four bytes.
func le(b []byte) int {
	n := 0
	for i := len(b) - 1; i >= 0; i-- {
		n = n<<8 | int(b[i])
	}
	return n
}

// TestDocsShotsAreTwiceTheirSize checks that every picture is twice the
// size the manifest gives its shot, so it is sharp on a high-density
// screen and its light and dark pictures match.
func TestDocsShotsAreTwiceTheirSize(t *testing.T) {
	for name, size := range readManifest(t).Shots {
		for _, theme := range []string{"light", "dark"} {
			f := "docs/shots/" + name + "-" + theme + ".webp"
			b, err := os.ReadFile(filepath.FromSlash(f))
			if err != nil {
				t.Errorf("%s: %v", f, err)
				continue
			}
			w, h, ok := webpSize(b)
			if !ok {
				t.Errorf("%s is not a WebP picture this test can read", f)
				continue
			}
			if w != 2*size.Width || h != 2*size.Height {
				t.Errorf("%s is %dx%d, want twice the manifest's %dx%d: %s", f, w, h, size.Width, size.Height, reshoot)
			}
		}
	}
}
