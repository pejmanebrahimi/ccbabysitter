#!/bin/sh
# Installs CC Babysitter on macOS and Linux:
#
#   curl -fsSL https://ccbabysitter.dev/install.sh | sh
#
# It downloads the release binary for this machine, checks it against the
# release's checksums.txt and puts it in ~/.local/bin. It never uses sudo,
# and the script itself writes nothing outside that folder and a temporary
# folder it removes. On a server with no display it then runs the installed
# program, which sets itself up as a systemd user service.
#
# Settings, read from the environment:
#   CCBABYSITTER_VERSION       release tag to install, such as v0.4.0
#                              (default: the latest release)
#   CCBABYSITTER_INSTALL_DIR   folder to install into (default: ~/.local/bin)
#   CCBABYSITTER_DOWNLOAD_URL  base URL holding the release files
#                              (default: the GitHub release for the version)
#
# Everything is in functions and the last line calls main, so a download
# cut short runs nothing at all.

REPO_URL="https://github.com/pejmanebrahimi/ccbabysitter"
WINDOWS_COMMAND="irm https://ccbabysitter.dev/install.ps1 | iex"

say() {
	printf '%s\n' "$*"
}

die() {
	printf 'ccbabysitter install: %s\n' "$*" >&2
	exit 1
}

has() {
	command -v "$1" >/dev/null 2>&1
}

# tmp_dir and part_file are removed on exit, however the script ends.
tmp_dir=""
part_file=""

cleanup() {
	if [ -n "$part_file" ]; then
		rm -f "$part_file"
		part_file=""
	fi
	if [ -n "$tmp_dir" ]; then
		rm -rf "$tmp_dir"
		tmp_dir=""
	fi
}

detect_os() {
	os_name=$(uname -s 2>/dev/null)
	case "$os_name" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*)
		printf 'ccbabysitter install: there is no CC Babysitter build for %s here.\n' "${os_name:-this system}" >&2
		printf 'On Windows, run this in PowerShell instead:\n  %s\n' "$WINDOWS_COMMAND" >&2
		exit 1
		;;
	esac
}

detect_arch() {
	machine=$(uname -m 2>/dev/null)
	case "$machine" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "there is no CC Babysitter build for the ${machine:-unknown} processor. Builds exist for amd64 (x86_64) and arm64." ;;
	esac
	# A shell running under Rosetta on an Apple silicon Mac reports
	# x86_64; the native build is the better one to install there.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ]; then
		if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = 1 ]; then
			arch=arm64
		fi
	fi
}

# base_url is where the release files are: the given URL, or the GitHub
# release for the chosen version.
base_url() {
	if [ -n "${CCBABYSITTER_DOWNLOAD_URL:-}" ]; then
		printf '%s\n' "${CCBABYSITTER_DOWNLOAD_URL%/}"
		return
	fi
	version="${CCBABYSITTER_VERSION:-}"
	case "$version" in
	"" | latest)
		printf '%s/releases/latest/download\n' "$REPO_URL"
		;;
	[vV]*)
		printf '%s/releases/download/v%s\n' "$REPO_URL" "${version#?}"
		;;
	*)
		printf '%s/releases/download/v%s\n' "$REPO_URL" "$version"
		;;
	esac
}

# wget_https URL FILE follows the default GitHub download one redirect at
# a time. Every address is checked before wget is allowed to request it, so
# an HTTPS-to-HTTP redirect cannot carry either the binary or checksums over
# plaintext. Wget normally allows 20 redirects, so keep the same limit.
wget_https() {
	wget_url="$1"
	wget_file="$2"
	wget_redirects=0
	while :; do
		case "$wget_url" in
		https://*) ;;
		*) die "wget refused a non-HTTPS download address: $wget_url. Nothing was installed." ;;
		esac

		wget_response="$tmp_dir/wget-response"
		if wget --max-redirect=0 --server-response -O "$wget_file" "$wget_url" 2>"$wget_response"; then
			rm -f "$wget_response"
			return 0
		fi

		wget_next=$(awk 'tolower($1) == "location:" { sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit }' "$wget_response")
		rm -f "$wget_response"
		[ -n "$wget_next" ] || return 1

		case "$wget_next" in
		https://*) wget_url="$wget_next" ;;
		http://* | *://*) die "wget refused a non-HTTPS redirect to $wget_next. Nothing was installed." ;;
		//*) wget_url="https:$wget_next" ;;
		/*)
			wget_host=${wget_url#https://}
			wget_host=${wget_host%%/*}
			wget_url="https://$wget_host$wget_next"
			;;
		\?*)
			wget_base=${wget_url%%\#*}
			wget_base=${wget_base%%\?*}
			wget_url="$wget_base$wget_next"
			;;
		*)
			wget_base=${wget_url%%\#*}
			wget_base=${wget_base%%\?*}
			case "$wget_base" in
			https://*/*) wget_url="${wget_base%/*}/$wget_next" ;;
			*) wget_url="$wget_base/$wget_next" ;;
			esac
			;;
		esac

		wget_redirects=$((wget_redirects + 1))
		[ "$wget_redirects" -le 20 ] || return 1
	done
}

