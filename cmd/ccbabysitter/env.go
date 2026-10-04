package main

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"strconv"
	"strings"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// currentUser reports the account name to show in the commands printed
// for reaching this machine, falling back to the $USER environment
// variable and then to "user" when the lookup fails.
func currentUser() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		name = "user"
	}
	return withoutDomain(name)
}

// withoutDomain drops the machine or domain part Windows puts in front of
// an account name ("HOST\alice"), which ssh does not take as part of a
// user name.
func withoutDomain(name string) string {
	if i := strings.LastIndex(name, `\`); i >= 0 && i < len(name)-1 {
		return name[i+1:]
	}
	return name
}

// hostName is this machine's own name, or "localhost" when it cannot be
// read.
func hostName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return host
}

// sshServerAddress returns this machine's side of an SSH_CONNECTION value,
// which reads "client_ip client_port server_ip server_port": the address
// the person connected to, and so one that is known to reach this machine
// from where they are. It is empty when conn is not in that form or the
// address could not stand as one word in a command line.
func sshServerAddress(conn string) string {
	f := strings.Fields(conn)
	if len(f) != 4 || !state.ValidServerAddress(f[2]) {
		return ""
	}
	return f[2]
}

// serverAddress is the address a person on another computer should use to
// reach this one: the address of the ssh connection this program was
// started from, else the one saved in stateDir the last time it was
// started over ssh, else hostname. An address seen now is saved for a
// later run with no ssh connection of its own, such as the systemd
// service. Saving is best effort, since failing to remember it only means
// a later run falls back to the host name.
func serverAddress(stateDir, sshConnection, hostname string) string {
	if addr := sshServerAddress(sshConnection); addr != "" {
		_ = state.SaveServerAddress(stateDir, addr)
		return addr
	}
	if addr := state.ServerAddress(stateDir); addr != "" {
		return addr
	}
	return hostname
}

// currentUserAndAddress reports the account name and the address to put
// in the commands printed for reaching this machine from another one,
// remembering the address in stateDir as serverAddress does.
func currentUserAndAddress(stateDir string) (string, string) {
	return currentUser(), serverAddress(stateDir, os.Getenv("SSH_CONNECTION"), hostName())
}

// tunnelHint is the command line that forwards the page's port from
// another machine over SSH. It leaves out -N, so the same window is also
// an ordinary shell on this machine for as long as it stays open. The
// target is quoted for the shell the person pastes the line into when it
// holds anything that shell would treat specially.
func tunnelHint(port int, user, address string) string {
	return fmt.Sprintf("ssh -L %d:127.0.0.1:%d %s", port, port, hosts.ShellQuote(hosts.SSHTarget(user, address)))
}

// portOf pulls the port back out of a URL of the form
// http://127.0.0.1:<port>, the only shape this program ever builds one in.
func portOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Port()
}

// banner renders the lines CC Babysitter prints to the console once the
// page is ready to use. A headless machine gets three extra lines saying
// its page is not reachable directly, with the command to connect from
// another computer that also forwards the page's port, and the URL to open
// there once it is connected. The keep-awake line states the one rule
// there is rather than a setting, since there is nothing to change. A
// version left empty is left out of the versions line rather than printed
// blank, and a missing CLI is called out by name instead of silently
// vanishing from the line. closing is the last line: how to quit a copy in
// this terminal, or how a copy in the background keeps running.
func banner(version, pageURL string, headless bool, user, address, cliVersion, desktopVersion, vscodeExtVersion, closing string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s %s\n", buildinfo.Name, version)
	fmt.Fprintf(&b, "Page: %s\n", pageURL)
	if headless {
		if n, err := strconv.Atoi(portOf(pageURL)); err == nil {
			fmt.Fprintf(&b, "To view and control this server's %s from a computer with a browser, connect from that computer with:\n", buildinfo.Name)
			fmt.Fprintf(&b, "  %s\n", tunnelHint(n, user, address))
			fmt.Fprintf(&b, "then open %s in its browser while that connection is open.\n", pageURL)
		}
	}

	fmt.Fprint(&b, "Keep computer awake: while at least one session is babysat, the computer does not idle-sleep\n")

	var parts []string
	if cliVersion != "" {
		parts = append(parts, "Claude Code CLI "+cliVersion)
	} else {
		parts = append(parts, "Claude Code CLI not found")
	}
	if desktopVersion != "" {
		parts = append(parts, "Desktop "+desktopVersion)
	}
	if vscodeExtVersion != "" {
		parts = append(parts, "VS Code extension "+vscodeExtVersion)
	}
	fmt.Fprintf(&b, "%s\n", strings.Join(parts, " "+string(rune(0x00b7))+" "))

	if olderThanVerified(cliVersion) {
		fmt.Fprintf(&b, "This build was verified with Claude Code %s or later. Older versions may word their output differently.\n", buildinfo.VerifiedCLIVersion)
	}

	fmt.Fprintln(&b, closing)
	return b.String()
}

// foregroundClosing ends the banner of a copy that serves in this terminal.
const foregroundClosing = "Press Ctrl+C to quit. Babysat sessions keep running, but nothing restarts them and the computer may sleep."

// serviceClosing ends the banner the background copy itself writes, to the
// journal or nowhere, since it has no terminal to quit it in.
const serviceClosing = "This is the background copy. Quit it with: ccbabysitter quit"

// backgroundClosing ends what the launcher prints: the copy runs in the
// background, whether it starts again by itself, and that the window can
// be closed.
func backgroundClosing(server, loginStart bool) string {
	switch {
	case loginStart && server:
		return "CC Babysitter runs in the background and starts again when this server boots. You can close this window."
	case loginStart:
		return "CC Babysitter runs in the background and starts again when you log in. You can close this window."
	case server:
		return "CC Babysitter runs in the background until you quit it or restart. Start at boot is off. You can close this window."
	}
	return "CC Babysitter runs in the background until you quit it or restart. Start at login is off. You can close this window."
}

// crashRestartReason is the first Activity line of a background copy that
// launchd or systemd started again because the one before it ended without
// cleaning up, such as after a crash.
const crashRestartReason = "Started again by the system after CC Babysitter stopped unexpectedly."

// startedReason is the background copy's first Activity line: the
// launcher's note when it left one; otherwise, when the copy before it
// ended without cleaning up earlier in this boot, that the system started
// it again; and otherwise that the system started it, at login on a
// desktop or at boot on a server.
func startedReason(note string, server, crashed bool) string {
	switch {
	case note != "":
		return note
	case crashed:
		return crashRestartReason
	case server:
		return "Started at boot."
	}
	return "Started at login."
}

// restartedAfterCrash reports whether a background copy the system started
// replaces one that ended without cleaning up earlier in this boot: the
// earlier one left its note, made at prevMs, after the computer booted at
// bootSec, seconds since the Unix epoch. Only launchd and systemd restart
// it; Windows starts it only at login. A boot time of zero is unknown.
func restartedAfterCrash(goos string, prevMs int64, wasThere bool, bootSec uint64) bool {
	if goos == "windows" || !wasThere || bootSec == 0 {
		return false
	}
	return prevMs/1000 >= int64(bootSec)
}

// olderThanVerified reports whether a detected CLI version is earlier than
// the one this build was checked against. A version that is missing or
// that cannot be read as numbers says nothing either way, and says nothing
// at all rather than a warning nobody can act on.
func olderThanVerified(cliVersion string) bool {
	if cliVersion == "" {
		return false
	}
	return compareVersions(cliVersion, buildinfo.VerifiedCLIVersion) < 0
}

// compareVersions orders two dot separated version strings by comparing
// each component as a number, so 2.1.100 comes after 2.1.99. A component
// that is not a number counts as zero.
func compareVersions(a, b string) int {
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
			if an < bn {
				return -1
			}
			return 1
		}
	}
	return 0
}

// openBrowser opens the page in the operating system's default browser.
func openBrowser(pageURL string) error {
	return hosts.OpenURL(pageURL)
}
