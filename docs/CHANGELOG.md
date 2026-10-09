# Changelog

Versions follow MAJOR.MINOR.PATCH, with a pre-release such as 0.5.0-rc.1 published as MAJOR.MINOR.PATCH-<pre> under a heading of the same name. Patch for fixes with no visible change, minor for new or visibly changed behaviour, major when the state file or a flow changes so that old state is no longer valid. Bump when a build is handed out or put into use, not on every edit. Changes merged since the last release are listed under Unreleased until the next one.

## Unreleased

- New: on a machine with no display, New session on the page and `ccbabysitter start PATH` start a babysat background session with Remote Control in a project folder. When Claude Code does not trust the folder yet, the page asks first and `start` wants `--trust`. Then CC Babysitter answers Claude Code's own question whether to trust the folder, and Claude Code records the trust itself (#87).
- Fixed: a session's model no longer shows as `<synthetic>` after it is resumed. Claude Code marks messages it writes itself that way, and the model shown is now the last one a model wrote (#98).
- Fixed: the page's note that Claude Code is not logged in now shows when it is not. The CLI exits with an error when nobody is logged in, which was read as no answer (#103).
- Changed: New session and `ccbabysitter start` start nothing while Claude Code is not logged in, and say so: such a session could reach neither the model nor Remote Control (#103).

## 0.8.0 (2026-10-09)

- Changed: on the In background card of a session that came from a terminal, or from no app it can go back to, the main button attaches to the background copy, which keeps running with Remote Control on: Open in Terminal where CC Babysitter can open a terminal, and Copy attach command where it cannot, such as on a server. End background copy, which ends the copy and gives the command to resume the session in any terminal, moves to the card's menu (#79).
- Fixed: the In background card's main button no longer waits for the browser to stretch its column to fill it, which could leave it the width of its label for a moment when its menu opened (#78).

## 0.7.0 (2026-10-08)

- Changed: Activity says why a babysat session went down, when CC Babysitter can tell, instead of "host process exited": Claude Desktop closed, closed and updated, or restarted for an update, with both versions; the session ended while Claude Desktop kept running; the computer restarted; VS Code, the session's terminal or its SSH connection closed; or the background copy ended. The Claude Desktop reasons need macOS or Windows. When several sessions went down together, one line says so as the first is brought back. Where nothing tells, the reason is "the process running it exited". The In background card gives the same reason, for example "At 22:11, Claude Desktop restarted for an update." (#68).
- Changed: the In background card's way back is one split button, such as Back to Desktop, with a menu for Open on claude.ai, Open in Terminal and Copy attach command that works with the keyboard and screen readers. The card and the Back to Desktop dialog say that Claude Desktop shows "Claude Code crashed" for the session while its background copy runs, and that Try again in Desktop picks it up after Back to Desktop (#50).
- Changed: `ccbabysitter uninstall` removes CC Babysitter completely, after saying what goes and asking: start at login, a copy still running, the state folder, the program, and on Windows its folder in the user Path. Babysat sessions keep running. `--yes` asks nothing, and `--keep-files` only removes start at login, as uninstall did before (#59).
- Changed: keep-awake stops idle sleep only, and a laptop set to sleep when its lid closes still sleeps then. Where closing the lid would put the computer to sleep, as macOS, Windows' power plan or Linux's logind says, the Babysit dialog, `ccbabysitter status` and the page's keep-awake pill now say it is kept awake while its lid is open, and the README explains it. When the computer slept while sessions were babysat, Activity says so once it is awake, with the time they could not be reached, for example "The computer slept because its lid was closed. Babysat sessions could not be reached from 08:52 to 09:04." The cause comes from macOS's power log; elsewhere the line names the sleep alone (#38).
- New: an About panel on the page, beside Settings. It has the version details to copy into a bug report, the update command for the system, and the ways to report a problem: by email to hello@ccbabysitter.dev, as a GitHub issue, or privately for a security problem. `ccbabysitter help version` and the README say the same (#54).

## 0.6.1 (2026-10-08)

- Changed: when Claude Code's daemon stops a background session that sat idle and CC Babysitter brings it back, Activity now gives the reason as "Claude Code stopped it after 8 hours idle" instead of "host process exited", which read like a crash (#49).
- Fixed: the Claude Code plugin could ask whether you had installed CC Babysitter without ever showing the install command, and after quitting CC Babysitter it offered a settings change that cannot work once it has quit. It now shows every command in the reply before asking, and offers `ccbabysitter uninstall` to stop CC Babysitter for good (#58).
- Fixed: after Back to Desktop, the next rescue of that session first passed `--remote-control`, which made the CLI fork a copy; CC Babysitter stopped and removed the copy and retried without flags, but Activity showed "resume forked a copy", and a copy that lived long enough left an empty Remote Control entry in Claude Desktop under Other. A session the CLI keeps a background job record for is now resumed without flags from the first try (#55).
- Fixed: the Back to Desktop dialog said Remote Control stays off until you switch it on in Desktop, but Desktop turns it back on by itself when it was on for that session before. The dialog now says so (#52).
- Fixed: on a machine without the `claude` CLI, Activity got "agents --json did not answer:" every minute with nothing after the colon. It now says once that the Claude Code CLI is not installed, and says it again only after the CLI has worked in between; when the CLI answers with nothing, the line gives its error (#40).

## 0.6.0 (2026-10-07)

- New: `ccbabysitter open` opens the page in your browser and prints no key, so an AI agent can run it. On a machine with no display it says how to connect instead.
- New: `ccbabysitter version --json` also says `headless`, true on a machine with no display.
- New: a Claude Code plugin. Install it with `/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter` and say "babysit this session"; it also opens the page and answers questions about CC Babysitter.

## 0.5.2 (2026-10-06)

- Fixed: runs of a Claude Desktop scheduled task were listed as ordinary sessions, with a Babysit button, and could be babysat, though Desktop starts the task again on its schedule and a copy brought back could repeat its work. A run is now never babysat: the page lists runs apart, in a folded Scheduled task runs section with no Babysit button, `ccbabysitter list` prints them last under their own heading, `list --json` and `show --json` carry `scheduledTask`, and `ccbabysitter babysit` refuses one with the reason.

## 0.5.1 (2026-10-05)

- Fixed: the warning that a session would not come back showed for a session in a git worktree whose folder the claude CLI trusts through a folder above it, and could not be cleared. A worktree takes its trust from any folder above it; only a repository with a .git folder stops at its own folder.
- Fixed: on a Mac, the install script started CC Babysitter again after you had quit it, without opening the page. It now restarts it only when it runs in the background, as on Linux and Windows, and otherwise says how to start it.
- Fixed: after replacing an earlier version's login item, such as the Startup folder script on Windows, Activity said "Start at login was turned on outside CC Babysitter." It now says "Start at login kept on: CC Babysitter rewrote its login entry."

## 0.5.0 (2026-10-05)

CC Babysitter now runs in the background on Linux, macOS and Windows and starts again when you log in, so you can close the terminal you started it from.

- New: `ccbabysitter quit` and a Quit button in the page's settings stop CC Babysitter. Babysat sessions keep running, but nothing brings them back until you run `ccbabysitter` again. Activity says "asked to quit", followed by ", from the command line" when the command asked.
- Changed: on Linux, desktop and server alike, `ccbabysitter` starts CC Babysitter in the background through the systemd user manager and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading, and stays off once you turn it off. Run `ccbabysitter --foreground` to keep it in the terminal instead. `--port` now only applies with `--foreground`.
- Changed: running `ccbabysitter` while it already runs in the background prints the start information again and opens the page.
- Changed: on a Linux desktop, CC Babysitter starts with the graphical session, so it sees the desktop. Turning start at login off keeps the systemd unit, so `ccbabysitter` can still start it; `ccbabysitter uninstall` removes it.
- Changed: the install script restarts a background copy that is already running on a Linux desktop, so it runs the new version.
- Changed: on macOS, `ccbabysitter` starts CC Babysitter in the background as a user LaunchAgent and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading. Logging in no longer opens the page, and CC Babysitter starts again if it crashes. `ccbabysitter uninstall` now works on macOS: it stops the LaunchAgent and removes its plist. The install script restarts a LaunchAgent that is already running, so it runs the new version, and a login started by an earlier version's LaunchAgent runs the new binary as the background copy.
- Changed: on Windows, `ccbabysitter` starts `ccbabysitter-background.exe`, a copy of the program built to show no console window, which runs in the background, and gives the terminal back. Start at login is turned on the first time, also for anyone upgrading, as a per-user Run value that starts it with no window; it replaces the Startup folder script earlier versions wrote. The install script installs both programs, asks a running copy to quit before replacing them, and starts it again afterwards. `ccbabysitter uninstall` now works on Windows. Releases carry `ccbabysitter-background-windows-<arch>.exe` beside each Windows program.
- Changed: `ccbabysitter uninstall` forgets that start at login was turned on, so a later `ccbabysitter` turns it on again, as on a machine that never had CC Babysitter.
- Changed: when launchd or systemd starts CC Babysitter again after it stopped unexpectedly, Activity says "Started again by the system after CC Babysitter stopped unexpectedly." instead of "Started at login."
- Changed: on Windows, when the app CC Babysitter is started from keeps its programs in a job that forbids leaving it, the background copy is started through Explorer, so closing that app no longer ends it.
- Changed: a page that lost its key, such as the one `ccbabysitter` opened before CC Babysitter restarted, says to run `ccbabysitter` to open it again. When CC Babysitter is not running, every command says "Start it with: ccbabysitter", on a server too, where it named a systemctl command that fails after `ccbabysitter uninstall`.
- Fixed: the warning that a session could not be brought back missed folders inside a git repository whose trust came from a folder above it. The desktop app accepts that, but the Claude Code CLI, which brings sessions back, does not. The warning now follows the CLI and says what is at stake and the exact command to run: "Won't come back if its app closes. Claude Code needs a one-time OK to run on its own in this folder. In a terminal, run `cd <folder> && claude`, answer Yes, then exit."

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
