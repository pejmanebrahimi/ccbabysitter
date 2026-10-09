#!/bin/sh
# Stamps the assembled website so each deploy's pages ask for their own
# stylesheets, scripts and docs screenshots.
#
#   scripts/stamp-site.sh DIR STAMP
#
# Every href or src in DIR's pages that names a stylesheet or script under
# /assets/, or a picture under /docs/shots/, gets ?v=STAMP added. GitHub
# Pages lets a browser keep a file for ten minutes, so without the stamp a
# visitor could see a new page with the stylesheet the last deploy served.
# The deploy passes the commit it deploys. STAMP is letters and digits
# only. An address that already has a query is left as it is, so a second
# run changes nothing.
set -eu

dir=${1:-}
stamp=${2:-}
if [ -z "$dir" ] || [ ! -d "$dir" ]; then
	echo "stamp-site.sh: no such folder: $dir" >&2
	exit 2
fi
case "$stamp" in
'' | *[!A-Za-z0-9]*)
	echo "stamp-site.sh: the stamp must be letters and digits, not \"$stamp\"" >&2
	exit 2
	;;
esac

pages=$(find "$dir" -type f -name '*.html')
if [ -z "$pages" ]; then
	echo "stamp-site.sh: no pages in $dir" >&2
	exit 1
fi
printf '%s\n' "$pages" | while IFS= read -r page; do
	sed -E 's#(href|src)="(/assets/[^"?]+\.(css|js)|/docs/shots/[^"?]+\.webp)"#\1="\2?v='"$stamp"'"#g' "$page" >"$page.stamped"
	mv "$page.stamped" "$page"
done
