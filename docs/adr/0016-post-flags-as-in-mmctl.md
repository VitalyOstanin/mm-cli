# 0016. Reply and list-size flags as in mmctl

- Status: Accepted
- Date: 2026-11-10

## Context

mm-cli replied in a thread with `--reply <post-id>` and limited lists with `-n, --limit`. `mmctl post create` has `-r, --reply-to` ("Post id to reply to"), and `mmctl post list` has `-n, --number`. Other `mmctl` lists page with `--page` and `--per-page`, which mm-cli lists do not do.

## Decision

- `send` and `post create` take `-r, --reply-to <post-id>`; `--reply` is removed.
- Every mm-cli list takes `-n, --number`: `messages` and `post list`, `post search`, `file list`, `saved list`, `thread list`; `--limit` is removed.

## Consequences

- The flags of `mmctl post create` and `post list` work the same in mm-cli.
- Scripts that pass `--reply` or `--limit` get exit code 2 (see 0010); `-n` keeps working.