# download URL FILE
# curl is tried first, and for the default GitHub download it is held to
# HTTPS and TLS 1.2 or later. With wget, the default download follows each
# redirect through wget_https, which refuses any address outside HTTPS.
# With CCBABYSITTER_DOWNLOAD_URL set, neither pin applies and the address
# is used as given.
download() {
	if has curl; then
		if [ -z "${CCBABYSITTER_DOWNLOAD_URL:-}" ]; then
			curl --proto '=https' --tlsv1.2 -fsSL -o "$2" "$1"
		else
			curl -fsSL -o "$2" "$1"
		fi
	else
		if [ -z "${CCBABYSITTER_DOWNLOAD_URL:-}" ]; then
			wget_https "$1" "$2"
		else
			wget -qO "$2" "$1"
		fi
	fi
}

# sha256_of FILE prints the file's SHA-256 in lower case. main has made
# sure one of the two tools is there.
sha256_of() {
	if has sha256sum; then
		sum=$(sha256sum "$1") || return 1
	else
		sum=$(shasum -a 256 "$1") || return 1
	fi
	printf '%s\n' "${sum%% *}" | tr 'A-F' 'a-f'
}

# expected_sha256 SUMS NAME prints the checksum listed for NAME, in lower
# case, or nothing when the list has no line for it.
expected_sha256() {
	tr -d '\r' <"$1" | awk -v name="$2" '$2 == name || $2 == "*" name { print $1; exit }' | tr 'A-F' 'a-f'
}

