package hosts

import (
	"regexp"
	"runtime"
	"strings"
)

// targetOS is the operating system inFolder renders commands for. It
// defaults to the build's own OS but is a variable, rather than a direct
// runtime.GOOS reference, so tests can set it to any OS and check the
// rendered form without a matching build tag.
var targetOS = runtime.GOOS

// BackgroundResumeArgs builds the argument list for resuming a session in
// the background. A session that has already saved its own options is
// resumed flagless, exactly as it was started; otherwise remote control is
// requested. It never names the session: the transcript already carries
// its title, and every extra flag is one more reason for the CLI to fork a
// copy instead.
func BackgroundResumeArgs(id string, hasSavedOptions bool) []string {
	args := []string{"--bg", "--resume", id}
	if hasSavedOptions {
		return args
	}
	return append(args, "--remote-control")
}

// StopArgs builds the argument list for stopping a session by its short id.
func StopArgs(short string) []string { return []string{"stop", short} }

// RemoveArgs builds the argument list for removing a session by its short
// id.
func RemoveArgs(short string) []string { return []string{"rm", short} }

// AgentsArgs builds the argument list for listing sessions as JSON.
func AgentsArgs() []string { return []string{"agents", "--json"} }

// VisibleResumeCommand renders the command line a person would type in a
// terminal or desktop window to resume id.
func VisibleResumeCommand(id string, remoteControl bool) string {
	if remoteControl {
		return "claude --resume " + id + " --remote-control"
	}
	return "claude --resume " + id
}

// ResumeCommandIn renders the command line that resumes id from the folder
// it was started in. `claude --resume` looks a conversation up in the
// project of the folder it runs from, so the command changes there first.
// The folder is quoted for PowerShell on Windows and for a POSIX shell
// everywhere else; with no folder known, the bare resume command is all
// there is to give.
func ResumeCommandIn(cwd, id string) string {
	return inFolder(cwd, VisibleResumeCommand(id, false))
}

// BackgroundResumeCommandIn renders the command line that resumes id as a
// background session from the folder it was started in, for a person to
// run by hand when this program could not.
func BackgroundResumeCommandIn(cwd, id string) string {
	return inFolder(cwd, "claude --bg --resume "+id)
}

// inFolder prefixes command with a change to cwd and leaves it as it is
// when no folder is known. On Windows the command targets PowerShell,
// since Windows PowerShell 5.1 (the default Windows shell) has no `&&`
// and cmd.exe reads single quotes literally rather than as quoting; the
// folder is passed as -LiteralPath, because a plain cd reads [ and ] in it
// as a wildcard. Every other OS keeps the POSIX form, with the path quoted
// for a POSIX shell.
func inFolder(cwd, command string) string {
	if cwd == "" {
		return command
	}
	if targetOS == "windows" {
		return "cd -LiteralPath " + powerShellQuote(cwd) + "; " + command
	}
	return "cd " + ShellQuote(cwd) + " && " + command
}

// powerShellQuote returns s single-quoted for PowerShell. PowerShell ends
// a single-quoted string on the typographic single quotes as well as on
// the plain one, so each of them inside the string is escaped by doubling
// it; nothing else needs escaping there. Windows paths are always quoted,
// since they routinely contain spaces.
func powerShellQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '\u2018', '\u2019', '\u201a', '\u201b':
			b.WriteRune(r)
		}
		b.WriteRune(r)
	}
	b.WriteByte('\'')
	return b.String()
}

// ShellQuote returns s in a form a POSIX shell reads back as exactly s, a
// leading ~/ aside, which is left for the shell to expand. A
// string made only of characters no shell treats specially is left as it
// is, so a plain path stays easy to read; anything else is put in single
// quotes, with each single quote inside closed, escaped and reopened.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if rest, ok := strings.CutPrefix(s, "~/"); ok && rest != "" {
		// A home-relative path keeps its tilde outside the quotes, where
		// the shell still expands it.
		return "~/" + ShellQuote(rest)
	}
	plain := true
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("/._-+,:@%=", r):
		default:
			plain = false
		}
		if !plain {
			break
		}
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// safeSSHUser matches a user name that is one plain token to ssh: it
// starts with no dash, so ssh cannot read it as an option, and holds
// nothing a shell would treat specially.
var safeSSHUser = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*$`)

// SSHTarget renders the destination word of an ssh command. An IPv6
// address goes in bare: ssh reads everything after the user@ as the host,
// and would keep brackets as part of the name. A user that is not a safe
// single token is left out, so the command names the address alone and
// ssh uses the local user name.
func SSHTarget(user, address string) string {
	if !safeSSHUser.MatchString(user) {
		return address
	}
	return user + "@" + address
}

// AttachCommand renders the command line for attaching to a background
// session by its short id.
func AttachCommand(short string) string { return "claude attach " + short }

// SSHAttachCommand renders the command line that attaches to a background
// session on this machine from another one, where target is user@host
// (quoted here for the person's shell when it needs it) and cli is the
// full path to the CLI on this machine. A command run over ssh
// does not read the part of the shell's startup file that adds the
// installer's folder to PATH, so the CLI is named by its full path, never
// through ~, which the person's own shell would expand to their own home.
// On a POSIX server the path is quoted twice when it needs quoting at all:
// once for the shell on this machine, which ssh hands the command to, and
// once more for the shell the person types the line into. On a Windows
// server ssh hands the command to cmd.exe, which does not read single
// quotes, so a path with a space is put in double quotes for it instead,
// and the result is quoted once for the person's own shell, which would
// otherwise swallow the backslashes. With no path known, the bare name is
// all there is to give.
func SSHAttachCommand(target, cli, short string) string {
	if cli == "" {
		cli = "claude"
	}
	var remote string
	if targetOS == "windows" {
		remote = cli
		if strings.ContainsAny(cli, " \t") {
			remote = `"` + cli + `"`
		}
	} else {
		remote = ShellQuote(cli)
	}
	return "ssh -t " + ShellQuote(target) + " " + ShellQuote(remote) + " attach " + short
}
