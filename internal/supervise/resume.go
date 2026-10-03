package supervise

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// validShortID reports whether short is exactly eight lowercase hex
// characters, the only form a short id is ever allowed to take before it
// reaches a Runner call.
func validShortID(short string) bool {
	if len(short) != 8 {
		return false
	}
	for _, r := range short {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

// savedConversation is what a look for a session's saved conversation
// found.
type savedConversation int

const (
	// savedUnknown means the look could not be finished, such as when a
	// folder could not be read, and says nothing either way. It is tried
	// again on the next pass.
	savedUnknown savedConversation = iota
	// savedNone means the look was finished and nothing is saved.
	savedNone
	// savedThere means the conversation is saved.
	savedThere
)

// transcriptSaved looks for a saved transcript for a session with the
// given id, started in cwd. A session with no transcript at all has
// nothing to resume from, so its babysitting ends instead of trying, but
// only when the look is sure: a look that failed part way never ends it.
func (d *Deps) transcriptSaved(cwd, id string) savedConversation {
	_, err := claude.LookupTranscript(d.ProjectsDir, cwd, id)
	switch {
	case err == nil:
		return savedThere
	case errors.Is(err, fs.ErrNotExist):
		return savedNone
	}
	return savedUnknown
}

// sessionLabel renders the label a log entry should use for a session:
// its display name when it has one, otherwise its short id.
func sessionLabel(name, id string) string {
	if name != "" {
		return name
	}
	return shortOf(id)
}

// stopCopyOrRefuse validates a copy id the CLI reported forking, lowercased
// so a case-insensitive match through claude.ParseCopy can never send a
// mixed-case argument to the runner, then stops and removes it. ok is false
// when nothing was touched: either the reported id is, as far as this can
// tell, the original session itself rather than a copy, or it does not
// even have the shape of a short id. Either way res names the id that was
// left alone, since a message that cannot be traced back to an actual
// process risks leaving a real forked copy running unnoticed.
func (d *Deps) stopCopyOrRefuse(ctx context.Context, copyID, id, cwd, original string) (res Result, ok bool) {
	if copyID == original {
		return Result{Message: fmt.Sprintf("Claude reported a copy with the same short id %s as the session itself, so nothing was stopped or removed. Run by hand: %s", copyID, hosts.BackgroundResumeCommandIn(cwd, id))}, false
	}
	if !validShortID(copyID) {
		return Result{Message: fmt.Sprintf("Claude reported a copy %s that could not be safely stopped or removed. Check `claude agents` and remove it by hand.", copyID)}, false
	}
	stopOut, stopErr := d.Runner.Run(ctx, "", hosts.StopArgs(copyID)...)
	if stopErr != nil {
		return Result{Message: fmt.Sprintf("Claude forked a copy %s and it could not be stopped: %s. Stop and remove it by hand: run `claude stop %s`, then `claude rm %s`.", copyID, why(stopOut, stopErr), copyID, copyID)}, false
	}
	rmOut, rmErr := d.Runner.Run(ctx, "", hosts.RemoveArgs(copyID)...)
	if rmErr != nil {
		return Result{Message: fmt.Sprintf("Claude forked a copy %s that was stopped but could not be removed: %s. Remove it by hand: `claude rm %s`.", copyID, why(rmOut, rmErr), copyID)}, false
	}
	return Result{}, true
}

// resumeBackground runs the flags rule for resuming a background session,
// recovers once from a forked copy, and never leaves a copy behind: the
// copy is always stopped and removed, whether the recovery succeeds or a
// second fork forces an honest failure.
func (d *Deps) resumeBackground(ctx context.Context, id, name, cwd string, hasSaved bool) (string, Result) {
	if !claude.ValidID(id) {
		return "", Result{Message: "That is not a valid session id."}
	}
	d.defaults()
	// A session id may be written in upper case, while a copy id that got
	// this far is always lower case hex. Comparing them in the same case is
	// what keeps the guard below from mistaking the original session for a
	// copy and removing it.
	original := strings.ToLower(shortOf(id))
	// Flags are only passed when the session has never saved its own, so
	// this is exactly the question "did we ask for Remote Control".
	askedForRC := !hasSaved

	out, runErr := d.Runner.Run(ctx, cwd, hosts.BackgroundResumeArgs(id, hasSaved)...)
	recovered := false
	if rawCopy, ok := claude.ParseCopy(out); ok {
		copyID := strings.ToLower(rawCopy)
		if res, ok := d.stopCopyOrRefuse(ctx, copyID, id, cwd, original); !ok {
			return "", res
		}
		d.Log.Auto(sessionLabel(name, id), "resume forked a copy", "stopped and removed copy "+copyID+", retrying without flags")
		out, runErr = d.Runner.Run(ctx, cwd, hosts.BackgroundResumeArgs(id, true)...)
		recovered = true
		if rawCopy2, again := claude.ParseCopy(out); again {
			copy2 := strings.ToLower(rawCopy2)
			if res, ok := d.stopCopyOrRefuse(ctx, copy2, id, cwd, original); !ok {
				return "", res
			}
			// Two forks in a row means the CLI would not wake this session at
			// all, which is what it does when the session is running
			// somewhere already.
			if short, res, live := d.alreadyRunning(id); live {
				return short, res
			}
			return "", Result{Message: fmt.Sprintf("Claude forked a copy twice, and both copies %s and %s were stopped and removed. Run by hand: %s", copyID, copy2, hosts.BackgroundResumeCommandIn(cwd, id))}
		}
	}

	short, ok := claude.ParseBackgrounded(out)
	if !ok {
		if refusedAsAlreadyRunning(out) {
			if short, res, live := d.alreadyRunning(id); live {
				return short, res
			}
		}
		// why, rather than the output alone: a resume that timed out or
		// could not be started says nothing at all, and "no output" tells
		// the user nothing about what went wrong.
		return "", Result{Message: fmt.Sprintf("Resume did not start: %s. Run by hand: %s", why(out, runErr), hosts.BackgroundResumeCommandIn(cwd, id))}
	}

	msg := "running as background session " + short
	if recovered {
		msg += " after a second try without flags"
	}
	msg += ". " + d.remoteControlClause(ctx, id, short, askedForRC)
	return short, Result{OK: true, Message: msg, ShortID: short}
}

// refusedAsAlreadyRunning reports whether the CLI turned a resume down
// because the session is running already. More than one wording is matched
// because the CLI has put this refusal in more than one way, and reading it
// wrongly costs the user a second copy of a live session.
func refusedAsAlreadyRunning(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "is already running in the background") ||
		strings.Contains(low, "is running as a background session")
}

// alreadyRunning turns a refused resume into the plain answer that there
// was nothing to do, naming the host the session is in. A host a person can
// see is preferred over a background copy, since that is the one they are
// being told about. live is false when nothing is running after all, which
// leaves the caller to report the refusal as the failure it is.
func (d *Deps) alreadyRunning(id string) (short string, res Result, live bool) {
	d.Obs.RefreshNow()
	sessions := d.Obs.Current().All(id)
	if len(sessions) == 0 {
		return "", Result{}, false
	}
	found := sessions[0]
	for _, s := range sessions {
		if s.Host != claude.HostBackground {
			found = s
			break
		}
	}
	return found.ShortID, Result{
		OK:            true,
		Message:       "the session is already running in " + hostLabel(found.Host) + ", nothing to resume",
		ShortID:       found.ShortID,
		AlreadyLiveIn: found.Host,
	}, true
}

// rcNotConnectedYet is what is said when the bridge has not turned up
// inside the window. It is never "off": a session whose Remote Control is
// still being set up looks exactly like one that has none, and the card is
// the thing that keeps telling the truth afterwards.
const rcNotConnectedYet = "Remote Control is not connected yet. The card shows when it is."

// rcPollInterval is how often the session files are looked at while waiting
// for a bridge to turn up.
const rcPollInterval = 250 * time.Millisecond

// maxLooks is how many times a wait may look before it gives up, alongside
// the deadline it is really waiting on. A deadline is read off the injected
// clock, which a test can hold still, while the pause between looks is real
// time: counting the looks as well means a clock that never moves ends the
// wait instead of leaving it to spin for ever. It is far more looks than the
// longest window this program ever waits out needs, so a real wait is never
// cut short by it.
const maxLooks = 256

// remoteControlClause reports Remote Control's state in the background copy
// that has just been started. A resume that asked for it is given the
// longer window, because asking for it and getting it are seconds apart;
// off is only ever said about a session that was observed without a bridge
// and was not asked to have one.
//
// It reads the session files directly rather than refreshing the observer,
// because the goroutine that reconciles published snapshots is the one that
// runs this: refreshing here would hand that loop the very pass it is in
// the middle of, once per look, for as long as the bridge took to arrive.
func (d *Deps) remoteControlClause(ctx context.Context, id, short string, askedForRC bool) string {
	window := d.SettleTimeout
	if askedForRC {
		window = d.RCSettleTimeout
	}
	deadline := d.Now().Add(window)
	seenWithoutBridge := false
	for look := 0; look < maxLooks; look++ {
		for _, s := range d.Obs.Peek().All(id) {
			if s.Host != claude.HostBackground {
				continue
			}
			if s.RemoteControl {
				return "Remote Control is on."
			}
			seenWithoutBridge = true
		}
		if d.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return rcNotConnectedYet
		case <-time.After(rcPollInterval):
		}
	}
	if seenWithoutBridge && !askedForRC {
		return "Remote Control is off: attach with `claude attach " + short + "` and type /rc."
	}
	return rcNotConnectedYet
}

// shortOf renders the short form of a session id: its first eight
// characters, or the whole id when it is shorter than that.
func shortOf(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// firstLine returns the first non-blank line of s, or a placeholder when s
// has none.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return "no output"
}

// why renders why a CLI call did not do what was asked: its own first line
// of output, or the error it failed with when it said nothing at all. A
// call that timed out prints nothing, so without the error the only thing
// left to show would be the placeholder, which explains nothing. A closing
// full stop is dropped, because every caller writes its own after it.
func why(out string, err error) string {
	line := firstLine(out)
	if strings.TrimSpace(out) == "" && err != nil {
		line = err.Error()
	}
	return strings.TrimRight(line, ". ")
}
