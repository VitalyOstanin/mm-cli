# TODO

Known gaps that were left out on purpose, with the reason.

## Contents

- [Release artifacts](#release-artifacts)
- [Login](#login)
- [Channel addresses](#channel-addresses)
- [Terminal output](#terminal-output)
- [Workflows](#workflows)

## Release artifacts

- Sign the binaries and notarize the macOS builds. Until then macOS users remove the quarantine attribute by hand (see the README).
- Attach an SBOM to each release. goreleaser can produce one, but it needs an extra tool (syft) in the release job.

## Login

- Log in with a username and password, with an MFA code when the account has one, as `mmctl auth login --username --password-file --mfa-token` does. Needed only for servers without SSO; the browser login and `--token-stdin` cover the rest. Secrets would come from stdin or a file, never from arguments. Record the decision in an ADR before implementing.
- Change the saved server's default team without logging in again: `mm-cli auth set <name> --team <team>`, checking on the server that the user belongs to the team (exit code 2 otherwise). Today the team is set only by `auth login --team` or by editing `team` in `config.yaml`.

## Channel addresses

- A channel name of 26 lowercase letters and digits is read as a channel id, since ids look the same. When such an id is not found, mm-cli could retry it as a name. This needs a change in the retry logic of `withChannel` and `checkedChannel`; until then such a channel is addressed by its id.

## Terminal output

- Text output replaces control characters from the server but keeps Unicode bidirectional controls (U+202A–U+202E, U+2066–U+2069), which can reorder how a line is displayed. Decide whether to replace them as well.

## Workflows

- `release.yml` calls `ci.yml` as `./.github/workflows/ci.yml` and silences zizmor's `self-repository` audit. Switch to the `$/.github/workflows/ci.yml` form once actionlint accepts it (v1.7.12 does not).
