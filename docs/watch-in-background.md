# Running watch in the background

`mm-cli watch --reactions --notify` shows a desktop notification when
someone reacts to your message. To get them all day, it has to run in the
background. This page describes the recommended way on Linux, a systemd user
service, and the alternatives.

Russian version: [watch-in-background.ru.md](watch-in-background.ru.md).

## Contents

- [What the service gives](#what-the-service-gives)
- [Installation](#installation)
- [Checking that it works](#checking-that-it-works)
- [After an expired session](#after-an-expired-session)
- [Stopping, disabling, removing](#stopping-disabling-removing)
- [Containers and headless hosts](#containers-and-headless-hosts)
- [Other ways](#other-ways)
- [macOS and Windows](#macos-and-windows)

## What the service gives

The example unit is
[docs/examples/mm-cli-watch.service](examples/mm-cli-watch.service).

- It starts with the graphical session and stops at logout
  (`PartOf=` and `WantedBy=graphical-session.target`). The token is read from
  the OS secret store, which is available only in that session.
- After a failure (a crash, a lost network that ends the command) it
  restarts in 30 seconds (`Restart=on-failure`, `RestartSec=30`).
- When the session is missing or expired, mm-cli shows one notification,
  "mm-cli: sign in again", and exits with code 3. The unit does not restart
  on it (`RestartPreventExitStatus=3`): restarting cannot help, and a loop
  would only repeat the notification.
- A normal stop is not a failure: mm-cli exits with 143 on SIGTERM, and
  `SuccessExitStatus=143` keeps the unit out of the `failed` state at every
  `systemctl --user stop` and at every logout.
- The output goes to the journal; `systemctl --user` starts, stops and shows
  the state.

## Installation

From the repository root:

```sh
mkdir -p ~/.config/systemd/user
cp docs/examples/mm-cli-watch.service ~/.config/systemd/user/
command -v mm-cli     # the absolute path of the binary
```

Edit `ExecStart=` in the copied file. systemd needs an absolute path to the
binary. The example assumes `~/.local/bin/mm-cli` (`%h` is the home
directory); `go install` puts the binary in `$(go env GOPATH)/bin` (or
`$GOBIN`), so write the output of `command -v mm-cli` instead. Then:

```sh
systemctl --user daemon-reload
systemctl --user enable --now mm-cli-watch.service
```

Sign in first with `mm-cli auth login`. Add `--messages` to `ExecStart=` to
be notified about new messages too, and `--server <name>` for a server other
than the current one.

## Checking that it works

```sh
systemctl --user status mm-cli-watch.service
journalctl --user -u mm-cli-watch -f
```

The status must be `active (running)`. To see a notification, ask someone to
put a reaction on your message. To try it alone, add `--include-own` to
`ExecStart=`, post a message to yourself (`@me`), react to it and click the
notification: it opens the message in the desktop app. Remove
`--include-own` afterwards. A removed reaction gives no notification
(ADR 0024).

## After an expired session

The unit stays stopped after the notification "mm-cli: sign in again". Run:

```sh
mm-cli auth login
systemctl --user restart mm-cli-watch.service
```

## Stopping, disabling, removing

```sh
systemctl --user stop mm-cli-watch.service      # until the next login
systemctl --user disable --now mm-cli-watch.service
rm ~/.config/systemd/user/mm-cli-watch.service
systemctl --user daemon-reload
```

## Containers and headless hosts

Without a graphical session there is no secret store. Keep the token in a
file: add `Environment=MM_TOKEN_STORAGE=file` to the `[Service]` section
(and sign in with the same variable set). The `graphical-session.target`
lines do not apply there; use `WantedBy=default.target` instead. Without a
notification service on the session bus `--notify` fails with exit code 1,
so on a host without a desktop run `mm-cli watch --reactions` and read its
output from the journal.

## Other ways

| Way                                  | Pros                                  | Cons                                                                                         |
|--------------------------------------|---------------------------------------|----------------------------------------------------------------------------------------------|
| Desktop autostart (`.desktop` file)  | Starts at login; no systemd needed    | No restart after a failure; no journal of its own; exit code 3 is not told apart             |
| From an AI agent session             | Nothing to install                    | Lives only as long as the session; fits when the agent reads `--json` itself (`mm-cli help agents`) |
| Transient unit, `systemd-run --user` | Same properties as the service        | Must be started at every login; the absolute path to the binary is required (`%h` is not expanded) |

## macOS and Windows

On macOS use a launchd agent, on Windows a Task Scheduler task at logon;
both run `mm-cli watch --reactions --notify`.
