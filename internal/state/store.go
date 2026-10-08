// Package state persists CC Babysitter's own data in one folder. It never
// reads or writes anything under Claude Code's own directories.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ccbabysitter.dev/ccbabysitter/internal/buildinfo"
	"ccbabysitter.dev/ccbabysitter/internal/claude"
)

// dirMode and fileMode are what this program's own folder and files are
// created with: readable and writable by the account that runs it and by
// nobody else. Windows ignores these bits, and every attempt to apply them
// there is allowed to fail quietly for that reason.
const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
	// parentDirMode is what a folder on the way to ours is created with
	// when it does not exist yet. It belongs to the account, not to this
	// program, so it gets the permissions anything else would have given
	// it rather than ours.
	parentDirMode os.FileMode = 0o755
)

// Settings holds the user's preferences, stored alongside the watch list.
type Settings struct {
	// KeepAwakeMode is kept so that a file written by another build still
	// loads. The computer is kept awake while at least one session is
	// babysat, which is not a preference, and this field always reads back
	// as that rule.
	KeepAwakeMode string `json:"keepAwakeMode"`
	Autostart     bool   `json:"autostart"`
	// AutoBabysit is whether, on a machine with no display, every new
	// background session is babysat as soon as it starts. It is on unless
	// the user switched it off: a saved file that does not mention it,
	// including one from a build that kept this choice under another name
	// and started it off, loads with it on.
	AutoBabysit     bool   `json:"autoBabysit"`
	AutoOpenBrowser bool   `json:"autoOpenBrowser"`
	Theme           string `json:"theme"` // dark | light | auto
}

// The themes the page offers. Auto follows the light or dark mode of the
// computer showing the page.
const (
	ThemeDark  = "dark"
	ThemeLight = "light"
	ThemeAuto  = "auto"
)

// ValidTheme reports whether theme is one the page offers.
func ValidTheme(theme string) bool {
	switch theme {
	case ThemeDark, ThemeLight, ThemeAuto:
		return true
	}
	return false
}

// KeepAwakeWhileBabysitting is the one keep-awake rule there is: the
// request is held while at least one watch is active.
const KeepAwakeWhileBabysitting = "babysitting"

// Watch is one session CC Babysitter is tracking or has tracked.
type Watch struct {
	SessionID           string      `json:"sessionId"`
	ShortID             string      `json:"shortId"`
	Name                string      `json:"name"`
	Cwd                 string      `json:"cwd"`
	OriginHost          claude.Host `json:"originHost"`
	OriginRemoteControl bool        `json:"originRemoteControl"`
	HasSavedOptions     bool        `json:"hasSavedOptions"`
	WatchedSince        time.Time   `json:"watchedSince"`
	Paused              bool        `json:"paused"`
	PauseReason         string      `json:"pauseReason,omitempty"`
	Failures            []time.Time `json:"failures"`
	LastResume          time.Time   `json:"lastResume"`
	PromiseState        string      `json:"promiseState"` // inplace | fallback | paused
	// BackgroundSince is when the watch last went to the background: when
	// its app went away and a background copy took the session over. It is
	// kept while the copy is started again and cleared once the watch
	// leaves the background.
	BackgroundSince time.Time `json:"backgroundSince,omitzero"`
	// BackgroundCause is why the watch went to the background: the reason
	// its first rescue gave in Activity. It is kept and cleared with
	// BackgroundSince, and empty when not known.
	BackgroundCause string `json:"backgroundCause,omitempty"`
}

// State is the entire contents of state.json. A file written by another
// build may carry keys this one does not know; they are ignored on read and
// not written back.
type State struct {
	Version  string   `json:"version"`
	Settings Settings `json:"settings"`
	Watches  []Watch  `json:"watches"`
	// BootTime is when the computer had booted, in seconds since the Unix
	// epoch, as CC Babysitter last started. Another one at the next start
	// means the computer restarted in between. Zero when not known.
	BootTime uint64 `json:"bootTime,omitempty"`
	// BootID is the id the system gave the boot CC Babysitter last started
	// in, which tells a restart more surely than the boot time, which moves
	// with the clock. Empty where the system gives none.
	BootID string `json:"bootId,omitempty"`
}

