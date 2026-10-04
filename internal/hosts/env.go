// Package hosts knows the apps a Claude Code session can live in: a
// terminal window, a background CLI process, the desktop app or a VS Code
// window. It reads what is installed and running on this machine, builds
// the exact claude command lines the rest of the program runs, and opens a
// page in the browser.
package hosts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

// Env is a snapshot of what this machine can offer a Claude Code session:
// whether the CLI, the desktop app and the VS Code extension are present,
// and whether the session is running somewhere with no terminal window or
// browser to fall back on.
type Env struct {
	Platform string `json:"platform"`
	Headless bool   `json:"headless"`
	// CLIFound says only that a program called claude was found, on PATH
	// or in one of the folders its installers use, and CLIPath is where.
	// CLIPresent is the stronger claim that it also answered when asked for
	// its version. The two are separate because a CLI that is installed but
	// slow to answer is not the same thing as one that is not installed:
	// refusing to start a session is right in the second case and wrong in
	// the first. CLIPath stays off the page.
	CLIFound   bool   `json:"cliFound"`
	CLIPath    string `json:"-"`
	CLIPresent bool   `json:"cliPresent"`
	CLIVersion string `json:"cliVersion"`
	// CLILoggedIn says whether the CLI reported an account logged in when
	// asked for its login status. It is unknown whenever that answer could
	// not be read, including when there is no CLI to ask: the page only
	// speaks up when the CLI itself said no.
	CLILoggedIn      LoginState `json:"cliLoggedIn"`
	DesktopInstalled bool       `json:"desktopInstalled"`
	DesktopRunning   bool       `json:"desktopRunning"`
	DesktopVersion   string     `json:"desktopVersion"`
	VSCodeInstalled  bool       `json:"vscodeInstalled"`
	VSCodeExtVersion string     `json:"vscodeExtVersion"`
}

// LoginState is what the CLI said about being logged in.
type LoginState string

// The three answers a login check can end with.
const (
	LoginYes     LoginState = "yes"
	LoginNo      LoginState = "no"
	LoginUnknown LoginState = "unknown"
)

// Probes supplies everything Detect needs from the operating system, so
// tests can script every answer without touching a real machine.
type Probes struct {
	Platform        string
	LookPath        func(string) (string, error)
	Getenv          func(string) string
	RegistryHandler func(scheme string) (string, bool)
	FileExists      func(string) bool
	ReadFile        func(string) ([]byte, error)
	ListDir         func(string) []string
	Home            string
	// DesktopProcess reports whether the desktop app's own process is
	// currently running on this machine. A nil func counts as "no".
	DesktopProcess func() bool
	// IsExecutable reports whether a file can be run. It is how the CLI is
	// found in its install folders when PATH does not have it; a nil func
	// means only PATH is asked.
	IsExecutable func(string) bool
}

// cliLocator finds the claude CLI through these probes.
func (p Probes) cliLocator() claude.Locator {
	return claude.Locator{OS: p.Platform, LookPath: p.LookPath, Getenv: p.Getenv, IsExecutable: p.IsExecutable}
}

func (p Probes) desktopProcessRunning() bool {
	if p.DesktopProcess == nil {
		return false
	}
	return p.DesktopProcess()
}

var (
	reVersion        = regexp.MustCompile(`(\d+\.\d+\.\d+)`)
	reDesktopVersion = regexp.MustCompile(`Claude_(\d+\.\d+\.\d+)`)
	rePlistVersion   = regexp.MustCompile(`CFBundleShortVersionString</key>\s*<string>([^<]+)`)
	reExtVersion     = regexp.MustCompile(`^anthropic\.claude-code-(\d+(?:\.\d+)*)`)
)

// Detect reads the current machine's environment. snapshotHasDesktop tells
// it whether the latest session snapshot already showed a session running
// with a desktop entrypoint, which counts as the desktop app being open
// even when its own process could not be found.
func Detect(ctx context.Context, p Probes, runner claude.Runner, snapshotHasDesktop bool) Env {
	return detect(ctx, p, runner, snapshotHasDesktop, true)
}

