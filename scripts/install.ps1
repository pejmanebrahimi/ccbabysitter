# Installs CC Babysitter on Windows, in Windows PowerShell 5.1 or PowerShell 7:
#
#   irm https://ccbabysitter.dev/install.ps1 | iex
#
# It downloads the release programs for this machine, ccbabysitter.exe and
# the windowless ccbabysitter-background.exe that runs in the background,
# checks them against the release's checksums.txt, puts them in
# %LOCALAPPDATA%\Programs\CCBabysitter and adds that folder to your user
# Path. A CC Babysitter that is running is asked to quit first and started
# again in the background afterwards. It needs no admin rights.
#
# Settings, read from the environment:
#   CCBABYSITTER_VERSION       release tag to install, such as v0.4.0
#                              (default: the latest release)
#   CCBABYSITTER_INSTALL_DIR   folder to install into
#                              (default: %LOCALAPPDATA%\Programs\CCBabysitter)
#   CCBABYSITTER_DOWNLOAD_URL  base URL holding the release files
#                              (default: the GitHub release for the version)
#
# Everything is in one function called on the last line, so a download cut
# short runs nothing at all, and the settings it changes stay inside it
# rather than in the PowerShell window it runs in. A failure ends in a
# thrown error rather than exit, which would close that window, so a
# script or tool that ran this sees it failed.

