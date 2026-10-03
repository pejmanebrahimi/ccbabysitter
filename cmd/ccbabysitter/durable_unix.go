//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// syncDir flushes a folder, so a file just renamed into it keeps its new
// name through a power cut. A file system that cannot flush a folder at
// all says so with EINVAL or ENOTSUP; that is not an error here, since
// there is nothing more this program could do. Any other failure is.
var syncDir = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}
