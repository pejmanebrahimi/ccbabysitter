package client

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/procs"
	"ccbabysitter.dev/ccbabysitter/internal/supervise"
)

// ErrNotFound is returned when no session goes by the word given.
var ErrNotFound = errors.New("no such session")

// ErrNotInSession is returned for the word self when the command is not
// running inside a Claude Code session.
var ErrNotInSession = errors.New("this command is not running inside a Claude Code session")

// AmbiguousError is returned when a word fits more than one session.
// IDs holds their short ids, or their full ids when the word is a short id
// that more than one of them has.
type AmbiguousError struct {
	Word string
	IDs  []string
}

// Error names the word and the sessions it could mean.
func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d sessions match %s: %s", len(e.IDs), e.Word, strings.Join(e.IDs, ", "))
}

// Process is what FindSelf needs to know about the machine's processes.
type Process interface {
	Parent(pid int) (int, bool)
	CreateTime(pid int) (int64, bool)
}

// Session is one session as the command line shows it. Its JSON is part
// of the promise made to agents: fields are only ever added.
type Session struct {
	ID            string   `json:"id"`
	ShortID       string   `json:"shortId"`
	Name          string   `json:"name"`
	AlsoCalled    string   `json:"alsoCalled,omitempty"`
	Folder        string   `json:"folder"`
	App           string   `json:"app"`  // terminal|background|desktop|vscode|other, "" when running nowhere
	Apps          []string `json:"apps"` // every app it runs in now, never null
	Running       bool     `json:"running"`
	PID           int      `json:"pid,omitempty"`
	RemoteControl bool     `json:"remoteControl"`
	Status        string   `json:"status"` // busy|idle|"" as the session reports it
	Babysat       bool     `json:"babysat"`
	State         string   `json:"state,omitempty"` // watching|background|starting|stuck while babysat
	Tokens        Tokens   `json:"tokens"`
	Model         string   `json:"model,omitempty"`
	LastActivity  string   `json:"lastActivity,omitempty"` // RFC 3339, UTC
	UptimeSeconds int64    `json:"uptimeSeconds"`
	RemoteURL     string   `json:"remoteUrl,omitempty"`
	AttachCmd     string   `json:"attachCmd,omitempty"`
	SSHAttachCmd  string   `json:"sshAttachCmd,omitempty"`
	ResumeCmd     string   `json:"resumeCmd,omitempty"`
	RCHint        string   `json:"rcHint,omitempty"`
	Warning       string   `json:"warning,omitempty"`
	CanStop       bool     `json:"canStop"`
	CanUnbabysit  bool     `json:"canUnbabysit"`
	// ScheduledTask is true for a run of a Claude Desktop scheduled task,
	// which is never babysat.
	ScheduledTask bool `json:"scheduledTask"`
	procStart     string
}

