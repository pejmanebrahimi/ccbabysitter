# CC Babysitter for Claude Code

Say "babysit this session" in Claude Code, and [CC Babysitter](https://ccbabysitter.dev) keeps it alive: the computer stays awake, and if the session's app closes, the same session comes back in the background with Remote Control on, so you can still reach it from your phone. You can also ask whether a session is babysat, open CC Babysitter's page, or ask how it works and what it did.

This plugin works in Claude Code only, on the computer CC Babysitter runs on. On claude.ai and in Cowork it can only say so.

CC Babysitter is an independent open-source project. It is not made by, affiliated with or endorsed by Anthropic. Claude and Claude Code are trademarks of Anthropic.

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

On macOS it also runs `pmset -g batt` without asking, to see whether the computer is a laptop, so it can mention that closing the lid makes the computer sleep. Its output has no serial number.

When CC Babysitter is not installed or not running, the plugin shows you the command that installs or starts it, for you to run in your terminal app. It never runs that command itself.

It never runs a command whose output carries the page's key, so the key never enters your chat.

## What it reads

The plugin reads only what CC Babysitter's commands answer, and its two reference files beside the skill.

CC Babysitter reads Claude Code's own local files and never writes to them:

- the session files in `~/.claude/sessions`: each session's id, folder, name and process;
- the transcripts in `~/.claude/projects`: token and turn counts, the model and the session's title. It reads no message content, except the first characters of a session's first message, to tell a run of a Claude Desktop scheduled task from an ordinary session, and it keeps nothing of it;
- Claude Code's settings in `~/.claude.json`, to see whether a folder is trusted.

## Privacy

Nothing is collected or sent anywhere: no telemetry, no account. CC Babysitter's page is served on `127.0.0.1` only. The only network use is downloading a release from GitHub when you install or update CC Babysitter.

## Tests

The `evals` folder holds the plugin's test cases and a stand-in `ccbabysitter` script that only those tests use (`sh evals/run.sh`). Claude Code never loads them.
