package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunVersion(t *testing.T) {
	out := captureStdout(t, func() {
		if rc := run([]string{"version"}); rc != 0 {
			t.Fatalf("rc = %d", rc)
		}
	})
	if !strings.Contains(out, buildinfo.Name) || !strings.Contains(out, buildinfo.Version) {
		t.Fatalf("unexpected version output: %q", out)
	}
}

func TestRunBogusSubcommand(t *testing.T) {
	if rc := run([]string{"bogus"}); rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
}

func TestRunUnknownFlag(t *testing.T) {
	if rc := run([]string{"--not-a-real-flag"}); rc != 2 {
		t.Fatalf("rc = %d, want 2", rc)
	}
}

// The usage text calls the web page the page, and follows the program's
// own text rules: no parentheses.
func TestUsageSaysPageWithoutParentheses(t *testing.T) {
	var b strings.Builder
	printUsage(&b)
	out := b.String()
	if strings.Contains(strings.ToLower(out), "cockpit") {
		t.Fatalf("the usage says page, not cockpit:\n%s", out)
	}
	if strings.ContainsAny(out, "();") {
		t.Fatalf("no parentheses or semicolons in the usage:\n%s", out)
	}
	for _, want := range []string{"the page", "by default", "47391", "random free port",
		"start CC Babysitter. It runs in the background and starts at login", "--foreground"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}
}

// --service is what the systemd unit passes. It serves in the foreground
// and never opens a browser, and nobody needs to type it, so the usage
// leaves it out.
func TestServiceFlag(t *testing.T) {
	opts, err := parseServeFlags([]string{"--service"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.NoOpen || opts.Demo || opts.Port != defaultPort {
		t.Fatalf("%+v", opts)
	}
	var b strings.Builder
	printUsage(&b)
	if strings.Contains(b.String(), "--service") {
		t.Fatalf("--service is not in the usage:\n%s", b.String())
	}
}

func TestParseServeFlags(t *testing.T) {
	opts, err := parseServeFlags([]string{"--no-open", "--port", "5000", "--demo"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.NoOpen || !opts.Demo || opts.Port != 5000 {
		t.Fatalf("%+v", opts)
	}
	if opts, _ := parseServeFlags(nil, false); opts.NoOpen || opts.Service || opts.Port != defaultPort {
		t.Fatalf("defaults: %+v", opts)
	}
	if opts, _ := parseServeFlags([]string{"--service"}, false); !opts.Service || !opts.NoOpen {
		t.Fatalf("--service: %+v", opts)
	}
	// A unit written by an older build runs --no-open, not --service, and
	// one may run with no flags at all, but systemd still sends its output
	// to the journal: that is service output too, and opens no browser.
	for _, args := range [][]string{{"--no-open"}, nil} {
		if opts, _ := parseServeFlags(args, true); !opts.Service || !opts.NoOpen {
			t.Fatalf("%v to the journal: %+v", args, opts)
		}
	}
	if opts, _ := parseServeFlags([]string{"--no-open"}, false); opts.Service {
		t.Fatalf("--no-open in a terminal: %+v", opts)
	}
	if _, err := parseServeFlags([]string{"--no-open", "extra"}, false); err == nil {
		t.Fatal("a stray argument is refused")
	}
	if opts, _ := parseServeFlags([]string{"--foreground"}, false); !opts.Foreground || opts.PortSet {
		t.Fatalf("--foreground: %+v", opts)
	}
	if opts, _ := parseServeFlags([]string{"--port", "5000"}, false); !opts.PortSet || opts.Port != 5000 {
		t.Fatalf("--port: %+v", opts)
	}
}

// A command's help flag shows its page and runs nothing: install and
// uninstall change the service, and reset deletes the state folder.
func TestHelpFlagRunsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "--help"}, "ccbabysitter install\n"},
		{[]string{"install", "-h"}, "ccbabysitter install\n"},
		{[]string{"uninstall", "--help"}, "ccbabysitter uninstall\n"},
		{[]string{"uninstall", "-help"}, "ccbabysitter uninstall\n"},
		{[]string{"reset", "--help"}, "ccbabysitter reset\n"},
		{[]string{"version", "-h"}, "ccbabysitter version\n"},
		{[]string{"-help"}, "Run it:"},
	} {
		var rc int
		out := captureStdout(t, func() { rc = run(c.args) })
		if rc != 0 || !strings.Contains(out, c.want) {
			t.Errorf("run(%v) = %d, %q", c.args, rc, out)
		}
		if strings.Contains(out, buildinfo.Version) && c.args[0] == "version" {
			t.Errorf("run(%v) printed the version instead of the page", c.args)
		}
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Words after version, install, uninstall or reset are a usage error: the
// usage goes to stderr, and nothing runs, nothing prompts and nothing is
// deleted.
func TestExtraWordsAfterOneShotCommandsAreUsageErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	for _, args := range [][]string{
		{"version", "extra"},
		{"version", "--json", "extra"},
		{"install", "extra"},
		{"uninstall", "extra"},
		{"reset", "extra"},
		{"reset", "--yes"},
	} {
		var rc int
		var stdout string
		stderr := captureStderr(t, func() {
			stdout = captureStdout(t, func() { rc = run(args) })
		})
		if rc != 2 {
			t.Errorf("run(%v) = %d, want 2", args, rc)
		}
		if stdout != "" {
			t.Errorf("run(%v) printed on stdout: %q", args, stdout)
		}
		if !strings.Contains(stderr, "Run it:") {
			t.Errorf("run(%v) did not print the usage on stderr: %q", args, stderr)
		}
	}
}

// version --json also says whether this machine has a display, which an
// AI agent needs before offering to open the page, without running
// anything that prints the page's key.
func TestVersionJSON(t *testing.T) {
	saved := versionHeadless
	t.Cleanup(func() { versionHeadless = saved })
	for _, headless := range []bool{false, true} {
		versionHeadless = func() bool { return headless }
		var rc int
		out := captureStdout(t, func() { rc = run([]string{"version", "--json"}) })
		want := `{"schema":1,"name":"CC Babysitter","version":"` + buildinfo.Version + `","headless":` + fmt.Sprint(headless) + `}` + "\n"
		if rc != 0 || out != want {
			t.Fatalf("version --json = %d, %q; want %q", rc, out, want)
		}
	}
}
