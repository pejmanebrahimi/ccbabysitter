package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// autostartInstaller enables or disables launching this program at login
// on the platform this binary was built for. It is set by exactly one of
// autostart_darwin.go, autostart_windows.go or autostart_linux.go through
// an init function; a build for any other platform leaves it nil, which
// tells the rest of the program that autostart is not offered here.
var autostartInstaller func(enable bool) (path string, err error)

// autostartInstalled reports whether the login item autostartInstaller
// writes is there now. It only ever looks. It is set alongside
// autostartInstaller by the same init functions, and is nil wherever that
// one is.
var autostartInstalled func() (bool, error)

// pathPresent reports whether something is at path, without following a
// symlink there. Not finding it is an answer, and every other failure is
// an error.
func pathPresent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// resolvedExecutablePath returns the absolute path to the running binary
// with any symlink resolved, so an autostart entry always names the real
// file rather than a link that might later point somewhere else. It
// refuses a path that looks like a scratch build, since writing an
// autostart entry or a systemd unit that names one would point at a file
// that is already gone by the time anything tries to run it.
// executablePath is where this program is, for a unit or LaunchAgent to
// start. Tests replace it, since a test binary lives in a temporary folder
// that a unit must never name.
var executablePath = resolvedExecutablePath

func resolvedExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	if err := refuseTemporaryBinary(real, os.TempDir()); err != nil {
		return "", err
	}
	return real, nil
}

// refuseTemporaryBinary reports a plain error when path sits inside
// tempDir, or contains a path element starting with "go-build": both mark
// a binary that "go run" compiled into a scratch directory it deletes as
// soon as the command exits, rather than something built to stay put.
// tempDir is a parameter (rather than this calling os.TempDir() itself) so
// the check can be tested without depending on where the test binary
// itself happens to live.
func refuseTemporaryBinary(path, tempDir string) error {
	if tempDir != "" {
		// tempDir itself can be a symlink (/tmp on macOS points at
		// /private/tmp), while path has already had its own symlinks
		// resolved by the time this runs for real. Checking against both
		// forms means the comparison works whichever of the two, or
		// neither, has been resolved, which is what lets this be tested
		// with plain strings instead of real paths on disk.
		candidates := []string{tempDir}
		if real, err := filepath.EvalSymlinks(tempDir); err == nil && real != tempDir {
			candidates = append(candidates, real)
		}
		for _, dir := range candidates {
			if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
				return fmt.Errorf("%s is inside a temporary folder. Put the program somewhere it will stay, then switch this on", path)
			}
		}
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, "go-build") {
			return fmt.Errorf("%s looks like a temporary build made by go run. Build it with go build, then switch this on from the result", path)
		}
	}
	return nil
}

