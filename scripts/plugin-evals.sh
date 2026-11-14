#!/bin/sh
# Runs the plugin's evals, in plugin/evals, against the fake ccbabysitter
# beside this script (plugin-evals-fake.sh), never a real one. Both live
# here rather than in the plugin folder, which ships only what the plugin
# itself needs. The runs' shell commands cannot read your home folder or
# /tmp, so the fake is copied to a temporary folder under /Users/Shared on
# macOS (else /var/tmp), put first on PATH as ccbabysitter, and every PATH
# folder that holds a real ccbabysitter is left out. Extra arguments go to
# claude plugin eval, for example: sh scripts/plugin-evals.sh --runs 1
set -eu
here=$(cd "$(dirname "$0")" && pwd)
# claude often sits in the same folder as ccbabysitter, so find it first.
claude=$(command -v claude)
parent=/var/tmp
[ -d /Users/Shared ] && parent=/Users/Shared
bin=$(mktemp -d "$parent/ccb-evals.XXXXXX")
trap 'rm -rf "$bin"' EXIT
cp "$here/plugin-evals-fake.sh" "$bin/ccbabysitter"
chmod +x "$bin/ccbabysitter"
path=$bin
old_ifs=$IFS
IFS=:
for dir in $PATH; do
	[ -e "$dir/ccbabysitter" ] || path=$path:$dir
done
IFS=$old_ifs
PATH=$path "$claude" plugin eval "$here/../plugin" --trust-plugin --ablation none \
	--allow-tools "Bash(ccbabysitter *)" "Bash(ccbabysitter)" "Bash(pmset -g batt)" "Bash(uname)" "Bash(uname *)" \
	"$@"
