#!/bin/sh
# Tests scripts/install.sh against release files served from this machine.
#
#   scripts/test-install.sh                  run install.sh with /bin/sh
#   scripts/test-install.sh dash             ... or with another shell,
#   scripts/test-install.sh bash --posix     given as a command and its
#   scripts/test-install.sh zsh --emulate sh arguments
#
# It builds CC Babysitter for this machine into a temporary folder, writes
# a checksums.txt beside it, serves the folder with python3 -m http.server
# on a free loopback port and runs install.sh with CCBABYSITTER_DOWNLOAD_URL
# pointing there. Every run gets its own temporary HOME, install folder and
# TMPDIR, and a clean environment, so nothing touches the real home folder.
#
# The Linux server path, where install.sh runs the installed program to set
# up a service, is tested with a stand-in program that only records how it
# was called. The real program is only ever run as "ccbabysitter version".
#
# Needs go, python3, curl and sha256sum or shasum. Exits 0 when every check
# passes.

set -u

here=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$here")
script="$here/install.sh"

if [ "$#" -gt 0 ]; then
	shell_name="$1"
	shift
	shell_args="$*"
else
	shell_name=/bin/sh
	shell_args=""
fi
shell_path=$(command -v "$shell_name") || {
	echo "no such shell: $shell_name" >&2
	exit 2
}
real_curl=$(command -v curl) || {
	echo "curl is needed" >&2
	exit 2
}
base_path="/usr/bin:/bin:/usr/sbin:/sbin"

work=$(mktemp -d)
server_pid=""

cleanup() {
	if [ -n "$server_pid" ]; then
		kill "$server_pid" 2>/dev/null
		wait "$server_pid" 2>/dev/null
		server_pid=""
	fi
	rm -rf "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

passed=0
failed=0

ok() {
	passed=$((passed + 1))
}

bad() {
	failed=$((failed + 1))
	printf '  FAIL [%s] %s\n' "$case_name" "$*"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# --- release files ---------------------------------------------------------

host_os=$(cd "$root" && go env GOOS)
host_arch=$(cd "$root" && go env GOARCH)
case "$host_os" in
darwin | linux) ;;
*)
	echo "install.sh is for macOS and Linux; this is $host_os" >&2
	exit 2
	;;
esac
host_asset="ccbabysitter-$host_os-$host_arch"

srv="$work/srv"
mkdir -p "$srv/good" "$srv/bad" "$srv/missing" "$srv/star" "$srv/fake"

echo "Building $host_asset"
(cd "$root" && CGO_ENABLED=0 go build -o "$srv/good/$host_asset" ./cmd/ccbabysitter) || exit 1
want_version=$("$srv/good/$host_asset" version)

# good: the real program and its checksum.
printf '%s  %s\n' "$(sha256 "$srv/good/$host_asset")" "$host_asset" >"$srv/good/checksums.txt"

# bad: the real program with a checksum that does not match it.
cp "$srv/good/$host_asset" "$srv/bad/"
printf '%s  %s\n' 0000000000000000000000000000000000000000000000000000000000000000 "$host_asset" >"$srv/bad/checksums.txt"

# missing: a checksums.txt with no line for this machine's file.
cp "$srv/good/$host_asset" "$srv/missing/"
printf '%s  %s\n' "$(sha256 "$srv/good/$host_asset")" "$host_asset.old" >"$srv/missing/checksums.txt"

# star: the binary-mode form sha256sum -b writes, in upper case, with CRLF.
cp "$srv/good/$host_asset" "$srv/star/"
printf '%s *%s\r\n' "$(sha256 "$srv/good/$host_asset" | tr 'a-f' 'A-F')" "$host_asset" >"$srv/star/checksums.txt"

# fake: stand-ins for every macOS and Linux build, which record each call
# in $FAKE_LOG, with how many bytes a run with no arguments could read from
# its stdin, and exit with $FAKE_EXIT.
: >"$srv/fake/checksums.txt"
for name in ccbabysitter-darwin-amd64 ccbabysitter-darwin-arm64 ccbabysitter-linux-amd64 ccbabysitter-linux-arm64; do
	cat >"$srv/fake/$name" <<'EOF'
