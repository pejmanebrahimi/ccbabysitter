package main

import (
	"os"
	"path/filepath"
	"runtime"
)

// writeDurable replaces the file at path with data so that a crash or a
// power cut at any moment leaves either the old file or the new one, never
// an empty or half-written one. It writes a temporary file next to it,
// flushes that to disk, renames it into place and, where the system allows,
// flushes the folder too. The start-at-login files need this: a systemd
// unit that comes back empty after a power cut counts as masked and never
// starts again, so the service would not survive the very reboot it is for.
func writeDurable(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	// The rename itself lives in the folder, so the folder is flushed too.
	// Windows cannot open a folder for this; its rename is already
	// written through by the file system.
	if runtime.GOOS != "windows" {
		if d, err := os.Open(filepath.Dir(path)); err == nil {
			_ = d.Sync()
			d.Close()
		}
	}
	return nil
}
