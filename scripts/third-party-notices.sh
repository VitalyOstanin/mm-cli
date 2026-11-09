#!/usr/bin/env bash
# Generates THIRD_PARTY_NOTICES: the license texts of every module that ends up
# in a release binary (union over all release targets), and fails when a
# dependency uses a license outside the allowed list.
#
# Usage: scripts/third-party-notices.sh [output-file]   (default: THIRD_PARTY_NOTICES)
# The output file is generated, not committed.
set -euo pipefail

GO_LICENSES="github.com/google/go-licenses/v2@v2.0.1"
ALLOWED="MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0"
# Keep in sync with goos/goarch in .goreleaser.yaml.
TARGETS=(linux/amd64 darwin/amd64 darwin/arm64 windows/amd64)
PKG=./cmd/mm-cli

cd "$(dirname "$0")/.."
out="${1:-THIRD_PARTY_NOTICES}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Build the tool for the host once: GOOS/GOARCH below are for the analysed
# targets and must not apply to the tool itself.
GOBIN="$work/bin" go install "$GO_LICENSES"
lic="$work/bin/go-licenses"

for target in "${TARGETS[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  echo "license check: ${goos}/${goarch}" >&2
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
	"$lic" check "$PKG" --allowed_licenses="$ALLOWED"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
	"$lic" report "$PKG" 2>/dev/null >>"$work/report.csv"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
	"$lic" save "$PKG" --save_path="$work/save-${goos}-${goarch}" --force
done

# Union of all targets: one entry per library, "name,url,license".
sort -u "$work/report.csv" >"$work/libs.csv"

{
  echo "Third-party notices for mm-cli"
  echo
  echo "mm-cli is distributed with the following third-party libraries."
  echo "Each section gives the library, its license and the license text."
  while IFS=, read -r name _url license; do
	# The project's own license ships as LICENSE next to this file.
	[ "$name" = "github.com/VitalyOstanin/mm-cli" ] && continue
	echo
	echo "================================================================================"
	echo "${name} (${license})"
	echo "================================================================================"
	echo
	found=0
	for dir in "$work"/save-*; do
	  [ -d "$dir/$name" ] || continue
	  for f in "$dir/$name"/LICEN[CS]E* "$dir/$name"/COPYING* "$dir/$name"/NOTICE*; do
		[ -f "$f" ] || continue
		cat "$f"
		echo
		found=1
	  done
	  break
	done
	if [ "$found" -eq 0 ]; then
	  echo "ERROR: no license file saved for ${name}" >&2
	  exit 1
	fi
  done <"$work/libs.csv"
} >"$out"

echo "wrote $out ($(wc -l <"$out") lines, $(wc -l <"$work/libs.csv") libraries)" >&2
cut -d, -f3 "$work/libs.csv" | sort | uniq -c >&2
