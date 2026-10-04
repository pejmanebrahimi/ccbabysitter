//go:build darwin

package main

import (
	"os"
	"path/filepath"
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