#!/bin/sh
if [ "${1:-}" = version ]; then
	printf 'argc=%s args=%s\n' "$#" "$*" >>"$FAKE_LOG"
	echo "CC Babysitter 9.9.9"
	exit 0
fi
printf 'argc=%s args=%s stdin=%s\n' "$#" "$*" "$(wc -c | tr -d ' ')" >>"$FAKE_LOG"
echo "stand-in service setup"
exit "${FAKE_EXIT:-0}"
EOF
	printf '%s  %s\n' "$(sha256 "$srv/fake/$name")" "$name" >>"$srv/fake/checksums.txt"
done

# --- server ----------------------------------------------------------------

port=$(python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
python3 -m http.server --bind 127.0.0.1 --directory "$srv" "$port" >"$work/server.log" 2>&1 &
server_pid=$!
url="http://127.0.0.1:$port"
tries=0
until "$real_curl" -fsS -o /dev/null "$url/good/checksums.txt" 2>/dev/null; do
	tries=$((tries + 1))
	if [ "$tries" -gt 100 ]; then
		echo "the test server did not start" >&2
		cat "$work/server.log" >&2
		exit 1
	fi
	sleep 0.1
done

# --- cases -----------------------------------------------------------------

# new_case NAME makes a fresh folder for one run: c/home, c/tmp (TMPDIR),
# c/shim (first on PATH) and c/run (the working folder). The shims make
# pgrep find no running copy, give the machine a browser opener, so a
# Linux machine running these tests is a desktop unless a case says not,
# and stand in for systemctl as a user manager that answers, logging each
# call to c/systemctl.log, so the real one is never asked anything.
new_case() {
	case_name="$1"
	c="$work/cases/$1"
	if [ -e "$c" ]; then
		echo "case folder $c is used twice" >&2
		exit 2
	fi
	mkdir -p "$c/home" "$c/tmp" "$c/shim" "$c/run"
	printf '#!/bin/sh\nexit 1\n' >"$c/shim/pgrep"
	printf '#!/bin/sh\nexit 0\n' >"$c/shim/xdg-open"
	shim_systemctl 0
	chmod 755 "$c/shim/pgrep" "$c/shim/xdg-open"
	: >"$c/fake.log"
	path="$c/shim:$base_path"
	how=file
}

# shim_systemctl STATUS [ACTIVE] makes systemctl exit with STATUS, and its
# is-active question with ACTIVE: 3, not running, unless given.
shim_systemctl() {
	printf '#!/bin/sh\nprintf "%%s\\n" "$*" >>"%s"\ncase " $* " in *" is-active "*) exit %s ;; esac\nexit %s\n' "$c/systemctl.log" "${2:-3}" "$1" >"$c/shim/systemctl"
	chmod 755 "$c/shim/systemctl"
}

# shim_uname OS MACHINE makes uname report another system.
shim_uname() {
	cat >"$c/shim/uname" <<EOF
#!/bin/sh
case "\${1:-}" in
-m) echo "$2" ;;
*) echo "$1" ;;
esac
EOF
	chmod 755 "$c/shim/uname"
}

# run_install [NAME=VALUE...] runs install.sh in a clean environment; the
# arguments are added to it, later ones winning. $how says how the shell
# gets the script: file (as an argument), redirect (sh < install.sh) or
# pipe (cat install.sh | sh, the way curl | sh hands it over). Output goes
# to c/out and the exit status to $status.
run_install() {
	(
		cd "$c/run" || exit 99
		set -- env -i \
			HOME="$c/home" \
			TMPDIR="$c/tmp" \
			PATH="$path" \
			SHELL=/bin/sh \
			DISPLAY=:0 \
			FAKE_LOG="$c/fake.log" \
			"$@" \
			"$shell_path" $shell_args
		case "$how" in
		file) "$@" "$script" </dev/null ;;
		redirect) "$@" <"$script" ;;
		pipe) cat "$script" | "$@" ;;
		esac
	) >"$c/out" 2>&1
	status=$?
}

