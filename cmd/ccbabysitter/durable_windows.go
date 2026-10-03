//go:build windows

package main

// syncDir does nothing on Windows. A folder cannot be opened and flushed
// there the way it can elsewhere, and Go's rename does not ask Windows to
// write the change through either, so the file's contents are on disk when
// it is renamed but the rename itself is left to the file system. Making
// it durable would take a direct Windows call (MoveFileEx with
// MOVEFILE_WRITE_THROUGH).
var syncDir = func(string) error { return nil }
