package supervise

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// fixture wires a Deps to fakes so a test can drive the supervisor and
// inspect exactly what it did. files is guarded by the mutex, because the
// observer's reader closure and a test both touch it, sometimes from inside
// a fake CLI response and sometimes from the supervisor's own goroutine.
type fixture struct {
	d *Deps

	mu    sync.Mutex
	files [][]byte

	p *procs.Fake
	r *claude.FakeRunner
}

func newFixture(t *testing.T, respond func(args []string) (string, error)) *fixture {
	f := &fixture{p: procs.NewFake()}
	f.r = claude.NewFakeRunner(respond)
	log, err := state.NewLog(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	obs := observe.New(f.readFiles, f.p, f.r, log)
	f.d = &Deps{
		Store: &state.Store{Dir: t.TempDir()}, Log: log, Obs: obs, Procs: f.p, Runner: f.r,
		Env: func() hosts.Env {
			return hosts.Env{Platform: "windows", CLIFound: true, CLIPresent: true, DesktopInstalled: true, VSCodeInstalled: true}
		},
		ProjectsDir: t.TempDir(), Now: time.Now, Backoff: 10 * time.Second, StartupGrace: -1,
		Home: "/home/dev", ClaudeConfig: filepath.Join(t.TempDir(), "claude.json"),
		RCSettleTimeout: 200 * time.Millisecond,
	}
	return f
}

// setFiles replaces the session files the observer reads.
func (f *fixture) setFiles(files ...[]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append([][]byte{}, files...)
}

// addFile appends one more session file to what the observer reads.
func (f *fixture) addFile(file []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files = append(f.files, file)
}

func (f *fixture) readFiles() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.files))
	copy(out, f.files)
	return out
}

// writeTranscript creates the synthetic transcript file the promise
// requires before it will resume a session, at the path Claude itself
// would use for id started in cwd.
func (f *fixture) writeTranscript(t *testing.T, cwd, id string) {
	t.Helper()
	dir := filepath.Join(f.d.ProjectsDir, claude.Slug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"type":"summary","summary":"demo"}` + "\n")
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), line, 0o644); err != nil {
		t.Fatal(err)
	}
}

// bgSession builds a background session file with a chosen bridge session
// id, empty meaning remote control off.
func bgSession(pid int, id, bridge string) []byte {
	return []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"%s","cwd":"/home/dev/ws","procStart":"7","kind":"bg","entrypoint":"cli","bridgeSessionId":"%s"}`, pid, id, bridge))
}

// desktopSession builds a desktop session file with Remote Control on.
func desktopSession(pid int, id string) []byte {
	return []byte(fmt.Sprintf(`{"pid":%d,"sessionId":"%s","cwd":"/home/dev/ws","procStart":"7","kind":"interactive","entrypoint":"claude-desktop","bridgeSessionId":"b"}`, pid, id))
}

func TestResumeBackgroundFlagsAndCopyRecovery(t *testing.T) {
	id := "dddddddd-0000-4000-8000-000000000001"
	calls := 0
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "--remote-control"):
			calls++
			return "note: background session dddddddd keeps its own saved options, so the flags you passed started a copy as aabbccdd.\nbackgrounded \u00b7 aabbccdd", nil
		case strings.HasPrefix(a, "--bg --resume"):
			calls++
			return "note: woke session dddddddd with its saved options.\nbackgrounded \u00b7 dddddddd", nil
		}
		return "ok", nil
	})
	f.d.SettleTimeout = 50 * time.Millisecond
	short, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", false)
	if !res.OK || short != "dddddddd" {
		t.Fatalf("%+v %q", res, short)
	}
	joined := strings.Join(f.r.CallList(), "\n")
	if !strings.Contains(joined, "stop aabbccdd") || !strings.Contains(joined, "rm aabbccdd") {
		t.Fatalf("copy must be stopped and removed:\n%s", joined)
	}
	if calls != 2 || !strings.Contains(res.Message, "after a second try without flags") {
		t.Fatalf("flagged attempt then flagless recovery: %d %q", calls, res.Message)
	}
	if strings.Contains(joined, "--name") {
		t.Fatalf("a resume never passes a name:\n%s", joined)
	}
}

