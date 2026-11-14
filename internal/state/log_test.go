package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogWritesActivityAndRolls(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.maxBytes = 400
	ch, cancel := l.Subscribe()
	defer cancel()
	l.Auto("demo-a1", "host process exited", "resumed as background 11111111")
	e := <-ch
	if !e.Automatic || e.Reason == "" || e.Session != "demo-a1" {
		t.Fatalf("%+v", e)
	}
	for i := 0; i < 30; i++ {
		l.Info("s", strings.Repeat("x", 30))
	}
	fi, _ := os.Stat(filepath.Join(dir, "log.txt"))
	if fi.Size() > 800 {
		t.Fatalf("log did not roll: %d", fi.Size())
	}
	rec := l.Recent(5, "")
	if len(rec) != 5 || !strings.Contains(rec[0].Message, "x") {
		t.Fatalf("%+v", rec)
	}
	if got := l.Recent(50, "demo-a1"); len(got) != 1 || !got[0].Automatic {
		t.Fatalf("filter by session: %+v", got)
	}
	l.Error("s", "boom")
	if last := l.Recent(1, "")[0]; last.Level != "ERROR" {
		t.Fatalf("%+v", last)
	}
}

// A debugging line goes to log.txt only: Activity and its subscribers never
// see it.
func TestDebugLinesStayOutOfActivity(t *testing.T) {
	dir := t.TempDir()
	l, err := NewLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	ch, cancel := l.Subscribe()
	defer cancel()
	l.Debug("demo-a1", "resuming: agents listed nothing for this session")
	l.Info("", "after")
	if e := <-ch; e.Message != "after" {
		t.Fatalf("a subscriber got %+v", e)
	}
	for _, e := range l.Recent(10, "") {
		if e.Level == "DEBUG" {
			t.Fatalf("Activity has %+v", e)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "log.txt"))
	if err != nil || !strings.Contains(string(data), "DEBUG demo-a1: resuming: agents listed nothing for this session") {
		t.Fatalf("log.txt: %q %v", data, err)
	}
}