expect_status() {
	if [ "$status" = "$1" ]; then ok; else bad "exit status $status, want $1"; fi
}

expect_out() {
	if grep -F -q -- "$1" "$c/out"; then ok; else bad "output lacks: $1"; fi
}

expect_no_out() {
	if grep -F -q -- "$1" "$c/out"; then bad "output has: $1"; else ok; fi
}

expect_installed() {
	if [ -x "$1/ccbabysitter" ] && [ "$("$1/ccbabysitter" version 2>/dev/null)" = "$2" ]; then
		ok
	else
		bad "$1/ccbabysitter is not an installed copy printing \"$2\""
	fi
}

expect_absent() {
	if [ -e "$1" ] || [ -L "$1" ]; then bad "$1 exists"; else ok; fi
}

# expect_only DIR NAMES... checks DIR holds exactly those entries.
expect_only() {
	d="$1"
	shift
	got=$(cd "$d" && ls -A | tr '\n' ' ')
	want=""
	for n in "$@"; do
		want="$want$n "
	done
	if [ "$got" = "$want" ]; then ok; else bad "$d holds \"$got\", want \"$want\""; fi
}

# expect_clean checks the temporary folder was removed and nothing was
# written anywhere else in the home folder than the install folder.
expect_clean() {
	expect_only "$c/tmp"
}

expect_fake_calls() {
	got=$(tr '\n' ';' <"$c/fake.log")
	if [ "$got" = "$1" ]; then ok; else bad "stand-in calls \"$got\", want \"$1\""; fi
}

show_on_failure() {
	if [ "$failed" != "$1" ]; then
		sed 's/^/    | /' "$c/out"
	fi
}

check() {
	before=$failed
	"$@"
	show_on_failure "$before"
}

# 1. A desktop install into the default folder, which is not on PATH.
case_default_dir() {
	new_case default-dir
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" SHELL=/bin/zsh
	expect_status 0
	expect_installed "$c/home/.local/bin" "$want_version"
	expect_only "$c/home" .local
	expect_only "$c/home/.local" bin
	expect_only "$c/home/.local/bin" ccbabysitter
	expect_out "Installed $want_version to $c/home/.local/bin/ccbabysitter"
	expect_out "$c/home/.local/bin is not on your PATH."
	expect_out "~/.zshrc"
	expect_out '  export PATH="$HOME/.local/bin:$PATH"'
	expect_out "Start it with: $c/home/.local/bin/ccbabysitter"
	expect_no_out "already running"
	expect_clean
}

# 2. The PATH hint names the right file for the shell.
case_hint_bash() {
	new_case hint-bash
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" SHELL=/bin/bash
	expect_status 0
	expect_out "add this line to ~/.bashrc"
	expect_out '  export PATH="$HOME/.local/bin:$PATH"'
	expect_clean
}

case_hint_sh() {
	new_case hint-sh
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" SHELL=/bin/sh
	expect_status 0
	expect_out "add this line to ~/.profile"
	expect_out '  export PATH="$HOME/.local/bin:$PATH"'
	expect_clean
}

case_hint_fish() {
	new_case hint-fish
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" SHELL=/usr/local/bin/fish
	expect_status 0
	expect_out "  fish_add_path \"$c/home/.local/bin\""
	expect_no_out "export PATH"
	expect_clean
}

# 3. A folder outside HOME is named as it is in the hint.
case_hint_outside_home() {
	new_case hint-outside-home
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" CCBABYSITTER_INSTALL_DIR="$c/opt/bin" SHELL=/bin/zsh
	expect_status 0
	expect_installed "$c/opt/bin" "$want_version"
	expect_out "  export PATH=\"$c/opt/bin:\$PATH\""
	expect_out "Start it with: $c/opt/bin/ccbabysitter"
	expect_only "$c/home"
	expect_clean
}

# 4. A folder already on PATH gets no hint and the short command.
case_on_path() {
	new_case on-path
	path="$c/shim:$c/bin:$base_path"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" CCBABYSITTER_INSTALL_DIR="$c/bin/"
	expect_status 0
	expect_installed "$c/bin" "$want_version"
	expect_no_out "not on your PATH"
	expect_out "Start it with: ccbabysitter"
	expect_no_out "Start it with: $c"
	expect_only "$c/home"
	expect_clean
}

