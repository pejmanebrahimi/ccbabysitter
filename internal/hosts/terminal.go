package hosts

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

// scriptPattern names the scripts OpenAttach leaves for Terminal on macOS,
// and scriptMaxAge is how long one is kept: Terminal reads it as it opens,
// so anything older than this has done its job.
const (
	scriptPattern = "attach-*.command"
	scriptMaxAge  = time.Minute
)

// windowsUnsafe are the characters cmd.exe reads as syntax, or expands,
// even inside quotes, together with the one Windows Terminal splits its
// command line on. A CLI path holding any of them is never handed over.
const windowsUnsafe = `&|<>^%!";`

// linuxTerminals are the terminals tried on Linux, in order, each with the
// flag after which it takes the command to run.
var linuxTerminals = []struct{ name, flag string }{
	{"x-terminal-emulator", "-e"},
	{"gnome-terminal", "--"},
	{"konsole", "-e"},
	{"xterm", "-e"},
}

// TerminalLauncher opens the operating system's own terminal on
// `claude attach` for a background session. Closing that window leaves the
// session running, since attaching never owns it. Every way it reaches the
// system is a field, so a test can stand in for all of them and nothing is
// ever started.
type TerminalLauncher struct {
	// OS is the operating system to open a terminal on.
	OS string
	// Dir is this program's own folder, where the small script Terminal
	// runs on macOS is written.
	Dir string
	// LookPath finds a program on PATH, and Start starts one without
	// waiting for it.
	LookPath func(string) (string, error)
	Start    func(name string, args ...string) error
	Now      func() time.Time
	// Getenv and IsExecutable let the CLI be found in its install folders
	// when PATH does not have it. Either left nil means only PATH is asked.
	Getenv       func(string) string
	IsExecutable func(string) bool
}

// NewTerminalLauncher returns a launcher for this machine that writes its
// scripts under dir.
func NewTerminalLauncher(dir string) *TerminalLauncher {
	return &TerminalLauncher{
		OS:       runtime.GOOS,
		Dir:      dir,
		LookPath: exec.LookPath,
		Start: func(name string, args ...string) error {
			return startDetached(exec.Command(name, args...))
		},
		Now:          time.Now,
		Getenv:       os.Getenv,
		IsExecutable: claude.IsExecutable,
	}
}

// Name says what OpenAttach opens here: Terminal on macOS, Windows
// Terminal on Windows when it is installed, and a terminal anywhere else.
func (l *TerminalLauncher) Name() string {
	switch l.OS {
	case "darwin":
		return "Terminal"
	case "windows":
		if _, err := l.LookPath("wt.exe"); err == nil {
			return "Windows Terminal"
		}
	}
	return "a terminal"
}

// OpenAttach opens a terminal running `claude attach short` and names what
// it opened, the same way Name does. The short id must be exactly eight
// lowercase hex characters, and the CLI is run by its full path, found the
// same way as the one this program runs. Each part of the command is
// handed over as an argument of its own, so no Unix shell reads it. On
// macOS the script that Terminal runs quotes the path, and on Windows,
// where cmd.exe and Windows Terminal read the command line for
// themselves, a path either would misread is refused.
func (l *TerminalLauncher) OpenAttach(short string) (string, error) {
	if !isAttachID(short) {
		return "", errors.New("that is not a background session id")
	}
	cli, ok := claude.Locator{OS: l.OS, LookPath: l.LookPath, Getenv: l.Getenv, IsExecutable: l.IsExecutable}.Find()
	if !ok {
		return "", errors.New("the Claude Code CLI was not found")
	}
	switch l.OS {
	case "darwin":
		return "Terminal", l.openMacTerminal(cli, short)
	case "windows":
		if strings.ContainsAny(cli, windowsUnsafe) {
			return "", errors.New("the path to the Claude Code CLI has characters a Windows command line would misread")
		}
		if wt, err := l.LookPath("wt.exe"); err == nil {
			return "Windows Terminal", l.Start(wt, cli, "attach", short)
		}
		return "a terminal", l.Start("cmd.exe", "/c", "start", "", cli, "attach", short)
	default:
		for _, term := range linuxTerminals {
			if path, err := l.LookPath(term.name); err == nil {
				return "a terminal", l.Start(path, term.flag, cli, "attach", short)
			}
		}
		return "", errors.New("no terminal program was found on PATH")
	}
}

