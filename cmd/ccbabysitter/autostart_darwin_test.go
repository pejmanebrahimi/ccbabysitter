//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchAgentInstalledFollowsTheFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if autostartInstalled == nil {
		t.Fatal("the check must be set on this platform")
	}
	if on, err := autostartInstalled(); err != nil || on {
		t.Fatalf("no file yet: %v %v", on, err)
	}
	path := filepath.Join(home, "Library", "LaunchAgents", "com.ccbabysitter.plist")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(launchAgentPlist("/Applications/ccbabysitter", "", "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if on, err := autostartInstalled(); err != nil || !on {
		t.Fatalf("the file is there: %v %v", on, err)
	}
}

func TestLaunchAgentInstalledReportsOtherErrors(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A plain file where the folder should be makes the look fail with
	// something other than not there.
	if err := os.MkdirAll(filepath.Join(home, "Library"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "Library", "LaunchAgents"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := autostartInstalled(); err == nil {
		t.Fatal("expected an error")
	}
}

// Turning start at login on moves the plist into LaunchAgents, and off
// moves it back to the state folder; neither unloads the running job.
func TestStartAtLoginMovesThePlist(t *testing.T) {
	launchdHome(t)
	f := useFakeLaunchctl(t)
	var out strings.Builder
	if !(launchdControl{}).Write(&out, true) {
		t.Fatal(out.String())
	}
	if _, err := installAutostartDarwin(true); err != nil {
		t.Fatal(err)
	}
	if on, _ := pathPresent(agentPath()); !on {
		t.Fatal("not in LaunchAgents after on")
	}
	if on, _ := pathPresent(offPath()); on {
		t.Fatal("still in the state folder after on")
	}
	// Activity says "start at login disabled: removed PATH": the path is
	// the LaunchAgent that is gone.
	if path, err := installAutostartDarwin(false); err != nil || path != agentPath() {
		t.Fatalf("off reported %q, %v", path, err)
	}
	if on, _ := pathPresent(offPath()); !on {
		t.Fatal("not back in the state folder after off")
	}
	if on, _ := pathPresent(agentPath()); on {
		t.Fatal("still in LaunchAgents after off")
	}
	if len(f.calls) != 0 {
		t.Fatalf("launchctl was called: %q", f.calls)
	}
}

func TestStartAtLoginWritesAPlistWhenThereIsNone(t *testing.T) {
	launchdHome(t)
	useFakeLaunchctl(t)
	if _, err := installAutostartDarwin(true); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(agentPath())
	if err != nil || !strings.Contains(string(text), "--service") {
		t.Fatalf("%v %q", err, text)
	}
}
