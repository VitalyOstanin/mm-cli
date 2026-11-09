#!/usr/bin/env bash
# Regenerates docs/cli.md (or the file given as $1) by walking the cobra command
# tree through --help output; new commands are picked up automatically.
set -euo pipefail
cd "$(dirname "$0")/.."

bin=$(mktemp -d)/mm-cli
trap 'rm -rf "$(dirname "$bin")"' EXIT
go build -o "$bin" ./cmd/mm-cli

anchor() { tr ' ' '-' <<<"$1"; }

# subs <command path...>: names listed under "Available Commands" (help and
# completion excluded), in the order cobra prints them.
subs() {
	"$bin" "$@" --help | sed -n '/^Available Commands:/,/^$/p' |
		awk 'NR>1 && NF && $1!="help" && $1!="completion" {print $1}'
}

# Top-level commands come from the root --help: those with subcommands are
# groups, the rest are single commands.
groups=()
singles=()
for c in $(subs); do
	if [ -n "$(subs "$c")" ]; then groups+=("$c"); else singles+=("$c"); fi
done

# toc <indent> <path...>: one contents entry per command, recursively.
toc() {
	local indent=$1
	shift
	local c
	for c in $(subs "$@"); do
		echo "${indent}- [$* $c](#$(anchor "$* $c"))"
		toc "  $indent" "$@" "$c"
	done
}

# section <level> <path...>: heading, help text and subcommand sections.
section() {
	local level=$1
	shift
	echo
	echo "$level $*"
	echo
	echo '```'
	local kids
	kids=$(subs "$@")
	if [ -n "$kids" ]; then
		"$bin" "$@" --help # groups keep their Global Flags block
	else
		"$bin" "$@" --help | sed '/^Global Flags:/,$d'
	fi
	echo '```'
	local c
	for c in $kids; do
		section "$level#" "$@" "$c"
	done
}

{
	echo "# Command reference"
	echo
	echo "Generated from \`mm-cli <command> --help\` by \`scripts/gen-cli-doc.sh\`; do not edit by hand."
	echo
	echo "## Contents"
	echo
	echo "- [Global flags](#global-flags)"
	for g in "${groups[@]}"; do
		echo "- [$g](#$g)"
		toc "  " "$g"
	done
	for s in "${singles[@]}"; do echo "- [$s](#$s)"; done
	echo
	echo "## Global flags"
	echo
	echo '```'
	"$bin" --help | sed -n '/^Flags:/,$p'
	echo '```'
	for c in "${groups[@]}" "${singles[@]}"; do
		section "##" "$c"
	done
} >"${1:-docs/cli.md}"
echo "${1:-docs/cli.md} updated"
