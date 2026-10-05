package supervise

import (
	"strings"
	"testing"
)

// The warning says what is at stake, then the one command that fixes it,
// with the session's own folder in it.
func TestUntrustedWarningNamesTheFolder(t *testing.T) {
	w := UntrustedWarning("/home/dev/my ws")
	if !strings.HasPrefix(w, "Won't come back if its app closes.") || !strings.Contains(w, "'/home/dev/my ws'") || !strings.Contains(w, "answer Yes") {
		t.Fatalf("%q", w)
	}
}