func TestResumeBackgroundFailsHonestly(t *testing.T) {
	id := "eeeeeeee-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) { return "error: nope", errors.New("exit 1") })
	_, res := f.d.resumeBackground(context.Background(), id, "", "/home/dev/ws", true)
	if res.OK || !strings.Contains(res.Message, "claude --bg --resume "+id) {
		t.Fatalf("%+v", res)
	}
}

func TestResumeBackgroundInvalidIDNeverCallsRunner(t *testing.T) {
	f := newFixture(t, func([]string) (string, error) { return "ok", nil })
	_, res := f.d.resumeBackground(context.Background(), "not-a-valid-id", "n", "/home/dev/ws", false)
	if res.OK || !strings.Contains(res.Message, "not a valid session id") {
		t.Fatalf("%+v", res)
	}
	if len(f.r.CallList()) != 0 {
		t.Fatalf("runner must not be called: %v", f.r.CallList())
	}
}

func TestResumeBackgroundRefusesCopyThatLooksLikeOriginal(t *testing.T) {
	id := "66666666-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) {
		return "note: started a copy as 66666666.\nbackgrounded \u00b7 66666666", nil
	})
	_, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", false)
	if res.OK {
		t.Fatalf("must not succeed when the reported copy looks like the original: %+v", res)
	}
	if len(f.r.CallList()) != 1 {
		t.Fatalf("only the initial resume call should run, nothing must be stopped or removed: %v", f.r.CallList())
	}
	if !strings.Contains(res.Message, "claude --bg --resume "+id) {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Message, "66666666") {
		t.Fatalf("message must name the short id it refused to touch: %q", res.Message)
	}
}

func TestResumeBackgroundLowercasesUppercaseCopyID(t *testing.T) {
	id := "77777777-0000-4000-8000-000000000001"
	calls := 0
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "--remote-control"):
			calls++
			return "note: started a copy as AABBCCDD.\nbackgrounded \u00b7 AABBCCDD", nil
		case strings.HasPrefix(a, "--bg --resume"):
			calls++
			return "note: woke session 77777777 with its saved options.\nbackgrounded \u00b7 77777777", nil
		}
		return "ok", nil
	})
	f.d.SettleTimeout = 50 * time.Millisecond
	short, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", false)
	if !res.OK || short != "77777777" {
		t.Fatalf("%+v %q", res, short)
	}
	joined := strings.Join(f.r.CallList(), "\n")
	if !strings.Contains(joined, "stop aabbccdd") || !strings.Contains(joined, "rm aabbccdd") {
		t.Fatalf("the copy must be stopped and removed using its lowercase form:\n%s", joined)
	}
	if strings.Contains(joined, "AABBCCDD") {
		t.Fatalf("the uppercase form must never reach the runner:\n%s", joined)
	}
	if calls != 2 {
		t.Fatalf("expected a flagged attempt then a flagless retry: %d", calls)
	}
}

func TestResumeBackgroundRemoteControlOn(t *testing.T) {
	id := "22222222-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.r.SetRespond(func([]string) (string, error) {
		f.p.SetAlive(7, true)
		f.addFile(bgSession(7, id, "bridge-a"))
		return "note: woke session 22222222 with its saved options.\nbackgrounded \u00b7 22222222", nil
	})
	f.d.SettleTimeout = 50 * time.Millisecond
	short, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", true)
	if !res.OK || short != "22222222" || !strings.HasSuffix(res.Message, "Remote Control is on.") {
		t.Fatalf("%+v", res)
	}
}

