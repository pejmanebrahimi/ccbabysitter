# AGENTS.md

Notes for AI coding agents working on CC Babysitter's code. Humans: see `CONTRIBUTING.md`, which has the same rules with more words.

To use CC Babysitter rather than change it (list sessions, babysit one, babysit the session you run in), run `ccbabysitter --help`.

## What this is

A single Go binary that keeps Claude Code sessions alive and reachable. It watches every Claude Code session on the machine, and for a session the user babysits it keeps the computer awake and, if the session's app dies, starts the same session again in the background with Remote Control (`claude --bg --resume`). It serves a local page on `127.0.0.1` to see and control all this. Where the system allows it, a plain `ccbabysitter` starts it in the background and gives the terminal back: as a systemd user unit on Linux, a LaunchAgent on macOS, and a windowless second program on Windows, each starting again at login, or at boot on a Linux server.

## Layout

- `cmd/ccbabysitter`: the command line, the launcher that starts CC Babysitter in the background and the service code for each system (systemd, launchd, the Windows Run key and windowless program), serving the page, start at login. On Windows the same code is also built as `ccbabysitter-background.exe`, the windowless background copy.
- `internal/client`: the client the control commands (`list`, `babysit`, `stop`, ...) use to talk to the running copy through the same local API as the page.
- `internal/claude`: reads Claude Code's own session files and parses the `claude` CLI's output. Read-only.
- `internal/hosts`: the apps a session can live in (terminal, background, Claude Desktop, VS Code), what is installed, and the exact `claude` command lines the program runs.
- `internal/observe`: turns session files into snapshots and change events.
- `internal/procs`: process identity. A pid is never trusted alone; its creation time must match too.
- `internal/supervise`: the babysitting policy (`Decide` in `rules.go`, pure and easy to test) and the supervisor that acts on it.
- `internal/power`: the user-mode keep-awake request.
- `internal/state`: CC Babysitter's own folder of state.
- `internal/web`: the local HTTP API, the event stream, the request guard, the demo engine, and the page itself in `internal/web/ui` (plain HTML, CSS and JavaScript, embedded, no build step).
- `site`: the static website, also plain files. `scripts`: the install scripts and their test.

## Checks every change must pass

```
go build ./...
go vet ./...
go test -race ./...
gofmt -l .        # must print nothing
sh scripts/test-install.sh    # when scripts/ changes
```

The page and site tests run JavaScript under `node` and skip themselves when it is missing; install Node.js if you touch `internal/web/ui` or `site`. CI runs the tests on Linux, macOS and Windows.

To look at the page without touching a real session: `go run ./cmd/ccbabysitter --demo`.

## Rules

- **Never touch the user's real sessions from tests or while trying things out.** Tests use the fakes (`claude.NewFakeRunner`, `procs.NewFake`, the fakes in each package's tests) and temporary folders. Do not run the real binary against the user's machine unless they ask, and never end a process you did not start.
- **Keep the hard rules in `README.md`:** opt-in only, every automatic action explained in the Activity panel, Claude's files read-only, Claude Desktop and VS Code never modified or opened, loopback only, user mode only.
- **Do not weaken `internal/web/guard.go`.** Every request goes through it. A new route needs no exception, and a change to the guard needs a test that shows another web page still cannot reach the API.
- **Before ending or starting anything, match the process exactly** (pid and creation time, through `internal/procs`), never by name alone.
- **Cross-platform.** Code that differs per OS lives in `_darwin.go`, `_linux.go`, `_windows.go` files or behind `runtime.GOOS`. Build shell and PowerShell command lines with the quoting helpers in `internal/hosts/targets.go`; never quote by hand.
- **Plain ASCII** in every shipped file: no curly quotes or long dashes. A test checks this.
- **No new dependencies** without an issue that agrees on it first.
- **The page and the command line stay in step.** Both drive the engine through the local API. A new action on the page needs its command, its `help` page and its README line; a change to a command's `--json` output only adds fields within a `schema` number.
- **Comments and docs say what the code does now**, for a reader who knows nothing of its history.

## Sending a change

Small pull requests, one thing each, with tests. Open an issue first for anything that changes what CC Babysitter does. Say in the pull request what you checked by hand. Security problems go through `SECURITY.md`, not a public issue.