function Install-CCBabysitter {
    $ErrorActionPreference = 'Stop'
    # The progress bar makes downloads many times slower in 5.1.
    $ProgressPreference = 'SilentlyContinue'

    $repoUrl = 'https://github.com/pejmanebrahimi/ccbabysitter'
    $runningMessage = 'CC Babysitter is running and could not be asked to quit. Quit it (Ctrl+C in its window, or ccbabysitter quit), then run this again.'

    # Test-FileLocked reports whether an exception, or one inside it, is a
    # sharing or lock violation: the file is open in another program.
    function Test-FileLocked($exception) {
        for ($e = $exception; $e; $e = $e.InnerException) {
            $code = $e.HResult -band 0xFFFF
            if ($code -eq 32 -or $code -eq 33) {
                return $true
            }
        }
        return $false
    }

    # Get-Reason is the innermost message of an exception, which is the
    # one that says what went wrong rather than which call failed.
    function Get-Reason($exception) {
        while ($exception.InnerException) {
            $exception = $exception.InnerException
        }
        return $exception.Message
    }

    # Windows PowerShell 5.1 may not offer TLS 1.2, which GitHub requires.
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    $machine = $env:PROCESSOR_ARCHITEW6432
    if (-not $machine) {
        $machine = $env:PROCESSOR_ARCHITECTURE
    }
    switch ($machine) {
        'AMD64' { $arch = 'amd64' }
        'ARM64' { $arch = 'arm64' }
        default {
            if (-not $machine) {
                $machine = 'unknown'
            }
            throw "There is no CC Babysitter build for the $machine processor. Builds exist for AMD64 and ARM64."
        }
    }
    $asset = "ccbabysitter-windows-$arch.exe"
    # The background copy is the same program built for Windows to show no
    # console window, installed beside ccbabysitter.exe.
    $bgAsset = "ccbabysitter-background-windows-$arch.exe"

    if ($env:CCBABYSITTER_DOWNLOAD_URL) {
        $base = $env:CCBABYSITTER_DOWNLOAD_URL.TrimEnd('/')
    } else {
        $version = $env:CCBABYSITTER_VERSION
        if (-not $version -or $version -eq 'latest') {
            $base = "$repoUrl/releases/latest/download"
        } else {
            # A tag is v0.4.0; 0.4.0 and V0.4.0 mean the same one.
            if ($version -match '^v') {
                $version = $version.Substring(1)
            }
            $base = "$repoUrl/releases/download/v$version"
        }
    }

    if ($env:CCBABYSITTER_INSTALL_DIR) {
        $dir = $env:CCBABYSITTER_INSTALL_DIR
    } else {
        $dir = Join-Path $env:LOCALAPPDATA 'Programs\CCBabysitter'
    }
    # A relative folder is taken from this window's current location.
    $dir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($dir)
    if ($dir.Length -gt 3) {
        $dir = $dir.TrimEnd('\')
    }
    $dest = Join-Path $dir 'ccbabysitter.exe'
    $bgDest = Join-Path $dir 'ccbabysitter-background.exe'

    # Replace-File puts the new copy at $src in place of $target: written
    # beside it under a temporary name, the old one moved aside to .bak,
    # the new one moved into place, and the old one moved back if that
    # fails. It returns whether there is a .bak to delete or put back.
    function Replace-File($src, $target) {
        $bak = "$target.bak"
        $part = Join-Path (Split-Path -Parent $target) ('.ccbabysitter-' + [Guid]::NewGuid().ToString('N') + '.part')
        $moved = $false
        try {
            [IO.File]::Copy($src, $part, $true)
            if (Test-Path -LiteralPath $target) {
                # Windows keeps a running program's file open, so it can
                # be neither written nor moved. Opening it for writing
                # finds that out while the old copy is still whole.
                [IO.File]::Open($target, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None).Close()
                if (Test-Path -LiteralPath $bak) {
                    [IO.File]::Delete($bak)
                }
                [IO.File]::Move($target, $bak)
                $moved = $true
            }
            try {
                [IO.File]::Move($part, $target)
            } catch {
                $failure = $_
                if ($moved) {
                    try {
                        [IO.File]::Move($bak, $target)
                        $moved = $false
                    } catch {
                        throw "$(Get-Reason $failure.Exception). The old copy could not be put back and is at $bak"
                    }
                }
                throw $failure
            }
        } catch {
            if (Test-FileLocked $_.Exception) {
                throw $runningMessage
            }
            throw "Could not install ${target}: $(Get-Reason $_.Exception)"
        } finally {
            if (Test-Path -LiteralPath $part) {
                Remove-Item -LiteralPath $part -Force -ErrorAction SilentlyContinue
            }
        }
        return $moved
    }

    # Restore-File puts the .bak of $target back, for an install that did
    # not work out.
    function Restore-File($target) {
        $bak = "$target.bak"
        if (Test-Path -LiteralPath $target) {
            [IO.File]::Delete($target)
        }
        [IO.File]::Move($bak, $target)
    }

    # Invoke-Quiet runs the installed program and returns its exit code,
    # with its output thrown away. Windows PowerShell 5.1 turns a line a
    # program writes to stderr into an error, which this script's Stop
    # preference would end on, so that preference is relaxed in here.
    function Invoke-Quiet($exe, [string[]]$arguments) {
        $ErrorActionPreference = 'Continue'
        & $exe @arguments *> $null
        return $LASTEXITCODE
    }

    # Test-Writable reports whether every file in $paths that exists can be
    # opened for writing, which a running program's file cannot.
    function Test-Writable([string[]]$paths) {
        foreach ($path in $paths) {
            if (-not (Test-Path -LiteralPath $path)) {
                continue
            }
            try {
                [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None).Close()
            } catch {
                return $false
            }
        }
        return $true
    }

    # Get-Sum is the checksum checksums.txt gives for $name, or nothing.
    function Get-Sum($sums, $name) {
        foreach ($line in Get-Content -LiteralPath $sums) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -ge 2 -and ($fields[1] -ceq $name -or $fields[1] -ceq "*$name")) {
                return $fields[0].ToLowerInvariant()
            }
        }
        return $null
    }

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('ccbabysitter-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp -Force | Out-Null
    $moved = $false
    $bgMoved = $false
    $wasRunning = $false
    try {
        $sums = Join-Path $tmp 'checksums.txt'
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile $sums
        } catch {
            throw "Could not download $base/checksums.txt: $(Get-Reason $_.Exception)"
        }
        # Releases before 0.5 have no windowless program: those install
        # ccbabysitter.exe alone, as they always did.
        $names = @($asset)
        if (Get-Sum $sums $bgAsset) {
            $names += $bgAsset
        }
        $files = @{}
        foreach ($name in $names) {
            $files[$name] = Join-Path $tmp $name
            Write-Host "Downloading $name from $base"
            try {
                Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile $files[$name]
            } catch {
                throw "Could not download $base/${name}: $(Get-Reason $_.Exception)"
            }
            $want = Get-Sum $sums $name
            if (-not $want) {
                throw "checksums.txt has no line for $name, so the download cannot be checked. Nothing was installed."
            }
            $got = (Get-FileHash -LiteralPath $files[$name] -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($got -ne $want) {
                throw "The download of $name does not match its checksum (expected $want, got $got). Nothing was installed."
            }
        }
        $withBackground = $files.ContainsKey($bgAsset)

        # A running CC Babysitter keeps its program files open, so it is
        # asked to quit first, and started again in the background once
        # the new version is in place. The wait is for the files to be
        # let go, not only for the page to stop answering. A copy from
        # before quit existed does not know the command and stays running,
        # and the replace below then says to quit it by hand.
        $quitAsked = $false
        if ($withBackground -and (Test-Path -LiteralPath $dest)) {
            if ((Invoke-Quiet $dest @('status')) -eq 0) {
                $wasRunning = $true
                Write-Host 'Asking CC Babysitter to quit, to replace it.'
                if ((Invoke-Quiet $dest @('quit')) -eq 0) {
                    $quitAsked = $true
                    $deadline = (Get-Date).AddSeconds(15)
                    while (-not (Test-Writable @($dest, $bgDest)) -and (Get-Date) -lt $deadline) {
                        Start-Sleep -Milliseconds 300
                    }
                }
            }
        }

        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        # A windowless program that is new here has no .bak; a rollback
        # removes it instead.
        $bgNew = $withBackground -and -not (Test-Path -LiteralPath $bgDest)
        try {
            if ($withBackground) {
                $bgMoved = Replace-File $files[$bgAsset] $bgDest
            }
            try {
                $moved = Replace-File $files[$asset] $dest
            } catch {
                if ($bgMoved) {
                    try { Restore-File $bgDest } catch { }
                } elseif ($bgNew) {
                    Remove-Item -LiteralPath $bgDest -Force -ErrorAction SilentlyContinue
                }
                throw
            }
        } catch {
            # Asked to quit, it answered, but has not let its files go yet.
            if ($quitAsked -and $_.Exception.Message -eq $runningMessage) {
                throw 'CC Babysitter was asked to quit and is still letting go of its files. Run this again in a moment.'
            }
            # It answers, but is a version from before quit existed, so
            # only Ctrl+C in its window stops it.
            if ($wasRunning -and -not $quitAsked -and $_.Exception.Message -eq $runningMessage) {
                throw 'CC Babysitter is running in a window, and this version of it cannot be asked to quit. Quit it with Ctrl+C in its window, then run this again.'
            }
            throw
        }
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # The old copies are deleted only now that the new program has run. If
    # it does not run, the old ones are put back as well as they can be.
    $installed = $null
    $runs = $false
    try {
        $installed = & $dest version
        $runs = ($LASTEXITCODE -eq 0)
    } catch {
        $runs = $false
    }
    if (-not $runs) {
        if ($moved -or $bgMoved) {
            try {
                if ($moved) { Restore-File $dest }
                if ($bgMoved) { Restore-File $bgDest }
                if ($bgNew) { Remove-Item -LiteralPath $bgDest -Force -ErrorAction SilentlyContinue }
            } catch {
                throw "$dest was installed but does not run on this machine, and the old copy could not be put back. It is at $dest.bak"
            }
            throw "$dest was installed but does not run on this machine, so the old copy was put back."
        }
        if ($bgNew) { Remove-Item -LiteralPath $bgDest -Force -ErrorAction SilentlyContinue }
        throw "$dest was installed but does not run on this machine."
    }
    foreach ($target in @($dest, $bgDest)) {
        Remove-Item -LiteralPath "$target.bak" -Force -ErrorAction SilentlyContinue
    }
    Write-Host "Installed $installed to $dir"

    # An earlier version started at login from a Startup folder script,
    # which opens a console window. With the windowless program installed,
    # the per-user Run value takes its place, also when CC Babysitter is not
    # running now.
    $legacy = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\Startup\CCBabysitter.cmd'
    if ($withBackground -and $env:APPDATA -and (Test-Path -LiteralPath $legacy)) {
        $run = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
        Set-ItemProperty -Path $run -Name 'CCBabysitter' -Value ('"' + $bgDest + '" --service')
        Remove-Item -LiteralPath $legacy -Force
        Write-Host 'Start at login now starts the windowless program, with no console window.'
    }

    # The user Path is read and written as stored, so entries such as
    # %USERPROFILE%\bin keep their variables rather than being expanded.
    $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment')
    try {
        $userPath = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $entries = @($userPath -split ';' | Where-Object { $_ } | ForEach-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') })
        if ($entries -notcontains $dir) {
            if ($userPath) {
                $newPath = $userPath.TrimEnd(';') + ';' + $dir
            } else {
                $newPath = $dir
            }
            $key.SetValue('Path', $newPath, [Microsoft.Win32.RegistryValueKind]::ExpandString)
            # Setting a user variable this way tells Windows the
            # environment changed, so windows opened from now on see the
            # new Path; this one is set and removed only for that.
            [Environment]::SetEnvironmentVariable('CCBABYSITTER_INSTALL_REFRESH', '1', 'User')
            [Environment]::SetEnvironmentVariable('CCBABYSITTER_INSTALL_REFRESH', $null, 'User')
            Write-Host "Added $dir to your user Path."
        }
    } finally {
        $key.Close()
    }
    $sessionEntries = @($env:Path -split ';' | Where-Object { $_ } | ForEach-Object { $_.TrimEnd('\') })
    if ($sessionEntries -notcontains $dir) {
        $env:Path = $env:Path.TrimEnd(';') + ';' + $dir
        Write-Host "Added $dir to the Path of this window."
    }

    Write-Host ''
    if ($wasRunning) {
        # The new version runs in the background again, and prints where
        # its page is, without opening it.
        $ErrorActionPreference = 'Continue'
        & $dest --no-open
        if ($LASTEXITCODE -ne 0) {
            throw 'CC Babysitter was updated but did not start in the background again. Start it with: ccbabysitter'
        }
    } else {
        Write-Host 'Start it with: ccbabysitter'
    }
}

Install-CCBabysitter
