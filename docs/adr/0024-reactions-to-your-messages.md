# 0024. Reactions: post reaction add, remove, list and watch --reactions

- Status: Accepted; reaction catch-up frequency refined by ADR 0027
- Date: 2026-11-13

## Context

In the Mattermost app a reaction belongs to a message: "Add Reaction" in the message menu, "click to remove" on your own reaction. The API names the same operations saveReaction, getReactions and deleteReaction; a reaction is the triple user, post, emoji and cannot be edited. The app does not notify about reactions and has no list of the reactions others put on your messages. The server sends a `reaction_added` WebSocket event to the members of the channel, and the post search returns posts with their reactions in `metadata.reactions`. mm-cli had no reaction commands, and `watch` printed only new messages, only to the terminal.

## Decision

- Reactions are a subgroup of `post`, next to the other message actions (ADR 0018, 0019), named as in the app and the API:
  - `post reaction add <post> <emoji>...` adds your reactions; `--replace <old>` changes one reaction for another, adding the new one before removing the old one, so a failure never leaves the message without your reaction;
  - `post reaction remove <post> <emoji>...` removes your reactions;
  - `post reaction list <post>` lists all reactions on one message;
  - `post reaction list [--since]` without a message lists the reactions other people put on messages you posted since the given time (default 7 days), oldest first. It finds your messages with one search across all your teams (`from:you after:date`) and takes the reactions from the search results, so the number of requests does not grow with the number of messages. A reaction made now on a message older than `--since` is not listed.
- Messages printed by any command carry their reactions: in JSON a `reactions` field grouped by emoji with the count and the users, in text one line of counts. The server already includes them, so there are no extra requests.
- `watch` watches messages, reactions to your messages, or both in one connection: no flags or `--messages` for messages, `--reactions` for reactions, both flags for both. A reaction by yourself is dropped at once unless `--include-own` is given (for checking notifications); for a reaction by someone else the post is fetched once to learn its author, and the answer is cached. After a reconnect `watch` fetches what it missed while disconnected.
- In `watch --json` every line has a `type` field, `post` or `reaction`, so one stream can carry both.
- A reaction is one record in `post reaction list` and `watch`: the post and its thread root, channel, team, who reacted, emoji, time, the full message, `permalink` (`https://`, as Copy Link in the app) and `app_link` (`mattermost://`, which opens the message in the desktop app).
- `--notify` shows a desktop notification for everything watched except your own messages, in addition to the output, and a click on it opens the message: on Linux through the freedesktop notification service over D-Bus and `xdg-open`; `terminal-notifier` on macOS, or `osascript` without opening when it is missing; a toast through PowerShell on Windows, with no extra modules. `--urgency low|normal|critical` sets the urgency on Linux, normal by default.
- Emoji are shown as pictures where a terminal or desktop can: text output prints a standard emoji as its Unicode character followed by its name (`👍 :+1:`, the name stays greppable), a custom one as `:name:`; a notification title uses the character, or for a custom emoji `:name:` with the emoji image (looked up and downloaded once per emoji, kept in the user cache directory, never failing the watch). JSON keeps the name only.

## Consequences

- Scripts and AI agents can add, change, remove and read reactions, and read reactions and messages from one JSON stream, loading the thread by `root_id`.
- One long-running `watch --reactions --notify` (for example a user service) replaces checking messages for reactions by hand.
- The JSON of messages gained a `reactions` field, and `watch --json` lines a `type` field.
- Each new message reacted to costs one request in `watch --reactions`; reactions in channels you have left are not sent by the server and are not watched.
