package claude

import (
	"os"
	"path/filepath"
	"testing"
)

func writeJob(t *testing.T, dir, short, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, short), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, short, "state.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A session the CLI has run in the background keeps a job record with its
// own saved options, and only a record for that very session counts.
func TestHasSavedOptions(t *testing.T) {
	id := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	dir := t.TempDir()
	if HasSavedOptions(dir, id) {
		t.Fatal("no record: no saved options")
	}
	writeJob(t, dir, "1a2b3c4d", `{"state":"done","sessionId":"`+id+`","template":"bg","respawnFlags":["--remote-control"]}`)
	if !HasSavedOptions(dir, id) {
		t.Fatal("a record for the session: saved options")
	}
	if !HasSavedOptions(dir, "1A2B3C4D-5E6F-4A7B-8C9D-0E1F2A3B4C5D") {
		t.Fatal("an id in upper case is the same session")
	}
	other := "1a2b3c4d-0000-4000-8000-000000000001"
	if HasSavedOptions(dir, other) {
		t.Fatal("a record under the same short id for another session does not count")
	}
	writeJob(t, dir, "aaaaaaaa", `{"sessionId":`)
	if HasSavedOptions(dir, "aaaaaaaa-0000-4000-8000-000000000001") {
		t.Fatal("a damaged record does not count")
	}
	for _, bad := range []string{"", "..", "../../etc", "1a2b3c4d"} {
		if HasSavedOptions(dir, bad) {
			t.Fatalf("%q is not a session id", bad)
		}
	}
}
