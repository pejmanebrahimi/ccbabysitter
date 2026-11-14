package supervise

import (
	"context"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// TrustFolder gives the claude CLI its one-time OK to run on its own in
// dir, which it needs before it brings a session back there in the
// background. This is Allow rescue on the page and `ccbabysitter trust`.
//
// On a machine with no display the CLI's question is answered with Yes
// here, as New session does. Where there is a display, a terminal opens in
// the folder with the CLI running, for the person to answer: the question
// can only be answered here on Linux, and on a desktop the person is at
// the screen anyway. Either way, a babysat session stuck because the
// folder was not trusted comes back once it is.
//
// The checks and opening a terminal run on the loop. Answering the
// question takes seconds, so it runs on the caller's goroutine, as New
// session's does, and the loop goes on babysitting meanwhile.
func (s *Supervisor) TrustFolder(dir string, via Via) Result {
	answer := ""
	res := s.ask(func(context.Context) Result { return s.trustFolderAction(dir, via, &answer) })
	if answer == "" {
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	if r, ok := s.trustFolder(ctx, answer, via); !ok {
		return r
	}
	s.ask(func(context.Context) Result {
		s.trustRecheckAt = time.Time{}
		return Result{OK: true}
	})
	return Result{OK: true, Message: "Claude Code trusts " + answer + " now. Sessions stuck there come back by themselves."}
}

// trustFolderAction is the part of TrustFolder that runs on the loop. It
// sets answer to the folder whose question is still to be answered, off
// the loop, and leaves it empty when its own result is the answer.
func (s *Supervisor) trustFolderAction(dir string, via Via, answer *string) Result {
	dir, res, ok := s.projectFolder(dir)
	if !ok {
		return res
	}
	if trusted, _ := s.trustNow().Trusted(dir); trusted {
		s.trustRecheckAt = time.Time{}
		return Result{OK: true, Message: "Claude Code trusts " + dir + " already."}
	}
	if !s.env.Headless && s.deps.OpenTrustTerminal != nil {
		opened, err := s.deps.OpenTrustTerminal(dir)
		if err != nil {
			return Result{Message: "Could not open a terminal: " + err.Error() + ". Run `" + hosts.TrustCommandIn(dir) + "` in any terminal, answer Yes, then exit."}
		}
		s.logInfo(dir, "opened "+opened+" with Claude Code running, to give it its one-time OK for this folder"+via.Suffix())
		return Result{OK: true, Message: "Opened " + opened + " in " + dir + " with Claude Code running. Answer Yes to its question, then type /exit. Sessions stuck there come back by themselves."}
	}
	*answer = dir
	return Result{OK: true}
}
