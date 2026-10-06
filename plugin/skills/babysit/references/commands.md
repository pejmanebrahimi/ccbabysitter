# CC Babysitter command reference

Generated from `ccbabysitter help`; do not edit by hand.

```
CC Babysitter keeps Claude Code sessions alive and reachable.

Run it:
  ccbabysitter                 start CC Babysitter. It runs in the background and starts at login
  ccbabysitter --foreground    run in this terminal until Ctrl+C
  ccbabysitter --no-open       start it without opening the page
  ccbabysitter --demo          scripted sessions, touches nothing real
  ccbabysitter --port N        with --foreground, prefer this port for the page, 47391 by default, else a random free port
  ccbabysitter install         on Linux, set it up to start at boot and keep running after logout
  ccbabysitter uninstall       stop the background copy and remove its service or login entry
  ccbabysitter reset           delete the state folder after confirmation
  ccbabysitter version         print the name and version

Control the running copy, for people and AI agents alike:
  ccbabysitter status          is it running, where, and what it is doing
  ccbabysitter list            every Claude Code session on this machine
  ccbabysitter show S          one session in full
  ccbabysitter babysit S       keep S alive: if its app dies, it comes back in the background
  ccbabysitter unbabysit S     stop babysitting S, which keeps running where it is
  ccbabysitter retry S         try again on a babysat session that is stuck
  ccbabysitter stop S --yes    stop the background copy of S and keep the conversation
  ccbabysitter activity [S]    what CC Babysitter did and why, newest first
  ccbabysitter settings        show the settings, or change one with: settings NAME VALUE
  ccbabysitter open            open the page in your browser, printing no key
  ccbabysitter quit            quit CC Babysitter. Babysat sessions keep running where they are
  ccbabysitter help COMMAND    everything about one command

S is a session id, a short id, a name, or self.
A full id always wins. A word that fits two sessions, as a short id or a name, is refused.
self is the session this command runs in.
Add --json for one JSON document on stdout. Exit codes: 0 done, 1 refused,
2 wrong usage, 3 CC Babysitter is not running, 4 no such session or more than one.

Examples:
  ccbabysitter babysit self
  ccbabysitter list --json
  ccbabysitter stop 3f2a9c1e --yes
```

## status

```
ccbabysitter status

Tells whether CC Babysitter is running, at what address, how many sessions it
sees, how many are babysat, and whether it keeps the computer awake. The address
includes the page's key, so it opens the page in a browser as it is.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text, two lines:
  CC Babysitter 0.4.0 is running at http://127.0.0.1:47391/?token=KEY
  Sessions: 5, babysat: 2. Keeping the computer awake: yes
Keeping the computer awake says yes, no, or not supported on this system.

With --json: {"schema":1,"running":true,"version":"...","url":"...","headless":false,
"sessions":5,"babysat":2,"keepAwake":{"held":true,"supported":true}}.
When it is not running, the sentence says so on stderr, the exit code is 3 and
stdout is {"schema":1,"running":false} with --json. That is the answer, not an error.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter status
  ccbabysitter status --json
```

## list

```
ccbabysitter list

Lists every Claude Code session on this machine, babysat or not, running or not.
Sessions that are not babysat come first, then the babysat ones. Runs of a Claude
Desktop scheduled task come last, under their own heading, since they are never
babysat: Desktop starts the task again on its schedule.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: a header, then one line per session, columns separated by two spaces:
  ID       short id, 8 characters
  NAME     the session's name, or - when it has none
  APP      where it runs: terminal, background, desktop, vscode or other, - when nowhere
  RC       Remote Control, on or off
  BABYSAT  no, or watching, in background, starting or stuck
  TOKENS   everything used so far, as 12k or 4.5M
  UPTIME   as 45s, 12m, 3h 5m or 2d 4h, - when it is not running
  FOLDER   the folder it works in
Scheduled task runs follow, after an empty line when other sessions come first,
under the line "Scheduled task runs, never babysat:", with the same columns.
With no sessions it says: No Claude Code sessions are running.

With --json: {"schema":1,"sessions":[...]}, an empty array when there are none.
A scheduled task's run is in the same array, with scheduledTask true.
Each session has the fields shown by ccbabysitter help show, and the same names.
In JSON, state is watching, background, starting or stuck, and is left out when the
session is not babysat. The text says in background for background.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter list
  ccbabysitter list --json
```

