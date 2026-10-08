package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

// The script that removes the program on Windows waits for this process
// to exit, since Windows cannot delete a running program, then removes the
// folder whole or only the files, each path quoted for PowerShell.
func TestWindowsRemoveScript(t *testing.T) {
	whole := windowsRemoveScript(4242, removalPlan{folder: `C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter`, whole: true,
		files: []string{`C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter\ccbabysitter.exe`}})
	for _, want := range []string{
		"Wait-Process -Id 4242 -ErrorAction SilentlyContinue",
		`Remove-Item -LiteralPath 'C:\Users\Jo O''Neil\AppData\Local\Programs\CCBabysitter' -Recurse -Force`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("script has no %q:\n%s", want, whole)
		}
	}
	files := windowsRemoveScript(7, removalPlan{folder: `C:\tools`, files: []string{`C:\tools\ccbabysitter.exe`, `C:\tools\ccbabysitter-background.exe`}})
	if !strings.Contains(files, `Remove-Item -LiteralPath 'C:\tools\ccbabysitter.exe','C:\tools\ccbabysitter-background.exe' -Force`) || strings.Contains(files, "-Recurse") {
		t.Errorf("script:\n%s", files)
	}
}

// The script that takes the folder out of the user Path reads and writes
// the Path as stored, as the install script does, compares each entry
// expanded and without a trailing backslash, and says when it removed one.
func TestWindowsPathScript(t *testing.T) {
	s := windowsPathScript(`C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter`)
	for _, want := range []string{
		"[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames",
		"[Environment]::ExpandEnvironmentVariables($_).TrimEnd('\\') -ne 'C:\\Users\\Jo O''Neil\\AppData\\Local\\Programs\\CCBabysitter'",
		"[Microsoft.Win32.RegistryValueKind]::ExpandString",
		"'removed'",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script has no %q:\n%s", want, s)
		}
	}
}

// A script goes to PowerShell base64 encoded in UTF-16LE, so no quoting
// of the command line can change it.
func TestEncodePowerShell(t *testing.T) {
	enc := encodePowerShell("Write-Output '\u00e9'")
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(raw)%2 != 0 {
		t.Fatalf("%q: %v", enc, err)
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
	}
	if got := string(utf16.Decode(units)); got != "Write-Output '\u00e9'" {
		t.Fatalf("decoded %q", got)
	}
}
