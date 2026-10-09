// Package claude reads Claude Code's on-disk session state and parses the
// CLI's own text and JSON output. It never writes anything back.
package claude

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Host names where a Claude Code session is running.
type Host string

const (
	HostTerminal   Host = "terminal"
	HostBackground Host = "background"
	HostDesktop    Host = "desktop"
	HostVSCode     Host = "vscode"
	HostOther      Host = "other"
	HostNone       Host = "none"
)

// Session is the parsed contents of one session file.
type Session struct {
	ID, ShortID   string
	PID           int
	ProcStart     string // raw, as written in the file
	Cwd, Name     string
	Host          Host
	Entrypoint    string // raw
	RemoteControl bool
	Status        string
	// BridgeSessionID is the session's Remote Control bridge id, empty
	// while Remote Control is off.
	BridgeSessionID string
	// StartedAt is when the session started in this process, zero when the
	// file does not say. A background process is started ahead of time and
	// given to a session later, so it can be well after the process's start.
	StartedAt time.Time
	// StatusUpdatedAt is when Claude Code last set Status, zero when the
	// file does not say.
	StatusUpdatedAt time.Time
}

type sessionFile struct {
	PID             json.Number     `json:"pid"`
	SessionID       string          `json:"sessionId"`
	Cwd             string          `json:"cwd"`
	ProcStart       json.RawMessage `json:"procStart"`
	Kind            string          `json:"kind"`
	Entrypoint      string          `json:"entrypoint"`
	Name            string          `json:"name"`
	JobID           string          `json:"jobId"`
	Status          string          `json:"status"`
	BridgeSessionID string          `json:"bridgeSessionId"`
	StartedAt       json.RawMessage `json:"startedAt"`
	StatusUpdatedAt json.RawMessage `json:"statusUpdatedAt"`
}

const (
	// maxNameRunes bounds the display name kept from a session file. A name
	// is chosen by a person and is never long; anything past this is a file
	// that is not what it claims to be, and the whole of it would otherwise
	// be carried into every view and every log line.
	maxNameRunes = 200
	// maxCwdBytes bounds the working directory kept from a session file,
	// comfortably past the longest path any operating system will accept.
	maxCwdBytes = 4096
)

// ParseSessionFile parses one session file. It errors on malformed JSON,
// when pid or cwd are missing or invalid, and when the session id is not a
// canonical UUID: an id that is not one would end up in a command line and
// in a file path, so a file carrying anything else is skipped rather than
// half trusted. The display name and the working directory are capped
// rather than rejected, since a session is still worth showing when only
// its label is unreasonable.
func ParseSessionFile(data []byte) (Session, error) {
	var f sessionFile
	if err := json.Unmarshal(data, &f); err != nil {
		return Session{}, err
	}
	pid, err := f.PID.Int64()
	if err != nil || pid <= 0 || f.Cwd == "" {
		return Session{}, errors.New("session file missing pid, sessionId or cwd")
	}
	if !ValidID(f.SessionID) {
		return Session{}, errors.New("session file has no canonical session id")
	}
	return Session{
		ID:              f.SessionID,
		ShortID:         shortIDOf(f.JobID, f.SessionID),
		PID:             int(pid),
		ProcStart:       decodeProcStart(f.ProcStart),
		Cwd:             capBytes(f.Cwd, maxCwdBytes),
		Name:            capRunes(f.Name, maxNameRunes),
		Host:            ClassifyHost(f.Entrypoint, f.Kind),
		Entrypoint:      f.Entrypoint,
		RemoteControl:   f.BridgeSessionID != "",
		Status:          f.Status,
		BridgeSessionID: f.BridgeSessionID,
		StartedAt:       decodeMillis(f.StartedAt),
		StatusUpdatedAt: decodeMillis(f.StatusUpdatedAt),
	}, nil
}

// decodeMillis reads a time written as milliseconds since 1970, and gives
// the zero time for anything else, so a file with an odd value is still
// read for the rest.
func decodeMillis(raw json.RawMessage) time.Time {
	var ms int64
	if json.Unmarshal(raw, &ms) != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// shortIDOf picks the eight character id a session is known by on a command
// line. The file's own jobId is used only when it has exactly that shape,
// because it ends up in a command a person is invited to copy and run; a
// jobId of any other shape is ignored in favour of the session id's own
// first eight characters, which the canonical id check has already
// guaranteed to be hex.
func shortIDOf(jobID, sessionID string) string {
	if isShortID(jobID) {
		return strings.ToLower(jobID)
	}
	return strings.ToLower(sessionID[:8])
}

// isShortID reports whether s is exactly eight hex characters, in either
// case.
func isShortID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, r := range s {
		if !isHexDigit(r) {
			return false
		}
	}
	return true
}

// capRunes returns s cut to at most n runes, never splitting one in half.
func capRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// capBytes returns s cut to at most n bytes, backing off to the last whole
// rune boundary rather than leaving a broken one behind.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// decodeProcStart reads procStart as plain text whether Claude wrote it as a
// quoted string or a bare number, and returns "" when the field is absent.
func decodeProcStart(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// ClassifyHost maps Claude's entrypoint and kind to a Host. Only "cli"
// names a session this program knows how to close and resume, so only it
// becomes a terminal or a background session. Anything else, an entrypoint
// left empty included, is HostOther and is never touched; its raw
// entrypoint is kept separately on the Session.
func ClassifyHost(entrypoint, kind string) Host {
	switch entrypoint {
	case "claude-desktop", "claude-desktop-3p":
		return HostDesktop
	case "claude-vscode":
		return HostVSCode
	case "cli":
		if kind == "bg" || kind == "background" {
			return HostBackground
		}
		return HostTerminal
	default:
		return HostOther
	}
}
