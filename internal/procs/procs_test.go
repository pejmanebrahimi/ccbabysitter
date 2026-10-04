package procs

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// invented100nsTicks is a made up FILETIME, a round number rather than
// anything read off a real machine; the milliseconds it stands for are
// worked out here rather than written down beside it.
const invented100nsTicks = int64(134000000000000000)

func TestMatchStartWindowsFiletime(t *testing.T) {
	ticks := itoa(invented100nsTicks)
	ms := (invented100nsTicks - filetimeEpochTicks) / 10000

	if MatchStart(ms, ticks) != Matches {
		t.Fatal("exact filetime must match")
	}
	if MatchStart(ms+5000, ticks) != Mismatch {
		t.Fatal("5 s off is another process")
	}
	if MatchStart(ms, "") != Unknown {
		t.Fatal("empty procStart is unknown")
	}
	if MatchStart(ms, "garbage") != Unknown {
		t.Fatal("unparseable procStart is unknown")
	}
}

func TestMatchStartUnixForms(t *testing.T) {
	now := time.Now().UnixMilli()
	if MatchStart(now, itoa(now)) != Matches {
		t.Fatal("unix ms")
	}
	if MatchStart(now, itoa(now/1000)) != Matches {
		t.Fatal("unix seconds")
	}
	if MatchStart(now, itoa(now-3600_000)) != Mismatch {
		t.Fatal("an hour off is another process")
	}
	if MatchStart(now, "1") != Unknown {
		t.Fatal("an integer fitting no known form is unknown, not a match")
	}
}

// The unit a number is read in comes from its magnitude, so a value that
// does name a date and disagrees is a real Mismatch, not a value that gets
// another chance under a different unit. A millisecond value read as
// seconds, or the other way round, would otherwise land thousands of years
// away and be called Unknown, which lets the process be ended on the
// strength of the name check alone.
func TestMatchStartChoosesTheUnitByMagnitude(t *testing.T) {
	now := time.Now().UnixMilli()
	sec := now / 1000

	if got := MatchStart(now, itoa(sec)); got != Matches {
		t.Fatalf("a second value against its own creation time: %v", got)
	}
	if got := MatchStart(now, itoa(now/1000/1000)); got != Unknown {
		t.Fatalf("a number below every known range names no date: %v", got)
	}
	// A millisecond value compared against a creation time a thousand times
	// smaller is a definite disagreement about a date, not an unknown.
	if got := MatchStart(sec, itoa(now)); got != Mismatch {
		t.Fatalf("a millisecond value that disagrees is a mismatch: %v", got)
	}
	if got := MatchStart(now, itoa(invented100nsTicks)); got != Mismatch {
		t.Fatalf("a filetime that disagrees is a mismatch: %v", got)
	}
}

// Linux has no creation time to read: it reports the ticks since boot at
// which a process started, and turning that into a wall clock time means
// adding a boot time that is itself only known to the second. Two readings
// of the same process can land up to about a second apart whichever unit
// they were written down in. Anything tighter would declare every such
// session a different process, empty the page, and stop every watch
// from ever resuming.
func TestMatchStartUnixFormsTolerateSubSecondRounding(t *testing.T) {
	sec := time.Now().Unix()
	seconds := itoa(sec)
	for _, offset := range []int64{0, 400, 900, -400, -900} {
		if got := MatchStart(sec*1000+offset, seconds); got != Matches {
			t.Errorf("seconds, %d ms away: %v", offset, got)
		}
	}
	if got := MatchStart(sec*1000+5000, seconds); got != Mismatch {
		t.Errorf("seconds, five seconds away is another process: %v", got)
	}

	ms := sec * 1000
	milliseconds := itoa(ms)
	for _, offset := range []int64{0, 400, 900, -400, -900} {
		if got := MatchStart(ms+offset, milliseconds); got != Matches {
			t.Errorf("milliseconds, %d ms away: %v", offset, got)
		}
	}
	if got := MatchStart(ms+5000, milliseconds); got != Mismatch {
		t.Errorf("milliseconds, five seconds away is another process: %v", got)
	}
}

