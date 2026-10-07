package claude

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestFakeRunnerRecordsCallsAndAnswers(t *testing.T) {
	f := NewFakeRunner(func(args []string) (string, error) {
		if len(args) > 0 && args[0] == "agents" {
			return "[]", nil
		}
		return "backgrounded . 12121212 . n", nil
	})
	out, err := f.Run(context.Background(), "/w", "--bg", "--resume", "id")
	if err != nil || out == "" {
		t.Fatal(out, err)
	}
	calls := f.CallList()
	cwds := f.CwdList()
	if len(calls) != 1 || calls[0] != "--bg --resume id" || len(cwds) != 1 || cwds[0] != "/w" {
		t.Fatalf("%v %v", calls, cwds)
	}
}

func TestFakeRunnerSetRespondAndDefault(t *testing.T) {
	f := NewFakeRunner(nil)
	if out, err := f.Run(context.Background(), "", "x"); err != nil || out != "" {
		t.Fatalf("expected empty default answer, got %q %v", out, err)
	}
	f.SetRespond(func(args []string) (string, error) {
		return strings.Join(args, ","), nil
	})
	out, err := f.Run(context.Background(), "/other", "a", "b")
	if err != nil || out != "a,b" {
		t.Fatalf("%q %v", out, err)
	}
	if calls := f.CallList(); len(calls) != 2 || calls[1] != "a b" {
		t.Fatalf("expected two recorded calls, got %v", calls)
	}
	if cwds := f.CwdList(); len(cwds) != 2 || cwds[1] != "/other" {
		t.Fatalf("expected two recorded cwds, got %v", cwds)
	}
}

// A program that is not installed fails with exec.ErrNotFound on every
// system, which the observer relies on to say "not installed" once.
func TestExecRunnerMissingBinaryErrors(t *testing.T) {
	r := ExecRunner{Bin: "definitely-not-a-real-binary-xyz"}
	_, err := r.Run(context.Background(), "", "--version")
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("want exec.ErrNotFound, got %v", err)
	}
}

func TestExecRunnerRunsRealBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no plain echo binary on windows")
	}
	r := ExecRunner{Bin: "echo"}
	out, err := r.Run(context.Background(), "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected output to contain hello, got %q", out)
	}
}

func TestExecRunnerTimesOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no plain sleep binary on windows")
	}
	r := ExecRunner{Bin: "sleep"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.Run(ctx, "", "5")
	elapsed := time.Since(start)

	if err == nil || err.Error() != "claude did not answer in time" {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Run took %v, expected the timeout to land within 3s", elapsed)
	}
}
