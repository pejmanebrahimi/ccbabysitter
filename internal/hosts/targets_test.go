package hosts

import (
	"strings"
	"testing"
)

// withTargetOS renders commands for goos until the test ends, whatever OS
// the test runs on.
func withTargetOS(t *testing.T, goos string) {
	t.Helper()
	old := targetOS
	targetOS = goos
	t.Cleanup(func() { targetOS = old })
}

func TestBackgroundResumeArgs(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555502"
	// A resume never names the session: its transcript already carries its
	// title, and every extra flag is one more reason for the CLI to fork a
	// copy instead.
	if got := strings.Join(BackgroundResumeArgs(id, false), " "); got != "--bg --resume "+id+" --remote-control" {
		t.Fatal(got)
	}
	if got := strings.Join(BackgroundResumeArgs(id, true), " "); got != "--bg --resume "+id {
		t.Fatal("saved options: never any flag: " + got)
	}
}

func TestOtherCommandHelpers(t *testing.T) {
	if got := strings.Join(StopArgs("a1b2c3d4"), " "); got != "stop a1b2c3d4" {
		t.Fatal(got)
	}
	if got := strings.Join(RemoveArgs("a1b2c3d4"), " "); got != "rm a1b2c3d4" {
		t.Fatal(got)
	}
	if got := strings.Join(AgentsArgs(), " "); got != "agents --json" {
		t.Fatal(got)
	}
	id := "11111111-2222-4333-8444-555555555503"
	if got := VisibleResumeCommand(id, true); got != "claude --resume "+id+" --remote-control" {
		t.Fatal(got)
	}
	if got := VisibleResumeCommand(id, false); got != "claude --resume "+id {
		t.Fatal(got)
	}
	if got := AttachCommand("a1b2c3d4"); got != "claude attach a1b2c3d4" {
		t.Fatal(got)
	}
}

