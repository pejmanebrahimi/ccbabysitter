package claude

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// The screen the Claude Code CLI draws when it asks whether to trust a
// folder, as its terminal output arrives: colours, cursor moves and the
// marker before the chosen option.
func trustScreen(onYes bool) string {
	no, yes := "  No, exit", "  Yes, I trust this folder"
	if onYes {
		yes = "❯ Yes, I trust this folder"
	} else {
		no = "❯ No, exit"
	}
	return "\x1b[2K\x1b[1A\x1b[38;5;214mAccessing workspace:\x1b[39m\r\n" +
		"/srv/project\r\n\r\n" +
		"Quick safety check: Is this a project you created or one you trust? (Like your\r\n" +
		"own code, a well-known open source project, or work from your team). If not,\r\n" +
		"take a moment to review what's in this folder first.\r\n\r\n" +
		"Claude Code'll be able to read, edit, and execute files here.\r\n\r\n" +
		"\x1b[36m" + no + "\x1b[39m\r\n" +
		"\x1b[36m" + yes + "\x1b[39m\r\n\r\n" +
		"Enter to confirm · Esc to cancel\r\n"
}

func TestReadTrustScreen(t *testing.T) {
	for _, c := range []struct {
		name         string
		screen       string
		asked, onYes bool
	}{
		{"nothing yet", "\x1b[?25l", false, false},
		{"asked, on No", trustScreen(false), true, false},
		{"asked, on Yes", trustScreen(true), true, true},
		{"a redraw after Down", trustScreen(false) + "\x1b[3A\x1b[2K" + trustScreen(true), true, true},
		{"a redraw back to No", trustScreen(true) + trustScreen(false), true, false},
		{"another question", "Do you want to continue?\r\n❯ 1. Yes\r\n  2. No\r\n", false, false},
	} {
		asked, onYes := readTrustScreen(c.screen)
		if asked != c.asked || onYes != c.onYes {
			t.Errorf("%s: asked %v onYes %v, want %v %v", c.name, asked, onYes, c.asked, c.onYes)
		}
	}
}

// fakeTerminal plays the CLI's side of the pseudo-terminal: it draws a
// screen when started and answers each key with another one.
type fakeTerminal struct {
	mu     sync.Mutex
	out    chan []byte
	typed  []string
	ended  bool
	answer func(key string) string
}

func newFakeTerminal(first string, answer func(key string) string) *fakeTerminal {
	f := &fakeTerminal{out: make(chan []byte, 16), answer: answer}
	if first != "" {
		f.out <- []byte(first)
	}
	return f
}

func (f *fakeTerminal) Write(p []byte) (int, error) {
	f.mu.Lock()
	key := string(p)
	f.typed = append(f.typed, key)
	answer := f.answer
	f.mu.Unlock()
	if answer != nil {
		if next := answer(key); next != "" {
			f.out <- []byte(next)
		}
	}
	return len(p), nil
}

func (f *fakeTerminal) Output() <-chan []byte { return f.out }

func (f *fakeTerminal) End() error {
	f.mu.Lock()
	f.ended = true
	f.mu.Unlock()
	return nil
}

func (f *fakeTerminal) keys() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, k := range f.typed {
		switch k {
		case keyDown:
			names = append(names, "down")
		case keyEnter:
			names = append(names, "enter")
		default:
			names = append(names, strings.TrimSpace(k))
		}
	}
	return strings.Join(names, " ")
}

var quick = trustTimings{ask: 300 * time.Millisecond, settle: 30 * time.Millisecond, after: 30 * time.Millisecond}

// The CLI's default is No. Accepting moves to Yes first, sees the marker
// there, and only then presses Enter.
func TestAcceptTrustMovesToYesBeforeEnter(t *testing.T) {
	f := newFakeTerminal(trustScreen(false), func(key string) string {
		if key == keyDown {
			return trustScreen(true)
		}
		return ""
	})
	if err := acceptTrust(context.Background(), f, quick); err != nil {
		t.Fatal(err)
	}
	if got := f.keys(); got != "down enter" {
		t.Errorf("keys %q, want %q", got, "down enter")
	}
}

// When Yes is chosen already, Enter is the only key.
func TestAcceptTrustOnYesPressesEnter(t *testing.T) {
	f := newFakeTerminal(trustScreen(true), nil)
	if err := acceptTrust(context.Background(), f, quick); err != nil {
		t.Fatal(err)
	}
	if got := f.keys(); got != "enter" {
		t.Errorf("keys %q, want %q", got, "enter")
	}
}

// When the CLI never asks, as in a folder it trusts already or when its
// wording has changed, nothing is typed.
func TestAcceptTrustWithoutTheQuestionTypesNothing(t *testing.T) {
	f := newFakeTerminal("Welcome to Claude Code\r\n> ", nil)
	err := acceptTrust(context.Background(), f, quick)
	if !errors.Is(err, ErrTrustNotAsked) {
		t.Fatalf("err %v, want ErrTrustNotAsked", err)
	}
	if got := f.keys(); got != "" {
		t.Errorf("keys %q, want none", got)
	}
}

// When Down never brings the marker to Yes, Enter is never pressed, so
// nothing is answered that was not chosen.
func TestAcceptTrustNeverConfirmsWithoutYes(t *testing.T) {
	f := newFakeTerminal(trustScreen(false), func(key string) string {
		if key == keyDown {
			return trustScreen(false)
		}
		return ""
	})
	err := acceptTrust(context.Background(), f, quick)
	if !errors.Is(err, ErrTrustNoYes) {
		t.Fatalf("err %v, want ErrTrustNoYes", err)
	}
	if strings.Contains(f.keys(), "enter") {
		t.Errorf("Enter was pressed: %q", f.keys())
	}
}

// A context that ends stops the wait for the question.
func TestAcceptTrustStopsWithTheContext(t *testing.T) {
	f := newFakeTerminal("", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := acceptTrust(ctx, f, trustTimings{ask: time.Minute, settle: time.Millisecond, after: time.Millisecond}); err == nil {
		t.Fatal("no error after the context ended")
	}
}
