package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustFileReadsTheFlagForAFolderAndItsChildren(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeConfig(t, path, `{"numStartups":3,"projects":{`+
		`"/home/dev/ws":{"hasTrustDialogAccepted":true,"allowedTools":[]},`+
		`"/home/dev/other":{"hasTrustDialogAccepted":false}}}`)
	tf := &TrustFile{Path: path}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		dir     string
		trusted bool
	}{
		{"/home/dev/ws", true},
		{"/home/dev/ws/sub/deeper", true},
		{"/home/dev/ws/", true},
		{"/home/dev/other", false},
		{"/home/dev/new", false},
	}
	for _, c := range cases {
		trusted, known := tf.Trusted(c.dir, now)
		if !known || trusted != c.trusted {
			t.Errorf("%s: trusted=%v known=%v, want trusted=%v", c.dir, trusted, known, c.trusted)
		}
	}
}

// Without a readable file nothing is claimed, so no warning is ever shown
// on a guess.
func TestTrustFileClaimsNothingWithoutAFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, known := (&TrustFile{Path: filepath.Join(dir, "missing.json")}).Trusted("/home/dev/ws", now); known {
		t.Fatal("a missing file must leave the answer unknown")
	}
	bad := filepath.Join(dir, "bad.json")
	writeConfig(t, bad, "{ not json")
	if _, known := (&TrustFile{Path: bad}).Trusted("/home/dev/ws", now); known {
		t.Fatal("an unreadable file must leave the answer unknown")
	}
}

// The file is read again once it has changed, but not more often than the
// recheck interval, however often the question is asked.
func TestTrustFileNoticesAChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeConfig(t, path, `{"projects":{}}`)
	tf := &TrustFile{Path: path}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if trusted, _ := tf.Trusted("/home/dev/ws", now); trusted {
		t.Fatal("nothing is trusted yet")
	}
	writeConfig(t, path, `{"projects":{"/home/dev/ws":{"hasTrustDialogAccepted":true}}}`)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if trusted, _ := tf.Trusted("/home/dev/ws", now.Add(time.Second)); trusted {
		t.Fatal("the file is not looked at again inside the recheck interval")
	}
	if trusted, _ := tf.Trusted("/home/dev/ws", now.Add(trustRecheck)); !trusted {
		t.Fatal("a changed file must be read again")
	}
}

// A settings file past the size cap is never read: the answer is unknown,
// so no warning is claimed, however valid what is in it would be.
func TestTrustFileClaimsNothingPastTheSizeCap(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	path := filepath.Join(dir, ".claude.json")
	body := `{"projects":{"/home/dev/ws":{"hasTrustDialogAccepted":true}}}`
	writeConfig(t, path, body)
	if trusted, known := (&TrustFile{Path: path}).Trusted("/home/dev/ws", now); !known || !trusted {
		t.Fatalf("under the cap the file is read: trusted=%v known=%v", trusted, known)
	}
	if _, known := (&TrustFile{Path: path, limit: int64(len(body)) - 1}).Trusted("/home/dev/ws", now); known {
		t.Fatal("a file one byte past the cap must leave the answer unknown")
	}

	huge := filepath.Join(dir, "huge.json")
	writeConfig(t, huge, body)
	if err := os.Truncate(huge, maxConfigBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, known := (&TrustFile{Path: huge}).Trusted("/home/dev/ws", now); known {
		t.Fatal("a file past the real cap must leave the answer unknown")
	}
}

// What Read hands back is a value: answering from it never touches the
// file again, so it can be asked on any goroutine the reading one gave it to.
func TestTrustIsAValueThatNeedsNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	writeConfig(t, path, `{"projects":{"/home/dev/ws":{"hasTrustDialogAccepted":true}}}`)
	tr := (&TrustFile{Path: path}).Read(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if trusted, known := tr.Trusted("/home/dev/ws/sub"); !known || !trusted {
		t.Fatalf("trusted=%v known=%v", trusted, known)
	}
	if _, known := (Trust{}).Trusted("/home/dev/ws"); known {
		t.Fatal("the zero Trust knows nothing")
	}
}

// Inside a git repository the Claude Code CLI counts trust only from the
// folder up to the repository's own folder, never from a folder above it,
// though the desktop app does. A worktree, whose .git is a file, is a
// repository of its own. Outside any repository every folder above counts.
func TestTrustStopsAtTheRepository(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	repo := filepath.Join(src, "repo")
	worktree := filepath.Join(repo, ".claude", "worktrees", "wt")
	plain := filepath.Join(src, "plain", "sub")
	for _, d := range []string{filepath.Join(repo, ".git"), worktree, filepath.Join(repo, "pkg", "deep"), plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, filepath.Join(worktree, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n")
	config := func(trusted ...string) Trust {
		body := `{"projects":{`
		for i, d := range trusted {
			if i > 0 {
				body += ","
			}
			body += `"` + filepath.ToSlash(d) + `":{"hasTrustDialogAccepted":true}`
		}
		path := filepath.Join(t.TempDir(), ".claude.json")
		writeConfig(t, path, body+"}}")
		return (&TrustFile{Path: path}).Read(time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC))
	}
	cases := []struct {
		name    string
		trust   Trust
		dir     string
		trusted bool
	}{
		{"a folder above the repository", config(src), repo, false},
		{"a folder above, asked from inside", config(src), filepath.Join(repo, "pkg", "deep"), false},
		{"the repository itself", config(repo), repo, true},
		{"the repository, asked from inside", config(repo), filepath.Join(repo, "pkg", "deep"), true},
		{"a worktree under a trusted repository", config(repo), worktree, false},
		{"a trusted worktree", config(worktree), worktree, true},
		{"no repository, a folder above", config(src), plain, true},
	}
	for _, c := range cases {
		if trusted, known := c.trust.Trusted(c.dir); !known || trusted != c.trusted {
			t.Errorf("%s: trusted=%v known=%v, want %v", c.name, trusted, known, c.trusted)
		}
	}
}
