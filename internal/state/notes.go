package state

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// startReasonFile holds the one-line reason the launcher started or
	// restarted the background copy, for that copy's first Activity line.
	startReasonFile = "start-reason"
	// loginStartFile notes that the launcher turned start at login on
	// once, so a later choice to turn it off is respected.
	loginStartFile = "login-start-offered"
)

// WriteStartReason notes why the launcher is about to start the background
// copy, which that copy reads once as it starts.
func WriteStartReason(dir, reason string) error {
	return writeNote(dir, startReasonFile, reason)
}

// TakeStartReason returns the reason WriteStartReason noted and removes
// it, so a later start, such as one at login, is not given the same
// reason. It is empty when there is none.
func TakeStartReason(dir string) string {
	path := filepath.Join(dir, startReasonFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	return strings.TrimSpace(string(data))
}

// MarkLoginStartOffered notes that the launcher turned start at login on.
func MarkLoginStartOffered(dir string) error {
	return writeNote(dir, loginStartFile, "start at login was turned on by default")
}

// LoginStartOffered reports whether MarkLoginStartOffered was noted.
func LoginStartOffered(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, loginStartFile))
	return err == nil && info.Mode().IsRegular()
}

// writeNote writes one line as the file name in dir, whole or not at all.
func writeNote(dir, name, line string) error {
	if err := EnsureDir(dir); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	tmp := path + ".tmp"
	if err := writeSynced(tmp, []byte(line+"\n")); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
