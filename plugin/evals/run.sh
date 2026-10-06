#!/bin/sh
# Runs the plugin's evals against the fake ccbabysitter, never a real one.
# The runs' shell commands cannot read your home folder or /tmp, so the
# fake is copied to a temporary folder under /Users/Shared on macOS (else
# /var/tmp), put first on PATH, and every PATH folder that holds a real
# ccbabysitter is left out. Extra arguments go to
# claude plugin eval, for example: sh plugin/evals/run.sh --runs 1
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# claude often sits in the same folder as ccbabysitter, so find it first.
claude=$(command -v claude)
parent=/var/tmp
[ -d /Users/Shared ] && parent=/Users/Shared
bin=$(mktemp -d "$parent/ccb-evals.XXXXXX")
trap 'rm -rf "$bin"' EXIT
cp "$here/fake/ccbabysitter" "$bin/ccbabysitter"
path=$bin
old_ifs=$IFS
IFS=:
for dir in $PATH; do
	[ -e "$dir/ccbabysitter" ] || path=$path:$dir
done
IFS=$old_ifs
PATH=$path "$claude" plugin eval "$here/.." --trust-plugin --ablation none \
	--allow-tools "Bash(ccbabysitter *)" "Bash(ccbabysitter)" "Bash(pmset -g batt)" "Bash(uname)" "Bash(uname *)" \
	"$@"