# install_dir prints the folder to install into, as an absolute path with
# no trailing slash.
install_dir() {
	dir="${CCBABYSITTER_INSTALL_DIR:-}"
	if [ -z "$dir" ]; then
		[ -n "${HOME:-}" ] || die "HOME is not set, so set CCBABYSITTER_INSTALL_DIR to the folder to install into."
		dir="$HOME/.local/bin"
	fi
	case "$dir" in
	/*) ;;
	*) dir="$(pwd)/$dir" ;;
	esac
	while [ "$dir" != / ] && [ "${dir%/}" != "$dir" ]; do
		dir="${dir%/}"
	done
	printf '%s\n' "$dir"
}

on_path() {
	case ":${PATH:-}:" in
	*":$1:"* | *":$1/:"*) return 0 ;;
	esac
	return 1
}

# path_hint tells how to put DIR on PATH for the user's shell.
path_hint() {
	shown="$1"
	if [ -n "${HOME:-}" ]; then
		case "$1" in
		"$HOME"/*) shown='$HOME'"${1#"$HOME"}" ;;
		esac
	fi
	say ""
	say "$1 is not on your PATH."
	case "$(basename "${SHELL:-sh}")" in
	zsh)
		say "To add it, add this line to ~/.zshrc and open a new terminal:"
		say "  export PATH=\"$shown:\$PATH\""
		;;
	bash)
		say "To add it, add this line to ~/.bashrc and open a new terminal:"
		say "  export PATH=\"$shown:\$PATH\""
		;;
	fish)
		say "To add it, run this once in fish:"
		say "  fish_add_path \"$1\""
		;;
	*)
		say "To add it, add this line to ~/.profile and log in again:"
		say "  export PATH=\"$shown:\$PATH\""
		;;
	esac
}

# headless reports whether this is a Linux machine where a plain run of
# CC Babysitter sets it up as a server's service: inside SSH, with no
# display, or with no way to open a browser. It is the rule the program
# itself uses.
headless() {
	[ "$os" = linux ] || return 1
	if [ -n "${SSH_CONNECTION:-}" ] || [ -n "${SSH_TTY:-}" ]; then
		return 0
	fi
	if [ -z "${DISPLAY:-}" ] && [ -z "${WAYLAND_DISPLAY:-}" ]; then
		return 0
	fi
	has xdg-open || return 0
	return 1
}

# service_ready reports whether a plain run here would set CC Babysitter
# up as a service and give the terminal back: it was not started by
# systemd itself, and the user's systemd manager answers. Anywhere else,
# such as a container, a build step or a root shell with no user manager,
# a plain run serves in the foreground and would never return.
service_ready() {
	[ -z "${INVOCATION_ID:-}" ] || return 1
	has systemctl || return 1
	systemctl --user show-environment >/dev/null 2>&1
}

# knows_background reports whether the program just installed runs in the
# background when started plainly, which versions from 0.5 do and know
# the quit command. An older release, installed by this newer script, is
# left for the person to start.
knows_background() {
	"$dest" help quit >/dev/null 2>&1
}

# mac_agent_running reports whether CC Babysitter's LaunchAgent runs in this
# Mac's login, which a plain run then restarts on the version just
# installed. One that was quit, or never started, is left for the person to
# start; an earlier version's plist is taken care of at the next login.
mac_agent_running() {
	[ "$os" = darwin ] || return 1
	has launchctl || return 1
	launchctl print "gui/$(id -u)/com.ccbabysitter" 2>/dev/null | grep -q 'state = running'
}

already_running() {
	has pgrep || return 1
	pgrep -x -u "$(id -u)" ccbabysitter >/dev/null 2>&1
}

main() {
	set -u
	trap cleanup EXIT
	trap 'cleanup; exit 129' HUP
	trap 'cleanup; exit 130' INT
	trap 'cleanup; exit 143' TERM

	detect_os
	detect_arch
	asset="ccbabysitter-$os-$arch"
	base=$(base_url)
	dir=$(install_dir) || exit 1
	dest="$dir/ccbabysitter"
	has curl || has wget || die "this needs curl or wget to download CC Babysitter."
	has sha256sum || has shasum || die "this needs sha256sum or shasum to check the download."

	tmp_root="${TMPDIR:-/tmp}"
	tmp_dir=$(mktemp -d "${tmp_root%/}/ccbabysitter.XXXXXX") || die "could not make a temporary folder in $tmp_root"

	say "Downloading $asset from $base"
	download "$base/$asset" "$tmp_dir/$asset" || die "could not download $base/$asset"
	download "$base/checksums.txt" "$tmp_dir/checksums.txt" || die "could not download $base/checksums.txt"

	want=$(expected_sha256 "$tmp_dir/checksums.txt" "$asset")
	[ -n "$want" ] || die "checksums.txt has no line for $asset, so the download cannot be checked. Nothing was installed."
	got=$(sha256_of "$tmp_dir/$asset") || die "could not compute the checksum of the download."
	[ "$got" = "$want" ] || die "the download of $asset does not match its checksum (expected $want, got $got). Nothing was installed."

	mkdir -p "$dir" || die "could not create $dir"
	part_file=$(mktemp "$dir/.ccbabysitter.XXXXXX") || die "could not write to $dir"
	cp "$tmp_dir/$asset" "$part_file" || die "could not write to $dir"
	chmod 755 "$part_file" || die "could not make $part_file executable"
	mv -f "$part_file" "$dest" || die "could not install $dest"
	part_file=""

	version=$("$dest" version) || die "$dest was installed but does not run on this machine."
	say "Installed $version to $dest"

	run="ccbabysitter"
	if ! on_path "$dir"; then
		path_hint "$dir"
		run="$dest"
	else
		found=$(command -v ccbabysitter 2>/dev/null)
		if [ -n "$found" ] && ! [ "$found" -ef "$dest" ]; then
			say ""
			say "Another copy, $found, comes first on your PATH. Remove it, or start this one by its full path."
			run="$dest"
		fi
	fi

	if service_ready; then
		if headless; then
			# A plain run here sets CC Babysitter up as a user service, or
			# updates the one already set up, and says how to connect.
			say ""
			"$dest" </dev/null
			exit $?
		fi
		if systemctl --user is-active --quiet ccbabysitter >/dev/null 2>&1 && knows_background; then
			# A desktop's background copy: a plain run restarts it on the
			# new version and prints where its page is, without opening it
			# again.
			say ""
			"$dest" --no-open </dev/null
			exit $?
		fi
	fi

	if mac_agent_running && knows_background; then
		# A Mac's background copy, the LaunchAgent: a plain run restarts it
		# on the new version and prints where its page is, without opening
		# it again.
		say ""
		"$dest" --no-open </dev/null
		exit $?
	fi

	say ""
	say "Start it with: $run"
	if already_running; then
		say "CC Babysitter is already running. Quit it and start it again to use the new version."
	fi
}

main "$@"
