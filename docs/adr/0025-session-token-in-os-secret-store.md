# 0025. Session token in the OS secret store

- Status: Accepted; account naming superseded by ADR 0026
- Date: 2026-11-13

## Context

ADR 0005 put the session token of each server in `sessions/<name>.token`, mode 0600, because a keyring dependency would complicate the build. A session token is a bearer credential: anything that reads the file acts as the user until the session expires. A file is exposed to backups, synchronised home directories and other processes of the same user, while macOS Keychain, Windows Credential Manager and the Linux Secret Service (GNOME Keyring, KWallet, KeePassXC) keep secrets encrypted and, for the last one, locked with the login.

The dependency objection no longer holds: `github.com/zalando/go-keyring` needs no cgo. It talks to `/usr/bin/security` on macOS, to Credential Manager through `wincred` on Windows and to the Secret Service over D-Bus (`godbus`, already a dependency) on Linux and the BSDs, so the binaries stay static and cross-compiled.

## Decision

- By default the session token is kept in the OS secret store: service `mm-cli`, account the saved server's name (the key under `servers` in `config.yaml`).
- Where no secret store is available (no D-Bus session, locked or absent collection, a platform the library does not support) mm-cli fails, and never falls back to a file silently. The error names the cause and the two ways out: start or unlock the secret store, or choose files. It is reported with exit code 1 (other error): the command line is right and logging in again would not help, so neither 2 nor 3 fits.
- Files are chosen explicitly, with `token_storage: file` in `config.yaml` or `MM_TOKEN_STORAGE=file`; the environment overrides the file, for containers, CI and headless hosts. The allowed values are `keyring` (default) and `file`; any other value is a configuration error, exit code 2. With `file` the behaviour is that of ADR 0005, unchanged.
- A token file left by an earlier version is moved into the secret store the first time the token is read: the token is written, read back and compared, and only then is the file deleted. Text output prints one line on stderr saying so; `--json` output stays unchanged. If the store is unavailable, or the read-back differs, the file is kept and the error explains the way to keep using it.
- `auth logout` and `auth delete` remove the token from the store and any leftover token file; a token that is not there is not an error. A successful save into the store also removes a leftover file, so that no plain copy remains.
- The one-time client token of a login in progress (`sessions/<name>.pending`) stays a file. It is found by scanning the directory, because the second process of a login does not know the server name; the secret store cannot be searched by value. It is useful only within 3 minutes, is removed when the login ends, and cannot be used without the browser's server token.
- The storage sits behind a small interface in `internal/config` (secret-store and file backends); tests inject an in-memory one and never touch the real store.

## Consequences

- A copied or backed-up configuration directory no longer carries working credentials.
- Headless and container use needs `MM_TOKEN_STORAGE=file` (or the key in `config.yaml`) once, or a running secret service.
- Switching `token_storage` does not move tokens: after changing from `keyring` to `file`, log in again; the token left in the store can be removed with `auth logout`/`auth delete` while it is selected.
- Each token read is a call to the store (a process on macOS, D-Bus on Linux); the wait for a browser login reads local state once a second for at most 3 minutes.
- Windows Credential Manager limits a secret to 2560 bytes; a longer token is refused with the same error and the same two ways out.
- ADR 0005 is superseded as to where the token lives; its file format stays in use in `file` mode.
