package state

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// startReasonFile holds the one-line reason the launcher started or
	// restarted the background copy, for that copy's first Activity line.
	startReasonFile = "start-reason"
	// loginStartFile notes that the launcher turned start at login on
	// once, so a later choice to turn it off is respected.
	loginStartFile = "login-start-offered"
	// serviceRunningFile holds the creation time of the background copy
	// that runs now, removed when it ends cleanly.
	serviceRunningFile = "service-running"
)

// LoginItemNoteFile holds the one-line reason CC Babysitter wrote the login
// item itself, for the next copy's Activity. The launcher writes it, and so
// does scripts/install.ps1 when it replaces an earlier version's Startup
// folder script, so its name and its one plain line are shared with that
// script.
const LoginItemNoteFile = "login-item-note"

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

// WriteLoginItemNote notes why CC Babysitter wrote the login item itself,
// such as replacing an earlier version's, which the next copy reads once as
// it starts.
func WriteLoginItemNote(dir, line string) error {
	return writeNote(dir, LoginItemNoteFile, line)
}

// TakeLoginItemNote returns the line WriteLoginItemNote noted and removes
// it. It is empty when there is none.
func TakeLoginItemNote(dir string) string {
	path := filepath.Join(dir, LoginItemNoteFile)
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

// ForgetLoginStartOffered removes that note, so the next plain run turns
// start at login on again. A note that is not there is not an error.
func ForgetLoginStartOffered(dir string) error {
	err := os.Remove(filepath.Join(dir, loginStartFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// LoginStartOffered reports whether MarkLoginStartOffered was noted.
func LoginStartOffered(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, loginStartFile))
	return err == nil && info.Mode().IsRegular()
}

// MarkServiceRunning notes that the background copy created at createMs,
// milliseconds since the Unix epoch, runs now.
func MarkServiceRunning(dir string, createMs int64) error {
	return writeNote(dir, serviceRunningFile, strconv.FormatInt(createMs, 10))
}

// ClearServiceRunning removes that note, as the background copy ends
// cleanly.
func ClearServiceRunning(dir string) {
	_ = os.Remove(filepath.Join(dir, serviceRunningFile))
}

// TakeServiceRunning returns the creation time an earlier background copy
// noted and removes the note. ok is true only when there was one, which
// means that copy did not end cleanly.
func TakeServiceRunning(dir string) (createMs int64, ok bool) {
	path := filepath.Join(dir, serviceRunningFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	_ = os.Remove(path)
	createMs, err = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	return createMs, err == nil
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
