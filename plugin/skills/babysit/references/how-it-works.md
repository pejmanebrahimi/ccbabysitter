# How CC Babysitter works

Generated from README.md at the top of the CC Babysitter repository; do not edit by hand.

## Why a babysitter?

No matter how hard you hope or try, your computer and the Claude Code app will find a creative way[^1] to kill the remote-controlled session you started before leaving the computer, hoping you could keep issuing destiny-defining orders to your coding agents while you're vacationing where the map gives up. Well, this has happened to me, and believe me, we are not alone[^2]. Of course, you can go ahead and write some watchdog scripts on a laptop, or tmux and whatnot on a server, but some of us prefer a babysitter.

If a babysat session's app dies, the session comes back in the background with Remote Control on, and the computer stays awake. On a server, babysat sessions survive SSH drops and reboots, no tmux needed. And one page shows every Claude Code session on the machine, in any app, with its tokens and uptime. Your agents can use it too: `ccbabysitter babysit self`.

[^1]: The ones I've run into: the Claude app updates itself and, for some reason, fails to start again; or it does come back after the update, but only some of the remote-controlled sessions are restored. Your computer or server can also act up and restart itself, for any reason, good or bad.

[^2]: Reports on Claude Code's issue tracker: [#95364](https://github.com/anthropics/claude-code/issues/95364), [#94049](https://github.com/anthropics/claude-code/issues/94049), [#99585](https://github.com/anthropics/claude-code/issues/99585), [#100114](https://github.com/anthropics/claude-code/issues/100114), [#89599](https://github.com/anthropics/claude-code/issues/89599).

## How it works

A babysat session stays in its own app, and CC Babysitter never moves a session from one app to another. Once a session is carried in the background you can keep using it through Remote Control, attach to it from a terminal with `claude attach`, as Open in Terminal on the page does, or stop the background copy and open the session again wherever you like. While the copy runs, Claude Desktop shows "Claude Code crashed" for a session that came from it, because only one copy of a session runs at a time. Back to Desktop on the page, or `ccbabysitter stop`, ends the copy, and Try again in Desktop then picks the session up where it left off.

On a server with no display the same page is the session manager. A plain `ccbabysitter` there sets it up as a service that survives a reboot, prints the command to connect from your laptop, and gives you the terminal back; `ccbabysitter install` does the same. Start a session with New session on the page, or `ccbabysitter start` and a project folder's path, or yourself with `claude --bg --remote-control` in that folder, and it is babysat as soon as it starts. A session you start yourself is babysat unless you switch that off in Settings; one `ccbabysitter start` starts always is. Attach to a babysat session from any SSH shell, unbabysit or stop it when you are done, and reach the page with the command CC Babysitter gave you.

## What babysitting does

- Keeps the computer awake while at least one babysat session is running or being started. Keep-awake stops idle sleep only: a laptop set to sleep when its lid closes still sleeps then. macOS has no setting to stop that, short of running with the lid closed on power with an external display, keyboard and mouse; Windows and Linux can be set to do nothing when the lid closes. Where closing the lid would put the computer to sleep, the Babysit dialog and `status` say it is kept awake while its lid is open. When the computer slept while sessions were babysat, Activity says so once it is awake, with the time they could not be reached, and on macOS whether the lid was closed.
- Tells you how to switch Remote Control on when it is off. Nothing outside a session can switch it on.
- If the session's app closes, starts the session again as a background session with Remote Control, under the same id, with `claude --bg --resume`. Activity and the In background card say why the session went down, when CC Babysitter can tell: for example, Claude Desktop restarted for an update, or the computer restarted.
- Starts a babysat background session again when it froze. A session counts as frozen when it has been busy for 20 minutes while its transcript, its status, its processes and its CPU time all stayed the same. That happens when its connection drops in the middle of a turn, and Remote Control goes offline with it. It stops the frozen copy with `claude stop`, which keeps the conversation, and starts the session again in the background, and Activity says why. The turn that froze is lost. After three freezes in two hours it stops trying, and the card shows the session as stuck. A frozen session in a terminal or an app is left as it is, since it belongs to that app; its card and `show` say it is not responding.
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
- **User mode only.** No admin rights, ever. CC Babysitter adds one thing to your system, its own start-at-login entry in your user account: a systemd user unit on Linux, a LaunchAgent on macOS, a Run value on Windows. It is on by default, and you can turn it off with `ccbabysitter settings autostart off`, or on a desktop in Settings.
- **One folder of state** (`%LOCALAPPDATA%\CCBabysitter` on Windows, the XDG data directory elsewhere: `~/.local/share/ccbabysitter`, or `$XDG_DATA_HOME/ccbabysitter` when that is set).

## Start at login

