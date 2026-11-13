# Changelog

All notable changes are recorded here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/). The JSON output format is part of the public interface: any change to it is listed here.

## [Unreleased]

## [Unreleased]

### Security

- The session token is kept in the OS secret store (ADR 0025): Keychain on macOS, Credential Manager on Windows, the Secret Service (GNOME Keyring, KWallet, KeePassXC) on Linux and BSD. A token file of an earlier version is moved into the store on its first read, and deleted only after the stored copy is read back; text mode says so in one line on stderr. When the store is unavailable, commands fail with exit code 1 and say how to fix it, never falling back to a file silently; `token_storage: file` in `config.yaml` or `MM_TOKEN_STORAGE=file` keeps tokens in `sessions/<name>.token` as before (for containers and hosts without a secret store). When a token file cannot be removed after the move, a warning (`mm-cli: warning: server <name>: ...`) is printed and the command goes on. After switching `token_storage` back from `file`, a token file written meanwhile replaces the older store entry on its first read. `auth delete` removes the token before it rewrites `config.yaml`.
- The secret-store entry is named after the saved server and its address (`chat https://chat.example.com`, ADR 0026), not the name alone: two configurations with the same server name and different addresses no longer share or overwrite a token, and a token is never offered to another address.

## [0.3.1] - 2026-11-12

### Fixed

- `send --schedule` and `post create --schedule` with attachments now send the file details together with the file ids, as the Mattermost app does, so an open app shows the attachments in Drafts > Scheduled at once instead of after a reload.

## [0.3.0] - 2026-11-11

### Added

- `file search <query>`: search attachments in the team, as in the Files tab of the app's search, with the Mattermost search syntax and `--in`, `--from`, `--ext`, `-n`. Text and `--json` output are those of `file list`.

### Changed

- The `--help` of `post search`, `post saved`, `auth list`, `auth logout`, `channel mark-read`, `channel pinned`, `file list`, `profile picture reset`, `status custom clear` and `thread list` now also lists the `--json` output and exit codes, as every other command does.

### Security

- Built with Go 1.26.11, which fixes [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859) in `net/http`; building from source needs Go 1.26.11 or later.

## [0.2.0] - 2026-11-10

Upgrading from 0.1: this release renames commands, flags, JSON fields and `config.yaml` keys (see Changed), so scripts written for 0.1 need updating. In `config.yaml` rename `profiles` to `servers` and `default` to `current`; to go back to 0.1, rename them back.

### Added

- `thread list`, `thread follow`, `thread unfollow`, `thread mark-read` (with `--all`): followed threads as in the Threads view, with unread replies and mentions. `thread list --json` prints objects with `id`, `team`, `channel_id`, `channel`, `channel_type`, `root`, `reply_count`, `unread_replies`, `unread_mentions` and `last_reply_at`; the action output gains a `team` field for `thread mark-read --all`.
- Help topics for AI agents and scripts: `mm-cli help agents`, `addressing`, `output`, `exit-codes`, `login`, `config`, `load`. The `--help` of every command now describes its behaviour, side effects, `--json` output and exit codes, with examples; `docs/cli.md` includes the topics.
- Scheduled messages, as in Drafts > Scheduled of the Mattermost app: `send --schedule <when>` (and `post create`) with `tomorrow`, `monday`, `YYYY-MM-DD HH:MM` or RFC 3339 in the time zone of the Mattermost profile, `--repeat-weekly`; `scheduled list`, `scheduled edit`, `scheduled reschedule`, `scheduled send` (send now), `scheduled delete`. A scheduled message in `--json` output has `id`, `channel_id`, `channel`, `root_id`, `message`, `files`, `scheduled_at`, `repeat`, `error`, `create_at` and `update_at`.
- `profile get`, `profile edit`, `profile picture set|reset`: the user's profile on the server (username, first and last name, nickname, position, email, time zone, picture), as in Profile Settings of the Mattermost app.
- `status get`, `status set online|away|dnd|offline` (with `--until` for Do not disturb), `status custom set|clear`: status and custom status, as in the app's status menu.
- `auth handler install` and `auth handler remove`: register mm-cli for the `mattermost-dev://` link the sign-in page opens (Linux, Windows, macOS), so that `auth login` finishes without pasting the address, also when the browser is already signed in to Mattermost. `auth login --callback <link>` without a server name finishes the login the link belongs to. The client token of a login now starts with `dev-`. With `--json` the handler commands print `{"action": "handler_installed"}` or `{"action": "handler_removed"}` with a `path` field.
- `user_agent` in a server entry of `config.yaml` replaces the `User-Agent` header `mm-cli/<version>` in every request to that server. The WebSocket of `watch` now sends the same header instead of the Go default.

