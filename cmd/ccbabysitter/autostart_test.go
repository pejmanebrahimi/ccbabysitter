package main

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLaunchAgentPlistEscapesPath(t *testing.T) {
	p := launchAgentPlist(`/Users/dev/Applications & Co/ccbabysitter`, "", "")
	if !strings.Contains(p, "com.ccbabysitter") {
		t.Fatalf("missing the label in\n%s", p)
	}
	if !strings.Contains(p, "&amp;") {
		t.Fatalf("ampersand not escaped in\n%s", p)
	}
	if strings.Contains(p, "Applications & Co") {
		t.Fatalf("raw ampersand leaked into the plist:\n%s", p)
	}
	if !strings.Contains(p, "RunAtLoad") || !strings.Contains(p, "<true/>") {
		t.Fatalf("missing RunAtLoad in\n%s", p)
	}
	if !strings.Contains(p, "Interactive") {
		t.Fatalf("missing the process type in\n%s", p)
	}
}

// hostPath turns a slash-separated absolute path into one this OS reads as
// absolute, on drive C: on Windows, since refuseTemporaryBinary takes paths
// apart with the host's own separator, the way the real ones arrive.
func hostPath(p string) string {
	if runtime.GOOS == "windows" {
		return `C:` + filepath.FromSlash(p)
	}
	return p
}

func TestRefuseTemporaryBinaryInsideTempDir(t *testing.T) {
	tmp := hostPath("/tmp")
	if err := refuseTemporaryBinary(hostPath("/tmp/ccbabysitter"), tmp); err == nil {
		t.Fatal("expected refusal for a path directly inside the temp folder")
	}
	if err := refuseTemporaryBinary(hostPath("/tmp/sub/ccbabysitter"), tmp); err == nil {
		t.Fatal("expected refusal for a path nested inside the temp folder")
	}
	if err := refuseTemporaryBinary(hostPath("/home/dev/ws/ccbabysitter"), tmp); err != nil {
		t.Fatalf("unexpected refusal for a permanent path: %v", err)
	}
	if err := refuseTemporaryBinary(hostPath("/tmpfiles/ccbabysitter"), tmp); err != nil {
		t.Fatalf("a folder that only starts with the temp folder's name is not inside it: %v", err)
	}
}

func TestRefuseTemporaryBinaryGoBuildDir(t *testing.T) {
	if err := refuseTemporaryBinary(hostPath("/var/folders/xy/go-build12345/b001/exe/main"), ""); err == nil {
		t.Fatal("expected refusal for a path made by go run")
	}
	if err := refuseTemporaryBinary(hostPath("/home/dev/ws/ccbabysitter"), ""); err != nil {
		t.Fatalf("unexpected refusal for a permanent path: %v", err)
	}
}

// A systemd user service runs with systemd's own PATH, which leaves out
// the folder the installer puts the CLI in, so the unit names that folder
// first. With no CLI found at install time the unit has no Environment
// line at all.
func TestUnitFileShapes(t *testing.T) {
	const without = `[Unit]
Description=CC Babysitter: keeps Claude Code sessions alive and remote-controlled
After=network-online.target

[Service]
ExecStart=/home/dev/.local/bin/ccbabysitter --service
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=default.target
`
	if got := mustUnitFile(t, "/home/dev/.local/bin/ccbabysitter", ""); got != without {
		t.Fatalf("got\n%s\nwant\n%s", got, without)
	}

	const with = `[Unit]
Description=CC Babysitter: keeps Claude Code sessions alive and remote-controlled
After=network-online.target

[Service]
Environment=PATH=/home/dev/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ExecStart=/home/dev/.local/bin/ccbabysitter --service
Restart=on-failure
RestartSec=5
KillMode=process

[Install]
WantedBy=default.target
`
	if got := mustUnitFile(t, "/home/dev/.local/bin/ccbabysitter", "/home/dev/.local/bin/claude"); got != with {
		t.Fatalf("got\n%s\nwant\n%s", got, with)
	}
}

// A folder systemd would read differently is quoted and escaped, so the
// line stays one assignment of exactly that folder.
func TestUnitEnvironmentQuoting(t *testing.T) {
	cases := map[string]string{
		"PATH=/home/dev/bin:/usr/bin":      "Environment=PATH=/home/dev/bin:/usr/bin",
		"PATH=/home/dev/my tools:/usr/bin": `Environment="PATH=/home/dev/my tools:/usr/bin"`,
		"PATH=/home/dev/100%:/usr/bin":     "Environment=PATH=/home/dev/100%%:/usr/bin",
		`PATH=/home/dev/a"b\c:/usr/bin`:    `Environment="PATH=/home/dev/a\"b\\c:/usr/bin"`,
		"PATH=/home/o'brien/bin:/usr/bin":  `Environment="PATH=/home/o'brien/bin:/usr/bin"`,
	}
	for in, want := range cases {
		if got := unitEnvironment(in); got != want {
			t.Errorf("unitEnvironment(%q) = %q, want %q", in, got, want)
		}
	}
}