## show

```
ccbabysitter show S

Shows one session in full, one Label: value line each. Lines for things that
are not set are left out, except App, Running, Remote Control, Babysat and Tokens.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text lines, in this order: Id, Short id, Name, Also called, Folder, App, Also running
in, Running, Scheduled task, Remote Control, Status, Babysat, State, Tokens, Model,
Last activity, Uptime, Open with Remote Control, Attach, Attach over ssh, Resume, To
switch Remote Control on, Warning. Tokens is like: in 1.2k, out 3.4k, cache 50k. App
is none when it runs nowhere. State is watching, in background, starting or stuck.
Scheduled task is yes for a run of a Claude Desktop scheduled task, and left out
otherwise.

With --json: {"schema":1,"session":{...}}. These fields are always there: id, shortId,
name, folder, app, apps, running, remoteControl, status, babysat, tokens {input,
output, cacheRead, cacheWrite}, uptimeSeconds, canStop, canUnbabysit, scheduledTask.
Empty ones are "" or [], and app is "" when it runs nowhere. These are left out when
not set: alsoCalled, pid, state, model, lastActivity as RFC 3339 in UTC, remoteUrl,
attachCmd, sshAttachCmd, resumeCmd, rcHint, warning. New fields may be added.
In JSON, state is watching, background, starting or stuck. The text says in
background for background, so filter on background when using --json.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter show self
  ccbabysitter show api --json
```

## babysit

```
ccbabysitter babysit S [--start-at-login]

Keeps S alive. If the app it runs in dies, it comes back in the background and
stays reachable. This is the page's Babysit button. A run of a Claude Desktop
scheduled task is refused: Desktop starts the task again on its schedule.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  --start-at-login   also start CC Babysitter when you log in, as the page's box does
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"...","session":"SHORT ID"}.
ok is false when it was refused, with exit code 1 and the reason in message.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter babysit self
  ccbabysitter babysit api --start-at-login
```

## unbabysit

```
ccbabysitter unbabysit S

Stops babysitting S. The session keeps running where it is. This is the page's
Unbabysit button.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"...","session":"SHORT ID"}.
ok is false when it was refused, with exit code 1 and the reason in message.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter unbabysit api
```

## retry

```
ccbabysitter retry S

Tries again on a babysat session that is stuck, as the page's Try again button
does. Use it when list or show says its state is stuck.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"...","session":"SHORT ID"}.
ok is false when it was refused, with exit code 1 and the reason in message.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter retry nightly
```

## stop

```
ccbabysitter stop S --yes

Stops the background copy of S. The conversation is kept, and can be resumed.
Without --yes it changes nothing: it says what it would stop and exits with 2.
Only a session with a background copy can be stopped. canStop in show --json says
whether S has one. Otherwise stop is refused, with exit code 1.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  --yes              confirm. Without it nothing is stopped
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"...","session":"SHORT ID"}.
ok is false when it was refused, with exit code 1 and the reason in message.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter stop 3f2a9c1e --yes
```

## activity

```
ccbabysitter activity [S] [-n N]

Shows what CC Babysitter did and why, newest first. With S, only the entries
about that session. It looks through the newest 500 entries, so an older entry
about S is not found.

S is a session id, a short id, a name, or self. A full id always wins. A word
that fits two sessions, as a short id or a name, is refused, with exit code 4. self is
always the session this command runs in. See ccbabysitter list.

Flags:
  -n N               show at most N entries, 20 by default, 500 at most
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text, one line each: 2026-10-02 14:05:09  SESSION  MESSAGE
The time is local, SESSION is - for entries about no session in particular, and
(automatic: REASON) follows the message when CC Babysitter acted by itself.

With --json: {"schema":1,"entries":[{"time":"RFC 3339","level":"...","session":"...",
"automatic":false,"reason":"...","message":"..."}]}, an empty array when there are none.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter activity
  ccbabysitter activity worker -n 5
```

## settings

