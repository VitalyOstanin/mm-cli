# 0008. The help is the full reference for agents

- Status: Accepted
- Date: 2026-11-09

## Context

AI agents use mm-cli by reading `--help`; they usually do not read the README. Before this decision only a few commands had a description and examples, and the rules shared by all commands (channel addresses, JSON output, exit codes, sign-in without a terminal, configuration, load limits) were only in the README.

## Decision

- The rules shared by all commands are help topics, shown by `mm-cli help <topic>`: `agents`, `addressing`, `output`, `exit-codes`, `login`, `config`, `load`. The root help stays short and points agents at `mm-cli help agents`.
- Every runnable command has a description (`Long`) with its behaviour, side effects, `--json` output and significant exit codes, and examples (`Example`); every group has a description.
- The help of every command ends with one line pointing at the topics, added by the help template rather than repeated in each command.
- A test walks the command tree and fails when a command lacks a description or examples, when a topic is missing, or when an exit code of `clierr` is not in the `exit-codes` topic.
- `docs/cli.md` is generated from the help and includes the topics.
- Statements in the help are checked against the code; behaviour of the server that is not verified is not described.

## Consequences

- An agent can sign in, read, answer and delete without the README.
- Every new command needs its description and examples before the tests pass.
- The README and the topics overlap; the help is the one kept exact.
