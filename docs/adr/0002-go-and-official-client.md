# 0002. Go and the official API client

- Status: Accepted
- Date: 2026-11-09

## Context

mm-cli has to run on Linux, macOS and Windows as one file without a runtime, and to cover most of the Mattermost REST API: posts, files, channels, preferences, bookmarks, the WebSocket.

## Decision

mm-cli is written in Go and uses `Client4` and the models from `github.com/mattermost/mattermost/server/public`, the client the Mattermost server and its tools use themselves. Commands are built with `cobra`. HTTP goes through mm-cli's own `http.RoundTripper`, which adds rate limiting and retries (see [0004](0004-rate-limit-no-polling.md)).

## Consequences

- Static binaries for every target from one `go build`; releases are made with GoReleaser.
- API types and endpoints follow the server; new API features arrive by updating the module.
- The module is large and pulls in many indirect dependencies; `govulncheck` runs in CI.
- `Client4` reports errors as `AppError`; mm-cli maps them to exit codes by HTTP status.
