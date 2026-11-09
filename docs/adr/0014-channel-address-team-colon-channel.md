# 0014. A channel in another team is team:channel

- Status: Accepted
- Date: 2026-11-10

## Context

mm-cli addressed a channel in another team as `team/channel`. `mmctl` writes it as `team:channel` (`channelArgSeparator = ":"` in `server/cmd/mmctl/commands/channelargs.go`); the Mattermost apps have no text form of a channel address, only the URL `/team/channels/channel`. Channel names and usernames never contain ':'.

## Decision

- A channel in another team is `team:channel`, the only accepted form.
- Channel labels in text and JSON output use the same form (`myteam:town-square`).
- An argument in the old form `team/channel` is a usage error (exit code 2) whose message names the new form.

## Consequences

- An address written for `mmctl` works in mm-cli, and the other way round.
- Scripts that pass `team/channel` stop working (see 0010) and are told how to fix the address.
