# Changelog

Versions follow MAJOR.MINOR.PATCH, with a pre-release such as 0.5.0-rc.1 published as MAJOR.MINOR.PATCH-<pre> under a heading of the same name. Patch for fixes with no visible change, minor for new or visibly changed behaviour, major when the state file or a flow changes so that old state is no longer valid. Bump when a build is handed out or put into use, not on every edit. Changes merged since the last release are listed under Unreleased until the next one.

## Unreleased

- Fixed: on a server, Activity said "Start at login was turned on outside CC Babysitter." right after installing, although CC Babysitter set up its own service. It now says "Set up as a service that starts at boot.", and a service turned off by hand is described as the service, not as start at login.

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
