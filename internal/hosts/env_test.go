package hosts

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

func probes(platform string) Probes {
	return Probes{
		Platform: platform,
		LookPath: func(n string) (string, error) {
			if n == "claude" || n == "code" {
				return "/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		Getenv: func(string) string { return "" },
		RegistryHandler: func(scheme string) (string, bool) {
			if scheme == "claude" {
				return `"C:\Program Files\WindowsApps\Claude_2.2553.1.0_x64__pzs8sxrjxfjjc\app\Claude.exe" "%1"`, true
			}
			return "", false
		},
		FileExists:     func(string) bool { return false },
		ReadFile:       func(string) ([]byte, error) { return nil, errors.New("not found") },
		DesktopProcess: func() bool { return false },
	}
}

func TestDetectWindows(t *testing.T) {
	r := claude.NewFakeRunner(func(args []string) (string, error) { return "2.1.275 (Claude Code)\n", nil })
	env := Detect(context.Background(), probes("windows"), r, true)
	if !env.CLIPresent || !env.CLIFound || env.CLIVersion != "2.1.275" {
		t.Fatalf("%+v", env)
	}
	if !env.DesktopInstalled || env.DesktopVersion != "2.2553.1" || !env.DesktopRunning {
		t.Fatalf("%+v", env)
	}
	if !env.VSCodeInstalled || env.Headless {
		t.Fatalf("%+v", env)
	}
}

func TestDetectHeadlessLinux(t *testing.T) {
	p := probes("linux")
	p.RegistryHandler = func(string) (string, bool) { return "", false }
	p.LookPath = func(n string) (string, error) {
		if n == "claude" {
			return "/usr/bin/claude", nil
		}
		return "", errors.New("not found")
	}
	r := claude.NewFakeRunner(func([]string) (string, error) { return "2.1.275", nil })
	env := Detect(context.Background(), p, r, false)
	if !env.Headless || env.DesktopInstalled || env.VSCodeInstalled {
		t.Fatalf("%+v", env)
	}
}

func TestDetectSSHIsHeadless(t *testing.T) {
	p := probes("windows")
	p.Getenv = func(k string) string {
		if k == "SSH_CONNECTION" {
			return "1.2.3.4 5 6.7.8.9 22"
		}
		return ""
	}
	r := claude.NewFakeRunner(func([]string) (string, error) { return "2.1.275", nil })
	if env := Detect(context.Background(), p, r, false); !env.Headless {
		t.Fatalf("%+v", env)
	}
}

// A CLI that is installed but slow to answer is not the same thing as one
// that is not installed at all, and the difference decides whether the
// page refuses to start a session. CLIFound answers the only
// question that matters for that: is the binary there.
func TestDetectCLIMissingOrSilent(t *testing.T) {
	p := probes("windows")
	p.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	r := claude.NewFakeRunner(nil)
	env := Detect(context.Background(), p, r, false)
	if env.CLIPresent || env.CLIFound {
		t.Fatalf("missing: %+v", env)
	}

	p2 := probes("windows")
	r2 := claude.NewFakeRunner(func([]string) (string, error) { return "", errors.New("boom") })
	env2 := Detect(context.Background(), p2, r2, false)
	if env2.CLIPresent {
		t.Fatalf("silent: %+v", env2)
	}
	if !env2.CLIFound {
		t.Fatalf("a binary that is on PATH but did not answer is still on PATH: %+v", env2)
	}
}

// TestDetectDesktopRunningNeedsProcessOrSnapshot checks that having a CLI
// binary named claude on the machine is never mistaken for the desktop app
// being open: only its own process or a snapshot that already saw a
// desktop session counts.
func TestDetectDesktopRunningNeedsProcessOrSnapshot(t *testing.T) {
	p := probes("windows")
	p.DesktopProcess = func() bool { return false }
	r := claude.NewFakeRunner(func([]string) (string, error) { return "2.1.275", nil })
	env := Detect(context.Background(), p, r, false)
	if !env.CLIPresent {
		t.Fatalf("expected CLI present: %+v", env)
	}
	if env.DesktopRunning {
		t.Fatalf("expected DesktopRunning false: %+v", env)
	}
}

func TestDetectDesktopRunningNilProcessProbe(t *testing.T) {
	p := probes("windows")
	p.DesktopProcess = nil
	r := claude.NewFakeRunner(func([]string) (string, error) { return "2.1.275", nil })
	if env := Detect(context.Background(), p, r, false); env.DesktopRunning {
		t.Fatalf("nil probe should count as false: %+v", env)
	}
}

func TestVSCodeExtVersionPicksHighestNumerically(t *testing.T) {
	p := probes("darwin")
	p.Home = "/home/dev"
	want := filepath.Join("/home/dev", ".vscode", "extensions")
	p.ListDir = func(dir string) []string {
		if dir != want {
			t.Fatalf("unexpected dir: %s", dir)
		}
		return []string{
			"anthropic.claude-code-2.1.9-darwin-arm64",
			"anthropic.claude-code-2.1.100-darwin-arm64",
			"anthropic.claude-code-2.1.99-darwin-arm64",
			"unrelated-extension-9.9.9",
		}
	}
	if got := vscodeExtVersion(p); got != "2.1.100" {
		t.Fatalf("got %q", got)
	}
}

// A service manager starts this program with a PATH that leaves out the
// folder the installer puts the CLI in. It is still found there, and its
// full path is kept for the commands that name it. It runs as the host OS,
// whose path rules the CLI is found by.
func TestDetectFindsCLIOutsidePath(t *testing.T) {
	home, name := "/home/dev", "claude"
	if runtime.GOOS == "windows" {
		home, name = `C:\Users\dev`, "claude.exe"
	}
	p := probes(runtime.GOOS)
	p.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	p.Getenv = func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}
	want := filepath.Join(home, ".local", "bin", name)
	p.IsExecutable = func(path string) bool { return path == want }
	r := claude.NewFakeRunner(func([]string) (string, error) { return "2.1.275", nil })
	env := Detect(context.Background(), p, r, false)
	if !env.CLIFound || !env.CLIPresent || env.CLIPath != want {
		t.Fatalf("%+v", env)
	}
}

// Headless reads the real environment by the same rule, so an ssh
// session counts as headless on every platform.
func TestHeadlessReadsTheRealEnvironment(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "198.51.100.4 50000 203.0.113.7 22")
	if !Headless() {
		t.Fatal("an ssh session is headless")
	}
}