# A copy that comes earlier on PATH is pointed out, and the new one is
# started by its full path.
case_shadowed() {
	new_case shadowed
	mkdir -p "$c/other"
	printf '#!/bin/sh\necho old\n' >"$c/other/ccbabysitter"
	chmod 755 "$c/other/ccbabysitter"
	path="$c/shim:$c/other:$c/bin:$base_path"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" CCBABYSITTER_INSTALL_DIR="$c/bin"
	expect_status 0
	expect_installed "$c/bin" "$want_version"
	expect_no_out "not on your PATH"
	expect_out "Another copy, $c/other/ccbabysitter, comes first on your PATH."
	expect_out "Start it with: $c/bin/ccbabysitter"
	expect_clean
}

# A PATH entry with a trailing slash is still this folder: no hint and
# no warning about another copy.
case_path_trailing_slash() {
	new_case path-trailing-slash
	path="$c/shim:$c/bin/:$base_path"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" CCBABYSITTER_INSTALL_DIR="$c/bin"
	expect_status 0
	expect_installed "$c/bin" "$want_version"
	expect_no_out "not on your PATH"
	expect_no_out "Another copy"
	expect_out "Start it with: ccbabysitter"
	expect_clean
}

# 5. A running copy is told to be restarted.
case_running() {
	new_case running
	printf '#!/bin/sh\nexit 0\n' >"$c/shim/pgrep"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good"
	expect_status 0
	expect_out "CC Babysitter is already running. Quit it and start it again to use the new version."
	expect_clean
}

# 6. An older copy is replaced.
case_replace() {
	new_case replace
	mkdir -p "$c/home/.local/bin"
	printf '#!/bin/sh\necho old\n' >"$c/home/.local/bin/ccbabysitter"
	chmod 755 "$c/home/.local/bin/ccbabysitter"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good"
	expect_status 0
	expect_installed "$c/home/.local/bin" "$want_version"
	expect_only "$c/home/.local/bin" ccbabysitter
	expect_clean
}

# 7. A checksum mismatch stops without touching the installed copy.
case_mismatch() {
	new_case mismatch
	mkdir -p "$c/home/.local/bin"
	printf '#!/bin/sh\necho old\n' >"$c/home/.local/bin/ccbabysitter"
	chmod 755 "$c/home/.local/bin/ccbabysitter"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/bad"
	expect_status 1
	expect_out "does not match its checksum"
	expect_out "Nothing was installed."
	expect_installed "$c/home/.local/bin" old
	expect_only "$c/home/.local/bin" ccbabysitter
	expect_no_out "Installed"
	expect_clean
}

# 8. A checksums.txt with no line for the file stops.
case_missing_line() {
	new_case missing-line
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/missing"
	expect_status 1
	expect_out "checksums.txt has no line for $host_asset"
	expect_absent "$c/home/.local"
	expect_clean
}

# 9. The binary-mode, upper case, CRLF form of checksums.txt is accepted.
case_star_format() {
	new_case star-format
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/star"
	expect_status 0
	expect_installed "$c/home/.local/bin" "$want_version"
	expect_clean
}

# 10. A failed download stops.
case_download_fails() {
	new_case download-fails
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/nothing-here"
	expect_status 1
	expect_out "could not download $url/nothing-here/$host_asset"
	expect_absent "$c/home/.local"
	expect_clean
}

# 11. Systems without a build are refused, pointing Windows users to the
# PowerShell command.
case_unknown_os() {
	new_case unknown-os
	shim_uname FreeBSD amd64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake"
	expect_status 1
	expect_out "there is no CC Babysitter build for FreeBSD here."
	expect_out "  irm https://ccbabysitter.dev/install.ps1 | iex"
	expect_no_out "Downloading"
	expect_absent "$c/home/.local"
	expect_clean
}

