---
name: babysit
description: Keep a Claude Code session alive and reachable with CC Babysitter, and answer questions about CC Babysitter. Use when the person asks, in any language, to babysit this or another session, keep it alive, awake or reachable, stop babysitting, stop the background copy of a session that CC Babysitter brought back, check whether a session is babysat, open the CC Babysitter page, panel or dashboard, or asks how CC Babysitter works or what it did, such as why a session was restarted.
argument-hint: "[session: a name, short id, or nothing for this session]"
allowed-tools: Bash(ccbabysitter version) Bash(ccbabysitter version *) Bash(ccbabysitter help) Bash(ccbabysitter help *) Bash(ccbabysitter list *) Bash(ccbabysitter show *) Bash(ccbabysitter activity) Bash(ccbabysitter activity *) Bash(ccbabysitter babysit *) Bash(ccbabysitter unbabysit *) Bash(ccbabysitter retry *) Bash(ccbabysitter open) Bash(ccbabysitter open *) Bash(~/.local/bin/ccbabysitter version) Bash(~/.local/bin/ccbabysitter version *) Bash(~/.local/bin/ccbabysitter help) Bash(~/.local/bin/ccbabysitter help *) Bash(~/.local/bin/ccbabysitter list *) Bash(~/.local/bin/ccbabysitter show *) Bash(~/.local/bin/ccbabysitter activity) Bash(~/.local/bin/ccbabysitter activity *) Bash(~/.local/bin/ccbabysitter babysit *) Bash(~/.local/bin/ccbabysitter unbabysit *) Bash(~/.local/bin/ccbabysitter retry *) Bash(~/.local/bin/ccbabysitter open) Bash(~/.local/bin/ccbabysitter open *) Bash(pmset -g batt) PowerShell(ccbabysitter version) PowerShell(ccbabysitter version *) PowerShell(ccbabysitter help) PowerShell(ccbabysitter help *) PowerShell(ccbabysitter list *) PowerShell(ccbabysitter show *) PowerShell(ccbabysitter activity) PowerShell(ccbabysitter activity *) PowerShell(ccbabysitter babysit *) PowerShell(ccbabysitter unbabysit *) PowerShell(ccbabysitter retry *) PowerShell(ccbabysitter open) PowerShell(ccbabysitter open *)
---

# Babysit with CC Babysitter

CC Babysitter is a separate program on this computer. For a session it babysits, it keeps the computer awake and, if the session's app closes, starts the same session again in the background with Remote Control, so it stays reachable from a phone. This skill drives its command line. The request, if one came with the command, is: $ARGUMENTS

## Rules

- Never run `ccbabysitter` with no command, `ccbabysitter status`, `ccbabysitter --foreground`, `ccbabysitter --no-open` or `ccbabysitter --demo`. Their output carries the page's key, which must never enter this chat. To show the page, use `ccbabysitter open`. When CC Babysitter's own answer tells the person to run `ccbabysitter status`, pass that on; do not run it.
- Run each CC Babysitter command on its own and exactly as written here, with nothing added: no `;`, `&&`, `|`, `2>&1` or `echo`. The exit code shows in the result, and anything added makes the command ask for permission.
- Ask the person with AskUserQuestion cards, never by asking them to type yes or no. Only where that tool is not available, ask the same question in plain words.
- Ask on a card every time before `ccbabysitter stop`, `ccbabysitter settings NAME VALUE`, `ccbabysitter install`, `ccbabysitter uninstall` and `ccbabysitter quit`. Never run `ccbabysitter reset`: explain what it deletes and give the command for them to run.
- Never install or update anything yourself.
- Answer in the person's language. Say what CC Babysitter answered in plain words, but keep its commands exactly as written.
- In a place with no shell (claude.ai chat or Cowork), say that this works only in Claude Code, on the computer CC Babysitter runs on, and stop.

## Where answers come from

Two references beside this file hold what CC Babysitter's own documentation says. Read the one that fits the question:

