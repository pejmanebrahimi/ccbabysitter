package supervise

import (
	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// WatchState is what a watch is doing right now. The card, the mascot and
// the one action the page offers for a watch all follow it.
type WatchState string

const (
	// StateWatching means the session is live in an app. The watch keeps
	// the computer awake and stands by.
	StateWatching WatchState = "watching"
	// StateInBackground means the session's app went away and our own
	// background copy carries the session, reachable through Remote
	// Control.
	StateInBackground WatchState = "background"
	// StateStarting means the session is running nowhere and the watch is
	// about to start, or start again, its background copy.
	StateStarting WatchState = "starting"
	// StateStuck means three starts failed within five minutes, or its
	// background copy froze three times within two hours. Nothing is tried
	// again until the person asks.
	StateStuck WatchState = "stuck"
)

// StateOf reads a watch's state off the sessions live for it right now.
//
// A watch whose copy is gone while an app shows the session is Watching:
// the next reconcile pass hands the watch to that app, and until then the
// session is plainly running there.
func StateOf(w state.Watch, live []claude.Session) WatchState {
	if w.Paused {
		return StateStuck
	}
	if len(live) == 0 {
		return StateStarting
	}
	if w.PromiseState == "fallback" {
		for _, sn := range live {
			if sn.Host == claude.HostBackground {
				return StateInBackground
			}
		}
	}
	return StateWatching
}