func TestResumeBackgroundRemoteControlOff(t *testing.T) {
	id := "33333333-0000-4000-8000-000000000001"
	f := newFixture(t, nil)
	f.r.SetRespond(func([]string) (string, error) {
		f.p.SetAlive(8, true)
		f.addFile(bgSession(8, id, ""))
		return "note: woke session 33333333 with its saved options.\nbackgrounded \u00b7 33333333", nil
	})
	f.d.SettleTimeout = 50 * time.Millisecond
	short, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", true)
	want := "Remote Control is off: attach with `claude attach " + short + "` and type /rc."
	if !res.OK || !strings.HasSuffix(res.Message, want) {
		t.Fatalf("%+v", res)
	}
}

func TestResumeBackgroundRemoteControlNotYetKnown(t *testing.T) {
	id := "44444444-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) {
		return "note: woke session 44444444 with its saved options.\nbackgrounded \u00b7 44444444", nil
	})
	f.d.SettleTimeout = 50 * time.Millisecond
	_, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", true)
	if !res.OK || !strings.HasSuffix(res.Message, "Remote Control is not connected yet. The card shows when it is.") {
		t.Fatalf("%+v", res)
	}
}

func TestTranscriptSaved(t *testing.T) {
	f := newFixture(t, nil)
	id := "45454545-0000-4000-8000-000000000001"
	if got := f.d.transcriptSaved("/home/dev/ws", id); got != savedNone {
		t.Fatalf("no transcript was written yet: got %v", got)
	}
	f.writeTranscript(t, "/home/dev/ws", id)
	if got := f.d.transcriptSaved("/home/dev/ws", id); got != savedThere {
		t.Fatalf("transcript was written: got %v", got)
	}
}

// A look that could not be finished says nothing either way.
func TestTranscriptSavedUnknownWhenAFolderCannotBeRead(t *testing.T) {
	f := newFixture(t, nil)
	lockProjects(t, f)
	if got := f.d.transcriptSaved("/home/dev/ws", "45454545-0000-4000-8000-000000000002"); got != savedUnknown {
		t.Fatalf("got %v", got)
	}
}

// lockProjects makes the fixture's projects folder unreadable until the
// test ends, as a folder whose permissions were changed would be.
func lockProjects(t *testing.T, f *fixture) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("folder permissions do not stop this user here")
	}
	if err := os.Chmod(f.d.ProjectsDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.d.ProjectsDir, 0o755) })
}

func TestResumeBackgroundRefusesCopyThatLooksLikeAnUpperCaseOriginal(t *testing.T) {
	// The session id is written in upper case and the copy the CLI names is
	// the session's own short id in lower case: the two must still be
	// recognised as the same session.
	id := "AAAAAAAA-0000-4000-8000-000000000001"
	f := newFixture(t, func([]string) (string, error) {
		return "note: started a copy as aaaaaaaa.\nbackgrounded \u00b7 aaaaaaaa", nil
	})
	_, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", false)
	if res.OK {
		t.Fatalf("a copy id equal to the original must never succeed: %+v", res)
	}
	if len(f.r.CallList()) != 1 {
		t.Fatalf("an id written in upper case must not defeat the guard: %v", f.r.CallList())
	}
	if !strings.Contains(res.Message, "claude --bg --resume "+id) {
		t.Fatalf("%+v", res)
	}
}

// A resume the CLI refuses because the session is running already is not a
// failure of anything. Starting a second copy would be the one outcome
// nobody wants, so the answer is to say where it is running.
func TestResumeBackgroundSaysTheSessionIsAlreadyRunning(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555525"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "--bg --resume") {
			f.p.SetAlive(7, true)
			f.addFile(desktopSession(7, id))
			return "error: session 11111111 is already running in the background; run claude attach 11111111", nil
		}
		return "", nil
	})
	short, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", true)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if res.Message != "the session is already running in the desktop app, nothing to resume" {
		t.Fatalf("%q", res.Message)
	}
	if res.AlreadyLiveIn != claude.HostDesktop {
		t.Fatalf("the result must name the host: %+v", res)
	}
	if short != "11111111" {
		t.Fatalf("%q", short)
	}
}

