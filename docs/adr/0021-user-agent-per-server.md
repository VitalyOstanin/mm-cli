# 0021. User-Agent is set per server

- Status: Accepted
- Date: 2026-11-10

## Context

Every request carried `User-Agent: mm-cli/<version>`, and the WebSocket of `watch` carried the Go default. A server, or a proxy in front of it, may treat clients by this header, and the user may need another value for one server without changing it for the others. The other per-server setting, `rate_limit`, already lives in the server's entry of `config.yaml`.

## Decision

- A server entry of `config.yaml` takes an optional `user_agent`. When set, it replaces `mm-cli/<version>` in every request to that server: API calls, the login, and the WebSocket of `watch`.
- Without it the header is `mm-cli/<version>`, the WebSocket included.
- There is no global key, flag or environment variable.
- The value must be printable ASCII; anything else makes `config.yaml` invalid (exit code 2), so that a value cannot split the request.
- `auth login` to a saved server keeps its `user_agent`, as it keeps `rate_limit`.

## Consequences

- The header can be changed for one server by editing `config.yaml`; there is no command for it.
- `watch` now sends `mm-cli/<version>` instead of the Go default.
