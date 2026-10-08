package main

import (
	"encoding/base64"
	"strings"
	"testing"
	"unicode/utf16"
)

// The script that removes the program on Windows waits for this process
// to exit, since Windows cannot delete a running program, and knows it by
// its id and its start time, never by the id alone. Then it removes the
// folder whole or only the files, each path quoted for PowerShell, trying
// again for a while, since the background copy may still be on its way
// out.
func TestWindowsRemoveScript(t *testing.T) {
	whole := windowsRemoveScript(4242, 1790000000123, removalPlan{folder: `C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter`, whole: true,
		files: []string{`C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter\ccbabysitter.exe`}})
	for _, want := range []string{
		"$p = Get-Process -Id 4242 -ErrorAction SilentlyContinue",
		"[Math]::Abs($started - 1790000000123) -lt 2000",
		"$p.WaitForExit()",
		"foreach ($try in 1..40)",
		`Remove-Item -LiteralPath 'C:\Users\Jo O''Neil\AppData\Local\Programs\CCBabysitter' -Recurse -Force`,
		`if (-not (Test-Path -LiteralPath 'C:\Users\Jo O''Neil\AppData\Local\Programs\CCBabysitter')) { break }`,
	} {
		if !strings.Contains(whole, want) {
			t.Errorf("script has no %q:\n%s", want, whole)
		}
	}
	files := windowsRemoveScript(7, 0, removalPlan{folder: `C:\tools`, files: []string{`C:\tools\ccbabysitter.exe`, `C:\tools\ccbabysitter-background.exe`}})
	if !strings.Contains(files, `Remove-Item -LiteralPath 'C:\tools\ccbabysitter.exe','C:\tools\ccbabysitter-background.exe' -Force`) || strings.Contains(files, "-Recurse") {
		t.Errorf("script:\n%s", files)
	}
	if strings.Contains(files, "Get-Process") {
		t.Errorf("a process whose start time is not known is not waited on by its id alone:\n%s", files)
	}
}

// The script that takes the folder out of the user Path reads and writes
// the Path as stored, as the install script does, compares each entry
// expanded and without a trailing backslash with each way of writing the
// folder, keeps every other entry, empty ones too, and the value's kind,
// and says when it removed one.
func TestWindowsPathScript(t *testing.T) {
	s := windowsPathScript(`C:\Users\Jo O'Neil\AppData\Local\Programs\CCBabysitter`, `D:\Profiles\Jo\AppData\Local\Programs\CCBabysitter`)
	for _, want := range []string{
		"[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames",
		"-not (@('C:\\Users\\Jo O''Neil\\AppData\\Local\\Programs\\CCBabysitter','D:\\Profiles\\Jo\\AppData\\Local\\Programs\\CCBabysitter') -contains [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\\'))",
		"$entries = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -split ';'",
		"$_ -eq '' -or -not (",
		"$key.SetValue('Path', ($kept -join ';'), $key.GetValueKind('Path'))",
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

// Where the terminal's job will not let a program out, the helper is
// started by Windows' own process service, outside the job and with no
// window, with the command line quoted for PowerShell.
func TestWindowsStartOutsideJobScript(t *testing.T) {
	s := windowsStartOutsideJobScript(`"C:\Windows\powershell.exe" -EncodedCommand QQB`, `C:\Windows`)
	for _, want := range []string{
		"New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }",
		"Invoke-CimMethod -ClassName Win32_Process -MethodName Create",
		`CommandLine = '"C:\Windows\powershell.exe" -EncodedCommand QQB'`,
		`CurrentDirectory = 'C:\Windows'`,
		"if ($r.ReturnValue -ne 0) { exit 1 }",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script has no %q:\n%s", want, s)
		}
	}
}
