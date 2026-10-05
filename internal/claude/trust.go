package claude

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// maxConfigBytes is the largest settings file TrustFile will read. The file
// is the CLI's own and normally far smaller; past this the answer is simply
// unknown.
const maxConfigBytes = 64 << 20

// trustRecheck is how often TrustFile looks at the file again. The page is
// rebuilt many times a minute and the answer changes only when somebody
// runs claude in a new folder.
const trustRecheck = 5 * time.Second

// Trust is one reading of the Claude Code CLI's trust flags. It is a value:
// answering from it never touches the file, and it is never changed once
// made, so the goroutine that read it can hand it to any other. The zero
// Trust knows nothing.
type Trust struct {
	trusted map[string]bool
	ok      bool
}

// Trusted reports whether the Claude Code CLI trusts dir: dir or a folder
// above it is trusted, but inside a git repository only up to the
// repository's own folder. The CLI does not take trust from a folder above
// a repository, though the desktop app does, so a session the desktop app
// runs happily can still be refused a background copy. known is false
// when the settings file could not be read, and then nothing is claimed
// either way. Finding the repository looks for a .git file or folder; the
// settings file is never touched.
func (t Trust) Trusted(dir string) (trusted, known bool) {
	if !t.ok {
		return false, false
	}
	p := filepath.Clean(dir)
	repo := repositoryOf(p)
	for {
		if t.trusted[filepath.ToSlash(p)] {
			return true, true
		}
		parent := filepath.Dir(p)
		if parent == p || p == repo {
			return false, true
		}
		p = parent
	}
}

// repositoryOf is the folder of the git repository dir is in, the nearest
// folder at or above it holding .git, a folder in a repository and a file
// in a worktree, or "" outside any.
func repositoryOf(dir string) string {
	for p := dir; ; {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}

// TrustFile answers whether the Claude Code CLI trusts a folder, which a
// background session needs before it will start there. The answer comes
// from the hasTrustDialogAccepted flags in the CLI's settings file. The
// file is only ever read, and read again only once it has changed.
//
// A TrustFile is not safe for concurrent use: one goroutine owns it, and
// hands what it read to others as a Trust.
type TrustFile struct {
	Path string

	// limit is the largest file read, maxConfigBytes when zero.
	limit int64

	checked time.Time
	modTime time.Time
	size    int64
	trusted map[string]bool
	ok      bool
}

// Trusted reports whether dir, or a folder above it, is trusted, looking at
// the file again first when it is time to.
func (t *TrustFile) Trusted(dir string, now time.Time) (trusted, known bool) {
	return t.Read(now).Trusted(dir)
}

// Read looks at the file again when it is time to and returns what it says
// now. The map inside is replaced, never changed, when the file is read
// again, so a Trust handed out earlier stays as it was.
func (t *TrustFile) Read(now time.Time) Trust {
	t.refresh(now)
	return Trust{trusted: t.trusted, ok: t.ok}
}

// refresh reads the file again when it has changed since the last read,
// looking no more often than trustRecheck.
func (t *TrustFile) refresh(now time.Time) {
	if !t.checked.IsZero() && now.Sub(t.checked) < trustRecheck {
		return
	}
	t.checked = now
	limit := t.limit
	if limit <= 0 {
		limit = maxConfigBytes
	}
	info, err := os.Stat(t.Path)
	if err != nil || info.IsDir() || info.Size() > limit {
		t.ok, t.trusted = false, nil
		return
	}
	if t.ok && info.ModTime().Equal(t.modTime) && info.Size() == t.size {
		return
	}
	data, err := readAtMost(t.Path, limit)
	if err != nil {
		t.ok, t.trusted = false, nil
		return
	}
	var f struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if json.Unmarshal(data, &f) != nil {
		t.ok, t.trusted = false, nil
		return
	}
	trusted := make(map[string]bool, len(f.Projects))
	for dir, p := range f.Projects {
		if p.HasTrustDialogAccepted {
			trusted[filepath.ToSlash(filepath.Clean(dir))] = true
		}
	}
	t.trusted, t.modTime, t.size, t.ok = trusted, info.ModTime(), info.Size(), true
}

// errTooLarge is readAtMost's answer for a file that grew past the limit
// after it was measured.
var errTooLarge = errors.New("settings file is larger than the limit")

// readAtMost reads the whole file, refusing it when it holds more than
// limit bytes, so a file that grows between being measured and being read
// is never read past the limit.
func readAtMost(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}
