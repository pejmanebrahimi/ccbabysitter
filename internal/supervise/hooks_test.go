package supervise

import (
	"context"

	"ccbabysitter.dev/ccbabysitter/internal/claude"
	"ccbabysitter.dev/ccbabysitter/internal/observe"
	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// The hooks in this file let a test reach the supervisor's own state
// without racing the goroutine that owns it: each one is posted through
// ask, so it runs exactly where every other change to that state runs.

// addWatchForTest installs a ready made watch, as if the user had asked
// for it, and persists the result.
func (s *Supervisor) addWatchForTest(w state.Watch) Result {
	return s.ask(func(context.Context) Result {
		s.st.Watches = append(s.st.Watches, w)
		s.seen[w.SessionID] = true
		s.persist()
		return Result{OK: true}
	})
}

// reconcileForTest runs one reconcile pass over a chosen snapshot, so a
// test can drive the loop a known number of times instead of waiting for
// the observer to deliver one.
func (s *Supervisor) reconcileForTest(snap observe.Snapshot) Result {
	return s.ask(func(ctx context.Context) Result {
		s.reconcile(ctx, snap)
		return Result{OK: true}
	})
}

// absentForTest reports how many consecutive snapshots have found no live
// session for id.
func (s *Supervisor) absentForTest(id string) int {
	n := 0
	s.ask(func(context.Context) Result {
		n = s.absent[id]
		return Result{OK: true}
	})
	return n
}

// watchForTest reads one watch out of the state the loop owns. A test that
// drives reconcile directly, rather than through the loop, has no
// published view to read, since only the loop publishes one.
func (s *Supervisor) watchForTest(id string) (state.Watch, bool) {
	var found state.Watch
	ok := false
	s.ask(func(context.Context) Result {
		if w := s.find(id); w != nil {
			found, ok = *w, true
		}
		return Result{OK: true}
	})
	return found, ok
}

// trustReadForTest reports whether an answer about the CLI's settings file
// has come back from the stats worker and been taken in by the loop.
func (s *Supervisor) trustReadForTest() bool {
	read := false
	s.ask(func(context.Context) Result {
		read = !s.trustAt.IsZero() && !s.trustInFlight
		return Result{OK: true}
	})
	return read
}

// handedBackForTest reports what the supervisor remembers about handing a
// session back to its app.
func (s *Supervisor) handedBackForTest(id string) (claude.Host, bool) {
	var to claude.Host
	ok := false
	s.ask(func(context.Context) Result {
		var hb handBack
		hb, ok = s.handedBack[id]
		to = hb.to
		return Result{OK: true}
	})
	return to, ok
}

// keptForTest reports whether anything is still kept about why id went
// down.
func (s *Supervisor) keptForTest(id string) bool {
	kept := false
	s.ask(func(context.Context) Result {
		_, host := s.lastHost[id]
		_, cause := s.causes[id]
		_, absent := s.absent[id]
		kept = host || cause || absent || s.announced[id]
		return Result{OK: true}
	})
	return kept
}
