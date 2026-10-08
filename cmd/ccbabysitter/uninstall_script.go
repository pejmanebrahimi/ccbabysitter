package main

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf16"

	"ccbabysitter.dev/ccbabysitter/internal/hosts"
)

// windowsRemoveScript is the PowerShell script that removes the program
// on Windows, since Windows cannot delete a program while it runs: the
// folder whole, or only the files. It first waits for this process to
// exit, known by its id, pid, and its start time, startMs in milliseconds
// since the Unix epoch, never by the id alone; with no start time it does
// not wait. It tries again for twenty seconds, for files the background
// copy holds while it is still on its way out.
func windowsRemoveScript(pid int, startMs int64, plan removalPlan) string {
	var b strings.Builder
	if startMs > 0 {
		fmt.Fprintf(&b, "$p = Get-Process -Id %d -ErrorAction SilentlyContinue\n", pid)
		b.WriteString("if ($p) {\n")
		b.WriteString("  $started = [long]($p.StartTime.ToUniversalTime() - [DateTime]::new(1970, 1, 1, 0, 0, 0, [DateTimeKind]::Utc)).TotalMilliseconds\n")
		fmt.Fprintf(&b, "  if ([Math]::Abs($started - %d) -lt 2000) { $p.WaitForExit() }\n", startMs)
		b.WriteString("}\n")
	}
	var remove, gone string
	if plan.whole {
		folder := hosts.PowerShellQuote(plan.folder)
		remove = "Remove-Item -LiteralPath " + folder + " -Recurse -Force -ErrorAction SilentlyContinue"
		gone = "-not (Test-Path -LiteralPath " + folder + ")"
	} else {
		quoted := make([]string, len(plan.files))
		for i, f := range plan.files {
			quoted[i] = hosts.PowerShellQuote(f)
		}
		list := strings.Join(quoted, ",")
		remove = "Remove-Item -LiteralPath " + list + " -Force -ErrorAction SilentlyContinue"
		gone = "-not (@(" + list + ") | Where-Object { Test-Path -LiteralPath $_ })"
	}
	b.WriteString("foreach ($try in 1..40) {\n")
	b.WriteString("  " + remove + "\n")
	b.WriteString("  if (" + gone + ") { break }\n")
	b.WriteString("  Start-Sleep -Milliseconds 500\n")
	b.WriteString("}\n")
	return b.String()
}

// windowsPathScript is the PowerShell script that takes the program's
// folder, written in each of the ways in folders, out of the user Path.
// Like the install script, it reads and writes the Path as stored, so other
// entries keep their variables, and compares each entry expanded and
// without a trailing backslash. Every other entry stays as it was, empty
// ones too, and so does the value's kind. It prints removed when it took an
// entry out, and tells Windows the environment changed, so windows opened
// from then on see the new Path.
func windowsPathScript(folders ...string) string {
	quoted := make([]string, len(folders))
	for i, f := range folders {
		quoted[i] = hosts.PowerShellQuote(f)
	}
	return `$key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
if ($key) {
  try {
    $entries = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames) -split ';'
    $kept = @($entries | Where-Object { $_ -eq '' -or -not (@(` + strings.Join(quoted, ",") + `) -contains [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\')) })
    if ($kept.Count -lt @($entries).Count) {
      $key.SetValue('Path', ($kept -join ';'), $key.GetValueKind('Path'))
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

// windowsStartOutsideJobScript is the PowerShell script that starts
// commandLine through Windows' own process service, in the folder dir and
// with no window. What it starts is not in the job of the terminal or ssh
// session the script runs in, so it lives on when that ends.
func windowsStartOutsideJobScript(commandLine, dir string) string {
	return `$startup = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
  CommandLine = ` + hosts.PowerShellQuote(commandLine) + `
  CurrentDirectory = ` + hosts.PowerShellQuote(dir) + `
  ProcessStartupInformation = $startup
}
if ($r.ReturnValue -ne 0) { exit 1 }
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
