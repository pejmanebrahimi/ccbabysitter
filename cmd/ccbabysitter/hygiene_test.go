package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is where this package sits relative to the top of the tree.
const repoRoot = "../.."

// shippedRoots are the folders whose every file is published, and
// shippedFiles are the published files that sit at the top of the tree.
var (
	shippedRoots = []string{"cmd", "internal", "scripts", ".github", "site", "plugin", ".claude-plugin"}
	shippedFiles = []string{
		"README.md", "CONTRIBUTING.md", "SECURITY.md", "LICENSE", "AGENTS.md", "CLAUDE.md",
		filepath.Join("docs", "CHANGELOG.md"), "go.mod", ".gitignore", ".gitattributes",
	}
)

// Everything published has to be plain ASCII text. A byte outside that is
// either a character an editor may render differently from what the next
// reader sees, or a control byte that makes every text tool treat the file
// as binary: grep stops reporting matches in it, so a check that greps for
// something unwanted would pass on a file full of it.
//
// This walks the whole published tree rather than one package's own files,
// because the file that picks up a stray byte is never the one anybody
// thought to check.
func TestEverythingPublishedIsPlainASCIIText(t *testing.T) {
	for _, path := range shippedPaths(t) {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		for i := 0; i < len(body); i++ {
			if !plainTextByte(body[i]) {
				t.Errorf("%s: byte 0x%02x at offset %d is not plain ASCII text%s",
					path, body[i], i, nearby(body, i))
				break
			}
		}
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

// nearby renders the printable text around an offset, so a failure says
// where in the file to look rather than only how far in.
func nearby(body []byte, at int) string {
	from, to := at-40, at+40
	if from < 0 {
		from = 0
	}
	if to > len(body) {
		to = len(body)
	}
	var b strings.Builder
	for _, c := range body[from:to] {
		if c >= 0x20 && c <= 0x7e {
			b.WriteByte(c)
		} else {
			b.WriteByte('.')
		}
	}
	return ", near: " + b.String()
}

// shippedPaths lists every published file. A listed top-level file that is
// missing fails the test, so renaming one cannot quietly take it out of
// the check.
func shippedPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, name := range shippedFiles {
		path := filepath.Join(repoRoot, name)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is listed as published but cannot be read: %v", name, err)
			continue
		}
		paths = append(paths, path)
	}
	for _, root := range shippedRoots {
		err := filepath.WalkDir(filepath.Join(repoRoot, root), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// Local eval results: gitignored, and they hold replies in
				// other languages and may hold real session text.
				if filepath.ToSlash(path) == repoRoot+"/plugin/evals/results" {
					return filepath.SkipDir
				}
				return nil
			}
			// Binary assets of the website; site/site_test.go checks its text files.
			if ext := strings.ToLower(filepath.Ext(path)); ext == ".png" || ext == ".woff2" {
				return nil
			}
			paths = append(paths, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(paths) < 20 {
		t.Fatalf("only found %d published files, so this is not checking what it thinks it is", len(paths))
	}
	return paths
}
