package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The deploy runs scripts/stamp-site.sh on the assembled site, so every
// page asks for its stylesheets, scripts and docs screenshots under an
// address that changes with each deploy, and a browser never pairs a new
// page with an old stylesheet it kept for a few minutes.

const stamp = "abc123def456"

var (
	assetRef   = regexp.MustCompile(`(?:href|src)="(/assets/[^"]+\.(?:css|js)|/docs/shots/[^"]+\.webp)[^"]*"`)
	stampedRef = regexp.MustCompile(`(?:href|src)="(?:/assets/[^"?]+\.(?:css|js)|/docs/shots/[^"?]+\.webp)\?v=` + stamp + `"`)
)

// stampCopy copies every page of the site into a new folder, runs the
// stamp script on it with args, and returns the folder.
func stampCopy(t *testing.T, args ...string) (string, error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the deploy stamps the site on Linux")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	dir := t.TempDir()
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") {
			continue
		}
		to := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, []byte(readSite(t, f)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "stamp-site.sh"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(sh, append([]string{script, dir}, args...)...).CombinedOutput()
	if err != nil {
		return dir, &stampError{err, string(out)}
	}
	return dir, nil
}

type stampError struct {
	err error
	out string
}

func (e *stampError) Error() string { return e.err.Error() + ": " + e.out }

// TestStampMarksEveryAssetAndNothingElse checks that the script adds the
// stamp to every stylesheet, script and screenshot a page loads, changes
// nothing else, and changes nothing more when run a second time.
func TestStampMarksEveryAssetAndNothingElse(t *testing.T) {
	dir, err := stampCopy(t, stamp)
	if err != nil {
		t.Fatal(err)
	}
	pages := 0
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") {
			continue
		}
		pages++
		before := readSite(t, f)
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		after := string(b)
		if n, m := len(assetRef.FindAllString(before, -1)), len(stampedRef.FindAllString(after, -1)); n != m {
			t.Errorf("%s loads %d stylesheets, scripts and screenshots, %d of them stamped", f, n, m)
		}
		if strings.ReplaceAll(after, "?v="+stamp, "") != before {
			t.Errorf("%s changed beyond the stamps", f)
		}
	}
	if pages == 0 {
		t.Fatal("no pages were stamped")
	}
	if _, err := exec.LookPath("sh"); err == nil {
		script, _ := filepath.Abs(filepath.Join("..", "scripts", "stamp-site.sh"))
		if out, err := exec.Command("sh", script, dir, stamp).CombinedOutput(); err != nil {
			t.Fatalf("a second run failed: %v\n%s", err, out)
		}
		b, err := os.ReadFile(filepath.Join(dir, "docs", "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "?v="+stamp+"?v=") {
			t.Error("a second run stamped the addresses again")
		}
	}
}

// TestStampWantsAStamp checks that the script refuses to run without a
// stamp, or with one that is not letters and digits.
func TestStampWantsAStamp(t *testing.T) {
	for _, args := range [][]string{nil, {""}, {"a b"}, {"x;rm"}} {
		if _, err := stampCopy(t, args...); err == nil {
			t.Errorf("stamp-site.sh ran with %q", args)
		}
	}
}

// TestTheDeployStampsTheSite checks that the deploy runs the script on the
// assembled site with the commit it deploys.
func TestTheDeployStampsTheSite(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `sh scripts/stamp-site.sh _site "${GITHUB_SHA::12}"`) {
		t.Error("site.yml does not stamp _site with the commit")
	}
}