// mustUnitFile is unitFile for a path that is known to be writable.
func mustUnitFile(t *testing.T, binPath, cliPath string) string {
	t.Helper()
	u, err := unitFile(binPath, cliPath, false, "")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// systemd splits an ExecStart= line into words and expands specifiers, so
// a path with a space, a quote or a percent sign is quoted or escaped the
// way an Environment= line is, and one with a line break is refused.
func TestUnitExecStartQuoting(t *testing.T) {
	cases := map[string]string{
		"/home/dev/.local/bin/ccbabysitter": "ExecStart=/home/dev/.local/bin/ccbabysitter --service",
		"/home/Jane Doe/bin/ccbabysitter":   `ExecStart="/home/Jane Doe/bin/ccbabysitter" --service`,
		"/opt/100%/ccbabysitter":            "ExecStart=/opt/100%%/ccbabysitter --service",
		`/home/a"b/ccbabysitter`:            `ExecStart="/home/a\"b/ccbabysitter" --service`,
		"/home/o'brien/bin/ccbabysitter":    `ExecStart="/home/o'brien/bin/ccbabysitter" --service`,
		"/opt/a$b/ccbabysitter":             "ExecStart=/opt/a$$b/ccbabysitter --service",
	}
	for bin, want := range cases {
		u := mustUnitFile(t, bin, "")
		if !strings.Contains(u, "\n"+want+"\n") {
			t.Errorf("unitFile(%q) has no line %q in\n%s", bin, want, u)
		}
	}
	bin := "/home/a\nb/ccbabysitter"
	_, err := unitFile(bin, "", false, "")
	if err == nil {
		t.Fatal("a path with a line break was accepted")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%q", bin)) {
		t.Errorf("the error does not name the path: %v", err)
	}
}

// A desktop unit starts with the graphical session, after it, when the
// display variables are in the user manager; a server unit starts at boot.
func TestUnitFileTargetsTheGraphicalSessionOnADesktop(t *testing.T) {
	desktop, err := unitFile("/home/a/.local/bin/ccbabysitter", "", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(desktop, "After=graphical-session.target\n") || !strings.Contains(desktop, "WantedBy=graphical-session.target\n") || strings.Contains(desktop, "default.target") {
		t.Fatalf("desktop unit:\n%s", desktop)
	}
	server, err := unitFile("/home/a/.local/bin/ccbabysitter", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(server, "WantedBy=default.target\n") || strings.Contains(server, "graphical-session") {
		t.Fatalf("server unit:\n%s", server)
	}
}

// A state folder moved with XDG_DATA_HOME in a shell profile is not in the
// user manager's environment, so the unit names it, and the copy it starts
// uses the same folder as the launcher and the command line.
func TestUnitFileCarriesTheDataHome(t *testing.T) {
	u, err := unitFile("/home/a/.local/bin/ccbabysitter", "", true, "/data/home a")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(u, "\n"+`Environment="XDG_DATA_HOME=/data/home a"`+"\n") {
		t.Fatalf("unit:\n%s", u)
	}
	u, _ = unitFile("/home/a/.local/bin/ccbabysitter", "", true, "")
	if strings.Contains(u, "XDG_DATA_HOME") {
		t.Fatalf("no data home, but:\n%s", u)
	}
}

func TestLaunchAgentPlistRunsTheServiceAndComesBackFromACrash(t *testing.T) {
	p := launchAgentPlist("/Users/dev/.local/bin/ccbabysitter", "/Users/dev/.local/bin/claude", "/Users/dev/data")
	for _, want := range []string{
		"<string>/Users/dev/.local/bin/ccbabysitter</string>\n\t\t<string>--service</string>",
		"<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>",
		"<key>AbandonProcessGroup</key>\n\t<true/>",
		"<key>ThrottleInterval</key>\n\t<integer>2</integer>",
		"<key>PATH</key>\n\t\t<string>/Users/dev/.local/bin:/opt/homebrew/bin:/opt/homebrew/sbin:" + servicePath + "</string>",
		"<key>XDG_DATA_HOME</key>\n\t\t<string>/Users/dev/data</string>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist has no\n%s\nin\n%s", want, p)
		}
	}
	q := launchAgentPlist("/b/ccbabysitter", "", "")
	if strings.Contains(q, "XDG_DATA_HOME") || !strings.Contains(q, "<key>PATH</key>\n\t\t<string>/opt/homebrew/bin:/opt/homebrew/sbin:"+servicePath+"</string>") {
		t.Errorf("without a CLI or data home:\n%s", q)
	}
}

// A line break in the data folder would end the Environment line and add a
// line of the person's choosing to the unit: it is refused.
func TestUnitFileRefusesALineBreakInTheDataHome(t *testing.T) {
	if _, err := unitFile("/home/a/.local/bin/ccbabysitter", "", true, "/data\nExecStartPre=/bin/false"); err == nil {
		t.Fatal("a data home with a line break was written into the unit")
	}
}
