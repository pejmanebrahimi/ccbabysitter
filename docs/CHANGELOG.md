# Changelog

Versions follow MAJOR.MINOR.PATCH, with a pre-release such as 0.5.0-rc.1 published as MAJOR.MINOR.PATCH-<pre> under a heading of the same name. Patch for fixes with no visible change, minor for new or visibly changed behaviour, major when the state file or a flow changes so that old state is no longer valid. Bump when a build is handed out or put into use, not on every edit. Changes merged since the last release are listed under Unreleased until the next one.

## Unreleased

- New: `ccbabysitter quit` and a Quit button in the page's settings stop CC Babysitter. Babysat sessions keep running, but nothing brings them back until you run `ccbabysitter` again. Activity says "asked to quit", followed by ", from the command line" when the command asked.
- Changed: on Linux, desktop and server alike, `ccbabysitter` starts CC Babysitter in the background through the systemd user manager and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading, and stays off once you turn it off. Run `ccbabysitter --foreground` to keep it in the terminal instead. `--port` now only applies with `--foreground`.
- Changed: running `ccbabysitter` while it already runs in the background prints the start information again and opens the page.
- Changed: on a Linux desktop, CC Babysitter starts with the graphical session, so it sees the desktop. Turning start at login off keeps the systemd unit, so `ccbabysitter` can still start it; `ccbabysitter uninstall` removes it.
- Changed: the install script restarts a background copy that is already running on a Linux desktop, so it runs the new version.
- Changed: on macOS, `ccbabysitter` starts CC Babysitter in the background as a user LaunchAgent and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading. Logging in no longer opens the page, and CC Babysitter starts again if it crashes. `ccbabysitter uninstall` now works on macOS: it stops the LaunchAgent and removes its plist. The install script restarts a LaunchAgent that is already running, so it runs the new version, and a login started by an earlier version's LaunchAgent runs the new binary as the background copy.
- Changed: on Windows, `ccbabysitter` starts `ccbabysitter-background.exe`, a copy of the program built to show no console window, which runs in the background, and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading, as a per-user Run value that starts it with no window; it replaces the Startup folder script earlier versions wrote. The install script installs both programs, asks a running copy to quit before replacing them, and starts it again afterwards. `ccbabysitter uninstall` now works on Windows. Releases carry `ccbabysitter-background-windows-<arch>.exe` beside each Windows program.

## 0.4.2 (2026-10-03)

- Fixed: on a server, Activity said "Start at login was turned on outside CC Babysitter." right after installing, although CC Babysitter set up its own service. It now says "Set up as a service that starts at boot.", and a service turned off by hand is described as the service, not as start at login.
- Fixed: when a stray copy of a session could not be cleaned up, the page and Activity gave `claude stop X && claude rm X`, which Windows PowerShell 5.1 cannot run. They now give the two commands one after the other.

## 0.4.1 (2026-10-03)

- Fixed: the start-at-login files (the systemd unit on Linux, the LaunchAgent on macOS, the Startup script on Windows) are written so that a power cut right after `ccbabysitter install` or after turning on start at login can no longer leave them empty. An empty systemd unit counts as masked, so the service did not come back after that reboot.

## 0.4.0 (2026-10-03)

First public release, as a preview.

- Babysit a Claude Code session in a terminal, Claude Desktop or VS Code: if its app closes, crashes or is killed, the same session comes back in the background with Remote Control on, and the computer stays awake.
- On a Linux server, CC Babysitter runs as a service, so babysat sessions survive SSH drops and reboots, and new background sessions are babysat as soon as they start.
- One local page shows every Claude Code session on the machine, in any app, with its tokens and uptime. It is served on `127.0.0.1` and needs a key, which the address CC Babysitter prints includes.
- A command line for people and AI agents: `status`, `list`, `show`, `babysit`, `unbabysit`, `retry`, `stop`, `activity` and `settings`, with `--json`, and `self` for the session a command runs in.
- Install with one command on macOS, Linux or Windows, or with `go install`. Ready-built binaries for macOS, Linux and Windows on amd64 and arm64, with checksums and build provenance attestations.
- Open source under the MIT license.
