package supervise

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
)

// startFixture is a supervisor on a machine with no display, with a
// project folder, the CLI's settings file and a fake trust answerer.
type startFixture struct {
	f       *fixture
	s       *Supervisor
	project string
	trusted map[string]bool
	accept  []string
	// acceptDoes is what the fake answerer does: by default it records the
	// trust in the settings file, as the CLI does after Yes.
	acceptDoes func(dir string) error
}

func newStartFixture(t *testing.T, headless bool, respond func(args []string) (string, error)) *startFixture {
	t.Helper()
	sf := &startFixture{trusted: map[string]bool{}}
	sf.f = newFixture(t, respond)
	sf.project = filepath.Join(t.TempDir(), "shop-api")
	if err := os.Mkdir(sf.project, 0o755); err != nil {
		t.Fatal(err)
	}
	sf.f.d.Env = func() hosts.Env {
		return hosts.Env{Platform: "linux", CLIFound: true, CLIPresent: true, Headless: headless}
	}
	sf.acceptDoes = func(dir string) error { sf.trust(t, dir); return nil }
	sf.f.d.AcceptTrust = func(_ context.Context, dir string) error {
		sf.accept = append(sf.accept, dir)
		return sf.acceptDoes(dir)
	}
	sf.writeConfig(t)
	sf.s = New(*sf.f.d)
	return sf
}

// trust records dir as trusted in the CLI's settings file.
func (sf *startFixture) trust(t *testing.T, dir string) {
	sf.trusted[dir] = true
	sf.writeConfig(t)
}

