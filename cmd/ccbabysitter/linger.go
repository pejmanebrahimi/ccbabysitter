package main

import (
	"fmt"
	"io"
	"strings"

	"ccbabysitter.dev/ccbabysitter/internal/state"
)

// lingering is systemd's lingering for one user, which keeps their user
// services running after they log out and starts them at boot. The real
// one runs loginctl; tests use a fake so they never change the real
// setting.
type lingering interface {
	// Lingering reports whether lingering is on for user now, or an error
	// when that could not be found out.
	Lingering(user string) (bool, error)
	// SetLingering turns lingering on or off for user.
	SetLingering(user string, on bool) error
}

// turnLingeringOn turns lingering on for user so the service keeps
// running after they log out, and remembers in stateDir when it was CC
// Babysitter that turned it on, so uninstall turns it off only then.
// Lingering that is on already is left alone; it is not remembered unless
// CC Babysitter turned it on earlier, and then it stays remembered. When
// whether it was on cannot be found out, it is turned on but not
// remembered, so uninstall never turns off lingering it did not turn on.
//
// Turning it on can need rights the user does not have, so a failure is
// reported with the command to run, and the rest of the setup still
// counts as done.
func turnLingeringOn(out io.Writer, l lingering, stateDir, user string) {
	on, err := l.Lingering(user)
	if err == nil && on {
		return
	}
	wasOff := err == nil
	if err := l.SetLingering(user, true); err != nil {
		fmt.Fprintln(out, "loginctl enable-linger", user, "failed. Run it yourself to keep the service running after you log out:")
		fmt.Fprintln(out, "  sudo loginctl enable-linger", user)
		fmt.Fprintln(out, "Everything else is set up.")
		return
	}
	if wasOff {
		if err := state.MarkLingeringTurnedOn(stateDir); err != nil {
			fmt.Fprintln(out, "could not note that CC Babysitter turned lingering on, so uninstall will leave it on:", err)
		}
	}
}

// releaseLingering is uninstall's side of turnLingeringOn: it turns
// lingering off for user only when stateDir remembers that CC Babysitter
// turned it on, and otherwise says it left lingering as it was, since
// other services of the user's own may need it.
func releaseLingering(out io.Writer, l lingering, stateDir, user string) {
	if !state.LingeringTurnedOn(stateDir) {
		fmt.Fprintln(out, "Left lingering for", user, "as it was, since CC Babysitter has no note that it turned it on.")
		return
	}
	if err := l.SetLingering(user, false); err != nil {
		fmt.Fprintln(out, "loginctl disable-linger", user, "failed. Run it yourself if you no longer want lingering:")
		fmt.Fprintln(out, "  loginctl disable-linger", user)
		return
	}
	if err := state.ForgetLingeringTurnedOn(stateDir); err != nil {
		fmt.Fprintln(out, "could not remove the note that CC Babysitter turned lingering on:", err)
	}
	fmt.Fprintln(out, "Turned lingering off for", user+", which CC Babysitter had turned on.")
}

// lingerFromShowUser reads the answer to loginctl show-user <user> -p
// Linger, which is one line, Linger=yes or Linger=no.
func lingerFromShowUser(text string) (bool, error) {
	switch strings.TrimSpace(text) {
	case "Linger=yes":
		return true, nil
	case "Linger=no":
		return false, nil
	}
	return false, fmt.Errorf("loginctl said %q, not Linger=yes or Linger=no", strings.TrimSpace(text))
}
