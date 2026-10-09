# Contributing to CC Babysitter

Thanks for helping. This page says how to build and test CC Babysitter and how changes get in.

## Build and test

You need Go 1.27 or later. The page tests in `internal/web` run the page's JavaScript under `node` and are skipped when it is missing, so install Node.js too if you change anything under `internal/web`.

```
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

`gofmt -l .` must print nothing. CI also runs the tests with `-race` on macOS and Linux.

To try the page without touching any real session, run `go run ./cmd/ccbabysitter --demo`.

The writing of the README and the website is checked with [Vale](https://vale.sh), as CI does. Install it (`brew install vale`, or a release from its GitHub page), then run `vale README.md site` from the top of the tree. Error-level findings fail the check, and warnings and suggestions are advice. `.vale.ini` says which rules apply and why some are turned down, and the Go tests run Vale too when it is installed. The styles in `.vale/styles` are copied verbatim from their releases, and are the one exception to plain ASCII. A linter is only a floor: also read a changed page aloud, and check that it does one job.

Some files are made from others: the plugin's references and the docs' command reference come from the help pages in `cmd/ccbabysitter/help.go`, and the docs' How it works page and the plugin's how-it-works file from sections of `README.md`. If you change either, run `go test ./cmd/ccbabysitter -update-reference` and commit what it rewrites. Until you do, a test fails.
The docs search reads `site/docs/search.json`, made from the docs pages. If you change a docs page, or the help text or README sections that pages are made from, run `go test ./site -run TestDocsSearchIndex -update-search` and commit it. Until you do, a test fails.

The docs show screenshots of the page, taken from the demo. If you change `internal/web/ui` or `internal/web/demo.go`, run `node scripts/docs-shots.mjs` and commit the new pictures in `site/docs/shots`. Until you do, a test fails. It needs Go, Node.js 22 or later and Google Chrome or Chromium, and `CHROME` names the browser when it is not in its usual place.

If you change `scripts/install.sh`, run `scripts/test-install.sh`. It builds CC Babysitter, serves the release files from this machine, and runs the script against them with a temporary home folder. It needs `go`, `python3`, `curl`, and `sha256sum` or `shasum`.

## Issues

- **Kinds.** An issue's kind is its label. The two issue forms give `bug`, for something that does not work as it should, and `enhancement`, for a new feature or an improvement. Maintainers also use `question` and `documentation`, and mark issues where outside help is welcome with `help wanted` and `good first issue`.
- **Bugs** follow the bug report form: versions and system, steps to reproduce, what happened, what you expected, and the Activity lines. A bug that cannot be reproduced from the issue may get a request for more detail.
- **Enhancements** say the problem first, then a short proposal. Once the approach is agreed, the issue gets a "Done when" checklist that the pull request is checked against. The details of how belong in the pull request.
- **Big features** start as one issue where the approach is agreed. They are split into sub-issues only when the pieces ship separately.
- **Milestones.** Issues that must be done before 1.0 are in the `1.0` milestone.
- **Triage.** New issues are usually read and labelled within a few days. An issue waiting for information from its author for two weeks may be closed; it can be reopened when the information arrives. Closed issues say why: completed, not planned (with the reason), or duplicate (with a link).

## Sending a change

- `main` is protected. Every change comes as a pull request, and it is merged only when CI is green: `go vet`, the tests on Linux, macOS and Windows, and the `gofmt` check.
- Keep pull requests small and focused on one thing. A small change is easier to review and quicker to merge.
- For a larger change, or anything that changes what CC Babysitter does, open an issue first so we can agree on the approach before you spend time on it.
- When a pull request fixes an issue, write `Fixes #N` in its description, so merging it closes the issue.
- A change users will notice adds a line under `## Unreleased` in `docs/CHANGELOG.md`. A release renames that heading to `## X.Y.Z (date)`.
- Keep to the hard rules in `README.md`: opt-in only, every automatic action explained, Claude's files read-only, loopback only, user mode only.
- Shipped files are plain ASCII text: no curly quotes, long dashes or other characters outside ASCII. `go test ./...` checks this for the code, the documents, the scripts and the workflows.

## Security problems

Do not open a public issue for a vulnerability. See `SECURITY.md`.

## License

By contributing, you agree that your contribution is licensed under the MIT license in `LICENSE`.
