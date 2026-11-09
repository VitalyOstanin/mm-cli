#!/usr/bin/env bash
# Prints the CHANGELOG.md section of a release, for use as the GitHub release
# text: tag v1.2.3 gives the body of "## [1.2.3]" without the heading.
#
#   scripts/release-notes.sh v1.2.3
#
# CHANGELOG.md is read from the repository root, or from the file named by
# MM_CLI_CHANGELOG. The section ends at the next heading or at the link
# definitions at the end of the file. Fails if the section is missing or has
# no text.
set -euo pipefail

if [[ $# -ne 1 || -z $1 ]]; then
	echo "usage: $0 <tag>  (for example v1.2.3)" >&2
	exit 2
fi

tag=$1
version=${tag#v}
changelog=${MM_CLI_CHANGELOG:-$(dirname "$0")/../CHANGELOG.md}

if [[ ! -r $changelog ]]; then
	echo "release-notes: $changelog not found" >&2
	exit 1
fi

if ! grep -qF "## [$version]" "$changelog"; then
	echo "release-notes: no section \"## [$version]\" in $changelog" >&2
	exit 1
fi

notes=$(awk -v heading="## [$version]" '
	found && /^## / { exit }
	found && /^\[[^]]+\]: / { exit }
	found { print }
	index($0, heading) == 1 { found = 1 }
' "$changelog")

# Trim leading and trailing blank lines.
notes=$(printf '%s\n' "$notes" | sed -e '/./,$!d' | sed -e ':a' -e '/^\n*$/{$d;N;ba' -e '}')

if [[ -z ${notes//[[:space:]]/} ]]; then
	echo "release-notes: section \"## [$version]\" in $changelog is empty" >&2
	exit 1
fi

printf '%s\n' "$notes"
