# 0019. Save and unsave are post actions

- Status: Accepted
- Date: 2026-11-10

## Context

mm-cli had a group `saved` with `add`, `remove` and `list`. The app's message menu has Save Message and Remove from Saved, its thread menu has Save and Unsave, and the list is Saved messages, across all channels. The API keeps them as flagged posts; `mmctl` has no saved messages. 0018 put pin and unpin under `post`.

## Decision

- `post save <post-id>` and `post unsave <post-id>` replace `saved add` and `saved remove`.
- `post saved` replaces `saved list`.
- The group `saved` is removed.

## Consequences

- Saving and pinning are both post actions, with the app's verbs.
- Scripts that call `saved add`, `saved remove` or `saved list` get exit code 2 (see 0010).
