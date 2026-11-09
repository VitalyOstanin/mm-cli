# 0009. Names follow the Mattermost ecosystem

- Status: Accepted
- Date: 2026-11-09

## Context

Users and agents know Mattermost from its app, its API and `mmctl`. Names of mm-cli's own making make them translate. mm-cli called its saved server connections "profiles", while in Mattermost a profile is the user's profile (Profile Settings: Full Name, Username, Nickname, Position, Email, Profile Picture), and `mmctl` keeps connections under `auth` (`auth login`, `list`, `current`, `set`, `delete`, `renew`, `clean`).

## Decision

- Commands, flags and terms are named as in the Mattermost ecosystem: labels of the app (`webapp/channels/src/i18n/en.json`), the API and `mmctl`. Before a name is chosen, it is looked up there.
- A name already taken inside mm-cli is not a reason to depart from the ecosystem; the mm-cli name changes instead.
- Saved connections move to `auth list`, `auth current`, `auth set` and `auth delete`, replacing `profile list`, `profile default` and `profile remove`.
- `profile` is the user's profile on the server; `status` is the user's status (Online, Away, Do not disturb, Offline) and custom status.
- Existing commands are checked against the ecosystem names in the same way.

## Consequences

- People and agents who know Mattermost find commands under familiar names.
- Existing names change; see 0010 for compatibility.
