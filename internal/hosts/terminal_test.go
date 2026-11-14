package hosts

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// started is one program a launcher asked to start, recorded instead of
// run, so no test ever opens a window.
type started struct {
	name string
	args []string
}

// fakeLauncher builds a launcher for goos that finds only the programs in
// onPath and records what it would have started.
func fakeLauncher(t *testing.T, goos string, onPath map[string]string) (*TerminalLauncher, *[]started) {
	t.Helper()
	var calls []started
	l := &TerminalLauncher{
		OS:  goos,
		Dir: t.TempDir(),
		LookPath: func(name string) (string, error) {
			if p, ok := onPath[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Start: func(name string, args ...string) error {
			calls = append(calls, started{name, append([]string(nil), args...)})
			return nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC) },
	}
	return l, &calls
}

// skipOnWindowsHost skips a test of the macOS or Linux launcher on a
// Windows host. Those launchers are only ever reached on macOS and Linux,
// and the CLI path they are handed is a POSIX one, which the host's own
// path rules, used to find the CLI, do not read as absolute on Windows.
func skipOnWindowsHost(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the macOS and Linux launchers only run on macOS and Linux hosts")
	}
}

// On macOS the attach command goes into a small script in this program's
// own folder, which Terminal is asked to open. The path to the CLI is
// quoted for the shell that runs the script.
func TestOpenAttachOnMacOSWritesAScriptForTerminal(t *testing.T) {
	skipOnWindowsHost(t)
	l, calls := fakeLauncher(t, "darwin", map[string]string{"claude": "/Users/dev/my tools/claude"})
	app, err := l.OpenAttach("4d4d4d4d")
	if err != nil || app != "Terminal" {
		t.Fatalf("%q %v", app, err)
	}
	if len(*calls) != 1 {
		t.Fatalf("%+v", *calls)
	}
	c := (*calls)[0]
	if c.name != "open" || len(c.args) != 3 || c.args[0] != "-a" || c.args[1] != "Terminal" {
		t.Fatalf("%+v", c)
	}
	script := c.args[2]
	if filepath.Dir(script) != l.Dir || !strings.HasSuffix(script, ".command") {
		t.Fatalf("the script belongs in this program's own folder: %s", script)
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\nexec '/Users/dev/my tools/claude' attach 4d4d4d4d\n"; string(body) != want {
		t.Fatalf("script:\n%s\nwant:\n%s", body, want)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(script)
		if err != nil || fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("the script must be executable: %v %v", fi.Mode(), err)
		}
	}
}

// Scripts older than a minute are cleared away on each call, and newer
// ones are left for the Terminal that may still be reading them.
func TestOpenAttachOnMacOSClearsOldScripts(t *testing.T) {
	l, _ := fakeLauncher(t, "darwin", map[string]string{"claude": "/usr/local/bin/claude"})
	old := filepath.Join(l.Dir, "attach-11111111-1.command")
	fresh := filepath.Join(l.Dir, "attach-22222222-2.command")
	other := filepath.Join(l.Dir, "state.json")
	for _, p := range []string{old, fresh, other} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := l.Now().Add(-2 * time.Minute)
	for _, p := range []string{old, other} {
		if err := os.Chtimes(p, long, long); err != nil {
			t.Fatal(err)
		}
	}
	recent := l.Now().Add(-10 * time.Second)
	if err := os.Chtimes(fresh, recent, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := l.OpenAttach("4d4d4d4d"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("a script older than a minute is removed")
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s must be left alone: %v", filepath.Base(p), err)
		}
	}
}

