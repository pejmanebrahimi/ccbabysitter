# How CC Babysitter works

Generated from README.md at the top of the CC Babysitter repository; do not edit by hand.

## Why a babysitter?

I left Claude Code working on my laptop overnight, with Remote Control on and "keep computer awake" ticked. In the morning, my session had stopped, and I could not reach it from my phone. Claude Desktop had updated itself in the night and restarted, but it reopened only the session on my screen. The others stayed stopped. I'm not the only one: [1](https://github.com/anthropics/claude-code/issues/92933), [2](https://github.com/anthropics/claude-code/issues/95364), [3](https://github.com/anthropics/claude-code/issues/95491).

On my server I wanted a few sessions running around the clock. That meant tmux, one window per session, and making sure it all came back after every reboot.

CC Babysitter does both. If a babysat session's app dies, the session comes back in the background with Remote Control on, and the computer stays awake. On a server, babysat sessions survive SSH drops and reboots, no tmux needed. And one page shows every Claude Code session on the machine, in any app, with its tokens and uptime. Your agents can use it too: `ccbabysitter babysit self`.

Sure, you could raise it yourself with a watchdog script, tmux and systemd. Some of us prefer a babysitter.

## How it works

A babysat session stays in its own app, and CC Babysitter never moves a session from one app to another. Once a session is carried in the background you have two choices: keep using it through Remote Control, or stop the background copy and open the session again wherever you like.

On a server with no display the same page is the session manager. A plain `ccbabysitter` there sets it up as a service that survives a reboot, prints the command to connect from your laptop, and gives you the terminal back; `ccbabysitter install` does the same. Start a session yourself with `claude --bg --remote-control` in a project folder and it is babysat as soon as it starts, unless you switch that off in Settings. Attach to a babysat session from any SSH shell, unbabysit or stop it when you are done, and reach the page with the command CC Babysitter gave you.

## What babysitting does

- Keeps the computer awake while at least one babysat session is running or being started.
- Tells you how to switch Remote Control on when it is off. Nothing outside a session can switch it on.
- If the session's app closes, starts the session again as a background session with Remote Control, under the same id, with `claude --bg --resume`.
- Lets you unbabysit while the session is still in its own app. Once the background copy carries it, you stop the copy instead with `claude stop`, which keeps the conversation.
- Says before you babysit when a background copy could not start, with the command that fixes it: the Claude Code CLI must trust the folder, and inside a git repository it takes that trust only from the repository's own folder, though the desktop app also accepts a trusted folder above it, while a worktree takes it from any folder above; it never starts in your home folder.
- Offers, in the same dialog, to start CC Babysitter at login so the promise survives a restart, when that is off. A plain `ccbabysitter` turns it on the first time it starts CC Babysitter in the background, also when you upgrade, as Start at login below says.
- Stops babysitting a session that ended before anything was said in it, and says so in Activity: Claude saves a conversation only after the first message, so there is nothing to bring back.
- Never babysits a run of a Claude Desktop scheduled task, since Desktop starts the task again on its schedule and a copy brought back could repeat its work. Runs are listed apart, in a folded Scheduled task runs section on the page and last in `ccbabysitter list`. A run stays a run even if you carry on chatting in it.
- Waits 90 seconds after it starts on a machine with a display before starting anything in the background, so apps that restore their own sessions at login, Claude Desktop in particular, go first.
- Gives every background session a Copy button for `claude attach <short>`, and on a server a second one for the same command run over `ssh` from another machine. A background copy stopped from the page keeps both on its Not running row, until CC Babysitter restarts.

## Hard rules

- **Opt-in only.** Nothing happens to a session that is not babysat. On a desktop, babysitting is always something you choose. On a server, a new background session is babysat as soon as it starts, unless "Babysit every new background session" is switched off in Settings.
- **Every automatic action is explained** in the Activity panel with its reason.
- **Stopping a background copy is confirmed first**, and nothing else the page does closes a session.
- **Claude's files are read-only.** Sessions are controlled only through the `claude` CLI.
- **Claude Desktop and VS Code are never modified or opened.**
- **Loopback only.** The page is served on `127.0.0.1`, requests that come from other web pages are refused, and every request must carry the page's key, which only your account can read.
- **User mode only.** No admin rights, ever. The only thing CC Babysitter adds to your system is its own start-at-login entry in your user account: a systemd user unit on Linux, a LaunchAgent on macOS, a Run value on Windows. It is on by default, and you can turn it off with `ccbabysitter settings autostart off`, or on a desktop in Settings.
- **One folder of state** (`%LOCALAPPDATA%\CCBabysitter` on Windows, the XDG data directory elsewhere: `~/.local/share/ccbabysitter`, or `$XDG_DATA_HOME/ccbabysitter` when that is set).

## Start at login