// Find returns the watch with the given session id, or nil.
func (s *State) Find(id string) *Watch {
	for i := range s.Watches {
		if s.Watches[i].SessionID == id {
			return &s.Watches[i]
		}
	}
	return nil
}

// DefaultSettings returns the settings a fresh install starts with.
func DefaultSettings() Settings {
	return Settings{
		KeepAwakeMode:   KeepAwakeWhileBabysitting,
		AutoBabysit:     true,
		AutoOpenBrowser: true,
		Theme:           ThemeDark,
	}
}

// DefaultDir is the app's own folder: %LOCALAPPDATA%\CCBabysitter on Windows,
// $XDG_DATA_HOME/ccbabysitter or ~/.local/share/ccbabysitter elsewhere.
func DefaultDir() string {
	if runtime.GOOS == "windows" {
		if l := os.Getenv("LOCALAPPDATA"); l != "" {
			return filepath.Join(l, "CCBabysitter")
		}
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "ccbabysitter")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "ccbabysitter")
}

// Store reads and writes state.json in Dir.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func (s *Store) path() string { return filepath.Join(s.Dir, "state.json") }

// HealReport says whether a saved state file had to be set aside to carry
// on, and why. A caller that can tell the user is expected to: silently
// starting from nothing after a file was put out of the way would look
// exactly like every babysat session having been forgotten for no reason.
// Reason is a whole sentence, meant to be shown exactly as it is.
type HealReport struct {
	Healed bool
	Reason string
}

// The two reasons a saved state file is set aside, each written as the
// sentence the user reads. Both lead with what actually happened rather
// than with the consequence, so the first words say which of the two it
// was.
const (
	healUnreadable = "saved state could not be read and was moved to state.json.bad, so no sessions are being babysat"
	healVersion    = "saved state was written by version %s and cannot be used by %s, so it was moved to state.json.bad and no sessions are being babysat"
)

// Load reads state.json, starting from defaults when it cannot be used.
// Use LoadReport where the difference matters.
func (s *Store) Load() (State, error) {
	st, _, err := s.LoadReport()
	return st, err
}

// LoadReport reads state.json and also says whether the file that was
// there had to be moved aside. A file that cannot be parsed, and one saved
// by a version of this program whose state this one cannot read, are both
// renamed to state.json.bad and replaced by fresh defaults.
func (s *Store) LoadReport() (State, HealReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := State{Version: buildinfo.Version, Settings: DefaultSettings()}
	data, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return fresh, HealReport{}, nil
	}
	if err != nil {
		return State{}, HealReport{}, err
	}
	st := beforeDecode()
	if err := json.Unmarshal(data, &st); err != nil {
		if err := s.setAside(); err != nil {
			return State{}, HealReport{}, err
		}
		return fresh, HealReport{Healed: true, Reason: healUnreadable}, nil
	}
	if reason, incompatible := incompatibleVersion(st.Version); incompatible {
		if err := s.setAside(); err != nil {
			return State{}, HealReport{}, err
		}
		return fresh, HealReport{Healed: true, Reason: reason}, nil
	}
	normalize(&st)
	return st, HealReport{}, nil
}

// setAside renames state.json to state.json.bad, replacing any earlier
// one, so the file that could not be used is still there to look at.
func (s *Store) setAside() error {
	_ = os.Remove(s.path() + ".bad")
	return os.Rename(s.path(), s.path()+".bad")
}

// incompatibleVersion reports whether a stored version's major number
// differs from the running one, which is what a major number is for: the
// state file's shape changed and this build cannot read the old one. A
// file with no version at all, or one whose version cannot be read as a
// number, predates this check and is given the benefit of the doubt, since
// everything about its shape is still understood.
func incompatibleVersion(stored string) (string, bool) {
	storedMajor, ok := majorOf(stored)
	if !ok {
		return "", false
	}
	runningMajor, ok := majorOf(buildinfo.Version)
	if !ok || storedMajor == runningMajor {
		return "", false
	}
	return fmt.Sprintf(healVersion, stored, buildinfo.Version), true
}