// On Linux the first terminal found on PATH, in a fixed order, is started
// on the attach command, each part of it its own argument.
func TestOpenAttachOnLinuxUsesTheFirstTerminalFound(t *testing.T) {
	skipOnWindowsHost(t)
	cases := []struct {
		name   string
		onPath map[string]string
		want   []string
	}{
		{"the system's choice first", map[string]string{"x-terminal-emulator": "/usr/bin/x-terminal-emulator", "xterm": "/usr/bin/xterm"},
			[]string{"/usr/bin/x-terminal-emulator", "-e", "/home/dev/.local/bin/claude", "attach", "4d4d4d4d"}},
		{"gnome", map[string]string{"gnome-terminal": "/usr/bin/gnome-terminal", "konsole": "/usr/bin/konsole"},
			[]string{"/usr/bin/gnome-terminal", "--", "/home/dev/.local/bin/claude", "attach", "4d4d4d4d"}},
		{"konsole", map[string]string{"konsole": "/usr/bin/konsole", "xterm": "/usr/bin/xterm"},
			[]string{"/usr/bin/konsole", "-e", "/home/dev/.local/bin/claude", "attach", "4d4d4d4d"}},
		{"xterm", map[string]string{"xterm": "/usr/bin/xterm"},
			[]string{"/usr/bin/xterm", "-e", "/home/dev/.local/bin/claude", "attach", "4d4d4d4d"}},
	}
	for _, c := range cases {
		c.onPath["claude"] = "/home/dev/.local/bin/claude"
		l, calls := fakeLauncher(t, "linux", c.onPath)
		app, err := l.OpenAttach("4d4d4d4d")
		if err != nil || app != "a terminal" || len(*calls) != 1 {
			t.Fatalf("%s: %q %v %+v", c.name, app, err, *calls)
		}
		got := append([]string{(*calls)[0].name}, (*calls)[0].args...)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s: started %q, want %q", c.name, got, c.want)
		}
	}

	l, calls := fakeLauncher(t, "linux", map[string]string{"claude": "/usr/bin/claude"})
	if _, err := l.OpenAttach("4d4d4d4d"); err == nil || len(*calls) != 0 {
		t.Fatalf("with no terminal on PATH nothing is started: %v %+v", err, *calls)
	}
}

// On Windows it is Windows Terminal when it is there, and a console
// window through cmd.exe when it is not.
func TestOpenAttachOnWindows(t *testing.T) {
	cli := `C:\Users\dev\AppData\Local\Programs\claude\claude.exe`
	l, calls := fakeLauncher(t, "windows", map[string]string{"claude": cli, "wt.exe": `C:\Users\dev\AppData\Local\Microsoft\WindowsApps\wt.exe`})
	if app, err := l.OpenAttach("4d4d4d4d"); err != nil || app != "Windows Terminal" {
		t.Fatalf("%q %v", app, err)
	}
	got := append([]string{(*calls)[0].name}, (*calls)[0].args...)
	if want := []string{`C:\Users\dev\AppData\Local\Microsoft\WindowsApps\wt.exe`, cli, "attach", "4d4d4d4d"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("started %q, want %q", got, want)
	}

	l, calls = fakeLauncher(t, "windows", map[string]string{"claude": cli})
	if app, err := l.OpenAttach("4d4d4d4d"); err != nil || app != "a terminal" {
		t.Fatalf("%q %v", app, err)
	}
	got = append([]string{(*calls)[0].name}, (*calls)[0].args...)
	if want := []string{"cmd.exe", "/c", "start", "", cli, "attach", "4d4d4d4d"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("started %q, want %q", got, want)
	}
}

// Nothing is started for an id that is not eight hex characters, for a
// CLI that is not there, or when the terminal itself would not start.
func TestOpenAttachRefuses(t *testing.T) {
	for _, bad := range []string{"", "4d4d4d4", "4d4d4d4d4", "4D4D4D4D", "4d4d4d4g", "4d4d;rm ", "../../x"} {
		l, calls := fakeLauncher(t, "linux", map[string]string{"claude": "/usr/bin/claude", "xterm": "/usr/bin/xterm"})
		if _, err := l.OpenAttach(bad); err == nil || len(*calls) != 0 {
			t.Errorf("%q: %v %+v", bad, err, *calls)
		}
	}

	l, calls := fakeLauncher(t, "darwin", map[string]string{})
	if _, err := l.OpenAttach("4d4d4d4d"); err == nil || len(*calls) != 0 {
		t.Fatalf("no CLI, nothing started: %v %+v", err, *calls)
	}
	if entries, _ := os.ReadDir(l.Dir); len(entries) != 0 {
		t.Fatalf("no CLI, no script: %v", entries)
	}

	l, _ = fakeLauncher(t, "linux", map[string]string{"claude": "/usr/bin/claude", "xterm": "/usr/bin/xterm"})
	l.Start = func(string, ...string) error { return errors.New("permission denied") }
	if _, err := l.OpenAttach("4d4d4d4d"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the reason it failed is kept: %v", err)
	}
}