// A CLI path with a space is read by two shells: the one the person types
// the line into, then the one ssh hands the command to on the server. It is
// quoted for both, so the server's shell still sees one word.
func TestSSHAttachCommandQuotesThePathForBothShells(t *testing.T) {
	old := targetOS
	targetOS = "linux"
	defer func() { targetOS = old }()

	if got := SSHAttachCommand("dev@203.0.113.7", "/home/dev/.local/bin/claude", "a1b2c3d4"); got != "ssh -t dev@203.0.113.7 /home/dev/.local/bin/claude attach a1b2c3d4" {
		t.Fatal(got)
	}
	if got := SSHAttachCommand("dev@example-host", "", "a1b2c3d4"); got != "ssh -t dev@example-host claude attach a1b2c3d4" {
		t.Fatal(got)
	}
	got := SSHAttachCommand("dev@203.0.113.7", "/home/dev/my tools/claude", "a1b2c3d4")
	want := `ssh -t dev@203.0.113.7 ''\''/home/dev/my tools/claude'\''' attach a1b2c3d4`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A resume command names the folder the session belongs to, because
// `claude --resume` looks the id up in the folder it is run from. The path
// is quoted so a POSIX shell reads it back exactly, spaces and quotes
// included.
func TestResumeCommandInQuotesTheFolder(t *testing.T) {
	withTargetOS(t, "linux")
	id := "11111111-2222-4333-8444-555555555504"
	cases := []struct{ cwd, want string }{
		{"/home/dev/ws", "cd /home/dev/ws && claude --resume " + id},
		{"/home/dev/my ws/it's", `cd '/home/dev/my ws/it'\''s' && claude --resume ` + id},
		{"/home/dev/$HOME;rm", `cd '/home/dev/$HOME;rm' && claude --resume ` + id},
		{"", "claude --resume " + id},
	}
	for _, c := range cases {
		if got := ResumeCommandIn(c.cwd, id); got != c.want {
			t.Fatalf("%q: got %q, want %q", c.cwd, got, c.want)
		}
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":                  "''",
		"/home/dev/ws":      "/home/dev/ws",
		"a b":               "'a b'",
		"it's":              `'it'\''s'`,
		"`x`":               "'`x`'",
		"C:\\Users\\dev":    `'C:\Users\dev'`,
		"/home/dev/a-b_c.d": "/home/dev/a-b_c.d",
		"~/projects/x y":    "~/'projects/x y'",
		"~/ws":              "~/ws",
		"~":                 "'~'",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Fatalf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestBackgroundResumeCommandIn(t *testing.T) {
	withTargetOS(t, "linux")
	id := "11111111-2222-4333-8444-555555555505"
	if got := BackgroundResumeCommandIn("/home/dev/my ws", id); got != "cd '/home/dev/my ws' && claude --bg --resume "+id {
		t.Fatal(got)
	}
	if got := BackgroundResumeCommandIn("", id); got != "claude --bg --resume "+id {
		t.Fatal(got)
	}
}

// The command that gives a folder the CLI's trust: claude, run there, which
// asks once. On Windows it targets PowerShell, which has no &&.
func TestTrustCommandIn(t *testing.T) {
	withTargetOS(t, "linux")
	if got := TrustCommandIn("/home/dev/my ws"); got != "cd '/home/dev/my ws' && claude" {
		t.Fatal(got)
	}
	withTargetOS(t, "windows")
	if got := TrustCommandIn(`C:\Users\dev\my ws`); got != `cd -LiteralPath 'C:\Users\dev\my ws'; claude` {
		t.Fatal(got)
	}
}

// On Windows, the resume commands target PowerShell (Windows PowerShell
// 5.1 has no `&&`, and cmd.exe reads single quotes literally), so the
// rendered form differs from every other OS. The folder is named with
// -LiteralPath, since a plain cd reads [ and ] in it as a wildcard, and
// every quote PowerShell ends a single-quoted string on is doubled: the
// typographic single quotes as well as the plain one.
func TestResumeCommandInOnWindows(t *testing.T) {
	withTargetOS(t, "windows")

	id := "11111111-2222-4333-8444-555555555501"
	cases := []struct{ cwd, want string }{
		{`C:\Users\dev\my ws`, `cd -LiteralPath 'C:\Users\dev\my ws'; claude --resume ` + id},
		{`C:\Users\dev\it's`, `cd -LiteralPath 'C:\Users\dev\it''s'; claude --resume ` + id},
		{`C:\Users\dev\ws [old]`, `cd -LiteralPath 'C:\Users\dev\ws [old]'; claude --resume ` + id},
		{"C:\\Users\\dev\\it\u2019s \u2018x\u2018 \u201ay\u201b", "cd -LiteralPath 'C:\\Users\\dev\\it\u2019\u2019s \u2018\u2018x\u2018\u2018 \u201a\u201ay\u201b\u201b'; claude --resume " + id},
		{`C:\Users\dev\$env:PATH`, `cd -LiteralPath 'C:\Users\dev\$env:PATH'; claude --resume ` + id},
		{"", "claude --resume " + id},
	}
	for _, c := range cases {
		if got := ResumeCommandIn(c.cwd, id); got != c.want {
			t.Errorf("%q: got %q, want %q", c.cwd, got, c.want)
		}
	}
	if got := BackgroundResumeCommandIn(`C:\Users\dev\my ws`, id); got != `cd -LiteralPath 'C:\Users\dev\my ws'; claude --bg --resume `+id {
		t.Fatal(got)
	}
}

// The existing POSIX expectations hold unchanged on Linux and darwin.
func TestResumeCommandInOnPOSIX(t *testing.T) {
	for _, os := range []string{"linux", "darwin"} {
		old := targetOS
		targetOS = os
		func() {
			defer func() { targetOS = old }()
			id := "11111111-2222-4333-8444-555555555506"
			if got := ResumeCommandIn("/home/dev/my ws", id); got != "cd '/home/dev/my ws' && claude --resume "+id {
				t.Fatalf("%s: %q", os, got)
			}
			if got := BackgroundResumeCommandIn("/home/dev/my ws", id); got != "cd '/home/dev/my ws' && claude --bg --resume "+id {
				t.Fatalf("%s: %q", os, got)
			}
		}()
	}
}

// On a Windows server ssh hands the command to cmd.exe, which does not
// read single quotes. The path reaches it bare, or in double quotes when
// it has a space, and is quoted once for the person's own shell so its
// backslashes survive.
func TestSSHAttachCommandOnWindows(t *testing.T) {
	old := targetOS
	targetOS = "windows"
	defer func() { targetOS = old }()

	got := SSHAttachCommand("dev@203.0.113.7", `C:\Users\dev\.local\bin\claude.exe`, "a1b2c3d4")
	if want := `ssh -t dev@203.0.113.7 'C:\Users\dev\.local\bin\claude.exe' attach a1b2c3d4`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	got = SSHAttachCommand("dev@203.0.113.7", `C:\Program Files\claude\claude.exe`, "a1b2c3d4")
	if want := `ssh -t dev@203.0.113.7 '"C:\Program Files\claude\claude.exe"' attach a1b2c3d4`; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// A user name that is not one safe token is left out, so ssh falls back
// to the local user name, and an IPv6 address stays bare, since ssh keeps
// brackets as part of the host name in a user@host destination.
func TestSSHTarget(t *testing.T) {
	cases := []struct{ user, address, want string }{
		{"alice", "203.0.113.7", "alice@203.0.113.7"},
		{"alice", "2001:db8::2", "alice@2001:db8::2"},
		{"a b", "host", "host"},
		{"-oProxy", "host", "host"},
		{"", "host", "host"},
		{"al.ice_1-x", "h", "al.ice_1-x@h"},
		{"a b", "2001:db8::2", "2001:db8::2"},
	}
	for _, c := range cases {
		if got := SSHTarget(c.user, c.address); got != c.want {
			t.Errorf("SSHTarget(%q, %q) = %q, want %q", c.user, c.address, got, c.want)
		}
	}
}

// An IPv6 target holds only characters no shell treats specially, so it
// stays bare on the line.
func TestSSHAttachCommandLeavesAnIPv6TargetBare(t *testing.T) {
	old := targetOS
	targetOS = "linux"
	defer func() { targetOS = old }()

	got := SSHAttachCommand(SSHTarget("alice", "2001:db8::2"), "/home/alice/.local/bin/claude", "a1b2c3d4")
	want := "ssh -t alice@2001:db8::2 /home/alice/.local/bin/claude attach a1b2c3d4"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}
