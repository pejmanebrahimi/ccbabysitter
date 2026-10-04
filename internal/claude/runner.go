package claude

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Runner runs the claude CLI. cwd may be empty, meaning the caller's own
// working directory.
type Runner interface {
	Run(ctx context.Context, cwd string, args ...string) (string, error)
}

// ExecRunner runs the real claude binary. Stdin is always closed so a
// prompt the CLI might wait on can never hang the caller, and the child is
// given a bounded amount of time to answer. An empty Bin means the CLI
// FindCLI finds, looked up again on every run so one installed while this
// program is running is picked up; when there is none, plain "claude" is
// run, which fails the way a missing program always does.
type ExecRunner struct{ Bin string }

// Run executes the binary with args in cwd and returns its combined
// stdout and stderr. When ctx carries no deadline, a default of 60 seconds
// is applied. If the process is still running when that deadline passes,
// it is killed and Run returns the "claude did not answer in time" error
// alongside whatever output was produced before the kill.
func (r ExecRunner) Run(ctx context.Context, cwd string, args ...string) (string, error) {
	bin := r.Bin
	if bin == "" {
		bin = FindCLI()
	}
	if bin == "" {
		bin = "claude"
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	noWindow(cmd)
	cmd.Dir = cwd
	cmd.Stdin = strings.NewReader("")
	// Bound how long Wait keeps the output pipes open after the context is
	// canceled, so a killed child can never leave Run hanging.
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.String(), errors.New("claude did not answer in time")
	}
	return out.String(), err
}

// FakeRunner scripts answers for tests and records every call it receives.
// It is safe for concurrent use.
type FakeRunner struct {
	mu      sync.Mutex
	respond func(args []string) (string, error)
	calls   []string
	cwds    []string
}

// NewFakeRunner builds a FakeRunner that answers each call with respond.
// respond may be nil, in which case every call answers with an empty
// string and no error.
func NewFakeRunner(respond func(args []string) (string, error)) *FakeRunner {
	return &FakeRunner{respond: respond}
}

// Run records the call and, if a respond function is set, returns its
// answer for args.
func (f *FakeRunner) Run(_ context.Context, cwd string, args ...string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	f.cwds = append(f.cwds, cwd)
	respond := f.respond
	f.mu.Unlock()
	if respond == nil {
		return "", nil
	}
	return respond(args)
}

// CallList returns each recorded call's arguments, joined by a space, in
// the order Run was invoked.
func (f *FakeRunner) CallList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// CwdList returns the cwd passed to each recorded call, in the order Run
// was invoked.
func (f *FakeRunner) CwdList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.cwds))
	copy(out, f.cwds)
	return out
}

// SetRespond replaces the scripted answer function used by later calls.
func (f *FakeRunner) SetRespond(respond func(args []string) (string, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond = respond
}
