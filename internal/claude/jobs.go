package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// JobsDir returns the directory holding the CLI's records of background
// jobs, one folder per short id, ~/.claude/jobs.
func JobsDir() string { return filepath.Join(Dir(), "jobs") }

// SavedOptions reports whether the CLI keeps a background job record for
// the session id under jobsDir, and whether the options saved in it turn
// Remote Control on. A session that has run in the background once keeps
// its own saved options there, and a later resume obeys them: a resume that
// passes flags starts a copy instead of waking the session. It checks the
// id before it reaches a file path, only reads, and answers false for a
// record that is missing, unreadable, damaged, far too large, or for
// another session.
func SavedOptions(jobsDir, id string) (saved, remoteControl bool) {
	if jobsDir == "" || !ValidID(id) {
		return false, false
	}
	id = strings.ToLower(id)
	f, err := os.Open(filepath.Join(jobsDir, id[:8], "state.json"))
	if err != nil {
		return false, false
	}
	defer f.Close()
	// A job record is a few kilobytes; anything far larger is not one.
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return false, false
	}
	var rec struct {
		SessionID       string   `json:"sessionId"`
		ResumeSessionID string   `json:"resumeSessionId"`
		RespawnFlags    []string `json:"respawnFlags"`
	}
	if json.Unmarshal(data, &rec) != nil {
		return false, false
	}
	if strings.ToLower(rec.SessionID) != id && strings.ToLower(rec.ResumeSessionID) != id {
		return false, false
	}
	for _, flag := range rec.RespawnFlags {
		if flag == "--remote-control" {
			return true, true
		}
	}
	return true, false
}
