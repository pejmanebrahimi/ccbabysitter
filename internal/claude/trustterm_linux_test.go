package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTrustCLI writes a stand-in for the claude CLI: on its terminal it
// draws the trust question with No chosen, redraws it with Yes chosen
// after Down, writes "yes" to answered after Enter, and then waits, as the
// CLI does, until it is ended.
func fakeTrustCLI(t *testing.T, answered string) string {
	t.Helper()
	question := "Quick safety check: Is this a project you created or one you trust?\\r\\n"
	script := "#!/bin/sh\n" +
		"stty raw -echo\n" +
		"printf '" + question + "\\342\\235\\257 No, exit\\r\\n  Yes, I trust this folder\\r\\n'\n" +
		"dd bs=3 count=1 >/dev/null 2>&1\n" +
		"printf '  No, exit\\r\\n\\342\\235\\257 Yes, I trust this folder\\r\\n'\n" +
		"dd bs=1 count=1 >/dev/null 2>&1\n" +
		"echo yes > '" + answered + "'\n" +
		"sleep 60\n"
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// On a real pseudo-terminal, AcceptTrust chooses Yes, presses Enter and
// ends the CLI it started, which would otherwise keep running.
func TestAcceptTrustOnAPseudoTerminal(t *testing.T) {
	answered := filepath.Join(t.TempDir(), "answered")
	bin := fakeTrustCLI(t, answered)
	start := time.Now()
	if err := AcceptTrust(context.Background(), bin, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(answered); err != nil || strings.TrimSpace(string(b)) != "yes" {
		t.Fatalf("the question was not answered: %q %v", b, err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("took %v: the CLI was not ended", took)
	}
}
