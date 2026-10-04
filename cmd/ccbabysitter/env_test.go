package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
)

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestBannerStatesTheKeepAwakeRuleAndTunnelsWhenHeadless(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", true, "dev", "203.0.113.7", "2.1.275", "", "", foregroundClosing)
	for _, want := range []string{
		"Keep computer awake: while at least one session is babysat, the computer does not idle-sleep",
		"To view and control this server's CC Babysitter from a computer with a browser, connect from that computer with:\n" +
			"  ssh -L 47391:127.0.0.1:47391 dev@203.0.113.7\n" +
			"then open http://127.0.0.1:47391 in its browser while that connection is open.\n",
		"Ctrl+C",
	} {
		if !contains(b, want) {
			t.Fatalf("missing %q in\n%s", want, b)
		}
	}
	for _, gone := range []string{"Always", "Babysitting", "change it in the header"} {
		if contains(b, gone) {
			t.Fatalf("the banner must not offer a mode any more, found %q in\n%s", gone, b)
		}
	}
}

// The banner calls the web page what the rest of the program calls it.
func TestBannerNamesThePage(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "2.1.275", "", "", foregroundClosing)
	if !contains(b, "Page: http://127.0.0.1:47391\n") {
		t.Fatalf("missing the page line in\n%s", b)
	}
	if contains(strings.ToLower(b), "cockpit") {
		t.Fatalf("the banner says page, not cockpit:\n%s", b)
	}
}

// A machine nobody is sitting at gets the lines about reaching the page from
// another computer; the one in front of the user does not.
func TestBannerOffersNoTunnelWhenNotHeadless(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "2.1.275", "", "", foregroundClosing)
	for _, gone := range []string{"To view and control this server's", "ssh -L"} {
		if contains(b, gone) {
			t.Fatalf("a non-headless banner must not offer a tunnel: found %q in\n%s", gone, b)
		}
	}
}

func TestBannerMissingCLIReportsNotFound(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "", "", "", foregroundClosing)
	if !contains(b, "Claude Code CLI not found") {
		t.Fatalf("missing the not-found line in\n%s", b)
	}
}

func TestBannerOmitsUnknownVersions(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "2.1.275", "2.2553.1", "2.1.263", foregroundClosing)
	for _, want := range []string{"Claude Code CLI 2.1.275", "Desktop 2.2553.1", "VS Code extension 2.1.263"} {
		if !contains(b, want) {
			t.Fatalf("missing %q in\n%s", want, b)
		}
	}
}