// On Windows the path to the CLI reaches cmd.exe or Windows Terminal as
// part of a command line either reads for itself, so a path holding a
// character either of them treats as syntax is refused and nothing starts.
func TestOpenAttachOnWindowsRefusesACLIPathEitherShellWouldMisread(t *testing.T) {
	for _, cli := range []string{
		`C:\Users\R&D\claude.exe`, `C:\Users\a%PATH%b\claude.exe`, `C:\Users\a;b\claude.exe`,
		`C:\Users\a|b\claude.exe`, `C:\Users\a^b\claude.exe`, `C:\Users\a!b\claude.exe`,
		`C:\Users\a<b\claude.exe`, `C:\Users\a>b\claude.exe`, `C:\Users\a"b\claude.exe`,
	} {
		for _, withWT := range []bool{true, false} {
			onPath := map[string]string{"claude": cli}
			if withWT {
				onPath["wt.exe"] = `C:\Windows\wt.exe`
			}
			l, calls := fakeLauncher(t, "windows", onPath)
			if _, err := l.OpenAttach("4d4d4d4d"); err == nil || len(*calls) != 0 {
				t.Errorf("%s with Windows Terminal %v: %v %+v", cli, withWT, err, *calls)
			}
		}
	}
	l, calls := fakeLauncher(t, "windows", map[string]string{"claude": `C:\Program Files (x86)\claude\claude.exe`})
	if _, err := l.OpenAttach("4d4d4d4d"); err != nil || len(*calls) != 1 {
		t.Fatalf("spaces and brackets are quoted by the argument rules: %v %+v", err, *calls)
	}
}

// What the button offers is what OpenAttach opens: Terminal on macOS,
// Windows Terminal when it is there, a terminal anywhere else.
func TestTerminalNameIsWhatOpens(t *testing.T) {
	cases := []struct {
		goos   string
		onPath map[string]string
		want   string
	}{
		{"darwin", map[string]string{}, "Terminal"},
		{"windows", map[string]string{"wt.exe": `C:\Windows\wt.exe`}, "Windows Terminal"},
		{"windows", map[string]string{}, "a terminal"},
		{"linux", map[string]string{"xterm": "/usr/bin/xterm"}, "a terminal"},
		{"linux", map[string]string{}, "a terminal"},
	}
	for _, c := range cases {
		l, _ := fakeLauncher(t, c.goos, c.onPath)
		if got := l.Name(); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.goos, c.onPath, got, c.want)
		}
	}
}

// A CLI that PATH does not have is still found in the folder the
// installer puts it in, and the terminal is started on its full path. It
// runs as the host OS, so on Windows it is the Windows install folder and
// a console window through cmd.exe.
func TestOpenAttachFindsCLIOutsidePath(t *testing.T) {
	goos, home, name := "linux", "/home/dev", "claude"
	onPath := map[string]string{"xterm": "/usr/bin/xterm"}
	if runtime.GOOS == "windows" {
		goos, home, name = "windows", `C:\Users\dev`, "claude.exe"
		onPath = map[string]string{}
	}
	l, calls := fakeLauncher(t, goos, onPath)
	cli := filepath.Join(home, ".local", "bin", name)
	l.Getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	l.IsExecutable = func(p string) bool { return p == cli }
	if _, err := l.OpenAttach("4d4d4d4d"); err != nil {
		t.Fatal(err)
	}
	want := "-e " + cli + " attach 4d4d4d4d"
	if goos == "windows" {
		want = "/c start  " + cli + " attach 4d4d4d4d"
	}
	if len(*calls) != 1 || strings.Join((*calls)[0].args, " ") != want {
		t.Fatalf("%+v, want args %q", *calls, want)
	}

	l.IsExecutable = func(string) bool { return false }
	if _, err := l.OpenAttach("4d4d4d4d"); err == nil || err.Error() != "the Claude Code CLI was not found" {
		t.Fatalf("with no CLI anywhere: %v", err)
	}
}

