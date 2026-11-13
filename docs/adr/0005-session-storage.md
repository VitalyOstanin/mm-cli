# 0005. Session storage

- Status: Superseded by 0025 (the place of the session token)
- Date: 2026-11-09

## Context

After sign-in mm-cli needs the session token on every run. Desktop keyrings are not available everywhere mm-cli runs (servers, containers, CI), and a keyring dependency would complicate the single-binary build.

## Decision

Profiles are kept in `config.yaml` in the user configuration directory (`$MM_CONFIG_DIR` overrides it). The session token of each profile is a separate file, `sessions/<profile>.token`, written atomically with mode 0600 in directories with mode 0700. On Unix, mm-cli refuses to read a token file that other users can read. `config.yaml` holds no secrets. The address cache is kept apart, in the user cache directory.

## Consequences

- Works the same on every platform and in headless environments.
- The token is protected by file permissions only; anyone who can read the user's files can use the session. `mm-cli auth logout` ends the session on the server and deletes the file.
- Several profiles can be signed in at once.