func (sf *startFixture) writeConfig(t *testing.T) {
	t.Helper()
	projects := map[string]any{}
	for dir := range sf.trusted {
		projects[filepath.ToSlash(dir)] = map[string]any{"hasTrustDialogAccepted": true}
	}
	b, err := json.Marshal(map[string]any{"projects": projects})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sf.f.d.ClaudeConfig, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startsBackground(args []string) bool {
	return strings.Join(args, " ") == "--bg --remote-control"
}

// Starting a session is offered only on a machine with no display.
func TestStartRefusedWithADisplay(t *testing.T) {
	sf := newStartFixture(t, false, nil)
	res := sf.s.Start(sf.project, false, ViaPage)
	if res.OK || res.Message != StartRefusedWithDisplay {
		t.Fatalf("%+v", res)
	}
	if len(sf.f.r.CallList()) != 0 || len(sf.accept) != 0 {
		t.Fatalf("ran %v, accepted %v", sf.f.r.CallList(), sf.accept)
	}
}

// A folder that is not an absolute path to a folder, or is the home
// folder, is refused with the reason, and nothing is run.
func TestStartRefusesFoldersItCannotUse(t *testing.T) {
	sf := newStartFixture(t, true, nil)
	file := filepath.Join(sf.project, "notes.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sf.f.d.Home = sf.project
	sf.s = New(*sf.f.d)
	for _, c := range []struct{ path, want string }{
		{"", "Name the folder to start the session in."},
		{"shop-api", "Give the folder's full path, starting with /."},
		{filepath.Join(sf.project, "missing"), "There is no folder at " + filepath.Join(sf.project, "missing") + "."},
		{file, file + " is a file, not a folder."},
		{sf.project, "Claude Code does not start background sessions in the home folder. Choose a project folder."},
	} {
		res := sf.s.Start(c.path, true, ViaPage)
		if res.OK || res.Message != c.want {
			t.Errorf("%q: %+v, want %q", c.path, res, c.want)
		}
	}
	if len(sf.f.r.CallList()) != 0 || len(sf.accept) != 0 {
		t.Fatalf("ran %v, accepted %v", sf.f.r.CallList(), sf.accept)
	}
}

// A folder the CLI does not trust yet is not trusted without the
// person's yes: the answer asks for it, and nothing is run.
func TestStartAsksBeforeTrusting(t *testing.T) {
	sf := newStartFixture(t, true, nil)
	res := sf.s.Start(sf.project, false, ViaPage)
	if res.OK || !res.NeedsTrust || res.Message != TrustQuestion(sf.project) {
		t.Fatalf("%+v", res)
	}
	if len(sf.f.r.CallList()) != 0 || len(sf.accept) != 0 {
		t.Fatalf("ran %v, accepted %v", sf.f.r.CallList(), sf.accept)
	}
}

// With the person's yes, the folder is trusted through the CLI's own
// question, the session is started with Remote Control, and Activity says
// both.
func TestStartTrustsThenStarts(t *testing.T) {
	sf := newStartFixture(t, true, func(args []string) (string, error) {
		if startsBackground(args) {
			return "backgrounded \u00b7 1a2b3c4d \u00b7 shop-api (claude attach 1a2b3c4d)", nil
		}
		return "[]", nil
	})
	res := sf.s.Start(sf.project, true, ViaPage)
	if !res.OK || res.ShortID != "1a2b3c4d" || res.Message != "Started a new session in "+sf.project+" as background session 1a2b3c4d, with Remote Control. It is babysat." {
		t.Fatalf("%+v", res)
	}
	if strings.Join(sf.accept, " ") != sf.project {
		t.Fatalf("accepted %v", sf.accept)
	}
	calls, cwds := sf.f.r.CallList(), sf.f.r.CwdList()
	if len(calls) != 1 || calls[0] != "--bg --remote-control" || cwds[0] != sf.project {
		t.Fatalf("calls %q in %q", calls, cwds)
	}
	got := strings.Join(logMessages(sf.f.d.Log), "\n")
	for _, want := range []string{"trusted the folder for Claude Code, as asked", "started background session 1a2b3c4d with Remote Control"} {
		if !strings.Contains(got, want) {
			t.Errorf("Activity has no %q:\n%s", want, got)
		}
	}
}

// A folder the CLI trusts already is started without its question.
func TestStartInATrustedFolder(t *testing.T) {
	sf := newStartFixture(t, true, func(args []string) (string, error) {
		return "backgrounded \u00b7 1a2b3c4d \u00b7 shop-api", nil
	})
	sf.trust(t, sf.project)
	if res := sf.s.Start(sf.project, false, ViaCLI); !res.OK {
		t.Fatalf("%+v", res)
	}
	if len(sf.accept) != 0 {
		t.Fatalf("asked for trust in a trusted folder: %v", sf.accept)
	}
}

// When the CLI's question cannot be answered, or the folder is still not
// trusted after it, nothing is started and the one-time command is given.
func TestStartGivesTheCommandWhenTrustFails(t *testing.T) {
	for _, c := range []struct {
		name string
		does func(string) error
	}{
		{"not asked", func(string) error { return claude.ErrTrustNotAsked }},
		{"no Yes", func(string) error { return claude.ErrTrustNoYes }},
		{"answered but not recorded", func(string) error { return nil }},
	} {
		sf := newStartFixture(t, true, nil)
		sf.acceptDoes = c.does
		res := sf.s.Start(sf.project, true, ViaPage)
		if res.OK || res.NeedsTrust || !strings.Contains(res.Message, hosts.TrustCommandIn(sf.project)) {
			t.Errorf("%s: %+v", c.name, res)
		}
		if len(sf.f.r.CallList()) != 0 {
			t.Errorf("%s: started anyway: %v", c.name, sf.f.r.CallList())
		}
	}
}

// When the CLI does not say it started a background session, the answer
// says what it said and how to start one by hand.
func TestStartSaysWhenTheCLIDidNotStart(t *testing.T) {
	sf := newStartFixture(t, true, func([]string) (string, error) {
		return "Error: Workspace not trusted", errors.New("exit status 1")
	})
	sf.trust(t, sf.project)
	res := sf.s.Start(sf.project, false, ViaPage)
	if res.OK || !strings.Contains(res.Message, "Workspace not trusted") || !strings.Contains(res.Message, "claude --bg --remote-control") {
		t.Fatalf("%+v", res)
	}
}

// The session started is babysat when it shows up, even with "Babysit
// every new background session" off.
func TestStartedSessionIsBabysat(t *testing.T) {
	id := "1a2b3c4d-0000-4000-8000-000000000001"
	sf := newStartFixture(t, true, func(args []string) (string, error) {
		return "backgrounded \u00b7 1a2b3c4d \u00b7 shop-api", nil
	})
	sf.trust(t, sf.project)
	sf.s.st.Settings.AutoBabysit = false
	sf.s.reconcileForTest(observe.Snapshot{At: time.Now()})
	if res := sf.s.Start(sf.project, false, ViaPage); !res.OK {
		t.Fatalf("%+v", res)
	}
	sf.f.p.SetAlive(21, true)
	sf.f.setFiles(sess(21, id, "background", "cli", "1a2b3c4d"))
	sf.f.d.Obs.RefreshNow()
	sf.s.reconcileForTest(sf.f.d.Obs.Current())
	if _, ok := sf.s.watchForTest(id); !ok {
		t.Fatal("the session started was not babysat")
	}
}
