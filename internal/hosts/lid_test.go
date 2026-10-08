package hosts

import (
	"context"
	"testing"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

// macOS: closing the lid puts the computer to sleep when ioreg says so. A
// Mac on an external display keeps running with its lid shut, and a Mac
// with no lid has no such entry.
func TestDarwinLidSleeps(t *testing.T) {
	for _, c := range []struct {
		out  string
		want bool
	}{
		{"      \"AppleClamshellCausesSleep\" = Yes\n      \"AppleClamshellState\" = No\n", true},
		{"      \"AppleClamshellCausesSleep\" = No\n      \"AppleClamshellState\" = Yes\n", false},
		{"", false},
	} {
		if got := darwinLidSleeps(c.out); got != c.want {
			t.Errorf("darwinLidSleeps(%q) = %v, want %v", c.out, got, c.want)
		}
	}
}

// Windows: the power plan's lid action for the power the computer runs on
// now, plugged in or on battery: 0 does nothing, anything else sleeps,
// hibernates or shuts down. A computer whose plan has no lid action has no
// lid.
func TestWindowsLidSleeps(t *testing.T) {
	const both = `Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)
  GUID Alias: SCHEME_BALANCED
  Subgroup GUID: 4f971e89-eebd-4455-a8de-9e59040e7347  (Power buttons and lid)
    GUID Alias: SUB_BUTTONS
    Power Setting GUID: 5ca83367-6e45-459f-a27b-476b1d01c936  (Lid close action)
      GUID Alias: LIDACTION
      Possible Setting Index: 000
      Possible Setting Friendly Name: Do nothing
      Possible Setting Index: 001
      Possible Setting Friendly Name: Sleep
    Current AC Power Setting Index: 0x00000000
    Current DC Power Setting Index: 0x00000001
`
	const noLid = `Power Scheme GUID: 381b4222-f694-41f0-9685-ff5bb260df2e  (Balanced)
  GUID Alias: SCHEME_BALANCED
`
	for _, c := range []struct {
		out  string
		onAC bool
		want bool
	}{
		{both, true, false},
		{both, false, true},
		{noLid, false, false},
	} {
		if got := windowsLidSleeps(c.out, c.onAC); got != c.want {
			t.Errorf("windowsLidSleeps(onAC %v) = %v, want %v", c.onAC, got, c.want)
		}
	}
}

// Linux: logind's lid action for how the computer is now, docked to an
// external display, on external power or on battery. ignore and lock leave
// it running; an empty action on external power is the battery one.
func TestLinuxLidSleeps(t *testing.T) {
	for _, c := range []struct {
		name string
		out  string
		want bool
	}{
		{"on power, default", "b false\nb true\ns \"suspend\"\ns \"\"\ns \"ignore\"\n", true},
		{"on power, set to ignore", "b false\nb true\ns \"suspend\"\ns \"ignore\"\ns \"ignore\"\n", false},
		{"on battery", "b false\nb false\ns \"suspend\"\ns \"ignore\"\ns \"ignore\"\n", true},
		{"docked", "b true\nb true\ns \"suspend\"\ns \"suspend\"\ns \"ignore\"\n", false},
		{"locks only", "b false\nb false\ns \"lock\"\ns \"\"\ns \"ignore\"\n", false},
		{"older logind, one answer", "s \"hibernate\"\n", true},
		{"nothing", "", false},
	} {
		if got := linuxLidSleeps(c.out); got != c.want {
			t.Errorf("%s: linuxLidSleeps = %v, want %v", c.name, got, c.want)
		}
	}
}

// The environment says whether closing the lid now puts the computer to
// sleep, as the probe answers, and no when there is no probe.
func TestDetectLidSleeps(t *testing.T) {
	r := claude.NewFakeRunner(func(args []string) (string, error) { return "2.1.275 (Claude Code)\n", nil })
	p := probes("darwin")
	if Detect(context.Background(), p, r, false).LidSleeps {
		t.Fatal("no probe counted as a lid that sleeps")
	}
	p.LidSleeps = func() bool { return true }
	if !Detect(context.Background(), p, r, false).LidSleeps {
		t.Fatal("the probe's answer was lost")
	}
}

// The environment names the processor, for the version details a bug
// report carries.
func TestDetectArch(t *testing.T) {
	r := claude.NewFakeRunner(func(args []string) (string, error) { return "2.1.275 (Claude Code)\n", nil })
	p := probes("darwin")
	p.Arch = "arm64"
	if got := Detect(context.Background(), p, r, false).Arch; got != "arm64" {
		t.Fatalf("arch %q", got)
	}
}
