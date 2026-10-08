package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// savedBool is SavedOptions' first answer, for the checks that need only it.
func savedBool(dir, id string) bool {
	saved, _ := SavedOptions(dir, id)
	return saved
}

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
func TestSavedOptions(t *testing.T) {
	id := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	dir := t.TempDir()
	if savedBool(dir, id) {
		t.Fatal("no record: no saved options")
	}
	writeJob(t, dir, "1a2b3c4d", `{"state":"done","sessionId":"`+id+`","template":"bg","respawnFlags":["--remote-control"]}`)
	if !savedBool(dir, id) {
		t.Fatal("a record for the session: saved options")
	}
	if !savedBool(dir, "1A2B3C4D-5E6F-4A7B-8C9D-0E1F2A3B4C5D") {
		t.Fatal("an id in upper case is the same session")
	}
	other := "1a2b3c4d-0000-4000-8000-000000000001"
	if savedBool(dir, other) {
		t.Fatal("a record under the same short id for another session does not count")
	}
	writeJob(t, dir, "aaaaaaaa", `{"sessionId":`)
	if savedBool(dir, "aaaaaaaa-0000-4000-8000-000000000001") {
		t.Fatal("a damaged record does not count")
	}
	for _, bad := range []string{"", "..", "../../etc", "1a2b3c4d"} {
		if savedBool(dir, bad) {
			t.Fatalf("%q is not a session id", bad)
		}
	}
}

// The saved options say whether the session keeps Remote Control on, which
// decides how long a resume waits for it to connect.
func TestSavedOptionsRemoteControl(t *testing.T) {
	dir := t.TempDir()
	on := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	off := "5e6f7a8b-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeJob(t, dir, "1a2b3c4d", `{"sessionId":"`+on+`","respawnFlags":["--remote-control","--model","sonnet"]}`)
	writeJob(t, dir, "5e6f7a8b", `{"sessionId":"`+off+`","respawnFlags":["--model","sonnet"]}`)
	if saved, rc := SavedOptions(dir, on); !saved || !rc {
		t.Fatalf("saved with Remote Control: %v %v", saved, rc)
	}
	if saved, rc := SavedOptions(dir, off); !saved || rc {
		t.Fatalf("saved without Remote Control: %v %v", saved, rc)
	}
}

// A record far larger than any real one is not read as one.
func TestSavedOptionsIgnoresAHugeRecord(t *testing.T) {
	dir := t.TempDir()
	id := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeJob(t, dir, "1a2b3c4d", `{"sessionId":"`+id+`","pad":"`+strings.Repeat("x", 2<<20)+`"}`)
	if saved, _ := SavedOptions(dir, id); saved {
		t.Fatal("a record over 1 MiB does not count")
	}
}

// The CLI may name a job's folder by its own job id rather than the start
// of the session id; the record is found by the session id inside it.
func TestSavedOptionsUnderAnotherFolderName(t *testing.T) {
	dir := t.TempDir()
	id := "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	writeJob(t, dir, "9f8e7d6c", `{"sessionId":"`+id+`","respawnFlags":["--remote-control"]}`)
	writeJob(t, dir, "0a0b0c0d", `{"sessionId":"0a0b0c0d-0000-4000-8000-000000000001"}`)
	if saved, rc := SavedOptions(dir, id); !saved || !rc {
		t.Fatalf("a record in a folder named by the job id: %v %v", saved, rc)
	}
	if saved, _ := SavedOptions(dir, "2b3c4d5e-5e6f-4a7b-8c9d-0e1f2a3b4c5d"); saved {
		t.Fatal("no record for this session in any folder")
	}
}
