package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

func TestUnitFile(t *testing.T) {
	u := mustUnitFile(t, "/home/dev/.local/bin/ccbabysitter", "")
	for _, want := range []string{
		"ExecStart=/home/dev/.local/bin/ccbabysitter --service",
		"Restart=on-failure",
		"KillMode=process",
		"WantedBy=default.target",
		"Description=CC Babysitter",
	} {
		if !strings.Contains(u, want) {
			t.Fatalf("missing %q in\n%s", want, u)
		}
	}
}

// The page asks whether the unit is there so it can say how to make a
// babysat session survive a restart. Asking only ever looks.
func TestServiceInstalledLooksForTheUnit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if serviceInstalled() {
		t.Fatal("no unit has been written yet")
	}
	path, err := systemdUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(mustUnitFile(t, "/home/dev/bin/ccbabysitter", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if !serviceInstalled() {
		t.Fatal("the unit is there")
	}
}

func TestDisplayVariablesToImport(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XAUTHORITY", "/run/user/1000/xauth")
	got := displayVariablesSet()
	if strings.Join(got, " ") != "DISPLAY XAUTHORITY" {
		t.Fatalf("got %q", got)
	}
	os.Unsetenv("DISPLAY")
	os.Unsetenv("XAUTHORITY")
	if got := displayVariablesSet(); len(got) != 0 {
		t.Fatalf("nothing set, got %q", got)
	}
}

// fakeSystemctl records systemctl calls instead of making them.
func fakeSystemctl(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	saved := runSystemctl
	runSystemctl = func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }
	t.Cleanup(func() { runSystemctl = saved })
	return &calls
}

// The display variables go to the user manager before a desktop unit is
// started or restarted, and never for a server unit or from an ssh session,
// whose forwarded display would outlive the connection.
func TestDisplayIsImportedOnlyForADesktopStart(t *testing.T) {
	t.Setenv("DISPLAY", ":0")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XAUTHORITY", "")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	calls := fakeSystemctl(t)
	var out strings.Builder
	systemdControl{}.Start(&out, true)
	systemdControl{}.Restart(&out, true)
	if got := strings.Join(*calls, "; "); got != "import-environment DISPLAY; start ccbabysitter; import-environment DISPLAY; restart ccbabysitter" {
		t.Fatalf("desktop: %q", got)
	}
	*calls = nil
	systemdControl{}.Start(&out, false)
	if got := strings.Join(*calls, "; "); got != "start ccbabysitter" {
		t.Fatalf("server: %q", got)
	}
	*calls = nil
	t.Setenv("SSH_CONNECTION", "198.51.100.4 50000 203.0.113.7 22")
	systemdControl{}.Start(&out, true)
	if got := strings.Join(*calls, "; "); got != "start ccbabysitter" {
		t.Fatalf("over ssh: %q", got)
	}
}

// A rewritten unit whose start at login was on is enabled again, so its
// link moves to the target the unit names now; one that was off is not.
func TestRefreshUnitReenablesARewrittenEnabledUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	saved := executablePath
	executablePath = func() (string, error) { return "/home/dev/.local/bin/ccbabysitter", nil }
	t.Cleanup(func() { executablePath = saved })
	unit, err := systemdUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		if err := writeUnitAt(unit, "[Service]\nExecStart=/old/ccbabysitter --no-open\n"); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(filepath.Dir(unit), "default.target.wants", "ccbabysitter.service")
		os.Remove(link)
		if enabled {
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(unit, link); err != nil {
				t.Fatal(err)
			}
		}
		calls := fakeSystemctl(t)
		var out strings.Builder
		rewritten, ok := systemdControl{}.RefreshUnit(&out, true)
		want := "daemon-reload"
		if enabled {
			want = "daemon-reload; reenable ccbabysitter"
		}
		if !rewritten || !ok || strings.Join(*calls, "; ") != want {
			t.Fatalf("enabled %v: rewritten %v ok %v calls %q out %q", enabled, rewritten, ok, *calls, out.String())
		}
	}
}

// A unit that is already the right one, but whose enable link is still in
// the other kind's wants folder, as when an earlier reenable failed, is
// enabled again so the link follows the unit.
func TestRefreshUnitMovesALinkLeftInTheWrongFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	saved := executablePath
	executablePath = func() (string, error) { return "/home/dev/.local/bin/ccbabysitter", nil }
	t.Cleanup(func() { executablePath = saved })
	unit, err := systemdUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	text, err := unitFile("/home/dev/.local/bin/ccbabysitter", claude.FindCLI(), true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeUnitAt(unit, text); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(unit), "default.target.wants", "ccbabysitter.service")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unit, link); err != nil {
		t.Fatal(err)
	}
	calls := fakeSystemctl(t)
	var out strings.Builder
	rewritten, ok := systemdControl{}.RefreshUnit(&out, true)
	if rewritten || !ok || strings.Join(*calls, "; ") != "reenable ccbabysitter" {
		t.Fatalf("rewritten %v ok %v calls %q", rewritten, ok, *calls)
	}
}

// Uninstall forgets that start at login was turned on once, so a later
// plain run turns it on again, as on a machine that never had it.
func TestLinuxUninstallForgetsTheLoginStartChoice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	fakeSystemctl(t)
	if err := state.MarkLoginStartOffered(state.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	runUninstall(&out)
	if state.LoginStartOffered(state.DefaultDir()) {
		t.Fatalf("the choice is still remembered after uninstall:\n%s", out.String())
	}
}