// The same refusal with nothing actually running is still a failure, and it
// must read like one rather than pointing at a session nobody can find.
func TestResumeBackgroundStillFailsWhenNothingIsRunning(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555526"
	f := newFixture(t, func([]string) (string, error) {
		return "error: session 11111111 is already running in the background", nil
	})
	_, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", true)
	if res.OK || res.AlreadyLiveIn != "" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Message, "claude --bg --resume "+id) {
		t.Fatalf("%q", res.Message)
	}
}

// A resume that asked for Remote Control is given the longer window, and a
// bridge that has not arrived by the end of it is never reported as off.
func TestRemoteControlIsNeverCalledOffWhenItWasAskedFor(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555527"
	f := newFixture(t, nil)
	f.r.SetRespond(func(args []string) (string, error) {
		if strings.HasPrefix(strings.Join(args, " "), "--bg --resume") {
			f.p.SetAlive(8, true)
			f.addFile(bgSession(8, id, ""))
			f.d.Obs.RefreshNow()
		}
		return "note: woke session 11111111 with its saved options.\nbackgrounded \u00b7 11111111", nil
	})
	_, res := f.d.resumeBackground(context.Background(), id, "n", "/home/dev/ws", false)
	if !res.OK {
		t.Fatalf("%+v", res)
	}
	if !strings.HasSuffix(res.Message, "Remote Control is not connected yet. The card shows when it is.") {
		t.Fatalf("%q", res.Message)
	}
}

// The CLI ends its own sentence with a full stop, and the message around it
// adds one of its own. Only one of them may reach the page.
func TestAResumeFailureHasOneFullStop(t *testing.T) {
	id := "eeeeeeee-0000-4000-8000-000000000002"
	f := newFixture(t, func([]string) (string, error) {
		return "Workspace not trusted. Run claude in the project directory.", errors.New("exit 1")
	})
	_, res := f.d.resumeBackground(context.Background(), id, "", "/home/dev/ws", true)
	if res.OK {
		t.Fatalf("%+v", res)
	}
	if strings.Contains(res.Message, "..") {
		t.Fatalf("a doubled full stop: %q", res.Message)
	}
	if !strings.Contains(res.Message, "project directory. Run by hand") {
		t.Fatalf("%q", res.Message)
	}
}

// The command offered to run by hand starts in the session's own folder,
// quoted for the shell of the OS it runs on, since a resume only finds the
// conversation from there. The quoting itself is tested in hosts.
func TestTheResumeToRunByHandStartsInTheFolder(t *testing.T) {
	id := "eeeeeeee-0000-4000-8000-000000000003"
	f := newFixture(t, func([]string) (string, error) { return "", errors.New("exit 1") })
	cwd := "/home/dev/my ws/it's"
	_, res := f.d.resumeBackground(context.Background(), id, "", cwd, true)
	want := "Run by hand: " + hosts.BackgroundResumeCommandIn(cwd, id)
	if res.OK || !strings.HasSuffix(res.Message, want) {
		t.Fatalf("got %q, want it to end with %q", res.Message, want)
	}
}

// A forked copy that cannot be stopped is given as two commands, one after
// the other: Windows PowerShell 5.1 cannot run commands joined with &&.
func TestAForkedCopyThatCannotBeStoppedIsGivenAsTwoCommands(t *testing.T) {
	r := claude.NewFakeRunner(func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "stop" {
			return "", errors.New("no such session")
		}
		return "", nil
	})
	d := &Deps{Runner: r}
	res, ok := d.stopCopyOrRefuse(context.Background(), "abcd1234", "11112222-3333-4444-5555-666677778888", t.TempDir(), "11112222")
	if ok {
		t.Fatal("a copy that could not be stopped is not ok")
	}
	if strings.Contains(res.Message, "&&") {
		t.Fatalf("the commands must not be joined with &&: %q", res.Message)
	}
	if !strings.Contains(res.Message, "run `claude stop abcd1234`, then `claude rm abcd1234`") {
		t.Fatalf("the message must give both commands in order: %q", res.Message)
	}
}