// looksLikeClaudeProgram is the only thing between an unconfirmed identity
// and a process being ended, so what it accepts and what it refuses are
// both pinned down here.
func TestLooksLikeClaudeProgram(t *testing.T) {
	cases := []struct {
		what string
		id   programIdentity
		want bool
	}{
		{
			"a versioned unix install",
			programIdentity{Name: "claude", Exe: "/Users/dev/.local/share/claude/versions/2.1.269"},
			true,
		},
		{
			"a versioned unix install whose name says nothing",
			programIdentity{Name: "2.1.269", Exe: "/Users/dev/.local/share/claude/versions/2.1.269"},
			true,
		},
		{
			"a windows install",
			programIdentity{Name: "claude.exe", Exe: `C:\Users\dev\AppData\Roaming\Claude\claude-code\2.1.275\claude.exe`},
			true,
		},
		{
			"the macos application bundle",
			programIdentity{Name: "Claude", Exe: "/Applications/Claude.app/Contents/MacOS/Claude"},
			true,
		},
		{
			"an interpreter pointed at the entry script",
			programIdentity{Name: "node", Exe: "/usr/bin/node", Argv: []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}},
			true,
		},
		{
			"an interpreter pointed at a package manager's own shim",
			programIdentity{Name: "node", Exe: "/usr/bin/node", Argv: []string{"node", "/usr/local/bin/claude"}},
			true,
		},
		{
			"the same shim on windows",
			programIdentity{Name: "node.exe", Exe: `C:\Program Files\nodejs\node.exe`, Argv: []string{"node.exe", `C:\Users\dev\AppData\Roaming\npm\claude`}},
			true,
		},
		{
			"an interpreter pointed at a file that merely lives under a claude folder",
			programIdentity{Name: "node", Exe: "/usr/bin/node", Argv: []string{"node", "/home/dev/.claude/x.js"}},
			false,
		},
		{
			"an unrelated program merely reading a claude file",
			programIdentity{Name: "tail", Exe: "/usr/bin/tail", Argv: []string{"/usr/bin/tail", "-f", "/home/dev/.claude/logs/x"}},
			false,
		},
		{
			"a program that happens to live under a claude folder",
			programIdentity{Name: "procs.test", Exe: "/home/dev/.claude/worktrees/x/procs.test", Argv: []string{"/home/dev/.claude/worktrees/x/procs.test"}},
			false,
		},
		{
			"a program whose name merely contains the word",
			programIdentity{Name: "claudette", Exe: "/usr/local/bin/claudette"},
			false,
		},
		{
			"an interpreter pointed at something else entirely",
			programIdentity{Name: "node", Exe: "/usr/bin/node", Argv: []string{"node", "/home/dev/.claude/hooks/notify.js"}},
			false,
		},
		{
			"an unrelated program with the entry script only in a later argument",
			programIdentity{Name: "grep", Exe: "/usr/bin/grep", Argv: []string{"/usr/bin/grep", "-r", "claude-code/cli"}},
			false,
		},
		{
			"nothing known at all",
			programIdentity{},
			false,
		},
	}
	for _, c := range cases {
		if got := looksLikeClaudeProgram(c.id); got != c.want {
			t.Errorf("%s: looksLikeClaudeProgram(%+v) = %v, want %v", c.what, c.id, got, c.want)
		}
	}
}

func TestMatchStartMacLayoutUTC(t *testing.T) {
	when := time.Date(2026, 9, 11, 11, 15, 32, 0, time.UTC)
	s := when.Format(macLayout)
	createMs := when.UnixMilli()

	if MatchStart(createMs, s) != Matches {
		t.Fatal("utc rendering must match its own creation time")
	}

	off := createMs + 3600_000
	if MatchStart(off, s) != Mismatch {
		_, offsetSec := when.In(time.Local).Zone()
		if offsetSec != -3600 {
			t.Fatal("one hour off from the utc rendering must mismatch")
		}
	}
}

