# 0006. Rate-limit state shared between runs, and no polling

- Status: Accepted
- Date: 2026-11-09
- Supersedes: [0004](0004-rate-limit-no-polling.md)

## Context

ADR 0004 put a rate limiter into each process. Agents often run mm-cli as a series of short commands, and each new process started at the full rate with a full burst: whatever the server had said with `429`, `Retry-After` or `X-RateLimit-Reset` was lost on exit. After three failed retries one command exited with code 6, and the next one sent up to three requests at once into the same ban. A `Retry-After` longer than 60 seconds was cut to 60, so the retry went out before the server allowed it. Retry pauses grew linearly without jitter, permanent errors such as an untrusted certificate were retried, and the two-minute timeout of the whole HTTP call could end a wait the server asked for with a network error (code 7) instead of code 6.

## Decision

All HTTP requests pass through one transport per process that:

1. Limits the rate to 1 request per second with bursts of 3; a profile may set another rate.
2. On `429`, bans requests until the time given by `Retry-After` (5 seconds by default, at most one hour) or `X-RateLimit-Reset`, halves the current rate (down to one request per 10 seconds) and sets the burst to 1. It retries at most 3 times, only for requests whose body can be replayed; then fails with exit code 6.
3. Waits in the process for at most 2 minutes. A longer ban is never shortened: the command fails at once with exit code 6 and says until when the server refuses requests.
4. Shares this state between runs of one profile through `ratelimit.json` in the profile's cache directory: the end of the ban, and the reduced rate with the time it was cut. The file is written only when the state changes (a `429`, or a refusal to wait) and removed when the rate is back to full; it is written through a temporary file and a rename, without locks. When two states meet, the later ban and the slower rate win. A new run reads the file once at start: during a ban it waits, printing `mm-cli: rate limited by the server, waiting Ns` to stderr, or fails with code 6 if the wait is longer than 2 minutes. A reduced rate grows back by half every 30 seconds since it was cut. A damaged or foreign file is ignored.
5. Recovers inside a run as before: after 20 successful responses in a row the rate grows by half, up to the profile's rate.
6. Retries network errors and `502`/`503`/`504` at most twice, only for GET and HEAD, after 1 s, 2 s, 4 s ... (at most 30 s) plus up to half of that at random. Errors a retry cannot fix are not retried: a certificate that does not verify, TLS spoken to a plain-HTTP port, a host name that does not exist.
7. Bounds each attempt by 2 minutes until the response headers arrive. There is no timeout for the whole call, so waits for the server and long downloads are not cut off.

There is no polling anywhere. `watch` holds one WebSocket connection, filters `posted` events on the client and reconnects with a pause of 1, 2, 4 ... up to 60 seconds. Channel and user addresses are cached; author names are fetched in one request per page. Bulk operations run one request at a time.

## Consequences

- A series of commands from an agent honours a ban the server announced to any of them, instead of sending a new burst from each process.
- A long ban ends commands at once with code 6 and the time to try again, instead of retrying early and possibly extending the ban.
- Concurrent runs do not share one token bucket: without locks each process keeps its own limiter, and simultaneous writes may lose one update, which costs at most one more `429`.
- Commands that make many requests are slower by design.
- A user who wants to retry before the ban ends can delete `ratelimit.json` from the profile's cache directory.
