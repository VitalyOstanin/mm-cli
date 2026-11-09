# 0004. Rate limiter and no polling

- Status: Superseded by [0006](0006-rate-limit-state-between-runs.md)
- Date: 2026-11-09

## Context

mm-cli is used against shared company servers, including by agents that may run many commands in a row. Too many requests slow the server for everyone and can get the account rate limited or blocked.

## Decision

All HTTP requests pass through one transport per process that:

1. Limits the rate to 1 request per second with bursts of 3; a profile may set another rate.
2. On `429`, waits for `Retry-After` (5 seconds by default, at most 60) and retries at most 3 times, only for requests whose body can be replayed; then fails with exit code 6.
3. Adapts: each `429` halves the current rate (down to one request per 10 seconds) and sets the burst to 1; after 20 successful responses in a row the rate grows by half, up to the profile's rate. When the server sends `X-RateLimit-Remaining` ≤ 1, the next request waits until `X-RateLimit-Reset` (at most 60 seconds).
4. Retries network errors and `502`/`503`/`504` at most twice, only for GET and HEAD.

There is no polling anywhere. `watch` holds one WebSocket connection, filters `posted` events on the client and reconnects with a pause of 1, 2, 4 ... up to 60 seconds. Channel and user addresses are cached; author names are fetched in one request per page. Bulk operations run one request at a time.

## Consequences

- A burst of commands from an agent cannot flood the server.
- Commands that make many requests are slower by design.
- The adaptive state lives only in one process; a short command starts at the profile's rate.
