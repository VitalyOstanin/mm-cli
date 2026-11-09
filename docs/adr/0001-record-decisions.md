# 0001. Record decisions as ADRs

- Status: Accepted
- Date: 2026-11-09

## Context

mm-cli makes choices that are not visible in the code alone: why a login flow was chosen, why the client is slow on purpose. Without a record, later changes undo them by accident.

## Decision

Architecture decisions are written as short records in `docs/adr/`, in the format of Michael Nygard: context, decision, consequences. A record's body is not edited after it is accepted, apart from typos and broken links. A changed decision is a new record that supersedes the old one; the old record's status becomes "Superseded by NNNN". `docs/adr/README.md` lists all records with their current status.

## Consequences

- The reasons behind the design stay next to the code and in its history.
- Changing a decision takes a new record, which is a little more work than editing a paragraph.