On Linux, `ccbabysitter` runs CC Babysitter in the background through the systemd user manager, writing the unit `~/.config/systemd/user/ccbabysitter.service` the first time, and turns start at login on that first time, also when you upgrade from an earlier version. On a desktop the unit starts with your graphical session, after it, so CC Babysitter sees the desktop. On a server it starts at boot and keeps running after you log out. Turn it off with `ccbabysitter settings autostart off`, or on a desktop in Settings: CC Babysitter keeps running until you quit it or restart, and a later `ccbabysitter` leaves it off. Every run, and `ccbabysitter install`, writes the unit again when it no longer matches this binary, such as after you move the binary, and restarts a running copy when its unit changed or it is another version. A copy already running with the right unit and version is left running. `ccbabysitter install` always turns start at boot on.

On macOS, `ccbabysitter` runs CC Babysitter in the background as a user LaunchAgent, `com.ccbabysitter`, loaded with `launchctl` the way `brew services start` does it, and turns start at login on that first time, also when you upgrade. The LaunchAgent runs the service, which never opens a browser, so logging in does not open the page; it starts CC Babysitter again if it crashes, but not after `ccbabysitter quit`. While start at login is on its plist is `~/Library/LaunchAgents/com.ccbabysitter.plist`. macOS may show a "Background Items Added" notice the first time, and lists CC Babysitter under System Settings, General, Login Items. Switching it off there under Allow in the Background stops macOS from starting it at all, and `ccbabysitter` then says so; switch it back on there, or run `ccbabysitter --foreground`. Turning start at login off moves the plist into the state folder: CC Babysitter keeps running until you quit it or log out, and a plain `ccbabysitter` still starts it. A Mac reached only over ssh, with nobody logged in at its screen, has no session to load the LaunchAgent into, so there `ccbabysitter` runs in the terminal.

On Windows, `ccbabysitter` starts `ccbabysitter-background.exe`, the windowless copy installed beside it, which runs in the background with no console window; closing the terminal or the app that ran `ccbabysitter` does not stop it. Start at login, turned on the first time, is the value `CCBabysitter` under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, which starts the windowless copy at login, again with no window. It replaces the Startup folder script `CCBabysitter.cmd` that earlier versions wrote, which `ccbabysitter` removes. A copy installed with `go install` has no windowless program, so there `ccbabysitter` runs in the terminal.

Every login entry points at the program where it is at that moment, so keep the program where the install script put it, or somewhere else it will stay, such as `~/bin`; a copy in a temporary folder is refused. `ccbabysitter` writes the unit, the LaunchAgent or the Run value again when the program has moved.

## Uninstall

Run `ccbabysitter uninstall`. It says what it removes and asks first, then removes everything CC Babysitter put on this computer:

- Start at login: the LaunchAgent on macOS, the systemd user service on Linux, and the Run value on Windows. On Linux it turns lingering off only when CC Babysitter turned it on, which it notes in the file `lingering-turned-on` in the state folder; lingering that was already on stays on, since other services of yours may need it.
- A copy still running, which it asks to quit.
- The state folder, with the settings, the babysat sessions and the activity log: `~/.local/share/ccbabysitter` (or `$XDG_DATA_HOME/ccbabysitter`) on macOS and Linux, `%LOCALAPPDATA%\CCBabysitter` on Windows.
- The program. On macOS and Linux that is the one file, in `~/.local/bin` or wherever it is, and the folder stays. On Windows it is the folder `%LOCALAPPDATA%\Programs\CCBabysitter`, which also comes out of your user Path; Windows cannot delete a running program, so the folder goes a moment after the command ends. A program anywhere else, such as a folder you chose with `CCBABYSITTER_INSTALL_DIR`, loses only CC Babysitter's own files, and the folder stays, also on your Path. A `ccbabysitter` that is a link loses the link, and what it points at stays.

Babysat sessions keep running where they are. `ccbabysitter uninstall --yes` asks nothing, for a script with no terminal to ask in. `ccbabysitter uninstall --keep-files` only stops the background copy and removes start at login, and a later plain `ccbabysitter` turns it on again, as on a machine that never had CC Babysitter. Another `ccbabysitter` still on your PATH, such as one `go install` put in Go's `bin` folder, is named at the end, for you to delete if you want. A copy run with `go run` has no program to delete. If you added the Claude Code plugin, remove it in Claude Code with `/plugin`.

## Requirements

The `claude` CLI at run time, on PATH or where its installer puts it (`~/.local/bin` or `~/.claude/local`): CC Babysitter reads Claude Code's own files and drives sessions through the CLI. It needs Claude Code 2.1.275 or later. With an older CLI it still runs, but prints a warning when it starts, since older versions may word their output differently and some things may not work as described. Building needs only Go 1.27 or later.