```
ccbabysitter settings [NAME VALUE]

Shows the settings, or changes one. These are the page's settings.
  autostart      on or off     start CC Babysitter when you log in, or at boot on a server
  auto-babysit   on or off     babysit every new background session on a server
  open-browser   on or off     open the page when you run ccbabysitter
  theme          auto, dark or light

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text, one line each: autostart: on, auto-babysit: on, open-browser: on, theme: auto.
Changing one prints the answer, or Saved., and exits with 0, or with 1 when refused.

With --json: {"schema":1,"settings":{"autostart":false,"autoBabysit":true,
"openBrowser":true,"theme":"auto"}}. A change answers like the actions do:
{"schema":1,"ok":true,"message":"..."}.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter settings
  ccbabysitter settings theme dark
```

## open

```
ccbabysitter open

Opens the running copy's page in your default browser, with a one-time address,
as starting CC Babysitter does. Nothing it prints carries the page's key, so it is
safe to run from an AI agent. On a machine with no display it opens nothing and says
how to connect from another computer. It never starts CC Babysitter.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"..."}.
ok is false when the browser could not be opened, with exit code 1.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter open
```

## quit

```
ccbabysitter quit

Quits the running copy of CC Babysitter, as the page's Quit button does. Babysat
sessions keep running where they are, but nothing brings them back if their app
dies, and the computer may sleep, until you run ccbabysitter again.

Flags:
  --json             print one JSON document on stdout instead of text
  --url URL          talk to the copy at this page address instead of the saved one:
                     http://127.0.0.1:PORT/?token=KEY as CC Babysitter prints it, or
                     http://127.0.0.1:PORT without a key, which is only for your own
                     running copy: the key saved on this machine goes only to the
                     address that copy saved. To talk to another copy, such as a
                     demo, pass the full address it printed, with ?token=.
                     A key given here can be seen by other accounts while the
                     command runs, so leave --url out for your own running copy

Text: the answer on stdout and exit code 0, or the reason on stderr and exit code 1.

With --json: {"schema":1,"ok":true,"message":"..."}.
ok is false when it was refused, with exit code 1 and the reason in message.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 CC Babysitter is not running,
4 no such session or more than one.
On an error with --json, stdout is {"schema":1,"ok":false,"error":"...","code":"..."}
where code is not-running, usage, not-found, ambiguous, not-in-session, no-key,
no-answer or failed. no-key, with exit code 1, means CC Babysitter is running but the
page's key was missing or wrong. no-answer, with exit code 1, means CC Babysitter did
not answer in time, and the action may still finish.

A control command never starts CC Babysitter. Start it with ccbabysitter.

Example:
  ccbabysitter quit
```

## install

```
ccbabysitter install

On Linux, sets CC Babysitter up as a systemd user service that starts at boot and
keeps running after you log out, and starts it, whether or not the machine has a
display. Running ccbabysitter on a Linux server does the same. On macOS and
Windows it says that install is for Linux and exits with 2: there a plain
ccbabysitter starts CC Babysitter in the background.

Example:
  ccbabysitter install
```

## uninstall

```
ccbabysitter uninstall

On Linux, stops and disables the service, however it was set up, removes its
file, and turns lingering off when CC Babysitter turned it on. On macOS, stops
the LaunchAgent and removes its plist. On Windows, quits CC Babysitter, removes
its Run value and an earlier version's Startup folder script. The state folder
stays, but a later plain ccbabysitter turns start at login on again, as on a
machine that never had CC Babysitter.

Example:
  ccbabysitter uninstall
```

## reset

```
ccbabysitter reset

Deletes the state folder, which holds the settings, the babysat sessions and the
activity log. It names the folder and asks Continue? [y/N] first, and anything but y
changes nothing. It refuses, with exit code 1, while CC Babysitter is running.

Example:
  ccbabysitter reset
```

## version

```
ccbabysitter version [--json]

Prints the name and the version, and exits with 0. With --json, prints
{"schema":1,"name":"CC Babysitter","version":"...","headless":false} instead, where
headless is true on a machine with no display. It works whether or not CC Babysitter
is running. Any other word after version is a usage error, exit code 2.

Example:
  ccbabysitter version
  ccbabysitter version --json
```

## help

```
ccbabysitter help [COMMAND]

Without a command, prints the list of everything ccbabysitter does. With one, prints
that command's page: what it does, its flags, its output and its exit codes.
ccbabysitter --help and ccbabysitter -h are the same as help.

Exit codes: 0 done, 2 no such command.

Example:
  ccbabysitter help babysit
```
