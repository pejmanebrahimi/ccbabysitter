package main

import (
	"os"
	"path/filepath"
)

// writeDurable replaces the file at path with data so that a crash or a
// power cut at any moment leaves either the old file or the new one, never
// an empty or half-written one. It writes a temporary file next to it,
// flushes that to disk, renames it into place and flushes the folder, so
// the new name is on disk too. The start-at-login files need this: a
// systemd unit that comes back empty after a power cut counts as masked and
// never starts again, so the service would not survive the very reboot it
// is for.
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
	return syncDir(filepath.Dir(path))
}

// removeDurable removes the file at path and any temporary file a crash
// during writeDurable left next to it. A file that is already gone is not
// an error.
func removeDurable(path string) error {
	_ = os.Remove(path + ".tmp")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
