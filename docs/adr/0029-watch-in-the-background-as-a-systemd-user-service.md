# 0029. Watch in the background as a systemd user service

- Status: Accepted
- Date: 2026-11-13

## Context

`watch --reactions --notify` is useful only while it runs, that is, all day in the background. It needs the OS secret store for the token and the session bus for notifications, so it must live inside the user's graphical session. Exit code 3 (no session, or it expired) cannot be fixed by a restart, and exit code 143 (SIGTERM) is a normal stop. Several ways to keep it running were considered.

## Decision

- The supported way on Linux is a systemd user unit bound to `graphical-session.target` (`PartOf=`, `After=`, `WantedBy=`), with `Restart=on-failure`, `RestartSec=30`, `RestartPreventExitStatus=3` and `SuccessExitStatus=143`. The example is `docs/examples/mm-cli-watch.service`; the guide is `docs/watch-in-background.md` (and `.ru.md`).
- `ExecStart=` must hold the absolute path of the binary, taken from `command -v mm-cli`.
- Headless hosts add `Environment=MM_TOKEN_STORAGE=file`.
- macOS (launchd agent) and Windows (Task Scheduler at logon) keep a one-line pointer; no recipes are shipped for them.

Alternatives considered:

- Desktop autostart (`.desktop` file): no restart after a failure, no journal of its own, exit code 3 not told apart from other codes.
- Running from an AI agent session: lives only as long as the session; suitable when the agent reads `--json` itself.
- A transient `systemd-run --user` unit: same properties as the service, but it must be started at every login, and systemd does not expand `%h` in its executable path.

## Consequences

- The unit starts and stops with the graphical session, restarts after failures in 30 seconds, stays stopped after one "sign in again" notification, and keeps its output in the journal.
- Without `SuccessExitStatus=143` every stop and every logout would leave the unit in the `failed` state, so the example carries it.
- After an expired session the user signs in and restarts the unit by hand.