// DetectWithoutLogin is Detect without asking the CLI whether an account
// is logged in, which leaves CLILoggedIn unknown. That question can take
// as long as the version one again, so a program that has to answer
// quickly, such as one just starting its page, asks it later.
func DetectWithoutLogin(ctx context.Context, p Probes, runner claude.Runner, snapshotHasDesktop bool) Env {
	return detect(ctx, p, runner, snapshotHasDesktop, false)
}

func detect(ctx context.Context, p Probes, runner claude.Runner, snapshotHasDesktop, askLogin bool) Env {
	env := Env{Platform: p.Platform, CLILoggedIn: LoginUnknown}

	if cli, ok := p.cliLocator().Find(); ok {
		env.CLIFound, env.CLIPath = true, cli
		cctx, cancel := context.WithTimeout(ctx, cliAnswerTimeout)
		out, runErr := runner.Run(cctx, "", "--version")
		cancel()
		if m := reVersion.FindStringSubmatch(out); runErr == nil && m != nil {
			env.CLIPresent, env.CLIVersion = true, m[1]
		}
		// A CLI that did not answer for its version is not asked again:
		// it would only hold detection up for as long a second time.
		if env.CLIPresent && askLogin {
			env.CLILoggedIn = loginState(ctx, runner)
		}
	}

	switch p.Platform {
	case "windows":
		if handler, ok := p.RegistryHandler("claude"); ok {
			env.DesktopInstalled = true
			env.DesktopVersion = "installed"
			if m := reDesktopVersion.FindStringSubmatch(handler); m != nil {
				env.DesktopVersion = m[1]
			}
		}
	case "darwin":
		if p.FileExists("/Applications/Claude.app") {
			env.DesktopInstalled, env.DesktopVersion = true, "installed"
			if b, err := p.ReadFile("/Applications/Claude.app/Contents/Info.plist"); err == nil {
				if m := rePlistVersion.FindSubmatch(b); m != nil {
					env.DesktopVersion = string(m[1])
				}
			}
		}
	default:
		// Linux has no install location to stat: the desktop scheme
		// handler registered with xdg-mime is the only signal.
		if _, ok := p.RegistryHandler("claude"); ok {
			env.DesktopInstalled, env.DesktopVersion = true, "installed"
		}
	}
	env.DesktopRunning = env.DesktopInstalled && (snapshotHasDesktop || p.desktopProcessRunning())

	if _, err := p.LookPath("code"); err == nil || p.FileExists(vscodeDefaultDir(p)) {
		env.VSCodeInstalled = true
		env.VSCodeExtVersion = vscodeExtVersion(p)
	}

	env.Headless = isHeadless(p)
	return env
}

// cliAnswerTimeout bounds each question env detection puts to the CLI.
const cliAnswerTimeout = 20 * time.Second

// loginState asks the CLI whether an account is logged in. The answer is a
// JSON object with a loggedIn field; a command that fails, prints no such
// object or leaves the field out gives an unknown answer rather than a
// guess. Anything printed ahead of the object, such as a warning, is
// skipped.
func loginState(ctx context.Context, runner claude.Runner) LoginState {
	cctx, cancel := context.WithTimeout(ctx, cliAnswerTimeout)
	out, err := runner.Run(cctx, "", "auth", "status")
	cancel()
	if err != nil {
		return LoginUnknown
	}
	start := strings.IndexByte(out, '{')
	if start < 0 {
		return LoginUnknown
	}
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&status); err != nil || status.LoggedIn == nil {
		return LoginUnknown
	}
	if *status.LoggedIn {
		return LoginYes
	}
	return LoginNo
}

// isHeadless reports whether this session has no way to open a terminal
// window or a browser: an SSH session on any platform, or a Linux session
// with neither an X nor a Wayland display and no browser opener on PATH.
func isHeadless(p Probes) bool {
	if p.Getenv("SSH_CONNECTION") != "" || p.Getenv("SSH_TTY") != "" {
		return true
	}
	if p.Platform == "linux" {
		if p.Getenv("DISPLAY") == "" && p.Getenv("WAYLAND_DISPLAY") == "" {
			return true
		}
		if _, err := p.LookPath("xdg-open"); err != nil {
			return true
		}
	}
	return false
}

