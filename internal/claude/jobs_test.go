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
	id := "304152c6-817e-4b42-89da-b16ceb5dd457"
	dir := t.TempDir()
	if HasSavedOptions(dir, id) {
		t.Fatal("no record: no saved options")
	}
	writeJob(t, dir, "304152c6", `{"state":"done","sessionId":"`+id+`","template":"bg","respawnFlags":["--remote-control"]}`)
	if !HasSavedOptions(dir, id) {
		t.Fatal("a record for the session: saved options")
	}
	if !HasSavedOptions(dir, "304152C6-817E-4B42-89DA-B16CEB5DD457") {
		t.Fatal("an id in upper case is the same session")
	}
	other := "304152c6-0000-4000-8000-000000000001"
	if HasSavedOptions(dir, other) {
		t.Fatal("a record under the same short id for another session does not count")
	}
	writeJob(t, dir, "aaaaaaaa", `{"sessionId":`)
	if HasSavedOptions(dir, "aaaaaaaa-0000-4000-8000-000000000001") {
		t.Fatal("a damaged record does not count")
	}
	for _, bad := range []string{"", "..", "../../etc", "304152c6"} {
		if HasSavedOptions(dir, bad) {
			t.Fatalf("%q is not a session id", bad)
		}
	}
}
