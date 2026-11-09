# 0012. CI must pass at release, not on every commit

- Status: Accepted
- Date: 2026-11-09

## Context

mm-cli has one maintainer and no outside contributors yet. Required status checks on `master` would block small fixes for flaky runners, while a broken release would reach users through GitHub releases and the Go module proxy.

## Decision

- `master` has no required status checks; CI still runs on pushes and pull requests.
- The release workflow calls the whole CI workflow (`workflow_call`), and the job that builds and publishes the release needs it to pass.

## Consequences

- A red run on `master` is allowed between releases, and is fixed before tagging.
- A tag cannot publish a release whose code fails CI.
