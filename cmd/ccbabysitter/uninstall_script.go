package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"

	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// windowsRemoveScript is the PowerShell script that removes the program
// on Windows once this process, pid, has exited, since Windows cannot
// delete a program while it runs: the folder whole, or only the files.
func windowsRemoveScript(pid int, plan removalPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Wait-Process -Id %d -ErrorAction SilentlyContinue\n", pid)
	b.WriteString("Start-Sleep -Milliseconds 500\n")
	if plan.whole {
		b.WriteString("Remove-Item -LiteralPath " + hosts.PowerShellQuote(plan.folder) + " -Recurse -Force -ErrorAction SilentlyContinue\n")
		return b.String()
	}
	quoted := make([]string, len(plan.files))
	for i, f := range plan.files {
		quoted[i] = hosts.PowerShellQuote(f)
	}
	b.WriteString("Remove-Item -LiteralPath " + strings.Join(quoted, ",") + " -Force -ErrorAction SilentlyContinue\n")
	return b.String()
}

// windowsPathScript is the PowerShell script that takes folder out of the
// user Path. Like the install script, it reads and writes the Path as
// stored, so other entries keep their variables, and compares each entry
// expanded and without a trailing backslash. It prints removed when it
// took an entry out, and tells Windows the environment changed, so
// windows opened from then on see the new Path.
func windowsPathScript(folder string) string {
	return `$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
if ($key) {
  try {
    $old = @([string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -split ';' | Where-Object { $_ })
    $kept = @($old | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -ne ` + hosts.PowerShellQuote(folder) + ` })
    if ($kept.Count -lt $old.Count) {
      $key.SetValue('Path', ($kept -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
      [Environment]::SetEnvironmentVariable('CCBABYSITTER_INSTALL_REFRESH', '1', 'User')
      [Environment]::SetEnvironmentVariable('CCBABYSITTER_INSTALL_REFRESH', $null, 'User')
      'removed'
    }
  } finally {
    $key.Close()
  }
}
`
}

// encodePowerShell is script as PowerShell's -EncodedCommand takes it:
// base64 of UTF-16LE, which no quoting of the command line can change.
func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(units))
	for i, u := range units {
		b[2*i] = byte(u)
		b[2*i+1] = byte(u >> 8)
	}
	return base64.StdEncoding.EncodeToString(b)
}
