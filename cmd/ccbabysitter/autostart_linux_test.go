//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUnitEnabledFollowsTheWantsLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if autostartInstalled == nil {
		t.Fatal("the check must be set on this platform")
	}
	unit, err := writeUnit("/home/dev/.local/bin/ccbabysitter", false)
	if err != nil {
		t.Fatal(err)
	}
	if on, err := autostartInstalled(); err != nil || on {
		t.Fatalf("a unit that is only written is not enabled: %v %v", on, err)
	}
	link := filepath.Join(home, ".config", "systemd", "user", "default.target.wants", "ccbabysitter.service")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unit, link); err != nil {
		t.Fatal(err)
	}
	if on, err := autostartInstalled(); err != nil || !on {
		t.Fatalf("the link is there: %v %v", on, err)
	}
}

func TestUnitEnabledReportsOtherErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A plain file where the wants folder should be makes the look fail
	// with something other than not there.
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.target.wants"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := autostartInstalled(); err == nil {
		t.Fatal("expected an error")
	}
}

func TestAutostartInstalledSeesEitherLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	user := filepath.Join(home, ".config", "systemd", "user")
	for _, target := range []string{"default.target.wants", "graphical-session.target.wants"} {
		dir := filepath.Join(user, target)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(dir, "ccbabysitter.service")
		if err := os.WriteFile(link, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if on, err := autostartInstalledLinux(); err != nil || !on {
			t.Errorf("%s: installed = %v, %v", target, on, err)
		}
		os.Remove(link)
	}
	if on, _ := autostartInstalledLinux(); on {
		t.Error("installed with no link at all")
	}
}

// systemctl enable and disable change a link in a wants folder and flush
// nothing, so a power cut right after turning start at login on could lose
// the link, and CC Babysitter would not start at the next boot. Both wants
// folders that exist, and the unit's own folder that holds them, are
// flushed afterwards.
func TestStartLinksAreFlushed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	unit, err := systemdUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	wants := filepath.Join(filepath.Dir(unit), "graphical-session.target.wants")
	if err := os.MkdirAll(wants, 0o755); err != nil {
		t.Fatal(err)
	}
	var flushed []string
	saved := syncDir
	syncDir = func(dir string) error { flushed = append(flushed, dir); return nil }
	t.Cleanup(func() { syncDir = saved })

	flushStartLinks(unit)
	want := []string{wants, filepath.Dir(unit)}
	if len(flushed) != 2 || flushed[0] != want[0] || flushed[1] != want[1] {
		t.Fatalf("flushed %q, want %q", flushed, want)
	}
}
