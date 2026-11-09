# 0020. Marking as read is mark-read

- Status: Accepted
- Date: 2026-11-10

## Context

mm-cli marked a channel read with `channel read` and threads with `thread read [--all]`. The app calls the actions Mark as Read (channel menu), Mark as read (thread menu) and Mark all as read (all threads). Next to `messages` and `post thread`, the name `read` can be taken for reading messages, while the commands only clear the unread state.

## Decision

- `channel mark-read <channel>` replaces `channel read`.
- `thread mark-read <post-id>... | --all` replaces `thread read`.
- JSON output keeps the action `"marked_read"`.

## Consequences

- The command names repeat the app's menu items and no longer suggest reading messages.
- Scripts that call `channel read` or `thread read` get exit code 2 (see 0010).
