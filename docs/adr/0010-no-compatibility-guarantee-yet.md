# 0010. No backward compatibility yet

- Status: Accepted
- Date: 2026-11-09

## Context

mm-cli was published as 0.1.0 a day before this decision. Its commands and JSON output are still taking shape (see 0009), and keeping old names as aliases would add code and help text for users who do not exist yet.

## Decision

Until a later record says otherwise, commands, flags and the JSON output may change without aliases or transition periods. Every such change is listed in the changelog under Changed or Removed.

## Consequences

- Names and output can be corrected as soon as a better choice is found.
- Scripts written against one version may break on the next; the changelog says what to change.
- A record that introduces a compatibility promise supersedes this one, at the latest before 1.0.
