# mm-cli

Mattermost from the command line: read, send, edit and delete messages and files in any channel, including your own direct channel; create and change channels; sign in through the same SSO flow as the desktop app.

[Русская версия](README.ru.md) · [Command reference](docs/cli.md) · [Decisions](docs/adr/README.md) · [Changelog](CHANGELOG.md)

## Contents

- [Features](#features)
- [Install](#install)
- [Sign in](#sign-in)
- [Addressing channels](#addressing-channels)
- [Examples](#examples)
- [Output](#output)
- [Exit codes](#exit-codes)
- [Load on the server](#load-on-the-server)
- [Configuration](#configuration)
- [Development](#development)
- [License](#license)

## Features

- Messages: send with attachments, list, read threads, search, edit, view and restore earlier versions, delete.
- Files: list a channel's attachments, download, replace or remove an attachment of your message.
- Channels: create, show, list (with unread counts), edit, archive, mark as read.
- Followed threads (the Threads view): list with unread counts, follow, unfollow, mark read.
- Scheduled messages (Drafts > Scheduled): schedule a message for tomorrow, Monday or any time, optionally every week; list, edit, reschedule, send now, delete (the server needs scheduled messages turned on and a license; otherwise the commands exit with code 5).
- Pins, saved messages and channel bookmarks (bookmarks need a server that has them turned on; otherwise the commands exit with code 5).
- `watch`: new messages as they arrive, over one WebSocket connection.
- Sign-in through SSO (SAML, GitLab, OpenID Connect, Google, Office 365) with the desktop-app flow, so no personal access token is needed; two-factor authentication happens in the browser as usual. A token from stdin works too.
- Text output for people, `--json` for scripts and agents.
- Several servers side by side as profiles.

## Install

With Go 1.26.10 or later:

```sh
go install github.com/VitalyOstanin/mm-cli/cmd/mm-cli@latest
```

Or download a binary for Linux or Windows (amd64) or macOS (Apple silicon or Intel) from the [releases page](https://github.com/VitalyOstanin/mm-cli/releases) and check it against `checksums.txt`.

The macOS binaries are not signed or notarized, so Gatekeeper blocks the first start of a downloaded file. After checking the checksum, remove the quarantine attribute:

```sh
xattr -d com.apple.quarantine mm-cli
```

## Sign in

```sh
mm-cli auth login https://chat.example.com
```

mm-cli opens the server's sign-in page in your browser. After you sign in (including any second factor), the browser lands on a page whose address starts with `https://chat.example.com/login/desktop?client_token=...`. Copy that address and paste it into the terminal within 3 minutes.

- Ignore the page's offer to open the desktop app: the login has to finish in mm-cli.
- If the browser was already signed in to Mattermost, it moves on to a channel at once. Take the address from the browser history (search for `login/desktop`), or sign in from a private window.

Without a terminal (scripts, agents) the login runs in two steps:

```sh
mm-cli auth login https://chat.example.com --no-browser
# open the printed address, sign in, copy the /login/desktop address, then
# pass it on stdin within 3 minutes:
mm-cli auth login --profile chat-example-com --callback - < address.txt
```

The first step exits with code 0 without logging in. With `--json` it prints `{"status": "pending", "profile": "...", "url": "...", "method": "saml", "login_url": "...", "expires_in": 180, "next": "mm-cli auth login --profile ... --callback -"}`; a finished login prints `"status": "logged_in"` with the profile, user and team. `--callback '<address>'` also works, but the address carries a one-time token and is then visible to other local users in the process list and stays in the shell history.

With a personal access token or a session token instead:

```sh
mm-cli auth login https://chat.example.com --token-stdin < token.txt
```

`mm-cli auth check` shows who is signed in; `mm-cli auth logout` ends the session on the server and deletes the local token.

When you belong to several teams, pass `--team myteam` to `auth login` to make it the default, or `--team` to any command.

## Addressing channels

| Address              | Meaning                                                        |
|----------------------|----------------------------------------------------------------|
| `@me`                | your direct channel with yourself, a place for notes and files |
| `@bob`               | the direct channel with bob, created on first use              |
| `town-square`        | a channel in the default team                                  |
| `myteam/town-square` | a channel in another team                                      |
| 26-character id      | a channel id                                                   |

Resolved ids are cached per profile, so a name costs one request only the first time.

## Examples

```sh
mm-cli send @me "note to self"
mm-cli send @me -f report.pdf -f photo.jpg
echo "multi-line text" | mm-cli send town-square -
mm-cli send town-square "a reply" --reply <post-id>
mm-cli send town-square "standup in 5 minutes" --schedule "2026-11-12 09:55"
mm-cli scheduled list

mm-cli messages town-square -n 50 --since 3d
mm-cli post thread <post-id>
mm-cli thread list --unread
mm-cli thread read <post-id>
mm-cli post search deploy --in town-square --from @bob
mm-cli post edit <post-id> "corrected text"
mm-cli post delete <post-id> --yes

mm-cli file list town-square
mm-cli file download <post-id> -o ~/Downloads
mm-cli file download <file-id> --file -o ~/Downloads

mm-cli channel create notes --private --exist-ok
mm-cli channel list --unread
mm-cli watch town-square @bob
```

Every command is described in [docs/cli.md](docs/cli.md) and in `mm-cli <command> --help`. For AI agents and scripts, `mm-cli help agents` and the other help topics (`addressing`, `output`, `exit-codes`, `login`, `config`, `load`) describe everything needed without this README.

Reading a channel (`messages`, `post thread`, `watch`) does not mark it as read; `mm-cli channel read` does. Likewise `thread list` and `post thread` leave threads unread; `mm-cli thread read` marks them read. Following a thread marks it read on the server.

## Output

Text output shows one entry per message: local time, `@author`, message id, thread details, the text and attachment lines. With `--json` commands print JSON with stable field names:

```json
{
  "id": "...",
  "channel_id": "...",
  "root_id": "",
  "create_at": "2026-01-02T15:04:05Z",
  "user": {"id": "...", "username": "alice"},
  "message": "hello",
  "files": [{"id": "...", "name": "report.pdf", "size": 12345, "mime": "application/pdf"}],
  "reply_count": 0,
  "edited": false,
  "pinned": false
}
```

`watch --json` prints one such object per line, with an extra `channel` field.

Commands that change something without returning the changed object print what they did:

| Command                                | `--json` output                                                                      |
|----------------------------------------|--------------------------------------------------------------------------------------|
| `pin add`, `pin remove`                | `{"action": "pinned"}` or `"unpinned"`, `"id"`: the message id                       |
| `saved add`, `saved remove`            | `{"action": "saved"}` or `"unsaved"`, `"id"`: the message id                         |
| `channel archive`, `unarchive`, `read` | `{"action": "archived"}`, `"unarchived"` or `"marked_read"`, `"id"`: the channel id  |
| `thread follow`, `unfollow`, `read`    | `[{"action": "followed"}, ...]`, `"unfollowed"` or `"marked_read"`, `"id"`: root id  |
| `thread read --all`                    | `[{"action": "marked_read", "team": "<team name>"}, ...]`                            |
| `bookmark delete`                      | `{"action": "deleted", "id": "<bookmark>", "channel_id": "..."}`                     |
| `post delete`                          | `[{"action": "deleted", "id": "<message>"}, ...]`                                    |
| `scheduled delete`                     | `[{"action": "deleted", "id": "<scheduled message>"}, ...]`                          |
| `file replace`                         | `{"action": "replaced", "post_id": "...", "old_file_id": "...", "file_id": "<new>"}` |
| `file remove`                          | `{"action": "removed", "post_id": "...", "file_id": "..."}`                          |
| `file download`                        | `[{"file_id": "...", "path": "./report.pdf", "size": 12345}, ...]`                   |
| `auth logout`                          | `{"action": "logged_out", "profile": "<name>"}`                                      |
| `profile default <name>`               | `{"action": "default_set", "profile": "<name>"}`                                     |
| `profile default`                      | `{"default": "<name>"}`                                                              |
| `profile remove`                       | `{"action": "removed", "profile": "<name>"}`                                         |

When `post delete`, `file download` or a `thread` command fails part-way, stdout still lists what was done before the error.

Errors with `--json` go to stderr as `{"error": {"code": 5, "message": "..."}}`. The JSON format is a public interface: changes are recorded in the [changelog](CHANGELOG.md).

## Exit codes

| Code | Meaning                                    |
|------|--------------------------------------------|
| 0    | success                                    |
| 1    | other error                                |
| 2    | wrong usage: arguments, flags, local files |
| 3    | not signed in, or the session has expired  |
| 4    | forbidden                                  |
| 5    | not found                                  |
| 6    | rate limited by the server after retries   |
| 7    | network error                              |
| 130  | stopped by SIGINT (Ctrl+C)                 |
| 143  | stopped by SIGTERM                         |

## Load on the server

mm-cli is meant to be gentle with shared servers:

- At most 1 request per second with bursts of 3, per process; a profile may change this.
- On `429 Too Many Requests` it waits as the server asks (`Retry-After`, 5 seconds by default), up to 3 times, and halves its own rate; the rate grows back slowly after successful requests and with time.
- A wait the server asks for is never shortened. If it is longer than 2 minutes, the command fails at once with exit code 6 and says until when the server refuses requests.
- The ban and the reduced rate are kept for the profile in `ratelimit.json` in the cache directory, so the next commands honour them too: a new command waits for a short ban (printing `mm-cli: rate limited by the server, waiting Ns` to stderr) or fails with code 6 during a long one.
- Network errors and `502`/`503`/`504` are retried only for reads; sending and deleting are never repeated.
- No polling: `watch` keeps one WebSocket connection and reconnects with a pause of 1, 2, 4 ... up to 60 seconds.
- Author names are fetched in one request per page of messages.

## Configuration

Files live in the user configuration directory (`~/.config/mm-cli` on Linux), or in `$MM_CONFIG_DIR`:

| Path                       | Contents                                       |
|----------------------------|------------------------------------------------|
| `config.yaml`              | profiles: server URL, default team, rate limit |
| `sessions/<profile>.token` | the session token, readable only by you (0600) |

The address cache is kept in the user cache directory (`~/.cache/mm-cli` on Linux), or in `$MM_CONFIG_DIR/cache`. User names in it are checked with the server again after 7 days, so a renamed user shows up under the new name.

```yaml
default: chat-example-com
profiles:
  chat-example-com:
    url: https://chat.example.com
    team: myteam
    rate_limit:        # optional; per_second at most 10, burst at most 20
      per_second: 1
      burst: 3
```

`--profile` or `$MM_PROFILE` selects a profile; `mm-cli profile list` and `mm-cli profile default <name>` manage them.

## Development

```sh
scripts/check.sh          # what CI runs: gofmt, go vet, golangci-lint, tests with -race, govulncheck
scripts/check.sh --fix    # apply formatting and lint fixes, then run the quick checks
scripts/integration.sh    # tests against a real Mattermost in a container
scripts/gen-cli-doc.sh    # regenerate docs/cli.md after changing commands or help text
```

CI regenerates [docs/cli.md](docs/cli.md) and fails if it differs from the committed file, so commit the regenerated reference together with the change. Shell scripts are checked with `shellcheck`, GitHub workflows with `actionlint` and [zizmor](https://docs.zizmor.sh/); `scripts/check.sh` runs zizmor through `uvx` when [uv](https://docs.astral.sh/uv/) is installed.

### Layout

| Package            | Purpose                                                        |
|--------------------|----------------------------------------------------------------|
| `cmd/mm-cli`       | `main`                                                         |
| `internal/cli`     | cobra commands, one file per resource                          |
| `internal/client`  | `model.Client4` with the rate-limiting, 429-aware transport    |
| `internal/auth`    | desktop-app SSO flow                                           |
| `internal/config`  | profiles, session files (mode 0600), pending logins            |
| `internal/resolve` | channel addresses (`@me`, `@user`, `team/name`, id) and cache  |
| `internal/render`  | text and JSON output; JSON field names are a public interface  |
| `internal/clierr`  | exit codes                                                     |
| `internal/fakemm`  | fake Mattermost server for tests                               |

Design decisions are recorded in [docs/adr](docs/adr/README.md).

### Releasing

1. Rename `## [Unreleased]` in [CHANGELOG.md](CHANGELOG.md) to `## [X.Y.Z] - YYYY-MM-DD` and commit.
2. Tag the commit `vX.Y.Z` and push the tag: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. The release workflow first runs the whole CI workflow (lint, tests on Linux, macOS and Windows, integration tests, vulnerability scan, build check) and publishes nothing unless every job passes; then it builds the binaries, attaches `checksums.txt` and `THIRD_PARTY_NOTICES`, and takes the release text from that CHANGELOG section; it fails if the section is missing or empty.

Integration tests start `mattermost/mattermost-preview` in podman (or docker), create users, a team and content on it, and remove the container afterwards. They never talk to any other server.

The repository is public: code, tests and documentation use only made-up data such as `chat.example.com`, `alice`, `bob`, `myteam` and `town-square`. The pre-commit hook checks staged changes against a private list of forbidden strings kept outside the repository (`~/.config/mm-cli-dev/denylist`); enable the hooks with `git config core.hooksPath .githooks`.

## License

[MIT](LICENSE), copyright (c) 2026 Vitaly Ostanin.
