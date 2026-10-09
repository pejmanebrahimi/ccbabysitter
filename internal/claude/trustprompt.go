package claude

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// The Claude Code CLI starts a background session only in a folder it
// trusts, and it asks whether to trust one only when it runs on a
// terminal. AcceptTrust answers that question the way a person would, so a
// person who has said yes on the page does not need a shell for it. It
// never writes the CLI's files: the CLI records the trust itself.

// ErrTrustNotAsked is returned when the CLI did not ask whether to trust
// the folder: it trusts it already, or its question reads differently now.
var ErrTrustNotAsked = errors.New("Claude Code did not ask whether to trust the folder")

// ErrTrustNoYes is returned when the CLI asked but its "Yes, I trust this
// folder" could not be chosen, so nothing was answered.
var ErrTrustNoYes = errors.New("could not choose Yes in Claude Code's trust question")

// The keys a person presses to answer the question.
const (
	keyDown  = "\x1b[B"
	keyEnter = "\r"
)

// selected is the mark the CLI draws before the option that is chosen.
const selected = "\u276f"

// ansiCode matches the terminal's control sequences: colours, cursor
// moves, screen clearing and window titles.
var ansiCode = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()][0-9A-Za-z]|\x1b[=>78]`)

// flat is a screen's text with the control sequences and every space and
// line break taken out, in lower case. The CLI places words with cursor
// moves rather than spaces, so only the letters are worth comparing.
func flat(screen string) string {
	s := ansiCode.ReplaceAllString(screen, "")
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}

// readTrustScreen reports whether the screen so far shows the CLI's trust
// question, with both of its answers, and whether the last option marked
// as chosen is "Yes, I trust this folder".
func readTrustScreen(screen string) (asked, onYes bool) {
	s := flat(screen)
	asked = strings.Contains(s, "yes,itrustthisfolder") && strings.Contains(s, "no,exit")
	if !asked {
		return false, false
	}
	i := strings.LastIndex(s, selected)
	if i < 0 {
		return true, false
	}
	// The options may be numbered: the mark, then "2.", then the words.
	rest := strings.TrimLeft(s[i+len(selected):], "0123456789")
	rest = strings.TrimPrefix(rest, ".")
	return true, strings.HasPrefix(rest, "yes,itrustthisfolder")
}

// trustTerminal is the CLI running on a pseudo-terminal: what is typed
// goes to it, its screen comes back on Output, which closes when it
// exits, and End ends the process if it is still running and waits for
// it.
type trustTerminal interface {
	io.Writer
	Output() <-chan []byte
	End() error
}

// trustTimings are how long to wait for the question, for the screen to
// redraw after a key, and after Enter for the CLI to record the answer.
type trustTimings struct {
	ask, settle, after time.Duration
}

var defaultTrustTimings = trustTimings{ask: 30 * time.Second, settle: 800 * time.Millisecond, after: 3 * time.Second}

// startTrustTerminal starts the CLI bin on a pseudo-terminal in dir. It is
// set on the systems that have one and nil on the others.
var startTrustTerminal func(ctx context.Context, bin, dir string) (trustTerminal, error)

// AcceptTrust runs the CLI bin in dir on a pseudo-terminal and answers its
// question whether to trust the folder with "Yes, I trust this folder",
// then ends the CLI without sending a message, so no conversation is
// saved. It returns ErrTrustNotAsked when there was no such question and
// ErrTrustNoYes when Yes could not be chosen. The caller checks the CLI's
// settings afterwards to know the folder is trusted.
func AcceptTrust(ctx context.Context, bin, dir string) error {
	if startTrustTerminal == nil {
		return errors.New("this system has no pseudo-terminal to run Claude Code on")
	}
	if bin == "" {
		bin = FindCLI()
	}
	if _, err := exec.LookPath(bin); err != nil {
		return errors.New("Claude Code's command was not found")
	}
	term, err := startTrustTerminal(ctx, bin, dir)
	if err != nil {
		return err
	}
	defer term.End()
	return acceptTrust(ctx, term, defaultTrustTimings)
}

// acceptTrust answers the question on term: it waits for the question,
// moves the choice to Yes one Down at a time, checking the mark after each,
// and presses Enter only once the mark is on Yes.
func acceptTrust(ctx context.Context, term trustTerminal, t trustTimings) error {
	var screen strings.Builder
	// read takes in the screen until the question shows, when untilAsked,
	// or until it has been quiet for d, so a screen that arrives in pieces
	// is read whole before anything is decided from it.
	read := func(d time.Duration, untilAsked bool) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		for {
			select {
			case b, ok := <-term.Output():
				if !ok {
					return io.EOF
				}
				screen.Write(b)
				if untilAsked {
					if asked, _ := readTrustScreen(screen.String()); asked {
						return nil
					}
				} else {
					timer.Reset(d)
				}
			case <-timer.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := read(t.ask, true); err != nil && err != io.EOF {
		return err
	}
	asked, onYes := readTrustScreen(screen.String())
	if !asked {
		return ErrTrustNotAsked
	}
	// Let the question finish drawing before the first key.
	if err := read(t.settle, false); err != nil && err != io.EOF {
		return err
	}
	for tries := 0; ; tries++ {
		if _, onYes = readTrustScreen(screen.String()); onYes {
			break
		}
		if tries == 3 {
			return ErrTrustNoYes
		}
		if _, err := io.WriteString(term, keyDown); err != nil {
			return ErrTrustNoYes
		}
		if err := read(t.settle, false); err != nil && err != io.EOF {
			return err
		}
	}
	if _, err := io.WriteString(term, keyEnter); err != nil {
		return err
	}
	if err := read(t.after, false); err != nil && err != io.EOF {
		return err
	}
	return nil
}
