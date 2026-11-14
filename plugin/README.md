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

The plugin pre-approves nothing, so Claude Code asks you before each command it runs, as it does for other commands. "Yes, and don't ask again" covers that command in the project you are in; Claude Code asks again in another project. To allow one everywhere, add a rule such as `Bash(ccbabysitter babysit *)` to `permissions.allow` in `~/.claude/settings.json` (see Claude Code's permissions documentation). Never allow `Bash(ccbabysitter *)`: it would also cover `ccbabysitter status`, whose output carries the page's key.

Without a card (Claude Code's own prompt still applies): `ccbabysitter version`, `ccbabysitter help`, `ccbabysitter list`, `ccbabysitter show`, `ccbabysitter activity`, `ccbabysitter babysit`, `ccbabysitter unbabysit`, `ccbabysitter retry` and `ccbabysitter open`.

After asking you on a card: `ccbabysitter stop`, `ccbabysitter settings` changes, `ccbabysitter install`, `ccbabysitter uninstall` and `ccbabysitter quit`. It never runs `ccbabysitter reset`, and never installs anything.

On macOS it also runs `pmset -g batt`, once in a conversation, to see whether the computer is a laptop, so it can mention that closing the lid makes the computer sleep. Its output has no serial number.

When CC Babysitter is not installed or not running, the plugin shows you the command that installs or starts it, for you to run in your terminal app. It never runs that command itself.

It never runs a command whose output carries the page's key, so the key never enters your chat.

## What it reads

The plugin reads only what CC Babysitter's commands answer, and its two reference files beside the skill.

CC Babysitter reads Claude Code's own local files and never writes to them:

- the session files in `~/.claude/sessions`: each session's id, folder, name and process;
- the transcripts in `~/.claude/projects`: token and turn counts, the model and the session's title. It reads no message content, except the first characters of a session's first message, to tell a run of a Claude Desktop scheduled task from an ordinary session, and it keeps nothing of it;
- Claude Code's settings in `~/.claude.json`: only whether each folder is trusted, nothing else;
- the background job records in `~/.claude/jobs`, when bringing a babysat session back: only whether it ran in the background before, and whether its saved options turn Remote Control on;
- the end of the background daemon's log, `~/.claude/daemon.log`, when a babysat session ends: only whether Claude Code stopped it for being idle.

## Privacy

CC Babysitter has no telemetry and no account, and collects nothing. The plugin's commands talk only to CC Babysitter on `127.0.0.1`, where its page is served too. A session CC Babysitter starts or brings back is an ordinary Claude Code session that connects to Anthropic, usually with Remote Control on, so you can reach it from your Claude account. The only downloads are installing and updating CC Babysitter, with a command you run yourself; the plugin shows it when CC Babysitter is not installed. That command fetches the install script from ccbabysitter.dev, then the release and its `checksums.txt` from https://github.com/pejmanebrahimi/ccbabysitter/releases.

## Help

- How to use the plugin: https://ccbabysitter.dev/docs/claude-code-plugin/
- Troubleshooting, with an entry for every warning: https://ccbabysitter.dev/docs/troubleshooting/
- Report a problem or ask a question: email hello@ccbabysitter.dev, or open an issue at https://github.com/pejmanebrahimi/ccbabysitter/issues
- Report a vulnerability privately: https://github.com/pejmanebrahimi/ccbabysitter/security/advisories/new, as https://github.com/pejmanebrahimi/ccbabysitter/blob/main/SECURITY.md describes

## Tests

The `evals` folder holds the plugin's test cases. They run against a stand-in `ccbabysitter` script that lives outside the plugin, in the repository's `scripts` folder (`sh scripts/plugin-evals.sh`). Claude Code never loads them.

## License

MIT. See `LICENSE`.