// launchAgentPlist is the macOS LaunchAgent that runs binPath as the
// background copy, with --service, which serves without opening a browser,
// so a start at login never pops the page. launchd starts it when the
// LaunchAgent is loaded (RunAtLoad), which is at login when the file is in
// ~/Library/LaunchAgents, and starts it again when it crashes, but not when
// it quits on purpose with exit code 0 (KeepAlive, SuccessfulExit false).
// AbandonProcessGroup keeps launchd from ending what the copy started, such
// as the processes Claude's background sessions run in, when the copy
// ends. ThrottleInterval lets launchd try again after two seconds rather
// than ten when a start loses the state folder to a copy still quitting.
// launchd gives a job a bare PATH, so the plist names one with the
// claude CLI's folder first when it is known, and XDG_DATA_HOME when the
// launcher has one, so the copy uses the same state folder.
func launchAgentPlist(binPath, cliPath, dataHome string) string {
	// Homebrew's folders on Apple Silicon, where a claude CLI installed
	// with npm finds its node, come before the system's.
	pathValue := "/opt/homebrew/bin:/opt/homebrew/sbin:" + servicePath
	if cliPath != "" {
		// The plist is only ever read by launchd, so the path is a macOS
		// one whatever this was built for, and path takes it apart.
		pathValue = path.Dir(cliPath) + ":" + pathValue
	}
	env := "\t\t<key>PATH</key>\n\t\t<string>" + xmlEscape(pathValue) + "</string>\n"
	if dataHome != "" {
		env += "\t\t<key>XDG_DATA_HOME</key>\n\t\t<string>" + xmlEscape(dataHome) + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.ccbabysitter</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(binPath) + `</string>
		<string>--service</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>2</integer>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>EnvironmentVariables</key>
	<dict>
` + env + `	</dict>
</dict>
</plist>
`
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// startupCmd is the two line script the Windows Startup folder runs at
// login. It refuses a path holding a double quote or a percent sign,
// since either could make the script do something other than start the
// named program: a quote would end the quoted argument early, and cmd.exe
// expands a percent-delimited name as an environment variable.
func startupCmd(binPath string) (string, error) {
	if strings.ContainsAny(binPath, `"%`) {
		return "", fmt.Errorf("the executable path cannot be quoted safely for a startup script: %q", binPath)
	}
	return "@echo off\nstart \"\" \"" + binPath + "\"\n", nil
}

// servicePath is the PATH the systemd user unit runs with, after the
// folder holding the claude CLI. A user service otherwise gets systemd's
// own default, which leaves out ~/.local/bin, where the official
// installer puts the CLI, so the CLI and anything it starts in turn could
// not find it.
const servicePath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// unitFile is the systemd user unit that starts binPath, used by the
// launcher, the install subcommand and the Linux start at login toggle. It
// passes --service, which serves in the foreground of the unit and never
// opens a browser. When cliPath, the claude CLI found when the unit is
// written, is known, the unit puts its folder first on PATH; without one
// there is no Environment line and the unit gets systemd's default.
//
// A desktop unit is wanted by, and ordered after, graphical-session.target,
// which systemd reaches once a graphical session is running and its
// display variables are in the user manager, so the copy it starts sees the
// desktop. A server unit is wanted by default.target and starts at boot,
// with lingering on.
//
// dataHome, when set, is the XDG_DATA_HOME the launcher sees, which a shell
// profile sets but the user manager does not have; the unit names it so the
// copy uses the same state folder as the launcher and the command line.
//
// A binPath holding a line break cannot be written into a unit at all, so
// it is an error naming the path.
//
// KillMode=process makes stopping or restarting the service end CC
// Babysitter's own process only: Claude's background sessions, and the
// daemon that hosts them, are started from the service and so live in its
// group, and they must outlive it, since nothing but a confirmed Stop may
// close a session.
func unitFile(binPath, cliPath string, desktop bool, dataHome string) (string, error) {
	exec, err := unitExecStart(binPath)
	if err != nil {
		return "", err
	}
	env := ""
	if cliPath != "" {
		// The unit is only ever read by systemd, so the path is a Linux one
		// whatever this was built for, and path rather than filepath takes
		// it apart.
		env = unitEnvironment("PATH="+path.Dir(cliPath)+":"+servicePath) + "\n"
	}
	if strings.ContainsAny(dataHome, "\n\r") {
		return "", fmt.Errorf("the data folder cannot be written into a service file: %q", dataHome)
	}
	if dataHome != "" {
		env += unitEnvironment("XDG_DATA_HOME="+dataHome) + "\n"
	}
	after, wantedBy := "After=network-online.target\n", "default.target"
	if desktop {
		after += "After=graphical-session.target\n"
		wantedBy = "graphical-session.target"
	}
	return `[Unit]
Description=CC Babysitter: keeps Claude Code sessions alive and remote-controlled
` + after + `
[Service]
` + env + `ExecStart=` + exec + ` --service
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=` + wantedBy + `
`, nil
}

// unitEnvironment renders an Environment= line for assignment. systemd
// expands a percent sign as a specifier, so each one is doubled, and an
// assignment holding a space, a quote of either kind or a backslash is put
// in double quotes, with the backslash and the double quote escaped, so it
// stays one assignment.
func unitEnvironment(assignment string) string {
	assignment = strings.ReplaceAll(assignment, "%", "%%")
	if strings.ContainsAny(assignment, " \t\"'\\") {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
		return `Environment="` + r.Replace(assignment) + `"`
	}
	return "Environment=" + assignment
}

// unitExecStart renders the program part of an ExecStart= line for
// binPath by the rules of unitEnvironment, with one more: systemd
// substitutes $VARIABLE in the words of an ExecStart= line, so a dollar
// sign is doubled as well as a percent sign. A path holding a space, a
// tab, a quote of either kind or a backslash is put in double quotes, with
// the backslash and the double quote escaped (a single quote is literal
// inside them). A line break cannot be
// carried in a unit line, so it is an error.
func unitExecStart(binPath string) (string, error) {
	if strings.ContainsAny(binPath, "\n\r") {
		return "", fmt.Errorf("the executable path cannot be written into a service file: %q", binPath)
	}
	binPath = strings.ReplaceAll(binPath, "%", "%%")
	binPath = strings.ReplaceAll(binPath, "$", "$$")
	if strings.ContainsAny(binPath, " \t\"'\\") {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
		return `"` + r.Replace(binPath) + `"`, nil
	}
	return binPath, nil
}
