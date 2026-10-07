package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// JobsDir returns the directory holding the CLI's records of background
// jobs, one folder per short id, ~/.claude/jobs.
func JobsDir() string { return filepath.Join(Dir(), "jobs") }

// SavedOptions reports whether the CLI keeps a background job record for
// the session id under jobsDir, and whether the options saved in it turn
// Remote Control on. A session that has run in the background once keeps
// its own saved options there, and a later resume obeys them: a resume that
// passes flags starts a copy instead of waking the session. The record is
// usually in the folder named by the start of the session id; the CLI may
// name the folder by its own job id instead, so the other folders are
// looked through too. It checks the id before it reaches a file path, only
// reads, and answers false for a record that is missing, unreadable,
// damaged, far too large, or for another session.
func SavedOptions(jobsDir, id string) (saved, remoteControl bool) {
	if jobsDir == "" || !ValidID(id) {
		return false, false
	}
	id = strings.ToLower(id)
	if found, rc := readJob(filepath.Join(jobsDir, id[:8], "state.json"), id); found {
		return true, rc
	}
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return false, false
	}
	for i, e := range entries {
		// A jobs folder holds one entry per background session; far more
		// than that is not one to search.
		if i >= maxJobFolders {
			break
		}
		name := e.Name()
		if !e.IsDir() || name == id[:8] || !shortJobName.MatchString(name) {
			continue
		}
		if found, rc := readJob(filepath.Join(jobsDir, name, "state.json"), id); found {
			return true, rc
		}
	}
	return false, false
}

// maxJobFolders bounds how many job folders SavedOptions looks through.
const maxJobFolders = 1000

// shortJobName is the shape of a job folder's name: eight hex digits.
var shortJobName = regexp.MustCompile(`^[0-9a-f]{8}$`)

// readJob reads one job record at path and reports whether it belongs to
// the session id, which must be in lower case, and whether its saved
// options turn Remote Control on.
func readJob(path, id string) (found, remoteControl bool) {
	f, err := os.Open(path)
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