func TestMatchStartMacLayoutLocalRetry(t *testing.T) {
	_, offsetSec := time.Now().Zone()
	if offsetSec == 0 {
		t.Skip("local timezone is UTC, so this machine cannot exercise the retry")
	}

	// The rendered text carries no zone. Interpreting it as UTC first
	// will miss by the local offset, so MatchStart must retry it as
	// local time before giving up.
	local := time.Date(2026, 9, 11, 11, 15, 32, 0, time.Local)
	s := local.Format(macLayout)
	createMs := local.UnixMilli()

	if MatchStart(createMs, s) != Matches {
		t.Fatal("local retry must match the same wall clock reading")
	}
}

func TestFakeTerminateRemovesPid(t *testing.T) {
	f := NewFake()
	f.SetAlive(5, true)

	if !f.Terminate(5, "x") || f.IsAlive(5) {
		t.Fatal("fake terminate")
	}
	calls := f.CallList()
	if len(calls) != 1 || calls[0] != "terminate 5" {
		t.Fatalf("%v", calls)
	}
}

func TestFakeRefuseTerminate(t *testing.T) {
	f := NewFake()
	f.SetAlive(5, true)
	f.SetRefuse(true)

	if f.Terminate(5, "x") || !f.IsAlive(5) {
		t.Fatal("a refusing fake must not remove the pid")
	}
}

func TestFakeStatsAndCreateTime(t *testing.T) {
	f := NewFake()
	want := TreeStats{RSS: 42, Processes: 1, PIDs: []int{5}}
	f.SetStats(5, want)
	f.SetCreateTime(5, 1000)
	f.SetAlive(5, true)

	got, ok := f.Tree(5)
	if !ok || got.RSS != want.RSS || got.Processes != want.Processes || len(got.PIDs) != len(want.PIDs) {
		t.Fatalf("tree: %+v %v", got, ok)
	}
	if ct, ok := f.CreateTime(5); !ok || ct != 1000 {
		t.Fatalf("create time: %v %v", ct, ok)
	}
	if _, ok := f.CreateTime(999); ok {
		t.Fatal("create time for an unset pid must report false")
	}
	if !f.Exists(5, 1000) {
		t.Fatal("exists must match a stored creation time")
	}
	if f.Exists(5, 2000) {
		t.Fatal("exists must reject a creation time that does not match what was stored")
	}

	f.SetAlive(6, true)
	if !f.Exists(6, 12345) {
		t.Fatal("exists without a stored creation time must behave like IsAlive")
	}
}

func TestRealOwnProcess(t *testing.T) {
	r := NewReal()
	pid := os.Getpid()

	if !r.Alive(pid, "") {
		t.Fatal("own pid must be alive with an unknown start time")
	}

	ct, ok := r.CreateTime(pid)
	if !ok {
		t.Fatal("own create time")
	}

	wrongStart := itoa(ct - 3600_000)
	if r.Alive(pid, wrongStart) {
		t.Fatal("a start time an hour off must not match")
	}
	if !r.Terminate(pid, wrongStart) {
		t.Fatal("a definite mismatch is reported as already gone")
	}
	if !r.Alive(pid, "") {
		t.Fatal("a mismatched terminate must never touch the real process")
	}

	if r.Terminate(pid, "") {
		t.Fatal("an unknown identity on our own test process must refuse to terminate")
	}
	if !r.Alive(pid, "") {
		t.Fatal("a refused terminate must never touch the process")
	}

	if r.Alive(1<<30, "") {
		t.Fatal("a bogus pid must not be alive")
	}

	st, ok := r.Tree(pid)
	if !ok || st.RSS == 0 {
		t.Fatalf("tree stats: %+v %v", st, ok)
	}
	if st.Processes == 0 || len(st.PIDs) == 0 {
		t.Fatalf("tree pids: %+v", st)
	}
}

func TestRealChildProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a posix sleep binary")
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	childPid := cmd.Process.Pid

	r := NewReal()
	r.GraceWait = 300 * time.Millisecond
	r.KillWait = 300 * time.Millisecond

	if !r.Alive(childPid, "") {
		t.Fatal("child must be alive")
	}

	ct, ok := r.CreateTime(childPid)
	if !ok {
		t.Fatal("child create time")
	}
	if !r.Exists(childPid, ct) {
		t.Fatal("child must exist at its own creation time")
	}

	selfStats, ok := r.Tree(os.Getpid())
	if !ok {
		t.Fatal("own tree")
	}
	found := false
	for _, p := range selfStats.PIDs {
		if p == childPid {
			found = true
		}
	}
	if !found || selfStats.Processes < 2 {
		t.Fatalf("expected the child pid in our own tree, got %+v", selfStats)
	}

	if !r.Terminate(childPid, itoa(ct)) {
		t.Fatal("terminate child")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	if r.Alive(childPid, "") {
		t.Fatal("child should be gone")
	}
}

