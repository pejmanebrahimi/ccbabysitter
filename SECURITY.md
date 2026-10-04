# Security

## Reporting a vulnerability

Please report vulnerabilities privately, not in a public issue. On the repository at https://github.com/pejmanebrahimi/ccbabysitter, open the Security tab and choose "Report a vulnerability", or go straight to https://github.com/pejmanebrahimi/ccbabysitter/security/advisories/new. Only you and the maintainers see the report.

Please include the output of `ccbabysitter version`, your operating system, and the steps that show the problem. You will get an answer in the advisory.

Only the latest release gets security fixes while CC Babysitter is a preview.

## In scope

- The local page and its guard. The page is served on `127.0.0.1`, and every request must carry the page's key, which only your account can read. A way for another web page, another account on the same machine, another machine, or a DNS name to read the page's data or make it act is in scope. Browsers share cookies across all ports of `127.0.0.1`, so on a computer shared with other accounts, a website you visit can send your browser to another account's local server, which would then receive the page's cookie. Running CC Babysitter on a server over SSH is not affected, because the browser runs on your own computer.
- The files and settings it writes: its state folder, the start-at-login entry (a systemd user unit on Linux, a LaunchAgent on macOS, a Run value under `HKCU` on Windows), and the scripts it writes into its state folder to open a terminal.
- The commands it runs: the `claude` CLI, `systemctl --user`, `loginctl`, `launchctl`, `reg`, its own windowless program on Windows, and the programs it starts to open a browser or a terminal. A session name, folder or other value that makes one of these run something other than what was meant is in scope.
- The install scripts, `install.sh` and `install.ps1`, and the release binaries and checksums they download.

## Out of scope

- Claude Code itself. Report problems in Claude Code to Anthropic.
- Anything that needs a program already running as your own user: such a program can already do whatever you can.