// Tokens is what a session has used so far.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// Labels returns the names the Activity log may use for the session: its
// name, the name it is also called, its short id, and the first 8
// characters of its id, which is what the engine calls an unnamed session
// and can differ from the short id of a background job. Empty ones and
// repeats are left out.
func (s Session) Labels() []string {
	var out []string
	seen := make(map[string]bool)
	for _, l := range []string{s.Name, s.AlsoCalled, s.ShortID, s.ID[:min(8, len(s.ID))]} {
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// appNames turns hosts into the app names the command line prints, never
// returning nil.
func appNames(live []claude.Host) []string {
	out := make([]string, 0, len(live))
	for _, h := range live {
		out = append(out, string(h))
	}
	return out
}

// activity formats a last-activity time as RFC 3339 in UTC, and as the
// empty string when there is none.
func activity(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// tokensOf copies the token counts a session has used so far.
func tokensOf(st claude.Stats) Tokens {
	return Tokens{
		Input:      st.InputTokens,
		Output:     st.OutputTokens,
		CacheRead:  st.CacheReadTokens,
		CacheWrite: st.CacheWriteTokens,
	}
}

// Sessions lists every session in v as the command line shows it: the
// sessions that are not babysat first, then the babysat ones. The
// supervisor puts a babysat session only in Watches, running or not. If an
// id is in both lists, which a view built mid-change can do, the watch's
// entry replaces the session's where it stood.
func Sessions(v supervise.View) []Session {
	out := make([]Session, 0, len(v.Sessions)+len(v.Watches))
	at := make(map[string]int)
	for _, s := range v.Sessions {
		at[s.ID] = len(out)
		out = append(out, Session{
			ID:            s.ID,
			ShortID:       s.ShortID,
			Name:          s.Name,
			AlsoCalled:    s.AlsoCalled,
			Folder:        s.Cwd,
			App:           string(s.Host),
			Apps:          appNames(s.Live),
			Running:       true,
			PID:           s.PID,
			RemoteControl: s.RemoteControl,
			Status:        s.Status,
			Babysat:       s.Watched,
			Tokens:        tokensOf(s.Stats),
			Model:         s.Stats.Model,
			LastActivity:  activity(s.Stats.LastActivity),
			UptimeSeconds: s.Tree.UptimeSeconds,
			AttachCmd:     s.AttachCmd,
			SSHAttachCmd:  s.SSHAttachCmd,
			Warning:       s.FallbackWarning,
			CanStop:       s.CanStop,
			CanUnbabysit:  false,
			ScheduledTask: s.ScheduledTask,
			procStart:     s.ProcStart,
		})
	}
	for _, w := range v.Watches {
		e := Session{
			ID:            w.SessionID,
			ShortID:       w.ShortID,
			Name:          w.Name,
			AlsoCalled:    w.AlsoCalled,
			Folder:        w.Cwd,
			App:           string(w.Host),
			Apps:          appNames(w.Live),
			Running:       w.Host != "",
			PID:           w.PID,
			RemoteControl: w.RCOn,
			Status:        w.Status,
			Babysat:       true,
			State:         string(w.State),
			Tokens:        tokensOf(w.Stats),
			Model:         w.Stats.Model,
			LastActivity:  activity(w.Stats.LastActivity),
			UptimeSeconds: w.Tree.UptimeSeconds,
			RemoteURL:     w.RemoteURL,
			AttachCmd:     w.AttachCmd,
			SSHAttachCmd:  w.SSHAttachCmd,
			ResumeCmd:     w.ResumeCmd,
			RCHint:        w.RCHint,
			Warning:       w.FallbackWarning,
			CanStop:       w.CanStop,
			CanUnbabysit:  w.CanUnbabysit,
			procStart:     w.ProcStart,
		}
		if i, ok := at[e.ID]; ok {
			out[i] = e
			continue
		}
		at[e.ID] = len(out)
		out = append(out, e)
	}
	return out
}

// maxSelfSteps bounds how far up the process tree FindSelf looks.
const maxSelfSteps = 64

// FindSelf walks up from pid through its parents and returns the process
// id of the running session it finds, if any. A session's process id only
// counts when the process now holding it started when the session's file
// says it did, and a start time that is missing or cannot be read does not
// count, so a reused id is never taken for the session. A parent id is
// only followed when that process started no later than its child: an
// orphan keeps its dead parent's id on Windows, and the id may by now belong
// to a process in an unrelated tree. Where either start is unknown, the walk
// ends.
func FindSelf(v supervise.View, pid int, p Process) (int, bool) {
	running := make(map[int]Session)
	for _, s := range Sessions(v) {
		if s.Running && s.PID > 0 {
			running[s.PID] = s
		}
	}
	seen := map[int]bool{pid: true}
	cur := pid
	curStart, ok := p.CreateTime(cur)
	if !ok {
		return 0, false
	}
	for i := 0; i < maxSelfSteps; i++ {
		parent, ok := p.Parent(cur)
		if !ok || parent <= 1 || seen[parent] {
			return 0, false
		}
		seen[parent] = true
		parentStart, ok := p.CreateTime(parent)
		if !ok || parentStart > curStart {
			return 0, false
		}
		if s, ok := running[parent]; ok && procs.MatchStart(parentStart, s.procStart) == procs.Matches {
			return parent, true
		}
		cur, curStart = parent, parentStart
	}
	return 0, false
}

// Resolve finds the session word names: self for the session the command
// is running inside (self reports its process id), else a full id, else a
// short id, else a name or the name it is also called. A full id wins
// outright. A word that fits two sessions, whether as the same short id,
// the same name, or one session's short id and another's name, is an
// *AmbiguousError.
func Resolve(v supervise.View, word string, self func() (int, bool)) (Session, error) {
	if word == "" {
		return Session{}, ErrNotFound
	}
	all := Sessions(v)
	if word == "self" {
		pid, ok := self()
		if !ok {
			return Session{}, ErrNotInSession
		}
		for _, s := range all {
			if s.Running && s.PID == pid {
				return s, nil
			}
		}
		return Session{}, ErrNotInSession
	}
	for _, s := range all {
		if s.ID != "" && s.ID == word {
			return s, nil
		}
	}
	var short, named []Session
	for _, s := range all {
		if s.ShortID != "" && s.ShortID == word {
			short = append(short, s)
		}
		if (s.Name != "" && s.Name == word) || (s.AlsoCalled != "" && s.AlsoCalled == word) {
			named = append(named, s)
		}
	}
	if len(short) == 0 {
		return only(word, named, false)
	}
	// A short id that is also another session's name fits both, and is
	// refused. A session called by its own short id is just that session.
	both := short
	for _, n := range named {
		if !hasID(both, n.ID) {
			both = append(both[:len(both):len(both)], n)
		}
	}
	return only(word, both, true)
}

// hasID reports whether list has a session with the given id.
func hasID(list []Session, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}
	return false
}

// only is the one session in matches, ErrNotFound when there is none, and an
// *AmbiguousError when there are several. It names their short ids, or their
// full ids when the short id is what they share.
func only(word string, matches []Session, fullIDs bool) (Session, error) {
	switch len(matches) {
	case 0:
		return Session{}, ErrNotFound
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, s := range matches {
		ids[i] = s.ShortID
		if fullIDs {
			ids[i] = s.ID
		}
	}
	return Session{}, &AmbiguousError{Word: word, IDs: ids}
}
