package hosts

import (
	"context"
	"os/exec"
	"time"
)

// lidSleeps asks ioreg whether closing this Mac's lid now puts it to sleep.
func lidSleeps() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-r", "-k", "AppleClamshellState", "-d", "1").Output()
	return err == nil && darwinLidSleeps(string(out))
}