// The tunnel command leaves out -N, so the window it runs in is also an
// ordinary shell on the server.
func TestTunnelHintFormat(t *testing.T) {
	got := tunnelHint(47391, "dev", "203.0.113.7")
	want := "ssh -L 47391:127.0.0.1:47391 dev@203.0.113.7"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSSHServerAddress(t *testing.T) {
	cases := map[string]string{
		"198.51.100.4 52814 203.0.113.7 22":   "203.0.113.7",
		"2001:db8::4 52814 2001:db8::7 22":    "2001:db8::7",
		"":                                    "",
		"198.51.100.4 52814":                  "",
		"198.51.100.4 52814 $(x) 22":          "",
		"198.51.100.4 52814 [2001:db8::7] 22": "",
	}
	for in, want := range cases {
		if got := sshServerAddress(in); got != want {
			t.Errorf("sshServerAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

// The address of the ssh connection this was started from wins and is
// remembered, so a later run with no ssh connection of its own, such as
// the service, still names it; with neither, the host name is all there
// is.
func TestServerAddressOrder(t *testing.T) {
	dir := t.TempDir()
	if got := serverAddress(dir, "", "example-host"); got != "example-host" {
		t.Fatalf("nothing seen or saved: got %q", got)
	}
	if got := serverAddress(dir, "198.51.100.4 52814 203.0.113.7 22", "example-host"); got != "203.0.113.7" {
		t.Fatalf("from the connection: got %q", got)
	}
	if got := serverAddress(dir, "", "example-host"); got != "203.0.113.7" {
		t.Fatalf("from the saved address: got %q", got)
	}
	if got := serverAddress(dir, "198.51.100.4 52814 203.0.113.9 22", "example-host"); got != "203.0.113.9" {
		t.Fatalf("a new connection wins over the saved address: got %q", got)
	}
	if got := serverAddress(dir, "", "example-host"); got != "203.0.113.9" {
		t.Fatalf("the newer address is the one saved: got %q", got)
	}
	if got := serverAddress(dir, "198.51.100.4 52814 $(x) 22", "example-host"); got != "203.0.113.9" {
		t.Fatalf("an address that is not one plain word is ignored: got %q", got)
	}
}

// A CLI older than the one this build was checked against may word its
// output differently, which is worth one line on startup; a newer one, or
// one that could not be detected at all, is worth nothing.
func TestBannerNotesAnOlderCLI(t *testing.T) {
	const note = "verified with Claude Code"
	older := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "2.1.9", "", "", foregroundClosing)
	if !strings.Contains(older, note) {
		t.Fatalf("an older CLI must be called out:\n%s", older)
	}
	if !strings.Contains(older, buildinfo.VerifiedCLIVersion) {
		t.Fatalf("the note must name the version:\n%s", older)
	}
	for _, version := range []string{buildinfo.VerifiedCLIVersion, "2.1.300", "3.0.0", ""} {
		out := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", version, "", "", foregroundClosing)
		if strings.Contains(out, note) {
			t.Fatalf("version %q must not be called out:\n%s", version, out)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2.1.275", "2.1.275", 0},
		{"2.1.99", "2.1.100", -1},
		{"2.1.100", "2.1.99", 1},
		{"2.2", "2.1.999", 1},
		{"2.1", "2.1.0", 0},
		{"not-a-version", "2.1.275", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// The README states the oldest Claude Code version this build was checked
// against, so it has to move with the constant.
func TestReadmeNamesTheVerifiedCLIVersion(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "Claude Code " + buildinfo.VerifiedCLIVersion + " or later"; !strings.Contains(string(body), want) {
		t.Fatalf("README.md does not say %q", want)
	}
}

// The banner prints the address with the page's key wherever it tells a
// person to open the page, and the tunnel still forwards the plain port.
func TestBannerCarriesTheKeyedAddress(t *testing.T) {
	keyed := "http://127.0.0.1:47391/?token=" + strings.Repeat("ab", 32)
	b := banner("0.1.0", keyed, true, "dev", "203.0.113.7", "2.1.275", "", "", foregroundClosing)
	for _, want := range []string{
		"Page: " + keyed + "\n",
		"  ssh -L 47391:127.0.0.1:47391 dev@203.0.113.7\n",
		"then open " + keyed + " in its browser while that connection is open.\n",
	} {
		if !contains(b, want) {
			t.Fatalf("missing %q in\n%s", want, b)
		}
	}
}

// An IPv6 address stays bare after the user@, where ssh reads it whole,
// and needs no quoting for the shell.
func TestTunnelHintLeavesAnIPv6AddressBare(t *testing.T) {
	got := tunnelHint(47391, "alice", "2001:db8::2")
	want := "ssh -L 47391:127.0.0.1:47391 alice@2001:db8::2"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// The ssh destination the page shows follows the same rules as the tunnel
// command, and only a machine with no display has one.
func TestSSHTargetFor(t *testing.T) {
	if got := sshTargetFor(false, "alice", "203.0.113.7"); got != "" {
		t.Errorf("a machine with a display has no target, got %q", got)
	}
	if got := sshTargetFor(true, "alice", "2001:db8::2"); got != "alice@2001:db8::2" {
		t.Errorf("got %q", got)
	}
	if got := sshTargetFor(true, "a b", "203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("got %q", got)
	}
}

func TestWithoutDomain(t *testing.T) {
	for in, want := range map[string]string{`HOST\alice`: "alice", `CORP\dev\bob`: "bob", "alice": "alice", `trailing\`: `trailing\`} {
		if got := withoutDomain(in); got != want {
			t.Errorf("withoutDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBackgroundClosing(t *testing.T) {
	for _, c := range []struct {
		server, on bool
		want       string
	}{
		{false, true, "CC Babysitter runs in the background and starts again when you log in. You can close this window."},
		{true, true, "CC Babysitter runs in the background and starts again when this server boots. You can close this window."},
		{false, false, "CC Babysitter runs in the background until you quit it or restart. Start at login is off. You can close this window."},
		{true, false, "CC Babysitter runs in the background until you quit it or restart. Start at boot is off. You can close this window."},
	} {
		if got := backgroundClosing(c.server, c.on); got != c.want {
			t.Errorf("server %v, on %v: %q", c.server, c.on, got)
		}
	}
}

func TestBannerEndsWithItsClosingLine(t *testing.T) {
	b := banner("0.1.0", "http://127.0.0.1:47391", false, "dev", "box", "2.1.275", "", "", backgroundClosing(false, true))
	if !strings.HasSuffix(b, backgroundClosing(false, true)+"\n") || strings.Contains(b, "Ctrl+C") {
		t.Fatalf("banner:\n%s", b)
	}
}

func TestStartedReason(t *testing.T) {
	if got := startedReason("Started in the background by ccbabysitter.", false, true); got != "Started in the background by ccbabysitter." {
		t.Fatalf("a launcher's note is kept: %q", got)
	}
	if got := startedReason("", false, false); got != "Started at login." {
		t.Fatalf("desktop: %q", got)
	}
	if got := startedReason("", true, false); got != "Started at boot." {
		t.Fatalf("server: %q", got)
	}
	for _, server := range []bool{false, true} {
		if got := startedReason("", server, true); got != crashRestartReason {
			t.Fatalf("restarted after a crash, server %v: %q", server, got)
		}
	}
}

// A background copy that ended without cleaning up, earlier in this boot,
// was restarted by launchd or systemd. One from before this boot ended
// with the computer, and the new one started at login or boot. Windows
// never restarts it, so there it is always login.
func TestRestartedAfterCrash(t *testing.T) {
	const boot = 1_000_000 // seconds
	cases := []struct {
		goos     string
		prevMs   int64
		wasThere bool
		boot     uint64
		want     bool
	}{
		{"darwin", (boot + 60) * 1000, true, boot, true},
		{"linux", (boot + 60) * 1000, true, boot, true},
		{"darwin", (boot - 60) * 1000, true, boot, false},
		{"darwin", (boot + 60) * 1000, false, boot, false},
		{"windows", (boot + 60) * 1000, true, boot, false},
		{"darwin", (boot + 60) * 1000, true, 0, false},
	}
	for _, c := range cases {
		if got := restartedAfterCrash(c.goos, c.prevMs, c.wasThere, c.boot); got != c.want {
			t.Errorf("restartedAfterCrash(%+v) = %v", c, got)
		}
	}
}

// The background copy's own banner goes to the journal: it says how to
// quit a copy that has no terminal, not Ctrl+C.
func TestServiceClosingHasNoCtrlC(t *testing.T) {
	if strings.Contains(serviceClosing, "Ctrl+C") || !strings.Contains(serviceClosing, "ccbabysitter quit") {
		t.Fatalf("%q", serviceClosing)
	}
}
