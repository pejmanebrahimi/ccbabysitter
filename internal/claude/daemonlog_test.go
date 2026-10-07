package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The CLI daemon writes a line when it stops an idle background session.
// Only a recent line for the very session counts, and a missing or odd log
// is simply no answer.
func TestRetired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	now := time.Date(2026, 10, 6, 14, 7, 40, 0, time.UTC)
	if _, ok := Retired(path, "304152c6", now, 5*time.Minute); ok {
		t.Fatal("no log: no answer")
	}
	log := "[2026-10-05T11:17:37.050Z] [bg] bg retire 304152c6: idle-prompt, idle 8h, worker 2.1.283 (daemon 2.1.289)\n" +
		"[2026-10-06T14:06:00.000Z] [bg] bg retire aaaaaaaa: settled, idle 1h\n" +
		"garbage line\n" +
		"[not a time] [bg] bg retire 304152c6: settled, idle 8h\n" +
		"[2026-10-06T14:07:36.616Z] [bg] bg retire 304152c6: settled, idle 8h\n" +
		"[2026-10-06T14:07:37.350Z] [bg] bg settled 304152c6 (done)\n"
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	idle, ok := Retired(path, "304152c6", now, 5*time.Minute)
	if !ok || idle != 8*time.Hour {
		t.Fatalf("the recent retire line: %v %v", idle, ok)
	}
	if _, ok := Retired(path, "304152c6", now.Add(time.Hour), 5*time.Minute); ok {
		t.Fatal("a retire line older than the window does not count")
	}
	if _, ok := Retired(path, "bbbbbbbb", now, 5*time.Minute); ok {
		t.Fatal("another session's line does not count")
	}
	if idle, ok := Retired(path, "aaaaaaaa", now, 5*time.Minute); !ok || idle != time.Hour {
		t.Fatalf("a one-hour retire: %v %v", idle, ok)
	}
	if _, ok := Retired(path, "../x", now, 5*time.Minute); ok {
		t.Fatal("not a short id")
	}
}