func TestTerminateReapsPastZombie(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a posix sleep binary; windows has no zombie state")
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	childPid := cmd.Process.Pid
	// Deliberately left unreaped until the very end of the test: once
	// Terminate ends it, the child lingers as a zombie (already exited,
	// but still occupying a pid entry) until something calls Wait.
	defer func() { _ = cmd.Wait() }()

	r := NewReal()
	r.GraceWait = 500 * time.Millisecond
	r.KillWait = 500 * time.Millisecond

	ct, ok := r.CreateTime(childPid)
	if !ok {
		t.Fatal("child create time")
	}

	if !r.Terminate(childPid, itoa(ct)) {
		t.Fatal("terminate must report success for a process that exited, zombie or not")
	}
	if r.Alive(childPid, "") {
		t.Fatal("a zombie must not be reported alive")
	}
}

func TestVerifiedChildRejectsWrongParent(t *testing.T) {
	pid := os.Getpid()
	realPpid := os.Getppid()

	if _, ok := verifiedChild(pid, realPpid); !ok {
		t.Fatal("the process's real parent must verify")
	}
	if _, ok := verifiedChild(pid, realPpid+999_999); ok {
		t.Fatal("a stale recorded parent must be rejected")
	}
	if _, ok := verifiedChild(1<<30, realPpid); ok {
		t.Fatal("a nonexistent pid must be rejected")
	}
}

func TestTreeCPUPercentSecondSampleIsNonNegative(t *testing.T) {
	r := NewReal()
	pid := os.Getpid()

	st1, ok := r.Tree(pid)
	if !ok {
		t.Fatal("first tree")
	}
	if st1.CPUPercent != 0 {
		t.Fatalf("the first sample must be zero, got %v", st1.CPUPercent)
	}

	end := time.Now().Add(200 * time.Millisecond)
	sum := 0
	for time.Now().Before(end) {
		sum++
	}
	_ = sum

	st2, ok := r.Tree(pid)
	if !ok {
		t.Fatal("second tree")
	}
	if st2.CPUPercent < 0 {
		t.Fatalf("cpu percent must never be negative: %v", st2.CPUPercent)
	}
}

func TestExeContains(t *testing.T) {
	r := NewReal()
	if !r.ExeContains("") {
		t.Fatal("an empty substring should match any executable path")
	}
	if r.ExeContains("this-should-not-exist-in-any-real-path-xyz123") {
		t.Fatal("a nonsense substring should not match")
	}
}

func TestRealParentOfThisProcess(t *testing.T) {
	ppid, ok := NewReal().Parent(os.Getpid())
	if !ok || ppid != os.Getppid() {
		t.Fatalf("Parent = %d, %v; want %d", ppid, ok, os.Getppid())
	}
}

func TestFakeParent(t *testing.T) {
	f := NewFake()
	if _, ok := f.Parent(5); ok {
		t.Fatal("unset parent reported")
	}
	f.SetParent(5, 4)
	if p, ok := f.Parent(5); !ok || p != 4 {
		t.Fatalf("Parent = %d, %v", p, ok)
	}
}

// Exe names the program of an exact process: the pid and its creation
// time, so a reused pid is never mistaken for it.
func TestRealExe(t *testing.T) {
	r := NewReal()
	pid := os.Getpid()
	ct, ok := r.CreateTime(pid)
	if !ok {
		t.Fatal("own create time")
	}
	exe, ok := r.Exe(pid, ct)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !ok || filepath.Base(exe) != filepath.Base(self) {
		t.Fatalf("Exe = %q, %v, want %q", exe, ok, self)
	}
	if _, ok := r.Exe(pid, ct-3600_000); ok {
		t.Fatal("a wrong creation time named a program")
	}
}