// Headless reports whether the machine this runs on is headless right now,
// by the same rule Detect puts in Env.Headless, read from the real
// environment. It is cheap: it only reads environment variables and, on
// Linux, looks for xdg-open on PATH.
func Headless() bool {
	return isHeadless(RealProbes(nil))
}

func vscodeDefaultDir(p Probes) string {
	switch p.Platform {
	case "windows":
		return p.Getenv("LOCALAPPDATA") + `\Programs\Microsoft VS Code\Code.exe`
	case "darwin":
		return "/Applications/Visual Studio Code.app"
	default:
		return "/usr/share/code/code"
	}
}

// vscodeExtVersion picks the newest installed claude-code extension folder.
// Folder names can carry any number of version components and several
// versions can be installed side by side, so each component is compared as
// a number rather than as text: otherwise "2.1.100" would sort behind
// "2.1.99".
func vscodeExtVersion(p Probes) string {
	if p.ListDir == nil {
		return ""
	}
	dir := filepath.Join(p.Home, ".vscode", "extensions")
	best := ""
	for _, name := range p.ListDir(dir) {
		m := reExtVersion.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if best == "" || versionLess(best, m[1]) {
			best = m[1]
		}
	}
	return best
}

// versionLess reports whether a names an earlier version than b, comparing
// each dot separated component numerically.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var an, bn int
		if i < len(as) {
			an, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bn, _ = strconv.Atoi(bs[i])
		}
		if an != bn {
			return an < bn
		}
	}
	return false
}

// RealProbes wires Probes to the real operating system. desktopProcess
// reports whether any running process has an executable path containing a
// given substring; the caller wires this to its own process table lookup
// so this package never has to know how processes are enumerated.
func RealProbes(desktopProcess func(substr string) bool) Probes {
	platform := runtime.GOOS
	home, _ := os.UserHomeDir()

	// The marker is the fragment of an executable path that identifies
	// the desktop app's own process on each platform. Linux has no
	// reliable marker, so its desktop process is never reported running
	// this way; a snapshot that already saw a desktop session is the
	// only signal there.
	marker := ""
	switch platform {
	case "windows":
		marker = "_pzs8sxrjxfjjc"
	case "darwin":
		marker = "/Claude.app/Contents/MacOS/Claude"
	}

	return Probes{
		Platform: platform,
		Home:     home,
		LookPath: exec.LookPath,
		Getenv:   os.Getenv,
		RegistryHandler: func(scheme string) (string, bool) {
			return realRegistryHandler(platform, scheme)
		},
		FileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		ReadFile:     os.ReadFile,
		IsExecutable: claude.IsExecutable,
		ListDir: func(dir string) []string {
			entries, err := os.ReadDir(dir)
			if err != nil {
				return nil
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			return names
		},
		DesktopProcess: func() bool {
			if marker == "" || desktopProcess == nil {
				return false
			}
			return desktopProcess(marker)
		},
	}
}

var reRegSZ = regexp.MustCompile(`REG_SZ\s+(.+)`)

// realRegistryHandler answers the claude URL scheme handler question for
// the platform it runs on: a registry read on Windows, an xdg-mime query
// on Linux, and nothing on macOS, where the handler is found by checking
// for the app bundle instead. Every external command it runs is bounded
// to five seconds so a hung helper can never stall detection.
func realRegistryHandler(platform, scheme string) (string, bool) {
	switch platform {
	case "windows":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "reg", "query",
			`HKCU\Software\Classes\`+scheme+`\shell\open\command`, "/ve")
		noWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return "", false
		}
		m := reRegSZ.FindStringSubmatch(string(out))
		if m == nil {
			return "", false
		}
		return strings.TrimSpace(m[1]), true
	case "linux":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "xdg-mime", "query", "default",
			"x-scheme-handler/"+scheme).Output()
		if err != nil {
			return "", false
		}
		handler := strings.TrimSpace(string(out))
		if handler == "" {
			return "", false
		}
		return handler, true
	default:
		return "", false
	}
}
