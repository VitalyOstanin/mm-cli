# 0015. Confirmation without a terminal is --confirm

- Status: Accepted
- Date: 2026-11-10

## Context

Commands that delete or send without undo ask for confirmation on a terminal; without one they needed `-y, --yes`. `mmctl` uses `--confirm` for the same purpose (`post delete`, `channel delete`) and has no short form.

## Decision

- The flag is `--confirm`, without a short form, in every command that asks for confirmation.
- `--yes` and `-y` are removed.

## Consequences

- The flag reads the same as in `mmctl`.
- Scripts that pass `--yes` or `-y` get exit code 2 for an unknown flag (see 0010).
