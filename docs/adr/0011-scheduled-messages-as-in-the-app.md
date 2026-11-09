# 0011. Scheduled messages as in the app

- Status: Accepted
- Date: 2026-11-09

## Context

Mattermost schedules messages from the Send button and lists them in Drafts, on the Scheduled tab, with the actions Edit scheduled post, Reschedule post, Send now, Copy text and Delete scheduled post. The quick choices are 9:00 tomorrow and 9:00 on the next Monday in the user's time zone; a custom time can repeat weekly. The API has create, list (per team), update and delete, but no "send now": the web app creates a post from the scheduled one and then deletes it, and does not offer it for weekly messages or for failed ones, except failures `unknown` and `unable_to_send`.

## Decision

- `send` (and `post create`) take `--schedule <when>` and `--repeat-weekly`; the group `scheduled` has `list`, `edit`, `reschedule`, `send` and `delete`, following the app's actions. Copy text is the text in `scheduled list --json`.
- `<when>` is `tomorrow` or `monday` (9:00, like the app's quick choices; `next-monday` means the same), `YYYY-MM-DD HH:MM`, or RFC 3339. The time zone is the one in the user's Mattermost profile, else the machine's.
- `scheduled send` does what the web app does: create the post, then delete the scheduled one, with the same restrictions.
- A server without scheduled messages gives exit code 5 with a message saying so.

## Consequences

- What a user sees in the app and what mm-cli does match, with the same names.
- Finding a scheduled message by id lists the user's scheduled messages, since the API has no request for one message.
- If "send now" fails after the post is created, the scheduled message stays and could be sent again by the server; this matches the web app.
