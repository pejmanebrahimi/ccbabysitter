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