| Question | Read |
|---|---|
| A command, flag, JSON field or exit code | references/commands.md (the CLI's help pages), or `ccbabysitter help <command>` when it is installed, which matches the installed version |
| How it behaves: what babysitting does, restarts and start at login, the hard rules, uninstalling, requirements | references/how-it-works.md (from the README) |

Do not guess beyond them. When neither answers the question, say so, and point to https://github.com/pejmanebrahimi/ccbabysitter.

## 1. Find CC Babysitter

Run `ccbabysitter version --json`. It answers whether or not CC Babysitter is running; its `headless` is true on a machine with no display. If the command is not found, try the default install folder: `~/.local/bin/ccbabysitter version --json` on macOS and Linux, or on Windows `"$LOCALAPPDATA/Programs/CCBabysitter/ccbabysitter.exe" version --json` from Git Bash or `& "$env:LOCALAPPDATA\Programs\CCBabysitter\ccbabysitter.exe" version --json` from PowerShell (right after an install, Claude Code still has the old PATH); if that works, use that path in place of the plain command name from here on. If neither works, it is not installed: for a question about CC Babysitter go to section 6; for anything else, section 2.

Whether it is running shows in the next command: exit code 3 means not running (section 3).

## 2. Not installed

Say: CC Babysitter isn't installed on this computer. Run this in your terminal app; it installs CC Babysitter and starts it, and its page opens in your browser (when `version --json` said `"headless":true`, say instead that it prints how to connect from another computer). Then give the one command for this system:

- macOS and Linux: `curl -fsSL https://ccbabysitter.dev/install.sh | sh && ~/.local/bin/ccbabysitter`
- Windows (PowerShell): `irm https://ccbabysitter.dev/install.ps1 | iex; ccbabysitter`

Say "your terminal app" and mean it: run with `!` here, its output, page key included, would land in this chat. Then a card: [Done, try again] [Cancel]. On Done, start again at section 1 and carry on with the original request. Remember that CC Babysitter was just started in this conversation.

## 3. Not running (exit code 3)

Say: CC Babysitter is installed but not running. Start it by running `ccbabysitter` in your terminal app; it keeps running in the background from then on, and its page opens in your browser (or, on a machine with no display, it prints how to connect from another computer). Then the same card, and on Done carry on. Remember that it was just started.

## 4. Babysit

The session is the request's target, or this session when there is none: `ccbabysitter babysit self --json`, or `ccbabysitter babysit <name or short id> --json`. On a refusal the JSON's `code` says why:

- `ambiguous`: the word fits more than one session; the message lists their ids. Run `ccbabysitter list --json`, offer only those sessions on a card by name and folder, then babysit the chosen one by its id.
- `not-found`: no session goes by that word; say so, and offer the running sessions from `ccbabysitter list --json` on a card.
- `not-in-session`: this command does not run inside a Claude Code session, so ask on a card which session is meant, from `ccbabysitter list --json`.
- Any other refusal (exit code 1): say CC Babysitter's reason in plain words, for example that a scheduled task run is never babysat.

On success, say what it answered, including any warning, such as "Won't come back if its app closes" with its command, which you give exactly. On macOS (the platform Claude Code reports for this session), run `pmset -g batt`: if it lists an InternalBattery, this is a MacBook, so add that keeping it awake needs the lid open, or an external display.

## 5. The page

The first time this skill runs in a conversation, whatever the request, end with one card: "Open the CC Babysitter page?" [Open] [Not now]. Do not offer it again in the same conversation, and do not offer it at all when CC Babysitter was just started in this conversation (its page just opened), when `version --json` says `"headless":true`, or when this session itself runs in the background (`ccbabysitter show self --json` says `"app":"background"`): a browser would open on a computer nobody may be at. In those cases do not mention the page at all. On Open, run `ccbabysitter open` and say what it answered.

When the person asks for the page, panel or dashboard, run `ccbabysitter open` straight away, with no card, and say what it answered. On a machine with no display it says how to connect from another computer instead.

## 6. Other requests and questions

- Is it babysat? `ccbabysitter show <session> --json`: `babysat`, and `state` while it is.
- Stop babysitting: `ccbabysitter unbabysit <session>`. Try a stuck one again: `ccbabysitter retry <session>`.
- Stop a background copy: a card first, then `ccbabysitter stop <session> --yes`.
- Settings: `ccbabysitter settings` shows them; a card first, then `ccbabysitter settings NAME VALUE`.
- Quit, install or uninstall: a card first, then the command.
- Reset: explain that it deletes CC Babysitter's state folder, then give `ccbabysitter reset` for them to run.
- What a command does: `ccbabysitter help <command>`, and answer from what it says; it works even when CC Babysitter is not running and matches the installed version.
- How it behaves (what happens on a restart, what babysitting does, what it never does): references/how-it-works.md.
- The page's address: tell the person to run `ccbabysitter status` in their terminal app, since the address carries the page's key; do not run it. To just see the page, use `ccbabysitter open`.
- What it did, or why: `ccbabysitter activity` (or `ccbabysitter activity <session>`) and `ccbabysitter show <session> --json`.
- Not installed: answer from the two references, and point to https://github.com/pejmanebrahimi/ccbabysitter for the rest.

Exit codes: 0 done, 1 refused, 2 wrong usage, 3 not running, 4 no such session, more than one, or no session to call self. Every command, flag and JSON field is in references/commands.md.