// The CLI is asked whether an account is logged in, and only a clear
// answer counts: output that is not the expected object, or an object
// without the field, leave the state unknown. The object is the answer
// even when the command exits with an error, as the CLI does when nobody
// is logged in.
func TestDetectLoginState(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
		want LoginState
	}{
		{"logged in", `{"loggedIn": true, "authMethod": "claude.ai"}`, nil, LoginYes},
		{"not logged in", `{"loggedIn": false, "authMethod": "none", "apiProvider": "firstParty"}`, nil, LoginNo},
		{"warning first", "a warning line\n{\"loggedIn\": false}\n", nil, LoginNo},
		// The CLI exits with 1 when nobody is logged in, and still prints
		// the object, which is the answer.
		{"logged out exits with 1", `{"loggedIn": false, "authMethod": "none"}`, errors.New("exit status 1"), LoginNo},
		{"command failed with no answer", "", errors.New("signal: killed"), LoginUnknown},
		{"not json", "Not logged in", nil, LoginUnknown},
		{"field missing", `{"authMethod": "none"}`, nil, LoginUnknown},
		{"field not a bool", `{"loggedIn": "no"}`, nil, LoginUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := claude.NewFakeRunner(func(args []string) (string, error) {
				if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
					return tc.out, tc.err
				}
				return "2.1.275 (Claude Code)\n", nil
			})
			env := Detect(context.Background(), probes("linux"), r, false)
			if env.CLILoggedIn != tc.want {
				t.Fatalf("got %q, want %q", env.CLILoggedIn, tc.want)
			}
		})
	}
}

// With no CLI, or one that does not answer at all, there is nobody to ask
// and the login state is unknown, never "not logged in".
func TestDetectLoginStateUnknownWithoutAnAnsweringCLI(t *testing.T) {
	p := probes("linux")
	p.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	r := claude.NewFakeRunner(func([]string) (string, error) { return `{"loggedIn": false}`, nil })
	if env := Detect(context.Background(), p, r, false); env.CLILoggedIn != LoginUnknown || len(r.CallList()) != 0 {
		t.Fatalf("missing CLI: %+v, calls %v", env, r.CallList())
	}

	silent := claude.NewFakeRunner(func([]string) (string, error) { return "", errors.New("boom") })
	env := Detect(context.Background(), probes("linux"), silent, false)
	if env.CLILoggedIn != LoginUnknown {
		t.Fatalf("silent CLI: %+v", env)
	}
	if calls := silent.CallList(); len(calls) != 1 || calls[0] != "--version" {
		t.Fatalf("a CLI that did not answer for its version is not asked again: %v", calls)
	}
}

// Detecting without the login question asks the CLI for its version only
// and leaves the login state unknown, however the CLI would have answered.
func TestDetectWithoutLoginNeverAsksForTheLogin(t *testing.T) {
	r := claude.NewFakeRunner(func(args []string) (string, error) {
		if len(args) == 2 && args[0] == "auth" && args[1] == "status" {
			return `{"loggedIn": true}`, nil
		}
		return "2.1.275 (Claude Code)\n", nil
	})
	env := DetectWithoutLogin(context.Background(), probes("linux"), r, false)
	if env.CLILoggedIn != LoginUnknown || !env.CLIPresent || env.CLIVersion != "2.1.275" {
		t.Fatalf("%+v", env)
	}
	if calls := r.CallList(); len(calls) != 1 || calls[0] != "--version" {
		t.Fatalf("only the version is asked: %v", calls)
	}
}

// DesktopNow reads only the desktop app's state: whether its process can be
// checked on this system, whether it runs, and the installed version. It
// never runs the CLI.
func TestDesktopNow(t *testing.T) {
	mac := probes("darwin")
	mac.FileExists = func(p string) bool { return p == "/Applications/Claude.app" }
	mac.ReadFile = func(string) ([]byte, error) {
		return []byte("<key>CFBundleShortVersionString</key>\n<string>2.26454.2</string>"), nil
	}
	mac.DesktopProcess = func() bool { return true }
	if known, running, version := DesktopNow(mac); !known || !running || version != "2.26454.2" {
		t.Fatalf("mac, running: %v %v %q", known, running, version)
	}
	mac.DesktopProcess = func() bool { return false }
	if known, running, version := DesktopNow(mac); !known || running || version != "2.26454.2" {
		t.Fatalf("mac, closed: %v %v %q", known, running, version)
	}
	win := probes("windows")
	win.DesktopProcess = func() bool { return true }
	if known, running, version := DesktopNow(win); !known || !running || version != "2.2553.1" {
		t.Fatalf("windows: %v %v %q", known, running, version)
	}
	linux := probes("linux")
	if known, _, version := DesktopNow(linux); known || version != "installed" {
		t.Fatalf("linux cannot check the process: %v %q", known, version)
	}
	none := probes("darwin")
	if known, running, version := DesktopNow(none); known || running || version != "" {
		t.Fatalf("no desktop app: %v %v %q", known, running, version)
	}
}
