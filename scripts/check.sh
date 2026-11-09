#!/usr/bin/env bash
# Runs the same checks as CI.
#
#   scripts/check.sh          everything: format, vet, lint, shellcheck, tests, vulnerabilities, denylist
#   scripts/check.sh --lint   format, vet, lint, shellcheck and denylist only (quick)
#   scripts/check.sh --fix    fix formatting and autofixable lint findings, then --lint
set -euo pipefail
cd "$(dirname "$0")/.."

lint=(go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0)
mode=${1:-all}

step() { printf '==> %s\n' "$*"; }

if [[ $mode == --fix ]]; then
	step "gofmt -w"
	gofmt -w .
	step "golangci-lint --fix"
	"${lint[@]}" run --fix ./...
	mode=--lint
fi

step "gofmt"
unformatted=$(gofmt -l .)
if [[ -n $unformatted ]]; then
	printf 'not formatted (run scripts/check.sh --fix):\n%s\n' "$unformatted"
	exit 1
fi
step "go vet"
go vet ./...
step "golangci-lint"
"${lint[@]}" run ./...
if command -v shellcheck >/dev/null; then
	step "shellcheck"
	shellcheck scripts/*.sh .githooks/*
else
	step "shellcheck not installed, skipped"
fi
step "denylist"
scripts/denylist-check.sh --tree

if [[ $mode == all ]]; then
	step "go test -race"
	go test -race -count=1 -timeout 5m ./...
	step "govulncheck"
	go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
fi
step "ok"
