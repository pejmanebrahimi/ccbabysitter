package site

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the generated images")

// calmArt reads the calm pose and its colours out of art.js.
func calmArt(t *testing.T) ([]string, map[byte]color.RGBA) {
	t.Helper()
	src := readSite(t, "assets/art.js")
	m := regexp.MustCompile(`calm:\s*\[([^\]]*)\]`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("no calm pose in art.js")
	}
	rows := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(m[1], -1)
	var grid []string
	for _, r := range rows {
		grid = append(grid, r[1])
	}
	cm := regexp.MustCompile(`var COLOR = \{([^}]*)\}`).FindStringSubmatch(src)
	if cm == nil {
		t.Fatal("no COLOR in art.js")
	}
	cols := map[byte]color.RGBA{}
	for _, p := range regexp.MustCompile(`([a-z]):\s*"#([0-9a-f]{6})"`).FindAllStringSubmatch(cm[1], -1) {
		v, _ := strconv.ParseUint(p[2], 16, 32)
		cols[p[1][0]] = color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
	}
	return grid, cols
}

func fill(img *image.RGBA, c color.RGBA) {
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
}

func block(img *image.RGBA, x, y, s int, c color.RGBA) {
	for dy := 0; dy < s; dy++ {
		for dx := 0; dx < s; dx++ {
			img.SetRGBA(x+dx, y+dy, c)
		}
	}
}

func drawArt(img *image.RGBA, grid []string, cols map[byte]color.RGBA, x0, y0, s int) {
	for y, row := range grid {
		for x := 0; x < len(row); x++ {
			if c, ok := cols[row[x]]; ok {
				block(img, x0+x*s, y0+y*s, s, c)
			}
		}
	}
}

// A 5 by 7 pixel font with just the letters of the name.
var glyphs = map[rune][7]string{
	'C': {".###.", "#...#", "#....", "#....", "#....", "#...#", ".###."},
	'B': {"####.", "#...#", "#...#", "####.", "#...#", "#...#", "####."},
	'a': {".....", ".....", ".###.", "....#", ".####", "#...#", ".####"},
	'b': {"#....", "#....", "####.", "#...#", "#...#", "#...#", "####."},
	'y': {".....", ".....", "#...#", "#...#", ".####", "....#", ".###."},
	's': {".....", ".....", ".####", "#....", ".###.", "....#", "####."},
	'i': {"..#..", ".....", ".##..", "..#..", "..#..", "..#..", ".###."},
	't': {".#...", ".#...", "####.", ".#...", ".#...", ".#..#", "..##."},
	'e': {".....", ".....", ".###.", "#...#", "#####", "#....", ".###."},
	'r': {".....", ".....", "#.##.", "##..#", "#....", "#....", "#...."},
	' ': {".....", ".....", ".....", ".....", ".....", ".....", "....."},
}

func drawText(img *image.RGBA, text string, x0, y0, s int, c color.RGBA) {
	for i, r := range text {
		g := glyphs[r]
		for y, row := range g {
			for x := 0; x < 5; x++ {
				if row[x] == '#' {
					block(img, x0+i*6*s+x*s, y0+y*s, s, c)
				}
			}
		}
	}
}

func encode(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var (
	paper = color.RGBA{0xf3, 0xef, 0xe7, 255}
	ink   = color.RGBA{0x17, 0x15, 0x12, 255}
	line  = color.RGBA{0xdd, 0xd5, 0xc7, 255}
)

func ogImage(grid []string, cols map[byte]color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 1200, 630))
	fill(img, paper)
	for x := 0; x < 1200; x++ {
		img.SetRGBA(x, 440, line)
	}
	drawArt(img, grid, cols, (1200-16*16)/2, 440-15*16, 16)
	name := "CC Babysitter"
	w := len(name)*6*6 - 6
	drawText(img, name, (1200-w)/2, 490, 6, ink)
	return img
}

func icon(grid []string, cols map[byte]color.RGBA, size, scale int, bg *color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	if bg != nil {
		fill(img, *bg)
	}
	off := (size - 16*scale) / 2
	drawArt(img, grid, cols, off, off, scale)
	return img
}

func faviconSVG(grid []string, cols map[byte]color.RGBA) []byte {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" shape-rendering="crispEdges">`)
	for y, row := range grid {
		for x := 0; x < len(row); x++ {
			if c, ok := cols[row[x]]; ok {
				fmt.Fprintf(&b, `<rect x="%d" y="%d" width="1" height="1" fill="#%02x%02x%02x"/>`, x, y, c.R, c.G, c.B)
			}
		}
	}
	b.WriteString("</svg>\n")
	return []byte(b.String())
}

func TestImagesMatchTheArt(t *testing.T) {
	grid, cols := calmArt(t)
	if len(grid) != 16 {
		t.Fatalf("the calm grid has %d rows, want 16", len(grid))
	}
	for y, row := range grid {
		if len(row) != 16 {
			t.Fatalf("calm row %d has %d columns, want 16", y, len(row))
		}
		for x := 0; x < len(row); x++ {
			if _, ok := cols[row[x]]; !ok && row[x] != '.' {
				t.Fatalf("calm row %d column %d is %q, which has no colour in COLOR", y, x, row[x])
			}
		}
	}
	want := map[string][]byte{
		"og.png":               encode(t, ogImage(grid, cols)),
		"favicon-32.png":       encode(t, icon(grid, cols, 32, 2, nil)),
		"apple-touch-icon.png": encode(t, icon(grid, cols, 180, 10, &paper)),
		"favicon.svg":          faviconSVG(grid, cols),
		// The Claude Code plugin's icon, which Anthropic's plugin directory
		// shows on its listing: a square PNG of 512 to 2048 pixels.
		"../plugin/.claude-plugin/icon.png": encode(t, icon(grid, cols, 512, 28, &paper)),
	}
	for name, b := range want {
		if *update {
			if err := os.WriteFile(name, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./site -run TestImagesMatchTheArt -update)", name, err)
		}
		if !bytes.Equal(got, b) {
			t.Errorf("%s does not match the art in art.js; run go test ./site -run TestImagesMatchTheArt -update", name)
		}
	}
}
