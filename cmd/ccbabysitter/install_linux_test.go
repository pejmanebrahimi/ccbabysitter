package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
