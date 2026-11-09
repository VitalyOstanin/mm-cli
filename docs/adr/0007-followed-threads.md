# 0007. Followed threads and read state

- Status: Accepted
- Date: 2026-11-09

## Context

The Threads view of Mattermost (Collapsed Reply Threads) lists the threads a user follows, with unread replies and mentions. Agents read threads to answer them; if reading marked threads read, the user would lose the unread marks the app shows. The server has its own rules: following a thread updates its last-viewed time (`UpdateViewedTimestamp: state` in `App.UpdateThreadFollowForUser`), and a thread of a direct or group channel is returned for every team of the user.

## Decision

- `thread list`, `thread follow`, `thread unfollow` and `thread read` (with `--all`) cover the Threads view; `thread get` is not added, because `post thread` already shows a whole thread.
- Reading never marks a thread read: `thread list` and `post thread` leave unread counts alone. Only `thread read` marks threads read. That `thread follow` marks the thread read is the server's behaviour and is stated in the help.
- An id of any message of a thread acts on its root.
- Without a selected team, `thread list` merges the threads of all teams of the user and keeps a direct-channel thread once.
- Commands that act on several ids act one id at a time and print what they did before an error, as `post delete` does.

## Consequences

- An agent can read and answer threads without changing what the user sees as unread.
- `thread follow` cannot be used to follow a thread while keeping it unread.
- Listing all teams costs one request per team.
