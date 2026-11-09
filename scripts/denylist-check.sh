#!/usr/bin/env bash
# Checks content against a private list of forbidden strings kept outside the
# repository (work hosts, names, channel names and similar). The list itself
# is never committed; without it the check warns and passes.
#
#   scripts/denylist-check.sh --staged          staged files (pre-commit hook)
#   scripts/denylist-check.sh --tree            tracked and untracked, not ignored
#   scripts/denylist-check.sh --message <file>  a commit message (commit-msg hook)
#
# Matches are reported by file name only, so the forbidden strings do not end
# up in logs.
set -euo pipefail

list=${MM_CLI_DENYLIST:-${XDG_CONFIG_HOME:-$HOME/.config}/mm-cli-dev/denylist}
if [[ ! -r $list ]]; then
	echo "denylist-check: $list not found, skipping (create it to enable the check)" >&2
	exit 0
fi
patterns=$(grep -v -e '^[[:space:]]*#' -e '^[[:space:]]*$' "$list" || true)
[[ -n $patterns ]] || exit 0

matches() { grep -q -i -F -f <(printf '%s\n' "$patterns"); }

mode=${1:---staged}
case $mode in
--message)
	if matches <"$2"; then
		echo "denylist-check: the commit message contains a forbidden string" >&2
		exit 1
	fi
	exit 0
	;;
--staged) files=$(git diff --cached --name-only --diff-filter=ACMR) ;;
--tree) files=$(git ls-files --cached --others --exclude-standard) ;;
*)
	echo "usage: $0 [--staged | --tree | --message <file>]" >&2
	exit 2
	;;
esac

found=0
while IFS= read -r f; do
	[[ -n $f ]] || continue
	if [[ $mode == --staged ]]; then
		git show ":$f" | matches && { echo "denylist-check: forbidden string in $f" >&2; found=1; }
	elif [[ -f $f ]]; then
		matches <"$f" && { echo "denylist-check: forbidden string in $f" >&2; found=1; }
	fi
done <<<"$files"
if printf '%s\n' "$files" | matches; then
	echo "denylist-check: forbidden string in a file name" >&2
	found=1
fi
exit $found
