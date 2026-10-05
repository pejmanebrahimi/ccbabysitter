package state

import "testing"

func TestStartReasonIsTakenOnce(t *testing.T) {
	dir := t.TempDir()
	if got := TakeStartReason(dir); got != "" {
		t.Fatalf("no note yet, got %q", got)
	}
	if err := WriteStartReason(dir, "Started in the background by ccbabysitter."); err != nil {
		t.Fatal(err)
	}
	if got := TakeStartReason(dir); got != "Started in the background by ccbabysitter." {
		t.Fatalf("got %q", got)
	}
	if got := TakeStartReason(dir); got != "" {
		t.Fatalf("taken twice: %q", got)
	}
}

func TestLoginStartOfferedIsRemembered(t *testing.T) {
	dir := t.TempDir()
	if LoginStartOffered(dir) {
		t.Fatal("offered before anything was noted")
	}
	if err := MarkLoginStartOffered(dir); err != nil {
		t.Fatal(err)
	}
	if !LoginStartOffered(dir) {
		t.Fatal("the note was not kept")
	}
}

// The background copy notes that it runs, with its creation time, and
// removes the note when it ends cleanly. The next one takes the note: one
// left behind means the last one did not end cleanly.
func TestServiceRunningNote(t *testing.T) {
	dir := t.TempDir()
	if _, ok := TakeServiceRunning(dir); ok {
		t.Fatal("a note in an empty folder")
	}
	if err := MarkServiceRunning(dir, 1234567); err != nil {
		t.Fatal(err)
	}
	ms, ok := TakeServiceRunning(dir)
	if !ok || ms != 1234567 {
		t.Fatalf("took %d, %v", ms, ok)
	}
	if _, ok := TakeServiceRunning(dir); ok {
		t.Fatal("taking the note leaves it there")
	}
	_ = MarkServiceRunning(dir, 1)
	ClearServiceRunning(dir)
	if _, ok := TakeServiceRunning(dir); ok {
		t.Fatal("a cleared note is still there")
	}
}

// Forgetting the note makes the next plain run turn start at login on
// again, as on a machine that never had CC Babysitter.
func TestForgetLoginStartOffered(t *testing.T) {
	dir := t.TempDir()
	if err := ForgetLoginStartOffered(dir); err != nil {
		t.Fatalf("forgetting a note that is not there: %v", err)
	}
	_ = MarkLoginStartOffered(dir)
	if err := ForgetLoginStartOffered(dir); err != nil || LoginStartOffered(dir) {
		t.Fatalf("still offered after forgetting: %v", err)
	}
}

// The launcher's note about a login item it wrote itself is read once.
func TestLoginItemNote(t *testing.T) {
	dir := t.TempDir()
	if got := TakeLoginItemNote(dir); got != "" {
		t.Fatalf("a note in an empty folder: %q", got)
	}
	if err := WriteLoginItemNote(dir, "Start at login kept on: CC Babysitter rewrote its login entry."); err != nil {
		t.Fatal(err)
	}
	if got := TakeLoginItemNote(dir); got != "Start at login kept on: CC Babysitter rewrote its login entry." {
		t.Fatalf("took %q", got)
	}
	if got := TakeLoginItemNote(dir); got != "" {
		t.Fatalf("taking the note leaves it there: %q", got)
	}
}
