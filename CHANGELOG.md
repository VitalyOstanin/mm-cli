# Changelog

All notable changes are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). The JSON output format is part of the public interface: any change to it is listed here.

## [Unreleased]

### Added

- `thread list`, `thread follow`, `thread unfollow`, `thread read` (with `--all`): followed threads as in the Threads view, with unread replies and mentions. `thread list --json` prints objects with `id`, `team`, `channel_id`, `channel`, `channel_type`, `root`, `reply_count`, `unread_replies`, `unread_mentions` and `last_reply_at`; the action output gains a `team` field for `thread read --all`.
- Help topics for AI agents and scripts: `mm-cli help agents`, `addressing`, `output`, `exit-codes`, `login`, `config`, `load`. The `--help` of every command now describes its behaviour, side effects, `--json` output and exit codes, with examples; `docs/cli.md` includes the topics.
- Scheduled messages, as in Drafts > Scheduled of the Mattermost app: `send --schedule <when>` (and `post create`) with `tomorrow`, `monday`, `YYYY-MM-DD HH:MM` or RFC 3339 in the time zone of the Mattermost profile, `--repeat-weekly`; `scheduled list`, `scheduled edit`, `scheduled reschedule`, `scheduled send` (send now), `scheduled delete`. A scheduled message in `--json` output has `id`, `channel_id`, `channel`, `root_id`, `message`, `files`, `scheduled_at`, `repeat`, `error`, `create_at` and `update_at`.

### Fixed

- README: the `team` field of `thread read --all` output is the team name, not its id.

## [0.1.0] - 2026-11-09

### Added

- `auth login`: sign-in through the desktop-app SSO flow (SAML, GitLab, OpenID Connect, Google, Office 365), in one step from a terminal or in two steps with `--no-browser` and `--callback`; `--token-stdin` as a fallback. The pasted address may be `http://` as shown by browser developer tools, or a whole "Copy as cURL" command. `auth check`, `auth logout`.
- Profiles for several servers: `profile list`, `profile default`, `profile remove`.
- Messages: `send` / `post create` with up to 10 attachments, `messages` / `post list`, `post get`, `post thread`, `post search`, `post edit`, `post history`, `post restore`, `post delete`.
- Files: `file list`, `file download`, `file replace`, `file remove`.
- Channels: `channel create`, `get`, `list`, `edit`, `archive`, `unarchive`, `read`.
- `pin add|remove|list`, `saved add|remove|list`, `bookmark add|get|list|edit|delete`.
- `watch`: new messages over one WebSocket connection, with reconnects after 1, 2, 4 ... up to 60 seconds; `--json` prints one object per line.
- Integration tests against Mattermost 12.1 in a container (`scripts/integration.sh`) and in CI.
- Channel addresses `@me`, `@user`, `name`, `team/name` and ids, with a per-profile cache.
- `--json` output for every command; errors as JSON on stderr; exit codes 0–7.
- Gentle load on the server: 1 request per second with bursts of 3, `Retry-After` on `429` with an adaptive rate, retries only for reads.
