package supervise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// StartRefusedWithDisplay is the answer to starting a session on a machine
// with a display, where people start sessions in their own apps.
const StartRefusedWithDisplay = "Starting a session from here is for a machine with no display. Start it in your terminal or Claude app."

// StartNotLoggedIn is the answer to starting a session while the claude
// CLI is not logged in, when it could reach neither the model nor Remote
// Control. It says what the page's note about the login says.
const StartNotLoggedIn = "Claude Code is not logged in on this machine. Run `claude` once and log in, then try again."

// startedWait is how long a session Start started is waited for, to be
// babysat when it shows up, before it is no longer looked for.
const startedWait = 2 * time.Minute

// startTimeout bounds the slow part of a start: answering the CLI's trust
// question and starting the background session.
const startTimeout = 2 * time.Minute

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
//
// The checks and the bookkeeping run on the loop. Answering the question
// and starting the session take seconds, so they run here, on the
// caller's goroutine, and the loop goes on babysitting meanwhile.
func (s *Supervisor) Start(dir string, trust bool, via Via) Result {
	var plan startPlan
	if res := s.ask(func(context.Context) Result { return s.planStart(dir, trust, &plan) }); !res.OK {
		return res
	}
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()
	// The login answer the loop holds can be a minute old, so one that
	// says logged out is asked again before the start is refused.
	if plan.loggedOut && hosts.AskLogin(ctx, s.deps.Runner) == hosts.LoginNo {
		return Result{Message: StartNotLoggedIn}
	}
	if plan.trust {
		if res, ok := s.trustFolder(ctx, plan.dir, via); !ok {
			return res
		}
	}
	out, runErr := s.deps.Runner.Run(ctx, plan.dir, hosts.NewBackgroundArgs()...)
	short, ok := claude.ParseBackgrounded(out)
	short = strings.ToLower(short)
	if !ok {
		reason := why(out, runErr)
		s.logInfo(plan.dir, "the new session did not start: "+reason+via.Suffix())
		return Result{Message: "The session did not start: " + reason + ". Start it by hand: " + hosts.NewBackgroundCommandIn(plan.dir)}
	}
	return s.ask(func(context.Context) Result {
		s.started[short] = s.deps.Now()
		s.logInfo(plan.dir, "started background session "+short+" with Remote Control"+via.Suffix())
		return Result{OK: true, ShortID: short,
			Message: "Started a new session in " + plan.dir + " as background session " + short + ", with Remote Control. It is babysat."}
	})
}

// startPlan is what the checks decided: the folder, by its real path,
// whether the CLI's trust question has to be answered first, and whether
// the CLI last said it is logged out.
type startPlan struct {
	dir       string
	trust     bool
	loggedOut bool
}

// planStart checks dir and the CLI's trust, and fills plan when the start
// can go ahead. Every refusal says why.
func (s *Supervisor) planStart(dir string, trust bool, plan *startPlan) Result {
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
	}
	// Claude Code records trust under a folder's real path, so a folder
	// reached through a link is checked, trusted and started by its own.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	home := realPath(s.deps.Home)
	switch {
	case home != "" && dir == home:
		return Result{Message: "Claude Code does not start background sessions in the home folder. Choose a project folder."}
	case filepath.Dir(dir) == dir || (home != "" && holds(dir, home)):
		return Result{Message: dir + " holds your home folder. Choose a project folder."}
	}
	plan.dir = dir
	plan.loggedOut = s.env.CLILoggedIn == hosts.LoginNo
	// A folder is taken as trusted only when Claude Code's settings say so.
	// When they cannot be read, as before Claude Code has ever run here, it
	// is not.
	if trusted, _ := s.trustNow().Trusted(dir); !trusted {
		if !trust {
			return Result{Message: TrustQuestion(dir), NeedsTrust: true}
		}
		plan.trust = true
	}
	return Result{OK: true}
}

// holds reports whether the folder dir has inside path, at any depth.
func holds(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath is p by its real path, or p cleaned when that cannot be told,
// and "" for "".
func realPath(p string) string {
	if p == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return filepath.Clean(p)
}

// trustFolder answers the claude CLI's question whether to trust dir with
// Yes, then reads the CLI's settings to be sure the folder is trusted.
// When either part does not work it gives the one-time command to run.
func (s *Supervisor) trustFolder(ctx context.Context, dir string, via Via) (Result, bool) {
	byHand := " Run `" + hosts.TrustCommandIn(dir) + "` on this machine once, answer Yes, then exit, and try again."
	fail := func(reason string) (Result, bool) {
		s.logInfo(dir, "could not trust the folder for Claude Code: "+reason+via.Suffix())
		return Result{Message: "Could not trust the folder: " + reason + "." + byHand}, false
	}
	if s.deps.AcceptTrust == nil {
		return fail("Claude Code's question cannot be answered on this system")
	}
	if err := s.deps.AcceptTrust(ctx, dir); err != nil {
		reason := err.Error()
		if errors.Is(err, claude.ErrTrustNotAsked) || errors.Is(err, claude.ErrTrustNoYes) {
			reason = strings.TrimSuffix(reason, ".")
		}
		return fail(reason)
	}
	if trusted, known := s.trustNow().Trusted(dir); !known || !trusted {
		return fail("Claude Code was answered Yes but does not show the folder as trusted")
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
