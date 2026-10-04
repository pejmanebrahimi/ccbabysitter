package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryCommandInTheUsageHasHelp(t *testing.T) {
	var b bytes.Buffer
	printUsage(&b)
	runCommands := map[string]bool{"install": true, "uninstall": true, "reset": true, "version": true, "help": true}
	seen := 0
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(line, "Examples") {
			break // the examples repeat commands the list above has named
		}
		f := strings.Fields(line)
		// The plain run line's second word is its description ("start"),
		// and --no-open, --demo and --port are flags, not commands.
		if len(f) < 2 || f[0] != "ccbabysitter" || !(runCommands[f[1]] || isControlCommand(f[1])) {
			continue
		}
		seen++
		var h bytes.Buffer
		if !printCommandHelp(&h, f[1]) || !strings.Contains(h.String(), "ccbabysitter "+f[1]) {
			t.Errorf("no help page for %s", f[1])
		}
	}
	if seen != 15 {
		t.Errorf("found %d commands in the usage, want 15", seen)
	}
}

func TestUsageNamesSelfJSONAndExitCodes(t *testing.T) {
	var b bytes.Buffer
	printUsage(&b)
	for _, want := range []string{"babysit S", "self", "--json", "Exit codes", "ccbabysitter babysit self", "ccbabysitter --demo"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("usage has no %q", want)
		}
	}
}

// longUsageLines are the two usage lines that shipped before the control
// commands and say more than fits in 100 characters.
var longUsageLines = []string{"  ccbabysitter                 start CC Babysitter.", "  ccbabysitter --port N"}

func TestHelpLinesFitAndAreASCII(t *testing.T) {
	var all bytes.Buffer
	printUsage(&all)
	for _, name := range []string{"status", "list", "show", "babysit", "unbabysit", "retry", "stop", "activity", "settings", "quit", "install", "uninstall", "reset", "version", "help"} {
		printCommandHelp(&all, name)
	}
	for _, line := range strings.Split(all.String(), "\n") {
		long := false
		for _, p := range longUsageLines {
			long = long || strings.HasPrefix(line, p)
		}
		if len(line) >= 100 && !long {
			t.Errorf("help line is %d characters: %q", len(line), line)
		}
		for _, r := range line {
			if r > 126 || (r < 32 && r != '\t') {
				t.Errorf("non-ASCII in help: %q", line)
				break
			}
		}
	}
}

func TestEveryExampleParses(t *testing.T) {
	var all bytes.Buffer
	printUsage(&all)
	for _, name := range []string{"status", "list", "show", "babysit", "unbabysit", "retry", "stop", "activity", "settings", "quit"} {
		printCommandHelp(&all, name)
	}
	inExamples := false
	for _, line := range strings.Split(all.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "Example"):
			inExamples = true
			continue
		case !strings.HasPrefix(line, "  "):
			inExamples = false
		}
		f := strings.Fields(line)
		if !inExamples || len(f) < 2 || f[0] != "ccbabysitter" || !isControlCommand(f[1]) {
			continue
		}
		if _, err := parseControlArgs(f[1], f[2:]); err != nil {
			t.Errorf("example %q does not parse: %v", line, err)
		}
	}
}

func TestHelpExitsZero(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}, {"help", "babysit"}, {"help", "install"}} {
		if code := run(args); code != 0 {
			t.Errorf("run(%v) = %d", args, code)
		}
	}
	if code := run([]string{"help", "nope"}); code != 2 {
		t.Errorf("help nope = %d, want 2", code)
	}
}