// OpenTrust opens the system's own terminal in a folder with the Claude
// Code CLI running there, so the person can answer its question whether to
// trust the folder. On macOS that is a script for Terminal, named so the
// usual clean-up clears it too.
func TestOpenTrustOnMacOS(t *testing.T) {
	skipOnWindowsHost(t)
	l, calls := fakeLauncher(t, "darwin", map[string]string{"claude": "/Users/dev/my tools/claude"})
	app, err := l.OpenTrust("/Users/dev/my project")
	if err != nil || app != "Terminal" || len(*calls) != 1 {
		t.Fatalf("%q %v %+v", app, err, *calls)
	}
	script := (*calls)[0].args[2]
	if ok, _ := filepath.Match(scriptPattern, filepath.Base(script)); !ok {
		t.Fatalf("the script is not cleared like the others: %s", script)
	}
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\ncd '/Users/dev/my project' && exec '/Users/dev/my tools/claude'\n"; string(body) != want {
		t.Fatalf("script:\n%s\nwant:\n%s", body, want)
	}
}

// On Linux the first terminal found runs a shell that changes to the
// folder and starts the CLI there.
func TestOpenTrustOnLinux(t *testing.T) {
	skipOnWindowsHost(t)
	l, calls := fakeLauncher(t, "linux", map[string]string{"claude": "/home/dev/.local/bin/claude", "xterm": "/usr/bin/xterm"})
	if app, err := l.OpenTrust("/home/dev/my project"); err != nil || app != "a terminal" {
		t.Fatalf("%q %v", app, err)
	}
	got := append([]string{(*calls)[0].name}, (*calls)[0].args...)
	want := []string{"/usr/bin/xterm", "-e", "sh", "-c", "cd '/home/dev/my project' && exec /home/dev/.local/bin/claude"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("started %q, want %q", got, want)
	}
}

// On Windows the terminal starts in the folder, and a folder either shell
// would misread is refused.
func TestOpenTrustOnWindows(t *testing.T) {
	cli := `C:\Users\dev\AppData\Local\Programs\claude\claude.exe`
	l, calls := fakeLauncher(t, "windows", map[string]string{"claude": cli, "wt.exe": `C:\wt.exe`})
	if app, err := l.OpenTrust(`C:\work\shop`); err != nil || app != "Windows Terminal" {
		t.Fatalf("%q %v", app, err)
	}
	got := append([]string{(*calls)[0].name}, (*calls)[0].args...)
	if want := []string{`C:\wt.exe`, "-d", `C:\work\shop`, cli}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("started %q, want %q", got, want)
	}
	l, calls = fakeLauncher(t, "windows", map[string]string{"claude": cli})
	if _, err := l.OpenTrust(`C:\work\shop`); err != nil {
		t.Fatal(err)
	}
	got = append([]string{(*calls)[0].name}, (*calls)[0].args...)
	if want := []string{"cmd.exe", "/c", "start", "", "/D", `C:\work\shop`, cli}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("started %q, want %q", got, want)
	}
	if _, err := l.OpenTrust(`C:\work\a&b`); err == nil {
		t.Fatal("a folder cmd.exe would misread was handed over")
	}
}

// A folder that is not a full path is never handed to a terminal.
func TestOpenTrustRefusesARelativeFolder(t *testing.T) {
	l, calls := fakeLauncher(t, "linux", map[string]string{"claude": "/usr/bin/claude", "xterm": "/usr/bin/xterm"})
	if _, err := l.OpenTrust("project"); err == nil || len(*calls) != 0 {
		t.Fatalf("%v %+v", err, *calls)
	}
}
