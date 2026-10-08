package site

import (
	"os/exec"
	"testing"
)

// The README and the website pass Vale's error-level rules, with the styles
// in .vale and the settings in .vale.ini, when Vale is installed. CI's test
// job has no Vale, so this skips there; CI's docs job runs Vale itself.
func TestTheWritingPassesVale(t *testing.T) {
	vale, err := exec.LookPath("vale")
	if err != nil {
		t.Skip("vale is not installed")
	}
	cmd := exec.Command(vale, "--no-global", "--minAlertLevel", "error", "README.md", "site")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vale README.md site: %v\n%s", err, out)
	}
}
