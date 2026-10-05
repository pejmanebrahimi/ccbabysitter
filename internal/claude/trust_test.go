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

// Inside a git repository, one whose .git is a folder, the Claude Code CLI
// counts trust only from the folder up to the repository's own folder,
// never from a folder above it, though the desktop app does. A worktree,
// whose .git is a file, takes trust from any folder above it, wherever the
// worktree lives: the nearest .git decides. Outside any repository every folder above counts. The
// two real cases this follows: a repository in a trusted folder that the
// CLI refused to start a session in, and a worktree in a trusted folder
// where the CLI asked nothing. A worktree also takes its main repository's
// trust: that only ever removes a warning, never adds one.
func TestTrustStopsAtTheRepository(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	repo := filepath.Join(src, "repo")
	inner := filepath.Join(repo, ".claude", "worktrees", "wt")
	outside := filepath.Join(src, "worktrees", "ui-ticket")
	plain := filepath.Join(src, "plain", "sub")
	for _, d := range []string{filepath.Join(repo, ".git"), inner, outside, filepath.Join(repo, "pkg", "deep"), plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig(t, filepath.Join(inner, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n")
	writeConfig(t, filepath.Join(outside, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "ui-ticket")+"\n")
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
		{"a worktree outside the repository, in a trusted folder", config(src), outside, true},
		{"a worktree inside a trusted repository", config(repo), inner, true},
		{"a worktree inside a repository, in a trusted folder", config(src), inner, true},
		{"a trusted worktree", config(inner), inner, true},
		{"a worktree nothing above trusts", config(plain), outside, false},
		{"a worktree outside its trusted repository", config(repo), outside, true},
		{"no repository, a folder above", config(src), plain, true},
	}
	for _, c := range cases {
		if trusted, known := c.trust.Trusted(c.dir); !known || trusted != c.trusted {
			t.Errorf("%s: trusted=%v known=%v, want %v", c.name, trusted, known, c.trusted)
		}
	}
}

// Only a worktree's gitdir line names a main repository; anything else in a
// .git file names none, and nothing is trusted on its account.
func TestMainRepository(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repos", "ui")
	cases := map[string]string{
		"gitdir: " + filepath.ToSlash(filepath.Join(repo, ".git", "worktrees", "x")) + "\n": repo,
		"gitdir: " + filepath.Join(repo, ".git", "modules", "sub") + "\n":                   "",
		"not a gitdir line\n": "",
		"":                    "",
	}
	for body, want := range cases {
		f := filepath.Join(t.TempDir(), ".git")
		writeConfig(t, f, body)
		if got := mainRepository(f); got != want {
			t.Errorf("%q: got %q, want %q", body, got, want)
		}
	}
	if got := mainRepository(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("a missing file names %q", got)
	}
}