### Changed

- `scheduled edit`, `scheduled reschedule` and `send --schedule` with `--json` look up only the channel of the message, instead of listing all of the user's channels.
- `auth delete` in text mode reminds on stderr that the session on the server stays valid until it expires and names `auth logout --server <name>`, when a session was saved. `--json` output is unchanged.
- Saved server connections moved under `auth`, named as in `mmctl auth`: `profile list` is now `auth list`, `profile default` is `auth current` (show) and `auth set <name>` (choose), `profile remove` is `auth delete`. The JSON field `default` of the list is now `current`; `auth set` prints `{"action": "current_set"}`, `auth delete` prints `{"action": "deleted"}`. The name `profile` now means the user's profile on the server.
- A saved connection is now a server, as in the Mattermost desktop and mobile apps and `mmctl`: `--profile` is `--server`, `$MM_PROFILE` is `$MM_SERVER`, `auth login` names the server with `--name`; in `config.yaml` the keys `profiles` and `default` are `servers` and `current`. A `config.yaml` with the old keys is refused with exit code 2 until the two keys are renamed. The JSON field `profile` of `auth login`, `auth check`, `auth logout`, `auth set` and `auth delete` is now `server`.
- A channel in another team is written `team:channel`, as in `mmctl`; `team/channel` is refused with exit code 2 and a hint. Channel labels in text and JSON output (`thread list`, `scheduled list`, `channel list`, `channel create`) use the same form.
- `--yes` and `-y` are now `--confirm`, as in `mmctl`.
- `send` and `post create`: `--reply` is now `-r, --reply-to`, as in `mmctl post create`.
- `-n, --limit` is now `-n, --number` in every list, as in `mmctl post list`.
- `bookmark add` and `bookmark edit`: `--name` and `--url` are now `--display-name` and `--link-url`, and the bookmark fields `name` and `url` of the JSON output are now `display_name` and `link_url`, as in the API.
- `pin add` and `pin remove` are now `post pin` and `post unpin`; `pin list` is `channel pinned`, as the app's Pin to Channel and Pinned messages.
- `saved add`, `saved remove` and `saved list` are now `post save`, `post unsave` and `post saved`, as the app's Save, Unsave and Saved messages.
- `channel read` is now `channel mark-read`, as the app's Mark as Read.

### Fixed

- README: the `team` field of `thread mark-read --all` output is the team name, not its id.
- An unknown subcommand of a command group (`mm-cli channel nosuch`) and an unknown help topic (`mm-cli help nosuch`) now fail with exit code 2 and a "did you mean" hint, instead of printing the help with exit code 0. The usage line of a group no longer suggests that the group runs by itself.
- `auth login <url>` names a server with an internationalized domain by its ASCII form (`https://чат.example.com` is `xn--80a0bn-example-com`), and a URL that gives no name fails with exit code 2 and asks for `--name`.
- `send --schedule` and `post create --schedule` check the time before uploading files, so a past time leaves no files on the server.
- The time zone of the Mattermost profile is used even on systems without a time zone database (Windows, minimal containers): the binary carries its own. A profile zone that still does not load is an error, instead of the local zone being used silently.
- `status custom set --clear-after` with a past time says that the time is not in the future, instead of reporting a wrong format.
- `thread follow`, `thread unfollow` and `thread mark-read` with `--json` print the threads already changed before an error, as `post delete` does; `thread mark-read --all --json` prints `[]` when there is nothing to mark.
- `scheduled list` for a user in no team fails with exit code 5 and says why: the server lists scheduled messages only through a team.
- With no saved server, every command says to log in (exit code 3), also when `config.yaml` still names a current server.
- `status get` looks up all the users given in one request; an unknown user is exit code 5.
- `auth delete` reports a login in progress that it could not remove, and removes the other files of the server anyway.
- A channel address of the old form `team/channel` fails with exit code 2 and the `team:channel` hint also when no server is saved: commands that take a channel check its form before opening the session.
- `file download` tells apart attachment names that differ only in Unicode normalization (`é` composed or decomposed), which macOS file systems treat as one name, so one no longer overwrites the other.
- The message for a `config.yaml` of mm-cli 0.1 no longer suggests just logging in again, which reads that same file: rename the two keys, or delete the file and log in again.

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

[Unreleased]: https://github.com/VitalyOstanin/mm-cli/compare/v0.3.1...HEAD
[0.3.1]: https://github.com/VitalyOstanin/mm-cli/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/VitalyOstanin/mm-cli/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/VitalyOstanin/mm-cli/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/VitalyOstanin/mm-cli/releases/tag/v0.1.0
