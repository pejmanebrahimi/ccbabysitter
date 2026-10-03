package main

import (
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
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("mode %v, want 0644", fi.Mode().Perm())
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

func TestWriteDurableLeavesTheOldFileWhenItCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-folder", "unit")
	if err := writeDurable(path, []byte("x"), 0o644); err == nil {
		t.Fatal("writing into a folder that does not exist succeeded")
	}
}
