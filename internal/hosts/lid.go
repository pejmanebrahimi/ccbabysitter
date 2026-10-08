package hosts

import (
	"regexp"
	"strconv"
	"strings"
)

// darwinLidSleeps reads ioreg's clamshell entry: closing the lid puts the
// Mac to sleep. A Mac on power with an external display keeps running with
// its lid shut, and a Mac with no lid has no entry at all.
func darwinLidSleeps(ioregOut string) bool {
	return strings.Contains(ioregOut, `"AppleClamshellCausesSleep" = Yes`)
}

// powerIndex is how powercfg writes a setting's current value, in hex.
var powerIndex = regexp.MustCompile(`0x[0-9A-Fa-f]{8}`)

// windowsLidSleeps reads powercfg's answer about the lid close action: the
// last two values it writes are the current ones, plugged in and then on
// battery. 0 does nothing; sleeping, hibernating and shutting down all take
// the computer away. A plan with no lid action has no lid.
func windowsLidSleeps(powercfgOut string, onAC bool) bool {
	found := powerIndex.FindAllString(powercfgOut, -1)
	if len(found) < 2 {
		return false
	}
	value := found[len(found)-1]
	if onAC {
		value = found[len(found)-2]
	}
	n, err := strconv.ParseUint(value[2:], 16, 32)
	return err == nil && n != 0
}

// linuxLidSleeps reads busctl's answer about logind, the five properties
// Docked, OnExternalPower, HandleLidSwitch, HandleLidSwitchExternalPower
// and HandleLidSwitchDocked, one per line, or only HandleLidSwitch from an
// older logind. The action that counts is the one for how the computer is
// now, and only ignore and lock leave it running. An empty action on
// external power is the battery one.
func linuxLidSleeps(busctlOut string) bool {
	var values []string
	for _, line := range strings.Split(strings.TrimSpace(busctlOut), "\n") {
		kind, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if kind == "s" {
			value = strings.Trim(value, `"`)
		}
		values = append(values, value)
	}
	action := ""
	switch len(values) {
	case 1:
		action = values[0]
	case 5:
		docked, onPower := values[0] == "true", values[1] == "true"
		action = values[2]
		switch {
		case docked:
			action = values[4]
		case onPower && values[3] != "":
			action = values[3]
		}
	default:
		return false
	}
	return action != "" && action != "ignore" && action != "lock"
}
