package supervise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// StartRefusedWithDisplay is the answer to starting a session on a machine
// with a display, where people start sessions in their own apps.
const StartRefusedWithDisplay = "Starting a session from here is for a machine with no display. Start it in your terminal or Claude app."

// TrustQuestion asks whether to trust dir, in the claude CLI's own terms,
// before this program answers the CLI's question for the person.
func TrustQuestion(dir string) string {
	return "Claude Code has not been allowed to run in " + dir + " yet. Trust this folder? Claude Code will be able to read, edit and run files in it."
}

// Start starts a new background session with Remote Control in dir, on a
// machine with no display, and babysits it once it shows up. When the
// claude CLI does not trust dir yet, it answers with NeedsTrust unless
// trust is set, the person's yes, and then it answers the CLI's own
// question whether to trust the folder before starting.
func (s *Supervisor) Start(dir string, trust bool, via Via) Result {
	return s.ask(func(ctx context.Context) Result { return s.start(ctx, dir, trust, via) })
}

func (s *Supervisor) start(ctx context.Context, dir string, trust bool, via Via) Result {
	if !s.env.Headless {
		return Result{Message: StartRefusedWithDisplay}
	}
	dir = strings.TrimSpace(dir)
	switch {
	case dir == "":
		return Result{Message: "Name the folder to start the session in."}
	case !filepath.IsAbs(dir):
		return Result{Message: "Give the folder's full path, starting with /."}
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return Result{Message: "There is no folder at " + dir + "."}
	case !info.IsDir():
		return Result{Message: dir + " is a file, not a folder."}
	case s.deps.Home != "" && dir == filepath.Clean(s.deps.Home):
		return Result{Message: "Claude Code does not start background sessions in the home folder. Choose a project folder."}
	}

	if trusted, known := s.trustNow().Trusted(dir); known && !trusted {
		if !trust {
			return Result{Message: TrustQuestion(dir), NeedsTrust: true}
		}
		if res, ok := s.trustFolder(ctx, dir, via); !ok {
			return res
		}
	}

	out, runErr := s.deps.Runner.Run(ctx, dir, hosts.NewBackgroundArgs()...)
	short, ok := claude.ParseBackgrounded(out)
	if !ok {
		return Result{Message: "The session did not start: " + why(out, runErr) + ". Start it by hand: " + hosts.NewBackgroundCommandIn(dir)}
	}
	s.started[short] = true
	s.logInfo(dir, "started background session "+short+" with Remote Control"+via.Suffix())
	return Result{OK: true, ShortID: short,
		Message: "Started a new session in " + dir + " as background session " + short + ", with Remote Control. It is babysat."}
}

// trustFolder answers the claude CLI's question whether to trust dir with
// Yes, then reads the CLI's settings to be sure the folder is trusted.
// When either part does not work it gives the one-time command to run.
func (s *Supervisor) trustFolder(ctx context.Context, dir string, via Via) (Result, bool) {
	byHand := " Run `" + hosts.TrustCommandIn(dir) + "` on this machine once, answer Yes, then exit, and try again."
	if s.deps.AcceptTrust == nil {
		return Result{Message: "Claude Code's question whether to trust the folder cannot be answered here." + byHand}, false
	}
	if err := s.deps.AcceptTrust(ctx, dir); err != nil {
		reason := err.Error()
		if errors.Is(err, claude.ErrTrustNotAsked) || errors.Is(err, claude.ErrTrustNoYes) {
			reason = strings.TrimSuffix(reason, ".")
		}
		return Result{Message: "Could not trust the folder: " + reason + "." + byHand}, false
	}
	if trusted, known := s.trustNow().Trusted(dir); !known || !trusted {
		return Result{Message: "Claude Code was answered Yes but does not show the folder as trusted." + byHand}, false
	}
	s.logInfo(dir, "trusted the folder for Claude Code, as asked"+via.Suffix())
	return Result{}, true
}

// trustNow reads the claude CLI's trust flags from its settings file as
// they are this moment. The reading the page uses is a few seconds old at
// most, which is too old just after the CLI recorded a trust.
func (s *Supervisor) trustNow() claude.Trust {
	return (&claude.TrustFile{Path: s.deps.ClaudeConfig}).Read(s.deps.Now())
}