// The pages say what their command cannot do or is capped at.
func TestStopAndActivityPagesSayTheirLimits(t *testing.T) {
	for name, wants := range map[string][]string{
		"stop":     {"background copy", "canStop", "show --json"},
		"activity": {"500"},
	} {
		var b bytes.Buffer
		printCommandHelp(&b, name)
		flat := strings.Join(strings.Fields(b.String()), " ")
		for _, want := range wants {
			if !strings.Contains(flat, want) {
				t.Errorf("the %s page has no %q:\n%s", name, want, b.String())
			}
		}
	}
	var b bytes.Buffer
	printCommandHelp(&b, "stop")
	if !strings.Contains(strings.Join(strings.Fields(b.String()), " "), "Only a session with a background copy can be stopped") {
		t.Errorf("the stop page does not say which sessions can be stopped:\n%s", b.String())
	}
	b.Reset()
	printCommandHelp(&b, "activity")
	if !strings.Contains(strings.Join(strings.Fields(b.String()), " "), "newest 500 entries") {
		t.Errorf("the activity page does not say how far back it looks:\n%s", b.String())
	}
}

// The status page says its address carries the page's key, and every
// control page names the no-key error and that --url takes the address
// with the key.
func TestHelpNamesThePageKey(t *testing.T) {
	var b bytes.Buffer
	printCommandHelp(&b, "status")
	if page := b.String(); !strings.Contains(page, "includes the page's key") || !strings.Contains(page, "/?token=") {
		t.Errorf("the status page does not say its address carries the key:\n%s", page)
	}
	for name := range controlCommands {
		var h bytes.Buffer
		printCommandHelp(&h, name)
		for _, want := range []string{"no-key", "http://127.0.0.1:PORT/?token=KEY"} {
			if !strings.Contains(h.String(), want) {
				t.Errorf("the %s page has no %q", name, want)
			}
		}
	}
}

// Every control page warns that a key given with --url can be seen by
// other accounts while the command runs.
func TestHelpWarnsAboutAKeyOnTheCommandLine(t *testing.T) {
	for name := range controlCommands {
		var h bytes.Buffer
		printCommandHelp(&h, name)
		if !strings.Contains(h.String(), "other accounts") {
			t.Errorf("the %s page does not warn about a key given with --url", name)
		}
	}
}

// Every control page says a --url without a key is only for this
// account's own running copy, and that another copy, such as a demo, is
// reached with the full address it printed.
func TestHelpSaysABareURLIsOnlyForYourOwnCopy(t *testing.T) {
	for name := range controlCommands {
		var h bytes.Buffer
		printCommandHelp(&h, name)
		page := strings.Join(strings.Fields(h.String()), " ")
		for _, want := range []string{"only for your own running copy", "the full address it printed, with ?token="} {
			if !strings.Contains(page, want) {
				t.Errorf("the %s page has no %q:\n%s", name, want, h.String())
			}
		}
	}
}

// The usage and the control pages state how a word naming a session is
// read, and that a word fitting two sessions is refused.
func TestHelpStatesHowASessionWordIsRead(t *testing.T) {
	var u bytes.Buffer
	printUsage(&u)
	for _, want := range []string{
		"4 no such session or more than one.",
		"S is a session id, a short id, a name, or self.",
		"A full id always wins.",
		"A word that fits two sessions, as a short id or a name, is refused.",
	} {
		if !strings.Contains(u.String(), want) {
			t.Errorf("usage has no %q", want)
		}
	}
	if strings.Contains(u.String(), "beats a name") {
		t.Errorf("usage still says a short id beats a name")
	}
	if strings.Contains(u.String(), "4 no such session.") {
		t.Errorf("usage still says 4 is only no such session")
	}
	for _, name := range []string{"show", "babysit", "unbabysit", "retry", "stop", "activity"} {
		var b bytes.Buffer
		printCommandHelp(&b, name)
		flat := strings.Join(strings.Fields(b.String()), " ")
		if !strings.Contains(flat, "A full id always wins. A word that fits two sessions, as a short id or a name, is refused") {
			t.Errorf("%s page does not state the order", name)
		}
	}
}

// quit's refusal, as from an older running copy, is the action refusal
// document, which the page states.
func TestQuitHelpStatesItsRefusal(t *testing.T) {
	var b bytes.Buffer
	printCommandHelp(&b, "quit")
	if !strings.Contains(b.String(), "ok is false when it was refused, with exit code 1 and the reason in message.") {
		t.Fatalf("help quit:\n%s", b.String())
	}
}
