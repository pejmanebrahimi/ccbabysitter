package claude

import (
	"encoding/json"
	"errors"
	"strings"
)

// AgentEntry is one row of `claude agents --json`.
type AgentEntry struct {
	PID                                    int
	ID, SessionID, Kind, Status, Name, Cwd string
}

type agentRaw struct {
	PID       json.Number `json:"pid"`
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Kind      string      `json:"kind"`
	Status    string      `json:"status"`
	Name      string      `json:"name"`
	Cwd       string      `json:"cwd"`
}

// ErrNoAnswer means the output was not a JSON array: the CLI did not answer.
var ErrNoAnswer = errors.New("claude agents --json did not answer")

// ParseAgents parses `claude agents --json`, tolerating leading non-JSON
// lines. It returns ErrNoAnswer rather than an empty list when the output is
// not an array, because an unanswered check must never authorise a resume.
// Each row is decoded on its own, so one malformed row (a pid that is not a
// number, a field of the wrong type) is skipped rather than discarding every
// other row in the same answer.
func ParseAgents(out string) ([]AgentEntry, error) {
	i := strings.IndexByte(out, '[')
	if i < 0 {
		return nil, ErrNoAnswer
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(out[i:]), &rows); err != nil {
		return nil, ErrNoAnswer
	}
	list := make([]AgentEntry, 0, len(rows))
	for _, row := range rows {
		var r agentRaw
		if err := json.Unmarshal(row, &r); err != nil {
			continue
		}
		pid, err := r.PID.Int64()
		if err != nil || r.SessionID == "" {
			continue
		}
		list = append(list, AgentEntry{PID: int(pid), ID: r.ID, SessionID: r.SessionID, Kind: r.Kind, Status: r.Status, Name: r.Name, Cwd: r.Cwd})
	}
	return list, nil
}

// Listed reports whether any row of list is for the session id, whatever
// it says about it.
func Listed(list []AgentEntry, id string) bool {
	for _, a := range list {
		if strings.EqualFold(a.SessionID, id) {
			return true
		}
	}
	return false
}

// JobOnAnother returns the row of the running background job that goes by
// short and runs a session other than id, if list holds one. A background
// job keeps its short id when the conversation in it changes, as after
// /clear, so this is the job a watch knew, now running another
// conversation. short may be empty, which finds nothing.
func JobOnAnother(list []AgentEntry, id, short string) (AgentEntry, bool) {
	if short == "" {
		return AgentEntry{}, false
	}
	for _, a := range list {
		if !strings.EqualFold(a.ID, short) || a.SessionID == "" || strings.EqualFold(a.SessionID, id) {
			continue
		}
		if a.PID <= 0 || (a.Kind != "background" && a.Kind != "bg") || !ValidID(a.SessionID) {
			continue
		}
		switch strings.ToLower(a.Status) {
		case "stopped", "exited", "failed", "done":
			continue
		}
		return a, true
	}
	return AgentEntry{}, false
}

// RunningCopy returns the row of a background session that is running
// and runs the session id, if list holds one. It goes by the session id
// alone, never the short id: a background job keeps its short id when the
// conversation in it changes, and then runs that other conversation. A row
// with no process, or one that says it has stopped, is a copy that is not
// running any more.
func RunningCopy(list []AgentEntry, id string) (AgentEntry, bool) {
	for _, a := range list {
		if !strings.EqualFold(a.SessionID, id) || a.PID <= 0 || (a.Kind != "background" && a.Kind != "bg") {
			continue
		}
		switch strings.ToLower(a.Status) {
		case "stopped", "exited", "failed", "done":
			continue
		}
		return a, true
	}
	return AgentEntry{}, false
}