On Linux, `ccbabysitter` runs CC Babysitter in the background through the systemd user manager, writing the unit `~/.config/systemd/user/ccbabysitter.service` the first time, and turns start at login on that first time, also when you upgrade from an earlier version. On a desktop the unit starts with your graphical session, after it, so CC Babysitter sees the desktop. On a server it starts at boot and keeps running after you log out. Turn it off with `ccbabysitter settings autostart off`, or on a desktop in Settings: CC Babysitter keeps running until you quit it or restart, and a later `ccbabysitter` leaves it off. Every run, and `ccbabysitter install`, writes the unit again when it no longer matches this binary, such as after you move the binary, and restarts a running copy when its unit changed or it is another version. A copy already running with the right unit and version is left running. `ccbabysitter install` always turns start at boot on.

On macOS, `ccbabysitter` runs CC Babysitter in the background as a user LaunchAgent, `com.ccbabysitter`, loaded with `launchctl` the way `brew services start` does it, and turns start at login on that first time, also when you upgrade. The LaunchAgent runs the service, which never opens a browser, so logging in does not open the page; it starts CC Babysitter again if it crashes, but not after `ccbabysitter quit`. While start at login is on its plist is `~/Library/LaunchAgents/com.ccbabysitter.plist`. macOS may show a "Background Items Added" notice the first time, and lists CC Babysitter under System Settings, General, Login Items. Switching it off there under Allow in the Background stops macOS from starting it at all, and `ccbabysitter` then says so; switch it back on there, or run `ccbabysitter --foreground`. Turning start at login off moves the plist into the state folder: CC Babysitter keeps running until you quit it or log out, and a plain `ccbabysitter` still starts it. A Mac reached only over ssh, with nobody logged in at its screen, has no session to load the LaunchAgent into, so there `ccbabysitter` runs in the terminal.

On Windows, `ccbabysitter` starts `ccbabysitter-background.exe`, the windowless copy installed beside it, which runs in the background with no console window; closing the terminal or the app that ran `ccbabysitter` does not stop it. Start at login, turned on the first time, is the value `CCBabysitter` under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, which starts the windowless copy at login, again with no window. It replaces the Startup folder script `CCBabysitter.cmd` that earlier versions wrote, which `ccbabysitter` removes. A copy installed with `go install` has no windowless program, so there `ccbabysitter` runs in the terminal.

Every login entry points at the program where it is at that moment, so keep the program where the install script put it, or somewhere else it will stay, such as `~/bin`; a copy in a temporary folder is refused. `ccbabysitter` writes the unit, the LaunchAgent or the Run value again when the program has moved.

## Uninstall

`ccbabysitter uninstall` on its own leaves the state folder, but a later plain `ccbabysitter` turns start at login on again, as on a machine that never had CC Babysitter. `ccbabysitter reset` deletes the state folder after asking, and refuses while CC Babysitter is still running, so quit it first. Run the steps for your system in this order.

macOS:

1. Run `ccbabysitter uninstall`. It stops the LaunchAgent and removes its plist, `~/Library/LaunchAgents/com.ccbabysitter.plist` or the one in the state folder.
2. Quit any copy still running in a terminal with `ccbabysitter quit` or Ctrl+C.
3. Run `ccbabysitter reset` to delete the state folder, `~/.local/share/ccbabysitter` (or `$XDG_DATA_HOME/ccbabysitter`).
4. Delete the binary: `rm ~/.local/bin/ccbabysitter`, or wherever `command -v ccbabysitter` says it is.

Linux:

1. Run `ccbabysitter uninstall`. It stops and disables the systemd user service and removes `~/.config/systemd/user/ccbabysitter.service`, however the unit was written: by a plain `ccbabysitter`, by `ccbabysitter install`, or by "Start CC Babysitter when I log in". It turns lingering off for your user only when CC Babysitter turned it on, which it notes in the file `lingering-turned-on` in the state folder; lingering that was already on is left on, since other services of yours may need it. Skip this step if none of those happened.
2. Quit any copy still running in a terminal with `ccbabysitter quit` or Ctrl+C.
3. Run `ccbabysitter reset` to delete the state folder, `~/.local/share/ccbabysitter` (or `$XDG_DATA_HOME/ccbabysitter`).
4. Delete the binary: `rm ~/.local/bin/ccbabysitter`, or wherever `command -v ccbabysitter` says it is.

Windows:

1. Run `ccbabysitter uninstall`. It quits CC Babysitter and removes its Run value, and an earlier version's `CCBabysitter.cmd` from your Startup folder.
2. Quit any copy still running in a window with `ccbabysitter quit` or Ctrl+C.
3. Run `ccbabysitter reset` to delete the state folder, `%LOCALAPPDATA%\CCBabysitter`.
4. Delete the folder holding the binary, `%LOCALAPPDATA%\Programs\CCBabysitter`, or the folder you installed into.
5. Remove that folder from your user Path: open "Edit environment variables for your account" from the Start menu, select Path, choose Edit, and delete the entry.

A copy installed with `go install` is deleted from `$(go env GOPATH)/bin` instead.

## Requirements

The `claude` CLI at run time, on PATH or where its installer puts it (`~/.local/bin` or `~/.claude/local`): CC Babysitter reads Claude Code's own files and drives sessions through the CLI. It needs Claude Code 2.1.275 or later. With an older CLI it still runs, but prints a warning when it starts, since older versions may word their output differently and some things may not work as described. Building needs only Go 1.27 or later.
