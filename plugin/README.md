# CC Babysitter for Claude Code

Say "babysit this session" in Claude Code, and [CC Babysitter](https://ccbabysitter.dev) keeps it alive: the computer stays awake, and if the session's app closes, the same session comes back in the background with Remote Control on, so you can still reach it from your phone. You can also ask whether a session is babysat, open CC Babysitter's page, or ask how it works and what it did.

This plugin works in Claude Code only, on the computer CC Babysitter runs on. On claude.ai and in Cowork it can only say so.

## Install

In Claude Code: `/plugin install ccbabysitter --marketplace pejmanebrahimi/ccbabysitter`

If CC Babysitter itself is not installed yet, the plugin gives you the one command that installs and starts it.

## Use

- "babysit this session", or `/ccbabysitter:babysit`
- "babysit the api session", or `/ccbabysitter:babysit api`
- "is this babysat?", "stop babysitting it", "open the babysitter page"
- "what does unbabysit do?", "why was my session restarted?"

## What it runs

Without asking: `ccbabysitter version`, `ccbabysitter help`, `ccbabysitter list`, `ccbabysitter show`, `ccbabysitter activity`, `ccbabysitter babysit`, `ccbabysitter unbabysit`, `ccbabysitter retry` and `ccbabysitter open`.

After asking you on a card: `ccbabysitter stop`, `ccbabysitter settings` changes, `ccbabysitter install`, `ccbabysitter uninstall` and `ccbabysitter quit`. It never runs `ccbabysitter reset`, and never installs anything.

It never runs a command whose output carries the page's key, so the key never enters your chat.
