# 0018. Pin and unpin are post actions, pinned messages a channel view

- Status: Accepted
- Date: 2026-11-10

## Context

mm-cli had a group `pin` with `add`, `remove` and `list`. In the app, Pin to Channel and Unpin from Channel are items of a message's menu, and Pinned messages opens from the channel header. The API has `POST /posts/{id}/pin`, `POST /posts/{id}/unpin` and `GET /channels/{id}/pinned`. `mmctl` has no pins.

## Decision

- `post pin <post-id>` and `post unpin <post-id>` replace `pin add` and `pin remove`.
- `channel pinned <channel>` replaces `pin list`.
- The group `pin` is removed.

## Consequences

- The verbs are those of the app and the API, and the commands sit where the actions are in the app.
- Scripts that call `pin add`, `pin remove` or `pin list` get exit code 2 (see 0010).
