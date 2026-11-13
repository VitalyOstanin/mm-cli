# 0028. In watch --json, "channel" is a channel address

- Status: Accepted
- Date: 2026-11-13

## Context

`watch --json` prints "post" lines and, since ADR 0024, "reaction" lines. Both had a "channel" field, but with different meanings: a "post" line carried the display name from the WebSocket event ("Town Square"), a "reaction" line carried the channel name ("town-square", "@bob" for a direct channel). A reader could not use the field the same way in both, and the display name cannot be passed back to mm-cli.

## Decision

- In both line types "channel" is the channel address as mm-cli accepts it (see `mm-cli help addressing`): the channel name for a public or private channel, "@<username>" for a direct channel (the channel id if the user name cannot be looked up), the channel id for a group message channel.
- Both line types also carry "channel_display_name", the name the Mattermost app shows ("Town Square"; "@bob" for a direct channel; for a group channel the list of names as the server gives it).
- Both line types carry "team", the team name of the channel ("" for direct and group channels). Reaction lines already had it; post lines get it.
- `post reaction list --json` uses the same "channel" address and also gains "channel_display_name"; its "team" keeps its meaning, the team of the link.
- Text output and desktop notifications do not change; for a group channel they show its display name, not the id.
- Backward compatibility with the earlier meaning of "channel" in post lines is not kept.

## Consequences

- An agent replies with `mm-cli send <team>:<channel>` or by `channel_id`, whichever line type it read.
- The display name is still available, in its own field.
- Post lines need the channel and team looked up, as reaction lines already do; the lookups are cached and respect the request rate limit.
- Scripts that read "channel" from post lines as a display name must switch to "channel_display_name".
