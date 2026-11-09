# mm-cli — rules for contributors and AI agents

mm-cli is a command-line client for Mattermost (binary `mm-cli`). This
repository is public.

## Contents

- [No work or personal data](#no-work-or-personal-data)
- [Language](#language)
- [Being gentle with servers](#being-gentle-with-servers)
- [Layout](#layout)
- [Checks](#checks)

## No work or personal data

Never commit real server addresses, people's names or user names, team or
channel names, ids, tokens, message text or home-directory paths — not in code,
tests, fixtures, examples, docs or commit messages.

- Use made-up data only: `chat.example.com`, `alice`, `bob`, `myteam`,
  `town-square`; ids from `fakemm.ID("prefix")`.
- A private list of forbidden strings lives outside the repository
  (`~/.config/mm-cli-dev/denylist`, overridable with `MM_CLI_DENYLIST`).
  `scripts/denylist-check.sh` checks it from the git hooks in `.githooks/`
  (enable once with `git config core.hooksPath .githooks`) and from
  `scripts/check.sh`. Never add the list itself to the repository.

## Language

English is the main language: code, comments, `--help`, error messages, docs,
ADRs, CHANGELOG and commit messages. `README.md` (English) and `README.ru.md`
(Russian) are kept in sync; change both in the same commit.

## Being gentle with servers

- Tests never contact a real server: command tests use `internal/fakemm`,
  integration tests a local Mattermost container.
- Do not raise the default request rate (1 request/s, burst 3) or the retry
  limits in `internal/client`. No polling loops: new messages come through one
  WebSocket connection.
- Bulk operations go one request at a time through the same limiter.

## Layout

| Package            | Purpose                                                        |
|--------------------|----------------------------------------------------------------|
| `cmd/mm-cli`       | `main`                                                         |
| `internal/cli`     | cobra commands, one file per resource                          |
| `internal/client`  | `model.Client4` with the rate-limiting, 429-aware transport    |
| `internal/auth`    | desktop-app SSO flow                                           |
| `internal/config`  | saved servers, session files (mode 0600), pending logins       |
| `internal/resolve` | channel addresses (`@me`, `@user`, `team:name`, id) and cache  |
| `internal/render`  | text and JSON output; JSON field names are a public interface  |
| `internal/clierr`  | exit codes                                                     |
| `internal/fakemm`  | fake Mattermost server for tests                               |

The Mattermost client library (`server/public`) is Apache-2.0. The mmctl
sources are AGPL: read them to learn API behaviour, never copy code from them.

## Checks

```sh
scripts/check.sh --fix   # format and autofix, then lint (quick)
scripts/check.sh --lint  # format check, vet, lint, shellcheck, actionlint, zizmor, denylist
scripts/check.sh         # everything CI runs, including go test -race and govulncheck
```