case_windows_shell() {
	new_case windows-shell
	shim_uname MINGW64_NT-10.0-26100 x86_64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake"
	expect_status 1
	expect_out "On Windows, run this in PowerShell instead:"
	expect_out "  irm https://ccbabysitter.dev/install.ps1 | iex"
	expect_absent "$c/home/.local"
	expect_clean
}

case_unknown_arch() {
	new_case unknown-arch
	shim_uname Linux i686
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake"
	expect_status 1
	expect_out "there is no CC Babysitter build for the i686 processor."
	expect_no_out "Downloading"
	expect_absent "$c/home/.local"
	expect_clean
}

# 12. uname -m spellings map to the release names.
case_arch_names() {
	for pair in Linux:x86_64:linux-amd64 Linux:amd64:linux-amd64 Linux:aarch64:linux-arm64 Linux:arm64:linux-arm64 Darwin:arm64:darwin-arm64; do
		os=${pair%%:*}
		rest=${pair#*:}
		machine=${rest%%:*}
		want=${rest#*:}
		new_case "arch-$os-$machine"
		shim_uname "$os" "$machine"
		run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake"
		expect_status 0
		expect_out "Downloading ccbabysitter-$want from"
		expect_out "Installed CC Babysitter 9.9.9"
		expect_fake_calls "argc=1 args=version;"
		expect_clean
	done
}

# 13. Linux with no display: the installed program is run with no
# arguments and its exit status is the script's.
case_linux_no_display() {
	new_case linux-no-display
	shim_uname Linux x86_64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= FAKE_EXIT=3
	expect_status 3
	expect_out "stand-in service setup"
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_no_out "Start it with"
	expect_clean
}

case_linux_ssh() {
	new_case linux-ssh
	shim_uname Linux aarch64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" SSH_CONNECTION="192.0.2.1 50000 192.0.2.2 22"
	expect_status 0
	expect_out "stand-in service setup"
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_no_out "Start it with"
	expect_clean
}

case_linux_ssh_tty() {
	new_case linux-ssh-tty
	shim_uname Linux x86_64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" SSH_TTY=/dev/pts/0
	expect_status 0
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_clean
}

# The program also treats a display with no browser opener as a server,
# so the script does the same rather than leave it to a run in this shell.
case_linux_no_opener() {
	new_case linux-no-opener
	shim_uname Linux x86_64
	rm "$c/shim/xdg-open"
	path="$c/shim:$work/no-opener-bin"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake"
	expect_status 0
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_clean
}

# No display, but a plain run would not set up a service there: it would
# serve in the foreground and the install would never return. So the
# program is not run, and the script says how to start it instead.
case_linux_no_user_manager() {
	new_case linux-no-user-manager
	shim_uname Linux x86_64
	shim_systemctl 1
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY=
	expect_status 0
	expect_fake_calls "argc=1 args=version;"
	if [ "$(cat "$c/systemctl.log")" = "--user show-environment" ]; then ok; else bad "systemctl calls: $(cat "$c/systemctl.log")"; fi
	expect_out "Start it with: $c/home/.local/bin/ccbabysitter"
	expect_no_out "stand-in service setup"
	expect_clean
}

case_linux_no_systemctl() {
	new_case linux-no-systemctl
	shim_uname Linux x86_64
	rm "$c/shim/systemctl"
	path="$c/shim:$work/no-systemctl-bin"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY=
	expect_status 0
	expect_fake_calls "argc=1 args=version;"
	expect_out "Start it with:"
	expect_clean
}

case_linux_invocation_id() {
	new_case linux-invocation-id
	shim_uname Linux x86_64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= INVOCATION_ID=0123456789abcdef
	expect_status 0
	expect_fake_calls "argc=1 args=version;"
	expect_out "Start it with:"
	expect_clean
}

# The script reaches the shell on stdin, as with curl | sh: the install
# works, and the program run for the service gets an empty stdin rather
# than the rest of the script.
case_stdin_pipe() {
	new_case stdin-pipe
	how=pipe
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good"
	expect_status 0
	expect_installed "$c/home/.local/bin" "$want_version"
	expect_out "Start it with: $c/home/.local/bin/ccbabysitter"
	expect_clean
}

case_stdin_pipe_service() {
	new_case stdin-pipe-service
	shim_uname Linux aarch64
	how=pipe
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= FAKE_EXIT=4
	expect_status 4
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_clean
}

case_stdin_redirect() {
	new_case stdin-redirect
	shim_uname Linux x86_64
	how=redirect
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" SSH_TTY=/dev/pts/0
	expect_status 0
	expect_fake_calls "argc=1 args=version;argc=0 args= stdin=0;"
	expect_out "Installed CC Babysitter 9.9.9"
	expect_clean
}

# 14. A Linux desktop is not set up as a service.
case_linux_desktop() {
	new_case linux-desktop
	shim_uname Linux x86_64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= WAYLAND_DISPLAY=wayland-0
	expect_status 0
	expect_fake_calls "argc=1 args=version;"
	expect_out "Start it with: $c/home/.local/bin/ccbabysitter"
	expect_no_out "stand-in service setup"
	expect_clean
}

# A Linux desktop whose background copy runs as the user service: a plain
# run replaces it with the new version, and opens no page.
case_linux_desktop_running() {
	new_case linux-desktop-running
	shim_uname Linux x86_64
	shim_systemctl 0 0
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= WAYLAND_DISPLAY=wayland-0
	expect_status 0
	expect_fake_calls "argc=1 args=version;argc=1 args=--no-open stdin=0;"
	expect_no_out "Start it with"
	expect_clean
}

# 15. A Mac reached over SSH is not set up as a service either.
case_mac_ssh() {
	new_case mac-ssh
	shim_uname Darwin arm64
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/fake" DISPLAY= SSH_CONNECTION="192.0.2.1 50000 192.0.2.2 22"
	expect_status 0
	expect_fake_calls "argc=1 args=version;"
	expect_out "Start it with:"
	expect_clean
}

# 16. The default download URL follows CCBABYSITTER_VERSION. A curl shim
# records the URL and fails, so nothing leaves this machine.
case_default_url() {
	# Numbered, since url-v0.4.0 and url-V0.4.0 are one folder on a file
	# system that ignores case.
	n=0
	for pair in ":latest/download" "latest:latest/download" "v0.4.0:download/v0.4.0" "V0.4.0:download/v0.4.0" "0.4.0:download/v0.4.0"; do
		version=${pair%%:*}
		want=${pair#*:}
		n=$((n + 1))
		new_case "url-$n-${version:-unset}"
		printf '#!/bin/sh\nprintf "%%s\\n" "$*" >>"%s"\nexit 22\n' "$c/curl.log" >"$c/shim/curl"
		chmod 755 "$c/shim/curl"
		if [ -n "$version" ]; then
			run_install CCBABYSITTER_VERSION="$version"
		else
			run_install
		fi
		expect_status 1
		if grep -F -q -- "--proto =https --tlsv1.2 -fsSL -o $c/tmp/" "$c/curl.log" &&
			grep -F -q -- " https://github.com/pejmanebrahimi/ccbabysitter/releases/$want/$host_asset" "$c/curl.log"; then
			ok
		else
			bad "curl was called as: $(cat "$c/curl.log")"
		fi
		expect_absent "$c/home/.local"
		expect_clean
	done
}

# 17. With no curl, wget is used; with no sha256sum, shasum is.
case_fallbacks() {
	new_case fallbacks
	cat >"$c/shim/wget" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >>"$c/wget.log"
[ "\$1" = -qO ] || exit 2
exec "$real_curl" -fsSL -o "\$2" "\$3"
EOF
	chmod 755 "$c/shim/wget"
	path="$c/shim:$work/fallback-bin"
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good"
	expect_status 0
	expect_installed "$c/home/.local/bin" "$want_version"
	if [ "$(wc -l <"$c/wget.log" | tr -d ' ')" = 2 ]; then ok; else bad "wget calls: $(cat "$c/wget.log")"; fi
	expect_clean

	# The default download, from GitHub, uses the latest release URL. A wget
	# shim records the call and fails, so nothing leaves this machine.
	new_case fallbacks-default-url
	printf '#!/bin/sh\nprintf "%%s\\n" "$*" >>"%s"\nexit 8\n' "$c/wget.log" >"$c/shim/wget"
	chmod 755 "$c/shim/wget"
	path="$c/shim:$work/fallback-bin"
	run_install
	expect_status 1
	if grep -F -q -- "-qO $c/tmp/" "$c/wget.log" &&
		grep -F -q -- " https://github.com/pejmanebrahimi/ccbabysitter/releases/latest/download/$host_asset" "$c/wget.log"; then
		ok
	else
		bad "wget was called as: $(cat "$c/wget.log")"
	fi
	expect_absent "$c/home/.local"
	expect_clean
}

# 18. A relative install folder is taken from the working folder.
case_relative_dir() {
	new_case relative-dir
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" CCBABYSITTER_INSTALL_DIR=rel/bin
	expect_status 0
	expect_installed "$c/run/rel/bin" "$want_version"
	expect_out "Installed $want_version to $(cd "$c/run" && pwd -P)/rel/bin/ccbabysitter"
	expect_clean
}

# 19. With no HOME and no install folder there is nowhere to install.
case_no_home() {
	new_case no-home
	run_install CCBABYSITTER_DOWNLOAD_URL="$url/good" HOME=
	expect_status 1
	expect_out "HOME is not set"
	expect_clean
}

# 20. A script cut short runs nothing: without its last line it only
# defines functions.
case_cut_short() {
	new_case cut-short
	lines=$(wc -l <"$script" | tr -d ' ')
	head -n $((lines - 1)) "$script" >"$c/cut.sh"
	(
		cd "$c/run" &&
			env -i HOME="$c/home" TMPDIR="$c/tmp" PATH="$path" CCBABYSITTER_DOWNLOAD_URL="$url/good" \
				"$shell_path" $shell_args "$c/cut.sh"
	) >"$c/out" 2>&1
	status=$?
	expect_status 0
	if [ -s "$c/out" ]; then bad "printed: $(cat "$c/out")"; else ok; fi
	expect_only "$c/home"
	expect_clean
}

# bin folders that leave a command out, for the fallback and opener cases.
make_bin_without() {
	bin="$1"
	shift
	mkdir -p "$bin"
	for d in /usr/bin /bin /usr/sbin /sbin; do
		[ -d "$d" ] || continue
		for f in "$d"/*; do
			n=${f##*/}
			skip=""
			for leave in "$@"; do
				[ "$n" = "$leave" ] && skip=1
			done
			[ -n "$skip" ] && continue
			[ -e "$bin/$n" ] || [ -L "$bin/$n" ] || ln -s "$f" "$bin/$n"
		done
	done
}

if command -v shasum >/dev/null 2>&1; then
	make_bin_without "$work/fallback-bin" curl wget sha256sum
	fallback_note="wget and shasum"
else
	make_bin_without "$work/fallback-bin" curl wget
	fallback_note="wget (no shasum on this machine)"
fi
make_bin_without "$work/no-opener-bin" xdg-open
make_bin_without "$work/no-systemctl-bin" systemctl

echo "Running install.sh with: $shell_path $shell_args"
for t in case_default_dir case_hint_bash case_hint_sh case_hint_fish case_hint_outside_home \
	case_on_path case_shadowed case_path_trailing_slash case_running case_replace case_mismatch case_missing_line case_star_format \
	case_download_fails case_unknown_os case_windows_shell case_unknown_arch case_arch_names \
	case_linux_no_display case_linux_ssh case_linux_ssh_tty case_linux_no_opener \
	case_linux_no_user_manager case_linux_no_systemctl case_linux_invocation_id \
	case_stdin_pipe case_stdin_pipe_service case_stdin_redirect \
	case_linux_desktop case_linux_desktop_running case_mac_ssh case_default_url case_fallbacks case_relative_dir \
	case_no_home case_cut_short; do
	check "$t"
done

echo "Fallback case used $fallback_note."
echo "$passed checks passed, $failed failed."
[ "$failed" = 0 ]
