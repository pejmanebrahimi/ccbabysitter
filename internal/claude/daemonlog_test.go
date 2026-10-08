package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI daemon writes a line when it stops an idle background session.
// Only a recent line for the very session counts, and a missing or odd log
// is simply no answer.
func TestRetired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	now := time.Date(2026, 10, 6, 14, 7, 40, 0, time.UTC)
	if _, ok := Retired(path, "1a2b3c4d", now.Add(-5*time.Minute), now); ok {
		t.Fatal("no log: no answer")
	}
	log := "[2026-10-05T11:17:37.050Z] [bg] bg retire 1a2b3c4d: idle-prompt, idle 8h, worker 2.1.283 (daemon 2.1.289)\n" +
		"[2026-10-06T14:06:00.000Z] [bg] bg retire aaaaaaaa: settled, idle 1h\n" +
		"garbage line\n" +
		"[not a time] [bg] bg retire 1a2b3c4d: settled, idle 8h\n" +
		"[2026-10-06T14:07:36.616Z] [bg] bg retire 1a2b3c4d: settled, idle 8h\n" +
		"[2026-10-06T14:07:37.350Z] [bg] bg settled 1a2b3c4d (done)\n"
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	idle, ok := Retired(path, "1a2b3c4d", now.Add(-5*time.Minute), now)
	if !ok || idle != 8*time.Hour {
		t.Fatalf("the recent retire line: %v %v", idle, ok)
	}
	if _, ok := Retired(path, "1a2b3c4d", now.Add(time.Hour-5*time.Minute), now.Add(time.Hour)); ok {
		t.Fatal("a retire line older than the window does not count")
	}
	if _, ok := Retired(path, "bbbbbbbb", now.Add(-5*time.Minute), now); ok {
		t.Fatal("another session's line does not count")
	}
	if idle, ok := Retired(path, "aaaaaaaa", now.Add(-5*time.Minute), now); !ok || idle != time.Hour {
		t.Fatalf("a one-hour retire: %v %v", idle, ok)
	}
	if _, ok := Retired(path, "../x", now.Add(-5*time.Minute), now); ok {
		t.Fatal("not a short id")
	}
}

// A retire line from before the start of the window, such as before the
// session's last rescue, does not explain a later exit.
func TestRetiredOnlyAfterFrom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	line := "[2026-10-06T14:07:36.616Z] [bg] bg retire 1a2b3c4d: settled, idle 8h\n"
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	rescued := time.Date(2026, 10, 6, 14, 7, 42, 0, time.UTC)
	if _, ok := Retired(path, "1a2b3c4d", rescued, rescued.Add(2*time.Minute)); ok {
		t.Fatal("a line from before the rescue does not count")
	}
}

// Only the end of a large log is read, and the line the daemon just wrote
// is there.
func TestRetiredReadsTheEndOfALargeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	filler := strings.Repeat("[2026-10-06T10:00:00.000Z] [bg] bg spare spawned host pid=1\n", 8000)
	line := "[2026-10-06T14:07:36.616Z] [bg] bg retire 1a2b3c4d: settled, idle 8h\n"
	if err := os.WriteFile(path, []byte(filler+line), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 14, 7, 40, 0, time.UTC)
	if idle, ok := Retired(path, "1a2b3c4d", now.Add(-5*time.Minute), now); !ok || idle != 8*time.Hour {
		t.Fatalf("the last line of a %d-byte log: %v %v", len(filler)+len(line), idle, ok)
	}
}
