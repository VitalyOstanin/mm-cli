# 0013. Saved connections are servers

- Status: Accepted
- Date: 2026-11-10

## Context

0009 moved the commands for saved connections under `auth`, but the saved entry itself was still called a profile: the `--profile` flag, `$MM_PROFILE`, the `default` and `profiles` keys of `config.yaml`, `sessions/<profile>.token`, `auth login --profile` and the `"profile"` field of JSON output. Meanwhile `mm-cli profile` is the user's profile on the server.

In the Mattermost clients the saved entry is a server: the desktop app has Servers, Add Server, Edit Server, Remove Server and Server Display Name; the mobile app has Edit server and "You are using this name for another server"; `mmctl` names it by a server name (`auth login --name`, `auth set [server name]`) and keeps it internally as credentials. Other candidates are taken: "connection" means a connected workspace of shared channels (Secure Connections), an outgoing OAuth connection or a network connection; "workspace" means a connected workspace; "instance" is the server URL in `mmctl`; `auth` is the command group, and as a flag (`--auth`) it reads as an authentication method next to `--sso`.

## Decision

- A saved connection is a server, named by its server name.
- The global flag is `--server <name>`, the environment variable is `$MM_SERVER`.
- `auth login` takes `--name <name>`, as in `mmctl`; the name still defaults to the host with dots replaced by '-'.
- In `config.yaml` the entries are under `servers:`, and the current one is `current:`, after `auth current`.
- Session files are `sessions/<server name>.token`.
- JSON output names the field `"server"`.
- The `auth` group keeps its commands: `login`, `logout`, `check`, `list`, `current`, `set`, `delete`.

## Consequences

- `profile` has one meaning in mm-cli: the user's profile on the server.
- An existing `config.yaml` is not migrated (see 0010): it is refused with a message to rename the keys `profiles` and `default` to `servers` and `current`, or to log in again. The entries and the session files keep their format, so renaming the keys is enough.
- The help says "name of a saved server, see auth list" wherever `--server` appears, so that it is not taken for a URL.
