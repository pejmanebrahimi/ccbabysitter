// Package buildinfo holds the compiled-in identity of CC Babysitter.
package buildinfo

// Version is MAJOR.MINOR.PATCH, or MAJOR.MINOR.PATCH-<pre> such as 0.5.0-rc.1
// for a pre-release. Bump per docs/CHANGELOG.md when a build is handed out.
const Version = "0.5.0"

// Name is the product name shown in the UI and the console.
const Name = "CC Babysitter"

// VerifiedCLIVersion is the oldest Claude Code CLI version this build's
// behaviour was checked against. A CLI older than this may word its output
// differently, so the console says so on startup rather than leaving the
// user to work out why something did not take.
const VerifiedCLIVersion = "2.1.275"
