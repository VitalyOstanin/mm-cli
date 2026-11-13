# 0027. Reaction catch-up runs at most once per 10 minutes

- Status: Accepted
- Date: 2026-11-13

## Context

ADR 0024 makes `watch --reactions` fetch what it missed while disconnected. For reactions this meant searching your own messages of the last 7 days on every reconnect (up to 10 search requests at 1 request per second) to find reactions made in the gap. On flaky networks (VPN, sleep and wake) reconnects are frequent, so this multiplies the load on the server's search.

A reaction does update its message's update time on the server, but finding such messages would need a request per channel, which is worse. The bulk endpoint `POST /api/v4/posts/ids/reactions` was considered and rejected: Mattermost v11 removed it.

## Decision

- The search for reactions missed while disconnected runs at most once per 10 minutes.
- When a reconnect comes sooner, the reaction catch-up is postponed, not dropped: its starting point stays where it was, and the next catch-up (at the latest when the 10 minutes pass) covers the whole gap without duplicates.
- Message catch-up still runs on every reconnect.
- Live reactions over WebSocket are unaffected.

## Consequences

- At most 10 search requests per 10 minutes, however often the connection drops.
- A reaction made during a short disconnect can appear up to 10 minutes late.
- Works on every server version, with no dependence on endpoints that newer servers removed.
- ADR 0024 is refined as to the reaction catch-up frequency only; everything else in it stands.
