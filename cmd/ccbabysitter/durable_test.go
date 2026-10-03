package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteDurableReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ccbabysitter.service")
	if err := writeDurable(path, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeDurable(path, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "second\n" {
		t.Fatalf("file holds %q, %v", got, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("the temporary file was left behind: %v", err)
	}
	if runtime.GOOS != "windows" {
		// The umask may take bits away from 0644 but never adds any, and
		// the owner can always read and write.
		fi, _ := os.Stat(path)
		if m := fi.Mode().Perm(); m&^0o644 != 0 || m&0o600 != 0o600 {
			t.Fatalf("mode %v, want 0644 less the umask", m)
		}
	}
}

func TestWriteDurableIgnoresALeftoverTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unit")
	if err := os.WriteFile(path+".tmp", []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeDurable(path, []byte("fresh"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "fresh" {
		t.Fatalf("file holds %q", got)
	}
}

// A write that fails must leave the file that was there exactly as it was,
// which a plain truncate-and-write would not.
func TestWriteDurableKeepsTheOldFileWhenTheWriteFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions do not stop file creation the same way on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write into a read-only folder")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "unit")
	if err := os.WriteFile(path, []byte("old unit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	if err := writeDurable(path, []byte("new unit\n"), 0o644); err == nil {
		t.Fatal("writing into a read-only folder succeeded")
	}
	if got, _ := os.ReadFile(path); string(got) != "old unit\n" {
		t.Fatalf("the old file was changed to %q", got)
	}
}

// A folder flush that fails is reported, so the caller does not claim the
// file is safely on disk.
func TestWriteDurableReportsAFailedFolderFlush(t *testing.T) {
	failed := errors.New("folder flush failed")
	saved := syncDir
	syncDir = func(string) error { return failed }
	t.Cleanup(func() { syncDir = saved })
	path := filepath.Join(t.TempDir(), "unit")
	if err := writeDurable(path, []byte("x"), 0o644); !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the folder flush error", err)
	}
}

func TestRemoveDurableRemovesALeftoverTemporaryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unit")
	for _, p := range []string{path, path + ".tmp"} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeDurable(path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".tmp"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s is still there: %v", p, err)
		}
	}
	if err := removeDurable(path); err != nil {
		t.Fatalf("removing a file that is already gone: %v", err)
	}
}