// OpenTrust opens the operating system's own terminal in dir with the
// Claude Code CLI running there, so the person can answer its question
// whether to trust the folder, and returns the name of what it opened. dir
// must be a full path. On Windows, where cmd.exe and Windows Terminal read
// the command line for themselves, a folder or CLI path either would
// misread is refused.
func (l *TerminalLauncher) OpenTrust(dir string) (string, error) {
	if !absFor(l.OS, dir) {
		return "", errors.New("that is not a full path to a folder")
	}
	cli, ok := claude.Locator{OS: l.OS, LookPath: l.LookPath, Getenv: l.Getenv, IsExecutable: l.IsExecutable}.Find()
	if !ok {
		return "", errors.New("the Claude Code CLI was not found")
	}
	run := "cd " + ShellQuote(dir) + " && exec " + ShellQuote(cli)
	switch l.OS {
	case "darwin":
		return "Terminal", l.openMacScript("attach-trust-*.command", run)
	case "windows":
		if strings.ContainsAny(cli, windowsUnsafe) || strings.ContainsAny(dir, windowsUnsafe) {
			return "", errors.New("the folder or the path to the Claude Code CLI has characters a Windows command line would misread")
		}
		if wt, err := l.LookPath("wt.exe"); err == nil {
			return "Windows Terminal", l.Start(wt, "-d", dir, cli)
		}
		return "a terminal", l.Start("cmd.exe", "/c", "start", "", "/D", dir, cli)
	default:
		for _, term := range linuxTerminals {
			if path, err := l.LookPath(term.name); err == nil {
				return "a terminal", l.Start(path, term.flag, "sh", "-c", run)
			}
		}
		return "", errors.New("no terminal program was found on PATH")
	}
}

// absFor reports whether dir is a full path on the system goos, which is
// not always the one this program runs on in a test.
func absFor(goos, dir string) bool {
	if goos == "windows" {
		return len(dir) >= 3 && ((dir[0] >= 'A' && dir[0] <= 'Z') || (dir[0] >= 'a' && dir[0] <= 'z')) && dir[1] == ':' && (dir[2] == '\\' || dir[2] == '/')
	}
	return strings.HasPrefix(dir, "/")
}

// openMacScript writes run into an executable script in this program's
// own folder, named by pattern, and asks Terminal to open it. Scripts left
// by earlier calls are cleared away first once they are over a minute old.
func (l *TerminalLauncher) openMacScript(pattern, run string) error {
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	l.clearOldScripts()
	f, err := os.CreateTemp(l.Dir, pattern)
	if err != nil {
		return err
	}
	path := f.Name()
	_, werr := f.WriteString("#!/bin/sh\n" + run + "\n")
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(path, 0o700)); err != nil {
		_ = os.Remove(path)
		return err
	}
	return l.Start("open", "-a", "Terminal", path)
}

// openMacTerminal writes the attach command into an executable script in
// this program's own folder and asks Terminal to open it. Scripts left by
// earlier calls are cleared away first once they are over a minute old.
func (l *TerminalLauncher) openMacTerminal(cli, short string) error {
	return l.openMacScript("attach-"+short+"-*.command", "exec "+ShellQuote(cli)+" attach "+short)
}

// clearOldScripts removes the scripts in Dir that are older than a minute.
// A script that cannot be removed is left for the next call.
func (l *TerminalLauncher) clearOldScripts() {
	matches, _ := filepath.Glob(filepath.Join(l.Dir, scriptPattern))
	cutoff := l.Now().Add(-scriptMaxAge)
	for _, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.ModTime().Before(cutoff) {
			_ = os.Remove(p)
		}
	}
}

// isAttachID reports whether s is exactly eight lowercase hex characters,
// the only shape a short id may have before it is put in a command.
func isAttachID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
