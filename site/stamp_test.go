package site

import (
	"errors"
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
	// anyAsset is any address in a page under /assets/ or /docs/shots/,
	// whatever the kind of file or the attribute, so a file the script's
	// pattern leaves out shows up here.
	anyAsset = regexp.MustCompile(`(?:href|src|srcset|content)=["']?/(?:assets|docs/shots)/[^"' >]*`)
)

func stampScript(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the deploy stamps the site on Linux")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh is not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", "scripts", "stamp-site.sh"))
	if err != nil {
		t.Fatal(err)
	}
	return sh, script
}

// copyPages copies every page of the site into a new folder.
func copyPages(t *testing.T) string {
	t.Helper()
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
	return dir
}

// runStamp runs the script on dir with args and returns its exit code and
// output.
func runStamp(t *testing.T, dir string, args ...string) (int, string) {
	t.Helper()
	sh, script := stampScript(t)
	out, err := exec.Command(sh, append([]string{script, dir}, args...)...).CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// readPages reads every page in dir by its path in the site.
func readPages(t *testing.T, dir string) map[string]string {
	t.Helper()
	pages := map[string]string{}
	for _, f := range siteFiles(t) {
		if !strings.HasSuffix(f, ".html") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		pages[f] = string(b)
	}
	return pages
}

// TestStampMarksEveryAssetAndNothingElse checks that the script adds the
// stamp to every stylesheet, script and screenshot a page loads, and only
// there, and that a second run changes nothing.
func TestStampMarksEveryAssetAndNothingElse(t *testing.T) {
	stampScript(t)
	dir := copyPages(t)
	if code, out := runStamp(t, dir, stamp); code != 0 {
		t.Fatalf("stamp-site.sh exited %d:\n%s", code, out)
	}
	first := readPages(t, dir)
	if len(first) == 0 {
		t.Fatal("no pages were stamped")
	}
	for f, after := range first {
		before := readSite(t, f)
		n := len(assetRef.FindAllString(before, -1))
		if m := len(stampedRef.FindAllString(after, -1)); m != n {
			t.Errorf("%s loads %d stylesheets, scripts and screenshots, %d of them stamped", f, n, m)
		}
		if m := strings.Count(after, "?v="+stamp); m != n {
			t.Errorf("%s has %d stamps for %d stylesheets, scripts and screenshots", f, m, n)
		}
		for _, ref := range anyAsset.FindAllString(after, -1) {
			if !strings.Contains(ref, "?v="+stamp) {
				t.Errorf("%s loads %s without the stamp", f, ref)
			}
		}
		if strings.ReplaceAll(after, "?v="+stamp, "") != before {
			t.Errorf("%s changed beyond the stamps", f)
		}
	}
	if code, out := runStamp(t, dir, stamp); code != 0 {
		t.Fatalf("a second run exited %d:\n%s", code, out)
	}
	for f, again := range readPages(t, dir) {
		if again != first[f] {
			t.Errorf("a second run changed %s", f)
		}
	}
}

// TestStampWantsAStampAndPages checks that the script refuses, with exit
// code 2 and a reason, a missing stamp, one that is not letters and
// digits, and a folder that is not there, and stops when the folder has no
// pages.
func TestStampWantsAStampAndPages(t *testing.T) {
	stampScript(t)
	dir := copyPages(t)
	for _, args := range [][]string{nil, {""}, {"a b"}, {"x;rm"}} {
		if code, out := runStamp(t, dir, args...); code != 2 || !strings.Contains(out, "letters and digits") {
			t.Errorf("stamp-site.sh with %q exited %d:\n%s", args, code, out)
		}
	}
	if code, out := runStamp(t, filepath.Join(dir, "missing"), stamp); code != 2 || !strings.Contains(out, "no such folder") {
		t.Errorf("stamp-site.sh on a missing folder exited %d:\n%s", code, out)
	}
	if code, out := runStamp(t, t.TempDir(), stamp); code != 1 || !strings.Contains(out, "no pages") {
		t.Errorf("stamp-site.sh on an empty folder exited %d:\n%s", code, out)
	}
}

// TestTheDeployStampsTheSite checks that the deploy runs the script on the
// assembled site with the commit it deploys, in a line that runs, and
// deploys again when the script changes.
func TestTheDeployStampsTheSite(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "site.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(b)
	if !regexp.MustCompile(`(?m)^ +sh scripts/stamp-site\.sh _site "\$\{GITHUB_SHA::12\}"$`).MatchString(workflow) {
		t.Error("site.yml does not stamp _site with the commit")
	}
	if !strings.Contains(workflow, `      - "scripts/stamp-site.sh"`) {
		t.Error("site.yml does not deploy again when scripts/stamp-site.sh changes")
	}
}