// majorOf reads the leading MAJOR number out of a MAJOR.MINOR.PATCH
// version string.
func majorOf(version string) (int, bool) {
	head, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0, false
	}
	return n, true
}

// beforeDecode is what a saved file is read into. Decoding leaves a field
// the file does not mention as it was, so a setting whose absence means
// on is set here and survives only when the file says nothing about it.
func beforeDecode() State {
	return State{Settings: Settings{AutoBabysit: true}}
}

// normalize fills in the settings a saved file left out, forces the one
// that is not a choice and names the app of a session this program
// started, so the rest of the program never has to guard against any of
// it.
func normalize(st *State) {
	// A session this program started itself used to be recorded as having
	// no app of its own. It lives in the background, and that is where it
	// is watched. Such a session has no other app to fall back from, so a
	// watch saved as fallback for it is Watching in the background again.
	for i := range st.Watches {
		w := &st.Watches[i]
		if w.OriginHost == claude.HostNone {
			w.OriginHost = claude.HostBackground
		}
		if w.OriginHost == claude.HostBackground && w.PromiseState == "fallback" {
			w.PromiseState = "inplace"
		}
	}
	defaults := DefaultSettings()
	// Whatever a saved file says about keep-awake, the rule is the rule.
	st.Settings.KeepAwakeMode = KeepAwakeWhileBabysitting
	// Follow system became Auto when a light theme of its own was added.
	if st.Settings.Theme == "system" {
		st.Settings.Theme = ThemeAuto
	}
	if !ValidTheme(st.Settings.Theme) {
		st.Settings.Theme = defaults.Theme
	}
}

// Peek reads state.json without side effects: it never renames anything,
// and returns false when the file is missing, unreadable, or written by a
// version of this program whose state this one cannot read. That last case
// is the same judgement LoadReport makes, so a caller that only wants to
// look never reports watches out of a file the program itself would set
// aside unread.
func (s *Store) Peek() (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path())
	if err != nil {
		return State{}, false
	}
	st := beforeDecode()
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, false
	}
	if _, incompatible := incompatibleVersion(st.Version); incompatible {
		return State{}, false
	}
	return st, true
}

// Save writes state.json atomically (temp file, then rename) and stamps
// the version of the binary doing the writing. The temp file is flushed to
// the disk before the rename: without that, a machine that loses power
// moments after a save can be left with a state.json the rename made
// visible but whose contents were never written, which is exactly the file
// Load would then have to throw away.
func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := EnsureDir(s.Dir); err != nil {
		return err
	}
	st.Version = buildinfo.Version
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path() + ".tmp"
	if err := writeSynced(tmp, data); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// writeSynced writes data to path and does not return until the operating
// system says the bytes have reached the disk.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// EnsureDir creates this program's own folder if it is not there, and
// tightens a folder that already exists to the same permissions, so a
// folder created by an earlier build with laxer ones does not stay that
// way. Windows has no such permissions and reports errors for the attempt,
// which is why the tightening is allowed to fail.
//
// Only our own folder is made private. Any parent that has to be created
// on the way to it gets the ordinary permissions a folder would normally
// be created with: on a fresh account the path runs through folders such
// as the user's own data directory, which other programs will keep their
// own files in, and locking those down would be this program deciding
// something that is none of its business.
func EnsureDir(dir string) error {
	if parent := filepath.Dir(dir); parent != dir {
		if err := os.MkdirAll(parent, parentDirMode); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, dirMode); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		// Something is already there. Only a folder will do: carrying on
		// past a file of that name would turn into a failure to write
		// state.json later, which says far less about what is wrong.
		info, statErr := os.Stat(dir)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a folder", dir)
		}
	}
	_ = os.Chmod(dir, dirMode)
	return nil
}
